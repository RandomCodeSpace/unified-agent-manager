# ADR 0004: Web interface through provider APIs

- Status: Accepted
- Date: 2026-09-23

## Context

Users want to start a Copilot task from a browser, close the
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

Only Copilot is registered for now. Web features are built against Copilot
first, and a provider is offered only when it supports them the same way.
Per-Task context size, usage and credits (#188), and importing previous
sessions are capability-gated exceptions to this rule.
The image and PDF gate for attachments is not an exception: it follows what
each model reports, and every provider must apply it the same way.
OpenCode support was removed from the CLI, TUI and web code on 2026-09-26.
Saved records remain on disk for recovery; no OpenCode runtime is launched.

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
the web service only opens `surface: "web"` records. Import can link a new
web Task to a terminal conversation after the in-use check; each surface retains its own record and never deletes the other.

Tokens and transcripts are not written to disk by UAM. Transcript history is
read back from the provider when a conversation is reopened, and read without
opening it for a Task whose conversation is not open (see Task lifecycle).

### Access

The service binds to loopback by default and can explicitly bind another IP
address. Access requires a session cookie obtained by presenting the access
token stored owner-only next to `sessions.json`, or a task-scoped signed file
key on the file-view route. Both credentials are bound to the request `Host`.
Any Host may reach the sign-in page and static assets. State-changing requests
must pass `net/http.CrossOriginProtection` and use the route's required content
type. There is no CORS and no supported anonymous mode.

Both public and private CLI entry points reject `--no-auth`; server and daemon
configuration have no authentication bypass. Token load, creation or validation
failure stops startup before listening. A legacy `web.json` with `no_auth: true`
is retained only for detection: status reports an unsupported insecure process
and `restart_required: true`, and `uam web` refuses to reuse it. The owner must
stop it and start the secure service; no restart or token replacement is automatic.

## HTTP contract

All JSON. All `/api/*` routes except `/api/auth` and `/api/login` require a
cookie, apart from the file-key route which validates its own signed credential.
Errors are
`{"error": "<message>"}` with a 4xx/5xx status.

| Method and path | Body | Result |
|---|---|---|
| `GET /api/auth` | – | `{"authenticated": bool, "required": true}` |
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
| `tool_output` | `{"seq", "session_id", "agent_id"?, "item_id", "text"}` (append to the existing tool's output; opt-in below) |
| `items_trimmed` | `{"seq", "session_id", "items": [{"id", "agent_id"?}]}` (remove evicted transcript items and mark history truncated) |
| `interaction` | `{"seq", "session_id", "interaction": Interaction}` (upsert by `id`) |
| `submission` | `{"seq", "session_id", "submission": Submission}` |

A comment line is sent every 15 seconds. Each subscriber has a bounded queue;
a subscriber that falls behind is disconnected, and the browser's automatic
reconnect receives a fresh snapshot. Provider event processing never waits on
a subscriber.

The browser opts in with `&tool_output=delta`. Output-only appends to running
tools then send only the new text; starts, rewrites, metadata changes and
completion still send full `item` events. Clients without the parameter retain
the full-item protocol. Sequence numbers are monotonic, not contiguous. Full
snapshots and subagent detail responses retain the complete current output.
The browser batches transcript updates once per animation frame while visible.

When the retained transcript exceeds the item or byte budget, `items_trimmed`
follows the update that triggered eviction. IDs are scoped by agent, with an
absent `agent_id` identifying the main agent. Browsers remove those items from
the main and open subagent transcripts. A subagent fetch replays newer trims
alongside other buffered frames; trims already covered by its snapshot are
ignored. Closing its panel aborts the fetch and releases its transcript and
buffer. Main history and detail hydration each reserve at most 256 frames and
2 MiB of serialized string data, charged at two bytes per UTF-16 code unit. The
two owners therefore share a 4 MiB allowance. Overflow discards the affected
buffer and offers an explicit Retry
from a fresh snapshot instead of displaying incomplete output.

The browser negotiates `history=recent` on `GET /api/events?session={id}` and
`GET /api/sessions/{id}`. Initial snapshots and replacement `history` frames
then carry only the newest page plus `history_before`, an opaque cursor. An
empty cursor means the beginning of retained history; an absent cursor means
the server uses the legacy full-history contract. Other clients retain that
contract unless they opt in.

`GET /api/sessions/{id}/history?before={cursor}` uses the same session-cookie
authentication as the detail route and returns `{seq, items, before}`. Pages
are oldest first, at most 50 complete items with a 64 KiB item-data target.
One oversized item is returned alone and unmodified to guarantee progress.
The cursor names an item boundary, so concurrent appends cannot shift pages.
Invalid cursors return 400, missing tasks 404, and an evicted/replaced boundary
409, using the existing `{error}` response shape. A 409 starts a fresh recent
snapshot. Reads do not open or prompt a provider conversation.

Approaching the top while scrolling fetches the preceding page automatically;
there is no “Show earlier messages” button. One page loads at a time, with a
10-second timeout, cancellation on navigation, and the visible message anchored
when rows are prepended. A failed read reports an inline status and retries on
the next upward scroll. Older servers reveal already-downloaded rows through
the same scrolling behavior. Locating an older subagent also loads preceding
pages until its parent row is available.

Paged clients distinguish new `item` frames by `append: true`; updates to
unfetched items wait for their page. During a page read the browser buffers
main-agent item/output/trim frames within its 256-frame and 2 MiB reservation.
It replays only frames newer than the response's `seq`, and
ignores subsequently delivered frames already represented by each older item.
The response never advances the live stream's sequence watermark. Replacement
history and reconnect snapshots invalidate pending pages. Existing server
retention limits still apply; pagination cannot recover history already trimmed
by the provider or service.

### Compact transcript and revealed bodies

The web client also requests `view=compact-v1`. The existing snapshot and task
detail declare `representation: "compact-v1"`, `detail_stream: true`, and an
opaque service-instance `epoch`. No marker means the legacy path; empty tasks
negotiate through the envelope too. Legacy callers retain their full items and
completion frames. API reads keep the existing authentication, host checks and
`Cache-Control: no-store` behavior.

Compact items retain user/assistant text and tool identity, state, timestamps,
images and display fields. Tool input/output and reasoning text are deferred.
`tool.display_arg` is for labels, while `tool.path` preserves full file identity.
`has_input`, `has_output`, and `item.compact` presence flags distinguish an
unloaded body from an empty result. Question tools retain the input/output needed
to show their questions and answers. These semantic exceptions still use the
existing retention limits and may exceed a page's soft byte target.

Closed subagents receive bounded `preview` and `result_summary` fields, not their
transcript events. Text preview publication is coalesced; actionable status and
final changes are immediate. An opened subagent uses recent compact history and
loads older pages through the same upward-scroll behavior as the main task.

One main EventSource owns the selected task's chat, compact records, interactions,
status and global state. At most one optional detail EventSource owns revealed
bodies and one open subagent transcript. Opening a detail does not reconnect the
main stream. The detail URL is `/api/events/detail?session={id}`, with optional
`agent={id}` and up to eight repeated `item` parameters. Each parameter is a
URL-encoded JSON pair `[agent_id,item_id]`; an empty agent ID means the main
agent. The browser canonicalizes the interest set and suspends child interests
when a parent disclosure closes.

The detail subscriber registers and captures data under one manager lock.
`detail_snapshot`, individual `body` frames, optional `detail_page` frames, and
`detail_ready` share that snapshot barrier; later queued events follow them.
A reconnect supplies inclusive `agent_before` and `agent_until` boundaries for
the held subagent window. The server sends a recent-tail `detail_snapshot` with
`range: true`, then `detail_page` frames with `scope: "window"` and both boundary
cursors. It refreshes that window without hydrating the gap between it and the
recent tail. Missing boundaries or an over-budget range produce an explicit
`range_reset` and a bounded recent fallback. All pages share one captured barrier
and are staged until readiness. Live compact agent events
use `item`, `delta`, and `items_trimmed`; revealed bodies use `body` replacement,
`body_delta`, or `body_output`. A real history replacement sends `detail_reset`.

Sequences are comparable only within one epoch. A compact mutation precedes its
authoritative body replacement, and the browser tracks each affected body's
coverage separately from unrelated main or detail events. Completion does not
make a body immutable: final-only output, final suffixes, rewrites and later
corrections still reach an open view. Request/connection identity also prevents
callbacks from abandoned loads applying to a new selection.

Recreating the detail connection originally resent every continuing body. The
measured large-body expansion fixture transferred 27.9 MB for 21 snapshots,
compared with a 1.33 MB legacy snapshot. Continuing bodies can therefore supply a
third tuple member, their fully applied sequence, plus the matching `epoch`
query. A bounded server map records each retained item's last mutation sequence.
When that proves the held body current, `body_current` acknowledges it at the new
barrier; otherwise the full body is sent. Every mutation updates the evidence,
and trimming/history eviction removes it. Closed bodies retain no reusable
coverage. The same fixture then transferred 1.34 MB total, including about 15 KB
for the 20 reconfigurations after initial delivery.

`GET /api/sessions/{id}/items/{item_id}?agent_id={id}` returns a complete retained
item with task/agent identity, epoch and sequence for one-off copy actions. Its
response is a point-in-time value and never hydrates the live body store. Compact
subagent reads accept `view=compact-v1`; their older-page route is
`/api/sessions/{id}/subagents/{agent_id}/history?before={cursor}`. Reads never open
or prompt a provider conversation.

Compact main and subagent history routes accept either `before` or `after`, and
return both boundary cursors. The named boundary is exclusive. Supplying both
directions, a repeated cursor, or a malformed cursor returns 400; a missing task
returns 404 and an evicted boundary returns 409. Legacy responses keep their
existing shape.

Each active transcript keeps a contiguous reading window of at most 150 items
and 4 MiB of accounted data, plus a separate recent tail of at most 50 items
under the same byte allowance. One oversized item remains intact to allow
progress. Compact metadata preserves IDs, order, kinds and required semantic
state for up to 2,000 retained items; it excludes chat and body text, images and
display strings. Live corrections update held items without resurrecting
evicted rows. Scrolling back toward newer history refetches evicted pages.
Jump to latest uses the current tail.

Evicted rows leave bounded spacer records containing IDs and measured heights.
Partial reloads estimate the remaining spacer height, then compensate using the
visible row's actual position. Disclosure identities survive backward extension
and discovery of an earlier user prompt. Evicting the focused row returns focus
to the conversation unless the user has moved it to another control.

### Recent task presentation and scroll commits

The browser retains at most five recent compact task pages, with a 16 MiB
accounted-data limit. Entries contain at most 50 recent items under the same
64 KiB soft page target. One oversized item remains complete, but an entry that
exceeds the cache allowance is not admitted. String and object accounting bounds
cache-owned data; it is not a measurement of total JavaScript heap. Older pages,
revealed bodies and subagent transcripts are excluded. Entries are projected
when leaving a confirmed task, avoiding full-page serialization per token.

A cached selection shows matching content and a "Refreshing task..." status
until the main snapshot confirms it. Cached status cannot enable sending,
stopping, approvals, history reads or detail subscriptions. The current pane
guard also keeps typing inactive until confirmation. Drafts stay in their
existing storage and survive cache eviction. Authentication, capability or epoch
changes clear the cache; history replacement, trims and deletion invalidate
affected entries. A late HTTP page cannot merge across epochs.

Ordinary history prepends use a React transition. `HistoryAnchor` captures the
visible row immediately before React commits and restores its offset afterward,
so a slow response anchors to the reader's current position. Parent-locate keeps
the synchronous commit needed to find its target. Upward wheel, touch or keyboard
demand can fetch one earlier page within three viewport heights, capped at
1,600 pixels. There is no initial or recursive history prefetch.

The unread-state callback is created outside the App render scope. Heap snapshots
showed that otherwise shared callback contexts could retain previous App renders
and their transcripts after navigation. The extracted factory captures only the
selected ID, visit times and mount time.

### HTTP compression

After the existing security checks, the routing boundary negotiates gzip for
API JSON, both event streams and embedded text, JavaScript and SVG assets.
It uses the standard library's `gzip.BestSpeed`; no dependency or configuration
setting is added. Missing or refused gzip support retains identity responses.
An explicit gzip exclusion overrides a wildcard. Responses preserve existing
`Vary` fields and include `Accept-Encoding` when needed.

Compressed responses discard the original `Content-Length`. HEAD describes the
negotiated representation without allocating a compressor or sending a body.
Binary, already encoded, bodyless, range and `Content-Disposition` responses
remain identity, preserving task-file and attachment validators and ranges.

Each SSE flush first flushes gzip, then the HTTP response. The wrapper exposes
the underlying writer to `ResponseController`, preserving the existing write
deadlines and cancellation. Compression therefore does not wait for a stream
to close before delivering an event or heartbeat. On return, pooled writers are
closed and reset to `io.Discard` so they retain no response or connection.

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
per-Task selection and is the first provider-parity exception. A provider
without it accepts only Default. The browser warns that long context may
cost more; models carry their token prices since #188 (see Usage and
credits). Creation defaults to `default`;
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

Copilot mapping: `Send` passes no mode and returns `ErrBusy` while a
foreground turn runs. With no mode the CLI delivers the prompt at once when
its main agent is idle, even while a background shell runs. An explicit
`Mode: "enqueue"`, or any prompt sent during a turn, is held until
`session.idle`, which CLI 1.0.88 withholds while a background shell runs, so
such a prompt could wait forever. `Steer` passes `Mode: "immediate"` and keeps the returned `messageId`. A
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
sending under the same call ID.

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
where `copilot --resume` would inherit it.

### Provider contract additions

| Addition | Meaning |
|---|---|
| `Option.AllowOnce` (`allow_once`) | Marks the decision that allows this one request and nothing more. Copilot marks `approve_once` unless managed policy requires a person or the SDK cannot read the request's kind. |
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

### Transcripts of Tasks that are not open

- Date: 2026-09-24 (decided in #193; replaces "a settled or archived Task
  shows no transcript after the service restarts")

Viewing a Task whose conversation is not open reads its recorded transcript
without opening the conversation. That covers settled and archived Tasks,
closed and failed ones, and every Task after the service restarts until its
conversation opens again. Viewing means `GET /api/sessions/{id}`, the
snapshot of `GET /api/events?session={id}`, and `GET
/api/sessions/{id}/subagents/{agent_id}`.

- **Read-only.** Reading sends nothing, changes neither mode nor model, and
  leaves the Task as it was: its stage, state and `updated_at` stay, and a
  settled or archived Task stays read-only. A failed read changes nothing
  either; the detail says why.
- **Copilot.** The adapter reads the persisted journal with
  `sessions.readPersistedEvents` (experimental). It does not create, resume
  or activate the session, so it takes no in-use lock and starts no hook or
  MCP server. It retains at most 16 MiB of encoded events and 64 pages,
  newest first. A longer journal keeps its newest part and sets
  `history_truncated`. Older pages are drained without retention to finish
  the SDK snapshot and release its handle. The SDK exposes no release-cursor
  call. Cancellation is bounded by the read context; a cursor left after an
  interrupted continuation expires after five idle minutes. The experimental
  API excludes ephemeral events and may omit payloads reconstructed only
  for an active session. The transcript is rebuilt by the same code as a
  reopen. Measured with CLI 1.0.88 on a Task settled after two turns with a
  subagent, and on one whose CLI was killed with an idle subagent: reading
  left `events.jsonl` unchanged (size, modification time and SHA-256), and the
  items, subagents and attachment digests matched a resume. A resume with
  `getMessages` and a disconnect also wrote nothing to `events.jsonl`, but it
  takes the in-use lock while it reads, removed a dead holder's stale lock
  files, and activates the session, so it is not used for reading.
- **Missing journals.** CLI 1.0.88 returns an unstructured RPC -32603 error
  containing `journal is unavailable` for an absent or unreadable journal.
  The adapter maps that text to `ErrConversationNotFound`; a recorded-error
  test pins the match. It cannot distinguish a missing journal from an
  unreadable one.
- **State.** `SessionDetail` gains `history`: `loaded`, `loading` or
  `unavailable`, and `history_reason` when unavailable. Detail also has `seq`;
  browsers discard an older detail response after a newer history event.
  The first view answers `loading` at once and starts the read; a `history` event then
  carries the items. A conversation that opens reads the record itself and
  also sends a `history` event.
- **Bounds.** At most two reads run at a time; views of a Task that is being
  read share its read. A transcript read this way stays in memory and is
  dropped after 10 minutes without a view while no event stream watches the
  Task; the next view reads it again. A failed read is tried again on a view
  a minute later. Encoded retained items and subagent metadata are capped at 16 MiB per history,
  keeping its newest items; the history frame stays below the 32 MiB
  subscriber limit. Cached histories share a 64 MiB budget with
  least-recently-viewed eviction, including watched Tasks under pressure.
  A delete or a send cancels an in-flight read. Import uses the HTTP request
  context and waits at most five seconds for a read slot, then returns 503.
- **Subagents.** Nothing of a conversation that is not open runs, so a
  subagent the record leaves running shows as cancelled, as it does when a
  conversation closes.

| Provider contract addition (`internal/agentapi`) | Meaning |
|---|---|
| `HistoryReader.ReadHistory(ctx, ReadRequest{ConversationID, Workdir}) (History, error)` | Optional. Reads a record without opening the conversation; `ErrConversationNotFound` when the ID has none |
| `History.Truncated` | Only the newest part of a record too large to read whole was returned |

| Event | `data` |
|---|---|
| `history` | `{"seq", "session_id", "history", "history_reason"?, "history_truncated", "items": [Item], "subagents": [Subagent]}`: replaces the main-agent items and the subagents the browser shows |

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

| Method and path | Body | Result |
|---|---|---|
| `GET /api/sessions/{id}` | – | `SessionDetail` gains `seq`, `history` and `history_reason`; a Task whose conversation is not open answers `loading` at once, and a `history` event follows |

## Stop one subagent

- Date: 2026-09-24 (decided in #160)

`Conversation.CancelSubagent(ctx, agentID)` stops the exact agent instance.
Copilot sends `session.tasks.cancel` with the envelope `agentId`, never the
parent tool-call ID. It does not abort the parent, send another prompt, or
suppress a replacement agent that the parent chooses to launch.

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

## Task defaults for new Tasks

- Date: 2026-09-24 (decided in #171); moved from each Project into Settings on
  2026-09-25

One setting, shared by every browser, says what a **new Task** starts with:
`{provider, model, effort, context_size, mode}`. **New task** opens a draft on
these defaults (see "Create a new Task only on its first message"); the first
message goes through the normal composer, and the provider titles the Task
from it as before. Until 2026-09-25 each Project kept its own defaults, set in
the Add and Edit project dialogs; that was more than the owner wanted to
manage per Project, so the defaults are one section of Settings and Projects
carry none.

- **Storage.** `web_settings` gains an optional `task_defaults` object with
  all five keys, omitted until set. On load, defaults with a missing
  provider, a control character, an oversized value, an unknown context size
  or a mode other than `safe` or `yolo` are cleared. An empty context size
  loads as `default`.
- **Migration.** A `web_projects` entry written before this change may carry
  a `defaults` object. On load, when `task_defaults` is unset, the valid
  defaults of the most recently created Project that has any become the
  setting; every Project's `defaults` is cleared in memory, so the next save
  drops the key. A set `task_defaults` is never replaced by a Project's.
- **Checks on write.** The provider must be registered. Model, effort and
  context size are checked against the provider's models exactly as a Task's
  selection is, so a context size other than `default` needs the provider's
  context-size capability. `mode` must be `safe` or `yolo`; empty is refused.
  An empty context size is stored as `default`. A failed check is 400 and
  changes nothing; the same value again writes nothing.
- **Resolved by the browser.** The server applies no defaults to `POST
  /api/sessions`, which already takes no prompt and no name. The browser
  resolves the setting against the live models. A default model the
  provider no longer offers, or hidden in Settings, falls back to `auto` (or
  the first visible model), with no effort and context size `default`, so New
  task never fails on a stale default. With the setting unset, a new Task
  starts with the provider's own defaults: `auto`, no effort, `default` and
  `safe`.
- **Import** takes the setting's model, effort and context size when the
  conversation's own model is no longer offered, and its mode.

### HTTP changes

| Method and path | Body | Result |
|---|---|---|
| `PATCH /api/settings` | gains `"task_defaults"?: {provider, model, effort, context_size, mode}` | `Settings`; 400 when it is not an object or fails the checks above |
| `POST /api/projects` | `{"dir", "name"?}` | 201 `Project`; a `defaults` key is ignored |
| `PATCH /api/projects/{id}` | `{"name"}` | `Project`; 400 without a name; a `defaults` key is ignored; 404 |

`Settings` gains `task_defaults`, omitted until set; `context_size` is
`default` unless a tier is chosen. A change sends a `settings` frame.
`Project` no longer has `defaults`.

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

## Listening beyond loopback

- Date: 2026-09-24 (decided in #174)

The original #174 decision allowed listening beyond loopback and optional
unauthenticated access. The secure-only decision in #227 on 2026-09-26 supersedes
that optional access mode. Non-loopback binds, header logging and owner-set tokens
remain supported; sign-in is mandatory on every bind.

- **Listen.** `--listen` takes any IP literal: loopback, the unspecified
  address (`0.0.0.0` or `::`), or an interface address. `localhost` means
  `127.0.0.1`; other host names are refused. The default stays
  `127.0.0.1:8260`. The service binds exactly the literal's address family,
  so `0.0.0.0` does not become a dual-stack `[::]` listener.
- **Rebinding rule.** Any `Host` can reach sign-in and static assets. A rebound
  website has no cookie or file key for its own Host. Credentials are Host-bound;
  cross-origin, JSON and CSP checks remain. The cookie is `Secure` behind TLS
  or an HTTPS proxy.
- **Local URL.** `uam web` and `uam web status` show the listen address and a
  URL this host can use: a wildcard bind maps to loopback on the same port
  (`0.0.0.0` to `127.0.0.1`, `::` to `::1`), and any other address is used
  as is. `status --json` keeps `listen` and `url`. `status` and `stop` read
  `web.json` and signal the verified PID; they never connect to the service.
- **Warning.** Whenever the listen address is not loopback, `uam web` and
  `uam web status` print `Warning: listening on <addr>, so other machines can
  reach this service; sign-in is required`. Status for an unsupported legacy
  daemon instead says it has no authentication and requires restart.
- **Header logging.** `--log-headers`, off by default, logs one JSON record
  per request where the checks decide: method, path, remote address, `Host`,
  every header and the outcome. Headers that can carry a credential are
  redacted (`Cookie`, `Authorization`, `Proxy-Authorization`, and any whose
  name contains `auth`, `cookie`, `token`, `key`, `secret`, `session`, `jwt`,
  `signature`, `password` or `credential`), values are capped at 512 bytes, and
  bodies are never read, since `/api/login` carries the token in its body.
- **Owner-set token.** `uam web token set` reads a token of 24 to 256
  printable ASCII characters without whitespace from stdin, without echo at
  a terminal, and atomically replaces `web-token`. It takes no argument, flag
  or environment variable, which `ps`, shell history or the agents' inherited
  environment would expose. It refuses while the service runs, so the running
  service never holds a token the file no longer has. Cookies are HMACs keyed
  by the token, so a service restarted with a new token rejects every earlier
  cookie.

## Slash commands and file references

- Date: 2026-09-24 (decided in #176, from the research for #172)

Neither provider parses prompt text, so UAM resolves `/` commands and `@`
files itself and sends each provider its structured form. `$`, `!`, `#` and
`@agent` are left out by owner decision. Text that starts with them is sent as
plain text, and so is a `/word` that is not a listed command.

- **Commands.** A Task offers only commands that become a prompt. Copilot lists
  `session.commands.list` with built-ins and skills, keeps every `skill` and
  the built-ins `init` and `review`, and drops the rest: they duplicate web
  controls, change the mode, only print text, or reach outside the Task.
  UAM drops names with spaces
  or control characters and sanitizes descriptions and hints.
- **Running a command.** The command route has the rules of a send: the same
  `request_id` record, 409 while a turn runs, and no queue or steer. The name
  must be on the Task's list, else 404, and nothing is recorded. Copilot runs
  `session.commands.invoke` and sends the result only when it is an
  `agent-prompt` without `mode`, with `displayPrompt` set to `/name arguments`.
  Any other result, or an invoke error, is a `rejected` submission; invoke
  starts no turn, so nothing reached the model.
- **File list.** `git ls-files --cached --others --exclude-standard -z` runs in
  the Task's directory through the read-only git runner the Changes view uses,
  so `.gitignore` applies and paths are relative to that directory. Output is
  capped at 4 MiB. UAM adds parent directories, matches in Go (base-name
  prefix, then base name, then path, case-insensitive) and checks each result
  on disk: symbolic links, special files and paths that are gone are left out.
  A directory outside Git gives an empty list with the Changes view's reason.
- **File references.** A prompt names at most 20 files relative to the Task's
  directory. UAM opens the directory with `os.Root` and refuses, with a 400
  that names the path, anything absolute, with `..`, not in clean form, at or
  inside a symbolic link, missing, special, or binary (a NUL in the first 8000
  bytes, as git decides). Directories are allowed. Nothing is sent or recorded
  when one fails. A queued prompt keeps its files, and UAM checks them again
  when the prompt is sent; a failure then is a `rejected` submission that
  pauses the queue. A steer takes files too, checked the same way. The
  text stays as typed, `@path` tokens included. Copilot receives
  `AttachmentFile` or `AttachmentDirectory` with the absolute path and the
  relative path as display name; the model gets a `<tagged_files>` pointer and
  reads the file with its tools, which asks a read permission in Safe mode.

### Copilot configuration discovery

The owner turned on `EnableConfigDiscovery` for created and resumed
conversations. A probe on 2026-09-24 (CLI 1.0.88, SDK 1.0.14, `gpt-5-mini`,
a temporary Git project with a project skill, a project agent,
`.github/copilot-instructions.md`, `.mcp.json` and `.github/hooks/*.json`)
compared the runtime default with discovery on:

| What | Default (nil) | Discovery on |
|---|---|---|
| Skills | 2 built-in | 57: project `.github/skills`, `~/.agents/skills`, built-in |
| Commands | 33 built-in | 33 built-in and 52 skills |
| Custom agents | none | the project agent |
| Custom instructions | loaded (`instructions.getSources` listed the file, and the model followed it) | the same |
| Hooks in `.github/hooks/` | ran (`sessionStart`, `userPromptSubmitted`) | the same |
| MCP servers | none | the built-in `github-mcp-server`, connected |
| Workspace `.mcp.json` | not loaded | not loaded; the CLI loads workspace MCP only for a trusted folder |
| Plugins | none | none |

So discovery adds skills, project agents and the built-in GitHub MCP server.
Custom instructions and file hooks load either way. Hooks therefore already
ran shell commands in web Tasks before this change, without a permission
request. MCP servers from a trusted folder's `.mcp.json` or
`.github/mcp.json`, and from a user MCP configuration, were not observed: the
probe folder was not trusted, and this host has no user MCP configuration.

### Provider contract additions (`internal/agentapi`)

| Addition | Meaning |
|---|---|
| `Conversation.Send(ctx, Prompt)` | `Prompt{Text, Files}` replaces the prompt string. `File{Path, Rel, Dir}` is a reference the web service already checked. |
| `Conversation.Commands(ctx)` | `[]Command{Name, Description, Kind, InputHint}`; `Kind` is `skill` or `command`. |
| `Conversation.RunCommand(ctx, name, args Prompt)` | Runs a listed command with `args.Text` as its arguments. Same outcomes as `Send`; a result that would not start a prompt turn is a rejection. |

### HTTP additions and changes

| Method and path | Body | Result |
|---|---|---|
| `GET /api/sessions/{id}/commands` | – | `{"commands": [{"name", "description", "kind", "input_hint"}]}`; opens the conversation as a viewer does; 409 when it is not open |
| `GET /api/sessions/{id}/files?q=&limit=` | – | `{"files": [{"path", "type"}], "reason"}`; `type` is `file` or `directory`; `limit` 1 to 200, default 50, else 400 |
| `GET /api/projects/{id}/files?q=&limit=` | – | The same list for a Project's directory, for a new Task that does not exist yet; 404 for an unknown Project |
| `POST /api/sessions/{id}/prompt` | gains `"files"?: [string]` | 400 naming a refused path, or for files on a steer during a turn |
| `POST /api/sessions/{id}/command` | `{"request_id", "name", "arguments", "files"?}` | 202 `Submission`; 400 invalid `request_id` or name, or a refused file; 404 not a listed command; 409 while a turn runs; 413 arguments over the prompt limit |

`QueuedPrompt` gains `files` (omitted when empty).

## Browser attachments

- Date: 2026-09-24 (decided in #177, from the research for #172)

The browser uploads a file to UAM first and then names it by ID in a prompt.
No request carries base64 in JSON.

- **Upload.** `POST /api/sessions/{id}/attachments?name=<file name>` takes the
  raw file as the body with `Content-Type: application/octet-stream`. That
  route alone has a 10 MiB body cap; every other route keeps the 1 MiB cap and
  the JSON rule. The server makes one exception to the JSON rule, for exactly
  that method and path. The Host, cross-origin and sign-in checks still apply,
  and `application/octet-stream`, like JSON, is not a type a cross-site form
  can send. The name is for display only: UAM keeps the base name, sanitized
  and clipped to 120 characters.
- **Types.** UAM takes the type from the bytes, never from the name or a
  header. An image is what `http.DetectContentType` calls png, jpeg, gif or
  webp. A PDF must be `application/pdf` there and start with `%PDF-`. Anything
  else that is valid UTF-8 and holds no NUL byte is text, whatever
  `http.DetectContentType` makes of its first bytes; UAM stores and sends it as
  `text/plain`. A text file whose first element is `<svg>` is refused. HEIC,
  audio, video, archives and executables fail the text rule (415). HTML passes
  as text and is never served as HTML.
- **Limits.** Images 3 MiB, PDFs 10 MiB, text 256 KiB (413). A prompt carries
  at most 5 uploads, and no more images than the model's `max_images` (400).
- **Model gate.** The owner chose to gate images and PDFs per model. Copilot's
  `capabilities.supports.vision` allows images, and
  `capabilities.limits.vision` gives `max_prompt_images` and
  `supported_media_types`; a PDF needs `application/pdf` in that list. A model
  that reports neither, such as `auto`, is not gated. Text is never gated. UAM
  checks the gate at upload and again when the prompt goes out, so a model
  change cannot slip an image past it.
- **Storage.** Uploads live in `web-attachments/<task id>/` next to
  `sessions.json`, never in a project directory. Directories are 0700 and
  files 0600: `<upload id>` holds the bytes and `<upload id>.json` the name,
  type, size, SHA-256 and the times it was stored and first sent. Deleting a
  Task or removing its Project deletes its directory. At start UAM reads the
  records and deletes the directories of Tasks it no longer has. An upload
  that no sent or queued prompt carries expires after 24 hours; UAM checks at
  start, after each upload and every hour. A sent upload lives as long as its
  Task, because the transcript shows it.
- **Sending.** A prompt, a queued prompt, a steer and a command take
  `attachments: [id]`. Copilot CLI 1.0.88 folds a steer's blobs and file
  references into the running turn like its text. A queued prompt keeps the IDs,
  and UAM reads the bytes when it sends; a missing file then is a `rejected`
  submission that pauses the queue. A retried `request_id` returns the
  recorded outcome, so nothing is uploaded or sent twice. Copilot receives each
  upload as an `AttachmentBlob` with base64 data, the MIME type and the name as
  display name, except a PDF: UAM hard-links it to `<id>.d/<name>.pdf` beside
  the stored copy (through `os.Root`) and Copilot receives that path as an
  `AttachmentFile`. Copilot CLI passes a document to the model natively only
  where its model client supports the type (it reported none for GPT-6 Luna,
  and a PDF blob then reached the model as a bare name); for a file it
  otherwise gives the agent the path, to read with whatever tools the host
  has (UAM ships none; `pdftotext` is poppler-utils). The
  sweep removes the `.d` directory with its upload.
- **Transcript.** User items carry `attachments: [{"id"?, "name", "mime",
  "size"?}]`. Copilot's live `user.message` holds the blob data, and its
  recorded event holds `assetId: "sha256:<hex>"` and `byteLength` instead; a
  PDF is a file attachment under `web-attachments/<task>/<id>.d/`, which the
  adapter hashes from disk, and any other file attachment is a reference, not
  an upload. Such a PDF is `not_native` unless the message's
  `supportedNativeDocumentMimeTypes` lists its type and
  `nativeDocumentPathFallbackPaths` does not list its path; the browser then
  warns under it that the agent had to read the file with host tools. The adapter reports the
  SHA-256 of the content, and UAM gives each item attachment the ID of this
  Task's stored upload with the same content. Matching on content works for
  Copilot after a reload or a restart, and needs no message ID from the
  send, which Copilot does not return. An attachment without a stored copy has
  no `id`, and the browser shows it as a chip without a preview.
- **Serving.** `GET /api/sessions/{id}/attachments/{attachment_id}` returns a
  stored upload to a signed-in browser with the sniffed `Content-Type`
  (`text/plain; charset=utf-8` for text), `X-Content-Type-Options: nosniff`,
  the service CSP and `Cache-Control: no-store`. Images are `inline`; PDFs and
  text are `attachment`. `mime.FormatMediaType` encodes the file name. The
  browser shows images from this route, never from `blob:` URLs, and the
  existing `img-src 'self'` allows it.

### Provider contract additions (`internal/agentapi`)

| Addition | Meaning |
|---|---|
| `Model.Media *Media` | `Media{Images, PDF, MaxImages, Types}`; nil means the model reports nothing and is not gated. `MaxImages` 0 means no limit; empty `Types` means any type `Images` and `PDF` allow. |
| `Prompt.Attachments []Blob` | `Blob{Name, MIME, Data, Path}`: checked upload bytes to send inline; `Path`, set for a PDF, is a copy under the file's own name for a provider that reads documents from disk. |
| `Item.Attachments []Attachment` | `Attachment{ID, Name, MIME, Size, NotNative, SHA256}`. Adapters fill `Name`, `MIME`, `Size` and `SHA256`; UAM sets `ID`. `SHA256` never reaches a browser. |

### HTTP additions and changes

| Method and path | Body | Result |
|---|---|---|
| `POST /api/sessions/{id}/attachments?name=` | the raw file, `Content-Type: application/octet-stream` | 201 `{"id", "name", "mime", "size"}`; 400 empty file or refused by the model gate; 404 unknown Task; 409 settled or archived Task; 413 over a limit; 415 wrong request type or file type |
| `GET /api/sessions/{id}/attachments/{attachment_id}` | – | the file; 404 for an unknown Task or attachment, or another Task's |
| `POST /api/sessions/{id}/prompt` | gains `"attachments"?: [string]` | 400 for an unknown ID, more than 5, too many images, a gate refusal, or attachments on a steer |
| `POST /api/sessions/{id}/command` | gains `"attachments"?: [string]` | the same checks |
| `GET /api/meta` | each model gains `"media"?: {"images", "pdf", "max_images"?, "types"?}` | absent when the model reports nothing |

`QueuedPrompt` gains `attachments: [{"id", "name", "mime", "size"}]`, omitted
when empty.

## Project badges

- Date: 2026-09-24 (decided in #184)

Each Project has a badge: two characters on a colour, taken from its name.
The owner asked for text from the Project's name and a random colour.

- **Text.** Two uppercase ASCII letters or digits, unique among Projects. The
  first is the name's first ASCII letter or digit, or `P` when it has none.
  The second is the last ASCII letter or digit, so "config" gets `CG` and
  "configuration" gets `CN`. When that pair is taken, choose a random remaining
  letter or digit from the name. When those pairs are taken, or the name has
  one character, use a random free A–Z, then any free pair. Only past 1,296
  Projects can a text repeat; once all pairs are used, keep stored duplicates.
- **Colour.** A key from a fixed palette of ten tones: `red`, `orange`,
  `amber`, `lime`, `green`, `teal`, `cyan`, `blue`, `violet`, `pink`. UAM
  picks at random among the tones no other Project uses, or among all ten
  once each is used. The browser maps each key to a colour of the one theme;
  the server stores and sends only the key.
- **Assignment.** Adding a Project picks its badge against the stored
  Projects. On start, UAM gives a new badge to every Project whose badge is
  missing, whose text is not two uppercase ASCII letters or digits, whose
  colour is not a palette key, or whose text an older Project's badge already
  has; the older Project keeps its badge. It writes `sessions.json` only when
  it changed a badge. A rename and a restart keep the badge. If the store is read-only, assignment uses the Project ID as a
  stable seed. The choices use `math/rand/v2`; a badge is not a secret.
  The Add dialog says the badge is assigned when added, since its colour
  and collision fallback depend on the Projects stored at that moment.
- **Storage.** A `web_projects` entry gains `badge: {"text", "color"}`,
  omitted until one is assigned, so nothing migrates.

### HTTP additions and changes

| Method and path | Body | Result |
|---|---|---|
| `POST /api/projects` | unchanged | 201 `Project` with its new badge |
| `PATCH /api/projects/{id}` | unchanged | `Project`; the badge never changes |

`Project` gains `badge: {"text", "color"}` in every response and in the
`project` and `snapshot` frames.

## Settings

- Date: 2026-09-24 (decided in #183)

The service stores the web interface's settings, so they apply in every
browser. The first says what Enter does in the composer while a turn runs:
`steer`, the owner's default, or `queue`. The other action stays on
Ctrl/Cmd+Enter. Files and attachments queue because steering accepts text
only. If the provider refuses steering as unsupported, the draft stays in
place and the next Enter queues it with an explanation. Commands stay
blocked during a turn because the server cannot queue commands.
The browser applies the setting: the prompt API does not
change, and the client still sends `mode`. The second picks the model that
titles new Tasks, built to the contract from research #182.

- **Storage.** A top-level `web_settings` object next to `web_projects` holds
  `send_default`. It is omitted until a setting is changed, so nothing
  migrates and a store the web interface never touched gains no key. On
  load, an absent or unrecognised `send_default` means `steer` at runtime.
  The raw value and keys a newer UAM wrote inside `web_settings` survive
  unrelated writes.
- **Checks on write.** `PATCH` accepts only keys it knows. An unknown key, a
  value that is not a string, or a value other than `steer` or `queue` is 400
  and changes nothing. A request that changes nothing writes nothing and
  sends no frame.
- **Hidden models (#191).** `hidden_models: {<provider>: [model ID]}` lists
  the models the browser does not offer anywhere a model is chosen. A `PATCH`
  replaces the list of each provider it names and leaves the others; an
  empty list hides nothing for that provider, and the key is omitted when no
  model is hidden. The provider must be registered. Each ID is 1 to 128
  bytes of UTF-8 without control characters, as stored model IDs are
  checked; duplicates are removed and the list is sorted; at most 200 IDs
  per provider. IDs the catalog does not list now are kept, since models
  can come back. Hiding every model the provider lists now is 400. On load,
  invalid IDs are dropped and the same caps apply. Hiding is a display
  preference, not a policy: the server does not refuse a hidden model in a
  Task's requests or in the Task defaults, and a Task or the defaults already
  on one keep it.
- **Task title model, now the Utility model.** `title_model: {<provider>:
  <model ID>}` names the provider's Utility model, the model UAM uses for
  its own small AI jobs, of which titling new Tasks is the only one so far.
  The key keeps its first name, so stored settings need no migration. A
  provider without an entry uses its cheapest priced model, computed when a
  title is due and given in `/api/meta` as `ProviderInfo.cheapest_model`:
  among the listed models with input and output prices, not `auto` and not
  hidden in Settings, the lowest input plus output price per token, then
  the lower input price, then the lower ID. With no priced model the
  provider keeps its own title; in a headless Copilot session that title is
  the first prompt as typed. The value `none` opts a provider out: it keeps
  its own title and UAM makes no AI call. It is stored as such, since an
  absent entry now means the cheapest model. A `PATCH` sets each provider
  it names and leaves the others. An empty ID removes that provider's
  entry, and the key is omitted when no provider has one. The provider must
  be registered. A model ID also needs the `titles` capability and an ID in
  the provider's current list, `auto` included; `none` and the empty ID
  need neither. Anything else is 400. On load, UAM drops entries whose
  provider or ID is empty, longer than 256 bytes or holds a control
  character. The setting is read when a Task's first prompt is accepted,
  so changing it never retitles a Task.
- **When a Task gets a title.** Only when the provider accepts the Task's
  first prompt, the Task has no name and no title yet, it has no user item
  and no accepted or uncertain submission, and the setting names a model for
  its provider. Commands, steers, later prompts and reopened Tasks never
  start a job. UAM sends the prompt text only, sanitized and cut to 2,000
  runes, never files or attachments. The job runs in the background and
  never delays the turn. At most two run at once. Each gets 20 s from the
  moment it has a slot.
- **Applying it.** UAM drops `<think>` blocks from the reply and keeps the
  content of a `<title>` or `<session-title>` element when there is one. It
  takes the first non-empty line, sanitizes it and collapses whitespace. It
  strips a `Title:` label, wrapping quotes, backticks and emphasis, and a
  trailing `.`, `,`, `:` or `;`. Past 60 runes it cuts at the last space
  within 60, or at 60, without an ellipsis. UAM then sets `web.title` and
  calls `Conversation.SetTitle`. For Copilot that is `session.name.set`,
  which marks the name as set by the user, so neither the CLI nor its TUI
  renames the session later. Browsers still show `name || title || "New
  task"`. Copilot's first-prompt title shows until UAM's arrives, 3 to 5 s
  later in the probes. A rename made while the job runs wins, even one the
  user cleared again. UAM then drops its title and leaves the conversation's
  name alone.
- **Failure.** An unavailable model, a send or session error, the timeout or
  a reply that cleans to nothing leaves the provider's title. UAM logs it at
  info with the Task ID, the model and the error, never the message, and
  neither retries nor tries another model. When `SetTitle` fails, the web
  keeps UAM's title, Copilot keeps its own, and UAM logs that too.
- **The title session.** Copilot's `Title` runs a separate session with the
  chosen model at its lowest effort (`none`, else `minimal`, else `low`,
  else the model's default) in the Task's directory. It has no tools, and
  config discovery, custom instructions, hooks, host git, skills, infinite
  sessions, memory, streaming and the session store are off. A fixed system
  message replaces Copilot's, the client name is `uam-title`, and every
  permission request is rejected. Once the session exists, UAM disconnects
  and deletes it with a fresh 5 s deadline, whether the job succeeded,
  failed or was cut short by shutdown. Shutdown waits for title jobs before
  it stops the providers. With the store off, the delete leaves nothing. A
  probe on 2026-09-24 titled a sample prompt with gpt-6-luna in 2.7 s as
  "Add persistent light and dark mode toggle". Afterwards the session had no
  `session-state` directory, no `session-store.db` row and no `session.list`
  entry, so `copilot --resume` never lists it after the job. Every title
  costs AI credits; research #182 measured about 0.002 per title for
  gpt-6-luna.

### Provider contract additions (`internal/agentapi`)

| Addition | Meaning |
|---|---|
| `Capabilities.Titles` (`titles`) | The provider implements `Titler` and its conversations implement `SetTitle`. Only such providers can have a title model. |
| `Titler.Title(ctx, TitleRequest{Model, Workdir, Text})` → `string` | Asks the model for a title in a throwaway conversation without tools, which it always deletes, and returns the reply as it came. `ctx` bounds the whole call. |
| `Conversation.SetTitle(ctx, title)` | Names the conversation in the provider's own store. `ErrUnsupported` means the provider cannot. |

### HTTP additions and changes

| Method and path | Body | Result |
|---|---|---|
| `GET /api/settings` | – | `Settings`: `{"send_default", "hidden_models"?: {<provider>: [model ID]}, "title_model"?: {<provider>: model ID or "none"}}` |
| `PATCH /api/settings` | `{"send_default"?, "hidden_models"?: {<provider>: [model ID]}, "title_model"?: {<provider>: model ID, "none" or ""}}` | the new `Settings`; 400 for an unknown key or value, an unregistered provider, an invalid ID, more than 200 IDs, every listed model hidden, or a title model the provider cannot use or does not list, checked before anything changes |

Both routes pass the same Host, cross-origin, JSON and sign-in checks as
every other route.

### Event stream additions

| Event | `data` |
|---|---|
| `snapshot` | gains `"settings": Settings` |
| `settings` | `{"seq", "settings": Settings}`, sent when a setting changes, `hidden_models` and `title_model` included |

## Usage and credits

- Date: 2026-09-24 (decided in #188)

The owner asked to show AI credits used and remaining next to the model. This
is the second capability-gated exception to provider parity, after per-Task
context size. A provider has the `usage` capability only when it reports
account quota, as Copilot does.
The browser shows usage only for a provider with the capability.

- **Account quota.** UAM reads every usage provider's quota at start, after
  each of its turns ends, and at most once a minute while a browser is
  connected. A browser gets the cached result: in the `snapshot` frame, as a
  `usage` frame when what it shows changes, and from `GET /api/usage`, which
  never calls the provider. A failed read keeps the last quotas and sets
  `stale`. `reset_at` is shown only while it is in the future: on
  2026-09-24 Copilot returned a `resetDate` a few minutes before the call.
  Copilot's `account.getQuota` is experimental. In the same probe, the
  premium request count did not change in a read a few seconds after a 1x
  turn, so a count can lag the turn that used it.
- **Per Task.** A Task's `usage: {ai_units}` is its conversation's AI units
  so far, main agent and subagents together, once the provider reports them;
  it is absent before that, never zero. Each report is the conversation's
  total, not an increment, so a later report replaces an earlier one. UAM does
  not store it. Copilot reports each model call's cost in `assistant.usage`
  as nano-AI units, which UAM divides by 10⁹. AI units are the AI Credits the
  catalog's token prices use: a probe's call priced at the catalog's prices
  cost exactly its `totalNanoAiu`. The adapter adds each call to the CLI's
  latest session total from `session.usage_checkpoint` (or
  `session.shutdown`), which replaces the running sum when it arrives.
- **What history keeps.** `assistant.usage` is ephemeral: the CLI never
  records it in `events.jsonl`. It does record `session.usage_checkpoint`
  after model calls and `session.shutdown` on disconnect, both with the
  session-wide `totalNanoAiu`. The 2026-09-24 probe found that total in
  `session.getEvents` after a disconnect and resume in the same CLI and
  after a CLI restart. So a Task's AI units survive a browser reload (UAM
  holds them) and a service restart once its recorded transcript loads,
  from the latest recorded total. This includes a settled or archived Task
  whose transcript is read without opening its conversation.
- **Per model.** Models gain `cost_tier`, Copilot's relative cost
  (`low`, `medium`, `high` or `very_high`), and `discount_percent`, which
  Copilot reports for `auto` (10 on 2026-09-24). Both are omitted when not
  reported; an unknown tier or a discount outside 1–100 is dropped.
- **Prices.** The owner then asked for the cost next to the model based on
  the context. Models gain `prices`: Copilot's `billing.tokenPrices` in AI
  Credits per `batch_size` tokens (1,000,000 on 2026-09-24), as `input`,
  `output`, `cache_read`, `cache_write` and `max_prompt_tokens`, and a
  `long_context` object with the same fields except `batch_size`. UAM takes
  `cacheReadPrice`, else the deprecated `cachePrice`, and `maxPromptTokens`,
  else the deprecated `contextMax`. A field Copilot does not report is
  omitted, a negative or non-finite price is dropped, and a model with no
  price left has no `prices`; `auto` has none.
- **Cached share.** Each main-agent `assistant.usage` reports the call's
  `inputTokens` and, of those, the `cacheReadTokens` read from the prompt
  cache (the probe's `inputTokens` also counted `cacheWriteTokens`). The
  Task's `context` gains `prompt` and `cached` from the latest such call, so
  the browser can price the cached share of the context at `cache_read` and
  the rest at `input`. They are omitted until a call reports them, stay on
  later context reports until the next call replaces them, and follow the
  `context` rules: live only, cleared by a reopen or a selection change. A report with `cached` above `prompt`
  or below 0 drops both. The context usage report itself says nothing about
  the cache.

### Provider contract additions (`internal/agentapi`)

| Addition | Meaning |
|---|---|
| `Capabilities.Usage` (`usage`) | The provider implements `QuotaReporter` and reports conversation usage. |
| `QuotaReporter.Quota(ctx)` → `[]Quota{Type, Used, Entitlement, Unlimited, RemainingPercent, Overage, ResetAt}` | The signed-in account's quotas, sorted by type. `Entitlement` is 0 when unlimited; `ResetAt` is zero when not reported. |
| `EventUsage` with `Usage{AIUnits}` | The conversation's AI units so far. |
| `History.Usage` | The recorded total, or nil when the record has none. |
| `Model.CostTier`, `Model.DiscountPercent` | The relative cost tier (`Cost*` constants) and a whole-number discount. |
| `Model.Prices` → `Prices{BatchSize, TierPrices, LongContext *TierPrices}`, `TierPrices{Input, Output, CacheRead, CacheWrite, MaxPromptTokens}` | Token prices per batch in AI Credits; nil prices were not reported. |
| `Context.Prompt`, `Context.Cached` | The latest main-agent call's input tokens and how many of them were read from the cache. |

Copilot mapping: `Quota` sends `account.getQuota`; an entitlement of -1 or
`isUnlimitedEntitlement` means unlimited. `CostTier` comes from
`modelPickerPriceCategory`, `DiscountPercent` from `billing.discountPercent`.

### HTTP additions and changes

| Method and path | Body | Result |
|---|---|---|
| `GET /api/usage` | – | `{"quotas": [{"provider", "type", "used", "entitlement", "unlimited", "remaining_percent", "overage", "reset_at"?}], "stale", "updated_at"?}`; quotas sorted by provider, then type; `updated_at` is when the oldest shown quotas were read, omitted before a read succeeded |

It passes the same Host, cross-origin and sign-in checks as every other
route. `ProviderInfo.capabilities` gains `usage`; models in `/api/meta` gain
`cost_tier`, `discount_percent` and `prices: {"batch_size", "input", "output",
"cache_read", "cache_write", "max_prompt_tokens", "long_context"?: {"input",
"output", "cache_read", "cache_write", "max_prompt_tokens"}}` where reported.
`SessionSummary`, and so `SessionDetail`, gains optional
`usage: {"ai_units"}`, sent in `session` frames like `context`, and
`context` gains optional `prompt` and `cached`.

### Event stream additions

| Event | `data` |
|---|---|
| `snapshot` | gains `"usage"`, the `GET /api/usage` body |
| `usage` | `{"seq", "usage"}`, sent when the quotas or `stale` change |

## Browsing folders for a Project

- Date: 2026-09-24 (decided in #190)

The Add project dialog keeps its path field and gains a folder picker that
browses the host's directories and creates one. The server lists and creates;
the Project is still added through `POST /api/projects`, which resolves
symbolic links as before.

- **Paths.** A path must pass the one rule shared with `canonicalWorkdir`
  (`checkPathText`): present, absolute and *displayable*, that is valid UTF-8
  that display cleaning (`displaytext.Sanitize`) leaves unchanged and free of
  control characters. The folder routes also require clean form
  (`filepath.Clean(p) == p`). Anything else is 400, before the file system
  is asked. Then: a missing path is 404 (also when a component is a file);
  permission denied is 403 (`fs.ErrPermission`), as is a read-only file
  system (`EROFS`, "You can't create folders here"); a component over the
  name limit (`ENAMETOOLONG`) or a symbolic-link loop (`ELOOP`) is 400; a
  full disk or quota (`ENOSPC`, `EDQUOT`) is 507; a path that is not a
  directory is 400. None of these is logged as a failure; only an unexpected
  error is a 500. An empty `path` lists the service user's home.
- **Listing.** `os.ReadDir` reads the directory. Only directories are listed,
  and symbolic links that `os.Stat` resolves to a directory, marked `link`;
  files and other links are left out. So is any entry whose name is not
  displayable (see Paths): `canonicalWorkdir` would refuse it as a Project,
  and leaving it out rather than marking it unusable means the browser never
  holds a raw path that cleaning would have changed. Nothing inside an entry
  is read except whether it holds `.git`, a directory or, in a linked
  worktree, a file (`git`). `hidden` means the name starts with `.`;
  dot-folders are left out unless the query has `hidden=1`, and the browser's
  Show hidden toggle lists again with it. Entries are sorted by name without
  regard to case, then by exact name, and capped at 1,000 after the hidden
  filter, with `truncated: true`, so a truncated listing always shows 1,000
  rows. `name` is the last element of `path`, both exactly as on disk.
  `parent` is `filepath.Dir(path)`, omitted at `/`. Paths are not resolved,
  so a link's entries sit under the link's path.
- **Creating.** `name` must be one path element: not empty, `.` or `..`, no
  `/`, NUL or other control character, no leading or trailing whitespace, at
  most 255 bytes, valid UTF-8. `parent` must be an existing directory under
  the path rules above. UAM calls `os.Mkdir(filepath.Join(parent, name),
  0755)`, never `MkdirAll`, so the umask applies and nothing else is created.
  An existing file, folder or link with that name is 409.
- **Logging.** A created folder is logged at info level with its path. A
  listing is logged at debug level only, so the default log does not record
  what was browsed.

### HTTP additions and changes

| Method and path | Body | Result |
|---|---|---|
| `GET /api/fs/dirs?path=&hidden=` | – | `{"path", "parent"?, "entries": [{"name", "path", "git", "hidden", "link"}], "truncated"}`; dot-folders only with `hidden=1`; 400 relative, unclean, not displayable, too long, a link loop or not a directory; 403 permission denied; 404 missing |
| `POST /api/fs/dirs` | `{"parent", "name"}` | 201 `{"path"}`; 400 invalid name or parent, or parent not a directory; 403 permission denied or a read-only file system; 404 missing parent; 409 the name exists; 507 disk full or quota exceeded |

Both routes need a Host-bound session cookie; the POST also passes the
cross-origin and JSON checks.

### Security

The routes show the service user's directory tree and create folders in it,
as the service user. That is no more than a signed-in browser can already do
through a Task. Anonymous requests cannot browse the tree or create folders.

## Diagrams in a sandboxed frame

Issue #189. Providers answer with fenced ` ```mermaid ` blocks and code in
many languages; the page showed both as plain code.

### Context

- The service policy is `style-src 'self'` with no `unsafe-inline`; the page
  renders no `<style>` element and no `style=""` markup (DESIGN.md, "CSP
  constraints"). Mermaid cannot meet that: a feasibility probe of mermaid
  11.17.2 and 12.0.0 with `securityLevel: 'strict'`, `htmlLabels: false` and
  every render target always inserted a `<style>` element and set about 200
  `style` attributes, 74 violations for a flowchart and 13 for a sequence
  diagram, and no setting avoids it. mermaid 12.0.0 also pulls `lodash-es`
  with two high-severity advisories.
- Relaxing `style-src` for the page would extend the same allowance to every
  provider reply's markup, so it was not an option; neither was Mermaid's own
  `securityLevel: 'sandbox'`, which builds a `srcdoc` frame with an inline
  script per diagram inside the page.

### Decision

mermaid 11.17.2 (MIT, exact pin, `npm audit` clean) runs only inside one
hidden `<iframe sandbox="allow-scripts">`, created on the first diagram and
never given `allow-same-origin`. Its document, `/diagram-frame.html`, is a
second Vite bundle embedded like the rest and served by the Go service with
its own policy:

```text
default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src data:; font-src 'self'; connect-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'
```

Every other response keeps the service policy unchanged, byte for byte
(`TestOnlyTheDiagramFrameRelaxesThePolicy`); the page's policy needs no
`frame-src`, since `default-src 'self'` covers it.

What the isolation gives:

- The frame's origin is opaque. `document.cookie` and `localStorage` throw
  `SecurityError`; a `fetch` of the API is refused by `connect-src 'none'`
  before it leaves, and would be a cross-site request without cookies
  anyway. The sandbox grants no forms, popups or top navigation.
- `frame-ancestors 'self'` (plus `X-Frame-Options: SAMEORIGIN`) lets only
  the service's own origin embed the document.
- The page treats the frame as untrusted: it accepts messages only from its
  own frame's window, validates their shape, and shows the SVG only as
  `<img src="data:image/svg+xml;charset=utf-8,…">`, never as inline DOM. An
  SVG image is an isolated document that runs no script and loads nothing,
  and `img-src 'self' data:` already allowed it.

Protocol (`web/src/lib/diagram.ts`, shared by both bundles): the page posts
`{id, source, theme}` with target `'*'`, since an opaque origin cannot be
named; the frame renders with `securityLevel: 'strict'`, `htmlLabels: false`,
theme `base` and variables from the DESIGN.md tokens, writes the viewBox
size onto the SVG root as `width` and `height` (Mermaid's `width="100%"`
would make the image 300×150), and posts `{id, svg, width, height}` or
`{id, error}` back to the origin the request came from. One request is in
flight at a time with a 10 s deadline, and the frame load has the same
deadline. A frame that fails to load or stops responding is discarded;
every request assigned to it falls back to code. The next render creates a
fresh frame. Results are cached per source; parse errors remain cached,
while transient failures can be retried when the block mounts again.

### Findings (Chromium 148)

- `'self'` in the frame's policy resolves to the document URL's origin, not
  to the opaque origin: the script and a stylesheet from `/assets/` load.
  (CSP's *self-origin* is the response URL's origin; Chromium matches the
  opaque origin's precursor.)
- A module script does not load in the frame: module fetches use `mode:
  cors`, the request `Origin` is `null`, and the service sends no
  `Access-Control-Allow-Origin`. The frame bundle is therefore a classic
  IIFE with Mermaid's lazy diagram chunks inlined (3.4 MB, 924 kB gzip),
  fetched once per page load on the first diagram. Fonts are CORS-fetched
  too, so the frame cannot use the self-hosted Inter, and an SVG image could
  not load a web font either: diagrams measure and draw with the system
  sans stack, which both documents resolve the same way.
- The frame's navigation request is made by the page and carries the
  session cookie (same-site); the frame's own subresource requests carry
  none (cross-site). The document is static and unauthenticated like
  `index.html` and `/assets/*`; it holds no secret, so the auth model does
  not change. Protected API calls still require Host-bound authentication.
- Mermaid measures text in the frame, and gantt and the charts take their
  width from it, so the frame is laid out at 800×600, invisible, not
  `display: none`.
- Sequence labels wrap within their available width, so notes spanning two
  participants keep their text inside the fixed-width note box.

### HTTP additions and changes

| Method and path | Result |
|---|---|
| `GET /diagram-frame.html` | the frame document with the frame policy, `X-Frame-Options: SAMEORIGIN` and `Cache-Control: no-cache`; 403 for a foreign Host; 405 for other methods |
| `GET /assets/diagram-frame-<hash>.js` | the frame bundle, served like every asset, with the service policy |

### Highlighting

Code blocks with a language are highlighted by lowlight 3.3.0 (MIT) over
highlight.js 11.11.2 (BSD-3-Clause; lowlight pins `~11.11`), 18 grammars,
loaded on the first such block as a 73.6 kB chunk. lowlight returns a tree
that is turned into React elements, so the page sets no innerHTML. The
`hljs-*` classes take their colours from the tokens in `index.css`. The main
bundle grew from 748.0 to 756.5 kB (gzip 232.1 to 235.4 kB).

### Consequences

- Two policies exist. The frame's is bound to one document by name in
  `serveAsset`, and the tests pin both strings and check that assets, the
  SPA fallback, the API and refusals all keep the service policy.
- The first diagram of a page load costs a 3.4 MB download. A code-split
  ESM frame would need `Access-Control-Allow-Origin` on `/assets/*` (public
  files, but a header the service has never sent) or a slimmer Mermaid
  registration; both are possible follow-ups.
- Diagrams use Figtree: the frame bundles its Latin file as a data URL (about
  20 KB), builds the face from bytes (no fetch, so no CORS) and embeds it in
  each SVG, which as an image loads nothing else; other scripts fall back to
  the system font.
- Mermaid's error text appears as a quiet note under the code; the code
  block stays usable either way.

## Tool calls, thinking and approvals in the turn

- Date: 2026-09-24 (decided in #181)

A permission request now names the tool call it is for, so the browser can
show a decided request on that call's row instead of as a line of its own.
This section holds the backend part of the contract.

- **Link.** `Interaction.ToolCallID` (`tool_call_id`) is the ID of the `tool`
  item, with the same `agent_id`, that the request is for: for a permission,
  the call that needs it; for a question, the tool call that asked it
  (Copilot's `ask_user`, see "Copilot questions" below). It is empty when
  the provider does not say or the adapter does not know the item. The
  browser treats an empty ID, or one that matches no item it holds, as no
  link.
- **Copilot.** Every permission request kind in SDK 1.0.14 has an optional
  `toolCallId`: shell, write, read, url, mcp, custom-tool, memory, hook,
  factory and the three extension kinds. The adapter reads the same field
  from the raw JSON of a kind the SDK cannot read, and trims it. It is the
  `toolCallId` of the tool execution events, which the transcript already
  uses as the tool item's ID.
- **Sanitizing.** The service keeps `tool_call_id` only if it is valid UTF-8,
  at most 256 bytes, and holds no space or control character. It drops
  anything else rather than cleaning it, because a changed ID would match no
  tool item.
- **Where.** Every serialized `Interaction` carries the field:
  `SessionDetail.interactions` (also inside the `snapshot` frame), the
  `interaction` frame, and the reply of
  `POST /api/sessions/{id}/interactions/{iid}`. `SubagentDetail` has no
  interactions. A subagent's requests are in the Task's `interactions`, with
  their `agent_id`.

### Provider contract additions (`internal/agentapi`)

| Addition | Meaning |
|---|---|
| `Interaction.ToolCallID` (`tool_call_id`, omitted when empty) | The ID of the `tool` item, with the same `AgentID`, that a request is for: the call a permission is for, or the call that asked a question. Empty when unknown. |

### HTTP additions and changes

No route changes. `Interaction` gains `tool_call_id`, omitted when empty.

### Browser

The browser shows what ran in the turn, in the order it ran, and nothing about
it after the transcript.

- **Tool rows.** Each `tool` item is one row in its turn: a status mark, the
  tool name, and its main argument on one line, ellipsised. The argument comes
  from the input JSON per tool (`web/src/lib/transcript.ts`): the command for
  `bash`, the URL for `web_fetch`, the path for `view`, `create` and `edit`,
  the pattern for `grep` and `glob`, the query for `sql`, the skill name, the
  description for `task`; an unknown tool takes the first of those keys it
  has, then its first string value, then the JSON itself; a non-JSON input
  shows as is. The full input and output stay behind the row's disclosure.
  The owner's later screenshot direction groups consecutive calls under an
  expandable summary of successful operations, with distinct known file paths
  and explicit running, failed and missing-result counts. Every original row
  stays available inside; prose, thinking, questions and subagent rows remain
  interleaved. The foreground timeline uses the original user item time while
  working, including across steer. Completed turns say "Worked" without an
  elapsed value: item creation and session update times are not completion
  timestamps. Background task status remains separate.
- **Thinking.** A reasoning item's text shows inline, clamped to three lines
  (measured, so one long paragraph clamps too) with Show more / Show less;
  while it streams, the latest three lines show. An empty reasoning item is
  not drawn. The expanded state is remembered per item for the browser
  session, as before. Copilot history restores thinking from the recorded
  assistant message's `reasoningText`, including messages with no visible
  content. Live reasoning is not duplicated when that message arrives.
- **Approvals.** A decided request whose `tool_call_id` matches a tool item
  with the same `agent_id` appears only as a mark on that row: a shield and a
  word, "auto" when the resolution is `allowed (yolo)`, "allowed" for
  `answered`, "denied" for `rejected`, "expired" for `expired`. The tooltip
  and the accessible name give the full resolution. When several requests
  name one call, the mark shows the latest outcome and a count of earlier
  requests; its tooltip and accessible name retain every resolution. A
  decided request without a matched call joins its turn's rows at its time,
  as a quiet row of the same kind. Pending
  requests stay action cards after the transcript, and are the only requests
  drawn there.
- **Questions.** Every provider's question interaction renders a question
  block in its turn, linked to its tool when known and standalone otherwise.
  Copilot's `ask_user` also restores the question and choices from the tool input
  (`{"question", "choices"}`), and once the call completes, the answer from
  its output ("User selected: <choice>" for a choice, "User responded:
  <text>" for typed text, per a probe on CLI 1.0.88; any other text is shown
  whole) under "You answered", with the chosen choice marked. A call left
  open after a restart or stopped turn reads "No answer." A decline is
  read from the linked interaction's `rejected` state, or, from history, from
  the output the CLI records for it: a *completed* call whose output is
  "User responded: The user was unable to respond due to an error". A failed
  call shows its output. This comes from the tool item, so it survives a
  reload and a restart. The live question interaction, when the adapter
  linked it (see below), or a decided question whose text matches the call
  when it did not, is claimed by that block. Repeated questions with the same
  text match calls in time order. A pending question also keeps its action
  card, where the user answers it.
- **Copilot questions.** The SDK's `ask_user` callback carries the question
  and choices only. The `user_input.requested` event carries the same
  question with its `toolCallId` and the envelope's agent ID, so the adapter
  matches the two by question text in arrival order, whichever arrives first:
  a pending question that has no tool call yet is emitted again with the link and its
  agent; an event that arrives first is kept for the callback. Each queue is
  capped at 16; unmatched events and answered-question markers expire after
  30 seconds. A late event for an already answered question is consumed
  before the next question with that text. An event without a tool call links
  nothing. The callback has no agent ID, so identical concurrent questions
  cannot be matched across agents more precisely than their arrival order.
- **Subagents.** The Subagents panel draws the same rows, blocks and marks,
  with the requests whose `agent_id` is the subagent's.

## Images from tools

- Date: 2026-09-24 (decided in #187)

No Copilot model returns images, but tools can: a screenshot tool, an MCP
server, or Copilot's `view` of an image file. UAM keeps the images a tool's
result carried with the Task and names them on that tool item. This section
holds the backend part of the contract.

- **Copilot, live.** A `tool.execution_complete` result can carry images two
  ways: `contents` blocks of type `image` (base64 `data` and `mimeType`,
  what MCP tools return) and `binaryResultsForLlm` entries of type `image`
  with inline `data`. A probe on 2026-09-24 (CLI 1.0.88, SDK 1.0.14,
  `gpt-5-mini`) had `view` of a png in the project return only the second
  form, for the main agent and for a subagent alike. The adapter maps both
  forms; entries of type `resource`, audio and size-omitted markers are not
  images.
- **Copilot, recorded.** The recorded completion keeps no bytes. Its
  `binaryResultsForLlm` entry holds `assetId: "sha256:<hex>"` and
  `byteLength`, and a `session.binary_asset` event written just before it
  holds the bytes under that asset ID. The probe found both after a reload
  and after the CLI was stopped and the session resumed, and UAM restored the
  image from them with its own copy deleted. The adapter holds at most 64
  recent asset events; a result naming an asset it does not hold reports the
  digest alone, and UAM links it to its stored copy by SHA-256. A
  size-omitted marker has no bytes and no digest, so such an image cannot come
  back after a restart.
- **Storage.** Tool images live with the uploads in
  `web-attachments/<task id>/`, 0700 directories and 0600 files, as
  `<image id>` and `<image id>.json`. The record is the upload record with
  `"tool": true`. A tool image lives as long as its Task: the 24-hour expiry
  skips it, deleting the Task or removing its Project deletes it, and the
  start-up sweep removes the directories of unknown Tasks. It is not an
  upload: naming its ID in a prompt's `attachments` is a 400. Read-only history
  loads and imports use the same image store before publishing the transcript;
  an import that cannot register its Task removes its newly stored images.
- **Checks.** UAM sniffs the bytes: png, jpeg, gif and webp only, by
  `http.DetectContentType`, so SVG never passes. It takes the type from the
  bytes, never from the provider. An image may be at most 5 MiB, and a Task
  keeps at most 50 tool images. An image this Task already keeps, by the
  SHA-256 of its bytes, is not stored again and gets the same ID, whichever
  tool call or agent returned it. An image the provider reports by digest
  alone is shown only when that copy exists.
- **Dropped images.** An image that fails a check, is past the cap, or has
  no bytes and no stored copy is left out of `images`, and the tool item's
  `images_note` says how many and why, for example "1 image not kept: a task
  keeps at most 50 images". The tool item is published immediately without
  images. A worker stores images from a queue capped at 256 tool results,
  then publishes the item with their IDs. A full queue drops the images with
  a note. No stream frame carries an image without an ID, and image storage
  never blocks the provider event callback.
- **Serving.** A tool image is served by the attachment route with the
  upload headers: the sniffed `Content-Type`, `nosniff`, the service CSP,
  `Cache-Control: no-store` and `inline`. A tool image without a name gets
  `Content-Disposition: inline` with no file name.

### Provider contract additions (`internal/agentapi`)

| Addition | Meaning |
|---|---|
| `Item.Images []Image` (`images`, omitted when empty) | The images a tool item's result returned, in the provider's order. |
| `Image{ID, MIME, Size, Name, SHA256, Data}` | Adapters fill `Data`, `MIME` and `Name` when the provider has one, or `SHA256` alone when the record has only the digest. UAM digests and stores `Data`, sets `ID`, `Size` and the sniffed `MIME`, and clears `Data`. `SHA256` and `Data` never reach a browser. |
| `Item.ImagesNote` (`images_note`, omitted when empty) | Set by UAM when it left images out, and why. Adapters leave it empty. |

### HTTP additions and changes

| Method and path | Body | Result |
|---|---|---|
| `GET /api/sessions/{id}/attachments/{attachment_id}` | – | also serves a tool image by its `images[].id`; 401 signed out; 404 for an unknown Task or image, or another Task's |

A tool item gains `images: [{"id", "mime", "size", "name"?}]` and
`images_note?`, in `SessionDetail.items` (also in the `snapshot` frame), the
`item` frame and `SubagentDetail.items`.

### Browser

The tool row shows its `images` as thumbnails up to 160px high under the
row, visible without opening the disclosure, loaded from
`GET /api/sessions/{id}/attachments/{image id}`: the same thumbnails and
lightbox as a user turn's uploads (`ImageThumbs` in
`web/src/components/Attachments.tsx`), so a click opens the image large with
**Open original**. An image without a name is "Image". `images_note` follows
the thumbnails as a caption. The browser never builds a `blob:` or `data:`
URL for them, so `img-src 'self' data:` stands. The Subagents panel does the
same.

## Importing previous sessions

- Date: 2026-09-24 (decided in #192, from the research in #138)

A Project lists the conversations its provider recorded for the Project's
directory, and the user can import one as a Task. This is a
capability-gated exception to the provider-parity rule. A provider offers
`import` only when it can list conversations by folder and can tell that another client
holds one open. Copilot probes `sessions.checkInUse` and
`readPersistedEvents` once per provider instance and advertises `import` only
when both are supported.

- **List.** `Importer.Previous` lists the conversations whose working
  directory is exactly the Project's directory: Copilot's `session.list` with
  an exact `cwd` filter, remote sessions left out. A Copilot session appears
  only after its first message. UAM leaves out conversations any Task is
  linked to, sanitizes titles like other provider labels, sorts newest first
  by `updated_at`, and returns at most 100. `in_use` comes from `InUse`.
- **Import.** The conversation must be listed for the Project's directory, no
  Task may be linked to it, and no other client may hold it. UAM reads its
  transcript with the reader of the Task lifecycle section, then creates an
  active Task linked to it. Nothing is sent to the model. The Task starts in
  state `closed` with the transcript loaded: viewing never opens it, and its
  next prompt opens it. It keeps the model the record last selected for the
  main agent when the provider offers that model, otherwise it takes the
  Task defaults of Settings when they name this provider, otherwise the
  provider default. It takes the mode of those defaults. The provider's title
  is shown until the user names the Task.
- **In use.** `InUse` is Copilot's `sessions.checkInUse` (experimental),
  backed by an `flock` per holder process. It reports other processes
  holding the conversation on this host under the same `COPILOT_HOME`,
  including a uam terminal session and a terminal `copilot --resume`. It
  never reports the CLI of uam web itself; uam web tracks its own open
  conversations. Only imported or terminal-linked Tasks
  run this check before every send, command, steer and subagent follow-up,
  and before a queued prompt is sent. While another client holds the
  conversation, the write is refused with 409 and nothing is sent; a queued
  prompt stays at the front and the queue pauses. A check that fails refuses
  with 502, and nothing is sent. The check times out after five seconds and
  releases the Task operation lock while waiting. If a lifecycle operation
  changes the Task meanwhile, the send is refused and must be retried.
- **Race window.** The check is a snapshot, not a lock. A client that opens
  the conversation after the check, while UAM opens it, sends, or runs the
  turn, is not caught. Two writers then fork the journal without an error
  (#138), and each keeps its own view of the history. While uam web holds a
  conversation, a terminal `copilot --resume` shows Copilot's "Session in
  use" dialog; "Resume anyway" is the user's choice.
- **Terminal sessions.** A uam terminal session tied to the same conversation
  (a terminal record with the same provider and `provider_session_id`, the
  most recently seen one) stays the terminal's. Import records its ID in the
  Task's `web.terminal_session`. While that session's host runs, which the
  service checks as the terminal does (the host's state file and a
  start-time-checked process), the detail shows `terminal_session`. Deleting
  the web Task deletes only its own record: never the terminal record, and
  never the provider's conversation.

### Provider contract additions (`internal/agentapi`)

| Addition | Meaning |
|---|---|
| `Capabilities.Import` | The provider implements `Importer` |
| `Importer.Previous(ctx, workdir) ([]PreviousConversation, error)` | Conversations recorded with exactly `workdir` as their working directory; empty lists all local conversations with their Workdir |
| `Importer.InUse(ctx, ids) ([]string, error)` | The IDs another process holds open; a snapshot, never the provider's own runtime |
| `PreviousConversation{ID, Title, Workdir, CreatedAt, UpdatedAt}` | `Title` is untrusted provider text |
| `History.Model` | The main agent's model as the record last selects it, or "" |

### HTTP additions and changes

| Method and path | Body | Result |
|---|---|---|
| `GET /api/previous/counts` | – | 200 `{project_id: count}` for every Project, including zero, capped at 100; one provider listing without in-use checks; 502 on listing failure |
| `GET /api/projects/{id}/previous` | – | 200 `[{"provider", "conversation_id", "title", "created_at", "updated_at", "in_use"}]`, `[]` without an importing provider; 404 unknown Project; 502 when listing or the in-use check fails |
| `POST /api/projects/{id}/previous/{conversation_id}/import` | ignored (`Content-Type: application/json`) | 201 `SessionSummary` with `state: "closed"`; 400 invalid ID; 404 unknown Project, or a conversation not listed for its directory or gone; 409 already a Task, held by another client, or the directory is gone; 502 when listing, the in-use check or the read fails; 503 when the read queue is busy or import is cancelled |
| `POST /api/sessions/{id}/prompt`, `/command`, `/subagents/{agent_id}/prompt` | unchanged | 409 while another client holds the conversation; 502 when the check fails |
| `GET /api/sessions/{id}` | – | `SessionDetail` gains `terminal_session: {"id", "name"}` while the tied terminal session's host runs |
| `GET /api/meta` | – | `capabilities` gains `import` |

The record's `web` object gains `terminal_session`, the ID of the tied
terminal record, and `imported`, a durable boolean marking imported Tasks.
The capability flag is the authority for import support; providers setting it
must implement `Importer`.

## Amendment: recorded step times

`Item.time` is when the item began, and `Item` gains `ended_at` (omitted
while it runs or when the provider did not record an end). The Copilot
adapter takes both from the recorded events: a tool call runs from
`tool.execution_start` to `tool.execution_complete`; a thought runs from its
model call's `assistant.turn_start` to the event that carried it. The service
keeps an item's earliest `time` when a later event replaces it, so a live
item and the same item read back from history agree. The browser shows a
duration only from these two times and never infers one from the next item.
