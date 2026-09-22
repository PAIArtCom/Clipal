# Services, Status, and Updates

## `clipal status`

Use this to inspect the current instance without starting the server.

```bash
clipal status
clipal status --json
clipal status --no-service
```

It summarizes:

- version
- config directory
- listen address and port
- health check result
- Web UI URL
- enabled provider counts by client group
- background service status

## `clipal service`

Use this to install and manage the background service.

```bash
clipal service install
clipal service status
clipal service restart
clipal service stop
clipal service uninstall
```

`clipal service restart` remains the canonical form in docs and scripts. For interactive use, `clipal restart` is available as a shortcut.

Common flags:

```bash
clipal service install --config-dir /path/to/config
clipal service install --force
clipal service status --raw
clipal service status --json
```

Background service style by OS:

- macOS: `launchd`
- Linux: `systemd --user`
- Windows: Task Scheduler

OS-specific details:

- [macOS](macos.md)
- [Linux](linux.md)
- [Windows](windows.md)

## `clipal update`

Use this to check for updates or replace the current binary in place:

```bash
clipal update --check
clipal update --dry-run
clipal update
```

During an update, Clipal reports the release check, checksum download, binary download, SHA256 verification, and installation stages. An interactive terminal refreshes download percentage, transferred size, speed, and ETA in place. Redirected output and CI runs receive stable line-oriented stage logs instead. Progress is written to stderr while the final result remains on stdout, preserving the existing final-output contract for scripts.

Scripts that do not need progress output can use:

```bash
clipal update --quiet
```

On Windows, Clipal first reports that the update is scheduled. The updater helper replaces the binary after the current process exits; its final `clipal: update applied` message confirms that replacement completed.

## Long-Running Setup Advice

For background operation, this is a good default in `config.yaml`:

```yaml
log_stdout: false
log_retention_days: 7
```

That avoids duplicated logs between Clipal and the OS service manager.

## Common Flows

After first install:

```bash
clipal status
clipal service install
clipal service status
```

After updating:

```bash
clipal update
clipal service restart
clipal restart   # shortcut
clipal status
```

## Related Docs

- [Getting Started](getting-started.md)
- [Troubleshooting](troubleshooting.md)
