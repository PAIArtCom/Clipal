# Landing page 第二轮设计重构执行计划

日期：2026-11-16。范围：Clipal 静态营销站的中英文页面、共享 CSS/JS 和相关设计规范。此文件也是执行代理的上下文交接；无需重新扫描全仓库。

## 1. 已完成的定位与诊断

- 基线 HEAD：`6548dfa`。工作区开始时干净。
- `7961d6e`（2026-11-13）整页重构：定义 Kinetic Structuralism / Quantum Control Plane，增加 18 客户端矩阵、Diff 模态框、四支柱、真实截图 Tour、安装和社区；同时修改品牌哲学和中英文设计系统。
- `6548dfa` 仅修补 Diff 高亮，没有改变信息架构或核心视觉。
- 规划来源：`docs/zh/design-system.md`（97 行）、`docs/en/design-system.md`（83 行）、`assets/clipal-icon-philosophy.md`。这些是首轮美术意图，尚没有转化目标、响应式验收和事实校验。
- 现有实现：`landing-page/index.html`、`landing-page/zh/index.html` 各 1093 行；`css/style.css` 1275 行；`js/main.js` 266 行。纯 HTML/CSS/原生 JS，无框架或构建依赖。

### 具体问题及影响

1. **叙事优先级颠倒。** H1 抽象，首段堆积 AST、协议、OAuth、计价；首个正文是 18 个高密度卡片，产品截图直到第四个大区块才出现。访客很难快速理解自己的收益和下一步。
2. **视觉语言停留在重复面板。** 全页近似的黑底、细框、小等宽标签和长标题缺少构图节奏。三维装饰终端并非真实产品，真实 UI 没有成为视觉主角。
3. **首屏响应式错误。** CSS 在 max-width:1024px 将 `.hero-console-viewport` 设为 `order:-1`，现场窄视口截图可见模拟终端占据标题前约 400px。导航在 768px 以下直接隐藏，无替代菜单；长标签、代码、浮层和 1024px 左右导航存在挤压风险。
4. **信任内容与产品事实冲突。** 页面/JSON-LD 声称 AES 加密 config.json、所有格式通用无损 AST、sub-ms failover、零开销、固定内存/延迟和竞品性能；文档实际是 YAML provider 配置及独立 OAuth 凭证目录。现有 comparison 表包含未经证实的竞品否定与数字。OAuth 的 Region Spoofing / biometrics 等表述也缺乏依据。
5. **演示误作实时数据。** 导航显示 GATEWAY ACTIVE；JS 无限追加虚构日志并随机增加费用，无演示标记；hero 命令 `clipal service start --unified-route=/clipal` 无现有快速入门依据。
6. **交互与可访问性不完整。** CLI 卡是 article 加 click，无原生键盘操作；tab 只切 class，无 selected/roving focus；模态框无 Escape/焦点管理；复制失败仅 console.error，连续点击可能覆盖反馈。无 reduced-motion 支持。
7. **重复维护与漂移。** 两个语言页内联大量相似图标、配置样例与 style，JS 又持有第二份双语文案。新版本应收敛结构和数据来源，但不为营销页增加大型工具链。

## 2. 第二轮目标与设计方向

**定位：给 AI 编程工具使用者的本地 LLM 网关。** 首屏回答：这是做什么的、解决什么问题、在哪里运行、如何开始。

- 英文 H1 方向：`Your coding tools. One local gateway.`；中文方向：`AI 编程工具，一个本地网关。` 可按排版打磨，避免逐字硬译。
- 副文案用两句解释集中管理提供商/凭证、自动模式的密钥和提供商切换、浏览器内查看请求与费用。
- 主要转化：`Get started / 开始使用` 到安装；次要：`Explore the console / 看看控制台` 到真实 UI。首屏可紧凑提供 npm 复制，但避免多个同权 CTA。
- 品牌方向：**精密的本地路由工作台**。冷黑/石墨、浅白大标题、单一鲜明薄荷绿主信号，青/琥珀用于有意义的协议或故障状态；保留结构性线条与有节制的景深。摆脱大量 HUD 术语和无差别小框，避免霓虹光球、杂色渐变和通用 SaaS 卡片堆叠。
- 设计独特性由构图建立：大尺度标题、清晰留白、输入→本地 Clipal→上游的定制路由示意、接近实物的控制台截图、横向分隔和少量不对称功能展台。图示不能暗示跨协议自动翻译，协议兼容各自对应上游。
- 桌面 1440px 使用约 1180–1240px 内容宽度；H1 约 64–88px，正文 16–18px。中文控制行长，使用系统中文字体与合理行高。移动端必须先标题、价值和 CTA，再视觉。

## 3. 新页面顺序与内容规格

1. **导航**：品牌、功能/控制台/集成/安装、文档、语言切换、GitHub；移动端原生 button 菜单，展开状态与 Escape/点击链接关闭。
2. **Hero**：清晰 H1、两句收益、主次 CTA、简短开源/本地/跨平台说明。主视觉为自定义路由工作台或真实 UI 与路由的有机组合。示例状态明确标注 `Illustrative routing / 路由示意`，无伪造实时运行状态。
3. **产品证据**：高可读的真实截图 Tour，Providers / Takeover / Usage / Settings 四种视图，联动简短收益说明。不能仅以缩成不可读的小图作证据，可提供点击放大原生 dialog。维持自然比例，必要时提供完整查看。
4. **能力叙事**：最多 3–4 个有主次的图文模块，涵盖可预览可回滚配置、auto 多密钥/提供商切换、Claude/Codex/Antigravity OAuth、请求/费用来源可见。至少一个清晰的小型路由故障示意，标注示例，静态或用户触发即可。
5. **集成**：18 个真实支持客户端的紧凑 logo/name 网格，前置常用项，全部可发现。取消每个客户端都展示不可靠 Diff 的冗长卡墙，改为精选简短说明与完整 catalog 链接。若保留详情，用原生可聚焦按钮+dialog，信息以 catalog 为准，不发明配置。
6. **上手**：npm / macOS+Linux shell / Windows PowerShell / AI 助手 prompt。命令可选择可复制，之后明确 `clipal` 或 service install/start，最后打开 `http://127.0.0.1:3333/` 添加 provider 并启用 takeover。不同平台命令不得混成一串。AI prompt 可折叠但保留完整内容和复制。
7. **FAQ/信任**：4–5 个精炼问答，说明本地管理、上游请求、配置回滚边界、原生协议、用量费用。用原生 details。避免无依据的保证。移除原来的竞品性能表，改用自身工作流/事实说明。
8. **社区/收尾**：GitHub、文档、保留微信群/作者二维码（可收敛到折叠区）；最后再次明确安装入口。页脚简洁。

## 4. 产品事实包（执行时直接使用）

- README 定位：本地 LLM API proxy；macOS/Linux/Windows 单二进制；UI 默认 `http://127.0.0.1:3333/`，管理 UI 仅允许 loopback。代理可另行配置监听边界。MIT。
- `/clipal/*` 统一入口，保留 Anthropic/OpenAI/Gemini 原生请求族并按路径选择；**不能写成任意模型/协议自动互转**。
- Auto 模式先用同一 provider 的下一个可用 key，再切其他 provider；manual pinned 模式不自动切 provider 或 key。没有可支持的固定故障切换耗时、端到端延迟、内存或可用率承诺。
- CLI takeover 18 个：Claude Code、Codex CLI、OpenCode、Antigravity CLI、Continue、Aider、Goose、Pi、Qwen Code、Kimi Code CLI、Copilot CLI、Kilo CLI、CodeBuddy Code、Crush、iFlow CLI、TRAE Agent、Grok Build、DeepSeek Harness。
- takeover 流程：preview、首次 apply 记录 managed files 原内容/是否存在/权限、reapply 保留 cycle baseline、rollback 恢复 baseline 并结束周期。不是跨文件断电原子事务。配置已写入 ≠ 实际 CLI 流量验证成功，project/session/managed/env 可覆盖；shell 集成应用与回滚后需新终端。
- 不要一概宣传 AST；采用“保留无关设置的定向配置变更，先预览，保留备份，可回滚”。不要虚构通用 JSON/TOML/YAML 无损解析。
- API-key providers 在 `~/.clipal` 的 YAML；OAuth 凭证独立位于 `~/.clipal/oauth/`。不承诺 AES、全部加密或任何 config.json 安全存储。API 请求仍发送到用户选择的上游，必要认证信息提供给上游；“本地网关”不等于离线推理。
- OAuth 支持 Claude、Codex、Antigravity-backed Google accounts，具备 refresh token 时自动刷新；不承诺任何订阅普遍可用，不宣传 region spoofing 或 biometrics。
- 费用优先 upstream-reported cost，否则 embedded model-specs exact-model catalog；未知价格缺失，不猜价；更新目录不重算历史。可以说“请求、Token 与费用来源可见”，不能把估算当真实账单。
- 安装命令：`npm install -g clipal`；`curl -fsSL https://clipal.paiart.com/install.sh | sh`；Windows `powershell -ExecutionPolicy ByPass -c "irm https://clipal.paiart.com/install.ps1 | iex"`。
- 启动 `clipal`，或 `clipal service install` 再 `clipal service start`。npm 使用平台 optional dependencies，不需要 npm12 allow-scripts；无需首页详细解释旧包历史。
- 正式链接根：`https://github.com/PAIArtCom/Clipal`；文档 `blob/main/docs/en/`、`blob/main/docs/zh/`；catalog `blob/main/docs/cli-takeover.md`；下载 `releases/latest`；网站 `https://clipal.paiart.com/` / `/zh/`。

## 5. 文件与资源地图（避免重复扫描）

直接重写：
- `landing-page/index.html` 英文，canonical `/`、hreflang 保留；metadata/OG/JSON-LD 必须同步真实正文，不带旧夸张承诺。
- `landing-page/zh/index.html` 中文，资源现有相对路径带 `../`，注意语言跳转；保持结构、能力与交互一致。
- `landing-page/css/style.css` 共用 token、布局、断点、focus-visible、reduced motion。
- `landing-page/js/main.js` 共用 progressive enhancement：menu、tabs、copy、dialog；取消随机费用和无限假日志。尽量不再维护整块双语业务文案，可放 HTML 各自 panel。
- `docs/en/design-system.md`、`docs/zh/design-system.md` 更新到实际新版；`assets/clipal-icon-philosophy.md` 去除无依据性能语义并匹配新版品牌方向。

资源可直接复用：
- `landing-page/assets/icons/clipal-icon.svg` + favicon，保留既有标志几何。
- `assets/screenshots/providers.png` 2048×1268、`takeover.png` 2018×1430、`settings.png` 2010×1690、`status.png` 2018×1436（这里的 assets 路径均相对 landing-page/）。另有 clipal.png 和现成 OG JPEG。
- `assets/logos/` 有 18 客户端 logo（claude-code、codex-cli、opencode、google 对应 Antigravity、continue、aider、goose、pi、qwen、kimi、copilot、kilo、codebuddy、crush、iflow、trae、grok、deepseek），以及上游 openai/anthropic/google 等。注意暗色 SVG 对比，不粗暴反相所有彩色 logo。
- `assets/community/wechat-group.jpeg`、`wechat-supporter.jpeg` 保留原图比例，不裁掉二维码。
- `_redirects` / `_headers` 保留；GitHub Actions `.github/workflows/deploy-landing-page.yml` 直接 `wrangler pages deploy landing-page`，故不要引入要求 build 才能部署的框架/包依赖。
- 本地预览已启动：`http://localhost:8765/`，Python server session 84568，根为 landing-page。

只需有疑问时定点查证：`README.md`、`docs/cli-takeover.md`（前 55 行支持边界）、`docs/en/web-ui.md`（约 65–100 行 takeover）、`docs/en/pricing.md`、`docs/en/routing-and-failover.md`、`docs/en/config-reference.md`。不要再次 log 全仓库、遍历 Go 后端或读取全部 release notes。

## 6. 执行顺序与验收

1. 先做完整新视觉和内容结构，直接实现两种语言；允许整体重写静态页面，勿层叠大量旧 CSS 补丁。
2. 实现必要交互，所有外链真实、无占位 href；无 JS 时主要内容/安装/FAQ 仍可读。原生 tab 需 selected、controls、tabpanel、Arrow/Home/End；或选择更简单且语义正确的控件。
3. 文档与 metadata 同步，所有旧安全/性能夸张承诺从中英文正文、JS、JSON-LD 一起去除。
4. 校验 HTML 资源/锚点、语言对等、JS 语法；用浏览器检查两语言在 1440、1024、768、390、320 宽度，无横向溢出，无首屏顺序倒置，无截图拉伸/截字。keyboard 验证 nav/tab/dialog（Escape、关闭后归还焦点）、copy 成功/失败提示，reduced motion，无 console error。
5. 交付实际浏览器预览截图与简短验证记录。不要部署、push 或修改 Go 产品代码；不要执行页面展示的安装命令。

优先级：P0 首屏定位/视觉重构/真实截图/去除失实承诺/移动端/安装；P1 18 集成、完整键盘和反馈、SEO、双语；P2 轻量动效与补充社区细节。P0/P1 均完成后再交付。

不需要为了小型静态站添加测试框架、npm dependency 或新生成资产。验证工具、临时截图和临时脚本放 /tmp，必要时让主代理进行独立浏览器验收。

## 7. 实施与独立验收记录

本轮由 GPT-6 Astra / high 执行代理完成两语言页、共享 CSS/JS、双语设计规范和品牌说明；主代理提供上述上下文后执行独立验收。未添加部署依赖，未提交、推送或部署。

- EN/ZH 在实际浏览器 1440、1024、768、390、320 px 下逐一核验，整页没有横向溢出；手机标题与 CTA 位于路由示意之前。
- 验证移动导航展开与 Escape 关闭、标签页 ArrowRight/Home/End、安装渠道切换、示意流程从主密钥到备用密钥再到备用提供商、dialog 关闭及触发按钮焦点恢复；未发现浏览器 console error。
- 复制操作出现成功反馈；代码具有失败提示和选中文本回退。浏览器工具未回读到剪贴板实际文本，故不将系统剪贴板内容、拒绝权限场景列为已实测通过项。
- 手机截图弹窗新增随当前截图更新的原图链接，确认设置视图链接指向正确的 settings.png。修复 Goose 单色图标在深色背景下的对比度。
- 独立静态验证两语言 IDs、锚点、tab/copy 引用、本地资源、JSON-LD、JS 语法与 diff 格式。无 JS 可读性和 reduced-motion 通过源码检查，未模拟系统设置或禁用浏览器 JS。
- 保留本地预览：`http://localhost:8765/zh/` 与 `/`。现有产品截图仍为仓库提供的示例界面，不代表最新版本实时状态。

## 8. Hero 动态演示追加（2026-11-16）

按用户反馈将 Hero 升级为动态路由演示。沿 SVG 路径移动的信号、选中客户端／原生协议、429 密钥状态及备用密钥 200 响应联动。提供暂停、播放、下一步与客户端选择；离屏／页面后台停播，reduced-motion 默认暂停并禁用位移动画。静态标注为演示，不连接实际网关或产生上游请求。

浏览器实测双语控制、暂停、Codex→OpenAI 与 OpenCode→Gemini 示例、429→RETRY→200 状态、离屏停播及 1440/768/390/320px 布局通过；没有 console error。减少动态效果和后台标签事件通过源码核验，未修改系统偏好。

## 9. 英文评审批注落实（2026-11-17）

- 保留宽屏构图与正文间距。640px 以下 Hero 操作区不换行，通过按钮内边距／字号让两个入口同排。
- 英文 h1/h2 使用同一 EB Garamond 字族的正体与斜体；次级章节字号适当降低，安装区继续突出。自托管 Latin variable WOFF2 共约 90KB，保留 OFL 许可。
- 手机客户端目录改为 64px 高紧凑双列，18 项全部保留；跑马灯保持展示用途，不新增强调动效控制的界面。
- Hero 从仅 Key01/02 改为当前渠道的 API Provider01/OAuth Provider02，联动 Claude/Codex/Antigravity 账号标签。模拟 API 无剩余可用密钥后切换兼容 OAuth 提供商；旧中文演示暂不改。
- 实测 320px 可用安装列265px，原隐式 auto track321px 导致溢出。指定 minmax(0,1fr) 和子项可收缩；长命令由局部代码框横向滚动，保留全文和复制内容。
- 生成英文1200×630 OG卡片、180px Apple Touch图标、192/512px应用图标，补齐manifest。
- 将 closing 正文规则限定为 closing-inner p，避免压过 footer-mega 字号，不用 !important。
- 中文 HTML 保持不变，待英文确认再对齐。

最终浏览器复核：英文1440/1024/768/390/360/320px的document scrollWidth均等于clientWidth，无整页横向溢出。另发现手机堆叠后的cell-wide仍保留12列间隙，导致约1px溢出；现将堆叠区域切为单列，避免零宽轨道被间隙撑开。320px下Hero两操作中心Y一致。OpenAI API Provider01呈429、OpenAI OAuth Provider02关联Codex账号并呈200 OK；Google关联Antigravity账号。长PowerShell命令在184px代码框内滚动，展开后台服务和AI提示词仍未撑大页面。英文OG/应用图标/manifest引用、JSON-LD、JS语法与diff格式检查通过，无浏览器console error。

## 10. 多特性演示（2026-11-17）

根据最新反馈，不再让每个请求都遇到429。英文Hero按同一渠道依次播放：正常路由3步直接成功、提供商兜底5步（仅此段显示429）、黏性路由5步（两次匹配请求复用已成功OAuth提供商）。完成三段后切换客户端，进度标记按场景步数同步。黏性文案仅指匹配学到的路由，不承诺所有请求永久固定。中文保持待对齐。

浏览器完整核验14个连续状态（包含循环后的下一渠道）：正常段无错误、兜底段API429→OAuth200、黏性段STICKY→200→STICKY→200，渠道在三个故事期间保持一致，循环时切换。320px静态宽度复核无横向溢出；JS语法及diff格式检查通过。

## 11. 中文版本对齐（2026-11-17）

以当前英文结构对齐全部中文页面，中文标题和正文独立编写，避免逐句硬译。共用组件、18种客户端、安装命令与13帧演示状态一致；动态标签通过HTML属性本地化。中文使用宋体标题及系统中文正文，移动端按钮同排、64px客户端行和完整页尾字标保持一致。补齐中文OG图、canonical/hreflang、FAQ结构化数据及中文启动manifest。

静态验证两语言IDs、锚点、tab/copy引用、安装/启动命令、13帧状态完全对应，本地及分享资源、JSON-LD、manifest均有效。浏览器核验中文六档宽度无溢出、按钮同排；三段演示及429后持续冷却正确，手机导航Escape、安装渠道与中文AI提示词、截图弹窗与焦点恢复通过，无console error。
