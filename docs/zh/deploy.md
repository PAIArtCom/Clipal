# 部署包

部署包用于把一台机器上已经可用的 Clipal 配置迁移到另一台机器。

当你已经在本地配置好了 provider，希望另一台机器直接复用同一套 provider URL、API Key、路由和全局设置时，可以使用部署包。目标是不打开 Web UI，也不重新手动输入密钥。

## 导出

```bash
clipal export
```

默认会读取普通 Clipal 配置目录（`~/.clipal`），并在当前目录写出 `clipal.json`。

如果要指定包名，传 `-o`。没有后缀时，Clipal 会自动补 `.json`；如果传了其他后缀，Clipal 会拒绝。

```bash
clipal export -o prod
```

如果要导出指定配置目录：

```bash
clipal export -o prod --config-dir /path/to/config
```

部署包会包含 provider URL 和 API Key。请把它当作敏感文件处理。

## 普通导入

```bash
clipal import clipal.json
```

普通导入会把包里的配置文件写入当前机器的 Clipal 配置目录。

如果要导入到指定目录：

```bash
clipal import prod.json --config-dir /path/to/config
```

如果导入时要同时执行 CLI Takeover：

```bash
clipal import prod.json --takeover claude,codex,gemini
```

导入后，用现有 service 命令启动或重启 Clipal：

```bash
clipal service install --config-dir /path/to/config
clipal service restart
```

## 临时导入

```bash
clipal import prod.json --temporary
```

临时导入会创建一个独立的临时配置目录，并把部署包里的文件写到那里。它不会覆盖普通 Clipal 配置目录。

命令会输出临时目录和 cleanup 命令。密钥仍然会写入这个临时目录，所以清理前需要先停止正在使用该目录的 Clipal 进程。

使用临时配置运行 Clipal：

```bash
clipal --config-dir /tmp/clipal-deploy-...
```

当前版本中，`--start` 只会打印对应的启动命令，不会自动安装或重启系统服务。

`--takeover` 会复用 Clipal 现有的一键接管实现，支持 `claude`、`codex`、`opencode`、`gemini`、`continue`、`aider` 和 `goose`。

## 一条命令部署 Agent

在新机器上安装好 Clipal 后，把 `clipal.json` 放到当前目录，然后执行：

```bash
clipal deploy
```

这个命令会：

1. 如果当前目录存在 `clipal.json`，先导入它。
2. 检查 `codex`、`claude`、`gemini` 是否已经安装。
3. 对缺失的 CLI 执行官方安装命令。
4. 调用 Clipal 现有的一键接管，把 Codex、Claude Code、Gemini CLI 指向 Clipal。

如果只想预览，不修改机器：

```bash
clipal deploy --dry-run
```

如果只部署部分 Agent：

```bash
clipal deploy codex
clipal deploy claude gemini
```

如果只想安装官方 CLI，不做 Clipal Takeover：

```bash
clipal install codex
clipal install claude
clipal install gemini
```

`clipal deploy <agent>` = 安装所选 Agent，并调用 Clipal 现有的一键接管。

`clipal install <agent>` = 只执行官方安装命令，不修改 Agent 配置。

也可以继续使用逗号参数：

```bash
clipal deploy --agents codex,claude
```

Clipal 不替换上游安装器。它只会直接执行官方安装命令；如果缺少 `bash`、`npm`、`sh`、`curl` 或 `powershell` 等官方命令依赖，会明确报错。

## 安全说明

- 部署包按设计会包含 API Key。
- 只把部署包移动到你信任的机器。
- 导入会拒绝不属于 Clipal 配置文件清单的文件名。
- 现有 Clipal 配置格式和路由行为不变。
