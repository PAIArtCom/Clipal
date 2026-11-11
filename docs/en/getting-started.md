# Getting Started

## 1. Install

The fastest path is npm:

```bash
npm install -g clipal
```

If you use npm 12 or newer, no extra flags are needed: current Clipal npm packages use platform-specific optional dependencies instead of install scripts.

> Older Clipal npm versions used a `postinstall` download. If you are installing one of those versions with npm 12+, use `npm install -g clipal --allow-scripts=clipal`.

The location used by `npm install -g` depends on your npm prefix. With a
system prefix such as `/usr/local`, updating the installed binary requires
`sudo`; use a user-local npm prefix if you want passwordless updates.

### Standalone installer

If you do not want to use npm, install the standalone binary with the official script:

```bash
curl -fsSL https://clipal.paiart.com/install.sh | sh
```

On Windows (PowerShell):

```powershell
powershell -ExecutionPolicy ByPass -c "irm https://clipal.paiart.com/install.ps1 | iex"
```

The installer detects the OS and CPU, downloads the matching asset from the latest
release, verifies it against `checksums.txt`, and installs to `$HOME/.local/bin`
(Windows: `%LOCALAPPDATA%\Programs\clipal`). Use `CLIPAL_INSTALL_DIR` to choose a
different directory and `CLIPAL_VERSION` (for example `v0.30.3`) to pin a release.

You can also download the right binary from [Releases](https://github.com/PAIArtCom/Clipal/releases) and place it on your `PATH`.
On Linux, the recommended location is `$HOME/.local/bin`; installing to
`/usr/local/bin` requires `sudo` and is a system-wide installation.
Latest stable release: [GitHub Releases latest](https://github.com/PAIArtCom/Clipal/releases/latest)

Platform-specific notes:

- [macOS](macos.md)
- [Linux](linux.md)
- [Windows](windows.md)

Verify the version:

```bash
clipal --version
```

## 2. Start Clipal

```bash
clipal
```

Common startup overrides:

```bash
clipal --config-dir /path/to/config
clipal --listen-addr 127.0.0.1
clipal --port 3333
clipal --log-level debug
```

By default, Clipal starts both:

- the local proxy
- the Web management UI at `http://127.0.0.1:3333/`

## 3. Configure Providers via Web UI

Open the Web UI in your browser and add your providers there — no config files needed:

```text
http://127.0.0.1:3333/
```

From the **Providers** page you can:

- Add, edit, or remove API-key providers (Claude, OpenAI, Google, and any OpenAI-compatible endpoint)
- Start `OAuth -> Codex` for OpenAI, `OAuth -> Claude` for Claude, or `OAuth -> Antigravity` for Google and let Clipal append the provider automatically
- Set `base_url`, `api_key` / `api_keys`, and routing weights for API-key providers
- Reorder, pin, enable, or disable OAuth providers the same way as API-key providers

Changes take effect immediately without a restart.

OAuth upstream notes:

- `OAuth -> Codex` supports OpenAI `Responses` requests and model listing: `/v1/models` for OpenAI-compatible clients, or `/models?client_version=...` for native Codex clients
- `OAuth -> Claude` supports Claude `messages` and `count_tokens`
- `OAuth -> Antigravity` supports Gemini-compatible `generateContent`, `streamGenerateContent`, `countTokens`, model listing, and Gemini image models through `generateContent`. Imagen/Veo `predict*` endpoints use Google AI Studio API-key or Vertex providers instead.
- For Claude and Codex OAuth, ordinary clients use an Agent SDK-compatible upstream envelope by default, with required transport fields handled by Clipal.
- Client-supplied control fields win over defaults when supported by the target model: explicit `tools`, Claude `thinking` / `context_management` / `output_config`, and Codex `reasoning` / `tool_choice` / `parallel_tool_calls` are preserved.
- OAuth credentials are stored locally outside YAML under `~/.clipal/oauth/`
- Clipal refreshes access tokens automatically before expiry when a `refresh_token` is available, and retries once after an upstream `401` by forcing a refresh
- OAuth providers are usually added through the authorization flow; if you already have Codex CLI `auth.json` (`~/.codex/auth.json`), CLIProxyAPI single-account OAuth JSON, or a sub2api export JSON bundle, you can import it from the same Add Provider dialog

If you prefer to manage configuration as code, you can still edit YAML files directly. Field-level details and example templates live in [Config Reference](config-reference.md).

## 4. Verify It Is Running

```bash
curl -fsS http://127.0.0.1:3333/health
clipal status
clipal status --json
```

## 5. Connect Your Client

Clipal standardizes client ingress on:

- `/clipal`

Compatibility aliases remain available for older setups:

- `/claudecode`
- `/codex`
- `/google`
- `/gemini`

See [Client Setup](client-setup.md) for exact client-side configuration.

## 6. What To Read Next

- Want a full walkthrough of the Web UI: [Web UI Guide](web-ui.md)
- Want to understand failover, pinning, and multi-key behavior: [Routing and Failover](routing-and-failover.md)
- Want autostart or background service setup: [Services, Status, and Updates](services.md)
- Hit a problem: [Troubleshooting](troubleshooting.md)
