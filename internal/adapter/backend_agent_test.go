package adapter

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/mux"
)

// Compile-time interface compliance assertions.
var (
	_ AgentAdapter     = (*BackendAgent)(nil)
	_ ResumableAdapter = (*BackendAgent)(nil)
)

// fakeBackend is an in-memory mux.Backend implementation for tests.
type fakeBackend struct {
	sessions map[mux.SessionHandle]fakeSession
}

type fakeSession struct {
	spec       mux.SpawnSpec
	lines      []string
	paneCmd    string
	panePID    int
	writeLog   [][]byte
	createTime time.Time
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{sessions: map[mux.SessionHandle]fakeSession{}}
}

func (f *fakeBackend) Spawn(ctx context.Context, spec mux.SpawnSpec) (mux.SessionHandle, error) {
	h := mux.SessionHandle(spec.SessionName)
	f.sessions[h] = fakeSession{spec: spec, paneCmd: "claude", panePID: 42, createTime: time.Now()}
	return h, nil
}

func (f *fakeBackend) Has(ctx context.Context, h mux.SessionHandle) (bool, error) {
	_, ok := f.sessions[h]
	return ok, nil
}

func (f *fakeBackend) List(ctx context.Context, prefix string) ([]mux.SessionInfo, error) {
	out := []mux.SessionInfo{}
	for h, s := range f.sessions {
		out = append(out, mux.SessionInfo{
			Handle: h, CreatedAt: s.createTime, PanePID: s.panePID,
			Cwd: s.spec.Cwd, PaneCmd: s.paneCmd,
		})
	}
	return out, nil
}

func (f *fakeBackend) Capture(ctx context.Context, h mux.SessionHandle, lines int) (mux.PaneCapture, error) {
	s := f.sessions[h]
	return mux.PaneCapture{Lines: s.lines, PaneCmd: s.paneCmd, PanePID: s.panePID, CapturedAt: time.Now()}, nil
}

func (f *fakeBackend) Write(ctx context.Context, h mux.SessionHandle, data []byte) error {
	s := f.sessions[h]
	s.writeLog = append(s.writeLog, append([]byte(nil), data...))
	f.sessions[h] = s
	return nil
}

func (f *fakeBackend) Resize(ctx context.Context, h mux.SessionHandle, cols, rows uint16) error {
	return nil
}

func (f *fakeBackend) Kill(ctx context.Context, h mux.SessionHandle) error {
	delete(f.sessions, h)
	return nil
}

func (f *fakeBackend) Attach(ctx context.Context, h mux.SessionHandle) (mux.PaneStream, error) {
	return nil, nil
}

func (f *fakeBackend) Subscribe(ctx context.Context, h mux.SessionHandle) (<-chan mux.Event, error) {
	return nil, nil
}

func TestBackendAgentDispatchCallsSpawnAndWrite(t *testing.T) {
	fb := newFakeBackend()
	a := NewBackendAgent(
		"claude",
		"Claude",
		[]CommandCandidate{{Display: "claude", Args: []string{"/bin/echo", "claude"}}},
		[]string{"--yolo"},
		DefaultPatterns("claude"),
		fb,
	)
	sess, err := a.Dispatch(context.Background(), DispatchRequest{Prompt: "hello", Cwd: "/tmp"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if sess.AgentType != "claude" {
		t.Fatalf("expected agent claude, got %q", sess.AgentType)
	}
	if len(fb.sessions) != 1 {
		t.Fatalf("expected 1 spawned session, got %d", len(fb.sessions))
	}
	for _, s := range fb.sessions {
		if len(s.writeLog) == 0 {
			t.Fatalf("expected prompt to be written, got 0 writes")
		}
	}
}

func TestBackendAgentListFiltersByPrefix(t *testing.T) {
	fb := newFakeBackend()
	// Pre-populate two sessions: one for claude, one not.
	fb.sessions["uam-claude-abc"] = fakeSession{paneCmd: "claude", panePID: 1}
	fb.sessions["uam-codex-def"] = fakeSession{paneCmd: "codex", panePID: 2}
	a := NewBackendAgent(
		"claude",
		"Claude",
		[]CommandCandidate{{Display: "claude", Args: []string{"/bin/echo"}}},
		nil,
		DefaultPatterns("claude"),
		fb,
	)
	out, err := a.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 session matching claude prefix, got %d", len(out))
	}
}

func newCoveredBackendAgent(b mux.Backend) *BackendAgent {
	return NewBackendAgent(
		"claude",
		"Claude",
		[]CommandCandidate{{Display: "claude", Args: []string{"/bin/echo", "claude"}}},
		[]string{"--yolo"},
		DefaultPatterns("claude"),
		b,
	)
}

func TestBackendAgentDisplayNameAndAvailable(t *testing.T) {
	fb := newFakeBackend()
	a := newCoveredBackendAgent(fb)
	if a.DisplayName() != "Claude" {
		t.Fatalf("DisplayName = %q", a.DisplayName())
	}
	if ok, reason := a.Available(); !ok || reason != "" {
		t.Fatalf("Available with /bin/echo present should succeed: %v %q", ok, reason)
	}

	missing := NewBackendAgent("missing", "Missing",
		[]CommandCandidate{{Display: "definitely-missing", Args: []string{"definitely-missing-uam-test-binary"}}},
		nil, DefaultPatterns("missing"), fb)
	if ok, reason := missing.Available(); ok || reason == "" {
		t.Fatalf("missing binary should report unavailable: %v %q", ok, reason)
	}
	if _, err := missing.Dispatch(context.Background(), DispatchRequest{}); err == nil {
		t.Fatal("Dispatch with unavailable command should error")
	}

	empty := NewBackendAgent("empty", "Empty", nil, nil, DefaultPatterns("empty"), fb)
	if ok, reason := empty.Available(); ok || reason != "no command configured" {
		t.Fatalf("empty candidates should say 'no command configured': %v %q", ok, reason)
	}
}

func TestBackendAgentResumeReplaysMetadataAndSkipsPromptWhenConfigured(t *testing.T) {
	fb := newFakeBackend()
	a := newCoveredBackendAgent(fb)
	a.SkipPromptOnResume = true

	preexistingCreated := time.Unix(1700000000, 0)
	sess, err := a.Resume(context.Background(), ResumeRequest{
		ID:          "deadbeef-1234-5678-9abc-def012345678",
		Name:        "bugfix",
		Prompt:      "fix parser",
		Cwd:         "/tmp/project",
		Mode:        "yolo",
		TmuxSession: "uam-claude-deadbeef",
		CreatedAt:   preexistingCreated,
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if sess.DisplayName != "bugfix" || sess.Cwd != "/tmp/project" || sess.TmuxSession != "uam-claude-deadbeef" {
		t.Fatalf("Resume did not preserve metadata: %+v", sess)
	}
	if !sess.CreatedAt.Equal(preexistingCreated) {
		t.Fatalf("Resume should preserve CreatedAt: got %v", sess.CreatedAt)
	}
	if sess.Activity != "resumed" {
		t.Fatalf("Resume Activity = %q, want resumed", sess.Activity)
	}
	for _, s := range fb.sessions {
		if len(s.writeLog) != 0 {
			t.Fatalf("SkipPromptOnResume should suppress writes, got %d", len(s.writeLog))
		}
	}
}

func TestBackendAgentResumeMintsIDWhenEmpty(t *testing.T) {
	fb := newFakeBackend()
	a := newCoveredBackendAgent(fb)
	sess, err := a.Resume(context.Background(), ResumeRequest{Prompt: "hello", Cwd: "/tmp"})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if sess.ID == "" {
		t.Fatal("Resume with empty ID should mint a fresh one")
	}
}

func TestBackendAgentDispatchSafeModeAppendsSafeArgs(t *testing.T) {
	fb := newFakeBackend()
	a := newCoveredBackendAgent(fb)
	a.SafeArgs = []string{"--safe"}
	if _, err := a.Dispatch(context.Background(), DispatchRequest{Prompt: "hi", Cwd: "/tmp", Mode: "safe"}); err != nil {
		t.Fatalf("Dispatch safe: %v", err)
	}
	if len(fb.sessions) != 1 {
		t.Fatalf("expected one spawn, got %d", len(fb.sessions))
	}
	for _, s := range fb.sessions {
		argvJoined := strings.Join(s.spec.Argv, " ")
		if !strings.Contains(argvJoined, "--safe") {
			t.Fatalf("safe-mode argv missing --safe: %q", argvJoined)
		}
		if strings.Contains(argvJoined, "--yolo") {
			t.Fatalf("safe-mode argv must not include --yolo: %q", argvJoined)
		}
	}
}

func TestBackendAgentDispatchUsesSessionArgsAndDefaultName(t *testing.T) {
	fb := newFakeBackend()
	a := newCoveredBackendAgent(fb)
	a.SessionArgs = func(req ResumeRequest, activity string) []string {
		return []string{"--marker", activity}
	}
	sess, err := a.Dispatch(context.Background(), DispatchRequest{Cwd: "/tmp/project"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if sess.DisplayName != "project" {
		t.Fatalf("default display name should be cwd basename %q, got %q", "project", sess.DisplayName)
	}
	for _, s := range fb.sessions {
		argv := strings.Join(s.spec.Argv, " ")
		if !strings.Contains(argv, "--marker dispatched") {
			t.Fatalf("SessionArgs not applied: %q", argv)
		}
	}
}

func TestBackendAgentPeekReplyStopRenameSubscribeAttachTarget(t *testing.T) {
	fb := newFakeBackend()
	// Pre-populate a session so Peek and Reply have something to address.
	handle := mux.SessionHandle("uam-claude-abc12345")
	fb.sessions[handle] = fakeSession{
		paneCmd: "claude",
		panePID: 1,
		lines:   []string{"working hard", "do you want to proceed?"},
	}
	a := newCoveredBackendAgent(fb)

	peek, err := a.Peek(context.Background(), "abc12345")
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}
	if peek.State != NeedsInput {
		t.Fatalf("Peek state = %s, want NeedsInput", peek.State)
	}
	if !strings.Contains(peek.TailText, "proceed?") {
		t.Fatalf("Peek TailText missing prompt body: %q", peek.TailText)
	}

	if err := a.Reply(context.Background(), "abc12345", "yes"); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if len(fb.sessions[handle].writeLog) != 1 {
		t.Fatalf("Reply should produce one write, got %d", len(fb.sessions[handle].writeLog))
	}
	if got := string(fb.sessions[handle].writeLog[0]); got != "yes\r" {
		t.Fatalf("Reply payload = %q, want %q", got, "yes\r")
	}

	// Passing the full handle (already prefixed) must short-circuit target().
	if err := a.Reply(context.Background(), "uam-claude-abc12345", "again"); err != nil {
		t.Fatalf("Reply via full handle: %v", err)
	}

	spec, err := a.Attach("abc12345")
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if len(spec.Argv) == 0 || spec.Argv[0] != "uam" {
		t.Fatalf("Attach Argv = %v", spec.Argv)
	}

	if err := a.Rename(context.Background(), "abc12345", "ignored"); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	ch, err := a.Subscribe(context.Background())
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if ch != nil {
		t.Fatalf("Subscribe should return nil channel for the tmux-era path, got %v", ch)
	}

	if err := a.Stop(context.Background(), "abc12345"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, exists := fb.sessions[handle]; exists {
		t.Fatal("Stop should remove the session from the backend")
	}
}

func TestBackendAgentListPropagatesBackendError(t *testing.T) {
	a := newCoveredBackendAgent(&errBackend{listErr: errSentinel})
	if _, err := a.List(context.Background()); err == nil {
		t.Fatal("expected List to surface backend error")
	}
}

func TestBackendAgentPeekPropagatesBackendError(t *testing.T) {
	a := newCoveredBackendAgent(&errBackend{captureErr: errSentinel})
	if _, err := a.Peek(context.Background(), "abc12345"); err == nil {
		t.Fatal("expected Peek to surface backend error")
	}
}

func TestBackendAgentDispatchPropagatesSpawnError(t *testing.T) {
	a := newCoveredBackendAgent(&errBackend{spawnErr: errSentinel})
	if _, err := a.Dispatch(context.Background(), DispatchRequest{Prompt: "hi", Cwd: "/tmp"}); err == nil {
		t.Fatal("expected Dispatch to surface backend spawn error")
	}
}

func TestBackendAgentDispatchPropagatesWriteError(t *testing.T) {
	a := newCoveredBackendAgent(&errBackend{writeErr: errSentinel})
	if _, err := a.Dispatch(context.Background(), DispatchRequest{Prompt: "hi", Cwd: "/tmp"}); err == nil {
		t.Fatal("expected Dispatch to surface backend write error")
	}
}

func TestPidAliveZeroPidIsDead(t *testing.T) {
	if pidAlive(0) {
		t.Fatal("pid 0 must not be reported alive")
	}
	if pidAlive(-1) {
		t.Fatal("negative pid must not be reported alive")
	}
	// Self-pid is always alive.
	if !pidAlive(os.Getpid()) {
		t.Fatal("self pid should be alive")
	}
}

func TestBackendAgentChangedRecentlyWindowExpires(t *testing.T) {
	a := newCoveredBackendAgent(newFakeBackend())
	if !a.changedRecently("k", []string{"a"}, time.Minute) {
		t.Fatal("first observation should be recent")
	}
	// Force the recorded change instant into the past so the window elapses.
	a.mu.Lock()
	prev := a.hashes["k"]
	prev.changed = time.Now().Add(-time.Hour)
	a.hashes["k"] = prev
	a.mu.Unlock()
	if a.changedRecently("k", []string{"a"}, time.Second) {
		t.Fatal("identical hash past the window must be reported as not-recent")
	}
}

// --- shared helpers ---

var errSentinel = errSimple("backend boom")

type errSimple string

func (e errSimple) Error() string { return string(e) }

// errBackend lets each individual mux.Backend method opt into a deterministic
// failure for error-propagation tests, while leaving the others as no-ops.
type errBackend struct {
	spawnErr   error
	listErr    error
	captureErr error
	writeErr   error
}

func (e *errBackend) Spawn(ctx context.Context, spec mux.SpawnSpec) (mux.SessionHandle, error) {
	if e.spawnErr != nil {
		return "", e.spawnErr
	}
	return mux.SessionHandle(spec.SessionName), nil
}

func (e *errBackend) Has(ctx context.Context, h mux.SessionHandle) (bool, error) { return true, nil }
func (e *errBackend) List(ctx context.Context, prefix string) ([]mux.SessionInfo, error) {
	return nil, e.listErr
}

func (e *errBackend) Capture(ctx context.Context, h mux.SessionHandle, lines int) (mux.PaneCapture, error) {
	if e.captureErr != nil {
		return mux.PaneCapture{}, e.captureErr
	}
	return mux.PaneCapture{}, nil
}

func (e *errBackend) Write(ctx context.Context, h mux.SessionHandle, data []byte) error {
	return e.writeErr
}

func (e *errBackend) Resize(ctx context.Context, h mux.SessionHandle, cols, rows uint16) error {
	return nil
}
func (e *errBackend) Kill(ctx context.Context, h mux.SessionHandle) error { return nil }
func (e *errBackend) Attach(ctx context.Context, h mux.SessionHandle) (mux.PaneStream, error) {
	return nil, nil
}
func (e *errBackend) Subscribe(ctx context.Context, h mux.SessionHandle) (<-chan mux.Event, error) {
	return nil, nil
}
