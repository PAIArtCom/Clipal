# Client Setup

## One Important Mental Model

Clipal does not route by app name. It routes by request style, and `/clipal` is the preferred ingress for all of them:

- Claude-style
- OpenAI / Codex-style
- Gemini-style

Pick the client base URL that points at `/clipal`. Clipal detects the request family from the upstream path. The older `/claudecode`, `/codex`, and `/gemini` prefixes are still available as compatibility aliases.

One important routing detail: under `/clipal`, generic `/v1/*` resource paths follow OpenAI-compatible routing by default. Gemini-specific routing is kept on `/v1beta/*`, `/upload/*`, and Gemini model RPC paths such as `/v1beta/models/{model}:generateContent`.

## Claude Code

Edit `~/.claude/settings.json`:

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:3333/clipal"
  }
}
```

Notes:

- The Web UI takeover updates `ANTHROPIC_BASE_URL` in `~/.claude/settings.json`
- It may also set `hasCompletedOnboarding = true` in `~/.claude.json` so Claude Code honors the user-level override
- Your existing `ANTHROPIC_AUTH_TOKEN` is left untouched
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

## OpenCode

Edit `~/.config/opencode/opencode.json`:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "model": "clipal/gpt-5.4",
  "provider": {
    "clipal": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "Clipal",
      "options": {
        "baseURL": "http://127.0.0.1:3333/clipal",
        "apiKey": "clipal"
      },
      "models": {
        "gpt-5.4": {
          "name": "GPT-5.4"
        }
      }
    }
  }
}
```

- Keep the OpenCode `baseURL` at `http://127.0.0.1:3333/clipal`; do not append `/v1` manually

Notes:

- The Web UI can apply and roll back this user-level change for you from `CLI Takeover`
- The Web UI takeover adds or updates `provider.clipal` and rewrites the active `model` to `clipal/<current-model-id>` when possible
- Project-local `opencode.json` or environment-based overrides may still take precedence

## Gemini CLI

Edit `~/.gemini/.env`:

```dotenv
GEMINI_API_BASE=http://127.0.0.1:3333/clipal
```

Notes:

- The Web UI takeover only updates `GEMINI_API_BASE` in the user-level Gemini CLI `.env`
- Project-local `.env` files or exported environment variables may still take precedence
- After applying or rolling back from `CLI Takeover`, restart Gemini CLI or open a new session so it reloads the updated environment

## Continue

Edit `~/.continue/config.yaml`:

```yaml
models:
  - name: Clipal
    provider: openai
    model: gpt-5.4
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

## Aider

Edit `~/.aider.conf.yml`:

```yaml
model: openai/gpt-5.4
openai-api-base: http://127.0.0.1:3333/clipal
openai-api-key: clipal
```

Notes:

- The Web UI takeover updates `openai-api-base` and a minimal `model` value in the home-level Aider config
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
      "name": "gpt-5.4",
      "context_limit": 128000
    }
  ]
}
```

Notes:

- The Web UI takeover manages a dedicated Goose custom provider file rather than rewriting built-in provider config
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
