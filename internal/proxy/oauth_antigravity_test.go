package proxy

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PAIArtCom/Clipal/internal/config"
	oauthpkg "github.com/PAIArtCom/Clipal/internal/oauth"
)

func TestAntigravityOAuthFallbackVersionMatchesCurrentRelease(t *testing.T) {
	if antigravityOAuthFallbackVersion != "2.13.0" {
		t.Fatalf("antigravityOAuthFallbackVersion = %q, want 2.13.0", antigravityOAuthFallbackVersion)
	}
}

func TestAntigravitySetupFailureIsActionableBeforeGeneration(t *testing.T) {
	for _, mode := range []config.ClientMode{config.ClientModeAuto, config.ClientModeManual} {
		for _, tc := range []struct {
			kind    string
			status  int
			message string
		}{
			{"project_required", http.StatusConflict, "requires a user-provided Google Cloud project"},
			{"tier_unavailable", http.StatusForbidden, "UNSUPPORTED_LOCATION"},
		} {
			t.Run(string(mode)+"/"+tc.kind, func(t *testing.T) {
				client := &stubGeminiOAuthClient{provider: config.OAuthProviderAntigravity, refreshErr: &oauthpkg.AntigravitySetupError{Kind: tc.kind, TierID: "standard-tier", ReasonCodes: []string{"UNSUPPORTED_LOCATION"}}}
				svc := oauthpkg.NewService(t.TempDir(), oauthpkg.WithProviderClient(client))
				cred := &oauthpkg.Credential{Ref: "stable", Provider: config.OAuthProviderAntigravity, Email: "account@example.com", AccessToken: "private-access", RefreshToken: "private-refresh", ExpiresAt: time.Now().Add(time.Hour)}
				if err := svc.Store().Save(cred); err != nil {
					t.Fatal(err)
				}
				cp := newClientProxy(ClientGoogle, mode, "target", []config.Provider{{Name: "target", AuthType: config.ProviderAuthTypeOAuth, OAuthProvider: config.OAuthProviderAntigravity, OAuthRef: cred.Ref, Priority: 1}}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
				cp.oauth = svc
				cp.httpClient.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
					t.Error("generation was sent despite failed account setup")
					return newResponse(200, nil, `{}`), nil
				})
				req := httptest.NewRequest(http.MethodPost, "http://proxy/gemini/v1beta/models/gemini-3-flash:generateContent", strings.NewReader(`{"contents":[{"parts":[{"text":"hello"}]}]}`))
				w := httptest.NewRecorder()
				cp.forwardWithFailover(w, req, "/v1beta/models/gemini-3-flash:generateContent")
				if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.message) {
					t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
				}
				if strings.Contains(w.Body.String(), "private-") {
					t.Fatal("account secret was disclosed")
				}
			})
		}
	}
}

func TestCreateProxyRequest_AntigravityOAuthRebuildsUpstreamContextAndWrapsGenerate(t *testing.T) {
	dir := t.TempDir()
	svc := oauthpkg.NewService(dir)
	if err := svc.Store().Save(&oauthpkg.Credential{
		Ref:         "antigravity-sean-example-com-project-1",
		Provider:    config.OAuthProviderAntigravity,
		Email:       "sean@example.com",
		AccessToken: "access-1",
		Metadata: map[string]string{
			"project_id": "project-1",
		},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cp := newClientProxy(ClientGoogle, config.ClientModeAuto, "", []config.Provider{
		{
			Name:          "antigravity-oauth",
			AuthType:      config.ProviderAuthTypeOAuth,
			OAuthProvider: config.OAuthProviderAntigravity,
			OAuthRef:      "antigravity-sean-example-com-project-1",
			Priority:      1,
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = svc

	body := []byte(`{"model":"client-model","project":"client-project","requestId":"request-1","contents":[{"parts":[{"text":"hello"}]}],"generationConfig":{"responseModalities":["IMAGE","TEXT"]},"userPromptId":"prompt-1","requestType":"ANTIGRAVITY","enabledCreditTypes":["GOOGLE_ONE_AI"]}`)
	original := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1beta/models/gemini-3.1-flash-image:generateContent?key=client-key&api_key=client-api-key&alt=json", bytes.NewReader(body))
	original.Header.Set("Content-Type", "application/json")
	original.Header.Set("Authorization", "Bearer client-token")
	original.Header.Set("x-goog-api-key", "client-key")
	original.Header.Set("Cookie", "secret=1")
	original.Header.Set("Proxy-Authorization", "Basic dGVzdA==")
	original.Header.Set("X-Goog-Api-Client", "public-sdk")
	original.Header.Set("Client-Metadata", `{"client":"public"}`)
	original.Header.Set("X-Goog-User-Project", "client-billing-project")
	original.Header.Set("X-Goog-Request-Reason", "client-request-reason")
	original.Header.Set("X-Antigravity-Session", "client-session")
	original.Header.Set("X-Cloud-Code-Project", "client-project")
	original = withRequestContext(original, RequestContext{
		ClientType:     ClientGoogle,
		Family:         ProtocolFamilyGemini,
		Capability:     CapabilityGeminiGenerateContent,
		UpstreamPath:   "/v1beta/models/gemini-3.1-flash-image:generateContent",
		UnifiedIngress: true,
	})

	proxyReq, err := cp.createProxyRequest(original, cp.providers[0], "", "/v1beta/models/gemini-3.1-flash-image:generateContent", body)
	if err != nil {
		t.Fatalf("createProxyRequest: %v", err)
	}
	if got := proxyReq.URL.String(); got != "https://daily-cloudcode-pa.googleapis.com/v1internal:generateContent" {
		t.Fatalf("url = %q", got)
	}
	if got := proxyReq.Method; got != http.MethodPost {
		t.Fatalf("method = %q", got)
	}
	if got := proxyReq.Header.Get("Authorization"); got != "Bearer access-1" {
		t.Fatalf("Authorization = %q", got)
	}
	for _, key := range []string{
		"x-goog-api-key",
		"Cookie",
		"Proxy-Authorization",
		"X-Goog-Api-Client",
		"Client-Metadata",
		"X-Goog-User-Project",
		"X-Goog-Request-Reason",
		"X-Antigravity-Session",
		"X-Cloud-Code-Project",
	} {
		if got := proxyReq.Header.Get(key); got != "" {
			t.Fatalf("%s = %q, want empty", key, got)
		}
	}
	if got := proxyReq.Header.Get("User-Agent"); got != antigravityOAuthUserAgent() {
		t.Fatalf("User-Agent = %q", got)
	}
	if got := proxyReq.Header.Get("Accept"); got != "application/json" {
		t.Fatalf("Accept = %q", got)
	}
	if got := proxyReq.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}

	root := decodeRequestBodyMap(t, proxyReq)
	if got := root["model"]; got != "gemini-3.1-flash-image" {
		t.Fatalf("model = %v", got)
	}
	if got := root["project"]; got != "project-1" {
		t.Fatalf("project = %v", got)
	}
	if got := root["requestId"]; got != "request-1" {
		t.Fatalf("requestId = %v", got)
	}
	if got := root["userPromptId"]; got != "prompt-1" {
		t.Fatalf("userPromptId = %v", got)
	}
	if got := root["requestType"]; got != "ANTIGRAVITY" {
		t.Fatalf("requestType = %v", got)
	}
	if got := root["enabledCreditTypes"]; got == nil {
		t.Fatalf("enabledCreditTypes missing: %#v", root)
	}
	request, ok := root["request"].(map[string]any)
	if !ok {
		t.Fatalf("request = %#v, want object", root["request"])
	}
	for _, key := range []string{"model", "project", "request_id", "requestId", "user_prompt_id", "userPromptId", "request_type", "requestType", "enabled_credit_types", "enabledCreditTypes"} {
		if _, ok := request[key]; ok {
			t.Fatalf("did not expect nested %s in request: %#v", key, request)
		}
	}
	if _, ok := request["generationConfig"]; !ok {
		t.Fatalf("request.generationConfig missing: %#v", request)
	}
}

func TestCreateProxyRequest_AntigravityOAuthStreamForcesAltSSE(t *testing.T) {
	dir := t.TempDir()
	svc := oauthpkg.NewService(dir)
	if err := svc.Store().Save(&oauthpkg.Credential{
		Ref:         "antigravity-sean-example-com-project-1",
		Provider:    config.OAuthProviderAntigravity,
		Email:       "sean@example.com",
		AccessToken: "access-1",
		Metadata: map[string]string{
			"project_id": "project-1",
		},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cp := newClientProxy(ClientGoogle, config.ClientModeAuto, "", []config.Provider{
		{
			Name:          "antigravity-oauth",
			AuthType:      config.ProviderAuthTypeOAuth,
			OAuthProvider: config.OAuthProviderAntigravity,
			OAuthRef:      "antigravity-sean-example-com-project-1",
			Priority:      1,
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = svc

	body := []byte(`{"contents":[]}`)
	original := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1beta/models/gemini-3.1-pro:streamGenerateContent?alt=json&key=client-key", bytes.NewReader(body))
	original = withRequestContext(original, RequestContext{
		ClientType:     ClientGoogle,
		Family:         ProtocolFamilyGemini,
		Capability:     CapabilityGeminiStreamGenerate,
		UpstreamPath:   "/v1beta/models/gemini-3.1-pro:streamGenerateContent",
		UnifiedIngress: true,
	})

	proxyReq, err := cp.createProxyRequest(original, cp.providers[0], "", "/v1beta/models/gemini-3.1-pro:streamGenerateContent", body)
	if err != nil {
		t.Fatalf("createProxyRequest: %v", err)
	}
	if got := proxyReq.URL.String(); got != "https://daily-cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse" {
		t.Fatalf("url = %q", got)
	}
	if got := proxyReq.Header.Get("Accept"); got != "text/event-stream" {
		t.Fatalf("Accept = %q", got)
	}
}

func TestCreateProxyRequest_AntigravityOAuthCountTokensPreservesOfficialFields(t *testing.T) {
	dir := t.TempDir()
	svc := oauthpkg.NewService(dir)
	if err := svc.Store().Save(&oauthpkg.Credential{
		Ref:         "antigravity-sean-example-com-project-1",
		Provider:    config.OAuthProviderAntigravity,
		Email:       "sean@example.com",
		AccessToken: "access-1",
		Metadata: map[string]string{
			"project_id": "project-1",
		},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cp := newClientProxy(ClientGoogle, config.ClientModeAuto, "", []config.Provider{
		{
			Name:          "antigravity-oauth",
			AuthType:      config.ProviderAuthTypeOAuth,
			OAuthProvider: config.OAuthProviderAntigravity,
			OAuthRef:      "antigravity-sean-example-com-project-1",
			Priority:      1,
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = svc

	body := []byte(`{
		"model":"client-model",
		"contents":[{"parts":[{"text":"count me"}]}],
		"generationConfig":{"temperature":0.2},
		"systemInstruction":{"parts":[{"text":"system"}]},
		"tools":[{"googleSearch":{}}],
		"cachedContent":"cachedContents/123"
	}`)
	original := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1beta/models/gemini-3.1-pro:countTokens", bytes.NewReader(body))
	original = withRequestContext(original, RequestContext{
		ClientType:     ClientGoogle,
		Family:         ProtocolFamilyGemini,
		Capability:     CapabilityGeminiCountTokens,
		UpstreamPath:   "/v1beta/models/gemini-3.1-pro:countTokens",
		UnifiedIngress: true,
	})

	proxyReq, err := cp.createProxyRequest(original, cp.providers[0], "", "/v1beta/models/gemini-3.1-pro:countTokens", body)
	if err != nil {
		t.Fatalf("createProxyRequest: %v", err)
	}
	if got := proxyReq.URL.String(); got != "https://daily-cloudcode-pa.googleapis.com/v1internal:countTokens" {
		t.Fatalf("url = %q", got)
	}

	root := decodeRequestBodyMap(t, proxyReq)
	request, ok := root["request"].(map[string]any)
	if !ok {
		t.Fatalf("request = %#v, want object", root["request"])
	}
	if got := request["model"]; got != "models/gemini-3.1-pro" {
		t.Fatalf("request.model = %v", got)
	}
	for _, key := range []string{"contents", "generationConfig", "systemInstruction", "tools", "cachedContent"} {
		if _, ok := request[key]; !ok {
			t.Fatalf("request.%s missing: %#v", key, request)
		}
	}
}

func TestBuildAntigravityOAuthRequestRejectsInvalidJSON(t *testing.T) {
	_, _, err := buildAntigravityOAuthRequest(CapabilityGeminiGenerateContent, "/v1beta/models/gemini-3.1-pro:generateContent", []byte(`{"contents":`), "project-1")
	if err == nil {
		t.Fatalf("expected invalid JSON error")
	}
	if got, want := err.Error(), "antigravity oauth request body must be valid json"; !strings.Contains(got, want) {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestBuildAntigravityOAuthRequestRejectsUnsupportedCapability(t *testing.T) {
	_, _, err := buildAntigravityOAuthRequest(CapabilityGeminiPredictLongRunning, "/v1beta/models/veo-3.1:predictLongRunning", []byte(`{"instances":[]}`), "project-1")
	if err == nil {
		t.Fatalf("expected unsupported capability error")
	}
	if got, want := err.Error(), "antigravity oauth does not support"; !strings.Contains(got, want) {
		t.Fatalf("error = %q, want %q", got, want)
	}
}
