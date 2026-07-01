package oauth

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
)

func TestAntigravityGenerateAuthURLUsesAntigravityClientAndScopes(t *testing.T) {
	client := &AntigravityClient{
		AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
		ClientID: "antigravity-client",
		Scopes: []string{
			defaultGeminiScopeCloudPlatform,
			defaultGeminiScopeUserInfoEmail,
			defaultAntigravityScopeCCLog,
			defaultAntigravityScopeExperiments,
		},
	}
	pkce := PKCECodes{CodeVerifier: "verifier-123", CodeChallenge: "challenge-123"}

	authURL, err := client.GenerateAuthURL("state-123", testGeminiRedirectURI, pkce)
	if err != nil {
		t.Fatalf("GenerateAuthURL: %v", err)
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	query := parsed.Query()
	if got := query.Get("client_id"); got != "antigravity-client" {
		t.Fatalf("client_id = %q", got)
	}
	scopes := strings.Fields(query.Get("scope"))
	for _, want := range []string{defaultAntigravityScopeCCLog, defaultAntigravityScopeExperiments} {
		if !containsString(scopes, want) {
			t.Fatalf("scope %q missing from %q", want, query.Get("scope"))
		}
	}
	if got := query.Get("code_challenge_method"); got != "S256" {
		t.Fatalf("code_challenge_method = %q", got)
	}
}

func TestNewAntigravityClientAppliesEnvironmentOverrides(t *testing.T) {
	t.Setenv("CLIPAL_OAUTH_ANTIGRAVITY_AUTH_URL", "http://127.0.0.1/auth")
	t.Setenv("CLIPAL_OAUTH_ANTIGRAVITY_TOKEN_URL", "http://127.0.0.1/token")
	t.Setenv("CLIPAL_OAUTH_ANTIGRAVITY_USERINFO_URL", "http://127.0.0.1/userinfo")
	t.Setenv("CLIPAL_OAUTH_ANTIGRAVITY_CLOUD_CODE_URL", "http://127.0.0.1/cloudcode")
	t.Setenv("CLIPAL_OAUTH_ANTIGRAVITY_DAILY_CLOUD_CODE_URL", "http://127.0.0.1/daily")
	t.Setenv("CLIPAL_OAUTH_ANTIGRAVITY_CLIENT_ID", "client")
	t.Setenv("CLIPAL_OAUTH_ANTIGRAVITY_CLIENT_SECRET", "secret")
	t.Setenv("CLIPAL_OAUTH_ANTIGRAVITY_PROJECT_ID", "project-1")
	t.Setenv("CLIPAL_OAUTH_ANTIGRAVITY_SCOPE", "scope-a scope-b")

	client := NewAntigravityClient()

	if got := client.authURL(); got != "http://127.0.0.1/auth" {
		t.Fatalf("authURL = %q", got)
	}
	if got := client.tokenURL(); got != "http://127.0.0.1/token" {
		t.Fatalf("tokenURL = %q", got)
	}
	if got := client.userInfoURL(); got != "http://127.0.0.1/userinfo" {
		t.Fatalf("userInfoURL = %q", got)
	}
	if got := client.cloudCodeURL(); got != "http://127.0.0.1/cloudcode" {
		t.Fatalf("cloudCodeURL = %q", got)
	}
	if got := client.dailyCloudCodeURL(); got != "http://127.0.0.1/daily" {
		t.Fatalf("dailyCloudCodeURL = %q", got)
	}
	if got := client.clientID(); got != "client" {
		t.Fatalf("clientID = %q", got)
	}
	if got := client.clientSecret(); got != "secret" {
		t.Fatalf("clientSecret = %q", got)
	}
	if got := client.requestedProjectID(nil); got != "project-1" {
		t.Fatalf("projectID = %q", got)
	}
	if got := strings.Join(client.scopes(), " "); got != "scope-a scope-b" {
		t.Fatalf("scopes = %q", got)
	}
}

func TestAntigravityClientMetadataMatchesInstalledProtocol(t *testing.T) {
	t.Setenv("CLIPAL_OAUTH_ANTIGRAVITY_VERSION", "9.8.7")

	metadata := antigravityClientMetadata()

	for key, want := range map[string]any{
		"ide_type":       "ANTIGRAVITY",
		"ide_version":    "9.8.7",
		"plugin_version": "9.8.7",
		"update_channel": "STABLE",
		"plugin_type":    "CLOUD_CODE",
		"ide_name":       "Antigravity",
	} {
		if got := metadata[key]; got != want {
			t.Fatalf("metadata[%q] = %v, want %v", key, got, want)
		}
	}
	if got := metadata["platform"]; got == "" || got == nil {
		t.Fatalf("metadata.platform = %v, want non-empty platform", got)
	}
	if got := antigravityUserAgent(); !strings.Contains(got, "9.8.7") {
		t.Fatalf("User-Agent = %q, want version override", got)
	}
}

func TestExtractAntigravityTierIDDefaultsToFreeTier(t *testing.T) {
	if got := extractAntigravityTierID(map[string]any{}); got != defaultAntigravityFreeTierID {
		t.Fatalf("tier = %q, want %q", got, defaultAntigravityFreeTierID)
	}
	if got := extractAntigravityTierID(map[string]any{
		"allowedTiers": []any{
			map[string]any{"id": "standard-tier", "isDefault": true},
		},
	}); got != "standard-tier" {
		t.Fatalf("tier = %q, want standard-tier", got)
	}
}

func TestExtractAntigravityTierMetadataPreservesPaidCurrentAndDefault(t *testing.T) {
	metadata := extractAntigravityTierMetadata(map[string]any{
		"currentTier": map[string]any{
			"id":   "free-tier",
			"name": "Antigravity",
		},
		"paidTier": map[string]any{
			"id":   "g1-pro-tier",
			"name": "Google AI Pro",
			"availableCredits": []any{
				map[string]any{
					"creditType":                  "GOOGLE_ONE_AI",
					"minimumCreditAmountForUsage": "50",
				},
			},
		},
		"allowedTiers": []any{
			map[string]any{"id": "free-tier", "name": "Antigravity", "isDefault": true},
			map[string]any{"id": "standard-tier", "name": "Antigravity"},
		},
	})

	for key, got := range map[string]string{
		"tier_id":                              metadata.TierID,
		"current_tier_id":                      metadata.CurrentTierID,
		"current_tier_name":                    metadata.CurrentTierName,
		"paid_tier_id":                         metadata.PaidTierID,
		"paid_tier_name":                       metadata.PaidTierName,
		"paid_credit_type":                     metadata.PaidCreditType,
		"paid_minimum_credit_amount_for_usage": metadata.PaidMinimumCreditAmountForUsage,
		"allowed_default_tier_id":              metadata.AllowedDefaultTierID,
		"allowed_default_tier_name":            metadata.AllowedDefaultTierName,
		"onboard_tier_id":                      metadata.onboardTierID(),
	} {
		want := map[string]string{
			"tier_id":                              "g1-pro-tier",
			"current_tier_id":                      "free-tier",
			"current_tier_name":                    "Antigravity",
			"paid_tier_id":                         "g1-pro-tier",
			"paid_tier_name":                       "Google AI Pro",
			"paid_credit_type":                     "GOOGLE_ONE_AI",
			"paid_minimum_credit_amount_for_usage": "50",
			"allowed_default_tier_id":              "free-tier",
			"allowed_default_tier_name":            "Antigravity",
			"onboard_tier_id":                      "free-tier",
		}[key]
		if got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestAntigravityMetadataFromTokenStoresPaidAndCurrentTiers(t *testing.T) {
	client := &AntigravityClient{
		ClientID:     "client-1",
		ClientSecret: "secret-1",
		TokenURL:     "https://oauth2.googleapis.com/token",
		Scopes:       []string{"scope-a", "scope-b"},
	}

	metadata := client.metadataFromToken(&geminiTokenResponse{
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		Scope:        "scope-a",
		TokenType:    "Bearer",
	}, "project-1", antigravityTierMetadata{
		TierID:                          "g1-pro-tier",
		CurrentTierID:                   "free-tier",
		CurrentTierName:                 "Antigravity",
		PaidTierID:                      "g1-pro-tier",
		PaidTierName:                    "Google AI Pro",
		PaidCreditType:                  "GOOGLE_ONE_AI",
		PaidMinimumCreditAmountForUsage: "50",
		AllowedDefaultTierID:            "free-tier",
		AllowedDefaultTierName:          "Antigravity",
	}, false)

	for key, want := range map[string]string{
		"tier_id":                              "g1-pro-tier",
		"current_tier_id":                      "free-tier",
		"current_tier_name":                    "Antigravity",
		"paid_tier_id":                         "g1-pro-tier",
		"paid_tier_name":                       "Google AI Pro",
		"paid_credit_type":                     "GOOGLE_ONE_AI",
		"paid_minimum_credit_amount_for_usage": "50",
		"allowed_default_tier_id":              "free-tier",
		"allowed_default_tier_name":            "Antigravity",
	} {
		if got := metadata[key]; got != want {
			t.Fatalf("metadata[%q] = %q, want %q", key, got, want)
		}
	}
}

func TestAntigravityRefreshDropsStalePaidTierMetadata(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}
			if got := r.Form.Get("grant_type"); got != "refresh_token" {
				t.Fatalf("grant_type = %q, want refresh_token", got)
			}
			_, _ = io.WriteString(w, `{"access_token":"access-2","refresh_token":"refresh-2","expires_in":3600,"scope":"scope-a","token_type":"Bearer"}`)
		case "/userinfo":
			_, _ = io.WriteString(w, `{"email":"sean@example.com"}`)
		case "/v1internal:loadCodeAssist":
			_, _ = io.WriteString(w, `{
  "cloudaicompanionProject": "project-123",
  "currentTier": {"id": "free-tier", "name": "Antigravity"},
  "allowedTiers": [
    {"id": "free-tier", "name": "Antigravity", "isDefault": true}
  ]
}`)
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	client := &AntigravityClient{
		TokenURL:         server.URL + "/token",
		UserInfoURL:      server.URL + "/userinfo",
		CloudCodeURL:     server.URL,
		CloudCodeVersion: "v1internal",
		ClientID:         "client-1",
		ClientSecret:     "secret-1",
		Scopes:           []string{"scope-a"},
		HTTPClient:       server.Client(),
		Now:              func() time.Time { return now },
	}
	refreshed, err := client.Refresh(context.Background(), &Credential{
		Ref:          "antigravity-sean-example-com-project-123",
		Provider:     config.OAuthProviderAntigravity,
		Email:        "sean@example.com",
		AccountID:    "project-123",
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		Metadata: map[string]string{
			"project_id":                           "project-123",
			"requested_project_id":                 "requested-project",
			"tier_id":                              "g1-pro-tier",
			"current_tier_id":                      "free-tier",
			"current_tier_name":                    "Antigravity",
			"paid_tier_id":                         "g1-pro-tier",
			"paid_tier_name":                       "Google AI Pro",
			"paid_credit_type":                     "GOOGLE_ONE_AI",
			"paid_minimum_credit_amount_for_usage": "50",
			"allowed_default_tier_id":              "free-tier",
			"allowed_default_tier_name":            "Antigravity",
		},
	})
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got := refreshed.Metadata["requested_project_id"]; got != "requested-project" {
		t.Fatalf("requested_project_id = %q, want requested-project", got)
	}
	for _, key := range []string{
		"paid_tier_id",
		"paid_tier_name",
		"paid_credit_type",
		"paid_minimum_credit_amount_for_usage",
	} {
		if got := refreshed.Metadata[key]; got != "" {
			t.Fatalf("metadata[%q] = %q, want empty", key, got)
		}
	}
	if got := refreshed.Metadata["tier_id"]; got != "free-tier" {
		t.Fatalf("tier_id = %q, want free-tier", got)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
