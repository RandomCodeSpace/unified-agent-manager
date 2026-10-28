package web

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// sessionStatsFile holds the last scan as key=value lines, beside the store.
const sessionStatsFile = "session-stats.txt"

// Rows kept from the per-tool and per-model tallies; the rest are left out of
// the summary.
const (
	sessionStatsTopTools    = 10
	sessionStatsTopFailures = 5
	sessionStatsTopModels   = 5
)

// sessionStatsRecent is the window of SessionStats.Recent.
const sessionStatsRecent = 7 * 24 * time.Hour

// stallRetrySource marks uam's continuation of a stalled request on its user
// message in the journal (the Copilot adapter's stallSource).
const stallRetrySource = "uam-stall-retry"

// shellPoll matches a shell command that waits: a sleep, or a while/until loop.
var shellPoll = regexp.MustCompile(`(?s)\bsleep\b|\b(while|until)\b.*\bdo\b`)

// SessionStat is one key=value line. Keys are "<group>.<name>".
type SessionStat struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// SessionStats is GET /api/session-stats: counts over every Copilot session
// journal on this server, uam's own counts and its configuration. Recent is the
// journal counts again over the events of the last 7 days. It holds no prompt,
// reply, command or tool output text.
type SessionStats struct {
	Path   string        `json:"path"`
	Stats  []SessionStat `json:"stats"`
	Recent []SessionStat `json:"recent"`
}

// journalEvent is the part of an events.jsonl line the counts read. Command
// texts are only hashed in memory; no text read here reaches the summary.
type journalEvent struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	AgentID   string `json:"agentId"`
	Data      struct {
		ToolCallID string          `json:"toolCallId"`
		ToolName   string          `json:"toolName"`
		Arguments  json.RawMessage `json:"arguments"`
		Success    *bool           `json:"success"`
		Result     json.RawMessage `json:"result"`
		Reason     string          `json:"reason"`
		Error      struct {
			Message string `json:"message"`
		} `json:"error"`
		ErrorType         string `json:"errorType"`
		RequestID         string `json:"requestId"`
		Source            string `json:"source"`
		Cause             string `json:"cause"`
		PermissionRequest struct {
			FullCommandText         string `json:"fullCommandText"`
			Intention               string `json:"intention"`
			CanOfferSessionApproval bool   `json:"canOfferSessionApproval"`
		} `json:"permissionRequest"`
		Usage struct {
			Model              string  `json:"model"`
			Initiator          string  `json:"initiator"`
			InputTokens        float64 `json:"inputTokens"`
			OutputTokens       float64 `json:"outputTokens"`
			CacheReadTokens    float64 `json:"cacheReadTokens"`
			CacheWriteTokens   float64 `json:"cacheWriteTokens"`
			ReasoningTokens    float64 `json:"reasoningTokens"`
			Cost               float64 `json:"cost"`
			TimeToFirstTokenMs float64 `json:"timeToFirstTokenMs"`
			NumToolCalls       float64 `json:"numToolCalls"`
			CopilotUsage       struct {
				TotalNanoAiu float64 `json:"totalNanoAiu"`
			} `json:"copilotUsage"`
		} `json:"usage"`
	} `json:"data"`
}

// usageSum totals the session.usage_record calls of one initiator.
type usageSum struct {
	calls                                                      int
	input, output, cacheRead, cacheWrite, reasoning, cost, aiu float64
}

// modelSpend is one model's calls' cost units and AI units.
type modelSpend struct{ cost, aiu float64 }

// fetchStatus is a failed web_fetch's class: its HTTP status, a refusal by
// uam's fuse, a timeout, or other.
var fetchStatus = regexp.MustCompile(`status code (\d{3})\b`)

type sessionTally struct {
	sessions, withPrompts, skipped       int
	first, last                          string
	events, tools, failures, permissions map[string]int
	errors, aborts                       map[string]int

	// Permission requests: wait seconds, how many could offer session
	// approval or had an intention, and repeats of a command already asked
	// in the same session; commands counts each command hash over all.
	waits                       []float64
	offers, intentions, repeats int
	commands                    map[[32]byte]int
	// Shell calls, those that wait (shellPoll), and repeats of the same
	// command in one turn: from a prompt to its reply, not one model step.
	shells, polls, sameInTurn int
	// Completed turns: seconds, tool calls, and how many ran no tool;
	// promptTurns is the main turns of each owner prompt.
	turnSecs, turnTools, promptTurns []float64
	noTools                          int
	usage                            map[string]*usageSum
	ttfb                             []float64
	// The main agent's (not a subagent's) model calls: per owner prompt,
	// those that asked for tools, and those that asked for two or more at
	// once; its tool calls by tool; each model's spend.
	mainPerPrompt            []float64
	mainWithTools, mainMulti int
	mainTools                map[string]int
	spend                    map[string]*modelSpend
	// web_fetch: calls, failures by class, fetches of a host already
	// blocked (blockedFetch) in the same session, and each blocked host's
	// session count.
	fetches, fetchRetries         int
	fetchFailures                 map[string]int
	fetchFailedHosts              map[string]int
	models, modelChanges          map[string]int
	rateSwitches, prompts, stalls int
	days                          map[string]bool
}

// journalScan is one journal's state while it is read.
type journalScan struct {
	tools    map[string]string
	asked    map[string]time.Time
	commands map[[32]byte]bool
	// turns is each agent's open model step ("" the main agent); turnsSince
	// counts the main steps since the last owner prompt, -1 before the
	// first. shell holds each agent's shell commands since its last prompt.
	turns      map[string]*openTurn
	turnsSince int
	shell      map[string]map[[32]byte]bool
	// mainSince counts the main agent's model calls since the last owner
	// prompt, -1 before the first. fetchHost is each web_fetch call's host,
	// failedHosts the hosts whose fetch failed in this session.
	mainSince   int
	fetchHost   map[string]string
	failedHosts map[string]bool
}

type openTurn struct {
	at    time.Time
	tools int
}

func newSessionTally() sessionTally {
	return sessionTally{events: map[string]int{}, tools: map[string]int{}, failures: map[string]int{}, permissions: map[string]int{}, errors: map[string]int{}, aborts: map[string]int{},
		commands: map[[32]byte]int{}, usage: map[string]*usageSum{}, models: map[string]int{}, modelChanges: map[string]int{}, days: map[string]bool{},
		mainTools: map[string]int{}, spend: map[string]*modelSpend{}, fetchFailures: map[string]int{}, fetchFailedHosts: map[string]int{}}
}

// SessionStats scans every session journal under COPILOT_HOME, adds uam's own
// counts and configuration (extra first among them), writes the summary to
// sessionStatsFile, the recent counts after it as "7d."-prefixed lines, and
// returns it.
func (m *Manager) SessionStats(ctx context.Context, extra ...SessionStat) (SessionStats, error) {
	scope, err := m.configurationScope("")
	if err != nil {
		return SessionStats{}, err
	}
	journals, _ := filepath.Glob(filepath.Join(scope.base, "session-state", "*", "events.jsonl"))
	now := time.Now()
	t, recent := newSessionTally(), newSessionTally()
	for _, path := range journals {
		if err := ctx.Err(); err != nil {
			return SessionStats{}, err
		}
		t.scan(path, &recent, now.Add(-sessionStatsRecent))
	}
	stats := append(t.stats(now), m.uamStats()...)
	stats = append(append(stats, extra...), m.configStats()...)
	out := SessionStats{Path: filepath.Join(filepath.Dir(m.store.Path()), sessionStatsFile), Stats: stats, Recent: recent.stats(now)}
	var b strings.Builder
	write := func(prefix string, list []SessionStat) {
		for _, s := range list {
			b.WriteString(prefix)
			b.WriteString(s.Key)
			b.WriteByte('=')
			b.WriteString(s.Value)
			b.WriteByte('\n')
		}
	}
	write("", out.Stats)
	write("7d.", slices.DeleteFunc(slices.Clone(out.Recent), func(s SessionStat) bool { return strings.HasPrefix(s.Key, "scan.") }))
	if err := writeFileAtomic(out.Path, ".session-stats-*", []byte(b.String())); err != nil {
		return SessionStats{}, err
	}
	return out, nil
}

// journalCount is one tally's state while a journal is read; seen is whether
// the journal counts as one of its sessions.
type journalCount struct {
	t              *sessionTally
	js             *journalScan
	seen, prompted bool
}

func newJournalCount(t *sessionTally, seen bool) *journalCount {
	return &journalCount{t: t, seen: seen, js: &journalScan{tools: map[string]string{}, asked: map[string]time.Time{}, commands: map[[32]byte]bool{}, turns: map[string]*openTurn{}, turnsSince: -1, shell: map[string]map[[32]byte]bool{},
		mainSince: -1, fetchHost: map[string]string{}, failedHosts: map[string]bool{}}}
}

func (c *journalCount) add(e *journalEvent) {
	c.seen = true
	c.prompted = c.t.add(e, c.js) || c.prompted
}

func (c *journalCount) done() {
	if !c.seen {
		return
	}
	c.t.sessions++
	c.t.withPrompts += b2i(c.prompted)
	if c.js.turnsSince >= 0 {
		c.t.promptTurns = append(c.t.promptTurns, float64(c.js.turnsSince))
	}
	if c.js.mainSince > 0 {
		c.t.mainPerPrompt = append(c.t.mainPerPrompt, float64(c.js.mainSince))
	}
	for host := range c.js.failedHosts {
		c.t.fetchFailedHosts[host]++
	}
}

// scan counts one journal into t, and its events at or after since into
// recent, which counts it as a session only when one of them is.
func (t *sessionTally) scan(path string, recent *sessionTally, since time.Time) {
	f, err := os.Open(path) // #nosec G304 -- a journal under the service's own COPILOT_HOME.
	if err != nil {
		t.skipped++
		return
	}
	defer func() { _ = f.Close() }()
	all, last := newJournalCount(t, true), newJournalCount(recent, false)
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			var e journalEvent
			if json.Unmarshal(line, &e) != nil {
				t.skipped++
			} else {
				all.add(&e)
				if at, err := time.Parse(time.RFC3339Nano, e.Timestamp); err == nil && !at.Before(since) {
					last.add(&e)
				}
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.skipped++
			}
			break
		}
	}
	all.done()
	last.done()
}

// add counts one event; it reports whether the event is a user prompt.
func (t *sessionTally) add(e *journalEvent, js *journalScan) bool {
	t.events[e.Type]++
	if e.Timestamp != "" {
		if t.first == "" || e.Timestamp < t.first {
			t.first = e.Timestamp
		}
		t.last = max(t.last, e.Timestamp)
	}
	at, _ := time.Parse(time.RFC3339Nano, e.Timestamp)
	switch e.Type {
	case "tool.execution_start":
		js.tools[e.Data.ToolCallID] = e.Data.ToolName
		t.tools[e.Data.ToolName]++
		if turn := js.turns[e.AgentID]; turn != nil {
			turn.tools++
		}
		if e.AgentID == "" {
			t.mainTools[e.Data.ToolName]++
		}
		if e.Data.ToolName == "web_fetch" {
			t.addFetch(e, js)
		}
		if e.Data.ToolName == "bash" || e.Data.ToolName == "shell" {
			seen := js.shell[e.AgentID]
			if seen == nil {
				seen = map[[32]byte]bool{}
				js.shell[e.AgentID] = seen
			}
			t.addShell(e.Data.Arguments, seen)
		}
	case "tool.execution_complete":
		if e.Data.Success != nil && !*e.Data.Success {
			t.failures[cmp.Or(js.tools[e.Data.ToolCallID], "unknown")]++
			if host, ok := js.fetchHost[e.Data.ToolCallID]; ok {
				class := fetchFailureClass(e.Data.Error.Message)
				t.fetchFailures[class]++
				if host != "" && blockedFetch[class] {
					js.failedHosts[host] = true
				}
			}
		}
	case "permission.requested":
		p := e.Data.PermissionRequest
		js.asked[e.Data.RequestID] = at
		t.offers += b2i(p.CanOfferSessionApproval)
		t.intentions += b2i(strings.TrimSpace(p.Intention) != "")
		if p.FullCommandText != "" {
			h := sha256.Sum256([]byte(p.FullCommandText))
			t.commands[h]++
			t.repeats += b2i(js.commands[h])
			js.commands[h] = true
		}
	case "permission.completed":
		var result struct {
			Kind string `json:"kind"`
		}
		_ = json.Unmarshal(e.Data.Result, &result)
		t.permissions[cmp.Or(result.Kind, "unknown")]++
		if asked, ok := js.asked[e.Data.RequestID]; ok {
			delete(js.asked, e.Data.RequestID)
			if !asked.IsZero() && !at.IsZero() {
				t.waits = append(t.waits, max(at.Sub(asked).Seconds(), 0))
			}
		}
	case "assistant.turn_start":
		js.turns[e.AgentID] = &openTurn{at: at}
		if e.AgentID == "" && js.turnsSince >= 0 {
			js.turnsSince++
		}
	case "assistant.turn_end":
		if turn := js.turns[e.AgentID]; turn != nil {
			delete(js.turns, e.AgentID)
			t.turnTools = append(t.turnTools, float64(turn.tools))
			t.noTools += b2i(turn.tools == 0)
			if !turn.at.IsZero() && !at.IsZero() {
				t.turnSecs = append(t.turnSecs, max(at.Sub(turn.at).Seconds(), 0))
			}
		}
	case "session.usage_record":
		t.addUsage(e, js)
	case "session.model_change":
		t.modelChanges[cmp.Or(e.Data.Source, "unknown")]++
		t.rateSwitches += b2i(e.Data.Cause == "rate_limit_auto_switch")
	case "session.error":
		t.errors[cmp.Or(e.Data.ErrorType, "unknown")]++
	case "abort":
		t.aborts[cmp.Or(e.Data.Reason, "unknown")]++
	case "user.message":
		t.stalls += b2i(e.Data.Source == stallRetrySource)
		if e.Data.Source != stallRetrySource {
			delete(js.shell, e.AgentID)
		}
		// The owner's prompts: neither a subagent's task nor uam's
		// continuation of a stalled request.
		if e.AgentID == "" && e.Data.Source != stallRetrySource {
			t.prompts++
			t.days[e.Timestamp[:min(len(e.Timestamp), 10)]] = true
			if js.turnsSince >= 0 {
				t.promptTurns = append(t.promptTurns, float64(js.turnsSince))
			}
			js.turnsSince = 0
			// Every prompt makes a model call: none means the journal
			// recorded no usage (an older CLI), not a free prompt.
			if js.mainSince > 0 {
				t.mainPerPrompt = append(t.mainPerPrompt, float64(js.mainSince))
			}
			js.mainSince = 0
		}
		return true
	}
	return false
}

// addShell counts a shell call that waits, and one whose command is in seen,
// the agent's commands since its prompt.
func (t *sessionTally) addShell(raw json.RawMessage, seen map[[32]byte]bool) {
	t.shells++
	var args struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(raw, &args) != nil || args.Command == "" {
		return
	}
	t.polls += b2i(shellPoll.MatchString(args.Command))
	h := sha256.Sum256([]byte(args.Command))
	t.sameInTurn += b2i(seen[h])
	seen[h] = true
}

// addFetch counts a web_fetch call, and one of a host that already failed in
// this session. The host is kept in memory only, never in the summary.
func (t *sessionTally) addFetch(e *journalEvent, js *journalScan) {
	t.fetches++
	var args struct {
		URL string `json:"url"`
	}
	host := ""
	if json.Unmarshal(e.Data.Arguments, &args) == nil {
		if u, err := url.Parse(args.URL); err == nil {
			host = strings.ToLower(u.Host)
		}
	}
	js.fetchHost[e.Data.ToolCallID] = host
	t.fetchRetries += b2i(host != "" && js.failedHosts[host])
}

// blockedFetch are the failure classes that say a host is unreachable from
// here, not that a URL was wrong: those the fuse blocks, and timeouts.
var blockedFetch = map[string]bool{"status_401": true, "status_403": true, "status_407": true, "status_451": true, "refused_by_fuse": true, "timeout": true}

// fetchFailureClass names a failed web_fetch by its error message.
func fetchFailureClass(message string) string {
	switch {
	case strings.Contains(message, "uam fuse:"):
		return "refused_by_fuse"
	case fetchStatus.MatchString(message):
		return "status_" + fetchStatus.FindStringSubmatch(message)[1]
	case strings.Contains(strings.ToLower(message), "timeout") || strings.Contains(strings.ToLower(message), "timed out"):
		return "timeout"
	}
	return "other"
}

// addUsage adds one model call to the total and to its initiator's sums,
// and a main agent's call to its counts.
func (t *sessionTally) addUsage(e *journalEvent, js *journalScan) {
	u := e.Data.Usage
	if u.Initiator != "sub-agent" {
		if js.mainSince >= 0 {
			js.mainSince++
		}
		t.mainWithTools += b2i(u.NumToolCalls >= 1)
		t.mainMulti += b2i(u.NumToolCalls >= 2)
	}
	model := cmp.Or(u.Model, "unknown")
	spend := t.spend[model]
	if spend == nil {
		spend = &modelSpend{}
		t.spend[model] = spend
	}
	spend.cost += u.Cost
	spend.aiu += u.CopilotUsage.TotalNanoAiu / 1e9
	group := map[string]string{"user": "user", "sub-agent": "subagent"}[u.Initiator]
	for _, g := range []string{"total", cmp.Or(group, "other")} {
		sum := t.usage[g]
		if sum == nil {
			sum = &usageSum{}
			t.usage[g] = sum
		}
		sum.calls++
		sum.input += u.InputTokens
		sum.output += u.OutputTokens
		sum.cacheRead += u.CacheReadTokens
		sum.cacheWrite += u.CacheWriteTokens
		sum.reasoning += u.ReasoningTokens
		sum.cost += u.Cost
		sum.aiu += u.CopilotUsage.TotalNanoAiu / 1e9
	}
	t.models[cmp.Or(u.Model, "unknown")]++
	if u.TimeToFirstTokenMs > 0 {
		t.ttfb = append(t.ttfb, u.TimeToFirstTokenMs)
	}
}

func (t *sessionTally) stats(now time.Time) []SessionStat {
	var out []SessionStat
	add := func(key string, n int) { out = append(out, SessionStat{key, strconv.Itoa(n)}) }
	addF := func(key string, v float64, prec int) {
		out = append(out, SessionStat{key, strconv.FormatFloat(v, 'f', prec, 64)})
	}
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
	add("friction.tool_failure_pct", pct(sum(t.failures), t.events["tool.execution_start"]))
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

	requests := t.events["permission.requested"]
	addF("approvals.wait_median_s", quantile(t.waits, 0.5), 1)
	addF("approvals.wait_p90_s", quantile(t.waits, 0.9), 1)
	addF("approvals.wait_total_s", fsum(t.waits), 0)
	add("approvals.session_offer_pct", pct(t.offers, requests))
	add("approvals.with_intention_pct", pct(t.intentions, requests))
	add("approvals.repeats", t.repeats)
	add("approvals.repeat_pct", pct(t.repeats, requests))
	asked3 := 0
	for _, n := range t.commands {
		asked3 += b2i(n >= 3)
	}
	add("approvals.commands_asked_3x", asked3)

	add("polling.shell_calls", t.shells)
	add("polling.sleep_or_loop", t.polls)
	add("polling.sleep_or_loop_pct", pct(t.polls, t.shells))
	add("polling.same_command_in_prompt", t.sameInTurn)

	add("turns.completed", len(t.turnSecs))
	addF("turns.median_s", quantile(t.turnSecs, 0.5), 1)
	addF("turns.p90_s", quantile(t.turnSecs, 0.9), 1)
	addF("turns.tool_calls_median", quantile(t.turnTools, 0.5), 0)
	add("turns.no_tools_pct", pct(t.noTools, len(t.turnTools)))
	addF("turns.per_prompt_median", quantile(t.promptTurns, 0.5), 0)

	for _, g := range []string{"total", "user", "subagent", "other"} {
		u := t.usage[g]
		if u == nil {
			u = &usageSum{}
		}
		add("tokens."+g+"_calls", u.calls)
		addF("tokens."+g+"_input", u.input, 0)
		addF("tokens."+g+"_output", u.output, 0)
		addF("tokens."+g+"_cache_read", u.cacheRead, 0)
		addF("tokens."+g+"_cache_write", u.cacheWrite, 0)
		addF("tokens."+g+"_reasoning", u.reasoning, 0)
		addF("tokens."+g+"_cost", u.cost, 2)
		addF("tokens."+g+"_ai_units", u.aiu, 2)
	}
	addF("models.ttfb_median_ms", quantile(t.ttfb, 0.5), 0)
	addF("models.ttfb_p90_ms", quantile(t.ttfb, 0.9), 0)
	add("models.rate_limit_switches", t.rateSwitches)
	for _, kv := range ranked(t.models, sessionStatsTopModels) {
		add("model_calls."+kv.k, kv.n)
	}
	spend := func(model string) modelSpend {
		if s := t.spend[model]; s != nil {
			return *s
		}
		return modelSpend{}
	}
	for _, kv := range ranked(t.models, sessionStatsTopModels) {
		addF("model_cost."+kv.k, spend(kv.k).cost, 2)
	}
	for _, kv := range ranked(t.models, sessionStatsTopModels) {
		addF("model_ai_units."+kv.k, spend(kv.k).aiu, 2)
	}

	mainCalls := t.usage["user"].callsOrZero() + t.usage["other"].callsOrZero()
	add("main.model_calls", mainCalls)
	addF("main.model_calls_per_prompt_median", quantile(t.mainPerPrompt, 0.5), 0)
	addF("main.model_calls_per_prompt_p90", quantile(t.mainPerPrompt, 0.9), 0)
	add("main.calls_with_tools", t.mainWithTools)
	add("main.multi_tool_calls_pct", pct(t.mainMulti, t.mainWithTools))
	add("main.tool_calls", sum(t.mainTools))
	add("main.tool_calls_pct", pct(sum(t.mainTools), t.events["tool.execution_start"]))
	for _, kv := range ranked(t.mainTools, sessionStatsTopTools) {
		add("main_tools."+kv.k, kv.n)
	}

	add("web_fetch.calls", t.fetches)
	add("web_fetch.failed", sum(t.fetchFailures))
	add("web_fetch.failed_pct", pct(sum(t.fetchFailures), t.fetches))
	add("web_fetch.retries_of_blocked_host", t.fetchRetries)
	add("web_fetch.blocked_hosts", len(t.fetchFailedHosts))
	multi := 0
	for _, n := range t.fetchFailedHosts {
		multi += b2i(n >= 2)
	}
	add("web_fetch.blocked_hosts_in_2plus_sessions", multi)
	for _, kv := range ranked(t.fetchFailures, 0) {
		add("web_fetch.failure_"+kv.k, kv.n)
	}
	for _, kv := range ranked(t.modelChanges, 0) {
		add("model_changes."+kv.k, kv.n)
	}

	add("activity.owner_prompts", t.prompts)
	add("activity.active_days", len(t.days))
	addF("activity.sessions_per_day", ratio(t.withPrompts, len(t.days)), 1)
	addF("activity.prompts_per_day", ratio(t.prompts, len(t.days)), 1)
	add("activity.subagents_started", t.events["subagent.started"])
	add("activity.subagents_completed", t.events["subagent.completed"])
	add("activity.subagents_failed", t.events["subagent.failed"])
	addF("activity.compactions_per_session", ratio(t.events["session.compaction_complete"], t.withPrompts), 2)
	add("activity.stall_retries", t.stalls)
	return out
}

func (u *usageSum) callsOrZero() int {
	if u == nil {
		return 0
	}
	return u.calls
}

// pct is n of total in whole percent, 0 for no total.
func pct(n, total int) int {
	if total == 0 {
		return 0
	}
	return n * 100 / total
}

func ratio(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return float64(n) / float64(d)
}

// quantile is the nearest-rank q quantile of xs, 0 for none.
func quantile(xs []float64, q float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := slices.Sorted(slices.Values(xs))
	return s[max(int(math.Ceil(q*float64(len(s))))-1, 0)]
}

func fsum(xs []float64) float64 {
	n := 0.0
	for _, x := range xs {
		n += x
	}
	return n
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
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
	extra := []SessionStat{{"config.uam_version", cmp.Or(s.version, "unknown")}}
	if s.connections != nil {
		extra = append(extra, SessionStat{"config.connected_instances", strconv.Itoa(len(s.connections.List()))})
	}
	stats, err := s.m.SessionStats(r.Context(), extra...)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}
