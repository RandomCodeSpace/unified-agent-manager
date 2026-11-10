package web

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// SetModel changes the supplied settings together for the Task's next turns.
// An omitted effort or context size survives a model change when supported.
// With the conversation open, settings are stored only after provider success;
// otherwise the next open applies them. Changes during a turn are refused.
func (m *Manager) SetModel(id string, model, effort, contextSize *string) (SessionSummary, error) {
	s, err := m.lookup(id)
	if err != nil {
		return SessionSummary{}, err
	}
	s.op.Lock()
	defer s.op.Unlock()
	return m.setModelLocked(s, model, effort, contextSize)
}

// The caller holds s.op, shared by command invocation and Stop.
func (m *Manager) setModelLocked(s *webSession, model, effort, contextSize *string) (SessionSummary, error) {
	id := s.id
	m.mu.Lock()
	if s.removed {
		m.mu.Unlock()
		return SessionSummary{}, newError(http.StatusNotFound, msgSessionNotFound)
	}
	if s.stage != StageActive {
		m.mu.Unlock()
		return SessionSummary{}, s.readOnlyLocked()
	}
	if model != nil && *model == "" {
		m.mu.Unlock()
		return SessionSummary{}, newError(http.StatusBadRequest, "model must be an offered model ID")
	}
	nextModel, nextEffort, nextSize := s.model, s.effort, cmp.Or(s.contextSize, "default")
	if model != nil {
		nextModel = *model
	}
	if effort != nil {
		nextEffort = *effort
	}
	if contextSize != nil {
		nextSize = cmp.Or(*contextSize, "default")
	}
	if nextModel != s.model {
		mo := m.modelLocked(s.provider, nextModel)
		if effort == nil && !slices.Contains(mo.Efforts, nextEffort) {
			nextEffort = ""
		}
		if contextSize == nil && !slices.ContainsFunc(mo.ContextSizes, func(size agentapi.ContextSize) bool { return size.ID == nextSize }) {
			nextSize = "default"
		}
	}
	if err := m.validateSelectionLocked(s.provider, nextModel, nextEffort, nextSize); err != nil {
		m.mu.Unlock()
		return SessionSummary{}, err
	}
	if s.model == nextModel && s.effort == nextEffort && cmp.Or(s.contextSize, "default") == nextSize {
		m.mu.Unlock()
		return m.Summary(id)
	}
	if busy(s.state()) {
		m.mu.Unlock()
		return SessionSummary{}, newError(http.StatusConflict, "the model, effort or context size cannot change while a turn is running")
	}
	conv := s.conv
	m.mu.Unlock()
	if conv != nil {
		ctx, cancel := context.WithTimeout(m.ctx, controlTimeout)
		err := conv.SetModel(ctx, nextModel, nextEffort, nextSize)
		cancel()
		switch {
		case err == nil, errors.Is(err, agentapi.ErrClosed):
			// The next open reapplies the selection if the conversation closed.
		case errors.Is(err, agentapi.ErrUnsupported):
			return SessionSummary{}, newError(http.StatusConflict, "this provider does not support changing the model settings")
		default:
			log.Warn("web model switch failed", "session", id, "error", err)
			return SessionSummary{}, newError(http.StatusBadGateway, "could not change the model settings: %s", shortError(err))
		}
	}
	m.mu.Lock()
	before := m.summaryLocked(s)
	s.model, s.effort, s.contextSize = nextModel, nextEffort, nextSize
	s.context = nil
	m.changedLocked(s, before)
	m.mu.Unlock()
	return m.Summary(id)
}

// modelLocked returns metadata only for an offered model.
func (m *Manager) modelLocked(provider, model string) agentapi.Model {
	for _, mo := range m.infos[provider].Models {
		if mo.ID == model {
			return mo
		}
	}
	return agentapi.Model{}
}

func (m *Manager) validateSelectionLocked(provider, model, effort, contextSize string) error {
	mo := m.modelLocked(provider, model)
	if model != "" && mo.ID == "" {
		return newError(http.StatusBadRequest, "model %q is not offered by this provider", model)
	}
	if effort != "" && (model == "" || model == "auto" || !slices.Contains(mo.Efforts, effort)) {
		return newError(http.StatusBadRequest, "effort %q is not offered by model %q", effort, model)
	}
	if contextSize != "default" && (!m.infos[provider].Capabilities.ContextSize || model == "" || model == "auto" || !slices.ContainsFunc(mo.ContextSizes, func(size agentapi.ContextSize) bool { return size.ID == contextSize && size.Tokens > 0 })) {
		return newError(http.StatusBadRequest, "context size %q is not offered by model %q", contextSize, model)
	}
	return nil
}
