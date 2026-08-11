package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/PAIArtCom/Clipal/internal/config"
	"github.com/PAIArtCom/Clipal/internal/proxy"
	"github.com/PAIArtCom/Clipal/internal/testutil"
)

func writeConfigFixture(t *testing.T, dir string, cfg *config.Config) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), formatGlobalConfigYAML(cfg.Global), 0o600); err != nil {
		t.Fatalf("write config.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "openai.yaml"), formatClientConfigYAML("codex", cfg.OpenAI), 0o600); err != nil {
		t.Fatalf("write openai.yaml: %v", err)
	}
}

func newRuntimeAPI(t *testing.T) (*API, *proxy.Router, *config.Config, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		Global: config.DefaultGlobalConfig(),
		OpenAI: config.ClientConfig{
			Mode: config.ClientModeAuto,
			Providers: []config.Provider{
				{Name: "p1", BaseURL: "https://example.com", APIKey: "k1", Priority: 1},
			},
		},
	}
	writeConfigFixture(t, dir, cfg)
	loaded, err := config.Load(dir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	router := proxy.NewRouter(loaded)
	return NewAPI(dir, "test", router), router, loaded, dir
}

func TestLoadConfigOrWriteError_Failure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("unknown: value\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	rec := httptest.NewRecorder()
	api := NewAPI(dir, "test", nil)
	cfg := api.loadConfigOrWriteError(rec)
	if cfg != nil {
		t.Fatalf("expected nil config")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	got := testutil.DecodeJSONMap(t, rec.Body.Bytes())
	if got["status"] != float64(http.StatusInternalServerError) {
		t.Fatalf("body = %#v", got)
	}
}

func TestSaveGlobalConfigOrWriteError_Paths(t *testing.T) {
	api, router, loaded, dir := newRuntimeAPI(t)
	_ = dir

	rec := httptest.NewRecorder()
	badCfg := *loaded
	badCfg.Global.Port = 0
	if api.saveGlobalConfigOrWriteError(rec, &badCfg) {
		t.Fatalf("expected validation failure")
	}
	if got := router.ConfigSnapshot().Global.Port; got != loaded.Global.Port {
		t.Fatalf("runtime port changed on validation failure: %d", got)
	}

	filePath := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	api.configDir = filePath
	rec = httptest.NewRecorder()
	writeFailCfg := *loaded
	writeFailCfg.Global.Port = 4444
	if api.saveGlobalConfigOrWriteError(rec, &writeFailCfg) {
		t.Fatalf("expected write failure")
	}
	if got := router.ConfigSnapshot().Global.Port; got != loaded.Global.Port {
		t.Fatalf("runtime port changed on write failure: %d", got)
	}

	api.configDir = dir
	rec = httptest.NewRecorder()
	okCfg := *loaded
	okCfg.Global.Port = 4545
	okCfg.Global.LogLevel = config.LogLevelDebug
	if !api.saveGlobalConfigOrWriteError(rec, &okCfg) {
		t.Fatalf("expected success: %s", rec.Body.String())
	}
	if got := router.ConfigSnapshot().Global.Port; got != loaded.Global.Port {
		t.Fatalf("runtime port = %d, want %d", got, loaded.Global.Port)
	}
	if got := router.ConfigSnapshot().Global.LogLevel; got != config.LogLevelDebug {
		t.Fatalf("runtime log level = %q, want %q", got, config.LogLevelDebug)
	}
	reloaded, err := config.Load(dir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got := reloaded.Global.Port; got != 4545 {
		t.Fatalf("saved port = %d, want 4545", got)
	}
}

func TestSaveGlobalConfigOrWriteError_RollsBackWhenRuntimeReloadFails(t *testing.T) {
	api, router, loaded, dir := newRuntimeAPI(t)

	if err := os.WriteFile(filepath.Join(dir, "gemini.yaml"), []byte("providers: [\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	rec := httptest.NewRecorder()
	cfg := *loaded
	cfg.Global.LogLevel = config.LogLevelDebug
	if api.saveGlobalConfigOrWriteError(rec, &cfg) {
		t.Fatalf("expected reload failure")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if got := router.ConfigSnapshot().Global.LogLevel; got != loaded.Global.LogLevel {
		t.Fatalf("runtime log level changed on reload failure: %q", got)
	}
	if _, err := config.Load(dir); err == nil {
		t.Fatalf("expected config.Load to fail because gemini.yaml is still broken")
	}
	deleteBroken := filepath.Join(dir, "gemini.yaml")
	if err := os.Remove(deleteBroken); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	reloaded, err := config.Load(dir)
	if err != nil {
		t.Fatalf("config.Load after cleanup: %v", err)
	}
	if got := reloaded.Global.LogLevel; got != loaded.Global.LogLevel {
		t.Fatalf("saved log level = %q, want rollback to %q", got, loaded.Global.LogLevel)
	}
}

func TestSaveClientConfigOrWriteError_Paths(t *testing.T) {
	api, router, loaded, dir := newRuntimeAPI(t)

	rec := httptest.NewRecorder()
	badCfg := *loaded
	badCfg.OpenAI.Mode = config.ClientMode("invalid")
	if api.saveClientConfigOrWriteError(rec, "codex", &badCfg) {
		t.Fatalf("expected validation failure")
	}
	if got := router.ConfigSnapshot().OpenAI.Mode; got != loaded.OpenAI.Mode {
		t.Fatalf("runtime mode changed on validation failure: %q", got)
	}

	rec = httptest.NewRecorder()
	if api.saveClientConfigOrWriteError(rec, "unknown", loaded) {
		t.Fatalf("expected unknown client failure")
	}
	if got := router.ConfigSnapshot().OpenAI.Mode; got != loaded.OpenAI.Mode {
		t.Fatalf("runtime mode changed on unknown client: %q", got)
	}

	filePath := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	api.configDir = filePath
	rec = httptest.NewRecorder()
	writeFailCfg := *loaded
	writeFailCfg.OpenAI.Mode = config.ClientModeManual
	writeFailCfg.OpenAI.PinnedProvider = "p1"
	if api.saveClientConfigOrWriteError(rec, "codex", &writeFailCfg) {
		t.Fatalf("expected write failure")
	}
	if got := router.ConfigSnapshot().OpenAI.Mode; got != loaded.OpenAI.Mode {
		t.Fatalf("runtime mode changed on write failure: %q", got)
	}

	api.configDir = dir
	rec = httptest.NewRecorder()
	okCfg := *loaded
	okCfg.OpenAI.Mode = config.ClientModeManual
	okCfg.OpenAI.PinnedProvider = "p1"
	if !api.saveClientConfigOrWriteError(rec, "codex", &okCfg) {
		t.Fatalf("expected success: %s", rec.Body.String())
	}
	if got := router.ConfigSnapshot().OpenAI.Mode; got != config.ClientModeManual {
		t.Fatalf("runtime mode = %q, want manual", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "openai.yaml")); err != nil {
		t.Fatalf("expected openai.yaml to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "codex.yaml")); !os.IsNotExist(err) {
		t.Fatalf("expected codex.yaml to stay absent after save, err=%v", err)
	}
}

func TestSaveClientConfigOrWriteError_RollsBackWhenRuntimeReloadFails(t *testing.T) {
	api, router, loaded, dir := newRuntimeAPI(t)

	if err := os.WriteFile(filepath.Join(dir, "gemini.yaml"), []byte("providers: [\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	rec := httptest.NewRecorder()
	cfg := *loaded
	cfg.OpenAI.Mode = config.ClientModeManual
	cfg.OpenAI.PinnedProvider = "p1"
	if api.saveClientConfigOrWriteError(rec, "codex", &cfg) {
		t.Fatalf("expected reload failure")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if got := router.ConfigSnapshot().OpenAI.Mode; got != loaded.OpenAI.Mode {
		t.Fatalf("runtime mode changed on reload failure: %q", got)
	}
	if err := os.Remove(filepath.Join(dir, "gemini.yaml")); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	reloaded, err := config.Load(dir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got := reloaded.OpenAI.Mode; got != loaded.OpenAI.Mode {
		t.Fatalf("saved mode = %q, want rollback to %q", got, loaded.OpenAI.Mode)
	}
}
