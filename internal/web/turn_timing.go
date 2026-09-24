package web

import "github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"

// Keep no more timing records than the bounded transcript can show. Timings
// belong to UAM: provider history must never invent or replace these records.
const maxTurnTimings = maxItems

func (m *Manager) observeTurnTimingLocked(s *webSession, state agentapi.TurnState) {
	if state == agentapi.TurnWorking {
		// Tool/model iterations and autopilot continuations share one foreground
		// turn until the adapter supplies a terminal boundary.
		if s.activeTiming >= 0 {
			return
		}
		id, err := newUUID()
		if err != nil {
			return
		}
		timing := TurnTiming{ID: id, UserItemID: s.pendingTimingUser, StartedAt: m.now(), State: StateWorking}
		s.pendingTimingUser = ""
		if len(s.turnTimings) >= maxTurnTimings {
			s.turnTimings = append([]TurnTiming(nil), s.turnTimings[len(s.turnTimings)-maxTurnTimings+1:]...)
		}
		s.turnTimings = append(s.turnTimings, timing)
		s.activeTiming = len(s.turnTimings) - 1
		m.publishTurnTimingLocked(s, timing)
		return
	}
	if s.activeTiming < 0 || (state != agentapi.TurnCompleted && state != agentapi.TurnCancelled && state != agentapi.TurnFailed) {
		return
	}
	timing := s.turnTimings[s.activeTiming]
	timing.EndedAt, timing.State = m.now(), string(state)
	s.turnTimings[s.activeTiming] = timing
	s.activeTiming = -1
	s.pendingTimingUser = ""
	m.publishTurnTimingLocked(s, timing)
}

// Only a new live ordinary user item can anchor an observation. Replayed
// history, steers, automatic continuations and subagent items do not start it.
func (m *Manager) linkTurnTimingLocked(s *webSession, item agentapi.Item) {
	if item.AgentID != "" || item.Kind != agentapi.ItemUser || item.Delivery != "" {
		return
	}
	if _, exists := s.itemIdx[item.ID]; exists {
		return
	}
	if s.activeTiming < 0 {
		s.pendingTimingUser = item.ID
		return
	}
	timing := s.turnTimings[s.activeTiming]
	if timing.UserItemID != "" {
		return
	}
	timing.UserItemID = item.ID
	s.turnTimings[s.activeTiming] = timing
	m.publishTurnTimingLocked(s, timing)
}

func (m *Manager) forgetTurnTimingLocked(s *webSession) {
	s.pendingTimingUser = ""
	if s.activeTiming < 0 {
		return
	}
	timing := s.turnTimings[s.activeTiming]
	timing.State = "unknown"
	s.turnTimings[s.activeTiming] = timing
	s.activeTiming = -1
	m.publishTurnTimingLocked(s, timing)
}

func (m *Manager) publishTurnTimingLocked(s *webSession, timing TurnTiming) {
	s.timingRevision++
	m.broadcastLocked("turn_timing", s.id, func(seq uint64) any { return turnTimingEvent{Seq: seq, SessionID: s.id, TurnTiming: timing} })
}
