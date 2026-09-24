package web

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
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
	// maxHistoryReads bounds the read-only transcript loads running at once.
	maxHistoryReads = 2
	// historyIdle is how long a transcript read without opening the
	// conversation stays in memory after a viewer last asked for it;
	// historyRetry is how soon a failed read is tried again.
	historyIdle  = 10 * time.Minute
	historyRetry = time.Minute
	historySweep = time.Minute
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
	// branchMu orders Project branch reads, so an older read never replaces
	// a newer one. It is never taken while holding mu.
	branchMu sync.Mutex
	// settingsMu orders settings changes, so the stored and published
	// settings match. It is never taken while holding mu.
	settingsMu sync.Mutex

	// imageJobs feeds the one goroutine that stores tool images, so Emit
	// never writes a file. imageWG counts the jobs not yet finished; tests
	// wait on it. storeImageHook, when set, runs before each job is stored.
	imageJobs      chan imageJob
	imageWG        sync.WaitGroup
	storeImageHook func()

	mu       sync.Mutex
	infos    map[string]ProviderInfo
	modelsAt map[string]time.Time
	fetching map[string]bool
	projects map[string]*Project
	settings Settings
	sessions map[string]*webSession
	dirty    map[string]struct{}
	creating map[string]chan struct{}
	seq      uint64
	subs     map[*Subscriber]struct{}
	closed   bool
	now      func() time.Time
	// pick returns a random int in [0, n) for badge choices; tests replace
	// it before Start.
	pick func(n int) int
	// branchAt is when each Project's branch was last read.
	branchAt map[string]time.Time
	// quota is each usage provider's quota cache; quotaPolled is when the
	// latest read began.
	quota       map[string]*quotaCache
	quotaPolled time.Time
	// quotaKick asks the usage loop for a read; quotaTick is how often the
	// loop checks for a due one (tests set it before Start).
	quotaKick chan struct{}
	quotaTick time.Duration
	// titles counts title jobs, which Shutdown waits for before providers
	// stop: a job deletes its throwaway conversation on the way out.
	// titleSlots holds one token per job calling its provider.
	titles     sync.WaitGroup
	titleSlots chan struct{}
	// reads holds a slot for each read-only transcript load in progress.
	reads chan struct{}
	// hostLive reports whether a terminal session host runs; nil means none.
	hostLive func(sessionName string) bool
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
		pick:      randomPick,
		branchAt:  map[string]time.Time{},
		quota:     map[string]*quotaCache{},
		quotaKick: make(chan struct{}, 1),
		quotaTick: quotaCheck,

		titleSlots: make(chan struct{}, maxTitleJobs),
		imageJobs:  make(chan imageJob, maxImageJobs),
		reads:      make(chan struct{}, maxHistoryReads),
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
	usage       *agentapi.Usage
	name        string
	title       string
	lastModel   string
	// renames counts Rename calls, so a title job knows when a rename came
	// while it ran.
	renames uint64
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
	turnSeq           uint64
	turnTimings       []TurnTiming
	activeTiming      int
	pendingTimingUser string
	timingRevision    uint64

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

	subagents        []*agentapi.Subagent
	subIdx           map[string]*agentapi.Subagent
	stoppedSubagents map[string]bool // accepted stops awaiting the provider event
	backgroundTasks  *agentapi.BackgroundTasks
	execution        *agentapi.ExecutionState

	commandSubmissions []Submission
	commandLedger      string
	stopSeq            uint64
	submissions        []Submission
	last               *Submission
	createReq          string
	// subagentPrompts are the outcomes of follow-ups to subagents, within
	// maxSubmissions. They are not Task submissions: last never holds one.
	subagentPrompts []Submission

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

	// uploads are the Task's stored attachments and tool images by ID.
	uploads map[string]*upload
	// imageMu serializes storing tool images, so an image the live stream
	// and the history both carry is stored once. It is taken before
	// Manager.mu, never while holding it.
	imageMu sync.Mutex

	// history is HistoryLoaded once items and subagents hold the
	// conversation's record, HistoryLoading while it is read, and
	// HistoryUnavailable, with historyReason, when it could not be; "" until
	// a viewer asks. historyRead marks a record read without opening the
	// conversation: only such a record is dropped when idle. historyUsed is
	// when a viewer last asked for it, historyFailed when a read last failed.
	history, historyReason     string
	historyRead                bool
	historyCancel              context.CancelFunc
	historyGen                 uint64
	historyBytes               int
	historyUsed, historyFailed time.Time
	// terminalID is the terminal session record tied to the same
	// conversation; terminalHost and terminalName are its host session and
	// name, when that record was found.
	terminalID, terminalHost, terminalName string
	imported                               bool

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
	timingRevision                                                                                                   uint64
	commandResult                                                                                                    *agentapi.CommandResult
	turn, detail, name, convID, reqID, reqStatus, commandLedger, projectID, model, effort, contextSize, title, stage string
	mode                                                                                                             store.Mode
	settledAt, archivedAt                                                                                            time.Time
}

func newSession(id, provider, name, workdir, convID string, created time.Time) *webSession {
	return &webSession{
		id: id, provider: provider, name: name, workdir: workdir, convID: convID,
		createdAt: created, updatedAt: created, base: StateIdle, mode: store.ModeSafe, activeTiming: -1,
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
	k := persistKey{timingRevision: s.timingRevision, turn: s.durableState(), detail: s.detail, name: s.name, convID: s.convID, projectID: s.projectID, model: s.model, effort: s.effort, contextSize: s.contextSize, title: s.title, mode: s.mode,
		stage: s.stage, settledAt: s.settledAt, archivedAt: s.archivedAt}
	if s.last != nil {
		k.reqID, k.reqStatus = s.last.RequestID, s.last.Status
		k.commandResult = s.last.CommandResult
	}
	k.commandLedger = s.commandLedger
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
// assigns records from before Projects existed to a Project, and gives each
// Project without a valid badge one. A provider whose check fails is listed
// as unavailable; it is not fatal.
func (m *Manager) Start(ctx context.Context) error {
	infos := m.checkProviders(ctx)
	cfg, err := m.store.Load()
	if err != nil {
		return fmt.Errorf("load web sessions: %w", err)
	}
	if needsProject(cfg) || len(badgeless(cfg.WebProjects)) > 0 {
		now := m.now()
		err := m.store.Update(func(c *store.Config) error {
			if err := assignProjects(c, now); err != nil {
				return err
			}
			assignBadges(c, m.pick)
			cfg = *c
			return nil
		})
		if err != nil {
			// Keep serving: the assignment holds for this run. A record that
			// later gets its project_id written without the Project, or a
			// Project without its badge, is assigned again on the next start.
			log.Warn("assign web sessions to projects failed", "error", err)
			if err := assignProjects(&cfg, now); err != nil {
				return fmt.Errorf("assign web sessions to projects: %w", err)
			}
			assignBadges(&cfg, nil)
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
		m.projects[id] = &Project{ID: p.ID, Name: loadedName(p.Name, p.Dir), Dir: p.Dir, CreatedAt: p.CreatedAt, Defaults: TaskDefaults(p.Defaults), Badge: Badge(p.Badge)}
	}
	m.settings = Settings{SendDefault: cmp.Or(cfg.WebSettings.SendDefault, store.WebSendSteer), HiddenModels: cfg.WebSettings.HiddenModels, TitleModel: cfg.WebSettings.TitleModel}
	if m.settings.SendDefault != store.WebSendQueue {
		m.settings.SendDefault = store.WebSendSteer
	}
	terminals := map[string]store.SessionRecord{}
	for _, rec := range cfg.Sessions {
		if rec.Surface == "" && rec.ID != "" {
			terminals[rec.ID] = rec
		}
	}
	for _, rec := range cfg.Sessions {
		if rec.Surface != store.SurfaceWeb || rec.ID == "" {
			continue
		}
		s := sessionFromRecord(rec)
		if t, ok := terminals[s.terminalID]; ok && s.terminalID != "" {
			s.terminalHost, s.terminalName = t.SessionName, t.Name
		}
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
	usage := m.usageProviderLocked()
	m.mu.Unlock()
	m.loadUploads()
	m.sweepUploads()
	m.wg.Add(3)
	go m.persistLoop()
	go m.sweepLoop()
	if usage {
		m.wg.Add(1)
		go m.usageLoop()
	}
	go m.imageLoop()
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
		s.turnTimings = slices.Clone(web.TurnTimings)
		if len(s.turnTimings) > maxTurnTimings {
			s.turnTimings = s.turnTimings[len(s.turnTimings)-maxTurnTimings:]
		}
		for i := range s.turnTimings {
			if s.turnTimings[i].State == StateWorking {
				s.turnTimings[i].State = "unknown"
			}
		}
		_ = json.Unmarshal(web.CommandSubmissions, &s.commandSubmissions)
		s.commandLedger = string(web.CommandSubmissions)
		if knownStates[web.Turn] {
			s.base = web.Turn
		}
		s.detail = web.Detail
		s.projectID, s.model, s.title = web.ProjectID, web.Model, cleanTitle(web.Title)
		s.terminalID = web.TerminalSession
		s.imported = web.Imported
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
			if len(web.CommandResult) > 0 {
				_ = json.Unmarshal(web.CommandResult, &sub.CommandResult)
			}
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
			info.Capabilities = p.Capabilities()
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
		if !validCostTier(mo.CostTier) {
			mo.CostTier = ""
		}
		if mo.DiscountPercent < 0 || mo.DiscountPercent > 100 {
			mo.DiscountPercent = 0
		}
		mo.Prices = cleanPrices(mo.Prices)
		if mo.Media != nil {
			media := *mo.Media
			media.Types = slices.Clone(media.Types)
			mo.Media = &media
		}
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
		Effort: s.effort, ContextSize: cmp.Or(s.contextSize, "default"), Context: s.context, Usage: s.usage,
		LastModel: s.lastModel, SubagentsRunning: s.runningSubagents(), Workdir: s.workdir, ConversationID: s.convID,
		Execution: s.execution, State: s.state(), StateDetail: s.detail, Open: s.conv != nil, Pending: permissions + questions,
		CreatedAt: s.createdAt, UpdatedAt: s.updatedAt, Capabilities: m.infos[s.provider].Capabilities, Queued: len(s.queue),
		Mode: string(s.mode), Stage: s.stage, SettledAt: s.settledAt, ArchivedAt: s.archivedAt,
	}
}

// detailLocked builds s's detail; terminal says whether the host of its
// terminal session runs.
func (m *Manager) detailLocked(s *webSession, terminal bool) SessionDetail {
	d := SessionDetail{
		SessionSummary:   m.summaryLocked(s),
		Seq:              m.seq,
		TurnTimings:      append([]TurnTiming{}, s.turnTimings...),
		Items:            s.agentItems(""),
		Interactions:     make([]agentapi.Interaction, 0, len(s.interactions)),
		Subagents:        s.subagentList(),
		HistoryTruncated: s.truncated,
		Queue:            s.queueSnapshot(),
		QueuePaused:      s.queuePaused,
		History:          s.historyState(),
		HistoryReason:    s.historyReason,
	}
	if terminal {
		d.TerminalSession = &TerminalSession{ID: s.terminalID, Name: cleanTitle(s.terminalName)}
	}
	for _, ix := range s.interactions {
		d.Interactions = append(d.Interactions, ix.Interaction)
	}
	if s.backgroundTasks != nil {
		snapshot := *s.backgroundTasks
		snapshot.Tasks = slices.Clone(snapshot.Tasks)
		d.BackgroundTasks = &snapshot
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

// Detail returns one session with its retained transcript. Asking for it
// starts a read-only load of the recorded transcript when the conversation
// is not open and the transcript is not in memory.
func (m *Manager) Detail(id string) (SessionDetail, error) {
	s, err := m.lookup(id)
	if err != nil {
		return SessionDetail{}, err
	}
	terminal := m.terminalLive(s)
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.removed {
		return SessionDetail{}, newError(http.StatusNotFound, "session not found")
	}
	m.viewHistoryLocked(s)
	return m.detailLocked(s, terminal), nil
}

// Subagent returns one subagent of a session with its retained transcript.
func (m *Manager) Subagent(id, agentID string) (SubagentDetail, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return SubagentDetail{}, newError(http.StatusNotFound, "session not found")
	}
	m.viewHistoryLocked(s)
	sa := s.subIdx[agentID]
	if sa == nil {
		return SubagentDetail{}, newError(http.StatusNotFound, "subagent not found")
	}
	return SubagentDetail{Seq: m.seq, Subagent: *sa, Items: s.agentItems(agentID)}, nil
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
	m.refreshBranches(m.ctx, false)
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.projectsLocked()
}

func (m *Manager) publishProjectLocked(p Project) {
	m.broadcastLocked("project", "", func(seq uint64) any { return projectEvent{Seq: seq, Project: p} })
}

// AddProject adds the directory dir as a Project, with defaults for its new
// Tasks unless defaults is nil, and gives it a badge. A directory has at most
// one Project; adding it again reports the existing one with 409.
func (m *Manager) AddProject(dir, name string, defaults *TaskDefaults) (Project, error) {
	canonical, err := canonicalWorkdir(dir)
	if err != nil {
		return Project{}, err
	}
	clean, err := cleanName(name, filepath.Base(canonical))
	if err != nil {
		return Project{}, err
	}
	var d TaskDefaults
	if defaults != nil {
		if d, err = m.taskDefaults(*defaults); err != nil {
			return Project{}, err
		}
	}
	id, err := newUUID()
	if err != nil {
		return Project{}, fmt.Errorf("generate project id: %w", err)
	}
	branch := readBranch(m.ctx, canonical)
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
	p := Project{ID: id, Name: clean, Dir: canonical, CreatedAt: m.now(), Defaults: d, Branch: branch}
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
		cfg.WebProjects[id] = store.WebProject{ID: id, Name: clean, Dir: canonical, CreatedAt: p.CreatedAt, Defaults: store.WebTaskDefaults(d), Badge: badge}
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

// UpdateProject renames a Project, sets the defaults for its new Tasks, or
// both; a nil argument leaves that part alone. An empty name resets it to the
// directory's base name.
func (m *Manager) UpdateProject(id string, name *string, defaults *TaskDefaults) (Project, error) {
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
	if defaults != nil {
		if next.Defaults, err = m.taskDefaults(*defaults); err != nil {
			return Project{}, err
		}
	}
	if err := m.store.Update(func(cfg *store.Config) error {
		stored, ok := cfg.WebProjects[id]
		if !ok {
			return errProjectNotFound
		}
		if name != nil {
			stored.Name = next.Name
		}
		if defaults != nil {
			stored.Defaults = store.WebTaskDefaults(next.Defaults)
		}
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
	*p = next
	m.publishProjectLocked(*p)
	return *p, nil
}

// taskDefaults checks a Project's defaults for new Tasks the way Create
// checks a Task's selection and mode, and returns them with an empty context
// size made "default". The provider need only be registered.
func (m *Manager) taskDefaults(d TaskDefaults) (TaskDefaults, error) {
	d.ContextSize = cmp.Or(d.ContextSize, "default")
	m.mu.Lock()
	registered := m.providers[d.Provider] != nil
	selectionErr := m.validateSelectionLocked(d.Provider, d.Model, d.Effort, d.ContextSize)
	m.mu.Unlock()
	if !registered {
		return TaskDefaults{}, newError(http.StatusBadRequest, "unknown provider %q", d.Provider)
	}
	if selectionErr != nil {
		return TaskDefaults{}, selectionErr
	}
	if _, err := parseMode(d.Mode); err != nil {
		return TaskDefaults{}, err
	}
	return d, nil
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

// Settings returns the web interface's settings.
func (m *Manager) Settings() Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings
}

// SettingsPatch is a settings change; a nil field changes nothing.
// HiddenModels replaces the hidden model IDs of each provider it names, and
// only those; an empty list hides none of that provider's models. TitleModel
// sets the title model of each provider it names; an empty ID gives that
// provider its own title back.
type SettingsPatch struct {
	SendDefault  *string
	HiddenModels map[string][]string
	TitleModel   map[string]string
}

// UpdateSettings applies p. An invalid value is refused with 400 and changes
// nothing. A change is stored and sent as a settings frame; no change writes
// nothing. Hidden models are a display preference: nothing else checks them.
// A title model must be one the provider lists now, and the provider must
// have the titles capability.
func (m *Manager) UpdateSettings(p SettingsPatch) (Settings, error) {
	if p.SendDefault != nil && *p.SendDefault != store.WebSendSteer && *p.SendDefault != store.WebSendQueue {
		return Settings{}, newError(http.StatusBadRequest, "send_default must be %q or %q", store.WebSendSteer, store.WebSendQueue)
	}
	hidden := make(map[string][]string, len(p.HiddenModels))
	for provider, ids := range p.HiddenModels {
		if m.providers[provider] == nil {
			return Settings{}, newError(http.StatusBadRequest, "unknown provider %q", clipRunes(displaytext.Sanitize(provider), maxDetailRunes))
		}
		if slices.ContainsFunc(ids, func(id string) bool { return !store.ValidHiddenModel(id) }) {
			return Settings{}, newError(http.StatusBadRequest, "a hidden model ID must be 1 to %d bytes without control characters", store.MaxHiddenModelBytes)
		}
		ids = slices.Clone(ids)
		slices.Sort(ids)
		if ids = slices.Compact(ids); len(ids) > store.MaxHiddenModels {
			return Settings{}, newError(http.StatusBadRequest, "at most %d models of a provider can be hidden", store.MaxHiddenModels)
		}
		hidden[provider] = ids
	}
	for provider := range p.TitleModel {
		if m.providers[provider] == nil {
			return Settings{}, newError(http.StatusBadRequest, "unknown provider %q", clipRunes(displaytext.Sanitize(provider), maxDetailRunes))
		}
	}
	m.settingsMu.Lock()
	defer m.settingsMu.Unlock()
	m.mu.Lock()
	current := m.settings
	for provider, ids := range hidden {
		info := m.infos[provider]
		if listed := info.Models; len(listed) > 0 && !slices.ContainsFunc(listed, func(mo agentapi.Model) bool { _, found := slices.BinarySearch(ids, mo.ID); return !found }) {
			m.mu.Unlock()
			return Settings{}, newError(http.StatusBadRequest, "at least one %s model must stay visible", info.DisplayName)
		}
	}
	for provider, model := range p.TitleModel {
		info := m.infos[provider]
		switch {
		case model == "":
		case m.titlerLocked(provider) == nil:
			m.mu.Unlock()
			return Settings{}, newError(http.StatusBadRequest, "%s cannot title tasks with a model", m.providers[provider].DisplayName())
		case !slices.ContainsFunc(info.Models, func(mo agentapi.Model) bool { return mo.ID == model }):
			m.mu.Unlock()
			return Settings{}, newError(http.StatusBadRequest, "%s does not offer model %q", info.DisplayName, clipRunes(displaytext.Sanitize(model), maxDetailRunes))
		}
	}
	m.mu.Unlock()
	next := current
	if p.SendDefault != nil {
		next.SendDefault = *p.SendDefault
	}
	if len(hidden) > 0 {
		next.HiddenModels = withProviders(current.HiddenModels, hidden)
	}
	if len(p.TitleModel) > 0 {
		next.TitleModel = withProviders(current.TitleModel, p.TitleModel)
	}
	if next.SendDefault == current.SendDefault && maps.EqualFunc(next.HiddenModels, current.HiddenModels, slices.Equal) && maps.Equal(next.TitleModel, current.TitleModel) {
		return current, nil
	}
	if err := m.store.Update(func(cfg *store.Config) error {
		cfg.WebSettings.SendDefault = next.SendDefault
		cfg.WebSettings.HiddenModels = withProviders(cfg.WebSettings.HiddenModels, hidden)
		cfg.WebSettings.TitleModel = withProviders(cfg.WebSettings.TitleModel, p.TitleModel)
		return nil
	}); err != nil {
		return Settings{}, fmt.Errorf("save web settings: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings = next
	m.broadcastLocked("settings", "", func(seq uint64) any { return settingsEvent{Seq: seq, Settings: next} })
	return next, nil
}

// withProviders returns a copy of current with each provider in change set
// to its value, or removed for an empty one; nil when none is left.
func withProviders[V string | []string](current, change map[string]V) map[string]V {
	out := maps.Clone(current)
	if out == nil {
		out = map[string]V{}
	}
	for provider, ids := range change {
		if len(ids) == 0 {
			delete(out, provider)
		} else {
			out[provider] = ids
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Delete deletes an archived Task's record and its stored attachments. It
// is refused for any other stage. The conversation is never deleted at the
// provider.
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
		var commandResult json.RawMessage
		if key.commandResult != nil {
			commandResult, _ = json.Marshal(key.commandResult)
		}
		patches = append(patches, recordPatch{
			id: s.id, provider: s.provider, name: s.name, convID: s.convID, mode: s.mode, updated: s.updatedAt,
			web: store.WebState{
				Turn: key.turn, TurnTimings: slices.Clone(s.turnTimings), RequestID: key.reqID, RequestStatus: key.reqStatus, CommandResult: commandResult, CommandSubmissions: json.RawMessage(key.commandLedger), UpdatedAt: s.updatedAt, Detail: s.detail,
				ProjectID: key.projectID, Model: key.model, Effort: key.effort, ContextSize: key.contextSize, Title: key.title,
				Stage: key.stage, SettledAt: key.settledAt, ArchivedAt: key.archivedAt, TerminalSession: s.terminalID, Imported: s.imported,
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

// Emit applies the event at once. A tool item's images are stored by the
// image goroutine, since storing writes files and Emit must not wait: the
// item goes out without them now and again with them once they are stored.
func (k sink) Emit(ev agentapi.Event) {
	if ev.Kind == agentapi.EventItem && ev.Item != nil && ev.Item.Kind == agentapi.ItemTool && len(ev.Item.Images) > 0 {
		it := *ev.Item
		images := it.Images
		it.Images, it.ImagesNote = nil, ""
		ev.Item = &it
		k.m.handleEvent(k.s, k.gen, ev)
		k.m.queueImages(imageJob{s: k.s, gen: k.gen, agentID: it.AgentID, itemID: it.ID, images: images})
		return
	}
	k.m.handleEvent(k.s, k.gen, ev)
}

// imageJob is one tool item's images waiting to be stored.
type imageJob struct {
	s               *webSession
	gen             uint64
	agentID, itemID string
	images          []agentapi.Image
}

// maxImageJobs bounds the images waiting to be stored. Past it, a tool
// item's images are left out with a note, as an over-cap image would be.
const maxImageJobs = 256

func (m *Manager) queueImages(job imageJob) {
	m.imageWG.Add(1)
	select {
	case m.imageJobs <- job:
	default:
		m.imageWG.Done()
		log.Warn("tool images dropped: too many waiting to be stored", "session", job.s.id, "count", len(job.images))
		m.applyImages(job, nil, imagesNote(len(job.images), "too many images arrived at once"))
	}
}

// imageLoop stores queued tool images one job at a time until the manager
// stops; jobs still queued then are released unstored.
func (m *Manager) imageLoop() {
	defer m.wg.Done()
	for {
		select {
		case <-m.ctx.Done():
			for {
				select {
				case <-m.imageJobs:
					m.imageWG.Done()
				default:
					return
				}
			}
		case job := <-m.imageJobs:
			m.storeImages(job)
			m.imageWG.Done()
		}
	}
}

// storeImages stores one job's images and puts them on their item, unless
// the conversation they came from is gone.
func (m *Manager) storeImages(job imageJob) {
	m.mu.Lock()
	stale := job.s.gen != job.gen || m.closed
	m.mu.Unlock()
	if stale {
		return
	}
	if m.storeImageHook != nil {
		m.storeImageHook()
	}
	it := agentapi.Item{Images: job.images}
	m.keepImages(job.s, &it)
	m.applyImages(job, it.Images, it.ImagesNote)
}

// applyImages puts stored images on the item they belong to and publishes
// it again, when that item and its conversation are still current.
func (m *Manager) applyImages(job imageJob, images []agentapi.Image, note string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := job.s
	if s.gen != job.gen || m.closed {
		return
	}
	i, ok := s.itemIdx[itemKey(job.agentID, job.itemID)]
	if !ok {
		return
	}
	it := s.items[i]
	it.Images, it.ImagesNote = images, note
	m.upsertItemLocked(s, clampItem(it, m.now()), true)
}

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
			it := clampItem(*ev.Item, m.now())
			s.linkUploadsLocked(&it)
			m.linkTurnTimingLocked(s, it)
			m.upsertItemLocked(s, it, true)
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
			m.upsertSubagentLocked(s, *ev.Subagent, true)
		}
	case agentapi.EventExecution:
		if ev.Execution != nil {
			state := *ev.Execution
			s.execution = &state
		}
	case agentapi.EventBackgroundTasks:
		if ev.BackgroundTasks != nil {
			m.backgroundTasksLocked(s, *ev.BackgroundTasks)
		}
	case agentapi.EventContext:
		if ev.Context != nil && ev.Context.Used >= 0 && ev.Context.Limit > 0 {
			usage := *ev.Context
			if usage.Cached < 0 || usage.Cached > usage.Prompt {
				usage.Prompt, usage.Cached = 0, 0
			}
			if s.context == nil || *s.context != usage {
				s.context = &usage
			}
		}
	case agentapi.EventUsage:
		m.applyUsageLocked(s, ev.Usage)
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
		m.forgetBackgroundTaskStateLocked(s)
		m.pauseQueueLocked(s)
		s.setBase(StateFailed, detail)
		log.Warn("web provider conversation exited", "session", s.id, "provider", s.provider)
	}
	m.changedLocked(s, before)
}

func (m *Manager) applyTurnLocked(s *webSession, turn agentapi.Turn) {
	m.observeTurnTimingLocked(s, turn.State)
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
	if turn.State != agentapi.TurnWorking {
		// The agent may have switched branches during the turn.
		m.kickBranchLocked(s.projectID)
		m.kickQuotaLocked(s.provider)
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
	// A new conversation has no earlier record: everything streams in.
	s.history = HistoryLoaded
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
	if err := m.register(s, conv, rec, nil); err != nil {
		m.closeConversation(conv)
		return SessionSummary{}, err
	}
	log.Info("web session created", "session", id, "provider", prov.Name())
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
	s.persisted = persistKey{turn: rec.Web.Turn, name: rec.Name, convID: rec.ProviderSessionID, projectID: s.projectID, model: s.model, effort: s.effort, contextSize: s.contextSize, title: rec.Web.Title, mode: s.mode}
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
		// A viewer told the transcript was loading gets it read instead.
		m.viewHistoryLocked(s)
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
	m.cancelHistoryLocked(s)
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
	var histErr error
	if err == nil && withHistory {
		if history, histErr = conv.History(ctx); histErr != nil {
			log.Warn("read web conversation history failed", "session", s.id, "error", histErr)
		}
		// Only a conversation that is still the current one stores images.
		m.mu.Lock()
		current := s.gen == gen && !m.closed
		m.mu.Unlock()
		for i := range history.Items {
			if current && history.Items[i].Kind == agentapi.ItemTool {
				m.keepImages(s, &history.Items[i])
			}
		}
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
		m.applyHistoryLocked(s, history, false)
		m.openedHistoryLocked(s, withHistory, histErr)
		m.autoAllowPendingLocked(s)
	case err != nil && s.gen == gen:
		s.setBase(StateFailed, openFailureDetail(err, s.convID))
	}
	m.finishOpeningLocked(s)
	if err != nil && s.history == "" {
		// A viewer may be waiting for the transcript this open was to read.
		m.viewHistoryLocked(s)
	}
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

// turnInput is what a submission sends: a prompt, or, when command is set,
// that command with text as its arguments. Files are relative project
// paths; attachments are upload IDs of the Task.
type turnInput struct {
	text, command      string
	files, attachments []string
}

// Submit sends one prompt in mode: ModeSend ("" too), ModeQueue or
// ModeSteer. A repeated request ID returns the recorded outcome, or "queued"
// while the prompt waits in the queue, without contacting the provider.
func (m *Manager) Submit(id string, req PromptRequest) (Submission, error) {
	if !validRequestID(req.RequestID) {
		return Submission{}, newError(http.StatusBadRequest, "request_id must be a UUID")
	}
	mode := req.Mode
	switch mode {
	case "":
		mode = ModeSend
	case ModeSend, ModeQueue, ModeSteer:
	default:
		return Submission{}, newError(http.StatusBadRequest, "mode must be send, queue or steer")
	}
	if strings.TrimSpace(req.Text) == "" {
		return Submission{}, newError(http.StatusBadRequest, "prompt text is required")
	}
	if len(req.Text) > maxPromptBytes {
		return Submission{}, newError(http.StatusRequestEntityTooLarge, "prompt is too large")
	}
	s, err := m.lookup(id)
	if err != nil {
		return Submission{}, err
	}
	return m.submit(s, turnInput{text: req.Text, files: req.Files, attachments: req.Attachments}, req.RequestID, mode)
}

// Command runs one of the provider's listed commands. It follows the rules
// of a send: a repeated request ID returns the recorded outcome, a running
// turn refuses it, and there is no queue or steer.
func (m *Manager) Command(id string, req CommandRequest) (Submission, error) {
	if !validRequestID(req.RequestID) {
		return Submission{}, newError(http.StatusBadRequest, "request_id must be a UUID")
	}
	if !validCommandName(req.Name) {
		return Submission{}, newError(http.StatusBadRequest, "name must be a command name without the slash or spaces")
	}
	if len(req.Arguments) > maxPromptBytes {
		return Submission{}, newError(http.StatusRequestEntityTooLarge, "arguments are too large")
	}
	s, err := m.lookup(id)
	if err != nil {
		return Submission{}, err
	}
	m.mu.Lock()
	provider := s.provider
	m.mu.Unlock()
	// OpenCode puts the arguments into the command's template and then runs
	// every !`cmd` in the result as a shell command, without asking.
	if provider == agentapi.ProviderOpenCode && strings.Contains(req.Arguments, "!`") {
		return Submission{}, newError(http.StatusBadRequest, "command arguments must not contain !`")
	}
	if provider == agentapi.ProviderCopilot {
		return m.executeCommand(s, req)
	}
	return m.submit(s, turnInput{text: req.Arguments, command: req.Name, files: req.Files, attachments: req.Attachments}, req.RequestID, ModeSend)
}

const maxCommandName = 200

func validCommandName(name string) bool {
	if name == "" || len(name) > maxCommandName || strings.HasPrefix(name, "/") || !utf8.ValidString(name) {
		return false
	}
	return !strings.ContainsFunc(name, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
}

// Commands lists the live catalogue, reopening only the exact conversation
// of an active Task. Discovery never submits a prompt.
func (m *Manager) Commands(ctx context.Context, id string) ([]agentapi.Command, error) {
	s, err := m.lookup(id)
	if err != nil {
		return nil, err
	}
	s.op.Lock()
	defer s.op.Unlock()
	m.mu.Lock()
	err = s.readOnlyLocked()
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if err = m.checkHolder(s); err != nil {
		return nil, err
	}
	if err = m.openLocked(s, true); err != nil {
		return nil, err
	}
	m.mu.Lock()
	conv, state := s.conv, s.state()
	m.mu.Unlock()
	if conv == nil {
		return nil, newError(http.StatusConflict, "the provider conversation is not open")
	}
	commands, err := m.listCommands(conv)
	if errors.Is(err, agentapi.ErrUnsupported) {
		return []agentapi.Command{}, nil
	}
	for i := range commands {
		if turnRunning(state) && !commands[i].AllowDuringTurn && commands[i].DisabledReason == "" {
			commands[i].DisabledReason = "Wait for the active turn to finish"
		}
	}
	return commands, err
}

// listCommands reads conv's commands, made safe to show. A name a user
// could not type after a slash is left out.
func (m *Manager) listCommands(conv agentapi.Conversation) ([]agentapi.Command, error) {
	ctx, cancel := context.WithTimeout(m.ctx, controlTimeout)
	defer cancel()
	listed, err := conv.Commands(ctx)
	if errors.Is(err, agentapi.ErrUnsupported) {
		return nil, err
	}
	if err != nil {
		return nil, newError(http.StatusBadGateway, "could not list the provider's commands: %s", shortError(err))
	}
	out := make([]agentapi.Command, 0, len(listed))
	for _, c := range listed {
		if !validCommandName(c.Name) || slices.ContainsFunc(out, func(o agentapi.Command) bool { return o.Name == c.Name }) {
			continue
		}
		if c.Kind != agentapi.CommandSkill {
			c.Kind = agentapi.CommandPrompt
		}
		c.Description = clipRunes(strings.TrimSpace(displaytext.Sanitize(c.Description)), maxDetailRunes)
		c.InputHint = clipRunes(strings.TrimSpace(displaytext.Sanitize(c.InputHint)), maxDetailRunes)
		c.Aliases = slices.DeleteFunc(slices.Clone(c.Aliases), func(alias string) bool { return !validCommandName(alias) })
		c.DisabledReason = clipRunes(displaytext.Sanitize(c.DisabledReason), maxDetailRunes)
		out = append(out, c)
	}
	return out, nil
}

// commandOffered checks name against conv's listed commands.
func (m *Manager) commandOffered(conv agentapi.Conversation, name string) error {
	commands, err := m.listCommands(conv)
	switch {
	case errors.Is(err, agentapi.ErrUnsupported):
		return newError(http.StatusConflict, "this provider has no commands")
	case err != nil:
		return err
	case !slices.ContainsFunc(commands, func(c agentapi.Command) bool { return c.Name == name }):
		return newError(http.StatusNotFound, "/%s is not one of this task's commands", name)
	}
	return nil
}

var errTurnRunning = newError(http.StatusConflict, "a turn is already running in this session")

// turnRunning reports whether state is a running turn: working or waiting for
// the user. Opening a conversation (starting) is not a turn.
func turnRunning(state string) bool {
	return state == StateWorking || state == StateAwaitingPermission || state == StateAwaitingAnswer
}

func (m *Manager) submit(s *webSession, in turnInput, reqID, mode string) (Submission, error) {
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
	workdir := s.workdir
	m.mu.Unlock()
	// Checked before anything is queued or sent; a queued prompt's files are
	// checked again when it is sent.
	if _, err := checkFiles(workdir, in.files); err != nil {
		return Submission{}, err
	}
	m.mu.Lock()
	uploads, err := m.checkUploadsLocked(s, in.attachments)
	if err != nil {
		m.mu.Unlock()
		return Submission{}, err
	}
	state, conv := s.state(), s.conv
	switch {
	// Behind queued prompts that are about to be sent, a queued prompt waits
	// its turn even when no turn is running.
	case mode == ModeQueue && (turnRunning(state) || s.queueSending != "" || (len(s.queue) > 0 && !s.queuePaused)):
		defer m.mu.Unlock()
		return m.enqueueLocked(s, in, uploads, reqID)
	case mode == ModeSteer && turnRunning(state):
		m.mu.Unlock()
		if len(in.files) > 0 || len(uploads) > 0 {
			return Submission{}, newError(http.StatusBadRequest, "a steer takes text only; send files and attachments with a prompt")
		}
		return m.steer(s, conv, in.text, reqID)
	case busy(state) || s.queueSending != "":
		m.mu.Unlock()
		return Submission{}, errTurnRunning
	}
	m.mu.Unlock()
	return m.send(s, in, reqID)
}

// send starts a turn with in. The caller holds s.op. An error means nothing
// was sent and nothing was recorded; otherwise the outcome is recorded, and a
// prompt that was not accepted pauses the queue.
func (m *Manager) send(s *webSession, in turnInput, reqID string) (Submission, error) {
	if sub, found, err := m.checkedPrompt(s, reqID); found || err != nil {
		return sub, err
	}
	if err := m.openLocked(s, true); err != nil {
		if errors.Is(err, errShuttingDown) {
			return Submission{}, err
		}
		_, msg := errorStatus(err)
		return m.recordSubmission(s, reqID, SubmissionRejected, msg, true), nil
	}

	m.mu.Lock()
	conv, workdir := s.conv, s.workdir
	uploads, err := m.checkUploadsLocked(s, in.attachments)
	m.mu.Unlock()
	if conv == nil {
		return m.recordSubmission(s, reqID, SubmissionRejected, "the provider conversation is not open", true), nil
	}
	var files []agentapi.File
	var blobs []agentapi.Blob
	if err == nil {
		files, err = checkFiles(workdir, in.files)
	}
	if err == nil {
		blobs, err = m.readBlobs(s.id, uploads)
	}
	if err != nil {
		_, msg := errorStatus(err)
		return m.recordSubmission(s, reqID, SubmissionRejected, msg, true), nil
	}
	if in.command != "" {
		if err := m.commandOffered(conv, in.command); err != nil {
			return Submission{}, err
		}
	}

	m.mu.Lock()
	if s.conv != conv {
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
	titleModel := m.titleModelLocked(s, in)
	s.setBase(StateWorking, "")
	mark := s.turnSeq
	m.changedLocked(s, before)
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(m.ctx, sendTimeout)
	prompt := agentapi.Prompt{Text: in.text, Files: files, Attachments: blobs}
	if in.command == "" {
		err = conv.Send(ctx, prompt)
	} else {
		err = conv.RunCommand(ctx, in.command, prompt)
	}
	cancel()
	if err == nil || errors.Is(err, agentapi.ErrSubmissionUncertain) {
		m.markUsed(s, uploads)
	}
	if err == nil {
		if titleModel != "" {
			m.startTitle(s, titleModel, in.text)
		}
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
	case errors.Is(err, agentapi.ErrUnsupported):
		return Submission{}, newError(http.StatusConflict, "this provider has no commands")
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
	if sub, found, err := m.checkedPrompt(s, reqID); found || err != nil {
		return sub, err
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
	for i := len(s.commandSubmissions) - 1; i >= 0; i-- {
		if s.commandSubmissions[i].RequestID == reqID {
			return s.commandSubmissions[i], true
		}
	}
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
func (m *Manager) enqueueLocked(s *webSession, in turnInput, uploads []*upload, reqID string) (Submission, error) {
	if len(s.queue) >= maxQueue {
		return Submission{}, newError(http.StatusConflict, "the queue is full (%d prompts)", maxQueue)
	}
	before := m.summaryLocked(s)
	q := QueuedPrompt{RequestID: reqID, Text: in.text, QueuedAt: m.now(), Files: in.files, Attachments: uploadInfos(uploads)}
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
	if m.closed || s.removed || len(s.queue) == 0 || s.queuePaused || s.queueSending != "" || busy(s.state()) {
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
	in := turnInput{text: head.Text, files: head.Files}
	for _, a := range head.Attachments {
		in.attachments = append(in.attachments, a.ID)
	}
	_, err := m.send(s, in, head.RequestID)
	m.mu.Lock()
	defer m.mu.Unlock()
	s.queueSending = ""
	if err != nil {
		// Nothing was sent: a turn is running after all, the service is
		// stopping, or another client holds the conversation. The prompt waits
		// at the front for the next completed turn.
		if !m.closed && !s.removed {
			before := m.summaryLocked(s)
			s.queue = slices.Insert(s.queue, 0, head)
			s.queueChanged = true
			// The in-use check refused it: the queue waits for the user.
			if errors.Is(err, errHeldElsewhere) || errors.Is(err, errHolderUnknown) || s.base == StateClosed {
				m.pauseQueueLocked(s)
			}
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
	s, err := m.lookup(id)
	if err != nil {
		return SessionSummary{}, err
	}
	s.op.Lock()
	defer s.op.Unlock()
	m.mu.Lock()
	if err := s.readOnlyLocked(); err != nil {
		m.mu.Unlock()
		return SessionSummary{}, err
	}
	conv, state, caps := s.conv, s.state(), m.infos[s.provider].Capabilities
	if !caps.Cancel {
		m.mu.Unlock()
		return SessionSummary{}, newError(http.StatusConflict, "this provider does not support cancelling a turn")
	}
	if conv == nil || state == StateStarting || (!busy(state) && (s.execution == nil || s.execution.Mode != "autopilot")) {
		m.mu.Unlock()
		return SessionSummary{}, newError(http.StatusConflict, "no turn is running")
	}
	s.stopSeq++
	before := m.summaryLocked(s)
	m.pauseQueueLocked(s)
	m.changedLocked(s, before)
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(m.ctx, controlTimeout)
	err = conv.Cancel(ctx)
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

// CancelSubagent stops only the selected agent. Successful repeats never
// resend, and final status is supplied by the provider's subagent event.
func (m *Manager) CancelSubagent(id, agentID string) (agentapi.Subagent, error) {
	s, err := m.lookup(id)
	if err != nil {
		return agentapi.Subagent{}, err
	}
	s.op.Lock()
	defer s.op.Unlock()
	m.mu.Lock()
	sa := s.subIdx[agentID]
	switch {
	case s.removed:
		m.mu.Unlock()
		return agentapi.Subagent{}, newError(http.StatusNotFound, "session not found")
	case sa == nil:
		m.mu.Unlock()
		return agentapi.Subagent{}, newError(http.StatusNotFound, "subagent not found")
	case sa.Status.Terminal(), s.stoppedSubagents[agentID]:
		result := *sa
		m.mu.Unlock()
		return result, nil
	case m.closed || s.stage != StageActive || s.conv == nil || s.state() == StateStarting || sa.Status != agentapi.SubagentRunning:
		m.mu.Unlock()
		return agentapi.Subagent{}, newError(http.StatusConflict, "the subagent has no active conversation")
	}
	conv := s.conv
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(m.ctx, controlTimeout)
	err = conv.CancelSubagent(ctx, agentID)
	cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	// Completion can race a provider rejection because the agent already
	// finished. An accepted stop still expires its abandoned interactions.
	if err != nil && sa.Status.Terminal() {
		return *sa, nil
	}
	switch {
	case err == nil:
		before := m.summaryLocked(s)
		if s.stoppedSubagents == nil {
			s.stoppedSubagents = map[string]bool{}
		}
		s.stoppedSubagents[agentID] = true
		m.expireSubagentLocked(s, agentID)
		m.changedLocked(s, before)
		return *sa, nil
	case errors.Is(err, agentapi.ErrUnsupported):
		return agentapi.Subagent{}, newError(http.StatusConflict, "this provider cannot stop a subagent")
	case errors.Is(err, agentapi.ErrClosed):
		return agentapi.Subagent{}, newError(http.StatusConflict, "the provider conversation is closed")
	default:
		return agentapi.Subagent{}, newError(http.StatusBadGateway, "could not stop subagent: %s", shortError(err))
	}
}

// PromptSubagent sends text to one idle subagent of an active Task whose
// conversation is open and runs no turn. The follow-up is not a Task turn:
// the Task's state, queue and last submission stay as they are, and the
// subagent's status arrives through its events. A repeated request ID
// returns the recorded outcome without contacting the provider.
func (m *Manager) PromptSubagent(id, agentID, text, requestID string) (Submission, error) {
	if !validRequestID(requestID) {
		return Submission{}, newError(http.StatusBadRequest, "request_id must be a UUID")
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
	s.op.Lock()
	defer s.op.Unlock()
	m.mu.Lock()
	if i := slices.IndexFunc(s.subagentPrompts, func(p Submission) bool { return p.RequestID == requestID }); i >= 0 {
		sub := s.subagentPrompts[i]
		m.mu.Unlock()
		return sub, nil
	}
	m.mu.Unlock()
	holderErr := m.checkHolder(s)
	m.mu.Lock()
	// Another request may have finished while the holder RPC released s.op.
	if i := slices.IndexFunc(s.subagentPrompts, func(p Submission) bool { return p.RequestID == requestID }); i >= 0 {
		sub := s.subagentPrompts[i]
		m.mu.Unlock()
		return sub, nil
	}
	if holderErr != nil {
		m.mu.Unlock()
		return Submission{}, holderErr
	}
	sa := s.subIdx[agentID]
	switch {
	case s.removed:
		err = newError(http.StatusNotFound, "session not found")
	case sa == nil:
		err = newError(http.StatusNotFound, "subagent not found")
	case m.closed:
		err = errShuttingDown
	case s.stage != StageActive:
		err = s.readOnlyLocked()
	case s.conv == nil:
		err = newError(http.StatusConflict, "the provider conversation is not open")
	case busy(s.state()):
		err = newError(http.StatusConflict, "the task is running or waiting for input; a subagent takes a follow-up only while the task is idle")
	case sa.Status == agentapi.SubagentRunning:
		err = newError(http.StatusConflict, "the subagent is still running; it takes a follow-up once it is idle")
	case sa.Status != agentapi.SubagentIdle:
		err = newError(http.StatusConflict, "the subagent does not take follow-ups (%s)", sa.Status)
	}
	conv := s.conv
	m.mu.Unlock()
	if err != nil {
		return Submission{}, err
	}
	ctx, cancel := context.WithTimeout(m.ctx, sendTimeout)
	err = conv.PromptSubagent(ctx, agentID, text)
	cancel()
	switch {
	case err == nil:
		return m.recordSubagentPrompt(s, requestID, SubmissionAccepted, ""), nil
	case errors.Is(err, agentapi.ErrUnsupported):
		return Submission{}, newError(http.StatusConflict, "this provider cannot chat with a subagent")
	case errors.Is(err, agentapi.ErrClosed):
		return Submission{}, newError(http.StatusConflict, "the provider conversation is closed")
	}
	log.Warn("web subagent follow-up failed", "session", s.id, "error", err)
	if errors.Is(err, agentapi.ErrSubmissionUncertain) {
		// Never resend: the subagent may already be working on it.
		return m.recordSubagentPrompt(s, requestID, SubmissionUncertain,
			"the provider may or may not have received this follow-up, and uam did not resend it: "+shortError(err)), nil
	}
	return m.recordSubagentPrompt(s, requestID, SubmissionRejected, shortError(err)), nil
}

// recordSubagentPrompt keeps a follow-up's outcome for repeated request IDs.
// Nothing else changes: the subagent's own events report what it does.
func (m *Manager) recordSubagentPrompt(s *webSession, reqID, status, msg string) Submission {
	sub := Submission{RequestID: reqID, Status: status, Error: msg, Time: m.now()}
	m.mu.Lock()
	defer m.mu.Unlock()
	s.subagentPrompts = append(s.subagentPrompts, sub)
	if len(s.subagentPrompts) > maxSubmissions {
		s.subagentPrompts = append([]Submission(nil), s.subagentPrompts[len(s.subagentPrompts)-maxSubmissions:]...)
	}
	return sub
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
	m.forgetBackgroundTaskStateLocked(s)
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
	case len(s.queue) > 0 || s.queueSending != "":
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
		return SessionSummary{}, newError(http.StatusNotFound, "session not found")
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
	return m.setModelLocked(s, model, effort, contextSize)
}

// The caller holds s.op, shared by command invocation and Stop.
func (m *Manager) setModelLocked(s *webSession, model, effort, contextSize *string) (SessionSummary, error) {
	id := s.id
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
		m.forgetBackgroundTaskStateLocked(s)
		// The queue lives in memory only; the prompts in it are not sent.
		s.queue, s.queuePaused = nil, false
		m.changedLocked(s, before)
	}
	for sub := range m.subs {
		m.dropLocked(sub)
	}
	m.mu.Unlock()
	// Abort in-flight opens, sends and title jobs; their outcome is
	// recorded as usual. Title jobs delete their throwaway conversations
	// before the providers stop.
	m.cancel()
	wait(ctx, &m.titles)
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
	wait(ctx, &m.wg)
	if err := m.flush(); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("persist web sessions: %w", err)
	}
	return firstErr
}

// wait waits for wg until ctx ends.
func wait(ctx context.Context, wg *sync.WaitGroup) {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
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
