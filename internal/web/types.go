package web

// Keep request/response DTOs in the web package so we don't leak internal config
// structs directly over HTTP. This prevents accidental JSON field-name mismatches
// and lets us redact sensitive fields like API keys.

import (
	"net/url"
	"strings"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
	integrationpkg "github.com/lansespirit/Clipal/internal/integration"
	"github.com/lansespirit/Clipal/internal/telemetry"
)

// GlobalConfigRequest represents a request to update global configuration
type GlobalConfigRequest struct {
	ListenAddr            string                      `json:"listen_addr"`
	AllowRemoteProxy      bool                        `json:"allow_remote_proxy"`
	AllowRemoteWebUI      bool                        `json:"allow_remote_web_ui"`
	Port                  int                         `json:"port"`
	LogLevel              string                      `json:"log_level"`
	ReactivateAfter       string                      `json:"reactivate_after"`
	UpstreamIdleTimeout   string                      `json:"upstream_idle_timeout"`
	ResponseHeaderTimeout string                      `json:"response_header_timeout"`
	UpstreamProxyMode     *string                     `json:"upstream_proxy_mode,omitempty"`
	UpstreamProxyURL      *string                     `json:"upstream_proxy_url,omitempty"`
	MaxRequestBodyBytes   int64                       `json:"max_request_body_bytes"`
	LogDir                string                      `json:"log_dir"`
	LogRetentionDays      int                         `json:"log_retention_days"`
	LogStdout             *bool                       `json:"log_stdout"`
	Notifications         NotificationsConfigRequest  `json:"notifications"`
	CircuitBreaker        CircuitBreakerConfigRequest `json:"circuit_breaker"`
	Routing               RoutingConfigRequest        `json:"routing"`
}

type NotificationsConfigRequest struct {
	Enabled        bool   `json:"enabled"`
	MinLevel       string `json:"min_level"`
	ProviderSwitch *bool  `json:"provider_switch"`
}

type CircuitBreakerConfigRequest struct {
	FailureThreshold    int    `json:"failure_threshold"`
	SuccessThreshold    int    `json:"success_threshold"`
	OpenTimeout         string `json:"open_timeout"`
	HalfOpenMaxInFlight int    `json:"half_open_max_inflight"`
}

type RoutingConfigRequest struct {
	StickySessions   StickySessionsConfigRequest   `json:"sticky_sessions"`
	BusyBackpressure BusyBackpressureConfigRequest `json:"busy_backpressure"`
}

type StickySessionsConfigRequest struct {
	Enabled     *bool   `json:"enabled,omitempty"`
	ExplicitTTL *string `json:"explicit_ttl,omitempty"`
}

type BusyBackpressureConfigRequest struct {
	Enabled            *bool   `json:"enabled,omitempty"`
	ShortRetryAfterMax *string `json:"short_retry_after_max,omitempty"`
	MaxInlineWait      *string `json:"max_inline_wait,omitempty"`
}

// GlobalConfigResponse represents the global configuration returned to the UI.
type GlobalConfigResponse struct {
	ListenAddr            string                       `json:"listen_addr"`
	AllowRemoteProxy      bool                         `json:"allow_remote_proxy"`
	AllowRemoteWebUI      bool                         `json:"allow_remote_web_ui"`
	Port                  int                          `json:"port"`
	LogLevel              string                       `json:"log_level"`
	ReactivateAfter       string                       `json:"reactivate_after"`
	UpstreamIdleTimeout   string                       `json:"upstream_idle_timeout"`
	ResponseHeaderTimeout string                       `json:"response_header_timeout"`
	UpstreamProxyMode     string                       `json:"upstream_proxy_mode"`
	UpstreamProxyURL      string                       `json:"upstream_proxy_url"`
	MaxRequestBodyBytes   int64                        `json:"max_request_body_bytes"`
	LogDir                string                       `json:"log_dir"`
	LogRetentionDays      int                          `json:"log_retention_days"`
	LogStdout             bool                         `json:"log_stdout"`
	Notifications         NotificationsConfigResponse  `json:"notifications"`
	CircuitBreaker        CircuitBreakerConfigResponse `json:"circuit_breaker"`
	Routing               RoutingConfigResponse        `json:"routing"`
}

type NotificationsConfigResponse struct {
	Enabled        bool   `json:"enabled"`
	MinLevel       string `json:"min_level"`
	ProviderSwitch bool   `json:"provider_switch"`
}

type CircuitBreakerConfigResponse struct {
	FailureThreshold    int    `json:"failure_threshold"`
	SuccessThreshold    int    `json:"success_threshold"`
	OpenTimeout         string `json:"open_timeout"`
	HalfOpenMaxInFlight int    `json:"half_open_max_inflight"`
}

type RoutingConfigResponse struct {
	StickySessions   StickySessionsConfigResponse   `json:"sticky_sessions"`
	BusyBackpressure BusyBackpressureConfigResponse `json:"busy_backpressure"`
}

type StickySessionsConfigResponse struct {
	Enabled     bool   `json:"enabled"`
	ExplicitTTL string `json:"explicit_ttl"`
}

type BusyBackpressureConfigResponse struct {
	Enabled            bool   `json:"enabled"`
	ShortRetryAfterMax string `json:"short_retry_after_max"`
	MaxInlineWait      string `json:"max_inline_wait"`
}

type ClientConfigRequest struct {
	Mode           string `json:"mode"`
	PinnedProvider string `json:"pinned_provider"`
}

type ClientConfigResponse struct {
	Mode            string                  `json:"mode"`
	PinnedProvider  string                  `json:"pinned_provider"`
	OverrideSupport ProviderOverrideSupport `json:"override_support"`
}

type ProviderOverrideSupport struct {
	Model  bool                          `json:"model"`
	OpenAI OpenAIProviderOverrideSupport `json:"openai"`
	Claude ClaudeProviderOverrideSupport `json:"claude"`
}

type OpenAIProviderOverrideSupport struct {
	ReasoningEffort bool `json:"reasoning_effort"`
}

type ClaudeProviderOverrideSupport struct {
	ThinkingBudgetTokens bool `json:"thinking_budget_tokens"`
	Effort               bool `json:"effort"`
}

type ProviderOverridesRequest struct {
	Model  *string                         `json:"model,omitempty"`
	OpenAI *OpenAIProviderOverridesRequest `json:"openai,omitempty"`
	Claude *ClaudeProviderOverridesRequest `json:"claude,omitempty"`
}

type OpenAIProviderOverridesRequest struct {
	ReasoningEffort *string `json:"reasoning_effort,omitempty"`
}

type ClaudeProviderOverridesRequest struct {
	ThinkingBudgetTokens *int    `json:"thinking_budget_tokens,omitempty"`
	Effort               *string `json:"effort,omitempty"`
}

type ProviderOverridesResponse struct {
	Model  string                           `json:"model,omitempty"`
	OpenAI *OpenAIProviderOverridesResponse `json:"openai,omitempty"`
	Claude *ClaudeProviderOverridesResponse `json:"claude,omitempty"`
}

type OpenAIProviderOverridesResponse struct {
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

type ClaudeProviderOverridesResponse struct {
	ThinkingBudgetTokens int    `json:"thinking_budget_tokens,omitempty"`
	Effort               string `json:"effort,omitempty"`
}

// ProviderRequest represents a request to create or update a provider
type ProviderRequest struct {
	Name          string                    `json:"name"`
	BaseURL       string                    `json:"base_url"`
	APIKey        string                    `json:"api_key,omitempty"`
	APIKeys       []string                  `json:"api_keys,omitempty"`
	AuthType      config.ProviderAuthType   `json:"auth_type,omitempty"`
	OAuthProvider config.OAuthProvider      `json:"oauth_provider,omitempty"`
	OAuthRef      string                    `json:"oauth_ref,omitempty"`
	ProxyMode     *string                   `json:"proxy_mode,omitempty"`
	ProxyURL      *string                   `json:"proxy_url,omitempty"`
	Overrides     *ProviderOverridesRequest `json:"overrides,omitempty"`
	// Priority is 1-based. Omit to keep existing value (on updates) or to
	// auto-assign the next priority (on create).
	Priority *int  `json:"priority,omitempty"`
	Enabled  *bool `json:"enabled,omitempty"`
}

// ProviderResponse is returned for provider listings (never includes api_key).
type ProviderResponse struct {
	Name             string                     `json:"name"`
	DisplayName      string                     `json:"display_name,omitempty"`
	BaseURL          string                     `json:"base_url"`
	AuthType         config.ProviderAuthType    `json:"auth_type"`
	OAuthProvider    config.OAuthProvider       `json:"oauth_provider,omitempty"`
	OAuthRef         string                     `json:"oauth_ref,omitempty"`
	OAuthAuthStatus  string                     `json:"oauth_auth_status,omitempty"`
	OAuthExpiresAt   string                     `json:"oauth_expires_at,omitempty"`
	OAuthLastRefresh string                     `json:"oauth_last_refresh,omitempty"`
	OAuthPlanType    string                     `json:"oauth_plan_type,omitempty"`
	OAuthRateLimits  *ProviderOAuthLimits       `json:"oauth_rate_limits,omitempty"`
	ProxyMode        string                     `json:"proxy_mode"`
	ProxyURLHint     string                     `json:"proxy_url_hint,omitempty"`
	Priority         int                        `json:"priority"`
	Enabled          bool                       `json:"enabled"`
	KeyCount         int                        `json:"key_count"`
	Usage            *ProviderUsageResponse     `json:"usage,omitempty"`
	Overrides        *ProviderOverridesResponse `json:"overrides,omitempty"`
}

type ProviderUsageResponse struct {
	RequestCount     int64                         `json:"request_count,omitempty"`
	SuccessCount     int64                         `json:"success_count,omitempty"`
	InputTokens      int64                         `json:"input_tokens,omitempty"`
	OutputTokens     int64                         `json:"output_tokens,omitempty"`
	TotalTokens      int64                         `json:"total_tokens,omitempty"`
	ReasoningTokens  int64                         `json:"reasoning_tokens,omitempty"`
	ThoughtsTokens   int64                         `json:"thoughts_tokens,omitempty"`
	UsageBreakdowns  []ProviderUsageBreakdownEntry `json:"usage_breakdowns,omitempty"`
	SpendTodayMicros int64                         `json:"spend_today_micros,omitempty"`
	SpendWeekMicros  int64                         `json:"spend_week_micros,omitempty"`
	LastUsedAt       string                        `json:"last_used_at,omitempty"`
	HasUsage         bool                          `json:"has_usage,omitempty"`
	HasCost          bool                          `json:"has_cost,omitempty"`
}

type ProviderUsageBreakdownEntry struct {
	Kind   string `json:"kind,omitempty"`
	Parent string `json:"parent,omitempty"`
	Tokens int64  `json:"tokens,omitempty"`
}

type ProviderOAuthLimits struct {
	Primary    *ProviderOAuthLimitWindow      `json:"primary,omitempty"`
	Secondary  *ProviderOAuthLimitWindow      `json:"secondary,omitempty"`
	Additional []ProviderOAuthAdditionalLimit `json:"additional,omitempty"`
}

type ProviderOAuthAdditionalLimit struct {
	LimitID   string                    `json:"limit_id,omitempty"`
	LimitName string                    `json:"limit_name,omitempty"`
	Primary   *ProviderOAuthLimitWindow `json:"primary,omitempty"`
	Secondary *ProviderOAuthLimitWindow `json:"secondary,omitempty"`
}

type ProviderOAuthLimitWindow struct {
	UsedPercent   float64 `json:"used_percent,omitempty"`
	WindowMinutes int     `json:"window_minutes,omitempty"`
	ResetsAt      string  `json:"resets_at,omitempty"`
}

type ProviderOAuthMetadataResponse struct {
	OAuthPlanType   string               `json:"oauth_plan_type,omitempty"`
	OAuthRateLimits *ProviderOAuthLimits `json:"oauth_rate_limits,omitempty"`
}

// ReorderRequest represents a request to reorder providers
type ReorderRequest struct {
	Providers []string `json:"providers"` // Array of provider names in desired order
}

type OAuthStartRequest struct {
	ClientType string               `json:"client_type"`
	Provider   config.OAuthProvider `json:"provider"`
	ProxyMode  *string              `json:"proxy_mode,omitempty"`
	ProxyURL   *string              `json:"proxy_url,omitempty"`
}

type OAuthProviderOptionResponse struct {
	Provider config.OAuthProvider `json:"provider"`
}

type OAuthStartResponse struct {
	SessionID string               `json:"session_id"`
	Provider  config.OAuthProvider `json:"provider"`
	AuthURL   string               `json:"auth_url"`
	ExpiresAt string               `json:"expires_at,omitempty"`
}

type OAuthSessionResponse struct {
	SessionID      string               `json:"session_id"`
	Provider       config.OAuthProvider `json:"provider"`
	Status         string               `json:"status"`
	AuthURL        string               `json:"auth_url,omitempty"`
	ExpiresAt      string               `json:"expires_at,omitempty"`
	CredentialRef  string               `json:"credential_ref,omitempty"`
	Email          string               `json:"email,omitempty"`
	ProviderName   string               `json:"provider_name,omitempty"`
	ProviderAction string               `json:"provider_action,omitempty"`
	DisplayName    string               `json:"display_name,omitempty"`
	Error          string               `json:"error,omitempty"`
}

type OAuthSessionCodeRequest struct {
	Code       string `json:"code"`
	ClientType string `json:"client_type,omitempty"`
}

type OAuthSessionLinkRequest struct {
	ClientType string `json:"client_type,omitempty"`
}

type OAuthAccountResponse struct {
	Provider        config.OAuthProvider `json:"provider"`
	Ref             string               `json:"ref"`
	Email           string               `json:"email,omitempty"`
	ExpiresAt       string               `json:"expires_at,omitempty"`
	LastRefresh     string               `json:"last_refresh,omitempty"`
	LinkedProviders []string             `json:"linked_providers,omitempty"`
}

type OAuthImportResponse struct {
	ClientType    string                          `json:"client_type"`
	Provider      config.OAuthProvider            `json:"provider"`
	ImportedCount int                             `json:"imported_count"`
	LinkedCount   int                             `json:"linked_count"`
	SkippedCount  int                             `json:"skipped_count"`
	FailedCount   int                             `json:"failed_count"`
	Message       string                          `json:"message,omitempty"`
	Results       []OAuthImportFileResultResponse `json:"results,omitempty"`
}

type OAuthImportFileResultResponse struct {
	File           string               `json:"file"`
	Status         string               `json:"status"`
	Provider       config.OAuthProvider `json:"provider,omitempty"`
	Ref            string               `json:"ref,omitempty"`
	Email          string               `json:"email,omitempty"`
	ProviderName   string               `json:"provider_name,omitempty"`
	ProviderAction string               `json:"provider_action,omitempty"`
	Message        string               `json:"message,omitempty"`
}

// StatusResponse represents the system status
type StatusResponse struct {
	Version   string                  `json:"version"`
	Uptime    string                  `json:"uptime"`
	ConfigDir string                  `json:"config_dir"`
	Clients   map[string]ClientStatus `json:"clients"`
}

// ClientStatus represents the status of a client proxy
type ClientStatus struct {
	Mode           string `json:"mode"`
	PinnedProvider string `json:"pinned_provider,omitempty"`

	ProviderCount    int               `json:"provider_count"`
	EnabledProviders []string          `json:"enabled_providers"`
	CurrentProvider  string            `json:"current_provider"`
	CurrentProviders map[string]string `json:"current_providers,omitempty"`

	LastSwitch  *ProviderSwitchStatus `json:"last_switch,omitempty"`
	LastRequest *RequestOutcomeStatus `json:"last_request,omitempty"`
	Providers   []ProviderStatus      `json:"providers,omitempty"`
}

type ProviderSwitchStatus struct {
	At     string `json:"at"`
	From   string `json:"from"`
	To     string `json:"to"`
	Reason string `json:"reason"`
	Status int    `json:"status"`
	Label  string `json:"label,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type ProviderStatus struct {
	Name              string `json:"name"`
	Priority          int    `json:"priority"`
	Enabled           bool   `json:"enabled"`
	KeyCount          int    `json:"key_count"`
	AvailableKeyCount int    `json:"available_key_count,omitempty"`
	State             string `json:"state,omitempty"`
	Label             string `json:"label,omitempty"`
	Detail            string `json:"detail,omitempty"`

	SkipReason string `json:"skip_reason,omitempty"` // disabled | deactivated | circuit_open

	DeactivatedReason  string `json:"deactivated_reason,omitempty"`
	DeactivatedMessage string `json:"deactivated_message,omitempty"`
	DeactivatedIn      string `json:"deactivated_in,omitempty"`

	CircuitState  string `json:"circuit_state,omitempty"` // closed | open | half_open
	CircuitOpenIn string `json:"circuit_open_in,omitempty"`
}

type RequestOutcomeStatus struct {
	At         string `json:"at"`
	Provider   string `json:"provider"`
	Status     int    `json:"status"`
	Delivery   string `json:"delivery"`
	Protocol   string `json:"protocol"`
	Capability string `json:"capability,omitempty"`
	Cause      string `json:"cause,omitempty"`
	Bytes      int    `json:"bytes"`
	Result     string `json:"result,omitempty"`
	Label      string `json:"label,omitempty"`
	Detail     string `json:"detail,omitempty"`
}

// ErrorResponse represents an error response
type ErrorResponse struct {
	Error  string `json:"error"`
	Status int    `json:"status"`
	Reason string `json:"reason"`
}

// SuccessResponse represents a success response
type SuccessResponse struct {
	Message string `json:"message"`
}

// ServiceActionRequest triggers an OS service action.
// Most fields are optional and only apply on some platforms.
type ServiceActionRequest struct {
	Force      bool   `json:"force"`
	StdoutPath string `json:"stdout_path,omitempty"` // macOS: launchd StandardOutPath
	StderrPath string `json:"stderr_path,omitempty"` // macOS: launchd StandardErrorPath
}

// ServiceStatusResponse reports best-effort status for "clipal service status".
// Output and Error (if any) are intended for display/debugging.
type ServiceStatusResponse struct {
	OS        string `json:"os"`
	Supported bool   `json:"supported"`
	Installed bool   `json:"installed"`
	Loaded    bool   `json:"loaded"`
	Running   bool   `json:"running"`
	OK        bool   `json:"ok"`
	Detail    string `json:"detail,omitempty"`
	Output    string `json:"output,omitempty"`
	Error     string `json:"error,omitempty"`

	// UI helpers (e.g. Windows may require elevated install in some environments).
	InstallCommand string `json:"install_command,omitempty"`
	InstallHint    string `json:"install_hint,omitempty"`
}

type IntegrationResponse struct {
	Product             string `json:"product"`
	Name                string `json:"name"`
	State               string `json:"state"`
	TargetPath          string `json:"target_path"`
	BackupAvailable     bool   `json:"backup_available"`
	Warning             string `json:"warning,omitempty"`
	CurrentContent      string `json:"current_content,omitempty"`
	PlannedContent      string `json:"planned_content,omitempty"`
	BackupContent       string `json:"backup_content,omitempty"`
	BackupTargetExisted bool   `json:"backup_target_existed"`
}

type IntegrationActionResponse struct {
	Message string              `json:"message"`
	Product string              `json:"product"`
	Status  IntegrationResponse `json:"status"`
}

func boolPtrOrTrue(v *bool) bool {
	if v == nil {
		return true
	}
	return *v
}

func toGlobalConfigResponse(gc config.GlobalConfig) GlobalConfigResponse {
	return GlobalConfigResponse{
		ListenAddr:            gc.ListenAddr,
		AllowRemoteProxy:      gc.AllowRemoteProxy,
		AllowRemoteWebUI:      gc.AllowRemoteWebUI,
		Port:                  gc.Port,
		LogLevel:              string(gc.LogLevel),
		ReactivateAfter:       gc.ReactivateAfter,
		UpstreamIdleTimeout:   gc.UpstreamIdleTimeout,
		ResponseHeaderTimeout: gc.ResponseHeaderTimeout,
		UpstreamProxyMode:     string(gc.NormalizedUpstreamProxyMode()),
		UpstreamProxyURL:      gc.NormalizedUpstreamProxyURL(),
		MaxRequestBodyBytes:   gc.MaxRequestBody,
		LogDir:                gc.LogDir,
		LogRetentionDays:      gc.LogRetentionDays,
		LogStdout:             boolPtrOrTrue(gc.LogStdout),
		Notifications: NotificationsConfigResponse{
			Enabled:        gc.Notifications.Enabled,
			MinLevel:       string(gc.Notifications.MinLevel),
			ProviderSwitch: boolPtrOrTrue(gc.Notifications.ProviderSwitch),
		},
		CircuitBreaker: CircuitBreakerConfigResponse{
			FailureThreshold:    gc.CircuitBreaker.FailureThreshold,
			SuccessThreshold:    gc.CircuitBreaker.SuccessThreshold,
			OpenTimeout:         gc.CircuitBreaker.OpenTimeout,
			HalfOpenMaxInFlight: gc.CircuitBreaker.HalfOpenMaxInFlight,
		},
		Routing: RoutingConfigResponse{
			StickySessions: StickySessionsConfigResponse{
				Enabled:     gc.Routing.StickySessions.Enabled,
				ExplicitTTL: gc.Routing.StickySessions.ExplicitTTL,
			},
			BusyBackpressure: BusyBackpressureConfigResponse{
				Enabled:            gc.Routing.BusyBackpressure.Enabled,
				ShortRetryAfterMax: gc.Routing.BusyBackpressure.ShortRetryAfterMax,
				MaxInlineWait:      gc.Routing.BusyBackpressure.MaxInlineWait,
			},
		},
	}
}

func mapProviderOverridesResponse(p config.Provider) *ProviderOverridesResponse {
	model := p.ModelOverride()
	reasoning := p.OpenAIReasoningEffort()
	thinking := p.ClaudeThinkingBudgetTokens()
	claudeEffort := p.ClaudeEffort()

	if model == "" && reasoning == "" && thinking == 0 && claudeEffort == "" {
		return nil
	}

	resp := &ProviderOverridesResponse{
		Model: model,
	}
	if reasoning != "" {
		resp.OpenAI = &OpenAIProviderOverridesResponse{
			ReasoningEffort: reasoning,
		}
	}
	if thinking > 0 || claudeEffort != "" {
		resp.Claude = &ClaudeProviderOverridesResponse{
			ThinkingBudgetTokens: thinking,
			Effort:               claudeEffort,
		}
	}
	return resp
}

func toProviderResponses(providers []config.Provider, usageByProvider map[string]telemetry.ProviderUsage) []ProviderResponse {
	out := make([]ProviderResponse, 0, len(providers))
	for _, p := range providers {
		out = append(out, ProviderResponse{
			Name:          p.Name,
			BaseURL:       p.BaseURL,
			AuthType:      p.NormalizedAuthType(),
			OAuthProvider: p.NormalizedOAuthProvider(),
			OAuthRef:      p.NormalizedOAuthRef(),
			ProxyMode:     string(p.NormalizedProxyMode()),
			ProxyURLHint:  proxyURLHint(p.NormalizedProxyURL()),
			Priority:      p.Priority,
			Enabled:       p.IsEnabled(),
			KeyCount:      p.KeyCount(),
			Usage:         mapProviderUsageResponse(usageByProvider[p.Name]),
			Overrides:     mapProviderOverridesResponse(p),
		})
	}
	return out
}

func mapProviderUsageResponse(usage telemetry.ProviderUsage) *ProviderUsageResponse {
	if usage.RequestCount == 0 &&
		usage.SuccessCount == 0 &&
		usage.InputTokens == 0 &&
		usage.OutputTokens == 0 &&
		usage.TotalTokens == 0 &&
		usage.ReasoningTokens == 0 &&
		usage.ThoughtsTokens == 0 &&
		usage.TotalCostMicros == 0 &&
		!usage.HasCost &&
		usage.LastUsedAt.IsZero() {
		return nil
	}
	now := time.Now()
	if !usage.LastUsedAt.IsZero() {
		now = now.In(usage.LastUsedAt.Location())
	}
	spendTodayMicros := providerSpendForRecentDays(usage, now, 1)
	spendWeekMicros := providerSpendForRecentDays(usage, now, 7)
	resp := &ProviderUsageResponse{
		RequestCount:     usage.RequestCount,
		SuccessCount:     usage.SuccessCount,
		InputTokens:      usage.InputTokens,
		OutputTokens:     usage.OutputTokens,
		TotalTokens:      usage.TotalTokens,
		ReasoningTokens:  usage.ReasoningTokens,
		ThoughtsTokens:   usage.ThoughtsTokens,
		SpendTodayMicros: spendTodayMicros,
		SpendWeekMicros:  spendWeekMicros,
		HasUsage:         usage.Usage != nil,
		HasCost:          usage.HasCost,
	}
	if usage.ReasoningTokens > 0 {
		resp.UsageBreakdowns = append(resp.UsageBreakdowns, ProviderUsageBreakdownEntry{
			Kind:   "reasoning",
			Parent: "output",
			Tokens: usage.ReasoningTokens,
		})
	}
	if usage.ThoughtsTokens > 0 {
		resp.UsageBreakdowns = append(resp.UsageBreakdowns, ProviderUsageBreakdownEntry{
			Kind:   "thoughts",
			Parent: "total",
			Tokens: usage.ThoughtsTokens,
		})
	}
	if !usage.LastUsedAt.IsZero() {
		resp.LastUsedAt = usage.LastUsedAt.Format(time.RFC3339)
	}
	return resp
}

func providerSpendForRecentDays(usage telemetry.ProviderUsage, now time.Time, days int) int64 {
	if days <= 0 || len(usage.DailyCosts) == 0 {
		return 0
	}
	var total int64
	for offset := 0; offset < days; offset++ {
		key := now.AddDate(0, 0, -offset).Format("2006-01-02")
		bucket, ok := usage.DailyCosts[key]
		if !ok || !bucket.HasCost {
			continue
		}
		total += bucket.CostMicros
	}
	return total
}

func proxyURLHint(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func toProviderOverrideSupport(s providerOverrideSupport) ProviderOverrideSupport {
	return ProviderOverrideSupport{
		Model: s.Model,
		OpenAI: OpenAIProviderOverrideSupport{
			ReasoningEffort: s.OpenAI.ReasoningEffort,
		},
		Claude: ClaudeProviderOverrideSupport{
			ThinkingBudgetTokens: s.Claude.ThinkingBudgetTokens,
			Effort:               s.Claude.Effort,
		},
	}
}

func toIntegrationResponse(product integrationpkg.ProductID, status integrationpkg.Status, preview integrationpkg.Preview) IntegrationResponse {
	return IntegrationResponse{
		Product:             string(product),
		Name:                integrationpkg.ProductName(product),
		State:               string(status.State),
		TargetPath:          status.TargetPath,
		BackupAvailable:     status.BackupAvailable,
		Warning:             status.Warning,
		CurrentContent:      preview.CurrentContent,
		PlannedContent:      preview.PlannedContent,
		BackupContent:       preview.BackupContent,
		BackupTargetExisted: preview.BackupTargetExisted,
	}
}
