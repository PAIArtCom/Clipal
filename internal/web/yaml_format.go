package web

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/lansespirit/Clipal/internal/config"
)

func writeBufferString(b *bytes.Buffer, s string) {
	_, _ = b.WriteString(s)
}

func formatClientConfigYAML(clientType string, cc config.ClientConfig) []byte {
	var b bytes.Buffer

	header := clientConfigHeader(clientType)
	writeBufferString(&b, "# "+header+"\n")
	writeBufferString(&b, "# Providers are sorted by priority (lower number = higher priority)\n\n")

	mode := strings.TrimSpace(string(cc.Mode))
	if mode == "" {
		mode = string(config.ClientModeAuto)
	}
	writeBufferString(&b, fmt.Sprintf("mode: %s # auto | manual\n", yamlMaybeQuoteEmpty(mode)))
	writeBufferString(&b, fmt.Sprintf("pinned_provider: %s # used when mode=manual\n\n", yamlMaybeQuoteEmpty(strings.TrimSpace(cc.PinnedProvider))))

	if len(cc.Providers) == 0 {
		writeBufferString(&b, "providers: []\n")
		return b.Bytes()
	}

	providers := make([]config.Provider, len(cc.Providers))
	copy(providers, cc.Providers)
	sort.SliceStable(providers, func(i, j int) bool {
		return providers[i].Priority < providers[j].Priority
	})

	uniquePriorities := make([]int, 0, len(providers))
	for _, p := range providers {
		if len(uniquePriorities) == 0 || uniquePriorities[len(uniquePriorities)-1] != p.Priority {
			uniquePriorities = append(uniquePriorities, p.Priority)
		}
	}
	cut1, cut2 := priorityTierCuts(uniquePriorities)

	writeBufferString(&b, "providers:\n")

	prevTier := 0
	for i, p := range providers {
		if i > 0 {
			writeBufferString(&b, "\n")
		}

		tier := priorityTier(p.Priority, cut1, cut2)
		if tier != prevTier {
			writeBufferString(&b, fmt.Sprintf("  # Priority level %d: %s\n", tier, priorityTierLabel(tier, cut1, cut2)))
			prevTier = tier
		}

		authType := p.NormalizedAuthType()
		writeBufferString(&b, fmt.Sprintf("  - name: %s\n", yamlDoubleQuote(p.Name)))
		if authType == config.ProviderAuthTypeAPIKey || strings.TrimSpace(p.BaseURL) != "" {
			writeBufferString(&b, fmt.Sprintf("    base_url: %s\n", yamlDoubleQuote(p.BaseURL)))
		}
		if authType == config.ProviderAuthTypeOAuth {
			writeBufferString(&b, fmt.Sprintf("    auth_type: %s\n", yamlDoubleQuote(string(authType))))
			writeBufferString(&b, fmt.Sprintf("    oauth_provider: %s\n", yamlDoubleQuote(string(p.NormalizedOAuthProvider()))))
			writeBufferString(&b, fmt.Sprintf("    oauth_ref: %s\n", yamlDoubleQuote(p.NormalizedOAuthRef())))
			if p.NormalizedOAuthIdentity() != "" {
				writeBufferString(&b, fmt.Sprintf("    oauth_identity: %s\n", yamlDoubleQuote(p.NormalizedOAuthIdentity())))
			}
		}
		if p.NormalizedProxyMode() != config.ProviderProxyModeDefault {
			writeBufferString(&b, fmt.Sprintf("    proxy_mode: %s\n", yamlDoubleQuote(string(p.NormalizedProxyMode()))))
		}
		if p.NormalizedProxyMode() == config.ProviderProxyModeCustom && p.NormalizedProxyURL() != "" {
			writeBufferString(&b, fmt.Sprintf("    proxy_url: %s\n", yamlDoubleQuote(p.NormalizedProxyURL())))
		}
		if authType == config.ProviderAuthTypeAPIKey {
			keys := p.NormalizedAPIKeys()
			if len(keys) <= 1 {
				writeBufferString(&b, fmt.Sprintf("    api_key: %s\n", yamlDoubleQuote(p.PrimaryAPIKey())))
			} else {
				writeBufferString(&b, "    api_keys:\n")
				for _, key := range keys {
					writeBufferString(&b, fmt.Sprintf("      - %s\n", yamlDoubleQuote(key)))
				}
			}
		}
		writeBufferString(&b, fmt.Sprintf("    priority: %d\n", p.Priority))
		writeBufferString(&b, fmt.Sprintf("    enabled: %v\n", p.IsEnabled()))
		if p.ModelOverride() != "" || p.OpenAIReasoningEffort() != "" || p.ClaudeThinkingBudgetTokens() > 0 || p.ClaudeEffort() != "" {
			writeBufferString(&b, "    overrides:\n")
			if p.ModelOverride() != "" {
				writeBufferString(&b, fmt.Sprintf("      model: %s\n", yamlDoubleQuote(p.ModelOverride())))
			}
			if p.OpenAIReasoningEffort() != "" {
				writeBufferString(&b, "      openai:\n")
				writeBufferString(&b, fmt.Sprintf("        reasoning_effort: %s\n", yamlDoubleQuote(p.OpenAIReasoningEffort())))
			}
			if p.ClaudeThinkingBudgetTokens() > 0 || p.ClaudeEffort() != "" {
				writeBufferString(&b, "      claude:\n")
				if p.ClaudeThinkingBudgetTokens() > 0 {
					writeBufferString(&b, fmt.Sprintf("        thinking_budget_tokens: %d\n", p.ClaudeThinkingBudgetTokens()))
				}
				if p.ClaudeEffort() != "" {
					writeBufferString(&b, fmt.Sprintf("        effort: %s\n", yamlDoubleQuote(p.ClaudeEffort())))
				}
			}
		}
	}

	writeBufferString(&b, "\n")
	return b.Bytes()
}

func formatGlobalConfigYAML(gc config.GlobalConfig) []byte {
	var b bytes.Buffer

	writeBufferString(&b, "# Global configuration for clipal\n")
	writeBufferString(&b, "# Managed by the web UI (comments kept for readability)\n\n")

	// Quote strings to avoid YAML injection via newlines/# and to keep the file
	// parseable even if a value contains spaces or special characters.
	writeBufferString(&b, fmt.Sprintf("listen_addr: %s\n", yamlDoubleQuote(strings.TrimSpace(gc.ListenAddr))))
	writeBufferString(&b, fmt.Sprintf("port: %d\n", gc.Port))
	writeBufferString(&b, fmt.Sprintf("log_level: %s # debug | info | warn | error\n", yamlDoubleQuote(strings.TrimSpace(string(gc.LogLevel)))))
	writeBufferString(&b, fmt.Sprintf("reactivate_after: %s # set to 0 to disable temporary deactivation\n", yamlDoubleQuote(strings.TrimSpace(gc.ReactivateAfter))))
	writeBufferString(&b, "# Cancel an upstream attempt if no response body bytes are received for this long.\n")
	writeBufferString(&b, "# Useful for SSE streams that can hang after headers. Set to 0 to disable.\n")
	writeBufferString(&b, fmt.Sprintf("upstream_idle_timeout: %s\n", yamlDoubleQuote(strings.TrimSpace(gc.UpstreamIdleTimeout))))
	writeBufferString(&b, "# How long to wait for the upstream to return response headers.\n")
	writeBufferString(&b, "# Set to 0 to disable.\n")
	writeBufferString(&b, fmt.Sprintf("response_header_timeout: %s\n", yamlDoubleQuote(strings.TrimSpace(gc.ResponseHeaderTimeout))))
	writeBufferString(&b, "# Default upstream proxy for providers whose proxy_mode is default.\n")
	writeBufferString(&b, fmt.Sprintf("upstream_proxy_mode: %s # environment | direct | custom\n", yamlDoubleQuote(string(gc.NormalizedUpstreamProxyMode()))))
	writeBufferString(&b, "# Supported proxy URLs: http://, https://, socks5://, socks5h://\n")
	writeBufferString(&b, fmt.Sprintf("upstream_proxy_url: %s\n", yamlDoubleQuote(gc.NormalizedUpstreamProxyURL())))
	writeBufferString(&b, "# Max request body size in bytes (clipal buffers request bodies for retries).\n")
	writeBufferString(&b, fmt.Sprintf("max_request_body_bytes: %d\n\n", gc.MaxRequestBody))

	writeBufferString(&b, "# Default: <config-dir>/logs (e.g. ~/.clipal/logs)\n")
	writeBufferString(&b, fmt.Sprintf("log_dir: %s\n", yamlDoubleQuote(strings.TrimSpace(gc.LogDir))))
	writeBufferString(&b, fmt.Sprintf("log_retention_days: %d # default 7 days\n", gc.LogRetentionDays))
	writeBufferString(&b, fmt.Sprintf("log_stdout: %v\n\n", boolPtrOrTrue(gc.LogStdout)))

	writeBufferString(&b, "# Circuit breaker (prevents repeated requests to unhealthy providers)\n")
	writeBufferString(&b, "circuit_breaker:\n")
	writeBufferString(&b, fmt.Sprintf("  failure_threshold: %d # consecutive failures before opening (set to 0 to disable)\n", gc.CircuitBreaker.FailureThreshold))
	writeBufferString(&b, fmt.Sprintf("  success_threshold: %d # consecutive successes in half-open before closing\n", gc.CircuitBreaker.SuccessThreshold))
	writeBufferString(&b, fmt.Sprintf("  open_timeout: %s # e.g. 60s, 2m\n", yamlDoubleQuote(strings.TrimSpace(gc.CircuitBreaker.OpenTimeout))))
	writeBufferString(&b, fmt.Sprintf("  half_open_max_inflight: %d # concurrent probe requests in half-open\n\n", gc.CircuitBreaker.HalfOpenMaxInFlight))

	writeBufferString(&b, "# Desktop notifications (best-effort, cross-platform via beeep)\n")
	writeBufferString(&b, "notifications:\n")
	writeBufferString(&b, fmt.Sprintf("  enabled: %v\n", gc.Notifications.Enabled))
	writeBufferString(&b, fmt.Sprintf("  min_level: %s # debug | info | warn | error\n", yamlDoubleQuote(strings.TrimSpace(string(gc.Notifications.MinLevel)))))
	writeBufferString(&b, fmt.Sprintf("  provider_switch: %v\n", boolPtrOrTrue(gc.Notifications.ProviderSwitch)))

	writeBufferString(&b, "\n# Routing strategy\n")
	writeBufferString(&b, "routing:\n")
	writeBufferString(&b, "  sticky_sessions:\n")
	writeBufferString(&b, fmt.Sprintf("    enabled: %v\n", gc.Routing.StickySessions.Enabled))
	writeBufferString(&b, fmt.Sprintf("    explicit_ttl: %s # idle TTL for explicit sticky bindings\n", yamlDoubleQuote(strings.TrimSpace(gc.Routing.StickySessions.ExplicitTTL))))
	writeBufferString(&b, fmt.Sprintf("    cache_hint_ttl: %s\n", yamlDoubleQuote(strings.TrimSpace(gc.Routing.StickySessions.CacheHintTTL))))
	writeBufferString(&b, fmt.Sprintf("    dynamic_feature_ttl: %s\n", yamlDoubleQuote(strings.TrimSpace(gc.Routing.StickySessions.DynamicFeatureTTL))))
	writeBufferString(&b, fmt.Sprintf("    dynamic_feature_capacity: %d\n", gc.Routing.StickySessions.DynamicFeatureCapacity))
	writeBufferString(&b, fmt.Sprintf("    response_lookup_ttl: %s\n", yamlDoubleQuote(strings.TrimSpace(gc.Routing.StickySessions.ResponseLookupTTL))))
	writeBufferString(&b, "  busy_backpressure:\n")
	writeBufferString(&b, fmt.Sprintf("    enabled: %v\n", gc.Routing.BusyBackpressure.Enabled))
	writeBufferString(&b, fmt.Sprintf("    retry_delays: [%s]\n", yamlInlineQuotedList(gc.Routing.BusyBackpressure.RetryDelays)))
	writeBufferString(&b, fmt.Sprintf("    probe_max_inflight: %d\n", gc.Routing.BusyBackpressure.ProbeMaxInFlight))
	writeBufferString(&b, fmt.Sprintf("    short_retry_after_max: %s\n", yamlDoubleQuote(strings.TrimSpace(gc.Routing.BusyBackpressure.ShortRetryAfterMax))))
	writeBufferString(&b, fmt.Sprintf("    max_inline_wait: %s\n", yamlDoubleQuote(strings.TrimSpace(gc.Routing.BusyBackpressure.MaxInlineWait))))

	writeBufferString(&b, "\n")
	return b.Bytes()
}

func yamlInlineQuotedList(values []string) string {
	if len(values) == 0 {
		return ""
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, yamlDoubleQuote(strings.TrimSpace(v)))
	}
	return strings.Join(out, ", ")
}

func clientConfigHeader(clientType string) string {
	if canonical, ok := config.CanonicalClientType(clientType); ok {
		clientType = canonical
	}
	switch clientType {
	case "claude":
		return "Claude-style API providers configuration"
	case "openai":
		return "OpenAI-compatible API providers configuration"
	case "gemini":
		return "Gemini-style API providers configuration"
	default:
		return fmt.Sprintf("%s providers configuration", clientType)
	}
}

func priorityTierCuts(uniquePriorities []int) (cut1 int, cut2 int) {
	if len(uniquePriorities) == 0 {
		return 0, 0
	}
	if len(uniquePriorities) == 1 {
		return uniquePriorities[0], uniquePriorities[0]
	}
	if len(uniquePriorities) == 2 {
		return uniquePriorities[0], uniquePriorities[1]
	}

	minP := uniquePriorities[0]
	maxP := uniquePriorities[len(uniquePriorities)-1]
	if minP >= maxP {
		return minP, maxP
	}

	// Split the numeric priority range into 3 buckets and snap to existing values.
	// Tiers may be empty if the configured priorities are sparse (e.g. 1,2,1000),
	// which is intentional for readability.
	target1 := float64(minP) + float64(maxP-minP)/3.0
	target2 := float64(minP) + 2.0*float64(maxP-minP)/3.0

	cut1 = minP
	cut2 = minP
	for _, p := range uniquePriorities {
		if float64(p) <= target1 {
			cut1 = p
		}
		if float64(p) <= target2 {
			cut2 = p
		}
	}
	if cut2 < cut1 {
		cut2 = cut1
	}
	return cut1, cut2
}

func priorityTier(priority int, cut1 int, cut2 int) int {
	switch {
	case priority <= cut1:
		return 1
	case priority <= cut2:
		return 2
	default:
		return 3
	}
}

func priorityTierLabel(tier int, cut1 int, cut2 int) string {
	switch tier {
	case 1:
		return fmt.Sprintf("highest priority (primary) [priority<=%d]", cut1)
	case 2:
		return fmt.Sprintf("fallback [priority<=%d]", cut2)
	default:
		return fmt.Sprintf("backup / other [priority>%d]", cut2)
	}
}

func yamlMaybeQuoteEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return `""`
	}
	return s
}

func yamlDoubleQuote(s string) string {
	// YAML double-quoted scalar with minimal escaping.
	var b strings.Builder
	b.Grow(len(s) + 2)
	_ = b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			_, _ = b.WriteString(`\\`)
		case '"':
			_, _ = b.WriteString(`\"`)
		case '\n':
			_, _ = b.WriteString(`\n`)
		case '\r':
			_, _ = b.WriteString(`\r`)
		case '\t':
			_, _ = b.WriteString(`\t`)
		default:
			if c < 0x20 {
				// Control characters should be escaped in YAML double quotes.
				_, _ = fmt.Fprintf(&b, `\x%02x`, c)
				continue
			}
			_ = b.WriteByte(c)
		}
	}
	_ = b.WriteByte('"')
	return b.String()
}
