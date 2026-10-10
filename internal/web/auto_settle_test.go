package web

import (
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// A Task quiet for a week settles itself; the settle keeps updated_at, the
// Task's last message, and says who settled it.
func TestAutoSettleAfterSevenQuietDays(t *testing.T) {
	m, prov, st := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "work", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
		t.Fatal(err)
	}
	conv.EmitTurn(agentapi.TurnCompleted, "")
	last := mustSummary(t, m, sum.ID).UpdatedAt

	setNow(m, last.Add(autoSettleAfter-time.Second))
	m.autoSettle()
	if got := mustSummary(t, m, sum.ID); got.Stage != StageActive || conv.Closes() != 0 {
		t.Fatalf("settled before a quiet week: %+v", got)
	}

	at := last.Add(autoSettleAfter)
	setNow(m, at)
	m.autoSettle()
	got := mustSummary(t, m, sum.ID)
	if got.Stage != StageSettled || got.SettledBy != settledByAuto || !got.SettledAt.Equal(at) || !got.UpdatedAt.Equal(last) || got.Open || conv.Closes() != 1 {
		t.Fatalf("after a quiet week = %+v, closes %d", got, conv.Closes())
	}
	rec, _ := loadRecord(t, st, "fake", sum.ID)
	if rec.Web.Stage != StageSettled || rec.Web.SettledBy != settledByAuto || !rec.Web.UpdatedAt.Equal(last) {
		t.Fatalf("stored = %+v", rec.Web)
	}
	m.mu.Lock()
	row := m.uamTaskRowLocked(m.sessions[sum.ID])
	m.mu.Unlock()
	if row.Stage != StageSettled || row.SettledBy != settledByAuto {
		t.Fatalf("uam task row = %+v", row)
	}
}

// A quiet Task that runs, waits or holds something the owner should see stays
// active.
func TestAutoSettleSparesTasksThatWaitOrRun(t *testing.T) {
	for name, hold := range map[string]func(*testing.T, *Manager, SessionSummary, *agenttest.Conversation){
		"working": func(t *testing.T, m *Manager, sum SessionSummary, _ *agenttest.Conversation) {
			if _, err := m.Submit(sum.ID, PromptRequest{Text: "work", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
				t.Fatal(err)
			}
		},
		"asking": func(_ *testing.T, _ *Manager, _ SessionSummary, conv *agenttest.Conversation) {
			conv.EmitInteraction(question("q1"))
		},
		"queued": func(t *testing.T, m *Manager, sum SessionSummary, conv *agenttest.Conversation) {
			if _, err := m.Submit(sum.ID, PromptRequest{Text: "work", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Submit(sum.ID, PromptRequest{Text: "later", RequestID: mustUUID(t), Mode: ModeQueue}); err != nil {
				t.Fatal(err)
			}
			conv.EmitTurn(agentapi.TurnCancelled, "") // pauses the queue
		},
		"subagent": func(_ *testing.T, _ *Manager, _ SessionSummary, conv *agenttest.Conversation) {
			conv.EmitSubagent(agentapi.Subagent{ID: "a1", Name: "survey", Status: agentapi.SubagentRunning})
		},
		"unseen failure": func(_ *testing.T, m *Manager, sum SessionSummary, _ *agenttest.Conversation) {
			setSession(m, sum.ID, func(s *webSession) { s.unseenEnd = true })
		},
		"rewind held": func(_ *testing.T, m *Manager, sum SessionSummary, _ *agenttest.Conversation) {
			setSession(m, sum.ID, func(s *webSession) { s.rewind = &store.WebRewind{RequestID: "r", State: rewindUncertain} })
		},
		"page open": func(t *testing.T, m *Manager, sum SessionSummary, _ *agenttest.Conversation) {
			sub, _, err := m.Subscribe(sum.ID)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { m.Unsubscribe(sub) })
		},
	} {
		t.Run(name, func(t *testing.T) {
			m, prov, _ := newTestManager(t)
			sum, conv := createSession(t, m, prov)
			hold(t, m, sum, conv)
			setNow(m, mustSummary(t, m, sum.ID).UpdatedAt.Add(2*autoSettleAfter))
			m.autoSettle()
			if got := mustSummary(t, m, sum.ID); got.Stage != StageActive || got.SettledBy != "" {
				t.Fatalf("settled a Task that is %s: %+v", name, got)
			}
		})
	}
}

// A Task whose op is held is left for the next sweep; the others settle in
// the same sweep.
func TestAutoSettleSkipsAHeldTask(t *testing.T) {
	m, prov, st := newTestManager(t)
	held, _ := createSession(t, m, prov)
	free, _ := createSession(t, m, prov)
	setNow(m, time.Now().Add(2*autoSettleAfter))
	s, err := m.lookup(held.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.op.Lock()
	m.autoSettle()
	s.op.Unlock()
	if mustSummary(t, m, held.ID).Stage != StageActive {
		t.Fatal("settled a Task an operation held")
	}
	if rec, _ := loadRecord(t, st, "fake", free.ID); rec.Web.Stage != StageSettled || rec.Web.SettledBy != settledByAuto {
		t.Fatalf("free Task stored = %+v", rec.Web)
	}
	m.autoSettle()
	if got := mustSummary(t, m, held.ID); got.Stage != StageSettled || got.SettledBy != settledByAuto {
		t.Fatalf("held Task after the next sweep = %+v", got)
	}
}

// Reopen clears settled_by and is activity, so the week starts again; a
// settle by hand records no settled_by.
func TestAutoSettleReopenClearsSettledByAndRestartsTheClock(t *testing.T) {
	m, prov, st := newTestManager(t)
	sum, _ := createSession(t, m, prov)
	start := mustSummary(t, m, sum.ID).UpdatedAt.Add(autoSettleAfter)
	setNow(m, start)
	m.autoSettle()
	reopenedAt := start.Add(time.Hour)
	setNow(m, reopenedAt)
	reopened, err := m.Reopen(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Stage != StageActive || reopened.SettledBy != "" || !reopened.UpdatedAt.Equal(reopenedAt) {
		t.Fatalf("reopened = %+v", reopened)
	}
	if rec, _ := loadRecord(t, st, "fake", sum.ID); rec.Web.SettledBy != "" {
		t.Fatalf("stored after reopen = %+v", rec.Web)
	}
	setNow(m, reopenedAt.Add(autoSettleAfter-time.Minute))
	m.autoSettle()
	if got := mustSummary(t, m, sum.ID); got.Stage != StageActive {
		t.Fatalf("settled within a week of the reopen: %+v", got)
	}
	settled, err := m.Settle(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	if settled.SettledBy != "" {
		t.Fatalf("settled by hand = %+v", settled)
	}
}
