package oauth

import (
	"net/url"
	"strings"
	"testing"
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

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
