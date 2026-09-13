package app

import (
	"path/filepath"
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"

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

// #91 — Bubble Tea runs tea.Batch members concurrently, so Run can return on
// tea.Quit while the flush is still writing. The quit command must be a
// tea.Sequence whose flush has landed in the store before it yields QuitMsg.
func TestQuitSequencesFlushBeforeQuit(t *testing.T) {
	for _, key := range []string{"ctrl+c", "esc"} {
		t.Run(key, func(t *testing.T) {
			m, st := twoLiveSessionModel(t)
			m.selected = 0
			if cmd := m.moveSession(1); cmd == nil {
				t.Fatal("move should schedule a flush")
			}
			model, cmd := m.handleKey(keyMsg(key))
			m = model.(Model)
			if !m.quitting || cmd == nil {
				t.Fatalf("%s should quit with a command", key)
			}
			msg := cmd()
			if _, ok := msg.(tea.BatchMsg); ok {
				t.Fatal("quit must tea.Sequence the flush before Quit; tea.Batch gives no ordering")
			}
			steps, ok := cmdChildren(msg)
			if !ok {
				t.Fatalf("quit must return a tea.Sequence, got %T", msg)
			}
			for _, step := range steps {
				if _, quit := step().(tea.QuitMsg); !quit {
					continue
				}
				cfg, err := st.Load()
				if err != nil {
					t.Fatal(err)
				}
				recB := cfg.Sessions[store.Key("fake", "b")]
				recA := cfg.Sessions[store.Key("fake", "a")]
				if recB.SortIndex != 0 || recA.SortIndex != 1 {
					t.Fatalf("flush must land before QuitMsg: a=%d b=%d", recA.SortIndex, recB.SortIndex)
				}
				return
			}
			t.Fatal("quit command never yielded tea.QuitMsg")
		})
	}
}

// drainCmd executes a tea.Cmd and recursively runs any batched or sequenced
// children so its side effects against the store run synchronously in the test.
func drainCmd(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if children, ok := cmdChildren(cmd()); ok {
		for _, c := range children {
			drainCmd(c)
		}
	}
}

// cmdChildren unpacks a tea.BatchMsg or a tea.Sequence message into its member
// commands. Sequence's message type is unexported, so both are matched
// structurally as a []tea.Cmd.
func cmdChildren(msg tea.Msg) ([]tea.Cmd, bool) {
	v := reflect.ValueOf(msg)
	if v.Kind() != reflect.Slice || v.Type().Elem() != reflect.TypeOf(tea.Cmd(nil)) {
		return nil, false
	}
	children := make([]tea.Cmd, v.Len())
	for i := range children {
		children[i], _ = v.Index(i).Interface().(tea.Cmd)
	}
	return children, true
}
