package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/PAIArtCom/Clipal/internal/config"
)

func piQwenPlannerFixture(t *testing.T, product ProductID, model string) (Manager, string) {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, "client")
	m := Manager{configDir: filepath.Join(home, "clipal"), homeDir: home}
	if err := m.SetTarget(product, TargetOptions{ConfigDir: dir, Model: model}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return m, dir
}

func TestPiPlannerPreservesCredentialsAndModelMetadata(t *testing.T) {
	m, dir := piQwenPlannerFixture(t, ProductPi, "")
	original := []byte(`{"custom":true,"providers":{"other":{"apiKey":"fixture-other"},"clipal":{"apiKey":"$FIXTURE_KEY","headers":{"x-custom":"keep"},"models":[{"id":"gateway-dynamic","name":"Personal","baseUrl":"https://old.invalid","api":"anthropic-messages"},{"id":"keep-model"}]}}}`)
	path := filepath.Join(dir, "models.json")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"defaultModel":"gateway-dynamic","theme":"keep"}`), 0600); err != nil {
		t.Fatal(err)
	}
	authPath := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"untouched":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	files, err := m.planPi(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("files: %d", len(files))
	}
	var doc map[string]any
	if err = json.Unmarshal(files[0].Planned, &doc); err != nil {
		t.Fatal(err)
	}
	providers := requireConfigValue[map[string]any](t, doc["providers"])
	provider := requireConfigValue[map[string]any](t, providers["clipal"])
	models := requireConfigValue[[]any](t, provider["models"])
	selected := requireConfigValue[map[string]any](t, models[0])
	if provider["apiKey"] != "$FIXTURE_KEY" || selected["name"] != "Personal" || selected["baseUrl"] != m.advertisedBaseURL(cfg) || selected["api"] != "openai-completions" || len(models) != 2 || doc["custom"] != true {
		t.Fatalf("planned: %s", files[0].Planned)
	}
	var settings map[string]any
	if err = json.Unmarshal(files[1].Planned, &settings); err != nil {
		t.Fatal(err)
	}
	if settings["defaultProvider"] != "clipal" || settings["defaultModel"] != "gateway-dynamic" || settings["theme"] != "keep" {
		t.Fatalf("settings: %s", files[1].Planned)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, original) {
		t.Fatal("planner wrote configuration")
	}
	auth, _ := os.ReadFile(authPath)
	if string(auth) != `{"untouched":true}` {
		t.Fatal("auth changed")
	}
}

func TestPiPlannerRejectsMalformedSiblingBeforeWriting(t *testing.T) {
	for _, body := range []string{`null`, `[]`, `{"providers":[]}`, `{"providers":{"clipal":{"models":{}}}}`, `{"providers":{"clipal":{"models":[false]}}}`} {
		t.Run(body, func(t *testing.T) {
			m, dir := piQwenPlannerFixture(t, ProductPi, "dynamic")
			path := filepath.Join(dir, "models.json")
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			if files, err := m.planPi(&config.Config{Global: config.DefaultGlobalConfig()}); err == nil {
				t.Fatalf("accepted malformed input: %#v", files)
			}
			after, _ := os.ReadFile(path)
			if string(after) != body {
				t.Fatal("changed malformed input")
			}
			if _, err := os.Stat(filepath.Join(dir, "settings.json")); !os.IsNotExist(err) {
				t.Fatal("created sibling")
			}
		})
	}
}

func TestPiPlannerFreshSelectionCreatesBothPlannedDocuments(t *testing.T) {
	m, dir := piQwenPlannerFixture(t, ProductPi, "opaque/gateway-model")
	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	files, err := m.planPi(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("files: %d", len(files))
	}
	var models, settings map[string]any
	if err = json.Unmarshal(files[0].Planned, &models); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(files[1].Planned, &settings); err != nil {
		t.Fatal(err)
	}
	providers, ok := models["providers"].(map[string]any)
	if !ok {
		t.Fatalf("missing providers: %s", files[0].Planned)
	}
	provider, ok := providers["clipal"].(map[string]any)
	if !ok {
		t.Fatalf("missing Clipal provider: %s", files[0].Planned)
	}
	entries, ok := provider["models"].([]any)
	if !ok || len(entries) != 1 {
		t.Fatalf("missing models: %s", files[0].Planned)
	}
	selected := requireConfigValue[map[string]any](t, entries[0])
	if provider["baseUrl"] != m.advertisedBaseURL(cfg) || provider["apiKey"] != "clipal" || selected["id"] != "opaque/gateway-model" {
		t.Fatalf("provider: %#v", provider)
	}
	if settings["defaultProvider"] != "clipal" || settings["defaultModel"] != "opaque/gateway-model" {
		t.Fatalf("settings: %#v", settings)
	}
	for _, name := range []string{"models.json", "settings.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("planner created %s", name)
		}
	}
}

func assertPiQwenPlannerRejectsScalar(t *testing.T, product ProductID, filename string, doc map[string]any) {
	t.Helper()
	m, dir := piQwenPlannerFixture(t, product, "explicit-model")
	path := filepath.Join(dir, filename)
	original, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	if product == ProductPi {
		_, err = m.planPi(cfg)
	} else {
		_, err = m.planQwen(cfg)
	}
	if err == nil {
		t.Fatalf("accepted malformed scalar: %s", original)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatalf("malformed file changed: %v", err)
	}
}

func TestPiPlannerRejectsMalformedRoutingScalarsWithExplicitSelection(t *testing.T) {
	for _, key := range []string{"defaultModel", "defaultProvider"} {
		t.Run(key, func(t *testing.T) {
			assertPiQwenPlannerRejectsScalar(t, ProductPi, "settings.json", map[string]any{key: false})
		})
	}
	for _, key := range []string{"id", "baseUrl", "api"} {
		t.Run("model-"+key, func(t *testing.T) {
			entry := map[string]any{"id": "explicit-model", key: 42}
			assertPiQwenPlannerRejectsScalar(t, ProductPi, "models.json", map[string]any{"providers": map[string]any{"clipal": map[string]any{"models": []any{entry}}}})
		})
	}
	for _, key := range []string{"baseUrl", "api", "apiKey"} {
		t.Run("provider-"+key, func(t *testing.T) {
			assertPiQwenPlannerRejectsScalar(t, ProductPi, "models.json", map[string]any{"providers": map[string]any{"clipal": map[string]any{key: false}}})
		})
	}
}
