# Terminal Cleanup, OpenCode Default, and New Auto-Attach Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep the shell clean after returning from UAM-managed sessions, make OpenCode the default provider for new configs, and make `uam new` attach to the session it just created.

**Architecture:** Add an internal quiet-attach mode that suppresses the post-detach note only for attach flows that return to UAM, while preserving direct attach behavior at the session layer. Centralize the default provider as a store constant set to `opencode`. Change the guided CLI `new` flow to dispatch, then immediately reuse the existing attach path for the created session.

**Tech Stack:** Go, Bubble Tea, native PTY/session backend in `internal/session`, store JSON config in `internal/store`, tests with `go test`.

---

## File Structure

- Modify `internal/session/attach.go`: add quiet attach options and a shared env var name; suppress the post-detach note when quiet mode is active.
- Modify `internal/session/session_test.go`: keep current direct-attach note coverage and add quiet-mode regression coverage.
- Modify `internal/app/app.go`: mark TUI-launched attach commands quiet so detach notes do not sit behind the dashboard.
- Modify `internal/app/app_more_test.go`: assert app attach commands include the quiet env var.
- Modify `internal/cli/cli.go`: mark CLI attach flows that return to the dashboard quiet; change `uam new` to attach after dispatch.
- Modify `internal/cli/cli_test.go`: assert `uam new` dispatches and attaches via the existing attach flow.
- Modify `internal/store/store.go`: introduce `DefaultAgentName = "opencode"` and use it in default and normalize paths.
- Modify `internal/store/store_test.go` and `internal/store/store_more_test.go`: update default-agent expectations.
- Modify `internal/app/app_more_test.go`: assert the app starts with OpenCode when OpenCode is available.
- Modify `main_test.go`: adapt end-to-end `uam new` tests so they detach from the auto-opened session.
- Modify `README.md`: document OpenCode default and that `uam new` opens the created session.

---

### Task 1: Centralize and Change the Default Provider to OpenCode

**Files:**
- Modify: `internal/store/store.go:23-31`, `internal/store/store.go:231-238`, `internal/store/store.go:475-481`
- Modify: `internal/app/app.go:149-155`
- Test: `internal/store/store_test.go:14-33`
- Test: `internal/store/store_more_test.go:65-71`
- Test: `internal/app/app_more_test.go`

- [ ] **Step 1: Write failing store expectations for OpenCode**

In `internal/store/store_test.go`, change the default-agent assertion in `TestLoadMissingReturnsDefaultConfig` to:

```go
if cfg.DefaultAgent != "opencode" {
	t.Fatalf("default agent = %q", cfg.DefaultAgent)
}
```

In `internal/store/store_more_test.go`, change the normalize assertion in `TestNormalizeMigrateBackupMoveAside` to:

```go
if cfg.SchemaVersion != CurrentSchemaVersion || cfg.DefaultAgent != "opencode" || cfg.UI.PeekWidth != 60 || cfg.Sessions == nil {
	t.Fatalf("bad normalize %+v", cfg)
}
```

- [ ] **Step 2: Add app-level failing coverage for the initial default**

Add this test to `internal/app/app_more_test.go`:

```go
func TestNewWithDepsStartsFromOpenCodeDefault(t *testing.T) {
	fake := &svcFakeAdapter{name: "opencode", available: true}
	m := NewWithDeps(nil, adapter.NewRegistry([]adapter.AgentAdapter{fake}))
	if m.defaultAgent != "opencode" {
		t.Fatalf("default agent = %q, want opencode", m.defaultAgent)
	}
}
```

If `adapter` is not already imported in `internal/app/app_more_test.go`, add it to that file's import block:

```go
"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
```

- [ ] **Step 3: Run the focused failing tests**

Run:

```bash
go test ./internal/store ./internal/app
```

Expected before implementation: failures mentioning `default agent = "claude"` or `default agent = "codex"` depending on registry availability.

- [ ] **Step 4: Implement the OpenCode default constant**

In `internal/store/store.go`, add this constant near the existing default constants:

```go
const DefaultAgentName = "opencode"
```

Change `DefaultConfig` in `internal/store/store.go` to:

```go
func DefaultConfig() Config {
	return Config{
		SchemaVersion: CurrentSchemaVersion,
		DefaultAgent:  DefaultAgentName,
		Sessions:      map[string]SessionRecord{},
		UI:            UISettings{Sort: "state", PeekWidth: 60},
	}
}
```

Change the blank default fallback in `normalize` to:

```go
if cfg.DefaultAgent == "" {
	cfg.DefaultAgent = DefaultAgentName
}
```

Change `NewWithDeps` in `internal/app/app.go` from the hard-coded `"claude"` default to:

```go
m := Model{service: NewService(st, reg), defaultAgent: store.DefaultAgentName, wizardCwd: ".", execProcess: tea.ExecProcess, lastPeekAt: map[string]time.Time{}, peekClock: time.Now}
```

Update the adjacent comment to refer to the baked-in OpenCode default:

```go
// The baked-in OpenCode default may not be installed; reconcile it to an
// enabled provider so Enter-with-no-input and the prompt hint never point at
// a disabled agent (C2-9).
```

- [ ] **Step 5: Verify the focused tests pass**

Run:

```bash
go test ./internal/store ./internal/app
```

Expected after implementation: both packages pass.

---

### Task 2: Add Quiet Attach Mode to Prevent Shell Residue

**Files:**
- Modify: `internal/session/attach.go:61-184`
- Test: `internal/session/session_test.go:345-520`

- [ ] **Step 1: Add failing quiet-mode session test**

Add this test after `TestAttachDetachDrainsPumpBeforeScreenRestore` in `internal/session/session_test.go`:

```go
func TestAttachQuietSuppressesPrimaryScreenNotice(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	name := "uam-fake-quiet01"
	if err := c.CreateSession(ctx, name, t.TempDir(), nil, []string{"/bin/sh", "-c", "echo banner; sleep 60"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	waitFor(t, "banner", func() bool {
		out, _ := c.Capture(ctx, name, 10)
		return strings.Contains(out, "banner")
	})

	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ptmx.Close(); _ = tty.Close() }()

	done := make(chan error, 1)
	go func() { done <- runAttachWithOptions(c.Dir, name, tty, tty, attachOptions{quiet: true}) }()

	var mu sync.Mutex
	var got []byte
	go func() {
		buf := make([]byte, 4096)
		for {
			n, rerr := ptmx.Read(buf)
			if n > 0 {
				mu.Lock()
				got = append(got, buf[:n]...)
				mu.Unlock()
			}
			if rerr != nil {
				return
			}
		}
	}()
	snapshot := func() string {
		mu.Lock()
		defer mu.Unlock()
		return string(got)
	}

	waitFor(t, "replay banner", func() bool { return strings.Contains(snapshot(), "banner") })
	if _, err := ptmx.Write([]byte{0x02, 'd'}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runAttach: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("detach chord did not detach")
	}

	full := snapshot()
	exit := strings.LastIndex(full, "\x1b[?1049l")
	if exit < 0 {
		t.Fatalf("detach must leave the alternate screen: %q", full)
	}
	tail := strings.ReplaceAll(full[exit+len("\x1b[?1049l"):], "\r", "")
	if strings.Contains(tail, "[uam:") {
		t.Fatalf("quiet attach must not print a primary-screen notice, tail=%q full=%q", tail, full)
	}
}
```

- [ ] **Step 2: Run the focused failing test**

Run:

```bash
go test ./internal/session -run TestAttachQuietSuppressesPrimaryScreenNotice -count=1
```

Expected before implementation: compile failure for `runAttachWithOptions` or `attachOptions`, or a failure because `[uam: detached]` still appears.

- [ ] **Step 3: Implement quiet attach options**

In `internal/session/attach.go`, add these declarations near the detach constants:

```go
const AttachQuietEnv = "UAM_ATTACH_QUIET"

type attachOptions struct {
	quiet bool
}
```

Change `RunAttach` to read the quiet env var and call the option-aware function:

```go
func RunAttach(args []string) error {
	fs := flag.NewFlagSet("__attach", flag.ContinueOnError)
	dir := fs.String("dir", DefaultDir(), "session runtime directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return errors.New("attach requires a session name")
	}
	return runAttachWithOptions(*dir, fs.Arg(0), os.Stdin, os.Stdout, attachOptions{quiet: os.Getenv(AttachQuietEnv) == "1"})
}
```

Keep the existing test helper entrypoint by adding this wrapper:

```go
func runAttach(dir, name string, stdin *os.File, stdout *os.File) error {
	return runAttachWithOptions(dir, name, stdin, stdout, attachOptions{})
}
```

Rename the current `runAttach` implementation to:

```go
func runAttachWithOptions(dir, name string, stdin *os.File, stdout *os.File, opts attachOptions) error {
```

At the end of that function, replace the unconditional note with:

```go
if !opts.quiet {
	_, _ = fmt.Fprintf(stdout, "\r\n[uam: %s]\r\n", note)
}
return nil
```

- [ ] **Step 4: Verify direct attach behavior is preserved**

Run:

```bash
go test ./internal/session -run 'TestAttachOwnsTerminalStateOnTTY|TestAttachDetachDrainsPumpBeforeScreenRestore|TestAttachQuietSuppressesPrimaryScreenNotice' -count=1
```

Expected after implementation: direct attach tests still see `[uam: detached]`; quiet test does not.

---

### Task 3: Mark UAM-Returning Attach Paths as Quiet

**Files:**
- Modify: `internal/app/app.go:1136-1148`
- Modify: `internal/cli/cli.go:393-408`
- Test: `internal/app/app_more_test.go:135-173`
- Test: `internal/cli/cli_test.go:146-169`

- [ ] **Step 1: Add failing app test for quiet attach env**

In `internal/app/app_more_test.go`, update `TestDispatchedMessageAttachesNewSession` so its fake runner captures the command env:

```go
var gotArgs []string
var gotEnv []string
m.execProcess = func(cmd *exec.Cmd, cb tea.ExecCallback) tea.Cmd {
	gotArgs = append([]string(nil), cmd.Args...)
	gotEnv = append([]string(nil), cmd.Env...)
	return func() tea.Msg { return cb(nil) }
}
```

After the existing `gotArgs` assertion, add:

```go
if !envContains(gotEnv, session.AttachQuietEnv, "1") {
	t.Fatalf("attach command env missing %s=1: %v", session.AttachQuietEnv, gotEnv)
}
```

Add this helper to `internal/app/app_more_test.go`:

```go
func envContains(env []string, key, value string) bool {
	want := key + "=" + value
	for _, item := range env {
		if item == want {
			return true
		}
	}
	return false
}
```

If `session` is not already imported in `internal/app/app_more_test.go`, add it:

```go
"github.com/RandomCodeSpace/unified-agent-manager/internal/session"
```

- [ ] **Step 2: Add failing CLI attach env coverage through new behavior**

In `internal/cli/cli_test.go`, extend `cliFakeAdapter` to record attach calls and keep returning `echo`:

```go
attached []string
```

Change its `Attach` method to:

```go
func (f *cliFakeAdapter) Attach(id string) (adapter.AttachSpec, error) {
	f.attached = append(f.attached, id)
	return adapter.AttachSpec{Argv: []string{"echo", id}}, nil
}
```

In `TestRunCommandAttachLastAndNew`, after the `uam new` call, assert the new session was attached and the dashboard runner was called a third time:

```go
out := captureCLIStdout(t, func() {
	withCLIStdin(t, "fake\n\n/tmp\n#from-new prompt\n", func() { must(t, runNew(context.Background(), svc, runTUI)) })
})
if !strings.Contains(out, "abc12345") {
	t.Fatalf("new should attach the created session, output=%q", out)
}
if tuiCalls != 3 {
	t.Fatalf("TUI calls=%d, want 3", tuiCalls)
}
if len(fake.attached) == 0 || fake.attached[len(fake.attached)-1] != "abc12345" {
	t.Fatalf("new did not attach created session, attached=%v", fake.attached)
}
```

- [ ] **Step 3: Run focused failing tests**

Run:

```bash
go test ./internal/app ./internal/cli -run 'TestDispatchedMessageAttachesNewSession|TestRunCommandAttachLastAndNew' -count=1
```

Expected before implementation: app env assertion fails, and CLI test compile fails until `runNew` accepts `runTUI` and auto-attaches.

- [ ] **Step 4: Set quiet env in app attach execution**

In `internal/app/app.go`, after creating `cmd` in `execAttachSpec`, add:

```go
cmd.Env = append(os.Environ(), session.AttachQuietEnv+"=1")
```

The function should end like this:

```go
cmd := exec.Command(spec.Argv[0], spec.Argv[1:]...) // #nosec G204 -- attach argv is generated by trusted agent adapters, no shell expansion.
cmd.Env = append(os.Environ(), session.AttachQuietEnv+"=1")
return runner(cmd, func(err error) tea.Msg { return attachFinishedMsg{err: err} })
```

- [ ] **Step 5: Set quiet env in CLI attach execution**

In `internal/cli/cli.go`, after creating `cmd` in `execAttach`, add:

```go
cmd.Env = append(os.Environ(), session.AttachQuietEnv+"=1")
```

The command setup should be:

```go
cmd := exec.CommandContext(ctx, spec.Argv[0], spec.Argv[1:]...) // #nosec G204 -- attach argv is generated by trusted agent adapters, no shell expansion.
cmd.Stdin = os.Stdin
cmd.Stdout = os.Stdout
cmd.Stderr = os.Stderr
cmd.Env = append(os.Environ(), session.AttachQuietEnv+"=1")
```

- [ ] **Step 6: Verify focused tests pass**

Run:

```bash
go test ./internal/app ./internal/cli -run 'TestDispatchedMessageAttachesNewSession|TestRunCommandAttachLastAndNew' -count=1
```

Expected after implementation: both focused tests pass.

---

### Task 4: Make `uam new` Attach the Created Session

**Files:**
- Modify: `internal/cli/cli.go:89-96`, `internal/cli/cli.go:299-356`
- Modify: `internal/cli/cli_test.go:146-169`
- Modify: `main_test.go:105-166`

- [ ] **Step 1: Change CLI tests to call the new signature**

In `internal/cli/cli_test.go`, change direct calls from:

```go
runNew(context.Background(), svc)
```

to:

```go
runNew(context.Background(), svc, runTUI)
```

Use this `runTUI` value in tests that only need a no-op dashboard return:

```go
runTUI := func(ctx context.Context, model tea.Model) error { return nil }
```

- [ ] **Step 2: Update `runCommand` to pass the TUI runner into `runNew`**

In `internal/cli/cli.go`, change the `new` branch from:

```go
case "new":
	return runNew(ctx, svc)
```

to:

```go
case "new":
	return runNew(ctx, svc, runTUI)
```

- [ ] **Step 3: Change `runNew` signature and attach after dispatch**

Change the `runNew` signature in `internal/cli/cli.go` to:

```go
func runNew(ctx context.Context, svc *app.Service, runTUI func(context.Context, tea.Model) error) error {
```

Replace the final dispatch success block:

```go
fmt.Printf("dispatched %s (%s)\n", sess.ID, sess.SessionName)
return nil
```

with:

```go
if sess.ID == "" {
	return errors.New("new: dispatched session has empty id")
}
return execAttach(ctx, svc, sess.ID, runTUI)
```

Keep the existing warning behavior for advisory dispatch errors:

```go
if err != nil {
	if sess.ID == "" {
		return err
	}
	fmt.Fprintln(os.Stderr, "warning:", err)
}
```

- [ ] **Step 4: Update main end-to-end `uam new` tests to detach**

In `main_test.go`, update `TestRunNew` input so the attach subprocess receives a detach chord after the prompt line:

```go
_, _ = w.WriteString("claude\n\n/tmp\nfrom wizard\n\x02d")
```

In the same test, replace the old stdout assertion with a TUI-return assertion. Set `runTUIFn` around the test:

```go
oldRunTUI := runTUIFn
defer func() { runTUIFn = oldRunTUI }()
returnedToTUI := false
runTUIFn = func(ctx context.Context, model tea.Model) error {
	returnedToTUI = true
	return nil
}
```

After running `uam new`, assert:

```go
if !returnedToTUI {
	t.Fatal("new should attach and then return to UAM TUI")
}
```

In `TestRunNewAllowsEmptyPrompt`, update the input similarly:

```go
_, _ = w.WriteString("claude\n\n/tmp\n\n\x02d")
```

Replace the `dispatched` output assertion with the same `returnedToTUI` assertion and keep the existing store assertions that verify one prompt-less session was persisted.

- [ ] **Step 5: Verify the new flow tests pass**

Run:

```bash
go test ./internal/cli . -run 'TestRunCommandAttachLastAndNew|TestRunNew|TestRunNewAllowsEmptyPrompt' -count=1
```

Expected after implementation: the guided new flow dispatches, attaches, detaches through the supplied chord in tests, and returns through the dashboard runner.

---

### Task 5: Document the User-Visible Behavior

**Files:**
- Modify: `README.md:63-83`
- Modify: `README.md:85-100`

- [ ] **Step 1: Update quick start text**

In `README.md`, replace:

```md
Guided dispatch flow:
```

with:

```md
Guided dispatch flow, using OpenCode by default when it is available, then opening the created session immediately:
```

- [ ] **Step 2: Update CLI summary**

In the CLI command list, replace:

```md
uam new                          # guided dispatch wizard
```

with:

```md
uam new                          # guided dispatch wizard, then attach
```

- [ ] **Step 3: Verify docs-only diff is intentional**

Run:

```bash
git diff -- README.md
```

Expected: only the two wording changes above.

---

### Task 6: Full Verification

**Files:**
- No source changes in this task.

- [ ] **Step 1: Run focused package tests**

Run:

```bash
go test ./internal/session ./internal/app ./internal/cli ./internal/store -count=1
```

Expected: all four packages pass.

- [ ] **Step 2: Run the full test suite**

Run:

```bash
go test ./... -count=1
```

Expected: all packages pass.

- [ ] **Step 3: Inspect the final diff**

Run:

```bash
git diff -- internal/session/attach.go internal/session/session_test.go internal/app/app.go internal/app/app_more_test.go internal/cli/cli.go internal/cli/cli_test.go internal/store/store.go internal/store/store_test.go internal/store/store_more_test.go main_test.go README.md
```

Expected: the diff contains only quiet attach support, OpenCode default, `uam new` auto-attach, tests, and README updates.

- [ ] **Step 4: Manual smoke test in a real terminal**

Run from a real terminal after building:

```bash
go run ./cmd/uam
```

In the TUI, open an OpenCode or Copilot session, detach with `Ctrl+B d`, quit UAM with `Esc`, and confirm the shell does not show `[uam: detached]` or agent screen residue.

Then run:

```bash
go run ./cmd/uam new
```

Accept or enter the provider, complete the prompts, confirm the created session opens immediately, detach with `Ctrl+B d`, and confirm UAM returns cleanly.

---

## Self-Review

- Spec coverage: The plan covers the shell residue root cause, OpenCode default, and `uam new` auto-attach.
- Placeholder scan: No placeholders are present; every task has concrete files, code snippets, commands, and expected outcomes.
- Type consistency: The plan consistently uses `store.DefaultAgentName`, `session.AttachQuietEnv`, `attachOptions`, and `runAttachWithOptions`.
- Scope: The plan avoids unrelated terminal-emulator filtering because the user clarified the residue appears when returning to shell, which points at the primary-screen note path.

## Commit Policy

Do not commit these changes unless the user explicitly asks for a commit. The final implementation should leave the worktree with reviewed source changes and passing verification output.
