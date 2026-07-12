package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
)

func TestCodexRefreshMetadataMatchesInstalledRelease(t *testing.T) {
	if !strings.HasPrefix(defaultCodexUserAgent, "codex_cli_rs/0.144.1 ") {
		t.Fatalf("defaultCodexUserAgent = %q, want Codex 0.144.1 identity", defaultCodexUserAgent)
	}
}

func TestCodexStartLoginAndPollCompletesCredential(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 4, 18, 20, 0, 0, 0, time.UTC)
	var gotRedirectURI string

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if got := r.Form.Get("grant_type"); got != "authorization_code" {
			t.Fatalf("grant_type = %q, want authorization_code", got)
		}
		if got := r.Form.Get("code"); got != "auth-code" {
			t.Fatalf("code = %q, want auth-code", got)
		}
		if got := r.Form.Get("code_verifier"); got == "" {
			t.Fatalf("expected code_verifier to be set")
		}
		gotRedirectURI = r.Form.Get("redirect_uri")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(`{"access_token":"access-1","refresh_token":"refresh-1","id_token":"%s","expires_in":3600}`, testJWT("sean@example.com", "acct_123")))
	}))
	defer tokenServer.Close()

	svc := NewService(dir,
		WithNowFunc(func() time.Time { return now }),
		WithCodexClient(&CodexClient{
			AuthURL:      "https://auth.openai.com/oauth/authorize",
			TokenURL:     tokenServer.URL,
			ClientID:     "test-client",
			CallbackHost: "127.0.0.1",
			CallbackPort: 0,
			CallbackPath: "/auth/callback",
			HTTPClient:   tokenServer.Client(),
			Now:          func() time.Time { return now },
		}),
	)

	session, err := svc.StartLogin(config.OAuthProviderCodex)
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	if session.Status != LoginStatusPending {
		t.Fatalf("status = %q, want %q", session.Status, LoginStatusPending)
	}

	authURL, err := url.Parse(session.AuthURL)
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	query := authURL.Query()
	if query.Get("client_id") != "test-client" {
		t.Fatalf("client_id = %q", query.Get("client_id"))
	}
	if query.Get("state") != session.ID {
		t.Fatalf("state = %q, want %q", query.Get("state"), session.ID)
	}
	if query.Get("code_challenge") == "" {
		t.Fatalf("expected code_challenge to be set")
	}
	if got := query.Get("scope"); got != defaultCodexScope {
		t.Fatalf("scope = %q, want %q", got, defaultCodexScope)
	}
	if got := query.Get("originator"); got != defaultCodexOriginator {
		t.Fatalf("originator = %q, want %q", got, defaultCodexOriginator)
	}
	if got := query.Get("prompt"); got != "" {
		t.Fatalf("prompt = %q, want empty", got)
	}

	redirectURI := query.Get("redirect_uri")
	resp, err := http.Get(redirectURI + "?code=auth-code&state=" + url.QueryEscape(session.ID))
	if err != nil {
		t.Fatalf("http.Get callback: %v", err)
	}
	_ = resp.Body.Close()

	completed, err := svc.PollLogin(session.ID)
	if err != nil {
		t.Fatalf("PollLogin: %v", err)
	}
	if completed.Status != LoginStatusCompleted {
		t.Fatalf("status = %q, want %q", completed.Status, LoginStatusCompleted)
	}
	if completed.Email != "sean@example.com" {
		t.Fatalf("email = %q, want sean@example.com", completed.Email)
	}
	if completed.CredentialRef != "codex-sean-example-com" {
		t.Fatalf("credential_ref = %q, want codex-sean-example-com", completed.CredentialRef)
	}
	if gotRedirectURI != redirectURI {
		t.Fatalf("redirect_uri = %q, want %q", gotRedirectURI, redirectURI)
	}

	cred, err := svc.Load(config.OAuthProviderCodex, completed.CredentialRef)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cred.AccessToken != "access-1" {
		t.Fatalf("access token = %q, want access-1", cred.AccessToken)
	}
	if cred.Email != "sean@example.com" {
		t.Fatalf("email = %q, want sean@example.com", cred.Email)
	}
}

func TestCodexRefreshUsesOfficialJSONRequestAndPreservesOmittedFields(t *testing.T) {
	now := time.Date(2026, 4, 18, 21, 30, 0, 0, time.UTC)
	previousExpiresAt := now.Add(10 * time.Minute)
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want POST", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("content-type = %q, want application/json", got)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Fatalf("accept = %q, want application/json", got)
		}
		if got := r.Header.Get("Originator"); got != defaultCodexOriginator {
			t.Fatalf("originator = %q, want %q", got, defaultCodexOriginator)
		}
		if got := r.Header.Get("User-Agent"); got != defaultCodexUserAgent {
			t.Fatalf("user-agent = %q, want %q", got, defaultCodexUserAgent)
		}

		var req codexRefreshRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if got := req.GrantType; got != "refresh_token" {
			t.Fatalf("grant_type = %q, want refresh_token", got)
		}
		if got := req.ClientID; got != "test-client" {
			t.Fatalf("client_id = %q, want test-client", got)
		}
		if got := req.RefreshToken; got != "refresh-1" {
			t.Fatalf("refresh_token = %q, want refresh-1", got)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"access-2"}`)
	}))
	defer tokenServer.Close()

	client := &CodexClient{
		TokenURL:   tokenServer.URL,
		ClientID:   "test-client",
		HTTPClient: tokenServer.Client(),
		Now:        func() time.Time { return now },
	}
	cred, err := client.Refresh(context.Background(), &Credential{
		Ref:          "codex-sean-example-com",
		Provider:     config.OAuthProviderCodex,
		Email:        "sean@example.com",
		AccountID:    "acct_123",
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		ExpiresAt:    previousExpiresAt,
		Metadata: map[string]string{
			"id_token":                   testJWT("sean@example.com", "acct_123"),
			"chatgpt_account_is_fedramp": "true",
			"custom":                     "keep",
		},
	})
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got := cred.AccessToken; got != "access-2" {
		t.Fatalf("access_token = %q, want access-2", got)
	}
	if got := cred.RefreshToken; got != "refresh-1" {
		t.Fatalf("refresh_token = %q, want refresh-1", got)
	}
	if got := cred.ExpiresAt; !got.Equal(previousExpiresAt) {
		t.Fatalf("expires_at = %s, want %s", got, previousExpiresAt)
	}
	if got := cred.Metadata["id_token"]; got == "" {
		t.Fatalf("expected id_token metadata to be preserved")
	}
	if got := cred.Metadata["chatgpt_account_is_fedramp"]; got != "true" {
		t.Fatalf("chatgpt_account_is_fedramp = %q, want true", got)
	}
	if got := cred.Metadata["custom"]; got != "keep" {
		t.Fatalf("custom metadata = %q, want keep", got)
	}
}

func TestNewCodexClient_AppliesEnvironmentOverrides(t *testing.T) {
	t.Setenv("CLIPAL_OAUTH_CODEX_AUTH_URL", "http://127.0.0.1:18080/oauth/authorize")
	t.Setenv("CLIPAL_OAUTH_CODEX_TOKEN_URL", "http://127.0.0.1:18080/oauth/token")
	t.Setenv("CLIPAL_OAUTH_CODEX_USAGE_URL", "http://127.0.0.1:18080/wham/usage")
	t.Setenv("CLIPAL_OAUTH_CODEX_CLIENT_ID", "clipal-dev-client")
	t.Setenv("CLIPAL_OAUTH_CODEX_CALLBACK_HOST", "127.0.0.1")
	t.Setenv("CLIPAL_OAUTH_CODEX_CALLBACK_PORT", "0")
	t.Setenv("CLIPAL_OAUTH_CODEX_CALLBACK_PATH", "/mock/callback")

	client := NewCodexClient()
	if got := client.authURL(); got != "http://127.0.0.1:18080/oauth/authorize" {
		t.Fatalf("authURL = %q", got)
	}
	if got := client.tokenURL(); got != "http://127.0.0.1:18080/oauth/token" {
		t.Fatalf("tokenURL = %q", got)
	}
	if got := client.usageURL(); got != "http://127.0.0.1:18080/wham/usage" {
		t.Fatalf("usageURL = %q", got)
	}
	if got := client.clientID(); got != "clipal-dev-client" {
		t.Fatalf("clientID = %q", got)
	}
	if got := client.callbackHost(); got != "127.0.0.1" {
		t.Fatalf("callbackHost = %q", got)
	}
	if got := client.callbackPort(); got != 0 {
		t.Fatalf("callbackPort = %d", got)
	}
	if got := client.callbackPath(); got != "/mock/callback" {
		t.Fatalf("callbackPath = %q", got)
	}
}

func TestNewCodexClient_IgnoresInvalidCallbackPortOverride(t *testing.T) {
	t.Setenv("CLIPAL_OAUTH_CODEX_CALLBACK_PORT", "nope")

	client := NewCodexClient()
	if got := client.callbackPort(); got != defaultCodexCallbackPort {
		t.Fatalf("callbackPort = %d, want %d", got, defaultCodexCallbackPort)
	}
}
