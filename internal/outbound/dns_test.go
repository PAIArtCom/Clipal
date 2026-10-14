package outbound

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/http/httpproxy"
)

func TestResolvedIPv6ConnectsWhileIPv4IsStalled(t *testing.T) {
	stalledDone := make(chan struct{})
	base := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
		if address == "192.0.2.1:80" {
			<-ctx.Done()
			close(stalledDone)
			return nil, ctx.Err()
		}
		if address != "[2001:db8::1]:80" {
			t.Errorf("unexpected address %s", address)
			return nil, context.Canceled
		}
		client, server := net.Pipe()
		go func() {
			defer func() { _ = server.Close() }()
			req, err := http.ReadRequest(bufio.NewReader(server))
			if err != nil {
				return
			}
			if req.Host != "example.com" {
				t.Errorf("Host=%s", req.Host)
			}
			_, _ = io.WriteString(server, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok")
		}()
		return client, nil
	}}}
	client, err := WithDNS(base, "auto")
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.com/", nil)
	transport, ok := client.Transport.(*transport)
	if !ok {
		t.Fatal("unexpected DNS transport")
	}
	route, err := transport.route(req)
	if err != nil {
		t.Fatal(err)
	}
	route.resolver.cache["example.com"] = answer{ips: []net.IP{net.ParseIP("192.0.2.1"), net.ParseIP("2001:db8::1")}, until: time.Now().Add(time.Minute), service: "cloudflare"}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("IPv4 stall prevented working IPv6: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	select {
	case <-stalledDone:
	case <-time.After(time.Second):
		t.Fatal("losing IPv4 connection was not cancelled")
	}
}

// Shared local TLS fixtures cover proxy-owned DNS, opt-in public references,
// and direct Cloudflare -> Google resolution without external network access.
func TestRequestDNSAndOptionalReferencesUseEffectiveRoute(t *testing.T) {
	variants := []struct{ scheme, mode string }{}
	for _, scheme := range []string{"http", "https", "socks5", "direct"} {
		for _, mode := range []string{"cloudflare", "google", "auto-primary", "auto-fallback", "system", "auto-both-fail"} {
			variants = append(variants, struct{ scheme, mode string }{scheme, mode})
		}
	}
	for _, variant := range variants {
		scheme, mode := variant.scheme, variant.mode
		t.Run(scheme+"/"+mode, func(t *testing.T) {
			var mu sync.Mutex
			var queries []string
			var tunnels []string
			var services []string
			target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Host != "example.com:443" {
					t.Errorf("Host=%q", r.Host)
				}
				if r.Header.Get("Authorization") != "Bearer model-only" {
					t.Errorf("auth missing from model request")
				}
				_, _ = io.WriteString(w, "ok")
			}))
			defer target.Close()
			doh := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Accept-Language") != "" {
					t.Error("DNS request contains account metadata or secrets")
				}
				mu.Lock()
				services = append(services, r.URL.Path)
				mu.Unlock()
				if (mode == "auto-fallback" && r.URL.Path == "/dns-query") || mode == "auto-both-fail" {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				data, _ := io.ReadAll(r.Body)
				var m dnsmessage.Message
				if err := m.Unpack(data); err != nil {
					t.Error(err)
					return
				}
				q := m.Questions[0]
				mu.Lock()
				queries = append(queries, q.Name.String())
				mu.Unlock()
				m.Response = true
				m.RCode = dnsmessage.RCodeSuccess
				if q.Type == dnsmessage.TypeA {
					m.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: q.Name, Type: q.Type, Class: dnsmessage.ClassINET, TTL: 60}, Body: &dnsmessage.AResource{A: [4]byte{127, 0, 0, 1}}}}
				}
				data, _ = m.Pack()
				w.Header().Set("Content-Type", "application/dns-message")
				_, _ = w.Write(data)
			}))
			defer doh.Close()
			_, port, _ := net.SplitHostPort(strings.TrimPrefix(doh.URL, "https://"))
			dohHost := net.JoinHostPort("example.com", port)
			original := endpoints["cloudflare"]
			endpoints["cloudflare"] = endpoint{url: "https://" + dohHost + "/dns-query", bootstrap: "192.0.2.53"}
			originalGoogle := endpoints["google"]
			endpoints["google"] = endpoint{url: "https://" + dohHost + "/google-dns-query", bootstrap: "192.0.2.54"}
			defer func() { endpoints["cloudflare"] = original; endpoints["google"] = originalGoogle }()
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = listener.Close() }()
			if scheme == "https" {
				listener = tls.NewListener(listener, target.TLS.Clone())
			}
			var workers sync.WaitGroup
			acceptDone := make(chan struct{})
			go func() {
				defer close(acceptDone)
				for {
					conn, err := listener.Accept()
					if err != nil {
						return
					}
					workers.Add(1)
					go func() {
						defer workers.Done()
						defer func() { _ = conn.Close() }()
						reader := bufio.NewReader(conn)
						var address string
						if scheme != "socks5" {
							req, err := http.ReadRequest(reader)
							if err != nil {
								return
							}
							if req.Method != http.MethodConnect {
								t.Errorf("method=%s", req.Method)
								return
							}
							address = req.Host
						} else {
							greeting := make([]byte, 2)
							if _, err := io.ReadFull(reader, greeting); err != nil {
								return
							}
							methods := make([]byte, int(greeting[1]))
							_, _ = io.ReadFull(reader, methods)
							_, _ = conn.Write([]byte{5, 0})
							header := make([]byte, 4)
							_, _ = io.ReadFull(reader, header)
							switch header[3] {
							case 1:
								ip := make([]byte, 4)
								_, _ = io.ReadFull(reader, ip)
								address = net.IP(ip).String()
							case 3:
								n, _ := reader.ReadByte()
								domain := make([]byte, int(n))
								_, _ = io.ReadFull(reader, domain)
								address = string(domain)
							default:
								return
							}
							port := make([]byte, 2)
							_, _ = io.ReadFull(reader, port)
							address = net.JoinHostPort(address, strconv.Itoa(int(binary.BigEndian.Uint16(port))))
						}
						mu.Lock()
						tunnels = append(tunnels, address)
						mu.Unlock()
						upstream := strings.TrimPrefix(target.URL, "https://")
						if address == dohHost {
							upstream = strings.TrimPrefix(doh.URL, "https://")
						} else if address != "example.com:443" {
							t.Errorf("proxy received an unexpected address instead of the original domain: %s", address)
							return
						}
						remote, err := net.Dial("tcp", upstream)
						if err != nil {
							return
						}
						defer func() { _ = remote.Close() }()
						if scheme != "socks5" {
							_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
						} else {
							_, _ = conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
						}
						done := make(chan struct{})
						go func() { _, _ = io.Copy(remote, reader); _ = remote.Close(); close(done) }()
						_, _ = io.Copy(conn, remote)
						_ = conn.Close()
						<-done
					}()
				}
			}()
			proxyURL, _ := url.Parse(scheme + "://" + listener.Addr().String())
			base := target.Client()
			originalTransport, ok := base.Transport.(*http.Transport)
			if !ok {
				t.Fatal("test client transport must be HTTP")
			}
			upstreamTransport := originalTransport.Clone()
			pool := x509.NewCertPool()
			pool.AddCert(target.Certificate())
			pool.AddCert(doh.Certificate())
			upstreamTransport.TLSClientConfig.RootCAs = pool
			upstreamTransport.Proxy = http.ProxyURL(proxyURL)
			if scheme == "direct" {
				upstreamTransport.Proxy = nil
			}
			if scheme == "http" && mode == "auto-both-fail" {
				policy := (&httpproxy.Config{HTTPSProxy: proxyURL.String(), NoProxy: "example.com:444"}).ProxyFunc()
				upstreamTransport.Proxy = func(req *http.Request) (*url.URL, error) { return policy(req.URL) }
			}
			upstreamTransport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				if address == proxyURL.Host {
					return (&net.Dialer{}).DialContext(ctx, network, address)
				}
				mu.Lock()
				tunnels = append(tunnels, address)
				mu.Unlock()
				host, port, _ := net.SplitHostPort(address)
				var mapped string
				switch {
				case host == "192.0.2.53" || host == "192.0.2.54":
					mapped = strings.TrimPrefix(doh.URL, "https://")
				case host == "127.0.0.1" && (port == "443" || port == "444"):
					mapped = strings.TrimPrefix(target.URL, "https://")
				case host == "example.com" && scheme == "direct" && mode == "system":
					mapped = strings.TrimPrefix(target.URL, "https://")
				default:
					t.Errorf("unexpected dial or system target DNS fallback: %s", address)
					return nil, context.Canceled
				}
				return (&net.Dialer{}).DialContext(ctx, network, mapped)
			}
			base.Transport = upstreamTransport
			// Both application and resolver TLS verify the local fixture roots.
			dns := "cloudflare"
			if mode == "system" || mode == "google" {
				dns = mode
			}
			if strings.HasPrefix(mode, "auto-") {
				dns = "auto"
			}
			client, err := WithDNS(base, dns)
			if err != nil {
				t.Fatal(err)
			}
			defer client.CloseIdleConnections()
			shutdown := func() { client.CloseIdleConnections(); _ = listener.Close(); <-acceptDone; workers.Wait() }
			req, _ := http.NewRequest(http.MethodGet, "https://example.com:443/", nil)
			req.Header.Set("Authorization", "Bearer model-only")
			resp, err := client.Do(req)
			if scheme == "direct" && mode == "auto-both-fail" {
				if err == nil {
					_ = resp.Body.Close()
					t.Fatal("direct request bypassed failed DoH")
				}
				shutdown()
				mu.Lock()
				defer mu.Unlock()
				if len(services) != 2 || services[0] != "/dns-query" || services[1] != "/google-dns-query" {
					t.Fatalf("failover=%v", services)
				}
				for _, address := range tunnels {
					host, _, _ := net.SplitHostPort(address)
					if host != "192.0.2.53" && host != "192.0.2.54" {
						t.Fatalf("DNS failure allowed target dial %s", address)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			data, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if string(data) != "ok" {
				t.Fatalf("response=%s", data)
			}
			wantMode := "proxy_hostname"
			if scheme == "direct" {
				wantMode = "resolved_ip"
				if mode == "system" {
					wantMode = "system_dns"
				}
			}
			if got := RouteInfo(client, req.URL.String()).ConnectMode; got != wantMode {
				t.Fatalf("mode=%s want=%s", got, wantMode)
			}
			if scheme != "direct" {
				mu.Lock()
				queryCount := len(services)
				mu.Unlock()
				if queryCount != 0 || ActiveResolver(client, req.URL.String()) != "" {
					t.Fatal("proxy business request made public DoH queries")
				}
			}
			if mode != "system" {
				_, lookupErr := Lookup(context.Background(), client, req.URL.String())
				if mode == "auto-both-fail" {
					if lookupErr == nil {
						t.Fatal("failed optional reference appeared successful")
					}
					// A failed optional query must not gate a subsequent proxy connection.
					client.CloseIdleConnections()
					retry, err := client.Do(req)
					if err != nil {
						t.Fatalf("optional DoH failure blocked proxy request: %v", err)
					}
					_ = retry.Body.Close()
					if retry.StatusCode != http.StatusOK {
						t.Fatal("proxy retry failed")
					}
					if scheme == "http" {
						// The same client respects NO_PROXY per target. This bypassed
						// port now requires DoH, and must fail rather than dial the host.
						bypass, _ := http.NewRequest(http.MethodGet, "https://example.com:444/", nil)
						if p, err := ProxyFor(client, bypass.URL.String()); err != nil || p != nil {
							t.Fatal("NO_PROXY ignored")
						}
						if response, err := client.Do(bypass); err == nil {
							_ = response.Body.Close()
							t.Fatal("NO_PROXY route bypassed failed DoH")
						}
					}
				} else if lookupErr != nil {
					t.Fatal(lookupErr)
				}
			}
			wantService := "cloudflare"
			if mode == "system" || mode == "auto-both-fail" {
				wantService = ""
			}
			if mode == "auto-fallback" || mode == "google" {
				wantService = "google"
			}
			if got := ActiveResolver(client, req.URL.String()); got != wantService {
				t.Fatalf("resolver=%s want=%s", got, wantService)
			}
			shutdown()
			mu.Lock()
			defer mu.Unlock()
			wantQueries := 2
			if mode == "system" || mode == "auto-both-fail" {
				wantQueries = 0
			}
			if mode == "auto-fallback" && (len(services) != 3 || services[0] != "/dns-query" || services[1] != "/google-dns-query") {
				t.Fatalf("resolver priority=%v", services)
			}
			if mode == "auto-primary" && len(services) != 2 {
				t.Fatalf("Google queried despite healthy Cloudflare: %v", services)
			}
			if len(queries) != wantQueries || len(tunnels) < 1 {
				t.Fatalf("queries=%v tunnels=%v", queries, tunnels)
			}
		})
	}
}

func TestDoHFailureDoesNotFallBackToSystemDNS(t *testing.T) {
	var rawDialed bool
	base := &http.Client{Transport: &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr != "8.8.8.8:443" {
			t.Errorf("unexpected direct dial %s", addr)
		}
		rawDialed = true
		return nil, context.DeadlineExceeded
	}}}
	client, err := WithDNS(base, "google")
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://target.invalid/", nil)
	_, err = client.Do(req)
	if err == nil || !rawDialed {
		t.Fatal("expected DoH bootstrap failure")
	}
	encoded, _ := json.Marshal(err.Error())
	if strings.Contains(string(encoded), "lookup target.invalid") {
		t.Fatal("target fell back to system DNS")
	}
}
