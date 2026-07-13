package web

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lansespirit/Clipal/internal/transfer"
)

func TestLocalOnly_RejectsNonLoopbackRemote(t *testing.T) {
	dir := t.TempDir()
	h := NewHandler(dir, "test", nil)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	req.Host = "localhost:3333"
	req.RemoteAddr = "8.8.8.8:12345"
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestLocalOnly_RejectsNonLocalHostHeader(t *testing.T) {
	dir := t.TempDir()
	h := NewHandler(dir, "test", nil)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "http://evil.example/", nil)
	req.Host = "evil.example:3333"
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestLocalOnly_APIStateChanging_RequiresUIHeader(t *testing.T) {
	dir := t.TempDir()
	h := NewHandler(dir, "test", nil)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body := []byte(`{
  "listen_addr": "127.0.0.1",
  "port": 3333,
  "log_level": "info",
  "reactivate_after": "10m",
  "upstream_idle_timeout": "1m",
  "max_request_body_bytes": 12345,
  "log_dir": "",
  "log_retention_days": 7,
  "log_stdout": true,
  "notifications": {
    "enabled": false,
    "min_level": "error",
    "provider_switch": true
  },
  "circuit_breaker": {
    "failure_threshold": 4,
    "success_threshold": 2,
    "open_timeout": "60s",
    "half_open_max_inflight": 1
  }
}`)

	req := httptest.NewRequest(http.MethodPut, "http://localhost/api/config/global/update", bytes.NewReader(body))
	req.Host = "localhost:3333"
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got status=%d body=%s", w.Code, w.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodPut, "http://localhost/api/config/global/update", bytes.NewReader(body))
	req2.Host = "localhost:3333"
	req2.RemoteAddr = "127.0.0.1:12345"
	req2.Header.Set("X-Clipal-UI", "1")
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()

	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got status=%d body=%s", w2.Code, w2.Body.String())
	}
}

func TestServeIndex_ContentTypeAndNotFound(t *testing.T) {
	h := NewHandler(t.TempDir(), "test", nil)

	req := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	w := httptest.NewRecorder()
	h.serveIndex(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("content-type=%q", got)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache-control=%q", got)
	}
	body := w.Body.String()
	if body == "" {
		t.Fatalf("expected index body to be non-empty")
	}

	req = httptest.NewRequest(http.MethodGet, "http://localhost/missing", nil)
	w = httptest.NewRecorder()
	h.serveIndex(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestServeStatic_SetsNoStoreCacheControl(t *testing.T) {
	h := NewHandler(t.TempDir(), "test", nil)

	req := httptest.NewRequest(http.MethodGet, "http://localhost/static/app.js", nil)
	w := httptest.NewRecorder()
	h.serveStatic(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache-control=%q", got)
	}
}

func TestServeStatic_ContentTypeAndNotFound(t *testing.T) {
	h := NewHandler(t.TempDir(), "test", nil)

	req := httptest.NewRequest(http.MethodGet, "http://localhost/static/app.js", nil)
	w := httptest.NewRecorder()
	h.serveStatic(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "text/javascript; charset=utf-8" && got != "application/javascript" {
		t.Fatalf("content-type=%q", got)
	}

	req = httptest.NewRequest(http.MethodGet, "http://localhost/static/missing.js", nil)
	w = httptest.NewRecorder()
	h.serveStatic(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing status=%d body=%s", w.Code, w.Body.String())
	}

}

func TestLoopbackAndLocalhostHelpers(t *testing.T) {
	t.Parallel()

	if !isLoopbackRemote("[::1]:3333") {
		t.Fatalf("expected IPv6 loopback remote to pass")
	}
	if !isLoopbackRemote("127.0.0.1:3333") {
		t.Fatalf("expected IPv4 loopback remote to pass")
	}
	if isLoopbackRemote("192.168.1.10:3333") {
		t.Fatalf("expected non-loopback remote to fail")
	}

	for _, host := range []string{"", "localhost:3333", "foo.localhost:9999", "[::1]:4444", "127.0.0.1:5555"} {
		if !isLocalhostHost(host) {
			t.Fatalf("expected host %q to be accepted as localhost", host)
		}
	}
	if isLocalhostHost("example.com:3333") {
		t.Fatalf("expected example.com to be rejected")
	}
}

func TestLocalOnly_AllowsIPv6LoopbackAndEmptyHost(t *testing.T) {
	called := false
	h := NewHandler(t.TempDir(), "test", nil)
	handler := h.localOnly(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	req.RemoteAddr = "[::1]:4321"
	req.Host = ""
	w := httptest.NewRecorder()

	handler(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !called {
		t.Fatalf("expected wrapped handler to be called")
	}
}

func TestLocalOnly_AllowsRemoteWhenEnabledInConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("allow_remote_web_ui: true\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	h := NewHandler(dir, "test", nil)
	called := false
	handler := h.localOnly(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	req.RemoteAddr = "192.0.2.10:12345"
	req.Host = "example.com"
	w := httptest.NewRecorder()
	handler(w, req)
	if w.Code != http.StatusNoContent || !called {
		t.Fatalf("status=%d called=%v body=%s", w.Code, called, w.Body.String())
	}
	if got := w.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options=%q", got)
	}
	if got := w.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy=%q", got)
	}
}

func TestLocalOnly_RemoteModeStillRequiresStateChangeHeader(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("allow_remote_web_ui: true\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	h := NewHandler(dir, "test", nil)
	handler := h.localOnly(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/test", strings.NewReader(`{}`))
	req.RemoteAddr = "192.0.2.10:12345"
	req.Host = "example.com"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestLocalOnly_APIStateChanging_RejectsWrongContentType(t *testing.T) {
	dir := t.TempDir()
	h := NewHandler(dir, "test", nil)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPut, "http://localhost/api/config/global/update", strings.NewReader(`{}`))
	req.Host = "localhost:3333"
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Clipal-UI", "1")
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestLocalOnly_APIStateChanging_AllowsMultipartForOAuthImport(t *testing.T) {
	h := NewHandler(t.TempDir(), "test", nil)
	called := false
	handler := h.localOnly(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("client_type", "openai"); err != nil {
		t.Fatalf("WriteField client_type: %v", err)
	}
	part, err := writer.CreateFormFile("files", "codex.json")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write([]byte(`{"type":"codex"}`)); err != nil {
		t.Fatalf("part.Write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "http://localhost/api/oauth/import/cli-proxy-api", &body)
	req.Host = "localhost:3333"
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Clipal-UI", "1")
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	handler(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !called {
		t.Fatalf("expected wrapped handler to be called")
	}
}

func TestIntegrations_RouteIsRegistered(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	h := NewHandler(t.TempDir(), "test", nil)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "http://localhost/api/integrations", nil)
	req.Host = "localhost:3333"
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("route status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestLegacyConfigExportRouteIsNotRegistered(t *testing.T) {
	h := NewHandler(t.TempDir(), "test", nil)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "http://localhost/api/config/export", nil)
	req.Host = "localhost:3333"
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("legacy export route status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestDataImportUsesBoundedAggregateRequestLimit(t *testing.T) {
	if got := apiRequestBodyLimit("/api/data/import/preview"); got != transfer.MaxJSONImportRequestBytes {
		t.Fatalf("preview body limit=%d want=%d", got, transfer.MaxJSONImportRequestBytes)
	}
	if transfer.MaxJSONImportRequestBytes <= transfer.MaxImportFileBytes {
		t.Fatalf("encoded request limit=%d must exceed per-file limit=%d", transfer.MaxJSONImportRequestBytes, transfer.MaxImportFileBytes)
	}
}

func TestLocalOnly_APIBodyLimit(t *testing.T) {
	h := NewHandler(t.TempDir(), "test", nil)
	called := false
	handler := h.localOnly(func(w http.ResponseWriter, r *http.Request) {
		called = true
		buf := make([]byte, maxAPIRequestBytes+1)
		_, err := r.Body.Read(buf)
		if err == nil {
			t.Fatalf("expected request body limit error")
		}
		writeError(w, err.Error(), http.StatusRequestEntityTooLarge)
	})

	req := httptest.NewRequest(http.MethodPost, "http://localhost/api/test", bytes.NewReader(bytes.Repeat([]byte("a"), maxAPIRequestBytes+1)))
	req.Host = "localhost:3333"
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Clipal-UI", "1")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler(w, req)
	if !called {
		t.Fatalf("expected wrapped handler to be called")
	}
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
