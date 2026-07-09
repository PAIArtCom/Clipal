package main

import (
	"bytes"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMainHelperProcess(t *testing.T) {
	if os.Getenv("CLIPAL_MAIN_HELPER") != "1" {
		return
	}

	os.Args = append([]string{"clipal"}, strings.Split(os.Getenv("CLIPAL_MAIN_ARGS"), "\n")...)
	resetForMainTest()
	main()
	os.Exit(0)
}

func resetForMainTest() {
	// main() uses the package-global default FlagSet.
	// Reset it so multiple helper invocations in the same binary are isolated.
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
}

func runMainHelper(t *testing.T, args ...string) (string, int) {
	t.Helper()
	return runMainHelperInDir(t, "", args...)
}

func runMainHelperInDir(t *testing.T, dir string, args ...string) (string, int) {
	t.Helper()

	//nolint:gosec // Tests intentionally re-exec the current test binary as a helper process.
	cmd := exec.Command(os.Args[0], "-test.run=TestMainHelperProcess")
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(),
		"CLIPAL_MAIN_HELPER=1",
		"CLIPAL_MAIN_ARGS="+strings.Join(args, "\n"),
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return string(out), exitErr.ExitCode()
	}
	t.Fatalf("CombinedOutput: %v", err)
	return "", 0
}

func writeMainConfig(t *testing.T, dir string, port int, configYAML string) {
	t.Helper()

	if strings.TrimSpace(configYAML) == "" {
		configYAML = fmt.Sprintf(`listen_addr: "127.0.0.1"
port: %d
log_level: "info"
reactivate_after: "1h"
upstream_idle_timeout: "3m"
response_header_timeout: "2m"
max_request_body_bytes: 33554432
log_dir: ""
log_retention_days: 7
log_stdout: true
notifications:
  enabled: false
  min_level: "error"
  provider_switch: true
circuit_breaker:
  failure_threshold: 4
  success_threshold: 2
  open_timeout: "60s"
  half_open_max_inflight: 1
ignore_count_tokens_failover: false
`, port)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(configYAML), 0o600); err != nil {
		t.Fatalf("WriteFile config.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "openai.yaml"), []byte(`
mode: auto
providers:
  - name: p1
    base_url: https://example.com
    api_key: key1
    priority: 1
`), 0o600); err != nil {
		t.Fatalf("WriteFile openai.yaml: %v", err)
	}
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("unexpected addr type %T", ln.Addr())
	}
	return addr.Port
}

func waitForPort(t *testing.T, port int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("port %d did not become ready within %s", port, timeout)
}

func TestMainVersionFlag(t *testing.T) {
	out, code := runMainHelper(t, "--version")
	if code != 0 {
		t.Fatalf("exit code = %d, out=%s", code, out)
	}
	if !strings.Contains(out, "clipal ") || !strings.Contains(out, "commit:") {
		t.Fatalf("unexpected version output: %s", out)
	}
}

func TestResolveRootCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		wantCmd  rootCommand
		wantArgs []string
		wantErr  string
	}{
		{
			name:    "NoArgsRunsServer",
			wantCmd: rootCommandRun,
		},
		{
			name:     "RootFlagsRunServer",
			args:     []string{"--version"},
			wantCmd:  rootCommandRun,
			wantArgs: []string{"--version"},
		},
		{
			name:     "RestartAliasRoutesToService",
			args:     []string{"restart", "--dry-run"},
			wantCmd:  rootCommandService,
			wantArgs: []string{"restart", "--dry-run"},
		},
		{
			name:     "ServiceCommandPassesThrough",
			args:     []string{"service", "restart"},
			wantCmd:  rootCommandService,
			wantArgs: []string{"restart"},
		},
		{
			name:     "DeployCommandPassesThrough",
			args:     []string{"deploy", "export", "-o", "prod.json"},
			wantCmd:  rootCommandDeploy,
			wantArgs: []string{"export", "-o", "prod.json"},
		},
		{
			name:     "ExportAliasRoutesToDeployExport",
			args:     []string{"export"},
			wantCmd:  rootCommandDeploy,
			wantArgs: []string{"export"},
		},
		{
			name:     "ImportAliasRoutesToDeployImport",
			args:     []string{"import", "clipal.json"},
			wantCmd:  rootCommandDeploy,
			wantArgs: []string{"import", "clipal.json"},
		},
		{
			name:     "InstallAliasRoutesToDeployInstall",
			args:     []string{"install", "codex"},
			wantCmd:  rootCommandDeploy,
			wantArgs: []string{"install", "codex"},
		},
		{
			name:    "HelpTokenShowsRootHelp",
			args:    []string{"help"},
			wantCmd: rootCommandHelp,
		},
		{
			name:    "ShortHelpFlagShowsRootHelp",
			args:    []string{"-h"},
			wantCmd: rootCommandHelp,
		},
		{
			name:    "UnknownCommandReturnsError",
			args:    []string{"restart-now"},
			wantErr: `unknown command "restart-now"`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotCmd, gotArgs, err := resolveRootCommand(tt.args)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error %q, got nil", tt.wantErr)
				}
				if err.Error() != tt.wantErr {
					t.Fatalf("err=%q want=%q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveRootCommand(%v): %v", tt.args, err)
			}
			if gotCmd != tt.wantCmd {
				t.Fatalf("cmd=%q want=%q", gotCmd, tt.wantCmd)
			}
			if fmt.Sprintf("%q", gotArgs) != fmt.Sprintf("%q", tt.wantArgs) {
				t.Fatalf("args=%q want=%q", gotArgs, tt.wantArgs)
			}
		})
	}
}

func TestMainHelpFlagShowsCommands(t *testing.T) {
	out, code := runMainHelper(t, "-h")
	if code != 0 {
		t.Fatalf("exit code = %d, out=%s", code, out)
	}
	if !strings.Contains(out, "Commands:") || !strings.Contains(out, "clipal restart") || !strings.Contains(out, "deploy") {
		t.Fatalf("unexpected help output: %s", out)
	}
}

func TestDeployExportImportCLI(t *testing.T) {
	src := t.TempDir()
	writeMainConfig(t, src, 3333, "")
	if err := os.WriteFile(filepath.Join(src, "gemini.yaml"), []byte("mode: auto\nproviders: []\n"), 0o600); err != nil {
		t.Fatalf("WriteFile gemini.yaml: %v", err)
	}

	packagePath := filepath.Join(t.TempDir(), "prod.json")
	out, code := runMainHelper(t, "deploy", "export", "-o", packagePath, "--config-dir", src)
	if code != 0 {
		t.Fatalf("export exit code = %d, out=%s", code, out)
	}
	if !strings.Contains(out, "Deploy package written:") {
		t.Fatalf("unexpected export output: %s", out)
	}

	dst := t.TempDir()
	out, code = runMainHelper(t, "deploy", "import", packagePath, "--config-dir", dst)
	if code != 0 {
		t.Fatalf("import exit code = %d, out=%s", code, out)
	}
	if !strings.Contains(out, "Deploy package imported:") {
		t.Fatalf("unexpected import output: %s", out)
	}
	got, err := os.ReadFile(filepath.Join(dst, "openai.yaml"))
	if err != nil {
		t.Fatalf("ReadFile openai.yaml: %v", err)
	}
	if !strings.Contains(string(got), "api_key: key1") {
		t.Fatalf("imported openai.yaml did not include secret: %s", got)
	}
}

func TestDeployExportDefaultOutputAndFixedSuffix(t *testing.T) {
	src := t.TempDir()
	writeMainConfig(t, src, 3333, "")
	outDir := t.TempDir()

	out, code := runMainHelper(t, "deploy", "export", "--config-dir", src, "--output-dir", outDir)
	if code != 0 {
		t.Fatalf("default export exit code = %d, out=%s", code, out)
	}
	defaultPackage := filepath.Join(outDir, "clipal.json")
	if _, err := os.Stat(defaultPackage); err != nil {
		t.Fatalf("expected default package %s: %v", defaultPackage, err)
	}
	if !strings.Contains(out, defaultPackage) {
		t.Fatalf("default package path missing from output:\n%s", out)
	}

	out, code = runMainHelper(t, "deploy", "export", "-o", filepath.Join(outDir, "prod"), "--config-dir", src)
	if code != 0 {
		t.Fatalf("suffix append export exit code = %d, out=%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(outDir, "prod.json")); err != nil {
		t.Fatalf("expected suffixed package: %v", err)
	}

	out, code = runMainHelper(t, "deploy", "export", "-o", filepath.Join(outDir, "prod.conf"), "--config-dir", src)
	if code != 2 {
		t.Fatalf("wrong suffix exit code = %d, out=%s", code, out)
	}
	if !strings.Contains(out, "must use .json") {
		t.Fatalf("expected fixed suffix error, out=%s", out)
	}
}

func TestDeployImportRootAlias(t *testing.T) {
	src := t.TempDir()
	writeMainConfig(t, src, 3333, "")

	packagePath := filepath.Join(t.TempDir(), "clipal.json")
	out, code := runMainHelper(t, "export", "-o", packagePath, "--config-dir", src)
	if code != 0 {
		t.Fatalf("export alias exit code = %d, out=%s", code, out)
	}

	dst := t.TempDir()
	out, code = runMainHelper(t, "import", packagePath, "--config-dir", dst)
	if code != 0 {
		t.Fatalf("import alias exit code = %d, out=%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(dst, "openai.yaml")); err != nil {
		t.Fatalf("import alias should restore openai.yaml: %v", err)
	}
}

func TestDeployImportTemporary(t *testing.T) {
	src := t.TempDir()
	writeMainConfig(t, src, 3333, "")

	packagePath := filepath.Join(t.TempDir(), "prod.json")
	out, code := runMainHelper(t, "deploy", "export", "-o", packagePath, "--config-dir", src)
	if code != 0 {
		t.Fatalf("export exit code = %d, out=%s", code, out)
	}

	normalTarget := t.TempDir()
	out, code = runMainHelper(t, "deploy", "import", packagePath, "--config-dir", normalTarget, "--temporary")
	if code != 0 {
		t.Fatalf("temporary import exit code = %d, out=%s", code, out)
	}
	tmpDir := extractOutputValue(t, out, "Temporary config dir:")
	if tmpDir == "" || tmpDir == normalTarget {
		t.Fatalf("unexpected temporary config dir %q in output:\n%s", tmpDir, out)
	}
	if !strings.Contains(out, "Cleanup: rm -rf ") {
		t.Fatalf("missing cleanup hint in output:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(normalTarget, "openai.yaml")); !os.IsNotExist(err) {
		t.Fatalf("temporary import should not write normal config dir, err=%v", err)
	}
	got, err := os.ReadFile(filepath.Join(tmpDir, "openai.yaml"))
	if err != nil {
		t.Fatalf("ReadFile temporary openai.yaml: %v", err)
	}
	if !strings.Contains(string(got), "api_key: key1") {
		t.Fatalf("temporary import did not include secret: %s", got)
	}
	_ = os.RemoveAll(tmpDir)
}

func TestDeployImportAppliesTakeover(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	src := t.TempDir()
	writeMainConfig(t, src, 4567, "")

	packagePath := filepath.Join(t.TempDir(), "prod.json")
	out, code := runMainHelper(t, "deploy", "export", "-o", packagePath, "--config-dir", src)
	if code != 0 {
		t.Fatalf("export exit code = %d, out=%s", code, out)
	}

	dst := t.TempDir()
	out, code = runMainHelper(t, "deploy", "import", packagePath, "--config-dir", dst, "--takeover", "codex")
	if code != 0 {
		t.Fatalf("import exit code = %d, out=%s", code, out)
	}
	if !strings.Contains(out, "Takeover applied: codex") {
		t.Fatalf("missing takeover output:\n%s", out)
	}
	raw, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("ReadFile codex config: %v", err)
	}
	body := string(raw)
	if !strings.Contains(body, `model_provider = 'clipal'`) && !strings.Contains(body, `model_provider = "clipal"`) {
		t.Fatalf("codex config missing model_provider:\n%s", body)
	}
	if !strings.Contains(body, "http://127.0.0.1:4567/clipal") {
		t.Fatalf("codex config missing imported Clipal base URL:\n%s", body)
	}
}

func TestDeployDryRunShowsOfficialInstallCommands(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	out, code := runMainHelper(t, "deploy", "--dry-run", "--agents", "codex,claude,gemini")
	if code != 0 {
		t.Fatalf("deploy dry-run exit code = %d, out=%s", code, out)
	}
	for _, want := range []string{
		"curl -fsSL https://chatgpt.com/codex/install.sh | sh",
		"curl -fsSL https://claude.ai/install.sh | bash",
		"npm install -g @google/gemini-cli",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, out)
		}
	}
}

func TestShellCommandStringShowsOfficialPipelineCommand(t *testing.T) {
	t.Parallel()

	got := shellCommandString([]string{"sh", "-c", "curl -fsSL https://chatgpt.com/codex/install.sh | sh"})
	want := "curl -fsSL https://chatgpt.com/codex/install.sh | sh"
	if got != want {
		t.Fatalf("shellCommandString = %q, want %q", got, want)
	}
}

func TestDeployPositionalAgentDryRun(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	out, code := runMainHelper(t, "deploy", "codex", "--dry-run")
	if code != 0 {
		t.Fatalf("deploy codex dry-run exit code = %d, out=%s", code, out)
	}
	if !strings.Contains(out, "curl -fsSL https://chatgpt.com/codex/install.sh | sh") {
		t.Fatalf("codex install command missing:\n%s", out)
	}
	if strings.Contains(out, "@anthropic-ai/claude-code") || strings.Contains(out, "@google/gemini-cli") {
		t.Fatalf("deploy codex should not include other agents:\n%s", out)
	}
	if !strings.Contains(out, "Would apply takeover: codex") {
		t.Fatalf("deploy codex should include takeover:\n%s", out)
	}
}

func TestDeployPackageAndPositionalAgentDryRun(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	src := t.TempDir()
	writeMainConfig(t, src, 3333, "")
	packagePath := filepath.Join(t.TempDir(), "prod.json")
	out, code := runMainHelper(t, "export", "-o", packagePath, "--config-dir", src)
	if code != 0 {
		t.Fatalf("export exit code = %d, out=%s", code, out)
	}

	dst := t.TempDir()
	out, code = runMainHelper(t, "deploy", packagePath, "codex", "--config-dir", dst, "--dry-run")
	if code != 0 {
		t.Fatalf("deploy package codex dry-run exit code = %d, out=%s", code, out)
	}
	if !strings.Contains(out, "Would import deploy config: "+packagePath+" -> "+dst) {
		t.Fatalf("deploy should import the explicit package:\n%s", out)
	}
	if !strings.Contains(out, "curl -fsSL https://chatgpt.com/codex/install.sh | sh") {
		t.Fatalf("codex install command missing:\n%s", out)
	}
	if strings.Contains(out, "claude.ai/install.sh") || strings.Contains(out, "@google/gemini-cli") {
		t.Fatalf("explicit codex deploy should not include other agents:\n%s", out)
	}
	if !strings.Contains(out, "Would apply takeover: codex") {
		t.Fatalf("deploy package codex should include codex takeover:\n%s", out)
	}
}

func TestInstallAliasInstallsOnlySelectedAgent(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	out, code := runMainHelper(t, "install", "codex", "--dry-run")
	if code != 0 {
		t.Fatalf("install codex dry-run exit code = %d, out=%s", code, out)
	}
	if !strings.Contains(out, "curl -fsSL https://chatgpt.com/codex/install.sh | sh") {
		t.Fatalf("codex install command missing:\n%s", out)
	}
	if strings.Contains(out, "Would apply takeover") {
		t.Fatalf("install alias should not apply takeover:\n%s", out)
	}
}

func TestDeployHelpShowsAgentCommands(t *testing.T) {
	out, code := runMainHelper(t, "deploy", "--help")
	if code != 0 {
		t.Fatalf("deploy help exit code = %d, out=%s", code, out)
	}
	for _, want := range []string{
		"clipal deploy codex",
		"clipal install codex",
		"clipal deploy prod.json codex",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("deploy help missing %q:\n%s", want, out)
		}
	}
}

func TestDeployImportsDefaultPackageFromCurrentDirectory(t *testing.T) {
	src := t.TempDir()
	writeMainConfig(t, src, 3333, "")

	workDir := t.TempDir()
	out, code := runMainHelper(t, "export", "--config-dir", src, "--output-dir", workDir)
	if code != 0 {
		t.Fatalf("export exit code = %d, out=%s", code, out)
	}

	dst := t.TempDir()
	out, code = runMainHelperInDir(t, workDir, "deploy", "--config-dir", dst, "--skip-install", "--skip-takeover")
	if code != 0 {
		t.Fatalf("deploy exit code = %d, out=%s", code, out)
	}
	if !strings.Contains(out, "Imported deploy config:") {
		t.Fatalf("deploy should import default package:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dst, "openai.yaml")); err != nil {
		t.Fatalf("deploy should restore openai.yaml: %v", err)
	}
}

func extractOutputValue(t *testing.T, out, prefix string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	t.Fatalf("missing output prefix %q in:\n%s", prefix, out)
	return ""
}

func TestMainServiceHelpShowsUsage(t *testing.T) {
	out, code := runMainHelper(t, "service", "--help")
	if code != 0 {
		t.Fatalf("exit code = %d, out=%s", code, out)
	}
	if !strings.Contains(out, "usage: clipal service") || !strings.Contains(out, "clipal service restart") {
		t.Fatalf("unexpected help output: %s", out)
	}
}

func TestMainUnknownCommandShowsUsage(t *testing.T) {
	out, code := runMainHelper(t, "restart-now")
	if code != 2 {
		t.Fatalf("exit code = %d, out=%s", code, out)
	}
	if !strings.Contains(out, `clipal: unknown command "restart-now"`) {
		t.Fatalf("unexpected error output: %s", out)
	}
	if !strings.Contains(out, "clipal restart") {
		t.Fatalf("expected usage hint in output: %s", out)
	}
}

func TestRenderUpdateResultOutput(t *testing.T) {
	t.Parallel()

	plan := &updatePlan{
		CurrentVersion:  "v0.11.0",
		LatestVersion:   "v0.11.1",
		ExecutablePath:  "/tmp/clipal",
		BinaryAssetName: "clipal-darwin-arm64",
		ChecksumsName:   "checksums.txt",
		DownloadURL:     "https://example.com/clipal",
	}

	t.Run("UpdatedIncludesRestartHint", func(t *testing.T) {
		t.Parallel()
		out := renderUpdateResultOutput(plan, updateResultOptions{
			Updated: true,
			GOOS:    "darwin",
		})
		if !strings.Contains(out, "updated: v0.11.0 -> v0.11.1") {
			t.Fatalf("missing update line: %s", out)
		}
		if !strings.Contains(out, "clipal restart") {
			t.Fatalf("missing restart hint: %s", out)
		}
	})

	t.Run("WindowsScheduledIncludesRestartHint", func(t *testing.T) {
		t.Parallel()
		out := renderUpdateResultOutput(plan, updateResultOptions{
			Updated: true,
			GOOS:    "windows",
		})
		if !strings.Contains(out, "update scheduled: v0.11.0 -> v0.11.1") {
			t.Fatalf("missing scheduled line: %s", out)
		}
		if !strings.Contains(out, "clipal restart") {
			t.Fatalf("missing restart hint: %s", out)
		}
	})

	t.Run("UpToDateHasNoRestartHint", func(t *testing.T) {
		t.Parallel()
		out := renderUpdateResultOutput(plan, updateResultOptions{
			Updated: false,
			GOOS:    "darwin",
		})
		if !strings.Contains(out, "up to date: v0.11.0") {
			t.Fatalf("missing up-to-date line: %s", out)
		}
		if strings.Contains(out, "clipal restart") {
			t.Fatalf("unexpected restart hint: %s", out)
		}
	})
}

func TestMainConfigLoadFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("unknown_field: true\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	out, code := runMainHelper(t, "--config-dir", dir)
	if code != 1 {
		t.Fatalf("exit code = %d, out=%s", code, out)
	}
	if !strings.Contains(out, "Error loading configuration:") {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestMainConfigValidationFailure(t *testing.T) {
	dir := t.TempDir()
	writeMainConfig(t, dir, 3333, `listen_addr: "127.0.0.1"
port: 0
log_level: "info"
reactivate_after: "1h"
upstream_idle_timeout: "3m"
response_header_timeout: "2m"
max_request_body_bytes: 33554432
log_dir: ""
log_retention_days: 7
log_stdout: true
notifications:
  enabled: false
  min_level: "error"
  provider_switch: true
circuit_breaker:
  failure_threshold: 4
  success_threshold: 2
  open_timeout: "60s"
  half_open_max_inflight: 1
ignore_count_tokens_failover: false
`)

	out, code := runMainHelper(t, "--config-dir", dir)
	if code != 1 {
		t.Fatalf("exit code = %d, out=%s", code, out)
	}
	if !strings.Contains(out, "Invalid configuration:") {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestMainSignalShutdownPath(t *testing.T) {
	port := freeTCPPort(t)
	dir := t.TempDir()
	writeMainConfig(t, dir, port, "")

	//nolint:gosec // Tests intentionally re-exec the current test binary as a helper process.
	cmd := exec.Command(os.Args[0], "-test.run=TestMainHelperProcess")
	cmd.Env = append(os.Environ(),
		"CLIPAL_MAIN_HELPER=1",
		"CLIPAL_MAIN_ARGS="+strings.Join([]string{"--config-dir", dir}, "\n"),
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForPort(t, port, 5*time.Second)

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("Signal: %v", err)
	}

	err := cmd.Wait()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			t.Fatalf("exit code = %d, out=%s", exitErr.ExitCode(), out.String())
		}
		t.Fatalf("CombinedOutput: %v", err)
	}
	if !strings.Contains(out.String(), "received signal terminated, shutting down") {
		t.Fatalf("unexpected output: %s", out.String())
	}
}
