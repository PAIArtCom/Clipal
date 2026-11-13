# Clipal Design System Specification
## Kinetic Structuralism & Quantum Control Plane

---

## 1. Core Philosophy

Clipal is a high-throughput local LLM API gateway and mission control for coding agents. Its visual language is established as **Kinetic Structuralism & Quantum Control Plane**:
- **Sci-Fi Avant-Garde**: Evokes the precision, rigor, and mathematical discipline of deep-space telemetry and aerospace consoles.
- **Elegance & Restraint**: Strictly forbids cheap blurry neon blooms ("glow soup") and fuzzy pastel gradient blobs. All luminosity originates from sharp physical reflections and high-definition vector indicators.
- **Spatial 3D & Kinetic Motion**: Leverages genuine three-dimensional perspective (`perspective: 1200px ~ 1400px`), cursor-driven inertial parallax tilt, and mechanical spring damping.
- **Bold Structural Expression**: Beveled titanium corners, HUD telemetry crosshairs, immutable AST diff viewers, and microsecond packet telemetry streams.

---

## 2. Tokens & Materials

### 2.1 Surfaces & Depths
| Token | Physical Value | Architectural Purpose |
| :--- | :--- | :--- |
| `--bg-deep` | `#050507` | Deepest obsidian void hosting the 32px coordinate grid |
| `--bg-base` | `#07070a` | Viewport backdrop, cold carbon black |
| `--bg-surface` | `#0c0d12` | Base structural panel, polarized titanium texture |
| `--bg-surface-elevated` | `#12131a` | Elevated surface (modals, floating navbars, viewports) |
| `--bg-terminal` | `#09090d` | Monospaced code frames, AST diff inspectors, terminal logs |

### 2.2 Structural Dividers & High-Contrast Lines
- Dismisses muddy dropshadows in favor of razor-sharp structural lines:
  - `var(--border-subtle)`: `rgba(255, 255, 255, 0.08)` — Secondary data delimiters
  - `var(--border-medium)`: `rgba(255, 255, 255, 0.15)` — Default card boundaries & button outlines
  - `var(--border-bright)`: `rgba(255, 255, 255, 0.35)` — Active borders and specular highlights
  - `var(--border-active)`: `#00ff9d` — Active terminal scanlines & focus rings

### 2.3 Quantum Signal Colors (No Glows, Laser Precision)
| Signal Token | Value | Semantics & Usage |
| :--- | :--- | :--- |
| **Tritium Green** | `#00FF9D` | Active node, AST verified, session lock, healthy state |
| **Cobalt Cyan** | `#00E5FF` | Protocol multiplexing, diff highlights, telemetry metadata |
| **Reactor Amber** | `#FFB800` | 429 failover pivot, cooldown timer, long-context tiers |
| **Plasma Red** | `#FF3366` | Upstream circuit trip, key exhaustion, policy conflict |

---

## 3. 3D Spatial Depth & Motion Physics

### 3.1 Inertial Parallax Tilt
- Core viewports implement native 3D perspective:
  ```css
  .hero-console-viewport {
    perspective: 1400px;
  }
  .hero-3d-stage {
    transform-style: preserve-3d;
  }
  ```
- Containers calculate rotational matrices from cursor offsets using `requestAnimationFrame`, smoothly returning to equilibrium on cursor departure.

### 3.2 Z-Axis Layering
- Auxiliary telemetry cards float in spatial orbits displaced at `translateZ(40px)` and `translateZ(50px)`, creating real depth against the parent chassis.

### 3.3 Kinetic Spring Damping
- Kinetic easing: `--ease-kinetic: cubic-bezier(0.16, 1, 0.3, 1)`
- Mechanical latching: `--ease-mechanical: cubic-bezier(0.2, 0.8, 0.2, 1)`
- Tab switching and modal triggers utilize crisp micro-contractions and latching rather than limp opacity fades.

---

## 4. Typography & Information Density

- **Display Typography**: `Outfit` / `Inter`, weight 700–800, tight tracking (`-0.02em` to `-0.03em`).
- **Body & Labels**: `Inter`, line height 1.6, stark contrast against titanium surfaces.
- **Code & Telemetry**: `JetBrains Mono` / `SF Mono`, enforced tabular figures:
  ```css
  font-variant-numeric: tabular-nums;
  ```
  Ensures real-time micro-dollar counters (`$0.042189`) tick without layout jitter.

---

## 5. Anti-Patterns (Strict Rules)
- ❌ **No Glow Soup**: Never use `filter: blur(80px)` or diffuse neon gradient clouds.
- ❌ **No Muddy Grays**: Never use opaque low-contrast grays; use alpha-based stark white borders.
- ❌ **No Sluggish Transitions**: Keep interactions under 0.3s with snappy mechanical momentum.
