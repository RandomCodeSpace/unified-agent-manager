package web

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// Every Utility model call, whatever asks for it, goes through runUtility:
// it is refused once the day's limit in Settings is reached and recorded in
// the Utility log, kept in utilityLogFile beside the store for
// utilityRetentionDays local days.
const (
	utilityLogFile       = "utility-log.jsonl"
	utilityRetentionDays = 30
	// maxUtilityCalls bounds the log however many calls are skipped.
	maxUtilityCalls = 100_000
	// utilityPage is the calls a GET /api/utility page holds at most.
	utilityPage = 200
	// charsPerToken estimates tokens when the provider reports none.
	charsPerToken = 4
)

// The purposes of Utility calls.
const (
	purposeTitle           = "title"
	purposeSubagentSummary = "subagent-summary"
	purposePlannerTriage   = "planner-triage"
	purposePlannerSuggest  = "planner-suggest"
	purposeCommitMessage   = "commit-message"
)

// The outcomes of Utility calls, and why one was skipped.
const (
	utilityOK      = "ok"
	utilityError   = "error"
	utilitySkipped = "skipped"

	skippedLimit = "daily_limit"
	skippedOff   = "off"
)

const codeUtilityPaused = "utility_paused"

// UtilityCall is one Utility model call in the log. At is when it started;
// Day is its server-local date, set when the log is read. Tokens are the
// provider's figures unless Estimated, when they are worked out from the
// characters. Reason is why a call was skipped (daily_limit or off) or
// failed.
type UtilityCall struct {
	ID           int64     `json:"id"`
	At           time.Time `json:"at"`
	Day          string    `json:"day,omitempty"`
	Purpose      string    `json:"purpose"`
	Provider     string    `json:"provider,omitempty"`
	Model        string    `json:"model,omitempty"`
	TaskID       string    `json:"task_id,omitempty"`
	ProjectID    string    `json:"project_id,omitempty"`
	PromptChars  int       `json:"prompt_chars"`
	ReplyChars   int       `json:"reply_chars"`
	InputTokens  int64     `json:"input_tokens"`
	OutputTokens int64     `json:"output_tokens"`
	Estimated    bool      `json:"estimated,omitempty"`
	Credits      float64   `json:"credits,omitempty"`
	DurationMS   int64     `json:"duration_ms"`
	Outcome      string    `json:"outcome"`
	Reason       string    `json:"reason,omitempty"`
}

// UtilityToday is today's use against the limit. Paused is set once no
// more calls run today; ResetsAt is the server's next local midnight.
type UtilityToday struct {
	Day      string    `json:"day"`
	Calls    int       `json:"calls"`
	Limit    int       `json:"limit"`
	Paused   bool      `json:"paused"`
	ResetsAt time.Time `json:"resets_at"`
}

// UtilityDay totals one day of the log. Estimated is set when some of its
// tokens are estimates.
type UtilityDay struct {
	Day          string  `json:"day"`
	Calls        int     `json:"calls"`
	Errors       int     `json:"errors"`
	Skipped      int     `json:"skipped"`
	PromptChars  int64   `json:"prompt_chars"`
	ReplyChars   int64   `json:"reply_chars"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	Estimated    bool    `json:"estimated,omitempty"`
	Credits      float64 `json:"credits,omitempty"`
}

// UtilityLog is the GET /api/utility response: today, every day kept with
// its totals, and a page of calls, newest first. Next, when set, is the
// before value of the next page.
type UtilityLog struct {
	Today UtilityToday  `json:"today"`
	Days  []UtilityDay  `json:"days"`
	Calls []UtilityCall `json:"calls"`
	Next  int64         `json:"next,omitempty"`
}

// utilityLog is the Utility log and today's count. Calls are in the order
// they ended, which is ID order. started counts the calls begun on day,
// running ones included.
type utilityLog struct {
	mu      sync.Mutex
	path    string
	calls   []UtilityCall
	nextID  int64
	day     string
	started int
}

func dayOf(t time.Time) string { return t.Local().Format(time.DateOnly) }

func midnightAfter(t time.Time) time.Time {
	y, mo, d := t.Local().Date()
	return time.Date(y, mo, d+1, 0, 0, 0, 0, time.Local)
}

// utilityCutoff is the start of the oldest day kept.
func utilityCutoff(now time.Time) time.Time {
	y, mo, d := now.Local().Date()
	return time.Date(y, mo, d-utilityRetentionDays+1, 0, 0, 0, 0, time.Local)
}

// utilityLimitLocked is the daily limit in Settings. The caller holds mu.
func (m *Manager) utilityLimitLocked() int {
	if l := m.settings.UtilityDailyLimit; l != nil {
		return *l
	}
	return store.DefaultUtilityDailyLimit
}

// pausedError says why a call was skipped.
func pausedError(reason string, limit int) *Error {
	msg := "Background AI is off: its daily limit is 0. Raise it in Settings → Background AI"
	if reason == skippedLimit {
		msg = fmt.Sprintf("Background AI is paused until tomorrow: today's %d calls are used. Raise the limit in Settings → Background AI", limit)
	}
	return &Error{Status: http.StatusConflict, Code: codeUtilityPaused, Message: msg}
}

// runUtility makes one Utility call through run, which passes the usage
// callback on to the provider, unless today's limit is reached; then it
// returns a codeUtilityPaused error without calling run. Either way the call
// is logged. call names the purpose, provider, model and Task or Project;
// prompt is the text sent.
func (m *Manager) runUtility(ctx context.Context, call UtilityCall, prompt string, run func(context.Context, func(agentapi.UtilityUsage)) (string, error)) (string, error) {
	m.mu.Lock()
	limit := m.utilityLimitLocked()
	m.mu.Unlock()
	call.At = m.now()
	if reason := m.utility.reserve(call.At, limit); reason != "" {
		call.Outcome, call.Reason = utilitySkipped, reason
		m.utility.add(call)
		return "", pausedError(reason, limit)
	}
	var usage *agentapi.UtilityUsage
	start := time.Now()
	reply, err := run(ctx, func(u agentapi.UtilityUsage) { usage = &u })
	call.DurationMS = time.Since(start).Milliseconds()
	call.PromptChars, call.ReplyChars = utf8.RuneCountInString(prompt), utf8.RuneCountInString(reply)
	if usage != nil {
		call.InputTokens, call.OutputTokens, call.Credits = usage.InputTokens, usage.OutputTokens, usage.Credits
	} else {
		call.Estimated = true
		call.InputTokens = int64((call.PromptChars + charsPerToken - 1) / charsPerToken)
		call.OutputTokens = int64((call.ReplyChars + charsPerToken - 1) / charsPerToken)
	}
	call.Outcome = utilityOK
	if err != nil {
		call.Outcome, call.Reason = utilityError, clipRunes(displaytext.Sanitize(err.Error()), maxDetailRunes)
	}
	m.utility.add(call)
	return reply, err
}

// reserve counts a call starting at now, or says why it may not start.
func (l *utilityLog) reserve(now time.Time, limit int) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rollLocked(now)
	switch {
	case limit <= 0:
		return skippedOff
	case l.started >= limit:
		return skippedLimit
	}
	l.started++
	return ""
}

// rollLocked starts the count afresh on a new day. The caller holds mu.
func (l *utilityLog) rollLocked(now time.Time) {
	if day := dayOf(now); day != l.day {
		l.day, l.started = day, 0
	}
}

// add logs call, appending it to the file, and drops the days past
// retention once a day.
func (l *utilityLog) add(call UtilityCall) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.nextID++
	call.ID, call.Day = l.nextID, ""
	l.calls = append(l.calls, call)
	cutoff := utilityCutoff(call.At)
	if l.calls[0].At.Before(cutoff) || len(l.calls) > maxUtilityCalls {
		l.pruneLocked(cutoff)
		l.rewriteLocked()
		return
	}
	if l.path == "" {
		return
	}
	line, err := json.Marshal(call)
	if err != nil {
		return
	}
	f, err := os.OpenFile(l.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err == nil {
		_, err = f.Write(append(line, '\n'))
		err = errors.Join(err, f.Close())
	}
	if err != nil {
		log.Warn("append to the utility log failed", "error", err)
	}
}

// pruneLocked drops calls from before cutoff, and past maxUtilityCalls the
// oldest tenth more, so the file is not rewritten on every call. The caller
// holds mu.
func (l *utilityLog) pruneLocked(cutoff time.Time) {
	l.calls = slices.DeleteFunc(l.calls, func(c UtilityCall) bool { return c.At.Before(cutoff) })
	if extra := len(l.calls) - maxUtilityCalls; extra > 0 {
		l.calls = slices.Delete(l.calls, 0, extra+maxUtilityCalls/10)
	}
}

// rewriteLocked replaces the file with the calls kept. The caller holds mu.
func (l *utilityLog) rewriteLocked() {
	if l.path == "" {
		return
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, c := range l.calls {
		if err := enc.Encode(c); err != nil {
			return
		}
	}
	tmp := l.path + ".tmp"
	err := os.WriteFile(tmp, buf.Bytes(), 0o600)
	if err == nil {
		err = os.Rename(tmp, l.path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		log.Warn("rewrite the utility log failed", "error", err)
	}
}

// loadUtilityLog reads the log beside the store, skipping lines it cannot
// read, and drops the days past retention.
func (m *Manager) loadUtilityLog() {
	l := &m.utility
	l.mu.Lock()
	defer l.mu.Unlock()
	l.path = filepath.Join(filepath.Dir(m.store.Path()), utilityLogFile)
	f, err := os.Open(l.path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		log.Warn("read the utility log failed", "error", err)
		return
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	dropped := false
	for sc.Scan() {
		var c UtilityCall
		if json.Unmarshal(sc.Bytes(), &c) != nil || c.ID <= l.nextID || c.At.IsZero() {
			dropped = true
			continue
		}
		l.nextID = c.ID
		l.calls = append(l.calls, c)
	}
	if err := sc.Err(); err != nil {
		log.Warn("read the utility log failed", "error", err)
	}
	now := m.now()
	n := len(l.calls)
	l.pruneLocked(utilityCutoff(now))
	if dropped || len(l.calls) != n {
		l.rewriteLocked()
	}
	l.rollLocked(now)
	for _, c := range l.calls {
		if c.Outcome != utilitySkipped && dayOf(c.At) == l.day {
			l.started++
		}
	}
}

// UtilityLog returns today's use, the totals of every day kept and up to
// limit calls before the call with ID before (0: the newest), newest first.
func (m *Manager) UtilityLog(before int64, limit int) UtilityLog {
	m.mu.Lock()
	dailyLimit := m.utilityLimitLocked()
	m.mu.Unlock()
	now := m.now()
	l := &m.utility
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rollLocked(now)
	out := UtilityLog{
		Today: UtilityToday{Day: l.day, Calls: l.started, Limit: dailyLimit, Paused: l.started >= dailyLimit, ResetsAt: midnightAfter(now)},
		Days:  []UtilityDay{},
		Calls: []UtilityCall{},
	}
	byDay := map[string]*UtilityDay{}
	for i := len(l.calls) - 1; i >= 0; i-- {
		c := l.calls[i]
		c.Day = dayOf(c.At)
		d := byDay[c.Day]
		if d == nil {
			d = &UtilityDay{Day: c.Day}
			byDay[c.Day] = d
		}
		switch c.Outcome {
		case utilitySkipped:
			d.Skipped++
		case utilityError:
			d.Errors++
			fallthrough
		default:
			d.Calls++
			d.PromptChars += int64(c.PromptChars)
			d.ReplyChars += int64(c.ReplyChars)
			d.InputTokens += c.InputTokens
			d.OutputTokens += c.OutputTokens
			d.Estimated = d.Estimated || c.Estimated
			d.Credits += c.Credits
		}
		if (before > 0 && c.ID >= before) || out.Next != 0 {
			continue
		}
		if len(out.Calls) == limit {
			out.Next = out.Calls[len(out.Calls)-1].ID
			continue
		}
		out.Calls = append(out.Calls, c)
	}
	for _, d := range byDay {
		out.Days = append(out.Days, *d)
	}
	slices.SortFunc(out.Days, func(a, b UtilityDay) int { return cmp.Compare(b.Day, a.Day) })
	return out
}

func (s *Server) handleUtility(w http.ResponseWriter, r *http.Request) {
	var before int64
	if v := r.URL.Query().Get("before"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "before must be a call ID")
			return
		}
		before = n
	}
	limit := utilityPage
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > utilityPage {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("limit must be 1 to %d", utilityPage))
			return
		}
		limit = n
	}
	writeJSON(w, http.StatusOK, s.m.UtilityLog(before, limit))
}
