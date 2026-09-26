package web

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func turnClock(m *Manager) (time.Time, func(time.Duration)) {
	start := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	var elapsed atomic.Int64
	m.now = func() time.Time { return start.Add(time.Duration(elapsed.Load())) }
	return start, func(d time.Duration) { elapsed.Store(int64(d)) }
}

func TestTurnTimingForegroundBoundarySurvivesBackgroundAndReload(t *testing.T) {
	m, prov, st := newTestManager(t)
	start, advance := turnClock(m)
	sum, conv := createSession(t, m, prov)
	sub, _, err := m.Subscribe(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Unsubscribe(sub)
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.EmitItem(agentapi.Item{ID: "user", Kind: agentapi.ItemUser, Text: "start server", Time: start})
	advance(5 * time.Second)
	conv.EmitTurn(agentapi.TurnWorking, "") // Another tool/model iteration in the same turn.
	for _, item := range []agentapi.Item{{ID: "steer", Kind: agentapi.ItemUser, Delivery: agentapi.DeliverySteer}, {ID: "auto", Kind: agentapi.ItemUser, Delivery: agentapi.DeliveryAutopilot}, {ID: "child", Kind: agentapi.ItemUser, AgentID: "helper"}} {
		conv.EmitItem(item)
	}
	conv.Emit(agentapi.Event{Kind: agentapi.EventBackgroundTasks, BackgroundTasks: &agentapi.BackgroundTasks{Known: true, Tasks: []agentapi.BackgroundTask{{ID: "server", Status: "running"}}}})
	advance(12 * time.Second)
	conv.EmitTurn(agentapi.TurnCompleted, "")
	first := detail(t, m, sum.ID).TurnTimings
	if len(first) != 1 || first[0].UserItemID != "user" || !first[0].StartedAt.Equal(start) || !first[0].EndedAt.Equal(start.Add(12*time.Second)) || first[0].State != StateCompleted {
		t.Fatalf("foreground interval=%+v", first)
	}
	advance(time.Hour)
	conv.Emit(agentapi.Event{Kind: agentapi.EventBackgroundTasks, BackgroundTasks: &agentapi.BackgroundTasks{Known: true, Tasks: []agentapi.BackgroundTask{{ID: "server", Status: "completed"}}}})
	conv.EmitTurn(agentapi.TurnCompleted, "") // Late duplicate cannot extend the interval.
	if got := detail(t, m, sum.ID).TurnTimings; len(got) != 1 || got[0] != first[0] {
		t.Fatalf("background extended turn: %+v", got)
	}
	var live TurnTiming
	for i := 0; i < 3; i++ {
		decodeField(t, frameOf(t, sub, "turn_timing"), "turn_timing", &live)
	}
	if live != first[0] {
		t.Fatalf("terminal SSE=%+v want %+v", live, first[0])
	}
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if saved := cfg.Sessions[store.Key(prov.Name(), sum.ID)].Web.TurnTimings; len(saved) != 1 || saved[0] != first[0] {
		t.Fatalf("persisted interval=%+v", saved)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted := startManager(t, st, agenttest.NewProvider("fake", allCaps))
	if got := detail(t, restarted, sum.ID).TurnTimings; len(got) != 1 || got[0] != first[0] {
		t.Fatalf("reload interval=%+v", got)
	}
}

func TestTurnTimingSteerReceiptOnlyAnchorsAfterIdleEcho(t *testing.T) {
	m, prov, _ := newTestManager(t)
	start, advance := turnClock(m)
	sum, conv := createSession(t, m, prov)
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.EmitItem(agentapi.Item{ID: "start", Kind: agentapi.ItemUser, Time: start})
	conv.EmitItem(agentapi.Item{ID: "late-steer", Kind: agentapi.ItemUser, Text: "later", Time: start.Add(time.Second), Delivery: agentapi.DeliverySteer, SteerStatus: agentapi.SteerAccepted})
	conv.EmitItem(agentapi.Item{ID: "old-answer", Kind: agentapi.ItemAssistant, Text: "old turn", Time: start.Add(2 * time.Second)})
	if got := detail(t, m, sum.ID).TurnTimings; len(got) != 1 || got[0].UserItemID != "start" {
		t.Fatalf("receipt changed the running turn: %+v", got)
	}
	advance(3 * time.Second)
	conv.EmitTurn(agentapi.TurnCompleted, "")
	conv.EmitTurn(agentapi.TurnWorking, "") // Copilot delivered the steer after idle.
	conv.EmitItem(agentapi.Item{ID: "late-steer", Kind: agentapi.ItemUser, Text: "later", Time: start.Add(3 * time.Second)})
	got := detail(t, m, sum.ID)
	if len(got.TurnTimings) != 2 || got.TurnTimings[1].UserItemID != "late-steer" {
		t.Fatalf("idle echo did not anchor its real turn: %+v", got.TurnTimings)
	}
	if len(got.Items) != 3 || got.Items[0].ID != "start" || got.Items[1].ID != "old-answer" || got.Items[2].ID != "late-steer" || !got.Items[2].Time.Equal(start.Add(3*time.Second)) {
		t.Fatalf("idle echo kept receipt before old-turn content or time: %+v", got.Items)
	}
	var echoes int
	for _, item := range got.Items {
		if item.ID == "late-steer" {
			echoes++
			if item.SteerStatus != "" || item.Delivery != "" {
				t.Fatalf("provider echo did not replace receipt: %+v", item)
			}
		}
	}
	if echoes != 1 {
		t.Fatalf("receipt and echo made %d rows", echoes)
	}
}

func TestSteerReceiptEchoKeepsKnownUploadWhenProviderOmitsIt(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	known := agentapi.Attachment{ID: "upload-1", Name: "note.txt", MIME: "text/plain", Size: 7}
	at := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	conv.EmitItem(agentapi.Item{ID: "steer", Kind: agentapi.ItemUser, Text: "read note", Time: at, Delivery: agentapi.DeliverySteer, SteerStatus: agentapi.SteerAccepted, Attachments: []agentapi.Attachment{known}})
	conv.EmitItem(agentapi.Item{ID: "old-answer", Kind: agentapi.ItemAssistant, Text: "old turn", Time: at.Add(time.Second)})
	conv.EmitItem(agentapi.Item{ID: "steer", Kind: agentapi.ItemUser, Text: "read note", Time: at.Add(2 * time.Second), Delivery: agentapi.DeliverySteer})
	items := detail(t, m, sum.ID).Items
	if len(items) != 2 || items[0].ID != "steer" || !items[0].Time.Equal(at) || items[1].ID != "old-answer" || items[0].SteerStatus != "" || len(items[0].Attachments) != 1 || items[0].Attachments[0] != known {
		t.Fatalf("provider echo lost the known upload or did not replace receipt: %+v", items)
	}
}

func TestSteerReceiptHistoryKeepsKnownUploadWhenProviderOmitsIt(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	known := agentapi.Attachment{ID: "upload-1", Name: "note.txt", MIME: "text/plain", Size: 7}
	conv.EmitItem(agentapi.Item{ID: "steer", Kind: agentapi.ItemUser, Text: "read note", Delivery: agentapi.DeliverySteer, SteerStatus: agentapi.SteerAccepted, Attachments: []agentapi.Attachment{known}})
	m.mu.Lock()
	m.applyHistoryLocked(m.sessions[sum.ID], agentapi.History{Items: []agentapi.Item{{ID: "steer", Kind: agentapi.ItemUser, Text: "read note", Delivery: agentapi.DeliverySteer}}}, false)
	m.mu.Unlock()
	items := detail(t, m, sum.ID).Items
	if len(items) != 1 || items[0].SteerStatus != "" || len(items[0].Attachments) != 1 || items[0].Attachments[0] != known {
		t.Fatalf("provider history lost known receipt upload: %+v", items)
	}
}

func TestTurnTimingStopAndFailureWaitForTerminalEvidence(t *testing.T) {
	m, prov, _ := newTestManager(t)
	start, advance := turnClock(m)
	sum, conv := createSession(t, m, prov)
	conv.EmitItem(agentapi.Item{ID: "user1", Kind: agentapi.ItemUser, Time: start}) // User item may precede turn state.
	conv.EmitTurn(agentapi.TurnWorking, "")
	advance(2 * time.Second)
	if _, err := m.Cancel(sum.ID); err != nil {
		t.Fatal(err)
	}
	if got := detail(t, m, sum.ID).TurnTimings[0]; got.State != StateWorking || !got.EndedAt.IsZero() {
		t.Fatalf("Stop request invented completion: %+v", got)
	}
	advance(5 * time.Second)
	conv.EmitTurn(agentapi.TurnCancelled, "")
	advance(6 * time.Second)
	conv.EmitTurn(agentapi.TurnCancelled, "")
	advance(10 * time.Second)
	conv.EmitItem(agentapi.Item{ID: "user2", Kind: agentapi.ItemUser, Time: start.Add(10 * time.Second)})
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.EmitItem(agentapi.Item{ID: "user2", Kind: agentapi.ItemUser, Text: "updated"}) // Repeated item is not a new anchor.
	advance(13 * time.Second)
	conv.EmitTurn(agentapi.TurnFailed, "failed")
	got := detail(t, m, sum.ID).TurnTimings
	if len(got) != 2 || got[0].UserItemID != "user1" || got[0].State != StateCancelled || got[0].EndedAt.Sub(got[0].StartedAt) != 5*time.Second || got[1].UserItemID != "user2" || got[1].State != StateFailed || got[1].EndedAt.Sub(got[1].StartedAt) != 3*time.Second {
		t.Fatalf("terminal intervals=%+v", got)
	}
}

func TestTurnTimingUnknownAfterConnectionLoss(t *testing.T) {
	for _, how := range []string{"close", "exit", "shutdown"} {
		t.Run(how, func(t *testing.T) {
			m, prov, st := newTestManager(t)
			_, advance := turnClock(m)
			sum, conv := createSession(t, m, prov)
			conv.EmitTurn(agentapi.TurnWorking, "")
			conv.EmitItem(agentapi.Item{ID: "user", Kind: agentapi.ItemUser})
			advance(9 * time.Second)
			switch how {
			case "close":
				if _, err := m.Close(sum.ID); err != nil {
					t.Fatal(err)
				}
			case "exit":
				conv.Emit(agentapi.Event{Kind: agentapi.EventExit, Error: "lost connection"})
			case "shutdown":
				if err := m.Shutdown(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			got := detail(t, m, sum.ID).TurnTimings
			if len(got) != 1 || got[0].State != "unknown" || !got[0].EndedAt.IsZero() {
				t.Fatalf("connection loss invented endpoint: %+v", got)
			}
			if how == "shutdown" {
				restarted := startManager(t, st, agenttest.NewProvider("fake", allCaps))
				if got := detail(t, restarted, sum.ID).TurnTimings; len(got) != 1 || got[0].State != "unknown" || !got[0].EndedAt.IsZero() {
					t.Fatalf("restart resumed elapsed clock: %+v", got)
				}
			}
		})
	}
}

func TestTurnTimingHistoryDoesNotInventDurations(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	m.mu.Lock()
	s := m.sessions[sum.ID]
	m.upsertItemLocked(s, agentapi.Item{ID: "old-user", Kind: agentapi.ItemUser, Time: time.Now().Add(-time.Hour)}, false)
	m.upsertItemLocked(s, agentapi.Item{ID: "old-reply", Kind: agentapi.ItemAssistant, Time: time.Now()}, false)
	m.mu.Unlock()
	conv.EmitTurn(agentapi.TurnCompleted, "")
	if got := detail(t, m, sum.ID).TurnTimings; len(got) != 0 {
		t.Fatalf("history fabricated duration: %+v", got)
	}
	conv.EmitTurn(agentapi.TurnWorking, "")
	if got := detail(t, m, sum.ID).TurnTimings; len(got) != 1 || got[0].UserItemID != "" {
		t.Fatalf("new work attached to historical prompt: %+v", got)
	}
	record := store.SessionRecord{ID: "old", Web: &store.WebState{TurnTimings: []store.TurnTiming{{ID: "interrupted", State: StateWorking, StartedAt: time.Now()}}}}
	loaded := sessionFromRecord(record)
	if loaded.activeTiming != -1 || loaded.turnTimings[0].State != "unknown" || !loaded.turnTimings[0].EndedAt.IsZero() {
		t.Fatalf("stale persisted interval=%+v", loaded.turnTimings)
	}
}
