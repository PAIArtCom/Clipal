package oauth

import (
	"encoding/base64"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/PAIArtCom/Clipal/internal/config"
)

func TestParseCLIProxyAPICredential_Codex(t *testing.T) {
	cred, err := ParseCLIProxyAPICredential([]byte(`{
  "type": "codex",
  "email": "sean@example.com",
  "account_id": "acct_123",
  "access_token": "access-1",
  "refresh_token": "refresh-1",
  "id_token": "` + testOAuthJWT("sean@example.com", "acct_123") + `",
  "expired": "2026-04-29T11:54:11+08:00",
  "last_refresh": "2026-04-21T11:54:11+08:00",
  "disabled": false
}`))
	if err != nil {
		t.Fatalf("ParseCLIProxyAPICredential: %v", err)
	}

	if got := cred.Provider; got != config.OAuthProviderCodex {
		t.Fatalf("provider = %q, want codex", got)
	}
	if got := cred.Ref; got != "codex-sean-example-com" {
		t.Fatalf("ref = %q, want codex-sean-example-com", got)
	}
	if got := cred.Email; got != "sean@example.com" {
		t.Fatalf("email = %q", got)
	}
	if got := cred.AccountID; got != "acct_123" {
		t.Fatalf("account_id = %q", got)
	}
	if got := cred.Metadata["id_token"]; got == "" {
		t.Fatalf("expected id_token metadata to be preserved")
	}
	wantExpiresAt := time.Date(2026, 4, 29, 3, 54, 11, 0, time.UTC)
	if !cred.ExpiresAt.Equal(wantExpiresAt) {
		t.Fatalf("expires_at = %s, want %s", cred.ExpiresAt, wantExpiresAt)
	}
	wantLastRefresh := time.Date(2026, 4, 21, 3, 54, 11, 0, time.UTC)
	if !cred.LastRefresh.Equal(wantLastRefresh) {
		t.Fatalf("last_refresh = %s, want %s", cred.LastRefresh, wantLastRefresh)
	}
}

func TestParseCLIProxyAPICredential_Claude(t *testing.T) {
	cred, err := ParseCLIProxyAPICredential([]byte(`{
  "type": "claude",
  "email": "sean@example.com",
  "account": {
    "uuid": "acct_123",
    "email_address": "sean@example.com"
  },
  "organization": {
    "uuid": "org_123",
    "name": "Anthropic"
  },
  "access_token": "access-1",
  "refresh_token": "refresh-1",
  "expired": "2026-04-29T11:54:11+08:00",
  "last_refresh": "2026-04-21T11:54:11+08:00",
  "disabled": false
}`))
	if err != nil {
		t.Fatalf("ParseCLIProxyAPICredential: %v", err)
	}

	if got := cred.Provider; got != config.OAuthProviderClaude {
		t.Fatalf("provider = %q, want claude", got)
	}
	if got := cred.Ref; got != "claude-sean-example-com" {
		t.Fatalf("ref = %q, want claude-sean-example-com", got)
	}
	if got := cred.Email; got != "sean@example.com" {
		t.Fatalf("email = %q", got)
	}
	if got := cred.AccountID; got != "acct_123" {
		t.Fatalf("account_id = %q, want acct_123", got)
	}
	if got := cred.Metadata["organization_id"]; got != "org_123" {
		t.Fatalf("metadata organization_id = %q, want org_123", got)
	}
	if got := cred.Metadata["organization_name"]; got != "Anthropic" {
		t.Fatalf("metadata organization_name = %q, want Anthropic", got)
	}
	wantExpiresAt := time.Date(2026, 4, 29, 3, 54, 11, 0, time.UTC)
	if !cred.ExpiresAt.Equal(wantExpiresAt) {
		t.Fatalf("expires_at = %s, want %s", cred.ExpiresAt, wantExpiresAt)
	}
	wantLastRefresh := time.Date(2026, 4, 21, 3, 54, 11, 0, time.UTC)
	if !cred.LastRefresh.Equal(wantLastRefresh) {
		t.Fatalf("last_refresh = %s, want %s", cred.LastRefresh, wantLastRefresh)
	}
}

func TestParseCLIProxyAPICredential_Gemini(t *testing.T) {
	cred, err := ParseCLIProxyAPICredential([]byte(`{
  "type": "gemini",
  "email": "sean@example.com",
  "project_id": "gen-lang-client-123",
  "access_token": "access-1",
  "refresh_token": "refresh-1",
  "expiry": "2026-04-29T11:54:11+08:00",
  "last_refresh": "2026-04-21T11:54:11+08:00",
  "token": {
    "access_token": "access-1",
    "refresh_token": "refresh-1",
    "token_type": "Bearer"
  },
  "token_type": "Bearer",
  "auto": true,
  "checked": true,
  "disabled": false
}`))
	if err != nil {
		t.Fatalf("ParseCLIProxyAPICredential: %v", err)
	}

	if got := cred.Provider; got != config.OAuthProviderGemini {
		t.Fatalf("provider = %q, want gemini", got)
	}
	if got := cred.Ref; got != "gemini-sean-example-com-gen-lang-client-123" {
		t.Fatalf("ref = %q, want gemini-sean-example-com-gen-lang-client-123", got)
	}
	if got := cred.Email; got != "sean@example.com" {
		t.Fatalf("email = %q", got)
	}
	if got := cred.AccountID; got != "gen-lang-client-123" {
		t.Fatalf("account_id = %q", got)
	}
	if got := cred.Metadata["project_id"]; got != "gen-lang-client-123" {
		t.Fatalf("metadata project_id = %q", got)
	}
	if got := cred.Metadata["auto_project"]; got != "true" {
		t.Fatalf("metadata auto_project = %q", got)
	}
	if got := cred.Metadata["checked"]; got != "true" {
		t.Fatalf("metadata checked = %q", got)
	}
	if got := cred.Metadata["token_type"]; got != "Bearer" {
		t.Fatalf("metadata token_type = %q", got)
	}
	if got := cred.Metadata["token_json"]; got == "" {
		t.Fatalf("expected token_json metadata to be preserved")
	}
	wantExpiresAt := time.Date(2026, 4, 29, 3, 54, 11, 0, time.UTC)
	if !cred.ExpiresAt.Equal(wantExpiresAt) {
		t.Fatalf("expires_at = %s, want %s", cred.ExpiresAt, wantExpiresAt)
	}
	wantLastRefresh := time.Date(2026, 4, 21, 3, 54, 11, 0, time.UTC)
	if !cred.LastRefresh.Equal(wantLastRefresh) {
		t.Fatalf("last_refresh = %s, want %s", cred.LastRefresh, wantLastRefresh)
	}
}

func TestParseCLIProxyAPICredential_FillsIdentityFromIDToken(t *testing.T) {
	cred, err := ParseCLIProxyAPICredential([]byte(`{
  "type": "codex",
  "access_token": "access-1",
  "refresh_token": "refresh-1",
  "id_token": "` + testOAuthJWT("sean@example.com", "acct_123") + `"
}`))
	if err != nil {
		t.Fatalf("ParseCLIProxyAPICredential: %v", err)
	}
	if got := cred.Email; got != "sean@example.com" {
		t.Fatalf("email = %q", got)
	}
	if got := cred.AccountID; got != "acct_123" {
		t.Fatalf("account_id = %q", got)
	}
	if got := cred.Ref; got != "codex-sean-example-com" {
		t.Fatalf("ref = %q", got)
	}
}

func TestParseCLIProxyAPICredential_CodexSupportsSub2APIFields(t *testing.T) {
	cred, err := ParseCLIProxyAPICredential([]byte(`{
  "type": "codex",
  "access_token": "access-1",
  "refresh_token": "refresh-1",
  "chatgpt_account_id": "acct_123",
  "chatgpt_user_id": "user_123",
  "organization_id": "org_123",
  "plan_type": "plus",
  "client_id": "client_123",
  "expires_at": 1777776981
}`))
	if err != nil {
		t.Fatalf("ParseCLIProxyAPICredential: %v", err)
	}
	if got := cred.Ref; got != "codex-acct-123" {
		t.Fatalf("ref = %q, want codex-acct-123", got)
	}
	if got := cred.AccountID; got != "acct_123" {
		t.Fatalf("account_id = %q, want acct_123", got)
	}
	if got := cred.Metadata["plan_type"]; got != "plus" {
		t.Fatalf("plan_type = %q, want plus", got)
	}
	if got := cred.Metadata["chatgpt_user_id"]; got != "user_123" {
		t.Fatalf("chatgpt_user_id = %q, want user_123", got)
	}
	wantExpiresAt := time.Unix(1777776981, 0).UTC()
	if !cred.ExpiresAt.Equal(wantExpiresAt) {
		t.Fatalf("expires_at = %s, want %s", cred.ExpiresAt, wantExpiresAt)
	}
}

func TestParseCodexNativeAuthCredential(t *testing.T) {
	accessToken := testOAuthJWTWithExp("sean@example.com", "acct_ignored", time.Date(2026, 5, 3, 12, 34, 56, 0, time.UTC))
	cred, err := ParseCodexNativeAuthCredential([]byte(`{
  "auth_mode": "chatgpt",
  "OPENAI_API_KEY": null,
  "tokens": {
    "id_token": "` + testOAuthJWTWithPlan("sean@example.com", "acct_123", "plus") + `",
    "access_token": "` + accessToken + `",
    "refresh_token": "refresh-1",
    "account_id": "acct_123"
  },
  "last_refresh": "2026-04-29T14:12:58.368988800Z"
}`))
	if err != nil {
		t.Fatalf("ParseCodexNativeAuthCredential: %v", err)
	}
	if got := cred.Provider; got != config.OAuthProviderCodex {
		t.Fatalf("provider = %q, want codex", got)
	}
	if got := cred.Ref; got != "codex-sean-example-com" {
		t.Fatalf("ref = %q, want codex-sean-example-com", got)
	}
	if got := cred.Email; got != "sean@example.com" {
		t.Fatalf("email = %q", got)
	}
	if got := cred.AccountID; got != "acct_123" {
		t.Fatalf("account_id = %q", got)
	}
	if got := cred.AccessToken; got != accessToken {
		t.Fatalf("access_token was not preserved")
	}
	if got := cred.RefreshToken; got != "refresh-1" {
		t.Fatalf("refresh_token = %q, want refresh-1", got)
	}
	if got := cred.Metadata["id_token"]; got == "" {
		t.Fatalf("expected id_token metadata to be preserved")
	}
	if got := cred.Metadata["auth_mode"]; got != "chatgpt" {
		t.Fatalf("auth_mode = %q, want chatgpt", got)
	}
	if got := cred.Metadata["plan_type"]; got != "plus" {
		t.Fatalf("plan_type = %q, want plus", got)
	}
	wantExpiresAt := time.Date(2026, 5, 3, 12, 34, 56, 0, time.UTC)
	if !cred.ExpiresAt.Equal(wantExpiresAt) {
		t.Fatalf("expires_at = %s, want %s", cred.ExpiresAt, wantExpiresAt)
	}
	wantLastRefresh := time.Date(2026, 4, 29, 14, 12, 58, 368988800, time.UTC)
	if !cred.LastRefresh.Equal(wantLastRefresh) {
		t.Fatalf("last_refresh = %s, want %s", cred.LastRefresh, wantLastRefresh)
	}
}

func TestParseOAuthImportEntries_CodexNativeAuthJSON(t *testing.T) {
	results, err := ParseOAuthImportEntries([]byte(`{
  "auth_mode": "chatgpt",
  "tokens": {
    "id_token": "` + testOAuthJWT("sean@example.com", "acct_123") + `",
    "access_token": "` + testOAuthJWTWithExp("sean@example.com", "acct_123", time.Date(2026, 5, 3, 12, 34, 56, 0, time.UTC)) + `",
    "refresh_token": "refresh-1"
  }
}`))
	if err != nil {
		t.Fatalf("ParseOAuthImportEntries: %v", err)
	}
	if len(results) != 1 || results[0].Err != nil || results[0].Credential == nil {
		t.Fatalf("results = %#v", results)
	}
	if got := results[0].Credential.Ref; got != "codex-sean-example-com" {
		t.Fatalf("ref = %q, want codex-sean-example-com", got)
	}
}

func TestParseOAuthImportEntries_Sub2APIBundle(t *testing.T) {
	results, err := ParseOAuthImportEntries([]byte(`{
  "exported_at": "2026-04-23T09:35:45Z",
  "accounts": [
    {
      "name": "OpenAI OAuth",
      "platform": "openai",
      "type": "oauth",
      "credentials": {
        "access_token": "access-1",
        "refresh_token": "refresh-1",
        "chatgpt_account_id": "acct_123",
        "expires_at": 1777776981,
        "plan_type": "plus"
      }
    },
    {
      "name": "API Key",
      "platform": "openai",
      "type": "apikey",
      "credentials": {
        "api_key": "sk-test"
      }
    }
  ]
}`))
	if err != nil {
		t.Fatalf("ParseOAuthImportEntries: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results len = %d, want 2", len(results))
	}
	if got := results[0].Entry; got != "accounts[0]" {
		t.Fatalf("entry = %q, want accounts[0]", got)
	}
	if results[0].Err != nil {
		t.Fatalf("results[0].Err = %v, want nil", results[0].Err)
	}
	if results[0].Credential == nil {
		t.Fatalf("results[0].Credential = nil")
	}
	if got := results[0].Credential.Provider; got != config.OAuthProviderCodex {
		t.Fatalf("provider = %q, want codex", got)
	}
	if got := results[0].Credential.AccountID; got != "acct_123" {
		t.Fatalf("account_id = %q, want acct_123", got)
	}
	if !errors.Is(results[1].Err, ErrCLIProxyAPINotCredential) {
		t.Fatalf("results[1].Err = %v, want ErrCLIProxyAPINotCredential", results[1].Err)
	}
}

func TestParseOAuthImportEntries_Sub2APIBundleGeminiWithoutEmail(t *testing.T) {
	results, err := ParseOAuthImportEntries([]byte(`{
  "exported_at": "2026-04-23T09:35:45Z",
  "accounts": [
    {
      "name": "Gemini OAuth",
      "platform": "gemini",
      "type": "oauth",
      "credentials": {
        "access_token": "access-1",
        "refresh_token": "refresh-1",
        "project_id": "proj-123",
        "oauth_type": "code_assist",
        "expires_at": "2026-04-29T11:54:11+08:00"
      }
    }
  ]
}`))
	if err != nil {
		t.Fatalf("ParseOAuthImportEntries: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	if results[0].Err != nil {
		t.Fatalf("results[0].Err = %v, want nil", results[0].Err)
	}
	if results[0].Credential == nil {
		t.Fatalf("results[0].Credential = nil")
	}
	if got := results[0].Credential.Provider; got != config.OAuthProviderGemini {
		t.Fatalf("provider = %q, want gemini", got)
	}
	if got := results[0].Credential.AccountID; got != "proj-123" {
		t.Fatalf("account_id = %q, want proj-123", got)
	}
}

func TestParseOAuthImportEntries_Sub2APIBundleClaudeWithoutEmail(t *testing.T) {
	results, err := ParseOAuthImportEntries([]byte(`{
  "exported_at": "2026-04-23T09:35:45Z",
  "accounts": [
    {
      "name": "Claude Team A",
      "platform": "anthropic",
      "type": "oauth",
      "credentials": {
        "access_token": "access-1",
        "refresh_token": "refresh-1",
        "expires_at": "2026-04-29T11:54:11+08:00"
      }
    }
  ]
}`))
	if err != nil {
		t.Fatalf("ParseOAuthImportEntries: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	if results[0].Credential != nil {
		t.Fatalf("results[0].Credential = %#v, want nil", results[0].Credential)
	}
	if got := results[0].Err; got == nil || got.Error() != "claude credential missing email/account_id/organization_id" {
		t.Fatalf("results[0].Err = %v, want missing identity error", got)
	}
}

func TestParseCLIProxyAPICredential_SkipCases(t *testing.T) {
	tests := []struct {
		name string
		body string
		want error
	}{
		{
			name: "missing type",
			body: `{"access_token":"access-1"}`,
			want: ErrCLIProxyAPINotCredential,
		},
		{
			name: "unsupported type",
			body: `{"type":"vertex","access_token":"access-1"}`,
			want: ErrCLIProxyAPIUnsupportedType,
		},
		{
			name: "disabled",
			body: `{"type":"codex","access_token":"access-1","disabled":true}`,
			want: ErrCLIProxyAPIDisabledCredential,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseCLIProxyAPICredential([]byte(tc.body))
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func testOAuthJWT(email string, accountID string) string {
	return testOAuthJWTWithPlan(email, accountID, "")
}

func testOAuthJWTWithPlan(email string, accountID string, planType string) string {
	header := `{"alg":"none","typ":"JWT"}`
	auth := `"chatgpt_account_id":"` + accountID + `"`
	if planType != "" {
		auth += `,"chatgpt_plan_type":"` + planType + `"`
	}
	payload := `{"email":"` + email + `","sub":"sub_123","https://api.openai.com/auth":{` + auth + `}}`
	return base64.RawURLEncoding.EncodeToString([]byte(header)) + "." +
		base64.RawURLEncoding.EncodeToString([]byte(payload)) + "."
}

func testOAuthJWTWithExp(email string, accountID string, expiresAt time.Time) string {
	header := `{"alg":"none","typ":"JWT"}`
	payload := `{"email":"` + email + `","sub":"sub_123","exp":` + strconv.FormatInt(expiresAt.Unix(), 10) + `,"https://api.openai.com/auth":{"chatgpt_account_id":"` + accountID + `"}}`
	return base64.RawURLEncoding.EncodeToString([]byte(header)) + "." +
		base64.RawURLEncoding.EncodeToString([]byte(payload)) + "."
}
