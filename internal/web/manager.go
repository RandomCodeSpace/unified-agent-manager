package web

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// Provider call bounds. Each derives from the service context, never from an
// HTTP request, so a browser leaving never aborts provider work.
const (
	checkTimeout   = 15 * time.Second
	openTimeout    = 2 * time.Minute
	sendTimeout    = 2 * time.Minute
	controlTimeout = 30 * time.Second
	// viewWait bounds how long a page load waits for a lazy open before it
	// shows the session as starting; the open itself continues.
	viewWait = 15 * time.Second
)

const (
	maxPromptBytes  = 256 << 10
	maxNameRunes    = 120
	maxDetailRunes  = 300
	maxSubmissions  = 64
	maxQueue        = 20
	recentWorkdirsN = 10
	// modelsMaxAge is how long a model catalog is used before /api/meta
	// reloads it; entitlements can change while the service runs.
	modelsMaxAge = 5 * time.Minute
)

// Manager owns every web session, its provider conversation, and the event
// fan-out to browsers.
//
// Two locks per session keep provider callbacks non-blocking: session.op
// serializes operations that call the provider (open, send, close) and may be
// held across those calls, while Manager.mu guards all observable state and is
// only ever held for short, allocation-bounded sections. Provider events take
// only Manager.mu, so a slow provider call or an absent browser never delays
// event processing.
type Manager struct {
	store     *store.Store
	providers map[string]agentapi.Provider
	order     []string

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// persistMu orders sessions.json writes: each write snapshots state after
	// taking it, so a later write never carries older state.
	persistMu sync.Mutex
	wake      chan struct{}
	// projectMu serializes Project changes, Task creation's final write and
	// Project removal, each of which checks and then writes the store. It is
	// taken before any session.op and never while holding mu.
	projectMu sync.Mutex

	mu       sync.Mutex
	infos    map[string]ProviderInfo
	modelsAt map[string]time.Time
	fetching map[string]bool
	projects map[string]*Project
	sessions map[string]*webSession
	dirty    map[string]struct{}
	creating map[string]chan struct{}
	seq      uint64
	subs     map[*Subscriber]struct{}
	closed   bool
	now      func() time.Time
}

// NewManager builds a manager for providers. Start must run before use.
func NewManager(st *store.Store, providers []agentapi.Provider) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		store:     st,
		providers: map[string]agentapi.Provider{},
		ctx:       ctx,
		cancel:    cancel,
		wake:      make(chan struct{}, 1),
		infos:     map[string]ProviderInfo{},
		modelsAt:  map[string]time.Time{},
		fetching:  map[string]bool{},
		projects:  map[string]*Project{},
		sessions:  map[string]*webSession{},
		dirty:     map[string]struct{}{},
		creating:  map[string]chan struct{}{},
		subs:      map[*Subscriber]struct{}{},
		now:       time.Now,
	}
	for _, p := range providers {
		if p == nil {
			continue
		}
		if _, dup := m.providers[p.Name()]; dup {
			continue
		}
		m.providers[p.Name()] = p
		m.order = append(m.order, p.Name())
	}
	return m
}

type webSession struct {
	// op serializes provider-facing operations on this session. It is never
	// taken while holding Manager.mu.
	op sync.Mutex

	// Everything below is guarded by Manager.mu.
	id          string
	projectID   string
	provider    string
	model       string
	effort      string
	contextSize string
	context     *agentapi.Context
	name        string
	title       string
	lastModel   string
	// mode is safe or yolo: a yolo Task's permission requests are allowed
	// once without asking.
	mode      store.Mode
	workdir   string
	convID    string
	createdAt time.Time
	updatedAt time.Time
	base      string
	detail    string
	// removed is set once the session was deleted; operations waiting on op
	// must not act on it.
	removed bool
	// stage is StageActive, StageSettled or StageArchived.
	stage                 string
	settledAt, archivedAt time.Time
	// turnSeq increments whenever base changes, so a failed Send only
	// restores the previous state when nothing else changed it meanwhile.
	turnSeq uint64

	conv agentapi.Conversation
	// gen identifies the current conversation; events from an older one are
	// ignored.
	gen     uint64
	opening chan struct{}

	items     []agentapi.Item
	itemIdx   map[string]int
	itemBytes int
	truncated bool

	interactions []*interaction
	ixIdx        map[string]*interaction

	subagents []*agentapi.Subagent
	subIdx    map[string]*agentapi.Subagent

	submissions []Submission
	last        *Submission
	createReq   string

	// queue holds prompts waiting for the running turn, oldest first. It
	// lives in memory only: stopping the service drops it.
	queue []QueuedPrompt
	// queueSending is the head removed for a send whose outcome is pending.
	queueSending string
	// queuePaused keeps the queue from draining after a turn that did not
	// complete or a prompt that was not accepted, until the user resumes or
	// clears it. It is never set with an empty queue.
	queuePaused bool
	// queueChanged tells changedLocked to publish the queue.
	queueChanged bool

	persisted persistKey
}

type interaction struct {
	agentapi.Interaction
	// answering is set while this service's answer is with the provider, so
	// a concurrent second answer is refused instead of racing it.
	answering bool
	// yolo is set once yolo mode claimed the interaction; it no longer waits
	// for the user.
	yolo bool
}

// persistKey is the durable part of a session; sessions.json is written only
// when it changes, never per streamed token.
type persistKey struct {
	turn, detail, name, convID, reqID, reqStatus, projectID, model, effort, contextSize, title, stage string
	mode                                                                                              store.Mode
	settledAt, archivedAt                                                                             time.Time
}

func newSession(id, provider, name, workdir, convID string, created time.Time) *webSession {
	return &webSession{
		id: id, provider: provider, name: name, workdir: workdir, convID: convID,
		createdAt: created, updatedAt: created, base: StateIdle, mode: store.ModeSafe,
		itemIdx: map[string]int{}, ixIdx: map[string]*interaction{}, subIdx: map[string]*agentapi.Subagent{},
	}
}

func (s *webSession) setBase(state, detail string) {
	s.base = state
	s.detail = detail
	s.turnSeq++
}

func (s *webSession) pendingKinds() (permissions, questions int) {
	for _, ix := range s.interactions {
		if ix.State != agentapi.InteractionPending || ix.yolo {
			continue
		}
		if ix.Kind == agentapi.InteractionPermission {
			permissions++
		} else {
			questions++
		}
	}
	return permissions, questions
}

// state derives the reported state: opening wins, then pending permission,
// then pending question, then the turn/lifecycle state.
func (s *webSession) state() string {
	if s.opening != nil {
		return StateStarting
	}
	permissions, questions := s.pendingKinds()
	switch {
	case permissions > 0:
		return StateAwaitingPermission
	case questions > 0:
		return StateAwaitingAnswer
	default:
		return s.base
	}
}

func (s *webSession) durableState() string {
	state := s.state()
	if state == StateStarting {
		return s.base
	}
	return state
}

func (s *webSession) key() persistKey {
	k := persistKey{turn: s.durableState(), detail: s.detail, name: s.name, convID: s.convID, projectID: s.projectID, model: s.model, effort: s.effort, contextSize: s.contextSize, title: s.title, mode: s.mode,
		stage: s.stage, settledAt: s.settledAt, archivedAt: s.archivedAt}
	if s.last != nil {
		k.reqID, k.reqStatus = s.last.RequestID, s.last.Status
	}
	return k
}

func busy(state string) bool {
	switch state {
	case StateWorking, StateAwaitingPermission, StateAwaitingAnswer, StateStarting:
		return true
	}
	return false
}

var knownStates = map[string]bool{
	StateIdle: true, StateStarting: true, StateWorking: true, StateAwaitingPermission: true,
	StateAwaitingAnswer: true, StateCompleted: true, StateCancelled: true, StateFailed: true,
	StateInterrupted: true, StateClosed: true,
}

const interruptedDetail = "the uam web service stopped while this turn was running"

// Start checks providers, loads their model catalogs and the web records,
// and assigns records from before Projects existed to a Project. A provider
// whose check fails is listed as unavailable; it is not fatal.
func (m *Manager) Start(ctx context.Context) error {
	infos := m.checkProviders(ctx)
	cfg, err := m.store.Load()
	if err != nil {
		return fmt.Errorf("load web sessions: %w", err)
	}
	if needsProject(cfg) {
		now := m.now()
		err := m.store.Update(func(c *store.Config) error {
			if err := assignProjects(c, now); err != nil {
				return err
			}
			cfg = *c
			return nil
		})
		if err != nil {
			// Keep serving: the assignment holds for this run. A record that
			// later gets its project_id written without the Project is
			// assigned again on the next start.
			log.Warn("assign web sessions to projects failed", "error", err)
			if err := assignProjects(&cfg, now); err != nil {
				return fmt.Errorf("assign web sessions to projects: %w", err)
			}
		}
	}
	m.mu.Lock()
	m.infos = infos
	for name, info := range infos {
		if info.Available {
			m.modelsAt[name] = m.now()
		}
	}
	for id, p := range cfg.WebProjects {
		m.projects[id] = &Project{ID: p.ID, Name: loadedName(p.Name, p.Dir), Dir: p.Dir, CreatedAt: p.CreatedAt}
	}
	for _, rec := range cfg.Sessions {
		if rec.Surface != store.SurfaceWeb || rec.ID == "" {
			continue
		}
		s := sessionFromRecord(rec)
		// A turn cannot survive the service that drove it: report it as
		// interrupted, never resume or replay it.
		if busy(s.base) {
			s.setBase(StateInterrupted, interruptedDetail)
			s.updatedAt = m.now()
			m.dirty[s.id] = struct{}{}
		}
		s.persisted = s.key()
		m.sessions[s.id] = s
	}
	m.mu.Unlock()
	m.wg.Add(1)
	go m.persistLoop()
	if err := m.flush(); err != nil {
		log.Warn("persist interrupted web sessions failed", "error", err)
	}
	return nil
}

// lacksProject reports whether a web record has no stored Project: no
// project_id, or one naming a Project that is not in web_projects (written
// by a run whose migration could not be saved, or dropped as invalid).
func lacksProject(cfg *store.Config, rec store.SessionRecord) bool {
	if rec.Surface != store.SurfaceWeb || rec.ID == "" || rec.Workdir == "" {
		return false
	}
	if rec.Web == nil || rec.Web.ProjectID == "" {
		return true
	}
	_, ok := cfg.WebProjects[rec.Web.ProjectID]
	return !ok
}

// needsProject reports whether a web record still lacks a Project.
func needsProject(cfg store.Config) bool {
	for _, rec := range cfg.Sessions {
		if lacksProject(&cfg, rec) {
			return true
		}
	}
	return false
}

// assignProjects gives every web record that lacks a Project the Project for
// its workdir, creating one named after the directory when needed. Only such
// records are touched, so running it again changes nothing. A removed
// Project does not come back: removing it deleted its Tasks in the same
// update. The directory need not exist any more.
func assignProjects(cfg *store.Config, now time.Time) error {
	byDir := map[string]string{}
	for id, p := range cfg.WebProjects {
		byDir[p.Dir] = id
	}
	for key, rec := range cfg.Sessions {
		if !lacksProject(cfg, rec) {
			continue
		}
		id, ok := byDir[rec.Workdir]
		if !ok {
			var err error
			if id, err = newUUID(); err != nil {
				return fmt.Errorf("generate project id: %w", err)
			}
			if cfg.WebProjects == nil {
				cfg.WebProjects = map[string]store.WebProject{}
			}
			cfg.WebProjects[id] = store.WebProject{ID: id, Name: loadedName("", rec.Workdir), Dir: rec.Workdir, CreatedAt: now}
			byDir[rec.Workdir] = id
		}
		web := store.WebState{}
		if rec.Web != nil {
			web = *rec.Web
		}
		web.ProjectID = id
		rec.Web = &web
		cfg.Sessions[key] = rec
	}
	return nil
}

// loadedName is a stored Project name made safe to show, or the directory's
// base name when it has none.
func loadedName(name, dir string) string {
	if clean, err := cleanName(name, filepath.Base(dir)); err == nil {
		return clean
	}
	return clipRunes(displaytext.Sanitize(dir), maxNameRunes)
}

// cleanTitle is a provider title made safe and short enough to show.
func cleanTitle(title string) string {
	return clipRunes(strings.TrimSpace(displaytext.Sanitize(title)), maxNameRunes)
}

func sessionFromRecord(rec store.SessionRecord) *webSession {
	s := newSession(rec.ID, rec.Agent, rec.Name, rec.Workdir, rec.ProviderSessionID, rec.CreatedAt)
	s.updatedAt = rec.LastSeenAt
	if rec.Mode == store.ModeYolo {
		s.mode = store.ModeYolo
	}
	if web := rec.Web; web != nil {
		if knownStates[web.Turn] {
			s.base = web.Turn
		}
		s.detail = web.Detail
		s.projectID, s.model, s.title = web.ProjectID, web.Model, cleanTitle(web.Title)
		s.effort, s.contextSize = web.Effort, cmp.Or(web.ContextSize, "default")
		// An unknown stage loads as active, as an unknown turn state is ignored.
		if web.Stage == StageSettled || web.Stage == StageArchived {
			s.stage, s.settledAt, s.archivedAt = web.Stage, web.SettledAt, web.ArchivedAt
		}
		if !web.UpdatedAt.IsZero() {
			s.updatedAt = web.UpdatedAt
		}
		if web.RequestID != "" {
			sub := Submission{RequestID: web.RequestID, Status: web.RequestStatus, Time: web.UpdatedAt}
			s.submissions = []Submission{sub}
			s.last = &sub
		}
	}
	if s.updatedAt.IsZero() {
		s.updatedAt = s.createdAt
	}
	return s
}

func (m *Manager) checkProviders(ctx context.Context) map[string]ProviderInfo {
	infos := make(map[string]ProviderInfo, len(m.order))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, name := range m.order {
		p := m.providers[name]
		wg.Add(1)
		go func() {
			defer wg.Done()
			info := ProviderInfo{Name: p.Name(), DisplayName: p.DisplayName(), Capabilities: p.Capabilities(), Available: true, Models: []agentapi.Model{}}
			checkCtx, cancel := context.WithTimeout(ctx, checkTimeout)
			err := p.Check(checkCtx)
			cancel()
			if err != nil {
				info.Available = false
				info.Reason = shortError(err)
				log.Warn("web provider unavailable", "provider", name, "error", err)
			} else if models, err := loadModels(ctx, p); err != nil {
				log.Warn("load web provider models failed", "provider", name, "error", err)
			} else {
				info.Models = models
			}
			mu.Lock()
			infos[name] = info
			mu.Unlock()
		}()
	}
	wg.Wait()
	return infos
}

// loadModels reads p's selectable models. ErrUnsupported means only the
// provider default is offered.
func loadModels(ctx context.Context, p agentapi.Provider) ([]agentapi.Model, error) {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	models, err := p.Models(ctx)
	if errors.Is(err, agentapi.ErrUnsupported) {
		return []agentapi.Model{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]agentapi.Model, 0, len(models))
	seen := map[string]bool{}
	for _, mo := range models {
		if mo.ID == "" || seen[mo.ID] {
			continue
		}
		seen[mo.ID] = true
		name := clipRunes(strings.TrimSpace(displaytext.Sanitize(mo.Name)), maxNameRunes)
		mo.Name = cmp.Or(name, mo.ID)
		mo.Efforts = append([]string{}, mo.Efforts...)
		mo.ContextSizes = append([]agentapi.ContextSize{}, mo.ContextSizes...)
		out = append(out, mo)
	}
	return out, nil
}

// RefreshModels reloads the catalog of every available provider whose copy
// is older than modelsMaxAge. A failed load keeps the previous catalog and is
// retried after modelsMaxAge.
func (m *Manager) RefreshModels() {
	m.mu.Lock()
	var stale []agentapi.Provider
	for _, name := range m.order {
		if m.infos[name].Available && !m.fetching[name] && m.now().Sub(m.modelsAt[name]) >= modelsMaxAge {
			m.fetching[name] = true
			stale = append(stale, m.providers[name])
		}
	}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, p := range stale {
		wg.Add(1)
		go func() {
			defer wg.Done()
			models, err := loadModels(m.ctx, p)
			m.mu.Lock()
			defer m.mu.Unlock()
			delete(m.fetching, p.Name())
			m.modelsAt[p.Name()] = m.now()
			if err != nil {
				log.Warn("refresh web provider models failed", "provider", p.Name(), "error", err)
				return
			}
			info := m.infos[p.Name()]
			info.Models = models
			m.infos[p.Name()] = info
		}()
	}
	wg.Wait()
}

// Providers lists every provider with its availability.
func (m *Manager) Providers() []ProviderInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ProviderInfo, 0, len(m.order))
	for _, name := range m.order {
		out = append(out, m.infos[name])
	}
	return out
}

// RecentWorkdirs returns distinct workdirs of stored records of any surface,
// most recently seen first.
func (m *Manager) RecentWorkdirs() []string {
	out := []string{}
	cfg, err := m.store.Load()
	if err != nil {
		log.Warn("load recent workdirs failed", "error", err)
		return out
	}
	recs := make([]store.SessionRecord, 0, len(cfg.Sessions))
	for _, rec := range cfg.Sessions {
		if rec.Workdir != "" {
			recs = append(recs, rec)
		}
	}
	seen := func(rec store.SessionRecord) time.Time {
		if rec.LastSeenAt.IsZero() {
			return rec.CreatedAt
		}
		return rec.LastSeenAt
	}
	sort.Slice(recs, func(i, j int) bool {
		if a, b := seen(recs[i]), seen(recs[j]); !a.Equal(b) {
			return a.After(b)
		}
		return recs[i].Workdir < recs[j].Workdir
	})
	distinct := map[string]bool{}
	for _, rec := range recs {
		if distinct[rec.Workdir] {
			continue
		}
		distinct[rec.Workdir] = true
		out = append(out, rec.Workdir)
		if len(out) == recentWorkdirsN {
			break
		}
	}
	return out
}

func (m *Manager) lookup(id string) (*webSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return nil, newError(http.StatusNotFound, "session not found")
	}
	return s, nil
}

func (m *Manager) summaryLocked(s *webSession) SessionSummary {
	permissions, questions := s.pendingKinds()
	return SessionSummary{
		ID: s.id, ProjectID: s.projectID, Provider: s.provider, Model: s.model, Name: s.name, Title: s.title,
		Effort: s.effort, ContextSize: cmp.Or(s.contextSize, "default"), Context: s.context,
		LastModel: s.lastModel, SubagentsRunning: s.runningSubagents(), Workdir: s.workdir, ConversationID: s.convID,
		State: s.state(), StateDetail: s.detail, Open: s.conv != nil, Pending: permissions + questions,
		CreatedAt: s.createdAt, UpdatedAt: s.updatedAt, Capabilities: m.infos[s.provider].Capabilities, Queued: len(s.queue),
		Mode: string(s.mode), Stage: s.stage, SettledAt: s.settledAt, ArchivedAt: s.archivedAt,
	}
}

func (m *Manager) detailLocked(s *webSession) SessionDetail {
	d := SessionDetail{
		SessionSummary:   m.summaryLocked(s),
		Items:            s.agentItems(""),
		Interactions:     make([]agentapi.Interaction, 0, len(s.interactions)),
		Subagents:        make([]agentapi.Subagent, 0, len(s.subagents)),
		HistoryTruncated: s.truncated,
		Queue:            s.queueSnapshot(),
		QueuePaused:      s.queuePaused,
	}
	for _, ix := range s.interactions {
		d.Interactions = append(d.Interactions, ix.Interaction)
	}
	for _, sa := range s.subagents {
		d.Subagents = append(d.Subagents, *sa)
	}
	if s.last != nil {
		last := *s.last
		d.LastSubmission = &last
	}
	return d
}

func (m *Manager) summariesLocked() []SessionSummary {
	out := make([]SessionSummary, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, m.summaryLocked(s))
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// List returns every web session, newest first.
func (m *Manager) List() []SessionSummary {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.summariesLocked()
}

// Summary returns one session's summary.
func (m *Manager) Summary(id string) (SessionSummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return SessionSummary{}, newError(http.StatusNotFound, "session not found")
	}
	return m.summaryLocked(s), nil
}

// Detail returns one session with its retained transcript.
func (m *Manager) Detail(id string) (SessionDetail, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return SessionDetail{}, newError(http.StatusNotFound, "session not found")
	}
	return m.detailLocked(s), nil
}

// Subagent returns one subagent of a session with its retained transcript.
func (m *Manager) Subagent(id, agentID string) (SubagentDetail, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return SubagentDetail{}, newError(http.StatusNotFound, "session not found")
	}
	sa := s.subIdx[agentID]
	if sa == nil {
		return SubagentDetail{}, newError(http.StatusNotFound, "subagent not found")
	}
	return SubagentDetail{Subagent: *sa, Items: s.agentItems(agentID)}, nil
}

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
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.projectsLocked()
}

func (m *Manager) publishProjectLocked(p Project) {
	m.broadcastLocked("project", "", func(seq uint64) any { return projectEvent{Seq: seq, Project: p} })
}

// AddProject adds the directory dir as a Project. A directory has at most
// one Project; adding it again reports the existing one with 409.
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
	p := Project{ID: id, Name: clean, Dir: canonical, CreatedAt: m.now()}
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
		cfg.WebProjects[id] = store.WebProject{ID: id, Name: clean, Dir: canonical, CreatedAt: p.CreatedAt}
		return nil
	}); err != nil {
		if existing != "" {
			return Project{}, err
		}
		return Project{}, fmt.Errorf("save web project: %w", err)
	}
	m.mu.Lock()
	m.projects[id] = &p
	m.publishProjectLocked(p)
	m.mu.Unlock()
	log.Info("web project added", "project", id)
	return p, nil
}

func projectExists(id string) *Error {
	return &Error{Status: http.StatusConflict, Message: "this directory already has a project", ProjectID: id}
}

// RenameProject renames a Project. An empty name resets it to the
// directory's base name.
func (m *Manager) RenameProject(id, name string) (Project, error) {
	m.projectMu.Lock()
	defer m.projectMu.Unlock()
	m.mu.Lock()
	p := m.projects[id]
	var dir string
	if p != nil {
		dir = p.Dir
	}
	m.mu.Unlock()
	if p == nil {
		return Project{}, errProjectNotFound
	}
	clean, err := cleanName(name, filepath.Base(dir))
	if err != nil {
		return Project{}, err
	}
	if err := m.store.Update(func(cfg *store.Config) error {
		stored, ok := cfg.WebProjects[id]
		if !ok {
			return errProjectNotFound
		}
		stored.Name = clean
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
	p.Name = clean
	m.publishProjectLocked(*p)
	return *p, nil
}

var errProjectNotFound = newError(http.StatusNotFound, "project not found")

// RemoveProject deletes a Project and its Task records. It is refused unless
// every Task in it is archived. Conversations are never deleted at the
// provider, and the directory is not touched.
func (m *Manager) RemoveProject(id string) error {
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
		return newError(http.StatusConflict, "archive every task in this project first")
	}
	if err := m.store.Update(func(cfg *store.Config) error {
		delete(cfg.WebProjects, id)
		for key, rec := range cfg.Sessions {
			if rec.Surface == store.SurfaceWeb && rec.Web != nil && rec.Web.ProjectID == id {
				delete(cfg.Sessions, key)
			}
		}
		return nil
	}); err != nil {
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
	m.broadcastLocked("project_removed", "", func(seq uint64) any { return projectRemovedEvent{Seq: seq, ProjectID: id} })
	m.mu.Unlock()
	for _, conv := range convs {
		m.closeConversation(conv)
	}
	log.Info("web project removed", "project", id, "tasks", len(tasks))
	return nil
}

// Delete deletes an archived Task's record. It is refused for any other
// stage. The conversation is never deleted at the provider.
func (m *Manager) Delete(id string) error {
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
		return newError(http.StatusNotFound, "session not found")
	case closed:
		return errShuttingDown
	case !archived:
		return newError(http.StatusConflict, "only an archived task can be deleted; archive it first")
	}
	if err := m.store.Update(func(cfg *store.Config) error {
		key := store.Key(s.provider, s.id)
		if rec, ok := cfg.Sessions[key]; ok && rec.ID == s.id && rec.Surface == store.SurfaceWeb {
			delete(cfg.Sessions, key)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("delete web session: %w", err)
	}
	m.mu.Lock()
	conv := m.forgetLocked(s)
	m.mu.Unlock()
	if conv != nil {
		m.closeConversation(conv)
	}
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
	delete(m.sessions, s.id)
	delete(m.dirty, s.id)
	m.broadcastLocked("session_removed", "", func(seq uint64) any { return sessionRemovedEvent{Seq: seq, SessionID: s.id} })
	return conv
}

// changedLocked publishes s's queue when it changed and its summary when it
// changed since before, and schedules a sessions.json write when its durable
// part or its queue changed.
func (m *Manager) changedLocked(s *webSession, before SessionSummary) {
	after := m.summaryLocked(s)
	after.UpdatedAt = before.UpdatedAt
	key := s.key()
	durable := key != s.persisted
	queue := s.queueChanged
	s.queueChanged = false
	if after == before && !durable && !queue {
		return
	}
	// updated_at is Task activity, which is what the durable part records:
	// turn state (including waiting for input), detail, name, title, model
	// and the last submission. Queue changes are activity too. A viewer
	// opening the conversation (open, starting) and provider capability or
	// catalog changes are not.
	if durable || queue {
		s.updatedAt = m.now()
	}
	if m.sessions[s.id] != s {
		// Not yet (or no longer) listed; Create publishes it once registered.
		return
	}
	if queue {
		m.broadcastLocked("queue", s.id, func(seq uint64) any {
			return queueEvent{Seq: seq, SessionID: s.id, Queue: s.queueSnapshot(), Paused: s.queuePaused}
		})
	}
	summary := m.summaryLocked(s)
	m.broadcastLocked("session", "", func(seq uint64) any { return sessionEvent{Seq: seq, Session: summary} })
	if durable || queue {
		if durable {
			s.persisted = key
		}
		m.dirty[s.id] = struct{}{}
		select {
		case m.wake <- struct{}{}:
		default:
		}
	}
}

func (m *Manager) persistLoop() {
	defer m.wg.Done()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.wake:
			if err := m.flush(); err != nil {
				log.Warn("persist web sessions failed", "error", err)
			}
		}
	}
}

type recordPatch struct {
	id, provider, name, convID string
	mode                       store.Mode
	updated                    time.Time
	web                        store.WebState
}

// flush writes the durable state of every dirty session.
func (m *Manager) flush() error {
	m.persistMu.Lock()
	defer m.persistMu.Unlock()
	m.mu.Lock()
	patches := make([]recordPatch, 0, len(m.dirty))
	for id := range m.dirty {
		s := m.sessions[id]
		if s == nil {
			continue
		}
		key := s.key()
		patches = append(patches, recordPatch{
			id: s.id, provider: s.provider, name: s.name, convID: s.convID, mode: s.mode, updated: s.updatedAt,
			web: store.WebState{
				Turn: key.turn, RequestID: key.reqID, RequestStatus: key.reqStatus, UpdatedAt: s.updatedAt, Detail: s.detail,
				ProjectID: key.projectID, Model: key.model, Effort: key.effort, ContextSize: key.contextSize, Title: key.title,
				Stage: key.stage, SettledAt: key.settledAt, ArchivedAt: key.archivedAt,
			},
		})
	}
	m.dirty = map[string]struct{}{}
	m.mu.Unlock()
	if len(patches) == 0 {
		return nil
	}
	err := m.store.Update(func(cfg *store.Config) error {
		for _, p := range patches {
			key := store.Key(p.provider, p.id)
			rec, ok := cfg.Sessions[key]
			// Never recreate a record removed behind the service's back, and
			// never touch a record this surface does not own.
			if !ok || rec.ID != p.id || rec.Surface != store.SurfaceWeb {
				continue
			}
			rec.Name = p.name
			rec.Mode = p.mode
			rec.ProviderSessionID = p.convID
			rec.LastSeenAt = p.updated
			// Update in place: fields a newer uam wrote must survive.
			web := store.WebState{}
			if rec.Web != nil {
				web = *rec.Web
			}
			web.Update(p.web)
			rec.Web = &web
			cfg.Sessions[key] = rec
		}
		return nil
	})
	if err != nil {
		// Retry with the next change rather than spinning on a store that
		// refuses writes (for example a read-only newer schema).
		m.mu.Lock()
		for _, p := range patches {
			m.dirty[p.id] = struct{}{}
		}
		m.mu.Unlock()
		return err
	}
	return nil
}

// sink routes one conversation's events to its session. gen pins it to that
// conversation so a closed or replaced one cannot change the session.
type sink struct {
	m   *Manager
	s   *webSession
	gen uint64
}

func (k sink) Emit(ev agentapi.Event) { k.m.handleEvent(k.s, k.gen, ev) }

// handleEvent applies one provider event. It only takes Manager.mu and never
// waits on browsers: subscriber queues are fed without blocking.
func (m *Manager) handleEvent(s *webSession, gen uint64, ev agentapi.Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.gen != gen {
		return
	}
	before := m.summaryLocked(s)
	switch ev.Kind {
	case agentapi.EventItem:
		if ev.Item != nil && ev.Item.ID != "" {
			m.upsertItemLocked(s, clampItem(*ev.Item, m.now()), true)
		}
	case agentapi.EventDelta:
		if ev.Delta != nil && ev.Delta.ItemID != "" {
			m.applyDeltaLocked(s, *ev.Delta)
		}
	case agentapi.EventTurn:
		if ev.Turn != nil {
			m.applyTurnLocked(s, *ev.Turn)
		}
	case agentapi.EventInteraction:
		if ev.Interaction != nil && ev.Interaction.ID != "" {
			m.upsertInteractionLocked(s, *ev.Interaction)
		}
	case agentapi.EventSubagent:
		if ev.Subagent != nil && ev.Subagent.ID != "" {
			m.upsertSubagentLocked(s, *ev.Subagent)
		}
	case agentapi.EventContext:
		if ev.Context != nil && ev.Context.Used >= 0 && ev.Context.Limit > 0 {
			if s.context == nil || *s.context != *ev.Context {
				usage := *ev.Context
				s.context = &usage
			}
		}
	case agentapi.EventTitle:
		if title := cleanTitle(ev.Title); title != "" {
			s.title = title
		}
	case agentapi.EventExit:
		// The conversation is gone. Report it; never replay a prompt or open
		// a replacement on the user's behalf.
		detail := "the provider runtime exited"
		if ev.Error != "" {
			detail = "the provider runtime exited: " + clipRunes(displaytext.Sanitize(ev.Error), maxDetailRunes)
		}
		s.conv = nil
		s.gen++
		m.expirePendingLocked(s, "the provider runtime exited")
		m.endSubagentsLocked(s)
		m.pauseQueueLocked(s)
		s.setBase(StateFailed, detail)
		log.Warn("web provider conversation exited", "session", s.id, "provider", s.provider)
	}
	m.changedLocked(s, before)
}

func (m *Manager) applyTurnLocked(s *webSession, turn agentapi.Turn) {
	if model := clipRunes(strings.TrimSpace(displaytext.Sanitize(turn.Model)), maxNameRunes); model != "" {
		s.lastModel = model
	}
	switch turn.State {
	case agentapi.TurnWorking:
		s.setBase(StateWorking, "")
	case agentapi.TurnCompleted:
		s.setBase(StateCompleted, "")
		// The whole turn is over, steers included: the queue may go on.
		m.kickDrainLocked(s)
	case agentapi.TurnCancelled:
		s.setBase(StateCancelled, "")
		m.pauseQueueLocked(s)
	case agentapi.TurnFailed:
		detail := "the provider reported that the turn failed"
		if turn.Error != "" {
			detail = clipRunes(displaytext.Sanitize(turn.Error), maxDetailRunes)
		}
		s.setBase(StateFailed, detail)
		m.pauseQueueLocked(s)
	}
}

// CreateRequest is the POST /api/sessions body. Name may be empty; the
// provider's title is shown until the user names the Task.
type CreateRequest struct {
	ProjectID   string `json:"project_id"`
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	Effort      string `json:"effort"`
	ContextSize string `json:"context_size"`
	Name        string `json:"name"`
	Prompt      string `json:"prompt"`
	RequestID   string `json:"request_id"`
	// Mode is safe (also when empty) or yolo.
	Mode string `json:"mode"`
}

// Create opens a new provider conversation in a Project's directory, records
// the session (a Task), and, when a prompt is given, submits it through the
// same path as Submit.
func (m *Manager) Create(req CreateRequest) (SessionSummary, error) {
	prov, err := m.availableProvider(req.Provider)
	if err != nil {
		return SessionSummary{}, err
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
		return SessionSummary{}, newError(http.StatusBadRequest, "unknown project_id %q", req.ProjectID)
	}
	if selectionErr != nil {
		return SessionSummary{}, selectionErr
	}
	name, err := cleanTaskName(req.Name)
	if err != nil {
		return SessionSummary{}, err
	}
	mode := store.ModeSafe
	if req.Mode != "" {
		if mode, err = parseMode(req.Mode); err != nil {
			return SessionSummary{}, err
		}
	}
	if info, err := os.Stat(workdir); err != nil || !info.IsDir() {
		return SessionSummary{}, newError(http.StatusConflict, "the project directory %s no longer exists", workdir)
	}
	hasPrompt := strings.TrimSpace(req.Prompt) != ""
	if hasPrompt && len(req.Prompt) > maxPromptBytes {
		return SessionSummary{}, newError(http.StatusRequestEntityTooLarge, "prompt is too large")
	}
	reqID := req.RequestID
	if reqID != "" && !validRequestID(reqID) {
		return SessionSummary{}, newError(http.StatusBadRequest, "request_id must be a UUID")
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
	id, err := newUUID()
	if err != nil {
		return SessionSummary{}, fmt.Errorf("generate session id: %w", err)
	}
	now := m.now()
	s := newSession(id, prov.Name(), name, workdir, "", now)
	s.projectID, s.model, s.mode = project.ID, req.Model, mode
	s.effort, s.contextSize = req.Effort, req.ContextSize
	s.createReq = reqID
	s.gen = 1
	ctx, cancel := context.WithTimeout(m.ctx, openTimeout)
	conv, err := prov.Open(ctx, agentapi.OpenRequest{SessionID: id, Workdir: workdir, Title: name, Model: req.Model, Effort: req.Effort, ContextSize: req.ContextSize, Events: sink{m: m, s: s, gen: 1}})
	cancel()
	if err != nil {
		log.Warn("open web conversation failed", "provider", prov.Name(), "error", err)
		return SessionSummary{}, newError(http.StatusBadGateway, "could not start a %s conversation: %s", prov.DisplayName(), shortError(err))
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
		ProviderSessionID: convID, Web: &store.WebState{Turn: StateIdle, UpdatedAt: now, ProjectID: project.ID, Model: req.Model, Effort: req.Effort, ContextSize: req.ContextSize},
	}
	if err := m.register(s, conv, rec); err != nil {
		m.closeConversation(conv)
		return SessionSummary{}, err
	}
	log.Info("web session created", "session", id, "provider", prov.Name())
	if hasPrompt {
		if _, err := m.submit(s, req.Prompt, reqID, ModeSend); err != nil {
			log.Warn("initial web prompt not submitted", "session", id, "error", err)
		}
	}
	return m.Summary(id)
}

// register writes a new session's record and lists it. The Project may have
// been removed while the conversation opened; projectMu orders this against
// RemoveProject.
func (m *Manager) register(s *webSession, conv agentapi.Conversation, rec store.SessionRecord) error {
	m.projectMu.Lock()
	defer m.projectMu.Unlock()
	m.mu.Lock()
	projectGone := m.projects[s.projectID] == nil
	m.mu.Unlock()
	if projectGone {
		return newError(http.StatusConflict, "the project was removed")
	}
	if err := m.store.Update(func(cfg *store.Config) error {
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
	s.persisted = persistKey{turn: StateIdle, name: rec.Name, convID: rec.ProviderSessionID, projectID: s.projectID, model: s.model, effort: s.effort, contextSize: s.contextSize, mode: s.mode}
	m.sessions[s.id] = s
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

func (m *Manager) availableProvider(name string) (agentapi.Provider, error) {
	m.mu.Lock()
	prov := m.providers[name]
	info := m.infos[name]
	m.mu.Unlock()
	if prov == nil {
		return nil, newError(http.StatusBadRequest, "unknown provider %q", name)
	}
	if info.Available {
		return prov, nil
	}
	// Re-check so installing or signing in to a provider does not need a
	// service restart.
	ctx, cancel := context.WithTimeout(m.ctx, checkTimeout)
	err := prov.Check(ctx)
	cancel()
	m.mu.Lock()
	info.Available = err == nil
	info.Reason = ""
	if err != nil {
		info.Reason = shortError(err)
	}
	m.infos[name] = info
	m.mu.Unlock()
	if err != nil {
		return nil, newError(http.StatusConflict, "%s is unavailable: %s", prov.DisplayName(), info.Reason)
	}
	return prov, nil
}

var errShuttingDown = newError(http.StatusServiceUnavailable, "the uam web service is shutting down")

func (m *Manager) isClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

// View opens a session's conversation lazily for a viewer and waits a bounded
// time for it. The open runs on the service, not on ctx: a viewer leaving
// early does not abort it.
func (m *Manager) View(ctx context.Context, id string) error {
	s, err := m.lookup(id)
	if err != nil {
		return err
	}
	done := m.viewOpen(s)
	timer := time.NewTimer(viewWait)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	case <-ctx.Done():
	}
	return nil
}

var closedChan = func() chan struct{} { ch := make(chan struct{}); close(ch); return ch }()

func (m *Manager) viewOpen(s *webSession) <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || !m.autoOpenableLocked(s) {
		return closedChan
	}
	if s.opening != nil {
		return s.opening
	}
	before := m.summaryLocked(s)
	s.opening = make(chan struct{})
	done := s.opening
	m.changedLocked(s, before)
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		s.op.Lock()
		defer s.op.Unlock()
		_ = m.openLocked(s, false)
	}()
	return done
}

// autoOpenableLocked reports whether merely viewing s may open its
// conversation. Failed, closed, settled and archived sessions stay as they
// are until the user acts on them, so their reported outcome is not replaced
// by a page load.
func (m *Manager) autoOpenableLocked(s *webSession) bool {
	return !s.removed && s.stage == StageActive && s.conv == nil && s.convID != "" && s.base != StateFailed && s.base != StateClosed &&
		m.providers[s.provider] != nil && m.infos[s.provider].Available
}

func (m *Manager) finishOpeningLocked(s *webSession) {
	if s.opening != nil {
		close(s.opening)
		s.opening = nil
	}
}

// openLocked reopens s's exact conversation. The caller holds s.op. explicit
// is true for user actions (sending a prompt); a viewer only opens sessions
// autoOpenableLocked allows.
func (m *Manager) openLocked(s *webSession, explicit bool) error {
	m.mu.Lock()
	if s.removed {
		m.finishOpeningLocked(s)
		m.mu.Unlock()
		return newError(http.StatusNotFound, "session not found")
	}
	if s.conv != nil || m.closed || (!explicit && !m.autoOpenableLocked(s)) {
		before := m.summaryLocked(s)
		m.finishOpeningLocked(s)
		m.changedLocked(s, before)
		open, closed := s.conv != nil, m.closed
		m.mu.Unlock()
		if !open && closed {
			return errShuttingDown
		}
		return nil
	}
	before := m.summaryLocked(s)
	prov := m.providers[s.provider]
	if prov == nil || s.convID == "" {
		detail := fmt.Sprintf("provider %q is not available in this service", s.provider)
		if s.convID == "" {
			detail = "this session has no provider conversation id; uam will not create a replacement"
		}
		s.setBase(StateFailed, detail)
		m.finishOpeningLocked(s)
		m.changedLocked(s, before)
		m.mu.Unlock()
		return newError(http.StatusConflict, "%s", detail)
	}
	if s.opening == nil {
		s.opening = make(chan struct{})
	}
	s.gen++
	gen := s.gen
	req := agentapi.OpenRequest{SessionID: s.id, ConversationID: s.convID, Workdir: s.workdir, Title: s.name, Events: sink{m: m, s: s, gen: gen}}
	withHistory := m.infos[s.provider].Capabilities.History
	model, effort, contextSize := s.model, s.effort, cmp.Or(s.contextSize, "default")
	s.context = nil
	var selectionErr error
	if effort != "" || contextSize != "default" {
		selectionErr = m.validateSelectionLocked(s.provider, model, effort, contextSize)
	}
	m.changedLocked(s, before)
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(m.ctx, openTimeout)
	defer cancel()
	var conv agentapi.Conversation
	err := selectionErr
	if err == nil {
		conv, err = prov.Open(ctx, req)
	}
	if err == nil && conv.ID() != req.ConversationID {
		// Exactness is the contract: a different conversation is a failure,
		// not a substitute.
		m.closeConversation(conv)
		err = fmt.Errorf("provider opened conversation %q instead of %q", conv.ID(), req.ConversationID)
	}
	// Apply the selected model before anything is sent: it may have been
	// changed while the conversation was closed. A conversation that cannot
	// take it, ErrUnsupported included (a provider with a catalog must
	// switch), is not used with another model.
	if err == nil && model != "" {
		if setErr := conv.SetModel(ctx, model, effort, contextSize); setErr != nil {
			m.closeConversation(conv)
			err = fmt.Errorf("apply model %s: %w", model, setErr)
		}
	}
	var history agentapi.History
	if err == nil && withHistory {
		recorded, histErr := conv.History(ctx)
		if histErr != nil {
			log.Warn("read web conversation history failed", "session", s.id, "error", histErr)
		}
		history = recorded
	}

	m.mu.Lock()
	before = m.summaryLocked(s)
	stale := s.gen != gen || m.closed
	switch {
	case err == nil && !stale:
		s.conv = conv
		if s.base == StateClosed || s.base == StateFailed {
			s.setBase(StateIdle, "")
		}
		m.applyHistoryLocked(s, history)
		m.autoAllowPendingLocked(s)
	case err != nil && s.gen == gen:
		s.setBase(StateFailed, openFailureDetail(err, s.convID))
	}
	m.finishOpeningLocked(s)
	m.changedLocked(s, before)
	m.mu.Unlock()

	if err != nil {
		log.Warn("reopen web conversation failed", "session", s.id, "provider", s.provider, "error", err)
		if errors.Is(err, agentapi.ErrConversationNotFound) {
			return newError(http.StatusConflict, "%s", openFailureDetail(err, req.ConversationID))
		}
		return newError(http.StatusBadGateway, "%s", openFailureDetail(err, req.ConversationID))
	}
	if stale {
		m.closeConversation(conv)
		return newError(http.StatusConflict, "the session changed while its conversation was opening")
	}
	return nil
}

func openFailureDetail(err error, convID string) string {
	if errors.Is(err, agentapi.ErrConversationNotFound) {
		return fmt.Sprintf("provider conversation %s no longer exists; uam did not create a replacement", convID)
	}
	return "could not open the provider conversation: " + shortError(err)
}

func (m *Manager) closeConversation(conv agentapi.Conversation) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(m.ctx), controlTimeout)
	defer cancel()
	if err := conv.Close(ctx); err != nil && !errors.Is(err, agentapi.ErrClosed) {
		log.Warn("close web conversation failed", "error", err)
	}
}

var requestIDRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func validRequestID(id string) bool { return requestIDRE.MatchString(id) }

// Submit sends one prompt in mode: ModeSend ("" too), ModeQueue or
// ModeSteer. A repeated request ID returns the recorded outcome, or "queued"
// while the prompt waits in the queue, without contacting the provider.
func (m *Manager) Submit(id, text, requestID, mode string) (Submission, error) {
	if !validRequestID(requestID) {
		return Submission{}, newError(http.StatusBadRequest, "request_id must be a UUID")
	}
	switch mode {
	case "":
		mode = ModeSend
	case ModeSend, ModeQueue, ModeSteer:
	default:
		return Submission{}, newError(http.StatusBadRequest, "mode must be send, queue or steer")
	}
	if strings.TrimSpace(text) == "" {
		return Submission{}, newError(http.StatusBadRequest, "prompt text is required")
	}
	if len(text) > maxPromptBytes {
		return Submission{}, newError(http.StatusRequestEntityTooLarge, "prompt is too large")
	}
	s, err := m.lookup(id)
	if err != nil {
		return Submission{}, err
	}
	return m.submit(s, text, requestID, mode)
}

var errTurnRunning = newError(http.StatusConflict, "a turn is already running in this session")

// turnRunning reports whether state is a running turn: working or waiting for
// the user. Opening a conversation (starting) is not a turn.
func turnRunning(state string) bool {
	return state == StateWorking || state == StateAwaitingPermission || state == StateAwaitingAnswer
}

func (m *Manager) submit(s *webSession, text, reqID, mode string) (Submission, error) {
	s.op.Lock()
	defer s.op.Unlock()
	m.mu.Lock()
	if sub, ok := s.findRequest(reqID); ok {
		m.mu.Unlock()
		return sub, nil
	}
	if s.removed {
		m.mu.Unlock()
		return Submission{}, newError(http.StatusNotFound, "session not found")
	}
	if m.closed {
		m.mu.Unlock()
		return Submission{}, errShuttingDown
	}
	if err := s.readOnlyLocked(); err != nil {
		m.mu.Unlock()
		return Submission{}, err
	}
	state, conv := s.state(), s.conv
	switch {
	// Behind queued prompts that are about to be sent, a queued prompt waits
	// its turn even when no turn is running.
	case mode == ModeQueue && (turnRunning(state) || (len(s.queue) > 0 && !s.queuePaused)):
		defer m.mu.Unlock()
		return m.enqueueLocked(s, text, reqID)
	case mode == ModeSteer && turnRunning(state):
		m.mu.Unlock()
		return m.steer(s, conv, text, reqID)
	case busy(state):
		m.mu.Unlock()
		return Submission{}, errTurnRunning
	}
	m.mu.Unlock()
	return m.send(s, text, reqID)
}

// send starts a turn with text. The caller holds s.op. An error means nothing
// was sent and nothing was recorded; otherwise the outcome is recorded, and a
// prompt that was not accepted pauses the queue.
func (m *Manager) send(s *webSession, text, reqID string) (Submission, error) {
	if err := m.openLocked(s, true); err != nil {
		if errors.Is(err, errShuttingDown) {
			return Submission{}, err
		}
		_, msg := errorStatus(err)
		return m.recordSubmission(s, reqID, SubmissionRejected, msg, true), nil
	}

	m.mu.Lock()
	conv := s.conv
	if conv == nil {
		m.mu.Unlock()
		return m.recordSubmission(s, reqID, SubmissionRejected, "the provider conversation is not open", true), nil
	}
	// Reopening can surface a turn or request the provider still has.
	if busy(s.state()) {
		m.mu.Unlock()
		return Submission{}, errTurnRunning
	}
	before := m.summaryLocked(s)
	prevBase, prevDetail := s.base, s.detail
	s.setBase(StateWorking, "")
	mark := s.turnSeq
	m.changedLocked(s, before)
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(m.ctx, sendTimeout)
	err := conv.Send(ctx, text)
	cancel()
	if err == nil {
		return m.recordSubmission(s, reqID, SubmissionAccepted, "", false), nil
	}
	m.mu.Lock()
	before = m.summaryLocked(s)
	if s.turnSeq == mark {
		s.base, s.detail = prevBase, prevDetail
	}
	m.changedLocked(s, before)
	m.mu.Unlock()
	log.Warn("web prompt submission failed", "session", s.id, "error", err)
	switch {
	case errors.Is(err, agentapi.ErrBusy):
		return Submission{}, newError(http.StatusConflict, "the provider is still running a turn")
	case errors.Is(err, agentapi.ErrSubmissionUncertain):
		// Never resubmit: the provider may already be working on it.
		return m.recordSubmission(s, reqID, SubmissionUncertain,
			"the provider may or may not have received this prompt, and uam did not resend it: "+shortError(err), true), nil
	default:
		return m.recordSubmission(s, reqID, SubmissionRejected, shortError(err), true), nil
	}
}

// steer adds text to the running turn. The turn state does not change: the
// turn the steer joins reports its own end. The caller holds s.op.
func (m *Manager) steer(s *webSession, conv agentapi.Conversation, text, reqID string) (Submission, error) {
	if conv == nil {
		return m.recordSubmission(s, reqID, SubmissionRejected, "the provider conversation is not open", false), nil
	}
	ctx, cancel := context.WithTimeout(m.ctx, sendTimeout)
	err := conv.Steer(ctx, text)
	cancel()
	switch {
	case err == nil:
		return m.recordSubmission(s, reqID, SubmissionAccepted, "", false), nil
	case errors.Is(err, agentapi.ErrUnsupported):
		return Submission{}, newError(http.StatusConflict, "this provider cannot steer a running turn")
	}
	log.Warn("web steer submission failed", "session", s.id, "error", err)
	if errors.Is(err, agentapi.ErrSubmissionUncertain) {
		// Never resubmit: the provider may already have folded it in.
		return m.recordSubmission(s, reqID, SubmissionUncertain,
			"the provider may or may not have received this steer, and uam did not resend it: "+shortError(err), false), nil
	}
	return m.recordSubmission(s, reqID, SubmissionRejected, shortError(err), false), nil
}

func (s *webSession) findSubmission(reqID string) (Submission, bool) {
	for i := len(s.submissions) - 1; i >= 0; i-- {
		if s.submissions[i].RequestID == reqID {
			return s.submissions[i], true
		}
	}
	return Submission{}, false
}

// findRequest returns the recorded outcome of reqID, or "queued" while it
// waits in the queue.
func (s *webSession) findRequest(reqID string) (Submission, bool) {
	if sub, ok := s.findSubmission(reqID); ok {
		return sub, true
	}
	for _, q := range s.queue {
		if q.RequestID == reqID {
			return Submission{RequestID: reqID, Status: SubmissionQueued, Time: q.QueuedAt}, true
		}
	}
	return Submission{}, false
}

func (s *webSession) hasSubmission(reqID string) bool {
	_, ok := s.findRequest(reqID)
	return ok
}

// remember keeps sub for repeated request IDs, within maxSubmissions.
func (s *webSession) remember(sub Submission) {
	s.submissions = append(s.submissions, sub)
	if len(s.submissions) > maxSubmissions {
		s.submissions = append([]Submission(nil), s.submissions[len(s.submissions)-maxSubmissions:]...)
	}
}

// recordSubmission records a prompt's outcome. pauseQueue pauses a non-empty
// queue when the outcome is not accepted: queued work never follows a prompt
// that did not start its turn.
func (m *Manager) recordSubmission(s *webSession, reqID, status, msg string, pauseQueue bool) Submission {
	sub := Submission{RequestID: reqID, Status: status, Error: msg, Time: m.now()}
	m.mu.Lock()
	before := m.summaryLocked(s)
	s.remember(sub)
	last := sub
	s.last = &last
	m.broadcastLocked("submission", s.id, func(seq uint64) any {
		return submissionEvent{Seq: seq, SessionID: s.id, Submission: sub}
	})
	if pauseQueue && status != SubmissionAccepted {
		m.pauseQueueLocked(s)
	}
	m.changedLocked(s, before)
	m.mu.Unlock()
	// The request ID must be durable before the browser hears the outcome,
	// so a restarted service still recognizes a retry.
	if err := m.flush(); err != nil {
		log.Warn("persist web submission failed", "session", s.id, "error", err)
	}
	return sub
}

func (s *webSession) queueSnapshot() []QueuedPrompt {
	return append([]QueuedPrompt{}, s.queue...)
}

// enqueueLocked adds a prompt to the queue. Nothing reaches the provider
// before the running turn completes.
func (m *Manager) enqueueLocked(s *webSession, text, reqID string) (Submission, error) {
	if len(s.queue) >= maxQueue {
		return Submission{}, newError(http.StatusConflict, "the queue is full (%d prompts)", maxQueue)
	}
	before := m.summaryLocked(s)
	q := QueuedPrompt{RequestID: reqID, Text: text, QueuedAt: m.now()}
	s.queue = append(s.queue, q)
	s.queueChanged = true
	m.changedLocked(s, before)
	if !busy(s.state()) {
		m.kickDrainLocked(s)
	}
	return Submission{RequestID: reqID, Status: SubmissionQueued, Time: q.QueuedAt}, nil
}

// pauseQueueLocked keeps a non-empty queue from draining until the user
// resumes or clears it. The caller publishes the change.
func (m *Manager) pauseQueueLocked(s *webSession) {
	if len(s.queue) > 0 && !s.queuePaused {
		s.queuePaused = true
		s.queueChanged = true
	}
}

// kickDrainLocked has the queue's head sent once nothing else holds s.op.
// The drain checks everything again first, so a spare kick is harmless.
func (m *Manager) kickDrainLocked(s *webSession) {
	if m.closed || s.removed || len(s.queue) == 0 || s.queuePaused {
		return
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.drain(s)
	}()
}

// drain sends the queue's head through the normal submit path, with its own
// request ID, when no turn is running. The head leaves the queue before it is
// sent: from then on it cannot be cancelled, and it is never resent.
func (m *Manager) drain(s *webSession) {
	s.op.Lock()
	defer s.op.Unlock()
	m.mu.Lock()
	if m.closed || s.removed || len(s.queue) == 0 || s.queuePaused || busy(s.state()) {
		m.mu.Unlock()
		return
	}
	before := m.summaryLocked(s)
	head := s.queue[0]
	s.queueSending = head.RequestID
	s.queue = slices.Delete(s.queue, 0, 1)
	s.queueChanged = true
	m.changedLocked(s, before)
	m.mu.Unlock()
	_, err := m.send(s, head.Text, head.RequestID)
	m.mu.Lock()
	defer m.mu.Unlock()
	s.queueSending = ""
	if err != nil {
		// Nothing was sent: a turn is running after all, or the service is
		// stopping. The prompt waits at the front for the next completed turn.
		if !m.closed && !s.removed {
			before := m.summaryLocked(s)
			s.queue = slices.Insert(s.queue, 0, head)
			s.queueChanged = true
			m.changedLocked(s, before)
		}
	}
}

// CancelQueued removes one prompt from the queue before it is sent. It never
// contacts the provider. Cancelling it again succeeds; a prompt already sent
// is refused with 409.
func (m *Manager) CancelQueued(id, reqID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	switch {
	case s == nil:
		return newError(http.StatusNotFound, "session not found")
	case m.closed:
		return errShuttingDown
	case s.stage != StageActive:
		return s.readOnlyLocked()
	}
	i := slices.IndexFunc(s.queue, func(q QueuedPrompt) bool { return q.RequestID == reqID })
	if i < 0 {
		if s.queueSending != "" && s.queueSending == reqID {
			return newError(http.StatusConflict, "the prompt was already sent")
		}
		sub, ok := s.findSubmission(reqID)
		switch {
		case !ok:
			return newError(http.StatusNotFound, "queued prompt not found")
		case sub.Status == SubmissionCancelled:
			return nil
		default:
			return newError(http.StatusConflict, "the prompt was already sent")
		}
	}
	before := m.summaryLocked(s)
	s.queue = slices.Delete(s.queue, i, i+1)
	m.cancelledLocked(s, reqID)
	if len(s.queue) == 0 {
		s.queuePaused = false
	}
	s.queueChanged = true
	m.changedLocked(s, before)
	return nil
}

// ClearQueue removes every queued prompt, as CancelQueued does one.
func (m *Manager) ClearQueue(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	switch {
	case s == nil:
		return newError(http.StatusNotFound, "session not found")
	case m.closed:
		return errShuttingDown
	case s.stage != StageActive:
		return s.readOnlyLocked()
	case len(s.queue) == 0:
		return nil
	}
	before := m.summaryLocked(s)
	for _, q := range s.queue {
		m.cancelledLocked(s, q.RequestID)
	}
	s.queue, s.queuePaused = nil, false
	s.queueChanged = true
	m.changedLocked(s, before)
	return nil
}

// ResumeQueue lets a paused queue drain again: at once when no turn is
// running, otherwise after the running turn completes.
func (m *Manager) ResumeQueue(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	switch {
	case s == nil:
		return newError(http.StatusNotFound, "session not found")
	case m.closed:
		return errShuttingDown
	case s.stage != StageActive:
		return s.readOnlyLocked()
	}
	if s.queuePaused {
		before := m.summaryLocked(s)
		s.queuePaused = false
		s.queueChanged = true
		m.changedLocked(s, before)
	}
	if !busy(s.state()) {
		m.kickDrainLocked(s)
	}
	return nil
}

// cancelledLocked remembers that reqID left the queue unsent, so a repeated
// request reports that instead of queueing it again. It is not a submission:
// last_submission stays as it was.
func (m *Manager) cancelledLocked(s *webSession, reqID string) {
	s.remember(Submission{RequestID: reqID, Status: SubmissionCancelled, Time: m.now()})
}

// Cancel aborts the running turn. It is distinct from a viewer leaving and
// from Close: the conversation stays open.
func (m *Manager) Cancel(id string) (SessionSummary, error) {
	m.mu.Lock()
	s := m.sessions[id]
	if s == nil {
		m.mu.Unlock()
		return SessionSummary{}, newError(http.StatusNotFound, "session not found")
	}
	conv, state, caps := s.conv, s.state(), m.infos[s.provider].Capabilities
	if !caps.Cancel {
		m.mu.Unlock()
		return SessionSummary{}, newError(http.StatusConflict, "this provider does not support cancelling a turn")
	}
	if conv == nil || state == StateStarting || !busy(state) {
		m.mu.Unlock()
		return SessionSummary{}, newError(http.StatusConflict, "no turn is running")
	}
	before := m.summaryLocked(s)
	m.pauseQueueLocked(s)
	m.changedLocked(s, before)
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(m.ctx, controlTimeout)
	err := conv.Cancel(ctx)
	cancel()
	switch {
	case err == nil:
	case errors.Is(err, agentapi.ErrUnsupported):
		return SessionSummary{}, newError(http.StatusConflict, "this provider does not support cancelling a turn")
	case errors.Is(err, agentapi.ErrClosed):
		return SessionSummary{}, newError(http.StatusConflict, "the provider conversation is closed")
	default:
		return SessionSummary{}, newError(http.StatusBadGateway, "cancel failed: %s", shortError(err))
	}
	return m.Summary(id)
}

// Close disconnects the conversation and keeps the record.
func (m *Manager) Close(id string) (SessionSummary, error) {
	s, err := m.lookup(id)
	if err != nil {
		return SessionSummary{}, err
	}
	s.op.Lock()
	defer s.op.Unlock()
	m.mu.Lock()
	if s.removed {
		m.mu.Unlock()
		return SessionSummary{}, newError(http.StatusNotFound, "session not found")
	}
	before := m.summaryLocked(s)
	conv := m.disconnectLocked(s)
	m.changedLocked(s, before)
	m.mu.Unlock()
	if conv != nil {
		m.closeConversation(conv)
	}
	if err := m.flush(); err != nil {
		log.Warn("persist closed web session failed", "session", id, "error", err)
	}
	return m.Summary(id)
}

// disconnectLocked detaches s from its conversation, as Close does, and
// returns the conversation for the caller to close.
func (m *Manager) disconnectLocked(s *webSession) agentapi.Conversation {
	conv := s.conv
	s.conv = nil
	s.gen++
	m.expirePendingLocked(s, "the session was closed")
	m.endSubagentsLocked(s)
	m.pauseQueueLocked(s)
	s.setBase(StateClosed, "")
	return conv
}

// Settle marks an active Task complete and closes its conversation. It is
// refused while the Task is busy, has queued prompts or waits for an answer.
func (m *Manager) Settle(id string) (SessionSummary, error) {
	return m.moveStage(id, StageSettled, StageActive)
}

// Reopen makes a settled Task active again. Its next prompt reopens the same
// conversation.
func (m *Manager) Reopen(id string) (SessionSummary, error) {
	return m.moveStage(id, StageActive, StageSettled)
}

// Archive moves an active or settled Task to its final stage; nothing moves
// it back. An active Task must meet the same conditions as for Settle.
func (m *Manager) Archive(id string) (SessionSummary, error) {
	return m.moveStage(id, StageArchived, StageActive, StageSettled)
}

// moveStage moves a Task from one of the stages in from to stage to. Leaving
// the active stage closes the conversation. Holding s.op keeps prompts, drains
// and opens out while the preconditions are checked.
func (m *Manager) moveStage(id, to string, from ...string) (SessionSummary, error) {
	s, err := m.lookup(id)
	if err != nil {
		return SessionSummary{}, err
	}
	s.op.Lock()
	defer s.op.Unlock()
	m.mu.Lock()
	switch {
	case s.removed:
		m.mu.Unlock()
		return SessionSummary{}, newError(http.StatusNotFound, "session not found")
	case m.closed:
		m.mu.Unlock()
		return SessionSummary{}, errShuttingDown
	case !slices.Contains(from, s.stage):
		m.mu.Unlock()
		return SessionSummary{}, newError(http.StatusConflict, "a task that is %s cannot be %s", stageName(s.stage), stageVerb(to))
	}
	before := m.summaryLocked(s)
	var conv agentapi.Conversation
	if s.stage == StageActive {
		if err := s.settleableLocked(); err != nil {
			m.mu.Unlock()
			return SessionSummary{}, err
		}
		if s.conv != nil {
			conv = m.disconnectLocked(s)
		}
	}
	now := m.now()
	switch to {
	case StageSettled:
		s.settledAt = now
	case StageArchived:
		s.archivedAt = now
	case StageActive:
		s.settledAt = time.Time{}
	}
	s.stage = to
	m.changedLocked(s, before)
	m.mu.Unlock()
	if conv != nil {
		m.closeConversation(conv)
	}
	if err := m.flush(); err != nil {
		log.Warn("persist web task stage failed", "session", id, "error", err)
	}
	log.Info("web task stage changed", "session", id, "stage", stageName(to))
	return m.Summary(id)
}

// settleableLocked reports why an active Task cannot be settled or archived
// now, or nil. A pending interaction that does not make the Task busy is a
// yolo approval still on its way to the provider.
func (s *webSession) settleableLocked() error {
	switch {
	case busy(s.state()):
		return newError(http.StatusConflict, "the task is running or waiting for input; stop the turn first")
	case slices.ContainsFunc(s.interactions, func(ix *interaction) bool { return ix.State == agentapi.InteractionPending }):
		return newError(http.StatusConflict, "a permission request is still being answered; try again")
	case len(s.queue) > 0:
		return newError(http.StatusConflict, "the task has queued prompts; send or clear them first")
	}
	return nil
}

// readOnlyLocked refuses changes to a settled or archived Task.
func (s *webSession) readOnlyLocked() error {
	switch s.stage {
	case StageSettled:
		return newError(http.StatusConflict, "the task is settled; reopen it first")
	case StageArchived:
		return newError(http.StatusConflict, "the task is archived")
	}
	return nil
}

func stageName(stage string) string {
	if stage == StageActive {
		return "active"
	}
	return stage
}

func stageVerb(stage string) string {
	switch stage {
	case StageSettled:
		return "settled"
	case StageArchived:
		return "archived"
	}
	return "reopened"
}

// Rename sets the Task's typed name. An empty name shows the provider title
// again; the provider's conversation is not renamed.
func (m *Manager) Rename(id, name string) (SessionSummary, error) {
	clean, err := cleanTaskName(name)
	if err != nil {
		return SessionSummary{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return SessionSummary{}, newError(http.StatusNotFound, "session not found")
	}
	if s.stage == StageArchived {
		return SessionSummary{}, s.readOnlyLocked()
	}
	before := m.summaryLocked(s)
	s.name = clean
	m.changedLocked(s, before)
	return m.summaryLocked(s), nil
}

// SetModel changes the supplied settings together for the Task's next turns.
// An omitted effort or context size survives a model change when supported.
// With the conversation open, settings are stored only after provider success;
// otherwise the next open applies them. Changes during a turn are refused.
func (m *Manager) SetModel(id string, model, effort, contextSize *string) (SessionSummary, error) {
	s, err := m.lookup(id)
	if err != nil {
		return SessionSummary{}, err
	}
	s.op.Lock()
	defer s.op.Unlock()
	m.mu.Lock()
	if s.removed {
		m.mu.Unlock()
		return SessionSummary{}, newError(http.StatusNotFound, "session not found")
	}
	if s.stage != StageActive {
		m.mu.Unlock()
		return SessionSummary{}, s.readOnlyLocked()
	}
	if model != nil && *model == "" {
		m.mu.Unlock()
		return SessionSummary{}, newError(http.StatusBadRequest, "model must be an offered model ID")
	}
	nextModel, nextEffort, nextSize := s.model, s.effort, cmp.Or(s.contextSize, "default")
	if model != nil {
		nextModel = *model
	}
	if effort != nil {
		nextEffort = *effort
	}
	if contextSize != nil {
		nextSize = cmp.Or(*contextSize, "default")
	}
	if nextModel != s.model {
		mo := m.modelLocked(s.provider, nextModel)
		if effort == nil && !slices.Contains(mo.Efforts, nextEffort) {
			nextEffort = ""
		}
		if contextSize == nil && !slices.ContainsFunc(mo.ContextSizes, func(size agentapi.ContextSize) bool { return size.ID == nextSize }) {
			nextSize = "default"
		}
	}
	if err := m.validateSelectionLocked(s.provider, nextModel, nextEffort, nextSize); err != nil {
		m.mu.Unlock()
		return SessionSummary{}, err
	}
	if s.model == nextModel && s.effort == nextEffort && cmp.Or(s.contextSize, "default") == nextSize {
		m.mu.Unlock()
		return m.Summary(id)
	}
	if busy(s.state()) {
		m.mu.Unlock()
		return SessionSummary{}, newError(http.StatusConflict, "the model, effort or context size cannot change while a turn is running")
	}
	conv := s.conv
	m.mu.Unlock()
	if conv != nil {
		ctx, cancel := context.WithTimeout(m.ctx, controlTimeout)
		err := conv.SetModel(ctx, nextModel, nextEffort, nextSize)
		cancel()
		switch {
		case err == nil, errors.Is(err, agentapi.ErrClosed):
			// The next open reapplies the selection if the conversation closed.
		case errors.Is(err, agentapi.ErrUnsupported):
			return SessionSummary{}, newError(http.StatusConflict, "this provider does not support changing the model settings")
		default:
			log.Warn("web model switch failed", "session", id, "error", err)
			return SessionSummary{}, newError(http.StatusBadGateway, "could not change the model settings: %s", shortError(err))
		}
	}
	m.mu.Lock()
	before := m.summaryLocked(s)
	s.model, s.effort, s.contextSize = nextModel, nextEffort, nextSize
	s.context = nil
	m.changedLocked(s, before)
	m.mu.Unlock()
	return m.Summary(id)
}

// modelLocked returns metadata only for an offered model.
func (m *Manager) modelLocked(provider, model string) agentapi.Model {
	for _, mo := range m.infos[provider].Models {
		if mo.ID == model {
			return mo
		}
	}
	return agentapi.Model{}
}

func (m *Manager) validateSelectionLocked(provider, model, effort, contextSize string) error {
	mo := m.modelLocked(provider, model)
	if model != "" && mo.ID == "" {
		return newError(http.StatusBadRequest, "model %q is not offered by this provider", model)
	}
	if effort != "" && (model == "" || model == "auto" || !slices.Contains(mo.Efforts, effort)) {
		return newError(http.StatusBadRequest, "effort %q is not offered by model %q", effort, model)
	}
	if contextSize != "default" && (!m.infos[provider].Capabilities.ContextSize || model == "" || model == "auto" || !slices.ContainsFunc(mo.ContextSizes, func(size agentapi.ContextSize) bool { return size.ID == contextSize && size.Tokens > 0 })) {
		return newError(http.StatusBadRequest, "context size %q is not offered by model %q", contextSize, model)
	}
	return nil
}

// Answer forwards the user's answer to a pending interaction. The first
// answer wins. The service answers on the user's behalf only for permission
// requests of a yolo Task, and never for questions.
func (m *Manager) Answer(id, interactionID string, answer agentapi.Answer) (agentapi.Interaction, error) {
	m.mu.Lock()
	s := m.sessions[id]
	if s == nil {
		m.mu.Unlock()
		return agentapi.Interaction{}, newError(http.StatusNotFound, "session not found")
	}
	ix := s.ixIdx[interactionID]
	if ix == nil {
		m.mu.Unlock()
		return agentapi.Interaction{}, newError(http.StatusNotFound, "interaction not found")
	}
	if err := interactionOpen(ix); err != nil {
		m.mu.Unlock()
		return agentapi.Interaction{}, err
	}
	if err := validateAnswer(ix.Interaction, answer); err != nil {
		m.mu.Unlock()
		return agentapi.Interaction{}, err
	}
	conv := s.conv
	if conv == nil {
		before := m.summaryLocked(s)
		m.expireLocked(s, ix, "the provider conversation is not open")
		m.changedLocked(s, before)
		m.mu.Unlock()
		return agentapi.Interaction{}, newError(http.StatusGone, "the interaction expired")
	}
	ix.answering = true
	m.mu.Unlock()
	return m.respond(s, ix, conv, interactionID, answer)
}

// respond sends answer to an interaction the caller claimed by setting
// ix.answering, and records the outcome. While it is claimed, every other
// answer is refused, so the first answer wins.
func (m *Manager) respond(s *webSession, ix *interaction, conv agentapi.Conversation, interactionID string, answer agentapi.Answer) (agentapi.Interaction, error) {
	ctx, cancel := context.WithTimeout(m.ctx, controlTimeout)
	err := conv.Respond(ctx, interactionID, answer)
	cancel()

	m.mu.Lock()
	defer m.mu.Unlock()
	before := m.summaryLocked(s)
	ix.answering = false
	if err != nil {
		// Not answered: the request is the user's again.
		ix.yolo = false
	}
	switch {
	case err == nil:
		if ix.State == agentapi.InteractionPending {
			ix.State, ix.Resolution = resolution(ix.Interaction, answer)
			if ix.yolo {
				ix.Resolution = yoloResolution
			}
			m.publishInteractionLocked(s, ix)
		}
		m.changedLocked(s, before)
		return ix.Interaction, nil
	case errors.Is(err, agentapi.ErrInteractionGone), errors.Is(err, agentapi.ErrClosed):
		if ix.State == agentapi.InteractionPending {
			m.expireLocked(s, ix, "the provider no longer waits for this answer")
		}
		m.changedLocked(s, before)
		return agentapi.Interaction{}, newError(http.StatusGone, "the interaction expired")
	default:
		m.changedLocked(s, before)
		log.Warn("web interaction answer failed", "session", s.id, "error", err)
		return agentapi.Interaction{}, newError(http.StatusBadGateway, "answer failed: %s", shortError(err))
	}
}

// yoloResolution is the recorded resolution of a permission request that
// yolo mode allowed.
const yoloResolution = "allowed (yolo)"

// autoAllowLocked answers a pending permission request of a yolo Task with
// the provider's single-use allow option, through the same claim as a
// browser's answer. Questions, and requests without that option (the
// provider's policy says a person must decide), stay with the user.
func (m *Manager) autoAllowLocked(s *webSession, ix *interaction) {
	if s.mode != store.ModeYolo || m.closed || s.removed || s.conv == nil ||
		ix.Kind != agentapi.InteractionPermission || ix.State != agentapi.InteractionPending || ix.answering {
		return
	}
	i := slices.IndexFunc(ix.Options, func(o agentapi.Option) bool { return o.AllowOnce && !o.Reject })
	if i < 0 {
		return
	}
	conv, id, answer := s.conv, ix.ID, agentapi.Answer{Decision: ix.Options[i].ID, Auto: true}
	ix.answering, ix.yolo = true, true
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		if _, err := m.respond(s, ix, conv, id, answer); err != nil {
			log.Warn("web yolo approval failed", "session", s.id, "interaction", id, "error", err)
		}
	}()
}

// autoAllowPendingLocked applies yolo mode to every pending permission
// request of s.
func (m *Manager) autoAllowPendingLocked(s *webSession) {
	for _, ix := range s.interactions {
		m.autoAllowLocked(s, ix)
	}
}

// parseMode validates a Task mode.
func parseMode(mode string) (store.Mode, error) {
	switch store.Mode(mode) {
	case store.ModeSafe, store.ModeYolo:
		return store.Mode(mode), nil
	}
	return "", newError(http.StatusBadRequest, "mode must be safe or yolo")
}

// SetMode sets the Task's permission mode at any time, even while a turn
// runs. It applies to permission requests raised afterwards; switching to yolo
// also answers the ones already pending.
func (m *Manager) SetMode(id, mode string) (SessionSummary, error) {
	md, err := parseMode(mode)
	if err != nil {
		return SessionSummary{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return SessionSummary{}, newError(http.StatusNotFound, "session not found")
	}
	if err := s.readOnlyLocked(); err != nil {
		return SessionSummary{}, err
	}
	before := m.summaryLocked(s)
	s.mode = md
	m.autoAllowPendingLocked(s)
	m.changedLocked(s, before)
	return m.summaryLocked(s), nil
}

func interactionOpen(ix *interaction) error {
	switch ix.State {
	case agentapi.InteractionExpired:
		return newError(http.StatusGone, "the interaction expired")
	case agentapi.InteractionPending:
		if ix.answering {
			return newError(http.StatusConflict, "the interaction is already being answered")
		}
		return nil
	default:
		return newError(http.StatusConflict, "the interaction was already resolved")
	}
}

// Shutdown ends every conversation this service drives: running turns are
// recorded as interrupted, conversations are closed, and every provider is
// asked to stop the runtimes it started.
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	var convs []agentapi.Conversation
	for _, s := range m.sessions {
		before := m.summaryLocked(s)
		if st := s.state(); busy(st) && st != StateStarting {
			s.setBase(StateInterrupted, interruptedDetail)
		}
		if s.conv != nil {
			convs = append(convs, s.conv)
			s.conv = nil
		}
		s.gen++
		m.expirePendingLocked(s, "the uam web service stopped")
		m.endSubagentsLocked(s)
		// The queue lives in memory only; the prompts in it are not sent.
		s.queue, s.queuePaused = nil, false
		m.changedLocked(s, before)
	}
	for sub := range m.subs {
		m.dropLocked(sub)
	}
	m.mu.Unlock()
	// Abort in-flight opens and sends; their outcome is recorded as usual.
	m.cancel()
	for _, conv := range convs {
		m.closeConversation(conv)
	}
	var firstErr error
	for _, name := range m.order {
		if err := m.providers[name].Shutdown(ctx); err != nil {
			log.Warn("web provider shutdown failed", "provider", name, "error", err)
			if firstErr == nil {
				firstErr = fmt.Errorf("shut down %s: %w", name, err)
			}
		}
	}
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
	if err := m.flush(); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("persist web sessions: %w", err)
	}
	return firstErr
}

// canonicalWorkdir validates a requested project directory and returns its
// canonical path.
func canonicalWorkdir(p string) (string, error) {
	if p == "" {
		return "", newError(http.StatusBadRequest, "dir is required")
	}
	if !filepath.IsAbs(p) {
		return "", newError(http.StatusBadRequest, "dir must be an absolute path")
	}
	for _, r := range p {
		if unicode.IsControl(r) {
			return "", newError(http.StatusBadRequest, "dir contains control characters")
		}
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

// cleanTaskName sanitizes an optional Task name; "" means the provider title
// is shown.
func cleanTaskName(name string) (string, error) {
	clean := strings.TrimSpace(displaytext.Sanitize(name))
	if utf8.RuneCountInString(clean) > maxNameRunes {
		return "", newError(http.StatusBadRequest, "name is longer than %d characters", maxNameRunes)
	}
	return clean, nil
}

func cleanName(name, fallback string) (string, error) {
	clean := strings.TrimSpace(displaytext.Sanitize(name))
	if clean == "" {
		clean = clipRunes(strings.TrimSpace(displaytext.Sanitize(fallback)), maxNameRunes)
	}
	if clean == "" {
		return "", newError(http.StatusBadRequest, "name is required")
	}
	if utf8.RuneCountInString(clean) > maxNameRunes {
		return "", newError(http.StatusBadRequest, "name is longer than %d characters", maxNameRunes)
	}
	return clean, nil
}

// shortError is provider error text made safe and short enough to show.
func shortError(err error) string {
	return clipRunes(displaytext.Sanitize(err.Error()), maxDetailRunes)
}

func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

func newUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32], nil
}
