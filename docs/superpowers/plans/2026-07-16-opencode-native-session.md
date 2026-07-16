# Native OpenCode Session Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace UAM's generated OpenCode identity plugin with an authenticated per-session OpenCode 1.18.1 server/attach supervisor that creates, resumes, and tracks exact native sessions without regressions.

**Architecture:** The OpenCode adapter validates the selected OpenCode command, enforces version 1.18.1 or newer, and replaces the provider argv with an internal `uam __opencode` command. That supervisor owns a loopback-only authenticated `opencode serve`, creates or validates one exact root session over HTTP, runs `opencode attach --session`, mirrors OpenCode's auto-permission event response in yolo mode, and atomically hands changing root IDs to the existing native session host through the verified runtime directory.

**Tech Stack:** Go 1.25.12, standard-library `net/http`, Server-Sent Events, `os/exec`, the existing native PTY/session backend, OpenCode 1.18.1 CLI and HTTP API, Go tests with race detection.

## Global Constraints

- Require OpenCode `>= 1.18.1`; reject older, prerelease-at-the-floor, timed-out, and malformed version probes with upgrade guidance.
- Support Linux and macOS on AMD64 and ARM64; Windows remains out of scope.
- Preserve CLI commands and flags, JSON list shape, provider aliases, prompt delivery, session names, store schema v3, unknown-field preservation, default yolo policy, and TUI layout.
- New OpenCode sessions never use `-c`; exact resume requires a validated retained `ProviderSessionID`.
- Never generate, execute, validate, or inject `uam-identity-plugin.mjs`; legacy files remain inert and are not deleted automatically.
- Bind OpenCode servers only to numeric `127.0.0.1` and authenticate HTTP, SSE, and attach with a fresh random password that never enters argv, logs, store data, runtime files, or terminal errors.
- Safe mode leaves OpenCode permission prompts untouched. Yolo mode replies `once` only to emitted `permission.asked` events, matching OpenCode 1.18.1 `--auto` and preserving explicit deny rules.
- Add no dependency and introduce no store migration or schema bump.
- Every production change starts with a test that fails for the intended reason.

---

## Current State

- `internal/adapter/opencode/opencode.go` injects `OPENCODE_CONFIG_CONTENT`, creates `uam-identity-plugin.mjs`, reads persistent provider state, probes `--help` for `--auto`, and falls back to `opencode -c` when no provider ID exists.
- `internal/adapter/agent.go:Agent.startSession` calls `PrepareLaunch`, appends `SessionArgs`, then launches only the provider command. `LaunchPreparation` cannot replace that command.
- `internal/session/host.go` already reads a provider-neutral identity handoff after child exit, and the process host already owns the PTY/process group needed by a supervisor.
- `internal/session/session.go` verifies a per-UID owner-only runtime directory and cleans only `<name>.json` plus `<name>.sock`.
- `internal/cli/cli.go:runWithoutStore` routes `__host` and `__attach` before store access; it has no OpenCode supervisor route or exact internal exit-code propagation.
- `internal/app/service_test.go:TestProductionProviderResumeKindMatrixThroughAmbiguityGuard` currently characterizes OpenCode-without-provider-ID as heuristic resume.
- The development machine has UAM `0.3.3` installed and OpenCode `1.17.18`; the approved floor is `1.18.1`.

## Decisions

1. **Per-UAM-session server, not a shared daemon.** It isolates `/new`, credentials, lifecycle, and permission events. Confidence: 98%. Risk: two OpenCode processes per UAM session; accepted because correctness and cleanup dominate small local overhead.
2. **Stable shipped HTTP surface, not caller-selected experimental V2 IDs.** `POST /session` returns the authoritative ID; `GET /session/:id` validates resume. Confidence: 97%. Rejected: unshipped `/api/session` caller IDs and heuristic `-c`.
3. **Existing PTY prompt path, not HTTP prompt submission.** `Backend.SendLine` queues the initial prompt until attach reads the PTY, preserving current delivery and avoiding a second prompt protocol. Confidence: 96%. Risk: attach-start ordering; covered with delayed-attach tests.
4. **Event reply parity, not blanket allow rules.** OpenCode evaluates deny/allow before emitting `permission.asked`; replying `once` to emitted asks exactly matches its 1.18.1 `--auto` path. Confidence: 99%, verified against tagged upstream source.
5. **Runtime identity file, not persistent provider state.** It reuses the verified `0700` runtime boundary, is cleaned with the session, and keeps schema v3 unchanged. Confidence: 98%.
6. **Small generic command override.** Add only `LaunchPreparation.Command []string`; non-OpenCode providers continue down the current path byte-for-byte. Confidence: 97%.

## File Structure

- Modify `internal/adapter/adapter.go`: add the optional launch-command override.
- Modify `internal/adapter/agent.go` and `internal/adapter/agent_test.go`: validate the normal provider command, then use a copied override when present.
- Create `internal/session/provider_identity.go` and `internal/session/provider_identity_test.go`: secure provider-neutral runtime identity read/write/path helpers.
- Modify `internal/session/session.go`, `internal/session/host.go`, and session tests: clean the identity file without losing the exit handoff.
- Replace `internal/adapter/opencode/opencode.go` with adapter wiring only.
- Create `internal/adapter/opencode/command.go` and `command_test.go`: direct-path/shell-alias command construction and minimum-version probe/cache.
- Create `internal/adapter/opencode/client.go` and `client_test.go`: authenticated health/session/permission/SSE client.
- Create `internal/adapter/opencode/supervisor.go` and `supervisor_test.go`: per-session server, attach, event tracking, permission parity, and cleanup.
- Modify `internal/adapter/opencode/opencode_test.go`: remove plugin implementation tests and add adapter-boundary regressions.
- Modify `internal/cli/cli.go` and `internal/cli/cli_test.go`: route `__opencode` before store access and preserve attach exit codes.
- Modify `internal/app/service_test.go`: make missing OpenCode identity unsupported instead of heuristic.
- Modify `README.md`: state the minimum version, exact-session behavior, and non-blocking legacy-file cleanup guidance.

---

### Task 1: Characterize OpenCode 1.18.1 and Add the Generic Command Override

**Objective:** Prove the local OpenCode contract and let one adapter replace its launch argv without changing other providers.

**Prerequisites:** Approved design and clean `codex/opencode-native-session` branch.

**Files:**
- Modify: `internal/adapter/adapter.go`
- Modify: `internal/adapter/agent.go`
- Test: `internal/adapter/agent_test.go`

**Interfaces:**
- Produces: `LaunchPreparation.Command []string`.
- Consumes: existing `Agent.commandForRequest`, `Backend.CreateSession`, and defensive slice copying convention.

- [ ] **Step 1: Upgrade and characterize the approved local binary**

Run:

```bash
rtk proxy opencode upgrade 1.18.1
rtk proxy opencode --version
rtk proxy opencode serve --help
rtk proxy opencode attach --help
```

Expected: version output is exactly `1.18.1`; help lists `serve --hostname/--port` and `attach <url> --session/--dir`, and attach does not list `--auto`.

Failure signal: upgrade fails or the flags differ. Stop, capture the exact output, and revise the approved contract before code changes.

- [ ] **Step 2: Write failing override tests**

Add table-driven coverage to `internal/adapter/agent_test.go` with a fake `PrepareLaunch` returning:

```go
adapter.LaunchPreparation{Command: []string{"/trusted/uam", "__opencode", "--name", sessionName}}
```

Assert `Backend.CreateSession` receives that exact command, mutating the original preparation slice after `Dispatch` cannot change the backend's captured command, an invalid `CommandAlias` still fails before `PrepareLaunch`, and a provider without `Command` retains its existing argv including yolo/session args.

- [ ] **Step 3: Run the focused test and confirm the intended failure**

Run: `rtk go test ./internal/adapter -run 'Test.*Launch.*Command|Test.*Preparation' -count=1`

Expected before implementation: compile failure because `LaunchPreparation.Command` does not exist.

- [ ] **Step 4: Implement the minimal override**

Change `LaunchPreparation` in `internal/adapter/adapter.go` to:

```go
type LaunchPreparation struct {
	Command           []string
	ExtraArgs         []string
	Env               map[string]string
	ProviderSessionID string
}
```

In `Agent.startSession`, keep `commandForRequest(ctx, req, extra)` as the validation and compatibility path, then replace only a non-empty result:

```go
cmd, err := a.commandForRequest(ctx, req, extra)
if err != nil {
	return Session{}, err
}
if len(preparation.Command) > 0 {
	cmd = append([]string(nil), preparation.Command...)
}
```

Reject a non-nil zero-length override by treating it as absent; do not add another interface or alter non-OpenCode argv.

- [ ] **Step 5: Verify and commit**

Run:

```bash
rtk go test ./internal/adapter -count=1
rtk go test ./internal/adapter/claude ./internal/adapter/codex ./internal/adapter/omp -count=1
```

Expected: all packages pass and existing provider argv tests remain unchanged.

Commit: `feat(adapter): support prepared command overrides`

Rollback: revert this commit; no persisted or external state is involved.

Dependencies: none.

---

### Task 2: Add Secure Runtime Provider Identity Handoff

**Objective:** Move live OpenCode identity into the verified native-session runtime boundary and remove it safely on shutdown.

**Prerequisites:** Task 1 complete.

**Files:**
- Create: `internal/session/provider_identity.go`
- Create: `internal/session/provider_identity_test.go`
- Modify: `internal/session/session.go`
- Modify: `internal/session/host.go`
- Modify: `internal/session/session_test.go`

**Interfaces:**
- Produces:

```go
func ProviderIdentityPath(dir, name string) (string, error)
func WriteProviderIdentity(dir, name, providerSessionID string) error
func ReadProviderIdentity(dir, name string) (string, error)
```

- Consumes: `ValidateName`, `VerifyDir`, `store.ValidProviderSessionID`, and `ProviderIdentityFileEnv`.

- [ ] **Step 1: Write failing path/read/write/security tests**

In `internal/session/provider_identity_test.go`, cover:

- canonical path `<verified-dir>/<name>.provider.json` and invalid session-name rejection;
- atomic `0600` JSON round trip for `ses_abc123`;
- missing file returns `"", nil`;
- invalid provider ID is rejected before write;
- symlink, directory, mode `0644`, foreign owner when the platform permits, file larger than 1024 bytes, malformed JSON, and embedded session-name mismatch fail closed;
- a failed write leaves the previous valid identity readable;
- no `.tmp` file remains after success.

Use the payload shape:

```go
type providerIdentity struct {
	SessionName       string `json:"session_name"`
	ProviderSessionID string `json:"provider_session_id"`
}
```

- [ ] **Step 2: Run the focused test and confirm the intended failure**

Run: `rtk go test ./internal/session -run ProviderIdentity -count=1`

Expected before implementation: compile failure for the three missing exported functions.

- [ ] **Step 3: Implement secure identity helpers**

In `provider_identity.go`:

- set `const maxProviderIdentityBytes = 1024`;
- validate `name` before path construction and `providerSessionID` before writing;
- call `VerifyDir` before every read or write;
- write a `0600` temporary file in the same directory, call `Sync`, close it, rename it over the canonical path, and remove the temporary file on every error;
- inspect the final/read path with `os.Lstat`, require a current-user-owned regular non-symlink file with exact `0600` mode, then open with `unix.Open(..., unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)`;
- read through `io.LimitReader(file, maxProviderIdentityBytes+1)`, reject oversize, reject unknown trailing JSON values, require the embedded name to match, and revalidate with `store.ValidProviderSessionID`;
- return `"", nil` only for `os.ErrNotExist`.

- [ ] **Step 4: Write the shutdown cleanup regression**

Extend `internal/session/session_test.go` so a short-lived host writes `<name>.provider.json`, exits immediately, persists that ID through `TryRecordSessionExit`, and removes state, socket, and provider identity files. Assert stale-host cleanup removes all three.

- [ ] **Step 5: Preserve the value before cleanup**

In `host.shutdown`, read `h.providerIdentityFile` before calling `removeSessionFiles`, pass the captured string into `recordExit(exitCode, providerID string)`, and add `ProviderIdentityPath(dir, name)` to the paths removed by `removeSessionFiles`. Keep identity-read failure advisory and sanitized through the existing host helper; never accept an unverified value.

- [ ] **Step 6: Verify and commit**

Run:

```bash
rtk go test -race ./internal/session -count=1
rtk go test ./internal/store -count=1
```

Expected: all session/store tests pass, the identity survives long enough to reach the store, and no runtime file remains after exit.

Commit: `feat(session): add secure runtime provider identity`

Rollback: revert this commit; runtime identity files are ephemeral and schema-free.

Dependencies: Task 1 only for final wiring, not for helper behavior.

---

### Task 3: Build Provider Command and Minimum-Version Validation

**Objective:** Execute the same validated OpenCode path or shell alias for probe, serve, and attach, and reject unsupported versions before session creation.

**Prerequisites:** Task 1 complete; local OpenCode 1.18.1 characterization passed.

**Files:**
- Create: `internal/adapter/opencode/command.go`
- Create: `internal/adapter/opencode/command_test.go`

**Interfaces:**
- Produces:

```go
const minimumVersion = "1.18.1"

type providerCommand struct {
	path  string
	shell string
	alias string
}

func providerCommandFor(req adapter.ResumeRequest) (providerCommand, error)
func providerCommandFromFlags(path, shell, alias string) (providerCommand, error)
func (c providerCommand) argv(args ...string) []string
func (c providerCommand) command(ctx context.Context, args ...string) *exec.Cmd
func requireMinimumVersion(ctx context.Context, command providerCommand) error
```

- Consumes: `req.ExecutablePath`, validated `req.CommandAlias`, absolute `$SHELL`, and `adapter.ShellJoin`.

- [ ] **Step 1: Write failing command-construction tests**

Cover a direct absolute executable and a shell alias. Direct `argv("serve")` must be `[]string{path, "serve"}`. Alias argv must be:

```go
[]string{shell, "-ic", "exec " + adapter.ShellJoin([]string{alias, "serve"})}
```

Reject relative paths, non-regular direct paths, empty path-and-alias, alias with unsafe characters, relative shell paths, and simultaneous direct path plus alias.

- [ ] **Step 2: Write failing version tests**

Use helper executables to return `1.18.0`, `1.18.1`, `1.19.0`, `2.0.0`, `1.18.1-beta.1`, malformed output, nonzero exit, and a response delayed beyond 750 ms. Assert only stable versions at or above `1.18.1` pass, and every error includes sanitized detected output, the required version, and `opencode upgrade 1.18.1` without leaking environment values.

Add a direct-file cache test: two probes of unchanged inode/size/mtime execute once; replacing the file changes the identity and executes again. Do not cache shell aliases because shell configuration may change without shell-binary metadata changing.

- [ ] **Step 3: Run and confirm failure**

Run: `rtk go test ./internal/adapter/opencode -run 'ProviderCommand|MinimumVersion' -count=1`

Expected before implementation: compile failure for the new types/functions.

- [ ] **Step 4: Implement command validation and version comparison**

Use an internal numeric version type:

```go
type semanticVersion struct {
	major, minor, patch int
	prerelease          bool
}
```

Parse only one trimmed `v?MAJOR.MINOR.PATCH` token with optional semver prerelease/build suffix, compare numeric components, and treat a prerelease at the floor as lower. Run the probe with a 750 ms child context. Cache successful and failed direct-path results by absolute path, size, nanosecond mtime, device, and inode; guard the map with a mutex.

- [ ] **Step 5: Verify and commit**

Run: `rtk go test -race ./internal/adapter/opencode -run 'ProviderCommand|MinimumVersion' -count=1`

Expected: all cases pass under the race detector.

Commit: `feat(opencode): require supported provider command version`

Rollback: revert this commit; it has no persisted effect.

Dependencies: Task 1.

---

### Task 4: Implement the Authenticated OpenCode HTTP and SSE Client

**Objective:** Encapsulate only the shipped 1.18.1 health/session/event/permission operations needed by the supervisor.

**Prerequisites:** OpenCode API characterization from Task 1.

**Files:**
- Create: `internal/adapter/opencode/client.go`
- Create: `internal/adapter/opencode/client_test.go`

**Interfaces:**
- Produces:

```go
type serverHealth struct {
	Healthy bool   `json:"healthy"`
	Version string `json:"version"`
}

type sessionInfo struct {
	ID        string `json:"id"`
	ParentID  string `json:"parentID,omitempty"`
	Directory string `json:"directory"`
	Title     string `json:"title"`
}

type eventEnvelope struct {
	Type       string          `json:"type"`
	Properties json.RawMessage `json:"properties"`
}

type apiClient struct {
	baseURL  *url.URL
	username string
	password string
	directory string
	http     *http.Client
}

func newAPIClient(baseURL, username, password, directory string, client *http.Client) (*apiClient, error)
func (c *apiClient) health(ctx context.Context) (serverHealth, error)
func (c *apiClient) createSession(ctx context.Context, title string) (sessionInfo, error)
func (c *apiClient) getSession(ctx context.Context, id string) (sessionInfo, error)
func (c *apiClient) replyPermission(ctx context.Context, requestID string) error
func (c *apiClient) subscribe(ctx context.Context, ready chan<- struct{}, events chan<- eventEnvelope) error
```

- [ ] **Step 1: Write failing HTTP contract tests with `httptest.Server`**

Assert every request uses Basic Auth, `X-OpenCode-Directory`, a bounded context, and the exact contracts:

- `GET /global/health` -> `{ "healthy": true, "version": "1.18.1" }`;
- `POST /session` with JSON `{ "title": "UAM: <sanitized-name>", "metadata": { "uam": true } }`;
- `GET /session/ses_abc123` with path-escaped validated ID;
- `POST /permission/per_abc123/reply` with `{ "reply": "once" }`;
- `GET /event` with `Accept: text/event-stream`.

Cover 401, 404 exact resume, 500, wrong content type, malformed JSON, response body over 1 MiB, SSE event over 256 KiB, comment/blank/multiline SSE framing, cancellation, and a server error body containing terminal controls. Assert returned errors are display-sanitized and never include the password.

- [ ] **Step 2: Run and confirm failure**

Run: `rtk go test ./internal/adapter/opencode -run 'APIClient|SSE' -count=1`

Expected before implementation: compile failure for the new client/functions.

- [ ] **Step 3: Implement bounded authenticated requests**

Construct URLs only from a parsed loopback `http://127.0.0.1:<port>` base; reject userinfo, non-loopback hosts, fragments, and non-HTTP schemes. Add Basic Auth and `X-OpenCode-Directory` centrally. Use `io.LimitReader` and reject trailing JSON. Map 404 from `getSession` to a sentinel `errSessionNotFound`; include only sanitized, length-capped response excerpts in other errors.

- [ ] **Step 4: Implement bounded SSE parsing**

Use `bufio.Reader`, accumulate only `data:` fields up to 256 KiB until a blank line, join multiple data lines with `\n`, ignore comments/unknown fields, decode one `eventEnvelope`, and send it with context cancellation. Close `ready` exactly once after status/content-type validation; an EOF returns an error so the caller controls reconnect backoff.

- [ ] **Step 5: Verify and commit**

Run: `rtk go test -race ./internal/adapter/opencode -run 'APIClient|SSE' -count=1`

Expected: contract, hostile-input, and cancellation tests pass.

Commit: `feat(opencode): add authenticated server client`

Rollback: revert this commit; no caller uses it yet.

Dependencies: Task 3 for shared version/provider-ID validation behavior.

---

### Task 5: Implement the Per-Session OpenCode Supervisor

**Objective:** Own the authenticated server and attach process, exact session lifecycle, `/new` identity updates, yolo permission replies, and bounded cleanup.

**Prerequisites:** Tasks 2–4 complete.

**Files:**
- Create: `internal/adapter/opencode/supervisor.go`
- Create: `internal/adapter/opencode/supervisor_test.go`

**Interfaces:**
- Produces:

```go
type supervisorOptions struct {
	Command           providerCommand
	Directory         string
	SessionName       string
	ProviderSessionID string
	Yolo              bool
	RuntimeDir        string
}

type ExitError struct { Code int }
func (e *ExitError) Error() string
func (e *ExitError) ExitCode() int

func RunSupervisorCommand(args []string) error
func runSupervisor(ctx context.Context, opts supervisorOptions) error
```

- Consumes: Task 2 identity helpers, Task 3 `providerCommand`, Task 4 `apiClient`, `OPENCODE_SERVER_USERNAME`, and `OPENCODE_SERVER_PASSWORD`.

- [ ] **Step 1: Write failing command parser and secret tests**

Cover direct executable flags and shell-alias flags, canonical session-name/runtime-dir validation, exact provider-ID validation, safe/yolo mode, missing/duplicate/conflicting flags, and positional-argument rejection. Capture process argv, runtime files, logs, and returned errors; assert a fixed test password appears in none of them.

- [ ] **Step 2: Write failing new/resume and prompt-order integration tests**

Use a fake OpenCode executable implemented by the Go test helper-process pattern. It must implement `serve` with the exact HTTP/SSE routes and `attach` by recording argv/env and reading stdin. Assert:

- new launch starts `serve --hostname 127.0.0.1 --port <ephemeral>`, creates one root, persists its returned ID, then runs `attach <url> --dir <cwd> --session <id>`;
- exact resume performs `GET /session/:id`, never calls create, and attaches that ID;
- a missing resume returns an actionable error and never adds `-c`, `--continue`, or another session ID;
- input queued before delayed attach is read once by attach unchanged according to current `Backend.SendLine` behavior; resume reads no stored prompt.

- [ ] **Step 3: Write failing event and permission tests**

Feed `session.created` root, child, wrong-directory, malformed, duplicate, and `/new` events. Assert only a root in the exact directory advances the runtime identity, and one supervisor cannot change another session's file. Disconnect SSE, reconnect with 25/50/100/200/400 ms capped backoff, retain the last identity, and reject ambiguous recovery.

In safe mode, assert `permission.asked` is forwarded to the TUI with zero reply calls. In yolo mode, assert one `{ "reply": "once" }` per valid emitted request, duplicate request IDs are idempotently ignored, and explicit-deny/no-event produces no reply. Track created session parent relationships so only the active root tree is eligible.

- [ ] **Step 4: Write failing lifecycle tests**

Cover readiness timeout, three port-bind attempts, server exit before attach, server death during attach, attach exit 0, attach exit 23, context cancellation, SIGHUP/SIGTERM cleanup, stuck-child SIGKILL escalation, and bounded server-log capture. After each case, assert both children are reaped and no listener or credential-bearing file remains. Run the simultaneous exit/cancel cases under `-race`.

- [ ] **Step 5: Implement startup and exact session selection**

Generate 32 random bytes with `crypto/rand` and hex-encode them. For at most three attempts, reserve and release `127.0.0.1:0`, start `serve` with replaced credential env, poll authenticated health within one five-second whole-pass deadline, and retry only a pre-readiness process/bind failure. Start SSE and wait for its ready signal before create/lookup. Create a root titled `UAM: <display-sanitized-session-name>` or validate the retained ID with `getSession`; require returned ID grammar, root status, and exact directory before `WriteProviderIdentity`.

- [ ] **Step 6: Implement attach, event loop, yolo replies, and cleanup**

Start attach with inherited stdin/stdout/stderr, credential env, exact `--dir` and `--session`, and no `-c`/`--continue`. Run the event loop with bounded reconnect. Maintain root/child ownership from `session.created`; update the file atomically only for root `/new` in the configured directory. In yolo mode reply `once` to valid owned `permission.asked`; in safe mode do nothing. On every return path cancel HTTP/SSE, terminate attach and server, wait 1500 ms, then kill and reap survivors. Keep server output in a mutex-backed 64 KiB ring and sanitize excerpts.

- [ ] **Step 7: Preserve attach exit codes**

Return nil for attach code 0 and `&ExitError{Code: code}` for codes 1–255. Treat signaled attach as code 1. Server/readiness/API failures return ordinary sanitized errors. Unit-test `ExitError.ExitCode()` and `errors.As` behavior.

- [ ] **Step 8: Verify and commit**

Run:

```bash
rtk go test -race ./internal/adapter/opencode -run 'Supervisor|Permission|Lifecycle|PromptOrder' -count=1
rtk go test ./internal/session -run 'Host|ProviderIdentity' -count=1
```

Expected: all supervisor paths finish within test deadlines, preserve exact IDs, and leave zero fake child processes.

Commit: `feat(opencode): supervise native server sessions`

Rollback: revert this commit; the supervisor is not reachable until Task 6 wiring.

Dependencies: Tasks 2, 3, and 4.

---

### Task 6: Wire the Adapter and CLI, Then Remove the Plugin Path

**Objective:** Make production OpenCode launches use the supervisor exclusively and make legacy `.mjs` state irrelevant.

**Prerequisites:** Tasks 1–5 complete.

**Files:**
- Replace: `internal/adapter/opencode/opencode.go`
- Modify: `internal/adapter/opencode/opencode_test.go`
- Modify: `internal/cli/cli.go`
- Modify: `internal/cli/cli_test.go`
- Modify: `internal/app/service_test.go`

**Interfaces:**
- Consumes: `LaunchPreparation.Command`, `providerCommandFor`, `requireMinimumVersion`, `session.ProviderIdentityPath`, `session.ReadProviderIdentity`, and `RunSupervisorCommand`.
- Produces: an `opencode.New` adapter with exact-only resume and no plugin/provider-state dependency.

- [ ] **Step 1: Replace plugin expectations with failing adapter-boundary tests**

Rewrite `opencode_test.go` to assert:

- `PrepareLaunch` rejects 1.18.0 before `Backend.CreateSession` and accepts 1.18.1;
- accepted launch command starts with the current absolute UAM executable and `__opencode`, includes validated provider command, cwd, runtime dir, session name, yolo/safe mode, and optional exact ID;
- env contains only the provider-neutral runtime identity path added by this adapter and does not add or modify `OPENCODE_CONFIG_CONTENT`;
- `LiveProviderSessionID` reads the runtime identity helper;
- `ResumeKind` is exact with a valid ID and unsupported without one;
- stale, permissive, symlinked, malformed, or directory-shaped legacy `uam-identity-plugin.mjs` and provider-state paths do not affect dispatch;
- no production source under `internal/adapter/opencode` contains `uam-identity-plugin.mjs`, `pluginSource`, `ensureProviderFiles`, `OPENCODE_CONFIG_CONTENT`, or a `"-c"` OpenCode fallback.

- [ ] **Step 2: Run and confirm failure**

Run: `rtk go test ./internal/adapter/opencode ./internal/app -run 'OpenCode|ProductionProviderResumeKind' -count=1`

Expected before wiring: plugin-oriented expectations or heuristic-resume expectations fail.

- [ ] **Step 3: Reduce `opencode.go` to adapter wiring**

Keep `New`, `prepareLaunch`, `liveProviderSessionID`, OpenCode ID validation, and test cache reset. Remove plugin source, provider-state paths, config merging, owner repair, `supportsAuto`, and `sessionArgs` fallback. `prepareLaunch` must:

1. build and validate `providerCommand` from `ResumeRequest`;
2. call `requireMinimumVersion`;
3. resolve the current UAM executable to an absolute path;
4. compute `session.ProviderIdentityPath(session.DefaultDir(), sessionName)`;
5. return `LaunchPreparation{Command: internalArgv, Env: map[string]string{session.ProviderIdentityFileEnv: identityPath}, ProviderSessionID: req.ProviderSessionID}`.

Set `ResumeKindFor` to `ResumeExact` only when the ID passes OpenCode validation; otherwise return `ResumeUnsupported`. Keep `SkipPromptOnResume = true`.

- [ ] **Step 4: Route the internal supervisor before store access**

Import `internal/adapter/opencode` in `internal/cli/cli.go` and add:

```go
case "__opencode":
	return true, opencode.RunSupervisorCommand(args[1:])
```

to `runWithoutStore`. In `Main`, before logging/printing a returned error, detect:

```go
var exitCoder interface{ ExitCode() int }
if errors.As(err, &exitCoder) {
	os.Exit(exitCoder.ExitCode())
}
```

Add CLI tests proving `__opencode` does not open the store and a helper-process test proving exit code 23 is propagated without printing credentials.

- [ ] **Step 5: Change the production resume matrix**

In `TestProductionProviderResumeKindMatrixThroughAmbiguityGuard`, replace `opencode fallback` with `opencode missing identity` and expect `ResumeUnsupported`/an exact-resume-required error rather than an allow-latest ambiguity. Assert `--allow-latest` cannot enable OpenCode heuristic resume.

- [ ] **Step 6: Verify legacy removal and commit**

Run:

```bash
rtk go test -race ./internal/adapter/opencode ./internal/cli ./internal/app -count=1
rtk rg -n 'uam-identity-plugin\.mjs|pluginSource|ensureProviderFiles|OPENCODE_CONFIG_CONTENT|\[\]string\{"-c"\}' internal/adapter/opencode
```

Expected: tests pass and `rtk rg` exits 1 with no production matches. References in historical docs are allowed.

Commit: `refactor(opencode): remove generated identity plugin`

Rollback: revert this commit to restore the old launch path; do not revert earlier dormant primitives unless bisecting.

Dependencies: Tasks 1–5.

---

### Task 7: End-to-End Regression Coverage and Documentation

**Objective:** Demonstrate independent same-project sessions, `/new`, cleanup, prompt behavior, platform compilation, and operator guidance without changing schema or UI.

**Prerequisites:** Task 6 complete.

**Files:**
- Modify: `internal/adapter/opencode/supervisor_test.go`
- Modify: `internal/adapter/opencode/opencode_test.go`
- Modify: `internal/session/session_test.go`
- Modify: `README.md`

**Interfaces:** No new production interfaces.

- [ ] **Step 1: Add the full fake-binary regression matrix**

Add deterministic tests that launch two UAM OpenCode sessions with the same cwd and distinct session names. Assert distinct authenticated ports/passwords/provider IDs, isolated `/new` updates, exact restart IDs, no prompt replay, child-session exclusion, and complete cleanup. Add Unicode/multiline/empty prompts and current `SendLine` newline normalization as characterization expectations. Add unsafe legacy paths and unknown `OPENCODE_CONFIG_CONTENT` preservation tests.

- [ ] **Step 2: Run the end-to-end package tests under race**

Run:

```bash
rtk go test -race ./internal/adapter/opencode ./internal/session ./internal/app ./internal/cli -count=1
```

Expected: zero failures/races and no leaked helper processes reported by test cleanup.

- [ ] **Step 3: Document the operator contract**

Update `README.md` to state:

- OpenCode `1.18.1+` is required and `opencode upgrade 1.18.1` is the remediation command;
- UAM uses a private loopback server per managed OpenCode terminal for exact sessions and `/new` tracking;
- default yolo and `--safe` behavior are unchanged;
- stale `$XDG_STATE_HOME/uam/providers/opencode/uam-identity-plugin.mjs` is inert and cannot block launch;
- optional manual cleanup removes only the documented UAM-generated directory after the user verifies it, with no automatic deletion;
- Linux and macOS AMD64/ARM64 are supported.

- [ ] **Step 4: Run a real OpenCode smoke test**

Build to a temporary path, use temporary UAM store/runtime directories, start a real 1.18.1 managed session under a PTY, detach, confirm its exact provider ID appears in `uam ls --json`, reattach, run `/new`, detach, confirm only that record's ID changed, then `uam kill-all` and verify no listener/process remains. Repeat with two same-project sessions and one `--safe` session. Do not submit a model prompt during this smoke test.

Expected: exact IDs are distinct/stable as described; legacy `.mjs` permissions are never inspected; all children and runtime identity files disappear after kill.

Failure signal: any latest-session substitution, prompt replay, cross-record ID change, permission auto-reply in safe mode, or orphaned process blocks completion.

- [ ] **Step 5: Commit**

Commit: `test(opencode): cover native session integration`

Rollback: revert documentation/test commit independently; production behavior remains unchanged.

Dependencies: Task 6.

---

### Task 8: Full Quality Gate and Release-Readiness Review

**Objective:** Produce final evidence that the branch is regression-safe and portable; do not push, merge, or publish.

**Prerequisites:** Tasks 1–7 complete and working tree reviewed.

**Files:** No planned source changes; fix only failures caused by this branch with a failing regression test first.

- [ ] **Step 1: Format and inspect the exact diff**

Run:

```bash
rtk gofmt -w internal/adapter/adapter.go internal/adapter/agent.go internal/adapter/opencode/*.go internal/session/*.go internal/cli/cli.go
rtk git diff --check
rtk git diff --stat main...HEAD
rtk git status --short
```

Expected: no format/diff errors; only approved files and task commits are present.

- [ ] **Step 2: Run the complete repository quality gate**

Run each independently:

```bash
rtk go test -race -count=1 -covermode=atomic -coverpkg=./... ./...
rtk go vet ./...
rtk golangci-lint run ./...
rtk staticcheck ./...
rtk gosec ./...
rtk govulncheck ./...
rtk go mod verify
```

Expected: every command exits 0; coverage is at least the current 88.1% baseline; no dependency files change.

- [ ] **Step 3: Compile all supported Darwin targets**

Run:

```bash
rtk proxy env GOOS=darwin GOARCH=amd64 go test -run '^$' ./...
rtk proxy env GOOS=darwin GOARCH=arm64 go test -run '^$' ./...
rtk proxy env GOOS=linux GOARCH=amd64 go test -run '^$' ./...
rtk proxy env GOOS=linux GOARCH=arm64 go test -run '^$' ./...
```

Expected: all four compile-only runs exit 0.

- [ ] **Step 4: Review security and compatibility invariants**

Run targeted searches and inspect results:

```bash
rtk rg -n 'uam-identity-plugin\.mjs|OPENCODE_CONFIG_CONTENT|\b-c\b|--continue' internal/adapter/opencode
rtk rg -n 'OPENCODE_SERVER_PASSWORD|password' internal/adapter/opencode
rtk git diff main...HEAD -- go.mod go.sum internal/store internal/app/app.go
```

Expected: no plugin/fallback production path; credential occurrences are env setup/redaction/tests only; no dependency, store schema, or TUI layout diff.

- [ ] **Step 5: Final review commit only if verification required a tested fix**

If verification produced a branch-caused failure, add the smallest failing regression, fix it, rerun the failed command and all earlier gates, and commit `fix(opencode): close native session regression`. Otherwise create no empty commit.

Rollback: revert only the fix commit if it independently regresses behavior.

Dependencies: all prior tasks.

---

## Tests

The mandatory pass criteria are:

1. Focused TDD command from every task first fails for the stated missing behavior, then passes.
2. `rtk go test -race -count=1 -covermode=atomic -coverpkg=./... ./...` exits 0 with coverage `>= 88.1%`.
3. Vet, golangci-lint, staticcheck, gosec, govulncheck, module verification, `git diff --check`, and all four Linux/Darwin compile matrices exit 0.
4. OpenCode 1.18.0 and floor prereleases fail before `Backend.CreateSession`; 1.18.1 and later stable builds pass.
5. New, exact resume, two-same-project, `/new`, safe/yolo, prompt, detach, stop, restart, natural exit, signal, server failure, and stale-legacy scenarios match the approved design.
6. Hostile HTTP/SSE/state data remains bounded and sanitized; credentials never appear in argv, logs, state, errors, or rendered output.
7. Store schema v3, unknown fields, CLI/JSON shapes, TUI layout, non-OpenCode provider argv, and default yolo behavior remain unchanged.

## Completion

Evidence required before claiming completion:

- task commits exist in order and the working tree is clean;
- exact commands and actual outputs from Task 8 are recorded;
- real OpenCode 1.18.1 smoke evidence shows distinct same-project IDs, isolated `/new`, exact reattach, safe/yolo parity, and orphan-free cleanup;
- production search proves the `.mjs` injection and `-c` fallback are absent;
- no push, PR, merge, install of the feature build, or release occurs without a separate explicit user instruction.

Remaining risk after completion: OpenCode may change its shipped HTTP schema in a future version. The minimum-version contract and bounded client errors make such drift fail visibly; supporting a future breaking version requires a separately tested compatibility update.
