# Minimalist Agent Dashboard Design

## Outcome

Unified Agent Manager presents one fast, literal dashboard for finding and entering agent sessions. It does not use the airport or flight metaphor. It does not add another screen. Confirmation is the only layer allowed above the dashboard.

The interface is mouse-first without becoming mouse-only. A pointer and a keyboard resolve to the same semantic commands, and every visible action remains named in text.

## Non-negotiable boundary

This is a UX-only change. Session discovery, ordering, attachment, resume behavior, stop behavior, refresh cadence, persistence, provider adapters, and command arguments remain unchanged. UI code calls the existing operations through thin command adapters. No service or adapter contract changes are part of this work.

The dashboard may simplify how those operations are presented. It must not change what any confirmed operation does.

## User stories

### Scan agents

As a developer, I can see every known session, its literal lifecycle state, provider, and primary action without decoding a metaphor.

Acceptance criteria:

- The header says `Agents` and reports filter-matching and total session counts. With no filter, the counts are equal.
- Every visible row contains identity, lifecycle text, and one primary action.
- Running, stopped, and failed states differ in text or glyph as well as color; refresh progress is labeled beside the Bubbles spinner.
- Refresh accepts the existing ordered session projection and preserves selection by stable identity when the selected session still exists.

### Navigate sessions

As a developer, I can select and enter a session with either a mouse or keyboard.

Acceptance criteria:

- One click on a row selects it; it does not activate it.
- One click on the explicit `Attach` or `Resume` button activates that action.
- Arrow keys and wheel motion move selection through the same ordered roster; the viewport follows selection.
- Enter invokes the selected row's primary action.
- A resize or refresh cannot reinterpret an event produced by the previously displayed view as a new action.

### Confirm destructive actions

As a developer, I can verify the exact session before a destructive action runs.

Acceptance criteria:

- Stop and ambiguous resume confirmations use a Huh form.
- The confirmation names the session and the consequence.
- `Cancel` is initially focused; `Stop` or `Continue` is explicit.
- Buttons support pointer activation and the same keyboard focus and submit path.
- The overlay owns all input while open and prevents click-through.

### Work in a constrained terminal

As a developer on SSH, a phone, or a split pane, I can still identify and enter sessions at 40 columns by 12 rows.

Acceptance criteria:

- Identity, lifecycle state, selection, and primary action remain visible.
- Secondary fields disappear as complete units, without empty separators.
- Long values truncate by display-cell width.
- The selected session remains selected across breakpoint changes.
- Unicode symbols and emoji have measured ASCII fallbacks.

## Design derivation

Two independent ADHD divergence passes explored interaction architecture and visual composition. Each used five isolated frames, followed by weighted novelty, value, and feasibility scoring and a focus pass on the three strongest results.

The selected combination is:

1. **Compact sentence grammar**: rows read as identity, literal state, and primary verb rather than a miniature database table. Weighted score: 9.00.
2. **Refresh quiescence**: keep geometry still, commit complete frames atomically, and reject stale intent. Weighted score: 8.90.
3. **Fixed-stride row bus**: fixed-height keyed rows share one geometry contract for rendering, focus, and mouse input. Weighted score: 8.50.

The repeated principles across the divergent frames were stable session keys, one layout manifest, literal state, progressive field removal, explicit text actions, a single accent, bounded feedback, and Huh as the top input layer.

The following alternatives are rejected:

- Expanding the selected row, because it moves pointer targets.
- Lifecycle columns, because automatic regrouping moves sessions during refresh.
- A command-palette grid, because it adds navigation depth instead of direct actions.
- Hover-only affordances and double-click activation, because neither is reliable across terminals.
- Per-provider color identities, because color would become overloaded and inconsistent in low-color terminals.
- Continuous animation, because it adds repaint work without communicating more state.

## Dashboard anatomy

The dashboard has four stable bands:

1. Header: a fixed-width Lip Gloss `UAM` badge and version on the left; local `HH:MM TZ` time, literal `Agents`, visible/total count, and refresh state use the remaining space on the right.
2. Roster: a viewport of fixed-height session rows.
3. Context: selected session details, the secondary `Stop` or `Remove` action, or a persistent error. This band never changes height within a breakpoint.
4. Help: one line of Bubbles help output for the commands that fit the current width. It does not expand, overlay, or change roster geometry.

The brand is a three-letter, single-cell-safe `UAM` badge rather than an emoji wordmark, so terminal font fallback cannot split or shift it. Rows use compact terminal glyphs rather than decorative emoji. Warnings may use `⚠ Warning`, always with literal text and an ASCII `! Warning` fallback. No meaning depends on emoji.

### Row grammar

Rows remain a fixed height within a breakpoint. Selection uses a narrow
sky-blue rail and stronger identity text. Provider is the first field after the
rail and uses a padded Lip Gloss label with a contrasting fill. The action is a
text button with a contrasting fill when focused. Selection and activation are
separate states.

Wide row:

```text
▌ [codex] ● Running  agent-name  workspace/task…  4m  [ Attach ]
```

Compact row:

```text
▌ [codex] ● Running · agent-name                  [Attach]
```

40-column row:

```text
▌ [codex] ● Running agent-name  [Attach]
```

The state is always literal. The glyph and color reinforce it. The action never disappears; secondary metadata truncates or drops first.

## Responsive layout

Horizontal breakpoints are outcomes of measured width, not device names. Height only changes the viewport row count:

| Layout | Width | Roster fields | Fixed context |
| --- | --- | --- | --- |
| Wide | at least 96 columns | state, identity, provider, task/workspace, age, action | provider, observed update time and zone, session ID, path, `Stop` or `Remove` |
| Compact | 60 through 95 columns | state, identity, provider, action | provider, observed update time and zone, session ID, shortened path, `Stop` or `Remove` |
| Narrow | 40 through 59 columns | state, identity, provider, action | provider, observed update time and zone, `Stop` or `Remove` |

The header never drops UAM, version, local clock with timezone, or the literal `Agents` label; refresh detail and session-count wording truncate first. The wide roster drops age, then task as its flexible space is exhausted before entering the compact tier. The context band drops path and session ID before provider or observed update time. Provider, identity, literal state, and primary action are invariant. Heights reduce the number of viewport rows rather than changing row grammar.

Below 40 by 12, the dashboard remains read-only where possible and states the minimum size. It must not expose a clipped or ambiguous destructive target.

## Immutable presented frame

Rendering and interaction use one committed frame:

```text
PresentedFrame
  revision
  width, height
  rendered view
  selected session key
  ordered session keys
  semantic nodes
```

Each actionable semantic node contains a persistent ID, stable session key where applicable, action signature, and semantic command ID. Its display-cell bounds and z-order come from the same Lip Gloss layer stored in the presented compositor; they are not duplicated in a second geometry structure.

Required node forms include:

- `session/<key>/row`
- `session/<key>/action/attach`
- `session/<key>/action/resume`
- `session/<key>/action/stop`
- `session/<key>/action/remove`
- `footer/retry`

Session node IDs encode the provider-and-session identity, not viewport position, so reorder and filtering do not rename an action. Huh renders its own buttons without layer IDs; the permitted pointer bridge therefore derives button spans from the exact Huh render and sends the same accept or cancel input back through the form.

A pure layout function builds rendered content and nodes from the same model snapshot. Each View call captures that complete presented frame in its `View.OnMouse` closure, so geometry and pointer resolution cannot come from different renders.

The periodic refresh scheduler permits at most one in-flight load. Selection is anchored by stable session key. Bubble Tea v2 invokes `View.OnMouse` on the view that was actually displayed; that closure hit-tests its captured Lip Gloss compositor and emits an intent containing the captured frame revision, terminal dimensions, session key, and semantic command. Update executes it only if all remain current and the action is still available. Otherwise it discards the intent without calling the backend and displays `View changed; select again.` Raw coordinates are never resolved against a newer frame.

The lifecycle-to-action projection is fixed:

| Existing session condition | Literal state | Primary action | Secondary action |
| --- | --- | --- | --- |
| Process alive | Running | Attach | Stop |
| Process exited without failure detail | Stopped | Resume | Remove |
| Process exited with failure detail | Failed | Resume | Remove |

The projection reads existing session fields and calls existing commands. It does not invent a new lifecycle or resume decision.

## Input model

| Intent | Mouse | Keyboard | Semantic command |
| --- | --- | --- | --- |
| Select session | click row directly, or wheel up or down | Up or Down | select a stable key and make it visible |
| Enter session | click `Attach` or `Resume` | Enter | invoke primary action |
| Manage session | click `Stop` when running or `Remove` when stopped | Ctrl+X | open the context-appropriate confirmation |
| Confirm | click Huh button | Tab or arrows, then Enter | submit Huh form |
| Cancel | click Huh button or backdrop | Escape | cancel Huh form |
| Retry refresh | click `Retry` | `r` while refresh error is visible | invoke existing refresh path |

No double-click, hover-only command, hidden gesture, or coordinate-only backend call is permitted.

## Confirmation layer

Confirmations use Huh `Form` and `Confirm`. Lip Gloss composes the form above the dashboard. The dashboard stays visible so scope remains obvious, but its hit map is frozen and disabled.

The default choice is cancel. Destructive copy is literal:

```text
⚠ Stop "agent-name"?
This stops the process and removes the managed session.

[ Stop session ]  [ Cancel ]
```

Huh renders the affirmative action before the negative action. The negative `Cancel` choice is focused initially despite appearing second.

Stopped and failed sessions use the same Huh component with `Remove "agent-name"?` and `[ Remove session ]  [ Cancel ]`. The existing `r` shortcut changes the pending intent to the existing restart command and submits through the same Huh path; it does not add a third handcrafted button.

Huh does not supply pointer activation. The only custom adapter permitted maps manifest-owned button and backdrop rectangles to the same Huh focus, submit, and cancel messages used by keyboard input. It does not render a custom button or form.

The underlying roster may receive a harmless refresh while a confirmation is open, but it cannot accept input. The form snapshots the stable session key and action signature. If the target disappears or its lifecycle changes enough to change the proposed action, submission is refused and the form closes with a literal status message. Metadata-only changes do not invalidate it. Ambiguous resume means the existing resume path returned its existing ambiguity error and requested explicit permission to continue with the provider's latest retained conversation.

If a confirmation is resized below its safe minimum, it remains cancelable through Escape and hides the destructive submit target until enough space returns.

## Charm component boundary

Use this pinned v2 Charm stack as one coordinated upgrade:

- Bubble Tea `v2.0.9` owns the model loop, view flags, keyboard events, and pointer events.
- Bubbles `v2.2.1` viewport owns final roster offset, clipping, and fill; key and help own command discoverability; spinner owns initial-load and refresh progress. UAM lazily renders only the viewport's visible logical lines because Bubbles has no lazy item delegate, then routes wheel input through stable selection because the viewport has no session semantics.
- Huh `v2.0.3` form and confirm own confirmation focus, validation, choices, and submission.
- Lip Gloss `v2.0.6` owns terminal-aware styles, joins, action-cell layers, compositor hit testing, and final overlay composition.

The module set requires Go `1.25.8` or newer; the repository toolchain is pinned to the project ceiling of Go `1.26.3`. Known standard-library findings above that ceiling are explicitly allowlisted by ID in the security gate, while any new finding still fails CI.

Application code owns only the domain projection, pure responsive layout manifest, semantic command adapter, and the narrow pointer-to-component bridge Charm does not provide. Dashboard actions are Lip Gloss text layers identified in the compositor, not a local button widget. It must not create replacement widgets for viewports, help, spinners, forms, or buttons.

The coordinated upgrade is required because the current Huh release and Bubble Tea pointer/view APIs share the v2 ecosystem. Mixing major versions would require compatibility glue larger than the UI itself, which would be an impressive way to violate the component constraint.

## Visual system

- Surface: terminal default background, never a painted full-screen slab.
- Primary text: terminal-aware high contrast.
- Secondary text: cool gray.
- Focus and primary action: one restrained blue accent, dark on light terminals
  and bright on dark terminals.
- Pending or ambiguous state: amber with literal text.
- Error or destructive action: red with literal text.
- Density: one-cell vertical rhythm and one-cell gutters; action labels receive horizontal padding.
- Motion: Bubbles spinner only while work is unresolved. No ambient animation or row transitions.

Color is always secondary to wording, glyph, position, or style. `NO_COLOR`,
monochrome, ASCII, and low-color terminals retain the same information
hierarchy. Bubble Tea's background-colour request selects and can refresh the
light or dark palette at runtime. Text-bearing palette pairs and focused action
fills maintain at least 4.5:1 contrast against their intended backgrounds.

## Feedback and failure behavior

- Preserve the last good roster during refresh.
- Render the Bubbles spinner only while initial load or refresh work is unresolved.
- Initial load may show the Bubbles spinner plus `Loading agents` because no prior frame exists.
- Refresh failure leaves the roster usable and renders `Refresh failed  [ Retry ]` in the fixed context band.
- Refresh errors remain visible until a successful refresh or explicit Retry.

The fixed context band resolves competing messages in this order: confirmation scope, refresh failure with Retry, status or stale-view notice, then selected-session detail. A higher-priority message replaces a lower-priority one without changing band height.

## Performance contract

- On the same development host, focused input benchmarks target a mean below 8 milliseconds per event with 1,000 projected sessions.
- On the same development host, visible-frame construction benchmarks target a mean below 16 milliseconds with 1,000 projected sessions.
- Hit testing is bounded to visible semantic nodes.
- Static styled fragments and measured display widths may be cached by frame.
- Periodic refresh bursts never overlap loads; direct input and modal events do not wait behind refresh work.
- Backend polling cadence is unchanged.

The recorded 1,000-session benchmark on Linux amd64, Go 1.26.2, an AMD EPYC 9354P, and a 120 by 40 frame produced 5.97 to 6.08 ms per visible-frame construction and 0.122 to 0.130 ms per pointer event across three runs. These are UI measurements, not claims about backend throughput; the benchmark remains available for later host comparisons.

## Verification

Required automated evidence:

- Geometry and invariant-content coverage at 120 by 40, 96 by 20, 80 by 24, 60 by 15, and 40 by 12.
- Property checks that nodes are in bounds, actionable nodes do not overlap, and every pointer action has a keyboard equivalent.
- Display-width cases for CJK, combining characters, Unicode glyphs, emoji fallback, ASCII mode, and borders.
- A stale click after refresh, reorder, removal, or resize invokes no backend action.
- A current click invokes exactly one existing action with unchanged arguments.
- Huh blocks click-through and defaults destructive forms to cancel.
- Refresh failure preserves the last good roster and Retry uses the existing refresh path.
- Selection survives refresh and breakpoint changes by stable session key.
- Existing service, adapter, store, CLI, and protocol behavior remains unchanged; tests use the coordinated Charm v2 APIs where their UI expectations require migration.

Manual evidence uses a real terminal with mouse reporting enabled. It covers row selection, explicit action buttons, wheel navigation, Huh buttons, resize during pointer use, SSH, `NO_COLOR`, ASCII fallback, and 40 by 12. Automated PTY input is useful, but a synthetic pointer is not a human hand and should not be promoted beyond its station.

## Delivery sequence

1. Upgrade the Charm stack together while preserving current behavior.
2. Introduce the pure layout manifest and immutable presented-frame tests.
3. Replace the metaphorical board with the literal responsive roster.
4. Route mouse and keyboard through shared semantic commands and reject stale intents.
5. Replace handcrafted confirmations with Huh forms and pointer button adaptation.
6. Complete responsive, accessibility, performance, and backend regression gates.

Each step is independently reviewable. A later step may not compensate for a backend regression introduced by an earlier one.
