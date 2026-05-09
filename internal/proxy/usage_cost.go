package proxy

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/lansespirit/Clipal/internal/config"
	"github.com/lansespirit/Clipal/internal/telemetry"
)

const (
	usdMicrosPerDollar          int64 = 1_000_000
	tokensPerMillion            int64 = 1_000_000
	openAILongContextThreshold  int64 = 272_000
	geminiLargePromptThreshold  int64 = 200_000
	claudeWebSearchMicrosPerUse int64 = 10_000
)

type openAICostRates struct {
	InputPerMTokMicros       int64
	CachedInputPerMTokMicros int64
	OutputPerMTokMicros      int64
	LongContextThreshold     int64
	LongInputPerMTokMicros   int64
	LongCachedPerMTokMicros  int64
	LongOutputPerMTokMicros  int64
}

type claudeCostRates struct {
	InputPerMTokMicros      int64
	OutputPerMTokMicros     int64
	CacheWritePerMTokMicros int64
	CacheReadPerMTokMicros  int64
}

type geminiTierRates struct {
	TextInputPerMTokMicros  int64
	AudioInputPerMTokMicros int64
	OutputPerMTokMicros     int64
	TextCachePerMTokMicros  int64
	AudioCachePerMTokMicros int64
}

type geminiCostRates struct {
	ThresholdTokens int64
	Standard        geminiTierRates
	LargePrompt     geminiTierRates
}

func applyUsageCostSnapshot(original *http.Request, requestCtx RequestContext, provider config.Provider, payload *requestPayload, snapshot telemetry.UsageSnapshot) telemetry.UsageSnapshot {
	if snapshot.HasCost {
		return snapshot
	}
	if costMicros, ok := usageCostMicrosFromRaw(snapshot.Usage); ok {
		snapshot.CostMicros = costMicros
		snapshot.HasCost = true
		return snapshot
	}

	costMicros, ok := oauthUsageCostMicros(original, requestCtx, provider, payload, snapshot)
	if !ok {
		return snapshot
	}
	snapshot.CostMicros = costMicros
	snapshot.HasCost = true
	return snapshot
}

func oauthUsageCostMicros(original *http.Request, requestCtx RequestContext, provider config.Provider, payload *requestPayload, snapshot telemetry.UsageSnapshot) (int64, bool) {
	if snapshot.Usage == nil || !provider.UsesOAuth() {
		return 0, false
	}

	model := effectiveUsageCostModel(original, requestCtx, provider, payload)
	if model == "" {
		return 0, false
	}

	switch provider.NormalizedOAuthProvider() {
	case config.OAuthProviderCodex:
		if requestCtx.Capability != CapabilityOpenAIResponses {
			return 0, false
		}
		return calculateOpenAICostMicros(model, snapshot.Usage)
	case config.OAuthProviderClaude:
		if requestCtx.Capability != CapabilityClaudeMessages {
			return 0, false
		}
		return calculateClaudeCostMicros(model, snapshot.Usage)
	case config.OAuthProviderGemini:
		if requestCtx.Capability != CapabilityGeminiGenerateContent && requestCtx.Capability != CapabilityGeminiStreamGenerate {
			return 0, false
		}
		return calculateGeminiCostMicros(model, snapshot.Usage)
	default:
		return 0, false
	}
}

func effectiveUsageCostModel(original *http.Request, requestCtx RequestContext, provider config.Provider, payload *requestPayload) string {
	var root map[string]any
	if payload != nil {
		if rewritten, ok := payload.providerRoot(original, requestCtx, provider); ok {
			root = rewritten
		} else {
			root = payload.jsonRoot()
		}
	}

	model := strings.TrimSpace(stickyModelName(requestCtx, root))
	if model == "" {
		model = provider.ModelOverride()
	}
	return normalizeUsageCostModel(model)
}

func normalizeUsageCostModel(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return ""
	}
	model = strings.TrimPrefix(model, "/")
	for _, prefix := range []string{
		"models/",
		"model/",
		"openai/",
		"anthropic/",
		"google/",
		"gemini/",
		"claude/",
	} {
		model = strings.TrimPrefix(model, prefix)
	}
	return strings.TrimSpace(model)
}

func usageCostMicrosFromRaw(raw map[string]any) (int64, bool) {
	if raw == nil {
		return 0, false
	}

	for _, key := range []string{"cost_micros", "costMicros", "cost_usd_micros", "costUsdMicros"} {
		if micros, ok := int64ValueRaw(raw[key]); ok && micros >= 0 {
			return micros, true
		}
	}
	for _, key := range []string{"cost_usd", "costUSD", "costUsd"} {
		if micros, ok := usdMicrosValue(raw[key]); ok {
			return micros, true
		}
	}

	costValue, ok := raw["cost"]
	if !ok {
		return 0, false
	}
	if costMap, ok := costValue.(map[string]any); ok {
		currency := strings.ToUpper(strings.TrimSpace(stringValueRaw(costMap["currency"])))
		if currency != "" && currency != "USD" {
			return 0, false
		}
		if micros, ok := usdMicrosValue(costMap["amount"]); ok {
			return micros, true
		}
	}
	return usdMicrosValue(costValue)
}

func calculateOpenAICostMicros(model string, raw map[string]any) (int64, bool) {
	if !hasOpenAIUsageFields(raw) {
		return 0, false
	}

	rates, ok := openAICostRatesForModel(model)
	if !ok {
		return 0, false
	}

	promptTokens, _ := int64Lookup(raw, "prompt_tokens", "input_tokens")
	outputTokens, _ := int64Lookup(raw, "completion_tokens", "output_tokens")
	cachedTokens, _ := nestedInt64Lookup(raw, "input_tokens_details", "cached_tokens")
	if cachedTokens < 0 {
		cachedTokens = 0
	}
	if cachedTokens > promptTokens {
		cachedTokens = promptTokens
	}

	if rates.LongContextThreshold > 0 && promptTokens > rates.LongContextThreshold {
		rates.InputPerMTokMicros = rates.LongInputPerMTokMicros
		rates.CachedInputPerMTokMicros = rates.LongCachedPerMTokMicros
		rates.OutputPerMTokMicros = rates.LongOutputPerMTokMicros
	}

	billablePromptTokens := promptTokens - cachedTokens
	total := costMicrosForTokens(billablePromptTokens, rates.InputPerMTokMicros)
	total += costMicrosForTokens(cachedTokens, rates.CachedInputPerMTokMicros)
	total += costMicrosForTokens(outputTokens, rates.OutputPerMTokMicros)
	return total, true
}

func openAICostRatesForModel(model string) (openAICostRates, bool) {
	switch {
	case strings.HasPrefix(model, "gpt-5.5"):
		return openAICostRates{
			InputPerMTokMicros:       5_000_000,
			CachedInputPerMTokMicros: 500_000,
			OutputPerMTokMicros:      30_000_000,
			LongContextThreshold:     openAILongContextThreshold,
			LongInputPerMTokMicros:   10_000_000,
			LongCachedPerMTokMicros:  1_000_000,
			LongOutputPerMTokMicros:  45_000_000,
		}, true
	case strings.HasPrefix(model, "gpt-5.4-mini"):
		return openAICostRates{
			InputPerMTokMicros:       750_000,
			CachedInputPerMTokMicros: 75_000,
			OutputPerMTokMicros:      4_500_000,
		}, true
	case strings.HasPrefix(model, "gpt-5.4"):
		return openAICostRates{
			InputPerMTokMicros:       2_500_000,
			CachedInputPerMTokMicros: 250_000,
			OutputPerMTokMicros:      15_000_000,
			LongContextThreshold:     openAILongContextThreshold,
			LongInputPerMTokMicros:   5_000_000,
			LongCachedPerMTokMicros:  500_000,
			LongOutputPerMTokMicros:  22_500_000,
		}, true
	case strings.HasPrefix(model, "gpt-5.3-codex"), strings.HasPrefix(model, "gpt-5.2-codex"), strings.HasPrefix(model, "gpt-5.2"):
		return openAICostRates{
			InputPerMTokMicros:       1_750_000,
			CachedInputPerMTokMicros: 175_000,
			OutputPerMTokMicros:      14_000_000,
		}, true
	case strings.HasPrefix(model, "gpt-5"):
		return openAICostRates{
			InputPerMTokMicros:       1_250_000,
			CachedInputPerMTokMicros: 125_000,
			OutputPerMTokMicros:      10_000_000,
		}, true
	case strings.HasPrefix(model, "gpt-4.1-mini"):
		return openAICostRates{
			InputPerMTokMicros:       400_000,
			CachedInputPerMTokMicros: 100_000,
			OutputPerMTokMicros:      1_600_000,
		}, true
	case strings.HasPrefix(model, "gpt-4.1"):
		return openAICostRates{
			InputPerMTokMicros:       2_000_000,
			CachedInputPerMTokMicros: 500_000,
			OutputPerMTokMicros:      8_000_000,
		}, true
	default:
		return openAICostRates{}, false
	}
}

func calculateClaudeCostMicros(model string, raw map[string]any) (int64, bool) {
	if !hasClaudeUsageFields(raw) {
		return 0, false
	}

	speedFast := strings.EqualFold(strings.TrimSpace(stringValueRaw(raw["speed"])), "fast")
	rates, ok := claudeCostRatesForModel(model, speedFast)
	if !ok {
		return 0, false
	}

	inputTokens, _ := int64Lookup(raw, "input_tokens")
	outputTokens, _ := int64Lookup(raw, "output_tokens")
	cacheWriteTokens, _ := int64Lookup(raw, "cache_creation_input_tokens")
	cacheReadTokens, _ := int64Lookup(raw, "cache_read_input_tokens")
	webSearchRequests, _ := nestedInt64Lookup(raw, "server_tool_use", "web_search_requests")

	total := costMicrosForTokens(inputTokens, rates.InputPerMTokMicros)
	total += costMicrosForTokens(outputTokens, rates.OutputPerMTokMicros)
	total += costMicrosForTokens(cacheWriteTokens, rates.CacheWritePerMTokMicros)
	total += costMicrosForTokens(cacheReadTokens, rates.CacheReadPerMTokMicros)
	if webSearchRequests > 0 {
		total += webSearchRequests * claudeWebSearchMicrosPerUse
	}
	return total, true
}

func claudeCostRatesForModel(model string, speedFast bool) (claudeCostRates, bool) {
	switch {
	case containsAny(model, "haiku-4.5", "haiku-4-5"):
		return claudeCostRates{
			InputPerMTokMicros:      1_000_000,
			OutputPerMTokMicros:     5_000_000,
			CacheWritePerMTokMicros: 1_250_000,
			CacheReadPerMTokMicros:  100_000,
		}, true
	case containsAny(model, "haiku-3.5", "haiku-3-5"):
		return claudeCostRates{
			InputPerMTokMicros:      800_000,
			OutputPerMTokMicros:     4_000_000,
			CacheWritePerMTokMicros: 1_000_000,
			CacheReadPerMTokMicros:  80_000,
		}, true
	case containsAny(model, "opus-4.7", "opus-4-7", "opus-4.5", "opus-4-5"):
		return claudeCostRates{
			InputPerMTokMicros:      5_000_000,
			OutputPerMTokMicros:     25_000_000,
			CacheWritePerMTokMicros: 6_250_000,
			CacheReadPerMTokMicros:  500_000,
		}, true
	case containsAny(model, "opus-4.6", "opus-4-6"):
		if speedFast {
			return claudeCostRates{
				InputPerMTokMicros:      30_000_000,
				OutputPerMTokMicros:     150_000_000,
				CacheWritePerMTokMicros: 37_500_000,
				CacheReadPerMTokMicros:  3_000_000,
			}, true
		}
		return claudeCostRates{
			InputPerMTokMicros:      5_000_000,
			OutputPerMTokMicros:     25_000_000,
			CacheWritePerMTokMicros: 6_250_000,
			CacheReadPerMTokMicros:  500_000,
		}, true
	case containsAny(model, "opus-4.1", "opus-4-1", "opus-4"):
		return claudeCostRates{
			InputPerMTokMicros:      15_000_000,
			OutputPerMTokMicros:     75_000_000,
			CacheWritePerMTokMicros: 18_750_000,
			CacheReadPerMTokMicros:  1_500_000,
		}, true
	case containsAny(model, "sonnet-4.6", "sonnet-4-6", "sonnet-4.5", "sonnet-4-5", "sonnet-4", "3.7-sonnet", "3-7-sonnet", "3.5-sonnet", "3-5-sonnet"):
		return claudeCostRates{
			InputPerMTokMicros:      3_000_000,
			OutputPerMTokMicros:     15_000_000,
			CacheWritePerMTokMicros: 3_750_000,
			CacheReadPerMTokMicros:  300_000,
		}, true
	default:
		return claudeCostRates{}, false
	}
}

func calculateGeminiCostMicros(model string, raw map[string]any) (int64, bool) {
	if !hasGeminiUsageFields(raw) {
		return 0, false
	}

	rates, ok := geminiCostRatesForModel(model)
	if !ok {
		return 0, false
	}

	promptTokens, _ := int64Lookup(raw, "promptTokenCount")
	candidateTokens, _ := int64Lookup(raw, "candidatesTokenCount")
	thoughtTokens, _ := int64Lookup(raw, "thoughtsTokenCount")
	totalTokens, _ := int64Lookup(raw, "totalTokenCount")
	cachedTokens, _ := int64Lookup(raw, "cachedContentTokenCount")
	if cachedTokens < 0 {
		cachedTokens = 0
	}
	if cachedTokens > promptTokens {
		cachedTokens = promptTokens
	}

	outputTokens := candidateTokens + thoughtTokens
	if totalTokens > promptTokens {
		residualOutput := totalTokens - promptTokens
		if residualOutput > outputTokens {
			outputTokens = residualOutput
		}
	}

	textLikePromptTokens, audioPromptTokens := geminiPromptTokenCounts(raw, promptTokens)
	cachedTextTokens, cachedAudioTokens := geminiCachedTokenSplit(cachedTokens, textLikePromptTokens, audioPromptTokens)
	billableTextTokens := textLikePromptTokens - cachedTextTokens
	billableAudioTokens := audioPromptTokens - cachedAudioTokens
	billablePromptTokens := promptTokens - cachedTokens
	if remainder := billablePromptTokens - billableTextTokens - billableAudioTokens; remainder > 0 {
		billableTextTokens += remainder
	}

	tier := rates.Standard
	if rates.ThresholdTokens > 0 && promptTokens > rates.ThresholdTokens {
		tier = rates.LargePrompt
	}

	total := costMicrosForTokens(billableTextTokens, tier.TextInputPerMTokMicros)
	total += costMicrosForTokens(billableAudioTokens, tier.AudioInputPerMTokMicros)
	total += costMicrosForTokens(cachedTextTokens, tier.TextCachePerMTokMicros)
	total += costMicrosForTokens(cachedAudioTokens, tier.AudioCachePerMTokMicros)
	total += costMicrosForTokens(outputTokens, tier.OutputPerMTokMicros)
	return total, true
}

func geminiCostRatesForModel(model string) (geminiCostRates, bool) {
	switch {
	case strings.HasPrefix(model, "gemini-3.1-pro"), strings.HasPrefix(model, "gemini-3-pro"):
		return geminiCostRates{
			Standard: geminiTierRates{
				TextInputPerMTokMicros:  3_500_000,
				AudioInputPerMTokMicros: 1_000_000,
				OutputPerMTokMicros:     15_000_000,
				TextCachePerMTokMicros:  875_000,
				AudioCachePerMTokMicros: 250_000,
			},
		}, true
	case strings.HasPrefix(model, "gemini-3.1-flash"), strings.HasPrefix(model, "gemini-3-flash"):
		return geminiCostRates{
			Standard: geminiTierRates{
				TextInputPerMTokMicros:  250_000,
				AudioInputPerMTokMicros: 500_000,
				OutputPerMTokMicros:     1_500_000,
				TextCachePerMTokMicros:  50_000,
				AudioCachePerMTokMicros: 100_000,
			},
		}, true
	case strings.HasPrefix(model, "gemini-2.5-pro"):
		return geminiCostRates{
			ThresholdTokens: geminiLargePromptThreshold,
			Standard: geminiTierRates{
				TextInputPerMTokMicros:  1_250_000,
				AudioInputPerMTokMicros: 3_000_000,
				OutputPerMTokMicros:     10_000_000,
				TextCachePerMTokMicros:  125_000,
				AudioCachePerMTokMicros: 300_000,
			},
			LargePrompt: geminiTierRates{
				TextInputPerMTokMicros:  2_500_000,
				AudioInputPerMTokMicros: 3_000_000,
				OutputPerMTokMicros:     15_000_000,
				TextCachePerMTokMicros:  250_000,
				AudioCachePerMTokMicros: 300_000,
			},
		}, true
	case strings.HasPrefix(model, "gemini-2.5-flash-lite"):
		return geminiCostRates{
			Standard: geminiTierRates{
				TextInputPerMTokMicros:  100_000,
				AudioInputPerMTokMicros: 300_000,
				OutputPerMTokMicros:     400_000,
				TextCachePerMTokMicros:  10_000,
				AudioCachePerMTokMicros: 30_000,
			},
			LargePrompt: geminiTierRates{
				TextInputPerMTokMicros:  100_000,
				AudioInputPerMTokMicros: 300_000,
				OutputPerMTokMicros:     400_000,
				TextCachePerMTokMicros:  10_000,
				AudioCachePerMTokMicros: 30_000,
			},
		}, true
	case strings.HasPrefix(model, "gemini-2.5-flash"):
		return geminiCostRates{
			ThresholdTokens: geminiLargePromptThreshold,
			Standard: geminiTierRates{
				TextInputPerMTokMicros:  300_000,
				AudioInputPerMTokMicros: 1_000_000,
				OutputPerMTokMicros:     2_500_000,
				TextCachePerMTokMicros:  75_000,
				AudioCachePerMTokMicros: 250_000,
			},
			LargePrompt: geminiTierRates{
				TextInputPerMTokMicros:  600_000,
				AudioInputPerMTokMicros: 1_000_000,
				OutputPerMTokMicros:     3_500_000,
				TextCachePerMTokMicros:  150_000,
				AudioCachePerMTokMicros: 250_000,
			},
		}, true
	default:
		return geminiCostRates{}, false
	}
}

func geminiPromptTokenCounts(raw map[string]any, promptTokens int64) (int64, int64) {
	details, _ := raw["promptTokensDetails"].([]any)
	if len(details) == 0 {
		return promptTokens, 0
	}

	var textLikeTokens int64
	var audioTokens int64
	var detailedTotal int64
	for _, item := range details {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		tokenCount, ok := int64ValueRaw(entry["tokenCount"])
		if !ok || tokenCount <= 0 {
			continue
		}
		detailedTotal += tokenCount
		switch strings.ToUpper(strings.TrimSpace(stringValueRaw(entry["modality"]))) {
		case "AUDIO":
			audioTokens += tokenCount
		default:
			textLikeTokens += tokenCount
		}
	}
	if detailedTotal <= 0 {
		return promptTokens, 0
	}
	if detailedTotal < promptTokens {
		textLikeTokens += promptTokens - detailedTotal
	}
	return textLikeTokens, audioTokens
}

func geminiCachedTokenSplit(cachedTokens int64, textLikeTokens int64, audioTokens int64) (int64, int64) {
	if cachedTokens <= 0 {
		return 0, 0
	}
	if audioTokens <= 0 {
		return cachedTokens, 0
	}
	if textLikeTokens <= 0 {
		return 0, cachedTokens
	}

	totalPromptTokens := textLikeTokens + audioTokens
	if totalPromptTokens <= 0 {
		return cachedTokens, 0
	}

	cachedAudioTokens := int64(math.Round(float64(cachedTokens) * float64(audioTokens) / float64(totalPromptTokens)))
	if cachedAudioTokens > audioTokens {
		cachedAudioTokens = audioTokens
	}
	if cachedAudioTokens > cachedTokens {
		cachedAudioTokens = cachedTokens
	}
	cachedTextTokens := cachedTokens - cachedAudioTokens
	if cachedTextTokens > textLikeTokens {
		overflow := cachedTextTokens - textLikeTokens
		cachedTextTokens = textLikeTokens
		cachedAudioTokens += overflow
	}
	return cachedTextTokens, cachedAudioTokens
}

func hasOpenAIUsageFields(raw map[string]any) bool {
	return raw != nil && (hasAnyKey(raw, "prompt_tokens", "input_tokens", "completion_tokens", "output_tokens", "total_tokens") || hasNestedKey(raw, "input_tokens_details"))
}

func hasClaudeUsageFields(raw map[string]any) bool {
	return raw != nil && hasAnyKey(raw, "input_tokens", "output_tokens", "cache_creation_input_tokens", "cache_read_input_tokens", "total_tokens")
}

func hasGeminiUsageFields(raw map[string]any) bool {
	return raw != nil && (hasAnyKey(raw, "promptTokenCount", "candidatesTokenCount", "thoughtsTokenCount", "totalTokenCount", "cachedContentTokenCount") || hasNestedKey(raw, "promptTokensDetails"))
}

func hasAnyKey(raw map[string]any, keys ...string) bool {
	for _, key := range keys {
		if _, ok := raw[key]; ok {
			return true
		}
	}
	return false
}

func hasNestedKey(raw map[string]any, key string) bool {
	_, ok := raw[key]
	return ok
}

func containsAny(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if strings.Contains(value, fragment) {
			return true
		}
	}
	return false
}

func costMicrosForTokens(tokens int64, perMillionMicros int64) int64 {
	if tokens <= 0 || perMillionMicros <= 0 {
		return 0
	}
	return (tokens*perMillionMicros + tokensPerMillion/2) / tokensPerMillion
}

func int64Lookup(raw map[string]any, keys ...string) (int64, bool) {
	for _, key := range keys {
		if value, ok := raw[key]; ok {
			if parsed, ok := int64ValueRaw(value); ok {
				return parsed, true
			}
		}
	}
	return 0, false
}

func nestedInt64Lookup(raw map[string]any, path ...string) (int64, bool) {
	value, ok := nestedValue(raw, path...)
	if !ok {
		return 0, false
	}
	return int64ValueRaw(value)
}

func nestedValue(raw map[string]any, path ...string) (any, bool) {
	current := any(raw)
	for _, key := range path {
		nextMap, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		next, ok := nextMap[key]
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}

func usdMicrosValue(value any) (int64, bool) {
	parsed, ok := float64ValueRaw(value)
	if !ok || parsed < 0 || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return 0, false
	}
	return int64(math.Round(parsed * float64(usdMicrosPerDollar))), true
}

func float64ValueRaw(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func int64ValueRaw(value any) (int64, bool) {
	switch typed := value.(type) {
	case int64:
		return typed, true
	case int32:
		return int64(typed), true
	case int:
		return int64(typed), true
	case float64:
		return int64(typed), true
	case float32:
		return int64(typed), true
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return parsed, true
		}
		if parsed, err := typed.Float64(); err == nil {
			return int64(parsed), true
		}
		return 0, false
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return 0, false
		}
		if parsed, err := strconv.ParseInt(text, 10, 64); err == nil {
			return parsed, true
		}
		if parsed, err := strconv.ParseFloat(text, 64); err == nil {
			return int64(parsed), true
		}
		return 0, false
	default:
		return 0, false
	}
}

func stringValueRaw(value any) string {
	text, _ := value.(string)
	return text
}
