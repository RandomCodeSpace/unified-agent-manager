package web

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

func (m *Manager) closeConversation(conv agentapi.Conversation) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(m.ctx), controlTimeout)
	defer cancel()
	if err := conv.Close(ctx); err != nil && !errors.Is(err, agentapi.ErrClosed) {
		log.Warn("close web conversation failed", "error", err)
	}
}

// Close disconnects the conversation and keeps the record.
func (m *Manager) Close(id string) (SessionSummary, error) {
	s, err := m.lookup(id)
	if err != nil {
		return SessionSummary{}, err
	}
	s.op.Lock()
	defer s.op.Unlock()
	m.mu.Lock()
	if s.removed {
		m.mu.Unlock()
		return SessionSummary{}, newError(http.StatusNotFound, msgSessionNotFound)
	}
	before := m.summaryLocked(s)
	conv := m.disconnectLocked(s)
	m.changedLocked(s, before)
	m.mu.Unlock()
	if conv != nil {
		m.closeConversation(conv)
	}
	if err := m.flush(); err != nil {
		log.Warn("persist closed web session failed", "session", id, "error", err)
	}
	return m.Summary(id)
}

// disconnectLocked detaches s from its conversation, as Close does, and
// returns the conversation for the caller to close.
func (m *Manager) disconnectLocked(s *webSession) agentapi.Conversation {
	conv := s.conv
	s.conv = nil
	s.gen++
	m.expirePendingLocked(s, "the session was closed")
	m.endSubagentsLocked(s)
	m.forgetBackgroundTaskStateLocked(s)
	m.pauseQueueLocked(s)
	s.fuses = nil
	s.setBase(StateClosed, "")
	if s.wrap != nil {
		m.finishWrapLocked(s, "", "the conversation was closed")
	}
	m.releaseClosedHistoryLocked(s)
	return conv
}

// closeIdleConversations closes every open conversation that stayed idle
// and unwatched for idleClose. The Task keeps its state; its next view or
// prompt reopens the conversation, as after a restart.
func (m *Manager) closeIdleConversations() {
	m.mu.Lock()
	now := m.now()
	var idle []*webSession
	for _, s := range m.sessions {
		switch {
		case s.conv == nil:
		case s.activeAt.IsZero() || m.keepsOpenLocked(s):
			s.activeAt = now
		case now.Sub(s.activeAt) >= idleClose:
			idle = append(idle, s)
		}
	}
	m.mu.Unlock()
	for _, s := range idle {
		m.closeIdle(s)
	}
}

// closeIdle closes s's conversation when it is still idle. A Task whose op is
// held (a prompt, open or stage change) is left for the next sweep.
func (m *Manager) closeIdle(s *webSession) {
	if !s.op.TryLock() {
		return
	}
	defer s.op.Unlock()
	m.mu.Lock()
	idle := m.now().Sub(s.activeAt)
	if m.closed || s.removed || s.conv == nil || m.keepsOpenLocked(s) || idle < idleClose {
		m.mu.Unlock()
		return
	}
	before := m.summaryLocked(s)
	conv := m.suspendLocked(s, true)
	m.changedLocked(s, before)
	m.mu.Unlock()
	m.closeConversation(conv)
	log.Info("closed idle web conversation", "session", s.id, "idle", idle.Round(time.Second))
}

// suspendLocked detaches s from its open conversation and returns it for the
// caller to close outside mu. The Task keeps its state; its next view or
// prompt reopens the conversation, as after a restart. dropHistory drops the
// transcript when the provider keeps it, for the reopen to read it again.
func (m *Manager) suspendLocked(s *webSession, dropHistory bool) agentapi.Conversation {
	conv := s.conv
	s.conv = nil
	s.gen++
	m.endSubagentsLocked(s)
	m.forgetBackgroundTaskStateLocked(s)
	if s.wrap != nil {
		m.finishWrapLocked(s, "", "the conversation was closed")
	}
	if dropHistory && m.infos[s.provider].Capabilities.History {
		m.dropHistoryLocked(s)
	}
	return conv
}

// keepsOpenLocked reports whether s has a viewer or anything its open
// conversation still runs or waits for.
func (m *Manager) keepsOpenLocked(s *webSession) bool {
	for sub := range m.subs {
		if sub.session == s.id {
			return true
		}
	}
	return s.runsOrWaitsLocked()
}

// runsOrWaitsLocked reports whether s's open conversation still runs or
// waits for anything: a turn, an answer, queued prompts, subagents,
// background tasks, an active objective or an aside question.
func (s *webSession) runsOrWaitsLocked() bool {
	if s.asking || s.settleableLocked() != nil || s.runningSubagents() > 0 {
		return true
	}
	// Without an open conversation the snapshot is stale: nothing of it runs.
	if t := s.backgroundTasks; t != nil && s.conv != nil && (!t.Known || slices.ContainsFunc(t.Tasks, func(task agentapi.BackgroundTask) bool {
		return task.Status != "completed" && task.Status != "failed" && task.Status != "cancelled"
	})) {
		return true
	}
	return s.execution != nil && s.execution.Objective != nil && s.execution.Objective.Status == "active"
}
