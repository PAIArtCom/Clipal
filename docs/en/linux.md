# Linux Guide

English: [docs/en/linux.md](linux.md) | 中文: [docs/zh/linux.md](../zh/linux.md)

This page only covers Linux-specific differences. For the shared setup flow, see [Getting Started](getting-started.md).

## Install The Binary

One-line install:

```bash
curl -fsSL https://clipal.paiart.com/install.sh | sh
```

The script downloads the matching release asset, verifies it against
`checksums.txt`, and installs to `$HOME/.local/bin`. Use
`CLIPAL_INSTALL_DIR=/usr/local/bin` for a system-wide install, but the script
does not use `sudo`, so run it from a writable location or install manually.

Or download from Releases:

- `clipal-linux-amd64`
- `clipal-linux-arm64`

Example:

```bash
chmod +x ./clipal-linux-amd64
mkdir -p "$HOME/.local/bin"
install -m 0755 ./clipal-linux-amd64 "$HOME/.local/bin/clipal"
clipal --version
```

Make sure `"$HOME/.local/bin"` is on your `PATH` (for example, add
`export PATH="$HOME/.local/bin:$PATH"` to your shell profile). This user-local
location is recommended because `clipal update` can replace the binary without
administrator privileges.

If you prefer a system-wide installation, `/usr/local/bin` is also supported,
but writing there requires `sudo`:

```bash
sudo install -m 0755 ./clipal-linux-amd64 /usr/local/bin/clipal
clipal --version
```

## Temporary Background Run

For a quick short-lived background run:

```bash
nohup clipal >/dev/null 2>&1 &
```

Useful for short-term use, but not the preferred long-running setup.

## Long-Running Setup: systemd User Service

The recommended approach is the built-in command flow:

```bash
clipal service install
clipal service status
clipal service restart
clipal service stop
clipal service uninstall
```

Useful variants:

```bash
clipal service install --force
clipal service install --config-dir /path/to/config
```

## Manual systemd Setup

If you want to manage the unit file yourself, create:

```text
~/.config/systemd/user/clipal.service
```

The example below assumes the recommended user-local installation. If you
installed Clipal under `/usr/local/bin`, change `ExecStart` to that path.

Minimal example:

```ini
[Unit]
Description=clipal local proxy
After=network.target

[Service]
Type=simple
ExecStart=%h/.local/bin/clipal --config-dir %h/.clipal
Restart=always
RestartSec=2

[Install]
WantedBy=default.target
```

Enable and start it:

```bash
systemctl --user daemon-reload
systemctl --user enable --now clipal.service
```

## Logging Advice

For long-running setups, this is a good default in `config.yaml`:

```yaml
log_stdout: false
log_retention_days: 7
```

Two common log sources:

- Clipal's own rotating logs
- `journalctl --user -u clipal.service -e`

## Linux-Specific Notes

- If the port is in use, change `port` or override with `--port`
- If the binary lives under your home directory, update `ExecStart` accordingly

For shared issues, continue with [Troubleshooting](troubleshooting.md).
