package web

import (
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
	recentWorkdirsN = 10
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

	mu       sync.Mutex
	infos    map[string]ProviderInfo
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
	id        string
	provider  string
	name      string
	workdir   string
	convID    string
	createdAt time.Time
	updatedAt time.Time
	base      string
	detail    string
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

	submissions []Submission
	last        *Submission
	createReq   string

	persisted persistKey
}

type interaction struct {
	agentapi.Interaction
	// answering is set while this service's answer is with the provider, so
	// a concurrent second answer is refused instead of racing it.
	answering bool
}

// persistKey is the durable part of a session; sessions.json is written only
// when it changes, never per streamed token.
type persistKey struct {
	turn, detail, name, convID, reqID, reqStatus string
}

func newSession(id, provider, name, workdir, convID string, created time.Time) *webSession {
	return &webSession{
		id: id, provider: provider, name: name, workdir: workdir, convID: convID,
		createdAt: created, updatedAt: created, base: StateIdle,
		itemIdx: map[string]int{}, ixIdx: map[string]*interaction{},
	}
}

func (s *webSession) setBase(state, detail string) {
	s.base = state
	s.detail = detail
	s.turnSeq++
}

func (s *webSession) pendingKinds() (permissions, questions int) {
	for _, ix := range s.interactions {
		if ix.State != agentapi.InteractionPending {
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
	k := persistKey{turn: s.durableState(), detail: s.detail, name: s.name, convID: s.convID}
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

// Start checks providers and loads the web records. A provider whose check
// fails is listed as unavailable; it is not fatal.
func (m *Manager) Start(ctx context.Context) error {
	infos := m.checkProviders(ctx)
	cfg, err := m.store.Load()
	if err != nil {
		return fmt.Errorf("load web sessions: %w", err)
	}
	m.mu.Lock()
	m.infos = infos
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

func sessionFromRecord(rec store.SessionRecord) *webSession {
	s := newSession(rec.ID, rec.Agent, rec.Name, rec.Workdir, rec.ProviderSessionID, rec.CreatedAt)
	s.updatedAt = rec.LastSeenAt
	if web := rec.Web; web != nil {
		if knownStates[web.Turn] {
			s.base = web.Turn
		}
		s.detail = web.Detail
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
			info := ProviderInfo{Name: p.Name(), DisplayName: p.DisplayName(), Capabilities: p.Capabilities(), Available: true}
			checkCtx, cancel := context.WithTimeout(ctx, checkTimeout)
			err := p.Check(checkCtx)
			cancel()
			if err != nil {
				info.Available = false
				info.Reason = shortError(err)
				log.Warn("web provider unavailable", "provider", name, "error", err)
			}
			mu.Lock()
			infos[name] = info
			mu.Unlock()
		}()
	}
	wg.Wait()
	return infos
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
		ID: s.id, Provider: s.provider, Name: s.name, Workdir: s.workdir, ConversationID: s.convID,
		State: s.state(), StateDetail: s.detail, Open: s.conv != nil, Pending: permissions + questions,
		CreatedAt: s.createdAt, UpdatedAt: s.updatedAt, Capabilities: m.infos[s.provider].Capabilities,
	}
}

func (m *Manager) detailLocked(s *webSession) SessionDetail {
	d := SessionDetail{
		SessionSummary:   m.summaryLocked(s),
		Items:            append(make([]agentapi.Item, 0, len(s.items)), s.items...),
		Interactions:     make([]agentapi.Interaction, 0, len(s.interactions)),
		HistoryTruncated: s.truncated,
	}
	for _, ix := range s.interactions {
		d.Interactions = append(d.Interactions, ix.Interaction)
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

// changedLocked publishes s's summary when it changed since before and
// schedules a sessions.json write when its durable part changed.
func (m *Manager) changedLocked(s *webSession, before SessionSummary) {
	after := m.summaryLocked(s)
	after.UpdatedAt = before.UpdatedAt
	key := s.key()
	durable := key != s.persisted
	if after == before && !durable {
		return
	}
	s.updatedAt = m.now()
	if m.sessions[s.id] != s {
		// Not yet (or no longer) listed; Create publishes it once registered.
		return
	}
	summary := m.summaryLocked(s)
	m.broadcastLocked("session", "", func(seq uint64) any { return sessionEvent{Seq: seq, Session: summary} })
	if durable {
		s.persisted = key
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
			id: s.id, provider: s.provider, name: s.name, convID: s.convID, updated: s.updatedAt,
			web: store.WebState{Turn: key.turn, RequestID: key.reqID, RequestStatus: key.reqStatus, UpdatedAt: s.updatedAt, Detail: s.detail},
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
			rec.ProviderSessionID = p.convID
			rec.LastSeenAt = p.updated
			web := p.web
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
		s.setBase(StateFailed, detail)
		log.Warn("web provider conversation exited", "session", s.id, "provider", s.provider)
	}
	m.changedLocked(s, before)
}

func (m *Manager) applyTurnLocked(s *webSession, turn agentapi.Turn) {
	switch turn.State {
	case agentapi.TurnWorking:
		s.setBase(StateWorking, "")
	case agentapi.TurnCompleted:
		s.setBase(StateCompleted, "")
	case agentapi.TurnCancelled:
		s.setBase(StateCancelled, "")
	case agentapi.TurnFailed:
		detail := "the provider reported that the turn failed"
		if turn.Error != "" {
			detail = clipRunes(displaytext.Sanitize(turn.Error), maxDetailRunes)
		}
		s.setBase(StateFailed, detail)
	}
}

// CreateRequest is the POST /api/sessions body.
type CreateRequest struct {
	Provider  string `json:"provider"`
	Workdir   string `json:"workdir"`
	Name      string `json:"name"`
	Prompt    string `json:"prompt"`
	RequestID string `json:"request_id"`
}

// Create opens a new provider conversation, records the session, and, when a
// prompt is given, submits it through the same path as Submit.
func (m *Manager) Create(req CreateRequest) (SessionSummary, error) {
	prov, err := m.availableProvider(req.Provider)
	if err != nil {
		return SessionSummary{}, err
	}
	workdir, err := canonicalWorkdir(req.Workdir)
	if err != nil {
		return SessionSummary{}, err
	}
	name, err := cleanName(req.Name, filepath.Base(workdir))
	if err != nil {
		return SessionSummary{}, err
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
	s.createReq = reqID
	s.gen = 1
	ctx, cancel := context.WithTimeout(m.ctx, openTimeout)
	conv, err := prov.Open(ctx, agentapi.OpenRequest{SessionID: id, Workdir: workdir, Title: name, Events: sink{m: m, s: s, gen: 1}})
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
		ID: id, Agent: prov.Name(), Name: name, Mode: store.ModeSafe, Workdir: workdir,
		CreatedAt: now, LastSeenAt: now, Status: store.StatusActive, Surface: store.SurfaceWeb,
		ProviderSessionID: convID, Web: &store.WebState{Turn: StateIdle, UpdatedAt: now},
	}
	if err := m.store.Update(func(cfg *store.Config) error {
		if !cfg.PutSession(store.Key(rec.Agent, rec.ID), rec) {
			return errors.New("session id collides with an existing record")
		}
		return nil
	}); err != nil {
		m.closeConversation(conv)
		return SessionSummary{}, fmt.Errorf("save web session: %w", err)
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		m.closeConversation(conv)
		return SessionSummary{}, errShuttingDown
	}
	s.convID = convID
	s.conv = conv
	s.persisted = persistKey{turn: StateIdle, name: name, convID: convID}
	m.sessions[id] = s
	// Announce the new session. Events that arrived during Open may have
	// changed its state before the record existed; this also persists that.
	m.changedLocked(s, SessionSummary{})
	m.mu.Unlock()
	log.Info("web session created", "session", id, "provider", prov.Name())
	if hasPrompt {
		if _, err := m.submit(s, req.Prompt, reqID); err != nil {
			log.Warn("initial web prompt not submitted", "session", id, "error", err)
		}
	}
	return m.Summary(id)
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
// conversation. Failed and closed sessions stay as they are until the user
// acts on them, so their reported outcome is not replaced by a page load.
func (m *Manager) autoOpenableLocked(s *webSession) bool {
	return s.conv == nil && s.convID != "" && s.base != StateFailed && s.base != StateClosed &&
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
	m.changedLocked(s, before)
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(m.ctx, openTimeout)
	defer cancel()
	conv, err := prov.Open(ctx, req)
	if err == nil && conv.ID() != req.ConversationID {
		// Exactness is the contract: a different conversation is a failure,
		// not a substitute.
		m.closeConversation(conv)
		err = fmt.Errorf("provider opened conversation %q instead of %q", conv.ID(), req.ConversationID)
	}
	var history []agentapi.Item
	if err == nil && withHistory {
		items, histErr := conv.History(ctx)
		if histErr != nil {
			log.Warn("read web conversation history failed", "session", s.id, "error", histErr)
		}
		history = items
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

// Submit sends one prompt. A repeated request ID returns the recorded
// outcome without contacting the provider.
func (m *Manager) Submit(id, text, requestID string) (Submission, error) {
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
	return m.submit(s, text, requestID)
}

var errTurnRunning = newError(http.StatusConflict, "a turn is already running in this session")

func (m *Manager) submit(s *webSession, text, reqID string) (Submission, error) {
	s.op.Lock()
	defer s.op.Unlock()
	m.mu.Lock()
	if sub, ok := s.findSubmission(reqID); ok {
		m.mu.Unlock()
		return sub, nil
	}
	if m.closed {
		m.mu.Unlock()
		return Submission{}, errShuttingDown
	}
	if busy(s.state()) {
		m.mu.Unlock()
		return Submission{}, errTurnRunning
	}
	m.mu.Unlock()

	if err := m.openLocked(s, true); err != nil {
		if errors.Is(err, errShuttingDown) {
			return Submission{}, err
		}
		_, msg := errorStatus(err)
		return m.recordSubmission(s, reqID, SubmissionRejected, msg), nil
	}

	m.mu.Lock()
	conv := s.conv
	if conv == nil {
		m.mu.Unlock()
		return m.recordSubmission(s, reqID, SubmissionRejected, "the provider conversation is not open"), nil
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
		return m.recordSubmission(s, reqID, SubmissionAccepted, ""), nil
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
			"the provider may or may not have received this prompt, and uam did not resend it: "+shortError(err)), nil
	default:
		return m.recordSubmission(s, reqID, SubmissionRejected, shortError(err)), nil
	}
}

func (s *webSession) findSubmission(reqID string) (Submission, bool) {
	for i := len(s.submissions) - 1; i >= 0; i-- {
		if s.submissions[i].RequestID == reqID {
			return s.submissions[i], true
		}
	}
	return Submission{}, false
}

func (s *webSession) hasSubmission(reqID string) bool {
	_, ok := s.findSubmission(reqID)
	return ok
}

func (m *Manager) recordSubmission(s *webSession, reqID, status, msg string) Submission {
	sub := Submission{RequestID: reqID, Status: status, Error: msg, Time: m.now()}
	m.mu.Lock()
	before := m.summaryLocked(s)
	s.submissions = append(s.submissions, sub)
	if len(s.submissions) > maxSubmissions {
		s.submissions = append([]Submission(nil), s.submissions[len(s.submissions)-maxSubmissions:]...)
	}
	last := sub
	s.last = &last
	m.broadcastLocked("submission", s.id, func(seq uint64) any {
		return submissionEvent{Seq: seq, SessionID: s.id, Submission: sub}
	})
	m.changedLocked(s, before)
	m.mu.Unlock()
	// The request ID must be durable before the browser hears the outcome,
	// so a restarted service still recognizes a retry.
	if err := m.flush(); err != nil {
		log.Warn("persist web submission failed", "session", s.id, "error", err)
	}
	return sub
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
	m.mu.Unlock()
	if !caps.Cancel {
		return SessionSummary{}, newError(http.StatusConflict, "this provider does not support cancelling a turn")
	}
	if conv == nil || state == StateStarting || !busy(state) {
		return SessionSummary{}, newError(http.StatusConflict, "no turn is running")
	}
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
	before := m.summaryLocked(s)
	conv := s.conv
	s.conv = nil
	s.gen++
	m.expirePendingLocked(s, "the session was closed")
	s.setBase(StateClosed, "")
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

// Rename changes the session's display name.
func (m *Manager) Rename(id, name string) (SessionSummary, error) {
	clean, err := cleanName(name, "")
	if err != nil {
		return SessionSummary{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return SessionSummary{}, newError(http.StatusNotFound, "session not found")
	}
	before := m.summaryLocked(s)
	s.name = clean
	m.changedLocked(s, before)
	return m.summaryLocked(s), nil
}

// Answer forwards the user's answer to a pending interaction. The first
// answer wins; the service never answers on the user's behalf.
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

	ctx, cancel := context.WithTimeout(m.ctx, controlTimeout)
	err := conv.Respond(ctx, interactionID, answer)
	cancel()

	m.mu.Lock()
	defer m.mu.Unlock()
	ix.answering = false
	before := m.summaryLocked(s)
	switch {
	case err == nil:
		if ix.State == agentapi.InteractionPending {
			ix.State, ix.Resolution = resolution(ix.Interaction, answer)
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
		log.Warn("web interaction answer failed", "session", id, "error", err)
		return agentapi.Interaction{}, newError(http.StatusBadGateway, "answer failed: %s", shortError(err))
	}
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
		return "", newError(http.StatusBadRequest, "workdir is required")
	}
	if !filepath.IsAbs(p) {
		return "", newError(http.StatusBadRequest, "workdir must be an absolute path")
	}
	for _, r := range p {
		if unicode.IsControl(r) {
			return "", newError(http.StatusBadRequest, "workdir contains control characters")
		}
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", newError(http.StatusBadRequest, "workdir does not exist")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", newError(http.StatusBadRequest, "workdir is not a directory")
	}
	return filepath.Clean(resolved), nil
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
