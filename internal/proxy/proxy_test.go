package proxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
	"github.com/lansespirit/Clipal/internal/logger"
	oauthpkg "github.com/lansespirit/Clipal/internal/oauth"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

type trackingReadCloser struct {
	readCalled  bool
	closeCalled bool
}

func (r *trackingReadCloser) Read(_ []byte) (int, error) {
	r.readCalled = true
	return 0, errors.New("body should not be read")
}

func (r *trackingReadCloser) Close() error {
	r.closeCalled = true
	return nil
}

type flushCountingResponseWriter struct {
	header  http.Header
	body    bytes.Buffer
	status  int
	flushes int
}

func (w *flushCountingResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *flushCountingResponseWriter) Write(p []byte) (int, error) {
	return w.body.Write(p)
}

func (w *flushCountingResponseWriter) WriteHeader(statusCode int) {
	w.status = statusCode
}

func (w *flushCountingResponseWriter) Flush() {
	w.flushes++
}

func newResponse(status int, hdr http.Header, body string) *http.Response {
	if hdr == nil {
		hdr = make(http.Header)
	}
	return &http.Response{
		StatusCode: status,
		Header:     hdr,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

const testResponseHeaderTimeout = 2 * time.Minute

func TestPathPrefixMatchAndStrip(t *testing.T) {
	t.Parallel()

	if !pathMatchesPrefix("/claudecode", "/claudecode") {
		t.Fatalf("expected exact prefix match")
	}
	if !pathMatchesPrefix("/claudecode/v1/messages", "/claudecode") {
		t.Fatalf("expected subpath prefix match")
	}
	if pathMatchesPrefix("/claudecodeX/v1/messages", "/claudecode") {
		t.Fatalf("expected non-match for similar prefix")
	}

	if got := stripClientPrefix("/claudecode", "/claudecode"); got != "/" {
		t.Fatalf("expected '/', got %q", got)
	}
	if got := stripClientPrefix("/claudecode/", "/claudecode"); got != "/" {
		t.Fatalf("expected '/', got %q", got)
	}
	if got := stripClientPrefix("/claudecode/v1/messages", "/claudecode"); got != "/v1/messages" {
		t.Fatalf("expected '/v1/messages', got %q", got)
	}
}

func TestRouterRefreshDueOAuthProvidersRefreshesEnabledProviders(t *testing.T) {
	now := time.Now().UTC()
	var refreshCalls int
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"access-2","refresh_token":"refresh-2","token_type":"Bearer","expires_in":3600,"account":{"uuid":"acct_123","email_address":"sean@example.com"},"organization":{"uuid":"org_123","name":"Example"}}`)
	}))
	defer tokenServer.Close()

	dir := t.TempDir()
	svc := oauthpkg.NewService(dir, oauthpkg.WithClaudeClient(&oauthpkg.ClaudeClient{
		TokenURL:   tokenServer.URL,
		ClientID:   "test-client",
		HTTPClient: tokenServer.Client(),
		Now:        func() time.Time { return now },
	}))
	if err := svc.Store().Save(&oauthpkg.Credential{
		Ref:          "claude-sean-example-com",
		Provider:     config.OAuthProviderClaude,
		Email:        "sean@example.com",
		AccountID:    "acct_123",
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		ExpiresAt:    now.Add(20 * time.Minute),
		LastRefresh:  now.Add(-80 * time.Minute),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	provider := config.Provider{
		Name:          "claude-oauth",
		AuthType:      config.ProviderAuthTypeOAuth,
		OAuthProvider: config.OAuthProviderClaude,
		OAuthRef:      "claude-sean-example-com",
		Priority:      1,
	}
	router := &Router{
		oauth: svc,
		proxies: map[ClientType]*ClientProxy{
			ClientClaude: newClientProxy(ClientClaude, config.ClientModeAuto, "", []config.Provider{provider}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{}),
		},
	}

	router.refreshDueOAuthProviders()

	if refreshCalls != 1 {
		t.Fatalf("refresh calls = %d, want 1", refreshCalls)
	}
	refreshed, err := svc.Store().Load(config.OAuthProviderClaude, "claude-sean-example-com")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := refreshed.AccessToken; got != "access-2" {
		t.Fatalf("access token = %q, want refreshed token", got)
	}
}

func TestRouterOAuthRefreshMaintainerStopCancelsInFlightRefresh(t *testing.T) {
	now := time.Now().UTC()
	started := make(chan struct{})
	release := make(chan struct{})
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-started:
		default:
			close(started)
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer tokenServer.Close()

	dir := t.TempDir()
	svc := oauthpkg.NewService(dir, oauthpkg.WithClaudeClient(&oauthpkg.ClaudeClient{
		TokenURL:   tokenServer.URL,
		ClientID:   "test-client",
		HTTPClient: tokenServer.Client(),
		Now:        func() time.Time { return now },
	}))
	if err := svc.Store().Save(&oauthpkg.Credential{
		Ref:          "claude-sean-example-com",
		Provider:     config.OAuthProviderClaude,
		Email:        "sean@example.com",
		AccountID:    "acct_123",
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		ExpiresAt:    now.Add(20 * time.Minute),
		LastRefresh:  now.Add(-80 * time.Minute),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	provider := config.Provider{
		Name:          "claude-oauth",
		AuthType:      config.ProviderAuthTypeOAuth,
		OAuthProvider: config.OAuthProviderClaude,
		OAuthRef:      "claude-sean-example-com",
		Priority:      1,
	}
	router := &Router{
		oauth:             svc,
		oauthRefreshEvery: time.Hour,
		proxies: map[ClientType]*ClientProxy{
			ClientClaude: newClientProxy(ClientClaude, config.ClientModeAuto, "", []config.Provider{provider}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{}),
		},
	}

	router.startOAuthRefreshMaintainer()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("refresh did not start")
	}

	stopped := make(chan struct{})
	go func() {
		router.stopOAuthRefreshMaintainer()
		close(stopped)
	}()
	select {
	case <-stopped:
		close(release)
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("stopOAuthRefreshMaintainer did not cancel in-flight refresh")
	}
}

func TestResponseBodyWriterFlushesOnlyStreamingResponses(t *testing.T) {
	t.Parallel()

	plainWriter := &flushCountingResponseWriter{}
	plainResp := newResponse(http.StatusOK, http.Header{"Content-Type": []string{"application/json"}}, "")
	plainBody := responseBodyWriter(plainWriter, httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/test", nil), plainResp)
	if _, err := plainBody.Write([]byte("a")); err != nil {
		t.Fatalf("plain write: %v", err)
	}
	if _, err := plainBody.Write([]byte("b")); err != nil {
		t.Fatalf("plain write: %v", err)
	}
	if plainWriter.flushes != 0 {
		t.Fatalf("plain flushes: got %d want 0", plainWriter.flushes)
	}

	streamWriter := &flushCountingResponseWriter{}
	streamResp := newResponse(http.StatusOK, http.Header{"Content-Type": []string{"text/event-stream"}}, "")
	streamBody := responseBodyWriter(streamWriter, httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/responses", nil), streamResp)
	if _, err := streamBody.Write([]byte("a")); err != nil {
		t.Fatalf("stream write: %v", err)
	}
	if _, err := streamBody.Write([]byte("b")); err != nil {
		t.Fatalf("stream write: %v", err)
	}
	if streamWriter.flushes != 2 {
		t.Fatalf("stream flushes: got %d want 2", streamWriter.flushes)
	}
}

func TestBuildTargetURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		baseURL   string
		path      string
		rawQuery  string
		wantURL   string
		wantError bool
	}{
		{
			name:    "scheme-present",
			baseURL: "https://api.anthropic.com",
			path:    "/v1/messages",
			wantURL: "https://api.anthropic.com/v1/messages",
		},
		{
			name:    "scheme-missing-defaults-to-https",
			baseURL: "api.anthropic.com",
			path:    "/v1/messages",
			wantURL: "https://api.anthropic.com/v1/messages",
		},
		{
			name:    "base-path-joined",
			baseURL: "https://example.com/api",
			path:    "/v1/messages",
			wantURL: "https://example.com/api/v1/messages",
		},
		{
			name:    "deduplicates-terminal-v1-prefix",
			baseURL: "https://example.com/v1",
			path:    "/v1/responses",
			wantURL: "https://example.com/v1/responses",
		},
		{
			name:    "deduplicates-terminal-v1beta-prefix",
			baseURL: "https://example.com/v1beta",
			path:    "/v1beta/models/gemini-2.5-pro:generateContent",
			wantURL: "https://example.com/v1beta/models/gemini-2.5-pro:generateContent",
		},
		{
			name:    "deduplicates-terminal-upload-v1beta-prefix",
			baseURL: "https://example.com/upload/v1beta",
			path:    "/upload/v1beta/files",
			wantURL: "https://example.com/upload/v1beta/files",
		},
		{
			name:     "query-preserved",
			baseURL:  "https://example.com",
			path:     "/v1/messages",
			rawQuery: "a=1&b=2",
			wantURL:  "https://example.com/v1/messages?a=1&b=2",
		},
		{
			name:      "empty-base",
			baseURL:   "   ",
			path:      "/v1/messages",
			wantError: true,
			wantURL:   "",
			rawQuery:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := buildTargetURL(tc.baseURL, tc.path, tc.rawQuery)
			if tc.wantError {
				if err == nil {
					t.Fatalf("expected error, got nil (url=%q)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantURL {
				t.Fatalf("unexpected url: got %q want %q", got, tc.wantURL)
			}
		})
	}
}

func TestNewClientProxy_UsesProviderSpecificProxyModes(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://env-proxy:8080")

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "default", BaseURL: "http://default.example", APIKey: "k1", Priority: 1},
		{Name: "direct", BaseURL: "http://direct.example", APIKey: "k2", ProxyMode: config.ProviderProxyModeDirect, Priority: 2},
		{Name: "custom", BaseURL: "http://custom.example", APIKey: "k3", ProxyMode: config.ProviderProxyModeCustom, ProxyURL: "http://custom-proxy:9090", Priority: 3},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})

	req, err := http.NewRequest(http.MethodGet, "http://upstream.example/v1/test", nil)
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}

	defaultTransport, ok := cp.upstreamHTTPClient(0).Transport.(*http.Transport)
	if !ok {
		t.Fatalf("default transport type = %T", cp.upstreamHTTPClient(0).Transport)
	}
	defaultProxy, err := defaultTransport.Proxy(req)
	if err != nil {
		t.Fatalf("default proxy: %v", err)
	}
	if defaultProxy == nil || defaultProxy.String() != "http://env-proxy:8080" {
		t.Fatalf("default proxy = %v, want http://env-proxy:8080", defaultProxy)
	}

	directTransport, ok := cp.upstreamHTTPClient(1).Transport.(*http.Transport)
	if !ok {
		t.Fatalf("direct transport type = %T", cp.upstreamHTTPClient(1).Transport)
	}
	if directTransport.Proxy != nil {
		directProxy, err := directTransport.Proxy(req)
		if err != nil {
			t.Fatalf("direct proxy: %v", err)
		}
		if directProxy != nil {
			t.Fatalf("direct proxy = %v, want nil", directProxy)
		}
	}

	customTransport, ok := cp.upstreamHTTPClient(2).Transport.(*http.Transport)
	if !ok {
		t.Fatalf("custom transport type = %T", cp.upstreamHTTPClient(2).Transport)
	}
	customProxy, err := customTransport.Proxy(req)
	if err != nil {
		t.Fatalf("custom proxy: %v", err)
	}
	if customProxy == nil || customProxy.String() != "http://custom-proxy:9090" {
		t.Fatalf("custom proxy = %v, want http://custom-proxy:9090", customProxy)
	}
}

func TestNewClientProxyWithGlobalProxy_UsesGlobalDefaultForDefaultProviders(t *testing.T) {
	t.Parallel()

	req, err := http.NewRequest(http.MethodGet, "http://upstream.example/v1/test", nil)
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}

	directCP := newClientProxyWithGlobalProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "default", BaseURL: "http://default.example", APIKey: "k1", Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{}, config.GlobalUpstreamProxyModeDirect, "")

	directTransport, ok := directCP.upstreamHTTPClient(0).Transport.(*http.Transport)
	if !ok {
		t.Fatalf("direct transport type = %T", directCP.upstreamHTTPClient(0).Transport)
	}
	if directTransport.Proxy != nil {
		directProxy, err := directTransport.Proxy(req)
		if err != nil {
			t.Fatalf("direct proxy: %v", err)
		}
		if directProxy != nil {
			t.Fatalf("direct proxy = %v, want nil", directProxy)
		}
	}

	customCP := newClientProxyWithGlobalProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "default", BaseURL: "http://default.example", APIKey: "k1", Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{}, config.GlobalUpstreamProxyModeCustom, "http://global-proxy:8081")

	customTransport, ok := customCP.upstreamHTTPClient(0).Transport.(*http.Transport)
	if !ok {
		t.Fatalf("custom transport type = %T", customCP.upstreamHTTPClient(0).Transport)
	}
	customProxy, err := customTransport.Proxy(req)
	if err != nil {
		t.Fatalf("custom proxy: %v", err)
	}
	if customProxy == nil || customProxy.String() != "http://global-proxy:8081" {
		t.Fatalf("custom proxy = %v, want http://global-proxy:8081", customProxy)
	}
}

func TestNewClientProxyWithGlobalProxy_SharesHTTPClientsByEffectivePolicy(t *testing.T) {
	t.Parallel()

	cp := newClientProxyWithGlobalProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "default-a", BaseURL: "http://default-a.example", APIKey: "k1", Priority: 1},
		{Name: "default-b", BaseURL: "http://default-b.example", APIKey: "k2", Priority: 2},
		{Name: "direct-a", BaseURL: "http://direct-a.example", APIKey: "k3", ProxyMode: config.ProviderProxyModeDirect, Priority: 3},
		{Name: "direct-b", BaseURL: "http://direct-b.example", APIKey: "k4", ProxyMode: config.ProviderProxyModeDirect, Priority: 4},
		{Name: "custom-a", BaseURL: "http://custom-a.example", APIKey: "k5", ProxyMode: config.ProviderProxyModeCustom, ProxyURL: "http://custom-proxy:9090", Priority: 5},
		{Name: "custom-b", BaseURL: "http://custom-b.example", APIKey: "k6", ProxyMode: config.ProviderProxyModeCustom, ProxyURL: "http://custom-proxy:9090", Priority: 6},
		{Name: "custom-c", BaseURL: "http://custom-c.example", APIKey: "k7", ProxyMode: config.ProviderProxyModeCustom, ProxyURL: "http://other-proxy:9090", Priority: 7},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{}, config.GlobalUpstreamProxyModeEnvironment, "")

	if cp.upstreamHTTPClient(0) != cp.upstreamHTTPClient(1) {
		t.Fatalf("default providers should share the environment client")
	}
	if cp.upstreamHTTPClient(2) != cp.upstreamHTTPClient(3) {
		t.Fatalf("direct providers should share the direct client")
	}
	if cp.upstreamHTTPClient(4) != cp.upstreamHTTPClient(5) {
		t.Fatalf("matching custom proxy URLs should share the same client")
	}
	if cp.upstreamHTTPClient(4) == cp.upstreamHTTPClient(6) {
		t.Fatalf("different custom proxy URLs should not share the same client")
	}
	if cp.upstreamHTTPClient(0) == cp.upstreamHTTPClient(2) {
		t.Fatalf("environment and direct policies should not share the same client")
	}
}

func TestNewClientProxyWithGlobalProxy_SharesHTTPClientsAcrossEquivalentCustomProxyForms(t *testing.T) {
	t.Parallel()

	cp := newClientProxyWithGlobalProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "plain", BaseURL: "http://plain.example", APIKey: "k1", ProxyMode: config.ProviderProxyModeCustom, ProxyURL: "http://proxy.example", Priority: 1},
		{Name: "slash", BaseURL: "http://slash.example", APIKey: "k2", ProxyMode: config.ProviderProxyModeCustom, ProxyURL: "http://proxy.example/", Priority: 2},
		{Name: "query", BaseURL: "http://query.example", APIKey: "k3", ProxyMode: config.ProviderProxyModeCustom, ProxyURL: "http://proxy.example?x=1", Priority: 3},
		{Name: "userinfo", BaseURL: "http://userinfo.example", APIKey: "k4", ProxyMode: config.ProviderProxyModeCustom, ProxyURL: "http://USER:PASS@PROXY.EXAMPLE:80/path", Priority: 4},
		{Name: "userinfo-plain", BaseURL: "http://userinfo-plain.example", APIKey: "k5", ProxyMode: config.ProviderProxyModeCustom, ProxyURL: "http://USER:PASS@proxy.example", Priority: 5},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{}, config.GlobalUpstreamProxyModeEnvironment, "")

	if cp.upstreamHTTPClient(0) != cp.upstreamHTTPClient(1) || cp.upstreamHTTPClient(0) != cp.upstreamHTTPClient(2) {
		t.Fatalf("equivalent authority-only custom proxy forms should share the same client")
	}
	if cp.upstreamHTTPClient(3) != cp.upstreamHTTPClient(4) {
		t.Fatalf("equivalent custom proxy forms with matching userinfo should share the same client")
	}
	if cp.upstreamHTTPClient(0) == cp.upstreamHTTPClient(3) {
		t.Fatalf("different proxy identities should not share the same client")
	}
}

func TestNewClientProxy_UsesCustomSocksProxy(t *testing.T) {
	t.Parallel()

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "custom-socks", BaseURL: "http://custom.example", APIKey: "k1", ProxyMode: config.ProviderProxyModeCustom, ProxyURL: "socks5://custom-proxy:1080", Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})

	req, err := http.NewRequest(http.MethodGet, "https://upstream.example/v1/test", nil)
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}

	transport, ok := cp.upstreamHTTPClient(0).Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T", cp.upstreamHTTPClient(0).Transport)
	}

	customProxy, err := transport.Proxy(req)
	if err != nil {
		t.Fatalf("custom proxy: %v", err)
	}
	if customProxy == nil || customProxy.String() != "socks5://custom-proxy" {
		t.Fatalf("custom proxy = %v, want socks5://custom-proxy", customProxy)
	}
}

func TestAddForwardedHeaders(t *testing.T) {
	t.Parallel()

	original, err := http.NewRequest(http.MethodPost, "http://localhost/claudecode/v1/messages", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	original.RemoteAddr = "1.2.3.4:5678"
	original.Host = "example.com:3333"
	original.TLS = &tls.ConnectionState{}

	proxyReq, err := http.NewRequest(http.MethodPost, "https://api.example.com/v1/messages", nil)
	if err != nil {
		t.Fatalf("new proxy request: %v", err)
	}

	addForwardedHeaders(proxyReq, original)

	if got := proxyReq.Header.Get("X-Forwarded-For"); got != "1.2.3.4" {
		t.Fatalf("X-Forwarded-For: got %q want %q", got, "1.2.3.4")
	}
	if got := proxyReq.Header.Get("X-Forwarded-Proto"); got != "https" {
		t.Fatalf("X-Forwarded-Proto: got %q want %q", got, "https")
	}
	if got := proxyReq.Header.Get("X-Forwarded-Host"); got != "example.com:3333" {
		t.Fatalf("X-Forwarded-Host: got %q want %q", got, "example.com:3333")
	}
	if got := proxyReq.Header.Get("X-Forwarded-Port"); got != "3333" {
		t.Fatalf("X-Forwarded-Port: got %q want %q", got, "3333")
	}
}

func TestCreateProxyRequest_PreservesClaudeXAPIKeyStyle(t *testing.T) {
	t.Parallel()

	cp := newClientProxy(ClientClaude, config.ClientModeAuto, "", []config.Provider{
		{Name: "claude", BaseURL: "https://api.anthropic.com", APIKey: "provider-key", Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})

	original := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/messages", bytes.NewReader([]byte(`{"x":1}`)))
	original.Header.Set("x-api-key", "client-key")
	original = withRequestContext(original, RequestContext{
		ClientType:     ClientClaude,
		Family:         ProtocolFamilyClaude,
		Capability:     CapabilityClaudeMessages,
		UpstreamPath:   "/v1/messages",
		UnifiedIngress: true,
	})

	proxyReq, err := cp.createProxyRequest(original, cp.providers[0], "provider-key", "/v1/messages", []byte(`{"x":1}`))
	if err != nil {
		t.Fatalf("createProxyRequest: %v", err)
	}
	if got := proxyReq.Header.Get("x-api-key"); got != "provider-key" {
		t.Fatalf("x-api-key: got %q want %q", got, "provider-key")
	}
	if got := proxyReq.Header.Get("Authorization"); got != "" {
		t.Fatalf("Authorization: got %q want empty", got)
	}
	if got := proxyReq.Header.Get("x-goog-api-key"); got != "" {
		t.Fatalf("x-goog-api-key: got %q want empty", got)
	}
}

func TestCreateProxyRequest_UsesGeminiXGoogAPIKeyStyle(t *testing.T) {
	t.Parallel()

	cp := newClientProxy(ClientGemini, config.ClientModeAuto, "", []config.Provider{
		{Name: "gemini", BaseURL: "https://generativelanguage.googleapis.com", APIKey: "provider-key", Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})

	original := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1beta/models/gemini-2.5-pro:generateContent", bytes.NewReader([]byte(`{"contents":[]}`)))
	original.Header.Set("x-goog-api-key", "client-key")
	original = withRequestContext(original, RequestContext{
		ClientType:     ClientGemini,
		Family:         ProtocolFamilyGemini,
		Capability:     CapabilityGeminiGenerateContent,
		UpstreamPath:   "/v1beta/models/gemini-2.5-pro:generateContent",
		UnifiedIngress: true,
	})

	proxyReq, err := cp.createProxyRequest(original, cp.providers[0], "provider-key", "/v1beta/models/gemini-2.5-pro:generateContent", []byte(`{"contents":[]}`))
	if err != nil {
		t.Fatalf("createProxyRequest: %v", err)
	}
	if got := proxyReq.Header.Get("x-goog-api-key"); got != "provider-key" {
		t.Fatalf("x-goog-api-key: got %q want %q", got, "provider-key")
	}
	if got := proxyReq.Header.Get("Authorization"); got != "" {
		t.Fatalf("Authorization: got %q want empty", got)
	}
	if got := proxyReq.Header.Get("x-api-key"); got != "" {
		t.Fatalf("x-api-key: got %q want empty", got)
	}
}

func TestCreateProxyRequest_UsesBearerForVertexGeminiProvider(t *testing.T) {
	t.Parallel()

	cp := newClientProxy(ClientGemini, config.ClientModeAuto, "", []config.Provider{
		{Name: "vertex", BaseURL: "https://us-central1-aiplatform.googleapis.com", APIKey: "provider-token", Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})

	vertexPath := "/v1/projects/proj/locations/us-central1/publishers/google/models/gemini-2.5-pro:generateContent"
	original := httptest.NewRequest(http.MethodPost, "http://proxy/clipal"+vertexPath, bytes.NewReader([]byte(`{"contents":[]}`)))
	original.Header.Set("x-goog-api-key", "client-key")
	original = withRequestContext(original, RequestContext{
		ClientType:     ClientGemini,
		Family:         ProtocolFamilyGemini,
		Capability:     CapabilityGeminiGenerateContent,
		UpstreamPath:   vertexPath,
		UnifiedIngress: true,
	})

	proxyReq, err := cp.createProxyRequest(original, cp.providers[0], "provider-token", vertexPath, []byte(`{"contents":[]}`))
	if err != nil {
		t.Fatalf("createProxyRequest: %v", err)
	}
	if got := proxyReq.Header.Get("Authorization"); got != "Bearer provider-token" {
		t.Fatalf("Authorization: got %q want %q", got, "Bearer provider-token")
	}
	if got := proxyReq.Header.Get("x-goog-api-key"); got != "" {
		t.Fatalf("x-goog-api-key: got %q want empty", got)
	}
	if got := proxyReq.URL.String(); got != "https://us-central1-aiplatform.googleapis.com"+vertexPath {
		t.Fatalf("url: got %q", got)
	}
}

func TestApplyProviderAPIKey_UnknownCarrierFallsBackToProtocolDefault(t *testing.T) {
	original := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/messages", nil)
	original = withRequestContext(original, RequestContext{
		ClientType:     ClientClaude,
		Family:         ProtocolFamilyClaude,
		Capability:     CapabilityClaudeMessages,
		UpstreamPath:   "/v1/messages",
		UnifiedIngress: true,
	})

	proxyReq := httptest.NewRequest(http.MethodPost, "http://upstream/v1/messages", nil)
	proxyReq.Header.Set("Authorization", "Bearer stale")

	detectAuthCarrierFuncMu.Lock()
	origDetect := detectAuthCarrierFunc
	detectAuthCarrierFunc = func(*http.Request) authCarrier { return authCarrier("future-carrier") }
	detectAuthCarrierFuncMu.Unlock()
	defer func() {
		detectAuthCarrierFuncMu.Lock()
		detectAuthCarrierFunc = origDetect
		detectAuthCarrierFuncMu.Unlock()
	}()

	applyProviderAPIKey(proxyReq, original, "provider-key")

	if got := proxyReq.Header.Get("x-api-key"); got != "provider-key" {
		t.Fatalf("x-api-key: got %q want %q", got, "provider-key")
	}
	if got := proxyReq.Header.Get("Authorization"); got != "" {
		t.Fatalf("Authorization: got %q want empty", got)
	}
}

func TestRegisterExactAndSubtreeRegistersExpectedServeMuxMatches(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	var hits int
	registerExactAndSubtree(mux, "/clipal", func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantHits   int
	}{
		{name: "exact", path: "/clipal", wantStatus: http.StatusNoContent, wantHits: 1},
		{name: "subtree", path: "/clipal/v1/messages", wantStatus: http.StatusNoContent, wantHits: 2},
		{name: "neighbor prefix", path: "/clipalx", wantStatus: http.StatusNotFound, wantHits: 2},
	}

	for _, tt := range tests {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "http://proxy"+tt.path, nil)
		mux.ServeHTTP(rr, req)
		if rr.Result().StatusCode != tt.wantStatus {
			t.Fatalf("%s status: got %d want %d", tt.name, rr.Result().StatusCode, tt.wantStatus)
		}
		if hits != tt.wantHits {
			t.Fatalf("%s hits: got %d want %d", tt.name, hits, tt.wantHits)
		}
	}
}

func TestCreateProxyRequest_PreservesExplicitAuthorizationCarrier(t *testing.T) {
	t.Parallel()

	cp := newClientProxy(ClientGemini, config.ClientModeAuto, "", []config.Provider{
		{Name: "gemini-gateway", BaseURL: "https://gateway.example.com", APIKey: "provider-key", Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})

	original := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1beta/models/gemini-2.5-pro:generateContent", bytes.NewReader([]byte(`{"contents":[]}`)))
	original.Header.Set("Authorization", "Bearer client-key")
	original = withRequestContext(original, RequestContext{
		ClientType:     ClientGemini,
		Family:         ProtocolFamilyGemini,
		Capability:     CapabilityGeminiGenerateContent,
		UpstreamPath:   "/v1beta/models/gemini-2.5-pro:generateContent",
		UnifiedIngress: true,
	})

	proxyReq, err := cp.createProxyRequest(original, cp.providers[0], "provider-key", "/v1beta/models/gemini-2.5-pro:generateContent", []byte(`{"contents":[]}`))
	if err != nil {
		t.Fatalf("createProxyRequest: %v", err)
	}
	if got := proxyReq.Header.Get("Authorization"); got != "Bearer provider-key" {
		t.Fatalf("Authorization: got %q want %q", got, "Bearer provider-key")
	}
	if got := proxyReq.Header.Get("x-goog-api-key"); got != "" {
		t.Fatalf("x-goog-api-key: got %q want empty", got)
	}
}

func TestCreateProxyRequest_OverridesGeminiQueryKey(t *testing.T) {
	t.Parallel()

	cp := newClientProxy(ClientGemini, config.ClientModeAuto, "", []config.Provider{
		{Name: "gemini", BaseURL: "https://generativelanguage.googleapis.com", APIKey: "provider-key", Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})

	original := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1beta/models/gemini-2.5-pro:generateContent?key=client-key&alt=sse", bytes.NewReader([]byte(`{"contents":[]}`)))
	original = withRequestContext(original, RequestContext{
		ClientType:     ClientGemini,
		Family:         ProtocolFamilyGemini,
		Capability:     CapabilityGeminiGenerateContent,
		UpstreamPath:   "/v1beta/models/gemini-2.5-pro:generateContent",
		UnifiedIngress: true,
	})

	proxyReq, err := cp.createProxyRequest(original, cp.providers[0], "provider-key", "/v1beta/models/gemini-2.5-pro:generateContent", []byte(`{"contents":[]}`))
	if err != nil {
		t.Fatalf("createProxyRequest: %v", err)
	}
	if got := proxyReq.URL.Query().Get("key"); got != "provider-key" {
		t.Fatalf("query key: got %q want %q", got, "provider-key")
	}
	if got := proxyReq.URL.Query().Get("alt"); got != "sse" {
		t.Fatalf("query alt: got %q want %q", got, "sse")
	}
	if got := proxyReq.Header.Get("Authorization"); got != "" {
		t.Fatalf("Authorization: got %q want empty", got)
	}
}

func TestCreateProxyRequest_DefaultsClaudeAuthCarrierWithoutClientAuth(t *testing.T) {
	t.Parallel()

	cp := newClientProxy(ClientClaude, config.ClientModeAuto, "", []config.Provider{
		{Name: "claude", BaseURL: "https://api.anthropic.com", APIKey: "provider-key", Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})

	original := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/messages", bytes.NewReader([]byte(`{"x":1}`)))
	original = withRequestContext(original, RequestContext{
		ClientType:     ClientClaude,
		Family:         ProtocolFamilyClaude,
		Capability:     CapabilityClaudeMessages,
		UpstreamPath:   "/v1/messages",
		UnifiedIngress: true,
	})

	proxyReq, err := cp.createProxyRequest(original, cp.providers[0], "provider-key", "/v1/messages", []byte(`{"x":1}`))
	if err != nil {
		t.Fatalf("createProxyRequest: %v", err)
	}
	if got := proxyReq.Header.Get("x-api-key"); got != "provider-key" {
		t.Fatalf("x-api-key: got %q want %q", got, "provider-key")
	}
	if got := proxyReq.Header.Get("Authorization"); got != "" {
		t.Fatalf("Authorization: got %q want empty", got)
	}
}

func TestCreateProxyRequest_DefaultsGeminiAuthCarrierWithoutClientAuth(t *testing.T) {
	t.Parallel()

	cp := newClientProxy(ClientGemini, config.ClientModeAuto, "", []config.Provider{
		{Name: "gemini", BaseURL: "https://generativelanguage.googleapis.com", APIKey: "provider-key", Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})

	original := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1beta/models/gemini-2.5-flash-image:generateContent", bytes.NewReader([]byte(`{"contents":[]}`)))
	original = withRequestContext(original, RequestContext{
		ClientType:     ClientGemini,
		Family:         ProtocolFamilyGemini,
		Capability:     CapabilityGeminiGenerateContent,
		UpstreamPath:   "/v1beta/models/gemini-2.5-flash-image:generateContent",
		UnifiedIngress: true,
	})

	proxyReq, err := cp.createProxyRequest(original, cp.providers[0], "provider-key", "/v1beta/models/gemini-2.5-flash-image:generateContent", []byte(`{"contents":[]}`))
	if err != nil {
		t.Fatalf("createProxyRequest: %v", err)
	}
	if got := proxyReq.Header.Get("x-goog-api-key"); got != "provider-key" {
		t.Fatalf("x-goog-api-key: got %q want %q", got, "provider-key")
	}
	if got := proxyReq.Header.Get("Authorization"); got != "" {
		t.Fatalf("Authorization: got %q want empty", got)
	}
}

func TestIsUpstreamIdleTimeout(t *testing.T) {
	t.Parallel()

	attemptCtx, cancelAttempt := context.WithCancelCause(context.Background())
	cancelAttempt(errUpstreamIdleTimeout)

	if !isUpstreamIdleTimeout(attemptCtx, nil) {
		t.Fatalf("expected idle timeout via context.Cause with nil err")
	}
	if !isUpstreamIdleTimeout(attemptCtx, attemptCtx.Err()) {
		t.Fatalf("expected idle timeout via attemptCtx.Err()")
	}
	if !isUpstreamIdleTimeout(attemptCtx, errUpstreamIdleTimeout) {
		t.Fatalf("expected idle timeout via sentinel error")
	}

	otherCtx, cancelOther := context.WithCancelCause(context.Background())
	cancelOther(nil)
	if isUpstreamIdleTimeout(otherCtx, nil) {
		t.Fatalf("did not expect idle timeout for generic cancellation")
	}
}

func TestHandleRequest_MaxRequestBodyBytes(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Global: config.GlobalConfig{
			ListenAddr:      "127.0.0.1",
			Port:            3333,
			LogLevel:        config.LogLevelDebug,
			ReactivateAfter: "1h",
			MaxRequestBody:  8,
		},
		OpenAI: config.ClientConfig{
			Providers: []config.Provider{
				{Name: "p1", BaseURL: "http://example.com", APIKey: "k1", Priority: 1},
			},
		},
	}

	router := NewRouter(cfg)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/test", bytes.NewReader([]byte("123456789")))
	router.handleRequest(rr, req)

	if rr.Result().StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status: got %d want %d", rr.Result().StatusCode, http.StatusRequestEntityTooLarge)
	}
}

func TestForwardWithFailover_DeactivateOn401(t *testing.T) {
	t.Parallel()

	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "p1":
			h := make(http.Header)
			h.Set("Content-Type", "application/json")
			return newResponse(http.StatusUnauthorized, h, `{"error":{"type":"authentication_error","code":"invalid_api_key","message":"bad key"}}`), nil
		case "p2":
			return newResponse(http.StatusOK, nil, "ok"), nil
		default:
			return nil, errors.New("unexpected host")
		}
	})

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
		{Name: "p2", BaseURL: "http://p2", APIKey: "k2", Priority: 2},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.httpClient.Transport = rt

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/test", bytes.NewReader([]byte(`{"x":1}`)))
	cp.forwardWithFailover(rr, req, "/v1/test")

	res := rr.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d", res.StatusCode, http.StatusOK)
	}
	if got := rr.Body.String(); got != "ok" {
		t.Fatalf("body: got %q want %q", got, "ok")
	}
	if !cp.isDeactivated(0) {
		t.Fatalf("expected provider 0 to be deactivated")
	}
	if got := cp.getCurrentIndex(); got != 1 {
		t.Fatalf("currentIndex: got %d want %d", got, 1)
	}
}

func TestForwardWithFailover_AllUnavailableDoesNotReadBody(t *testing.T) {
	t.Parallel()

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.deactivateFor(0, "rate_limit", http.StatusTooManyRequests, "limited", time.Hour)

	body := &trackingReadCloser{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/test", nil)
	req.Body = body

	cp.forwardWithFailover(rr, req, "/v1/test")

	if body.readCalled {
		t.Fatalf("expected unavailable request to return before reading body")
	}
	if rr.Result().StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status: got %d want %d", rr.Result().StatusCode, http.StatusTooManyRequests)
	}
}

func TestForwardWithFailover_RetryOn503DoesNotDeactivate(t *testing.T) {
	t.Parallel()

	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "p1":
			return newResponse(http.StatusServiceUnavailable, nil, "down"), nil
		case "p2":
			return newResponse(http.StatusOK, nil, "ok"), nil
		default:
			return nil, errors.New("unexpected host")
		}
	})

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
		{Name: "p2", BaseURL: "http://p2", APIKey: "k2", Priority: 2},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.httpClient.Transport = rt

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/test", bytes.NewReader([]byte(`{"x":1}`)))
	cp.forwardWithFailover(rr, req, "/v1/test")

	res := rr.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d", res.StatusCode, http.StatusOK)
	}
	if got := rr.Body.String(); got != "ok" {
		t.Fatalf("body: got %q want %q", got, "ok")
	}
	if cp.isDeactivated(0) {
		t.Fatalf("expected provider 0 NOT to be deactivated")
	}
}

func TestForwardWithFailover_ContextCanceledDoesNotRetryOtherProviders(t *testing.T) {
	t.Parallel()

	var calls int
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		<-r.Context().Done()
		return nil, r.Context().Err()
	})

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
		{Name: "p2", BaseURL: "http://p2", APIKey: "k2", Priority: 2},
		{Name: "p3", BaseURL: "http://p3", APIKey: "k3", Priority: 3},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.httpClient.Transport = rt

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/test", bytes.NewReader([]byte(`{"x":1}`))).WithContext(ctx)
	cp.forwardWithFailover(rr, req, "/v1/test")

	if calls != 1 {
		t.Fatalf("expected only 1 upstream attempt after context canceled, got %d", calls)
	}
}

func TestForwardWithFailover_IdleTimeoutBeforeFirstByteRetriesNextProvider(t *testing.T) {
	t.Parallel()

	var callsP1 int
	var callsP2 int

	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "p1":
			callsP1++
			body := io.NopCloser(readerFunc(func(p []byte) (int, error) {
				<-r.Context().Done()
				return 0, r.Context().Err()
			}))
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body}, nil
		case "p2":
			callsP2++
			return newResponse(http.StatusOK, nil, "ok"), nil
		default:
			return nil, errors.New("unexpected host")
		}
	})

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
		{Name: "p2", BaseURL: "http://p2", APIKey: "k2", Priority: 2},
	}, time.Hour, 10*time.Millisecond, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.httpClient.Transport = rt

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/test", bytes.NewReader([]byte(`{"x":1}`)))
	cp.forwardWithFailover(rr, req, "/v1/test")

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d", rr.Result().StatusCode, http.StatusOK)
	}
	if got := rr.Body.String(); got != "ok" {
		t.Fatalf("body: got %q want %q", got, "ok")
	}
	if callsP1 != 1 || callsP2 != 1 {
		t.Fatalf("unexpected upstream call counts: p1=%d p2=%d", callsP1, callsP2)
	}
	if got := cp.getCurrentIndex(); got != 1 {
		t.Fatalf("currentIndex: got %d want %d", got, 1)
	}
}

func TestForwardWithFailover_PartialStreamDoesNotLogCompleted(t *testing.T) {
	var (
		logMu    sync.Mutex
		messages []string
	)
	logger.SetHook(func(_ string, message string) {
		if strings.Contains(message, "streamerr") {
			logMu.Lock()
			messages = append(messages, message)
			logMu.Unlock()
		}
	})
	t.Cleanup(func() {
		logger.SetHook(nil)
	})

	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "streamerr" {
			return nil, errors.New("unexpected host")
		}
		reads := 0
		body := io.NopCloser(readerFunc(func(p []byte) (int, error) {
			reads++
			if reads == 1 {
				copy(p, []byte("chunk"))
				return len("chunk"), nil
			}
			return 0, errors.New("upstream stream broke")
		}))
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       body,
		}, nil
	})

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "streamerr", BaseURL: "http://streamerr", APIKey: "k1", Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.httpClient.Transport = rt

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/test", bytes.NewReader([]byte(`{"x":1}`)))
	cp.forwardWithFailover(rr, req, "/v1/test")

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d", rr.Result().StatusCode, http.StatusOK)
	}
	if got := rr.Body.String(); got != "chunk" {
		t.Fatalf("body: got %q want %q", got, "chunk")
	}

	logMu.Lock()
	defer logMu.Unlock()

	var sawCopyFailure bool
	for _, msg := range messages {
		if strings.Contains(msg, "Completed via streamerr") {
			t.Fatalf("did not expect completion log, got %q", msg)
		}
		if strings.Contains(msg, "response copy failed via streamerr") {
			sawCopyFailure = true
		}
	}
	if !sawCopyFailure {
		t.Fatalf("expected response copy failure log for streamerr, got %v", messages)
	}
}

func TestProtocolTrackerDetectsCompletionMarkers(t *testing.T) {
	openAIResp := &http.Response{Header: http.Header{"Content-Type": []string{"text/event-stream"}}}
	openAITracker := newProtocolTracker(ClientOpenAI, nil, openAIResp)
	openAITracker.append([]byte("event: response.completed\ndata: {\"type\":\"response.completed\"}\n\n"))
	if got := openAITracker.finalStatus(); got != protocolCompleted {
		t.Fatalf("openai finalStatus: got %s want %s", got, protocolCompleted)
	}

	claudeResp := &http.Response{Header: http.Header{"Content-Type": []string{"text/event-stream"}}}
	claudeTracker := newProtocolTracker(ClientClaude, nil, claudeResp)
	claudeTracker.append([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	if got := claudeTracker.finalStatus(); got != protocolCompleted {
		t.Fatalf("claude finalStatus: got %s want %s", got, protocolCompleted)
	}
}

func TestForwardWithFailover_CodexSSEDoneLogsCompleted(t *testing.T) {
	var (
		logMu    sync.Mutex
		messages []string
	)
	logger.SetHook(func(_ string, message string) {
		if strings.Contains(message, "ssedone") {
			logMu.Lock()
			messages = append(messages, message)
			logMu.Unlock()
		}
	})
	t.Cleanup(func() {
		logger.SetHook(nil)
	})

	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "ssedone" {
			return nil, errors.New("unexpected host")
		}
		reads := 0
		body := io.NopCloser(readerFunc(func(p []byte) (int, error) {
			reads++
			switch reads {
			case 1:
				copy(p, []byte("data: {\"type\":\"response.output_text.delta\"}\n\n"))
				return len("data: {\"type\":\"response.output_text.delta\"}\n\n"), nil
			case 2:
				copy(p, []byte("data: [DONE]\n\n"))
				return len("data: [DONE]\n\n"), nil
			default:
				return 0, io.EOF
			}
		}))
		h := make(http.Header)
		h.Set("Content-Type", "text/event-stream")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     h,
			Body:       body,
		}, nil
	})

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "ssedone", BaseURL: "http://ssedone", APIKey: "k1", Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.httpClient.Transport = rt

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/responses", bytes.NewReader([]byte(`{"stream":true}`)))
	cp.forwardWithFailover(rr, req, "/v1/responses")

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d", rr.Result().StatusCode, http.StatusOK)
	}

	logMu.Lock()
	defer logMu.Unlock()
	for _, msg := range messages {
		if strings.Contains(msg, "Completed via ssedone") {
			return
		}
	}
	t.Fatalf("expected completion log for ssedone, got %v", messages)
}

func TestForwardWithFailover_CodexSSEEOFWithoutDoneLogsIncomplete(t *testing.T) {
	var (
		logMu    sync.Mutex
		messages []string
	)
	logger.SetHook(func(_ string, message string) {
		if strings.Contains(message, "sseincomplete") {
			logMu.Lock()
			messages = append(messages, message)
			logMu.Unlock()
		}
	})
	t.Cleanup(func() {
		logger.SetHook(nil)
	})

	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "sseincomplete" {
			return nil, errors.New("unexpected host")
		}
		reads := 0
		body := io.NopCloser(readerFunc(func(p []byte) (int, error) {
			reads++
			switch reads {
			case 1:
				copy(p, []byte("data: {\"type\":\"response.output_text.delta\"}\n\n"))
				return len("data: {\"type\":\"response.output_text.delta\"}\n\n"), nil
			default:
				return 0, io.EOF
			}
		}))
		h := make(http.Header)
		h.Set("Content-Type", "text/event-stream")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     h,
			Body:       body,
		}, nil
	})

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "sseincomplete", BaseURL: "http://sseincomplete", APIKey: "k1", Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.httpClient.Transport = rt

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/responses", bytes.NewReader([]byte(`{"stream":true}`)))
	cp.forwardWithFailover(rr, req, "/v1/responses")

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d", rr.Result().StatusCode, http.StatusOK)
	}

	logMu.Lock()
	defer logMu.Unlock()

	var sawIncomplete bool
	for _, msg := range messages {
		if strings.Contains(msg, "Completed via sseincomplete") {
			t.Fatalf("did not expect completion log, got %q", msg)
		}
		if strings.Contains(msg, "Incomplete response via sseincomplete") {
			sawIncomplete = true
		}
	}
	if !sawIncomplete {
		t.Fatalf("expected incomplete stream log for sseincomplete, got %v", messages)
	}
}

func TestClaudeCountTokens_FailureDoesNotRetryOrAffectHealthState(t *testing.T) {
	t.Parallel()

	var srv1Count int
	var srv2Count int

	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "p1":
			srv1Count++
			return newResponse(http.StatusServiceUnavailable, nil, "down"), nil
		case "p2":
			srv2Count++
			return newResponse(http.StatusOK, nil, "ok"), nil
		default:
			return nil, errors.New("unexpected host")
		}
	})

	cfg := &config.Config{
		Global: config.GlobalConfig{
			ListenAddr:      "127.0.0.1",
			Port:            3333,
			LogLevel:        config.LogLevelDebug,
			ReactivateAfter: "1h",
		},
		Claude: config.ClientConfig{
			Providers: []config.Provider{
				{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
				{Name: "p2", BaseURL: "http://p2", APIKey: "k2", Priority: 2},
			},
		},
	}

	router := NewRouter(cfg)
	cp := router.proxies[ClientClaude]
	if cp == nil {
		t.Fatalf("expected claudecode proxy to be initialized")
	}
	if got := cp.getCurrentIndex(); got != 0 {
		t.Fatalf("currentIndex: got %d want %d", got, 0)
	}
	cp.httpClient.Transport = rt

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/claudecode/v1/messages/count_tokens", bytes.NewReader([]byte(`{"x":1}`)))
	router.handleRequest(rr, req)

	res := rr.Result()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d want %d", res.StatusCode, http.StatusServiceUnavailable)
	}
	if got := strings.TrimSpace(rr.Body.String()); got != "down" {
		t.Fatalf("body: got %q want %q", got, "down")
	}
	if got := cp.getCurrentIndex(); got != 0 {
		t.Fatalf("currentIndex changed by count_tokens: got %d want %d", got, 0)
	}
	if got := cp.countTokensIndex; got != 0 {
		t.Fatalf("countTokensIndex changed by count_tokens: got %d want %d", got, 0)
	}
	if cp.isDeactivated(0) {
		t.Fatalf("provider 0 should not be deactivated by count_tokens")
	}
	if cp.breakers[0].state != circuitClosed {
		t.Fatalf("breaker state changed by count_tokens: got %s want %s", cp.breakers[0].state, circuitClosed)
	}
	if srv1Count != 1 || srv2Count != 0 {
		t.Fatalf("unexpected request counts: srv1=%d srv2=%d", srv1Count, srv2Count)
	}

	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "http://proxy/claudecode/v1/messages/count_tokens", bytes.NewReader([]byte(`{"x":2}`)))
	router.handleRequest(rr2, req2)

	res2 := rr2.Result()
	if res2.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status2: got %d want %d", res2.StatusCode, http.StatusServiceUnavailable)
	}
	if got := strings.TrimSpace(rr2.Body.String()); got != "down" {
		t.Fatalf("body2: got %q want %q", got, "down")
	}
	if got := cp.getCurrentIndex(); got != 0 {
		t.Fatalf("currentIndex changed by count_tokens (second): got %d want %d", got, 0)
	}
	if got := cp.countTokensIndex; got != 0 {
		t.Fatalf("countTokensIndex changed by count_tokens (second): got %d want %d", got, 0)
	}
	if cp.isDeactivated(0) {
		t.Fatalf("provider 0 should still not be deactivated by count_tokens")
	}
	if cp.breakers[0].state != circuitClosed {
		t.Fatalf("breaker state changed by second count_tokens: got %s want %s", cp.breakers[0].state, circuitClosed)
	}
	if srv1Count != 2 || srv2Count != 0 {
		t.Fatalf("unexpected request counts after second call: srv1=%d srv2=%d", srv1Count, srv2Count)
	}
}

func TestClaudeCountTokens_NetworkFailureDoesNotRetryOrAffectHealthState(t *testing.T) {
	t.Parallel()

	var srv1Count int
	var srv2Count int

	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "p1":
			srv1Count++
			return nil, errors.New("dial failed")
		case "p2":
			srv2Count++
			return newResponse(http.StatusOK, nil, "ok"), nil
		default:
			return nil, errors.New("unexpected host")
		}
	})

	cfg := &config.Config{
		Global: config.GlobalConfig{
			ListenAddr:      "127.0.0.1",
			Port:            3333,
			LogLevel:        config.LogLevelDebug,
			ReactivateAfter: "1h",
		},
		Claude: config.ClientConfig{
			Providers: []config.Provider{
				{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
				{Name: "p2", BaseURL: "http://p2", APIKey: "k2", Priority: 2},
			},
		},
	}

	router := NewRouter(cfg)
	cp := router.proxies[ClientClaude]
	if cp == nil {
		t.Fatalf("expected claudecode proxy to be initialized")
	}
	cp.httpClient.Transport = rt

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/claudecode/v1/messages/count_tokens", bytes.NewReader([]byte(`{"x":1}`)))
	router.handleRequest(rr, req)

	if rr.Result().StatusCode != http.StatusBadGateway {
		t.Fatalf("status: got %d want %d", rr.Result().StatusCode, http.StatusBadGateway)
	}
	if got := cp.getCurrentIndex(); got != 0 {
		t.Fatalf("currentIndex changed by count_tokens network failure: got %d want %d", got, 0)
	}
	if got := cp.countTokensIndex; got != 0 {
		t.Fatalf("countTokensIndex changed by count_tokens network failure: got %d want %d", got, 0)
	}
	if cp.isDeactivated(0) {
		t.Fatalf("provider 0 should not be deactivated by count_tokens network failure")
	}
	if cp.breakers[0].state != circuitClosed {
		t.Fatalf("breaker state changed by count_tokens network failure: got %s want %s", cp.breakers[0].state, circuitClosed)
	}
	if srv1Count != 1 || srv2Count != 0 {
		t.Fatalf("unexpected request counts: srv1=%d srv2=%d", srv1Count, srv2Count)
	}
}

func TestGeminiCountTokens_DoesNotRetryOrAffectHealthState(t *testing.T) {
	t.Parallel()

	var srv1Count int
	var srv2Count int

	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "p1":
			srv1Count++
			return newResponse(http.StatusServiceUnavailable, nil, "down"), nil
		case "p2":
			srv2Count++
			return newResponse(http.StatusOK, nil, "ok"), nil
		default:
			return nil, errors.New("unexpected host")
		}
	})

	cfg := &config.Config{
		Global: config.GlobalConfig{
			ListenAddr:      "127.0.0.1",
			Port:            3333,
			LogLevel:        config.LogLevelDebug,
			ReactivateAfter: "1h",
		},
		Gemini: config.ClientConfig{
			Providers: []config.Provider{
				{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
				{Name: "p2", BaseURL: "http://p2", APIKey: "k2", Priority: 2},
			},
		},
	}

	router := NewRouter(cfg)
	cp := router.proxies[ClientGemini]
	if cp == nil {
		t.Fatalf("expected gemini proxy to be initialized")
	}
	cp.httpClient.Transport = rt

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/gemini/v1beta/models/gemini-2.0-flash:countTokens", bytes.NewReader([]byte(`{"contents":[]}`)))
	router.handleRequest(rr, req)

	res := rr.Result()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d want %d", res.StatusCode, http.StatusServiceUnavailable)
	}
	if got := strings.TrimSpace(rr.Body.String()); got != "down" {
		t.Fatalf("body: got %q want %q", got, "down")
	}
	if got := cp.getCurrentIndex(); got != 0 {
		t.Fatalf("currentIndex changed by gemini countTokens: got %d want %d", got, 0)
	}
	if cp.isDeactivated(0) {
		t.Fatalf("provider 0 should not be deactivated by gemini countTokens")
	}
	if cp.breakers[0].state != circuitClosed {
		t.Fatalf("breaker state changed by gemini countTokens: got %s want %s", cp.breakers[0].state, circuitClosed)
	}
	if srv1Count != 1 || srv2Count != 0 {
		t.Fatalf("unexpected request counts: srv1=%d srv2=%d", srv1Count, srv2Count)
	}
}

func TestClaudeCountTokens_SkipsUnavailableCurrentProvider(t *testing.T) {
	t.Parallel()

	var srv1Count int
	var srv2Count int

	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "p1":
			srv1Count++
			return newResponse(http.StatusOK, nil, "p1"), nil
		case "p2":
			srv2Count++
			return newResponse(http.StatusOK, nil, "p2"), nil
		default:
			return nil, errors.New("unexpected host")
		}
	})

	cfg := &config.Config{
		Global: config.GlobalConfig{
			ListenAddr:      "127.0.0.1",
			Port:            3333,
			LogLevel:        config.LogLevelDebug,
			ReactivateAfter: "1h",
			CircuitBreaker: config.CircuitBreakerConfig{
				FailureThreshold:    1,
				SuccessThreshold:    1,
				OpenTimeout:         "1h",
				HalfOpenMaxInFlight: 1,
			},
		},
		Claude: config.ClientConfig{
			Providers: []config.Provider{
				{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
				{Name: "p2", BaseURL: "http://p2", APIKey: "k2", Priority: 2},
			},
		},
	}

	router := NewRouter(cfg)
	cp := router.proxies[ClientClaude]
	if cp == nil {
		t.Fatalf("expected claudecode proxy to be initialized")
	}
	cp.httpClient.Transport = rt
	cp.breakers[0].state = circuitOpen
	cp.breakers[0].openedAt = time.Now()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/claudecode/v1/messages/count_tokens", bytes.NewReader([]byte(`{"x":1}`)))
	router.handleRequest(rr, req)

	res := rr.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d", res.StatusCode, http.StatusOK)
	}
	if got := strings.TrimSpace(rr.Body.String()); got != "p2" {
		t.Fatalf("body: got %q want %q", got, "p2")
	}
	if got := cp.getCurrentIndex(); got != 0 {
		t.Fatalf("currentIndex changed by count_tokens skip: got %d want %d", got, 0)
	}
	if srv1Count != 0 || srv2Count != 1 {
		t.Fatalf("unexpected request counts: srv1=%d srv2=%d", srv1Count, srv2Count)
	}
}

func TestClaudeCountTokens_AllUnavailableReturnsRetryAfter(t *testing.T) {
	t.Parallel()

	var srv1Count int

	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		srv1Count++
		return newResponse(http.StatusOK, nil, "p1"), nil
	})

	cfg := &config.Config{
		Global: config.GlobalConfig{
			ListenAddr:      "127.0.0.1",
			Port:            3333,
			LogLevel:        config.LogLevelDebug,
			ReactivateAfter: "1h",
			CircuitBreaker: config.CircuitBreakerConfig{
				FailureThreshold:    1,
				SuccessThreshold:    1,
				OpenTimeout:         "1h",
				HalfOpenMaxInFlight: 1,
			},
		},
		Claude: config.ClientConfig{
			Providers: []config.Provider{
				{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
			},
		},
	}

	router := NewRouter(cfg)
	cp := router.proxies[ClientClaude]
	if cp == nil {
		t.Fatalf("expected claudecode proxy to be initialized")
	}
	cp.httpClient.Transport = rt
	cp.breakers[0].state = circuitOpen
	cp.breakers[0].openedAt = time.Now()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/claudecode/v1/messages/count_tokens", bytes.NewReader([]byte(`{"x":1}`)))
	router.handleRequest(rr, req)

	res := rr.Result()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d want %d", res.StatusCode, http.StatusServiceUnavailable)
	}
	if got := strings.TrimSpace(rr.Body.String()); !strings.Contains(got, "Advisory request temporarily unavailable") {
		t.Fatalf("body: got %q", got)
	}
	if ra := strings.TrimSpace(res.Header.Get("Retry-After")); ra == "" {
		t.Fatalf("expected Retry-After header to be set")
	}
	if srv1Count != 0 {
		t.Fatalf("expected no upstream call, got %d", srv1Count)
	}
	snap := cp.runtimeSnapshot(time.Now())
	if snap.LastRequest == nil {
		t.Fatalf("expected last request to be recorded")
	}
	if got := snap.LastRequest.Result; got != "advisory_request_unavailable" {
		t.Fatalf("last request result: got %q want %q", got, "advisory_request_unavailable")
	}
}

func TestClaudeCountTokens_RuntimeSnapshotOmitsScopedProvider(t *testing.T) {
	t.Parallel()

	cp := newClientProxy(ClientClaude, config.ClientModeAuto, "", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
		{Name: "p2", BaseURL: "http://p2", APIKey: "k2", Priority: 2},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.countTokensIndex = 1

	snap := cp.runtimeSnapshot(time.Now())
	if snap.CurrentProvider != "p1" {
		t.Fatalf("current provider: got %q want %q", snap.CurrentProvider, "p1")
	}
	if snap.CurrentProviders["default"] != "p1" {
		t.Fatalf("current providers default: got %q want %q", snap.CurrentProviders["default"], "p1")
	}
	if _, ok := snap.CurrentProviders[string(CapabilityClaudeCountTokens)]; ok {
		t.Fatalf("did not expect %q in current providers: %#v", CapabilityClaudeCountTokens, snap.CurrentProviders)
	}
}

func TestForwardWithFailover_DeactivateOn429Quota(t *testing.T) {
	t.Parallel()

	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "p1":
			h := make(http.Header)
			h.Set("Content-Type", "application/json")
			return newResponse(http.StatusTooManyRequests, h, `{"error":{"message":"quota","type":"insufficient_quota","code":"insufficient_quota"}}`), nil
		case "p2":
			return newResponse(http.StatusOK, nil, "ok"), nil
		default:
			return nil, errors.New("unexpected host")
		}
	})

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
		{Name: "p2", BaseURL: "http://p2", APIKey: "k2", Priority: 2},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.httpClient.Transport = rt

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/test", bytes.NewReader([]byte(`{"x":1}`)))
	cp.forwardWithFailover(rr, req, "/v1/test")

	res := rr.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d", res.StatusCode, http.StatusOK)
	}
	if !cp.isDeactivated(0) {
		t.Fatalf("expected provider 0 to be deactivated")
	}
}

func TestForwardWithFailover_429RateLimitDoesNotDeactivate(t *testing.T) {
	t.Parallel()

	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "p1":
			h := make(http.Header)
			h.Set("Content-Type", "application/json")
			return newResponse(http.StatusTooManyRequests, h, `{"error":{"message":"rate limit","type":"rate_limit_exceeded","code":"rate_limit_exceeded"}}`), nil
		case "p2":
			return newResponse(http.StatusOK, nil, "ok"), nil
		default:
			return nil, errors.New("unexpected host")
		}
	})

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
		{Name: "p2", BaseURL: "http://p2", APIKey: "k2", Priority: 2},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.httpClient.Transport = rt

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/test", bytes.NewReader([]byte(`{"x":1}`)))
	cp.forwardWithFailover(rr, req, "/v1/test")

	res := rr.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d", res.StatusCode, http.StatusOK)
	}
	if cp.isDeactivated(0) {
		t.Fatalf("expected provider 0 NOT to be deactivated")
	}
}

func TestForwardWithFailover_RetriesNextKeyBeforeNextProvider(t *testing.T) {
	t.Parallel()

	var hosts []string
	var auths []string
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		hosts = append(hosts, r.URL.Host)
		auths = append(auths, r.Header.Get("Authorization"))
		switch r.Header.Get("Authorization") {
		case "Bearer k1":
			h := make(http.Header)
			h.Set("Content-Type", "application/json")
			h.Set("Retry-After", "60")
			return newResponse(http.StatusTooManyRequests, h, `{"error":{"message":"rate limit","type":"rate_limit_exceeded","code":"rate_limit_exceeded"}}`), nil
		case "Bearer k2":
			return newResponse(http.StatusOK, nil, "ok"), nil
		default:
			return nil, errors.New("unexpected authorization header")
		}
	})

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKeys: []string{"k1", "k2"}, Priority: 1},
		{Name: "p2", BaseURL: "http://p2", APIKey: "fallback", Priority: 2},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.httpClient.Transport = rt

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/test", bytes.NewReader([]byte(`{"x":1}`)))
	cp.forwardWithFailover(rr, req, "/v1/test")

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d", rr.Result().StatusCode, http.StatusOK)
	}
	if len(hosts) != 2 || hosts[0] != "p1" || hosts[1] != "p1" {
		t.Fatalf("expected both attempts to stay on provider p1, got hosts=%v auth=%v", hosts, auths)
	}
	if got := cp.currentIndex; got != 0 {
		t.Fatalf("currentIndex: got %d want %d", got, 0)
	}
	if got := cp.currentKeyIndex[0]; got != 1 {
		t.Fatalf("currentKeyIndex[0]: got %d want %d", got, 1)
	}
}

func TestForwardWithFailover_ReleasesHalfOpenProbeWhenKeysExhausted(t *testing.T) {
	t.Parallel()

	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		h := make(http.Header)
		h.Set("Content-Type", "application/json")
		h.Set("Retry-After", "30")
		return newResponse(http.StatusTooManyRequests, h, `{"error":{"message":"rate limit","type":"rate_limit_exceeded","code":"rate_limit_exceeded"}}`), nil
	})

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKeys: []string{"k1", "k2"}, Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{
		enabled:             true,
		failureThreshold:    1,
		successThreshold:    1,
		openTimeout:         time.Minute,
		halfOpenMaxInFlight: 1,
	})
	cp.httpClient.Transport = rt
	cp.breakers[0].state = circuitHalfOpen

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/test", bytes.NewReader([]byte(`{"x":1}`)))
	cp.forwardWithFailover(rr, req, "/v1/test")

	if got := cp.breakers[0].halfOpenInFlight; got != 0 {
		t.Fatalf("halfOpenInFlight leaked: got %d want %d", got, 0)
	}
}

func TestForwardManual_DoesNotDeactivateOn401(t *testing.T) {
	t.Parallel()

	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return newResponse(http.StatusUnauthorized, nil, `{"error":{"type":"authentication_error","code":"invalid_api_key","message":"bad key"}}`), nil
	})

	cp := newClientProxy(ClientOpenAI, config.ClientModeManual, "p1", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.httpClient.Transport = rt

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/test", bytes.NewReader([]byte(`{"x":1}`)))
	cp.forwardWithFailover(rr, req, "/v1/test")

	res := rr.Result()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status: got %d want %d", res.StatusCode, http.StatusUnauthorized)
	}
	if cp.isDeactivated(0) {
		t.Fatalf("expected pinned provider NOT to be deactivated on auth failure in manual mode")
	}
}

func TestForwardManual_MultiKeyPassesThroughPinnedProviderResponse(t *testing.T) {
	t.Parallel()

	var auths []string
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		auths = append(auths, r.Header.Get("Authorization"))
		h := make(http.Header)
		h.Set("Retry-After", "9")
		return newResponse(http.StatusUnauthorized, h, `{"error":{"type":"authentication_error","code":"invalid_api_key","message":"bad key"}}`), nil
	})

	cp := newClientProxy(ClientOpenAI, config.ClientModeManual, "p1", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKeys: []string{"k1", "k2"}, Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.httpClient.Transport = rt

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/test", bytes.NewReader([]byte(`{"x":1}`)))
	cp.forwardWithFailover(rr, req, "/v1/test")

	res := rr.Result()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status: got %d want %d", res.StatusCode, http.StatusUnauthorized)
	}
	if got := res.Header.Get("Retry-After"); got != "9" {
		t.Fatalf("retry-after: got %q want %q", got, "9")
	}
	if len(auths) != 1 || auths[0] != "Bearer k1" {
		t.Fatalf("expected a single attempt with the preferred key, got auths=%v", auths)
	}
	if cp.isDeactivated(0) {
		t.Fatalf("expected pinned provider NOT to be deactivated in manual mode")
	}
	if cp.isKeyDeactivated(0, 0) || cp.isKeyDeactivated(0, 1) {
		t.Fatalf("expected manual mode not to deactivate keys")
	}
}

func TestForwardManual_MultiKeyPassesThrough402(t *testing.T) {
	t.Parallel()

	var auths []string
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		auths = append(auths, r.Header.Get("Authorization"))
		return newResponse(http.StatusPaymentRequired, nil, `{"error":{"type":"billing_error","message":"no money"}}`), nil
	})

	cp := newClientProxy(ClientOpenAI, config.ClientModeManual, "p1", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKeys: []string{"k1", "k2"}, Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.httpClient.Transport = rt

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/test", bytes.NewReader([]byte(`{"x":1}`)))
	cp.forwardWithFailover(rr, req, "/v1/test")

	res := rr.Result()
	if res.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("status: got %d want %d", res.StatusCode, http.StatusPaymentRequired)
	}
	if len(auths) != 1 || auths[0] != "Bearer k1" {
		t.Fatalf("expected a single attempt with the preferred key, got auths=%v", auths)
	}
	if cp.isDeactivated(0) {
		t.Fatalf("expected pinned provider NOT to be deactivated in manual mode")
	}
	if cp.isKeyDeactivated(0, 0) || cp.isKeyDeactivated(0, 1) {
		t.Fatalf("expected manual mode not to deactivate keys")
	}
}

func TestForwardManual_MultiKeyPassesThrough429RetryAfter(t *testing.T) {
	t.Parallel()

	var auths []string
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		auths = append(auths, r.Header.Get("Authorization"))
		h := make(http.Header)
		h.Set("Retry-After", "17")
		h.Set("Content-Type", "application/json")
		return newResponse(http.StatusTooManyRequests, h, `{"error":{"message":"rate limit","type":"rate_limit_exceeded","code":"rate_limit_exceeded"}}`), nil
	})

	cp := newClientProxy(ClientOpenAI, config.ClientModeManual, "p1", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKeys: []string{"k1", "k2"}, Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.httpClient.Transport = rt

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/test", bytes.NewReader([]byte(`{"x":1}`)))
	cp.forwardWithFailover(rr, req, "/v1/test")

	res := rr.Result()
	if res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status: got %d want %d", res.StatusCode, http.StatusTooManyRequests)
	}
	if got := res.Header.Get("Retry-After"); got != "17" {
		t.Fatalf("retry-after: got %q want %q", got, "17")
	}
	if len(auths) != 1 || auths[0] != "Bearer k1" {
		t.Fatalf("expected a single attempt with the preferred key, got auths=%v", auths)
	}
	if cp.isDeactivated(0) {
		t.Fatalf("expected pinned provider NOT to be deactivated in manual mode")
	}
	if cp.isKeyDeactivated(0, 0) || cp.isKeyDeactivated(0, 1) {
		t.Fatalf("expected manual mode not to deactivate keys")
	}
}

func TestForwardManual_DoesNotDeactivateOn402(t *testing.T) {
	t.Parallel()

	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return newResponse(http.StatusPaymentRequired, nil, `{"error":{"type":"billing_error","message":"no money"}}`), nil
	})

	cp := newClientProxy(ClientOpenAI, config.ClientModeManual, "p1", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.httpClient.Transport = rt

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/test", bytes.NewReader([]byte(`{"x":1}`)))
	cp.forwardWithFailover(rr, req, "/v1/test")

	res := rr.Result()
	if res.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("status: got %d want %d", res.StatusCode, http.StatusPaymentRequired)
	}
	if cp.isDeactivated(0) {
		t.Fatalf("expected pinned provider NOT to be deactivated on billing failure in manual mode")
	}
}

func TestForwardManual_DoesNotDeactivateOn429Quota(t *testing.T) {
	t.Parallel()

	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		h := make(http.Header)
		h.Set("Content-Type", "application/json")
		return newResponse(http.StatusTooManyRequests, h, `{"error":{"message":"quota","type":"insufficient_quota","code":"insufficient_quota"}}`), nil
	})

	cp := newClientProxy(ClientOpenAI, config.ClientModeManual, "p1", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.httpClient.Transport = rt

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/test", bytes.NewReader([]byte(`{"x":1}`)))
	cp.forwardWithFailover(rr, req, "/v1/test")

	res := rr.Result()
	if res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status: got %d want %d", res.StatusCode, http.StatusTooManyRequests)
	}
	if cp.isDeactivated(0) {
		t.Fatalf("expected pinned provider NOT to be deactivated on quota failure in manual mode")
	}
}

func TestForwardManual_PassesThrough200(t *testing.T) {
	t.Parallel()

	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		h := make(http.Header)
		h.Set("Content-Type", "text/plain")
		return newResponse(http.StatusOK, h, "ok"), nil
	})

	cp := newClientProxy(ClientOpenAI, config.ClientModeManual, "p1", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.httpClient.Transport = rt

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/test", bytes.NewReader([]byte(`{"x":1}`)))
	cp.forwardWithFailover(rr, req, "/v1/test")

	res := rr.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d", res.StatusCode, http.StatusOK)
	}
	if got := rr.Body.String(); got != "ok" {
		t.Fatalf("body: got %q want %q", got, "ok")
	}
	if ra := strings.TrimSpace(res.Header.Get("Retry-After")); ra != "" {
		t.Fatalf("did not expect Retry-After on success, got %q", ra)
	}
	if cp.isDeactivated(0) {
		t.Fatalf("expected pinned provider NOT to be deactivated")
	}
}

func TestForwardWithFailover_AllProvidersHalfOpenBusy_ReturnsRetryAfter(t *testing.T) {
	t.Parallel()

	// Transport should never be called when the circuit breaker refuses the request.
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected upstream call to %s", r.URL.String())
		return nil, errors.New("unreachable")
	})

	cbCfg := circuitBreakerConfig{
		enabled:             true,
		failureThreshold:    1,
		successThreshold:    1,
		openTimeout:         60 * time.Second,
		halfOpenMaxInFlight: 1,
	}

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, cbCfg)
	cp.httpClient.Transport = rt

	// Force half-open saturation.
	if len(cp.breakers) != 1 || cp.breakers[0] == nil {
		t.Fatalf("expected 1 circuit breaker")
	}
	cb := cp.breakers[0]
	cb.mu.Lock()
	cb.state = circuitHalfOpen
	cb.halfOpenInFlight = cb.cfg.halfOpenMaxInFlight
	cb.mu.Unlock()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/test", bytes.NewReader([]byte(`{"x":1}`)))
	cp.forwardWithFailover(rr, req, "/v1/test")

	res := rr.Result()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d want %d", res.StatusCode, http.StatusServiceUnavailable)
	}
	if ra := strings.TrimSpace(res.Header.Get("Retry-After")); ra == "" {
		t.Fatalf("expected Retry-After header to be set")
	}
}

func TestForwardWithFailover_ReactivateAfterTTL(t *testing.T) {
	t.Parallel()

	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return newResponse(http.StatusOK, nil, "ok"), nil
	})

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.httpClient.Transport = rt

	// Simulate a prior deactivation older than the TTL.
	cp.deactivated[0] = providerDeactivation{until: time.Now().Add(-2 * time.Hour)}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/test", bytes.NewReader([]byte(`{"x":1}`)))
	cp.forwardWithFailover(rr, req, "/v1/test")

	if cp.isDeactivated(0) {
		t.Fatalf("expected provider 0 to be reactivated")
	}
	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d", rr.Result().StatusCode, http.StatusOK)
	}
}

func TestForwardWithFailover_LastSwitchTracksLastHop(t *testing.T) {
	t.Parallel()

	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "p1":
			return newResponse(http.StatusServiceUnavailable, nil, "server busy"), nil
		case "p2":
			h := make(http.Header)
			h.Set("Retry-After", "30")
			return newResponse(http.StatusTooManyRequests, h, `{"error":{"message":"rate limit","type":"rate_limit_exceeded","code":"rate_limit_exceeded"}}`), nil
		case "p3":
			return newResponse(http.StatusOK, nil, "ok"), nil
		default:
			return nil, errors.New("unexpected host")
		}
	})

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
		{Name: "p2", BaseURL: "http://p2", APIKey: "k2", Priority: 2},
		{Name: "p3", BaseURL: "http://p3", APIKey: "k3", Priority: 3},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.httpClient.Transport = rt

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/responses", bytes.NewReader([]byte(`{"x":1}`)))
	cp.forwardWithFailover(rr, req, "/v1/responses")

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d", rr.Result().StatusCode, http.StatusOK)
	}
	if cp.lastSwitch.From != "p2" || cp.lastSwitch.To != "p3" {
		t.Fatalf("lastSwitch hop: got %s -> %s want p2 -> p3", cp.lastSwitch.From, cp.lastSwitch.To)
	}
	if cp.lastSwitch.Reason != "rate_limit" {
		t.Fatalf("lastSwitch reason: got %q want %q", cp.lastSwitch.Reason, "rate_limit")
	}
	if cp.lastSwitch.Status != http.StatusTooManyRequests {
		t.Fatalf("lastSwitch status: got %d want %d", cp.lastSwitch.Status, http.StatusTooManyRequests)
	}
}

func TestForwardWithFailover_AllProvidersFailedUpdatesLastRequest(t *testing.T) {
	t.Parallel()

	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "p1" {
			return nil, errors.New("unexpected host")
		}
		return newResponse(http.StatusServiceUnavailable, nil, "server busy"), nil
	})

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.httpClient.Transport = rt

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/responses", bytes.NewReader([]byte(`{"x":1}`)))
	cp.forwardWithFailover(rr, req, "/v1/responses")

	if rr.Result().StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d want %d", rr.Result().StatusCode, http.StatusServiceUnavailable)
	}
	if cp.lastRequest.Result != "all_providers_failed" {
		t.Fatalf("lastRequest.Result: got %q want %q", cp.lastRequest.Result, "all_providers_failed")
	}
	if cp.lastRequest.Provider != "p1" {
		t.Fatalf("lastRequest.Provider: got %q want %q", cp.lastRequest.Provider, "p1")
	}
	if !strings.Contains(cp.lastRequest.Detail, "p1 returned HTTP 503 Service Unavailable") {
		t.Fatalf("lastRequest.Detail: got %q", cp.lastRequest.Detail)
	}
}

func TestForwardWithFailover_RequestBuildFailureUpdatesLastRequest(t *testing.T) {
	t.Parallel()

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "broken", BaseURL: "://bad-url", APIKey: "k1", Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/responses", bytes.NewReader([]byte(`{"x":1}`)))
	cp.forwardWithFailover(rr, req, "/v1/responses")

	if rr.Result().StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d want %d", rr.Result().StatusCode, http.StatusServiceUnavailable)
	}
	if cp.lastRequest.Result != "request_rejected" {
		t.Fatalf("lastRequest.Result: got %q want %q", cp.lastRequest.Result, "request_rejected")
	}
	if cp.lastRequest.Provider != "broken" {
		t.Fatalf("lastRequest.Provider: got %q want %q", cp.lastRequest.Provider, "broken")
	}
	if !strings.Contains(cp.lastRequest.Detail, "invalid base_url") {
		t.Fatalf("lastRequest.Detail: got %q", cp.lastRequest.Detail)
	}
}

func TestForwardWithFailover_403GzipBodyDecodedAndRetriesNextProvider(t *testing.T) {
	t.Parallel()

	wantBody := `{"error":{"message":"账户余额不足","type":"permission_error","param":null,"code":null}}`

	var callsP1 int
	var callsP2 int

	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "p1":
			callsP1++
			var buf bytes.Buffer
			gz := gzip.NewWriter(&buf)
			_, _ = gz.Write([]byte(wantBody))
			_ = gz.Close()
			h := make(http.Header)
			h.Set("Content-Type", "application/json")
			h.Set("Content-Encoding", "gzip")
			return &http.Response{
				StatusCode: http.StatusForbidden,
				Header:     h,
				Body:       io.NopCloser(bytes.NewReader(buf.Bytes())),
			}, nil
		case "p2":
			callsP2++
			return newResponse(http.StatusOK, nil, "ok"), nil
		default:
			return nil, errors.New("unexpected host")
		}
	})

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
		{Name: "p2", BaseURL: "http://p2", APIKey: "k2", Priority: 2},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.httpClient.Transport = rt

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/test", bytes.NewReader([]byte(`{"x":1}`)))
	cp.forwardWithFailover(rr, req, "/v1/test")

	res := rr.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d", res.StatusCode, http.StatusOK)
	}
	if got := rr.Body.String(); got != "ok" {
		t.Fatalf("body: got %q want %q", got, "ok")
	}
	if callsP1 != 1 || callsP2 != 1 {
		t.Fatalf("unexpected upstream call counts: p1=%d p2=%d", callsP1, callsP2)
	}
	if !cp.isDeactivated(0) {
		t.Fatalf("expected provider 0 to be deactivated")
	}
	if got := cp.deactivated[0].message; got == "" || !strings.Contains(got, "账户余额不足") {
		t.Fatalf("expected decoded deactivation message, got %q", got)
	}
	if strings.Contains(cp.deactivated[0].message, "\x1f\x8b") {
		t.Fatalf("expected deactivation message to be decoded, got gzip bytes")
	}
}

func TestForwardWithFailover_429AnthropicRateLimitDoesNotDeactivate(t *testing.T) {
	t.Parallel()

	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "p1":
			h := make(http.Header)
			h.Set("Content-Type", "application/json")
			return newResponse(http.StatusTooManyRequests, h, `{"type":"error","error":{"type":"rate_limit_error","message":"rate limit"}}`), nil
		case "p2":
			return newResponse(http.StatusOK, nil, "ok"), nil
		default:
			return nil, errors.New("unexpected host")
		}
	})

	cp := newClientProxy(ClientClaude, config.ClientModeAuto, "", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
		{Name: "p2", BaseURL: "http://p2", APIKey: "k2", Priority: 2},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.httpClient.Transport = rt

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/claudecode/v1/test", bytes.NewReader([]byte(`{"x":1}`)))
	cp.forwardWithFailover(rr, req, "/v1/test")

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d", rr.Result().StatusCode, http.StatusOK)
	}
	if cp.isDeactivated(0) {
		t.Fatalf("expected provider 0 NOT to be deactivated")
	}
}

func TestForwardWithFailover_429RetryAfterCooldown(t *testing.T) {
	t.Parallel()

	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "p1":
			h := make(http.Header)
			h.Set("Content-Type", "application/json")
			h.Set("Retry-After", "120")
			return newResponse(http.StatusTooManyRequests, h, `{"error":{"message":"rate limit","type":"rate_limit_exceeded","code":"rate_limit_exceeded"}}`), nil
		case "p2":
			return newResponse(http.StatusOK, nil, "ok"), nil
		default:
			return nil, errors.New("unexpected host")
		}
	})

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
		{Name: "p2", BaseURL: "http://p2", APIKey: "k2", Priority: 2},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.httpClient.Transport = rt

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/test", bytes.NewReader([]byte(`{"x":1}`)))
	cp.forwardWithFailover(rr, req, "/v1/test")

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d", rr.Result().StatusCode, http.StatusOK)
	}
	if !cp.isDeactivated(0) {
		t.Fatalf("expected provider 0 to be in cooldown")
	}
	until := cp.deactivationUntil(0)
	if until.IsZero() {
		t.Fatalf("expected non-zero cooldown until")
	}
	if time.Until(until) < 90*time.Second {
		t.Fatalf("expected cooldown close to 120s, got %s", time.Until(until))
	}
}

func TestForwardWithFailover_429RetryAfterCappedAtOneHour(t *testing.T) {
	t.Parallel()

	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "p1":
			h := make(http.Header)
			h.Set("Content-Type", "application/json")
			h.Set("Retry-After", "7200")
			return newResponse(http.StatusTooManyRequests, h, `{"error":{"message":"rate limit","type":"rate_limit_exceeded","code":"rate_limit_exceeded"}}`), nil
		case "p2":
			return newResponse(http.StatusOK, nil, "ok"), nil
		default:
			return nil, errors.New("unexpected host")
		}
	})

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
		{Name: "p2", BaseURL: "http://p2", APIKey: "k2", Priority: 2},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.httpClient.Transport = rt

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/test", bytes.NewReader([]byte(`{"x":1}`)))
	cp.forwardWithFailover(rr, req, "/v1/test")

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d", rr.Result().StatusCode, http.StatusOK)
	}
	if !cp.isDeactivated(0) {
		t.Fatalf("expected provider 0 to be in cooldown")
	}
	until := cp.deactivationUntil(0)
	if until.IsZero() {
		t.Fatalf("expected non-zero cooldown until")
	}
	remaining := time.Until(until)
	if remaining > time.Hour+5*time.Second {
		t.Fatalf("expected cooldown capped near 1h, got %s", remaining)
	}
}

func TestForwardWithFailover_OAuthRetryAfterNotCappedAtOneHour(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	svc := oauthpkg.NewService(dir)
	if err := svc.Store().Save(&oauthpkg.Credential{
		Ref:         "claude-sean-example-com",
		Provider:    config.OAuthProviderClaude,
		Email:       "sean@example.com",
		AccountID:   "acct_123",
		AccessToken: "access-1",
		ExpiresAt:   time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "api.anthropic.com":
			h := make(http.Header)
			h.Set("Content-Type", "application/json")
			h.Set("Retry-After", "7200")
			return newResponse(http.StatusTooManyRequests, h, `{"type":"error","error":{"type":"rate_limit_error","message":"rate limit"}}`), nil
		case "p2":
			return newResponse(http.StatusOK, nil, "ok"), nil
		default:
			return nil, errors.New("unexpected host")
		}
	})

	cp := newClientProxy(ClientClaude, config.ClientModeAuto, "", []config.Provider{
		{
			Name:          "claude-oauth",
			AuthType:      config.ProviderAuthTypeOAuth,
			OAuthProvider: config.OAuthProviderClaude,
			OAuthRef:      "claude-sean-example-com",
			Priority:      1,
		},
		{Name: "p2", BaseURL: "http://p2", APIKey: "k2", Priority: 2},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = svc
	cp.httpClient.Transport = rt

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/claudecode/v1/messages", bytes.NewReader([]byte(`{"model":"claude-3-5-sonnet","messages":[]}`)))
	cp.forwardWithFailover(rr, req, "/v1/messages")

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d body=%s", rr.Result().StatusCode, http.StatusOK, rr.Body.String())
	}
	if !cp.isDeactivated(0) {
		t.Fatalf("expected oauth provider to be in cooldown")
	}
	remaining := time.Until(cp.deactivationUntil(0))
	if remaining < 90*time.Minute {
		t.Fatalf("expected oauth cooldown to exceed 1h cap, got %s", remaining)
	}
	if remaining > 2*time.Hour+5*time.Second {
		t.Fatalf("expected oauth cooldown near 2h, got %s", remaining)
	}
}

func TestForwardWithFailover_AllProvidersCooldownReturnsRetryAfter(t *testing.T) {
	t.Parallel()

	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "p1":
			h := make(http.Header)
			h.Set("Content-Type", "application/json")
			h.Set("Retry-After", "30")
			return newResponse(http.StatusTooManyRequests, h, `{"error":{"message":"rate limit","type":"rate_limit_exceeded","code":"rate_limit_exceeded"}}`), nil
		case "p2":
			h := make(http.Header)
			h.Set("Content-Type", "application/json")
			h.Set("Retry-After", "45")
			return newResponse(http.StatusTooManyRequests, h, `{"type":"error","error":{"type":"rate_limit_error","message":"rate limit"}}`), nil
		default:
			return nil, errors.New("unexpected host")
		}
	})

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
		{Name: "p2", BaseURL: "http://p2", APIKey: "k2", Priority: 2},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.httpClient.Transport = rt

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/test", bytes.NewReader([]byte(`{"x":1}`)))
	cp.forwardWithFailover(rr, req, "/v1/test")

	res := rr.Result()
	if res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status: got %d want %d", res.StatusCode, http.StatusTooManyRequests)
	}
	if ra := res.Header.Get("Retry-After"); ra == "" {
		t.Fatalf("expected Retry-After header to be set")
	}
}

func TestForwardManual_DoesNotFailover(t *testing.T) {
	t.Parallel()

	var callsP1 int
	var callsP2 int
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "p1":
			callsP1++
			return nil, errors.New("network down")
		case "p2":
			callsP2++
			return newResponse(http.StatusOK, nil, "ok"), nil
		default:
			return nil, errors.New("unexpected host")
		}
	})

	cp := newClientProxy(ClientOpenAI, config.ClientModeManual, "p1", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
		{Name: "p2", BaseURL: "http://p2", APIKey: "k2", Priority: 2},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.httpClient.Transport = rt

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/test", bytes.NewReader([]byte(`{"x":1}`)))
	cp.forwardWithFailover(rr, req, "/v1/test")

	res := rr.Result()
	if res.StatusCode != http.StatusBadGateway {
		t.Fatalf("status: got %d want %d", res.StatusCode, http.StatusBadGateway)
	}
	if callsP1 != 1 {
		t.Fatalf("expected p1 to be called once, got %d", callsP1)
	}
	if callsP2 != 0 {
		t.Fatalf("expected p2 not to be called, got %d", callsP2)
	}
}

func TestForwardManual_PassesThroughRetryAfter(t *testing.T) {
	t.Parallel()

	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		h := make(http.Header)
		h.Set("Retry-After", "10")
		h.Set("Content-Type", "application/json")
		return newResponse(http.StatusTooManyRequests, h, `{"error":{"type":"rate_limit_error","message":"slow down"}}`), nil
	})

	cp := newClientProxy(ClientOpenAI, config.ClientModeManual, "p1", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.httpClient.Transport = rt

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/test", bytes.NewReader([]byte(`{"x":1}`)))
	cp.forwardWithFailover(rr, req, "/v1/test")

	res := rr.Result()
	if res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status: got %d want %d", res.StatusCode, http.StatusTooManyRequests)
	}
	if got := strings.TrimSpace(res.Header.Get("Retry-After")); got != "10" {
		t.Fatalf("Retry-After: got %q want %q", got, "10")
	}
	if got := rr.Body.String(); !strings.Contains(got, "slow down") {
		t.Fatalf("expected upstream body to be forwarded, got %q", got)
	}

	if len(cp.deactivated) != 1 {
		t.Fatalf("expected one provider deactivation slot, got %d", len(cp.deactivated))
	}
	if cp.isDeactivated(0) {
		t.Fatalf("expected pinned provider NOT to be deactivated in manual mode")
	}
}

func TestCircuitBreaker_OpensAndReturnsRetryAfter(t *testing.T) {
	t.Parallel()

	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return newResponse(http.StatusServiceUnavailable, nil, "down"), nil
	})

	cbCfg := circuitBreakerConfig{
		enabled:             true,
		failureThreshold:    2,
		successThreshold:    1,
		openTimeout:         10 * time.Second,
		halfOpenMaxInFlight: 1,
	}

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, cbCfg)
	cp.httpClient.Transport = rt

	// First two failures trip the circuit.
	for i := 0; i < 2; i++ {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/test", bytes.NewReader([]byte(`{"x":1}`)))
		cp.forwardWithFailover(rr, req, "/v1/test")
		if rr.Result().StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("status[%d]: got %d want %d", i, rr.Result().StatusCode, http.StatusServiceUnavailable)
		}
	}

	// Next request should be blocked by open circuit and return Retry-After.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/test", bytes.NewReader([]byte(`{"x":1}`)))
	cp.forwardWithFailover(rr, req, "/v1/test")
	res := rr.Result()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d want %d", res.StatusCode, http.StatusServiceUnavailable)
	}
	if ra := res.Header.Get("Retry-After"); ra == "" {
		t.Fatalf("expected Retry-After header to be set")
	}
}

func TestCircuitBreaker_DoesNotCountClientCancelAsFailure(t *testing.T) {
	t.Parallel()

	firstRead := make(chan struct{})
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		body := &ctxCancelBody{
			ctx:       r.Context(),
			first:     []byte("hello"),
			firstRead: firstRead,
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       body,
		}, nil
	})

	cbCfg := circuitBreakerConfig{
		enabled:             true,
		failureThreshold:    1,
		successThreshold:    1,
		openTimeout:         10 * time.Second,
		halfOpenMaxInFlight: 1,
	}

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, cbCfg)
	cp.httpClient.Transport = rt

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		<-firstRead
		cancel()
	}()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/test", bytes.NewReader([]byte(`{"x":1}`))).WithContext(ctx)
	cp.forwardWithFailover(rr, req, "/v1/test")

	if len(cp.breakers) != 1 || cp.breakers[0] == nil {
		t.Fatalf("expected 1 circuit breaker")
	}
	cb := cp.breakers[0]
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if cb.state != circuitClosed {
		t.Fatalf("expected circuit to remain closed, got %s", cb.state)
	}
	if cb.consecutiveFailures != 0 {
		t.Fatalf("expected no failures recorded, got %d", cb.consecutiveFailures)
	}
}

type ctxCancelBody struct {
	ctx       context.Context
	first     []byte
	sentFirst bool
	firstRead chan struct{}
}

func (b *ctxCancelBody) Read(p []byte) (int, error) {
	if !b.sentFirst {
		b.sentFirst = true
		if b.firstRead != nil {
			close(b.firstRead)
		}
		return copy(p, b.first), nil
	}
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (b *ctxCancelBody) Close() error { return nil }
