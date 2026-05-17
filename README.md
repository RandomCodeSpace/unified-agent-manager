# unified-agent-manager (`uam`)

A terminal UI that replicates Claude Code's "agent view" experience across
multiple coding-agent CLIs in one unified dashboard: Claude Code, OpenAI
Codex, GitHub Copilot CLI, and OpenCode.

> Status: **Phase 0 skeleton.** This is a bootstrapped Go module with an
> empty Bubble Tea app. Adapter packages, tmux backend, and TUI table land
> in subsequent phases.

## Build

```sh
make build      # produces ./bin/uam
make run        # build + launch the (empty) TUI; press q to quit
```

Requires Go 1.24+ and tmux 3.x.

## Roadmap

| Phase | Scope |
|---|---|
| 0 | Skeleton (this commit): `go mod`, Makefile, Bubble Tea entry, file logger |
| 1 | `internal/store` (atomic write + flock + schema versioning) + `internal/tmux` wrapper on private socket `-L uam` |
| 2 | ClaudeAdapter (dispatch/list/attach/stop in yolo mode) + shell subcommands |
| 3 | TUI MVP: table, dispatch input, attach/stop keys |
| 4 | Peek + Reply via `capture-pane` + `send-keys` |
| 5 | Claude `detect.go` patterns + grouping (Needs input / Working / Completed / Review) |
| 6 | CodexAdapter + `@<agent>` dispatch selector |
| 7 | CopilotAdapter + OpenCodeAdapter (capability-probed) |
| 8 | Easy-mode wizard (`uam new` + `e` keybinding) |
| 9 | State-detection polish (pane-hash demotion, optional `--classifier=llm`) |
| 10 | PR status dot via `gh` |
| 11 | Pin / rename / group-by-dir / reorder |
| 12 | Help overlay, confirm overlays, prune-old, README screencast |

## References

- Claude Code agent view (UX inspiration): <https://code.claude.com/docs/en/agent-view>
- ctm (session-persistence patterns): <https://github.com/RandomCodeSpace/ctm>
