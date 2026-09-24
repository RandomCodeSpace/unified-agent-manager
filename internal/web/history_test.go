package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// closedTasks creates one Task per stage in a first run ("" is an active
// Task that failed, so viewing does not open it), stops that run, and
// starts a second one whose provider has each conversation's record.
func closedTasks(t *testing.T, stages ...string) (*Manager, *agenttest.Provider, *store.Store, []SessionSummary) {
	t.Helper()
	st := openTestStore(t)
	prov := agenttest.NewProvider("fake", allCaps)
	m := startManager(t, st, prov)
	project := addProject(t, m, t.TempDir())
	var tasks []SessionSummary
	for _, stage := range stages {
		sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project})
		if err != nil {
			t.Fatal(err)
		}
		switch stage {
		case StageSettled:
			_, err = m.Settle(sum.ID)
		case StageArchived:
			_, err = m.Archive(sum.ID)
		default:
			prov.Last().Emit(agentapi.Event{Kind: agentapi.EventExit, Error: "the CLI died"})
		}
		if err != nil {
			t.Fatal(err)
		}
		tasks = append(tasks, sum)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	prov2 := agenttest.NewProvider("fake", allCaps)
	for i, task := range tasks {
		prov2.SetHistory(task.ConversationID, agentapi.History{
			Items: []agentapi.Item{
				{ID: fmt.Sprintf("u%d", i), Kind: agentapi.ItemUser, Text: "do it"},
				{ID: fmt.Sprintf("a%d", i), Kind: agentapi.ItemAssistant, Text: "done"},
				{ID: "s1", Kind: agentapi.ItemAssistant, Text: "sub", AgentID: "sa1"},
			},
			Subagents: []agentapi.Subagent{{ID: "sa1", Name: "explore", Status: agentapi.SubagentRunning}},
		})
	}
	m2 := startManager(t, st, prov2)
	for i := range tasks {
		tasks[i] = mustSummary(t, m2, tasks[i].ID)
	}
	return m2, prov2, st, tasks
}

func mustSummary(t *testing.T, m *Manager, id string) SessionSummary {
	t.Helper()
	s, err := m.Summary(id)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func waitHistory(t *testing.T, m *Manager, id, want string) SessionDetail {
	t.Helper()
	var d SessionDetail
	waitUntil(t, "history "+want, func() bool {
		d = detail(t, m, id)
		return d.History == want
	})
	return d
}

func TestClosedTasksShowTheirTranscriptReadOnlyAfterRestart(t *testing.T) {
	m, prov, st, tasks := closedTasks(t, StageSettled, StageArchived, "")
	before, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		sub, snapshot, err := m.Subscribe(task.ID)
		if err != nil {
			t.Fatal(err)
		}
		var snap struct {
			Session SessionDetail `json:"session"`
		}
		if err := json.Unmarshal(parseFrame(t, snapshot).data["session"], &snap.Session); err != nil {
			t.Fatal(err)
		}
		if snap.Session.History != HistoryLoading || len(snap.Session.Items) != 0 {
			t.Fatalf("%s snapshot = history %q, %d items", task.Stage, snap.Session.History, len(snap.Session.Items))
		}
		f := frameOf(t, sub, "history")
		var ev historyEvent
		raw, _ := json.Marshal(f.data)
		if err := json.Unmarshal(raw, &ev); err != nil {
			t.Fatal(err)
		}
		if ev.SessionID != task.ID || ev.History != HistoryLoaded || len(ev.Items) != 2 || ev.Items[1].Text != "done" ||
			len(ev.Subagents) != 1 || ev.Subagents[0].Status != agentapi.SubagentCancelled {
			t.Fatalf("history event = %+v", ev)
		}
		m.Unsubscribe(sub)

		d := detail(t, m, task.ID)
		if d.History != HistoryLoaded || len(d.Items) != 2 || d.Stage != task.Stage || d.State != task.State || d.Open || !d.UpdatedAt.Equal(task.UpdatedAt) {
			t.Fatalf("%s detail = %+v", task.Stage, d)
		}
		sa, err := m.Subagent(task.ID, "sa1")
		if err != nil || len(sa.Items) != 1 || sa.Items[0].Text != "sub" {
			t.Fatalf("subagent = %+v, %v", sa, err)
		}
	}
	if len(prov.Opens()) != 0 || len(prov.Reads()) != 3 {
		t.Fatalf("viewing opened %d conversations and read %d", len(prov.Opens()), len(prov.Reads()))
	}
	// Still read-only: nothing is sent, and the settings stay as they are.
	for _, task := range tasks[:2] {
		if _, err := m.Submit(task.ID, PromptRequest{Text: "more", RequestID: mustUUID(t), Mode: ModeSend}); statusOf(err) != http.StatusConflict {
			t.Fatalf("prompt to a %s task = %v", task.Stage, err)
		}
		if _, err := m.SetMode(task.ID, "yolo"); statusOf(err) != http.StatusConflict {
			t.Fatalf("mode change of a %s task = %v", task.Stage, err)
		}
	}
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	after, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	for key, rec := range before.Sessions {
		got := after.Sessions[key]
		was, _ := json.Marshal(rec)
		now, _ := json.Marshal(got)
		if string(was) != string(now) {
			t.Fatalf("viewing changed %s:\n%s\n%s", key, was, now)
		}
	}
}

func TestHistoryLoadsAreSharedAndAtMostTwoRun(t *testing.T) {
	m, prov, _, tasks := closedTasks(t, StageSettled, StageSettled, StageArchived)
	release := make(chan struct{})
	var mu sync.Mutex
	running, peak := 0, 0
	prov.SetReadHook(func(ctx context.Context, req agentapi.ReadRequest) (agentapi.History, error) {
		mu.Lock()
		running++
		peak = max(peak, running)
		mu.Unlock()
		defer func() { mu.Lock(); running--; mu.Unlock() }()
		select {
		case <-release:
		case <-ctx.Done():
			return agentapi.History{}, ctx.Err()
		}
		return agentapi.History{Items: []agentapi.Item{{ID: "u", Kind: agentapi.ItemUser, Text: req.ConversationID}}}, nil
	})
	for range 3 {
		for _, task := range tasks {
			if d := detail(t, m, task.ID); d.History != HistoryLoading {
				t.Fatalf("detail = %q, want loading at once", d.History)
			}
		}
	}
	waitUntil(t, "two reads", func() bool { return len(prov.Reads()) == 2 })
	time.Sleep(50 * time.Millisecond)
	if n := len(prov.Reads()); n != 2 {
		t.Fatalf("%d reads started, want 2 while both slots are busy", n)
	}
	close(release)
	for _, task := range tasks {
		if d := waitHistory(t, m, task.ID, HistoryLoaded); len(d.Items) != 1 || d.Items[0].Text != task.ConversationID {
			t.Fatalf("items = %+v", d.Items)
		}
	}
	if n := len(prov.Reads()); n != 3 || peak != 2 {
		t.Fatalf("reads = %d, peak = %d; want one per task and at most two at once", n, peak)
	}
	if reads := prov.Reads(); reads[0].Workdir == "" || !slices.ContainsFunc(reads, func(r agentapi.ReadRequest) bool { return r.ConversationID == tasks[0].ConversationID }) {
		t.Fatalf("reads = %+v", reads)
	}
}

func TestFailedHistoryReadLeavesTheTaskAsItWas(t *testing.T) {
	m, prov, _, tasks := closedTasks(t, StageSettled)
	task := tasks[0]
	prov.SetReadHook(func(context.Context, agentapi.ReadRequest) (agentapi.History, error) {
		return agentapi.History{}, errors.New("CLI \x1b[31mcrashed")
	})
	detail(t, m, task.ID)
	d := waitHistory(t, m, task.ID, HistoryUnavailable)
	if d.HistoryReason != "could not read the recorded transcript: CLI crashed" || d.Stage != StageSettled || d.State != task.State || len(d.Items) != 0 {
		t.Fatalf("detail = %+v", d)
	}
	// Viewing again soon does not read again; after historyRetry it does.
	detail(t, m, task.ID)
	if n := len(prov.Reads()); n != 1 {
		t.Fatalf("reads = %d", n)
	}
	prov.SetReadHook(nil)
	prov.ForgetConversation(task.ConversationID)
	later := time.Now().Add(historyRetry + time.Second)
	m.mu.Lock()
	m.now = func() time.Time { return later }
	m.mu.Unlock()
	detail(t, m, task.ID)
	waitUntil(t, "second read", func() bool { return len(prov.Reads()) == 2 })
	d = waitHistory(t, m, task.ID, HistoryUnavailable)
	if !strings.Contains(d.HistoryReason, "no longer exists") || d.Stage != StageSettled {
		t.Fatalf("detail = %+v", d)
	}
}

func TestIdleHistoryIsDroppedAndReadAgain(t *testing.T) {
	m, prov, _, tasks := closedTasks(t, StageSettled, StageArchived)
	settled, archived := tasks[0], tasks[1]
	detail(t, m, settled.ID)
	detail(t, m, archived.ID)
	waitHistory(t, m, settled.ID, HistoryLoaded)
	waitHistory(t, m, archived.ID, HistoryLoaded)
	watch, _, err := m.Subscribe(archived.ID)
	if err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(historyIdle + time.Second)
	m.mu.Lock()
	m.now = func() time.Time { return later }
	m.mu.Unlock()
	m.evictHistories()
	m.mu.Lock()
	s, a := m.sessions[settled.ID], m.sessions[archived.ID]
	settledGone, archivedKept := len(s.items) == 0 && len(s.subagents) == 0 && s.history == "", len(a.items) == 3
	m.mu.Unlock()
	if !settledGone || !archivedKept {
		t.Fatalf("after idle: settled dropped %v, watched archived kept %v", settledGone, archivedKept)
	}
	m.Unsubscribe(watch)
	if d := detail(t, m, settled.ID); d.History != HistoryLoading {
		t.Fatalf("view after eviction = %q", d.History)
	}
	waitHistory(t, m, settled.ID, HistoryLoaded)
	if n := len(prov.Reads()); n != 3 {
		t.Fatalf("reads = %d, want the dropped transcript read again", n)
	}
}

func TestOpenTranscriptsAreNotDropped(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	conv.EmitItem(agentapi.Item{ID: "a1", Kind: agentapi.ItemAssistant, Text: "hi"})
	if _, err := m.Settle(sum.ID); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(historyIdle + time.Hour)
	m.mu.Lock()
	m.now = func() time.Time { return later }
	m.mu.Unlock()
	m.evictHistories()
	if d := detail(t, m, sum.ID); d.History != HistoryLoaded || len(d.Items) != 1 || len(prov.Reads()) != 0 {
		t.Fatalf("detail = %+v, reads %d", d, len(prov.Reads()))
	}
}

func TestHistoryFrameStaysBelowSubscriberLimit(t *testing.T) {
	m, prov, _, tasks := closedTasks(t, StageArchived)
	h := agentapi.History{}
	for i := range 10 {
		h.Items = append(h.Items, agentapi.Item{ID: fmt.Sprint(i), Kind: agentapi.ItemAssistant, Text: strings.Repeat("<", 1<<20)})
	}
	unbounded, err := encodeFrame("history", historyEvent{Items: h.Items})
	if err != nil || len(unbounded) <= subscriberBytes {
		t.Fatalf("fixture frame size = %d, %v", len(unbounded), err)
	}
	prov.SetHistory(tasks[0].ConversationID, h)
	sub, _, err := m.Subscribe(tasks[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Unsubscribe(sub) })
	f := frameOf(t, sub, "history")
	frame, err := json.Marshal(f.data)
	if err != nil {
		t.Fatal(err)
	}
	var event historyEvent
	if err := json.Unmarshal(frame, &event); err != nil {
		t.Fatal(err)
	}
	if len(frame) >= subscriberBytes || !event.Truncated || len(event.Items) == 0 || event.Items[len(event.Items)-1].ID != "9" {
		t.Fatalf("history frame = %d bytes, truncated %v, items %d", len(frame), event.Truncated, len(event.Items))
	}
	d := detail(t, m, tasks[0].ID)
	if d.Seq < event.Seq || d.Seq == 0 {
		t.Fatalf("detail seq %d before history %d", d.Seq, event.Seq)
	}
}

func TestHistoryBudgetEvictsLeastRecentlyViewed(t *testing.T) {
	m, prov, _, tasks := closedTasks(t, StageArchived, StageArchived, StageArchived, StageArchived, StageArchived, StageArchived)
	h := agentapi.History{}
	for i := range 3 {
		h.Items = append(h.Items, agentapi.Item{ID: fmt.Sprint(i), Kind: agentapi.ItemAssistant, Text: strings.Repeat("x", 4<<20)})
	}
	for _, task := range tasks {
		prov.SetHistory(task.ConversationID, h)
	}
	for _, task := range tasks[:5] {
		waitHistory(t, m, task.ID, HistoryLoaded)
	}
	// Refresh the oldest: the second Task is now the eviction candidate.
	_ = detail(t, m, tasks[0].ID)
	waitHistory(t, m, tasks[5].ID, HistoryLoaded)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[tasks[0].ID].history != HistoryLoaded || m.sessions[tasks[1].ID].history != "" {
		t.Fatal("budget did not evict the least recently viewed history")
	}
	total := 0
	for _, s := range m.sessions {
		if s.historyRead {
			total += s.historyBytes
		}
	}
	if total > maxCachedHistoryBytes {
		t.Fatalf("cached %d bytes", total)
	}
}

func TestDeleteCancelsHistoryLoad(t *testing.T) {
	m, prov, _, tasks := closedTasks(t, StageArchived)
	started, cancelled := make(chan struct{}), make(chan struct{})
	prov.SetReadHook(func(ctx context.Context, _ agentapi.ReadRequest) (agentapi.History, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return agentapi.History{}, ctx.Err()
	})
	_ = detail(t, m, tasks[0].ID)
	<-started
	if err := m.Delete(tasks[0].ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("deleted Task kept reading history")
	}
}

func TestSendCancelsReadWithoutOverwritingLiveHistory(t *testing.T) {
	m, prov, _, tasks := closedTasks(t, "")
	started, cancelled := make(chan struct{}), make(chan struct{})
	prov.SetReadHook(func(ctx context.Context, _ agentapi.ReadRequest) (agentapi.History, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return agentapi.History{Items: []agentapi.Item{{ID: "stale", Text: "stale"}}}, nil
	})
	_ = detail(t, m, tasks[0].ID)
	<-started
	if sub, err := m.Submit(tasks[0].ID, PromptRequest{Text: "go", RequestID: mustUUID(t)}); err != nil || sub.Status != SubmissionAccepted {
		t.Fatalf("send = %+v, %v", sub, err)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("send left read running")
	}
	if d := detail(t, m, tasks[0].ID); d.History != HistoryLoaded || slices.ContainsFunc(d.Items, func(it agentapi.Item) bool { return it.ID == "stale" }) {
		t.Fatalf("detail after send = %+v", d)
	}
}
