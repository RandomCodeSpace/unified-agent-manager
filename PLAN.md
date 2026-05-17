# unified-agent-manager — Implementation Plan

## Context

The repository `/home/user/unified-agent-manager` is currently empty (no commits, no files). The goal is to build a **terminal UI that replicates Claude Code's "agent view" (`claude agents`) experience but works across multiple coding-agent CLIs in one unified dashboard** — Claude Code, OpenAI Codex, GitHub Copilot CLI, and OpenCode.

Why: Claude Code has a great background-session dashboard (see https://code.claude.com/docs/en/agent-view), but it only manages Claude sessions. Developers who use Codex, Copilot, or OpenCode alongside Claude have to context-switch between tools. A unified TUI lets you dispatch, peek, attach, and stop sessions across all of them from one screen, with the same UX patterns: rows grouped by state (Needs input / Working / Completed / Ready for review), Space to peek, Enter to attach, Ctrl+X to stop, PR status dots, pin/rename/group, etc.

Intended outcome: a single-binary CLI called `uam` that opens an agent-view-style TUI showing every Claude and Codex session on the machine, with feature parity for the core operations the reference UX provides.

## Key Decisions (confirmed with user)

- **Stack:** Go + Bubble Tea (single static binary; matches lazygit/gh dash distribution model; Bubble Tea's Elm-style model maps cleanly to the event-driven nature of the dashboard).
- **Uniform tmux backend for every agent.** Codex/Copilot/OpenCode have no supervisor; the user chose uniformity over Claude's first-class supervisor advantage. Every session — Claude, Codex, Copilot, OpenCode — runs interactively inside its own tmux session on private socket `-L uam`, named `uam-<agent>-<id>`. State for all of them is derived by capturing pane content. We give up Claude's `state.json` but get one mental model, one code path for attach/peek/reply/stop, and one set of bugs.
- **Adapter abstraction:** All backends sit behind a common `AgentAdapter` interface. Adding a new CLI later (Aider, etc.) is a matter of dropping in a new package under `internal/adapter/<name>/` with its own `detect.go` patterns.
- **Supported agents at launch:** Claude Code (`claude`), OpenAI Codex (`codex`), GitHub Copilot CLI (`gh copilot` / `copilot`), OpenCode (`opencode`). Each is one adapter package.
- **Session storage** modeled on [`RandomCodeSpace/ctm`](https://github.com/RandomCodeSpace/ctm)'s `~/.config/ctm/sessions.json` pattern: a single JSON file at `~/.config/uam/sessions.json` with atomic write (temp + rename), flock-based locking, schema versioning with auto-migration, and `.bak.<unix-nano>` backups on schema upgrade. Each record holds `id` (uuid), `name`, `agent`, `mode` (always `yolo` for now), `workdir`, `tmux_session`, `created_at`, `last_seen_at`, `pinned`, `group`, `sort_index`, plus cached PR info. Live state (Working/NeedsInput/...) is NEVER persisted — adapters re-derive it from tmux every refresh.
- **Always yolo mode.** Every session is launched with the provider's "full access / skip permissions" flag — `claude --dangerously-skip-permissions`, `codex --sandbox danger-full-access`, Copilot agent mode with auto-approval, `opencode` equivalent. We rely entirely on each provider's own safety mechanisms (Codex's sandbox, Claude's own guardrails, etc.); UAM does NOT layer its own git-checkpoint commits on top. If a provider offers a checkpoint feature natively, it's used as-is.
- **Easy mode.** A guided wizard (`uam new` with no args, or `e` keybinding inside the TUI) walks the user through: 1) pick provider, 2) confirm or change workdir, 3) enter prompt. Each step uses Bubble Tea's list/input components. The wizard skips disabled providers (capability probe failed at startup). Power users can still bypass with `uam dispatch <agent> "<prompt>"` or by typing `@<agent> <prompt>` directly into the TUI's dispatch input.
- **Build order:** Full phased plan (Phase 0 → Phase 8), ~10 working days of solo dev.

## Repository Layout

```
unified-agent-manager/
├── README.md
├── go.mod / go.sum
├── Makefile                       # build, lint, test, install
├── .golangci.yml
├── cmd/uam/main.go                # entrypoint
├── internal/
│   ├── app/
│   │   ├── app.go                 # top-level Bubble Tea Model
│   │   ├── messages.go            # tea.Msg types
│   │   └── keymap.go              # central key bindings
│   ├── ui/
│   │   ├── table.go               # grouped sessions table
│   │   ├── dispatch.go            # bottom prompt input
│   │   ├── peek.go                # peek/reply side panel
│   │   ├── help.go                # ? overlay
│   │   ├── styles.go              # lipgloss colors, state icons
│   │   └── format.go              # time, truncation, PR dot
│   ├── adapter/
│   │   ├── adapter.go             # AgentAdapter interface + Session/State types
│   │   ├── registry.go            # name → adapter resolution
│   │   ├── claude/{claude.go, detect.go, patterns.go, pr.go}
│   │   ├── codex/{codex.go, detect.go, patterns.go}
│   │   ├── copilot/{copilot.go, detect.go, patterns.go}
│   │   └── opencode/{opencode.go, detect.go, patterns.go}
│   ├── tmux/{tmux.go, parse.go}   # ONLY place that shells out to tmux
│   ├── store/{store.go, schema.go, migrate.go, flock.go}
│   ├── wizard/easy.go             # guided "uam new" provider→workdir→prompt flow
│   ├── pr/gh.go                   # optional `gh pr view` for PR dot
│   ├── refresh/scheduler.go       # tick + fsnotify + per-session backoff
│   └── log/log.go                 # file logger at ~/.cache/uam/uam.log
└── test/                          # adapter/tmux/store tests
```

## Core Types (`internal/adapter/adapter.go`)

```
State:        NeedsInput | Working | Completed | ReadyForReview | Failed | Idle
ProcLiveness: Alive | Exited
PRStatus:     None | Open | Merged | Closed | Draft

Session struct {
  ID, AgentType, DisplayName, Cwd string
  State State
  ProcAlive ProcLiveness
  Activity string         // one-line row summary
  LastChange, CreatedAt time.Time
  PR *PRRef
  Pinned bool
  Group string
  SortIndex int
}

PeekResult struct {
  TailText, Summary string
  AwaitingInput bool
}
```

## `AgentAdapter` Interface

```
Name() string
Dispatch(ctx, prompt, cwd) (Session, error)
List(ctx) ([]Session, error)
Peek(ctx, id) (PeekResult, error)
Reply(ctx, id, text) error
Attach(id) (AttachSpec, error)        // app suspends TUI and execs argv
Stop(ctx, id) error
Rename(ctx, id, newName) error
Subscribe(ctx) (<-chan SessionEvent, error)   // nil channel = poll-only
```

Adapters are stateless about live state — they always re-read truth from tmux or Claude's job store. Our own metadata (pin, rename, group, SortIndex, PR cache) is overlaid by `store` after each adapter call.

## Yolo Mode (no UAM-managed checkpoints)

UAM launches every session with the provider's "full access / skip permissions" flag and stops there. It does NOT make its own git commits, stash changes, or otherwise mutate the workdir before dispatch — that's the provider's responsibility. If a provider has its own checkpoint/rollback feature (e.g. some sandboxed CLIs snapshot state internally), UAM doesn't interfere with it.

| Agent | Yolo invocation |
|---|---|
| Claude | `claude --dangerously-skip-permissions` |
| Codex | `codex --sandbox danger-full-access` |
| Copilot | `copilot --allow-all-tools` (or the current flag — probed at startup) |
| OpenCode | `opencode --auto-approve` (or current equivalent) |

If the user wants safe mode for one dispatch, `uam dispatch --safe <agent> "<prompt>"` drops the yolo flag and lets the provider run with its default prompts.

## ClaudeAdapter (`internal/adapter/claude/`)

Runs `claude` interactively inside its own tmux session in yolo mode. Same mechanics as the other adapters; only the inner CLI and its pane-scrape patterns differ.

- **Dispatch:**
  ```
  tmux -L uam new-session -d -s uam-claude-<id> -c <cwd> -x 200 -y 50 \
    -e UAM_AGENT=claude -e UAM_ID=<id> 'claude --dangerously-skip-permissions; exec bash'
  tmux -L uam send-keys -t uam-claude-<id> -l -- "<prompt>"
  tmux -L uam send-keys -t uam-claude-<id> Enter
  ```
- **List:** `tmux -L uam list-sessions -F ...`, filter `uam-claude-` prefix.
- **Peek:** `tmux -L uam capture-pane -p -t <name> -S -200 -J`, then `detect.Classify(lines)` (Claude-specific patterns).
- **Reply:** `send-keys -l -- "<text>"` then `send-keys Enter`. For multiple-choice prompts where Claude shows numbered options, allow sending a single digit + Enter.
- **Attach:** `AttachSpec{argv: ["tmux", "-L", "uam", "attach", "-t", "uam-claude-<id>"]}` via `tea.ExecProcess`.
- **Stop:** `tmux -L uam kill-session -t uam-claude-<id>`.
- **State detection** (`claude/detect.go`) — same `Classify(lines) -> (State, ProcAlive, summary)` shape as Codex, but tuned to Claude's TUI:
  1. Pane process is no longer `claude` (it's `bash`) → `Completed` + `Exited`.
  2. Trailing lines show Claude's permission/tool-approval prompt or `Do you want to`/numbered-choice block → `NeedsInput`.
  3. Trailing line has Claude's spinner glyphs (`✻`/`✽` animated frames) or a tool-running indicator → `Working`.
  4. Trailing line is Claude's idle prompt (`>`, `Try`, etc.) with no spinner → `Completed`.
  5. Error markers (`Error:`, red banner text) without a prompt → `Failed`.
  6. Pane content hash unchanged for >15s when last classification was `Working` → demote to `Completed`.
- **Patterns** (`claude/patterns.go`): same versioned struct as Codex but populated with Claude-specific glyphs/regex. The user can override via config without recompiling.
- **PR detection:** regex `https://github.com/.../pull/\d+` against last ~500 captured lines; first match wins, cached in store, refreshed via `internal/pr` at most every 60s.

## CopilotAdapter (`internal/adapter/copilot/`)

Runs GitHub Copilot CLI's interactive/agent mode inside tmux. Command is whichever of `gh copilot` or `copilot` is on PATH (probed at startup).

- **Dispatch:**
  ```
  tmux -L uam new-session -d -s uam-copilot-<id> -c <cwd> -x 200 -y 50 \
    -e UAM_AGENT=copilot -e UAM_ID=<id> '<copilot-cmd> --allow-all-tools; exec bash'
  tmux -L uam send-keys -t uam-copilot-<id> -l -- "<prompt>"
  tmux -L uam send-keys -t uam-copilot-<id> Enter
  ```
- **List/Peek/Reply/Attach/Stop:** identical shape to ClaudeAdapter, swap session prefix to `uam-copilot-`.
- **State detection** (`copilot/detect.go`): Copilot CLI shows a distinct prompt indicator and offers Run/Explain/Revise choices when it suggests a command — `NeedsInput` fires on those choice prompts. Spinner detection from the `Thinking...` / `Generating...` strings. Idle state when the trailing line is the prompt sigil with no spinner.
- **PR detection:** same regex as the others.
- **Capability probe:** if neither `gh copilot` nor `copilot` resolves on PATH, the adapter is disabled at startup and its agent option is removed from the dispatch selector.

## OpenCodeAdapter (`internal/adapter/opencode/`)

Runs `opencode` interactively inside tmux. Same shape as the others.

- **Dispatch:**
  ```
  tmux -L uam new-session -d -s uam-opencode-<id> -c <cwd> -x 200 -y 50 \
    -e UAM_AGENT=opencode -e UAM_ID=<id> 'opencode --auto-approve; exec bash'
  tmux -L uam send-keys -t uam-opencode-<id> -l -- "<prompt>"
  tmux -L uam send-keys -t uam-opencode-<id> Enter
  ```
- **List/Peek/Reply/Attach/Stop:** identical shape, prefix `uam-opencode-`.
- **State detection** (`opencode/detect.go`): OpenCode's TUI uses its own spinner and prompt patterns; capture last 200 lines and classify via the same `Classify(lines) -> (State, ProcAlive, summary)` shape. `NeedsInput` triggers on tool-approval / file-edit confirmation prompts; `Failed` on red error banners.
- **PR detection:** same regex.
- **Capability probe:** if `opencode` is not on PATH, the adapter is disabled and removed from the dispatch selector.

### What we give up by not using `claude --bg`

- No structured `state.json` — we screen-scrape instead.
- No first-class "Ready for review" surfaced by Claude's daemon — we infer it from PR-URL presence + PR status, same as Codex.
- No automatic daemon-managed lifecycle (idle timeout, auto-respawn) — tmux sessions live until killed.

These are acceptable for uniformity. If we later add a `--claude-native` flag, it can opt back into the supervisor backend behind the same `AgentAdapter` interface.

## CodexAdapter (`internal/adapter/codex/`)

- **Dispatch:**
  ```
  tmux -L uam new-session -d -s uam-codex-<id> -c <cwd> -x 200 -y 50 \
    -e UAM_AGENT=codex -e UAM_ID=<id> 'codex --sandbox danger-full-access; exec bash'
  tmux -L uam send-keys -t uam-codex-<id> -l -- "<prompt>"
  tmux -L uam send-keys -t uam-codex-<id> Enter
  ```
  `-l` (literal) is critical so shell metacharacters aren't expanded. Fixed `-x 200 -y 50` so capture is deterministic regardless of who attaches.
- **List:** `tmux -L uam list-sessions -F '#{session_name}|#{session_created}|...'`, filter `uam-codex-` prefix.
- **Peek:** `tmux -L uam capture-pane -p -t <name> -S -200 -J`.
- **Reply:** same `send-keys -l` + `Enter` pattern.
- **Attach:** `AttachSpec{argv: ["tmux", "-L", "uam", "attach", "-t", "uam-codex-<id>"]}` via `tea.ExecProcess`.
- **Stop:** `tmux -L uam kill-session -t uam-codex-<id>`.
- **State detection** (`detect.go`) — pure function `Classify(lines []string) (State, ProcAlive, summary)`:
  1. Pane process no longer `codex` → `Completed` + `Exited`.
  2. Trailing lines match known Codex approval/continue prompt regex → `NeedsInput`.
  3. Trailing line has spinner glyph (`⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏` etc.) or known status string → `Working`.
  4. Trailing line is the Codex idle prompt sigil (`>`, `❯`) → `Completed`.
  5. Error markers without a prompt → `Failed`.
  6. Else: `Working` if pane content hash changed within 10s, else `Completed`.
  Summary = longest non-blank line in last 20 lines that isn't pure whitespace/spinner.
- **Patterns** live in `patterns.go` as a versioned struct (`Spinners`, `PromptSigils`, `NeedsInputRegex`, ...) so we can tune without code churn.
- **Optional v2:** `--classifier=llm` flag pipes last 50 lines to `claude -p` for `{state, summary}` JSON. Off by default.

## tmux Wrapper (`internal/tmux/tmux.go`)

All commands use private socket `-L uam` to isolate from the user's own tmux server. This is the ONLY place in the codebase that shells out to `tmux`.

| Op | Command |
|---|---|
| Create | `tmux -L uam new-session -d -s <name> -c <cwd> -x 200 -y 50 -e UAM_ID=<id> '<cmd>; exec bash'` |
| List | `tmux -L uam list-sessions -F '#{session_name}|#{session_created}|#{session_attached}|#{pane_pid}|#{pane_current_path}|#{pane_current_command}'` |
| Peek | `tmux -L uam capture-pane -p -t <name> -S -200 -J` |
| Reply | `tmux -L uam send-keys -t <name> -l -- "<text>"` then `... send-keys -t <name> Enter` |
| Attach | exec `tmux -L uam attach -t <name>` |
| Stop | `tmux -L uam kill-session -t <name>` |
| Exists | `tmux -L uam has-session -t <name>` |
| Pid alive | `tmux -L uam display -p -t <name> '#{pane_pid}'` then `kill -0 <pid>` |

## Easy Mode Wizard (`internal/wizard/easy.go`)

Triggered by `uam new` (no args) from the shell, or pressing `e` from the TUI's table view. A three-step Bubble Tea sub-model:

1. **Pick provider** — list of enabled adapters (capability-probed at startup). Disabled providers are greyed out with the reason ("not on PATH"). Default selection is `default_agent` from `sessions.json`.
2. **Pick workdir** — text input prefilled with the current `cwd`; `Tab` for filesystem completion; warn if the path is not a git repo (means no checkpoint will be created).
3. **Enter prompt** — multiline input; `Ctrl+G` opens `$EDITOR` for a longer prompt; `Enter` sends, `Esc` cancels back to step 2.

On submit: write the session record to `sessions.json` (yolo mode), create the git checkpoint, then call the chosen adapter's `Dispatch`. The new row appears immediately in the TUI.

Power users skip the wizard with `uam dispatch <agent> "<prompt>"` or by typing `@<agent> <prompt>` into the TUI's dispatch input directly.

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
| `uam attach <name-or-id>` | Resume session per the flow above |
| `uam last` | Attach to most recent session |
| `uam ls` | List sessions (one row per session, machine-readable with `--json`) |
| `uam peek <id>` | Print last 200 lines of pane to stdout |
| `uam stop <id>` | Kill the tmux session (record kept) |
| `uam rm <id>` | Kill + remove from `sessions.json` |

## TUI Layout

```
┌──────────────────────────────────────────────────────────────────────┐
│ unified-agent-manager   14 sessions   ●5 working  ●2 need input      │
├──────────────────────────────────────────────────────────────────────┤
│ NEEDS INPUT (2)                                                      │
│  ● ◐ codex   refactor auth     approving git diff…   2m   ◐ open PR  │
│  ● ◐ claude  fix flaky test    waiting for permission 15s            │
│                                                                      │
│ WORKING (5) ...                                                      │
│ COMPLETED (5) ...                                                    │
│ READY FOR REVIEW (2) ...                                             │
├──────────────────────────────────────────────────────────────────────┤
│ > _                                    [Enter] dispatch  [?] help    │
└──────────────────────────────────────────────────────────────────────┘
```

Icon: shape = process liveness (`●` alive, `◯` exited); color = state (yellow=needs input, blue=working, green=completed, purple=review, red=failed, grey=idle). PR dot: yellow=open, green=merged, purple=draft, grey=closed/none.

### Keybindings (`internal/app/keymap.go`)

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
  "schema_version": 1,
  "default_agent": "claude",
  "sessions": {
    "claude:3d99e759": {
      "id": "3d99e759-...",
      "agent": "claude",
      "name": "fix flaky test",
      "mode": "yolo",
      "workdir": "/home/user/repo",
      "tmux_session": "uam-claude-3d99e759",
      "created_at": "...",
      "last_seen_at": "...",
      "pinned": true,
      "group": "repo",
      "sort_index": 0,
      "pr": { "url": "...", "number": 42, "last_status": "open", "last_checked": "..." }
    }
  },
  "ui": { "group_by_dir": false, "sort": "state", "peek_width": 60 }
}
```

Invariants:
- Composite key `agent:short_id` prevents cross-adapter id collisions.
- Live state (Working/NeedsInput/Completed/Failed) is NEVER persisted — adapters re-derive it from tmux on every refresh.
- Startup: prune entries whose tmux session no longer exists AND `last_seen_at` is older than 7 days.

## Refresh Strategy (`internal/refresh/scheduler.go`)

Two signal sources merged into one Bubble Tea message stream:

1. **Polling (tmux, both adapters):** every 2s, `tmux -L uam list-sessions` for appear/disappear of any `uam-*` session; for each visible session, `Peek` (capture-pane + classify) at most every 5s (per-session leaky bucket).
2. **Slow tick (PR status):** every 60s per session with a known PR URL, fetch `gh pr view --json state,isDraft,mergedAt` (only if `gh` is on PATH).

Row-summary display cap: at most one update every 15s per row (matches Claude's documented cadence). Internal classification can update faster than the displayed text. While a session is peek-focused, its poll interval drops to 1s.

## Phased Build Order

| Phase | Scope | Days |
|---|---|---|
| 0 | Skeleton: `go mod init`, Makefile, file logger, empty Bubble Tea app | 0.5 |
| 1 | `internal/store` (ctm-style: atomic write, flock, schema version, migrate, .bak) + `internal/tmux` wrapper on private socket `-L uam` | 1.5 |
| 2 | ClaudeAdapter dispatch (yolo flag) / list / attach / stop; `uam dispatch`, `uam ls`, `uam attach`, `uam last`, `uam rm` subcommands | 2 |
| 3 | TUI MVP: table, dispatch input, attach/stop key handlers; row colors by state (still flat list) | 1 |
| 4 | Peek + Reply for Claude via capture-pane + send-keys; 2s tick scheduler | 1 |
| 5 | Claude `detect.go` patterns + state grouping (Needs input / Working / Completed / Review) | 1.5 |
| 6 | CodexAdapter (same shape, codex-specific patterns, `--sandbox danger-full-access`) + `@<agent>` selector | 1.5 |
| 7 | CopilotAdapter + OpenCodeAdapter (same shape, their own `detect.go`, yolo flags, capability probe to hide unavailable agents) | 2 |
| 8 | Easy-mode wizard (`uam new` + `e` keybinding) | 1 |
| 9 | State-detection polish across all four: pane-hash demotion, `Failed` markers, optional `--classifier=llm` | 1.5 |
| 10 | PR status dot via `gh` (all adapters) | 1 |
| 11 | Pin / rename / group-by-dir / reorder, persisted to store | 1 |
| 12 | `?` overlay, confirm overlays, prune-old, README screencast | 1 |

Total ≈ 15.5 working days (added store hardening, easy-mode wizard, Copilot + OpenCode adapters).

## Critical Files

- `/home/user/unified-agent-manager/cmd/uam/main.go`
- `/home/user/unified-agent-manager/internal/app/app.go`
- `/home/user/unified-agent-manager/internal/adapter/adapter.go`
- `/home/user/unified-agent-manager/internal/adapter/claude/claude.go`
- `/home/user/unified-agent-manager/internal/adapter/claude/detect.go`
- `/home/user/unified-agent-manager/internal/adapter/codex/codex.go`
- `/home/user/unified-agent-manager/internal/adapter/codex/detect.go`
- `/home/user/unified-agent-manager/internal/adapter/copilot/copilot.go`
- `/home/user/unified-agent-manager/internal/adapter/copilot/detect.go`
- `/home/user/unified-agent-manager/internal/adapter/opencode/opencode.go`
- `/home/user/unified-agent-manager/internal/adapter/opencode/detect.go`
- `/home/user/unified-agent-manager/internal/tmux/tmux.go`
- `/home/user/unified-agent-manager/internal/store/store.go`
- `/home/user/unified-agent-manager/internal/store/migrate.go`
- `/home/user/unified-agent-manager/internal/wizard/easy.go`
- `/home/user/unified-agent-manager/internal/refresh/scheduler.go`

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
| 3 | Dispatch three jobs in different states (one mid-tool, one awaiting permission, one idle) — rows grouped under right headers with correct colors; timestamps update at most every 15s |
| 4 | `tmux -L uam ls` lists both `uam-claude-<id>` and `uam-codex-<id>` sessions; external `tmux -L uam kill-session` on either makes the row disappear within one tick |
| 5 | Copilot/OpenCode agents appear in dispatch selector only when their CLIs are on PATH; dispatching to each spawns `uam-copilot-<id>` / `uam-opencode-<id>` and round-trips dispatch/peek/reply/attach/stop |
| 5 | Leave a Codex session at idle prompt → row turns green within 15s; trigger spinner → row turns blue within 2s; cause an error → row turns red |
| 6 | Have an agent open a PR → dot appears yellow; close PR via `gh` → dot turns grey within 60s |
| 7 | Pin a session; restart `uam` → pin survives. Rename a session → tmux name unchanged, display name updates. Toggle group-by-dir → rows regroup by `cwd` basename |
| 8 | `uam --help`, `uam ls`, `uam dispatch claude "hi"`, `uam stop <id>` all behave like their TUI equivalents |

Automated tests focus on these pure layers:
- `internal/store` — JSON round-trip
- `internal/tmux/parse.go` — parse `list-sessions -F` output
- `internal/adapter/claude/detect.go` — classification against captured Claude pane fixtures
- `internal/adapter/codex/detect.go` — classification against captured Codex pane fixtures
- `internal/adapter/copilot/detect.go` — classification against captured Copilot pane fixtures
- `internal/adapter/opencode/detect.go` — classification against captured OpenCode pane fixtures

## Risks / Caveats

- **Pattern fragility.** Both Claude's and Codex's prompt sigils, spinner glyphs, and "needs input" wording are unstable surfaces — they change with CLI updates. `patterns.go` for each adapter MUST be table-tested and easy to override via config without recompiling. Phase 5 budgets a full day for tuning against captured fixtures.
- **No supervisor for either agent.** A machine reboot or tmux server kill drops every session. We do not attempt to restore Claude sessions via `claude --resume <sessionId>` in MVP (Claude no longer manages them for us). A future enhancement could pair tmux session names with Claude session IDs and re-spawn on startup.
- **Permission modes.** Auto-accepting permissions for unattended sessions is intentionally NOT plumbed in MVP. Both CLIs handle this themselves; we don't override.
- **TTY sizing.** `-x 200 -y 50` is a deterministic but fixed size. Very wide TUIs that wrap differently could confuse pattern matching; we capture with `-J` (join wrapped lines) to mitigate, but Phase 5 should validate on multiple terminal widths.
