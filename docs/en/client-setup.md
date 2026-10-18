# Client Setup

## One Important Mental Model

Clipal does not route by app name. It routes by request style, and `/clipal` is the preferred ingress for all of them:

- Claude-style
- OpenAI / Codex-style
- Gemini-style

Pick the client base URL that points at `/clipal`. Clipal detects the request family from the upstream path. The older `/claudecode`, `/codex`, and `/gemini` prefixes are still available as compatibility aliases.

One important routing detail: under `/clipal`, ambiguous generic resource paths such as `/files`, `/batches`, `/v1/files`, and `/v1/batches` follow OpenAI-compatible routing by default. Gemini-specific routing is kept on `/v1beta/*`, `/upload/*`, Gemini model RPC paths such as `/v1beta/models/{model}:generateContent`, Google AI Studio `interactions`, Gemini REST resources such as `operations`, `fileSearchStores`, `generatedFiles`, `corpora`, `authTokens`, `agents`, and `webhooks`, Google-style resource methods such as `/files:register` and `/batches/{batch}:cancel`, and Vertex Gemini `projects/.../publishers/google/models/...` plus `projects/.../locations/.../operations...` paths. Gemini Live API WebSocket proxying is not supported yet.

## Claude Code

Edit `~/.claude/settings.json`:

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:3333/clipal",
    "ANTHROPIC_API_KEY": "clipal"
  }
}
```

Notes:

- The Web UI takeover updates `ANTHROPIC_BASE_URL` in `~/.claude/settings.json`
- If neither `ANTHROPIC_API_KEY` nor `ANTHROPIC_AUTH_TOKEN` exists, it inserts the placeholder `ANTHROPIC_API_KEY = clipal` so Claude Code can start without a separate Claude login
- Preview and apply include `hasCompletedOnboarding = true` in `~/.claude.json`; a custom `CLAUDE_CONFIG_DIR` stores that file inside the selected directory
- Your existing `ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN` values are left untouched
- The Web UI can apply and roll back this user-level change for you from `CLI Takeover`

## Codex CLI

Edit `~/.codex/config.toml`:

```toml
model_provider = "clipal"

[model_providers.clipal]
name = "clipal"
base_url = "http://127.0.0.1:3333/clipal"
wire_api = "responses"
```

Notes:

- The Web UI can apply and roll back this user-level change for you from `CLI Takeover`
- Codex ignores model provider settings in project-local `.codex/config.toml`; keep the Clipal provider in user-level `~/.codex/config.toml`
- Takeover supports `CODEX_HOME` and an explicitly selected `<profile>.config.toml`; launch that profile with `--profile <profile>`. See [takeover cycles and configuration locations](web-ui.md#cli-takeover).

## OpenCode

Edit `~/.config/opencode/opencode.json`:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "model": "clipal/YOUR_GATEWAY_MODEL_ID",
  "provider": {
    "clipal": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "Clipal",
      "options": {
        "baseURL": "http://127.0.0.1:3333/clipal",
        "apiKey": "clipal"
      },
      "models": {
        "YOUR_GATEWAY_MODEL_ID": {
          "name": "Gateway model"
        }
      }
    }
  }
}
```

- Keep the OpenCode `baseURL` at `http://127.0.0.1:3333/clipal`; do not append `/v1` manually

Notes:

- The Web UI can apply and roll back this user-level change for you from `CLI Takeover`
- Takeover supports custom files, `OPENCODE_CONFIG`, `OPENCODE_CONFIG_DIR`, and XDG paths, preferring the later-loaded `opencode.jsonc`. Provider restrictions that exclude Clipal block apply and remain unchanged.
- Restrictions and main/small models are resolved from the merged known user layers; only the selected file is changed. An explicit gateway model selection replaces a stale alias wire ID while retaining other metadata; without an explicit selection, alias mappings are preserved. See [takeover and config merging](web-ui.md#cli-takeover).
- The Web UI takeover adds or updates `provider.clipal` and rewrites the active `model` to `clipal/<current-model-id>` when possible
- Project-local `opencode.json` or environment-based overrides may still take precedence

## Antigravity CLI (`agy`)

Use **CLI Takeover → Antigravity CLI**. Clipal updates
`~/.gemini/antigravity-cli/settings.json` with `"modelProvider": "gemini"` and
adds marked blocks to the applicable shell startup files:

Automatic takeover supports bash, zsh, and POSIX sh on macOS and Linux.
On Windows, set the following environment variables manually and set
`"modelProvider": "gemini"` in the settings file.

```sh
export GEMINI_API_KEY='clipal'
export GOOGLE_GEMINI_BASE_URL='http://127.0.0.1:3333/clipal'
```

Open a new terminal (or source the indicated profile), then run `agy`.
The placeholder satisfies the CLI’s nonempty-key requirement; upstream
credentials stay in Clipal. Preview covers the settings file and all applicable startup files; apply preserves unrelated
settings, and rollback restores each file to its cycle baseline. Existing
signed-in Antigravity sessions do not need to be deleted.

Antigravity CLI does not load `.env` files. See the
[official API-key and custom-endpoint instructions](https://www.antigravity.google/docs/install?tab=cli).

Use `agy models` to list the CLI's selectable IDs. In API-key mode the CLI can
send a Gemini API model name and thinking budget instead of the selected OAuth
route ID. For Antigravity OAuth backends, Clipal resolves these using that
account's model catalog: exact IDs take precedence; thinking levels or exact
budgets select only a unique matching variant in the same model family. A
`-preview` alias resolves only when its counterpart is advertised. Unknown IDs
remain unchanged, and ambiguous variants produce an error rather than silently
selecting another model. Explicit thinking amounts and visibility are preserved.
The catalog is cached for five minutes, isolated by account and outbound route;
discovery failures are not cached. API-key upstreams retain their original paths.

## Continue

Edit `~/.continue/config.yaml`:

```yaml
models:
  - name: Clipal
    provider: openai
    model: YOUR_GATEWAY_MODEL_ID
    apiBase: http://127.0.0.1:3333/clipal
    apiKey: clipal
    roles:
      - chat
      - edit
      - apply
```

Notes:

- The Web UI takeover adds or updates a Clipal model entry in the user-level Continue config
- Continue may still keep another model selected in the app, so you may need to switch to `Clipal` inside Continue
- Workspace-level Continue settings can still override the user-level config
- CLI users can launch `cn --config <managed-file>` to override a saved alternative config. Takeover honors `CONTINUE_GLOBAL_DIR` and preserves advanced Clipal model options without inferring autocomplete capability.

## Aider

Edit `~/.aider.conf.yml`:

```yaml
model: openai/YOUR_GATEWAY_MODEL_ID
openai-api-base: http://127.0.0.1:3333/clipal
openai-api-key: clipal
```

Notes:

- The Web UI takeover updates `openai-api-base` and uses an existing OpenAI-compatible model or an explicit gateway model choice
- Select a directory or file and launch with `aider --config <file>`. Models from another provider require an explicit choice; API keys and auxiliary model settings are preserved.
- Repo-local `.aider.conf.yml`, `.env`, current-directory config, and CLI flags can still override the home config
- Existing `openai-api-key` values are left untouched
- If `openai-api-key` is missing, Clipal writes a placeholder `clipal` key so the user-level config is immediately usable

## Goose

Edit `~/.config/goose/custom_providers/clipal.json`:

```json
{
  "name": "clipal",
  "engine": "openai",
  "display_name": "Clipal",
  "base_url": "http://127.0.0.1:3333/clipal/v1/chat/completions",
  "models": [
    {
      "name": "YOUR_GATEWAY_MODEL_ID"
    }
  ]
}
```

Notes:

- The Web UI takeover manages a dedicated Goose custom provider file rather than rewriting built-in provider config
- Takeover supports `GOOSE_PATH_ROOT/config` or an explicit configuration directory and preserves headers, authentication, streaming settings, and model metadata. New providers require a model choice and do not invent a context length.
- You may still need to select the Clipal provider or model inside Goose
- Restart Goose or open a new session after apply or rollback so it reloads provider config

## Generic OpenAI-Compatible Clients

For local clients that support a custom OpenAI Base URL or API host, the usual choice is:

```text
Base URL: http://127.0.0.1:3333/clipal
```

Common examples:

- Cherry Studio
- Kelivo
- Chatbox
- ChatWise
- other desktop apps with OpenAI-compatible mode

Typical settings:

- Provider type: OpenAI Compatible / OpenAI API
- Base URL: `http://127.0.0.1:3333/clipal`
- API Key: if the client insists, any non-empty placeholder usually works

Notes:

- Compatibility still depends on the exact paths, payload format, and model parameters the client sends
- If you are migrating an existing setup, the legacy aliases remain available: `/claudecode`, `/codex`, `/gemini`

## Quick Checks

- Clipal is running: `clipal status`
- Health check works: `curl -fsS http://127.0.0.1:3333/health`
- The client points to `http://127.0.0.1:3333/clipal`
- The client is not still pointing at an old official API host somewhere else

If setup still fails, continue with [Troubleshooting](troubleshooting.md).
