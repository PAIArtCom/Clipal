# Clipal 设计系统规范 (Design System Spec)
## 动力学结构主义与量子控制中枢 (Kinetic Structuralism & Quantum Control Plane)

---

## 1. 核心设计哲学

Clipal 是面向开发者的高吞吐本地大模型网关与 AI 助手控制台。其视觉语言确立为 **【动力学结构主义与量子控制中枢】**：
- **前沿科幻（Sci-Fi Avant-Garde）**：具备深空工程、航天遥测与高维网络控制台的精密秩序感。
- **优雅与克制（Elegance & Restraint）**：严格禁止廉价的弥散荧光大光斑（Glow Soup）与模糊渐变球。一切光感来自于物理界面的折射与高锐度指示线。
- **动效立体（Spatial 3D & Kinetic Physics）**：界面拥有真正的三维深度（CSS 3D / Perspective）、物理惯性视差倾角（Inertial Tilt）与机械阻尼微动效。
- **创意大胆表现力强（Bold Structural Expression）**：高反差钛金属切角、HUD 状态十字卡扣、不可变快照 Diff 视窗与毫秒级脉冲流。

---

## 2. 颜色与材质系统 (Tokens & Materials)

### 2.1 表面与暗色深度 (Surfaces)
| Token 变量 | 物理取值 | 用途与层级说明 |
| :--- | :--- | :--- |
| `--bg-deep` | `#050507` | 最深处虚空底层，承载全局 32px 矢量参考网格 |
| `--bg-base` | `#07070a` | 主页面视口背景，深邃、无杂质冷碳黑 |
| `--bg-surface` | `#0c0d12` | 基础卡片面板表面，偏振冷钛质感 |
| `--bg-surface-elevated` | `#12131a` | 抬升层面板表面（模态框、悬浮导航、悬浮展台） |
| `--bg-terminal` | `#09090d` | 代码视窗、Diff 检查器与终端输出底色 |

### 2.2 结构性线框与边界 (Structural Dividers)
- 摒弃大面积脏灰阴影，以极细的高对比度结构线建立骨架：
  - `var(--border-subtle)`: `rgba(255, 255, 255, 0.08)` —— 弱分割线、次级数据列分隔
  - `var(--border-medium)`: `rgba(255, 255, 255, 0.15)` —— 标准卡片外框、按钮边界
  - `var(--border-bright)`: `rgba(255, 255, 255, 0.35)` —— 激活态边框、HUD 卡扣高光
  - `var(--border-active)`: `#00ff9d` —— 终端当前活动行、焦点锚定指示

### 2.3 量子信号色 (Quantum Signals - 拒绝光晕，锐利单色)
| 信号色代号 | 色值 | 含义与场景 |
| :--- | :--- | :--- |
| **折跃绿 (Tritium Green)** | `#00FF9D` | 节点存活、AST 注入就绪、会话锁定成功、健康 |
| **极光青 (Cobalt Cyan)** | `#00E5FF` | 协议归一化分流、Diff 高亮、遥测元数据、路径展示 |
| **聚变金 (Reactor Amber)** | `#FFB800` | 429 容灾切换、冷却倒计时探测中、长文本溢价提示 |
| **脉冲红 (Plasma Red)** | `#FF3366` | 上游熔断、单 Key 耗尽、配置冲突拦截 |

---

## 3. 三维空间深度与动力学交互 (3D Spatial & Motion)

### 3.1 空间透视与惯性微倾角 (Inertial Parallax Tilt)
- 核心展示台采用真 3D 视角：
  ```css
  .viewport-3d {
    perspective: 1200px ~ 1400px;
    transform-style: preserve-3d;
  }
  ```
- 容器监听光标位移，计算旋转矩阵：
  $$\text{rotateX} = \left(\frac{y - \text{centerY}}{\text{centerY}}\right) \times -12^\circ$$
  $$\text{rotateY} = \left(\frac{x - \text{centerX}}{\text{centerX}}\right) \times 12^\circ$$
  配合 `requestAnimationFrame` 驱动流畅物理跟随，光标离开时平滑复位。

### 3.2 Z 轴空间分层 (Z-Axis Layering)
- 数据副面板（如 AST 守护状态卡、429 容灾指示卡）不平铺于父级，而是通过 `translateZ(40px)` 或 `translateZ(60px)` 悬浮于空中，产生真实视差。

### 3.3 机械阻尼缓动 (Kinetic Spring Damping)
- 弹簧阻尼：`--ease-kinetic: cubic-bezier(0.16, 1, 0.3, 1)`
- 机械开合：`--ease-mechanical: cubic-bezier(0.2, 0.8, 0.2, 1)`
- 切换 Tab、唤起 Modal 时，采用微观收缩—滑动—咬合（Slide & Latch）机制，杜绝软绵绵的无力淡入。

---

## 4. 排版与信息密度 (Typography & Density)

- **显示标题**：`Outfit` / `Inter`，字重 700~800，字距紧凑（`-0.02em` ~ `-0.03em`），彰显科技工程的自信与力量。
- **正文与标签**：`Inter`，行高 1.6，字级清晰分明。
- **代码与数据流**：`JetBrains Mono` / `SF Mono`，数字统一启用等宽：
  ```css
  font-variant-numeric: tabular-nums;
  ```
  保证微美元实时计价器（`$0.042189`）在跳动递增时界面不产生横向抖动。

---

## 5. 核心组件规范 (Component Specs)

1. **HUD 倒角与准线卡扣 (`.hud-bracket`)**：
   - 卡片左上与右下角设置 6px 纯直角 HUD 准星标，呈现类似飞船驾驶舱 HUD 仪表的工程质感。
2. **命令复制条 (`.btn-cmd`)**：
   - 带有激光绿高亮代码文本与独立的 `复制` 信号徽章，点击后触发锁死微动效并反馈 `✓ 已复制到剪贴板`。
3. **AST Diff 视窗 (`.diff-viewer`)**：
   - 包含行号列、保留行（dim/gray）与注入行（`#00ff9d` 带微弱绿色背景块）。
4. **全景实景展台 (`.tour-viewport`)**：
   - 顶部具备红黄绿冷光微型信号灯与当前路径指示，底部配备图文联动说明与功能徽标。

---

## 6. 严苛禁忌 (Anti-Patterns)
- ❌ **严禁光晕泛滥**：不得使用 `filter: blur(80px)`、多色弥散大荧光球或大面积渐变色作为卡片背景。
- ❌ **严禁脏灰与模糊边框**：不得使用不带 alpha 的纯灰实线；边框必须使用半透明高反差白或精准单色。
- ❌ **严禁无意义的慢动效**：动效时间不得超过 0.3s；所有交互必须干脆利落、符合物理机械阻尼。
