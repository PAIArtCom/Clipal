package integration

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/PAIArtCom/Clipal/internal/config"
)

func TestKiloPlannerPreservesProviderMetadataAndDoesNotWrite(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "selected")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "kilo.jsonc")
	original := []byte(`{
		// JSONC is the current CLI's documented format.
		"theme": "keep",
		"model": "original/gateway-id",
		"provider": {
			"original": {"models": {"gateway-id": {"name": "Existing", "limit": {"context": 42000}}}},
			"clipal": {
				"options": {"apiKey": "{env:PRIVATE_TOKEN}", "headers": {"x-tenant": "keep"}},
				"custom": "retain"
			}
		}
	}`)
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	m := Manager{homeDir: home, configDir: filepath.Join(home, "clipal")}
	if err := m.SetTarget(ProductKiloCLI, TargetOptions{ConfigDir: dir}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	files, err := m.planKilo(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].TargetPath != path {
		t.Fatalf("unexpected plan: %#v", files)
	}
	doc, err := parseJSONCMap(files[0].Planned)
	if err != nil {
		t.Fatal(err)
	}
	if doc["model"] != "clipal/gateway-id" || doc["theme"] != "keep" {
		t.Fatalf("routing or unrelated setting changed incorrectly: %#v", doc)
	}
	provider := openCodeProvider(doc)
	options := requireConfigValue[map[string]any](t, provider["options"])
	if options["baseURL"] != m.advertisedBaseURL(cfg) || options["apiKey"] != "{env:PRIVATE_TOKEN}" || provider["custom"] != "retain" {
		t.Fatalf("provider metadata = %#v", provider)
	}
	if requireConfigValue[map[string]any](t, options["headers"])["x-tenant"] != "keep" {
		t.Fatal("provider headers were lost")
	}
	model := requireConfigValue[map[string]any](t, requireConfigValue[map[string]any](t, provider["models"])["gateway-id"])
	if model["name"] != "Existing" || requireConfigValue[map[string]any](t, model["limit"])["context"] != float64(42000) {
		t.Fatalf("model metadata = %#v", model)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatalf("planner wrote to config: %v", err)
	}
}

func TestKiloPlannerRejectsMalformedRoutingWithoutWrites(t *testing.T) {
	for _, original := range []string{`{`, `null`, `[]`, `{"provider":[]}`, `{"provider":{"clipal":[]}}`, `{"provider":{"clipal":{"options":[]}}}`, `{"provider":{"clipal":{"models":[]}}}`} {
		t.Run(original, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, "kilo.json")
			if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			m := Manager{homeDir: home, configDir: filepath.Join(home, "clipal")}
			if err := m.SetTarget(ProductKiloCLI, TargetOptions{ConfigFile: path, Model: "selected"}); err != nil {
				t.Fatal(err)
			}
			if _, err := m.planKilo(&config.Config{Global: config.DefaultGlobalConfig()}); err == nil {
				t.Fatal("malformed configuration accepted")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != original {
				t.Fatalf("config changed on error: %v", err)
			}
		})
	}
}

func TestKiloPlannerRequiresSelectedOrExistingModel(t *testing.T) {
	home := t.TempDir()
	m := Manager{homeDir: home, configDir: filepath.Join(home, "clipal")}
	if _, err := m.planKilo(&config.Config{Global: config.DefaultGlobalConfig()}); err == nil {
		t.Fatal("planner invented a model")
	}
}
