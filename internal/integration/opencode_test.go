package integration

import (
	"encoding/json"
	"fmt"
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

func TestOpenCodePreservesProviderAndModelMetadata(t *testing.T) {
	m := Manager{homeDir: t.TempDir(), configDir: t.TempDir()}
	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	path, err := m.opencodeTargetPath()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"model":"custom/alias","provider":{"custom":{"models":{"alias":{"id":"wire-model","name":"User model","limit":{"context":32000,"output":8000}}}},"clipal":{"name":"User gateway","npm":"@ai-sdk/openai","options":{"headers":{"X-Team":"keep"}}}}}`)
	if err = os.WriteFile(path, raw, 0640); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		cfg.Global.Port++
		if _, err = m.Apply(ProductOpenCode, cfg); err != nil {
			t.Fatal(err)
		}
	}
	doc, err := readJSONMap(path)
	if err != nil {
		t.Fatal(err)
	}
	providers, ok := doc["provider"].(map[string]any)
	if !ok {
		t.Fatal("missing providers")
	}
	managed, ok := providers["clipal"].(map[string]any)
	if !ok {
		t.Fatal("missing provider")
	}
	if managed["name"] != "User gateway" || managed["npm"] != "@ai-sdk/openai" {
		t.Fatal("replaced provider metadata")
	}
	models, ok := managed["models"].(map[string]any)
	if !ok {
		t.Fatal("missing models")
	}
	model, ok := models["alias"].(map[string]any)
	if !ok {
		t.Fatal("missing model")
	}
	if model["id"] != "wire-model" || model["name"] != "User model" || model["limit"] == nil {
		t.Fatalf("lost wire metadata: %#v", model)
	}
	if _, err = m.Rollback(ProductOpenCode, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(raw) {
		t.Fatal("did not restore original")
	}
}

func TestOpenCodeExplicitSelectionOverridesWireAlias(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			m := Manager{homeDir: t.TempDir(), configDir: t.TempDir()}
			cfg := &config.Config{Global: config.DefaultGlobalConfig()}
			if explicit {
				if err := m.SetTarget(ProductOpenCode, TargetOptions{Model: "selected-id"}); err != nil {
					t.Fatal(err)
				}
			}
			path, err := m.opencodeTargetPath()
			if err != nil {
				t.Fatal(err)
			}
			if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			raw := []byte(`{"model":"clipal/selected-id","provider":{"clipal":{"models":{"selected-id":{"id":"old-wire-id","name":"User alias","limit":{"context":32000}}}}}}`)
			if err = os.WriteFile(path, raw, 0640); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				cfg.Global.Port++
				if _, err = m.Apply(ProductOpenCode, cfg); err != nil {
					t.Fatal(err)
				}
			}
			doc, err := readJSONMap(path)
			if err != nil {
				t.Fatal(err)
			}
			models, ok := openCodeProvider(doc)["models"].(map[string]any)
			if !ok {
				t.Fatal("missing models")
			}
			model, ok := models["selected-id"].(map[string]any)
			if !ok {
				t.Fatal("missing alias")
			}
			want := "old-wire-id"
			if explicit {
				want = "selected-id"
			}
			if model["id"] != want || model["name"] != "User alias" || model["limit"] == nil {
				t.Fatalf("incorrect alias or lost metadata: %#v", model)
			}
			if _, err = m.Rollback(ProductOpenCode, cfg); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != string(raw) {
				t.Fatal("rollback changed original alias")
			}
		})
	}
}

func TestOpenCodeMergedRestrictionsAndOverrides(t *testing.T) {
	cases := []struct {
		name, lower, upper, inline string
		blocked                    bool
	}{
		{"disabled in JSON", `{"disabled_providers":["clipal"]}`, `{}`, "", true},
		{"allowlist in JSON", `{"enabled_providers":["other"]}`, `{}`, "", true},
		{"higher allowlist excludes", `{"enabled_providers":["clipal"]}`, `{"enabled_providers":["other"]}`, "", true},
		{"higher clears disabled", `{"disabled_providers":["clipal"]}`, `{"disabled_providers":[]}`, "", false},
		{"higher includes provider", `{"enabled_providers":["other"]}`, `{"enabled_providers":["clipal"]}`, "", false},
		{"inline blocks", `{}`, `{}`, `{"disabled_providers":["clipal"]}`, true},
		{"inline allows", `{"disabled_providers":["clipal"]}`, `{}`, `{"disabled_providers":[]}`, false},
		{"inline shadows model", `{}`, `{}`, `{"model":"other/model"}`, true},
		{"inline shadows wire ID", `{}`, `{}`, `{"provider":{"clipal":{"models":{"selected-id":{"id":"other-wire"}}}}}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OPENCODE_CONFIG_CONTENT", tc.inline)
			m := Manager{homeDir: t.TempDir(), configDir: t.TempDir()}
			cfg := &config.Config{Global: config.DefaultGlobalConfig()}
			if err := m.SetTarget(ProductOpenCode, TargetOptions{Model: "selected-id"}); err != nil {
				t.Fatal(err)
			}
			dir, err := m.clientConfigDir(ProductOpenCode)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			lower, upper := filepath.Join(dir, "opencode.json"), filepath.Join(dir, "opencode.jsonc")
			for path, raw := range map[string]string{lower: tc.lower, upper: tc.upper} {
				if err = os.WriteFile(path, []byte(raw), 0640); err != nil {
					t.Fatal(err)
				}
			}
			status, err := m.Status(ProductOpenCode, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if (status.Verification == "blocked") != tc.blocked {
				t.Fatalf("incorrect merged restriction: %+v", status)
			}
			_, err = m.Apply(ProductOpenCode, cfg)
			if tc.blocked {
				if err == nil {
					t.Fatal("applied despite merged restriction")
				}
				got, e := os.ReadFile(upper)
				if e != nil || string(got) != tc.upper {
					t.Fatal("changed blocked file")
				}
				if m.hasLatestBackup(ProductOpenCode) {
					t.Fatal("blocked apply published baseline")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if _, err = m.Rollback(ProductOpenCode, cfg); err != nil {
					t.Fatal(err)
				}
				got, e := os.ReadFile(upper)
				if e != nil || string(got) != tc.upper {
					t.Fatal("rollback changed upper layer")
				}
			}
			got, e := os.ReadFile(lower)
			if e != nil || string(got) != tc.lower {
				t.Fatal("changed read-only lower layer")
			}
		})
	}
}

func TestOpenCodeInheritedModelsAndReadOnlyDependency(t *testing.T) {
	m := Manager{homeDir: t.TempDir(), configDir: t.TempDir()}
	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	dir, err := m.clientConfigDir(ProductOpenCode)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	lower, upper := filepath.Join(dir, "opencode.json"), filepath.Join(dir, "opencode.jsonc")
	raw := []byte(`{"model":"source/alias","small_model":"source/small","provider":{"source":{"models":{"alias":{"id":"wire-main","limit":{"context":32000}},"small":{"id":"wire-small"}}}},"instructions":["keep.md"]}`)
	if err = os.WriteFile(lower, raw, 0640); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(upper, []byte(`{"permission":{"*":"deny"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(ProductOpenCode, cfg); err != nil {
		t.Fatal(err)
	}
	doc, err := readJSONMap(upper)
	if err != nil {
		t.Fatal(err)
	}
	if doc["model"] != "clipal/alias" || doc["small_model"] != "clipal/small" || doc["instructions"] != nil {
		t.Fatalf("incorrect inherited selection or copied unrelated fields: %#v", doc)
	}
	if _, err = m.Rollback(ProductOpenCode, cfg); err != nil {
		t.Fatal(err)
	}
	// Detect a lower layer changing after the plan, before replacing the target.
	before, err := os.ReadFile(upper)
	if err != nil {
		t.Fatal(err)
	}
	m.beforeTakeoverWrite = func(_ int, _ string) error {
		return os.WriteFile(lower, []byte(`{"disabled_providers":["clipal"]}`), 0640)
	}
	if _, err = m.Apply(ProductOpenCode, cfg); err == nil {
		t.Fatal("ignored a changed read-only input")
	}
	after, e := os.ReadFile(upper)
	if e != nil || string(after) != string(before) {
		t.Fatal("changed target despite stale dependency")
	}
	if m.hasLatestBackup(ProductOpenCode) {
		t.Fatal("failed apply retained a new baseline")
	}
}

func TestOpenCodeGlobalCustomFileAndDirectoryOrder(t *testing.T) {
	for _, laterDirBlocks := range []bool{false, true} {
		t.Run(fmt.Sprint(laterDirBlocks), func(t *testing.T) {
			home := t.TempDir()
			global := filepath.Join(home, "xdg", "opencode")
			custom := filepath.Join(home, "selected.jsonc")
			dir := filepath.Join(home, "custom-dir")
			t.Setenv("XDG_CONFIG_HOME", filepath.Dir(global))
			t.Setenv("OPENCODE_CONFIG", "")
			t.Setenv("OPENCODE_CONFIG_DIR", dir)
			t.Setenv("OPENCODE_CONFIG_CONTENT", "")
			m := Manager{homeDir: home, configDir: t.TempDir()}
			cfg := &config.Config{Global: config.DefaultGlobalConfig()}
			// config_file takes precedence over the separately saved directory. The
			// launch environment's custom directory is still a later client layer.
			if err := m.SetTarget(ProductOpenCode, TargetOptions{ConfigFile: custom, ConfigDir: filepath.Join(home, "unused"), Model: "selected-id"}); err != nil {
				t.Fatal(err)
			}
			upper := `{}`
			if laterDirBlocks {
				upper = `{"disabled_providers":["clipal"]}`
			}
			files := map[string]string{
				filepath.Join(global, "config.json"): `{"disabled_providers":["clipal"]}`,
				custom:                               `{"disabled_providers":[]}`,
				filepath.Join(dir, "config.json"):    `{"enabled_providers":["unused-legacy-file"]}`,
				filepath.Join(dir, "opencode.json"):  upper,
			}
			for path, body := range files {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0640); err != nil {
					t.Fatal(err)
				}
			}
			status, err := m.Status(ProductOpenCode, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if (status.Verification == "blocked") != laterDirBlocks {
				t.Fatalf("incorrect layer order: %+v", status)
			}
			_, err = m.Apply(ProductOpenCode, cfg)
			if laterDirBlocks {
				if err == nil {
					t.Fatal("ignored later directory restriction")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if _, err = m.Rollback(ProductOpenCode, cfg); err != nil {
					t.Fatal(err)
				}
			}
			for path, body := range files {
				got, e := os.ReadFile(path)
				if e != nil || string(got) != body {
					t.Fatalf("modified source layer %s", path)
				}
			}
		})
	}
}
