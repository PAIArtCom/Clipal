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

	//nolint:gosec // Tests intentionally re-exec the current test binary as a helper process.
	cmd := exec.Command(os.Args[0], "-test.run=TestMainHelperProcess")
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
			name:     "ExportCommandPassesThrough",
			args:     []string{"export", "-o", "backup.json"},
			wantCmd:  rootCommandExport,
			wantArgs: []string{"-o", "backup.json"},
		},
		{
			name:     "ImportCommandPassesThrough",
			args:     []string{"import", "--dry-run", "backup.json"},
			wantCmd:  rootCommandImport,
			wantArgs: []string{"--dry-run", "backup.json"},
		},
		{
			name:     "InitCommandPassesThrough",
			args:     []string{"init", "--dry-run"},
			wantCmd:  rootCommandInit,
			wantArgs: []string{"--dry-run"},
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
	if !strings.Contains(out, "Commands:") || !strings.Contains(out, "clipal restart") {
		t.Fatalf("unexpected help output: %s", out)
	}
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
