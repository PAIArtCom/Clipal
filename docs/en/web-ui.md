# Web UI Guide

## How To Access It

Once Clipal is running, open:

```text
http://127.0.0.1:3333/
```

If you changed the port, replace `3333` with your actual port.

The current section and client are kept in the URL hash (for example `#/providers/openai` or `#/status`), so reloading or bookmarking returns to the same view.

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
- Separately enable remote proxy access or remote Web UI access; both are disabled by default
- Change log level
- Configure reactivation, timeouts, and request body limits
- Configure the circuit breaker
- Configure log directory, retention, and stdout output
- Configure desktop notifications
- Unsaved edits are flagged, and leaving the page asks whether to discard them
- Fields that only take effect after a restart (listen address, port) carry a restart hint
- Enabling remote Web UI access asks for confirmation before saving, because the management UI has no built-in authentication

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
- `Claude Code` takeover updates `ANTHROPIC_BASE_URL` in `~/.claude/settings.json`, inserts a placeholder `ANTHROPIC_API_KEY` only when no Claude client auth env is present, may also mark `~/.claude.json` onboarding as complete, and does not overwrite existing `ANTHROPIC_API_KEY` or `ANTHROPIC_AUTH_TOKEN`
- `OpenCode` takeover adds or updates `provider.clipal` and rewrites the active `model` to `clipal/<current-model-id>`
- `Gemini CLI` takeover only updates `GEMINI_API_BASE` in `~/.gemini/.env`
- `Continue` takeover adds or updates a user-level `Clipal` model entry
- `Aider` takeover updates the home-level `openai-api-base` and a minimal OpenAI-compatible `model`; existing `openai-api-key` values stay untouched, and a placeholder `clipal` key is inserted only when missing
- `Goose` takeover manages a dedicated custom provider file under `~/.config/goose/custom_providers/`
- Project-local, managed, or enterprise settings may still override the effective behavior
- Re-applying an already managed integration is designed to be a no-op, so the original backup stays restorable
- After apply or rollback, restart the client or open a new session so it reloads the updated user config

### Export Full Backup

- Use the **Export Full Backup** panel on the **Backup & Import** tab to download a complete `clipal.data/v1` JSON backup
- The backup includes global and client configuration, provider API keys, OAuth access and refresh tokens, and usage data

### Import Data

- Use the **Import Data** panel on the **Backup & Import** tab to preview and apply native backups or supported external credential exports

## Common Provider States In The UI

- `disabled`: manually disabled in config
- `unavailable`: currently blocked by auth, billing, quota, or no usable API key
- `cooling_down`: temporarily cooling down after a retryable failure or open circuit
- `recovery_probe`: Clipal is sending limited traffic while probing whether the provider has recovered

## Security Boundary

- The Web UI is localhost-only by default
- When the user explicitly enables `allow_remote_web_ui` in Global Settings, the management UI accepts remote access. This mode has no built-in authentication; protect it with a firewall, VPN, or reverse proxy.
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

## OAuth account request environment

Adding a new OAuth account offers **Request country / region** and **Request timezone (IANA)**, prefilled with **US / `America/New_York`**. These are request preferences, not evidence of the account's registration or billing country. New York's IANA timezone follows daylight saving automatically; no fixed UTC−5 offset is stored. Unchanged defaults use `source: default`; edited choices use `source: user`.

Preferences belong to the OAuth account and are shared by its linked providers. Refresh, reauthorization, restart and proxy changes retain saved choices. Imports preserve existing settings and their provenance; they do not apply new-account defaults. Legacy values without a trustworthy source remain visible with `needs_confirmation: true`. They may have come from an exit estimate, so the account globe dialog lets the user review, change and explicitly save them. Until confirmation, region fields are omitted from outgoing metadata. Reads, import previews and exports do not persist migration. Upstream region attributes remain separate credential metadata and are displayed independently; they never overwrite request preferences.

Device IDs derive from a versioned namespace, OAuth service and account ID using SHA-256. Credentials lacking an account ID use stable account identity/ref as a fallback, which diagnostics report. Token-only legacy accounts also use this fallback to confirm request preferences without changing provider linking identities. Account language supplies `Accept-Language`; supported environment headers and adapter metadata use confirmed preferences. Prompts, messages, tools and conversation IDs retain their original meaning. Claude metadata updates re-sign the billing content hash. Failover applies the selected account's settings.

Proxy selection follows provider configuration, inherited global configuration, environment policy and `NO_PROXY`. Clipal implements HTTP CONNECT, HTTPS proxies and SOCKS5; all proxy requests retain target hostnames for proxy-side DNS and routing. Proxy software handles its own VMess/VLESS protocols. Clipal neither probes software controllers nor discovers or takes over listening ports. TUN remains transparent to Clipal; no explicit proxy entry does not prove a physical direct route.

Account DNS defaults to **auto** and follows the effective policy for each target, including `NO_PROXY`: a standard proxy receives the target hostname and performs resolution, with no public DoH prerequisite. Without an explicit proxy, auto uses **Cloudflare → Google DoH**, fixed service bootstrap IPs and returned target IPs; IPv4/IPv6 connections use a 250 ms stagger and bounded multi-address budgets. Failed required DoH never falls back to system target DNS, and proxy failure never falls back to direct target connections. No explicit proxy does not prove a physical direct route because TUN may still apply. Proxy hostname bootstrap can require the system resolver. Existing account DNS values are retained: `system` selects system resolution without a proxy; `cloudflare`/`google` select that DoH service without a proxy. All standard proxy routes delegate target resolution regardless of that stored choice, so no strict preflight is enabled. DNS selection is computed per request without writing account identity, region, tokens or stored DNS values.

**Check consistency** reports the selected proxy entry, who resolves the target hostname, and whether an anonymous target HTTP connection completes. Any HTTP status proves HTTP reachability, not OAuth authentication. Reference IP services may report their own exit IP/country/timezone; a matching proxy entry cannot prove the model target uses that exit. The target exit therefore stays **Not verified**, and observations never initialize or change account regions. No account credentials, refresh or paid model requests are sent by this diagnostic. Usage metadata has a 30-second total budget for required no-proxy DoH, refresh and upstream I/O.

The **Include public DoH reference for proxy routes** checkbox defaults off and applies only to the current diagnostic request. With a proxy selected, it queries the stored Cloudflare/Google choice, or Cloudflare → Google for `auto`/`system`, through the same entry after reachability checks. Results do not verify the proxy's DNS backend, target IP or egress. Failure is reported separately as **Not verified** and cannot block business requests; there is no strict preflight option. The reference query has a maximum 10-second budget within the diagnostic's 25-second total deadline and sends no account credentials or environment headers.

Management endpoints are `GET` / `PUT /api/providers/<client>/<name>/oauth-environment` and `POST /api/providers/<client>/<name>/oauth-diagnostics`. The POST body may be empty or `{ "check_doh": false }`; `{ "check_doh": true }` opts into the proxy reference query. It is ignored when the target has no explicit proxy, where the normal checks use the account's actual DNS policy. GET and diagnosis do not change preferences. PUT explicitly confirms or edits country and IANA timezone, checks the loaded account identity, preserves DNS/tokens, and uses existing localhost management and CSRF controls.

## Related Docs

- [Config Reference](config-reference.md)
- [Routing and Failover](routing-and-failover.md)
- [Services, Status, and Updates](services.md)
