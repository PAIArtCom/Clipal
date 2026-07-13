package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/lansespirit/Clipal/internal/app"
	"github.com/lansespirit/Clipal/internal/config"
	"github.com/lansespirit/Clipal/internal/selfupdate"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

type rootCommand string

const (
	rootCommandRun         rootCommand = "run"
	rootCommandHelp        rootCommand = "help"
	rootCommandUpdate      rootCommand = "update"
	rootCommandStatus      rootCommand = "status"
	rootCommandService     rootCommand = "service"
	rootCommandExport      rootCommand = "export"
	rootCommandImport      rootCommand = "import"
	rootCommandInit        rootCommand = "init"
	rootCommandApplyUpdate rootCommand = "__apply-update"
)

type updatePlan struct {
	CurrentVersion  string
	LatestVersion   string
	ExecutablePath  string
	BinaryAssetName string
	ChecksumsName   string
	DownloadURL     string
}

type updateResultOptions struct {
	Check   bool
	DryRun  bool
	Updated bool
	GOOS    string
}

func main() {
	cmd, args, err := resolveRootCommand(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "clipal: %v\n\n", err)
		printRootUsage(os.Stderr)
		os.Exit(2)
	}

	switch cmd {
	case rootCommandHelp:
		printRootUsage(os.Stdout)
		return
	case rootCommandUpdate:
		runUpdate(args)
		return
	case rootCommandStatus:
		runStatus(args)
		return
	case rootCommandService:
		runService(args)
		return
	case rootCommandExport:
		if err := runDataExport(args, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "clipal export: %v\n", err)
			os.Exit(1)
		}
		return
	case rootCommandImport:
		if err := runDataImport(args, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "clipal import: %v\n", err)
			os.Exit(1)
		}
		return
	case rootCommandInit:
		runInit(args)
		return
	case rootCommandApplyUpdate:
		runApplyUpdate(args)
		return
	case rootCommandRun:
		runServer(args)
		return
	default:
		fmt.Fprintf(os.Stderr, "clipal: unsupported command %q\n", cmd)
		os.Exit(2)
	}
}

func resolveRootCommand(args []string) (rootCommand, []string, error) {
	if len(args) == 0 {
		return rootCommandRun, nil, nil
	}

	switch args[0] {
	case "help", "-h", "--help":
		return rootCommandHelp, nil, nil
	case "update":
		return rootCommandUpdate, args[1:], nil
	case "status":
		return rootCommandStatus, args[1:], nil
	case "service":
		return rootCommandService, args[1:], nil
	case "export":
		return rootCommandExport, args[1:], nil
	case "import":
		return rootCommandImport, args[1:], nil
	case "init":
		return rootCommandInit, args[1:], nil
	case "__apply-update":
		return rootCommandApplyUpdate, args[1:], nil
	case "restart":
		return rootCommandService, append([]string{"restart"}, args[1:]...), nil
	}

	if strings.HasPrefix(args[0], "-") {
		return rootCommandRun, args, nil
	}
	return "", nil, fmt.Errorf("unknown command %q", args[0])
}

func printRootUsage(w io.Writer) {
	fmt.Fprintln(w, "usage:")
	fmt.Fprintln(w, "  clipal [flags]")
	fmt.Fprintln(w, "  clipal <command> [args]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  status            Show runtime and service status without starting the server")
	fmt.Fprintln(w, "  service           Install and manage the background service")
	fmt.Fprintln(w, "  update            Check for updates or replace the current binary in place")
	fmt.Fprintln(w, "  export            Export a complete clipal.data/v1 backup")
	fmt.Fprintln(w, "  import            Preview and import Clipal or external credential data")
	fmt.Fprintln(w, "  init              Bootstrap selected AI CLIs and apply safe local takeover")
	fmt.Fprintln(w, "  restart           Shortcut for 'clipal service restart'")
	fmt.Fprintln(w, "  help              Show this help")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  -config-dir string")
	fmt.Fprintln(w, "        Configuration directory (default: ~/.clipal)")
	fmt.Fprintln(w, "  -detach-console")
	fmt.Fprintln(w, "        Windows: detach console window (used by Task Scheduler)")
	fmt.Fprintln(w, "  -listen-addr string")
	fmt.Fprintln(w, "        Override listen address from config (default: 127.0.0.1)")
	fmt.Fprintln(w, "  -allow-remote-proxy")
	fmt.Fprintln(w, "        Allow proxy requests from non-loopback clients")
	fmt.Fprintln(w, "  -allow-remote-web-ui")
	fmt.Fprintln(w, "        Allow Web UI requests from non-loopback clients")
	fmt.Fprintln(w, "  -log-level string")
	fmt.Fprintln(w, "        Override log level (debug/info/warn/error)")
	fmt.Fprintln(w, "  -port int")
	fmt.Fprintln(w, "        Override port from config")
	fmt.Fprintln(w, "  -version")
	fmt.Fprintln(w, "        Show version information")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Examples:")
	fmt.Fprintln(w, "  clipal")
	fmt.Fprintln(w, "  clipal status")
	fmt.Fprintln(w, "  clipal restart")
	fmt.Fprintln(w, "  clipal service install")
	fmt.Fprintln(w, "  clipal update")
}

func runServer(args []string) {
	fs := flag.NewFlagSet("clipal", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printRootUsage(os.Stderr)
	}

	configDir := fs.String("config-dir", "", "Configuration directory (default: ~/.clipal)")
	listenAddr := fs.String("listen-addr", "", "Override listen address from config (default: 127.0.0.1)")
	allowRemoteProxy := fs.Bool("allow-remote-proxy", false, "Allow proxy requests from non-loopback clients")
	allowRemoteWebUI := fs.Bool("allow-remote-web-ui", false, "Allow Web UI requests from non-loopback clients")
	port := fs.Int("port", 0, "Override port from config")
	logLevel := fs.String("log-level", "", "Override log level (debug/info/warn/error)")
	detachConsole := fs.Bool("detach-console", false, "Windows: detach console window (used by Task Scheduler)")
	showVersion := fs.Bool("version", false, "Show version information")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printRootUsage(os.Stdout)
			return
		}
		os.Exit(2)
	}

	if extra := fs.Args(); len(extra) > 0 {
		fmt.Fprintf(os.Stderr, "clipal: unexpected argument %q\n\n", extra[0])
		printRootUsage(os.Stderr)
		os.Exit(2)
	}

	if *showVersion {
		fmt.Printf("clipal %s (commit: %s, built: %s)\n", version, commit, date)
		os.Exit(0)
	}

	maybeDetachConsole(*detachConsole)

	// Determine config directory
	cfgDir := *configDir
	if cfgDir == "" {
		cfgDir = config.GetConfigDir()
	}

	// Load configuration
	cfg, err := config.Load(cfgDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
		os.Exit(1)
	}

	// Apply command line overrides
	if *listenAddr != "" {
		cfg.Global.ListenAddr = *listenAddr
	}
	if *allowRemoteProxy {
		cfg.Global.AllowRemoteProxy = true
	}
	if *allowRemoteWebUI {
		cfg.Global.AllowRemoteWebUI = true
	}
	if *port > 0 {
		cfg.Global.Port = *port
	}
	if *logLevel != "" {
		cfg.Global.LogLevel = config.LogLevel(*logLevel)
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Invalid configuration: %v\n", err)
		os.Exit(1)
	}

	application, err := app.New(cfgDir, cfg, app.BuildInfo{
		Version: version,
		Commit:  commit,
		Date:    date,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "clipal failed to initialize: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = application.Shutdown(context.Background()) }()

	// Handle shutdown signals
	errCh := make(chan error, 1)
	go func() {
		errCh <- application.Start()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		application.LogSignalShutdown(sig.String())
		fmt.Fprintf(os.Stderr, "clipal: received signal %s, shutting down...\n", sig.String())
		if err := application.Shutdown(context.Background()); err != nil {
			application.LogShutdownFailure(err)
			fmt.Fprintf(os.Stderr, "clipal: graceful shutdown failed: %v\n", err)
		}
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			application.LogServerError(err)
			fmt.Fprintf(os.Stderr, "clipal: server stopped with error: %v\n", err)
			os.Exit(1)
		}
	}
	application.LogStopped()
}

func runUpdate(args []string) {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	check := fs.Bool("check", false, "Check for updates only")
	force := fs.Bool("force", false, "Force update (allow reinstall/downgrade)")
	dryRun := fs.Bool("dry-run", false, "Show what would be downloaded and replaced")
	timeout := fs.Duration("timeout", 2*time.Minute, "Overall update timeout")
	relaunch := fs.Bool("relaunch", false, "Windows: relaunch clipal after updating")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	opts := selfupdate.Options{
		Check:    *check,
		Force:    *force,
		DryRun:   *dryRun,
		Timeout:  *timeout,
		Relaunch: *relaunch,
	}

	plan, needsOrUpdated, err := selfupdate.Update(context.Background(), version, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "clipal update failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Print(renderUpdateResultOutput(updatePlanFromSelfUpdate(plan), updateResultOptions{
		Check:   *check,
		DryRun:  *dryRun,
		Updated: needsOrUpdated,
		GOOS:    runtime.GOOS,
	}))
}

func updatePlanFromSelfUpdate(plan *selfupdate.Plan) *updatePlan {
	if plan == nil {
		return &updatePlan{}
	}
	return &updatePlan{
		CurrentVersion:  plan.CurrentVersion,
		LatestVersion:   plan.LatestVersion,
		ExecutablePath:  plan.ExecutablePath,
		BinaryAssetName: plan.BinaryAsset.Name,
		ChecksumsName:   plan.ChecksumsAsset.Name,
		DownloadURL:     plan.BinaryAsset.BrowserDownloadURL,
	}
}

func renderUpdateResultOutput(plan *updatePlan, opts updateResultOptions) string {
	if plan == nil {
		plan = &updatePlan{}
	}

	var b strings.Builder
	writeLine := func(format string, args ...any) {
		_, _ = fmt.Fprintf(&b, format+"\n", args...)
	}

	if opts.Check {
		if opts.Updated {
			writeLine("update available: %s -> %s", plan.CurrentVersion, plan.LatestVersion)
		} else {
			writeLine("up to date: %s", plan.CurrentVersion)
		}
		return b.String()
	}

	if opts.DryRun {
		writeLine("current: %s", plan.CurrentVersion)
		writeLine("latest: %s", plan.LatestVersion)
		writeLine("exe: %s", plan.ExecutablePath)
		writeLine("asset: %s", plan.BinaryAssetName)
		writeLine("checksums: %s", plan.ChecksumsName)
		writeLine("download: %s", plan.DownloadURL)
		return b.String()
	}

	if opts.Updated {
		if opts.GOOS == "windows" {
			writeLine("update scheduled: %s -> %s", plan.CurrentVersion, plan.LatestVersion)
			writeLine("note: this process will exit so the updater can replace %s", plan.ExecutablePath)
		} else {
			writeLine("updated: %s -> %s", plan.CurrentVersion, plan.LatestVersion)
		}
		writeLine("next: if Clipal runs as a background service, restart it to load the new binary: clipal restart")
		writeLine("      canonical form: clipal service restart")
		return b.String()
	}

	writeLine("up to date: %s", plan.CurrentVersion)
	return b.String()
}

func runApplyUpdate(args []string) {
	fs := flag.NewFlagSet("__apply-update", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	pid := fs.Int("pid", 0, "PID to wait for before replacing")
	src := fs.String("src", "", "Downloaded update binary path")
	dst := fs.String("dst", "", "Target executable path to replace")
	helper := fs.String("helper", "", "Helper executable path to delete after update")
	relaunch := fs.Bool("relaunch", false, "Relaunch updated binary after replacing")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	err := selfupdate.ApplyUpdateWindows(selfupdate.ApplyUpdateOptions{
		PID:      *pid,
		Src:      *src,
		Dst:      *dst,
		Helper:   *helper,
		Relaunch: *relaunch,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "clipal: apply update failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stdout, "clipal: update applied")
}
