package adapter

import (
	"context"
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
