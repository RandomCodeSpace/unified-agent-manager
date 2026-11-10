package web

import (
	"bytes"
	"os"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// A failed append keeps the turn's rows queued and readable, and a later
// flush writes them.
func TestFailedAppendKeepsTheTurnRecordsForTheNextFlush(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	// A directory in the file's place: every append fails.
	path := turnTodosPath(m, sum.ID)
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.Emit(todosEvent(true, agentapi.Todo{ID: "a", Title: "Page", Status: agentapi.TodoDone, ChangedAt: time.Unix(10, 0)}))
	conv.EmitTurn(agentapi.TurnCompleted, "")
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	timing := lastTiming(t, m, sum.ID)
	m.mu.Lock()
	queued := len(m.sessions[sum.ID].turnTodos)
	m.mu.Unlock()
	if rec, err := m.TurnTodos(sum.ID, timing.ID); err != nil || snapshotIDs(rec) != "a" || queued != 1 {
		t.Fatalf("after a failed append: %d queued, turn todos = %+v, %v", queued, rec, err)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	queued = len(m.sessions[sum.ID].turnTodos)
	m.mu.Unlock()
	data, err := os.ReadFile(path)
	if err != nil || bytes.Count(data, []byte("\n")) != 1 || queued != 0 {
		t.Fatalf("after the retry: %d queued, turn-todos.jsonl = %q, %v", queued, data, err)
	}
	if rec, err := m.TurnTodos(sum.ID, timing.ID); err != nil || snapshotIDs(rec) != "a" {
		t.Fatalf("turn todos from the file = %+v, %v", rec, err)
	}
}

func TestAfterAppendTakesWrittenRecordsAndBoundsUnwrittenOnes(t *testing.T) {
	queue := make([]int, maxQueuedTurnRecords+5)
	for i := range queue {
		queue[i] = i
	}
	if got := afterAppend(append([]int(nil), queue...), 3, false); len(got) != len(queue)-3 || got[0] != 3 {
		t.Fatalf("written: %d left, first %d", len(got), got[0])
	}
	if got := afterAppend(append([]int(nil), queue...), 3, true); len(got) != maxQueuedTurnRecords || got[0] != 5 {
		t.Fatalf("unwritten: %d left, first %d", len(got), got[0])
	}
}
