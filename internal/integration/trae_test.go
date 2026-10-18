package integration

import (
	"github.com/PAIArtCom/Clipal/internal/config"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"testing"
)

func TestTRAEPlannerPreservesProviderCredentialsAndModelMetadata(t *testing.T) {
	original := "custom: keep\nagents:\n  trae_agent: {model: personal, enable_lakeview: false}\nmodels:\n  personal: {model: existing/model, model_provider: original, temperature: 0.8}\nmodel_providers:\n  original: {provider: anthropic, api_key: saved-personal}\n  clipal: {provider: openai, api_key: saved-managed}\n"
	m, path := harnessClientFixture(t, ProductTRAEAgent, "trae_config.yaml", original, "")
	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	files, err := m.planTRAE(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err = yaml.Unmarshal(files[0].Planned, &doc); err != nil {
		t.Fatal(err)
	}
	providers := requireConfigValue[map[string]any](t, doc["model_providers"])
	route := requireConfigValue[map[string]any](t, providers["clipal"])
	if route["api_key"] != "saved-managed" || route["provider"] != "openai" || route["base_url"] != m.advertisedBaseURL(cfg) || requireConfigValue[map[string]any](t, providers["original"])["api_key"] != "saved-personal" || doc["custom"] != "keep" {
		t.Fatalf("providers=%#v", providers)
	}
	model := requireConfigValue[map[string]any](t, requireConfigValue[map[string]any](t, doc["models"])["personal"])
	if model["model"] != "existing/model" || model["temperature"] != 0.8 || model["model_provider"] != "clipal" {
		t.Fatalf("model=%#v", model)
	}
	actual, _ := os.ReadFile(path)
	if string(actual) != original {
		t.Fatal("planner wrote configuration")
	}
}
func TestTRAEPlannerRequiresExplicitLocation(t *testing.T) {
	home := t.TempDir()
	m := Manager{homeDir: home, configDir: filepath.Join(home, "clipal")}
	if _, err := m.planTRAE(&config.Config{Global: config.DefaultGlobalConfig()}); err == nil {
		t.Fatal("invented a global TRAE configuration path")
	}
}
func TestTRAEPlannerRejectsMalformedConfig(t *testing.T) {
	for _, original := range []string{"null", "[]", "models: [broken", "agents: []", "agents: {trae_agent: {model: []}}", "model_providers: {clipal: {api_key: {bad: value}}}", "models: []", "model_providers: []", "{}\n---\n{}"} {
		t.Run(original, func(t *testing.T) {
			m, path := harnessClientFixture(t, ProductTRAEAgent, "trae_config.yaml", original, "selected")
			if _, err := m.planTRAE(&config.Config{Global: config.DefaultGlobalConfig()}); err == nil {
				t.Fatal("accepted malformed configuration")
			}
			actual, _ := os.ReadFile(path)
			if string(actual) != original {
				t.Fatal("modified malformed configuration")
			}
		})
	}
}

func TestTRAEPlannerCreatesCompleteSelectedModelAtExplicitPath(t *testing.T) {
	m, path := harnessClientFixture(t, ProductTRAEAgent, "trae_config.yaml", "", "opaque/model")
	files, err := m.planTRAE(&config.Config{Global: config.DefaultGlobalConfig()})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err = yaml.Unmarshal(files[0].Planned, &doc); err != nil {
		t.Fatal(err)
	}
	agent := requireConfigValue[map[string]any](t, requireConfigValue[map[string]any](t, doc["agents"])["trae_agent"])
	model := requireConfigValue[map[string]any](t, requireConfigValue[map[string]any](t, doc["models"])[requireConfigValue[string](t, agent["model"])])
	if model["model"] != "opaque/model" || agent["enable_lakeview"] != false || agent["max_steps"] != 200 {
		t.Fatalf("incomplete fresh configuration: %#v", doc)
	}
	for _, key := range []string{"model_provider", "temperature", "top_p", "top_k", "parallel_tool_calls", "max_retries"} {
		if _, ok := model[key]; !ok {
			t.Fatalf("missing required model field %s", key)
		}
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("planner wrote configuration")
	}
}
