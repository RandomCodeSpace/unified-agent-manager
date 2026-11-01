package web

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

const (
	// maxBudgetCredits bounds a Thread's credit budget.
	maxBudgetCredits = 1_000_000
	// budgetWarnShare of the credits, or budgetWarnPoints short of the quota
	// limit, the agent is told to wrap up.
	budgetWarnShare  = 0.8
	budgetWarnPoints = 5
)

var errBudgetReached = &Error{Status: http.StatusConflict, Code: "budget_reached", Message: "This Thread reached its budget. Raise or remove the budget to send more."}

// BudgetView is SessionSummary.Budget. Spent is the AI units the Thread's
// conversation reported since the budget was set; QuotaUsed is the highest
// share, in percent, used of the provider's limited account quotas, omitted
// while none is known. WrappingUp is set while the Thread, at its limit,
// writes its handoff.
type BudgetView struct {
	Credits      float64  `json:"credits,omitempty"`
	Spent        float64  `json:"spent"`
	QuotaPercent float64  `json:"quota_percent,omitempty"`
	QuotaUsed    *float64 `json:"quota_used,omitempty"`
	Reached      bool     `json:"reached,omitempty"`
	WrappingUp   bool     `json:"wrapping_up,omitempty"`
}

func budgetRecord(b store.Budget) *store.Budget {
	if b == (store.Budget{}) {
		return nil
	}
	return &b
}

func handoffRecord(h store.WebHandoff) *store.WebHandoff {
	if h == (store.WebHandoff{}) {
		return nil
	}
	return &h
}

func (s *webSession) usedUnits() float64 {
	if s.usage == nil {
		return 0
	}
	return s.usage.AIUnits
}

func (s *webSession) budgetSpent() float64 {
	return max(0, s.usedUnits()-s.budget.From)
}

// quotaUsedLocked is the highest share used of provider's limited quotas.
func (m *Manager) quotaUsedLocked(provider string) (float64, bool) {
	c := m.quota[provider]
	if c == nil {
		return 0, false
	}
	used, ok := 0.0, false
	for _, q := range c.quotas {
		if !q.Unlimited {
			used, ok = max(used, 100-q.RemainingPercent), true
		}
	}
	return used, ok
}

func (m *Manager) budgetViewLocked(s *webSession) *BudgetView {
	if s.budget.Credits == 0 && s.budget.QuotaPercent == 0 {
		return nil
	}
	v := &BudgetView{Credits: s.budget.Credits, Spent: s.budgetSpent(), QuotaPercent: s.budget.QuotaPercent, Reached: s.budget.Reached, WrappingUp: s.budget.WrapUp}
	if used, ok := m.quotaUsedLocked(s.provider); ok {
		v.QuotaUsed = &used
	}
	return v
}

// budgetOverLocked says why s's budget is spent; "" while it is not.
func (m *Manager) budgetOverLocked(s *webSession) string {
	b := s.budget
	if b.Credits > 0 && s.budgetSpent() >= b.Credits {
		return fmt.Sprintf("Stopped at its budget: %s of %s AI credits", creditText(s.budgetSpent()), creditText(b.Credits))
	}
	if used, ok := m.quotaUsedLocked(s.provider); ok && b.QuotaPercent > 0 && used >= b.QuotaPercent {
		return fmt.Sprintf("Stopped at its budget: account quota %s%% used, limit %s%%", creditText(used), creditText(b.QuotaPercent))
	}
	return ""
}

// checkBudgetLocked marks s's budget reached once it is spent. A running
// turn of a Thread whose budget is reached is not stopped: it wraps up
// (budgetWrap) and leaves a handoff. Autopilot between its turns is stopped
// as before. The caller reports the change (changedLocked).
func (m *Manager) checkBudgetLocked(s *webSession) {
	why := m.budgetOverLocked(s)
	if why != "" {
		if !s.budget.Reached {
			m.count("budget_reached")
		}
		s.budget.Reached = true
	}
	if m.closed || !s.budget.Reached || s.budgetStopping || s.wrap != nil || s.budget.WrapUp || s.conv == nil {
		return
	}
	why = cmp.Or(why, "Stopped at its budget")
	running := busy(s.state())
	if running && s.handoff == nil {
		s.wrap = newWrap(s, why)
		s.budget.WrapUp = true
		return
	}
	// A turn after the handoff (only an uncertain send starts one) stops.
	if running || s.execution != nil && s.execution.Mode == "autopilot" {
		m.budgetStopLocked(s, why)
	}
}

// budgetStopLocked has uam stop s's running turn for its budget, saying why.
func (m *Manager) budgetStopLocked(s *webSession, why string) {
	if s.budgetStopping {
		return
	}
	s.budgetStopping = true
	id := s.id
	m.wg.Add(1)
	go func() {
		defer log.Recover("budget stop", &m.mu)
		defer m.wg.Done()
		_, _ = m.cancelBecause(id, stopBudget, why)
		m.mu.Lock()
		s.budgetStopping = false
		m.mu.Unlock()
	}()
}

// checkQuotaBudgetsLocked checks the Threads with a quota budget after the
// account quotas were read.
func (m *Manager) checkQuotaBudgetsLocked() {
	for _, s := range m.sessions {
		if s.removed || s.budget.QuotaPercent == 0 {
			continue
		}
		before := m.summaryLocked(s)
		m.checkBudgetLocked(s)
		m.changedLocked(s, before)
	}
}

// budgetToolLocked is the budget's part of a tool call: deny once it is
// reached, during the wrap-up with the handoff instruction, and a note to
// wrap up, once, as it nears.
func (m *Manager) budgetToolLocked(s *webSession) (deny, note string) {
	b := s.budget
	if s.wrap != nil {
		return wrapDeny, ""
	}
	if b.Reached {
		return "uam budget: this Thread reached the spending limit its owner set. Stop working, and report what is done and what is left.", ""
	}
	if s.budgetWarned {
		return "", ""
	}
	near := b.Credits > 0 && s.budgetSpent() >= b.Credits*budgetWarnShare
	if used, ok := m.quotaUsedLocked(s.provider); ok && b.QuotaPercent > 0 && used >= b.QuotaPercent-budgetWarnPoints {
		near = true
	}
	if !near {
		return "", ""
	}
	s.budgetWarned = true
	return "", "uam budget: this Thread is close to the spending limit its owner set. At the limit uam denies tools and asks for a handoff. Wrap up: finish the current step and report."
}

// SetBudget sets the Thread's budget, running or not: credits more AI
// units from now and the account quota used up to quotaPercent; zero is no
// limit, both zero remove it. Setting it again starts its credits afresh,
// and ends a wrap-up: the turn goes on with its tools, and a handoff still
// being written is dropped.
func (m *Manager) SetBudget(id string, credits, quotaPercent float64) (SessionSummary, error) {
	if !(credits >= 0 && credits <= maxBudgetCredits) || !(quotaPercent >= 0 && quotaPercent <= 100) {
		return SessionSummary{}, newError(http.StatusBadRequest, "budget credits must be 0 to %d and quota_percent 0 to 100", maxBudgetCredits)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil || s.removed {
		return SessionSummary{}, newError(http.StatusNotFound, msgSessionNotFound)
	}
	if s.stage == StageArchived {
		return SessionSummary{}, s.readOnlyLocked()
	}
	before := m.summaryLocked(s)
	m.dropWrapLocked(s)
	s.budget, s.budgetWarned = store.Budget{}, false
	if credits > 0 || quotaPercent > 0 {
		s.budget = store.Budget{Credits: credits, From: s.usedUnits(), QuotaPercent: quotaPercent}
		m.count("budget_set")
		m.checkBudgetLocked(s)
	}
	m.changedLocked(s, before)
	if !s.budget.Reached {
		m.kickDrainLocked(s)
	}
	return m.summaryLocked(s), nil
}

// BudgetPrediction is the provider's estimate of the AI credits the next
// prompt of a Thread with a credit budget may use (agentapi.LimitPredictor);
// ok is false without one. It reads no model and changes nothing.
func (m *Manager) BudgetPrediction(ctx context.Context, id string) (credits float64, ok bool, err error) {
	s, err := m.lookup(id)
	if err != nil {
		return 0, false, err
	}
	m.mu.Lock()
	var conv agentapi.Conversation
	if !m.closed && !s.removed && s.stage == StageActive && s.budget.Credits > 0 && !s.budget.Reached {
		conv = s.conv
	}
	m.mu.Unlock()
	p, ok := conv.(agentapi.LimitPredictor)
	if !ok {
		return 0, false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, controlTimeout)
	defer cancel()
	credits, err = p.PredictTurnCredits(ctx)
	if err != nil || !(credits > 0) || math.IsInf(credits, 0) {
		return 0, false, nil
	}
	return credits, true, nil
}

func (s *Server) handleBudgetPrediction(w http.ResponseWriter, r *http.Request) {
	credits, ok, err := s.m.BudgetPrediction(r.Context(), r.PathValue("id"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	out := map[string]float64{}
	if ok {
		out["credits"] = credits
	}
	writeJSON(w, http.StatusOK, out)
}

// The wrap-up. At its budget a running turn is not stopped. A tool already
// running finishes (tools cost no AI credits; model calls do), and so does
// the model call in flight. Every further tool call is denied with the
// handoff instruction (wrapDeny), so the agent's next reply is its handoff.
// A turn that ends before the agent was told gets one follow-up turn with
// the same instruction (wrapPrompt), its tools still denied. It is a turn of
// its own, not a steer: an "immediate" steer waits for the next model call,
// which a finishing reply may not have, and interrupting the turn would
// throw away the reply in flight and kill a running tool. The wrap-up gets
// wrapCalls model calls once the budget is reached; uam stops it at the
// next one (the backstop), as it stops a Thread at its budget otherwise.
// The handoff is the main agent's reply after it was told, with uam's facts
// (handoffNotes.markdown) added; without such a reply, uam's facts alone.
const (
	wrapCalls = 2
	// handoffReadWait bounds the git and journal reads of the facts.
	handoffReadWait = 10 * time.Second
	// Bounds of the facts' lists.
	maxHandoffFiles   = 30
	maxHandoffTodos   = 20
	maxHandoffFailed  = 5
	maxHandoffRunes   = 160
	maxHandoffCommits = 30
	maxHandoffRepos   = 10

	handoffShape = "Write it in Markdown with these sections: ## Done, ## In progress (what is half-done and the state it is in), ## Next steps, ## How to resume (files and commands). uam adds the open todos, the commits, the uncommitted files and the AI credits spent."
	wrapDeny     = "uam budget: this Thread reached the spending limit its owner set. Do not start new work and do not call tools. Reply now with a handoff for whoever continues this work. " + handoffShape
	wrapPrompt   = "uam: this Thread reached the spending limit its owner set. Stop here: do not call tools or start new work. Reply with a handoff for whoever continues this work. " + handoffShape
)

// budgetWrap is a Thread's wrap-up. since is the main agent's last reply
// when it began and turnStart its turn's start; before holds the tool calls
// already in the transcript then, so a call that shows up later is one uam
// denied. told is set once the main agent got the instruction (a denied
// call of its own, or the wrap-up prompt), prompted once uam sent the
// prompt, started once a turn ran after it; calls counts model calls. stop
// is set when the turn is to end without a handoff reply (stopWhy says
// why), done once uam reads the facts. promptWait ends a wrap-up whose
// prompt the provider may not have received.
type budgetWrap struct {
	since, why, stopWhy                 string
	turnStart                           time.Time
	before                              map[string]bool
	told, prompted, started, stop, done bool
	calls                               int
	promptWait                          *time.Timer
}

// wrapPromptWait is how long a wrap-up waits for a turn after a prompt the
// provider may not have received.
var wrapPromptWait = 2 * time.Minute

// handoffRepo is one repository's part of the facts: its working tree
// against HEAD and the commits made since the Thread started.
type handoffRepo struct {
	// label is its folder relative to the Thread's, "" for the Thread's own.
	label   string
	files   taskListing
	err     error
	commits []string // "hash\tsubject", newest first
	more    bool     // more commits than were read
}

// handoffFiles reads the facts' repositories: the Thread's folder's (first,
// even when it is not one) and those inside the folder holding files its
// edit tools changed, such as repositories it created there, which the
// folder's own git status does not see. Edits outside the folder are left
// out: git runs only where the Thread works.
var handoffFiles = func(ctx context.Context, workdir string, edits map[string]time.Time, turnStart, since time.Time) []handoffRepo {
	files, err := listTaskChanges(ctx, workdir, edits, turnStart, time.Time{})
	main := handoffRepo{files: files, err: err}
	main.commits, main.more = handoffCommits(ctx, files.repo, since)
	repos := []handoffRepo{main}
	bases := []string{filepath.Clean(workdir)}
	if real, err := filepath.EvalSymlinks(workdir); err == nil {
		bases = append(bases, real)
	}
	// inside is p's path relative to the folder, ok when p is in it.
	inside := func(p string) (string, bool) {
		for _, base := range bases {
			if rel, err := filepath.Rel(base, p); err == nil && filepath.IsLocal(rel) {
				return rel, true
			}
		}
		return "", false
	}
	abs := map[string]time.Time{}
	for p, at := range edits {
		if !filepath.IsAbs(p) {
			p = filepath.Join(workdir, p)
		}
		if _, ok := inside(filepath.Clean(p)); ok {
			abs[p] = at
		}
	}
	for _, top := range editRepos(abs) {
		rel, ok := inside(top)
		if !ok || (files.repo != nil && top == files.repo.top) || len(repos) > maxHandoffRepos {
			continue
		}
		r := handoffRepo{label: filepath.ToSlash(rel)}
		r.files, r.err = listTaskChanges(ctx, top, abs, turnStart, time.Time{})
		r.commits, r.more = handoffCommits(ctx, r.files.repo, since)
		repos = append(repos, r)
	}
	return repos
}

// editRepos lists the work trees holding paths: for each, the nearest
// folder above it with a .git entry, symbolic links resolved as git does.
func editRepos(paths map[string]time.Time) []string {
	seen, tops := map[string]string{}, map[string]bool{}
	var top func(dir string) string
	top = func(dir string) string {
		if t, ok := seen[dir]; ok {
			return t
		}
		t := ""
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			t = dir
			if real, err := filepath.EvalSymlinks(dir); err == nil {
				t = real
			}
		} else if parent := filepath.Dir(dir); parent != dir {
			t = top(parent)
		}
		seen[dir] = t
		return t
	}
	for p := range paths {
		if t := top(filepath.Dir(p)); t != "" {
			tops[t] = true
		}
	}
	return slices.Sorted(maps.Keys(tops))
}

// handoffCommits lists HEAD's commits since the Thread started, newest
// first, at most maxHandoffCommits, and whether there are more. Commits by
// anything else in the same repository since then are among them.
func handoffCommits(ctx context.Context, repo *gitRepo, since time.Time) ([]string, bool) {
	if repo == nil || !repo.hasHead || since.IsZero() {
		return nil, false
	}
	out, code, _, err := runGit(ctx, repo.git, repo.top, 64<<10, "log", "--since="+since.UTC().Format("2006-01-02 15:04:05 +0000"),
		"--format=%h%x09%s", "-n", strconv.Itoa(maxHandoffCommits+1), "HEAD", "--")
	if err != nil || code != 0 {
		return nil, false
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if lines[0] == "" {
		return nil, false
	}
	if len(lines) > maxHandoffCommits {
		return lines[:maxHandoffCommits], true
	}
	return lines, false
}

// newWrap begins s's wrap-up.
func newWrap(s *webSession, why string) *budgetWrap {
	w := &budgetWrap{since: lastReplyID(s), why: why, turnStart: s.turnStart, before: map[string]bool{}}
	for _, it := range s.items {
		if it.Tool != nil {
			w.before[it.ID] = true
		}
	}
	return w
}

// dropWrapLocked ends s's wrap-up without a handoff.
func (m *Manager) dropWrapLocked(s *webSession) {
	if w := s.wrap; w != nil && w.promptWait != nil {
		w.promptWait.Stop()
	}
	s.wrap = nil
}

// wrapItemLocked notes a tool call of the wrap-up: every call that started
// after it began was denied, and a main agent's call says it got the
// instruction. Subagents' denials do not tell the main agent anything.
func (m *Manager) wrapItemLocked(s *webSession, it agentapi.Item) {
	w := s.wrap
	if w == nil || w.done || it.Tool == nil || w.before[it.ID] || it.AgentID != "" {
		return
	}
	if it.Tool.Status == agentapi.ToolFailed || it.Tool.Status == agentapi.ToolCompleted {
		w.told = true
	}
}

// lastReplyID is the ID of the main agent's last reply with text; "" when
// it has none.
func lastReplyID(s *webSession) string {
	for i := len(s.items) - 1; i >= 0; i-- {
		if it := s.items[i]; it.AgentID == "" && it.Kind == agentapi.ItemAssistant && strings.TrimSpace(it.Text) != "" {
			return it.ID
		}
	}
	return ""
}

// wrapCallLocked counts a model call of the wrap-up, and stops the turn at
// each call past wrapCalls: a stop the provider refused (busy, a timeout) is
// tried again at the next.
func (m *Manager) wrapCallLocked(s *webSession) {
	w := s.wrap
	if w == nil || w.done {
		return
	}
	if w.calls++; w.calls > wrapCalls {
		if !w.stop {
			w.stop, w.stopWhy = true, fmt.Sprintf("the wrap-up used its %d model calls", wrapCalls)
		}
		m.budgetStopLocked(s, w.why)
	}
}

// wrapTurnEndLocked goes on with the wrap-up once its turn ended: a reply
// after the agent was told is the handoff; a turn that completed before it
// was told gets the wrap-up prompt; otherwise the handoff is uam's facts.
func (m *Manager) wrapTurnEndLocked(s *webSession, state agentapi.TurnState) {
	w := s.wrap
	if w == nil || w.done {
		return
	}
	reply := lastReplyID(s)
	switch {
	case w.stop:
		m.finishWrapLocked(s, "", w.stopWhy)
	case w.told && reply != "" && reply != w.since:
		m.finishWrapLocked(s, reply, "")
	case !w.prompted && state == agentapi.TurnCompleted && s.conv != nil:
		w.prompted, w.told, w.since = true, true, reply
		m.sendWrapPromptLocked(s, w)
	case state == agentapi.TurnFailed:
		m.finishWrapLocked(s, "", "the turn failed")
	case state == agentapi.TurnCancelled:
		m.finishWrapLocked(s, "", "the turn was stopped")
	default:
		m.finishWrapLocked(s, "", "the agent wrote no handoff")
	}
}

// sendWrapPromptLocked sends the wrap-up prompt as the Thread's next turn,
// past its budget's refusal of sends. Failing that, the handoff is uam's
// facts.
func (m *Manager) sendWrapPromptLocked(s *webSession, w *budgetWrap) {
	reqID, err := newUUID()
	if err != nil {
		m.finishWrapLocked(s, "", "uam could not send the wrap-up prompt")
		return
	}
	m.wg.Add(1)
	go func() {
		defer log.Recover("budget wrap-up", &m.mu)
		defer m.wg.Done()
		s.op.Lock()
		defer s.op.Unlock()
		m.mu.Lock()
		live := !m.closed && !s.removed && s.wrap == w && !w.stop && !w.done
		m.mu.Unlock()
		if !live {
			return
		}
		sub, err := m.send(s, turnInput{text: wrapPrompt}, reqID)
		m.mu.Lock()
		defer m.mu.Unlock()
		if err == nil && sub.Status == SubmissionAccepted {
			return
		}
		if err == nil && sub.Status == SubmissionUncertain {
			// The provider may have it: a turn may still come. Without one the
			// wrap-up ends with the facts.
			if s.wrap == w && !w.done && !w.started && !m.closed {
				w.promptWait = time.AfterFunc(wrapPromptWait, func() { m.wrapPromptLapsed(s, w) })
			}
			return
		}
		if s.wrap == w && !w.done && !m.closed {
			before := m.summaryLocked(s)
			m.finishWrapLocked(s, "", "uam could not send the wrap-up prompt")
			m.changedLocked(s, before)
		}
	}()
}

// wrapPromptLapsed ends a wrap-up whose uncertain prompt started no turn.
func (m *Manager) wrapPromptLapsed(s *webSession, w *budgetWrap) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || s.removed || s.wrap != w || w.done || w.started || busy(s.state()) {
		return
	}
	before := m.summaryLocked(s)
	m.finishWrapLocked(s, "", "the wrap-up prompt was not confirmed and no turn started")
	m.changedLocked(s, before)
}

// handoffNotes is what the facts take from memory when the wrap-up ends.
type handoffNotes struct {
	id, workdir, noReply string
	edits                map[string]time.Time
	turnStart, started   time.Time
	spent, credits       float64
	spentKnown           bool
	quotaPercent         float64
	quotaUsed            *float64
	intent               string
	todos                TodoView
	failed               []string
}

// finishWrapLocked ends the wrap-up with replyID as the handoff, or with
// uam's facts alone (noReply says why). The facts' git and journal reads
// run outside the lock; the Thread shows Wrapping up until they are done.
func (m *Manager) finishWrapLocked(s *webSession, replyID, noReply string) {
	w := s.wrap
	if w == nil {
		w = &budgetWrap{why: "Stopped at its budget"}
		s.wrap = w
	}
	if w.done {
		return
	}
	w.done = true
	if w.promptWait != nil {
		w.promptWait.Stop()
	}
	in := handoffNotes{id: s.id, workdir: s.workdir, noReply: noReply, edits: maps.Clone(s.edits), turnStart: s.turnStart, started: s.createdAt,
		spent: s.budgetSpent(), spentKnown: s.usage != nil, credits: s.budget.Credits, quotaPercent: s.budget.QuotaPercent, intent: s.turnIntent, todos: s.turnActivity.Todos}
	if used, ok := m.quotaUsedLocked(s.provider); ok {
		in.quotaUsed = &used
	}
	in.failed = failedTools(s.items, w.turnStart, w.before)
	m.wg.Add(1)
	go func() {
		defer log.Recover("budget handoff", &m.mu)
		defer m.wg.Done()
		ctx, cancel := context.WithTimeout(m.ctx, handoffReadWait)
		repos := handoffFiles(ctx, in.workdir, in.edits, in.turnStart, in.started)
		split, _ := m.TaskTimeSplit(ctx, in.id)
		cancel()
		facts := in.markdown(repos, split)
		m.mu.Lock()
		defer m.mu.Unlock()
		if s.wrap != w || s.removed {
			return
		}
		before := m.summaryLocked(s)
		s.wrap, s.budget.WrapUp = nil, false
		s.handoff = &store.WebHandoff{At: m.now(), ReplyID: replyID, NoReply: noReply, Facts: facts}
		m.count("handoffs")
		m.changedLocked(s, before)
	}()
}

// failedTools names the tool calls that failed since from (the whole
// transcript when from is zero), newest last. Calls not in before started
// in the wrap-up, which denied them: they are left out.
func failedTools(items []agentapi.Item, from time.Time, before map[string]bool) []string {
	var out []string
	for _, it := range items {
		tc := it.Tool
		if tc == nil || tc.Status != agentapi.ToolFailed || it.Time.Before(from) || before != nil && !before[it.ID] {
			continue
		}
		command := toolCommand(tc)
		what := cmp.Or(command, inputPath(tc.Input), strings.Join(patchHeaderPaths(tc.Input), ", "), tc.Title)
		line := "`" + mdCode(tc.Name) + "`"
		if what != "" && what != tc.Name {
			line += ": " + "`" + mdCode(what) + "`"
		}
		if code := exitOf(tc); code != nil {
			line += fmt.Sprintf(" (exit %d)", *code)
		} else if msg := firstLine(tc.Output); command == "" && msg != "" {
			// A shell's first output line is seldom its error; another tool's output is.
			line += " — " + mdCode(msg)
		}
		if it.AgentID != "" {
			line += " (subagent)"
		}
		out = append(out, line)
	}
	if len(out) > maxHandoffFailed {
		out = out[len(out)-maxHandoffFailed:]
	}
	return out
}

// mdCode is text for a Markdown code span: one line, no backticks, clipped.
func mdCode(text string) string {
	text = strings.Join(strings.Fields(displaytext.Sanitize(text)), " ")
	return clipRunes(strings.ReplaceAll(text, "`", "'"), maxHandoffRunes)
}

// handoffFactsHead heads the facts; the memory pin leaves it out.
const handoffFactsHead = "## Facts\n\n_Recorded by uam, not written by the agent._\n\n"

// markdown is the handoff's facts: what uam recorded, without any model.
// What the next Thread acts on comes first: the open todos, the commits and
// the uncommitted files; then what the budget counted.
func (in handoffNotes) markdown(repos []handoffRepo, split *TimeSplit) string {
	var b strings.Builder
	b.WriteString(handoffFactsHead)
	if in.noReply != "" {
		fmt.Fprintf(&b, "- **Handoff:** the agent wrote none (%s).\n", in.noReply)
	}
	if in.intent != "" {
		fmt.Fprintf(&b, "- **Last intent:** %s\n", mdCode(in.intent))
	}
	if t := in.todos; t.Known && t.Counts.Total > 0 {
		c := t.Counts
		fmt.Fprintf(&b, "- **Todo list:** %d done, %d in progress, %d to do, %d blocked.\n", c.Done, c.InProgress, c.Pending, c.Blocked)
		// Only the open rows: the done ones are the agent's Done section.
		shown := 0
		for _, row := range t.Todos {
			note := ""
			switch row.Status {
			case agentapi.TodoDone:
				continue
			case agentapi.TodoInProgress:
				note = " (in progress)"
			case agentapi.TodoBlocked:
				note = " (blocked"
				if row.Note != "" {
					note += ": " + mdCode(row.Note)
				}
				note += ")"
			}
			if shown == maxHandoffTodos {
				fmt.Fprintf(&b, "  - and %d more open\n", c.Total-c.Done-maxHandoffTodos)
				break
			}
			shown++
			fmt.Fprintf(&b, "  - [ ] %s%s\n", mdCode(row.Title), note)
		}
	}
	writeHandoffGit(&b, repos)
	if len(in.failed) > 0 {
		fmt.Fprintf(&b, "- **Failed tool calls in the last turn:** %d\n", len(in.failed))
		for _, f := range in.failed {
			fmt.Fprintf(&b, "  - %s\n", f)
		}
	}
	if in.credits > 0 {
		spent := "spend not reported yet"
		if in.spentKnown {
			spent = creditText(in.spent) + " spent"
		}
		fmt.Fprintf(&b, "- **AI credits:** %s; budget %s, counted from when it was set.\n", spent, creditText(in.credits))
	}
	if in.quotaPercent > 0 {
		used := "not read yet"
		if in.quotaUsed != nil {
			used = creditText(*in.quotaUsed) + "% used"
		}
		fmt.Fprintf(&b, "- **Account quota:** %s, limit %s%%.\n", used, creditText(in.quotaPercent))
	}
	if split != nil && (split.ModelMS != nil || split.LocalMS > 0) {
		model := int64(0)
		if split.ModelMS != nil {
			model = *split.ModelMS
		}
		fmt.Fprintf(&b, "- **Time:** %s in the model, %s running tools.\n", time.Duration(model*int64(time.Millisecond)).Round(time.Second), time.Duration(split.LocalMS*int64(time.Millisecond)).Round(time.Second))
	}
	return strings.TrimRight(b.String(), "\n")
}

// writeHandoffGit writes the commits since the Thread started and the
// uncommitted files of repos, each path under its repository's label.
func writeHandoffGit(b *strings.Builder, repos []handoffRepo) {
	var commits, edited []string
	more, read, all, others := false, false, 0, 0
	for _, r := range repos {
		prefix := ""
		if r.label != "" {
			prefix = r.label + "/"
		}
		if r.err != nil {
			where := "the Thread's folder"
			if r.label != "" {
				where = "`" + mdCode(r.label) + "`"
			}
			fmt.Fprintf(b, "- **Git:** could not read %s (%s).\n", where, mdCode(shortError(r.err)))
			continue
		}
		if r.files.repo == nil {
			continue
		}
		read = true
		for _, c := range r.commits {
			hash, subject, _ := strings.Cut(c, "\t")
			line := "`" + mdCode(hash) + "` " + mdCode(subject)
			if r.label != "" {
				line = "`" + mdCode(r.label) + "` " + line
			}
			commits = append(commits, line)
		}
		more = more || r.more
		for _, f := range r.files.task {
			edited = append(edited, fmt.Sprintf("`%s` %s +%d −%d", mdCode(prefix+f.Path), f.Status, f.Additions, f.Deletions))
		}
		all += len(r.files.all.Files)
		others += len(r.files.all.Files) - len(r.files.task)
	}
	if !read {
		if len(repos) > 0 && repos[0].err == nil {
			b.WriteString("- **Files changed:** not known; the folder is not a git repository.\n")
		}
		return
	}
	if len(commits) > 0 {
		count := strconv.Itoa(len(commits))
		if more {
			count += " or more"
		}
		fmt.Fprintf(b, "- **Commits since the Thread started:** %s\n", count)
		writeCapped(b, commits, maxHandoffCommits)
	}
	switch {
	case all == 0:
		b.WriteString("- **Uncommitted changes:** none against `HEAD`.\n")
	case len(edited) == 0:
		b.WriteString("- **Files changed by this Thread:** none of its edits differ from `HEAD`.\n")
	default:
		fmt.Fprintf(b, "- **Files changed by this Thread** against `HEAD`: %d\n", len(edited))
		writeCapped(b, edited, maxHandoffFiles)
	}
	// The rest of the working trees (shell commands, other Threads, the
	// owner) as a count.
	if others > 0 {
		fmt.Fprintf(b, "- **Other changes in the folder:** %d %s from `HEAD` that this Thread's edit tools did not touch (shell commands or others may have).\n", others, plural(others, "file differs", "files differ"))
	}
}

// writeCapped writes lines as a nested list, at most limit of them.
func writeCapped(b *strings.Builder, lines []string, limit int) {
	for i, line := range lines {
		if i == limit {
			fmt.Fprintf(b, "  - and %d more\n", len(lines)-limit)
			return
		}
		fmt.Fprintf(b, "  - %s\n", line)
	}
}

// creditText writes AI units as the web UI does (formatCredits): "<0.01"
// for a tiny non-zero amount, two decimals below 1, one below 10.
func creditText(v float64) string {
	switch {
	case v == 0:
		return "0"
	case v < 0.005:
		return "<0.01"
	case v < 1:
		return strconv.FormatFloat(v, 'f', 2, 64)
	case v < 10:
		return strconv.FormatFloat(v, 'f', 1, 64)
	}
	return strconv.FormatFloat(math.Round(v), 'f', 0, 64)
}
