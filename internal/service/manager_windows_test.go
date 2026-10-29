//go:build windows

package service

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestWindowsManagerBatterySettingsBeforeRun(t *testing.T) {
	for _, action := range []Action{ActionInstall, ActionStart, ActionRestart} {
		t.Run(string(action), func(t *testing.T) {
			plan, err := (windowsManager{}).Plan(action, Options{
				BinaryPath: `C:\Program Files\Clipal\clipal.exe`,
				ConfigDir:  `C:\Users\example\.clipal`,
			})
			if err != nil {
				t.Fatal(err)
			}
			wantCount := 3
			if action == ActionStart {
				wantCount = 2
			}
			if len(plan.Commands) != wantCount {
				t.Fatalf("commands = %#v", plan.Commands)
			}
			last := len(plan.Commands) - 1
			run := plan.Commands[last]
			if run.Path != "schtasks.exe" || !reflect.DeepEqual(run.Args, []string{"/Run", "/TN", windowsTaskName}) {
				t.Fatalf("last command must start task: %#v", run)
			}
			settings := plan.Commands[last-1]
			if settings.Path != "powershell.exe" || settings.IgnoreError {
				t.Fatalf("battery settings must succeed before task starts: %#v", settings)
			}
			if len(settings.Args) != 4 || !reflect.DeepEqual(settings.Args[:3], []string{"-NoProfile", "-NonInteractive", "-Command"}) {
				t.Fatalf("PowerShell args = %#v", settings.Args)
			}
			script := settings.Args[3]
			for _, want := range []string{
				`$ErrorActionPreference = 'Stop'`,
				`Get-ScheduledTask -TaskPath '\' -TaskName '` + windowsTaskName + `'`,
				`$task.Settings.DisallowStartIfOnBatteries = $false`,
				`$task.Settings.StopIfGoingOnBatteries = $false`,
				`Set-ScheduledTask -InputObject $task`,
			} {
				if !strings.Contains(script, want) {
					t.Errorf("script missing %q: %s", want, script)
				}
			}
			if strings.Contains(script, "New-ScheduledTaskSettingsSet") {
				t.Fatal("must preserve existing task settings")
			}
		})
	}
}

func TestWindowsManagerNonStartingActionsDoNotChangeBatterySettings(t *testing.T) {
	for _, action := range []Action{ActionStop, ActionStatus, ActionUninstall} {
		t.Run(string(action), func(t *testing.T) {
			plan, err := (windowsManager{}).Plan(action, Options{
				BinaryPath: `C:\clipal.exe`,
				ConfigDir:  `C:\config`,
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, command := range plan.Commands {
				if command.Path != "schtasks.exe" && !(action == ActionUninstall && command.Path == "powershell.exe") {
					t.Fatalf("unexpected settings change for %s: %#v", action, command)
				}
			}
		})
	}
}

func TestWindowsManagerInstallCopiesBinaryToStablePath(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `C:\Users\example\AppData\Local`)
	plan, err := (windowsManager{}).Plan(ActionInstall, Options{
		BinaryPath: `C:\Users\example\Downloads\clipal.exe`,
		ConfigDir:  `C:\Users\example\.clipal`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Copies) != 1 {
		t.Fatalf("copies = %#v", plan.Copies)
	}
	copy := plan.Copies[0]
	if copy.Source != `C:\Users\example\Downloads\clipal.exe` || !strings.HasSuffix(copy.Dest, `Clipal`+string(filepath.Separator)+`clipal.exe`) {
		t.Fatalf("copy = %#v", copy)
	}
	create := plan.Commands[0]
	if create.Path != "schtasks.exe" || !strings.Contains(create.Args[4], `Clipal`+string(filepath.Separator)+`clipal.exe`) {
		t.Fatalf("task command should use stable binary: %#v", create)
	}
}
