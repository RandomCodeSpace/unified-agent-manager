---
version: 3
name: uam-web-workbench
description: Design system for `uam web`, the browser workbench for running coding agents. A dense, calm, borderless tool used for hours at a time, on a laptop over SSH and at 420px on a phone. One warm, mid-light canvas (a single theme, no dark scheme), one ink ladder for every piece of chrome, hairlines instead of boxes, a single restrained accent (blue) for focus, motion and links, and one warm "needs you" colour (orange) that is the only thing allowed to shout. Inter for UI and chat, JetBrains Mono for identifiers, code and diffs. No serif, no pills, no gradients. Everything is a row or a column of text; surfaces step by one shade, never by borders inside borders. Built with Tailwind v4 (this file's tokens are the `@theme`) and Base UI primitives (menus, context menus, dialogs, tooltips, selects) under a strict CSP.

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

motion:
  fast: 100ms
  base: 160ms
  slow: 240ms
  easing: "cubic-bezier(0.2, 0, 0, 1)"
  pulse: 1.6s
  spin: 0.9s

layout:
  sidebar-width: 264px
  drawer-width: 300px
  header-height: 44px
  chat-column-width: 100%
  chat-gutter: 24px
  chat-gutter-phone: 12px
  panel-default-width: 440px
  panel-min-width: 320px
  panel-max-width: "min(880px, viewport − 264px − 480px)"
  bp-phone: 480px
  bp-sidebar-collapse: 960px
  bp-panels-inline: 1280px
  bp-wide: 1800px

components:
  shell:
    backgroundColor: "{colors.canvas}"
    textColor: "{colors.body}"
    typography: "{typography.ui-regular}"
  sidebar:
    backgroundColor: "{colors.rail}"
    textColor: "{colors.body}"
    typography: "{typography.ui-regular}"
    width: "{layout.sidebar-width}"
    padding: "0 8px"
  sidebar-header:
    height: "{layout.header-height}"
    typography: "{typography.title}"
    textColor: "{colors.ink}"
  project-row:
    textColor: "{colors.ink}"
    typography: "{typography.ui}"
    rounded: "{rounded.sm}"
    padding: "0 4px"
    height: 32px
  project-row-branch:
    textColor: "{colors.muted}"
    typography: "{typography.keycap}"
  task-row:
    textColor: "{colors.body}"
    typography: "{typography.ui-regular}"
    rounded: "{rounded.sm}"
    padding: "0 8px"
    height: 32px
  task-row-selected:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.ink}"
    typography: "{typography.ui}"
    shadow: "0 1px 2px rgba(28, 27, 24, 0.05)"
  task-row-meta:
    typography: "{typography.caption}"
    textColor: "{colors.muted}"
  shelf-header:
    textColor: "{colors.muted}"
    typography: "{typography.caption}"
    height: 28px
  main-header:
    backgroundColor: "{colors.canvas}"
    textColor: "{colors.ink}"
    typography: "{typography.display-sm}"
    borderColor: "{colors.hairline}"
    height: "{layout.header-height}"
    padding: "0 8px 0 12px"
  main-header-meta:
    textColor: "{colors.muted}"
    typography: "{typography.keycap}"
    height: 28px
  transcript:
    backgroundColor: "{colors.canvas}"
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
    shadow: "0 1px 2px rgba(28, 27, 24, 0.05)"
  message-assistant:
    backgroundColor: transparent
    textColor: "{colors.body}"
    typography: "{typography.chat-body}"
    padding: "0"
  thinking-row:
    textColor: "{colors.muted}"
    typography: "{typography.ui-regular}"
    height: 24px
  thinking-body:
    backgroundColor: "{colors.sunken}"
    textColor: "{colors.muted}"
    typography: "{typography.ui-regular}"
    rounded: "{rounded.sm}"
    padding: "8px 12px"
  ledger-row:
    textColor: "{colors.muted}"
    typography: "{typography.code-sm}"
    height: 24px
    padding: "0 32px 0 4px"
  subagent-row:
    backgroundColor: "rgba(222, 220, 214, 0.6)"
    textColor: "{colors.ink}"
    typography: "{typography.ui-regular}"
    rounded: "{rounded.sm}"
    padding: "6px 6px 6px 12px"
  interaction-card:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.ink}"
    typography: "{typography.ui-regular}"
    borderColor: "{colors.hairline}"
    accentColor: "{colors.attention}"
    rounded: "{rounded.md}"
    padding: "12px 16px"
  composer:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.ink}"
    typography: "{typography.chat-body}"
    borderColor: "{colors.hairline}"
    rounded: "{rounded.md}"
    padding: "12px 14px 8px"
    minHeight: 96px
  composer-picker:
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
  code-block:
    backgroundColor: "{colors.code-bg}"
    textColor: "{colors.ink}"
    typography: "{typography.code}"
    borderColor: "{colors.hairline}"
    rounded: "{rounded.md}"
    padding: "10px 12px"
  code-inline:
    backgroundColor: "{colors.sunken}"
    textColor: "{colors.ink}"
    typography: "{typography.code}"
    rounded: "{rounded.xs}"
    padding: "1px 4px"
  side-panel:
    backgroundColor: "{colors.canvas}"
    textColor: "{colors.body}"
    typography: "{typography.ui-regular}"
    borderColor: "{colors.hairline}"
    width: "{layout.panel-default-width}"
  panel-handle:
    width: 8px
    lineColor: "{colors.hairline}"
    activeColor: "{colors.accent}"
  diff-line-add:
    backgroundColor: "{colors.diff-add-bg}"
    textColor: "{colors.diff-add-text}"
    typography: "{typography.code-sm}"
  diff-line-del:
    backgroundColor: "{colors.diff-del-bg}"
    textColor: "{colors.diff-del-text}"
    typography: "{typography.code-sm}"
  menu:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.ink}"
    typography: "{typography.ui-regular}"
    borderColor: "{colors.hairline-strong}"
    rounded: "{rounded.md}"
    padding: "4px"
    shadow: "0 4px 16px rgba(28, 27, 24, 0.12), 0 1px 2px rgba(28, 27, 24, 0.08)"
  menu-item:
    typography: "{typography.ui-regular}"
    rounded: "{rounded.sm}"
    padding: "4px 8px"
    height: 30px
  tooltip:
    backgroundColor: "{colors.ink}"
    textColor: "{colors.on-primary}"
    typography: "{typography.caption}"
    rounded: "{rounded.sm}"
    padding: "4px 8px"
  dialog:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.body}"
    typography: "{typography.ui-regular}"
    rounded: "{rounded.md}"
    padding: "20px"
    maxWidth: 440px
    maxWidthBrowsing: 560px
    shadow: "0 16px 48px rgba(28, 27, 24, 0.18), 0 2px 6px rgba(28, 27, 24, 0.08)"
  folder-picker-well:
    backgroundColor: "{colors.canvas}"
    rounded: "{rounded.sm}"
    height: "min(320px, 40dvh)"
  folder-picker-row:
    textColor: "{colors.body}"
    typography: "{typography.code}"
    rounded: "{rounded.sm}"
    padding: "0 4px 0 8px"
    height: 32px
  folder-picker-row-selected:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.ink}"
    shadow: "0 1px 2px rgba(28, 27, 24, 0.05)"
---

## Overview

`uam web` is a workbench, not a magazine: a person sits in this screen for hours and needs a launcher. One ink ladder, one surface ladder, rows instead of cards, and colour that only appears when the agent is moving, broken, or needs a human.

Version 3 describes the shipped system after the 2026-09-24 revamp (issue #173): the sidebar is the only place Tasks are listed, in the manner of T3 Code's sidebar; the composer settings are one compact toolbar; every surface is built from Tailwind utilities on the tokens above, with Base UI primitives for menus, context menus, dialogs, tooltips and selects. There is one mid-light theme and no theme toggle.

**Key characteristics**
- Two ladders carry the whole UI: surfaces (`rail` → `canvas` → `surface` → `raised`, plus `sunken` for wells) and text (`ink` → `body` → `muted` → `faint`). Chrome never uses a colour outside these ladders except the four semantic tones, the accent and the attention colour.
- Borderless by default. A component gets a hairline only when it floats or is interactive (composer, cards, menus, dialogs, inputs). Nothing inside a card gets another border; nested structure uses a `sunken` well or an indent.
- Colour is reserved for act-now (`attention`), in-motion (`accent`) and broken (`error`, `warning`). Ready is unlabelled: a finished row shows its relative time, not a colour.
- The assistant does not get a bubble. The user does.
- Density is a row: 32px sidebar rows, 28px shelf headers, 24px ledger rows, 44px headers, 28px composer pickers. A coarse pointer grows every target to 44px by padding, not by scaling type.
- Motion is functional, short and consistent (see Motion); every animation is off under `prefers-reduced-motion`.

## Principles

1. **Stable data only.** Rows show state the server knows (task state, stage, time, branch, model), never heuristics or previews.
2. **Typographic hierarchy before boxes.** Weight (400/500/600), then colour step (`ink`/`body`/`muted`), then indent, then a hairline. A bordered box is the last resort.
3. **One accent, one alarm.** `accent` is the only chromatic chrome colour. `attention` is the only warm one and the only one that may fill (a chip wash).
4. **Surfaces step, they do not stack.** Depth = one shade lighter. Shadows are for things that float over content (menus, tooltips, dialogs, the drawer, an inline panel's selected row).
5. **Code is code.** Anything typed, run, edited or diffed, and every identifier (model, branch, path), is JetBrains Mono at 11–13px. Prose is Inter.
6. **Every action has three doors.** Anything in a context menu is also on a hover "…" button (always visible on touch) and reachable by keyboard.
7. **CSP-clean.** No `<style>` elements, no inline `style` markup, fonts from `'self'` (see CSP constraints).

## Colors

### Theme
One theme. The owner decided on 2026-09-24 that `uam web` ships a single mid-light scheme: a dimmed-paper canvas (L* ≈ 91), clearly softer than white and clearly lighter than a dark theme, so the screen reads the same on every machine and nothing flips with the OS setting. There is no theme toggle, no stored preference and no `prefers-color-scheme` switch; `color-scheme: light` keeps native controls in step.

All greys are warm near-neutrals. Nothing reaches pure white: `raised` (#f3f2ee) is the lightest surface. The sidebar sits on `rail`, one step darker than the canvas; raised surfaces are one step lighter; wells one step darker.

In Tailwind the tokens are `--color-<name>` in `web/src/index.css` (`@theme`), used as `bg-raised`, `text-muted`, `border-hairline` and so on. Tailwind's own palette is removed (`--color-*: initial`), so nothing outside this table can be written by accident.

### Surfaces
| Role | Value | Use |
|---|---|---|
| `rail` | #dedcd6 | Sidebar and drawer floor |
| `canvas` | #e8e6e1 | Main pane: header, transcript, panels, login, empty states |
| `surface` | #eeece7 | Hover step on raised controls; read-only composer |
| `raised` | #f3f2ee | Composer, interaction cards, menus, dialogs, inputs, selected sidebar row |
| `sunken` | #dedcd6 | Wells: code blocks, thinking body, subagent rows, inline code, meter track |
| `bubble-user` | #f3f2ee | User message bubble |
| `code-bg` | #dedcd6 | Alias of `sunken` for code, kept separate so code can be retuned alone |
| `selection` | #cfdaf3 | `::selection` |
| `backdrop` | rgba(28,27,24,.35) | Behind dialogs, the drawer and overlay panels |

### Text
| Role | Value | Use |
|---|---|---|
| `ink` | #1c1b18 | Titles, selected and needs-you rows, user bubble text, code, project names |
| `body` | #3d3b36 | Assistant prose, default UI text, rows at rest |
| `muted` | #5c5953 | Meta, captions, times, ledger rows, shelf headers, placeholders, thinking text |
| `faint` | #7d7a72 | Glyphs only: chevrons, idle and closed marks, diff line numbers, version (≥3:1) |

### Lines
| Role | Value | Use |
|---|---|---|
| `hairline` | #d3d1ca | Separators, header rule, card edge, code block edge, shelf rule, panel edge |
| `hairline-strong` | #bfbdb5 | Input edge, secondary button edge, menu and dialog edge |

### Action and signal
| Role | Value | Use |
|---|---|---|
| `primary` / `on-primary` | #1c1b18 / #f3f2ee | Primary button and tooltip fill. There is no coloured primary button. |
| `accent` / `on-accent` | #2a55bd / #f3f2ee | Links, focus ring, working dot and word, selected radio check, panel handle when active, caret |
| `accent-wash` | #dbe3f5 | Locate flash in the transcript |
| `attention` / `attention-wash` | #9a400a / #f4e1cf | "Needs you": permission and question states, the Approval/Input row words, the chip on a pending card, the yolo mode icon |
| `success` / `success-wash` | #196a41 / #d8e8dd | Completed mark and "Done" row word, connected dot, diff `+` counts |
| `warning` / `warning-wash` | #7a5500 / #efe3c3 | Interrupted state, reconnecting banner, context meter at ≥90% |
| `error` / `error-wash` | #b3261e / #f4d9d5 | Failed state, Deny and Delete, error lines, offline banner, diff `−` counts |

### Diff
| Role | Value |
|---|---|
| `diff-add-bg` / `diff-add-text` | #d3e6da / #14532d |
| `diff-del-bg` / `diff-del-text` | #efd6d2 / #7f1d1d |

### Contrast (WCAG 2.x, computed)
Every text colour clears AA (≥4.5:1) on every ground it can sit on; `faint` is glyph-only and held at ≥3:1. The values are unchanged from version 2 (the table there still applies); the tightest pairs are `muted` on `hairline` (4.57) and the semantic tones on `rail`/`sunken` (4.8–4.9). Do not lighten them and do not darken the grounds.

## Typography

- **UI and chat:** Inter Variable (`@fontsource-variable/inter`, SIL OFL 1.1), family `'Inter Variable'`.
- **Code, identifiers, diffs:** JetBrains Mono Variable (`@fontsource-variable/jetbrains-mono`, SIL OFL 1.1), family `'JetBrains Mono Variable'`, ligatures off.
- Both are imported once in `web/src/main.tsx` and served from the same origin (`font-src 'self'`). No CDN.

### Scale
The scale is the `--text-*` theme in `index.css`; Tailwind's default sizes are removed, so the only sizes are these:

| Token (utility) | Size | Weight | Line | Use |
|---|---|---|---|---|
| `display-md` | 20px | 600 | 1.3 | Empty-state and login headings |
| `display-sm` | 17px | 600 | 1.35 | Task title in the header, dialog titles |
| `title` | 14px | 600 | 1.4 | Card titles, panel titles, dialog section headings, the wordmark |
| `ui` (+ `font-medium`) | 13px | 500 | 1.4 | Buttons, selected and needs-you rows, project names, pickers |
| `ui` | 13px | 400 | 1.4 | Rows at rest, menu items, meta lines, form controls |
| `chat` / `chat-lg` | 15px / 16px | 400 | 1.6 | User and assistant prose; 16px at ≤480px (also prevents iOS input zoom) |
| `caption` | 12px | 400 | 1.4 | Times, row status words, shelf headers, counts, chips, notes |
| `code` / `code-sm` | 13px / 12px | 400 | 1.55 / 1.5 | Code blocks / ledger rows, file paths, diff body, picker values |
| `keycap` (11px mono) | 11px | 400–500 | 1 | Branch names, model names in headers, version |
| `eyebrow` | 11px | 600 | 1.2 | Reserved; no eyebrows above headings in the shipped UI |

Rules: weight is ternary (400/500/600); only `display-*` carry negative tracking; counts, durations and times use `tabular-nums`; markdown headings inside chat do not scale up; the transcript fills the main pane between the gutters, with no fixed column cap.

## Layout

### Shell
Full viewport, no page scroll: `grid h-dvh grid-cols-[264px_minmax(0,1fr)]` from 960px up. The sidebar is a **fixed 264px** and is not resizable. Below 960px the sidebar is a 300px drawer (a Base UI dialog sliding from the left, with backdrop) opened from the header's menu button. The main pane is a column: header (44px, hairline below) → optional meta line (28px) → transcript (flex 1, `overflow-y: auto`, `overscroll-behavior: contain`) → composer (pinned).

### Side panels (Changes, Subagents)
From 1280px a panel sits inline to the right of the column and is **resizable**: an 8px handle on its inner edge (`role="separator"`, `aria-orientation="vertical"`, `aria-valuenow/min/max`), drag with pointer capture, arrow keys step 16px (64 with Shift), Home/End go to the limits, Enter or double-click resets to the default. Width is clamped to 320px … min(880px, viewport − 264 − 480) so the chat column keeps at least 480px, and saved per panel in `localStorage` (`uam.panel.changes`, `uam.panel.subagents`). The width is applied as the `--panel-w` custom property through CSSOM during the drag, so nothing re-renders per pointer move. Between 960 and 1279px a panel is a 440px overlay from the right; below 960px it is a full-screen sheet. Overlays and sheets do not resize. One panel is open at a time.

### Breakpoints
| Width | Sidebar | Column | Panels |
|---|---|---|---|
| ≤480 (phone) | Drawer | 12px gutters, chat 16px, controls 44px | Full-screen sheet |
| 481–959 | Drawer | 16px gutters | Full-screen sheet |
| 960–1279 | Fixed 264px | 24px gutters | Overlay 440px |
| ≥1280 | Fixed 264px | 24px gutters, fills the rest | Inline, resizable |

### Spacing and density
4px base. Sidebar rows 32px (44 on coarse pointers), shelf headers 28px, project rows 32px (40 with a branch line), ledger and disclosure rows 24px, headers 44px, buttons 32 (28 small, 36 large), inputs 36, chips 20, pickers 28, menu items 30 (44 on coarse pointers).

## Elevation & Depth

| Level | Treatment | Shadow | Use |
|---|---|---|---|
| 0 Flat | Surface step only | none | Sidebar on `rail`, everything else on `canvas` |
| 1 Raised | `raised` fill + 1px `hairline` | `raised`: `0 1px 2px .05` | Composer, interaction cards, user bubble, selected sidebar row |
| 2 Floating | `raised` + 1px `hairline-strong` | `float`: `0 4px 16px .12, 0 1px 2px .08` | Menus, context menus, selects, tooltips (ink fill) |
| 3 Modal | `raised` + `backdrop` | `modal`: `0 16px 48px .18, 0 2px 6px .08` | Dialogs, drawer, overlay panels |

## Shapes

`xs` 4px chips, inline code, inputs in rows; `sm` 6px buttons, rows, menu items, inputs; `md` 10px composer, code blocks, cards, menus, dialogs; `lg` 14px the user bubble only; `full` dots and spinners. No pills.

## Components

Every component is Tailwind utilities on the tokens, in `web/src/components/`; the Base UI wrappers live in `web/src/components/ui/` (`button`, `menu` with `ContextMenu`, `dialog` with `AlertDialog` and `Sheet`, `tooltip`, `select`). States are Base UI data attributes (`data-open`, `data-highlighted`, `data-disabled`, `data-starting-style`, `data-ending-style`) or ARIA (`aria-current`, `aria-pressed`, `aria-expanded`). Hover is one surface step; press adds `ink`.

### Sidebar
Sits on `rail`, 264px, no border to the main pane (the `rail` → `canvas` step is the seam).

**Header (44px).** The brand at left: a 20px two-pane mark in `ink` (inline SVG, no asset) and the wordmark `uam` at `title` weight; clicking it goes home. At right, three 28px icon buttons: the connection dot (`success` steady when connected; `warning` pulsing while connecting or reconnecting; `error` when offline; tooltip and `role="status"` text carry the words), **New task** (`SquarePen`, targets the open Task's project or the one with the newest activity, named in its tooltip) and **Add project** (`FolderPlus`).

**Project row (32px; 40px with a branch).** Chevron (`faint`, rotates 90° when open) → name (`ui` 500 `ink`, truncates) with the git branch beneath it when the Project reports one (`GitBranch` glyph + `keycap` mono `muted`, truncating). When collapsed, the count of active Tasks sits at the right in `caption` `faint`, and if a hidden Task needs you the chevron becomes an `attention` dot (hover shows the chevron again). On hover, focus-within or a coarse pointer the row reveals **New task** and a **"…"** project menu; right-click opens the same menu (New task · Edit project · Remove project). Left/Right arrows collapse and expand; the state persists.

**Task row (32px).** `grid 16px | 1fr | auto`: the state mark, the title (`ui` `body`; `ink` 500 when selected, needs-you or unread; `muted` when settled or archived), and a right slot. The right slot shows a **status word** for act-now, in-motion, broken and unread rows, in that state's tone (Approval, Input, Working, Starting, Failed, Paused, Done, Stopped) and the **relative time** otherwise (`caption` `tabular-nums`). On hover or focus the slot cross-fades to a **"…"** button (Base UI menu, `side="right"`); on a coarse pointer the button is always visible and the meta text stays to its left. Right-click opens the same items as a context menu: Rename · Close conversation (open Tasks) · Settle or Reopen · Archive · Delete (archived only, destructive). A busy Task's Settle and Archive are disabled with the reason under the item. Selected: `raised` fill, `ink`, a 1px shadow. Double-click or F2 starts an **inline rename**: the title becomes an input with the text selected; Enter or blur saves, Esc cancels, an empty value clears the name so the provider's title shows again.

**Shelves.** Under a Project's active Tasks come **Settled** and **Archived** shelf headers (28px): the word and count in `caption` `muted`, a hairline, a chevron. Collapsed by default; the state persists per shelf (`uam.shelves`). A collapsed shelf or group still shows the open Task pinned beneath its header. Archived Tasks open like any other, read-only.

**Keyboard.** Arrow Up/Down move between rows, Home/End jump, Left/Right collapse or expand a project, Enter opens, F2 renames, Shift+F10 or the context-menu key opens the row menu. Focus rings sit inside rows (`-outline-offset-2`).

**Footer.** The service version in `keycap` `faint` at left and **Log out** at right when a token is required. When the stream is not connected a banner row appears above it: `warning-wash` (reconnecting) or `error-wash` (offline) with the pulsing dot and the full sentence.

### Empty and loading states
With no Task open the main pane centres the mark, "Ready when you are." (`display-md`), one sentence, and two buttons: **New task in ‹project›** (primary) and **Add project** (ghost). Without a Project: "Add a project to begin." and a primary **Add project**. While a Task's detail loads, the header keeps its height with a quiet placeholder bar and three placeholder lines pulse below (static under reduced motion). A deleted Task explains itself and offers **Back to projects**.

### Task header
44px, `canvas`, hairline below. Left: the drawer button (narrow only), the title (`display-sm` `ink`, truncating; double-click, the hover pencil, or Rename in the menu edits it in place with the same rules as the row), then the state chip (`StateMark` with the word; `Archived`/`Settled` as an outlined chip when read-only), and a spinner while a lifecycle request is in flight. Right: **Subagents** (`Bot` + count, spinner while any run; label hidden below 481px), **Changes** (`FileDiff` + count), and the **"…"** menu with the same items as the row. A 28px **meta line** below carries the git branch (`GitBranch` + `keycap` mono, truncating to 40%), the model of the latest turn (`keycap` mono) and, at the right, the **context meter**: a 64px `sunken` track with an `accent` fill (`warning` at ≥90%) and `used / limit` in compact numerals, `role="meter"` with the full text in `aria-valuetext` and a tooltip. Nothing wraps at 420px: labels drop, the title truncates, the meta line truncates.

### Transcript
User turns are bubbles on the right (`bubble-user`, `lg` radius, 78%/560px max, 88% on phone). Assistant prose is the page. The first assistant turn carries `provider · model` in `caption`. Every message has a hover copy button and a right-click menu (Copy message). Items that arrive after the transcript mounted rise in (`rise`, 240ms); items present at mount appear at once; streaming text appends with no animation.

- **Thinking row (24px):** chevron + "Thinking…" (shimmering while streaming, with the latest line as a `faint` preview) or "Thought for 12s"; the body is a `sunken` well at `ui` `muted`, max 320px, with Copy thinking.
- **Ledger (24px rows):** "3 tool calls · 1 running" folds the run; each tool row shows a drawn mark (spinner, check, cross, dash), the tool name at 500 and its argument in `code-sm`. Hover shows "…"; right-click and the menu offer Expand/Collapse, Copy command, Copy output. Expanded, input and output are code blocks labelled `input` and `output`.
- **Code block:** `code-bg`, hairline, `md` radius, a 24px header with the language (or `code`) and a copy button; right-click offers Copy code. Max height 480px with internal scroll; never wraps.
- **Subagent row:** a `sunken` row with the `Bot` glyph, the name, the status chip, model, duration and **Open**; right-click offers Open and Copy agent ID. Subagent output never renders in the main column.
- **Interaction card (pending):** `raised`, hairline, `md`; a `chip-attention` with the `ShieldQuestion`/`MessageCircleQuestion` glyph heads it; the request in a `sunken` mono well; buttons right-aligned (Deny `danger`, Allow for task `secondary`, Allow once `primary`; stacked full-width on phone). Once decided it collapses to one `caption` line so the transcript reads on.

### Composer
`raised`, hairline, `md`, pinned under the transcript with the column gutters; focus-within lifts it one shadow step. The textarea (`chat`, `field-sizing: content`, 56px to 40dvh) sits above a single toolbar row:

- **Pickers** at left, four 28px ghost buttons with a glyph, the current value and a small chevron: Model (`Cpu`, value in `code-sm`), Effort (`Gauge`), Context size (`Layers`), Mode (`Shield`; `ShieldOff` in `attention` for yolo). Each opens a Base UI radio menu above it with the choices, a check on the current one, and descriptions where they help (token counts, "may cost more", what Safe and Yolo do). When a value cannot change (a turn is running, the model has no effort levels, the provider has no context sizes, the Task is read-only) the picker stays visible, dimmed, and says why on hover, focus and in its accessible name. Context size appears only where the capability allows.
- **Actions** at right: while a turn runs, **Stop** (secondary, square glyph), **Steer** (secondary, `Zap`) and **Queue** (primary square, `ListPlus`); otherwise **Send** (primary square, `ArrowUp`). Tooltips carry the shortcuts: Enter sends or queues, Ctrl+Enter steers, Shift+Enter adds a line. Read-only Tasks show no send controls.
- Notices (read-only, uncertain, rejected, errors) sit in a hairline-separated strip above the textarea; the queue is a disclosure strip with numbered rows, a per-row cancel, Resume and Clear. "Latest turn ran on ‹model›" appears below the toolbar only when routing differed.
- **Inline pickers:** typing `/` as the first character or `@` at the start or after whitespace opens a level-2 listbox above the textarea, full width on phone and 440px from `sm` up, max height min(300px, 40dvh). Rows are 30px (44px on touch); `/` groups rows under Commands and Skills, and `@` lists files and folders in `code-sm`. Focus never leaves the textarea, which drives the list through `aria-activedescendant`: Up and Down move, Enter or Tab picks, Escape dismisses. Loading, empty, and reason lines (for example, commands before the conversation opens) sit inside the popover.
- **Chips row:** picked `@` files and uploads sit in a wrapping row above the textarea. An upload chip is a 44px `sunken` well holding a 36px thumbnail (or the kind's glyph), the name, and its state: `Uploading… 42%` with a 2px `accent` bar along the bottom edge, size and kind when done, or the refusal in `error` on an `error-wash`. A file-reference chip is a smaller mono chip with `@`. Each chip has a remove button. Queued prompts repeat these as 20px caption chips.
- **Attach:** a subtle `Paperclip` icon button leads the toolbar. Its tooltip gives the model's media gate and the limits (images 3 MiB, PDF 10 MiB, text 256 KiB, 5 per message). It never disables for the model, since text is always allowed: the gate narrows the file picker and the tooltip names it. Once a message holds the most uploads it can carry, the button stays visible, dimmed, `aria-disabled`, and says why. Read-only Tasks show no composer controls, so no Attach button. Paste and drag-and-drop also work; while files are dragged over the composer, a `raised` overlay with an inset dashed `accent` outline shows "Drop to attach" and the same note (`fade-in`).
- **Transcript attachments:** a user turn's images render as thumbnails up to 160px high; hover lifts the shadow and scales the image to 1.02. Clicking one opens a lightbox dialog (max 92vw by 1100px, image max 72dvh) with **Open original**. Other files are 32px chips with glyph, name, and size that open the stored copy; without a stored copy the chip is inert.
- Phone: Effort, Context size and Mode drop their value text and keep the glyph (the tooltip, accessible name and menu still carry the value), so the toolbar stays one row; every control is 44px; the textarea is 16px.

### Menus, context menus, tooltips, selects, dialogs
All Base UI. Menus: `raised`, `hairline-strong`, `md`, 4px padding, `float` shadow, 30px items (44 on coarse pointers), highlighted item one surface step, destructive item in `error` with `error-wash` highlight, disabled items dimmed with their reason in `caption` beneath. They scale from 0.97 and fade over 100ms from the transform origin. Context menus open at the pointer (long-press on touch) and hold exactly the items of the matching "…" button. An item's action runs once the menu has finished closing, and an action that takes focus itself (inline rename) tells the menu to leave focus alone (`takesFocus`), so the menu's focus return never undoes it. Tooltips are `ink` on `on-primary`, 400ms delay, no delay while another is open; they are hints for sighted users and never the only name of a control. Selects (project dialogs) open below their trigger with a check on the chosen row. Dialogs: `raised`, `md`, 20px padding, 440px max (560px, `--spacing-sheet-wide`, while Add project browses folders), `modal` shadow on the backdrop; a close button in the title row; on phone they become a bottom sheet with full-width buttons. Confirmations (`AlertDialog`) focus **Cancel** first; Archive and Delete always confirm.

### Folder picker
The Add project dialog's path field keeps a **Browse** button (`FolderOpen`, secondary, `aria-expanded`) beside it. Browse opens the picker inline under the field (`fade-in`), the dialog widens to 560px over `slow` and settles back once a folder is chosen. Nothing floats: the picker is a column of rows in the dialog (`components/FolderPicker.tsx`).

- **Breadcrumb.** The folder being shown as `code-sm` mono segments, root first, each a 24px button (`muted`; the last `ink` with `aria-current="location"`), `/` separators in `faint`, wrapping when deep. At its right a **Show hidden** toggle (`EyeOff`/`Eye`, ghost, `aria-pressed`); hidden folders are filtered in the browser.
- **The well.** A `canvas` well (`sm`), fixed at min(320px, 40dvh) so moving between folders never changes the dialog's height; on phone it is 55dvh and fills the sheet. Inside, a `role="listbox"` of folders: a `Folder` glyph, the name in `code` mono, and marks in `caption` `muted` for a git repository (`GitBranch` + "git") and a symbolic link (`Link` + "link"); hidden names are `muted`. Rows are 32px (44 on coarse pointers); hover is one step up (`surface`), the selected row is `raised` with `ink` and the 1px shadow, the same as a selected sidebar row. A `ChevronRight` at the row's end opens the folder. "Showing the first 1,000" closes a truncated list.
- **Selection is the target.** A click selects, a double-click or the chevron opens. **Use this folder** takes the selected row, else the folder being shown; the path it will take sits beside the button in `code-sm` mono, truncated at its start so the folder's own name stays visible. Navigating clears the selection.
- **Footer.** **Up** (`FolderUp`, disabled at `/`) and **New folder** (`FolderPlus`) at left, **Use this folder** (secondary; the dialog's primary stays **Add project**) at right; full-width buttons on phone. New folder adds a row at the top of the well with a mono name field and Create/Cancel icon buttons: Enter creates and selects the new folder (a dot-name turns Show hidden on), Escape cancels, errors sit under the row in `caption` `error`.
- **Keyboard.** Focus rests on the listbox (`aria-activedescendant`): Up and Down move, Home and End jump, Enter opens the selected folder, Backspace goes up, typing jumps to a name, Ctrl/Cmd+Enter anywhere in the picker means Use this folder. Escape cancels the New folder row first, then closes the picker; the dialog stays open.
- **Quiet states.** Loading shows three pulsing placeholders only before the first listing; afterwards the previous rows dim while the next folder loads. An empty folder says "No folders here" (or how many are hidden). A folder that cannot be listed keeps its breadcrumb and Up and says why in `caption` `muted`: "You don't have access to this folder" for 403, "This folder does not exist" for 404. A path in the field that is not a folder starts the picker at home instead.

### Buttons and inputs
Primary is ink; secondary is `raised` with a `hairline-strong` edge; ghost has no fill; danger is `error` text with `error-wash` hover; icon buttons are 28px (32 in headers) `muted` glyphs. Disabled is 45% opacity. Inputs are 36px `raised` with a `hairline-strong` edge that turns `accent` on focus; labels above in `caption` `muted`.

### State marks
| State | Mark | Row word | Chip |
|---|---|---|---|
| working / starting | 8px `accent` dot, pulsing (a static ring under reduced motion) | Working / Starting | "Working" `accent` |
| needs permission / answer | 8px `attention` dot | Approval / Input | `chip-attention` with the full label |
| completed | check `success` | Done (unread only) | "Completed" `success` |
| cancelled | dash `muted` | Stopped (unread only) | "Cancelled" `muted` |
| failed | cross `error` | Failed | "Failed" `error` |
| interrupted | pause `warning` | Paused | "Interrupted" `warning` |
| idle | dashed circle `faint` | time | "Idle" `muted` |
| closed | hollow dot `faint` | Closed (unread only) | "Closed" `muted` |

Only the attention chip has a fill. Icons are lucide, 12–16px, one stroke weight.

## Motion

One set of values. The easing is the `--ease-app` token in `index.css`; the durations are the bare `duration-100`, `duration-160` and `duration-240` utilities and nothing else (named here as fast, base and slow):

| Token | Value | Use |
|---|---|---|
| `fast` | 100ms | Hover and press steps, menu and tooltip enter/exit, the row slot cross-fade |
| `base` | 160ms | Chevrons, disclosure rows, panel fade-in |
| `slow` | 240ms | Dialogs and the drawer, group and shelf expand/collapse, the meter fill |
| easing | `cubic-bezier(0.2, 0, 0, 1)` | Everything above (exponential ease-out) |
| `pulse` | 1.6s | The working dot, loading placeholders, the reconnecting dot |
| `spin` | 0.9s linear | Spinners |

Authored moments:
- **Sidebar groups and shelves** open and close by animating `grid-template-rows` 0fr ↔ 1fr (`slow`); collapsed content is `inert`. Chevrons rotate (`base`).
- **Rows** step surfaces in `fast`; the meta text and the "…" button cross-fade in place; inline rename swaps without layout shift.
- **Menus, context menus, pickers, selects** scale from 0.97 at their transform origin and fade (`fast`); **tooltips** rise 2px and fade; **dialogs** rise 8px and fade with the backdrop (`slow`); the **drawer** slides from the left (`slow`); overlay panels slide in from the right; inline panels fade in.
- **Task switching:** the pane rises in (`rise`, 240ms) while the header keeps its height, so nothing jumps; loading shows pulsing placeholders.
- **Transcript:** new items rise in; streaming text appends without animation; "Thinking…" shimmers `muted` → `body`; the working dot pulses; the locate action flashes `accent-wash` for 1.4s.
- **Composer:** Stop and Steer rise in when a turn starts; the send button scales to 0.95 on press.
- **Reduced motion:** `@media (prefers-reduced-motion: reduce)` disables every transition and animation; the working dot becomes a static ring, placeholders are static, groups snap.

No motion library; everything is CSS transitions and keyframes through Tailwind (`animate-rise`, `animate-fade-in`, `animate-slide-in`, `animate-pulse-dot`, `animate-spin`, `animate-shimmer`, `animate-flash`).

## Accessibility

- **Focus:** `:focus-visible { outline: 2px solid focus; outline-offset: 2px }` everywhere; inside rows the ring is inset. Base UI traps focus in dialogs and the drawer and returns it to the opener.
- **Keyboard:** the sidebar as described; Esc closes the topmost popup, then a panel, then the Changes sheet; Enter/Shift+Enter/Ctrl+Enter in the composer; arrow keys on the panel handle; in the folder picker, arrows, Enter, Backspace, type-ahead and Ctrl/Cmd+Enter as described.
- **Names:** every icon button has an `aria-label`; pickers put the value and any reason in theirs; the meter has `aria-valuetext`; the connection dot has `role="status"` text.
- **Live regions:** the ledger summary is `aria-live="polite"`; the transcript is `role="log"`; errors are `role="alert"`.
- **Targets:** ≥44×44 on coarse pointers (rows, buttons, menu items, pickers, choice rows).
- **Colour is never the only signal:** every state has a glyph and a word; add/del rows carry `+`/`−`.
- **Zoom:** the layout holds at 200% (the sidebar becomes the drawer under 960px).

## CSP constraints

The server sends `default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; font-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'`. Consequences:

- **No `<style>` elements at runtime.** This is why the primitives are Base UI: it creates none (its scroll lock and positioning go through CSSOM), and the app wraps itself in `<CSPProvider disableStyleElements>` so the two parts that could render one (Select and ScrollArea) never do. Radix was rejected because its modal layers inject `<style>` through react-remove-scroll.
- **No inline `style=""` markup.** Dynamic values that cannot be classes (the panel width, the meter fill, Base UI's popup position) are set through the CSSOM (`element.style.setProperty`, React's `style` prop), which `style-src` does not govern. The browser checks record zero `securitypolicyviolation` events.
- **No inline scripts, no `eval`.** Vite emits external chunks only.
- **Fonts and icons from `'self'`:** fontsource woff2 files hashed into `/assets/`; icons are lucide SVG in the markup.

## Do / Don't

### Do
- Step surfaces by one shade to show depth; add a hairline only to floating or interactive surfaces.
- Keep the assistant flat and give the user the bubble.
- Reserve `attention` for states that need a human: a dot, a row word, a chip. Never a button fill.
- Set every identifier (model, branch, tool, path, command, diff) in JetBrains Mono at 11–13px.
- Truncate rows; wrap nothing there. Put every context-menu action on a "…" button too.
- Collapse thinking by default, collapse the ledger once everything is done, keep the shelves collapsed.
- Let the transcript, assistant content and composer fill the pane between the gutters.

### Don't
- Don't draw a border inside a border; a card's children use indents and sunken wells.
- Don't add a coloured primary button; the primary is ink.
- Don't use pills; the only 14px radius is the user bubble.
- Don't use a serif, weights outside 400–600, an eyebrow above a heading, or scaled markdown headings in chat.
- Don't fill chips except the attention chip.
- Don't show heuristics or previews in rows; rows show server state.
- Don't put a colour on a ready row; ready is a time.
- Don't cap the chat column at desktop widths or make the sidebar resizable.
- Don't use `<style>` elements or `style=""` markup; classes first, CSSOM custom properties for measured values.

## Iteration guide

1. Add a token before you add a value: colours, sizes, radii, shadows and the easing live in `web/src/index.css` under `@theme`, mirrored here; durations are the bare `duration-100/160/240` utilities.
2. Add a component here before adding one there; use the Base UI wrapper in `components/ui/` rather than a raw primitive.
3. A new state extends the State marks table; do not invent a fifth semantic colour.
4. Check contrast for every new text/surface pair before merging.
5. Screenshot at 420×900 and 1440×900 for any change to layout tokens; keep `securitypolicyviolation` at zero.
