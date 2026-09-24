package web

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"sort"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// maxPrevious caps the previous sessions listed for a Project.
const maxPrevious = 100
const holderTimeout = 5 * time.Second

var (
	errHeldElsewhere = newError(http.StatusConflict, "another client has this conversation open, for example copilot --resume in a terminal; close it there, then try again")
	errHolderUnknown = newError(http.StatusBadGateway, "uam could not check whether another client has this conversation open; nothing was sent or imported")
	errAlreadyTask   = newError(http.StatusConflict, "this conversation is already a task")
)

// SetHostProbe sets how the service tells that a terminal session host runs,
// for Tasks tied to a terminal session. Call it before Start.
func (m *Manager) SetHostProbe(live func(sessionName string) bool) { m.hostLive = live }

// terminalLive reports whether the host of s's terminal session runs.
func (m *Manager) terminalLive(s *webSession) bool {
	m.mu.Lock()
	host := s.terminalHost
	m.mu.Unlock()
	return host != "" && m.hostLive != nil && m.hostLive(host)
}

// checkHolder refuses a write to s's conversation while another client holds
// it open, for a provider that can tell (Capabilities.Import). The check is a
// snapshot: a client that opens the conversation right after it, or during
// the turn it starts, is not caught. The caller holds s.op; the RPC releases
// it so a slow holder check cannot block lifecycle controls. A changed Task
// must be retried against its new state.
func (m *Manager) checkHolder(s *webSession) error {
	m.mu.Lock()
	prov, convID, external := m.providers[s.provider], s.convID, s.imported || s.terminalID != ""
	key, gen := s.key(), s.gen
	m.mu.Unlock()
	if !external || convID == "" {
		return nil
	}
	s.op.Unlock()
	held, err := m.holders(m.ctx, prov, []string{convID})
	s.op.Lock()
	m.mu.Lock()
	changed, removed, closed := s.key() != key || s.gen != gen, s.removed, m.closed
	m.mu.Unlock()
	switch {
	case closed:
		return errShuttingDown
	case removed:
		return newError(http.StatusNotFound, "session not found")
	case changed:
		return newError(http.StatusConflict, "the task changed while checking whether its conversation is in use; try again")
	case err != nil:
		return err
	case slices.Contains(held, convID):
		return errHeldElsewhere
	}
	return nil
}

// checkedPrompt preserves request-ID idempotency when another submission
// completes while the holder check has released the operation lock.
func (m *Manager) checkedPrompt(s *webSession, requestID string) (Submission, bool, error) {
	err := m.checkHolder(s)
	m.mu.Lock()
	sub, found := s.findRequest(requestID)
	m.mu.Unlock()
	if found {
		return sub, true, nil
	}
	return Submission{}, false, err
}

func (m *Manager) holders(ctx context.Context, prov agentapi.Provider, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if prov == nil || !prov.Capabilities().Import {
		return nil, errHolderUnknown
	}
	ctx, cancel := context.WithTimeout(ctx, holderTimeout)
	defer cancel()
	held, err := prov.(agentapi.Importer).InUse(ctx, ids)
	if err != nil {
		log.Warn("web in-use check failed", "provider", prov.Name(), "error", err)
		return nil, errHolderUnknown
	}
	return held, nil
}

// importersLocked returns the available providers that can import, in order.
func (m *Manager) importersLocked() []string {
	var out []string
	for _, name := range m.order {
		p := m.providers[name]
		if p.Capabilities().Import && m.infos[name].Available {
			out = append(out, name)
		}
	}
	return out
}

// linkedLocked reports whether a Task is linked to conversation id of
// provider.
func (m *Manager) linkedLocked(provider, id string) bool {
	for _, s := range m.sessions {
		if s.provider == provider && s.convID == id {
			return true
		}
	}
	return false
}

// Previous lists, newest first and at most maxPrevious, the conversations
// that providers able to import recorded for a Project's directory and that
// no Task is linked to, each marked while another client holds it open.
func (m *Manager) Previous(projectID string) ([]PreviousConversation, error) {
	m.mu.Lock()
	project := m.projects[projectID]
	names := m.importersLocked()
	m.mu.Unlock()
	if project == nil {
		return nil, errProjectNotFound
	}
	out := []PreviousConversation{}
	for _, name := range names {
		list, err := m.previousOf(name, project.Dir)
		if err != nil {
			return nil, err
		}
		out = append(out, list...)
	}
	sortPrevious(out)
	return out[:min(len(out), maxPrevious)], nil
}

// PreviousCounts lists each provider once without holder checks. Counts use
// the same cap and linked-conversation exclusions as the Project list.
func (m *Manager) PreviousCounts(ctx context.Context) (map[string]int, error) {
	m.mu.Lock()
	names := m.importersLocked()
	projects := make(map[string]string, len(m.projects))
	for id, p := range m.projects {
		projects[id] = p.Dir
	}
	m.mu.Unlock()
	out := make(map[string]int, len(projects))
	for id := range projects {
		out[id] = 0
	}
	ctx, cancel := context.WithTimeout(ctx, controlTimeout)
	defer cancel()
	for _, name := range names {
		listed, err := m.listPrevious(ctx, m.providers[name], "")
		if err != nil {
			return nil, err
		}
		m.mu.Lock()
		seen := map[string]bool{}
		for _, c := range listed {
			if seen[c.ID] || !store.ValidProviderSessionID(c.ID) || m.linkedLocked(name, c.ID) {
				continue
			}
			seen[c.ID] = true
			for id, dir := range projects {
				if c.Workdir == dir && out[id] < maxPrevious {
					out[id]++
				}
			}
		}
		m.mu.Unlock()
	}
	return out, nil
}

// previousOf lists at most maxPrevious previous sessions of one provider for
// dir, with their in-use marks.
func (m *Manager) previousOf(name, dir string) ([]PreviousConversation, error) {
	prov := m.providers[name]
	ctx, cancel := context.WithTimeout(m.ctx, controlTimeout)
	defer cancel()
	listed, err := m.listPrevious(ctx, prov, dir)
	if err != nil {
		return nil, err
	}
	out := []PreviousConversation{}
	m.mu.Lock()
	for _, c := range listed {
		if store.ValidProviderSessionID(c.ID) && !m.linkedLocked(name, c.ID) {
			out = append(out, PreviousConversation{Provider: name, ConversationID: c.ID, Title: cleanTitle(c.Title), CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt})
		}
	}
	m.mu.Unlock()
	sortPrevious(out)
	out = out[:min(len(out), maxPrevious)]
	ids := make([]string, len(out))
	for i, p := range out {
		ids[i] = p.ConversationID
	}
	held, err := m.holders(ctx, prov, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].InUse = slices.Contains(held, out[i].ConversationID)
	}
	return out, nil
}

func sortPrevious(list []PreviousConversation) {
	sort.SliceStable(list, func(i, j int) bool {
		if !list[i].UpdatedAt.Equal(list[j].UpdatedAt) {
			return list[i].UpdatedAt.After(list[j].UpdatedAt)
		}
		return list[i].ConversationID < list[j].ConversationID
	})
}

// Import creates an active Task linked to a previous conversation of a
// Project's directory. Nothing is sent. The Task starts closed with the
// transcript read without opening the conversation; its next prompt opens it.
// It keeps the conversation's model when the provider offers it, otherwise
// takes the Project's defaults, and it takes the Project's mode. A
// conversation another client holds open, or a Task is linked to, is refused
// with 409.
func (m *Manager) Import(ctx context.Context, projectID, convID string) (SessionSummary, error) {
	ctx, cancelRequest := context.WithCancel(ctx)
	stop := context.AfterFunc(m.ctx, cancelRequest)
	defer func() { stop(); cancelRequest() }()
	if !store.ValidProviderSessionID(convID) {
		return SessionSummary{}, newError(http.StatusBadRequest, "invalid conversation id")
	}
	m.mu.Lock()
	var p Project
	project, closed := m.projects[projectID], m.closed
	if project != nil {
		p = *project
	}
	names := m.importersLocked()
	linked := slices.ContainsFunc(names, func(name string) bool { return m.linkedLocked(name, convID) })
	m.mu.Unlock()
	switch {
	case closed:
		return SessionSummary{}, errShuttingDown
	case project == nil:
		return SessionSummary{}, errProjectNotFound
	case linked:
		return SessionSummary{}, errAlreadyTask
	}
	if info, err := os.Stat(p.Dir); err != nil || !info.IsDir() {
		return SessionSummary{}, newError(http.StatusConflict, "the project directory %s no longer exists", p.Dir)
	}
	name, found, err := m.findPrevious(ctx, names, p.Dir, convID)
	if ctx.Err() != nil {
		return SessionSummary{}, newError(http.StatusServiceUnavailable, "import cancelled; no task was created")
	}
	if err != nil {
		return SessionSummary{}, err
	}
	if name == "" {
		return SessionSummary{}, newError(http.StatusNotFound, "no previous session %s in this project's directory", convID)
	}
	prov := m.providers[name]
	checkCtx, cancel := context.WithTimeout(ctx, controlTimeout)
	held, err := m.holders(checkCtx, prov, []string{convID})
	cancel()
	if ctx.Err() != nil {
		return SessionSummary{}, newError(http.StatusServiceUnavailable, "import cancelled; no task was created")
	}
	switch {
	case err != nil:
		return SessionSummary{}, err
	case slices.Contains(held, convID):
		return SessionSummary{}, errHeldElsewhere
	}
	reader, ok := prov.(agentapi.HistoryReader)
	if !ok {
		return SessionSummary{}, newError(http.StatusConflict, "%s cannot read the conversation without opening it", prov.DisplayName())
	}
	h, err := m.readHistory(ctx, reader, convID, p.Dir)
	switch {
	case ctx.Err() != nil:
		return SessionSummary{}, newError(http.StatusServiceUnavailable, "import cancelled; no task was created")
	case errors.Is(err, agentapi.ErrConversationNotFound):
		return SessionSummary{}, newError(http.StatusNotFound, "provider conversation %s no longer exists", convID)
	case err != nil && isHistoryBusy(err):
		return SessionSummary{}, err
	case err != nil:
		return SessionSummary{}, newError(http.StatusBadGateway, "could not read the conversation: %s", shortError(err))
	}

	m.mu.Lock()
	model, effort, size := "", "", "default"
	if h.Model != "" && m.validateSelectionLocked(name, h.Model, "", "default") == nil {
		model = h.Model
	} else if d := p.Defaults; d.Provider == name && m.validateSelectionLocked(name, d.Model, d.Effort, cmp.Or(d.ContextSize, "default")) == nil {
		model, effort, size = d.Model, d.Effort, cmp.Or(d.ContextSize, "default")
	}
	m.mu.Unlock()
	mode := store.ModeSafe
	if p.Defaults.Mode == string(store.ModeYolo) {
		mode = store.ModeYolo
	}
	id, err := newUUID()
	if err != nil {
		return SessionSummary{}, fmt.Errorf("generate session id: %w", err)
	}
	now := m.now()
	s := newSession(id, name, "", p.Dir, convID, now)
	s.projectID, s.model, s.effort, s.contextSize, s.mode = p.ID, model, effort, size, mode
	s.imported = true
	s.title = cleanTitle(found.Title)
	// The conversation is not open: viewing reads it, and a prompt opens it.
	s.base = StateClosed
	if term, ok := m.terminalRecord(name, convID); ok {
		s.terminalID, s.terminalHost, s.terminalName = term.ID, term.SessionName, term.Name
	}
	for i := range h.Items {
		if h.Items[i].Kind == agentapi.ItemTool {
			m.keepImages(s, &h.Items[i])
		}
	}
	m.mu.Lock()
	m.installHistoryLocked(s, h) // not listed yet: nobody is told
	s.historyUsed = now
	m.mu.Unlock()
	rec := store.SessionRecord{
		ID: id, Agent: name, Mode: mode, Workdir: p.Dir, CreatedAt: now, LastSeenAt: now, Status: store.StatusActive, Surface: store.SurfaceWeb,
		ProviderSessionID: convID, Web: &store.WebState{Turn: StateClosed, UpdatedAt: now, ProjectID: p.ID, Model: model, Effort: effort,
			ContextSize: size, Title: s.title, TerminalSession: s.terminalID, Imported: true},
	}
	unlinked := func(cfg *store.Config) error {
		if ctx.Err() != nil {
			return newError(http.StatusServiceUnavailable, "import cancelled; no task was created")
		}
		for _, other := range cfg.Sessions {
			if other.Surface == store.SurfaceWeb && other.Agent == name && other.ProviderSessionID == convID {
				return errAlreadyTask
			}
		}
		return nil
	}
	if err := m.register(s, nil, rec, unlinked); err != nil {
		removeUploads(m.taskUploadDir(s.id))
		return SessionSummary{}, err
	}
	log.Info("web task imported", "session", id, "provider", name)
	return m.Summary(id)
}

// findPrevious returns the provider among names that lists conversation id
// for dir, with its listing, or "" when none does.
func (m *Manager) findPrevious(ctx context.Context, names []string, dir, id string) (string, agentapi.PreviousConversation, error) {
	for _, name := range names {
		prov := m.providers[name]
		ctx, cancel := context.WithTimeout(ctx, controlTimeout)
		listed, err := m.listPrevious(ctx, prov, dir)
		cancel()
		if err != nil {
			return "", agentapi.PreviousConversation{}, err
		}
		if i := slices.IndexFunc(listed, func(c agentapi.PreviousConversation) bool { return c.ID == id }); i >= 0 {
			return name, listed[i], nil
		}
	}
	return "", agentapi.PreviousConversation{}, nil
}

func (m *Manager) listPrevious(ctx context.Context, prov agentapi.Provider, dir string) ([]agentapi.PreviousConversation, error) {
	listed, err := prov.(agentapi.Importer).Previous(ctx, dir)
	if err != nil {
		return nil, newError(http.StatusBadGateway, "could not list previous %s sessions: %s", prov.DisplayName(), shortError(err))
	}
	return listed, nil
}

// terminalRecord returns the terminal session record tied to conversation id
// of provider, the most recently seen when there are several. The record
// stays the terminal's.
func (m *Manager) terminalRecord(provider, id string) (store.SessionRecord, bool) {
	cfg, err := m.store.Load()
	if err != nil {
		log.Warn("load terminal sessions failed", "error", err)
		return store.SessionRecord{}, false
	}
	var best store.SessionRecord
	found := false
	for _, rec := range cfg.Sessions {
		if rec.Surface != "" || rec.ID == "" || rec.Agent != provider || rec.ProviderSessionID != id {
			continue
		}
		if !found || rec.LastSeenAt.After(best.LastSeenAt) {
			best, found = rec, true
		}
	}
	return best, found
}

func isHistoryBusy(err error) bool {
	code, _ := errorStatus(err)
	return code == http.StatusServiceUnavailable
}
