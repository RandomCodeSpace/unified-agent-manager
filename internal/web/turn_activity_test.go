package web

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

func cancelledTurn(reason string) agentapi.Event {
	return agentapi.Event{Kind: agentapi.EventTurn, Turn: &agentapi.Turn{State: agentapi.TurnCancelled, Reason: reason}}
}

func TestStopReasonPrecedencePersistsAndClearsAtTheNextTurn(t *testing.T) {
	m, prov, st := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	summary := func() SessionSummary {
		t.Helper()
		s, err := m.Summary(sum.ID)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}

	// The owner's Stop wins over whatever the provider reports for it.
	conv.EmitTurn(agentapi.TurnWorking, "")
	if _, err := m.Cancel(sum.ID); err != nil {
		t.Fatal(err)
	}
	conv.Emit(cancelledTurn("remote_command"))
	if got := summary(); got.State != StateCancelled || got.StopReason != stopOwner || got.StateDetail != "" {
		t.Fatalf("owner stop = %s %q %q", got.State, got.StopReason, got.StateDetail)
	}

	// Without a stop of uam's, the provider's reason says why, with a
	// sentence for older clients.
	conv.EmitTurn(agentapi.TurnWorking, "")
	if got := summary(); got.StopReason != "" {
		t.Fatalf("a new turn kept the stop reason %q", got.StopReason)
	}
	conv.Emit(cancelledTurn("autopilot_credit_limit"))
	if got := summary(); got.StopReason != stopCreditLimit || got.StateDetail != "Autopilot stopped at its credit limit" {
		t.Fatalf("credit limit stop = %q %q", got.StopReason, got.StateDetail)
	}

	// It survives a restart, written with the cancelled state.
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	if rec, _ := loadRecord(t, st, prov.Name(), sum.ID); rec.Web == nil || rec.Web.StopReason != stopCreditLimit {
		t.Fatalf("persisted = %+v", rec.Web)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	m = startManager(t, st, agenttest.NewProvider("fake", allCaps))
	if got := summary(); got.State != StateCancelled || got.StopReason != stopCreditLimit {
		t.Fatalf("reloaded = %s %q", got.State, got.StopReason)
	}
}

// A Stop that arrives as the turn completes on its own explains nothing
// later: the next turn's own cancellation reports the provider's reason.
func TestStopReasonOfAStopThatLostTheRaceIsDropped(t *testing.T) {
	for _, end := range []agentapi.TurnState{agentapi.TurnCompleted, agentapi.TurnFailed} {
		m, prov, _ := newTestManager(t)
		sum, conv := createSession(t, m, prov)
		conv.EmitTurn(agentapi.TurnWorking, "")
		if _, err := m.Cancel(sum.ID); err != nil {
			t.Fatal(err)
		}
		conv.EmitTurn(end, "")
		conv.EmitTurn(agentapi.TurnWorking, "")
		conv.Emit(cancelledTurn("autopilot_credit_limit"))
		if got, _ := m.Summary(sum.ID); got.StopReason != stopCreditLimit || got.StateDetail != "Autopilot stopped at its credit limit" {
			t.Errorf("after a %s turn: stop reason %q, detail %q", end, got.StopReason, got.StateDetail)
		}
	}
}

func TestStopReasonForEachProviderCode(t *testing.T) {
	for reason, want := range map[string]string{
		"autopilot_credit_limit": stopCreditLimit,
		"remote_command":         stopRemote,
		"user_abort":             stopMCP,
		"user_initiated":         "",
		"":                       "",
		"something_new":          "",
	} {
		m, prov, _ := newTestManager(t)
		sum, conv := createSession(t, m, prov)
		conv.EmitTurn(agentapi.TurnWorking, "")
		conv.Emit(cancelledTurn(reason))
		got, _ := m.Summary(sum.ID)
		if got.StopReason != want || (want == "") != (got.StateDetail == "") {
			t.Errorf("%q: stop reason %q, detail %q; want %q", reason, got.StopReason, got.StateDetail, want)
		}
		// Shown only while the Task is cancelled.
		conv.EmitTurn(agentapi.TurnCompleted, "")
		if got, _ := m.Summary(sum.ID); got.StopReason != "" {
			t.Errorf("%q: completed Task reports %q", reason, got.StopReason)
		}
	}
}

func TestTurnActivityIsPublishedNeverWrittenAndPerTurn(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	sub, _, err := m.Subscribe(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Unsubscribe(sub)
	conv.EmitTurn(agentapi.TurnWorking, "")
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	s := m.sessions[sum.ID]
	revision, persisted := s.timingRevision, s.persisted
	m.mu.Unlock()

	at := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	conv.Emit(agentapi.Event{Kind: agentapi.EventActivity, Activity: &agentapi.Activity{Intent: "Building \x1b[1mthe index " + strings.Repeat("x", 300)}})
	conv.Emit(agentapi.Event{Kind: agentapi.EventActivity, Activity: &agentapi.Activity{Intent: "Building the index page", Retry: &agentapi.Retry{Count: 2, Reason: "rate\nlimited", Status: 429, At: at}}})
	// The same activity again publishes nothing.
	conv.Emit(agentapi.Event{Kind: agentapi.EventActivity, Activity: &agentapi.Activity{Intent: "Building the index page", Retry: &agentapi.Retry{Count: 2, Reason: "rate\nlimited", Status: 429, At: at}}})
	// Another model call of the same turn keeps it.
	conv.EmitTurn(agentapi.TurnWorking, "")

	var first, second TurnActivity
	decodeField(t, frameOf(t, sub, "turn_activity"), "turn_activity", &first)
	decodeField(t, frameOf(t, sub, "turn_activity"), "turn_activity", &second)
	if want := "Building the index " + strings.Repeat("x", maxIntentRunes-len("Building the index ")) + "…"; first.Intent != want || first.Retry != nil || first.Todos.Known {
		t.Fatalf("first frame = %+v", first)
	}
	if r := second.Retry; second.Intent != "Building the index page" || r == nil || r.Count != 2 || r.Reason != "rate limited" || r.Status != 429 || !r.At.Equal(at) {
		t.Fatalf("second frame = %+v retry %+v", second, second.Retry)
	}
	if got := detail(t, m, sum.ID).TurnActivity; got == nil || got.Intent != "Building the index page" || got.Retry == nil {
		t.Fatalf("detail activity = %+v", got)
	}
	// sessions.json is written only when the durable key changes.
	m.mu.Lock()
	durable, timing := s.persisted == persisted && s.key() == persisted, s.timingRevision == revision
	m.mu.Unlock()
	if !durable || !timing {
		t.Fatalf("activity changed what is written: key unchanged %v, timing revision unchanged %v", durable, timing)
	}

	// The turn's end clears it, and so does the next turn's start.
	conv.EmitTurn(agentapi.TurnCompleted, "")
	var cleared TurnActivity
	decodeField(t, frameOf(t, sub, "turn_activity"), "turn_activity", &cleared)
	if cleared.Intent != "" || cleared.Retry != nil {
		t.Fatalf("activity after the turn = %+v", cleared)
	}
	conv.Emit(agentapi.Event{Kind: agentapi.EventActivity, Activity: &agentapi.Activity{Intent: "late"}})
	conv.EmitTurn(agentapi.TurnWorking, "")
	if got := detail(t, m, sum.ID).TurnActivity; got == nil || got.Intent != "" {
		t.Fatalf("new turn activity = %+v", got)
	}
}

func TestSubagentRetryIsCleanAndOnlyWhileRunning(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	retry := &agentapi.Retry{Count: 1, Reason: "rate_\x1b[2Jlimited " + strings.Repeat("y", 100), Status: 42}
	conv.EmitSubagent(agentapi.Subagent{ID: "a1", Name: "explore", Status: agentapi.SubagentRunning, Retry: retry})
	got := detail(t, m, sum.ID).Subagents
	if len(got) != 1 || got[0].Retry == nil || got[0].Retry.Reason != "rate_limited "+strings.Repeat("y", maxRetryReasonRunes-len("rate_limited "))+"…" || got[0].Retry.Status != 0 {
		t.Fatalf("running subagent = %+v", got)
	}
	conv.EmitSubagent(agentapi.Subagent{ID: "a1", Name: "explore", Status: agentapi.SubagentCompleted, Retry: retry})
	if got := detail(t, m, sum.ID).Subagents; len(got) != 1 || got[0].Retry != nil {
		t.Fatalf("completed subagent kept its retry: %+v", got[0].Retry)
	}
}

func todosEvent(known bool, rows ...agentapi.Todo) agentapi.Event {
	return agentapi.Event{Kind: agentapi.EventTodos, Todos: &agentapi.TodoList{Known: known, Todos: rows}}
}

func TestTurnActivityTodosCountsNowAndCap(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	at := func(sec int64) time.Time { return time.Unix(sec, 0) }
	rows := []agentapi.Todo{
		{ID: "a", Title: " Plan \x1b[1mit ", Status: agentapi.TodoDone, ChangedAt: at(10)},
		{ID: "b", Title: "Build", Status: agentapi.TodoInProgress, AgentID: "sub-1", ChangedAt: at(20)},
		{ID: "c", Title: "Test", Status: agentapi.TodoInProgress, ChangedAt: at(20)},
		{ID: "d", Title: "Ship", Status: agentapi.TodoBlocked, Note: "needs\nsign-in", ChangedAt: at(15)},
		{ID: "e", Title: "Docs", Status: agentapi.TodoPending, Note: "not a reason"},
		{ID: "f", Title: strings.Repeat("t", 300), Status: "archived"},
		{ID: "g", Title: "Same time, later in order", Status: agentapi.TodoInProgress, ChangedAt: at(20)},
		{ID: "h", Title: "Older", Status: agentapi.TodoInProgress, ChangedAt: at(5)},
	}
	for i := range maxTodoRows {
		rows = append(rows, agentapi.Todo{ID: "r" + strings.Repeat("x", i), Title: "Row", Status: agentapi.TodoDone})
	}
	conv.Emit(todosEvent(true, rows...))
	v := detail(t, m, sum.ID).TurnActivity.Todos
	if !v.Known || v.Touched || len(v.Todos) != maxTodoRows || v.Omitted != len(rows)-maxTodoRows {
		t.Fatalf("view: known %v touched %v rows %d omitted %d", v.Known, v.Touched, len(v.Todos), v.Omitted)
	}
	if want := (TodoCounts{Total: len(rows), Done: 1 + maxTodoRows, Blocked: 1, Open: 6}); v.Counts != want {
		t.Fatalf("counts = %+v, want %+v", v.Counts, want)
	}
	// The latest change in progress, the main agent's at a tie, then the first.
	if v.Now != "c" {
		t.Fatalf("now = %q", v.Now)
	}
	if a, d, e, f := v.Todos[0], v.Todos[3], v.Todos[4], v.Todos[5]; a.Title != "Plan it" || d.Note != "needs sign-in" || e.Note != "" || f.Status != agentapi.TodoPending || f.Title != strings.Repeat("t", maxTodoTitleRunes)+"…" {
		t.Fatalf("rows = %+v", v.Todos[:6])
	}
	// None in progress: no row is Now.
	conv.Emit(todosEvent(true, agentapi.Todo{ID: "a", Title: "Plan", Status: agentapi.TodoDone}))
	if v := detail(t, m, sum.ID).TurnActivity.Todos; v.Now != "" || v.Omitted != 0 || v.Counts != (TodoCounts{Total: 1, Done: 1}) {
		t.Fatalf("done view = %+v", v)
	}
}

func TestTurnActivityTodosTouchedOnlyByTheRunningTurnAndNeverWritten(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	at := func(sec int64) time.Time { return time.Unix(sec, 0) }
	todos := func() TodoView {
		t.Helper()
		return detail(t, m, sum.ID).TurnActivity.Todos
	}
	plan := []agentapi.Todo{{ID: "a", Title: "Plan", Status: agentapi.TodoDone, ChangedAt: at(10)}, {ID: "b", Title: "Build", Status: agentapi.TodoInProgress, ChangedAt: at(10)}}

	// A turn that writes the list touches it, and the answer stays after it.
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.Emit(todosEvent(true, plan...))
	conv.EmitTurn(agentapi.TurnCompleted, "")
	if v := todos(); !v.Touched || v.Counts.Open != 1 {
		t.Fatalf("first turn = %+v", v)
	}

	// The next turn finds it untouched, through its model calls, until the
	// list changes; the same list again changes nothing.
	conv.EmitTurn(agentapi.TurnWorking, "")
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	s := m.sessions[sum.ID]
	revision, persisted := s.timingRevision, s.persisted
	m.mu.Unlock()
	conv.Emit(todosEvent(true, plan...))
	conv.EmitTurn(agentapi.TurnWorking, "")
	if v := todos(); v.Touched {
		t.Fatal("an unchanged list touched the turn")
	}
	done := []agentapi.Todo{plan[0], {ID: "b", Title: "Build", Status: agentapi.TodoDone, ChangedAt: at(30)}}
	conv.Emit(todosEvent(true, done...))
	if v := todos(); !v.Touched || v.Counts.Done != 2 {
		t.Fatalf("changed list = %+v", v)
	}
	// Todo changes write nothing to sessions.json.
	m.mu.Lock()
	durable, timing := s.persisted == persisted && s.key() == persisted, s.timingRevision == revision
	m.mu.Unlock()
	if !durable || !timing {
		t.Fatalf("todos changed what is written: key unchanged %v, timing revision unchanged %v", durable, timing)
	}
	conv.EmitTurn(agentapi.TurnCompleted, "")

	// Between turns nothing touches it.
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.EmitTurn(agentapi.TurnCompleted, "")
	conv.Emit(todosEvent(true, append(done, agentapi.Todo{ID: "c", Title: "Late", Status: agentapi.TodoPending, ChangedAt: at(40)})...))
	if v := todos(); v.Touched || v.Counts.Total != 3 {
		t.Fatalf("between turns = %+v", v)
	}

	// A list unknown when the turn started (read again on reopening): rows
	// without a newer change leave it untouched.
	conv.Emit(todosEvent(false, done...))
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.Emit(todosEvent(true, agentapi.Todo{ID: "a", Title: "Plan", Status: agentapi.TodoDone}, agentapi.Todo{ID: "b", Title: "Build", Status: agentapi.TodoDone}))
	if v := todos(); v.Touched || !v.Known {
		t.Fatalf("reread list = %+v", v)
	}
	conv.Emit(todosEvent(true, agentapi.Todo{ID: "a", Title: "Plan", Status: agentapi.TodoDone}, agentapi.Todo{ID: "b", Title: "Build", Status: agentapi.TodoInProgress, ChangedAt: at(50)}))
	if v := todos(); !v.Touched {
		t.Fatalf("changed reread list = %+v", v)
	}

	// Once the conversation is gone the list is not known.
	conv.Exit("gone")
	if v := todos(); v.Known || len(v.Todos) != 2 {
		t.Fatalf("after exit = %+v", v)
	}
}
