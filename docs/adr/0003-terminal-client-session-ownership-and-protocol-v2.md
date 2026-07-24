# ADR 0003: Terminal client/session ownership and protocol v2

- Status: Accepted
- Date: 2026-07-23

## Context

A Managed Session is durable; an attached terminal client is not. One host can
have several attached terminals, each with different size, terminal behavior,
and network lifetime. Persisting a client ID, terminal dimensions, capabilities,
or controller role would make a later attach trust stale state. Allowing every
client to write would also corrupt provider input and make resize nondeterministic.

## Decision

The host owns the PTY, session runtime, provider launch, and durable session
record. The provider owns its conversation. A client owns only its local screen,
raw-mode lifetime, and its own input stream. Client state is live runtime state;
it is never written to the session configuration.

### Roles and one-writer rule

| Role | Receives provider output | May write stdin, resize, or reply | Transition |
|---|---|---|---|
| Controller | Yes | Yes; the only writer | Starts as first non-observer, or receives a transfer/promotion. |
| Standby | Yes | No | Requests control or is promoted when the controller leaves. |
| Observer | Yes | No | Remains read-only. |

There is exactly one controller. PTY stdin, terminal resize, and provider reply
are a single ownership domain: while a controller exists, other client writes
and out-of-band replies/resizes are rejected or ignored. Controller changes use
an ownership generation, so delayed frames from an old controller cannot regain
write access. A controller can transfer to the next standby; controller loss
promotes the next standby. Observers are never promoted merely by waiting.

### Attach controls and modes

The default control prefix is `Ctrl+B`; a profile can set `C-a` through `C-z`.
Outside bracketed paste, prefix commands are local to UAM:

| Command | Result |
|---|---|
| `prefix d` | Detach. |
| `prefix c` | Send literal `Ctrl+C` to the provider. |
| `prefix r` | Request control. |
| `prefix o` | Transfer control when used by the controller. |
| `prefix i` | Show client role and selected/effective profile. |
| `prefix m` | Toggle mouse passthrough for this attachment only. |
| `prefix prefix` | Send a literal prefix byte to the provider. |

Bracketed-paste payload bypasses prefix parsing completely. Mouse policy controls
whether UAM locally filters provider mouse modes; it does not turn a mouse event
that the terminal or SSH client never sent into input. The temporary mouse toggle
is client-local and expires when that attachment exits.

### Protocol compatibility matrix

Protocol v1 is the legacy attach stream. Protocol v2 adds a client hello,
role/generation events, and framed server output. Both keep provider key bytes
native; UAM does not translate a provider keyboard protocol.

| Host | Client | Result |
|---|---|---|
| v1 | v1 | Legacy raw PTY output; one legacy controller. |
| v1 | v2 | v2 client accepts an unversioned legacy response and consumes raw PTY output; no v2 ownership features. |
| v2 | v1 | v2 host preserves the v1 raw stream and one-controller legacy behavior. |
| v2 | v2 | Framed PTY/control output, hello validation, controller/standby/observer roles, and generation checks. |
| Either | Explicit unsupported version | Reject the attach; do not silently downgrade an explicit version. |

A terminal name, color hint, or claimed capability in a hello is metadata used
for bounded diagnostics and safe local decisions. It is not evidence of terminal
support. Unsupported or secret-like hints are redacted in diagnostics.

### Provider terminal policy

Every provider keeps native provider input. The current outer-screen policy is:

| Provider | Outer screen | Key protocol |
|---|---|---|
| Claude Code | UAM | Native |
| OpenAI Codex | Primary | Native |
| GitHub Copilot CLI | UAM | Native |
| Hermes Agent | UAM | Native |
| Oh My Pi | UAM | Native |
| OpenCode | UAM | Native |

`TERM` supplied to the provider is fixed to the UAM-supported value; a client
TERM hint does not override it. Profiles may select provider, approval mode,
command alias, mouse policy, control prefix, quick-detach policy, and
scrollback. They cannot inject environment, terminal capability, provider
resume, or unsafe-resume policy.

### Persistence and recovery

Schema v4 stores session and profile data, not runtime client state. Older
schema files are copied to an adjacent backup before migration; migration then
writes atomically. Restore a chosen backup only while UAM is stopped and use a
binary compatible with that backup. Equal-schema unknown fields are preserved;
a newer schema is read-only to an older binary.

Stopping or rebooting ends the host and PTY. Retained records support a
provider-aware relaunch/resume according to the provider policy; they do not
preserve a live PTY, terminal modes, controller, or attached clients. Normal
detach and handled signals restore terminal modes. SIGKILL cannot execute that
cleanup, so a terminal may need `reset` or replacement.

### Diagnostics and SSH

`uam doctor --json` reports store/runtime health, profile validity, provider
terminal policy, and available protocol versions. `uam doctor <session-id>
--json` also reports role counts, selected/effective profile, and fallback
reasons. It excludes terminal content and redacts secret-like identifiers.

SSH transports bytes. It does not transport the local clipboard, right-click
meaning, or a guarantee that a terminal supports a named TERM. Use keyboard
paste bindings on the SSH client. `UAM_ATTACH_MOUSE=off` favors local selection
and paste; `auto` and `on` preserve provider mouse reporting. See
[ADR 0002](0002-terminal-ownership-over-ssh.md) for cleanup and client limits.

## Consequences

- A reconnect gets a fresh client identity and negotiated state, never an old
  controller lease.
- Operators can predict which terminal may change provider input or geometry.
- Old hosts and clients keep working within the explicit v1 boundary instead of
  receiving invented v2 behavior.
- Terminal smoke evidence records observed behavior; it does not turn TERM
  names into a capability database.
