package transfer

import (
	"strings"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
	"github.com/lansespirit/Clipal/internal/oauth"
	"github.com/lansespirit/Clipal/internal/telemetry"
)

func emptyDataset() *Dataset {
	return &Dataset{
		Schema:        SchemaName,
		SchemaVersion: SchemaVersion,
		ExportedAt:    time.Now().UTC(),
		Producer:      Producer{Name: "clipal", Version: "external-import"},
		Data: Data{
			Clients: map[string]Client{
				"claude": {Mode: "auto", Providers: []Provider{}},
				"openai": {Mode: "auto", Providers: []Provider{}},
				"gemini": {Mode: "auto", Providers: []Provider{}},
			},
			Credentials: []Credential{},
			Usage:       Usage{Clients: map[string]map[string]ProviderUsage{}},
		},
	}
}

func datasetFromState(cfg *config.Config, credentials []oauth.Credential, usage telemetry.DataSnapshot, version string, now time.Time) *Dataset {
	dataset := emptyDataset()
	dataset.ExportedAt = now.UTC()
	dataset.Producer.Version = version
	dataset.Data.Global = globalFromConfig(cfg.Global)
	dataset.Data.Clients["claude"] = clientFromConfig(cfg.Claude)
	dataset.Data.Clients["openai"] = clientFromConfig(cfg.OpenAI)
	dataset.Data.Clients["gemini"] = clientFromConfig(cfg.Gemini)
	dataset.Data.Credentials = make([]Credential, 0, len(credentials))
	for _, credential := range credentials {
		dataset.Data.Credentials = append(dataset.Data.Credentials, credentialFromOAuth(credential))
	}
	dataset.Data.Usage = usageFromTelemetry(usage)
	return dataset
}

func globalFromConfig(in config.GlobalConfig) Global {
	return Global{
		ListenAddr: in.ListenAddr, Port: in.Port, LogLevel: string(in.LogLevel),
		ReactivateAfter: in.ReactivateAfter, UpstreamIdleTimeout: in.UpstreamIdleTimeout,
		ResponseHeaderTimeout: in.ResponseHeaderTimeout, UpstreamProxyMode: string(in.NormalizedUpstreamProxyMode()),
		UpstreamProxyURL: in.NormalizedUpstreamProxyURL(), MaxRequestBodyBytes: in.MaxRequestBody,
		LogDir: in.LogDir, LogRetentionDays: in.LogRetentionDays, LogStdout: in.LogStdout,
		Notifications:  Notifications{Enabled: in.Notifications.Enabled, MinLevel: string(in.Notifications.MinLevel), ProviderSwitch: in.Notifications.ProviderSwitch},
		CircuitBreaker: CircuitBreaker{FailureThreshold: in.CircuitBreaker.FailureThreshold, SuccessThreshold: in.CircuitBreaker.SuccessThreshold, OpenTimeout: in.CircuitBreaker.OpenTimeout, HalfOpenMaxInFlight: in.CircuitBreaker.HalfOpenMaxInFlight},
		Routing: Routing{
			StickySessions:   StickySessions{Enabled: in.Routing.StickySessions.Enabled, ExplicitTTL: in.Routing.StickySessions.ExplicitTTL, CacheHintTTL: in.Routing.StickySessions.CacheHintTTL, DynamicFeatureTTL: in.Routing.StickySessions.DynamicFeatureTTL, DynamicFeatureCapacity: in.Routing.StickySessions.DynamicFeatureCapacity, ResponseLookupTTL: in.Routing.StickySessions.ResponseLookupTTL},
			BusyBackpressure: BusyBackpressure{Enabled: in.Routing.BusyBackpressure.Enabled, RetryDelays: append([]string(nil), in.Routing.BusyBackpressure.RetryDelays...), ProbeMaxInFlight: in.Routing.BusyBackpressure.ProbeMaxInFlight, ShortRetryAfterMax: in.Routing.BusyBackpressure.ShortRetryAfterMax, MaxInlineWait: in.Routing.BusyBackpressure.MaxInlineWait},
		},
	}
}

func (in Global) toConfig() config.GlobalConfig {
	return config.GlobalConfig{
		ListenAddr: in.ListenAddr, Port: in.Port, LogLevel: config.LogLevel(in.LogLevel), ReactivateAfter: in.ReactivateAfter,
		UpstreamIdleTimeout: in.UpstreamIdleTimeout, ResponseHeaderTimeout: in.ResponseHeaderTimeout,
		UpstreamProxyMode: config.GlobalUpstreamProxyMode(in.UpstreamProxyMode), UpstreamProxyURL: in.UpstreamProxyURL,
		MaxRequestBody: in.MaxRequestBodyBytes, LogDir: in.LogDir, LogRetentionDays: in.LogRetentionDays, LogStdout: in.LogStdout,
		Notifications:  config.NotificationsConfig{Enabled: in.Notifications.Enabled, MinLevel: config.LogLevel(in.Notifications.MinLevel), ProviderSwitch: in.Notifications.ProviderSwitch},
		CircuitBreaker: config.CircuitBreakerConfig{FailureThreshold: in.CircuitBreaker.FailureThreshold, SuccessThreshold: in.CircuitBreaker.SuccessThreshold, OpenTimeout: in.CircuitBreaker.OpenTimeout, HalfOpenMaxInFlight: in.CircuitBreaker.HalfOpenMaxInFlight},
		Routing: config.RoutingConfig{
			StickySessions:   config.StickySessionsConfig{Enabled: in.Routing.StickySessions.Enabled, ExplicitTTL: in.Routing.StickySessions.ExplicitTTL, CacheHintTTL: in.Routing.StickySessions.CacheHintTTL, DynamicFeatureTTL: in.Routing.StickySessions.DynamicFeatureTTL, DynamicFeatureCapacity: in.Routing.StickySessions.DynamicFeatureCapacity, ResponseLookupTTL: in.Routing.StickySessions.ResponseLookupTTL},
			BusyBackpressure: config.BusyBackpressureConfig{Enabled: in.Routing.BusyBackpressure.Enabled, RetryDelays: append([]string(nil), in.Routing.BusyBackpressure.RetryDelays...), ProbeMaxInFlight: in.Routing.BusyBackpressure.ProbeMaxInFlight, ShortRetryAfterMax: in.Routing.BusyBackpressure.ShortRetryAfterMax, MaxInlineWait: in.Routing.BusyBackpressure.MaxInlineWait},
		},
	}
}

func clientFromConfig(in config.ClientConfig) Client {
	out := Client{Mode: string(in.Mode), PinnedProvider: in.PinnedProvider, Providers: make([]Provider, 0, len(in.Providers))}
	for _, p := range in.Providers {
		out.Providers = append(out.Providers, providerFromConfig(p))
	}
	return out
}

func (in Client) toConfig() config.ClientConfig {
	out := config.ClientConfig{Mode: config.ClientMode(in.Mode), PinnedProvider: in.PinnedProvider, Providers: make([]config.Provider, 0, len(in.Providers))}
	for _, p := range in.Providers {
		out.Providers = append(out.Providers, p.toConfig())
	}
	return out
}

func providerFromConfig(in config.Provider) Provider {
	out := Provider{Name: in.Name, BaseURL: in.BaseURL, APIKeys: in.NormalizedAPIKeys(), AuthType: string(in.NormalizedAuthType()), OAuthProvider: string(in.NormalizedOAuthProvider()), OAuthRef: in.NormalizedOAuthRef(), OAuthIdentity: in.NormalizedOAuthIdentity(), ProxyMode: string(in.NormalizedProxyMode()), ProxyURL: in.NormalizedProxyURL(), Priority: in.Priority, Enabled: in.Enabled}
	if o := config.NormalizeProviderOverrides(in.Overrides); o != nil {
		out.Overrides = &ProviderOverrides{Model: o.Model}
		if o.OpenAI != nil {
			out.Overrides.OpenAI = &OpenAIOverrides{ReasoningEffort: o.OpenAI.ReasoningEffort}
		}
		if o.Claude != nil {
			out.Overrides.Claude = &ClaudeOverrides{ThinkingBudgetTokens: o.Claude.ThinkingBudgetTokens, Effort: o.Claude.Effort}
		}
	}
	return out
}

func (in Provider) toConfig() config.Provider {
	out := config.Provider{Name: in.Name, BaseURL: in.BaseURL, APIKeys: append([]string(nil), in.APIKeys...), AuthType: config.ProviderAuthType(in.AuthType), OAuthProvider: config.OAuthProvider(in.OAuthProvider), OAuthRef: in.OAuthRef, OAuthIdentity: in.OAuthIdentity, ProxyMode: config.ProviderProxyMode(in.ProxyMode), ProxyURL: in.ProxyURL, Priority: in.Priority, Enabled: in.Enabled}
	if len(out.APIKeys) == 1 {
		out.APIKey, out.APIKeys = out.APIKeys[0], nil
	}
	if in.Overrides != nil {
		out.Overrides = &config.ProviderOverrides{Model: in.Overrides.Model}
		if in.Overrides.OpenAI != nil {
			out.Overrides.OpenAI = &config.OpenAIOverrides{ReasoningEffort: in.Overrides.OpenAI.ReasoningEffort}
		}
		if in.Overrides.Claude != nil {
			out.Overrides.Claude = &config.ClaudeOverrides{ThinkingBudgetTokens: in.Overrides.Claude.ThinkingBudgetTokens, Effort: in.Overrides.Claude.Effort}
		}
	}
	config.NormalizeProviderAuthSettings(&out)
	config.NormalizeProviderProxySettings(&out)
	return out
}

func credentialFromOAuth(in oauth.Credential) Credential {
	return Credential{Ref: in.Ref, Provider: string(in.Provider), Email: in.Email, AccountID: in.AccountID, AccessToken: in.AccessToken, RefreshToken: in.RefreshToken, ExpiresAt: in.ExpiresAt, LastRefresh: in.LastRefresh, Metadata: cloneStrings(in.Metadata)}
}

func (in Credential) toOAuth() oauth.Credential {
	return oauth.Credential{Ref: strings.TrimSpace(in.Ref), Provider: config.OAuthProvider(strings.ToLower(strings.TrimSpace(in.Provider))), Email: strings.TrimSpace(in.Email), AccountID: strings.TrimSpace(in.AccountID), AccessToken: strings.TrimSpace(in.AccessToken), RefreshToken: strings.TrimSpace(in.RefreshToken), ExpiresAt: in.ExpiresAt, LastRefresh: in.LastRefresh, Metadata: cloneStrings(in.Metadata)}
}

func cloneStrings(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func usageFromTelemetry(in telemetry.DataSnapshot) Usage {
	out := Usage{Clients: make(map[string]map[string]ProviderUsage, len(in.Clients))}
	for clientName, providers := range in.Clients {
		next := make(map[string]ProviderUsage, len(providers))
		for providerName, usage := range providers {
			daily := make(map[string]DailyCostBucket, len(usage.DailyCosts))
			for day, bucket := range usage.DailyCosts {
				daily[day] = DailyCostBucket{CostMicros: bucket.CostMicros, HasCost: bucket.HasCost}
			}
			next[providerName] = ProviderUsage{RequestCount: usage.RequestCount, SuccessCount: usage.SuccessCount, InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, TotalTokens: usage.TotalTokens, ReasoningTokens: usage.ReasoningTokens, ThoughtsTokens: usage.ThoughtsTokens, TotalCostMicros: usage.TotalCostMicros, LastUsedAt: usage.LastUsedAt, Usage: cloneAnyMap(usage.Usage), DailyCosts: daily, HasCost: usage.HasCost}
		}
		out.Clients[clientName] = next
	}
	return out
}

func (in Usage) toTelemetry() telemetry.DataSnapshot {
	out := telemetry.DataSnapshot{Clients: make(map[string]map[string]telemetry.ProviderUsage, len(in.Clients))}
	for clientName, providers := range in.Clients {
		next := make(map[string]telemetry.ProviderUsage, len(providers))
		for providerName, usage := range providers {
			daily := make(map[string]telemetry.DailyCostBucket, len(usage.DailyCosts))
			for day, bucket := range usage.DailyCosts {
				daily[day] = telemetry.DailyCostBucket{CostMicros: bucket.CostMicros, HasCost: bucket.HasCost}
			}
			next[providerName] = telemetry.ProviderUsage{RequestCount: usage.RequestCount, SuccessCount: usage.SuccessCount, InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, TotalTokens: usage.TotalTokens, ReasoningTokens: usage.ReasoningTokens, ThoughtsTokens: usage.ThoughtsTokens, TotalCostMicros: usage.TotalCostMicros, LastUsedAt: usage.LastUsedAt, Usage: cloneAnyMap(usage.Usage), DailyCosts: daily, HasCost: usage.HasCost}
		}
		out.Clients[clientName] = next
	}
	return out
}

func cloneAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
