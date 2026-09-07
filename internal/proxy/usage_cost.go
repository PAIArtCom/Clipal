package proxy

import (
	"encoding/json"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/PAIArtCom/Clipal/internal/config"
	"github.com/PAIArtCom/Clipal/internal/pricing"
	"github.com/PAIArtCom/Clipal/internal/telemetry"
)

const (
	maxRecordedCostMicros int64 = 1_000_000_000_000

	// Anthropic reports fast mode in usage metadata instead of using a
	// separately priced model ID. It multiplies Opus 4.6 token rates,
	// including cache rates; per-query search charges remain unchanged by the
	// token-only multiplier.
	claudeOpus46FastTokenRateMultiplier int64 = 6
)

func applyUsageCostSnapshot(original *http.Request, requestCtx RequestContext, provider config.Provider, payload *requestPayload, snapshot telemetry.UsageSnapshot) telemetry.UsageSnapshot {
	if snapshot.HasCost {
		if validRecordedCost(snapshot.CostMicros) {
			if snapshot.CostSource == "" {
				snapshot.CostSource = telemetry.CostSourceUpstream
			}
			return snapshot
		}
		snapshot.CostMicros = 0
		snapshot.HasCost = false
		snapshot.CostSource = ""
		snapshot.CatalogVersion = ""
	}
	if costMicros, ok := usageCostMicrosFromRaw(snapshot.Usage); ok {
		snapshot.CostMicros = costMicros
		snapshot.HasCost = true
		snapshot.CostSource = telemetry.CostSourceUpstream
		return snapshot
	}

	costMicros, ok := inferredUsageCostMicros(original, requestCtx, provider, payload, snapshot)
	if !ok || !validRecordedCost(costMicros) {
		return snapshot
	}
	snapshot.CostMicros = costMicros
	snapshot.HasCost = true
	snapshot.CostSource = telemetry.CostSourceCatalog
	snapshot.CatalogVersion = pricing.CatalogMetadata().Version
	return snapshot
}

func validRecordedCost(costMicros int64) bool {
	return costMicros >= 0 && costMicros <= maxRecordedCostMicros
}

func inferredUsageCostMicros(original *http.Request, requestCtx RequestContext, provider config.Provider, payload *requestPayload, snapshot telemetry.UsageSnapshot) (int64, bool) {
	if snapshot.Usage == nil {
		return 0, false
	}

	root := effectiveUsageCostRequestRoot(original, requestCtx, provider, payload)
	model := effectiveUsageCostModel(requestCtx, provider, root)
	if model == "" {
		return 0, false
	}
	serviceTier := usageCostServiceTier(root)

	switch requestCtx.Family {
	case ProtocolFamilyOpenAI:
		if !isOpenAIGenerationCapability(requestCtx.Capability) {
			return 0, false
		}
		return calculateOpenAICostMicros(model, snapshot.Usage, serviceTier, pricingProviderHint(provider))
	case ProtocolFamilyClaude:
		if requestCtx.Capability != CapabilityClaudeMessages {
			return 0, false
		}
		return calculateClaudeCostMicros(model, snapshot.Usage, root, serviceTier)
	case ProtocolFamilyGemini:
		if requestCtx.Capability != CapabilityGeminiGenerateContent && requestCtx.Capability != CapabilityGeminiStreamGenerate {
			return 0, false
		}
		return calculateGeminiCostMicros(model, snapshot.Usage, serviceTier)
	default:
		return 0, false
	}
}

func effectiveUsageCostRequestRoot(original *http.Request, requestCtx RequestContext, provider config.Provider, payload *requestPayload) map[string]any {
	if payload != nil {
		if rewritten, ok := payload.providerRoot(original, requestCtx, provider); ok {
			return rewritten
		}
		return payload.jsonRoot()
	}
	return nil
}

func effectiveUsageCostModel(requestCtx RequestContext, provider config.Provider, root map[string]any) string {
	model := strings.TrimSpace(stickyModelName(requestCtx, root))
	if model == "" {
		model = provider.ModelOverride()
	}
	return strings.ToLower(strings.TrimSpace(model))
}

func usageCostServiceTier(root map[string]any) pricing.ServiceTier {
	tier := strings.ToLower(strings.TrimSpace(stringValueRaw(root["service_tier"])))
	if tier == "" {
		tier = strings.ToLower(strings.TrimSpace(stringValueRaw(root["processing_mode"])))
	}
	switch tier {
	case "batch":
		return pricing.ServiceTierBatch
	case "priority":
		return pricing.ServiceTierPriority
	case "flex":
		return pricing.ServiceTierFlex
	default:
		return pricing.ServiceTierStandard
	}
}

func pricingProviderHint(provider config.Provider) string {
	if parsed, err := url.Parse(strings.TrimSpace(provider.BaseURL)); err == nil {
		host := strings.ToLower(parsed.Hostname())
		for _, candidate := range []struct{ marker, hint string }{
			{"openrouter", "openrouter"}, {"deepseek", "deepseek"}, {"mistral", "mistral"},
			{"dashscope", "dashscope"}, {"together", "together_ai"}, {"groq", "groq"},
		} {
			if strings.Contains(host, candidate.marker) {
				return candidate.hint
			}
		}
	}
	return strings.Trim(strings.ToLower(strings.TrimSpace(provider.Name)), "/")
}

func usageCostMicrosFromRaw(raw map[string]any) (int64, bool) {
	if raw == nil {
		return 0, false
	}

	for _, key := range []string{"cost_micros", "costMicros", "cost_usd_micros", "costUsdMicros"} {
		if micros, ok := int64ValueRaw(raw[key]); ok && validRecordedCost(micros) {
			return micros, true
		}
	}
	for _, key := range []string{"cost_usd", "costUSD", "costUsd"} {
		if micros, ok := usdMicrosValue(raw[key]); ok && validRecordedCost(micros) {
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
		if micros, ok := usdMicrosValue(costMap["amount"]); ok && validRecordedCost(micros) {
			return micros, true
		}
	}
	micros, ok := usdMicrosValue(costValue)
	return micros, ok && validRecordedCost(micros)
}

func calculateOpenAICostMicros(model string, raw map[string]any, serviceTier pricing.ServiceTier, providerHint string) (int64, bool) {
	if !hasOpenAIUsageFields(raw) {
		return 0, false
	}
	if !validNonNegativeIntFields(raw, "prompt_tokens", "input_tokens", "completion_tokens", "output_tokens", "prompt_cache_hit_tokens") ||
		!validNestedNonNegativeIntField(raw, "input_tokens_details", "cached_tokens") ||
		!validNestedNonNegativeIntField(raw, "prompt_tokens_details", "cached_tokens") ||
		!validNestedNonNegativeIntField(raw, "output_tokens_details", "reasoning_tokens") ||
		!validNestedNonNegativeIntField(raw, "completion_tokens_details", "reasoning_tokens") {
		return 0, false
	}

	promptTokens, _ := int64Lookup(raw, "prompt_tokens", "input_tokens")
	outputTokens, _ := int64Lookup(raw, "completion_tokens", "output_tokens")
	cachedTokens, ok := nestedInt64Lookup(raw, "input_tokens_details", "cached_tokens")
	if !ok {
		cachedTokens, _ = nestedInt64Lookup(raw, "prompt_tokens_details", "cached_tokens")
	}
	if cachedTokens == 0 {
		cachedTokens, _ = int64Lookup(raw, "prompt_cache_hit_tokens")
	}
	if cachedTokens < 0 {
		cachedTokens = 0
	}
	if cachedTokens > promptTokens {
		cachedTokens = promptTokens
	}

	reasoningTokens, ok := nestedInt64Lookup(raw, "output_tokens_details", "reasoning_tokens")
	if !ok {
		reasoningTokens, _ = nestedInt64Lookup(raw, "completion_tokens_details", "reasoning_tokens")
	}
	return pricing.CostMicrosForProvider(model, pricing.PlatformOpenAI, providerHint, pricing.Usage{
		UncachedInputTokens: promptTokens - cachedTokens,
		CachedInputTokens:   cachedTokens,
		OutputTokens:        outputTokens,
		ReasoningTokens:     reasoningTokens,
		ServiceTier:         serviceTier,
	})
}

func calculateClaudeCostMicros(model string, raw map[string]any, requestRoot map[string]any, serviceTier pricing.ServiceTier) (int64, bool) {
	if !hasClaudeUsageFields(raw) {
		return 0, false
	}
	if !validNonNegativeIntFields(raw, "input_tokens", "output_tokens", "cache_creation_input_tokens", "cache_read_input_tokens") ||
		!validNestedNonNegativeIntField(raw, "cache_creation", "ephemeral_5m_input_tokens") ||
		!validNestedNonNegativeIntField(raw, "cache_creation", "ephemeral_1h_input_tokens") ||
		!validNestedNonNegativeIntField(raw, "server_tool_use", "web_search_requests") {
		return 0, false
	}

	inputTokens, _ := int64Lookup(raw, "input_tokens")
	outputTokens, _ := int64Lookup(raw, "output_tokens")
	cacheWriteTokens, cacheWrite1HTokens, ok := claudeCacheWriteTokens(raw, requestRoot)
	if !ok {
		return 0, false
	}
	cacheReadTokens, _ := int64Lookup(raw, "cache_read_input_tokens")
	webSearchRequests, _ := nestedInt64Lookup(raw, "server_tool_use", "web_search_requests")

	return pricing.CostMicros(model, pricing.PlatformAnthropic, pricing.Usage{
		UncachedInputTokens: inputTokens,
		CachedInputTokens:   cacheReadTokens,
		CacheWriteTokens:    cacheWriteTokens,
		CacheWrite1HTokens:  cacheWrite1HTokens,
		OutputTokens:        outputTokens,
		SearchQueries:       webSearchRequests,
		SearchContextSize:   "medium",
		ServiceTier:         serviceTier,
		TokenRateMultiplier: claudeFastTokenRateMultiplier(model, raw),
	})
}

func claudeCacheWriteTokens(raw map[string]any, requestRoot map[string]any) (int64, int64, bool) {
	total, _ := int64Lookup(raw, "cache_creation_input_tokens")
	total = max(total, 0)
	fiveMinute, hasFiveMinute := nestedInt64Lookup(raw, "cache_creation", "ephemeral_5m_input_tokens")
	oneHour, hasOneHour := nestedInt64Lookup(raw, "cache_creation", "ephemeral_1h_input_tokens")
	if hasFiveMinute || hasOneHour {
		fiveMinute = max(fiveMinute, 0)
		oneHour = max(oneHour, 0)
		if total == 0 {
			total = fiveMinute + oneHour
		}
		if fiveMinute > total || oneHour > total-fiveMinute || fiveMinute+oneHour != total {
			return 0, 0, false
		}
		return total - oneHour, oneHour, true
	}

	hasOneHourTTL, hasDefaultTTL := claudeCacheTTLs(requestRoot)
	if hasOneHourTTL && hasDefaultTTL {
		return 0, 0, false
	}
	if hasOneHourTTL {
		return 0, total, true
	}
	return total, 0, true
}

func claudeCacheTTLs(value any) (bool, bool) {
	var oneHour, defaultTTL bool
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			if cacheControl, ok := typed["cache_control"].(map[string]any); ok {
				if strings.EqualFold(strings.TrimSpace(stringValueRaw(cacheControl["ttl"])), "1h") {
					oneHour = true
				} else {
					defaultTTL = true
				}
			}
			for _, child := range typed {
				visit(child)
			}
		case []any:
			for _, child := range typed {
				visit(child)
			}
		}
	}
	visit(value)
	return oneHour, defaultTTL
}

func claudeFastTokenRateMultiplier(model string, raw map[string]any) int64 {
	if !strings.EqualFold(strings.TrimSpace(stringValueRaw(raw["speed"])), "fast") {
		return 1
	}
	model = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(model)), "anthropic/")
	switch model {
	case "claude-opus-4-6", "claude-opus-4-6-20260205":
		return claudeOpus46FastTokenRateMultiplier
	default:
		return 1
	}
}

func calculateGeminiCostMicros(model string, raw map[string]any, serviceTier pricing.ServiceTier) (int64, bool) {
	if !hasGeminiUsageFields(raw) {
		return 0, false
	}
	if !validNonNegativeIntFields(raw, "promptTokenCount", "toolUsePromptTokenCount", "candidatesTokenCount", "responseTokenCount", "thoughtsTokenCount", "totalTokenCount", "cachedContentTokenCount") || !validGeminiPromptTokenDetails(raw) {
		return 0, false
	}

	promptTokens, _ := int64Lookup(raw, "promptTokenCount")
	toolUsePromptTokens, _ := int64Lookup(raw, "toolUsePromptTokenCount")
	candidateTokens, _ := int64Lookup(raw, "candidatesTokenCount", "responseTokenCount")
	thoughtTokens, _ := int64Lookup(raw, "thoughtsTokenCount")
	totalTokens, _ := int64Lookup(raw, "totalTokenCount")
	cachedTokens, _ := int64Lookup(raw, "cachedContentTokenCount")
	effectivePromptTokens := addUsageCount(promptTokens, toolUsePromptTokens)
	if cachedTokens < 0 {
		cachedTokens = 0
	}
	if cachedTokens > effectivePromptTokens {
		cachedTokens = effectivePromptTokens
	}

	outputTokens := addUsageCount(candidateTokens, thoughtTokens)
	if totalTokens > effectivePromptTokens {
		residualOutput := totalTokens - effectivePromptTokens
		if residualOutput > outputTokens {
			outputTokens = residualOutput
		}
	}

	_, audioPromptTokens := geminiPromptTokenCounts(raw, promptTokens)
	_, cachedAudioTokens := geminiCachedTokenSplit(cachedTokens, effectivePromptTokens-audioPromptTokens, audioPromptTokens)
	return pricing.CostMicros(model, pricing.PlatformGemini, pricing.Usage{
		UncachedInputTokens: effectivePromptTokens - cachedTokens,
		CachedInputTokens:   cachedTokens,
		AudioInputTokens:    audioPromptTokens - cachedAudioTokens,
		OutputTokens:        outputTokens,
		ReasoningTokens:     thoughtTokens,
		ServiceTier:         serviceTier,
	})
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
		detailedTotal = addUsageCount(detailedTotal, tokenCount)
		switch strings.ToUpper(strings.TrimSpace(stringValueRaw(entry["modality"]))) {
		case "AUDIO":
			audioTokens = addUsageCount(audioTokens, tokenCount)
		default:
			textLikeTokens = addUsageCount(textLikeTokens, tokenCount)
		}
	}
	if detailedTotal <= 0 {
		return promptTokens, 0
	}
	if detailedTotal < promptTokens {
		textLikeTokens = addUsageCount(textLikeTokens, promptTokens-detailedTotal)
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

	totalPromptTokens := addUsageCount(textLikeTokens, audioTokens)
	if totalPromptTokens <= 0 {
		return cachedTokens, 0
	}

	numerator := new(big.Int).Mul(big.NewInt(cachedTokens), big.NewInt(audioTokens))
	denominator := big.NewInt(totalPromptTokens)
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(numerator, denominator, remainder)
	if new(big.Int).Lsh(remainder, 1).Cmp(denominator) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	cachedAudioTokens := quotient.Int64()
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

func addUsageCount(left, right int64) int64 {
	left = max(left, 0)
	right = max(right, 0)
	const maxInt64 = int64(^uint64(0) >> 1)
	if left > maxInt64-right {
		return maxInt64
	}
	return left + right
}

func hasOpenAIUsageFields(raw map[string]any) bool {
	return raw != nil && (hasAnyKey(raw, "prompt_tokens", "input_tokens", "completion_tokens", "output_tokens") || hasNestedKey(raw, "input_tokens_details"))
}

func hasClaudeUsageFields(raw map[string]any) bool {
	return raw != nil && hasAnyKey(raw, "input_tokens", "output_tokens", "cache_creation_input_tokens", "cache_read_input_tokens")
}

func hasGeminiUsageFields(raw map[string]any) bool {
	return raw != nil && (hasAnyKey(raw, "promptTokenCount", "candidatesTokenCount", "responseTokenCount", "thoughtsTokenCount", "toolUsePromptTokenCount", "cachedContentTokenCount") || hasNestedKey(raw, "promptTokensDetails"))
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

func validNonNegativeIntFields(raw map[string]any, keys ...string) bool {
	for _, key := range keys {
		value, present := raw[key]
		if !present {
			continue
		}
		parsed, ok := int64ValueRaw(value)
		if !ok || parsed < 0 {
			return false
		}
	}
	return true
}

func validNestedNonNegativeIntField(raw map[string]any, parent, key string) bool {
	value, present := raw[parent]
	if !present {
		return true
	}
	nested, ok := value.(map[string]any)
	if !ok {
		return false
	}
	return validNonNegativeIntFields(nested, key)
}

func validGeminiPromptTokenDetails(raw map[string]any) bool {
	value, present := raw["promptTokensDetails"]
	if !present {
		return true
	}
	details, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range details {
		entry, ok := item.(map[string]any)
		if !ok || !validNonNegativeIntFields(entry, "tokenCount") {
			return false
		}
	}
	return true
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
	var raw string
	switch typed := value.(type) {
	case string:
		raw = typed
	case json.Number:
		raw = typed.String()
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return 0, false
		}
		raw = strconv.FormatFloat(typed, 'g', -1, 64)
	case float32:
		if math.IsNaN(float64(typed)) || math.IsInf(float64(typed), 0) {
			return 0, false
		}
		raw = strconv.FormatFloat(float64(typed), 'g', -1, 32)
	case int:
		raw = strconv.Itoa(typed)
	case int64:
		raw = strconv.FormatInt(typed, 10)
	case int32:
		raw = strconv.FormatInt(int64(typed), 10)
	default:
		return 0, false
	}
	return pricing.DollarsToMicros(strings.TrimSpace(raw))
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
		if math.IsNaN(typed) || math.IsInf(typed, 0) || typed < math.MinInt64 || typed >= float64(uint64(1)<<63) || math.Trunc(typed) != typed {
			return 0, false
		}
		return int64(typed), true
	case float32:
		return int64ValueRaw(float64(typed))
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return parsed, true
		}
		if parsed, err := typed.Float64(); err == nil {
			return int64ValueRaw(parsed)
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
			return int64ValueRaw(parsed)
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
