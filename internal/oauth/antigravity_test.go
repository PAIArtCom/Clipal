package oauth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/PAIArtCom/Clipal/internal/config"
)

const testAntigravityRedirectURI = "http://127.0.0.1:9876/oauth2callback"

func TestAntigravityFallbackVersionMatchesCurrentRelease(t *testing.T) {
	if defaultAntigravityVersionLabel != "2.13.0" {
		t.Fatalf("defaultAntigravityVersionLabel = %q, want 2.13.0", defaultAntigravityVersionLabel)
	}
}

func TestAntigravityGenerateAuthURLUsesAntigravityClientAndScopes(t *testing.T) {
	client := &AntigravityClient{
		AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
		ClientID: "antigravity-client",
		Scopes: []string{
			defaultGoogleScopeCloudPlatform,
			defaultGoogleScopeUserInfoEmail,
			defaultAntigravityScopeCCLog,
			defaultAntigravityScopeExperiments,
		},
	}
	pkce := PKCECodes{CodeVerifier: "verifier-123", CodeChallenge: "challenge-123"}

	authURL, err := client.GenerateAuthURL("state-123", testAntigravityRedirectURI, pkce)
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

func TestExtractAntigravityTierMetadataDefaultsToFreeTier(t *testing.T) {
	if got := extractAntigravityTierMetadata(map[string]any{}).TierID; got != defaultAntigravityFreeTierID {
		t.Fatalf("tier = %q, want %q", got, defaultAntigravityFreeTierID)
	}
	if got := extractAntigravityTierMetadata(map[string]any{
		"allowedTiers": []any{
			map[string]any{"id": "standard-tier", "isDefault": true},
		},
	}).TierID; got != "standard-tier" {
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

	metadata := client.metadataFromToken(&googleTokenResponse{
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

func TestAntigravityCredentialFromTokenRequiresUsableProject(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("GOOGLE_CLOUD_PROJECT_ID", "")
	for _, tc := range []struct {
		name, load, onboard, requested, wantProject, wantError string
		operations                                             []string
		pending                                                []string
		camel                                                  bool
	}{
		{name: "assigned", load: `{"cloudaicompanionProject":"project-123"}`, wantProject: "project-123"},
		{name: "onboarded", load: `{}`, onboard: `{"done":true,"response":{"cloudaicompanionProject":{"id":"project-123"}}}`, wantProject: "project-123"},
		{name: "unnamed pending camel compatibility", load: `{}`, onboard: `{"done":false}`, pending: []string{`{"done":true,"response":{"cloudaicompanionProject":"project-123"}}`}, camel: true, wantProject: "project-123"},
		{name: "empty completion", load: `{}`, onboard: `{"done":true,"response":{}}`, wantError: "project_id"},
		{name: "ineligible location", load: `{"ineligibleTiers":[{"tierId":"free-tier","reasonCode":"UNSUPPORTED_LOCATION","reasonMessage":"Not available in your location."}]}`, wantError: "UNSUPPORTED_LOCATION"},
		{name: "other tier ineligible", load: `{"cloudaicompanionProject":"project-123","ineligibleTiers":[{"reasonCode":"UNSUPPORTED_LOCATION"}]}`, wantProject: "project-123"},
		{name: "standard tier needs supplied project", load: `{"allowedTiers":[{"id":"standard-tier","isDefault":true,"userDefinedCloudaicompanionProject":true}],"ineligibleTiers":[{"tierId":"free-tier","reasonCode":"UNSUPPORTED_LOCATION"}]}`, wantError: "requires a user-provided Google Cloud project"},
		{name: "supplied standard project", load: `{"allowedTiers":[{"id":"standard-tier","isDefault":true,"userDefinedCloudaicompanionProject":true}]}`, requested: "configured-project", wantProject: "configured-project"},
		{name: "another tier restriction does not block onboarding", load: `{"allowedTiers":[{"id":"free-tier","isDefault":true}],"ineligibleTiers":[{"tierId":"g1-pro-tier","reasonCode":"SUBSCRIPTION_REQUIRED"}]}`, onboard: `{"done":true,"response":{"cloudaicompanionProject":"project-123"}}`, wantProject: "project-123"},
		{name: "poll named operation", load: `{}`, onboard: `{"name":"operations/test-123","done":false}`, operations: []string{`{"name":"operations/test-123","done":false}`, `{"name":"operations/test-123","done":true,"response":{"cloudaicompanionProject":"project-123"}}`}, wantProject: "project-123"},
		{name: "failed operation", load: `{}`, onboard: `{"name":"operations/test-123","done":false}`, operations: []string{`{"done":true,"error":{"code":7,"message":"permission denied"},"response":{"cloudaicompanionProject":"must-not-save"}}`}, wantError: "permission denied"},
		{name: "failed initial operation", load: `{}`, onboard: `{"done":true,"error":{"code":7,"message":"permission denied"}}`, wantError: "permission denied"},
		{name: "account validation", load: `{"ineligibleTiers":[{"tierId":"free-tier","reasonCode":"VALIDATION_REQUIRED"}]}`, wantError: "validation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			onboardCalls, operationCalls, compatibilityCalls := 0, 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/userinfo":
					_, _ = io.WriteString(w, `{"email":"account@example.com"}`)
				case "/v1internal:loadCodeAssist":
					_, _ = io.WriteString(w, tc.load)
				case "/v1internal:onboardUser":
					var payload map[string]any
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Error(err)
					}
					if tc.camel && payload["tierId"] == nil {
						compatibilityCalls++
						w.WriteHeader(http.StatusBadRequest)
						_, _ = io.WriteString(w, `{"error":"use tierId"}`)
						return
					}
					onboardCalls++
					if tc.onboard == "" {
						t.Error("unexpected onboarding for supplied project or deterministic setup requirement")
					}
					if onboardCalls > 1 && onboardCalls-2 < len(tc.pending) {
						_, _ = io.WriteString(w, tc.pending[onboardCalls-2])
					} else {
						_, _ = io.WriteString(w, tc.onboard)
					}
				case "/v1internal/operations/test-123":
					if r.Method != http.MethodGet || r.Header.Get("User-Agent") != antigravityUserAgent() {
						t.Errorf("operation method/identity: %s %s", r.Method, r.Header.Get("User-Agent"))
					}
					if operationCalls >= len(tc.operations) {
						t.Error("unexpected operation retry")
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					_, _ = io.WriteString(w, tc.operations[operationCalls])
					operationCalls++
				default:
					t.Errorf("unexpected path %q", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			client := &AntigravityClient{UserInfoURL: server.URL + "/userinfo", CloudCodeURL: server.URL, DailyCloudCodeURL: server.URL, ProjectID: tc.requested, HTTPClient: server.Client(), Sleep: func(time.Duration) {}}
			cred, err := client.credentialFromToken(context.Background(), &googleTokenResponse{AccessToken: "access", RefreshToken: "refresh", ExpiresIn: 3600}, nil)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) || cred != nil {
					t.Fatalf("credential present=%v error=%v, want %q", cred != nil, err, tc.wantError)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if cred.AccountID != tc.wantProject || cred.Metadata["project_id"] != tc.wantProject {
					t.Fatalf("project not populated: account=%q metadata=%q", cred.AccountID, cred.Metadata["project_id"])
				}
			}
			wantOnboard := 0
			if tc.onboard != "" {
				wantOnboard = 1 + len(tc.pending)
			}
			if tc.camel && compatibilityCalls != 1 {
				t.Errorf("compatibility retries=%d, want 1", compatibilityCalls)
			}
			if onboardCalls != wantOnboard || operationCalls != len(tc.operations) {
				t.Fatalf("onboard=%d operations=%d", onboardCalls, operationCalls)
			}
		})
	}
}
