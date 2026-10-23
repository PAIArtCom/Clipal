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
google.yaml
```

Google 渠道配置使用 `google.yaml`。加载时会将 `gemini.yaml` 重命名迁移，保留文件内容与权限；若两份配置等价则移除旧文件，内容不同时明确报错并保留两份。所有写入均使用 `google.yaml`，包括经由旧 `gemini` 管理 API 别名提交的修改。

`config.yaml` 是全局配置，其余三个文件分别管理不同客户端分组。

对应模板：

- [../../examples/config.yaml](../../examples/config.yaml)
- [../../examples/claude.yaml](../../examples/claude.yaml)
- [../../examples/openai.yaml](../../examples/openai.yaml)
- [../../examples/google.yaml](../../examples/google.yaml)

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

`openai.yaml`、`claude.yaml`、`google.yaml` 都支持 OAuth 上游。推荐通过 Web UI 的授权流程创建，而不是手写这些字段。

## 全局配置 `config.yaml`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `listen_addr` | string | `127.0.0.1` | 监听地址 |
| `allow_remote_proxy` | bool | `false` | 当 `listen_addr` 不是 loopback 地址时必须开启；显式允许远程客户端使用代理。 |
| `allow_remote_web_ui` | bool | `false` | 显式允许远程访问 Web UI 和管理 API。该模式没有内置认证，请使用防火墙、VPN 或反向代理保护。 |
| `port` | int | `3333` | 本地代理端口 |
| `log_level` | string | `info` | `debug` / `info` / `warn` / `error` |
| `reactivate_after` | duration | `1h` | provider 临时禁用后的自动恢复时间；设为 `0` 可禁用基于鉴权、计费、额度错误的临时禁用 |
| `upstream_idle_timeout` | duration | `5m` | 上游响应 body 长时间无字节时中断当前尝试；`0` 表示关闭，其他低于 `5m` 的正值会自动提升为 `5m` |
| `response_header_timeout` | duration | `5m` | 等待上游响应头的超时；`0` 表示关闭，其他低于 `5m` 的正值会自动提升为 `5m` |
| `upstream_proxy_mode` | string | `environment` | 作为默认值应用到 `proxy_mode: default` 的 provider；可选 `environment` / `direct` / `custom` |
| `upstream_proxy_url` | string | 空 | 当 `upstream_proxy_mode: custom` 时必填；支持 `http://`、`https://`、`socks5://` 和 `socks5h://` 代理 URL |
| `max_request_body_bytes` | int | `67108864` | 请求体大小上限，默认 64 MiB；低于该值会自动提升为该上限 |
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
- `google.yaml`

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
| `oauth_provider` | string | 仅 OAuth | 支持的组合是：`openai.yaml` 用 `codex`，`claude.yaml` 用 `claude`，`google.yaml` 用 `antigravity` |
| `oauth_ref` | string | 仅 OAuth | 指向本地 OAuth 凭据文件的引用 ID |
| `proxy_mode` | string | 否 | 该 provider 的上游代理模式；`default` 表示使用全局默认代理 |
| `proxy_url` | string | 否 | 当 `proxy_mode: custom` 时必填；支持 `http://`、`https://`、`socks5://` 和 `socks5h://` 代理 URL |
| `priority` | int | 否 | 数字越小优先级越高；省略或 `0` 时按 `1` 处理 |
| `enabled` | bool | 否 | 是否启用，默认 `true` |
| `model` | string | 否 | 对支持的 OpenAI / Claude 请求强制改写为这个上游模型名 |
| `openai.reasoning_effort` | string | 否 | 仅 OpenAI。Responses 写入 `reasoning.effort`；Chat Completions 仅替换请求中已有的 `reasoning_effort`。可用档位因模型而异：`none`、`minimal`、`low`、`medium`、`high`、`xhigh`、`max`。 |
| `claude.effort` | string | 否 | 仅 Claude。使用 adaptive thinking 写入 `output_config.effort`。可用档位因模型而异：`low`、`medium`、`high`、`xhigh`、`max`。 |
| `google.thinking_level` | string | 否 | 仅 Gemini `generateContent`。写入 `generationConfig.thinkingConfig.thinkingLevel`。可用级别因模型而异：`minimal`、`low`、`medium`、`high`。 |

### OAuth Provider 说明

OAuth provider 仍然放在同一个 `providers[]` 列表里，和 API-key provider 使用相同的顺序、置顶、启停与 failover 逻辑。

- 当前支持：`openai.yaml` 中的 `auth_type: oauth` + `oauth_provider: codex`；`claude.yaml` 中的 `auth_type: oauth` + `oauth_provider: claude`；`google.yaml` 中的 `auth_type: oauth` + `oauth_provider: antigravity`。Google OAuth 仅支持 `antigravity`；加载配置时会排除已移除的 `oauth_provider: gemini` 条目，其凭据文件保留在磁盘上。
- 当前协议范围：Codex OAuth 支持 OpenAI `/v1/responses*` 和模型列表：OpenAI 兼容客户端使用 `/v1/models`；原生 Codex 客户端使用 `/models?client_version=...` 并接收 Codex catalog schema。Claude OAuth 支持 `/v1/messages` 和 `/v1/messages/count_tokens`；Antigravity OAuth 支持 Gemini-compatible `generateContent`、`streamGenerateContent`、`countTokens`、模型列表，以及通过 `generateContent` 调用 Gemini 图片模型。它不包装 Imagen/Veo `predict*` 端点；这些路由请使用 Google AI Studio API key 或 Vertex provider。
- Claude OAuth 请求默认使用轻量 Agent SDK 兼容 envelope，并由 Clipal 生成当前传输 header 和 billing fingerprint。
- Codex OAuth Responses 请求会归一化为轻量 Agent SDK 兼容形态，并由 Clipal 处理必要传输字段。
- 内置 OAuth 传输身份当前对应 Codex CLI `0.156.0`、Claude Code `2.1.280` 和 Antigravity 2.0 `2.13.0`。macOS 上 Antigravity 优先读取已安装应用的版本；受控部署可通过 `CLIPAL_OAUTH_ANTIGRAVITY_VERSION` 覆盖。
- 默认值不会覆盖目标模型支持的客户端显式字段。如果请求中已经带了 `tools`、Claude `thinking` / `context_management` / `output_config`，或 Codex `reasoning` / `tool_choice` / `parallel_tool_calls`，Clipal 会保留这些值；不支持相关能力的 Claude 模型仍可能移除不兼容的 thinking/output 控制。
- OAuth provider 不允许设置 `base_url`、`api_key`、`api_keys`
- 推荐在 Web UI 里，在对应客户端页面通过 `Add Provider -> OAuth -> Codex`、`Claude` 或 `Antigravity` 直接发起授权
- Antigravity 授权需要 Code Assist 返回、开户完成后分配或用户明确提供的非空项目 ID。所选档位若有 `userDefinedCloudaicompanionProject: true`，就需要用户拥有权限的项目，Clipal 不会对该档位尝试托管项目开户。现有项目选择依次使用 `CLIPAL_OAUTH_ANTIGRAVITY_PROJECT_ID`、`GOOGLE_CLOUD_PROJECT`／`GOOGLE_CLOUD_PROJECT_ID` 或已保存的项目元数据。其他档位的限制不会否定已分配的项目或可用的所选档位。请求地区偏好不能证明 Google 账号资格。
- Antigravity 开户会在同一服务地址轮询命名 operation，保留自身客户端身份、检查 operation 错误，并拒绝项目为空的完成结果。未命名的待完成响应会有限重试。已有凭证缺少项目元数据时，有效 access token 直接用于补全元数据，不额外换 token 或延长 token 寿命；没有 refresh token 的有效凭证也可补全。过期或被拒绝的 access token 才需刷新。补全成功保留 Provider 关联与账号偏好，仅补项目失败时不覆盖原凭证。已有账号若已成功刷新 token，且 userinfo 确认身份一致，即使随后遇到已知项目／资格限制，也会保存刷新结果，保留轮换后的 refresh token；仍返回初始化错误，不虚构项目，也不会将缺少可用项目的新授权保存为成功账号。
- 已知的 Antigravity 初始化限制会在模型生成前返回：需要用户项目为 HTTP 409，所选档位不可用或需要账号验证为 HTTP 403。自动路由仍可选择其他可用 Provider；只有本次没有 Provider 能继续、且失败均为已知初始化限制时，才返回相应错误。公开错误只包含初始化事实和原因码，不包含凭据、原始上游响应或验证链接。
- Add Provider 对话框也可以导入已有 OAuth 授权文件：Codex CLI 的 `auth.json`（`~/.codex/auth.json`）、CLIProxyAPI 单账号 OAuth JSON、sub2api 导出的 JSON。导入时会按当前选择的 OAuth 服务过滤账号。
- 授权成功后，后端会根据账号身份生成稳定的内部 `name`，UI 里显示邮箱作为可读标签
- OAuth 请求配置保存在凭据 `request_environment` 中。添加账号默认 US / `America/New_York`，来源为 `default` 或 `user`；导入保留原值，旧地区需要确认。代理使用 provider、全局、环境和系统配置及 `NO_PROXY`，不探测控制器。账号 DNS 的 `auto` 有标准代理时由代理解析；无代理入口时优先尝试 Cloudflare → Google DoH 及目标 IP，解析或连接建立失败后回退系统域名通路。Clipal 调整自身连接路径，不修改用户网络配置。保存的 `cloudflare`／`google` 值选择无代理时的 DNS 及可选诊断服务，不启用严格代理预检。代理 DoH 参考检查仅是单次诊断选项，不作为账号配置保存。见 [Web UI](web-ui.md#oauth-账号请求环境)。
- OAuth 凭据不写入 YAML，而是保存在 `~/.clipal/oauth/<provider>/<email>--<oauth_ref>.json`
- 只要存在 `refresh_token`，Clipal 会在 access token 临近过期时自动刷新；如果上游先返回 `401`，也会强制 refresh 后再重试一次
- 已创建的 OAuth provider 可以像普通 provider 一样排序、置顶、启停，但凭据修改需要重新授权，不走通用编辑表单

## 使用建议

- 只有一个 key 时用 `api_key`
- 需要同 provider 多 key 轮转时用 `api_keys`
- 需要统一默认代理时，优先配置全局 `upstream_proxy_mode` / `upstream_proxy_url`，并让 provider 使用 `proxy_mode: default`
- 需要让某个 provider 绕过全局默认代理、环境代理和系统代理时，用 `proxy_mode: direct`
- `environment` 按目标协议依次使用 HTTP_PROXY／HTTPS_PROXY（含小写形式）、ALL_PROXY 和系统设置；NO_PROXY 同时适用于环境与系统代理。macOS 原生构建通过 CFNetwork 支持 SOCKS、例外规则和 PAC；Windows 使用 WinHTTP；Linux 在可用时读取 GNOME 手动代理，否则沿用环境变量。macOS 非 CGO 构建通过 scutil 支持静态代理。GNOME PAC 明确报不支持，不绕过代理。显式 direct／custom 优先。每次请求只读取设置，不修改网络或探测代理软件。
- 不同上游对同一模型族使用不同模型 ID 时，可为该 provider 配置 `model`
- 只有在你希望 Clipal 按 provider 覆盖客户端默认推理／思考参数时，才配置对应渠道的档位；可用档位因模型而异
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
