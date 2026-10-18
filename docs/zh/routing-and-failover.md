# 路由与故障切换

## 路由前缀

Clipal 现在将客户端入口统一规范为这些本地路径：

| 路径 | 用途 |
|------|------|
| `/` | Web UI |
| `/health` | 健康检查 |
| `/clipal/*` | 首选统一入口，接收 Claude / OpenAI / Gemini 风格请求 |
| `/claudecode/*` | 兼容保留的 Claude 别名入口 |
| `/codex/*` | 兼容保留的 OpenAI 别名入口 |
| `/google/*` | Google 渠道入口，接收 Gemini 兼容请求 |
| `/gemini/*` | 兼容保留的 Gemini 别名入口 |

在 `/clipal/*` 下，Clipal 会根据上游路径自动识别请求协议族。旧别名仍保留给历史客户端配置使用。底层三组 backend client 仍然维持各自独立的 provider 列表和运行状态。

## Provider 选择顺序

每个客户端分组都会按下面的规则选择上游：

1. 先过滤掉 `enabled: false` 的 provider
2. 按 `priority` 从小到大排序
3. 同优先级保持配置文件中的顺序
4. 成功的 provider 会成为后续请求的优先候选，减少频繁切换

## `mode: auto`

这是默认模式。

行为：

- 当前 provider 成功时，继续优先使用它
- 当前 provider 失败时，尝试下一个可用 provider
- 如果同一个 provider 配置了多个 key，会先尝试该 provider 的下一个可用 key

适合场景：

- 主用 / 备用 provider
- 多个上游之间自动容灾

## `mode: manual`

行为：

- 只请求 `pinned_provider`
- 不自动切换到其他 provider
- 也不会在背后切换到同 provider 的其他 key
- 上游返回什么，Clipal 就直接透传什么

适合场景：

- 你需要临时强制锁定某个 provider 做调试或稳定复现

## 临时禁用与冷却

Clipal 会根据上游失败类型决定是否临时跳过某个 provider：

- `401` / `403`：鉴权或权限问题，通常会临时禁用
- `402`：额度或计费问题，通常会临时禁用
- `429`：会进一步判断
  - 配额或鉴权类：临时禁用
  - 速率限制或过载类：切下一个，但不一定长期禁用

被临时跳过的 provider 会在 `reactivate_after` 到期后自动恢复。

## 多 Key 行为

一个 provider 可以配置：

- `api_key`
- 或 `api_keys`

当配置了 `api_keys` 时：

- Clipal 会去重并保留顺序
- 在自动模式下，先尝试同 provider 的下一个可用 key
- 只有当前 provider 的 key 都不可用时，才会继续切到下一个 provider

## 熔断器

如果启用了 `circuit_breaker`：

- 连续失败达到阈值后，provider 会进入 `open`
- `open_timeout` 到期后进入 `half_open`
- 半开探测连续成功达到阈值后恢复为 `closed`

好处：

- 避免反复打到已经明显不健康的上游

## 当所有 provider 都不可用

如果当前客户端分组下没有可用 provider：

- Clipal 会返回失败
- 某些可重试场景下会带 `Retry-After`

## 热加载

配置文件变更会自动重载，通常无需重启。

热加载影响：

- provider 列表会按新配置重建
- 上一轮运行态中的临时禁用状态会被新的配置加载覆盖

## 相关文档

- [配置参考](config-reference.md)
- [Web UI 使用说明](web-ui.md)
- [排障与 FAQ](troubleshooting.md)
