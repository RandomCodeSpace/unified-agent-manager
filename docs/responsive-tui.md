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
is busy or idle.

Attaching to Running reconnects to the existing host. Acting on Stopped resumes
the provider when supported. If that resume can only select the provider's most
recent conversation and several retained sessions share the Workspace, the TUI
asks for confirmation before launching anything.

## Layout classes

The layout is derived from the current terminal dimensions on every resize.

| Layout | Geometry | Operations view | Peek view |
|---|---|---|---|
| **Wide** | At least 96 columns and 28 rows | Full-width list; the selected row expands with task, Workspace, exact ID, and PR. | Session list remains beside the output tail. |
| **Standard** | At least 58 columns and 24 rows, but below Wide | Full-width list with an expanded selected row. | Output tail replaces the list so it has useful width. |
| **Compact** | Fewer than 58 columns or fewer than 24 rows | Ordinary rows use one line; the selected row uses a second task line. | Output tail becomes the primary surface. |

The prompt is reserved at the bottom before the remaining rows are allocated.
The New Session wizard is a primary surface in all layouts, so every step remains
usable when a mobile keyboard reduces the available height. Content is truncated
by terminal-cell width without splitting Unicode text.

## Keyboard map

| Key | Action |
|---|---|
| `↑` / `↓` | Move the selected row. With Peek open, output follows the selection. |
| `Enter` / `→` | Attach to Running, or resume and attach to Stopped. |
| `Space` | Open or close Peek for Running. For Stopped, resume in the background; this may first require latest-conversation confirmation. |
| Type + `Enter` | Dispatch with the default provider. In Peek, send a reply to the selected session. |
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
| `?` | Open key help. |
| `Esc` | Close the current overlay or input; from the base dashboard, quit. |

Inside an attached session, `Ctrl+B d` detaches. A bare left arrow also detaches
when the provider input is empty and the quick-detach option is enabled. See the
README for the complete attach-key contract.

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
prompt content. Peek replies also keep `/` as literal input rather than entering
filter mode; an empty Operations dashboard still opens the filter and shows a
zero-result state.

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
   `Space` to dedicate the primary surface to Peek and `Esc` to return.
3. Use `e` for the bounded wizard instead of composing a long inline dispatch.
4. Use `Ctrl+G` with a terminal/editor combination that supports external editor
   handoff when a multi-line prompt is easier outside the small viewport.

The bottom prompt and current primary action remain visible as the height
changes. UAM does not assume a fixed phone aspect ratio.

## SSH, mouse, and paste

Mouse reporting defaults on for local attachment and off when the environment
contains `SSH_CONNECTION` or `SSH_TTY`. Override it with
`UAM_ATTACH_MOUSE=on|off|auto`. Keeping it off over SSH lets the terminal retain
selection and paste gestures; setting it on restores provider mouse interaction.

Bracketed-paste payload is forwarded literally, including control bytes, UTF-8,
and line endings. UAM cannot initiate paste from a local clipboard. Windows users
should run SSH in Windows Terminal and configure a Windows Terminal paste
binding. See [ADR 0002](adr/0002-terminal-ownership-over-ssh.md) for the terminal
ownership boundary and limitations.

## Accessibility and no-color operation

- `NO_COLOR` disables styling even when the terminal advertises color.
- Lifecycle and pull-request states have distinct glyphs, so meaning is not
  encoded by color alone.
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
2. Confirm Windows Terminal has a paste binding for the gesture or key chord.
3. Keep the remote setting at `UAM_ATTACH_MOUSE=auto`, or set it to `off`.
4. Test `Ctrl+Shift+V` or `Shift+Insert` to separate a client binding problem
   from remote input handling.

If resuming a stopped row reports ambiguity, read the provider and Workspace in
the message. Confirm only when selecting the provider's latest conversation is
acceptable. Otherwise start a new Managed Session or restore an exact provider
identity.
