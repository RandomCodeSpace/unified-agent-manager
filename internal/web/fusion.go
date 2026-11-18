package web

import (
	"context"
	"errors"
	"net/http"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// FusionTurns reads the Copilot Fusion turns the Thread's conversation
// recorded, oldest first: none when its provider keeps no such record or the
// conversation is gone. It is read on demand, never held or persisted.
func (m *Manager) FusionTurns(id string) ([]agentapi.Fusion, error) {
	m.mu.Lock()
	s := m.sessions[id]
	if s == nil {
		m.mu.Unlock()
		return nil, newError(http.StatusNotFound, msgSessionNotFound)
	}
	reader, _ := m.providers[s.provider].(agentapi.FusionReader)
	req := agentapi.ReadRequest{ConversationID: s.convID, Workdir: s.workdir}
	gone := s.convID == "" || s.archiveGone
	m.mu.Unlock()
	if reader == nil || gone {
		return []agentapi.Fusion{}, nil
	}
	release, err := m.readSlot(m.ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	ctx, cancel := context.WithTimeout(m.ctx, openTimeout)
	defer cancel()
	turns, err := reader.ReadFusion(ctx, req)
	if errors.Is(err, agentapi.ErrConversationNotFound) {
		return []agentapi.Fusion{}, nil
	}
	return turns, err
}

func (s *Server) handleFusion(w http.ResponseWriter, r *http.Request) {
	turns, err := s.m.FusionTurns(r.PathValue("id"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"turns": turns})
}
