package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lansespirit/Clipal/internal/config"
)

func TestClaudeStatus_NotConfiguredWhenFileMissing(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	m := Manager{configDir: filepath.Join(home, ".clipal"), homeDir: home}

	status, err := m.Status(ProductClaudeCode, cfg)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.State != StateNotConfigured {
		t.Fatalf("state = %q", status.State)
	}
	if status.TargetPath != filepath.Join(home, ".claude", "settings.json") {
		t.Fatalf("target path = %q", status.TargetPath)
	}
}

func TestClaudeStatus_ConfiguredWithPlaceholderAPIKey(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	targetPath := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(targetPath, []byte(`{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:3333/clipal","ANTHROPIC_API_KEY":"clipal"}}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	m := Manager{configDir: filepath.Join(home, ".clipal"), homeDir: home}

	status, err := m.Status(ProductClaudeCode, cfg)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.State != StateConfigured {
		t.Fatalf("state = %q", status.State)
	}
}

func TestClaudeStatus_NotConfiguredWhenClientAuthMissing(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	targetPath := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(targetPath, []byte(`{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:3333/clipal"}}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	m := Manager{configDir: filepath.Join(home, ".clipal"), homeDir: home}

	status, err := m.Status(ProductClaudeCode, cfg)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.State != StateNotConfigured {
		t.Fatalf("state = %q", status.State)
	}
}

func TestClaudeApply_CreatesAndMergesUserSettings(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	targetPath := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(targetPath, []byte(`{"theme":"dark","env":{"FOO":"bar"}}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	cfg.Global.Port = 4444

	m := Manager{configDir: filepath.Join(home, ".clipal"), homeDir: home}
	result, err := m.Apply(ProductClaudeCode, cfg)
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
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v\nbody=%s", err, raw)
	}
	if decoded["theme"] != "dark" {
		t.Fatalf("theme = %v", decoded["theme"])
	}
	env, ok := decoded["env"].(map[string]any)
	if !ok {
		t.Fatalf("env type = %T", decoded["env"])
	}
	if env["FOO"] != "bar" {
		t.Fatalf("FOO = %v", env["FOO"])
	}
	if env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:4444/clipal" {
		t.Fatalf("ANTHROPIC_BASE_URL = %v", env["ANTHROPIC_BASE_URL"])
	}
	if env["ANTHROPIC_API_KEY"] != "clipal" {
		t.Fatalf("ANTHROPIC_API_KEY = %v", env["ANTHROPIC_API_KEY"])
	}
	if _, exists := env["ANTHROPIC_AUTH_TOKEN"]; exists {
		t.Fatalf("ANTHROPIC_AUTH_TOKEN should not be inserted: %v", env["ANTHROPIC_AUTH_TOKEN"])
	}

	homeConfigRaw, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatalf("ReadFile home config: %v", err)
	}
	var homeConfig map[string]any
	if err := json.Unmarshal(homeConfigRaw, &homeConfig); err != nil {
		t.Fatalf("Unmarshal home config: %v\nbody=%s", err, homeConfigRaw)
	}
	if got, _ := homeConfig["hasCompletedOnboarding"].(bool); !got {
		t.Fatalf("hasCompletedOnboarding = %v", homeConfig["hasCompletedOnboarding"])
	}
}

func TestClaudeApply_PreservesExistingAuthToken(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	targetPath := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(targetPath, []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"user-token"}}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	m := Manager{configDir: filepath.Join(home, ".clipal"), homeDir: home}

	result, err := m.Apply(ProductClaudeCode, cfg)
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
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v\nbody=%s", err, raw)
	}
	env, ok := decoded["env"].(map[string]any)
	if !ok {
		t.Fatalf("env type = %T", decoded["env"])
	}
	if env["ANTHROPIC_AUTH_TOKEN"] != "user-token" {
		t.Fatalf("ANTHROPIC_AUTH_TOKEN = %v", env["ANTHROPIC_AUTH_TOKEN"])
	}
	if _, exists := env["ANTHROPIC_API_KEY"]; exists {
		t.Fatalf("ANTHROPIC_API_KEY should not be inserted when auth token exists: %v", env["ANTHROPIC_API_KEY"])
	}
}

func TestClaudeApply_IsIdempotentAndRollbackRestoresOriginal(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	targetPath := filepath.Join(claudeDir, "settings.json")
	original := []byte("{\"env\":{\"FOO\":\"bar\"}}\n")
	if err := os.WriteFile(targetPath, original, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	homeConfigPath := filepath.Join(home, ".claude.json")
	homeOriginal := []byte("{\"hasCompletedOnboarding\":false,\"theme\":\"dark\"}\n")
	if err := os.WriteFile(homeConfigPath, homeOriginal, 0o600); err != nil {
		t.Fatalf("WriteFile home config: %v", err)
	}

	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	m := Manager{configDir: filepath.Join(home, ".clipal"), homeDir: home}

	if _, err := m.Apply(ProductClaudeCode, cfg); err != nil {
		t.Fatalf("Apply first: %v", err)
	}
	if _, err := m.Apply(ProductClaudeCode, cfg); err != nil {
		t.Fatalf("Apply second: %v", err)
	}

	rolledBack, err := m.Rollback(ProductClaudeCode, cfg)
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
	if string(got) != string(original) {
		t.Fatalf("restored body = %q want %q", got, original)
	}

	gotHome, err := os.ReadFile(homeConfigPath)
	if err != nil {
		t.Fatalf("ReadFile home config after rollback: %v", err)
	}
	if string(gotHome) != string(homeOriginal) {
		t.Fatalf("restored home config = %q want %q", gotHome, homeOriginal)
	}
}
