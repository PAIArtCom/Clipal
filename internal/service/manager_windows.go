//go:build windows

package service

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

const windowsTaskName = "Clipal"

type windowsManager struct{}

func DefaultManager() Manager {
	return windowsManager{}
}

func (windowsManager) Plan(action Action, opts Options) (*Plan, error) {
	if opts.BinaryPath == "" {
		return nil, fmt.Errorf("binary path is required")
	}
	if opts.ConfigDir == "" {
		return nil, fmt.Errorf("config dir is required")
	}

	// Run the binary the user installed directly. Keeping a second copy makes
	// upgrades and diagnostics ambiguous.
	runLine := buildWindowsTaskRunLine(opts.BinaryPath, opts.ConfigDir)

	plan := &Plan{}
	switch action {
	case ActionInstall:
		runAs := currentWindowsUser()
		createArgs := []string{"/Create", "/TN", windowsTaskName, "/TR", runLine, "/SC", "ONLOGON"}
		if runAs != "" {
			createArgs = append(createArgs, "/RU", runAs)
			// Run in the interactive session so clipal can access network normally.
			// The clipal binary detaches its console window when started by a task.
			createArgs = append(createArgs, "/IT")
		}
		// Be explicit about privilege level; default is LIMITED but we want to keep it stable.
		createArgs = append(createArgs, "/RL", "LIMITED")
		if opts.Force {
			createArgs = append(createArgs, "/F")
		}
		plan.Commands = append(plan.Commands,
			Command{Path: "schtasks.exe", Args: createArgs},
			windowsUserPathCommand(filepath.Dir(opts.BinaryPath)),
			// Apply both battery conditions before the first /Run. Task Scheduler
			// may refuse /Run while on battery when either default is enabled.
			windowsBatterySettingsCommand(),
			Command{Path: "schtasks.exe", Args: []string{"/Run", "/TN", windowsTaskName}},
		)
	case ActionUninstall:
		plan.Commands = append(plan.Commands,
			Command{Path: "schtasks.exe", Args: []string{"/End", "/TN", windowsTaskName}, IgnoreError: true},
			Command{Path: "schtasks.exe", Args: []string{"/Delete", "/TN", windowsTaskName, "/F"}},
			removeLegacyWindowsBinaryCommand(),
		)
	case ActionStart:
		plan.Commands = append(plan.Commands,
			windowsBatterySettingsCommand(),
			Command{Path: "schtasks.exe", Args: []string{"/Run", "/TN", windowsTaskName}},
		)
	case ActionStop:
		plan.Commands = append(plan.Commands, Command{Path: "schtasks.exe", Args: []string{"/End", "/TN", windowsTaskName}, IgnoreError: true})
	case ActionRestart:
		plan.Commands = append(plan.Commands,
			Command{Path: "schtasks.exe", Args: []string{"/End", "/TN", windowsTaskName}, IgnoreError: true},
			windowsBatterySettingsCommand(),
			Command{Path: "schtasks.exe", Args: []string{"/Run", "/TN", windowsTaskName}},
		)
	case ActionStatus:
		plan.Commands = append(plan.Commands, Command{Path: "schtasks.exe", Args: []string{"/Query", "/TN", windowsTaskName, "/FO", "LIST", "/V"}})
	default:
		return nil, fmt.Errorf("unsupported action %q", action)
	}

	return plan, nil
}

func windowsBatterySettingsCommand() Command {
	// schtasks /Create defaults to blocking startup on battery and stopping on
	// transition to battery. Update the existing settings before /Run, including
	// for tasks installed by older Clipal versions, without resetting other settings.
	const script = `$ErrorActionPreference = 'Stop'; ` +
		`$task = Get-ScheduledTask -TaskPath '\' -TaskName '` + windowsTaskName + `'; ` +
		`$task.Settings.DisallowStartIfOnBatteries = $false; ` +
		`$task.Settings.StopIfGoingOnBatteries = $false; ` +
		`Set-ScheduledTask -InputObject $task | Out-Null`
	return Command{
		Path: "powershell.exe",
		Args: []string{"-NoProfile", "-NonInteractive", "-Command", script},
	}
}

func windowsUserPathCommand(binaryDir string) Command {
	dir := strings.ReplaceAll(filepath.Clean(binaryDir), "'", "''")
	script := `$ErrorActionPreference = 'Stop'; ` +
		`$dir = '` + dir + `'; ` +
		`$current = [Environment]::GetEnvironmentVariable('Path', 'User'); ` +
		`$parts = @(); if ($current) { $parts = $current -split ';' | Where-Object { $_ -and $_.Trim() }; }; ` +
		`if (-not ($parts | Where-Object { $_.TrimEnd('\') -ieq $dir.TrimEnd('\') })) { ` +
		`[Environment]::SetEnvironmentVariable('Path', (($parts + $dir) -join ';'), 'User') }`
	return Command{
		Path: "powershell.exe",
		Args: []string{"-NoProfile", "-NonInteractive", "-Command", script},
	}
}

func removeLegacyWindowsBinaryCommand() Command {
	root := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if root == "" {
		if home, err := os.UserHomeDir(); err == nil {
			root = filepath.Join(home, "AppData", "Local")
		}
	}
	path := strings.ReplaceAll(filepath.Join(root, "Clipal", "clipal.exe"), "'", "''")
	return Command{
		Path:        "powershell.exe",
		Args:        []string{"-NoProfile", "-NonInteractive", "-Command", `Remove-Item -LiteralPath '` + path + `' -Force -ErrorAction SilentlyContinue`},
		IgnoreError: true,
	}
}

func currentWindowsUser() string {
	// Prefer a fully qualified account name (DOMAIN\User) when available.
	if u, err := user.Current(); err == nil {
		if name := strings.TrimSpace(u.Username); name != "" {
			return name
		}
	}
	// Fall back to environment variables.
	userName := strings.TrimSpace(os.Getenv("USERNAME"))
	if userName == "" {
		return ""
	}
	domain := strings.TrimSpace(os.Getenv("USERDOMAIN"))
	if domain != "" {
		return domain + `\` + userName
	}
	return userName
}

func buildWindowsTaskRunLine(binaryPath, configDir string) string {
	bin := quoteWindowsCmd(binaryPath)
	cfg := quoteWindowsCmd(configDir)
	// Detach the console so users don't accidentally close the task window and
	// kill clipal. This also makes Task Scheduler runs effectively "background".
	return fmt.Sprintf("%s --detach-console --config-dir %s", bin, cfg)
}

// quoteWindowsCmd quotes a value for a Windows command line fragment.
// This is used for schtasks /TR, which expects a single command line string.
func quoteWindowsCmd(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return `""`
	}
	escaped := strings.ReplaceAll(s, `"`, `\"`)
	return `"` + escaped + `"`
}
