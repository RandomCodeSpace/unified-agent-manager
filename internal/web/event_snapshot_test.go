package web

import (
	"bytes"
	"math"
	"sync"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func snapshotFixture(t *testing.T) (*Manager, string, func()) {
	t.Helper()
	m, prov, _ := newTestManager(t)
	sum, _ := createSession(t, m, prov)
	m.mu.Lock()
	s := m.sessions[sum.ID]
	price, credits, exit, limit, enabled := 1.0, 2.0, 0, 3, true
	m.settings.TokenPrices = map[string]map[string]store.WebTokenPrice{"fake": {"model": {Input: &price, Output: &price, CacheRead: &price, CacheWrite: &price}}}
	m.settings.HiddenModels = map[string][]string{"fake": {"old"}}
	m.settings.SubagentModels = map[string][]string{"fake": {"old"}}
	m.settings.TitleModel = map[string]string{"fake": "old"}
	m.settings.CustomModels = []CustomModel{{Name: "old"}}
	m.settings.UtilityDailyLimit, m.settings.SuggestReplies, m.settings.CompactionThreshold = &limit, &enabled, &limit
	s.context, s.usage = &agentapi.Context{Used: 1}, &agentapi.Usage{AIUnits: 1}
	s.execution = &agentapi.ExecutionState{Known: true, Objective: &agentapi.AutopilotObjective{Objective: "old", CreditsUsed: &credits, CreditLimit: &credits}}
	m.upsertItemLocked(s, agentapi.Item{ID: "tool", Kind: agentapi.ItemTool,
		Tool: &agentapi.ToolCall{Name: "edit", Status: agentapi.ToolRunning, ExitCode: &exit, Tail: []agentapi.OutputLine{{Text: "old"}}, Declaration: &agentapi.FileDeclaration{ArtifactID: "old", Path: "old"},
			EditEventID: "event", FileEdits: []agentapi.FileEdit{{Path: "/old", Kind: "edit", Additions: 1, DiffStatus: "available"}}},
		Images: []agentapi.Image{{ID: "old"}}, Attachments: []agentapi.Attachment{{ID: "old"}}}, false)
	m.upsertItemLocked(s, agentapi.Item{ID: "receipt", Kind: agentapi.ItemNotice,
		Completion: &agentapi.TaskCompletion{Decision: agentapi.CompletionBlocked, Summary: "old", Blocker: &agentapi.CompletionBlocker{Kind: "old", Reason: "old"}}}, false)
	m.upsertItemLocked(s, agentapi.Item{ID: "plan", Kind: agentapi.ItemNotice,
		Plan: &agentapi.PlanReview{RequestID: "old", Summary: "old", Actions: []agentapi.PlanAction{agentapi.PlanInteractive}}}, false)
	s.interactions = []*interaction{{Interaction: agentapi.Interaction{ID: "question", Kind: agentapi.InteractionQuestion, State: agentapi.InteractionPending,
		Options: []agentapi.Option{{ID: "old"}}, Questions: []agentapi.Question{{Text: "old", Choices: []string{"old"},
			Field: &agentapi.Field{Name: "old", Values: []string{"old"}, Minimum: &price, MaxLength: &limit}}},
		Elicitation: &agentapi.Elicitation{Mode: agentapi.ElicitationURL, URL: "https://old.example"}}},
		{Interaction: agentapi.Interaction{ID: "review", Kind: agentapi.InteractionPlanReview, State: agentapi.InteractionPending,
			Plan: &agentapi.PlanReview{RequestID: "old", Content: "old", Previous: "old", Actions: []agentapi.PlanAction{agentapi.PlanInteractive}}}}}
	s.subagents = []*agentapi.Subagent{{ID: "child", Status: agentapi.SubagentRunning, Runs: []agentapi.SubagentRun{{Trigger: "old"}}, Retry: &agentapi.Retry{Reason: "old"}}}
	s.queue = []QueuedPrompt{{Files: []string{"old"}, Attachments: []agentapi.Attachment{{ID: "old"}}}}
	s.last = &Submission{CommandResult: &agentapi.CommandResult{Options: []agentapi.CommandOption{{Name: "old"}}}}
	s.turnActivity.Retry = &agentapi.Retry{Reason: "old"}
	s.turnActivity.Todos.Todos = []agentapi.Todo{{ID: "old"}}
	s.turnTimings = []store.TurnTiming{{ID: "turn", State: "completed", Changes: &store.TurnChangeCounts{Status: "available", EventID: "old", Files: 1, Additions: 1}}}
	s.rewind = &store.WebRewind{RequestID: "old", State: rewindUncertain, Mode: "conversation", Discarded: []string{"old"}, Result: []byte(`{"outcome":"success"}`)}
	s.schedules = &agentapi.ScheduleSnapshot{Supported: true, Known: true, Entries: []agentapi.ScheduleEntry{{ID: "1", Cron: "old"}}}
	m.mu.Unlock()
	return m, sum.ID, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		price, credits, exit, limit, enabled = 9, 9, 9, 9, false
		m.settings.HiddenModels["fake"][0], m.settings.SubagentModels["fake"][0] = "changed", "changed"
		m.settings.TitleModel["fake"], m.settings.CustomModels[0].Name = "changed", "changed"
		s.context.Used, s.usage.AIUnits, s.execution.Objective.Objective = 9, 9, "changed"
		s.items[0].Tool.Tail[0].Text, s.items[0].Tool.Declaration.Path, s.items[0].Tool.FileEdits[0].Path = "changed", "changed", "/changed"
		s.items[0].Images[0].ID, s.items[0].Attachments[0].ID = "changed", "changed"
		s.items[1].Completion.Summary, s.items[1].Completion.Blocker.Kind = "changed", "changed"
		s.interactions[0].Options[0].ID, s.interactions[0].Questions[0].Text, s.interactions[0].Questions[0].Choices[0] = "changed", "changed", "changed"
		s.items[2].Plan.Summary, s.items[2].Plan.Actions[0] = "changed", agentapi.PlanExitOnly
		s.interactions[1].Plan.Content, s.interactions[1].Plan.Previous, s.interactions[1].Plan.Actions[0] = "changed", "changed", agentapi.PlanExitOnly
		field := s.interactions[0].Questions[0].Field
		field.Name, field.Values[0], *field.Minimum, *field.MaxLength = "changed", "changed", 9, 9
		s.interactions[0].Elicitation.URL = "https://changed.example"
		s.subagents[0].Runs[0].Trigger, s.subagents[0].Retry.Reason = "changed", "changed"
		s.queue[0].Files[0], s.queue[0].Attachments[0].ID = "changed", "changed"
		s.last.CommandResult.Options[0].Name, s.turnActivity.Retry.Reason, s.turnActivity.Todos.Todos[0].ID = "changed", "changed", "changed"
		s.turnTimings[0].Changes.EventID, s.turnTimings[0].Changes.Files = "changed", 9
		s.rewind.RequestID, s.rewind.State, s.rewind.Discarded[0], s.rewind.Result[2] = "changed", rewindApplied, "changed", 'X'
		s.schedules.Entries[0].Cron = "changed"
	}
}

func TestCapturedSubscriptionOwnsMutablePayload(t *testing.T) {
	for _, mode := range []string{"home", "legacy", "recent", "compact"} {
		t.Run(mode, func(t *testing.T) {
			m, id, mutate := snapshotFixture(t)
			if mode == "home" {
				id = ""
			}
			sub, payload, err := m.captureSubscription(id, true, mode == "recent", mode == "compact")
			if err != nil {
				t.Fatal(err)
			}
			defer m.Unsubscribe(sub)
			before, err := encodeFrame("snapshot", payload)
			if err != nil {
				t.Fatal(err)
			}
			mutate()
			after, err := encodeFrame("snapshot", payload)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("captured %s snapshot changed after manager mutation: %v", mode, err)
			}
		})
	}
}

func TestCapturedSubscriptionQueuesLaterEventsBeforeEncoding(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	conv.EmitDelta("answer", agentapi.ItemAssistant, "before")
	sub, payload, err := m.captureSubscription(sum.ID, true, false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Unsubscribe(sub)
	conv.EmitDelta("answer", agentapi.ItemAssistant, " after")
	raw, err := encodeFrame("snapshot", payload)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := parseFrame(t, raw)
	if bytes.Contains(raw, []byte("before after")) || !bytes.Contains(raw, []byte("before")) {
		t.Fatal("encoding included a delta after snapshot capture")
	}
	next := parseFrame(t, <-sub.Frames())
	if next.event != "delta" || next.seq <= snapshot.seq || string(next.data["text"]) != `" after"` {
		t.Fatalf("later delta lost or misordered: %+v", next)
	}
}

func TestCapturedSubscriptionConcurrentMutation(t *testing.T) {
	m, id, mutate := snapshotFixture(t)
	sub, payload, err := m.captureSubscription(id, true, false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Unsubscribe(sub)
	before, err := encodeFrame("snapshot", payload)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 100 {
			mutate()
		}
	})
	for range 100 {
		raw, err := encodeFrame("snapshot", payload)
		if err != nil || !bytes.Equal(before, raw) {
			t.Errorf("concurrent mutation changed capture: %v", err)
			break
		}
	}
	wg.Wait()
}

func TestSubscribeEncodingFailureRemovesRegisteredSubscriber(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, _ := createSession(t, m, prov)
	price := math.NaN()
	m.mu.Lock()
	m.settings.TokenPrices = map[string]map[string]store.WebTokenPrice{"fake": {"model": {Input: &price}}}
	before := len(m.subs)
	m.mu.Unlock()
	if sub, raw, err := m.Subscribe(sum.ID); err == nil || sub != nil || raw != nil {
		t.Fatalf("invalid JSON did not fail subscription: %v", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.subs) != before {
		t.Fatal("encoding failure leaked a subscriber")
	}
}
