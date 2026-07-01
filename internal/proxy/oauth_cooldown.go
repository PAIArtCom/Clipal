package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
	oauthpkg "github.com/lansespirit/Clipal/internal/oauth"
)

const (
	oauthCooldownLookupTimeout = 5 * time.Second
	maxOAuthRetryAfterCooldown = 8 * 24 * time.Hour
	oauthExhaustedPercent      = 100
	geminiOAuthExhaustedFloor  = 0.05
	claudeOAuthExhaustedFloor  = 95.0
)

func isOAuthCooldownReason(reason string) bool {
	switch strings.TrimSpace(reason) {
	case "quota", "rate_limit", "overloaded":
		return true
	default:
		return false
	}
}

func (cp *ClientProxy) oauthCooldownForFailure(ctx context.Context, provider config.Provider, providerIndex int, path string, hdr http.Header, body []byte, fallback time.Duration) time.Duration {
	if !provider.UsesOAuth() {
		return fallback
	}

	now := time.Now()
	switch provider.NormalizedOAuthProvider() {
	case config.OAuthProviderCodex:
		if cooldown, ok := cp.codexOAuthCooldown(ctx, provider, providerIndex, now); ok {
			return cooldown
		}
	case config.OAuthProviderClaude:
		if cooldown, ok := cp.claudeOAuthCooldown(ctx, provider, providerIndex, hdr, now); ok {
			return cooldown
		}
		if cooldown, ok := oauthRetryAfter(hdr, body, now); ok {
			return cooldown
		}
		return fallback
	case config.OAuthProviderGemini:
		usageCooldown, usageOK := cp.geminiOAuthCooldown(ctx, provider, providerIndex, path, now)
		retryAfterCooldown, retryAfterOK := oauthRetryAfter(hdr, body, now)
		if usageOK && retryAfterOK {
			if retryAfterCooldown > usageCooldown {
				return retryAfterCooldown
			}
			return usageCooldown
		}
		if usageOK {
			return usageCooldown
		}
		if retryAfterOK {
			return retryAfterCooldown
		}
		return fallback
	}

	if cooldown, ok := oauthRetryAfter(hdr, body, now); ok {
		return cooldown
	}
	return fallback
}

func (cp *ClientProxy) codexOAuthCooldown(ctx context.Context, provider config.Provider, providerIndex int, now time.Time) (time.Duration, bool) {
	if cp == nil || cp.oauth == nil {
		return 0, false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	lookupCtx, cancel := context.WithTimeout(ctx, oauthCooldownLookupTimeout)
	defer cancel()

	details, err := cp.oauth.GetCodexUsageWithHTTPClient(lookupCtx, provider.NormalizedOAuthRef(), cp.oauthHTTPClientForProvider(provider, providerIndex))
	if err != nil {
		return 0, false
	}
	until, ok := codexOAuthCooldownUntil(details, now)
	if !ok {
		return 0, false
	}
	cooldown := until.Sub(now)
	if cooldown <= 0 {
		return 0, false
	}
	if cooldown > maxOAuthRetryAfterCooldown {
		cooldown = maxOAuthRetryAfterCooldown
	}
	return cooldown, true
}

func (cp *ClientProxy) claudeOAuthCooldown(ctx context.Context, provider config.Provider, providerIndex int, hdr http.Header, now time.Time) (time.Duration, bool) {
	if cp == nil || cp.oauth == nil {
		return 0, false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	lookupCtx, cancel := context.WithTimeout(ctx, oauthCooldownLookupTimeout)
	defer cancel()

	details, err := cp.oauth.GetClaudeUsageWithHTTPClient(lookupCtx, provider.NormalizedOAuthRef(), cp.oauthHTTPClientForProvider(provider, providerIndex))
	if err != nil {
		return 0, false
	}
	until, ok := claudeOAuthCooldownUntil(details, claudeOAuthRepresentativeClaim(hdr), now)
	if !ok {
		return 0, false
	}
	cooldown := until.Sub(now)
	if cooldown <= 0 {
		return 0, false
	}
	if cooldown > maxOAuthRetryAfterCooldown {
		cooldown = maxOAuthRetryAfterCooldown
	}
	return cooldown, true
}

func (cp *ClientProxy) geminiOAuthCooldown(ctx context.Context, provider config.Provider, providerIndex int, path string, now time.Time) (time.Duration, bool) {
	if cp == nil || cp.oauth == nil {
		return 0, false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	lookupCtx, cancel := context.WithTimeout(ctx, oauthCooldownLookupTimeout)
	defer cancel()

	modelName, _ := geminiModelFromPath(path)
	details, err := cp.oauth.GetGeminiUsageForProviderWithHTTPClient(lookupCtx, provider.NormalizedOAuthProvider(), provider.NormalizedOAuthRef(), cp.oauthHTTPClientForProvider(provider, providerIndex))
	if err != nil {
		return 0, false
	}
	until, ok := geminiOAuthCooldownUntil(details, modelName, now)
	if !ok {
		return 0, false
	}
	cooldown := until.Sub(now)
	if cooldown <= 0 {
		return 0, false
	}
	if cooldown > maxOAuthRetryAfterCooldown {
		cooldown = maxOAuthRetryAfterCooldown
	}
	return cooldown, true
}

func codexOAuthCooldownUntil(details *oauthpkg.CodexUsageDetails, now time.Time) (time.Time, bool) {
	if details == nil {
		return time.Time{}, false
	}

	var latest time.Time
	consider := func(limitReached bool, windows ...*oauthpkg.CodexUsageWindow) {
		// When limitReached=true the API confirms exhaustion, so accept windows
		// that are close to 100% (handles floating-point accumulation). When
		// limitReached=false, require the window to actually be at the hard cap.
		threshold := float64(oauthExhaustedPercent)
		if limitReached {
			threshold = 98.0
		}
		for _, window := range windows {
			if window == nil || !window.ResetsAt.After(now) {
				continue
			}
			if window.UsedPercent < threshold {
				continue
			}
			if latest.IsZero() || window.ResetsAt.After(latest) {
				latest = window.ResetsAt
			}
		}
	}

	consider(details.LimitReached, details.Primary, details.Secondary)
	for _, limit := range details.Additional {
		consider(limit.LimitReached, limit.Primary, limit.Secondary)
	}

	if latest.IsZero() {
		return time.Time{}, false
	}
	return latest, true
}

func claudeOAuthCooldownUntil(details *oauthpkg.ClaudeUsageDetails, representativeClaim string, now time.Time) (time.Time, bool) {
	if details == nil {
		return time.Time{}, false
	}

	if representativeClaim != "" {
		for _, window := range claudeOAuthWindowsForClaim(details, representativeClaim) {
			if claudeOAuthWindowExhausted(window, claudeOAuthExhaustedFloor, now) {
				return window.ResetsAt, true
			}
		}
		return time.Time{}, false
	}

	var (
		exhausted *oauthpkg.ClaudeUsageWindow
		count     int
	)
	for _, window := range claudeOAuthAllWindows(details) {
		if !claudeOAuthWindowExhausted(window, float64(oauthExhaustedPercent), now) {
			continue
		}
		exhausted = window
		count++
		if count > 1 {
			return time.Time{}, false
		}
	}
	if exhausted == nil {
		return time.Time{}, false
	}
	return exhausted.ResetsAt, true
}

func geminiOAuthCooldownUntil(details *oauthpkg.GeminiUsageDetails, modelName string, now time.Time) (time.Time, bool) {
	if details == nil || len(details.Buckets) == 0 {
		return time.Time{}, false
	}

	if matched := geminiOAuthBucketsForModel(details.Buckets, modelName); len(matched) > 0 {
		return geminiOAuthLatestExhaustedReset(matched, now)
	}
	return geminiOAuthLatestExhaustedReset(details.Buckets, now)
}

func geminiOAuthBucketsForModel(buckets []oauthpkg.GeminiUsageBucket, modelName string) []oauthpkg.GeminiUsageBucket {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return nil
	}

	exact := make([]oauthpkg.GeminiUsageBucket, 0, len(buckets))
	tier := make([]oauthpkg.GeminiUsageBucket, 0, len(buckets))
	targetTier := geminiOAuthQuotaTier(modelName)

	for _, bucket := range buckets {
		bucketModel := strings.TrimSpace(bucket.ModelID)
		if bucketModel == "" {
			continue
		}
		if bucketModel == modelName {
			exact = append(exact, bucket)
			continue
		}
		if targetTier != "" && geminiOAuthQuotaTier(bucketModel) == targetTier {
			tier = append(tier, bucket)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return tier
}

func geminiOAuthLatestExhaustedReset(buckets []oauthpkg.GeminiUsageBucket, now time.Time) (time.Time, bool) {
	var latest time.Time
	for _, bucket := range buckets {
		if !bucket.ResetTime.After(now) || !geminiOAuthBucketExhausted(bucket) {
			continue
		}
		if latest.IsZero() || bucket.ResetTime.After(latest) {
			latest = bucket.ResetTime
		}
	}
	if latest.IsZero() {
		return time.Time{}, false
	}
	return latest, true
}

func geminiOAuthBucketExhausted(bucket oauthpkg.GeminiUsageBucket) bool {
	if bucket.RemainingFraction != nil && *bucket.RemainingFraction <= geminiOAuthExhaustedFloor {
		return true
	}
	return bucket.RemainingAmount != nil && *bucket.RemainingAmount <= 0
}

func geminiOAuthQuotaTier(modelName string) string {
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	switch {
	case strings.Contains(modelName, "flash-lite"):
		return "flash-lite"
	case strings.Contains(modelName, "flash"):
		return "flash"
	case strings.Contains(modelName, "pro"):
		return "pro"
	default:
		return ""
	}
}

func oauthRetryAfter(hdr http.Header, body []byte, now time.Time) (time.Duration, bool) {
	var max time.Duration
	consider := func(d time.Duration, ok bool) {
		if !ok || d <= 0 {
			return
		}
		if d > maxOAuthRetryAfterCooldown {
			d = maxOAuthRetryAfterCooldown
		}
		if d > max {
			max = d
		}
	}

	consider(parseRetryAfter(hdr.Get("Retry-After")))
	for _, key := range oauthRetryAfterHeaderKeys {
		consider(parseOAuthHintDuration(hdr.Get(key), now))
	}
	consider(oauthRetryAfterFromBody(body, now))

	if max <= 0 {
		return 0, false
	}
	return max, true
}

var oauthRetryAfterHeaderKeys = []string{
	"X-RateLimit-Reset",
	"X-RateLimit-Reset-Requests",
	"X-RateLimit-Reset-Tokens",
	"Anthropic-RateLimit-Requests-Reset",
	"Anthropic-RateLimit-Tokens-Reset",
	"Anthropic-RateLimit-Input-Tokens-Reset",
	"Anthropic-RateLimit-Output-Tokens-Reset",
	"Anthropic-RateLimit-Unified-Reset",
	"Anthropic-RateLimit-Unified-Overage-Reset",
	"Anthropic-RateLimit-Unified-5h-Reset",
	"Anthropic-RateLimit-Unified-7d-Reset",
	"X-Goog-Quota-Reset",
}

func claudeOAuthRepresentativeClaim(hdr http.Header) string {
	if hdr == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(hdr.Get("Anthropic-RateLimit-Unified-Representative-Claim")))
}

func claudeOAuthWindowsForClaim(details *oauthpkg.ClaudeUsageDetails, claim string) []*oauthpkg.ClaudeUsageWindow {
	if details == nil {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(claim)) {
	case "five_hour":
		return []*oauthpkg.ClaudeUsageWindow{details.FiveHour}
	case "seven_day":
		return []*oauthpkg.ClaudeUsageWindow{details.SevenDay}
	case "seven_day_oauth_apps":
		return []*oauthpkg.ClaudeUsageWindow{details.SevenDayOAuthApps, details.SevenDay}
	case "seven_day_opus":
		return []*oauthpkg.ClaudeUsageWindow{details.SevenDayOpus, details.SevenDay}
	case "seven_day_sonnet":
		return []*oauthpkg.ClaudeUsageWindow{details.SevenDaySonnet, details.SevenDay}
	default:
		return nil
	}
}

func claudeOAuthAllWindows(details *oauthpkg.ClaudeUsageDetails) []*oauthpkg.ClaudeUsageWindow {
	if details == nil {
		return nil
	}
	return []*oauthpkg.ClaudeUsageWindow{
		details.FiveHour,
		details.SevenDay,
		details.SevenDayOAuthApps,
		details.SevenDayOpus,
		details.SevenDaySonnet,
	}
}

func claudeOAuthWindowExhausted(window *oauthpkg.ClaudeUsageWindow, threshold float64, now time.Time) bool {
	if window == nil || !window.ResetsAt.After(now) {
		return false
	}
	return window.Utilization >= threshold
}

func oauthRetryAfterFromBody(body []byte, now time.Time) (time.Duration, bool) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 || !json.Valid(body) {
		return 0, false
	}
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return 0, false
	}

	var max time.Duration
	var walk func(string, any)
	walk = func(key string, value any) {
		if d, ok := oauthDurationFromJSONHint(key, value, now); ok && d > max {
			max = d
		}
		switch typed := value.(type) {
		case map[string]any:
			for childKey, childValue := range typed {
				walk(childKey, childValue)
			}
		case []any:
			for _, item := range typed {
				walk("", item)
			}
		}
	}
	walk("", root)

	if max <= 0 {
		return 0, false
	}
	if max > maxOAuthRetryAfterCooldown {
		max = maxOAuthRetryAfterCooldown
	}
	return max, true
}

func oauthDurationFromJSONHint(key string, value any, now time.Time) (time.Duration, bool) {
	normalized := normalizeOAuthHintKey(key)
	if normalized == "" {
		return 0, false
	}
	switch {
	case strings.Contains(normalized, "retryafter") ||
		strings.Contains(normalized, "retrydelay") ||
		strings.Contains(normalized, "resetafter") ||
		strings.Contains(normalized, "resetdelay"):
		return parseOAuthHintDuration(jsonHintString(value), now)
	case strings.Contains(normalized, "resetat") ||
		strings.Contains(normalized, "resetsat") ||
		strings.Contains(normalized, "resettime") ||
		strings.Contains(normalized, "resettimestamp"):
		return parseOAuthAbsoluteReset(jsonHintString(value), now)
	case normalized == "message":
		return parseOAuthQuotaResetMessage(jsonHintString(value))
	default:
		return 0, false
	}
}

func normalizeOAuthHintKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.ReplaceAll(key, "_", "")
	key = strings.ReplaceAll(key, "-", "")
	return key
}

func jsonHintString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return strings.TrimSpace(typed.String())
	case float64:
		return strings.TrimSpace(strings.TrimRight(strings.TrimRight(strconv.FormatFloat(typed, 'f', -1, 64), "0"), "."))
	default:
		return ""
	}
}

func parseOAuthHintDuration(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if n, err := strconv.ParseFloat(value, 64); err == nil {
		sec := int64(n)
		if sec > 1_000_000_000 || sec > 1_000_000_000_000 {
			if d, ok := parseOAuthAbsoluteReset(value, now); ok {
				return d, true
			}
		}
	}
	if d, ok := parseDurationHint(value); ok {
		return d, true
	}
	if d, ok := parseOAuthAbsoluteReset(value, now); ok {
		return d, true
	}
	return 0, false
}

func parseOAuthAbsoluteReset(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if t, err := http.ParseTime(value); err == nil {
		return t.Sub(now), true
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.Sub(now), true
	}
	if n, err := strconv.ParseFloat(value, 64); err == nil {
		sec := int64(n)
		if sec > 1_000_000_000_000 {
			sec = sec / 1000
		}
		if sec > 1_000_000_000 {
			return time.Unix(sec, 0).Sub(now), true
		}
	}
	return 0, false
}

func parseOAuthQuotaResetMessage(value string) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	lower := strings.ToLower(value)
	if !strings.Contains(lower, "quota") || !strings.Contains(lower, "reset after") {
		return 0, false
	}
	idx := strings.Index(lower, "reset after")
	if idx < 0 {
		return 0, false
	}
	tail := strings.TrimSpace(value[idx+len("reset after"):])
	end := 0
	for end < len(tail) {
		c := tail[end]
		if (c >= '0' && c <= '9') || c == '.' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			end++
			continue
		}
		break
	}
	if end == 0 {
		return 0, false
	}
	token := strings.TrimRight(tail[:end], ".;,")
	d, err := time.ParseDuration(token)
	if err != nil || d <= 0 {
		return 0, false
	}
	return d, true
}
