# ADR 0002: Terminal ownership over SSH

- Status: Accepted
- Date: 2026-07-12

## Context

UAM's dashboard, attach client, provider TUI, local terminal emulator, SSH
transport, and remote shell can all change terminal modes. If ownership is not
explicit, a provider can leave the dashboard or shell with overlapping output,
hidden cursors, mouse escape sequences, or bracketed-paste state that no longer
matches the active program.

SSH adds another boundary: the remote process receives bytes, not the local
clipboard or a right-click event. Terminal applications also disagree about
whether mouse tracking should capture clicks instead of performing local text
selection and paste.

## Decision

### Attach owns its terminal screen

When output is a terminal, the attach client enters an alternate screen that it
owns. Input independently enters raw mode when it is a terminal. Provider output
is rendered inside that boundary. Seven-bit DEC-private CSI sequences used by
supported providers to enter or leave the alternate screen are contained so
they cannot escape into the user's primary shell screen. The filter deliberately
does not interpret a bare C1 byte as CSI because that byte value can occur inside
UTF-8 input; supported provider output uses the seven-bit `ESC [` form.

On detach or return, UAM drains output within the owned screen and restores the
terminal contract: reset attributes, disable mouse/focus tracking, disable
bracketed paste, show the cursor, leave the owned alternate screen, and return
to a clean line. The dashboard separately repaints after an attached process
returns. Cleanup is intentionally targeted; UAM does not reset the entire
terminal or erase scrollback.

Non-terminal streams do not receive alternate-screen control sequences.

### Mouse policy is explicit

`UAM_ATTACH_MOUSE` accepts:

| Value | Behavior |
|---|---|
| `auto` or unset | Enable provider mouse reporting locally. Disable it when `SSH_CONNECTION` or `SSH_TTY` indicates an SSH session. |
| `on` | Preserve provider mouse reporting, including over SSH. |
| `off` | Suppress provider mouse-reporting modes while attached. |

Invalid values use `auto`. With mouse reporting off, UAM removes only the
well-known mouse mode parameters from provider control sequences and preserves
unrelated terminal modes. This makes local selection and terminal-defined paste
more predictable over SSH without rewriting provider text.

### Paste is a byte stream

UAM recognizes bracketed-paste start and end markers across input packet
boundaries. While a paste is active, payload bytes pass to the provider
literally. Attach shortcuts such as `Ctrl+B`, `Ctrl+C`, and `Ctrl+Z` are not
interpreted inside that payload. UTF-8, newlines, and carriage returns are not
normalized.

Outside bracketed paste, the established attach keys retain their meaning.

### Client limitations stay visible

UAM can preserve only bytes that the terminal and SSH client send. It cannot
observe the local clipboard, convert a Windows mouse event into remote input, or
make PowerShell's console host behave like another terminal.

For Windows clients, Windows Terminal is the recommended host for PowerShell
SSH. Configure a local paste binding such as right-click, `Ctrl+Shift+V`, or
`Shift+Insert`; the exact binding belongs to Windows Terminal. Once it sends the
text, UAM forwards it unchanged. If selection or right-click is captured by a
remote provider, keep `UAM_ATTACH_MOUSE=auto` or set it to `off` on the remote
host.

Windows remains an SSH client in this design. UAM is not a native Windows
process.

## Rejected alternatives

### Clipboard emulation and OSC 52

Rejected. UAM does not read or synchronize the local clipboard and does not
inject OSC 52. Clipboard access has different security and consent semantics,
is inconsistently supported through SSH, and would not repair a client that
never sends the paste action.

### Full terminal reset or scrollback clear

Rejected because it destroys user-visible shell history and resets unrelated
terminal preferences. Targeted cleanup is sufficient to re-establish ownership.

### Always enable provider mouse reporting

Rejected because many SSH terminal workflows depend on the local terminal for
selection and paste. Users can still opt in with `UAM_ATTACH_MOUSE=on`.

### Strip all private terminal modes

Rejected because providers legitimately use cursor, focus, keyboard, and paste
modes. Filtering is limited to alternate-screen ownership and the configured
mouse policy.

## Consequences

- Returning from attach or quitting the dashboard leaves a predictable shell
  surface without erasing scrollback.
- SSH defaults favor terminal selection and paste; local use retains provider
  mouse interaction.
- Pasted control bytes cannot accidentally trigger UAM detach or suppression
  shortcuts.
- Terminal-client configuration remains necessary when the client does not send
  a paste operation.
