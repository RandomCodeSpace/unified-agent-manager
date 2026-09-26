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
| **Legacy terminal record** | Saved metadata from UAM's retired terminal support. It remains on disk but is not an active Task and has no terminal controls in current UAM. |

See the [web guide](docs/web.md) for current behavior. Earlier terminal ADRs
remain historical records of the retired interface.
