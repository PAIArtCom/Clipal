package oauth

import (
	"context"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/PAIArtCom/Clipal/internal/outbound"
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

//nolint:staticcheck // The native uTLS adapter needs x/net's already-negotiated connection API.
type anthropicClientConn = http2.ClientConn

type anthropicUTLSRoundTripper struct {
	mu          sync.Mutex
	connections map[string]*anthropicClientConn
	pending     map[string]*sync.Cond
	dialer      net.Dialer
	dialContext outbound.DialContext
	rootCAs     *x509.CertPool
}

func newAnthropicHTTPClient(timeout time.Duration) *http.Client {
	fallback := http.RoundTripper(http.DefaultTransport)
	if transport, ok := http.DefaultTransport.(*http.Transport); ok && transport != nil {
		clone := transport.Clone()
		clone.ForceAttemptHTTP2 = true
		clone.Proxy = outbound.NetworkProxy
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
		connections: make(map[string]*anthropicClientConn),
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

	conn, err := rt.getOrCreateConnection(req.Context(), cacheKey, serverName, addr)
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

func (rt *anthropicUTLSRoundTripper) getOrCreateConnection(ctx context.Context, cacheKey string, serverName string, addr string) (*anthropicClientConn, error) {
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

	conn, err := rt.createConnection(ctx, serverName, addr)

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

func (rt *anthropicUTLSRoundTripper) createConnection(ctx context.Context, serverName string, addr string) (*anthropicClientConn, error) {
	dial := rt.dialContext
	if dial == nil {
		dial = rt.dialer.DialContext
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rawConn, err := dial(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}

	tlsConn := tls.UClient(rawConn, &tls.Config{ServerName: serverName, RootCAs: rt.rootCAs}, tls.HelloChrome_Auto)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = rawConn.Close()
		return nil, err
	}

	//nolint:staticcheck // Reuse the negotiated uTLS connection rather than create another TLS session.
	conn, err := (&http2.Transport{}).NewClientConn(tlsConn)
	if err != nil {
		_ = tlsConn.Close()
		return nil, err
	}
	return conn, nil
}

// Keep the existing Anthropic TLS profile when account DNS supplies the dialer.
func (rt *anthropicFallbackRoundTripper) WithDialContext(dial outbound.DialContext) http.RoundTripper {
	ant := newAnthropicUTLSRoundTripper()
	ant.dialContext = dial
	fallback := (&http.Transport{ForceAttemptHTTP2: true}).Clone()
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		fallback = base.Clone()
	}
	if old, ok := rt.fallback.(*http.Transport); ok {
		fallback = old.Clone()
		if old.TLSClientConfig != nil {
			ant.rootCAs = old.TLSClientConfig.RootCAs
		}
	}
	fallback.Proxy = nil
	fallback.DialTLSContext = nil
	//nolint:staticcheck // Prevent the deprecated hook from bypassing the account dialer.
	fallback.DialTLS = nil
	fallback.DialContext = dial
	return &anthropicFallbackRoundTripper{anthropic: ant, fallback: fallback}
}

func (rt *anthropicFallbackRoundTripper) CloseIdleConnections() {
	if closer, ok := rt.fallback.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
	if ant, ok := rt.anthropic.(*anthropicUTLSRoundTripper); ok {
		ant.mu.Lock()
		defer ant.mu.Unlock()
		for key, conn := range ant.connections {
			if conn.State().StreamsActive == 0 {
				_ = conn.Close()
				delete(ant.connections, key)
			}
		}
	}
}
