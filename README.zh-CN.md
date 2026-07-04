# Clipal

<div align="center">
  <img src="assets/clipal-hancock5.jpeg" alt="Clipal" width="100%">
  <p><b>你的终极本地 LLM API 网关与管理器</b></p>
  <p>
    <a href="README.md">English</a> | <a href="README.zh-CN.md">中文</a>
  </p>
</div>

---

**Clipal** 是一款专为开发者生产力打造的本地 LLM API 反向代理与管理工具。如果你正在使用诸如 Claude Code、Continue、Aider 或 Cherry Studio 等 AI 工具，Clipal 将成为你的智能流量大管家。它将多个上游大模型服务统一收口，支持自动故障切换、API Key 轮询，并提供了一个美观的 Web UI——让你专注于写代码，而不是折腾配置文件。

## 微信交流群

扫描下方二维码加入 Clipal 微信交流群，或添加作者微信。

<div align="center">
  <table>
    <tr>
      <td align="center">
        <img src="assets/wechat-group.jpeg" alt="Clipal 微信交流群二维码" width="220"><br>
        <sub>微信群</sub>
      </td>
      <td align="center">
        <img src="assets/wechat-supporter.jpeg" alt="作者微信二维码" width="220"><br>
        <sub>作者微信</sub>
      </td>
    </tr>
  </table>
</div>

## ✨ 为什么选择 Clipal？

### 🚀 **一键 CLI 接管 (CLI Takeover)**
告别手动寻找、修改隐藏配置文件的烦恼。在 Web UI 中只需一键，Clipal 就能自动接管 **Claude Code、Codex CLI、OpenCode、Gemini CLI、Continue、Aider** 以及 **Goose** 的配置。
- 自动帮你配置本地 Base URL。
- 接管前自动备份原始配置。
- 随时支持一键回滚。

### 🛡️ **坚如磐石的故障切换与多 Key 轮询**
遇到并发限制、速率限制或者余额耗尽导致生成中断？
- **多 Key 轮询**：为单个 provider 配置多个 API Key，Clipal 会在同 provider 内自动重试并轮换 Key，直至成功。
- **优先级自动容灾**：当主模型/服务商不可用时，基于预设优先级秒级无缝切换到备用模型，自带断路器和并发阻断机制。
- **OAuth 上游**：可以在 Web UI 里授权接入 Codex、Claude 和 Antigravity-backed Google 账号，并继续复用同一套排序、置顶、启停与 failover 逻辑；只要有 `refresh_token`，Clipal 也会自动刷新 access token。Antigravity OAuth 支持 Gemini-compatible 文本、流式、token 计数、模型列表，以及通过 `generateContent` 调用 Gemini 图片模型；Imagen/Veo `predict*` 端点仍走 Google AI Studio API key 或 Vertex 路由。Claude 和 Codex OAuth 对普通客户端默认使用 Agent SDK 兼容包装，并由 Clipal 处理必要传输字段。请求里显式传入的 `tools`、thinking/reasoning、输出控制等字段在目标模型支持时优先于 Clipal 默认值。

### 🎛️ **美观且强大的本地 Web UI**
可视化管理你的 AI 工作流。在这里增删、停用 provider，或者在“手动模式”下置顶特定模型，亦或调整全局运行参数。所有更改**热重载**生效，无需重启服务。

![Clipal Web UI](assets/webUI.png)

### ⚡ **无感知的后台守护服务**
Clipal 编译为单文件二进制，跨平台支持 macOS、Linux 和 Windows。
只需敲入 `clipal service install` 和 `clipal service start`，它就会静默在后台永远为你跑着。想要查看状态或重启？用 `clipal status` 和 `clipal restart` 瞬间搞定。

---

## 🔌 广泛的客户端支持

Clipal 现已将所有客户端入口统一规范到单一路由：`http://127.0.0.1:3333/clipal`。
它原生支持智能识别和兼容以下请求风格：
- **Anthropic / Claude**
- **OpenAI / Codex**
- **Google Gemini**

**常见支持工具：**
- **AI 编程助手：** Claude Code、Codex CLI、OpenCode、Gemini CLI、Continue、Aider、Goose
- **桌面端 AI 聊天：** Cherry Studio、Kelivo、Chatbox、ChatWise (兼容 OpenAI API)

---

## ⚡ 快速开始

### 让你的 AI 帮你安装

如果你在使用 Claude Code、Codex CLI 等能操作终端的 AI，可以直接把下面这段话发给它：

```text
请帮我安装并启动 Clipal。项目地址：https://github.com/lansespirit/Clipal

请根据我当前的操作系统和架构，查看这个项目的 Releases 和文档，帮我完成下载、安装和启动，并确认我可以打开 Web UI 使用。需要时请参考这些官方链接：
- Releases: https://github.com/lansespirit/Clipal/releases
- 快速开始: https://github.com/lansespirit/Clipal/blob/main/docs/zh/getting-started.md
- Web UI: https://github.com/lansespirit/Clipal/blob/main/docs/zh/web-ui.md

安装完成后，再指导我在 Web UI 里完成一键接管，并添加第一个 provider。
```

### 1. 安装 Clipal
最快的方式是直接通过 npm 安装：

```bash
npm install -g clipal
clipal --version
```

如果你更偏好独立二进制，也可以前往 [Releases](https://github.com/lansespirit/Clipal/releases) 页面下载对应系统的文件，并放入环境变量 `PATH` 中。最新稳定版可直接使用 [GitHub Releases latest](https://github.com/lansespirit/Clipal/releases/latest)。

```bash
chmod +x clipal*
./clipal* --version
```

### 2. 运行与管理
在前台直接启动：
```bash
clipal
```
或者将其安装为后台服务：
```bash
clipal service install
clipal service start
```

### 3. 访问管理后台
打开浏览器访问 `http://127.0.0.1:3333/`，即可管理所有模型并为你的常用 AI 工具开启 **CLI Takeover**。Provider 可以直接在 Web UI 里添加，不需要手动预先复制配置文件。

---

## 📖 完整文档导航

深入了解 Clipal 的全部能力：
- 🚀 [快速开始](docs/zh/getting-started.md)
- 🔌 [客户端接入指南](docs/zh/client-setup.md)
- ⚙️ [配置参考](docs/zh/config-reference.md)
- 🖥️ [Web UI 使用说明](docs/zh/web-ui.md)
- 🔀 [路由与故障切换魔法](docs/zh/routing-and-failover.md)
- 🛠️ [后台服务、状态与更新](docs/zh/services.md)
- 📚 [文档首页](docs/zh/README.md) & [Release Notes](release-notes/)

## 🔒 隐私与安全

- **100% 本地运行**：默认仅监听 `127.0.0.1:3333`。
- **Web UI 隔离保护**：即使代理开启了对外网段访问 (`0.0.0.0`)，Web 管理界面也严格强制仅限本机 (loopback) 访问。
- **真 Key 替换**：你在 Clipal 中配置的上游 API Key 只存在本地，Clipal 会在代理时自动覆盖并注入到真正的请求中，你可以在客户端放心地填入任何占位符。
- **OAuth 凭据独立存储**：OAuth 账号凭据单独保存在本机 `~/.clipal/oauth/` 下，不写入 YAML provider 文件。

<div align="center">
  <img src="assets/Clipal-luffy2.jpeg" alt="Clipal" width="100%">
</div>

## 📄 License
MIT

## 🙏 致谢
感谢 [linux.do](https://linux.do/) 社区提供的支持。
