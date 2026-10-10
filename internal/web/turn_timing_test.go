package web

import (
	"context"
	"encoding/json"
	"errors"
	"os"
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

func TestTurnTimingPausesForManualRequestsAndSurvivesReload(t *testing.T) {
	m, prov, st := newTestManager(t)
	start, advance := turnClock(m)
	sum, conv := createSession(t, m, prov)
	conv.EmitTurn(agentapi.TurnWorking, "")
	advance(5 * time.Second)
	conv.EmitInteraction(permissionRequest("permission"))
	sub, _, err := m.Subscribe(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Unsubscribe(sub)
	check := func(pausedAt time.Time, pausedMS int64) TurnTiming {
		t.Helper()
		got := detail(t, m, sum.ID).TurnTimings[0]
		if !got.PausedAt.Equal(pausedAt) || got.PausedMS != pausedMS {
			t.Fatalf("pause=%v/%d, want %v/%d", got.PausedAt, got.PausedMS, pausedAt, pausedMS)
		}
		return got
	}
	check(start.Add(5*time.Second), 0)
	advance(10 * time.Second)
	conv.EmitInteraction(question("question"))
	advance(20 * time.Second)
	conv.SetRespondHook(func(context.Context, string, agentapi.Answer) error { return errors.New("try again") })
	if _, err := m.Answer(sum.ID, "permission", agentapi.Answer{Decision: "allow"}); err == nil {
		t.Fatal("failed answer accepted")
	}
	check(start.Add(5*time.Second), 0)
	conv.SetRespondHook(nil)
	advance(30 * time.Second)
	if _, err := m.Answer(sum.ID, "permission", agentapi.Answer{Decision: "allow"}); err != nil {
		t.Fatal(err)
	}
	check(start.Add(5*time.Second), 0) // The question still waits; no double counting.
	advance(65 * time.Second)
	if _, err := m.Answer(sum.ID, "question", agentapi.Answer{Answers: [][]string{{"blue"}}}); err != nil {
		t.Fatal(err)
	}
	resumed := check(time.Time{}, 60_000)
	var live TurnTiming
	decodeField(t, frameOf(t, sub, "turn_timing"), "turn_timing", &live)
	if live != resumed {
		t.Fatalf("resume SSE=%+v, want %+v", live, resumed)
	}
	advance(70 * time.Second)
	conv.EmitInteraction(question("again"))
	check(start.Add(70*time.Second), 60_000)
	advance(80 * time.Second)
	conv.EmitTurn(agentapi.TurnCancelled, "")
	ended := check(time.Time{}, 70_000)
	if ended.EndedAt.Sub(ended.StartedAt)-time.Duration(ended.PausedMS)*time.Millisecond != 10*time.Second {
		t.Fatalf("cancelled turn counted waiting time: %+v", ended)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted := startManager(t, st, agenttest.NewProvider("fake", allCaps))
	if got := detail(t, restarted, sum.ID).TurnTimings[0]; got != ended {
		t.Fatalf("reload=%+v, want %+v", got, ended)
	}
}

func TestTurnTimingYoloClaimsDoNotPauseButQuestionsDo(t *testing.T) {
	m, prov, _ := newTestManager(t)
	start, advance := turnClock(m)
	sum, conv := createTask(t, m, prov, "yolo")
	conv.EmitTurn(agentapi.TurnWorking, "")
	release := make(chan struct{})
	defer close(release)
	conv.SetRespondHook(func(ctx context.Context, _ string, _ agentapi.Answer) error {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	})
	advance(5 * time.Second)
	conv.EmitInteraction(onceRequest("auto", ""))
	if got := detail(t, m, sum.ID).TurnTimings[0]; !got.PausedAt.IsZero() || got.PausedMS != 0 {
		t.Fatalf("automatic approval paused timer: %+v", got)
	}
	advance(10 * time.Second)
	conv.EmitInteraction(question("manual"))
	if got := detail(t, m, sum.ID).TurnTimings[0]; !got.PausedAt.Equal(start.Add(10 * time.Second)) {
		t.Fatalf("question in yolo mode did not pause timer: %+v", got)
	}
}

// Each model call's usage lands on the running turn, so a reader can see what the
// turn generated and how fast; usage outside a turn counts for nothing there.
func TestTurnTimingSumsModelCallUsage(t *testing.T) {
	m, prov, st := newTestManager(t)
	_, advance := turnClock(m)
	sum, conv := createSession(t, m, prov)
	conv.Emit(agentapi.Event{Kind: agentapi.EventTokens, Tokens: &agentapi.TokenUsage{Model: "m", Input: 100, Output: 10, DurationMS: 500}})
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.EmitItem(agentapi.Item{ID: "user", Kind: agentapi.ItemUser, Text: "go"})
	conv.Emit(agentapi.Event{Kind: agentapi.EventTokens, Tokens: &agentapi.TokenUsage{Model: "m", Input: 1200, Output: 300, CacheRead: 1000, DurationMS: 6000, NanoAIU: 25230000, Cost: 1}})
	advance(2 * time.Second)
	conv.Emit(agentapi.Event{Kind: agentapi.EventTokens, Tokens: &agentapi.TokenUsage{Model: "subagent", Input: 800, Output: 200, CacheRead: 600, DurationMS: 4000, NanoAIU: 4013500, Cost: 0.5}})
	conv.Emit(agentapi.Event{Kind: agentapi.EventTurn, Turn: &agentapi.Turn{State: agentapi.TurnCompleted, Model: "gpt-6-luna"}})
	got := detail(t, m, sum.ID).TurnTimings
	if len(got) != 1 || got[0].InputTokens != 2000 || got[0].OutputTokens != 500 || got[0].GenerationMS != 10000 {
		t.Fatalf("turn usage=%+v", got)
	}
	if got[0].Model != "gpt-6-luna" || got[0].CacheReadTokens != 1600 || got[0].Calls != 2 || got[0].NanoAIU != 29243500 || got[0].PremiumCost != 1.5 {
		t.Fatalf("turn waybill=%+v", got[0])
	}
	// Switching models keeps each completed turn's carrier and pricing.
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.Emit(agentapi.Event{Kind: agentapi.EventTokens, Tokens: &agentapi.TokenUsage{Model: "ollama/deepseek-v4.1-flash", Input: 200, Output: 20}})
	conv.Emit(agentapi.Event{Kind: agentapi.EventTurn, Turn: &agentapi.Turn{State: agentapi.TurnCompleted, Model: "ollama/deepseek-v4.1-flash"}})
	unpriced := detail(t, m, sum.ID).TurnTimings[1]
	if unpriced.Model != "ollama/deepseek-v4.1-flash" || unpriced.Calls != 1 || unpriced.NanoAIU != 0 || unpriced.PremiumCost != 0 {
		t.Fatalf("unpriced turn=%+v", unpriced)
	}
	encoded, err := json.Marshal(unpriced)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["nano_aiu"] != nil || fields["premium_cost"] != nil {
		t.Fatalf("unpriced turn carries postage: %s", encoded)
	}
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	saved := cfg.Sessions[store.Key(prov.Name(), sum.ID)].Web.TurnTimings
	if len(saved) != 2 || saved[0] != got[0] || saved[1] != unpriced {
		t.Fatalf("persisted waybills=%+v", saved)
	}
}

func TestTurnTimingOldJSONHasNoWaybill(t *testing.T) {
	var timing TurnTiming
	if err := json.Unmarshal([]byte(`{"id":"old","state":"completed","input_tokens":100,"output_tokens":20}`), &timing); err != nil {
		t.Fatal(err)
	}
	if timing.InputTokens != 100 || timing.OutputTokens != 20 || timing.Model != "" || timing.CacheReadTokens != 0 || timing.Calls != 0 || timing.NanoAIU != 0 || timing.PremiumCost != 0 {
		t.Fatalf("old timing=%+v", timing)
	}
}

// savedTiming reads the session's only turn timing from sessions.json, or a
// zero one while none is saved.
func savedTiming(t *testing.T, st *store.Store, key string) TurnTiming {
	t.Helper()
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := cfg.Sessions[key]
	if !ok || rec.Web == nil || len(rec.Web.TurnTimings) != 1 {
		return TurnTiming{}
	}
	return rec.Web.TurnTimings[0]
}

// fileOf stats path; nil while it does not exist. Both files are replaced by
// a rename, so a write shows as another file.
func fileOf(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func sameFile(a, b os.FileInfo) bool {
	return a == nil && b == nil || a != nil && b != nil && os.SameFile(a, b) && a.ModTime().Equal(b.ModTime())
}

// savedTokens reads the input tokens the token ledger file holds.
func savedTokens(t *testing.T, st *store.Store) int64 {
	t.Helper()
	again := NewManager(st, nil)
	if err := again.loadTokenLedger(); err != nil {
		t.Fatal(err)
	}
	return int64(again.TokenUsage().Periods["lifetime"].Total.Input)
}

// A model call is not worth rewriting sessions.json and the token ledger: its
// counts go out live and reach both files when the turn ends.
func TestModelCallUsageIsSavedWithTheTurnEnd(t *testing.T) {
	old := lazyFlushDelay
	lazyFlushDelay = time.Hour
	t.Cleanup(func() { lazyFlushDelay = old })
	m, prov, st := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	key := store.Key(prov.Name(), sum.ID)
	conv.EmitTurn(agentapi.TurnWorking, "")
	waitUntil(t, "the turn start saved", func() bool { return savedTiming(t, st, key).ID != "" })
	time.Sleep(50 * time.Millisecond)
	before, ledgerBefore := fileOf(t, st.Path()), fileOf(t, m.tokenLedgerPath())
	for range 20 {
		conv.Emit(agentapi.Event{Kind: agentapi.EventTokens, Tokens: &agentapi.TokenUsage{Model: "m", Input: 10, Output: 1, DurationMS: 100}})
	}
	if got := detail(t, m, sum.ID).TurnTimings[0]; got.InputTokens != 200 {
		t.Fatalf("live counts=%+v", got)
	}
	time.Sleep(100 * time.Millisecond)
	if !sameFile(before, fileOf(t, st.Path())) {
		t.Fatal("a model call rewrote sessions.json")
	}
	if !sameFile(ledgerBefore, fileOf(t, m.tokenLedgerPath())) {
		t.Fatal("a model call rewrote the token ledger")
	}
	conv.EmitTurn(agentapi.TurnCompleted, "")
	waitUntil(t, "the turn's counts saved", func() bool {
		got := savedTiming(t, st, key)
		return got.EndedAt.After(got.StartedAt) && got.InputTokens == 200 && got.OutputTokens == 20 && got.GenerationMS == 2000
	})
	waitUntil(t, "the ledger saved", func() bool { return savedTokens(t, st) == 200 })
}

// A long turn still saves its counts: lazyFlushDelay after the first unsaved one.
func TestModelCallUsageIsSavedWithinTheLazyFlushDelay(t *testing.T) {
	old := lazyFlushDelay
	lazyFlushDelay = 20 * time.Millisecond
	t.Cleanup(func() { lazyFlushDelay = old })
	m, prov, st := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	key := store.Key(prov.Name(), sum.ID)
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.Emit(agentapi.Event{Kind: agentapi.EventTokens, Tokens: &agentapi.TokenUsage{Model: "m", Input: 30, Output: 3}})
	waitUntil(t, "the counts saved mid-turn", func() bool { return savedTiming(t, st, key).InputTokens == 30 })
	if got := savedTiming(t, st, key); !got.EndedAt.IsZero() {
		t.Fatalf("turn ended: %+v", got)
	}
	waitUntil(t, "the ledger saved", func() bool { return savedTokens(t, st) == 30 })
	if got := detail(t, m, sum.ID).TurnTimings[0]; got.InputTokens != 30 {
		t.Fatalf("live counts=%+v", got)
	}
}
