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
	target := TargetOptions{}
	if ProductTargetCapabilities(product).Shell {
		target.Shell = "sh"
	}
	if modelSelectionProduct(product) {
		target.Model = "test-model"
	}
	if product == ProductKimiCLI {
		target.ContextWindow = 32768
	}
	if product == ProductTRAEAgent {
		target.ConfigFile = filepath.Join(m.homeDir, "trae_config.yaml")
	}
	if err := m.SetTarget(product, target); err != nil {
		t.Fatal(err)
	}
	files, err := m.planTakeover(product, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var originals []backupFile
	for _, file := range files {
		body := []byte("{\"theme\":\"original\"}\n")
		switch file.Format {
		case managedTOML:
			body = []byte("theme = 'original'\n")
		case managedYAML:
			body = []byte("theme: original\n")
		case managedBytes:
			body = []byte("# original shell settings\nexport KEEP='original'\n")
		}
		if product == ProductDeepSeekHarness {
			body = []byte("- id: keep-original\n  config: {}\n")
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
	for _, product := range SupportedProducts() {
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
	for _, product := range SupportedProducts() {
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
	for _, product := range SupportedProducts() {
		t.Run(string(product), func(t *testing.T) {
			m, cfg, originals := takeoverFixture(t, product)
			link := originals[0].TargetPath
			target := filepath.Join(t.TempDir(), "shared.toml")
			if err := os.Rename(link, target); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, link); err != nil {
				t.Skip(err)
			}
			if _, err := m.Apply(product, cfg); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Rollback(product, cfg); err != nil {
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

		})
	}
}

func TestTakeoverAdoptsLegacyBaseline(t *testing.T) {
	// Only products shipped with the old backup format have legacy baselines.
	for _, product := range []ProductID{ProductClaudeCode, ProductCodexCLI, ProductOpenCode, ProductAntigravityCLI, ProductContinue, ProductAider, ProductGoose} {
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

func TestTakeoverMissingFilesRestoreAbsence(t *testing.T) {
	for _, product := range SupportedProducts() {
		t.Run(string(product), func(t *testing.T) {
			m, cfg, originals := takeoverFixture(t, product)
			for _, file := range originals {
				if err := os.Remove(file.TargetPath); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := m.Apply(product, cfg); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Rollback(product, cfg); err != nil {
				t.Fatal(err)
			}
			for _, file := range originals {
				if _, err := os.Stat(file.TargetPath); !os.IsNotExist(err) {
					t.Fatalf("file was not removed: %s: %v", file.TargetPath, err)
				}
			}
		})
	}
}

func TestTakeoverRequiresExplicitModelWithoutExistingSelection(t *testing.T) {
	for _, product := range []ProductID{ProductOpenCode, ProductContinue, ProductAider, ProductGoose} {
		t.Run(string(product), func(t *testing.T) {
			m := Manager{homeDir: t.TempDir(), configDir: t.TempDir()}
			cfg := &config.Config{Global: config.DefaultGlobalConfig()}
			status, err := m.Status(product, cfg)
			if err != nil || status.State != StateNotConfigured {
				t.Fatalf("status: %+v %v", status, err)
			}
			if _, err = m.Apply(product, cfg); err == nil {
				t.Fatal("guessed a model")
			}
			if _, err = os.Stat(status.TargetPath); !os.IsNotExist(err) {
				t.Fatal("failed validation created target")
			}
			if m.hasLatestBackup(product) {
				t.Fatal("failed validation created baseline")
			}
			if err = m.SetTarget(product, TargetOptions{Model: "gateway/arbitrary-id"}); err != nil {
				t.Fatal(err)
			}
			if _, err = m.Apply(product, cfg); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTakeoverPreservesContinueAndGooseOptions(t *testing.T) {
	for _, product := range []ProductID{ProductContinue, ProductGoose} {
		t.Run(string(product), func(t *testing.T) {
			m := Manager{homeDir: t.TempDir(), configDir: t.TempDir()}
			cfg := &config.Config{Global: config.DefaultGlobalConfig()}
			var path string
			var raw []byte
			if product == ProductContinue {
				path, _ = m.continueTargetPath()
				raw = []byte("name: User\nversion: 1.0.0\nschema: v1\nmodels:\n  - name: Other\n    provider: openai\n    model: old\n  - name: Clipal\n    provider: openai\n    model: chosen\n    apiBase: http://old\n    apiKey: user\n    useResponsesApi: false\n    capabilities: [tool_use]\n    roles: [chat]\n")
			} else {
				path, _ = m.gooseTargetPath()
				raw = []byte(`{"name":"clipal","engine":"openai","base_url":"http://old","models":[{"name":"chosen","context_limit":32768}],"headers":{"X-Team":"keep"},"requires_auth":false,"supports_streaming":false}`)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, raw, 0640); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				cfg.Global.Port++
				if _, err := m.Apply(product, cfg); err != nil {
					t.Fatal(err)
				}
			}
			if product == ProductContinue {
				doc, err := readYAMLMap(path)
				if err != nil {
					t.Fatal(err)
				}
				model := findContinueClipalModel(doc)
				roles, ok := model["roles"].([]any)
				if !ok {
					t.Fatal("missing roles")
				}
				if model["model"] != "chosen" || model["apiKey"] != "user" || model["useResponsesApi"] != false || model["capabilities"] == nil || len(roles) != 1 {
					t.Fatalf("lost options: %#v", model)
				}
			} else {
				doc, err := readJSONMap(path)
				if err != nil {
					t.Fatal(err)
				}
				headers, ok := doc["headers"].(map[string]any)
				if !ok {
					t.Fatal("missing headers")
				}
				models, ok := doc["models"].([]any)
				if !ok || len(models) == 0 {
					t.Fatal("missing models")
				}
				model, ok := models[0].(map[string]any)
				if !ok {
					t.Fatal("invalid model")
				}
				if headers["X-Team"] != "keep" || doc["supports_streaming"] != false || doc["requires_auth"] != false || model["name"] != "chosen" {
					t.Fatalf("lost options: %#v", doc)
				}
			}
			if _, err := m.Rollback(product, cfg); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, raw) {
				t.Fatal("rollback was not byte-exact")
			}
		})
	}
}

func TestOpenCodeRestrictionsBlockWithoutChangingFiles(t *testing.T) {
	for _, restriction := range []string{`"disabled_providers":["clipal"]`, `"enabled_providers":["other"]`, `"enabled_providers":[]`} {
		t.Run(restriction, func(t *testing.T) {
			m, cfg, originals := takeoverFixture(t, ProductOpenCode)
			raw := []byte("{" + restriction + ` ,"model":"openai/test"}`)
			if err := os.WriteFile(originals[0].TargetPath, raw, 0640); err != nil {
				t.Fatal(err)
			}
			status, err := m.Status(ProductOpenCode, cfg)
			if err != nil || status.Verification != "blocked" {
				t.Fatalf("missing restriction: %+v %v", status, err)
			}
			if _, err = m.Apply(ProductOpenCode, cfg); err == nil {
				t.Fatal("applied a disabled provider")
			}
			got, err := os.ReadFile(originals[0].TargetPath)
			if err != nil || !bytes.Equal(got, raw) {
				t.Fatal("changed restricted configuration")
			}
		})
	}
}

func TestOtherClientPathsFollowClientContracts(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	m := Manager{homeDir: home, configDir: t.TempDir()}
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("OPENCODE_CONFIG", "")
	t.Setenv("OPENCODE_CONFIG_DIR", "")
	path, err := m.opencodeTargetPath()
	if err != nil || path != filepath.Join(root, "opencode", "opencode.json") {
		t.Fatalf("XDG: %s %v", path, err)
	}
	jsonc := filepath.Join(root, "opencode", "opencode.jsonc")
	if err = os.MkdirAll(filepath.Dir(jsonc), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(jsonc, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	path, err = m.opencodeTargetPath()
	if err != nil || path != jsonc {
		t.Fatal("did not prefer higher-priority JSONC file")
	}
	custom := filepath.Join(t.TempDir(), "custom.jsonc")
	t.Setenv("OPENCODE_CONFIG", custom)
	path, err = m.opencodeTargetPath()
	if err != nil || path != custom {
		t.Fatal("ignored OPENCODE_CONFIG")
	}
	t.Setenv("CONTINUE_GLOBAL_DIR", root)
	path, err = m.continueTargetPath()
	if err != nil || path != filepath.Join(root, "config.yaml") {
		t.Fatal("ignored CONTINUE_GLOBAL_DIR")
	}
	t.Setenv("GOOSE_PATH_ROOT", root)
	path, err = m.gooseTargetPath()
	if err != nil || path != filepath.Join(root, "config", "custom_providers", "clipal.json") {
		t.Fatal("ignored GOOSE_PATH_ROOT")
	}
	for _, product := range []ProductID{ProductOpenCode, ProductContinue, ProductAider} {
		chosen := filepath.Join(t.TempDir(), "explicit.conf")
		if err = m.SetTarget(product, TargetOptions{ConfigFile: chosen, Model: "chosen"}); err != nil {
			t.Fatal(err)
		}
		status, e := m.Status(product, &config.Config{Global: config.DefaultGlobalConfig()})
		if e != nil || status.TargetPath != chosen {
			t.Fatalf("explicit file: %+v %v", status, e)
		}
	}
}
