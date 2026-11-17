# Clipal landing-page design system

## Direction: Signal Brutalism

The site pairs a near-black canvas (`#0a0a0a`) with warm paper bands (`#f1f0ea`) and a single acid-green signal (`#c8ff3d`). Oversized typography, visible 12-column grid lines, numbered chapters, and sharp dividers establish its visual identity. Desktop layouts preserve generous breathing room between display headings, the copy/demo bench, and the client ticker; mobile adaptations reshape component geometry rather than compressing prose rhythm.

Primary conversion: install Clipal (`Get started` in hero and sticky header). Secondary conversion: explore the real browser console.

## Typography and color

- English display headings and all italic chapter/step numerals use self-hosted `EB Garamond` variable roman and italic (`font-weight: 500`).
- Body and UI copy use self-hosted `Geist`; commands, route labels, and metadata use `Geist Mono`. Font licenses reside in `landing-page/fonts/`.
- Chinese headings place `EB Garamond` first for Latin characters (`AI`, `CLI`, `18`, `OAuth`) and adapt CJK display rendering per operating system (`data-os`): multi-weight `Songti SC` on Apple platforms, `Source Han Serif SC` / `Microsoft YaHei UI` Bold on Windows (avoiding bitmap `SimSun` faux-bold), and `Noto Serif CJK SC` on Linux.
- Palette tokens: `--ink` (`#0a0a0a`), `--paper` (`#f1f0ea`), `--acid` (`#c8ff3d`), `--acid-ink` (`#1f2b03`), `--muted` (`#a8a8a0`), `--faint` (`#8a8a82`, WCAG AA compliant on dark surfaces), and `--heat` (`#ff5a1f` for 429 rate-limit states).
- Plain HTML, CSS, and progressive-enhancement JavaScript with zero build step or external runtime font requests.

## Hero composition and routing demonstration

- `.hero-meta` sits at the top of `.hero-copy` above the lead paragraph so `h1.display` keeps its full typographic scale while maintaining balanced vertical rhythm down to the bottom client ticker.
- The route illustration is explicitly labeled as illustrative. Incoming tool paths (`Claude Code`, `Codex CLI`, `OpenCode`) pass through Clipal (`127.0.0.1:3333` / `/clipal/*`) to native `Anthropic`, `OpenAI`, and `Gemini` protocols, then branch via an SVG bus into two side-by-side upstream cards (`Provider 01` with `[API]` badge and `Provider 02` with `[OAuth]` badge; stacked below `520px`).
- Three interactive scenario pills (`01 Normal`, `02 Fallback`, `03 Sticky`) allow direct inspection or automatic playback through normal routing, 429 cooldown + OAuth fallback, and sticky route reuse before rotating to the next client.
- Playback pauses offscreen, in background tabs, and under `prefers-reduced-motion: reduce`.

## Console, features, integrations, and install

- Four console tabs align with the actual Web UI: `Providers` (including per-provider token usage and spend), `CLI takeover`, `System status` (readiness checklist, uptime, circuit breaker, and active channel routes), and `Settings` (loopback-only by default).
- Non-link items in `.protocol-stack` and `.client-grid` use subtle surface elevation rather than `--acid` primary button fills, preserving unambiguous click affordance and icon contrast across all viewports.
- All 18 CLI integrations use official vector brand marks in `landing-page/assets/logos/`; below `640px` they render as compact two-column rows (`>= 64px` height).
- The acid-green `#install` section pairs a sticky runtime spec summary in columns 1–3 with dark step cards in columns 4–12. Copy buttons maintain fixed icon width on phones (`⧉` → `✓`) without shifting code scroll areas.
- FAQ covers both practical workflows (combining OAuth accounts with API keys, clean rollback/uninstall) and explicit product boundaries (upstream forwarding, credential storage, native protocol routing, and pricing sources).

## SEO, structured data, and AI discovery

- Both `/` (EN) and `/zh/` (ZH) ship localized `SoftwareApplication` and `FAQPage` JSON-LD, bidirectional `hreflang`, 1200×630 Open Graph cards (`assets/og/clipal-og-en.png`, `assets/og/clipal-og-zh.png`), and Web App Manifests.
- Root discovery files include `robots.txt`, `sitemap.xml` (with `xhtml:link` alternates), and `llms.txt` summarizing capabilities, boundaries, 18 supported CLIs, and installation commands for coding agents and AI search engines.
