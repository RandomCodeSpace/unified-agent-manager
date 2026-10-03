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
  sidebar-width: 320px
  sidebar-rail-width: 48px
  drawer-width: 300px
  header-height: 44px
  chat-gutter: 24px
  chat-gutter-phone: 12px
  panel-default-width: 440px
  panel-min-width: 320px
  panel-max-width: "min(880px, main pane − 480px)"
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
  subagent-card:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.ink}"
    typography: "{typography.ui-regular}"
    rounded: "{rounded.md}"
    shadow: "{shadows.raised}"
  subagent-row:
    textColor: "{colors.body}"
    typography: "{typography.caption}"
    padding: "2px 6px 2px 8px"
    height: 28px
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

Version 7 (the 2026-09-25 activity pass) folds a turn's thinking and tool calls into its head row: the turn line ("Took 1m 2s · 5 thoughts (42s) · 3 commands · 2 files read ›") replaces the per-run activity rows, the assistant's paragraphs run uninterrupted, only what needs a person stays in the answer (failures, questions, permissions, images, subagents, a "Changed n files" line), and the whole timeline is one click away, in place under the turn line or in the Activity side panel. The previous per-run rows remain as the Detailed density (Settings → This browser → Activity), for comparison on the same Task. Since 2026-10-02 the step in progress is not folded while it runs: the live step at the turn's foot shows the thought streaming or the call running until it ends and joins the turn line's counts.

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
5. **Code is code, and only in the conversation.** Code blocks, inline code, tool rows, the commands in permission requests, native command output and diffs are JetBrains Mono at 11–13px. Everything else in the app, identifiers included (model, branch, path, version, keys), is Figtree.
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
| `canvas` | #fcfcfd | Main pane: header, transcript, panels, login, empty states; the loading veil at 70% |
| `surface` | #f7f8fa | Hover step on raised controls; read-only composer |
| `raised` | #ffffff | Composer, interaction cards, menus, dialogs, inputs, Task cards on the rail |
| `sunken` | #f0f1f4 | Wells: code blocks, thinking body, inline code, meter track |
| `bubble-user` | #f1f2f5 | User message bubble |
| `tint-well` | #f4f5f8 | One step below `canvas`: activity and tool-row pills, chips and uploads on the well, the settled question block and the finish card (`muted` text 4.87:1, `faint` glyphs 3.21:1) |
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
| `chat` / `chat-lg` | 14px / 16px | 400 | 1.7 / 1.6 | Conversation prose at 14px; composer input uses 16px on phones to prevent iOS input zoom; on iOS every other field is 16px for the same reason |
| `badge` (+ `font-bold`) | 8px | 700 | 1 | The two characters of a Project badge |
| `caption` | 12px | 400 | 1.4 | Times, row status words, shelf headers, counts, chips, notes, settings help |
| `code` / `code-sm` | 13px / 12px | 400 | 1.55 / 1.5 | Code blocks / ledger rows, diff body (mono) |
| `keycap` | 11px | 400–500 | 1 | Keyboard hints, version |
| `eyebrow` | 11px | 600 | 1.2 | Reserved; no eyebrows above headings in the shipped UI |

Rules: weight is ternary (400/500/600); only `display-*` carry negative tracking; counts, durations and times use `tabular-nums`; markdown headings inside chat do not scale up; the transcript and the composer fill the main pane inside its gutters (24px desktop, 16px from 481px, 12px on a phone); only a user bubble keeps its own cap (88%/720px).

## Layout

### Shell
Full viewport, no page scroll: `grid h-dvh grid-cols-[320px_minmax(0,1fr)]` from 960px up. The sidebar is a **fixed 320px** and is not resizable, but it **collapses to a rail**: the UAM brand in its header (or Ctrl/Cmd+B) animates the first grid column to 48px over `slow` (240ms) while the sidebar keeps its 320px inside an `overflow: clip` column (clip, not hidden, so scrolling the selected row into view can never shift it sideways), so nothing inside reflows; once collapsed it is `inert` under the **collapsed rail** (see Sidebar), and the main pane takes the rest of the width. The state is kept per browser (`uam.sidebar`). The rail's UAM mark brings the sidebar back; the main pane's headers carry no sidebar toggle on a wide screen. Below 960px the sidebar is a 360px drawer (never wider than the viewport less 44px) (a Base UI dialog sliding from the left, with backdrop) that the toggle in the pane header, and Ctrl/Cmd+B, open and close. The main pane is a column: header (44px, hairline below) → transcript (flex 1, `overflow-y: auto`, `overflow-x: hidden`, `overscroll-behavior: contain`) → composer (pinned); transcript and composer fill the pane between its gutters (see Typography rules), so collapsing the sidebar gives them the freed width. Nothing rubber-bands sideways: `overscroll-behavior-x: none` applies everywhere. A lost connection also shows as a banner across the top of the main pane, above the pane's header. Opening the stream (the first load, another Task) is a load, not a lost connection: it shows as the loading veil (see Placeholder and loading states), never as a banner. A signed-out provider shows the same way, as a `warning-wash` strip with a `warning` dot ("GitHub Copilot is signed out. Sign in in Settings.") and a small secondary **Open Settings** (absent while Settings is open), for as long as `GET /api/meta` lists it `signed_out`. A redeployed service shows the same way, once, as a quiet `surface` strip ("UAM was updated." with an `accent` dot and a small secondary **Reload**): only while a draft, an upload or an open popup stops the page from reloading itself; never a toast, never repeated.

**Safe areas.** The page is an installable web app (`viewport-fit=cover`), so the shell pads all four sides with `env(safe-area-inset-*)`: the headers, the connection strip and the pinned composer keep clear of a notch, rounded corners and the home indicator, and the drawer and the side sheets, which are fixed to the viewport, pad their own top, bottom and outer edge. Nothing else reads the insets. While the on-screen keyboard is open (the visual viewport over 100px shorter than the page, not pinch-zoomed), the shell takes the visual viewport's height and its top in the page, scroll included (`lib/viewport`, `--app-height`, `--app-top`), and drops its bottom inset, so the composer sits on the keyboard whatever the browser panned or scrolled; mobile browsers resize neither the page nor `dvh` for it. Once it closes the page goes back to its top, which iOS can leave scrolled. On iOS the drawer's dim starts at the drawer's edge rather than the screen's: iOS paints the status bar in the colour of a full-width fixed element at the top edge, and the dim there turned it grey.

### Side panels (Changes, Files, Activity)
From 1280px a panel sits inline to the right of the column and is **resizable**: an 8px handle on its inner edge (`role="separator"`, `aria-orientation="vertical"`, `aria-valuenow/min/max`), drag with pointer capture, arrow keys step 16px (64 with Shift), Home/End go to the limits, Enter or double-click resets to the default. Width is clamped to 320px … min(880px, main pane − 480) so the chat column keeps at least 480px (the main pane is the viewport less the sidebar, open or collapsed to its rail), and saved per panel in `localStorage` (`uam.panel.changes`, `uam.panel.activity`). The width is applied as the `--panel-w` custom property through CSSOM during the drag, so nothing re-renders per pointer move. Between 960 and 1279px a panel is a 440px overlay from the right; below 960px it is a full-screen sheet. Overlays and sheets are Base UI dialogs (focus trap, Esc, a backdrop that fades both ways) that slide in and out over `slow`; an inline panel commits the column's width at once and slides over the space it left, and slides out before it leaves. Overlays and sheets do not resize. One panel is open at a time.

### Breakpoints
| Width | Sidebar | Column | Panels |
|---|---|---|---|
| ≤480 (phone) | Drawer | 12px gutters, chat 16px, controls 44px | Full-screen sheet |
| 481–959 | Drawer | 16px gutters | Full-screen sheet |
| 960–1279 | Fixed 320px, collapses to the 48px rail | 24px gutters | Overlay 440px |
| ≥1280 | Fixed 320px, collapses to the 48px rail | 24px gutters, fills the rest | Inline, resizable |

A collapsed sidebar does not change the panel limits: an inline panel still keeps 480px for the chat column as if the sidebar were showing.

### Spacing and density
4px base. Sidebar rows 32px (44 on coarse pointers), shelf headers 28px, project rows 32px (40 with a branch line), ledger and disclosure rows 24px, headers 44px, buttons 32 (28 small, 36 large), inputs 36, chips 20, pickers 28, menu items 30 (44 on coarse pointers).

## Elevation & Depth

Every level is cool ink (`rgba(20, 28, 45, α)`, the canvas is cool) in two layers: a 1px ring at 5–6% that makes the edge read crisp without a drawn border, and one soft shadow. The tokens are `--shadow-*` in `index.css` (`shadow-raised`, `shadow-float`, `shadow-modal`, `shadow-well`, `shadow-focus`, `shadow-focus-float`).

| Level | Treatment | Shadow | Use |
|---|---|---|---|
| 0 Flat | Surface step only | none | Sidebar on `rail`, assistant prose and everything else on `canvas`; activity and tool rows are `tint-well` pills at this level |
| −1 Well | `sunken` or `code-bg` fill | `well`: `inset 0 0 0 1px .05` | Inputs, selects, the segmented track, code blocks, the models checklist |
| 1 Raised | `raised` fill | `raised`: `0 0 0 1px .05, 0 2px 6px .07` | Every Task card on the rail (the open one `tint-selected`), the user bubble (on `bubble-user`), a reply's subagent list, question blocks and the finish card (both on `tint-well` once settled), Settings cards, the segmented thumb, a selected file or folder row |
| 2 Floating | `raised` | `float`: `0 0 0 1px .06, 0 6px 20px .10` | The composer, the pending interaction card, menus, context menus, popovers, selects' lists, the inline picker, tooltips (ink fill), a lifted card or thumbnail on hover |
| 3 Modal | `raised` + `backdrop` | `modal`: `0 0 0 1px .06, 0 20px 48px .18` | Dialogs, the command palette, the drawer, overlay panels, the lightbox image |
| Focus | any of the above | `focus`: `0 0 0 1px focus, 0 0 0 4px focus@18%` (`focus-float` adds `0 10px 28px .12`) | Fields, the sidebar search and, through `focus-within`, the composer |

A shadow is never transitioned. The composer's focus and every hover lift (`lift`) draw the deeper shadow on a pseudo-element at opacity 0 and fade it in; the element itself moves only on `transform`. In a long transcript only the user bubbles and the few cards carry a shadow; runs, tool rows and thinking are fills or pills.

**Scroll edges.** The transcript is never cut by a hard edge: the Task header (`pane-header`) has no rule and, once content has scrolled beneath it (an IntersectionObserver on a sentinel at the container's top sets `data-scrolled`; nothing runs per scroll event), fades in a 24px gradient of the canvas and a 5% ink tint below its edge; the composer's dock (`transcript-dock`) overlaps the transcript's last 40px with a gradient up to the canvas, so rows fade out beneath the floating composer. Both are small absolutely positioned pseudo-elements over the scroll area, not masks on it, and neither takes pointer events.

## Shapes

One scale of three: `sm` 6px for controls (buttons, inputs, selects, menu items, the segmented track, switches), `md` 10px for cards and popups (Task rows, subagent cards, question blocks, code blocks, menus, popovers, the inline picker), `lg` 14px for the surfaces that float over the pane (the composer, the pending interaction card, dialogs, Settings cards, the user bubble). `xs` 4px stays for what is under 24px tall (chips, inline code, the Project badge, a segment and the thumb inside a segmented control, the switch thumb). `full` is dots, spinners, the Send and Stop circles and the activity and tool-row pills.

## Components

Every component is Tailwind utilities on the tokens, in `web/src/components/`; the Base UI wrappers live in `web/src/components/ui/` (`button`, `menu` with `ContextMenu`, `dialog` with `AlertDialog` and `Sheet`, `tooltip`, `select`, `segmented` on the radio group). States are Base UI data attributes (`data-open`, `data-highlighted`, `data-disabled`, `data-starting-style`, `data-ending-style`) or ARIA (`aria-current`, `aria-pressed`, `aria-expanded`). Hover is one surface step; press adds `ink`.

### Sidebar
Sits on `rail`, 320px, no border to the main pane: the `rail` → `canvas` step is the seam, with a 1px `hairline` rule at the column's right edge that fades out over the top and bottom 12% (`rail-edge`).

**Header (44px).** Local Search at left, followed by the compact 16px UAM mark with the `title` wordmark "UAM" beside it (one button), the Project filter (a badge button), Add project and New task (a pen, Alt+N). The UAM mark collapses the sidebar to its rail without changing the selected Task or URL, retaining Ctrl/Cmd+B and focus restoration (focus on the sidebar moves to the rail's mark, and back).

**Collapsed rail (48px, wide layout only).** Full height on `rail` with the same fading `rail-edge` seam, the sidebar's own controls stacked in one centred column with tips opening to the right: at the top, in a 44px row level with the pane headers, the UAM mark without the wordmark, which the 48px rail has no room for (Show sidebar, with the Needs you count pill on its corner); below it New task (the pen, Alt+N, the same palette), the Project filter (the same badge button and list, opening to the right of the rail) and Add project (the same dialog); at the foot Settings, the planner (while it is on) and the connection dot. Buttons are the header's 28px icon buttons, 6px apart, with 44px hit areas and 16px gaps on a coarse pointer. Search, the Task list and Log out wait for the expanded sidebar. A phone keeps the drawer and never shows the rail. The connection indicator lives in the footer. Search matches all entered words across Task name/title and real Project name/directory/branch within the selected Project filter. Results include active, settled and archived Tasks; clearing Search restores the active list and shelves. No backend search or PR lookups.

**Project filter (the badge button).** The header's badge button is the filter: the filtered Project's badge, or a `Layers` glyph in `muted` for all of them (T3 Code). It opens a 288px Base UI popover below it, the level 2 surface: a "Search projects…" box that takes focus (name and directory, every word), an **All projects** row, then one 30px row per Project with its badge and name and, at the right, a gear button named "Edit <project>" that opens Edit project once the popover has closed, so focus lands back on the badge. The current choice carries an `accent` check. The box keeps focus: arrows move the highlight (`aria-activedescendant` over `role="option"` rows), Enter chooses, Esc closes, a pointer over a row takes the highlight. Choosing a row closes the popover. A filter limits the flat Task list and lifecycle shelves to that Project, and is remembered per browser (`uam.projectFilter`); a remembered Project that no longer exists counts as no filter. The button is present whenever there is a Project.

**Project badge (16px).** The two characters at 8px in the UI font (Figtree), weight 700, with about 3px side padding in `on-primary` on the Project's tone (`bg-badge-<tone>`), `xs` radius, `aria-hidden` beside the name that names it. It leads each Task row, the filter button and its rows, the New task palette's rows, the Task header before the title, the Edit project dialog's title, and the Remove dialog's name row.

**State-grouped Task list.** The active Tasks across the visible Projects form four groups, in order: **Needs you** (a question or permission waits, or the Task failed or was interrupted and has not been opened since), **Ready for review** (finished and not opened since), **Working** (a turn or a subagent runs) and **Idle**. A group is a 28px `eyebrow` uppercase heading (`attention` for Needs you, `muted` otherwise) with its count in a `sunken` pill, then its rows; an empty group is left out. Needs you, Ready for review and Idle list the latest change first; Working keeps creation order so busy rows hold still. The Needs you heading carries the hint **Alt+J next** in keycaps at its right (not on a coarse pointer); Alt+J / Alt+K open the next / previous Task in that group, wrapping, except in the terminal and in a text field where the key types. "Opened since" is the per-browser read tracking (`uam.viewed`, from the first visit `uam.viewedSince`). There are no Project headings: Project membership is the badge on each row, and the filter shows one Project. A Project is managed in one place, **Edit project** (the gear on its filter row): its name, with **Previous sessions** (import) and **Remove project** as secondary actions that open over it and land back on their button when closed. Task menus carry Task actions only. Adding a Project selects its filter. The tab title, the app badge and the sidebar toggle (a filled `attention` count pill on its corner when the list is hidden or a drawer) carry the Needs you count.

**Task row (56px minimum).** Every row in every group is a Task card: `raised` on the `raised` shadow (the elevation is its boundary on the rail; there is no drawn border), `md` radius, 4px apart. Hover lifts it (`lift`: 1px up, the `float` shadow fading in on its pseudo-element); the open Task's card is `tint-selected` instead of `raised`, with its name in semibold; an unread or Needs you row sets its name in medium `ink`. Two lines: the 16px Project badge (the name for screen readers), the Task name at 13px, `+N −M` in mono `meta` (`success` / `error`) when the service reports the Task's own changes, and the relative time of its last change in `meta` `muted`; then, indented under the name, one plain status line in `caption` and its tone: "Asks: <question>" and "Wants your OK to <what>" (`attention`, up to two lines), "Finished, ready for your review" (`success`, else "Finished" in `muted`), "Stopped with an error" (`error`), "Interrupted before it finished" (`warning`), "Compacting…" while the conversation is compacted (`accent`), "Working" or "Working · quiet 12m" once the provider has been silent three minutes (`accent`; never what the agent is doing). A Needs you row carries no answer controls: questions and permissions are answered in the Task. Selected rows set the name in semibold (600). Keyboard focus retains its visible outline. Task-row actions live in the context menu, opened by right-click, Shift+F10/Menu key or supported touch long-press. The one visible action is **Settle** on an active row that can settle: a 24px check-circle button at the end of the status line, shown on hover and keyboard focus (always on touch). All lifecycle/context menu actions, F2/double-click rename, keyboard navigation and the pinned selected Task remain available. Search results are one flat list of the same rows and label settled/archived state.

**Shelves.** At the foot of the list (following the active Tasks once they scroll) come **Settled** and **Archived** shelf headers (28px): the word and count in `caption` `muted`, a fading rule, a chevron. A shelf row is one 32px line holding the Project badge and the Task title, at 60% opacity until hovered, focused or selected. Its tip (to the right) carries the full title, the badge with the Project name, the directory in `on-primary/70`, and the created, settled and archived times (local date and time; a time the Task lacks is left out). Collapsed by default; the state persists per shelf (`uam.shelves`). A collapsed shelf still shows the open Task pinned beneath its header. Shelf state is scoped to All projects or the selected Project. Archived Tasks open like any other, read-only.

**Keyboard.** Arrow Up/Down move between rows, Home/End jump, Enter opens, F2 renames, Shift+F10 or the context-menu key opens the row menu. Focus rings sit inside rows (`-outline-offset-2`).

**Footer.** A 28px **Settings** gear (`aria-pressed` while the Settings view is open) and the service version in `keycap` `faint` at left, **Log out** at right when a token is required. When the connection is lost a banner row appears above it: `warning-wash` (reconnecting) or `error-wash` (offline) with the pulsing dot and the full sentence.

### Placeholder and loading states
With no Task open the main pane is a quiet placeholder: the mark at 36px with the `display-md` wordmark "UAM" beside it, and one `ui` `muted` line ("Open a task from the sidebar, or start a new one there."; without a Project, "Add a project in the sidebar to begin."). There are no buttons: New task and Add project live in the sidebar, and there is no empty-state screen (#185). When the sidebar is a drawer, a 44px header above it carries the drawer toggle, the brand and the connection dot; a collapsed wide sidebar leaves those to its rail. **Loading is never blank, and never the empty state.** The app knows, per data set, whether it has loaded at least once, and until then draws a **skeleton** (`Skeleton` in `common.tsx`): a few `sunken` bars in the shape of what is coming, with one highlight band sweeping over the group (`sweep`, transform only; the band is a pseudo-element on the group, so a skeleton is one animation, mounted only while loading, and still under Motion: Match system). A skeleton is a `role="status"` carrying its label for screen readers, its bars are `aria-hidden`, and the region around it is `aria-busy`. Only once a load has confirmed there is nothing does the empty state appear ("No projects yet…" with Add project in the sidebar, "Add a project in the sidebar to begin." in the pane, "New task in …" over an empty conversation, "No changes.", "No previous sessions…", "No project matches"). A failed load is an error line with **Retry**, not the empty state.

The flags: `state.loaded` (the first snapshot; before it the Projects and Tasks are unknown, the sidebar list is a deck of card-shaped bars and the main pane is the loading pane), `meta` with `metaError` (the catalogs: the composer's pickers hold a skeleton bar and Settings shows the New tasks, Utility model and Models cards as skeletons until `GET /api/meta` answers; a failure puts an error line with Retry in the Models card and `checkVersion` keeps the last catalogs once there are some), a Task's `history === 'loading'` with no items (a transcript skeleton in place of the "New task" intro), the Changes list and a file's diff (`null` until the reply), a subagent's transcript (`loading` in `state.agents`, Retry on failure), Previous sessions (`null` until the reply; Refresh retries), and the New task palette while `loaded` is false. The folder picker keeps its own loading and status lines inside the well. While a Task's detail loads and nothing was on screen before, the loading pane keeps the header's height over a transcript-shaped skeleton (a bubble at the right, then lines); when another Task was open it stays, inert, until the new one lands or the wait passes 600ms. While the stream opens (the first load, another Task, a reload of the open one) and the wait passes 600ms, a **loading veil** covers the pane below its header: `canvas` at 70% over whatever is there (the skeleton, a cached Task, the placeholder), taking the pointer, with the plain spinner and "Loading…" (`caption` `muted`, `role="status"`) on a `canvas` pill at its centre. It fades in and out (`base`, opacity only), leaves the header usable, and never covers Settings or a new Task; a retry after a lost connection keeps the connection banner instead. Every other wait (a button's own request, the inline picker) is the `Loading` line (nothing for 300ms, then a spinner and a word) and whatever was loaded before stays in place. The in-browser mock takes `?mock&slow=<ms>` to hold the first snapshot and every reply that long, so each of these states can be looked at. A deleted Task explains itself and offers **Back to projects**.

**New task** is a command palette (T3 Code): a `sheet-wide` modal near the top of the screen (a bottom sheet below `sm`) without a title row, a back arrow and a "Search…" box that takes focus, a "Projects" group listing every Project as a 44px row with its badge, name and directory (`meta`, `muted`), a keycap hint **Alt+1…9** at the right of the first nine, and a footer of keycaps: ↑ ↓ Navigate · Enter Select · Esc Close. The filtered Project starts highlighted, else the most recently active one; the search box matches name and directory, and "No project matches" is the empty state. Enter, a click or Alt+digit choose; the palette closes, then the draft opens with its composer focused (the closing dialog leaves focus alone). With exactly one Project the pen opens the draft at once. Alt+N opens the palette from anywhere but a menu or dialog (Ctrl+N and Ctrl+digits belong to the browser). Choosing a Project opens a draft, not a Task: the pane shows a 44px header with the Project badge and `display-sm` "New task", the same centred "New task in …" lines as an empty Task, and the composer on the New tasks defaults from Settings, focused on a fine pointer. Nothing is created, listed in the sidebar or put in the URL until the first Send, which creates the Task with the chosen settings, sends the message with its `@` files and attachments, and opens the Task. Leaving the draft (another Task, Settings, a reload) discards it; its text is kept per Project in this browser (`uam.draft.new.<project>`), attachments are not. The `/` and `$` lists say commands and skills are available after the first message; the execution picker appears once the Task exists.

### Settings view
Not a dialog: a view in the main pane, `#settings` in the URL, opened from the sidebar footer's gear and closed by its **×**, by opening a Task, or by the gear again. Its header matches the Task header (44px, `display-sm` "Settings", the drawer toggle first on a narrow screen, a spinner while a change saves). The body fills the available pane with 16–24px gutters and one compact `muted` line ("Kept by the service, so they apply in every browser. This browser's own settings are at the end.") followed by **sections**, each a floating card (`raised`, `lg`, 20px padding, the `raised` shadow) with a `title` heading, 16px gaps within and between cards, and no lines between rows: rows are separated by their 8px vertical padding alone. A custom provider and the Add provider form sit in a `tint-well` block inside the Models card; the models checklist is a `raised` well. Models use compact rows in one column on narrow screens, two at 1280px and three at 1800px; provider headings span the grid. Names, IDs, costs, visibility state and 44px touch targets remain available. The header stays fixed while the body scrolls. A **row** is the label (`ui` 500 `ink`) with its help in `caption` `muted` on the left and the control on the right (stacked on a phone). **GitHub Copilot** comes first (one card per provider whose sign-in can be managed, `CopilotAccount.tsx`): a status line with a `success` dot and "Signed in as <login>" or a `warning` dot and "Signed out", a `caption` `muted` line saying how (a sign-in stored on the server, the token in a named environment variable shown in a `sunken` code chip, or the GitHub CLI) or Copilot's reason, and at the right secondary **Check again**, **Use another token**, and a `danger` **Sign out** only for a stored sign-in. The token form (signed out, or after Use another token) is a labelled password field with a `github_pat_…` placeholder and a primary **Sign in** in one row (stacked on a phone), help naming the token type and permission with an `accent` underlined link to GitHub's token page, and a `warning` Note that it changes the account for every Task on the server. Replacing a sign-in and Sign out are confirmed in an alert dialog. An environment token shows a `warning` Note naming the variable instead of the form. A last `muted` Note says device-code sign-in is not supported and names `copilot login`. The **Composer** section has one row, "While a task is running, Enter…", a **segmented control** (`Steer` | `Queue`, Steer the default) whose help names the other action's shortcut and menu. **New tasks** holds what every new Task starts with (the same Model, Effort, Context size and Mode fields the composer's pickers set, `TaskDefaultsFields`, in a two-column grid capped at 576px, one column on a phone), saved as each field changes; Projects carry no defaults of their own. Below them, a row "Compact the conversation when its context reaches" with a select of 50% to 90% in steps of 5 ("80% (default)"); its help says that compacting earlier keeps answers faster and cheaper but drops older detail sooner, and that an open Task picks a change up when it reopens. **Utility model** (the model UAM uses for its own small AI jobs, such as titling new Tasks) offers, for each provider with title support, "Cheapest (currently <model>)" as the default, the provider's own title (no AI), then each visible model with its reported prices; it is absent when no provider has title support. **Background AI** shows today's Utility calls against the daily limit (`title` count over a thin meter, `warning` once paused, with a `warning` Note saying it is paused until tomorrow), the limit as a number field with Save, and the log behind a subtle disclosure button with a chevron ("Show log · N calls today" / "Hide log"), collapsed on every visit and read only while open, opening with the shared `Collapse`: per server-local day a `ui` 500 heading with the day's totals in `caption` `muted`, then indented rows (time, purpose, the Task as a subtle button or the Project, a `warning`/`error` chip for skipped or failed calls, a `caption` line of model, characters, tokens, duration and credits), 25 at a time with **Show older calls**. **Models** lists names, IDs, reported costs and visible/hidden switches; only a hidden row says so in words. Hidden current selections keep their label with "hidden in Settings" but do not appear as choices. Unknown hidden IDs stay removable. **Shell access** holds one switch, **Terminal** (off by default; see Terminal), whose help says what turning it on allows: a shell for anyone signed in, as the uam user, without the agent's permission prompts. **MCP servers** (present when a provider manages them) is one `muted` Note with **Add server** (secondary, `sm`) beside it, then one row per server: the name (`ui` 500), its type as an outlined chip (`HTTP`, `SSE`, `Command`), a `muted` source chip for a read-only plugin or built-in server, `Off` in `meta` `muted` when new Tasks skip it, and under it the command line or address in `code-sm` mono with the env or header names as `NAME=••••`; on the right Edit, Remove (`danger`, behind an AlertDialog) and the switch for new Tasks. Edit on a command server while Terminal is off is `aria-disabled` with the reason in its tooltip. The add/edit form is a `tint-well` block like the custom provider form: Name and a segmented type control (`HTTP` | `SSE` | `Command`; Command only while Terminal is on, with a Note saying why otherwise), then Address with a Note that it is shown to everyone signed in, or Command, Working folder and a mono Arguments textarea (one per line); env variables or headers are rows of a mono name and a password-type value whose stored value reads `•••• set` and is never filled in. The Task's **MCP servers** dialog (a `Dialog`, sheet on a phone) lists the servers: name and source, a `Dot` with the state word (`success` Connected, `error` Failed, `warning` Needs sign-in, pulsing `muted` Starting, `faint` Off for this task) and a text button with the tool count that opens a `tint-well` list of tool names in mono with two-line descriptions; on the right Sign in (secondary), Sign in again (ghost, connected remote servers), Restart (secondary, failed or stopped) and the switch for this Task; a failure's text in `caption` `error` below. Signing in opens a numbered `tint-well` panel under its row: a link that opens the provider's page in a new tab, the plain explanation that the page it ends on may not load from another computer, and a mono address field with **Finish sign-in** (primary). The footer holds **Reconnect with current settings**. **This browser**, last, holds settings kept in `localStorage` rather than by the service: **Notify me when a Task needs me or finishes**, a switch that asks for the browser's permission only when turned on (a refusal leaves it off with an `error` Note naming the fix; its help says where notices arrive, and on an iPhone or iPad tab it is disabled and explains Add to Home Screen; see Notifications in docs/web.md), **Motion**, a segmented control (`Always on` | `Match system`, Always on the default; see Motion), and **Activity**, a segmented control (`Compact` | `Detailed`, Compact the default; see Transcript, Activity density), which an open Task follows at once. Controls disable while saving; stale responses cannot overwrite a newer save. A change shows at once and is saved through `PATCH /api/settings`; a refusal puts the old value back and an `error` Note says why.

**Segmented control** (`ui/segmented`, Base UI radio group): a 32px inset `sunken` track (the `well` ring) with 2px padding and `sm` radius; segments are equal columns, `xs`, at least 64px wide, `ui` 500 `muted`, the chosen one `ink`; under them one floating `raised` thumb (the `raised` shadow) slides to the chosen segment on its transform (`base`, snapping under Motion: Match system). The segment count and the chosen index reach the thumb as `--seg-n`/`--seg-i` through the CSSOM. Arrow keys move the choice; the group is named by the row label and described by its help.

**Switch** (`ui/switch`, Base UI): 32×18, an inset track (`faint`, an inner shadow; `accent` when on) under a floating 14px `raised` thumb with its own small shadow that slides 14px on its transform (`fast`). Off, the `faint` track is 3:1 against `raised` and the white thumb 3:1 against the track, so the control's boundary and state read without colour alone.

### Task header
44px, `canvas`, no rule; once the transcript has scrolled beneath it a fade appears under its edge (see Elevation, Scroll edges). Left: the drawer toggle (on a narrow screen), the Project badge, the title (`display-sm` `ink`, truncating; double-click, the hover pencil, or Rename in the menu edits it in place with the same rules as the row), then the state chip (`StateMark` with the word; `Archived`/`Settled` as an outlined chip when read-only), a spinner while a lifecycle request is in flight, and the rename pencil, which takes no room until the title is hovered or it is focused. Right: the project strip, that is the branch (`meta`, `muted`, with the Project folder in its tooltip; hidden below 481px) and **Changes** (`FileDiff` + count, `aria-pressed` while open; label hidden below 481px) and **Files**; where the Project has no Git (not a repository, or git not installed) one `warning` button (`TriangleAlert` + "Not a Git repository" or "Git not installed"; label hidden below 481px) stands in their place and opens a popover with the reason, and a compact turn drops its "Changed n files" line, then **Terminal** (`SquareTerminal`, tooltip "Terminal in <Project>", `aria-pressed` while its panel is open; label hidden below 481px; present while Settings → Terminal is on, with or without Git and on read-only Tasks too; see Terminal), then **Activity** (`Activity` + the count of the Task's thoughts and tool calls, `aria-pressed` while its panel is open; present once there is one; label hidden below 481px), then **Subagents** (`Bot` + count, spinner while any run; label hidden below 481px), and the **"…"** menu with the same items as the row plus, for an active Task of a provider that manages MCP servers, **MCP servers…** (`Plug`), which opens the Task's MCP servers dialog (see Settings view, MCP servers). The model and context details live in the composer. At 420px the title truncates and action labels shorten. Below 640px (`max-sm`, a phone) the title keeps the row: the state chip shows its glyph alone (the word stays for screen readers), and Files and Terminal leave the row for the end of the "…" menu (Browse files or Close files, Open terminal or Close terminal); from 640px up the header is unchanged. The switch is a media query in the component, not `display: none`, so a hidden item is never a stop in the menu's arrow-key order.

### Terminal
For the user, never the agents: a shell in the Project folder, running as the uam user. **Settings → Terminal** turns it on (off by default); then the Task header offers **Terminal**, and turning the setting off closes every open terminal, the panel with it. The terminal is docked at the bottom of the main pane, under the Task view, at every width: part of the layout, never an overlay; the Task view (transcript and composer) gives it the height. Its top edge is a fading rule and a drag handle (`uam.panel.terminal-h`, 320px at first, 160px up to the height that leaves the Task view 280px; arrow keys and double-click as for side panels). It is not one of the one-at-a-time side panels: Changes, Files, command output and a preview open beside the conversation above it. It belongs to the app, not the Task: switching Tasks keeps it and its shell; the Task header's **Terminal** shows pressed while it is open and closes it, and opens it in that Task's Project folder when closed. Its header (36px): `SquareTerminal`, "Terminal" (`title`), the Project folder (`meta` `muted`, truncating, the full path in its tooltip), the status (`meta` `muted`, a live region: Connecting…, Connected, Exited (code N), Disconnected), **Restart** (`RotateCcw`, a new shell in place of the old; label hidden below 481px) and **×**. The body is the terminal canvas, the panel less an 8px margin; it fits whole cells on every size change (a ResizeObserver, one fit per frame) and tells the shell its new size. The terminal takes focus when the panel opens. **Mouse clipboard** (Windows Terminal, PuTTY): releasing a mouse selection copies it, with "Copied" for 1.4s in a `meta` `muted` status line before the status; right-click and middle-click paste the clipboard through xterm.js's paste (bracketed paste holds) instead of the browser's menu; Ctrl+Shift+C copies off macOS (Cmd+C on it). A program tracking the mouse gets its clicks unless Shift is held. Where the browser will not let the page read the clipboard, that line says "Clipboard blocked. Use Ctrl+Shift+V to paste." (Cmd+V on macOS) for 6s. Touch is untouched: a long-press keeps the browser's own behaviour and never pastes. Esc belongs to the programs in the terminal: neither Esc, a backdrop press nor a press in the conversation closes the panel; only **×** does. **Kill-on-close:** the shell lives exactly as long as its socket. Closing the dock, Restart, reloading, removing its Project or turning the setting off ends it and whatever runs in it; there is no reattaching. A refused socket (the setting off, the folder gone, eight terminals already open) reaches the page only as a close, so it reads as one `error` line, "Could not open a terminal.", with **Retry**. **Theme:** the one theme: `canvas` under `ink`, an `ink` cursor, the `selection` tint, 14px `font-mono` (JetBrains Mono) on a 1.25 line height, and sixteen ANSI colours at 5:1 or more on `canvas`, from the signal and badge tones where one fits. **WebGL only** (see CSP constraints): with WebGL turned off, or once its context is lost, the panel says "The terminal needs WebGL, which this browser has turned off." and shows no terminal. xterm.js and its CSS come in a lazy chunk with the panel's content, never with the page.

### Transcript
User turns are bubbles on the right (`bubble-user`, `lg` radius, the `raised` shadow, 88%/720px max, 88% on phone). Assistant prose is the page and stays flat. Provider and model labels are omitted from the main conversation; the model remains in the composer. Every message with text has a hover copy button and a right-click menu (Copy message); a user message of only attachments shows its chips alone, and one with neither text nor chips (only file references, sent through the API) reads "No text" in `caption` `muted`. A steer the provider accepted but has not recorded yet reads semibold italic (its tooltip and screen-reader name say "accepted, not delivered yet"); once delivered it settles to normal text. Items that arrive after the transcript mounted rise in (`rise`, 240ms); items present at mount appear at once; streaming text appends with no animation.

Tool and reasoning records arrive with lightweight labels and state. Their full bodies load when their own disclosure is visible and open, with an inline loading/error state. Open bodies stay live through completion and later corrections. Closing a parent suspends its children while preserving remembered expansion choices. Copying an unloaded body fetches that one retained value. Closed subagents show their recent preview and state; opening one loads its recent transcript and scrolls into older history automatically. These rules change data delivery, not activity grouping or the existing controls.

Revisiting one of the five cached recent Tasks shows that Task's content immediately with a "Refreshing task..." status. It stays visible during a slow refresh, under the loading veil once the wait passes 600ms. Typing and task actions wait for the fresh snapshot; the selected Task's saved draft returns on confirmation. Cached transcript eviction does not remove drafts. Scrolling loads one page at a time and preserves the visible message when the page arrives. Main and subagent transcripts retain a small reading window and a recent live tail. Moving back toward newer history automatically refetches evicted rows. Expanded groups retain their state across paging, and Jump to latest returns to the current tail.

- **Activity density:** how a turn's work is drawn is a per-browser choice (Settings → This browser → Activity, `uam.activity`): **Compact**, the default, is the turn line, the promotion rules and the timeline below; **Detailed** is the activity row, thinking row and tool runs that follow, one activity row per run of work between two paragraphs, exactly as before. A subagent's transcript, in its panel or sheet, uses the same choice. Its prose and inline paths wrap within the row; code blocks and wide tables retain their own keyboard-accessible horizontal scroll regions.
- **Turn line (Compact):** the turn status row is the one line for everything the agent did in the turn: "Took 1m 2s · 5 thoughts (42s) · 3 commands · 2 files read ›" (`caption` `muted`, `tabular-nums`, truncated, never wrapped; only the subagent chip beside it may wrap to its own line). After the duration come the counts in a fixed order: finished thoughts with their total recorded time (each thought from its model call's start to its end, as the provider recorded them; a thought without a recorded end adds no time), commands, files changed and files read (distinct paths), searches, other tools, questions answered and declined, requests decided; then what stays explicit: "1 failed" in `error` (only that part; the line stays `muted`), calls without a result, questions unanswered, images returned. A call still running and thinking that still streams are not counted; the live step at the turn's foot shows them, and each joins the counts when it ends. Until the turn has a count (its first step still runs, or its only call waits for the user) the line stays blank in its slot, with no bare chevron: the live step or the foot's label ("Waiting for your approval: …") says what is happening, and the first count lands in the same 34px slot without moving anything. With anything counted the line is a button (`aria-expanded`, `aria-controls`; the screen reader hears "…, activity of this turn") that opens the turn's whole timeline in place through the shared height collapse, indented behind a fading left rule: each thought as its row (its text `muted` on its own click), each tool call as its row with its approval mark, details and images, each settled question and decided request as a one-line row (the question's card already stands in the answer). Every timeline row has a left gutter (`caption` `tabular-nums` `faint`, the row's height): its start to the second on a 24-hour clock (the full local date and time in its tooltip), then its recorded duration in `muted`, right-aligned in a fixed 40px slot, blank while the step runs or when the provider recorded no end. Nothing is inferred from what followed. Detailed rows carry no gutter. At its foot a **Collapse** button (`ChevronUp`, `caption` `muted`) folds it too, bringing the turn line back into view and focus back to it, so a long timeline never has to be scrolled back up to close. The timeline is mounted on the first open only, so a collapsed turn costs one button; the choice is per turn and per mount, not remembered. Live, the same line carries the counts alone (the working label over the composer says the turn is busy, and for how long), updating in place as items append; it reads `attention` while a call waits for the user. Prose alone folds nothing: the row stays "Took 12s", as before. A subagent's `task` call is never counted there, nor its failure in "1 failed": the reply's subagent chip beside the counts carries them (Subagents below).
- **Promotion (Compact):** nothing the person must see hides in the turn line. These stand in the answer at their natural place, exactly as their rows read inside a run: a question once it no longer waits (the question block), a steer bubble, a notice. A tool call itself never stands in the answer; what it produced for the person does, at the call's place: for a call whose result returned images, the thumbnails alone (the same thumbnails and lightbox as under its tool row, with its `images_note`, but no row, mark or tool name); for a completed `uam_show_file` declaration, its file card alone, the same card for every file type. The call folds like any other: the turn line counts it (images stay explicit, "1 image") and its tool row, thumbnails included, is in the timeline. Subagents never stand in the answer: the reply's subagent chip counts them, its list holds their rows, and the live set shows the ones in use; the timeline leaves their `task` calls out. A pending permission or question stays the action card under the transcript; a decided permission sits on its tool row in the timeline (the approval mark) or, naming no row, is a decided-request row there. A turn whose completed edit, write or create calls named files ends with one `caption` line, `FileDiff` + "Changed 2 files" (the paths in its tooltip) and **View changes**, which opens the Changes sheet; line counts are not known here. Routine reads, searches, commands and thoughts never interrupt the paragraphs, and neither does a failed call: it folds like any other, the turn line's "1 failed" in `error` says so, and its `error` row, which opens onto its output, is in the timeline.
- **Activity row (Detailed):** everything the agent does between two messages (thinking, tool calls, the quiet rows of decided requests) folds into one 24px `caption` `muted` pill (`tint-well` fill, `full` radius, `tint-hover` on hover; no shadow, since a transcript holds many), closed by default: a chevron in a fixed 14px slot, then one line, truncated, never wrapped, such as "Thought 4×, ran 4 commands and read 2 files · 1m 4s" (finished thoughts counted, the tool-run phrases, the duration from the first item to the next item's start once known). Nothing that needs attention hides in it: while a call runs or thinking streams the working mark takes the chevron's slot and the label names it ("Running: bash npm test", "Thinking…"); a call waiting for permission reads "Waiting for your approval: …" in `attention` and its card stays under the transcript; a failure turns the row `error` with "1 failed"; images a tool returned are counted ("2 images"); a call that never reported is "1 without a result". The last run of the streaming turn stays closed too and its label updates in place, so nothing on screen moves while items append. It opens through the shared height collapse onto the rows below, indented 22px, each with its own disclosure. Assistant prose, user bubbles, notices and question blocks stand between the runs, never inside them. A completed `uam_show_file` declaration folds into its run like any other call, and its file card stands right after that run. A subagent's row, in any state, folds into the activity row in its `task` call's place (a one-row `subagent-card`); "Show where it was spawned" opens that fold, and the live set at the foot shows the ones in use.
- **Thinking:** thoughts are metadata, never paragraphs: in Compact they are a count and a total time on the turn line, and their text appears in the expanded timeline, `muted`, and, while the thought streams, in the live step; in Detailed each is one 24px row inside its activity row (`ui` `muted`, a chevron): "Thinking…" shimmering while it streams, "Thought for 12s" once its recorded end arrives (from its model call's start). Nothing of the text shows while the row is closed, so streaming never resizes it; a click opens the text (markdown whose headings stay `muted` too, `md-quiet`, behind a 2px fading left rule, with Copy thinking) through the shared height collapse. Empty reasoning items are not drawn. The choice is remembered per item for the browser session.
- **Tool runs (Detailed):** consecutive tool calls form one compact disclosure (a `tint-well` pill like the activity row, a button over the shared collapse, never a native `<details>` snap), such as "Changed 1 file and ran 3 commands"; its tool rows are indented behind a fading left rule and each row is a transparent pill that fills `tint-well` on hover. A tool row whose title repeats its name as the verb ("Edit cmd/doctor.go" for `edit`) shows the rest only. Counts use actual successful known tool operations, with distinct known file paths; failures, running calls and calls without a result remain explicit. Unknown tool types count as tools, never inferred shell subprocesses. Opening the summary retains every original tool row, full input/output, copy actions, approvals and images. Tool runs and thinking rows sit inside their activity row; assistant prose, questions and declared files' cards stay outside it.
- **Turn status:** one 34px row, with no rule under it (nothing full-width separates turns; the user bubble and the whitespace do), heads every assistant turn, under the user bubble. Live, it keeps its slot, blank (in Compact, the counts): the working label says the turn is busy; ended, the same row reads "Took 12s" from the recorded duration, or stays blank when none was recorded. In Compact the same row is the turn line: the counts follow the duration and the row opens the timeline. "Working" stays the Task state's name (sidebar row, header chip); the turn row does not repeat it. A Task whose turn completed while a subagent still runs (Copilot lets background subagents outlive their turn) shows Working there too, the header chip's tooltip counting the subagents running (`shownState` in `lib/tasks.ts`). So does a Task whose conversation is compacting (summary `compacting`, from `/compact` or the provider's automatic compaction), unless it waits for the user; its header chip then reads "Compacting…" with the working mark. How it ended is a notice row in the transcript. Streamed content lands below it, so nothing on screen moves when a turn starts or ends. Background shells remain independently visible in the composer toolbar and never drive foreground timing.

- **Approval mark:** a decided permission request sits on its tool row, right-aligned: a 12px shield (`ShieldCheck`, `ShieldX` for denied, `Shield` for expired) in `faint` and one `caption` word in `muted`, "auto" for yolo, "allowed" or "denied" for a person, "expired". When several requests name one call the mark shows the latest word and "+n" in `faint`; the Base UI tooltip and the accessible name carry every request's full "title · state · resolution", latest first. It is never a line of its own. A decided request that names no tool row is a 24px row of the same kind in its turn at its time: glyph, title in `ui`, the request's first line in mono, the word at the right.
- **Question block:** a question renders as an `md` card on the `raised` shadow (`raised` while pending, `tint-well` once settled, so it stands apart from the canvas), from its interaction (any provider: text, header, choices, state, resolution) and, for Copilot's `ask_user`, from the tool's input and output, so it reads the same after a reload or a restart. While pending: a `caption` "Question" head with the `MessageCircleQuestion` glyph and "· Waiting for your answer" in `attention`, each question as markdown in `body` with its choices as a list. Settled (answered, declined, left or failed) it is compact, at most two lines: one button (44px on a coarse pointer, `aria-expanded`) holding the glyph, the question in `ink` on line 1 and the answer on line 2, each truncated with the whole text in its tooltip and in the button's accessible name; where the card is at least 42rem wide (container query) both share one line, the answer taking at most half. The answer reads "You chose" (`caption` `muted`) and the chosen labels joined by ", ", or "You wrote" and the typed text, in `ink`; "Declined", "Not answered" (a call left open by a restart or a stopped turn) and "Answered" (no recorded text) in `muted`; "Failed: …" in `error`. Several questions in one request are one summary: "3 questions" in `muted`, the questions joined by " · ", then "You answered" and the recorded answers, so the card never grows past two lines. A `ChevronRight` at the end turns down when open; the shared height collapse then shows the full card under a "Question" head: each question as markdown with its choices, a `success` check on the chosen ones and hollow dots on the rest, a fading rule and the whole answer. The disclosure state is kept per question across re-renders and history pages. It sits on the tool row that asked, or stands alone in its turn when no row did. While the question waits it is drawn once: a question with one question as the composer's extension (answer mode), any other as the action card below the transcript; the block appears once it is answered, declined or left.
- **Tool images:** the images a tool returned sit under its row, indented to the text, visible when its run is expanded, without opening the individual tool details: the same thumbnails and lightbox as transcript attachments (160px, `sunken`, hover lifts and scales 1.02, at least 44px square on coarse pointers); `images_note` follows as a `caption` `muted` line. The row appears first without them and gains them when UAM has stored them. In Compact the same thumbnails also stand alone in the answer at the call's place (promotion).
- **Code block:** `code-bg` with the inset `well` ring (no border, no rule under the header), `md` radius, a 24px header with the language (or `code`) and a copy button; right-click offers Copy code. Max height 480px with internal scroll; never wraps.
- **Code highlighting:** a block with a language gets `hljs-*` spans in five tones on `code-bg`: comments `muted`, keywords `accent`, strings `success`, numbers `attention`, names and types `warning`, titles `ink` at 500; added and removed diff lines use the diff tokens. Nothing italic, nothing outside the ladders. The grammars load on the first such block.
- **Diagram card:** a fenced `mermaid` block in the code-block chrome. The header reads `mermaid`, shows a spinner while the frame renders, then a Diagram / Code toggle (two 20px ghost buttons, `aria-pressed`) before the copy button, which copies the source. The diagram is an `<img>` on the well, centred at its intrinsic size up to the block width and 480px, and fades in (`base`); clicking it opens the lightbox (the attachments' dialog). While the fence is still streaming the code shows; when Mermaid rejects the source the code stays, with a `caption` note under it ("Diagram could not be rendered: …"). Inside the diagram the colours are tokens (`raised` nodes, `hairline-strong` borders, `muted` lines, `accent-wash` secondary, `warning-wash` notes) in Figtree: neither the sandboxed frame nor an SVG image can fetch the page's font, so the frame bundles the Latin file, measures with it and embeds it in each SVG.
- **Chart card:** a `uam_chart` chart is drawn at its box's width (40px steps), so its axis text stays at `caption` size at any width; the frame draws it in a box as wide as the chart, since Mermaid measures an xychart's labels on screen. Series colours are data, not identity, and the one place the badge tones mean something else: several series take `badge-blue`, `badge-orange`, `badge-teal`, `badge-pink` (the Okabe-Ito hues), then `badge-violet`, `badge-cyan`, `badge-green`; a lone series takes the tone its name hashes to. Never `badge-red` (it would read as an error).
- **Subagents:** they live in the conversation, prominent only while in use; there is no subagent panel. A reply's subagents are those its `task` calls spawned (a reused subagent keeps its first call, so it stays with the reply that first spawned it). A subagent another subagent spawned has its call in that subagent's transcript, so it joins its top-level ancestor's reply: in the chip's count, the list and the index. Which calls belong to which reply comes from the Task's outline (the server's list of every user message and subagent call it holds, `outline`, with the identity index `history_index` for what this browser loaded since), never from the window on screen or the live tail, so a long reply is counted whole and a subagent finds its reply before that reply's history page is loaded. A subagent may run more than once (`runs`: spawned, then resumed by the agent or by a follow-up); without runs, everything below reads as if it ran once. Turn numbers never appear: a cross reference is a clock time with ↑ or ↓, or the user message quoted.
- **Subagent identity:** while its reply has at most five `task` calls (loaded or not), each subagent gets a tone by its call's order: `badge-violet`, `badge-pink`, `badge-cyan`, `badge-amber`, `badge-teal` (never red, green or blue, which are states). It shows as a 6px dot before the name; the name always stands beside it. A reply with more draws all of its subagents neutral.
- **Subagent chip (Compact):** beside the turn line's counts and read the same way: a 24px text button like the counts (`caption` `muted`, `body` while open, `tint-well` on hover, no fill, ring or pill), the 6px identity dots (the `Bot` glyph past five), then "3 subagents · 1.2M tokens · 2 done · 1 failed · 1 running · 1 stopped" (`caption`; the tokens are the sum of what its subagents report, in K, M or B, left out while none is known; only the count parts that are not zero; done `success`, failed `error`, running `accent`, stopped `muted`; done is completed or idle, stopped is cancelled), and a `ChevronRight` that turns down while open; on a narrow screen it wraps under the counts rather than truncating both; while some of the reply's calls have no subagent loaded here, "2 of 3 loaded" in `muted` follows the noun (the dots or glyph and the noun come from the full count). It is a button (`aria-expanded`, `aria-controls`) that opens the reply's list under the line through the shared height collapse, folded by default, the choice kept per reply like the turn line's. A turn of only subagent calls still gets its line, for the chip. While the live set shows every subagent of a reply, that reply's chip waits (an open list keeps it): the set says the same, and the chip comes back when the set leaves.
- **Subagent list:** read like the turn's timeline, not a card: no head (the chip says how many), the rows flowing top to bottom into columns at least 20rem wide with 24px between them, as many as the conversation's width holds (one on a phone). Rows go by family, a subagent then the ones it spawned, each level indented 18px behind a `CornerDownRight`; a family never splits across columns. Families with a failed member come first, then those with a running one, then the rest, each in spawn order. Past twelve a filter sits above the rows (name or one-line result, case-insensitive), keeping the families with a match. It renders 50 families, then "Show 50 more"; a `locate` clears the filter and pages to its row. In Detailed each row is its own one-row card in its activity run, and a reply past eight is this list instead, once, at its first call in the window. An activity run of nothing but subagent calls is named by them ("3 subagents · 2m"); otherwise its label leaves them out, as the turn line does.
- **Subagent row:** the same one line in a reply's list, the live set and Detailed's one-row card, read like a tool row: 28px (44px on a coarse pointer), `caption`: the state glyph (the working mark, a `success` check, an `error` cross, the idle glyph, a `faint` dash), the identity dot, the name (`body`, truncated), how long it took and its tokens ("174K"), both `meta` `tabular-nums` `muted` in fixed slots so the columns line up; while it runs the time so far ticks in `accent`. A failed row adds its error under the name (`caption` `error`, one line, the whole in its tooltip); a stop that fails says why there (`role="alert"`). The row is one button named "name, state" (": error" when failed; `aria-haspopup="dialog"`), `tint-selected` while its transcript is open. On desktop, resting the pointer on it for 400ms opens its peek and a click opens its transcript; on a phone a tap opens its sheet. It has no context menu and no "…": Copy agent ID, Show where it was spawned and Stop are in the peek and the transcript. With more than one run the peek and the transcript say "3 runs" ("50+ runs" once the record, which keeps the first run and the newest 49, may have dropped some). In a list the row carries its `task` call's id, so a `locate` (from the index or "Show where it was spawned") opens the reply's chip and page, waits for the folds to open, scrolls the conversation so the row's top sits 16px under its top (never mid-touch on iOS WebKit) and flashes it; from the index it then opens the row's transcript.
- **Subagent peek (desktop):** one 384px `popup` per Task under the row (flipped to fit), moving from row to row with the pointer; it opens after 400ms and closes 120ms after the pointer leaves the row and the peek. Head: the state glyph, the name (`ui` 500 `ink`) and the state word in its tone at the right, then "2m 40s · 174K tokens · 19 tool calls" (`caption` `muted`, whatever is known; while it runs the time so far in `accent`), then the model and, with more than one run, the run count. Then, by state: running, its last four steps, one line each (a tool call's name and argument, a thought, a paragraph's first line; `code-sm` on `surface` with the `well` ring, or "Nothing recorded yet."); failed, its error (`code-sm` `error` on `error-wash`); otherwise its result line (at most four lines). Then "Spawned 17:05 by the main agent" (or by the subagent that spawned it) with **Show where it was spawned**; under a hairline, **Stop** (`danger`, while it runs, confirmed), **Full transcript ›**, and Copy agent ID at the right. A press on the row or **Full transcript ›** opens the transcript in its place.
- **Subagent transcript:** one per Task, and never beside the peek (a Task streams one subagent's transcript at a time). On desktop it is a 640px panel (`raised`, `lg` radius, the `modal` shadow) beside the row it was opened from (right of it, else wherever it fits; picked from the index without a row to land on, under the Subagents button), at most 70vh tall, over the page dimmed like a dialog's. On a phone it is a sheet from the bottom over the same dim: first the peek (without Copy agent ID and Show where it was spawned), then, after **Full transcript ›**, 85% of the screen with the transcript; dragging its handle down past 80px closes it. Head: the way back ("← name", when it was opened from the subagent that spawned it), the state glyph, the name (`ui` 600 `ink`), **Stop** while it runs (confirmed; the confirmation leaves the transcript open) and close; what it was asked (two lines); the state in its tone, the time so far while it runs, "2m 40s · 174K tokens · 19 tool calls" and the model (`caption` `muted`). Then a strip of `tint-well` pills: its runs, with more than one ("Run 2 · ✓ 1m 12s", the outcome in its state's tone, what started it and when in the tooltip), where a run scrolls the transcript below to its first item, paging older history in if need be (the first run to the transcript's start, a running one with nothing recorded yet to its end), or says "That run is not in the retained transcript."; nothing is written while the reader scrolls or touches it. A full record shows "Earlier runs not kept · latest runs" after Run 1 and numbers none of the later ones; a run without a recorded start cannot be picked. After them the subagents it spawned ("name Open"), each opening in its place with the way back; on desktop, **Show where it was spawned** (which closes it first) and Copy agent ID. Below, the subagent's own transcript (a `region` named "Transcript of <name>") on `surface` with the `well` ring fills the rest and scrolls on its own (`overscroll-contain`), ending with "Result sent to the main agent". There is no composer: a subagent takes no message from here. Esc, close or a press outside closes it and focus returns to the row; a subagent that is gone closes it. While it is open, its subagent stays in the live set.
- **Live subagents:** while a subagent runs, the live set sits at the conversation's foot, after the last reply and before the working line, in both densities; only in the main transcript, and only while the window reaches the live tail. It is not a card: a head line, then rows in columns like a reply's list. It holds the latest reply's subagents and any running subagent an earlier reply spawned. Its head (`caption` `tabular-nums`, `Bot` glyph) says "2 subagents running · 340K tokens", or "8 subagents · 1.2M tokens" and the counts once some have ended, with a 160px, 6px progress bar past five (done `success`, failed `error`, running `accent` at 45%, on `tint-well`; scaled with `transform`). Rows go by family as in a list, failed and running families first, 50 families, then "Show 50 more". Once none of the set runs and no transcript of it is open, the set goes, and it comes back at its first page. A subagent failing is said once, politely (`role="status"`, "Subagent <name> failed."). Its rows carry no call id; a reply's list does.
- **Subagent index:** the Task header's **Subagents** button keeps the count, a "+" while older ones wait in the record, the working mark while one runs, and a `ChevronDown`; it opens a 440px popover: a search box (name or line), status filters as pills with counts (All, Running in `accent`, Failed in `error`, Done; `aria-pressed`), then the subagents grouped by the user message that started their reply, newest first: the message's first line quoted and truncated, its time, "48 · 2 failed · 15 running" and **Jump**, then a 28px `caption` row per subagent like a tool row: state glyph, name (500 `body`, up to 60% of the line), its line in `muted` (`error` when failed), its duration and its tokens in `meta`. It renders 100 rows, then "Show 100 more"; subagents with no call, or whose call is not in the held history, group under "Earlier in the conversation", where picking one opens its transcript under the Subagents button; "Show older subagents" reads the next page from the provider's record. Picking a placed subagent closes the popover, scrolls to its row and opens its transcript beside it. When a jump cannot land (its call is past the history held), the reason is said above the composer, focused, with a dismiss button, and in the index while it is open.
- **Interaction card (pending):** `raised`, `lg`, the `float` shadow (it is the one thing in the transcript that asks for a hand); a `chip-attention` with the `ShieldQuestion`/`MessageCircleQuestion` glyph heads it; the request in a `sunken` mono well; buttons right-aligned (Deny `danger`, Allow for task `secondary`, Allow once `primary`; stacked full-width on phone). A question with one question, from a provider that takes answers, is not a card: it is the composer's extension (Composer, answer mode). A request with several questions keeps its own form on the card: the rows, a "Your answer" field for each question that takes text, Decline and Answer. Once decided it collapses in place (its last pending look, inert, over `slow`) and its record moves to its tool row (the approval mark) or its turn (the quiet row); nothing above it moves.

### Composer
The floating control plane: `raised`, `lg`, the `float` shadow, detached from the pane's foot by the gutters (16px below) and overlapping the transcript's last 40px, which fades out beneath it (the dock's gradient). Focus-within fades in the `focus-float` shadow on its pseudo-element: one step deeper and neutral, with no accent edge and no glow; the caret shows where typing goes. One surface: the textarea (`chat`, `field-sizing: content`, 56px to 40dvh) above one control row. From left: **Attach**, then the pickers; at the right the actions, with the primary button always last so Stop and the other action rise in beside it and nothing else moves. When the row runs short (side panels open beside the sidebar), it folds in a fixed order, the least important first, each fold keeping the ones before it (`lib/toolbarFold`): Permissions and execution drops the execution word ("Safe"); the credits become their glyph; Effort and context becomes its glyph; Permissions and execution becomes its glyph; both move into the **More** menu; the Model picker becomes its glyph; and only then do the actions wrap under the row. A folded control keeps its accessible name and its tooltip leads with the value ("Model: GPT-5 mini"). The Model's name may truncate, but only once the other pickers have moved into More and never below 48px (about five letters); it folds to the glyph instead, so no label shows as one letter. The row refolds when its width or contents change, by one attribute on the row, without rendering the composer. On a phone the effort/context, permissions and execution pickers fold into a **More** (`Ellipsis`) menu that holds the same groups, the credits become a `Coins` glyph and the cost estimate moves into their popover; the row stays one row at 420px. **The suggested reply** is ghost text in the empty textarea, in place of the placeholder: `chat` (`chat-lg` on a phone) in `muted`, in the textarea's own padding, on a layer over it and out of its flow, so it never changes the composer's height; it clamps to two lines with an ellipsis. Right Arrow or End in the empty box takes it; on a coarse pointer a `subtle` `icon` arrow button (44px target) at its end does the same. The textarea's accessible description carries it; the visible copy is `aria-hidden`.

- **Pickers**: Model (`Cpu`, value in `caption`; its tooltip adds "Latest turn ran on ‹model›" when routing differed), combined Effort and Context (`Gauge`, e.g. "high · 200K"; it names only what can change, and for a model that fixes both it reads "Default", disabled, with the reason in its tooltip), and **Permissions and execution**, one menu for how freely the agent acts: a Permissions group (Safe, Yolo) and, where the provider reports execution modes, an Execution mode group under a separator (Interactive, Autopilot, with the objective, reasons and Retry). Its value reads both, "Safe · Interactive" (the mode as reported, so "Plan" too; permissions alone where there are no execution modes), and its glyph is a four-step ladder of how freely the agent acts: Safe · Interactive `ShieldCheck` in `success` (safest), Safe · Autopilot `ShieldHalf` in `warning`, Yolo · Interactive `ShieldOff` in `attention` (unsafe), Yolo · Autopilot `ShieldAlert` in `error` (highly risky; the glyph only, the control stays neutral). The menu's first line and the tooltip say the step in words, in the step's tone. The pickers are separated by hairlines. The combined menu has two named sections; unsupported choices show their reason. Model/effort/context changes stay disabled during a turn. Permissions can change during a turn. Read-only values remain visible and cannot change.
- **Context ring:** a 16px ring after Model, absent until usage is reported. It is `accent`, `attention` at 80%, and `error` at 95%. A short radial tick in `ink` crosses the ring where the conversation starts compacting (Settings → New tasks, 80% by default). Its accessible name, tooltip and click/tap popover carry used/limit/percentage; the popover also says "Compacts at 80% (218K tokens)" and shows reported prompt/cache counts.
- **Credits:** only when the provider reports usage. A quiet chip shows remaining allowance, `attention` at 20% and `error` at 5%; the popover shows used/entitlement, a future reset date, Task AI units and stale state. The adjacent input-cost estimate uses current context, cache-read prices where known, and long-context pricing where applicable. It excludes output and stays absent when prices or context are unknown. Model choices carry the same estimate and reported cost tier/discount.
- **Background tasks:** the Task's background shells, after the pickers and only while it has any: a quiet chip with the working mark and how many run, or the `Terminal` glyph and how many there were once none runs ("Background tasks: 1 running" as its name and tooltip). A click or tap opens a popover (up to 384px, the list scrolling past 256px): each shell's description, its status (the `Running` chip, else the reported word), its command in `code-sm`, and **Stop** on a running one, which confirms first (Confirmations). Unknown status says so and disables Stop. It stays on a phone, where the row still fits.
- **Project strip:** lives in the Task header (branch, then the Changes opener with its count, or the no-Git warning in place of Changes and Files); the composer carries none. The Changes opener remains available on read-only Tasks, and closing Changes returns focus to it.
- **Actions** at right: while a turn runs, **Stop** (a round ink button like Send, square glyph; red is for errors and destructive confirmations, never a control at rest), then one primary split pill of two `icon-md` halves (44px on a coarse pointer) joined by a 1px `on-primary` rule: a glyph with no visible text, named (`aria-label`) and drawn for what Enter does (the setting: **Send now** (steer) by default with `ArrowUp`, the same arrow as Send, since it goes at once; or **After this turn** (queue) with `ListEnd`, the waiting strip's glyph, since that is where it lands), and a `ChevronDown` half ("More send options") that opens a menu above it holding the other action, in words, with its shortcut; on a phone it stays on the toolbar's row, wrapping under it only when the row cannot hold every 44px target. Otherwise **Send** (round primary, `ArrowUp`). Every action is a glyph; its name is for screen readers and its tooltip, which says what it does and carries the shortcuts: Enter does the named action, Ctrl+Enter (⌘+Enter on a Mac) the other, Shift+Enter adds a line. Up from the first line (or an empty textarea) recalls the Task's previous prompts newest first, shell-style, with the caret at the end; Down from the last line comes forward and, past the newest, restores the draft, as does Escape. Editing a recalled prompt makes it the draft; chips and uploads are untouched. An open inline picker takes Up and Down first. When a steer is impossible (the provider cannot steer a running turn, or the draft's model settings differ from the turn's) Send becomes After this turn and Enter queues; Send now stays in the menu, disabled, with its reason under it, and a note above the textarea says why. Read-only Tasks show no send controls.
- **Answer mode:** while a question with one question waits on the open Task, the composer is its one place: the question is the composer's extension, above what answers it, on the same surface. From the top: the `chip-attention` with the `MessageCircleQuestion` glyph ("Needs answer", the card's cue), the question as markdown in `ui` (its header in `caption` `muted` above it), and its choices as selectable rows (a radio, or a checkbox where several may be chosen; 32px, 44px on a coarse pointer; the chosen row fills `tint-hover`); the chosen option shows there and nowhere else, and choosing the chosen radio again clears it. Every question takes a typed answer (Copilot's `ask_user` accepts one even when the agent sent `allowFreeform: false`), and the answer is exactly one of the chosen options or the typed text: typing clears the chosen options, and choosing an option clears the typed text. An option whose label ends with "(Recommended)", in any case (the first, if several do), arrives chosen, once per question: a change or a clear stands, and its label shows and is sent as offered. The question and its rows scroll inside a box capped at min(240px, 30dvh), never the surface, with no mask; a fading rule separates them from the textarea. The placeholder reads "Type your answer…" when nothing is chosen and "Or type your own answer…" when an option is ("Choose an option above…" for a question that takes no free text). The action row reads Stop · Decline · **Answer**: Decline is a round `icon-md` `danger` button with the `X` glyph, named "Decline" ("Decline to answer this question" its tooltip), that rises in beside Stop and leaves with the question (the same enter and exit as Stop); the primary is the round Send, named **Answer**, with `ArrowUp` (Enter; Ctrl+Enter does the same, Shift+Enter adds a line; the send-mode setting does not apply), enabled by a chosen option, or by typed text where free text is allowed. Nothing goes with an answer: Attach stays visible, dimmed and `aria-disabled` with "Answer the question first." as its reason, a dropped or pasted file is refused with the same words as a `warn` note (no drop overlay), and `@`, `/` and `$` open no picker (an answer is plain text, never a file or a command). The mode has its own buffer: the Task's draft is parked when the question arrives, is what the storage keeps meanwhile, and comes back when the question resolves, however it resolves (answered, declined, withdrawn, answered from another tab); an answer left unsent stays only where the draft was empty. Sending: the typed text is the answer, else the chosen options. A 409 or 410, on Answer or on Decline, shows the card's wording as a `warn` note here. A question arriving on a desktop focuses the composer when nothing else holds focus, never on a touch screen.
- Notices (read-only, uncertain, rejected, errors) sit in a strip above the textarea, separated by spacing alone; the queue is a disclosure strip with numbered rows, a per-row cancel, Resume and Clear.
- **Inline pickers:** typing `/` or `$` as the first character or `@` at the start or after whitespace opens a level-2 listbox above the textarea, full width on phone and 440px from `sm` up, max height min(300px, 40dvh). Rows are 30px (44px on touch); the highlighted row is `hairline` under `ink`, its secondary text a step darker (`muted` to `body`, `faint` to `muted`) so it stays readable there; `/` groups rows under Commands and Skills, `$` offers only Skills, and `@` lists files and folders in `caption`. Focus never leaves the textarea, which drives the list through `aria-activedescendant`: Up and Down move, Enter or Tab picks, Escape dismisses. Loading, empty, and reason lines (for example, an unavailable provider catalogue) sit inside the popover.
- **Command outcomes and execution:** alias search and literal argument choices reuse the inline picker. Disabled commands carry a reason and never fall through as prompts; catalogue failures hold slash input with Retry commands. Command results use a compact composer strip for text or explicit subcommand choices, or open existing controls. The strip holds only confirmations and choices, one short line or a list to pick from; a native command's longer output (Markdown, or text of more than one short line: `/context`, `/usage`, `/env`, `/skills`) opens in the **command output panel** instead, a right side panel like Files (`SquareSlash` + the command, a close button; inline from the side-panel breakpoint, a sheet below it) that replaces the other right panels and is replaced by them, so the composer never grows with it. A press in the composer leaves it open (the next command is typed there), a press in the conversation closes it with the other inline panels, and closing it returns focus to the conversation only when focus was inside it; a polite status line says where the output went. Its plain output is terminal output: it keeps its line breaks and is set in `code-sm` mono like a tool's output; Markdown output renders as Markdown. The body is a labelled, keyboard-reachable scroll region; a new run replaces the output and starts at its top. Execution mode shares the Permissions and execution menu in the composer toolbar; its group shows objective state, reported turns, credits, limits and reasons. Unknown observations stay qualified. Stop remains available during autonomous continuation, including between turns, and never optimistically claims completion. Safe/Yolo remains the separate permission policy.
- **Chips row:** picked `@` files and uploads sit in a wrapping row above the textarea. An upload chip is a 44px `sunken` well holding a 36px thumbnail (or the kind's glyph), the name, and its state: `Uploading… 42%` with a 2px `accent` bar along the bottom edge, size and kind when done, or the refusal in `error` on an `error-wash`. A file-reference chip is a smaller chip with `@`. Each chip has a remove button. Queued prompts repeat these as 20px caption chips.
- **Attach:** a subtle `Paperclip` icon button opens the control row at the left. Its tooltip gives the model's media gate and the limits (images 3 MiB, PDF 10 MiB, text 256 KiB, 5 per message). It never disables for the model, since text is always allowed: the gate narrows the file picker and the tooltip names it. Once a message holds the most uploads it can carry, the button stays visible, dimmed, `aria-disabled`, and says why. Read-only Tasks show no composer controls, so no Attach button. Paste and drag-and-drop also work; while files are dragged over the composer, a `raised` overlay with an inset dashed `accent` outline shows "Drop to attach" and the same note (`fade-in`).
- **Transcript attachments:** a user turn's images render as thumbnails up to 160px high; hover lifts the shadow and scales the image to 1.02. Clicking one opens the lightbox: no card, the image (max 94vw by 1400px, 78dvh, `sunken` fill for transparent images, `modal` shadow) on the see-through `scrim`, its name and details in `on-primary` above with the close button, **Open original** below; a press outside the image closes it, and it scales from 0.98 and fades (`slow`). Other files are 32px chips with glyph, name, and size that open the stored copy; without a stored copy the chip is inert. A PDF the provider did not pass to the model as a document (`not_native`, from the provider's own record of the message) adds one `caption` `warning` line with `TriangleAlert` under the chips: the agent got the file to read with the tools on this machine instead, which can miss scanned pages, images and layout, or fail without a PDF text tool. Images, text files and a PDF the model received get no line.
- Phone: the control row stays one row (More menu, glyph faces); controls have 44px coarse-pointer targets and the textarea is 16px.

### Since you left and the finish card
**Since you left** is one line under the Task header, outside the scroller, on an `info-wash` `md` strip: the `History` glyph in `info`, "**Since you left** (42 min): …" in `ui` `ink`, then **Jump to where you stopped** (`subtle`, `info`) and a dismiss ×. It wraps to two lines on a phone. It is built from the Task's own record only (lib/evidence.ts). **The finish card** sits at the end of the transcript under a completed turn: a `tint-well` `lg` card on the `raised` shadow, one step below the canvas, "Finished — check the evidence" in `title`, a `warning-wash` chip "N claims not verified", then rows divided by hairlines: each check (a `success` check, an `error` cross, or a dashed `warning` circle when unclear) with its plain name ("Ran the tests"), the command in `code-sm` and its exit status, counts and time in `caption` `muted`, and **Show output** (`subtle`, `accent`); then each claim quoted in `ui` with what backs it in `muted`, or "Not verified · why" in `warning`. Then an `eyebrow` "Changed in this turn" list (each row opens Changes), the commit panel's slot, and the secondary action **Review changes** (full width on a phone). No model writes any of it.

### Menus, context menus, tooltips, selects, dialogs
All Base UI. Menus: `raised`, `md`, 4px padding, the `float` shadow (its ring is the edge), 30px items (44 on coarse pointers), highlighted item one surface step, destructive item in `error` with `error-wash` highlight, disabled items dimmed with their reason in `caption` beneath, groups separated by a fading rule. They scale from 0.97 and fade over 100ms from the transform origin, as do popovers, the selects' lists and tooltips. Context menus open at the pointer (long-press on touch) and hold exactly the items of the matching "…" button. An item's action runs once the menu has finished closing, and an action that takes focus itself (inline rename) tells the menu to leave focus alone (`takesFocus`), so the menu's focus return never undoes it. Tooltips are `ink` on `on-primary`, 400ms delay, no delay while another is open; they are hints for sighted users and never the only name of a control. Selects (project dialogs, Settings) are filled `sunken` triggers like inputs and open below with a check on the chosen row. Dialogs: `raised`, `lg`, 20px padding, 440px max (560px, `--spacing-sheet-wide`, while Add project browses folders), the `modal` shadow on the backdrop, scaling in from 0.97 with the backdrop's fade; a close button in the title row; on phone they become a bottom sheet with full-width buttons. A dialog is never taller than the viewport less its 16px gutters: the title row and the footer (Cancel, the primary) stay and the body scrolls between them. Confirmations (`AlertDialog`) focus **Cancel** first; Archive and Delete always confirm.

### Confirmations
Every destructive action goes through an `AlertDialog` (`ui/dialog`; the state of one confirmation is `useConfirm`, which keeps its target through the exit so the copy never changes on screen). Destructive means data is lost or a running process is killed: **Delete task**, **Remove project**, **Remove provider** and **Remove model** (Settings, custom models), **Cancel** a queued prompt and **Clear** the queue (their text is not kept; the dialog quotes the prompt), **Stop** a background task (it kills the shell; the dialog names the command) and **Stop** a subagent (its work ends; the main agent gets no result). **Close conversation** and **Archive** confirm too, since neither can be undone from the web interface. The copy is one shape: the title names the verb and the object ("Remove provider ollama?", "Stop subagent “Survey templates”?"), the description is one line on the consequence, the confirm button carries the verb in `danger` (a `sunken` fill), and **Cancel** (or **Keep**, where "Cancel" would be the action itself) takes focus first, so Enter never destroys by accident; Escape cancels. One confirmation guards a risk rather than a loss: switching into **Yolo with Autopilot** (picking Yolo while autopilot runs, Autopilot while in yolo, or a typed `/allow-all`, `/yolo` or `/autopilot` that lands there) asks "Switch to Yolo with Autopilot?" with **Switch**; the other three steps switch at once. Nothing else confirms: **Stop turn** and Esc stay instant (they only interrupt, and the Task can continue), a hidden model is a switch, and **Log out** is a click.

### Folder picker
The Add project dialog's path field keeps a **Browse** button (`FolderOpen`, secondary, `aria-expanded`) beside it, and under the field a **Recent folders…** select (the shared Select) fills the field with a recent directory. Browse opens the picker inline under the field through the shared height collapse while the dialog widens to 560px over `slow`, so the dialog grows instead of jumping, and settles back once a folder is chosen. Nothing floats: the picker is a column of rows in the dialog (`components/FolderPicker.tsx`).

- **Breadcrumb.** The folder being shown as `caption` segments, root first, each a 24px button (44 on coarse pointers; `muted`, the last `ink` with `aria-current="location"`), `/` separators in `faint`, wrapping when deep. At its right, flush with the well's edge, a **Show hidden** toggle (`EyeOff`/`Eye`, ghost, `aria-pressed`) that lists the folder again with dot-folders (`hidden=1`).
- **The well.** A `canvas` well (`sm`), fixed at `--spacing-picker` (min(320px, 40dvh)) so moving between folders never changes the dialog's height; on phone `--spacing-picker-phone` (40dvh), so the Name field and the sticky footer stay reachable. Inside, a `role="listbox"` whose only children are the `role="option"` rows: a `Folder` glyph, the name in `ui`, and marks in `caption` `muted` for a git repository (`GitBranch` + "git") and a symbolic link (`Link` + "link"); hidden names are `muted`. Rows are 32px (44 on coarse pointers); hover is one step up (`surface`), the selected row is `raised` with `ink` and the 1px shadow. Each row is one button; the `ChevronRight` at its end is a 32px (44 on coarse pointers) hit region of that button, not a control of its own: a press there opens the folder. Status lines (loading, empty, error, "Showing the first 1,000") sit in the well above or below the listbox, never inside it.
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

Only the attention chip has a fill. Chrome icons are lucide, 12–16px, one stroke weight. The Copilot provider mark uses its official Primer SVG. The working mark is one component (`WorkingMark` in `common.tsx`) and the one sign of agent work in progress: the sidebar rows, the Task header, the transcript's live foot line, the subagent "Running" chips and the Subagents header button, and the background tasks' toolbar chip and "Running" chips all move alike. The plain spinner means UAM itself is waiting on a request; tool rows keep it too, since a single tool call in flight is not the Task working. While a turn runs, the **working label** floats centred just above the composer, over the dock's fade and outside the scroller, so it stays in view at any scroll position and on any history page: a 28px `raised` label, `sm` radius, on the `float` shadow, with the mark, one calm gerund ("Untangling…", "Sifting…", shimmering; the list is `lib/verbs.ts`) and the recorded foreground work time, excluding waits for manual approvals and answers (`tabular-nums` `faint`, in a slot three characters wide, so the label keeps its width for the first hour; steering does not reset it; without a trustworthy start, no time). It stays while subagents still run after the turn ended, timed from the first of them to start. It says only that the agent is working and for how long; what it is doing stays in the transcript. The one exception: while the conversation compacts, "Compacting the conversation…" replaces the gerund here and at the foot of the transcript (and the screen reader hears that instead of "Busy"), since the agent is not answering then. It never moves: while it shows, **Jump to bottom** is a 28px arrow-only button (same `secondary` fill and `float` shadow, "Jump to bottom" as its name and tooltip) pinned 8px to its right, outside its centring; with no turn running the button keeps its label and takes the centre. It comes and goes through `Appear`, and the screen reader hears "Busy" once. In Compact no row is live, so the foot of the transcript, where new output lands, holds the **live step**, the step in progress unfolded: a thought streaming is the mark and "Thinking…" (shimmering) over its text as it arrives (`md-quiet`, `ui` `muted`, behind the 2px fading left rule); a call running is its tool row (spinner, name, argument; the same row as in the timeline, so it expands and has its menu) over the tail of its output (`code-sm` `muted` on `code-bg`, indented under the row). The text sits in a box at most 80px tall that shows its newest lines and clips the older ones above it: it never grows into a block, never scrolls and never writes `scrollTop`. Each step rises in (`rise`, transform and opacity); when it ends it leaves the foot and the turn line counts it, and the next step takes its place. Once a turn has shown a live step, the foot keeps that room (108px, 128px on a coarse pointer) until the turn ends, so a step folding into the turn line never pulls the transcript up and stick-to-bottom never jumps. A call waiting for permission or an answer, a question and a subagent's `task` call are not unfolded: the foot names them in one line, "Waiting for your approval: …" in `attention` or "Running: task …", with the mark, and their cards and rows stay where they were. Between steps the foot stays, blank; it grows in when the turn starts and folds away when it ends through the height collapse, leaving only the turn line. In Detailed the live activity row names the step and the transcript has no foot line. A subagent's transcript in its panel keeps its own foot with the same live step, the gerund between steps (hidden in Detailed while the last row is already live). The verb is chosen once per turn by hashing the id of the user message that began it, so it never cycles or flickers and reads the same after a reload; it is never "Working", "Thinking" or "Running", which name other things. Nothing in the transcript moves for it.

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
- **Sidebar collapse** animates the shell's first `grid-template-columns` track 320px ↔ 48px (`slow`); the sidebar keeps its width inside the clipped column and is `inert` once collapsed, and the rail fades in over it (`fade-in`).
- **Every disclosure** (shelves, turn lines, activity rows, tool runs, tool rows, thinking, the folder picker, a decided card) opens and closes through one component, `Collapse`, which animates `grid-template-rows` 0fr ↔ 1fr (`slow`); collapsed content is `inert`, and a closing owner stays mounted until `onClosed`. A turn line, activity row, tool run, tool row and panel row mount their content on the first open (`appear`), so the closed transcript carries none of the hidden code blocks. Chevrons rotate (`base`). Nothing uses a native `<details>` snap.
- **Rows** step surfaces in `fast`; the meta text and the "…" button cross-fade in place; inline rename swaps without layout shift. A **state change** fades the new state glyph in over the old one's 16px slot and steps the status word's and the chip's colour (`base`, `StateMark` and `Chip`), so a row turning from Working to Needs permission changes without a snap.
- **Buttons:** the filled ones (primary, secondary, danger; an action) press to 0.97 (`fast`); ghost and subtle buttons only tint.
- **Lift** (`.lift`): Task cards and image thumbnails rise 1px on hover (transform, `base`) while the `float` shadow drawn on their pseudo-element fades in (opacity); the shadow itself never animates. **Switch** and **segmented** thumbs slide on their transform (`fast` / `base`). The composer's deeper focus shadow fades in on its pseudo-element (`base`).
- **Menus, context menus, pickers, selects, popovers and tooltips** scale from 0.97 at their transform origin and fade (`fast`); **dialogs** scale from 0.97 and fade with the backdrop (`slow`); the **drawer** slides from the left (`slow`); overlay panels slide in from the right and back out, with the backdrop fading both ways; inline panels slide over their committed space and slide out before they leave. A tooltip that is not open never closes on its hover path: a view transition's snapshot makes the browser report the pointer leaving a hovered trigger, and Base UI's hover close flushes synchronously, which would cancel the transition (`Tip` cancels that close, so "New task" under the pointer still cross-fades).
- **Task switching:** selection and snapshots render immediately, without a view transition or a pane entrance animation. The previous Task stays mounted but inert while the selected Task loads.
- **Task list:** React's `<ViewTransition>` with the "sessions" type lets rows rise in, fade out and slide to their place (`slow` for the move, `fast` for the cross-fade). The page root is `view-transition-name: none`, so nothing else snapshots and streamed text keeps painting live; every other render leaves rows to their CSS colour transitions. The first load of the list is a skeleton; the list fades in (`base`) once it fills.
- **Transcript:** new rows rise in, tool rows one by one as they arrive; streaming text appends without animation; the turn status row keeps its slot while the turn runs until "Took" lands in it, and in Compact its counts change in place; "Thinking…" shimmers `muted` → `body`; the working mark orbits and breathes; the locate action (a subagent's row) flashes `accent-wash` for 1.4s; a rendered diagram fades in (`base`) over the code it replaces; the working label and the "Jump to bottom" button appear and leave through `Appear`.
- **Appear** (`ui/appear.tsx`) is the one enter/exit for a small control in a fixed slot: it fades and scales from 0.9 (`base`), stays mounted and inert through its exit, and takes the control's place in a row so nothing beside it moves.
- **Notes and strips** (`Note`, the connection, update and error strips at the top of the pane) fade in (`base`); nothing slides. The loading veil fades in and out (`base`).
- **Composer:** Stop appears beside the fixed primary when a turn starts and leaves when it ends (`Appear`); the queue strip opens and closes through `Collapse` (`slow`), holding its last list through the exit, and never grows in when the composer mounts with one; "Resend last prompt" rises in; the send button presses like every primary.
- **Reduced motion:** honoured only when Settings → Motion is **Match system**; the default, **Always on**, animates even when the OS asks for reduced motion (Windows with animation effects off, Remote Desktop). The choice is kept per browser (`uam.motion`) and applied as `data-motion="system"` on `<html>` by the entry module before the first render and at once when changed. Then `@media (prefers-reduced-motion: reduce)` disables every transition and animation, view-transition pseudo-elements included, and the `motion-reduce:` variant (redefined with `@custom-variant` to require the attribute) applies; the working mark stands still, disclosures, panels and the sidebar snap, and every exit still reports on its timer so nothing lingers.

No motion library; everything is CSS transitions and keyframes through Tailwind (`animate-rise`, `animate-fade-in`, `animate-slide-in`, `animate-pulse-dot`, `animate-spin`, `animate-shimmer`, `animate-sweep`, `animate-flash`, `animate-orbit`, `animate-breathe`) and two presence components, `Collapse` and `Appear`. Only `transform`, `opacity`, colours and `Collapse`'s `grid-template-rows` animate: never a shadow, a filter, a size or a position; nothing reflows the transcript per frame, no moment adds layout shift, and no surface uses `backdrop-filter` (remote desktops).

## Accessibility

- **Focus:** a plain ring, never a glow. `:focus-visible` is the 2px `focus` outline, 1px out, with no halo; inside rows the ring is inset. Text fields replace the outline with a lift (`raised` fill and the `focus` shadow), never a coloured edge; select triggers keep the ring; the composer shows focus as its deeper neutral `focus-float` shadow and the caret. Base UI traps focus in dialogs and the drawer and returns it to the opener.
- **Keyboard:** the sidebar as described; Ctrl/Cmd+B collapses it to its rail and expands it (the drawer on a narrow screen), and focus on the sidebar or the rail follows the toggle so nobody is left on an inert element; Alt+N opens the New task palette, where arrows, Enter, Esc and Alt+1…9 work as in the filter dropdown; Esc closes the topmost popup, then a panel, then the Changes sheet; Enter/Shift+Enter/Ctrl+Enter in the composer, following the send default; arrow keys on the panel handle and the segmented control; in the folder picker, arrows, Enter, Backspace, type-ahead and Ctrl/Cmd+Enter as described.
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
- **A push-only service worker.** The app manifest (`/manifest.webmanifest`, served as `application/manifest+json`, `no-cache`) and the icons are enough for Chromium to offer install (`Page.getInstallabilityErrors` is empty on desktop and mobile). `/sw.js` (`no-cache`, `script-src 'self'`) is registered only once notifications are turned on, and handles `push` and `notificationclick` alone: no fetch handler and no cache, so the interface stays a live view of the service and nothing sits between the page and `/api/*`, the event stream or the immutable `/assets/`. Updates come from the service's version in `/api/meta` (see Layout, Shell). The manifest link carries `crossorigin="use-credentials"`: without it a browser fetches the manifest with no cookies, so a sign-in proxy in front of the service answers with a redirect to its login page, which `default-src 'self'` blocks.
- **The terminal draws with WebGL, never xterm.js's DOM renderer.** The DOM renderer styles its rows through `<style>` elements, which the policy blocks, so its text comes out unstyled. The WebGL addon loads before the terminal opens, so the DOM renderer is never created, and a WebGL2 probe runs first, because xterm.js falls back to it silently when WebGL fails as it opens. There is no fallback: without WebGL the panel says so. The WebGL renderer still appends one `<style>` holding the scrollbar slider's colours; it is blocked too (two `style-src-elem` reports per terminal opened, the only violations recorded with a terminal open), and index.css carries the same rules, from the theme's `ink`.
- **Mermaid runs in a sandboxed frame, not in the page.** It cannot render without a `<style>` element and `style` attributes, so it lives in `/diagram-frame.html`, embedded as `<iframe sandbox="allow-scripts">` (opaque origin: no cookies, no storage) and served with its own policy: `default-src 'none'; script-src 'sha256-…'; style-src 'self' 'unsafe-inline'; img-src data:; font-src 'self'; connect-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'`. The page posts the source, gets the SVG string back and shows it only as a `data:` image, which `img-src` already allows. The page's own policy is unchanged, and the frame's script is a classic script, since a module script from an opaque origin needs CORS, inline in the document and allowed by its hash, since the frame's own requests carry no cookie and a sign-in proxy would redirect a separate script to its login page (ADR 0004, "Diagrams in a sandboxed frame").

## Do / Don't

### Do
- Step surfaces by one shade and one elevation level to show depth; a seam that must still be a rule fades out at its ends.
- Keep the assistant flat and give the user the bubble.
- Reserve `attention` for states that need a human: a dot, a row word, a chip. Never a button fill.
- Use a badge tone only inside a Project badge, a chart's series or a subagent's identity; a tone never stands for a state.
- Set code in the conversation (code blocks, inline code, tool rows, request commands, native command output) and diffs in JetBrains Mono at 11–13px; everything else, identifiers included, in Figtree.
- Truncate rows; wrap nothing there. Put every context-menu action on a "…" button too.
- Collapse turn lines, activity rows and thinking by default, collapse the ledger once everything is done, keep the shelves collapsed.
- Keep a turn's paragraphs uninterrupted: thoughts and routine calls are counts on the turn line; only a question, a permission, the turn's edits and what a call produced for the person (its images, a declared file's card, never the call's own row) stand in the answer; a failure is the turn line's `error` count.
- Let the transcript and the composer fill the main pane; only the gutters and a user bubble's own cap bound them.

### Don't
- Don't draw a border at all: a surface has a ring in its shadow, a field is filled, a seam fades. A card's children use indents and sunken wells.
- Don't add a coloured primary button; the primary is ink.
- Don't use pills for controls. The composer Send/Stop circles and the transcript's activity and tool rows are the explicit exceptions; other controls keep the shared rectangular shapes. Don't fill a control at rest with `error`: Stop is ink.
- Don't use a serif, weights outside 400–600, an eyebrow above a heading, or scaled markdown headings in chat.
- Don't fill chips except the attention chip.
- Don't show heuristics or previews in rows; rows show server state.
- Don't put a colour on a ready row; ready is a time.
- Don't put the transcript or the composer back in a centred fixed-width column, or make the sidebar resizable; it is fixed or collapsed to its rail, nothing between.
- Don't animate a shadow, a filter or a size, add `backdrop-filter`, mask a scroll container, or add per-row shadows to a long list (the Task cards' static `raised` shadow is the one exception: the active list is short and the shadow never animates); lift and deepen shadows through a pseudo-element's opacity, never add a coloured glow, and fade scroll edges with small overlays.
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
