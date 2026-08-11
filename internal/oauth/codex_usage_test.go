package oauth

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/PAIArtCom/Clipal/internal/config"
)

type stubCodexUsageProvider struct {
	usage    *CodexUsageDetails
	usageErr error
}

func (c *stubCodexUsageProvider) Provider() config.OAuthProvider {
	return config.OAuthProviderCodex
}

func (c *stubCodexUsageProvider) StartLogin(time.Time, time.Duration) (*LoginSession, error) {
	return nil, fmt.Errorf("not implemented")
}

func (c *stubCodexUsageProvider) ExchangeSessionCode(context.Context, *LoginSession, string) (*Credential, error) {
	return nil, fmt.Errorf("not implemented")
}

func (c *stubCodexUsageProvider) Refresh(_ context.Context, cred *Credential) (*Credential, error) {
	return cred.Clone(), nil
}

func (c *stubCodexUsageProvider) FetchUsage(context.Context, *Credential) (*CodexUsageDetails, error) {
	if c.usage == nil {
		return nil, c.usageErr
	}
	return c.usage, c.usageErr
}

func TestCodexFetchUsage_ComputesResetTimeFromRelativeSeconds(t *testing.T) {
	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	usageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer access-1" {
			t.Fatalf("authorization = %q, want Bearer access-1", got)
		}
		if got := r.Header.Get("Chatgpt-Account-Id"); got != "account-123" {
			t.Fatalf("chatgpt-account-id = %q, want account-123", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
  "plan_type": "pro",
  "rate_limit": {
    "primary_window": {
      "used_percent": 42,
      "limit_window_seconds": 3600,
      "reset_after_seconds": 120
    }
  }
}`)
	}))
	defer usageServer.Close()

	client := &CodexClient{
		UsageURL:   usageServer.URL,
		HTTPClient: usageServer.Client(),
		Now:        func() time.Time { return now },
	}

	details, err := client.FetchUsage(t.Context(), &Credential{
		Ref:         "codex-sean-example-com",
		AccessToken: "access-1",
		AccountID:   "account-123",
	})
	if err != nil {
		t.Fatalf("FetchUsage: %v", err)
	}
	if details == nil || details.Primary == nil {
		t.Fatalf("expected primary usage details, got %#v", details)
	}
	if got := details.Primary.WindowMinutes; got != 60 {
		t.Fatalf("window_minutes = %d, want 60", got)
	}
	if got := details.Primary.ResetsAt; !got.Equal(now.Add(120 * time.Second)) {
		t.Fatalf("resets_at = %s, want %s", got.Format(time.RFC3339), now.Add(120*time.Second).Format(time.RFC3339))
	}
}

func TestMapCodexUsagePayload_PreservesLimitFlags(t *testing.T) {
	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	reset := now.Add(5 * time.Hour)

	details := mapCodexUsagePayload(codexUsagePayload{
		PlanType: "pro",
		RateLimit: &codexUsageRateLimitDetails{
			Allowed:      false,
			LimitReached: true,
			PrimaryWindow: &codexUsageWindow{
				UsedPercent:   99.5,
				WindowMinutes: 300,
				ResetAt:       reset.Unix(),
			},
		},
		AdditionalRateLimits: []codexAdditionalLimit{
			{
				LimitName:      "Code review",
				MeteredFeature: "code_review",
				RateLimit: &codexUsageRateLimitDetails{
					Allowed:      true,
					LimitReached: false,
					PrimaryWindow: &codexUsageWindow{
						UsedPercent:       75,
						LimitWindowSecs:   604800,
						ResetAfterSeconds: 3600,
					},
				},
			},
		},
	}, now)

	if details.Allowed || !details.LimitReached {
		t.Fatalf("limit flags = allowed=%v reached=%v, want false/true", details.Allowed, details.LimitReached)
	}
	if details.Primary == nil || !details.Primary.ResetsAt.Equal(reset) {
		t.Fatalf("primary = %#v, want reset %s", details.Primary, reset.Format(time.RFC3339))
	}
	if len(details.Additional) != 1 {
		t.Fatalf("additional len = %d, want 1", len(details.Additional))
	}
	if !details.Additional[0].Allowed || details.Additional[0].LimitReached {
		t.Fatalf("additional flags = allowed=%v reached=%v, want true/false", details.Additional[0].Allowed, details.Additional[0].LimitReached)
	}
}

func TestServiceGetCodexUsage_UsesRegisteredProviderClient(t *testing.T) {
	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	svc := NewService(dir,
		WithNowFunc(func() time.Time { return now }),
		WithProviderClient(&stubCodexUsageProvider{
			usage: &CodexUsageDetails{
				PlanType: "pro",
				Primary: &CodexUsageWindow{
					UsedPercent:   12,
					WindowMinutes: 60,
					ResetsAt:      now.Add(30 * time.Minute),
				},
			},
		}),
	)
	if err := svc.Store().Save(&Credential{
		Ref:         "codex-sean-example-com",
		Provider:    config.OAuthProviderCodex,
		Email:       "sean@example.com",
		AccountID:   "account-123",
		AccessToken: "access-1",
		ExpiresAt:   now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	details, err := svc.GetCodexUsage(t.Context(), "codex-sean-example-com")
	if err != nil {
		t.Fatalf("GetCodexUsage: %v", err)
	}
	if details == nil || details.Primary == nil {
		t.Fatalf("details = %#v, want primary usage window", details)
	}
	if got := details.Primary.UsedPercent; got != 12 {
		t.Fatalf("used_percent = %v, want 12", got)
	}
}
