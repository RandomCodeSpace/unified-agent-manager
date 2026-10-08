package copilot

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func todoRow(id, title, status string) rpc.PlanSQLTodosRow {
	return rpc.PlanSQLTodosRow{ID: &id, Title: &title, Status: &status}
}

// todoLists returns the todo lists emitted after the first n events.
func todoLists(h webHarness, n int) []agentapi.TodoList {
	var out []agentapi.TodoList
	for _, e := range h.sink.all()[n:] {
		if e.Kind == agentapi.EventTodos {
			out = append(out, *e.Todos)
		}
	}
	return out
}

func (s *fakeSession) setTodos(rows ...rpc.PlanSQLTodosRow) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.todoRows = rows
}

func (s *fakeSession) todoReadCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.todoReads
}

// todosChanged is a session.todos_changed by agentID ("" for the main
// agent) at second sec.
func todosChanged(id, agentID string, sec int64) copilot.SessionEvent {
	e := ev(id, &rpc.SessionTodosChangedData{})
	if agentID != "" {
		e.AgentID = &agentID
	}
	e.Timestamp = time.Unix(sec, 0)
	return e
}

func TestSubagentStartHookOnCreateAndResume(t *testing.T) {
	h := openWeb(t)
	sink := &recSink{}
	if _, err := h.p.Open(context.Background(), agentapi.OpenRequest{SessionID: "s-2", ConversationID: "conv-2", Workdir: "/work", Events: sink}); err != nil {
		t.Fatal(err)
	}
	for name, hooks := range map[string]*copilot.SessionHooks{"create": h.fc.create[0].Hooks, "resume": h.fc.resume[0].Hooks} {
		if hooks == nil || hooks.OnPreToolUse == nil || hooks.OnSubagentStart == nil {
			t.Fatalf("%s hooks = %+v", name, hooks)
		}
		out, err := hooks.OnSubagentStart(copilot.SubagentStartHookInput{AgentName: "general-purpose"}, copilot.HookInvocation{SessionID: "s"})
		if err != nil || out == nil || out.AdditionalContext != subagentSystem {
			t.Fatalf("%s subagent start = %+v, %v", name, out, err)
		}
	}
	for _, rule := range []string{"Keep your own row in the session's `todos` table with the sql tool", "set it to done right when you finish", "no Co-authored-by trailer and no line crediting an AI"} {
		if !strings.Contains(subagentSystem, rule) {
			t.Errorf("subagentSystem lacks %q", rule)
		}
	}
}

func TestWebTodosDebounceCoalescesReadsOneAtATime(t *testing.T) {
	h := openWeb(t)
	// A new conversation has no list to read on opening.
	if n := h.fs.todoReadCount(); n != 0 {
		t.Fatalf("reads on create = %d", n)
	}
	h.fs.setTodos(todoRow("a", "Write the page", "in_progress"))
	n := len(h.sink.all())
	for i := range 3 {
		h.fs.onEvent(todosChanged("c"+string(rune('0'+i)), "", 100))
	}
	waitFor(t, "the todo list", func() bool { return len(todoLists(h, n)) == 1 })
	if reads := h.fs.todoReadCount(); reads != 1 {
		t.Fatalf("a burst made %d reads", reads)
	}
	if got := todoLists(h, n)[0]; !got.Known || len(got.Todos) != 1 || got.Todos[0].Status != agentapi.TodoInProgress {
		t.Fatalf("list = %+v", got)
	}

	// Changes during a read ask for one more read, not one each.
	entered, release := make(chan struct{}, 4), make(chan struct{})
	h.fs.mu.Lock()
	h.fs.todoHook = func() {
		entered <- struct{}{}
		<-release
	}
	h.fs.mu.Unlock()
	h.fs.onEvent(todosChanged("d1", "", 101))
	<-entered
	h.fs.onEvent(todosChanged("d2", "", 102))
	time.Sleep(2 * todosDebounce)
	h.fs.onEvent(todosChanged("d3", "", 103))
	time.Sleep(2 * todosDebounce)
	close(release)
	waitFor(t, "the second read", func() bool { return h.fs.todoReadCount() == 3 })
	time.Sleep(2 * todosDebounce)
	if reads := h.fs.todoReadCount(); reads != 3 {
		t.Fatalf("reads = %d, want the first, the one in flight and one more", reads)
	}
	// The same list again is not emitted again.
	if got := todoLists(h, n); len(got) != 1 {
		t.Fatalf("unchanged reads emitted %+v", got)
	}
}

func TestWebTodosAttributeOnlyAWindowOfOneAgent(t *testing.T) {
	h := openWeb(t)
	n := len(h.sink.all())
	read := func(rows ...rpc.PlanSQLTodosRow) agentapi.TodoList {
		t.Helper()
		h.fs.setTodos(rows...)
		want := len(todoLists(h, n)) + 1
		waitFor(t, "a todo list", func() bool { return len(todoLists(h, n)) == want })
		return todoLists(h, n)[want-1]
	}

	// One subagent wrote: its rows are its own, at the latest change.
	h.fs.onEvent(todosChanged("e1", "agent-1", 100))
	h.fs.onEvent(todosChanged("e2", "agent-1", 102))
	got := read(todoRow("a", "Page A", "in_progress"))
	if a := got.Todos[0]; a.AgentID != "agent-1" || !a.ChangedAt.Equal(time.Unix(102, 0)) {
		t.Fatalf("one agent's row = %+v", a)
	}

	// Two agents in a window: a new row gets no owner; a row seen before
	// keeps its own, and its time moves only with its status.
	h.fs.onEvent(todosChanged("e3", "agent-2", 110))
	h.fs.onEvent(todosChanged("e4", "", 111))
	got = read(todoRow("a", "Page A, renamed", "in_progress"), todoRow("b", "Page B", "pending"))
	if a, b := got.Todos[0], got.Todos[1]; a.AgentID != "agent-1" || !a.ChangedAt.Equal(time.Unix(102, 0)) || a.Title != "Page A, renamed" || b.AgentID != "" || !b.ChangedAt.Equal(time.Unix(111, 0)) {
		t.Fatalf("two agents' rows = %+v", got.Todos)
	}

	// The main agent alone: no tag either.
	h.fs.onEvent(todosChanged("e5", "", 120))
	got = read(todoRow("a", "Page A, renamed", "done"), todoRow("b", "Page B", "pending"), todoRow("c", "README", "pending"))
	if a, c := got.Todos[0], got.Todos[2]; a.AgentID != "agent-1" || !a.ChangedAt.Equal(time.Unix(120, 0)) || c.AgentID != "" || !c.ChangedAt.Equal(time.Unix(120, 0)) {
		t.Fatalf("main agent's rows = %+v", got.Todos)
	}
}

func TestWebTodosMapCleanAndBoundRows(t *testing.T) {
	h := openWeb(t)
	n := len(h.sink.all())
	unknown, note, noStatus := "archived", "  needs \x1b[31msign-in\n ", rpc.PlanSQLTodosRow{ID: copilot.String("n"), Title: copilot.String("No status")}
	blocked := todoRow("b", " Blocked \x1b[1mrow ", "blocked")
	blocked.Description = &note
	pending := todoRow("p", "Pending", "pending")
	pending.Description = &note
	rows := []rpc.PlanSQLTodosRow{blocked, pending, noStatus, todoRow("u", "Unknown", unknown), {Title: copilot.String("No id")}, todoRow("b", "Duplicate", "done"), todoRow("long", strings.Repeat("é", 300), "done")}
	for i := range maxTodos {
		rows = append(rows, todoRow("r"+strings.Repeat("x", i), "Row", "pending"))
	}
	h.fs.setTodos(rows...)
	h.fs.onEvent(todosChanged("e1", "", 100))
	waitFor(t, "the todo list", func() bool { return len(todoLists(h, n)) == 1 })
	got := todoLists(h, n)[0].Todos
	if len(got) != maxTodos {
		t.Fatalf("rows = %d, want %d", len(got), maxTodos)
	}
	if b := got[0]; b.Title != "Blocked row" || b.Status != agentapi.TodoBlocked || b.Note != "needs sign-in" {
		t.Fatalf("blocked = %+v", b)
	}
	if p := got[1]; p.Note != "" || got[2].Status != agentapi.TodoPending || got[3].Status != agentapi.TodoPending || got[4].ID != "long" {
		t.Fatalf("rows = %+v", got[:5])
	}
	if title := got[4].Title; title != strings.Repeat("é", maxTodoTitleRunes) {
		t.Fatalf("long title = %d runes", len([]rune(title)))
	}
}

func TestWebTodosReadFailureMarksTheListUnknown(t *testing.T) {
	h := openWeb(t)
	n := len(h.sink.all())
	h.fs.setTodos(todoRow("a", "Step", "pending"))
	h.fs.onEvent(todosChanged("e1", "", 100))
	waitFor(t, "the todo list", func() bool { return len(todoLists(h, n)) == 1 })
	h.fs.mu.Lock()
	h.fs.todosErr = errors.New("offline")
	h.fs.mu.Unlock()
	h.fs.onEvent(todosChanged("e2", "", 101))
	waitFor(t, "the unknown list", func() bool { return len(todoLists(h, n)) == 2 })
	h.fs.onEvent(todosChanged("e3", "", 102))
	waitFor(t, "a third read", func() bool { return h.fs.todoReadCount() == 3 })
	h.fs.mu.Lock()
	h.fs.todosErr = nil
	h.fs.mu.Unlock()
	h.fs.onEvent(todosChanged("e4", "", 103))
	waitFor(t, "the known list", func() bool { return len(todoLists(h, n)) == 3 })
	got := todoLists(h, n)
	if got[1].Known || len(got[1].Todos) != 1 || !got[2].Known || got[2].Todos[0] != got[0].Todos[0] {
		t.Fatalf("lists = %+v", got)
	}
}

func TestWebOpenResumeReadsTheTodosOnce(t *testing.T) {
	fc := &fakeClient{todos: []rpc.PlanSQLTodosRow{todoRow("a", "Left open", "in_progress")}}
	p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	sink := &recSink{}
	conv, err := p.Open(context.Background(), agentapi.OpenRequest{SessionID: "s-1", ConversationID: "conv-1", Workdir: "/work", Events: sink})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conv.Close(context.Background()) }()
	h := webHarness{p: p, fc: fc, fs: fc.sessions[0], conv: conv, sink: sink}
	waitFor(t, "the read on resume", func() bool { return h.fs.todoReadCount() == 1 })
	waitFor(t, "the list", func() bool { return len(todoLists(h, 0)) == 1 })
	time.Sleep(2 * todosDebounce)
	// A row first seen on opening has no owner and no time of change.
	if got := todoLists(h, 0); h.fs.todoReadCount() != 1 || len(got) != 1 || !got[0].Known || len(got[0].Todos) != 1 || got[0].Todos[0] != (agentapi.Todo{ID: "a", Title: "Left open", Status: agentapi.TodoInProgress}) {
		t.Fatalf("reads %d, lists %+v", h.fs.todoReadCount(), got)
	}
}

func TestSDKSessionReadTodosFallsBack(t *testing.T) {
	rt := startFakeRuntime(t)
	s := openFakeSDKSession(t, rt)
	ctx := context.Background()
	rt.set("session.plan.readSqlTodosWithDependencies", `{"rows":[{"id":"a","title":"Step","status":"done"}],"dependencies":[]}`)
	if rows, err := s.ReadTodos(ctx); err != nil || len(rows) != 1 || *rows[0].ID != "a" || rt.last("session.plan.readSqlTodos") != nil {
		t.Fatalf("ReadTodos = %+v, %v", rows, err)
	}
	// A CLI that refuses the read with dependencies is asked for the rows alone.
	rt.fail("session.plan.readSqlTodosWithDependencies", "unknown method")
	rt.set("session.plan.readSqlTodos", `{"rows":[{"id":"b","title":"Other"}]}`)
	if rows, err := s.ReadTodos(ctx); err != nil || len(rows) != 1 || *rows[0].ID != "b" || rows[0].Status != nil {
		t.Fatalf("fallback ReadTodos = %+v, %v", rows, err)
	}
	rt.fail("session.plan.readSqlTodos", "unknown method")
	if _, err := s.ReadTodos(ctx); err == nil {
		t.Fatal("ReadTodos hid a failure")
	}
	// A failure that is not the CLI's answer is not retried another way.
	fallbacks := func() int {
		rt.mu.Lock()
		defer rt.mu.Unlock()
		return len(rt.requests["session.plan.readSqlTodos"])
	}
	before := fallbacks()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	rt.set("session.plan.readSqlTodos", `{"rows":[]}`)
	if _, err := s.ReadTodos(cancelled); err == nil || fallbacks() != before {
		t.Fatalf("cancelled ReadTodos = %v, fallback reads %d", err, fallbacks()-before)
	}
}
