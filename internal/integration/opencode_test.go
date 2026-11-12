package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PAIArtCom/Clipal/internal/config"
)

func openCodeTestConfig() *config.Config {
	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	cfg.Global.Port = 4455
	cfg.OpenAI.Providers = []config.Provider{{Name: "fixture-openai", BaseURL: "https://openai.example/v1", APIKey: "fixture-key"}}
	cfg.Claude.Providers = []config.Provider{{Name: "fixture-anthropic", BaseURL: "https://anthropic.example", APIKey: "fixture-key"}}
	disabled := false
	cfg.Google.Providers = []config.Provider{{Name: "fixture-google", BaseURL: "https://google.example", APIKey: "fixture-key", Enabled: &disabled}}
	return cfg
}

func TestOpenCodeWildcardMatchMatchesOpenCodeSemantics(t *testing.T) {
	tests := []struct {
		value, pattern string
		want           bool
	}{
		{value: "openai", pattern: "openai", want: true},
		{value: "company-us", pattern: "company-*", want: true},
		{value: "company-us", pattern: "company-?s", want: true},
		{value: "provider/v2", pattern: "provider*", want: true},
		{value: "openai", pattern: "*ai", want: true},
		{value: "openai", pattern: "anthropic", want: false},
		{value: "ls", pattern: "ls *", want: true},
		{value: "ls -la", pattern: "ls *", want: true},
		{value: "ls", pattern: "ls ?", want: false},
	}
	for _, test := range tests {
		t.Run(test.value+"/"+test.pattern, func(t *testing.T) {
			if got := openCodeWildcardMatch(test.value, test.pattern); got != test.want {
				t.Fatalf("openCodeWildcardMatch(%q, %q) = %t, want %t", test.value, test.pattern, got, test.want)
			}
		})
	}
}

func TestOpenCodeStatus_NotConfiguredWhenFileMissing(t *testing.T) {
	home := t.TempDir()
	m := Manager{configDir: filepath.Join(home, ".clipal"), homeDir: home}
	status, err := m.Status(ProductOpenCode, openCodeTestConfig())
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

func TestOpenCodeApply_RoutesEnabledProtocolsAndPreservesModelSelection(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".config", "opencode", "opencode.json")
	original := `{
  "$schema": "https://opencode.ai/config.json",
  "theme": "opencode",
  "model": "openai/custom-model",
  "small_model": "anthropic/haiku",
  "providers": {
    "openai": {
      "name": "My OpenAI",
      "settings": {"baseURL": "https://old-openai.example/v1", "apiKey": "old-key", "organization": "team"},
      "models": {"custom-model": {"name": "Custom Model", "limit": {"context": 32000}}}
    },
    "google": {"settings": {"baseURL": "https://keep-google.example", "apiKey": "keep-key"}}
  }
}
`
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	m := Manager{configDir: filepath.Join(home, ".clipal"), homeDir: home}
	cfg := openCodeTestConfig()
	if _, err := m.Apply(ProductOpenCode, cfg); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	doc, err := readJSONMap(path)
	if err != nil {
		t.Fatal(err)
	}
	if doc["model"] != "openai/custom-model" || doc["small_model"] != "anthropic/haiku" || doc["theme"] != "opencode" {
		t.Fatalf("changed OpenCode model selection or unrelated setting: %#v", doc)
	}
	providers := requireConfigValue[map[string]any](t, doc["providers"])
	openai := requireConfigValue[map[string]any](t, providers["openai"])
	openaiSettings := requireConfigValue[map[string]any](t, openai["settings"])
	if openaiSettings["baseURL"] != "http://127.0.0.1:4455/clipal/v1" || openaiSettings["apiKey"] != "clipal" || openaiSettings["organization"] != "team" {
		t.Fatalf("OpenAI route settings = %#v", openaiSettings)
	}
	if openai["name"] != "My OpenAI" || requireConfigValue[map[string]any](t, openai["models"])["custom-model"] == nil {
		t.Fatalf("lost OpenAI catalog metadata: %#v", openai)
	}
	anthropic := requireConfigValue[map[string]any](t, providers["anthropic"])
	anthropicSettings := requireConfigValue[map[string]any](t, anthropic["settings"])
	if anthropicSettings["baseURL"] != "http://127.0.0.1:4455/clipal" || anthropicSettings["apiKey"] != "clipal" {
		t.Fatalf("Anthropic route settings = %#v", anthropicSettings)
	}
	google := requireConfigValue[map[string]any](t, providers["google"])
	googleSettings := requireConfigValue[map[string]any](t, google["settings"])
	if googleSettings["baseURL"] != "https://keep-google.example" || googleSettings["apiKey"] != "keep-key" {
		t.Fatalf("changed inactive Google provider: %#v", googleSettings)
	}
	if _, err := m.Rollback(ProductOpenCode, cfg); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != original {
		t.Fatalf("rollback body = %q, err = %v", got, err)
	}
}

func TestOpenCodeApply_CreatesJSONCWithoutChangingGlobalModel(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "opencode.jsonc")
	original := strings.TrimSpace(`{
  "$schema": "https://opencode.ai/config.json",
  // Preserve comments and the session-level selection.
  "model": "anthropic/claude-sonnet-4-5"
}`) + "\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	m := Manager{configDir: filepath.Join(home, ".clipal"), homeDir: home}
	cfg := openCodeTestConfig()
	if _, err := m.Apply(ProductOpenCode, cfg); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("Unmarshal: %v\nbody=%s", err, raw)
	}
	if doc["model"] != "anthropic/claude-sonnet-4-5" {
		t.Fatalf("changed model selection: %s", raw)
	}
	providers := requireConfigValue[map[string]any](t, doc["providers"])
	if providers["openai"] == nil || providers["anthropic"] == nil || providers["google"] != nil {
		t.Fatalf("routed protocols = %#v", providers)
	}
}

func TestOpenCodeApply_RejectsWhenNoClipalProtocolIsEnabled(t *testing.T) {
	home := t.TempDir()
	m := Manager{configDir: filepath.Join(home, ".clipal"), homeDir: home}
	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	status, err := m.Status(ProductOpenCode, cfg)
	if err != nil || status.Verification != "blocked" {
		t.Fatalf("status = %+v, err = %v", status, err)
	}
	if _, err := m.Apply(ProductOpenCode, cfg); err == nil {
		t.Fatal("applied without an enabled Clipal protocol provider")
	}
	if _, err := os.Stat(status.TargetPath); !os.IsNotExist(err) {
		t.Fatalf("created target for blocked apply: %v", err)
	}
}

func TestOpenCodePolicyRestrictionsFollowV2Order(t *testing.T) {
	t.Setenv("OPENCODE_CONFIG_CONTENT", "")
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	lower := filepath.Join(dir, "opencode.json")
	upper := filepath.Join(dir, "opencode.jsonc")
	if err := os.WriteFile(lower, []byte(`{"policies":[{"action":"provider.use","resource":"openai","effect":"deny"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(upper, []byte(`{"policies":[{"action":"provider.use","resource":"openai","effect":"allow"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	m := Manager{configDir: filepath.Join(home, ".clipal"), homeDir: home}
	cfg := openCodeTestConfig()
	status, err := m.Status(ProductOpenCode, cfg)
	if err != nil || status.Verification != "blocked" {
		t.Fatalf("global deny should outrank project config: status=%+v err=%v", status, err)
	}
	if _, err := m.Apply(ProductOpenCode, cfg); err == nil {
		t.Fatal("applied while OpenCode policy denies the provider")
	}
	got, err := os.ReadFile(upper)
	if err != nil || string(got) != `{"policies":[{"action":"provider.use","resource":"openai","effect":"allow"}]}` {
		t.Fatalf("changed policy layer: %s, err=%v", got, err)
	}
}

func TestOpenCodeRouteShadowBlocksLaterLayer(t *testing.T) {
	home := t.TempDir()
	customDir := filepath.Join(home, "project-config")
	t.Setenv("OPENCODE_CONFIG_DIR", customDir)
	t.Setenv("OPENCODE_CONFIG_CONTENT", "")
	if err := os.MkdirAll(customDir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENCODE_CONFIG_CONTENT", `{"providers":{"openai":{"settings":{"baseURL":"http://other.example/v1"}}}}`)
	m := Manager{configDir: filepath.Join(home, ".clipal"), homeDir: home}
	cfg := openCodeTestConfig()
	status, err := m.Status(ProductOpenCode, cfg)
	if err != nil || status.Verification != "blocked" {
		t.Fatalf("status = %+v, err = %v", status, err)
	}
	if _, err := m.Apply(ProductOpenCode, cfg); err == nil {
		t.Fatal("applied despite a later endpoint override")
	}
}
