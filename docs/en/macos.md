# macOS Guide

English: [docs/en/macos.md](macos.md) | 中文: [docs/zh/macos.md](../zh/macos.md)

This page only covers macOS-specific differences. For shared setup flow, see [Getting Started](getting-started.md).

## Install The Binary

One-line install:

```bash
curl -fsSL https://clipal.paiart.com/install.sh | sh
```

The script downloads the matching release asset, verifies it against
`checksums.txt`, and installs to `$HOME/.local/bin`. Set `CLIPAL_INSTALL_DIR`
to choose another directory.

Apple Silicon:

- `clipal-darwin-arm64`

Intel:

- `clipal-darwin-amd64`

Recommended options:

### Option A: put it in `~/bin`

```bash
mkdir -p ~/bin
mv ~/Downloads/clipal-darwin-arm64 ~/bin/clipal
chmod +x ~/bin/clipal
```

If needed, add `~/bin` to `PATH`:

```bash
echo 'export PATH="$HOME/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

### Option B: put it in a Homebrew path

```bash
sudo mv ~/Downloads/clipal-darwin-arm64 /opt/homebrew/bin/clipal
sudo chmod +x /opt/homebrew/bin/clipal
```

## Background Operation: launchd

The recommended path is the built-in command flow:

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
clipal service install --stdout ~/.clipal/logs/launchd.out --stderr ~/.clipal/logs/launchd.err
```

This manages:

```text
~/Library/LaunchAgents/com.lansespirit.clipal.plist
```

## Manual launchd Control

If you want full control over the plist, you can still manage the LaunchAgent yourself.

Common commands:

```bash
launchctl bootstrap "gui/$(id -u)" ~/Library/LaunchAgents/com.lansespirit.clipal.plist
launchctl kickstart -k "gui/$(id -u)/com.lansespirit.clipal"
launchctl bootout "gui/$(id -u)" ~/Library/LaunchAgents/com.lansespirit.clipal.plist
```

## Logging Advice

For background operation, this is a good default in `config.yaml`:

```yaml
log_stdout: false
log_retention_days: 7
```

That avoids duplicated logs between Clipal and launchd stdout capture.

## macOS-Specific Notes

- "I see requests but I did not open Claude Code" often means an editor extension or background helper is retrying
- To inspect port usage:

```bash
lsof -nP -iTCP:3333
```

For general issues, continue with [Troubleshooting](troubleshooting.md).
