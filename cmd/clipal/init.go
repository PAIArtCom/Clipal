package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lansespirit/Clipal/internal/bootstrap"
	"github.com/lansespirit/Clipal/internal/config"
	"github.com/lansespirit/Clipal/internal/integration"
	"github.com/lansespirit/Clipal/internal/service"
)

func runInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { printInitUsage(os.Stderr) }
	toolsRaw := fs.String("tools", "", "Comma-separated tools: codex,claude,antigravity,gemini")
	configDir := fs.String("config-dir", "", "Configuration directory (default: ~/.clipal)")
	dryRun := fs.Bool("dry-run", false, "Print installation actions without executing them")
	skipInstall := fs.Bool("skip-install", false, "Do not install missing CLI binaries")
	skipTakeover := fs.Bool("skip-takeover", false, "Do not apply existing CLI takeover")
	importPath := fs.String("import", "", "Restore a clipal.data/v1 backup before server setup")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		os.Exit(2)
	}
	if len(fs.Args()) > 0 {
		fmt.Fprintf(os.Stderr, "clipal init: unexpected argument %q\n", fs.Args()[0])
		os.Exit(2)
	}
	tools, err := bootstrap.ParseTools(*toolsRaw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "clipal init: %v\n", err)
		os.Exit(2)
	}
	cfgDir := *configDir
	if cfgDir == "" {
		cfgDir = config.GetConfigDir()
	}
	if strings.TrimSpace(*importPath) != "" {
		importArgs := []string{"--config-dir", cfgDir, "--mode", "replace"}
		if *dryRun {
			importArgs = append(importArgs, "--dry-run")
		} else {
			importArgs = append(importArgs, "--yes")
		}
		importArgs = append(importArgs, *importPath)
		if err := runDataImport(importArgs, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "clipal init: import backup: %v\n", err)
			os.Exit(1)
		}
		if *dryRun {
			return
		}
		fmt.Println("backup: restored Clipal data; CLI takeover will be applied for this server user")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	setup := bootstrap.New()
	for _, tool := range tools {
		if *skipInstall {
			fmt.Printf("%s: installation skipped\n", tool)
			continue
		}
		result, err := setup.Ensure(ctx, tool, *dryRun)
		if err != nil {
			fmt.Fprintf(os.Stderr, "clipal init: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("%s: %s", result.Tool, result.Message)
		if result.Binary != "" {
			fmt.Printf(" (%s)", result.Binary)
		}
		fmt.Println()
	}
	if *dryRun {
		return
	}
	if err := ensureInitService(ctx, cfgDir); err != nil {
		fmt.Fprintf(os.Stderr, "clipal init: %v\n", err)
		os.Exit(1)
	}
	if *skipTakeover {
		return
	}
	cfg, err := config.Load(cfgDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "clipal init: load config: %v\n", err)
		os.Exit(1)
	}
	manager := integration.NewManager(cfgDir)
	for _, tool := range tools {
		product, ok := initIntegrationProduct(tool)
		if !ok {
			continue
		}
		result, err := manager.Apply(product, cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "clipal init: apply %s takeover: %v\n", tool, err)
			os.Exit(1)
		}
		fmt.Printf("%s: %s\n", tool, result.Message)
	}
	for _, tool := range tools {
		if tool == bootstrap.ToolAntigravity {
			fmt.Println("antigravity: installed; proxy takeover is pending the official agy configuration contract")
		}
	}
	fmt.Println("Management UI is localhost-only. From your computer: ssh -L 3333:127.0.0.1:3333 user@server")
}

func initIntegrationProduct(tool bootstrap.Tool) (integration.ProductID, bool) {
	switch tool {
	case bootstrap.ToolCodex:
		return integration.ProductCodexCLI, true
	case bootstrap.ToolClaude:
		return integration.ProductClaudeCode, true
	case bootstrap.ToolGemini:
		return integration.ProductGeminiCLI, true
	default:
		return "", false
	}
}

func ensureInitService(ctx context.Context, configDir string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("determine executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	opts := service.Options{ConfigDir: configDir, BinaryPath: exe}
	mgr := service.DefaultManager()
	installPlan, err := mgr.Plan(service.ActionInstall, opts)
	if err == nil {
		if _, err := service.ExecutePlan(ctx, installPlan, false); err != nil {
			return fmt.Errorf("service install: %w", err)
		}
	} else if !strings.Contains(err.Error(), "service already installed") {
		return fmt.Errorf("plan service install: %w", err)
	}
	for _, action := range []service.Action{service.ActionStart} {
		plan, err := mgr.Plan(action, opts)
		if err != nil {
			return fmt.Errorf("plan service %s: %w", action, err)
		}
		if _, err := service.ExecutePlan(ctx, plan, false); err != nil {
			return fmt.Errorf("service %s: %w", action, err)
		}
	}
	return nil
}

func printInitUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: clipal init [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Bootstrap selected AI CLIs once, start Clipal as a background service, and apply existing safe takeover.")
	fmt.Fprintln(w, "Antigravity is installed and login-ready; its proxy takeover is intentionally deferred until its official configuration contract is verified.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "examples:")
	fmt.Fprintln(w, "  clipal init")
	fmt.Fprintln(w, "  clipal init --tools codex,claude,antigravity")
	fmt.Fprintln(w, "  clipal init --import /tmp/clipal-data.json")
	fmt.Fprintln(w, "  clipal init --dry-run")
}
