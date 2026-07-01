package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
	oauthpkg "github.com/lansespirit/Clipal/internal/oauth"
)

func TestForwardWithFailover_GeminiOAuthUnwrapsGenerateResponse(t *testing.T) {
	dir := t.TempDir()
	svc := oauthpkg.NewService(dir)
	if err := svc.Store().Save(&oauthpkg.Credential{
		Ref:         "gemini-sean-example-com-gen-lang-client-123",
		Provider:    config.OAuthProviderGemini,
		Email:       "sean@example.com",
		AccessToken: "access-1",
		Metadata: map[string]string{
			"project_id": "gen-lang-client-123",
		},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cp := newClientProxy(ClientGemini, config.ClientModeAuto, "", []config.Provider{
		{
			Name:          "gemini-oauth",
			AuthType:      config.ProviderAuthTypeOAuth,
			OAuthProvider: config.OAuthProviderGemini,
			OAuthRef:      "gemini-sean-example-com-gen-lang-client-123",
			Priority:      1,
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = svc

	var calls int32
	cp.httpClient.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return newResponse(http.StatusOK, http.Header{"Content-Type": []string{"application/json"}}, `{"response":{"candidates":[{"content":{"parts":[{"text":"hello"}]}}],"usageMetadata":{"totalTokenCount":3}},"traceId":"trace-1","remainingCredits":[{"creditType":"GOOGLE_ONE_AI","creditAmount":"7"}]}`), nil
	})

	rr := httptest.NewRecorder()
	body := []byte(`{"contents":[{"parts":[{"text":"hello"}]}]}`)
	req := httptest.NewRequest(http.MethodPost, "http://proxy/gemini/v1beta/models/gemini-2.5-pro:generateContent", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withRequestContext(req, RequestContext{
		ClientType:     ClientGemini,
		Family:         ProtocolFamilyGemini,
		Capability:     CapabilityGeminiGenerateContent,
		UpstreamPath:   "/v1beta/models/gemini-2.5-pro:generateContent",
		UnifiedIngress: true,
	})

	cp.forwardWithFailover(rr, req, "/v1beta/models/gemini-2.5-pro:generateContent")

	if got := rr.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d body=%s", got, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Fatalf("Content-Type = %q", got)
	}
	bodyText := rr.Body.String()
	if strings.Contains(bodyText, `"response":`) {
		t.Fatalf("body still contains Cloud Code envelope: %s", bodyText)
	}
	if !strings.Contains(bodyText, `"responseId":"trace-1"`) {
		t.Fatalf("body missing responseId: %s", bodyText)
	}
	if !strings.Contains(bodyText, `"candidates"`) {
		t.Fatalf("body missing candidates: %s", bodyText)
	}
	if !strings.Contains(bodyText, `"usageMetadata"`) {
		t.Fatalf("body missing usageMetadata: %s", bodyText)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("upstream calls = %d, want 1", got)
	}
}

func TestPrepareGeminiOAuthResponse_UnwrapsGenerateResponseWithoutContentType(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://proxy/gemini/v1beta/models/gemini-2.5-pro:generateContent", nil)
	req = withRequestContext(req, RequestContext{
		ClientType:     ClientGemini,
		Family:         ProtocolFamilyGemini,
		Capability:     CapabilityGeminiGenerateContent,
		UpstreamPath:   "/v1beta/models/gemini-2.5-pro:generateContent",
		UnifiedIngress: true,
	})
	resp := newResponse(http.StatusOK, nil, `{"response":{"candidates":[{"content":{"parts":[{"text":"hello"}]}}]},"traceId":"trace-1"}`)

	rewritten, err := prepareGeminiOAuthResponse(req, resp)
	if err != nil {
		t.Fatalf("prepareGeminiOAuthResponse: %v", err)
	}
	body, err := io.ReadAll(rewritten.Body)
	if err != nil {
		t.Fatalf("io.ReadAll: %v", err)
	}
	bodyText := string(body)
	if strings.Contains(bodyText, `"response":`) {
		t.Fatalf("body still contains Cloud Code envelope: %s", bodyText)
	}
	if !strings.Contains(bodyText, `"responseId":"trace-1"`) {
		t.Fatalf("body missing responseId: %s", bodyText)
	}
}

func TestPrepareOAuthProviderResponse_RewritesAntigravityModels(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://proxy/gemini/v1beta/models", nil)
	req = withRequestContext(req, RequestContext{
		ClientType:     ClientGemini,
		Family:         ProtocolFamilyGemini,
		Capability:     CapabilityGeminiModels,
		UpstreamPath:   "/v1beta/models",
		UnifiedIngress: true,
	})
	resp := newResponse(http.StatusOK, http.Header{"Content-Type": []string{"application/json"}}, `{
		"models": {
			"gemini-3.1-flash-image": {"displayName": "Nano Banana 2", "maxTokens": 1048576},
			"veo-3.1": {"displayName": "Veo 3.1"}
		}
	}`)

	rewritten, err := prepareOAuthProviderResponse(req, config.Provider{
		AuthType:      config.ProviderAuthTypeOAuth,
		OAuthProvider: config.OAuthProviderAntigravity,
	}, resp)
	if err != nil {
		t.Fatalf("prepareOAuthProviderResponse: %v", err)
	}
	body, err := io.ReadAll(rewritten.Body)
	if err != nil {
		t.Fatalf("io.ReadAll: %v", err)
	}
	bodyText := string(body)
	if !strings.Contains(bodyText, `"name":"models/gemini-3.1-flash-image"`) {
		t.Fatalf("body missing Gemini model name: %s", bodyText)
	}
	if !strings.Contains(bodyText, `"displayName":"Nano Banana 2"`) {
		t.Fatalf("body missing displayName: %s", bodyText)
	}
	if !strings.Contains(bodyText, `"supportedGenerationMethods":["generateContent","streamGenerateContent","countTokens"]`) {
		t.Fatalf("body missing generation methods: %s", bodyText)
	}
}

func TestForwardWithFailover_GeminiOAuthRewritesStreamResponse(t *testing.T) {
	dir := t.TempDir()
	svc := oauthpkg.NewService(dir)
	if err := svc.Store().Save(&oauthpkg.Credential{
		Ref:         "gemini-sean-example-com-gen-lang-client-123",
		Provider:    config.OAuthProviderGemini,
		Email:       "sean@example.com",
		AccessToken: "access-1",
		Metadata: map[string]string{
			"project_id": "gen-lang-client-123",
		},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cp := newClientProxy(ClientGemini, config.ClientModeAuto, "", []config.Provider{
		{
			Name:          "gemini-oauth",
			AuthType:      config.ProviderAuthTypeOAuth,
			OAuthProvider: config.OAuthProviderGemini,
			OAuthRef:      "gemini-sean-example-com-gen-lang-client-123",
			Priority:      1,
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = svc

	streamBody := strings.Join([]string{
		"event: message",
		`data: {"response":{"candidates":[{"content":{"parts":[{"text":"hello"}]}}]},"traceId":"trace-1"}`,
		"",
		`data: {"remainingCredits":[{"creditType":"GOOGLE_ONE_AI","creditAmount":"5"}]}`,
		"",
		`data: {"response":{"usageMetadata":{"totalTokenCount":9}},"traceId":"trace-1"}`,
		"",
	}, "\n")
	cp.httpClient.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return newResponse(http.StatusOK, http.Header{"Content-Type": []string{"text/event-stream"}}, streamBody), nil
	})

	rr := httptest.NewRecorder()
	body := []byte(`{"contents":[{"parts":[{"text":"hello"}]}]}`)
	req := httptest.NewRequest(http.MethodPost, "http://proxy/gemini/v1beta/models/gemini-2.5-pro:streamGenerateContent", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withRequestContext(req, RequestContext{
		ClientType:     ClientGemini,
		Family:         ProtocolFamilyGemini,
		Capability:     CapabilityGeminiStreamGenerate,
		UpstreamPath:   "/v1beta/models/gemini-2.5-pro:streamGenerateContent",
		UnifiedIngress: true,
	})

	cp.forwardWithFailover(rr, req, "/v1beta/models/gemini-2.5-pro:streamGenerateContent")

	if got := rr.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d body=%s", got, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("Content-Type = %q", got)
	}
	bodyText := rr.Body.String()
	if strings.Contains(bodyText, `"response":`) {
		t.Fatalf("stream still contains Cloud Code envelope: %s", bodyText)
	}
	if strings.Contains(bodyText, `"remainingCredits"`) {
		t.Fatalf("stream still contains Cloud Code-only metadata chunk: %s", bodyText)
	}
	if !strings.Contains(bodyText, `"responseId":"trace-1"`) {
		t.Fatalf("stream missing responseId: %s", bodyText)
	}
	if !strings.Contains(bodyText, `"candidates"`) {
		t.Fatalf("stream missing candidates chunk: %s", bodyText)
	}
	if !strings.Contains(bodyText, `"usageMetadata"`) {
		t.Fatalf("stream missing usageMetadata chunk: %s", bodyText)
	}
}

func TestPrepareGeminiOAuthResponse_RewritesStreamWithoutContentType(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://proxy/gemini/v1beta/models/gemini-2.5-pro:streamGenerateContent", nil)
	req = withRequestContext(req, RequestContext{
		ClientType:     ClientGemini,
		Family:         ProtocolFamilyGemini,
		Capability:     CapabilityGeminiStreamGenerate,
		UpstreamPath:   "/v1beta/models/gemini-2.5-pro:streamGenerateContent",
		UnifiedIngress: true,
	})
	streamBody := strings.Join([]string{
		"id: 123",
		`data: {"response":`,
		`data: {"candidates":[{"content":{"parts":[{"text":"hello"}]}}]},`,
		`data: "traceId":"trace-1"}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	resp := newResponse(http.StatusOK, nil, streamBody)

	rewritten, err := prepareGeminiOAuthResponse(req, resp)
	if err != nil {
		t.Fatalf("prepareGeminiOAuthResponse: %v", err)
	}
	body, err := io.ReadAll(rewritten.Body)
	if err != nil {
		t.Fatalf("io.ReadAll: %v", err)
	}
	bodyText := string(body)
	if strings.Contains(bodyText, `"response":`) {
		t.Fatalf("stream still contains Cloud Code envelope: %s", bodyText)
	}
	if !strings.Contains(bodyText, "id: 123") {
		t.Fatalf("stream did not preserve id line: %s", bodyText)
	}
	if !strings.Contains(bodyText, `"responseId":"trace-1"`) {
		t.Fatalf("stream missing responseId: %s", bodyText)
	}
	if !strings.Contains(bodyText, "data: [DONE]") {
		t.Fatalf("stream did not preserve done event: %s", bodyText)
	}
}
