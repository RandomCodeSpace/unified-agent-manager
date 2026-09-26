package web

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"slices"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

const historyPageItems = 50
const historyPageBytes = 64 << 10

// HistoryPage is a backwards page of complete items, oldest first. Before is
// opaque and empty at the beginning of retained history. One oversized item
// is returned alone, rather than silently truncating it or preventing progress.
type HistoryPage struct {
	Seq    uint64          `json:"seq"`
	Items  []agentapi.Item `json:"items"`
	Before string          `json:"before"`
}

func historyPage(items []agentapi.Item, end int) HistoryPage {
	start, size := end, 0
	for start > 0 && end-start < historyPageItems {
		encoded, _ := json.Marshal(items[start-1])
		if start < end && size+len(encoded)+1 > historyPageBytes {
			break
		}
		size += len(encoded) + 1
		start--
	}
	page := HistoryPage{Items: slices.Clone(items[start:end])}
	if page.Items == nil {
		page.Items = []agentapi.Item{}
	}
	if start > 0 {
		page.Before = base64.RawURLEncoding.EncodeToString([]byte(items[start].ID))
	}
	return page
}

func recentDetail(d SessionDetail) SessionDetail {
	page := historyPage(d.Items, len(d.Items))
	d.Items, d.HistoryBefore = page.Items, &page.Before
	return d
}

// OlderHistory uses an item boundary, so concurrent appends cannot move the
// page. Eviction or replacement of the boundary requires a fresh snapshot.
func (m *Manager) OlderHistory(id, before string) (HistoryPage, error) {
	boundary, err := base64.RawURLEncoding.DecodeString(before)
	if err != nil || len(boundary) == 0 || len(boundary) > 4096 {
		return HistoryPage{}, newError(http.StatusBadRequest, "invalid history cursor")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return HistoryPage{}, newError(http.StatusNotFound, "session not found")
	}
	s.historyUsed = m.now()
	items := s.agentItems("")
	end := slices.IndexFunc(items, func(it agentapi.Item) bool { return it.ID == string(boundary) })
	if end < 0 {
		return HistoryPage{}, newError(http.StatusConflict, "history changed; reload the task")
	}
	page := historyPage(items, end)
	page.Seq = m.seq
	return page, nil
}

func (s *Server) handleHistoryPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("view") == compactRepresentation || r.PathValue("agent_id") != "" {
		q := r.URL.Query()
		if len(q["before"])+len(q["after"]) != 1 {
			writeFailure(w, newError(400, "provide exactly one history cursor"))
			return
		}
		page, err := s.m.CompactHistoryPage(r.PathValue("id"), r.PathValue("agent_id"), q.Get("before"), q.Get("after"))
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, page)
		return
	}
	page, err := s.m.OlderHistory(r.PathValue("id"), r.URL.Query().Get("before"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}
