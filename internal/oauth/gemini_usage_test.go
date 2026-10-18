package oauth

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PAIArtCom/Clipal/internal/config"
)

type stubGeminiUsageProvider struct {
	provider config.OAuthProvider
	usage    *GeminiUsageDetails
	usageErr error
	calls    int32
	refresh  func(*Credential) (*Credential, error)
}

func (c *stubGeminiUsageProvider) Provider() config.OAuthProvider {
	if c.provider != "" {
		return c.provider
	}
	return config.OAuthProviderAntigravity
}

func (c *stubGeminiUsageProvider) StartLogin(time.Time, time.Duration) (*LoginSession, error) {
	return nil, fmt.Errorf("not implemented")
}

func (c *stubGeminiUsageProvider) ExchangeSessionCode(context.Context, *LoginSession, string) (*Credential, error) {
	return nil, fmt.Errorf("not implemented")
}

func (c *stubGeminiUsageProvider) Refresh(_ context.Context, cred *Credential) (*Credential, error) {
	if c.refresh != nil {
		return c.refresh(cred)
	}
	return cred.Clone(), nil
}

func (c *stubGeminiUsageProvider) FetchUsage(context.Context, *Credential) (*GeminiUsageDetails, error) {
	atomic.AddInt32(&c.calls, 1)
	if c.usage == nil {
		return nil, c.usageErr
	}
	return c.usage, c.usageErr
}

func TestAntigravityFetchUsage_UsesDailyCloudCodeQuotaEndpoint(t *testing.T) {
	t.Setenv("CLIPAL_OAUTH_ANTIGRAVITY_VERSION", "9.8.7")
	resetTime := "2026-05-07T12:15:00Z"

	cloudCodeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Method; got != http.MethodPost {
			t.Fatalf("method = %q, want POST", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer access-1" {
			t.Fatalf("authorization = %q, want Bearer access-1", got)
		}
		if got := r.Header.Get("User-Agent"); got != antigravityUserAgent() {
			t.Fatalf("user-agent = %q, want %q", got, antigravityUserAgent())
		}
		if got := r.Header.Get("X-Goog-Api-Client"); got != "" {
			t.Fatalf("x-goog-api-client = %q, want empty", got)
		}
		if got := r.Header.Get("Client-Metadata"); got != "" {
			t.Fatalf("client-metadata = %q, want empty", got)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("io.ReadAll: %v", err)
		}
		if got := string(body); got != `{"project":"project-123"}` {
			t.Fatalf("body = %q", got)
		}

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1internal:retrieveUserQuota":
			_, _ = io.WriteString(w, `{
				"buckets": [
					{
						"modelId": "gemini-3.1-pro",
						"tokenType": "GOOGLE_ONE_AI",
						"remainingAmount": "3",
						"remainingFraction": 0.25,
						"resetTime": "`+resetTime+`"
					}
				]
			}`)
		case "/v1internal:retrieveUserQuotaSummary":
			_, _ = io.WriteString(w, `{
				"groups": [
					{
						"displayName": "Gemini Models",
						"description": "Models within this group: Gemini Pro",
						"buckets": [
							{
								"bucketId": "gemini-models:pro",
								"displayName": "Gemini Pro",
								"remainingFraction": 0.25,
								"resetTime": "`+resetTime+`"
							}
						]
					}
				]
			}`)
		default:
			t.Fatalf("path = %q, want retrieveUserQuota or retrieveUserQuotaSummary", r.URL.Path)
		}
	}))
	defer cloudCodeServer.Close()

	client := &AntigravityClient{
		DailyCloudCodeURL: cloudCodeServer.URL,
		CloudCodeVersion:  "v1internal",
		HTTPClient:        cloudCodeServer.Client(),
	}

	details, err := client.FetchUsage(t.Context(), &Credential{
		Ref:         "antigravity-sean-example-com-project-123",
		Provider:    config.OAuthProviderAntigravity,
		AccessToken: "access-1",
		AccountID:   "project-123",
		Metadata: map[string]string{
			"project_id": "project-123",
		},
	})
	if err != nil {
		t.Fatalf("FetchUsage: %v", err)
	}
	if details == nil || len(details.Buckets) != 1 {
		t.Fatalf("details = %#v, want 1 bucket", details)
	}
	bucket := details.Buckets[0]
	if got := bucket.ModelID; got != "gemini-3.1-pro" {
		t.Fatalf("model_id = %q, want gemini-3.1-pro", got)
	}
	if got := bucket.TokenType; got != "GOOGLE_ONE_AI" {
		t.Fatalf("token_type = %q, want GOOGLE_ONE_AI", got)
	}
	if bucket.RemainingFraction == nil || *bucket.RemainingFraction != 0.25 {
		t.Fatalf("remaining_fraction = %#v, want 0.25", bucket.RemainingFraction)
	}
	if len(details.Groups) != 1 || len(details.Groups[0].Buckets) != 1 {
		t.Fatalf("groups = %#v, want one summary bucket", details.Groups)
	}
	if got := details.Groups[0].Buckets[0].BucketID; got != "gemini-models:pro" {
		t.Fatalf("summary bucket id = %q, want gemini-models:pro", got)
	}
}

func TestAntigravityFetchUsage_FallsBackToExplicitModelQuotas(t *testing.T) {
	const catalog = `{"models":{
		"gemini-pro":{"quotaInfo":{"remainingFraction":1}},
		"gemini-flash":{"quotaInfo":{"remainingFraction":0,"resetTime":"2026-10-18T12:00:00Z"}},
		"no-quota":{},
		"unknown":{"quotaInfo":{"resetTime":"2026-10-18T12:00:00Z"}},
		"invalid":{"quotaInfo":{"remainingFraction":"unknown"}}
	}}`
	for _, tc := range []struct {
		name        string
		quotaStatus int
		modelStatus int
		catalog     string
		wantErr     bool
		wantBuckets int
	}{
		{"empty quota", 200, 200, catalog, false, 2},
		{"unsupported quota", 404, 200, catalog, false, 2},
		{"unsupported method", 405, 200, catalog, false, 2},
		{"no reported model quotas", 200, 200, `{"models":{"no-quota":{}}}`, false, 0},
		{"catalog unavailable", 200, 503, `{}`, true, 0},
		{"malformed catalog", 200, 200, `{"models":`, true, 0},
		{"invalid catalog shape", 200, 200, `{"models":[]}`, true, 0},
		{"authorization failure", 403, 200, catalog, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			modelCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer access-1" || r.Header.Get("User-Agent") != antigravityUserAgent() {
					t.Errorf("unexpected request identity")
				}
				body, _ := io.ReadAll(r.Body)
				if string(body) != `{"project":"project-123"}` {
					t.Errorf("request body = %s", body)
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v1internal:retrieveUserQuota":
					w.WriteHeader(tc.quotaStatus)
					_, _ = io.WriteString(w, `{"buckets":[]}`)
				case "/v1internal:retrieveUserQuotaSummary":
					w.WriteHeader(http.StatusNotFound)
				case "/v1internal:fetchAvailableModels":
					modelCalls++
					w.WriteHeader(tc.modelStatus)
					_, _ = io.WriteString(w, tc.catalog)
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			client := &AntigravityClient{DailyCloudCodeURL: server.URL, HTTPClient: server.Client(), Sleep: func(time.Duration) {}}
			details, err := client.FetchUsage(t.Context(), &Credential{AccessToken: "access-1", AccountID: "project-123"})
			if (err != nil) != tc.wantErr {
				t.Fatalf("FetchUsage error = %v, want error=%t", err, tc.wantErr)
			}
			if tc.quotaStatus == 403 && modelCalls != 0 {
				t.Fatal("authorization failures must not trigger catalog fallback")
			}
			if err != nil {
				return
			}
			if len(details.Buckets) != tc.wantBuckets {
				t.Fatalf("buckets = %#v, want %d", details.Buckets, tc.wantBuckets)
			}
			if tc.wantBuckets == 2 {
				if details.Buckets[0].ModelID != "gemini-flash" || *details.Buckets[0].RemainingFraction != 0 || details.Buckets[0].ResetTime.IsZero() ||
					details.Buckets[1].ModelID != "gemini-pro" || *details.Buckets[1].RemainingFraction != 1 {
					t.Fatalf("explicit quota fractions or deterministic model ordering lost: %#v", details.Buckets)
				}
			}
		})
	}
}

func TestServiceGetGeminiUsage_RepairsMissingProjectBeforeQuotaFetch(t *testing.T) {
	for _, repairFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("repairFails=%t", repairFails), func(t *testing.T) {
			now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
			refreshes := 0
			provider := &stubGeminiUsageProvider{
				provider: config.OAuthProviderAntigravity,
				usage:    &GeminiUsageDetails{Buckets: []GeminiUsageBucket{{ModelID: "gemini-pro"}}},
				refresh: func(cred *Credential) (*Credential, error) {
					refreshes++
					if repairFails {
						return nil, fmt.Errorf("account setup failed")
					}
					out := cred.Clone()
					out.AccountID = "project-123"
					out.Metadata = map[string]string{"project_id": "project-123"}
					return out, nil
				},
			}
			svc := NewService(t.TempDir(), WithNowFunc(func() time.Time { return now }))
			svc.registerProviderClient(provider)
			cred := &Credential{Provider: config.OAuthProviderAntigravity, Ref: "legacy-account", AccessToken: "access-1", ExpiresAt: now.Add(time.Hour)}
			if err := svc.Store().Save(cred); err != nil {
				t.Fatal(err)
			}
			details, err := svc.GetGeminiUsageForProviderWithHTTPClient(t.Context(), cred.Provider, cred.Ref, nil)
			if refreshes != 1 || (err != nil) != repairFails {
				t.Fatalf("refreshes=%d error=%v, want one setup attempt and error=%t", refreshes, err, repairFails)
			}
			if repairFails {
				if provider.calls != 0 {
					t.Fatal("failed setup must not fetch quota")
				}
			} else if details == nil || len(details.Buckets) != 1 || provider.calls != 1 {
				t.Fatalf("quota was not fetched after project setup: %#v", details)
			}
		})
	}
}

func TestServiceGetGeminiUsage_UsesRegisteredProviderClient(t *testing.T) {
	now := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	provider := &stubGeminiUsageProvider{
		usage: &GeminiUsageDetails{
			Buckets: []GeminiUsageBucket{
				{
					ModelID:   "gemini-2.5-pro",
					TokenType: "REQUESTS",
					ResetTime: now.Add(5 * time.Hour),
				},
			},
		},
	}
	svc := NewService(dir,
		WithNowFunc(func() time.Time { return now }),
		WithProviderClient(provider),
	)
	if err := svc.Store().Save(&Credential{
		Ref:         "gemini-sean-example-com-project-123",
		Provider:    config.OAuthProviderAntigravity,
		Email:       "sean@example.com",
		AccountID:   "project-123",
		AccessToken: "access-1",
		ExpiresAt:   now.Add(time.Hour),
		Metadata: map[string]string{
			"project_id": "project-123",
		},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	details, err := svc.GetGeminiUsageForProviderWithHTTPClient(t.Context(), config.OAuthProviderAntigravity, "gemini-sean-example-com-project-123", nil)
	if err != nil {
		t.Fatalf("GetGeminiUsage: %v", err)
	}
	if details == nil || len(details.Buckets) != 1 {
		t.Fatalf("details = %#v, want 1 bucket", details)
	}
	if got := details.Buckets[0].ModelID; got != "gemini-2.5-pro" {
		t.Fatalf("model_id = %q, want gemini-2.5-pro", got)
	}
	if got := atomic.LoadInt32(&provider.calls); got != 1 {
		t.Fatalf("fetch calls = %d, want 1", got)
	}
}

func TestServiceGetGeminiUsageForProvider_UsesAntigravityProviderClient(t *testing.T) {
	now := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	provider := &stubGeminiUsageProvider{
		provider: config.OAuthProviderAntigravity,
		usage: &GeminiUsageDetails{
			Buckets: []GeminiUsageBucket{
				{
					ModelID:   "gemini-3.1-pro",
					TokenType: "GOOGLE_ONE_AI",
					ResetTime: now.Add(5 * time.Hour),
				},
			},
		},
	}
	svc := NewService(dir,
		WithNowFunc(func() time.Time { return now }),
		WithProviderClient(provider),
	)
	if err := svc.Store().Save(&Credential{
		Ref:         "antigravity-sean-example-com-project-123",
		Provider:    config.OAuthProviderAntigravity,
		Email:       "sean@example.com",
		AccountID:   "project-123",
		AccessToken: "access-1",
		ExpiresAt:   now.Add(time.Hour),
		Metadata: map[string]string{
			"project_id": "project-123",
		},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	details, err := svc.GetGeminiUsageForProviderWithHTTPClient(t.Context(), config.OAuthProviderAntigravity, "antigravity-sean-example-com-project-123", nil)
	if err != nil {
		t.Fatalf("GetGeminiUsageForProviderWithHTTPClient: %v", err)
	}
	if details == nil || len(details.Buckets) != 1 {
		t.Fatalf("details = %#v, want 1 bucket", details)
	}
	if got := details.Buckets[0].ModelID; got != "gemini-3.1-pro" {
		t.Fatalf("model_id = %q, want gemini-3.1-pro", got)
	}
	if got := atomic.LoadInt32(&provider.calls); got != 1 {
		t.Fatalf("fetch calls = %d, want 1", got)
	}
}

func TestServiceGetGeminiUsageForProvider_CleansUpUnsupportedProviderCall(t *testing.T) {
	now := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	svc := NewService(dir, WithNowFunc(func() time.Time { return now }))
	provider := config.OAuthProvider("unsupported")
	if err := svc.Store().Save(&Credential{
		Ref:         "unsupported-sean-example-com-project-123",
		Provider:    provider,
		Email:       "sean@example.com",
		AccountID:   "project-123",
		AccessToken: "access-1",
		ExpiresAt:   now.Add(time.Hour),
		Metadata: map[string]string{
			"project_id": "project-123",
		},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	_, err := svc.GetGeminiUsageForProviderWithHTTPClient(t.Context(), provider, "unsupported-sean-example-com-project-123", nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported oauth provider") {
		t.Fatalf("GetGeminiUsageForProviderWithHTTPClient error = %v, want unsupported provider", err)
	}
	svc.mu.Lock()
	pending := len(svc.geminiUsageCalls)
	svc.mu.Unlock()
	if pending != 0 {
		t.Fatalf("pending gemini usage calls = %d, want 0", pending)
	}
}

func TestServiceGetGeminiUsage_CachesSuccessfulFetches(t *testing.T) {
	now := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	provider := &stubGeminiUsageProvider{
		usage: &GeminiUsageDetails{
			Buckets: []GeminiUsageBucket{
				{
					ModelID:   "gemini-2.5-pro",
					TokenType: "REQUESTS",
					ResetTime: now.Add(5 * time.Hour),
				},
			},
		},
	}
	svc := NewService(dir,
		WithNowFunc(func() time.Time { return now }),
		WithGeminiUsageTTL(30*time.Second),
		WithProviderClient(provider),
	)
	if err := svc.Store().Save(&Credential{
		Ref:         "gemini-sean-example-com-project-123",
		Provider:    config.OAuthProviderAntigravity,
		Email:       "sean@example.com",
		AccountID:   "project-123",
		AccessToken: "access-1",
		ExpiresAt:   now.Add(time.Hour),
		Metadata: map[string]string{
			"project_id": "project-123",
		},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	first, err := svc.GetGeminiUsageForProviderWithHTTPClient(t.Context(), config.OAuthProviderAntigravity, "gemini-sean-example-com-project-123", nil)
	if err != nil {
		t.Fatalf("first GetGeminiUsage: %v", err)
	}
	second, err := svc.GetGeminiUsageForProviderWithHTTPClient(t.Context(), config.OAuthProviderAntigravity, "gemini-sean-example-com-project-123", nil)
	if err != nil {
		t.Fatalf("second GetGeminiUsage: %v", err)
	}
	if first == nil || second == nil {
		t.Fatalf("expected non-nil details: first=%#v second=%#v", first, second)
	}
	if got := atomic.LoadInt32(&provider.calls); got != 1 {
		t.Fatalf("fetch calls = %d, want 1", got)
	}
}

func TestServiceRefreshInvalidatesAntigravityUsageCache(t *testing.T) {
	now := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	provider := &stubGeminiUsageProvider{
		provider: config.OAuthProviderAntigravity,
		usage: &GeminiUsageDetails{
			Buckets: []GeminiUsageBucket{
				{ModelID: "gemini-3.1-pro", ResetTime: now.Add(time.Hour)},
			},
		},
	}
	svc := NewService(dir,
		WithNowFunc(func() time.Time { return now }),
		WithGeminiUsageTTL(time.Hour),
		WithProviderClient(provider),
	)
	if err := svc.Store().Save(&Credential{
		Ref:          "antigravity-sean-example-com-project-123",
		Provider:     config.OAuthProviderAntigravity,
		Email:        "sean@example.com",
		AccountID:    "project-123",
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		ExpiresAt:    now.Add(time.Hour),
		Metadata: map[string]string{
			"project_id": "project-123",
		},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := svc.GetGeminiUsageForProviderWithHTTPClient(t.Context(), config.OAuthProviderAntigravity, "antigravity-sean-example-com-project-123", nil); err != nil {
		t.Fatalf("first usage fetch: %v", err)
	}
	if got := atomic.LoadInt32(&provider.calls); got != 1 {
		t.Fatalf("fetch calls after first usage = %d, want 1", got)
	}
	if _, err := svc.RefreshWithHTTPClient(t.Context(), config.OAuthProviderAntigravity, "antigravity-sean-example-com-project-123", nil); err != nil {
		t.Fatalf("RefreshWithHTTPClient: %v", err)
	}
	if _, err := svc.GetGeminiUsageForProviderWithHTTPClient(t.Context(), config.OAuthProviderAntigravity, "antigravity-sean-example-com-project-123", nil); err != nil {
		t.Fatalf("second usage fetch: %v", err)
	}
	if got := atomic.LoadInt32(&provider.calls); got != 2 {
		t.Fatalf("fetch calls after refresh = %d, want 2", got)
	}
}

func TestServiceGetGeminiUsage_RefreshesCacheAfterTTL(t *testing.T) {
	now := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	provider := &stubGeminiUsageProvider{
		usage: &GeminiUsageDetails{
			Buckets: []GeminiUsageBucket{
				{
					ModelID:   "gemini-2.5-pro",
					TokenType: "REQUESTS",
					ResetTime: now.Add(5 * time.Hour),
				},
			},
		},
	}
	svc := NewService(dir,
		WithNowFunc(func() time.Time { return now }),
		WithGeminiUsageTTL(30*time.Second),
		WithProviderClient(provider),
	)
	if err := svc.Store().Save(&Credential{
		Ref:         "gemini-sean-example-com-project-123",
		Provider:    config.OAuthProviderAntigravity,
		Email:       "sean@example.com",
		AccountID:   "project-123",
		AccessToken: "access-1",
		ExpiresAt:   now.Add(time.Hour),
		Metadata: map[string]string{
			"project_id": "project-123",
		},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := svc.GetGeminiUsageForProviderWithHTTPClient(t.Context(), config.OAuthProviderAntigravity, "gemini-sean-example-com-project-123", nil); err != nil {
		t.Fatalf("first GetGeminiUsage: %v", err)
	}
	now = now.Add(31 * time.Second)
	if _, err := svc.GetGeminiUsageForProviderWithHTTPClient(t.Context(), config.OAuthProviderAntigravity, "gemini-sean-example-com-project-123", nil); err != nil {
		t.Fatalf("second GetGeminiUsage: %v", err)
	}
	if got := atomic.LoadInt32(&provider.calls); got != 2 {
		t.Fatalf("fetch calls = %d, want 2", got)
	}
}
