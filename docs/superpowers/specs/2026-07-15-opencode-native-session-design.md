# Native OpenCode session integration

Date: 2026-07-15
Status: Approved architecture; pending written-spec review

## Context

UAM currently injects a generated `uam-identity-plugin.mjs` into OpenCode.
The plugin observes root-session events and writes the active OpenCode session
ID to provider state so UAM can resume with `opencode --session <id>` instead
of the ambiguous `opencode -c` fallback. This fixes exact resume and lets a
managed UAM terminal follow OpenCode `/new` to its newly created root session.

The generated module and its provider-state directory must be private because
OpenCode executes the module. Older or externally modified installations can
leave the file or directory with broader modes, causing a secure fail-closed
check to block OpenCode launch.

OpenCode v1.18.1 provides stable HTTP session creation, exact-session attach,
and server-sent session events. UAM will use those supported interfaces and
remove the generated JavaScript dependency. The upstream evidence and rejected
alternatives are recorded in
`docs/research/2026-07-15-opencode-session-identity.md`.

## Goals

- Remove production creation, loading, and validation of
  `uam-identity-plugin.mjs`.
- Require OpenCode v1.18.1 or newer with an actionable error for older builds.
- Create a new OpenCode root session through the stable server API and retain
  its exact returned ID before interactive use begins.
- Resume the exact retained OpenCode session without falling back to `-c` for
  newly created records.
- Track `/new` so only the owning UAM record advances to the new root session.
- Preserve the current interactive OpenCode TUI, prompt bytes, safe/yolo
  behavior, session names, store schema v3, and other provider behavior.
- Support Linux and macOS on AMD64 and ARM64.

## Non-goals

- Do not adopt OpenCode's experimental V2 `/api/session` contract.
- Do not build a shared, machine-wide OpenCode daemon.
- Do not migrate or rewrite OpenCode's own session database.
- Do not delete unknown, user-authored, symlinked, or ownership-mismatched
  files while cleaning up legacy UAM state.
- Do not change UAM's TUI layout, JSON output, store schema, default yolo
  policy, or non-OpenCode adapters.

## Compatibility boundary

The OpenCode provider command topology necessarily changes from a single
embedded TUI process to a UAM supervisor that owns a headless OpenCode server
and an attached OpenCode TUI. User-visible behavior remains compatible:

- a new UAM dispatch opens the OpenCode TUI on a fresh root session;
- an existing UAM session resumes its retained exact root session;
- `/new` creates a new OpenCode root session within the same managed terminal;
- closing, stopping, and restarting the UAM session clean up every child;
- initial prompts are delivered byte-for-byte once, and resume does not replay
  the original prompt;
- safe mode keeps OpenCode's normal permission behavior and yolo mode retains
  the current auto-approval behavior.

Existing schema-v3 `ProviderSessionID` values remain valid. No store migration
or schema bump is needed.

## Architecture

### Provider boundary

The OpenCode adapter will resolve the executable through the existing trusted
path logic and probe `opencode --version`. It will accept semantic versions at
or above v1.18.1 and reject older or unparsable versions before creating a UAM
session record. The error will show the resolved executable, detected version,
required version, and `opencode upgrade 1.18.1` guidance.

The probe result will be cached by executable stat identity, following the
existing `--auto` capability probe pattern, so regular refreshes do not spawn
version checks.

For an accepted version, the adapter will launch an internal UAM command rather
than injecting `OPENCODE_CONFIG_CONTENT`. The internal command is an OpenCode
supervisor and receives only explicit launch data: executable path, working
directory, UAM session name, optional retained provider session ID, and mode.

### Per-session supervisor

Each managed OpenCode session gets one supervisor under the existing native
UAM session host. The native host remains the PTY owner and continues to see a
single foreground child. The supervisor owns two OpenCode subprocesses:

1. `opencode serve`, bound to `127.0.0.1` on a per-session port;
2. `opencode attach <url> --session <id>`, connected to that server and using
   the native UAM PTY for its terminal streams.

The server uses a fresh cryptographically random password supplied only through
the child environment. The password is passed to the attach client through its
environment, never through argv, logs, store records, state files, or terminal
output.

The supervisor allocates a loopback port, starts the server, and polls
`GET /global/health` with authentication until it reports OpenCode v1.18.1 or
newer. Port-bind races are retried with a new port a bounded number of times.
Startup has a five-second whole-pass deadline.

### Session creation and resume

For a new UAM dispatch, the supervisor calls stable `POST /session`, scoped to
the requested working directory. The request carries a UAM-identifying title
or metadata and mode-appropriate permission rules. The returned root session
ID becomes authoritative before the attach client starts.

For resume, the retained `ProviderSessionID` is validated locally and then
confirmed through `GET /session/:id`. A missing exact session is an actionable
error; UAM does not silently substitute OpenCode's most recent session.

After the exact session is available, the supervisor starts
`opencode attach --session <id>`. The supervisor never reads its PTY stdin.
UAM's existing `Agent.startSession` path continues to deliver an initial prompt
through `Backend.SendLine`; the PTY queues those bytes while the supervisor
finishes server/session setup, and the attached OpenCode TUI consumes them when
it starts. This reuses the current byte path instead of adding an HTTP prompt
path or a second host protocol. Resume preserves the existing behavior of not
resubmitting the stored prompt.

### `/new` and identity tracking

Before starting the attach client, the supervisor subscribes to authenticated
`GET /event`. It accepts root `session.created` events only when the session has
no parent and belongs to this supervisor's project/directory. Child-agent
sessions are ignored.

Because the attached TUI talks only to this dedicated server, a subsequent root
session created by `/new` belongs to this managed UAM session. The supervisor
atomically records the new provider session ID in an owner-only runtime
identity file under the already verified UAM session runtime directory. The
OpenCode adapter reads that file during normal live-session reconciliation and
patches the existing store record's `ProviderSessionID` field.

This runtime file replaces the persistent provider-state handoff. It is a
regular current-user-owned file with mode `0600`, placed below the verified
`0700` runtime directory, written by temporary-file plus rename, size-limited,
and decoded with the existing provider-ID allow-list. Runtime cleanup removes
it with the corresponding session socket and state.

If the event stream disconnects, the supervisor reconnects with bounded
backoff. The last verified ID remains authoritative. Recovery may query root
sessions updated through the dedicated server, but it may advance the identity
only when exactly one unambiguous newer root exists; otherwise it warns and
keeps the last verified ID.

### Lifecycle and exit behavior

The supervisor owns the complete process group. It forwards terminal-relevant
signals to the attach client and guarantees that server and attach processes
are terminated on normal exit, stop, restart, context cancellation, startup
failure, or attach failure. Cleanup is bounded and escalates from graceful
termination to kill after a short deadline.

The attach client's exit code remains the provider exit code observed by the
native UAM host. A server failure while attach is active terminates attach and
surfaces a concise sanitized error. Server logs are captured in a bounded ring
buffer and included only in diagnostic logs, with credentials removed.

## Security

- Bind only to numeric loopback, never wildcard interfaces.
- Authenticate every HTTP, SSE, and attach connection with a per-process
  high-entropy secret.
- Keep the secret out of argv, logs, persistent state, terminal rendering, and
  error strings.
- Resolve and probe the same executable that is later launched; do not re-read
  `PATH` after validation.
- Validate every OpenCode session ID before using it in a URL, state path, or
  resume command.
- Treat ownership, symlink, and file-type violations in the UAM runtime
  directory as non-repairable security errors.
- Never execute generated JavaScript and never inject a plugin URL into
  `OPENCODE_CONFIG_CONTENT`.
- Preserve raw prompts for API delivery while sanitizing only rendered error
  and status text.

## Legacy state

The old generated plugin is not in OpenCode's automatic plugin directories; it
was active only because UAM injected its file URL. Once injection is removed,
an existing file is inert and must not block launch.

UAM will stop reading and writing the old provider-state identity directory.
The first release will not delete it automatically. Documentation will provide
an explicit cleanup command for the known UAM path. A future `uam setup` cleanup
may remove it only after no-follow inspection proves that every removed object
is a current-user-owned regular file/directory and the module content matches a
known UAM-generated version. Cleanup failure is advisory and never blocks
OpenCode launch.

## Error handling

- OpenCode older than v1.18.1: fail before dispatch with upgrade guidance.
- Version probe timeout or malformed output: fail with the executable path and
  captured sanitized output.
- Server readiness timeout: stop all started children and return a bounded
  diagnostic excerpt.
- Authentication, create, lookup, prompt, or attach failure: stop all children;
  do not persist an unverified provider ID.
- Exact resume target missing: fail explicitly; never use `-c`.
- Event-stream interruption: keep the last verified identity, reconnect, and
  warn without terminating an otherwise healthy attached session.
- Runtime identity persistence failure: keep the live TUI running, warn, and
  retain the last persisted ID so failure cannot redirect resume heuristically.

## Test strategy

Implementation follows characterization-first TDD. Tests use fake OpenCode
executables and `httptest` servers for deterministic lifecycle and API coverage,
plus a gated real-OpenCode v1.18.1 compatibility suite.

Required automated coverage:

- semantic-version parsing: below, equal to, above, prerelease, malformed, and
  probe timeout;
- exact new-session creation and exact resume without `-c`;
- missing resume target fails without choosing the latest session;
- two concurrent UAM sessions in the same project retain different OpenCode
  IDs and isolated authenticated servers;
- `/new` advances only the owning UAM record;
- child-session events never replace the root identity;
- SSE disconnect, replay gap, ambiguity, and recovery;
- raw prompt bytes, empty prompt, Unicode, multiline, and resume-no-replay;
- safe and yolo permission parity;
- port collision and retry, readiness timeout, authentication rejection, API
  failure, attach failure, and server death;
- stop, restart, natural exit, signals, and orphan-free cleanup under the race
  detector;
- runtime identity no-follow, owner, mode, size, name, and atomic-write checks;
- unsafe or stale legacy `.mjs` and provider-state paths do not block launch;
- no OpenCode plugin URL is added to `OPENCODE_CONFIG_CONTENT`;
- Linux and Darwin AMD64/ARM64 compile and lifecycle coverage.

The complete repository quality gate remains required:

```text
go test -race -count=1 -covermode=atomic -coverpkg=./... ./...
go vet ./...
golangci-lint run ./...
staticcheck ./...
gosec ./...
govulncheck ./...
go mod verify
gofmt cleanliness check
```

## Acceptance criteria

- OpenCode v1.18.0 and older receive a clear minimum-version error.
- OpenCode v1.18.1 and newer dispatch, attach, stop, restart, and resume on
  Linux and macOS.
- Production code no longer creates or references
  `uam-identity-plugin.mjs` or injects its URL.
- A stale or incorrectly permissioned legacy module cannot block launch.
- Fresh UAM OpenCode sessions never use `-c`.
- Multiple UAM sessions in one project resume their own exact conversations.
- `/new` updates only the owning UAM record before the next reattach.
- No OpenCode server remains after its UAM session exits or is stopped.
- The server is loopback-only and rejects unauthenticated requests.
- Existing store fixtures and unknown fields round-trip unchanged under schema
  v3.
- Other provider commands and behavior remain unchanged.

## Rollout

This is a minor-version change because it raises the OpenCode requirement and
changes the provider's internal process topology. Release notes will call out
OpenCode v1.18.1 as the minimum and provide the upstream upgrade command and
optional legacy-state cleanup instructions.

The rollout sequence is:

1. upgrade the development machine to OpenCode v1.18.1;
2. characterize stable server, session, permission, PTY prompt, attach, and event
   behavior against the real binary;
3. implement behind the OpenCode adapter boundary with no plugin fallback;
4. run focused concurrency/lifecycle tests and the full quality gate;
5. exercise two same-project sessions and `/new` manually over local and SSH
   attaches;
6. merge only after Linux and Darwin CI is green;
7. publish the next minor UAM release.

The plugin-based implementation remains in git history for rollback, but the
new release does not dynamically fall back to it. If the native server adapter
is not healthy, dispatch fails explicitly rather than silently returning to
ambiguous session behavior.
