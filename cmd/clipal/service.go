package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PAIArtCom/Clipal/internal/config"
	"github.com/PAIArtCom/Clipal/internal/service"
)

func runService(args []string) {
	fs := flag.NewFlagSet("service", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printServiceUsage(os.Stderr)
	}

	configDir := fs.String("config-dir", "", "Configuration directory (default: ~/.clipal)")
	binaryPath := fs.String("bin", "", "Path to clipal binary (default: current executable)")
	force := fs.Bool("force", false, "Reinstall/update the system service if it already exists")
	dryRun := fs.Bool("dry-run", false, "Print actions without executing them")
	raw := fs.Bool("raw", false, "Status only: print underlying service manager output")
	jsonOut := fs.Bool("json", false, "Status only: output status as JSON")
	timeout := fs.Duration("timeout", 30*time.Second, "Overall timeout for service manager commands")

	// macOS launchd (optional)
	stdoutPath := fs.String("stdout", "", "macOS: launchd StandardOutPath (optional)")
	stderrPath := fs.String("stderr", "", "macOS: launchd StandardErrorPath (optional)")

	if len(args) == 0 || (len(args) == 1 && isHelpToken(args[0])) {
		printServiceUsage(os.Stdout)
		return
	}

	actionArg, flagArgs, err := splitServiceArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "clipal service: %v\n", err)
		printServiceUsage(os.Stderr)
		os.Exit(2)
	}

	if err := fs.Parse(flagArgs); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printServiceUsage(os.Stdout)
			return
		}
		os.Exit(2)
	}

	action, err := service.ParseAction(actionArg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "clipal service: %v\n", err)
		printServiceUsage(os.Stderr)
		os.Exit(2)
	}

	cfgDir := *configDir
	if cfgDir == "" {
		cfgDir = config.GetConfigDir()
	}

	bin := *binaryPath
	if bin == "" {
		exe, exeErr := os.Executable()
		if exeErr != nil {
			fmt.Fprintf(os.Stderr, "clipal service: failed to determine executable path: %v\n", exeErr)
			os.Exit(1)
		}
		if resolved, resolvedErr := filepath.EvalSymlinks(exe); resolvedErr == nil {
			exe = resolved
		}
		bin = exe
	}

	mgr := service.DefaultManager()
	opts := service.Options{
		ConfigDir:  cfgDir,
		BinaryPath: bin,
		Force:      *force,
		DryRun:     *dryRun,
		StdoutPath: *stdoutPath,
		StderrPath: *stderrPath,
	}

	plan, err := mgr.Plan(action, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "clipal service %s failed: %v\n", action, err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	if action == service.ActionStatus && !opts.DryRun && !*raw {
		st, rawOut, stErr := service.GetStatus(ctx, opts)
		if stErr != nil {
			if rawOut != "" {
				fmt.Fprintln(os.Stderr, rawOut)
			}
			fmt.Fprintf(os.Stderr, "clipal service %s failed: %v\n", action, stErr)
			os.Exit(1)
		}

		if *jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(st); err != nil {
				fmt.Fprintf(os.Stderr, "clipal service %s failed: %v\n", action, err)
				os.Exit(1)
			}
			return
		}

		printServiceStatus(st)
		return
	}

	out, err := service.ExecutePlan(ctx, plan, opts.DryRun)
	if err != nil {
		if out != "" {
			fmt.Fprint(os.Stderr, out)
		}
		fmt.Fprintf(os.Stderr, "clipal service %s failed: %v\n", action, err)
		os.Exit(1)
	}

	if out != "" {
		fmt.Fprint(os.Stdout, out)
	}
}

func printServiceUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: clipal service [flags] <install|uninstall|start|stop|restart|status>")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "examples:")
	fmt.Fprintln(w, "  clipal service install --config-dir ~/.clipal")
	fmt.Fprintln(w, "  clipal service restart")
	fmt.Fprintln(w, "  clipal service status")
	fmt.Fprintln(w, "  clipal service status --raw")
	fmt.Fprintln(w, "  clipal service status --json")
}

func isHelpToken(s string) bool {
	switch strings.TrimSpace(s) {
	case "help", "-h", "--help":
		return true
	default:
		return false
	}
}

func printServiceStatus(st service.Status) {
	installed := "no"
	if st.Installed {
		installed = "yes"
	}
	loaded := "no"
	if st.Loaded {
		loaded = "yes"
	}
	running := "no"
	if st.Running {
		running = "yes"
	}
	pid := ""
	if st.PID > 0 {
		pid = fmt.Sprintf(" (pid %d)", st.PID)
	}

	fmt.Fprintf(os.Stdout, "Service:   %s (%s)\n", st.Manager, st.Name)
	if strings.TrimSpace(st.Scope) != "" {
		fmt.Fprintf(os.Stdout, "Scope:     %s\n", st.Scope)
	}
	fmt.Fprintf(os.Stdout, "Installed: %s\n", installed)
	fmt.Fprintf(os.Stdout, "Loaded:    %s\n", loaded)
	fmt.Fprintf(os.Stdout, "Running:   %s%s\n", running, pid)
	if strings.TrimSpace(st.BinaryPath) != "" {
		fmt.Fprintf(os.Stdout, "Program:   %s\n", st.BinaryPath)
	}
	if strings.TrimSpace(st.ConfigDir) != "" {
		fmt.Fprintf(os.Stdout, "Config:    %s\n", st.ConfigDir)
	}
	if strings.TrimSpace(st.StdoutPath) != "" || strings.TrimSpace(st.StderrPath) != "" {
		fmt.Fprintf(os.Stdout, "Logs:      stdout=%s\n", orDash(st.StdoutPath))
		fmt.Fprintf(os.Stdout, "           stderr=%s\n", orDash(st.StderrPath))
	}
	if strings.TrimSpace(st.LastExit) != "" {
		fmt.Fprintf(os.Stdout, "Last exit: %s\n", st.LastExit)
	}
	if strings.TrimSpace(st.Detail) != "" {
		fmt.Fprintf(os.Stdout, "Detail:    %s\n", st.Detail)
	}
	fmt.Fprintln(os.Stdout, "")
	fmt.Fprintln(os.Stdout, "Hint:")
	fmt.Fprintln(os.Stdout, "  clipal service status --raw   (full manager output)")
}

// splitServiceArgs accepts both:
//
//	clipal service [flags] <action>
//	clipal service <action> [flags]
//
// It extracts the action token and returns a reordered slice containing only
// flag arguments (so flag.FlagSet can parse them reliably).
func splitServiceArgs(args []string) (action string, flagArgs []string, err error) {
	// Flags that require a following value (unless provided via -flag=value).
	needsValue := map[string]bool{
		"config-dir": true,
		"bin":        true,
		"timeout":    true,
		"stdout":     true,
		"stderr":     true,
	}

	var haveAction bool
	for i := 0; i < len(args); i++ {
		a := args[i]

		// Standard "--" terminator: everything after is treated as positional.
		if a == "--" {
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("missing action")
			}
			if haveAction {
				return "", nil, fmt.Errorf("unexpected argument %q", args[i+1])
			}
			action = args[i+1]
			haveAction = true
			// Any further positional args are not supported.
			if i+2 < len(args) {
				return "", nil, fmt.Errorf("unexpected argument %q", args[i+2])
			}
			break
		}

		if strings.HasPrefix(a, "-") {
			flagArgs = append(flagArgs, a)

			name := strings.TrimLeft(a, "-")
			if name == "" {
				continue
			}
			// Handle -flag=value / --flag=value.
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				name = name[:eq]
			}
			if needsValue[name] && !strings.Contains(a, "=") {
				// For value flags, consume the next arg as the value even if it begins with "-".
				if i+1 >= len(args) {
					return "", nil, fmt.Errorf("flag %s requires a value", a)
				}
				flagArgs = append(flagArgs, args[i+1])
				i++
			}
			continue
		}

		// Non-flag token -> action.
		if !haveAction {
			action = a
			haveAction = true
			continue
		}
		return "", nil, fmt.Errorf("unexpected argument %q", a)
	}

	if !haveAction {
		return "", nil, fmt.Errorf("missing action")
	}
	return action, flagArgs, nil
}
