# unified-agent-manager (`uam`)

<p align="center">
  <a href="https://github.com/RandomCodeSpace/unified-agent-manager/actions/workflows/ci.yml"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/RandomCodeSpace/unified-agent-manager/ci.yml?branch=main&label=ci&style=for-the-badge&logo=githubactions&logoColor=white"></a>
  <a href="https://github.com/RandomCodeSpace/unified-agent-manager/actions/workflows/security.yml"><img alt="Security" src="https://img.shields.io/github/actions/workflow/status/RandomCodeSpace/unified-agent-manager/security.yml?branch=main&label=security&style=for-the-badge&logo=githubactions&logoColor=white"></a>
  <a href="https://sonarcloud.io/project/overview?id=RandomCodeSpace_unified-agent-manager"><img alt="Quality Gate" src="https://img.shields.io/sonar/quality_gate/RandomCodeSpace_unified-agent-manager?server=https%3A%2F%2Fsonarcloud.io&style=for-the-badge&logo=sonarcloud"></a>
  <a href="https://github.com/RandomCodeSpace/unified-agent-manager/releases"><img alt="Release" src="https://img.shields.io/github/v/release/RandomCodeSpace/unified-agent-manager?style=for-the-badge&logo=github"></a>
  <a href="https://go.dev/"><img alt="Go" src="https://img.shields.io/github/go-mod/go-version/RandomCodeSpace/unified-agent-manager?style=for-the-badge&logo=go"></a>
</p>

`uam` keeps coding-agent terminals running after you close the dashboard or lose
an SSH connection. It gives Claude Code, Codex, Copilot, Hermes, Oh My Pi, and
OpenCode one session roster without depending on tmux.

Each managed session runs under a small detached `uam` host that owns the
provider PTY, terminal state, scrollback, and a private Unix socket. Reopening
the dashboard reconnects to that host. If the process stopped, UAM can relaunch
it with the provider's supported resume behavior.

```text
your terminal <-> uam attach <-> Unix socket <-> detached host <-> PTY <-> agent CLI
```

## What you get

- One mouse-friendly and keyboard-friendly dashboard for every installed agent
  CLI.
- Detached sessions that survive dashboard exit, terminal close, and SSH
  disconnect.
- Explicit `Running`, `Stopped`, and `Failed` states based on process liveness,
  not guesses made from terminal text.
- Attach, resume, restart, stop, remove, pin, rename, filter, reorder, and
  workspace grouping.
- Exact provider resume where the provider exposes stable identity, with a
  fail-closed confirmation before ambiguous latest-session resumes.
- One controller per attached session, deterministic handoff to standby
  clients, and protocol-compatible multi-terminal viewing.
- Persistent launch and attachment profiles for provider, approval mode,
  command alias, mouse handling, control prefix, quick detach, and scrollback.
- GitHub pull request discovery from provider output, with optional status
  refresh through `gh`.

UAM manages processes and terminal connections. It does not create branches,
worktrees, commits, stashes, or filesystem isolation.

## Supported providers

UAM probes provider executables at startup. Missing CLIs stay out of the
dashboard instead of breaking it.

| Provider | Command | Resume behavior | Outer screen |
|---|---|---|---|
| Claude Code | `claude` | Exact when Claude supports a seeded session ID; guarded latest continuation for older records | UAM |
| OpenAI Codex | `codex` | Guarded `resume --last` | Primary |
| GitHub Copilot CLI | `copilot` | Exact by UAM session ID | UAM |
| Hermes Agent | `hermes` | Unsupported; start a new managed session | UAM |
| Oh My Pi | `omp` | Exact for sessions with dedicated provider state; guarded latest continuation for legacy records | Primary |
| OpenCode | `opencode` | Exact provider conversation only | UAM |

OpenCode must be version 1.18.1 or newer. Run `opencode upgrade 1.18.1` if UAM
rejects an older installation.

"Exact" means UAM can address the intended provider conversation. "Guarded"
means the provider can only continue its most recent conversation. When more
than one retained session for that provider shares a workspace, UAM refuses the
heuristic launch until you confirm it in the TUI or pass `--allow-latest` in the
CLI.

## Platform support

| Tier | Systems |
|---|---|
| Release targets | Linux and macOS on AMD64 and ARM64 |
| Distro CI | Ubuntu, Debian, Arch Linux, UBI8, and RHEL-compatible Linux |
| Best effort | Other Linux distributions |
| Client only | Windows Terminal or another Windows SSH client connected to a Linux or macOS host |
| Unsupported host | Native Windows |

Release binaries use `CGO_ENABLED=0`. They do not need distro-specific C
libraries or a local tmux installation.

## Install

Download a release archive and its signed checksum manifest from
[GitHub Releases](https://github.com/RandomCodeSpace/unified-agent-manager/releases),
or install the latest Go module version:

```sh
go install github.com/RandomCodeSpace/unified-agent-manager/cmd/uam@latest
```

To build the current checkout:

```sh
make build
./bin/uam version
```

Source builds require Go 1.25.8 or newer. The module pins its build toolchain,
which Go downloads automatically when needed.

## Start your first session

Install and authenticate at least one supported provider CLI, then run:

```sh
uam doctor
uam new
```

`uam new` asks for a provider, optional command alias, workspace, session name,
and prompt. It launches the provider and attaches immediately.

Detach with `Ctrl+B d`. The provider keeps running. Open the dashboard later
and select the session:

```sh
uam
```

For a non-interactive launch, put flags before the provider name:

```sh
uam dispatch claude "fix the flaky tests"
uam dispatch --cwd /path/to/repo codex "review this package"
uam dispatch --safe --alias ghcp copilot "review this branch"
uam dispatch --profile focused opencode "implement the parser"
```

The default launch mode adds the provider's full-access or auto-approve option
when that provider has one. `--safe` omits those options and leaves the
provider's normal approval behavior in place. It is not an operating-system
sandbox.

## Dashboard controls

The dashboard is a single `Agents` roster. Clicking a row selects it. Only the
named `Attach`, `Resume`, `Stop`, `Remove`, or `Retry` target performs an action.
That separation prevents a tap used for navigation from launching or deleting
anything.

| Input | Action |
|---|---|
| Click a row | Select it |
| Click `Attach` or `Resume` | Run the row's primary action |
| Click `Stop` or `Remove` | Open a confirmation with Cancel focused |
| Mouse wheel, `Up`, `Down` | Move selection |
| `Enter`, `Right` | Attach to Running or resume and attach to Stopped |
| `Space` | Resume a Stopped session without attaching |
| `/` | Filter by name, ID, provider, task, workspace, alias, or lifecycle |
| `e` | Open the new-session wizard |
| `Tab` | Cycle the default provider |
| `Ctrl+T` | Pin or unpin |
| `Ctrl+R` | Rename |
| `Ctrl+X` | Confirm Stop or Remove |
| `Ctrl+S` | Toggle workspace grouping |
| `Shift+Up`, `Shift+Down` | Reorder within the current lifecycle and group |
| `r` | Retry a failed refresh |
| `?` | Toggle the compact help footer |
| `Esc` | Close the current overlay or quit the dashboard |
| `Ctrl+C` | Quit from anywhere |

The roster uses three width classes: Wide at 96 columns or more, Compact from
60 to 95, and Narrow from 40 to 59. Height changes the visible row count, not
row meaning. Below 40 columns or 12 rows, UAM hides destructive targets and
shows a minimum-size notice.

Mouse reporting is enabled by default. Set `UAM_NO_MOUSE=1` before launching
the dashboard when native drag-to-select copying matters more than pointer
controls.

See [Responsive TUI design and operations](docs/responsive-tui.md) for layout,
filtering, mobile use, no-color behavior, and terminal troubleshooting.

## Attached-session controls

An attachment is a temporary client. It is not another managed session. The
first interactive client controls provider input and PTY size. Later clients
receive output as standbys until control transfers or the controller leaves.

The default local prefix is `Ctrl+B`:

| Input | Action |
|---|---|
| `prefix d` | Detach |
| `prefix c` | Send a literal `Ctrl+C` to the provider |
| `prefix r` | Request control |
| `prefix o` | Transfer control to the next standby |
| `prefix i` | Show role and profile information |
| `prefix m` | Toggle provider mouse passthrough for this attachment |
| `prefix prefix` | Send the literal prefix byte to the provider |

A profile can replace `Ctrl+B` with another control letter. Plain `Ctrl+C` and
`Ctrl+Z` are swallowed while attached so they do not accidentally terminate or
suspend a detached provider. Use `prefix c` when you intend to interrupt the
provider.

A bare Left arrow detaches only when the provider input is empty and quick
detach is enabled. It remains a normal cursor key inside a draft. Copilot turns
this shortcut off by default because it uses Left for its own navigation.

Bracketed paste bypasses all prefix handling and passes through byte for byte.
`UAM_ATTACH_MOUSE=off` keeps mouse gestures with the local terminal instead of
the provider. `auto` and `on` preserve provider mouse reporting locally and
over SSH.

The normative controller, standby, observer, and compatibility rules are in
[Terminal client/session ownership and protocol v2](docs/adr/0003-terminal-client-session-ownership-and-protocol-v2.md).

## CLI reference

```text
uam
uam new [--profile <name>]
uam dispatch [--safe] [--cwd <path>] [--alias <name>] [--profile <name>] <agent> [#session-name] [prompt]
uam ls [--json]
uam attach [--allow-latest] <name-or-id>
uam last
uam stop <id>
uam restart [--allow-latest] <id>
uam rm <id>
uam kill-all
uam doctor [<session-id>] [--json]
uam version

uam profile ls [--json]
uam profile show <name> [--json]
uam profile set <name> [profile flags]
uam profile rm <name>
uam profile default <name|none>
uam profile assign <session-id> <name|none>
uam profile override <session-id> [profile flags]
uam profile effective <session-id> [--json]
```

`attach`, `stop`, `restart`, and `rm` accept a full ID, an unambiguous ID
prefix, or the backend session name. `stop` keeps a resumable record. `rm`
stops the process and deletes the record. `restart` keeps the UAM identity and
asks the provider to resume its conversation.

### Profiles

Profiles make repeated launch and attach policy explicit:

```sh
uam profile set focused \
  --provider claude \
  --mode safe \
  --mouse off \
  --prefix C-a \
  --back-detach off \
  --scrollback 8000

uam profile default focused
uam profile effective <session-id> --json
```

Profile flags are `--provider`, `--mode safe|yolo`, `--alias`,
`--mouse auto|on|off`, `--prefix C-a` through `C-z`,
`--back-detach auto|on|off`, `--scrollback`, and repeatable `--unset`.

Resolution order is fixed: built-in defaults, provider policy, the selected
profile, per-session overrides, attachment-local overrides, then negotiated
client capabilities. Profiles cannot inject arbitrary environment variables or
change a provider's resume classification.

## Persistence and recovery

Durable configuration lives at:

```text
${XDG_CONFIG_HOME:-~/.config}/uam/sessions.json
```

Set `UAM_CONFIG_DIR` to place `sessions.json` elsewhere. Writes use a lock,
temporary file, sync, and rename. Schema migrations create an adjacent backup
first. A newer schema opens read-only in an older binary so unknown data is not
silently destroyed.

Live runtime files use `UAM_SESSION_DIR` or a private per-user directory under
the system temp directory, usually `/tmp/uam-<uid>`. That directory contains
short-lived sockets, process identity, and provider handoff state. It is
owner-only and deliberately separate from durable configuration.

A reboot removes the old hosts and PTYs but leaves durable records. Selecting
a Stopped row performs a provider-aware relaunch. It does not restore the old
terminal buffer or attached clients.

## Safety boundaries

- UAM may launch providers with broad file and command permissions. Treat the
  prompt, repository, provider configuration, and instructions the provider
  reads as trusted input.
- `--safe` changes provider arguments. It does not sandbox UAM or the provider
  process.
- Several managed sessions may share one workspace and edit the same files.
  Use separate Git worktrees or checkouts when concurrent tasks need isolation.
- Runtime socket ownership protects sessions from other local users. It does
  not protect the current user from a provider launched under that same user.
- UAM sanitizes stored text before terminal display and excludes terminal
  content and secret-like values from diagnostics.
- Normal detach and handled signals restore terminal modes. `SIGKILL` cannot
  run cleanup. Use `reset` or start a fresh terminal if a killed client leaves
  the terminal unusable.

## Useful environment variables

| Variable | Purpose |
|---|---|
| `UAM_CONFIG_DIR` | Override the durable configuration directory |
| `UAM_SESSION_DIR` | Override the runtime socket and state directory |
| `UAM_CACHE_DIR` | Override the log and cache directory |
| `UAM_NO_MOUSE=1` | Disable dashboard mouse reporting |
| `UAM_ATTACH_MOUSE=auto|on|off` | Control provider mouse passthrough for one attachment |
| `UAM_ATTACH_PREFIX=C-a` | Override the attachment prefix for one client |
| `UAM_ATTACH_BACK_DETACH=0` | Disable bare-Left quick detach for one client |
| `UAM_ASCII=1` | Force ASCII dashboard glyphs |
| `UAM_WIDE=0` | Trust ambiguous Unicode glyphs as one cell wide |
| `NO_COLOR=1` | Disable color styling |
| `UAM_DEBUG=1` | Enable debug logging |
| `VISUAL`, `EDITOR` | Select the external editor used by the TUI prompt editor |

## Development

```sh
make test        # unit, integration, in-process host, and real-PTY fixtures
make test-e2e    # built binary through real host, attach, and dashboard PTYs
make cover       # write coverage.out and print total coverage
make lint        # golangci-lint
make build       # static binary at bin/uam
```

CI also runs the race detector, `go vet`, Staticcheck, distro real-PTY tests,
`govulncheck`, `gosec`, release configuration checks, and signed release
publication.

Read [Testing UAM](docs/testing.md) before changing PTY, terminal, attach, or
session-host behavior. Those paths have failure modes that ordinary unit tests
cannot reproduce.

## Design and operations

- [Terminology](CONTEXT.md)
- [Managed Session vs. Provider Conversation](docs/adr/0001-managed-session-vs-provider-conversation.md)
- [Terminal ownership over SSH](docs/adr/0002-terminal-ownership-over-ssh.md)
- [Terminal client/session ownership and protocol v2](docs/adr/0003-terminal-client-session-ownership-and-protocol-v2.md)
- [Responsive TUI design and operations](docs/responsive-tui.md)
- [Terminal and OS support](docs/terminals.md)
- [Testing UAM](docs/testing.md)

Prebuilt binaries, checksums, SBOMs, and signing material are published on the
[releases page](https://github.com/RandomCodeSpace/unified-agent-manager/releases).
