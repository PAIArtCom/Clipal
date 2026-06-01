package oauth

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
