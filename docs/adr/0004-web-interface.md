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
- Copilot and OpenCode must be installed and signed in on the Linux host.
  UAM does not download or update them.
- Older `uam` binaries do not know `surface` and would show web records as
  ordinary stopped sessions.
