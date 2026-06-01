package oauth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestClaudeGenerateAuthURL(t *testing.T) {
	client := &ClaudeClient{
		AuthURL:  "https://platform.claude.com/oauth/authorize",
		ClientID: "test-client",
		Scope:    "scope-a scope-b",
	}
	pkce := PKCECodes{
		CodeVerifier:  "verifier-123",
		CodeChallenge: "challenge-123",
	}

	authURL, err := client.GenerateAuthURL("state-123", "http://localhost:54545/callback", pkce)
	if err != nil {
		t.Fatalf("GenerateAuthURL: %v", err)
	}

	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	if got := parsed.Scheme + "://" + parsed.Host + parsed.Path; got != "https://platform.claude.com/oauth/authorize" {
		t.Fatalf("auth url = %q", got)
	}

	query := parsed.Query()
	if got := query.Get("code"); got != "true" {
		t.Fatalf("code = %q, want true", got)
	}
	if got := query.Get("client_id"); got != "test-client" {
		t.Fatalf("client_id = %q, want test-client", got)
	}
	if got := query.Get("response_type"); got != "code" {
		t.Fatalf("response_type = %q, want code", got)
	}
	if got := query.Get("redirect_uri"); got != "http://localhost:54545/callback" {
		t.Fatalf("redirect_uri = %q", got)
	}
	if got := query.Get("scope"); got != "scope-a scope-b" {
		t.Fatalf("scope = %q", got)
	}
	if got := query.Get("code_challenge"); got != "challenge-123" {
		t.Fatalf("code_challenge = %q", got)
	}
	if got := query.Get("code_challenge_method"); got != "S256" {
		t.Fatalf("code_challenge_method = %q, want S256", got)
	}
	if got := query.Get("state"); got != "state-123" {
		t.Fatalf("state = %q, want state-123", got)
	}
}

func TestClaudeExchangeCode(t *testing.T) {
	now := time.Date(2026, 4, 22, 8, 0, 0, 0, time.UTC)
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

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("io.ReadAll: %v", err)
		}
		var req claudeTokenRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("json.Unmarshal: %v", err)
		}
		if req.Code != "auth-code" {
			t.Fatalf("code = %q, want auth-code", req.Code)
		}
		if req.State != "session-123" {
			t.Fatalf("state = %q, want session-123", req.State)
		}
		if req.GrantType != "authorization_code" {
			t.Fatalf("grant_type = %q, want authorization_code", req.GrantType)
		}
		if req.ClientID != "test-client" {
			t.Fatalf("client_id = %q, want test-client", req.ClientID)
		}
		if req.RedirectURI != "http://localhost:54545/callback" {
			t.Fatalf("redirect_uri = %q", req.RedirectURI)
		}
		if req.CodeVerifier != "verifier-123" {
			t.Fatalf("code_verifier = %q, want verifier-123", req.CodeVerifier)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"access-1","refresh_token":"refresh-1","token_type":"Bearer","expires_in":3600,"organization":{"uuid":"org_123","name":"Anthropic"},"account":{"uuid":"acct_123","email_address":"sean@example.com"}}`)
	}))
	defer tokenServer.Close()

	client := &ClaudeClient{
		TokenURL:   tokenServer.URL,
		ClientID:   "test-client",
		HTTPClient: tokenServer.Client(),
		Now:        func() time.Time { return now },
	}

	cred, err := client.ExchangeCode(context.Background(), "auth-code", "session-123", "http://localhost:54545/callback", PKCECodes{
		CodeVerifier:  "verifier-123",
		CodeChallenge: "challenge-123",
	})
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}

	if got := cred.Ref; got != "claude-sean-example-com" {
		t.Fatalf("ref = %q, want claude-sean-example-com", got)
	}
	if got := cred.Provider; got != "claude" {
		t.Fatalf("provider = %q, want claude", got)
	}
	if got := cred.Email; got != "sean@example.com" {
		t.Fatalf("email = %q, want sean@example.com", got)
	}
	if got := cred.AccountID; got != "acct_123" {
		t.Fatalf("account_id = %q, want acct_123", got)
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
	if got := cred.Metadata["organization_id"]; got != "org_123" {
		t.Fatalf("organization_id = %q, want org_123", got)
	}
	if got := cred.Metadata["organization_name"]; got != "Anthropic" {
		t.Fatalf("organization_name = %q, want Anthropic", got)
	}
	if got := cred.Metadata["account_id"]; got != "acct_123" {
		t.Fatalf("metadata account_id = %q, want acct_123", got)
	}
	if got := cred.Metadata["account_email"]; got != "sean@example.com" {
		t.Fatalf("account_email = %q, want sean@example.com", got)
	}
	if got := cred.Metadata["token_type"]; got != "Bearer" {
		t.Fatalf("token_type = %q, want Bearer", got)
	}
}

func TestClaudeRefreshPreservesIdentityMetadata(t *testing.T) {
	now := time.Date(2026, 4, 22, 9, 0, 0, 0, time.UTC)
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("io.ReadAll: %v", err)
		}
		var req claudeTokenRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("json.Unmarshal: %v", err)
		}
		if req.GrantType != "refresh_token" {
			t.Fatalf("grant_type = %q, want refresh_token", req.GrantType)
		}
		if req.ClientID != "test-client" {
			t.Fatalf("client_id = %q, want test-client", req.ClientID)
		}
		if req.RefreshToken != "refresh-1" {
			t.Fatalf("refresh_token = %q, want refresh-1", req.RefreshToken)
		}
		if req.Scope != "" {
			t.Fatalf("scope = %q, want omitted", req.Scope)
		}
		if got := r.Header.Get("Anthropic-Beta"); got != defaultClaudeRefreshBeta {
			t.Fatalf("Anthropic-Beta = %q, want %q", got, defaultClaudeRefreshBeta)
		}
		if got := r.Header.Get("User-Agent"); got != defaultClaudeRefreshUA {
			t.Fatalf("User-Agent = %q, want %q", got, defaultClaudeRefreshUA)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"access-2","refresh_token":"","token_type":"Bearer","expires_in":7200}`)
	}))
	defer tokenServer.Close()

	client := &ClaudeClient{
		TokenURL:   tokenServer.URL,
		ClientID:   "test-client",
		HTTPClient: tokenServer.Client(),
		Now:        func() time.Time { return now },
	}

	cred, err := client.Refresh(context.Background(), &Credential{
		Ref:          "claude-sean-example-com",
		Provider:     "claude",
		Email:        "sean@example.com",
		AccountID:    "acct_123",
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		Metadata: map[string]string{
			"organization_id":   "org_123",
			"organization_name": "Anthropic",
			"custom":            "keep",
		},
	})
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	if got := cred.Ref; got != "claude-sean-example-com" {
		t.Fatalf("ref = %q, want claude-sean-example-com", got)
	}
	if got := cred.Email; got != "sean@example.com" {
		t.Fatalf("email = %q, want sean@example.com", got)
	}
	if got := cred.AccountID; got != "acct_123" {
		t.Fatalf("account_id = %q, want acct_123", got)
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
	if got := cred.Metadata["organization_id"]; got != "org_123" {
		t.Fatalf("organization_id = %q, want org_123", got)
	}
	if got := cred.Metadata["organization_name"]; got != "Anthropic" {
		t.Fatalf("organization_name = %q, want Anthropic", got)
	}
	if got := cred.Metadata["custom"]; got != "keep" {
		t.Fatalf("custom metadata = %q, want keep", got)
	}
	if got := cred.Metadata["token_type"]; got != "Bearer" {
		t.Fatalf("token_type = %q, want Bearer", got)
	}
}

func TestNewClaudeClient_DefaultsAndEnvOverrides(t *testing.T) {
	defaults := NewClaudeClient()
	if got := defaults.authURL(); got != defaultClaudeAuthURL {
		t.Fatalf("authURL = %q, want %q", got, defaultClaudeAuthURL)
	}
	if got := defaults.tokenURL(); got != defaultClaudeTokenURL {
		t.Fatalf("tokenURL = %q, want %q", got, defaultClaudeTokenURL)
	}
	if got := defaults.clientID(); got != defaultClaudeClientID {
		t.Fatalf("clientID = %q, want %q", got, defaultClaudeClientID)
	}
	if got := defaults.scope(); got != defaultClaudeScope {
		t.Fatalf("scope = %q, want %q", got, defaultClaudeScope)
	}
	if got := defaults.callbackHost(); got != defaultClaudeCallbackHost {
		t.Fatalf("callbackHost = %q, want %q", got, defaultClaudeCallbackHost)
	}
	if got := defaults.callbackPort(); got != defaultClaudeCallbackPort {
		t.Fatalf("callbackPort = %d, want %d", got, defaultClaudeCallbackPort)
	}
	if got := defaults.callbackPath(); got != defaultClaudeCallbackPath {
		t.Fatalf("callbackPath = %q, want %q", got, defaultClaudeCallbackPath)
	}

	t.Setenv("CLIPAL_OAUTH_CLAUDE_AUTH_URL", "http://127.0.0.1:18080/oauth/authorize")
	t.Setenv("CLIPAL_OAUTH_CLAUDE_TOKEN_URL", "http://127.0.0.1:18080/oauth/token")
	t.Setenv("CLIPAL_OAUTH_CLAUDE_CLIENT_ID", "claude-dev-client")
	t.Setenv("CLIPAL_OAUTH_CLAUDE_SCOPE", "profile inference")
	t.Setenv("CLIPAL_OAUTH_CLAUDE_CALLBACK_HOST", "127.0.0.1")
	t.Setenv("CLIPAL_OAUTH_CLAUDE_CALLBACK_PORT", "18081")
	t.Setenv("CLIPAL_OAUTH_CLAUDE_CALLBACK_PATH", "oauth/callback")

	overrides := NewClaudeClient()
	if got := overrides.authURL(); got != "http://127.0.0.1:18080/oauth/authorize" {
		t.Fatalf("authURL override = %q", got)
	}
	if got := overrides.tokenURL(); got != "http://127.0.0.1:18080/oauth/token" {
		t.Fatalf("tokenURL override = %q", got)
	}
	if got := overrides.clientID(); got != "claude-dev-client" {
		t.Fatalf("clientID override = %q", got)
	}
	if got := overrides.scope(); got != "profile inference" {
		t.Fatalf("scope override = %q", got)
	}
	if got := overrides.callbackHost(); got != "127.0.0.1" {
		t.Fatalf("callbackHost override = %q", got)
	}
	if got := overrides.callbackPort(); got != 18081 {
		t.Fatalf("callbackPort override = %d", got)
	}
	if got := overrides.callbackPath(); got != "/oauth/callback" {
		t.Fatalf("callbackPath override = %q", got)
	}

	t.Setenv("CLIPAL_OAUTH_CLAUDE_CALLBACK_PORT", "invalid")
	invalidPort := NewClaudeClient()
	if got := invalidPort.callbackPort(); got != defaultClaudeCallbackPort {
		t.Fatalf("callbackPort = %d, want %d", got, defaultClaudeCallbackPort)
	}
}

func TestClaudeExchangeCodeUsesStateFragmentFromCode(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("io.ReadAll: %v", err)
		}
		var req claudeTokenRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("json.Unmarshal: %v", err)
		}
		if req.Code != "auth-code" {
			t.Fatalf("code = %q, want auth-code", req.Code)
		}
		if req.State != "fragment-state" {
			t.Fatalf("state = %q, want fragment-state", req.State)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"access-1","refresh_token":"refresh-1","expires_in":60,"account":{"email_address":"sean@example.com","uuid":"acct_123"},"organization":{"uuid":"org_123"}}`)
	}))
	defer tokenServer.Close()

	client := &ClaudeClient{
		TokenURL:   tokenServer.URL,
		ClientID:   "test-client",
		HTTPClient: tokenServer.Client(),
		Now:        func() time.Time { return time.Date(2026, 4, 22, 9, 30, 0, 0, time.UTC) },
	}

	if _, err := client.ExchangeCode(context.Background(), "auth-code#fragment-state", "session-123", "http://localhost:54545/callback", PKCECodes{
		CodeVerifier:  "verifier-123",
		CodeChallenge: "challenge-123",
	}); err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
}
