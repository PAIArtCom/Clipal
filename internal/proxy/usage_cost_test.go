package proxy

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lansespirit/Clipal/internal/config"
	"github.com/lansespirit/Clipal/internal/telemetry"
)

func TestApplyUsageCostSnapshot_UsesDirectCostFromRaw(t *testing.T) {
	t.Parallel()

	snapshot := applyUsageCostSnapshot(nil, RequestContext{}, config.Provider{}, nil, telemetry.UsageSnapshot{
		Usage: map[string]any{
			"costUSD": 0.03238,
		},
	})
	if !snapshot.HasCost {
		t.Fatalf("expected direct cost to be tracked")
	}
	if snapshot.CostMicros != 32_380 {
		t.Fatalf("cost_micros = %d", snapshot.CostMicros)
	}
}

func TestApplyUsageCostSnapshot_CodexUsesProviderOverrideAndCachedTokens(t *testing.T) {
	t.Parallel()

	body := []byte(`{"model":"gpt-4.1","input":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/responses", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	requestCtx := RequestContext{
		ClientType:   ClientOpenAI,
		Family:       ProtocolFamilyOpenAI,
		Capability:   CapabilityOpenAIResponses,
		UpstreamPath: "/v1/responses",
	}
	req = withRequestContext(req, requestCtx)

	provider := config.Provider{
		AuthType:      config.ProviderAuthTypeOAuth,
		OAuthProvider: config.OAuthProviderCodex,
		Overrides: &config.ProviderOverrides{
			Model: strPtr("gpt-5.4"),
		},
	}

	snapshot := applyUsageCostSnapshot(req, requestCtx, provider, newRequestPayload(body), telemetry.UsageSnapshot{
		Usage: map[string]any{
			"prompt_tokens":     100000.0,
			"completion_tokens": 20000.0,
			"input_tokens_details": map[string]any{
				"cached_tokens": 25000.0,
			},
		},
	})
	if !snapshot.HasCost {
		t.Fatalf("expected codex cost to be tracked")
	}
	if snapshot.CostMicros != 493_750 {
		t.Fatalf("cost_micros = %d", snapshot.CostMicros)
	}
}

func TestApplyUsageCostSnapshot_ClaudeUsesRawCacheAndWebSearchUsage(t *testing.T) {
	t.Parallel()

	body := []byte(`{"model":"claude-sonnet-4-5","messages":[]}`)
	req := httptest.NewRequest(http.MethodPost, "http://proxy/claude/v1/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	requestCtx := RequestContext{
		ClientType:   ClientClaude,
		Family:       ProtocolFamilyClaude,
		Capability:   CapabilityClaudeMessages,
		UpstreamPath: "/v1/messages",
	}
	req = withRequestContext(req, requestCtx)

	provider := config.Provider{
		AuthType:      config.ProviderAuthTypeOAuth,
		OAuthProvider: config.OAuthProviderClaude,
	}

	snapshot := applyUsageCostSnapshot(req, requestCtx, provider, newRequestPayload(body), telemetry.UsageSnapshot{
		Usage: map[string]any{
			"input_tokens":                10000.0,
			"output_tokens":               2000.0,
			"cache_creation_input_tokens": 4000.0,
			"cache_read_input_tokens":     3000.0,
			"server_tool_use": map[string]any{
				"web_search_requests": 2.0,
			},
		},
	})
	if !snapshot.HasCost {
		t.Fatalf("expected claude cost to be tracked")
	}
	if snapshot.CostMicros != 95_900 {
		t.Fatalf("cost_micros = %d", snapshot.CostMicros)
	}
}

func TestApplyUsageCostSnapshot_GeminiUsesThoughtTokensAndLargePromptTier(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1beta/models/gemini-2.5-pro:generateContent", bytes.NewReader([]byte(`{"contents":[]}`)))
	req.Header.Set("Content-Type", "application/json")
	requestCtx := RequestContext{
		ClientType:   ClientGemini,
		Family:       ProtocolFamilyGemini,
		Capability:   CapabilityGeminiGenerateContent,
		UpstreamPath: "/v1beta/models/gemini-2.5-pro:generateContent",
	}
	req = withRequestContext(req, requestCtx)

	provider := config.Provider{
		AuthType:      config.ProviderAuthTypeOAuth,
		OAuthProvider: config.OAuthProviderGemini,
	}

	snapshot := applyUsageCostSnapshot(req, requestCtx, provider, newRequestPayload([]byte(`{"contents":[]}`)), telemetry.UsageSnapshot{
		Usage: map[string]any{
			"promptTokenCount":        250000.0,
			"candidatesTokenCount":    10000.0,
			"thoughtsTokenCount":      5000.0,
			"cachedContentTokenCount": 50000.0,
			"promptTokensDetails": []any{
				map[string]any{"modality": "TEXT", "tokenCount": 200000.0},
				map[string]any{"modality": "AUDIO", "tokenCount": 50000.0},
			},
		},
	})
	if !snapshot.HasCost {
		t.Fatalf("expected gemini cost to be tracked")
	}
	if snapshot.CostMicros != 758_000 {
		t.Fatalf("cost_micros = %d", snapshot.CostMicros)
	}
}
