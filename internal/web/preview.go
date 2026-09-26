package web

import (
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

const previewInterval = 250 * time.Millisecond

type subagentPreviewState struct {
	sent  compactSubagent
	at    time.Time
	timer *time.Timer
}

func (m *Manager) itemPreviewLocked(s *webSession, it agentapi.Item) {
	if it.AgentID != "" {
		m.queueSubagentPreviewLocked(s, it.AgentID, false)
		return
	}
	// A parent task result may arrive after the subagent's terminal status.
	for _, sa := range s.subagents {
		if sa.ParentToolCallID == it.ID {
			m.queueSubagentPreviewLocked(s, sa.ID, true)
		}
	}
}
func (m *Manager) queueSubagentPreviewLocked(s *webSession, id string, force bool) {
	sa := s.subIdx[id]
	if sa == nil {
		return
	}
	if s.previews == nil {
		s.previews = map[string]*subagentPreviewState{}
	}
	state := s.previews[id]
	if state == nil {
		state = &subagentPreviewState{}
		s.previews[id] = state
	}
	if force || state.at.IsZero() || m.now().Sub(state.at) >= previewInterval {
		next := s.compactSubagent(*sa)
		if next == state.sent {
			return
		}
		if state.timer != nil {
			state.timer.Stop()
			state.timer = nil
		}
		m.sendSubagentPreviewLocked(s, state, next)
		return
	}
	if state.timer != nil {
		return
	}
	state.timer = time.AfterFunc(previewInterval-m.now().Sub(state.at), func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		state.timer = nil
		if m.closed || m.sessions[s.id] != s || s.previews[id] != state {
			return
		}
		if sa := s.subIdx[id]; sa != nil {
			next := s.compactSubagent(*sa)
			if next != state.sent {
				m.sendSubagentPreviewLocked(s, state, next)
			}
		}
	})
}
func (m *Manager) sendSubagentPreviewLocked(s *webSession, state *subagentPreviewState, next compactSubagent) {
	state.sent = next
	state.at = m.now()
	m.broadcastFilteredLocked("subagent", s.id, compactMain, func(seq uint64) any {
		return struct {
			detailBarrier
			Subagent compactSubagent `json:"subagent"`
		}{detailBarrier{seq, m.epoch, s.id}, next}
	})
}
func (s *webSession) stopPreviews() {
	for _, state := range s.previews {
		if state.timer != nil {
			state.timer.Stop()
		}
	}
	s.previews = nil
}
