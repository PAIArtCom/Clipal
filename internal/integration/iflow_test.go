package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/PAIArtCom/Clipal/internal/config"
)

// Shared by these three client planners: fixtures never touch real client homes.
func harnessClientFixture(t *testing.T, product ProductID, filename, original, model string) (Manager, string) {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, "client")
	path := filepath.Join(dir, filename)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if original != "" {
		if err := os.WriteFile(path, []byte(original), 0600); err != nil {
			t.Fatal(err)
		}
	}
	m := Manager{homeDir: home, configDir: filepath.Join(home, "clipal")}
	target := TargetOptions{ConfigDir: dir, Model: model}
	if err := m.SetTarget(product, target); err != nil {
		t.Fatal(err)
	}
	return m, path
}
func TestIFlowPlannerPreservesAuthenticationAndUnknownConfig(t *testing.T) {
	original := `{"modelName":"vendor/model","apiKey":"saved-key","selectedAuthType":"iflow","theme":"keep","oauth":{"token":"retain"}}`
	m, path := harnessClientFixture(t, ProductIFlow, "settings.json", original, "")
	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	files, err := m.planIFlow(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err = json.Unmarshal(files[0].Planned, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["modelName"] != "vendor/model" || doc["apiKey"] != "saved-key" || doc["theme"] != "keep" || doc["selectedAuthType"] != "openai-compatible" || doc["baseUrl"] != m.advertisedBaseURL(cfg) || requireConfigValue[map[string]any](t, doc["oauth"])["token"] != "retain" {
		t.Fatalf("configuration=%#v", doc)
	}
	actual, _ := os.ReadFile(path)
	if string(actual) != original {
		t.Fatal("planner wrote configuration")
	}
}
func TestIFlowPlannerRequiresModelAndRejectsMalformed(t *testing.T) {
	for _, original := range []string{`{`, `null`, `[]`, `{}`, `{"apiKey":7,"modelName":"selected"}`, `{"modelName":7}`} {
		t.Run(original, func(t *testing.T) {
			m, path := harnessClientFixture(t, ProductIFlow, "settings.json", original, "")
			if _, err := m.planIFlow(&config.Config{Global: config.DefaultGlobalConfig()}); err == nil {
				t.Fatal("expected error")
			}
			actual, _ := os.ReadFile(path)
			if string(actual) != original {
				t.Fatal("modified configuration")
			}
		})
	}
}
