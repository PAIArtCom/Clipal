# 客户端接入

## 先理解一件事

Clipal 不是按“某个具体 App 名字”路由，而是按请求风格识别，且统一推荐通过 `/clipal` 接入：

- Claude 风格
- OpenAI / Codex 风格
- Gemini 风格

客户端 Base URL 建议统一指向 `/clipal`，再由 Clipal 根据请求路径识别协议族。旧的 `/claudecode`、`/codex`、`/gemini` 前缀仍保留为兼容别名。

需要注意一个路由细节：在 `/clipal` 下，`/files`、`/batches`、`/v1/files`、`/v1/batches` 这类有歧义的通用资源路径默认按 OpenAI 兼容协议处理。Gemini 特有路由保留在 `/v1beta/*`、`/upload/*`、`/v1beta/models/{model}:generateContent` 这类模型方法路径、Google AI Studio `interactions`，Gemini REST 资源（如 `operations`、`fileSearchStores`、`generatedFiles`、`corpora`、`authTokens`、`agents`、`webhooks`），Google 风格资源方法（如 `/files:register`、`/batches/{batch}:cancel`），以及 Vertex Gemini `projects/.../publishers/google/models/...` 和 `projects/.../locations/.../operations...` 路径上。Gemini Live API WebSocket 代理暂不支持。

## Claude Code

编辑 `~/.claude/settings.json`：

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:3333/clipal",
    "ANTHROPIC_API_KEY": "clipal"
  }
}
```

说明：

- Web UI 的一键接管会更新 `~/.claude/settings.json` 里的 `ANTHROPIC_BASE_URL`
- 如果既没有 `ANTHROPIC_API_KEY` 也没有 `ANTHROPIC_AUTH_TOKEN`，它会自动补一个占位 `ANTHROPIC_API_KEY = clipal`，确保 Claude Code 无需单独登录也能发起请求
- 它也可能在 `~/.claude.json` 中写入 `hasCompletedOnboarding = true`，确保 Claude Code 会真正采用这个用户级覆盖
- 现有的 `ANTHROPIC_API_KEY` 和 `ANTHROPIC_AUTH_TOKEN` 不会被改写
- 现在也可以直接在 Web UI 的 `CLI Takeover` 页面里一键 apply / rollback 这个用户级修改

## Codex CLI

编辑 `~/.codex/config.toml`：

```toml
model_provider = "clipal"

[model_providers.clipal]
name = "clipal"
base_url = "http://127.0.0.1:3333/clipal"
wire_api = "responses"
```

说明：

- 现在也可以直接在 Web UI 的 `CLI Takeover` 页面里一键 apply / rollback 这个用户级修改
- 如果你的环境里还有 workspace 级 Codex 配置，最终生效结果仍可能优先取 workspace 配置

## OpenCode

编辑 `~/.config/opencode/opencode.json`：

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

- OpenCode 的 `baseURL` 保持为 `http://127.0.0.1:3333/clipal` 即可，不要手动再补 `/v1`

说明：

- 现在也可以直接在 Web UI 的 `CLI Takeover` 页面里一键 apply / rollback 这个用户级修改
- Web UI 的一键接管会新增或更新 `provider.clipal`，并在可能的情况下把当前 `model` 改写为 `clipal/<当前模型ID>`
- 项目级 `opencode.json` 或环境变量覆盖仍可能影响最终生效结果

## Gemini CLI（Legacy）

编辑 `~/.gemini/.env`：

```dotenv
GEMINI_API_BASE=http://127.0.0.1:3333/clipal
```

说明：

- Google 已把个人 Gemini CLI OAuth 迁移到 Antigravity；Clipal 新增 Google 个人 OAuth provider 时请使用 Antigravity OAuth。
- Web UI 的一键接管只会更新 Gemini CLI 用户级 `.env` 里的 `GEMINI_API_BASE`
- 项目级 `.env` 或当前 shell 已导出的环境变量，仍可能覆盖最终生效结果
- 在 `CLI Takeover` 里 apply / rollback 之后，建议重启 Gemini CLI 或新开一个会话，让它重新加载环境变量

## Continue

编辑 `~/.continue/config.yaml`：

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

说明：

- Web UI 的一键接管会在用户级 Continue 配置里新增或更新一个 `Clipal` 模型项
- Continue 应用内当前选中的模型仍可能不是它，所以你可能还需要在 Continue 里手动切到 `Clipal`
- workspace 级 Continue 配置仍可能覆盖用户级配置

## Aider

编辑 `~/.aider.conf.yml`：

```yaml
model: openai/gpt-5.4
openai-api-base: http://127.0.0.1:3333/clipal
openai-api-key: clipal
```

说明：

- Web UI 的一键接管会更新 home 级 Aider 配置中的 `openai-api-base`，并补一个最小可用的 `model`
- repo 内 `.aider.conf.yml`、`.env`、当前目录配置和 CLI 参数仍可能覆盖 home 配置
- 已有的 `openai-api-key` 不会被改写
- 如果 `openai-api-key` 缺失，Clipal 会自动补一个占位值 `clipal`，保证用户级配置可直接使用

## Goose

编辑 `~/.config/goose/custom_providers/clipal.json`：

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

说明：

- Web UI 的一键接管会管理一个独立的 Goose custom provider 文件，而不是重写内建 provider
- 你可能仍需要在 Goose 内手动选择 Clipal provider 或对应模型
- Apply / Rollback 后建议重启 Goose 或新开一个会话，让它重新加载 provider 配置

## 通用 OpenAI 兼容客户端

对于支持“自定义 OpenAI Base URL”或“自定义 API Host”的本地客户端，通常使用：

```text
Base URL: http://127.0.0.1:3333/clipal
```

常见例子：

- Cherry Studio
- Kelivo
- Chatbox
- ChatWise
- 其他支持 OpenAI 兼容接口的桌面客户端

常见设置建议：

- Provider 类型：OpenAI Compatible / OpenAI API
- Base URL：`http://127.0.0.1:3333/clipal`
- API Key：若客户端强制要求，可填写任意非空占位值

注意：

- 客户端是否可用，取决于它发送的接口路径、请求体格式和模型参数是否与上游兼容
- 如果你在迁移旧配置，仍可继续使用兼容别名：`/claudecode`、`/codex`、`/gemini`

## 常见检查项

- Clipal 已启动：`clipal status`
- 健康检查正常：`curl -fsS http://127.0.0.1:3333/health`
- 客户端 Base URL 指向了 `http://127.0.0.1:3333/clipal`
- 本地客户端没有把旧的官方 Base URL 缓存在别处

如果接入后仍失败，继续看 [排障与 FAQ](troubleshooting.md)。
