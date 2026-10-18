# Web UI CSS Guidelines

The stylesheet files live in [`internal/web/static/styles`](../internal/web/static/styles/).

## Purpose

These rules keep the Clipal Web UI visually consistent and safe to evolve.

Two failure modes are being prevented:

- semantic leakage: a class name says one thing, several pages reuse it for unrelated layouts, and a small change regresses other tabs
- visual drift: one-off colors, shadows, radii or button styles that do not belong to the design system

## Visual Language

The UI follows modern macOS (Sonoma/Sequoia) rather than flat web styling:

- **Window chrome**: a sticky bar in two parts. A thin opaque title band (`--chrome-band`, `--toolbar-height`) holds the brand on the left and the language/appearance controls on the right, with a hairline under it, like a macOS title bar. Below it, on translucent window colour (`--material-bar`) and with no rule underneath, sits the section tab bar, modelled on Safari's tabs: equal-width tabs in a recessed track (`tabbar`, `tab`), the selected tab raised as a white sheet, hairline dividers between unselected neighbours, and one tinted glyph per section (`tab--<section>`). On narrow screens unselected tabs collapse to their glyph. The tab bar belongs to the page content, so nothing cuts it off from it.
- **Content groups**: white (dark: raised gray) surfaces with a large radius, a hairline edge and a soft multi-layer shadow. One level of surface only: never put shadowed cards inside a card; inside a group, structure comes from spacing, section titles and hairline rows.
- **Navigation levels**: the window tab bar is level one. A section with sub-groups (Providers → Claude / OpenAI / Google) uses a tab view: segmented tabs centred on the top edge of a recessed panel (`client-tabview`), so everything for that group sits inside the panel. Controls that belong to a group never sit beside its tabs.
- **List + inspector**: a collection whose items carry rich detail (providers) uses a source list on the left and an inspector on the right (`provider-split`, `provider-panel`, `provider-inspector`), like macOS Mail or System Settings. List rows stay one line of identity plus state and a primary switch; actions and details live in the inspector. Selection is a rounded accent-tinted plate, rows are separated by space. On narrow screens the inspector replaces the list and offers Back.
- **Raised controls**: buttons, pop-up selects, segmented thumbs and checkboxes have a subtle top-to-bottom gradient, a 0.5px top highlight and a hairline edge. Primary actions use the accent gradient.
- **Inset fields**: text inputs are recessed (inner shadow, hairline edge) and show the accent focus ring.
- **Materials**: toolbar, sticky save bar, tooltips and toasts use translucent backgrounds with blur.
- **Sheets**: dialogs are rounded sheets with a deep shadow over a light scrim; on phones they become bottom sheets.
- **Icons**: one SVG sprite at the top of `index.html` defines every glyph as a `<symbol id="i-…">`; use it as `<svg class="ic" aria-hidden="true"><use href="#i-name"/></svg>`. Add new glyphs to the sprite instead of pasting paths. Do not use emoji as UI icons.
- **Copy budget**: the screen shows titles, labels, values, errors and risk warnings. Explanations, page and panel descriptions and field hints live behind an ⓘ hint (`hint`), restart-only fields use the amber restart hint (`hint--restart`), caveats use `hint--warn`. Recurring commands with a well-known glyph (refresh, restore, reset, copy, check) are icon buttons (`btn-icon`, `row-icon`) whose label is in `title` and `aria-label`.

The accent is the Clipal blue (`--accent`). Status uses semantic tones only: success, warning, danger, plus accent for "in use".

## Tokens

`tokens.css` defines every color, shadow, radius, type size, control height and duration, for light (`:root`) and dark (`.dark`). Components never branch on theme; they read tokens.

Use tokens, never literals, for:

- color: `--accent*`, `--success|warning|danger` (fills) and their `-text` (readable labels) and `-soft` (tints), `--bg-*`, `--fill*`, `--text-*`, `--separator`, `--border*`
- elevation: `--shadow-control`, `--shadow-control-inset`, `--shadow-group`, `--shadow-popover`, `--shadow-sheet`, `--shadow-thumb`
- shape: `--radius-xs|sm|md|lg|xl|full`
- size: `--control-sm|md`, `--text-2xs` … `--text-2xl`
- spacing: `--space-*`, `--gap-*`, `--inset-*`, `--page-*` (see Spacing and Layout)
- motion: `--ease`, `--ease-spring`, `--dur-fast|dur|dur-slow`, `--transition`

Add a token instead of repeating a literal in two places.

## Spacing and Layout

### Scale

Every margin, padding, gap and offset comes from one 4px-based scale with eight tiers:

| Token | Value | Typical use |
| --- | --- | --- |
| `--space-2xs` | 2px | insets inside control tracks (segmented control, tab bar), title to subtitle in dense rows |
| `--space-xs` | 4px | glyph to label in small text, title to its meta line, label to value in a tile |
| `--space-sm` | 8px | glyph to label in body text and buttons, neighbouring buttons, chips |
| `--space-md` | 12px | horizontal control padding, separate clusters in one bar |
| `--space-lg` | 16px | button padding, column gap in forms |
| `--space-xl` | 24px | gap between a title and the actions on the same row |
| `--space-2xl` | 32px | column gap between gauges |
| `--space-3xl` | 48px | page bottom and empty states (`--gap-section`) |

A computed value is allowed only when it is derived from tokens and a fixed component size, to put something on an alignment line (for example `calc((1lh - 16px) / 2)` to centre a glyph on the first text line). Comment why.

### Rhythm

Grouping comes from distance, not from lines or boxes. Page rhythm uses the semantic tokens, each clearly larger than the one inside it:

| Level | Token | Desktop | ≤760px | Between |
| --- | --- | --- | --- | --- |
| inner | `--gap-inner` | 8 | 8 | label and its control, a field and its hint |
| item | `--gap-item` | 16 | 12 | siblings in one group: fields, tiles; a panel's or page's title and the content under it |
| module | `--gap-module` | 24 | 16 | content groups (cards, panels) |
| section | `--gap-section` | 48 | 32 | page regions; page bottom; empty-state breathing room |

- A heading sits closer to its own content than to what comes before it: page titles sit `--tabbar-inset-bottom` + `--page-top` below the tab bar and `--gap-item` above their content.
- Padding scales with the container: `--inset-group` (24, 16 on narrow screens) for cards, panels and sheets; `--inset-tile` (12×16, 12 on narrow screens) for tiles, wells and banners inside a group.
- Hairlines are for lists of rows (provider rows, detail rows, activity) and the edge of a scrolling dialog body. Do not add a rule or a background block where a larger gap already separates the content.
- A row of cards of different heights stretches to one height (`align-items: stretch`) or the cards go full width; never leave a hole beside a short card.

### Grid and breakpoints

- Brand, tab bar, page titles and content share one left and right edge: `--content-width` minus `--page-gutter` on each side. Anything that bleeds to the gutters (the sticky save bar) offsets by `--page-gutter` and pads back by the same amount.
- Chrome spacing: band (40px, 52px on touch screens), `--tabbar-inset-top` (24, 16 on narrow screens) to the tab bar, `--tabbar-inset-bottom` (12) to the sticky edge, then `--page-top` (12, 8 on narrow screens) to the page title. The tab bar sits closer to the content than the band does to it. The insets are padding on `.toolbar`, so `--toolbar-offset` (the sticky height used by the save bar and toasts) is their exact sum; change them together.
- One breakpoint, 760px, changes the spacing tokens in `tokens.css` so every level shrinks together. Components never set their own narrow-screen padding. 640px only changes layout (stacking columns, bottom-sheet dialogs).
- Touch screens (`pointer: coarse`) get 44px targets. Adjacent buttons are at least `--space-sm` apart; dense icon clusters (28px targets) may use `--space-2xs` because each target already carries its own padding.

### Type rhythm

- Body text uses `--leading-normal`; headings use the tighter `--leading-tight`; long explanatory text in dialogs and results uses `--leading-relaxed`.
- Explanatory text that can run long caps its measure (`max-width` on `empty-state__hint`, tooltips at 280px).
- Values that change while visible (spend, usage, counts) use `font-variant-numeric: tabular-nums`. Single-line values truncate with an ellipsis on the text element, not on its flex container.

## Core Rules

### 1. Use page-scoped blocks for page layout

Page sections own their block names, for example `provider-row__main`, `integration-card__foot`, `status-card__groups`, `service-panel__actions`.

Do not reuse another page's block class because the layout looks similar.

### 2. Use primitives for visual language

| Need | Primitive |
| --- | --- |
| Push button | `btn` + `btn-primary` (one per view), `btn-secondary`, `btn-tinted` (main action repeated across many items), `btn-danger`, `btn-plain`; size `btn-sm` |
| Icon button | `icon-btn` (dialog close, toolbar), `row-icon` (dense list rows; `is-danger`, `is-active`) |
| Disabled-reason tooltip around a button | `action-shell` + `tooltip action-tooltip` + `tooltip-content` |
| Inline explanation | `tooltip hint` (+ `hint--restart`, `hint--warn`, `tooltip--end` near a right edge) wrapping `hint__btn` and `tooltip-content`; put it in a `label-row` after the label |
| Icon-only push button | `btn btn-secondary btn-icon` (label in `title` and `aria-label`) |
| Choice between 2–6 options | `segmented-group` + `segment-btn` (`segmented-group--sm` for compact) |
| On/off | `switch` + `slider` (`switch--sm` in rows) |
| Text entry | `form-input`, `form-textarea`, `form-select`, `search-field` |
| Passive capsule | `pill` + size `pill--xs|sm` + tone `pill--neutral|primary|success|warning|danger|muted` |
| Type marker on a title | `badge` (`badge--mono`) |
| Status | `status-dot` / `status-text` with `is-primary|success|warn|danger|info|off` |
| Content group | `card` (`card--wide` to span a grid row) |
| Key/value tiles | `kv-grid`, `kv-item`, `kv-label`, `kv-value` (`kv-value--mono`) |
| Monospace output | `log-output` |
| Section heading inside a group | `section-title` |
| Page title row (every tab with a title) | `page-intro`, `page-intro__title-row`, `page-intro__title`, `page-intro__meta` |
| Empty list or unavailable feature | `empty-state` + `__title`, `__hint`, `__actions`, `__detail` |

Prefer modifiers over one-off aliases. There is one button system; do not add parallel button families.

### 3. Shared classes must not own page rhythm

A primitive may define shape, internal padding, tone and typography. It must not define section spacing, page-specific borders or page alignment.

### 4. Keep block naming lightweight BEM

- block: `status-card`
- element: `status-card__header`
- modifier: `status-card__header--compact`
- state: `is-disabled`, `is-expanded`, `is-active`

### 5. No inline styles

Markup must not carry `style="…"`. The only exception is a value computed at runtime (for example a usage bar width via `:style`).

## File Ownership

- `tokens.css`: variables only, plus the media queries that retune them (narrow screens, coarse pointers)
- `base.css`: reset, base typography, page background, container, utilities, keyframes
- `primitives.css`: reusable controls and building blocks (buttons, segmented control, fields, checkbox, switch, pills, badges, status, card, kv tiles, tooltip, spinner, icons)
- `components.css`: shared composites: toolbar and tab bar, page header (`page-intro`), `empty-state`, connection banner, toasts, dialogs, the provider form layout, loading overlay
- `pages.css`: tab-specific blocks: providers, takeover, settings and backup, services, status

## Review Checklist

Before merging Web UI CSS work, verify:

- colors, shadows, radii, sizes and every spacing value come from tokens
- grouping reads from distance alone: inner < item < module < section, with no rule or tint doing the work spacing should do
- edges line up with the page gutter at desktop, 760px and phone widths
- the class name matches the actual ownership
- the change only affects the intended page or block
- a primitive is not being used as page layout glue
- no inline style, emoji icon or pasted SVG path was introduced (icons come from the sprite)
- explanatory copy sits behind a hint rather than as visible paragraphs
- the tab was checked in the browser in light and dark appearance and at phone width

## Anti-Patterns

Do not:

- reuse another page's header class
- add DOM-structure tests for exact visual layout
- create new aliases like `foo-pill` when `pill--*` modifiers are enough
- put page spacing onto a primitive selector
- hard-code a color or shadow that a token already expresses
- write a px margin, padding or gap, or add a per-component narrow-screen spacing override
- separate groups with stacked borders, rules or tinted boxes when a larger gap would do
