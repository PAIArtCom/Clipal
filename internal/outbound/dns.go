// Package outbound supplies account-scoped DNS and proxy connections while
// preserving original HTTP hosts, TLS SNI and certificate verification.
package outbound

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

type DialContext func(context.Context, string, string) (net.Conn, error)
type dialTransport interface {
	WithDialContext(DialContext) http.RoundTripper
}
type endpoint struct{ url, bootstrap string }

var endpoints = map[string]endpoint{
	"cloudflare": {"https://cloudflare-dns.com/dns-query", "1.1.1.1"},
	"google":     {"https://dns.google/dns-query", "8.8.8.8"},
}

type answer struct {
	ips     []net.IP
	until   time.Time
	service string
}
type resolver struct {
	client   *http.Client
	endpoint string
	mu       sync.Mutex
	cache    map[string]answer
	fallback *resolver
	service  string
}

func newRouteResolver(dns string, route *url.URL, dial DialContext, roots *x509.CertPool, domain bool) *resolver {
	if dns == "auto" {
		primary := newRouteResolver("cloudflare", route, dial, roots, domain)
		primary.fallback = newRouteResolver("google", route, dial, roots, domain)
		return primary
	}
	e := endpoints[dns]
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		base = &http.Transport{ForceAttemptHTTP2: true}
	}
	t := base.Clone()
	t.Proxy = nil
	t.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		// All standard proxy routes retain the service domain for DNS/routing.
		// Other routes bootstrap by fixed IP; TLS verification is unchanged.
		bootstrap := e.bootstrap
		if domain {
			bootstrap = host
		}
		return dialRoute(ctx, net.JoinHostPort(bootstrap, port), route, dial, roots)
	}
	return &resolver{client: &http.Client{Transport: t, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, endpoint: e.url, cache: map[string]answer{}, service: dns}
}

func (r *resolver) lookupService(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	var ips []net.IP
	ttl := uint32(300)
	for _, typ := range []dnsmessage.Type{dnsmessage.TypeA, dnsmessage.TypeAAAA} {
		name, err := dnsmessage.NewName(host + ".")
		if err != nil {
			return nil, fmt.Errorf("invalid DNS name")
		}
		msg := dnsmessage.Message{Header: dnsmessage.Header{RecursionDesired: true}, Questions: []dnsmessage.Question{{Name: name, Type: typ, Class: dnsmessage.ClassINET}}}
		wire, err := msg.Pack()
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(wire))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/dns-message")
		req.Header.Set("Accept", "application/dns-message")
		req.Header["User-Agent"] = nil
		resp, err := r.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("account DoH connection failed")
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 65537))
		_ = resp.Body.Close()
		if readErr != nil || len(data) > 65536 || resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("account DoH returned an invalid response")
		}
		var result dnsmessage.Message
		if err := result.Unpack(data); err != nil || !result.Response || result.RCode != dnsmessage.RCodeSuccess || len(result.Questions) != 1 || result.Questions[0] != msg.Questions[0] {
			return nil, fmt.Errorf("account DoH lookup failed")
		}
		for _, a := range result.Answers {
			if a.Header.TTL < ttl {
				ttl = a.Header.TTL
			}
			switch value := a.Body.(type) {
			case *dnsmessage.AResource:
				ips = append(ips, net.IP(append([]byte(nil), value.A[:]...)))
			case *dnsmessage.AAAAResource:
				ips = append(ips, net.IP(append([]byte(nil), value.AAAA[:]...)))
			}
		}
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("account DoH returned no addresses")
	}
	r.mu.Lock()
	if len(r.cache) >= 256 {
		r.cache = map[string]answer{}
	}
	r.cache[host] = answer{ips: ips, until: time.Now().Add(time.Duration(ttl) * time.Second), service: r.service}
	r.mu.Unlock()
	return ips, nil
}

type transport struct {
	base       http.RoundTripper
	dns        string
	dial       DialContext
	proxy      func(*http.Request) (*url.URL, error)
	mu         sync.Mutex
	routes     map[string]*routeTransport
	proxyRoots *x509.CertPool
}
type routeTransport struct {
	rt       http.RoundTripper
	resolver *resolver
}

// WithDNS leaves existing clients unchanged for system DNS. A DoH client has
// independent pools and caches; callers should reuse it for the account/route.
func WithDNS(client *http.Client, dns string) (*http.Client, error) {
	return Configure(client, dns, nil)
}

func Configure(client *http.Client, dns string, alternative http.RoundTripper) (*http.Client, error) {
	if (dns == "" || dns == "system") && alternative == nil {
		return client, nil
	}
	if _, ok := endpoints[dns]; !ok && dns != "" && dns != "system" && dns != "auto" {
		return nil, fmt.Errorf("unsupported account DNS service")
	}
	if client == nil {
		client = &http.Client{}
	}
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	var proxy func(*http.Request) (*url.URL, error)
	var roots *x509.CertPool
	dial := DialContext((&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext)
	if t, ok := base.(*http.Transport); ok {
		proxy = t.Proxy
		if t.TLSClientConfig != nil {
			roots = t.TLSClientConfig.RootCAs
		}
		if t.DialContext != nil {
			dial = t.DialContext
		}
	} else if _, ok := base.(dialTransport); !ok {
		return nil, fmt.Errorf("transport does not support account DNS")
	}
	if alternative != nil {
		base = alternative
	}
	copy := *client
	copy.Transport = &transport{base: base, dns: dns, dial: dial, proxy: proxy, routes: map[string]*routeTransport{}, proxyRoots: roots}
	return &copy, nil
}

func (t *transport) route(req *http.Request) (*routeTransport, error) {
	var proxy *url.URL
	var err error
	if t.proxy != nil {
		proxy, err = t.proxy(req)
		if err != nil {
			return nil, fmt.Errorf("account proxy policy failed")
		}
	}
	key := "direct"
	if proxy != nil {
		key = proxy.String()
	}
	domain := proxy != nil
	if domain {
		key += "/hostname"
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if route := t.routes[key]; route != nil {
		return route, nil
	}
	var r *resolver
	if t.dns != "" && t.dns != "system" {
		r = newRouteResolver(t.dns, proxy, t.dial, t.proxyRoots, domain)
	}
	dial := DialContext(func(ctx context.Context, network, addr string) (net.Conn, error) {
		// Standard proxies own target resolution. Public DoH is only queried by
		// an explicit diagnostic Lookup; it never gates a proxy connection.
		if proxy != nil || r == nil {
			return dialRoute(ctx, addr, proxy, t.dial, t.proxyRoots)
		}
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := r.lookup(ctx, host)
		if err != nil {
			return nil, err
		}
		return dialResolved(ctx, ips, port, func(ctx context.Context, addr string) (net.Conn, error) {
			return dialRoute(ctx, addr, proxy, t.dial, t.proxyRoots)
		})
	})
	var rt http.RoundTripper
	if base, ok := t.base.(*http.Transport); ok {
		clone := base.Clone()
		clone.Proxy = nil
		clone.DialTLSContext = nil
		//nolint:staticcheck // Clear the deprecated hook too, so it cannot bypass account DNS.
		clone.DialTLS = nil
		clone.DialContext = dial
		rt = clone
	} else {
		custom, ok := t.base.(dialTransport)
		if !ok {
			return nil, fmt.Errorf("unsupported account transport")
		}
		rt = custom.WithDialContext(dial)
	}
	result := &routeTransport{rt: rt, resolver: r}
	t.routes[key] = result
	return result, nil
}
func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	route, err := t.route(req)
	if err != nil {
		return nil, err
	}
	return route.rt.RoundTrip(req)
}
func (t *transport) CloseIdleConnections() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, r := range t.routes {
		if closer, ok := r.rt.(interface{ CloseIdleConnections() }); ok {
			closer.CloseIdleConnections()
		}
		if r.resolver != nil {
			r.resolver.closeIdleConnections()
		}
	}
}

// Lookup explicitly queries public DoH on the selected target entry. Through a
// proxy its answers are reference data, not the proxy's actual DNS answers.
func Lookup(ctx context.Context, client *http.Client, target string) ([]net.IP, error) {
	t, ok := client.Transport.(*transport)
	if !ok {
		return nil, fmt.Errorf("account DoH is not enabled")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	route, err := t.route(req)
	if err != nil {
		return nil, err
	}
	if route.resolver == nil {
		return nil, fmt.Errorf("account DoH is not enabled")
	}
	return route.resolver.lookup(ctx, req.URL.Hostname())
}

func (r *resolver) lookup(ctx context.Context, host string) ([]net.IP, error) {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	r.mu.Lock()
	cached := r.cache[host]
	r.mu.Unlock()
	if time.Now().Before(cached.until) {
		return cached.ips, nil
	}
	primaryTimeout := 5 * time.Second
	if deadline, ok := ctx.Deadline(); ok && r.fallback != nil {
		if remaining := time.Until(deadline) / 2; remaining < primaryTimeout {
			primaryTimeout = remaining
		}
	}
	queryCtx, cancel := context.WithTimeout(ctx, primaryTimeout)
	ips, err := r.lookupService(queryCtx, host)
	cancel()
	if err == nil || r.fallback == nil {
		return ips, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	queryCtx, cancel = context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ips, err = r.fallback.lookupService(queryCtx, host)
	if err != nil {
		return nil, fmt.Errorf("cloudflare and google account DoH both failed")
	}
	r.fallback.mu.Lock()
	fallback := r.fallback.cache[host]
	r.fallback.mu.Unlock()
	r.mu.Lock()
	r.cache[host] = fallback
	r.mu.Unlock()
	return ips, nil
}
func (r *resolver) closeIdleConnections() {
	r.client.CloseIdleConnections()
	if r.fallback != nil {
		r.fallback.closeIdleConnections()
	}
}
func ActiveResolver(client *http.Client, target string) string {
	t, ok := client.Transport.(*transport)
	if !ok {
		return ""
	}
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return ""
	}
	route, err := t.route(req)
	if err != nil || route.resolver == nil {
		return ""
	}
	route.resolver.mu.Lock()
	defer route.resolver.mu.Unlock()
	return route.resolver.cache[strings.ToLower(req.URL.Hostname())].service
}
