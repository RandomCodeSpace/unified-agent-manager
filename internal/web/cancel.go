package web

import (
	"context"
	"errors"
	"net/http"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// cancelledLocked remembers that reqID left the queue unsent, so a repeated
// request reports that instead of queueing it again. It is not a submission:
// last_submission stays as it was.
func (m *Manager) cancelledLocked(s *webSession, reqID string) {
	s.remember(Submission{RequestID: reqID, Status: SubmissionCancelled, Time: m.now()})
}

// Cancel aborts the running turn at the owner's request. It is distinct from
// a viewer leaving and from Close: the conversation stays open.
func (m *Manager) Cancel(id string) (SessionSummary, error) {
	return m.cancelBecause(id, stopOwner, "")
}

// cancelBecause is Cancel by uam itself: by is the stop reason it reports,
// and reason says why, as the cancelled Task's detail. An empty reason is
// the owner's Stop.
//
// Stop waits at most stopOpWait for another operation on the Thread, then
// goes on without it, so an operation stuck in the provider (a send, a
// steer, an answer) cannot hold it. That is safe: what Stop changes is
// guarded by m.mu, and those operations check the state again under m.mu
// once their provider call returns.
func (m *Manager) cancelBecause(id, by, reason string) (SessionSummary, error) {
	s, err := m.lookup(id)
	if err != nil {
		return SessionSummary{}, err
	}
	if lockWithin(&s.op, stopOpWait) {
		defer s.op.Unlock()
	}
	m.mu.Lock()
	if err := s.readOnlyLocked(); err != nil {
		m.mu.Unlock()
		return SessionSummary{}, err
	}
	conv, state, caps := s.conv, s.state(), m.infos[s.provider].Capabilities
	if !caps.Cancel {
		m.mu.Unlock()
		return SessionSummary{}, newError(http.StatusConflict, "this provider does not support cancelling a turn")
	}
	if w := s.wrap; w != nil && by == stopOwner && !w.done {
		// The owner's Stop ends the wrap-up: no prompt, uam's facts only.
		w.stop, w.stopWhy = true, "you stopped the wrap-up"
		if !busy(state) {
			before := m.summaryLocked(s)
			m.finishWrapLocked(s, "", w.stopWhy)
			m.changedLocked(s, before)
			m.mu.Unlock()
			return m.Summary(id)
		}
	}
	if conv == nil || state == StateStarting || (!busy(state) && (s.execution == nil || s.execution.Mode != "autopilot")) {
		m.mu.Unlock()
		return SessionSummary{}, newError(http.StatusConflict, "no turn is running")
	}
	s.stopSeq++
	s.stopReason, s.stopBy = reason, by
	before := m.summaryLocked(s)
	m.expireOwnLocked(s, "the turn was stopped")
	m.pauseQueueLocked(s)
	m.changedLocked(s, before)
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(m.ctx, controlTimeout)
	err = conv.Cancel(ctx)
	cancel()
	switch {
	case err == nil:
	case errors.Is(err, agentapi.ErrUnsupported):
		return SessionSummary{}, newError(http.StatusConflict, "this provider does not support cancelling a turn")
	case errors.Is(err, agentapi.ErrClosed):
		return SessionSummary{}, newError(http.StatusConflict, msgConversationClosed)
	case errors.Is(err, agentapi.ErrBusy):
		return SessionSummary{}, newError(http.StatusConflict, "another operation on the Thread is in progress; try Stop again")
	default:
		return SessionSummary{}, newError(http.StatusBadGateway, "cancel failed: %s", shortError(err))
	}
	return m.Summary(id)
}
