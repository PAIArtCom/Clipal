package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lansespirit/Clipal/internal/config"
	"github.com/lansespirit/Clipal/internal/transfer"
	"gopkg.in/yaml.v3"
)

func TestDataExportWritesCanonicalPrivateFile(t *testing.T) {
	dir := t.TempDir()
	writeCLITransferConfig(t, dir)
	output := filepath.Join(t.TempDir(), "backup.json")
	var stdout bytes.Buffer
	if err := runDataExport([]string{"--config-dir", dir, "-o", output}, &stdout); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"schema": "clipal.data"`)) {
		t.Fatalf("unexpected export: %s", data)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
}

func TestDataImportDryRunDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	writeCLITransferConfig(t, dir)
	input := filepath.Join(t.TempDir(), "credential.json")
	if err := os.WriteFile(input, []byte(`{"type":"codex","email":"cli@example.com","access_token":"token"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := runDataImport([]string{"--config-dir", dir, "--dry-run", input}, strings.NewReader(""), &stdout); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"format": "cliproxyapi"`) {
		t.Fatalf("unexpected preview: %s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "oauth")); !os.IsNotExist(err) {
		t.Fatalf("dry run wrote credentials: %v", err)
	}
}

func TestDataTransferHelpReturnsSuccess(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*bytes.Buffer) error
		want string
	}{
		{name: "export", run: func(out *bytes.Buffer) error { return runDataExport([]string{"--help"}, out) }, want: "usage: clipal export"},
		{name: "import", run: func(out *bytes.Buffer) error { return runDataImport([]string{"--help"}, strings.NewReader(""), out) }, want: "usage: clipal import"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := tc.run(&out); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), tc.want) {
				t.Fatalf("help=%q", out.String())
			}
			if !strings.Contains(out.String(), "--config-dir") {
				t.Fatalf("help does not list flags: %q", out.String())
			}
		})
	}
}

func TestDataImportRejectsTooManyFilesBeforeReading(t *testing.T) {
	args := []string{"--dry-run"}
	for i := 0; i < transfer.MaxImportFiles+1; i++ {
		args = append(args, fmt.Sprintf("missing-%d.json", i))
	}
	err := runDataImport(args, strings.NewReader(""), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "too many import files") {
		t.Fatalf("error=%v", err)
	}
}

func TestDataImportDelegatesToRunningInstanceForSameConfigDir(t *testing.T) {
	dir := t.TempDir()
	writeCLITransferConfig(t, dir)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address type = %T", listener.Addr())
	}
	port := tcpAddr.Port
	global := config.DefaultGlobalConfig()
	global.ListenAddr = "127.0.0.1"
	global.Port = port
	data, _ := yaml.Marshal(global)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	var previewed, applied, exported bool
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"config_dir": dir})
		case "/api/data/import/preview":
			previewed = true
			var req dataImportAPIRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if len(req.Files) != 1 || req.Files[0].Data != `{"type":"codex","access_token":"token"}` {
				t.Errorf("unexpected preview payload: %#v", req)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "plan", "format": "cliproxyapi", "mode": "merge", "credentials": 1})
		case "/api/data/import/apply":
			applied = true
			var req dataImportAPIRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.PlanID != "plan" {
				t.Errorf("plan_id=%q", req.PlanID)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"plan_id": "plan", "mode": "merge", "credentials": 1})
		case "/api/data/export":
			exported = true
			_, _ = w.Write([]byte(`{"schema":"clipal.data","schema_version":1}`))
		default:
			http.NotFound(w, r)
		}
	})}
	go func() { _ = server.Serve(listener) }()
	defer func() { _ = server.Close() }()

	input := filepath.Join(t.TempDir(), "credential.json")
	if err := os.WriteFile(input, []byte(`{"type":"codex","access_token":"token"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := runDataImport([]string{"--config-dir", dir, "--yes", input}, strings.NewReader(""), &stdout); err != nil {
		t.Fatal(err)
	}
	if !previewed || !applied {
		t.Fatalf("previewed=%v applied=%v", previewed, applied)
	}
	if _, err := os.Stat(filepath.Join(dir, "oauth")); !os.IsNotExist(err) {
		t.Fatalf("CLI should not apply locally while daemon is running: %v", err)
	}
	stdout.Reset()
	if err := runDataExport([]string{"--config-dir", dir, "--output", "-"}, &stdout); err != nil {
		t.Fatal(err)
	}
	if !exported || !strings.Contains(stdout.String(), `"schema":"clipal.data"`) {
		t.Fatalf("exported=%v output=%q", exported, stdout.String())
	}
}

func TestRunningTransferEndpointRefusesUnsafeLocalFallback(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   any
	}{
		{name: "status-error", status: http.StatusInternalServerError, body: map[string]any{"error": "busy"}},
		{name: "different-config", status: http.StatusOK, body: map[string]any{"config_dir": "/different/config"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeCLITransferConfig(t, dir)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			tcpAddr, ok := listener.Addr().(*net.TCPAddr)
			if !ok {
				t.Fatalf("listener address type = %T", listener.Addr())
			}
			global := config.DefaultGlobalConfig()
			global.ListenAddr, global.Port = "127.0.0.1", tcpAddr.Port
			data, _ := yaml.Marshal(global)
			if err := os.WriteFile(filepath.Join(dir, "config.yaml"), data, 0o600); err != nil {
				t.Fatal(err)
			}
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(tc.body)
			})}
			go func() { _ = server.Serve(listener) }()
			defer func() { _ = server.Close() }()
			if _, running, err := runningTransferEndpoint(dir); err == nil || running {
				t.Fatalf("running=%v err=%v, want refusal", running, err)
			}
		})
	}
}

func writeCLITransferConfig(t *testing.T, dir string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		_ = listener.Close()
		t.Fatalf("listener address type = %T", listener.Addr())
	}
	port := tcpAddr.Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	global := config.DefaultGlobalConfig()
	global.ListenAddr = "127.0.0.1"
	global.Port = port
	values := map[string]any{
		"config.yaml": global,
		"claude.yaml": config.ClientConfig{Mode: config.ClientModeAuto},
		"openai.yaml": config.ClientConfig{Mode: config.ClientModeAuto},
		"gemini.yaml": config.ClientConfig{Mode: config.ClientModeAuto},
	}
	for name, value := range values {
		data, err := yaml.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
