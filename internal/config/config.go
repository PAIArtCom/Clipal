package config

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

func ptr[T any](v T) *T { return &v }

const (
	defaultReactivateAfter       = time.Hour
	defaultUpstreamIdleTimeout   = 3 * time.Minute
	defaultResponseHeaderTimeout = 2 * time.Minute
	defaultCircuitOpenTimeout    = 60 * time.Second
)

// LogLevel represents the log level
type LogLevel string

const (
	LogLevelDebug LogLevel = "debug"
	LogLevelInfo  LogLevel = "info"
	LogLevelWarn  LogLevel = "warn"
	LogLevelError LogLevel = "error"
)

type NotificationsConfig struct {
	Enabled        bool     `yaml:"enabled"`
	MinLevel       LogLevel `yaml:"min_level"`
	ProviderSwitch *bool    `yaml:"provider_switch"`
}

type CircuitBreakerConfig struct {
	// FailureThreshold opens the circuit after this many consecutive failures.
	FailureThreshold int `yaml:"failure_threshold"`
	// SuccessThreshold closes the circuit after this many consecutive successes in half-open.
	SuccessThreshold int `yaml:"success_threshold"`
	// OpenTimeout controls how long the circuit remains open before transitioning to half-open.
	OpenTimeout string `yaml:"open_timeout"`
	// HalfOpenMaxInFlight limits concurrent probe requests in half-open state.
	HalfOpenMaxInFlight int `yaml:"half_open_max_inflight"`
}

type StickySessionsConfig struct {
	Enabled                bool   `yaml:"enabled"`
	ExplicitTTL            string `yaml:"explicit_ttl"`
	CacheHintTTL           string `yaml:"cache_hint_ttl"`
	DynamicFeatureTTL      string `yaml:"dynamic_feature_ttl"`
	DynamicFeatureCapacity int    `yaml:"dynamic_feature_capacity"`
	ResponseLookupTTL      string `yaml:"response_lookup_ttl"`
}

type BusyBackpressureConfig struct {
	Enabled            bool     `yaml:"enabled"`
	RetryDelays        []string `yaml:"retry_delays"`
	ProbeMaxInFlight   int      `yaml:"probe_max_inflight"`
	ShortRetryAfterMax string   `yaml:"short_retry_after_max"`
	MaxInlineWait      string   `yaml:"max_inline_wait"`
}

type RoutingConfig struct {
	StickySessions   StickySessionsConfig   `yaml:"sticky_sessions"`
	BusyBackpressure BusyBackpressureConfig `yaml:"busy_backpressure"`
}

// OpenTimeoutDuration parses the configured circuit breaker timeout.
func (c CircuitBreakerConfig) OpenTimeoutDuration() (time.Duration, error) {
	d, err := time.ParseDuration(strings.TrimSpace(c.OpenTimeout))
	if err != nil {
		return 0, fmt.Errorf("invalid circuit_breaker.open_timeout: %w", err)
	}
	return d, nil
}

// GlobalConfig represents the global configuration
type GlobalConfig struct {
	ListenAddr       string   `yaml:"listen_addr"`
	AllowRemoteProxy bool     `yaml:"allow_remote_proxy"`
	AllowRemoteWebUI bool     `yaml:"allow_remote_web_ui"`
	Port             int      `yaml:"port"`
	LogLevel         LogLevel `yaml:"log_level"`
	ReactivateAfter  string   `yaml:"reactivate_after"`
	// UpstreamIdleTimeout cancels an upstream attempt if no response body bytes are received
	// for the duration (useful for SSE streams that may hang after headers).
	// Set to "0" to disable.
	UpstreamIdleTimeout string `yaml:"upstream_idle_timeout"`
	// ResponseHeaderTimeout controls how long we wait for the upstream to return
	// response headers after the request is fully written. Set to "0" to disable.
	ResponseHeaderTimeout string                  `yaml:"response_header_timeout"`
	UpstreamProxyMode     GlobalUpstreamProxyMode `yaml:"upstream_proxy_mode,omitempty"`
	UpstreamProxyURL      string                  `yaml:"upstream_proxy_url,omitempty"`
	MaxRequestBody        int64                   `yaml:"max_request_body_bytes"`
	LogDir                string                  `yaml:"log_dir"`
	LogRetentionDays      int                     `yaml:"log_retention_days"`
	LogStdout             *bool                   `yaml:"log_stdout"`
	Notifications         NotificationsConfig     `yaml:"notifications"`
	CircuitBreaker        CircuitBreakerConfig    `yaml:"circuit_breaker"`
	Routing               RoutingConfig           `yaml:"routing"`
	// Deprecated: retained only so older config.yaml files still load under
	// strict KnownFields decoding. Runtime no longer reads this field.
	IgnoreCountTokensFailover bool `yaml:"ignore_count_tokens_failover"`
}

// RuntimeDurations contains the parsed global timing values used by the proxy runtime.
type RuntimeDurations struct {
	ReactivateAfter       time.Duration
	UpstreamIdleTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
}

type ClientMode string

const (
	ClientModeAuto   ClientMode = "auto"
	ClientModeManual ClientMode = "manual"
)

type ProviderOverrides struct {
	Model  *string          `yaml:"model,omitempty"`
	OpenAI *OpenAIOverrides `yaml:"openai,omitempty"`
	Claude *ClaudeOverrides `yaml:"claude,omitempty"`
}

type ProviderAuthType string

const (
	ProviderAuthTypeAPIKey ProviderAuthType = "api_key"
	ProviderAuthTypeOAuth  ProviderAuthType = "oauth"
)

type OAuthProvider string

const (
	OAuthProviderCodex       OAuthProvider = "codex"
	OAuthProviderGemini      OAuthProvider = "gemini"
	OAuthProviderAntigravity OAuthProvider = "antigravity"
	OAuthProviderClaude      OAuthProvider = "claude"
)

type OpenAIOverrides struct {
	ReasoningEffort *string `yaml:"reasoning_effort,omitempty"`
}

type ClaudeOverrides struct {
	ThinkingBudgetTokens *int    `yaml:"thinking_budget_tokens,omitempty"`
	Effort               *string `yaml:"effort,omitempty"`
}

type GlobalUpstreamProxyMode string

const (
	GlobalUpstreamProxyModeEnvironment GlobalUpstreamProxyMode = "environment"
	GlobalUpstreamProxyModeDirect      GlobalUpstreamProxyMode = "direct"
	GlobalUpstreamProxyModeCustom      GlobalUpstreamProxyMode = "custom"
)

type ProviderProxyMode string

const (
	ProviderProxyModeDefault ProviderProxyMode = "default"
	ProviderProxyModeDirect  ProviderProxyMode = "direct"
	ProviderProxyModeCustom  ProviderProxyMode = "custom"
)

type UpstreamProxySettingsPatch struct {
	Mode *string
	URL  *string
}

type ProviderProxySettingsPatch struct {
	Mode *string
	URL  *string
}

type providerYAML struct {
	Name                 string             `yaml:"name"`
	BaseURL              string             `yaml:"base_url"`
	APIKey               string             `yaml:"api_key,omitempty"`
	APIKeys              []string           `yaml:"api_keys,omitempty"`
	AuthType             ProviderAuthType   `yaml:"auth_type,omitempty"`
	OAuthProvider        OAuthProvider      `yaml:"oauth_provider,omitempty"`
	OAuthRef             string             `yaml:"oauth_ref,omitempty"`
	OAuthIdentity        string             `yaml:"oauth_identity,omitempty"`
	ProxyMode            ProviderProxyMode  `yaml:"proxy_mode,omitempty"`
	ProxyURL             string             `yaml:"proxy_url,omitempty"`
	Priority             int                `yaml:"priority"`
	Enabled              *bool              `yaml:"enabled,omitempty"`
	Overrides            *ProviderOverrides `yaml:"overrides,omitempty"`
	Model                string             `yaml:"model,omitempty"`
	ReasoningEffort      string             `yaml:"reasoning_effort,omitempty"`
	ThinkingBudgetTokens int                `yaml:"thinking_budget_tokens,omitempty"`
}

// Provider represents an API provider configuration
type Provider struct {
	Name          string             `yaml:"name"`
	BaseURL       string             `yaml:"base_url"`
	APIKey        string             `yaml:"api_key,omitempty"`
	APIKeys       []string           `yaml:"api_keys,omitempty"`
	AuthType      ProviderAuthType   `yaml:"auth_type,omitempty"`
	OAuthProvider OAuthProvider      `yaml:"oauth_provider,omitempty"`
	OAuthRef      string             `yaml:"oauth_ref,omitempty"`
	OAuthIdentity string             `yaml:"oauth_identity,omitempty"`
	ProxyMode     ProviderProxyMode  `yaml:"proxy_mode,omitempty"`
	ProxyURL      string             `yaml:"proxy_url,omitempty"`
	Priority      int                `yaml:"priority"`
	Enabled       *bool              `yaml:"enabled,omitempty"`
	Overrides     *ProviderOverrides `yaml:"-"`
}

func (p *Provider) UnmarshalYAML(value *yaml.Node) error {
	var raw providerYAML
	if err := value.Decode(&raw); err != nil {
		return err
	}
	overrides := NormalizeProviderOverrides(raw.Overrides)
	if overrides == nil {
		overrides = &ProviderOverrides{}
	}
	if trimmed := strings.TrimSpace(raw.Model); trimmed != "" && overrides.Model == nil {
		overrides.Model = ptr(trimmed)
	}
	if trimmed := strings.TrimSpace(raw.ReasoningEffort); trimmed != "" {
		if overrides.OpenAI == nil {
			overrides.OpenAI = &OpenAIOverrides{}
		}
		if overrides.OpenAI.ReasoningEffort == nil {
			overrides.OpenAI.ReasoningEffort = ptr(trimmed)
		}
	}
	if raw.ThinkingBudgetTokens != 0 {
		if overrides.Claude == nil {
			overrides.Claude = &ClaudeOverrides{}
		}
		if overrides.Claude.ThinkingBudgetTokens == nil {
			overrides.Claude.ThinkingBudgetTokens = ptr(raw.ThinkingBudgetTokens)
		}
	}
	*p = Provider{
		Name:          raw.Name,
		BaseURL:       raw.BaseURL,
		APIKey:        raw.APIKey,
		APIKeys:       append([]string(nil), raw.APIKeys...),
		AuthType:      raw.AuthType,
		OAuthProvider: raw.OAuthProvider,
		OAuthRef:      raw.OAuthRef,
		OAuthIdentity: raw.OAuthIdentity,
		ProxyMode:     raw.ProxyMode,
		ProxyURL:      raw.ProxyURL,
		Priority:      raw.Priority,
		Enabled:       raw.Enabled,
		Overrides:     NormalizeProviderOverrides(overrides),
	}
	NormalizeProviderAuthSettings(p)
	NormalizeProviderProxySettings(p)
	return nil
}

func (p Provider) MarshalYAML() (any, error) {
	authType := p.NormalizedAuthType()
	proxyMode := p.NormalizedProxyMode()
	proxyURL := p.NormalizedProxyURL()
	oauthProvider := p.NormalizedOAuthProvider()
	oauthRef := p.NormalizedOAuthRef()
	oauthIdentity := p.NormalizedOAuthIdentity()
	if authType == ProviderAuthTypeAPIKey {
		authType = ""
		oauthProvider = ""
		oauthRef = ""
		oauthIdentity = ""
	}
	if proxyMode == ProviderProxyModeDefault {
		proxyMode = ""
	}
	if proxyMode != ProviderProxyModeCustom {
		proxyURL = ""
	}
	return providerYAML{
		Name:          p.Name,
		BaseURL:       p.BaseURL,
		APIKey:        p.APIKey,
		APIKeys:       append([]string(nil), p.APIKeys...),
		AuthType:      authType,
		OAuthProvider: oauthProvider,
		OAuthRef:      oauthRef,
		OAuthIdentity: oauthIdentity,
		ProxyMode:     proxyMode,
		ProxyURL:      proxyURL,
		Priority:      p.Priority,
		Enabled:       p.Enabled,
		Overrides:     NormalizeProviderOverrides(p.Overrides),
	}, nil
}

func (p Provider) ModelOverride() string {
	if p.Overrides == nil || p.Overrides.Model == nil {
		return ""
	}
	return strings.TrimSpace(*p.Overrides.Model)
}

func (p Provider) OpenAIReasoningEffort() string {
	if p.Overrides == nil || p.Overrides.OpenAI == nil || p.Overrides.OpenAI.ReasoningEffort == nil {
		return ""
	}
	return strings.TrimSpace(*p.Overrides.OpenAI.ReasoningEffort)
}

func (p Provider) ClaudeThinkingBudgetTokens() int {
	if p.Overrides == nil || p.Overrides.Claude == nil || p.Overrides.Claude.ThinkingBudgetTokens == nil {
		return 0
	}
	return *p.Overrides.Claude.ThinkingBudgetTokens
}

func (p Provider) ClaudeEffort() string {
	if p.Overrides == nil || p.Overrides.Claude == nil || p.Overrides.Claude.Effort == nil {
		return ""
	}
	return normalizeClaudeEffort(*p.Overrides.Claude.Effort)
}

func NormalizeProviderOverrides(overrides *ProviderOverrides) *ProviderOverrides {
	if overrides == nil {
		return nil
	}

	var normalized ProviderOverrides
	if overrides.Model != nil {
		trimmed := strings.TrimSpace(*overrides.Model)
		if trimmed != "" {
			normalized.Model = ptr(trimmed)
		}
	}
	if overrides.OpenAI != nil && overrides.OpenAI.ReasoningEffort != nil {
		trimmed := strings.TrimSpace(*overrides.OpenAI.ReasoningEffort)
		if trimmed != "" {
			normalized.OpenAI = &OpenAIOverrides{
				ReasoningEffort: ptr(trimmed),
			}
		}
	}
	if overrides.Claude != nil {
		var claude ClaudeOverrides
		if overrides.Claude.ThinkingBudgetTokens != nil && *overrides.Claude.ThinkingBudgetTokens != 0 {
			claude.ThinkingBudgetTokens = ptr(*overrides.Claude.ThinkingBudgetTokens)
		}
		if overrides.Claude.Effort != nil {
			if trimmed := strings.TrimSpace(*overrides.Claude.Effort); trimmed != "" {
				if effort := normalizeClaudeEffort(trimmed); effort != "" {
					claude.Effort = ptr(effort)
				} else {
					claude.Effort = ptr(trimmed)
				}
			}
		}
		if claude.ThinkingBudgetTokens != nil || claude.Effort != nil {
			normalized.Claude = &claude
		}
	}
	if normalized.Model == nil && normalized.OpenAI == nil && normalized.Claude == nil {
		return nil
	}
	return &normalized
}

func normalizeClaudeEffort(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "xhigh":
		return "max"
	case "low", "medium", "high", "max":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func NormalizeProviderAuthSettings(provider *Provider) {
	if provider == nil {
		return
	}
	provider.AuthType = provider.NormalizedAuthType()
	if provider.AuthType != ProviderAuthTypeOAuth {
		provider.OAuthProvider = ""
		provider.OAuthRef = ""
		provider.OAuthIdentity = ""
		return
	}
	provider.OAuthProvider = provider.NormalizedOAuthProvider()
	provider.OAuthRef = provider.NormalizedOAuthRef()
	provider.OAuthIdentity = provider.NormalizedOAuthIdentity()
}

func NormalizeProviderProxySettings(provider *Provider) {
	if provider == nil {
		return
	}
	provider.ProxyMode = provider.NormalizedProxyMode()
	provider.ProxyURL = provider.NormalizedProxyURL()
}

func NormalizeUpstreamProxySettings(global *GlobalConfig) {
	if global == nil {
		return
	}
	global.UpstreamProxyMode = global.NormalizedUpstreamProxyMode()
	global.UpstreamProxyURL = global.NormalizedUpstreamProxyURL()
}

func NormalizeUpstreamProxySettingsPatch(patch *UpstreamProxySettingsPatch) {
	if patch == nil {
		return
	}
	patch.Mode = normalizeLowerTrimStringPtr(patch.Mode)
	patch.URL = normalizeTrimStringPtr(patch.URL)
}

func NormalizeProviderProxySettingsPatch(patch *ProviderProxySettingsPatch) {
	if patch == nil {
		return
	}
	patch.Mode = normalizeLowerTrimStringPtr(patch.Mode)
	patch.URL = normalizeTrimStringPtr(patch.URL)
}

func (g GlobalConfig) NormalizedUpstreamProxyMode() GlobalUpstreamProxyMode {
	mode := strings.ToLower(strings.TrimSpace(string(g.UpstreamProxyMode)))
	if mode == "" {
		return GlobalUpstreamProxyModeEnvironment
	}
	return GlobalUpstreamProxyMode(mode)
}

func (g GlobalConfig) NormalizedUpstreamProxyURL() string {
	return strings.TrimSpace(g.UpstreamProxyURL)
}

// CanonicalUpstreamProxyURL returns the canonical text form of the configured
// upstream proxy URL.
func (g GlobalConfig) CanonicalUpstreamProxyURL() string {
	return CanonicalProxyURL(g.UpstreamProxyURL)
}

// EffectiveUpstreamProxyIdentity returns the runtime proxy identity used for
// policy key construction and reload identity comparison.
func (g GlobalConfig) EffectiveUpstreamProxyIdentity() string {
	return EffectiveProxyIdentity(g.UpstreamProxyURL)
}

func (p Provider) NormalizedProxyMode() ProviderProxyMode {
	mode := strings.ToLower(strings.TrimSpace(string(p.ProxyMode)))
	if mode == "" {
		return ProviderProxyModeDefault
	}
	return ProviderProxyMode(mode)
}

func (p Provider) NormalizedAuthType() ProviderAuthType {
	authType := strings.ToLower(strings.TrimSpace(string(p.AuthType)))
	if authType == "" {
		return ProviderAuthTypeAPIKey
	}
	return ProviderAuthType(authType)
}

func (p Provider) NormalizedOAuthProvider() OAuthProvider {
	return OAuthProvider(strings.ToLower(strings.TrimSpace(string(p.OAuthProvider))))
}

func (p Provider) NormalizedOAuthRef() string {
	return strings.TrimSpace(p.OAuthRef)
}

func (p Provider) NormalizedOAuthIdentity() string {
	return strings.TrimSpace(p.OAuthIdentity)
}

func (p Provider) UsesOAuth() bool {
	return p.NormalizedAuthType() == ProviderAuthTypeOAuth
}

func (p Provider) NormalizedProxyURL() string {
	return strings.TrimSpace(p.ProxyURL)
}

// CanonicalProxyURL returns the canonical text form of the configured provider
// proxy URL.
func (p Provider) CanonicalProxyURL() string {
	return CanonicalProxyURL(p.ProxyURL)
}

// EffectiveProxyIdentity returns the runtime proxy identity used for policy key
// construction and reload identity comparison.
func (p Provider) EffectiveProxyIdentity() string {
	return EffectiveProxyIdentity(p.ProxyURL)
}

func ApplyUpstreamProxySettings(global *GlobalConfig, patch UpstreamProxySettingsPatch) error {
	if global == nil {
		return nil
	}
	NormalizeUpstreamProxySettingsPatch(&patch)
	if patch.Mode == nil {
		if patch.URL != nil {
			return fmt.Errorf("upstream_proxy_url requires upstream_proxy_mode")
		}
		return nil
	}

	mode := GlobalUpstreamProxyMode(*patch.Mode)
	proxyURL := global.NormalizedUpstreamProxyURL()
	if patch.URL != nil {
		proxyURL = *patch.URL
	}

	switch mode {
	case GlobalUpstreamProxyModeEnvironment, GlobalUpstreamProxyModeDirect:
		if patch.URL != nil && proxyURL != "" {
			return fmt.Errorf("upstream_proxy_url requires upstream_proxy_mode custom")
		}
		global.UpstreamProxyMode = mode
		global.UpstreamProxyURL = ""
		return nil
	case GlobalUpstreamProxyModeCustom:
		if proxyURL == "" {
			return fmt.Errorf("upstream_proxy_url is required when upstream_proxy_mode=custom")
		}
		if err := ValidateProxyURL(proxyURL); err != nil {
			return err
		}
		global.UpstreamProxyMode = mode
		global.UpstreamProxyURL = proxyURL
		return nil
	default:
		return fmt.Errorf("upstream_proxy_mode must be one of environment, direct, custom")
	}
}

func ApplyProviderProxySettings(provider *Provider, patch ProviderProxySettingsPatch, isUpdate bool) error {
	if provider == nil {
		return nil
	}
	NormalizeProviderProxySettingsPatch(&patch)
	if !isUpdate && patch.Mode == nil && patch.URL == nil {
		provider.ProxyMode = ProviderProxyModeDefault
		provider.ProxyURL = ""
		return nil
	}
	if patch.Mode == nil {
		if patch.URL != nil {
			return fmt.Errorf("proxy_url requires proxy_mode")
		}
		return nil
	}

	mode := ProviderProxyMode(*patch.Mode)
	proxyURL := provider.NormalizedProxyURL()
	if patch.URL != nil {
		proxyURL = *patch.URL
	}

	switch mode {
	case ProviderProxyModeDefault, ProviderProxyModeDirect:
		if patch.URL != nil && proxyURL != "" {
			return fmt.Errorf("proxy_url requires proxy_mode custom")
		}
		provider.ProxyMode = mode
		provider.ProxyURL = ""
		return nil
	case ProviderProxyModeCustom:
		if proxyURL == "" {
			return fmt.Errorf("proxy_url is required when proxy_mode=custom")
		}
		if err := ValidateProxyURL(proxyURL); err != nil {
			return err
		}
		provider.ProxyMode = mode
		provider.ProxyURL = proxyURL
		return nil
	default:
		return fmt.Errorf("proxy_mode must be one of default, direct, custom")
	}
}

func validateGlobalProxySettings(scope string, mode GlobalUpstreamProxyMode, rawURL string) error {
	switch mode {
	case GlobalUpstreamProxyModeEnvironment, GlobalUpstreamProxyModeDirect:
		if rawURL != "" {
			return fmt.Errorf("%s: upstream_proxy_url requires upstream_proxy_mode custom", scope)
		}
	case GlobalUpstreamProxyModeCustom:
		if rawURL == "" {
			return fmt.Errorf("%s: upstream_proxy_url is required when upstream_proxy_mode=custom", scope)
		}
		if err := ValidateProxyURL(rawURL); err != nil {
			return fmt.Errorf("%s: %w", scope, err)
		}
	default:
		return fmt.Errorf("%s: invalid upstream_proxy_mode %q", scope, mode)
	}
	return nil
}

func validateProviderProxySettings(scope string, mode ProviderProxyMode, rawURL string) error {
	switch mode {
	case ProviderProxyModeDefault, ProviderProxyModeDirect:
		if rawURL != "" {
			return fmt.Errorf("%s: proxy_url requires proxy_mode custom", scope)
		}
	case ProviderProxyModeCustom:
		if rawURL == "" {
			return fmt.Errorf("%s: proxy_url is required when proxy_mode=custom", scope)
		}
		if err := ValidateProxyURL(rawURL); err != nil {
			return fmt.Errorf("%s: %w", scope, err)
		}
	default:
		return fmt.Errorf("%s: invalid proxy_mode %q", scope, mode)
	}
	return nil
}

func normalizeLowerTrimStringPtr(v *string) *string {
	if v == nil {
		return nil
	}
	trimmed := strings.ToLower(strings.TrimSpace(*v))
	return &trimmed
}

func normalizeTrimStringPtr(v *string) *string {
	if v == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*v)
	return &trimmed
}

// IsEnabled returns whether the provider is enabled (default true)
func (p *Provider) IsEnabled() bool {
	if p.Enabled == nil {
		return true
	}
	return *p.Enabled
}

// NormalizedAPIKeys returns the configured API keys with whitespace removed,
// empty entries dropped, and duplicates removed while preserving order.
func (p *Provider) NormalizedAPIKeys() []string {
	if p == nil {
		return nil
	}
	keys := make([]string, 0, len(p.APIKeys)+1)
	seen := make(map[string]struct{}, len(p.APIKeys)+1)
	appendKey := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		if _, ok := seen[v]; ok {
			return
		}
		seen[v] = struct{}{}
		keys = append(keys, v)
	}
	if strings.TrimSpace(p.APIKey) != "" {
		appendKey(p.APIKey)
	}
	for _, key := range p.APIKeys {
		appendKey(key)
	}
	return keys
}

// PrimaryAPIKey returns the first normalized API key, or an empty string.
func (p *Provider) PrimaryAPIKey() string {
	keys := p.NormalizedAPIKeys()
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

// KeyCount returns the number of normalized API keys configured for the provider.
func (p *Provider) KeyCount() int {
	return len(p.NormalizedAPIKeys())
}

// ClientConfig represents a client-specific configuration
type ClientConfig struct {
	Mode           ClientMode `yaml:"mode"`
	PinnedProvider string     `yaml:"pinned_provider"`
	Providers      []Provider `yaml:"providers"`
}

// Config represents the complete application configuration
type Config struct {
	Global    GlobalConfig
	Claude    ClientConfig
	OpenAI    ClientConfig
	Gemini    ClientConfig
	configDir string
}

type clientConfigFileSpec struct {
	clientType  string
	currentName string
	legacyName  string
}

var clientConfigFileSpecs = []clientConfigFileSpec{
	{clientType: "claude", currentName: "claude.yaml", legacyName: "claude-code.yaml"},
	{clientType: "openai", currentName: "openai.yaml", legacyName: "codex.yaml"},
	{clientType: "gemini", currentName: "gemini.yaml", legacyName: ""},
}

func CanonicalClientType(clientType string) (string, bool) {
	switch strings.TrimSpace(strings.ToLower(clientType)) {
	case "claude", "claude-code", "claudecode":
		return "claude", true
	case "openai", "codex":
		return "openai", true
	case "gemini":
		return "gemini", true
	default:
		return "", false
	}
}

// DefaultGlobalConfig returns the default global configuration
func DefaultGlobalConfig() GlobalConfig {
	return GlobalConfig{
		ListenAddr:            "127.0.0.1",
		Port:                  3333,
		LogLevel:              LogLevelInfo,
		ReactivateAfter:       "1h",
		UpstreamIdleTimeout:   "3m",
		ResponseHeaderTimeout: "2m",
		UpstreamProxyMode:     GlobalUpstreamProxyModeEnvironment,
		UpstreamProxyURL:      "",
		// Default body limit: 32 MiB. clipal buffers request bodies to support retries,
		// so a hard cap prevents unbounded memory usage.
		MaxRequestBody:   32 * 1024 * 1024,
		LogDir:           "",
		LogRetentionDays: 7,
		LogStdout:        ptr(true),
		Notifications: NotificationsConfig{
			Enabled:        false,
			MinLevel:       LogLevelError,
			ProviderSwitch: ptr(true),
		},
		CircuitBreaker: CircuitBreakerConfig{
			// Conservative defaults: only trips on sustained failures.
			FailureThreshold:    4,
			SuccessThreshold:    2,
			OpenTimeout:         "60s",
			HalfOpenMaxInFlight: 1,
		},
		Routing: RoutingConfig{
			StickySessions: StickySessionsConfig{
				Enabled:                true,
				ExplicitTTL:            "30m",
				CacheHintTTL:           "10m",
				DynamicFeatureTTL:      "10m",
				DynamicFeatureCapacity: 1024,
				ResponseLookupTTL:      "15m",
			},
			BusyBackpressure: BusyBackpressureConfig{
				Enabled:            true,
				RetryDelays:        []string{"5s", "10s"},
				ProbeMaxInFlight:   1,
				ShortRetryAfterMax: "3s",
				MaxInlineWait:      "8s",
			},
		},
	}
}

// DefaultRuntimeDurations returns the runtime timing defaults used by Clipal.
func DefaultRuntimeDurations() RuntimeDurations {
	return RuntimeDurations{
		ReactivateAfter:       defaultReactivateAfter,
		UpstreamIdleTimeout:   defaultUpstreamIdleTimeout,
		ResponseHeaderTimeout: defaultResponseHeaderTimeout,
	}
}

// Load loads the configuration from the specified directory
func Load(configDir string) (*Config, error) {
	cfg := &Config{
		Global:    DefaultGlobalConfig(),
		configDir: configDir,
	}

	// Load global config
	globalPath := filepath.Join(configDir, "config.yaml")
	if err := loadYAML(globalPath, &cfg.Global); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to load global config: %w", err)
	}
	NormalizeUpstreamProxySettings(&cfg.Global)

	if err := migrateLegacyClientConfigFiles(configDir); err != nil {
		return nil, err
	}

	// Load client configs
	claudePath := filepath.Join(configDir, "claude.yaml")
	if err := loadYAML(claudePath, &cfg.Claude); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to load claude config: %w", err)
	}

	openAIPath := filepath.Join(configDir, "openai.yaml")
	if err := loadYAML(openAIPath, &cfg.OpenAI); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to load openai config: %w", err)
	}

	geminiPath := filepath.Join(configDir, "gemini.yaml")
	if err := loadYAML(geminiPath, &cfg.Gemini); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to load gemini config: %w", err)
	}

	applyClientDefaults(&cfg.Claude)
	applyClientDefaults(&cfg.OpenAI)
	applyClientDefaults(&cfg.Gemini)

	// Sort providers by priority
	sortProviders(cfg.Claude.Providers)
	sortProviders(cfg.OpenAI.Providers)
	sortProviders(cfg.Gemini.Providers)

	return cfg, nil
}

func clientConfigFileSpecForType(clientType string) (clientConfigFileSpec, bool) {
	clientType, ok := CanonicalClientType(clientType)
	if !ok {
		return clientConfigFileSpec{}, false
	}
	for _, spec := range clientConfigFileSpecs {
		if spec.clientType == clientType {
			return spec, true
		}
	}
	return clientConfigFileSpec{}, false
}

func ClientConfigFilename(clientType string) (string, error) {
	spec, ok := clientConfigFileSpecForType(clientType)
	if !ok {
		return "", fmt.Errorf("unknown client type: %q", clientType)
	}
	return spec.currentName, nil
}

func WatchedConfigFilenames() []string {
	names := []string{"config.yaml"}
	seen := map[string]struct{}{"config.yaml": {}}
	for _, spec := range clientConfigFileSpecs {
		if spec.currentName != "" {
			if _, ok := seen[spec.currentName]; !ok {
				names = append(names, spec.currentName)
				seen[spec.currentName] = struct{}{}
			}
		}
	}
	return names
}

func migrateLegacyClientConfigFiles(configDir string) error {
	for _, spec := range clientConfigFileSpecs {
		if err := migrateLegacyClientConfigFile(configDir, spec); err != nil {
			return err
		}
	}
	return nil
}

func migrateLegacyClientConfigFile(configDir string, spec clientConfigFileSpec) error {
	if spec.legacyName == "" || spec.currentName == spec.legacyName {
		return nil
	}

	currentPath := filepath.Join(configDir, spec.currentName)
	legacyPath := filepath.Join(configDir, spec.legacyName)

	currentInfo, currentErr := os.Stat(currentPath)
	legacyInfo, legacyErr := os.Stat(legacyPath)
	currentExists := currentErr == nil
	legacyExists := legacyErr == nil

	if currentErr != nil && !os.IsNotExist(currentErr) {
		return fmt.Errorf("failed to stat %s: %w", spec.currentName, currentErr)
	}
	if legacyErr != nil && !os.IsNotExist(legacyErr) {
		return fmt.Errorf("failed to stat %s: %w", spec.legacyName, legacyErr)
	}
	if currentExists && currentInfo.IsDir() {
		return fmt.Errorf("%s is a directory", spec.currentName)
	}
	if legacyExists && legacyInfo.IsDir() {
		return fmt.Errorf("%s is a directory", spec.legacyName)
	}

	switch {
	case !currentExists && !legacyExists:
		return nil
	case currentExists && !legacyExists:
		return nil
	case !currentExists && legacyExists:
		if err := os.Rename(legacyPath, currentPath); err != nil {
			return fmt.Errorf("failed to migrate %s to %s: %w", spec.legacyName, spec.currentName, err)
		}
		return nil
	default:
		same, err := clientConfigFilesEquivalent(currentPath, legacyPath)
		if err != nil {
			return err
		}
		if !same {
			return fmt.Errorf("both %s and %s exist with different content", spec.currentName, spec.legacyName)
		}
		if err := os.Remove(legacyPath); err != nil {
			return fmt.Errorf("failed to remove legacy config %s: %w", spec.legacyName, err)
		}
		return nil
	}
}

func clientConfigFilesEquivalent(currentPath string, legacyPath string) (bool, error) {
	var current ClientConfig
	if err := loadYAML(currentPath, &current); err != nil {
		return false, fmt.Errorf("failed to load %s: %w", filepath.Base(currentPath), err)
	}
	var legacy ClientConfig
	if err := loadYAML(legacyPath, &legacy); err != nil {
		return false, fmt.Errorf("failed to load %s: %w", filepath.Base(legacyPath), err)
	}
	applyClientDefaults(&current)
	applyClientDefaults(&legacy)
	return reflect.DeepEqual(current, legacy), nil
}

// loadYAML loads a YAML file into the target struct
func loadYAML(path string, target interface{}) error {
	warnIfPermissiveConfig(path)

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	return dec.Decode(target)
}

// sortProviders sorts providers by priority (ascending)
func sortProviders(providers []Provider) {
	// Stable sort so providers with the same priority keep YAML order.
	sort.SliceStable(providers, func(i, j int) bool {
		return providers[i].Priority < providers[j].Priority
	})
}

func applyClientDefaults(cc *ClientConfig) {
	if cc == nil {
		return
	}
	// Normalize mode to lowercase to tolerate "Auto", "MANUAL", etc.
	cc.Mode = ClientMode(strings.ToLower(strings.TrimSpace(string(cc.Mode))))
	if cc.Mode == "" {
		cc.Mode = ClientModeAuto
	}

	// Priority is 1-based. Older configs may omit it (decoded as 0).
	for i := range cc.Providers {
		if cc.Providers[i].Priority == 0 {
			cc.Providers[i].Priority = 1
		}
		cc.Providers[i].BaseURL = strings.TrimSpace(cc.Providers[i].BaseURL)
		cc.Providers[i].APIKey = strings.TrimSpace(cc.Providers[i].APIKey)
		cc.Providers[i].APIKeys = cc.Providers[i].NormalizedAPIKeys()
		NormalizeProviderAuthSettings(&cc.Providers[i])
		NormalizeProviderProxySettings(&cc.Providers[i])
		cc.Providers[i].Overrides = NormalizeProviderOverrides(cc.Providers[i].Overrides)
		if len(cc.Providers[i].APIKeys) == 1 {
			cc.Providers[i].APIKey = cc.Providers[i].APIKeys[0]
			cc.Providers[i].APIKeys = nil
		} else {
			cc.Providers[i].APIKey = ""
		}
	}
}

// RuntimeDurations parses the runtime timing values from global config.
func (g GlobalConfig) RuntimeDurations() (RuntimeDurations, error) {
	out := DefaultRuntimeDurations()

	reactivateAfter, err := time.ParseDuration(strings.TrimSpace(g.ReactivateAfter))
	if err != nil {
		return RuntimeDurations{}, fmt.Errorf("invalid reactivate_after: %w", err)
	}
	upstreamIdle, err := time.ParseDuration(strings.TrimSpace(g.UpstreamIdleTimeout))
	if err != nil {
		return RuntimeDurations{}, fmt.Errorf("invalid upstream_idle_timeout: %w", err)
	}
	responseHeaderTimeout, err := time.ParseDuration(strings.TrimSpace(g.ResponseHeaderTimeout))
	if err != nil {
		return RuntimeDurations{}, fmt.Errorf("invalid response_header_timeout: %w", err)
	}

	out.ReactivateAfter = reactivateAfter
	out.UpstreamIdleTimeout = upstreamIdle
	out.ResponseHeaderTimeout = responseHeaderTimeout
	return out, nil
}

// GetEnabledProviders returns only enabled providers for a client config
func GetEnabledProviders(cc ClientConfig) []Provider {
	var enabled []Provider
	for _, p := range cc.Providers {
		if p.IsEnabled() {
			enabled = append(enabled, p)
		}
	}
	return enabled
}

// GetConfigDir returns the default config directory
func GetConfigDir() string {
	if dir := os.Getenv("CLIPAL_CONFIG_DIR"); dir != "" {
		return dir
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".clipal")
	}
	return ".clipal"
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if strings.TrimSpace(c.Global.ListenAddr) == "" {
		return fmt.Errorf("listen_addr cannot be empty")
	}
	if !isLoopbackListenAddr(c.Global.ListenAddr) && !c.Global.AllowRemoteProxy {
		return fmt.Errorf("non-loopback listen_addr requires allow_remote_proxy: true")
	}
	if c.Global.Port < 1 || c.Global.Port > 65535 {
		return fmt.Errorf("invalid port: %d", c.Global.Port)
	}
	if c.Global.MaxRequestBody < 1 {
		return fmt.Errorf("invalid max_request_body_bytes: %d", c.Global.MaxRequestBody)
	}

	switch c.Global.LogLevel {
	case LogLevelDebug, LogLevelInfo, LogLevelWarn, LogLevelError:
		// valid
	default:
		return fmt.Errorf("invalid log level: %s", c.Global.LogLevel)
	}

	if c.Global.Notifications.MinLevel != "" {
		switch c.Global.Notifications.MinLevel {
		case LogLevelDebug, LogLevelInfo, LogLevelWarn, LogLevelError:
			// valid
		default:
			return fmt.Errorf("invalid notifications.min_level: %s", c.Global.Notifications.MinLevel)
		}
	}

	d, err := time.ParseDuration(c.Global.ReactivateAfter)
	if err != nil || d < 0 {
		return fmt.Errorf("invalid reactivate_after: %s", c.Global.ReactivateAfter)
	}
	idle, err := time.ParseDuration(c.Global.UpstreamIdleTimeout)
	if err != nil || idle < 0 {
		return fmt.Errorf("invalid upstream_idle_timeout: %s", c.Global.UpstreamIdleTimeout)
	}
	respHdr, err := time.ParseDuration(c.Global.ResponseHeaderTimeout)
	if err != nil || respHdr < 0 {
		return fmt.Errorf("invalid response_header_timeout: %s", c.Global.ResponseHeaderTimeout)
	}
	if c.Global.LogRetentionDays < 0 {
		return fmt.Errorf("invalid log_retention_days: %d", c.Global.LogRetentionDays)
	}
	if err := validateGlobalProxySettings("global upstream proxy", c.Global.NormalizedUpstreamProxyMode(), c.Global.NormalizedUpstreamProxyURL()); err != nil {
		return err
	}

	// Circuit breaker:
	// - failure_threshold == 0 disables the circuit breaker entirely.
	// - failure_threshold  > 0 enables it and requires the other fields to be valid.
	if c.Global.CircuitBreaker.FailureThreshold < 0 {
		return fmt.Errorf("invalid circuit_breaker.failure_threshold: %d", c.Global.CircuitBreaker.FailureThreshold)
	}
	if c.Global.CircuitBreaker.SuccessThreshold < 0 {
		return fmt.Errorf("invalid circuit_breaker.success_threshold: %d", c.Global.CircuitBreaker.SuccessThreshold)
	}
	if c.Global.CircuitBreaker.HalfOpenMaxInFlight < 0 {
		return fmt.Errorf("invalid circuit_breaker.half_open_max_inflight: %d", c.Global.CircuitBreaker.HalfOpenMaxInFlight)
	}
	if c.Global.CircuitBreaker.FailureThreshold > 0 {
		if c.Global.CircuitBreaker.SuccessThreshold <= 0 {
			return fmt.Errorf("invalid circuit_breaker.success_threshold: %d", c.Global.CircuitBreaker.SuccessThreshold)
		}
		if c.Global.CircuitBreaker.HalfOpenMaxInFlight <= 0 {
			return fmt.Errorf("invalid circuit_breaker.half_open_max_inflight: %d", c.Global.CircuitBreaker.HalfOpenMaxInFlight)
		}
		cbTimeout, err := time.ParseDuration(c.Global.CircuitBreaker.OpenTimeout)
		if err != nil || cbTimeout <= 0 {
			return fmt.Errorf("invalid circuit_breaker.open_timeout: %s", c.Global.CircuitBreaker.OpenTimeout)
		}
	}

	if err := validateRoutingConfig(c.Global.Routing); err != nil {
		return err
	}

	if err := validateClientConfig("claude", c.Claude); err != nil {
		return err
	}
	if err := validateClientConfig("openai", c.OpenAI); err != nil {
		return err
	}
	if err := validateClientConfig("gemini", c.Gemini); err != nil {
		return err
	}

	// Validate providers
	if err := validateProviders("claude", c.Claude.Providers); err != nil {
		return err
	}
	if err := validateProviders("openai", c.OpenAI.Providers); err != nil {
		return err
	}
	if err := validateProviders("gemini", c.Gemini.Providers); err != nil {
		return err
	}

	return nil
}

func isLoopbackListenAddr(value string) bool {
	host := strings.Trim(strings.TrimSpace(value), "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (c *Config) ConfigDir() string {
	return c.configDir
}

func validateClientConfig(name string, cc ClientConfig) error {
	switch cc.Mode {
	case ClientModeAuto, ClientModeManual:
		// ok
	default:
		return fmt.Errorf("%s: invalid mode: %q (expected %q or %q)", name, cc.Mode, ClientModeAuto, ClientModeManual)
	}
	if cc.Mode == ClientModeManual {
		pin := strings.TrimSpace(cc.PinnedProvider)
		if pin == "" {
			return fmt.Errorf("%s: pinned_provider is required when mode=manual", name)
		}
		found := false
		for _, p := range cc.Providers {
			if p.Name == pin {
				if !p.IsEnabled() {
					return fmt.Errorf("%s: pinned_provider %q is disabled", name, pin)
				}
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%s: pinned_provider %q not found", name, pin)
		}
	}
	return nil
}

func validateProviders(clientName string, providers []Provider) error {
	seenNames := make(map[string]int, len(providers))
	for i, p := range providers {
		if p.Name == "" {
			return fmt.Errorf("%s provider %d: name is required", clientName, i+1)
		}
		if firstIdx, ok := seenNames[p.Name]; ok {
			return fmt.Errorf("%s: duplicate provider name %q at positions %d and %d", clientName, p.Name, firstIdx+1, i+1)
		}
		seenNames[p.Name] = i
		switch p.NormalizedAuthType() {
		case ProviderAuthTypeAPIKey, ProviderAuthTypeOAuth:
			// ok
		default:
			return fmt.Errorf("%s provider %s: auth_type must be one of %q or %q", clientName, p.Name, ProviderAuthTypeAPIKey, ProviderAuthTypeOAuth)
		}
		if strings.TrimSpace(p.APIKey) != "" && len(p.APIKeys) > 0 {
			return fmt.Errorf("%s provider %s: api_key and api_keys cannot both be set", clientName, p.Name)
		}
		if p.UsesOAuth() {
			if len(p.NormalizedAPIKeys()) > 0 {
				return fmt.Errorf("%s provider %s: api_key and api_keys cannot be set when auth_type=oauth", clientName, p.Name)
			}
			if p.NormalizedOAuthProvider() == "" {
				return fmt.Errorf("%s provider %s: oauth_provider is required when auth_type=oauth", clientName, p.Name)
			}
			if p.NormalizedOAuthRef() == "" {
				return fmt.Errorf("%s provider %s: oauth_ref is required when auth_type=oauth", clientName, p.Name)
			}
			if strings.TrimSpace(p.BaseURL) != "" {
				return fmt.Errorf("%s provider %s: base_url is not allowed when auth_type=oauth", clientName, p.Name)
			}
			switch p.NormalizedOAuthProvider() {
			case OAuthProviderCodex:
				if clientName != "openai" {
					return fmt.Errorf("%s provider %s: oauth_provider %q is only supported for openai client", clientName, p.Name, OAuthProviderCodex)
				}
			case OAuthProviderGemini, OAuthProviderAntigravity:
				if clientName != "gemini" {
					return fmt.Errorf("%s provider %s: oauth_provider %q is only supported for gemini client", clientName, p.Name, p.NormalizedOAuthProvider())
				}
			case OAuthProviderClaude:
				if clientName != "claude" {
					return fmt.Errorf("%s provider %s: oauth_provider %q is only supported for claude client", clientName, p.Name, OAuthProviderClaude)
				}
			default:
				return fmt.Errorf("%s provider %s: unsupported oauth_provider %q", clientName, p.Name, p.NormalizedOAuthProvider())
			}
		} else {
			if p.BaseURL == "" {
				return fmt.Errorf("%s provider %s: base_url is required", clientName, p.Name)
			}
			if len(p.NormalizedAPIKeys()) == 0 {
				return fmt.Errorf("%s provider %s: api_key or api_keys is required", clientName, p.Name)
			}
			if p.NormalizedOAuthProvider() != "" || p.NormalizedOAuthRef() != "" {
				return fmt.Errorf("%s provider %s: oauth_provider and oauth_ref require auth_type=oauth", clientName, p.Name)
			}
		}
		if p.Priority < 1 {
			return fmt.Errorf("%s provider %s: priority must be >= 1", clientName, p.Name)
		}
		if err := validateProviderProxySettings(fmt.Sprintf("%s provider %s", clientName, p.Name), p.NormalizedProxyMode(), p.NormalizedProxyURL()); err != nil {
			return err
		}
		if !providerOverridesSupportedForClient(clientName, p.Overrides) {
			return fmt.Errorf("%s provider %s: unsupported overrides for client", clientName, p.Name)
		}
		if p.ClaudeThinkingBudgetTokens() < 0 {
			return fmt.Errorf("%s provider %s: thinking_budget_tokens must be >= 0", clientName, p.Name)
		}
		if p.Overrides != nil && p.Overrides.Claude != nil && p.Overrides.Claude.Effort != nil && p.ClaudeEffort() == "" {
			return fmt.Errorf("%s provider %s: claude effort must be one of low, medium, high, max, xhigh", clientName, p.Name)
		}
	}
	return nil
}

func providerOverridesSupportedForClient(clientName string, overrides *ProviderOverrides) bool {
	if overrides == nil {
		return true
	}
	switch clientName {
	case "openai":
		return overrides.Model == nil && overrides.OpenAI == nil && overrides.Claude == nil ||
			overrides.Claude == nil
	case "claude":
		return overrides.Model == nil && overrides.OpenAI == nil && overrides.Claude == nil ||
			overrides.OpenAI == nil
	case "gemini":
		return overrides == nil
	default:
		return false
	}
}

func validateRoutingConfig(rc RoutingConfig) error {
	if rc.StickySessions.Enabled {
		if err := validateOptionalPositiveDuration("routing.sticky_sessions.explicit_ttl", rc.StickySessions.ExplicitTTL); err != nil {
			return err
		}
		if err := validatePositiveDuration("routing.sticky_sessions.cache_hint_ttl", rc.StickySessions.CacheHintTTL); err != nil {
			return err
		}
		if err := validatePositiveDuration("routing.sticky_sessions.dynamic_feature_ttl", rc.StickySessions.DynamicFeatureTTL); err != nil {
			return err
		}
		if err := validatePositiveDuration("routing.sticky_sessions.response_lookup_ttl", rc.StickySessions.ResponseLookupTTL); err != nil {
			return err
		}
		if rc.StickySessions.DynamicFeatureCapacity <= 0 {
			return fmt.Errorf("invalid routing.sticky_sessions.dynamic_feature_capacity: %d", rc.StickySessions.DynamicFeatureCapacity)
		}
	}

	if rc.BusyBackpressure.Enabled {
		if len(rc.BusyBackpressure.RetryDelays) == 0 {
			return fmt.Errorf("invalid routing.busy_backpressure.retry_delays: empty")
		}
		for i, delay := range rc.BusyBackpressure.RetryDelays {
			if err := validatePositiveDuration(fmt.Sprintf("routing.busy_backpressure.retry_delays[%d]", i), delay); err != nil {
				return err
			}
		}
		if rc.BusyBackpressure.ProbeMaxInFlight < 0 {
			return fmt.Errorf("invalid routing.busy_backpressure.probe_max_inflight: %d", rc.BusyBackpressure.ProbeMaxInFlight)
		}
		if err := validateOptionalPositiveDuration("routing.busy_backpressure.short_retry_after_max", rc.BusyBackpressure.ShortRetryAfterMax); err != nil {
			return err
		}
		if err := validateOptionalPositiveDuration("routing.busy_backpressure.max_inline_wait", rc.BusyBackpressure.MaxInlineWait); err != nil {
			return err
		}
	}

	return nil
}

func validatePositiveDuration(field string, value string) error {
	d, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil || d <= 0 {
		return fmt.Errorf("invalid %s: %s", field, value)
	}
	return nil
}

func validateOptionalPositiveDuration(field string, value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return validatePositiveDuration(field, value)
}
