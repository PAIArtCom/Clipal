package integration

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PAIArtCom/Clipal/internal/config"
)

func takeoverFixture(t *testing.T, product ProductID) (Manager, *config.Config, []backupFile) {
	t.Helper()
	m := Manager{homeDir: t.TempDir(), configDir: t.TempDir()}
	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	if product == ProductAntigravityCLI && os.Getenv("SHELL") == "" {
		if err := m.SetTarget(product, TargetOptions{Shell: "sh"}); err != nil {
			t.Fatal(err)
		}
	}
	files, err := m.planTakeover(product, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var originals []backupFile
	for i, file := range files {
		body := []byte("{\"theme\":\"original\"}\n")
		if product == ProductCodexCLI {
			body = []byte("model = 'original'\n")
		} else if product == ProductAntigravityCLI && i > 0 {
			body = []byte("# original shell settings\nexport KEEP='original'\n")
		}
		if err := os.MkdirAll(filepath.Dir(file.TargetPath), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file.TargetPath, body, 0640); err != nil {
			t.Fatal(err)
		}
		original, err := readBackupFile(file.TargetPath)
		if err != nil {
			t.Fatal(err)
		}
		originals = append(originals, original)
	}
	return m, cfg, originals
}

func assertTakeoverFiles(t *testing.T, want []backupFile) {
	t.Helper()
	for _, expected := range want {
		got, err := readBackupFile(expected.TargetPath)
		if err != nil {
			t.Fatal(err)
		}
		if got.Existed != expected.Existed || !bytes.Equal(got.Original, expected.Original) || got.Mode != expected.Mode {
			t.Fatalf("file not restored: %s", expected.TargetPath)
		}
	}
}

func TestTakeoverCyclesPreserveBaselineAcrossUpdates(t *testing.T) {
	for _, product := range []ProductID{ProductCodexCLI, ProductClaudeCode, ProductAntigravityCLI} {
		t.Run(string(product), func(t *testing.T) {
			m, cfg, originals := takeoverFixture(t, product)
			for cycle := 0; cycle < 2; cycle++ {
				if _, err := m.Apply(product, cfg); err != nil {
					t.Fatal(err)
				}
				baseline, err := m.loadTakeoverCycle(product)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := m.Apply(product, cfg); err != nil {
					t.Fatal(err)
				}
				cfg.Global.Port++
				if _, err := m.Apply(product, cfg); err != nil {
					t.Fatal(err)
				}
				updated, err := m.loadTakeoverCycle(product)
				if err != nil {
					t.Fatal(err)
				}
				if updated.BackupDir != baseline.BackupDir {
					t.Fatal("reapply replaced active baseline")
				}
				result, err := m.Rollback(product, cfg)
				if err != nil {
					t.Fatal(err)
				}
				if result.Status.BackupAvailable || m.hasLatestBackup(product) {
					t.Fatal("rollback did not end cycle")
				}
				assertTakeoverFiles(t, originals)
				for i, file := range originals {
					changed := bytes.ReplaceAll(file.Original, []byte("original"), []byte("updated"))
					if err := os.WriteFile(file.TargetPath, changed, file.Mode); err != nil {
						t.Fatal(err)
					}
					originals[i].Original = changed
				}
			}
		})
	}
}

func TestTakeoverFailureRestoresFilesAndCycle(t *testing.T) {
	for _, product := range []ProductID{ProductCodexCLI, ProductClaudeCode, ProductAntigravityCLI} {
		t.Run(string(product), func(t *testing.T) {
			m, cfg, originals := takeoverFixture(t, product)
			failAt := 0
			if len(originals) > 1 {
				failAt = 1
			}
			fail := func(i int, _ string) error {
				if i == failAt {
					return errors.New("injected write failure")
				}
				return nil
			}
			m.beforeTakeoverWrite = fail
			if _, err := m.Apply(product, cfg); err == nil {
				t.Fatal("expected apply failure")
			}
			assertTakeoverFiles(t, originals)
			if m.hasLatestBackup(product) {
				t.Fatal("failed first apply published backup")
			}
			m.beforeTakeoverWrite = nil
			if _, err := m.Apply(product, cfg); err != nil {
				t.Fatal(err)
			}
			var applied []backupFile
			for _, original := range originals {
				file, err := readBackupFile(original.TargetPath)
				if err != nil {
					t.Fatal(err)
				}
				applied = append(applied, file)
			}
			baseline, err := m.loadTakeoverCycle(product)
			if err != nil {
				t.Fatal(err)
			}
			cfg.Global.Port++
			m.beforeTakeoverWrite = fail
			if _, err := m.Apply(product, cfg); err == nil {
				t.Fatal("expected update failure")
			}
			assertTakeoverFiles(t, applied)
			if _, err := m.Rollback(product, cfg); err == nil {
				t.Fatal("expected rollback failure")
			}
			assertTakeoverFiles(t, applied)
			after, err := m.loadTakeoverCycle(product)
			if err != nil || after.BackupDir != baseline.BackupDir {
				t.Fatalf("lost baseline after failure: %v", err)
			}
			m.beforeTakeoverWrite = nil
			if _, err := m.Rollback(product, cfg); err != nil {
				t.Fatal(err)
			}
			assertTakeoverFiles(t, originals)
		})
	}
}

func TestClaudeInvalidOnboardingCannotPartiallyApply(t *testing.T) {
	m, cfg, originals := takeoverFixture(t, ProductClaudeCode)
	if err := os.WriteFile(originals[1].TargetPath, []byte("{invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Preview(ProductClaudeCode, cfg); err == nil {
		t.Fatal("preview ignored invalid onboarding file")
	}
	if _, err := m.Apply(ProductClaudeCode, cfg); err == nil {
		t.Fatal("expected validation error")
	}
	assertTakeoverFiles(t, originals[:1])
	if m.hasLatestBackup(ProductClaudeCode) {
		t.Fatal("validation failure replaced baseline")
	}
}

func TestTakeoverInspectionFailureKeepsCommittedRecovery(t *testing.T) {
	m, cfg, originals := takeoverFixture(t, ProductClaudeCode)
	// A running client can change an earlier file after its replacement.
	m.beforeTakeoverWrite = func(index int, _ string) error {
		if index == 1 {
			return os.WriteFile(originals[0].TargetPath, []byte("{invalid"), 0640)
		}
		return nil
	}
	result, err := m.Apply(ProductClaudeCode, cfg)
	if err != nil || result.Status.State != StateError || !result.Status.BackupAvailable {
		t.Fatalf("committed write lost recovery state: %+v, %v", result, err)
	}
	m.beforeTakeoverWrite = nil
	if _, err := m.Rollback(ProductClaudeCode, cfg); err != nil {
		t.Fatal(err)
	}
	assertTakeoverFiles(t, originals)
}

func TestTakeoverTargetsRespectEnvironmentAndSelection(t *testing.T) {
	home := t.TempDir()
	codex := t.TempDir()
	claude := t.TempDir()
	t.Setenv("CODEX_HOME", codex)
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	m := Manager{homeDir: home, configDir: t.TempDir()}
	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	for product, want := range map[ProductID]string{ProductCodexCLI: filepath.Join(codex, "config.toml"), ProductClaudeCode: filepath.Join(claude, "settings.json")} {
		status, err := m.Status(product, cfg)
		if err != nil || status.TargetPath != want {
			t.Fatalf("target %s: %#v %v", product, status, err)
		}
	}
	path, err := m.claudeHomeConfigPath()
	if err != nil || path != filepath.Join(claude, ".claude.json") {
		t.Fatalf("onboarding path %s %v", path, err)
	}
	selected := t.TempDir()
	if err := m.SetTarget(ProductCodexCLI, TargetOptions{ConfigDir: selected, Profile: "work"}); err != nil {
		t.Fatal(err)
	}
	if path, err := m.codexTargetPath(); err != nil || path != filepath.Join(selected, "work.config.toml") {
		t.Fatalf("profile target %s %v", path, err)
	}
	if _, err := m.Apply(ProductCodexCLI, cfg); err != nil {
		t.Fatal(err)
	}
	if err := m.SetTarget(ProductCodexCLI, TargetOptions{}); err == nil {
		t.Fatal("changed target with active cycle")
	}
	if _, err := m.Rollback(ProductCodexCLI, cfg); err != nil {
		t.Fatal(err)
	}
	for _, target := range []TargetOptions{{ConfigDir: "relative"}, {Profile: "../other"}} {
		if err := m.SetTarget(ProductCodexCLI, target); err == nil {
			t.Fatal("accepted unsafe target")
		}
	}
	if err := m.SetTarget(ProductCodexCLI, TargetOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestAntigravityFreshShellLoadsAndClearsTakeover(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			if _, err := os.Stat("/bin/" + shell); err != nil {
				t.Skip("shell unavailable")
			}
			t.Setenv("SHELL", "/bin/"+shell)
			m := Manager{homeDir: t.TempDir(), configDir: t.TempDir()}
			cfg := &config.Config{Global: config.DefaultGlobalConfig()}
			zdot := t.TempDir()
			t.Setenv("ZDOTDIR", zdot)
			if shell == "bash" {
				if err := os.WriteFile(filepath.Join(m.homeDir, ".bash_profile"), []byte("# login profile without bashrc\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for _, takeover := range []bool{true, false} {
				if takeover {
					if _, err := m.Apply(ProductAntigravityCLI, cfg); err != nil {
						t.Fatal(err)
					}
				} else {
					if _, err := m.Rollback(ProductAntigravityCLI, cfg); err != nil {
						t.Fatal(err)
					}
				}
				modes := []string{"-ic"}
				if shell == "bash" {
					modes = append(modes, "-lc")
				}
				for _, mode := range modes {
					cmd := exec.Command("/bin/"+shell, mode, `printf '%s|%s' "$GEMINI_API_KEY" "$GOOGLE_GEMINI_BASE_URL"`)
					cmd.Env = []string{"HOME=" + m.homeDir, "PATH=/usr/bin:/bin", "ZDOTDIR=" + zdot}
					output, err := cmd.Output()
					if err != nil {
						t.Fatal(err)
					}
					want := "|"
					if takeover {
						want = "clipal|" + m.advertisedBaseURL(cfg)
					}
					if string(output) != want {
						t.Fatalf("%s %s after takeover=%v: %q", shell, mode, takeover, output)
					}
				}
			}
		})
	}
}

func TestTakeoverStatusReportsRoutingOverrides(t *testing.T) {
	t.Setenv("CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST", "1")
	m, cfg, _ := takeoverFixture(t, ProductClaudeCode)
	result, err := m.Apply(ProductClaudeCode, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status.Verification != "blocked" || !strings.Contains(result.Status.Warning, "MANAGED_BY_HOST") {
		t.Fatalf("missing host conflict: %#v", result.Status)
	}
	t.Setenv("CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST", "")
	path, err := m.claudeTargetPath()
	if err != nil {
		t.Fatal(err)
	}
	body, err := readJSONMap(path)
	if err != nil {
		t.Fatal(err)
	}
	env, ok := body["env"].(map[string]any)
	if !ok {
		t.Fatal("missing env")
	}
	env["CLAUDE_CODE_USE_VERTEX"] = "1"
	raw, err := marshalJSONMap(body)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	status, err := m.Status(ProductClaudeCode, cfg)
	if err != nil || status.Verification != "blocked" {
		t.Fatalf("missing provider conflict %#v %v", status, err)
	}
}

func TestTakeoverRecoveryFailureRetainsBaseline(t *testing.T) {
	m, cfg, originals := takeoverFixture(t, ProductClaudeCode)
	m.beforeTakeoverWrite = func(i int, _ string) error {
		if i != 1 {
			return nil
		}
		if err := os.Remove(originals[0].TargetPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(originals[0].TargetPath, 0700); err != nil {
			t.Fatal(err)
		}
		return errors.New("injected failure with unavailable recovery target")
	}
	if _, err := m.Apply(ProductClaudeCode, cfg); err == nil || !strings.Contains(err.Error(), "backup retained") {
		t.Fatalf("expected retained recovery baseline: %v", err)
	}
	if _, err := m.loadTakeoverCycle(ProductClaudeCode); err != nil {
		t.Fatalf("lost recovery baseline: %v", err)
	}
	if err := os.Remove(originals[0].TargetPath); err != nil {
		t.Fatal(err)
	}
	m.beforeTakeoverWrite = nil
	if _, err := m.Rollback(ProductClaudeCode, cfg); err != nil {
		t.Fatal(err)
	}
	assertTakeoverFiles(t, originals)
}

func TestTakeoverRollbackRestoresPermissionsAfterFailure(t *testing.T) {
	m, cfg, originals := takeoverFixture(t, ProductClaudeCode)
	if _, err := m.Apply(ProductClaudeCode, cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(originals[0].TargetPath, 0600); err != nil {
		t.Fatal(err)
	}
	current, err := readBackupFile(originals[0].TargetPath)
	if err != nil {
		t.Fatal(err)
	}
	m.beforeTakeoverWrite = func(i int, _ string) error {
		if i == 1 {
			return errors.New("rollback failure")
		}
		return nil
	}
	if _, err := m.Rollback(ProductClaudeCode, cfg); err == nil {
		t.Fatal("expected rollback failure")
	}
	assertTakeoverFiles(t, []backupFile{current})
	m.beforeTakeoverWrite = nil
	if _, err := m.Rollback(ProductClaudeCode, cfg); err != nil {
		t.Fatal(err)
	}
	assertTakeoverFiles(t, originals)
}

func TestTakeoverPreservesConfigurationSymlinks(t *testing.T) {
	m, cfg, originals := takeoverFixture(t, ProductCodexCLI)
	link := originals[0].TargetPath
	target := filepath.Join(t.TempDir(), "shared.toml")
	if err := os.Rename(link, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skip(err)
	}
	if _, err := m.Apply(ProductCodexCLI, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Rollback(ProductCodexCLI, cfg); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("replaced configuration symlink")
	}
	raw, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(raw, originals[0].Original) {
		t.Fatal("did not restore symlink target")
	}
}

func TestTakeoverAdoptsLegacyBaseline(t *testing.T) {
	for _, product := range []ProductID{ProductCodexCLI, ProductClaudeCode, ProductAntigravityCLI} {
		t.Run(string(product), func(t *testing.T) {
			m, cfg, originals := takeoverFixture(t, product)
			paths := []string{}
			for _, file := range originals[1:] {
				paths = append(paths, file.TargetPath)
			}
			if _, err := m.createBackup(product, originals[0].TargetPath, paths...); err != nil {
				t.Fatal(err)
			}
			files, err := m.planTakeover(product, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err = m.writeTakeoverFiles(files, nil); err != nil {
				t.Fatal(err)
			}
			cfg.Global.Port++
			if _, err = m.Apply(product, cfg); err != nil {
				t.Fatal(err)
			}
			if _, err = m.Rollback(product, cfg); err != nil {
				t.Fatal(err)
			}
			assertTakeoverFiles(t, originals)
		})
	}
}
