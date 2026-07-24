# ADR 0002: Terminal ownership over SSH

- Status: Accepted
- Date: 2026-07-12
- Amended: 2026-07-14 — provider scrolling now takes priority over terminal-owned mouse gestures by default.

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

### Screen ownership follows provider policy

When output is a terminal, input independently enters raw mode. The provider's
terminal policy decides the outer screen:

| Outer-screen policy | Current providers | Attach behavior |
|---|---|---|
| UAM | Claude Code, Copilot, Hermes, Oh My Pi, OpenCode | UAM enters and owns an alternate screen. Provider alternate-screen sequences are contained inside that boundary. |
| Primary | OpenAI Codex | UAM attaches on the primary screen and does not create an outer alternate screen. |

The Codex primary-screen exception is deliberate. It does not change the host's
PTY ownership, the one-controller input/resize/reply rule, or native provider
keys. Seven-bit DEC-private CSI handling remains bounded; UAM does not interpret
a bare C1 byte as CSI because that byte can occur inside UTF-8 input.

On detach or return, UAM drains output and restores the terminal contract it
owns: reset attributes, disable mouse/focus tracking, disable bracketed paste,
show the cursor, leave an outer alternate screen only when one was entered, and
return to a clean line. The dashboard separately repaints after an attached
process returns. Cleanup is targeted; it does not reset the entire terminal or
erase scrollback.

Non-terminal streams do not receive alternate-screen control sequences.

### Mouse policy is explicit

`UAM_ATTACH_MOUSE` accepts:

| Value | Behavior |
|---|---|
| `auto` or unset | Preserve provider mouse reporting locally and over SSH. |
| `on` | Preserve provider mouse reporting, including over SSH. |
| `off` | Suppress provider mouse-reporting modes while attached. |

Invalid values use the default `auto` behavior. Only explicit `off` suppresses
provider mouse reporting, retaining the former SSH behavior for users who
prioritize terminal-owned selection and paste. With mouse reporting off, UAM
removes only the well-known mouse mode parameters from provider control
sequences and preserves unrelated terminal modes without rewriting provider
text.

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
SSH. Configure a keyboard paste binding such as `Ctrl+V`, `Ctrl+Shift+V`, or
`Shift+Insert`; the exact binding belongs to Windows Terminal. Once it sends the
text, UAM forwards it unchanged. Provider mouse reporting is preserved by
default so remote providers can scroll. Set `UAM_ATTACH_MOUSE=off` on the remote
host when terminal-owned selection or right-click paste is more important.

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

### Disable provider mouse reporting automatically over SSH

Rejected because it prevents wheel and touch scrolling in providers such as
OpenCode and OMP. Users who prioritize terminal-owned selection or paste can
still opt out with `UAM_ATTACH_MOUSE=off`.

### Strip all private terminal modes

Rejected because providers legitimately use cursor, focus, keyboard, and paste
modes. Filtering is limited to alternate-screen ownership and the configured
mouse policy.

## Consequences

- Returning from attach or quitting the dashboard leaves a predictable shell
  surface without erasing scrollback.
- SSH defaults favor provider mouse interaction; terminal selection and paste
  may require keyboard bindings or explicit `UAM_ATTACH_MOUSE=off`.
- Pasted control bytes cannot accidentally trigger UAM detach or suppression
  shortcuts.
- Terminal-client configuration remains necessary when the client does not send
  a paste operation.
- `TERM` and color hints received through SSH are diagnostic metadata only. They
  are not capability proof and do not authorize a different terminal protocol.
- Normal detach, interrupt, hangup, and termination paths restore the owned
  terminal contract. SIGKILL cannot run cleanup, so it cannot promise restored
  modes; use a fresh terminal or `reset` when the local terminal is unusable.

The controller, standby, observer, and protocol-v2 ownership rules are defined
by [ADR 0003](0003-terminal-client-session-ownership-and-protocol-v2.md).
