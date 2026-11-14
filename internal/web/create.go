package web

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// CreateRequest is the POST /api/sessions body. Name may be empty; the
// provider's title is shown until the user names the Task.
type CreateRequest struct {
	ProjectID   string `json:"project_id"`
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	Effort      string `json:"effort"`
	ContextSize string `json:"context_size"`
	Agent       string `json:"agent"` // custom agent ID; "" is the provider's default
	// OutputStyle is the Thread's own output style name; "" uses its
	// Project's (output_style.go).
	OutputStyle string `json:"output_style,omitempty"`
	// Skills is the Thread's own skill list; nil uses its Project's
	// (skills_choice.go).
	Skills    *[]string `json:"skills,omitempty"`
	Name      string    `json:"name"`
	Prompt    string    `json:"prompt"`
	RequestID string    `json:"request_id"`
	// Mode is safe (also when empty) or yolo.
	Mode string `json:"mode"`
	// Place is where the Thread runs; nil is the Project's current branch.
	Place *PlaceRequest `json:"place,omitempty"`
	// spawnedBy is the Task whose uam_create_task call creates this one, and
	// id the new Task's ID that call chose. Only that call sets them.
	id, spawnedBy string
	// routineID is the routine whose run creates this Task, and autopilot
	// whether the run turns autopilot on before the first message; only a
	// run sets them.
	routineID string
	autopilot bool
	// rerunOf is the Task Rerun runs again.
	rerunOf string
	// samePlace and sameWorkdir are the worktree place and folder of that
	// Task when it has one; the new Task runs there, not in a new place.
	samePlace   *store.WebPlace
	sameWorkdir string
	// gitlabRegister binds the reserved investigation atomically with its Task.
	gitlabRegister func(*store.Config) error
}

// Create opens a new provider conversation in a Project's directory, records
// the session (a Task), and, when a prompt is given, submits it through the
// same path as Submit.
func (m *Manager) Create(req CreateRequest) (SessionSummary, error) {
	prov, workdir, mode, err := m.checkCreate(&req)
	if err != nil {
		return SessionSummary{}, err
	}
	return m.createChecked(req, prov, workdir, mode)
}

// checkCreate refuses what Create refuses before it opens a conversation.
// It cleans req's name, fills in its context size, and returns its provider,
// its Project's directory and its mode.
func (m *Manager) checkCreate(req *CreateRequest) (agentapi.Provider, string, store.Mode, error) {
	prov, err := m.availableProvider(req.Provider, req.Model)
	if err != nil {
		return nil, "", "", err
	}
	m.mu.Lock()
	project := m.projects[req.ProjectID]
	var workdir string
	if project != nil {
		workdir = project.Dir
	}
	req.ContextSize = cmp.Or(req.ContextSize, "default")
	selectionErr := m.validateSelectionLocked(prov.Name(), req.Model, req.Effort, req.ContextSize)
	m.mu.Unlock()
	if project == nil {
		return nil, "", "", newError(http.StatusBadRequest, "unknown project_id %q", req.ProjectID)
	}
	if selectionErr != nil {
		return nil, "", "", selectionErr
	}
	if req.Name, err = cleanTaskName(req.Name); err != nil {
		return nil, "", "", err
	}
	if err := checkPlace(req.Place); err != nil {
		return nil, "", "", err
	}
	mode := store.ModeSafe
	if req.Mode != "" {
		if mode, err = parseMode(req.Mode); err != nil {
			return nil, "", "", err
		}
	}
	if mode == store.ModeAssisted {
		m.syncAssisted(prov.Name())
		m.mu.Lock()
		err = m.assistedSupportLocked(prov.Name())
		m.mu.Unlock()
		if err != nil {
			return nil, "", "", err
		}
	}
	if info, err := os.Stat(workdir); err != nil || !info.IsDir() {
		return nil, "", "", newError(http.StatusConflict, "the project directory %s no longer exists", workdir)
	}
	if strings.TrimSpace(req.Prompt) != "" && len(req.Prompt) > maxPromptBytes {
		return nil, "", "", newError(http.StatusRequestEntityTooLarge, msgPromptTooLarge)
	}
	if err := m.checkAgent(prov.Name(), workdir, req.Agent); err != nil {
		return nil, "", "", err
	}
	m.mu.Lock()
	err = checkOutputStyleChoice(m.settings.OutputStyles, req.OutputStyle)
	m.mu.Unlock()
	if err != nil {
		return nil, "", "", err
	}
	if req.Skills, err = cleanSkillChoice(req.Skills); err != nil {
		return nil, "", "", err
	}
	return prov, workdir, mode, nil
}

// createChecked is Create once checkCreate has taken req.
func (m *Manager) createChecked(req CreateRequest, prov agentapi.Provider, workdir string, mode store.Mode) (SessionSummary, error) {
	var err error
	name := req.Name
	hasPrompt := strings.TrimSpace(req.Prompt) != ""
	reqID := req.RequestID
	if reqID != "" && !validRequestID(reqID) {
		return SessionSummary{}, newError(http.StatusBadRequest, msgRequestIDNotUUID)
	}
	if reqID != "" {
		if existing, found := m.claimCreate(reqID); found {
			return existing, nil
		}
		defer m.releaseCreate(reqID)
	} else if hasPrompt {
		if reqID, err = newUUID(); err != nil {
			return SessionSummary{}, fmt.Errorf("generate request id: %w", err)
		}
	}
	if m.isClosed() {
		return SessionSummary{}, errShuttingDown
	}
	id := req.id
	if id == "" {
		if id, err = newUUID(); err != nil {
			return SessionSummary{}, fmt.Errorf("generate session id: %w", err)
		}
	}
	m.mu.Lock()
	var project Project
	if p := m.projects[req.ProjectID]; p != nil {
		project = *p
	}
	m.mu.Unlock()
	project.Dir = workdir
	var place *store.WebPlace
	undoPlace := func() {}
	if req.samePlace != nil {
		release, err := m.keepWorktree(m.ctx, project.Dir, req.samePlace)
		if err != nil {
			return SessionSummary{}, err
		}
		defer release()
		workdir, place = req.sameWorkdir, req.samePlace
	} else if workdir, place, undoPlace, err = m.makePlace(m.ctx, project, req.Place, firstNonEmpty(req.Name, req.Prompt)); err != nil {
		return SessionSummary{}, err
	}
	registered := false
	defer func() {
		if !registered {
			undoPlace()
		}
	}()
	now := m.now()
	s := newSession(id, prov.Name(), name, workdir, "", now)
	s.checkout = m.locateCheckout(m.ctx, s.workdir)
	s.projectID, s.model, s.mode = req.ProjectID, req.Model, mode
	s.place, s.branch = place, project.Branch
	if place != nil {
		s.branch = place.Branch
	}
	switched := req.Place != nil && req.samePlace == nil && (req.Place.Mode == placeSwitch || req.Place.Mode == placeBranch)
	if switched && place == nil {
		s.branch, _ = readBranch(m.ctx, workdir)
	}
	s.effort, s.contextSize, s.agent, s.style, s.skillChoice = req.Effort, req.ContextSize, req.Agent, req.OutputStyle, req.Skills
	m.mu.Lock()
	m.pinOutputStyleLocked(s, req.ProjectID)
	m.pinSkillsLocked(s, req.ProjectID)
	m.mu.Unlock()
	s.createReq = reqID
	s.spawnedBy = req.spawnedBy
	s.routineID = req.routineID
	s.rerunOf = req.rerunOf
	s.nativeChanges = prov.Capabilities().SessionDiffNeedsTracking
	// A new conversation has no earlier record: everything streams in.
	s.history = HistoryLoaded
	s.editsKnown = true
	s.gen = 1
	m.mu.Lock()
	open := m.withHostToolsLocked(agentapi.OpenRequest{SessionID: id, Workdir: workdir, Title: name, Model: req.Model, Effort: req.Effort, ContextSize: req.ContextSize, Agent: req.Agent, Events: sink{m: m, s: s, gen: 1}, ValidateFile: m.declarationValidator(id, workdir), AssistedApprovalModel: approvalModel(mode)}, s)
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(m.ctx, openTimeout)
	conv, err := prov.Open(ctx, open)
	cancel()
	if err != nil {
		log.Warn("open web conversation failed", "provider", prov.Name(), "error", err)
		if mode == store.ModeAssisted && errors.Is(err, agentapi.ErrUnsupported) {
			m.syncAssisted(prov.Name())
			m.mu.Lock()
			refused := m.assistedSupportLocked(prov.Name())
			m.mu.Unlock()
			if refused != nil {
				return SessionSummary{}, refused
			}
		}
		status := http.StatusBadGateway
		if errors.Is(err, agentapi.ErrAgentUnavailable) {
			status = http.StatusConflict
		}
		return SessionSummary{}, newError(status, "could not start a %s conversation: %s", prov.DisplayName(), shortError(err))
	}
	convID := conv.ID()
	// The ID is persisted and later passed back as an argv/URL value; refuse
	// one the store would reject on its next load.
	if !store.ValidProviderSessionID(convID) {
		m.closeConversation(conv)
		return SessionSummary{}, newError(http.StatusBadGateway, "the provider returned an unusable conversation id")
	}
	rec := store.SessionRecord{
		ID: id, Agent: prov.Name(), Name: name, Mode: mode, Workdir: workdir,
		CreatedAt: now, LastSeenAt: now, Status: store.StatusActive, Surface: store.SurfaceWeb,
		ProviderSessionID: convID, Web: &store.WebState{Turn: StateIdle, UpdatedAt: now, ProjectID: req.ProjectID, Model: req.Model, Effort: req.Effort, ContextSize: req.ContextSize, Agent: req.Agent, OutputStyle: s.style, Skills: s.skillChoice, SpawnedBy: req.spawnedBy, RoutineID: req.routineID, RerunOf: req.rerunOf, NativeChanges: s.nativeChanges, Place: s.clonedPlace()},
	}
	check := req.gitlabRegister
	if req.spawnedBy != "" {
		check = func(cfg *store.Config) error { return spawnRoom(cfg, rec) }
	}
	if err := m.register(s, conv, rec, check); err != nil {
		m.closeConversation(conv)
		return SessionSummary{}, err
	}
	registered = true
	log.Info("web session created", "session", id, "provider", prov.Name())
	m.mu.Lock()
	if switched {
		// The Project and the Threads sharing its folder are on the new branch.
		m.kickBranchLocked(req.ProjectID)
	}
	m.mu.Unlock()
	if req.autopilot {
		m.startAutopilot(s)
	}
	if hasPrompt {
		if _, err := m.submit(s, turnInput{text: req.Prompt}, reqID, ModeSend); err != nil {
			log.Warn("initial web prompt not submitted", "session", id, "error", err)
		}
	}
	return m.Summary(id)
}

// register writes a new session's record and lists it. The Project may have
// been removed while the conversation opened; projectMu orders this against
// RemoveProject. check, when set, may refuse the write from the stored
// records. conv is nil for a Task whose conversation is not open.
func (m *Manager) register(s *webSession, conv agentapi.Conversation, rec store.SessionRecord, check func(*store.Config) error) error {
	m.projectMu.Lock()
	defer m.projectMu.Unlock()
	m.mu.Lock()
	projectGone := m.projects[s.projectID] == nil
	m.mu.Unlock()
	if projectGone {
		return newError(http.StatusConflict, "the project was removed")
	}
	if err := m.store.Update(func(cfg *store.Config) error {
		// Once Shutdown has begun, the Task would not be listed, so its
		// record is not written either.
		if m.isClosed() {
			return errShuttingDown
		}
		if check != nil {
			if err := check(cfg); err != nil {
				return err
			}
		}
		if !cfg.PutSession(store.Key(rec.Agent, rec.ID), rec) {
			return errors.New("session id collides with an existing record")
		}
		return nil
	}); err != nil {
		return fmt.Errorf("save web session: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errShuttingDown
	}
	s.convID = rec.ProviderSessionID
	s.conv = conv
	s.persisted = persistKey{turn: rec.Web.Turn, name: rec.Name, convID: rec.ProviderSessionID, projectID: s.projectID, model: s.model, effort: s.effort, contextSize: s.contextSize, agent: s.agent, style: s.style, skills: skillsKey(s.skillChoice), title: rec.Web.Title, mode: s.mode}
	if rec.Web.Place != nil {
		s.persisted.place = *rec.Web.Place
	}
	m.sessions[s.id] = s
	if s.historyRead {
		m.enforceHistoryBudgetLocked(s)
	}
	m.autoAllowPendingLocked(s)
	// Announce the new session. Events that arrived during Open may have
	// changed its state before the record existed; this also persists that.
	m.changedLocked(s, SessionSummary{})
	return nil
}

// claimCreate makes one create per request ID proceed. A repeat of a
// finished create returns that session; a concurrent repeat waits for it.
func (m *Manager) claimCreate(reqID string) (SessionSummary, bool) {
	for {
		m.mu.Lock()
		for _, s := range m.sessions {
			if s.createReq == reqID || s.hasSubmission(reqID) {
				summary := m.summaryLocked(s)
				m.mu.Unlock()
				return summary, true
			}
		}
		wait, running := m.creating[reqID]
		if !running {
			m.creating[reqID] = make(chan struct{})
			m.mu.Unlock()
			return SessionSummary{}, false
		}
		m.mu.Unlock()
		<-wait
	}
}

func (m *Manager) releaseCreate(reqID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ch, ok := m.creating[reqID]; ok {
		close(ch)
		delete(m.creating, reqID)
	}
}

// canonicalWorkdir validates a requested project directory and returns its
// canonical path.
func canonicalWorkdir(p string) (string, error) {
	if err := checkPathText("dir", p); err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", newError(http.StatusBadRequest, "dir does not exist")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", newError(http.StatusBadRequest, "dir is not a directory")
	}
	return filepath.Clean(resolved), nil
}
