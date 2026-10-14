package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/PAIArtCom/Clipal/internal/config"
	"github.com/PAIArtCom/Clipal/internal/oauth"
)

func accountEnvironmentAPI(t *testing.T) (*Handler, *oauth.Credential) {
	t.Helper()
	dir := t.TempDir()
	h := NewHandler(dir, "test", nil)
	if err := os.WriteFile(filepath.Join(dir, "claude.yaml"), []byte("providers:\n  - name: account\n    proxy_mode: direct\n    auth_type: oauth\n    oauth_provider: claude\n    oauth_ref: owner\n    priority: 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cred := &oauth.Credential{Provider: config.OAuthProviderClaude, Ref: "owner", AccountID: "account-id", AccessToken: "secret-access", RefreshToken: "secret-refresh", Metadata: map[string]string{"country_code": "JP"}}
	if err := h.api.oauth.Store().Save(cred); err != nil {
		t.Fatal(err)
	}
	return h, cred
}

func TestProxyDiagnosticsPublicDoHIsOptInAndNonBlocking(t *testing.T) {
	for _, dns := range []string{"auto", "system", "cloudflare", "google"} {
		for _, enabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/optional=%t", dns, enabled), func(t *testing.T) {
				h, cred := accountEnvironmentAPI(t)
				_, err := h.api.oauth.Store().UpdateEnvironment(cred.Provider, cred.Ref, &oauth.RequestEnvironment{Country: "JP", Timezone: "Asia/Tokyo", Source: "user", DNS: dns})
				if err != nil {
					t.Fatal(err)
				}
				var queries atomic.Int32
				body := `{"ip":"203.0.113.2","country_code":"US","timezone":{"id":"America/New_York"}}`
				// Stub anonymous HTTP targets and reject every public DoH tunnel.
				// No DNS service or authenticated upstream is contacted by this test.
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") != "" || r.Header.Get("Accept-Language") != "" {
						t.Error("diagnostic leaked account headers")
					}
					if r.Method == http.MethodConnect {
						if r.Host == "cloudflare-dns.com:443" || r.Host == "dns.google:443" {
							queries.Add(1)
							w.WriteHeader(http.StatusServiceUnavailable)
							return
						}
						hijacker, ok := w.(http.Hijacker)
						if !ok {
							t.Error("test proxy does not support HTTP CONNECT")
							w.WriteHeader(http.StatusInternalServerError)
							return
						}
						conn, rw, err := hijacker.Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						defer func() { _ = conn.Close() }()
						_, _ = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
						_ = rw.Flush()
						request, err := http.ReadRequest(rw.Reader)
						if err != nil || request.Header.Get("Authorization") != "" {
							t.Error("invalid anonymous target request")
							return
						}
						_, _ = fmt.Fprintf(rw, "HTTP/1.1 200 OK\r\nConnection: close\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
						_ = rw.Flush()
						return
					}
					_, _ = w.Write([]byte(body))
				}))
				defer server.Close()
				cfg := "providers:\n  - name: account\n    auth_type: oauth\n    oauth_provider: claude\n    oauth_ref: owner\n    proxy_mode: custom\n    proxy_url: " + server.URL + "\n    priority: 1\n"
				if err := os.WriteFile(filepath.Join(h.api.configDir, "claude.yaml"), []byte(cfg), 0600); err != nil {
					t.Fatal(err)
				}
				originalTarget, originalURLs := diagnosticTarget, diagnosticExitURLs
				diagnosticTarget = func(config.OAuthProvider) string { return "http://model.example/" }
				diagnosticExitURLs = []string{"http://reference.example/"}
				defer func() { diagnosticTarget, diagnosticExitURLs = originalTarget, originalURLs }()
				files, _ := filepath.Glob(filepath.Join(h.api.configDir, "oauth", "claude", "*.json"))
				before, _ := os.ReadFile(files[0])
				payload := fmt.Sprintf(`{"check_doh":%t}`, enabled)
				req := httptest.NewRequest(http.MethodPost, "/api/providers/claude/account/oauth-diagnostics", strings.NewReader(payload))
				w := httptest.NewRecorder()
				h.api.HandleProviderDiagnostics(w, req)
				var report consistencyReport
				if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
					t.Fatal(err)
				}
				if w.Code != http.StatusOK || report.TargetStatus != 200 || report.ExitIP != "203.0.113.2" || report.Routing.ConnectMode != "proxy_hostname" {
					t.Fatalf("optional query blocked connection checks: %+v", report)
				}
				found := false
				for _, check := range report.Checks {
					if check.Name == "doh_reference" {
						found = true
						if check.Status != "pending" {
							t.Fatal("failed reference falsely verified")
						}
					}
				}
				if found != enabled || (queries.Load() > 0) != enabled {
					t.Fatal("public DoH was not opt-in")
				}
				if enabled {
					want := int32(2)
					if dns == "cloudflare" || dns == "google" {
						want = 1
					}
					if queries.Load() != want {
						t.Fatalf("queries=%d want=%d", queries.Load(), want)
					}
				}
				after, _ := os.ReadFile(files[0])
				if !bytes.Equal(before, after) {
					t.Fatal("diagnostic changed account identity, preferences or tokens")
				}
			})
		}
	}
}

func TestProviderDiagnosticsRejectsStrictOrMalformedOptions(t *testing.T) {
	h, _ := accountEnvironmentAPI(t)
	for _, body := range []string{`{"strict_doh":true}`, `{"check_doh":"yes"}`, `{"check_doh":true}{}`, `{"check_doh":`} {
		w := httptest.NewRecorder()
		h.api.HandleProviderDiagnostics(w, httptest.NewRequest(http.MethodPost, "/api/providers/claude/account/oauth-diagnostics", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest {
			t.Fatal("unsupported diagnostic policy accepted")
		}
	}
}

func TestProviderEnvironmentRoutesValidatePersistAndRedact(t *testing.T) {
	h, cred := accountEnvironmentAPI(t)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	path := "/api/providers/claude/account/oauth-environment"
	call := func(method, body string, csrf bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://127.0.0.1"+path, strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:3210"
		if csrf {
			req.Header.Set("X-Clipal-UI", "1")
			req.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}
	if w := call(http.MethodPut, `{"country":"US"}`, false); w.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status=%d", w.Code)
	}
	if w := call(http.MethodPut, `{"country":"US","dns":"google"}`, true); w.Code != http.StatusBadRequest {
		t.Fatalf("environment accepted an unsupported DNS edit: %d", w.Code)
	}
	w := call(http.MethodGet, "", false)
	if w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	var result environmentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Environment.DeviceID != cred.Environment.DeviceID || !result.Environment.NeedsConfirmation || result.Environment.Country != "" || result.UpstreamRegion["country_code"] != "JP" || result.Environment.DNS != "auto" {
		t.Fatalf("environment=%+v", result.Environment)
	}
	if bytes.Contains(w.Body.Bytes(), []byte("secret-access")) || bytes.Contains(w.Body.Bytes(), []byte("refresh_token")) {
		t.Fatal("environment API exposed tokens")
	}

	options := map[string]string{"country": "US", "timezone": "America/New_York", "account_identity": result.AccountIdentity}
	raw, _ := json.Marshal(options)
	if w := call(http.MethodPut, string(raw)+`{}`, true); w.Code != http.StatusBadRequest {
		t.Fatal("trailing JSON accepted")
	}
	if w := call(http.MethodPut, string(raw), true); w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	saved, err := h.api.oauth.Load(cred.Provider, cred.Ref)
	if err != nil || saved.Environment.Source != "user" || saved.Environment.NeedsConfirmation || saved.Environment.Timezone != "America/New_York" {
		t.Fatal("confirmation was not persisted")
	}
	options["timezone"] = "UTC-5"
	raw, _ = json.Marshal(options)
	if w := call(http.MethodPut, string(raw), true); w.Code != http.StatusBadRequest {
		t.Fatal("invalid timezone accepted")
	}
	options["timezone"] = "America/New_York"
	options["account_identity"] = "different-account"
	raw, _ = json.Marshal(options)
	if w := call(http.MethodPut, string(raw), true); w.Code != http.StatusConflict {
		t.Fatal("stale account edit accepted")
	}

}

func TestTokenOnlyLegacyAccountCanConfirmRequestRegion(t *testing.T) {
	h, cred := accountEnvironmentAPI(t)
	cred.AccountID = ""
	cred.Environment = nil
	files, err := filepath.Glob(filepath.Join(h.api.configDir, "oauth", "claude", "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("credential files: %v %v", files, err)
	}
	raw, err := json.Marshal(cred)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(files[0], raw, 0600); err != nil {
		t.Fatal(err)
	}
	path := "/api/providers/claude/account/oauth-environment"
	w := httptest.NewRecorder()
	h.api.HandleProviderEnvironment(w, httptest.NewRequest(http.MethodGet, path, nil))
	var result environmentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != http.StatusOK {
		t.Fatalf("read environment: %s %v", w.Body.String(), err)
	}
	if result.AccountIdentity == "" || result.Environment.DeviceID != oauth.AccountDeviceID(cred) || !result.Environment.NeedsConfirmation {
		t.Fatal("token-only account has no usable stable editing identity")
	}
	options, _ := json.Marshal(map[string]string{"country": "JP", "timezone": "Asia/Tokyo", "account_identity": result.AccountIdentity})
	w = httptest.NewRecorder()
	h.api.HandleProviderEnvironment(w, httptest.NewRequest(http.MethodPut, path, bytes.NewReader(options)))
	if w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	saved, err := h.api.oauth.Load(cred.Provider, cred.Ref)
	if err != nil || saved.Environment.NeedsConfirmation || saved.Environment.Source != "user" || saved.Environment.Country != "JP" || saved.Environment.DeviceID != result.Environment.DeviceID || saved.AccessToken != cred.AccessToken || saved.RefreshToken != cred.RefreshToken {
		t.Fatalf("confirmation changed identity/tokens or failed: %v", err)
	}
	// The fallback is only for environment editing, never a provider link identity.
	if oauth.AccountIdentityKey(saved) != "" {
		t.Fatal("environment confirmation changed provider identity semantics")
	}
}

func TestProviderDiagnosticsDoesNotInferTargetExitOrAccountRegion(t *testing.T) {
	h, cred := accountEnvironmentAPI(t)
	_, err := h.api.oauth.Store().UpdateEnvironment(cred.Provider, cred.Ref, &oauth.RequestEnvironment{Country: "JP", Timezone: "Asia/Tokyo", Source: "user", DNS: "system"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("diagnostic leaked credentials")
		}
		ioBody := `{"ip":"203.0.113.2","country_code":"US","timezone":{"id":"America/New_York"}}`
		_, _ = w.Write([]byte(ioBody))
	}))
	defer server.Close()
	originalTarget := diagnosticTarget
	diagnosticTarget = func(config.OAuthProvider) string { return server.URL + "/target" }
	defer func() { diagnosticTarget = originalTarget }()
	original := diagnosticExitURLs
	diagnosticExitURLs = []string{server.URL}
	defer func() { diagnosticExitURLs = original }()
	req := httptest.NewRequest(http.MethodPost, "/api/providers/claude/account/oauth-diagnostics", nil)
	w := httptest.NewRecorder()
	h.api.HandleProviderDiagnostics(w, req)
	if w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	var report consistencyReport
	_ = json.Unmarshal(w.Body.Bytes(), &report)
	found := false
	for _, check := range report.Checks {
		if check.Name == "network_exit" && check.Status == "pending" {
			found = true
		}
		if check.Name == "network_exit" && check.Status == "ok" {
			t.Fatal("target exit falsely verified")
		}
		if check.Name == "dns" && check.Status == "ok" {
			t.Fatal("system DNS was falsely verified")
		}
	}
	if !found || report.ExitIP != "203.0.113.2" || report.ObservationSource != server.URL || report.TargetStatus != 200 || report.BootstrapDNS != "DoH is not used" {
		t.Fatalf("report=%+v", report)
	}
	stored, err := h.api.oauth.Load(cred.Provider, cred.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Environment.Country != "JP" {
		t.Fatal("diagnostic rewrote account region")
	}
}
