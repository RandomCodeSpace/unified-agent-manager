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

Every row includes its provider, evidence-based lifecycle label, and age since
the Managed Session was created. Age is deliberately not an activity indicator:
live discovery timestamps change on every refresh and cannot prove that an agent
is busy or idle. The age is additionally tinted along a four-stop ramp (fresh
under an hour, recent under a day, old under a week, then stale), which colours
the same creation-time fact rather than introducing an activity claim —
`adapter.Session.LastChange` is re-stamped by every discovery scan and so cannot
support one.

Attaching to Running reconnects to the existing host. Acting on Stopped resumes
the provider when supported. If that resume can only select the provider's most
recent conversation and several retained sessions share the Workspace, the TUI
asks for confirmation before launching anything.

## Layout classes

The layout is derived from the current terminal dimensions on every resize.

| Layout | Geometry | Operations view |
|---|---|---|
| **Wide** | At least 96 columns and 28 rows | Borderless full-width list at the density the row budget affords; every session shows the same fields. |
| **Standard** | At least 58 columns and 24 rows, but below Wide | Borderless full-width list; full session IDs are retained. |
| **Compact** | Fewer than 58 columns or fewer than 24 rows | Same density ladder; the lifecycle word yields to its glyph and IDs shorten to an eight-character prefix. |

### Density ladder

Row height is not fixed per layout. `densityFor(visibleCount, budget, overhead)`
divides the body's remaining lines among the visible sessions and returns one,
two, or three lines per session, applied uniformly:

| Rung | Lines per session | Adds |
|---|---|---|
| 1 | 1 | Chip, lifecycle glyph, pin, name with the task inline, PR mark, provider, lifecycle word (non-compact), dwell |
| 2 | 2 | The task summary on its own line, including any failure detail |
| 3 | 3 | Workspace path, profile `selected→effective` (omitted when it is the empty `default→none`), resume fidelity, command alias when it differs from the provider, session ID |

Section headings and workspace-contention warnings are counted as overhead
*before* the rung is chosen, so the chosen density always fits whenever one line
per session would fit. Blocks are atomic: windowing snaps the viewport back to a
block boundary rather than rendering a session's task line with no name above it.

### Addressing without modifier keys

The first ten visible sessions wear a chip (`1`-`9`, then `0`). Pressing a chip
moves the cursor to it; pressing the same chip again attaches. A mouse click or
a phone tap resolves through the same verb — any line of a block, including its
task and provenance lines, resolves to that block's session — and the wheel
walks the selection. `/` narrows the roster and re-chips it, which is how rosters
larger than ten are addressed.

Digits bind only on an empty composer, and only when the chip is actually on
screen: an unassigned digit is ordinary text, so a small roster never swallows
input. Letters were deliberately not used — taking nine of them would break
dispatching any prompt that starts with one.

Mouse reporting is on by default because it is what makes the dashboard usable
by touch. It takes over the terminal's native drag-to-select copying, so
`UAM_NO_MOUSE=1` disables it.

The prompt is reserved at the bottom before the remaining rows are allocated.
The New Session wizard is a primary surface in all layouts, so every step remains
usable when a mobile keyboard reduces the available height. Content is truncated
by terminal-cell width without splitting Unicode text.

## Keyboard map

| Key | Action |
|---|---|
| `↑` / `↓` | Move the selected row. |
| `Enter` / `→` | Attach to Running, or resume and attach to Stopped. |
| `Space` | Resume Stopped in the background; this may first require latest-conversation confirmation. Otherwise enter a space in the composer. |
| Type + `Enter` | Dispatch with the default provider. |
| `@provider:alias #name prompt` | Choose provider, optional command alias, optional name, and prompt inline. |
| `Tab` | Cycle the default provider. In the wizard, cycle provider or complete a path according to the current step. |
| `e` | Open the four-step New Session wizard. |
| `Ctrl+G` | Open `$VISUAL` or `$EDITOR` for the wizard prompt. |
| `Ctrl+T` | Pin or unpin the selected row. |
| `Ctrl+R` | Rename the selected Managed Session. |
| `Ctrl+X` | Confirm stop **and record removal**; press `r` in the confirmation to restart in place. Use CLI `uam stop <id>` to retain a Stopped row. |
| `Ctrl+S` | Toggle Workspace grouping. |
| `Shift+↑` / `Shift+↓` | Reorder within the same lifecycle, pin, and visible Workspace group. |
| `/` with an empty command | Enter live filtering. Type to narrow, use arrows to move, and press `Esc` to clear. |
| `?` with an empty command | Open key help. With text typed, `?` is ordinary input. |
| `Esc` | Close the current overlay or input; from the base dashboard, quit. |
| `Ctrl+C` | Quit from anywhere, including help, the wizard, rename, and confirmations. |

Inside an attached session, `Ctrl+B d` detaches. A bare left arrow also detaches
when the provider input is empty and the quick-detach option is enabled. See the
README for the complete attach-key contract.

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

Press `/` while the command composer is empty to filter the existing dashboard.
Matching is case-insensitive across display name, managed-session ID, provider,
command alias, task, Workspace, and lifecycle label. Space-separated terms must
all match the same session. The dashboard shows matched/total counts and removes
empty Workspace sections without changing the stored order.

Filtering is a temporary presentation state. It is not stored, and pin, rename,
stop, attach, resume, grouping, and reorder actions still use the session's
provider-and-ID identity. `Esc` clears the query and restores the prior selection
when it still exists. A slash typed after command text has begun remains literal
prompt content. An empty Operations dashboard still opens the filter and shows
a zero-result state.

## Workspace grouping and parallel sessions

`Ctrl+S` groups rows by normalized absolute working directory without resolving
symlinks. The grouping is a presentation projection: turning it off restores the
canonical lifecycle, pin, and manual ordering.

When two or more Running sessions share one Workspace, the heading shows a
warning. This is not an error and does not serialize the agents. It means the
processes can read and modify the same files concurrently. Use separate Git
worktrees or checkouts when tasks require filesystem isolation; UAM never creates
them automatically.

Reordering cannot cross a Running/Stopped boundary, a pin boundary, or (while
grouped) a Workspace boundary. Rejected moves leave selection and persisted
order unchanged.

## Mobile operation

With an on-screen keyboard, prefer Compact mode intentionally:

Mobile operation requires a terminal extra-keys row or hardware keyboard that
can send Escape, Tab, arrows, and Control chords. UAM does not currently provide
touch-only substitutes for those terminal keys.

1. Keep the terminal narrower than 58 columns or let the keyboard reduce it
   below 24 rows.
2. Use the one-line rows and expanded two-line selection to scan sessions; use
   the numbered chips or `Enter` to attach to the selected session.
3. Use `e` for the bounded wizard instead of composing a long inline dispatch.
4. Use `Ctrl+G` with a terminal/editor combination that supports external editor
   handoff when a multi-line prompt is easier outside the small viewport.

The bottom prompt and current primary action remain visible as the height
changes. UAM does not assume a fixed phone aspect ratio.

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
Provider policy fixes native keys and outer-screen
behavior; Codex is primary-screen while the other current providers use a UAM
outer screen. `uam profile assign <session-id> <name|none>` selects the
per-session profile; `uam profile override <session-id> [profile flags]` sets
the durable final profile layer; `uam profile effective <session-id> --json`
shows it. Attachment-local mouse, prefix, and quick-detach choices last only for
that client, and capabilities can constrain whether local filtering or an owned
screen is available. A provider-constrained profile must match its session
provider. `uam profile rm <name>` refuses a profile that is still default or
selected by a session. Launch-time fields are provider, mode, alias, and
scrollback; profiles and session selection/overrides persist; client identity,
role, dimensions, protocol, and capabilities do not.

| Provider | Resume policy | Outer screen |
|---|---|---|
| Claude Code | Exact when its seeded ID is retained; otherwise guarded latest continuation | UAM |
| GitHub Copilot CLI | Exact for UAM-created records | UAM |
| OpenCode | Exact root conversation only | UAM |
| Oh My Pi | Exact with its dedicated state; legacy records use guarded latest continuation | UAM |
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
- No surface paints a background or uses reverse video: the selected session is
  marked by an accent edge bar down the left of its block, which cannot bleed
  across the frame when a styled run is truncated.
- Names, paths, prompts, headings, and status text are sanitized before display
  so stored control sequences cannot alter the terminal.
- Width calculations account for emoji, combining characters, and CJK text.
- The prompt, current selection, lifecycle headings, and modal choices remain
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
