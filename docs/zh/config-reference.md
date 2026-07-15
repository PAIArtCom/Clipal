# 配置参考

## 配置文件位置

默认目录：

- macOS / Linux: `~/.clipal/`
- Windows: `%USERPROFILE%\\.clipal\\`

默认文件：

```text
config.yaml
claude.yaml
openai.yaml
gemini.yaml
```

`config.yaml` 是全局配置，其余三个文件分别管理不同客户端分组。

对应模板：

- [../../examples/config.yaml](../../examples/config.yaml)
- [../../examples/claude.yaml](../../examples/claude.yaml)
- [../../examples/openai.yaml](../../examples/openai.yaml)
- [../../examples/gemini.yaml](../../examples/gemini.yaml)

## 最小示例

`config.yaml`：

```yaml
listen_addr: 127.0.0.1
allow_remote_proxy: false
allow_remote_web_ui: false
port: 3333
log_level: info
reactivate_after: 1h
```

`openai.yaml`：

```yaml
mode: auto
pinned_provider: ""

providers:
  - name: openai-compatible
    base_url: https://api.openai.com
    api_key: sk-xxx
    priority: 1
    enabled: true
```

`openai.yaml`、`claude.yaml`、`gemini.yaml` 都支持 OAuth 上游。推荐通过 Web UI 的授权流程创建，而不是手写这些字段。

## 全局配置 `config.yaml`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `listen_addr` | string | `127.0.0.1` | 监听地址 |
| `allow_remote_proxy` | bool | `false` | 当 `listen_addr` 不是 loopback 地址时必须开启；显式允许远程客户端使用代理。 |
| `allow_remote_web_ui` | bool | `false` | 显式允许远程访问 Web UI 和管理 API。该模式没有内置认证，请使用防火墙、VPN 或反向代理保护。 |
| `port` | int | `3333` | 本地代理端口 |
| `log_level` | string | `info` | `debug` / `info` / `warn` / `error` |
| `reactivate_after` | duration | `1h` | provider 临时禁用后的自动恢复时间；设为 `0` 可禁用基于鉴权、计费、额度错误的临时禁用 |
| `upstream_idle_timeout` | duration | `3m` | 上游响应 body 长时间无字节时中断当前尝试 |
| `response_header_timeout` | duration | `2m` | 等待上游响应头的超时 |
| `upstream_proxy_mode` | string | `environment` | 作为默认值应用到 `proxy_mode: default` 的 provider；可选 `environment` / `direct` / `custom` |
| `upstream_proxy_url` | string | 空 | 当 `upstream_proxy_mode: custom` 时必填；支持 `http://`、`https://`、`socks5://` 和 `socks5h://` 代理 URL |
| `max_request_body_bytes` | int | `33554432` | 请求体大小上限，默认 32 MiB |
| `log_dir` | string | `<config-dir>/logs` | 日志目录 |
| `log_retention_days` | int | `7` | 日志保留天数；`0` 表示永久保留；默认保留 7 天 |
| `log_stdout` | bool | `true` | 是否同时输出到 stdout；长期后台运行通常建议设为 `false` |

### `notifications`

```yaml
notifications:
  enabled: false
  min_level: error
  provider_switch: true
```

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | `false` | 是否启用桌面通知 |
| `min_level` | string | `error` | `debug` / `info` / `warn` / `error` |
| `provider_switch` | bool | `true` | 是否为 provider 切换发送通知 |

### `circuit_breaker`

```yaml
circuit_breaker:
  failure_threshold: 4
  success_threshold: 2
  open_timeout: 60s
  half_open_max_inflight: 1
```

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `failure_threshold` | int | `4` | 连续失败多少次后打开熔断；`0` 表示禁用 |
| `success_threshold` | int | `2` | 半开状态下连续成功多少次后恢复 |
| `open_timeout` | duration | `60s` | 熔断打开多久后进入半开探测 |
| `half_open_max_inflight` | int | `1` | 半开探测并发上限 |

### `routing`

```yaml
routing:
  sticky_sessions:
    enabled: true
    explicit_ttl: 30m
    cache_hint_ttl: 10m
    dynamic_feature_ttl: 10m
    dynamic_feature_capacity: 1024
    response_lookup_ttl: 15m
  busy_backpressure:
    enabled: true
    retry_delays:
      - 5s
      - 10s
    probe_max_inflight: 1
    short_retry_after_max: 3s
    max_inline_wait: 8s
```

`sticky_sessions` 用来控制 Clipal 在内存里保留黏性线索的时间：

- `explicit_ttl`：显式链路键，例如 OpenAI `previous_response_id`
- `cache_hint_ttl`：缓存导向的显式 hint，例如 `prompt_cache_key`
- `dynamic_feature_ttl`：根据人类消息历史提取的短期启发式黏性
- `dynamic_feature_capacity`：动态 / cache-level 黏性缓存的容量上限，超出后按最近最少使用淘汰
- `response_lookup_ttl`：response id 查询缓存的保留时间

`busy_backpressure` 用来控制 Clipal 遇到并发限制类 `429` 时的处理方式：

- `retry_delays`：overflow 之前的 inline wait / backoff 序列
- `probe_max_inflight`：单个 busy provider 允许的恢复探测并发上限
- `short_retry_after_max`：只有非常短的 retry hint 才会进入 busy 处理分支
- `max_inline_wait`：单个请求在代理内等待的最长时间，超过后直接 overflow 到其他 provider

## 客户端配置

三个客户端文件结构相同：

- `claude.yaml`
- `openai.yaml`
- `gemini.yaml`

示例：

```yaml
mode: auto
pinned_provider: ""

providers:
  - name: primary
    base_url: https://api.example.com
    api_key: sk-xxx
    priority: 1
    enabled: true

  - name: backup
    base_url: https://backup.example.com
    api_keys:
      - sk-a
      - sk-b
    priority: 2
    enabled: true
```

### 顶层字段

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `mode` | string | `auto` | `auto` 或 `manual` |
| `pinned_provider` | string | 空 | `mode: manual` 时要锁定的 provider 名称 |
| `providers` | array | 无 | provider 列表 |

### `providers[]`

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `name` | string | 是 | provider 名称 |
| `auth_type` | string | 否 | 默认为 `api_key`；OAuth 上游填 `oauth` |
| `base_url` | string | 仅 API Key | 上游 API Base URL；OAuth provider 不允许设置 |
| `api_key` | string | 仅 API Key | 单个 API Key |
| `api_keys` | array | 仅 API Key | 多个 API Key，按顺序使用 |
| `oauth_provider` | string | 仅 OAuth | 支持的组合是：`openai.yaml` 用 `codex`，`claude.yaml` 用 `claude`，`gemini.yaml` 用 `antigravity` |
| `oauth_ref` | string | 仅 OAuth | 指向本地 OAuth 凭据文件的引用 ID |
| `proxy_mode` | string | 否 | 该 provider 的上游代理模式；`default` 表示使用全局默认代理 |
| `proxy_url` | string | 否 | 当 `proxy_mode: custom` 时必填；支持 `http://`、`https://`、`socks5://` 和 `socks5h://` 代理 URL |
| `priority` | int | 否 | 数字越小优先级越高；省略或 `0` 时按 `1` 处理 |
| `enabled` | bool | 否 | 是否启用，默认 `true` |
| `model` | string | 否 | 对支持的 OpenAI / Claude 请求强制改写为这个上游模型名 |
| `reasoning_effort` | string | 否 | 仅 OpenAI。对 `/v1/responses*` 写入 `reasoning.effort`；对 chat/completions 仅替换请求中已存在的 `reasoning_effort` |
| `thinking_budget_tokens` | int | 否 | 仅 Claude。对支持的请求写入 `thinking = {type: "enabled", budget_tokens: ...}` |

### OAuth Provider 说明

OAuth provider 仍然放在同一个 `providers[]` 列表里，和 API-key provider 使用相同的顺序、置顶、启停与 failover 逻辑。

- 当前支持：`openai.yaml` 中的 `auth_type: oauth` + `oauth_provider: codex`；`claude.yaml` 中的 `auth_type: oauth` + `oauth_provider: claude`；`gemini.yaml` 中的 `auth_type: oauth` + `oauth_provider: antigravity`。历史 `oauth_provider: gemini` 配置仍可加载以保持兼容，但新的 Gemini CLI OAuth 授权入口不再提供。
- 当前协议范围：Codex OAuth 支持 OpenAI `/v1/responses*`；Claude OAuth 支持 `/v1/messages` 和 `/v1/messages/count_tokens`；Antigravity OAuth 支持 Gemini-compatible `generateContent`、`streamGenerateContent`、`countTokens`、模型列表，以及通过 `generateContent` 调用 Gemini 图片模型。它不包装 Imagen/Veo `predict*` 端点；这些路由请使用 Google AI Studio API key 或 Vertex provider。
- Claude OAuth 请求默认使用轻量 Agent SDK 兼容 envelope，并由 Clipal 生成当前传输 header 和 billing fingerprint。
- Codex OAuth Responses 请求会归一化为轻量 Agent SDK 兼容形态，并由 Clipal 处理必要传输字段。
- 默认值不会覆盖目标模型支持的客户端显式字段。如果请求中已经带了 `tools`、Claude `thinking` / `context_management` / `output_config`，或 Codex `reasoning` / `tool_choice` / `parallel_tool_calls`，Clipal 会保留这些值；不支持相关能力的 Claude 模型仍可能移除不兼容的 thinking/output 控制。
- OAuth provider 不允许设置 `base_url`、`api_key`、`api_keys`
- 推荐在 Web UI 里，在对应客户端页面通过 `Add Provider -> OAuth -> Codex`、`Claude` 或 `Antigravity` 直接发起授权
- Add Provider 对话框也可以导入已有 OAuth 授权文件：Codex CLI 的 `auth.json`（`~/.codex/auth.json`）、CLIProxyAPI 单账号 OAuth JSON、sub2api 导出的 JSON。导入时会按当前选择的 OAuth 服务过滤账号。
- 授权成功后，后端会根据账号身份生成稳定的内部 `name`，UI 里显示邮箱作为可读标签
- OAuth 凭据不写入 YAML，而是保存在 `~/.clipal/oauth/<provider>/<email>--<oauth_ref>.json`
- 只要存在 `refresh_token`，Clipal 会在 access token 临近过期时自动刷新；如果上游先返回 `401`，也会强制 refresh 后再重试一次
- 已创建的 OAuth provider 可以像普通 provider 一样排序、置顶、启停，但凭据修改需要重新授权，不走通用编辑表单

## 使用建议

- 只有一个 key 时用 `api_key`
- 需要同 provider 多 key 轮转时用 `api_keys`
- 需要统一默认代理时，优先配置全局 `upstream_proxy_mode` / `upstream_proxy_url`，并让 provider 使用 `proxy_mode: default`
- 需要让某个 provider 绕过全局默认代理和环境代理时，用 `proxy_mode: direct`
- 不同上游对同一模型族使用不同模型 ID 时，可为该 provider 配置 `model`
- 只有在你希望 Clipal 按 provider 覆盖客户端默认思考参数时，才配置 `reasoning_effort` 或 `thinking_budget_tokens`
- Google AI Studio / Gemini API key provider 使用 `https://generativelanguage.googleapis.com`。Clipal 会透传 Gemini 的 `generateContent`、流式生成、token 计数、embeddings、Imagen/Veo `predict*`、文件、cached contents、tuned models、`interactions`、batches、operations、file search stores、generated files、corpora、auth tokens、agents 和 webhooks，不改写请求体。Gemini Live API WebSocket 代理暂不支持。
- Vertex Gemini REST provider 使用区域 base URL，例如 `https://us-central1-aiplatform.googleapis.com`。把短期 OAuth / service account access token 放在 `api_key` 或 `api_keys` 里；Clipal 对 Vertex host 会用 `Authorization: Bearer ...` 转发。
- 常驻后台运行时，建议：

```yaml
log_stdout: false
log_retention_days: 7 # 0 表示永久保留
```

- 面向局域网暴露代理前，请先明确安全边界；默认建议保持：

```yaml
listen_addr: 127.0.0.1
```

## 相关文档

- [路由与故障切换](routing-and-failover.md)
- [Web UI 使用说明](web-ui.md)
- [后台服务、状态与更新](services.md)
