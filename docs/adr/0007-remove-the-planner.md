# ADR 0007: Remove the planner

- Status: Accepted
- Date: 2026-10-07
- Supersedes: the earlier ADRs 0005 and 0006 (removed; see git history)

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

A settings file with the `planner` switch and Task records with the
planner's `retired` mark still load; both keys drop on the next save.

At startup uam deletes the planner's files beside `sessions.json` in
`~/.config/uam/` (or `$UAM_CONFIG_DIR/`): `board.db` with `board.db-wal` and
`board.db-shm`, and the lane worktrees under `lanes/`. It reads each lane's
repository from the lane's `.git` file and, once the lane is gone, runs
`git worktree prune` there so the repository forgets it. It removes only
regular files and a real `lanes` folder, never through a symlink, and a
failure is logged without stopping startup. This one-time cleanup can be
dropped in a later release.

The branches the planner created in a Project's repository stay: the
integration branch `uam-plan-<id>` and attempt branches
`uam-plan-<id>-<n>-<suffix>`. To remove them, list them, check that nothing
on a branch is still needed, then delete it:

```sh
git branch --list 'uam-plan-*'
git log --oneline <base branch>..<branch>
git branch -D <branch>
```

## Consequences

Planning happens in ordinary Tasks. The planner's data is not kept, and
ADRs 0005 and 0006 remain only in git history.
