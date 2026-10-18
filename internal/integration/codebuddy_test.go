package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/PAIArtCom/Clipal/internal/config"
)

func TestCodeBuddyPlannerPreservesRegistryAndAuthentication(t *testing.T) {
	m, cfg, originals := takeoverFixture(t, ProductCodeBuddy)
	dir := filepath.Dir(originals[0].TargetPath)
	settings := []byte(`{"model":"custom/opaque", "env":{"CODEBUDDY_MODEL":"custom/opaque","KEEP":"yes"},"apiKeyHelper":"existing-helper","permissions":{"deny":["Bash(rm:*)"]}}`)
	registry := []byte(`{"metadata":{"keep":true},"availableModels":["other"],"models":[{"id":"other","url":"https://other","apiKey":"other-key"},{"id":"custom/opaque","api":"openai-responses","apiKey":"${USER_KEY}","maxInputTokens":123456,"supportsImages":true,"name":"My model"}]}`)
	for name, body := range map[string][]byte{"settings.json": settings, "models.json": registry} {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.SetTarget(ProductCodeBuddy, TargetOptions{Model: "custom/opaque"}); err != nil {
		t.Fatal(err)
	}
	files, err := m.planCodeBuddy(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("got %d files", len(files))
	}
	var gotSettings, gotRegistry map[string]any
	if err := json.Unmarshal(files[0].Planned, &gotSettings); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(files[1].Planned, &gotRegistry); err != nil {
		t.Fatal(err)
	}
	env, _ := gotSettings["env"].(map[string]any)
	if gotSettings["apiKeyHelper"] != "existing-helper" || env["KEEP"] != "yes" {
		t.Fatal("settings metadata lost")
	}
	models, _ := gotRegistry["models"].([]any)
	selected := requireConfigValue[map[string]any](t, models[1])
	if selected["apiKey"] != "${USER_KEY}" || selected["maxInputTokens"] != float64(123456) || selected["supportsImages"] != true || selected["name"] != "My model" {
		t.Fatalf("model metadata lost: %#v", selected)
	}
	if selected["url"] != m.advertisedBaseURL(cfg)+"/v1/chat/completions" {
		t.Fatalf("bad endpoint: %#v", selected)
	}
	if _, exists := selected["api"]; exists {
		t.Fatal("incompatible api retained")
	}
	if requireConfigValue[map[string]any](t, models[0])["apiKey"] != "other-key" {
		t.Fatal("unrelated model changed")
	}
	available := requireConfigValue[[]any](t, gotRegistry["availableModels"])
	if len(available) != 2 || available[1] != "custom/opaque" {
		t.Fatalf("bad availableModels: %#v", available)
	}
	for name, want := range map[string][]byte{"settings.json": settings, "models.json": registry} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("planner wrote %s", name)
		}
	}
}

func TestCodeBuddyPlannerRejectsMalformedConfiguration(t *testing.T) {
	for _, tc := range []struct{ name, settings, models string }{
		{"invalid settings", `{`, `{}`},
		{"null settings", `null`, `{}`},
		{"wrong env", `{"env":[]}`, `{}`},
		{"wrong selected model", `{"model":123}`, `{}`},
		{"invalid models", `{}`, `{`},
		{"wrong model list", `{}`, `{"models":{}}`},
		{"wrong model entry", `{}`, `{"models":[null]}`},
		{"duplicate selected model", `{}`, `{"models":[{"id":"test-model"},{"id":"test-model"}]}`},
		{"wrong available list", `{}`, `{"availableModels":{}}`},
		{"wrong available entry", `{}`, `{"availableModels":[1]}`},
		{"wrong auth", `{}`, `{"models":[{"id":"test-model","apiKey":123}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, cfg, originals := takeoverFixture(t, ProductCodeBuddy)
			dir := filepath.Dir(originals[0].TargetPath)
			for name, body := range map[string]string{"settings.json": tc.settings, "models.json": tc.models} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			files, err := m.planCodeBuddy(cfg)
			if err == nil || len(files) != 0 {
				t.Fatalf("expected refusal, files=%d err=%v", len(files), err)
			}
			for name, want := range map[string]string{"settings.json": tc.settings, "models.json": tc.models} {
				got, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != want {
					t.Fatalf("changed %s", name)
				}
			}
		})
	}
}

func TestCodeBuddyPlannerRequiresModel(t *testing.T) {
	m := Manager{homeDir: t.TempDir(), configDir: t.TempDir()}
	files, err := m.planCodeBuddy(&config.Config{Global: config.DefaultGlobalConfig()})
	if err == nil || len(files) != 0 {
		t.Fatalf("expected model selection error, files=%d err=%v", len(files), err)
	}
}
