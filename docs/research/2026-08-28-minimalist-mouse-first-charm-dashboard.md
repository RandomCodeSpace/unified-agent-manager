# Minimalist mouse-first Charm dashboard research

Date: 2026-08-28  
Scope: official Bubble Tea, Bubbles, Huh, and Lip Gloss documentation, tagged
source, and examples; plus this checkout's current TUI contracts. Security is
out of scope.

## Conclusion

Use one coordinated Charm v2 stack and keep only the UAM-specific projection as
application code. Bubbles supplies the viewport, help, key bindings, and
spinner. Huh owns the confirmation form and choices. Lip Gloss owns styling,
layers, composition, and hit testing. Bubble Tea owns the event loop and the
displayed view's pointer callback.

Neither Bubbles `list` nor `table` is a mouse-first session action grid: both
navigate by keyboard, `table` has fixed application-supplied column widths, and
neither owns click hit-testing. Wrapping either in enough custom behavior to
meet the pointer and responsive contracts would add a second component model.
The selected design instead renders UAM's fixed-height domain rows inside a
Bubbles viewport and derives semantic action layers from the same immutable
frame. Those layers are layout glue, not replacement widgets.

The current stable Charm line is Bubble Tea v2.0.9, Bubbles v2.2.1, Huh v2.0.3,
and Lip Gloss v2.0.6
([Bubble Tea releases](https://github.com/charmbracelet/bubbletea/releases/tag/v2.0.9),
[Bubbles releases](https://github.com/charmbracelet/bubbles/releases/tag/v2.2.1),
[Huh releases](https://github.com/charmbracelet/huh/releases/tag/v2.0.3),
[Lip Gloss releases](https://github.com/charmbracelet/lipgloss/releases/tag/v2.0.6)).
That is a coordinated major migration. It is required for Bubble Tea's
displayed-view mouse callback, Lip Gloss compositor hit testing, and the current
Huh form line, so it was treated as one migration with compile and regression
gates rather than as four independent dependency bumps.

The v2 minimums are Go 1.25.0 for Bubble Tea, Bubbles, and Lip Gloss, and Go
1.25.8 for Huh
([Bubble Tea go.mod](https://github.com/charmbracelet/bubbletea/blob/v2.0.9/go.mod),
[Bubbles go.mod](https://github.com/charmbracelet/bubbles/blob/v2.2.1/go.mod),
[Huh go.mod](https://github.com/charmbracelet/huh/blob/v2.0.3/go.mod),
[Lip Gloss go.mod](https://github.com/charmbracelet/lipgloss/blob/v2.0.6/go.mod)).
UAM's toolchain 1.25.14 clears the compiler floor, but adopting Huh v2 makes Go
1.25.8 the effective module floor and may require raising UAM's `go 1.25.0`
directive. The compiler itself is not the migration blocker.

## Verified repository baseline before this change

- UAM pins Bubble Tea v1.3.10 and Lip Gloss v1.1.0; it does not currently depend
  on Bubbles or Huh ([go.mod](../../go.mod#L7-L14)).
- The launcher already uses alternate-screen mode and cell-motion mouse
  reporting. `UAM_NO_MOUSE` restores native drag-to-copy
  ([cli.go](../../internal/cli/cli.go#L307-L336)). This is the correct mouse
  mode for clicks, releases, wheel events, and drag motion without idle hover
  traffic; Bubble Tea documents all-motion as less widely supported and useful
  for hover
  ([v1.3.10 options](https://github.com/charmbracelet/bubbletea/blob/v1.3.10/options.go#L122-L165)).
- The root model handles `WindowSizeMsg`, `MouseMsg`, and `KeyMsg`
  ([app.go](../../internal/app/app.go#L265-L340)). Left presses select/activate
  rows or invoke a wide-layout gate; wheel up/down changes selection
  ([app.go](../../internal/app/app.go#L851-L880)).
- Rendering already budgeted the header, body, and bottom region and truncated
  the final frame. It also maintained application-owned mouse geometry. The
  redesign preserves those useful constraints while removing the command
  composer and making the displayed Lip Gloss compositor the hit-test source
  of truth.
- Current confirmations and the new-session wizard replace the primary body
  inside the same responsive frame rather than launching a second screen
  ([app.go](../../internal/app/app.go#L1620-L1680),
  [app.go](../../internal/app/app.go#L1712-L1728)).

## Components that fit this dashboard

| Component | Verified capability | Fit and gap |
|---|---|---|
| UAM domain row projection | Flat rows, responsive field omission, filtering, selection, semantic session actions | Keep only as the thin domain-to-view projection inside Bubbles viewport and Lip Gloss layers. It is not a reusable local widget. |
| Bubbles `list` | Cursor navigation, filtering, pagination, status text, spinner, help, custom item delegates, and `SetWidth`/`SetHeight` ([source](https://github.com/charmbracelet/bubbles/blob/v2.2.1/list/list.go#L147-L258), [sizing](https://github.com/charmbracelet/bubbles/blob/v2.2.1/list/list.go#L624-L705)) | Viable for a conventional searchable list. Its built-in title/status/pagination/help is more chrome than this dashboard needs, and its update path has no mouse branch ([update](https://github.com/charmbracelet/bubbles/blob/v2.2.1/list/list.go#L819-L914)). |
| Bubbles `table` | Header/row rendering, cursor selection, paging, styles, and viewport width/height ([source](https://github.com/charmbracelet/bubbles/blob/v2.2.1/table/table.go#L129-L230), [sizing](https://github.com/charmbracelet/bubbles/blob/v2.2.1/table/table.go#L322-L341)) | Useful as a keyboard table, not a mouse-first action grid. It accepts fixed column widths and handles only key presses; setting table width does not recalculate columns. |
| Bubbles `viewport` | Fixed width/height, clipping, optional fill, soft wrap, horizontal/vertical scrolling, and wheel scrolling ([source](https://github.com/charmbracelet/bubbles/blob/v2.2.1/viewport/viewport.go#L43-L178), [mouse update](https://github.com/charmbracelet/bubbles/blob/v2.2.1/viewport/viewport.go#L656-L724)) | Own final roster offset, clipping, and fill. UAM must still map selection to a logical offset and route wheel movement because the component does not select semantic session rows or expose cell buttons. |
| Bubbles `key` + `help` | Central key bindings and width-aware short/full help; help truncates when its width budget is exceeded ([help source](https://github.com/charmbracelet/bubbles/blob/v2.2.1/help/help.go#L77-L162)) | Good low-risk extraction if UAM wants generated keyboard hints. Mouse affordances still need separate labels. |
| Bubbles `textinput` | Focused single-line editing, completion, cursor behavior, and explicit width ([source](https://github.com/charmbracelet/bubbles/blob/v2.2.1/textinput/textinput.go#L90-L205)) | Reasonable for command/filter input if its behavior is preferable to UAM's current shared `input` string. It is not required for the board redesign. |
| Huh `Form` / `Confirm` | Embeddable form state, validation, completion/abort states, explicit width/height, inline confirm labels, and configurable keys ([form source](https://github.com/charmbracelet/huh/blob/v2.0.3/form.go#L104-L180), [confirm source](https://github.com/charmbracelet/huh/blob/v2.0.3/field_confirm.go#L43-L165)) | Good for keyboard-driven modal forms. Huh's tagged source contains no mouse-event handler; click targets remain application work. |
| Lip Gloss | ANSI-aware width/height, joins, placement, styles, truncation, and, in v2, layers/compositing/hit tests ([layout docs](https://github.com/charmbracelet/lipgloss/blob/v2.0.6/README.md#measuring-width-and-height), [layer source](https://github.com/charmbracelet/lipgloss/blob/v2.0.6/layer.go#L10-L124)) | Keep for geometry. The v2 compositor materially helps true overlaid dialogs, but v1.1.0 has placement/joining rather than layers. |

`tree`, `filepicker`, `textarea`, progress, timer, and stopwatch solve different
problems. A spinner is justified only while a user-visible load is actually in
flight; decorating a two-second refresh loop with permanent animation would be
busywork in the literal sense.

## Mouse support and exact gaps

### Verified capabilities

Bubble Tea v1 cell-motion mode emits click, release, wheel, and drag events and
falls back from SGR to X10 coordinates when needed
([v1.3.10 option](https://github.com/charmbracelet/bubbletea/blob/v1.3.10/options.go#L122-L142)).
Bubble Tea v2 makes the event kinds distinct (`MouseClickMsg`,
`MouseReleaseMsg`, `MouseWheelMsg`, `MouseMotionMsg`), keeps zero-based
coordinates, and moves mouse mode into the returned `tea.View`
([mouse source](https://github.com/charmbracelet/bubbletea/blob/v2.0.9/mouse.go#L7-L143),
[view mouse modes](https://github.com/charmbracelet/bubbletea/blob/v2.0.9/tea.go#L283-L305)).
V2 also adds `View.OnMouse`, and the official clickable example combines it
with Lip Gloss layer IDs and compositor hit-testing
([example](https://github.com/charmbracelet/bubbletea/blob/v2.0.9/examples/clickable/main.go#L228-L260)).

Bubble Tea provides events, not semantic buttons. Bubbles `viewport` consumes
wheel messages, but `list`, `table`, and `tree` do not implement click selection.
Huh fields likewise consume keys, not mouse events. Therefore row selection,
gate/button bounds, press/release semantics, modal shielding, and cursor shape
are application contracts in either Charm generation.

The viewport wheel handler does not inspect pointer coordinates. Forwarding one
wheel message to multiple viewports scrolls all of them; the parent must
hit-test and route it to exactly one. Its v2 default is three lines per wheel
event
([source](https://github.com/charmbracelet/bubbles/blob/v2.2.1/viewport/viewport.go#L145-L152),
[handler](https://github.com/charmbracelet/bubbles/blob/v2.2.1/viewport/viewport.go#L696-L721)).

### Repository gaps worth fixing in a mouse-first pass

- Wheel events currently move selection before any row/body/modal hit-test.
  Scrolling over a footer or open modal therefore still moves the hidden board.
  Scope wheel handling to the visible board rectangle.
- Compact rows intentionally have no separate gate bounds: the first click
  selects and a second click activates. Keep that behavior explicit in copy and
  tests, or add a compact action target. Do not pretend a whole-row double click
  is the same interaction as a button.
- A Huh confirm will remain keyboard-only unless UAM adds modal button bounds.
  If click confirmation is required, handle it in the parent model and prevent
  the same event from falling through to dashboard rows.
- Mouse reporting captures terminal selection. Preserve `UAM_NO_MOUSE`; the
  tradeoff is inherent, not a renderer bug.

## Responsive sizing

Bubble Tea sends `WindowSizeMsg` once initially and on terminal resize
([v1 source](https://github.com/charmbracelet/bubbletea/blob/v1.3.10/screen.go#L3-L10)).
Treat that message as the only geometry input for the frame. Derive header,
footer, overlay, body, and hit rectangles together, subtracting every Lip Gloss
border/padding/margin before handing dimensions to a child.

Bubbles `list`, `table`, `viewport`, `help`, and `textinput` expose sizing APIs,
but they do not choose UAM's breakpoints. `table` columns must be recalculated by
the application. Huh auto-sizes from `WindowSizeMsg` only while its form width
or height remains zero; an explicitly sized overlay must call `WithWidth` and
`WithHeight` again after each resize
([Huh form update](https://github.com/charmbracelet/huh/blob/v2.0.3/form.go#L528-L562)).
Huh v2.0.3 specifically fixed select viewport width recomputation on
`WithWidth`, so older v2 patches should not be used for a responsive overlay
([release](https://github.com/charmbracelet/huh/releases/tag/v2.0.3)).

Recommendation: keep UAM's existing compact/wide board and fixed footer budget.
Use a centered, bounded modal width such as `min(preferred, width - gutters)`;
below its useful minimum, replace the body with the form instead of overpainting
an unreadable box. Final ANSI-aware truncation remains a safety rail, not the
layout algorithm.

## Rendering and input performance

### Verified behavior

Bubble Tea v1's renderer defaults to 60 FPS, caps at 120 FPS, drops an identical
frame, and skips unchanged lines
([renderer source](https://github.com/charmbracelet/bubbletea/blob/v1.3.10/standard_renderer.go#L15-L38),
[diff loop](https://github.com/charmbracelet/bubbletea/blob/v1.3.10/standard_renderer.go#L155-L220)).
Its own `ClearScreen` documentation says it should not be needed for regular
redraws
([source](https://github.com/charmbracelet/bubbletea/blob/v1.3.10/screen.go#L11-L20)).
Bubble Tea v2 replaces that renderer with the cell-based Cursed Renderer and
automatically uses synchronized output updates on supporting terminals
([v2 release](https://github.com/charmbracelet/bubbletea/releases/tag/v2.0.0),
[renderer source](https://github.com/charmbracelet/bubbletea/blob/v2.0.9/cursed_renderer.go#L533-L610)).
Outside alternate-screen mode, changing frame height forces erase/resize work;
stable full-window dimensions avoid that jitter path
([source](https://github.com/charmbracelet/bubbletea/blob/v2.0.9/cursed_renderer.go#L301-L340)).
Bubbles v2 removes viewport `HighPerformanceRendering` because the new renderer
makes the separate mode unnecessary
([upgrade guide](https://github.com/charmbracelet/bubbles/blob/v2.2.1/UPGRADE_GUIDE_V2.md#viewport)).

### Recommendations

- Keep cell motion, not all motion. UAM does not use hover, so idle motion is
  pure input load.
- Keep `View` deterministic for one model state. Compute one layout object per
  frame and use it for both rendering and hit-testing. UAM already follows this
  for gate geometry.
- Preserve stable row counts and fixed footer position. Clip by display cells
  before output so terminal wrapping cannot create phantom rows and cursor
  jumps.
- Do not call `ClearScreen` on ordinary refreshes or selection changes. Retain
  it only around terminal ownership transitions where UAM currently returns
  from an attached child process.
- Coalesce refresh/load results and avoid animation ticks unless pixels actually
  change. The current loading guard and reorder debounce are the right pattern;
  reducing `WithFPS` is a measurement-driven fallback, not the first fix.
- Keep the selected session anchored by identity across refreshes. Sorting or
  filtering should not make the highlight jump merely because new data arrived.

## Embedding Huh without a second screen

`Form.Run` is the wrong API inside UAM: Huh's source shows that it creates and
runs its own `tea.NewProgram`
([v2 source](https://github.com/charmbracelet/huh/blob/v2.0.3/form.go#L663-L708)).
The supported composition pattern is to store `*huh.Form` in the parent model,
return its `Init` command once, forward messages while the modal owns focus,
retain the returned form, inspect `StateCompleted`/`StateAborted`, and include
`form.View()` in the parent's rendered frame
([official embedded example](https://github.com/charmbracelet/huh/blob/v2.0.3/examples/bubbletea/main.go#L67-L168)).
At v2.0.3 the form's exported `Model` is a compatibility interface whose
`View` returns `string`, so `*huh.Form` does not directly satisfy Bubble Tea
v2's `tea.Model`; the parent composition above is not optional ceremony
([Huh compatibility source](https://github.com/charmbracelet/huh/blob/v2.0.3/internal/compat/model.go#L6-L39),
[Bubble Tea model](https://github.com/charmbracelet/bubbletea/blob/v2.0.9/tea.go#L52-L65)).
`Confirm.Inline(true)` changes line layout only; it is not an embedding or
overlay mode
([source](https://github.com/charmbracelet/huh/blob/v2.0.3/field_confirm.go#L135-L139)).

The rejected v1 lane would use Huh v0.8.0. Its module pins
Bubble Tea v1.3.6 and Lip Gloss v1.1.0, so Go's version selection can retain
UAM's newer Bubble Tea v1.3.10
([v0.8.0 go.mod](https://github.com/charmbracelet/huh/blob/v0.8.0/go.mod)).
Render the form in UAM's existing modal body. A true box over the still-visible
board requires an application-owned line/cell compositor in Lip Gloss v1; the
simpler body replacement already satisfies “one program, one alternate screen.”

With the selected v2 migration, render the dashboard as the base Lip Gloss layer and the
bounded Huh form as a higher-z layer. Use the compositor's hit result in
`View.OnMouse` to shield the board and route modal clicks. This improves
composition and hit-testing, but Huh `Confirm` itself is still keyboard-driven;
UAM must map modal button IDs to accept/reject actions.

## Compatibility and migration implications

The latest Charm packages use `charm.land/.../v2` imports and must move
together. Bubble Tea's official checklist includes these breaking changes:

- `View() string` becomes `View() tea.View`.
- `tea.KeyMsg` press handling becomes `tea.KeyPressMsg`; rune/alt fields and the
  string for space change.
- `tea.MouseMsg` becomes an interface with distinct click/release/wheel/motion
  messages; button constant names change.
- `WithAltScreen` and `WithMouseCellMotion` disappear; those settings become
  fields on `tea.View`.
- `tea.WindowSize()` becomes `tea.RequestWindowSize`.

The authoritative mappings and complete example are in the
[Bubble Tea v2 upgrade guide](https://github.com/charmbracelet/bubbletea/blob/v2.0.9/UPGRADE_GUIDE_V2.md).
Those changes touch UAM's root model, CLI launch, mouse tests, key tests, screen
restore paths, and every direct `View` assertion. This is not mechanical only;
the new declarative view owns terminal modes that UAM currently sets in
`RunTUI`.

Lip Gloss v2 removes root `AdaptiveColor` and `TerminalColor`; colors become
`image/color.Color`, and explicit `LightDark` selection or the compatibility
package replaces adaptive colors
([upgrade guide](https://github.com/charmbracelet/lipgloss/blob/v2.0.6/UPGRADE_GUIDE_V2.md#color-system)).
UAM's theme table uses `compat.AdaptiveColor` and synchronizes it from Bubble
Tea's live `BackgroundColorMsg`
([theme.go](../../internal/app/theme.go)), so the migration reaches its palette,
background event handling, and contrast tests. Huh and Bubbles have their own
coordinated [Huh v2 guide](https://github.com/charmbracelet/huh/blob/v2.0.3/UPGRADE_GUIDE_V2.md)
and [Bubbles v2 guide](https://github.com/charmbracelet/bubbles/blob/v2.2.1/UPGRADE_GUIDE_V2.md).

Two implementation lanes were evaluated:

1. **Rejected smallest change:** stay on the v1 stack, keep the custom board, fix wheel
   scope, and add Huh v0.8.0 only if its form behavior earns the dependency.
2. **Selected platform migration:** migrate Bubble Tea, Lip Gloss, and any new
   Bubbles/Huh usage to v2 in a dedicated change with existing rendering and
   input behavior held constant; redesign only after that baseline is green.

The v2 migration was gated as a separate integration stage before the dashboard
replacement. That preserves a compile-and-test boundary even though both stages
ship in the same UX change.
