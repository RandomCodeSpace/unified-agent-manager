package web

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// View opens a session's conversation lazily for a viewer and waits a bounded
// time for it. The open runs on the service, not on ctx: a viewer leaving
// early does not abort it.
func (m *Manager) View(ctx context.Context, id string) error {
	s, err := m.lookup(id)
	if err != nil {
		return err
	}
	done := m.viewOpen(s)
	timer := time.NewTimer(viewWait)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	case <-ctx.Done():
	}
	return nil
}

var closedChan = func() chan struct{} { ch := make(chan struct{}); close(ch); return ch }()

func (m *Manager) viewOpen(s *webSession) <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || !m.autoOpenableLocked(s) {
		return closedChan
	}
	if s.opening != nil {
		return s.opening
	}
	before := m.summaryLocked(s)
	s.opening = make(chan struct{})
	done := s.opening
	m.changedLocked(s, before)
	m.wg.Add(1)
	go func() {
		defer log.Recover("session open", &m.mu)
		defer m.wg.Done()
		s.op.Lock()
		defer s.op.Unlock()
		_ = m.openLocked(s, false)
	}()
	return done
}

// autoOpenableLocked reports whether merely viewing s may open its
// conversation. Failed, closed, settled and archived sessions stay as they
// are until the user acts on them, so their reported outcome is not replaced
// by a page load.
func (m *Manager) autoOpenableLocked(s *webSession) bool {
	return !s.removed && s.stage == StageActive && s.conv == nil && s.convID != "" && s.base != StateFailed && s.base != StateClosed &&
		m.providers[s.provider] != nil && m.infos[s.provider].Available
}

func (m *Manager) finishOpeningLocked(s *webSession) {
	if s.opening != nil {
		close(s.opening)
		s.opening = nil
	}
}

// withHostToolsLocked adds the host tools of s, req's Task, when there are
// any, the built-in skills and the compaction threshold to req. The caller
// holds mu.
func (m *Manager) withHostToolsLocked(req agentapi.OpenRequest, s *webSession) agentapi.OpenRequest {
	go func(dir string) {
		defer log.Recover("glab gate", &m.mu)
		m.glab.gate(m.ctx, dir)
	}(req.Workdir)
	req.Hooks.Pre = func(ctx context.Context, use agentapi.ToolUse) agentapi.ToolVerdict { return m.preToolUse(ctx, s, use) }
	req.Hooks.Failed = func(ctx context.Context, use agentapi.ToolUse, failure string) string {
		return m.failedToolUse(ctx, s, use, failure)
	}
	req.Hooks.Succeeded = func(ctx context.Context, use agentapi.ToolUse) { m.succeededToolUse(ctx, s, use) }
	req.Hooks.Prompt = func(context.Context) string { return m.takeHostNotes(s) }
	req.SkillDirectories = m.skillDirs
	req.Instructions = m.placeInstructionsLocked(s)
	req.OutputStyle = m.outputStyleLocked(s)
	s.openStyle = req.OutputStyle
	if t := m.settings.CompactionThreshold; t != nil {
		req.CompactionThreshold = float64(*t) / 100
	}
	s.compactAt = m.settings.compactionThreshold()
	if m.hostTools != nil {
		req.Tools, req.CallTool = m.hostTools(req.SessionID, s.spawnedBy != "" || s.routineID != "")
	}
	return req
}

// openLocked reopens s's exact conversation. The caller holds s.op. explicit
// is true for user actions (sending a prompt); a viewer only opens sessions
// autoOpenableLocked allows.
func (m *Manager) openLocked(s *webSession, explicit bool) error {
	m.syncAssisted(s.provider)
	m.mu.Lock()
	if s.removed {
		m.finishOpeningLocked(s)
		m.mu.Unlock()
		return newError(http.StatusNotFound, msgSessionNotFound)
	}
	if s.conv != nil || m.closed || (!explicit && !m.autoOpenableLocked(s)) {
		before := m.summaryLocked(s)
		m.finishOpeningLocked(s)
		// A viewer told the transcript was loading gets it read instead.
		m.viewHistoryLocked(s)
		m.changedLocked(s, before)
		open, closed := s.conv != nil, m.closed
		m.mu.Unlock()
		if !open && closed {
			return errShuttingDown
		}
		return nil
	}
	before := m.summaryLocked(s)
	prov := m.providers[s.provider]
	if prov == nil || s.convID == "" {
		detail := fmt.Sprintf("provider %q is not available in this service", s.provider)
		if s.convID == "" {
			detail = "this session has no provider conversation id; uam will not create a replacement"
		}
		s.setBase(StateFailed, detail)
		m.finishOpeningLocked(s)
		m.changedLocked(s, before)
		m.mu.Unlock()
		return newError(http.StatusConflict, "%s", detail)
	}
	if s.opening == nil {
		s.opening = make(chan struct{})
	}
	m.cancelHistoryLocked(s)
	s.gen++
	gen := s.gen
	req := m.withHostToolsLocked(agentapi.OpenRequest{SessionID: s.id, ConversationID: s.convID, Workdir: s.workdir, Title: s.name, Agent: s.agent, Events: sink{m: m, s: s, gen: gen}, ValidateFile: m.declarationValidator(s.id, s.workdir), AssistedApprovalModel: approvalModel(s.mode)}, s)
	withHistory := m.infos[s.provider].Capabilities.History
	model, effort, contextSize := s.model, s.effort, cmp.Or(s.contextSize, "default")
	req.Model, req.Effort, req.ContextSize = model, effort, contextSize
	s.compacting, s.compactingBackground = false, false
	// The same conversation reopens with the context its last report gave.
	if s.convID == "" {
		s.context = nil
	}
	var selectionErr error
	if effort != "" || contextSize != "default" {
		selectionErr = m.validateSelectionLocked(s.provider, model, effort, contextSize)
	}
	if selectionErr == nil && s.mode == store.ModeAssisted {
		selectionErr = m.assistedSupportLocked(s.provider)
	}
	m.changedLocked(s, before)
	m.mu.Unlock()

	m.restoreWorktree(m.ctx, s)
	ctx, cancel := context.WithTimeout(m.ctx, openTimeout)
	defer cancel()
	var conv agentapi.Conversation
	err := selectionErr
	if err == nil {
		conv, err = prov.Open(ctx, req)
	}
	if err == nil && conv.ID() != req.ConversationID {
		// Exactness is the contract: a different conversation is a failure,
		// not a substitute.
		m.closeConversation(conv)
		err = fmt.Errorf("provider opened conversation %q instead of %q", conv.ID(), req.ConversationID)
	}
	// Apply the selected model before anything is sent: it may have been
	// changed while the conversation was closed. A conversation that cannot
	// take it, ErrUnsupported included (a provider with a catalog must
	// switch), is not used with another model.
	if err == nil && model != "" {
		if setErr := conv.SetModel(ctx, model, effort, contextSize); setErr != nil {
			m.closeConversation(conv)
			err = fmt.Errorf("apply model %s: %w", model, setErr)
		}
	}
	var history agentapi.History
	var histErr error
	if err == nil && withHistory {
		if history, histErr = conv.History(ctx); histErr != nil {
			log.Warn("read web conversation history failed", "session", s.id, "error", histErr)
		}
		// Only a conversation that is still the current one stores images.
		m.mu.Lock()
		current := s.gen == gen && !m.closed
		m.mu.Unlock()
		for i := range history.Items {
			if current && history.Items[i].Kind == agentapi.ItemTool {
				m.keepImages(s, &history.Items[i])
			}
		}
	}
	notes := notesOf(history.Items)

	m.mu.Lock()
	before = m.summaryLocked(s)
	stale := s.gen != gen || m.closed
	switch {
	case err == nil && !stale:
		s.conv = conv
		s.modeUnknown = false // the open applied the recorded mode
		s.activeAt = m.now()
		if s.base == StateClosed || s.base == StateFailed {
			s.setBase(StateIdle, "")
		}
		m.noteHistoryLocked(s, history, notes, withHistory && histErr == nil)
		m.applyHistoryLocked(s, history, false)
		m.openedHistoryLocked(s, withHistory, histErr)
		if withHistory && histErr == nil {
			m.rereadRewindLocked(s)
		}
		m.autoAllowPendingLocked(s)
	case err != nil && s.gen == gen:
		s.setBase(StateFailed, openFailureDetail(err, s.convID))
		// The failed conversation's schedules are not this Task's.
		if s.schedules != nil {
			m.schedulesLocked(s, nil)
		}
	}
	m.finishOpeningLocked(s)
	if err != nil && s.history == "" {
		// A viewer may be waiting for the transcript this open was to read.
		m.viewHistoryLocked(s)
	}
	m.changedLocked(s, before)
	m.mu.Unlock()

	if err != nil {
		log.Warn("reopen web conversation failed", "session", s.id, "provider", s.provider, "error", err)
		if errors.Is(err, agentapi.ErrUnsupported) {
			m.syncAssisted(s.provider)
		}
		if errors.Is(err, agentapi.ErrConversationNotFound) || errors.Is(err, agentapi.ErrAgentUnavailable) {
			return newError(http.StatusConflict, "%s", openFailureDetail(err, req.ConversationID))
		}
		return newError(http.StatusBadGateway, "%s", openFailureDetail(err, req.ConversationID))
	}
	if stale {
		m.closeConversation(conv)
		return newError(http.StatusConflict, "the session changed while its conversation was opening")
	}
	return nil
}

func openFailureDetail(err error, convID string) string {
	if errors.Is(err, agentapi.ErrConversationNotFound) {
		return fmt.Sprintf("provider conversation %s no longer exists; uam did not create a replacement", convID)
	}
	if errors.Is(err, agentapi.ErrAgentUnavailable) {
		return shortError(err) + "; choose another agent or the default agent for this Thread"
	}
	return "could not open the provider conversation: " + shortError(err)
}
