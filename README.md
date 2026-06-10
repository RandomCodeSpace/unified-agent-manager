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
- Shows active and closed sessions in one dashboard
- Lets you peek at recent output without attaching (4000 lines of scrollback)
- Sends replies back into running agent sessions
- Persists session metadata across restarts, including each agent's exit code
- Supports pinning, renaming, manual reorder, and group-by-directory
- Detects GitHub PR URLs from agent output and can refresh PR state when `gh` is available
- Supports per-session command aliases such as a custom Copilot launcher

## Requirements

- Go 1.24+ to build from source
- Any provider CLI you want to manage already installed and authenticated

That's it — agents are spawned directly under uam's own session hosts, so
there is nothing else to install.

Providers are capability-probed at runtime. If a CLI is missing, `uam` hides it
from the dispatch UI instead of failing the whole app.

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

Guided dispatch flow:

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
uam new                          # guided dispatch wizard
uam dispatch [--safe] [--alias <name>] <agent> [#session-name] [prompt]
uam ls [--json]
uam peek <id>
uam attach <name-or-id>
uam last
uam stop <id>                    # kill the session, keep record
uam rm <id>                      # kill the session and remove record
uam kill-all                     # stop every managed session
uam version
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
| `Space` | Toggle peek panel |
| `Ctrl+T` | Pin selected session |
| `Ctrl+R` | Rename selected session |
| `Ctrl+X` | Stop or remove the selected session with confirmation |
| `Ctrl+S` | Toggle group-by-directory |
| `Shift+↑/↓` | Reorder rows |
| `e` | Open the guided dispatch wizard |
| `?` | Open help |
| `Esc` | Close overlays, clear input, or quit |

## Attached sessions

`uam attach` (or `Enter` in the TUI) bridges your terminal straight to the
agent's PTY:

- `Ctrl+B d` detaches and returns to the dashboard (`Ctrl+B Ctrl+B` sends a
  literal `Ctrl+B` to the agent)
- `←` (left arrow) also detaches when you haven't typed anything since the
  last submit/clear — tap it to hop back to the dashboard. Inside a draft it
  moves the cursor as usual, and after history/menu navigation it stays
  passthrough until the next `Enter`/`Esc`. Set `UAM_ATTACH_BACK_DETACH=0`
  to disable.
- The session keeps running after you detach or close the terminal
- `Ctrl+Z` is swallowed while attached — suspending an agent inside a detached
  session would leave it impossible to foreground
- Several terminals can attach to the same session at once

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

## Safety model

`uam` can launch providers in their full-access or auto-approve mode when the
provider supports it. Use `uam dispatch --safe ...` when you want the provider's
default approval behavior instead.

`uam` does not make git checkpoints, stash changes, or modify your repository on
its own. It starts and manages agent sessions; the provider remains responsible
for its own execution model.

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
