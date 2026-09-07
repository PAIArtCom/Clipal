package pricing

import "math/big"

const (
	above200KThreshold int64 = 200_000
	above272KThreshold int64 = 272_000
	microsPerDollar    int64 = 1_000_000
)

type tokenRates struct {
	input      rate
	output     rate
	cacheRead  rate
	cacheWrite rate
	audioInput rate
	reasoning  rate
}

func CostMicros(model string, platform Platform, usage Usage) (int64, bool) {
	return CostMicrosForProvider(model, platform, "", usage)
}

func CostMicrosForProvider(model string, platform Platform, providerHint string, usage Usage) (int64, bool) {
	if usage.UncachedInputTokens < 0 || usage.CachedInputTokens < 0 || usage.CacheWriteTokens < 0 ||
		usage.CacheWrite1HTokens < 0 || usage.AudioInputTokens < 0 || usage.OutputTokens < 0 ||
		usage.ReasoningTokens < 0 || usage.SearchQueries < 0 || usage.TokenRateMultiplier < 0 ||
		usage.AudioInputTokens > usage.UncachedInputTokens || usage.ReasoningTokens > usage.OutputTokens {
		return 0, false
	}
	_, spec, ok := resolveModelWithProvider(model, platform, providerHint)
	if !ok {
		return 0, false
	}
	rates, ok := ratesForUsage(spec, usage)
	if !ok {
		return 0, false
	}
	if rates.input.value == nil && rates.output.value == nil && rates.cacheRead.value == nil && rates.cacheWrite.value == nil {
		return 0, false
	}

	uncachedInput := nonNegative(usage.UncachedInputTokens)
	audioInput := nonNegative(usage.AudioInputTokens)
	textInput := uncachedInput - audioInput
	output := nonNegative(usage.OutputTokens)
	reasoning := nonNegative(usage.ReasoningTokens)
	textOutput := output - reasoning
	if (textInput > 0 && rates.input.value == nil) ||
		(audioInput > 0 && firstRate(rates.audioInput, rates.input).value == nil) ||
		(usage.CachedInputTokens > 0 && firstRate(rates.cacheRead, rates.input).value == nil) ||
		(usage.CacheWriteTokens > 0 && firstRate(rates.cacheWrite, rates.input).value == nil) ||
		(usage.CacheWrite1HTokens > 0 && cacheWrite1HRate(spec, rates).value == nil) ||
		(textOutput > 0 && rates.output.value == nil) ||
		(reasoning > 0 && firstRate(rates.reasoning, rates.output).value == nil) ||
		(usage.SearchQueries > 0 && searchRate(spec.SearchContext, usage.SearchContextSize).value == nil) {
		return 0, false
	}

	multiplier := usage.TokenRateMultiplier
	if multiplier <= 0 {
		multiplier = 1
	}
	total := new(big.Rat)
	addTokenCost(total, textInput, rates.input, multiplier)
	addTokenCost(total, audioInput, firstRate(rates.audioInput, rates.input), multiplier)
	addTokenCost(total, nonNegative(usage.CachedInputTokens), firstRate(rates.cacheRead, rates.input), multiplier)
	addTokenCost(total, nonNegative(usage.CacheWriteTokens), firstRate(rates.cacheWrite, rates.input), multiplier)
	addTokenCost(total, nonNegative(usage.CacheWrite1HTokens), cacheWrite1HRate(spec, rates), multiplier)
	addTokenCost(total, textOutput, rates.output, multiplier)
	addTokenCost(total, reasoning, firstRate(rates.reasoning, rates.output), multiplier)
	addSearchCost(total, spec.SearchContext, usage)
	return roundRat(total), true
}

func DollarsToMicros(raw string) (int64, bool) {
	value, err := decimalRat(raw)
	if err != nil || value == nil || value.Sign() < 0 {
		return 0, false
	}
	value.Mul(value, new(big.Rat).SetInt64(microsPerDollar))
	return roundRatChecked(value)
}

func cacheWrite1HRate(spec modelSpec, rates tokenRates) rate {
	if spec.CacheWrite1H.value == nil || spec.Input.value == nil || rates.input.value == nil {
		return firstRate(spec.CacheWrite1H, rates.cacheWrite, rates.input)
	}
	value := new(big.Rat).Mul(rates.input.value, spec.CacheWrite1H.value)
	value.Quo(value, spec.Input.value)
	return rate{value: value}
}

func ratesForUsage(spec modelSpec, usage Usage) (tokenRates, bool) {
	rates := tokenRates{
		input: spec.Input, output: spec.Output, cacheRead: spec.CacheRead,
		cacheWrite: spec.CacheWrite, audioInput: spec.AudioInput, reasoning: spec.ReasoningOutput,
	}
	switch usage.ServiceTier {
	case ServiceTierBatch:
		rates.input = firstRate(spec.BatchInput, rates.input)
		rates.output = firstRate(spec.BatchOutput, rates.output)
	case ServiceTierPriority:
		rates.input = firstRate(spec.PriorityInput, rates.input)
		rates.output = firstRate(spec.PriorityOutput, rates.output)
		rates.cacheRead = firstRate(spec.PriorityCacheRead, rates.cacheRead)
		rates.cacheWrite = firstRate(spec.PriorityCacheWrite, rates.cacheWrite)
	case ServiceTierFlex:
		rates.input = firstRate(spec.FlexInput, rates.input)
		rates.output = firstRate(spec.FlexOutput, rates.output)
		rates.cacheRead = firstRate(spec.FlexCacheRead, rates.cacheRead)
		rates.cacheWrite = firstRate(spec.FlexCacheWrite, rates.cacheWrite)
	}

	inputTokens := sumNonNegative(usage.UncachedInputTokens, usage.CachedInputTokens, usage.CacheWriteTokens, usage.CacheWrite1HTokens)
	if inputTokens > above272KThreshold && hasAnyRate(spec.Above272KInput, spec.Above272KOutput, spec.Above272KCacheRead, spec.Above272KCacheWrite) {
		switch usage.ServiceTier {
		case ServiceTierBatch:
			if tierRatesAffectUsage(spec, usage) {
				return tokenRates{}, false
			}
			rates.input = firstRate(spec.Above272KInput, rates.input)
			rates.output = firstRate(spec.Above272KOutput, rates.output)
			rates.cacheRead = firstRate(spec.Above272KCacheRead, rates.cacheRead)
			rates.cacheWrite = firstRate(spec.Above272KCacheWrite, rates.cacheWrite)
		case ServiceTierPriority:
			if !combinedRatesCoverUsage(usage, spec.PriorityInput, spec.PriorityOutput, spec.PriorityCacheRead, spec.PriorityCacheWrite, spec.Above272KPriorityInput, spec.Above272KPriorityOutput, spec.Above272KPriorityCacheRead, spec.Above272KPriorityCacheWrite) {
				return tokenRates{}, false
			}
			rates.input = firstRate(spec.Above272KPriorityInput, spec.Above272KInput, rates.input)
			rates.output = firstRate(spec.Above272KPriorityOutput, spec.Above272KOutput, rates.output)
			rates.cacheRead = firstRate(spec.Above272KPriorityCacheRead, spec.Above272KCacheRead, rates.cacheRead)
			rates.cacheWrite = firstRate(spec.Above272KPriorityCacheWrite, spec.Above272KCacheWrite, rates.cacheWrite)
		case ServiceTierFlex:
			if !combinedRatesCoverUsage(usage, spec.FlexInput, spec.FlexOutput, spec.FlexCacheRead, spec.FlexCacheWrite, spec.Above272KFlexInput, spec.Above272KFlexOutput, spec.Above272KFlexCacheRead, spec.Above272KFlexCacheWrite) {
				return tokenRates{}, false
			}
			rates.input = firstRate(spec.Above272KFlexInput, spec.Above272KInput, rates.input)
			rates.output = firstRate(spec.Above272KFlexOutput, spec.Above272KOutput, rates.output)
			rates.cacheRead = firstRate(spec.Above272KFlexCacheRead, spec.Above272KCacheRead, rates.cacheRead)
			rates.cacheWrite = firstRate(spec.Above272KFlexCacheWrite, spec.Above272KCacheWrite, rates.cacheWrite)
		default:
			rates.input = firstRate(spec.Above272KInput, rates.input)
			rates.output = firstRate(spec.Above272KOutput, rates.output)
			rates.cacheRead = firstRate(spec.Above272KCacheRead, rates.cacheRead)
			rates.cacheWrite = firstRate(spec.Above272KCacheWrite, rates.cacheWrite)
		}
	} else if inputTokens > above200KThreshold && hasAnyRate(spec.Above200KInput, spec.Above200KOutput, spec.Above200KCacheRead, spec.Above200KCacheWrite) {
		if usage.ServiceTier != ServiceTierStandard && tierRatesAffectUsage(spec, usage) {
			return tokenRates{}, false
		}
		rates.input = firstRate(spec.Above200KInput, rates.input)
		rates.output = firstRate(spec.Above200KOutput, rates.output)
		rates.cacheRead = firstRate(spec.Above200KCacheRead, rates.cacheRead)
		rates.cacheWrite = firstRate(spec.Above200KCacheWrite, rates.cacheWrite)
	}
	return rates, true
}

func tierRatesAffectUsage(spec modelSpec, usage Usage) bool {
	switch usage.ServiceTier {
	case ServiceTierBatch:
		return ((usage.UncachedInputTokens > 0 || usage.CacheWrite1HTokens > 0) && spec.BatchInput.value != nil) || (usage.OutputTokens > 0 && spec.BatchOutput.value != nil)
	case ServiceTierPriority:
		return usageUsesRates(usage, spec.PriorityInput, spec.PriorityOutput, spec.PriorityCacheRead, spec.PriorityCacheWrite)
	case ServiceTierFlex:
		return usageUsesRates(usage, spec.FlexInput, spec.FlexOutput, spec.FlexCacheRead, spec.FlexCacheWrite)
	default:
		return false
	}
}

func combinedRatesCoverUsage(usage Usage, tierInput, tierOutput, tierCacheRead, tierCacheWrite, combinedInput, combinedOutput, combinedCacheRead, combinedCacheWrite rate) bool {
	return ((usage.UncachedInputTokens <= 0 && usage.CacheWrite1HTokens <= 0) || tierInput.value == nil || combinedInput.value != nil) &&
		(usage.OutputTokens <= 0 || tierOutput.value == nil || combinedOutput.value != nil) &&
		(usage.CachedInputTokens <= 0 || tierCacheRead.value == nil || combinedCacheRead.value != nil) &&
		((usage.CacheWriteTokens <= 0 && usage.CacheWrite1HTokens <= 0) || tierCacheWrite.value == nil || combinedCacheWrite.value != nil)
}

func usageUsesRates(usage Usage, input, output, cacheRead, cacheWrite rate) bool {
	return ((usage.UncachedInputTokens > 0 || usage.CacheWrite1HTokens > 0) && input.value != nil) ||
		(usage.OutputTokens > 0 && output.value != nil) ||
		(usage.CachedInputTokens > 0 && cacheRead.value != nil) ||
		((usage.CacheWriteTokens > 0 || usage.CacheWrite1HTokens > 0) && cacheWrite.value != nil)
}

func addTokenCost(total *big.Rat, units int64, price rate, multiplier int64) {
	if units <= 0 || price.value == nil {
		return
	}
	amount := new(big.Rat).Mul(price.value, new(big.Rat).SetInt64(units))
	amount.Mul(amount, new(big.Rat).SetInt64(microsPerDollar))
	amount.Mul(amount, new(big.Rat).SetInt64(multiplier))
	total.Add(total, amount)
}

func addSearchCost(total *big.Rat, prices map[string]rate, usage Usage) {
	if usage.SearchQueries <= 0 {
		return
	}
	price := searchRate(prices, usage.SearchContextSize)
	addTokenCost(total, usage.SearchQueries, price, 1)
}

func searchRate(prices map[string]rate, contextSize string) rate {
	price := prices["search_context_size_"+contextSize]
	if price.value == nil {
		price = prices["search_context_size_medium"]
	}
	return price
}

func firstRate(rates ...rate) rate {
	for _, candidate := range rates {
		if candidate.value != nil {
			return candidate
		}
	}
	return rate{}
}

func hasAnyRate(rates ...rate) bool {
	return firstRate(rates...).value != nil
}

func nonNegative(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

func sumNonNegative(values ...int64) int64 {
	var total int64
	const maxInt64 = int64(^uint64(0) >> 1)
	for _, value := range values {
		value = nonNegative(value)
		if total > maxInt64-value {
			return maxInt64
		}
		total += value
	}
	return total
}

func roundRat(value *big.Rat) int64 {
	rounded, ok := roundRatChecked(value)
	if !ok {
		return int64(^uint64(0) >> 1)
	}
	return rounded
}

func roundRatChecked(value *big.Rat) (int64, bool) {
	if value == nil || value.Sign() <= 0 {
		return 0, true
	}
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(value.Num(), value.Denom(), remainder)
	if new(big.Int).Lsh(remainder, 1).Cmp(value.Denom()) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() {
		return 0, false
	}
	return quotient.Int64(), true
}
