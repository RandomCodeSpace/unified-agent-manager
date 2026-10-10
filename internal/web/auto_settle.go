package web

import (
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// Who settled a Task when the owner did not: the quiet-Task sweep, or the
// done check's sweep once the owner saw a reply checked done.
const (
	settledByAuto     = "auto"
	settledByReviewed = "reviewed"
)

// autoSettleAfter is how long an active Task stays quiet before the hourly
// sweep settles it; a var for tests. Quiet is measured from updated_at, the
// Task's last activity.
var autoSettleAfter = 7 * 24 * time.Hour

// autoSettle settles every active Task quiet for autoSettleAfter that could
// be settled by hand and waits for nothing (unattendedLocked). Reopen
// restarts its week. The settle is not activity, so updated_at keeps the
// Task's last message.
func (m *Manager) autoSettle() {
	m.sweepSettle(settledByAuto, m.autoSettleableLocked)
}

// sweepSettle settles, as by, every Task settleable reports, then writes
// them once.
func (m *Manager) sweepSettle(by string, settleable func(*webSession) bool) {
	m.mu.Lock()
	var due []*webSession
	for _, s := range m.sessions {
		if settleable(s) {
			due = append(due, s)
		}
	}
	m.mu.Unlock()
	settled := 0
	for _, s := range due {
		if m.settleOne(s, by, settleable) {
			settled++
		}
	}
	if settled == 0 {
		return
	}
	if err := m.flush(); err != nil {
		log.Warn("persist settled web tasks failed", "by", by, "error", err)
	}
	log.Info("settled web tasks", "by", by, "tasks", settled)
}

// settleOne settles s as by when settleable still holds. A Task whose op is
// held (a prompt, open or stage change) is left for the next sweep.
func (m *Manager) settleOne(s *webSession, by string, settleable func(*webSession) bool) bool {
	if !s.op.TryLock() {
		return false
	}
	defer s.op.Unlock()
	m.mu.Lock()
	if m.closed || !settleable(s) {
		m.mu.Unlock()
		return false
	}
	conv, err := m.restageLocked(s, StageSettled, by)
	m.mu.Unlock()
	if err != nil {
		return false
	}
	m.endCalls(s.id)
	if conv != nil {
		m.closeConversation(conv)
	}
	return true
}

// autoSettleableLocked reports whether the quiet-Task sweep may settle s now.
func (m *Manager) autoSettleableLocked(s *webSession) bool {
	return m.now().Sub(s.updatedAt) >= autoSettleAfter && m.unattendedLocked(s)
}

// unattendedLocked reports whether s is an active Task a sweep may settle:
// Settle would allow it and nothing waits on it, no turn, answer, queue,
// rewind, unseen failure or open page.
func (m *Manager) unattendedLocked(s *webSession) bool {
	if s.removed || s.stage != StageActive || s.opening != nil {
		return false
	}
	if s.runsOrWaitsLocked() || s.pendingAsk() != nil || s.rewindHoldLocked() != nil || s.unseenEnd {
		return false
	}
	return !m.watchedLocked(s.id)
}
