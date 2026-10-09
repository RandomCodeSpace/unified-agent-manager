package web

import (
	"context"
	"errors"
	"net/http"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// TaskUsageMetrics returns a transient native snapshot. Reading it never
// records host tokens/AI units or attaches metrics to stored Task state.
func (m *Manager) TaskUsageMetrics(ctx context.Context, id string) (*agentapi.TaskUsageMetrics, error) {
	s, err := m.lookup(id)
	if err != nil {
		return nil, err
	}
	s.op.Lock()
	defer s.op.Unlock()
	m.mu.Lock()
	err = s.readOnlyLocked()
	if err == nil && (m.closed || s.removed || s.conv == nil || s.state() == StateStarting) {
		err = newError(http.StatusConflict, "usage metrics need an already-open Task")
	}
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if err = m.checkHolder(s); err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.closed || s.removed || m.sessions[id] != s || s.conv == nil || s.state() == StateStarting || s.stage != StageActive {
		m.mu.Unlock()
		return nil, newError(http.StatusConflict, "the Task's native usage is no longer available")
	}
	conv, convID, gen := s.conv, s.convID, s.gen
	model, effort, tier := s.model, s.effort, s.contextSize
	m.mu.Unlock()
	reader, ok := conv.(agentapi.UsageMetricsReader)
	if !ok {
		return nil, newError(http.StatusConflict, "this provider does not support native usage metrics")
	}
	ctx, cancel := context.WithTimeout(ctx, controlTimeout)
	defer cancel()
	out, err := reader.UsageMetrics(ctx)
	m.mu.Lock()
	current := !m.closed && !s.removed && m.sessions[id] == s && s.conv == conv && s.convID == convID && s.gen == gen && s.stage == StageActive && s.model == model && s.effort == effort && s.contextSize == tier
	m.mu.Unlock()
	if !current {
		return nil, newError(http.StatusConflict, "the Task or selection changed; reopen usage metrics")
	}
	if errors.Is(err, agentapi.ErrUnsupported) {
		return nil, newError(http.StatusConflict, "this runtime does not support native usage metrics")
	}
	if errors.Is(err, agentapi.ErrClosed) {
		return nil, newError(http.StatusConflict, "the Task's native usage is no longer available")
	}
	if err != nil || out == nil {
		return nil, newError(http.StatusBadGateway, "the provider could not read native usage metrics")
	}
	return out, nil
}

func (s *Server) handleTaskUsageMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	out, err := s.m.TaskUsageMetrics(r.Context(), r.PathValue("id"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
