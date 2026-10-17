package oauth

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PAIArtCom/Clipal/internal/outbound"
)

type claudeRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f claudeRoundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestNewClaudeClient_UsesAnthropicFallbackTransport(t *testing.T) {
	client := NewClaudeClient()
	if client.HTTPClient == nil {
		t.Fatalf("HTTPClient is nil")
	}

	transport, ok := client.HTTPClient.Transport.(*anthropicFallbackRoundTripper)
	if !ok || transport == nil {
		t.Fatalf("transport = %T, want *anthropicFallbackRoundTripper", client.HTTPClient.Transport)
	}
	if transport.anthropic == nil {
		t.Fatalf("anthropic transport is nil")
	}
	if transport.fallback == nil {
		t.Fatalf("fallback transport is nil")
	}
}

func TestAnthropicFallbackRoundTripper_RoutesAnthropicHostsToUTLS(t *testing.T) {
	var anthropicCalls int
	var fallbackCalls int

	transport := &anthropicFallbackRoundTripper{
		anthropic: claudeRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			anthropicCalls++
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
		fallback: claudeRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			fallbackCalls++
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"fallback":true}`)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}

	req, err := http.NewRequest(http.MethodPost, "https://platform.claude.com/v1/oauth/token", nil)
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}

	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if anthropicCalls != 1 {
		t.Fatalf("anthropic calls = %d, want 1", anthropicCalls)
	}
	if fallbackCalls != 0 {
		t.Fatalf("fallback calls = %d, want 0", fallbackCalls)
	}
}

func TestClaudeExchangeCode_DefaultClientSupportsLocalTokenOverride(t *testing.T) {
	now := time.Date(2026, 4, 22, 11, 0, 0, 0, time.UTC)
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Method; got != http.MethodPost {
			t.Fatalf("method = %q, want POST", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"access-1","refresh_token":"refresh-1","token_type":"Bearer","expires_in":3600,"organization":{"uuid":"org_123","name":"Anthropic"},"account":{"uuid":"acct_123","email_address":"sean@example.com"}}`)
	}))
	defer tokenServer.Close()

	client := NewClaudeClient()
	client.TokenURL = tokenServer.URL
	client.ClientID = "test-client"
	client.Now = func() time.Time { return now }

	cred, err := client.ExchangeCode(context.Background(), "auth-code", "session-123", "http://localhost:54545/callback", PKCECodes{
		CodeVerifier:  "verifier-123",
		CodeChallenge: "challenge-123",
	})
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}

	if got := cred.AccessToken; got != "access-1" {
		t.Fatalf("access_token = %q, want access-1", got)
	}
	if got := cred.RefreshToken; got != "refresh-1" {
		t.Fatalf("refresh_token = %q, want refresh-1", got)
	}
	if got := cred.Email; got != "sean@example.com" {
		t.Fatalf("email = %q, want sean@example.com", got)
	}
}

func TestAnthropicAccountDialerPreservesTLSRootsAndHost(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 || r.Host != "example.com" || r.TLS.ServerName != "example.com" {
			t.Errorf("protocol/Host/SNI=%d/%s/%s", r.ProtoMajor, r.Host, r.TLS.ServerName)
		}
		_, _ = io.WriteString(w, "ok")
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	base := &anthropicFallbackRoundTripper{fallback: server.Client().Transport}
	configured, ok := base.WithDialContext(func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, strings.TrimPrefix(server.URL, "https://"))
	}).(*anthropicFallbackRoundTripper)
	if !ok {
		t.Fatal("expected Anthropic transport")
	}
	defer configured.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com/", nil)
	// Exercise the native TLS adapter itself while retaining original Host/SNI.
	resp, err := configured.anthropic.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(data) != "ok" {
		t.Fatalf("response=%q", data)
	}
}

func TestNativeClaudeAccountClientRetainsTargetProxyPolicy(t *testing.T) {
	entry, err := url.Parse("http://127.0.0.1:3128")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var addresses []string
	fallback := &http.Transport{
		Proxy: func(req *http.Request) (*url.URL, error) {
			if req.URL.Hostname() == "no-proxy.example" {
				return nil, nil
			}
			return entry, nil
		},
		DialContext: func(_ context.Context, _, addr string) (net.Conn, error) {
			mu.Lock()
			addresses = append(addresses, addr)
			mu.Unlock()
			return nil, errors.New("fixture dial failure")
		},
	}
	original := &http.Client{Transport: &anthropicFallbackRoundTripper{anthropic: newAnthropicUTLSRoundTripper(), fallback: fallback}}
	client, err := EnvironmentHTTPClient(original, &RequestEnvironment{DNS: "auto"}, "claude")
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.anthropic.com/", nil)
	if _, err := client.Do(req); err == nil {
		t.Fatal("expected fixture connection failure")
	}
	mu.Lock()
	got := append([]string(nil), addresses...)
	addresses = nil
	mu.Unlock()
	if len(got) != 1 || got[0] != "127.0.0.1:3128" {
		t.Fatalf("proxy business request bypassed its entry or queried DoH: %v", got)
	}
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, "https://no-proxy.example/", nil)
	if _, err := client.Do(req); err == nil {
		t.Fatal("expected fixture DNS failure")
	}
	mu.Lock()
	got = append([]string(nil), addresses...)
	mu.Unlock()
	if strings.Join(got, ",") != "cloudflare-dns.com:443,1.1.1.1:443,dns.google:443,8.8.8.8:443,no-proxy.example:443" {
		t.Fatalf("no-proxy route did not retain automatic DNS: %v", got)
	}
	if fallback.Proxy == nil {
		t.Fatal("account wrapper mutated the original transport")
	}
}

func TestNativeAnthropicClientObservesInheritedNetworkPolicyChanges(t *testing.T) {
	for _, key := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy", "REQUEST_METHOD"} {
		t.Setenv(key, "")
	}
	client := newAnthropicHTTPClient(time.Second)
	client, err := EnvironmentHTTPClient(client, &RequestEnvironment{DNS: "auto"}, "claude")
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	req, _ := http.NewRequest(http.MethodGet, "https://platform.claude.com/v1/oauth/token", nil)
	transport, ok := client.Transport.(*environmentTransport)
	if !ok {
		t.Fatalf("expected environment transport, got %T", client.Transport)
	}
	inherited := &http.Client{Transport: transport.base}
	for _, proxy := range []string{"http://first.example:8080", "http://second.example:8081"} {
		t.Setenv("HTTPS_PROXY", proxy)
		got, err := outbound.ProxyFor(inherited, req.URL.String())
		if err != nil || got == nil || got.String() != proxy {
			t.Fatalf("native inherited proxy=%v error=%v, want %s", got, err, proxy)
		}
	}
	t.Setenv("NO_PROXY", "platform.claude.com")
	if got, err := outbound.ProxyFor(inherited, req.URL.String()); err != nil || got != nil {
		t.Fatalf("native NO_PROXY=%v error=%v", got, err)
	}
}
