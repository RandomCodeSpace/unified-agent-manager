# unified-agent-manager (`uam`)

<p align="center">
  <a href="https://github.com/RandomCodeSpace/unified-agent-manager/actions/workflows/security.yml"><img alt="Security" src="https://img.shields.io/github/actions/workflow/status/RandomCodeSpace/unified-agent-manager/security.yml?branch=main&label=security&style=for-the-badge&logo=githubactions&logoColor=white"></a>
  <a href="https://github.com/RandomCodeSpace/unified-agent-manager/actions/workflows/sonar.yml"><img alt="SonarCloud" src="https://img.shields.io/github/actions/workflow/status/RandomCodeSpace/unified-agent-manager/sonar.yml?branch=main&label=sonarcloud&style=for-the-badge&logo=sonarcloud&logoColor=white"></a>
  <a href="https://sonarcloud.io/project/overview?id=RandomCodeSpace_unified-agent-manager"><img alt="Quality Gate" src="https://img.shields.io/sonar/quality_gate/RandomCodeSpace_unified-agent-manager?server=https%3A%2F%2Fsonarcloud.io&style=for-the-badge&logo=sonarcloud"></a>
  <a href="https://github.com/RandomCodeSpace/unified-agent-manager/releases"><img alt="Release" src="https://img.shields.io/github/v/release/RandomCodeSpace/unified-agent-manager?style=for-the-badge&logo=github"></a>
  <a href="https://go.dev/"><img alt="Go" src="https://img.shields.io/github/go-mod/go-version/RandomCodeSpace/unified-agent-manager?style=for-the-badge&logo=go"></a>
</p>

`uam` is a terminal dashboard for managing multiple coding-agent CLIs from one place.
It gives you a single TUI for launching, peeking, replying to, attaching to, and
stopping long-running agent sessions — no tmux (or any other multiplexer)
required.

Supported providers:

- Claude Code
- OpenAI Codex
- GitHub Copilot CLI
- Hermes Agent
- Oh My Pi
- OpenCode

## What it does

- Runs each managed session under its own lightweight, detached host process
  (a PTY + terminal emulator + Unix socket) — sessions keep running when the
  TUI exits, exactly like a tmux server, with no external dependency
- Shows Running and Stopped sessions in one dashboard, with grounded exit detail
- Lets you peek at recent output without attaching (4000 lines of scrollback)
- Sends replies back into running agent sessions
- Persists session metadata across restarts, including each agent's exit code
- Supports pinning, renaming, manual reorder, and group-by-directory
- Detects GitHub PR URLs from agent output and can refresh PR state when `gh` is available
- Supports per-session command aliases such as a custom Copilot launcher

## Requirements

- Go 1.25+ to build from source (the pinned toolchain downloads automatically)
- Any provider CLI you want to manage already installed and authenticated
- OpenCode 1.18.1 or newer when using the OpenCode provider. If UAM reports an
  older version, run `opencode upgrade 1.18.1` before dispatching or resuming.

That's it — agents are spawned directly under uam's own session hosts, so
there is nothing else to install.

Providers are capability-probed at runtime. If a CLI is missing, `uam` hides it
from the dispatch UI instead of failing the whole app.

## Supported platforms

- Linux (Ubuntu), on AMD64 and ARM64
- macOS, on AMD64 (Intel) and ARM64 (Apple silicon)
- Native Windows is not supported. Windows Terminal and PowerShell can be used
  only as SSH clients connecting to a Linux or macOS host running `uam`.

## Install

Install the `uam` binary directly:

```sh
go install github.com/RandomCodeSpace/unified-agent-manager/cmd/uam@latest
```

Build locally:

```sh
make build
```

## Quick start

Open the dashboard:

```sh
uam
```

Guided dispatch flow, using OpenCode by default when it is available, then opening the created session immediately:

```sh
uam new
```

Headless dispatch examples:

```sh
uam dispatch claude "fix flaky tests"
uam dispatch --cwd /path/to/repo codex "review this package"
uam dispatch --alias ghcp copilot "review this branch"
```

## CLI

```sh
uam                              # open the TUI
uam new                          # guided dispatch wizard, then attach
uam dispatch [--safe] [--alias <name>] <agent> [#session-name] [prompt]
uam ls [--json]
uam peek <id>
uam attach [--allow-latest] <name-or-id>
uam last
uam stop <id>                    # kill the session, keep record
uam restart [--allow-latest] <id>  # stop the agent and resume it in place
uam rm <id>                      # kill the session and remove record
uam kill-all                     # stop every managed session
uam version
uam doctor [<session-id>] [--json]
uam profile ls [--json]
uam profile show <name> [--json]
uam profile set <name> [profile flags]
uam profile rm <name>
uam profile default <name|none>
uam profile assign <session-id> <name|none>
uam profile override <session-id> [profile flags]
uam profile effective <session-id> [--json]
```

## TUI keys

| Key | Action |
|---|---|
| `↑` / `↓` | Move selection |
| `Enter` / `→` | Attach selected session |
| Type prompt + `Enter` | Dispatch to the default agent |
| `@agent prompt` | Dispatch to a specific agent |
| `@agent:alias prompt` | Dispatch with a command alias |
| `Tab` | Cycle default agent |
| `Space` | Toggle Peek for Running; resume Stopped in the background |
| `Ctrl+T` | Pin selected session |
| `Ctrl+R` | Rename selected session |
| `Ctrl+X` | Stop and remove the selected record, or restart it, with confirmation |
| `Ctrl+S` | Toggle group-by-directory |
| `Shift+↑/↓` | Reorder rows |
| `/` with an empty command | Filter by name, provider, task, workspace, ID, or lifecycle |
| `e` | Open the guided dispatch wizard |
| `?` | Open help |
| `Esc` | Close overlays, clear input, or quit |

The dashboard responds to every terminal resize. Operations always use a
full-width, bordered session list; the selected row expands in place with its
task, Workspace, identity, and pull request. Wide terminals split only when Peek
is open. Compact or keyboard-constrained mobile terminals keep ordinary rows to
one line and expand the selected row to two. See
[Responsive TUI design and operations](docs/responsive-tui.md) for layout
thresholds, filtering, mobile guidance, lifecycle labels, and accessibility.

## Attached sessions

`uam attach` (or `Enter` in the TUI) bridges your terminal straight to the
agent's PTY. An attach client is temporary client state, not part of the
Managed Session record. The host permits one controller at a time; additional
interactive clients wait as standbys, and observers receive output without
being allowed to send input, resize the PTY, or answer terminal queries. See
[terminal client and session ownership](docs/adr/0003-terminal-client-session-ownership-and-protocol-v2.md)
for the normative ownership and protocol rules.

- `Ctrl+B d` detaches and returns to the dashboard by default. `prefix prefix`
  sends a literal configured prefix (`Ctrl+B Ctrl+B` only when the profile uses
  `C-b`); `prefix c` sends a literal `Ctrl+C`. A profile can change the prefix;
  use the profile's `C-x` spelling, such as `C-a`, when configuring it.
- `prefix r` requests control, `prefix o` transfers control when used by the
  current controller, `prefix i` reports the current role, and `prefix m`
  toggles mouse passthrough for this attachment only. A prefix command never
  enters provider input.
- Plain `Ctrl+C` is swallowed while attached so terminal copy shortcuts do not
  cancel the agent
- `←` (left arrow) also detaches when you haven't typed anything since the
  last submit/clear — tap it to hop back to the dashboard. Inside a draft it
  moves the cursor as usual, and after history/menu navigation it stays
  passthrough until the next `Enter`/`Esc`. Set `UAM_ATTACH_BACK_DETACH=0`
  to disable.
- The session keeps running after you detach or close the terminal
- `Ctrl+Z` is swallowed while attached — suspending an agent inside a detached
  session would leave it impossible to foreground
- Several terminals can attach to the same session at once

`UAM_ATTACH_MOUSE` controls whether provider mouse reporting is preserved:

- `auto` (the default) preserves provider mouse reporting locally and over SSH
- `on` preserves provider mouse reporting everywhere
- `off` suppresses provider mouse modes so the terminal keeps selection and
  paste gestures

Bracketed-paste payload is forwarded byte-for-byte. Control bytes inside a paste
do not trigger UAM's attach shortcuts. UAM cannot access the client clipboard or
turn an unsent mouse gesture into remote input. Terminal names and color hints
reported by an attachment are diagnostics metadata, not proof that the client
supports a terminal feature.

For PowerShell SSH, use Windows Terminal and configure a keyboard paste binding
such as `Ctrl+V`, `Ctrl+Shift+V`, or `Shift+Insert`. Provider mouse reporting is
enabled by default so OpenCode and other mouse-aware providers can scroll. Set
`UAM_ATTACH_MOUSE=off` on the remote host when terminal-owned selection or
right-click paste is more important. Native Windows remains unsupported;
Windows is the SSH client in this setup.

In the TUI, `Ctrl+X` followed by `y` stops the process **and removes its stored
record**. Use `uam stop <id>` when you want to stop the process but retain a
Stopped row for later resume. For paste diagnosis, follow the
[SSH troubleshooting steps](docs/responsive-tui.md#ssh-mouse-and-paste).

## Resuming sessions

Detach/reattach never restarts anything — the provider keeps running under its
host and attach is a plain reconnect. Resume applies only when the provider
process is Stopped, such as after a reboot, clean exit, or `uam stop`.

UAM distinguishes an **exact resume**, which targets known provider state, from
a **heuristic resume**, which asks the provider to continue its latest
conversation. If a heuristic provider has multiple retained sessions in the
same workspace, UAM fails closed before launching it. The TUI asks for explicit
confirmation; CLI users may retry only when latest-conversation behavior is
acceptable:

```sh
uam attach --allow-latest <name-or-id>
uam restart --allow-latest <id>
```

Provider behavior:

- **Claude Code**: uam seeds claude's session id with the uam id at dispatch
  (`--session-id`, when the installed claude supports it) and resumes that
  exact conversation with `--resume <id>` — several sessions in the same
  directory each resume their own conversation. Sessions dispatched before
  this feature (or with an older Claude Code) use the guarded `--continue`
  heuristic.
- **Copilot**: exact resume — the session is named with the uam id at
  dispatch (`--name`) and resumed by that exact name (`--resume=<id>`).
- **OpenCode**: exact resume only. UAM learns the current root conversation ID
  and resumes it with `--session`. A stopped legacy record without a valid
  exact identity cannot be resumed; dispatch a new Managed Session instead.
- **Oh My Pi**: new sessions receive a UAM-ID-specific provider state directory,
  making `-c` exact. Legacy records without that directory retain guarded
  latest-conversation behavior.
- **Codex**: `codex resume --last` is heuristic because Codex cannot currently
  be given the UAM ID when the conversation is created.
- **Hermes**: no provider resume command is configured. A stopped Hermes record
  cannot be resumed; dispatch a new Managed Session instead.
- After a reboot, records survive in the store and resume on attach — a
  provider-aware relaunch, not a surviving PTY. The old terminal process and
  its screen modes are gone. A SIGKILL similarly prevents normal terminal
  cleanup; start a fresh terminal or run `reset` if the local terminal is left
  in an unusable mode.

### Multiple sessions in one workspace

UAM can run several Managed Sessions in one project directory. They have
independent UAM IDs, terminal hosts, attach points, and provider conversations,
but they share the same files. The grouped TUI warns when more than one Running
session shares a Workspace. Use separate Git worktrees or checkouts when agents
must not edit the same tree; UAM does not create them automatically.

OpenCode's `/new` creates a new provider conversation *inside the current
Managed Session*. It intentionally does not create another UAM row or host. UAM
tracks the newly selected root conversation for later exact resume. To get two
independently attachable sessions, use `uam new` or `uam dispatch` again.

Each managed OpenCode terminal owns a private authenticated server bound to a
distinct loopback port. UAM uses that server to create or validate the exact
root conversation, attach to that exact ID, and observe `/new` root changes.
Consequently, two OpenCode sessions in the same Workspace retain independent
ports, credentials, terminal hosts, and provider conversation IDs.

### OpenCode upgrade cleanup

Current UAM releases do not create, inspect, repair, execute, or delete the
legacy identity plugin at
`$XDG_STATE_HOME/uam/providers/opencode/uam-identity-plugin.mjs` (under
`~/.local/state` when `XDG_STATE_HOME` is unset). A stale file there is inert;
its contents, type, ownership, or permissions cannot block OpenCode launch.

No automatic cleanup is performed. If no older UAM installation still needs
that generated state, cleanup is optional. First inspect and verify the exact
UAM-generated directory, then remove only that directory:

```sh
legacy_dir="${XDG_STATE_HOME:-$HOME/.local/state}/uam/providers/opencode"
printf 'Review before removal: %s\n' "$legacy_dir"
ls -la -- "$legacy_dir"
# After verifying the printed path and contents:
rm -rf -- "$legacy_dir"
```

The terminology and compatibility decision are documented in
[Managed Session vs. Provider Conversation](docs/adr/0001-managed-session-vs-provider-conversation.md).

## Session storage

`uam` stores session metadata at:

```text
${XDG_CONFIG_HOME:-~/.config}/uam/sessions.json
```

Writes are atomic and lock-protected. If the file needs migration or recovery,
`uam` creates backup files next to it.

Per-session runtime state (control sockets and state files) lives in a
per-user directory under the system temp dir — `/tmp/uam-<uid>` on most
systems (override with `UAM_SESSION_DIR`) — created owner-only and verified
to be owned by you. The temp dir is used instead of `$XDG_RUNTIME_DIR`
deliberately: logind wipes the runtime dir when your last login session ends,
which would strand detached sessions that survive logout (the same reason
tmux lives in `/tmp/tmux-<uid>`). Hosts periodically refresh their files'
timestamps so age-based `/tmp` cleanup never collects a long-idle session.

Note for distros with `KillUserProcesses=yes` in logind.conf: any detached
process — uam session hosts and tmux alike — is killed at logout unless you
run `loginctl enable-linger`.

> Upgrading from a tmux-backed release: sessions still running inside the old
> `tmux -L uam` server are not visible to the native backend. Finish or stop
> them first (`tmux -L uam kill-server`); stored session records carry over
> unchanged and remain resumable.

### Profiles and diagnostics

Profiles supply stable launch and attach defaults. Resolution is ordered; a
later layer can refine only fields it is allowed to control:

1. **Hard safety invariants** fix the provider `TERM` value to
   `xterm-256color` and reject profile-supplied environment, capability, or
   resume-policy changes.
2. **Global defaults** select the default provider, yolo mode, scrollback,
   mouse policy, `C-b` prefix, and quick-detach behavior.
3. **Built-in provider policy** selects the provider identity, native key
   protocol, and outer-screen policy. OpenAI Codex is the primary-screen
   exception; the other current providers use a UAM outer screen.
4. **Selected named profile** applies the default profile or a session-selected
   profile. A provider-constrained profile must match the session provider.
5. **Per-session overrides** refine the selected profile for that durable
   session.
6. **Client-local attachment overrides** can refine mouse, prefix, and
   quick-detach for only the current attachment.
7. **Capability constraints** apply the client's negotiated capabilities. For
   example, local mouse filtering and an owned outer screen require that the
   client supports them; terminal hints are not capability proof.

Launch-time fields are provider, approval mode, command alias, and scrollback.
The default profile, named profiles, selected session profile, and per-session
overrides are persistent configuration. Mouse, control prefix, and quick-detach
can also be attachment-local; client identity, role, dimensions, protocol, and
capabilities are runtime-only and never enter `sessions.json`.

```sh
uam profile set focused --provider claude --mode safe --mouse off --prefix C-a --back-detach off --scrollback 8000
uam profile default focused
uam profile show focused --json
uam profile ls --json
uam doctor --json
```

Use `uam profile assign <session-id> <name|none>` to select a profile for one
session, `uam profile override <session-id> [profile flags]` for its final
overrides, and `uam profile effective <session-id> --json` to inspect the
resolved result. `uam profile rm <name>` refuses to delete a default or a
profile still selected by a session; clear those references first. `uam doctor
<session-id> --json` reports runtime roles, supported protocol versions,
resolved profile, provider terminal policy, and fallback reasons. Diagnostics
redact secret-like values and do not print terminal input or output.

Schema v4 migrates older records atomically. Before a migration, UAM writes an
exact adjacent `sessions.json.bak.*` backup; if the write fails, the original
remains in place. To roll back, stop UAM, replace `sessions.json` with the
chosen backup, then start a compatible binary. Same-schema unknown fields round
trip. A config from a newer schema opens read-only so an older binary cannot
clobber fields it does not understand.

## Safety model

`uam` launches providers in their full-access or auto-approve ("yolo") mode by
default when the provider supports it. In that mode, treat the repository,
prompt, provider configuration, and any instructions the agent reads as trusted:
the provider may execute commands and change files without pausing for approval.
Use `uam dispatch --safe ...` when you want the provider's default approval
behavior instead. Safe mode changes provider arguments; it is not an operating-
system sandbox and does not reduce the permissions of the `uam` process itself.

OpenCode keeps the same safety-mode contract as the other providers. Default
yolo mode automatically approves permission requests for the active root
conversation tree. `uam dispatch --safe ...` leaves OpenCode permission prompts
visible for the user and does not auto-reply. Safe mode still is not an
operating-system sandbox.

`uam` does not make git checkpoints, stash changes, or modify your repository on
its own. It starts and manages agent sessions; the provider remains responsible
for its own execution model.

## Design and operations

- [Terminology glossary](CONTEXT.md)
- [Managed Session vs. Provider Conversation](docs/adr/0001-managed-session-vs-provider-conversation.md)
- [Terminal ownership over SSH](docs/adr/0002-terminal-ownership-over-ssh.md)
- [Terminal client/session ownership and protocol v2](docs/adr/0003-terminal-client-session-ownership-and-protocol-v2.md)
- [Responsive TUI design and operations](docs/responsive-tui.md)

## Development

```sh
make test
make build
make lint
```

## Releases

Prebuilt binaries are published on the
[GitHub Releases](https://github.com/RandomCodeSpace/unified-agent-manager/releases)
page.
