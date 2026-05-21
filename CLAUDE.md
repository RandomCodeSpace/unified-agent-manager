# CLAUDE.md — unified-agent-manager (`uam`)

A Go + Bubble Tea terminal UI that manages multiple coding-agent CLIs
(Claude Code, OpenAI Codex, GitHub Copilot, Hermes, OpenCode) in one
dashboard. Each managed session runs inside a private tmux server
(`tmux -L uam`); `uam` infers session state by regex-classifying
`capture-pane` output. `PLAN.md` is the authoritative spec (Phases 0–12).

## Commands

```sh
make build      # -> ./bin/uam
make test       # go test ./...
make run        # build + launch the TUI
make lint       # golangci-lint run ./...
make clean      # rm -rf bin
```

Real binary entrypoint is `cmd/uam`; root `main.go` is a thin
compatibility shim (`go install <module-root>` installs a binary named
`unified-agent-manager`). Requires Go 1.24+ and tmux 3.x.

## Architecture

```
cmd/uam, main.go            entrypoints (main.go = compat shim)
internal/cli                argument routing, command dispatch
internal/app                Service (business logic) + Bubble Tea Model
internal/adapter            AgentAdapter interface, shared TmuxAgent,
                            Registry, pane state classification (detect.go)
internal/adapter/<provider> per-agent factories (claude, codex, copilot,
                            hermes, opencode) — each ~10 lines
internal/tmux               all tmux shell-out logic
internal/store              sessions.json: flock, atomic write, migration
internal/pr                 optional `gh pr view` status lookup
internal/refresh            refresh ticker policy
internal/{log,version,execpath}  support packages
```

Flow: `cli` parses args → `app.Service` orchestrates → `adapter`
implementations drive agents → `tmux` shells out + `store` persists.
Providers are capability-probed at startup; unavailable CLIs are hidden.

## Conventions

- **Surgical changes.** Match existing style; touch only what the task needs.
- **Go style:** `gofmt`/`goimports` clean, wrap errors with `%w`, small
  interfaces, accept interfaces / return structs.
- **No shell expansion.** Commands reach tmux via argv + custom
  `ShellJoin`/`shellQuote` (`internal/tmux/tmux.go`). Keep it that way.
- **`#nosec` annotations** are deliberate and documented inline — preserve
  the rationale comment when editing nearby code.
- **Store writes** must stay atomic (temp file + rename) and flock-guarded.
- New providers: add an `internal/adapter/<name>` factory using
  `adapter.NewTmuxAgent`, register it in `cli.NewService`.

## Testing

Standard `go test`; every package has co-located `_test.go` files.
Run before declaring work done:

```sh
go vet ./... && gofmt -l . && go test ./...
```

CI also runs `govulncheck`, `gosec`, SonarCloud, and CodeQL on `main`/PRs.
