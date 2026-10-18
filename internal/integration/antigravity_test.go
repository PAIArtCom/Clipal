package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/PAIArtCom/Clipal/internal/config"
)

func TestAntigravityTakeoverPreviewApplyAndRollback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-profile takeover is Unix-only")
	}
	for _, shell := range []string{"/bin/bash", "/bin/zsh", "/bin/sh"} {
		for _, existed := range []bool{false, true} {
			t.Run(filepath.Base(shell)+map[bool]string{true: "-existing", false: "-new"}[existed], func(t *testing.T) {
				t.Setenv("SHELL", shell)
				home := t.TempDir()
				m := Manager{homeDir: home, configDir: t.TempDir()}
				cfg := &config.Config{Global: config.DefaultGlobalConfig()}
				settings, profile, err := m.antigravityPaths()
				if err != nil {
					t.Fatal(err)
				}
				originalSettings := []byte("{\"theme\":\"dark\",\"modelProvider\":\"other\"}\n")
				originalProfile := []byte("# user profile\nexport GEMINI_API_KEY='prior-key'\nexport KEEP_ME='yes'\n")
				if existed {
					if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
						t.Fatal(err)
					}
					for path, data := range map[string][]byte{settings: originalSettings, profile: originalProfile} {
						if err := os.WriteFile(path, data, 0o600); err != nil {
							t.Fatal(err)
						}
					}
				}
				preview, err := m.Preview(ProductAntigravityCLI, cfg)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(preview.PlannedContent, settings) || !strings.Contains(preview.PlannedContent, profile) || !strings.Contains(preview.PlannedContent, "GOOGLE_GEMINI_BASE_URL") {
					t.Fatalf("preview must cover both files: %s", preview.PlannedContent)
				}
				if !existed {
					if _, err := os.Stat(settings); !os.IsNotExist(err) {
						t.Fatal("preview wrote settings")
					}
				}
				result, err := m.Apply(ProductAntigravityCLI, cfg)
				if err != nil || result.Status.State != StateConfigured {
					t.Fatalf("apply=%#v error=%v", result, err)
				}
				raw, err := os.ReadFile(settings)
				if err != nil {
					t.Fatal(err)
				}
				var body map[string]any
				if err := json.Unmarshal(raw, &body); err != nil {
					t.Fatal(err)
				}
				if body["modelProvider"] != "gemini" || (existed && body["theme"] != "dark") {
					t.Fatalf("settings lost unrelated values: %s", raw)
				}
				profileBytes, err := os.ReadFile(profile)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Count(string(profileBytes), antigravityBlockStart) != 1 || (existed && !strings.Contains(string(profileBytes), "KEEP_ME")) {
					t.Fatal("profile was not preserved")
				}
				output, err := exec.Command("/bin/sh", "-c", `. "$1"; printf '%s|%s' "$GEMINI_API_KEY" "$GOOGLE_GEMINI_BASE_URL"`, "sh", profile).CombinedOutput()
				if err != nil || string(output) != "clipal|http://127.0.0.1:3333/clipal" {
					t.Fatalf("shell environment=%s error=%v", output, err)
				}
				if _, err = m.Apply(ProductAntigravityCLI, cfg); err != nil {
					t.Fatal(err)
				}
				// A changed endpoint must retain the first backup, not capture the managed state.
				cfg.Global.Port = 4455
				if _, err = m.Apply(ProductAntigravityCLI, cfg); err != nil {
					t.Fatal(err)
				}
				backupPreview, err := m.Preview(ProductAntigravityCLI, cfg)
				if err != nil {
					t.Fatal(err)
				}
				if existed && !strings.Contains(backupPreview.BackupContent, "prior-key") {
					t.Fatal("initial profile backup was overwritten")
				}
				if _, err = m.Rollback(ProductAntigravityCLI, cfg); err != nil {
					t.Fatal(err)
				}
				for path, want := range map[string][]byte{settings: originalSettings, profile: originalProfile} {
					got, err := os.ReadFile(path)
					if !existed {
						if !os.IsNotExist(err) {
							t.Fatalf("new file remains after rollback: %s", path)
						}
					} else if err != nil || string(got) != string(want) {
						t.Fatalf("rollback %s=%s error=%v", path, got, err)
					}
				}
			})
		}
	}
}

func TestAntigravityTakeoverRejectsInvalidInputsBeforeWriting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-profile takeover is Unix-only")
	}
	for _, kind := range []string{"unsupported-shell", "invalid-settings", "invalid-profile"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			m := Manager{homeDir: home, configDir: t.TempDir()}
			cfg := &config.Config{Global: config.DefaultGlobalConfig()}
			t.Setenv("SHELL", "/bin/bash")
			settings, profile, _ := m.antigravityPaths()
			if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
				t.Fatal(err)
			}
			body := "{\"theme\":\"dark\"}"
			shell := "# safe profile\n"
			switch kind {
			case "unsupported-shell":
				t.Setenv("SHELL", "/bin/fish")
			case "invalid-settings":
				body = "{"
			case "invalid-profile":
				shell = antigravityBlockStart + "\nmissing end\n"
			}
			if err := os.WriteFile(settings, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(profile, []byte(shell), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Apply(ProductAntigravityCLI, cfg); err == nil {
				t.Fatal("expected validation failure")
			}
			got, _ := os.ReadFile(settings)
			actual, _ := os.ReadFile(profile)
			if string(got) != body || string(actual) != shell || m.hasLatestBackup(ProductAntigravityCLI) {
				t.Fatal("failed preflight changed user files or backup")
			}
		})
	}
}

func TestGeminiCLIIsNotATakeoverProduct(t *testing.T) {
	m := Manager{homeDir: t.TempDir(), configDir: t.TempDir()}
	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	for _, product := range SupportedProducts() {
		if product == ProductID("gemini") {
			t.Fatal("retired CLI still listed")
		}
	}
	if _, err := m.Apply(ProductID("gemini"), cfg); err == nil {
		t.Fatal("retired CLI remains configurable")
	}
}
