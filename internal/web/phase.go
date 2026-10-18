package web

import (
	"slices"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
)

// modelCallsDelay coalesces model_calls frames: one at most this often.
// Tests shorten it.
var modelCallsDelay = 250 * time.Millisecond

// phaseLocked is what s waits on now: the owner, while a request waits for
// them; else a model call in flight; else a running tool. nil when none
// holds. Between turns the provider reports only subagents that outlived the
// turn and compaction; a subagent's call or tool shown then says so.
func (m *Manager) phaseLocked(s *webSession) *Phase {
	var p Phase
	w := s.workPhase
	between := s.activeTiming < 0
	if w != nil {
		p.Calls, p.Subagents = max(w.Calls, 0), min(max(w.Subagents, 0), max(w.Calls, 0))
	}
	if ix := s.oldestWait(); ix != nil {
		p.Kind, p.Since, p.Detail = PhaseWaiting, ix.Time, s.waitDetail(ix)
	} else if p.Calls > 0 {
		p.Kind, p.Since, p.Model = PhaseModel, w.CallsSince, phaseText(w.Model, maxNameRunes)
		if w.Compacting {
			p.By = PhaseByCompaction
		} else if between && p.Calls == p.Subagents {
			p.By, p.Detail = PhaseBySubagent, phaseText(w.Agent, maxIntentRunes)
		}
	} else if w != nil && w.Tool != "" {
		p.Kind, p.Since, p.Tool = PhaseLocal, w.ToolSince, phaseText(w.Tool, maxNameRunes)
		p.Detail = phaseText(w.Detail, maxIntentRunes)
		if between && w.ToolSubagent {
			p.By = PhaseBySubagent
		}
	} else {
		return nil
	}
	if cur := s.turnActivity.Phase; p.Since.IsZero() && cur != nil && cur.Kind == p.Kind {
		p.Since = cur.Since
	} else if p.Since.IsZero() {
		p.Since = m.now()
	}
	return &p
}

// phaseText is s on one line, sanitized and clipped to n runes.
func phaseText(s string, n int) string {
	return clipRunes(strings.Join(strings.Fields(displaytext.Sanitize(s)), " "), n)
}

// oldestWait is the oldest request waiting for the owner, nil when none
// does; a yolo claim does not wait.
func (s *webSession) oldestWait() *interaction {
	var oldest *interaction
	for _, ix := range s.interactions {
		if ix.State == agentapi.InteractionPending && !ix.yolo && (oldest == nil || ix.Time.Before(oldest.Time)) {
			oldest = ix
		}
	}
	return oldest
}

// waitDetail says what ix asks of the owner: "approve" and the tool, when
// its tool call is known, "answer question" or "review plan".
func (s *webSession) waitDetail(ix *interaction) string {
	switch ix.Kind {
	case agentapi.InteractionPermission:
		if i, ok := s.itemIdx[itemKey(ix.AgentID, ix.ToolCallID)]; ok && ix.ToolCallID != "" && s.items[i].Tool != nil {
			if tool := phaseText(s.items[i].Tool.Name, maxNameRunes); tool != "" {
				return "approve " + tool
			}
		}
		return "approve request"
	case agentapi.InteractionPlanReview:
		return "review plan"
	}
	return "answer question"
}

// updatePhaseLocked shows s's phase as it is now. changedLocked runs it, so
// a request the owner answered, or one that came, moves it.
func (m *Manager) updatePhaseLocked(s *webSession) {
	next := s.turnActivity
	next.Phase = m.phaseLocked(s)
	m.setTurnActivityLocked(s, next)
}

func samePhase(a, b *Phase) bool {
	return a == b || a != nil && b != nil && *a == *b
}

// maxModelCallThreads bounds each list of Threads in ModelCalls.
const maxModelCallThreads = 50

// modelCallsLocked counts the model calls in flight across the Threads, and
// the Threads running locally, from their phases, with the Utility calls in
// flight; it names the Threads of each, up to maxModelCallThreads.
func (m *Manager) modelCallsLocked() ModelCalls {
	c := ModelCalls{Background: m.utilityLive, ModelThreads: []string{}, SubagentThreads: []string{}, LocalThreads: []string{}}
	for _, s := range m.sessions {
		p := s.turnActivity.Phase
		if p == nil {
			continue
		}
		busy := false
		if p.Calls > p.Subagents {
			c.Threads++
			c.ModelThreads = append(c.ModelThreads, s.id)
			busy = true
		}
		if p.Subagents > 0 {
			c.Subagents += p.Subagents
			c.SubagentThreads = append(c.SubagentThreads, s.id)
			busy = true
		}
		if p.Kind == PhaseLocal {
			c.Local++
			c.LocalThreads = append(c.LocalThreads, s.id)
			busy = true
		}
		if busy {
			c.Busy++
		}
	}
	for _, ids := range []*[]string{&c.ModelThreads, &c.SubagentThreads, &c.LocalThreads} {
		slices.Sort(*ids)
		*ids = (*ids)[:min(len(*ids), maxModelCallThreads)]
	}
	return c
}

// kickModelCallsLocked sends a model_calls frame within modelCallsDelay,
// once for any number of changes meanwhile.
func (m *Manager) kickModelCallsLocked() {
	if m.modelCallsTimer == nil {
		m.modelCallsTimer = time.AfterFunc(modelCallsDelay, m.flushModelCalls)
	}
}

// flushModelCalls sends the counts as they are now, even when they equal the
// last frame's: a snapshot taken meanwhile may have carried others.
func (m *Manager) flushModelCalls() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.modelCallsTimer = nil
	next := m.modelCallsLocked()
	m.broadcastLocked("model_calls", "", func(seq uint64) any { return modelCallsEvent{Seq: seq, ModelCalls: next} })
}

// countUtilityCall makes a Utility call through call, counted in flight
// until it returns or panics.
func (m *Manager) countUtilityCall(call func() (string, error)) (string, error) {
	m.mu.Lock()
	m.utilityCallLocked(1)
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.utilityCallLocked(-1)
		m.mu.Unlock()
	}()
	return call()
}

// utilityCallLocked counts a Utility call starting (+1) or ending (-1).
func (m *Manager) utilityCallLocked(delta int) {
	m.utilityLive = max(m.utilityLive+delta, 0)
	m.kickModelCallsLocked()
}
