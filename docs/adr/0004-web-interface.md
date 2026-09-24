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
Only Copilot is registered. The provider contract names no provider-specific
concept, so another provider can be added the same way later.

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
| `Provider.Models` → `[]Model{ID, Name}` | The selectable models. Empty or `ErrUnsupported` means the provider default only. A provider that lists models must support `SetModel`, so a model can only be chosen where it can also be switched. |
| `OpenRequest.Model` | Used only when creating a conversation, never on reopen. |
| `Conversation.SetModel` | Switches the model from the next turn on. |
| `Item.AgentID`, `Delta.AgentID`, `Interaction.AgentID` | Empty for the main agent, otherwise the provider's subagent instance ID. |
| `Turn.Model` | The model the provider reported for the turn. Not persisted. |
| `EventTitle` (`Event.Title`) | The provider-generated conversation title. |
| `EventSubagent` (`Event.Subagent`) | Upserts `Subagent{ID, ParentToolCallID, Name, Description, Status, Error, StartedAt, EndedAt}`. Status is `running`, `completed`, `failed` or `cancelled`; once terminal, later updates are ignored. |
| `History` → `{Items, Subagents}` | Subagent items carry `AgentID`; subagent records are rebuilt from recorded events. |

Copilot mapping: `AgentID` is the event envelope's `agentId`, so a subagent's
prompt, replies and tool calls never enter the main transcript, live or in
history. `subagent.started/completed/failed` map to `EventSubagent`, with
`ParentToolCallID` from `toolCallId`; the second, cancelled completion Copilot
sends on disconnect is ignored. `session.title_changed` maps to `EventTitle`,
the last main-agent `assistant.usage.model` of a turn to `Turn.Model`,
`models.list` entries with no policy or an `enabled` policy to `Models`,
`SessionConfig.Model` to the model at creation and `Session.SetModel` to
`SetModel`.

### Models

The catalog is loaded when the service starts and reloaded by `GET /api/meta`
once it is older than five minutes, because entitlements change. A failed
reload keeps the previous catalog. A model outside the catalog is refused
with 400. A model change is refused with 409 while a turn is running; with
the conversation open, the stored model changes only once the provider
accepted the switch. With the conversation closed, the model is stored and
applied by the next open before anything is sent; a conversation that cannot
take it is not used with another model.

### HTTP additions and changes

| Method and path | Body | Result |
|---|---|---|
| `GET /api/projects` | – | `{"projects": [Project]}` |
| `POST /api/projects` | `{"dir", "name"?}` | 201 `Project`; 400 not an absolute, existing directory; 409 `{"error", "project_id"}` when the directory has a Project |
| `PATCH /api/projects/{id}` | `{"name"}` | `Project` (empty name resets to the directory's base name); 404 |
| `DELETE /api/projects/{id}` | – | 204; 404; 409 while any of its Tasks is busy |
| `POST /api/sessions` | `{"project_id", "provider", "model"?, "name"?, "prompt"?, "request_id"?}` | 201 `SessionSummary`; 400 unknown `project_id` or model outside the catalog; 409 when the directory no longer exists. `workdir` is no longer accepted. |
| `PATCH /api/sessions/{id}` | `{"name"?, "model"?}` | `SessionSummary`; empty `name` shows the title again; 400 nothing to change or model outside the catalog; 409 model change while a turn runs; 502 provider refused the switch |
| `DELETE /api/sessions/{id}` | – | 204; 404; 409 while busy |
| `GET /api/sessions/{id}/subagents/{agent_id}` | – | `{"subagent": Subagent, "items": [Item]}`; 404 |

A `DELETE` without a body needs no `Content-Type`; it still passes the
`Host`, cross-origin and cookie checks.

Shape changes:

- `ProviderInfo` gains `models: [{id, name}]`.
- `Project`: `id`, `name`, `dir`, `created_at`.
- `SessionSummary` gains `project_id`, `model`, `title`, `last_model` (from the
  latest turn that reported one; live only) and `subagents_running`. `name`
  may be empty.
- `SessionDetail.items` holds only the main agent's items; it gains
  `subagents: [Subagent]`.
- `Item` and `Interaction` gain `agent_id` (omitted for the main agent).
- `Subagent`: `id`, `parent_tool_call_id`, `name`, `description`, `status`,
  `error`, `started_at`, `ended_at` (empty fields omitted).

### Event stream additions

| Event | `data` |
|---|---|
| `snapshot` | gains `"projects": [Project]` |
| `item`, `delta` | gain `agent_id` (omitted for the main agent); they stay on the Task's stream |
| `subagent` | `{"seq", "session_id", "subagent": Subagent}` (selected session) |
| `project` | `{"seq", "project": Project}` (added or renamed) |
| `project_removed` | `{"seq", "project_id"}` |
| `session_removed` | `{"seq", "session_id"}` |

Sequencing, bounded queues and the snapshot rules are unchanged.
