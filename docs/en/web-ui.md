# Web UI Guide

## How To Access It

Once Clipal is running, open:

```text
http://127.0.0.1:3333/
```

If you changed the port, replace `3333` with your actual port.

## What The Web UI Can Do

### Providers

- View providers by client group
- Add, edit, and delete API-key providers
- Enable or disable providers
- Switch between `auto` and `manual`
- Pin a provider in `manual` mode
- Enter either a single `api_key` or multi-line `api_keys`
- Configure provider-level proxy mode and custom proxy URL
- Configure provider-level model overrides
- Configure provider-level OpenAI `reasoning_effort` or Claude `thinking_budget_tokens`
- Start OAuth authorization for supported client/provider combinations
- Import OAuth credential files from the Add Provider dialog. Supported files are Codex CLI `auth.json` (`~/.codex/auth.json`), CLIProxyAPI single-account OAuth JSON files, and sub2api export JSON bundles; Clipal imports only accounts matching the selected service.
- Route Claude and Codex OAuth requests through Agent SDK-compatible upstream wrapping by default, with required transport fields handled by Clipal.
- View OAuth auth status and refresh summary on provider cards
- Load OAuth plan and rate-limit details for supported providers (currently Codex)

### Global Settings

- Change listen address and port
- Change log level
- Configure reactivation, timeouts, and request body limits
- Configure the circuit breaker
- Configure log directory, retention, and stdout output
- Configure desktop notifications

### System Status

- View version and uptime
- View config directory
- View current mode, pinned provider, and preferred provider per client group
- View last switch event and last request summary
- View provider runtime state, configured key count, and available key count

### Services

- Use the common `clipal service` actions from the UI
- Install, start, stop, restart, or uninstall the background service
- View best-effort service status

### CLI Takeover

- Inspect supported CLI integrations from the UI
- Apply one-click user-level takeover for `Claude Code`, `Codex CLI`, `OpenCode`, `Gemini CLI`, `Continue`, `Aider`, and `Goose`
- Create a backup before the first takeover so rollback can restore the original file
- Roll back to the last captured pre-takeover state
- See the exact target config file path that Clipal manages
- Preview the current file and the exact result Clipal will write before applying changes

Notes:

- This feature currently edits user-level config only
- `Claude Code` takeover updates `ANTHROPIC_BASE_URL` in `~/.claude/settings.json`, may also mark `~/.claude.json` onboarding as complete, and does not overwrite `ANTHROPIC_AUTH_TOKEN`
- `OpenCode` takeover adds or updates `provider.clipal` and rewrites the active `model` to `clipal/<current-model-id>`
- `Gemini CLI` takeover only updates `GEMINI_API_BASE` in `~/.gemini/.env`
- `Continue` takeover adds or updates a user-level `Clipal` model entry
- `Aider` takeover updates the home-level `openai-api-base` and a minimal OpenAI-compatible `model`; existing `openai-api-key` values stay untouched, and a placeholder `clipal` key is inserted only when missing
- `Goose` takeover manages a dedicated custom provider file under `~/.config/goose/custom_providers/`
- Project-local, managed, or enterprise settings may still override the effective behavior
- Re-applying an already managed integration is designed to be a no-op, so the original backup stays restorable
- After apply or rollback, restart the client or open a new session so it reloads the updated user config

### Export

- Export the current config as JSON for backup or migration

## Common Provider States In The UI

- `disabled`: manually disabled in config
- `unavailable`: currently blocked by auth, billing, quota, or no usable API key
- `cooling_down`: temporarily cooling down after a retryable failure or open circuit
- `recovery_probe`: Clipal is sending limited traffic while probing whether the provider has recovered

## Security Boundary

- The Web UI is localhost-only
- Even if the proxy listens on `0.0.0.0` or `::`, the management UI rejects non-loopback requests
- The management API is intended for local use and does not add a separate auth layer
- State-changing API calls require `X-Clipal-UI: 1`
- State-changing calls with a body require `Content-Type: application/json`
- The UI never shows raw API keys directly

## Which Changes Usually Apply Immediately

Most config edits are hot-reloaded without a restart, including:

- provider lists
- priorities
- `mode`
- failover-related timings
- request body limit
- notification settings

Changes that often still need a restart to fully take effect:

- listen address
- listen port
- some log output target changes

## Related Docs

- [Config Reference](config-reference.md)
- [Routing and Failover](routing-and-failover.md)
- [Services, Status, and Updates](services.md)
