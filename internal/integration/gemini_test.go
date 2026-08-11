package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PAIArtCom/Clipal/internal/config"
)

func TestGeminiStatus_NotConfiguredWhenFileMissing(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	m := Manager{configDir: filepath.Join(home, ".clipal"), homeDir: home}

	status, err := m.Status(ProductGeminiCLI, cfg)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.State != StateNotConfigured {
		t.Fatalf("state = %q", status.State)
	}
	if status.TargetPath != filepath.Join(home, ".gemini", ".env") {
		t.Fatalf("target path = %q", status.TargetPath)
	}
}

func TestGeminiApply_CreatesOrUpdatesDotEnvPreservingOtherEntries(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	geminiDir := filepath.Join(home, ".gemini")
	if err := os.MkdirAll(geminiDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	targetPath := filepath.Join(geminiDir, ".env")
	original := strings.TrimSpace(`
# existing config
FOO=bar
GEMINI_API_BASE=https://old.example.com/gemini
GOOGLE_API_KEY=secret
`) + "\n"
	if err := os.WriteFile(targetPath, []byte(original), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	cfg.Global.Port = 4455
	m := Manager{configDir: filepath.Join(home, ".clipal"), homeDir: home}

	result, err := m.Apply(ProductGeminiCLI, cfg)
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
		"# existing config",
		"FOO=bar",
		"GOOGLE_API_KEY=secret",
		"GEMINI_API_BASE=http://127.0.0.1:4455/clipal",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("updated .env missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "https://old.example.com/gemini") {
		t.Fatalf("old GEMINI_API_BASE should be replaced:\n%s", body)
	}
}

func TestGeminiApply_IsIdempotentAndRollbackRestoresOriginal(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	geminiDir := filepath.Join(home, ".gemini")
	if err := os.MkdirAll(geminiDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	targetPath := filepath.Join(geminiDir, ".env")
	original := strings.TrimSpace(`
GOOGLE_API_KEY=secret
`) + "\n"
	if err := os.WriteFile(targetPath, []byte(original), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	m := Manager{configDir: filepath.Join(home, ".clipal"), homeDir: home}

	if _, err := m.Apply(ProductGeminiCLI, cfg); err != nil {
		t.Fatalf("Apply first: %v", err)
	}
	if _, err := m.Apply(ProductGeminiCLI, cfg); err != nil {
		t.Fatalf("Apply second: %v", err)
	}

	rolledBack, err := m.Rollback(ProductGeminiCLI, cfg)
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
