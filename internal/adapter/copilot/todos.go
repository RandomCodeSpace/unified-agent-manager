package copilot

import (
	"context"
	"slices"
	"strings"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// The agents keep the todo list in the session's SQL `todos` table with the
// sql tool (taskSystem and subagentSystem ask them to). session.todos_changed
// says a statement changed it and which agent ran it, without the rows, so
// they are read through the experimental plan RPCs.
const (
	// todosDebounce gathers the changes of a burst into one read.
	todosDebounce = 250 * time.Millisecond
	// A list is cut at maxTodos rows, its texts at these rune counts.
	maxTodos          = 200
	maxTodoTitleRunes = 200
	maxTodoNoteRunes  = 300
	// todosTurnWait bounds how long a turn's end waits for the read of the
	// list it left.
	todosTurnWait = time.Second
)

// todoReads is the conversation's todo list and the reads that keep it.
type todoReads struct {
	// last is the list last emitted; nil before the first.
	last *agentapi.TodoList
	// rows are the last read's rows by id, with their owner and the time
	// their status last changed.
	rows map[string]agentapi.Todo
	// pending holds the changes since the last read started; flight those
	// since the running read started, which it may or may not see.
	pending, flight todoWindow
	timer           *time.Timer
	// reading is set while a read runs; again asks it for one more.
	reading, again bool
	// held is a turn's end waiting for the read of the list it left, and
	// release emits it if that read stalls (emitTurnEndLocked).
	held    *agentapi.Turn
	release *time.Timer
}

// todoWindow is the agents that changed the list in a span ("" for the
// main agent) and the time of the latest change.
type todoWindow struct {
	agents map[string]bool
	at     time.Time
}

func (w *todoWindow) add(agentID string, at time.Time) {
	if w.agents == nil {
		w.agents = map[string]bool{}
	}
	w.agents[agentID] = true
	if at.After(w.at) {
		w.at = at
	}
}

// owner is the agent a row first seen after the window wrote: the only one
// that changed the list in it, else none rather than a guess.
func (w todoWindow) owner() string {
	if len(w.agents) != 1 {
		return ""
	}
	for id := range w.agents {
		return id
	}
	return ""
}

// observeTodosLocked schedules a read of the list for session.todos_changed
// and reports whether ev was one. Reads wait todosDebounce after the first
// change they gather.
func (c *conversation) observeTodosLocked(ev copilot.SessionEvent, agentID string) bool {
	if _, ok := ev.Data.(*rpc.SessionTodosChangedData); !ok {
		return false
	}
	t := &c.todos
	t.pending.add(agentID, ev.Timestamp)
	if t.reading {
		t.flight.add(agentID, ev.Timestamp)
	}
	if t.timer == nil {
		t.timer = time.AfterFunc(todosDebounce, func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.todos.timer = nil
			c.checkTodosLocked()
		})
	}
	return true
}

// checkTodosLocked reads the list on its own goroutine, as checkTasksLocked
// reads the task list: one read at a time; asking during it adds one more.
func (c *conversation) checkTodosLocked() {
	t := &c.todos
	switch {
	case c.closed || c.sess == nil:
	case t.reading:
		t.again = true
	default:
		t.reading = true
		go c.readTodos(c.sess)
	}
}

func (c *conversation) readTodos(sess sdkSession) {
	for {
		c.mu.Lock()
		t := &c.todos
		t.flight, t.pending = t.pending, todoWindow{}
		// A turn end held before this read started waits for its list.
		held := t.held
		c.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), webTasksTimeout)
		rows, err := sess.ReadTodos(ctx)
		cancel()
		c.mu.Lock()
		switch {
		case c.closed:
		case err != nil:
			log.Debug("copilot todo read failed", "session", c.id, "error", errText(err))
			if t.last != nil && t.last.Known {
				list := *t.last
				list.Known = false
				c.emitTodosLocked(list)
			}
		default:
			c.emitTodosLocked(t.apply(rows))
		}
		if held != nil && t.held == held {
			c.releaseTurnLocked()
		}
		t.flight = todoWindow{}
		again := t.again && !c.closed
		t.reading, t.again = again, false
		c.mu.Unlock()
		if !again {
			return
		}
	}
}

// apply maps a read's rows, in the provider's order, and remembers them. A
// row's owner is fixed when it is first seen; its ChangedAt moves to the
// latest change of the read's window when its status differs from the last
// read. Rows first seen by a read without changes, as on opening the
// conversation, get neither.
func (t *todoReads) apply(rows []rpc.PlanSQLTodosRow) agentapi.TodoList {
	list := agentapi.TodoList{Known: true, Todos: make([]agentapi.Todo, 0, min(len(rows), maxTodos))}
	seen := make(map[string]agentapi.Todo, len(list.Todos))
	for _, row := range rows {
		todo := todoOf(row)
		if _, dup := seen[todo.ID]; todo.ID == "" || dup {
			continue
		}
		if len(list.Todos) == maxTodos {
			break
		}
		if prev, ok := t.rows[todo.ID]; ok {
			todo.AgentID, todo.ChangedAt = prev.AgentID, prev.ChangedAt
			if prev.Status != todo.Status {
				todo.ChangedAt = t.flight.at
			}
		} else {
			todo.AgentID, todo.ChangedAt = t.flight.owner(), t.flight.at
		}
		seen[todo.ID] = todo
		list.Todos = append(list.Todos, todo)
	}
	t.rows = seen
	return list
}

// todoOf maps a row: a missing or unknown status is pending, the column's
// default; only a blocked row keeps its description, as the reason.
func todoOf(row rpc.PlanSQLTodosRow) agentapi.Todo {
	todo := agentapi.Todo{
		ID:     clipRuneCount(orEmpty(row.ID), maxTodoTitleRunes),
		Title:  clipRuneCount(strings.TrimSpace(displaytext.Sanitize(orEmpty(row.Title))), maxTodoTitleRunes),
		Status: agentapi.TodoPending,
	}
	switch status := orEmpty(row.Status); status {
	case agentapi.TodoInProgress, agentapi.TodoDone, agentapi.TodoBlocked:
		todo.Status = status
	}
	if todo.Status == agentapi.TodoBlocked {
		todo.Note = clipRuneCount(strings.TrimSpace(displaytext.Sanitize(orEmpty(row.Description))), maxTodoNoteRunes)
	}
	return todo
}

// emitTodosLocked emits list unless it is the one last emitted.
func (c *conversation) emitTodosLocked(list agentapi.TodoList) {
	t := &c.todos
	if t.last != nil && t.last.Known == list.Known && slices.Equal(t.last.Todos, list.Todos) {
		return
	}
	t.last = &list
	snapshot := list
	c.emitLocked(agentapi.Event{Kind: agentapi.EventTodos, Todos: &snapshot})
}

// emitTurnEndLocked emits a turn's end once the todo list is as the turn
// left it, so the Manager keeps that list with the turn. With changes no
// read has started on, or a read running that may have missed some, it
// holds the turn and reads at once, past the debounce; the read emits the
// list, then the turn. todosTurnWait releases it if the read stalls, and so
// do the next turn's start, Close and exitLocked. The read runs on its own
// goroutine: this runs on the SDK's event goroutine.
func (c *conversation) emitTurnEndLocked(turn agentapi.Turn) {
	t := &c.todos
	if len(t.pending.agents) == 0 && !t.reading {
		c.emitLocked(agentapi.Event{Kind: agentapi.EventTurn, Turn: &turn})
		return
	}
	c.releaseTurnLocked()
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
	var release *time.Timer
	release = time.AfterFunc(todosTurnWait, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.todos.release == release {
			c.releaseTurnLocked()
		}
	})
	t.held, t.release = &turn, release
	c.checkTodosLocked()
}

// releaseTurnLocked emits the held turn end, if any.
func (c *conversation) releaseTurnLocked() {
	t := &c.todos
	if t.held == nil {
		return
	}
	turn := t.held
	t.release.Stop()
	t.held, t.release = nil, nil
	c.emitLocked(agentapi.Event{Kind: agentapi.EventTurn, Turn: turn})
}

// ReadTodos reads the session's todo rows through the experimental plan
// RPCs: with their dependencies, else, from a CLI that refuses that, the
// rows alone.
func (a sdkSessionAdapter) ReadTodos(ctx context.Context) ([]rpc.PlanSQLTodosRow, error) {
	res, err := a.s.RPC.Plan.ReadSqlTodosWithDependencies(ctx)
	if err == nil {
		return res.Rows, nil
	}
	if !isRPCError(err) {
		return nil, err
	}
	rows, err := a.s.RPC.Plan.ReadSqlTodos(ctx)
	if err != nil {
		return nil, err
	}
	return rows.Rows, nil
}
