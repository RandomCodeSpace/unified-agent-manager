# ADR 0005: Agentic planner

Status: accepted (2026-09-29). Decided in [#255](https://github.com/RandomCodeSpace/unified-agent-manager/issues/255) and [#248](https://github.com/RandomCodeSpace/unified-agent-manager/issues/248). The map is [#247](https://github.com/RandomCodeSpace/unified-agent-manager/issues/247).

## Context

uam gets a planner: an agile board of **epics, stories and subtasks** for each git Project.
- Agents decompose the work and carry it out through in-process tools.
- The owner confirms, launches and closes the work.
- The storage and rules are ported from kb (github.com/RandomCodeSpace/kb, MIT), using SQLite through `modernc.org/sqlite` (no CGO).
- It is not a record of every Task. A uam Task appears on the board only when it holds a subtask or plans under a container.

Naming: **Task** always means a uam conversation. The planner's leaf kind is **subtask** (`KindSubtask`), and nodes are **cards** on a **board**.

## Decision

## Principles

- Agents write the plan, the owner's edits stick, and only the owner closes work.
- Deterministic rules over judgement. Every stored state has a writer and a defined exit.
- The board holds only work that was planned or started. uam Tasks that never touch it don't appear on it.
- The planner sits behind a Settings switch, off by default. It exists only on Projects with a git repository.

## 1. Model

One table, where a node is a card. It has the ported kb fields (`#seq`, title, desc, prio, due, effort, labels, checklist, blocked flag, comments, blocker links, FTS search, revision) plus:

| Column | Meaning | Written by |
|---|---|---|
| `kind` | `epic`, `story` or `subtask` (the leaf) | create, split |
| `parent_id`, `rank` | Tree position. Kind rank is epic < story < subtask, a parent must outrank its child, and the root may hold any kind | agents (in scope, unconfirmed nodes only), owner |
| `win_condition` | One line saying what done means | agents on unconfirmed nodes, owner |
| `expires_at` | NULL means **confirmed**. Agent-created nodes start at now + 14 days; any owner save, launch, accept or restore sets it to NULL | uam |
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
- Progress is done ÷ non-cancelled confirmed leaves, counted rather than weighted. The UI adds "+N proposed" for unconfirmed leaves.
- When a container reaches done, uam does two things:
  - it cancels the container's remaining unconfirmed, unheld leaves with the comment "closed unconfirmed with #12";
  - it adds a roll-up comment made of the children's close comments.
- There is no force on containers.

## 3. Who may write what

A static actor table (owner or agent) sits in one transition function in `internal/board`.

**Agents, within scope, may:**
- create stories and subtasks;
- edit anything on an unconfirmed node, where the last write wins;
- on a confirmed node: tick, untick or add checklist items, comment, add children (under containers), and add blocker links. Any other change is filed as a `change` request, one pending per node per Task, with a newer request replacing the older one;
- claim a leaf, moving it from planned or todo to doing;
- file `done`, `cancel`, `blocked` and `split` requests.

**Agents never:**
- set done, cancelled or the blocked flag;
- write `accept_cmd` or `paths`, which appear in no tool schema;
- create epics or root nodes, or reparent a node outside their scope;
- restore or purge.

**The owner** may do everything. That includes marking a leaf done directly, with a comment: the finishing guard applies, and `force` exists on leaves only.

**Blocker links** may point only at confirmed cards. A link stays open while its blocker is not terminal, so cancelling the blocker releases it.

## 4. Scope and caps

- **Planning Task:** "Plan with agent" on a container starts a normal Task with a preamble. It creates and edits under that container and holds nothing.
- **Working Task:** its scope is the container it was launched from ("Do whole story"), or the parent of its single launched leaf. It creates siblings and claims leaves only within that scope.
- **Utility scout:** "Suggest stories" on a container follows the same rules. What it writes are unconfirmed nodes, so suggestions and agent decomposition are one mechanism.
- **Caps** are counted per Task, and calls made by subagents count against their Task:
  - 20 created nodes;
  - 10 unconfirmed children per container;
  - 20 comments per card;
  - one hold without a pending request at a time.
- A duplicate normalised title among live siblings is refused. The FTS similarity check is information only.

## 5. Launch, holds and release

- **Launch** is an owner action on a leaf only. It moves the leaf from planned or todo to doing, sets `held_by` to the new Task, and counts as a touch (it confirms and re-pins the leaf). The start of a hold records HEAD and `git status` as the evidence baseline.
- **"Do whole story"** launches the first pending confirmed leaf. The preamble carries the story's pending list, and the Task claims further leaves through the tool.
- **A hold persists while the Task is Active.** It can end only through one `releaseHold` path:

| Event | Result |
|---|---|
| Request accepted | done |
| Request rejected while the holder is live | Steer sent to the Task; the hold stays |
| Request rejected while the holder is not live | todo, with the reason as a comment |
| Settle | A dialog for each held leaf: keep held (it resumes after Reopen), release to todo, or cancel with a comment |
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
4. No diff, no commits and an empty command → the request is filed, flagged "no change in tree".
5. Otherwise → a done request with evidence rows.

**Evidence rows:**
- **diff:** `numstat` since the hold started. It shows the total, the files this Task touched, and any overlap with other live holds ("overlaps with #13 (Task X)"). It is tagged "tests or build files changed" when the diff touches test, build or CI files.
- **commits:** the log since the hold started.
- **accept:** command hash, HEAD, dirty flag, exit code and output tail.
- **transcript:** the span of the Task's transcript.
- **checklist:** the checklist state.

**The runner:**
- There is one per Project, guarded by a mutex. A claim that waits longer than the acceptance timeout is refused with "acceptance busy, retry".
- It runs `$SHELL -lc` in the Project directory, in its own process group, bound to the Task's context. Archive or Delete kills the run and discards the result.
- Editing a command marks the earlier green rows for that command as stale.

**The owner** accepts or rejects:
- Accept turns the claim text into the close comment and counts as a touch.
- Reject requires a reason.

**Leaves that are red by design**, such as a leaf that adds a failing test, have two options: fold red and green into one leaf, or have the owner set that leaf's command to `''`. The refusal message says so. Agents may attach a `proposed_accept_cmd` as text; the owner's Apply copies it into the leaf, and it never runs before that.

## 7. Split

- A split turns a leaf into a story with child subtasks.
  - Unticked checklist items become planned children.
  - Ticked items become children with a pending done request that cites the tick.
  - A split never creates done children.
- **Unconfirmed, unheld leaf:** the split applies directly.
- **Confirmed or held leaf,** including the holder's own leaf: the split is one inbox row. Accepting it applies the structure and accepts the ticked children together. A live hold moves to the first pending child.

## 8. Cancel, restore, purge

- Marking a leaf done, cancelling it and restoring it all require a non-empty comment. Automatic comments (attempt ended, roll-up, expired, imported, closed with parent) are exempt from the caps.
- **Cascade** (owner only) stamps one comment on each non-terminal leaf, "cancelled with #12: …", with a shared `cascade_id`. It releases holds and withdraws requests.
- **Restore** (owner only, comment required) reopens exactly the nodes with that `cascade_id` and confirms each one. A leaf under a cancelled parent can't be reopened on its own.
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
- **Card detail:** fields, win condition, checklist, the evidence trail, hold history (attempts), comments, and the owner-only command and paths.
- **Actions:** Launch, Do whole story, Plan with agent, Suggest stories, Confirm, Release, Cancel (with a comment), Restore (with a comment), Check at HEAD, Triage, and Purge cancelled.
- **The Settle dialog** for held leaves (§5).

**Floating picture-in-picture.** Any of the three views, or the Inbox, can pop out into a floating panel above the page while you work in a Task.
- On every browser the pop-out is an in-page panel. You can drag it by its header (touch too), resize it, maximise it, and hide it into a small tab. Its box persists.
- A pop-out from the Planner renders the Planner's own view state and stays up across navigation until it is closed. Its Hide lasts only while it is up. Closing it while a Task is open leaves that Task's panel as the tab for the rest of the visit, even over a stored Show.
- While a Task of a git Project is open and nothing is popped out, the panel shows the Task's Project's Board on its own, in a view state of its own. Following Tasks never changes the Planner's Board, selection or filters (21). Two explicit actions from the panel do change them:
  - opening a card, which selects it in the Planner and clears any filter there that would hide it;
  - moving the panel into a separate window.

  The panel never shows over the Planner view.
- Whether that panel opens expanded or as the tab is decided once each time a Task opens:
  - the owner's last Hide or Show of it for that Project, which persists, and only those two buttons write it;
  - else expanded when the Board has a live card;
  - on a phone, always the tab.

  A Board that fills later doesn't open it.
- Where the Document Picture-in-Picture API exists (Chromium, not on a phone), a header button moves the panel into a separate window, which needs the user's gesture. The window renders the Planner's view state. From a Task's panel, the Planner first switches to that Board, as picking it would. The window is rendered through a React portal from the main app, so it needs no new route and no second SSE stream.
- The prototype must confirm that the window works under uam's strict CSP: stylesheets copied as same-origin links, and no inline styles. It must also work behind the owner's sign-in proxy.

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
4. At most one acceptance run per Project happens at a time, a claim still waiting past the timeout is refused, and Archiving or Deleting the Task kills its run and records nothing.

**Holds and requests**

5. `doing ⇔ held_by` holds after every write.
6. After any Task transition (Settle, Reopen, Archive, Delete, or boot with the Task missing), no leaf is held by an Archived, Deleted or absent Task.
7. A restart changes no holds of Active or Settled Tasks and writes no comments.
8. `claim_done` and Accept refuse when the leaf isn't doing or isn't held by the Task making the request.
9. Any status change on a leaf withdraws its pending requests.

**Agent limits**

10. For the agent actor, moving to done or cancelled, setting the blocked flag, restoring and purging are all refused.
11. A split never produces a done child, and a split of a confirmed or held leaf is exactly one pending request.

**Containers**

12. Derived status and progress ignore unconfirmed leaves, except that a hold makes the container doing, and a container with no confirmed leaves is never done.
13. A container can't become done while a leaf under it is held or has a pending request. When it does become done, its unconfirmed, unheld leaves are cancelled with the automatic comment.

**Sweep, cascade and links**

14. The sweep never cancels a held node, a node with a pending request, or an ancestor of a held or confirmed node.
15. Restore reopens exactly one cascade's nodes and confirms them, and a restore without a comment is refused.
16. A cancelled blocker doesn't block, and a link to an unconfirmed card is refused.

**Caps, staleness and import**

17. Caps count per Task, and calls from subagents count against their Task.
18. The duplicate-title refusal ignores cancelled siblings.
19. Staleness is not computed for held leaves.
20. Imported cards are confirmed with no pin, and imported done cards carry the automatic close comment.

**UI**

21. The picture-in-picture window and the floating panel popped out from the Planner render the Planner's view state, and closing either loses no selection. The panel a Task shows on its own has its own view state, and following Tasks never changes the Planner's. Only two explicit actions from that panel change it: opening a card, and moving the panel into a separate window.

## Rejected (non-goals)

- Keeping the plan as Markdown in the repo.
- A card as a bundle of pointers with generated text.
- Weighted consolidation of proposals.
- A long-lived steward Task per epic.
- A per-Project rule table.
- Parsing the agent's shell commands for evidence.
- Per-Task git worktrees: overlap between Tasks is flagged, not prevented. Running acceptance in a temporary worktree is prototype-only, and only if overlap proves painful.
- Tracking every uam Task: no automatic card per Task, and no `#12` composer references in this effort.

## Deferred

- Auto-accept per Project.
- "Start next card".
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
  "held_by": "task uuid", "pinned_sha": "",
  "accept_cmd": null, "paths": [],
  "stale": {"behind": 3, "diverged": false, "files": ["a.go"]},
  "pending_requests": 1, "revision": 7,
  "created_at": "RFC3339", "updated_at": "RFC3339", "moved_at": "RFC3339"
}
```

- `status` is derived for containers.
- `progress` is sent for containers only.
- `expires_at` is sent only while the card is unconfirmed.
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
  "created_at": "RFC3339", "decided_at": "RFC3339", "decision_comment": ""
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
| `POST /api/board/cards` | Owner create; the card is confirmed: `{project_id, kind, parent_id, title, desc, win_condition, prio, effort, due, labels, checklist}` |
| `PATCH /api/board/cards/{ref}` | Owner edit of any field, including `accept_cmd`, `paths` and `project_id` (moving a card out of Unassigned); the edit confirms the card |
| `POST /api/board/cards/{ref}/confirm` | Confirm |
| `POST /api/board/cards/{ref}/dismiss` | Cancel an unconfirmed node, with the automatic comment "dismissed" |
| `POST /api/board/cards/{ref}/move` | `{parent_id, rank}` |
| `POST /api/board/cards/{ref}/status` | `{status: done|cancelled|todo, comment, force}` — owner-direct on a subtask; `cancelled` on a container is the cascade |
| `POST /api/board/cards/{ref}/restore` | `{comment}` |
| `POST /api/board/cards/{ref}/split` | `{children: [{title, win_condition}]}` (owner split) |
| `POST /api/board/cards/{ref}/comments` | `{body}` |
| `POST /api/board/links` | `{blocker, blocked}` |
| `DELETE /api/board/links?blocker=&blocked=` | Removes a blocker link |
| `POST /api/board/cards/{ref}/launch` | `{model, effort, mode, context_size}` (optional; Project defaults otherwise) → 201 `{card, session}`. On a subtask it launches that subtask; on a container it is "Do whole story" |
| `POST /api/board/cards/{ref}/plan` | `{brief, model, …}` → 201 `{session}`. A planning Task scoped to the container |
| `POST /api/board/cards/{ref}/release` | `{comment}` |
| `POST /api/board/cards/{ref}/check` | Check at HEAD → `{accept}` |
| `POST /api/board/cards/{ref}/triage` | `{verdict: valid|moot|conflicts, sentence, head}` |
| `POST /api/board/cards/{ref}/suggest` | `{brief, document, max}` → 202 `{job_id}` |
| `POST /api/board/requests/{id}/accept` | `{comment}` |
| `POST /api/board/requests/{id}/reject` | `{reason}` |
| `POST /api/board/purge` | `{project_id}` |
| `POST /api/board/import` | `{dir}` → `{imported, updated, unassigned, comments, links, skipped: [{id, reason}]}` |

**Launch and plan** create the Task through the existing create path, set the hold (launch only) in the same request, and send the preamble as the Task's first prompt.

The preamble is deterministic. It contains:
- the card's path with `#seq` (epic › story › subtask);
- the win condition, description and checklist;
- for "Do whole story", the pending list;
- for a stale card, the log since the pin (up to 50 lines) and the changed files;
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
| `board_create` | `{kind: story|subtask, parent, title, desc, win_condition, prio, effort, labels, checklist}` |
| `board_edit` | `{ref, title, desc, win_condition, prio, effort, labels, parent, rank}` — on a confirmed card this becomes a `change` request |
| `board_checklist` | `{ref, tick: [index], untick: [index], add: [text]}` |
| `board_comment` | `{ref, body}` |
| `board_link` | `{blocker, blocked}` |
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

These record how `internal/board` reads this contract, plus two later decisions. Where they differ from a section above, they win.

**Readings and deviations**

- **Stored container status:** epics and stories store only `planned` or `cancelled`. Cancelled is their own state (dismissed, swept or cascaded); every other status is derived (§2).
- **Derivation order:** cancelled itself → any leaf held: doing → no confirmed leaves: planned → every confirmed leaf cancelled: cancelled → every live confirmed leaf done: done, or doing while any leaf has a pending request → any confirmed leaf done: doing → otherwise planned.
- **`cascade_id` on every cancel:** a single-leaf cancel, each swept subtree and each close-on-done get one too, so Restore always has a set to reopen.
- **Replace rule per kind:** one pending request per card, Task and kind. A newer request replaces the older one for every kind, not only `change`.
- **Accepting `blocked`** links the named blocker if there is one, otherwise it sets the flag; the hold stays. **Accepting `cancel`** runs the cancel or cascade.
- **Not touches:** owner Release, owner cancel and accepting a cancel request don't confirm or re-pin anything. Releasing an unconfirmed subtask re-arms its expiry (§5).
- **One-hold cap:** only a pending `done`, `blocked` or `split` request exempts a hold; a `change` or `cancel` request doesn't (§4).
- **Cascades** skip containers that are already done. When a container reaches done, its own pending requests are withdrawn.
- **Split done requests:** the done requests a split files for ticked items need no hold to be accepted. An owner's split files them with an empty Task ID.
- **The sweep** also skips a subtree with a pending request anywhere in it, so inbox rows are never withdrawn silently.
- **Reconcile** also takes `asOf`, the time the caller read its Task snapshot, and leaves alone any hold that started at or after it. It takes each Project's uncommitted paths too, for the "uncommitted: …" comment. It treats an unknown stage as ended and does not sweep.
- **`scopes` table** (added to §13): one row per Task, holding its Project, its container and whether it is planning or working. Launch and Start planning write it, and §4 is enforced from it.
- **Extra error codes:** `not_found`, `forbidden`, `limit`, `duplicate`, `acceptance_busy` and `acceptance_failed`, alongside §14's. `unconfirmed_parent` is gone (decision 2).
- **Evidence (§6):** it is gathered before the acceptance run. A hold's baseline also keeps a blob hash for each dirty path (`holds.baseline_blobs`), so a path dirty at the start counts only if its content changed, marked `pre_dirty` on its file row. When the baseline commit no longer exists, the done request is filed with the flag `baseline_missing`, the touched files compared with HEAD, and no commits.
- **Links:** both ends must be in one Project. Only the owner can unlink.
- **Cancelled checks** use the card's own stored status. Nothing is created or moved under a cancelled card or anything below one, nothing is edited on a cancelled card until it is restored, and cancelled siblings don't count in the duplicate-title check. Ready, Launch and Claim refuse a subtask with a cancelled ancestor, as Restore does (§8).
- **Restore** reopens a leaf as todo if it was ever held, otherwise as planned, and runs the duplicate-title check.
- **Limits:** Purge deletes only subtrees that are entirely cancelled. A card moves between Projects only out of Unassigned.
- **Import (§11):** any source schema other than v11 is refused with `import_schema`, and a source that keeps changing while it is copied with `import_busy`. Imports are exempt from the duplicate-title rule, because the source allows repeated titles and refusing them would drop cards. An `import_refs` table maps each source UUID to its card, with the state last imported and the highest comment copied. Running the import again applies only the fields the source changed since the last run, so the owner's edits survive; it copies only new comments, makes each source link once both cards share a Project, removes links the source dropped while leaving the owner's unlinks alone, and moves a card still in Unassigned once its source project matches. A change aimed at a held card, a card cancelled in uam, or a status the owner couldn't set directly is skipped, reported and retried on the next run. A purged card is never recreated. Only git Projects match, and a name shared by several Projects matches none.
- **Agent tool parameters (§16):** `board_list` filters with `state`, and `board_link` takes `{ref, blocker}`, where `ref` is the blocked card, because no schema may have a `status` or `blocked` key. `board_request` also takes `proposed_accept_cmd`, for `done` only.
- **Agent tool results (§16):** each result is compact JSON, `{text, code, refs, card}`. A refusal carries `code` and `refs`, such as the open checklist items; `card` (`id, seq, kind, title, status`) is the card the call was about. The adapter records only this text, so the transcript chip is rebuilt from it after a reload. A result stays under 32 KiB, well under the 64 KiB the transcript records whole: `board_get` cuts the description and comment bodies and shows at most 20 cards per list, saying how many more there are, and a longer text is cut at the end.
- **Agent tool registration (§16):** the tools are added when a Task's conversation opens. Turning the switch on reaches Tasks opened afterwards, including a reopened one. Every call checks the switch and the Project's git again. A call runs only while its Task is active. Settling or archiving the Task ends its calls in progress before its holds are decided, and a hold such a call made anyway is released.
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

1. **Split under a story.** A leaf whose parent is a story splits into sibling leaves under that story, placed right after it and in order. Unticked items become planned siblings; ticked items become siblings with a pending done request that cites the tick. The original is cancelled with the automatic comment "split into #a, #b, …" and its own `cascade_id`, so Restore brings it back and leaves the siblings. A live hold moves to the first pending sibling. An unconfirmed, unheld leaf splits directly; a confirmed or held one files one split request, and accepting it applies everything at once. Under an epic or at the root, §7 is unchanged: the leaf becomes a story.
2. **An owner touch confirms ancestors.** Any owner touch on a card (save, create, launch, accept, restore, confirm or a status change) also confirms and re-pins each unconfirmed ancestor in the same transaction. This replaces the `unconfirmed_parent` refusal. So launching a leaf under an agent-suggested story confirms the story, and the sweep can't expire it.
3. **Board revisions in the snapshot** (added to §15). While the planner is on, the `snapshot` frame carries `boards`: each Project's board revision, plus `""` for Unassigned. It is omitted while the planner is off. A client reloads only the boards it holds an older revision of.
