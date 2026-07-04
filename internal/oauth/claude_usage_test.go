package oauth

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
)

type stubClaudeUsageProvider struct {
	usage    *ClaudeUsageDetails
	usageErr error
}

func (c *stubClaudeUsageProvider) Provider() config.OAuthProvider {
	return config.OAuthProviderClaude
}

func (c *stubClaudeUsageProvider) StartLogin(time.Time, time.Duration) (*LoginSession, error) {
	return nil, fmt.Errorf("not implemented")
}

func (c *stubClaudeUsageProvider) ExchangeSessionCode(context.Context, *LoginSession, string) (*Credential, error) {
	return nil, fmt.Errorf("not implemented")
}

func (c *stubClaudeUsageProvider) Refresh(_ context.Context, cred *Credential) (*Credential, error) {
	return cred.Clone(), nil
}

func (c *stubClaudeUsageProvider) FetchUsage(context.Context, *Credential) (*ClaudeUsageDetails, error) {
	if c.usage == nil {
		return nil, c.usageErr
	}
	return c.usage, c.usageErr
}

func TestClaudeFetchUsage_ParsesQuotaWindows(t *testing.T) {
	resetTime := "2026-05-08T12:15:00Z"
	const wantUsageUserAgent = "claude-cli/2.1.201 (external, sdk-cli)"

	usageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Method; got != http.MethodGet {
			t.Fatalf("method = %q, want GET", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer access-1" {
			t.Fatalf("authorization = %q, want Bearer access-1", got)
		}
		if got := r.Header.Get("Anthropic-Beta"); got != claudeUsageBeta {
			t.Fatalf("Anthropic-Beta = %q, want %q", got, claudeUsageBeta)
		}
		if got := r.Header.Get("User-Agent"); got != wantUsageUserAgent {
			t.Fatalf("User-Agent = %q, want %q", got, wantUsageUserAgent)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"subscriptionType": "max_20x",
			"five_hour": {
				"utilization": 100,
				"resets_at": "`+resetTime+`"
			},
			"seven_day_sonnet": {
				"utilization": 76,
				"resets_at": "`+resetTime+`"
			},
			"extra_usage": {
				"is_enabled": true,
				"monthly_limit": 2500,
				"used_credits": 1250,
				"utilization": 50
			}
		}`)
	}))
	defer usageServer.Close()

	client := &ClaudeClient{
		UsageURL:   usageServer.URL,
		HTTPClient: usageServer.Client(),
	}

	details, err := client.FetchUsage(t.Context(), &Credential{
		Ref:         "claude-sean-example-com",
		AccessToken: "access-1",
	})
	if err != nil {
		t.Fatalf("FetchUsage: %v", err)
	}
	if details == nil || details.FiveHour == nil {
		t.Fatalf("details = %#v, want five_hour window", details)
	}
	if details.PlanType != "max_20x" {
		t.Fatalf("plan type = %q, want max_20x", details.PlanType)
	}
	if got := details.FiveHour.Utilization; got != 100 {
		t.Fatalf("five_hour.utilization = %v, want 100", got)
	}
	wantReset, _ := time.Parse(time.RFC3339, resetTime)
	if !details.FiveHour.ResetsAt.Equal(wantReset) {
		t.Fatalf("five_hour.resets_at = %s, want %s", details.FiveHour.ResetsAt.Format(time.RFC3339), wantReset.Format(time.RFC3339))
	}
	if details.SevenDaySonnet == nil || details.SevenDaySonnet.Utilization != 76 {
		t.Fatalf("seven_day_sonnet = %#v, want utilization 76", details.SevenDaySonnet)
	}
	if details.ExtraUsage == nil || !details.ExtraUsage.IsEnabled {
		t.Fatalf("extra_usage = %#v, want enabled", details.ExtraUsage)
	}
	if details.ExtraUsage.MonthlyLimit == nil || *details.ExtraUsage.MonthlyLimit != 2500 {
		t.Fatalf("monthly_limit = %#v, want 2500", details.ExtraUsage.MonthlyLimit)
	}
}

func TestClaudeUsagePlanType_UsesKnownAliases(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload claudeUsagePayload
		want    string
	}{
		{
			name: "snake case subscription",
			payload: claudeUsagePayload{
				SubscriptionType: "pro",
				PlanType:         "free",
			},
			want: "pro",
		},
		{
			name: "camel case subscription",
			payload: claudeUsagePayload{
				SubscriptionTypeCamel: "max_20x",
				PlanType:              "free",
			},
			want: "max_20x",
		},
		{
			name: "plan type fallback",
			payload: claudeUsagePayload{
				PlanType: "team",
			},
			want: "team",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := claudeUsagePlanType(tc.payload); got != tc.want {
				t.Fatalf("claudeUsagePlanType() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestServiceGetClaudeUsage_UsesRegisteredProviderClient(t *testing.T) {
	now := time.Date(2026, 5, 8, 10, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	svc := NewService(dir,
		WithNowFunc(func() time.Time { return now }),
		WithProviderClient(&stubClaudeUsageProvider{
			usage: &ClaudeUsageDetails{
				FiveHour: &ClaudeUsageWindow{
					Utilization: 88,
					ResetsAt:    now.Add(2 * time.Hour),
				},
			},
		}),
	)
	if err := svc.Store().Save(&Credential{
		Ref:         "claude-sean-example-com",
		Provider:    config.OAuthProviderClaude,
		Email:       "sean@example.com",
		AccessToken: "access-1",
		ExpiresAt:   now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	details, err := svc.GetClaudeUsage(t.Context(), "claude-sean-example-com")
	if err != nil {
		t.Fatalf("GetClaudeUsage: %v", err)
	}
	if details == nil || details.FiveHour == nil {
		t.Fatalf("details = %#v, want five_hour window", details)
	}
	if got := details.FiveHour.Utilization; got != 88 {
		t.Fatalf("five_hour.utilization = %v, want 88", got)
	}
}
