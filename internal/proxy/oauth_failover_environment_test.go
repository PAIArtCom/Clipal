package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PAIArtCom/Clipal/internal/config"
	oauthpkg "github.com/PAIArtCom/Clipal/internal/oauth"
)

func TestForwardWithFailover_RebuildsClaudeAccountEnvironmentPerProvider(t *testing.T) {
	dir := t.TempDir()
	svc := oauthpkg.NewService(dir)
	accounts := []struct {
		ref, account, token, country, timezone string
	}{
		{ref: "account-a", account: "acct-a", token: "access-a", country: "JP", timezone: "Asia/Tokyo"},
		{ref: "account-b", account: "acct-b", token: "access-b", country: "DE", timezone: "Europe/Berlin"},
	}
	for _, account := range accounts {
		if err := svc.Store().Save(&oauthpkg.Credential{
			Ref:         account.ref,
			Provider:    config.OAuthProviderClaude,
			AccountID:   account.account,
			AccessToken: account.token,
			ExpiresAt:   time.Now().Add(time.Hour),
			Environment: &oauthpkg.RequestEnvironment{Country: account.country, Timezone: account.timezone, Source: "user"},
		}); err != nil {
			t.Fatal(err)
		}
	}

	providers := []config.Provider{
		{Name: "account-a", AuthType: config.ProviderAuthTypeOAuth, OAuthProvider: config.OAuthProviderClaude, OAuthRef: "account-a", Priority: 1},
		{Name: "account-b", AuthType: config.ProviderAuthTypeOAuth, OAuthProvider: config.OAuthProviderClaude, OAuthRef: "account-b", Priority: 2},
	}
	cp := newClientProxy(ClientClaude, config.ClientModeAuto, "", providers, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = svc
	defer cp.Close()

	var firstCalls, secondCalls int32
	var firstHeader, secondHeader string
	var firstBody, secondBody []byte
	first := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		atomic.AddInt32(&firstCalls, 1)
		firstHeader = req.Header.Get("Accept-Language")
		firstBody, _ = io.ReadAll(req.Body)
		return newResponse(http.StatusServiceUnavailable, http.Header{"Content-Type": []string{"application/json"}}, `{"error":"account-a unavailable"}`), nil
	})
	second := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		atomic.AddInt32(&secondCalls, 1)
		secondHeader = req.Header.Get("Accept-Language")
		secondBody, _ = io.ReadAll(req.Body)
		return newResponse(http.StatusOK, http.Header{"Content-Type": []string{"application/json"}}, `{"ok":true}`), nil
	})
	cp.providerHTTPClients = []*http.Client{{Transport: first}, {Transport: second}}

	body := []byte(`{"model":"claude-sonnet-4-5","messages":[],"metadata":{"country":"GB","timezone":"Europe/London"}}`)
	req := httptest.NewRequest(http.MethodPost, "http://proxy/claudecode/v1/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withRequestContext(req, RequestContext{ClientType: ClientClaude, Family: ProtocolFamilyClaude, Capability: CapabilityClaudeMessages, UpstreamPath: "/v1/messages", UnifiedIngress: true})
	rr := httptest.NewRecorder()
	cp.forwardWithFailover(rr, req, "/v1/messages")

	if rr.Code != http.StatusOK || rr.Body.String() != `{"ok":true}` {
		t.Fatalf("response = %d %q", rr.Code, rr.Body.String())
	}
	if atomic.LoadInt32(&firstCalls) != 1 || atomic.LoadInt32(&secondCalls) != 1 {
		t.Fatalf("calls = first:%d second:%d", firstCalls, secondCalls)
	}
	if firstHeader != "ja-JP" || secondHeader != "de-DE" {
		t.Fatalf("Accept-Language = first:%q second:%q", firstHeader, secondHeader)
	}
	assertClaudeEnvironmentBody(t, firstBody, "JP", "Asia/Tokyo")
	assertClaudeEnvironmentBody(t, secondBody, "DE", "Europe/Berlin")
}

func assertClaudeEnvironmentBody(t *testing.T, body []byte, country, timezone string) {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatalf("decode Claude body: %v", err)
	}
	meta, _ := root["metadata"].(map[string]any)
	if meta["country"] != country || meta["timezone"] != timezone {
		t.Fatalf("metadata = %#v, want country=%s timezone=%s", meta, country, timezone)
	}
}
