package web

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// A turn that changed the todo list keeps it as it left it. Its counts go
// on the turn's timing, which the turn's end writes anyway; its rows, too
// many for sessions.json, go to an append-only file beside the Task's
// uploads, one TurnTodos a line, read when a reply's foot opens them.
const (
	turnTodosFile = "turn-todos.jsonl"
	// maxSnapshotTodos bounds the rows a snapshot keeps; its counts count
	// all in scope.
	maxSnapshotTodos      = 50
	maxSnapshotAgentRunes = 64
	// Past maxTurnTodosBytes flush rewrites the file with at most
	// keptTurnTodosBytes of the newest records of turns the Task holds.
	maxTurnTodosBytes  = 4 << 20
	keptTurnTodosBytes = 2 << 20
	// maxTurnTodosLine bounds a line a read takes; a record is far smaller.
	maxTurnTodosLine = 1 << 20
)

// snapshotRank orders a snapshot's rows: blocked, in progress and pending
// before done.
var snapshotRank = map[string]int{agentapi.TodoBlocked: 0, agentapi.TodoInProgress: 1, agentapi.TodoPending: 2, agentapi.TodoDone: 3}

// snapshotTodosLocked keeps the todo list as the ending turn left it, for
// flush to append, and returns its counts for the turn's timing. Its scope
// is the rows the turn changed (against todoBase, as todosTouched compares)
// and the rows still open. A turn that did not touch the list, a list not
// known and an empty scope keep nothing and count zero.
func (m *Manager) snapshotTodosLocked(s *webSession, timing TurnTiming) TodoCounts {
	v, base := s.turnActivity.Todos, s.todoBase
	if !v.Known || !v.Touched {
		return TodoCounts{}
	}
	was := make(map[string]agentapi.Todo, len(base.Todos))
	for _, t := range base.Todos {
		was[t.ID] = t
	}
	since := latestTodoChange(base.Todos)
	changed := func(t agentapi.Todo) bool {
		if !base.Known {
			return t.ChangedAt.After(since)
		}
		b, ok := was[t.ID]
		return !ok || b.Status != t.Status || b.Title != t.Title
	}
	var rows []agentapi.Todo
	for _, t := range v.Todos {
		if t.Status != agentapi.TodoDone || changed(t) {
			rows = append(rows, t)
		}
	}
	if len(rows) == 0 {
		return TodoCounts{}
	}
	slices.SortStableFunc(rows, func(a, b agentapi.Todo) int {
		if r := cmp.Compare(snapshotRank[a.Status], snapshotRank[b.Status]); r != 0 || a.Status != agentapi.TodoDone {
			return r
		}
		return b.ChangedAt.Compare(a.ChangedAt)
	})
	counts := todoCounts(rows)
	if len(rows) > maxSnapshotTodos {
		rows, counts.Omitted = rows[:maxSnapshotTodos], len(rows)-maxSnapshotTodos
	}
	rec := TurnTodos{TimingID: timing.ID, EndedAt: timing.EndedAt, Intent: s.turnIntent, Todos: make([]snapshotTodo, len(rows)), Counts: counts}
	for i, t := range rows {
		rec.Todos[i].Todo = t
		if sa := s.subIdx[t.AgentID]; t.AgentID != "" && sa != nil {
			rec.Todos[i].Agent = clipRunes(sa.Name, maxSnapshotAgentRunes)
		}
	}
	s.turnTodos = append(s.turnTodos, rec)
	return counts
}

// keptTodos is a Task's snapshots for flush to append, with the timings it
// holds, which a compaction keeps the records of.
type keptTodos struct {
	s       *webSession
	records []TurnTodos
	timings []TurnTiming
}

// appendTurnTodos appends a Task's snapshots to its turn-todos.jsonl,
// owner-only, synced once, and compacts the file once it passes
// maxTurnTodosBytes. flush calls it outside mu, before it writes the turn's
// counts; the snapshots stay queued, and readable, until it has appended
// them. A Task deleted meanwhile keeps nothing.
func (m *Manager) appendTurnTodos(k keptTodos) {
	dir := m.taskUploadDir(k.s.id)
	var data []byte
	for _, r := range k.records {
		line, err := json.Marshal(r)
		if err != nil {
			continue
		}
		data = append(append(data, line...), '\n')
	}
	path := filepath.Join(dir, turnTodosFile)
	size, err := appendPrivate(m.uploadRoot(), dir, path, data)
	if err == nil && size > maxTurnTodosBytes {
		err = compactTurnTodos(path, k.timings)
	}
	if err != nil {
		log.Warn("keep the turn's todo list failed", "session", k.s.id, "error", err)
	}
	m.mu.Lock()
	// Only flush, one at a time, takes from the queue's front.
	k.s.turnTodos = slices.Delete(k.s.turnTodos, 0, len(k.records))
	removed := k.s.removed
	m.mu.Unlock()
	if removed {
		removeUploads(dir)
	}
}

// appendPrivate appends data to path, owner-only, creating it and its
// directories, syncs it and returns its size.
func appendPrivate(root, dir, path string, data []byte) (int64, error) {
	if err := privateDirs(root, dir); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600) // #nosec G304 -- UAM's own directory and file name.
	if err != nil {
		return 0, err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	var size int64
	if err == nil {
		var info os.FileInfo
		if info, err = f.Stat(); err == nil {
			size = info.Size()
		}
	}
	return size, errors.Join(err, f.Close())
}

// compactTurnTodos rewrites path with the last record of each turn the
// Task still holds a timing for, newest first until keptTurnTodosBytes,
// in the turns' order.
func compactTurnTodos(path string, timings []TurnTiming) error {
	data, err := os.ReadFile(path) // #nosec G304 -- UAM's own file.
	if err != nil {
		return err
	}
	last := map[string][]byte{}
	for line := range bytes.Lines(data) {
		var r struct {
			TimingID string `json:"timing_id"`
		}
		if bytes.HasSuffix(line, []byte("\n")) && json.Unmarshal(line, &r) == nil {
			last[r.TimingID] = line
		}
	}
	var kept [][]byte
	size := 0
	for _, t := range slices.Backward(timings) {
		line, ok := last[t.ID]
		if !ok {
			continue
		}
		if size+len(line) > keptTurnTodosBytes {
			break
		}
		size += len(line)
		kept = append(kept, line)
	}
	slices.Reverse(kept)
	return writeFileAtomic(path, ".turn-todos-*", bytes.Join(kept, nil))
}

// TurnTodos returns the todo list as the turn timingID of Task id left it.
// It reads settled and archived Tasks alike, and changes nothing.
func (m *Manager) TurnTodos(id, timingID string) (TurnTodos, error) {
	s, err := m.lookup(id)
	if err != nil {
		return TurnTodos{}, err
	}
	m.mu.Lock()
	held := slices.ContainsFunc(s.turnTimings, func(t TurnTiming) bool { return t.ID == timingID })
	i := slices.IndexFunc(s.turnTodos, func(r TurnTodos) bool { return r.TimingID == timingID })
	var queued TurnTodos
	if i >= 0 {
		queued = s.turnTodos[i]
	}
	path := filepath.Join(m.taskUploadDir(s.id), turnTodosFile)
	m.mu.Unlock()
	switch {
	case !held:
	case i >= 0:
		return queued, nil
	default:
		if rec, ok := readTurnTodos(path, timingID); ok {
			return rec, nil
		}
	}
	return TurnTodos{}, newError(http.StatusNotFound, "no todo list was kept for that turn")
}

// readTurnTodos scans path for timingID's records: the last whole one
// wins.
func readTurnTodos(path, timingID string) (TurnTodos, bool) {
	f, err := os.Open(path) // #nosec G304 -- UAM's own file.
	if err != nil {
		return TurnTodos{}, false
	}
	defer func() { _ = f.Close() }()
	key, _ := json.Marshal(timingID)
	prefix := append([]byte(`{"timing_id":`), key...)
	var rec TurnTodos
	found := false
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, maxTurnTodosLine)
	for sc.Scan() {
		var r TurnTodos
		if bytes.HasPrefix(sc.Bytes(), prefix) && json.Unmarshal(sc.Bytes(), &r) == nil {
			rec, found = r, true
		}
	}
	return rec, found
}

func (s *Server) handleTurnTodos(w http.ResponseWriter, r *http.Request) {
	rec, err := s.m.TurnTodos(r.PathValue("id"), r.PathValue("timing_id"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}
