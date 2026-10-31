package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// The assist features (docs/web.md): replies suggested after a turn, the
// outcome line of a completed turn, Run again and Export as Markdown. A
// suggestion is picked by a Utility model call (Decide) when more than one
// reply fits a finished turn; everything else is built from the transcript.
const (
	// maxSuggestedReplies is one: the composer shows a single suggestion.
	maxSuggestedReplies    = 1
	maxSuggestedReplyRunes = 120
	// maxAssistInputRunes bounds each message a Utility prompt quotes.
	maxAssistInputRunes = 2000
	// assistTimeout bounds one Utility call once it has a slot.
	assistTimeout = 45 * time.Second
)

// purposeSuggestReply names the suggestion's Decide calls; the Utility log
// shows them as decision-suggest-reply.
const purposeSuggestReply = "suggest-reply"

// assistRoutes adds the assist endpoints.
func (s *Server) assistRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/sessions/{id}/suggestions", s.handleSuggestions)
	mux.HandleFunc("POST /api/sessions/{id}/rerun", s.handleRerun)
	mux.HandleFunc("POST /api/sessions/{id}/fork", s.handleFork)
	mux.HandleFunc("POST /api/sessions/{id}/fork/dismiss", s.handleDismissFork)
	mux.HandleFunc("GET /api/sessions/{id}/rewind/preview", s.handleRewindPreview)
	mux.HandleFunc("POST /api/sessions/{id}/rewind", s.handleRewind)
	mux.HandleFunc("POST /api/sessions/{id}/rewind/reconcile", s.handleRewindReconcile)
	mux.HandleFunc("POST /api/sessions/{id}/rewind/release", s.handleRewindRelease)
	mux.HandleFunc("POST /api/sessions/{id}/resend", s.handleResend)
	mux.HandleFunc("GET /api/sessions/{id}/export", s.handleExport)
}

// persistLocked writes s's durable state soon without counting it as Task
// activity, as a change of what persistKey holds would. The caller holds mu.
func (m *Manager) persistLocked(s *webSession) {
	if m.sessions[s.id] != s {
		return
	}
	m.dirty[s.id] = struct{}{}
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// assistInput is text quoted in a Utility prompt: sanitized, and when longer
// than maxAssistInputRunes its start and end, where messages state what they
// ask and what was done.
func assistInput(text string) string {
	text = strings.TrimSpace(displaytext.Sanitize(text))
	runes := []rune(text)
	if len(runes) <= maxAssistInputRunes {
		return text
	}
	half := maxAssistInputRunes / 2
	return string(runes[:half]) + "\n…\n" + string(runes[len(runes)-half:])
}

// lastMain returns the index of the last main-agent item of kind, at or
// before end, or -1.
func (s *webSession) lastMain(kind agentapi.ItemKind, end int) int {
	for i := min(end, len(s.items)-1); i >= 0; i-- {
		if it := s.items[i]; it.AgentID == "" && it.Kind == kind {
			return i
		}
	}
	return -1
}

// lastMainItem returns the index of the main agent's last item other than a
// thought or completion receipt, or -1. Both can follow the final answer.
func (s *webSession) lastMainItem() int {
	for i := len(s.items) - 1; i >= 0; i-- {
		if s.items[i].AgentID == "" && s.items[i].Kind != agentapi.ItemReasoning && s.items[i].Completion == nil {
			return i
		}
	}
	return -1
}

/* ---------- Suggested replies ---------- */

// suggestReplies reports whether replies are suggested after a turn.
func (s Settings) suggestReplies() bool { return s.SuggestReplies == nil || *s.SuggestReplies }

// suggestSetting is the stored form of the setting: nil when on, the
// default, so only turning it off writes anything.
func suggestSetting(on bool) *bool {
	if on {
		return nil
	}
	return new(bool)
}

// Suggestions is the POST /api/sessions/{id}/suggestions response: replies
// the owner would likely send next, for the main transcript ending with the
// item ItemID. Both are empty when none are offered.
type Suggestions struct {
	ItemID  string   `json:"item_id"`
	Replies []string `json:"replies"`
}

// suggestionRun is a suggestion call in flight for the transcript ending
// with itemID; done closes when it ends.
type suggestionRun struct {
	itemID string
	done   chan struct{}
}

// suggestableLocked returns the ID of the item s's main transcript ends
// with when replies may be suggested for it: replies are on and the Task
// finished (finishedItemLocked). Otherwise "". The caller holds mu.
func (m *Manager) suggestableLocked(s *webSession) string {
	if !m.settings.suggestReplies() {
		return ""
	}
	return s.finishedItemLocked()
}

// finishedItemLocked returns the ID of the item s's main transcript ends
// with when the Task is active, its last turn completed, nothing waits or is
// queued, and its transcript ends with an assistant message. Otherwise "".
// The caller holds mu.
func (s *webSession) finishedItemLocked() string {
	if s.stage != StageActive || s.state() != StateCompleted || len(s.queue) > 0 {
		return ""
	}
	last := s.lastMainItem()
	if last < 0 || s.items[last].Kind != agentapi.ItemAssistant || strings.TrimSpace(s.items[last].Text) == "" {
		return ""
	}
	return s.items[last].ID
}

// suggestionPrompt quotes the last user message and final assistant reply,
// each bounded by assistInput.
func (s *webSession) suggestionPrompt(last int) string {
	user := ""
	if i := s.lastMain(agentapi.ItemUser, last); i >= 0 {
		user = s.items[i].Text
	}
	return "<user_message>\n" + assistInput(user) + "\n</user_message>\n<agent_reply>\n" + assistInput(s.items[last].Text) + "\n</agent_reply>"
}

// replyChoices are the replies a finished turn may be offered, by ID, in
// the order they are offered (docs/web.md).
var replyChoices = []DecisionOption{
	{ID: "commit", Description: "Commit this"},
	{ID: "push", Description: "Push it"},
	{ID: "run-tests", Description: "Run the tests"},
	{ID: "fix-tests", Description: "Fix the failing tests"},
	{ID: "fix-failed", Description: "Fix the failed command"},
	{ID: "follow-recommendation", Description: "Follow your recommendation"},
	{ID: "status", Description: "Show me the status"},
	{ID: "yes", Description: "Yes, go ahead"},
	{ID: "no", Description: "No, leave it"},
}

const suggestReplyInstructions = `Pick the reply the user would most likely send next, judging from their last message and the agent's final reply. Each option is that reply's text. Return null when none fits.`

// recommendsRE is a reply making an explicit recommendation.
var recommendsRE = regexp.MustCompile(`(?i)\bI(?:['’]d| would)? recommend\b|\bmy recommendation\b|\brecommended:|\(recommended\)`)

// gitLabRunning are the GitLab pipeline states of work still to finish, as
// the GitLab strip reads them.
var gitLabRunning = []string{"created", "waiting_for_resource", "preparing", "pending", "running", "scheduled"}

// replyCandidatesLocked returns the replies that fit s's finished turn,
// from its evidence f, the folder's cached unsaved work, its final reply
// and work still running after it (ciRunning: a GitLab pipeline of a commit
// it pushed). The caller holds mu.
func (m *Manager) replyCandidatesLocked(s *webSession, f turnFacts, ciRunning bool) []DecisionOption {
	var pending GitPending
	if p := m.gitPending[m.pendingTop[s.workdir]]; p != nil {
		pending = *p
	}
	final := s.items[s.lastMainItem()].Text
	all, _ := sentences(final)
	question := len(all) > 0 && strings.HasSuffix(all[len(all)-1], "?")
	tests := f.tests()
	fits := map[string]bool{
		"commit":                len(f.edits) > 0 && pending.Uncommitted > 0 && tests != outcomeFail && f.failed == 0,
		"push":                  pending.Unpushed > 0 && f.committed,
		"run-tests":             len(f.edits) > 0 && tests == "",
		"fix-tests":             tests == outcomeFail,
		"fix-failed":            f.failed > 0 && tests != outcomeFail,
		"follow-recommendation": recommendsRE.MatchString(inlineCodeRE.ReplaceAllString(codeFenceRE.ReplaceAllString(final, "\n"), "")),
		"status":                ciRunning || s.workRunning(),
		"yes":                   question,
		"no":                    question,
	}
	var out []DecisionOption
	for _, c := range replyChoices {
		if fits[c.ID] {
			out = append(out, c)
		}
	}
	return out
}

// workRunning reports whether a background shell or a subagent of s still
// runs, as its provider last reported them.
func (s *webSession) workRunning() bool {
	if s.backgroundTasks != nil && slices.ContainsFunc(s.backgroundTasks.Tasks, func(t agentapi.BackgroundTask) bool { return t.Status == "running" }) {
		return true
	}
	return slices.ContainsFunc(s.subagents, func(sa *agentapi.Subagent) bool { return sa.Status == agentapi.SubagentRunning })
}

// gitLabPipelineRunning reports whether Thread id's cached GitLab state has
// a pipeline still running for a commit recorded since since. It reads
// nothing from GitLab: only what the GitLab strip last fetched.
func (m *Manager) gitLabPipelineRunning(ctx context.Context, id string, since time.Time) bool {
	st, err := m.gitLabState(ctx, id, false)
	return err == nil && st.Commit != nil && !st.Commit.CreatedAt.Before(since) && st.Pipeline != nil && slices.Contains(gitLabRunning, st.Pipeline.Status)
}

// SuggestReplies returns the reply suggested for Task id's last completed
// turn. Code lists the replies that fit what the turn did
// (replyCandidatesLocked): none means none, one is the suggestion, and more
// are put to one Decide call, which may pick none. The answer is kept by
// the item the transcript ends with, so the same state is never asked for
// twice, a failure included. A concurrent request for the same state waits
// for the first. When replies are not offered, the answer is empty.
func (m *Manager) SuggestReplies(ctx context.Context, id string) (Suggestions, error) {
	none := Suggestions{Replies: []string{}}
	m.mu.Lock()
	s := m.sessions[id]
	if s == nil {
		m.mu.Unlock()
		return none, newError(http.StatusNotFound, msgSessionNotFound)
	}
	key := m.suggestableLocked(s)
	switch {
	case key == "" || m.closed:
		m.mu.Unlock()
		return none, nil
	case s.suggestions != nil && s.suggestions.ItemID == key:
		out := Suggestions{ItemID: key, Replies: append([]string{}, s.suggestions.Replies...)}
		m.mu.Unlock()
		return out, nil
	case s.suggesting != nil && s.suggesting.itemID == key:
		run := s.suggesting
		m.mu.Unlock()
		select {
		case <-run.done:
		case <-ctx.Done():
			return none, ctx.Err()
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		if s.suggestions != nil && s.suggestions.ItemID == key {
			return Suggestions{ItemID: key, Replies: append([]string{}, s.suggestions.Replies...)}, nil
		}
		return none, nil
	}
	// Shutdown waits for the reads and the call, and for their state changes.
	m.titles.Add(1)
	defer m.titles.Done()
	run := &suggestionRun{itemID: key, done: make(chan struct{})}
	s.suggesting = run
	facts, _ := s.turnFacts()
	workdir, since := s.workdir, s.turnStart
	m.mu.Unlock()

	// The work outlives a request that gives up: its answer is kept for the
	// next look. The unsaved work is read now, as the turn left it.
	ctx, stop := m.bound(context.WithoutCancel(ctx))
	defer stop()
	if len(facts.edits) > 0 || facts.committed {
		m.refreshGitPending(ctx, workdir)
	}
	ciRunning := facts.pushed && m.gitLabPipelineRunning(ctx, id, since)
	m.mu.Lock()
	var options []DecisionOption
	var evidence string
	// A turn begun or a rewind meanwhile leaves this state nothing to suggest.
	if m.suggestableLocked(s) == key {
		options = m.replyCandidatesLocked(s, facts, ciRunning)
		evidence = s.suggestionPrompt(s.lastMainItem())
	}
	m.mu.Unlock()

	replies := []string{}
	var err error
	switch len(options) {
	case 0:
	case 1:
		replies = append(replies, options[0].Description)
	default:
		var picked DecisionResult
		picked, err = m.Decide(ctx, DecisionRequest{Purpose: purposeSuggestReply, Instructions: suggestReplyInstructions, Input: evidence, Options: options, TaskID: id})
		for _, o := range options {
			if err == nil && o.ID == picked.Choice {
				replies = append(replies, o.Description)
			}
		}
	}
	var webErr *Error
	paused := errors.As(err, &webErr) && webErr.Code == codeUtilityPaused
	switch {
	case err == nil:
		m.suggestPauseLogged.Store(false)
		log.Info("reply suggested", "session", id, "candidates", len(options), "replies", len(replies))
	case paused && m.suggestPauseLogged.Swap(true):
		log.Debug("no reply suggested", "session", id, "error", err)
	default:
		m.suggestPauseLogged.Store(paused)
		log.Info("no reply suggested", "session", id, "candidates", len(options), "error", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.suggesting == run {
		s.suggesting = nil
	}
	close(run.done)
	// Past today's Utility limit, or without a Utility model, none are shown
	// and none are kept: a look once a call may run asks again.
	if m.closed || s.removed || paused || errors.Is(err, errUtilityUnavailable) {
		return none, nil
	}
	s.suggestions = &store.WebSuggestions{ItemID: key, Replies: replies}
	m.persistLocked(s)
	return Suggestions{ItemID: key, Replies: append([]string{}, replies...)}, nil
}

// loadSuggestions keeps stored suggestions that are well formed.
func loadSuggestions(stored *store.WebSuggestions) *store.WebSuggestions {
	if stored == nil || stored.ItemID == "" || len(stored.ItemID) > maxToolCallID {
		return nil
	}
	out := &store.WebSuggestions{ItemID: stored.ItemID, Replies: []string{}}
	for _, r := range stored.Replies {
		if r = clipRunes(strings.TrimSpace(displaytext.Sanitize(r)), maxSuggestedReplyRunes); r != "" && len(out.Replies) < maxSuggestedReplies {
			out.Replies = append(out.Replies, r)
		}
	}
	return out
}

func (s *Server) handleSuggestions(w http.ResponseWriter, r *http.Request) {
	out, err := s.m.SuggestReplies(r.Context(), r.PathValue("id"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	if len(out.Replies) > 0 {
		s.m.count("suggested_replies_shown")
	}
	writeJSON(w, http.StatusOK, out)
}

/* ---------- Outcome line ---------- */

// maxOutcomeRunes bounds a stored outcome line.
const maxOutcomeRunes = 200

// turnOutcome is what an outcome line says of a turn's evidence
// (turn_evidence.go), as the finish card shows it: the files the Task's
// edit tools changed, the result of its last test run ("" when it ran none
// or that run's result is unclear), and its commands that failed.
type turnOutcome struct {
	files  int
	tests  string
	failed int
}

func (f turnFacts) outcome(workdir string) turnOutcome {
	return turnOutcome{files: f.files(workdir), tests: f.tests(), failed: f.failed}
}

// outcomeLine joins a verb phrase, which may be empty, with what the
// evidence shows, and starts the line with a capital letter.
func outcomeLine(verb string, ev turnOutcome) string {
	var parts []string
	if verb != "" {
		parts = append(parts, verb)
	}
	if n := ev.files; n > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", n, plural(n, "file changed", "files changed")))
	}
	switch ev.tests {
	case outcomePass:
		parts = append(parts, "tests pass")
	case outcomeFail:
		parts = append(parts, "tests fail")
	}
	if ev.failed > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", ev.failed, plural(ev.failed, "command failed", "commands failed")))
	}
	line := strings.Join(parts, "; ")
	if r, size := utf8.DecodeRuneInString(line); size > 0 {
		line = string(unicode.ToUpper(r)) + line[size:]
	}
	return clipRunes(line, maxOutcomeRunes)
}

// outcomeTurnLocked updates s's outcome line for a turn transition: a turn
// that starts clears it, and one that completes gets the line its evidence
// shows, such as "1 file changed; tests pass". A matching accepted native
// summary supplies a phrase before it; a turn with no edits or commands
// reads "Answered". No model is asked. The caller holds mu.
func (m *Manager) outcomeTurnLocked(s *webSession, state agentapi.TurnState, completion *agentapi.TaskCompletion) {
	s.outcome = ""
	if state != agentapi.TurnCompleted {
		return
	}
	facts, ok := s.turnFacts()
	ev := facts.outcome(s.workdir)
	if ok {
		s.outcome = outcomeLine("", ev)
	}
	if c := checkCompletion(completion); c != nil && c.Decision == agentapi.CompletionAccepted && c.UserItemID != "" && len(s.turnTimings) > 0 {
		ended := s.turnTimings[len(s.turnTimings)-1]
		if ended.State == string(agentapi.TurnCompleted) && ended.UserItemID == c.UserItemID && !ended.EndedAt.IsZero() {
			if summary := cleanGeneratedTitle(c.Summary); summary != "" {
				s.outcome = outcomeLine(summary, ev)
				return
			}
		}
	}
	last := s.lastMainItem()
	if !ok || last < 0 || s.items[last].Kind != agentapi.ItemAssistant || strings.TrimSpace(s.items[last].Text) == "" {
		return
	}
	if len(facts.edits) == 0 && facts.commands == 0 {
		s.outcome = outcomeLine("Answered", ev)
	}
}

/* ---------- Run again ---------- */

// RerunRequest is the POST /api/sessions/{id}/rerun body. Model, when set,
// replaces the Task's model (Try with another model); RequestID makes a
// repeated request return the Task the first one created.
type RerunRequest struct {
	Model     string `json:"model"`
	RequestID string `json:"request_id"`
}

// maxRerunRead bounds each recorded window searched for the last message
// when that message is no longer held.
const maxRerunRead = 200

// Rerun creates a Task in Task id's Project with its settings, the model
// req names when it names one, and its last message as the first message.
// The new Task records id as rerun_of. A message's attachments are not sent
// again. The effort and context size go back to their defaults with another
// model.
func (m *Manager) Rerun(id string, req RerunRequest) (SessionSummary, error) {
	s, err := m.lookup(id)
	if err != nil {
		return SessionSummary{}, err
	}
	if err := m.waitReleasedHistory(m.ctx, s); err != nil {
		return SessionSummary{}, err
	}
	m.mu.Lock()
	if s.removed {
		m.mu.Unlock()
		return SessionSummary{}, newError(http.StatusNotFound, msgSessionNotFound)
	}
	create := CreateRequest{ProjectID: s.projectID, Provider: s.provider, Model: s.model, Effort: s.effort, ContextSize: s.contextSize, Agent: s.agent, Mode: string(s.mode), RequestID: req.RequestID, rerunOf: s.id}
	if req.Model != "" && req.Model != s.model {
		create.Model, create.Effort, create.ContextSize = req.Model, "", "default"
	}
	if s.place != nil && s.place.Worktree != "" {
		// It runs in the same worktree, on the same branch.
		place := *s.place
		create.samePlace, create.sameWorkdir = &place, s.workdir
	}
	text := ""
	for i := len(s.items) - 1; i >= 0 && text == ""; i-- {
		if ownerMessage(s.items[i]) {
			text = s.items[i].Text
		}
	}
	var read *archiveRead
	if text == "" && m.pagerLocked(s) != nil {
		read = m.windowReadLocked(s, "", "", maxRerunRead, 0)
	}
	m.mu.Unlock()
	for read != nil {
		w, err := m.readArchive(read)
		if err != nil {
			return SessionSummary{}, err
		}
		for i := len(w.items) - 1; i >= 0 && text == ""; i-- {
			if ownerMessage(w.items[i]) {
				text = w.items[i].Text
			}
		}
		if text != "" || w.start || len(w.items) == 0 || w.items[0].ID == read.req.ItemID {
			break
		}
		read.req.ItemID = w.items[0].ID
	}
	if strings.TrimSpace(text) == "" {
		return SessionSummary{}, newError(http.StatusConflict, "this thread has no message to run again")
	}
	create.Prompt = text
	return m.Create(create)
}

// ownerMessage reports whether it is a message the owner sent the main
// agent, not one the CLI generated to continue an autopilot turn.
func ownerMessage(it agentapi.Item) bool {
	return it.AgentID == "" && it.Kind == agentapi.ItemUser && it.Delivery != agentapi.DeliveryAutopilot
}

func (s *Server) handleRerun(w http.ResponseWriter, r *http.Request) {
	var req RerunRequest
	if !decodeBody(w, r, &req) {
		return
	}
	summary, err := s.m.Rerun(r.PathValue("id"), req)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, summary)
}
