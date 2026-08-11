package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PAIArtCom/Clipal/internal/config"
)

func TestOpenCodeStatus_NotConfiguredWhenFileMissing(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	m := Manager{configDir: filepath.Join(home, ".clipal"), homeDir: home}

	status, err := m.Status(ProductOpenCode, cfg)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.State != StateNotConfigured {
		t.Fatalf("state = %q", status.State)
	}
	if status.TargetPath != filepath.Join(home, ".config", "opencode", "opencode.json") {
		t.Fatalf("target path = %q", status.TargetPath)
	}
}

func TestOpenCodeApply_CreatesProviderAndSwitchesModel(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	opencodeDir := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(opencodeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	targetPath := filepath.Join(opencodeDir, "opencode.json")
	original := `{
  "$schema": "https://opencode.ai/config.json",
  "theme": "opencode",
  "model": "anthropic/claude-sonnet-4-5",
  "small_model": "anthropic/claude-haiku-4-5",
  "provider": {
    "anthropic": {
      "options": {
        "apiKey": "{env:ANTHROPIC_API_KEY}"
      },
      "models": {
        "claude-sonnet-4-5": {
          "name": "Claude Sonnet 4.5"
        }
      }
    }
  }
}
`
	if err := os.WriteFile(targetPath, []byte(original), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	cfg.Global.Port = 4455
	m := Manager{configDir: filepath.Join(home, ".clipal"), homeDir: home}

	result, err := m.Apply(ProductOpenCode, cfg)
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
	if got, _ := decoded["theme"].(string); got != "opencode" {
		t.Fatalf("theme = %q", got)
	}
	if got, _ := decoded["model"].(string); got != "clipal/claude-sonnet-4-5" {
		t.Fatalf("model = %q", got)
	}
	if got, _ := decoded["small_model"].(string); got != "clipal/claude-haiku-4-5" {
		t.Fatalf("small_model = %q", got)
	}
	providers, _ := decoded["provider"].(map[string]any)
	if providers == nil {
		t.Fatalf("provider missing")
	}
	if _, ok := providers["anthropic"].(map[string]any); !ok {
		t.Fatalf("anthropic provider missing")
	}
	clipalProvider, _ := providers["clipal"].(map[string]any)
	if clipalProvider == nil {
		t.Fatalf("clipal provider missing")
	}
	if got, _ := clipalProvider["npm"].(string); got != "@ai-sdk/openai-compatible" {
		t.Fatalf("clipal npm = %q", got)
	}
	options, _ := clipalProvider["options"].(map[string]any)
	if options == nil {
		t.Fatalf("clipal options missing")
	}
	if got, _ := options["baseURL"].(string); got != "http://127.0.0.1:4455/clipal" {
		t.Fatalf("clipal baseURL = %q", got)
	}
	if got, _ := options["apiKey"].(string); got != "clipal" {
		t.Fatalf("clipal apiKey = %q", got)
	}
	models, _ := clipalProvider["models"].(map[string]any)
	if models == nil {
		t.Fatalf("clipal models missing")
	}
	modelConfig, _ := models["claude-sonnet-4-5"].(map[string]any)
	if modelConfig == nil {
		t.Fatalf("clipal model config missing")
	}
	if got, _ := modelConfig["name"].(string); got != "Claude Sonnet 4.5" {
		t.Fatalf("clipal model name = %q", got)
	}
}

func TestOpenCodeApply_SupportsJSONCAndRollbackRestoresOriginal(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	opencodeDir := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(opencodeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	targetPath := filepath.Join(opencodeDir, "opencode.json")
	original := strings.TrimSpace(`{
  "$schema": "https://opencode.ai/config.json",
  // comment should not break takeover
  "model": "openai/gpt-5.4"
}`) + "\n"
	if err := os.WriteFile(targetPath, []byte(original), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	m := Manager{configDir: filepath.Join(home, ".clipal"), homeDir: home}

	if _, err := m.Apply(ProductOpenCode, cfg); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	rolledBack, err := m.Rollback(ProductOpenCode, cfg)
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
