package oauth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testGeminiRedirectURI = "http://127.0.0.1:39393/oauth2callback"

func TestGeminiGenerateAuthURL(t *testing.T) {
	client := &GeminiClient{
		AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
		ClientID: "test-client",
		Scopes: []string{
			"https://www.googleapis.com/auth/cloud-platform",
			"https://www.googleapis.com/auth/userinfo.email",
		},
	}
	pkce := PKCECodes{
		CodeVerifier:  "verifier-123",
		CodeChallenge: "challenge-123",
	}

	authURL, err := client.GenerateAuthURL("state-123", testGeminiRedirectURI, pkce)
	if err != nil {
		t.Fatalf("GenerateAuthURL: %v", err)
	}

	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	if got := parsed.Scheme + "://" + parsed.Host + parsed.Path; got != "https://accounts.google.com/o/oauth2/v2/auth" {
		t.Fatalf("auth url = %q", got)
	}

	query := parsed.Query()
	if got := query.Get("access_type"); got != "offline" {
		t.Fatalf("access_type = %q, want offline", got)
	}
	if got := query.Get("client_id"); got != "test-client" {
		t.Fatalf("client_id = %q, want test-client", got)
	}
	if got := query.Get("redirect_uri"); got != testGeminiRedirectURI {
		t.Fatalf("redirect_uri = %q", got)
	}
	if got := query.Get("response_type"); got != "code" {
		t.Fatalf("response_type = %q, want code", got)
	}
	if got := query.Get("prompt"); got != "consent" {
		t.Fatalf("prompt = %q, want consent", got)
	}
	if got := query.Get("scope"); got != "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email" {
		t.Fatalf("scope = %q", got)
	}
	if got := query.Get("state"); got != "state-123" {
		t.Fatalf("state = %q, want state-123", got)
	}
	if got := query.Get("code_challenge"); got != "challenge-123" {
		t.Fatalf("code_challenge = %q, want challenge-123", got)
	}
	if got := query.Get("code_challenge_method"); got != "S256" {
		t.Fatalf("code_challenge_method = %q, want S256", got)
	}
}

func TestNewGeminiClient_DefaultsToLoopbackDynamicCallback(t *testing.T) {
	client := NewGeminiClient()

	if got := client.callbackHost(); got != "127.0.0.1" {
		t.Fatalf("callbackHost = %q, want 127.0.0.1", got)
	}
	if got := client.callbackPort(); got != 0 {
		t.Fatalf("callbackPort = %d, want 0", got)
	}
	if got := client.callbackPath(); got != "/oauth2callback" {
		t.Fatalf("callbackPath = %q, want /oauth2callback", got)
	}
}

func TestNewGeminiClient_AppliesEnvironmentOverrides(t *testing.T) {
	t.Setenv("CLIPAL_OAUTH_GEMINI_AUTH_URL", "http://127.0.0.1:18080/oauth/authorize")
	t.Setenv("CLIPAL_OAUTH_GEMINI_TOKEN_URL", "http://127.0.0.1:18080/oauth/token")
	t.Setenv("CLIPAL_OAUTH_GEMINI_USERINFO_URL", "http://127.0.0.1:18080/oauth/userinfo")
	t.Setenv("CLIPAL_OAUTH_GEMINI_CLOUD_CODE_URL", "http://127.0.0.1:18080/cloudcode")
	t.Setenv("CLIPAL_OAUTH_GEMINI_CLOUD_CODE_VERSION", "v-test")
	t.Setenv("CLIPAL_OAUTH_GEMINI_CLIENT_ID", "clipal-gemini-client")
	t.Setenv("CLIPAL_OAUTH_GEMINI_CLIENT_SECRET", "clipal-gemini-secret")
	t.Setenv("CLIPAL_OAUTH_GEMINI_PROJECT_ID", "project-override")
	t.Setenv("CLIPAL_OAUTH_GEMINI_SCOPE", "scope-a scope-b")
	t.Setenv("CLIPAL_OAUTH_GEMINI_CALLBACK_HOST", "localhost")
	t.Setenv("CLIPAL_OAUTH_GEMINI_CALLBACK_PORT", "8085")
	t.Setenv("CLIPAL_OAUTH_GEMINI_CALLBACK_PATH", "/mock/callback")

	client := NewGeminiClient()

	if got := client.authURL(); got != "http://127.0.0.1:18080/oauth/authorize" {
		t.Fatalf("authURL = %q", got)
	}
	if got := client.tokenURL(); got != "http://127.0.0.1:18080/oauth/token" {
		t.Fatalf("tokenURL = %q", got)
	}
	if got := client.userInfoURL(); got != "http://127.0.0.1:18080/oauth/userinfo" {
		t.Fatalf("userInfoURL = %q", got)
	}
	if got := client.cloudCodeURL(); got != "http://127.0.0.1:18080/cloudcode" {
		t.Fatalf("cloudCodeURL = %q", got)
	}
	if got := client.cloudCodeVersion(); got != "v-test" {
		t.Fatalf("cloudCodeVersion = %q", got)
	}
	if got := client.clientID(); got != "clipal-gemini-client" {
		t.Fatalf("clientID = %q", got)
	}
	if got := client.clientSecret(); got != "clipal-gemini-secret" {
		t.Fatalf("clientSecret = %q", got)
	}
	if got := client.requestedProjectID(nil); got != "project-override" {
		t.Fatalf("projectID = %q", got)
	}
	if got := client.scopes(); strings.Join(got, " ") != "scope-a scope-b" {
		t.Fatalf("scopes = %q", strings.Join(got, " "))
	}
	if got := client.callbackHost(); got != "localhost" {
		t.Fatalf("callbackHost = %q, want localhost", got)
	}
	if got := client.callbackPort(); got != 8085 {
		t.Fatalf("callbackPort = %d, want 8085", got)
	}
	if got := client.callbackPath(); got != "/mock/callback" {
		t.Fatalf("callbackPath = %q, want /mock/callback", got)
	}
}

func TestNewGeminiClient_UsesOfficialGoogleCloudProjectEnv(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "official-project")

	client := NewGeminiClient()

	if got := client.requestedProjectID(nil); got != "official-project" {
		t.Fatalf("projectID = %q, want official-project", got)
	}
}

func TestGeminiResolveProjectIDRejectsNumericProjectID(t *testing.T) {
	client := &GeminiClient{ProjectID: "123456789"}

	_, _, _, err := client.resolveProjectID(context.Background(), "access-1", nil)
	if err == nil {
		t.Fatalf("expected numeric project id error")
	}
	if !strings.Contains(err.Error(), "numeric project number") {
		t.Fatalf("error = %q, want numeric project number", err.Error())
	}
}

func TestGeminiResolveProjectIDValidationRequiredReturnsActionableError(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("GOOGLE_CLOUD_PROJECT_ID", "")

	cloudCodeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/v1internal:loadCodeAssist" {
			t.Fatalf("path = %q, want loadCodeAssist", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"ineligibleTiers": [
				{
					"reasonCode": "VALIDATION_REQUIRED",
					"reasonMessage": "verification required",
					"validationUrl": "https://example.com/verify"
				}
			]
		}`)
	}))
	defer cloudCodeServer.Close()

	client := &GeminiClient{
		CloudCodeURL: cloudCodeServer.URL,
		HTTPClient:   cloudCodeServer.Client(),
		Sleep:        func(time.Duration) {},
	}

	_, _, _, err := client.resolveProjectID(context.Background(), "access-1", nil)
	if err == nil {
		t.Fatalf("expected validation required error")
	}
	if !strings.Contains(err.Error(), "validation required") || !strings.Contains(err.Error(), "https://example.com/verify") {
		t.Fatalf("error = %q, want validation URL", err.Error())
	}
}

func TestGeminiResolveProjectIDCurrentTierWithoutProjectRequiresProject(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("GOOGLE_CLOUD_PROJECT_ID", "")

	cloudCodeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/v1internal:loadCodeAssist" {
			t.Fatalf("path = %q, want loadCodeAssist", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"currentTier":{"id":"standard-tier"}}`)
	}))
	defer cloudCodeServer.Close()

	client := &GeminiClient{
		CloudCodeURL: cloudCodeServer.URL,
		HTTPClient:   cloudCodeServer.Client(),
		Sleep:        func(time.Duration) {},
	}

	_, _, _, err := client.resolveProjectID(context.Background(), "access-1", nil)
	if err == nil {
		t.Fatalf("expected project required error")
	}
	if !strings.Contains(err.Error(), "GOOGLE_CLOUD_PROJECT") {
		t.Fatalf("error = %q, want project env guidance", err.Error())
	}
}

func TestGeminiResolveProjectIDAllowsOnboardingWithNonValidationIneligibleTier(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("GOOGLE_CLOUD_PROJECT_ID", "")

	var onboardCalls int
	cloudCodeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1internal:loadCodeAssist":
			_, _ = io.WriteString(w, `{
				"allowedTiers":[{"id":"free-tier","isDefault":true}],
				"ineligibleTiers":[{"reasonCode":"UNSUPPORTED_LOCATION","reasonMessage":"unsupported for another tier"}]
			}`)
		case "/v1internal:onboardUser":
			onboardCalls++
			_, _ = io.WriteString(w, `{"done":true,"response":{"cloudaicompanionProject":{"id":"auto-project"}}}`)
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer cloudCodeServer.Close()

	client := &GeminiClient{
		CloudCodeURL: cloudCodeServer.URL,
		HTTPClient:   cloudCodeServer.Client(),
		Sleep:        func(time.Duration) {},
	}

	projectID, tierID, autoProject, err := client.resolveProjectID(context.Background(), "access-1", nil)
	if err != nil {
		t.Fatalf("resolveProjectID: %v", err)
	}
	if got := onboardCalls; got != 1 {
		t.Fatalf("onboard calls = %d, want 1", got)
	}
	if projectID != "auto-project" || tierID != defaultGeminiFreeTierID || !autoProject {
		t.Fatalf("project=%q tier=%q auto=%v", projectID, tierID, autoProject)
	}
}

func TestGeminiStartLogin_UsesLoopbackRedirectURIWithDynamicPort(t *testing.T) {
	session, err := (&GeminiClient{
		AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
		ClientID: "test-client",
		Scopes: []string{
			"https://www.googleapis.com/auth/cloud-platform",
			"https://www.googleapis.com/auth/userinfo.email",
		},
	}).StartLogin(time.Date(2026, 4, 22, 7, 0, 0, 0, time.UTC), 5*time.Minute)
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	defer func() {
		if session.callback != nil {
			_ = session.callback.Close()
		}
	}()

	parsedAuthURL, err := url.Parse(session.AuthURL)
	if err != nil {
		t.Fatalf("url.Parse authURL: %v", err)
	}

	redirectURI := parsedAuthURL.Query().Get("redirect_uri")
	if redirectURI == "" {
		t.Fatal("redirect_uri not set")
	}
	if session.redirectURI != redirectURI {
		t.Fatalf("session redirectURI = %q, want %q", session.redirectURI, redirectURI)
	}

	parsedRedirectURI, err := url.Parse(redirectURI)
	if err != nil {
		t.Fatalf("url.Parse redirectURI: %v", err)
	}
	if got := parsedRedirectURI.Scheme; got != "http" {
		t.Fatalf("scheme = %q, want http", got)
	}
	if got := parsedRedirectURI.Hostname(); got != "127.0.0.1" {
		t.Fatalf("hostname = %q, want 127.0.0.1", got)
	}
	port, err := strconv.Atoi(parsedRedirectURI.Port())
	if err != nil {
		t.Fatalf("redirect port %q is not numeric: %v", parsedRedirectURI.Port(), err)
	}
	if port <= 0 {
		t.Fatalf("redirect port = %d, want dynamic assigned port", port)
	}
	if got := parsedRedirectURI.Path; got != "/oauth2callback" {
		t.Fatalf("path = %q, want /oauth2callback", got)
	}
}

func TestGeminiExchangeCode(t *testing.T) {
	now := time.Date(2026, 4, 22, 8, 0, 0, 0, time.UTC)

	var loadProject string
	var onboardCalls int
	cloudCodeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("json.Unmarshal: %v", err)
		}

		switch r.URL.Path {
		case "/v1internal:loadCodeAssist":
			loadProject, _ = payload["cloudaicompanionProject"].(string)
			if loadProject != "project-explicit" {
				t.Fatalf("load project = %q, want project-explicit", loadProject)
			}
			metadata, _ := payload["metadata"].(map[string]any)
			if got, _ := metadata["duetProject"].(string); got != "project-explicit" {
				t.Fatalf("load metadata duetProject = %q, want project-explicit", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"currentTier":{"id":"tier-current"},"paidTier":{"id":"tier-paid"},"cloudaicompanionProject":{"id":"project-server"}}`)
		case "/v1internal:onboardUser":
			onboardCalls++
			t.Fatalf("unexpected onboard call for currentTier response")
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer cloudCodeServer.Close()

	userInfoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer access-1" {
			t.Fatalf("authorization = %q, want Bearer access-1", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"email":"sean@example.com"}`)
	}))
	defer userInfoServer.Close()

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if got := r.Form.Get("grant_type"); got != "authorization_code" {
			t.Fatalf("grant_type = %q, want authorization_code", got)
		}
		if got := r.Form.Get("client_id"); got != "test-client" {
			t.Fatalf("client_id = %q, want test-client", got)
		}
		if got := r.Form.Get("client_secret"); got != "test-secret" {
			t.Fatalf("client_secret = %q, want test-secret", got)
		}
		if got := r.Form.Get("code"); got != "auth-code" {
			t.Fatalf("code = %q, want auth-code", got)
		}
		if got := r.Form.Get("code_verifier"); got != "verifier-123" {
			t.Fatalf("code_verifier = %q, want verifier-123", got)
		}
		if got := r.Form.Get("redirect_uri"); got != testGeminiRedirectURI {
			t.Fatalf("redirect_uri = %q", got)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"access-1","refresh_token":"refresh-1","expires_in":3600,"scope":"scope-a","token_type":"Bearer"}`)
	}))
	defer tokenServer.Close()

	client := &GeminiClient{
		TokenURL:     tokenServer.URL,
		UserInfoURL:  userInfoServer.URL,
		CloudCodeURL: cloudCodeServer.URL,
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		ProjectID:    "project-explicit",
		HTTPClient:   tokenServer.Client(),
		Now:          func() time.Time { return now },
		Sleep:        func(time.Duration) {},
	}

	cred, err := client.ExchangeCode(context.Background(), "auth-code", testGeminiRedirectURI, PKCECodes{
		CodeVerifier:  "verifier-123",
		CodeChallenge: "challenge-123",
	})
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}

	if got := cred.Ref; got != "gemini-sean-example-com-project-server" {
		t.Fatalf("ref = %q, want gemini-sean-example-com-project-server", got)
	}
	if got := cred.Provider; got != "gemini" {
		t.Fatalf("provider = %q, want gemini", got)
	}
	if got := cred.Email; got != "sean@example.com" {
		t.Fatalf("email = %q, want sean@example.com", got)
	}
	if got := cred.AccountID; got != "project-server" {
		t.Fatalf("account_id = %q, want project-server", got)
	}
	if got := cred.AccessToken; got != "access-1" {
		t.Fatalf("access_token = %q, want access-1", got)
	}
	if got := cred.RefreshToken; got != "refresh-1" {
		t.Fatalf("refresh_token = %q, want refresh-1", got)
	}
	if got := cred.ExpiresAt; !got.Equal(now.Add(time.Hour)) {
		t.Fatalf("expires_at = %s, want %s", got, now.Add(time.Hour))
	}
	if got := cred.LastRefresh; !got.Equal(now) {
		t.Fatalf("last_refresh = %s, want %s", got, now)
	}
	if got := cred.Metadata["project_id"]; got != "project-server" {
		t.Fatalf("metadata project_id = %q, want project-server", got)
	}
	if got := cred.Metadata["requested_project_id"]; got != "project-explicit" {
		t.Fatalf("metadata requested_project_id = %q, want project-explicit", got)
	}
	if got := cred.Metadata["tier_id"]; got != "tier-paid" {
		t.Fatalf("metadata tier_id = %q, want tier-paid", got)
	}
	if got := cred.Metadata["auto_project"]; got != "false" {
		t.Fatalf("metadata auto_project = %q, want false", got)
	}
	if got := cred.Metadata["token_type"]; got != "Bearer" {
		t.Fatalf("metadata token_type = %q, want Bearer", got)
	}
	if got := cred.Metadata["granted_scope"]; got != "scope-a" {
		t.Fatalf("metadata granted_scope = %q, want scope-a", got)
	}
	if !strings.Contains(cred.Metadata["token_json"], `"refresh_token":"refresh-1"`) {
		t.Fatalf("token_json = %q, want refresh_token", cred.Metadata["token_json"])
	}
	if onboardCalls != 0 {
		t.Fatalf("onboard calls = %d, want 0", onboardCalls)
	}
}

func TestGeminiRefreshPreservesIdentityAndProjectMetadata(t *testing.T) {
	now := time.Date(2026, 4, 22, 9, 0, 0, 0, time.UTC)

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if got := r.Form.Get("grant_type"); got != "refresh_token" {
			t.Fatalf("grant_type = %q, want refresh_token", got)
		}
		if got := r.Form.Get("refresh_token"); got != "refresh-1" {
			t.Fatalf("refresh_token = %q, want refresh-1", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"access-2","refresh_token":"","expires_in":7200,"token_type":"Bearer"}`)
	}))
	defer tokenServer.Close()

	userInfoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer userInfoServer.Close()

	cloudCodeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("io.ReadAll: %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("json.Unmarshal: %v", err)
		}
		switch r.URL.Path {
		case "/v1internal:loadCodeAssist":
			if got, _ := payload["cloudaicompanionProject"].(string); got != "project-existing" {
				t.Fatalf("load project = %q, want project-existing", got)
			}
			metadata, _ := payload["metadata"].(map[string]any)
			if got, _ := metadata["duetProject"].(string); got != "project-existing" {
				t.Fatalf("load metadata duetProject = %q, want project-existing", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"currentTier":{"id":"tier-refresh"}}`)
		case "/v1internal:onboardUser":
			t.Fatalf("unexpected onboard call for existing currentTier")
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer cloudCodeServer.Close()

	client := &GeminiClient{
		TokenURL:     tokenServer.URL,
		UserInfoURL:  userInfoServer.URL,
		CloudCodeURL: cloudCodeServer.URL,
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		HTTPClient:   tokenServer.Client(),
		Now:          func() time.Time { return now },
		Sleep:        func(time.Duration) {},
	}

	cred, err := client.Refresh(context.Background(), &Credential{
		Ref:          "gemini-sean-example-com-project-existing",
		Provider:     "gemini",
		Email:        "sean@example.com",
		AccountID:    "project-existing",
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		Metadata: map[string]string{
			"project_id":           "project-existing",
			"requested_project_id": "project-existing",
			"tier_id":              "tier-existing",
			"custom":               "keep",
		},
	})
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	if got := cred.Ref; got != "gemini-sean-example-com-project-existing" {
		t.Fatalf("ref = %q, want gemini-sean-example-com-project-existing", got)
	}
	if got := cred.Email; got != "sean@example.com" {
		t.Fatalf("email = %q, want sean@example.com", got)
	}
	if got := cred.AccountID; got != "project-existing" {
		t.Fatalf("account_id = %q, want project-existing", got)
	}
	if got := cred.AccessToken; got != "access-2" {
		t.Fatalf("access_token = %q, want access-2", got)
	}
	if got := cred.RefreshToken; got != "refresh-1" {
		t.Fatalf("refresh_token = %q, want refresh-1", got)
	}
	if got := cred.ExpiresAt; !got.Equal(now.Add(2 * time.Hour)) {
		t.Fatalf("expires_at = %s, want %s", got, now.Add(2*time.Hour))
	}
	if got := cred.Metadata["project_id"]; got != "project-existing" {
		t.Fatalf("metadata project_id = %q, want project-existing", got)
	}
	if got := cred.Metadata["requested_project_id"]; got != "project-existing" {
		t.Fatalf("metadata requested_project_id = %q, want project-existing", got)
	}
	if got := cred.Metadata["tier_id"]; got != "tier-refresh" {
		t.Fatalf("metadata tier_id = %q, want tier-refresh", got)
	}
	if got := cred.Metadata["custom"]; got != "keep" {
		t.Fatalf("metadata custom = %q, want keep", got)
	}
}

func TestGeminiExchangeCodeFreeTierOnboardingOmitsProject(t *testing.T) {
	now := time.Date(2026, 4, 22, 9, 30, 0, 0, time.UTC)

	var loadCalls int
	var onboardCalls int

	cloudCodeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("io.ReadAll: %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("json.Unmarshal: %v", err)
		}

		switch r.URL.Path {
		case "/v1internal:loadCodeAssist":
			loadCalls++
			if got, _ := payload["cloudaicompanionProject"].(string); got != "project-explicit" {
				t.Fatalf("load project = %q, want project-explicit", got)
			}
			metadata, _ := payload["metadata"].(map[string]any)
			if got, _ := metadata["duetProject"].(string); got != "project-explicit" {
				t.Fatalf("load metadata duetProject = %q, want project-explicit", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"allowedTiers":[{"id":"free-tier","isDefault":true}]}`)
		case "/v1internal:onboardUser":
			onboardCalls++
			if _, ok := payload["cloudaicompanionProject"]; ok {
				t.Fatalf("free-tier onboard should omit cloudaicompanionProject")
			}
			if got, _ := payload["tierId"].(string); got != "free-tier" {
				t.Fatalf("tierId = %q, want free-tier", got)
			}
			metadata, _ := payload["metadata"].(map[string]any)
			if _, ok := metadata["duetProject"]; ok {
				t.Fatalf("free-tier onboard metadata should omit duetProject")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"done":true,"response":{"cloudaicompanionProject":{"id":"project-managed"}}}`)
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer cloudCodeServer.Close()

	userInfoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"email":"free@example.com"}`)
	}))
	defer userInfoServer.Close()

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"access-free","refresh_token":"refresh-free","expires_in":1800,"token_type":"Bearer"}`)
	}))
	defer tokenServer.Close()

	client := &GeminiClient{
		TokenURL:     tokenServer.URL,
		UserInfoURL:  userInfoServer.URL,
		CloudCodeURL: cloudCodeServer.URL,
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		ProjectID:    "project-explicit",
		HTTPClient:   tokenServer.Client(),
		Now:          func() time.Time { return now },
		Sleep:        func(time.Duration) {},
	}

	cred, err := client.ExchangeCode(context.Background(), "auth-code", testGeminiRedirectURI, PKCECodes{
		CodeVerifier:  "verifier-123",
		CodeChallenge: "challenge-123",
	})
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}

	if loadCalls != 1 {
		t.Fatalf("load calls = %d, want 1", loadCalls)
	}
	if onboardCalls != 1 {
		t.Fatalf("onboard calls = %d, want 1", onboardCalls)
	}
	if got := cred.AccountID; got != "project-managed" {
		t.Fatalf("account_id = %q, want project-managed", got)
	}
	if got := cred.Metadata["project_id"]; got != "project-managed" {
		t.Fatalf("metadata project_id = %q, want project-managed", got)
	}
	if got := cred.Metadata["requested_project_id"]; got != "project-explicit" {
		t.Fatalf("metadata requested_project_id = %q, want project-explicit", got)
	}
	if got := cred.Metadata["tier_id"]; got != "free-tier" {
		t.Fatalf("metadata tier_id = %q, want free-tier", got)
	}
	if got := cred.Metadata["auto_project"]; got != "false" {
		t.Fatalf("metadata auto_project = %q, want false", got)
	}
}

func TestGeminiExchangeCodeAutoDiscoversProjectViaOnboarding(t *testing.T) {
	now := time.Date(2026, 4, 22, 10, 0, 0, 0, time.UTC)

	var loadCalls int
	var onboardCalls int
	var getOperationCalls int
	var sleepCalls []time.Duration

	cloudCodeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1internal:loadCodeAssist":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("io.ReadAll: %v", err)
			}
			var payload map[string]any
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatalf("json.Unmarshal: %v", err)
			}
			loadCalls++
			if _, ok := payload["cloudaicompanionProject"]; ok {
				t.Fatalf("expected auto-discovery load request without explicit project")
			}
			metadata, _ := payload["metadata"].(map[string]any)
			if _, ok := metadata["duetProject"]; ok {
				t.Fatalf("auto-discovery load metadata should omit duetProject")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"allowedTiers":[{"id":"tier-auto","isDefault":true}]}`)
		case "/v1internal:onboardUser":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("io.ReadAll: %v", err)
			}
			var payload map[string]any
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatalf("json.Unmarshal: %v", err)
			}
			onboardCalls++
			if _, ok := payload["cloudaicompanionProject"]; ok {
				t.Fatalf("auto-discovery onboard should omit cloudaicompanionProject")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"done":false,"name":"operations/123"}`)
		case "/v1internal/operations/123":
			if got := r.Method; got != http.MethodGet {
				t.Fatalf("method = %q, want GET", got)
			}
			getOperationCalls++
			w.Header().Set("Content-Type", "application/json")
			if getOperationCalls == 1 {
				_, _ = io.WriteString(w, `{"name":"operations/123","done":false}`)
				return
			}
			_, _ = io.WriteString(w, `{"name":"operations/123","done":true,"response":{"cloudaicompanionProject":{"id":"project-auto"}}}`)
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer cloudCodeServer.Close()

	userInfoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"email":"auto@example.com"}`)
	}))
	defer userInfoServer.Close()

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"access-auto","refresh_token":"refresh-auto","expires_in":1800,"token_type":"Bearer"}`)
	}))
	defer tokenServer.Close()

	client := &GeminiClient{
		TokenURL:     tokenServer.URL,
		UserInfoURL:  userInfoServer.URL,
		CloudCodeURL: cloudCodeServer.URL,
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		HTTPClient:   tokenServer.Client(),
		Now:          func() time.Time { return now },
		Sleep: func(d time.Duration) {
			sleepCalls = append(sleepCalls, d)
		},
	}

	cred, err := client.ExchangeCode(context.Background(), "auth-code", testGeminiRedirectURI, PKCECodes{
		CodeVerifier:  "verifier-123",
		CodeChallenge: "challenge-123",
	})
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}

	if loadCalls != 1 {
		t.Fatalf("load calls = %d, want 1", loadCalls)
	}
	if onboardCalls != 1 {
		t.Fatalf("onboard calls = %d, want 1", onboardCalls)
	}
	if getOperationCalls != 2 {
		t.Fatalf("getOperation calls = %d, want 2", getOperationCalls)
	}
	if len(sleepCalls) != 1 {
		t.Fatalf("sleep calls = %d, want 1", len(sleepCalls))
	}
	for i, got := range sleepCalls {
		if got != 5*time.Second {
			t.Fatalf("sleep[%d] = %s, want %s", i, got, 5*time.Second)
		}
	}
	if got := cred.AccountID; got != "project-auto" {
		t.Fatalf("account_id = %q, want project-auto", got)
	}
	if got := cred.Metadata["project_id"]; got != "project-auto" {
		t.Fatalf("metadata project_id = %q, want project-auto", got)
	}
	if got := cred.Metadata["auto_project"]; got != "true" {
		t.Fatalf("metadata auto_project = %q, want true", got)
	}
}

func TestGeminiExchangeCodeStopsWhenAccountValidationRequired(t *testing.T) {
	var onboardCalls int
	cloudCodeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1internal:loadCodeAssist":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"ineligibleTiers":[{"reasonCode":"VALIDATION_REQUIRED","reasonMessage":"Validate account","validationUrl":"https://example.test/validate"}]}`)
		case "/v1internal:onboardUser":
			onboardCalls++
			t.Fatalf("unexpected onboard call for validation-required account")
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer cloudCodeServer.Close()

	userInfoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"email":"validate@example.com"}`)
	}))
	defer userInfoServer.Close()

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"access-validate","refresh_token":"refresh-validate","expires_in":1800,"token_type":"Bearer"}`)
	}))
	defer tokenServer.Close()

	client := &GeminiClient{
		TokenURL:     tokenServer.URL,
		UserInfoURL:  userInfoServer.URL,
		CloudCodeURL: cloudCodeServer.URL,
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		HTTPClient:   tokenServer.Client(),
		Sleep:        func(time.Duration) {},
	}

	_, err := client.ExchangeCode(context.Background(), "auth-code", testGeminiRedirectURI, PKCECodes{
		CodeVerifier:  "verifier-123",
		CodeChallenge: "challenge-123",
	})
	if err == nil {
		t.Fatalf("expected validation-required error")
	}
	if !strings.Contains(err.Error(), "gemini account validation required") ||
		!strings.Contains(err.Error(), "https://example.test/validate") {
		t.Fatalf("error = %q, want validation URL", err.Error())
	}
	if onboardCalls != 0 {
		t.Fatalf("onboard calls = %d, want 0", onboardCalls)
	}
}

func TestGeminiPollCloudCodeOperation_BoundsPendingOperations(t *testing.T) {
	var pollCalls int
	cloudCodeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/v1internal/operations/123" {
			t.Fatalf("path = %q, want /v1internal/operations/123", got)
		}
		pollCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"name":"operations/123","done":false}`)
	}))
	defer cloudCodeServer.Close()

	client := &GeminiClient{
		CloudCodeURL: cloudCodeServer.URL,
		HTTPClient:   cloudCodeServer.Client(),
		Sleep:        func(time.Duration) {},
	}

	_, err := client.pollCloudCodeOperation(context.Background(), "access-1", "operations/123")
	if err == nil {
		t.Fatalf("expected polling to fail for non-terminating operation")
	}
	if !strings.Contains(err.Error(), "did not complete") {
		t.Fatalf("error = %q, want poll bound failure", err.Error())
	}
	if pollCalls != defaultGeminiOperationPollMaxAttempts {
		t.Fatalf("poll calls = %d, want %d", pollCalls, defaultGeminiOperationPollMaxAttempts)
	}
}
