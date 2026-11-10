package web

import "net/http"

// Rename sets the Task's typed name. An empty name shows the Task's title
// again; the provider's conversation is not renamed. A title job running
// meanwhile leaves the Task alone.
func (m *Manager) Rename(id, name string) (SessionSummary, error) {
	clean, err := cleanTaskName(name)
	if err != nil {
		return SessionSummary{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return SessionSummary{}, newError(http.StatusNotFound, msgSessionNotFound)
	}
	if s.stage == StageArchived {
		return SessionSummary{}, s.readOnlyLocked()
	}
	before := m.summaryLocked(s)
	s.name = clean
	s.renames++
	m.changedLocked(s, before)
	return m.summaryLocked(s), nil
}
