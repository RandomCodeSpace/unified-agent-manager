# UAM terminology

This glossary distinguishes the process that UAM manages from the conversation
state owned by an agent provider.

| Term | Meaning |
|---|---|
| **Managed Session** | One persistent UAM record and its detached terminal host. It has a UAM ID, provider, name, workspace, prompt metadata, and lifecycle state. |
| **Provider Conversation** | Conversation state owned by Claude Code, Codex, Copilot, Oh My Pi, OpenCode, or another provider. A provider conversation may have its own identifier and resume rules. It is not the same object as a Managed Session. |
| **Attach** | Connect the current terminal to an already-running Managed Session. The attach operation itself does not start or resume a provider process, although the `uam attach` command first resumes a selected Stopped session when supported. Detaching leaves the running process in place. |
| **Resume** | Start a stopped Managed Session's provider process again, preserving the UAM identity and asking the provider to continue an earlier Provider Conversation. |
| **Workspace** | The working directory shared by a Managed Session and its provider process. Multiple Managed Sessions may use the same Workspace and therefore edit the same files. UAM does not create a worktree or other filesystem isolation automatically. |
| **Explicitly Stopped** | A Managed Session whose provider process was stopped through UAM. This is retained as reason metadata; it still appears in the **Stopped** lifecycle group. |
| **Running** | The Managed Session's provider process is alive. This is based on process liveness, not interpretation of terminal text. |
| **Stopped** | The provider process is not alive. The session record remains available and may be resumable. A clean exit, explicit stop, signal, or nonzero exit all belong to this group; exit detail distinguishes failures where known. |
| **Exact resume** | Resume targets a known Provider Conversation or a provider state directory dedicated to the Managed Session. Other sessions in the same Workspace cannot change the target. |
| **Heuristic resume** | The provider can only continue its most recent conversation or equivalent. When several retained sessions for that provider share a Workspace, UAM requires explicit confirmation because the target cannot be proven. |

See [Managed Session vs. Provider Conversation](docs/adr/0001-managed-session-vs-provider-conversation.md)
for the decision behind these definitions and [Responsive TUI operations](docs/responsive-tui.md)
for day-to-day controls.
