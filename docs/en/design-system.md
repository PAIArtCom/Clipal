# Clipal landing-page design system

## Current direction: Signal Brutalism

The English page uses a near-black canvas, warm paper sections and a single acid-green signal. Oversized headings, visible grids, numbered chapters and sharp dividers give the site its identity. Preserve the desktop composition and existing prose spacing; mobile adaptations should change component geometry rather than compress the reading rhythm.

Primary conversion: install Clipal. Secondary conversion: explore the real browser console. Explain the local gateway, compatible upstreams and how to start before introducing implementation details.

## Typography and color

- Main English headings use self-hosted EB Garamond variable roman and italic, with a shared weight of 500. `em` provides an italic variation within the same family rather than introducing a second typeface.
- Body and UI use Geist; commands, route labels and metadata use Geist Mono. Font licenses are retained in `landing-page/fonts/`.
- Hero display sizing and staggered desktop lines remain. Secondary section headings use `clamp(38px, 5.2vw, 84px)`; the install heading retains greater emphasis.
- Background `#0a0a0a`; paper `#f1f0ea`; acid `#c8ff3d`; secondary text `#a3a39b`; rate-limit signal `#ff5a1f`.
- The page remains plain HTML/CSS/JavaScript with no build requirement or externally hosted fonts at runtime.

## Hero demonstration

The route illustration is explicitly labeled as illustrative. It follows a coding client through Clipal, a native protocol, and compatible upstream providers. Claude, OpenAI and Google channels have distinct API and OAuth provider examples; the associated OAuth account labels are Claude, Codex and Antigravity.

The illustration rotates through three stories on the same channel: normal routing directly to a successful API response; fallback from an API provider with no remaining available keys to a compatible OAuth provider; and follow-up requests that match the learned route and reuse that successful OAuth provider. Only the fallback story shows a 429. Each story has its own caption, provider states, packet direction and progress count. After all three stories, the next client/channel begins. Sticky routing illustrates matching requests and avoids claiming that every request is permanently pinned.

Keep existing playback controls secondary. Do not add a prominent motion toolbar. Hero playback stops offscreen and in a background document; reduced-motion preferences start it paused. The illustration makes no network requests or live telemetry claims.

After the illustrative 429, the API provider enters `COOLDOWN` and retains that state throughout the remaining fallback and sticky stories. It does not immediately become `READY` when the backup succeeds. Only a new demonstration cycle resets the provider state; no fabricated cooldown countdown is shown.

## Mobile layouts

- Hero actions remain in one horizontal row. Smaller button padding and type preserve both the install action and console link without changing the paragraph spacing.
- Keep all 18 clients visible. Below 640px, use compact two-column rows of at least 64px instead of the desktop 150px tiles; omit decorative item numbers.
- The installation workflow explicitly uses a `minmax(0, 1fr)` grid track and shrinkable children. The earlier implicit auto track inherited a roughly 321px content minimum inside a 265px available column at a 320px viewport.
- Long commands scroll inside their code boxes. Never truncate the command text or solve content overflow by hiding it at the page edge.
- The large footer mark is styled independently of closing-copy paragraphs so its font size is not overridden by a more specific selector.
- Display the complete `Clipal.` wordmark on one line: use a 1.08 line height, positive vertical padding and no negative bottom margin. Scale it down with the viewport on phones; do not crop the letter outlines for a bleed effect.

## Product evidence and interaction

Real screenshots are labeled as example configurations. Four console views are available, with an enlargement dialog and a full-size image link. Dialog dismissal restores trigger focus. Tabs support arrow keys, Home and End. Copy feedback uses a live region and selects text as a fallback on failure. FAQ, service setup, AI setup prompt and community disclosures use native details/summary.

The client ticker is decorative evidence; the complete integration list remains available separately. Retain reduced-motion support without making motion controls a product focus.

## Assets

The English social card is `assets/og/clipal-og-en.png` at 1200×630. It follows the page typography and colors, uses the existing Clipal mark, and introduces no unverified performance claims. Apple Touch and 192/512px application icons derive from the same existing SVG. `site.webmanifest` references local assets and the English root route.

## Product claims

- Preserve native Anthropic, OpenAI and Gemini request families; do not imply arbitrary protocol translation.
- Auto mode tries available keys before compatible backup providers. Manual mode stays pinned.
- Describe targeted configuration changes, previews, baselines and rollback accurately.
- Local management does not imply offline inference or universal encryption of stored credentials.
- Distinguish upstream-reported costs from catalog estimates; unknown prices remain missing.
- Do not invent latency, memory, uptime or competitor benchmarks.

## Language scope and verification

Both locales now share the same structure, components and three routing stories. Chinese copy is independently written for Chinese readers; Chinese headings use a Songti-style system serif with normal emphasis rather than imitating Latin italics. Native-language state labels come from HTML data attributes. The Chinese page has its own social image, localized metadata and manifest entry point.

Verify 1440, 1024, 768, 390, 360 and 320px. Check single-row hero actions, provider type/channel labels, installation panels with long commands and expanded details, compact integration rows, heading font loading, footer type size, local asset references, schema/manifest parsing and JavaScript syntax.
