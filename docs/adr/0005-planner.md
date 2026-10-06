# ADR 0005: Agentic planner

Status: accepted (2026-09-29). Decided in [#255](https://github.com/RandomCodeSpace/unified-agent-manager/issues/255) and [#248](https://github.com/RandomCodeSpace/unified-agent-manager/issues/248). The map is [#247](https://github.com/RandomCodeSpace/unified-agent-manager/issues/247). Partly superseded by [ADR 0006](0006-planner-execution.md) (2026-10-06); each superseded rule below carries a note.

## Context

uam gets a planner: an agile board of **epics, stories and subtasks** for each git Project.
- Agents decompose the work and carry it out through in-process tools.
- The owner confirms, launches and closes the work; uam accepts a done request when the owner's acceptance command passes and nothing holds it back (decision 5).
- The storage and rules are ported from kb (github.com/RandomCodeSpace/kb, MIT), using SQLite through `modernc.org/sqlite` (no CGO).
- It is not a record of every Task. A uam Task appears on the board only when it holds a subtask or plans under a container.

Naming: **Task** always means a uam conversation. The planner's leaf kind is **subtask** (`KindSubtask`), and nodes are **cards** on a **board**.

## Decision

## Principles

- Agents write the plan, the owner's edits stick, and only the owner closes work: directly, or through the acceptance command they set (decision 5).
- Deterministic rules over judgement. Every stored state has a writer and a defined exit.
- The board holds only work that was planned or started. uam Tasks that never touch it don't appear on it.
- The planner sits behind a Settings switch, off by default. It exists only on Projects with a git repository.

## 1. Model

One table, where a node is a card. It has the ported kb fields (`#seq`, title, desc, prio, due, effort, labels, checklist, blocked flag, comments, blocker links, FTS search, revision) plus:

| Column | Meaning | Written by |
|---|---|---|
| `kind` | `epic`, `story` or `subtask` (the leaf) | create, split |
| `parent_id`, `rank` | Tree position. Kind rank is epic < story < subtask, a parent must outrank its child, and the root may hold any kind | agents (in scope, nodes not started, decision 8), owner |
| `win_condition` | One line saying what done means | agents on nodes not started (decision 8), owner |
| `expires_at` | NULL means **confirmed**. Agent-created nodes start at now + 14 days; the owner's Confirm, launch or attach, accept, restore or status change, or an automatic acceptance (decision 5), sets it to NULL. The owner's planning writes re-arm it to now + 14 days instead (decision 10). *Superseded under approved epics by [ADR 0006](0006-planner-execution.md) §2 items 2 and 19.* | uam |
| `held_by` | The Task holding a leaf. Invariant: `doing ⇔ held_by is set` | launch, claim, release |
| `pinned_sha` | HEAD at the last owner touch | uam |
| `accept_cmd` | Leaf override: NULL inherits the Project default, `''` means none, otherwise a command | **owner only** |
| `paths` | Optional globs used by staleness | **owner only** |
| `cascade_id` | Which cascade cancelled this node | uam |

There is also a `requests` table, which is the inbox. Each row has a node, a Task, a kind (`done`, `cancel`, `blocked`, `split` or `change`), a payload, evidence, a base revision, and a status (pending, accepted, rejected or withdrawn).

Leaf statuses are `planned | todo | doing | done | cancelled`. `planned` means never launched. `todo` means released after an attempt, or marked ready by the owner.

## 2. Derived containers

Epics and stories never store a status. Their status comes from the **confirmed** leaves in their subtree:

| Leaves | Container |
|---|---|
| no confirmed leaves | planned (never done) |
| every confirmed leaf cancelled | cancelled |
| every non-cancelled confirmed leaf done | done |
| any leaf held, or any confirmed leaf done | doing |
| otherwise | planned |

- A container can't reach done while any leaf under it is held or has a pending request.
- Progress is counted rather than weighted. The API's `progress` keeps the confirmed count the status rules read: `done` and `total` are the done and the non-cancelled confirmed leaves, and `proposed` the non-cancelled unconfirmed ones. Proposals are plan items (decision 6), so every view shows done ÷ all non-cancelled leaves, `total + proposed`, and still names the proposals: "0/3 done · 2 proposed".
- When a container reaches done, uam does two things:
  - it cancels the container's remaining unconfirmed, unheld leaves with the comment "closed unconfirmed with #12";
  - it adds a roll-up comment made of the children's close comments.
- There is no force on containers.

## 3. Who may write what

A static actor table (owner or agent) sits in one transition function in `internal/board`.

**Agents, within scope, may:**
- create stories and subtasks;
- plan any node not started, confirmed or not: edit, move within the tree rules, edit the checklist, link and unlink, split, and dismiss proposals, all applied directly, where the last write wins (decision 8);
- on a started subtask (held, doing or done): tick and untick (the holding Task only) and comment. A planning change to it is filed as a `change` or `split` request, one pending per node per Task, with a newer request replacing the older one, and the owner releases it before accepting;
- claim a confirmed leaf, moving it from planned or todo to doing (decision 6);
- file `done`, `cancel`, `blocked` and `split` requests.

**A Task with no scope**, one neither launched from a card nor planning under one (§4), may propose epics at the root (decision 4) and build them out within its own cards (§4, decision 9).

**Agents never:**
- set done, cancelled (superseded by [ADR 0006](0006-planner-execution.md) §2 item 6, which adds `board_delete`) or the blocked flag. uam accepts a done request only when the owner's acceptance command passes and nothing holds it back (decision 5);
- write `accept_cmd` or `paths`, which appear in no tool schema;
- create a story or subtask at the root, or an epic from a Task with a scope, or reparent a node outside their scope;
- restore or purge.

**The owner** may do everything. That includes marking a leaf done directly, with a comment: the finishing guard applies, and `force` exists on leaves only.

**Blocker links** join two cards of one kind under one parent, either or both of which may be a proposal (decisions 6 and 7). A link stays open while its blocker is not terminal, so cancelling the blocker, which expiry and dismissal do, releases it.

## 4. Scope and caps

- **Planning Task:** "Plan with agent" on a container starts a normal Task with a preamble. It creates and edits under that container and holds nothing.
- **Working Task:** its scope is the container it was launched from ("Do whole story"), or the parent of its single launched leaf. It creates siblings and claims leaves only within that scope. *Superseded for run Tasks by [ADR 0006](0006-planner-execution.md) §2 item 5.*
- **Utility scout:** "Suggest stories" on a container follows the same rules. What it writes are unconfirmed nodes, so suggestions and agent decomposition are one mechanism.
- **Own cards:** every Task's scope also covers each card it created that has not started, and everything under it (decision 9). So a Task with no scope builds out the epics it proposes, and no card created by the owner or another Task is in its reach unless it sits under one of the Task's own cards, the Task holds it, or it is under the Task's container.
- **Caps** are counted per Task, and calls made by subagents count against their Task:
  - 20 created nodes (superseded by [ADR 0006](0006-planner-execution.md) §2 item 8: 50 non-cancelled cards);
  - 10 unconfirmed children per container, the root counting as one;
  - 20 comments per card;
  - one hold without a pending request at a time.
- A duplicate normalised title among live siblings is refused. The FTS similarity check is information only.

## 5. Launch, holds and release

- **Launch** is an owner action on a leaf only. It moves the leaf from planned or todo to doing, sets `held_by` to the new Task, and counts as a touch (it confirms and re-pins the leaf). Launching a proposal, or a leaf under one, needs the owner's explicit confirm, which confirms the chain in the launch's transaction (decision 6). The start of a hold records HEAD and `git status` as the evidence baseline. *Superseded under approved epics by [ADR 0006](0006-planner-execution.md) §2 items 1 and 2.*
- **"Do whole story"** launches the first pending confirmed leaf. The preamble carries the story's pending list, and the Task claims further leaves through the tool. *Superseded under approved epics by [ADR 0006](0006-planner-execution.md) §2 item 5.*
- **A hold persists while the Task is Active.** *Superseded for lane holds by [ADR 0006](0006-planner-execution.md) §2 items 10, 13, 16 and 17.* It can end only through one `releaseHold` path:

| Event | Result |
|---|---|
| Request accepted, by the owner or automatically (decision 5) | done |
| Request rejected while the holder is live | Steer sent to the Task; the hold stays |
| Request rejected while the holder is not live | todo, with the reason as a comment |
| Settle | A dialog for each held leaf: keep held (it resumes after Reopen), release to todo, or cancel with a comment. *Superseded for lane holds by [ADR 0006](0006-planner-execution.md) §2 item 16.* |
| Archive or Delete | todo, with the comment "attempt #n ended, uncommitted: …" |
| Owner Release | todo |
| Owner cancel, or a cascade | cancelled; pending requests are withdrawn |

- **Reconciliation** is one pure function, `reconcile(nodes, tasks)`.
  - It runs at boot, after the Task store loads and before any outline write is accepted, and again after every Task transition.
  - It reads the Task store, never the live-session table, so Settled Tasks keep their holds across restarts.
  - When a hold is released on a leaf that is still unconfirmed, `expires_at` is re-armed to now + 14 days.
- Any status change on a leaf withdraws its pending requests. `claim_done` and Accept both require the leaf to be doing and held by the Task that made the request.

## 6. Evidence-backed done

`claim_done` checks, in this order:
1. Open checklist items, the blocked flag, or open blockers → refused, with the list.
2. The resolved acceptance command is non-empty → uam runs it. Any non-zero exit, 127 included, → refused with the output tail.
3. uam can't spawn the shell → the request is filed, flagged "acceptance could not run".
4. No diff and no commits → the request is filed, flagged "no change in tree", with or without a command (decision 5).
5. Otherwise → a done request with evidence rows.

**Evidence rows:**
- **diff:** `numstat` since the hold started. It shows the total, the files this Task touched, and any overlap with other live holds ("overlaps with #13 (Task X)"). It is tagged "tests or build files changed" when the diff touches test, build or CI files.
- **commits:** the log since the hold started.
- **accept:** command hash, HEAD, dirty flag, exit code and output tail.
- **transcript:** the span of the Task's transcript.
- **checklist:** the checklist state.

**The runner:**
- There is one per Project, guarded by a mutex. A claim that waits longer than the acceptance timeout is refused with "acceptance busy, retry". *Superseded by [ADR 0006](0006-planner-execution.md) §2 item 14.*
- It runs `$SHELL -lc` in the Project directory, in its own process group, bound to the Task's context. Archive or Delete kills the run and discards the result.
- Editing a command marks the earlier green rows for that command as stale.

**A passing command** accepts the done request as it is filed when nothing holds it back (decision 5 lists the conditions). Otherwise **the owner** accepts or rejects:
- Accept turns the claim text into the close comment and counts as a touch.
- Reject requires a reason.

**Leaves that are red by design**, such as a leaf that adds a failing test, have two options: fold red and green into one leaf, or have the owner set that leaf's command to `''`. The refusal message says so. Agents may attach a `proposed_accept_cmd` as text; the owner's Apply copies it into the leaf, and it never runs before that.

## 7. Split

- A split turns a leaf into a story with child subtasks.
  - Unticked checklist items become planned children.
  - Ticked items become children with a pending done request that cites the tick.
  - A split never creates done children.
- **Unconfirmed, unheld leaf:** the split applies directly.
- **Started leaf** (held, doing or done): an agent's split is one inbox row, which the owner accepts after releasing the leaf; the owner's own split is refused until then (decision 8). Any other leaf splits directly. Accepting applies the structure and accepts the ticked children together.

## 8. Cancel, restore, purge

- Marking a leaf done, cancelling it and restoring it all require a non-empty comment. Automatic comments (attempt ended, roll-up, expired, imported, closed with parent) are exempt from the caps.
- **Cascade** (owner only) stamps one comment on each non-terminal leaf, "cancelled with #12: …", with a shared `cascade_id`. It releases holds and withdraws requests.
- **Restore** (owner only, comment required) reopens exactly the nodes with that `cascade_id` and confirms each one. A leaf under a cancelled parent can't be reopened on its own. *Superseded under approved epics by [ADR 0006](0006-planner-execution.md) §2 item 19.*
- **The expiry sweep** runs at boot and on each outline write. It cancels expired nodes with an automatic comment. It skips any node that is held, has a pending request, or has a held or confirmed descendant. Swept nodes can be restored.
- **"Purge cancelled"** is the only hard delete, and only the owner can run it.

## 9. Staleness

- Computed for confirmed leaves that are not terminal and not held:
  - **behind N:** how many commits HEAD is ahead of the pin;
  - **diverged:** the pin is no longer an ancestor of HEAD;
  - **files touched:** files changed since the pin that match `paths`.
- It shows as a marker plus a per-Project count. It never creates inbox rows.
- **Utility triage** runs on demand. It returns valid, moot or conflicts, plus one sentence, cached per leaf and HEAD. The matching actions are: re-pin, cancel with a prefilled comment, or add a comment.
- **"Check at HEAD"** runs the acceptance command through the runner.
- **Launching a stale leaf** is allowed. The preamble then carries the log since the pin (up to 50 lines) and the changed files.

## 10. UI

**Three views of one Project's plan,** switchable in place. Selection and filters carry across them.
- **Tree:** the outline. Each container shows its progress, and unconfirmed nodes are collapsed as "+N suggested" under their parent with Confirm and Dismiss. This is where most planning happens.
- **Board:** kanban over leaves (planned, todo, doing, done), with swimlanes by story and a filter by epic. Cancelled leaves are hidden and can be toggled on.
- **Map:** a graph laid out as a tree (epic → story → subtask), with blocker links drawn as secondary edges. Nodes are coloured by derived status and show progress rings. It supports pan and zoom and opens a card on click.

**The rest of the surface:**
- **Inbox:** pending requests (done, cancel, blocked, split and change), showing their flags and evidence. The count folds into Needs you.
- **Card detail:** fields, win condition, checklist, the evidence trail, hold history (attempts), comments, and the owner-only command and paths. The owner ticks, renames (click the text; Enter or leaving the field saves, Esc cancels, empty text is refused), removes and adds checklist items there, also on a card with no checklist yet; each save sends the whole list. An Unassigned or cancelled card shows its checklist read-only.
- **Actions:** Launch, Do whole story, Plan with agent, Suggest stories, Confirm, Release, Cancel (with a comment), Restore (with a comment), Check at HEAD, Triage, and Purge cancelled.
- **The Settle dialog** for held leaves (§5).

**The plan inside a Task** (2026-10-02; it replaces the floating pop-out, its edge tab on every Task, its separate view state and the picture-in-picture window, which kept the plan beside the conversation only by covering it, and threw the owner out of the Task on a card click).
- **Story strip:** one line under a Task's header while its Project has a plan: "Epic › Story · 1/3 done · This task #25 · next #26 · waits for …", with what the card waits for through its story or epic. The Task's card is the subtask it holds, else one it finished (`worked_by`). A Task with no card gets a quiet "Not part of a story · Add to a story". A Project with no plan, or no git, shows no strip. A click opens the Plan panel on the Task's card.
- **Plan panel:** a side panel in the slot Files and Changes use, with a Plan button beside them. It shows "This task works on" (path, card, what it waits for), or for a Task with no card the form that adds it to a story, then an **Outline | Graph** switch.
  - **Outline:** epics › stories › subtasks with progress; the Task's card marked and its story open; a click opens a card's details in place: done when, description, checklist, dependencies at its own level and those inherited through its story or epic, the agents' requests, Edit, Discard for a proposal, and Launch with the confirm step for a subtask. A started subtask shows why its plan is locked.
  - **Graph:** one level of the layered DAG at a time (the Project's epics, an epic's stories, a story's subtasks), with a breadcrumb, a container opening its level, and a banner when the whole level waits through its container. It opens on the Task's story and draws its card outlined; a subtask's details open under it. Plain SVG (shapes and text, no HTML inside), left to right when the layers fit, else top to bottom, scrolling inside itself; the panel widens while it shows, as far as the side-panel width rule allows.
  - The panel has its own view state, so following Tasks never changes the Planner's.
- **Card links stay in the Task:** a transcript's card chip, and every card link inside the Task, open the card in the Plan panel. A card of another Project opens in the Planner.
- **Adding a Task to a story:** the owner picks a story and a new subtask (named after the Task) or one of its subtasks not started yet; the Task then holds it as a launched Task would (§5). Under a proposal it asks first, naming what it confirms.

**Performance and style:**
- One theme.
- No blur.
- Animate transform and opacity only.
- The Map's pan and zoom use a CSS transform.

## 11. kb import

A one-time import reads a **copy** of kb's `kb.db`, which must be at kb schema v11. uam never opens the original file.
- Imported cards become confirmed subtask leaves at the root, with no pin.
- A kb project whose name matches a uam Project imports into that Project. Otherwise the cards go to the **Unassigned** list (`project_id` is empty).
- Unassigned cards are read-only until the owner moves one into a git Project. Moving a card counts as a touch.
- Imported done cards get the automatic close comment "imported from kb".
- Comments and blocker links are copied too.
- Running the import again adds no duplicates, because it keys on kb's card UUID.

## Test plan (invariants from the adversarial pass)

Each item is a store, tool or UI test.

**Acceptance commands**
1. No planner tool schema contains `accept_cmd` or `paths`, and an update carrying either is rejected before any write.
2. An acceptance command runs only if the owner wrote it; no agent write can change what runs.
3. Every non-zero exit refuses the claim. Only a failure to spawn the shell produces a flagged request.
4. At most one acceptance run per Project happens at a time, a claim still waiting past the timeout is refused, and Archiving or Deleting the Task kills its run and records nothing. *Superseded by [ADR 0006](0006-planner-execution.md) §2 item 14 for a Project that raises its acceptance limit.*

**Holds and requests**

5. `doing ⇔ held_by` holds after every write.
6. After any Task transition (Settle, Reopen, Archive, Delete, or boot with the Task missing), no leaf is held by an Archived, Deleted or absent Task.
7. A restart changes no holds of Active or Settled Tasks and writes no comments. *Superseded by [ADR 0006](0006-planner-execution.md) §2 item 15.*
8. `claim_done` and Accept refuse when the leaf isn't doing or isn't held by the Task making the request.
9. Any status change on a leaf withdraws its pending requests.

**Agent limits**

10. For the agent actor, moving to done or cancelled, setting the blocked flag, restoring and purging are all refused.
11. A split never produces a done child, and an agent's split of a started leaf is exactly one pending request.

**Containers**

12. Derived status and progress ignore unconfirmed leaves, except that a hold makes the container doing, and a container with no confirmed leaves is never done.
13. A container can't become done while a leaf under it is held or has a pending request. When it does become done, its unconfirmed, unheld leaves are cancelled with the automatic comment.

**Sweep, cascade and links**

14. The sweep never cancels a held node, a node with a pending request, or an ancestor of a held or confirmed node.
15. Restore reopens exactly one cascade's nodes and confirms them, and a restore without a comment is refused.
16. A cancelled blocker doesn't block, a link between cards of different kinds or parents is refused, and links to proposals are allowed (decisions 6 and 7).

**Caps, staleness and import**

17. Caps count per Task, and calls from subagents count against their Task.
18. The duplicate-title refusal ignores cancelled siblings.
19. Staleness is not computed for held leaves.
20. Imported cards are confirmed with no pin, and imported done cards carry the automatic close comment.

**UI**

21. A Task's Plan panel has its own view state: following Tasks, and opening cards in the panel, never change the Planner's Board, selection or filters. A card link inside a Task never leaves the Task.

## Rejected (non-goals)

- Keeping the plan as Markdown in the repo.
- A card as a bundle of pointers with generated text.
- Weighted consolidation of proposals.
- A long-lived steward Task per epic.
- A per-Project rule table.
- Parsing the agent's shell commands for evidence.
- Per-Task git worktrees: overlap between Tasks is flagged, not prevented. Running acceptance in a temporary worktree is prototype-only, and only if overlap proves painful. *Superseded for approved epics by [ADR 0006](0006-planner-execution.md) §2 item 3.*
- Tracking every uam Task: no automatic card per Task, and no `#12` composer references in this effort.
- An auto-accept switch per Project: decision 5 accepts a done request automatically under its conditions, with no switch.

## Deferred

- "Start next card". *Superseded by [ADR 0006](0006-planner-execution.md) §2 item 4.*
- Queueing cards onto a running Task.
- Forge import (#248 item 8).

## 12. Packages

- **`internal/board`** owns the model and every rule in §1–§9:
  - the SQLite store: WAL, a single connection, and kb's busy retry;
  - the actor transition table, derivation, holds and `ReleaseHold`;
  - `Reconcile`, requests, split, cascade, restore, sweep, purge, search, the duplicate check and caps.
  - It knows nothing about HTTP, Copilot or `sessions.json`. Clock and ID sources are injectable for tests.
- **`internal/web/board*.go`** integrates the planner with the Manager:
  - the Settings switch, API handlers and the SSE frame;
  - reconcile calls at Start and on every Task transition;
  - launch and plan, which create a Task;
  - the Settle hold decisions;
  - the evidence runner (`board_evidence.go`), staleness (`board_stale.go`), the kb import (`board_import.go`), agent tools (`board_tools.go`) and Utility jobs (`board_ai.go`).
  - It never holds `m.mu` or a Task's `op` lock across SQL or git.
- **`internal/agentapi`** carries the host-tool seam (#249). A call identifies its Task, plus the agent ID when a subagent made it.
- **`internal/adapter/copilot/hosttools.go`** registers host tools into Task sessions and Utility sessions.
- **`web/src`** holds:
  - `api.ts` types and calls;
  - the `state.ts` reducer for the `board` frame;
  - `components/planner/*` (Tree, Board, Map, Inbox, CardDetail, picture-in-picture);
  - the matching routes in `web/src/mock`.

## 13. Storage

- `board.db` lives beside `sessions.json`, in `filepath.Dir(store.DefaultPath())`, with mode 0600. It is created the first time the Settings switch is turned on.
- **Tables:**
  - `cards`: kb's fields plus §1's columns;
  - `labels`, `comments` and `links`;
  - `requests` (§1);
  - `holds`: the attempt history, with id, card, Task, started_at, baseline HEAD, baseline porcelain status, ended_at and end reason;
  - `project_settings`: the Project ID and the default `accept_cmd`;
  - one revision counter per Project, maintained in the same transaction as each write.
- `#seq` is global to the board and is never reused.
- Comment authors are `owner`, `task:<id>` or `uam`. The last is for automatic comments, which carry an `automatic` flag.

## 14. HTTP API

- **Access:** every route sits behind uam's existing sign-in, cross-origin and JSON checks.
  - If the switch is off, the answer is 409 `{"error","code":"planner_off"}`.
  - A Project that is not a git repository gets 409 with the code `no_git`.
- **Errors:** they carry `{"error": "...", "code": "..."}`. The codes are:
  - `guard_open_items`, `guard_blockers` and `guard_blocked`;
  - `not_held`;
  - `holds_undecided`;
  - `unconfirmed_parent`, `read_only` (Unassigned) and `invalid`.
- `{ref}` is a card UUID or its `#seq` (with or without `#`).

**Card**

```json
{
  "id": "uuid", "seq": 12, "project_id": "uuid or empty for Unassigned",
  "kind": "epic|story|subtask", "parent_id": "uuid or null", "rank": 0,
  "title": "", "desc": "markdown", "win_condition": "",
  "status": "planned|todo|doing|done|cancelled",
  "progress": {"done": 3, "total": 5, "proposed": 2},
  "prio": 3, "due": "YYYY-MM-DD", "effort": "S|M|L", "labels": [],
  "checklist": [{"text": "", "done": false}], "blocked": false,
  "blocked_by": ["uuid"], "blocks": ["uuid"],
  "confirmed": true, "expires_at": "RFC3339",
  "held_by": "task uuid", "worked_by": "task uuid", "pinned_sha": "",
  "accept_cmd": null, "paths": [],
  "stale": {"behind": 3, "diverged": false, "files": ["a.go"]},
  "pending_requests": 1, "revision": 7,
  "created_at": "RFC3339", "updated_at": "RFC3339", "moved_at": "RFC3339"
}
```

- `status` is derived for containers.
- `progress` is sent for containers only.
- `expires_at` is sent only while the card is unconfirmed.
- `worked_by` is the Task of the subtask's latest attempt; it stays after the attempt ends.
- `accept_cmd` is `null` (inherit), `""` (none) or a command.
- `stale` is sent only when it has been computed (§9).

**Request**

```json
{
  "id": "uuid", "card_id": "uuid", "task_id": "uuid", "agent_id": "",
  "kind": "done|cancel|blocked|split|change",
  "comment": "", "payload": {}, "evidence": {},
  "flags": ["acceptance_could_not_run", "no_change_in_tree", "tests_or_build_changed", "overlap"],
  "base_revision": 7, "status": "pending|accepted|rejected|withdrawn",
  "created_at": "RFC3339", "decided_at": "RFC3339", "decision_comment": "", "decided_by": "owner|uam"
}
```

**Evidence** (a done request)

```json
{
  "baseline": {"head": "sha", "dirty": ["path"]},
  "diff": {"added": 10, "deleted": 2, "files": [{"path": "", "added": 1, "deleted": 0, "by_task": true, "overlap": {"card": 13, "task_id": "uuid"}}]},
  "commits": [{"sha": "", "subject": ""}],
  "accept": {"cmd": "", "cmd_hash": "", "head": "", "dirty": false, "exit": 0, "tail": "", "ran_at": "RFC3339", "stale": false},
  "transcript": {"task_id": "uuid", "from_item": "", "to_item": ""},
  "checklist": {"done": 3, "total": 3}
}
```

**Routes**

| Route | Does |
|---|---|
| `GET /api/board?project_id=<id or unassigned>` | `{cards, requests (pending), revision}` for one Project |
| `GET /api/board/projects/{id}` | `{accept_cmd, git: "" or no_git reason}` |
| `PATCH /api/board/projects/{id}` | `{accept_cmd}` sets the Project default |
| `GET /api/board/cards/{ref}` | `{card, comments, requests, holds}` |
| `POST /api/board/cards` | Owner create; the card is confirmed, or a proposal under a proposal (decision 10): `{project_id, kind, parent_id, title, desc, win_condition, prio, effort, due, labels, checklist}` |
| `PATCH /api/board/cards/{ref}` | Owner edit of any field, including `accept_cmd`, `paths` and `project_id` (moving a card out of Unassigned); the edit confirms no proposal (decision 10) |
| `POST /api/board/cards/{ref}/confirm` | Confirm |
| `POST /api/board/cards/{ref}/dismiss` | Cancel an unconfirmed node, with the automatic comment "dismissed" |
| `POST /api/board/cards/{ref}/move` | `{parent_id, rank}` |
| `POST /api/board/cards/{ref}/status` | `{status: done|cancelled|todo, comment, force}` — owner-direct on a subtask; `cancelled` on a container is the cascade |
| `POST /api/board/cards/{ref}/restore` | `{comment}` |
| `POST /api/board/cards/{ref}/split` | `{children: [{title, win_condition}]}` (owner split) |
| `POST /api/board/cards/{ref}/comments` | `{body}` |
| `POST /api/board/links` | `{blocker, blocked}` |
| `DELETE /api/board/links?blocker=&blocked=` | Removes a blocker link |
| `POST /api/board/cards/{ref}/launch` | `{provider, model, effort, mode, context_size, brief, confirm}` (all optional; the Task defaults in Settings otherwise) → 201 `{card, session}`. The UI's Launch and Do whole story ask for the model, effort, context size and mode, starting from the New task defaults, and an optional brief. On a subtask it launches that subtask; on a container it is "Do whole story" |
| `POST /api/board/cards/{ref}/attach` | `{task_id, title, confirm}` → 200 card. Owner only: the existing Task holds the subtask `ref` (not started), or a new subtask titled `title` (else the Task's name) under the story or epic `ref`, as a launch's would: same scope, baseline and confirm step (`unconfirmed` without `confirm`). The Task must be the card's Project's and not archived, and may hold one subtask; no prompt is sent |
| `POST /api/board/cards/{ref}/plan` | `{provider, model, effort, mode, context_size, brief}` (all optional, as for launch) → 201 `{session}`. A planning Task scoped to the container. The UI's Plan with agent asks for the same fields as Launch and a brief; Suggest runs on the Utility model and asks for no model |
| `POST /api/board/cards/{ref}/release` | `{comment}` |
| `POST /api/board/cards/{ref}/check` | Check at HEAD → `{accept}` |
| `POST /api/board/cards/{ref}/triage` | `{verdict: valid|moot|conflicts, sentence, head}` |
| `POST /api/board/cards/{ref}/suggest` | `{brief, document, max}` → 202 `{job_id}` |
| `POST /api/board/requests/{id}/accept` | `{comment}` |
| `POST /api/board/requests/{id}/reject` | `{reason}` |
| `POST /api/board/purge` | `{project_id}` |
| `POST /api/board/import` | `{dir}` → `{imported, updated, unassigned, comments, links, skipped: [{id, reason}]}` |

**Launch and plan** create the Task through the existing create path, set the hold (launch only) in the same request, and send the preamble as the Task's first prompt. The Task is named and titled after its card, "#12 Title" ("Plan #12 Title" for a planning Task), in uam and in the provider before the preamble goes, so neither the provider nor a title job titles it from the preamble.

The preamble is deterministic. It contains:
- the card's path with `#seq` (epic › story › subtask);
- the win condition, description and checklist;
- for "Do whole story", the pending list;
- for a stale card, the log since the pin (up to 50 lines) and the changed files;
- the owner's brief, when the launch or plan gave one;
- a short statement of the rules: use the board tools, finish with a `done` request, and never mark anything done yourself.

**Settle:** `POST /api/sessions/{id}/settle` takes an optional body, `{"holds": {"<card id>": {"action": "keep|release|cancel", "comment": ""}}}`. If the Task holds subtasks and the body doesn't decide each one, the answer is 409 `holds_undecided` with the held cards, and the UI shows the Settle dialog (§5).

**Reject:** rejecting a request while its Task is Active sends the reason to that Task through its normal send path (a steer while a turn runs). Otherwise the reason becomes a comment and the hold is released. The reply is the request plus `steered`, true only when the reason reached the Task; when it is false and the subtask is still held, the UI offers Release.

## 15. Live updates

- After each committed write, one `board` frame goes on the existing `/api/events` stream, broadcast to everyone: `{seq, project_id, revision, cards: [Card], removed: [id], requests: [Request]}`.
- A planner view loads `GET /api/board` when it opens and applies frames whose revision is higher than the one it loaded. If there is a gap in revisions, it fetches again.
- Utility jobs also send `board_job` frames: `{seq, job_id, card_id, status: running|done|failed, error}`.

## 16. Agent tools (#253)

- The tools are registered into Active Tasks of git Projects while the switch is on.
- `ref` accepts a UUID or `#seq`.
- Each result is `{text, card}`, and the card renders as a transcript chip.

| Tool | Parameters |
|---|---|
| `board_get` | `{ref}` — for a container, also the pending subtasks depth-first, blocked ones last |
| `board_list` | `{query, status, kind, parent}` |
| `board_create` | `{kind: epic|story|subtask, parent, title, desc, win_condition, prio, effort, labels, checklist}` — an epic takes no `parent` |
| `board_edit` | `{ref, title, desc, win_condition, prio, effort, labels, parent, rank}` — applies directly on a card not started; on a started one it becomes a `change` request |
| `board_checklist` | `{ref, tick: [index], untick: [index], add: [text]}` |
| `board_comment` | `{ref, body}` |
| `board_link` | `{blocker, blocked}` — one kind under one parent; either may be a proposal, neither may be started |
| `board_unlink` | `{ref, blocker}` — removes a link in scope; neither end may be started |
| `board_dismiss` | `{ref}` — cancels a proposal in scope and everything under it, restorable by the owner. *Replaced by `board_delete` in [ADR 0006](0006-planner-execution.md) §2 item 6.* |
| `board_claim` | `{ref}` |
| `board_split` | `{ref, children: [{title, win_condition}]}` |
| `board_request` | `{ref, kind: done|cancel|blocked, comment, blocker}` |

No schema has `accept_cmd`, `paths`, `status`, `blocked`, `force`, restore or purge.

## 17. Settings

- `planner` (bool, off by default) in `store.WebSettings` and `web.Settings`, set with `PATCH /api/settings {"planner": true}`.
- The Settings page shows:
  - the switch, and the reason it can't be turned on (no git binary);
  - the kb import (a source directory, then the report);
  - the Utility model in use.

## 18. Utility jobs (#254)

- **Suggest stories and split a document:** a store-less Utility session.
  - It has a replaced system message and no built-in tools, and every permission is rejected.
  - Its only host tools are `board_create`, `board_edit`, `board_get` and `board_list`, scoped to the container.
  - What it writes are unconfirmed nodes.
- **Triage** writes nothing. It is cached per card and HEAD.
- Utility sessions use the provider's Utility model from Settings (`title_model`).

## Consequences

- uam gains its first database and its first large dependency (`modernc.org/sqlite`, BSD-3-Clause, pure Go). The binary grows. The size change is recorded in the port PR.
- The planner is invisible until it is switched on, and it is unavailable on Projects without git.
- Task transitions now call `reconcile`, and Settle can answer 409 `holds_undecided`.

## Implementation amendments (2026-09-29)

These record how `internal/board` reads this contract, plus five later decisions. Where they differ from a section above, they win.

**Readings and deviations**

- **Stored container status:** epics and stories store only `planned` or `cancelled`. Cancelled is their own state (dismissed, swept or cascaded); every other status is derived (§2).
- **Derivation order:** cancelled itself → any leaf held: doing → no confirmed leaves: planned → every confirmed leaf cancelled: cancelled → every live confirmed leaf done: done, or doing while any leaf has a pending request → any confirmed leaf done: doing → otherwise planned.
- **`cascade_id` on every cancel:** a single-leaf cancel, each swept subtree and each close-on-done get one too, so Restore always has a set to reopen.
- **Replace rule per kind:** one pending request per card, Task and kind. A newer request replaces the older one for every kind, not only `change`.
- **Accepting `blocked`** links the named blocker if there is one, otherwise it sets the flag; the hold stays. **Accepting `cancel`** runs the cancel or cascade.
- **Not touches:** owner Release, owner cancel and accepting a cancel request don't confirm or re-pin anything. Releasing an unconfirmed subtask re-arms its expiry (§5). The owner's planning writes confirm no proposal either (decision 10).
- **One-hold cap:** only a pending `done`, `blocked` or `split` request exempts a hold; a `change` or `cancel` request doesn't (§4).
- **Cascades** skip containers that are already done. When a container reaches done, its own pending requests are withdrawn.
- **Split done requests:** the done requests a split files for ticked items need no hold to be accepted. An owner's split files them with an empty Task ID.
- **The sweep** also skips a subtree with a pending request anywhere in it, so inbox rows are never withdrawn silently.
- **Reconcile** also takes `asOf`, the time the caller read its Task snapshot, and leaves alone any hold that started at or after it. It takes each Project's uncommitted paths too, for the "uncommitted: …" comment. It treats an unknown stage as ended and does not sweep.
- **`scopes` table** (added to §13): one row per Task, holding its Project, its container and whether it is planning or working. Launch and Start planning write it, and §4 is enforced from it.
- **Extra error codes:** `not_found`, `forbidden`, `limit`, `duplicate`, `acceptance_busy` and `acceptance_failed`, alongside §14's. `unconfirmed_parent` is gone (decision 2).
- **Evidence (§6):** it is gathered before the acceptance run. A hold's baseline also keeps a blob hash for each dirty path (`holds.baseline_blobs`), so a path dirty at the start counts only if its content changed, marked `pre_dirty` on its file row. When the baseline commit no longer exists, the done request is filed with the flag `baseline_missing`, the touched files compared with HEAD, and no commits.
- **Links:** both ends must be in one Project. Agents unlink in scope too, and neither end of a new link or an unlink may be started (decisions 7 and 8).
- **Cancelled checks** use the card's own stored status. Nothing is created or moved under a cancelled card or anything below one, nothing is edited on a cancelled card until it is restored, and cancelled siblings don't count in the duplicate-title check. Ready, Launch and Claim refuse a subtask with a cancelled ancestor, as Restore does (§8).
- **Restore** reopens a leaf as todo if it was ever held, otherwise as planned, and runs the duplicate-title check.
- **Limits:** Purge deletes only subtrees that are entirely cancelled. A card moves between Projects only out of Unassigned.
- **Import (§11):** any source schema other than v11 is refused with `import_schema`, and a source that keeps changing while it is copied with `import_busy`. Imports are exempt from the duplicate-title rule, because the source allows repeated titles and refusing them would drop cards. An `import_refs` table maps each source UUID to its card, with the state last imported and the highest comment copied. Running the import again applies only the fields the source changed since the last run, so the owner's edits survive; it copies only new comments, makes each source link once both cards share a Project, removes links the source dropped while leaving the owner's unlinks alone, and moves a card still in Unassigned once its source project matches. A change aimed at a held card, a card cancelled in uam, or a status the owner couldn't set directly is skipped, reported and retried on the next run. A purged card is never recreated. Only git Projects match, and a name shared by several Projects matches none.
- **Agent tool parameters (§16):** `board_list` filters with `state`, and `board_link` takes `{ref, blocker}`, where `ref` is the blocked card, because no schema may have a `status` or `blocked` key. `board_request` also takes `proposed_accept_cmd`, for `done` only.
- **Agent tool results (§16):** each result is compact JSON, `{text, code, refs, card}`. A refusal carries `code` and `refs`, such as the open checklist items; `card` (`id, seq, kind, title, status`) is the card the call was about. The adapter records only this text, so the transcript chip is rebuilt from it after a reload. A result stays under 32 KiB, well under the 64 KiB the transcript records whole: `board_get` cuts the description and comment bodies and shows at most 20 cards per list, saying how many more there are, and a longer text is cut at the end.
- **Agent tool registration (§16):** the tools are added when a Task's conversation opens. When the switch or the Project's git changes while the conversation is open, the next prompt sent to the Task reopens it with the current tools. Only a prompt does this: slash commands, queued prompts and steers into a running turn never reopen it. The reopen waits while anything runs or waits in the conversation: a turn, a pending request, a queued prompt, a subagent that is running or idle, a background task or an active objective. Until then the prompt goes to the open conversation, which keeps the tools it opened with. The transcript shown stays in place across the reopen. A reopen that fails fails the prompt, as it does after a restart. Every call checks the switch and the Project's git again. A call runs only while its Task is active. Settling or archiving the Task ends its calls in progress before its holds are decided, and a hold such a call made anyway is released.
- **Evidence transcript (§14):** `transcript.partial` is set when the transcript uam holds does not reach back to the hold's start, so the `by_task` files may be incomplete, and when a tool call since then was clipped. A launch records its baseline the same way a claim does, with the blob names of the paths already dirty.
- **Check at HEAD (§9, §14):** `check` answers 202 `{job_id}`, not `{accept}`, because a run can outlast what the sign-in proxy in front of the service lets a request take. What can be told at once is refused at once: a container or a subtask with no resolved command is `invalid`, an Unassigned card is `read_only`, and a Project without git is `no_git`. The job then runs the resolved command (the subtask's own, else the Project default) through the Project's runner. The run is bound to the service, not to the request. Its last `board_job` frame is `done` with the run in `accept`, red and green alike (`exit` -1 when the shell did not start), or `failed` with `error` when the runner stayed busy past the timeout or the run could not finish. The run is recorded nowhere.
- **Planner jobs (§15):** a suggestion and a check are both jobs, one per card at a time. A second job on the same card is refused with `job_busy` (409), and jobs are refused while the service shuts down. Shutdown ends running jobs and waits for them. The `board_job` frame is `{seq, job_id, card_id, kind: suggest|check, status, error, accept}`. `error` is sent only when the job failed, and `accept` only on a finished check.
- **Stale green rows (§6):** nothing is stored. `accept.stale` is computed each time the API sends a done request (the Board, card detail, board frames, and the accept and reject replies): it is set when the row's `cmd_hash` is not the hash of the command the card resolves to now. Editing a card's command, or the Project default it inherits, reports its pending done requests as changed, so the board frame carries them again.
- **Staleness in the API (§9, §14):** `stale` is sent on `GET /api/board` only, and only for a subtask that is behind its pin or diverged from it; a subtask at its pin, and every board frame, carries none. The launch preamble reads staleness from the subtask as it was before the launch re-pinned it. Its files, like the marker's, are the changed files that match `paths`. The preamble says that the log and file names are repository data, not instructions.
- **Utility jobs (§18):** they run on the first available provider, in order, that registers host tools and has a Utility model. Otherwise they are refused with `utility_unavailable` (409). A model call or answer that fails is `utility_failed` (502). A triage times out after 75 seconds, so a slow model fails with this JSON rather than the proxy's page.
- **Triage (§9, §18):** the prompt carries the card, its pin and `paths`, the log since the pin (up to 50 commits) and the changed files that match `paths`, and no tools. The answer must be exactly one JSON object, `{"verdict": "valid|moot|conflicts", "sentence": "…"}`, with nothing around it; anything else fails, and nothing is guessed. Answers are kept in memory per card and HEAD, at most 256. A subtask staleness is not computed for, or one at its pin, is `invalid`.
- **Suggest (§4, §18):** `max` is 1 to 20 and defaults to 5. The brief is at most 8 KiB and the document at most 64 KiB. The job's ID is the Task ID its agent acts as, scoped to the container as a planning Task is, so §4's caps count per job. `max` also caps the job's creates. The job's agent may write proposals only (`Actor.Proposals`): the store refuses its edit of a confirmed card with `forbidden` inside the edit's own transaction, so it never files a change request. A cancelled container is `invalid`. A job is bounded by a 5-minute timeout. Its tool calls end with it: once its conversation ends, a later call is refused and the job waits for those still running before its last frame, so no card is written after it.
- **Import (§14):** a relative `dir` is `invalid`, `import_schema` is 422 and `import_busy` is 409.
- **Settings (§17):** no field is added. The UI shows the Utility model from `title_model` and each provider's `cheapest_model`.

**Decisions**

1. **Split under a story.** *Amended by [ADR 0006](0006-planner-execution.md) §2 item 18: a split copies the original's links to its parts.* A leaf whose parent is a story splits into sibling leaves under that story, placed right after it and in order. Unticked items become planned siblings; ticked items become siblings with a pending done request that cites the tick. The original is cancelled with the automatic comment "split into #a, #b, …" and its own `cascade_id`, so Restore brings it back and leaves the siblings. A leaf not started splits directly; an agent's split of a started one files one split request, which the owner accepts, applying everything at once, after releasing the leaf, so no hold moves (decision 8). Under an epic or at the root, §7 is unchanged: the leaf becomes a story.
2. **An owner touch confirms ancestors.** *Superseded under approved epics by [ADR 0006](0006-planner-execution.md) §2 items 2 and 19.* Any owner touch on a card (launch or attach, accept, restore, Confirm or a status change) also confirms and re-pins each unconfirmed ancestor in the same transaction. This replaces the `unconfirmed_parent` refusal. So launching a leaf under an agent-suggested story confirms the story, and the sweep can't expire it. Since decision 10 the owner's planning writes (save, create, move, rank, checklist, split, link) are not touches.
3. **Board revisions in the snapshot** (added to §15). While the planner is on, the `snapshot` frame carries `boards`: each Project's board revision, plus `""` for Unassigned. It is omitted while the planner is off. A client reloads only the boards it holds an older revision of.
4. **Agents propose epics** (2026-09-30; changes §3, §4 and §16). `board_create` also takes kind `epic` with no parent, from a Task with no scope: one that was neither launched from a card nor started with Plan with agent. Such a Task sees the whole Project board (§16) and before this decision could write nothing. Now it may propose epics at the root; since decision 9 it also plans under them. A Task with a scope writes only within its container, so a planning Task, a working Task (one launched on a root subtask included) and a Utility job propose no epics. An agent's epic is a proposal like any other: it expires 14 days after creation unless the owner confirms it, it counts toward the 20 created cards, the root counts as its container for the 10 unconfirmed children, and a duplicate live title at the root is refused. The owner confirms or dismisses it in the Tree, where root proposals fold into "+N suggested" at the root, and can confirm or cancel it from the card panel; the epic filter shows a proposed epic too, and lists no cancelled epic except the one it is set to, so expired proposals don't linger there. Stories and subtasks still need a parent, and an epic under any card is refused by the kind rule (`invalid`), as it is for the owner.
5. **A done request with a green acceptance command is accepted automatically** (2026-09-30; changes §1, §3, §5, §6, §10 and §14; superseded for run subtasks by [ADR 0006](0006-planner-execution.md) §2 items 9 and 11). An agent files `board_request` kind `done` on the subtask it holds, and uam accepts it as it files it, in the same transaction and through the owner's Accept path, when all of these hold: the finishing guard passes; a resolved acceptance command exists (the subtask's `accept_cmd`, else the Project default, both written by the owner only; a blank command is none, since commands are stored trimmed and one stored blank earlier resolves to none); that command exits 0; the request carries no flag (`no_change_in_tree`, no diff and no commits since the hold's baseline, is now raised whether or not a command exists, §6); the subtask still resolves to the command that ran; and accepting it would not close a container that still has live unconfirmed subtasks, which closing cancels (§2) and which may be the Task's own follow-up proposals or the owner's unreviewed suggestions. The store checks the flags, the command and the containers in the filing transaction. The accepted subtask is done and its hold ends as accepted. It and its unconfirmed ancestors are confirmed but not re-pinned, since no owner HEAD is known; (§1: an automatic acceptance also sets `expires_at` to NULL). Since decision 6 a held subtask and its ancestors are already confirmed. Its other pending requests are withdrawn, and its story and epic roll up as on any accept. The claim text is the close comment, and uam adds the automatic comment "Accepted automatically: the acceptance command passed". The request records `decided_by: uam` and keeps its evidence for review; the owner's decisions record `owner`, and decisions stored before `decided_by` existed are backfilled as the owner's, so the owner's own acceptances stay distinguishable. Otherwise the request waits in the Inbox for the owner, and the store says why: no command is set, the shell could not start, a flag is raised, the command changed during the run, or accepting it would close containers with proposals. A non-zero exit still refuses the claim. Cancel, blocked, split and change requests are never accepted automatically, and a `proposed_accept_cmd` never runs and never counts. The `board_request` result says the subtask is done, or why the request waits, naming its flags and, for containers, every proposal closing would cancel. The owner can move a done subtask back to To do from the card panel or a card's menu (`status: todo`, with an optional comment); the action is hidden under a cancelled card, where the store refuses it until that card is restored.
6. **Confirm gates execution, not planning** (2026-10-02; changes §3, §5, §7 and §14; superseded under approved epics by [ADR 0006](0006-planner-execution.md) §2 items 1 and 2). Agents and the owner link proposals, confirmed or not, so a plan's order is mapped before the owner confirms it. Nothing agentic runs on a proposal or under one: every hold starts in one place, which refuses with `unconfirmed` (409) and the proposals as `refs`, so Launch, Do whole story and Claim need the card and all its ancestors confirmed. Launch takes `confirm` in its body: without it a launch that would confirm proposals is refused before any Task is created; with it the card and its unconfirmed ancestors are confirmed in the launch's transaction. The UI asks first, listing what launching confirms and the proposals the card still waits on, which stay proposals and keep blocking. Blockers never stop a launch or a claim; they stop finishing (§6) and order Do whole story. Plan with agent and Suggest still run on proposals, since they only plan. Expiry and Dismiss cancel cards, which releases their links; Purge deletes the links with the cards. A split no longer moves a hold.
7. **One level per link** (2026-10-02; changes §3, §10 and test 16). A link joins two cards of one kind under one parent: epics with epics, stories with stories in one epic, subtasks with subtasks in one story or epic, and root stories or subtasks with root siblings of their kind. Other links are refused with `invalid`, naming the containers to link instead. Cycles are refused within each level. A card waits on its own open blockers and on each ancestor's, so a subtask in a story blocked by another story is held back with it: the finishing guard names those as "#n (via its story #s)", and the UI shows them the same way. Moving a linked card to another parent, or splitting it into a story, is refused until its links are removed. Links made before this rule are kept, honoured, shown and can be removed; only new ones are refused. A Task with no scope may link the epics it proposed, so several agent-proposed epics can be ordered (decision 9 covers this now).
8. **A started card keeps its plan** (2026-10-02; changes §3, §7, §16 and §18; narrowed by [ADR 0006](0006-planner-execution.md) §2 items 6, 7 and 13). Until a subtask starts, agents in scope plan it fully and directly, confirmed or not: edit, move, checklist, link, unlink, split and dismiss, within the tree and link rules; owner-only fields and lifecycle actions stay the owner's. An agent's move of a confirmed card under a proposal is still a change request, since confirmed never sits under unconfirmed. A started subtask, held, doing or done, is locked for agents and the owner alike: no edit to its title, description, win condition, priority, due date, effort, labels, checklist text, place or parent, no link or unlink touching it, and no split. Refusals are `in_progress` (409), "Release #n first" (or "Move #n back to To do first" when done). Still allowed: ticks by the holding Task and the owner, comments, requests (an agent's planning change becomes a request the owner accepts after Release), and the owner's Release, cancel, done and back to To do. Going back to To do keeps pending change and split requests. A container with a started subtask stays plannable, but moving it while a subtask under it is started, or dismissing it while one is held, is refused; the owner may still cancel it, which releases the holds (§8). A blocked request on a started card can't name a blocker; the comment names it.
9. **A Task plans the cards it created** (2026-10-02; changes §3, §4 and §16; its cap superseded by [ADR 0006](0006-planner-execution.md) §2 item 8). A Task's scope covers, besides its container and its held subtask, each card it created that has not started and everything under it. A Task with no scope can so turn one request into a whole plan: epics, stories and subtasks under them, and links at each level, without the owner running Plan with agent per epic. Nothing else widens: the caps (20 created cards, 10 unconfirmed children per container), the lock on started cards (decision 8), the execution gate (it holds nothing and claims no proposal, decision 6), owner-only fields and actions, and the Project boundary stay. Cards created by the owner or another Task, confirmed or not, stay out of its reach unless they sit under one of its own cards: an owner's card added under an agent's epic is part of that plan, as it is for a Task planning under the epic. A confirmed card the Task created stays plannable until it starts, since confirmation gates work, not planning.
10. **The owner's planning confirms nothing** (2026-10-02; changes §1, §14 and decision 2; superseded under approved epics by [ADR 0006](0006-planner-execution.md) §2 item 2). Confirmation happens only at execution: Launch, Do whole story and attaching a Task, each with its confirm step (decision 6), and the explicit Confirm. The owner's edit, move, rank, checklist change, split and link on a proposal leave it a proposal, and so do its proposed parents. An owner's new card under a proposal is a proposal too, as are the parts of an owner's split of a proposal, so a confirmed card still never sits under a proposal. Each of those writes except a link (which changes neither card's fields) re-arms the expiry of the proposal it touches and of its proposed parents to now + 14 days, so a proposal the owner is working on never lapses under them; an untouched one still expires 14 days after its last owner write or its creation. On a confirmed card these writes re-pin it to the owner's HEAD as before (§1). Moving a confirmed card under a proposal, directly or by accepting an agent's change request, still confirms that proposal and its proposed parents, since a confirmed card never sits under a proposal; the Move dialog says so. Owner status changes, accept and restore still confirm (decision 2).

## Amendment: `uam_create_task` (2026-09-30)

The host-tool seam also carries one tool outside the planner. `uam_create_task` starts a new Task, with a first message, in an existing Project, and needs no approval.

- **Registration.** Every Task's conversation gets it, after the planner tools when it has them, whatever the planner switch says. A Task it started (`spawned_by`) never gets it, so the chain is one level deep. The tool set a conversation opened with is compared with the Task's current one only for the planner tools (§16 amendment): `uam_create_task` never changes for a Task, so it never causes a reopen.
- **Arguments.** `{project, prompt, name, model}`. `project` is a Project's ID or exact name; a shared name is refused with the matching IDs, and no Project is created. The prompt is 1 to 16 KiB with no control characters but newline and tab, and the name is up to 120 characters, as for a Task. The model is checked as `POST /api/sessions` checks it; without one, the Task gets the model the browser's New task picks from the Task defaults. Effort and context size stay at their defaults.
- **Effect.** The call is checked at once. The Task is then created and sent its prompt in the background, and the call waits for that up to `viewWait` (15 seconds), the bound a viewer waits for an open. A create that fails in that time fails the call with its error. Otherwise the result gives the new Task's ID, name and Project, and says it runs on its own; when the conversation is still opening, it says so, and a failure after that is only logged. The Task starts in safe mode; the owner can switch it later.
- **Limits.** A call runs only while its Task is Active (`startCall`). One Task can start at most 5 Tasks. They are counted over the stored records whose `spawned_by` is that Task, in every stage, plus the creates still running. Deleting a Task removes its record and frees its place. A subagent's calls count for its Task. A create that finishes after Shutdown began writes no record.
- **Repeats.** The adapter answers a repeated call ID with the first call's result while the conversation stays open. A reopened conversation does not resume pending work, so an earlier call does not run again. New Task IDs are random, as for every other Task.
- **Storage.** `spawned_by` is kept in the Task's web state in `sessions.json` and sent in the session summary. The browser shows "Started by another task" at the start of that Task's conversation.
- **Import.** Importing the conversation of a deleted spawned Task makes an ordinary Task, which has the tool.
