# unified-agent-manager — Implementation Plan

## Context

The repository `/home/dev/projects/unified-agent-manager` contains a Go implementation of a **terminal UI that replicates Claude Code's "agent view" (`claude agents`) experience but works across multiple coding-agent CLIs in one unified dashboard** — Claude Code, OpenAI Codex, GitHub Copilot CLI, Hermes Agent, OpenCode, and Oh My Pi.

Why: Claude Code has a great background-session dashboard (see https://code.claude.com/docs/en/agent-view), but it only manages Claude sessions. Developers who use Codex, Copilot, Hermes, OpenCode, or Oh My Pi alongside Claude have to context-switch between tools. A unified TUI lets you dispatch, peek, attach, and stop sessions across all of them from one screen, with the same UX patterns: rows grouped as Active or Closed, Space to peek, Enter to attach, Ctrl+X to stop, PR status dots, pin/rename/group, etc.

Intended outcome: a single-binary CLI called `uam` that opens an agent-view-style TUI showing managed sessions across supported providers, with feature parity for the core operations the reference UX provides.

## Key Decisions (confirmed with user)

- **Stack:** Go + Bubble Tea (single static binary; matches lazygit/gh dash distribution model; Bubble Tea's Elm-style model maps cleanly to the event-driven nature of the dashboard).
- **Uniform tmux backend for every agent.** Codex/Copilot/Hermes/OpenCode have no UAM supervisor; the user chose uniformity over Claude's first-class supervisor advantage. Every session — Claude, Codex, Copilot, Hermes, OpenCode, Oh My Pi — runs interactively inside its own tmux session on private socket `-L uam`, named `uam-<agent>-<id>`. Runtime classification is liveness-only: `ClassifyPane` maps pane PID liveness to `adapter.Active`/`adapter.Failed` and `adapter.Alive`/`adapter.Exited`. Pane capture is still used for peek/reply and PR URL scraping.
- **Adapter abstraction:** All backends sit behind a common `AgentAdapter` interface. Current provider packages are thin registrations around the shared `adapter.TmuxAgent`; adding a new CLI later is mostly adding another `internal/adapter/<name>/` factory with command candidates and yolo args.
- **Supported agents at launch:** Claude Code (`claude`), OpenAI Codex (`codex`), GitHub Copilot CLI (`gh copilot` / `copilot`), Hermes Agent (`hermes`), OpenCode (`opencode`), and Oh My Pi (`omp`). Each is one adapter package.
- **Session storage** modeled on [`RandomCodeSpace/ctm`](https://github.com/RandomCodeSpace/ctm)'s `~/.config/ctm/sessions.json` pattern: a single JSON file at `~/.config/uam/sessions.json` with atomic write (temp + rename), flock-based locking, schema versioning with auto-migration, and `.bak.<unix-nano>` backups on schema upgrade. Each record holds `id` (uuid), `name`, `agent`, `mode`, `workdir`, `tmux_session`, `created_at`, `last_seen_at`, `pinned`, `group`, `sort_index`, `status`, plus cached PR info. Runtime liveness is not persisted. Store `status` distinguishes active records from records closed by the user (`store.StatusClosedByUser`); the UI groups by this as Active/Closed.
- **Yolo mode where providers support it.** Sessions are launched with the provider's full-access / auto-approval flag when one is configured: `claude --dangerously-skip-permissions`, `codex --sandbox danger-full-access`, `copilot --yolo`, and `omp --auto-approve`. Providers without a confirmed yolo flag (`hermes`, `opencode`) launch bare. We rely entirely on each provider's own safety mechanisms (Codex's sandbox, Claude's own guardrails, etc.); UAM does NOT layer its own git-checkpoint commits on top. If a provider offers a checkpoint feature natively, it's used as-is.
- **Command aliases.** A dispatch can override the provider launch command while keeping the canonical provider identity: `uam dispatch --alias ghcp copilot ...`, `uam new`'s command-alias prompt, or TUI input like `@copilot:ghcp ...`. The alias is persisted as `command_alias` and passed back into provider resume. Launch resolution prefers a real executable on `PATH`; if not found, UAM runs the alias through the user's interactive shell so profile aliases/functions can work.
- **Easy mode.** A guided wizard (`uam new` with no args, or `e` keybinding inside the TUI) walks the user through: 1) pick provider, 2) confirm or change workdir, 3) enter prompt. Each step uses Bubble Tea's list/input components. The wizard skips disabled providers (capability probe failed at startup). Power users can still bypass with `uam dispatch <agent> "<prompt>"` or by typing `@<agent> <prompt>` directly into the TUI's dispatch input.
- **Build order:** Full phased plan (Phase 0 → Phase 12), implemented in this repository as a cohesive MVP.

## Repository Layout

```
unified-agent-manager/
├── README.md
├── go.mod / go.sum
├── Makefile                       # build, lint, test, install
├── .golangci.yml
├── main.go                        # compatibility shim
├── cmd/uam/main.go                # real uam entrypoint
├── internal/
│   ├── app/
│   │   ├── app.go                 # service layer + Bubble Tea Model/rendering
│   │   └── service.go             # session orchestration and store merge logic
│   ├── adapter/
│   │   ├── adapter.go             # AgentAdapter interface + Session/State types
│   │   ├── tmux_adapter.go        # shared TmuxAgent implementation
│   │   ├── detect.go              # liveness-only ClassifyPane + PR URL extraction
│   │   ├── registry.go            # name → adapter resolution
│   │   ├── claude/claude.go
│   │   ├── codex/codex.go
│   │   ├── copilot/copilot.go
│   │   ├── hermes/hermes.go
│   │   ├── omp/omp.go
│   │   └── opencode/opencode.go
│   ├── tmux/{tmux.go, parse.go}   # ONLY place that shells out to tmux
│   ├── store/store.go             # JSON store, migration, flock, backups
│   ├── pr/gh.go                   # optional `gh pr view` for PR dot
│   └── log/log.go                 # file logger at ~/.cache/uam/uam.log
```

## Core Types (`internal/adapter/adapter.go`)

```
State:        Active | Failed
ProcLiveness: Alive | Exited
PRStatus:     None | Open | Merged | Closed | Draft

Session struct {
  ID, AgentType, DisplayName, Prompt, Cwd, TmuxSession string
  State State
  ProcAlive ProcLiveness
  LastChange, CreatedAt time.Time
  PR *PRRef
  Pinned bool
  Group string
  SortIndex int
  Closed bool             // mirrors store.StatusClosedByUser for UI grouping
}

PeekResult struct {
  TailText string
}
```

## `AgentAdapter` Interface

```
Name() string
DisplayName() string
Available() (bool, string)
Dispatch(ctx, DispatchRequest) (Session, error)
List(ctx) ([]Session, error)
Peek(ctx, id) (PeekResult, error)
Reply(ctx, id, text) error
Attach(id) (AttachSpec, error)        // app suspends TUI and execs argv
Stop(ctx, id) error
```

Adapters are stateless about runtime liveness — they always re-read pane PID truth from tmux. Our own metadata (pin, rename, group, SortIndex, closed status, PR cache) is overlaid by `store` after each adapter call.

## Yolo Mode (no UAM-managed checkpoints)

UAM launches every session with the provider's "full access / skip permissions" flag and stops there. It does NOT make its own git commits, stash changes, or otherwise mutate the workdir before dispatch — that's the provider's responsibility. If a provider has its own checkpoint/rollback feature (e.g. some sandboxed CLIs snapshot state internally), UAM doesn't interfere with it.

| Agent | Yolo invocation |
|---|---|
| Claude | `claude --dangerously-skip-permissions` |
| Codex | `codex --sandbox danger-full-access` |
| Copilot | `copilot --yolo` |
| Hermes Agent | `hermes` |
| Oh My Pi | `omp --auto-approve` |
| OpenCode | `opencode` |

If the user wants safe mode for one dispatch, `uam dispatch --safe <agent> "<prompt>"` drops the yolo flag and lets the provider run with its default prompts.

## Provider Adapters (`internal/adapter/{claude,codex,copilot,hermes,omp,opencode}/`)

Each provider package registers command candidates and yolo arguments, then delegates to the shared `adapter.TmuxAgent`. The shared adapter handles dispatch, resume, command aliases, list, peek, reply, attach, stop, liveness classification, and PR URL scraping.

- **Dispatch:** creates `uam-<agent>-<id>` under `tmux -L uam`, sets `UAM_AGENT`/`UAM_ID`, starts the provider command, and sends the prompt literally with `send-keys -l`.
- **List:** `tmux -L uam list-sessions -F ...`, filter `uam-<agent>-` prefix.
- **Peek:** `tmux -L uam capture-pane -p -t <name> -S -200 -J`.
- **Reply:** `send-keys -l -- "<text>"` then `send-keys Enter`.
- **Attach:** `AttachSpec{argv: ["tmux", "-L", "uam", "attach", "-t", "uam-<agent>-<id>"]}` via `tea.ExecProcess`.
- **Stop:** `tmux -L uam kill-session -t uam-<agent>-<id>`.
- **State detection:** shared `adapter.ClassifyPane(paneAlive)` returns `Active`/`Alive` when the pane PID is live and `Failed`/`Exited` when it is not. It deliberately does not inspect pane text.
- **PR detection:** regex `https://github.com/.../pull/\d+` against a captured pane tail; first match wins, cached in store, refreshed via `internal/pr` at most every 60s.
- **Capability probe:** unavailable provider commands are disabled at startup and removed from the dispatch selector.
- **Command alias:** `DispatchRequest.CommandAlias` replaces the provider command for that session only. `exec.LookPath` wins when the alias is an executable; otherwise `SHELL -ic` is used as a fallback for profile aliases/functions. Unsafe alias names containing shell metacharacters are rejected before launch.

### What we give up by not using `claude --bg`

- No structured `state.json` — UAM uses tmux liveness and persisted metadata instead.
- No first-class "Ready for review" surfaced by Claude's daemon — UAM shows PR status separately from lifecycle grouping.
- No automatic daemon-managed lifecycle (idle timeout, auto-respawn) — tmux sessions live until killed.

These are acceptable for uniformity. If we later add a `--claude-native` flag, it can opt back into the supervisor backend behind the same `AgentAdapter` interface.

## tmux Wrapper (`internal/tmux/tmux.go`)

All commands use private socket `-L uam` to isolate from the user's own tmux server. This is the ONLY place in the codebase that shells out to `tmux`.

| Op | Command |
|---|---|
| Create | `tmux -L uam new-session -d -s <name> -c <cwd> -x 200 -y 50 'env UAM_AGENT=<agent> UAM_ID=<id> <cmd>'` |
| List | `tmux -L uam list-sessions -F '#{session_name}|#{session_created}|#{session_attached}|#{pane_pid}|#{pane_current_path}|#{pane_current_command}'` |
| Peek | `tmux -L uam capture-pane -p -t <name> -S -200 -J` |
| Reply | `tmux -L uam send-keys -t <name> -l -- "<text>"` then `... send-keys -t <name> Enter` |
| Attach | exec `tmux -L uam attach -t <name>` |
| Stop | `tmux -L uam kill-session -t <name>` |
| Exists | `tmux -L uam has-session -t <name>` |
| Pid alive | `tmux -L uam display -p -t <name> '#{pane_pid}'` then `kill -0 <pid>` |

## Easy Mode Wizard (`internal/app/app.go`)

Triggered by `uam new` (no args) from the shell, or pressing `e` from the TUI's table view. A four-step Bubble Tea sub-model:

1. **Pick provider** — list of enabled adapters (capability-probed at startup). Disabled providers are greyed out with the reason ("not on PATH"). Default selection is `default_agent` from `sessions.json`.
2. **Optional command alias** — blank uses the provider's default command; a value like `ghcp` is persisted as `command_alias` and reused on resume.
3. **Pick workdir** — text input prefilled with the current `cwd`; `Tab` for filesystem completion; warn if the path is not a git repo (means no checkpoint will be created).
4. **Enter prompt** — multiline input; `Ctrl+G` opens `$EDITOR` for a longer prompt; `Enter` sends, `Esc` cancels back to step 3.

On submit: call the chosen adapter's `Dispatch`; after tmux session creation succeeds, write the session record to `sessions.json` (yolo mode). The new row appears immediately in the TUI.

Power users skip the wizard with `uam dispatch <agent> "<prompt>"`, `uam dispatch --alias ghcp copilot "<prompt>"`, or by typing `@<agent>[:alias] <prompt>` into the TUI's dispatch input directly.

## Session Resume (`internal/app` + adapters)

Adopts ctm's resume semantics. Three entry points, all backed by the same logic:

1. `uam attach <name-or-id>` — look up the session in `sessions.json`, then:
   - If `tmux -L uam has-session -t <tmux_session>` → exec `tmux -L uam attach -t <tmux_session>`.
   - Else fall back to the provider's native resume: e.g. `claude --resume <id> || claude` for Claude, `codex resume <id> || codex` for Codex. The fallback runs inside a fresh tmux session so the row stays visible in `uam`.
2. `uam last` — attach to the most recently active session (max `last_seen_at`).
3. Inside the TUI: `Enter` on a row does the same thing.

If the tmux session is dead AND the provider can't resume, the row is marked `Failed` with detail "session lost", and `Ctrl+X` cleans it up.

## Shell Subcommands

All subcommands share the TUI's code paths via `internal/app`:

| Command | Effect |
|---|---|
| `uam` | Open the TUI (default) |
| `uam new` | Run the easy-mode wizard |
| `uam dispatch <agent> "<prompt>"` | Headless dispatch; prints session id |
| `uam dispatch --safe <agent> "<prompt>"` | Skip the provider's yolo flag |
| `uam dispatch --alias ghcp copilot "<prompt>"` | Dispatch Copilot using command alias `ghcp` |
| `uam attach <name-or-id>` | Resume session per the flow above |
| `uam last` | Attach to most recent session |
| `uam ls` | List sessions (one row per session, machine-readable with `--json`) |
| `uam peek <id>` | Print last 200 lines of pane to stdout |
| `uam stop <id>` | Kill the tmux session (record kept) |
| `uam rm <id>` | Kill + remove from `sessions.json` |

## TUI Layout

```
┌──────────────────────────────────────────────────────────────────────┐
│ unified-agent-manager   14 sessions   Active 12   Closed 2           │
├──────────────────────────────────────────────────────────────────────┤
│ ACTIVE (12)                                                          │
│  ● ◐ codex   refactor auth     /home/user/repo       2m   ◐ open PR  │
│  ●   claude  fix flaky test    /home/user/repo       15s             │
│                                                                      │
│ CLOSED (2) ...                                                       │
├──────────────────────────────────────────────────────────────────────┤
│ > _                                    [Enter] dispatch  [?] help    │
└──────────────────────────────────────────────────────────────────────┘
```

Rows are grouped by the persisted closed flag: Active means not `store.StatusClosedByUser`, Closed means user-retired. Row glyphs reflect process liveness (`●` alive, `◯` exited) and closed status. PR dot: yellow=open, green=merged, purple=draft, grey=closed/none.

### Keybindings (`internal/app/app.go`)

| Key | Action |
|---|---|
| `↑` / `↓` | move selection |
| `Enter` (row) | attach |
| `→` | attach |
| `←` (empty dispatch) | unfocus / back from peek |
| `Space` | toggle peek panel |
| `Enter` (dispatch) | dispatch — agent chosen via `@claude`/`@codex` prefix or default |
| `Esc` | close peek / clear dispatch |
| `Ctrl+T` | pin |
| `Ctrl+R` | rename (inline) |
| `Ctrl+X` | stop+delete (confirm overlay) |
| `Ctrl+S` | toggle group-by-directory |
| `Shift+↑/↓` | reorder (writes `SortIndex` to store) |
| `Tab` | cycle default agent for dispatch |
| `?` | help overlay |
| `q` (empty dispatch) | quit |
| `e` | open easy-mode wizard (`uam new` equivalent) |

## State Persistence (`internal/store/`)

Path: `${XDG_CONFIG_HOME:-~/.config}/uam/sessions.json`. Pattern adopted directly from [`ctm`](https://github.com/RandomCodeSpace/ctm)'s `~/.config/ctm/sessions.json`:

- **Atomic writes:** write to `sessions.json.tmp.<pid>`, fsync, then `rename(2)` over `sessions.json`.
- **Flock-based locking:** `LOCK_EX` on `sessions.json.lock` across the read-modify-write cycle so concurrent `uam` invocations don't clobber each other (the TUI and `uam dispatch` can run side by side).
- **Schema versioning:** top-level `schema_version` int. On load, if the value is older than the binary's expected version, `migrate.go` upgrades in place after writing `sessions.json.bak.<unix-nano>` next to the file.
- **Self-healing decode:** if strict JSON decode fails, move the file aside to `.bak.<unix-nano>` and start fresh — better than refusing to launch.
- **Debounced flush:** 500ms after the last mutation.

```json
{
  "schema_version": 2,
  "default_agent": "claude",
  "sessions": {
    "claude:3d99e759": {
      "id": "3d99e759-...",
      "agent": "claude",
      "command_alias": "claude-fast",
      "name": "fix flaky test",
      "mode": "yolo",
      "workdir": "/home/user/repo",
      "tmux_session": "uam-claude-3d99e759",
      "created_at": "...",
      "last_seen_at": "...",
      "pinned": true,
      "group": "repo",
      "sort_index": 0,
      "status": "active",
      "pr": { "url": "...", "number": 42, "last_status": "open", "last_checked": "..." }
    }
  },
  "ui": { "group_by_dir": false, "sort": "state", "peek_width": 60 }
}
```

Invariants:
- Composite key `agent:short_id` prevents cross-adapter id collisions.
- `command_alias` is optional and scoped to the launch command; the canonical `agent` field remains the provider key used for grouping, tmux session names, and adapter selection.
- Runtime liveness (`Active`/`Failed`, `Alive`/`Exited`) is never persisted — adapters re-derive it from tmux on every refresh.
- `status` is persisted and drives UI grouping: active records stay in Active, `closed_by_user` records move to Closed.

## Refresh Strategy (`internal/app/app.go`)

Two signal sources merged into one Bubble Tea message stream:

1. **Polling (tmux-backed adapters):** every 2s, list tmux sessions and classify each pane by PID liveness only. Pane capture is not used for lifecycle state.
2. **PR scraping/status:** `TmuxAgent` captures a short pane tail for PR URL discovery with a per-session 60s rescan window; sessions with a known PR URL refresh `gh pr view --json state,isDraft,mergedAt` when `gh` is on PATH.

While a session is peek-focused, its pane capture refreshes at most once per second so the peek panel follows live output without tying lifecycle state to pane text.

## Phased Build Order

| Phase | Scope | Days |
|---|---|---|
| 0 | Skeleton: `go mod init`, Makefile, file logger, empty Bubble Tea app | 0.5 |
| 1 | `internal/store` (ctm-style: atomic write, flock, schema version, migrate, .bak) + `internal/tmux` wrapper on private socket `-L uam` | 1.5 |
| 2 | ClaudeAdapter dispatch (yolo flag) / list / attach / stop; `uam dispatch`, `uam ls`, `uam attach`, `uam last`, `uam rm` subcommands | 2 |
| 3 | TUI MVP: table, dispatch input, attach/stop key handlers; rows grouped Active/Closed | 1 |
| 4 | Peek + Reply for Claude via capture-pane + send-keys; 2s tick scheduler | 1 |
| 5 | Shared liveness-only `ClassifyPane` + Active/Closed grouping | 1.5 |
| 6 | CodexAdapter (`--sandbox danger-full-access`) + `@<agent>` selector | 1.5 |
| 7 | CopilotAdapter + OpenCodeAdapter + capability probe to hide unavailable agents | 2 |
| 8 | Easy-mode wizard (`uam new` + `e` keybinding) | 1 |
| 9 | Liveness/status polish, closed-by-user handling, and resume behavior | 1.5 |
| 10 | PR status dot via `gh` (all adapters) | 1 |
| 11 | Pin / rename / group-by-dir / reorder, persisted to store | 1 |
| 12 | `?` overlay, confirm overlays, prune-old, README screencast | 1 |

Total ≈ 15.5 working days (added store hardening, easy-mode wizard, Copilot + OpenCode adapters).

## Critical Files

- `/home/user/unified-agent-manager/main.go`
- `/home/user/unified-agent-manager/internal/app/app.go`
- `/home/user/unified-agent-manager/internal/adapter/adapter.go`
- `/home/user/unified-agent-manager/internal/adapter/claude/claude.go`
- `/home/user/unified-agent-manager/internal/adapter/codex/codex.go`
- `/home/user/unified-agent-manager/internal/adapter/copilot/copilot.go`
- `/home/user/unified-agent-manager/internal/adapter/hermes/hermes.go`
- `/home/user/unified-agent-manager/internal/adapter/omp/omp.go`
- `/home/user/unified-agent-manager/internal/adapter/opencode/opencode.go`
- `/home/user/unified-agent-manager/internal/adapter/tmux_adapter.go`
- `/home/user/unified-agent-manager/internal/adapter/detect.go`
- `/home/user/unified-agent-manager/internal/tmux/tmux.go`
- `/home/user/unified-agent-manager/internal/store/store.go`

## Reused Patterns / External Reference

- **Claude Code agent view docs** (UX reference): https://code.claude.com/docs/en/agent-view — state icons, grouping, keybindings, supervisor model.
- **ctm** (session-management reference): https://github.com/RandomCodeSpace/ctm — `sessions.json` schema, atomic write + flock + schema_version + `.bak` pattern, tmux-as-state-backbone. We replicate these patterns directly (but NOT ctm's auto-git-checkpoint — we let the provider handle sandboxing).
- **Bubble Tea ecosystem** (`bubbletea`, `bubbles`, `lipgloss`) — table, input, help widgets out of the box.
- **fsnotify** — standard Go file-watch lib, used by many tools that watch config dirs.
- **`tea.ExecProcess`** — Bubble Tea's idiomatic way to hand the terminal to a child process and resume the TUI on exit (used for both `claude --resume` and `tmux attach`).

## End-to-End Verification

Per-phase manual tests (Phase N is "done" only when its test passes end to end):

| Phase | Manual test |
|---|---|
| 1 | `uam` → type "list files" Enter → `tmux -L uam ls` shows `uam-claude-<id>` → row appears in TUI → `Enter` attaches into the live Claude session → detach (Ctrl-b d) → back in TUI → `Ctrl+X` removes row → `tmux -L uam ls` no longer shows it |
| 2 | Dispatch a prompt that asks a question → `Space` opens peek showing the captured pane tail → type answer + Enter → peek refreshes with Claude's next message within 2s |
| 3 | Dispatch jobs and stop one through UAM — live records stay under Active, user-retired records move under Closed |
| 4 | `tmux -L uam ls` lists both `uam-claude-<id>` and `uam-codex-<id>` sessions; external `tmux -L uam kill-session` on either makes the row disappear within one tick |
| 5 | Copilot/OpenCode agents appear in dispatch selector only when their CLIs are on PATH; dispatching to each spawns `uam-copilot-<id>` / `uam-opencode-<id>` and round-trips dispatch/peek/reply/attach/stop |
| 5 | Kill a managed pane externally → liveness becomes Exited/Failed without relying on pane text; attach can resume active records |
| 6 | Have an agent open a PR → dot appears yellow; close PR via `gh` → dot turns grey within 60s |
| 7 | Pin a session; restart `uam` → pin survives. Rename a session → tmux name unchanged, display name updates. Toggle group-by-dir → rows regroup by `cwd` basename |
| 8 | `uam --help`, `uam ls`, `uam dispatch claude "hi"`, `uam stop <id>` all behave like their TUI equivalents |
| 9 | `uam dispatch --alias ghcp copilot "hi"`, `uam new` with alias `ghcp`, and TUI `@copilot:ghcp hi` all persist `command_alias`; killing the tmux session and attaching resumes with the same alias |

Automated tests focus on these pure layers:
- `internal/store` — JSON round-trip
- `internal/tmux/parse.go` — parse `list-sessions -F` output
- `internal/adapter/detect.go` — liveness-only classification and PR URL extraction
- `internal/adapter/tmux_adapter.go` — shared tmux adapter behavior
- command alias coverage — CLI flag parsing, `uam new` alias step, TUI `@agent:alias` parsing, store JSON round-trip/merge, resume reuse, PATH executable preference, shell fallback, unsafe alias rejection

## Risks / Caveats

- **Limited lifecycle signal.** Liveness-only classification avoids fragile pane scraping, but it cannot distinguish working, idle, or waiting-for-input states. Peek remains the source of detail.
- **No supervisor for managed providers.** A machine reboot or tmux server kill drops every session. UAM resumes through provider-specific commands when available, but tmux remains the runtime backbone.
- **Permission modes.** Auto-accepting permissions for unattended sessions is intentionally NOT plumbed in MVP. Both CLIs handle this themselves; we don't override.
- **TTY sizing.** `-x 200 -y 50` is a deterministic but fixed size. Very wide TUIs can wrap differently in peek output, but lifecycle state no longer depends on parsing that output.
