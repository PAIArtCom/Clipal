package integration

import (
	"os"
	"testing"

	"github.com/PAIArtCom/Clipal/internal/config"
	toml "github.com/pelletier/go-toml/v2"
)

func TestGrokPlannerUsesExistingWireModelAndPreservesAuth(t *testing.T) {
	original := "custom='keep'\n[models]\ndefault='personal'\n[model.personal]\nmodel='dynamic-model'\napi_key='fixture-personal'\n[model.clipal]\napi_key='fixture-managed'\ncustom_setting=true\n"
	m, path := kimiGrokFixture(t, ProductGrokBuild, original, "", 0)
	files, err := m.planGrok(&config.Config{Global: config.DefaultGlobalConfig()})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := toml.Unmarshal(files[0].Planned, &doc); err != nil {
		t.Fatal(err)
	}
	managed := requireConfigValue[map[string]any](t, requireConfigValue[map[string]any](t, doc["model"])["clipal"])
	if managed["model"] != "dynamic-model" || managed["api_key"] != "fixture-managed" || managed["custom_setting"] != true || managed["api_backend"] != "chat_completions" {
		t.Fatalf("managed: %#v", managed)
	}
	if requireConfigValue[map[string]any](t, requireConfigValue[map[string]any](t, doc["model"])["personal"])["api_key"] != "fixture-personal" || doc["custom"] != "keep" {
		t.Fatal("lost existing config")
	}
	actual, _ := os.ReadFile(path)
	if string(actual) != original {
		t.Fatal("planner wrote client file")
	}
}

func TestGrokPlannerFreshSelectedModel(t *testing.T) {
	m, path := kimiGrokFixture(t, ProductGrokBuild, "", "selected-model", 0)
	files, err := m.planGrok(&config.Config{Global: config.DefaultGlobalConfig()})
	if err != nil || len(files) != 1 {
		t.Fatalf("plan: %v %v", files, err)
	}
	if files[0].TargetPath != path || files[0].Format != managedTOML {
		t.Fatalf("file: %#v", files[0])
	}
	var doc map[string]any
	if err := toml.Unmarshal(files[0].Planned, &doc); err != nil {
		t.Fatal(err)
	}
	if requireConfigValue[map[string]any](t, doc["models"])["default"] != "clipal" || requireConfigValue[map[string]any](t, requireConfigValue[map[string]any](t, doc["model"])["clipal"])["model"] != "selected-model" {
		t.Fatalf("missing routing: %#v", doc)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("planner wrote file")
	}
}

func TestGrokPlannerRejectsMalformedConfig(t *testing.T) {
	for _, original := range []string{"invalid=[", "models=1", "model='bad'", "[models]\ndefault=7", "[models]\ndefault='bad'\n[model]\nbad=1", "[models]\ndefault='bad'\n[model]\nclipal=1"} {
		t.Run(original, func(t *testing.T) {
			m, path := kimiGrokFixture(t, ProductGrokBuild, original, "selected", 0)
			if files, err := m.planGrok(&config.Config{Global: config.DefaultGlobalConfig()}); err == nil || len(files) != 0 {
				t.Fatalf("expected error, got %v %v", files, err)
			}
			actual, _ := os.ReadFile(path)
			if string(actual) != original {
				t.Fatal("modified file")
			}
		})
	}
}

func TestGrokPlannerRejectsMalformedCredentialScalars(t *testing.T) {
	for _, field := range []string{"api_key=7", "base_url=7", "api_backend=7", "env_key=7", "env_key={name='KEY'}", "env_key=['KEY',7]"} {
		t.Run(field, func(t *testing.T) {
			original := "[model.clipal]\n" + field + "\n"
			m, path := kimiGrokFixture(t, ProductGrokBuild, original, "selected-model", 0)
			if files, err := m.planGrok(&config.Config{Global: config.DefaultGlobalConfig()}); err == nil || len(files) != 0 {
				t.Fatalf("expected no-write failure: %v %v", files, err)
			}
			actual, _ := os.ReadFile(path)
			if string(actual) != original {
				t.Fatal("modified malformed config")
			}
		})
	}
}

func TestGrokPlannerPreservesValidEnvKeyForms(t *testing.T) {
	for _, value := range []string{"'KEY'", "['FIRST_KEY','SECOND_KEY']"} {
		m, _ := kimiGrokFixture(t, ProductGrokBuild, "[model.clipal]\nenv_key="+value+"\n", "selected-model", 0)
		files, err := m.planGrok(&config.Config{Global: config.DefaultGlobalConfig()})
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := toml.Unmarshal(files[0].Planned, &doc); err != nil {
			t.Fatal(err)
		}
		managed := requireConfigValue[map[string]any](t, requireConfigValue[map[string]any](t, doc["model"])["clipal"])
		if managed["env_key"] == nil || managed["api_key"] != nil {
			t.Fatalf("changed credentials: %#v", managed)
		}
	}
}
