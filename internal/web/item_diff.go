package web

import (
	"context"
	"errors"
	"net/http"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

type itemDiffBody struct {
	detailBarrier
	AgentID string `json:"agent_id"`
	ItemID  string `json:"item_id"`
	EventID string `json:"event_id"`
	agentapi.ItemDiff
}

// ItemDiff reads recorded native detail directly; it neither loads the
// transcript into the Manager nor resumes a closed provider conversation.
func (m *Manager) ItemDiff(ctx context.Context, id, agent, item, event, path string) (itemDiffBody, error) {
	if !validDetailID(agent, true) || !validDetailID(item, false) || !validDetailID(event, false) || len(event) > 256 || !validDetailID(path, false) {
		return itemDiffBody{}, newError(400, "invalid edit reference")
	}
	m.mu.Lock()
	s := m.sessions[id]
	if s == nil || s.removed {
		m.mu.Unlock()
		return itemDiffBody{}, newError(404, msgSessionNotFound)
	}
	reader, ok := m.providers[s.provider].(agentapi.ItemDiffReader)
	if !ok || s.convID == "" {
		m.mu.Unlock()
		return itemDiffBody{}, newError(409, "this task has no recorded native edit detail")
	}
	conv, workdir, gen, historyGen := s.convID, s.workdir, s.gen, s.historyGen
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, openTimeout)
	defer cancel()
	release, err := m.readSlot(ctx)
	if err != nil {
		return itemDiffBody{}, err
	}
	defer release()
	result, err := reader.ReadItemDiff(ctx, agentapi.ItemDiffRequest{ReadRequest: agentapi.ReadRequest{ConversationID: conv, Workdir: workdir}, AgentID: agent, ItemID: item, EventID: event, Path: path})
	if errors.Is(err, agentapi.ErrItemNotFound) || errors.Is(err, agentapi.ErrConversationNotFound) {
		return itemDiffBody{}, newError(404, "recorded edit not found")
	}
	if err != nil {
		return itemDiffBody{}, err
	}
	if result.Path != path || len(result.Patch) > agentapi.MaxEditPatchBytes || !utf8.ValidString(result.Patch) || !validDetailID(result.Status, false) || len(result.Status) > 32 {
		return itemDiffBody{}, newError(502, "invalid native edit detail")
	}
	if result.Status != "available" {
		result.Patch = ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.sessions[id]
	if current != s || s.removed || m.closed || s.convID != conv || s.gen != gen || s.historyGen != historyGen {
		return itemDiffBody{}, newError(409, msgHistoryChanged)
	}
	return itemDiffBody{detailBarrier: detailBarrier{m.seq, m.epoch, id}, AgentID: agent, ItemID: item, EventID: event, ItemDiff: result}, nil
}

func (s *Server) handleItemDiff(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if len(q["agent_id"]) > 1 || len(q["event_id"]) != 1 || len(q["path"]) != 1 || len(r.URL.RawQuery) > 16384 {
		writeFailure(w, newError(400, "invalid edit reference"))
		return
	}
	body, err := s.m.ItemDiff(r.Context(), r.PathValue("id"), q.Get("agent_id"), r.PathValue("item_id"), q.Get("event_id"), q.Get("path"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, body)
}
