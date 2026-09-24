# Web commands and execution state

The command menu reads the current provider catalogue. A command is executable
only when UAM has a handler for its effects and result. Unsupported commands stay
visible with a reason and are rejected before the native invocation. Discovery
can reopen an active Task's exact conversation, but first checks whether another
client holds an imported or terminal-linked conversation. Settled and archived
Tasks remain read-only.

## Copilot support

This table describes the web adapter for the pinned Copilot Go SDK 1.0.14. A
command also has to exist in the installed runtime's catalogue. Its native
aliases are accepted, including `goal`, `yolo`, and `models`.

| Commands | Web behavior |
| --- | --- |
| Discovered skills; `init`, `review`, `blame`, `fleet`, `research`, `security-review` | Resolve the native prompt and submit it with the selected Task model. Referenced files and attachments are supported. |
| `autopilot` / `goal` | Invoke native autopilot, including its on/off, objective, and credit-limit arguments. Reconcile execution mode and report the native objective state. Autopilot does not change permissions. |
| `allow-all` / `yolo` | Change the Task's existing permission policy: `on` means Yolo, `off` means Safe, `show` reports it, and no argument toggles it. |
| `permissions` | Open permission settings; `default`, `allow-all`, and `show` select Safe, select Yolo, or report the policy. |
| `model` / `models` | Open model settings, or select one offered Task model ID. Native global/repository and plan-model options are not accepted. |
| `rename` | Open rename, or rename this web Task. |
| `context`, `usage`, `list-dirs`, `env`, `skills` | Display native read-only output. Arguments are rejected before invocation. |
| `plan` | Disabled until the web client supports plan-exit approval. The SDK callback explicitly refuses automatic plan exit. |
| `every`, `after` | Disabled: web scheduling is not supported. |
| `cwd`, `add-dir` | Disabled: these change directories outside Task project settings. |
| `share`, `remote` | Disabled: publishing and remote-session handlers are not implemented. |
| Other native/client commands | Disabled until their effects and required web handlers are supported. |

The native catalogue controls whether a command can run during an active turn.
Commands use their own endpoint and never become Queue or Steer submissions.
Control and read-only commands refuse attachments before invocation; the browser
can preserve the draft. Text, completion, timeline-entry and subcommand-selection
results do not mark the foreground Working. Selecting a subcommand prefills the
composer for explicit submission. Native model/dialog results cannot be invoked
through an unsupported command to bypass Task model settings.

## Autopilot and Stop

Choose Interactive or Autopilot from the execution dropdown beside the model and
permission controls. Selecting a mode preserves the draft and attachments and
does not send a message or change Safe/Yolo permissions. Selecting Interactive
disables future continuation; use Stop to abort current work as well. Objective
details appear inside the dropdown, without a separate execution banner.

Execution mode (`interactive`, `plan`, or `autopilot`) is separate from the
Task's permission policy (`safe` or `yolo`). Runtime mode and objective state come
from `session.mode.get` and `session.autopilotObjective.getState`, refreshed on
provider events and exact reopen. Objective state is `active`, `paused`, or
`completed`; credits are the provider's current credit-window usage and limit.
A reported zero is preserved. Missing values are not inferred.

An assistant idle or explicit autopilot session-idle boundary does not complete
an autopilot foreground turn. A final session-idle event, with no autopilot mode
or with an abort, ends it. Interactive assistant idle still ends the foreground
while a background server keeps running. If execution mode cannot be read,
UAM waits for authoritative session completion rather than guessing interactive
mode. Disconnect retains the last state as unknown; a service restart has no
live execution observation until the provider is read again.

Stop serializes with command execution, requests interactive mode to disable
future autopilot continuation, then aborts active work. Abort is attempted even
if the mode change fails, and partial failures are reported. Stop pauses queued
follow-ups and never grants pending permissions. Changing to Yolo through the
permission control or an explicit permission command retains its existing
behavior of approving pending permission requests.

## Background shell cancellation

`POST /api/sessions/{id}/background-tasks/{task_id}/cancel` accepts no body and
returns `{accepted: true, background_tasks: ...}` after a fresh native task read.
The selected record must be a known, running shell belonging to the Task. The
adapter checks the fresh native record's type before calling `session.tasks.cancel`.
Subagent IDs are refused by this route. Foreground cancellation and other shell
processes are unaffected.

An accepted request may still have `status: running`: the UI reports Stop
requested until the provider reports a terminal state. Missing/non-shell IDs
return 404; unknown, closed, read-only or inactive Tasks and refused cancellation
return 409. A failed refresh retains the last snapshot with `known: false` and
returns an error. Subagents keep their separate cancellation endpoint.

## Command outcomes and retries

`POST /api/sessions/{id}/command` returns the existing Submission shape with an
optional `command_result`. The result is text, completed, select, or an action
opening an existing web control. A prompt result has no `command_result` and
uses normal turn events.

UAM durably reserves a request ID before invoking a potentially mutating command.
A lost or ambiguous response is uncertain and never automatically retried. The
latest 64 command request IDs and outcomes survive restart; a repeated retained
ID returns that result without invoking again. A storage failure before the
reservation is confirmed prevents invocation. A failure to persist the final
result reports an error; the earlier reservation still prevents replay.

Runtime testing remains a separate release check from fake-provider and race
regressions. No custom continuation-count limit or automatic permission grant is
implemented by this command surface.
