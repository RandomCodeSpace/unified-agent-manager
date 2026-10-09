package web

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

type turnChangeConversation struct {
	agentapi.Conversation
	read func(context.Context, string) (agentapi.NativeTurnChanges, error)
}

func (c *turnChangeConversation) TurnChanges(ctx context.Context, user string) (agentapi.NativeTurnChanges, error) {
	return c.read(ctx, user)
}

func nativeTurnFacts(event string, added int64) agentapi.NativeTurnChanges {
	return agentapi.NativeTurnChanges{Status: "available", EventID: event, Files: 1, Additions: added, Deletions: 1, Entries: []agentapi.NativeTurnFile{{Path: "/work/same.txt", Kind: "modified", Additions: added, Deletions: 1}}}
}
func waitTurnChanges(t *testing.T, m *Manager, id, status string) TurnTiming {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		timings := detail(t, m, id).TurnTimings
		if len(timings) > 0 && timings[len(timings)-1].Changes != nil && timings[len(timings)-1].Changes.Status == status {
			return timings[len(timings)-1]
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("native turn snapshot did not settle")
	return TurnTiming{}
}
func ownTurnChangeReader(m *Manager, id string, c *agenttest.Conversation, read func(context.Context, string) (agentapi.NativeTurnChanges, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	s.nativeChanges = true
	s.conv = &turnChangeConversation{Conversation: c, read: read}
}

// The capture keeps one read slot until after its final locked application.
// Occupying the other slots, then acquiring the last one, waits for that work
// without changing any production guard through shutdown or a new SDK event.
func finishTurnChangeCapture(t *testing.T, m *Manager, unblock func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for range maxHistoryReads - 1 {
		release, err := m.readSlot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
	}
	unblock()
	release, err := m.readSlot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
}

func TestTurnChangesTwoTurnsStayImmutableThroughCloseAndReload(t *testing.T) {
	m, p, st := newTestManager(t)
	sum, c := createSession(t, m, p)
	var reads atomic.Int64
	ownTurnChangeReader(m, sum.ID, c, func(_ context.Context, user string) (agentapi.NativeTurnChanges, error) {
		reads.Add(1)
		if user == "first" {
			return nativeTurnFacts("native-first", 2), nil
		}
		return nativeTurnFacts("native-second", 7), nil
	})
	c.EmitTurn(agentapi.TurnWorking, "")
	c.EmitItem(agentapi.Item{ID: "first", Kind: agentapi.ItemUser})
	c.EmitTurn(agentapi.TurnCompleted, "")
	first := waitTurnChanges(t, m, sum.ID, "available")
	c.EmitTurn(agentapi.TurnWorking, "")
	c.EmitItem(agentapi.Item{ID: "second", Kind: agentapi.ItemUser})
	c.EmitTurn(agentapi.TurnFailed, "partial failure")
	second := waitTurnChanges(t, m, sum.ID, "available")
	for range 40 {
		c.Emit(agentapi.Event{Kind: agentapi.EventTokens, Tokens: &agentapi.TokenUsage{Input: 1, Output: 1}})
	}
	c.EmitTurn(agentapi.TurnCompleted, "") // Duplicate end cannot recapture an older suffix.
	if reads.Load() != 2 {
		t.Fatalf("native reads on tokens/duplicate end=%d", reads.Load())
	}
	encoded, err := json.Marshal(detail(t, m, sum.ID))
	if err != nil || strings.Contains(string(encoded), "/work/same.txt") {
		t.Fatalf("detail retained file rows=%s,%v", encoded, err)
	}
	if first.Changes.Additions != 2 || second.Changes.Additions != 7 || first.ID == second.ID {
		t.Fatalf("owner facts=%+v/%+v", first, second)
	}
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	opens := len(p.Opens())
	got, err := m.TurnChanges(sum.ID, first.ID)
	if err != nil || got.Counts != *first.Changes || got.Files[0].Additions != 2 {
		t.Fatalf("closed historic=%+v,%v", got, err)
	}
	got.Files[0].Path = "mutated"
	d := detail(t, m, sum.ID)
	d.TurnTimings[0].Changes.Additions = 999
	again, err := m.TurnChanges(sum.ID, first.ID)
	if err != nil || again.Files[0].Path != "/work/same.txt" || again.Counts.Additions != 2 || len(p.Opens()) != opens {
		t.Fatalf("readonly ownership=%+v,%v", again, err)
	}
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	persisted := cfg.Sessions[store.Key(p.Name(), sum.ID)].Web.TurnTimings
	if len(persisted) != 2 || persisted[0].Changes.Additions != 2 {
		t.Fatalf("persisted=%+v", persisted)
	}
	loaded := sessionFromRecord(cfg.Sessions[store.Key(p.Name(), sum.ID)])
	persisted[0].Changes.Additions = 999
	if loaded.turnTimings[0].Changes.Additions != 2 {
		t.Fatal("loaded pointer aliases config")
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted := startManager(t, st, agenttest.NewProvider(p.Name(), allCaps))
	after, err := restarted.TurnChanges(sum.ID, first.ID)
	if err != nil || after.Counts.Additions != 2 || after.Files[0].Path != "/work/same.txt" {
		t.Fatalf("reloaded=%+v,%v", after, err)
	}
}

func TestTurnChangesInFlightRejectsLaterActivityAndGeneration(t *testing.T) {
	for _, tc := range []struct {
		name       string
		invalidate func(*Manager, *webSession, *agenttest.Conversation)
	}{
		{"new owner", func(_ *Manager, _ *webSession, c *agenttest.Conversation) {
			c.EmitItem(agentapi.Item{ID: "new-owner", Kind: agentapi.ItemUser})
		}},
		{"working", func(_ *Manager, _ *webSession, c *agenttest.Conversation) { c.EmitTurn(agentapi.TurnWorking, "") }},
		{"partial tool", func(_ *Manager, _ *webSession, c *agenttest.Conversation) {
			c.EmitItem(agentapi.Item{ID: "partial", Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "edit", Status: agentapi.ToolFailed}})
		}},
		{"conversation generation", func(m *Manager, s *webSession, _ *agenttest.Conversation) { m.mu.Lock(); s.gen++; m.mu.Unlock() }},
		{"history generation", func(m *Manager, s *webSession, _ *agenttest.Conversation) { m.mu.Lock(); s.historyGen++; m.mu.Unlock() }},
		{"close", func(m *Manager, s *webSession, _ *agenttest.Conversation) { _, _ = m.Close(s.id) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, p, _ := newTestManager(t)
			sum, c := createSession(t, m, p)
			entered, release := make(chan struct{}), make(chan struct{})
			ownTurnChangeReader(m, sum.ID, c, func(ctx context.Context, _ string) (agentapi.NativeTurnChanges, error) {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return agentapi.NativeTurnChanges{}, ctx.Err()
				}
				return nativeTurnFacts("native", 4), nil
			})
			c.EmitTurn(agentapi.TurnWorking, "")
			c.EmitItem(agentapi.Item{ID: "owner", Kind: agentapi.ItemUser})
			c.EmitTurn(agentapi.TurnCompleted, "")
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("read did not start")
			}
			m.mu.Lock()
			s := m.sessions[sum.ID]
			m.mu.Unlock()
			tc.invalidate(m, s, c)
			finishTurnChangeCapture(t, m, func() { close(release) })
			m.mu.Lock()
			got := s.turnTimings[0].Changes
			m.mu.Unlock()
			if got == nil || got.Status != "unknown" {
				t.Fatalf("stale native facts=%+v", got)
			}
		})
	}
}

func TestTurnChangesInFlightRejectsBlockedNextSendBeforeSDKEvents(t *testing.T) {
	m, p, _ := newTestManager(t)
	sum, c := createSession(t, m, p)
	entered, release := make(chan struct{}), make(chan struct{})
	ownTurnChangeReader(m, sum.ID, c, func(ctx context.Context, _ string) (agentapi.NativeTurnChanges, error) {
		close(entered)
		select {
		case <-release:
			return nativeTurnFacts("native-owner", 4), nil
		case <-ctx.Done():
			return agentapi.NativeTurnChanges{}, ctx.Err()
		}
	})
	c.EmitTurn(agentapi.TurnWorking, "")
	c.EmitItem(agentapi.Item{ID: "owner", Kind: agentapi.ItemUser})
	c.EmitTurn(agentapi.TurnCompleted, "")
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("capture did not start")
	}
	m.mu.Lock()
	s := m.sessions[sum.ID]
	seq, revision, gen, history := s.turnSeq, s.nativeDiffRevision, s.gen, s.historyGen
	m.mu.Unlock()
	sendEntered, sendRelease := make(chan struct{}), make(chan struct{})
	c.SetSendHook(func(ctx context.Context, _ string) error {
		close(sendEntered)
		select {
		case <-sendRelease:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	requestID := mustUUID(t)
	sendDone := make(chan error, 1)
	defer func() {
		close(sendRelease)
		select {
		case err := <-sendDone:
			if err != nil {
				t.Errorf("next Send: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("next Send did not finish after release")
		}
	}()
	go func() {
		_, err := m.Submit(sum.ID, PromptRequest{Text: "next", RequestID: requestID})
		sendDone <- err
	}()
	select {
	case <-sendEntered:
	case <-time.After(time.Second):
		t.Fatal("next Send did not start")
	}
	m.mu.Lock()
	if s.turnSeq == seq || s.base != StateWorking || s.activeTiming != -1 || s.nativeDiffRevision != revision || s.gen != gen || s.historyGen != history || len(s.turnTimings) != 1 || s.turnTimings[0].UserItemID != "owner" {
		m.mu.Unlock()
		t.Fatal("fixture did not preserve the pre-SDK-event submission gap")
	}
	m.mu.Unlock()
	finishTurnChangeCapture(t, m, func() { close(release) })
	m.mu.Lock()
	got := *s.turnTimings[0].Changes
	m.mu.Unlock()
	if got.Status != "unknown" {
		t.Fatalf("old native capture published during next Send: %+v", got)
	}
	// The assertion above runs while Send is blocked, before any event or
	// teardown can independently invalidate the capture.
	select {
	case err := <-sendDone:
		t.Fatalf("Send unexpectedly finished: %v", err)
	default:
	}
}

func TestTurnChangesBoundsUnknownAndHTTPBinding(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	sum, c := createSession(t, ts.m, ts.prov)
	ownTurnChangeReader(ts.m, sum.ID, c, func(context.Context, string) (agentapi.NativeTurnChanges, error) {
		return nativeTurnFacts("native", 3), nil
	})
	c.EmitTurn(agentapi.TurnWorking, "")
	c.EmitItem(agentapi.Item{ID: "owner", Kind: agentapi.ItemUser})
	c.EmitTurn(agentapi.TurnCancelled, "")
	timing := waitTurnChanges(t, ts.m, sum.ID, "available")
	path := "/api/sessions/" + sum.ID + "/turns/" + timing.ID + "/changes"
	unauth := ts.do(http.MethodGet, path, "")
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("auth=%d", unauth.Code)
	}
	w := ts.do(http.MethodGet, path, "", withCookie(ts))
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("route=%d %s", w.Code, w.Body)
	}
	var rec TurnChanges
	if err := json.Unmarshal(w.Body.Bytes(), &rec); err != nil || rec.Counts.Additions != 3 {
		t.Fatalf("route facts=%+v,%v", rec, err)
	}
	if strings.Contains(w.Body.String(), "patch") {
		t.Fatal("snapshot leaked patch")
	}
	other, _ := createSession(t, ts.m, ts.prov)
	wrong := ts.do(http.MethodGet, "/api/sessions/"+other.ID+"/turns/"+timing.ID+"/changes", "", withCookie(ts))
	if wrong.Code != http.StatusNotFound {
		t.Fatalf("cross Task=%d", wrong.Code)
	}
	f := nativeTurnFacts("native", 3)
	f.Entries[0].Path = strings.Repeat("a", agentapi.MaxTurnChangePathBytes+1)
	if got := checkedTurnChanges(timing, f); got.Counts.Status != "unknown" || len(got.Files) != 0 {
		t.Fatalf("oversized=%+v", got)
	}
	for _, status := range []string{"unknown", "busy", "unsupported"} {
		f := agentapi.NativeTurnChanges{Status: status, Files: 999}
		got := checkedTurnChanges(timing, f)
		if got.Counts.Status != status || got.Counts.Files != 0 {
			t.Fatalf("unavailable=%+v", got)
		}
	}
	clone := store.CloneTurnTimings([]TurnTiming{timing})
	if reflect.ValueOf(clone[0].Changes).Pointer() == reflect.ValueOf(timing.Changes).Pointer() {
		t.Fatal("snapshot pointer aliases")
	}
}

func TestTurnChangesEmptyDetailPreservesTimingArray(t *testing.T) {
	m, p, _ := newTestManager(t)
	sum, _ := createSession(t, m, p)
	encoded, err := json.Marshal(detail(t, m, sum.ID))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"turn_timings":[]`) {
		t.Fatalf("empty timing array changed: %s", encoded)
	}
}
