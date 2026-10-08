package web

import (
	"strings"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
)

// Bounds of the activity texts, in runes.
const (
	maxIntentRunes      = 160
	maxRetryReasonRunes = 64
)

// Why a turn was cancelled (SessionSummary.StopReason).
const (
	stopOwner       = "owner"
	stopTimeLimit   = "time_limit"
	stopCreditLimit = "credit_limit"
	stopRemote      = "remote"
	stopMCP         = "mcp"
	stopCLI         = "cli"
)

// providerStops maps the provider's reasons for an aborted turn to a stop
// reason and the cancelled state's detail. An unknown reason gives none.
var providerStops = map[string]struct{ code, detail string }{
	"autopilot_credit_limit": {stopCreditLimit, "Autopilot stopped at its credit limit"},
	"remote_command":         {stopRemote, "Stopped by a remote command"},
	"user_abort":             {stopMCP, "Stopped by an MCP server"},
	"user_initiated":         {stopCLI, "Stopped in the Copilot CLI"},
}

// stopCause says why a turn was cancelled: uam's own stop first (the
// owner's Stop, a routine's time limit), whatever the provider reports,
// then the provider's reason.
func (s *webSession) stopCause(providerReason string) (code, detail string) {
	if s.stopBy != "" {
		return s.stopBy, s.stopReason
	}
	p := providerStops[providerReason]
	return p.code, p.detail
}

// shownStopReason is the summary's StopReason: why the last turn was
// cancelled, while the Task is.
func (s *webSession) shownStopReason() string {
	if s.base != StateCancelled {
		return ""
	}
	return s.stopCode
}

// turnActivityTurnLocked starts the activity afresh when a turn starts and
// when it ends, so a turn never shows the one before it; a turn's later
// model calls (TurnWorking again) keep it. It runs before
// observeTurnTimingLocked opens the turn's timing.
func (m *Manager) turnActivityTurnLocked(s *webSession, state agentapi.TurnState) {
	if state == agentapi.TurnWorking {
		s.stopCode = ""
		if s.activeTiming >= 0 {
			return
		}
	}
	m.setTurnActivityLocked(s, TurnActivity{Todos: s.turnActivity.Todos})
}

// applyActivityLocked takes the adapter's activity of the running turn. It
// is published, never persisted: an intent or a retry writes nothing.
func (m *Manager) applyActivityLocked(s *webSession, a agentapi.Activity) {
	next := TurnActivity{Intent: clipRunes(strings.TrimSpace(displaytext.Sanitize(a.Intent)), maxIntentRunes), Todos: s.turnActivity.Todos}
	if a.Retry != nil {
		r := cleanRetry(*a.Retry)
		next.Retry = &r
	}
	m.setTurnActivityLocked(s, next)
}

func (m *Manager) setTurnActivityLocked(s *webSession, a TurnActivity) {
	cur := s.turnActivity
	sameRetry := a.Retry == cur.Retry || a.Retry != nil && cur.Retry != nil && *a.Retry == *cur.Retry
	if a.Intent == cur.Intent && sameRetry && a.Todos == cur.Todos {
		return
	}
	s.turnActivity = a
	m.broadcastLocked("turn_activity", s.id, func(seq uint64) any {
		return turnActivityEvent{Seq: seq, SessionID: s.id, TurnActivity: a}
	})
}

// cleanRetry bounds a provider's retry report.
func cleanRetry(r agentapi.Retry) agentapi.Retry {
	r.Count = max(r.Count, 0)
	r.Reason = clipRunes(strings.TrimSpace(displaytext.Sanitize(r.Reason)), maxRetryReasonRunes)
	if r.Status < 100 || r.Status > 599 {
		r.Status = 0
	}
	return r
}
