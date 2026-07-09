package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/lansespirit/Clipal/internal/agentinstall"
	"github.com/lansespirit/Clipal/internal/config"
	deploypkg "github.com/lansespirit/Clipal/internal/deploy"
	"github.com/lansespirit/Clipal/internal/integration"
)

const (
	defaultDeployPackageName = "clipal.json"
	deployPackageExt         = ".json"
)

func runDeploy(args []string) {
	if len(args) == 0 || (len(args) == 1 && isHelpToken(args[0])) {
		runDeploySetup(args)
		return
	}

	action := strings.TrimSpace(args[0])
	switch action {
	case "export":
		runDeployExport(args[1:])
	case "import":
		runDeployImport(args[1:])
	case "install":
		runDeploySetup(append([]string{"--skip-takeover"}, args[1:]...))
	default:
		runDeploySetup(args)
	}
}

func runDeploySetup(args []string) {
	fs := flag.NewFlagSet("deploy", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printDeployUsage(os.Stderr)
	}
	configDir := fs.String("config-dir", "", "Configuration directory to import into and use for takeover (default: ~/.clipal)")
	agentsRaw := fs.String("agents", "codex,claude,gemini", "Comma-separated agents to install and takeover")
	dryRun := fs.Bool("dry-run", false, "Print planned actions without changing the system")
	skipInstall := fs.Bool("skip-install", false, "Skip installing missing agent CLIs")
	skipTakeover := fs.Bool("skip-takeover", false, "Skip CLI takeover after install")
	positionals, flagArgs, err := splitDeploySetupArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "clipal deploy: %v\n", err)
		printDeployUsage(os.Stderr)
		os.Exit(2)
	}
	if err := fs.Parse(flagArgs); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printDeployUsage(os.Stdout)
			return
		}
		os.Exit(2)
	}
	agents, packagePath, err := resolveDeploySetupInputs(positionals, *agentsRaw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "clipal deploy: %v\n", err)
		os.Exit(2)
	}

	cfgDir := *configDir
	if cfgDir == "" {
		cfgDir = config.GetConfigDir()
	}

	if packagePath == "" {
		if _, err := os.Stat(defaultDeployPackageName); err == nil {
			packagePath = defaultDeployPackageName
		} else if err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "clipal deploy failed: stat %s: %v\n", defaultDeployPackageName, err)
			os.Exit(1)
		}
	}

	if packagePath != "" {
		if *dryRun {
			fmt.Fprintf(os.Stdout, "Would import deploy config: %s -> %s\n", packagePath, cfgDir)
		} else if err := deploypkg.ImportPackage(deploypkg.ImportOptions{Package: packagePath, ConfigDir: cfgDir}); err != nil {
			fmt.Fprintf(os.Stderr, "clipal deploy import failed: %v\n", err)
			os.Exit(1)
		} else {
			fmt.Fprintf(os.Stdout, "Imported deploy config: %s -> %s\n", packagePath, cfgDir)
		}
	}

	if !*skipInstall {
		if err := installAgents(agents, *dryRun); err != nil {
			fmt.Fprintf(os.Stderr, "clipal deploy install failed: %v\n", err)
			os.Exit(1)
		}
	}

	if !*skipTakeover {
		targets := make([]string, 0, len(agents))
		for _, agent := range agents {
			targets = append(targets, string(agent))
		}
		if *dryRun {
			fmt.Fprintf(os.Stdout, "Would apply takeover: %s\n", strings.Join(targets, ","))
		} else if err := applyTakeoverTargets(cfgDir, targets); err != nil {
			fmt.Fprintf(os.Stderr, "clipal deploy takeover failed: %v\n", err)
			os.Exit(1)
		}
	}
}

func splitDeploySetupArgs(args []string) (positionals []string, flagArgs []string, err error) {
	needsValue := map[string]bool{
		"config-dir": true,
		"agents":     true,
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flagArgs = append(flagArgs, a)
			name := strings.TrimLeft(a, "-")
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				name = name[:eq]
			}
			if needsValue[name] && !strings.Contains(a, "=") {
				if i+1 >= len(args) {
					return nil, nil, fmt.Errorf("flag %s requires a value", a)
				}
				flagArgs = append(flagArgs, args[i+1])
				i++
			}
			continue
		}
		positionals = append(positionals, a)
	}
	return positionals, flagArgs, nil
}

func resolveDeploySetupInputs(positionals []string, agentsRaw string) ([]agentinstall.AgentID, string, error) {
	if len(positionals) == 0 {
		agents, err := agentinstall.ParseAgents(agentsRaw)
		return agents, "", err
	}

	var packagePath string
	agentTokens := make([]string, 0, len(positionals))
	for _, positional := range positionals {
		if _, err := agentinstall.ParseAgent(positional); err == nil {
			agentTokens = append(agentTokens, positional)
			continue
		}
		if packagePath != "" {
			return nil, "", fmt.Errorf("unexpected argument %q", positional)
		}
		packagePath = positional
	}
	if len(agentTokens) == 0 {
		agents, err := agentinstall.ParseAgents(agentsRaw)
		return agents, packagePath, err
	}
	agents, err := agentinstall.ParseAgents(strings.Join(agentTokens, ","))
	if err != nil {
		return nil, "", err
	}
	return agents, packagePath, nil
}

func runDeployExport(args []string) {
	fs := flag.NewFlagSet("deploy export", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printDeployUsage(os.Stderr)
	}
	output := fs.String("o", "", "Output deploy package path (default: clipal.json)")
	outputDir := fs.String("output-dir", ".", "Directory for default output package")
	configDir := fs.String("config-dir", "", "Configuration directory to export (default: ~/.clipal)")
	takeover := fs.String("include-takeover", "", "Comma-separated takeover targets to include as metadata")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printDeployUsage(os.Stdout)
			return
		}
		os.Exit(2)
	}
	if extra := fs.Args(); len(extra) > 0 {
		fmt.Fprintf(os.Stderr, "clipal deploy export: unexpected argument %q\n", extra[0])
		printDeployUsage(os.Stderr)
		os.Exit(2)
	}

	cfgDir := *configDir
	if cfgDir == "" {
		cfgDir = config.GetConfigDir()
	}
	outputPath, err := normalizeDeployOutputPath(*output, *outputDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "clipal deploy export: %v\n", err)
		printDeployUsage(os.Stderr)
		os.Exit(2)
	}

	if err := deploypkg.ExportPackage(deploypkg.ExportOptions{
		ConfigDir:       cfgDir,
		Output:          outputPath,
		TakeoverTargets: splitCSV(*takeover),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "clipal deploy export failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stdout, "Deploy package written: %s\n", outputPath)
	fmt.Fprintln(os.Stdout, "Warning: this package contains provider URLs and API keys.")
}

func runDeployImport(args []string) {
	packagePath, flagArgs, err := splitDeployImportArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "clipal deploy import: %v\n", err)
		printDeployUsage(os.Stderr)
		os.Exit(2)
	}

	fs := flag.NewFlagSet("deploy import", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printDeployUsage(os.Stderr)
	}
	configDir := fs.String("config-dir", "", "Configuration directory to import into (default: ~/.clipal)")
	temporary := fs.Bool("temporary", false, "Import into an isolated temporary configuration directory")
	start := fs.Bool("start", false, "Print the command to start Clipal with the imported configuration")
	takeover := fs.String("takeover", "", "Comma-separated takeover targets to apply after import")
	if err := fs.Parse(flagArgs); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printDeployUsage(os.Stdout)
			return
		}
		os.Exit(2)
	}
	if extra := fs.Args(); len(extra) > 0 {
		fmt.Fprintf(os.Stderr, "clipal deploy import: unexpected argument %q\n", extra[0])
		printDeployUsage(os.Stderr)
		os.Exit(2)
	}
	if strings.TrimSpace(packagePath) == "" {
		fmt.Fprintln(os.Stderr, "clipal deploy import: package path is required")
		printDeployUsage(os.Stderr)
		os.Exit(2)
	}

	cfgDir := *configDir
	if *temporary {
		dir, err := os.MkdirTemp("", "clipal-deploy-*")
		if err != nil {
			fmt.Fprintf(os.Stderr, "clipal deploy import failed: create temporary config dir: %v\n", err)
			os.Exit(1)
		}
		cfgDir = dir
	} else if cfgDir == "" {
		cfgDir = config.GetConfigDir()
	}

	if err := deploypkg.ImportPackage(deploypkg.ImportOptions{
		Package:   packagePath,
		ConfigDir: cfgDir,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "clipal deploy import failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stdout, "Deploy package imported: %s\n", cfgDir)
	if *temporary {
		fmt.Fprintf(os.Stdout, "Temporary config dir: %s\n", cfgDir)
		fmt.Fprintf(os.Stdout, "Cleanup: rm -rf %s\n", cfgDir)
	}
	if targets := splitCSV(*takeover); len(targets) > 0 {
		if err := applyTakeoverTargets(cfgDir, targets); err != nil {
			fmt.Fprintf(os.Stderr, "clipal deploy import takeover failed: %v\n", err)
			os.Exit(1)
		}
	}
	if *start {
		fmt.Fprintf(os.Stdout, "Start: clipal --config-dir %s\n", cfgDir)
	}
}

func printDeployUsage(w io.Writer) {
	fmt.Fprintln(w, "usage:")
	fmt.Fprintln(w, "  clipal deploy [<package>] [<agent>...] [--dry-run]")
	fmt.Fprintln(w, "  clipal install [<agent>...] [--dry-run]")
	fmt.Fprintln(w, "  clipal deploy export [-o <package>] [--config-dir <dir>]")
	fmt.Fprintln(w, "  clipal deploy import <package> [--config-dir <dir>] [--temporary] [--start]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "agents:")
	fmt.Fprintln(w, "  codex, claude, gemini")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "examples:")
	fmt.Fprintln(w, "  clipal export")
	fmt.Fprintln(w, "  clipal export -o prod")
	fmt.Fprintln(w, "  clipal import clipal.json")
	fmt.Fprintln(w, "  clipal import prod.json --temporary --start")
	fmt.Fprintln(w, "  clipal deploy")
	fmt.Fprintln(w, "  clipal deploy codex")
	fmt.Fprintln(w, "  clipal deploy prod.json codex")
	fmt.Fprintln(w, "  clipal install codex")
}

func normalizeDeployOutputPath(output, outputDir string) (string, error) {
	output = strings.TrimSpace(output)
	if output == "" {
		dir := strings.TrimSpace(outputDir)
		if dir == "" {
			dir = "."
		}
		return filepath.Join(dir, defaultDeployPackageName), nil
	}
	if strings.HasSuffix(output, deployPackageExt) {
		return output, nil
	}
	if ext := filepath.Ext(output); ext != "" {
		return "", fmt.Errorf("deploy package path %q must use %s suffix", output, deployPackageExt)
	}
	return output + deployPackageExt, nil
}

func splitDeployImportArgs(args []string) (packagePath string, flagArgs []string, err error) {
	needsValue := map[string]bool{
		"config-dir": true,
		"takeover":   true,
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flagArgs = append(flagArgs, a)
			name := strings.TrimLeft(a, "-")
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				name = name[:eq]
			}
			if needsValue[name] && !strings.Contains(a, "=") {
				if i+1 >= len(args) {
					return "", nil, fmt.Errorf("flag %s requires a value", a)
				}
				flagArgs = append(flagArgs, args[i+1])
				i++
			}
			continue
		}
		if packagePath != "" {
			return "", nil, fmt.Errorf("unexpected argument %q", a)
		}
		packagePath = a
	}
	return packagePath, flagArgs, nil
}

func applyTakeoverTargets(configDir string, targets []string) error {
	cfg, err := config.Load(configDir)
	if err != nil {
		return fmt.Errorf("load imported config: %w", err)
	}
	mgr := integration.NewManager(configDir)
	for _, target := range targets {
		product, err := parseTakeoverTarget(target)
		if err != nil {
			return err
		}
		result, err := mgr.Apply(product, cfg)
		if err != nil {
			return fmt.Errorf("%s: %w", target, err)
		}
		fmt.Fprintf(os.Stdout, "Takeover applied: %s (%s)\n", result.Product, result.Message)
	}
	return nil
}

func installAgents(agents []agentinstall.AgentID, dryRun bool) error {
	for _, agent := range agents {
		plan, err := agentinstall.PlanForAgent(agent, runtime.GOOS)
		if err != nil {
			return err
		}
		if _, err := exec.LookPath(plan.CheckCommand); err == nil {
			fmt.Fprintf(os.Stdout, "Agent already installed: %s\n", agent)
			continue
		}
		if dryRun {
			fmt.Fprintf(os.Stdout, "Would install %s: %s\n", agent, shellCommandString(plan.InstallCommand))
			continue
		}
		if err := ensureInstallerAvailable(plan.InstallCommand[0]); err != nil {
			return fmt.Errorf("%s: %w", agent, err)
		}
		fmt.Fprintf(os.Stdout, "Installing %s: %s\n", agent, shellCommandString(plan.InstallCommand))
		cmd := exec.Command(plan.InstallCommand[0], plan.InstallCommand[1:]...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s install command failed: %w", agent, err)
		}
	}
	return nil
}

func ensureInstallerAvailable(command string) error {
	if _, err := exec.LookPath(command); err != nil {
		return fmt.Errorf("%s is required to run the official install command", command)
	}
	return nil
}

func parseTakeoverTarget(target string) (integration.ProductID, error) {
	switch strings.TrimSpace(strings.ToLower(target)) {
	case "claude", "claude-code", "claudecode":
		return integration.ProductClaudeCode, nil
	case "codex", "codex-cli":
		return integration.ProductCodexCLI, nil
	case "opencode", "open-code":
		return integration.ProductOpenCode, nil
	case "gemini", "gemini-cli":
		return integration.ProductGeminiCLI, nil
	case "continue":
		return integration.ProductContinue, nil
	case "aider":
		return integration.ProductAider, nil
	case "goose":
		return integration.ProductGoose, nil
	default:
		return "", fmt.Errorf("unknown takeover target %q", target)
	}
}

func shellCommandString(parts []string) string {
	if len(parts) == 3 && (parts[0] == "sh" || parts[0] == "bash") && parts[1] == "-c" {
		return parts[2]
	}
	return strings.Join(parts, " ")
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
