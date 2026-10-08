package web

import (
	"slices"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
)

// Bounds of the activity texts, in runes.
const (
	maxIntentRunes      = 160
	maxRetryReasonRunes = 64
	maxTodoTitleRunes   = 200
	maxTodoNoteRunes    = 300
)

// maxTodoRows bounds the todo rows a TodoView holds; its counts count all.
const maxTodoRows = 100

// Why a turn was cancelled (SessionSummary.StopReason).
const (
	stopOwner       = "owner"
	stopTimeLimit   = "time_limit"
	stopCreditLimit = "credit_limit"
	stopRemote      = "remote"
	stopMCP         = "mcp"
	stopCLI         = "cli"
)

// providerStops maps the provider's reasons for an aborted turn to a stop
// reason and the cancelled state's detail. An unknown reason gives none.
var providerStops = map[string]struct{ code, detail string }{
	"autopilot_credit_limit": {stopCreditLimit, "Autopilot stopped at its credit limit"},
	"remote_command":         {stopRemote, "Stopped by a remote command"},
	"user_abort":             {stopMCP, "Stopped by an MCP server"},
	"user_initiated":         {stopCLI, "Stopped in the Copilot CLI"},
}

// stopCause says why a turn was cancelled: uam's own stop first (the
// owner's Stop, a routine's time limit), whatever the provider reports,
// then the provider's reason.
func (s *webSession) stopCause(providerReason string) (code, detail string) {
	if s.stopBy != "" {
		return s.stopBy, s.stopReason
	}
	p := providerStops[providerReason]
	return p.code, p.detail
}

// shownStopReason is the summary's StopReason: why the last turn was
// cancelled, while the Task is.
func (s *webSession) shownStopReason() string {
	if s.base != StateCancelled {
		return ""
	}
	return s.stopCode
}

// turnActivityTurnLocked starts the activity afresh when a turn starts and
// when it ends, so a turn never shows the one before it; a turn's later
// model calls (TurnWorking again) keep it. The todo list stays: a starting
// turn takes it as found, untouched. It runs before observeTurnTimingLocked
// opens the turn's timing.
func (m *Manager) turnActivityTurnLocked(s *webSession, state agentapi.TurnState) {
	todos := s.turnActivity.Todos
	if state == agentapi.TurnWorking {
		s.stopCode = ""
		if s.activeTiming >= 0 {
			return
		}
		s.todoBase, todos.Touched = todos, false
	}
	m.setTurnActivityLocked(s, TurnActivity{Todos: todos})
}

// applyActivityLocked takes the adapter's activity of the running turn. It
// is published, never persisted: an intent or a retry writes nothing.
func (m *Manager) applyActivityLocked(s *webSession, a agentapi.Activity) {
	next := TurnActivity{Intent: clipRunes(strings.TrimSpace(displaytext.Sanitize(a.Intent)), maxIntentRunes), Todos: s.turnActivity.Todos}
	if a.Retry != nil {
		r := cleanRetry(*a.Retry)
		next.Retry = &r
	}
	m.setTurnActivityLocked(s, next)
}

func (m *Manager) setTurnActivityLocked(s *webSession, a TurnActivity) {
	cur := s.turnActivity
	sameRetry := a.Retry == cur.Retry || a.Retry != nil && cur.Retry != nil && *a.Retry == *cur.Retry
	if a.Intent == cur.Intent && sameRetry && a.Todos.equal(cur.Todos) {
		return
	}
	s.turnActivity = a
	m.broadcastLocked("turn_activity", s.id, func(seq uint64) any {
		return turnActivityEvent{Seq: seq, SessionID: s.id, TurnActivity: a}
	})
}

// cleanRetry bounds a provider's retry report.
func cleanRetry(r agentapi.Retry) agentapi.Retry {
	r.Count = max(r.Count, 0)
	r.Reason = clipRunes(strings.TrimSpace(displaytext.Sanitize(r.Reason)), maxRetryReasonRunes)
	if r.Status < 100 || r.Status > 599 {
		r.Status = 0
	}
	return r
}

// applyTodosLocked takes the conversation's todo list. Like the activity it
// is published, never persisted. Only a running turn touches it: changes
// between turns (subagents that outlived the last) do not.
func (m *Manager) applyTodosLocked(s *webSession, list agentapi.TodoList) {
	next := s.turnActivity
	touched := next.Todos.Touched
	next.Todos = todoView(list)
	next.Todos.Touched = touched || (s.activeTiming >= 0 && list.Known && todosTouched(s.todoBase, next.Todos))
	m.setTurnActivityLocked(s, next)
}

// forgetTodosLocked marks the list unknown while no conversation is open
// to read it.
func (m *Manager) forgetTodosLocked(s *webSession) {
	if !s.turnActivity.Todos.Known {
		return
	}
	next := s.turnActivity
	next.Todos.Known = false
	m.setTurnActivityLocked(s, next)
}

// todoView bounds a provider's list for the browser.
func todoView(list agentapi.TodoList) TodoView {
	rows := make([]agentapi.Todo, 0, len(list.Todos))
	for _, t := range list.Todos {
		rows = append(rows, cleanTodo(t))
	}
	v := TodoView{Known: list.Known, Counts: todoCounts(rows)}
	if len(rows) > maxTodoRows {
		rows, v.Omitted = rows[:maxTodoRows:maxTodoRows], len(rows)-maxTodoRows
	}
	v.Todos, v.Now = rows, todoNow(rows)
	return v
}

func cleanTodo(t agentapi.Todo) agentapi.Todo {
	t.ID, t.AgentID = clampText(t.ID, maxLabelText), clampText(t.AgentID, maxLabelText)
	t.Title = clipRunes(strings.TrimSpace(displaytext.Sanitize(t.Title)), maxTodoTitleRunes)
	switch t.Status {
	case agentapi.TodoInProgress, agentapi.TodoDone, agentapi.TodoBlocked:
	default:
		t.Status = agentapi.TodoPending
	}
	if t.Status == agentapi.TodoBlocked {
		t.Note = clipRunes(strings.TrimSpace(displaytext.Sanitize(t.Note)), maxTodoNoteRunes)
	} else {
		t.Note = ""
	}
	return t
}

// todoCounts counts rows by status: Open is pending and in progress.
func todoCounts(rows []agentapi.Todo) TodoCounts {
	c := TodoCounts{Total: len(rows)}
	for _, t := range rows {
		switch t.Status {
		case agentapi.TodoDone:
			c.Done++
		case agentapi.TodoBlocked:
			c.Blocked++
		default:
			c.Open++
		}
	}
	return c
}

// todoNow is the id of the row the work is at: the row in progress whose
// status changed last, the main agent's before a subagent's changed at the
// same time, then the first in the provider's order. "" when none is in
// progress.
func todoNow(rows []agentapi.Todo) string {
	now := -1
	for i, t := range rows {
		if t.Status != agentapi.TodoInProgress {
			continue
		}
		if now < 0 || t.ChangedAt.After(rows[now].ChangedAt) || (t.ChangedAt.Equal(rows[now].ChangedAt) && t.AgentID == "" && rows[now].AgentID != "") {
			now = i
		}
	}
	if now < 0 {
		return ""
	}
	return rows[now].ID
}

// todosTouched reports whether next differs from base, the list as the turn
// found it. When base was not known, as when the list read on reopening the
// conversation arrives after the turn started, only a status change newer
// than any in base counts.
func todosTouched(base, next TodoView) bool {
	if !base.Known {
		return latestTodoChange(next.Todos).After(latestTodoChange(base.Todos))
	}
	return base.Counts != next.Counts || !slices.EqualFunc(base.Todos, next.Todos, func(a, b agentapi.Todo) bool {
		return a.ID == b.ID && a.Status == b.Status && a.Title == b.Title
	})
}

func latestTodoChange(rows []agentapi.Todo) time.Time {
	var at time.Time
	for _, t := range rows {
		if t.ChangedAt.After(at) {
			at = t.ChangedAt
		}
	}
	return at
}

func (v TodoView) equal(o TodoView) bool {
	return v.Known == o.Known && v.Touched == o.Touched && v.Omitted == o.Omitted && v.Counts == o.Counts && v.Now == o.Now && slices.Equal(v.Todos, o.Todos)
}
