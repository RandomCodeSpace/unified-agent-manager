package web

import (
	"net/http"
	"slices"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// Settle marks an active Task complete and closes its conversation. It is
// refused while the Task is busy, has queued prompts, waits for an answer, or
// still runs subagents or background tasks. Its host tool calls in progress
// end.
func (m *Manager) Settle(id string) (SessionSummary, error) {
	summary, err := m.moveStage(id, StageSettled, StageActive)
	if err == nil {
		m.endCalls(id)
		m.count("settle")
	}
	return summary, err
}

// Reopen makes a settled Task active again. Its next prompt reopens the same
// conversation.
func (m *Manager) Reopen(id string) (SessionSummary, error) {
	return m.moveStage(id, StageActive, StageSettled)
}

// Unarchive restores an archived Task as finished without starting a turn.
// Its next prompt continues the same conversation.
func (m *Manager) Unarchive(id string) (SessionSummary, error) {
	return m.moveStage(id, StageActive, StageArchived)
}

// Archive puts away an active or settled Task, read-only until unarchived.
// An active Task must meet the same conditions as for Settle.
// Archiving ends the Task's host tool calls in progress.
func (m *Manager) Archive(id string) (SessionSummary, error) {
	summary, err := m.moveStage(id, StageArchived, StageActive, StageSettled)
	if err == nil {
		m.endCalls(id)
		m.count("archive")
	}
	return summary, err
}

// moveStage moves a Task from one of the stages in from to stage to. Leaving
// the active stage closes the conversation. Holding s.op keeps prompts, drains
// and opens out while the preconditions are checked.
func (m *Manager) moveStage(id, to string, from ...string) (SessionSummary, error) {
	s, err := m.lookup(id)
	if err != nil {
		return SessionSummary{}, err
	}
	s.op.Lock()
	defer s.op.Unlock()
	m.mu.Lock()
	switch {
	case s.removed:
		m.mu.Unlock()
		return SessionSummary{}, newError(http.StatusNotFound, msgSessionNotFound)
	case m.closed:
		m.mu.Unlock()
		return SessionSummary{}, errShuttingDown
	case !slices.Contains(from, s.stage):
		m.mu.Unlock()
		return SessionSummary{}, newError(http.StatusConflict, "a thread that is %s cannot be %s", stageName(s.stage), stageVerb(to))
	}
	conv, err := m.restageLocked(s, to, "")
	if err != nil {
		m.mu.Unlock()
		return SessionSummary{}, err
	}
	m.mu.Unlock()
	if conv != nil {
		m.closeConversation(conv)
	}
	if err := m.flush(); err != nil {
		log.Warn("persist web task stage failed", "session", id, "error", err)
	}
	log.Info("web task stage changed", "session", id, "stage", stageName(to))
	return m.Summary(id)
}

// restageLocked moves s to stage to and returns the conversation to close
// outside mu, or nil. by is who settles it: "" for the owner, settledByAuto
// for the quiet-Task sweep and settledByMerged for the merged-work sweep,
// whose settles are not Task activity. The caller
// holds s.op and mu and checked the stage s leaves.
func (m *Manager) restageLocked(s *webSession, to, by string) (agentapi.Conversation, error) {
	before := m.summaryLocked(s)
	var conv agentapi.Conversation
	if s.stage == StageActive {
		if err := s.settleableLocked(); err != nil {
			return nil, err
		}
		if s.conv != nil {
			conv = m.disconnectLocked(s)
		}
	}
	now := m.now()
	switch to {
	case StageSettled:
		s.settledAt, s.settledBy = now, by
	case StageArchived:
		s.archivedAt = now
	case StageActive:
		s.settledAt, s.settledBy = time.Time{}, ""
		if s.stage == StageArchived {
			s.archivedAt = time.Time{}
			s.setBase(StateCompleted, "")
			s.stopCode, s.unseenEnd = "", false
		}
	}
	s.stage = to
	if to != StageActive {
		m.releaseClosedHistoryLocked(s)
		m.kickWorktreeRemoveLocked(s)
		// Settling or archiving ends the owner's allowance; a denial stays.
		m.dropPairsLocked(s.id, false)
	}
	if by != "" {
		// updated_at stays the Task's last activity.
		s.persisted = s.key()
		m.persistLocked(s)
	}
	m.changedLocked(s, before)
	return conv, nil
}

// settleableLocked reports why an active Task cannot be settled or archived
// now, or nil. A pending interaction that does not make the Task busy is a
// yolo approval still on its way to the provider.
func (s *webSession) settleableLocked() error {
	switch {
	case busy(s.state()):
		return newError(http.StatusConflict, "the thread is running or waiting for input; stop the turn first")
	case slices.ContainsFunc(s.interactions, func(ix *interaction) bool { return ix.State == agentapi.InteractionPending }):
		return newError(http.StatusConflict, "a permission request is still being answered; try again")
	case len(s.queue) > 0 || s.queueSending != "":
		return newError(http.StatusConflict, "the thread has queued prompts; send or clear them first")
	case s.runningSubagents() > 0:
		return newError(http.StatusConflict, "subagents are still running; wait for them or stop them first")
	case s.runningBackgroundTasks() > 0:
		return newError(http.StatusConflict, "background tasks are still running; wait for them or stop them first")
	}
	return nil
}

// readOnlyLocked refuses changes to a settled or archived Task.
func (s *webSession) readOnlyLocked() error {
	switch s.stage {
	case StageSettled:
		return newError(http.StatusConflict, "the thread is settled; reopen it first")
	case StageArchived:
		return newError(http.StatusConflict, "the thread is archived")
	}
	return nil
}

func stageName(stage string) string {
	if stage == StageActive {
		return "active"
	}
	return stage
}

func stageVerb(stage string) string {
	switch stage {
	case StageSettled:
		return "settled"
	case StageArchived:
		return "archived"
	}
	return "reopened"
}
