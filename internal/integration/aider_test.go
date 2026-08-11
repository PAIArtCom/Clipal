package integration

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/PAIArtCom/Clipal/internal/config"
)

func TestAiderStatus_NotConfiguredWhenFileMissing(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	m := Manager{configDir: filepath.Join(home, ".clipal"), homeDir: home}

	status, err := m.Status(ProductAider, cfg)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.State != StateNotConfigured {
		t.Fatalf("state = %q", status.State)
	}
	if status.TargetPath != filepath.Join(home, ".aider.conf.yml") {
		t.Fatalf("target path = %q", status.TargetPath)
	}
}

func TestAiderApply_UpsertsOpenAIBasePreservingOtherKeys(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	targetPath := filepath.Join(home, ".aider.conf.yml")
	original := "analytics: false\nmodel: anthropic/claude-sonnet-4-5\nopenai-api-key: user-key\n"
	if err := os.WriteFile(targetPath, []byte(original), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	cfg.Global.Port = 4455
	m := Manager{configDir: filepath.Join(home, ".clipal"), homeDir: home}

	result, err := m.Apply(ProductAider, cfg)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Status.State != StateConfigured {
		t.Fatalf("state = %q", result.Status.State)
	}

	raw, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var decoded map[string]any
	if err := yaml.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("yaml.Unmarshal: %v\nbody=%s", err, raw)
	}
	if got, _ := decoded["analytics"].(bool); got {
		t.Fatalf("analytics = %v", got)
	}
	if got, _ := decoded["openai-api-base"].(string); got != "http://127.0.0.1:4455/clipal" {
		t.Fatalf("openai-api-base = %q", got)
	}
	if got, _ := decoded["openai-api-key"].(string); got != "user-key" {
		t.Fatalf("openai-api-key = %q", got)
	}
	if got, _ := decoded["model"].(string); got != "openai/claude-sonnet-4-5" {
		t.Fatalf("model = %q", got)
	}
}

func TestAiderApply_InsertsPlaceholderKeyWhenMissing(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	targetPath := filepath.Join(home, ".aider.conf.yml")
	original := "analytics: false\nmodel: gpt-5.4\n"
	if err := os.WriteFile(targetPath, []byte(original), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	m := Manager{configDir: filepath.Join(home, ".clipal"), homeDir: home}

	result, err := m.Apply(ProductAider, cfg)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Status.State != StateConfigured {
		t.Fatalf("state = %q", result.Status.State)
	}

	raw, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var decoded map[string]any
	if err := yaml.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("yaml.Unmarshal: %v\nbody=%s", err, raw)
	}
	if got, _ := decoded["openai-api-key"].(string); got != "clipal" {
		t.Fatalf("openai-api-key = %q", got)
	}
}

func TestAiderApply_IsIdempotentAndRollbackRestoresOriginal(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	targetPath := filepath.Join(home, ".aider.conf.yml")
	original := "model: gpt-5.4\n"
	if err := os.WriteFile(targetPath, []byte(original), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	m := Manager{configDir: filepath.Join(home, ".clipal"), homeDir: home}

	if _, err := m.Apply(ProductAider, cfg); err != nil {
		t.Fatalf("Apply first: %v", err)
	}
	if _, err := m.Apply(ProductAider, cfg); err != nil {
		t.Fatalf("Apply second: %v", err)
	}

	rolledBack, err := m.Rollback(ProductAider, cfg)
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if rolledBack.Status.State != StateNotConfigured {
		t.Fatalf("rollback state = %q", rolledBack.Status.State)
	}

	got, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile after rollback: %v", err)
	}
	if string(got) != original {
		t.Fatalf("restored body = %q want %q", got, original)
	}
}
