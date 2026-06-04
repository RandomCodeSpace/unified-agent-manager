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
stopping long-running agent sessions backed by a private tmux server.

Supported providers:

- Claude Code
- OpenAI Codex
- GitHub Copilot CLI
- Hermes Agent
- Oh My Pi
- OpenCode

## What it does

- Runs each managed session inside `tmux -L uam`
- Shows active and closed sessions in one dashboard
- Lets you peek at recent output without attaching
- Sends replies back into running agent sessions
- Persists session metadata across restarts
- Supports pinning, renaming, manual reorder, and group-by-directory
- Detects GitHub PR URLs from agent output and can refresh PR state when `gh` is available
- Supports per-session command aliases such as a custom Copilot launcher

## Requirements

- Go 1.24+ to build from source
- tmux 3.x
- Any provider CLI you want to manage already installed and authenticated

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
uam stop <id>                    # kill tmux session, keep record
uam rm <id>                      # kill tmux session and remove record
uam kill-all                     # stop the private tmux server and all sessions
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

## Session storage

`uam` stores session metadata at:

```text
${XDG_CONFIG_HOME:-~/.config}/uam/sessions.json
```

Writes are atomic and lock-protected. If the file needs migration or recovery,
`uam` creates backup files next to it.

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
