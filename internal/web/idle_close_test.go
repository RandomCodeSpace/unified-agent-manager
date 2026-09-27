package web

import (
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// The idle sweep leaves a conversation whose Task an operation holds, and one
// that became active after the sweep listed it; a later sweep closes it.
func TestIdleSweepLeavesATaskInUse(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	start := time.Now()
	setNow(m, start)
	m.closeIdleConversations() // first seen now
	s, err := m.lookup(sum.ID)
	if err != nil {
		t.Fatal(err)
	}

	// A prompt, open or stage change holds the Task when the sweep comes.
	setNow(m, start.Add(idleClose))
	s.op.Lock()
	m.closeIdleConversations()
	s.op.Unlock()
	if conv.Closes() != 0 {
		t.Fatal("the sweep closed a conversation an operation held")
	}

	// An event arrives between the sweep's listing and its close.
	conv.EmitItem(agentapi.Item{ID: "a1", Kind: agentapi.ItemAssistant, Text: "still here"})
	m.closeIdle(s)
	if conv.Closes() != 0 {
		t.Fatal("the sweep closed a conversation active since it was listed")
	}

	setNow(m, start.Add(2*idleClose))
	m.closeIdleConversations()
	if conv.Closes() != 1 || mustSummary(t, m, sum.ID).Open {
		t.Fatalf("idle conversation not closed: closes %d", conv.Closes())
	}
}

// Background shells keep a conversation open while one runs; once all have
// finished, the conversation closes when idle.
func TestIdleSweepClosesOnceBackgroundShellsFinish(t *testing.T) {
	m, prov, _ := newTestManager(t)
	_, conv := createSession(t, m, prov)
	shells := func(status string) {
		conv.Emit(agentapi.Event{Kind: agentapi.EventBackgroundTasks, BackgroundTasks: &agentapi.BackgroundTasks{Known: true, Tasks: []agentapi.BackgroundTask{{ID: "build", Status: status}}}})
	}
	start := time.Now()
	setNow(m, start)
	shells("running")
	setNow(m, start.Add(idleClose))
	m.closeIdleConversations()
	if conv.Closes() != 0 {
		t.Fatal("closed a conversation with a running background shell")
	}
	shells("completed")
	setNow(m, start.Add(2*idleClose))
	m.closeIdleConversations()
	if conv.Closes() != 1 {
		t.Fatalf("closes = %d after the shells finished", conv.Closes())
	}
}
