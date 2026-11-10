package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

func (s *webSession) queueSnapshot() []QueuedPrompt {
	return append([]QueuedPrompt{}, s.queue...)
}

// savedQueue is the part of the queue that is saved with the Task: the
// owner's prompts. Another Thread's message ends as interrupted when the
// service starts again (loadPairsLocked), so it is left out.
func (s *webSession) savedQueue() []QueuedPrompt {
	var out []QueuedPrompt
	for _, q := range s.queue {
		if q.From == nil {
			out = append(out, q)
		}
	}
	return out
}

// loadedQueue is a saved queue made safe to hold: the owner's prompts, in
// order, each within the bounds it had to meet when it was queued. Its files,
// attachments and settings are checked again when it is sent.
func loadedQueue(saved json.RawMessage) []QueuedPrompt {
	var stored, queue []QueuedPrompt
	if len(saved) == 0 || json.Unmarshal(saved, &stored) != nil {
		return nil
	}
	for _, q := range stored {
		empty := strings.TrimSpace(q.Text) == "" && len(q.Files) == 0 && len(q.Attachments) == 0
		known := slices.ContainsFunc(queue, func(k QueuedPrompt) bool { return k.RequestID == q.RequestID })
		if len(queue) == maxQueue || !validRequestID(q.RequestID) || known || q.From != nil || empty ||
			len(q.Text) > maxPromptBytes || len(q.Files) > maxFileRefs || len(q.Attachments) > maxAttachments {
			continue
		}
		queue = append(queue, q)
	}
	return queue
}

// enqueueLocked adds a prompt to the queue. Nothing reaches the provider
// before the running turn completes.
func (m *Manager) enqueueLocked(s *webSession, in turnInput, uploads []*upload, reqID string) (Submission, error) {
	if len(s.queue) >= maxQueue {
		return Submission{}, newError(http.StatusConflict, "the queue is full (%d prompts)", maxQueue)
	}
	before := m.summaryLocked(s)
	q := QueuedPrompt{RequestID: reqID, Text: in.text, QueuedAt: m.now(), Files: in.files, Attachments: uploadInfos(uploads), Settings: *in.settings}
	s.queue = append(s.queue, q)
	s.queueChanged = true
	m.changedLocked(s, before)
	if !busy(s.state()) {
		m.kickDrainLocked(s)
	}
	return Submission{RequestID: reqID, Status: SubmissionQueued, Time: q.QueuedAt}, nil
}

// pauseQueueLocked keeps a non-empty queue from draining until the user
// resumes or clears it. The caller publishes the change.
func (m *Manager) pauseQueueLocked(s *webSession) {
	if len(s.queue) > 0 && !s.queuePaused {
		s.queuePaused = true
		s.queueChanged = true
	}
}

// kickDrainLocked has the queue's head sent once nothing else holds s.op.
// The drain checks everything again first, so a spare kick is harmless.
func (m *Manager) kickDrainLocked(s *webSession) {
	if m.closed || s.removed || len(s.queue) == 0 || s.queuePaused {
		return
	}
	m.wg.Add(1)
	go func() {
		defer log.Recover("queue drain", &m.mu)
		defer m.wg.Done()
		m.drain(s)
	}()
}

// drain sends the queue's head through the normal submit path, with its own
// request ID, when no turn is running. The head leaves the queue before it is
// sent: from then on it cannot be cancelled, and it is never resent.
func (m *Manager) drain(s *webSession) {
	s.op.Lock()
	defer s.op.Unlock()
	m.mu.Lock()
	if m.closed || s.removed || len(s.queue) == 0 || s.queuePaused || s.queueSending != "" || busy(s.state()) || s.budget.Reached {
		m.mu.Unlock()
		return
	}
	before := m.summaryLocked(s)
	head := s.queue[0]
	s.queueSending = head.RequestID
	s.queue = slices.Delete(s.queue, 0, 1)
	s.queueChanged = true
	m.changedLocked(s, before)
	m.mu.Unlock()
	in := turnInput{text: head.Text, files: head.Files, settings: &head.Settings}
	for _, a := range head.Attachments {
		in.attachments = append(in.attachments, a.ID)
	}
	if head.From != nil {
		// Another Thread's message runs on this Thread's current settings.
		in.settings, in.peer = nil, head.From
		m.mu.Lock()
		why := m.peerBlockedLocked(s, head.From)
		if why != "" {
			before := m.summaryLocked(s)
			m.peerEndLocked(head.From, peerFailure(head.From), why)
			s.queueSending = ""
			m.changedLocked(s, before)
			m.kickDrainLocked(s)
		}
		m.mu.Unlock()
		if why != "" {
			return
		}
	}
	sub, err := m.send(s, in, head.RequestID)
	m.mu.Lock()
	defer m.mu.Unlock()
	s.queueSending = ""
	if head.From != nil && err == nil && sub.Status != SubmissionAccepted && sub.Status != SubmissionUncertain {
		m.peerEndLocked(head.From, peerFailure(head.From), sub.Error)
	}
	var blocked peerBlockedError
	if errors.As(err, &blocked) {
		// Refused at its commit point: it is dropped, and the queue goes on.
		before := m.summaryLocked(s)
		m.peerEndLocked(head.From, peerFailure(head.From), string(blocked))
		m.changedLocked(s, before)
		m.kickDrainLocked(s)
		return
	}
	if err != nil {
		// Nothing was sent: a turn is running after all, the service is
		// stopping, or another client holds the conversation. The prompt waits
		// at the front for the next completed turn, or, when the service is
		// stopping, for its last save.
		if !s.removed {
			before := m.summaryLocked(s)
			s.queue = slices.Insert(s.queue, 0, head)
			s.queueChanged = true
			// The in-use check refused it: the queue waits for the user.
			if errors.Is(err, errHeldElsewhere) || errors.Is(err, errHolderUnknown) || s.base == StateClosed {
				m.pauseQueueLocked(s)
			}
			m.changedLocked(s, before)
		}
	}
}

// CancelQueued removes one prompt from the queue before it is sent. It never
// contacts the provider. Cancelling it again succeeds; a prompt already sent
// is refused with 409.
func (m *Manager) CancelQueued(id, reqID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	switch {
	case s == nil:
		return newError(http.StatusNotFound, msgSessionNotFound)
	case m.closed:
		return errShuttingDown
	case s.stage != StageActive:
		return s.readOnlyLocked()
	}
	i := slices.IndexFunc(s.queue, func(q QueuedPrompt) bool { return q.RequestID == reqID })
	if i < 0 {
		if s.queueSending != "" && s.queueSending == reqID {
			return newError(http.StatusConflict, "the prompt was already sent")
		}
		sub, ok := s.findSubmission(reqID)
		switch {
		case !ok:
			return newError(http.StatusNotFound, "queued prompt not found")
		case sub.Status == SubmissionCancelled:
			return nil
		default:
			return newError(http.StatusConflict, "the prompt was already sent")
		}
	}
	before := m.summaryLocked(s)
	if from := s.queue[i].From; from != nil {
		m.peerEndLocked(from, peerCancelled, "the owner removed it from the queue")
	}
	s.queue = slices.Delete(s.queue, i, i+1)
	m.cancelledLocked(s, reqID)
	if len(s.queue) == 0 {
		s.queuePaused = false
	}
	s.queueChanged = true
	m.changedLocked(s, before)
	return nil
}

// ClearQueue removes every queued prompt, as CancelQueued does one.
func (m *Manager) ClearQueue(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	switch {
	case s == nil:
		return newError(http.StatusNotFound, msgSessionNotFound)
	case m.closed:
		return errShuttingDown
	case s.stage != StageActive:
		return s.readOnlyLocked()
	case len(s.queue) == 0:
		return nil
	}
	before := m.summaryLocked(s)
	for _, q := range s.queue {
		m.cancelledLocked(s, q.RequestID)
		if q.From != nil {
			m.peerEndLocked(q.From, peerCancelled, "the owner cleared the queue")
		}
	}
	s.queue, s.queuePaused = nil, false
	s.queueChanged = true
	m.changedLocked(s, before)
	return nil
}

// ResumeQueue lets a paused queue drain again: at once when no turn is
// running, otherwise after the running turn completes.
func (m *Manager) ResumeQueue(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	switch {
	case s == nil:
		return newError(http.StatusNotFound, msgSessionNotFound)
	case m.closed:
		return errShuttingDown
	case s.stage != StageActive:
		return s.readOnlyLocked()
	}
	if s.queuePaused {
		before := m.summaryLocked(s)
		s.queuePaused = false
		s.queueChanged = true
		m.changedLocked(s, before)
	}
	if !busy(s.state()) {
		m.kickDrainLocked(s)
	}
	return nil
}
