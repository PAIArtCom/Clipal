// Package transfer implements Clipal's canonical data format, format adapters,
// and the shared import/export service used by every operator surface.
package transfer

import (
	"time"
)

const (
	SchemaName    = "clipal.data"
	SchemaVersion = 1

	MaxImportFiles         = 512
	MaxImportFilenameBytes = 1024
	MaxImportFileBytes     = 16 << 20
	MaxImportTotalBytes    = 64 << 20
	// JSON string encoding can double valid JSON input through quote,
	// backslash, and whitespace escaping. Reserve bounded metadata overhead.
	MaxJSONImportRequestBytes = 2*MaxImportTotalBytes + 2*MaxImportFiles*MaxImportFilenameBytes + (1 << 20)
)

type Dataset struct {
	Schema        string    `json:"schema"`
	SchemaVersion int       `json:"schema_version"`
	ExportedAt    time.Time `json:"exported_at"`
	Producer      Producer  `json:"producer"`
	Data          Data      `json:"data"`
}

type Producer struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type Data struct {
	Global      Global            `json:"global"`
	Clients     map[string]Client `json:"clients"`
	Credentials []Credential      `json:"credentials"`
	Usage       Usage             `json:"usage"`
}

type Global struct {
	ListenAddr            string         `json:"listen_addr"`
	Port                  int            `json:"port"`
	LogLevel              string         `json:"log_level"`
	ReactivateAfter       string         `json:"reactivate_after"`
	UpstreamIdleTimeout   string         `json:"upstream_idle_timeout"`
	ResponseHeaderTimeout string         `json:"response_header_timeout"`
	UpstreamProxyMode     string         `json:"upstream_proxy_mode"`
	UpstreamProxyURL      string         `json:"upstream_proxy_url,omitempty"`
	MaxRequestBodyBytes   int64          `json:"max_request_body_bytes"`
	LogDir                string         `json:"log_dir,omitempty"`
	LogRetentionDays      int            `json:"log_retention_days"`
	LogStdout             *bool          `json:"log_stdout,omitempty"`
	Notifications         Notifications  `json:"notifications"`
	CircuitBreaker        CircuitBreaker `json:"circuit_breaker"`
	Routing               Routing        `json:"routing"`
}

type Notifications struct {
	Enabled        bool   `json:"enabled"`
	MinLevel       string `json:"min_level"`
	ProviderSwitch *bool  `json:"provider_switch,omitempty"`
}

type CircuitBreaker struct {
	FailureThreshold    int    `json:"failure_threshold"`
	SuccessThreshold    int    `json:"success_threshold"`
	OpenTimeout         string `json:"open_timeout"`
	HalfOpenMaxInFlight int    `json:"half_open_max_inflight"`
}

type Routing struct {
	StickySessions   StickySessions   `json:"sticky_sessions"`
	BusyBackpressure BusyBackpressure `json:"busy_backpressure"`
}

type StickySessions struct {
	Enabled                bool   `json:"enabled"`
	ExplicitTTL            string `json:"explicit_ttl"`
	CacheHintTTL           string `json:"cache_hint_ttl"`
	DynamicFeatureTTL      string `json:"dynamic_feature_ttl"`
	DynamicFeatureCapacity int    `json:"dynamic_feature_capacity"`
	ResponseLookupTTL      string `json:"response_lookup_ttl"`
}

type BusyBackpressure struct {
	Enabled            bool     `json:"enabled"`
	RetryDelays        []string `json:"retry_delays"`
	ProbeMaxInFlight   int      `json:"probe_max_inflight"`
	ShortRetryAfterMax string   `json:"short_retry_after_max"`
	MaxInlineWait      string   `json:"max_inline_wait"`
}

type Client struct {
	Mode           string     `json:"mode"`
	PinnedProvider string     `json:"pinned_provider,omitempty"`
	Providers      []Provider `json:"providers"`
}

type Provider struct {
	Name          string             `json:"name"`
	BaseURL       string             `json:"base_url,omitempty"`
	APIKeys       []string           `json:"api_keys,omitempty"`
	AuthType      string             `json:"auth_type"`
	OAuthProvider string             `json:"oauth_provider,omitempty"`
	OAuthRef      string             `json:"oauth_ref,omitempty"`
	OAuthIdentity string             `json:"oauth_identity,omitempty"`
	ProxyMode     string             `json:"proxy_mode,omitempty"`
	ProxyURL      string             `json:"proxy_url,omitempty"`
	Priority      int                `json:"priority"`
	Enabled       *bool              `json:"enabled,omitempty"`
	Overrides     *ProviderOverrides `json:"overrides,omitempty"`
}

type ProviderOverrides struct {
	Model  *string          `json:"model,omitempty"`
	OpenAI *OpenAIOverrides `json:"openai,omitempty"`
	Claude *ClaudeOverrides `json:"claude,omitempty"`
}

type OpenAIOverrides struct {
	ReasoningEffort *string `json:"reasoning_effort,omitempty"`
}

type ClaudeOverrides struct {
	ThinkingBudgetTokens *int    `json:"thinking_budget_tokens,omitempty"`
	Effort               *string `json:"effort,omitempty"`
}

type Credential struct {
	Ref          string            `json:"ref"`
	Provider     string            `json:"provider"`
	Email        string            `json:"email,omitempty"`
	AccountID    string            `json:"account_id,omitempty"`
	AccessToken  string            `json:"access_token"`
	RefreshToken string            `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time         `json:"expires_at,omitempty"`
	LastRefresh  time.Time         `json:"last_refresh,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

type Usage struct {
	Clients map[string]map[string]ProviderUsage `json:"clients"`
}

type ProviderUsage struct {
	RequestCount    int64                      `json:"request_count,omitempty"`
	SuccessCount    int64                      `json:"success_count,omitempty"`
	InputTokens     int64                      `json:"input_tokens,omitempty"`
	OutputTokens    int64                      `json:"output_tokens,omitempty"`
	TotalTokens     int64                      `json:"total_tokens,omitempty"`
	ReasoningTokens int64                      `json:"reasoning_tokens,omitempty"`
	ThoughtsTokens  int64                      `json:"thoughts_tokens,omitempty"`
	TotalCostMicros int64                      `json:"total_cost_micros,omitempty"`
	LastUsedAt      time.Time                  `json:"last_used_at,omitempty"`
	Usage           map[string]any             `json:"usage,omitempty"`
	DailyCosts      map[string]DailyCostBucket `json:"daily_costs,omitempty"`
	HasCost         bool                       `json:"has_cost,omitempty"`
}

type DailyCostBucket struct {
	CostMicros int64 `json:"cost_micros,omitempty"`
	HasCost    bool  `json:"has_cost,omitempty"`
}

type Mode string

const (
	ModeReplace Mode = "replace"
	ModeMerge   Mode = "merge"
)

type Input struct {
	Name string
	Data []byte
}

type Confidence int

const (
	ConfidenceNone Confidence = iota
	ConfidencePossible
	ConfidenceCertain
)

type DecodeOptions struct{}
type EncodeOptions struct{}

type Report struct {
	Format   string   `json:"format"`
	Warnings []string `json:"warnings,omitempty"`
	Errors   []string `json:"errors,omitempty"`
}

type ImportPlan struct {
	ID             string   `json:"id"`
	Format         string   `json:"format"`
	Mode           Mode     `json:"mode"`
	Native         bool     `json:"native"`
	Files          int      `json:"files"`
	Clients        int      `json:"clients"`
	Providers      int      `json:"providers"`
	Credentials    int      `json:"credentials"`
	UsageProviders int      `json:"usage_providers"`
	Warnings       []string `json:"warnings,omitempty"`
	Dataset        *Dataset `json:"-"`
}

type ApplyResult struct {
	PlanID            string                  `json:"plan_id"`
	Mode              Mode                    `json:"mode"`
	Providers         int                     `json:"providers"`
	Credentials       int                     `json:"credentials"`
	UsageProviders    int                     `json:"usage_providers"`
	CredentialResults []CredentialApplyResult `json:"credential_results,omitempty"`
}

type CredentialApplyResult struct {
	Provider       string `json:"provider"`
	Ref            string `json:"ref"`
	Email          string `json:"email,omitempty"`
	ProviderName   string `json:"provider_name,omitempty"`
	ProviderAction string `json:"provider_action,omitempty"`
}
