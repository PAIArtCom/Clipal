package integration

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/PAIArtCom/Clipal/internal/config"
)

func TestCopilotPlannerLiteralShellValues(t *testing.T) {
	m := Manager{homeDir: t.TempDir(), configDir: t.TempDir()}
	marker := filepath.Join(m.homeDir, "executed")
	model := "gateway/'\"$(touch " + marker + ")`touch " + marker + "`"
	if err := m.SetTarget(ProductCopilotCLI, TargetOptions{Shell: "sh", Model: model}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	files, err := m.planCopilot(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("got %d profiles", len(files))
	}
	if len(files[0].Dependencies) != 1 || filepath.Base(files[0].Dependencies[0].TargetPath) != "providers.json" {
		t.Fatal("missing registry dependency")
	}
	profile := filepath.Join(t.TempDir(), "profile")
	if err := os.WriteFile(profile, files[0].Planned, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", `. "$1"; test -z "${COPILOT_PROVIDER_BEARER_TOKEN+x}${COPILOT_PROVIDER_API_KEY_COMMAND+x}${COPILOT_PROVIDER_HEADERS+x}" || exit 17; printf '%s\000%s\000%s\000%s' "$COPILOT_MODEL" "$COPILOT_PROVIDER_WIRE_MODEL" "$COPILOT_PROVIDER_MODEL_ID" "$COPILOT_PROVIDER_BASE_URL"`, "sh", profile)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + m.homeDir, "COPILOT_PROVIDER_BEARER_TOKEN=old", "COPILOT_PROVIDER_API_KEY_COMMAND=touch " + marker, "COPILOT_PROVIDER_HEADERS=Authorization: old"}
	got, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("source profile: %v: %s", err, got)
	}
	want := []byte(model + "\x00" + model + "\x00" + model + "\x00" + m.advertisedBaseURL(cfg) + "/v1")
	if !bytes.Equal(got, want) {
		t.Fatalf("shell did not preserve literal values: %q", got)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("shell model executed code: %v", err)
	}
	if _, err := os.Stat(files[0].TargetPath); !os.IsNotExist(err) {
		t.Fatalf("planner wrote profile: %v", err)
	}
}

func TestCopilotShellBlockQuotesNewlinesLiterally(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "executed")
	value := "line one\n'\"$(touch " + marker + ")`touch " + marker + "`\nline three"
	body, err := managedShellBlock("# keep\n", "Copilot CLI", [][2]string{{"COPILOT_MODEL", value}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "profile")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", `. "$1"; printf '%s' "$COPILOT_MODEL"`, "sh", path)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	got, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("source profile: %v: %s", err, got)
	}
	if string(got) != value {
		t.Fatalf("literal value changed: %q", got)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("shell executed a literal: %v", err)
	}
}

func TestCopilotPlannerGuardsProviderRegistry(t *testing.T) {
	for _, body := range []string{`{`, `null`, `{"providers":{"existing":{}}}`, `{"models":[{"id":"existing"}]}`, `{"providers":"invalid"}`} {
		t.Run(body, func(t *testing.T) {
			m := Manager{homeDir: t.TempDir(), configDir: t.TempDir()}
			if err := m.SetTarget(ProductCopilotCLI, TargetOptions{Shell: "sh", Model: "gateway-model"}); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "providers.json")
			t.Setenv("COPILOT_PROVIDERS_CONFIG", path)
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			files, err := m.planCopilot(&config.Config{Global: config.DefaultGlobalConfig()})
			if err == nil || len(files) != 0 {
				t.Fatalf("expected registry refusal: files=%d err=%v", len(files), err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != body {
				t.Fatal("registry changed")
			}
		})
	}
}

func TestCopilotPlannerPreservesAuthenticationState(t *testing.T) {
	m := Manager{homeDir: t.TempDir(), configDir: t.TempDir()}
	if err := m.SetTarget(ProductCopilotCLI, TargetOptions{Shell: "sh", Model: "gateway-model"}); err != nil {
		t.Fatal(err)
	}
	dir, err := m.clientConfigDir(ProductCopilotCLI)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	auth := []byte(`{"auth_source":"preserved","github_token":"original","custom":{"keep":true}}`)
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, auth, 0600); err != nil {
		t.Fatal(err)
	}
	files, err := m.planCopilot(&config.Config{Global: config.DefaultGlobalConfig()})
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file.TargetPath == path {
			t.Fatal("authentication state included in write plan")
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, auth) {
		t.Fatal("authentication state changed")
	}
}
