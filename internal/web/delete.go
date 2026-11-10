package web

import (
	"fmt"
	"net/http"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// Delete deletes an archived Task's record and its stored attachments. It
// is refused for any other stage. The conversation is never deleted at the
// provider.
func (m *Manager) Delete(id string) error {
	return m.deleteTask(id)
}

func (m *Manager) deleteTask(id string) error {
	s, err := m.lookup(id)
	if err != nil {
		return err
	}
	s.op.Lock()
	defer s.op.Unlock()
	m.mu.Lock()
	gone, closed, archived := s.removed, m.closed, s.stage == StageArchived
	m.mu.Unlock()
	switch {
	case gone:
		return newError(http.StatusNotFound, msgSessionNotFound)
	case closed:
		return errShuttingDown
	case !archived:
		return newError(http.StatusConflict, "only an archived thread can be deleted; archive it first")
	}
	var dropped []store.WebFork
	if err := m.store.Update(func(cfg *store.Config) error {
		dropped = nil
		key := store.Key(s.provider, s.id)
		if rec, ok := cfg.Sessions[key]; ok && rec.ID == s.id && rec.Surface == store.SurfaceWeb {
			delete(cfg.Sessions, key)
		}
		// Its unresolved branch requests can no longer be reconciled.
		for requestID, pending := range cfg.WebForks {
			if pending.Provider == s.provider && pending.Lineage.SourceTaskID == s.id {
				dropped = append(dropped, pending)
				delete(cfg.WebForks, requestID)
			}
		}
		pruneGitLabCommits(cfg)
		return nil
	}); err != nil {
		return fmt.Errorf("delete web session: %w", err)
	}
	m.mu.Lock()
	conv := m.forgetLocked(s)
	m.kickWorktreeRemoveLocked(s)
	m.kickForkWorktreesRemoveLocked(dropped)
	m.mu.Unlock()
	if conv != nil {
		m.closeConversation(conv)
	}
	removeUploads(m.taskUploadDir(s.id))
	log.Info("web session deleted", "session", id)
	return nil
}

// forgetLocked drops a session whose record was deleted and returns its open
// conversation for the caller to close. The caller holds s.op.
func (m *Manager) forgetLocked(s *webSession) agentapi.Conversation {
	if s.removed || m.sessions[s.id] != s {
		return nil
	}
	conv := s.conv
	s.conv = nil
	s.gen++
	s.removed = true
	m.cancelHistoryLocked(s)
	s.stopPreviews()
	s.textBuffers = nil
	m.archive.forget(s.id)
	m.forgetDrawingsLocked(s.id)
	delete(m.sessions, s.id)
	for grants := range m.fileGrants {
		grants.revokeTask(s.id)
	}
	delete(m.dirty, s.id)
	m.dropPairsLocked(s.id, true)
	m.broadcastLocked("session_removed", "", func(seq uint64) any { return sessionRemovedEvent{Seq: seq, SessionID: s.id} })
	return conv
}
