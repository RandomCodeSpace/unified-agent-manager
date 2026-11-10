package web

import (
	"cmp"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func (m *Manager) projectsLocked() []Project {
	out := make([]Project, 0, len(m.projects))
	for _, p := range m.projects {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Projects returns every Project, oldest first.
func (m *Manager) Projects() []Project {
	m.refreshBranches(m.ctx, false)
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.projectsLocked()
}

func (m *Manager) publishProjectLocked(p Project) {
	m.broadcastLocked("project", "", func(seq uint64) any { return projectEvent{Seq: seq, Project: p} })
}

// AddProject adds the directory dir as a Project and gives it a badge. A
// directory has at most one Project; adding it again reports the existing
// one with 409.
func (m *Manager) AddProject(dir, name string) (Project, error) {
	canonical, err := canonicalWorkdir(dir)
	if err != nil {
		return Project{}, err
	}
	clean, err := cleanName(name, filepath.Base(canonical))
	if err != nil {
		return Project{}, err
	}
	id, err := newUUID()
	if err != nil {
		return Project{}, fmt.Errorf("generate project id: %w", err)
	}
	branch, noGit := readBranch(m.ctx, canonical)
	defaultBranch := readDefaultBranch(m.ctx, canonical)
	var link *string
	if detected, ok := detectBranchLink(m.ctx, canonical); ok {
		link = &detected
	}
	m.projectMu.Lock()
	defer m.projectMu.Unlock()
	m.mu.Lock()
	closed := m.closed
	var existing string
	for _, p := range m.projects {
		if p.Dir == canonical {
			existing = p.ID
		}
	}
	m.mu.Unlock()
	if closed {
		return Project{}, errShuttingDown
	}
	if existing != "" {
		return Project{}, projectExists(existing)
	}
	p := Project{ID: id, Name: clean, Dir: canonical, CreatedAt: m.now(), Branch: branch, NoGit: noGit, DefaultBranch: defaultBranch, BranchLink: link, linkTried: true}
	if err := m.store.Update(func(cfg *store.Config) error {
		for _, other := range cfg.WebProjects {
			if other.Dir == canonical {
				existing = other.ID
				return projectExists(other.ID)
			}
		}
		if cfg.WebProjects == nil {
			cfg.WebProjects = map[string]store.WebProject{}
		}
		badge := newBadge(clean, cfg.WebProjects, m.pick)
		p.Badge = Badge(badge)
		cfg.WebProjects[id] = store.WebProject{ID: id, Name: clean, Dir: canonical, CreatedAt: p.CreatedAt, Badge: badge, BranchLink: link}
		return nil
	}); err != nil {
		if existing != "" {
			return Project{}, err
		}
		return Project{}, fmt.Errorf("save web project: %w", err)
	}
	m.mu.Lock()
	m.projects[id] = &p
	m.branchAt[id] = time.Now()
	m.publishProjectLocked(p)
	m.mu.Unlock()
	log.Info("web project added", "project", id)
	return p, nil
}

func projectExists(id string) *Error {
	return &Error{Status: http.StatusConflict, Message: "this directory already has a project", ProjectID: id}
}

// UpdateProject renames a Project. An empty name resets it to the
// directory's base name.
func (m *Manager) UpdateProject(id, name string) (Project, error) {
	return m.patchProject(id, &name, nil, nil)
}

// patchProject sets the Project's name and branch link, each when given.
// An empty branch link turns the link off.
func (m *Manager) patchProject(id string, name, branchLink, outputStyle *string) (Project, error) {
	m.projectMu.Lock()
	defer m.projectMu.Unlock()
	m.mu.Lock()
	p := m.projects[id]
	var next Project
	if p != nil {
		next = *p
	}
	m.mu.Unlock()
	if p == nil {
		return Project{}, errProjectNotFound
	}
	var err error
	if name != nil {
		if next.Name, err = cleanName(*name, filepath.Base(next.Dir)); err != nil {
			return Project{}, err
		}
	}
	if branchLink != nil {
		link, err := cleanBranchLink(*branchLink)
		if err != nil {
			return Project{}, err
		}
		next.BranchLink = &link
	}
	if outputStyle != nil {
		m.mu.Lock()
		err = checkOutputStyleChoice(m.settings.OutputStyles, *outputStyle)
		m.mu.Unlock()
		if err != nil {
			return Project{}, err
		}
		next.OutputStyle = *outputStyle
	}
	if err := m.store.Update(func(cfg *store.Config) error {
		stored, ok := cfg.WebProjects[id]
		if !ok {
			return errProjectNotFound
		}
		stored.Name, stored.BranchLink, stored.OutputStyle = next.Name, next.BranchLink, next.OutputStyle
		cfg.WebProjects[id] = stored
		return nil
	}); err != nil {
		if errors.Is(err, errProjectNotFound) {
			return Project{}, err
		}
		return Project{}, fmt.Errorf("save web project: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setProjectLocked(p, next)
	return *p, nil
}

// taskDefaults checks the Task defaults setting the way Create checks a
// Task's selection and mode, and returns them with an empty context size made
// "default". The provider need only be registered.
func (m *Manager) taskDefaults(d TaskDefaults) (TaskDefaults, error) {
	d.ContextSize = cmp.Or(d.ContextSize, "default")
	m.mu.Lock()
	registered := m.providers[d.Provider] != nil
	selectionErr := m.validateSelectionLocked(d.Provider, d.Model, d.Effort, d.ContextSize)
	m.mu.Unlock()
	if !registered {
		return TaskDefaults{}, newError(http.StatusBadRequest, msgUnknownProvider, d.Provider)
	}
	if selectionErr != nil {
		return TaskDefaults{}, selectionErr
	}
	if _, err := parseDefaultMode(d.Mode); err != nil {
		return TaskDefaults{}, err
	}
	return d, nil
}

var errProjectNotFound = newError(http.StatusNotFound, "project not found")

// RemoveProject deletes a Project and its Task records. It is refused unless
// every Task in it is archived. Conversations are never deleted at the
// provider, and the directory is not touched.
func (m *Manager) RemoveProject(id string) error {
	return m.removeProject(id)
}

func (m *Manager) removeProject(id string) error {
	m.projectMu.Lock()
	defer m.projectMu.Unlock()
	m.mu.Lock()
	if m.projects[id] == nil {
		m.mu.Unlock()
		return errProjectNotFound
	}
	var tasks []*webSession
	for _, s := range m.sessions {
		if s.projectID == id {
			tasks = append(tasks, s)
		}
	}
	m.mu.Unlock()
	// Holding every Task's op keeps prompts, opens and model changes out
	// until the Tasks are gone.
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].id < tasks[j].id })
	for _, s := range tasks {
		s.op.Lock()
		defer s.op.Unlock()
	}
	m.mu.Lock()
	closed := m.closed
	unarchived := slices.ContainsFunc(tasks, func(s *webSession) bool { return !s.removed && s.stage != StageArchived })
	m.mu.Unlock()
	switch {
	case closed:
		return errShuttingDown
	case unarchived:
		return newError(http.StatusConflict, "archive every thread in this project first")
	}
	// The Project's routines go with it, in the same write.
	m.routines.mu.Lock()
	err := m.store.Update(func(cfg *store.Config) error {
		delete(cfg.WebProjects, id)
		for key, rec := range cfg.Sessions {
			if rec.Surface == store.SurfaceWeb && rec.Web != nil && rec.Web.ProjectID == id {
				delete(cfg.Sessions, key)
			}
		}
		for key, r := range cfg.WebRoutines {
			if r.ProjectID == id {
				delete(cfg.WebRoutines, key)
			}
		}
		pruneGitLabCommits(cfg)
		return nil
	})
	if err == nil {
		m.forgetRoutinesLocked(id)
	}
	m.routines.mu.Unlock()
	if err != nil {
		return fmt.Errorf("remove web project: %w", err)
	}
	m.mu.Lock()
	var convs []agentapi.Conversation
	for _, s := range tasks {
		if conv := m.forgetLocked(s); conv != nil {
			convs = append(convs, conv)
		}
	}
	delete(m.projects, id)
	delete(m.branchAt, id)
	m.broadcastLocked("project_removed", "", func(seq uint64) any { return projectRemovedEvent{Seq: seq, ProjectID: id} })
	m.mu.Unlock()
	for _, conv := range convs {
		m.closeConversation(conv)
	}
	for _, s := range tasks {
		removeUploads(m.taskUploadDir(s.id))
	}
	log.Info("web project removed", "project", id, "tasks", len(tasks))
	return nil
}
