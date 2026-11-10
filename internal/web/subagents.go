package web

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// Subagent returns one subagent of a session with its retained transcript.
func (m *Manager) Subagent(id, agentID string) (SubagentDetail, error) {
	return m.subagent(m.ctx, id, agentID)
}

// subagent is Subagent for a caller that may leave: ctx ends its wait for an
// evicted transcript.
func (m *Manager) subagent(ctx context.Context, id, agentID string) (SubagentDetail, error) {
	s, err := m.lookup(id)
	if err != nil {
		return SubagentDetail{}, err
	}
	if err := m.waitReleasedHistory(ctx, s); err != nil {
		return SubagentDetail{}, err
	}
	var fresh *archiveWindow
	for {
		m.mu.Lock()
		if s.removed {
			m.mu.Unlock()
			return SubagentDetail{}, newError(http.StatusNotFound, msgSessionNotFound)
		}
		m.viewHistoryLocked(s)
		sa := s.subIdx[agentID]
		if sa == nil {
			m.mu.Unlock()
			return SubagentDetail{}, newError(http.StatusNotFound, msgSubagentNotFound)
		}
		v := m.viewLocked(s, agentID, fresh)
		if v.missing && fresh == nil {
			read := m.windowReadLocked(s, agentID, "", archiveWindowItems, 0)
			m.mu.Unlock()
			if fresh, err = m.readArchive(read); err != nil {
				return SubagentDetail{}, err
			}
			continue
		}
		d := SubagentDetail{Seq: m.seq, Subagent: *sa, Items: append([]agentapi.Item(nil), v.items...)}
		m.mu.Unlock()
		return d, nil
	}
}

// CancelSubagent stops only the selected agent. Successful repeats never
// resend, and final status is supplied by the provider's subagent event.
func (m *Manager) CancelSubagent(id, agentID string) (agentapi.Subagent, error) {
	s, err := m.lookup(id)
	if err != nil {
		return agentapi.Subagent{}, err
	}
	s.op.Lock()
	defer s.op.Unlock()
	m.mu.Lock()
	sa := s.subIdx[agentID]
	switch {
	case s.removed:
		m.mu.Unlock()
		return agentapi.Subagent{}, newError(http.StatusNotFound, msgSessionNotFound)
	case sa == nil:
		m.mu.Unlock()
		return agentapi.Subagent{}, newError(http.StatusNotFound, msgSubagentNotFound)
	case sa.Status.Terminal(), s.stoppedSubagents[agentID]:
		result := *sa
		m.mu.Unlock()
		return result, nil
	case m.closed || s.stage != StageActive || s.conv == nil || s.state() == StateStarting || sa.Status != agentapi.SubagentRunning:
		m.mu.Unlock()
		return agentapi.Subagent{}, newError(http.StatusConflict, "the subagent has no active conversation")
	}
	conv := s.conv
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(m.ctx, controlTimeout)
	err = conv.CancelSubagent(ctx, agentID)
	cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	// Completion can race a provider rejection because the agent already
	// finished. An accepted stop still expires its abandoned interactions.
	if err != nil && sa.Status.Terminal() {
		return *sa, nil
	}
	switch {
	case err == nil:
		before := m.summaryLocked(s)
		if s.stoppedSubagents == nil {
			s.stoppedSubagents = map[string]bool{}
		}
		s.stoppedSubagents[agentID] = true
		m.expireSubagentLocked(s, agentID)
		m.changedLocked(s, before)
		return *sa, nil
	case errors.Is(err, agentapi.ErrUnsupported):
		return agentapi.Subagent{}, newError(http.StatusConflict, "this provider cannot stop a subagent")
	case errors.Is(err, agentapi.ErrClosed):
		return agentapi.Subagent{}, newError(http.StatusConflict, msgConversationClosed)
	default:
		return agentapi.Subagent{}, newError(http.StatusBadGateway, "could not stop subagent: %s", shortError(err))
	}
}

// PromptSubagent sends text to one idle subagent of an active Task whose
// conversation is open and runs no turn. The follow-up is not a Task turn:
// the Task's state, queue and last submission stay as they are, and the
// subagent's status arrives through its events. A repeated request ID
// returns the recorded outcome without contacting the provider.
func (m *Manager) PromptSubagent(id, agentID, text, requestID string) (Submission, error) {
	if !validRequestID(requestID) {
		return Submission{}, newError(http.StatusBadRequest, msgRequestIDNotUUID)
	}
	if strings.TrimSpace(text) == "" {
		return Submission{}, newError(http.StatusBadRequest, "prompt text is required")
	}
	if len(text) > maxPromptBytes {
		return Submission{}, newError(http.StatusRequestEntityTooLarge, msgPromptTooLarge)
	}
	s, err := m.lookup(id)
	if err != nil {
		return Submission{}, err
	}
	s.op.Lock()
	defer s.op.Unlock()
	m.mu.Lock()
	if i := slices.IndexFunc(s.subagentPrompts, func(p Submission) bool { return p.RequestID == requestID }); i >= 0 {
		sub := s.subagentPrompts[i]
		m.mu.Unlock()
		return sub, nil
	}
	m.mu.Unlock()
	holderErr := m.checkHolder(s)
	m.mu.Lock()
	// Another request may have finished while the holder RPC released s.op.
	if i := slices.IndexFunc(s.subagentPrompts, func(p Submission) bool { return p.RequestID == requestID }); i >= 0 {
		sub := s.subagentPrompts[i]
		m.mu.Unlock()
		return sub, nil
	}
	if holderErr != nil {
		m.mu.Unlock()
		return Submission{}, holderErr
	}
	sa := s.subIdx[agentID]
	switch {
	case s.removed:
		err = newError(http.StatusNotFound, msgSessionNotFound)
	case sa == nil:
		err = newError(http.StatusNotFound, msgSubagentNotFound)
	case m.closed:
		err = errShuttingDown
	case s.stage != StageActive:
		err = s.readOnlyLocked()
	case s.rewindHoldLocked() != nil:
		err = s.rewindHoldLocked()
	case s.conv == nil:
		err = newError(http.StatusConflict, msgConversationNotOpen)
	case busy(s.state()):
		err = newError(http.StatusConflict, "the thread is running or waiting for input; a subagent takes a follow-up only while the thread is idle")
	case sa.Status == agentapi.SubagentRunning:
		err = newError(http.StatusConflict, "the subagent is still running; it takes a follow-up once it is idle")
	case sa.Status != agentapi.SubagentIdle:
		err = newError(http.StatusConflict, "the subagent does not take follow-ups (%s)", sa.Status)
	}
	conv := s.conv
	m.mu.Unlock()
	if err != nil {
		return Submission{}, err
	}
	ctx, cancel := context.WithTimeout(m.ctx, sendTimeout)
	err = conv.PromptSubagent(ctx, agentID, text)
	cancel()
	switch {
	case err == nil:
		return m.recordSubagentPrompt(s, requestID, SubmissionAccepted, ""), nil
	case errors.Is(err, agentapi.ErrUnsupported):
		return Submission{}, newError(http.StatusConflict, "this provider cannot chat with a subagent")
	case errors.Is(err, agentapi.ErrClosed):
		return Submission{}, newError(http.StatusConflict, msgConversationClosed)
	}
	log.Warn("web subagent follow-up failed", "session", s.id, "error", err)
	if errors.Is(err, agentapi.ErrSubmissionUncertain) {
		// Never resend: the subagent may already be working on it.
		return m.recordSubagentPrompt(s, requestID, SubmissionUncertain,
			"the provider may or may not have received this follow-up, and uam did not resend it: "+shortError(err)), nil
	}
	return m.recordSubagentPrompt(s, requestID, SubmissionRejected, shortError(err)), nil
}

// recordSubagentPrompt keeps a follow-up's outcome for repeated request IDs.
// Nothing else changes: the subagent's own events report what it does.
func (m *Manager) recordSubagentPrompt(s *webSession, reqID, status, msg string) Submission {
	sub := Submission{RequestID: reqID, Status: status, Error: msg, Time: m.now()}
	m.mu.Lock()
	defer m.mu.Unlock()
	s.subagentPrompts = append(s.subagentPrompts, sub)
	if len(s.subagentPrompts) > maxSubmissions {
		s.subagentPrompts = append([]Submission(nil), s.subagentPrompts[len(s.subagentPrompts)-maxSubmissions:]...)
	}
	return sub
}
