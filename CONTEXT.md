# UAM terminology

| Term | Meaning |
|---|---|
| **Provider** | GitHub Copilot, the agent integration supported by UAM. Custom model endpoints still run through Copilot. |
| **Provider Conversation** | Conversation state owned by Copilot, with its own identifier and history. Removing a UAM Task does not delete this conversation. |
| **Workspace** | A working directory used by tasks. Tasks sharing a workspace may edit the same files; UAM does not create filesystem isolation, except that each Lane works in its own git worktree (introduced by ADR 0006). |
| **Project** | A named workspace that groups Tasks. It can be removed only when it has no Tasks or all its Tasks are Archived. Removal leaves the directory and provider conversations intact. |
| **Task** | A UAM record for a conversation in a Project. It has a model, a name, and lifecycle state. It can be created in UAM or imported from an existing Copilot conversation. |
| **Active** | A Task that can accept work. Its execution state may be idle, working, or waiting for the user. |
| **Settled** | A Task marked complete by the user. It is read-only and its conversation is closed. Reopening makes it active; the next prompt continues the same conversation. |
| **Archived** | The final stage of a Task. It is read-only, cannot be reopened, and can be deleted. |
| **Imported Task** | A Task linked to an existing Copilot conversation. UAM checks whether another client holds that conversation before importing or sending work. |
| **Spawned Task** | A Task another Task's agent started with `uam_create_task`, in an existing Project. It starts in safe mode, records the Task that started it (`spawned_by`), and cannot start Tasks itself. |
| **Routine** | Recurring work in a Project: on its schedule (daily, weekdays, every N hours or weekly, in the host's local time) it starts a Task with its prompt. Its runs never overlap, are capped per day and in minutes, and record their outcome. A Task a run started records the routine (`routine_id`) and cannot start Tasks itself. |
| **Legacy terminal record** | Saved metadata from UAM's retired terminal support. It remains on disk but is not an active Task and has no terminal controls in current UAM. |
| **Board** | A Project's plan in the planner (ADR 0005): cards arranged as epics, stories and subtasks. It exists only for git Projects, behind a Settings switch. |
| **Card** | One node on a Board: an epic, a story or a subtask. |
| **Epic** / **Story** | Container cards. Their status and progress are derived from the subtasks under them, and they are done only when every confirmed subtask is done. |
| **Subtask** | The leaf card, the unit of work a Task is launched on. It is never called a "task": a Task is always a UAM conversation. |
| **Confirmed** | A card the owner has saved, launched, accepted or restored, or a subtask whose done Request was accepted automatically (an agent's own proposal included), with its ancestors. Under an Approved epic, Approve confirms the cards it lists, and Restore makes proposals (ADR 0006). Agents in reach still plan it until it starts, and since ADR 0006 may delete it; on a started card they file change requests. |
| **Hold** | The link between a doing subtask and the Task working on it. It ends only through the release rules in ADR 0005, which ADR 0006 extends for Lanes: a Lane's hold also ends when its work lands or the owner Stops it, and an attempt that ends without landing pauses its subtask. |
| **Request** | An agent's ask for a decision on a card: done, cancel, blocked, split or change. The owner decides it, except that a done Request may be accepted automatically under ADR 0005 decision 5, and a Lane's done Request is accepted when its work lands (ADR 0006). A failing acceptance command refuses a done Request. Pending Requests form the Inbox. |
| **Finishing guard** | The refusal to finish a subtask with open checklist items, the blocked flag, or open blockers. Only the owner can override it. |
| **Approved epic** | An epic the owner approved once with Approve and run. The approval confirms the cards it lists and records the model, mode and parallel limit uam runs their subtasks with. Per-card Confirm and manual starts are refused under it. Introduced by ADR 0006. |
| **Pause** | The owner's flag on a card under an Approved epic: uam starts nothing at or under it, while running work goes on. uam also pauses a subtask whose attempt ended without landing. It is not the blocked flag, which says an agent is stuck. Introduced by ADR 0006. |
| **Lane** | One attempt at a subtask under an Approved epic: a new Task that works only on that subtask, in its own git worktree and branch made from the Integration branch. Introduced by ADR 0006. |
| **Integration branch** | The one git branch per Project that Landed subtasks are committed to. It follows the owner's base branch, and its work reaches the owner's branch only when the owner clicks Merge. Introduced by ADR 0006. |
| **Landed** | A subtask whose Lane's done Request was accepted and whose work is now one commit on the Integration branch. Accepting and landing are one step. Introduced by ADR 0006. |
| **Revert** | The owner's undo of a Landed subtask. Its commit, and those of the Landed subtasks started on top of it, are reverted on the Integration branch in one step, and their cards go back to To do, paused. Introduced by ADR 0006. |

See the [web guide](docs/web.md) for current behavior. Earlier terminal ADRs
remain historical records of the retired interface.
