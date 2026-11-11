# macOS 使用指南

English: [docs/en/macos.md](../en/macos.md) | 中文: [docs/zh/macos.md](macos.md)

这页只保留 macOS 相关差异。通用初始化和配置说明见 [快速开始](getting-started.md)。

## 安装二进制

一行命令安装：

```bash
curl -fsSL https://clipal.paiart.com/install.sh | sh
```

脚本会下载对应平台的 release 文件，用 `checksums.txt` 校验后安装到
`$HOME/.local/bin`。可以通过 `CLIPAL_INSTALL_DIR` 指定其他目录。

Apple Silicon:

- `clipal-darwin-arm64`

Intel:

- `clipal-darwin-amd64`

推荐安装方式之一：

### 方式 A：放到 `~/bin`

```bash
mkdir -p ~/bin
mv ~/Downloads/clipal-darwin-arm64 ~/bin/clipal
chmod +x ~/bin/clipal
```

如有需要，把 `~/bin` 加入 `PATH`：

```bash
echo 'export PATH="$HOME/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

### 方式 B：放到 Homebrew 路径

```bash
sudo mv ~/Downloads/clipal-darwin-arm64 /opt/homebrew/bin/clipal
sudo chmod +x /opt/homebrew/bin/clipal
```

## 后台运行：launchd

推荐使用内置命令：

```bash
clipal service install
clipal service status
clipal service restart
clipal service stop
clipal service uninstall
```

常用变体：

```bash
clipal service install --force
clipal service install --config-dir /path/to/config
clipal service install --stdout ~/.clipal/logs/launchd.out --stderr ~/.clipal/logs/launchd.err
```

内置命令会写入：

```text
~/Library/LaunchAgents/com.lansespirit.clipal.plist
```

## 手动管理 launchd

如果你想完全自定义 plist，也可以自己维护 LaunchAgent。

常用命令：

```bash
launchctl bootstrap "gui/$(id -u)" ~/Library/LaunchAgents/com.lansespirit.clipal.plist
launchctl kickstart -k "gui/$(id -u)/com.lansespirit.clipal"
launchctl bootout "gui/$(id -u)" ~/Library/LaunchAgents/com.lansespirit.clipal.plist
```

## 日志建议

后台运行建议在 `config.yaml` 中设置：

```yaml
log_stdout: false
log_retention_days: 7
```

这样可避免 Clipal 日志和 launchd stdout 重复。

## macOS 常见问题

- “有请求但我没打开 Claude Code”：通常是编辑器扩展或后台进程在重试
- 如果需要排查端口占用，可用：

```bash
lsof -nP -iTCP:3333
```

更多通用问题见 [排障与 FAQ](troubleshooting.md)。
