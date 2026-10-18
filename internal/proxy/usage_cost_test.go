package proxy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/PAIArtCom/Clipal/internal/config"
	"github.com/PAIArtCom/Clipal/internal/pricing"
	"github.com/PAIArtCom/Clipal/internal/telemetry"
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
	if snapshot.CostSource != telemetry.CostSourceUpstream || snapshot.CatalogVersion != "" {
		t.Fatalf("attribution = %#v", snapshot)
	}
}

func TestApplyUsageCostSnapshot_RejectsOverflowingDirectCost(t *testing.T) {
	t.Parallel()
	snapshot := applyUsageCostSnapshot(nil, RequestContext{}, config.Provider{}, nil, telemetry.UsageSnapshot{
		Usage: map[string]any{"costUSD": "1e100"},
	})
	if snapshot.HasCost {
		t.Fatalf("accepted overflowing direct cost: %#v", snapshot)
	}
}

func TestApplyUsageCostSnapshot_RoundsDirectDecimalCostExactly(t *testing.T) {
	t.Parallel()
	snapshot := applyUsageCostSnapshot(nil, RequestContext{}, config.Provider{}, nil, telemetry.UsageSnapshot{Usage: map[string]any{"costUSD": "0.0001245"}})
	if !snapshot.HasCost || snapshot.CostMicros != 125 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestApplyUsageCostSnapshot_RejectsExcessiveDirectMicros(t *testing.T) {
	t.Parallel()
	snapshot := applyUsageCostSnapshot(nil, RequestContext{}, config.Provider{}, nil, telemetry.UsageSnapshot{Usage: map[string]any{"cost_micros": json.Number("9223372036854775807")}})
	if snapshot.HasCost {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestApplyUsageCostSnapshot_AcceptsDirectMicrosAtLimit(t *testing.T) {
	t.Parallel()
	snapshot := applyUsageCostSnapshot(nil, RequestContext{}, config.Provider{}, nil, telemetry.UsageSnapshot{Usage: map[string]any{"cost_micros": json.Number("1000000000000")}})
	if !snapshot.HasCost || snapshot.CostMicros != maxRecordedCostMicros {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestApplyUsageCostSnapshot_RejectsExcessivePresetCost(t *testing.T) {
	t.Parallel()
	snapshot := applyUsageCostSnapshot(nil, RequestContext{}, config.Provider{}, nil, telemetry.UsageSnapshot{
		HasCost: true, CostMicros: maxRecordedCostMicros + 1, CostSource: telemetry.CostSourceUpstream,
	})
	if snapshot.HasCost || snapshot.CostMicros != 0 || snapshot.CostSource != "" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestApplyUsageCostSnapshot_EnforcesUSDCostLimit(t *testing.T) {
	t.Parallel()
	atLimit := applyUsageCostSnapshot(nil, RequestContext{}, config.Provider{}, nil, telemetry.UsageSnapshot{Usage: map[string]any{"costUSD": "1000000"}})
	if !atLimit.HasCost || atLimit.CostMicros != maxRecordedCostMicros {
		t.Fatalf("at-limit snapshot = %#v", atLimit)
	}
	aboveLimit := applyUsageCostSnapshot(nil, RequestContext{}, config.Provider{}, nil, telemetry.UsageSnapshot{Usage: map[string]any{"costUSD": "1000000.000001"}})
	if aboveLimit.HasCost {
		t.Fatalf("above-limit snapshot = %#v", aboveLimit)
	}
}

func TestUsageCostCalculation_IgnoresInvalidCounters(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		family     ProtocolFamily
		capability RequestCapability
		model      string
		usage      map[string]any
	}{
		{name: "openai malformed input", family: ProtocolFamilyOpenAI, capability: CapabilityOpenAIChatCompletions, model: "gpt-5.4", usage: map[string]any{"prompt_tokens": "bad", "completion_tokens": 100}},
		{name: "openai total only", family: ProtocolFamilyOpenAI, capability: CapabilityOpenAIChatCompletions, model: "gpt-5.4", usage: map[string]any{"total_tokens": 100}},
		{name: "claude negative cache", family: ProtocolFamilyClaude, capability: CapabilityClaudeMessages, model: "claude-opus-4-6", usage: map[string]any{"input_tokens": 100, "output_tokens": 100, "cache_read_input_tokens": -1}},
		{name: "claude total only", family: ProtocolFamilyClaude, capability: CapabilityClaudeMessages, model: "claude-opus-4-6", usage: map[string]any{"total_tokens": 100}},
		{name: "gemini malformed details", family: ProtocolFamilyGemini, capability: CapabilityGeminiGenerateContent, model: "gemini-2.5-flash", usage: map[string]any{"promptTokenCount": 100, "candidatesTokenCount": 100, "promptTokensDetails": []any{"bad"}}},
		{name: "gemini total only", family: ProtocolFamilyGemini, capability: CapabilityGeminiGenerateContent, model: "gemini-2.5-flash", usage: map[string]any{"totalTokenCount": 100}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "http://localhost", nil)
			model := tc.model
			provider := config.Provider{Overrides: &config.ProviderOverrides{Model: &model}}
			snapshot := applyUsageCostSnapshot(request, RequestContext{Family: tc.family, Capability: tc.capability}, provider, nil, telemetry.UsageSnapshot{Usage: tc.usage})
			if snapshot.HasCost || snapshot.CostMicros != 0 {
				t.Fatalf("invalid auxiliary usage was priced: %#v", snapshot)
			}
		})
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
	if snapshot.CostSource != telemetry.CostSourceCatalog || snapshot.CatalogVersion != pricing.CatalogMetadata().Version {
		t.Fatalf("attribution = %#v", snapshot)
	}
}

func TestApplyUsageCostSnapshot_OpenAIAPIProviderUsesModelFromRequest(t *testing.T) {
	t.Parallel()

	body := []byte(`{"model":"gpt-5.4","input":"hello"}`)
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
		AuthType: config.ProviderAuthTypeAPIKey,
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
		t.Fatalf("expected api-key provider cost to be tracked")
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

func TestApplyUsageCostSnapshot_ClaudeUsesOneHourCachePrice(t *testing.T) {
	t.Parallel()
	body := []byte(`{"model":"claude-opus-4-6","messages":[{"role":"user","content":[{"type":"text","text":"cached","cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`)
	req := httptest.NewRequest(http.MethodPost, "http://proxy/claude/v1/messages", bytes.NewReader(body))
	requestCtx := RequestContext{ClientType: ClientClaude, Family: ProtocolFamilyClaude, Capability: CapabilityClaudeMessages, UpstreamPath: "/v1/messages"}
	snapshot := applyUsageCostSnapshot(req, requestCtx, config.Provider{}, newRequestPayload(body), telemetry.UsageSnapshot{Usage: map[string]any{"cache_creation_input_tokens": 1000.0}})
	if !snapshot.HasCost || snapshot.CostMicros != 10_000 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestCalculateClaudeCostMicros_UsesCacheCreationBreakdown(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"cache_creation_input_tokens": 1000.0,
		"cache_creation":              map[string]any{"ephemeral_5m_input_tokens": 400.0, "ephemeral_1h_input_tokens": 600.0},
	}
	cost, ok := calculateClaudeCostMicros("claude-opus-4-6", raw, nil, pricing.ServiceTierStandard)
	if !ok || cost != 8_500 {
		t.Fatalf("cost = %d, ok = %v", cost, ok)
	}
}

func TestCalculateClaudeCostMicros_RejectsIncompleteCacheCreationBreakdown(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"cache_creation_input_tokens": 1000.0,
		"cache_creation":              map[string]any{"ephemeral_1h_input_tokens": 600.0},
	}
	if _, ok := calculateClaudeCostMicros("claude-opus-4-6", raw, nil, pricing.ServiceTierStandard); ok {
		t.Fatal("priced incomplete cache creation breakdown")
	}
}

func TestCalculateClaudeCostMicros_RejectsMixedTTLWithoutBreakdown(t *testing.T) {
	t.Parallel()
	raw := map[string]any{"cache_creation_input_tokens": 1000.0}
	root := map[string]any{"content": []any{
		map[string]any{"cache_control": map[string]any{"type": "ephemeral", "ttl": "1h"}},
		map[string]any{"cache_control": map[string]any{"type": "ephemeral"}},
	}}
	if _, ok := calculateClaudeCostMicros("claude-opus-4-6", raw, root, pricing.ServiceTierStandard); ok {
		t.Fatal("priced mixed cache TTLs without a usage breakdown")
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
		OAuthProvider: config.OAuthProviderAntigravity,
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
	if snapshot.CostMicros != 737_500 {
		t.Fatalf("cost_micros = %d", snapshot.CostMicros)
	}
}

func TestApplyUsageCostSnapshot_ExactModelDoesNotInheritFamilyPrice(t *testing.T) {
	t.Parallel()

	body := []byte(`{"model":"gpt-5.4-pro","input":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/responses", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	requestCtx := RequestContext{
		ClientType:   ClientOpenAI,
		Family:       ProtocolFamilyOpenAI,
		Capability:   CapabilityOpenAIResponses,
		UpstreamPath: "/v1/responses",
	}

	snapshot := applyUsageCostSnapshot(req, requestCtx, config.Provider{AuthType: config.ProviderAuthTypeAPIKey}, newRequestPayload(body), telemetry.UsageSnapshot{
		Usage: map[string]any{
			"input_tokens":  100000.0,
			"output_tokens": 20000.0,
		},
	})
	if !snapshot.HasCost || snapshot.CostMicros != 6_600_000 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestApplyUsageCostSnapshot_OpenRouterUsesProviderSpecificPrice(t *testing.T) {
	t.Parallel()
	body := []byte(`{"model":"deepseek/deepseek-chat","messages":[]}`)
	req := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/chat/completions", bytes.NewReader(body))
	requestCtx := RequestContext{ClientType: ClientOpenAI, Family: ProtocolFamilyOpenAI, Capability: CapabilityOpenAIChatCompletions, UpstreamPath: "/v1/chat/completions"}
	provider := config.Provider{Name: "fallback", BaseURL: "https://openrouter.ai/api", AuthType: config.ProviderAuthTypeAPIKey}
	snapshot := applyUsageCostSnapshot(req, requestCtx, provider, newRequestPayload(body), telemetry.UsageSnapshot{Usage: map[string]any{"prompt_tokens": 1_000_000.0, "completion_tokens": 1_000_000.0}})
	if !snapshot.HasCost || snapshot.CostMicros != 1_210_000 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestApplyUsageCostSnapshot_DeepSeekUsesCacheHitTokens(t *testing.T) {
	t.Parallel()
	body := []byte(`{"model":"deepseek-chat","messages":[]}`)
	req := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/chat/completions", bytes.NewReader(body))
	requestCtx := RequestContext{ClientType: ClientOpenAI, Family: ProtocolFamilyOpenAI, Capability: CapabilityOpenAIChatCompletions, UpstreamPath: "/v1/chat/completions"}
	provider := config.Provider{BaseURL: "https://api.deepseek.com", AuthType: config.ProviderAuthTypeAPIKey}
	snapshot := applyUsageCostSnapshot(req, requestCtx, provider, newRequestPayload(body), telemetry.UsageSnapshot{Usage: map[string]any{"prompt_tokens": 1_000_000.0, "prompt_cache_hit_tokens": 1_000_000.0}})
	if !snapshot.HasCost || snapshot.CostMicros != 28_000 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestApplyUsageCostSnapshot_CompatibleProviderUsesReasoningRate(t *testing.T) {
	t.Parallel()
	body := []byte(`{"model":"qwen-plus-2025-04-28","messages":[]}`)
	req := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/chat/completions", bytes.NewReader(body))
	requestCtx := RequestContext{ClientType: ClientOpenAI, Family: ProtocolFamilyOpenAI, Capability: CapabilityOpenAIChatCompletions, UpstreamPath: "/v1/chat/completions"}
	provider := config.Provider{BaseURL: "https://dashscope.aliyuncs.com/compatible-mode", AuthType: config.ProviderAuthTypeAPIKey}
	snapshot := applyUsageCostSnapshot(req, requestCtx, provider, newRequestPayload(body), telemetry.UsageSnapshot{Usage: map[string]any{
		"completion_tokens":         1_000_000.0,
		"completion_tokens_details": map[string]any{"reasoning_tokens": 1_000_000.0},
	}})
	if !snapshot.HasCost || snapshot.CostMicros != 4_000_000 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestApplyUsageCostSnapshot_UnknownModelHasNoInferredCost(t *testing.T) {
	t.Parallel()

	body := []byte(`{"model":"gpt-5.4-unknown","input":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/responses", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	requestCtx := RequestContext{
		ClientType:   ClientOpenAI,
		Family:       ProtocolFamilyOpenAI,
		Capability:   CapabilityOpenAIResponses,
		UpstreamPath: "/v1/responses",
	}

	snapshot := applyUsageCostSnapshot(req, requestCtx, config.Provider{AuthType: config.ProviderAuthTypeAPIKey}, newRequestPayload(body), telemetry.UsageSnapshot{
		Usage: map[string]any{"input_tokens": 1000.0, "output_tokens": 100.0},
	})
	if snapshot.HasCost {
		t.Fatalf("unexpected inferred cost: %#v", snapshot)
	}
}

func TestCalculateClaudeCostMicros_FastMultiplierUsesModelPremium(t *testing.T) {
	t.Parallel()

	raw := map[string]any{"input_tokens": 1000.0, "speed": "fast"}
	tests := []struct {
		model string
		want  int64
	}{
		// Opus 4.6 has no catalog multiplier and uses the 6x fallback.
		{model: "claude-opus-4-6", want: 30_000},
		// Opus 4.8 and 5.5 carry fast_mode_uplift_multiplier=2 in the catalog.
		{model: "claude-opus-4-8", want: 10_000},
		{model: "claude-opus-5-5", want: 8_000},
		// Models without a fast-mode premium keep their base rates.
		{model: "claude-sonnet-4-6", want: 3_000},
	}
	for _, tc := range tests {
		cost, ok := calculateClaudeCostMicros(tc.model, raw, nil, pricing.ServiceTierStandard)
		if !ok || cost != tc.want {
			t.Fatalf("%s fast cost = %d, ok = %v, want %d", tc.model, cost, ok, tc.want)
		}
	}
}

func TestCalculateClaudeCostMicros_USInferenceGeoStacksWithFastMode(t *testing.T) {
	t.Parallel()

	raw := map[string]any{
		"input_tokens":    1000.0,
		"output_tokens":   1000.0,
		"server_tool_use": map[string]any{"web_search_requests": 1.0},
		"inference_geo":   "us",
		"speed":           "fast",
	}
	cost, ok := calculateClaudeCostMicros("claude-opus-5-5", raw, nil, pricing.ServiceTierStandard)
	// Token costs are (4,000 + 20,000) micros, multiplied by 2 for fast mode
	// and 1.1 for US inference. The 10,000-micro search charge is unchanged.
	if !ok || cost != 62_800 {
		t.Fatalf("opus 5.5 fast US cost = %d, ok = %v", cost, ok)
	}
	raw["inference_geo"] = "global"
	delete(raw, "speed")
	if cost, ok := calculateClaudeCostMicros("claude-opus-5-5", raw, nil, pricing.ServiceTierStandard); !ok || cost != 34_000 {
		t.Fatalf("opus 5.5 global cost = %d, ok = %v", cost, ok)
	}
}

func TestCalculateClaudeCostMicros_FastMultiplierCoversTokenRatesButNotSearch(t *testing.T) {
	t.Parallel()

	raw := map[string]any{
		"input_tokens":            1000.0,
		"output_tokens":           1000.0,
		"cache_read_input_tokens": 1000.0,
		"server_tool_use":         map[string]any{"web_search_requests": 1.0},
		"speed":                   "fast",
	}
	cost, ok := calculateClaudeCostMicros("claude-opus-4-6", raw, nil, pricing.ServiceTierStandard)
	// Token costs are (5,000 + 25,000 + 500) micros at catalog rates, then
	// multiplied by 6. The separate 10,000-micro search charge is unchanged.
	if !ok || cost != 193_000 {
		t.Fatalf("opus 4.6 fast cost with cache and search = %d, ok = %v", cost, ok)
	}
}

func TestApplyUsageCostSnapshot_GeminiSupportsLiveUsageFields(t *testing.T) {
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
		AuthType: config.ProviderAuthTypeAPIKey,
	}

	snapshot := applyUsageCostSnapshot(req, requestCtx, provider, newRequestPayload([]byte(`{"contents":[],"model":"gemini-2.5-pro"}`)), telemetry.UsageSnapshot{
		Usage: map[string]any{
			"promptTokenCount":        1000.0,
			"responseTokenCount":      200.0,
			"toolUsePromptTokenCount": 150.0,
			"thoughtsTokenCount":      50.0,
			"totalTokenCount":         1400.0,
		},
	})
	if !snapshot.HasCost {
		t.Fatalf("expected gemini api-key provider cost to be tracked")
	}
	if snapshot.CostMicros != 3_938 {
		t.Fatalf("cost_micros = %d", snapshot.CostMicros)
	}
}
