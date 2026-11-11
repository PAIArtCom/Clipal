# Linux 使用指南

English: [docs/en/linux.md](../en/linux.md) | 中文: [docs/zh/linux.md](linux.md)

这页只保留 Linux 相关差异。通用初始化和配置说明见 [快速开始](getting-started.md)。

## 安装二进制

一行命令安装：

```bash
curl -fsSL https://clipal.paiart.com/install.sh | sh
```

脚本会下载对应平台的 release 文件，用 `checksums.txt` 校验后安装到
`$HOME/.local/bin`。如果需要系统范围安装，可以设置
`CLIPAL_INSTALL_DIR=/usr/local/bin`，但脚本不会调用 `sudo`，请确保目录可写，
或按下面的方式手动安装。

也可以从 Releases 下载：

- `clipal-linux-amd64`
- `clipal-linux-arm64`

示例：

```bash
chmod +x ./clipal-linux-amd64
mkdir -p "$HOME/.local/bin"
install -m 0755 ./clipal-linux-amd64 "$HOME/.local/bin/clipal"
clipal --version
```

请确认 `"$HOME/.local/bin"` 已加入 `PATH`（例如将
`export PATH="$HOME/.local/bin:$PATH"` 加入 shell 配置文件）。推荐使用这个
用户目录，因为 `clipal update` 可以在不需要管理员权限的情况下替换二进制文件。

如果需要系统范围安装，也可以使用 `/usr/local/bin`，但写入该目录需要 `sudo`：

```bash
sudo install -m 0755 ./clipal-linux-amd64 /usr/local/bin/clipal
clipal --version
```

## 临时后台运行

如果只是临时放后台：

```bash
nohup clipal >/dev/null 2>&1 &
```

这适合短期使用，不是长期推荐方案。

## 长期后台运行：systemd user service

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
```

## 手动 systemd 配置

如果你想手动维护 unit，可创建：

```text
~/.config/systemd/user/clipal.service
```

下面的示例假设使用推荐的用户目录安装。如果安装到了 `/usr/local/bin`，请将
`ExecStart` 改为对应路径。

最小示例：

```ini
[Unit]
Description=clipal local proxy
After=network.target

[Service]
Type=simple
ExecStart=%h/.local/bin/clipal --config-dir %h/.clipal
Restart=always
RestartSec=2

[Install]
WantedBy=default.target
```

启动并启用：

```bash
systemctl --user daemon-reload
systemctl --user enable --now clipal.service
```

## 日志建议

长期运行建议在 `config.yaml` 中设置：

```yaml
log_stdout: false
log_retention_days: 7
```

查看日志常用两种方式：

- Clipal 自己的轮转日志
- `journalctl --user -u clipal.service -e`

## Linux 常见问题

- 端口被占用：修改 `port` 或运行时用 `--port`
- 若二进制放在用户目录，记得对应修改 `ExecStart`

更多通用问题见 [排障与 FAQ](troubleshooting.md)。
