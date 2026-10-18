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
- View OAuth auth status and refresh summary in each provider's details
- Load OAuth plan and rate-limit details for Codex, Claude, and Google accounts (Antigravity)
- See each provider's runtime state in its row: in use, standby, disabled, cooling down (with time remaining), unavailable, or probing recovery

### Global Settings

- Change listen address and port
- Separately enable remote proxy access or remote Web UI access; both are disabled by default
- Change log level
- Configure reactivation, timeouts, and request body limits
- Configure the circuit breaker
- Configure log directory, retention, and stdout output
- Configure desktop notifications
- Unsaved edits are flagged, and leaving the page asks whether to discard them
- Fields that only take effect after a restart (listen address, port) carry an amber restart icon; other field explanations sit behind an ⓘ icon next to the label
- Enabling remote Web UI access asks for confirmation before saving, because the management UI has no built-in authentication

### System Status

- View version and uptime
- View config directory
- View current mode, pinned provider, and preferred provider per client group
- View the latest provider switch and latest request per client, with status color and relative time
- View provider runtime state, configured key count, and available key count

### Services

- Use the common `clipal service` actions from the UI
- Install, start, stop, restart, or uninstall the background service
- View best-effort service status

### CLI Takeover

- Inspect supported CLI integrations from the UI
- Apply one-click user-level takeover for `Claude Code`, `Codex CLI`, `OpenCode`, `Antigravity CLI`, `Continue`, `Aider`, and `Goose`
- Create a backup before the first takeover so rollback can restore the original file
- Roll back to the last captured pre-takeover state
- See the exact target config file path that Clipal manages
- Preview the current file and the exact result Clipal will write before applying changes

Notes:

- This feature currently edits user-level config only
- `Claude Code` takeover updates `ANTHROPIC_BASE_URL` in `~/.claude/settings.json`, inserts a placeholder `ANTHROPIC_API_KEY` only when no Claude client auth env is present, may also mark `~/.claude.json` onboarding as complete, and does not overwrite existing `ANTHROPIC_API_KEY` or `ANTHROPIC_AUTH_TOKEN`
- `OpenCode` takeover adds or updates `provider.clipal` and rewrites the active `model` to `clipal/<current-model-id>`
- `Antigravity CLI` takeover sets `modelProvider: "gemini"` in `~/.gemini/antigravity-cli/settings.json` and manages a marked block in the user’s bash, zsh, or POSIX sh profile exporting `GOOGLE_GEMINI_BASE_URL` and the local placeholder `GEMINI_API_KEY=clipal`. Both files are previewed and backed up; rollback restores both. Open a new terminal after applying or rolling back.
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

Adding a new OAuth account offers **Request country / region** and **Request timezone (IANA)** as dropdowns, prefilled with **US / `America/New_York`**. These are request preferences, not evidence of the account's registration or billing country. New York's IANA timezone follows daylight saving automatically; no fixed UTC−5 offset is stored. Unchanged defaults use `source: default`; edited choices use `source: user`. The Add OAuth and account environment dialogs share the same country and timezone choices. Country names follow the UI language; timezone values remain IANA identifiers. Native dropdowns support keyboard navigation and type-ahead selection. Changing country leaves the timezone unchanged, and existing saved timezone aliases remain selectable.

Preferences belong to the OAuth account and are shared by its linked providers. Refresh, reauthorization, restart and proxy changes retain saved choices. Imports preserve existing settings and their provenance; they do not apply new-account defaults. Legacy values without a trustworthy source remain visible with `needs_confirmation: true`. They may have come from an exit estimate, so the account globe dialog lets the user review, change and explicitly save them. Until confirmation, region fields are omitted from outgoing metadata. Reads, import previews and exports do not persist migration. Upstream region attributes remain separate credential metadata and are displayed independently; they never overwrite request preferences.

Device IDs derive from a versioned namespace, OAuth service and account ID using SHA-256. Credentials lacking an account ID use stable account identity/ref as a fallback, which diagnostics report. Token-only legacy accounts also use this fallback to confirm request preferences without changing provider linking identities. Account language supplies `Accept-Language`; supported environment headers and adapter metadata use confirmed preferences. Prompts, messages, tools and conversation IDs retain their original meaning. Claude metadata updates re-sign the billing content hash. Failover applies the selected account's settings.

Proxy selection follows provider configuration, inherited global configuration, environment and system policy and `NO_PROXY`. Clipal implements HTTP CONNECT, HTTPS proxies and SOCKS5; all proxy requests retain target hostnames for proxy-side DNS and routing. Proxy software handles its own VMess/VLESS protocols. Clipal neither probes software controllers nor discovers or takes over listening ports. TUN remains transparent to Clipal; no explicit proxy entry does not prove a physical direct route.

Account DNS defaults to **auto** and follows provider/global/environment/system proxy policy and `NO_PROXY` for each target. A standard proxy receives the target hostname and performs resolution, without a public DoH prerequisite. Without a proxy, auto first tries **Cloudflare → Google DoH** and the returned target IPs; DNS, TCP or standard TLS establishment failure falls back to the system hostname route. The DoH stage is capped at five seconds and reserves time within shorter request deadlines for fallback. VPN and TUN remain under OS routing on both paths. Standard HTTPS switching completes TLS before any HTTP request data is sent, so business requests are not replayed. Clipal does not scan proxy controllers or ports and does not change system network settings, saved DNS choices or account regions. Explicit `cloudflare`/`google` choices keep target resolution on that DoH service and fail without falling back to system target DNS; `system` follows the existing system resolver. DoH service hostnames use system bootstrap DNS with a fixed-IP fallback, always preserving original TLS SNI and certificate checks. IPv4/IPv6 target connections use a 250 ms stagger and shared deadlines. Proxy failure never falls back to a direct target connection. No explicit proxy does not prove a physical direct route because TUN may still apply. A first-hop proxy hostname may need system DNS. All standard proxy routes delegate target resolution regardless of the stored DNS choice; there is no strict proxy preflight.

Business requests reuse account HTTP clients and connection pools. DoH is queried only when a new connection needs an uncached address. Answers age from each DNS response's receipt, use the earliest A/AAAA expiry, and are capped at five minutes; concurrent cold lookups for the same account, route and hostname share one query sequence. Lookup failures back off for five seconds before retrying DoH; auto may use the system hostname during backoff, while explicit DoH selections still fail. An exhausted full five-second automatic DNS lookup budget also establishes backoff. Target TCP/TLS connection failures do not become DNS failure entries. Caller cancellation or a short caller deadline does not create backoff, and waiting requests can cancel independently. Positive and failed entries share a 256-entry limit with earliest-expiry eviction. These caches contain DNS results, not model responses.

**Check consistency** reports the selected proxy entry, who resolves the target hostname, and whether an anonymous target HTTP connection completes. Any HTTP status proves HTTP reachability, not OAuth authentication. Reference IP services may report their own exit IP/country/timezone; a matching proxy entry cannot prove the model target uses that exit. The target exit therefore stays **Not verified**, and observations never initialize or change account regions. No account credentials, refresh or paid model requests are sent by this diagnostic. Usage metadata has a 30-second total budget for automatic connection selection, refresh and upstream I/O.

The **Include public DoH reference for proxy routes** checkbox defaults off and applies only to the current diagnostic request. With a proxy selected, it queries the stored Cloudflare/Google choice, or Cloudflare → Google for `auto`/`system`, through the same entry after reachability checks. Results do not verify the proxy's DNS backend, target IP or egress. Failure is reported separately as **Not verified** and cannot block business requests; there is no strict preflight option. The reference query has a maximum 10-second budget within the diagnostic's 25-second total deadline and sends no account credentials or environment headers.

Management endpoints are `GET` / `PUT /api/providers/<client>/<name>/oauth-environment` and `POST /api/providers/<client>/<name>/oauth-diagnostics`. The POST body may be empty or `{ "check_doh": false }`; `{ "check_doh": true }` opts into the proxy reference query. It is ignored when the target has no explicit proxy, where the normal checks use the account's actual DNS policy. GET and diagnosis do not change preferences. PUT explicitly confirms or edits country and IANA timezone, checks the loaded account identity, preserves DNS/tokens, and uses existing localhost management and CSRF controls.


## Provider list

The Claude / OpenAI / Gemini tabs sit on the top edge of a panel, and everything inside that panel belongs to the selected client. Its first row holds the client's routing, the Auto / Manual switch (its ⓘ explains the mode) and the current routing candidate marked with a target icon, with **Add provider** on the right. A search box (name or base URL) appears there once a group has more than eight providers.

Below that row, the panel is split in two. On the left, providers are listed one per row in priority order: drag handle (its tooltip includes the configured priority), name, runtime state (in use, standby, disabled, cooling down with time remaining, unavailable, or probing recovery), and the enable switch. Click a row, or focus its name and press Enter, to show that provider on the right; the first provider is shown until you choose another.

The right-hand inspector shows the provider's name, state, OAuth tag and upstream host or provider ID, with pin, edit (OAuth accounts open the account environment instead) and delete at the top right. Below that come the reason for a degraded state; three fixed columns for credential status and last refresh (OAuth) or key count (API key), token usage with input/output, and spend today and over 7 days, where a dash marks a figure with no data yet; OAuth plan and limits with usage gauges; and rows for base URL, proxy, and provider ID, with copy buttons. Hovering or focusing usage or spend shows exact amounts and the token breakdown.

In **Plan & Limits**, click **Load** to retrieve the account’s upstream subscription and quota data, or **Refresh** to update it. Google quotas show the used percentage for each returned model or quota group and its reset time when available; 100% remaining appears as 0% used. Antigravity falls back to the model catalog’s explicit quota fields when the quota APIs are empty or unsupported; entries without quota values are omitted. Account initialization or authorization failures are shown as errors. Missing quota data is stated explicitly. A failed refresh shows an error and retains the previous data.

On desktop, the inspector stays below the app toolbar while the page scrolls. Long details scroll inside the inspector while its title and actions remain visible. Choosing another provider starts its details at the top without moving the provider list. Usage and spend tooltips remain visible outside the scrolling area, on hover or keyboard focus. Narrow or very short windows use normal page scrolling for details.

Reorder by dragging the handle, or focus the handle and press Alt+↑ / Alt+↓. Reordering is paused while a search is active, because the visible order is then only a subset. A failed reorder save restores the previous order.

In windows up to 760px wide or up to 360px high, the list and the inspector take turns: choosing a provider opens its inspector in place of the list, with a **Back** button to return. A search with no matches shows the list’s empty-result message; clearing it restores the selected detail. The current candidate is a routing preference, not proof of a completed request.

Automatic routing can stay on a provider that is succeeding or skip unavailable ones, so the configured order does not predict every request.

CLI Takeover cards show the target config path (abbreviated to `~` under the home directory; the full path is in the tooltip), a warning icon whose tooltip carries any tool-specific caveat, a restore icon button, and a collapsed change preview whose toggle shows the added and removed line counts. An open preview widens its card to the full row. For an active integration with a backup, the preview compares against the rollback backup; otherwise against the planned takeover result. The full current and target files stay one level deeper. Files too large for a line diff show only the full-file view.

## Saving, drafts, and connection feedback

Mode switches and pin changes keep the saved selection visible until the server accepts the change. Provider saves, deletes, enable switches, mode changes, and pin changes lock conflicting list actions and client selection until the request and refresh complete; a failed save leaves the original selection intact and reports the error so the operation can be retried.

Closing an edited Provider or OAuth account environment form (including Escape and the backdrop) asks before discarding changes. Failed saves preserve input. Global Settings locks its fields and save/reset actions while loading or resetting so a late read cannot overwrite new edits. A failed read shows an in-page retry banner; fields stay disabled until the first successful load, and a failed reset preserves the current draft. Reloading or closing the browser also warns about unsaved settings and form edits. Drafts remain in memory only; confirming discard or leaving the page loses them.

Malformed JSON is rejected before import format detection, with the offending filename in the error. Import file reads, previews, and applies lock file and mode selection and prevent duplicate submission. Applying an import and refreshing its results also prevents closing the preview or switching tabs; failures release the controls.

Gateway status refreshes every three seconds while the Providers or System Status tab is visible; service status refreshes while the Services or System Status tab is visible. Failed status requests show a persistent banner on those tabs instead of repeated error toasts. The last good data is retained with its last successful update time, or a notice that status has never loaded. Data older than 15 seconds is marked stale. Requests time out after 10 seconds, overlapping polls are skipped, and the banner offers retry and clears after a successful refresh. Status refresh failure does not by itself prove that upstream model requests have failed.

## Form controls and feedback

Timeouts and routing durations use a numeric field with a unit selector; request-body limits use bytes, KiB, MiB or GiB. Optional routing durations can be cleared to use defaults. Request-body limits must be at least 64 MiB; idle and response-header timeouts must be zero (disabled) or at least five minutes. Smaller values are flagged before submission. Existing compound durations and fine-grained values are preserved until explicitly edited. Opening the form does not normalize saved configuration. Controls for disabled routing and circuit-breaker features are disabled without deleting their values.

Reasoning effort offers common suggestions while allowing model-specific values; empty means no override. Provider names are validated before submission, and visible field labels focus their controls. Client tabs support left/right arrow navigation. Touch devices use larger action targets.

Dialogs move focus inside, keep Tab and Shift+Tab inside, isolate the background, and restore focus to the opening control. Account environment loading and diagnostics can be closed; late results do not change the closed dialog. Failed account preference loads provide an in-dialog retry. Account preference saves show a busy state and a success notification; errors from closed reads and diagnostics are ignored.

A failed provider-list load preserves the last list with a retry banner; it is not presented as an empty group. Mutations remain disabled until the list reloads successfully. Service HTTP errors show an error, and connection interruptions leave the action explicitly unconfirmed while status is checked.

## Related Docs

- [Config Reference](config-reference.md)
- [Routing and Failover](routing-and-failover.md)
- [Services, Status, and Updates](services.md)
