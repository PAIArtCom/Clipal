# Windows Guide

English: [docs/en/windows.md](windows.md) | 中文: [docs/zh/windows.md](../zh/windows.md)

This page only covers Windows-specific differences. For the shared setup flow, see [Getting Started](getting-started.md).

## Install The Binary

Download from Releases:

- `clipal-windows-amd64.exe`

Rename it to `clipal.exe` and place it somewhere like:

```text
C:\Users\<YOU>\bin\clipal.exe
```

Then add that directory to your user `PATH`.

The first `service install` copies the executable to `%LOCALAPPDATA%\Clipal\clipal.exe` and registers Task Scheduler against that stable path. This means the downloaded file can later be moved or removed without breaking the installed task. Re-run `service install --force` after downloading a new binary to replace the installed copy.

On Windows, `service install` requests elevation through the standard UAC confirmation dialog. Confirming the dialog continues the installation automatically; no Administrator PowerShell window or password needs to be provided to Clipal. `--dry-run` does not request elevation.

Verify:

```powershell
clipal.exe --version
```

## Background Operation: Task Scheduler

The recommended path is the built-in command flow:

```powershell
clipal.exe service install
clipal.exe service status
clipal.exe service restart
clipal.exe service stop
clipal.exe service uninstall
```

Useful variants:

```powershell
clipal.exe service install --force
clipal.exe service install --config-dir C:\path\to\config
clipal.exe service install --dry-run
```

The installed task uses `--detach-console` for background operation.

Clipal allows the task to start and keep running on battery power. Installation,
`service start`, and `service restart` apply these settings automatically. After
upgrading an older installation, run `clipal.exe service restart` once to repair
its battery settings; no manual Task Scheduler changes are needed. These commands
use Windows PowerShell and the built-in ScheduledTasks module to update the task.

## Manual Task Setup

If you want to manage Task Scheduler yourself:

- Program: `C:\Users\<YOU>\bin\clipal.exe`
- Arguments: `--detach-console --config-dir C:\Users\<YOU>\.clipal`
- Trigger: at logon
- Conditions: clear both “Start the task only if the computer is on AC power” and “Stop if the computer switches to battery power”

## Running As A Real Windows Service

If you need startup before login, you can wrap Clipal with a third-party tool such as NSSM.

Clipal itself ships with the Task Scheduler approach, not a built-in NSSM wrapper.

## Logging Advice

For background operation, this is a good default in `config.yaml`:

```yaml
log_stdout: false
log_retention_days: 7
```

## Windows-Specific Notes

- the Task Scheduler run user must match the user that owns the config directory
- `--config-dir` is easy to point at the wrong place
- the health check port must match the configured `port`
- older builds may show noisy permission warnings that are usually harmless on Windows

For shared issues, continue with [Troubleshooting](troubleshooting.md).
