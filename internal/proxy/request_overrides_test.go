package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
)

func strPtr(v string) *string { return &v }

func intPtr(v int) *int { return &v }

func decodeRequestBodyMap(t *testing.T, req *http.Request) map[string]any {
	t.Helper()
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatalf("json.Unmarshal: %v body=%s", err, string(body))
	}
	return root
}

func TestCreateProxyRequest_AppliesOpenAIResponsesOverrides(t *testing.T) {
	t.Parallel()

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{
			Name:     "openai",
			BaseURL:  "https://api.openai.example",
			APIKey:   "provider-key",
			Priority: 1,
			Overrides: &config.ProviderOverrides{
				Model: strPtr("gpt-5.4"),
				OpenAI: &config.OpenAIOverrides{
					ReasoningEffort: strPtr("high"),
				},
			},
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})

	original := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/responses", bytes.NewReader([]byte(`{"model":"gpt-4.1","input":"hello"}`)))
	original.Header.Set("Content-Type", "application/json")
	original = withRequestContext(original, RequestContext{
		ClientType:     ClientOpenAI,
		Family:         ProtocolFamilyOpenAI,
		Capability:     CapabilityOpenAIResponses,
		UpstreamPath:   "/v1/responses",
		UnifiedIngress: true,
	})

	proxyReq, err := cp.createProxyRequest(original, cp.providers[0], "provider-key", "/v1/responses", []byte(`{"model":"gpt-4.1","input":"hello"}`))
	if err != nil {
		t.Fatalf("createProxyRequest: %v", err)
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
}

func TestCreateProxyRequest_ReplacesChatReasoningEffortOnlyWhenPresent(t *testing.T) {
	t.Parallel()

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{
			Name:     "openai",
			BaseURL:  "https://api.openai.example",
			APIKey:   "provider-key",
			Priority: 1,
			Overrides: &config.ProviderOverrides{
				Model: strPtr("gpt-5.4-mini"),
				OpenAI: &config.OpenAIOverrides{
					ReasoningEffort: strPtr("low"),
				},
			},
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})

	original := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/chat/completions", bytes.NewReader([]byte(`{"model":"gpt-4.1","reasoning_effort":"medium","messages":[]}`)))
	original.Header.Set("Content-Type", "application/json")
	original = withRequestContext(original, RequestContext{
		ClientType:     ClientOpenAI,
		Family:         ProtocolFamilyOpenAI,
		Capability:     CapabilityOpenAIChatCompletions,
		UpstreamPath:   "/v1/chat/completions",
		UnifiedIngress: true,
	})

	proxyReq, err := cp.createProxyRequest(original, cp.providers[0], "provider-key", "/v1/chat/completions", []byte(`{"model":"gpt-4.1","reasoning_effort":"medium","messages":[]}`))
	if err != nil {
		t.Fatalf("createProxyRequest: %v", err)
	}

	root := decodeRequestBodyMap(t, proxyReq)
	if got := root["model"]; got != "gpt-5.4-mini" {
		t.Fatalf("model = %v", got)
	}
	if got := root["reasoning_effort"]; got != "low" {
		t.Fatalf("reasoning_effort = %v", got)
	}

	proxyReq, err = cp.createProxyRequest(original, cp.providers[0], "provider-key", "/v1/chat/completions", []byte(`{"model":"gpt-4.1","messages":[]}`))
	if err != nil {
		t.Fatalf("createProxyRequest: %v", err)
	}
	root = decodeRequestBodyMap(t, proxyReq)
	if _, ok := root["reasoning_effort"]; ok {
		t.Fatalf("did not expect reasoning_effort to be injected: %#v", root)
	}
}

func TestCreateProxyRequest_DoesNotRewriteOpenAINonGenerationModel(t *testing.T) {
	t.Parallel()

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{
			Name:      "openai",
			BaseURL:   "https://api.openai.example",
			APIKey:    "provider-key",
			Priority:  1,
			Overrides: &config.ProviderOverrides{Model: strPtr("text-embedding-3-large")},
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})

	original := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/embeddings", bytes.NewReader([]byte(`{"model":"text-embedding-3-small","input":"hello"}`)))
	original.Header.Set("Content-Type", "application/json")
	original = withRequestContext(original, RequestContext{
		ClientType:     ClientOpenAI,
		Family:         ProtocolFamilyOpenAI,
		Capability:     CapabilityOpenAIEmbeddings,
		UpstreamPath:   "/v1/embeddings",
		UnifiedIngress: true,
	})

	proxyReq, err := cp.createProxyRequest(original, cp.providers[0], "provider-key", "/v1/embeddings", []byte(`{"model":"text-embedding-3-small","input":"hello"}`))
	if err != nil {
		t.Fatalf("createProxyRequest: %v", err)
	}

	root := decodeRequestBodyMap(t, proxyReq)
	if got := root["model"]; got != "text-embedding-3-small" {
		t.Fatalf("model = %v, want original embeddings model", got)
	}
}

func TestCreateProxyRequest_AppliesClaudeThinkingOverrides(t *testing.T) {
	t.Parallel()

	cp := newClientProxy(ClientClaude, config.ClientModeAuto, "", []config.Provider{
		{
			Name:     "claude",
			BaseURL:  "https://api.anthropic.example",
			APIKey:   "provider-key",
			Priority: 1,
			Overrides: &config.ProviderOverrides{
				Model: strPtr("claude-sonnet-4-5"),
				Claude: &config.ClaudeOverrides{
					ThinkingBudgetTokens: intPtr(4096),
				},
			},
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})

	original := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/messages", bytes.NewReader([]byte(`{"model":"claude-3-7-sonnet","thinking":{"type":"disabled"},"messages":[]}`)))
	original.Header.Set("Content-Type", "application/json")
	original = withRequestContext(original, RequestContext{
		ClientType:     ClientClaude,
		Family:         ProtocolFamilyClaude,
		Capability:     CapabilityClaudeMessages,
		UpstreamPath:   "/v1/messages",
		UnifiedIngress: true,
	})

	proxyReq, err := cp.createProxyRequest(original, cp.providers[0], "provider-key", "/v1/messages", []byte(`{"model":"claude-3-7-sonnet","thinking":{"type":"disabled"},"messages":[]}`))
	if err != nil {
		t.Fatalf("createProxyRequest: %v", err)
	}

	root := decodeRequestBodyMap(t, proxyReq)
	if got := root["model"]; got != "claude-sonnet-4-5" {
		t.Fatalf("model = %v", got)
	}
	thinking, ok := root["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("thinking = %T %#v", root["thinking"], root["thinking"])
	}
	if got := thinking["type"]; got != "enabled" {
		t.Fatalf("thinking.type = %v", got)
	}
	if got := thinking["budget_tokens"]; got != float64(4096) {
		t.Fatalf("thinking.budget_tokens = %v", got)
	}
}

func TestCreateProxyRequest_SkipsClaudeThinkingBudgetForEffortModels(t *testing.T) {
	t.Parallel()

	cp := newClientProxy(ClientClaude, config.ClientModeAuto, "", []config.Provider{
		{
			Name:     "claude",
			BaseURL:  "https://api.anthropic.example",
			APIKey:   "provider-key",
			Priority: 1,
			Overrides: &config.ProviderOverrides{
				Model: strPtr("claude-opus-4-8"),
				Claude: &config.ClaudeOverrides{
					ThinkingBudgetTokens: intPtr(4096),
				},
			},
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})

	body := []byte(`{"model":"claude-opus-4-6","thinking":{"type":"disabled"},"messages":[]}`)
	original := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/messages", bytes.NewReader(body))
	original.Header.Set("Content-Type", "application/json")
	original = withRequestContext(original, RequestContext{
		ClientType:     ClientClaude,
		Family:         ProtocolFamilyClaude,
		Capability:     CapabilityClaudeMessages,
		UpstreamPath:   "/v1/messages",
		UnifiedIngress: true,
	})

	proxyReq, err := cp.createProxyRequest(original, cp.providers[0], "provider-key", "/v1/messages", body)
	if err != nil {
		t.Fatalf("createProxyRequest: %v", err)
	}

	root := decodeRequestBodyMap(t, proxyReq)
	if got := root["model"]; got != "claude-opus-4-8" {
		t.Fatalf("model = %v", got)
	}
	thinking, ok := root["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("thinking = %T %#v", root["thinking"], root["thinking"])
	}
	if _, ok := thinking["budget_tokens"]; ok {
		t.Fatalf("thinking.budget_tokens should not be injected for effort models: %#v", thinking)
	}
	if got := thinking["type"]; got != "disabled" {
		t.Fatalf("thinking.type = %v", got)
	}
}

func TestCreateProxyRequest_AppliesClaudeEffortOverride(t *testing.T) {
	t.Parallel()

	cp := newClientProxy(ClientClaude, config.ClientModeAuto, "", []config.Provider{
		{
			Name:     "claude",
			BaseURL:  "https://api.anthropic.example",
			APIKey:   "provider-key",
			Priority: 1,
			Overrides: &config.ProviderOverrides{
				Model: strPtr("claude-opus-4-8"),
				Claude: &config.ClaudeOverrides{
					Effort: strPtr("xhigh"),
				},
			},
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})

	body := []byte(`{"model":"claude-opus-4-6","thinking":{"type":"disabled"},"output_config":{"format":{"type":"text"}},"messages":[]}`)
	original := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/messages", bytes.NewReader(body))
	original.Header.Set("Content-Type", "application/json")
	original = withRequestContext(original, RequestContext{
		ClientType:     ClientClaude,
		Family:         ProtocolFamilyClaude,
		Capability:     CapabilityClaudeMessages,
		UpstreamPath:   "/v1/messages",
		UnifiedIngress: true,
	})

	proxyReq, err := cp.createProxyRequest(original, cp.providers[0], "provider-key", "/v1/messages", body)
	if err != nil {
		t.Fatalf("createProxyRequest: %v", err)
	}

	root := decodeRequestBodyMap(t, proxyReq)
	outputConfig, ok := root["output_config"].(map[string]any)
	if !ok {
		t.Fatalf("output_config = %T %#v", root["output_config"], root["output_config"])
	}
	if got := outputConfig["effort"]; got != "max" {
		t.Fatalf("output_config.effort = %v, want max", got)
	}
	if _, ok := outputConfig["format"]; !ok {
		t.Fatalf("output_config.format was not preserved: %#v", outputConfig)
	}
	thinking, ok := root["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("thinking = %T %#v", root["thinking"], root["thinking"])
	}
	if got := thinking["type"]; got != "adaptive" {
		t.Fatalf("thinking.type = %v, want adaptive", got)
	}
}

func TestCreateProxyRequest_SkipsOverridesForNonJSONRequests(t *testing.T) {
	t.Parallel()

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{
			Name:     "openai",
			BaseURL:  "https://api.openai.example",
			APIKey:   "provider-key",
			Priority: 1,
			Overrides: &config.ProviderOverrides{
				Model: strPtr("gpt-5.4"),
				OpenAI: &config.OpenAIOverrides{
					ReasoningEffort: strPtr("high"),
				},
			},
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})

	body := []byte(`{"model":"gpt-4.1","input":"hello"}`)
	original := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/responses", bytes.NewReader(body))
	original.Header.Set("Content-Type", "multipart/form-data; boundary=abc123")
	original = withRequestContext(original, RequestContext{
		ClientType:     ClientOpenAI,
		Family:         ProtocolFamilyOpenAI,
		Capability:     CapabilityOpenAIResponses,
		UpstreamPath:   "/v1/responses",
		UnifiedIngress: true,
	})

	proxyReq, err := cp.createProxyRequest(original, cp.providers[0], "provider-key", "/v1/responses", body)
	if err != nil {
		t.Fatalf("createProxyRequest: %v", err)
	}
	got, err := io.ReadAll(proxyReq.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("body = %s, want %s", string(got), string(body))
	}
}
