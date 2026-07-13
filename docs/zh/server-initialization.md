# 服务器初始化

`clipal init` 是面向服务器账号的一次性辅助命令：它检测所选 AI CLI，仅在缺失时调用其官方安装器，启动 Clipal 用户服务，并复用 Clipal 现有的安全用户级接管能力。它不是 CLI 版本管理器，也不托管远程终端。

```bash
clipal init
clipal init --tools codex,claude,antigravity
clipal init --dry-run
```

如果从本机迁移，请先导出完整备份并传到服务器，再在初始化时恢复：

```bash
# 本机
clipal export -o clipal-data.json
scp clipal-data.json user@server:/tmp/

# 服务器
clipal init --import /tmp/clipal-data.json
```

`--import` 以 replace 模式恢复 Clipal 的 Provider、OAuth 凭据和运行配置；随后初始化会为服务器当前 Unix 用户重新执行可用 CLI 的 takeover。它不会把本机的 CLI 配置当作服务器上已生效的配置。

默认工具是 Codex CLI、Claude Code 与 Antigravity CLI（`agy`）。Gemini CLI 保持支持，可通过 `--tools gemini` 显式选择。

Codex、Claude 与 Gemini 继续使用已有的预览、备份、apply 和 rollback 接管实现。Antigravity 会被安装并可进入官方远程登录流程；在确认 `agy` 的官方代理配置契约前，Clipal 不会声称已完成其代理接管。

## 远程访问

管理 UI 仍只允许 localhost 访问，请使用 SSH 隧道，而不是暴露管理端口：

```bash
ssh -L 3333:127.0.0.1:3333 user@server
```

随后在本机打开 `http://127.0.0.1:3333/` 配置 Provider 或完成 OAuth。通过 SSH 运行 `agy` 时，按其输出的远程登录 URL 完成授权。

如需主动开放代理或管理 UI，请在全局设置中开启对应的 `allow_remote_proxy` 或 `allow_remote_web_ui`。两者默认关闭；远程访问没有内置认证。

## 安全边界

- 请用将来实际运行 AI CLI 的同一 Unix 用户执行 `clipal init`。
- 已存在的 CLI 不会被重复安装。
- init 不会输出或传输 API Key / OAuth 凭据。
- 通过 CLI Takeover 页面可预览或回滚已经完成的 Codex、Claude、Gemini 接管。
