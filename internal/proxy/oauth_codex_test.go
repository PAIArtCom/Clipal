package proxy

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
	oauthpkg "github.com/lansespirit/Clipal/internal/oauth"
)

func TestCreateProxyRequest_CodexOAuthNonStreamingUsesResponsesStreamEndpoint(t *testing.T) {
	dir := t.TempDir()
	svc := oauthpkg.NewService(dir)
	if err := svc.Store().Save(&oauthpkg.Credential{
		Ref:         "codex-sean-example-com",
		Provider:    config.OAuthProviderCodex,
		Email:       "sean@example.com",
		AccountID:   "acct_123",
		AccessToken: "access-1",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{
			Name:          "codex-oauth",
			AuthType:      config.ProviderAuthTypeOAuth,
			OAuthProvider: config.OAuthProviderCodex,
			OAuthRef:      "codex-sean-example-com",
			Priority:      1,
			Overrides: &config.ProviderOverrides{
				Model: strPtr("gpt-5.4"),
				OpenAI: &config.OpenAIOverrides{
					ReasoningEffort: strPtr("high"),
				},
			},
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = svc

	body := []byte(`{"model":"gpt-4.1","instructions":null,"stream":false,"store":true,"stream_options":{"include_usage":true},"input":"hello"}`)
	original := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/responses?foo=bar", bytes.NewReader(body))
	original.Header.Set("Content-Type", "application/json")
	original.Header.Set("Authorization", "Bearer client-key")
	original.Header.Set("Cookie", "secret=1")
	original.Header.Set("X-Test", "keep")
	original = withRequestContext(original, RequestContext{
		ClientType:     ClientOpenAI,
		Family:         ProtocolFamilyOpenAI,
		Capability:     CapabilityOpenAIResponses,
		UpstreamPath:   "/v1/responses",
		UnifiedIngress: true,
	})

	proxyReq, err := cp.createProxyRequest(original, cp.providers[0], "", "/v1/responses", body)
	if err != nil {
		t.Fatalf("createProxyRequest: %v", err)
	}
	if got := proxyReq.URL.String(); got != "https://chatgpt.com/backend-api/codex/responses?foo=bar" {
		t.Fatalf("url = %q", got)
	}
	if got := proxyReq.Header.Get("Authorization"); got != "Bearer access-1" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := proxyReq.Header.Get("Accept"); got != "text/event-stream" {
		t.Fatalf("Accept = %q", got)
	}
	if got := proxyReq.Header.Get("Originator"); got != codexOAuthOriginator {
		t.Fatalf("Originator = %q", got)
	}
	if got := proxyReq.Header.Get("Chatgpt-Account-Id"); got != "acct_123" {
		t.Fatalf("Chatgpt-Account-Id = %q", got)
	}
	if got := proxyReq.Header.Get("Cookie"); got != "" {
		t.Fatalf("Cookie = %q, want empty", got)
	}
	if got := proxyReq.Header.Get("X-Test"); got != "" {
		t.Fatalf("X-Test = %q, want empty", got)
	}
	if got := proxyReq.Header.Get("Session-Id"); got == "" {
		t.Fatalf("expected Session-Id to be generated")
	}
	if got := proxyReq.Header.Get("Thread-Id"); got == "" {
		t.Fatalf("expected Thread-Id to be generated")
	}
	if got := proxyReq.Header.Get("X-Codex-Installation-Id"); got == "" {
		t.Fatalf("expected X-Codex-Installation-Id to be generated")
	}
	if got := proxyReq.Header.Get("X-Client-Request-Id"); got != proxyReq.Header.Get("Thread-Id") {
		t.Fatalf("X-Client-Request-Id = %q, want Thread-Id %q", got, proxyReq.Header.Get("Thread-Id"))
	}
	if got := proxyReq.Header.Get("Session_id"); got != "" {
		t.Fatalf("Session_id = %q, want empty", got)
	}
	if got := proxyReq.Header.Get("Version"); got != codexOAuthVersion {
		t.Fatalf("Version = %q", got)
	}
	if got := proxyReq.Header.Get("OpenAI-Beta"); got != "" {
		t.Fatalf("OpenAI-Beta = %q, want empty", got)
	}

	root := decodeRequestBodyMap(t, proxyReq)
	if got := root["model"]; got != "gpt-5.4" {
		t.Fatalf("model = %v", got)
	}
	reasoning, ok := root["reasoning"].(map[string]any)
	if !ok {
		t.Fatalf("reasoning = %T %#v", root["reasoning"], root["reasoning"])
	}
	if got := reasoning["effort"]; got != "high" {
		t.Fatalf("reasoning.effort = %v", got)
	}
	if got := root["instructions"]; got != "" {
		t.Fatalf("instructions = %#v", got)
	}
	if got := root["prompt_cache_key"]; got != proxyReq.Header.Get("Thread-Id") {
		t.Fatalf("prompt_cache_key = %v, want Thread-Id %q", got, proxyReq.Header.Get("Thread-Id"))
	}
	metadata, ok := root["client_metadata"].(map[string]any)
	if !ok {
		t.Fatalf("client_metadata = %T %#v", root["client_metadata"], root["client_metadata"])
	}
	if got := metadata["x-codex-installation-id"]; got == "" {
		t.Fatalf("expected client_metadata.x-codex-installation-id to be generated")
	}
	if got := root["stream"]; got != true {
		t.Fatalf("stream = %v", got)
	}
	if got := root["store"]; got != false {
		t.Fatalf("store = %v", got)
	}
	if _, ok := root["stream_options"]; ok {
		t.Fatalf("did not expect stream_options in compact body: %#v", root)
	}
	input, ok := root["input"].([]any)
	if !ok || len(input) != 1 {
		t.Fatalf("input = %#v", root["input"])
	}
	msg, ok := input[0].(map[string]any)
	if !ok {
		t.Fatalf("input[0] = %T %#v", input[0], input[0])
	}
	if got := msg["role"]; got != "user" {
		t.Fatalf("input[0].role = %v", got)
	}
}

func TestCreateCodexOAuthRequest_RefreshUsesProviderCustomProxy(t *testing.T) {
	now := time.Date(2026, 4, 18, 21, 0, 0, 0, time.UTC)
	var proxyHits int32
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&proxyHits, 1)
		if got := r.URL.Host; got != "auth.example" {
			t.Fatalf("proxied host = %q, want auth.example", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"access-2","refresh_token":"refresh-2","expires_in":3600}`)
	}))
	defer proxyServer.Close()

	svc := oauthpkg.NewService(t.TempDir(), oauthpkg.WithCodexClient(&oauthpkg.CodexClient{
		TokenURL: "http://auth.example/token",
		ClientID: "test-client",
		HTTPClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			t.Fatalf("refresh used default oauth HTTP client")
			return nil, nil
		})},
		Now: func() time.Time { return now },
	}))
	if err := svc.Store().Save(&oauthpkg.Credential{
		Ref:          "codex-sean-example-com",
		Provider:     config.OAuthProviderCodex,
		Email:        "sean@example.com",
		AccountID:    "acct_123",
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		ExpiresAt:    now.Add(-time.Minute),
		LastRefresh:  now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	provider := config.Provider{
		Name:          "codex-oauth",
		AuthType:      config.ProviderAuthTypeOAuth,
		OAuthProvider: config.OAuthProviderCodex,
		OAuthRef:      "codex-sean-example-com",
		ProxyMode:     config.ProviderProxyModeCustom,
		ProxyURL:      proxyServer.URL,
		Priority:      1,
	}
	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{provider}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = svc
	body := []byte(`{"model":"gpt-5.2","input":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/responses", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withRequestContext(req, RequestContext{
		ClientType:     ClientOpenAI,
		Family:         ProtocolFamilyOpenAI,
		Capability:     CapabilityOpenAIResponses,
		UpstreamPath:   "/v1/responses",
		UnifiedIngress: true,
	})

	proxyReq, err := cp.createCodexOAuthRequestWithPayloadForProvider(req, provider, 0, "/v1/responses", newRequestPayload(body))
	if err != nil {
		t.Fatalf("createCodexOAuthRequestWithPayloadForProvider: %v", err)
	}
	if got := proxyReq.Header.Get("Authorization"); got != "Bearer access-2" {
		t.Fatalf("Authorization = %q, want refreshed token", got)
	}
	if got := atomic.LoadInt32(&proxyHits); got != 1 {
		t.Fatalf("proxy hits = %d, want 1", got)
	}
}

func TestOAuthHTTPClientForProvider_ClaudeDirectPreservesProviderClient(t *testing.T) {
	providerDirect := config.Provider{
		Name:          "claude-oauth",
		AuthType:      config.ProviderAuthTypeOAuth,
		OAuthProvider: config.OAuthProviderClaude,
		OAuthRef:      "claude-ref",
		ProxyMode:     config.ProviderProxyModeDirect,
		Priority:      1,
	}
	cp := newClientProxy(ClientClaude, config.ClientModeAuto, "", []config.Provider{providerDirect}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	if got := cp.oauthHTTPClientForProvider(providerDirect, 0); got != nil {
		t.Fatalf("provider direct oauthHTTPClientForProvider = %T, want nil", got)
	}

	providerDefault := config.Provider{
		Name:          "claude-oauth",
		AuthType:      config.ProviderAuthTypeOAuth,
		OAuthProvider: config.OAuthProviderClaude,
		OAuthRef:      "claude-ref",
		Priority:      1,
	}
	cp = newClientProxyWithGlobalProxy(ClientClaude, config.ClientModeAuto, "", []config.Provider{providerDefault}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{}, config.GlobalUpstreamProxyModeDirect, "")
	if got := cp.oauthHTTPClientForProvider(providerDefault, 0); got != nil {
		t.Fatalf("global direct oauthHTTPClientForProvider = %T, want nil", got)
	}

	codexDirect := config.Provider{
		Name:          "codex-oauth",
		AuthType:      config.ProviderAuthTypeOAuth,
		OAuthProvider: config.OAuthProviderCodex,
		OAuthRef:      "codex-ref",
		ProxyMode:     config.ProviderProxyModeDirect,
		Priority:      1,
	}
	cp = newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{codexDirect}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	if got := cp.oauthHTTPClientForProvider(codexDirect, 0); got == nil {
		t.Fatalf("codex direct oauthHTTPClientForProvider = nil, want direct override client")
	}
}

func TestCreateProxyRequest_CodexOAuthStreamingUsesResponsesEndpoint(t *testing.T) {
	dir := t.TempDir()
	svc := oauthpkg.NewService(dir)
	if err := svc.Store().Save(&oauthpkg.Credential{
		Ref:         "codex-sean-example-com",
		Provider:    config.OAuthProviderCodex,
		Email:       "sean@example.com",
		AccountID:   "acct_123",
		AccessToken: "access-1",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{
			Name:          "codex-oauth",
			AuthType:      config.ProviderAuthTypeOAuth,
			OAuthProvider: config.OAuthProviderCodex,
			OAuthRef:      "codex-sean-example-com",
			Priority:      1,
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = svc

	body := []byte(`{"model":"gpt-4.1","stream":true,"input":"hello"}`)
	original := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/responses", bytes.NewReader(body))
	original.Header.Set("Content-Type", "application/json")
	original.Header.Set("Originator", "clipal-test")
	original.Header.Set("User-Agent", "clipal-test/1.0")
	original.Header.Set("X-Codex-Turn-Metadata", `{"turn_id":"turn-1"}`)
	original.Header.Set("X-Codex-Window-Id", "thread-123:0")
	original.Header.Set("X-Codex-Parent-Thread-Id", "parent-thread-1")
	original.Header.Set("X-Codex-Installation-Id", "install-123")
	original.Header.Set("X-Codex-Beta-Features", "feature-a")
	original.Header.Set("X-OpenAI-Subagent", "review")
	original.Header.Set("Session-Id", "session-123")
	original.Header.Set("Thread-Id", "thread-123")
	original.Header.Set("X-Client-Request-Id", "req-123")
	original.Header.Set("Cookie", "secret=1")
	original.Header.Set("X-Test", "keep")
	original = withRequestContext(original, RequestContext{
		ClientType:     ClientOpenAI,
		Family:         ProtocolFamilyOpenAI,
		Capability:     CapabilityOpenAIResponses,
		UpstreamPath:   "/v1/responses",
		UnifiedIngress: true,
	})

	proxyReq, err := cp.createProxyRequest(original, cp.providers[0], "", "/v1/responses", body)
	if err != nil {
		t.Fatalf("createProxyRequest: %v", err)
	}
	if got := proxyReq.URL.String(); got != "https://chatgpt.com/backend-api/codex/responses" {
		t.Fatalf("url = %q", got)
	}
	if got := proxyReq.Header.Get("Accept"); got != "text/event-stream" {
		t.Fatalf("Accept = %q", got)
	}
	if got := proxyReq.Header.Get("Originator"); got != "clipal-test" {
		t.Fatalf("Originator = %q", got)
	}
	if got := proxyReq.Header.Get("User-Agent"); got != "clipal-test/1.0" {
		t.Fatalf("User-Agent = %q", got)
	}
	if got := proxyReq.Header.Get("X-Codex-Turn-Metadata"); got != `{"turn_id":"turn-1"}` {
		t.Fatalf("X-Codex-Turn-Metadata = %q", got)
	}
	if got := proxyReq.Header.Get("X-Client-Request-Id"); got != "req-123" {
		t.Fatalf("X-Client-Request-Id = %q", got)
	}
	if got := proxyReq.Header.Get("Session-Id"); got != "session-123" {
		t.Fatalf("Session-Id = %q", got)
	}
	if got := proxyReq.Header.Get("Thread-Id"); got != "thread-123" {
		t.Fatalf("Thread-Id = %q", got)
	}
	if got := proxyReq.Header.Get("X-Codex-Installation-Id"); got != "install-123" {
		t.Fatalf("X-Codex-Installation-Id = %q", got)
	}
	if got := proxyReq.Header.Get("X-Codex-Window-Id"); got != "thread-123:0" {
		t.Fatalf("X-Codex-Window-Id = %q", got)
	}
	if got := proxyReq.Header.Get("X-Codex-Parent-Thread-Id"); got != "parent-thread-1" {
		t.Fatalf("X-Codex-Parent-Thread-Id = %q", got)
	}
	if got := proxyReq.Header.Get("X-Codex-Beta-Features"); got != "feature-a" {
		t.Fatalf("X-Codex-Beta-Features = %q", got)
	}
	if got := proxyReq.Header.Get("X-OpenAI-Subagent"); got != "review" {
		t.Fatalf("X-OpenAI-Subagent = %q", got)
	}
	if got := proxyReq.Header.Get("Cookie"); got != "" {
		t.Fatalf("Cookie = %q, want empty", got)
	}
	if got := proxyReq.Header.Get("X-Test"); got != "" {
		t.Fatalf("X-Test = %q, want empty", got)
	}
	if got := proxyReq.Header.Get("Session_id"); got != "" {
		t.Fatalf("Session_id = %q, want empty", got)
	}
	if got := proxyReq.Header.Get("OpenAI-Beta"); got != "" {
		t.Fatalf("OpenAI-Beta = %q, want empty", got)
	}

	root := decodeRequestBodyMap(t, proxyReq)
	if got := root["stream"]; got != true {
		t.Fatalf("stream = %v", got)
	}
	if got := root["prompt_cache_key"]; got != "thread-123" {
		t.Fatalf("prompt_cache_key = %v", got)
	}
	metadata, ok := root["client_metadata"].(map[string]any)
	if !ok {
		t.Fatalf("client_metadata = %T %#v", root["client_metadata"], root["client_metadata"])
	}
	if got := metadata["x-codex-installation-id"]; got != "install-123" {
		t.Fatalf("client_metadata.x-codex-installation-id = %v", got)
	}
}

func TestForwardWithFailover_CodexOAuthNonStreamingSynthesizesResponsesJSON(t *testing.T) {
	dir := t.TempDir()
	svc := oauthpkg.NewService(dir)
	if err := svc.Store().Save(&oauthpkg.Credential{
		Ref:         "codex-sean-example-com",
		Provider:    config.OAuthProviderCodex,
		Email:       "sean@example.com",
		AccountID:   "acct_123",
		AccessToken: "access-1",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var upstreamPath string
	var upstreamAccept string
	var upstreamRoot map[string]any
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		upstreamPath = r.URL.Path
		upstreamAccept = r.Header.Get("Accept")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll upstream body: %v", err)
		}
		if err := json.Unmarshal(body, &upstreamRoot); err != nil {
			t.Fatalf("json.Unmarshal upstream body: %v body=%s", err, string(body))
		}
		h := make(http.Header)
		h.Set("Content-Encoding", "gzip")
		h.Set("Content-Length", "999")
		return newResponse(http.StatusOK, h, "event: response.completed\n"+
			`data: {"type":"response.completed","response":{"id":"resp_123","object":"response","created_at":1710000000,"model":"gpt-5.2","status":"completed","output":[{"id":"msg_123","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"hello","annotations":[]}]}],"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}}`+
			"\n\n"), nil
	})

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{
			Name:          "codex-oauth",
			AuthType:      config.ProviderAuthTypeOAuth,
			OAuthProvider: config.OAuthProviderCodex,
			OAuthRef:      "codex-sean-example-com",
			Priority:      1,
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = svc
	cp.httpClient.Transport = rt

	body := []byte(`{"model":"gpt-5.2","stream":false,"input":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/responses", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withRequestContext(req, RequestContext{
		ClientType:     ClientOpenAI,
		Family:         ProtocolFamilyOpenAI,
		Capability:     CapabilityOpenAIResponses,
		UpstreamPath:   "/v1/responses",
		UnifiedIngress: true,
	})
	rr := httptest.NewRecorder()

	cp.forwardWithFailover(rr, req, "/v1/responses")

	if got := rr.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d body=%s", got, rr.Body.String())
	}
	if upstreamPath != "/backend-api/codex/responses" {
		t.Fatalf("upstream path = %q", upstreamPath)
	}
	if upstreamAccept != "text/event-stream" {
		t.Fatalf("upstream Accept = %q", upstreamAccept)
	}
	if got := upstreamRoot["stream"]; got != true {
		t.Fatalf("upstream stream = %v", got)
	}
	if got := upstreamRoot["store"]; got != false {
		t.Fatalf("upstream store = %v", got)
	}
	if got := rr.Result().Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := rr.Result().Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want empty", got)
	}
	if got := rr.Result().Header.Get("Content-Length"); got != "" {
		t.Fatalf("Content-Length = %q, want empty", got)
	}
	root := decodeRawJSONMap(t, rr.Body.Bytes())
	if got := root["id"]; got != "resp_123" {
		t.Fatalf("id = %v", got)
	}
	if got := root["status"]; got != "completed" {
		t.Fatalf("status field = %v", got)
	}
	output, ok := root["output"].([]any)
	if !ok || len(output) != 1 {
		t.Fatalf("output = %#v", root["output"])
	}
}

func TestForwardManual_CodexOAuthNonStreamingSynthesizesResponsesJSON(t *testing.T) {
	dir := t.TempDir()
	svc := oauthpkg.NewService(dir)
	if err := svc.Store().Save(&oauthpkg.Credential{
		Ref:         "codex-sean-example-com",
		Provider:    config.OAuthProviderCodex,
		Email:       "sean@example.com",
		AccountID:   "acct_123",
		AccessToken: "access-1",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var upstreamPath string
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		upstreamPath = r.URL.Path
		h := make(http.Header)
		h.Set("Content-Type", "text/event-stream")
		return newResponse(http.StatusOK, h, "event: response.completed\n"+
			`data: {"type":"response.completed","response":{"id":"resp_manual","object":"response","created_at":1710000000,"model":"gpt-5.2","status":"completed","output":[]}}`+
			"\n\n"), nil
	})

	cp := newClientProxy(ClientOpenAI, config.ClientModeManual, "codex-oauth", []config.Provider{
		{
			Name:          "codex-oauth",
			AuthType:      config.ProviderAuthTypeOAuth,
			OAuthProvider: config.OAuthProviderCodex,
			OAuthRef:      "codex-sean-example-com",
			Priority:      1,
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = svc
	cp.httpClient.Transport = rt

	body := []byte(`{"model":"gpt-5.2","input":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/responses", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withRequestContext(req, RequestContext{
		ClientType:     ClientOpenAI,
		Family:         ProtocolFamilyOpenAI,
		Capability:     CapabilityOpenAIResponses,
		UpstreamPath:   "/v1/responses",
		UnifiedIngress: true,
	})
	rr := httptest.NewRecorder()

	cp.forwardManual(rr, req, "/v1/responses")

	if got := rr.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d body=%s", got, rr.Body.String())
	}
	if upstreamPath != "/backend-api/codex/responses" {
		t.Fatalf("upstream path = %q", upstreamPath)
	}
	if got := rr.Result().Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
	root := decodeRawJSONMap(t, rr.Body.Bytes())
	if got := root["id"]; got != "resp_manual" {
		t.Fatalf("id = %v", got)
	}
}

func TestForwardWithFailover_CodexOAuthNonStreamingReturnsFailedResponse(t *testing.T) {
	dir := t.TempDir()
	svc := oauthpkg.NewService(dir)
	if err := svc.Store().Save(&oauthpkg.Credential{
		Ref:         "codex-sean-example-com",
		Provider:    config.OAuthProviderCodex,
		Email:       "sean@example.com",
		AccountID:   "acct_123",
		AccessToken: "access-1",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var calls int32
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		h := make(http.Header)
		h.Set("Content-Type", "text/event-stream")
		return newResponse(http.StatusOK, h, "event: response.failed\n"+
			`data: {"type":"response.failed","response":{"id":"resp_failed","object":"response","created_at":1710000000,"status":"failed","error":{"code":"rate_limit_exceeded","message":"try later"},"output":[]}}`+
			"\n\n"), nil
	})

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{
			Name:          "codex-oauth",
			AuthType:      config.ProviderAuthTypeOAuth,
			OAuthProvider: config.OAuthProviderCodex,
			OAuthRef:      "codex-sean-example-com",
			Priority:      1,
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = svc
	cp.httpClient.Transport = rt

	body := []byte(`{"model":"gpt-5.2","input":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/responses", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withRequestContext(req, RequestContext{
		ClientType:     ClientOpenAI,
		Family:         ProtocolFamilyOpenAI,
		Capability:     CapabilityOpenAIResponses,
		UpstreamPath:   "/v1/responses",
		UnifiedIngress: true,
	})
	rr := httptest.NewRecorder()

	cp.forwardWithFailover(rr, req, "/v1/responses")

	if got := rr.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d body=%s", got, rr.Body.String())
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("calls = %d, want 1", got)
	}
	root := decodeRawJSONMap(t, rr.Body.Bytes())
	if got := root["status"]; got != "failed" {
		t.Fatalf("status field = %v", got)
	}
	upstreamErr, ok := root["error"].(map[string]any)
	if !ok || upstreamErr["code"] != "rate_limit_exceeded" {
		t.Fatalf("error = %#v", root["error"])
	}
}

func TestSynthesizeCodexOAuthResponsesJSONReturnsFailedEventResponse(t *testing.T) {
	body := []byte("event: response.failed\n" +
		`data: {"type":"response.failed","response":{"id":"resp_123","object":"response","created_at":1710000000,"status":"failed","error":{"code":"rate_limit_exceeded","message":"try later"},"output":[]}}` +
		"\n\n")
	rewritten, completed, err := synthesizeCodexOAuthResponsesJSON(body)
	if err != nil {
		t.Fatalf("synthesizeCodexOAuthResponsesJSON: %v", err)
	}
	if !completed {
		t.Fatalf("completed = false, want true")
	}
	root := decodeRawJSONMap(t, rewritten)
	if got := root["status"]; got != "failed" {
		t.Fatalf("status = %v", got)
	}
	upstreamErr, ok := root["error"].(map[string]any)
	if !ok {
		t.Fatalf("error = %T %#v", root["error"], root["error"])
	}
	if got := upstreamErr["code"]; got != "rate_limit_exceeded" {
		t.Fatalf("error.code = %v", got)
	}
}

func TestSynthesizeCodexOAuthResponsesJSONReturnsIncompleteEventResponse(t *testing.T) {
	body := []byte("event: response.incomplete\n" +
		`data: {"type":"response.incomplete","response":{"id":"resp_123","object":"response","created_at":1710000000,"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[]}}` +
		"\n\n")
	rewritten, completed, err := synthesizeCodexOAuthResponsesJSON(body)
	if err != nil {
		t.Fatalf("synthesizeCodexOAuthResponsesJSON: %v", err)
	}
	if !completed {
		t.Fatalf("completed = false, want true")
	}
	root := decodeRawJSONMap(t, rewritten)
	if got := root["status"]; got != "incomplete" {
		t.Fatalf("status = %v", got)
	}
	details, ok := root["incomplete_details"].(map[string]any)
	if !ok || details["reason"] != "max_output_tokens" {
		t.Fatalf("incomplete_details = %#v", root["incomplete_details"])
	}
}

func TestSynthesizeCodexOAuthResponsesJSONDoesNotCompleteOnDoneOnly(t *testing.T) {
	body := []byte("event: response.output_text.delta\n" +
		`data: {"type":"response.output_text.delta","delta":"hello"}` +
		"\n\n" +
		"data: [DONE]\n\n")
	_, completed, err := synthesizeCodexOAuthResponsesJSON(body)
	if err != nil {
		t.Fatalf("synthesizeCodexOAuthResponsesJSON: %v", err)
	}
	if completed {
		t.Fatalf("completed = true, want false")
	}
}

func TestSynthesizeCodexOAuthResponsesJSONBuildsOutputFromTextDone(t *testing.T) {
	body := []byte("event: response.output_text.delta\n" +
		`data: {"type":"response.output_text.delta","delta":"hel"}` +
		"\n\n" +
		"event: response.output_text.done\n" +
		`data: {"type":"response.output_text.done","text":"hello"}` +
		"\n\n" +
		"event: response.completed\n" +
		`data: {"type":"response.completed"}` +
		"\n\n")
	rewritten, completed, err := synthesizeCodexOAuthResponsesJSON(body)
	if err != nil {
		t.Fatalf("synthesizeCodexOAuthResponsesJSON: %v", err)
	}
	if !completed {
		t.Fatalf("completed = false, want true")
	}
	root := decodeRawJSONMap(t, rewritten)
	output, ok := root["output"].([]any)
	if !ok || len(output) != 1 {
		t.Fatalf("output = %#v", root["output"])
	}
	msg, ok := output[0].(map[string]any)
	if !ok {
		t.Fatalf("output[0] = %T %#v", output[0], output[0])
	}
	content, ok := msg["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("content = %#v", msg["content"])
	}
	part, ok := content[0].(map[string]any)
	if !ok || part["text"] != "hello" {
		t.Fatalf("content[0] = %#v", content[0])
	}
}

func TestSynthesizeCodexOAuthResponsesJSONFillsEmptyCompletedOutputFromItemDone(t *testing.T) {
	body := []byte("event: response.created\n" +
		`data: {"type":"response.created","response":{"id":"resp_live","object":"response","created_at":1710000000,"model":"gpt-5.5","status":"in_progress","output":[]}}` +
		"\n\n" +
		"event: response.output_item.added\n" +
		`data: {"type":"response.output_item.added","item":{"id":"msg_live","type":"message","status":"in_progress","content":[],"role":"assistant"},"output_index":0}` +
		"\n\n" +
		"event: response.content_part.added\n" +
		`data: {"type":"response.content_part.added","content_index":0,"item_id":"msg_live","output_index":0,"part":{"type":"output_text","annotations":[],"text":""}}` +
		"\n\n" +
		"event: response.output_text.delta\n" +
		`data: {"type":"response.output_text.delta","content_index":0,"delta":"CLIPAL","item_id":"msg_live","output_index":0}` +
		"\n\n" +
		"event: response.output_text.done\n" +
		`data: {"type":"response.output_text.done","content_index":0,"item_id":"msg_live","output_index":0,"text":"CLIPAL_CODEX_NONSTREAM_OK"}` +
		"\n\n" +
		"event: response.content_part.done\n" +
		`data: {"type":"response.content_part.done","content_index":0,"item_id":"msg_live","output_index":0,"part":{"type":"output_text","annotations":[],"text":"CLIPAL_CODEX_NONSTREAM_OK"}}` +
		"\n\n" +
		"event: response.output_item.done\n" +
		`data: {"type":"response.output_item.done","item":{"id":"msg_live","type":"message","status":"completed","content":[{"type":"output_text","annotations":[],"text":"CLIPAL_CODEX_NONSTREAM_OK"}],"role":"assistant"},"output_index":0}` +
		"\n\n" +
		"event: response.completed\n" +
		`data: {"type":"response.completed","response":{"id":"resp_live","object":"response","created_at":1710000000,"model":"gpt-5.5","status":"completed","output":[],"usage":{"input_tokens":17,"output_tokens":11,"total_tokens":28}}}` +
		"\n\n")
	rewritten, completed, err := synthesizeCodexOAuthResponsesJSON(body)
	if err != nil {
		t.Fatalf("synthesizeCodexOAuthResponsesJSON: %v", err)
	}
	if !completed {
		t.Fatalf("completed = false, want true")
	}
	root := decodeRawJSONMap(t, rewritten)
	if got := root["id"]; got != "resp_live" {
		t.Fatalf("id = %v", got)
	}
	if got := root["status"]; got != "completed" {
		t.Fatalf("status = %v", got)
	}
	if _, ok := root["usage"].(map[string]any); !ok {
		t.Fatalf("usage = %#v", root["usage"])
	}
	output, ok := root["output"].([]any)
	if !ok || len(output) != 1 {
		t.Fatalf("output = %#v", root["output"])
	}
	msg, ok := output[0].(map[string]any)
	if !ok || msg["id"] != "msg_live" {
		t.Fatalf("output[0] = %#v", output[0])
	}
	content, ok := msg["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("content = %#v", msg["content"])
	}
	part, ok := content[0].(map[string]any)
	if !ok || part["text"] != "CLIPAL_CODEX_NONSTREAM_OK" {
		t.Fatalf("content[0] = %#v", content[0])
	}
}

func TestSynthesizeCodexOAuthResponsesJSONBuildsOutputFromItemDone(t *testing.T) {
	body := []byte("event: response.output_item.done\n" +
		`data: {"type":"response.output_item.done","item":{"id":"msg_123","type":"message","status":"completed","role":"assistant","content":[]}}` +
		"\n\n" +
		"event: response.completed\n" +
		`data: {"type":"response.completed"}` +
		"\n\n")
	rewritten, completed, err := synthesizeCodexOAuthResponsesJSON(body)
	if err != nil {
		t.Fatalf("synthesizeCodexOAuthResponsesJSON: %v", err)
	}
	if !completed {
		t.Fatalf("completed = false, want true")
	}
	root := decodeRawJSONMap(t, rewritten)
	output, ok := root["output"].([]any)
	if !ok || len(output) != 1 {
		t.Fatalf("output = %#v", root["output"])
	}
	item, ok := output[0].(map[string]any)
	if !ok || item["id"] != "msg_123" {
		t.Fatalf("output[0] = %#v", output[0])
	}
}

func TestShouldSynthesizeCodexOAuthNonStreamingResponseUsesResponsesDefaults(t *testing.T) {
	body := []byte(`{"model":"gpt-5.2","input":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/responses", bytes.NewReader(body))
	req = withRequestContext(req, RequestContext{
		ClientType:     ClientOpenAI,
		Family:         ProtocolFamilyOpenAI,
		Capability:     CapabilityOpenAIResponses,
		UpstreamPath:   "/v1/responses",
		UnifiedIngress: true,
	})
	provider := config.Provider{
		Name:          "codex-oauth",
		AuthType:      config.ProviderAuthTypeOAuth,
		OAuthProvider: config.OAuthProviderCodex,
	}

	if !shouldSynthesizeCodexOAuthNonStreamingResponse(req, provider, "/clipal/v1/responses", newRequestPayload(body)) {
		t.Fatalf("expected non-stream responses request to synthesize even when stream is omitted")
	}
	streamBody := []byte(`{"model":"gpt-5.2","stream":true,"input":"hello"}`)
	if shouldSynthesizeCodexOAuthNonStreamingResponse(req, provider, "/v1/responses", newRequestPayload(streamBody)) {
		t.Fatalf("did not expect explicit stream=true request to synthesize")
	}
}

func TestCreateProxyRequest_CodexOAuthLatestHeadersAndLegacySessionID(t *testing.T) {
	dir := t.TempDir()
	svc := oauthpkg.NewService(dir)
	if err := svc.Store().Save(&oauthpkg.Credential{
		Ref:         "codex-sean-example-com",
		Provider:    config.OAuthProviderCodex,
		Email:       "sean@example.com",
		AccountID:   "acct_123",
		AccessToken: "access-1",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{
			Name:          "codex-oauth",
			AuthType:      config.ProviderAuthTypeOAuth,
			OAuthProvider: config.OAuthProviderCodex,
			OAuthRef:      "codex-sean-example-com",
			Priority:      1,
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = svc

	body := []byte(`{"model":"gpt-5.2","stream":true,"input":"hello"}`)
	original := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/responses", bytes.NewReader(body))
	original.Header.Set("Content-Type", "application/json")
	original.Header.Set("Session_id", "legacy-session-123")
	original.Header.Set("Thread-Id", "thread-123")
	original.Header.Set("Version", "0.134.0")
	original.Header.Add("OpenAI-Beta", "responses=experimental")
	original.Header.Add("OpenAI-Beta", "responses_websockets=2026-02-06")
	original.Header.Set("X-ResponsesAPI-Include-Timing-Metrics", "true")
	original.Header.Set("X-Codex-Turn-State", "turn-state-123")
	original = withRequestContext(original, RequestContext{
		ClientType:     ClientOpenAI,
		Family:         ProtocolFamilyOpenAI,
		Capability:     CapabilityOpenAIResponses,
		UpstreamPath:   "/v1/responses",
		UnifiedIngress: true,
	})

	proxyReq, err := cp.createProxyRequest(original, cp.providers[0], "", "/v1/responses", body)
	if err != nil {
		t.Fatalf("createProxyRequest: %v", err)
	}
	if got := proxyReq.Header.Get("Session_id"); got != "" {
		t.Fatalf("Session_id = %q, want empty", got)
	}
	if got := proxyReq.Header.Get("Session-Id"); got != "legacy-session-123" {
		t.Fatalf("Session-Id = %q", got)
	}
	if got := proxyReq.Header.Get("Thread-Id"); got != "thread-123" {
		t.Fatalf("Thread-Id = %q", got)
	}
	if got := proxyReq.Header.Get("X-Client-Request-Id"); got != "thread-123" {
		t.Fatalf("X-Client-Request-Id = %q", got)
	}
	if got := proxyReq.Header.Get("Version"); got != "0.134.0" {
		t.Fatalf("Version = %q", got)
	}
	if got := proxyReq.Header.Get("OpenAI-Beta"); got != "responses_websockets=2026-02-06" {
		t.Fatalf("OpenAI-Beta = %q", got)
	}
	if got := proxyReq.Header.Values("OpenAI-Beta"); len(got) != 1 {
		t.Fatalf("OpenAI-Beta values = %#v, want one filtered value", got)
	}
	if got := proxyReq.Header.Get("X-ResponsesAPI-Include-Timing-Metrics"); got != "true" {
		t.Fatalf("X-ResponsesAPI-Include-Timing-Metrics = %q", got)
	}
	if got := proxyReq.Header.Get("X-Codex-Turn-State"); got != "turn-state-123" {
		t.Fatalf("X-Codex-Turn-State = %q", got)
	}
}

func TestCreateProxyRequest_CodexOAuthPreservesAndCompletesBodyContext(t *testing.T) {
	dir := t.TempDir()
	svc := oauthpkg.NewService(dir)
	if err := svc.Store().Save(&oauthpkg.Credential{
		Ref:         "codex-sean-example-com",
		Provider:    config.OAuthProviderCodex,
		Email:       "sean@example.com",
		AccountID:   "acct_123",
		AccessToken: "access-1",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{
			Name:          "codex-oauth",
			AuthType:      config.ProviderAuthTypeOAuth,
			OAuthProvider: config.OAuthProviderCodex,
			OAuthRef:      "codex-sean-example-com",
			Priority:      1,
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = svc

	body := []byte(`{
		"model":"gpt-5.2",
		"stream":true,
		"input":"hello",
		"prompt_cache_key":"client-cache-key",
		"client_metadata":{"x-codex-installation-id":"client-install","custom":"keep"},
		"reasoning":{"effort":"high"},
		"include":["file_search_call.results"]
	}`)
	original := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/responses", bytes.NewReader(body))
	original.Header.Set("Content-Type", "application/json")
	original.Header.Set("Thread-Id", "thread-123")
	original.Header.Set("X-Codex-Installation-Id", "header-install")
	original = withRequestContext(original, RequestContext{
		ClientType:     ClientOpenAI,
		Family:         ProtocolFamilyOpenAI,
		Capability:     CapabilityOpenAIResponses,
		UpstreamPath:   "/v1/responses",
		UnifiedIngress: true,
	})

	proxyReq, err := cp.createProxyRequest(original, cp.providers[0], "", "/v1/responses", body)
	if err != nil {
		t.Fatalf("createProxyRequest: %v", err)
	}
	root := decodeRequestBodyMap(t, proxyReq)
	if got := root["prompt_cache_key"]; got != "client-cache-key" {
		t.Fatalf("prompt_cache_key = %v", got)
	}
	metadata, ok := root["client_metadata"].(map[string]any)
	if !ok {
		t.Fatalf("client_metadata = %T %#v", root["client_metadata"], root["client_metadata"])
	}
	if got := metadata["x-codex-installation-id"]; got != "client-install" {
		t.Fatalf("client_metadata.x-codex-installation-id = %v", got)
	}
	if got := metadata["custom"]; got != "keep" {
		t.Fatalf("client_metadata.custom = %v", got)
	}
	include, ok := root["include"].([]any)
	if !ok {
		t.Fatalf("include = %T %#v", root["include"], root["include"])
	}
	if !containsStringValue(include, "file_search_call.results") {
		t.Fatalf("include missing existing value: %#v", include)
	}
	if !containsStringValue(include, "reasoning.encrypted_content") {
		t.Fatalf("include missing reasoning.encrypted_content: %#v", include)
	}
}

func TestBuildCodexOAuthRequest_NormalizesResponsesBodyForCompatibility(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5.2",
		"stream":true,
		"store":true,
		"functions":[{"name":"apply_patch","parameters":{"type":"object"}}],
		"function_call":{"name":"apply_patch"},
		"prompt_cache_retention":{"ttl":"1h"},
		"max_output_tokens":2048,
		"temperature":0.2,
		"input":[
			{"type":"message","role":"system","content":"be strict"},
			{"type":"message","role":"user","content":"hello"}
		]
	}`)

	targetPath, stream, rewritten, err := buildCodexOAuthRequest("/v1/responses", body)
	if err != nil {
		t.Fatalf("buildCodexOAuthRequest: %v", err)
	}
	if targetPath != "/responses" {
		t.Fatalf("targetPath = %q", targetPath)
	}
	if !stream {
		t.Fatalf("stream = false, want true")
	}

	var root map[string]any
	if err := json.Unmarshal(rewritten, &root); err != nil {
		t.Fatalf("json.Unmarshal: %v body=%s", err, string(rewritten))
	}
	if got := root["store"]; got != false {
		t.Fatalf("store = %v", got)
	}
	if got := root["instructions"]; got != "be strict" {
		t.Fatalf("instructions = %#v", got)
	}
	if _, ok := root["functions"]; ok {
		t.Fatalf("functions should be removed: %#v", root["functions"])
	}
	if _, ok := root["function_call"]; ok {
		t.Fatalf("function_call should be removed: %#v", root["function_call"])
	}
	if _, ok := root["prompt_cache_retention"]; ok {
		t.Fatalf("prompt_cache_retention should be removed: %#v", root["prompt_cache_retention"])
	}
	if _, ok := root["max_output_tokens"]; ok {
		t.Fatalf("max_output_tokens should be removed: %#v", root["max_output_tokens"])
	}
	if _, ok := root["temperature"]; ok {
		t.Fatalf("temperature should be removed: %#v", root["temperature"])
	}
	tools, ok := root["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v", root["tools"])
	}
	toolChoice, ok := root["tool_choice"].(map[string]any)
	if !ok {
		t.Fatalf("tool_choice = %T %#v", root["tool_choice"], root["tool_choice"])
	}
	function, ok := toolChoice["function"].(map[string]any)
	if !ok || function["name"] != "apply_patch" {
		t.Fatalf("tool_choice.function = %#v", toolChoice["function"])
	}
	input, ok := root["input"].([]any)
	if !ok || len(input) != 1 {
		t.Fatalf("input = %#v", root["input"])
	}
	msg, ok := input[0].(map[string]any)
	if !ok || msg["role"] != "user" {
		t.Fatalf("input[0] = %#v", input[0])
	}
}

func TestBuildCodexOAuthRequest_CompactEndpointKeepsCanonicalFields(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5.2",
		"input":[{"type":"message","role":"user","content":"hello"}],
		"instructions":"compact carefully",
		"tools":[{"type":"function","function":{"name":"apply_patch"}}],
		"parallel_tool_calls":true,
		"reasoning":{"effort":"high","summary":"auto"},
		"service_tier":"priority",
		"prompt_cache_key":"thread-123",
		"text":{"verbosity":"low"},
		"stream":true,
		"store":true,
		"stream_options":{"include_usage":true}
	}`)

	targetPath, stream, rewritten, err := buildCodexOAuthRequest("/v1/responses/compact", body)
	if err != nil {
		t.Fatalf("buildCodexOAuthRequest: %v", err)
	}
	if targetPath != "/responses/compact" {
		t.Fatalf("targetPath = %q", targetPath)
	}
	if stream {
		t.Fatalf("stream = true, want false")
	}

	var root map[string]any
	if err := json.Unmarshal(rewritten, &root); err != nil {
		t.Fatalf("json.Unmarshal: %v body=%s", err, string(rewritten))
	}
	for _, key := range []string{
		"model",
		"input",
		"instructions",
		"tools",
		"parallel_tool_calls",
		"reasoning",
		"service_tier",
		"prompt_cache_key",
		"text",
	} {
		if _, ok := root[key]; !ok {
			t.Fatalf("expected %q to be preserved in compact body: %#v", key, root)
		}
	}
	if _, ok := root["stream"]; ok {
		t.Fatalf("did not expect stream in compact body: %#v", root["stream"])
	}
	if _, ok := root["store"]; ok {
		t.Fatalf("did not expect store in compact body: %#v", root["store"])
	}
	if _, ok := root["stream_options"]; ok {
		t.Fatalf("did not expect stream_options in compact body: %#v", root["stream_options"])
	}
	if _, ok := root["include"]; ok {
		t.Fatalf("did not expect include in compact body: %#v", root["include"])
	}
}

func TestCreateProxyRequest_CodexOAuthIgnoresProviderBaseURL(t *testing.T) {
	dir := t.TempDir()
	svc := oauthpkg.NewService(dir)
	if err := svc.Store().Save(&oauthpkg.Credential{
		Ref:         "codex-sean-example-com",
		Provider:    config.OAuthProviderCodex,
		Email:       "sean@example.com",
		AccountID:   "acct_123",
		AccessToken: "access-1",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{
			Name:          "codex-oauth",
			BaseURL:       "https://should-not-be-used.example/codex",
			AuthType:      config.ProviderAuthTypeOAuth,
			OAuthProvider: config.OAuthProviderCodex,
			OAuthRef:      "codex-sean-example-com",
			Priority:      1,
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = svc

	body := []byte(`{"model":"gpt-4.1","input":"hello"}`)
	original := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/responses", bytes.NewReader(body))
	original.Header.Set("Content-Type", "application/json")
	original = withRequestContext(original, RequestContext{
		ClientType:     ClientOpenAI,
		Family:         ProtocolFamilyOpenAI,
		Capability:     CapabilityOpenAIResponses,
		UpstreamPath:   "/v1/responses",
		UnifiedIngress: true,
	})

	proxyReq, err := cp.createProxyRequest(original, cp.providers[0], "", "/v1/responses", body)
	if err != nil {
		t.Fatalf("createProxyRequest: %v", err)
	}
	if got := proxyReq.URL.String(); got != "https://chatgpt.com/backend-api/codex/responses" {
		t.Fatalf("url = %q", got)
	}
}

func TestCreateProxyRequest_CodexOAuthRefreshesExpiredCredential(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 4, 18, 21, 0, 0, 0, time.UTC)
	var refreshCalls int32

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if got := r.Form.Get("grant_type"); got != "refresh_token" {
			t.Fatalf("grant_type = %q", got)
		}
		atomic.AddInt32(&refreshCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(`{"access_token":"access-2","refresh_token":"refresh-2","id_token":"%s","expires_in":3600}`, testCodexJWT("sean@example.com", "acct_123")))
	}))
	defer tokenServer.Close()

	svc := oauthpkg.NewService(dir,
		oauthpkg.WithNowFunc(func() time.Time { return now }),
		oauthpkg.WithRefreshSkew(30*time.Second),
		oauthpkg.WithCodexClient(&oauthpkg.CodexClient{
			AuthURL:      "https://auth.openai.com/oauth/authorize",
			TokenURL:     tokenServer.URL,
			ClientID:     "test-client",
			CallbackHost: "127.0.0.1",
			CallbackPort: 0,
			CallbackPath: "/auth/callback",
			HTTPClient:   tokenServer.Client(),
			Now:          func() time.Time { return now },
		}),
	)
	if err := svc.Store().Save(&oauthpkg.Credential{
		Ref:          "codex-sean-example-com",
		Provider:     config.OAuthProviderCodex,
		Email:        "sean@example.com",
		AccountID:    "acct_123",
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		ExpiresAt:    now.Add(5 * time.Second),
		LastRefresh:  now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{
			Name:          "codex-oauth",
			AuthType:      config.ProviderAuthTypeOAuth,
			OAuthProvider: config.OAuthProviderCodex,
			OAuthRef:      "codex-sean-example-com",
			Priority:      1,
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = svc

	body := []byte(`{"model":"gpt-4.1","input":"hello"}`)
	original := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/responses", bytes.NewReader(body))
	original.Header.Set("Content-Type", "application/json")
	original = withRequestContext(original, RequestContext{
		ClientType:     ClientOpenAI,
		Family:         ProtocolFamilyOpenAI,
		Capability:     CapabilityOpenAIResponses,
		UpstreamPath:   "/v1/responses",
		UnifiedIngress: true,
	})

	proxyReq, err := cp.createProxyRequest(original, cp.providers[0], "", "/v1/responses", body)
	if err != nil {
		t.Fatalf("createProxyRequest: %v", err)
	}
	if got := proxyReq.Header.Get("Authorization"); got != "Bearer access-2" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := atomic.LoadInt32(&refreshCalls); got != 1 {
		t.Fatalf("refresh calls = %d, want 1", got)
	}

	loaded, err := svc.Load(config.OAuthProviderCodex, "codex-sean-example-com")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.AccessToken != "access-2" {
		t.Fatalf("stored access token = %q", loaded.AccessToken)
	}
	if loaded.RefreshToken != "refresh-2" {
		t.Fatalf("stored refresh token = %q", loaded.RefreshToken)
	}
}

func TestForwardWithFailover_CodexOAuthBuildFailureFallsBackToAPIKeyProvider(t *testing.T) {
	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{
			Name:          "codex-oauth",
			AuthType:      config.ProviderAuthTypeOAuth,
			OAuthProvider: config.OAuthProviderCodex,
			OAuthRef:      "missing-credential",
			Priority:      1,
		},
		{
			Name:     "openai-api",
			BaseURL:  "http://api.example",
			APIKey:   "provider-key",
			Priority: 2,
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = oauthpkg.NewService(t.TempDir())

	var calls int32
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		if got := r.URL.String(); got != "http://api.example/v1/responses" {
			t.Fatalf("url = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer provider-key" {
			t.Fatalf("Authorization = %q", got)
		}
		return newResponse(http.StatusOK, http.Header{"Content-Type": []string{"application/json"}}, `{"ok":true}`), nil
	})
	cp.httpClient.Transport = rt

	rr := httptest.NewRecorder()
	body := []byte(`{"model":"gpt-4.1","input":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/responses", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withRequestContext(req, RequestContext{
		ClientType:     ClientOpenAI,
		Family:         ProtocolFamilyOpenAI,
		Capability:     CapabilityOpenAIResponses,
		UpstreamPath:   "/v1/responses",
		UnifiedIngress: true,
	})

	cp.forwardWithFailover(rr, req, "/v1/responses")

	if got := rr.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d body=%s", got, rr.Body.String())
	}
	if got := rr.Body.String(); got != `{"ok":true}` {
		t.Fatalf("body = %q", got)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("upstream calls = %d, want 1", got)
	}
}

func TestForwardWithFailover_CodexOAuthSkipsUnsupportedCapabilityInAutoMode(t *testing.T) {
	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{
			Name:          "codex-oauth",
			AuthType:      config.ProviderAuthTypeOAuth,
			OAuthProvider: config.OAuthProviderCodex,
			OAuthRef:      "codex-sean-example-com",
			Priority:      1,
		},
		{
			Name:     "openai-api",
			BaseURL:  "http://api.example",
			APIKey:   "provider-key",
			Priority: 2,
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = oauthpkg.NewService(t.TempDir())

	var calls int32
	cp.httpClient.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		if got := r.URL.String(); got != "http://api.example/v1/chat/completions" {
			t.Fatalf("url = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer provider-key" {
			t.Fatalf("Authorization = %q", got)
		}
		return newResponse(http.StatusOK, http.Header{"Content-Type": []string{"application/json"}}, `{"ok":true}`), nil
	})

	body := []byte(`{"model":"gpt-4.1","messages":[{"role":"user","content":"hello"}]}`)
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withRequestContext(req, RequestContext{
		ClientType:     ClientOpenAI,
		Family:         ProtocolFamilyOpenAI,
		Capability:     CapabilityOpenAIChatCompletions,
		UpstreamPath:   "/v1/chat/completions",
		UnifiedIngress: true,
	})

	rr := httptest.NewRecorder()
	cp.forwardWithFailover(rr, req, "/v1/chat/completions")

	if got := rr.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d body=%s", got, rr.Body.String())
	}
	if got := rr.Body.String(); got != `{"ok":true}` {
		t.Fatalf("body = %q", got)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("upstream calls = %d, want 1", got)
	}
}

func TestForwardManual_CodexOAuthRejectsUnsupportedCapability(t *testing.T) {
	cp := newClientProxy(ClientOpenAI, config.ClientModeManual, "codex-oauth", []config.Provider{
		{
			Name:          "codex-oauth",
			AuthType:      config.ProviderAuthTypeOAuth,
			OAuthProvider: config.OAuthProviderCodex,
			OAuthRef:      "codex-sean-example-com",
			Priority:      1,
		},
		{
			Name:     "openai-api",
			BaseURL:  "http://api.example",
			APIKey:   "provider-key",
			Priority: 2,
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = oauthpkg.NewService(t.TempDir())

	var calls int32
	cp.httpClient.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return newResponse(http.StatusOK, http.Header{"Content-Type": []string{"application/json"}}, `{"ok":true}`), nil
	})

	body := []byte(`{"model":"gpt-4.1","messages":[{"role":"user","content":"hello"}]}`)
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withRequestContext(req, RequestContext{
		ClientType:     ClientOpenAI,
		Family:         ProtocolFamilyOpenAI,
		Capability:     CapabilityOpenAIChatCompletions,
		UpstreamPath:   "/v1/chat/completions",
		UnifiedIngress: true,
	})

	rr := httptest.NewRecorder()
	cp.forwardWithFailover(rr, req, "/v1/chat/completions")

	if got := rr.Result().StatusCode; got != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body=%s", got, rr.Body.String())
	}
	if got := rr.Body.String(); got != "Pinned provider only supports OpenAI Responses requests.\n" {
		t.Fatalf("body = %q", got)
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("upstream calls = %d, want 0", got)
	}
}

func TestForwardWithFailover_CodexOAuthRefreshesAndRetriesOn401(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 4, 20, 8, 0, 0, 0, time.UTC)
	var refreshCalls int32
	var upstreamCalls int32

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if got := r.Form.Get("grant_type"); got != "refresh_token" {
			t.Fatalf("grant_type = %q", got)
		}
		atomic.AddInt32(&refreshCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(`{"access_token":"access-2","refresh_token":"refresh-2","id_token":"%s","expires_in":3600}`, testCodexJWT("sean@example.com", "acct_123")))
	}))
	defer tokenServer.Close()

	svc := oauthpkg.NewService(dir,
		oauthpkg.WithNowFunc(func() time.Time { return now }),
		oauthpkg.WithCodexClient(&oauthpkg.CodexClient{
			AuthURL:      "https://auth.openai.com/oauth/authorize",
			TokenURL:     tokenServer.URL,
			ClientID:     "test-client",
			CallbackHost: "127.0.0.1",
			CallbackPort: 0,
			CallbackPath: "/auth/callback",
			HTTPClient:   tokenServer.Client(),
			Now:          func() time.Time { return now },
		}),
	)
	if err := svc.Store().Save(&oauthpkg.Credential{
		Ref:          "codex-sean-example-com",
		Provider:     config.OAuthProviderCodex,
		Email:        "sean@example.com",
		AccountID:    "acct_123",
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		ExpiresAt:    now.Add(time.Hour),
		LastRefresh:  now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{
			Name:          "codex-oauth",
			AuthType:      config.ProviderAuthTypeOAuth,
			OAuthProvider: config.OAuthProviderCodex,
			OAuthRef:      "codex-sean-example-com",
			Priority:      1,
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = svc
	var firstSessionID, firstThreadID, firstInstallationID string
	cp.httpClient.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		call := atomic.AddInt32(&upstreamCalls, 1)
		switch call {
		case 1:
			if got := r.Header.Get("Authorization"); got != "Bearer access-1" {
				t.Fatalf("Authorization(first) = %q", got)
			}
			firstSessionID = r.Header.Get("Session-Id")
			firstThreadID = r.Header.Get("Thread-Id")
			firstInstallationID = r.Header.Get("X-Codex-Installation-Id")
			return newResponse(http.StatusUnauthorized, http.Header{"Content-Type": []string{"application/json"}}, `{"error":{"type":"authentication_error","code":"token_invalid","message":"expired"}}`), nil
		case 2:
			if got := r.Header.Get("Authorization"); got != "Bearer access-2" {
				t.Fatalf("Authorization(second) = %q", got)
			}
			if got := r.Header.Get("Session-Id"); got == "" || got != firstSessionID {
				t.Fatalf("Session-Id(second) = %q, want first %q", got, firstSessionID)
			}
			if got := r.Header.Get("Thread-Id"); got == "" || got != firstThreadID {
				t.Fatalf("Thread-Id(second) = %q, want first %q", got, firstThreadID)
			}
			if got := r.Header.Get("X-Codex-Installation-Id"); got == "" || got != firstInstallationID {
				t.Fatalf("X-Codex-Installation-Id(second) = %q, want first %q", got, firstInstallationID)
			}
			return newResponse(http.StatusOK, http.Header{"Content-Type": []string{"application/json"}}, `{"ok":true}`), nil
		default:
			t.Fatalf("unexpected upstream call %d", call)
			return nil, nil
		}
	})

	rr := httptest.NewRecorder()
	body := []byte(`{"model":"gpt-5.2","input":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/responses", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withRequestContext(req, RequestContext{
		ClientType:     ClientOpenAI,
		Family:         ProtocolFamilyOpenAI,
		Capability:     CapabilityOpenAIResponses,
		UpstreamPath:   "/v1/responses",
		UnifiedIngress: true,
	})

	cp.forwardWithFailover(rr, req, "/v1/responses")

	if got := rr.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d body=%s", got, rr.Body.String())
	}
	if got := rr.Body.String(); got != `{"ok":true}` {
		t.Fatalf("body = %q", got)
	}
	if got := atomic.LoadInt32(&refreshCalls); got != 1 {
		t.Fatalf("refresh calls = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&upstreamCalls); got != 2 {
		t.Fatalf("upstream calls = %d, want 2", got)
	}
	if cp.isDeactivated(0) {
		t.Fatalf("provider should remain active after successful retry")
	}
	if cp.isKeyDeactivated(0, 0) {
		t.Fatalf("provider key should remain active after successful retry")
	}
}

func TestForwardWithFailover_CodexOAuthUsesUsageResetForCooldown(t *testing.T) {
	dir := t.TempDir()
	resetAt := time.Now().Add(5 * time.Hour).UTC()
	var usageCalls int32

	usageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&usageCalls, 1)
		if got := r.Header.Get("Authorization"); got != "Bearer access-1" {
			t.Fatalf("usage authorization = %q", got)
		}
		if got := r.Header.Get("Chatgpt-Account-Id"); got != "acct_123" {
			t.Fatalf("usage account = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(`{
  "plan_type": "pro",
  "rate_limit": {
    "allowed": false,
    "limit_reached": true,
    "primary_window": {
      "used_percent": 100,
      "window_minutes": 300,
      "reset_at": %d
    }
  }
}`, resetAt.Unix()))
	}))
	defer usageServer.Close()

	svc := oauthpkg.NewService(dir,
		oauthpkg.WithCodexClient(&oauthpkg.CodexClient{
			AuthURL:      "https://auth.openai.com/oauth/authorize",
			TokenURL:     "https://auth.openai.com/oauth/token",
			UsageURL:     usageServer.URL,
			ClientID:     "test-client",
			CallbackHost: "127.0.0.1",
			CallbackPort: 0,
			CallbackPath: "/auth/callback",
			HTTPClient:   usageServer.Client(),
			Now:          time.Now,
		}),
	)
	if err := svc.Store().Save(&oauthpkg.Credential{
		Ref:         "codex-sean-example-com",
		Provider:    config.OAuthProviderCodex,
		Email:       "sean@example.com",
		AccountID:   "acct_123",
		AccessToken: "access-1",
		ExpiresAt:   time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var codexCalls int32
	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{
			Name:          "codex-oauth",
			AuthType:      config.ProviderAuthTypeOAuth,
			OAuthProvider: config.OAuthProviderCodex,
			OAuthRef:      "codex-sean-example-com",
			Priority:      1,
		},
		{Name: "fallback", BaseURL: "http://fallback", APIKey: "k2", Priority: 2},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = svc
	cp.httpClient.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "chatgpt.com":
			atomic.AddInt32(&codexCalls, 1)
			h := make(http.Header)
			h.Set("Content-Type", "application/json")
			return newResponse(http.StatusTooManyRequests, h, `{"error":{"message":"rate limit","type":"rate_limit_exceeded","code":"rate_limit_exceeded"}}`), nil
		case "fallback":
			return newResponse(http.StatusOK, nil, "ok"), nil
		default:
			t.Fatalf("unexpected host %q", r.URL.Host)
			return nil, nil
		}
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/responses", bytes.NewReader([]byte(`{"model":"gpt-5.2","input":"hello"}`)))
	req.Header.Set("Content-Type", "application/json")
	req = withRequestContext(req, RequestContext{
		ClientType:     ClientOpenAI,
		Family:         ProtocolFamilyOpenAI,
		Capability:     CapabilityOpenAIResponses,
		UpstreamPath:   "/v1/responses",
		UnifiedIngress: true,
	})

	cp.forwardWithFailover(rr, req, "/v1/responses")

	if got := rr.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d body=%s", got, rr.Body.String())
	}
	if got := atomic.LoadInt32(&codexCalls); got != 1 {
		t.Fatalf("codex calls = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&usageCalls); got != 1 {
		t.Fatalf("usage calls = %d, want 1", got)
	}
	if !cp.isDeactivated(0) {
		t.Fatalf("expected codex oauth provider to be in cooldown")
	}
	if remaining := time.Until(cp.deactivationUntil(0)); remaining < 4*time.Hour {
		t.Fatalf("expected cooldown from usage reset, got %s", remaining)
	}
}

func TestForwardManual_CodexOAuthRefreshesAndRetriesOn401(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 4, 20, 8, 0, 0, 0, time.UTC)
	var refreshCalls int32
	var upstreamCalls int32

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if got := r.Form.Get("grant_type"); got != "refresh_token" {
			t.Fatalf("grant_type = %q", got)
		}
		atomic.AddInt32(&refreshCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(`{"access_token":"access-2","refresh_token":"refresh-2","id_token":"%s","expires_in":3600}`, testCodexJWT("sean@example.com", "acct_123")))
	}))
	defer tokenServer.Close()

	svc := oauthpkg.NewService(dir,
		oauthpkg.WithNowFunc(func() time.Time { return now }),
		oauthpkg.WithCodexClient(&oauthpkg.CodexClient{
			AuthURL:      "https://auth.openai.com/oauth/authorize",
			TokenURL:     tokenServer.URL,
			ClientID:     "test-client",
			CallbackHost: "127.0.0.1",
			CallbackPort: 0,
			CallbackPath: "/auth/callback",
			HTTPClient:   tokenServer.Client(),
			Now:          func() time.Time { return now },
		}),
	)
	if err := svc.Store().Save(&oauthpkg.Credential{
		Ref:          "codex-sean-example-com",
		Provider:     config.OAuthProviderCodex,
		Email:        "sean@example.com",
		AccountID:    "acct_123",
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		ExpiresAt:    now.Add(time.Hour),
		LastRefresh:  now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cp := newClientProxy(ClientOpenAI, config.ClientModeManual, "codex-oauth", []config.Provider{
		{
			Name:          "codex-oauth",
			AuthType:      config.ProviderAuthTypeOAuth,
			OAuthProvider: config.OAuthProviderCodex,
			OAuthRef:      "codex-sean-example-com",
			Priority:      1,
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = svc
	var firstSessionID, firstThreadID, firstInstallationID string
	cp.httpClient.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		call := atomic.AddInt32(&upstreamCalls, 1)
		switch call {
		case 1:
			if got := r.Header.Get("Authorization"); got != "Bearer access-1" {
				t.Fatalf("Authorization(first) = %q", got)
			}
			firstSessionID = r.Header.Get("Session-Id")
			firstThreadID = r.Header.Get("Thread-Id")
			firstInstallationID = r.Header.Get("X-Codex-Installation-Id")
			return newResponse(http.StatusUnauthorized, http.Header{"Content-Type": []string{"application/json"}}, `{"error":{"type":"authentication_error","code":"token_invalid","message":"expired"}}`), nil
		case 2:
			if got := r.Header.Get("Authorization"); got != "Bearer access-2" {
				t.Fatalf("Authorization(second) = %q", got)
			}
			if got := r.Header.Get("Session-Id"); got == "" || got != firstSessionID {
				t.Fatalf("Session-Id(second) = %q, want first %q", got, firstSessionID)
			}
			if got := r.Header.Get("Thread-Id"); got == "" || got != firstThreadID {
				t.Fatalf("Thread-Id(second) = %q, want first %q", got, firstThreadID)
			}
			if got := r.Header.Get("X-Codex-Installation-Id"); got == "" || got != firstInstallationID {
				t.Fatalf("X-Codex-Installation-Id(second) = %q, want first %q", got, firstInstallationID)
			}
			return newResponse(http.StatusOK, http.Header{"Content-Type": []string{"application/json"}}, `{"ok":true}`), nil
		default:
			t.Fatalf("unexpected upstream call %d", call)
			return nil, nil
		}
	})

	rr := httptest.NewRecorder()
	body := []byte(`{"model":"gpt-5.2","input":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/responses", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withRequestContext(req, RequestContext{
		ClientType:     ClientOpenAI,
		Family:         ProtocolFamilyOpenAI,
		Capability:     CapabilityOpenAIResponses,
		UpstreamPath:   "/v1/responses",
		UnifiedIngress: true,
	})

	cp.forwardManual(rr, req, "/v1/responses")

	if got := rr.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d body=%s", got, rr.Body.String())
	}
	if got := rr.Body.String(); got != `{"ok":true}` {
		t.Fatalf("body = %q", got)
	}
	if got := atomic.LoadInt32(&refreshCalls); got != 1 {
		t.Fatalf("refresh calls = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&upstreamCalls); got != 2 {
		t.Fatalf("upstream calls = %d, want 2", got)
	}
}

func testCodexJWT(email string, accountID string) string {
	header := `{"alg":"none","typ":"JWT"}`
	payload := fmt.Sprintf(`{"email":"%s","sub":"sub_123","https://api.openai.com/auth":{"chatgpt_account_id":"%s"}}`, email, accountID)
	return base64.RawURLEncoding.EncodeToString([]byte(header)) + "." +
		base64.RawURLEncoding.EncodeToString([]byte(payload)) + "."
}

func containsStringValue(values []any, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
