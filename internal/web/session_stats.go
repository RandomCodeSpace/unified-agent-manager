package web

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// sessionStatsFile holds the last scan as key=value lines, beside the store.
const sessionStatsFile = "session-stats.txt"

// Rows kept from the per-tool tallies; the rest are left out of the summary.
const (
	sessionStatsTopTools    = 10
	sessionStatsTopFailures = 5
)

// SessionStat is one key=value line. Keys are "<group>.<name>".
type SessionStat struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// SessionStats is GET /api/session-stats: counts over every Copilot session
// journal on this server. It holds no prompt, reply or tool output text.
type SessionStats struct {
	Path  string        `json:"path"`
	Stats []SessionStat `json:"stats"`
}

// journalEvent is the part of an events.jsonl line the counts read.
type journalEvent struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Data      struct {
		ToolCallID string          `json:"toolCallId"`
		ToolName   string          `json:"toolName"`
		Success    *bool           `json:"success"`
		Result     json.RawMessage `json:"result"`
		Reason     string          `json:"reason"`
		ErrorType  string          `json:"errorType"`
	} `json:"data"`
}

type sessionTally struct {
	sessions, withPrompts, skipped       int
	first, last                          string
	events, tools, failures, permissions map[string]int
	errors, aborts                       map[string]int
}

// SessionStats scans every session journal under COPILOT_HOME, writes the
// summary to sessionStatsFile and returns it.
func (m *Manager) SessionStats(ctx context.Context) (SessionStats, error) {
	scope, err := m.configurationScope("")
	if err != nil {
		return SessionStats{}, err
	}
	journals, _ := filepath.Glob(filepath.Join(scope.base, "session-state", "*", "events.jsonl"))
	t := sessionTally{events: map[string]int{}, tools: map[string]int{}, failures: map[string]int{}, permissions: map[string]int{}, errors: map[string]int{}, aborts: map[string]int{}}
	for _, path := range journals {
		if err := ctx.Err(); err != nil {
			return SessionStats{}, err
		}
		t.scan(path)
	}
	out := SessionStats{Path: filepath.Join(filepath.Dir(m.store.Path()), sessionStatsFile), Stats: t.stats(time.Now())}
	var b strings.Builder
	for _, s := range out.Stats {
		b.WriteString(s.Key)
		b.WriteByte('=')
		b.WriteString(s.Value)
		b.WriteByte('\n')
	}
	if err := writeFileAtomic(out.Path, ".session-stats-*", []byte(b.String())); err != nil {
		return SessionStats{}, err
	}
	return out, nil
}

func (t *sessionTally) scan(path string) {
	f, err := os.Open(path) // #nosec G304 -- a journal under the service's own COPILOT_HOME.
	if err != nil {
		t.skipped++
		return
	}
	defer func() { _ = f.Close() }()
	t.sessions++
	prompted := false
	tools := map[string]string{}
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			var e journalEvent
			if json.Unmarshal(line, &e) != nil {
				t.skipped++
			} else {
				prompted = t.add(&e, tools) || prompted
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.skipped++
			}
			break
		}
	}
	if prompted {
		t.withPrompts++
	}
}

// add counts one event; it reports whether the event is a user prompt.
func (t *sessionTally) add(e *journalEvent, tools map[string]string) bool {
	t.events[e.Type]++
	if e.Timestamp != "" {
		if t.first == "" || e.Timestamp < t.first {
			t.first = e.Timestamp
		}
		t.last = max(t.last, e.Timestamp)
	}
	switch e.Type {
	case "tool.execution_start":
		tools[e.Data.ToolCallID] = e.Data.ToolName
		t.tools[e.Data.ToolName]++
	case "tool.execution_complete":
		if e.Data.Success != nil && !*e.Data.Success {
			t.failures[cmp.Or(tools[e.Data.ToolCallID], "unknown")]++
		}
	case "permission.completed":
		var result struct {
			Kind string `json:"kind"`
		}
		_ = json.Unmarshal(e.Data.Result, &result)
		t.permissions[cmp.Or(result.Kind, "unknown")]++
	case "session.error":
		t.errors[cmp.Or(e.Data.ErrorType, "unknown")]++
	case "abort":
		t.aborts[cmp.Or(e.Data.Reason, "unknown")]++
	case "user.message":
		return true
	}
	return false
}

func (t *sessionTally) stats(now time.Time) []SessionStat {
	var out []SessionStat
	add := func(key string, n int) { out = append(out, SessionStat{key, strconv.Itoa(n)}) }
	day := func(ts string) string { return ts[:min(len(ts), 10)] }
	out = append(out, SessionStat{"scan.at", now.UTC().Format(time.RFC3339)})
	add("scan.skipped", t.skipped)
	add("sessions.total", t.sessions)
	add("sessions.with_prompts", t.withPrompts)
	out = append(out, SessionStat{"sessions.first", day(t.first)}, SessionStat{"sessions.last", day(t.last)})
	add("sessions.resumes", t.events["session.resume"])
	add("work.prompts", t.events["user.message"])
	add("work.turns", t.events["assistant.turn_start"])
	add("work.tool_calls", t.events["tool.execution_start"])
	add("work.subagents", t.events["subagent.started"])
	add("work.skills", t.events["skill.invoked"])
	add("work.compactions", t.events["session.compaction_complete"])
	add("work.model_changes", t.events["session.model_change"])
	add("work.mode_changes", t.events["session.mode_changed"])
	add("friction.turns_unfinished", max(0, t.events["assistant.turn_start"]-t.events["assistant.turn_end"]))
	add("friction.tool_failures", sum(t.failures))
	pct := 0
	if calls := t.events["tool.execution_start"]; calls > 0 {
		pct = sum(t.failures) * 100 / calls
	}
	add("friction.tool_failure_pct", pct)
	add("friction.permission_prompts", t.events["permission.requested"])
	add("friction.session_errors", sum(t.errors))
	for _, kv := range ranked(t.aborts, 0) {
		add("aborts."+kv.k, kv.n)
	}
	for _, kv := range ranked(t.errors, 0) {
		add("errors."+kv.k, kv.n)
	}
	for _, kv := range ranked(t.permissions, 0) {
		add("permissions."+kv.k, kv.n)
	}
	for _, kv := range ranked(t.tools, sessionStatsTopTools) {
		add("tools."+kv.k, kv.n)
	}
	for _, kv := range ranked(t.failures, sessionStatsTopFailures) {
		add("tool_failures."+kv.k, kv.n)
	}
	return out
}

type tallyRow struct {
	k string
	n int
}

// ranked is counts biggest first (name breaks ties), at most limit of them (0: all).
func ranked(counts map[string]int, limit int) []tallyRow {
	rows := make([]tallyRow, 0, len(counts))
	for k, n := range counts {
		rows = append(rows, tallyRow{strings.NewReplacer("=", "_", "\n", "_").Replace(k), n})
	}
	slices.SortFunc(rows, func(a, b tallyRow) int { return cmp.Or(b.n-a.n, strings.Compare(a.k, b.k)) })
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows
}

func sum(counts map[string]int) int {
	n := 0
	for _, v := range counts {
		n += v
	}
	return n
}

func (s *Server) handleSessionStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.m.SessionStats(r.Context())
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}
