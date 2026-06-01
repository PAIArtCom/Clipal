package oauth

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	tls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
)

var anthropicOAuthHosts = map[string]struct{}{
	"api.anthropic.com":   {},
	"platform.claude.com": {},
}

type anthropicFallbackRoundTripper struct {
	anthropic http.RoundTripper
	fallback  http.RoundTripper
}

type anthropicUTLSRoundTripper struct {
	mu          sync.Mutex
	connections map[string]*http2.ClientConn
	pending     map[string]*sync.Cond
	dialer      net.Dialer
}

func newAnthropicHTTPClient(timeout time.Duration) *http.Client {
	fallback := http.RoundTripper(http.DefaultTransport)
	if transport, ok := http.DefaultTransport.(*http.Transport); ok && transport != nil {
		clone := transport.Clone()
		clone.ForceAttemptHTTP2 = true
		fallback = clone
	}

	return &http.Client{
		Timeout: timeout,
		Transport: &anthropicFallbackRoundTripper{
			anthropic: newAnthropicUTLSRoundTripper(),
			fallback:  fallback,
		},
	}
}

func newAnthropicUTLSRoundTripper() *anthropicUTLSRoundTripper {
	return &anthropicUTLSRoundTripper{
		connections: make(map[string]*http2.ClientConn),
		pending:     make(map[string]*sync.Cond),
		dialer: net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		},
	}
}

func (rt *anthropicFallbackRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return nil, fmt.Errorf("request is nil")
	}

	if usesAnthropicUTLSTransport(req) && rt != nil && rt.anthropic != nil {
		return rt.anthropic.RoundTrip(req)
	}
	if rt != nil && rt.fallback != nil {
		return rt.fallback.RoundTrip(req)
	}
	return http.DefaultTransport.RoundTrip(req)
}

func usesAnthropicUTLSTransport(req *http.Request) bool {
	if req == nil || req.URL == nil || !strings.EqualFold(req.URL.Scheme, "https") {
		return false
	}
	_, ok := anthropicOAuthHosts[strings.ToLower(strings.TrimSpace(req.URL.Hostname()))]
	return ok
}

func (rt *anthropicUTLSRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return nil, fmt.Errorf("request is nil")
	}

	cacheKey := strings.ToLower(strings.TrimSpace(req.URL.Host))
	serverName := strings.TrimSpace(req.URL.Hostname())
	addr := strings.TrimSpace(req.URL.Host)
	if serverName == "" || addr == "" {
		return nil, fmt.Errorf("request host is required")
	}
	if !strings.Contains(addr, ":") {
		addr = net.JoinHostPort(addr, "443")
	}

	conn, err := rt.getOrCreateConnection(cacheKey, serverName, addr)
	if err != nil {
		return nil, err
	}

	resp, err := conn.RoundTrip(req)
	if err != nil {
		rt.mu.Lock()
		if cached, ok := rt.connections[cacheKey]; ok && cached == conn {
			delete(rt.connections, cacheKey)
		}
		rt.mu.Unlock()
		return nil, err
	}
	return resp, nil
}

func (rt *anthropicUTLSRoundTripper) getOrCreateConnection(cacheKey string, serverName string, addr string) (*http2.ClientConn, error) {
	rt.mu.Lock()
	if conn, ok := rt.connections[cacheKey]; ok && conn.CanTakeNewRequest() {
		rt.mu.Unlock()
		return conn, nil
	}
	if cond, ok := rt.pending[cacheKey]; ok {
		cond.Wait()
		if conn, ok := rt.connections[cacheKey]; ok && conn.CanTakeNewRequest() {
			rt.mu.Unlock()
			return conn, nil
		}
	}

	cond := sync.NewCond(&rt.mu)
	rt.pending[cacheKey] = cond
	rt.mu.Unlock()

	conn, err := rt.createConnection(serverName, addr)

	rt.mu.Lock()
	defer rt.mu.Unlock()
	delete(rt.pending, cacheKey)
	cond.Broadcast()
	if err != nil {
		return nil, err
	}
	rt.connections[cacheKey] = conn
	return conn, nil
}

func (rt *anthropicUTLSRoundTripper) createConnection(serverName string, addr string) (*http2.ClientConn, error) {
	rawConn, err := rt.dialer.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}

	tlsConn := tls.UClient(rawConn, &tls.Config{ServerName: serverName}, tls.HelloChrome_Auto)
	if err := tlsConn.Handshake(); err != nil {
		_ = rawConn.Close()
		return nil, err
	}

	conn, err := (&http2.Transport{}).NewClientConn(tlsConn)
	if err != nil {
		_ = tlsConn.Close()
		return nil, err
	}
	return conn, nil
}
