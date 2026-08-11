package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PAIArtCom/Clipal/internal/config"
)

func TestCodexStatus_NotConfiguredWhenFileMissing(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	m := Manager{configDir: filepath.Join(home, ".clipal"), homeDir: home}

	status, err := m.Status(ProductCodexCLI, cfg)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.State != StateNotConfigured {
		t.Fatalf("state = %q", status.State)
	}
	if status.TargetPath != filepath.Join(home, ".codex", "config.toml") {
		t.Fatalf("target path = %q", status.TargetPath)
	}
}

func TestCodexApply_CreatesProviderAndPreservesOthers(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	targetPath := filepath.Join(codexDir, "config.toml")
	original := `
model = "gpt-5"

[model_providers.other]
name = "other"
base_url = "https://other.example/v1"
`
	if err := os.WriteFile(targetPath, []byte(strings.TrimSpace(original)+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	cfg.Global.Port = 4455
	m := Manager{configDir: filepath.Join(home, ".clipal"), homeDir: home}

	result, err := m.Apply(ProductCodexCLI, cfg)
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
	body := string(raw)
	for _, want := range []string{
		`model = `,
		`gpt-5`,
		`model_provider = `,
		`clipal`,
		`[model_providers.other]`,
		`https://other.example/v1`,
		`[model_providers.clipal]`,
		`http://127.0.0.1:4455/clipal`,
		`wire_api`,
		`responses`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("config missing %q\n%s", want, body)
		}
	}
	for _, want := range []string{
		`[model_providers.other]`,
		`[model_providers.clipal]`,
		`name = `,
		`base_url = `,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("config missing %q\n%s", want, body)
		}
	}
}

func TestCodexApply_UpdatesExistingModelProvider(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	targetPath := filepath.Join(codexDir, "config.toml")
	original := `
model_provider = "openai"

[model_providers.openai]
name = "openai"
base_url = "https://api.openai.example/v1"
`
	if err := os.WriteFile(targetPath, []byte(strings.TrimSpace(original)+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	m := Manager{configDir: filepath.Join(home, ".clipal"), homeDir: home}

	result, err := m.Apply(ProductCodexCLI, cfg)
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
	body := string(raw)
	if strings.Contains(body, `model_provider = "openai"`) || strings.Contains(body, `model_provider = 'openai'`) {
		t.Fatalf("stale model_provider remained:\n%s", body)
	}
	decoded, err := readTOMLMap(targetPath)
	if err != nil {
		t.Fatalf("readTOMLMap: %v", err)
	}
	if got, _ := decoded["model_provider"].(string); got != "clipal" {
		t.Fatalf("model_provider = %q", got)
	}
	modelProviders, _ := decoded["model_providers"].(map[string]any)
	if modelProviders == nil {
		t.Fatalf("model_providers missing")
	}
	if _, ok := modelProviders["openai"].(map[string]any); !ok {
		t.Fatalf("openai provider missing")
	}
	clipalProvider, _ := modelProviders["clipal"].(map[string]any)
	if clipalProvider == nil {
		t.Fatalf("clipal provider missing")
	}
	if got, _ := clipalProvider["base_url"].(string); got != "http://127.0.0.1:3333/clipal" {
		t.Fatalf("clipal base_url = %q", got)
	}
	if got, _ := clipalProvider["wire_api"].(string); got != "responses" {
		t.Fatalf("clipal wire_api = %q", got)
	}
}

func TestCodexApply_IsIdempotentAndRollbackRestoresOriginal(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	targetPath := filepath.Join(codexDir, "config.toml")
	original := []byte("model = \"gpt-5\"\n")
	if err := os.WriteFile(targetPath, original, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	m := Manager{configDir: filepath.Join(home, ".clipal"), homeDir: home}

	if _, err := m.Apply(ProductCodexCLI, cfg); err != nil {
		t.Fatalf("Apply first: %v", err)
	}
	if _, err := m.Apply(ProductCodexCLI, cfg); err != nil {
		t.Fatalf("Apply second: %v", err)
	}

	rolledBack, err := m.Rollback(ProductCodexCLI, cfg)
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
}
