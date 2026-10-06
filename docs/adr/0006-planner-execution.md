# ADR 0006: Planner execution

Status: accepted (2026-10-06). It extends [ADR 0005](0005-planner.md) and supersedes the parts of it listed in §2, each of which ADR 0005 marks with a note.

## Context

Under ADR 0005 every start is manual. The owner confirms cards, launches a subtask or a whole story, and each Task works in the Project directory beside every other Task. On 2026-10-06 the owner asked for this instead:

- **R1. Plan from a Task.** The owner asks an ordinary Task for a plan, and its agent builds the epic, stories, subtasks and links.
- **R2. One approval, at the epic.** No per-card confirmation or launch after it.
- **R3. Block execution, not start it.** The owner can hold back any card under an approved epic.
- **R4. Parallel, with dependencies.** Ready subtasks run side by side, in dependency order.
- **R5. One Task per subtask.** Each Task works on its one subtask only.
- **R6. Revert per subtask.** Any landed subtask can be undone on its own.
- **R7. Full agent CRUD under the dependency rules.** Agents add, edit, move, link, unlink, split and delete cards.

Today's code stands in the way in six places:
- A Task may create 20 cards over its whole lifetime. One epic with four stories of four subtasks is 21.
- Confirm only walks up (decision 2). Nothing confirms a subtree downward.
- Blockers never stop a launch or a claim (decision 6).
- All Tasks share the Project directory, and a done request's commits are `base..HEAD` whoever made them. One subtask's change can't be isolated, so it can't be reverted alone.
- A launched Task is scoped to its subtask's parent, and the done reply tells it to claim the next pending subtask.
- Agents only dismiss proposals. They never cancel a confirmed card.

Three designs were scored: minimal on the shared tree, isolation first, and executor first. This ADR builds on the executor-first design. From isolation first it takes one integration branch per Project, landing that re-tests when the tip moved, the git preflight and the merge into the owner's branch. From the minimal design it takes approval of listed ids, later additions staying proposals, no new request kind, and manual starts outside approved epics left as they are. The scores are under Rejected.

Naming follows ADR 0005: a **Task** is a uam conversation, and the planner's leaf is a **subtask**.

## Decision

## Principles

- The owner approves an epic once. The run record that approval writes, with an explicit model, mode and parallel limit, is the only authorization to run anything under it.
- Outside approved epics nothing changes. Manual Launch, Do whole story, Attach, Claim and Confirm work as ADR 0005 describes.
- uam runs approved work. The executor is a goroutine in the server, not a Task, so ADR 0005's rejection of a steward Task stands.
- One attempt is one Task, one worktree and one subtask. An accepted subtask lands as one commit, and a revert removes exactly that commit and the ones started on top of it.
- Approving an epic also authorizes merging its landed work into the owner's base branch (§5.8). uam merges when an approved epic finishes and after the owner reverts merged work, and it never pushes or force-moves that branch.
- Store first, then the ref. Every write to the integration branch records its intent in board.db, moves the ref with compare-and-swap, then finalizes the store. The ref never moves backwards, and recovery finishes what a crash interrupted.
- Started is still the line (ADR 0005 decision 8). No agent write, and no owner write under an approved epic, may give running work a new open blocker.
- Kept from ADR 0005: derived container status, `expires_at NULL` as confirmed, the layered DAG, the started lock, `held_by` with its single `releaseHold` path, pure reconcile, evidence-backed done with the owner's `accept_cmd`, agents never writing done or blocked, agent reach (decision 9), agent-proposed epics, no card per Task, in-process tools, and an explicit model on every run launch. Changing any of these needs the owner.

## 1. Scope

The owner asks an ordinary Task for a plan and approves the epic once. From there uam runs it. Ready subtasks start in parallel, each in its own Task and its own git worktree, and any landed subtask can be reverted on its own. When an approved epic finishes, uam merges the integration branch into the owner's base branch; the epic approval covers that merge (§5.8).

How each requirement is met:
- **R1.** A Task with no scope already proposes epics and builds them out (ADR 0005 decisions 4 and 9). The real blocker is the cap, which becomes 50 live cards under a lifetime ceiling of 200 (§6.3). `board_create` and SKILL.md add the hand-off: when the plan is complete, ask the owner to approve #E, and end the turn.
- **R2.** A new owner action, **Approve and run**, on an epic (§6.2). It confirms downward exactly the proposals the dialog showed, at the revisions it showed, and writes a `runs` row with the model, mode and parallel limit. Under an approved epic nobody confirms or launches per card, and the store refuses both with `run_owned`.
- **R3.** An owner flag, `paused`, on any card under an approved epic, shown as Pause / Resume with the hint "Block execution" (§4.6). It holds back the card and everything under it. Stop on a running subtask is the owner's Release: it ends the attempt and pauses the card.
- **R4.** A server-side executor (§4) starts ready subtasks up to the epic's parallel limit and a global lane cap. A subtask is ready when it and its ancestors are confirmed, nothing at or above it is paused, it is not flagged blocked, and it has no open blocker, own or inherited.
- **R5.** Every attempt is a new Task in a new worktree, scoped to that subtask only (§5.3). It gets a reduced tool set and cannot claim.
- **R6.** An accepted subtask lands as one squash commit on the Project's integration branch (§5.4). Revert undoes it, together with the landed subtasks that started on top of it, in one compare-and-swap ref update (§5.7). When a revert cannot apply, the owner reopens the subtask without reverting its code.
- **R7.** Agents add, edit, move, link, unlink, split and delete every unstarted card in their reach, approved cards included, under the integrity rules of §6.3.

## 2. What this supersedes in ADR 0005

Each item names the ADR 0005 rule it replaces and the requirement that forces the change. Item 20 is the exception: it replaces a non-goal of this design's first draft, not an ADR 0005 rule. A bare "decision N" in this list is one of ADR 0005's decisions; "§9 decision N" is one of this ADR's.

1. **Decision 6, "Blockers never stop a launch or a claim".** Under an approved epic nothing starts while the subtask has an open blocker (its own or inherited), the blocked flag, or a pause at or above it. Manual Launch (with or without confirm), Do whole story, Attach and Claim there are refused `run_owned` (§10 has the order in which this lands). Manual starts outside approved epics keep decision 6. Forced by R4.
2. **Per-card confirmation as the gate on execution**, as stated in ADR 0005's `expires_at` row (§1), the confirm step on Launch (§5), decision 6's confirm step, decision 10's "confirmation happens only at execution", and Confirm walking only up. Approve confirms the listed subtree downward, at the listed revisions, and authorizes the run. Under an approved epic the store refuses per-card Confirm and Launch with confirm (`run_owned`), and the UI doesn't offer them. Outside approved epics nothing changes. Forced by R2 and R3.
3. **ADR 0005's Rejected item "Per-Task git worktrees: overlap is flagged, not prevented".** Every run attempt gets its own worktree and branch and lands on a per-Project integration branch. In a shared tree a subtask's change can't be isolated, so it can't be reverted alone. Forced by R4 and R6.
4. **ADR 0005's Deferred item "Start next card".** The executor starts ready subtasks itself. "Queueing cards onto a running Task" stays deferred, because one Task runs one subtask. Forced by R3 and R4.
5. **Working-Task scope and "Do whole story"** (ADR 0005 §4 and §5). A run Task is scoped to its one subtask. It gets no `board_claim`, and Attach and Do whole story are refused under approved epics. Forced by R5.
6. **"Agents never set cancelled" and decision 8's dismissal of proposals only.** `board_delete` replaces `board_dismiss`. It is a cascade cancel the owner can Restore, and it reaches confirmed and approved cards in reach, with the refusals of §6.3 rule 3. An agent never deletes an approved epic itself; it files a cancel request. Done and blocked still go through `board_request`. Forced by R7.
7. **Decision 8, "a container with a started subtask stays plannable"**, narrowed.
   - For agents, a write that gives a started subtask (held or done) a new open blocker is refused `in_progress` with refs. That covers moving a confirmed subtask into a done container that a started card waits on, and linking an open card onto a container with started work. Agent creates and splits under a done container make proposals, which derivation ignores, so they reopen nothing and stay allowed. Agents also may not link or unlink where the blocked side is a container with started or done subtasks under it.
   - For the owner, only under approved epics, Approve, Restore, link, move, the blocked flag and status changes are refused `in_progress` with refs when they would give a running lane subtask a new open blocker or the blocked flag. The owner Stops that subtask first, or waits for it to land. Owner writes outside approved epics are unchanged.

   Forced by R4 and R7: a reopened blocker makes the running lane's done claim fail the finishing guard.
8. **The cap of 20 created cards per Task, counted over its lifetime** (ADR 0005 §4 and decision 9). It becomes 50 non-cancelled cards, so deleted and expired cards give their slot back, under a lifetime ceiling of 200 created cards, cancelled ones included. The cap of 10 unconfirmed children per container, the 20-comment cap and the 14-day proposal expiry are unchanged. Forced by R1 (one epic with four stories of four subtasks is 21 cards) and R7 (re-planning after deletes).
9. **Decision 5, automatic acceptance**, for run subtasks.
   - Done means accepted and landed, in one step.
   - The `overlap` flag is not computed in worktrees, since there is nothing to overlap with.
   - `tests_or_build_changed` is recorded and shown but does not hold the request (§9 decision 3).
   - The flags `no_change_in_tree`, `baseline_missing` and `acceptance_could_not_run`, and the waits `command_changed` and `closes_with_proposals`, still hold it for the owner.
   - An acceptance run killed by the timeout is filed as `acceptance_could_not_run` rather than refused as red.

   Forced by R2. Otherwise nearly every test-first subtask would wait for an approval of its own.
10. **Reconcile releases an ended hold to todo and does nothing more** (ADR 0005 §5's release table). For a lane hold, `releaseHold` also sets `paused` by reason. `ended` and `rejected` set `paused='uam'`: an attempt ended without landing. `released` and `settled` set `paused='owner'`: the owner stopped it. `accepted`, `aborted`, `blocked` and `cancelled` set nothing. Forced by R4, so the executor never relaunches in a loop an attempt that failed or that the owner ended.
11. **The owner's "Back to To do" on a done subtask** (decision 5). On a landed subtask it is replaced by Revert, so the code and the card move together, and by "Reopen without reverting code" for when Revert cannot apply. Forced by R6.
12. **uam never commits for a Task.** Today uam commits only the files and message the owner picks. Inside lanes uam now commits what the Task left uncommitted, merges the integration tip into the lane, writes squash and revert commits with `commit-tree`, and moves `uam-plan-*` branches. It touches the owner's base branch only through the merge job of §5.8, which the epic approval authorizes. Forced by R6 and the owner's merge decision of 2026-10-06 (item 20).
13. **Decision 8's "Still allowed: the owner's Release, cancel, done and back to To do"** on started cards, for lane holds. Release becomes Stop: it ends the attempt and pauses the card (`paused='owner'`). Mark done is refused with "accept its done request or Stop it", because it would bypass landing. Back to To do is item 11. Cancel is unchanged. Forced by R4 and R6.
14. **ADR 0005 §6, "one runner per Project, guarded by a mutex", running in the Project directory, and its test 4, "at most one acceptance run per Project".** A lane runs its acceptance command in its own worktree. All runs of a Project, manual and lane, share a per-Project limit `accept_parallel`, default 1, so test 4 still holds by default. Raising the limit supersedes it for that Project. Forced by R4 and R5.
15. **ADR 0005 test 7, "A restart changes no holds of Active or Settled Tasks and writes no comments".** Boot recovery finishes landings and reverts that a crash interrupted, which ends holds and writes comments, and aborts uam's own merges left in lanes. The executor may then nudge and start. Forced by R4 and R6.
16. **ADR 0005 §5's Settle row, "keep held, release to todo, or cancel"**, for lane holds. Settle cannot keep a lane hold. Release stops the attempt (`paused='owner'`) and cancel cancels the card. Forced by R4: a settled holder would keep a slot with nobody working on it.
17. **ADR 0005 §5's "A hold persists while the Task is Active"**, for lane holds. Besides landing and Stop, accepting a blocked request on a lane hold ends the attempt with reason `blocked`. The flag or link it sets holds the subtask back without a pause, so clearing the flag or finishing the blocker restarts it. Forced by R4: the idle holder would otherwise be nudged against the owner's decision and then retired.
18. **Decision 1, split under a story.** The original's links are copied to every part, both ways, so its dependents keep waiting and its parts keep waiting on its blockers. A link to a dependent that has started is not copied, and the reply names it. An agent's split that would close a container is refused, as Delete is (§6.3 rule 3). Forced by R4: the cancelled original releases its dependents at once, and the executor would start them before the split-off work exists.
19. **Decision 2 and ADR 0005 §8, "Restore confirms"**, under an approved epic. Restore there brings every card under the epic back as a proposal with a fresh expiry, so it shows as "N to approve" instead of running at once. Forced by R2.
20. **This design's first-draft non-goal "automatic merge into the owner's branch".** Approving an epic now also authorizes merging into `base_ref` (§5.8). The merge runs on its own when an approved epic finishes and after an owner Revert of merged work; Retry merge stays for blocked merges and early merges. Forced by the owner's decision of 2026-10-06: "Merge also needs to be approval based only. I approve epic for approval."

## 3. Model

### 3.1 Schema

Three append-only migrations. No card status, no request kind and no requests column is added, so neither the cards table with its FTS triggers nor the requests table is ever rebuilt.

**v5, with Approve (S2):**
```sql
ALTER TABLE cards ADD COLUMN paused TEXT NOT NULL DEFAULT '' CHECK (paused IN ('', 'owner', 'uam'));
CREATE TABLE runs (
  epic_id      TEXT PRIMARY KEY,
  provider     TEXT NOT NULL,
  model        TEXT NOT NULL CHECK (model <> ''),
  effort       TEXT NOT NULL DEFAULT '',
  context_size TEXT NOT NULL DEFAULT '',
  mode         TEXT NOT NULL CHECK (mode IN ('safe', 'yolo')),
  parallel     INTEGER NOT NULL CHECK (parallel BETWEEN 1 AND 4),
  approved_at  TEXT NOT NULL
);
```

**v6, with lanes (S4):**
```sql
ALTER TABLE holds ADD COLUMN branch TEXT NOT NULL DEFAULT '';        -- non-empty marks a lane hold; the worktree path derives from it
ALTER TABLE holds ADD COLUMN landed_sha TEXT NOT NULL DEFAULT '';    -- set on the open hold before the ref moves (landing intent), kept once landed
ALTER TABLE holds ADD COLUMN reverted_sha TEXT NOT NULL DEFAULT '';  -- set before the ref moves (revert intent), kept once reverted
ALTER TABLE holds ADD COLUMN waited_on TEXT NOT NULL DEFAULT '[]';   -- JSON ids of the done subtasks this attempt waited on when it started
ALTER TABLE project_settings ADD COLUMN base_ref TEXT NOT NULL DEFAULT '';  -- the owner branch the integration branch follows
ALTER TABLE project_settings ADD COLUMN accept_parallel INTEGER NOT NULL DEFAULT 1 CHECK (accept_parallel BETWEEN 1 AND 4);
```

**v7, with the executor (S5):**
```sql
UPDATE cards SET paused = 'owner' WHERE kind = 'epic' AND paused = '' AND id IN (SELECT epic_id FROM runs);
```
Every epic approved before the executor existed comes back paused, so the deploy that brings the executor starts nothing on its own. Those approvals may be weeks old, in yolo mode, and made before the git preflight existed. Resume on such an epic runs the Approve preflight (§4.6).

**Left out on purpose:**
- No `holds.workdir`. The worktree path derives from the lanes root, the Project ID and the attempt name in `holds.branch` (§5.1).
- Two new release reasons, `aborted` (a start failed after the hold was written) and `blocked` (a blocked request accepted on a lane hold), need no DDL, because `holds.end_reason` has no CHECK. There is no `stopped` reason: Stop is the owner's Release.
- The landing intent lives on the open hold (`landed_sha`), not on the request. A done request's free-form payload gains `"landing": true` when it waits only to land, which boot recovery and the executor read.
- Purge also deletes the `runs` row of a purged epic.

Migrations are one-way, and an older binary refuses a newer schema. board.db is backed up before each deploy that migrates it.

### 3.2 Derived state

Nothing is stored beyond the columns above.

**An epic's run state:**

| State | Rule |
|---|---|
| Not approved | No `runs` row. The manual flow of ADR 0005 |
| Approved, paused | A `runs` row, and `paused` set on the epic |
| Running k of n | A `runs` row, k open lane holds without a pending done, blocked or split request (§4.1), n the parallel limit |
| Waiting for provider | A `runs` row, and the provider's breaker is open (executor memory, §4.5) |
| Idle | A `runs` row, and nothing ready: everything waits, is paused or is done |
| Finished | The epic derives done, and uam merges the integration branch into `base_ref` (§5.8). Settle adds "Run finished on uam-plan-x" |

**A subtask's state under an approved epic:**

| State | Rule |
|---|---|
| Proposal | It or an ancestor is unconfirmed: an addition after approval, or a card restored under the epic. Never starts. Counted as "N to approve" |
| Paused | `paused` set on it or an ancestor. `uam` means an attempt ended without landing |
| Waiting on #x (via #s) | It has open blockers, own or inherited, or the blocked flag |
| Ready | planned or todo, unheld, no pending request, not under a cancelled card, none of the rows above |
| Running | doing, with a lane hold whose `landed_sha` is empty |
| Landing | doing, with a lane hold whose `landed_sha` is set: the intent is stored and the ref update is under way or awaits recovery |
| Landed | done, and its latest hold has `landed_sha` |
| Reverted | todo, `paused='owner'`, and its latest hold has `reverted_sha` |
| Reopened, code kept | not done, `paused='owner'`, and its latest hold has `landed_sha` and no `reverted_sha` |
| Cancelled | as in ADR 0005 |

### 3.3 Card and Project JSON

- `paused` on every card.
- `run` on an epic: `{provider, model, effort, context_size, mode, parallel, approved_at}`.
- `lane` on a subtask, from its latest hold: `{branch, landed_sha, reverted_sha}`.
- `GET /api/board/projects/{id}` adds `integration {branch, base_ref, ahead, behind}` and `accept_parallel` (S4), and `executor {providers: [{provider, detail, until}]}` (S5).

## 4. Executor

### 4.1 Ready set and slots

`Store.RunFacts(ctx)` walks, in one read transaction, the outline of every Project that has a `runs` row. Boards are small, and the global lane cap needs all Projects at once.

For every subtask that is not cancelled, under an epic with a `runs` row:
```
ready(L) := L is a subtask, stored planned|todo, HeldBy == "", PendingRequests == 0
         && L and its ancestors are confirmed
         && no ancestor is cancelled
         && no ancestor-or-self has paused <> ''
         && !L.Blocked
         && waitsOn(L) is empty      (own and inherited blockers; done and cancelled blockers release)
```

**Slots have one definition.** `lanesInUse(epic)` counts the open lane holds under the epic whose card has no pending done, blocked or split request from the holder, the same exemption the one-hold cap uses. The global count is the same query over all Projects. `RunFacts` and `StartRun` both call it, and neither reads Task stages, which the store does not have. A Settled or Archived holder therefore keeps its slot until its hold is released; Settle releases lane holds (§4.6), and the executor retires stragglers (§4.2).

`RunFacts` also returns, per held lane subtask, the holder Task, the kind of any pending done, blocked or split request from it, whether a pending done is marked `landing`, and the open hold's `landed_sha`. Per lane Task it returns the latest hold and its end reason, so a landed Task is told apart from a stopped one. It returns each run's settings, each epic's pause, and the per-epic and global slot counts. `waitsOn` stays bound to the transaction; only the step after it is pure.

### 4.2 The pure step: `board.Next`

`Next` follows the pattern of reconcile's `endedHolds`: facts in, actions out, no I/O.

```go
type Turn int // Working, Waiting (permission or answer), Ended (completed or idle), Failed (turn failed or runtime exited),
              // Interrupted (restart), OwnerCancelled (cancelled with no reason), UAMCancelled (cancelled by uam with a reason)
type TaskFact struct {
	Stage    Stage
	Turn     Turn
	Provider string
	Lane     bool
	Holds    string // card id or ""
	Worked   bool   // the lane has commits or changes beyond its base; filled only for Failed holders
	FailedAt time.Time
}
type Memory struct {
	Now         time.Time
	Nudged      map[string]int       // task -> nudges since boot
	Starting    map[string]bool      // card -> a start is in progress
	Landing     map[string]bool      // card -> a land call is in flight (tool call, owner job or executor)
	LandRetry   map[string]time.Time // request -> next transient retry
	EpicBackoff map[string]time.Time
	Providers   map[string]Breaker   // per provider: open until, consecutive failures, detail
	SeenFailure map[string]time.Time // task -> the failure already counted
}
type Step struct {
	Start                        []Pick
	Nudge, Cancel, Retire, Abort []Act
	Land                         []string
	ProviderFailed               []string
	Merge                        []string // project IDs (§5.8)
}
func Next(f RunFacts, tasks map[string]TaskFact, mem Memory) Step
```

**Starts.** For each epic that is not paused, not backing off, and whose provider's breaker is closed: `free = min(parallel - lanesInUse(epic) - Starting(epic), maxLanes - lanesInUse(all) - Starting(all))`, with `maxLanes = 4`. Start the first `free` ready subtasks, highest priority first, then in outline order. A held subtask with a pending done, blocked or split request frees its slot; its Task is idle and its worktree waits on disk.

**Per lane hold:**

| Holder | Action |
|---|---|
| Open hold has a landing intent, no land call in flight | Land: finish it (§5.4) |
| Pending done marked `landing`, no land call in flight, retry time passed | Land |
| Another pending done, blocked or split from the holder | None: the owner decides. The slot is free |
| Working or Waiting | None |
| OwnerCancelled | None: the owner stepped in. The slot stays taken until the owner sends a message or Stops it |
| Interrupted, not nudged since boot | Nudge "uam restarted while you worked on #N; continue, then file board_request done". At most one nudge per provider per pass |
| Ended or UAMCancelled, not nudged | Nudge "you still hold #N and ended without a done request; finish it and file done, or file blocked with the reason" |
| Ended, already nudged | Retire |
| Failed, nothing in the lane | Abort: the attempt never did any work. Release `aborted` (todo, not paused), discard the Task, remove the lane, report a provider failure |
| Failed, with work in the lane | Report a provider failure once. When the provider's breaker allows, nudge "the provider failed during your turn; continue #N", one probe per provider. A holder whose turn fails after 3 nudges in a row is retired |
| Settled or Archived holder | Retire |

**Lane Tasks that hold nothing:**

| Task | Action |
|---|---|
| Its last hold ended `accepted` (landed) | Working: none, the agent is reading "landed ... end your turn". Idle: retire |
| Its last hold ended otherwise | Working: cancel with the reason "uam: #N was stopped". Idle: retire |
| Settled | Retire |

Retire archives the Task. Archive's reconcile releases any hold left, which for a lane hold is `ended` and pauses `uam` (§2 item 10).

### 4.3 Driver

`internal/web/executor.go` runs a loop shaped like the routine loop: the Manager's wait group, a 30-second ticker, and a kick channel with a buffer of one. Kicks come after every committed board write, when a lane Task's turn leaves Working, after reconcile on Task stage moves, at the end of opening the board, and at the end of every land, revert and merge job.

Each pass:
1. Snapshot the TaskFacts of every lane Task under the Manager lock. A Task is a lane Task when its workdir is under the lanes root. A cancelled turn is OwnerCancelled when its detail is empty and UAMCancelled otherwise, since uam's cancels store their reason as the detail. For Failed holders, `Worked` is computed after the lock is released, from `git status --porcelain` and `rev-list --count <base>..HEAD` in the lane. Also after the lock is released, each Project with a `runs` row gets the merge fact of §5.8: whether `base_ref` already has everything the integration branch carries. The pass never holds the Manager lock across SQL or git (ADR 0005 §12).
2. Call `RunFacts`, then `Next`.
3. Apply the step:
   - **Start** runs in a goroutine with a `Starting` reservation, under the Project land mutex (§5.3).
   - **Nudge** sends a prompt the way the launch's first prompt is sent.
   - **Cancel** goes through `cancelBecause` with a reason, never through the owner's turn cancel, which stores no reason.
   - **Retire** archives the Task, then cleans its lane (§5.6).
   - **Abort** calls `AbortRun`, discards the Task and removes the lane.
   - **Land** runs `landAndAccept` (§5.4) in a goroutine, with the card in `Landing` until it returns.
   - **ProviderFailed** updates the provider's breaker (§4.5).
   - **Merge** runs the merge job (§5.8) in a goroutine.

The filing tool call (§5.4) and the owner's Accept job (§5.5) also mark their card in `Landing` before they start, so `Next` never lands a request that another call is already landing.

### 4.4 Concurrency

- **Project land mutex.** One in-memory mutex per Project serializes every read-then-write of the integration branch: a lane start, from the tip read through `worktree add`, Task creation and `StartRun`; landing, both preparation and commit phase; and revert, sync and merge. Because lane starts read the tip under the same mutex, no lane forks from a ref state a later step could change.
- **Lock order.** Land mutex, then the Project's acceptance limit, then the store write. The Manager lock is never held while waiting on either. Steers and nudges go out after the mutex is released.
- **Roll forward.** Every write to the integration branch records its intent in the store, moves the ref with compare-and-swap (`git update-ref <ref> <new> <old>`), then finalizes the store. The intent is `landed_sha` on the open hold, or for a revert the cards already marked with `reverted_sha`. A failed compare-and-swap is compensated in the store only: the landing intent is cleared, or the revert undone. Recovery finishes an intent commit X with one rule:
  - the integration branch is an ancestor of X: fast-forward it to X with compare-and-swap, then finalize the store;
  - X is an ancestor of the integration branch: finalize the store;
  - otherwise: compensate the store and redo the operation from scratch.
- **Cancellation.** Preparation (merging the tip into a lane, re-running acceptance) runs on the caller's context, so Archive and Settle, which end a Task's tool calls, can still stop it. The commit phase (intent write, ref update, finalize or compensate) runs on `context.WithoutCancel` with its own control timeout, so neither Archive, Settle nor a closed browser stops it halfway.
- **Sticky intent.** While an open hold carries a landing intent, the store refuses every write that would change that card's status or end its hold, with code `landing` ("landing in progress, retry"). Only `AcceptLanded` and `ClearLanding` pass, and reconcile leaves such holds for recovery. A status change withdraws pending requests, so without this a hold release could withdraw a request whose commit is already on the integration branch.
- **Start re-check.** `StartRun` re-checks ready and both slot counts inside its own write, with the function `RunFacts` uses. It then scopes the Task to the subtask itself, records `waited_on` (§5.3) and starts the hold through the existing single start path. A link, edit, pause or delete that commits between `RunFacts` and `StartRun` makes it refuse `not_ready`; the driver discards the Task and the worktree and counts nothing toward backoff. A run start does not re-pin, and an automatic comment "started by the run of #E" tells it apart from a manual start.
- **Duplicate starts** are impossible, restarts included: the unique index on open holds and the in-transaction re-check both refuse them.
- **Owner git actions.** Lanes sit outside the Project directory, and the owner's Commit, Pull and Push count only Tasks whose workdir is inside the repo, so run Tasks never block them. The owner's git writes never block run Tasks either. The merge job takes the land mutex, then, when `base_ref` is checked out, the owner's git write path.
- **Jobs.** The owner's Accept of a lane done, Revert and Retry merge answer 202 `{job_id}` and report through `board_job` frames, and the automatic merge runs as the same job. No HTTP request waits on the land mutex, which an acceptance re-run can hold for up to 10 minutes.
- **Load.** Parallel per epic 1 to 4, a global cap of 4 open lane holds, and the per-Project acceptance limit, default 1.

### 4.5 Failure handling

- **An attempt ended without landing** (released `ended`, or `rejected` while the holder was not live). `releaseHold` sets `paused='uam'` on the lane subtask unless it or an ancestor is already paused, and adds the comment "paused: attempt #n ended without landing; branch `<b>` kept". The comment never lists uncommitted files, which reconcile reads from the Project directory, not the lane. Resume runs the subtask again in a fresh worktree. This removes the unbounded relaunch the attempt counter allows today.
- **Landing failures** split in two.
  - *Content failures* end the request in the same step: a merge conflict, a red or timed-out acceptance run on the new tip, a guard refusal, an unfinished merge, a stale lane. `Store.LandFailed` rejects the request with the reason and `decided_by = 'uam'`, and adds a card comment. The reason reaches the Task in the tool reply, or as a steer when the call was not the Task's own. If the holder is not live, the hold is released `rejected`, which pauses `uam`. Otherwise the subtask is back to held with no pending request, and the nudge-then-retire rows of §4.2 apply.
  - *Transient failures* keep the request pending and retry after 1, 2, 4, 8, then 15 minutes, with one comment per distinct reason: the integration branch checked out in a worktree, a compare-and-swap lost to a writer outside uam, or the holder Working when the executor or the owner's job wants to touch its lane.

  Landing never runs git in a lane whose holder's turn is Working, except inside the holder's own tool call.
- **Start failures**, by cause:
  - `not_ready`, from the pre-check or `StartRun`, is a race with a board write. Discard and count nothing.
  - Provider: Task creation fails, the first prompt is rejected, or the first turn fails with nothing in the lane. `AbortRun` (reason `aborted`: todo, not paused), discard the Task, remove the lane, and report to the provider's breaker.
  - Git or store: preflight, sync, `worktree add`, or a `StartRun` error other than `not_ready`. `AbortRun` if a hold was written, discard the Task, `git worktree remove --force` and `git branch -D`. The epic backs off for 1, 2, 4, 8, then 15 minutes, with one automatic comment per distinct error. After three consecutive git or store failures the epic gets `paused='uam'` with the error, and the owner must Resume it.
- **Provider breaker**, one per provider across all epics. An aborted start or a holder turn that Failed opens it. While it is open nothing starts and nothing is nudged on that provider. It backs off for 1, 2, 4, 8, then 15 minutes; when it allows, one probe nudge goes to one failed holder, and any completed turn on that provider closes it. While the provider reports signed out it stays open without probes. Each affected epic gets one automatic comment per distinct detail, and its chip reads "Waiting for `<provider>`: `<detail>`". A provider failure never pauses a subtask. Rate limits are not classified anywhere today; they arrive as failed turns and land here.
- **Model.** The `runs.model` CHECK forbids an empty model. Approve validates it as Task creation does, and creation checks it again. The executor never falls back to the Task defaults.
- **Safe mode.** A permission prompt leaves the Task waiting for permission, which is Waiting: it counts in Needs-you and keeps its slot.

### 4.6 Pause, Stop, Settle and blocked

- **Pause** (owner) works on any card under an approved epic, the epic included. Nothing new starts at or under it; running attempts go on. The parts of a split into siblings take the original's own pause, `owner` or `uam`, so a split never lets paused work start. **Resume** clears both `owner` and `uam`. From S5, Resume of an approved epic runs the Approve preflight and sync (§5.2) before the write, refuses with the same codes, and sets `base_ref` when it is empty.
- **Stop** is the owner's Release on a lane-held subtask. The hold ends `released`, the card gets `paused='owner'` and the comment. The web handler then cancels the holder with the reason "uam: #N was stopped", archives it and cleans its lane (§5.6). From S5 the executor's "holding nothing" rows are the safety net, for example after a crash. On a container, Stop is Pause followed by a Release of each running subtask under it; Pause goes first, so nothing new starts in between. There is no separate Stop route, store method or release reason.
- **Settle of a lane holder.** The Settle dialog offers release or cancel for a lane hold, not keep. Release goes through `ReleaseHold` with the settled reason and pauses `owner`, as Stop does. Holds nobody decided are released as before. A keep on a lane hold is refused with 400 before the Task moves. The settled lane Task then holds nothing and is retired.
- **An accepted blocked request on a lane hold** sets the flag or adds the link as before, and also ends the hold with reason `blocked`: todo, not paused. The flag or the open blocker keeps the subtask out of the ready set. Clearing the flag, or the blocker finishing, restarts it in a fresh worktree; the attempt branch is kept. The holder holds nothing and is retired.
- **Owner Mark done** on a lane-held subtask is refused with "accept its done request or Stop it". On an unheld subtask it is allowed as before, and that subtask has nothing to revert.
- **Owner writes that would reopen a running lane's blocker** are refused `in_progress` with refs (§6.3 rule 2).

### 4.7 Restart

Runs, holds, pauses and intents live in board.db. Tasks live in sessions.json, and a busy turn comes back as interrupted. Worktrees stay on disk. So the state survives a restart, and the first pass after boot (`recoverLanes`, from S4; S5 adds steps 3 and 4) does this:
1. Finish intents with the rule of §4.4: every open lane hold with `landed_sha`, and from S6 every landed hold whose `reverted_sha` is not on the integration branch.
2. In each lane whose holder is not Working, `merge --abort` a merge uam started, which its message marks with `Uam-Merge:` (§5.4). A merge the agent started is left for the agent, whose nudge says to finish it.
3. Nudge every interrupted lane holder once, at most one per provider per pass.
4. Land every pending done marked `landing` that has no intent, since the in-memory retry list is gone.
5. Sweep orphans. For each lane directory with no open hold and no Active Task: abort any merge, commit its leftovers to the attempt branch, remove it, and run `git worktree prune`. Delete attempt branches with no hold row and no commits beyond their fork point; a crash between `worktree add` and `StartRun` leaves those.
6. Cross-check. List the `Uam-Request` trailers on the integration branch since its fork from `base_ref`. A request that is not accepted gets one comment on its card naming the orphan commit. Only a hand edit of the integration branch produces one.

The nudge set lives in memory, so a restart costs at most one extra nudge per holder.

## 5. Git: lanes, landing, revert and merge

The git operations live in `internal/web/lanes.go`. They run git through the existing git helpers, with no shell and literal pathspecs.

### 5.1 Names and places

| Thing | Name or place |
|---|---|
| Integration branch | `uam-plan-<p8>`, where p8 is the first 8 hex digits of the Project ID. One per Project, so cross-epic blockers and reverts stay on one line. The prefix has no slash: a repo with a local branch named `uam`, as uam's own repo has, cannot hold `uam/...` refs |
| Attempt branch | `uam-plan-<p8>-<seq>-<id8>`, where id8 is the first 8 hex digits of a new UUID for each start. A sibling ref, so no ref directory and file clash, and never reused, so a branch a crash left can't block the next start |
| Worktree | `<directory of sessions.json>/lanes/<project-id>/<seq>-<id8>`, derived from the branch, not stored. That directory is private to the owner |
| Task workdir | The worktree plus the Project's path relative to the repo top, since a Project may be a subdirectory |
| After landing | The attempt branch is deleted. The squash commit carries the work, and its trailers name the card and the request. Attempt branches that did not land stay |

### 5.2 Preflight and the integration branch

**Preflight.** Approve refuses when it fails (from S4), and so does Resume (from S5):
- git 2.40 or later, for `merge-tree --write-tree` and `--merge-base`;
- `git var GIT_COMMITTER_IDENT` succeeds;
- the repo has a commit;
- `base_ref` exists.

**`base_ref`** is the branch checked out in the Project directory at the first Approve, or at the first Resume of an epic approved before S4. It is stored in `project_settings`, and the owner can change it.

**Creation.** The integration branch is created on first need at the `base_ref` tip, with a create-only `update-ref`.

**`hasAll(a, b)`** holds when merging b into a would bring nothing: b is a or an ancestor of a, or a is an ancestor of b with the same tree. Sync and merge test it instead of plain ancestry. With ancestry alone, the merge commit one side gets makes it look ahead of the other even when its tree adds nothing, so sync and merge would keep answering each other with empty merge commits.

**Sync**, under the land mutex, when `base_ref` brings something the integration branch lacks (`!hasAll(integ, base)`): `merge-tree --write-tree integ base`, and if that is clean, `commit-tree -p integ -p base` followed by a compare-and-swap. Sync runs at Approve and Resume, where a conflict refuses with the files, and before each lane start, where a conflict skips the sync and adds one comment on the epic per base sha. This is how work the owner committed to `base_ref` reaches later lanes, including an unapproved blocker epic done by hand.

Uncommitted changes in the owner's directory are never in a lane, and the Approve dialog says so.

### 5.3 Lane start

Under the Project land mutex:
1. Run the preflight (cached), then sync.
2. Pre-check with `CanStart`, which runs `StartRun`'s ready and slot checks read-only. A refusal stops here and counts nothing.
3. Read the integration tip.
4. `git worktree add -b <attempt> <dir> <tip>`.
5. Create the Task with the run's provider, model, effort, context size and mode, the name `#seq title`, and a workdir that Task creation accepts only under the lanes root.
6. `StartRun` with the baseline `{Head: tip, Dirty: []}` and the branch. It re-checks ready and slots, and records `waited_on`: every done subtask at or under a blocker of the subtask or of one of its ancestors.

Then, with the mutex released:

7. Send the run preamble (§6.4), built from the card `StartRun` returned.

Creation runs under the mutex, so a landing in the same Project waits for it, which takes seconds.

A run Task gets only `board_get`, `board_list`, `board_checklist`, `board_comment` and `board_request`, through the tool subset mechanism Utility jobs use. It gets no `uam_create_task`, as routine runs get none. Push and Pull from a run Task's git panel are refused.

### 5.4 Done claim and landing

A `board_request` done on a lane hold works in the Task's workdir:
1. The finishing guard runs.
2. Refuse `land_conflict` while the lane has a merge in progress (`MERGE_HEAD`) or unmerged paths (`ls-files -u`), naming the files: "finish your merge of uam-plan-x and commit, then file done again". Leftovers are never committed with conflict markers in them.
3. Commit leftovers in the lane: `add -A`, then `commit --no-verify -m "#seq: work in progress"`.
4. Read the integration tip. Refuse `land_stale` when `rev-list <tip>..HEAD` holds a commit with a `Uam-Request` or `Uam-Revert` trailer: the lane carries integration commits the branch no longer has. The reply tells the agent to end its turn, the nudge-then-retire rows end the attempt, and Resume starts fresh. With roll-forward only a hand edit of the integration branch causes this.
5. If the tip is not an ancestor of the lane's HEAD, run `git merge --no-edit --no-verify -m "Merge uam-plan-x\n\nUam-Merge: <tip>" <tip>` in the lane. On a conflict, collect the conflicted files and the landed cards that touched them (from the `Uam-Card` trailers on `base..tip`), run `merge --abort`, and refuse `land_conflict` with "run `git merge uam-plan-x` in your directory, resolve a.go, commit, file done again".
6. Collect evidence against the baseline `{Head: tip}`: the diff and commits are exactly this subtask on top of the current tip. No overlap is computed.
7. Run the acceptance command in the lane, first under the Project's acceptance limit (shared with manual runs in the Project directory), then under the directory's runner slot. A non-zero exit refuses, as before. A run the timeout kills is filed with `acceptance_could_not_run`, so it waits for the owner instead of counting as red.
8. Mark the card in `Landing`, then file the request. When it would otherwise be accepted automatically, a lane done stays pending with the wait `landing` and `payload.landing = true`. Every other wait reason still applies.
9. `landAndAccept`.
10. Reply with one of: "#31 is done and landed on uam-plan-x as 9f3e2a1. You are finished; end your turn."; the content failure and what to do; or "landing is queued: `<reason>`. uam lands it when that clears. End your turn."

**`landAndAccept(id, by)`**, under the Project land mutex.

Preparation, on the caller's context:
1. Re-read the request and stop unless it is pending. If the open hold already has `landed_sha`, go to step 7 and finish it with the rule of §4.4.
2. Outside the holder's own tool call, stop as transient while the holder's turn is Working.
3. Check the lane as in claim steps 2 and 4. A failure is a content failure.
4. If the tip moved since the claim, merge the new tip into the lane as in claim step 5 and re-run acceptance under the acceptance limit. A conflict, a red run or a timeout is a content failure.
5. `S := commit-tree <lane HEAD>^{tree} -p <tip>`, with the message `<title> (#seq)\n\n<claim>\n\nUam-Card: #seq\nUam-Request: <id>`.

Commit phase, on `context.WithoutCancel` with its own timeout:

6. `MarkLanding(id, S)` sets `landed_sha = S` on the open hold. This is the intent, and it is sticky (§4.4).
7. `update-ref refs/heads/<integ> S <tip>`. If the integration branch is checked out in any worktree, or the compare-and-swap fails, `ClearLanding(id)` and treat it as transient.
8. `AcceptLanded(id, S, by)` takes the existing accept-and-done path and adds the comment "Landed on uam-plan-x as `<short>`". If this store write fails, the intent stays and recovery finishes it. The ref never moves back.

On a content failure, `LandFailed(id, reason, holderActive)` (§4.5); the ref was not touched.

What lands is always what the acceptance command last tested. In S4, before the executor exists, a queued landing waits for the owner's Accept, which runs the same job, or for the next boot. From S5 the executor retries it.

### 5.5 Owner Accept of a waiting lane request

`POST /requests/{id}/accept` on a lane done answers 202 `{job_id}` with job kind `land`, reported through `board_job` frames. It refuses at once with 409 `task_working` while the holder's turn is Working. The job marks the card in `Landing` and runs `landAndAccept` with the owner as decider. A content failure ends the request through `LandFailed` and fails the job with the reason, which reaches the Task as a steer. A transient failure leaves the request pending for the executor's retry. The store's plain Accept refuses a lane done that does not come through this path. Reject is unchanged, except that it answers `landing` while an intent is stored.

### 5.6 Cleanup

When a lane Task is archived or deleted, or found orphaned at boot:
1. abort any merge in progress, since the Task is gone;
2. commit leftovers to the attempt branch (`--no-verify`);
3. `git worktree remove`;
4. delete the attempt branch if its hold landed, and keep it otherwise.

The transcript stays readable after the worktree is gone, because reading a Copilot history needs only the conversation ID. The recent-folders list leaves out workdirs under the lanes root, so lane directories never push the owner's folders out of the picker.

### 5.7 Revert (owner only)

**Closure.**
- The closure comes from the snapshots taken at start, not from the current links. Each landed hold's `waited_on` lists the done subtasks it waited on when it started.
- `Dependents(X)` is every landed subtask whose landed hold lists X in `waited_on`, taken transitively. Links changed after a subtask started don't change it.
- The seed is the subtask; on a story or an epic, every landed subtask under it. The closure is the seed, its landed dependents, and any cards the owner adds with `include`.
- A running attempt whose open hold lists a closure card in `waited_on` refuses with `revert_running`: "Stop #d first".
- Unstarted dependents wait again through their live links, because the seed returns to todo, paused.

**Preview** (`GET`) returns the closure, each landed sha, the files (`diff-tree --name-only`), a dry-run result, and whether `base_ref` already has any of those landed commits.

**Apply.** `POST` checks the closure against `expect` (409 `stale` on a difference) and running dependents (409 `revert_running`) at once, then answers 202 `{job_id}` with job kind `revert`. The job, under the land mutex:
1. Order the closure newest first, by position in the integration branch's first-parent history.
2. Build the chain as objects only. Starting from `cur = tip`, for each landed sha L: `T := merge-tree --write-tree --merge-base=L cur L^`, then `cur := commit-tree T -p cur -m "Revert #seq <title>\n\nThis reverts L.\n\nUam-Revert: #seq"`. No ref moves.
3. Any conflict fails the job with `revert_conflict` and writes nothing. The reason names the files and the later landed cards that touched them, so the owner can include them.

Commit phase, on `context.WithoutCancel`:

4. `Store.Revert(owner, items, expect, C = cur, comment)`, in one transaction: each card goes from done to todo with `paused='owner'`, `reverted_sha = C` on its landed hold, and the comment "reverted in `<C>`: `<reason>`". It refuses if the closure differs from `expect`.
5. One `update-ref integ C tip`. If the integration branch is checked out in a worktree, or the compare-and-swap fails, `Store.UndoRevert(items, C)` puts the cards back to done, clears `reverted_sha` and `paused`, and adds the comment "revert not applied: `<reason>`". The job fails with that reason.

Between the store write and the ref move nothing can start those cards: they are paused, and lane starts wait on the mutex. Recovery finishes a revert whose `reverted_sha` is not on the integration branch with the rule of §4.4: a fast-forward when the branch is an ancestor of C, `UndoRevert` otherwise.

**Reopen without reverting code.** The way out when Revert cannot apply, for example on a conflict with commits no card owns (sync merges, the owner's own commits) or after a hand edit of the integration branch. On a landed subtask, `POST /cards/{ref}/status {status: "todo", keep_code: true, comment}` sets todo and `paused='owner'`, leaves `reverted_sha` empty, and adds the comment "reopened without reverting `<landed sha>`; the code stays on uam-plan-x". It moves no ref and runs the owner integrity check (§6.3 rule 2). Its landed dependents stay landed, and the dialog lists them. The owner fixes the code by hand, in Terminal or on their own branch once the work is merged, or leaves it to the next attempt.

**After a merge.** If `base_ref` already has a reverted landing, the revert still lands on the integration branch, and when the revert job ends uam merges it into `base_ref` (§5.8). The owner's Revert is the approval for that merge. A running subtask is never reverted; it is Stopped. The owner's "Back to To do" on a landed subtask without `keep_code` is refused with "Revert #n instead".

### 5.8 Merge into the owner's branch (covered by the epic approval)

On 2026-10-06 the owner decided: "Merge also needs to be approval based only. I approve epic for approval." Approving an epic also authorizes merging its landed work into `base_ref`. There is no separate merge approval, and the normal flow needs no Merge click.

**Triggers.** `Next` returns `Merge` with the Projects to merge. It stays pure: besides `RunFacts` it takes one git fact per Project, read by the driver, whether `base_ref` already has everything the integration branch carries (`hasAll(base, integ)`, §5.2). A merge fires when:
- an approved epic derives done and `!hasAll(base, integ)`;
- an owner Revert job ended and `base_ref` already had one of the reverted landings. The owner's Revert is the approval for merging it.

The trigger is idempotent: once `hasAll(base, integ)` holds, nothing fires. Plain ancestry is not used, because after a sync merge brings an owner commit into the integration branch, that branch is not an ancestor of base even when nothing is left to merge. The Planner header's "N ahead" count and the revert preview's "already merged" flag use the same fact, counting landed commits that `base_ref` lacks rather than integration commits. A merge carries everything on the integration branch, including the landed subtasks of other approved epics in the Project. Each of those was approved under its own epic and passed acceptance. Proposals never land, so nothing unapproved reaches `base_ref` through uam.

**The merge job** has job kind `merge` and carries the Project instead of a card. It runs under the land mutex, with the commit phase on `context.WithoutCancel`, and first stops with nothing to do when `hasAll(base, integ)` holds.
1. **`base_ref` checked out** in the Project directory or another worktree that is not a lane:
   1. Begin a git write there, as the owner's Commit does. Lane Tasks never count as busy; any other busy Task in the repo answers `git_busy`.
   2. `merge-tree --write-tree HEAD integ`. A conflict answers `merge_conflict` with the files.
   3. `git merge --no-ff --no-edit -m "Merge uam-plan-x: <epic title> (#seq)" <integ>` through the owner's git write path, so hooks run and git refuses to overwrite local changes (`local_changes`, with the files).
2. **`base_ref` not checked out anywhere:** `merge-tree --write-tree base integ`, `commit-tree -p base -p integ`, then a compare-and-swap `update-ref refs/heads/<base_ref>`. No working tree changes.
3. uam never pushes and never force-moves `base_ref`.

**Outcomes.**
- Success: the epic gets the comment "Merged into `<base_ref>` as `<sha>`", which lists the landed subtasks it carried and marks those that changed tests or build files (§9 decision 3).
- `git_busy` and `local_changes` retry with backoff (1, 2, 4, 8, then 15 minutes), and the epic shows "Merge waiting: `<reason>`".
- `merge_conflict` is not retried on its own for the same pair of integration and base tips. The executor keeps that pair in memory, so after a restart it is tried once more. The epic gets one comment per distinct pair, naming the files and the landed cards that touched them, and shows "Merge blocked" with Retry merge. The owner resolves it in Terminal or reverts the offending subtask, and that revert then merges.

**Retry merge.** `POST /projects/{id}/merge` stays as an owner action: Retry merge after a blocked or waiting merge, and an early merge of an unfinished epic's landed work. It answers 202 `{job_id}` and runs the same job.

### 5.9 Limits that remain

- An unlinked semantic dependency, for example later code calling a reverted function, reverts cleanly but breaks the build. Revert does not run acceptance. The epic shows the new tip, and Check stays available.
- If the integration branch is rewritten by hand, the stored shas are no longer ancestors and Revert refuses. It never reverts partially. Reopen without reverting code is the way out; uam does not resolve revert conflicts.
- Lanes separate work by directory. Work an agent does outside its worktree is not isolated; the preamble tells it to stay in its directory, and nothing else holds it there.

## 6. Planning, agent writes and approval

### 6.1 Planning from a Task (R1)

1. The owner opens an ordinary Task and asks for a plan.
2. The agent creates the epic, stories and subtasks with `board_create`, then links each level with `board_link`.
3. Its reply tells the owner to approve #E in the Planner, and it ends the turn.
4. The Planner Inbox gets a derived **Plans to approve** section, which also counts in Needs-you. It lists live proposed epics, and approved epics with live proposals under them ("N to approve").
5. The owner gives feedback before approval in the same Task's chat.
6. **Approve and run** opens the dialog. It is labelled "Approve" in S2 to S4, while nothing runs yet.

### 6.2 Approve

`Store.Approve(ctx, owner, epicRef, settings, items)` is one owner write. `items` are `{id, revision}` pairs as the dialog showed them.

It refuses unless all of these hold:
- the card is a live epic and does not derive done;
- every item is a live card in its subtree, at the revision given. A mismatch refuses `stale` with refs, and the dialog reloads, so an edit by a planning agent between render and post is never approved unseen;
- no subtask under it is held by a non-lane hold ("finish or release #n first"), so manual and lane work never mix;
- every live container in the subtree has at least one live subtask that is confirmed or listed. A container with no confirmed subtask derives planned forever and stalls its dependents, and one whose only live subtasks are unlisted proposals would be in that state after approval;
- every unstarted subtask resolves to an acceptance command (§9 decision 4);
- the settings are valid: a model that is set and offered, parallel 1 to 4, mode safe or yolo. From S4 the git preflight must also pass and the base must sync cleanly;
- from S4, confirming the listed proposals gives no running lane subtask a new open blocker (§6.3 rule 2). Re-approval is the usual way a done container reopens, since a proposal added under it was ignored by derivation until then.

On success it confirms and pins each listed id, confirming ancestors as an owner touch does; upserts the `runs` row; clears the epic's own `paused`; and from S4 sets `base_ref` when it is empty. A proposal that was not listed stays a proposal. Approving again updates the settings and confirms new additions. Attempts already running keep their Task's settings.

From S2 the store refuses, under an approved epic, per-card Confirm and every manual start with `run_owned`. Accepting an agent's request there confirms no proposal either: an accepted move, split or done leaves each card its own confirmation, so a confirmed card moved under a proposal story waits, with the story, for the next approval. S4 reopens Launch there as a lane start gated by ready, and S5 refuses it again. So approved epics never collect non-lane holds or per-card confirmations before the executor arrives, and v7 pauses every approval made before S5.

### 6.3 Agent writes (R7)

- **Reach** is unchanged (ADR 0005 decision 9). A Task reaches the cards it created that have not started, with everything under them, its planning container, and its held subtask. Cards the owner or another Task created stay out of reach unless they sit under one of the Task's own cards. The owner grants reach to any epic with Plan with agent. A run Task reaches only its subtask.
- **Within reach** an agent may create, edit, move, link, unlink, split and delete every unstarted card, confirmed or approved, under the rules below.
- **Integrity**, enforced by the store. The single outline write path takes the actor, snapshots before the write and checks after it, so one place covers every write.
  1. **The started lock**, as in ADR 0005 decision 8.
  2. **No new open blocker for running work.** Before the write, take each covered subtask's hold-back set: its open blockers, own and inherited, plus its blocked flag. After the write, refuse `in_progress` with refs if any set gained a member. Coverage:
     - agent writes cover every started subtask in the Project, held or done. Agent creates and splits under a done container make proposals, which derivation ignores, so they pass and the container stays done;
     - owner writes under approved epics cover every running lane subtask. This catches Approve confirming a proposal under a done container a running lane waits on, Restore of a cancelled blocker, link, move, the blocked flag, and status changes such as Reopen without reverting code. The refusal tells the owner to Stop the named subtasks first or wait for them to land. Owner writes outside approved epics are not checked.
  3. **Delete.** `Store.Delete` generalizes Dismiss. It cascade-cancels the card with the comment "deleted by `<task>`", and the owner can Restore it. It refuses:
     - when anything in the subtree has started;
     - when a started subtask waits on the card or a card under it, directly or through an ancestor;
     - on an approved epic itself, where the agent files a cancel request instead;
     - when the write would bring a container other than the deleted card to done or cancelled. Deleting a story's last unfinished confirmed subtask would close the story, cancel its live proposals and release its dependents before the replacement work exists;
     - under an approved epic, when it would leave a confirmed container with no live confirmed subtask, which derives planned forever.

     A cancelled blocker releases its dependents, and the reply names them. SKILL.md tells planners to link the replacement before deleting a blocker.

     The container-closing refusal also covers an **agent split into siblings**. Splitting a story's last unfinished confirmed subtask cancels the original and leaves only proposals, which derivation ignores, so the story would derive done and release its dependents before the parts are approved. An agent split that would close a container is refused with the same code and refs as Delete. The owner may still split it. Under an approved epic the owner's Accept of an agent's split request is refused the same way, since its parts are proposals too: "accepting would close #S before its parts are approved; split #n yourself instead". The owner's own split keeps the parts confirmed and stays allowed.
  4. **Links on started work.** An agent link or unlink is refused `in_progress` when the blocked side is a container with a started or done subtask under it. Containers never start, so checking only the two endpoints misses this.
  5. **Moves across approved epics.** An agent move that changes a card's epic (the root counts as no epic) while either epic has a `runs` row is filed as a change request, the path a move under a proposal already takes. Otherwise a confirmed card could move into an approved epic, or from a safe-mode epic into a yolo one, and run under settings the owner never approved for it. Moves inside an approved epic are covered by the approval, subject to rule 2 and rule 3's emptying check.
  6. **Split copies links.** A split into siblings inserts the original's links on every part, both ways. They stay on one level because the parts share the story, and they cannot close a cycle because the parts are new. A link to a started dependent is not copied, and the reply names it. This applies to owner and agent splits alike.
  7. **The layered DAG and no cycles**, as in ADR 0005 decision 7.
- **Restore under an approved epic** brings every card under the epic, other than the epic itself, back as a proposal with a fresh expiry. The cards show as "N to approve" and run only after the owner approves them. Restore also runs rule 2's owner check.
- **Caps.** `CapCreated = 50`, counted over the Task's non-cancelled cards, so delete and expiry give slots back. A lifetime ceiling, `CapCreatedTotal = 200`, counts every card the Task created, cancelled ones included, so the cards one Task creates over its lifetime stay bounded while deletes keep freeing live slots. Purge deletes rows and so frees lifetime slots too; Purge is the owner's. The cap of 10 unconfirmed children per container, root included, is unchanged; it counts proposals only, so it binds before approval, not after. The 20-comment cap is unchanged.
- **Expiry** is unchanged. Unapproved plans and additions made after approval expire 14 days after creation or the last owner write. Approved cards never expire.
- **Under an approved epic**, an agent edit, link, unlink or delete of an existing unstarted card, and a move inside the epic, is covered by the approval, within the rules above. A move into or out of the epic is a change request. A new card is a proposal: the executor skips it, the epic shows "N to approve", and approving again picks it up. A proposal left under an approved story surfaces before it can be cancelled silently, because the story's closing done waits with `closes_with_proposals` and names it.

### 6.4 Agent tools

| Tool | Change | Slice |
|---|---|---|
| `board_delete {ref}` | Replaces `board_dismiss`. Its text states the reach and the refusals, and says to link a replacement before deleting a blocker | S1 |
| `board_create`, `board_link`, `board_edit`, `board_unlink`, `board_split` | Texts restate the integrity rules: no new blocker for started work, no link changes on containers with started work, splits keep links. The `board_create` reply on an epic says "when the plan is complete, ask the owner to approve #E in the Planner; nothing runs before that" | S1, S2 |
| `board_edit` moving into or out of an approved epic | Answers that it was filed as a change request for the owner | S2 |
| `board_get` | Shows the epic's run state and any pause on the path | S2 |
| `board_claim` | Under an approved epic: refused `run_owned` | S2 |
| Run preamble | "You work only on #31, in your own git worktree on branch uam-plan-x-31-1a2b3c4d, made from uam-plan-x at `<sha>`. Other subtasks run in parallel in their own worktrees. Stay in this directory; do not switch branches, push, or merge unless a done reply tells you to, and finish any merge you start before filing done. Tick the checklist and file board_request done; uam commits what is left, merges the integration tip, runs the acceptance command and lands your work as one commit. When it lands, or when the reply says landing is queued, end your turn." | S4 |
| Run Task tool set | `board_get`, `board_list`, `board_checklist`, `board_comment` and `board_request` only. No `uam_create_task` | S4 |
| `board_request` done reply for run Tasks | "landed ... end your turn"; "landing is queued ... end your turn"; `land_conflict` with the merge steps or "finish your merge"; `land_stale`; acceptance timed out, waiting for the owner | S4 |

The built-in `uam` skill (SKILL.md) changes in the same slice as the behaviour it describes:

| Slice | SKILL.md change |
|---|---|
| S1 | `board_delete` and its refusals; no new blockers for started work; no link changes on containers with started work; link the replacement before deleting a blocker; splits keep links, and a split that would close a container is refused like a delete; caps of 50 live cards, 200 cards over a Task's lifetime and 10 unconfirmed per card |
| S2 | The hand-off: build the epic, link it, tell the owner to approve #E, stop. After approval new cards wait for approval again, edits apply, moving a card into or out of an approved epic goes to the owner, and restored cards come back as proposals. Paused cards don't run. Nothing under an approved epic is claimed |
| S4 | Working on an approved epic: the worktree, landing, the conflict merge, finishing merges before filing done, no claims, ending the turn after landing or when landing is queued |
| S5 | uam starts an approved epic's subtasks; never claim there |
| S6 | A reverted or reopened subtask comes back paused; read its comment before re-planning |

## 7. HTTP API

Owner-only unless noted, behind the existing sign-in, cross-origin and JSON checks.

| Route | Slice | Notes |
|---|---|---|
| `POST /api/board/cards/{ref}/approve` `{items: [{id, revision}], provider, model, effort, context_size, mode, parallel}` | S2 | Returns the epic. 400 for a missing model or invalid settings. 409 `stale` with refs, and the dialog reloads. 409 `invalid` with refs for empty containers (including ones whose only live subtasks are unlisted proposals), subtasks with no command, and held subtasks. From S4 also `git_too_old`, `no_git_identity`, `merge_conflict` on sync, and `in_progress` with refs when it would reopen a running lane's blocker |
| `PATCH /api/board/cards/{ref}` `{paused: bool}` | S2 | An owner-only field beside `blocked`, only under approved epics. From S5, Resume on an approved epic runs the Approve preflight and sync and sets `base_ref` when it is empty |
| `POST /api/board/cards/{ref}/confirm` under an approved epic | S2 | 409 `run_owned`: "approve it from the epic" |
| `POST /api/board/cards/{ref}/launch` and `/attach` under an approved epic | S2, S4, S5 | S2: `run_owned`, with or without confirm (Do whole story is Launch on a container). S4: Launch on a subtask starts a lane with the run's settings, or refuses `not_ready` with refs; Attach and Do whole story stay `run_owned`. S5: Launch is `run_owned` again |
| `POST /api/board/cards/{ref}/restore` under an approved epic | S2 | Restores as proposals. From S4 also 409 `in_progress` when it would reopen a running lane's blocker |
| `POST /api/board/cards/{ref}/release` on a lane hold | S4 | Stop: ends the attempt (`released`), pauses the card, cancels and archives the holder, cleans the lane, and returns the card. 409 `landing` while an intent is stored |
| Settle with `keep` on a lane hold | S4 | 400: a lane hold is released or cancelled |
| `POST /api/board/requests/{id}/accept` on a lane done | S4 | 202 `{job_id}`, job kind `land`. 409 `task_working` while the holder's turn is Working |
| `GET /api/board/projects/{id}` | S4, S5 | Adds `integration {branch, base_ref, ahead, behind}` and `accept_parallel` (S4), and `executor {providers}` (S5). `PATCH` takes `base_ref` and `accept_parallel` (1 to 4) |
| `GET /api/board/cards/{ref}/revert` | S6 | The preview |
| `POST /api/board/cards/{ref}/revert` `{include: [ref], expect: [ref], comment}` | S6 | 202 `{job_id}`, job kind `revert`. 409 `stale` (the closure differs from `expect`) or `revert_running` at once. The job fails with `revert_conflict` |
| `POST /api/board/cards/{ref}/status` `{status: "todo", keep_code: true, comment}` on a landed subtask | S6 | Reopen without reverting code. Without `keep_code`: 409 `invalid`, "Revert #n instead" |
| `POST /api/board/projects/{id}/merge` | S6 | Retry merge, or an early merge; the normal merge is automatic (§5.8). 202 `{job_id}`, job kind `merge`. The job fails with `merge_conflict`, `local_changes` or `git_busy` |

New error codes: `not_ready`, `run_owned`, `stale`, `landing`, `task_working`, `land_conflict`, `land_stale`, `merge_conflict`, `local_changes`, `revert_conflict`, `revert_running`, `git_too_old` and `no_git_identity`.

## 8. UI

**Card actions.**
- An epic gets **Approve...** (S2), renamed **Approve and run...** in S5.
- Under an approved epic: Pause / Resume on every card (S2); Stop on running subtasks, which posts Release, and on containers, which pauses and then releases each running subtask under it (S4); Revert on landed subtasks, stories and epics, and Reopen without reverting code on landed subtasks (S6).
- Hidden under an approved epic: Confirm, Launch, Do whole story and Attach (S2; S4 shows Launch on ready subtasks, S5 hides it again), and Back to To do on landed subtasks (S6).
- Outside approved epics nothing changes.

**The Approve dialog** shows:
- the subtree that will run, grouped by story, with "waits on" lines, new proposals marked, and paused cards;
- refusals inline: empty containers, subtasks without a command, held subtasks, and from S4 running subtasks the approval would reopen a blocker of;
- the Task default fields. A model is required, and none is preselected unless Settings has one, so no fallback model applies silently;
- the mode, with Safe warning that permission prompts stop unattended work;
- parallel as a 1 to 4 segmented control, default 2;
- the Project acceptance command with an inline editor, and from S4 the acceptance limit ("Acceptance runs at a time", default 1) and "Check in a clean checkout";
- the base branch, the integration branch and the note "uncommitted changes are not included" (S4);
- a warning when the epic waits on an unapproved epic;
- in S2 to S4, the note "nothing runs until execution ships; manual starts are not offered under an approved epic".

The dialog posts the listed ids with the revisions it rendered. On `stale` it names the changed cards, reloads them and asks again. It also opens from a Task's Plan panel.

**Elsewhere.**
- The Inbox gets a **Plans to approve** section above the requests, and Needs-you counts it. The Accept job shows progress (S4).
- The Settle dialog offers release (labelled Stop) and cancel for a lane hold, not keep (S4).
- Chips always carry a word or screen-reader text: "N to approve", "Paused" or "Paused by uam", "Waiting on #x", "Landing", "Landed 9f3e2a1", "Reverted", "Reopened, code kept", "Waiting for Copilot: `<detail>`". The epic shows "Running 2 of 2 · Ready 3 · Waiting 4". No spinners: doing and Landing stay static glyphs.
- A card's Attempts list adds the branch, the landed sha and the reverted sha.
- The Planner header shows "uam-plan-x · 5 ahead of main" and the merge state: "Merge waiting: `<reason>`", or "Merge blocked" with Retry merge (S6). The merge job shows progress.
- Revert, Stop and Retry merge use the anchored confirmation popover. Revert shows a conflict inline, with Reopen without reverting code as the way out, and lists the dependents that stay landed.
- Under an approved epic the Tree's "+N suggested" fold loses its per-row Confirm; Dismiss stays.

## 9. Decisions (2026-10-06)

The owner accepted each of these on 2026-10-06.

1. **Agent reach is unchanged.** R7 works within ADR 0005 decision 9's reach, and the owner grants more with Plan with agent. Not chosen: Project-wide reach, which with deletes would let any unrelated Task cancel or rewrite approved work that runs unattended.
2. **Agent edits under an approved epic need no re-approval.** Edits, links, deletes and moves inside the epic are covered by the approval, consistent with "started is the line". New cards stay proposals until the owner approves again. A move into or out of an approved epic is a change request, and Approve checks each listed card's revision. Not chosen: pausing the epic for re-approval on any agent write, which needs a revision counter and brings back the per-change approval R2 removes.
3. **`tests_or_build_changed` does not hold a run subtask.** It is recorded, shown on the card and listed in the epic's merge comment. Since the merge became automatic (decision 11) there is no review point before the base branch: the flag is information, and Revert is the remedy. Not chosen: holding the request, which turns R2 back into per-subtask approval, since most subtasks touch tests.
4. **Approve requires every unstarted subtask to resolve to an acceptance command**, with the Project default editable in the dialog. Without one every done waits for the owner, and the DAG never moves on its own.
5. **Plans to approve is a derived list**, not an `approve` request kind. It needs no rebuild of the requests table and no new kind in every switch over kinds. Feedback goes through the planning Task's chat. Revisit if the owner wants Reject-to-steer from the Inbox.
6. **Parallel limits.** Per epic 1 to 4, default 2. A global cap of 4 open lane holds across the server, as a constant. A per-Project acceptance limit, default 1, editable 1 to 4. Each lane is a Copilot session plus an acceptance run, and commands written for one tree collide on fixed ports, temp files and databases, or slow past the timeout, when run side by side. Two lanes keep land conflicts and quota use low. Raise the acceptance limit only for a Project whose command is known to be safe in parallel.
7. **Failure policy.** A content failure is nudged once; on the second end without a request the attempt is retired and the subtask paused (`uam`), with no automatic new attempt. A failed turn never burns a subtask: an attempt that failed before changing anything is aborted (todo, not paused), a later failure keeps the hold, and a per-provider breaker backs off starts and nudges for 1 to 15 minutes and probes with one nudge. After a restart each holder gets one "continue" nudge, at most one per provider per pass. This bounds relaunch loops without an attempt counter.
8. **Lanes live in `<config dir>/lanes/`**, outside the repo, so run Tasks never block the owner's git actions. If Copilot refuses that directory, the fallback is `<repo>/.uam/lanes`, excluded through `.git/info/exclude`, and run Tasks then count as busy for the owner's Commit.
9. **No setup-command setting for now.** Acceptance commands set up what they need (for example `npm ci && npm test`), and the Approve dialog's "Check in a clean checkout" runs the acceptance command once in a temporary worktree at the tip (S4). A per-Project setup command comes only if runs show agents failing on a missing environment.
10. **One integration branch per Project**, following `base_ref`, synced at Approve, at Resume and before each lane start. Not chosen: per-epic branches, which break cross-epic blockers, because one epic's lanes would lack the other's code, and spread reverts across branches.
11. **Landed work reaches the owner's branch automatically, covered by the epic approval** (§5.8, S6). The owner decided it on 2026-10-06: "Merge also needs to be approval based only. I approve epic for approval." The merge job runs when an approved epic finishes and after an owner Revert of merged work, through the owner's git write path when `base_ref` is checked out. Retry merge stays for blocked merges and early merges. Not chosen: a Merge click as the only way in, which this design's first draft had (§2 item 20), and a copyable `git merge` command, since the owner often reads uam on a phone, where Terminal is awkward.
12. **R3 is called Pause / Resume**, with the hint "Block execution: uam won't start this or anything under it". "Blocked" already means an agent is stuck, and "hold" means a Task working a subtask. Stop, under an approved epic, is the owner's Release: it ends the attempt and pauses the card.
13. **No autopilot for run Tasks.** The preamble asks for a single turn, and nudges cover the rest. Autopilot has no time limit outside routines. Enable it only if runs show most attempts needing a nudge.
14. **Manual Launch and Do whole story stay outside approved epics**, ADR 0005 decision 6 included. Root cards and the owner's existing flows still need them, and removing them is not part of the request.
15. **When Revert cannot apply, the owner reopens without reverting code** (§5.7): todo and paused, the landed sha named in a comment, the code left on the integration branch with a warning, dependents left landed. Not chosen: resolving a revert by hand in a temporary worktree from the UI, which adds a conflict editor for a rare case that Terminal already covers.
16. **An accepted blocked request on a lane hold ends the attempt** with reason `blocked` and no pause. The flag or link holds the subtask back, and clearing it is the owner's go-ahead. Not chosen: also pausing `owner`, which needs a second click (Resume) after unblocking.
17. **Restore under an approved epic restores as proposals**, shown as "N to approve". Not chosen: restoring confirmed, which runs the cards at once without the Approve review.
18. **Epics approved before the executor ships come back paused** (migration v7), and Resume runs the preflight. Not chosen: requiring a fresh Approve. Either way a deploy never starts weeks-old approvals unattended.

## 10. Delivery

One PR per slice, merged in the order S0, S1, S3, S2, S4, S5, S6. S3 touches only the lane git code and may be built beside S1 and S2. S6 may be built beside S5 and merges after it.

| Slice | Delivers | Migration |
|---|---|---|
| S0 | This ADR | none |
| S1 | Agent CRUD under the integrity rules: `board_delete`, the agent checks of §6.3 rules 2 to 4, the container checks of delete and agent split, split copies links, the caps of 50 live and 200 lifetime cards | none |
| S2 | Approve at the epic, Pause, Plans to approve, `run_owned` for per-card Confirm and every manual start under approved epics, cross-epic agent moves as change requests, Restore as proposals | v5 |
| S3 | The lane git operations, with no user-facing change | none |
| S4 | Lane runs started by hand: Launch under an approved epic starts a lane gated by ready; landing, Stop, Settle, the owner's Accept job, boot recovery, the owner integrity check | v6 |
| S5 | The executor; Launch under approved epics is refused again; "Approve and run" | v7 |
| S6 | Revert, Reopen without reverting code, the automatic merge and Retry merge | none |

In S2 and S3 an approved epic runs nothing. Each slice that changes what agents see or can do updates SKILL.md (§6.4), and each that refines this design updates this ADR.

## Test plan (invariants)

Each item is a store, tool, web or UI test. Every ADR 0005 invariant still holds outside approved epics, except where §2 says otherwise.

**Approval and the execution gate**

1. Approve confirms exactly the listed ids at the listed revisions. An unlisted proposal stays one. It refuses a changed revision (`stale`), an id outside the subtree, a non-epic, an agent caller, an empty model, a done epic, an empty container, a container whose only live subtasks are unlisted proposals, a subtask with no command, and a held subtask.
2. Under an approved epic, per-card Confirm, Launch with or without confirm, Do whole story, Attach and Claim answer `run_owned`. In S4 only, Launch on a subtask starts a lane when it is ready and answers `not_ready` otherwise.
3. `paused` is owner-only. An agent write carrying it is refused, and the owner may pause a started card.
4. An agent move into or out of an approved epic, the root included, is a change request.
5. Restore under an approved epic makes proposals.
6. Purge drops the `runs` row of a purged epic.

**Agent writes**

7. Delete cascade-cancels a card in reach, which the owner can restore, and frees a live-card slot; the lifetime ceiling of 200 still counts the deleted card. It is refused out of reach, with started work under the card, with a started subtask waiting on it, on an approved epic, when it would close another container, and when it would leave an approved container with no live confirmed subtask.
8. No agent write gives a started subtask a new open blocker. Agent creates and splits under a done container make proposals and leave it done. The same writes by the owner outside approved epics pass.
9. Agents neither link nor unlink where the blocked side is a container with started or done work under it; the owner may.
10. A split copies the original's links to every part, both ways, except links to started dependents, which the result names. An agent split that would close a container is refused as a delete is, and so is the owner's Accept of an agent's split request under an approved epic; the owner's split is not.
11. Under an approved epic, no owner write gives a running lane subtask a new open blocker or the blocked flag.

**Starts and the executor**

12. `StartRun` re-checks ready and both slot counts in its own write. Slots count stored open lane holds only, whatever the holders' stages, and a pending done, blocked or split frees one.
13. `StartRun` records `waited_on`. A run Task is scoped to its subtask: it can claim no sibling and create no card.
14. A lane release pauses by reason: `ended` and `rejected` pause `uam`, `released` and `settled` pause `owner`, and `aborted`, `blocked` and `accepted` pause nothing. An accepted blocked request ends a lane hold.
15. `Next` is pure and table-tested: priority then outline order; the per-epic limit, the global cap and `Starting` reservations; a landing intent lands unless a land call is in flight, and a retry time is respected; an interrupted holder is nudged once, one per provider per pass; an Ended holder is nudged, then retired; a failed first turn with nothing in the lane aborts without a pause; a failure after work probes once the breaker allows and retires after 3 failed nudges; an open breaker starts and nudges nothing on its provider; an owner-cancelled turn is left alone; a landed Task is never cancelled, and a stopped Task still working is.
16. `RunFacts` never returns a root subtask as ready.

**Landing**

17. A landed subtask is exactly one commit on the integration branch, built from what acceptance last tested, with `Uam-Card` and `Uam-Request` trailers. The evidence is the lane's change only.
18. A conflict, an unfinished merge, a stale lane, or a red re-run after the tip moved leaves no pending request and does not move the ref. A timed-out run is filed with `acceptance_could_not_run`.
19. While an open hold carries a landing intent, release, cancel, reject and reconcile refuse or skip the card; only `AcceptLanded` and `ClearLanding` pass.
20. Archiving the holder during the commit phase leaves the ref and the store agreeing. A lost compare-and-swap clears the intent. Boot finishes an intent whether or not the ref moved.
21. Acceptance runs of two lanes in one Project run one at a time by default.
22. Lane Tasks never block the owner's Commit, Pull or Push, and their directories never appear in the recent folders or in an attempt's "uncommitted" comment.
23. Stop pauses `owner`, cancels the holder with a uam reason, archives it, removes the worktree and keeps the branch. Settle refuses keep on a lane hold. An archived lane is committed and removed.

**Revert and Merge**

24. The revert closure comes from the start snapshots; unlinking or relinking after landing changes nothing. It takes landed dependents along, newest first, in one ref update, and refuses while a dependent runs.
25. A revert writes the store before the ref. A failed compare-and-swap undoes the store, and a crash between the two is finished at boot. A conflict writes nothing and names the later cards that touched the files; including them then succeeds.
26. A reverted subtask waits for Resume, then reruns without the change.
27. Reopen without reverting code leaves the integration branch unchanged, keeps the landed sha and the dependents landed, and runs the owner integrity check. "Back to To do" without it is refused on a landed subtask.
28. An approved epic that finishes is merged into `base_ref` with no click, and nothing fires again once `base_ref` has everything the integration branch carries (`hasAll`), after a sync merge of an owner commit too. Sync skips a base that brings nothing new. An owner Revert of merged work merges again. A merge into a checked-out `base_ref` runs hooks, waits with backoff on local changes or a busy git write, and on a conflict stays blocked until Retry merge or a new tip; a `base_ref` not checked out gets only a ref move. Revert and merge run as jobs.

**Restart and migrations**

29. A new Manager on the same stores nudges each interrupted holder once and starts nothing twice.
30. The v4 to v5, v5 to v6 and v6 to v7 migrations run on fixtures, and v7 pauses every approved epic.

**UI**

31. The Approve dialog needs a model, posts ids with revisions and reloads on `stale`. Chips carry words. Confirm and Launch are hidden under approved epics. "Running 2 of 2" shows no spinner.

## Rejected (non-goals)

- Batch create.
- A "plan ready" or `approve` request kind (§9 decision 5).
- Worktrees for root cards or for epics nobody approved.
- Per-epic integration branches (§9 decision 10).
- Revert or merge started by an agent.
- Resuming a turn after a restart. uam nudges the Task instead.
- Resolving revert conflicts inside uam (§9 decision 15).
- A steward Task per epic, as in ADR 0005. The executor is a goroutine.
- An attempt counter. Pausing on a failed attempt bounds relaunches instead.
- A Stop route, store method or release reason of its own, a `holds.workdir` column, and a separate ref namespace for attempts. Stop is the owner's Release, the worktree path derives from the branch, and attempt branches are siblings of the integration branch.
- Making Stop and Retire wait for the land mutex. The sticky landing intent and the commit phase on `context.WithoutCancel` make landing safe against Archive instead.

The three designs, scored 1 to 10 (higher is better; for size, fewer concepts scores higher):

| Criterion | Minimal, shared tree | Isolation first | Executor first (base) |
|---|---|---|---|
| Correctness against R1 to R7 | 6. R6 is unsound under R4: a commit built from edit-tool paths takes a sibling's hunks in shared files, and a revert needs every Task in the repo idle | 8. Complete, but Project-wide reach plus re-approval on every agent write fights ADR 0005 decision 8 | 7. Complete, but per-epic branches break cross-epic dependents, and landing can merge an untested combination |
| Fit with existing code | 9. One column and the existing commit code | 6. Four migrations, a requests rebuild, a revision counter | 7. Mirrors pure reconcile and reuses holds and requests, but rebuilds requests for an approve kind |
| Revert soundness under parallel runs | 3 | 9 | 7 |
| Restart and failure robustness | 5. Interrupted or idle Tasks hold slots until the owner acts | 8 | 9 |
| Size | 9 | 4 | 6 |
| **Total** | **32** | **35** | **36** |

## Deferred

- Queueing cards onto a running Task.
- A per-Project setup command for lanes (§9 decision 9).
- Autopilot for run Tasks (§9 decision 13).
- Reject-to-steer for plans from the Inbox (§9 decision 5).
- The in-repo lanes root (§9 decision 8), used only if Copilot refuses lane directories outside the Project.

## Consequences

- **Fresh checkouts are the largest risk.** A lane has only tracked files: no node_modules, `.env`, generated code or build cache. An agent or acceptance command written for the warm main tree may fail or hit the 10-minute acceptance timeout, and every attempt then ends paused or waiting. Self-sufficient acceptance commands and "Check in a clean checkout" are the mitigations, and S4 and S5 are judged on a Project with dependencies, not only a small Go module.
- **Copilot in a directory outside the Project is unverified.** Trust prompts, permission prompts, and skill or instruction loading in a lane directory are checked when lanes first run (S4), with §9 decision 8's fallback.
- **Land conflicts** on shared files such as registries and route tables go back to the agents, and weaker models may loop until the subtask is paused. The default parallel of 2 and SKILL.md's advice to link subtasks that edit the same files limit this.
- **Landing is serialized per Project.** It holds the land mutex through any acceptance re-run, up to 10 minutes, and lane starts, reverts and merges in that Project wait behind it. A lane start also holds it across Task creation, which takes seconds. With the default acceptance limit of 1, claims queue as well, and a long command can push them past the timeout into `acceptance_busy` retries.
- **The migrations are one-way** (v5, v6, v7). Rolling a deploy back disables the planner, so board.db is backed up first.
- **Running epics hold up a restart that waits for idle Tasks.** The owner pauses running epics and lets attempts drain first. Recovery after an unplanned restart relies on nudges.
- **The owner's repo gains refs and worktrees:** `uam-plan-*` branches, attempt branches that did not land, and `.git/worktrees` entries. Disk use grows with each lane until cleanup.
- **Approval covers edits.** The planning Task can still change an approved card's text before it starts, with no new approval; Approve checks revisions only at approval time. The owner's lever is Pause.
- **An agent could weaken a test to pass**, since `tests_or_build_changed` no longer holds the request and merges are automatic (§5.8). That change would reach the base branch with no human look. The flag is listed in the epic's merge comment, and Revert is the remedy.
- **A merge changes the owner's working tree** when `base_ref` is checked out there. git refuses to overwrite local changes, and the merge retries with backoff, but files the owner has open in an editor change under them when it succeeds. A merge also carries the landed subtasks of other approved epics that have not finished.
- **Proposals added after approval** can be cancelled when their container closes. The `closes_with_proposals` wait shows them, but only to an owner who reads it before accepting.
- **The sticky landing intent** makes owner actions on that card answer `landing` between `MarkLanding` and `AcceptLanded`, and after a crash until the first pass.
- **A provider outage stalls every epic on that provider**, shown as "Waiting for `<provider>`". A turn that fails every time on one subtask looks like a provider failure until three failed nudges retire it.
- **Boot aborts only uam's own lane merges.** An agent's half-finished merge waits for its nudge, and its claim is refused until it finishes the merge.
