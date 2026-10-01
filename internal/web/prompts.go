package web

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// Saved prompts are messages the owner keeps to insert again from a
// composer: for every Project, or for one. They are part of Settings, so
// every browser gets them in the snapshot and in each settings frame, and
// they change only through these calls, one prompt at a time, so two
// browsers never overwrite each other's list.

// SavedPrompt is one saved prompt; ProjectID is empty for every Project.
type SavedPrompt = store.WebSavedPrompt

// PromptRequest bodies: AddPromptRequest adds a prompt, RenamePromptRequest
// renames one.
type AddPromptRequest struct {
	Name      string `json:"name"`
	Text      string `json:"text"`
	ProjectID string `json:"project_id"`
}

type RenamePromptRequest struct {
	Name string `json:"name"`
}

// AddPrompt saves a new prompt and returns it.
func (m *Manager) AddPrompt(req AddPromptRequest) (SavedPrompt, error) {
	id, err := newUUID()
	if err != nil {
		return SavedPrompt{}, fmt.Errorf("generate prompt id: %w", err)
	}
	p := SavedPrompt{ID: id, Name: strings.TrimSpace(req.Name), Text: req.Text, ProjectID: req.ProjectID, CreatedAt: m.now()}
	if err := store.ValidSavedPrompt(p); err != nil {
		return SavedPrompt{}, newError(http.StatusBadRequest, "%s", err.Error())
	}
	err = m.changePrompts(func(list []SavedPrompt) ([]SavedPrompt, error) {
		if p.ProjectID != "" && m.projects[p.ProjectID] == nil {
			return nil, newError(http.StatusBadRequest, "unknown project_id %q", p.ProjectID)
		}
		if len(list) >= store.MaxSavedPrompts {
			return nil, newError(http.StatusConflict, "at most %d prompts can be saved; delete one in Settings first", store.MaxSavedPrompts)
		}
		return append(list, p), nil
	})
	return p, err
}

// RenamePrompt renames prompt id.
func (m *Manager) RenamePrompt(id, name string) (SavedPrompt, error) {
	var out SavedPrompt
	err := m.changePrompts(func(list []SavedPrompt) ([]SavedPrompt, error) {
		i := slices.IndexFunc(list, func(p SavedPrompt) bool { return p.ID == id })
		if i < 0 {
			return nil, newError(http.StatusNotFound, "prompt not found")
		}
		out = list[i]
		out.Name = strings.TrimSpace(name)
		if err := store.ValidSavedPrompt(out); err != nil {
			return nil, newError(http.StatusBadRequest, "%s", err.Error())
		}
		list[i] = out
		return list, nil
	})
	return out, err
}

// DeletePrompt deletes prompt id.
func (m *Manager) DeletePrompt(id string) error {
	return m.changePrompts(func(list []SavedPrompt) ([]SavedPrompt, error) {
		i := slices.IndexFunc(list, func(p SavedPrompt) bool { return p.ID == id })
		if i < 0 {
			return nil, newError(http.StatusNotFound, "prompt not found")
		}
		return slices.Delete(list, i, i+1), nil
	})
}

// changePrompts applies change to a copy of the saved prompts under mu,
// stores the result and sends a settings frame. change may refuse.
func (m *Manager) changePrompts(change func([]SavedPrompt) ([]SavedPrompt, error)) error {
	m.settingsMu.Lock()
	defer m.settingsMu.Unlock()
	m.mu.Lock()
	next, err := change(slices.Clone(m.settings.SavedPrompts))
	m.mu.Unlock()
	if err != nil {
		return err
	}
	if err := m.store.Update(func(cfg *store.Config) error {
		cfg.WebSettings.SavedPrompts = slices.Clone(next)
		return nil
	}); err != nil {
		return fmt.Errorf("save prompts: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings.SavedPrompts = next
	settings := m.settings
	m.broadcastLocked("settings", "", func(seq uint64) any { return settingsEvent{Seq: seq, Settings: settings} })
	return nil
}

func (s *Server) handleAddPrompt(w http.ResponseWriter, r *http.Request) {
	var req AddPromptRequest
	if !decodeBody(w, r, &req) {
		return
	}
	p, err := s.m.AddPrompt(req)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) handleRenamePrompt(w http.ResponseWriter, r *http.Request) {
	var req RenamePromptRequest
	if !decodeBody(w, r, &req) {
		return
	}
	p, err := s.m.RenamePrompt(r.PathValue("id"), req.Name)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleDeletePrompt(w http.ResponseWriter, r *http.Request) {
	if err := s.m.DeletePrompt(r.PathValue("id")); err != nil {
		writeFailure(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
