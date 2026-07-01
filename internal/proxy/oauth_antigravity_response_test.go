package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lansespirit/Clipal/internal/config"
)

func TestPrepareOAuthProviderResponse_RewritesAntigravityModelsDedicatedAdapter(t *testing.T) {
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
