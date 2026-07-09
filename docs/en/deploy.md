# Deploy Packages

Deploy packages move a working Clipal configuration from one machine to another.

Use this when you have already configured providers locally and want another machine to use the same provider URLs, API keys, routing, and global settings without opening the Web UI or re-entering secrets.

## Export

```bash
clipal export
```

By default this reads the normal Clipal config directory (`~/.clipal`) and writes `clipal.json` in the current directory.

To choose a package name, pass `-o`. If the name has no suffix, Clipal appends `.json`; if it has another suffix, Clipal rejects it.

```bash
clipal export -o prod
```

To export a different config directory:

```bash
clipal export -o prod --config-dir /path/to/config
```

The package contains provider URLs and API keys. Treat it like a secret file.

## Normal Import

```bash
clipal import clipal.json
```

This writes the packaged config files into the normal Clipal config directory on the current machine.

To import into a specific directory:

```bash
clipal import prod.json --config-dir /path/to/config
```

To apply CLI takeover during import:

```bash
clipal import prod.json --takeover claude,codex,gemini
```

After import, start or restart Clipal using the existing service commands:

```bash
clipal service install --config-dir /path/to/config
clipal service restart
```

## Temporary Import

```bash
clipal import prod.json --temporary
```

Temporary import creates a separate temporary config directory and writes the packaged files there. It does not overwrite the normal Clipal config directory.

The command prints the temporary directory and a cleanup command. Secrets are still written to that temporary directory, so stop any Clipal process using it before cleanup.

To run Clipal with the temporary config:

```bash
clipal --config-dir /tmp/clipal-deploy-...
```

`--start` currently prints the start command for the imported directory instead of installing or restarting a service.

`--takeover` reuses Clipal's existing CLI Takeover implementation and supports `claude`, `codex`, `opencode`, `gemini`, `continue`, `aider`, and `goose`.

## One-Command Agent Deploy

After installing Clipal on a new machine, put `clipal.json` in the current directory and run:

```bash
clipal deploy
```

This command:

1. Imports `clipal.json` if it exists in the current directory.
2. Checks whether `codex`, `claude`, and `gemini` are installed.
3. Runs the official install command for any missing CLI.
4. Applies Clipal's existing CLI Takeover for Codex, Claude Code, and Gemini CLI.

To preview the commands without changing the machine:

```bash
clipal deploy --dry-run
```

To deploy only selected agents:

```bash
clipal deploy codex
clipal deploy claude gemini
```

To install an official CLI without applying Clipal takeover:

```bash
clipal install codex
clipal install claude
clipal install gemini
```

`clipal deploy <agent>` installs the selected agent and applies Clipal's existing one-click takeover.

`clipal install <agent>` only runs the official install command and does not change agent configuration.

The comma-separated form still works:

```bash
clipal deploy --agents codex,claude
```

Clipal does not replace the upstream installers. It executes the official install commands directly and fails clearly if required tools such as `bash`, `npm`, `sh`, `curl`, or `powershell` are missing.

## Safety Notes

- Deploy packages include API keys by design.
- Only move them to machines you trust.
- Import rejects filenames outside Clipal's known config file list.
- Existing Clipal config schema and routing behavior are unchanged.
