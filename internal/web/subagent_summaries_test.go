package web

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

type summaryProvider struct {
	*agenttest.Provider
	mu       sync.Mutex
	requests []agentapi.SubagentSummaryRequest
	hook     func(context.Context, agentapi.SubagentSummaryRequest) (string, error)
}

func (p *summaryProvider) SummarizeSubagent(ctx context.Context, req agentapi.SubagentSummaryRequest) (string, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	hook := p.hook
	p.mu.Unlock()
	if hook != nil {
		return hook(ctx, req)
	}
	return "Completed the requested check.", nil
}
func (p *summaryProvider) calls() []agentapi.SubagentSummaryRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]agentapi.SubagentSummaryRequest(nil), p.requests...)
}
func summaryManager(t *testing.T) (*Manager, *summaryProvider, *store.Store) {
	t.Helper()
	p := &summaryProvider{Provider: agenttest.NewProvider("fake", titleCaps)}
	p.SetModels(selectionModels(), nil)
	st := openTestStore(t)
	m := startManager(t, st, p)
	if _, err := m.UpdateSettings(SettingsPatch{TitleModel: map[string]string{"fake": "a"}}); err != nil {
		t.Fatal(err)
	}
	return m, p, st
}
func emitSummaryChild(c *agenttest.Conversation, id string, status agentapi.SubagentStatus) {
	c.EmitSubagent(agentapi.Subagent{ID: id, ParentToolCallID: "task-" + id, Description: "Check the implementation", Status: status})
}
func emitSummaryResult(c *agenttest.Conversation, id, text string) {
	c.EmitItem(agentapi.Item{ID: "task-" + id, Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "task", Status: agentapi.ToolCompleted, Output: text}})
}
func generatedSummary(t *testing.T, m *Manager, session, child string) string {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[session]
	if s == nil || s.subIdx[child] == nil {
		return ""
	}
	return s.compactSubagent(*s.subIdx[child]).Summary
}

func TestSubagentSummaryCompletesOnceInEitherEventOrder(t *testing.T) {
	for _, resultFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(resultFirst), func(t *testing.T) {
			m, p, _ := summaryManager(t)
			sum, c := createSession(t, m, p.Provider)
			emitSummaryChild(c, "child", agentapi.SubagentRunning)
			c.EmitItem(agentapi.Item{ID: "interim", AgentID: "child", Kind: agentapi.ItemAssistant, Text: "I will inspect it."})
			c.EmitDelta("task-child", agentapi.ItemAssistant, "a partial result")
			if resultFirst {
				emitSummaryResult(c, "child", "Implemented the cache bound and checked it.")
			}
			emitSummaryChild(c, "child", agentapi.SubagentCompleted)
			if !resultFirst {
				if len(p.calls()) != 0 {
					t.Fatal("summarized an interim message before the final parent result")
				}
				emitSummaryResult(c, "child", "Implemented the cache bound and checked it.")
			}
			waitUntil(t, "generated child summary", func() bool { return generatedSummary(t, m, sum.ID, "child") != "" })
			emitSummaryChild(c, "child", agentapi.SubagentIdle)
			emitSummaryChild(c, "child", agentapi.SubagentCompleted)
			emitSummaryResult(c, "child", "Implemented the cache bound and checked it.")
			if err := m.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
			calls := p.calls()
			if len(calls) != 1 || calls[0].Model != "a" || calls[0].Workdir != sum.Workdir || calls[0].Result != "Implemented the cache bound and checked it." {
				t.Fatalf("requests = %+v", calls)
			}
		})
	}
}

func TestSubagentSummaryFollowupRejectsOldReplyAndUsesNewResult(t *testing.T) {
	m, p, _ := summaryManager(t)
	started, cancelled := make(chan struct{}), make(chan struct{})
	p.hook = func(ctx context.Context, req agentapi.SubagentSummaryRequest) (string, error) {
		if req.Result == "Initial report" {
			close(started)
			<-ctx.Done()
			close(cancelled)
			return "Stale line", nil
		}
		return "Checked the follow-up.", nil
	}
	sum, c := createSession(t, m, p.Provider)
	emitSummaryChild(c, "child", agentapi.SubagentRunning)
	emitSummaryResult(c, "child", "Initial report")
	emitSummaryChild(c, "child", agentapi.SubagentIdle)
	<-started
	emitSummaryChild(c, "child", agentapi.SubagentRunning)
	<-cancelled
	// Neither the old parent report nor a streamed child prefix is this result.
	c.Emit(agentapi.Event{Kind: agentapi.EventDelta, Delta: &agentapi.Delta{AgentID: "child", ItemID: "followup", Kind: agentapi.ItemAssistant, Text: "partial"}})
	emitSummaryChild(c, "child", agentapi.SubagentIdle)
	if got := generatedSummary(t, m, sum.ID, "child"); got != "" {
		t.Fatalf("stale line = %q", got)
	}
	c.EmitItem(agentapi.Item{ID: "followup", AgentID: "child", Kind: agentapi.ItemAssistant, Text: "Follow-up final report"})
	waitUntil(t, "follow-up summary", func() bool { return generatedSummary(t, m, sum.ID, "child") == "Checked the follow-up." })
	calls := p.calls()
	if len(calls) != 2 || calls[1].Result != "Follow-up final report" {
		t.Fatalf("requests = %+v", calls)
	}
	emitSummaryChild(c, "child", agentapi.SubagentRunning)
	if got := generatedSummary(t, m, sum.ID, "child"); got != "" {
		t.Fatalf("running child retained %q", got)
	}
}

func TestSubagentSummaryPersistsWithoutReplayBackfill(t *testing.T) {
	m, p, st := summaryManager(t)
	sum, c := createSession(t, m, p.Provider)
	emitSummaryChild(c, "child", agentapi.SubagentRunning)
	emitSummaryResult(c, "child", "Saved result")
	emitSummaryChild(c, "child", agentapi.SubagentCompleted)
	waitUntil(t, "saved summary", func() bool { return generatedSummary(t, m, sum.ID, "child") != "" })
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	restored := sessionFromRecord(cfg.Sessions[store.Key("fake", sum.ID)])
	history := agentapi.History{Subagents: []agentapi.Subagent{{ID: "child", ParentToolCallID: "task-child", Status: agentapi.SubagentCompleted}}, Items: []agentapi.Item{{ID: "task-child", Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "task", Status: agentapi.ToolCompleted, Output: "Saved result"}}}}
	m.mu.Lock()
	m.applyHistoryLocked(restored, history, false)
	got := restored.compactSubagent(*restored.subIdx["child"]).Summary
	m.dropHistoryLocked(restored)
	m.applyHistoryLocked(restored, history, false)
	afterEviction := restored.compactSubagent(*restored.subIdx["child"]).Summary
	history.Items = append(history.Items, agentapi.Item{ID: "newer-answer", AgentID: "child", Kind: agentapi.ItemAssistant, Text: "A follow-up answer"})
	m.applyHistoryLocked(restored, history, false)
	newer := restored.compactSubagent(*restored.subIdx["child"]).Summary
	m.dropHistoryLocked(restored)
	history.Items = history.Items[:1]
	history.Items[0].Tool.Output = "A different result"
	m.applyHistoryLocked(restored, history, false)
	stale := restored.compactSubagent(*restored.subIdx["child"]).Summary
	m.mu.Unlock()
	if got == "" || afterEviction != got || stale != "" || newer != "" {
		t.Fatalf("restored %q, after eviction %q, stale %q", got, afterEviction, stale)
	}
	if len(p.calls()) != 1 {
		t.Fatal("history replay generated a summary")
	}
}

func TestSubagentSummaryDisabledAndFailedCallsKeepFallback(t *testing.T) {
	for _, mode := range []string{"none", "unpriced", "error", "timeout", "empty", "failed", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			m, p, _ := summaryManager(t)
			p.hook = func(ctx context.Context, _ agentapi.SubagentSummaryRequest) (string, error) {
				if _, ok := ctx.Deadline(); !ok {
					t.Error("utility job has no deadline")
				}
				switch mode {
				case "error":
					return "", errors.New("unavailable")
				case "timeout":
					return "", context.DeadlineExceeded
				case "empty":
					return " <think>hidden</think> ", nil
				}
				return "unexpected", nil
			}
			if mode == "none" {
				if _, err := m.UpdateSettings(SettingsPatch{TitleModel: map[string]string{"fake": store.WebTitleModelNone}}); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "unpriced" {
				m.mu.Lock()
				m.settings.TitleModel = nil
				info := m.infos["fake"]
				info.Models = nil
				m.infos["fake"] = info
				m.mu.Unlock()
			}
			sum, c := createSession(t, m, p.Provider)
			emitSummaryChild(c, "child", agentapi.SubagentRunning)
			emitSummaryResult(c, "child", "Provider report")
			status := agentapi.SubagentCompleted
			if mode == "failed" {
				status = agentapi.SubagentFailed
			}
			if mode == "cancelled" {
				status = agentapi.SubagentCancelled
			}
			emitSummaryChild(c, "child", status)
			shouldCall := mode == "error" || mode == "timeout" || mode == "empty"
			if shouldCall {
				waitUntil(t, "utility attempt", func() bool { return len(p.calls()) == 1 })
			}
			if err := m.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
			if (len(p.calls()) == 1) != shouldCall {
				t.Fatalf("calls=%d", len(p.calls()))
			}
			m.mu.Lock()
			compact := m.sessions[sum.ID].compactSubagent(*m.sessions[sum.ID].subIdx["child"])
			m.mu.Unlock()
			if compact.Summary != "" || compact.ResultSummary != "Provider report" {
				t.Fatalf("fallback = %+v", compact)
			}
		})
	}
}

func TestSubagentSummaryUsesCheapestAndBoundsText(t *testing.T) {
	m, p, _ := summaryManager(t)
	m.mu.Lock()
	m.settings.TitleModel = nil
	info := m.infos["fake"]
	info.Models = []agentapi.Model{pricedModel("cheap", 1, 1, 1e6), pricedModel("costly", 5, 5, 1e6)}
	m.infos["fake"] = info
	wantModel := m.utilityModelLocked("fake")
	m.mu.Unlock()
	if wantModel == "" {
		t.Fatal("fixture has no priced model")
	}
	p.hook = func(context.Context, agentapi.SubagentSummaryRequest) (string, error) {
		return "\n<think>hidden</think>\n" + strings.Repeat("界", 500) + "\nsecond line", nil
	}
	sum, c := createSession(t, m, p.Provider)
	c.EmitSubagent(agentapi.Subagent{ID: "child", ParentToolCallID: "task-child", Status: agentapi.SubagentRunning, Description: strings.Repeat("界", 1000)})
	emitSummaryResult(c, "child", strings.Repeat("界", 10000))
	c.EmitSubagent(agentapi.Subagent{ID: "child", Status: agentapi.SubagentCompleted})
	waitUntil(t, "bounded summary", func() bool { return generatedSummary(t, m, sum.ID, "child") != "" })
	req := p.calls()[0]
	line := generatedSummary(t, m, sum.ID, "child")
	if req.Model != wantModel || utf8.RuneCountInString(req.Result) > maxSummaryInputRunes || utf8.RuneCountInString(req.Description) > 256 || utf8.RuneCountInString(line) > maxSummaryRunes || len(line) > maxSummaryBytes || strings.Contains(line, "\n") {
		t.Fatalf("model=%q input=%d description=%d line=%d/%d", req.Model, utf8.RuneCountInString(req.Result), utf8.RuneCountInString(req.Description), utf8.RuneCountInString(line), len(line))
	}
}

func TestSubagentSummaryQueueIsBoundedAndShutdownCancels(t *testing.T) {
	m, p, _ := summaryManager(t)
	// Both title slots are occupied: the summary workers wait without making
	// a provider call, and only the fixed queue can accept more work.
	for range maxTitleJobs {
		m.titleSlots <- struct{}{}
	}
	for range 2 {
		_, c := createSession(t, m, p.Provider)
		for i := range 110 {
			id := fmt.Sprint(i)
			emitSummaryChild(c, id, agentapi.SubagentRunning)
			emitSummaryResult(c, id, "Result")
			emitSummaryChild(c, id, agentapi.SubagentCompleted)
		}
	}
	if n := len(m.summaryJobs); n != maxSubagents {
		t.Fatalf("pending summaries=%d, want %d", n, maxSubagents)
	}
	if len(p.calls()) != 0 {
		t.Fatal("summary bypassed shared title slots")
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(m.summaryJobs) != 0 || len(p.calls()) != 0 {
		t.Fatal("shutdown retained queued work or ran inference")
	}
	for range maxTitleJobs {
		<-m.titleSlots
	}
}

func TestSubagentSummaryDeletionCancelsAndCannotRecreateRecord(t *testing.T) {
	m, p, st := summaryManager(t)
	started, cancelled := make(chan struct{}), make(chan struct{})
	p.hook = func(ctx context.Context, _ agentapi.SubagentSummaryRequest) (string, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return "Late result", nil
	}
	sum, c := createSession(t, m, p.Provider)
	emitSummaryChild(c, "child", agentapi.SubagentRunning)
	emitSummaryResult(c, "child", "Result")
	emitSummaryChild(c, "child", agentapi.SubagentCompleted)
	<-started
	if _, err := m.Archive(sum.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(sum.ID); err != nil {
		t.Fatal(err)
	}
	<-cancelled
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Sessions[store.Key("fake", sum.ID)]; ok {
		t.Fatal("late summary recreated deleted record")
	}
}

func TestSubagentSummaryPublishesOnExistingCompactStream(t *testing.T) {
	m, p, _ := summaryManager(t)
	started, release := make(chan struct{}), make(chan struct{})
	p.hook = func(ctx context.Context, _ agentapi.SubagentSummaryRequest) (string, error) {
		close(started)
		select {
		case <-release:
			return "Checked the implementation.", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	sum, c := createSession(t, m, p.Provider)
	emitSummaryChild(c, "child", agentapi.SubagentRunning)
	emitSummaryResult(c, "child", "Provider result")
	emitSummaryChild(c, "child", agentapi.SubagentCompleted)
	<-started
	sub, _, err := m.subscribeView(sum.ID, true, true, true)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Unsubscribe(sub)
	close(release)
	frame := frameOf(t, sub, "subagent")
	var child compactSubagent
	decodeField(t, frame, "subagent", &child)
	if child.Summary != "Checked the implementation." || child.ResultSummary != "Provider result" {
		t.Fatalf("compact event=%+v", child)
	}
}

func TestSubagentSummarySavedMetadataIsBounded(t *testing.T) {
	s := newSession("session", "fake", "", "", "", time.Time{})
	records := make([]store.SubagentSummary, maxSubagents+10)
	for i := range records {
		records[i] = store.SubagentSummary{AgentID: fmt.Sprint(i), ItemID: "result", Digest: strings.Repeat("a", 64), Text: strings.Repeat("界", 1000)}
	}
	s.loadSubagentSummaries(records)
	if len(s.subagentSummaries) != maxSubagents {
		t.Fatalf("records=%d", len(s.subagentSummaries))
	}
	for _, record := range s.savedSubagentSummaries() {
		if len(record.Text) > maxSummaryBytes || utf8.RuneCountInString(record.Text) > maxSummaryRunes {
			t.Fatal("saved summary exceeded its bound")
		}
	}
}

func TestSubagentSummaryRejectsClippedReasoning(t *testing.T) {
	reply := "<think>" + strings.Repeat("private reasoning ", maxSummaryInputRunes) + "</think>\nVerified the result."
	if got := cleanGeneratedSummary(reply); got != "" {
		t.Fatalf("clipped reasoning became summary: %.40q", got)
	}
	if got := cleanGeneratedSummary("<think>brief reasoning</think>\nVerified the result."); got != "Verified the result." {
		t.Fatalf("complete block cleanup = %q", got)
	}
}

// No workers run in this fixture: tests control exactly when a queued job
// reaches a utility slot, without a debounce or scheduler timing assumption.
func queuedSummaryFixture(t *testing.T, followup bool) (*Manager, *summaryProvider, *webSession, func(agentapi.Event)) {
	t.Helper()
	p := &summaryProvider{Provider: agenttest.NewProvider("fake", titleCaps)}
	m := NewManager(openTestStore(t), []agentapi.Provider{p})
	t.Cleanup(m.cancel)
	m.infos["fake"] = ProviderInfo{Available: true}
	m.settings.TitleModel = map[string]string{"fake": "a"}
	s := newSession("session", "fake", "", "", "", time.Time{})
	m.sessions[s.id] = s
	emit := func(ev agentapi.Event) { m.handleEvent(s, s.gen, ev) }
	statuses := []agentapi.SubagentStatus{agentapi.SubagentRunning}
	if followup {
		statuses = append(statuses, agentapi.SubagentIdle, agentapi.SubagentRunning)
	}
	for _, status := range statuses {
		emit(agentapi.Event{Kind: agentapi.EventSubagent, Subagent: &agentapi.Subagent{ID: "child", ParentToolCallID: "task-child", Status: status}})
	}
	return m, p, s, emit
}

func takeSummaryJob(t *testing.T, m *Manager) subagentSummaryJob {
	t.Helper()
	select {
	case job := <-m.summaryJobs:
		return job
	default:
		t.Fatal("no candidate job queued")
		return subagentSummaryJob{}
	}
}

func TestSubagentSummaryReplacesCandidateBeforeStart(t *testing.T) {
	m, p, s, emit := queuedSummaryFixture(t, true)
	emit(agentapi.Event{Kind: agentapi.EventItem, Item: &agentapi.Item{ID: "interim", AgentID: "child", Kind: agentapi.ItemAssistant, Text: "I will inspect the change."}})
	emit(agentapi.Event{Kind: agentapi.EventSubagent, Subagent: &agentapi.Subagent{ID: "child", Status: agentapi.SubagentIdle}})
	old := takeSummaryJob(t, m)
	final := agentapi.Event{Kind: agentapi.EventItem, Item: &agentapi.Item{ID: "final", AgentID: "child", Kind: agentapi.ItemAssistant, Text: "The final complete result."}}
	emit(final)
	replacement := takeSummaryJob(t, m)
	// The old queued job starts after replacement. Its cleanup must not call
	// the run's latest cancel and thereby cancel that replacement.
	m.generateSubagentSummary(old)
	if len(p.calls()) != 0 {
		t.Fatal("obsolete queued candidate started inference")
	}
	if replacement.ctx.Err() != nil {
		t.Fatal("obsolete job cleanup cancelled the replacement")
	}
	m.generateSubagentSummary(replacement)
	if got := generatedSummary(t, m, s.id, "child"); got == "" {
		t.Fatal("final complete result has no summary")
	}
	emit(final)
	emit(agentapi.Event{Kind: agentapi.EventItem, Item: &agentapi.Item{ID: "interim", AgentID: "child", Kind: agentapi.ItemAssistant, Text: "I will inspect the change."}})
	emit(agentapi.Event{Kind: agentapi.EventSubagent, Subagent: &agentapi.Subagent{ID: "child", Status: agentapi.SubagentIdle}})
	if len(m.summaryJobs) != 0 || len(p.calls()) != 1 || p.calls()[0].Result != "The final complete result." {
		t.Fatalf("duplicate/candidate calls = %+v", p.calls())
	}
	// Replacing this already-published result at the cap must keep every
	// unrelated child's record; it needs no additional storage slot.
	for i := range maxSubagents - 1 {
		id := fmt.Sprint(i)
		s.subagentSummaries[id] = store.SubagentSummary{AgentID: id, Text: "Saved"}
	}
	emit(agentapi.Event{Kind: agentapi.EventItem, Item: &agentapi.Item{ID: "final-v2", AgentID: "child", Kind: agentapi.ItemAssistant, Text: "A newer complete result."}})
	m.generateSubagentSummary(takeSummaryJob(t, m))
	if len(s.subagentSummaries) != maxSubagents || len(p.calls()) != 2 {
		t.Fatal("replacement evicted unrelated metadata or lost its attempt")
	}
}

func TestSubagentSummaryChecksSourceBeforeInference(t *testing.T) {
	m, p, s, emit := queuedSummaryFixture(t, true)
	emit(agentapi.Event{Kind: agentapi.EventItem, Item: &agentapi.Item{ID: "answer", AgentID: "child", Kind: agentapi.ItemAssistant, Text: "Earlier complete result"}})
	emit(agentapi.Event{Kind: agentapi.EventSubagent, Subagent: &agentapi.Subagent{ID: "child", Status: agentapi.SubagentIdle}})
	job := takeSummaryJob(t, m)
	// A history refresh can replace source data without the live event hook
	// cancelling its job. The provider must not see the superseded source.
	m.mu.Lock()
	m.upsertItemLocked(s, agentapi.Item{ID: "answer", AgentID: "child", Kind: agentapi.ItemAssistant, Text: "Changed result"}, false)
	m.mu.Unlock()
	if job.ctx.Err() != nil {
		t.Fatal("fixture unexpectedly cancelled the job")
	}
	m.generateSubagentSummary(job)
	if len(p.calls()) != 0 {
		t.Fatal("changed source was checked only after inference")
	}
}

func TestSubagentSummaryReplacesCandidateDuringInference(t *testing.T) {
	m, p, _ := summaryManager(t)
	started := make(chan struct{})
	p.hook = func(ctx context.Context, req agentapi.SubagentSummaryRequest) (string, error) {
		if req.Result == "I will inspect the change." {
			close(started)
			<-ctx.Done()
			return "Obsolete summary", nil
		}
		return "Verified the final result.", nil
	}
	sum, c := createSession(t, m, p.Provider)
	for _, status := range []agentapi.SubagentStatus{agentapi.SubagentRunning, agentapi.SubagentIdle, agentapi.SubagentRunning} {
		emitSummaryChild(c, "child", status)
	}
	c.EmitItem(agentapi.Item{ID: "interim", AgentID: "child", Kind: agentapi.ItemAssistant, Text: "I will inspect the change."})
	emitSummaryChild(c, "child", agentapi.SubagentIdle)
	<-started
	final := agentapi.Item{ID: "final", AgentID: "child", Kind: agentapi.ItemAssistant, Text: "The final complete result."}
	c.EmitItem(final)
	waitUntil(t, "replacement summary after a started interim call", func() bool { return generatedSummary(t, m, sum.ID, "child") == "Verified the final result." })
	c.EmitItem(final)
	emitSummaryChild(c, "child", agentapi.SubagentIdle)
	if calls := p.calls(); len(calls) != 2 || calls[1].Result != "The final complete result." {
		t.Fatalf("replacement calls = %+v", calls)
	}
}

func TestSubagentSummaryInitialLateChildKeepsAuthoritativeParent(t *testing.T) {
	for _, pending := range []bool{true, false} {
		t.Run(fmt.Sprint(pending), func(t *testing.T) {
			m, p, s, emit := queuedSummaryFixture(t, false)
			emit(agentapi.Event{Kind: agentapi.EventItem, Item: &agentapi.Item{ID: "task-child", Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "task", Status: agentapi.ToolCompleted, Output: "Authoritative parent result"}}})
			emit(agentapi.Event{Kind: agentapi.EventSubagent, Subagent: &agentapi.Subagent{ID: "child", Status: agentapi.SubagentIdle}})
			job := takeSummaryJob(t, m)
			if !pending {
				m.generateSubagentSummary(job)
			}
			revision := s.summaryRevision
			late := agentapi.Event{Kind: agentapi.EventItem, Item: &agentapi.Item{ID: "late-child", AgentID: "child", Kind: agentapi.ItemAssistant, Text: "The child's delayed complete answer"}}
			emit(late)
			if pending {
				m.generateSubagentSummary(job)
			}
			if got := generatedSummary(t, m, s.id, "child"); got == "" {
				t.Fatal("late child hid the authoritative parent-result summary")
			}
			if calls := p.calls(); len(calls) != 1 || calls[0].Result != "Authoritative parent result" || len(m.summaryJobs) != 0 {
				t.Fatalf("unchanged parent result regenerated: %+v", calls)
			}
			record := s.subagentSummaries["child"]
			if record.LastAssistantID != "late-child" || s.summaryRevision <= revision {
				t.Fatal("refreshed metadata was not saved/marked dirty")
			}
			revision = s.summaryRevision
			emit(late)
			if s.summaryRevision != revision || len(m.summaryJobs) != 0 {
				t.Fatal("duplicate item changed saved metadata or queued inference")
			}
			// History alone cannot repair the identity of a saved result.
			m.mu.Lock()
			m.upsertItemLocked(s, agentapi.Item{ID: "replayed-newer", AgentID: "child", Kind: agentapi.ItemAssistant, Text: "A newer historical answer"}, false)
			m.mu.Unlock()
			if got := generatedSummary(t, m, s.id, "child"); got != "" || s.subagentSummaries["child"] != record {
				t.Fatal("replay silently repaired a saved identity")
			}
			emit(agentapi.Event{Kind: agentapi.EventSubagent, Subagent: &agentapi.Subagent{ID: "child", Status: agentapi.SubagentRunning}})
			if _, ok := s.subagentSummaries["child"]; ok {
				t.Fatal("follow-up retained the previous saved line")
			}
		})
	}
}

func TestSubagentSummaryInitialPendingIgnoresOlderRepeatedChild(t *testing.T) {
	m, p, s, emit := queuedSummaryFixture(t, false)
	interim := agentapi.Event{Kind: agentapi.EventItem, Item: &agentapi.Item{ID: "interim", AgentID: "child", Kind: agentapi.ItemAssistant, Text: "I will inspect the change."}}
	emit(interim)
	emit(agentapi.Event{Kind: agentapi.EventItem, Item: &agentapi.Item{ID: "task-child", Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "task", Status: agentapi.ToolCompleted, Output: "Authoritative parent result"}}})
	emit(agentapi.Event{Kind: agentapi.EventSubagent, Subagent: &agentapi.Subagent{ID: "child", Status: agentapi.SubagentIdle}})
	job := takeSummaryJob(t, m)
	emit(agentapi.Event{Kind: agentapi.EventItem, Item: &agentapi.Item{ID: "final", AgentID: "child", Kind: agentapi.ItemAssistant, Text: "The completed child answer."}})
	emit(interim)
	m.generateSubagentSummary(job)
	if got := generatedSummary(t, m, s.id, "child"); got == "" {
		t.Fatal("older repeated child displaced the pending final identity")
	}
	if record := s.subagentSummaries["child"]; record.LastAssistantID != "final" {
		t.Fatalf("saved identity = %q", record.LastAssistantID)
	}
	if calls := p.calls(); len(calls) != 1 || calls[0].Result != "Authoritative parent result" || len(m.summaryJobs) != 0 {
		t.Fatalf("parent summary attempts = %+v", calls)
	}
}
