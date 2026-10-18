package integration

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/PAIArtCom/Clipal/internal/config"
)

func TestCrushPlannerPreservesLegacyJSONMetadata(t *testing.T) {
	home := t.TempDir()
	m := Manager{homeDir: home, configDir: filepath.Join(home, "clipal")}
	dir := filepath.Join(home, "crush-user")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "crush.json")
	original := []byte(`{
		"options":{"debug":true},
		"models":{
			"large":{"provider":"other","model":"main","max_tokens":1234},
			"small":{"provider":"other","model":"helper","temperature":0.2}
		},
		"providers":{
			"other":{"api_key":"$OTHER_KEY","models":[{"id":"main","name":"Original","context_window":42000},{"id":"helper","name":"Helper"}]},
			"clipal":{"api_key":"$PRIVATE_KEY","oauth":{"access_token":"fixture"},"extra_headers":{"x-tenant":"keep"},"models":[{"id":"unused","name":"Keep"}]}
		}
	}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.SetTarget(ProductCrush, TargetOptions{ConfigDir: dir}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	files, err := m.planCrush(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].TargetPath != path || len(files[0].Dependencies) != 3 {
		t.Fatalf("unexpected plan: %#v", files)
	}
	doc, err := parseJSONCMap(files[0].Planned)
	if err != nil {
		t.Fatal(err)
	}
	providers := requireConfigValue[map[string]any](t, doc["providers"])
	provider := requireConfigValue[map[string]any](t, providers["clipal"])
	if provider["base_url"] != m.advertisedBaseURL(cfg) || provider["type"] != "openai-compat" || provider["api_key"] != "$PRIVATE_KEY" {
		t.Fatalf("provider routing = %#v", provider)
	}
	if requireConfigValue[map[string]any](t, provider["oauth"])["access_token"] != "fixture" || requireConfigValue[map[string]any](t, provider["extra_headers"])["x-tenant"] != "keep" {
		t.Fatal("auth or header metadata lost")
	}
	catalog := requireConfigValue[[]any](t, provider["models"])
	if len(catalog) != 3 || requireConfigValue[map[string]any](t, catalog[1])["context_window"] != float64(42000) {
		t.Fatalf("catalog metadata = %#v", catalog)
	}
	models := requireConfigValue[map[string]any](t, doc["models"])
	if requireConfigValue[map[string]any](t, models["large"])["provider"] != "clipal" || requireConfigValue[map[string]any](t, models["small"])["model"] != "helper" || requireConfigValue[map[string]any](t, models["large"])["max_tokens"] != float64(1234) {
		t.Fatalf("selected model metadata = %#v", models)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, original) {
		t.Fatalf("planner wrote config: %v", err)
	}
}

func TestCrushPlannerRejectsShellAndMachineRoutingOverrides(t *testing.T) {
	for _, name := range []string{"crushrc", ".crushrc", "machine-state"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			dir := filepath.Join(home, "user")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			path, original := filepath.Join(dir, name), "touch should-never-execute\n"
			if name == "machine-state" {
				dirState := filepath.Join(home, "state")
				t.Setenv("CRUSH_GLOBAL_DATA", dirState)
				if err := os.MkdirAll(dirState, 0o700); err != nil {
					t.Fatal(err)
				}
				path, original = filepath.Join(dirState, "crush.json"), `{"models":{"large":{"provider":"other","model":"override"}}}`
			}
			if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			m := Manager{homeDir: home, configDir: filepath.Join(home, "clipal")}
			if err := m.SetTarget(ProductCrush, TargetOptions{ConfigDir: dir, Model: "selected"}); err != nil {
				t.Fatal(err)
			}
			if _, err := m.planCrush(&config.Config{Global: config.DefaultGlobalConfig()}); err == nil {
				t.Fatal("unsafe or shadowed configuration accepted")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != original {
				t.Fatalf("dependency changed: %v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, "crush.json")); !os.IsNotExist(err) {
				t.Fatalf("planner created config: %v", err)
			}
		})
	}
}

func TestCrushPlannerRejectsMalformedJSONAndRouting(t *testing.T) {
	for _, original := range []string{`{`, `null`, `[]`, `{"providers":[]}`, `{"models":[]}`, `{"models":{"large":[]}}`, `{"providers":{"clipal":[]}}`, `{"providers":{"clipal":{"models":{}}}}`, `{"providers":{"clipal":{"models":[1]}}}`} {
		t.Run(original, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, "crush.json")
			if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			m := Manager{homeDir: home, configDir: filepath.Join(home, "clipal")}
			if err := m.SetTarget(ProductCrush, TargetOptions{ConfigDir: home, Model: "selected"}); err != nil {
				t.Fatal(err)
			}
			if _, err := m.planCrush(&config.Config{Global: config.DefaultGlobalConfig()}); err == nil {
				t.Fatal("malformed configuration accepted")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != original {
				t.Fatalf("config changed on error: %v", err)
			}
		})
	}
}

func TestCrushPlannerFreshConfigUsesSelectedModel(t *testing.T) {
	home := t.TempDir()
	m := Manager{homeDir: home, configDir: filepath.Join(home, "clipal")}
	if err := m.SetTarget(ProductCrush, TargetOptions{ConfigDir: home, Model: "gateway/model"}); err != nil {
		t.Fatal(err)
	}
	files, err := m.planCrush(&config.Config{Global: config.DefaultGlobalConfig()})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Existed {
		t.Fatalf("unexpected plan: %#v", files)
	}
	doc, err := parseJSONCMap(files[0].Planned)
	if err != nil {
		t.Fatal(err)
	}
	models := requireConfigValue[map[string]any](t, doc["models"])
	for _, slot := range []string{"large", "small"} {
		selected := requireConfigValue[map[string]any](t, models[slot])
		if selected["model"] != "gateway/model" || selected["provider"] != "clipal" {
			t.Fatalf("%s = %#v", slot, selected)
		}
	}
	provider := requireConfigValue[map[string]any](t, requireConfigValue[map[string]any](t, doc["providers"])["clipal"])
	catalog := requireConfigValue[[]any](t, provider["models"])
	if len(catalog) != 1 || requireConfigValue[map[string]any](t, catalog[0])["id"] != "gateway/model" || provider["api_key"] != "clipal" {
		t.Fatalf("provider = %#v", provider)
	}
}
