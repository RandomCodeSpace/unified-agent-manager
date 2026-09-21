# Responsive TUI design and operations

The UAM dashboard adapts to desktop terminals, split panes, and mobile terminals
where an on-screen keyboard may leave fewer than 24 rows. This guide explains
what stays visible and how to operate sessions safely in each layout.

## Lifecycle at a glance

Every stamp carries a literal lifecycle word next to its glyph:

- **Running** means the provider process is alive.
- **Stopped** means the provider process is gone but the Managed Session record
  remains. Clean exits and explicit stops use a neutral marker.
- **Failed N** means a known nonzero exit (`Failed 7`) or a signal
  (`Failed signal`); the failure marker replaces the neutral one.

UAM does not infer "working," "waiting," or "completed" by scraping provider
text, and the dashboard never reads session output. Everything on screen comes
from the stored record (name, provider, directory, creation time, pin, pull
request, profile) and the live process check. Nothing moves on a refresh tick
except the clock and a real lifecycle change.

Lifecycle is displayed, never sorted: a session keeps its position (and its
door digit) when it stops or fails. The canonical order is pinned first, then
the manual order, then newest first.

Creation time is shown as the clock when the session was created today, the day
when it was created this year, and the month and year otherwise. The ledger
adds `Updated HH:MM TZ` for stopped sessions, derived from the persisted
`LastSeenAt` observation and converted to the dashboard's local timezone. It
means UAM last observed the Managed Session then; it does not claim that the
provider produced output then.

Attaching to Running reconnects to the existing host. Acting on Stopped resumes
the provider when supported. If that resume can only select the provider's most
recent conversation and several retained sessions share the Workspace, the TUI
asks for confirmation before launching anything.

## Layout classes

The dashboard is a launcher and a switchboard. Top to bottom:

1. **Header**: the `UAM` badge and build version, the page range with fleet
   counts (`sessions 1-6 of 6 · 2 running · 1 failed`), and a local 24-hour
   `HH:MM TZ` clock. The clock and identity survive every width.
2. **Launch pad**: the provider and directory a new session would start with.
   `n` expands it in place into four fields (provider, directory, name, prompt)
   with the deck still visible beneath.
3. **Deck**: every session is a two-line stamp. Line one: selection rail, door
   digit, lifecycle glyph and word, name (with `★` when pinned) and provider.
   Line two: directory (elided from the left at a segment boundary) and creation
   time. Stamps fill columns left to right and rows top to bottom.
4. **Ledger**: the selected session in full, plus the action words that
   `Enter`, `Space`, `Ctrl+X`, `Ctrl+R` and `Ctrl+T` perform on it.
5. One Bubbles help line.

The deck's geometry depends on width only. Height changes how many stamp rows
fit on a page; it never changes what a stamp says.

| Width | Deck columns | Ledger |
|---|---|---|
| 40 through 79 columns | 1 | one line: the action words |
| 80 through 119 columns | 2 | three lines: identity, directory, actions |
| 120 columns and up | 3 (one more per further 40 columns) | two lines: identity with directory and ID, actions |

A stamp is at least 39 cells wide and grows to fill its column. No field is
ever dropped from a stamp; only the name and directory truncate. The ledger
appends optional facts (last observation, pull request, profile, directory, ID)
only while each fits whole and hands the rest to its next line, so nothing is
clipped mid-word.

When the fleet does not fit on one page, the header prints the visible range
and `PgUp`/`PgDn` page the deck. The first nine stamps on a page carry door
digits `1` through `9`.

At 40 by 12 the screen holds the header, the launch pad line, a rule, three
stamps, a rule, the action line and the help line. Smaller terminals show a
non-destructive minimum-size notice rather than clipped controls.

### Mouse and frame safety

A stamp body is a selection target. The door digit is a separate Lip Gloss
layer that attaches, and the ledger's `Attach`/`Resume` and `Stop`/`Remove`
words are layers of their own, so clicking a stamp never activates it. The
launch pad line is a layer that opens the pad. Wheel events move the selection
one deck row at a time.

Bubble Tea v2 routes pointer input through `View.OnMouse` on the view that was
actually displayed. The callback resolves the top Lip Gloss compositor layer
and emits a semantic intent containing the captured frame signature, dimensions,
provider, session ID, and action signature. Update discards the intent if a
refresh, resize, reorder, filter, lifecycle change, removal, or pad toggle made
it stale. Raw coordinates never call a service operation.

Mouse reporting is on by default. It takes over native drag-to-select copying,
so `UAM_NO_MOUSE=1` disables it.

## Keyboard map

| Key | Action |
|---|---|
| `↑` / `↓` | Move the selection one deck row. |
| `←` / `→` | Move the selection one stamp. |
| `PgUp` / `PgDn`, `Home` / `End` | Page the deck; jump to the first or last stamp. |
| `1` … `9` | Open the stamp behind that door digit on the visible page (attach, or resume and attach). |
| `0` | Reopen the session most recently attached this run. |
| `Enter` | Attach to Running, or resume and attach to Stopped. |
| Mouse stamp click | Select only. |
| Mouse door digit click, or ledger `Attach` / `Resume` click | Invoke the explicit primary action. |
| Mouse ledger `Stop` / `Remove` click | Open the Huh confirmation form. |
| Mouse launch pad click | Expand the launch pad. |
| Wheel | Move the selection one deck row. |
| `Space` | Resume Stopped in the background; this may first require latest-conversation confirmation. |
| `n` (or `e`) | Expand the launch pad in place. Inside it: `↑`/`↓` move between fields, `Tab` cycles the provider or completes the directory, `Shift+Tab` cycles the profile on the provider field, `Ctrl+G` opens `$VISUAL` or `$EDITOR` for the prompt, `Enter` starts the session, `Esc` cancels. The prompt still accepts the `@agent:alias #name prompt` shorthand. |
| `Tab` | Cycle the default provider shown on the launch pad. |
| `Ctrl+T` | Pin or unpin the selected session. |
| `Ctrl+R` | Rename the selected Managed Session inline in the ledger. |
| `Ctrl+X` | Open a Huh Stop or Remove form with Cancel focused; `r` selects the existing restart intent. |
| `Ctrl+S` | Toggle Workspace grouping. |
| `Shift+↑` / `Shift+↓` / `Shift+←` / `Shift+→` | Reorder along the same axes, within the same pin state and visible Workspace group. |
| `/` | Enter live filtering. Type to narrow, use arrows to move, and press `Esc` to clear. |
| `r` | Retry the existing load path while a persistent refresh failure is visible. |
| `?` | Toggle the one-line Bubbles help footer between primary and secondary commands. |
| `Esc` | Cancel the current form, pad, or filter; from the base dashboard, quit. |
| `Ctrl+C` | Quit from anywhere, including help, the launch pad, rename, and confirmations. |

The base dashboard has no free-text composer. Use the launch pad, `uam new`,
or other CLI commands to create and configure sessions.

Inside an attached session, `Ctrl+B d` detaches. Ctrl+Left also detaches when
the provider input is empty and the quick-detach option is enabled; bare arrows
always reach the provider. On uam's own alternate screen the last terminal row
is a persistent status bar (role, session, profile, keys) and the provider gets
the rows above it. See the README for the complete attach-key contract. After a
detach the cursor lands on the stamp you just left, so `Enter` re-enters it.

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

Press `/` from the base dashboard to filter the existing deck.
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

1. Keep the terminal between 40 and 79 columns and at least 12 rows.
2. Tap a stamp once to select it, then tap its door digit or the ledger's
   `Attach`/`Resume` word. Typing the digit opens the stamp without a tap.
3. Use `n` for the launch pad; the base dashboard has no free-text composer.
4. Use `Ctrl+G` with a terminal/editor combination that supports external editor
   handoff when a multi-line prompt is easier outside the small viewport.

The ledger's action line and the one-line help remain visible as height
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
- The launch pad, current selection, ledger, and modal choices remain textual
  and keyboard-operable.

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
