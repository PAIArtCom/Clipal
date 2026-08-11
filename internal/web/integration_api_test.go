package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PAIArtCom/Clipal/internal/config"
	"github.com/PAIArtCom/Clipal/internal/proxy"
)

func TestHandleListIntegrations_ReturnsSupportedProducts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	api := NewAPI(t.TempDir(), "test", nil)
	req := httptest.NewRequest(http.MethodGet, "/api/integrations", nil)
	w := httptest.NewRecorder()

	api.HandleListIntegrations(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	var got []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode json: %v\nbody=%s", err, w.Body.String())
	}
	if len(got) != 7 {
		t.Fatalf("len = %d", len(got))
	}

	products := make([]string, 0, len(got))
	for i, item := range got {
		product, ok := item["product"].(string)
		if !ok {
			t.Fatalf("product[%d] has unexpected type: %#v", i, item["product"])
		}
		products = append(products, product)
	}
	if strings.Join(products, ",") != "claude,codex,opencode,gemini,continue,aider,goose" {
		t.Fatalf("products = %v", products)
	}
}

func TestHandleIntegrationApply_Claude(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	api := NewAPI(t.TempDir(), "test", nil)
	req := httptest.NewRequest(http.MethodPost, "/api/integrations/claude/apply", nil)
	w := httptest.NewRecorder()

	api.HandleIntegrationAction(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	target := filepath.Join(home, ".claude", "settings.json")
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(body), "ANTHROPIC_BASE_URL") {
		t.Fatalf("expected Claude config to be written: %s", body)
	}
}

func TestHandleIntegrationRollback_Codex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	target := filepath.Join(codexDir, "config.toml")
	original := []byte("model = \"gpt-5\"\n")
	if err := os.WriteFile(target, original, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	api := NewAPI(t.TempDir(), "test", nil)

	applyReq := httptest.NewRequest(http.MethodPost, "/api/integrations/codex/apply", nil)
	applyW := httptest.NewRecorder()
	api.HandleIntegrationAction(applyW, applyReq)
	if applyW.Result().StatusCode != http.StatusOK {
		t.Fatalf("apply status=%d body=%s", applyW.Result().StatusCode, applyW.Body.String())
	}

	rollbackReq := httptest.NewRequest(http.MethodPost, "/api/integrations/codex/rollback", nil)
	rollbackW := httptest.NewRecorder()
	api.HandleIntegrationAction(rollbackW, rollbackReq)
	if rollbackW.Result().StatusCode != http.StatusOK {
		t.Fatalf("rollback status=%d body=%s", rollbackW.Result().StatusCode, rollbackW.Body.String())
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile after rollback: %v", err)
	}
	if string(got) != string(original) {
		t.Fatalf("restored body = %q want %q", got, original)
	}
}

func TestHandleIntegrationAction_UnknownProduct(t *testing.T) {
	api := NewAPI(t.TempDir(), "test", nil)
	req := httptest.NewRequest(http.MethodPost, "/api/integrations/nope/apply", nil)
	w := httptest.NewRecorder()

	api.HandleIntegrationAction(w, req)
	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}
}

func TestHandleListIntegrations_IncludesPreviewContent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	target := filepath.Join(codexDir, "config.toml")
	original := "model_provider = \"openai\"\n"
	if err := os.WriteFile(target, []byte(original), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	api := NewAPI(t.TempDir(), "test", nil)
	req := httptest.NewRequest(http.MethodGet, "/api/integrations", nil)
	w := httptest.NewRecorder()

	api.HandleListIntegrations(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	var got []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode json: %v\nbody=%s", err, w.Body.String())
	}

	var codex map[string]any
	for _, item := range got {
		if item["product"] == "codex" {
			codex = item
			break
		}
	}
	if codex == nil {
		t.Fatalf("codex integration not found: %+v", got)
	}

	current, _ := codex["current_content"].(string)
	planned, _ := codex["planned_content"].(string)
	if !strings.Contains(current, original) {
		t.Fatalf("current preview missing original config:\n%s", current)
	}
	for _, want := range []string{
		`model_provider = `,
		`clipal`,
		`[model_providers.clipal]`,
		`wire_api = `,
		`responses`,
	} {
		if !strings.Contains(planned, want) {
			t.Fatalf("planned preview missing %q:\n%s", want, planned)
		}
	}
}

func TestHandleListIntegrations_UsesRuntimeGlobalConfigForPreview(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	cfg.Global.Port = 4444
	router := proxy.NewRouter(cfg)
	api := NewAPI(t.TempDir(), "test", router)

	req := httptest.NewRequest(http.MethodGet, "/api/integrations", nil)
	w := httptest.NewRecorder()

	api.HandleListIntegrations(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	var got []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode json: %v\nbody=%s", err, w.Body.String())
	}

	var claude map[string]any
	for _, item := range got {
		if item["product"] == "claude" {
			claude = item
			break
		}
	}
	if claude == nil {
		t.Fatalf("claude integration not found: %+v", got)
	}

	planned, _ := claude["planned_content"].(string)
	if !strings.Contains(planned, "http://127.0.0.1:4444/clipal") {
		t.Fatalf("planned preview should use runtime port 4444:\n%s", planned)
	}
}

func TestHandleListIntegrations_ConfiguredShowsLatestBackup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	target := filepath.Join(codexDir, "config.toml")
	original := "model_provider = \"openai\"\n"
	if err := os.WriteFile(target, []byte(original), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	api := NewAPI(t.TempDir(), "test", nil)

	applyReq := httptest.NewRequest(http.MethodPost, "/api/integrations/codex/apply", nil)
	applyW := httptest.NewRecorder()
	api.HandleIntegrationAction(applyW, applyReq)
	if applyW.Result().StatusCode != http.StatusOK {
		t.Fatalf("apply status=%d body=%s", applyW.Result().StatusCode, applyW.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/integrations", nil)
	w := httptest.NewRecorder()
	api.HandleListIntegrations(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	var got []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode json: %v\nbody=%s", err, w.Body.String())
	}

	var codex map[string]any
	for _, item := range got {
		if item["product"] == "codex" {
			codex = item
			break
		}
	}
	if codex == nil {
		t.Fatalf("codex integration not found: %+v", got)
	}

	if codex["state"] != "configured" {
		t.Fatalf("state=%v want configured", codex["state"])
	}
	backupContent, _ := codex["backup_content"].(string)
	if !strings.Contains(backupContent, original) {
		t.Fatalf("backup preview missing original config:\n%s", backupContent)
	}
	backupTargetExisted, _ := codex["backup_target_existed"].(bool)
	if !backupTargetExisted {
		t.Fatalf("backup_target_existed=%v want true", codex["backup_target_existed"])
	}
}

func TestHandleIntegrationApply_Gemini(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	geminiDir := filepath.Join(home, ".gemini")
	if err := os.MkdirAll(geminiDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	target := filepath.Join(geminiDir, ".env")
	if err := os.WriteFile(target, []byte("GOOGLE_API_KEY=secret\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	api := NewAPI(t.TempDir(), "test", nil)
	req := httptest.NewRequest(http.MethodPost, "/api/integrations/gemini/apply", nil)
	w := httptest.NewRecorder()

	api.HandleIntegrationAction(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(body), "GEMINI_API_BASE=http://127.0.0.1:3333/clipal") {
		t.Fatalf("expected Gemini .env to be updated: %s", body)
	}
	if !strings.Contains(string(body), "GOOGLE_API_KEY=secret") {
		t.Fatalf("expected Gemini .env to preserve other entries: %s", body)
	}
}

func TestHandleIntegrationApply_Continue(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	continueDir := filepath.Join(home, ".continue")
	if err := os.MkdirAll(continueDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	target := filepath.Join(continueDir, "config.yaml")
	if err := os.WriteFile(target, []byte("models:\n  - name: Existing\n    provider: openai\n    model: gpt-5.4\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	api := NewAPI(t.TempDir(), "test", nil)
	req := httptest.NewRequest(http.MethodPost, "/api/integrations/continue/apply", nil)
	w := httptest.NewRecorder()

	api.HandleIntegrationAction(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(body), "name: Clipal") {
		t.Fatalf("expected Continue config to contain Clipal model: %s", body)
	}
	if !strings.Contains(string(body), "apiBase: http://127.0.0.1:3333/clipal") {
		t.Fatalf("expected Continue config to point at Clipal: %s", body)
	}
}
