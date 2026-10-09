package web

import (
	"context"
	"errors"
	"net/http"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// ContextBreakdown is returned only by the on-demand reader; it is never
// attached to summaries, history, SSE snapshots or stored Task state.
type ContextBreakdown struct {
	Info        *agentapi.ContextInfo        `json:"info,omitempty"`
	Attribution *agentapi.ContextAttribution `json:"attribution,omitempty"`
}

func (m *Manager) ContextBreakdown(ctx context.Context, id string, attribution bool) (ContextBreakdown, error) {
	s, err := m.lookup(id)
	if err != nil {
		return ContextBreakdown{}, err
	}
	s.op.Lock()
	defer s.op.Unlock()
	m.mu.Lock()
	err = s.readOnlyLocked()
	if err == nil && (m.closed || s.removed || s.conv == nil || s.state() == StateStarting) {
		err = newError(http.StatusConflict, "context breakdown needs an already-open Task")
	}
	m.mu.Unlock()
	if err != nil {
		return ContextBreakdown{}, err
	}
	if err = m.checkHolder(s); err != nil {
		return ContextBreakdown{}, err
	}
	m.mu.Lock()
	if m.closed || s.removed || s.conv == nil || s.state() == StateStarting || s.stage != StageActive {
		m.mu.Unlock()
		return ContextBreakdown{}, newError(http.StatusConflict, "the Task's context is no longer available")
	}
	conv, gen, convID := s.conv, s.gen, s.convID
	model, effort, tier := s.model, s.effort, s.contextSize
	m.mu.Unlock()
	reader, ok := conv.(agentapi.ContextReader)
	if !ok {
		return ContextBreakdown{}, newError(http.StatusConflict, "this provider does not support context breakdown")
	}
	ctx, cancel := context.WithTimeout(ctx, controlTimeout)
	defer cancel()
	var out ContextBreakdown
	if attribution {
		out.Attribution, err = reader.ContextAttribution(ctx)
	} else {
		out.Info, err = reader.ContextInfo(ctx)
	}
	m.mu.Lock()
	current := !m.closed && !s.removed && s.conv == conv && s.gen == gen && s.convID == convID && s.model == model && s.effort == effort && s.contextSize == tier && s.stage == StageActive
	m.mu.Unlock()
	if !current {
		return ContextBreakdown{}, newError(http.StatusConflict, "the Task or model selection changed; reopen context breakdown")
	}
	if errors.Is(err, agentapi.ErrUnsupported) {
		return ContextBreakdown{}, newError(http.StatusConflict, "this runtime does not support context breakdown")
	}
	if errors.Is(err, agentapi.ErrClosed) {
		return ContextBreakdown{}, newError(http.StatusConflict, "the Task's context is no longer available")
	}
	if err != nil {
		return ContextBreakdown{}, newError(http.StatusBadGateway, "the provider could not read context breakdown")
	}
	return out, nil
}

func (s *Server) handleContextBreakdown(w http.ResponseWriter, r *http.Request) {
	attribution := r.URL.Query().Get("attribution")
	if attribution != "" && attribution != "true" {
		writeError(w, http.StatusBadRequest, "attribution must be true when specified")
		return
	}
	out, err := s.m.ContextBreakdown(r.Context(), r.PathValue("id"), attribution == "true")
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
