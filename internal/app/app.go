package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/PAIArtCom/Clipal/internal/config"
	"github.com/PAIArtCom/Clipal/internal/logger"
	"github.com/PAIArtCom/Clipal/internal/notify"
	"github.com/PAIArtCom/Clipal/internal/proxy"
	"github.com/PAIArtCom/Clipal/internal/web"
)

var newRotatingFileWriterFunc = logger.NewRotatingFileWriter

// BuildInfo captures build metadata exposed by the application.
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

// Application owns the long-lived runtime for the proxy process.
type Application struct {
	buildInfo        BuildInfo
	router           *proxy.Router
	webHandler       *web.Handler
	stopRuntime      func() error
	shutdownNotifier func()
	stopOnce         sync.Once
	stopErr          error
}

// New constructs the long-lived application runtime from validated config.
func New(configDir string, cfg *config.Config, buildInfo BuildInfo) (*Application, error) {
	logger.SetLevel(cfg.Global.LogLevel)
	if err := configureFileLogging(configDir, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: log file setup failed: %v (logs will only go to stdout)\n", err)
		logger.Warn("log file setup failed: %v", err)
	}

	notify.Configure(cfg.Global.Notifications)
	logger.SetHook(notify.LogHook)

	router := proxy.NewRouter(cfg)
	return &Application{
		buildInfo:        buildInfo,
		router:           router,
		webHandler:       web.NewHandler(configDir, buildInfo.Version, router),
		stopRuntime:      router.Stop,
		shutdownNotifier: notify.Shutdown,
	}, nil
}

// Start begins serving proxy and management traffic.
func (a *Application) Start() error {
	return a.router.Start(a.buildInfo.Version, a.webHandler)
}

// LogSignalShutdown records the beginning of a signal-triggered shutdown.
func (a *Application) LogSignalShutdown(signalName string) {
	logger.Info("received signal %s, shutting down...", signalName)
}

// LogShutdownFailure records a graceful shutdown failure.
func (a *Application) LogShutdownFailure(err error) {
	if err == nil {
		return
	}
	logger.Warn("graceful shutdown failed: %v", err)
}

// LogServerError records an unexpected server exit.
func (a *Application) LogServerError(err error) {
	if err == nil {
		return
	}
	logger.Error("server stopped with error: %v", err)
}

// LogStopped records that the application has stopped.
func (a *Application) LogStopped() {
	logger.Info("clipal stopped")
}

// Shutdown stops long-lived runtime resources.
func (a *Application) Shutdown(_ context.Context) error {
	a.stopOnce.Do(func() {
		stopRuntime := a.stopRuntime
		if stopRuntime == nil && a.router != nil {
			stopRuntime = a.router.Stop
		}
		shutdownNotifier := a.shutdownNotifier
		if shutdownNotifier == nil {
			shutdownNotifier = notify.Shutdown
		}
		defer shutdownNotifier()
		if stopRuntime != nil {
			a.stopErr = stopRuntime()
		}
	})
	return a.stopErr
}

func configureFileLogging(cfgDir string, cfg *config.Config) error {
	logDir := strings.TrimSpace(cfg.Global.LogDir)
	if logDir == "" {
		logDir = filepath.Join(cfgDir, "logs")
	}

	retention := cfg.Global.LogRetentionDays
	if retention < 0 {
		retention = 7
	}

	w, err := newRotatingFileWriterFunc(logDir, "clipal", retention)
	if err != nil {
		return err
	}

	if cfg.Global.LogStdout == nil || *cfg.Global.LogStdout {
		logger.SetOutput(io.MultiWriter(w, os.Stdout))
	} else {
		logger.SetOutput(w)
	}
	return nil
}
