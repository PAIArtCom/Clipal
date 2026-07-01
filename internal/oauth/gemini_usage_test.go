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

	"github.com/lansespirit/Clipal/internal/config"
)

type stubGeminiUsageProvider struct {
	provider config.OAuthProvider
	usage    *GeminiUsageDetails
	usageErr error
	calls    int32
}

func (c *stubGeminiUsageProvider) Provider() config.OAuthProvider {
	if c.provider != "" {
		return c.provider
	}
	return config.OAuthProviderGemini
}

func (c *stubGeminiUsageProvider) StartLogin(time.Time, time.Duration) (*LoginSession, error) {
	return nil, fmt.Errorf("not implemented")
}

func (c *stubGeminiUsageProvider) ExchangeSessionCode(context.Context, *LoginSession, string) (*Credential, error) {
	return nil, fmt.Errorf("not implemented")
}

func (c *stubGeminiUsageProvider) Refresh(_ context.Context, cred *Credential) (*Credential, error) {
	return cred.Clone(), nil
}

func (c *stubGeminiUsageProvider) FetchUsage(context.Context, *Credential) (*GeminiUsageDetails, error) {
	atomic.AddInt32(&c.calls, 1)
	if c.usage == nil {
		return nil, c.usageErr
	}
	return c.usage, c.usageErr
}

func TestGeminiFetchUsage_ParsesQuotaBuckets(t *testing.T) {
	resetTime := "2026-05-07T12:15:00Z"

	cloudCodeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Method; got != http.MethodPost {
			t.Fatalf("method = %q, want POST", got)
		}
		if got := r.URL.Path; got != "/v1internal:retrieveUserQuota" {
			t.Fatalf("path = %q, want /v1internal:retrieveUserQuota", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer access-1" {
			t.Fatalf("authorization = %q, want Bearer access-1", got)
		}
		if got := r.Header.Get("User-Agent"); got != geminiCloudCodeUserAgent(defaultGeminiCloudCodeUserAgentModel) {
			t.Fatalf("user-agent = %q", got)
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
		_, _ = io.WriteString(w, `{
			"buckets": [
				{
					"modelId": "gemini-2.5-pro",
					"tokenType": "REQUESTS",
					"remainingAmount": "0",
					"remainingFraction": 0.03,
					"resetTime": "`+resetTime+`"
				}
			]
		}`)
	}))
	defer cloudCodeServer.Close()

	client := &GeminiClient{
		CloudCodeURL:     cloudCodeServer.URL,
		CloudCodeVersion: "v1internal",
		HTTPClient:       cloudCodeServer.Client(),
	}

	details, err := client.FetchUsage(t.Context(), &Credential{
		Ref:         "gemini-sean-example-com-project-123",
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
	if got := bucket.ModelID; got != "gemini-2.5-pro" {
		t.Fatalf("model_id = %q, want gemini-2.5-pro", got)
	}
	if got := bucket.TokenType; got != "REQUESTS" {
		t.Fatalf("token_type = %q, want REQUESTS", got)
	}
	if bucket.RemainingAmount == nil || *bucket.RemainingAmount != 0 {
		t.Fatalf("remaining_amount = %#v, want 0", bucket.RemainingAmount)
	}
	if bucket.RemainingFraction == nil || *bucket.RemainingFraction != 0.03 {
		t.Fatalf("remaining_fraction = %#v, want 0.03", bucket.RemainingFraction)
	}
	wantReset, _ := time.Parse(time.RFC3339, resetTime)
	if !bucket.ResetTime.Equal(wantReset) {
		t.Fatalf("reset_time = %s, want %s", bucket.ResetTime.Format(time.RFC3339), wantReset.Format(time.RFC3339))
	}
}

func TestGeminiFetchUsageRetriesTransientCloudCodeErrors(t *testing.T) {
	var calls int32
	var sleeps []time.Duration
	cloudCodeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := atomic.AddInt32(&calls, 1)
		if call == 1 {
			w.Header().Set("Retry-After", "1")
			http.Error(w, `{"error":{"status":"RESOURCE_EXHAUSTED"}}`, http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"buckets":[]}`)
	}))
	defer cloudCodeServer.Close()

	client := &GeminiClient{
		CloudCodeURL: cloudCodeServer.URL,
		HTTPClient:   cloudCodeServer.Client(),
		Sleep: func(d time.Duration) {
			sleeps = append(sleeps, d)
		},
	}

	_, err := client.FetchUsage(t.Context(), &Credential{
		Ref:         "gemini-sean-example-com-project-123",
		AccessToken: "access-1",
		AccountID:   "project-123",
		Metadata: map[string]string{
			"project_id": "project-123",
		},
	})
	if err != nil {
		t.Fatalf("FetchUsage: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("calls = %d, want 2", got)
	}
	if len(sleeps) != 1 || sleeps[0] != time.Second {
		t.Fatalf("sleeps = %#v, want [1s]", sleeps)
	}
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
		Provider:    config.OAuthProviderGemini,
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

	details, err := svc.GetGeminiUsage(t.Context(), "gemini-sean-example-com-project-123")
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
		Provider:    config.OAuthProviderGemini,
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

	first, err := svc.GetGeminiUsage(t.Context(), "gemini-sean-example-com-project-123")
	if err != nil {
		t.Fatalf("first GetGeminiUsage: %v", err)
	}
	second, err := svc.GetGeminiUsage(t.Context(), "gemini-sean-example-com-project-123")
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
		Provider:    config.OAuthProviderGemini,
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

	if _, err := svc.GetGeminiUsage(t.Context(), "gemini-sean-example-com-project-123"); err != nil {
		t.Fatalf("first GetGeminiUsage: %v", err)
	}
	now = now.Add(31 * time.Second)
	if _, err := svc.GetGeminiUsage(t.Context(), "gemini-sean-example-com-project-123"); err != nil {
		t.Fatalf("second GetGeminiUsage: %v", err)
	}
	if got := atomic.LoadInt32(&provider.calls); got != 2 {
		t.Fatalf("fetch calls = %d, want 2", got)
	}
}
