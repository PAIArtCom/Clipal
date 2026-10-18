package integration

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PAIArtCom/Clipal/internal/config"
	toml "github.com/pelletier/go-toml/v2"
)

func kimiGrokFixture(t *testing.T, product ProductID, body, model string, context int64) (Manager, string) {
	t.Helper()
	root := t.TempDir()
	m := Manager{configDir: filepath.Join(root, "clipal"), homeDir: root}
	dir := filepath.Join(root, "client")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := m.SetTarget(product, TargetOptions{ConfigDir: dir, Model: model, ContextWindow: context}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	if body != "" {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return m, path
}

func TestKimiPlannerPreservesSourceAndUsesConfiguredMetadata(t *testing.T) {
	original := "default_model = 'personal'\ncustom = 'keep'\n[providers.personal]\ntype = 'kimi'\napi_key = 'fixture-key'\n[models.personal]\nprovider = 'personal'\nmodel = 'dynamic-model'\nmax_context_size = 32768\ndisplay_name = 'Personal'\n"
	m, path := kimiGrokFixture(t, ProductKimiCLI, original, "", 0)
	files, err := m.planKimi(&config.Config{Global: config.DefaultGlobalConfig()})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].TargetPath != path || files[0].Format != managedTOML {
		t.Fatalf("files: %#v", files)
	}
	var doc map[string]any
	if err := toml.Unmarshal(files[0].Planned, &doc); err != nil {
		t.Fatal(err)
	}
	models := requireConfigValue[map[string]any](t, doc["models"])
	managed := requireConfigValue[map[string]any](t, models["clipal"])
	if managed["model"] != "dynamic-model" || managed["max_context_size"] != int64(32768) || managed["protocol"] != "openai" {
		t.Fatalf("managed: %#v", managed)
	}
	provider := requireConfigValue[map[string]any](t, requireConfigValue[map[string]any](t, doc["providers"])["personal"])
	if provider["api_key"] != "fixture-key" || doc["custom"] != "keep" || requireConfigValue[map[string]any](t, models["personal"])["provider"] != "personal" {
		t.Fatalf("lost data: %#v", doc)
	}
	actual, _ := os.ReadFile(path)
	if string(actual) != original {
		t.Fatal("planner wrote client file")
	}
}

func TestKimiPlannerFreshModelRequiresContext(t *testing.T) {
	for _, context := range []int64{0, 32768} {
		t.Run(map[int64]string{0: "missing", 32768: "provided"}[context], func(t *testing.T) {
			m, path := kimiGrokFixture(t, ProductKimiCLI, "", "gateway-model", context)
			files, err := m.planKimi(&config.Config{Global: config.DefaultGlobalConfig()})
			if context == 0 {
				if err == nil || !strings.Contains(err.Error(), "context") {
					t.Fatalf("error: %v", err)
				}
			} else if err != nil || len(files) != 1 {
				t.Fatalf("plan: %v, %v", files, err)
			}
			if context > 0 {
				var doc map[string]any
				if err := toml.Unmarshal(files[0].Planned, &doc); err != nil {
					t.Fatal(err)
				}
				if doc["default_model"] != "clipal" || requireConfigValue[map[string]any](t, requireConfigValue[map[string]any](t, doc["models"])["clipal"])["model"] != "gateway-model" || requireConfigValue[map[string]any](t, requireConfigValue[map[string]any](t, doc["providers"])["clipal"])["type"] != "openai" {
					t.Fatalf("missing routing: %#v", doc)
				}
			}

			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("planner created client config")
			}
		})
	}
}

func TestKimiPlannerRejectsMalformedConfigWithoutWriting(t *testing.T) {
	for _, original := range []string{"invalid = [", "models = 7", "providers = 'bad'", "default_model = 7", "default_model='bad'\n[models]\nbad=1", "default_model='bad'\n[models.bad]\nmodel='wire'\nmax_context_size=0"} {
		t.Run(original, func(t *testing.T) {
			m, path := kimiGrokFixture(t, ProductKimiCLI, original, "", 0)
			if files, err := m.planKimi(&config.Config{Global: config.DefaultGlobalConfig()}); err == nil || len(files) != 0 {
				t.Fatalf("expected error, got %v %v", files, err)
			}
			actual, _ := os.ReadFile(path)
			if !bytes.Equal(actual, []byte(original)) {
				t.Fatal("modified malformed file")
			}
		})
	}
}

func TestKimiPlannerExplicitContextOverridesManagedContext(t *testing.T) {
	original := "default_model='personal'\n[models.personal]\nmodel='dynamic-model'\nmax_context_size=32768\n[models.personal.overrides]\nmax_context_size=16384\ndisplay_name='Personal override'\n[models.clipal]\nmodel='old-model'\nmax_context_size=8192\n[models.clipal.overrides]\nmax_context_size=4096\ndisplay_name='Managed override'\n"
	m, _ := kimiGrokFixture(t, ProductKimiCLI, original, "personal", 65536)
	files, err := m.planKimi(&config.Config{Global: config.DefaultGlobalConfig()})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := toml.Unmarshal(files[0].Planned, &doc); err != nil {
		t.Fatal(err)
	}
	models := requireConfigValue[map[string]any](t, doc["models"])
	managed := requireConfigValue[map[string]any](t, models["clipal"])
	overrides := requireConfigValue[map[string]any](t, managed["overrides"])
	if managed["max_context_size"] != int64(65536) || overrides["max_context_size"] != int64(65536) || overrides["display_name"] != "Managed override" {
		t.Fatalf("managed: %#v", managed)
	}
	if requireConfigValue[map[string]any](t, requireConfigValue[map[string]any](t, models["personal"])["overrides"])["max_context_size"] != int64(16384) {
		t.Fatal("changed source override")
	}
}

func TestKimiPlannerRejectsMalformedCredentialScalars(t *testing.T) {
	for _, section := range []string{"providers.clipal", "models.clipal"} {
		for _, field := range []string{"api_key", "base_url"} {
			t.Run(section+"/"+field, func(t *testing.T) {
				original := "[" + section + "]\n" + field + "=7\n"
				m, path := kimiGrokFixture(t, ProductKimiCLI, original, "selected-model", 32768)
				if files, err := m.planKimi(&config.Config{Global: config.DefaultGlobalConfig()}); err == nil || len(files) != 0 {
					t.Fatalf("expected no-write failure: %v %v", files, err)
				}
				actual, _ := os.ReadFile(path)
				if string(actual) != original {
					t.Fatal("modified malformed config")
				}
			})
		}
	}
	for _, field := range []string{"api_key_env", "type"} {
		original := "[providers.clipal]\n" + field + "=7\n"
		m, path := kimiGrokFixture(t, ProductKimiCLI, original, "selected-model", 32768)
		if files, err := m.planKimi(&config.Config{Global: config.DefaultGlobalConfig()}); err == nil || len(files) != 0 {
			t.Fatalf("expected no-write failure: %v %v", files, err)
		}
		actual, _ := os.ReadFile(path)
		if string(actual) != original {
			t.Fatal("modified malformed config")
		}
	}
}
