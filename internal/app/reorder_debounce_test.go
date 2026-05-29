package app

import (
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func twoLiveSessionModel(t *testing.T) (Model, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	fake := &svcFakeAdapter{name: "fake", available: true}
	m := NewWithDeps(st, adapter.NewRegistry([]adapter.AgentAdapter{fake}))
	m.sessions = []adapter.Session{
		{ID: "a", AgentType: "fake", DisplayName: "a", ProcAlive: adapter.Alive},
		{ID: "b", AgentType: "fake", DisplayName: "b", ProcAlive: adapter.Alive},
	}
	return m, st
}

// F59 — a reorder must not persist synchronously; it schedules a debounced
// flush keyed by a sequence number. A flush carrying a stale seq (a newer move
// arrived) must be dropped, so a held Shift+arrow coalesces into one write.
func TestReorderDebounceDropsStaleFlush(t *testing.T) {
	m, _ := twoLiveSessionModel(t)
	m.selected = 0

	cmd := m.moveSession(1)
	if cmd == nil {
		t.Fatal("a within-partition move should schedule a debounced flush")
	}
	firstSeq := m.reorderSeq

	// A second move (e.g. Shift held) bumps the seq again before the first
	// flush tick fires.
	m.selected = 0
	m.sessions[0], m.sessions[1] = m.sessions[1], m.sessions[0]
	m.moveSession(1)
	if m.reorderSeq == firstSeq {
		t.Fatal("a second move must bump the reorder seq")
	}

	// The first (stale) flush tick must be dropped.
	model, flushCmd := m.Update(reorderFlushMsg{seq: firstSeq})
	m = model.(Model)
	if flushCmd != nil {
		t.Fatal("a stale reorder flush must not persist")
	}

	// The current flush tick persists.
	model, flushCmd = m.Update(reorderFlushMsg{seq: m.reorderSeq})
	m = model.(Model)
	if flushCmd == nil {
		t.Fatal("the current reorder flush must persist the order")
	}
}

// F59 — quitting with a reorder still pending must flush it (the debounce timer
// hasn't fired yet) so the manual order isn't lost.
func TestQuitFlushesPendingReorder(t *testing.T) {
	m, st := twoLiveSessionModel(t)
	m.selected = 0
	if cmd := m.moveSession(1); cmd == nil {
		t.Fatal("move should schedule a flush")
	}
	if !m.reorderPending {
		t.Fatal("a scheduled-but-unflushed reorder must be marked pending")
	}

	model, cmd := m.handleKey(keyMsg("ctrl+c"))
	m = model.(Model)
	if !m.quitting {
		t.Fatal("ctrl+c should quit")
	}
	if cmd == nil {
		t.Fatal("quit must batch a flush of the pending reorder")
	}
	// Drain the quit batch so the flush actually runs against the store.
	drainCmd(cmd)

	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	recB := cfg.Sessions[store.Key("fake", "b")]
	recA := cfg.Sessions[store.Key("fake", "a")]
	if recB.SortIndex != 0 || recA.SortIndex != 1 {
		t.Fatalf("pending reorder must be flushed on quit: a=%d b=%d", recA.SortIndex, recB.SortIndex)
	}
}

// drainCmd executes a tea.Cmd and recursively runs any batched children so its
// side effects against the store run synchronously in the test.
func drainCmd(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			drainCmd(c)
		}
	}
}
