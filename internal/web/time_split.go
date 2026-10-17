package web

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"time"
)

// Rows kept in TimeSplit.Tools, longest first.
const timeSplitTools = 64

// timeSplitCacheSize bounds the cached splits; a full cache starts over.
const timeSplitCacheSize = 256

// TimeSplit is GET /api/sessions/{id}/time-split: where a Thread's time went,
// read from its Copilot session journal. Model time is the sum of the model
// calls' own durations (main agent and subagents). Local time is the wall
// time tools ran, parallel runs counted once, without the time spent waiting
// on the owner and without time a model call was running. Waiting time is
// reported apart and is in neither. Tools that only wrap subagents are left
// out: their model calls already count.
type TimeSplit struct {
	// ModelMS is nil when the journal records no model call durations (CLIs
	// before session.usage_record).
	ModelMS    *int64 `json:"model_ms"`
	ModelCalls int    `json:"model_calls"`
	LocalMS    int64  `json:"local_ms"`
	LocalRuns  int    `json:"local_runs"`
	WaitingMS  int64  `json:"waiting_ms"`
	// Waits counts permission requests, ask_user questions and elicitations,
	// answered at once or not.
	Waits int `json:"waits"`
	// Tools is each tool's own run time without waiting, longest first; runs
	// overlapping each other or a model call count in full, so the rows may
	// add up to more than LocalMS.
	Tools []TimeSplitTool `json:"tools"`
}

// TimeSplitTool is one tool's row in TimeSplit.
type TimeSplitTool struct {
	Name string `json:"name"`
	MS   int64  `json:"ms"`
	Runs int    `json:"runs"`
}

// subagentTools run or talk to subagents; their time is the subagents' own.
var subagentTools = map[string]bool{"task": true, "read_agent": true, "write_agent": true, "list_agents": true}

// waitingTools wait on the owner for their whole run.
var waitingTools = map[string]bool{"ask_user": true}

// TaskTimeSplit reads the split from the Thread's journal; it works for
// closed, settled and archived Threads too. A Thread without a journal yet
// has an empty split.
func (m *Manager) TaskTimeSplit(ctx context.Context, id string) (*TimeSplit, error) {
	s, err := m.lookup(id)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	dir := m.sessionStateDirLocked(s)
	m.mu.Unlock()
	if dir == "" {
		return &TimeSplit{Tools: []TimeSplitTool{}}, nil
	}
	split, err := m.timeSplits.read(ctx, filepath.Join(dir, "events.jsonl"))
	if errors.Is(err, fs.ErrNotExist) {
		return &TimeSplit{Tools: []TimeSplitTool{}}, nil
	}
	if err != nil {
		return nil, newError(http.StatusInternalServerError, "the Thread's session journal could not be read")
	}
	return &split, nil
}

func (s *Server) handleTaskTimeSplit(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	out, err := s.m.TaskTimeSplit(r.Context(), r.PathValue("id"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// timeSplitCache keeps each journal's split until its size or modification
// time changes; journals reach several MB.
type timeSplitCache struct {
	mu      sync.Mutex
	entries map[string]timeSplitEntry
}

type timeSplitEntry struct {
	size  int64
	mod   time.Time
	split TimeSplit
}

func (c *timeSplitCache) read(ctx context.Context, path string) (TimeSplit, error) {
	f, err := os.Open(path) // #nosec G304 -- a journal under the service's own COPILOT_HOME.
	if err != nil {
		return TimeSplit{}, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return TimeSplit{}, err
	}
	c.mu.Lock()
	e, ok := c.entries[path]
	c.mu.Unlock()
	if ok && e.size == info.Size() && e.mod.Equal(info.ModTime()) {
		return e.split, nil
	}
	split, err := readTimeSplit(ctx, io.LimitReader(f, info.Size()))
	if err != nil {
		return TimeSplit{}, err
	}
	c.mu.Lock()
	if c.entries == nil || len(c.entries) >= timeSplitCacheSize {
		c.entries = map[string]timeSplitEntry{}
	}
	c.entries[path] = timeSplitEntry{size: info.Size(), mod: info.ModTime(), split: split}
	c.mu.Unlock()
	return split, nil
}

// splitEvent is the part of an events.jsonl line the split reads.
type splitEvent struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	AgentID   string `json:"agentId"`
	Data      struct {
		ToolCallID string `json:"toolCallId"`
		ToolName   string `json:"toolName"`
		RequestID  string `json:"requestId"`
		Usage      struct {
			Duration *float64 `json:"duration"`
		} `json:"usage"`
	} `json:"data"`
}

// span is [from, to) in Unix milliseconds.
type span struct{ from, to int64 }

type toolRun struct {
	name string
	span
}

type splitFold struct {
	model, waits []span
	runs         []toolRun
	modelCalls   int
	waitCount    int
	recorded     bool
	open         map[[2]string]toolRun // running tools by agent and call
	asking       map[string]int64      // owner requests by request ID, from
	last         int64                 // the latest event's time
}

// readTimeSplit folds one journal. Lines that do not parse are skipped.
// Open runs and requests end at session.shutdown and abort; at a
// session.resume (a CLI that stopped without shutting down) waiting ends at
// the resume, while a tool counts only to the last event the old CLI wrote,
// so the time it was down never counts as local; at the journal's end they
// end at its last event.
func readTimeSplit(ctx context.Context, r io.Reader) (TimeSplit, error) {
	f := splitFold{open: map[[2]string]toolRun{}, asking: map[string]int64{}}
	br := bufio.NewReader(r)
	for n := 0; ; n++ {
		if n%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return TimeSplit{}, err
			}
		}
		line, err := br.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			var e splitEvent
			if json.Unmarshal(line, &e) == nil {
				f.add(&e)
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return TimeSplit{}, err
		}
	}
	f.end(f.last, f.last)
	return f.split(), nil
}

func (f *splitFold) add(e *splitEvent) {
	t, err := time.Parse(time.RFC3339Nano, e.Timestamp)
	if err != nil {
		return
	}
	at := t.UnixMilli()
	prev := f.last
	f.last = max(f.last, at)
	d := &e.Data
	switch e.Type {
	case "session.usage_record":
		f.recorded = true
		if d.Usage.Duration != nil && *d.Usage.Duration >= 0 {
			f.modelCalls++
			f.model = append(f.model, span{at - int64(*d.Usage.Duration), at})
		}
	case "tool.execution_start":
		if !subagentTools[d.ToolName] {
			f.open[[2]string{e.AgentID, d.ToolCallID}] = toolRun{d.ToolName, span{at, at}}
		}
	case "tool.execution_complete":
		key := [2]string{e.AgentID, d.ToolCallID}
		if run, ok := f.open[key]; ok {
			delete(f.open, key)
			f.finish(run, at)
		}
	case "permission.requested", "elicitation.requested":
		f.asking[d.RequestID] = at
	case "permission.completed", "elicitation.completed":
		if from, ok := f.asking[d.RequestID]; ok {
			delete(f.asking, d.RequestID)
			f.wait(from, at)
		}
	case "session.shutdown", "abort":
		f.end(at, at)
	case "session.resume":
		f.end(prev, at)
	}
}

// end closes every open tool run at tools and every open wait at waits.
func (f *splitFold) end(tools, waits int64) {
	for key, run := range f.open {
		delete(f.open, key)
		if waitingTools[run.name] {
			f.finish(run, waits)
		} else {
			f.finish(run, tools)
		}
	}
	for id, from := range f.asking {
		delete(f.asking, id)
		f.wait(from, waits)
	}
}

func (f *splitFold) finish(run toolRun, at int64) {
	run.to = max(run.from, at)
	if waitingTools[run.name] {
		f.wait(run.from, run.to)
		return
	}
	f.runs = append(f.runs, run)
}

func (f *splitFold) wait(from, to int64) {
	f.waitCount++
	if to > from {
		f.waits = append(f.waits, span{from, to})
	}
}

func (f *splitFold) split() TimeSplit {
	out := TimeSplit{ModelCalls: f.modelCalls, LocalRuns: len(f.runs), Waits: f.waitCount, Tools: []TimeSplitTool{}}
	model, waits := spanUnion(f.model), spanUnion(f.waits)
	if f.recorded {
		var total int64
		for _, s := range f.model {
			total += s.to - s.from
		}
		out.ModelMS = &total
	}
	out.WaitingMS = spanLength(waits)
	all := make([]span, 0, len(f.runs))
	byTool := map[string]*TimeSplitTool{}
	for _, run := range f.runs {
		all = append(all, run.span)
		row := byTool[run.name]
		if row == nil {
			row = &TimeSplitTool{Name: clipRunes(run.name, 128)}
			byTool[run.name] = row
		}
		row.Runs++
		row.MS += run.to - run.from - spanOverlap(run.span, waits)
	}
	out.LocalMS = spanLength(spanMinus(spanMinus(spanUnion(all), waits), model))
	for _, row := range byTool {
		out.Tools = append(out.Tools, *row)
	}
	slices.SortFunc(out.Tools, func(a, b TimeSplitTool) int {
		return cmp.Or(cmp.Compare(b.MS, a.MS), cmp.Compare(b.Runs, a.Runs), cmp.Compare(a.Name, b.Name))
	})
	if len(out.Tools) > timeSplitTools {
		out.Tools = out.Tools[:timeSplitTools]
	}
	return out
}

// spanUnion merges spans into sorted, disjoint, non-empty spans.
func spanUnion(spans []span) []span {
	in := slices.Clone(spans)
	slices.SortFunc(in, func(a, b span) int { return cmp.Compare(a.from, b.from) })
	var out []span
	for _, s := range in {
		if s.to <= s.from {
			continue
		}
		if n := len(out); n > 0 && s.from <= out[n-1].to {
			out[n-1].to = max(out[n-1].to, s.to)
			continue
		}
		out = append(out, s)
	}
	return out
}

// spanMinus is a without b; both are unions.
func spanMinus(a, b []span) []span {
	var out []span
	j := 0
	for _, s := range a {
		for j < len(b) && b[j].to <= s.from {
			j++
		}
		from := s.from
		for k := j; k < len(b) && b[k].from < s.to; k++ {
			if b[k].from > from {
				out = append(out, span{from, b[k].from})
			}
			from = max(from, b[k].to)
		}
		if from < s.to {
			out = append(out, span{from, s.to})
		}
	}
	return out
}

// spanOverlap is how much of s the union b covers.
func spanOverlap(s span, b []span) int64 {
	var n int64
	for k := sort.Search(len(b), func(i int) bool { return b[i].to > s.from }); k < len(b) && b[k].from < s.to; k++ {
		n += min(s.to, b[k].to) - max(s.from, b[k].from)
	}
	return n
}

func spanLength(spans []span) int64 {
	var n int64
	for _, s := range spans {
		n += s.to - s.from
	}
	return n
}
