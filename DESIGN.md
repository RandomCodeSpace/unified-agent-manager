---
version: 4
name: uam-web-workbench
description: Design system for `uam web`, the browser workbench for running coding agents. A dense, calm, borderless tool used for hours at a time, on a laptop over SSH and at 420px on a phone. One cool, near-white canvas (a single theme, no dark scheme), one ink ladder for every piece of chrome, hairlines instead of boxes, a single restrained accent (blue) for focus, motion and links, and one warm "needs you" colour (orange) that is the only thing allowed to shout. Geist Sans for UI and chat, JetBrains Mono for identifiers, code and diffs. No serif, no pills, no gradients. Everything is a row or a column of text; surfaces step by one shade, never by borders inside borders. Built with Tailwind v4 (this file's tokens are the `@theme`) and Base UI primitives (menus, context menus, dialogs, tooltips, selects) under a strict CSP.

colors:
  rail: "#f7f8fa"
  canvas: "#fcfcfd"
  surface: "#f7f8fa"
  raised: "#ffffff"
  sunken: "#f0f1f4"
  bubble-user: "#f1f2f5"
  ink: "#25262b"
  body: "#494b53"
  muted: "#686b77"
  faint: "#686d79"
  hairline: "#e6e7ec"
  hairline-strong: "#c9ccd5"
  primary: "#25262b"
  on-primary: "#ffffff"
  accent: "#2a55bd"
  on-accent: "#ffffff"
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
  code-bg: "#f3f4f7"
  diff-add-bg: "#d3e6da"
  diff-add-text: "#14532d"
  diff-del-bg: "#efd6d2"
  diff-del-text: "#7f1d1d"
  backdrop: "rgba(28, 27, 24, 0.35)"
  badge-red: "#b3352a"
  badge-orange: "#a84c12"
  badge-amber: "#876000"
  badge-lime: "#56740f"
  badge-green: "#287541"
  badge-teal: "#13756b"
  badge-cyan: "#0e7089"
  badge-blue: "#3260c4"
  badge-violet: "#6c4fc6"
  badge-pink: "#b0347c"

typography:
  display-md:
    fontFamily: "'Geist Variable', system-ui, -apple-system, 'Segoe UI', Roboto, sans-serif"
    fontSize: 20px
    fontWeight: 600
    lineHeight: 1.3
    letterSpacing: -0.2px
  display-sm:
    fontFamily: "'Geist Variable', system-ui, sans-serif"
    fontSize: 17px
    fontWeight: 600
    lineHeight: 1.35
    letterSpacing: -0.1px
  title:
    fontFamily: "'Geist Variable', system-ui, sans-serif"
    fontSize: 14px
    fontWeight: 600
    lineHeight: 1.4
    letterSpacing: 0
  ui:
    fontFamily: "'Geist Variable', system-ui, sans-serif"
    fontSize: 13px
    fontWeight: 500
    lineHeight: 1.4
    letterSpacing: 0
  ui-regular:
    fontFamily: "'Geist Variable', system-ui, sans-serif"
    fontSize: 13px
    fontWeight: 400
    lineHeight: 1.4
    letterSpacing: 0
  chat-body:
    fontFamily: "'Geist Variable', system-ui, sans-serif"
    fontSize: 14px
    fontWeight: 400
    lineHeight: 1.7
    letterSpacing: 0
  chat-strong:
    fontFamily: "'Geist Variable', system-ui, sans-serif"
    fontSize: 14px
    fontWeight: 600
    lineHeight: 1.7
    letterSpacing: 0
  caption:
    fontFamily: "'Geist Variable', system-ui, sans-serif"
    fontSize: 12px
    fontWeight: 400
    lineHeight: 1.4
    letterSpacing: 0
  eyebrow:
    fontFamily: "'Geist Variable', system-ui, sans-serif"
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
  project-badge:
    textColor: "{colors.on-primary}"
    fontSize: 8px
    fontFamily: "{typography.code.fontFamily}"
    fontWeight: 600
    rounded: "{rounded.xs}"
    size: 16px
  project-filter:
    backgroundColor: transparent
    textColor: "{colors.muted}"
    typography: "{typography.ui-regular}"
    rounded: "{rounded.sm}"
    padding: "0 6px 0 4px"
    height: 28px
  project-filter-active:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.ink}"
    typography: "{typography.ui}"
    shadow: "0 1px 2px rgba(28, 27, 24, 0.05)"
  segmented-control:
    backgroundColor: "{colors.sunken}"
    textColor: "{colors.muted}"
    typography: "{typography.ui}"
    rounded: "{rounded.sm}"
    padding: "2px"
    height: 32px
  segmented-control-selected:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.ink}"
    rounded: "{rounded.xs}"
    shadow: "0 1px 2px rgba(28, 27, 24, 0.05)"
  task-row:
    textColor: "{colors.body}"
    fontSize: 12px
    rounded: "{rounded.md}"
    padding: "8px 10px"
    minHeight: 56px
  task-row-selected:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.ink}"
    typography: "{typography.ui}"
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
    heightPhone: 55dvh
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

Version 3 describes the shipped system after the 2026-09-24 revamp (issue #173): the sidebar is the only place Tasks are listed, in the manner of T3 Code's sidebar; the composer settings are one compact toolbar; every surface is built from Tailwind utilities on the tokens above, with Base UI primitives for menus, context menus, dialogs, tooltips and selects. There is one cool light theme and no theme toggle.

Version 4 adds the Project badge and its ten tones (#184), the collapsible and filterable sidebar with a quiet placeholder in place of the empty-state screen (#185), and the Settings view with the send default (#183).

**Key characteristics**
- Two ladders carry the whole UI: surfaces (`rail` → `canvas` → `surface` → `raised`, plus `sunken` for wells) and text (`ink` → `body` → `muted` → `faint`). Chrome never uses a colour outside these ladders except the four semantic tones, the accent and the attention colour.
- Borderless by default. A component gets a hairline only when it floats or is interactive (composer, cards, menus, dialogs, inputs). Nothing inside a card gets another border; nested structure uses a `sunken` well or an indent.
- Colour is reserved for act-now (`attention`), in-motion (`accent`) and broken (`error`, `warning`). Ready is unlabelled: a finished row shows its relative time, not a colour. The one exception is the Project badge, a 20px square in one of ten tones that identifies a Project wherever it appears.
- The assistant does not get a bubble. The user does.
- Density is a row: 32px sidebar rows, 28px shelf headers, 24px ledger rows, 44px headers, 28px composer pickers. A coarse pointer grows every target to 44px by padding, not by scaling type.
- Motion is functional, short and consistent (see Motion); every animation is off under `prefers-reduced-motion`.

## Principles

1. **Stable data only.** Rows show state the server knows (task state, stage, time, branch, model), never heuristics or previews.
2. **Typographic hierarchy before boxes.** Weight (400/500/600), then colour step (`ink`/`body`/`muted`), then indent, then a hairline. A bordered box is the last resort.
3. **One accent, one alarm.** `accent` is the only chromatic chrome colour. `attention` is the only warm one and the only one that may fill (a chip wash). The badge tones are identity, not state: they never mean anything but "this Project".
4. **Surfaces step, they do not stack.** Depth = one shade lighter. Shadows are for things that float over content (menus, tooltips, dialogs, the drawer, an inline panel's selected row).
5. **Code is code.** Anything typed, run, edited or diffed, and every identifier (model, branch, path), is JetBrains Mono at 11–13px. Prose is Inter.
6. **Every action has three doors.** Anything in a context menu is also on a hover "…" button (always visible on touch) and reachable by keyboard.
7. **CSP-clean.** No `<style>` elements, no inline `style` markup, fonts from `'self'` (see CSP constraints).

## Colors

### Theme
One theme. The owner's screenshot references on 2026-09-24 supersede the earlier warm mid-light direction: a near-white cool canvas, pale gray rail and user bubbles, white raised controls. Semantic status colors retain their meaning. There is no theme toggle, stored theme preference or OS-driven switch; `color-scheme: light` keeps native controls in step.

In Tailwind the tokens are `--color-<name>` in `web/src/index.css` (`@theme`), used as `bg-raised`, `text-muted`, `border-hairline` and so on. Tailwind's own palette is removed (`--color-*: initial`), so nothing outside this table can be written by accident.

### Surfaces
| Role | Value | Use |
|---|---|---|
| `rail` | #f7f8fa | Sidebar and drawer floor |
| `canvas` | #fcfcfd | Main pane: header, transcript, panels, login, empty states |
| `surface` | #f7f8fa | Hover step on raised controls; read-only composer |
| `raised` | #ffffff | Composer, interaction cards, menus, dialogs, inputs, selected sidebar row |
| `sunken` | #f0f1f4 | Wells: code blocks, thinking body, subagent rows, inline code, meter track |
| `bubble-user` | #f1f2f5 | User message bubble |
| `code-bg` | #f3f4f7 | Cool neutral background for code |
| `selection` | #cfdaf3 | `::selection` |
| `backdrop` | rgba(28,27,24,.35) | Behind dialogs, the drawer and overlay panels |

### Text
| Role | Value | Use |
|---|---|---|
| `ink` | #25262b | Titles, selected and needs-you rows, user bubble text, code, project names |
| `body` | #494b53 | Assistant prose, default UI text, rows at rest |
| `muted` | #686b77 | Meta, captions, times, ledger rows, shelf headers, placeholders, thinking text |
| `faint` | #686d79 | Glyphs only: chevrons, idle and closed marks, diff line numbers, version (≥3:1) |

### Lines
| Role | Value | Use |
|---|---|---|
| `hairline` | #e6e7ec | Separators, header rule, card edge, code block edge, shelf rule, panel edge |
| `hairline-strong` | #c9ccd5 | Input edge, secondary button edge, menu and dialog edge |

### Action and signal
| Role | Value | Use |
|---|---|---|
| `primary` / `on-primary` | #25262b / #ffffff | Primary button and tooltip fill. There is no coloured primary button. |
| `accent` / `on-accent` | #2a55bd / #ffffff | Links, focus ring, working dot and word, selected radio check, panel handle when active, caret |
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

### Badges
Each Project has a badge: two uppercase characters the service chooses from the name (unique among Projects, kept across renames) on one of ten tones, stored as the palette key. The tones are fills under `on-primary` text, tuned to the same depth as the semantic tones so they sit quietly on paper. They appear in sidebar Task metadata, the Project filter, the Task header and the Edit and Remove project dialogs; the Add project dialog says the badge is assigned when added, since available letters and colours can change before creation.

| Token | Value | On `on-primary` text | Against `rail` | Against `canvas` |
|---|---|---|---|---|
| `badge-red` | #b3352a | 5.42 | 4.43 | 4.87 |
| `badge-orange` | #a84c12 | 5.06 | 4.13 | 4.54 |
| `badge-amber` | #876000 | 5.06 | 4.14 | 4.55 |
| `badge-lime` | #56740f | 4.81 | 3.93 | 4.32 |
| `badge-green` | #287541 | 5.05 | 4.13 | 4.54 |
| `badge-teal` | #13756b | 4.95 | 4.05 | 4.45 |
| `badge-cyan` | #0e7089 | 5.07 | 4.14 | 4.55 |
| `badge-blue` | #3260c4 | 5.20 | 4.25 | 4.67 |
| `badge-violet` | #6c4fc6 | 5.25 | 4.29 | 4.71 |
| `badge-pink` | #b0347c | 5.17 | 4.22 | 4.64 |

Every tone clears AA for the text on it (≥4.5:1; the tightest is `lime` at 4.81) and the 3:1 non-text minimum against the sidebar and the canvas. A new tone must keep both.

### Contrast (WCAG 2.x, computed)
Every text colour clears AA (≥4.5:1) on every ground it can sit on; `faint` is glyph-only and held at ≥3:1. The values are unchanged from version 2 (the table there still applies); the tightest pairs are `muted` on `hairline` (4.57) and the semantic tones on `rail`/`sunken` (4.8–4.9). Do not lighten them and do not darken the grounds. The badge tones have their own table above.

## Typography

- **UI and chat:** Geist Variable (`@fontsource-variable/geist`, SIL OFL 1.1), family `'Geist Variable'`.
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
| `chat` / `chat-lg` | 14px / 16px | 400 | 1.7 / 1.6 | Conversation prose at 14px; composer input uses 16px on phones to prevent iOS input zoom |
| `caption` (+ `font-semibold`) | 12px | 600 | 1 | The two characters of a Project badge |
| `caption` | 12px | 400 | 1.4 | Times, row status words, shelf headers, counts, chips, notes, settings help |
| `code` / `code-sm` | 13px / 12px | 400 | 1.55 / 1.5 | Code blocks / ledger rows, file paths, diff body, picker values |
| `keycap` (11px mono) | 11px | 400–500 | 1 | Branch names, model names in headers, version |
| `eyebrow` | 11px | 600 | 1.2 | Reserved; no eyebrows above headings in the shipped UI |

Rules: weight is ternary (400/500/600); only `display-*` carry negative tracking; counts, durations and times use `tabular-nums`; markdown headings inside chat do not scale up; the transcript fills the main pane between the gutters, with no fixed column cap.

## Layout

### Shell
Full viewport, no page scroll: `grid h-dvh grid-cols-[264px_minmax(0,1fr)]` from 960px up. The sidebar is a **fixed 264px** and is not resizable, but it **collapses**: the UAM brand at the left of its header (or Ctrl/Cmd+B) animates the first grid column to 0 over `slow` (240ms) while the sidebar keeps its 264px inside an `overflow-hidden` column, so nothing inside reflows; once hidden it is `inert`, and the main pane takes the whole width. The state is kept per browser (`uam.sidebar`). While it is hidden, the same toggle sits at the start of the main pane's header (the Task header, the Settings header, or the placeholder's header) and brings it back. Below 960px the sidebar is a 300px drawer (a Base UI dialog sliding from the left, with backdrop) that the toggle in the pane header, and Ctrl/Cmd+B, open and close. The main pane is a column: header (44px, hairline below) → optional meta line (28px) → transcript (flex 1, `overflow-y: auto`, `overscroll-behavior: contain`) → composer (pinned).

### Side panels (Changes, Subagents)
From 1280px a panel sits inline to the right of the column and is **resizable**: an 8px handle on its inner edge (`role="separator"`, `aria-orientation="vertical"`, `aria-valuenow/min/max`), drag with pointer capture, arrow keys step 16px (64 with Shift), Home/End go to the limits, Enter or double-click resets to the default. Width is clamped to 320px … min(880px, viewport − 264 − 480) so the chat column keeps at least 480px, and saved per panel in `localStorage` (`uam.panel.changes`, `uam.panel.subagents`). The width is applied as the `--panel-w` custom property through CSSOM during the drag, so nothing re-renders per pointer move. Between 960 and 1279px a panel is a 440px overlay from the right; below 960px it is a full-screen sheet. Overlays and sheets do not resize. One panel is open at a time.

### Breakpoints
| Width | Sidebar | Column | Panels |
|---|---|---|---|
| ≤480 (phone) | Drawer | 12px gutters, chat 16px, controls 44px | Full-screen sheet |
| 481–959 | Drawer | 16px gutters | Full-screen sheet |
| 960–1279 | Fixed 264px, collapsible | 24px gutters | Overlay 440px |
| ≥1280 | Fixed 264px, collapsible | 24px gutters, fills the rest | Inline, resizable |

A collapsed sidebar does not change the panel limits: an inline panel still keeps 480px for the chat column as if the sidebar were showing.

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

`xs` 4px chips, inline code, inputs in rows, the Project badge, a segment inside a segmented control; `sm` 6px buttons, rows, menu items, inputs, the segmented control's track; `md` 10px composer, code blocks, cards, menus, dialogs; `lg` 14px the user bubble only; `full` dots and spinners. No pills.

## Components

Every component is Tailwind utilities on the tokens, in `web/src/components/`; the Base UI wrappers live in `web/src/components/ui/` (`button`, `menu` with `ContextMenu`, `dialog` with `AlertDialog` and `Sheet`, `tooltip`, `select`, `segmented` on the radio group). States are Base UI data attributes (`data-open`, `data-highlighted`, `data-disabled`, `data-starting-style`, `data-ending-style`) or ARIA (`aria-current`, `aria-pressed`, `aria-expanded`). Hover is one surface step; press adds `ink`.

### Sidebar
Sits on `rail`, 264px, no border to the main pane (the `rail` → `canvas` step is the seam).

**Header (44px).** Local Search at left, followed by the compact 16px UAM mark, Add project and New task. The UAM mark toggles the sidebar without changing the selected Task or URL, retaining Ctrl/Cmd+B and focus restoration. The connection indicator lives in the footer. Search matches all entered words across Task name/title and real Project name/directory/branch within the selected Project filter. Results include active, settled and archived Tasks; clearing Search restores the active list and shelves. No backend search or PR lookups.

**Project filter (28px).** Under the header, a ghost row: a `ListFilter` glyph and "All projects" in `muted`, or, once a Project is chosen, its badge and name raised (`raised` fill, `ink` 500, the 1px shadow) so an active filter is unmistakable, with a 28px **×** beside it that clears the filter in one click. It opens a Base UI radio menu listing "All projects" and every Project with its badge. A filter limits the flat Task list and lifecycle shelves to that Project, and is remembered per browser (`uam.projectFilter`); a remembered Project that no longer exists counts as no filter. The row is present whenever there is a Project.

**Project badge (16px).** The two characters at 8px in the existing monospace font, weight 600, with about 3px side padding in `on-primary` on the Project's tone (`bg-badge-<tone>`), `xs` radius, `aria-hidden` beside the name that names it. It leads each Task card's project metadata, the filter trigger and its menu rows, the Task header before the title, the Edit project dialog's title, and the Remove dialog's name row.

**Flat Task list.** No project group headings or collapse controls. Tasks across the visible Projects are newest first. Choose a Project in the filter to reveal its action menu (New task, Edit project, Remove project) and Previous sessions/import entry. Adding a Project selects its filter. Project membership appears once per Task card, not as a repeated group heading.

**Task card (56px minimum).** Two compact lines: provider icon and Task title at 12px, with truthful status or relative last activity at right; Project badge/name and real branch on the secondary line at 11px. Copilot uses the official 14px Primer Octicon with a GitHub Copilot accessible label and native tooltip; other providers keep their real names. The vendored SVG carries its pinned source and complete MIT notice. There is no invented PR count, model logo or elapsed-working duration from `updated_at`. The branch describes the Project checkout, not a per-Task worktree. Cards use a soft `raised/75` fill without a border. Selected cards use `accent-wash/65`; hover uses `sunken`. Keyboard focus retains its visible outline. Task-card actions live only in the context menu, opened by right-click, Shift+F10/Menu key or supported touch long-press. No visible action dots occupy the card. All lifecycle/context menu actions, F2/double-click rename, keyboard navigation and the pinned selected Task remain available. Search results label settled/archived state.

**Shelves.** Under the flat active Task list come **Settled** and **Archived** shelf headers (28px): the word and count in `caption` `muted`, a hairline, a chevron. Collapsed by default; the state persists per shelf (`uam.shelves`). A collapsed shelf still shows the open Task pinned beneath its header. Shelf state is scoped to All projects or the selected Project. Archived Tasks open like any other, read-only.

**Keyboard.** Arrow Up/Down move between rows, Home/End jump, Enter opens, F2 renames, Shift+F10 or the context-menu key opens the row menu. Focus rings sit inside rows (`-outline-offset-2`).

**Footer.** A 28px **Settings** gear (`aria-pressed` while the Settings view is open) and the service version in `keycap` `faint` at left, **Log out** at right when a token is required. When the stream is not connected a banner row appears above it: `warning-wash` (reconnecting) or `error-wash` (offline) with the pulsing dot and the full sentence.

### Placeholder and loading states
With no Task open the main pane is a quiet placeholder: the mark at 36px and one `ui` `muted` line ("Open a task from the sidebar, or start a new one there."; without a Project, "Add a project in the sidebar to begin."). There are no buttons: New task and Add project live in the sidebar, and there is no empty-state screen (#185). When the sidebar is hidden or is a drawer, a 44px header above it carries the sidebar toggle, the brand and the connection dot. While a Task's detail loads, the header keeps its height with a quiet placeholder bar and three placeholder lines pulse below (static under reduced motion). A deleted Task explains itself and offers **Back to projects**.

### Settings view
Not a dialog: a view in the main pane, `#settings` in the URL, opened from the sidebar footer's gear and closed by its **×**, by opening a Task, or by the gear again. Its header matches the Task header (44px, `display-sm` "Settings", the sidebar toggle first when the sidebar is hidden, a spinner while a change saves). The body fills the available pane with 16–24px gutters and one compact `muted` line ("Kept by the service, so they apply in every browser.") followed by **sections**: a `title` heading, 12px gaps within sections, 16px spacing at section boundaries, and a hairline between sections. Models use compact rows in one column on narrow screens, two at 1280px and three at 1800px; provider headings span the grid. Names, IDs, costs, visibility state and 44px touch targets remain available. The header stays fixed while the body scrolls. A **row** is the label (`ui` 500 `ink`) with its help in `caption` `muted` on the left and the control on the right (stacked on a phone). The **Composer** section has one row, "While a task is running, Enter…", a **segmented control** (`Steer` | `Queue`, Steer the default) whose help names the other action's shortcut and button. **Task titles** offers the provider title or a visible model for each provider with title support. **Models** lists names, IDs, reported costs and visible/hidden switches. Hidden current selections keep their label with "hidden in Settings" but do not appear as choices. Unknown hidden IDs stay removable. Controls disable while saving; stale responses cannot overwrite a newer save. A change shows at once and is saved through `PATCH /api/settings`; a refusal puts the old value back and an `error` Note says why.

**Segmented control** (`ui/segmented`, Base UI radio group): a 32px `sunken` track with 2px padding and `sm` radius; segments are `xs`, at least 64px wide, `ui` 500 `muted`; the chosen one is `raised` with `ink` text and the 1px shadow. Arrow keys move the choice; the group is named by the row label and described by its help.

### Task header
44px, `canvas`, hairline below. Left: the sidebar toggle (when the sidebar is hidden, or the drawer toggle when narrow), the Project badge, the title (`display-sm` `ink`, truncating; double-click, the hover pencil, or Rename in the menu edits it in place with the same rules as the row), then the state chip (`StateMark` with the word; `Archived`/`Settled` as an outlined chip when read-only), and a spinner while a lifecycle request is in flight. Right: **Subagents** (`Bot` + count, spinner while any run; label hidden below 481px), the **"…"** menu with the same items as the row. The model and context details live in the composer. The branch lives in its lower strip. At 420px the title truncates and action labels shorten.

### Transcript
User turns are bubbles on the right (`bubble-user`, `lg` radius, 88%/720px max, 88% on phone). Assistant prose is the page. Provider and model labels are omitted from the main conversation; the model remains in the composer. Every message has a hover copy button and a right-click menu (Copy message). Items that arrive after the transcript mounted rise in (`rise`, 240ms); items present at mount appear at once; streaming text appends with no animation.

- **Thinking:** the reasoning text inline at `ui` `muted` behind a 2px `hairline` left rule, clamped to 3 lines (measured, so one long paragraph clamps too) with **Show more** / **Show less** in `caption` (44px targets on coarse pointers); while it streams a shimmering "Thinking…" caption heads the latest three lines; done, "Thought for 12s" in `faint` sits beside the toggle. The text is markdown whose headings stay `muted` too (`md-quiet`); expanded, it has Copy thinking. Empty reasoning items are not drawn. The choice is remembered per item for the browser session.
- **Tool runs:** consecutive tool calls form one compact native disclosure, such as "Changed 1 file and ran 3 commands". Counts use actual successful known tool operations, with distinct known file paths; failures, running calls and calls without a result remain explicit. Unknown tool types count as tools, never inferred shell subprocesses. Opening the summary retains every original tool row, full input/output, copy actions, approvals and images. Assistant prose, thinking, questions and subagent rows stay interleaved and outside the grouped disclosure.
- **Turn separators:** a live foreground turn shows "Working for" from the original user item timestamp; steering does not reset it. Without a trustworthy start it says "Working". Completed turns say "Worked" without a fabricated duration because the current API has no turn completion timestamp. Background shells remain independently visible above the composer and never drive foreground timing.

- **Approval mark:** a decided permission request sits on its tool row, right-aligned: a 12px shield (`ShieldCheck`, `ShieldX` for denied, `Shield` for expired) in `faint` and one `caption` word in `muted`, "auto" for yolo, "allowed" or "denied" for a person, "expired". When several requests name one call the mark shows the latest word and "+n" in `faint`; the Base UI tooltip and the accessible name carry every request's full "title · state · resolution", latest first. It is never a line of its own. A decided request that names no tool row is a 24px row of the same kind in its turn at its time: glyph, title in `ui`, the request's first line in mono, the word at the right.
- **Question block:** a question renders as a `sunken/60` `md` block, from its interaction (any provider: text, header, choices, state, resolution) and, for Copilot's `ask_user`, from the tool's input and output, so it reads the same after a reload or a restart: a `caption` "Question" head with the `MessageCircleQuestion` glyph ("· Waiting for your answer" in `attention` while pending), each question as markdown in `body` with its choices as a list, a `success` check on the chosen ones and hollow dots on the rest, then a hairline and "You answered" in `caption` with the choice or the typed text in `ink`. Declined reads "You declined to answer."; a call left open by a restart or a stopped turn reads "No answer."; a failed call reads "Failed: …" in `error`. It sits on the tool row that asked, or stands alone in its turn when no row did. The pending action card below the transcript still takes the answer.
- **Tool images:** the images a tool returned sit under its row, indented to the text, visible when its run is expanded, without opening the individual tool details: the same thumbnails and lightbox as transcript attachments (160px, `sunken`, hover lifts and scales 1.02, at least 44px square on coarse pointers); `images_note` follows as a `caption` `muted` line. The row appears first without them and gains them when UAM has stored them.
- **Code block:** `code-bg`, hairline, `md` radius, a 24px header with the language (or `code`) and a copy button; right-click offers Copy code. Max height 480px with internal scroll; never wraps.
- **Code highlighting:** a block with a language gets `hljs-*` spans in five tones on `code-bg`: comments `muted`, keywords `accent`, strings `success`, numbers `attention`, names and types `warning`, titles `ink` at 500; added and removed diff lines use the diff tokens. Nothing italic, nothing outside the ladders. The grammars load on the first such block.
- **Diagram card:** a fenced `mermaid` block in the code-block chrome. The header reads `mermaid`, shows a spinner while the frame renders, then a Diagram / Code toggle (two 20px ghost buttons, `aria-pressed`) before the copy button, which copies the source. The diagram is an `<img>` on the well, centred at its intrinsic size up to the block width and 480px, and fades in (`base`); clicking it opens the lightbox (the attachments' dialog). While the fence is still streaming the code shows; when Mermaid rejects the source the code stays, with a `caption` note under it ("Diagram could not be rendered: …"). Inside the diagram the colours are tokens (`raised` nodes, `hairline-strong` borders, `muted` lines, `accent-wash` secondary, `warning-wash` notes) in the system sans font, since neither the sandboxed frame nor an SVG image can load Inter.
- **Subagent row:** a `sunken` row with the `Bot` glyph, the name, the status chip, model, duration and **Open**; right-click offers Open and Copy agent ID. Subagent output never renders in the main column.
- **Interaction card (pending):** `raised`, hairline, `md`; a `chip-attention` with the `ShieldQuestion`/`MessageCircleQuestion` glyph heads it; the request in a `sunken` mono well; buttons right-aligned (Deny `danger`, Allow for task `secondary`, Allow once `primary`; stacked full-width on phone). Once decided it leaves the bottom of the transcript for its tool row (the approval mark) or its turn (the quiet row).

### Composer
`raised`, hairline, `md`, pinned under the transcript with the column gutters; focus-within lifts it one shadow step. The textarea (`chat`, `field-sizing: content`, 56px to 40dvh) sits above a wrapping toolbar:

- **Pickers** at left: Model (`Cpu`, value in `code-sm`), combined Effort and Context (`Gauge`, e.g. "high · 200K"), and Permissions (`Shield`; `ShieldOff` in `attention` for yolo), separated by hairlines. The combined menu has two named sections; unsupported choices show their reason. Model/effort/context changes stay disabled during a turn. Permissions can change during a turn. Read-only values remain visible and cannot change.
- **Context ring:** a 16px ring after Model, with an empty track until usage is reported. It is `accent`, `attention` at 80%, and `error` at 95%. Its accessible name, tooltip and click/tap popover carry used/limit/percentage; the popover also shows reported prompt/cache counts.
- **Credits:** only when the provider reports usage. A quiet chip shows remaining allowance, `attention` at 20% and `error` at 5%; the popover shows used/entitlement, a future reset date, Task AI units and stale state. The adjacent input-cost estimate uses current context, cache-read prices where known, and long-context pricing where applicable. It excludes output and stays absent when prices or context are unknown. Model choices carry the same estimate and reported cost tier/discount.
- **Project strip:** attached below the composer, `sunken`, `caption`; Project badge/name and "Project folder" on the left, changed-file count opening Changes and display-only branch on the right. The Changes opener remains available on read-only Tasks. On phones only the count and branch remain. The Task header has no duplicate Changes opener; closing Changes returns focus to the footer opener.
- **Actions** at right: while a turn runs, **Stop** (round `error` button, square glyph), then the other action as a labelled secondary button and Enter's action as a round primary button: with the default setting (Steer) that is **Queue** (secondary, `ListPlus`) then **Steer** (primary, `Zap`); with Queue it is **Steer** (secondary, `Zap`) then **Queue** (primary, `ListPlus`). Otherwise **Send** (round primary, `ArrowUp`). Tooltips carry the shortcuts and follow the setting: Enter does the primary, Ctrl+Enter the other, Shift+Enter adds a line. When a steer is impossible (the message carries files or attachments) Enter queues, the primary is Queue, the Steer button stays, dimmed, with its reason, and a note above the textarea says why. Read-only Tasks show no send controls.
- Notices (read-only, uncertain, rejected, errors) sit in a hairline-separated strip above the textarea; the queue is a disclosure strip with numbered rows, a per-row cancel, Resume and Clear. "Latest turn ran on ‹model›" appears below the toolbar only when routing differed.
- **Inline pickers:** typing `/` or `$` as the first character or `@` at the start or after whitespace opens a level-2 listbox above the textarea, full width on phone and 440px from `sm` up, max height min(300px, 40dvh). Rows are 30px (44px on touch); `/` groups rows under Commands and Skills, `$` offers only Skills, and `@` lists files and folders in `code-sm`. Focus never leaves the textarea, which drives the list through `aria-activedescendant`: Up and Down move, Enter or Tab picks, Escape dismisses. Loading, empty, and reason lines (for example, an unavailable provider catalogue) sit inside the popover.
- **Command outcomes and execution:** alias search and literal argument choices reuse the inline picker. Disabled commands carry a reason and never fall through as prompts; catalogue failures hold slash input with Retry commands. Command results use a compact composer strip for text or explicit subcommand choices, or open existing controls. Execution mode and runtime objective state occupy a separate caption strip above the textarea; details disclose reported turns, credits, limits and reasons. Unknown observations stay qualified. Stop remains available during autonomous continuation, including between turns, and never optimistically claims completion. Safe/Yolo remains the separate permission policy.
- **Chips row:** picked `@` files and uploads sit in a wrapping row above the textarea. An upload chip is a 44px `sunken` well holding a 36px thumbnail (or the kind's glyph), the name, and its state: `Uploading… 42%` with a 2px `accent` bar along the bottom edge, size and kind when done, or the refusal in `error` on an `error-wash`. A file-reference chip is a smaller mono chip with `@`. Each chip has a remove button. Queued prompts repeat these as 20px caption chips.
- **Attach:** a subtle `Paperclip` icon button sits before Send/Stop on the right. Its tooltip gives the model's media gate and the limits (images 3 MiB, PDF 10 MiB, text 256 KiB, 5 per message). It never disables for the model, since text is always allowed: the gate narrows the file picker and the tooltip names it. Once a message holds the most uploads it can carry, the button stays visible, dimmed, `aria-disabled`, and says why. Read-only Tasks show no composer controls, so no Attach button. Paste and drag-and-drop also work; while files are dragged over the composer, a `raised` overlay with an inset dashed `accent` outline shows "Drop to attach" and the same note (`fade-in`).
- **Transcript attachments:** a user turn's images render as thumbnails up to 160px high; hover lifts the shadow and scales the image to 1.02. Clicking one opens a lightbox dialog (max 92vw by 1100px, image max 72dvh) with **Open original**. Other files are 32px chips with glyph, name, and size that open the stored copy; without a stored copy the chip is inert.
- Phone: the toolbar wraps and keeps the values visible; controls have 44px coarse-pointer targets and the textarea is 16px.

### Menus, context menus, tooltips, selects, dialogs
All Base UI. Menus: `raised`, `hairline-strong`, `md`, 4px padding, `float` shadow, 30px items (44 on coarse pointers), highlighted item one surface step, destructive item in `error` with `error-wash` highlight, disabled items dimmed with their reason in `caption` beneath. They scale from 0.97 and fade over 100ms from the transform origin. Context menus open at the pointer (long-press on touch) and hold exactly the items of the matching "…" button. An item's action runs once the menu has finished closing, and an action that takes focus itself (inline rename) tells the menu to leave focus alone (`takesFocus`), so the menu's focus return never undoes it. Tooltips are `ink` on `on-primary`, 400ms delay, no delay while another is open; they are hints for sighted users and never the only name of a control. Selects (project dialogs) open below their trigger with a check on the chosen row. Dialogs: `raised`, `md`, 20px padding, 440px max (560px, `--spacing-sheet-wide`, while Add project browses folders), `modal` shadow on the backdrop; a close button in the title row; on phone they become a bottom sheet with full-width buttons. A dialog is never taller than the viewport less its 16px gutters: the title row stays and the body scrolls inside. Confirmations (`AlertDialog`) focus **Cancel** first; Archive and Delete always confirm.

### Folder picker
The Add project dialog's path field keeps a **Browse** button (`FolderOpen`, secondary, `aria-expanded`) beside it. Browse opens the picker inline under the field (`fade-in`), the dialog widens to 560px over `slow` and settles back once a folder is chosen. Nothing floats: the picker is a column of rows in the dialog (`components/FolderPicker.tsx`).

- **Breadcrumb.** The folder being shown as `code-sm` mono segments, root first, each a 24px button (44 on coarse pointers; `muted`, the last `ink` with `aria-current="location"`), `/` separators in `faint`, wrapping when deep. At its right, flush with the well's edge, a **Show hidden** toggle (`EyeOff`/`Eye`, ghost, `aria-pressed`) that lists the folder again with dot-folders (`hidden=1`).
- **The well.** A `canvas` well (`sm`), fixed at `--spacing-picker` (min(320px, 40dvh)) so moving between folders never changes the dialog's height; on phone `--spacing-picker-phone` (55dvh) fills the sheet. Inside, a `role="listbox"` whose only children are the `role="option"` rows: a `Folder` glyph, the name in `code` mono, and marks in `caption` `muted` for a git repository (`GitBranch` + "git") and a symbolic link (`Link` + "link"); hidden names are `muted`. Rows are 32px (44 on coarse pointers); hover is one step up (`surface`), the selected row is `raised` with `ink` and the 1px shadow, the same as a selected sidebar row. Each row is one button; the `ChevronRight` at its end is a 32px (44 on coarse pointers) hit region of that button, not a control of its own: a press there opens the folder. Status lines (loading, empty, error, "Showing the first 1,000") sit in the well above or below the listbox, never inside it.
- **Selection is the target.** A click selects, a double-click or the chevron opens. **Use this folder** takes the selected path, else the folder being shown; the path it will take sits beside the button in `code-sm` mono, truncated at its start so the folder's own name stays visible. Navigating clears the selection.
- **Footer.** **Up** (`FolderUp`, disabled at `/`) and **New folder** (`FolderPlus`) at left, **Use this folder** (secondary; the dialog's primary stays **Add project**) at right; full-width buttons on phone. New folder adds a row at the top of the well, with no rule beneath it: the standard text input in mono and Create/Cancel icon buttons. Enter creates the folder and, if the user is still in that folder, selects it (a dot-name turns Show hidden on); the new path is the target at once, before the list refreshes. Escape cancels; errors sit under the row in `caption` `error`.
- **Keyboard.** Focus rests on the listbox (`aria-activedescendant`): Up and Down move, Home and End jump, Enter opens the selected folder, Backspace goes up, typing jumps to a name, Ctrl/Cmd+Enter anywhere in the picker means Use this folder. Escape cancels the New folder row first, then closes the picker; the dialog stays open.
- **Quiet states.** Loading shows three pulsing placeholders only before the first listing; afterwards the previous rows dim while the next folder loads. An empty folder says "No folders here". A folder that cannot be listed keeps its breadcrumb and Up and says why in `caption` `muted`: "You don't have access to this folder" for 403, "This folder does not exist" for 404. A path in the field that is not a folder starts the picker at home instead.

### Buttons and inputs
Primary is ink; secondary is `raised` with a `hairline-strong` edge; ghost has no fill; danger is `error` text with `error-wash` hover; icon buttons are 28px (32 in headers) `muted` glyphs. Disabled is 45% opacity. Inputs are 36px `raised` with a `hairline-strong` edge that turns `accent` on focus; labels above in `caption` `muted`.

### State marks
| State | Mark | Row word | Chip |
|---|---|---|---|
| working / starting | the working mark: a 14px `accent` glyph, a 2px-radius core breathing (opacity 1 → 0.45) inside a 25% ring while a 1.5px satellite orbits it, both 1.6s and in step (`orbit` linear, `breathe` ease-in-out); still under reduced motion | Working / Starting | "Working" `accent` |
| needs permission / answer | 8px `attention` dot | Approval / Input | `chip-attention` with the full label |
| completed | check `success` | Done (unread only) | "Completed" `success` |
| cancelled | dash `muted` | Stopped (unread only) | "Cancelled" `muted` |
| failed | cross `error` | Failed | "Failed" `error` |
| interrupted | pause `warning` | Paused | "Interrupted" `warning` |
| idle | dashed circle `faint` | time | "Idle" `muted` |
| closed | hollow dot `faint` | Closed (unread only) | "Closed" `muted` |

Only the attention chip has a fill. Chrome icons are lucide, 12–16px, one stroke weight. The Copilot provider mark uses its official Primer SVG. The working mark is one component (`WorkingMark` in `common.tsx`), so the sidebar rows, the Task header, the subagent "Running" chips and the transcript's "Working…" row move alike; tool rows keep the plain spinner, since a tool in flight is not the Task working.

## Motion

One set of values. The easing is the `--ease-app` token in `index.css`; the durations are the bare `duration-100`, `duration-160` and `duration-240` utilities and nothing else (named here as fast, base and slow):

| Token | Value | Use |
|---|---|---|
| `fast` | 100ms | Hover and press steps, menu and tooltip enter/exit, the row slot cross-fade |
| `base` | 160ms | Chevrons, disclosure rows, panel fade-in |
| `slow` | 240ms | Dialogs and the drawer, the sidebar collapse, group and shelf expand/collapse, the meter fill |
| easing | `cubic-bezier(0.2, 0, 0, 1)` | Everything above (exponential ease-out) |
| `pulse` | 1.6s | The working mark's orbit and breath, loading placeholders, the reconnecting dot |
| `spin` | 0.9s linear | Spinners |

Authored moments:
- **Sidebar collapse** animates the shell's first `grid-template-columns` track 264px ↔ 0 (`slow`); the sidebar keeps its width inside the clipped column and is `inert` once hidden.
- **Sidebar groups and shelves** open and close by animating `grid-template-rows` 0fr ↔ 1fr (`slow`); collapsed content is `inert`. Chevrons rotate (`base`).
- **Rows** step surfaces in `fast`; the meta text and the "…" button cross-fade in place; inline rename swaps without layout shift.
- **Menus, context menus, pickers, selects** scale from 0.97 at their transform origin and fade (`fast`); **tooltips** rise 2px and fade; **dialogs** rise 8px and fade with the backdrop (`slow`); the **drawer** slides from the left (`slow`); overlay panels slide in from the right; inline panels fade in.
- **Task switching:** the pane rises in (`rise`, 240ms) while the header keeps its height, so nothing jumps; loading shows pulsing placeholders.
- **Transcript:** new rows rise in, tool rows one by one as they arrive; streaming text appends without animation; "Thinking…" shimmers `muted` → `body`; the working mark orbits and breathes; the locate action flashes `accent-wash` for 1.4s; a rendered diagram fades in (`base`) over the code it replaces.
- **Composer:** Stop and Steer rise in when a turn starts; the send button scales to 0.95 on press.
- **Reduced motion:** `@media (prefers-reduced-motion: reduce)` disables every transition and animation; the working mark stands still, placeholders are static, groups and the sidebar snap.

No motion library; everything is CSS transitions and keyframes through Tailwind (`animate-rise`, `animate-fade-in`, `animate-slide-in`, `animate-pulse-dot`, `animate-spin`, `animate-shimmer`, `animate-flash`, `animate-orbit`, `animate-breathe`).

## Accessibility

- **Focus:** `:focus-visible { outline: 2px solid focus; outline-offset: 2px }` everywhere; inside rows the ring is inset. Base UI traps focus in dialogs and the drawer and returns it to the opener.
- **Keyboard:** the sidebar as described; Ctrl/Cmd+B hides and shows it (the drawer on a narrow screen), and focus follows the toggle so nobody is left on an inert element; Esc closes the topmost popup, then a panel, then the Changes sheet; Enter/Shift+Enter/Ctrl+Enter in the composer, following the send default; arrow keys on the panel handle and the segmented control; in the folder picker, arrows, Enter, Backspace, type-ahead and Ctrl/Cmd+Enter as described.
- **Names:** every icon button has an `aria-label`; pickers put the value and any reason in theirs; the meter has `aria-valuetext`; the connection dot has `role="status"` text.
- **Live regions:** the transcript is `role="log"`, so new tool rows announce themselves; errors are `role="alert"`. A tool row's accessible name is "name argument, state" plus its approval's full resolution; a chosen answer is marked "(chosen)".
- **Targets:** ≥44×44 on coarse pointers (rows, buttons, menu items, pickers, choice rows).
- **Colour is never the only signal:** every state has a glyph and a word; add/del rows carry `+`/`−`.
- **Zoom:** the layout holds at 200% (the sidebar becomes the drawer under 960px).

## CSP constraints

The server sends `default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; font-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'`. Consequences:

- **No `<style>` elements at runtime.** This is why the primitives are Base UI: it creates none (its scroll lock and positioning go through CSSOM), and the app wraps itself in `<CSPProvider disableStyleElements>` so the two parts that could render one (Select and ScrollArea) never do. Radix was rejected because its modal layers inject `<style>` through react-remove-scroll.
- **No inline `style=""` markup.** Dynamic values that cannot be classes (the panel width, the meter fill, Base UI's popup position) are set through the CSSOM (`element.style.setProperty`, React's `style` prop), which `style-src` does not govern. The browser checks record zero `securitypolicyviolation` events.
- **No inline scripts, no `eval`.** Vite emits external chunks only.
- **Fonts and icons from `'self'`:** fontsource woff2 files hashed into `/assets/`; icons are lucide SVG in the markup.
- **Mermaid runs in a sandboxed frame, not in the page.** It cannot render without a `<style>` element and `style` attributes, so it lives in `/diagram-frame.html`, embedded as `<iframe sandbox="allow-scripts">` (opaque origin: no cookies, no storage) and served with its own policy: `default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src data:; font-src 'self'; connect-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'`. The page posts the source, gets the SVG string back and shows it only as a `data:` image, which `img-src` already allows. The page's own policy is unchanged, and the frame's script is a classic script, since a module script from an opaque origin needs CORS (ADR 0004, "Diagrams in a sandboxed frame").

## Do / Don't

### Do
- Step surfaces by one shade to show depth; add a hairline only to floating or interactive surfaces.
- Keep the assistant flat and give the user the bubble.
- Reserve `attention` for states that need a human: a dot, a row word, a chip. Never a button fill.
- Use a badge tone only inside a Project badge; a tone never stands for a state.
- Set every identifier (model, branch, tool, path, command, diff) in JetBrains Mono at 11–13px.
- Truncate rows; wrap nothing there. Put every context-menu action on a "…" button too.
- Collapse thinking by default, collapse the ledger once everything is done, keep the shelves collapsed.
- Let the transcript, assistant content and composer fill the pane between the gutters.

### Don't
- Don't draw a border inside a border; a card's children use indents and sunken wells.
- Don't add a coloured primary button; the primary is ink.
- Don't use pills. The composer Send/Stop circles are the explicit exception; other controls keep the shared rectangular shapes.
- Don't use a serif, weights outside 400–600, an eyebrow above a heading, or scaled markdown headings in chat.
- Don't fill chips except the attention chip.
- Don't show heuristics or previews in rows; rows show server state.
- Don't put a colour on a ready row; ready is a time.
- Don't cap the chat column at desktop widths or make the sidebar resizable; it is fixed or hidden, nothing between.
- Don't put New task or Add project in the main pane; the placeholder stays quiet.
- Don't use `<style>` elements or `style=""` markup; classes first, CSSOM custom properties for measured values.
- Don't inline provider SVG or HTML; a diagram is a `data:` image rendered in the sandboxed frame.

## Iteration guide

1. Add a token before you add a value: colours, sizes, radii, shadows and the easing live in `web/src/index.css` under `@theme`, mirrored here; durations are the bare `duration-100/160/240` utilities.
2. Add a component here before adding one there; use the Base UI wrapper in `components/ui/` rather than a raw primitive.
3. A new state extends the State marks table; do not invent a fifth semantic colour.
4. Check contrast for every new text/surface pair before merging.
5. Screenshot at 420×900 and 1440×900 for any change to layout tokens; keep `securitypolicyviolation` at zero.

The Changes panel shows the workspace scope as "All uncommitted project changes
vs HEAD." The line wraps rather than truncates; it does not claim Task-only
ownership. Provider session-scope labels also wrap. File-row status columns fit
their text, including "untracked", before the truncated filename; touch menu
controls reserve 44px without covering the change counts.
