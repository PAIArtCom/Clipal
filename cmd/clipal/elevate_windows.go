//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/PAIArtCom/Clipal/internal/service"
	"golang.org/x/sys/windows"
)

// maybeElevateService asks Windows for a UAC-confirmed administrator process
// for installation. The elevated child receives the exact original arguments,
// so users never need to copy a command into an Administrator PowerShell.
func maybeElevateService(args []string, action service.Action) (bool, error) {
	if action != service.ActionInstall || serviceDryRunEnabled(args) {
		return false, nil
	}

	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return true, fmt.Errorf("check administrator permission: %w", err)
	}
	defer token.Close()
	if token.IsElevated() {
		return false, nil
	}

	exe, err := os.Executable()
	if err != nil {
		return true, fmt.Errorf("find executable for elevation: %w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(exe); resolveErr == nil {
		exe = resolved
	}

	quotedArgs := make([]string, 0, len(args))
	for _, arg := range args {
		quotedArgs = append(quotedArgs, powerShellSingleQuote(arg))
	}
	script := "$p = Start-Process -FilePath " + powerShellSingleQuote(exe) +
		" -ArgumentList @(" + strings.Join(quotedArgs, ", ") + ") -Verb RunAs -Wait -PassThru; exit $p.ExitCode"

	cmd := exec.Command("powershell.exe", "-NoProfile", "-Command", script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return true, fmt.Errorf("administrator confirmation was cancelled or failed: %w", err)
	}
	return true, nil
}

func serviceDryRunEnabled(args []string) bool {
	for _, arg := range args {
		if arg == "--dry-run" || arg == "-dry-run" {
			return true
		}
		for _, flag := range []string{"--dry-run=", "-dry-run="} {
			if strings.HasPrefix(arg, flag) {
				value, err := strconv.ParseBool(strings.TrimPrefix(arg, flag))
				return err == nil && value
			}
		}
	}
	return false
}

func powerShellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
