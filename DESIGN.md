---
version: 7
name: uam-web-workbench
description: Design system for `uam web`, the browser workbench for running coding agents. A dense, calm, borderless tool used for hours at a time, on a laptop over SSH and at 420px on a phone. One cool, near-white canvas (a single theme, no dark scheme), one ink ladder for every piece of chrome, elevation instead of drawn borders (surfaces float on soft cool shadows, seams fade out at their ends), a single restrained accent (blue) for focus, motion and links, and one warm "needs you" colour (orange) that is the only thing allowed to shout. Figtree everywhere in the app; JetBrains Mono only for code in the conversation (code blocks, inline code, tool rows, commands in requests) and diffs. No serif, no gradients but the fades at seams and scroll edges; the only pills are the activity and tool rows. Everything is a row or a column of text; surfaces step by one shade and one shadow level, never by borders inside borders. Built with Tailwind v4 (this file's tokens are the `@theme`) and Base UI primitives (menus, context menus, dialogs, tooltips, selects) under a strict CSP.

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
  faint: "#858995"
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
  scrim: "rgba(16, 17, 20, 0.8)"
  tint-hover: "#eceef2"
  tint-selected: "#e9edf8"
  tint-well: "#f4f5f8"
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
    fontFamily: "'Figtree Variable', system-ui, -apple-system, 'Segoe UI', Roboto, sans-serif"
    fontSize: 20px
    fontWeight: 600
    lineHeight: 1.3
    letterSpacing: -0.2px
  display-sm:
    fontFamily: "'Figtree Variable', system-ui, sans-serif"
    fontSize: 17px
    fontWeight: 600
    lineHeight: 1.35
    letterSpacing: -0.1px
  title:
    fontFamily: "'Figtree Variable', system-ui, sans-serif"
    fontSize: 14px
    fontWeight: 600
    lineHeight: 1.4
    letterSpacing: 0
  ui:
    fontFamily: "'Figtree Variable', system-ui, sans-serif"
    fontSize: 13px
    fontWeight: 500
    lineHeight: 1.4
    letterSpacing: 0
  ui-regular:
    fontFamily: "'Figtree Variable', system-ui, sans-serif"
    fontSize: 13px
    fontWeight: 400
    lineHeight: 1.4
    letterSpacing: 0
  chat-body:
    fontFamily: "'Figtree Variable', system-ui, sans-serif"
    fontSize: 14px
    fontWeight: 400
    lineHeight: 1.7
    letterSpacing: 0
  chat-strong:
    fontFamily: "'Figtree Variable', system-ui, sans-serif"
    fontSize: 14px
    fontWeight: 600
    lineHeight: 1.7
    letterSpacing: 0
  caption:
    fontFamily: "'Figtree Variable', system-ui, sans-serif"
    fontSize: 12px
    fontWeight: 400
    lineHeight: 1.4
    letterSpacing: 0
  eyebrow:
    fontFamily: "'Figtree Variable', system-ui, sans-serif"
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
    fontFamily: "'Figtree Variable', system-ui, sans-serif"
    fontSize: 11px
    fontWeight: 500
    lineHeight: 1
    letterSpacing: 0
  meta:
    fontFamily: "'Figtree Variable', system-ui, sans-serif"
    fontSize: 11px
    fontWeight: 400
    lineHeight: 1.3
    letterSpacing: 0
  badge:
    fontFamily: "'JetBrains Mono Variable', ui-monospace, monospace"
    fontSize: 8px
    fontWeight: 600
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
  command-palette:
    backgroundColor: "{colors.raised}"
    rounded: "{rounded.md}"
    maxWidth: 560px
    rowHeight: 44px
    shadow: "{shadows.modal}"
  segmented-control:
    backgroundColor: "{colors.sunken}"
    textColor: "{colors.muted}"
    typography: "{typography.ui}"
    rounded: "{rounded.sm}"
    padding: "2px"
    height: 32px
    shadow: "{shadows.well}"
  segmented-control-thumb:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.ink}"
    rounded: "{rounded.xs}"
    shadow: "{shadows.raised}"
  switch:
    backgroundColor: "{colors.faint}"
    checkedColor: "{colors.accent}"
    thumbColor: "{colors.raised}"
    rounded: "{rounded.sm}"
    size: "32px × 18px"
    shadow: "inset 0 1px 2px rgba(20, 28, 45, 0.2)"
  task-row:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.body}"
    fontSize: 12px
    rounded: "{rounded.md}"
    padding: "8px 10px"
    minHeight: 56px
    shadow: "{shadows.raised}"
  task-row-selected:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.ink}"
    typography: "{typography.ui}"
  task-row-meta:
    typography: "{typography.meta}"
    textColor: "{colors.muted}"
  shelf-header:
    textColor: "{colors.muted}"
    typography: "{typography.caption}"
    height: 28px
  main-header:
    backgroundColor: "{colors.canvas}"
    textColor: "{colors.ink}"
    typography: "{typography.display-sm}"
    height: "{layout.header-height}"
    padding: "0 8px 0 12px"
  main-header-meta:
    textColor: "{colors.muted}"
    typography: "{typography.meta}"
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
    maxWidth: "min(88%, 720px)"
    shadow: "{shadows.raised}"
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
  turn-line:
    backgroundColor: transparent
    textColor: "{colors.muted}"
    typography: "{typography.caption}"
    rounded: "{rounded.sm}"
    padding: "0 6px"
    height: 24px
  activity-panel-row:
    textColor: "{colors.muted}"
    typography: "{typography.code-sm}"
    rounded: "{rounded.sm}"
    padding: "0 32px 0 8px"
    height: 28px
  activity-row:
    backgroundColor: "{colors.tint-well}"
    textColor: "{colors.muted}"
    typography: "{typography.caption}"
    rounded: "{rounded.full}"
    padding: "0 12px 0 8px"
    height: 24px
  subagent-row:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.ink}"
    typography: "{typography.ui-regular}"
    rounded: "{rounded.md}"
    padding: "8px 8px 8px 14px"
    shadow: "{shadows.raised}"
  question-block:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.body}"
    typography: "{typography.ui-regular}"
    rounded: "{rounded.md}"
    padding: "12px 14px"
    shadow: "{shadows.raised}"
  interaction-card:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.ink}"
    typography: "{typography.ui-regular}"
    accentColor: "{colors.attention}"
    rounded: "{rounded.lg}"
    padding: "12px 16px"
    shadow: "{shadows.float}"
  composer:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.ink}"
    typography: "{typography.chat-body}"
    rounded: "{rounded.lg}"
    padding: "12px 14px 8px"
    minHeight: 96px
    shadow: "{shadows.float}"
    focusShadow: "{shadows.focus-float}"
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
    backgroundColor: "{colors.sunken}"
    textColor: "{colors.ink}"
    typography: "{typography.ui}"
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
    backgroundColor: "{colors.sunken}"
    textColor: "{colors.ink}"
    typography: "{typography.ui-regular}"
    rounded: "{rounded.sm}"
    padding: "0 10px"
    height: 36px
    shadow: "{shadows.well}"
    focusShadow: "{shadows.focus}"
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
    rounded: "{rounded.md}"
    padding: "10px 12px"
    shadow: "{shadows.well}"
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
    width: "{layout.panel-default-width}"
  panel-handle:
    width: 8px
    lineColor: "{colors.hairline-strong}, fading at both ends"
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
    rounded: "{rounded.md}"
    padding: "4px"
    shadow: "{shadows.float}"
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
    rounded: "{rounded.lg}"
    padding: "20px"
    maxWidth: 440px
    maxWidthBrowsing: 560px
    shadow: "{shadows.modal}"
  settings-card:
    backgroundColor: "{colors.raised}"
    rounded: "{rounded.lg}"
    padding: "20px"
    shadow: "{shadows.raised}"
  skeleton:
    backgroundColor: "{colors.sunken}"
    rounded: "{rounded.sm}"
    height: 16px
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
    shadow: "{shadows.raised}"

shadows:
  raised: "0 0 0 1px rgba(20, 28, 45, 0.05), 0 2px 6px rgba(20, 28, 45, 0.07)"
  float: "0 0 0 1px rgba(20, 28, 45, 0.06), 0 6px 20px rgba(20, 28, 45, 0.10)"
  modal: "0 0 0 1px rgba(20, 28, 45, 0.06), 0 20px 48px rgba(20, 28, 45, 0.18)"
  well: "inset 0 0 0 1px rgba(20, 28, 45, 0.05)"
  focus: "0 0 0 1px #2a55bd, 0 0 0 4px rgba(42, 85, 189, 0.18)"
  focus-float: "0 0 0 1px #2a55bd, 0 0 0 4px rgba(42, 85, 189, 0.18), 0 10px 28px rgba(20, 28, 45, 0.12)"
---

## Overview

`uam web` is a workbench, not a magazine: a person sits in this screen for hours and needs a launcher. One ink ladder, one surface ladder, rows instead of cards, and colour that only appears when the agent is moving, broken, or needs a human.

Version 3 describes the shipped system after the 2026-09-24 revamp (issue #173): the sidebar is the only place Tasks are listed, in the manner of T3 Code's sidebar; the composer settings are one compact toolbar; every surface is built from Tailwind utilities on the tokens above, with Base UI primitives for menus, context menus, dialogs, tooltips and selects. There is one cool light theme and no theme toggle.

Version 4 adds the Project badge and its ten tones (#184), the collapsible and filterable sidebar with a quiet placeholder in place of the empty-state screen (#185), and the Settings view with the send default (#183).

Version 6 (the 2026-09-25 floating pass) replaces every drawn border in the chrome with elevation: an elevation ladder of cool ink shadows, each level carrying a near-invisible 1px ring for a crisp edge; seams that still need a rule fade out at both ends; the transcript fades under the header and beneath the composer, which floats detached from the pane's foot as the control plane; Settings sections are floating cards; switches and segmented controls carry a floating thumb that slides; inputs, selects and secondary buttons are filled surfaces with no edge; menus, popovers, dialogs and tooltips scale in on the same ladder; Task cards lift on hover; scrollbars are thin overlays; the first load of the Task list, a transcript and the Changes list is a shimmer skeleton. Nothing animates but transform and opacity (see Motion), and there is no blur anywhere.

Version 7 (the 2026-09-25 activity pass) folds a turn's thinking and tool calls into its head row: the turn line ("Took 1m 2s · 5 thoughts (42s) · 3 commands · 2 files read ›") replaces the per-run activity rows, the assistant's paragraphs run uninterrupted, only what needs a person stays in the answer (failures, questions, permissions, images, subagents, a "Changed n files" line), and the whole timeline is one click away, in place under the turn line or in the Activity side panel. The previous per-run rows remain as the Detailed density (Settings → This browser → Activity), for comparison on the same Task.

Version 5 (the 2026-09-24 polish, W4/W5) caps the transcript and composer in one centred 760px column, moves the project strip into the Task header, makes the composer one surface with one control row, turns Stop into an ink circle like Send, gives every disclosure and side panel one animation each way, heads a turn with one status row that never moves, and cross-fades Task switches with React's ViewTransition. The 760px column was lifted afterwards: the transcript and composer fill the main pane.

**Key characteristics**
- Two ladders carry the whole UI: surfaces (`rail` → `canvas` → `surface` → `raised`, plus `sunken` for wells) and text (`ink` → `body` → `muted` → `faint`). Chrome never uses a colour outside these ladders except the four semantic tones, the accent and the attention colour.
- Borderless. Nothing in the chrome draws a line: a surface that floats or is interactive gets an elevation level (a soft cool shadow with a 1px ring), a field is a filled `sunken` surface, and a seam that must still read as one is a rule that fades out at both ends. Nothing inside a card gets another edge; nested structure uses a `sunken` well or an indent.
- Colour is reserved for act-now (`attention`), in-motion (`accent`) and broken (`error`, `warning`). Ready is unlabelled: a finished row shows its relative time, not a colour. The one exception is the Project badge, a 20px square in one of ten tones that identifies a Project wherever it appears.
- The assistant does not get a bubble. The user does.
- Density is a row: 32px sidebar rows, 28px shelf headers, 24px ledger rows, 44px headers, 28px composer pickers. A coarse pointer grows every target to 44px by padding, not by scaling type.
- Motion is functional, short and consistent (see Motion); it runs by default even under `prefers-reduced-motion`, and every animation is off there once Settings → Motion is "Match system".

## Principles

1. **Stable data only.** Rows show state the server knows (task state, stage, time, branch, model), never heuristics or previews.
2. **Typographic hierarchy before boxes.** Weight (400/500/600), then colour step (`ink`/`body`/`muted`), then indent, then a fading rule. A raised card is the last resort, and a drawn border never.
3. **One accent, one alarm.** `accent` is the only chromatic chrome colour. `attention` is the only warm one and the only one that may fill (a chip wash). The badge tones are identity, not state: they never mean anything but "this Project".
4. **Surfaces step and float, they do not stack.** Depth = one shade lighter and one level up the elevation ladder; the ladder's 1px ring is the only edge a surface has. Shadows are cool ink (`rgba(20,28,45,α)`) on this cool canvas, two layers at most, and are never animated: a hover lift or the composer's focus fades a pre-drawn shadow in through opacity.
5. **Code is code, and only in the conversation.** Code blocks, inline code, tool rows, the commands in permission requests and diffs are JetBrains Mono at 11–13px. Everything else in the app, identifiers included (model, branch, path, version, keys), is Figtree.
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
| `scrim` | rgba(16,17,20,.8) | Behind the image lightbox, which has no surface of its own |

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
| `hairline` | #e6e7ec | The sidebar's fading seam (`rail-edge`), markdown table rules and `hr` inside provider prose |
| `hairline-strong` | #c9ccd5 | The centre of every fading rule (`fade-rule`, `fade-rule-y`), the scrollbar thumb |

No chrome draws a full-width or full-height line. Where a seam still needs a rule (menu groups, the shelf headers, the answer under a question block, the tool rows' indent, the thinking text's left edge, the Changes list's foot, a dialog's secondary actions, the panel handle) it is a 1px gradient that fades out over its first and last 18% (`.fade-rule` horizontal, `.fade-rule-y` vertical, in `index.css`). Everywhere else separation is spacing, a surface step or an elevation level.

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

- **UI and chat:** Figtree Variable (`@fontsource-variable/figtree`, SIL OFL 1.1), family `'Figtree Variable'`.
- **Code in the conversation, diffs:** JetBrains Mono Variable (`@fontsource-variable/jetbrains-mono`, SIL OFL 1.1), family `'JetBrains Mono Variable'`, ligatures off.
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
| `badge` (+ `font-bold`) | 8px | 700 | 1 | The two characters of a Project badge |
| `caption` | 12px | 400 | 1.4 | Times, row status words, shelf headers, counts, chips, notes, settings help |
| `code` / `code-sm` | 13px / 12px | 400 | 1.55 / 1.5 | Code blocks / ledger rows, diff body (mono) |
| `keycap` | 11px | 400–500 | 1 | Keyboard hints, version |
| `eyebrow` | 11px | 600 | 1.2 | Reserved; no eyebrows above headings in the shipped UI |

Rules: weight is ternary (400/500/600); only `display-*` carry negative tracking; counts, durations and times use `tabular-nums`; markdown headings inside chat do not scale up; the transcript and the composer fill the main pane inside its gutters (24px desktop, 16px from 481px, 12px on a phone); only a user bubble keeps its own cap (88%/720px).

## Layout

### Shell
Full viewport, no page scroll: `grid h-dvh grid-cols-[264px_minmax(0,1fr)]` from 960px up. The sidebar is a **fixed 264px** and is not resizable, but it **collapses**: the UAM brand at the left of its header (or Ctrl/Cmd+B) animates the first grid column to 0 over `slow` (240ms) while the sidebar keeps its 264px inside an `overflow-hidden` column, so nothing inside reflows; once hidden it is `inert`, and the main pane takes the whole width. The state is kept per browser (`uam.sidebar`). While it is hidden, the same toggle sits at the start of the main pane's header (the Task header, the Settings header, or the placeholder's header) and brings it back. Below 960px the sidebar is a 300px drawer (a Base UI dialog sliding from the left, with backdrop) that the toggle in the pane header, and Ctrl/Cmd+B, open and close. The main pane is a column: header (44px, hairline below) → transcript (flex 1, `overflow-y: auto`, `overscroll-behavior: contain`) → composer (pinned); transcript and composer fill the pane between its gutters (see Typography rules), so collapsing the sidebar gives them the freed width. A lost connection also shows as a banner across the top of the main pane, above the pane's header. A redeployed service shows the same way, once, as a quiet `surface` strip ("UAM was updated." with an `accent` dot and a small secondary **Reload**): only while a draft, an upload or an open popup stops the page from reloading itself; never a toast, never repeated.

**Safe areas.** The page is an installable web app (`viewport-fit=cover`), so the shell pads all four sides with `env(safe-area-inset-*)`: the headers, the connection strip and the pinned composer keep clear of a notch, rounded corners and the home indicator, and the drawer and the side sheets, which are fixed to the viewport, pad their own top, bottom and outer edge. Nothing else reads the insets.

### Side panels (Changes, Subagents, Activity)
From 1280px a panel sits inline to the right of the column and is **resizable**: an 8px handle on its inner edge (`role="separator"`, `aria-orientation="vertical"`, `aria-valuenow/min/max`), drag with pointer capture, arrow keys step 16px (64 with Shift), Home/End go to the limits, Enter or double-click resets to the default. Width is clamped to 320px … min(880px, viewport − 264 − 480) so the chat column keeps at least 480px, and saved per panel in `localStorage` (`uam.panel.changes`, `uam.panel.subagents`, `uam.panel.activity`). The width is applied as the `--panel-w` custom property through CSSOM during the drag, so nothing re-renders per pointer move. Between 960 and 1279px a panel is a 440px overlay from the right; below 960px it is a full-screen sheet. Overlays and sheets are Base UI dialogs (focus trap, Esc, a backdrop that fades both ways) that slide in and out over `slow`; an inline panel commits the column's width at once and slides over the space it left, and slides out before it leaves. Overlays and sheets do not resize. One panel is open at a time.

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

Every level is cool ink (`rgba(20, 28, 45, α)`, the canvas is cool) in two layers: a 1px ring at 5–6% that makes the edge read crisp without a drawn border, and one soft shadow. The tokens are `--shadow-*` in `index.css` (`shadow-raised`, `shadow-float`, `shadow-modal`, `shadow-well`, `shadow-focus`, `shadow-focus-float`).

| Level | Treatment | Shadow | Use |
|---|---|---|---|
| 0 Flat | Surface step only | none | Sidebar on `rail`, assistant prose and everything else on `canvas`; activity and tool rows are `tint-well` pills at this level |
| −1 Well | `sunken` or `code-bg` fill | `well`: `inset 0 0 0 1px .05` | Inputs, selects, the segmented track, code blocks, the models checklist |
| 1 Raised | `raised` fill | `raised`: `0 0 0 1px .05, 0 2px 6px .07` | Task cards on the rail, the user bubble (on `bubble-user`), subagent rows, question blocks, Settings cards, the segmented thumb, a selected file or folder row |
| 2 Floating | `raised` | `float`: `0 0 0 1px .06, 0 6px 20px .10` | The composer, the pending interaction card, menus, context menus, popovers, selects' lists, the inline picker, tooltips (ink fill), a lifted card or thumbnail on hover |
| 3 Modal | `raised` + `backdrop` | `modal`: `0 0 0 1px .06, 0 20px 48px .18` | Dialogs, the command palette, the drawer, overlay panels, the lightbox image |
| Focus | any of the above | `focus`: `0 0 0 1px focus, 0 0 0 4px focus@18%` (`focus-float` adds `0 10px 28px .12`) | Fields, the sidebar search and, through `focus-within`, the composer |

A shadow is never transitioned. The composer's focus and every hover lift (`lift`) draw the deeper shadow on a pseudo-element at opacity 0 and fade it in; the element itself moves only on `transform`. In a long transcript only the user bubbles and the few cards carry a shadow; runs, tool rows and thinking are fills or pills.

**Scroll edges.** The transcript is never cut by a hard edge: the Task header (`pane-header`) has no rule and, once content has scrolled beneath it (an IntersectionObserver on a sentinel at the container's top sets `data-scrolled`; nothing runs per scroll event), fades in a 24px gradient of the canvas and a 5% ink tint below its edge; the composer's dock (`transcript-dock`) overlaps the transcript's last 40px with a gradient up to the canvas, so rows fade out beneath the floating composer. Both are small absolutely positioned pseudo-elements over the scroll area, not masks on it, and neither takes pointer events.

## Shapes

One scale of three: `sm` 6px for controls (buttons, inputs, selects, menu items, the segmented track, switches), `md` 10px for cards and popups (Task cards, subagent rows, question blocks, code blocks, menus, popovers, the inline picker), `lg` 14px for the surfaces that float over the pane (the composer, the pending interaction card, dialogs, Settings cards, the user bubble). `xs` 4px stays for what is under 24px tall (chips, inline code, the Project badge, a segment and the thumb inside a segmented control, the switch thumb). `full` is dots, spinners, the Send and Stop circles and the activity and tool-row pills.

## Components

Every component is Tailwind utilities on the tokens, in `web/src/components/`; the Base UI wrappers live in `web/src/components/ui/` (`button`, `menu` with `ContextMenu`, `dialog` with `AlertDialog` and `Sheet`, `tooltip`, `select`, `segmented` on the radio group). States are Base UI data attributes (`data-open`, `data-highlighted`, `data-disabled`, `data-starting-style`, `data-ending-style`) or ARIA (`aria-current`, `aria-pressed`, `aria-expanded`). Hover is one surface step; press adds `ink`.

### Sidebar
Sits on `rail`, 264px, no border to the main pane: the `rail` → `canvas` step is the seam, with a 1px `hairline` rule at the column's right edge that fades out over the top and bottom 12% (`rail-edge`).

**Header (44px).** Local Search at left, followed by the compact 16px UAM mark, the Project filter (a badge button), Add project and New task (a pen, Alt+N). The UAM mark toggles the sidebar without changing the selected Task or URL, retaining Ctrl/Cmd+B and focus restoration. The connection indicator lives in the footer. Search matches all entered words across Task name/title and real Project name/directory/branch within the selected Project filter. Results include active, settled and archived Tasks; clearing Search restores the active list and shelves. No backend search or PR lookups.

**Project filter (the badge button).** The header's badge button is the filter: the filtered Project's badge, or a `Layers` glyph in `muted` for all of them (T3 Code). It opens a 288px Base UI popover below it, the level 2 surface: a "Search projects…" box that takes focus (name and directory, every word), an **All projects** row, then one 30px row per Project with its badge and name and, at the right, a gear button named "Edit <project>" that opens Edit project once the popover has closed, so focus lands back on the badge. The current choice carries an `accent` check. The box keeps focus: arrows move the highlight (`aria-activedescendant` over `role="option"` rows), Enter chooses, Esc closes, a pointer over a row takes the highlight. Choosing a row closes the popover. A filter limits the flat Task list and lifecycle shelves to that Project, and is remembered per browser (`uam.projectFilter`); a remembered Project that no longer exists counts as no filter. The button is present whenever there is a Project.

**Project badge (16px).** The two characters at 8px in the UI font (Figtree), weight 700, with about 3px side padding in `on-primary` on the Project's tone (`bg-badge-<tone>`), `xs` radius, `aria-hidden` beside the name that names it. It leads each Task card's project metadata, the filter button and its rows, the New task palette's rows, the Task header before the title, the Edit project dialog's title, and the Remove dialog's name row.

**Flat Task list.** No project group headings or collapse controls. Tasks across the visible Projects are newest first. A Project is managed in one place, **Edit project** (the gear on its filter row): its name, with **Previous sessions** (import) and **Remove project** as secondary actions that open over it and land back on their button when closed. Task menus carry Task actions only. Adding a Project selects its filter. Project membership appears once per Task card, not as a repeated group heading.

**Task card (56px minimum).** Two compact lines: Project badge, name at 12px and real branch at 11px on the first line; then truthful status or relative last activity at 12px, the Task title at 13px, and the provider icon at right. Copilot uses the official 14px Primer Octicon with a GitHub Copilot accessible label and native tooltip; other providers keep their real names. The vendored SVG carries its pinned source and complete MIT notice. There is no invented PR count, model logo or elapsed-working duration from `updated_at`. The branch describes the Project checkout, not a per-Task worktree. Cards are `raised` on the `raised` shadow (level 1) without a border. Selected cards use `tint-selected` with the Project name and title in semibold (600); hover lifts the card 1px (`lift`: a transform, and the `float` shadow on its pseudo-element fading in), so the list reads as a deck. Keyboard focus retains its visible outline. Task-card actions live only in the context menu, opened by right-click, Shift+F10/Menu key or supported touch long-press. No visible action dots occupy the card. All lifecycle/context menu actions, F2/double-click rename, keyboard navigation and the pinned selected Task remain available. Search results label settled/archived state.

**Shelves.** Under the flat active Task list come **Settled** and **Archived** shelf headers (28px): the word and count in `caption` `muted`, a fading rule, a chevron. Collapsed by default; the state persists per shelf (`uam.shelves`). A collapsed shelf still shows the open Task pinned beneath its header. Shelf state is scoped to All projects or the selected Project. Archived Tasks open like any other, read-only.

**Keyboard.** Arrow Up/Down move between rows, Home/End jump, Enter opens, F2 renames, Shift+F10 or the context-menu key opens the row menu. Focus rings sit inside rows (`-outline-offset-2`).

**Footer.** A 28px **Settings** gear (`aria-pressed` while the Settings view is open) and the service version in `keycap` `faint` at left, **Log out** at right when a token is required. When the stream is not connected a banner row appears above it: `warning-wash` (reconnecting) or `error-wash` (offline) with the pulsing dot and the full sentence.

### Placeholder and loading states
With no Task open the main pane is a quiet placeholder: the mark at 36px and one `ui` `muted` line ("Open a task from the sidebar, or start a new one there."; without a Project, "Add a project in the sidebar to begin."). There are no buttons: New task and Add project live in the sidebar, and there is no empty-state screen (#185). When the sidebar is hidden or is a drawer, a 44px header above it carries the sidebar toggle, the brand and the connection dot. **Loading is never blank, and never the empty state.** The app knows, per data set, whether it has loaded at least once, and until then draws a **skeleton** (`Skeleton` in `common.tsx`): a few `sunken` bars in the shape of what is coming, with one highlight band sweeping over the group (`sweep`, transform only; the band is a pseudo-element on the group, so a skeleton is one animation, mounted only while loading, and still under Motion: Match system). A skeleton is a `role="status"` carrying its label for screen readers, its bars are `aria-hidden`, and the region around it is `aria-busy`. Only once a load has confirmed there is nothing does the empty state appear ("No projects yet…" with Add project in the sidebar, "Add a project in the sidebar to begin." in the pane, "New task in …" over an empty conversation, "No changes.", "No previous sessions…", "No project matches"). A failed load is an error line with **Retry**, not the empty state.

The flags: `state.loaded` (the first snapshot; before it the Projects and Tasks are unknown, the sidebar list is a deck of card-shaped bars and the main pane is the loading pane), `meta` with `metaError` (the catalogs: the composer's pickers hold a skeleton bar and Settings shows the New tasks, Utility model and Models cards as skeletons until `GET /api/meta` answers; a failure puts an error line with Retry in the Models card and `checkVersion` keeps the last catalogs once there are some), a Task's `history === 'loading'` with no items (a transcript skeleton in place of the "New task" intro), the Changes list and a file's diff (`null` until the reply), a subagent's transcript (`loading` in `state.agents`, Retry on failure), Previous sessions (`null` until the reply; Refresh retries), and the New task palette while `loaded` is false. The folder picker keeps its own loading and status lines inside the well. While a Task's detail loads and nothing was on screen before, the loading pane keeps the header's height over a transcript-shaped skeleton (a bubble at the right, then lines); when another Task was open it stays, inert, until the new one lands or the wait passes 600ms. Every other wait (a button's own request, the inline picker) is the `Loading` line (nothing for 300ms, then a spinner and a word) and whatever was loaded before stays in place. The in-browser mock takes `?mock&slow=<ms>` to hold the first snapshot and every reply that long, so each of these states can be looked at. A deleted Task explains itself and offers **Back to projects**.

**New task** is a command palette (T3 Code): a `sheet-wide` modal near the top of the screen (a bottom sheet below `sm`) without a title row, a back arrow and a "Search…" box that takes focus, a "Projects" group listing every Project as a 44px row with its badge, name and directory (`meta`, `muted`), a keycap hint **Alt+1…9** at the right of the first nine, and a footer of keycaps: ↑ ↓ Navigate · Enter Select · Esc Close. The filtered Project starts highlighted, else the most recently active one; the search box matches name and directory, and "No project matches" is the empty state. Enter, a click or Alt+digit choose; the palette closes, then the draft opens with its composer focused (the closing dialog leaves focus alone). With exactly one Project the pen opens the draft at once. Alt+N opens the palette from anywhere but a menu or dialog (Ctrl+N and Ctrl+digits belong to the browser). Choosing a Project opens a draft, not a Task: the pane shows a 44px header with the Project badge and `display-sm` "New task", the same centred "New task in …" lines as an empty Task, and the composer on the New tasks defaults from Settings, focused on a fine pointer. Nothing is created, listed in the sidebar or put in the URL until the first Send, which creates the Task with the chosen settings, sends the message with its `@` files and attachments, and opens the Task. Leaving the draft (another Task, Settings, a reload) discards it; its text is kept per Project in this browser (`uam.draft.new.<project>`), attachments are not. The `/` and `$` lists say commands and skills are available after the first message; the execution picker appears once the Task exists.

### Settings view
Not a dialog: a view in the main pane, `#settings` in the URL, opened from the sidebar footer's gear and closed by its **×**, by opening a Task, or by the gear again. Its header matches the Task header (44px, `display-sm` "Settings", the sidebar toggle first when the sidebar is hidden, a spinner while a change saves). The body fills the available pane with 16–24px gutters and one compact `muted` line ("Kept by the service, so they apply in every browser. This browser's own settings are at the end.") followed by **sections**, each a floating card (`raised`, `lg`, 20px padding, the `raised` shadow) with a `title` heading, 16px gaps within and between cards, and no lines between rows: rows are separated by their 8px vertical padding alone. A custom provider and the Add provider form sit in a `tint-well` block inside the Models card; the models checklist is a `raised` well. Models use compact rows in one column on narrow screens, two at 1280px and three at 1800px; provider headings span the grid. Names, IDs, costs, visibility state and 44px touch targets remain available. The header stays fixed while the body scrolls. A **row** is the label (`ui` 500 `ink`) with its help in `caption` `muted` on the left and the control on the right (stacked on a phone). The **Composer** section has one row, "While a task is running, Enter…", a **segmented control** (`Steer` | `Queue`, Steer the default) whose help names the other action's shortcut and button. **New tasks** holds what every new Task starts with (the same Model, Effort, Context size and Mode fields the composer's pickers set, `TaskDefaultsFields`, in a two-column grid capped at 576px, one column on a phone), saved as each field changes; Projects carry no defaults of their own. **Utility model** (the model UAM uses for its own small AI jobs, such as titling new Tasks) offers, for each provider with title support, "Cheapest (currently <model>)" as the default, the provider's own title (no AI), then each visible model with its reported prices; it is absent when no provider has title support. **Models** lists names, IDs, reported costs and visible/hidden switches; only a hidden row says so in words. Hidden current selections keep their label with "hidden in Settings" but do not appear as choices. Unknown hidden IDs stay removable. **This browser**, last, holds settings kept in `localStorage` rather than by the service: **Motion**, a segmented control (`Always on` | `Match system`, Always on the default; see Motion), and **Activity**, a segmented control (`Compact` | `Detailed`, Compact the default; see Transcript, Activity density), which an open Task follows at once. Controls disable while saving; stale responses cannot overwrite a newer save. A change shows at once and is saved through `PATCH /api/settings`; a refusal puts the old value back and an `error` Note says why.

**Segmented control** (`ui/segmented`, Base UI radio group): a 32px inset `sunken` track (the `well` ring) with 2px padding and `sm` radius; segments are equal columns, `xs`, at least 64px wide, `ui` 500 `muted`, the chosen one `ink`; under them one floating `raised` thumb (the `raised` shadow) slides to the chosen segment on its transform (`base`, snapping under Motion: Match system). The segment count and the chosen index reach the thumb as `--seg-n`/`--seg-i` through the CSSOM. Arrow keys move the choice; the group is named by the row label and described by its help.

**Switch** (`ui/switch`, Base UI): 32×18, an inset track (`faint`, an inner shadow; `accent` when on) under a floating 14px `raised` thumb with its own small shadow that slides 14px on its transform (`fast`). Off, the `faint` track is 3:1 against `raised` and the white thumb 3:1 against the track, so the control's boundary and state read without colour alone.

### Task header
44px, `canvas`, no rule; once the transcript has scrolled beneath it a fade appears under its edge (see Elevation, Scroll edges). Left: the sidebar toggle (when the sidebar is hidden, or the drawer toggle when narrow), the Project badge, the title (`display-sm` `ink`, truncating; double-click, the hover pencil, or Rename in the menu edits it in place with the same rules as the row), then the state chip (`StateMark` with the word; `Archived`/`Settled` as an outlined chip when read-only), a spinner while a lifecycle request is in flight, and the rename pencil, which takes no room until the title is hovered or it is focused. Right: the project strip, that is the branch (`meta`, `muted`, with the Project folder in its tooltip; hidden below 481px) and **Changes** (`FileDiff` + count, `aria-pressed` while open; label hidden below 481px), then **Activity** (`Activity` + the count of the Task's thoughts and tool calls, `aria-pressed` while its panel is open; present once there is one; label hidden below 481px), then **Subagents** (`Bot` + count, spinner while any run; label hidden below 481px), and the **"…"** menu with the same items as the row. The model and context details live in the composer. At 420px the title truncates and action labels shorten.

### Transcript
User turns are bubbles on the right (`bubble-user`, `lg` radius, the `raised` shadow, 88%/720px max, 88% on phone). Assistant prose is the page and stays flat. Provider and model labels are omitted from the main conversation; the model remains in the composer. Every message has a hover copy button and a right-click menu (Copy message). Items that arrive after the transcript mounted rise in (`rise`, 240ms); items present at mount appear at once; streaming text appends with no animation.

- **Activity density:** how a turn's work is drawn is a per-browser choice (Settings → This browser → Activity, `uam.activity`): **Compact**, the default, is the turn line, the promotion rules and the timeline below; **Detailed** is the activity row, thinking row and tool runs that follow, one activity row per run of work between two paragraphs, exactly as before. The subagent panel's transcript is always detailed.
- **Turn line (Compact):** the turn status row is the one line for everything the agent did in the turn: "Took 1m 2s · 5 thoughts (42s) · 3 commands · 2 files read ›" (`caption` `muted`, `tabular-nums`, truncated, never wrapped). After the duration come the counts in a fixed order: finished thoughts with their total time (a thought lasts until the next item), commands, files changed and files read (distinct paths), searches, subagents, other tools, questions answered and declined, requests decided; then what stays explicit: "1 failed" in `error` (only that part; the line stays `muted`), calls without a result, questions unanswered, images returned. A call still running and thinking that still streams are not counted; the live foot line names them. With anything folded the line is a button (`aria-expanded`, `aria-controls`; the screen reader hears "…, activity of this turn") that opens the turn's whole timeline in place through the shared height collapse, indented behind a fading left rule: each thought as its row (its text `muted` on its own click), each tool call as its row with its approval mark, details and images, each question block and decided request, then **Open in panel**. The timeline is mounted on the first open only, so a collapsed turn costs one button; the choice is per turn and per mount, not remembered. Live, the same line carries the working mark and "Busy for 12s" (the `role="timer"`), and its counts update in place as items append; it reads `attention` while a call waits for the user. Prose alone folds nothing: the row stays "Took 12s", as before.
- **Promotion (Compact):** nothing the person must see hides in the turn line. These stand in the answer at their natural place, exactly as their rows read inside a run: a failed call (its `error` tool row, which opens onto its output), a question once it no longer waits (the question block), a call whose result returned images (its tool row with the thumbnails under it), a subagent row, a steer bubble, a notice. A pending permission or question stays the action card under the transcript; a decided permission sits on its tool row in the timeline (the approval mark) or, naming no row, is a decided-request row there. A turn whose completed edit, write or create calls named files ends with one `caption` line, `FileDiff` + "Changed 2 files" (the paths in its tooltip) and **View changes**, which opens the Changes sheet; line counts are not known here. Routine reads, searches, successful commands and thoughts never interrupt the paragraphs.
- **Activity panel:** the Task's whole timeline in a side panel (`Activity` in the Task header, or **Open in panel** in an expanded turn; Esc closes it like the others). Under the header (the glyph, "Activity", the count) a row of filter buttons, `All · Commands · Files · Thinking · Failures`, each with its count (`aria-pressed`; ghost, `sm`, rectangular, since pills are only rows). The list groups entries by turn: a 28px `caption` head with the user message's first line (a button that scrolls the conversation to that turn and flashes its head row, `accent-wash` for 1.4s), its clock time and its recorded duration; then one 28px row per entry, `code-sm` mono for a call and `caption` for a thought, question or request: the kind's glyph in `faint` (`Terminal`, `FileText`, `Search`, `Brain`, `MessageCircleQuestion`, `ShieldCheck`, `Bot`, `Wrench`), the state mark, the label (name at 500, then the argument, the thought's first line, the question, the request's detail), the duration until the next item, and a hover **Show in the conversation** (`Crosshair`, always visible on touch) that jumps to its turn. A row is a button (`aria-expanded`) that opens onto the details, mounted on the first open: a thought's text `muted` (`md-quiet`), a call's approvals, input, output and images, a question block, a request's full resolution. Empty reads "Nothing recorded yet."; a filter with nothing reads "Nothing matches this filter.".
- **Activity row (Detailed):** everything the agent does between two messages (thinking, tool calls, the quiet rows of decided requests) folds into one 24px `caption` `muted` pill (`tint-well` fill, `full` radius, `tint-hover` on hover; no shadow, since a transcript holds many), closed by default: a chevron in a fixed 14px slot, then one line, truncated, never wrapped, such as "Thought 4×, ran 4 commands and read 2 files · 1m 4s" (finished thoughts counted, the tool-run phrases, the duration from the first item to the next item's start once known). Nothing that needs attention hides in it: while a call runs or thinking streams the working mark takes the chevron's slot and the label names it ("Running: bash npm test", "Thinking…"); a call waiting for permission reads "Waiting for your approval: …" in `attention` and its card stays under the transcript; a failure turns the row `error` with "1 failed"; images a tool returned are counted ("2 images"); a call that never reported is "1 without a result". The last run of the streaming turn stays closed too and its label updates in place, so nothing on screen moves while items append. It opens through the shared height collapse onto the rows below, indented 22px, each with its own disclosure. Assistant prose, user bubbles, notices, question blocks and subagent rows stand between the runs, never inside them: a subagent row carries its state and **Open**, which must stay in view.
- **Thinking:** thoughts are metadata, never paragraphs: in Compact they are a count and a total time on the turn line, and their text appears only in the expanded timeline or the Activity panel, `muted`; in Detailed each is one 24px row inside its activity row (`ui` `muted`, a chevron): "Thinking…" shimmering while it streams, "Thought for 12s" once the next item's timestamp is known. Nothing of the text shows while the row is closed, so streaming never resizes it; a click opens the text (markdown whose headings stay `muted` too, `md-quiet`, behind a 2px fading left rule, with Copy thinking) through the shared height collapse. Empty reasoning items are not drawn. The choice is remembered per item for the browser session.
- **Tool runs (Detailed):** consecutive tool calls form one compact disclosure (a `tint-well` pill like the activity row, a button over the shared collapse, never a native `<details>` snap), such as "Changed 1 file and ran 3 commands"; its tool rows are indented behind a fading left rule and each row is a transparent pill that fills `tint-well` on hover. A tool row whose title repeats its name as the verb ("Edit cmd/doctor.go" for `edit`) shows the rest only. Counts use actual successful known tool operations, with distinct known file paths; failures, running calls and calls without a result remain explicit. Unknown tool types count as tools, never inferred shell subprocesses. Opening the summary retains every original tool row, full input/output, copy actions, approvals and images. Tool runs and thinking rows sit inside their activity row; assistant prose, questions and subagent rows stay outside it.
- **Turn status:** one 34px row, with no rule under it (nothing full-width separates turns; the user bubble and the whitespace do), heads every assistant turn, under the user bubble. Live, it carries the working mark and "Busy for 12s" from the recorded foreground start (steering does not reset it; without a trustworthy start, "Busy"; the screen reader hears "Busy" once); ended, the same row reads "Took 12s" from the recorded duration, or stays blank when none was recorded. In Compact the same row is the turn line: the counts follow the duration and the row opens the timeline. "Working" stays the Task state's name (sidebar row, header chip); the turn row does not repeat it. Streamed content lands below it, so nothing on screen moves when a turn starts or ends. Background shells remain independently visible above the composer and never drive foreground timing.

- **Approval mark:** a decided permission request sits on its tool row, right-aligned: a 12px shield (`ShieldCheck`, `ShieldX` for denied, `Shield` for expired) in `faint` and one `caption` word in `muted`, "auto" for yolo, "allowed" or "denied" for a person, "expired". When several requests name one call the mark shows the latest word and "+n" in `faint`; the Base UI tooltip and the accessible name carry every request's full "title · state · resolution", latest first. It is never a line of its own. A decided request that names no tool row is a 24px row of the same kind in its turn at its time: glyph, title in `ui`, the request's first line in mono, the word at the right.
- **Question block:** a question renders as a `raised` `md` card on the `raised` shadow, from its interaction (any provider: text, header, choices, state, resolution) and, for Copilot's `ask_user`, from the tool's input and output, so it reads the same after a reload or a restart: a `caption` "Question" head with the `MessageCircleQuestion` glyph ("· Waiting for your answer" in `attention` while pending), each question as markdown in `body` with its choices as a list, a `success` check on the chosen ones and hollow dots on the rest, then a fading rule and "You answered" in `caption` with the choice or the typed text in `ink`. Declined reads "You declined to answer."; a call left open by a restart or a stopped turn reads "No answer."; a failed call reads "Failed: …" in `error`. It sits on the tool row that asked, or stands alone in its turn when no row did. While the question waits it is drawn once, as the action card below the transcript; the block appears once it is answered, declined or left.
- **Tool images:** the images a tool returned sit under its row, indented to the text, visible when its run is expanded, without opening the individual tool details: the same thumbnails and lightbox as transcript attachments (160px, `sunken`, hover lifts and scales 1.02, at least 44px square on coarse pointers); `images_note` follows as a `caption` `muted` line. The row appears first without them and gains them when UAM has stored them.
- **Code block:** `code-bg` with the inset `well` ring (no border, no rule under the header), `md` radius, a 24px header with the language (or `code`) and a copy button; right-click offers Copy code. Max height 480px with internal scroll; never wraps.
- **Code highlighting:** a block with a language gets `hljs-*` spans in five tones on `code-bg`: comments `muted`, keywords `accent`, strings `success`, numbers `attention`, names and types `warning`, titles `ink` at 500; added and removed diff lines use the diff tokens. Nothing italic, nothing outside the ladders. The grammars load on the first such block.
- **Diagram card:** a fenced `mermaid` block in the code-block chrome. The header reads `mermaid`, shows a spinner while the frame renders, then a Diagram / Code toggle (two 20px ghost buttons, `aria-pressed`) before the copy button, which copies the source. The diagram is an `<img>` on the well, centred at its intrinsic size up to the block width and 480px, and fades in (`base`); clicking it opens the lightbox (the attachments' dialog). While the fence is still streaming the code shows; when Mermaid rejects the source the code stays, with a `caption` note under it ("Diagram could not be rendered: …"). Inside the diagram the colours are tokens (`raised` nodes, `hairline-strong` borders, `muted` lines, `accent-wash` secondary, `warning-wash` notes) in Figtree: neither the sandboxed frame nor an SVG image can fetch the page's font, so the frame bundles the Latin file, measures with it and embeds it in each SVG.
- **Subagent row:** a `raised` `md` card on the `raised` shadow with the `Bot` glyph, the name, the status chip, model, duration and **Open**; right-click offers Open and Copy agent ID. Under the name, one `caption` `muted` line, truncated with the full text in its tooltip, says what the subagent is doing or what it reported, from real data only: while it runs, its latest step ("Running: bash npm test" from the last tool call's label, "Thinking…" while a thought is its latest item), from its own transcript once its panel was opened and before that from the latest live frame the browser keeps per subagent (kind and call only, never output), so after a reload the line stays absent until its next step; failed, its error; completed or idle, the first line of its report (the `task` call's result) as plain text, markdown marks dropped and a leading heading skipped when a body follows it; cancelled, the report line if one exists. The line grows in and folds away through the height collapse and keeps its last text while it folds; at 420px it sits under the name at full width, before the chips wrap. Nothing else of the subagent's output renders in the main column.
- **Interaction card (pending):** `raised`, `lg`, the `float` shadow (it is the one thing in the transcript that asks for a hand); a `chip-attention` with the `ShieldQuestion`/`MessageCircleQuestion` glyph heads it; the request in a `sunken` mono well; buttons right-aligned (Deny `danger`, Allow for task `secondary`, Allow once `primary`; stacked full-width on phone). Once decided it collapses in place (its last pending look, inert, over `slow`) and its record moves to its tool row (the approval mark) or its turn (the quiet row); nothing above it moves.

### Composer
The floating control plane: `raised`, `lg`, the `float` shadow, detached from the pane's foot by the gutters (16px below) and overlapping the transcript's last 40px, which fades out beneath it (the dock's gradient). Focus-within fades in the `focus-float` shadow on its pseudo-element: one step deeper and neutral, with no accent edge and no glow; the caret shows where typing goes. One surface: the textarea (`chat`, `field-sizing: content`, 56px to 40dvh) above one control row, which never wraps. From left: **Attach**, then the pickers; at the right the actions, with the primary button always last so Stop and the other action rise in beside it and nothing else moves. On a phone the effort/context, permissions and execution pickers fold into a **More** (`Ellipsis`) menu that holds the same groups, the credits become a `Coins` glyph and the cost estimate moves into their popover; the row stays one row at 420px.

- **Pickers**: Model (`Cpu`, value in `caption`; its tooltip adds "Latest turn ran on ‹model›" when routing differed), combined Effort and Context (`Gauge`, e.g. "high · 200K"; it names only what can change, and for a model that fixes both it reads "Default", disabled, with the reason in its tooltip), and Permissions (`Shield`; `ShieldOff` in `attention` for yolo), separated by hairlines. The combined menu has two named sections; unsupported choices show their reason. Model/effort/context changes stay disabled during a turn. Permissions can change during a turn. Read-only values remain visible and cannot change.
- **Context ring:** a 16px ring after Model, absent until usage is reported. It is `accent`, `attention` at 80%, and `error` at 95%. Its accessible name, tooltip and click/tap popover carry used/limit/percentage; the popover also shows reported prompt/cache counts.
- **Credits:** only when the provider reports usage. A quiet chip shows remaining allowance, `attention` at 20% and `error` at 5%; the popover shows used/entitlement, a future reset date, Task AI units and stale state. The adjacent input-cost estimate uses current context, cache-read prices where known, and long-context pricing where applicable. It excludes output and stays absent when prices or context are unknown. Model choices carry the same estimate and reported cost tier/discount.
- **Project strip:** lives in the Task header (branch, then the Changes opener with its count); the composer carries none. The Changes opener remains available on read-only Tasks, and closing Changes returns focus to it.
- **Actions** at right: while a turn runs, **Stop** (a round ink button like Send, square glyph; red is for errors and destructive confirmations, never a control at rest), then the other action as a labelled secondary button and Enter's action as a round primary button whose glyph cross-fades in place: with the default setting (Steer) that is **Queue** (secondary, `ListPlus`) then **Steer** (primary, `Zap`); with Queue it is **Steer** (secondary, `Zap`) then **Queue** (primary, `ListPlus`). Otherwise **Send** (round primary, `ArrowUp`). Tooltips carry the shortcuts and follow the setting: Enter does the primary, Ctrl+Enter the other, Shift+Enter adds a line. Up from the first line (or an empty textarea) recalls the Task's previous prompts newest first, shell-style, with the caret at the end; Down from the last line comes forward and, past the newest, restores the draft, as does Escape. Editing a recalled prompt makes it the draft; chips and uploads are untouched. An open inline picker takes Up and Down first. When a steer is impossible (the message carries files or attachments) Enter queues, the primary is Queue, the Steer button stays, dimmed, with its reason, and a note above the textarea says why. Read-only Tasks show no send controls.
- Notices (read-only, uncertain, rejected, errors) sit in a strip above the textarea, separated by spacing alone; the queue is a disclosure strip with numbered rows, a per-row cancel, Resume and Clear.
- **Inline pickers:** typing `/` or `$` as the first character or `@` at the start or after whitespace opens a level-2 listbox above the textarea, full width on phone and 440px from `sm` up, max height min(300px, 40dvh). Rows are 30px (44px on touch); `/` groups rows under Commands and Skills, `$` offers only Skills, and `@` lists files and folders in `caption`. Focus never leaves the textarea, which drives the list through `aria-activedescendant`: Up and Down move, Enter or Tab picks, Escape dismisses. Loading, empty, and reason lines (for example, an unavailable provider catalogue) sit inside the popover.
- **Command outcomes and execution:** alias search and literal argument choices reuse the inline picker. Disabled commands carry a reason and never fall through as prompts; catalogue failures hold slash input with Retry commands. Command results use a compact composer strip for text or explicit subcommand choices, or open existing controls. Execution mode appears beside Permissions in the composer toolbar; its popover shows objective state, reported turns, credits, limits and reasons. Unknown observations stay qualified. Stop remains available during autonomous continuation, including between turns, and never optimistically claims completion. Safe/Yolo remains the separate permission policy.
- **Chips row:** picked `@` files and uploads sit in a wrapping row above the textarea. An upload chip is a 44px `sunken` well holding a 36px thumbnail (or the kind's glyph), the name, and its state: `Uploading… 42%` with a 2px `accent` bar along the bottom edge, size and kind when done, or the refusal in `error` on an `error-wash`. A file-reference chip is a smaller chip with `@`. Each chip has a remove button. Queued prompts repeat these as 20px caption chips.
- **Attach:** a subtle `Paperclip` icon button opens the control row at the left. Its tooltip gives the model's media gate and the limits (images 3 MiB, PDF 10 MiB, text 256 KiB, 5 per message). It never disables for the model, since text is always allowed: the gate narrows the file picker and the tooltip names it. Once a message holds the most uploads it can carry, the button stays visible, dimmed, `aria-disabled`, and says why. Read-only Tasks show no composer controls, so no Attach button. Paste and drag-and-drop also work; while files are dragged over the composer, a `raised` overlay with an inset dashed `accent` outline shows "Drop to attach" and the same note (`fade-in`).
- **Transcript attachments:** a user turn's images render as thumbnails up to 160px high; hover lifts the shadow and scales the image to 1.02. Clicking one opens the lightbox: no card, the image (max 94vw by 1400px, 78dvh, `sunken` fill for transparent images, `modal` shadow) on the see-through `scrim`, its name and details in `on-primary` above with the close button, **Open original** below; a press outside the image closes it, and it scales from 0.98 and fades (`slow`). Other files are 32px chips with glyph, name, and size that open the stored copy; without a stored copy the chip is inert. A PDF the provider did not pass to the model as a document (`not_native`, from the provider's own record of the message) adds one `caption` `warning` line with `TriangleAlert` under the chips: the agent got the file to read with the tools on this machine instead, which can miss scanned pages, images and layout, or fail without a PDF text tool. Images, text files and a PDF the model received get no line.
- Phone: the control row stays one row (More menu, glyph faces); controls have 44px coarse-pointer targets and the textarea is 16px.

### Menus, context menus, tooltips, selects, dialogs
All Base UI. Menus: `raised`, `md`, 4px padding, the `float` shadow (its ring is the edge), 30px items (44 on coarse pointers), highlighted item one surface step, destructive item in `error` with `error-wash` highlight, disabled items dimmed with their reason in `caption` beneath, groups separated by a fading rule. They scale from 0.97 and fade over 100ms from the transform origin, as do popovers, the selects' lists and tooltips. Context menus open at the pointer (long-press on touch) and hold exactly the items of the matching "…" button. An item's action runs once the menu has finished closing, and an action that takes focus itself (inline rename) tells the menu to leave focus alone (`takesFocus`), so the menu's focus return never undoes it. Tooltips are `ink` on `on-primary`, 400ms delay, no delay while another is open; they are hints for sighted users and never the only name of a control. Selects (project dialogs, Settings) are filled `sunken` triggers like inputs and open below with a check on the chosen row. Dialogs: `raised`, `lg`, 20px padding, 440px max (560px, `--spacing-sheet-wide`, while Add project browses folders), the `modal` shadow on the backdrop, scaling in from 0.97 with the backdrop's fade; a close button in the title row; on phone they become a bottom sheet with full-width buttons. A dialog is never taller than the viewport less its 16px gutters: the title row and the footer (Cancel, the primary) stay and the body scrolls between them. Confirmations (`AlertDialog`) focus **Cancel** first; Archive and Delete always confirm.

### Confirmations
Every destructive action goes through an `AlertDialog` (`ui/dialog`; the state of one confirmation is `useConfirm`, which keeps its target through the exit so the copy never changes on screen). Destructive means data is lost or a running process is killed: **Delete task**, **Remove project**, **Remove provider** and **Remove model** (Settings, custom models), **Cancel** a queued prompt and **Clear** the queue (their text is not kept; the dialog quotes the prompt), **Stop** a background task (it kills the shell; the dialog names the command) and **Stop** a subagent (its work ends; the main agent gets no result). **Close conversation** and **Archive** confirm too, since neither can be undone from the web interface. The copy is one shape: the title names the verb and the object ("Remove provider ollama?", "Stop subagent “Survey templates”?"), the description is one line on the consequence, the confirm button carries the verb in `danger` (a `sunken` fill), and **Cancel** (or **Keep**, where "Cancel" would be the action itself) takes focus first, so Enter never destroys by accident; Escape cancels. Nothing else confirms: **Stop turn** and Esc stay instant (they only interrupt, and the Task can continue), a hidden model is a switch, and **Log out** is a click.

### Folder picker
The Add project dialog's path field keeps a **Browse** button (`FolderOpen`, secondary, `aria-expanded`) beside it, and under the field a **Recent folders…** select (the shared Select) fills the field with a recent directory. Browse opens the picker inline under the field through the shared height collapse while the dialog widens to 560px over `slow`, so the dialog grows instead of jumping, and settles back once a folder is chosen. Nothing floats: the picker is a column of rows in the dialog (`components/FolderPicker.tsx`).

- **Breadcrumb.** The folder being shown as `caption` segments, root first, each a 24px button (44 on coarse pointers; `muted`, the last `ink` with `aria-current="location"`), `/` separators in `faint`, wrapping when deep. At its right, flush with the well's edge, a **Show hidden** toggle (`EyeOff`/`Eye`, ghost, `aria-pressed`) that lists the folder again with dot-folders (`hidden=1`).
- **The well.** A `canvas` well (`sm`), fixed at `--spacing-picker` (min(320px, 40dvh)) so moving between folders never changes the dialog's height; on phone `--spacing-picker-phone` (40dvh), so the Name field and the sticky footer stay reachable. Inside, a `role="listbox"` whose only children are the `role="option"` rows: a `Folder` glyph, the name in `ui`, and marks in `caption` `muted` for a git repository (`GitBranch` + "git") and a symbolic link (`Link` + "link"); hidden names are `muted`. Rows are 32px (44 on coarse pointers); hover is one step up (`surface`), the selected row is `raised` with `ink` and the 1px shadow, the same as a selected sidebar row. Each row is one button; the `ChevronRight` at its end is a 32px (44 on coarse pointers) hit region of that button, not a control of its own: a press there opens the folder. Status lines (loading, empty, error, "Showing the first 1,000") sit in the well above or below the listbox, never inside it.
- **Selection is the target.** A click selects, a double-click or the chevron opens. **Use this folder** takes the selected path, else the folder being shown; the path it will take sits beside the button in `caption`, truncated at its start so the folder's own name stays visible. Navigating clears the selection.
- **Footer.** **Up** (`FolderUp`, disabled at `/`) and **New folder** (`FolderPlus`) at left, **Use this folder** (secondary; the dialog's primary stays **Add project**) at right; full-width buttons on phone. New folder adds a row at the top of the well, with no rule beneath it: the standard text input and Create/Cancel icon buttons. Enter creates the folder and, if the user is still in that folder, selects it (a dot-name turns Show hidden on); the new path is the target at once, before the list refreshes. Escape cancels; errors sit under the row in `caption` `error`.
- **Keyboard.** Focus rests on the listbox (`aria-activedescendant`): Up and Down move, Home and End jump, Enter opens the selected folder, Backspace goes up, typing jumps to a name, Ctrl/Cmd+Enter anywhere in the picker means Use this folder. Escape cancels the New folder row first, then closes the picker; the dialog stays open.
- **Quiet states.** Loading shows the `Loading` line only before the first listing; afterwards the previous rows dim while the next folder loads. An empty folder says "No folders here". A folder that cannot be listed keeps its breadcrumb and Up and says why in `caption` `muted`: "You don't have access to this folder" for 403, "This folder does not exist" for 404. A path in the field that is not a folder starts the picker at home instead.

### Buttons and inputs
Primary is ink; secondary is a filled `sunken` surface with no edge (`tint-hover` on hover, `hairline` pressed); ghost has no fill; danger is `error` text with `error-wash` hover; icon buttons are 28px (32 in headers) `muted` glyphs. Disabled is 45% opacity. Inputs and select triggers are 36px filled `sunken` surfaces with the inset `well` ring and no edge; focus lifts the field: it turns `raised` under the `focus` shadow, with no coloured edge, and the caret shows where typing goes; select triggers keep the 2px keyboard ring. `aria-invalid` draws a 1px `error` ring. Labels sit above in `caption` `muted`. On WCAG 1.4.11 and 2.4.7: a field is identified by its label, its placeholder or value and its caret rather than by a drawn edge, and its focus by the lift and the caret, not by a 3:1 edge (the fill steps 1.12:1 from `sunken` to `raised`); a secondary button is identified by its label. The owner chose this over a coloured focus edge on 2026-09-25; it is a deliberate trade for the borderless surface and is recorded here.

### State marks
| State | Mark | Row word | Chip |
|---|---|---|---|
| working / starting | the working mark: a 14px `accent` glyph, a 2px-radius core breathing (opacity 1 → 0.45) inside a 25% ring while a 1.5px satellite orbits it, both 1.6s and in step (`orbit` linear, `breathe` ease-in-out); still under reduced motion (Motion: Match system) | Working / Starting | "Working" `accent` |
| needs permission / answer | 8px `attention` dot | Approval / Input | `chip-attention` with the full label |
| completed | check `success` | Done (unread only) | "Completed" `success` |
| cancelled | dash `muted` | Stopped (unread only) | "Cancelled" `muted` |
| failed | cross `error` | Failed | "Failed" `error` |
| interrupted | pause `warning` | Paused | "Interrupted" `warning` |
| idle | dashed circle `faint` | time | "Idle" `muted` |
| closed | hollow dot `faint` | Closed (unread only) | "Closed" `muted` |

Only the attention chip has a fill. Chrome icons are lucide, 12–16px, one stroke weight. The Copilot provider mark uses its official Primer SVG. The working mark is one component (`WorkingMark` in `common.tsx`) and the one sign of agent work in progress: the sidebar rows, the Task header, the transcript's live foot line, the subagent "Running" chips and the Subagents header button, and the background tasks' "Running" chips and their "n running" line all move alike. The plain spinner means UAM itself is waiting on a request; tool rows keep it too, since a single tool call in flight is not the Task working. While a turn runs, a live line with the mark and one calm gerund ("Untangling…", "Sifting…"; the list is `lib/verbs.ts`) sits at the foot of the transcript, where new output lands, unless the last row is already live (in Detailed, a streaming thought or a running call's activity row says "Thinking…" or "Running: …"); in Compact no row is live, so the foot line itself names the current step, "Thinking…" (shimmering), "Running: bash npm test" or "Waiting for your approval: …" in `attention`, and falls back to the verb between steps; it grows in and folds away through the height collapse. The verb is chosen once per turn by hashing the id of the user message that began it, so it never cycles or flickers and reads the same after a reload; it is never "Working", "Thinking" or "Running", which name other things. It moves down as prose streams above it; nothing above it moves.

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
- **Every disclosure** (shelves, turn lines, activity rows, tool runs, tool rows, thinking, the Activity panel's rows, background tasks, the folder picker, a decided card) opens and closes through one component, `Collapse`, which animates `grid-template-rows` 0fr ↔ 1fr (`slow`); collapsed content is `inert`, and a closing owner stays mounted until `onClosed`. A turn line, activity row, tool run, tool row and panel row mount their content on the first open (`appear`), so the closed transcript carries none of the hidden code blocks. Chevrons rotate (`base`). Nothing uses a native `<details>` snap.
- **Rows** step surfaces in `fast`; the meta text and the "…" button cross-fade in place; inline rename swaps without layout shift. A **state change** fades the new state glyph in over the old one's 16px slot and steps the status word's and the chip's colour (`base`, `StateMark` and `Chip`), so a row turning from Working to Needs permission changes without a snap.
- **Buttons:** the filled ones (primary, secondary, danger; an action) press to 0.97 (`fast`); ghost and subtle buttons only tint.
- **Lift** (`.lift`): Task cards and image thumbnails rise 1px on hover (transform, `base`) while the `float` shadow drawn on their pseudo-element fades in (opacity); the shadow itself never animates. **Switch** and **segmented** thumbs slide on their transform (`fast` / `base`). The composer's deeper focus shadow fades in on its pseudo-element (`base`).
- **Menus, context menus, pickers, selects, popovers and tooltips** scale from 0.97 at their transform origin and fade (`fast`); **dialogs** scale from 0.97 and fade with the backdrop (`slow`); the **drawer** slides from the left (`slow`); overlay panels slide in from the right and back out, with the backdrop fading both ways; inline panels slide over their committed space and slide out before they leave. A tooltip that is not open never closes on its hover path: a view transition's snapshot makes the browser report the pointer leaving a hovered trigger, and Base UI's hover close flushes synchronously, which would cancel the transition (`Tip` cancels that close, so "New task" under the pointer still cross-fades).
- **Task switching and the Task list:** React's `<ViewTransition>` with typed transitions. Selecting a Task and the snapshot that lands it ("switch") cross-fade the pane (`base`); Task list changes ("sessions") let rows rise in, fade out and slide to their place (`slow` for the move, `fast` for the cross-fade). The page root is `view-transition-name: none`, so nothing else snapshots and streamed text keeps painting live; every other render leaves rows to their CSS colour transitions. The first load of the list is a skeleton; the list fades in (`base`) once it fills.
- **Transcript:** new rows rise in, tool rows one by one as they arrive; streaming text appends without animation; the turn status row keeps its slot from "Busy" to "Took", and in Compact its counts change in place; "Thinking…" shimmers `muted` → `body`; the working mark orbits and breathes; the locate actions (a subagent's row, a turn's head from the Activity panel) flash `accent-wash` for 1.4s; a rendered diagram fades in (`base`) over the code it replaces; the "New output" button appears and leaves through `Appear`.
- **Appear** (`ui/appear.tsx`) is the one enter/exit for a small control in a fixed slot: it fades and scales from 0.9 (`base`), stays mounted and inert through its exit, and takes the control's place in a row so nothing beside it moves.
- **Notes and strips** (`Note`, the connection, update and error strips at the top of the pane) fade in (`base`); nothing slides.
- **Composer:** Stop appears beside the fixed primary when a turn starts and leaves when it ends (`Appear`); the queue strip opens and closes through `Collapse` (`slow`), holding its last list through the exit, and never grows in when the composer mounts with one; "Resend last prompt" rises in; the send button presses like every primary.
- **Reduced motion:** honoured only when Settings → Motion is **Match system**; the default, **Always on**, animates even when the OS asks for reduced motion (Windows with animation effects off, Remote Desktop). The choice is kept per browser (`uam.motion`) and applied as `data-motion="system"` on `<html>` by the entry module before the first render and at once when changed. Then `@media (prefers-reduced-motion: reduce)` disables every transition and animation, view-transition pseudo-elements included, and the `motion-reduce:` variant (redefined with `@custom-variant` to require the attribute) applies; the working mark stands still, disclosures, panels and the sidebar snap, and every exit still reports on its timer so nothing lingers.

No motion library; everything is CSS transitions and keyframes through Tailwind (`animate-rise`, `animate-fade-in`, `animate-slide-in`, `animate-pulse-dot`, `animate-spin`, `animate-shimmer`, `animate-sweep`, `animate-flash`, `animate-orbit`, `animate-breathe`) and two presence components, `Collapse` and `Appear`. Only `transform`, `opacity`, colours and `Collapse`'s `grid-template-rows` animate: never a shadow, a filter, a size or a position; nothing reflows the transcript per frame, no moment adds layout shift, and no surface uses `backdrop-filter` (remote desktops).

## Accessibility

- **Focus:** a plain ring, never a glow. `:focus-visible` is the 2px `focus` outline, 1px out, with no halo; inside rows the ring is inset. Text fields replace the outline with a lift (`raised` fill and the `focus` shadow), never a coloured edge; select triggers keep the ring; the composer shows focus as its deeper neutral `focus-float` shadow and the caret. Base UI traps focus in dialogs and the drawer and returns it to the opener.
- **Keyboard:** the sidebar as described; Ctrl/Cmd+B hides and shows it (the drawer on a narrow screen), and focus follows the toggle so nobody is left on an inert element; Alt+N opens the New task palette, where arrows, Enter, Esc and Alt+1…9 work as in the filter dropdown; Esc closes the topmost popup, then a panel, then the Changes sheet; Enter/Shift+Enter/Ctrl+Enter in the composer, following the send default; arrow keys on the panel handle and the segmented control; in the folder picker, arrows, Enter, Backspace, type-ahead and Ctrl/Cmd+Enter as described.
- **Names:** every icon button has an `aria-label`; pickers put the value and any reason in theirs; the meter has `aria-valuetext`; the connection dot has `role="status"` text.
- **Live regions:** the transcript is `role="log"`, so new tool rows announce themselves; errors are `role="alert"`. A tool row's accessible name is "name argument, state" plus its approval's full resolution; a chosen answer is marked "(chosen)".
- **Targets:** ≥44×44 on coarse pointers (rows, buttons, menu items, pickers, choice rows).
- **Colour is never the only signal:** every state has a glyph and a word; add/del rows carry `+`/`−`.
- **Zoom:** the layout holds at 200% (the sidebar becomes the drawer under 960px).

## CSP constraints

The server sends `default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; font-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'`. Consequences:

- **No `<style>` elements at runtime.** This is why the primitives are Base UI: it creates none (its scroll lock and positioning go through CSSOM), and the app wraps itself in `<CSPProvider disableStyleElements>` so the two parts that could render one (Select and ScrollArea) never do. Radix was rejected because its modal layers inject `<style>` through react-remove-scroll.
- **No inline `style=""` markup.** Dynamic values that cannot be classes (the panel width, the meter fill, Base UI's popup position, React's `view-transition-name`) are set through the CSSOM (`element.style.setProperty`, React's `style` prop), which `style-src` does not govern. The browser checks record zero `securitypolicyviolation` events, verified for the view transitions against a built bundle served with the header.
- **No inline scripts, no `eval`.** Vite emits external chunks only.
- **Fonts and icons from `'self'`:** fontsource woff2 files hashed into `/assets/`; icons are lucide SVG in the markup.
- **No service worker.** The app manifest (`/manifest.webmanifest`, served as `application/manifest+json`, `no-cache`) and the icons are enough for Chromium to offer install (`Page.getInstallabilityErrors` is empty on desktop and mobile); the interface is a live view of the service, so nothing is cached for offline and no worker sits between the page and `/api/*` or the event stream. Updates come from the service's version in `/api/meta` (see Layout, Shell).
- **Mermaid runs in a sandboxed frame, not in the page.** It cannot render without a `<style>` element and `style` attributes, so it lives in `/diagram-frame.html`, embedded as `<iframe sandbox="allow-scripts">` (opaque origin: no cookies, no storage) and served with its own policy: `default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src data:; font-src 'self'; connect-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'`. The page posts the source, gets the SVG string back and shows it only as a `data:` image, which `img-src` already allows. The page's own policy is unchanged, and the frame's script is a classic script, since a module script from an opaque origin needs CORS (ADR 0004, "Diagrams in a sandboxed frame").

## Do / Don't

### Do
- Step surfaces by one shade and one elevation level to show depth; a seam that must still be a rule fades out at its ends.
- Keep the assistant flat and give the user the bubble.
- Reserve `attention` for states that need a human: a dot, a row word, a chip. Never a button fill.
- Use a badge tone only inside a Project badge; a tone never stands for a state.
- Set code in the conversation (code blocks, inline code, tool rows, request commands) and diffs in JetBrains Mono at 11–13px; everything else, identifiers included, in Figtree.
- Truncate rows; wrap nothing there. Put every context-menu action on a "…" button too.
- Collapse turn lines, activity rows and thinking by default, collapse the ledger once everything is done, keep the shelves collapsed.
- Keep a turn's paragraphs uninterrupted: thoughts and routine calls are counts on the turn line; only a failure, a question, a permission, an image, a subagent or the turn's edits stand in the answer.
- Let the transcript and the composer fill the main pane; only the gutters and a user bubble's own cap bound them.

### Don't
- Don't draw a border at all: a surface has a ring in its shadow, a field is filled, a seam fades. A card's children use indents and sunken wells.
- Don't add a coloured primary button; the primary is ink.
- Don't use pills for controls. The composer Send/Stop circles and the transcript's activity and tool rows are the explicit exceptions; other controls keep the shared rectangular shapes. Don't fill a control at rest with `error`: Stop is ink.
- Don't use a serif, weights outside 400–600, an eyebrow above a heading, or scaled markdown headings in chat.
- Don't fill chips except the attention chip.
- Don't show heuristics or previews in rows; rows show server state.
- Don't put a colour on a ready row; ready is a time.
- Don't put the transcript or the composer back in a centred fixed-width column, or make the sidebar resizable; it is fixed or hidden, nothing between.
- Don't animate a shadow, a filter or a size, add `backdrop-filter`, mask a scroll container, or add per-row shadows to a long list; lift and deepen shadows through a pseudo-element's opacity, never add a coloured glow, and fade scroll edges with small overlays.
- Don't put New task or Add project in the main pane; the placeholder stays quiet.
- Don't show an empty state before its data has loaded once (a skeleton until then), and don't run a destructive action without its confirmation.
- Don't use `<style>` elements or `style=""` markup; classes first, CSSOM custom properties for measured values.
- Don't inline provider SVG or HTML; a diagram is a `data:` image rendered in the sandboxed frame.

## Iteration guide

1. Add a token before you add a value: colours, sizes, radii, shadows and the easing live in `web/src/index.css` under `@theme`, mirrored here; durations are the bare `duration-100/160/240` utilities. A new surface picks a level from the elevation ladder; it does not get a border.
2. Add a component here before adding one there; use the Base UI wrapper in `components/ui/` rather than a raw primitive.
3. A new state extends the State marks table; do not invent a fifth semantic colour.
4. Check contrast for every new text/surface pair before merging.
5. Screenshot at 420×900 and 1440×900 for any change to layout tokens; keep `securitypolicyviolation` at zero.

The Changes panel shows the workspace scope as "All uncommitted project changes
vs HEAD." The line wraps rather than truncates; it does not claim Task-only
ownership. Provider session-scope labels also wrap. File-row status columns fit
their text, including "untracked", before the truncated filename; touch menu
controls reserve 44px without covering the change counts.
