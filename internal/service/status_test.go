package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatusMarshalJSON_StableShape(t *testing.T) {
	st := Status{
		Manager:    "launchd",
		Name:       "clipal",
		Scope:      "gui/501",
		Installed:  true,
		Loaded:     true,
		Running:    true,
		PID:        123,
		BinaryPath: "/bin/clipal",
		ConfigDir:  "/tmp/clipal",
		StdoutPath: "/tmp/stdout.log",
		StderrPath: "/tmp/stderr.log",
		LastExit:   "0",
		Detail:     "state=running",
	}

	got, err := st.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	for _, key := range []string{
		"manager", "name", "scope", "installed", "loaded", "running",
		"pid", "binary_path", "config_dir", "stdout_path", "stderr_path", "last_exit", "detail",
	} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("expected key %q in %s", key, string(got))
		}
	}
}

func TestFileExists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.txt")
	if fileExists(path) {
		t.Fatalf("expected missing file to report false")
	}
	if err := os.WriteFile(path, []byte("ok"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if !fileExists(path) {
		t.Fatalf("expected existing file to report true")
	}
}

func TestParseLaunchdStatus(t *testing.T) {
	st := parseLaunchdStatus(Status{
		Manager:    "launchd",
		Name:       "com.lansespirit.clipal",
		BinaryPath: "/fallback/clipal",
	}, `
state = running
pid = 4321
program = /usr/local/bin/clipal
stdout path = /tmp/clipal.out
stderr path = /tmp/clipal.err
last exit reason = 78
`)

	if !st.Loaded || !st.Running || st.PID != 4321 {
		t.Fatalf("status = %#v", st)
	}
	if st.Detail != "state=running" {
		t.Fatalf("detail = %q, want state=running", st.Detail)
	}
	if st.BinaryPath != "/usr/local/bin/clipal" {
		t.Fatalf("binary_path = %q", st.BinaryPath)
	}
	if st.StdoutPath != "/tmp/clipal.out" || st.StderrPath != "/tmp/clipal.err" {
		t.Fatalf("stdout/stderr = %q %q", st.StdoutPath, st.StderrPath)
	}
	if st.LastExit != "78" {
		t.Fatalf("last_exit = %q, want 78", st.LastExit)
	}
}

func TestParseLaunchdStatus_ToleratesMissingAndInvalidFields(t *testing.T) {
	st := parseLaunchdStatus(Status{Manager: "launchd"}, "state = waiting\npid = not-a-number\n")
	if !st.Loaded {
		t.Fatalf("expected loaded")
	}
	if st.Running {
		t.Fatalf("did not expect running")
	}
	if st.PID != 0 {
		t.Fatalf("pid = %d, want 0", st.PID)
	}
	if st.Detail != "state=waiting" {
		t.Fatalf("detail = %q, want state=waiting", st.Detail)
	}
}

func TestParseSystemdShowStatus(t *testing.T) {
	st := parseSystemdShowStatus(Status{Manager: "systemd"}, strings.Join([]string{
		"LoadState=loaded",
		"ActiveState=active",
		"SubState=running",
		"MainPID=789",
		"ExecStart={ path=/usr/local/bin/clipal ; argv[]=/usr/local/bin/clipal --config-dir /tmp/clipal ; ignore_errors=no ; start_time=[n/a] ; stop_time=[n/a] ; pid=0 ; code=(null) ; status=0/0 }",
	}, "\n"))

	if !st.Loaded || !st.Running || st.PID != 789 {
		t.Fatalf("status = %#v", st)
	}
	if st.Detail != "active=active sub=running" {
		t.Fatalf("detail = %q", st.Detail)
	}
}

func TestParseSystemdShowStatus_ToleratesNotFoundAndBadPID(t *testing.T) {
	st := parseSystemdShowStatus(Status{Manager: "systemd"}, "LoadState=not-found\nActiveState=failed\nSubState=dead\nMainPID=oops\n")
	if st.Loaded {
		t.Fatalf("did not expect loaded")
	}
	if st.Running {
		t.Fatalf("did not expect running")
	}
	if st.PID != 0 {
		t.Fatalf("pid = %d, want 0", st.PID)
	}
	if st.Detail != "active=failed sub=dead" {
		t.Fatalf("detail = %q", st.Detail)
	}
}

func TestParseWindowsTaskStatus(t *testing.T) {
	st := parseWindowsTaskStatus(Status{Manager: "schtasks"}, "TaskName: \\Clipal\nStatus: Running\n")
	if !st.Loaded || !st.Running {
		t.Fatalf("status = %#v", st)
	}
	if st.Detail != "status=Running" {
		t.Fatalf("detail = %q", st.Detail)
	}
}

func TestParseWindowsTaskStatus_LocalizedChinese(t *testing.T) {
	st := parseWindowsTaskStatus(Status{Manager: "schtasks"}, "任务名: \\Clipal\n模式: 运行中\n")
	if !st.Loaded || !st.Running {
		t.Fatalf("status = %#v", st)
	}
	if st.Detail != "status=运行中" {
		t.Fatalf("detail = %q", st.Detail)
	}
}

func TestParseWindowsTaskStatus_ToleratesMissingStatus(t *testing.T) {
	st := parseWindowsTaskStatus(Status{Manager: "schtasks"}, "TaskName: \\Clipal\nNext Run Time: N/A\n")
	if !st.Loaded {
		t.Fatalf("expected loaded")
	}
	if st.Running {
		t.Fatalf("did not expect running")
	}
	if st.Detail != "" {
		t.Fatalf("detail = %q, want empty", st.Detail)
	}
}
