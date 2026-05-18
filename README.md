# unified-agent-manager (`uam`)

<p align="center">
  <a href="https://github.com/RandomCodeSpace/unified-agent-manager/actions/workflows/security.yml"><img alt="Security" src="https://img.shields.io/github/actions/workflow/status/RandomCodeSpace/unified-agent-manager/security.yml?branch=main&label=security&style=for-the-badge&logo=githubactions&logoColor=white"></a>
  <a href="https://github.com/RandomCodeSpace/unified-agent-manager/actions/workflows/sonar.yml"><img alt="SonarCloud" src="https://img.shields.io/github/actions/workflow/status/RandomCodeSpace/unified-agent-manager/sonar.yml?branch=main&label=sonarcloud&style=for-the-badge&logo=sonarcloud&logoColor=white"></a>
  <a href="https://sonarcloud.io/project/overview?id=RandomCodeSpace_unified-agent-manager"><img alt="Quality Gate" src="https://img.shields.io/sonar/quality_gate/RandomCodeSpace_unified-agent-manager?server=https%3A%2F%2Fsonarcloud.io&style=for-the-badge&logo=sonarcloud"></a>
  <a href="https://github.com/RandomCodeSpace/unified-agent-manager/releases"><img alt="Release" src="https://img.shields.io/github/v/release/RandomCodeSpace/unified-agent-manager?style=for-the-badge&logo=github"></a>
  <a href="https://go.dev/"><img alt="Go" src="https://img.shields.io/github/go-mod/go-version/RandomCodeSpace/unified-agent-manager?style=for-the-badge&logo=go"></a>
</p>

A terminal UI that replicates Claude Code's "agent view" experience across
multiple coding-agent CLIs in one unified dashboard: Claude Code, OpenAI
Codex, GitHub Copilot CLI, and OpenCode.

Status: **complete MVP across PLAN.md Phases 0–12**.

## Features

- Single Go/Bubble Tea binary: `uam`
- Private tmux backend: every managed session runs under `tmux -L uam`
- Agent adapters for:
  - Claude Code: `claude --dangerously-skip-permissions`
  - Codex: `codex --sandbox danger-full-access`
  - GitHub Copilot CLI: `copilot --autopilot` or `gh copilot --autopilot`
  - OpenCode: `opencode --auto-approve`
- Persistent metadata at `${XDG_CONFIG_HOME:-~/.config}/uam/sessions.json`
- Atomic JSON writes, flock locking, schema migration backups, corrupt-file self-healing
- TUI grouping by session state: Needs Input, Working, Review, Failed, Completed
- Peek/reply/attach/stop flows backed by tmux `capture-pane` and `send-keys`
- Pin, rename, group-by-dir toggle, and persisted manual reorder
- PR URL detection from pane output plus optional `gh pr view` status refresh
- Shell commands for automation

## Build

```sh
go install github.com/RandomCodeSpace/unified-agent-manager/cmd/uam@latest
make build      # produces ./bin/uam
make test       # go test ./...
make run        # build + launch the TUI
```

Go names installed binaries after the last import-path element. The root path
`github.com/RandomCodeSpace/unified-agent-manager` therefore installs a binary
named `unified-agent-manager`; use `/cmd/uam` when you want the `uam` command.

Requires Go 1.24+ and tmux 3.x. Agent CLIs are capability-probed at runtime;
unavailable providers are hidden from the TUI dispatch selector.

## CLI

```sh
uam                              # open TUI
uam new                          # guided terminal dispatch flow
uam dispatch [--safe] <agent> "prompt"
uam dispatch --cwd /path/to/repo claude "fix flaky tests"
uam ls [--json]
uam peek <id>
uam attach <id>
uam last
uam version
uam stop <id>                    # kill tmux session, keep record
uam rm <id>                      # kill tmux session and remove record
```

## TUI keys

| Key | Action |
|---|---|
| `↑` / `↓` | Move selection |
| `Enter` / `→` | Attach selected session |
| Type prompt + `Enter` | Dispatch to default agent |
| `@codex prompt` | Dispatch to a specific agent |
| `Tab` | Cycle default agent |
| `Space` | Toggle peek panel |
| `Ctrl+T` | Pin selected session |
| `Ctrl+R` | Rename selected session |
| `Ctrl+X` | Stop/remove selected session with confirmation |
| `Ctrl+S` | Toggle group-by-directory |
| `Shift+↑/↓` | Reorder rows and persist `sort_index` |
| `e` | Open easy-mode wizard |
| `?` | Help overlay |
| `q` | Quit |

## Development

```sh
go test ./...
make build
```

## Security and quality

GitHub Actions run security checks on `main`, pull requests, and a weekly
schedule:

- CodeQL analysis for Go code scanning
- `govulncheck ./...` for known Go vulnerabilities
- `gosec ./...` with SARIF upload to GitHub code scanning
- Dependency Review on pull requests
- SonarCloud analysis with Go coverage from `coverage.out`

SonarCloud requires a repository secret named `SONAR_TOKEN`. The workflow skips
only the SonarCloud upload step until that secret exists.

Core packages:

- `internal/store`: sessions JSON, locking, migration, backups
- `internal/tmux`: all tmux shell-out logic
- `internal/adapter`: shared adapter interfaces, tmux adapter, state detection
- `internal/adapter/{claude,codex,copilot,opencode}`: provider registrations
- `internal/app`: service layer and Bubble Tea model
- `internal/pr`: optional GitHub PR status lookup
- `internal/refresh`: refresh ticker policy
