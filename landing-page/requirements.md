# Clipal Landing Page Requirements

> **Standard**: "Hancock" — Premium, Zero-Compromise, Developer-First
> **Goal**: Convert a curious developer into a Clipal user within 60 seconds of landing.

---

**Hosting & Community Context**: This page will be deployed at `https://clipal.paiart.com`. PAIART is an organization dedicated to building community ecosystems for Personal AI. The site should subtly reflect this connection in its branding and footer.

## 1. Strategic Foundation

### 1.1 Target Audience

| Segment | Profile |
|---|---|
| **Primary** | Chinese developer, heavy Claude Code / Aider / Continue user, frustrated by rate limits and key management |
| **Secondary** | International developer using multiple LLM providers, wants a clean local proxy without Docker/Python overhead |

### 1.2 Core Value Proposition (The Razor)

**Headline**: `"One Binary. All Your AI Tools. Zero Configuration Hell."`

**Sub-headline**: `"The local LLM gateway built for developers who ship. Bulletproof failover, one-click CLI takeover, zero server overhead."`

### 1.3 Competitive Differentiation (Anti-Positioning)

The landing page must implicitly communicate why Clipal wins without naming competitors directly. Use the comparison table in the feature section.

| Pain Point | Other Solutions | Clipal |
|---|---|---|
| Requires Python / Docker | LiteLLM, One-API | ✅ Single binary, runs anywhere |
| No CLI tool integration | Most proxies | ✅ One-click takeover for 7+ tools |
| Manual key rotation | Most proxies | ✅ Auto-rotate, auto-failover |
| Cloud-based, sends keys remotely | Many SaaS tools | ✅ 100% local, keys never leave your machine |

---

## 2. Visual Identity & Design System

### 2.0 Design Philosophy (The "Industrial Precision" Aesthetic)
1. **Absolute Control**: Utilize Black, Silver, and Gold to mirror professional, high-end instruments (e.g., Apple Pro devices, precision tools). The UI should elicit feelings of speed and mastery, leveraging gold to highlight critical actions like failover triggers.
2. **Data Sovereignty (PAIART alignment)**: Reinforce "Your Machine, Your AI Fortress." Represent local control visually through secure motifs (e.g., vault or lock aesthetics), echoing PAIART's mission of empowering personal AI communities without compromising data ownership.
3. **Silky Micro-interactions**: Establish a premium "Hancock Standard." E.g., hovering over tool logos triggers a metallic glow, terminal animations are impeccably crisp, and copy-interactions offer distinct, satisfying visual feedback.

### 2.1 Color Palette (Dark Theme — Default & Only)

```css
--bg-base:        #0a0a0b;          /* Deep obsidian */
--bg-surface:     rgba(255,255,255,0.03); /* Glass card */
--bg-surface-hover: rgba(255,255,255,0.06);
--accent-primary: #e5e7eb;          /* Silver / Platinum */
--accent-secondary: #d4af37;        /* Gold */
--accent-glow:    rgba(229,231,235,0.25);
--border:         rgba(255,255,255,0.08);
--border-hover:   rgba(212,175,55,0.4);
--text-primary:   #f8fafc;
--text-secondary: #94a3b8;
--text-muted:     #475569;
--success:        #22c55e;
--error:          #ef4444;
```

### 2.2 Typography

| Role | Font | Weight |
|---|---|---|
| Display / H1 | `Outfit` | 700–800 |
| Headings H2–H3 | `Inter` | 600–700 |
| Body | `Inter` | 400–500 |
| Mono / Code / Terminal | `JetBrains Mono` | 400 |

- Load from Google Fonts with `display=swap` and `preload` link tags.
- Base size: `16px`. Scale: `1.25` modular.

### 2.3 Motion Design Rules

- **Entry**: Elements fade-in + translate-Y (20px → 0) via `IntersectionObserver`. Duration: `0.5s ease-out`.
- **Hover**: Cards scale `1.02`, border color transitions to `--border-hover`. Duration: `0.2s`.
- **Glow**: Accent glow on CTA buttons uses `box-shadow` pulse, not `filter:blur` (performance).
- **Terminal**: Character-by-character typewriter at ~40ms/char with blinking cursor.
- **No layout shifts**: All animated elements have reserved dimensions before animation fires.

---

## 3. Page Architecture

### Section 1 — Hero (The Hook)

**Goal**: Communicate the core value and get the user to download or scroll within 5 seconds.

**Layout**: 50/50 Split — Left: Copy + CTAs | Right: Animated Terminal

#### Left Column — Copy
```
[Eyebrow label]: "Open Source · Local-First · v0.x.x"

[H1]: One Binary.
      All Your AI Tools.
      Zero Configuration Hell.

[Sub-headline]: Take control of your AI workflow. One local proxy,
                multiple providers, bulletproof failover. No Docker, no Python.

[CTAs]:
  Primary   → [↓ Download for macOS]   ← OS-detected, dynamic
  Secondary → [★ Star on GitHub]
  Tertiary  → [▶ View Docs]
```

**OS Detection Logic** (JS, ~10 lines):
- Detect `navigator.platform` / `navigator.userAgent`
- Dynamically set CTA href to the correct GitHub Releases asset URL
- Show platform badge: `Apple Silicon` / `Intel Mac` / `Linux x86_64` / `Linux ARM64` / `Windows`

#### Right Column — Simulated Terminal

The terminal must show a realistic, compelling scenario. Use this exact script:

```
$ clipal service start

  ██████╗██╗     ██╗██████╗  █████╗ ██╗
 ██╔════╝██║     ██║██╔══██╗██╔══██╗██║
 ██║     ██║     ██║██████╔╝███████║██║
 ██║     ██║     ██║██╔═══╝ ██╔══██║██║
 ╚██████╗███████╗██║██║     ██║  ██║███████╗
  ╚═════╝╚══════╝╚═╝╚═╝     ╚═╝  ╚═╝╚══════╝

✓ Clipal v0.18.1 running on http://127.0.0.1:3333
✓ Providers loaded: Anthropic, OpenAI, Gemini (3)
✓ Key pool: 5 keys active across 2 providers

→ [claude code]  POST /clipal  →  claude-4-6-sonnet  ✓  288ms
→ [codex]        POST /clipal  →  gpt-5.4       ✓  341ms
→ [gemini]  POST /clipal  →  gemini-3.1-flash  ✗
                                →  OpenAI  [FAILOVER]  ✓  519ms
```

**Terminal Styling**: Phosphor-green `#4ade80` for `✓`, red `#f87171` for `✗`, gold `#d4af37` for `→` routing arrows, dimmed gray for timestamps. Dark glass panel with subtle outer glow.

---

### Section 2 — Ecosystem Grid (Social Proof)

**Goal**: Instantly signal broad compatibility. "Oh, it works with my tools."

**Layout**: Two rows separated by a divider.

**Row 1 — AI Clients (Tools that USE Clipal)**:
Claude Code · Aider · Continue · Goose · Codex CLI · OpenCode · Gemini CLI · Cherry Studio

**Row 2 — LLM Providers (Backends Clipal routes TO)**:
Anthropic · OpenAI · Google Gemini · DeepSeek · Groq · Azure OpenAI

**Effect**:
- All logos: grayscale `filter: grayscale(1) opacity(0.5)` by default.
- On hover (individual card): Full color + opacity 1, slight `translateY(-2px)`.
- Continuous subtle marquee scroll animation on mobile (no hover states on touch).
- Logo assets: `.svg` format preferred. Use official brand logos.

---

### Section 3 — Feature Pillars (4-Column Grid)

**Goal**: Convince the technical user. Show depth, not just buzzwords.

Each pillar = Card with: Icon + Title + Description + Visual Demo

#### Pillar A: Bulletproof Failover

- **Icon**: Lucide `shield-check`
- **Title**: `Failover & Multi-Key Rotation`
- **Description**: Configure multiple API keys per provider. Clipal rotates automatically, detects quota exhaustion, and falls back to secondary providers—transparently, mid-request.
- **Visual**: SVG animated flow diagram:
  ```
  Request → Key 1 [QUOTA ✗] → Key 2 [OK ✓]
           ↘ Provider A [FAIL ✗] → Provider B [OK ✓]
  ```

#### Pillar B: One-Click CLI Takeover

- **Icon**: Lucide `terminal`
- **Title**: `One-Click CLI Takeover`
- **Description**: Stop hunting for hidden config files. One click in the Web UI, and Clipal automatically rewires Claude Code, Aider, Continue, Goose, and 3 more tools. Original configs are backed up. Safe rollback anytime.
- **Visual**: Animated code diff (before/after):
  ```diff
  - "api_key": "sk-ant-..."
  - "base_url": "https://api.anthropic.com"
  + "api_key": "clipal"
  + "base_url": "http://127.0.0.1:3333/clipal"
  ```

#### Pillar C: Real-time Web UI

- **Icon**: Lucide `layout-dashboard`
- **Title**: `Beautiful Local Dashboard`
- **Description**: Add providers, drag-and-drop reorder priority, toggle keys on/off, and watch live request logs—all from a local web dashboard. Hot-reloaded, no restarts.
- **Visual**: High-res screenshot of the actual Web UI with a subtle border glow. *(Use `assets/webUI.png` — ensure high quality)*

#### Pillar D: Local & Private by Design

- **Icon**: Lucide `lock`
- **Title**: `Your Keys. Your Machine. Always.`
- **Description**: Clipal binds to `127.0.0.1` only. Your API keys are stored in local YAML files and transparently injected. No telemetry, no cloud sync, no third-party key exposure.
- **Visual**: Network diagram showing `localhost only` boundary with keys inside the machine.

---

### Section 4 — "How It Works" (3-Step Stepper)

**Goal**: Remove the fear of complexity. Show that setup is genuinely < 5 minutes.

**Layout**: Vertical stepper with step numbers, terminal/browser mockup inline.

**Step 1 — Install** *(terminal mockup)*
```bash
npm install -g clipal
clipal --version
# → clipal version 0.18.1
```
Caption: *Recommended npm install. Standalone binaries remain available for macOS, Linux, and Windows.*

**Step 2 — Start the Service** *(terminal mockup)*
```bash
clipal service install
clipal service start
# ✓ Clipal running on http://127.0.0.1:3333
```
Caption: *Runs silently in the background. Survives reboots.*

**Step 3 — CLI Takeover** *(browser mockup / GIF)*
- Show the Web UI "Integrations" panel.
- User clicks "Use Clipal" next to "Claude Code".
- Status badge flips from gray to green: `● Active`.

Caption: *One click. All your AI tools now route through Clipal.*

---

### Section 5 — Download & Community

**Goal**: Final conversion. Reduce friction to zero.

**Layout**: Two-column — Left: Download panel | Right: Community (WeChat)

#### Left: Download Panel

- **Recommended npm install** (with Clipboard copy button):
  ```bash
  npm install -g clipal
  ```
- **Standalone binary fallback** → GitHub Releases
- **Version badge**: `Latest: v0.18.1` (auto-fetch from GitHub API or hardcoded with update workflow)

#### Right: Community Panel

- WeChat group QR code (`assets/wechat-group.png`) — styled with glow border.
- Text: "Join 800+ developers using Clipal. Get setup help, share configs, discuss new providers."
- GitHub Star button (iframe or badge).
- linux.do community mention (text only, subtle).

---

### Section 6 — Footer

- Logo + tagline: `"Clipal — Your Local LLM Gateway"`
- Ecosystem Note: `"Proudly part of the PAIART ecosystem — Empowering Personal AI Communities."`
- Links: GitHub · Documentation · Getting Started · Config Reference · License (MIT)
- Security note: `"100% local. Your keys never leave your machine."`
- Copyright: `© 2025 Clipal Contributors. MIT License.`
- Language toggle: `English | 中文`

---

## 4. Internationalization (i18n)

- **Default language**: English
- **Secondary language**: Simplified Chinese (`zh-CN`)
- **Implementation**: All user-facing strings stored in a `translations.js` object. Toggle button in header switches language without page reload.
- **Priority sections to translate**: Hero, Download, Community, Footer.
- **Chinese-specific additions**:
  - WeChat QR code featured more prominently in ZH view.
  - Add a note: `"支持 linux.do 社区"`

---

## 5. Technical Requirements

### 5.1 Stack Constraints

- **HTML**: Semantic HTML5. Single `index.html`.
- **CSS**: Vanilla CSS with custom properties. No Tailwind, no Bootstrap.
- **JS**: Vanilla ES6+. No React, Vue, or Angular. Total JS budget: **< 30KB uncompressed**.
- **Dependencies allowed** (CDN or self-hosted):
  - Lucide Icons (SVG sprites, self-hosted)
  - Google Fonts (preloaded, subset)
  - No other third-party JS libraries

### 5.2 Performance Targets

| Metric | Target |
|---|---|
| Lighthouse Performance | 100 |
| Lighthouse Accessibility | ≥ 95 |
| Lighthouse SEO | 100 |
| LCP | < 1.2s |
| CLS | 0 |
| TBT | < 50ms |
| Total page weight | < 500KB (gzipped) |

### 5.3 Asset Formats

| Asset Type | Format | Notes |
|---|---|---|
| Photos / UI screenshots | `.webp` | Quality 85, max 1920px wide |
| Icons | `.svg` | Inline or sprite sheet |
| OG Image | `.webp` / `.jpg` | 1200×630px |
| Favicon | `.svg` + `.ico` | Both formats required |
| Fonts | `.woff2` | Self-host subsets |

### 5.4 SEO & Meta

```html
<title>Clipal — Local LLM Gateway for Developers | One Binary, All AI Tools</title>
<meta name="description" content="Clipal is a local LLM proxy with bulletproof failover, one-click CLI takeover for Claude Code, Aider, and Continue. Single binary, no Docker, no Python.">
<meta name="keywords" content="LLM proxy, claude code api, api key manager, llm failover, aider proxy, local AI gateway, clipal">

<!-- OpenGraph -->
<meta property="og:title" content="Clipal — Local LLM Gateway">
<meta property="og:description" content="One binary. All your AI tools. Zero configuration hell.">
<meta property="og:image" content="/assets/og-image.webp">  <!-- 1200×630 -->
<meta property="og:type" content="website">

<!-- Twitter Card -->
<meta name="twitter:card" content="summary_large_image">

<!-- Structured Data -->
<script type="application/ld+json">
{
  "@context": "https://schema.org",
  "@type": "SoftwareApplication",
  "name": "Clipal",
  "applicationCategory": "DeveloperApplication",
  "operatingSystem": "macOS, Linux, Windows",
  "offers": { "@type": "Offer", "price": "0", "priceCurrency": "USD" },
  "license": "https://opensource.org/licenses/MIT"
}
</script>
```

**Target Keywords**:
- Primary: `clipal`, `local LLM proxy`, `claude code api key manager`
- Secondary: `llm failover`, `aider proxy`, `api key rotation`, `local AI gateway`

### 5.5 Cloudflare Pages Configuration

**`_headers` file**:
```
/assets/*
  Cache-Control: public, max-age=31536000, immutable

/fonts/*
  Cache-Control: public, max-age=31536000, immutable

/*.html
  Cache-Control: public, max-age=0, must-revalidate

/js/*
  Cache-Control: public, max-age=31536000, immutable

/css/*
  Cache-Control: public, max-age=31536000, immutable
```

**`_redirects` file**:
```
/zh    /index.html    200
/docs  https://github.com/PAIArtCom/Clipal/tree/main/docs    301
```

---

## 6. Directory & Asset Structure

The project strictly follows this directory layout, utilizing the pre-provided assets in the `assets/` folder. Implementations must link directly to these specific files.

```text
landing-page/
├── index.html
├── css/
│   └── style.css
├── js/
│   └── main.js
├── fonts/
└── assets/
    ├── community/
    │   └── wechat-group.png
    ├── icons/
    │   └── clipal-icon.svg
    │   └── favicon.ico
    ├── logos/
    │   ├── aider.svg
    │   ├── anthropic.svg
    │   ├── azure.svg
    │   ├── cherry-studio.svg
    │   ├── claude-code.svg
    │   ├── codex-cli.svg
    │   ├── continue.svg
    │   ├── deepseek.svg
    │   ├── gemini-cli.svg
    │   ├── google.svg
    │   ├── goose.svg
    │   ├── groq.svg
    │   ├── openai.svg
    │   └── opencode.svg
    └── screenshots/
        ├── clipal-og-image.jpeg
        ├── webui-cli-takeover.png
        └── webui-providers.png
```

---

## 7. Development Workflow

### Phase 1: Foundation (Day 1)
1. Create `index.html` with full semantic structure (all sections, no styling).
2. Create `css/style.css` with design tokens, typography, layout grid.
3. Create `js/main.js` scaffold with module pattern.

### Phase 2: Core Sections (Day 2–3)
4. Build Hero section with terminal animation.
5. Build Ecosystem Grid with hover effects.
6. Build Feature Pillars with SVG visuals.
7. Build How It Works stepper.

### Phase 3: Conversion & Polish (Day 4)
8. Build Download section with OS detection.
9. Build Community section with QR code.
10. Implement i18n toggle (EN / ZH).
11. Implement scroll-triggered animations.

### Phase 4: Audit & Deploy (Day 5)
12. Run Lighthouse audit. Fix any score below target.
13. Validate all meta tags with [opengraph.xyz](https://www.opengraph.xyz).
14. Test on: Chrome, Firefox, Safari, Mobile Safari, Chrome Android.
15. Connect repository to Cloudflare Pages (branch: `main`, build: none / direct upload).
16. Set custom domain if available.
