package app

import (
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
)

// F34 — Shift+up/down must not move a row across the Active/Closed (or pinned)
// partition boundary: SortSessions re-buckets by Closed then Pinned on the next
// refresh, so a cross-partition swap silently reverts. The move is a no-op at
// the boundary with honest feedback, and the rows stay put.
func TestMoveSessionAcrossActiveClosedBoundaryIsNoOp(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{
		{ID: "active", AgentType: "fake", DisplayName: "active", ProcAlive: adapter.Alive},
		{ID: "closed", AgentType: "fake", DisplayName: "closed", ProcAlive: adapter.Exited, Closed: true},
	}
	m.selected = 0
	cmd := m.moveSession(1) // would cross into the Closed partition
	if cmd != nil {
		t.Fatal("a cross-partition move must not persist a new order")
	}
	if m.selected != 0 {
		t.Fatalf("selection must stay put on a boundary move, got %d", m.selected)
	}
	if m.sessions[0].ID != "active" || m.sessions[1].ID != "closed" {
		t.Fatalf("rows must not swap across the partition boundary: %+v", m.sessions)
	}
	if m.message == "" {
		t.Fatal("a boundary move should give honest feedback, not silently no-op")
	}
}

// F34 — a pinned/unpinned boundary is also a partition boundary: SortSessions
// sorts Pinned above unpinned within the RUNNING group, so swapping a pinned and
// an unpinned row would revert on refresh too.
func TestMoveSessionAcrossPinnedBoundaryIsNoOp(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{
		{ID: "pinned", AgentType: "fake", DisplayName: "pinned", ProcAlive: adapter.Alive, Pinned: true},
		{ID: "plain", AgentType: "fake", DisplayName: "plain", ProcAlive: adapter.Alive},
	}
	m.selected = 0
	if cmd := m.moveSession(1); cmd != nil {
		t.Fatal("a pinned/unpinned boundary move must not persist a new order")
	}
	if m.sessions[0].ID != "pinned" || m.sessions[1].ID != "plain" {
		t.Fatalf("rows must not swap across the pinned boundary: %+v", m.sessions)
	}
}

// F34 — a within-partition move must still swap and persist, and the resulting
// SortIndex assignment must encode the new within-partition order so the move
// survives the next refresh.
func TestSortIndexEncodesWithinPartitionOrder(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{
		{ID: "a", AgentType: "fake", DisplayName: "a", ProcAlive: adapter.Alive},
		{ID: "b", AgentType: "fake", DisplayName: "b", ProcAlive: adapter.Alive},
	}
	m.selected = 0
	cmd := m.moveSession(1) // same partition: legal swap
	if cmd == nil {
		t.Fatal("a within-partition move must persist the new order")
	}
	if m.selected != 1 || m.sessions[0].ID != "b" || m.sessions[1].ID != "a" {
		t.Fatalf("within-partition move should swap rows: selected=%d %+v", m.selected, m.sessions)
	}
	// The persisted order, fed back through SortSessions, must keep b before a.
	out := append([]adapter.Session(nil), m.sessions...)
	for i := range out {
		out[i].SortIndex = i
	}
	SortSessions(out)
	if out[0].ID != "b" || out[1].ID != "a" {
		t.Fatalf("SortIndex must encode within-partition order, got %+v", out)
	}
}
