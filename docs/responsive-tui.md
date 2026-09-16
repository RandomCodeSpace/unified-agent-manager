# Responsive TUI design and operations

The UAM dashboard adapts to desktop terminals, split panes, and mobile terminals
where an on-screen keyboard may leave fewer than 24 rows. This guide explains
what stays visible and how to operate sessions safely in each layout.

## Lifecycle at a glance

Rows are partitioned by process liveness:

- **Running** means the provider process is alive.
- **Stopped** means the provider process is gone but the Managed Session record
  remains. Clean exits and explicit stops use a neutral marker. A known nonzero
  exit or signal uses a failure marker and displays `exit N` or `signal`.

UAM does not infer "working," "waiting," or "completed" by scraping provider
text. The selected row's prompt is kept as its task summary; failure detail is
added without replacing it.

Every row begins with provider as a padded, filled Lip Gloss label after the
selection rail; Wide rows additionally include creation age and task. Age is
deliberately not an activity indicator:
live discovery timestamps change on every refresh and cannot prove that an agent
is busy or idle. The age is additionally tinted along a four-stop ramp (fresh
under an hour, recent under a day, old under a week, then stale), which colours
the same creation-time fact rather than introducing an activity claim —
`adapter.Session.LastChange` is re-stamped by every discovery scan and so cannot
support one.

The selected-session context shows `Updated HH:MM TZ`, derived from the
persisted `LastSeenAt` liveness observation and converted to the dashboard's
local timezone. It means UAM last observed the Managed Session then; it does not
claim that the provider produced output or changed conversational state then.

Attaching to Running reconnects to the existing host. Acting on Stopped resumes
the provider when supported. If that resume can only select the provider's most
recent conversation and several retained sessions share the Workspace, the TUI
asks for confirmation before launching anything.

## Layout classes

The dashboard derives its horizontal grammar from width only. Height changes the
number of visible Bubbles viewport rows; it never changes row meaning or moves an
action into a different field.

| Layout | Width | Roster fields | Fixed context |
|---|---|---|---|
| **Wide** | At least 96 columns | lifecycle, identity, provider, task, age, primary action | provider, update time and zone, full ID, path, Stop or Remove |
| **Compact** | 60 through 95 columns | lifecycle, identity, provider, primary action | provider, update time and zone, full ID, shortened path, Stop or Remove |
| **Narrow** | 40 through 59 columns | lifecycle, identity, provider, primary action | provider, update time and zone, Stop or Remove |

Every session occupies exactly one line. Optional fields disappear as complete
units; identity, literal lifecycle, and the primary action never disappear. At
40 by 12, the screen contains a header, divider, seven roster rows, a context
divider, selected-session context, and one Bubbles help line. Smaller terminals
show a non-destructive minimum-size notice rather than clipped controls.

The header uses a fixed-width Lip Gloss `UAM` badge instead of an emoji wordmark,
followed by the build version. A local 24-hour `HH:MM TZ` clock and the literal
`Agents` label remain visible at every supported width; verbose refresh and
session-count wording yield first.

### Mouse and frame safety

A row is a selection target. `Attach` and `Resume` are separate Lip Gloss action
layers, so clicking a row never activates it. The selected session's `Stop` or
`Remove` action lives in the fixed context band. Wheel events move selection and
the viewport follows it.

Bubble Tea v2 routes pointer input through `View.OnMouse` on the view that was
actually displayed. The callback resolves the top Lip Gloss compositor layer
and emits a semantic intent containing the captured frame signature, dimensions,
provider, session ID, and action signature. Update discards the intent if a
refresh, resize, reorder, filter, lifecycle change, or removal made it stale.
Raw coordinates never call a service operation.

Mouse reporting is on by default. It takes over native drag-to-select copying,
so `UAM_NO_MOUSE=1` disables it.

## Keyboard map

| Key | Action |
|---|---|
| `↑` / `↓` | Move the selected row. |
| `Enter` / `→` | Attach to Running, or resume and attach to Stopped. |
| Mouse row click | Select only. |
| Mouse `Attach` / `Resume` click | Invoke the explicit primary action. |
| Mouse `Stop` / `Remove` click | Open the Huh confirmation form. |
| Wheel | Move selection through the viewport. |
| `Space` | Resume Stopped in the background; this may first require latest-conversation confirmation. |
| `Tab` | Cycle the default provider. In the wizard, cycle provider or complete a path according to the current step. |
| `e` | Open the four-step New Session wizard. |
| `Ctrl+G` | Open `$VISUAL` or `$EDITOR` for the wizard prompt. |
| `Ctrl+T` | Pin or unpin the selected row. |
| `Ctrl+R` | Rename the selected Managed Session. |
| `Ctrl+X` | Open a Huh Stop or Remove form with Cancel focused; `r` selects the existing restart intent. |
| `Ctrl+S` | Toggle Workspace grouping. |
| `Shift+↑` / `Shift+↓` | Reorder within the same lifecycle, pin, and visible Workspace group. |
| `/` | Enter live filtering. Type to narrow, use arrows to move, and press `Esc` to clear. |
| `r` | Retry the existing load path while a persistent refresh failure is visible. |
| `?` | Toggle the one-line Bubbles help footer between primary and secondary commands. |
| `Esc` | Cancel the current form or overlay; from the base dashboard, quit. |
| `Ctrl+C` | Quit from anywhere, including help, the wizard, rename, and confirmations. |

The base dashboard has no command composer. Use `uam new`, the existing `e`
wizard, or other CLI commands to create and configure sessions.

Inside an attached session, `Ctrl+B d` detaches. Ctrl+Left also detaches when
the provider input is empty and the quick-detach option is enabled; bare arrows
always reach the provider. On uam's own alternate screen the last terminal row
is a persistent status bar (role, session, profile, keys) and the provider gets
the rows above it. See the README for the complete attach-key contract.

## Multiple attached terminals

An attachment is a live client, not a second Managed Session. One client is the
**controller** and is the only one allowed to send provider input or resize the
PTY. Further interactive clients are **standbys**;
they see output and can request a handoff, but cannot interleave keystrokes.
**Observers** are output-only. If the controller disconnects, the next standby
is promoted. A controller can also transfer deliberately.

With the default prefix, use `Ctrl+B r` to request control — the controller is
shown a notice naming the requesting client, and decides whether to hand over —
`Ctrl+B o` from the controller to transfer, and `Ctrl+B i` to display your role. `Ctrl+B m`
changes mouse passthrough only for the current attachment. A configured profile
prefix replaces `Ctrl+B`; `prefix prefix` sends the configured literal prefix.
`Ctrl+B Ctrl+B` has that meaning only when the configured prefix is `C-b`.
Bracketed paste bypasses every prefix command, so pasted control bytes stay
provider input. The full protocol and mixed-version matrix is
[ADR 0003](adr/0003-terminal-client-session-ownership-and-protocol-v2.md).

## Filtering sessions

Press `/` from the base dashboard to filter the existing roster.
Matching is case-insensitive across display name, managed-session ID, provider,
command alias, task, Workspace, and lifecycle label. Space-separated terms must
all match the same session. The dashboard shows matched/total counts without
changing the stored order.

Filtering is a temporary presentation state. It is not stored, and pin, rename,
stop, attach, resume, grouping, and reorder actions still use the session's
provider-and-ID identity. `Esc` clears the query and restores the prior selection
when it still exists. An empty dashboard still opens the filter and shows a
zero-result state.

## Workspace grouping and parallel sessions

`Ctrl+S` groups rows by normalized absolute working directory without resolving
symlinks. The grouping is a presentation projection: turning it off restores the
canonical lifecycle, pin, and manual ordering.

Grouping changes roster order only; the minimalist dashboard does not insert
Workspace headings. Concurrent Running sessions can still read and modify the
same files. Use separate Git worktrees or checkouts when tasks require filesystem
isolation; UAM never creates them automatically.

Reordering cannot cross a Running/Stopped boundary, a pin boundary, or (while
grouped) a Workspace boundary. Rejected moves leave selection and persisted
order unchanged.

## Mobile operation

With an on-screen keyboard, prefer Narrow mode intentionally:

Mobile operation requires a terminal extra-keys row or hardware keyboard that
can send Escape, Tab, arrows, and Control chords. UAM does not currently provide
touch-only substitutes for those terminal keys.

1. Keep the terminal between 40 and 59 columns and at least 12 rows.
2. Tap a row once to select it, then tap its explicit `Attach` or `Resume`
   action. `Enter` activates the selected session without requiring a second tap.
3. Use `e` for the bounded wizard; the base dashboard has no command composer.
4. Use `Ctrl+G` with a terminal/editor combination that supports external editor
   handoff when a multi-line prompt is easier outside the small viewport.

The selected-session context, destructive action, and one-line help remain
visible as height changes. UAM does not assume a fixed phone aspect ratio.

## SSH, mouse, and paste

Mouse reporting defaults on for local and SSH attachments alike so wheel and
touch gestures reach mouse-aware providers such as OpenCode and OMP. Override it
with `UAM_ATTACH_MOUSE=on|off|auto`, where `auto` is the default and `on` is an
alias for it — the policy does not vary by transport. Set it to `off` when
terminal-owned selection or right-click paste is more important than provider
scrolling.

Bracketed-paste payload is forwarded literally, including control bytes, UTF-8,
and line endings. UAM cannot initiate paste from a local clipboard. Windows users
should run SSH in Windows Terminal and configure a Windows Terminal paste
binding. See [ADR 0002](adr/0002-terminal-ownership-over-ssh.md) for the terminal
ownership boundary and limitations.

The terminal's `TERM` or color name is metadata, not proof that it can perform a
specific feature. UAM keeps provider keys native and uses its supported provider
terminal policy rather than guessing from that name. An unsupported or sensitive
hint is redacted in diagnostics.

## Profiles, provider policy, and recovery

Use a named profile to make repeated launches predictable:

```sh
uam profile set focused --provider claude --mode safe --mouse off --prefix C-a --back-detach off --scrollback 8000
uam profile default focused
uam profile show focused --json
uam profile ls --json
uam doctor --json
```

Profile resolution is ordered: **hard safety invariants**, **global defaults**,
**built-in provider policy**, **selected named profile**, **per-session
overrides**, **client-local attachment overrides**, then **capability
constraints**. The invariant layer fixes provider `TERM` to `xterm-256color`
and rejects profile environment, terminal-capability, and resume-policy changes.
Provider policy fixes native keys and outer-screen behavior. Codex and Oh My Pi
use the primary screen; the other current providers use a UAM outer screen.
`uam profile assign <session-id> <name|none>` selects the
per-session profile; `uam profile override <session-id> [profile flags]` sets
the durable final profile layer; `uam profile effective <session-id> --json`
shows it. Attachment-local mouse, prefix, and quick-detach choices last only for
that client, and capabilities can constrain whether local filtering or an owned
screen is available. A provider-constrained profile must match its session
provider. `uam profile rm <name>` refuses a profile that is still default or
selected by a session. Launch-time fields are provider, mode, alias, and
scrollback; profiles and session selection/overrides persist; client identity,
role, dimensions, protocol, and capabilities do not.

### Provider resume and terminal policy

| Provider | Resume policy | Outer screen |
|---|---|---|
| Claude Code | Exact when its seeded ID is retained; otherwise guarded latest continuation | UAM |
| GitHub Copilot CLI | Exact for UAM-created records | UAM |
| OpenCode | Exact with a retained valid root `ses_…` ID; otherwise create a new Managed Session | UAM |
| Oh My Pi | Exact with its dedicated state; legacy records use guarded latest continuation | Primary |
| OpenAI Codex | Guarded latest continuation | Primary |
| Hermes Agent | Unsupported; create a new Managed Session | UAM |

`uam doctor <session-id> --json` shows runtime role counts, protocols,
profile resolution, provider policy, and safe fallback reasons. It does not
print terminal content or secret-like values.

### Cross-terminal smoke collection

Collect optional manual terminal evidence with the real collector, once per
available target:

```sh
scripts/terminal-smoke-real --terminal <kitty|wezterm|alacritty|ghostty|iterm2-ssh|windows-terminal-ssh> --output <path> --non-interactive
```

It reports whether the named local terminal is available for a manual visual run
or whether an SSH target is configured; unavailable/headless results are useful
evidence, not a test failure. This collector is optional/manual evidence and is
not a mandatory CI gate. In contrast,
`script/qa/docs-contract-smoke.sh --uam ./bin/uam --evidence-dir <absolute-dir>`
is the required documentation contract gate: it checks CLI help, isolated
profile/doctor examples, schema migration, and links, but does not claim to
test graphical terminal behavior.

Schema v4 creates an adjacent backup before migrating an older configuration.
If migration fails, the original stays in place. Stop UAM before restoring a
chosen backup, then use a compatible binary. Unknown equal-schema fields round
trip; a newer schema opens read-only to an older binary. Runtime client state is
never persisted.

## Accessibility and no-color operation

- `NO_COLOR` disables styling even when the terminal advertises color.
- Every semantic mark is defined in a single tone table as a colour paired with
  a distinct one-cell glyph and text attributes. A unit test asserts that each
  meaning-bearing tone owns a glyph, that those glyphs are pairwise distinct and
  exactly one cell wide, and that purely decorative tones (the dwell ramp) own no
  glyph at all — so no datum can be encoded by colour alone, and a two-cell glyph
  cannot silently shift the columns to its right.
- The terminal background remains untouched. Focused action cells, Retry, and
  Huh's focused button use a tightly bounded accent background; the selected
  session also has an accent edge marker, so focus never depends on colour or
  reverse video alone.
- Bubble Tea requests the terminal background colour at startup and updates the
  shared Lip Gloss and Huh palette when the terminal reports a change. Every
  text-bearing light/white and dark/black palette pair, including focused button
  text against its accent fill, is regression-tested at a minimum 4.5:1 contrast.
- Names, paths, prompts, headings, and status text are sanitized before display
  so stored control sequences cannot alter the terminal.
- Width calculations account for emoji, combining characters, and CJK text.
- The wizard prompt, current selection, lifecycle headings, and modal choices remain
  textual and keyboard-operable.

## Operational checks

If the screen looks corrupted after an older UAM or provider process exits,
start a fresh UAM dashboard so it can establish a new terminal-owned screen.
Current attach cleanup is targeted and should return to a visible cursor on a
clean line without clearing shell scrollback.

If right-click paste behaves differently through PowerShell SSH:

1. Confirm the SSH command is running inside Windows Terminal rather than the
   legacy console host.
2. Confirm Windows Terminal has a keyboard paste binding such as `Ctrl+V`,
   `Ctrl+Shift+V`, or `Shift+Insert`.
3. Keep the remote setting at `UAM_ATTACH_MOUSE=auto` for provider scrolling.
4. If terminal-owned selection or right-click paste is preferred, set the
   remote setting to `UAM_ATTACH_MOUSE=off` and reattach.

If resuming a stopped row reports ambiguity, read the provider and Workspace in
the message. Confirm only when selecting the provider's latest conversation is
acceptable. Otherwise start a new Managed Session or restore an exact provider
identity.

After a reboot, a retained record permits provider-aware relaunch/resume; the
old PTY and terminal modes did not survive. Normal detach and handled signals
restore the terminal contract. SIGKILL cannot do cleanup. If it leaves a local
terminal unusable, use `reset` or start a fresh terminal before attaching again.
