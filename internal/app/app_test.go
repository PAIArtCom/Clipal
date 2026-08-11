package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/PAIArtCom/Clipal/internal/config"
	"github.com/PAIArtCom/Clipal/internal/logger"
)

func captureFileDescriptor(t *testing.T, target **os.File, fn func()) string {
	t.Helper()

	old := *target
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	*target = w

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()

	_ = w.Close()
	*target = old
	got := <-done
	_ = r.Close()
	return got
}

func resetAppGlobals(t *testing.T) {
	t.Helper()
	logger.SetOutput(io.Discard)
	logger.SetHook(nil)
	logger.SetLevel(config.LogLevelInfo)
	newRotatingFileWriterFunc = logger.NewRotatingFileWriter
}

func TestNew_DegradesWhenConfigureFileLoggingFails(t *testing.T) {
	resetAppGlobals(t)
	defer resetAppGlobals(t)

	orig := newRotatingFileWriterFunc
	newRotatingFileWriterFunc = func(dir, prefix string, retention int) (*logger.RotatingFileWriter, error) {
		return nil, errors.New("boom")
	}
	defer func() { newRotatingFileWriterFunc = orig }()

	cfg := &config.Config{Global: config.DefaultGlobalConfig()}

	stderr := captureFileDescriptor(t, &os.Stderr, func() {
		app, err := New(t.TempDir(), cfg, BuildInfo{Version: "test"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if app == nil {
			t.Fatalf("expected application")
		}
	})

	if !strings.Contains(stderr, "log file setup failed") {
		t.Fatalf("expected warning on stderr, got %q", stderr)
	}
}

func TestConfigureFileLogging_DefaultDirAndStdoutToggle(t *testing.T) {
	resetAppGlobals(t)
	defer resetAppGlobals(t)

	cfgDir := t.TempDir()
	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	cfg.Global.LogDir = ""
	cfg.Global.LogRetentionDays = 0
	cfg.Global.LogStdout = func() *bool { v := true; return &v }()

	stdout := captureFileDescriptor(t, &os.Stdout, func() {
		if err := configureFileLogging(cfgDir, cfg); err != nil {
			t.Fatalf("configureFileLogging: %v", err)
		}
		logger.Info("stdout-visible")
	})

	logPath := filepath.Join(cfgDir, "logs")
	entries, err := os.ReadDir(logPath)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", logPath, err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one log file, got %d", len(entries))
	}
	logBytes, err := os.ReadFile(filepath.Join(logPath, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(logBytes), "stdout-visible") {
		t.Fatalf("expected file log to contain message, got %q", string(logBytes))
	}
	if !strings.Contains(stdout, "stdout-visible") {
		t.Fatalf("expected stdout to contain message, got %q", stdout)
	}

	resetAppGlobals(t)
	cfg2 := &config.Config{Global: config.DefaultGlobalConfig()}
	cfg2.Global.LogDir = ""
	cfg2.Global.LogStdout = func() *bool { v := false; return &v }()
	stdout2 := captureFileDescriptor(t, &os.Stdout, func() {
		if err := configureFileLogging(cfgDir, cfg2); err != nil {
			t.Fatalf("configureFileLogging: %v", err)
		}
		logger.Info("file-only")
	})
	if strings.Contains(stdout2, "file-only") {
		t.Fatalf("did not expect stdout to contain file-only log, got %q", stdout2)
	}
}

func TestConfigureFileLogging_PreservesZeroRetentionForKeepForever(t *testing.T) {
	resetAppGlobals(t)
	defer resetAppGlobals(t)

	var gotRetention int
	orig := newRotatingFileWriterFunc
	newRotatingFileWriterFunc = func(dir, prefix string, retention int) (*logger.RotatingFileWriter, error) {
		gotRetention = retention
		return orig(dir, prefix, retention)
	}
	defer func() { newRotatingFileWriterFunc = orig }()

	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	cfg.Global.LogRetentionDays = 0
	if err := configureFileLogging(t.TempDir(), cfg); err != nil {
		t.Fatalf("configureFileLogging: %v", err)
	}
	if gotRetention != 0 {
		t.Fatalf("retention = %d, want 0", gotRetention)
	}
}

func TestShutdown_IsIdempotent(t *testing.T) {
	resetAppGlobals(t)
	defer resetAppGlobals(t)

	var mu sync.Mutex
	stopCalls := 0
	notifyCalls := 0

	app := &Application{
		stopRuntime: func() error {
			mu.Lock()
			defer mu.Unlock()
			stopCalls++
			return nil
		},
		shutdownNotifier: func() {
			mu.Lock()
			defer mu.Unlock()
			notifyCalls++
		},
	}

	if err := app.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := app.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown second call: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if stopCalls != 1 {
		t.Fatalf("stopCalls = %d, want 1", stopCalls)
	}
	if notifyCalls != 1 {
		t.Fatalf("notifyCalls = %d, want 1", notifyCalls)
	}
}

func TestLifecycleLogMethods(t *testing.T) {
	resetAppGlobals(t)
	defer resetAppGlobals(t)

	type event struct {
		level string
		msg   string
	}
	var events []event
	logger.SetOutput(io.Discard)
	logger.SetHook(func(levelStr, message string) {
		events = append(events, event{level: levelStr, msg: message})
	})

	app := &Application{}
	app.LogSignalShutdown("SIGTERM")
	app.LogShutdownFailure(errors.New("shutdown failed"))
	app.LogServerError(errors.New("server failed"))
	app.LogStopped()

	want := []event{
		{level: "INFO", msg: "received signal SIGTERM, shutting down..."},
		{level: "WARN", msg: "graceful shutdown failed: shutdown failed"},
		{level: "ERROR", msg: "server stopped with error: server failed"},
		{level: "INFO", msg: "clipal stopped"},
	}
	if len(events) != len(want) {
		t.Fatalf("events = %#v", events)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events[%d] = %#v, want %#v", i, events[i], want[i])
		}
	}
}
