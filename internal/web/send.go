package web

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

var requestIDRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func validRequestID(id string) bool { return requestIDRE.MatchString(id) }

// turnInput is what a submission sends: a prompt, or, when command is set,
// that command with text as its arguments. Files are relative project
// paths; attachments are upload IDs of the Task.
type turnInput struct {
	text, command      string
	files, attachments []string
	settings           *PromptSettings
	// peer is the peer message the prompt delivers (messages.go).
	peer *store.TurnPeer
}

// Submit sends one prompt in mode: ModeSend ("" too), ModeQueue or
// ModeSteer. A prompt needs text, a file or an attachment. A repeated request
// ID returns the recorded outcome, or "queued" while the prompt waits in the
// queue, without contacting the provider.
func (m *Manager) Submit(id string, req PromptRequest) (Submission, error) {
	if !validRequestID(req.RequestID) {
		return Submission{}, newError(http.StatusBadRequest, msgRequestIDNotUUID)
	}
	mode := req.Mode
	switch mode {
	case "":
		mode = ModeSend
	case ModeSend, ModeQueue, ModeSteer:
	default:
		return Submission{}, newError(http.StatusBadRequest, "mode must be send, queue or steer")
	}
	if strings.TrimSpace(req.Text) == "" {
		if len(req.Files) == 0 && len(req.Attachments) == 0 {
			return Submission{}, newError(http.StatusBadRequest, "prompt text, a file or an attachment is required")
		}
		// A message of only files or uploads goes with no text, not blanks.
		req.Text = ""
	}
	if len(req.Text) > maxPromptBytes {
		return Submission{}, newError(http.StatusRequestEntityTooLarge, msgPromptTooLarge)
	}
	s, err := m.lookup(id)
	if err != nil {
		return Submission{}, err
	}
	if err := m.refuseSignedOut(s, req.Settings); err != nil {
		return Submission{}, err
	}
	return m.submit(s, turnInput{text: req.Text, files: req.Files, attachments: req.Attachments, settings: req.Settings}, req.RequestID, mode)
}

var errTurnRunning = newError(http.StatusConflict, "a turn is already running in this session")

// turnRunning reports whether state is a running turn: working or waiting for
// the user. Opening a conversation (starting) is not a turn.
func turnRunning(state string) bool {
	return state == StateWorking || state == StateAwaitingPermission || state == StateAwaitingAnswer
}

func (m *Manager) submit(s *webSession, in turnInput, reqID, mode string) (Submission, error) {
	s.op.Lock()
	defer s.op.Unlock()
	m.mu.Lock()
	if sub, ok := s.findRequest(reqID); ok {
		m.mu.Unlock()
		return sub, nil
	}
	if s.removed {
		m.mu.Unlock()
		return Submission{}, newError(http.StatusNotFound, msgSessionNotFound)
	}
	if m.closed {
		m.mu.Unlock()
		return Submission{}, errShuttingDown
	}
	if err := s.readOnlyLocked(); err != nil {
		m.mu.Unlock()
		return Submission{}, err
	}
	if err := s.rewindHoldLocked(); err != nil {
		m.mu.Unlock()
		return Submission{}, err
	}
	if s.budget.Reached {
		m.mu.Unlock()
		return Submission{}, errBudgetReached
	}
	workdir := s.workdir
	m.mu.Unlock()
	// Checked before anything is queued or sent; a queued prompt's files are
	// checked again when it is sent.
	if _, err := checkFiles(workdir, in.files); err != nil {
		return Submission{}, err
	}
	m.mu.Lock()
	state, conv := s.state(), s.conv
	willQueue := mode == ModeQueue && (turnRunning(state) || s.queueSending != "" || (len(s.queue) > 0 && !s.queuePaused))
	current := PromptSettings{Model: s.model, Effort: s.effort, ContextSize: cmp.Or(s.contextSize, "default")}
	selection := current
	if in.command == "" {
		if in.settings != nil {
			selection = *in.settings
			selection.ContextSize = cmp.Or(selection.ContextSize, "default")
		}
		// An immediate continuation keeps the persisted provider selection.
		// New selections and queued snapshots must use the current catalog.
		if selection != current || willQueue {
			if err := m.validateSelectionLocked(s.provider, selection.Model, selection.Effort, selection.ContextSize); err != nil {
				m.mu.Unlock()
				return Submission{}, err
			}
		}
		if selection.Model == "" && s.model != "" {
			m.mu.Unlock()
			return Submission{}, newError(http.StatusBadRequest, "model must be an offered model ID")
		}
		in.settings = &selection
	}
	if mode == ModeSteer && turnRunning(state) && selection != current {
		m.mu.Unlock()
		return Submission{}, newError(http.StatusConflict, "a running steer uses the current model settings; queue a next turn to change them")
	}
	uploads, err := m.checkUploadsForModelLocked(s, in.attachments, selection.Model)
	if err != nil {
		m.mu.Unlock()
		return Submission{}, err
	}
	switch {
	// Behind queued prompts that are about to be sent, a queued prompt waits
	// its turn even when no turn is running.
	case willQueue:
		defer m.mu.Unlock()
		m.count("prompt_queued")
		m.countLargeText(in.text, uploads)
		return m.enqueueLocked(s, in, uploads, reqID)
	case mode == ModeSteer && turnRunning(state):
		m.mu.Unlock()
		m.count("prompt_steered")
		m.countLargeText(in.text, uploads)
		return m.steer(s, conv, in, uploads, reqID)
	case busy(state) || s.queueSending != "":
		m.mu.Unlock()
		return Submission{}, errTurnRunning
	}
	m.mu.Unlock()
	if selection == current {
		// Let checkedPrompt/openLocked retain their saved-model and holder
		// checks. A queued snapshot keeps settings for dispatch validation.
		in.settings = nil
	}
	m.countLargeText(in.text, uploads)
	return m.send(s, in, reqID)
}

// send starts a turn with in. The caller holds s.op. An error means nothing
// was sent and nothing was recorded; otherwise the outcome is recorded, and a
// prompt that was not accepted pauses the queue.
func (m *Manager) send(s *webSession, in turnInput, reqID string) (Submission, error) {
	if sub, found, err := m.checkedPrompt(s, reqID); found || err != nil {
		return sub, err
	}
	if err := m.openLocked(s, true); err != nil {
		if errors.Is(err, errShuttingDown) {
			return Submission{}, err
		}
		_, msg := errorStatus(err)
		return m.recordSubmission(s, reqID, SubmissionRejected, msg, true), nil
	}
	if in.settings != nil {
		m.mu.Lock()
		running := busy(s.state())
		current := PromptSettings{Model: s.model, Effort: s.effort, ContextSize: cmp.Or(s.contextSize, "default")}
		selectionErr := m.validateSelectionLocked(s.provider, in.settings.Model, in.settings.Effort, in.settings.ContextSize)
		m.mu.Unlock()
		if running {
			return Submission{}, errTurnRunning
		}
		if selectionErr != nil {
			_, msg := errorStatus(selectionErr)
			return m.recordSubmission(s, reqID, SubmissionRejected, msg, true), nil
		}
		// An unchanged empty model is a legacy provider-default selection.
		// It needs no switch (SetModel intentionally rejects a new empty ID).
		if current != *in.settings {
			if _, err := m.setModelLocked(s, &in.settings.Model, &in.settings.Effort, &in.settings.ContextSize); err != nil {
				_, msg := errorStatus(err)
				return m.recordSubmission(s, reqID, SubmissionRejected, msg, true), nil
			}
		}
	}

	m.mu.Lock()
	conv, workdir := s.conv, s.workdir
	uploads, err := m.checkUploadsLocked(s, in.attachments)
	m.mu.Unlock()
	realWorkdir := realPath(workdir)
	if conv == nil {
		return m.recordSubmission(s, reqID, SubmissionRejected, msgConversationNotOpen, true), nil
	}
	var files []agentapi.File
	var blobs []agentapi.Blob
	if err == nil {
		files, err = checkFiles(workdir, in.files)
	}
	if err == nil {
		blobs, err = m.readBlobs(s.id, uploads)
	}
	if err != nil {
		_, msg := errorStatus(err)
		return m.recordSubmission(s, reqID, SubmissionRejected, msg, true), nil
	}
	if in.command != "" {
		if err := m.commandOffered(conv, in.command); err != nil {
			return Submission{}, err
		}
	}

	m.mu.Lock()
	// No turn starts writing in a repository while a commit or a pull runs
	// there: it waits for the git action to end.
	for done := m.gitWriteLocked(realWorkdir); done != nil; done = m.gitWriteLocked(realWorkdir) {
		m.mu.Unlock()
		select {
		case <-done:
		case <-m.ctx.Done():
			return Submission{}, errShuttingDown
		}
		m.mu.Lock()
	}
	if s.conv != conv {
		m.mu.Unlock()
		return m.recordSubmission(s, reqID, SubmissionRejected, msgConversationNotOpen, true), nil
	}
	// Reopening can surface a turn or request the provider still has.
	if busy(s.state()) {
		m.mu.Unlock()
		return Submission{}, errTurnRunning
	}
	if in.peer != nil {
		// The delivery's commit point (peerBlockedLocked): the pair must
		// still allow it after the open and the git wait.
		if why := m.peerBlockedLocked(s, in.peer); why != "" {
			m.mu.Unlock()
			return Submission{}, peerBlockedError(why)
		}
	}
	before := m.summaryLocked(s)
	prevBase, prevDetail, prevKind := s.base, s.detail, s.failKind
	plan := m.titlePlanLocked(s, in, uploads)
	if plan != nil && plan.afterReply {
		// Set before the send, so a turn that ends at once still finds it.
		s.titleWait = plan
	}
	fallback := ""
	if s.sendFallbackLocked(in, uploads) {
		fallback = s.title
	}
	if s.peerArm != nil {
		// An uncertain send left it armed, and no turn started from it
		// before this send: the provider did not take it.
		m.peerEndLocked(s.peerArm, peerFailure(s.peerArm), "the provider did not start its turn")
		s.peerArm = nil
	}
	if in.peer != nil {
		// Before the send: the turn's events can come before it returns.
		s.peerArm = in.peer
	}
	s.setBase(StateWorking, "")
	mark := s.turnSeq
	m.changedLocked(s, before)
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(m.ctx, sendTimeout)
	prompt := agentapi.Prompt{Text: in.text, Files: files, Attachments: blobs}
	if in.command == "" {
		err = conv.Send(ctx, prompt)
	} else {
		err = conv.RunCommand(ctx, in.command, prompt)
	}
	cancel()
	if err == nil || errors.Is(err, agentapi.ErrSubmissionUncertain) {
		m.markUsed(s, uploads)
	}
	if err == nil {
		if plan != nil && !plan.afterReply || fallback != "" {
			m.mu.Lock()
			before := m.summaryLocked(s)
			if plan != nil && !plan.afterReply {
				m.startTitleLocked(s, plan, blobs, "")
			}
			m.changedLocked(s, before)
			m.mu.Unlock()
		}
		return m.recordSubmission(s, reqID, SubmissionAccepted, "", false), nil
	}
	m.mu.Lock()
	before = m.summaryLocked(s)
	// An uncertain send keeps its arm and reservation: a turn that starts
	// is the message's; the next send, the conversation's end or a restart
	// ends it otherwise.
	if in.peer != nil && s.peerArm == in.peer && !errors.Is(err, agentapi.ErrSubmissionUncertain) {
		s.peerArm = nil
	}
	if plan != nil && s.titleWait == plan {
		s.titleWait = nil
	}
	// A message the provider surely did not take leaves no title behind.
	if fallback != "" && !errors.Is(err, agentapi.ErrSubmissionUncertain) && s.titleFallback && s.title == fallback {
		s.title, s.titleFallback = "", false
	}
	if s.turnSeq == mark {
		s.base, s.detail, s.failKind = prevBase, prevDetail, prevKind
	}
	m.changedLocked(s, before)
	m.mu.Unlock()
	log.Warn("web prompt submission failed", "session", s.id, "error", err)
	switch {
	case errors.Is(err, agentapi.ErrBusy):
		return Submission{}, newError(http.StatusConflict, "the provider is still running a turn")
	case errors.Is(err, agentapi.ErrUnsupported):
		return Submission{}, newError(http.StatusConflict, "this provider has no commands")
	case errors.Is(err, agentapi.ErrSubmissionUncertain):
		// Never resubmit: the provider may already be working on it.
		return m.recordSubmission(s, reqID, SubmissionUncertain,
			"the provider may or may not have received this prompt, and uam did not resend it: "+shortError(err), true), nil
	default:
		return m.recordSubmission(s, reqID, SubmissionRejected, shortError(err), true), nil
	}
}

// steer adds in, with its files and uploads, to the running turn. The turn
// state does not change: the turn the steer joins reports its own end. The
// caller holds s.op.
func (m *Manager) steer(s *webSession, conv agentapi.Conversation, in turnInput, uploads []*upload, reqID string) (Submission, error) {
	if conv == nil {
		return m.recordSubmission(s, reqID, SubmissionRejected, msgConversationNotOpen, false), nil
	}
	if sub, found, err := m.checkedPrompt(s, reqID); found || err != nil {
		return sub, err
	}
	m.mu.Lock()
	workdir := s.workdir
	m.mu.Unlock()
	files, err := checkFiles(workdir, in.files)
	var blobs []agentapi.Blob
	if err == nil {
		blobs, err = m.readBlobs(s.id, uploads)
	}
	if err != nil {
		return Submission{}, err
	}
	ctx, cancel := context.WithTimeout(m.ctx, sendTimeout)
	err = conv.Steer(ctx, agentapi.Prompt{Text: in.text, Files: files, Attachments: blobs})
	cancel()
	if err == nil || errors.Is(err, agentapi.ErrSubmissionUncertain) {
		m.markUsed(s, uploads)
	}
	switch {
	case err == nil:
		return m.recordSubmission(s, reqID, SubmissionAccepted, "", false), nil
	case errors.Is(err, agentapi.ErrUnsupported):
		return Submission{}, newError(http.StatusConflict, "this provider cannot steer a running turn")
	}
	log.Warn("web steer submission failed", "session", s.id, "error", err)
	if errors.Is(err, agentapi.ErrSubmissionUncertain) {
		// Never resubmit: the provider may already have folded it in.
		return m.recordSubmission(s, reqID, SubmissionUncertain,
			"the provider may or may not have received this steer, and uam did not resend it: "+shortError(err), false), nil
	}
	return m.recordSubmission(s, reqID, SubmissionRejected, shortError(err), false), nil
}

func (s *webSession) findSubmission(reqID string) (Submission, bool) {
	for i := len(s.submissions) - 1; i >= 0; i-- {
		if s.submissions[i].RequestID == reqID {
			return s.submissions[i], true
		}
	}
	return Submission{}, false
}

// findRequest returns the recorded outcome of reqID, or "queued" while it
// waits in the queue.
func (s *webSession) findRequest(reqID string) (Submission, bool) {
	for i := len(s.commandSubmissions) - 1; i >= 0; i-- {
		if s.commandSubmissions[i].RequestID == reqID {
			return s.commandSubmissions[i], true
		}
	}
	if sub, ok := s.findSubmission(reqID); ok {
		return sub, true
	}
	for _, q := range s.queue {
		if q.RequestID == reqID {
			return Submission{RequestID: reqID, Status: SubmissionQueued, Time: q.QueuedAt}, true
		}
	}
	return Submission{}, false
}

func (s *webSession) hasSubmission(reqID string) bool {
	_, ok := s.findRequest(reqID)
	return ok
}

// remember keeps sub for repeated request IDs, within maxSubmissions.
func (s *webSession) remember(sub Submission) {
	s.submissions = append(s.submissions, sub)
	if len(s.submissions) > maxSubmissions {
		s.submissions = append([]Submission(nil), s.submissions[len(s.submissions)-maxSubmissions:]...)
	}
}

// recordSubmission records a prompt's outcome. pauseQueue pauses a non-empty
// queue when the outcome is not accepted: queued work never follows a prompt
// that did not start its turn.
func (m *Manager) recordSubmission(s *webSession, reqID, status, msg string, pauseQueue bool) Submission {
	sub := Submission{RequestID: reqID, Status: status, Error: msg, Time: m.now()}
	m.mu.Lock()
	before := m.summaryLocked(s)
	s.remember(sub)
	// Another Thread's message is not the owner's: the composer never shows
	// its outcome.
	if !isPeerRequest(reqID) {
		last := sub
		s.last = &last
		m.broadcastLocked("submission", s.id, func(seq uint64) any {
			return submissionEvent{Seq: seq, SessionID: s.id, Submission: sub}
		})
	}
	if pauseQueue && status != SubmissionAccepted {
		m.pauseQueueLocked(s)
	}
	m.changedLocked(s, before)
	m.mu.Unlock()
	// The request ID must be durable before the browser hears the outcome,
	// so a restarted service still recognizes a retry.
	if err := m.flush(); err != nil {
		log.Warn("persist web submission failed", "session", s.id, "error", err)
	}
	return sub
}
