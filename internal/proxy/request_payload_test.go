package proxy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lansespirit/Clipal/internal/config"
)

func TestRequestPayloadCachesCodexOAuthRewrite(t *testing.T) {
	body := []byte(`{"model":"gpt-5.2","stream":false,"input":"hello"}`)
	payload := newRequestPayload(body)
	req := httptest.NewRequest(http.MethodPost, "http://proxy/v1/responses", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	requestCtx := RequestContext{
		ClientType: ClientOpenAI,
		Family:     ProtocolFamilyOpenAI,
		Capability: CapabilityOpenAIResponses,
	}
	provider := config.Provider{
		Name: "codex",
	}

	_, _, first, err := payload.codexOAuthRequest(req, requestCtx, provider, "/v1/responses")
	if err != nil {
		t.Fatalf("codexOAuthRequest first: %v", err)
	}
	_, _, second, err := payload.codexOAuthRequest(req, requestCtx, provider, "/v1/responses")
	if err != nil {
		t.Fatalf("codexOAuthRequest second: %v", err)
	}
	if len(first) == 0 || len(second) == 0 || &first[0] != &second[0] {
		t.Fatalf("expected cached codex rewrite body to be reused")
	}
}

func TestRequestPayloadCachesCodexOAuthProviderOverridesSeparately(t *testing.T) {
	body := []byte(`{"model":"gpt-5.2","stream":false,"input":"hello"}`)
	payload := newRequestPayload(body)
	req := httptest.NewRequest(http.MethodPost, "http://proxy/v1/responses", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	requestCtx := RequestContext{
		ClientType: ClientOpenAI,
		Family:     ProtocolFamilyOpenAI,
		Capability: CapabilityOpenAIResponses,
	}
	firstProvider := config.Provider{
		Name: "codex",
		Overrides: &config.ProviderOverrides{
			Model: strPtr("gpt-5.2-first"),
		},
	}
	secondProvider := config.Provider{
		Name: "codex",
		Overrides: &config.ProviderOverrides{
			Model: strPtr("gpt-5.2-second"),
		},
	}

	_, _, first, err := payload.codexOAuthRequest(req, requestCtx, firstProvider, "/v1/responses")
	if err != nil {
		t.Fatalf("codexOAuthRequest first provider: %v", err)
	}
	_, _, second, err := payload.codexOAuthRequest(req, requestCtx, secondProvider, "/v1/responses")
	if err != nil {
		t.Fatalf("codexOAuthRequest second provider: %v", err)
	}
	_, _, firstAgain, err := payload.codexOAuthRequest(req, requestCtx, firstProvider, "/v1/responses")
	if err != nil {
		t.Fatalf("codexOAuthRequest first provider again: %v", err)
	}
	if len(first) == 0 || len(firstAgain) == 0 || &first[0] != &firstAgain[0] {
		t.Fatalf("expected first provider cached codex rewrite body to be reused")
	}
	if len(second) == 0 || &first[0] == &second[0] {
		t.Fatalf("expected provider override rewrites to use separate cache entries")
	}

	firstRoot := decodeRawJSONMap(t, first)
	secondRoot := decodeRawJSONMap(t, second)
	if got := firstRoot["model"]; got != "gpt-5.2-first" {
		t.Fatalf("first model = %v", got)
	}
	if got := secondRoot["model"]; got != "gpt-5.2-second" {
		t.Fatalf("second model = %v", got)
	}
}

func TestRequestPayloadCachesGeminiOAuthRewrite(t *testing.T) {
	body := []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)
	payload := newRequestPayload(body)
	req := httptest.NewRequest(http.MethodPost, "http://proxy/v1beta/models/gemini-2.5-pro:generateContent", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	requestCtx := RequestContext{
		ClientType: ClientGemini,
		Family:     ProtocolFamilyGemini,
		Capability: CapabilityGeminiGenerateContent,
	}
	provider := config.Provider{Name: "gemini"}

	_, _, first, err := payload.geminiOAuthRequest(req, requestCtx, provider, "/v1beta/models/gemini-2.5-pro:generateContent", "project-1")
	if err != nil {
		t.Fatalf("geminiOAuthRequest first: %v", err)
	}
	_, _, second, err := payload.geminiOAuthRequest(req, requestCtx, provider, "/v1beta/models/gemini-2.5-pro:generateContent", "project-1")
	if err != nil {
		t.Fatalf("geminiOAuthRequest second: %v", err)
	}
	if len(first) == 0 || len(second) == 0 || &first[0] != &second[0] {
		t.Fatalf("expected cached gemini rewrite body to be reused")
	}
}

func decodeRawJSONMap(t *testing.T, body []byte) map[string]any {
	t.Helper()

	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatalf("json.Unmarshal: %v body=%s", err, string(body))
	}
	return root
}
