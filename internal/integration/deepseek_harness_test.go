package integration

import (
	"github.com/PAIArtCom/Clipal/internal/config"
	"gopkg.in/yaml.v3"
	"os"
	"testing"
)

func TestDeepSeekHarnessPlannerPreservesRoutesAndCredentialReferences(t *testing.T) {
	original := "- id: untouched\n  config: {enabled: true}\n- id: llm-pi-ai\n  config:\n    providers:\n      personal: {baseURL: https://personal.example}\n      clipal:\n        apiKeyEnv: PRIVATE_TOKEN\n        custom: keep\n        models: [{id: selected, name: Saved}]\n- id: agent-default-model\n  config: {provider: previous, model: selected}\n"
	m, path := harnessClientFixture(t, ProductDeepSeekHarness, "cordis.patch.yml", original, "")
	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	files, err := m.planDeepSeekHarness(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var entries []map[string]any
	if err = yaml.Unmarshal(files[0].Planned, &entries); err != nil {
		t.Fatal(err)
	}
	providers := requireConfigValue[map[string]any](t, requireConfigValue[map[string]any](t, entries[1]["config"])["providers"])
	route := requireConfigValue[map[string]any](t, providers["clipal"])
	if route["apiKeyEnv"] != "PRIVATE_TOKEN" || route["custom"] != "keep" || route["baseURL"] != m.advertisedBaseURL(cfg) || route["api"] != "openai-completions" || providers["personal"] == nil {
		t.Fatalf("route=%#v", route)
	}
	if len(requireConfigValue[[]any](t, route["models"])) != 1 || requireConfigValue[map[string]any](t, requireConfigValue[[]any](t, route["models"])[0])["name"] != "Saved" {
		t.Fatal("model metadata changed")
	}
	if requireConfigValue[map[string]any](t, entries[2]["config"])["provider"] != "clipal" {
		t.Fatal("default route not selected")
	}
	actual, _ := os.ReadFile(path)
	if string(actual) != original {
		t.Fatal("planner wrote configuration")
	}
}
func TestDeepSeekHarnessPlannerFreshGatewayModel(t *testing.T) {
	m, path := harnessClientFixture(t, ProductDeepSeekHarness, "cordis.patch.yml", "", "opaque/vendor-model")
	files, err := m.planDeepSeekHarness(&config.Config{Global: config.DefaultGlobalConfig()})
	if err != nil {
		t.Fatal(err)
	}
	var entries []map[string]any
	if err = yaml.Unmarshal(files[0].Planned, &entries); err != nil {
		t.Fatal(err)
	}
	selection := requireConfigValue[map[string]any](t, entries[0]["config"])
	if selection["model"] != "opaque/vendor-model" {
		t.Fatal("model ID changed")
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("planner created configuration")
	}
}
func TestDeepSeekHarnessPlannerRejectsMalformedPatch(t *testing.T) {
	for _, original := range []string{"null", "{}", "[broken", "- scalar", "- id: llm-pi-ai\n  config: {providers: {clipal: {apiKeyEnv: 5}}}", "- id: llm-pi-ai\n  config: {providers: {clipal: {headers: {Authorization: 5}}}}", "- id: llm-pi-ai\n  config: []", "- id: x\n- id: x", "[]\n---\n[]"} {
		t.Run(original, func(t *testing.T) {
			m, path := harnessClientFixture(t, ProductDeepSeekHarness, "cordis.patch.yml", original, "selected")
			if _, err := m.planDeepSeekHarness(&config.Config{Global: config.DefaultGlobalConfig()}); err == nil {
				t.Fatal("accepted malformed patch")
			}
			actual, _ := os.ReadFile(path)
			if string(actual) != original {
				t.Fatal("modified malformed patch")
			}
		})
	}
}
