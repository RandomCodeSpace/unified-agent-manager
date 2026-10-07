# ADR 0007: Remove the planner

- Status: Accepted
- Date: 2026-10-07
- Supersedes: [ADR 0005](0005-planner.md) and [ADR 0006](0006-planner-execution.md)

## Context

ADR 0005 gave each git Project a board of epics, stories and subtasks, kept
in `board.db`. Agents planned on it through the `board_*` tools, and the owner
confirmed, launched and closed the work on the Planner page. ADR 0006 added
execution: one approval per epic, a lane per subtask in its own git worktree,
landing on a per-Project integration branch, revert, and automatic merges
into the base branch.

The planner grew to about 27,700 lines, more than a quarter of uam's non-test
code. Its proposals, approvals, holds, change requests and acceptance
commands made it hard to use without first learning its rules. An ordinary
Task covers the owner's use: its agent plans with subagents and keeps the
plan in a markdown file.

## Decision

Remove the planner entirely. That covers the Planner page and a Task's Plan
panel, the board API, the agent `board_*` tools, the executor with its lanes,
integration branches, landing, revert and automatic merges, kb import, the
board's Background AI helpers, the Project's acceptance command and the
Settings switch. The built-in `uam` skill no longer mentions it. Copilot's
own plan mode is a separate feature and stays.

## What remains on disk

uam neither deletes nor migrates what the planner left behind.

- `board.db`, with `board.db-wal` and `board.db-shm`, sits beside
  `sessions.json` in `~/.config/uam/` (or `$UAM_CONFIG_DIR/`). uam no longer
  opens it. Delete the three files once the old board is no longer needed.
- Lane worktrees of each Project's repository sit under
  `lanes/<project id>/` in that same folder.
- Each Project's repository may keep branches named `uam-plan-*`: the
  integration branch `uam-plan-<id>` and attempt branches
  `uam-plan-<id>-<n>-<suffix>`.

To clear them, check in the Project's repository that nothing on them is
still needed, then remove the worktrees before the branches (git keeps a
branch a worktree has checked out). `git worktree prune` forgets lane
folders deleted by hand.

```sh
git worktree list
git branch --list 'uam-plan-*'
git log --oneline <base branch>..uam-plan-<id>
git worktree remove <lane path>
git worktree prune
git branch -D <branch>
```

## Consequences

Planning happens in ordinary Tasks. An old `board.db` can still be read with
any SQLite client, and ADRs 0005 and 0006 stay as the planner's record.
