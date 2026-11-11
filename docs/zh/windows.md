# Windows 使用指南

English: [docs/en/windows.md](../en/windows.md) | 中文: [docs/zh/windows.md](windows.md)

这页只保留 Windows 相关差异。通用初始化和配置说明见 [快速开始](getting-started.md)。

## 安装二进制

在 PowerShell 中一行命令安装：

```powershell
powershell -ExecutionPolicy ByPass -c "irm https://clipal.paiart.com/install.ps1 | iex"
```

脚本会下载对应平台的 release 文件，用 `checksums.txt` 校验后安装到
`%LOCALAPPDATA%\Programs\clipal`，并把这个目录加入当前用户的 `PATH`。
可以通过 `CLIPAL_INSTALL_DIR` 指定其他目录。

也可以从 Releases 下载：

- `clipal-windows-amd64.exe`

建议改名为 `clipal.exe`，放到例如：

```text
C:\Users\<YOU>\bin\clipal.exe
```

首次执行 `service install` 时，Clipal 会自动把这个目录加入当前用户的 `PATH`，并让
任务计划程序直接使用这个文件。安装完成后请重新打开 PowerShell，使新的 `PATH` 生效。
下载新版程序覆盖这个文件后，执行 `service install --force` 重新加载任务。

在 Windows 上，`service install` 会自动弹出系统 UAC 确认框请求提升权限。
用户点击“是”后会自动继续安装，不需要手动打开管理员 PowerShell，也不需要把密码输入
Clipal。`--dry-run` 不会弹出提权确认。

首次安装时请在二进制所在目录执行：

```powershell
.\clipal.exe service install
```

安装完成后重新打开 PowerShell，之后即可直接使用 `clipal` 命令。

确认：

```powershell
clipal --version
```

## 后台运行：任务计划程序

推荐使用内置命令：

```powershell
clipal.exe service install
clipal.exe service status
clipal.exe service restart
clipal.exe service stop
clipal.exe service uninstall
```

常用变体：

```powershell
clipal.exe service install --force
clipal.exe service install --config-dir C:\path\to\config
clipal.exe service install --dry-run
```

安装后的任务会使用 `--detach-console` 在后台运行，并在当前用户登录时启动。它使用
交互式用户会话，这是访问用户配置目录和本机网络端口所必需的；它不是“未登录也运行”
的 Windows 系统服务。

Clipal 允许任务在电池供电时启动，并在切换到电池供电后继续运行。安装、
`service start` 和 `service restart` 会自动应用这两项设置。旧版本用户升级后，
执行一次 `clipal.exe service restart` 即可修复已有任务的电池限制，无需手动修改
任务计划程序。这些命令使用 Windows PowerShell 及其内置的 ScheduledTasks 模块更新任务。

## 手动创建任务

如果你想自己维护任务计划：

- 程序：`C:\Users\<YOU>\bin\clipal.exe`
- 参数：`--detach-console --config-dir C:\Users\<YOU>\.clipal`
- 触发器：登录时
- 条件：取消勾选“只有在计算机使用交流电源时才启动此任务”和“如果计算机改用电池电源则停止”

## 作为 Windows Service

如果你需要“无需登录也运行”，可以用 NSSM 等第三方工具包装成真正的 Windows Service。

Clipal 自身内置的是任务计划程序方案，不直接内置 NSSM。

## 日志建议

后台运行建议在 `config.yaml` 中设置：

```yaml
log_stdout: false
log_retention_days: 7
```

## Windows 常见问题

- 任务计划程序运行账号和配置目录所属用户不一致
- `--config-dir` 没写对
- 端口与健康检查地址不一致
- 老版本出现权限位 Warning，多数是误报

更多通用问题见 [排障与 FAQ](troubleshooting.md)。
