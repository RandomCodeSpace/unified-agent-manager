# UAM terminology

| Term | Meaning |
|---|---|
| **Provider** | GitHub Copilot, the agent integration supported by UAM. Custom model endpoints still run through Copilot. |
| **Provider Conversation** | Conversation state owned by Copilot, with its own identifier and history. Removing a UAM Task does not delete this conversation. |
| **Workspace** | A working directory used by tasks. Tasks sharing a workspace may edit the same files; UAM does not create filesystem isolation. |
| **Project** | A named workspace that groups Tasks. It can be removed only when it has no Tasks or all its Tasks are Archived. Removal leaves the directory and provider conversations intact. |
| **Task** | A UAM record for a conversation in a Project. It has a model, a name, and lifecycle state. It can be created in UAM or imported from an existing Copilot conversation. |
| **Active** | A Task that can accept work. Its execution state may be idle, working, or waiting for the user. |
| **Settled** | A Task marked complete by the user. It is read-only and its conversation is closed. Reopening makes it active; the next prompt continues the same conversation. |
| **Archived** | The final stage of a Task. It is read-only, cannot be reopened, and can be deleted. |
| **Imported Task** | A Task linked to an existing Copilot conversation. UAM checks whether another client holds that conversation before importing or sending work. |
| **Spawned Task** | A Task another Task's agent started with `uam_create_task`, in an existing Project. It starts in safe mode, records the Task that started it (`spawned_by`), and cannot start Tasks itself. |
| **Routine** | Recurring work in a Project: on its schedule (daily, weekdays, every N hours or weekly, in the host's local time) it starts a Task with its prompt. Its runs never overlap, are capped per day and in minutes, and record their outcome. A Task a run started records the routine (`routine_id`) and cannot start Tasks itself. |
| **Legacy terminal record** | Saved metadata from UAM's retired terminal support. It remains on disk but is not an active Task and has no terminal controls in current UAM. |

See the [web guide](docs/web.md) for current behavior. Earlier terminal ADRs
remain historical records of the retired interface. ADRs 0005 and 0006
record the planner, which [ADR 0007](docs/adr/0007-remove-the-planner.md)
removed; their terms (Board, Card, Epic, Lane and so on) are no longer used.
