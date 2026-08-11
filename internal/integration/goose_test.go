package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/PAIArtCom/Clipal/internal/config"
)

func TestGooseStatus_NotConfiguredWhenFileMissing(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	m := Manager{configDir: filepath.Join(home, ".clipal"), homeDir: home}

	status, err := m.Status(ProductGoose, cfg)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.State != StateNotConfigured {
		t.Fatalf("state = %q", status.State)
	}
	if status.TargetPath != filepath.Join(home, ".config", "goose", "custom_providers", "clipal.json") {
		t.Fatalf("target path = %q", status.TargetPath)
	}
}

func TestGooseApply_CreatesManagedCustomProviderFile(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	cfg.Global.Port = 4455
	m := Manager{configDir: filepath.Join(home, ".clipal"), homeDir: home}

	result, err := m.Apply(ProductGoose, cfg)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Status.State != StateConfigured {
		t.Fatalf("state = %q", result.Status.State)
	}

	targetPath := filepath.Join(home, ".config", "goose", "custom_providers", "clipal.json")
	raw, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v\nbody=%s", err, raw)
	}
	if got, _ := decoded["name"].(string); got != "clipal" {
		t.Fatalf("name = %q", got)
	}
	if got, _ := decoded["engine"].(string); got != "openai" {
		t.Fatalf("engine = %q", got)
	}
	if got, _ := decoded["display_name"].(string); got != "Clipal" {
		t.Fatalf("display_name = %q", got)
	}
	if got, _ := decoded["base_url"].(string); got != "http://127.0.0.1:4455/clipal/v1/chat/completions" {
		t.Fatalf("base_url = %q", got)
	}
	models, _ := decoded["models"].([]any)
	if len(models) != 1 {
		t.Fatalf("models len = %d", len(models))
	}
	model, _ := models[0].(map[string]any)
	if got, _ := model["name"].(string); got != "gpt-5.4" {
		t.Fatalf("model name = %q", got)
	}
}

func TestGooseApply_IsIdempotentAndRollbackRemovesManagedFile(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	m := Manager{configDir: filepath.Join(home, ".clipal"), homeDir: home}

	if _, err := m.Apply(ProductGoose, cfg); err != nil {
		t.Fatalf("Apply first: %v", err)
	}
	if _, err := m.Apply(ProductGoose, cfg); err != nil {
		t.Fatalf("Apply second: %v", err)
	}

	targetPath := filepath.Join(home, ".config", "goose", "custom_providers", "clipal.json")
	raw, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile after apply: %v", err)
	}
	if string(raw) == "" {
		t.Fatalf("managed Goose provider file should not be empty")
	}

	rolledBack, err := m.Rollback(ProductGoose, cfg)
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if rolledBack.Status.State != StateNotConfigured {
		t.Fatalf("rollback state = %q", rolledBack.Status.State)
	}
	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		t.Fatalf("managed Goose provider file should be removed, stat err = %v", err)
	}
}
