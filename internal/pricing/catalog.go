package pricing

import (
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"sync"
)

// model-specs is vendored so pricing remains reproducible and works offline.
//
//go:embed catalog.json
var embeddedCatalog []byte

//go:embed catalog.sha
var embeddedCatalogVersion string

//go:embed catalog.commit
var embeddedModelSpecsCommit string

//go:embed catalog.integrity.sha256
var embeddedCatalogIntegrity string

type Platform string

const (
	PlatformOpenAI    Platform = "openai"
	PlatformAnthropic Platform = "anthropic"
	PlatformGemini    Platform = "gemini"
)

type ServiceTier string

const (
	ServiceTierStandard ServiceTier = "standard"
	ServiceTierBatch    ServiceTier = "batch"
	ServiceTierPriority ServiceTier = "priority"
	ServiceTierFlex     ServiceTier = "flex"
)

type Usage struct {
	UncachedInputTokens int64
	CachedInputTokens   int64
	CacheWriteTokens    int64
	CacheWrite1HTokens  int64
	AudioInputTokens    int64
	OutputTokens        int64
	ReasoningTokens     int64
	SearchQueries       int64
	SearchContextSize   string
	ServiceTier         ServiceTier
	// FastMode marks Anthropic fast-mode usage; token rates are multiplied by
	// the model's fast-mode premium.
	FastMode bool
	// InferenceGeo is the Anthropic inference geography reported in usage;
	// "us" applies the model's US regional processing premium to token rates.
	InferenceGeo string
}

type Metadata struct {
	Version          string `json:"version"`
	GeneratedAt      string `json:"generated_at"`
	ModelSpecsCommit string `json:"model_specs_commit"`
	UpstreamSource   string `json:"upstream_source"`
	UpstreamSHA      string `json:"upstream_sha"`
}

type catalogFile struct {
	Version     string               `json:"version"`
	GeneratedAt string               `json:"generated_at"`
	Upstream    catalogUpstream      `json:"upstream"`
	Models      map[string]modelSpec `json:"models"`
}

type catalogUpstream struct {
	Source string `json:"source"`
	SHA    string `json:"sha"`
}

type modelSpec struct {
	Platform string `json:"platform"`

	Input                       rate            `json:"input_cost_per_token"`
	Output                      rate            `json:"output_cost_per_token"`
	CacheRead                   rate            `json:"cache_read_input_token_cost"`
	CacheWrite                  rate            `json:"cache_creation_input_token_cost"`
	CacheWrite1H                rate            `json:"cache_creation_input_token_cost_above_1hr"`
	AudioInput                  rate            `json:"input_cost_per_audio_token"`
	ReasoningOutput             rate            `json:"output_cost_per_reasoning_token"`
	BatchInput                  rate            `json:"input_cost_per_token_batches"`
	BatchOutput                 rate            `json:"output_cost_per_token_batches"`
	BatchCacheRead              rate            `json:"cache_read_input_token_cost_batches"`
	BatchCacheWrite             rate            `json:"cache_creation_input_token_cost_batches"`
	PriorityInput               rate            `json:"input_cost_per_token_priority"`
	PriorityOutput              rate            `json:"output_cost_per_token_priority"`
	PriorityCacheRead           rate            `json:"cache_read_input_token_cost_priority"`
	PriorityCacheWrite          rate            `json:"cache_creation_input_token_cost_priority"`
	FlexInput                   rate            `json:"input_cost_per_token_flex"`
	FlexOutput                  rate            `json:"output_cost_per_token_flex"`
	FlexCacheRead               rate            `json:"cache_read_input_token_cost_flex"`
	FlexCacheWrite              rate            `json:"cache_creation_input_token_cost_flex"`
	Above200KInput              rate            `json:"input_cost_per_token_above_200k_tokens"`
	Above200KOutput             rate            `json:"output_cost_per_token_above_200k_tokens"`
	Above200KCacheRead          rate            `json:"cache_read_input_token_cost_above_200k_tokens"`
	Above200KCacheWrite         rate            `json:"cache_creation_input_token_cost_above_200k_tokens"`
	Above272KInput              rate            `json:"input_cost_per_token_above_272k_tokens"`
	Above272KOutput             rate            `json:"output_cost_per_token_above_272k_tokens"`
	Above272KCacheRead          rate            `json:"cache_read_input_token_cost_above_272k_tokens"`
	Above272KCacheWrite         rate            `json:"cache_creation_input_token_cost_above_272k_tokens"`
	Above272KFlexInput          rate            `json:"input_cost_per_token_above_272k_tokens_flex"`
	Above272KFlexOutput         rate            `json:"output_cost_per_token_above_272k_tokens_flex"`
	Above272KFlexCacheRead      rate            `json:"cache_read_input_token_cost_above_272k_tokens_flex"`
	Above272KFlexCacheWrite     rate            `json:"cache_creation_input_token_cost_above_272k_tokens_flex"`
	Above272KPriorityInput      rate            `json:"input_cost_per_token_above_272k_tokens_priority"`
	Above272KPriorityOutput     rate            `json:"output_cost_per_token_above_272k_tokens_priority"`
	Above272KPriorityCacheRead  rate            `json:"cache_read_input_token_cost_above_272k_tokens_priority"`
	Above272KPriorityCacheWrite rate            `json:"cache_creation_input_token_cost_above_272k_tokens_priority"`
	Above272KBatchInput         rate            `json:"input_cost_per_token_above_272k_tokens_batches"`
	Above272KBatchOutput        rate            `json:"output_cost_per_token_above_272k_tokens_batches"`
	Above272KBatchCacheRead     rate            `json:"cache_read_input_token_cost_above_272k_tokens_batches"`
	Above272KBatchCacheWrite    rate            `json:"cache_creation_input_token_cost_above_272k_tokens_batches"`
	SearchContext               map[string]rate `json:"search_context_cost_per_query"`
	FastModeUplift              rate            `json:"fast_mode_uplift_multiplier"`
	RegionalUSUplift            rate            `json:"regional_processing_uplift_multiplier_us"`
}

type rate struct {
	value *big.Rat
}

func (r *rate) UnmarshalJSON(data []byte) error {
	value, err := decimalRat(string(data))
	if err != nil {
		return err
	}
	r.value = value
	return nil
}

func decimalRat(raw string) (*big.Rat, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return nil, nil
	}
	if len(raw) > 64 {
		return nil, fmt.Errorf("price is too long")
	}
	if strings.HasPrefix(raw, "-") {
		return nil, fmt.Errorf("negative price %q", raw)
	}
	raw = strings.TrimPrefix(raw, "+")

	mantissa := raw
	exponent := 0
	if index := strings.IndexAny(mantissa, "eE"); index >= 0 {
		if strings.ContainsAny(mantissa[index+1:], "eE") {
			return nil, fmt.Errorf("invalid price %q", raw)
		}
		parsed, err := strconv.Atoi(mantissa[index+1:])
		if err != nil || parsed < -18 || parsed > 18 {
			return nil, fmt.Errorf("invalid price %q", raw)
		}
		exponent = parsed
		mantissa = mantissa[:index]
	}
	parts := strings.Split(mantissa, ".")
	if len(parts) > 2 {
		return nil, fmt.Errorf("invalid price %q", raw)
	}
	digits := parts[0]
	fractionDigits := 0
	if len(parts) == 2 {
		digits += parts[1]
		fractionDigits = len(parts[1])
	}
	if digits == "" || len(digits) > 32 {
		return nil, fmt.Errorf("invalid price %q", raw)
	}
	numerator, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return nil, fmt.Errorf("invalid price %q", raw)
	}
	scale := fractionDigits - exponent
	denominator := big.NewInt(1)
	if scale > 0 {
		denominator.Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
	} else if scale < 0 {
		numerator.Mul(numerator, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(-scale)), nil))
	}
	return new(big.Rat).SetFrac(numerator, denominator), nil
}

var (
	defaultCatalogOnce sync.Once
	defaultCatalog     catalogFile
	defaultCatalogErr  error
)

func loadCatalog() (catalogFile, error) {
	defaultCatalogOnce.Do(func() {
		if err := verifyCatalogIntegrity(embeddedCatalog, embeddedCatalogIntegrity); err != nil {
			defaultCatalogErr = err
			return
		}
		if err := json.Unmarshal(embeddedCatalog, &defaultCatalog); err != nil {
			defaultCatalogErr = fmt.Errorf("decode embedded model-specs catalog: %w", err)
			return
		}
		version := strings.TrimSpace(embeddedCatalogVersion)
		if defaultCatalog.Version == "" || defaultCatalog.Version != version {
			defaultCatalogErr = fmt.Errorf("embedded model-specs version mismatch: catalog=%q checksum=%q", defaultCatalog.Version, version)
			return
		}
		if len(defaultCatalog.Models) == 0 {
			defaultCatalogErr = fmt.Errorf("embedded model-specs catalog has no models")
		}
	})
	return defaultCatalog, defaultCatalogErr
}

func verifyCatalogIntegrity(data []byte, expected string) error {
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	expected = strings.TrimSpace(expected)
	if expected == "" || digest != expected {
		return fmt.Errorf("embedded model-specs integrity mismatch: catalog=%q expected=%q", digest, expected)
	}
	return nil
}

func CatalogMetadata() Metadata {
	catalog, err := loadCatalog()
	if err != nil {
		return Metadata{}
	}
	return Metadata{
		Version:          catalog.Version,
		GeneratedAt:      catalog.GeneratedAt,
		ModelSpecsCommit: strings.TrimSpace(embeddedModelSpecsCommit),
		UpstreamSource:   catalog.Upstream.Source,
		UpstreamSHA:      catalog.Upstream.SHA,
	}
}

func resolveModelWithProvider(model string, platform Platform, providerHint string) (string, modelSpec, bool) {
	catalog, err := loadCatalog()
	if err != nil {
		return "", modelSpec{}, false
	}
	candidates := modelCandidates(model)
	providerHint = strings.Trim(strings.ToLower(strings.TrimSpace(providerHint)), "/")
	if providerHint != "" {
		preferred := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			preferred = append(preferred, providerHint+"/"+candidate)
		}
		candidates = append(preferred, candidates...)
	}
	for _, candidate := range candidates {
		spec, ok := catalog.Models[candidate]
		if ok && platformMatchesProtocol(spec.Platform, platform) {
			return candidate, spec, true
		}
	}
	return "", modelSpec{}, false
}

func platformMatchesProtocol(pricePlatform string, protocolPlatform Platform) bool {
	if protocolPlatform == PlatformOpenAI {
		return true
	}
	return strings.EqualFold(pricePlatform, string(protocolPlatform))
}

func modelCandidates(model string) []string {
	model = strings.ToLower(strings.TrimSpace(model))
	model = strings.TrimPrefix(model, "/")
	if model == "" {
		return nil
	}
	candidates := []string{model}
	for {
		stripped := model
		for _, prefix := range []string{"models/", "model/", "openai/", "anthropic/", "google/", "gemini/", "claude/"} {
			if strings.HasPrefix(stripped, prefix) {
				stripped = strings.TrimPrefix(stripped, prefix)
				break
			}
		}
		if stripped == model {
			break
		}
		model = stripped
		candidates = append(candidates, model)
	}
	return candidates
}
