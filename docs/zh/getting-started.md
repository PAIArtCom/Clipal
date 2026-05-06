# 快速开始

## 1. 下载与安装

从 [Releases](https://github.com/lansespirit/Clipal/releases) 下载对应平台的二进制，并放到你的 `PATH` 中。
最新稳定版：[GitHub Releases latest](https://github.com/lansespirit/Clipal/releases/latest)

平台细节：

- [macOS](macos.md)
- [Linux](linux.md)
- [Windows](windows.md)

确认版本：

```bash
clipal --version
```

## 2. 启动 Clipal

```bash
clipal
```

常见启动参数：

```bash
clipal --config-dir /path/to/config
clipal --listen-addr 127.0.0.1
clipal --port 3333
clipal --log-level debug
```

Clipal 默认会同时启动：

- 本地代理服务
- Web 管理界面（`http://127.0.0.1:3333/`）

## 3. 通过 Web UI 配置 Provider

用浏览器打开管理界面，直接在页面上添加 provider，无需编辑任何配置文件：

```text
http://127.0.0.1:3333/
```

在 **Providers** 页面你可以：

- 添加、编辑或删除 provider（Claude、OpenAI、Gemini 及任意 OpenAI 兼容端点）
- 在 OpenAI 页面发起 `OAuth -> Codex`，在 Claude 页面发起 `OAuth -> Claude`，或在 Gemini 页面发起 `OAuth -> Gemini`，让 Clipal 自动追加对应 provider
- 设置 `base_url`、`api_key` / `api_keys` 和路由权重
- 像管理普通 provider 一样对 OAuth provider 排序、置顶、启停

所有改动**即时生效**，无需重启服务。

OAuth 上游说明：

- `OAuth -> Codex` 支持 OpenAI `Responses` 请求
- `OAuth -> Claude` 支持 Claude `messages` 和 `count_tokens`
- `OAuth -> Gemini` 支持 Gemini `generateContent`、`streamGenerateContent`、`countTokens`
- OAuth 凭据保存在 YAML 之外的 `~/.clipal/oauth/`
- 只要有 `refresh_token`，Clipal 会在 access token 过期前自动刷新；如果上游先返回 `401`，也会强制 refresh 后再重试一次
- OAuth provider 通常通过授权流程接入；如果你已经有 Codex CLI 的 `auth.json`（`~/.codex/auth.json`）、CLIProxyAPI 单账号 OAuth JSON，或 sub2api 导出的 JSON，也可以在同一个 Add Provider 对话框里直接导入

> **进阶 / 可选 — 手动编辑 YAML 配置**
>
> 如果你更习惯用代码管理配置，也可以继续直接编辑 YAML 文件。
> 默认配置目录：
> - macOS / Linux: `~/.clipal/`
> - Windows: `%USERPROFILE%\\.clipal\\`
>
> 从仓库模板初始化：
>
> ```bash
> mkdir -p ~/.clipal
> cp examples/config.yaml ~/.clipal/config.yaml
> cp examples/claude.yaml ~/.clipal/claude.yaml
> cp examples/openai.yaml ~/.clipal/openai.yaml
> cp examples/gemini.yaml ~/.clipal/gemini.yaml
> ```
>
> 模板链接：[config.yaml](../../examples/config.yaml) · [claude.yaml](../../examples/claude.yaml) · [openai.yaml](../../examples/openai.yaml) · [gemini.yaml](../../examples/gemini.yaml)
>
> 详细字段说明见 [配置参考](config-reference.md)。

## 4. 验证运行状态

```bash
curl -fsS http://127.0.0.1:3333/health
clipal status
clipal status --json
```

## 5. 接入你的客户端

Clipal 现在统一推荐的客户端入口是：

- `/clipal`

旧配置仍可继续使用兼容别名：

- `/claudecode`
- `/codex`
- `/gemini`

具体接入方式见 [客户端接入](client-setup.md)。

## 6. 下一步看什么

- 想了解 Web UI 的完整功能：看 [Web UI 使用说明](web-ui.md)
- 想了解自动切换、手动锁定、多 Key：看 [路由与故障切换](routing-and-failover.md)
- 想配置后台服务和开机自启：看 [后台服务、状态与更新](services.md)
- 遇到问题：看 [排障与 FAQ](troubleshooting.md)
