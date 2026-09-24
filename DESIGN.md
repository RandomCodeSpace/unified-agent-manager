---
version: 2
name: uam-web-workbench
description: Design system for `uam web`, the browser workbench for running coding agents. A dense, calm, borderless tool used for hours at a time, on a laptop over SSH and at 420px on a phone. One warm, mid-light canvas (a single theme, no dark scheme), one ink ladder for every piece of chrome, hairlines instead of boxes, a single restrained accent (blue) for focus and links, and one warm "needs you" colour (orange) that is the only thing allowed to shout. Inter for UI and chat, JetBrains Mono for code, ledger and diffs. No serif, no pills, no gradients, no marketing bands. Everything is a row or a column of text; surfaces step by one shade, never by borders inside borders.

colors:
  rail: "#dedcd6"
  canvas: "#e8e6e1"
  surface: "#eeece7"
  raised: "#f3f2ee"
  sunken: "#dedcd6"
  bubble-user: "#f3f2ee"
  ink: "#1c1b18"
  body: "#3d3b36"
  muted: "#5c5953"
  faint: "#7d7a72"
  hairline: "#d3d1ca"
  hairline-strong: "#bfbdb5"
  primary: "#1c1b18"
  on-primary: "#f3f2ee"
  accent: "#2a55bd"
  on-accent: "#f3f2ee"
  accent-wash: "#dbe3f5"
  focus: "#2a55bd"
  selection: "#cfdaf3"
  attention: "#9a400a"
  attention-wash: "#f4e1cf"
  success: "#196a41"
  success-wash: "#d8e8dd"
  warning: "#7a5500"
  warning-wash: "#efe3c3"
  error: "#b3261e"
  error-wash: "#f4d9d5"
  info: "#2a55bd"
  info-wash: "#dbe3f5"
  code-bg: "#dedcd6"
  diff-add-bg: "#d3e6da"
  diff-add-text: "#14532d"
  diff-del-bg: "#efd6d2"
  diff-del-text: "#7f1d1d"
  backdrop: "rgba(28, 27, 24, 0.35)"

typography:
  display-md:
    fontFamily: "'Inter Variable', system-ui, -apple-system, 'Segoe UI', Roboto, sans-serif"
    fontSize: 20px
    fontWeight: 600
    lineHeight: 1.3
    letterSpacing: -0.2px
  display-sm:
    fontFamily: "'Inter Variable', system-ui, sans-serif"
    fontSize: 17px
    fontWeight: 600
    lineHeight: 1.35
    letterSpacing: -0.1px
  title:
    fontFamily: "'Inter Variable', system-ui, sans-serif"
    fontSize: 14px
    fontWeight: 600
    lineHeight: 1.4
    letterSpacing: 0
  ui:
    fontFamily: "'Inter Variable', system-ui, sans-serif"
    fontSize: 13px
    fontWeight: 500
    lineHeight: 1.4
    letterSpacing: 0
  ui-regular:
    fontFamily: "'Inter Variable', system-ui, sans-serif"
    fontSize: 13px
    fontWeight: 400
    lineHeight: 1.4
    letterSpacing: 0
  chat-body:
    fontFamily: "'Inter Variable', system-ui, sans-serif"
    fontSize: 15px
    fontWeight: 400
    lineHeight: 1.6
    letterSpacing: 0
  chat-strong:
    fontFamily: "'Inter Variable', system-ui, sans-serif"
    fontSize: 15px
    fontWeight: 600
    lineHeight: 1.6
    letterSpacing: 0
  caption:
    fontFamily: "'Inter Variable', system-ui, sans-serif"
    fontSize: 12px
    fontWeight: 400
    lineHeight: 1.4
    letterSpacing: 0
  eyebrow:
    fontFamily: "'Inter Variable', system-ui, sans-serif"
    fontSize: 11px
    fontWeight: 600
    lineHeight: 1.2
    letterSpacing: 0.66px
    textTransform: uppercase
  code:
    fontFamily: "'JetBrains Mono Variable', ui-monospace, SFMono-Regular, Menlo, Consolas, 'Liberation Mono', monospace"
    fontSize: 13px
    fontWeight: 400
    lineHeight: 1.55
    letterSpacing: 0
  code-sm:
    fontFamily: "'JetBrains Mono Variable', ui-monospace, monospace"
    fontSize: 12px
    fontWeight: 400
    lineHeight: 1.5
    letterSpacing: 0
  keycap:
    fontFamily: "'JetBrains Mono Variable', ui-monospace, monospace"
    fontSize: 11px
    fontWeight: 500
    lineHeight: 1
    letterSpacing: 0

rounded:
  none: 0px
  xs: 4px
  sm: 6px
  md: 10px
  lg: 14px
  full: 9999px

spacing:
  "2": 2px
  "4": 4px
  "6": 6px
  "8": 8px
  "12": 12px
  "16": 16px
  "20": 20px
  "24": 24px
  "32": 32px
  "48": 48px

layout:
  rail-width: 264px
  drawer-width: 280px
  header-height: 44px
  chat-column-width: 100%
  chat-gutter: 24px
  chat-gutter-phone: 12px
  changes-panel-width: 440px
  bp-phone: 480px
  bp-rail-collapse: 960px
  bp-changes-inline: 1280px
  bp-wide: 1800px

components:
  shell:
    backgroundColor: "{colors.canvas}"
    textColor: "{colors.ink}"
    typography: "{typography.ui-regular}"
  rail:
    backgroundColor: "{colors.canvas}"
    textColor: "{colors.body}"
    typography: "{typography.ui-regular}"
    width: "{layout.rail-width}"
    padding: "8px"
  rail-group-label:
    textColor: "{colors.muted}"
    typography: "{typography.eyebrow}"
    padding: "12px 8px 4px"
  rail-project:
    textColor: "{colors.ink}"
    typography: "{typography.ui}"
    rounded: "{rounded.sm}"
    padding: "0 8px"
    height: 32px
  rail-task:
    textColor: "{colors.body}"
    typography: "{typography.ui-regular}"
    rounded: "{rounded.sm}"
    padding: "0 8px 0 24px"
    height: 30px
  rail-task-active:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    typography: "{typography.ui}"
  deck-row:
    backgroundColor: transparent
    textColor: "{colors.ink}"
    typography: "{typography.ui-regular}"
    rounded: "{rounded.sm}"
    padding: "0 12px"
    height: 44px
  main-header:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    typography: "{typography.display-sm}"
    borderColor: "{colors.hairline}"
    height: "{layout.header-height}"
    padding: "0 16px"
  transcript:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.body}"
    typography: "{typography.chat-body}"
    padding: "24px {layout.chat-gutter}"
  bubble-user:
    backgroundColor: "{colors.bubble-user}"
    textColor: "{colors.ink}"
    typography: "{typography.chat-body}"
    rounded: "{rounded.lg}"
    padding: "10px 14px"
    maxWidth: "min(78%, 560px)"
  message-assistant:
    backgroundColor: transparent
    textColor: "{colors.body}"
    typography: "{typography.chat-body}"
    padding: "0"
  thinking-block:
    textColor: "{colors.muted}"
    typography: "{typography.ui-regular}"
    borderColor: "{colors.hairline-strong}"
    padding: "4px 0 4px 12px"
  ledger:
    textColor: "{colors.muted}"
    typography: "{typography.ui-regular}"
    padding: "0"
  ledger-row:
    textColor: "{colors.muted}"
    typography: "{typography.code-sm}"
    height: 24px
    padding: "0 0 0 20px"
  subagent-block:
    backgroundColor: transparent
    textColor: "{colors.body}"
    typography: "{typography.ui-regular}"
    borderColor: "{colors.hairline-strong}"
    padding: "8px 0 8px 12px"
  approval-card:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.ink}"
    typography: "{typography.ui-regular}"
    borderColor: "{colors.hairline}"
    accentColor: "{colors.attention}"
    rounded: "{rounded.md}"
    padding: "12px 14px"
  question-card:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.ink}"
    typography: "{typography.ui-regular}"
    borderColor: "{colors.hairline}"
    accentColor: "{colors.attention}"
    rounded: "{rounded.md}"
    padding: "12px 14px"
  composer:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.ink}"
    typography: "{typography.chat-body}"
    borderColor: "{colors.hairline}"
    rounded: "{rounded.md}"
    padding: "10px 12px 8px"
    minHeight: 88px
  composer-textarea:
    backgroundColor: transparent
    textColor: "{colors.ink}"
    typography: "{typography.chat-body}"
    padding: "0"
  model-select:
    backgroundColor: transparent
    textColor: "{colors.body}"
    typography: "{typography.ui}"
    rounded: "{rounded.sm}"
    padding: "0 8px"
    height: 28px
  mode-select:
    backgroundColor: transparent
    textColor: "{colors.body}"
    typography: "{typography.ui}"
    rounded: "{rounded.sm}"
    padding: "0 8px"
    height: 28px
  button-primary:
    backgroundColor: "{colors.primary}"
    textColor: "{colors.on-primary}"
    typography: "{typography.ui}"
    rounded: "{rounded.sm}"
    padding: "0 12px"
    height: 32px
  button-secondary:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.ink}"
    typography: "{typography.ui}"
    borderColor: "{colors.hairline-strong}"
    rounded: "{rounded.sm}"
    padding: "0 12px"
    height: 32px
  button-ghost:
    backgroundColor: transparent
    textColor: "{colors.body}"
    typography: "{typography.ui}"
    rounded: "{rounded.sm}"
    padding: "0 8px"
    height: 32px
  button-danger:
    backgroundColor: transparent
    textColor: "{colors.error}"
    typography: "{typography.ui}"
    rounded: "{rounded.sm}"
    padding: "0 12px"
    height: 32px
  button-icon:
    backgroundColor: transparent
    textColor: "{colors.muted}"
    rounded: "{rounded.sm}"
    size: 28px
  text-input:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.ink}"
    typography: "{typography.ui-regular}"
    borderColor: "{colors.hairline-strong}"
    rounded: "{rounded.sm}"
    padding: "0 10px"
    height: 36px
  chip:
    backgroundColor: transparent
    textColor: "{colors.muted}"
    typography: "{typography.caption}"
    rounded: "{rounded.xs}"
    padding: "0 6px"
    height: 20px
  chip-attention:
    backgroundColor: "{colors.attention-wash}"
    textColor: "{colors.attention}"
    typography: "{typography.caption}"
    rounded: "{rounded.xs}"
    padding: "0 6px"
    height: 20px
  keycap:
    backgroundColor: "{colors.sunken}"
    textColor: "{colors.muted}"
    typography: "{typography.keycap}"
    borderColor: "{colors.hairline-strong}"
    rounded: "{rounded.xs}"
    padding: "0 5px"
    height: 18px
  code-block:
    backgroundColor: "{colors.code-bg}"
    textColor: "{colors.ink}"
    typography: "{typography.code}"
    rounded: "{rounded.md}"
    padding: "10px 12px"
  code-inline:
    backgroundColor: "{colors.sunken}"
    textColor: "{colors.ink}"
    typography: "{typography.code}"
    rounded: "{rounded.xs}"
    padding: "1px 4px"
  changes-sheet:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.body}"
    typography: "{typography.ui-regular}"
    borderColor: "{colors.hairline}"
    width: "{layout.changes-panel-width}"
  diff-line-add:
    backgroundColor: "{colors.diff-add-bg}"
    textColor: "{colors.diff-add-text}"
    typography: "{typography.code-sm}"
  diff-line-del:
    backgroundColor: "{colors.diff-del-bg}"
    textColor: "{colors.diff-del-text}"
    typography: "{typography.code-sm}"
  popover:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.ink}"
    typography: "{typography.ui-regular}"
    borderColor: "{colors.hairline-strong}"
    rounded: "{rounded.md}"
    padding: "4px"
  dialog:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.ink}"
    typography: "{typography.ui-regular}"
    rounded: "{rounded.md}"
    padding: "20px"
    maxWidth: 440px
---

## Overview

`uam web` is a workbench, not a magazine. The previous system (an ElevenLabs marketing analysis) gave the app an off-white canvas, a light serif display face, pill CTAs and gradient orbs. Those are page furniture; a person who sits in this screen for six hours needs a launcher: one ink ladder, one surface ladder, rows instead of cards, and colour that only appears when the agent needs a human.

What survives from the old file: restraint, hairlines, calm, warm near-black ink on a warm off-white floor. What changes: the serif and the marketing scale go; type is one sans (Inter) plus one mono (JetBrains Mono); radii tighten to a 6px app dialect; depth comes from stepping the surface one shade, never from stacking borders; there is one mid-light scheme (see Theme under Colors); and every token has its contrast measured on every ground it sits on.

**Key characteristics**
- Two ladders carry the whole UI: surfaces (`rail` → `canvas` → `surface` → `raised`, plus `sunken` for wells) and text (`ink` → `body` → `muted` → `faint`). Chrome never uses a colour outside these ladders except the four semantic tones, the accent and the attention colour.
- Borderless by default. A component gets a hairline only when it is interactive and floats above its parent (composer, cards, popovers). Nothing inside a card gets another border; nested structure is shown with a `sunken` well or an indent, never a side stripe.
- One accent (`accent`, blue) for focus, links, the working indicator and selection. One attention colour (`attention`, orange) reserved for "the agent needs you". Success/warning/error are marks and text, never fills, except their `-wash` on a chip.
- The assistant does not get a bubble. The user does. Assistant output is the page; the user's turns are the interruptions.
- Density is a row: 30–32px rail rows, 24px ledger rows, 44px deck rows, 28px composer controls. Mobile gets 44px targets by padding, not by scaling type.
- Motion is functional and short (100–240ms) and fully disabled under `prefers-reduced-motion`.

## Principles

1. **Stable data only.** Rows show state the server knows (task state, model, tool counts), never heuristics or previews. No skeleton shimmer on rows; empty is empty.
2. **Typographic hierarchy before boxes.** If two things need to be told apart, first try weight (400/500/600), then colour step (`ink`/`body`/`muted`), then indent, then a hairline. A bordered box is the last resort.
3. **One accent, one alarm.** `accent` is the only chromatic chrome colour. `attention` is the only warm one and the only one that may fill (a chip wash) in the rail or deck.
4. **Surfaces step, they do not stack.** Depth = one shade lighter. Shadows are for things that float over content (popover, dialog, drawer) only.
5. **Code is code.** Anything the agent typed, ran, edited or diffed is set in JetBrains Mono at 12–13px on `code-bg`. Prose is Inter.
6. **Same layout everywhere.** Phone, laptop and 1920px see the same components; only the rail's presence and the column's gutters change.
7. **CSP-clean.** No inline styles, no inline scripts, fonts from `'self'`. Anything dynamic is a class or a `data-` attribute.

## Colors

### Theme
One theme. The owner decided on 2026-09-24 that `uam web` ships a single mid-light scheme instead of a light/dark pair: a dimmed-paper canvas (L* ≈ 91), clearly lighter than a dark theme and clearly softer than a stark white one, so the screen reads the same on every machine and nothing flips with the OS setting. There is no theme toggle, no stored preference and no `prefers-color-scheme` switch; `color-scheme: light` keeps native controls in step.

All greys are warm near-neutrals (a hint of yellow in every grey). Nothing reaches pure white: `raised` (#f3f2ee) is the lightest surface. The ladder steps around the canvas: the rail one shade darker, raised surfaces one shade lighter, wells one shade darker.

**Adjustments.** This repository does not draw side-stripe accent borders (`border-left` rules). Where a design would reach for a 2px left rule (the thinking disclosure, the subagent block, the approval card's attention edge) the implementation uses a `sunken` well with a small radius for grouping and the `chip-attention` in the card header for attention. The intent (grouping, "needs you") is kept; the stripe is not.

### Surfaces
| Role | Value | Use |
|---|---|---|
| `rail` | #dedcd6 | Rail and drawer floor, one step darker than the canvas |
| `canvas` | #e8e6e1 | Main pane: header, transcript, deck, Changes sheet, login |
| `surface` | #eeece7 | Hover step on raised controls (secondary button hover) |
| `raised` | #f3f2ee | Composer, approval/question cards, popovers, dialogs, inputs, active rail row, deck row hover |
| `sunken` | #dedcd6 | Wells: code blocks, thinking body, subagent block, keycaps, inline code |
| `bubble-user` | #f3f2ee | User message bubble (a raised surface) |
| `code-bg` | #dedcd6 | Alias of `sunken`, kept separate so code can be retuned alone |
| `selection` | #cfdaf3 | `::selection` |
| `backdrop` | rgba(28,27,24,.35) | Behind dialogs, the phone drawer and the Changes overlay |

### Text
| Role | Value | Use |
|---|---|---|
| `ink` | #1c1b18 | Titles, active rail item, user bubble text, code |
| `body` | #3d3b36 | Assistant prose, default UI text |
| `muted` | #5c5953 | Meta, captions, ledger rows, group labels, placeholders, thinking text |
| `faint` | #7d7a72 | Non-text only: chevrons, closed-task marks, diff line numbers (≥3:1) |

### Lines
| Role | Value | Use |
|---|---|---|
| `hairline` | #d3d1ca | Separators, header rule, card edge, code block edge, diff hunk header fill |
| `hairline-strong` | #bfbdb5 | Input edge at rest, secondary button edge, popover edge |

Hairlines are decorative and exempt from contrast rules. Inputs and cards are identified by their `raised` fill and their content, not by the hairline, so WCAG 1.4.11 does not depend on it. The focus ring does the 3:1 job.

### Action and signal
| Role | Value | Use |
|---|---|---|
| `primary` / `on-primary` | #1c1b18 / #f3f2ee | Primary button (ink fill). There is no coloured primary button. |
| `accent` / `on-accent` | #2a55bd / #f3f2ee | Links, focus ring, working dot, new-activity dot, selected row check |
| `accent-wash` | #dbe3f5 | Selected popover row, `info` chip |
| `focus` | #2a55bd | `:focus-visible` outline; same as `accent` |
| `attention` / `attention-wash` | #9a400a / #f4e1cf | "Needs you": permission and question states, rail and deck counts, the chip on a pending card |
| `success` / `success-wash` | #196a41 / #d8e8dd | Completed mark, allowed chip, diff `+` counts |
| `warning` / `warning-wash` | #7a5500 / #efe3c3 | Interrupted state, non-blocking notices |
| `error` / `error-wash` | #b3261e / #f4d9d5 | Failed state, Deny button text, error lines, diff `−` counts |
| `info` / `info-wash` | #2a55bd / #dbe3f5 | Informational notice line (aliases accent) |

### Diff
| Role | Value |
|---|---|
| `diff-add-bg` / `diff-add-text` | #d3e6da / #14532d |
| `diff-del-bg` / `diff-del-text` | #efd6d2 / #7f1d1d |

Hunk headers are `muted` on `hairline`. Unchanged lines are `body` on `code-bg`. Add/del backgrounds are full-row; the `+`/`-` gutter glyph is the text colour at weight 500.

### Contrast (WCAG 2.x, computed)
Every text colour clears AA (≥4.5:1) on every ground it can sit on; `faint` is glyph-only and held at ≥3:1. Computed from the tokens above; `web/src/styles.css` is the source of truth.

| Pair | Ratio |
|---|---|
| ink on canvas | 13.81 |
| ink on rail | 12.56 |
| ink on raised / bubble-user | 15.37 |
| ink on sunken | 12.56 |
| body on canvas | 8.97 |
| body on rail / sunken / code-bg | 8.16 |
| body on raised | 9.99 |
| muted on canvas | 5.60 |
| muted on rail / sunken | 5.09 |
| muted on raised | 6.23 |
| muted on hairline (hunk header) | 4.57 |
| faint on canvas (glyphs only) | 3.44 |
| faint on rail / code-bg (glyphs only) | 3.13 |
| faint on raised (glyphs only) | 3.83 |
| on-primary on primary | 15.37 |
| accent on canvas | 5.37 |
| accent on rail / sunken | 4.89 |
| accent on raised | 5.98 |
| accent on accent-wash | 5.21 |
| attention on canvas | 5.41 |
| attention on rail | 4.92 |
| attention on raised | 6.02 |
| attention on attention-wash | 5.31 |
| success on canvas | 5.30 |
| success on rail / sunken | 4.82 |
| success on raised | 5.90 |
| success on success-wash | 5.19 |
| warning on canvas | 5.38 |
| warning on rail | 4.90 |
| warning on raised | 6.00 |
| warning on warning-wash | 5.26 |
| error on canvas | 5.24 |
| error on rail / sunken | 4.77 |
| error on raised | 5.84 |
| error on error-wash | 4.90 |
| diff-add-text on diff-add-bg | 6.99 |
| diff-del-text on diff-del-bg | 7.26 |

Rule for new pairs: any text colour must clear 4.5:1 on `canvas`, `rail`, `raised` and `sunken`. `muted` on `hairline` (4.57) and the semantic tones on `rail`/`sunken` (4.8–4.9) are the tightest pairs in the system; do not lighten them and do not darken the grounds.

## Typography

### Families
- **UI and chat:** Inter Variable, `@fontsource-variable/inter` (5.3.0, SIL OFL 1.1). Import `@fontsource-variable/inter` (the `wght` axis file; Latin subset is ~47 KB woff2). Family name as registered by the package: `'Inter Variable'`.
- **Code, ledger, diff, keycaps:** JetBrains Mono Variable, `@fontsource-variable/jetbrains-mono` (5.3.0, SIL OFL 1.1). Family name: `'JetBrains Mono Variable'`.
- Both are imported once in `web/src/main.tsx` and bundled by Vite, so the woff2 files are served from the same origin (`font-src 'self'`). No Google Fonts, no CDN, no proprietary faces.
- Fallbacks: `system-ui, -apple-system, 'Segoe UI', Roboto, sans-serif` and `ui-monospace, SFMono-Regular, Menlo, Consolas, 'Liberation Mono', monospace`.
- `font-display: swap` (package default). Set `font-size-adjust` is not needed; Inter and the system fallbacks share x-height closely enough.

### Scale
| Token | Size | Weight | Line | Tracking | Use |
|---|---|---|---|---|---|
| `display-md` | 20px | 600 | 1.3 | -0.2px | "Needs you" deck heading, empty-state title, login title |
| `display-sm` | 17px | 600 | 1.35 | -0.1px | Task title in main header, dialog title |
| `title` | 14px | 600 | 1.4 | 0 | Card title ("Copilot wants to run"), Changes sheet file header, project name |
| `ui` | 13px | 500 | 1.4 | 0 | Buttons, active rail row, chips with a label, tabs, model/mode select |
| `ui-regular` | 13px | 400 | 1.4 | 0 | Rail rows, menu items, deck row text, meta lines |
| `chat-body` | 15px | 400 | 1.6 | 0 | User and assistant prose. 16px at ≤480px (also prevents iOS input zoom in the composer). |
| `chat-strong` | 15px | 600 | 1.6 | 0 | `<strong>` and headings inside markdown (h1–h3 all render at 15/600; h1 gets 4px more top margin) |
| `caption` | 12px | 400 | 1.4 | 0 | Timestamps, "3 tool calls · 1 running", tool-call durations, chips |
| `eyebrow` | 11px | 600 | 1.2 | 0.66px, uppercase | Rail group labels (NEEDS YOU, PROJECTS), popover group labels, Changes sheet sections |
| `code` | 13px | 400 | 1.55 | 0 | Code blocks; inline code is `0.92em` of the surrounding size |
| `code-sm` | 12px | 400 | 1.5 | 0 | Ledger tool lines, file paths, diff body, command in approval card |
| `keycap` | 11px | 500 | 1 | 0 | Keyboard hints |

### Rules
- Weight is ternary: 400 body, 500 controls, 600 titles. No 300, no 700+.
- Only `display-*` carry negative tracking, and barely. Never track body.
- Numbers that line up (counts, durations, line numbers) use `font-variant-numeric: tabular-nums` via the `.num` class.
- Ligatures are **off** in mono (`font-variant-ligatures: none`): `!=`, `=>` and `->` must read as typed inside diffs and commands.
- Markdown headings inside assistant messages do not scale up. A chat is not a document; hierarchy inside a message comes from weight and spacing.
- The transcript and assistant content fill the available main-pane width after the rail, any inline side panel and the existing gutters. Do not impose a fixed chat-column width cap.

## Layout

### Shell
Full viewport, no page scroll: `height: 100dvh; display: grid; grid-template-columns: 264px 1fr`. The rail and the main pane scroll independently. The main pane is itself a column: header (44px, hairline below) → transcript (flex 1, `overflow-y: auto`, `overscroll-behavior: contain`) → composer (pinned).

### Rail (264px)
Sits on `rail`, one step darker than the canvas. Top row: wordmark `uam` at `ui` 600 + a search/switcher affordance showing the `⌘K` keycap. Then the **Needs you** group (only rendered when count > 0) and the **Projects** group. Each project is a row; its tasks are nested rows indented 16px with a 1px `hairline` vertical rule at x = 15px running the height of the list (the indent line, not a box). Bottom: connection line (`host · connected`) at `caption` in `muted`. Rail padding 8px; no border between rail and main, the `rail`→`canvas` step is the seam.

### Breakpoints
| Width | Rail | Chat column | Composer | Changes sheet |
|---|---|---|---|---|
| ≤480 (phone, 420 target) | Off-canvas drawer, 280px, `backdrop`, slides from left; header shows a menu button | 100% width, 12px gutters | Full width, sticky bottom, `padding-bottom: env(safe-area-inset-bottom)`; textarea 16px | Full-screen sheet from the bottom |
| 481–959 | Drawer as above | Full available width, 16px gutters | Inside column | Overlay panel 100% |
| 960–1279 | Fixed 264px | Full available width, 24px gutters | Inside column | Overlay panel 440px from right |
| 1280–1799 (1440 target) | Fixed 264px | Full available width, 24px gutters | Inside column | Inline 440px panel; column fills the remaining width |
| ≥1800 (1920 target) | Fixed 264px | Full available width, 24px gutters | Inside column | Inline 440px; column fills the remaining width |

At 1920 the main pane is 1656px wide with the side panel closed. The transcript column fills that width, with 24px gutters on each side. An inline Changes panel leaves 1216px for the column. Do not add unrelated side widgets.

### Bubble widths
- User bubble: `max-width: min(78%, 560px)` of the column; at ≤480px `88%`. Aligned right, `margin-left: auto`.
- Assistant message: full column width, no max beyond the column.
- Approval/question cards, subagent blocks, code blocks: full column width.
- Composer: full available column width between the existing gutters, so its edges line up with the transcript. No fixed width cap.

### Spacing and density
4px base. Tokens: 2, 4, 6, 8, 12, 16, 20, 24, 32, 48.
- Between turns: 24px. Between an assistant's ledger and its prose: 12px. Between paragraphs inside a message: 10px (`chat-body` line-height carries the rest).
- Rail rows 30–32px; ledger rows 24px; deck rows 44px; header 44px; buttons 32px (28px small, 36px large); inputs 36px; chips 20px; keycaps 18px.
- Phone: rail-drawer rows 40px, deck rows 52px, buttons 40px, to reach 44px effective targets with row padding.

## Elevation & Depth

| Level | Treatment | Shadow | Use |
|---|---|---|---|
| 0 Flat | Surface step only | none | Rail on `rail`, everything else on `canvas` |
| 1 Raised | `raised` fill + 1px `hairline` | none | Composer, approval/question cards, inputs, code blocks (code uses `sunken` + hairline) |
| 2 Floating | `raised` + 1px `hairline-strong` + shadow | `0 4px 16px rgba(28,27,24,.12)` | Popovers (task menu, model select), tooltips |
| 3 Modal | `raised` + shadow + `backdrop` | `0 16px 48px rgba(28,27,24,.18)` | Dialogs, phone drawer, Changes overlay and bottom sheet |

Nothing inside a Level 1 surface gets its own border. Nested structure inside cards uses an indent or a `sunken` well with a small radius; never a side stripe (see Adjustments under Theme).

## Shapes

| Token | Value | Use |
|---|---|---|
| `none` | 0 | Hairline separators, header rule, Changes sheet edge |
| `xs` | 4px | Keycaps, chips, inline code, state marks that are squares |
| `sm` | 6px | Buttons, inputs, rail rows, popover rows, model/mode selects |
| `md` | 10px | Composer, code blocks, approval/question cards, popovers, dialogs |
| `lg` | 14px | User bubble only |
| `full` | 9999px | Avatars, state dots, spinner |

No pills. Buttons are 6px rectangles at every size. The 14px bubble radius is the softest shape in the system and it belongs to the human.

## Components

Every component is a class in `web/src/styles.css`; no inline style. States are modifier classes or `data-state` attributes. Hover is a one-step surface change (`canvas` → `raised`; on the rail, `rail` → `canvas`; inside a raised surface, `raised` → `canvas`) unless noted; press is the same step plus `ink` text.

### Rail

**`rail-group-label`** — `eyebrow` in `muted`, padding 12px 8px 4px. "NEEDS YOU · 2" renders the count in `attention`; "PROJECTS" is plain.

**`rail-project`** — 32px row, `ui` (500) in `ink`, chevron glyph (`faint`) at left, project name, task count in `caption` `muted` at right. Click toggles; keyboard: Enter toggles, Right/Left expand/collapse. Hover: `canvas` fill, radius `sm`.

**`rail-task`** — 30px row, `ui-regular` in `body`, indented 24px with the indent line. Left: 8px state mark (see State marks). Text truncates with an ellipsis; no wrapping. Right (on hover/focus only): `button-icon` "…" menu. Active: `rail-task-active` (`raised` fill, `ink` text, weight 500). Tasks needing you also show a 6px `attention` dot at the far right regardless of hover.

**`rail-footer`** — `caption` in `muted`: `host · connected`; a `success` dot when the event stream is live, `error` dot with "reconnecting…" when not.

Drawer variant (≤959px): same rows at 40px height; opens from the header menu button and from a left-edge swipe; closes on selection, on `Esc`, on backdrop tap.

### Needs-you deck (home)
Shown in the main pane when no task is open, or when the rail group is clicked. Heading `display-md` "Needs you" with the count in `attention`; below it a plain list of `deck-row`s, then a "Recent" eyebrow and rows of recent tasks. No cards, no grid.

**`deck-row`** — 44px row: state mark (8px) → task title `ui-regular` `ink` (truncate) → project name `caption` `muted` → right: relative time `caption` `muted` and, for needs-permission rows, the requested action in `code-sm` (`npm run build`). Hover: `raised` fill. Enter/click opens the task; for permission rows, the row exposes inline `Allow once` / `Deny` ghost buttons on hover/focus so the deck can be cleared without opening every task.

### Main header
44px, `canvas`, hairline below. Left: menu button (phone only), task title `display-sm` `ink` truncated, then a `chip` with the state. Right: `Changes` `button-ghost` with count in `.num` (shows `attention` dot when there are uncommitted changes the user has not viewed), `button-icon` "…". The model is not shown here; it lives in the composer.

### User bubble
**`bubble-user`** — `bubble-user` fill, `ink` text, `chat-body`, radius `lg`, padding 10px 14px, right-aligned, `max-width: min(78%, 560px)`. No border, no avatar, no name. Timestamp on hover as `caption` `muted` below the bubble, right-aligned. Markdown inside user turns renders (code spans, lists) but without headings. Attachments (future) sit above the bubble as `chip`s, right-aligned.

### Assistant message
**`message-assistant`** — no bubble, no avatar, no name. Full column width, `body` colour, `chat-body`. Optional provider line above the first assistant turn of a task only: `caption` `muted` "copilot · gpt-5". Markdown: paragraphs 10px apart; lists indent 20px; blockquote = a `sunken` well (radius `sm`), `muted`; tables use hairline row rules only; links `accent` underlined on hover only.

**`code-block`** — `code-bg` fill, 1px `hairline`, radius `md`, padding 10px 12px, `code`. Header row (24px) inside the block when a language or filename is known: `code-sm` `muted` label at left, `button-icon` copy at right. Streams line by line; while streaming the last line ends with the caret (see Motion). Max height 480px with internal scroll; a "Show all" ghost button expands. Horizontal overflow scrolls; never wrap code.

**`code-inline`** — `sunken` fill, radius `xs`, padding 1px 4px, `0.92em`.

### Thinking block
**`thinking-block`** — a disclosure, not a card. No rule: the expanded text sits in a `sunken` well (radius `sm`, padding 8px 12px) indented under the row.
- **Collapsed:** one 24px row: chevron glyph (`faint`) + "Thought for 12s" in `ui-regular` `muted`. Click/Enter toggles. `aria-expanded` on the row button.
- **Streaming:** same row reads "Thinking…"; the label carries the text shimmer (Motion); no content shown until the user opens it. Opening while streaming shows the tokens as they arrive.
- **Expanded:** the row stays; below it the reasoning text at `ui-regular` (13px) in `muted`, max-height 320px with internal scroll, inside the well. Reasoning is never set in `chat-body` and never in `body` colour; it must read as quieter than the answer.
- Reduced motion: no shimmer; "Thinking…" static.

### Tool ledger
**`ledger`** — sits between the thinking row and the answer. Summary row (24px): `ui-regular` `muted`: "3 tool calls · 1 running" with a 12px spinner (`accent`) while any run; chevron to expand. Collapsed by default when all tool calls are done; expanded while any run.

**`ledger-row`** — 24px, `code-sm`, padding-left 20px, left glyph 12px:
- done: check glyph in `success`; label `muted`: `Read web/src/components/Conversation.tsx`
- running: spinner in `accent`; label `body`
- failed: cross in `error`; label `error`; expanding the row shows stderr in a `code-block`
- right: duration `caption` `.num` `muted` ("0.8s")
Tool name is the first word at weight 500 (`Read`, `Grep`, `Edit`, `Bash`); the argument follows in 400. Long arguments truncate from the middle so file names survive. Rows are keyboard-focusable; Enter expands the call's input/output as a `code-block` beneath it.

### Subagent block
**`subagent-block`** — a `sunken` block, radius `md`, padding 0 12px, full width (no left rule); wells inside it step up to `canvas`, its user bubble to `raised`. Header row (24px): "Subagent" `eyebrow` `muted` + its task in `ui` (500) `body` + state mark at right. Body: its own thinking row, ledger and prose at the same specs as the parent, all at `ui-regular` (13px) rather than 15px so the nesting reads as subordinate. Collapsed by default once completed, showing the final line of its summary in `muted`. Never nest more than one level visually; deeper subagents flatten into the parent subagent's ledger.

### Approval card
**`approval-card`** — `raised` fill, 1px `hairline`, radius `md`, padding 12px 14px; while pending, a `chip-attention` ("Needs permission") heads the card and is its only orange. Content: title `title` `ink` ("Copilot wants to run"), the requested action in a `code-block`-styled well (`sunken`, `code-sm`, no header), optional one-line reason `ui-regular` `muted`. Footer row: `Allow once` (`button-primary`), `Allow for task` (`button-secondary`), `Deny` (`button-danger`), right-aligned; on phone they stack full-width in that order. Keyboard: the card receives focus when it appears; `Enter` = Allow once, `Shift+Enter` = Allow for task, `Esc` = Deny (all announced in a `caption` hint row for keyboard users). After a decision the card collapses to one `ledger-row` ("Allowed once · npm run build") and the transcript continues.

### Question card
**`question-card`** — same chrome as the approval card. Title = the agent's question in `ui` (500) `ink`; choices as a vertical list of `button-secondary` rows (full width, left-aligned, radio semantics) or a `text-input` when free-form; "Send" as `button-primary`. Selected choice shows a check glyph in `accent`. After answering, collapses to a `ledger-row` ("Answered · Use pnpm").

### Composer
**`composer`** — `raised`, 1px `hairline`, radius `md`, padding 10px 12px 8px, `min-height: 88px`, pinned at the bottom of the column with 12px above the viewport edge and the column gutters at the sides. Focus-within: border becomes `hairline-strong` and the focus ring is on the textarea only (no double ring).
- Textarea: `chat-body`, `ink`, placeholder `muted` "Message copilot… (Enter to send, Shift+Enter for a new line)". Auto-grows to 40% of the viewport, then scrolls.
- Bottom row (28px): left cluster = `model-select`, `mode-select` ("default" / "yolo"), `mode-select` ("interactive" / "autopilot"); right cluster = queue hint and the send button.
- Send: 28px `button-primary` square with an up-arrow glyph. While the task is working the same slot is `Stop` (`button-secondary`, 28px, square glyph). Steer/queue (soon): while working, typing a message and pressing Enter shows a `chip` "Queued" under the composer with an "×"; `Ctrl+Enter` sends as a steer immediately. These chips sit in a 24px row that only exists while non-empty.
- Phone: composer spans the viewport width, radius `md` kept, `padding-bottom` adds the safe-area inset, textarea at 16px.

**`model-select`** / **`mode-select`** — `button-ghost` at 28px with `ui` text and a small chevron; the model name is set in `code-sm` (models are identifiers, not prose). Opens a `popover`: optional search input at the top when > 8 items, groups labelled by `eyebrow`, rows 30px (`ui-regular`, `ink`), selected row `accent-wash` fill with a check glyph in `accent` at right. Roving tab index, type-ahead, `Esc` closes. Mode changes take effect on the next message; the select shows the pending mode in `accent` until then.

### State marks and chips
State marks are 8px glyphs at the left of rail/deck rows; chips carry the word.

| State | Mark | Chip |
|---|---|---|
| working | 8px `accent` dot, pulsing (static ring under reduced motion) | "Working" `chip` `muted` |
| needs permission | 8px `attention` dot | "Needs permission" `chip-attention` |
| needs answer | 8px `attention` dot | "Needs answer" `chip-attention` |
| completed | check glyph `success` | "Completed" `chip` `success` text |
| cancelled | dash glyph `muted` | "Cancelled" `chip` `muted` |
| failed | cross glyph `error` | "Failed" `chip` `error` text |
| interrupted | pause glyph `warning` | "Interrupted" `chip` `warning` text |
| closed | hollow dot `faint` | "Closed" `chip` `faint`-glyph, `muted` text |

Only the attention chip has a fill. Every other chip is text plus glyph on the parent surface. Chips never appear inside prose.

### Changes sheet
**`changes-sheet`** — `canvas`, 1px `hairline` on its content edge, 440px inline at ≥1280px, overlay at 960–1279, full-screen bottom sheet on phone. Header (44px): "Changes" `title`, counts `caption` `.num` ("3 files · +48 −12" with `success`/`error` numerals), close `button-icon`. Section one: file list, 30px rows: status letter in `code-sm` (`M` `muted`, `A` `success`, `D` `error`), path `code-sm` `body` (truncate middle), `+n −n` `.num` at right. Selecting a file loads its unified diff below (or replaces the list on phone, with a back button). Diff: `code-sm` on `code-bg`, hunk header `muted` on `sunken`, add/del rows per Diff tokens, line numbers `faint` `.num` in a 2-column gutter, no wrap, horizontal scroll. No syntax highlighting in v1; add/del colour is the only colour.

### Buttons
| Variant | Fill | Text | Edge | Use |
|---|---|---|---|---|
| `button-primary` | `primary` | `on-primary` | none | One per view: Send, Allow once, dialog confirm |
| `button-secondary` | `raised` | `ink` | 1px `hairline-strong` | Allow for task, Stop, dialog cancel, choice rows |
| `button-ghost` | none | `body` | none | Header actions, selects, "Show all"; hover is one surface step |
| `button-danger` | none | `error` | none | Deny, Delete task; hover `error-wash` |
| `button-icon` | none | `muted` | none | 28px square; hover `ink` text + surface step |
All: `ui` 13/500, radius `sm`, height 32 (28 small, 36 large; 40 on phone). Disabled: 45% opacity, no pointer. Loading: label stays, a 12px spinner replaces the leading glyph; never change width while loading.

### Inputs
**`text-input`** — `raised`, `ink`, 1px `hairline-strong`, radius `sm`, height 36, padding 0 10px, `ui-regular`. Placeholder `muted`. Focus: ring (see Accessibility); border unchanged. Error: border `error` + `caption` `error` line below. Labels above at `caption` `muted`, 4px gap. Login page uses a single 360px column of these on `canvas`; no card around it.

### Dialogs
**`dialog`** — `raised`, radius `md`, padding 20px, `max-width: 440px`, Level 3 shadow, on `backdrop`. Title `display-sm`, body `ui-regular` `body`, footer buttons right-aligned (`button-secondary` cancel, `button-primary` or `button-danger` confirm). Focus trapped, `Esc` cancels, initial focus on the least destructive button. Used for: delete task, close project, disconnect. Never for approvals (those are cards in the transcript).

### Notices
**`notice-line`** — a single 32px row inside the transcript at `caption`, glyph + text, no fill: `info` for "Model changed to gpt-5", `warning` for "Interrupted by disconnect", `error` for "Provider exited (code 1)". Banner variant at the top of the main pane only for connection loss: `warning-wash` fill, `warning` text, 32px, no radius.

## Motion

| Token | Duration | Easing | Use |
|---|---|---|---|
| `fast` | 100ms | `cubic-bezier(0.2, 0, 0, 1)` | Hover/press surface steps, chip changes |
| `base` | 160ms | same | Disclosure open/close (height + opacity), popover in, drawer slide |
| `slow` | 240ms | same | Changes sheet slide, dialog in |

- Streaming text appends without animation. A 1ch-wide caret (`accent`, 1px wide bar) blinks at 1s at the end of the streaming node; removed when the turn ends.
- "Thinking…" label shimmer: a `muted`→`body`→`muted` text-colour sweep over 1.6s, on the label only.
- Working dot pulse: opacity 1→0.4→1 over 1.6s.
- Spinners rotate at 0.9s linear.
- New transcript items do not slide in; they appear. The transcript auto-follows the bottom only if the user was already within 80px of it; otherwise a `button-secondary` "↓ New output" pill-less 28px button appears above the composer.
- Reduced motion: `@media (prefers-reduced-motion: reduce)` sets every transition and animation to `none`; the caret becomes a static bar; the working dot becomes a static `accent` ring; spinners become a static three-quarter arc; disclosures snap.

## Accessibility

- **Focus:** `:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }` everywhere; inside the composer the ring is on the textarea; on rail rows the ring sits inside the row (`outline-offset: -2px`) so it is not clipped by the scroll container. Never remove outlines without a visible substitute.
- **Contrast:** see the table; new text colours must pass on every surface they can land on. `faint` is glyph-only.
- **Keyboard:** `⌘/Ctrl+K` opens the project/task switcher (a `popover` with a search input); `Esc` closes any popover, then stops the current task if nothing is open (with confirmation chip "Press Esc again to stop"); `Enter` sends, `Shift+Enter` newlines, `Ctrl+Enter` steers; `j`/`k` are not used (the transcript is prose). Rail is a tree (`role="tree"`, `aria-expanded` on projects). Approval card shortcuts as specified above.
- **Live regions:** the ledger summary is `aria-live="polite"`; the transcript is `role="log"`; approval and question cards use `role="group"` with `aria-labelledby` and move focus to themselves when they appear (once, not on every re-render).
- **Targets:** ≥44×44 effective on touch (row padding counts); icon buttons are 28px visual inside a 36px hit area on desktop and 44px on phone.
- **Colour is never the only signal:** every state has a glyph and a word; add/del rows carry `+`/`-`.
- **Zoom:** layout holds at 200% browser zoom (the rail collapses to the drawer at that point because the effective width drops under 960px).
- **Language:** set `lang` on the document; code blocks get `translate="no"`.

## Do / Don't

### Do
- Step surfaces by one shade to show depth; add a hairline only to floating or interactive surfaces.
- Keep the assistant flat and give the user the bubble.
- Reserve `attention` for states that need a human; use it on a dot, a chip, a card rule, a count. Never as a button fill.
- Set every identifier (model, tool, path, command, diff) in JetBrains Mono at 12–13px.
- Truncate rail and deck rows; wrap nothing there.
- Collapse thinking by default, collapse the ledger once everything is done, collapse subagents once they complete.
- Let the transcript, assistant content and composer fill the available main-pane width between the existing gutters.
- Specify hover as a surface step, press as surface step + `ink`.

### Don't
- Don't draw a border inside a border. A card's children use indents and sunken wells; never a side stripe.
- Don't add a coloured primary button; the primary is ink.
- Don't use pills for buttons; the only 14px radius is the user bubble.
- Don't use a serif, a display weight below 400 or above 600, or scaled markdown headings in chat.
- Don't fill chips except the attention chip.
- Don't show heuristics or previews in rows (no "probably done", no first-line peeks); rows show server state.
- Don't animate list rows in or out; don't shimmer skeletons.
- Don't put the model in the header; it belongs to the composer, where it is changed.
- Don't cap the chat column at desktop widths or add unrelated side widgets.
- Don't use inline `style` props or `<style>`/`<script>` tags in the app (see CSP).

## CSP constraints

The server sends `default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; font-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'`. Consequences for implementation:

- **No inline styles.** React `style={{…}}` writes a `style` attribute and is blocked by `style-src 'self'`. Every visual variant is a class or a `data-` attribute matched in `styles.css`. Dynamic dimensions (e.g. textarea auto-grow, sheet widths) are done with `rows`, CSS `field-sizing: content` where supported, or a small set of stepped classes, never per-element inline values.
- **No inline scripts, no `eval`.** Vite's production build emits external chunks only; do not enable plugins that inline module preloads or scripts.
- **Fonts from `'self'`.** Import `@fontsource-variable/inter` and `@fontsource-variable/jetbrains-mono` in `main.tsx`; Vite hashes the woff2 files into `/assets/`, served by the same origin. No `<link>` to a fonts CDN, no `@import url(https://…)`.
- **Icons** are inline SVG elements in JSX (markup, not style) or a same-origin sprite; no icon fonts from third parties.
- **Images** are same-origin or `data:`; avatars for providers are inline SVG.
- **Syntax highlighting** (if added later) must emit classes, not inline colours.

## Responsive summary

| Name | Width | Key changes |
|---|---|---|
| Phone | ≤480px | Rail → drawer; column 100% with 12px gutters; user bubble 88%; composer full width with safe-area; chat body 16px; buttons 40px; Changes → bottom sheet; approval buttons stack |
| Narrow | 481–959px | Drawer; column fills available width with 16px gutters; Changes overlay |
| Laptop | 960–1279px | Fixed rail; column fills available width with 24px gutters; Changes overlay 440px |
| Desktop | 1280–1799px | Fixed rail; column fills remaining width with 24px gutters; Changes inline 440px |
| Wide | ≥1800px | Column fills available width with 24px gutters; no fixed width cap |

## Iteration guide

1. Add a token before you add a hex. Every colour in `styles.css` is a `var(--…)` named here.
2. Add a component here before adding a class there; keep the class name equal to the component key.
3. When a new state or chip is needed, extend the State marks table; do not invent a fifth semantic colour.
4. Run the contrast script (`refs/contrast.py` in the design bundle, or any WCAG calculator) for every new text/surface pair before merging.
5. Screenshot at 420×860, 1440×900 and 1920×1080 for any change to layout tokens.
