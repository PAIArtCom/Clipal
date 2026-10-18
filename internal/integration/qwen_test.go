package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/PAIArtCom/Clipal/internal/config"
)

func TestQwenPlannerPreservesCatalogAndAuth(t *testing.T) {
	m, dir := piQwenPlannerFixture(t, ProductQwenCode, "")
	path := filepath.Join(dir, "settings.json")
	original := []byte(`{// comment
 "model":{"name":"gateway-dynamic","custom":true},"security":{"auth":{"selectedType":"qwen-oauth","apiKey":"fixture-key","custom":"keep"}},"providerProtocol":{"personal":"anthropic"},"modelProviders":{"personal":[{"id":"keep","envKey":"PERSONAL_KEY","metadata":{"custom":true}}],"clipal":[{"id":"gateway-dynamic","envKey":"FIXTURE_KEY","name":"Personal","generationConfig":{"temperature":0.7}},{"id":"other"}]},"env":{"FIXTURE_KEY":"fixture-value"},"custom":true}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	files, err := m.planQwen(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("files: %d", len(files))
	}
	var doc map[string]any
	if err = json.Unmarshal(files[0].Planned, &doc); err != nil {
		t.Fatal(err)
	}
	auth := requireConfigValue[map[string]any](t, requireConfigValue[map[string]any](t, doc["security"])["auth"])
	providers := requireConfigValue[map[string]any](t, doc["modelProviders"])
	entries := requireConfigValue[[]any](t, providers["clipal"])
	entry := requireConfigValue[map[string]any](t, entries[0])
	if auth["apiKey"] != "fixture-key" || auth["selectedType"] != "openai" || auth["custom"] != "keep" || len(entries) != 2 || entry["envKey"] != "FIXTURE_KEY" || entry["name"] != "Personal" || entry["baseUrl"] != m.advertisedBaseURL(cfg) || providers["personal"] == nil || doc["custom"] != true {
		t.Fatalf("planned: %s", files[0].Planned)
	}
	model := requireConfigValue[map[string]any](t, doc["model"])
	if model["baseUrl"] != m.advertisedBaseURL(cfg) || model["custom"] != true {
		t.Fatalf("selection: %#v", model)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, original) {
		t.Fatal("planner wrote configuration")
	}
}

func TestQwenPlannerRejectsMalformedProviderContainers(t *testing.T) {
	for _, body := range []string{`null`, `[]`, `{"modelProviders":[]}`, `{"modelProviders":{"openai":{"models":[]}}}`, `{"modelProviders":{"clipal":[false]}}`, `{"security":{"auth":[]}}`, `{"env":[]}`} {
		t.Run(body, func(t *testing.T) {
			m, dir := piQwenPlannerFixture(t, ProductQwenCode, "dynamic")
			path := filepath.Join(dir, "settings.json")
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			if files, err := m.planQwen(&config.Config{Global: config.DefaultGlobalConfig()}); err == nil {
				t.Fatalf("accepted malformed input: %#v", files)
			}
			after, _ := os.ReadFile(path)
			if string(after) != body {
				t.Fatal("changed malformed input")
			}
		})
	}
}

func TestQwenPlannerFreshSelectionUsesDedicatedCredentialReference(t *testing.T) {
	m, dir := piQwenPlannerFixture(t, ProductQwenCode, "opaque/gateway-model")
	files, err := m.planQwen(&config.Config{Global: config.DefaultGlobalConfig()})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err = json.Unmarshal(files[0].Planned, &doc); err != nil {
		t.Fatal(err)
	}
	providers := requireConfigValue[map[string]any](t, doc["modelProviders"])
	selected := requireConfigValue[map[string]any](t, requireConfigValue[[]any](t, providers["clipal"])[0])
	env := requireConfigValue[map[string]any](t, doc["env"])
	if selected["id"] != "opaque/gateway-model" || selected["envKey"] != "CLIPAL_API_KEY" || env["CLIPAL_API_KEY"] != "clipal" {
		t.Fatalf("planned: %s", files[0].Planned)
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.json")); !os.IsNotExist(err) {
		t.Fatal("planner created settings")
	}
}

func TestQwenPlannerRejectsMalformedRoutingScalarsWithExplicitSelection(t *testing.T) {
	for _, key := range []string{"name", "baseUrl"} {
		t.Run("model-"+key, func(t *testing.T) {
			assertPiQwenPlannerRejectsScalar(t, ProductQwenCode, "settings.json", map[string]any{"model": map[string]any{key: 42}})
		})
	}
	for _, key := range []string{"selectedType", "enforcedType", "apiKey", "baseUrl"} {
		t.Run("auth-"+key, func(t *testing.T) {
			assertPiQwenPlannerRejectsScalar(t, ProductQwenCode, "settings.json", map[string]any{"security": map[string]any{"auth": map[string]any{key: false}}})
		})
	}
	for _, key := range []string{"id", "baseUrl", "wireApi", "envKey"} {
		t.Run("provider-model-"+key, func(t *testing.T) {
			entry := map[string]any{"id": "explicit-model", key: 42}
			assertPiQwenPlannerRejectsScalar(t, ProductQwenCode, "settings.json", map[string]any{"modelProviders": map[string]any{"clipal": []any{entry}}})
		})
	}
	t.Run("credential-env", func(t *testing.T) {
		assertPiQwenPlannerRejectsScalar(t, ProductQwenCode, "settings.json", map[string]any{"env": map[string]any{"CLIPAL_API_KEY": 42}})
	})
	t.Run("protocol", func(t *testing.T) {
		assertPiQwenPlannerRejectsScalar(t, ProductQwenCode, "settings.json", map[string]any{"providerProtocol": map[string]any{"clipal": false}})
	})
}
