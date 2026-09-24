# ADR 0004: Web interface through provider APIs

- Status: Accepted
- Date: 2026-09-23

## Context

Users want to start a Copilot or OpenCode task from a browser, close the
browser and the SSH connection, and later see the same task's progress or
result. The terminal path (ADR 0001–0003) renders provider TUIs through a PTY
host. Rendering those TUIs in a browser would inherit terminal semantics
(scraping, keystroke injection) that a web interface cannot use safely.

## Decision

`uam web` starts one detached per-user service, `uam __web`, that serves an
embedded single-page application and drives providers through their
structured APIs:

| Provider | Integration |
|---|---|
| GitHub Copilot | Official Copilot Go SDK driving the installed `copilot` CLI |
| OpenCode | `opencode serve` HTTP API and event stream, one UAM-owned server per project directory |

Only Copilot is registered for now. Web features are built against Copilot
first, and a provider is offered only when it supports them the same way.
Per-Task context size is the sole capability-gated exception to this rule.
The OpenCode integration stays in the code base, unregistered.

### Ownership

| Owner | Owns |
|---|---|
| `uam __web` | Managed-session identity, project, exact provider conversation ID, execution lifetime, browser connections, pending interactions, reconnect state |
| Provider | Planning, model calls, tools, file edits, conversation semantics |
| Browser | Presentation and input only |

Provider conversations, turns, and provider runtimes belong to the service,
never to an HTTP request or an event-stream connection. Four operations stay
distinct:

| Operation | Effect |
|---|---|
| Viewer disconnects | Nothing on the provider side |
| Cancel | Aborts the current turn; the conversation stays open |
| Close session | Disconnects the provider conversation; the record stays |
| `uam web stop` | Stops the service and every runtime it started; running turns are recorded as interrupted |

The service never stops a provider server it did not start.

### Records

Web sessions are stored in `sessions.json` as ordinary `SessionRecord`s with
`surface: "web"`, the exact provider conversation ID in
`provider_session_id`, and a small `web` object with the last known turn state
and last accepted prompt request ID. Terminal commands and the dashboard do
not list, attach, resume, or prune records whose `surface` is non-empty, and
the web service only opens `surface: "web"` records. Neither surface takes over
a conversation owned by the other.

Tokens and transcripts are not written to disk by UAM. Transcript history is
read back from the provider when a conversation is reopened.

### Access

The service binds to a loopback address only. Access is by SSH local
forwarding or a same-host reverse proxy. Every API request must carry a
session cookie obtained by presenting the access token stored owner-only next
to `sessions.json`. Requests are rejected when the `Host` header is not
loopback or a configured public origin's host, and state-changing requests are
rejected unless they are same-origin (`net/http.CrossOriginProtection`) and
JSON. There is no CORS. `uam web --no-auth`, off by default, treats every
request as authenticated; the other checks stay, but it removes the only
barrier for anyone who can reach the service, including through a public
reverse proxy.

## HTTP contract

All JSON. All `/api/*` routes except `/api/auth` and `/api/login` require the
cookie, unless the service runs with `--no-auth`. Errors are
`{"error": "<message>"}` with a 4xx/5xx status.

| Method and path | Body | Result |
|---|---|---|
| `GET /api/auth` | – | `{"authenticated": bool, "required": bool}`; with `--no-auth`, `required` is false and `authenticated` true |
| `POST /api/login` | `{"token"}` | 204, sets cookie; 401 on mismatch |
| `POST /api/logout` | – | 204, clears cookie |
| `GET /api/meta` | – | `{"version", "providers": [ProviderInfo], "recent_workdirs": [string]}` |
| `GET /api/sessions` | – | `[SessionSummary]` |
| `POST /api/sessions` | `{"provider", "workdir", "name", "prompt"?, "request_id"?}` | 201 `SessionSummary` |
| `GET /api/sessions/{id}` | – | `SessionDetail` |
| `PATCH /api/sessions/{id}` | `{"name"}` | `SessionSummary` |
| `POST /api/sessions/{id}/prompt` | `{"text", "request_id"}` | 202 `Submission` |
| `POST /api/sessions/{id}/cancel` | – | 202 `SessionSummary` |
| `POST /api/sessions/{id}/close` | – | `SessionSummary` |
| `POST /api/sessions/{id}/interactions/{iid}` | `Answer` | `Interaction`; 409 already resolved; 410 expired |
| `GET /api/sessions/{id}/changes?scope=session\|workspace` | – | `Changes` |
| `GET /api/sessions/{id}/changes/file?scope=…&path=…` | – | `FileDiff` |
| `GET /api/events?session={id}` | – | `text/event-stream` |

`request_id` is a client-generated UUID per submission. A repeated
`request_id` returns the recorded `Submission` without contacting the
provider. A prompt while a turn is active returns 409.

Shapes (fields in `snake_case`):

- `ProviderInfo`: `name`, `display_name`, `available`, `reason`, `capabilities` (`cancel`, `permissions`, `questions`, `session_diff`, `history`).
- `SessionSummary`: `id`, `provider`, `name`, `workdir`, `conversation_id`, `state`, `state_detail`, `open`, `pending`, `created_at`, `updated_at`, `capabilities`.
- `state` is one of `idle`, `starting`, `working`, `awaiting_permission`, `awaiting_answer`, `completed`, `cancelled`, `failed`, `interrupted`, `closed`.
- `SessionDetail`: `SessionSummary` fields plus `items: [Item]`, `interactions: [Interaction]`, `history_truncated`, `last_submission: Submission|null`.
- `Item`, `ToolCall`, `Interaction`, `Option`, `Question`, `Answer`, `FileDiff`: the JSON forms in `internal/agentapi`.
- `Submission`: `request_id`, `status` (`accepted`, `rejected`, `uncertain`), `error`, `time`.
- `Changes`: `scope`, `label` (plain statement of what the scope includes), `supported`, `reason`, `files: [{path, status, additions, deletions}]`.

### Event stream

`GET /api/events?session={id}` first sends one `snapshot` event, then updates.
The snapshot is taken under the same lock that registers the subscriber, so
there is no gap and no duplicate between them. Every event's `data` is JSON
with a `seq` field from one service-wide counter; clients ignore any update
whose `seq` is not greater than the snapshot's.

| Event | `data` |
|---|---|
| `snapshot` | `{"seq", "sessions": [SessionSummary], "session": SessionDetail\|null}` |
| `session` | `{"seq", "session": SessionSummary}` (any session) |
| `item` | `{"seq", "session_id", "item": Item}` (selected session; upsert by `item.id`) |
| `delta` | `{"seq", "session_id", "item_id", "kind", "text"}` (append) |
| `interaction` | `{"seq", "session_id", "interaction": Interaction}` (upsert by `id`) |
| `submission` | `{"seq", "session_id", "submission": Submission}` |

A comment line is sent every 15 seconds. Each subscriber has a bounded queue;
a subscriber that falls behind is disconnected, and the browser's automatic
reconnect receives a fresh snapshot. Provider event processing never waits on
a subscriber.

## Consequences

- Closing the browser or losing the tunnel has no provider-side effect.
- A reboot or `uam web stop` ends running turns. Records keep the exact
  conversation ID, so the conversation can be reopened, but the interrupted
  turn is reported as interrupted and is never replayed.
- Survival across SSH logout depends on the host not killing user processes
  at logout (`KillUserProcesses`); UAM does not change that policy.
- Copilot must be installed and signed in on the Linux host. UAM does not
  download or update it.
- Older `uam` binaries do not know `surface` and would show web records as
  ordinary stopped sessions.

## Projects, Tasks, models, titles and subagents

- Date: 2026-09-24 (decided in issues #140 and #142)

This section extends the contract above and replaces it where they differ.
Only Copilot is registered. Providers share the contracts below, except for
the explicitly advertised context-size capability.

### Projects and Tasks

A **Project** is a directory the user adds. Its **Tasks** are the web
sessions (the routes keep the `sessions` name) whose `web.project_id` names it.

- Projects live in a top-level `web_projects` object in `sessions.json`,
  keyed by a UUID: `{id, name, dir, created_at}`. `dir` is canonical
  (absolute, symlinks resolved) and must be an existing directory when the
  Project is added. A directory has at most one Project, and `dir` never
  changes.
- A Task's record keeps `workdir` (copied from the Project) and gains
  `web.project_id`, `web.model` (empty means the provider default) and
  `web.title` (the provider's title, sanitized and bounded). `name` may be
  empty; browsers show `name || title || "New task"`.
- `web.effort` stores the chosen effort; empty means Default.
  `web.context_size` stores the chosen tier; absent or empty means `default`.
  Context usage is live state and is never written to this record.
- On start, each web record without `web.project_id` is assigned to the
  Project for its `workdir`, which is created (named after the directory)
  when missing, in one store update. Only such records are touched, so the
  migration is idempotent and a removed Project does not come back.
- Removing a Project deletes it and its Task records. Deleting a Task deletes
  its record. Both close open conversations but never delete them at the
  provider, never touch the directory, and are refused with 409 while a
  Task concerned is starting, working or waiting for input.

### Provider contract additions (`internal/agentapi`)

| Addition | Meaning |
|---|---|
| `Provider.Models` → `[]Model{ID, Name, Efforts, ContextSizes}` | The selectable models, their effort levels, and available context tiers with token budgets. Empty or `ErrUnsupported` means the provider default only. A provider that lists models must support `SetModel`. |
| `OpenRequest.Model`, `Effort`, `ContextSize` | Used only when creating a conversation, never as resume overrides. |
| `Conversation.SetModel(ctx context.Context, model, effort, contextSize string) error` | Applies the complete, validated selection from the next turn on. The caller never changes it during a turn. |
| `Item.AgentID`, `Delta.AgentID`, `Interaction.AgentID` | Empty for the main agent, otherwise the provider's subagent instance ID. |
| `Turn.Model` | The model the provider reported for the turn. Not persisted. |
| `EventTitle` (`Event.Title`) | The provider-generated conversation title. |
| `EventSubagent` (`Event.Subagent`) | Upserts `Subagent{ID, ParentToolCallID, Name, Description, Model, Effort, Status, Error, StartedAt, EndedAt}`. Model and effort are optional provider reports. Status is `running`, `completed`, `failed` or `cancelled`; once terminal, later updates are ignored. |
| `EventContext` (`Event.Context`) | Reports main-agent `Context{Used, Limit}` in tokens. Kept in memory only. |
| `History` → `{Items, Subagents}` | Subagent items carry `AgentID`; subagent records are rebuilt from recorded events. |

Copilot mapping: `AgentID` is the event envelope's `agentId`, so a subagent's
prompt, replies and tool calls never enter the main transcript, live or in
history. `subagent.started/configured/completed/failed` map to `EventSubagent`, with
`ParentToolCallID` from `toolCallId`; the second, cancelled completion Copilot
sends on disconnect is ignored. Started events can report the model;
configured events report the resolved model and effort. UAM does not infer
either from the parent Task. `session.title_changed` maps to `EventTitle`,
the last main-agent `assistant.usage.model` of a turn to `Turn.Model`,
`models.list` entries with no policy or an `enabled` policy to `Models`,
and `SessionConfig` to the selection at creation. Switching uses the SDK's
generated `session.model.switchTo` RPC so its result can be checked.

### Models

The catalog is loaded when the service starts and reloaded by `GET /api/meta`
once it is older than five minutes, because entitlements change. A failed
reload keeps the previous catalog. A model outside the catalog is refused
with 400.

### Effort and context

Effort levels come from the selected model's catalog entry, in provider
order. An empty `efforts` list means there is no effort control. A nonempty
effort requires an explicit model other than `auto` and must match a listed
level. Default is stored as an empty string and selected with
`{"effort": ""}`; it does not promise a particular runtime effort.

Context sizes are `{id, tokens}` entries. Copilot advertises `default` and,
when available, `long_context`, using the corresponding billing prompt
budgets. These are not the model's total context window. No known budget
means no size option; `auto` has none. The `context_size` capability enables
per-Task selection and is the only provider-parity exception. A provider
without it accepts only Default. The browser warns that long context may
cost more; the API does not expose prices. Creation defaults to `default`;
an explicit empty `context_size` also selects `default`.

Model, effort and context size are validated together under the Task's
operation lock. On PATCH, an omitted field keeps its current value. A model
change preserves omitted effort and context size only if the new model
supports them; otherwise they reset to Default. Explicit incompatible
choices return 400 without contacting the provider. Creation may leave the
model empty, but an explicit PATCH `{"model": ""}` is 400. Actual changes
during a turn return 409. Settled and archived Tasks also refuse these changes.

With an open conversation, UAM records the selection only after the provider
confirms success. Closed conversations keep the selected values for the
next open, which reapplies all three before sending a prompt. Resume itself
does not override the provider's settings. If reapplication fails, the
prompt is not sent.

Copilot always receives the selected effort and context tier on a switch.
Default effort requires a separate experimental
`session.model.setReasoningEffort("")` call because `switchTo` rejects an
empty effort and omitting it can preserve the previous one. This reset is
not atomic with the switch. A cancelled switch or one requiring compaction
consent returns 502 and leaves the recorded selection unchanged. UAM runs
the provider's compaction preflight without supplying consent. Deferred,
unknown, inconsistent or partially applied results also return 502, mark
the Task failed, and close the conversation so another prompt cannot use
unconfirmed settings. UAM does not attempt a rollback; the next explicit
open must successfully reapply the stored selection.

The optional summary field `context: {used, limit}` contains the latest
main-agent usage report. For Copilot, `limit` is the active prompt budget.
UAM preserves the reported counts, including usage above the limit. The
field is absent before a valid live report and after restart, reopening or
a selection change until another report arrives. It is not reconstructed
from history. Updates use the existing `session` event. Compaction success,
compaction failure and truncation produce transcript notices, including on
history replay; the meter changes only when a fresh usage report arrives.

### HTTP additions and changes

| Method and path | Body | Result |
|---|---|---|
| `GET /api/projects` | – | `{"projects": [Project]}` |
| `POST /api/projects` | `{"dir", "name"?}` | 201 `Project`; 400 not an absolute, existing directory; 409 `{"error", "project_id"}` when the directory has a Project |
| `PATCH /api/projects/{id}` | `{"name"}` | `Project` (empty name resets to the directory's base name); 404 |
| `DELETE /api/projects/{id}` | – | 204; 404; 409 while any of its Tasks is busy |
| `POST /api/sessions` | `{"project_id", "provider", "model"?, "effort"?, "context_size"?, "name"?, "prompt"?, "request_id"?}` | 201 `SessionSummary`; 400 unknown project or invalid selection; 409 when the directory no longer exists. `workdir` is no longer accepted. |
| `PATCH /api/sessions/{id}` | `{"name"?, "model"?, "effort"?, "context_size"?}` | `SessionSummary`; empty `name` shows the title again; 400 nothing to change or invalid selection; 409 selection change while a turn runs; 502 provider refused or did not confirm the selection |
| `DELETE /api/sessions/{id}` | – | 204; 404; 409 while busy |
| `GET /api/sessions/{id}/subagents/{agent_id}` | – | `{"seq", "subagent": Subagent, "items": [Item]}`; 404 |

A `DELETE` without a body needs no `Content-Type`; it still passes the
`Host`, cross-origin and cookie checks.

Shape changes:

- `ProviderInfo` gains `models: [{id, name, efforts: [string], context_sizes: [{id, tokens}]}]` and `capabilities.context_size`.
- `Project`: `id`, `name`, `dir`, `created_at`.
- `SessionSummary` gains `project_id`, `model`, `title`, `last_model` (from the
  latest turn that reported one; live only) and `subagents_running`. `name`
  may be empty. It also gains `effort`, `context_size` (always `default` or
  the selected tier), and optional `context: {used, limit}`.
- `SessionDetail.items` holds only the main agent's items; it gains
  `subagents: [Subagent]`.
- `Item` and `Interaction` gain `agent_id` (omitted for the main agent).
- `Subagent`: `id`, `parent_tool_call_id`, `name`, `description`, `status`,
  `model`, `effort`, `error`, `started_at`, `ended_at` (empty fields omitted).

### Event stream additions

| Event | `data` |
|---|---|
| `snapshot` | gains `"projects": [Project]` |
| `item`, `delta` | gain `agent_id` (omitted for the main agent); they stay on the Task's stream |
| `subagent` | `{"seq", "session_id", "subagent": Subagent}` (selected session) |
| `project` | `{"seq", "project": Project}` (added or renamed) |
| `project_removed` | `{"seq", "project_id"}` |
| `session_removed` | `{"seq", "session_id"}` |

The subagent GET captures `seq`, metadata and items under the same manager
lock used for SSE updates. Browsers buffer subagent events during the fetch
and apply only events with `seq` greater than the response's sequence, in
their original order. This boundary also excludes captured events delivered
after the response. Bounded queues and the stream snapshot rules are unchanged.

## Steer and queue

- Date: 2026-09-24 (decided in #149, built in #151)

A message sent while a turn runs either **steers** that turn or waits in a
**queue** until the turn ends. Steering uses the provider's own mid-turn
delivery. UAM holds the queue itself, because that is the only queue every
provider can offer the same way.

### Queue

- Each Task has its own first-in, first-out queue in memory. It holds at most
  20 prompts, each within the usual prompt size limit. Nothing in it reaches
  the provider while a turn runs.
- When a turn ends `completed`, UAM sends the head through the normal submit
  path with the head's own `request_id`. Steers do not end a turn early,
  because one provider idle covers the prompt and every steer folded into it.
  The head leaves the queue before UAM sends it, so it can no longer be
  cancelled, and it gets the usual `accepted`, `rejected` or `uncertain`
  outcome. UAM never resends it. If UAM sent nothing because a turn was
  running after all, the head goes back to the front.
- The queue **pauses** when a turn ends `cancelled` or `failed`, when the
  conversation exits or is closed, and when a prompt meant to start a turn (a
  drained head or a normal send) comes back `rejected` or `uncertain`. It
  stays paused across later turns until the user resumes or clears it. A
  stopped turn is never followed by queued work on its own. An empty queue is
  never paused.
- Cancelling a queued prompt removes it and never contacts the provider.
  There is no editing or reordering. Cancel and queue again instead.
- Stopping the service drops every queue. UAM does not send the prompts and
  forgets their request IDs. Anything that waits for the service to be idle
  before stopping it should treat a Task with `queued > 0` as busy.

### Steer

- UAM accepts a steer only while a turn runs (`working` or waiting for the
  user). UAM does not change the Task's state for a steer. The turn it joins
  reports its own end, and the queue drains only after that. A steer that
  reaches the provider after that turn ended can start a new turn, which the
  provider reports like any other turn start.
- An accepted steer cannot be withdrawn. When the provider uses it, its user
  item carries `delivery: "steer"`. When the turn is stopped or fails without
  using it, the transcript gets a notice that quotes it, "Steer not
  delivered: the turn was stopped" (or "the turn failed").
- A lost response is `uncertain`. UAM resends nothing.

### Provider contract additions (`internal/agentapi`)

| Addition | Meaning |
|---|---|
| `Conversation.Steer(ctx, prompt)` | Adds the prompt to the running turn. Same outcomes as `Send`, plus `ErrUnsupported` when the provider cannot steer. The adapter tracks delivery and reports an unused steer as an `ItemNotice`. |
| `Item.Delivery` (`delivery`) | `"steer"` on a user item that joined a running turn as a steer. |

`Steer` is its own method, not a mode on `Send`. The two have different
callers and different turn semantics, and a separate method leaves `Send` and
its callers unchanged.

Copilot mapping: `Send` passes `Mode: "enqueue"` explicitly, so a prompt that
reaches a CLI still busy with a turn runs after that turn instead of joining
it. `Steer` passes `Mode: "immediate"` and keeps the returned `messageId`. A
main-agent `user.message` with that `messageId` marks the steer as used, and
`delivery: "steering"` sets `Item.Delivery`. A steer that arrives during the
turn's final model call gets a follow-up call in the same turn, with
`delivery: "queued"`. It counts as delivered but carries no mark. A steer
still unused at an aborted or failed `session.idle` gets the notice, and
`session.abort` drops unused steers. One still unused at a completed idle
reached the CLI after that idle, and the CLI starts a new turn with it. A
main-agent `user.message` with `delivery: "idle"` reports `TurnWorking`, so
that turn is tracked and can be stopped. When a steer arrives, Copilot also moves
a running foreground shell command to the background. That completes the
shell's tool call, and the adapter ignores the partial output the shell keeps
sending under the same call ID. OpenCode, which is not registered, returns
`ErrUnsupported`.

### HTTP additions and changes

| Method and path | Body | Result |
|---|---|---|
| `POST /api/sessions/{id}/prompt` | `{"text", "request_id", "mode"?}` | `mode` is `send` (default), `queue` or `steer`. While a turn runs, `send` is 409, `queue` is 202 `Submission` with `status: "queued"` (409 when the queue holds 20), and `steer` is 202 `Submission` `accepted`, `rejected` or `uncertain` (409 when the provider cannot steer). While no turn runs, `queue` and `steer` send like `send`, except that `queue` joins a queue that is about to drain. |
| `DELETE /api/sessions/{id}/queue/{request_id}` | – | 204, also when already cancelled; 404 unknown; 409 already sent |
| `POST /api/sessions/{id}/queue/resume` | – | 204; sends the head at once when no turn runs |
| `POST /api/sessions/{id}/queue/clear` | – | 204; cancels every queued prompt and unpauses |

A repeated `request_id` for a prompt still in the queue returns its `queued`
`Submission` in any mode and never queues it twice. Once the prompt is
cancelled, `status` is `cancelled`. Once it is sent, the repeat returns the
send's outcome. `last_submission` changes when a prompt is sent or steered,
not when it is queued or cancelled.

Shape changes:

- `Submission.status` gains `queued` and `cancelled`.
- `SessionSummary` gains `queued` (count).
- `SessionDetail` gains `queue: [{request_id, text, queued_at}]` and
  `queue_paused`.
- `Item` gains `delivery` (omitted unless `"steer"`).

A queue change is Task activity and moves `updated_at`.

### Event stream additions

| Event | `data` |
|---|---|
| `queue` | `{"seq", "session_id", "queue": [QueuedPrompt], "paused"}` (selected session), after every queue change |

The service sends a `queue` frame under the same lock as the change it
describes, then the `session` frame with the new `queued` count. The frame
therefore sits in `seq` order with the state around it. Stop turn, for
example, sends `queue` with `paused: true` and then `session` with
`cancelled`.

## Yolo mode

- Date: 2026-09-24 (decided in #150, built in #152)

Until now the service never answered a permission request or a question for
the user. That changes for one case. The service never answers a question, and
it never approves a permission request unless the user turned on yolo for that
Task. Autopilot is left out: only Copilot has it, so it fails the same-way rule
above.

- **Mode.** Each Task has a mode, `safe` (the default) or `yolo`, stored in
  the record's existing `mode` field. Web records were always written `safe`,
  and the store already reads an unknown value as `safe`, so nothing migrates.
  The mode can change at any time, even while a turn runs.
- **Approvals.** On a yolo Task, the service answers each permission request
  with the option the adapter marked `AllowOnce`, which allows that one request
  only. It never picks "allow for this session" or any other option. Requests
  from subagents arrive on the Task and are answered the same way.
- **Exceptions.** Questions always wait for the user. So does a permission
  request that has no `AllowOnce` option: Copilot leaves the mark off when its
  managed policy says a person must approve (`RequiresManagedApproval()`), or
  when the request is of a kind the SDK cannot read.
- **One answer.** The service claims the request exactly as a browser answer
  does, so the first answer wins. A browser answer that arrives while yolo
  answers is refused with 409, and yolo skips a request a browser is already
  answering. A claimed request does not count as pending, so the Task does not
  show "awaiting permission" while yolo answers it. If the provider call fails
  for another reason than the request being gone, the request goes back to the
  user.
- **Switching.** A change applies to requests raised afterwards. Switching to
  yolo also answers the permission requests pending at that moment: the user
  who turns yolo on has just said that every request may run, and leaving the
  one the turn is waiting on for a click would stop the Task anyway. Switching
  back to safe stops approving from the next request on; answers already sent
  stay sent.
- **Record.** An auto-approved request stays in the Task's interactions,
  `answered`, with the resolution `allowed (yolo)`, in the detail and on the
  event stream, so the transcript shows what ran without asking.

This does not use Copilot's `session.permissions.setMode(allow-all)`. That RPC
is experimental, it stores the permission state in the provider conversation,
where `copilot --resume` would inherit it, and OpenCode has no runtime switch
that behaves the same.

### Provider contract additions

| Addition | Meaning |
|---|---|
| `Option.AllowOnce` (`allow_once`) | Marks the decision that allows this one request and nothing more. Copilot marks `approve_once` unless managed policy requires a person or the SDK cannot read the request's kind. OpenCode marks `once`. |
| `Answer.Auto` (never serialized) | Set only by yolo. Copilot then sends `approve_once` without `approvedInteractively`, which it sets for a person's answer. A client cannot set it. |

### HTTP additions and changes

| Method and path | Body | Result |
|---|---|---|
| `POST /api/sessions` | gains `"mode"?` | `safe` when absent; 400 for anything but `safe` or `yolo` |
| `PATCH /api/sessions/{id}` | gains `"mode"?`, alone or with the other Task settings | `SessionSummary`; 400 for an invalid mode, checked before anything changes |

`SessionSummary` gains `mode`. A mode change moves `updated_at`, is written to
`sessions.json`, and sends a `session` frame. `Interaction.options[]` gains
`allow_once`, omitted when false.

## Task lifecycle

- Date: 2026-09-24 (decided in #157)

A Task is **active** until the user settles or archives it. A **settled**
Task is one the user marked complete. It is read-only until reopened. An
**archived** Task is read-only for good. Deleting is the last step, and it is
allowed only after archiving, so a Task is never deleted by a single click.
This replaces the delete and remove rules in the Projects section above.

| Action | From | Preconditions | Effect |
|---|---|---|---|
| Settle | active | not busy (`starting`, `working`, awaiting the user); queue empty; no pending interaction | stage `settled`, `settled_at` set; the provider conversation is closed if open |
| Reopen | settled | none | stage active, `settled_at` cleared; the next prompt reopens the same conversation |
| Archive | active or settled | from active, the settle preconditions | stage `archived`, `archived_at` set; the conversation is closed if open. Final: there is no unarchive. An active Task archived this way keeps `settled_at` empty. |
| Delete | archived | none | removes the UAM record; the provider conversation is not deleted |
| Remove Project | any | every Task in it archived, or none | removes the Project and its archived records |

- **Storage.** The record's `web` object gains `stage` (`settled` or
  `archived`; absent means active), `settled_at` and `archived_at`. A record
  without a stage, and one with a stage this version does not know, loads as
  active, so nothing migrates.
- **Read-only.** A settled or archived Task refuses with 409 every prompt
  mode, the queue routes, and model, effort, context-size and mode changes.
  The Task can be renamed while settled, but not once archived. Viewing it
  never opens its conversation. Settling needs no pending interaction and closes the
  conversation, so a settled Task has nothing to answer.
- **Pending.** Besides the requests that make a Task wait for the user, a
  yolo approval still on its way to the provider counts as pending. Settling
  or archiving waits for it.
- **Stop first.** A Task that is running, starting or waiting for the user
  cannot be settled or archived. The user stops the turn, and answers or
  clears what waits, first. Settling therefore never interrupts work.
- **Retries.** A repeated `request_id` of a prompt sent before the Task was
  settled still returns its recorded outcome.

A settled or archived Task shows no transcript after the service restarts,
because UAM keeps no transcript and does not open the conversation to read it.
The same is true of a closed Task today.

### HTTP additions and changes

| Method and path | Body | Result |
|---|---|---|
| `POST /api/sessions/{id}/settle` | – | 200 `SessionSummary`; 404; 409 wrong stage, busy, queued prompts or a pending interaction |
| `POST /api/sessions/{id}/reopen` | – | 200 `SessionSummary`; 404; 409 unless settled |
| `POST /api/sessions/{id}/archive` | – | 200 `SessionSummary`; 404; 409 when archived, or when active and a settle precondition fails |
| `DELETE /api/sessions/{id}` | – | 204; 404; 409 unless archived |
| `DELETE /api/projects/{id}` | – | 204; 404; 409 unless every Task in it is archived |
| `POST /api/sessions/{id}/prompt`, the queue routes | unchanged | 409 while settled or archived |
| `PATCH /api/sessions/{id}` | unchanged | 409 for `model`, `effort`, `context_size` or `mode` while settled or archived, and for `name` while archived |

`SessionSummary` gains `stage` (omitted while active), `settled_at` and
`archived_at` (omitted while unset). A stage change moves `updated_at`, is
written to `sessions.json`, and sends a `session` frame.

## Stop one subagent

- Date: 2026-09-24 (decided in #160)

`Conversation.CancelSubagent(ctx, agentID)` stops the exact agent instance.
Copilot sends `session.tasks.cancel` with the envelope `agentId`, never the
parent tool-call ID. It does not abort the parent, send another prompt, or
suppress a replacement agent that the parent chooses to launch. OpenCode stays
unregistered and returns `ErrUnsupported` for this operation.

`POST /api/sessions/{id}/subagents/{agent_id}/cancel` takes no body or request
ID and returns 200 with the current `Subagent` record directly. As with other
POST controls, it requires `Content-Type: application/json`, authentication,
and a same-origin request. Unknown Tasks or agents return 404. Only a known
running agent in an active Task with an open conversation can start a stop;
otherwise the route returns 409. A known terminal agent returns 200 without
opening its conversation. Accepted stops are not sent again while the provider
completion event is pending. A provider failure returns 502 and leaves state
unchanged for an explicit retry; a completion that races the reply wins.

The record remains running until the provider reports its terminal status.
Copilot's `subagent.completed` with `cancelled: true` supplies `cancelled`,
published through the existing subagent event and detail response. Parent and
sibling status follows their own provider events.

An accepted stop or a provider-confirmed cancellation expires the target's
pending interactions. Cancelled history and later permission replays cannot
restore them. Cleanup uses `Interaction.AgentID` and preserves other agents'
requests. Copilot SDK v1.0.14 supplies an agent ID for permission events but
its `ask_user` callback supplies only a session ID. Such unattributed questions
stay pending because UAM cannot safely assign them to the stopped subagent.

## Chat with an idle subagent

- Date: 2026-09-24 (decided in #169)

`Subagent.Status` gains `idle`, which is not terminal: the live provider
reports that the subagent finished and takes a follow-up. `failed` and
`cancelled` stay final. `completed` becomes `idle` only on the provider's
report, and `idle` becomes `running` only when UAM's own follow-up is accepted
or its outcome is uncertain; the task list then settles it.
When a conversation closes or its runtime exits, `idle` falls back to
`completed`.

`Conversation.PromptSubagent(ctx, agentID, text)` sends a follow-up to the
exact agent instance. The main agent does not see it and no Task turn starts.
Ambiguous failures wrap `ErrSubmissionUncertain` and are never resent.
OpenCode stays unregistered and returns `ErrUnsupported`.

Copilot reads `session.tasks.list`. A subagent is `idle` only when the entry
for its exact agent ID says `idle` with execution mode `sync`; a background
subagent never is, because a follow-up wakes the main agent. The list is read
after a `subagent.completed` that was not cancelled, and after each
`session.background_tasks_changed` until the subagent is idle again or has
ended. A reopened conversation reads the list once; UAM's own record never
restores `idle`. `session.tasks.sendMessage` targets the agent ID, never the
parent tool call. `sent: false` is a refusal, with the provider's error. The
follow-up's events carry the agent ID and go only to the subagent's
transcript.

`POST /api/sessions/{id}/subagents/{agent_id}/prompt` takes
`{"text", "request_id"}` and returns 202 with a `Submission`, validated as a
Task prompt: 400 for a missing UUID or empty text, 413 over the prompt limit.
A repeated `request_id` returns the recorded outcome without a second send.
These outcomes are kept apart from the Task's submissions, in memory and
bounded the same way; `last_submission`, the queue and the Task's state do not
change. Unknown Tasks or agents return 404. The route returns 409 unless the
Task is active, its conversation is open, it runs no turn and waits for no
answer, and the subagent is `idle`, or when the provider cannot chat with a
subagent. It never opens a conversation. Status changes arrive through the
existing subagent event and detail response.

## Project defaults for new Tasks

- Date: 2026-09-24 (decided in #171)

A Project may keep **defaults for new Tasks**: `{provider, model, effort,
context_size, mode}`. **New task** creates the Task at once with the Project's
defaults and no prompt or name, then opens its empty chat. The first message
goes through the normal composer, and the provider titles the Task from it as
before. This replaces the New Task form.

- **Storage.** A `web_projects` entry gains an optional `defaults` object with
  all five keys, omitted when the Project has none, so nothing migrates. On
  load, defaults with a missing provider, a control character, an oversized
  value, an unknown context size or a mode other than `safe` or `yolo` are
  cleared; the Project stays. An empty context size loads as `default`.
- **Checks on write.** The provider must be registered. Model, effort and
  context size are checked against the provider's models exactly as a Task's
  selection is, so a context size other than `default` needs the provider's
  context-size capability. `mode` must be `safe` or `yolo`; empty is refused.
  An empty context size is stored as `default`. A failed check is 400 and
  changes nothing.
- **Resolved by the browser.** The server applies no defaults: `POST
  /api/sessions` is unchanged and already takes no prompt and no name. The
  browser resolves the defaults against the live models. A default model the
  provider no longer offers falls back to `auto` (or the first model), with no
  effort and context size `default`, so New task never fails on a stale
  default. A Project without defaults uses `auto`, no effort, `default` and
  `safe`.

### HTTP additions and changes

| Method and path | Body | Result |
|---|---|---|
| `POST /api/projects` | gains `"defaults"?` | 201 `Project`; 400 invalid defaults; otherwise unchanged |
| `PATCH /api/projects/{id}` | `{"name"?, "defaults"?}` | `Project`; the name is handled as before; 400 when neither is given or the defaults are invalid, checked before anything changes; 404 |

`Project` gains `defaults: {provider, model, effort, context_size, mode}`,
omitted when the Project has none; `context_size` is `default` unless a tier is
chosen. A change sends a `project` frame, as a rename does.

## Project branch

- Date: 2026-09-24 (decided in #175)

`Project` gains `branch`: the branch checked out in the git work tree that
holds `dir`, linked worktrees included. It is omitted when `dir` is not in a
work tree, HEAD is detached, or git cannot tell within two seconds. UAM runs
`git symbolic-ref --quiet HEAD` through the Changes view's git runner and
cleans the name like other labels. The branch lives only in memory; it is
never written to `sessions.json`.

UAM reads it when the Project is added and re-reads it when Projects are
listed (`GET /api/projects` and the `snapshot` frame, at most once per Project
every two seconds), when a Task in the Project ends a turn, and when a Task's
Changes load. Nothing polls. When the branch changes, the `project` frame
carries the Project again.
