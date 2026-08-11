package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/PAIArtCom/Clipal/internal/config"
)

func TestContinueStatus_NotConfiguredWhenFileMissing(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	m := Manager{configDir: filepath.Join(home, ".clipal"), homeDir: home}

	status, err := m.Status(ProductContinue, cfg)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.State != StateNotConfigured {
		t.Fatalf("state = %q", status.State)
	}
	if status.TargetPath != filepath.Join(home, ".continue", "config.yaml") {
		t.Fatalf("target path = %q", status.TargetPath)
	}
}

func TestContinueApply_CreatesClipalModelPreservingOtherModels(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	continueDir := filepath.Join(home, ".continue")
	if err := os.MkdirAll(continueDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	targetPath := filepath.Join(continueDir, "config.yaml")
	original := strings.TrimSpace(`
name: Existing Config
version: 1.0.0
schema: v1
docs:
  - keep-me
models:
  - name: Existing Chat
    provider: anthropic
    model: claude-sonnet-4-5
    roles:
      - chat
      - edit
  - name: Existing Auto
    provider: openai
    model: gpt-4.1-mini
    roles:
      - autocomplete
`) + "\n"
	if err := os.WriteFile(targetPath, []byte(original), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	cfg.Global.Port = 4455
	m := Manager{configDir: filepath.Join(home, ".clipal"), homeDir: home}

	result, err := m.Apply(ProductContinue, cfg)
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
	if decoded["name"] != "Existing Config" {
		t.Fatalf("name = %v", decoded["name"])
	}
	docs, _ := decoded["docs"].([]any)
	if len(docs) != 1 || docs[0] != "keep-me" {
		t.Fatalf("docs = %#v", docs)
	}

	models, _ := decoded["models"].([]any)
	if len(models) != 3 {
		t.Fatalf("models len = %d", len(models))
	}

	var foundClipal, foundExisting bool
	for _, item := range models {
		model, _ := item.(map[string]any)
		if model == nil {
			continue
		}
		switch model["name"] {
		case "Clipal":
			foundClipal = true
			if got, _ := model["provider"].(string); got != "openai" {
				t.Fatalf("clipal provider = %q", got)
			}
			if got, _ := model["model"].(string); got != "claude-sonnet-4-5" {
				t.Fatalf("clipal model = %q", got)
			}
			if got, _ := model["apiBase"].(string); got != "http://127.0.0.1:4455/clipal" {
				t.Fatalf("clipal apiBase = %q", got)
			}
			if got, _ := model["apiKey"].(string); got != "clipal" {
				t.Fatalf("clipal apiKey = %q", got)
			}
			roles, _ := model["roles"].([]any)
			if !hasAllStringValues(roles, "chat", "edit", "apply", "autocomplete") {
				t.Fatalf("clipal roles = %#v", roles)
			}
		case "Existing Chat":
			foundExisting = true
		}
	}
	if !foundClipal {
		t.Fatalf("Clipal model missing from models: %#v", models)
	}
	if !foundExisting {
		t.Fatalf("existing model missing from models: %#v", models)
	}
}

func TestContinueApply_IsIdempotentAndRollbackRestoresOriginal(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	continueDir := filepath.Join(home, ".continue")
	if err := os.MkdirAll(continueDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	targetPath := filepath.Join(continueDir, "config.yaml")
	original := strings.TrimSpace(`
name: Existing Config
version: 1.0.0
schema: v1
models:
  - name: Existing Chat
    provider: openai
    model: gpt-5.4
`) + "\n"
	if err := os.WriteFile(targetPath, []byte(original), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	m := Manager{configDir: filepath.Join(home, ".clipal"), homeDir: home}

	if _, err := m.Apply(ProductContinue, cfg); err != nil {
		t.Fatalf("Apply first: %v", err)
	}
	if _, err := m.Apply(ProductContinue, cfg); err != nil {
		t.Fatalf("Apply second: %v", err)
	}

	raw, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile after apply: %v", err)
	}
	if strings.Count(string(raw), "name: Clipal") != 1 {
		t.Fatalf("Clipal model should only appear once:\n%s", raw)
	}

	rolledBack, err := m.Rollback(ProductContinue, cfg)
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

func hasAllStringValues(values []any, wants ...string) bool {
	set := map[string]bool{}
	for _, value := range values {
		text, _ := value.(string)
		if text != "" {
			set[text] = true
		}
	}
	for _, want := range wants {
		if !set[want] {
			return false
		}
	}
	return true
}
