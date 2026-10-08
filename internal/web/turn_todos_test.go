package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

func turnTodosPath(m *Manager, id string) string {
	return filepath.Join(m.taskUploadDir(id), turnTodosFile)
}

func lastTiming(t *testing.T, m *Manager, id string) TurnTiming {
	t.Helper()
	timings := detail(t, m, id).TurnTimings
	if len(timings) == 0 {
		t.Fatal("no turn timing")
	}
	return timings[len(timings)-1]
}

func snapshotIDs(rec TurnTodos) string {
	ids := make([]string, len(rec.Todos))
	for i, t := range rec.Todos {
		ids[i] = t.ID
	}
	return strings.Join(ids, ",")
}

func TestTurnTodosScopeCapAndCountsOnTheTiming(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	at := func(sec int64) time.Time { return time.Unix(sec, 0) }
	conv.EmitSubagent(agentapi.Subagent{ID: "sub-1", Name: "explore " + strings.Repeat("x", 80), Status: agentapi.SubagentRunning})
	found := []agentapi.Todo{
		{ID: "old", Title: "Done before", Status: agentapi.TodoDone, ChangedAt: at(5)},
		{ID: "x", Title: "Plan", Status: agentapi.TodoPending},
		{ID: "y", Title: "Build", Status: agentapi.TodoInProgress, AgentID: "sub-1", ChangedAt: at(6)},
	}
	conv.Emit(todosEvent(true, found...))

	// The rows the turn changed and the rows still open: blocked, in
	// progress, pending, then done rows newest first. A done row the turn
	// did not change is out of scope.
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.Emit(agentapi.Event{Kind: agentapi.EventActivity, Activity: &agentapi.Activity{Intent: "Building the page"}})
	conv.Emit(agentapi.Event{Kind: agentapi.EventActivity, Activity: &agentapi.Activity{}})
	left := []agentapi.Todo{
		found[0],
		{ID: "x", Title: "Plan", Status: agentapi.TodoDone, ChangedAt: at(30)},
		found[2],
		{ID: "z", Title: "Ship", Status: agentapi.TodoBlocked, Note: "needs sign-in", ChangedAt: at(35)},
		{ID: "w", Title: "Write", Status: agentapi.TodoDone, ChangedAt: at(40)},
	}
	conv.Emit(todosEvent(true, left...))
	conv.EmitTurn(agentapi.TurnCompleted, "")
	first := lastTiming(t, m, sum.ID)
	if want := (TodoCounts{Total: 4, Done: 2, Blocked: 1, Open: 1}); first.Todo != want {
		t.Fatalf("timing counts = %+v, want %+v", first.Todo, want)
	}
	rec, err := m.TurnTodos(sum.ID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshotIDs(rec); got != "z,y,w,x" || rec.Counts != first.Todo || rec.Intent != "Building the page" || !rec.EndedAt.Equal(first.EndedAt) {
		t.Fatalf("snapshot = %s %+v", got, rec)
	}
	if y, z := rec.Todos[1], rec.Todos[0]; y.Agent != "explore "+strings.Repeat("x", maxSnapshotAgentRunes-len("explore "))+"…" || y.AgentID != "sub-1" || z.Agent != "" || z.Note != "needs sign-in" {
		t.Fatalf("rows = %+v", rec.Todos[:2])
	}

	// Past maxSnapshotTodos rows the rest are counted; a cancelled turn
	// keeps its list too.
	conv.EmitTurn(agentapi.TurnWorking, "")
	more := append([]agentapi.Todo(nil), left...)
	for i := range 60 {
		more = append(more, agentapi.Todo{ID: fmt.Sprintf("p%02d", i), Title: "Row", Status: agentapi.TodoPending})
	}
	conv.Emit(todosEvent(true, more...))
	conv.Emit(cancelledTurn(""))
	second := lastTiming(t, m, sum.ID)
	if want := (TodoCounts{Total: 62, Blocked: 1, Open: 61, Omitted: 62 - maxSnapshotTodos}); second.Todo != want {
		t.Fatalf("capped counts = %+v, want %+v", second.Todo, want)
	}
	rec, err = m.TurnTodos(sum.ID, second.ID)
	if err != nil || len(rec.Todos) != maxSnapshotTodos || !strings.HasPrefix(snapshotIDs(rec), "z,y,p00,p01,") || rec.Intent != "" {
		t.Fatalf("capped snapshot = %s %+v, %v", snapshotIDs(rec), rec.Counts, err)
	}

	// A turn that does not touch the list keeps nothing, nor does a failed
	// turn whose list is not known, nor a turn whose end is not known.
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.Emit(todosEvent(true, more...))
	conv.EmitTurn(agentapi.TurnCompleted, "")
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.Emit(todosEvent(true, left...))
	conv.Emit(todosEvent(false, left...))
	conv.EmitTurn(agentapi.TurnFailed, "boom")
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.Emit(todosEvent(true, more...))
	conv.Exit("gone")
	timings := detail(t, m, sum.ID).TurnTimings
	if len(timings) != 5 || timings[4].State != "unknown" {
		t.Fatalf("timings = %+v", timings)
	}
	for _, timing := range timings[2:] {
		if _, err := m.TurnTodos(sum.ID, timing.ID); timing.Todo != (TodoCounts{}) || statusOf(err) != http.StatusNotFound {
			t.Fatalf("%s turn kept %+v, read %v", timing.State, timing.Todo, err)
		}
	}
}

func TestTurnTodosOneAppendNoSecondWriteAndReload(t *testing.T) {
	m, prov, st := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	sub, _, err := m.Subscribe(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Unsubscribe(sub)
	conv.EmitSubagent(agentapi.Subagent{ID: "sub-1", Name: "explore", Status: agentapi.SubagentRunning})
	conv.EmitTurn(agentapi.TurnWorking, "")
	frameOf(t, sub, "turn_timing") // the turn's start
	conv.Emit(todosEvent(true, agentapi.Todo{ID: "a", Title: "Page", Status: agentapi.TodoDone, AgentID: "sub-1", ChangedAt: time.Unix(10, 0)}, agentapi.Todo{ID: "b", Title: "README", Status: agentapi.TodoPending}))
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	s := m.sessions[sum.ID]
	revision := s.timingRevision
	m.mu.Unlock()
	written := func() os.FileInfo {
		t.Helper()
		info, err := os.Stat(st.Path())
		if err != nil {
			t.Fatal(err)
		}
		return info
	}
	before := written()

	// The turn's end publishes its timing once, counts included, and the
	// same flush appends the rows and writes the counts.
	conv.EmitTurn(agentapi.TurnCompleted, "")
	var published TurnTiming
	decodeField(t, frameOf(t, sub, "turn_timing"), "turn_timing", &published)
	want := TodoCounts{Total: 2, Done: 1, Open: 1}
	m.mu.Lock()
	revised := s.timingRevision - revision
	m.mu.Unlock()
	if published.State != StateCompleted || published.Todo != want || revised != 1 {
		t.Fatalf("published %+v, revisions %d", published, revised)
	}
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(turnTodosPath(m, sum.ID))
	if err != nil || bytes.Count(data, []byte("\n")) != 1 {
		t.Fatalf("turn-todos.jsonl = %q, %v", data, err)
	}
	if info, err := os.Stat(turnTodosPath(m, sum.ID)); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("turn-todos.jsonl mode = %v, %v", info, err)
	}
	once := written()
	if os.SameFile(before, once) {
		t.Fatal("the turn's end did not write sessions.json")
	}
	rec, _ := loadRecord(t, st, prov.Name(), sum.ID)
	if timings := rec.Web.TurnTimings; len(timings) != 1 || timings[0].Todo != want {
		t.Fatalf("persisted timings = %+v", timings)
	}
	// Nothing is left for a second write.
	m.mu.Lock()
	_, dirty := m.dirty[sum.ID]
	queued := len(s.turnTodos)
	m.mu.Unlock()
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	if dirty || queued != 0 || !os.SameFile(once, written()) {
		t.Fatalf("a second write: dirty %v, queued %d", dirty, queued)
	}

	// After a restart the counts come back with the timing and the rows
	// from the file, subagent names included. A stray Task directory goes.
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(m.uploadRoot(), mustUUID(t))
	if err := os.MkdirAll(stray, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stray, turnTodosFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
	m = startManager(t, st, agenttest.NewProvider("fake", allCaps))
	timing := lastTiming(t, m, sum.ID)
	if timing.Todo != want {
		t.Fatalf("reloaded counts = %+v", timing.Todo)
	}
	got, err := m.TurnTodos(sum.ID, timing.ID)
	if err != nil || snapshotIDs(got) != "b,a" || got.Todos[1].Agent != "explore" {
		t.Fatalf("reloaded snapshot = %+v, %v", got, err)
	}
	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Fatalf("stray turn todos survived the sweep: %v", err)
	}
	// Deleting the Task deletes its file.
	if _, err := m.Archive(sum.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(sum.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(turnTodosPath(m, sum.ID)); !os.IsNotExist(err) {
		t.Fatalf("deleted Task's turn todos: %v", err)
	}
}

func TestTurnTodosRouteLastWinsReadOnlyAndArchived(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	sum, conv := createSession(t, ts.m, ts.prov)
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.Emit(todosEvent(true, agentapi.Todo{ID: "a", Title: "Step", Status: agentapi.TodoInProgress, ChangedAt: time.Unix(10, 0)}))
	conv.EmitTurn(agentapi.TurnCompleted, "")
	timing := lastTiming(t, ts.m, sum.ID)
	auth := withCookie(ts)
	get := func(target string) (int, TurnTodos) {
		t.Helper()
		w := ts.do(http.MethodGet, target, "", auth)
		var rec TurnTodos
		if w.Code == http.StatusOK {
			if err := json.Unmarshal(w.Body.Bytes(), &rec); err != nil {
				t.Fatal(err)
			}
		}
		return w.Code, rec
	}
	route := "/api/sessions/" + sum.ID + "/turns/" + timing.ID + "/todos"
	// Before the flush it reads the queued record.
	if code, rec := get(route); code != http.StatusOK || snapshotIDs(rec) != "a" {
		t.Fatalf("queued = %d %+v", code, rec)
	}
	if err := ts.m.flush(); err != nil {
		t.Fatal(err)
	}
	// The last whole record of the turn wins; a torn line does not.
	path := turnTodosPath(ts.m, sum.ID)
	later := TurnTodos{TimingID: timing.ID, EndedAt: timing.EndedAt, Intent: "later", Todos: []snapshotTodo{{Todo: agentapi.Todo{ID: "b", Title: "Other", Status: agentapi.TodoDone}}}, Counts: TodoCounts{Total: 1, Done: 1}}
	line, _ := json.Marshal(later)
	other, _ := json.Marshal(TurnTodos{TimingID: "elsewhere", Todos: []snapshotTodo{}})
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write(append(append(append(line, '\n'), append(other, '\n')...), line[:len(line)/2]...))
	_ = f.Close()
	size := func() int64 {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return info.Size()
	}
	was := size()
	if code, rec := get(route); code != http.StatusOK || rec.Intent != "later" || snapshotIDs(rec) != "b" {
		t.Fatalf("last wins = %d %+v", code, rec)
	}
	// Unknown turns and Tasks are not found; a turn the Task holds without
	// a record neither.
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.EmitTurn(agentapi.TurnCompleted, "")
	for _, target := range []string{"/api/sessions/" + sum.ID + "/turns/elsewhere/todos", "/api/sessions/missing/turns/" + timing.ID + "/todos", "/api/sessions/" + sum.ID + "/turns/" + lastTiming(t, ts.m, sum.ID).ID + "/todos"} {
		if code, _ := get(target); code != http.StatusNotFound {
			t.Fatalf("GET %s = %d", target, code)
		}
	}
	// An archived Task still reads it, and reading changes nothing.
	if _, err := ts.m.Archive(sum.ID); err != nil {
		t.Fatal(err)
	}
	if code, rec := get(route); code != http.StatusOK || rec.Intent != "later" || size() != was {
		t.Fatalf("archived = %d %+v, size %d -> %d", code, rec, was, size())
	}
}

func TestTurnTodosCompactionKeepsHeldTimingsNewestFirst(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	turn := func(title string) TurnTiming {
		t.Helper()
		conv.EmitTurn(agentapi.TurnWorking, "")
		conv.Emit(todosEvent(true, agentapi.Todo{ID: title, Title: title, Status: agentapi.TodoPending, ChangedAt: time.Now()}))
		conv.EmitTurn(agentapi.TurnCompleted, "")
		if err := m.flush(); err != nil {
			t.Fatal(err)
		}
		return lastTiming(t, m, sum.ID)
	}
	first := turn("first")
	// Records of turns the Task no longer holds push the file past its
	// bound; the next append compacts it to the held ones.
	path := turnTodosPath(m, sum.ID)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		_, _ = fmt.Fprintf(f, "{\"timing_id\":\"gone-%d\",\"pad\":%q}\n", i, strings.Repeat("x", 1<<20))
	}
	_ = f.Close()
	second := turn("second")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > keptTurnTodosBytes || bytes.Contains(data, []byte("gone-")) || bytes.Count(data, []byte("\n")) != 2 {
		t.Fatalf("compacted to %d bytes, %d lines", len(data), bytes.Count(data, []byte("\n")))
	}
	for _, timing := range []TurnTiming{first, second} {
		if _, err := m.TurnTodos(sum.ID, timing.ID); err != nil {
			t.Fatalf("held turn %s lost: %v", timing.ID, err)
		}
	}

	// Past keptTurnTodosBytes the oldest held records go; a turn's last
	// record is the one kept.
	timings := []TurnTiming{{ID: "t1"}, {ID: "t2"}, {ID: "t3"}}
	big := strings.Repeat("x", 900<<10)
	var lines []string
	for _, id := range []string{"t1", "t2", "t3", "gone", "t3"} {
		lines = append(lines, fmt.Sprintf("{\"timing_id\":%q,\"pad\":%q}\n", id, big+id))
	}
	lines[4] = strings.Replace(lines[4], "x", "y", 1)
	path = filepath.Join(t.TempDir(), turnTodosFile)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "")+`{"timing_id":"t1","torn`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := compactTurnTodos(path, timings); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != lines[1]+lines[4] {
		t.Fatalf("compacted to %d bytes: %.40q", len(data), data)
	}
}
