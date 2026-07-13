# 数据导入、导出与备份

Clipal 使用唯一的标准明文 JSON 文档完成完整备份：`clipal.data/v1`。它是数据格式，不是归档包，包含迁移或恢复私有 Clipal 实例所需的操作者自有数据：

- 全局配置和各客户端配置；
- API Key 与 OAuth Provider 定义；
- OAuth 凭据；
- 用量计数和费用历史。

OAuth 凭据会包含已有的 access token 和 refresh token。该明文导出是实例数据的完整迁移副本，归操作者所有。
数据导出 API 为 `/api/data/export`；旧的仅配置 `/api/config/export` 端点不属于这份新契约。

顶层契约如下：

```json
{
  "schema": "clipal.data",
  "schema_version": 1,
  "exported_at": "2026-07-12T12:00:00Z",
  "producer": { "name": "clipal", "version": "v0.x" },
  "data": {
    "global": {},
    "clients": {
      "claude": { "mode": "auto", "providers": [] },
      "openai": { "mode": "auto", "providers": [] },
      "gemini": { "mode": "auto", "providers": [] }
    },
    "credentials": [],
    "usage": { "clients": {} }
  }
}
```

这是全新的数据契约，旧版 Web UI 仅包含配置的 JSON 导出不会被兼容导入。

## CLI

```bash
# 完整备份；输出文件权限为 0600。
clipal export -o clipal-data.json

# 自动识别格式、输出计划，然后确认执行。
clipal import clipal-data.json

# 只检查和预览，不写入任何数据。
clipal import --dry-run clipal-data.json

# 显式合并原生备份并跳过交互确认。
clipal import --format clipal --mode merge --yes clipal-data.json
```

Clipal 原生数据默认采用 `replace`。选择 `merge` 时，全局设置使用导入值，Provider 按名称合并，凭据按账号身份合并，用量计数累加。

Schema v1 只定义 `claude`、`openai` 和 `gemini` 三个客户端键。未知客户端键和尾随的第二份 JSON 文档会直接被拒绝，不会被静默丢弃。Web UI 以原始文件文本传输所有整数用量与费用字段，因此完整的有符号 64 位整数不会发生精度损失。

同一条导入命令也支持 CLIProxyAPI 单账号 OAuth JSON、sub2api 导出 JSON 和 Codex `auth.json`。外部格式只导入凭据并且固定使用 `merge`；Clipal 会把账号关联到对应客户端，不会替换配置或用量。

导入接受普通文件（包括最终指向普通文件的符号链接）；单次最多 512 个文件，每个文件最大 16 MiB，所有原始文件数据合计最大 64 MiB。本地 CLI 导入与转交给运行实例的导入采用完全相同的限制。

## Web UI

打开 **全局设置**。在独立的“**导出完整备份**”面板中选择保存位置并下载 `clipal-data.json`；该备份包含全局与客户端配置、Provider API Key、OAuth access 和 refresh token，以及用量历史。在独立的“**导入数据**”面板中，选择一个原生备份，或选择一个/多个外部凭据 JSON 文件；点击“预览导入”会打开导入审查对话框，展示识别结果、导入模式、计划影响和警告，只能从该已审查对话框中执行导入。

预览阶段不会写入数据。若预览后所选数据或当前状态发生变化，Clipal 会拒绝执行并要求重新审查。执行前会同时快照配置、凭据和用量；如果持久化或运行时重载失败，Clipal 会恢复这三类数据。

CLI 检测到同一配置目录已有 Clipal 实例运行时，会通过该实例完成导出、预览和执行，使备份包含最新运行时数据，并让运行时遥测与导入用量进入同一个串行事务。如果配置的监听地址已被占用但无法确认实例身份，CLI 会拒绝回退到独立的本地写入。Web 导入直接复用运行实例的遥测 Store；导入期间完成的请求会等待事务结束，随后正常记账，不会被覆盖。OAuth 存储目录使用符号链接时，替换和回滚都会保留符号链接形态。
