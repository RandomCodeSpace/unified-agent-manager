# Fullscreen Attach Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every attached agent session fill the current terminal immediately and consistently, independent of provider harness.

**Architecture:** Keep the behavior in `internal/session`, the shared native backend used by every provider. Resize the host PTY and vterm to the attaching terminal geometry before queuing the initial redraw, then keep existing SIGWINCH resize handling for later terminal changes.

**Tech Stack:** Go, `github.com/creack/pty`, UAM native session host/attach protocol, Go tests.

---

## File Structure

- Modify `internal/session/host.go`: move attach-time resize ahead of initial redraw and keep ordering atomic under the host mutex.
- Modify `internal/session/session_test.go`: add a regression test proving attach replay uses the attaching terminal size, not the detached default size.
- Do not modify provider adapters (`internal/adapter/*`); provider-specific full-screen fixes are out of scope.
- Documentation is optional only if user-facing key behavior changes; this task changes backend consistency, not commands.

---

### Task 1: Resize Before Initial Attach Replay

**Files:**
- Modify: `internal/session/host.go:369-407`
- Modify: `internal/session/session_test.go`

- [ ] **Step 1: Write the failing regression test**

Add this test to `internal/session/session_test.go` near the other attach tests:

```go
func TestAttachReplayUsesCurrentTerminalSize(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	name := "uam-fake-feed1111"
	cmd := []string{"/bin/sh", "-c", "printf '\\033[999;999Hedge'; sleep 60"}
	if err := c.CreateSession(ctx, name, t.TempDir(), nil, cmd); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	waitFor(t, "edge rendered", func() bool {
		out, _ := c.Capture(ctx, name, 10)
		return strings.Contains(out, "edge")
	})

	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ptmx.Close(); _ = tty.Close() }()
	if err := pty.Setsize(ptmx, &pty.Winsize{Cols: 80, Rows: 24}); err != nil {
		t.Fatalf("set pty size: %v", err)
	}

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

	waitFor(t, "edge replay", func() bool { return strings.Contains(snapshot(), "edge") })
	pre := snapshot()
	if strings.Contains(pre, "\x1b[50;200H") {
		t.Fatalf("initial replay used detached default size instead of attach terminal size: %q", pre)
	}
	if !strings.Contains(pre, "\x1b[24;80H") {
		t.Fatalf("initial replay did not use attach terminal size, got %q", pre)
	}
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
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run:

```bash
go test ./internal/session -run TestAttachReplayUsesCurrentTerminalSize -count=1
```

Expected before implementation: FAIL because the replay still contains the detached default geometry (`\x1b[50;200H`) or does not contain the attach geometry (`\x1b[24;80H`).

- [ ] **Step 3: Implement attach-time resize before redraw**

In `internal/session/host.go`, change `handleAttach` so it applies the requested attach geometry while holding `h.mu` and before `h.term.Redraw()` is queued.

Use a small locked helper to avoid calling `h.applyResize` while already holding `h.mu`:

```go
func validSize(cols, rows int) bool {
	return cols > 0 && rows > 0 && cols <= 1000 && rows <= 1000
}

func (h *host) applyResizeLocked(cols, rows int) {
	if !validSize(cols, rows) {
		return
	}
	h.term.Resize(cols, rows)
	_ = pty.Setsize(h.ptmx, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}) // #nosec G115 -- bounds checked above
}

func resizeNudge(cols, rows int) (int, int, bool) {
	if rows > 1 {
		return cols, rows - 1, true
	}
	if cols > 1 {
		return cols - 1, rows, true
	}
	return 0, 0, false
}
```

Then update `applyResize`:

```go
func (h *host) applyResize(cols, rows int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.applyResizeLocked(cols, rows)
}
```

Then update `handleAttach` replay registration block:

```go
h.mu.Lock()
curCols, curRows := h.term.Size()
if validSize(req.Cols, req.Rows) {
	if req.Cols == curCols && req.Rows == curRows {
		if nudgeCols, nudgeRows, ok := resizeNudge(req.Cols, req.Rows); ok {
			h.applyResizeLocked(nudgeCols, nudgeRows)
		}
	}
	h.applyResizeLocked(req.Cols, req.Rows)
}
cl.out <- append([]byte(titleSequence(label)), h.term.Redraw()...)
h.clients[cl] = struct{}{}
h.mu.Unlock()
go h.attachWriter(cl)
h.attachReader(cl, br)
```

Remove the old post-writer `if req.Cols > 0 && req.Rows > 0 { ... }` block.

- [ ] **Step 4: Run focused tests**

Run:

```bash
go test ./internal/session -run 'TestAttachReplayUsesCurrentTerminalSize|TestTtyAttachOwnsAlternateScreen|TestReattachReplaysAgentPrivateModes|TestAttachDetachDrainsPumpBeforeScreenRestore' -count=1
```

Expected: PASS.

- [ ] **Step 5: Run full tests**

Run:

```bash
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 6: Self-review**

Check:

```bash
git diff -- internal/session/host.go internal/session/session_test.go
```

Confirm:
- The change is only in shared session backend and tests.
- No provider-specific adapter files changed.
- Non-TTY attach behavior is unchanged because invalid or zero attach sizes are ignored.
- Same-size attaches still get a nudge to force full-screen TUIs to repaint.

---

## Self-Review

- Spec coverage: shared backend behavior is covered by Task 1; provider-specific code is explicitly excluded.
- Placeholder scan: no placeholders remain.
- Type consistency: helper names and signatures are defined before use in the plan.
