package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PAIArtCom/Clipal/internal/config"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()
	fn()
	_ = w.Close()
	os.Stdout = old
	out := <-done
	_ = r.Close()
	return out
}

func writeStatusConfig(t *testing.T, dir string, port int) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(fmt.Sprintf("listen_addr: \"127.0.0.1\"\nport: %d\nlog_level: \"info\"\nreactivate_after: \"1h\"\nupstream_idle_timeout: \"3m\"\nresponse_header_timeout: \"2m\"\nmax_request_body_bytes: 33554432\nlog_dir: \"\"\nlog_retention_days: 7\nlog_stdout: true\nnotifications:\n  enabled: false\n  min_level: \"error\"\n  provider_switch: true\ncircuit_breaker:\n  failure_threshold: 4\n  success_threshold: 2\n  open_timeout: \"60s\"\n  half_open_max_inflight: 1\nignore_count_tokens_failover: false\n", port)), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestHealthCandidateURLs(t *testing.T) {
	tests := []struct {
		name string
		addr string
		want []string
	}{
		{"IPv4", "127.0.0.1", []string{"http://127.0.0.1:3333/health"}},
		{"AnyIPv4", "0.0.0.0", []string{"http://127.0.0.1:3333/health"}},
		{"AnyIPv6", "::", []string{"http://[::1]:3333/health"}},
		{"Localhost", "localhost", []string{"http://localhost:3333/health", "http://127.0.0.1:3333/health"}},
		{"Hostname", "example.local", []string{"http://example.local:3333/health", "http://127.0.0.1:3333/health", "http://localhost:3333/health"}},
		{"IPv6", "2001:db8::1", []string{"http://[2001:db8::1]:3333/health"}},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := healthCandidateURLs(tt.addr, 3333)
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
}

func TestCheckHealth(t *testing.T) {
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer okSrv.Close()

	badSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusServiceUnavailable)
	}))
	defer badSrv.Close()

	got := checkHealth(t.Context(), []string{okSrv.URL})
	if !got.OK || got.HTTPStatus != http.StatusOK {
		t.Fatalf("checkHealth OK = %#v", got)
	}

	got = checkHealth(t.Context(), []string{badSrv.URL})
	if got.OK || got.HTTPStatus != http.StatusServiceUnavailable {
		t.Fatalf("checkHealth non-200 = %#v", got)
	}

	got = checkHealth(t.Context(), []string{"http://127.0.0.1:1", okSrv.URL})
	if !got.OK || got.URL != okSrv.URL {
		t.Fatalf("checkHealth fallback = %#v", got)
	}
}

func TestPortLooksInUse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() {
		if err := ln.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("unexpected addr type %T", ln.Addr())
	}
	port := tcpAddr.Port
	if !portLooksInUse(t.Context(), port) {
		t.Fatalf("expected port %d to be in use", port)
	}

	ln2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	tcpAddr2, ok := ln2.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("unexpected addr type %T", ln2.Addr())
	}
	freePort := tcpAddr2.Port
	if err := ln2.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if portLooksInUse(t.Context(), freePort) {
		t.Fatalf("expected port %d to be free", freePort)
	}
}

func TestSummarizeProvidersAndPrintStatusReport(t *testing.T) {
	cc := config.ClientConfig{
		Providers: []config.Provider{
			{Name: "p1", BaseURL: "https://a.example", APIKey: "k1", Priority: 1},
			{Name: "p2", BaseURL: "https://b.example", APIKey: "k2", Priority: 2, Enabled: func() *bool { v := false; return &v }()},
		},
	}
	got := summarizeProviders("codex", cc)
	if got.Enabled != 1 || got.Active != "p1" {
		t.Fatalf("summarizeProviders = %#v", got)
	}

	out := captureStdout(t, func() {
		printStatusReport(statusReport{
			OK:        true,
			Summary:   "OK  Running",
			Version:   "v1.0.0",
			Commit:    "abc",
			Built:     "today",
			ConfigDir: "/tmp/clipal",
			Listen:    "127.0.0.1",
			Port:      3333,
			WebUI:     "http://localhost:3333/",
			Health:    healthStatus{OK: true, URL: "http://127.0.0.1:3333/health", HTTPStatus: 200},
			Providers: []providerStatus{got},
		})
	})
	for _, want := range []string{"OK  Running", "Health:", "Providers:", "OpenAI", "active: p1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in output:\n%s", want, out)
		}
	}
	if strings.Contains(out, "codex") {
		t.Fatalf("did not expect legacy display label in output:\n%s", out)
	}
}

func TestSummarizeProviders_ManualModeUsesPinnedProvider(t *testing.T) {
	cc := config.ClientConfig{
		Mode:           config.ClientModeManual,
		PinnedProvider: "p2",
		Providers: []config.Provider{
			{Name: "p1", BaseURL: "https://a.example", APIKey: "k1", Priority: 1},
			{Name: "p2", BaseURL: "https://b.example", APIKey: "k2", Priority: 2},
		},
	}
	got := summarizeProviders("openai", cc)
	if got.Enabled != 2 || got.Active != "p2" {
		t.Fatalf("summarizeProviders = %#v, want enabled=2 active=p2", got)
	}
}

func TestRunStatusHelperProcess(t *testing.T) {
	if os.Getenv("CLIPAL_STATUS_HELPER") == "1" {
		runStatus([]string{"--config-dir", os.Getenv("CLIPAL_STATUS_CONFIG_DIR"), "--json", "--no-service"})
		os.Exit(0)
	}

	run := func(t *testing.T, dir string) (string, int) {
		t.Helper()
		//nolint:gosec // Tests intentionally re-exec the current test binary as a helper process.
		cmd := exec.Command(os.Args[0], "-test.run=TestRunStatusHelperProcess")
		cmd.Env = append(os.Environ(),
			"CLIPAL_STATUS_HELPER=1",
			"CLIPAL_STATUS_CONFIG_DIR="+dir,
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

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"healthy"}`))
	}))
	defer srv.Close()
	tcpAddr, ok := srv.Listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("unexpected addr type %T", srv.Listener.Addr())
	}
	port := tcpAddr.Port

	okDir := t.TempDir()
	writeStatusConfig(t, okDir, port)
	out, code := run(t, okDir)
	if code != 0 {
		t.Fatalf("success exit code = %d, out=%s", code, out)
	}
	var okReport map[string]any
	if err := json.Unmarshal([]byte(out), &okReport); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out)
	}
	if okReport["ok"] != true {
		t.Fatalf("expected ok report, got %#v", okReport)
	}

	failDir := t.TempDir()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	tcpAddrFail, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("unexpected addr type %T", ln.Addr())
	}
	failPort := tcpAddrFail.Port
	if err := ln.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	writeStatusConfig(t, failDir, failPort)
	out, code = run(t, failDir)
	if code != 1 {
		t.Fatalf("failure exit code = %d, out=%s", code, out)
	}
	var failReport map[string]any
	if err := json.Unmarshal([]byte(out), &failReport); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out)
	}
	if failReport["ok"] != false {
		t.Fatalf("expected failed report, got %#v", failReport)
	}
}
