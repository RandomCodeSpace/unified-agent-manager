package web

import (
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// settledByAuto marks a Task the quiet-Task sweep settled.
const settledByAuto = "auto"

// autoSettleAfter is how long an active Task stays quiet before the hourly
// sweep settles it; a var for tests. Quiet is measured from updated_at, the
// Task's last activity.
var autoSettleAfter = 7 * 24 * time.Hour

// autoSettle settles every active Task quiet for autoSettleAfter that could
// be settled by hand and waits for nothing: no turn, answer, queue, rewind,
// unseen failure or open page. Reopen restarts its week. The settle is not
// activity, so updated_at keeps the Task's last message.
func (m *Manager) autoSettle() {
	m.mu.Lock()
	var quiet []*webSession
	for _, s := range m.sessions {
		if m.autoSettleableLocked(s) {
			quiet = append(quiet, s)
		}
	}
	m.mu.Unlock()
	settled := 0
	for _, s := range quiet {
		if m.autoSettleOne(s) {
			settled++
		}
	}
	if settled == 0 {
		return
	}
	if err := m.flush(); err != nil {
		log.Warn("persist auto-settled web tasks failed", "error", err)
	}
	log.Info("auto-settled quiet web tasks", "tasks", settled, "after", autoSettleAfter)
}

// autoSettleOne settles s when it is still quiet. A Task whose op is held (a
// prompt, open or stage change) is left for the next sweep.
func (m *Manager) autoSettleOne(s *webSession) bool {
	if !s.op.TryLock() {
		return false
	}
	defer s.op.Unlock()
	m.mu.Lock()
	if m.closed || !m.autoSettleableLocked(s) {
		m.mu.Unlock()
		return false
	}
	conv, err := m.restageLocked(s, StageSettled, settledByAuto)
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

// autoSettleableLocked reports whether the sweep may settle s now.
func (m *Manager) autoSettleableLocked(s *webSession) bool {
	if s.removed || s.stage != StageActive || s.opening != nil || m.now().Sub(s.updatedAt) < autoSettleAfter {
		return false
	}
	if s.runsOrWaitsLocked() || s.pendingAsk() != nil || s.rewindHoldLocked() != nil || s.unseenEnd {
		return false
	}
	for sub := range m.subs {
		if sub.session == s.id {
			return false
		}
	}
	return true
}
