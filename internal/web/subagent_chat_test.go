package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

// idleSubagent creates a Task with one subagent the provider reports idle.
func idleSubagent(t *testing.T, m *Manager, prov *agenttest.Provider) (SessionSummary, *agenttest.Conversation) {
	t.Helper()
	sum, conv := createSession(t, m, prov)
	for _, status := range []agentapi.SubagentStatus{agentapi.SubagentRunning, agentapi.SubagentCompleted, agentapi.SubagentIdle} {
		conv.EmitSubagent(agentapi.Subagent{ID: "target", ParentToolCallID: "call-target", Name: "helper", Status: status})
	}
	if sa, err := m.Subagent(sum.ID, "target"); err != nil || sa.Subagent.Status != agentapi.SubagentIdle {
		t.Fatalf("subagent = %+v, %v", sa, err)
	}
	return sum, conv
}

// acceptAndRun accepts follow-ups the way an adapter does: the subagent runs.
func acceptAndRun(conv *agenttest.Conversation) {
	conv.SetPromptSubagentHook(func(_ context.Context, agentID, _ string) error {
		conv.EmitSubagent(agentapi.Subagent{ID: agentID, Status: agentapi.SubagentRunning})
		return nil
	})
}

func promptBody(text, rid string) string {
	b, _ := json.Marshal(map[string]string{"text": text, "request_id": rid})
	return string(b)
}

func TestPromptSubagentRouteSendsOnceAndLeavesTheTaskAlone(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	auth := withCookie(ts)
	sum, conv := createSession(t, ts.m, ts.prov)
	// A paused queue beside an idle Task: the follow-up must not drain it.
	if sub, err := ts.m.Submit(sum.ID, "first", mustUUID(t), ModeSend); err != nil || sub.Status != SubmissionAccepted {
		t.Fatalf("first prompt = %+v, %v", sub, err)
	}
	conv.EmitTurn(agentapi.TurnWorking, "")
	mustSubmit(t, ts.m, sum.ID, "queued", mustUUID(t), ModeQueue, SubmissionQueued)
	conv.EmitTurn(agentapi.TurnCancelled, "")
	for _, status := range []agentapi.SubagentStatus{agentapi.SubagentRunning, agentapi.SubagentCompleted, agentapi.SubagentIdle} {
		conv.EmitSubagent(agentapi.Subagent{ID: "target", Name: "helper", Status: status})
	}
	acceptAndRun(conv)
	before := detail(t, ts.m, sum.ID)
	path := "/api/sessions/" + sum.ID + "/subagents/target/prompt"
	if w := ts.do(http.MethodPost, path, promptBody("x", mustUUID(t))); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated = %d", w.Code)
	}
	for _, c := range []struct {
		path, body string
		want       int
	}{
		{path, promptBody("x", "not-a-uuid"), http.StatusBadRequest},
		{path, promptBody(" \n", mustUUID(t)), http.StatusBadRequest},
		{path, promptBody(strings.Repeat("x", maxPromptBytes+1), mustUUID(t)), http.StatusRequestEntityTooLarge},
		{"/api/sessions/missing/subagents/target/prompt", promptBody("x", mustUUID(t)), http.StatusNotFound},
		{"/api/sessions/" + sum.ID + "/subagents/missing/prompt", promptBody("x", mustUUID(t)), http.StatusNotFound},
	} {
		if w := ts.do(http.MethodPost, c.path, c.body, auth); w.Code != c.want {
			t.Fatalf("%s %.40s = %d %s, want %d", c.path, c.body, w.Code, w.Body, c.want)
		}
	}
	rid := mustUUID(t)
	for range 2 {
		w := ts.do(http.MethodPost, path, promptBody("follow up", rid), auth)
		var sub Submission
		if err := json.Unmarshal(w.Body.Bytes(), &sub); err != nil || w.Code != http.StatusAccepted || sub.RequestID != rid || sub.Status != SubmissionAccepted {
			t.Fatalf("follow-up = %d %s", w.Code, w.Body)
		}
	}
	if got := conv.SubagentPrompts(); len(got) != 1 || got[0] != (agenttest.SubagentPrompt{AgentID: "target", Text: "follow up"}) {
		t.Fatalf("provider prompts = %+v", got)
	}
	if w := ts.do(http.MethodPost, path, promptBody("again", mustUUID(t)), auth); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "still running") {
		t.Fatalf("follow-up to a running subagent = %d %s", w.Code, w.Body)
	}
	// The follow-up's events belong to the subagent only.
	conv.EmitItem(agentapi.Item{ID: "u2", Kind: agentapi.ItemUser, Text: "follow up", AgentID: "target"})
	conv.EmitItem(agentapi.Item{ID: "m2", Kind: agentapi.ItemAssistant, Text: "reply", AgentID: "target"})
	conv.EmitSubagent(agentapi.Subagent{ID: "target", Status: agentapi.SubagentIdle})
	sa, err := ts.m.Subagent(sum.ID, "target")
	if err != nil || sa.Subagent.Status != agentapi.SubagentIdle || sa.Subagent.Name != "helper" || len(sa.Items) != 2 || sa.Items[0].Kind != agentapi.ItemUser || sa.Items[0].AgentID != "target" {
		t.Fatalf("subagent = %+v, %v", sa, err)
	}
	after := detail(t, ts.m, sum.ID)
	if after.State != before.State || after.State != StateCancelled || !after.QueuePaused || queueTexts(after) != "queued" ||
		after.LastSubmission == nil || *after.LastSubmission != *before.LastSubmission || len(after.Items) != len(before.Items) {
		t.Fatalf("task changed: before %+v, after %+v", before.SessionSummary, after.SessionSummary)
	}
	if len(conv.Sends()) != 1 || len(conv.Steers()) != 0 {
		t.Fatalf("task prompts: sends %v steers %v", conv.Sends(), conv.Steers())
	}
	// The Task's own composer still works.
	mustSubmit(t, ts.m, sum.ID, "next", mustUUID(t), ModeSend, SubmissionAccepted)
}

func TestPromptSubagentRefusals(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, m *Manager, sum SessionSummary, conv *agenttest.Conversation)
		want  int
	}{
		{"running", func(_ *testing.T, _ *Manager, _ SessionSummary, conv *agenttest.Conversation) {
			conv.EmitSubagent(agentapi.Subagent{ID: "target", Status: agentapi.SubagentRunning})
		}, http.StatusConflict},
		{"completed, as a background agent stays", func(_ *testing.T, _ *Manager, _ SessionSummary, conv *agenttest.Conversation) {
			conv.EmitSubagent(agentapi.Subagent{ID: "target", Status: agentapi.SubagentCompleted})
		}, http.StatusConflict},
		{"failed", func(_ *testing.T, _ *Manager, _ SessionSummary, conv *agenttest.Conversation) {
			conv.EmitSubagent(agentapi.Subagent{ID: "target", Status: agentapi.SubagentFailed})
		}, http.StatusConflict},
		{"cancelled", func(_ *testing.T, _ *Manager, _ SessionSummary, conv *agenttest.Conversation) {
			conv.EmitSubagent(agentapi.Subagent{ID: "target", Status: agentapi.SubagentCancelled})
		}, http.StatusConflict},
		{"task working", func(_ *testing.T, _ *Manager, _ SessionSummary, conv *agenttest.Conversation) {
			conv.EmitTurn(agentapi.TurnWorking, "")
		}, http.StatusConflict},
		{"task awaiting permission", func(_ *testing.T, _ *Manager, _ SessionSummary, conv *agenttest.Conversation) {
			conv.EmitInteraction(onceRequest("permission", "sibling"))
		}, http.StatusConflict},
		{"task settled", func(t *testing.T, m *Manager, sum SessionSummary, _ *agenttest.Conversation) {
			if _, err := m.Settle(sum.ID); err != nil {
				t.Fatal(err)
			}
		}, http.StatusConflict},
		{"task archived", func(t *testing.T, m *Manager, sum SessionSummary, _ *agenttest.Conversation) {
			if _, err := m.Archive(sum.ID); err != nil {
				t.Fatal(err)
			}
		}, http.StatusConflict},
		{"conversation closed", func(t *testing.T, m *Manager, sum SessionSummary, _ *agenttest.Conversation) {
			if _, err := m.Close(sum.ID); err != nil {
				t.Fatal(err)
			}
		}, http.StatusConflict},
		{"conversation exited", func(_ *testing.T, _ *Manager, _ SessionSummary, conv *agenttest.Conversation) {
			conv.Exit("gone")
		}, http.StatusConflict},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, prov, _ := newTestManager(t)
			sum, conv := idleSubagent(t, m, prov)
			c.setup(t, m, sum, conv)
			opens := len(prov.Opens())
			_, err := m.PromptSubagent(sum.ID, "target", "hi", mustUUID(t))
			if statusOf(err) != c.want {
				t.Fatalf("PromptSubagent = %v, want %d", err, c.want)
			}
			if len(conv.SubagentPrompts()) != 0 || len(prov.Opens()) != opens {
				t.Fatalf("refusal reached the provider: prompts %v, opens %d", conv.SubagentPrompts(), len(prov.Opens()))
			}
		})
	}
	m, prov, _ := newTestManager(t)
	sum, conv := idleSubagent(t, m, prov)
	for _, c := range []struct{ id, agent string }{{"missing", "target"}, {sum.ID, "missing"}} {
		if _, err := m.PromptSubagent(c.id, c.agent, "hi", mustUUID(t)); statusOf(err) != http.StatusNotFound {
			t.Fatalf("unknown %s/%s = %v", c.id, c.agent, err)
		}
	}
	conv.SetPromptSubagentHook(func(context.Context, string, string) error { return agentapi.ErrUnsupported })
	if _, err := m.PromptSubagent(sum.ID, "target", "hi", mustUUID(t)); statusOf(err) != http.StatusConflict || !strings.Contains(err.Error(), "cannot chat") {
		t.Fatalf("unsupported = %v", err)
	}
}

func TestPromptSubagentOutcomesAreRecordedAndNeverResent(t *testing.T) {
	for _, c := range []struct {
		status, text string
		err          error
	}{
		{SubmissionUncertain, "did not resend", fmt.Errorf("%w: timeout", agentapi.ErrSubmissionUncertain)},
		{SubmissionRejected, "Agent not found or not accepting messages", errors.New("copilot did not deliver the follow-up: Agent not found or not accepting messages")},
	} {
		t.Run(c.status, func(t *testing.T) {
			m, prov, _ := newTestManager(t)
			sum, conv := idleSubagent(t, m, prov)
			conv.SetPromptSubagentHook(func(context.Context, string, string) error { return c.err })
			rid := mustUUID(t)
			for range 2 {
				sub, err := m.PromptSubagent(sum.ID, "target", "hi", rid)
				if err != nil || sub.Status != c.status || !strings.Contains(sub.Error, c.text) || sub.RequestID != rid {
					t.Fatalf("PromptSubagent = %+v, %v", sub, err)
				}
			}
			if len(conv.SubagentPrompts()) != 1 {
				t.Fatalf("resent: %v", conv.SubagentPrompts())
			}
			if d := detail(t, m, sum.ID); d.LastSubmission != nil || d.State != StateIdle {
				t.Fatalf("task changed: %+v", d.SessionSummary)
			}
		})
	}
	m, prov, _ := newTestManager(t)
	sum, _ := idleSubagent(t, m, prov)
	for range maxSubmissions + 1 {
		if _, err := m.PromptSubagent(sum.ID, "target", "hi", mustUUID(t)); err != nil {
			t.Fatal(err)
		}
	}
	m.mu.Lock()
	kept := len(m.sessions[sum.ID].subagentPrompts)
	m.mu.Unlock()
	if kept != maxSubmissions {
		t.Fatalf("kept %d follow-up outcomes", kept)
	}
}

func TestSubagentIdleEndsWithConversationAndOnlyHistoryRestoresIt(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := idleSubagent(t, m, prov)
	stream, _, err := m.Subscribe(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Unsubscribe(stream)
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	var event agentapi.Subagent
	decodeField(t, frameOf(t, stream, "subagent"), "subagent", &event)
	if event.ID != "target" || event.Status != agentapi.SubagentCompleted {
		t.Fatalf("SSE = %+v", event)
	}
	reopen := func(history agentapi.SubagentStatus) {
		t.Helper()
		prov.AddConversation(conv.ID(), nil, agentapi.Subagent{ID: "target", Status: history})
		mustSubmit(t, m, sum.ID, "reopen", mustUUID(t), ModeSend, SubmissionAccepted)
		prov.Last().EmitTurn(agentapi.TurnCompleted, "")
	}
	// Reopening never restores idle from UAM's own record.
	reopen(agentapi.SubagentCompleted)
	if sa, _ := m.Subagent(sum.ID, "target"); sa.Subagent.Status != agentapi.SubagentCompleted {
		t.Fatalf("after reopen = %+v", sa.Subagent)
	}
	if _, err := m.PromptSubagent(sum.ID, "target", "hi", mustUUID(t)); statusOf(err) != http.StatusConflict {
		t.Fatalf("follow-up after reopen = %v", err)
	}
	// The provider's history, from its live task list, can.
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	reopen(agentapi.SubagentIdle)
	if sub, err := m.PromptSubagent(sum.ID, "target", "hi", mustUUID(t)); err != nil || sub.Status != SubmissionAccepted {
		t.Fatalf("follow-up after the provider reported idle = %+v, %v", sub, err)
	}
}
