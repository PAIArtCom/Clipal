# Web UI 使用说明

## 访问方式

启动 Clipal 后，在浏览器打开：

```text
http://127.0.0.1:3333/
```

如果你修改了端口，请把地址中的 `3333` 换成你的实际端口。

当前所在分区和客户端会记录在 URL 的 hash 中（例如 `#/providers/openai`、`#/status`），刷新页面或收藏后可回到同一视图。

## Web UI 能做什么

### Providers

- 按客户端分组查看 provider
- 新增、编辑、删除 API-key provider
- 启用或禁用 provider
- 调整 `auto` / `manual` 模式
- 在 `manual` 模式下固定某个 provider
- 录入单个 `api_key` 或多行 `api_keys`
- 配置 provider 级代理模式和自定义代理 URL
- 配置 provider 级模型覆盖
- 配置 provider 级 OpenAI `reasoning_effort` 或 Claude `thinking_budget_tokens`
- 对支持的客户端 / 服务组合发起 OAuth 授权
- 在 Add Provider 对话框里导入 OAuth 授权文件。当前支持 Codex CLI 的 `auth.json`（`~/.codex/auth.json`）、CLIProxyAPI 单账号 OAuth JSON，以及 sub2api 导出的 JSON；Clipal 只会导入与当前所选服务匹配的账号。
- 对 Claude 和 Codex OAuth 请求默认使用 Agent SDK 兼容上游包装，并由 Clipal 处理必要传输字段。
- 在 provider 卡片上查看 OAuth 鉴权状态和最近刷新摘要
- 为支持的 OAuth provider 加载套餐和限额详情（当前仅 Codex）

### Global Settings

- 修改监听地址和端口
- 分别开启远程代理访问或远程 Web UI 访问；两者默认均关闭
- 修改日志级别
- 配置自动恢复、超时、请求体限制
- 配置熔断器
- 配置日志目录、保留天数、stdout 输出
- 配置桌面通知
- 有未保存的修改时会给出标记，离开页面前会询问是否放弃
- 仅在重启后生效的字段（监听地址、端口）带有“需重启”标签
- 开启远程 Web UI 访问时，保存前会要求确认，因为管理界面没有内置认证

### System Status

- 查看版本和运行时长
- 查看配置目录
- 查看各客户端当前模式、固定 provider、当前优先 provider
- 查看最近切换事件和最近请求结果
- 查看每个 provider 的运行态、已配置 key 数、可用 key 数

### Services

- 调用 `clipal service` 的常见能力
- 安装、启动、停止、重启、卸载后台服务
- 查看后台服务状态

### CLI Takeover

- 在 UI 中查看当前支持的一键接管项
- 一键接管 `Claude Code`、`Codex CLI`、`OpenCode`、`Gemini CLI`、`Continue`、`Aider` 和 `Goose` 的用户级配置
- 首次接管前自动创建备份，便于后续回滚
- 一键回滚到接管前捕获的原始文件状态
- 展示 Clipal 实际管理的目标配置文件路径
- 在 apply 前预览“当前文件”和“Apply 后将写入的结果”

说明：

- 当前阶段只修改用户级配置
- `Claude Code` 的一键接管会更新 `~/.claude/settings.json` 里的 `ANTHROPIC_BASE_URL`，只有缺少 Claude 客户端认证环境变量时才补占位 `ANTHROPIC_API_KEY`，必要时也会把 `~/.claude.json` 的 onboarding 标记补成完成；已有 `ANTHROPIC_API_KEY` 和 `ANTHROPIC_AUTH_TOKEN` 不会被覆盖
- `OpenCode` 的一键接管会新增或更新 `provider.clipal`，并把当前 `model` 改写为 `clipal/<当前模型ID>`
- `Gemini CLI` 的一键接管只会更新 `~/.gemini/.env` 里的 `GEMINI_API_BASE`
- `Continue` 的一键接管会新增或更新用户级 `Clipal` 模型项
- `Aider` 的一键接管会更新 home 级 `openai-api-base`，并补一个最小 OpenAI 兼容 `model`；已有 `openai-api-key` 会保留，只有缺失时才自动补占位值 `clipal`
- `Goose` 的一键接管会管理 `~/.config/goose/custom_providers/` 下的独立 custom provider 文件
- 项目级、受管控或企业策略下发的配置仍可能覆盖最终生效结果
- 对已经由 Clipal 接管的配置重复执行 apply 会尽量保持幂等，不覆盖最初备份
- Apply 或 Rollback 后，建议重启客户端或新开一个会话，让它重新加载用户级配置

### 导出完整备份

- 在“备份与导入”页签的“导出完整备份”面板中下载完整的 `clipal.data/v1` JSON 备份
- 备份包含全局与客户端配置、Provider API Key、OAuth access 和 refresh token，以及用量历史

### 导入数据

- 在“备份与导入”页签的“导入数据”面板中预览并执行原生备份或受支持的外部凭据导入

## 状态页里常见的 provider 状态

- `disabled`：配置里手动禁用了
- `unavailable`：当前因为鉴权、计费、额度问题，或没有可用 API Key 而不可用
- `cooling_down`：因为可恢复错误或熔断打开而处于冷却期
- `recovery_probe`：Clipal 正在用有限流量探测这个 provider 是否已经恢复

## 安全边界

- Web UI 默认只允许本机访问
- 如用户在全局设置明确开启 `allow_remote_web_ui`，管理界面可远程访问；此模式没有内置认证，请使用防火墙、VPN 或反向代理保护
- 管理 API 设计为本机使用，不提供独立认证层
- 变更类 API 请求要求 `X-Clipal-UI: 1`
- 带请求体的变更类 API 需要 `Content-Type: application/json`
- UI 不会直接展示每个 API Key 的明文

## 哪些修改通常立即生效

大多数配置变更会被热加载，无需重启，例如：

- provider 列表
- 优先级
- `mode`
- 失败切换相关参数
- 请求体限制
- 通知配置

通常需要重启才能完全生效的项目：

- 监听地址
- 监听端口
- 某些日志输出目标相关设置

## OAuth 账号请求环境

添加新 OAuth 账号时提供“**请求国家 / 地区**”和“**请求时区（IANA）**”，预填 **US / `America/New_York`**。它们是请求配置，不能证明账号注册地或账单国家。纽约的 IANA 时区自动处理夏令时，不保存固定 UTC−5 偏移。未修改的默认值保存 `source: default`，修改后的选择保存 `source: user`。

配置绑定 OAuth 账号，同一账号关联的 provider 共享设置。刷新、重新授权、重启和切换代理均保留已有配置。导入保留原值及来源，不应用新账号默认值。没有可信来源的旧值保留显示，并标记 `needs_confirmation: true`；它们可能来自出口估算，用户需要在账号地球按钮中检查、修改并明确保存。确认前，地区字段不应用到请求元数据。读取、导入预览和导出不会写回迁移。凭据中的上游地区属性单独记录和展示，不覆盖请求配置。

设备标识由版本命名空间、OAuth 服务及账号 ID 做 SHA-256 生成。缺少账号 ID 时使用稳定账号身份或 ref，诊断会说明。仅含 token 的旧账号也可通过该回退身份确认请求配置，不改变 provider 的账号关联身份。已确认的账号语言用于 `Accept-Language`，支持的环境头及适配器元数据使用账号配置；用户消息、工具和会话 ID 保持原有含义。Claude metadata 修改后重新签名 billing hash；故障切换采用当前所选账号的配置。

代理来源遵循 provider 配置、继承的全局配置、环境策略及 `NO_PROXY`。Clipal 实现 HTTP CONNECT、HTTPS 代理和 SOCKS5，所有代理请求保留目标域名，由代理解析和分流。VMess/VLESS 等由代理软件处理。Clipal 不探测专用控制器，不自动发现或接管代理监听端口。TUN 对 Clipal 透明，未指定代理入口不能证明实际为物理直连。

账号 DNS 默认 **auto**，按每个目标的实际策略（含 `NO_PROXY`）选择：有标准代理时保留目标域名，由代理解析，不依赖公共 DoH；无显式代理时使用 **Cloudflare → Google DoH**、固定服务引导 IP 和返回的目标 IP，IPv4/IPv6 连接以 250 毫秒间隔竞速，多地址尝试共享有限预算。必需的 DoH 失败不回退系统目标 DNS，代理连接失败不回退直连目标。无显式代理不能证明物理直连，因为 TUN 仍可能生效。代理自身为域名时，第一跳可能使用系统解析。现有 DNS 值保持原样：无代理时，`system` 使用系统解析，`cloudflare`／`google` 使用指定 DoH 服务；有标准代理时，无论保存哪个值都由代理解析，均不启用严格预检。DNS 策略按请求计算，不改写账号身份、地区、token 或保存的 DNS 值。

“**检查一致性**”报告所选代理入口、由谁解析目标域名，以及不携带凭据的目标 HTTP 连接是否完成。收到 HTTP 状态只能证明 HTTP 连通性，不能证明 OAuth 认证。IP 参考服务可能报告自己的出口 IP、国家和时区；即使代理入口相同，也不能证明模型目标使用相同出口，因此目标出口保持“**尚未核验**”。观测不会初始化或修改账号地区。诊断不发送账号凭据、token 刷新或付费模型请求。用量元数据的总时间预算为 30 秒，包含无代理时必需的 DoH、刷新及上游请求。

“**代理下附加公共 DoH 参考诊断**”默认不勾选，仅对当前诊断请求生效。有标准代理时，在连通性检查之后，通过同一入口查询保存的 Cloudflare／Google 选择；`auto`／`system` 使用 Cloudflare → Google。结果不能证明代理实际 DNS 后端、目标 IP 或出口。失败单独显示为“**尚未核验**”，不阻断业务请求，不提供严格预检选项。参考查询最多占用 10 秒，受诊断总计 25 秒的期限约束，不携带账号凭据或环境请求头。

管理接口为 `GET` / `PUT /api/providers/<client>/<name>/oauth-environment` 和 `POST /api/providers/<client>/<name>/oauth-diagnostics`。POST 可使用空请求体或 `{ "check_doh": false }`；`{ "check_doh": true }` 开启代理参考查询。无显式代理时忽略此选项，常规检查使用账号实际 DNS 策略。GET 和诊断不改变配置；PUT 明确确认或修改国家及 IANA 时区，核验已加载账号身份，保留 DNS 和 token，沿用 localhost 管理访问与 CSRF 控制。

## 相关文档

- [配置参考](config-reference.md)
- [路由与故障切换](routing-and-failover.md)
- [后台服务、状态与更新](services.md)
