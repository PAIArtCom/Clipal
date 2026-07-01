package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
	oauthpkg "github.com/lansespirit/Clipal/internal/oauth"
)

func TestOAuthRetryAfter_UncapsHeaderForOAuth(t *testing.T) {
	t.Parallel()

	hdr := make(http.Header)
	hdr.Set("Retry-After", "7200")

	cooldown, ok := oauthRetryAfter(hdr, nil, time.Now())
	if !ok {
		t.Fatalf("expected retry-after to be parsed")
	}
	if cooldown < 2*time.Hour-5*time.Second || cooldown > 2*time.Hour+5*time.Second {
		t.Fatalf("cooldown = %s, want about 2h", cooldown)
	}
}

func TestOAuthRetryAfter_ParsesNestedRetryDelay(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"error": {
			"code": 429,
			"details": [
				{
					"@type": "type.googleapis.com/google.rpc.RetryInfo",
					"retryDelay": "5400s"
				}
			]
		}
	}`)

	cooldown, ok := oauthRetryAfter(nil, body, time.Now())
	if !ok {
		t.Fatalf("expected retry delay to be parsed")
	}
	if cooldown != 90*time.Minute {
		t.Fatalf("cooldown = %s, want 90m", cooldown)
	}
}

func TestOAuthRetryAfter_ParsesGoogleQuotaResetDelayMetadata(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"error": {
			"code": 429,
			"details": [
				{
					"@type": "type.googleapis.com/google.rpc.ErrorInfo",
					"metadata": {
						"quotaResetDelay": "1h43m56s"
					}
				}
			]
		}
	}`)

	cooldown, ok := oauthRetryAfter(nil, body, time.Now())
	if !ok {
		t.Fatalf("expected quota reset delay to be parsed")
	}
	if cooldown != time.Hour+43*time.Minute+56*time.Second {
		t.Fatalf("cooldown = %s, want 1h43m56s", cooldown)
	}
}

func TestOAuthRetryAfter_ParsesGoogleQuotaResetTimestamp(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	body := []byte(`{
		"error": {
			"code": 429,
			"details": [
				{
					"@type": "type.googleapis.com/google.rpc.ErrorInfo",
					"metadata": {
						"quotaResetTimeStamp": "2026-05-07T12:15:00Z"
					}
				}
			]
		}
	}`)

	cooldown, ok := oauthRetryAfter(nil, body, now)
	if !ok {
		t.Fatalf("expected quota reset timestamp to be parsed")
	}
	if cooldown != 2*time.Hour+15*time.Minute {
		t.Fatalf("cooldown = %s, want 2h15m", cooldown)
	}
}

func TestOAuthRetryAfter_ParsesQuotaResetMessage(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"error": {
			"message": "You have exhausted your capacity on this model. Your quota will reset after 1h43m56s."
		}
	}`)

	cooldown, ok := oauthRetryAfter(nil, body, time.Now())
	if !ok {
		t.Fatalf("expected quota reset message to be parsed")
	}
	if cooldown != time.Hour+43*time.Minute+56*time.Second {
		t.Fatalf("cooldown = %s, want 1h43m56s", cooldown)
	}
}

func TestOAuthRetryAfter_ParsesAnthropicUnifiedResetHeader(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 8, 10, 0, 0, 0, time.UTC)
	reset := now.Add(5 * time.Hour).Unix()
	hdr := make(http.Header)
	hdr.Set("Anthropic-RateLimit-Unified-Reset", strconv.FormatInt(reset, 10))

	cooldown, ok := oauthRetryAfter(hdr, nil, now)
	if !ok {
		t.Fatalf("expected unified reset header to be parsed")
	}
	if cooldown != 5*time.Hour {
		t.Fatalf("cooldown = %s, want 5h", cooldown)
	}
}

func TestCodexOAuthCooldownUntil_UsesLatestExhaustedReset(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	primaryReset := now.Add(5 * time.Hour)
	weeklyReset := now.Add(6 * 24 * time.Hour)

	until, ok := codexOAuthCooldownUntil(&oauthpkg.CodexUsageDetails{
		LimitReached: true,
		Primary: &oauthpkg.CodexUsageWindow{
			UsedPercent:   100,
			WindowMinutes: 300,
			ResetsAt:      primaryReset,
		},
		Secondary: &oauthpkg.CodexUsageWindow{
			UsedPercent:   100,
			WindowMinutes: 10080,
			ResetsAt:      weeklyReset,
		},
	}, now)
	if !ok {
		t.Fatalf("expected exhausted reset")
	}
	if !until.Equal(weeklyReset) {
		t.Fatalf("until = %s, want %s", until.Format(time.RFC3339), weeklyReset.Format(time.RFC3339))
	}
}

func TestCodexOAuthCooldownUntil_UsesLimitReachedEvenBelowHundredPercent(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	reset := now.Add(5 * time.Hour)

	until, ok := codexOAuthCooldownUntil(&oauthpkg.CodexUsageDetails{
		LimitReached: true,
		Primary: &oauthpkg.CodexUsageWindow{
			UsedPercent:   99.5,
			WindowMinutes: 300,
			ResetsAt:      reset,
		},
	}, now)
	if !ok {
		t.Fatalf("expected exhausted reset")
	}
	if !until.Equal(reset) {
		t.Fatalf("until = %s, want %s", until.Format(time.RFC3339), reset.Format(time.RFC3339))
	}
}

func TestClaudeOAuthCooldownUntil_PrefersRepresentativeClaimWindow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 8, 10, 0, 0, 0, time.UTC)
	sessionReset := now.Add(5 * time.Hour)
	weeklyReset := now.Add(6 * 24 * time.Hour)

	until, ok := claudeOAuthCooldownUntil(&oauthpkg.ClaudeUsageDetails{
		FiveHour: &oauthpkg.ClaudeUsageWindow{
			Utilization: 96,
			ResetsAt:    sessionReset,
		},
		SevenDay: &oauthpkg.ClaudeUsageWindow{
			Utilization: 100,
			ResetsAt:    weeklyReset,
		},
	}, "five_hour", now)
	if !ok {
		t.Fatalf("expected exhausted reset")
	}
	if !until.Equal(sessionReset) {
		t.Fatalf("until = %s, want %s", until.Format(time.RFC3339), sessionReset.Format(time.RFC3339))
	}
}

func TestGeminiOAuthCooldownUntil_PrefersMatchingTierBucket(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	proReset := now.Add(5 * time.Hour)
	flashReset := now.Add(30 * time.Minute)
	proRemaining := 0.03
	flashRemaining := 0.0

	until, ok := geminiOAuthCooldownUntil(&oauthpkg.GeminiUsageDetails{
		Buckets: []oauthpkg.GeminiUsageBucket{
			{
				ModelID:           "gemini-2.5-pro",
				RemainingFraction: &proRemaining,
				ResetTime:         proReset,
			},
			{
				ModelID:           "gemini-2.5-flash",
				RemainingFraction: &flashRemaining,
				ResetTime:         flashReset,
			},
		},
	}, "gemini-2.5-pro-preview-05-06", now)
	if !ok {
		t.Fatalf("expected exhausted reset")
	}
	if !until.Equal(proReset) {
		t.Fatalf("until = %s, want %s", until.Format(time.RFC3339), proReset.Format(time.RFC3339))
	}
}

func TestGeminiOAuthCooldownUntil_FallsBackToAnyExhaustedBucket(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	reset := now.Add(2 * time.Hour)
	remaining := 0.0

	until, ok := geminiOAuthCooldownUntil(&oauthpkg.GeminiUsageDetails{
		Buckets: []oauthpkg.GeminiUsageBucket{
			{
				ModelID:           "gemini-2.5-flash",
				RemainingFraction: &remaining,
				ResetTime:         reset,
			},
		},
	}, "gemini-2.5-pro", now)
	if !ok {
		t.Fatalf("expected exhausted reset")
	}
	if !until.Equal(reset) {
		t.Fatalf("until = %s, want %s", until.Format(time.RFC3339), reset.Format(time.RFC3339))
	}
}

func TestGeminiOAuthCooldownUntil_DoesNotFallbackAcrossTiersWhenMatchedTierExists(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	proReset := now.Add(5 * time.Hour)
	flashReset := now.Add(2 * time.Hour)
	proRemaining := 0.40
	flashRemaining := 0.0

	until, ok := geminiOAuthCooldownUntil(&oauthpkg.GeminiUsageDetails{
		Buckets: []oauthpkg.GeminiUsageBucket{
			{
				ModelID:           "gemini-2.5-pro",
				RemainingFraction: &proRemaining,
				ResetTime:         proReset,
			},
			{
				ModelID:           "gemini-2.5-flash",
				RemainingFraction: &flashRemaining,
				ResetTime:         flashReset,
			},
		},
	}, "gemini-2.5-pro-preview-05-06", now)
	if ok {
		t.Fatalf("until = %s, want no cooldown", until.Format(time.RFC3339))
	}
}

func TestGeminiOAuthCooldown_UsesAntigravityUsageProvider(t *testing.T) {
	now := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	reset := now.Add(2 * time.Hour)
	usageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Method; got != http.MethodPost {
			t.Fatalf("method = %s, want POST", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer access-1" {
			t.Fatalf("authorization = %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		if got := string(body); got != `{"project":"project-123"}` {
			t.Fatalf("body = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1internal:retrieveUserQuota":
			_, _ = io.WriteString(w, fmt.Sprintf(`{
				"buckets": [
					{
						"modelId": "gemini-3.1-pro",
						"tokenType": "GOOGLE_ONE_AI",
						"remainingFraction": 0.0,
						"resetTime": %q
					}
				]
			}`, reset.Format(time.RFC3339)))
		case "/v1internal:retrieveUserQuotaSummary":
			_, _ = io.WriteString(w, `{"groups":[]}`)
		default:
			t.Fatalf("path = %s, want quota or quota summary", r.URL.Path)
		}
	}))
	defer usageServer.Close()

	dir := t.TempDir()
	svc := oauthpkg.NewService(dir,
		oauthpkg.WithNowFunc(func() time.Time { return now }),
		oauthpkg.WithAntigravityClient(&oauthpkg.AntigravityClient{
			DailyCloudCodeURL: usageServer.URL,
			CloudCodeVersion:  "v1internal",
			HTTPClient:        usageServer.Client(),
			Now:               func() time.Time { return now },
		}),
	)
	if err := svc.Store().Save(&oauthpkg.Credential{
		Ref:         "antigravity-sean-example-com",
		Provider:    config.OAuthProviderAntigravity,
		AccessToken: "access-1",
		AccountID:   "project-123",
		ExpiresAt:   now.Add(time.Hour),
		Metadata: map[string]string{
			"project_id": "project-123",
		},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cp := &ClientProxy{oauth: svc}
	provider := config.Provider{
		Name:          "antigravity-oauth",
		AuthType:      config.ProviderAuthTypeOAuth,
		OAuthProvider: config.OAuthProviderAntigravity,
		OAuthRef:      "antigravity-sean-example-com",
	}

	cooldown, ok := cp.geminiOAuthCooldown(t.Context(), provider, 0, "/v1beta/models/gemini-3.1-pro:generateContent", now)
	if !ok {
		t.Fatalf("expected cooldown")
	}
	if cooldown != 2*time.Hour {
		t.Fatalf("cooldown = %s, want 2h", cooldown)
	}
}
