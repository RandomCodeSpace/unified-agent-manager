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
	purposeTitle              = "title"
	purposeCommitMessage      = "commit-message"
	purposeConfigurationDraft = "configuration-draft"
)

// The outcomes of Utility calls, and why one was skipped.
const (
	utilityOK      = "ok"
	utilityError   = "error"
	utilitySkipped = "skipped"

	skippedLimit   = "daily_limit"
	skippedOff     = "off"
	skippedInvalid = "invalid_streak"
)

const codeUtilityPaused = "utility_paused"

// The Utility job codes: no provider can run a Utility job, or the model's
// call or answer failed.
const (
	codeUtilityUnavailable = "utility_unavailable"
	codeUtilityFailed      = "utility_failed"
)

var errUtilityUnavailable = &Error{Status: http.StatusConflict, Code: codeUtilityUnavailable,
	Message: "no available provider can run Utility jobs; choose a Utility model in Settings"}

// UtilityModel is the provider and model a Utility job runs on.
type UtilityModel struct {
	Provider string
	Model    string
}

// utilityProviderLocked returns the provider that runs Utility jobs, and
// its Utility model from Settings: the first provider, in order, that is
// available, registers host tools and has a Utility model. ok is false when
// none does. The caller holds mu.
func (m *Manager) utilityProviderLocked() (runner agentapi.UtilityRunner, model UtilityModel, ok bool) {
	for _, name := range m.order {
		runner, ok := m.providers[name].(agentapi.UtilityRunner)
		if info := m.infos[name]; !ok || !info.Available || !info.Capabilities.HostTools {
			continue
		}
		if id := m.utilityModelLocked(name); id != "" {
			return runner, UtilityModel{Provider: name, Model: id}, true
		}
	}
	return nil, UtilityModel{}, false
}

// startUtilityLocked claims the Utility provider for one job, counted in
// titles so Shutdown waits for it to delete its conversation; the caller
// calls titles.Done when it ends. The caller holds mu.
func (m *Manager) startUtilityLocked() (agentapi.UtilityRunner, UtilityModel, error) {
	runner, model, ok := m.utilityProviderLocked()
	switch {
	case m.closed:
		return nil, model, errShuttingDown
	case !ok:
		return nil, model, errUtilityUnavailable
	}
	m.titles.Add(1)
	return runner, model, nil
}

// bound returns ctx, also ended when the service shuts down.
func (m *Manager) bound(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(m.ctx, cancel)
	return ctx, func() { stop(); cancel() }
}

// utilityFailed reports a failed Utility call; a call today's Utility limit
// refused keeps its own message.
func utilityFailed(what string, err error) *Error {
	if e, ok := errors.AsType[*Error](err); ok && e.Code == codeUtilityPaused {
		return e
	}
	return &Error{Status: http.StatusBadGateway, Code: codeUtilityFailed,
		Message: fmt.Sprintf("%s failed: %s", what, clipRunes(displaytext.Sanitize(err.Error()), maxDetailRunes))}
}

// UtilityCall is one Utility model call in the log. At is when it started;
// Day is its server-local date, set when the log is read. Tokens are the
// provider's figures unless Estimated, when they are worked out from the
// characters. PromptChars counts what the service sent the provider: a
// Utility request's system message and prompt, or the text a title is made
// from. Reason is why a call was skipped
// (daily_limit or off) or failed. SessionModel marks a title call made on
// the Task's own model because no Utility model is set.
type UtilityCall struct {
	ID           int64     `json:"id"`
	At           time.Time `json:"at"`
	Day          string    `json:"day,omitempty"`
	Purpose      string    `json:"purpose"`
	Provider     string    `json:"provider,omitempty"`
	Model        string    `json:"model,omitempty"`
	SessionModel bool      `json:"session_model,omitempty"`
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
	Answer       string    `json:"answer,omitempty"`
	Valid        *bool     `json:"valid,omitempty"`
	Retry        bool      `json:"retry,omitempty"`
	Fallback     bool      `json:"fallback,omitempty"`
}

// UtilityToday is today's use against the limit. ResetsAt is local midnight.
// Unlimited means the limit is lifted: Limit is then the one kept for when it
// is turned off, and the day never pauses for it.
type UtilityToday struct {
	Day       string    `json:"day"`
	Calls     int       `json:"calls"`
	Limit     int       `json:"limit"`
	Unlimited bool      `json:"unlimited,omitempty"`
	Paused    bool      `json:"paused"`
	ResetsAt  time.Time `json:"resets_at"`
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
	answers map[string][]bool
	paused  map[string]bool
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

// compactionThreshold is the compaction threshold in percent.
func (s Settings) compactionThreshold() int {
	if s.CompactionThreshold != nil {
		return *s.CompactionThreshold
	}
	return store.DefaultCompactionThreshold
}

// pausedError says why a call was skipped.
func pausedError(reason string, limit int) *Error {
	msg := "Background AI is off: its daily limit is 0. Raise it in Settings → Background AI"
	if reason == skippedLimit {
		msg = fmt.Sprintf("Background AI is paused until tomorrow: today's %d calls are used. Raise the limit in Settings → Background AI", limit)
	}
	if reason == skippedInvalid {
		msg = "Background AI purpose is paused until tomorrow after repeated invalid answers"
	}
	return &Error{Status: http.StatusConflict, Code: codeUtilityPaused, Message: msg}
}

// runUtility makes one Utility call through run, which passes the usage
// callback on to the provider, unless today's limit is reached; then it
// returns a codeUtilityPaused error without calling run. Either way the call
// is logged. call names the purpose, provider, model and Task or Project;
// prompt is the text sent.
func (m *Manager) runUtility(ctx context.Context, call UtilityCall, prompt string, run func(context.Context, func(agentapi.UtilityUsage)) (string, error)) (string, error) {
	return m.runUtilityChecked(ctx, call, prompt, run, nil)
}

func (m *Manager) runUtilityChecked(ctx context.Context, call UtilityCall, prompt string, run func(context.Context, func(agentapi.UtilityUsage)) (string, error), check func(*UtilityCall, string, error)) (string, error) {
	m.mu.Lock()
	limit, unlimited := m.utilityLimitLocked(), m.settings.UtilityUnlimited
	m.mu.Unlock()
	call.At = m.now()
	if reason := m.utility.reserve(call.At, call.Purpose, limit, unlimited); reason != "" {
		if check != nil {
			call.Fallback = true
		}
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
		m.mu.Lock()
		if len(usage.Tokens) == 0 {
			m.recordTokensLocked(call.Provider, agentapi.TokenUsage{Model: call.Model, Time: call.At, Input: usage.InputTokens, Output: usage.OutputTokens})
		}
		for _, tokens := range usage.Tokens {
			m.recordTokensLocked(call.Provider, tokens)
		}
		m.mu.Unlock()
	} else {
		call.Estimated = true
		call.InputTokens = int64((call.PromptChars + charsPerToken - 1) / charsPerToken)
		call.OutputTokens = int64((call.ReplyChars + charsPerToken - 1) / charsPerToken)
	}
	call.Outcome = utilityOK
	if err != nil {
		call.Outcome, call.Reason = utilityError, clipRunes(displaytext.Sanitize(err.Error()), maxDetailRunes)
	}
	if check != nil {
		check(&call, reply, err)
	}
	m.utility.add(call)
	return reply, err
}

// runUtilityRequest makes req through runUtility with runner. The prompt it
// logs is what req sends the model: its system message and its prompt.
func (m *Manager) runUtilityRequest(ctx context.Context, call UtilityCall, runner agentapi.UtilityRunner, req agentapi.UtilityRequest) (string, error) {
	return m.runUtility(ctx, call, req.System+req.Prompt, func(ctx context.Context, onUsage func(agentapi.UtilityUsage)) (string, error) {
		req.OnUsage = onUsage
		return runner.RunUtility(ctx, req)
	})
}

// reserve counts a call starting at now, or says why it may not start.
// unlimited lifts total.
func (l *utilityLog) reserve(now time.Time, purpose string, total int, unlimited bool) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rollLocked(now)
	switch {
	case !unlimited && total <= 0:
		return skippedOff
	case !unlimited && l.started >= total:
		return skippedLimit
	case l.paused[purpose]:
		return skippedInvalid
	}
	l.started++
	return ""
}

// rollLocked starts the count afresh on a new day. The caller holds mu.
func (l *utilityLog) rollLocked(now time.Time) {
	if day := dayOf(now); day != l.day {
		l.day, l.started = day, 0
		l.answers = map[string][]bool{}
		l.paused = map[string]bool{}
	}
}

// add logs call, appending it to the file, and drops the days past
// retention once a day.
func (l *utilityLog) add(call UtilityCall) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.recordAnswerLocked(call)
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
	if err := writeFileAtomic(l.path, utilityLogFile+".tmp.*", buf.Bytes()); err != nil {
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
			l.recordAnswerLocked(c)
		}
	}
}

// UtilityLog returns today's use, the totals of every day kept and up to
// limit calls before the call with ID before (0: the newest), newest first.
func (m *Manager) UtilityLog(before int64, limit int) UtilityLog {
	m.mu.Lock()
	dailyLimit, unlimited := m.utilityLimitLocked(), m.settings.UtilityUnlimited
	m.mu.Unlock()
	now := m.now()
	l := &m.utility
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rollLocked(now)
	out := UtilityLog{
		Today: UtilityToday{Day: l.day, Calls: l.started, Limit: dailyLimit, Unlimited: unlimited, Paused: !unlimited && l.started >= dailyLimit, ResetsAt: midnightAfter(now)},
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
		if c.Valid != nil {
			c.Valid = new(*c.Valid)
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
