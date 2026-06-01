package web

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
	oauthpkg "github.com/lansespirit/Clipal/internal/oauth"
	"github.com/lansespirit/Clipal/internal/telemetry"
	"github.com/lansespirit/Clipal/internal/testutil"
)

func TestHandleStartOAuthProvider_ReturnsAuthURLAndSessionID(t *testing.T) {
	api := newTestOAuthAPI(t)

	body := []byte(`{"client_type":"openai","provider":"codex"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/oauth/providers/start", bytes.NewReader(body))
	w := httptest.NewRecorder()
	api.HandleStartOAuthProvider(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	got := testutil.DecodeJSONMap(t, w.Body.Bytes())
	if got["provider"] != "codex" {
		t.Fatalf("provider = %v, want codex", got["provider"])
	}
	if got["session_id"] == "" {
		t.Fatalf("expected session_id, got %#v", got)
	}
	authURL, _ := got["auth_url"].(string)
	if authURL == "" {
		t.Fatalf("expected auth_url, got %#v", got)
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	if parsed.Query().Get("redirect_uri") == "" {
		t.Fatalf("expected redirect_uri in auth_url, got %s", authURL)
	}
}

func TestHandleStartOAuthProvider_RejectsInvalidProxySettings(t *testing.T) {
	api := newTestOAuthAPI(t)

	body := []byte(`{"client_type":"openai","provider":"codex","proxy_mode":"custom","proxy_url":"ftp://127.0.0.1:21"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/oauth/providers/start", bytes.NewReader(body))
	w := httptest.NewRecorder()
	api.HandleStartOAuthProvider(w, req)
	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}
}

func TestHandleCancelOAuthSession_ReleasesCallbackAndDeletesTarget(t *testing.T) {
	api := newTestOAuthAPI(t)
	start := startOAuthSessionFor(t, api, "openai", "codex")
	parsedAuthURL, err := url.Parse(start.AuthURL)
	if err != nil {
		t.Fatalf("url.Parse auth_url: %v", err)
	}
	redirectURI := parsedAuthURL.Query().Get("redirect_uri")
	parsedRedirect, err := url.Parse(redirectURI)
	if err != nil {
		t.Fatalf("url.Parse redirect_uri: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/oauth/sessions/"+start.SessionID+"/cancel", nil)
	w := httptest.NewRecorder()
	api.HandleGetOAuthSession(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("cancel status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}
	got := testutil.DecodeJSONMap(t, w.Body.Bytes())
	if got["status"] != "error" {
		t.Fatalf("status = %v, want error", got["status"])
	}
	if got["error"] != "oauth session cancelled" {
		t.Fatalf("error = %v, want oauth session cancelled", got["error"])
	}
	if _, ok := api.getOAuthTargetClient(start.SessionID); ok {
		t.Fatalf("expected oauth target to be deleted after cancel")
	}
	ln, err := net.Listen("tcp", parsedRedirect.Host)
	if err != nil {
		t.Fatalf("net.Listen: callback port still busy after cancel: %v", err)
	}
	_ = ln.Close()
}

func TestOAuthProxySettingsFromStartRequest_DefaultIsNotConfigured(t *testing.T) {
	mode := "default"
	settings, err := oauthProxySettingsFromStartRequest(OAuthStartRequest{
		ClientType: "claude",
		Provider:   config.OAuthProviderClaude,
		ProxyMode:  &mode,
	})
	if err != nil {
		t.Fatalf("oauthProxySettingsFromStartRequest: %v", err)
	}
	if settings.Configured {
		t.Fatalf("settings.Configured = true, want false")
	}
}

func TestHandleGetOAuthSession_CreatesProviderWithOAuthStartProxySettings(t *testing.T) {
	now := time.Date(2026, 4, 18, 21, 10, 0, 0, time.UTC)
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(`{"access_token":"access-1","refresh_token":"refresh-1","id_token":"%s","expires_in":3600}`, testOAuthJWT("sean@example.com", "acct_123")))
	}))
	defer tokenServer.Close()

	api := newTestOAuthAPI(t, oauthpkg.WithCodexClient(&oauthpkg.CodexClient{
		AuthURL:      "https://auth.openai.com/oauth/authorize",
		TokenURL:     tokenServer.URL,
		ClientID:     "test-client",
		CallbackHost: "127.0.0.1",
		CallbackPort: 0,
		CallbackPath: "/auth/callback",
		HTTPClient:   tokenServer.Client(),
		Now:          func() time.Time { return now },
	}))

	body := []byte(fmt.Sprintf(`{"client_type":"openai","provider":"codex","proxy_mode":"custom","proxy_url":%q}`, tokenServer.URL))
	req := httptest.NewRequest(http.MethodPost, "/api/oauth/providers/start", bytes.NewReader(body))
	w := httptest.NewRecorder()
	api.HandleStartOAuthProvider(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("start status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}
	got := testutil.DecodeJSONMap(t, w.Body.Bytes())
	sessionID, ok := got["session_id"].(string)
	if !ok || sessionID == "" {
		t.Fatalf("session_id = %v", got["session_id"])
	}
	authURL, ok := got["auth_url"].(string)
	if !ok || authURL == "" {
		t.Fatalf("auth_url = %v", got["auth_url"])
	}
	start := startedOAuthSession{
		SessionID: sessionID,
		AuthURL:   authURL,
	}
	completeOAuthCallback(t, start)
	linkOAuthSessionFor(t, api, start.SessionID, "openai")

	cfg, err := config.Load(api.configDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if len(cfg.OpenAI.Providers) != 1 {
		t.Fatalf("providers len = %d, want 1", len(cfg.OpenAI.Providers))
	}
	provider := cfg.OpenAI.Providers[0]
	if got := provider.NormalizedProxyMode(); got != config.ProviderProxyModeCustom {
		t.Fatalf("proxy_mode = %q, want custom", got)
	}
	if got := provider.NormalizedProxyURL(); got != tokenServer.URL {
		t.Fatalf("proxy_url = %q, want %q", got, tokenServer.URL)
	}
}

func TestHandleListOAuthProviders_ReturnsAvailableProvidersForClient(t *testing.T) {
	api := newTestOAuthAPI(t)

	req := httptest.NewRequest(http.MethodGet, "/api/oauth/providers?client_type=openai", nil)
	w := httptest.NewRecorder()
	api.HandleListOAuthProviders(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	var got []OAuthProviderOptionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(got) != 1 || got[0].Provider != config.OAuthProviderCodex {
		t.Fatalf("providers = %#v, want [codex]", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/oauth/providers?client_type=claude", nil)
	w = httptest.NewRecorder()
	api.HandleListOAuthProviders(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("claude status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("json.Unmarshal claude: %v", err)
	}
	if len(got) != 1 || got[0].Provider != config.OAuthProviderClaude {
		t.Fatalf("providers = %#v, want [claude]", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/oauth/providers?client_type=gemini", nil)
	w = httptest.NewRecorder()
	api.HandleListOAuthProviders(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("gemini status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("json.Unmarshal gemini: %v", err)
	}
	if len(got) != 1 || got[0].Provider != config.OAuthProviderGemini {
		t.Fatalf("providers = %#v, want [gemini]", got)
	}
}

func TestHandleGetOAuthSession_CompletedFlowRequiresExplicitLink(t *testing.T) {
	now := time.Date(2026, 4, 18, 21, 0, 0, 0, time.UTC)
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if got := r.Form.Get("grant_type"); got != "authorization_code" {
			t.Fatalf("grant_type = %q, want authorization_code", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(`{"access_token":"access-1","refresh_token":"refresh-1","id_token":"%s","expires_in":3600}`, testOAuthJWT("sean@example.com", "acct_123")))
	}))
	defer tokenServer.Close()

	api := newTestOAuthAPI(t, oauthpkg.WithCodexClient(&oauthpkg.CodexClient{
		AuthURL:      "https://auth.openai.com/oauth/authorize",
		TokenURL:     tokenServer.URL,
		ClientID:     "test-client",
		CallbackHost: "127.0.0.1",
		CallbackPort: 0,
		CallbackPath: "/auth/callback",
		HTTPClient:   tokenServer.Client(),
		Now:          func() time.Time { return now },
	}))

	start := startOAuthSessionFor(t, api, "openai", "codex")
	completeOAuthCallback(t, start)

	req := httptest.NewRequest(http.MethodGet, "/api/oauth/sessions/"+start.SessionID, nil)
	w := httptest.NewRecorder()
	api.HandleGetOAuthSession(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	got := testutil.DecodeJSONMap(t, w.Body.Bytes())
	if got["status"] != "completed" {
		t.Fatalf("status = %v, want completed", got["status"])
	}
	if got["provider_name"] != nil {
		t.Fatalf("provider_name = %v, want empty on read-only GET", got["provider_name"])
	}
	if got["display_name"] != "sean@example.com" {
		t.Fatalf("display_name = %v, want sean@example.com", got["display_name"])
	}

	cfg, err := config.Load(api.configDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if len(cfg.OpenAI.Providers) != 0 {
		t.Fatalf("providers len = %d, want 0 before link", len(cfg.OpenAI.Providers))
	}

	got = linkOAuthSessionFor(t, api, start.SessionID, "openai")
	if got["provider_name"] != "codex-sean-example-com" {
		t.Fatalf("provider_name = %v, want codex-sean-example-com", got["provider_name"])
	}
	if got["provider_action"] != "created" {
		t.Fatalf("provider_action = %v, want created", got["provider_action"])
	}
	cfg, err = config.Load(api.configDir)
	if err != nil {
		t.Fatalf("config.Load after link: %v", err)
	}
	if len(cfg.OpenAI.Providers) != 1 {
		t.Fatalf("providers len = %d, want 1 after link", len(cfg.OpenAI.Providers))
	}
	provider := cfg.OpenAI.Providers[0]
	if provider.Name != "codex-sean-example-com" {
		t.Fatalf("provider name = %q", provider.Name)
	}
	if got := provider.NormalizedAuthType(); got != config.ProviderAuthTypeOAuth {
		t.Fatalf("auth_type = %q, want oauth", got)
	}
	if got := provider.NormalizedOAuthRef(); got != "codex-sean-example-com" {
		t.Fatalf("oauth_ref = %q", got)
	}
	if _, ok := api.getOAuthTargetClient(start.SessionID); ok {
		t.Fatalf("expected oauth target for completed session to be cleaned up")
	}
}

func TestHandleGetOAuthSession_PollThenLinkPreservesOAuthStartProxySettings(t *testing.T) {
	now := time.Date(2026, 4, 18, 21, 15, 0, 0, time.UTC)
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(`{"access_token":"access-1","refresh_token":"refresh-1","id_token":"%s","expires_in":3600}`, testOAuthJWT("sean@example.com", "acct_123")))
	}))
	defer tokenServer.Close()

	api := newTestOAuthAPI(t, oauthpkg.WithCodexClient(&oauthpkg.CodexClient{
		AuthURL:      "https://auth.openai.com/oauth/authorize",
		TokenURL:     tokenServer.URL,
		ClientID:     "test-client",
		CallbackHost: "127.0.0.1",
		CallbackPort: 0,
		CallbackPath: "/auth/callback",
		HTTPClient:   tokenServer.Client(),
		Now:          func() time.Time { return now },
	}))

	body := []byte(fmt.Sprintf(`{"client_type":"openai","provider":"codex","proxy_mode":"custom","proxy_url":%q}`, tokenServer.URL))
	req := httptest.NewRequest(http.MethodPost, "/api/oauth/providers/start", bytes.NewReader(body))
	w := httptest.NewRecorder()
	api.HandleStartOAuthProvider(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("start status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}
	got := testutil.DecodeJSONMap(t, w.Body.Bytes())
	sessionID, ok := got["session_id"].(string)
	if !ok || sessionID == "" {
		t.Fatalf("session_id = %v", got["session_id"])
	}
	authURL, ok := got["auth_url"].(string)
	if !ok || authURL == "" {
		t.Fatalf("auth_url = %v", got["auth_url"])
	}
	start := startedOAuthSession{
		SessionID: sessionID,
		AuthURL:   authURL,
	}
	completeOAuthCallback(t, start)

	pollReq := httptest.NewRequest(http.MethodGet, "/api/oauth/sessions/"+start.SessionID, nil)
	pollW := httptest.NewRecorder()
	api.HandleGetOAuthSession(pollW, pollReq)
	if pollW.Result().StatusCode != http.StatusOK {
		t.Fatalf("poll status=%d body=%s", pollW.Result().StatusCode, pollW.Body.String())
	}
	poll := testutil.DecodeJSONMap(t, pollW.Body.Bytes())
	if poll["status"] != "completed" {
		t.Fatalf("poll status = %v, want completed", poll["status"])
	}

	linkOAuthSessionFor(t, api, start.SessionID, "openai")
	cfg, err := config.Load(api.configDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if len(cfg.OpenAI.Providers) != 1 {
		t.Fatalf("providers len = %d, want 1", len(cfg.OpenAI.Providers))
	}
	provider := cfg.OpenAI.Providers[0]
	if got := provider.NormalizedProxyMode(); got != config.ProviderProxyModeCustom {
		t.Fatalf("proxy_mode = %q, want custom", got)
	}
	if got := provider.NormalizedProxyURL(); got != tokenServer.URL {
		t.Fatalf("proxy_url = %q, want %q", got, tokenServer.URL)
	}
}

func TestOAuthHTTPClientForTarget_DefaultEnvironmentPreservesProviderClient(t *testing.T) {
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("ALL_PROXY", "")
	t.Setenv("NO_PROXY", "")
	t.Setenv("http_proxy", "")
	t.Setenv("https_proxy", "")
	t.Setenv("all_proxy", "")
	t.Setenv("no_proxy", "")
	api := newTestOAuthAPI(t)

	client := api.oauthHTTPClientForTarget(oauthTargetClient{
		ClientType: "claude",
		Provider:   config.OAuthProviderClaude,
	}, config.OAuthProviderClaude)
	if client != nil {
		t.Fatalf("oauthHTTPClientForTarget default = %T, want nil", client)
	}
}

func TestOAuthHTTPClientForTarget_ClaudeDirectPreservesProviderClient(t *testing.T) {
	api := newTestOAuthAPI(t)
	cfg, err := config.Load(api.configDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	cfg.Global.UpstreamProxyMode = config.GlobalUpstreamProxyModeDirect
	writeConfigFixture(t, api.configDir, cfg)

	client := api.oauthHTTPClientForTarget(oauthTargetClient{
		ClientType: "claude",
		Provider:   config.OAuthProviderClaude,
	}, config.OAuthProviderClaude)
	if client != nil {
		t.Fatalf("global direct oauthHTTPClientForTarget = %T, want nil", client)
	}

	client = api.oauthHTTPClientForTarget(oauthTargetClient{
		ClientType:      "claude",
		Provider:        config.OAuthProviderClaude,
		ProxyConfigured: true,
		ProxyMode:       config.ProviderProxyModeDirect,
	}, config.OAuthProviderClaude)
	if client != nil {
		t.Fatalf("provider direct oauthHTTPClientForTarget = %T, want nil", client)
	}
}

func TestHandleSubmitOAuthSessionCode_AutoCreatesGeminiProvider(t *testing.T) {
	now := time.Date(2026, 4, 22, 10, 15, 0, 0, time.UTC)
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if got := r.Form.Get("grant_type"); got != "authorization_code" {
			t.Fatalf("grant_type = %q, want authorization_code", got)
		}
		if got := r.Form.Get("code"); got != "manual-code" {
			t.Fatalf("code = %q, want manual-code", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"access-1","refresh_token":"refresh-1","expires_in":3600,"token_type":"Bearer"}`)
	}))
	defer tokenServer.Close()

	userInfoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"email":"sean@example.com"}`)
	}))
	defer userInfoServer.Close()

	cloudCodeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1internal:loadCodeAssist":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"allowedTiers":[{"id":"tier-default","isDefault":true}],"cloudaicompanionProject":{"id":"gen-lang-client-123"}}`)
		case "/v1internal:onboardUser":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"done":true,"response":{"cloudaicompanionProject":{"id":"gen-lang-client-123"}}}`)
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer cloudCodeServer.Close()

	api := newTestOAuthAPI(t, oauthpkg.WithGeminiClient(&oauthpkg.GeminiClient{
		AuthURL:      "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:     tokenServer.URL,
		UserInfoURL:  userInfoServer.URL,
		CloudCodeURL: cloudCodeServer.URL,
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		CallbackHost: "127.0.0.1",
		CallbackPort: 0,
		CallbackPath: "/oauth2callback",
		HTTPClient:   tokenServer.Client(),
		Now:          func() time.Time { return now },
		Sleep:        func(time.Duration) {},
	}))

	start := startOAuthSessionFor(t, api, "gemini", "gemini")
	body := []byte(`{"code":"manual-code"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/oauth/sessions/"+start.SessionID+"/code", bytes.NewReader(body))
	w := httptest.NewRecorder()
	api.HandleGetOAuthSession(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	got := testutil.DecodeJSONMap(t, w.Body.Bytes())
	if got["status"] != "completed" {
		t.Fatalf("status = %v, want completed", got["status"])
	}
	if got["provider_name"] != "gemini-sean-example-com-gen-lang-client-123" {
		t.Fatalf("provider_name = %v, want gemini-sean-example-com-gen-lang-client-123", got["provider_name"])
	}
	if got["display_name"] != "sean@example.com" {
		t.Fatalf("display_name = %v, want sean@example.com", got["display_name"])
	}

	cfg, err := config.Load(api.configDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if len(cfg.Gemini.Providers) != 1 {
		t.Fatalf("providers len = %d, want 1", len(cfg.Gemini.Providers))
	}
	if got := cfg.Gemini.Providers[0].Name; got != "gemini-sean-example-com-gen-lang-client-123" {
		t.Fatalf("provider name = %q, want gemini-sean-example-com-gen-lang-client-123", got)
	}
}

func TestHandleSubmitOAuthSessionCode_AcceptsCallbackURLInput(t *testing.T) {
	now := time.Date(2026, 4, 22, 10, 15, 0, 0, time.UTC)
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if got := r.Form.Get("grant_type"); got != "authorization_code" {
			t.Fatalf("grant_type = %q, want authorization_code", got)
		}
		if got := r.Form.Get("code"); got != "manual-code" {
			t.Fatalf("code = %q, want manual-code", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"access-1","refresh_token":"refresh-1","expires_in":3600,"token_type":"Bearer"}`)
	}))
	defer tokenServer.Close()

	userInfoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"email":"sean@example.com"}`)
	}))
	defer userInfoServer.Close()

	cloudCodeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1internal:loadCodeAssist":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"allowedTiers":[{"id":"tier-default","isDefault":true}],"cloudaicompanionProject":{"id":"gen-lang-client-123"}}`)
		case "/v1internal:onboardUser":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"done":true,"response":{"cloudaicompanionProject":{"id":"gen-lang-client-123"}}}`)
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer cloudCodeServer.Close()

	api := newTestOAuthAPI(t, oauthpkg.WithGeminiClient(&oauthpkg.GeminiClient{
		AuthURL:      "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:     tokenServer.URL,
		UserInfoURL:  userInfoServer.URL,
		CloudCodeURL: cloudCodeServer.URL,
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		CallbackHost: "127.0.0.1",
		CallbackPort: 0,
		CallbackPath: "/oauth2callback",
		HTTPClient:   tokenServer.Client(),
		Now:          func() time.Time { return now },
		Sleep:        func(time.Duration) {},
	}))

	start := startOAuthSessionFor(t, api, "gemini", "gemini")
	callbackURL := fmt.Sprintf("http://127.0.0.1:39393/oauth2callback?code=manual-code&state=%s", start.SessionID)
	body := []byte(fmt.Sprintf(`{"code":%q}`, callbackURL))
	req := httptest.NewRequest(http.MethodPost, "/api/oauth/sessions/"+start.SessionID+"/code", bytes.NewReader(body))
	w := httptest.NewRecorder()
	api.HandleGetOAuthSession(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	got := testutil.DecodeJSONMap(t, w.Body.Bytes())
	if got["status"] != "completed" {
		t.Fatalf("status = %v, want completed", got["status"])
	}
}

func TestHandleSubmitOAuthSessionCode_ClaudeRejectsRawCodeWithoutState(t *testing.T) {
	now := time.Date(2026, 4, 22, 10, 20, 0, 0, time.UTC)
	api := newTestOAuthAPI(t, oauthpkg.WithClaudeClient(&oauthpkg.ClaudeClient{
		AuthURL:      "https://platform.claude.com/oauth/authorize",
		TokenURL:     "https://platform.claude.com/v1/oauth/token",
		ClientID:     "test-client",
		CallbackHost: "127.0.0.1",
		CallbackPort: 0,
		CallbackPath: "/callback",
		Now:          func() time.Time { return now },
	}))

	start := startOAuthSessionFor(t, api, "claude", "claude")
	body := []byte(`{"code":"manual-code"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/oauth/sessions/"+start.SessionID+"/code", bytes.NewReader(body))
	w := httptest.NewRecorder()
	api.HandleGetOAuthSession(w, req)
	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "requires the full callback URL") {
		t.Fatalf("body = %q, want missing-state guidance", w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/oauth/sessions/"+start.SessionID, nil)
	w = httptest.NewRecorder()
	api.HandleGetOAuthSession(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("poll status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}
	got := testutil.DecodeJSONMap(t, w.Body.Bytes())
	if got["status"] != "pending" {
		t.Fatalf("status = %v, want pending", got["status"])
	}
}

func TestHandleSubmitOAuthSessionCode_ReturnsNotFoundForUnknownSession(t *testing.T) {
	api := newTestOAuthAPI(t)

	body := []byte(`{"code":"manual-code"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/oauth/sessions/missing-session/code", bytes.NewReader(body))
	w := httptest.NewRecorder()
	api.HandleGetOAuthSession(w, req)
	if w.Result().StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}
}

func TestHandleSubmitOAuthSessionCode_ReturnsSessionErrorOnExchangeFailure(t *testing.T) {
	api := newTestOAuthAPI(t, oauthpkg.WithCodexClient(&oauthpkg.CodexClient{
		AuthURL:      "https://auth.openai.com/oauth/authorize",
		TokenURL:     "http://127.0.0.1:1/unreachable",
		ClientID:     "test-client",
		CallbackHost: "127.0.0.1",
		CallbackPort: 0,
		CallbackPath: "/auth/callback",
		HTTPClient:   &http.Client{Timeout: 100 * time.Millisecond},
		Now:          func() time.Time { return time.Date(2026, 4, 22, 10, 30, 0, 0, time.UTC) },
	}))

	start := startOAuthSessionFor(t, api, "openai", "codex")
	body := []byte(`{"code":"manual-code"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/oauth/sessions/"+start.SessionID+"/code", bytes.NewReader(body))
	w := httptest.NewRecorder()
	api.HandleGetOAuthSession(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	got := testutil.DecodeJSONMap(t, w.Body.Bytes())
	if got["status"] != "error" {
		t.Fatalf("status = %v, want error", got["status"])
	}
	errText, _ := got["error"].(string)
	if errText == "" {
		t.Fatalf("expected error message, got %#v", got)
	}
}

func TestHandleSubmitOAuthSessionCode_ReturnsBadRequestForInvalidCallbackInput(t *testing.T) {
	api := newTestOAuthAPI(t, oauthpkg.WithCodexClient(&oauthpkg.CodexClient{
		AuthURL:      "https://auth.openai.com/oauth/authorize",
		TokenURL:     "http://127.0.0.1:1/unreachable",
		ClientID:     "test-client",
		CallbackHost: "127.0.0.1",
		CallbackPort: 0,
		CallbackPath: "/auth/callback",
		HTTPClient:   &http.Client{Timeout: 100 * time.Millisecond},
		Now:          func() time.Time { return time.Date(2026, 4, 22, 10, 30, 0, 0, time.UTC) },
	}))

	start := startOAuthSessionFor(t, api, "openai", "codex")
	body := []byte(`{"code":"http://127.0.0.1:54545/auth/callback?foo=bar"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/oauth/sessions/"+start.SessionID+"/code", bytes.NewReader(body))
	w := httptest.NewRecorder()
	api.HandleGetOAuthSession(w, req)
	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}
}

func TestEnsureOAuthProviderLinked_GeneratesNameFromEmailInsteadOfRef(t *testing.T) {
	cc := &config.ClientConfig{}
	cred := &oauthpkg.Credential{
		Ref:      "codex-acct-123",
		Provider: config.OAuthProviderCodex,
		Email:    "sean@example.com",
	}

	link := ensureOAuthProviderLinked(cc, cred, nil)
	if !link.Changed {
		t.Fatalf("expected provider to be created")
	}
	if got := link.Action; got != oauthProviderActionCreated {
		t.Fatalf("action = %q, want created", got)
	}
	if got := link.Provider.Name; got != "codex-sean-example-com" {
		t.Fatalf("provider name = %q, want codex-sean-example-com", got)
	}
	if got := link.Provider.NormalizedOAuthRef(); got != "codex-acct-123" {
		t.Fatalf("oauth_ref = %q, want codex-acct-123", got)
	}
}

func TestEnsureOAuthProviderLinked_RelinksBrokenProviderNameWhenCredentialFileIsMissing(t *testing.T) {
	cc := &config.ClientConfig{
		Providers: []config.Provider{
			{
				Name:          "codex-sean-example-com",
				AuthType:      config.ProviderAuthTypeOAuth,
				OAuthProvider: config.OAuthProviderCodex,
				OAuthRef:      "codex-stale-ref",
				OAuthIdentity: "acct:acct_123",
				Priority:      1,
				Enabled:       ptr(true),
			},
		},
	}
	cred := &oauthpkg.Credential{
		Ref:       "codex-sean-example-com",
		Provider:  config.OAuthProviderCodex,
		Email:     "sean@example.com",
		AccountID: "acct_123",
	}

	link := ensureOAuthProviderLinked(cc, cred, func(config.OAuthProvider, string) (*oauthpkg.Credential, error) {
		return nil, os.ErrNotExist
	})
	if !link.Changed {
		t.Fatalf("expected broken provider link to be repaired")
	}
	if got := link.Action; got != oauthProviderActionRelinked {
		t.Fatalf("action = %q, want relinked", got)
	}
	if got := link.Provider.Name; got != "codex-sean-example-com" {
		t.Fatalf("provider name = %q, want codex-sean-example-com", got)
	}
	if got := cc.Providers[0].NormalizedOAuthRef(); got != "codex-sean-example-com" {
		t.Fatalf("oauth_ref = %q, want codex-sean-example-com", got)
	}
}

func TestEnsureOAuthProviderLinked_DoesNotRelinkNamedProviderWithoutIdentityWhenCredentialFileIsMissing(t *testing.T) {
	cc := &config.ClientConfig{
		Providers: []config.Provider{
			{
				Name:          "codex-sean-example-com",
				AuthType:      config.ProviderAuthTypeOAuth,
				OAuthProvider: config.OAuthProviderCodex,
				OAuthRef:      "codex-stale-ref",
				Priority:      1,
				Enabled:       ptr(true),
			},
		},
	}
	cred := &oauthpkg.Credential{
		Ref:       "codex-sean-example-com",
		Provider:  config.OAuthProviderCodex,
		Email:     "sean@example.com",
		AccountID: "acct_123",
	}

	link := ensureOAuthProviderLinked(cc, cred, func(config.OAuthProvider, string) (*oauthpkg.Credential, error) {
		return nil, os.ErrNotExist
	})
	if got := link.Action; got != oauthProviderActionCreated {
		t.Fatalf("action = %q, want created", got)
	}
	if len(cc.Providers) != 2 {
		t.Fatalf("providers len = %d, want 2", len(cc.Providers))
	}
}

func TestEnsureOAuthProviderLinked_RelinksCustomNamedProviderWhenCandidateIsUnambiguous(t *testing.T) {
	cc := &config.ClientConfig{
		Providers: []config.Provider{
			{
				Name:          "primary",
				AuthType:      config.ProviderAuthTypeOAuth,
				OAuthProvider: config.OAuthProviderCodex,
				OAuthRef:      "codex-stale-ref",
				OAuthIdentity: "acct:acct_123",
				Priority:      1,
				Enabled:       ptr(true),
			},
		},
	}
	cred := &oauthpkg.Credential{
		Ref:       "codex-sean-example-com",
		Provider:  config.OAuthProviderCodex,
		Email:     "sean@example.com",
		AccountID: "acct_123",
	}

	link := ensureOAuthProviderLinked(cc, cred, func(config.OAuthProvider, string) (*oauthpkg.Credential, error) {
		return nil, os.ErrNotExist
	})
	if !link.Changed {
		t.Fatalf("expected custom-named provider to be relinked")
	}
	if got := link.Action; got != oauthProviderActionRelinked {
		t.Fatalf("action = %q, want relinked", got)
	}
	if got := link.Provider.Name; got != "primary" {
		t.Fatalf("provider name = %q, want primary", got)
	}
	if got := cc.Providers[0].NormalizedOAuthRef(); got != "codex-sean-example-com" {
		t.Fatalf("oauth_ref = %q, want codex-sean-example-com", got)
	}
}

func TestEnsureOAuthProviderLinked_DoesNotRelinkCustomNamedProviderAcrossAccounts(t *testing.T) {
	cc := &config.ClientConfig{
		Providers: []config.Provider{
			{
				Name:          "primary",
				AuthType:      config.ProviderAuthTypeOAuth,
				OAuthProvider: config.OAuthProviderCodex,
				OAuthRef:      "codex-stale-ref",
				OAuthIdentity: "acct:acct_123",
				Priority:      1,
				Enabled:       ptr(true),
			},
		},
	}
	cred := &oauthpkg.Credential{
		Ref:       "codex-other-example-com",
		Provider:  config.OAuthProviderCodex,
		Email:     "other@example.com",
		AccountID: "acct_456",
	}

	link := ensureOAuthProviderLinked(cc, cred, func(config.OAuthProvider, string) (*oauthpkg.Credential, error) {
		return nil, os.ErrNotExist
	})
	if link.Changed != true {
		t.Fatalf("expected a new provider to be created")
	}
	if got := link.Action; got != oauthProviderActionCreated {
		t.Fatalf("action = %q, want created", got)
	}
	if got := cc.Providers[0].NormalizedOAuthRef(); got != "codex-stale-ref" {
		t.Fatalf("original oauth_ref = %q, want codex-stale-ref", got)
	}
	if len(cc.Providers) != 2 {
		t.Fatalf("providers len = %d, want 2", len(cc.Providers))
	}
}

func TestEnsureOAuthProviderLinked_BackfillsIdentityForReusedProvider(t *testing.T) {
	cc := &config.ClientConfig{
		Providers: []config.Provider{
			{
				Name:          "codex-sean-example-com",
				AuthType:      config.ProviderAuthTypeOAuth,
				OAuthProvider: config.OAuthProviderCodex,
				OAuthRef:      "codex-sean-example-com",
				Priority:      1,
				Enabled:       ptr(true),
			},
		},
	}
	cred := &oauthpkg.Credential{
		Ref:       "codex-sean-example-com",
		Provider:  config.OAuthProviderCodex,
		Email:     "sean@example.com",
		AccountID: "acct_123",
	}

	link := ensureOAuthProviderLinked(cc, cred, nil)
	if got := link.Action; got != oauthProviderActionReused {
		t.Fatalf("action = %q, want reused", got)
	}
	if !link.Changed {
		t.Fatalf("expected identity backfill change")
	}
	if got := cc.Providers[0].NormalizedOAuthIdentity(); got != "acct:acct_123" {
		t.Fatalf("oauth_identity = %q, want acct:acct_123", got)
	}
}

func TestEnsureOAuthProviderLinked_PreservesProxySettingsForReusedProvider(t *testing.T) {
	cc := &config.ClientConfig{
		Providers: []config.Provider{
			{
				Name:          "codex-sean-example-com",
				AuthType:      config.ProviderAuthTypeOAuth,
				OAuthProvider: config.OAuthProviderCodex,
				OAuthRef:      "codex-sean-example-com",
				ProxyMode:     config.ProviderProxyModeDirect,
				Priority:      1,
				Enabled:       ptr(true),
			},
		},
	}
	cred := &oauthpkg.Credential{
		Ref:      "codex-sean-example-com",
		Provider: config.OAuthProviderCodex,
		Email:    "sean@example.com",
	}

	link := ensureOAuthProviderLinked(cc, cred, nil, oauthProviderProxySettings{
		Configured: true,
		Mode:       config.ProviderProxyModeCustom,
		URL:        "http://127.0.0.1:7890",
	})
	if got := link.Action; got != oauthProviderActionReused {
		t.Fatalf("action = %q, want reused", got)
	}
	if got := cc.Providers[0].NormalizedProxyMode(); got != config.ProviderProxyModeDirect {
		t.Fatalf("proxy_mode = %q, want direct", got)
	}
	if got := cc.Providers[0].NormalizedProxyURL(); got != "" {
		t.Fatalf("proxy_url = %q, want empty", got)
	}
}

func TestHandleGetOAuthSession_ReusesExistingProviderForDuplicateAuthorization(t *testing.T) {
	now := time.Date(2026, 4, 18, 21, 30, 0, 0, time.UTC)
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(`{"access_token":"access-1","refresh_token":"refresh-1","id_token":"%s","expires_in":3600}`, testOAuthJWT("sean@example.com", "acct_123")))
	}))
	defer tokenServer.Close()

	api := newTestOAuthAPI(t, oauthpkg.WithCodexClient(&oauthpkg.CodexClient{
		AuthURL:      "https://auth.openai.com/oauth/authorize",
		TokenURL:     tokenServer.URL,
		ClientID:     "test-client",
		CallbackHost: "127.0.0.1",
		CallbackPort: 0,
		CallbackPath: "/auth/callback",
		HTTPClient:   tokenServer.Client(),
		Now:          func() time.Time { return now },
	}))

	first := startOAuthSessionFor(t, api, "openai", "codex")
	completeOAuthCallback(t, first)
	req := httptest.NewRequest(http.MethodGet, "/api/oauth/sessions/"+first.SessionID, nil)
	w := httptest.NewRecorder()
	api.HandleGetOAuthSession(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("first status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}
	linkOAuthSessionFor(t, api, first.SessionID, "openai")

	second := startOAuthSessionFor(t, api, "openai", "codex")
	completeOAuthCallback(t, second)
	req = httptest.NewRequest(http.MethodGet, "/api/oauth/sessions/"+second.SessionID, nil)
	w = httptest.NewRecorder()
	api.HandleGetOAuthSession(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("second status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}
	got := testutil.DecodeJSONMap(t, w.Body.Bytes())
	if got["provider_name"] != "codex-sean-example-com" {
		t.Fatalf("provider_name = %v, want codex-sean-example-com", got["provider_name"])
	}
	if got["provider_action"] != "reused" {
		t.Fatalf("provider_action = %v, want reused", got["provider_action"])
	}

	cfg, err := config.Load(api.configDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if len(cfg.OpenAI.Providers) != 1 {
		t.Fatalf("providers len = %d, want 1", len(cfg.OpenAI.Providers))
	}
}

func TestHandleGetOAuthSession_ReusesExistingProviderWhenSameAccountHasLegacyRef(t *testing.T) {
	now := time.Date(2026, 4, 18, 21, 45, 0, 0, time.UTC)
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(`{"access_token":"access-new","refresh_token":"refresh-new","id_token":"%s","expires_in":3600}`, testOAuthJWT("sean@example.com", "acct_123")))
	}))
	defer tokenServer.Close()

	api := newTestOAuthAPI(t, oauthpkg.WithCodexClient(&oauthpkg.CodexClient{
		AuthURL:      "https://auth.openai.com/oauth/authorize",
		TokenURL:     tokenServer.URL,
		ClientID:     "test-client",
		CallbackHost: "127.0.0.1",
		CallbackPort: 0,
		CallbackPath: "/auth/callback",
		HTTPClient:   tokenServer.Client(),
		Now:          func() time.Time { return now },
	}))

	if err := api.oauth.Store().Save(&oauthpkg.Credential{
		Ref:          "codex-legacy-ref",
		Provider:     config.OAuthProviderCodex,
		Email:        "sean@example.com",
		AccountID:    "acct_123",
		AccessToken:  "access-old",
		RefreshToken: "refresh-old",
		ExpiresAt:    now.Add(-time.Hour),
		LastRefresh:  now.Add(-2 * time.Hour),
		Metadata: map[string]string{
			"id_token": testOAuthJWT("sean@example.com", "acct_123"),
		},
	}); err != nil {
		t.Fatalf("Save legacy credential: %v", err)
	}
	if err := os.WriteFile(filepath.Join(api.configDir, "openai.yaml"), []byte(`
providers:
  - name: codex-sean-example-com
    auth_type: oauth
    oauth_provider: codex
    oauth_ref: codex-legacy-ref
    priority: 1
`), 0o600); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}

	start := startOAuthSessionFor(t, api, "openai", "codex")
	completeOAuthCallback(t, start)

	req := httptest.NewRequest(http.MethodGet, "/api/oauth/sessions/"+start.SessionID, nil)
	w := httptest.NewRecorder()
	api.HandleGetOAuthSession(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	got := testutil.DecodeJSONMap(t, w.Body.Bytes())
	if got["provider_name"] != "codex-sean-example-com" {
		t.Fatalf("provider_name = %v, want codex-sean-example-com", got["provider_name"])
	}
	if got["provider_action"] != "reused" {
		t.Fatalf("provider_action = %v, want reused", got["provider_action"])
	}

	cfg, err := config.Load(api.configDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if len(cfg.OpenAI.Providers) != 1 {
		t.Fatalf("providers len = %d, want 1", len(cfg.OpenAI.Providers))
	}
	if got := cfg.OpenAI.Providers[0].NormalizedOAuthRef(); got != "codex-legacy-ref" {
		t.Fatalf("oauth_ref = %q, want codex-legacy-ref", got)
	}

	cred, err := api.oauth.Load(config.OAuthProviderCodex, "codex-legacy-ref")
	if err != nil {
		t.Fatalf("Load legacy ref: %v", err)
	}
	if got := cred.AccessToken; got != "access-new" {
		t.Fatalf("access_token = %q, want access-new", got)
	}
	if _, err := api.oauth.Load(config.OAuthProviderCodex, "codex-sean-example-com"); !os.IsNotExist(err) {
		t.Fatalf("Load new ref err = %v, want not-exist", err)
	}
}

func TestHandleGetOAuthSession_AppendsNumericSuffixWhenProviderNameAlreadyExists(t *testing.T) {
	now := time.Date(2026, 4, 18, 22, 0, 0, 0, time.UTC)
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(`{"access_token":"access-1","refresh_token":"refresh-1","id_token":"%s","expires_in":3600}`, testOAuthJWT("sean@example.com", "acct_123")))
	}))
	defer tokenServer.Close()

	api := newTestOAuthAPI(t, oauthpkg.WithCodexClient(&oauthpkg.CodexClient{
		AuthURL:      "https://auth.openai.com/oauth/authorize",
		TokenURL:     tokenServer.URL,
		ClientID:     "test-client",
		CallbackHost: "127.0.0.1",
		CallbackPort: 0,
		CallbackPath: "/auth/callback",
		HTTPClient:   tokenServer.Client(),
		Now:          func() time.Time { return now },
	}))

	if err := os.WriteFile(filepath.Join(api.configDir, "openai.yaml"), []byte(`
providers:
  - name: codex-sean-example-com
    base_url: https://api.example.com/v1
    api_key: provider-key
    priority: 1
`), 0o600); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}

	start := startOAuthSessionFor(t, api, "openai", "codex")
	completeOAuthCallback(t, start)

	req := httptest.NewRequest(http.MethodGet, "/api/oauth/sessions/"+start.SessionID, nil)
	w := httptest.NewRecorder()
	api.HandleGetOAuthSession(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	got := testutil.DecodeJSONMap(t, w.Body.Bytes())
	if got["provider_name"] != nil {
		t.Fatalf("provider_name = %v, want empty before explicit link", got["provider_name"])
	}

	cfg, err := config.Load(api.configDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if len(cfg.OpenAI.Providers) != 1 {
		t.Fatalf("providers len = %d, want 1 before link", len(cfg.OpenAI.Providers))
	}

	got = linkOAuthSessionFor(t, api, start.SessionID, "openai")
	if got["provider_name"] != "codex-sean-example-com-2" {
		t.Fatalf("provider_name = %v, want codex-sean-example-com-2", got["provider_name"])
	}
	if got["provider_action"] != "created" {
		t.Fatalf("provider_action = %v, want created", got["provider_action"])
	}

	cfg, err = config.Load(api.configDir)
	if err != nil {
		t.Fatalf("config.Load after link: %v", err)
	}
	if len(cfg.OpenAI.Providers) != 2 {
		t.Fatalf("providers len = %d, want 2 after link", len(cfg.OpenAI.Providers))
	}
	if cfg.OpenAI.Providers[1].Name != "codex-sean-example-com-2" {
		t.Fatalf("oauth provider name = %q, want codex-sean-example-com-2", cfg.OpenAI.Providers[1].Name)
	}
}

func TestHandleListOAuthAccounts_AndDeleteRemovesLinkedProviders(t *testing.T) {
	dir := t.TempDir()
	api := NewAPI(dir, "test", nil)
	cred := &oauthpkg.Credential{
		Ref:          "codex-sean-example-com",
		Provider:     config.OAuthProviderCodex,
		Email:        "sean@example.com",
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		ExpiresAt:    time.Date(2026, 4, 18, 23, 0, 0, 0, time.UTC),
		LastRefresh:  time.Date(2026, 4, 18, 22, 0, 0, 0, time.UTC),
	}
	if err := api.oauth.Store().Save(cred); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "openai.yaml"), []byte(`
providers:
  - name: codex-sean-example-com
    auth_type: oauth
    oauth_provider: codex
    oauth_ref: codex-sean-example-com
    priority: 1
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := api.telemetry.RecordUsage("openai", "codex-sean-example-com", telemetry.UsageSnapshot{
		UsageDelta: telemetry.UsageDelta{InputTokens: 1, OutputTokens: 2},
	}, time.Now()); err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/oauth/accounts/codex", nil)
	w := httptest.NewRecorder()
	api.HandleListOAuthAccounts(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	var accounts []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &accounts); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("accounts len = %d, want 1", len(accounts))
	}
	linked, ok := accounts[0]["linked_providers"].([]any)
	if !ok || len(linked) != 1 || linked[0] != "openai/codex-sean-example-com" {
		t.Fatalf("linked_providers = %#v", accounts[0]["linked_providers"])
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/oauth/accounts/codex/codex-sean-example-com", nil)
	w = httptest.NewRecorder()
	api.HandleDeleteOAuthAccount(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if len(cfg.OpenAI.Providers) != 0 {
		t.Fatalf("providers=%#v", cfg.OpenAI.Providers)
	}
	if _, err := api.oauth.Load(config.OAuthProviderCodex, "codex-sean-example-com"); !os.IsNotExist(err) {
		t.Fatalf("Load err = %v, want not-exist", err)
	}
	if _, ok := api.telemetry.ProviderSnapshot("openai", "codex-sean-example-com"); ok {
		t.Fatalf("expected usage snapshot for deleted oauth provider to be removed")
	}
}

func TestHandleGetOAuthSession_CompletedGetDoesNotRequireStoredCredential(t *testing.T) {
	now := time.Date(2026, 4, 18, 23, 15, 0, 0, time.UTC)
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(`{"access_token":"access-1","refresh_token":"refresh-1","id_token":"%s","expires_in":3600}`, testOAuthJWT("sean@example.com", "acct_123")))
	}))
	defer tokenServer.Close()

	api := newTestOAuthAPI(t, oauthpkg.WithCodexClient(&oauthpkg.CodexClient{
		AuthURL:      "https://auth.openai.com/oauth/authorize",
		TokenURL:     tokenServer.URL,
		ClientID:     "test-client",
		CallbackHost: "127.0.0.1",
		CallbackPort: 0,
		CallbackPath: "/auth/callback",
		HTTPClient:   tokenServer.Client(),
		Now:          func() time.Time { return now },
	}))

	start := startOAuthSessionFor(t, api, "openai", "codex")
	completeOAuthCallback(t, start)
	if err := api.oauth.Store().Delete(config.OAuthProviderCodex, "codex-sean-example-com"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/oauth/sessions/"+start.SessionID, nil)
	w := httptest.NewRecorder()
	api.HandleGetOAuthSession(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	got := testutil.DecodeJSONMap(t, w.Body.Bytes())
	if got["status"] != "completed" {
		t.Fatalf("status = %v, want completed", got["status"])
	}
	if got["display_name"] != "sean@example.com" {
		t.Fatalf("display_name = %v, want sean@example.com", got["display_name"])
	}
	if got["provider_name"] != nil {
		t.Fatalf("provider_name = %v, want empty after credential deletion", got["provider_name"])
	}
}

func TestHandleImportCLIProxyAPICredentials_ImportsAndLinksProvider(t *testing.T) {
	api := NewAPI(t.TempDir(), "test", nil)

	req := newOAuthImportRequest(t,
		"openai",
		"codex",
		importedOAuthFile{
			Name: "codex-sean.json",
			Body: `{
  "type": "codex",
  "email": "sean@example.com",
  "account_id": "acct_123",
  "access_token": "access-1",
  "refresh_token": "refresh-1",
  "id_token": "` + testOAuthJWT("sean@example.com", "acct_123") + `",
  "expired": "2026-04-29T11:54:11+08:00",
  "last_refresh": "2026-04-21T11:54:11+08:00"
}`,
		},
	)
	w := httptest.NewRecorder()
	api.HandleImportCLIProxyAPICredentials(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	var resp OAuthImportResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if resp.ImportedCount != 1 || resp.LinkedCount != 1 || resp.SkippedCount != 0 || resp.FailedCount != 0 {
		t.Fatalf("unexpected import counts: %#v", resp)
	}
	if len(resp.Results) != 1 || resp.Results[0].Status != "imported" {
		t.Fatalf("results = %#v", resp.Results)
	}
	if got := resp.Results[0].ProviderName; got != "codex-sean-example-com" {
		t.Fatalf("provider_name = %q, want codex-sean-example-com", got)
	}
	if got := resp.Results[0].ProviderAction; got != "created" {
		t.Fatalf("provider_action = %q, want created", got)
	}

	cfg, err := config.Load(api.configDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if len(cfg.OpenAI.Providers) != 1 {
		t.Fatalf("providers len = %d, want 1", len(cfg.OpenAI.Providers))
	}
	provider := cfg.OpenAI.Providers[0]
	if provider.Name != "codex-sean-example-com" {
		t.Fatalf("provider name = %q", provider.Name)
	}
	cred, err := api.oauth.Load(config.OAuthProviderCodex, "codex-sean-example-com")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cred.Email; got != "sean@example.com" {
		t.Fatalf("email = %q", got)
	}
}

func TestHandleImportCLIProxyAPICredentials_ImportsCodexNativeAuthJSON(t *testing.T) {
	api := NewAPI(t.TempDir(), "test", nil)

	req := newOAuthImportRequest(t,
		"openai",
		"codex",
		importedOAuthFile{
			Name: "auth.json",
			Body: `{
  "auth_mode": "chatgpt",
  "OPENAI_API_KEY": null,
  "tokens": {
    "id_token": "` + testOAuthJWTWithPlan("sean@example.com", "acct_123", "plus") + `",
    "access_token": "` + testOAuthJWT("sean@example.com", "acct_123") + `",
    "refresh_token": "refresh-1",
    "account_id": "acct_123"
  },
  "last_refresh": "2026-04-29T14:12:58.368988800Z"
}`,
		},
	)
	w := httptest.NewRecorder()
	api.HandleImportCLIProxyAPICredentials(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	var resp OAuthImportResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if resp.ImportedCount != 1 || resp.LinkedCount != 1 || resp.SkippedCount != 0 || resp.FailedCount != 0 {
		t.Fatalf("unexpected import counts: %#v", resp)
	}
	if got := findOAuthImportResult(resp.Results, "auth.json"); got.Status != "imported" || got.ProviderName != "codex-sean-example-com" {
		t.Fatalf("auth.json result = %#v", got)
	}
	cred, err := api.oauth.Load(config.OAuthProviderCodex, "codex-sean-example-com")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cred.Email; got != "sean@example.com" {
		t.Fatalf("email = %q", got)
	}
	if got := cred.Metadata["auth_mode"]; got != "chatgpt" {
		t.Fatalf("auth_mode = %q, want chatgpt", got)
	}
	if got := cred.Metadata["plan_type"]; got != "plus" {
		t.Fatalf("plan_type = %q, want plus", got)
	}
}

func TestHandleImportCLIProxyAPICredentials_ReportsSkippedAndFailedFiles(t *testing.T) {
	api := NewAPI(t.TempDir(), "test", nil)

	req := newOAuthImportRequest(t,
		"openai",
		"codex",
		importedOAuthFile{
			Name: "good.json",
			Body: `{
  "type": "codex",
  "email": "sean@example.com",
  "account_id": "acct_123",
  "access_token": "access-1",
  "refresh_token": "refresh-1"
}`,
		},
		importedOAuthFile{
			Name: "duplicate.json",
			Body: `{
  "type": "codex",
  "email": "sean@example.com",
  "account_id": "acct_123",
  "access_token": "access-2",
  "refresh_token": "refresh-2"
}`,
		},
		importedOAuthFile{
			Name: "vertex.json",
			Body: `{
  "type": "vertex",
  "access_token": "access-1"
}`,
		},
		importedOAuthFile{
			Name: "notes.txt",
			Body: `not-json`,
		},
		importedOAuthFile{
			Name: "broken.json",
			Body: `{`,
		},
	)
	w := httptest.NewRecorder()
	api.HandleImportCLIProxyAPICredentials(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	var resp OAuthImportResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if resp.ImportedCount != 1 || resp.LinkedCount != 1 || resp.SkippedCount != 3 || resp.FailedCount != 1 {
		t.Fatalf("unexpected import counts: %#v", resp)
	}
	if got := findOAuthImportResult(resp.Results, "duplicate.json"); got.Status != "skipped" {
		t.Fatalf("duplicate result = %#v", got)
	}
	if got := findOAuthImportResult(resp.Results, "vertex.json"); got.Status != "skipped" {
		t.Fatalf("vertex result = %#v", got)
	}
	if got := findOAuthImportResult(resp.Results, "notes.txt"); got.Status != "skipped" {
		t.Fatalf("notes result = %#v", got)
	}
	if got := findOAuthImportResult(resp.Results, "broken.json"); got.Status != "failed" {
		t.Fatalf("broken result = %#v", got)
	}

	cfg, err := config.Load(api.configDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if len(cfg.OpenAI.Providers) != 1 {
		t.Fatalf("providers len = %d, want 1", len(cfg.OpenAI.Providers))
	}
}

func TestHandleImportCLIProxyAPICredentials_DedupesSameAccountAcrossDifferentRefs(t *testing.T) {
	api := NewAPI(t.TempDir(), "test", nil)

	req := newOAuthImportRequest(t,
		"openai",
		"codex",
		importedOAuthFile{
			Name: "full.json",
			Body: `{
  "type": "codex",
  "email": "sean@example.com",
  "account_id": "acct_123",
  "access_token": "access-1",
  "refresh_token": "refresh-1"
}`,
		},
		importedOAuthFile{
			Name: "legacy.json",
			Body: `{
  "type": "codex",
  "account_id": "acct_123",
  "access_token": "access-2",
  "refresh_token": "refresh-2"
}`,
		},
	)
	w := httptest.NewRecorder()
	api.HandleImportCLIProxyAPICredentials(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	var resp OAuthImportResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if resp.ImportedCount != 1 || resp.LinkedCount != 1 || resp.SkippedCount != 1 || resp.FailedCount != 0 {
		t.Fatalf("unexpected import counts: %#v", resp)
	}
	if got := findOAuthImportResult(resp.Results, "legacy.json"); got.Status != "skipped" {
		t.Fatalf("legacy result = %#v, want skipped duplicate", got)
	}
}

func TestHandleImportCLIProxyAPICredentials_ImportsSub2APIBundle(t *testing.T) {
	api := NewAPI(t.TempDir(), "test", nil)

	req := newOAuthImportRequest(t,
		"openai",
		"codex",
		importedOAuthFile{
			Name: "sub2api-export.json",
			Body: `{
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
        "chatgpt_user_id": "user_123",
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
}`,
		},
	)
	w := httptest.NewRecorder()
	api.HandleImportCLIProxyAPICredentials(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	var resp OAuthImportResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if resp.ImportedCount != 1 || resp.LinkedCount != 1 || resp.SkippedCount != 1 || resp.FailedCount != 0 {
		t.Fatalf("unexpected import counts: %#v", resp)
	}
	if got := findOAuthImportResult(resp.Results, "sub2api-export.json#accounts[0]"); got.Status != "imported" {
		t.Fatalf("accounts[0] result = %#v, want imported", got)
	}
	if got := findOAuthImportResult(resp.Results, "sub2api-export.json#accounts[1]"); got.Status != "skipped" {
		t.Fatalf("accounts[1] result = %#v, want skipped", got)
	}

	cred, err := api.oauth.Load(config.OAuthProviderCodex, "codex-acct-123")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cred.AccountID; got != "acct_123" {
		t.Fatalf("account_id = %q, want acct_123", got)
	}
	if got := cred.Metadata["plan_type"]; got != "plus" {
		t.Fatalf("plan_type = %q, want plus", got)
	}
}

func TestHandleImportCLIProxyAPICredentials_ImportsSub2APIBundleGeminiOnlyForGeminiAdd(t *testing.T) {
	api := NewAPI(t.TempDir(), "test", nil)

	req := newOAuthImportRequest(t,
		"gemini",
		"gemini",
		importedOAuthFile{
			Name: "sub2api-gemini.json",
			Body: `{
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
    },
    {
      "name": "OpenAI OAuth",
      "platform": "openai",
      "type": "oauth",
      "credentials": {
        "access_token": "access-2",
        "chatgpt_account_id": "acct_123",
        "expires_at": 1777776981
      }
    }
  ]
}`,
		},
	)
	w := httptest.NewRecorder()
	api.HandleImportCLIProxyAPICredentials(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	var resp OAuthImportResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if resp.ImportedCount != 1 || resp.LinkedCount != 1 || resp.SkippedCount != 1 || resp.FailedCount != 0 {
		t.Fatalf("unexpected import counts: %#v", resp)
	}
	if got := findOAuthImportResult(resp.Results, "sub2api-gemini.json#accounts[0]"); got.Status != "imported" {
		t.Fatalf("accounts[0] result = %#v, want imported", got)
	}
	if got := findOAuthImportResult(resp.Results, "sub2api-gemini.json#accounts[1]"); got.Status != "skipped" {
		t.Fatalf("accounts[1] result = %#v, want skipped", got)
	}
	if _, err := api.oauth.Load(config.OAuthProviderCodex, "codex-acct-123"); !os.IsNotExist(err) {
		t.Fatalf("unexpected codex credential import err=%v", err)
	}
}

func TestHandleImportCLIProxyAPICredentials_FailsSub2APIClaudeWithoutStableIdentity(t *testing.T) {
	api := NewAPI(t.TempDir(), "test", nil)

	req := newOAuthImportRequest(t,
		"claude",
		"claude",
		importedOAuthFile{
			Name: "sub2api-claude.json",
			Body: `{
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
}`,
		},
	)
	w := httptest.NewRecorder()
	api.HandleImportCLIProxyAPICredentials(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	var resp OAuthImportResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if resp.ImportedCount != 0 || resp.LinkedCount != 0 || resp.SkippedCount != 0 || resp.FailedCount != 1 {
		t.Fatalf("unexpected import counts: %#v", resp)
	}
	if got := resp.Message; got != "imported 0 account(s), linked 0 provider(s), failed 1 entry(s)" {
		t.Fatalf("message = %q, want failed entry summary", got)
	}
	if got := findOAuthImportResult(resp.Results, "sub2api-claude.json#accounts[0]"); got.Status != "failed" || got.Message != "claude credential missing email/account_id/organization_id" {
		t.Fatalf("accounts[0] result = %#v, want failed missing identity", got)
	}
	if _, err := api.oauth.Load(config.OAuthProviderClaude, "claude-claude-team-a"); !os.IsNotExist(err) {
		t.Fatalf("unexpected claude credential import err=%v", err)
	}
}

func TestHandleImportCLIProxyAPICredentials_ReportsCanonicalRefAfterStoreMerge(t *testing.T) {
	api := NewAPI(t.TempDir(), "test", nil)
	if err := api.oauth.Store().Save(&oauthpkg.Credential{
		Ref:          "codex-acct-123",
		Provider:     config.OAuthProviderCodex,
		AccountID:    "acct_123",
		AccessToken:  "access-old",
		RefreshToken: "refresh-old",
	}); err != nil {
		t.Fatalf("Save existing credential: %v", err)
	}

	req := newOAuthImportRequest(t,
		"openai",
		"codex",
		importedOAuthFile{
			Name: "full.json",
			Body: `{
  "type": "codex",
  "email": "sean@example.com",
  "account_id": "acct_123",
  "access_token": "access-1",
  "refresh_token": "refresh-1"
}`,
		},
	)
	w := httptest.NewRecorder()
	api.HandleImportCLIProxyAPICredentials(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	var resp OAuthImportResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("results len = %d, want 1", len(resp.Results))
	}
	if got := resp.Results[0].Ref; got != "codex-acct-123" {
		t.Fatalf("ref = %q, want canonical merged ref codex-acct-123", got)
	}
}

func TestHandleImportCLIProxyAPICredentials_PersistsIdentityBackfillForReusedProvider(t *testing.T) {
	api := NewAPI(t.TempDir(), "test", nil)
	if err := api.oauth.Store().Save(&oauthpkg.Credential{
		Ref:          "codex-acct-123",
		Provider:     config.OAuthProviderCodex,
		Email:        "sean@example.com",
		AccountID:    "acct_123",
		AccessToken:  "access-old",
		RefreshToken: "refresh-old",
	}); err != nil {
		t.Fatalf("Save existing credential: %v", err)
	}
	if err := os.WriteFile(filepath.Join(api.configDir, "openai.yaml"), []byte(`
mode: auto
providers:
  - name: codex-sean-example-com
    auth_type: oauth
    oauth_provider: codex
    oauth_ref: codex-acct-123
    priority: 1
`), 0o600); err != nil {
		t.Fatalf("WriteFile openai.yaml: %v", err)
	}

	req := newOAuthImportRequest(t,
		"openai",
		"codex",
		importedOAuthFile{
			Name: "full.json",
			Body: `{
  "type": "codex",
  "email": "sean@example.com",
  "account_id": "acct_123",
  "access_token": "access-1",
  "refresh_token": "refresh-1"
}`,
		},
	)
	w := httptest.NewRecorder()
	api.HandleImportCLIProxyAPICredentials(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	cfg, err := config.Load(api.configDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if len(cfg.OpenAI.Providers) != 1 {
		t.Fatalf("providers len = %d, want 1", len(cfg.OpenAI.Providers))
	}
	if got := cfg.OpenAI.Providers[0].NormalizedOAuthIdentity(); got != "acct:acct_123" {
		t.Fatalf("oauth_identity = %q, want acct:acct_123", got)
	}
}

func TestHandleImportCLIProxyAPICredentials_RollsBackSavedCredentialWhenConfigSaveFails(t *testing.T) {
	api := NewAPI(t.TempDir(), "test", nil)
	req := newOAuthImportRequest(t,
		"openai",
		"codex",
		importedOAuthFile{
			Name: "codex-sean.json",
			Body: `{
  "type": "codex",
  "email": "sean@example.com",
  "account_id": "acct_123",
  "access_token": "access-1",
  "refresh_token": "refresh-1"
}`,
		},
	)
	if err := os.MkdirAll(filepath.Join(api.configDir, "oauth", "codex"), 0o700); err != nil {
		t.Fatalf("MkdirAll oauth dir: %v", err)
	}

	if err := os.Chmod(api.configDir, 0o500); err != nil {
		t.Fatalf("Chmod readonly configDir: %v", err)
	}
	defer func() {
		_ = os.Chmod(api.configDir, 0o700)
	}()

	w := httptest.NewRecorder()
	api.HandleImportCLIProxyAPICredentials(w, req)
	if w.Result().StatusCode != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	if _, err := api.oauth.Load(config.OAuthProviderCodex, "codex-sean-example-com"); !os.IsNotExist(err) {
		t.Fatalf("Load err = %v, want not-exist after rollback", err)
	}
	cfg, err := config.Load(api.configDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if len(cfg.OpenAI.Providers) != 0 {
		t.Fatalf("providers=%#v, want no persisted providers after rollback", cfg.OpenAI.Providers)
	}
}

func TestHandleImportCLIProxyAPICredentials_RollbackPreservesSkippedAbnormalEntries(t *testing.T) {
	api := NewAPI(t.TempDir(), "test", nil)
	req := newOAuthImportRequest(t,
		"openai",
		"codex",
		importedOAuthFile{
			Name: "codex-sean.json",
			Body: `{
  "type": "codex",
  "email": "sean@example.com",
  "account_id": "acct_123",
  "access_token": "access-1",
  "refresh_token": "refresh-1"
}`,
		},
	)
	providerDir := filepath.Join(api.configDir, "oauth", "codex")
	if err := os.MkdirAll(providerDir, 0o700); err != nil {
		t.Fatalf("MkdirAll oauth dir: %v", err)
	}
	linkPath := filepath.Join(providerDir, "dangling.json")
	if err := os.Symlink(filepath.Join(providerDir, "missing-target.json"), linkPath); err != nil {
		t.Skipf("Symlink: %v", err)
	}

	if err := os.Chmod(api.configDir, 0o500); err != nil {
		t.Fatalf("Chmod readonly configDir: %v", err)
	}
	defer func() {
		_ = os.Chmod(api.configDir, 0o700)
	}()

	w := httptest.NewRecorder()
	api.HandleImportCLIProxyAPICredentials(w, req)
	if w.Result().StatusCode != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	info, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatalf("Lstat abnormal entry after rollback: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("mode = %v, want symlink", info.Mode())
	}
	if _, err := api.oauth.Load(config.OAuthProviderCodex, "codex-sean-example-com"); !os.IsNotExist(err) {
		t.Fatalf("Load err = %v, want rolled back credential removal", err)
	}
}

func TestSnapshotOAuthImportProviderDir_RestoresOverwrittenSymlink(t *testing.T) {
	providerDir := filepath.Join(t.TempDir(), "oauth", "codex")
	if err := os.MkdirAll(providerDir, 0o700); err != nil {
		t.Fatalf("MkdirAll oauth dir: %v", err)
	}
	linkPath := filepath.Join(providerDir, "credential.json")
	if err := os.Symlink("missing-target.json", linkPath); err != nil {
		t.Skipf("Symlink: %v", err)
	}

	restore, finalize, err := snapshotOAuthImportProviderDir(providerDir)
	if err != nil {
		t.Fatalf("snapshotOAuthImportProviderDir: %v", err)
	}
	defer func() {
		if finalize != nil {
			_ = finalize()
		}
	}()

	if err := atomicWriteFile(linkPath, []byte(`{"ref":"new"}`), 0o600); err != nil {
		t.Fatalf("atomicWriteFile overwrite: %v", err)
	}
	if err := restore(); err != nil {
		t.Fatalf("restore: %v", err)
	}

	info, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatalf("Lstat restored symlink: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("mode = %v, want symlink", info.Mode())
	}
}

func TestHandleImportCLIProxyAPICredentials_SkipsAbnormalSnapshotEntries(t *testing.T) {
	api := NewAPI(t.TempDir(), "test", nil)
	providerDir := filepath.Join(api.configDir, "oauth", "codex")
	if err := os.MkdirAll(providerDir, 0o700); err != nil {
		t.Fatalf("MkdirAll oauth dir: %v", err)
	}
	if err := os.Symlink(filepath.Join(providerDir, "missing-target.json"), filepath.Join(providerDir, "dangling.json")); err != nil {
		t.Skipf("Symlink: %v", err)
	}

	req := newOAuthImportRequest(t,
		"openai",
		"codex",
		importedOAuthFile{
			Name: "codex-sean.json",
			Body: `{
  "type": "codex",
  "email": "sean@example.com",
  "account_id": "acct_123",
  "access_token": "access-1",
  "refresh_token": "refresh-1"
}`,
		},
	)
	w := httptest.NewRecorder()
	api.HandleImportCLIProxyAPICredentials(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	var resp OAuthImportResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if resp.ImportedCount != 1 || resp.LinkedCount != 1 || resp.FailedCount != 0 {
		t.Fatalf("unexpected import counts: %#v", resp)
	}
}

func TestHandleGetOAuthSession_ClaudeFlowRequiresExplicitLink(t *testing.T) {
	now := time.Date(2026, 4, 22, 9, 0, 0, 0, time.UTC)
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"access-1","refresh_token":"refresh-1","token_type":"Bearer","expires_in":3600,"organization":{"uuid":"org_123","name":"Anthropic"},"account":{"uuid":"acct_123","email_address":"sean@example.com"}}`)
	}))
	defer tokenServer.Close()

	api := newTestOAuthAPI(t, oauthpkg.WithClaudeClient(&oauthpkg.ClaudeClient{
		AuthURL:      "https://platform.claude.com/oauth/authorize",
		TokenURL:     tokenServer.URL,
		ClientID:     "test-client",
		CallbackHost: "127.0.0.1",
		CallbackPort: 0,
		CallbackPath: "/callback",
		HTTPClient:   tokenServer.Client(),
		Now:          func() time.Time { return now },
	}))

	start := startOAuthSessionFor(t, api, "claude", "claude")
	completeOAuthCallback(t, start)

	req := httptest.NewRequest(http.MethodGet, "/api/oauth/sessions/"+start.SessionID, nil)
	w := httptest.NewRecorder()
	api.HandleGetOAuthSession(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	got := testutil.DecodeJSONMap(t, w.Body.Bytes())
	if got["status"] != "completed" {
		t.Fatalf("status = %v, want completed", got["status"])
	}
	if got["provider_name"] != nil {
		t.Fatalf("provider_name = %v, want empty on read-only GET", got["provider_name"])
	}
	if got["display_name"] != "sean@example.com" {
		t.Fatalf("display_name = %v, want sean@example.com", got["display_name"])
	}

	cfg, err := config.Load(api.configDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if len(cfg.Claude.Providers) != 0 {
		t.Fatalf("providers len = %d, want 0 before link", len(cfg.Claude.Providers))
	}

	got = linkOAuthSessionFor(t, api, start.SessionID, "claude")
	if got["provider_name"] != "claude-sean-example-com" {
		t.Fatalf("provider_name = %v, want claude-sean-example-com", got["provider_name"])
	}
	if got["provider_action"] != "created" {
		t.Fatalf("provider_action = %v, want created", got["provider_action"])
	}

	cfg, err = config.Load(api.configDir)
	if err != nil {
		t.Fatalf("config.Load after link: %v", err)
	}
	if len(cfg.Claude.Providers) != 1 {
		t.Fatalf("providers len = %d, want 1 after link", len(cfg.Claude.Providers))
	}
	provider := cfg.Claude.Providers[0]
	if provider.Name != "claude-sean-example-com" {
		t.Fatalf("provider name = %q", provider.Name)
	}
	if got := provider.NormalizedAuthType(); got != config.ProviderAuthTypeOAuth {
		t.Fatalf("auth_type = %q, want oauth", got)
	}
	if got := provider.NormalizedOAuthRef(); got != "claude-sean-example-com" {
		t.Fatalf("oauth_ref = %q", got)
	}
}

func TestHandleGetOAuthSession_GeminiFlowRequiresExplicitLink(t *testing.T) {
	now := time.Date(2026, 4, 22, 10, 0, 0, 0, time.UTC)
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"access-1","refresh_token":"refresh-1","expires_in":3600,"token_type":"Bearer"}`)
	}))
	defer tokenServer.Close()

	userInfoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"email":"sean@example.com"}`)
	}))
	defer userInfoServer.Close()

	cloudCodeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1internal:loadCodeAssist":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"allowedTiers":[{"id":"tier-default","isDefault":true}],"cloudaicompanionProject":{"id":"gen-lang-client-123"}}`)
		case "/v1internal:onboardUser":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"done":true,"response":{"cloudaicompanionProject":{"id":"gen-lang-client-123"}}}`)
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer cloudCodeServer.Close()

	api := newTestOAuthAPI(t, oauthpkg.WithGeminiClient(&oauthpkg.GeminiClient{
		AuthURL:      "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:     tokenServer.URL,
		UserInfoURL:  userInfoServer.URL,
		CloudCodeURL: cloudCodeServer.URL,
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		CallbackHost: "127.0.0.1",
		CallbackPort: 0,
		CallbackPath: "/oauth2callback",
		HTTPClient:   tokenServer.Client(),
		Now:          func() time.Time { return now },
		Sleep:        func(time.Duration) {},
	}))

	start := startOAuthSessionFor(t, api, "gemini", "gemini")
	completeOAuthCallback(t, start)

	req := httptest.NewRequest(http.MethodGet, "/api/oauth/sessions/"+start.SessionID, nil)
	w := httptest.NewRecorder()
	api.HandleGetOAuthSession(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	got := testutil.DecodeJSONMap(t, w.Body.Bytes())
	if got["status"] != "completed" {
		t.Fatalf("status = %v, want completed", got["status"])
	}
	if got["provider_name"] != nil {
		t.Fatalf("provider_name = %v, want empty on read-only GET", got["provider_name"])
	}
	if got["display_name"] != "sean@example.com" {
		t.Fatalf("display_name = %v, want sean@example.com", got["display_name"])
	}

	cfg, err := config.Load(api.configDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if len(cfg.Gemini.Providers) != 0 {
		t.Fatalf("providers len = %d, want 0 before link", len(cfg.Gemini.Providers))
	}

	got = linkOAuthSessionFor(t, api, start.SessionID, "gemini")
	if got["provider_name"] != "gemini-sean-example-com-gen-lang-client-123" {
		t.Fatalf("provider_name = %v, want gemini-sean-example-com-gen-lang-client-123", got["provider_name"])
	}
	if got["provider_action"] != "created" {
		t.Fatalf("provider_action = %v, want created", got["provider_action"])
	}

	cfg, err = config.Load(api.configDir)
	if err != nil {
		t.Fatalf("config.Load after link: %v", err)
	}
	if len(cfg.Gemini.Providers) != 1 {
		t.Fatalf("providers len = %d, want 1 after link", len(cfg.Gemini.Providers))
	}
	provider := cfg.Gemini.Providers[0]
	if provider.Name != "gemini-sean-example-com-gen-lang-client-123" {
		t.Fatalf("provider name = %q", provider.Name)
	}
	if got := provider.NormalizedAuthType(); got != config.ProviderAuthTypeOAuth {
		t.Fatalf("auth_type = %q, want oauth", got)
	}
	if got := provider.NormalizedOAuthRef(); got != "gemini-sean-example-com-gen-lang-client-123" {
		t.Fatalf("oauth_ref = %q", got)
	}
}

type startedOAuthSession struct {
	SessionID string
	AuthURL   string
}

type importedOAuthFile struct {
	Name string
	Body string
}

func newTestOAuthAPI(t *testing.T, opts ...oauthpkg.Option) *API {
	t.Helper()
	api := NewAPI(t.TempDir(), "test", nil)
	defaultCodexClient := oauthpkg.NewCodexClient()
	defaultCodexClient.CallbackHost = "127.0.0.1"
	defaultCodexClient.CallbackPort = 0
	serviceOpts := []oauthpkg.Option{oauthpkg.WithCodexClient(defaultCodexClient)}
	serviceOpts = append(serviceOpts, opts...)
	api.oauth = oauthpkg.NewService(api.configDir, serviceOpts...)
	return api
}

func startOAuthSessionFor(t *testing.T, api *API, clientType string, provider string) startedOAuthSession {
	t.Helper()
	body := []byte(fmt.Sprintf(`{"client_type":%q,"provider":%q}`, clientType, provider))
	req := httptest.NewRequest(http.MethodPost, "/api/oauth/providers/start", bytes.NewReader(body))
	w := httptest.NewRecorder()
	api.HandleStartOAuthProvider(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("start status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}
	got := testutil.DecodeJSONMap(t, w.Body.Bytes())
	sessionID, ok1 := got["session_id"].(string)
	authURL, ok2 := got["auth_url"].(string)
	if !ok1 || !ok2 {
		t.Fatalf("missing or invalid session_id/auth_url in response: %#v", got)
	}
	return startedOAuthSession{
		SessionID: sessionID,
		AuthURL:   authURL,
	}
}

func linkOAuthSessionFor(t *testing.T, api *API, sessionID string, clientType string) map[string]any {
	t.Helper()
	body := []byte(fmt.Sprintf(`{"client_type":%q}`, clientType))
	req := httptest.NewRequest(http.MethodPost, "/api/oauth/sessions/"+sessionID+"/link", bytes.NewReader(body))
	w := httptest.NewRecorder()
	api.HandleGetOAuthSession(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("link status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}
	return testutil.DecodeJSONMap(t, w.Body.Bytes())
}

func completeOAuthCallback(t *testing.T, started startedOAuthSession) {
	t.Helper()
	parsed, err := url.Parse(started.AuthURL)
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	redirectURI := parsed.Query().Get("redirect_uri")
	resp, err := http.Get(redirectURI + "?code=auth-code&state=" + url.QueryEscape(started.SessionID))
	if err != nil {
		t.Fatalf("http.Get callback: %v", err)
	}
	_ = resp.Body.Close()
}

func testOAuthJWT(email string, accountID string) string {
	return testOAuthJWTWithPlan(email, accountID, "")
}

func testOAuthJWTWithPlan(email string, accountID string, planType string) string {
	header := `{"alg":"none","typ":"JWT"}`
	auth := fmt.Sprintf(`"chatgpt_account_id":"%s"`, accountID)
	if strings.TrimSpace(planType) != "" {
		auth += fmt.Sprintf(`,"chatgpt_plan_type":"%s"`, strings.TrimSpace(planType))
	}
	payload := fmt.Sprintf(`{"email":"%s","sub":"sub_123","https://api.openai.com/auth":{%s}}`, email, auth)
	return base64.RawURLEncoding.EncodeToString([]byte(header)) + "." +
		base64.RawURLEncoding.EncodeToString([]byte(payload)) + "."
}

func newOAuthImportRequest(t *testing.T, clientType string, provider string, files ...importedOAuthFile) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("client_type", clientType); err != nil {
		t.Fatalf("WriteField client_type: %v", err)
	}
	if err := writer.WriteField("provider", provider); err != nil {
		t.Fatalf("WriteField provider: %v", err)
	}
	for _, file := range files {
		part, err := writer.CreateFormFile("files", file.Name)
		if err != nil {
			t.Fatalf("CreateFormFile(%q): %v", file.Name, err)
		}
		if _, err := io.WriteString(part, file.Body); err != nil {
			t.Fatalf("WriteString(%q): %v", file.Name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/oauth/import/cli-proxy-api", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func findOAuthImportResult(results []OAuthImportFileResultResponse, file string) OAuthImportFileResultResponse {
	for _, result := range results {
		if result.File == file {
			return result
		}
	}
	return OAuthImportFileResultResponse{}
}
