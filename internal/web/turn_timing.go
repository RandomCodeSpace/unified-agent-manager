package web

import (
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// Keep no more timing records than the bounded transcript can show. Timings
// belong to UAM: provider history must never invent or replace these records.
const maxTurnTimings = maxItems

func (m *Manager) observeTurnTimingLocked(s *webSession, state agentapi.TurnState) *TurnTiming {
	if state == agentapi.TurnWorking {
		// Tool/model iterations and autopilot continuations share one foreground
		// turn until the adapter supplies a terminal boundary.
		if s.activeTiming >= 0 {
			return nil
		}
		id, err := newUUID()
		if err != nil {
			return nil
		}
		timing := TurnTiming{ID: id, UserItemID: s.pendingTimingUser, StartedAt: m.now(), State: StateWorking}
		s.pendingTimingUser = ""
		if len(s.turnTimings) >= maxTurnTimings {
			s.turnTimings = append([]TurnTiming(nil), s.turnTimings[len(s.turnTimings)-maxTurnTimings+1:]...)
		}
		s.turnTimings = append(s.turnTimings, timing)
		s.activeTiming = len(s.turnTimings) - 1
		m.publishTurnTimingLocked(s, timing)
		return nil
	}
	if s.activeTiming < 0 || (state != agentapi.TurnCompleted && state != agentapi.TurnCancelled && state != agentapi.TurnFailed) {
		return nil
	}
	timing := s.turnTimings[s.activeTiming]
	timing.EndedAt, timing.State = m.now(), string(state)
	finishTimingPause(&timing, timing.EndedAt)
	// The todo list as the turn left it: its counts are written with this
	// timing, its rows by the same flush (turn_todos.go).
	timing.Todo = m.snapshotTodosLocked(s, timing)
	if s.nativeChanges {
		timing.Changes = &store.TurnChangeCounts{Status: "unknown"}
	}
	s.turnTimings[s.activeTiming] = timing
	s.activeTiming = -1
	s.pendingTimingUser = ""
	m.publishTurnTimingLocked(s, timing)
	return &timing
}

// Pending manual requests pause the clock once, even when requests overlap.
// Yolo claims do not wait for the user and are excluded by pendingKinds.
func (m *Manager) updateTurnTimingPauseLocked(s *webSession) {
	if s.activeTiming < 0 {
		return
	}
	timing := s.turnTimings[s.activeTiming]
	permissions, questions := s.pendingKinds()
	paused := permissions+questions > 0
	if paused == !timing.PausedAt.IsZero() {
		return
	}
	if paused {
		timing.PausedAt = m.now()
	} else {
		finishTimingPause(&timing, m.now())
	}
	s.turnTimings[s.activeTiming] = timing
	m.publishTurnTimingLocked(s, timing)
}

// A model call's usage lands on the running turn: tokens in and out, and the
// call's duration. Subagent calls count too; they are the turn's work. It is
// not a durable change of its own: the turn's end or another change writes
// it, or a flush lazyFlushDelay later.
func (m *Manager) countTurnTokensLocked(s *webSession, usage agentapi.TokenUsage) {
	if s.activeTiming < 0 || (usage.Input == 0 && usage.Output == 0) {
		return
	}
	timing := s.turnTimings[s.activeTiming]
	timing.InputTokens += usage.Input
	timing.OutputTokens += usage.Output
	timing.GenerationMS += usage.DurationMS
	s.turnTimings[s.activeTiming] = timing
	m.dirty[s.id] = struct{}{}
	m.flushLaterLocked()
	m.broadcastTurnTimingLocked(s, timing)
}

func finishTimingPause(timing *TurnTiming, end time.Time) {
	if timing.PausedAt.IsZero() {
		return
	}
	timing.PausedMS += max(0, end.Sub(timing.PausedAt).Milliseconds())
	timing.PausedAt = time.Time{}
}

// Only a new live ordinary user item can anchor an observation. Replayed
// history, steers, automatic continuations and subagent items do not start it.
func (m *Manager) linkTurnTimingLocked(s *webSession, item agentapi.Item) {
	if item.AgentID != "" || item.Kind != agentapi.ItemUser || item.Delivery != "" {
		return
	}
	if i, exists := s.itemIdx[item.ID]; exists {
		previous := s.items[i]
		if previous.Delivery != agentapi.DeliverySteer || previous.SteerStatus == "" {
			return
		}
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

// publishTurnTimingLocked sends timing and makes it a durable change.
func (m *Manager) publishTurnTimingLocked(s *webSession, timing TurnTiming) {
	s.timingRevision++
	m.broadcastTurnTimingLocked(s, timing)
}

func (m *Manager) broadcastTurnTimingLocked(s *webSession, timing TurnTiming) {
	m.broadcastLocked("turn_timing", s.id, func(seq uint64) any { return turnTimingEvent{Seq: seq, SessionID: s.id, TurnTiming: timing} })
}
