package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/compat"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agents"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/session"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/version"
)

type Model struct {
	width, height       int
	sizeKnown           bool
	quitting            bool
	loading             bool
	hasLoaded           bool
	activity            spinner.Model
	service             *Service
	sessions            []adapter.Session
	selected            int
	input               string
	filterActive        bool
	filterQuery         string
	filterRestore       sessionIdentity
	filterSaved         bool
	defaultAgent        string
	message             string
	messageSetAt        time.Time
	refreshError        string
	helpOpen            bool
	darkBackground      bool
	confirmStop         bool
	confirmStopID       string
	confirmStopAgent    string
	confirmLatest       bool
	confirmLatestAgent  string
	confirmLatestID     string
	confirmLatestName   string
	confirmLatestAction latestAction
	confirmForm         *huh.Form
	confirmField        *huh.Confirm
	confirmValue        *bool
	confirmIntent       confirmationIntent
	confirmName         string
	confirmSignature    string
	confirmAffirmative  string
	confirmGeneration   uint64
	renaming            bool
	renameTargetID      string
	renameTargetAgent   string
	wizard              bool
	wizardStep          int
	wizardAgent         string
	wizardAgentExplicit bool
	wizardProfile       string
	wizardAlias         string
	wizardCwd           string
	profileNames        []string
	profileProviders    map[string]string
	defaultProfile      string
	profileBySession    map[sessionIdentity]string
	lastSeenBySession   map[sessionIdentity]time.Time
	groupByDir          bool
	execProcess         func(*exec.Cmd, tea.ExecCallback) tea.Cmd
	// reorderSeq increments on every reorder; a debounced flush tick only
	// persists when its seq still matches, so a held Shift+arrow coalesces into
	// one store write instead of one fsync per step. reorderPending marks a
	// scheduled-but-not-yet-flushed reorder so quit can flush it (F59).
	reorderSeq     int
	reorderPending bool
	reorderDirty   map[sessionIdentity]struct{}
	// Test seams for proving persistence/reload sequencing without timing a
	// filesystem lock. Production leaves both nil and uses Service directly.
	persistSortIndices func([]adapter.Session) error
	reloadSessions     func() sessionsLoadedMsg
	groupToggle        *groupToggleCoordinator
	// now is the presentation clock used for deterministic session-age labels.
	// Discovery refreshes LastChange on every scan, so the dashboard deliberately
	// derives age from CreatedAt instead.
	now func() time.Time
	// dashboardRevision invalidates pointer intent captured by a previously
	// displayed frame after a resize. Refreshes do not bump it: the frame
	// signature already covers the visible roster, and Bubble Tea keeps the
	// displayed view's OnMouse callback until the content changes, so a
	// counter that moves on a no-op refresh would reject every click that
	// follows one.
	dashboardRevision     uint64
	sessionLoads          *sessionLoadCoordinator
	appliedLoadGeneration uint64
}

// messageTTL is how long a status/error line stays on screen before a refresh
// tick clears it. A just-emitted message must survive at least one 2s tick, so
// the TTL is several ticks long (F53).
const messageTTL = 8 * time.Second

// reorderDebounce is how long a reorder waits for a follow-up move before it
// persists. A held Shift+arrow fires a move per repeat; without the debounce
// each one is a whole-file JSON encode + fsync + rename. Coalescing them into a
// single write after the keystrokes settle keeps the store off the hot path
// (F59).
const reorderDebounce = 500 * time.Millisecond

type sessionsLoadedMsg struct {
	refresh           bool
	loadGeneration    uint64
	sessions          []adapter.Session
	defaultAgent      string
	groupByDir        bool
	profileNames      []string
	profileProviders  map[string]string
	defaultProfile    string
	profileBySession  map[sessionIdentity]string
	lastSeenBySession map[sessionIdentity]time.Time
	err               error
}

type sessionLoadCoordinator struct {
	mu   sync.Mutex
	next uint64
}
type dispatchedMsg struct {
	session adapter.Session
	err     error
}
type attachSpecMsg struct {
	spec adapter.AttachSpec
	err  error
}
type attachFinishedMsg struct{ err error }
type latestAction string

const (
	latestResume  latestAction = "resume"
	latestAttach  latestAction = "attach"
	latestRestart latestAction = "restart"
)

type latestRequiredMsg struct {
	action latestAction
	agent  string
	id     string
	name   string
	err    error
}
type refreshMsg time.Time
type prRefreshMsg time.Time
type prRefreshedMsg struct{ err error }

// reorderFlushMsg is the debounced reorder-persist tick. It carries the seq of
// the reorder that scheduled it; the handler persists only when the seq still
// matches the latest move, dropping ticks superseded by a newer move (F59).
type reorderFlushMsg struct{ seq int }

type sessionIdentity struct {
	agent string
	id    string
}

type groupToggleCoordinator struct {
	mu         sync.Mutex
	generation uint64
}

type groupToggleResultMsg struct {
	generation uint64
	grouped    bool
	loaded     sessionsLoadedMsg
	err        error
}

// promptEditedMsg carries the result of editing the wizard prompt in $EDITOR.
// The editor is launched via tea.ExecProcess (which suspends the TUI, restores
// the terminal, and resumes cleanly); when it exits this message loads the file
// contents back into the prompt buffer (C2-8).
type promptEditedMsg struct {
	text string
	err  error
}

func New() Model {
	st, err := store.Open(store.DefaultPath())
	if err != nil {
		// The TUI degrades gracefully with a nil store (nothing persists), but
		// that must not happen silently — log it so "my sessions vanished" is
		// diagnosable.
		log.Warn("open store failed; running without persistence", "error", err)
	}
	client := session.NewClient()
	// Build the registry from the single shared adapter list so the TUI and the
	// CLI service can never diverge (the old hand-rolled list here omitted
	// hermes — F14).
	reg := adapter.NewRegistryWithBackend(client, agents.Default(client))
	return NewWithDeps(st, reg)
}

func NewWithDeps(st *store.Store, reg *adapter.Registry) Model {
	m := Model{
		service: NewService(st, reg), defaultAgent: store.DefaultAgentName,
		wizardCwd: ".", profileProviders: map[string]string{},
		profileBySession: map[sessionIdentity]string{}, lastSeenBySession: map[sessionIdentity]time.Time{}, execProcess: tea.ExecProcess,
		activity:       spinner.New(spinner.WithSpinner(spinner.Line), spinner.WithStyle(brandStyle)),
		loading:        true,
		darkBackground: compat.HasDarkBackground,
		sessionLoads:   &sessionLoadCoordinator{},
	}
	// The baked-in default may not be installed; reconcile it to an
	// enabled provider so Enter-with-no-input and the prompt hint never point at
	// a disabled agent (C2-9).
	m.defaultAgent = m.validateDefaultAgent(m.defaultAgent)
	return m
}

// validateDefaultAgent returns candidate when it is an enabled agent, otherwise
// the registry's chosen default (Registry.Default falls back to the first
// enabled adapter). When nothing is enabled — or there is no registry — the
// candidate is returned unchanged so the selector degrades gracefully instead of
// panicking on a nil Default (C2-9).
func (m Model) validateDefaultAgent(candidate string) string {
	if m.service == nil || m.service.Registry == nil {
		return candidate
	}
	if _, ok := m.service.Registry.Get(candidate); ok {
		return candidate
	}
	if a := m.service.Registry.Default(candidate); a != nil {
		return a.Name()
	}
	return candidate
}
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.refreshSessionsCmd(), m.activity.Tick, refreshTick(), prRefreshTick(100*time.Millisecond), tea.RequestBackgroundColor)
}

func refreshTick() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return refreshMsg(t) })
}

func prRefreshTick(after time.Duration) tea.Cmd {
	return tea.Tick(after, func(t time.Time) tea.Msg { return prRefreshMsg(t) })
}

// refreshStep advances the refresh state machine for one tick. It always
// re-arms the ticker (the caller batches that in); it schedules a fresh
// loadSessionsCmd only when no load is in flight, marking loading=true. This
// keeps stacked ticks from overlapping loads while never stopping the ticker
// (F17). startedLoad reports whether a load was scheduled this tick.
func (m Model) refreshStep(now time.Time) (Model, bool) {
	m.expireMessage(now)
	if m.loading {
		return m, false
	}
	m.loading = true
	return m, true
}

func (m Model) loadSessionsCmd() tea.Cmd {
	return m.sessionLoadCmd(false)
}

func (m Model) refreshSessionsCmd() tea.Cmd {
	return m.sessionLoadCmd(true)
}

func (m Model) sessionLoadCmd(refresh bool) tea.Cmd {
	coordinator := m.sessionLoads
	if coordinator == nil {
		coordinator = &sessionLoadCoordinator{}
	}
	return func() tea.Msg {
		coordinator.mu.Lock()
		defer coordinator.mu.Unlock()
		coordinator.next++
		generation := coordinator.next
		if m.reloadSessions != nil {
			loaded := m.reloadSessions()
			loaded.refresh = refresh
			loaded.loadGeneration = generation
			return loaded
		}
		sessions, cfg, err := m.service.LoadSessions(context.Background())
		profileNames := make([]string, 0, len(cfg.Profiles))
		profileProviders := make(map[string]string, len(cfg.Profiles))
		for name, profile := range cfg.Profiles {
			profileNames = append(profileNames, name)
			if profile.Provider != nil {
				profileProviders[name] = *profile.Provider
			}
		}
		sort.Strings(profileNames)
		profileBySession := make(map[sessionIdentity]string, len(cfg.Sessions))
		lastSeenBySession := make(map[sessionIdentity]time.Time, len(cfg.Sessions))
		for _, record := range cfg.Sessions {
			identity := sessionIdentity{agent: record.Agent, id: record.ID}
			profileBySession[identity] = record.Profile
			lastSeenBySession[identity] = record.LastSeenAt
		}
		return sessionsLoadedMsg{refresh: refresh, loadGeneration: generation, sessions: sessions, defaultAgent: cfg.DefaultAgent, groupByDir: cfg.UI.GroupByDir, profileNames: profileNames, profileProviders: profileProviders, defaultProfile: cfg.DefaultProfile, profileBySession: profileBySession, lastSeenBySession: lastSeenBySession, err: err}
	}
}

func (m Model) refreshPRCmd() tea.Cmd {
	return func() tea.Msg {
		return prRefreshedMsg{err: m.service.RefreshPRStatuses(context.Background())}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.handleWindowSize(msg), nil
	case tea.BackgroundColorMsg:
		m.darkBackground = msg.IsDark()
		// The v2 compatibility colors resolve through this value. Keep it in
		// sync with Bubble Tea's live terminal response instead of trusting the
		// package-load probe forever.
		compat.HasDarkBackground = m.darkBackground
		if m.confirmForm != nil {
			updated, _ := m.confirmForm.Update(msg)
			m.confirmForm = updated.(*huh.Form)
		}
		return m, nil
	case dashboardPointerIntent:
		return m.handleDashboardPointer(msg)
	case spinner.TickMsg:
		activity, cmd := m.activity.Update(msg)
		m.activity = activity
		if m.loading || (!m.hasLoaded && len(m.sessions) == 0) {
			return m, cmd
		}
		return m, nil
	case refreshMsg:
		next, startedLoad := m.refreshStep(time.Time(msg))
		// The ticker is re-armed unconditionally so refreshes never stop; the
		// load is added only when one wasn't already in flight (F17).
		if startedLoad {
			return next, tea.Batch(next.refreshSessionsCmd(), next.activity.Tick, refreshTick())
		}
		return next, refreshTick()
	case prRefreshMsg:
		return m, tea.Batch(m.refreshPRCmd(), prRefreshTick(prRefreshAge))
	case prRefreshedMsg:
		if msg.err != nil {
			log.Warn("refresh pull-request statuses failed", "error", msg.err)
			return m, nil
		}
		if m.loading {
			return m, nil
		}
		m.loading = true
		return m, tea.Batch(m.refreshSessionsCmd(), m.activity.Tick)
	case reorderFlushMsg:
		// Persist only if this is the latest reorder; a superseded tick is dropped
		// so a held Shift+arrow coalesces into one write (F59).
		if msg.seq != m.reorderSeq {
			return m, nil
		}
		return m, m.flushReorder()
	case groupToggleResultMsg:
		if !m.isLatestGroupToggle(msg.generation) {
			return m, nil
		}
		if msg.err != nil {
			m.setGroupByDir(!msg.grouped)
			m.setMessage("could not save view setting: " + msg.err.Error())
			return m, nil
		}
		return m.handleSessionsLoaded(msg.loaded), nil
	case sessionsLoadedMsg:
		return m.handleSessionsLoaded(msg), nil
	case dispatchedMsg:
		return m.handleDispatched(msg)
	case attachSpecMsg:
		return m, m.execAttachSpec(msg.spec, msg.err)
	case attachFinishedMsg:
		return m.handleAttachFinished(msg), tea.Batch(m.loadSessionsCmd(), tea.ClearScreen, tea.RequestWindowSize)
	case latestRequiredMsg:
		if !errors.Is(msg.err, ErrAmbiguousResume) {
			m.setMessage(msg.err.Error())
			return m, nil
		}
		m.openLatestConfirmation(msg.action, msg.agent, msg.id, msg.name)
		return m, nil
	case confirmationFormMsg:
		return m.handleConfirmationFormMsg(msg)
	case confirmationPointerIntent:
		return m.handleConfirmationPointer(msg)
	case promptEditedMsg:
		return m.handlePromptEdited(msg), nil
	case tea.MouseMsg:
		return m.handleMouse(msg)
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.PasteMsg:
		return m.handlePaste(msg.Content), nil
	}
	return m, nil
}

func (m Model) handleWindowSize(msg tea.WindowSizeMsg) Model {
	m.width, m.height = msg.Width, msg.Height
	m.sizeKnown = true
	m.dashboardRevision++
	m.resizeConfirmation()
	return m
}

// setMessage records a status/error line and stamps the time it was set so the
// refresh tick can TTL-expire it instead of blanket-clearing a just-emitted
// message (F53).
func (m *Model) setMessage(text string) {
	m.message = text
	m.messageSetAt = time.Now()
}

// expireMessage clears the status line once it has been on screen longer than
// messageTTL. now is the refresh-tick timestamp.
func (m *Model) expireMessage(now time.Time) {
	if m.message != "" && !m.messageSetAt.IsZero() && now.Sub(m.messageSetAt) >= messageTTL {
		m.message = ""
		m.messageSetAt = time.Time{}
	}
}

func (m Model) handleSessionsLoaded(msg sessionsLoadedMsg) Model {
	if msg.loadGeneration != 0 && msg.loadGeneration < m.appliedLoadGeneration {
		if msg.refresh {
			m.loading = false
		}
		return m
	}
	if msg.loadGeneration != 0 {
		m.appliedLoadGeneration = msg.loadGeneration
	}
	if msg.refresh {
		m.hasLoaded = true
		// Only the completion of the load that acquired the guard may release it.
		m.loading = false
		if msg.err != nil {
			// Refresh failures are persistent dashboard state, not a short-lived
			// toast. Keep the last good roster and expose the explicit Retry command.
			m.refreshError = msg.err.Error()
			return m
		}
		m.refreshError = ""
	} else if msg.err != nil {
		m.setMessage(msg.err.Error())
		return m
	}
	selectedAgent, selectedID := "", ""
	if sess, ok := m.selectedSession(); ok {
		selectedAgent, selectedID = sess.AgentType, sess.ID
	}
	// A load that raced the reorder debounce carries the store's pre-move
	// SortIndex; replacing the roster would make the pending flush persist the
	// revert (#92). Keep the manual order; the next refresh follows the flush.
	if msg.sessions != nil && !m.reorderPending {
		m.sessions = projectSessions(msg.sessions, msg.groupByDir)
		m.groupByDir = msg.groupByDir
	}
	if msg.defaultAgent != "" {
		// A persisted default may name an agent whose CLI was since uninstalled;
		// reconcile it to an enabled provider rather than dispatching to a
		// disabled one (C2-9).
		m.defaultAgent = m.validateDefaultAgent(msg.defaultAgent)
	}
	m.profileNames = append([]string(nil), msg.profileNames...)
	m.profileProviders = msg.profileProviders
	m.defaultProfile = msg.defaultProfile
	if msg.profileBySession != nil {
		m.profileBySession = msg.profileBySession
	}
	if msg.lastSeenBySession != nil {
		m.lastSeenBySession = msg.lastSeenBySession
	}
	if selectedID != "" {
		for i, sess := range m.sessions {
			if sess.AgentType == selectedAgent && sess.ID == selectedID {
				m.selected = i
				if m.filterActive {
					m.reconcileFilterSelection()
				}
				return m
			}
		}
	}
	m.selected = max(0, min(m.selected, len(m.sessions)-1))
	if m.filterActive {
		m.reconcileFilterSelection()
	}
	return m
}

func (m Model) handleDispatched(msg dispatchedMsg) (tea.Model, tea.Cmd) {
	// A live session (non-empty ID) attaches even when msg.err is set: the agent
	// is running and the error is advisory (e.g. the record failed to persist).
	// Only a true dispatch failure — no session — aborts with the error (F03).
	if msg.session.ID == "" {
		if msg.err != nil {
			m.setMessage(msg.err.Error())
		}
		return m, nil
	}
	if msg.err != nil {
		m.setMessage("attaching " + msg.session.ID + " (warning: " + msg.err.Error() + ")")
	} else {
		m.setMessage("attaching " + msg.session.ID)
	}
	m.input = ""
	return m, m.attachSessionCmd(msg.session)
}

func (m Model) handleAttachFinished(msg attachFinishedMsg) Model {
	if msg.err != nil {
		m.setMessage("session exited: " + msg.err.Error())
	} else {
		m.setMessage("returned to uam")
	}
	return m
}

// handlePromptEdited loads the text the user composed in $EDITOR back into the
// wizard prompt buffer. On error the buffer is left untouched and the error is
// surfaced in the status line (C2-8).
func (m Model) handlePromptEdited(msg promptEditedMsg) Model {
	if msg.err != nil {
		m.setMessage("editor: " + msg.err.Error())
		return m
	}
	m.input = strings.TrimRight(msg.text, "\n")
	return m
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	// Ctrl+C quits from anywhere. Routed before the modal dispatch because a
	// modal claims every key it does not recognise, which left help, the
	// wizard, rename and both confirmations with no way out but Esc — and no
	// way out at all for a user reaching for the universal quit.
	if key == "ctrl+c" {
		_, cmd := m.handleActionKey(key)
		return m, cmd
	}
	if handled, model, cmd := m.handleModalKey(msg, key); handled {
		return model, cmd
	}
	if m.filterActive {
		if handled, cmd := m.handleFilterKey(msg, key); handled {
			return m, cmd
		}
	}
	if key == "/" && m.input == "" {
		m.enterFilter()
		return m, nil
	}
	if handled, cmd := m.handleMovementKey(key); handled {
		return m, cmd
	}
	if handled, cmd := m.handleActionKey(key); handled {
		return m, cmd
	}
	// The base dashboard has no text composer. Printable keys are inert here;
	// filter, wizard, rename, and confirmation input was routed above.
	return m, nil
}

func (m Model) handleModalKey(msg tea.KeyPressMsg, key string) (bool, tea.Model, tea.Cmd) {
	if m.confirmLatest || m.confirmStop {
		return m.handleConfirmationKey(msg, key)
	}
	if m.wizard {
		model, cmd := m.handleWizardKey(msg)
		return true, model, cmd
	}
	if m.renaming {
		model, cmd := m.handleRenameKey(msg)
		return true, model, cmd
	}
	return false, m, nil
}

func (m *Model) clearLatestConfirmation() {
	m.confirmLatest = false
	m.confirmLatestAction = ""
	m.confirmLatestAgent = ""
	m.confirmLatestID = ""
	m.confirmLatestName = ""
}

func (m Model) retryLatestCmd(action latestAction, agentName, id string) tea.Cmd {
	opts := ResumeOptions{AllowLatest: true}
	switch action {
	case latestAttach:
		return func() tea.Msg {
			spec, err := m.service.AttachSpecExactWithOptions(context.Background(), agentName, id, opts)
			return attachSpecMsg{spec: spec, err: err}
		}
	case latestRestart:
		return func() tea.Msg {
			if err := m.service.RestartExactWithOptions(context.Background(), agentName, id, opts); err != nil {
				return sessionsLoadedMsg{err: err}
			}
			return m.loadSessionsCmd()()
		}
	default:
		return func() tea.Msg {
			if err := m.service.ResumeBackgroundExactWithOptions(context.Background(), agentName, id, opts); err != nil {
				return sessionsLoadedMsg{err: err}
			}
			return m.loadSessionsCmd()()
		}
	}
}

func (m *Model) handleMovementKey(key string) (bool, tea.Cmd) {
	switch key {
	case "up":
		m.moveSelection(-1)
		return true, nil
	case "down":
		m.moveSelection(1)
		return true, nil
	case "shift+up":
		return true, m.moveSession(-1)
	case "shift+down":
		return true, m.moveSession(1)
	}
	return false, nil
}

func (m *Model) moveSelection(delta int) {
	if m.filterActive {
		visible := m.visibleSessionIndices()
		if len(visible) == 0 {
			return
		}
		position := 0
		for i, index := range visible {
			if index == m.selected {
				position = i
				break
			}
		}
		next := position + delta
		if next >= 0 && next < len(visible) {
			m.selected = visible[next]
		}
		return
	}
	next := m.selected + delta
	if next >= 0 && next < len(m.sessions) {
		m.selected = next
	}
}

func (m *Model) moveSession(delta int) tea.Cmd {
	if m.filterActive {
		return m.moveFilteredSession(delta)
	}
	next := m.selected + delta
	if next < 0 || next >= len(m.sessions) {
		return nil
	}
	return m.moveSessionTo(next)
}

// moveSessionTo applies the shared identity-safe reorder invariants between two
// canonical indices. Both normal and filtered navigation resolve their target
// index before entering this path, so hidden rows are never mistaken for the
// selected session.
func (m *Model) moveSessionTo(next int) tea.Cmd {
	if m.selected < 0 || m.selected >= len(m.sessions) || next < 0 || next >= len(m.sessions) || next == m.selected {
		return nil
	}
	// SortSessions buckets rows by process liveness, then Pinned, before honoring
	// SortIndex. A swap that crosses either boundary is undone on the next
	// refresh (the row snaps back to its partition), so reject it and give
	// honest feedback instead of a move that silently reverts (F34).
	if !samePartition(m.sessions[m.selected], m.sessions[next]) {
		m.setMessage("can't reorder across the running/stopped or pinned boundary")
		return nil
	}
	if m.groupByDir && workspaceKey(m.sessions[m.selected].Cwd) != workspaceKey(m.sessions[next].Cwd) {
		m.setMessage("can't reorder across workspace groups")
		return nil
	}
	groupStart, groupEnd := m.reorderGroupBounds(m.selected)
	if reorderGroupHasCollidingIndices(m.sessions[groupStart:groupEnd]) {
		m.normalizeReorderGroup(groupStart, groupEnd)
	}
	m.sessions[m.selected].SortIndex, m.sessions[next].SortIndex = m.sessions[next].SortIndex, m.sessions[m.selected].SortIndex
	m.markReorderDirty(m.sessions[m.selected], m.sessions[next])
	m.sessions[m.selected], m.sessions[next] = m.sessions[next], m.sessions[m.selected]
	m.selected = next
	return m.scheduleReorderFlush()
}

// scheduleReorderFlush bumps the reorder seq, marks a flush pending, and arms a
// debounced tick carrying the new seq. Only the tick whose seq is still current
// when it fires actually persists, so a burst of moves collapses to one write
// (F59).
func (m *Model) scheduleReorderFlush() tea.Cmd {
	m.reorderSeq++
	m.reorderPending = true
	seq := m.reorderSeq
	return tea.Tick(reorderDebounce, func(time.Time) tea.Msg { return reorderFlushMsg{seq: seq} })
}

// flushReorder persists the current order if a reorder is pending, clearing the
// pending flag. It re-reads under flock via UpdateSortOrder (Store.Update), so
// the flush owns only the SortIndex keys and never clobbers a concurrent
// mutation with a stale snapshot (F59, F01).
func (m *Model) flushReorder() tea.Cmd {
	if !m.reorderPending {
		return nil
	}
	return m.persistSortIndicesCmd(m.captureReorderDirty())
}

func (m *Model) captureReorderDirty() []adapter.Session {
	dirty := make([]adapter.Session, 0, len(m.reorderDirty))
	for _, sess := range m.sessions {
		if _, ok := m.reorderDirty[sessionIdentity{agent: sess.AgentType, id: sess.ID}]; ok {
			dirty = append(dirty, sess)
		}
	}
	m.reorderPending = false
	m.reorderDirty = nil
	return dirty
}

func (m *Model) markReorderDirty(sessions ...adapter.Session) {
	if m.reorderDirty == nil {
		m.reorderDirty = make(map[sessionIdentity]struct{})
	}
	for _, sess := range sessions {
		m.reorderDirty[sessionIdentity{agent: sess.AgentType, id: sess.ID}] = struct{}{}
	}
}

func (m Model) reorderGroupBounds(index int) (int, int) {
	start, end := index, index+1
	for start > 0 && m.sameReorderGroup(m.sessions[index], m.sessions[start-1]) {
		start--
	}
	for end < len(m.sessions) && m.sameReorderGroup(m.sessions[index], m.sessions[end]) {
		end++
	}
	return start, end
}

func (m Model) sameReorderGroup(a, b adapter.Session) bool {
	return samePartition(a, b) && (!m.groupByDir || workspaceKey(a.Cwd) == workspaceKey(b.Cwd))
}

func reorderGroupHasCollidingIndices(sessions []adapter.Session) bool {
	seen := make(map[int]struct{}, len(sessions))
	for _, sess := range sessions {
		if _, ok := seen[sess.SortIndex]; ok {
			return true
		}
		seen[sess.SortIndex] = struct{}{}
	}
	return false
}

func (m *Model) normalizeReorderGroup(start, end int) {
	used := make(map[int]struct{}, len(m.sessions)-(end-start))
	for i, sess := range m.sessions {
		if i < start || i >= end {
			used[sess.SortIndex] = struct{}{}
		}
	}
	next := m.sessions[start].SortIndex
	for i := start; i < end; i++ {
		for {
			if _, exists := used[next]; !exists {
				break
			}
			next++
		}
		m.sessions[i].SortIndex = next
		m.markReorderDirty(m.sessions[i])
		used[next] = struct{}{}
		next++
	}
}

// samePartition reports whether two rows sort into the same SortSessions
// partition — they share the same Running/Stopped and Pinned flags. Only within a
// partition does SortIndex (and therefore a manual reorder) take effect (F34).
func samePartition(a, b adapter.Session) bool {
	return a.ProcAlive == b.ProcAlive && a.Pinned == b.Pinned
}

func (m *Model) handleActionKey(key string) (bool, tea.Cmd) {
	switch key {
	case "ctrl+c":
		m.quitting = true
		// Flush any pending reorder before exiting so the debounce timer not yet
		// having fired doesn't lose the manual order (F59). Sequence, not Batch:
		// Batch runs members concurrently and Run returns on Quit while the
		// flush is still writing (#91).
		return true, tea.Sequence(m.flushReorder(), tea.Quit)
	case "tab":
		m.cycleDefaultAgent()
		return true, m.persistDefaultAgent()
	case "?":
		m.helpOpen = !m.helpOpen
	case "r":
		if m.refreshError == "" {
			return false, nil
		}
		return true, m.retryRefresh()
	case "ctrl+s":
		grouped := !m.groupByDir
		generation := m.nextGroupToggleGeneration()
		var dirty []adapter.Session
		if m.reorderPending {
			dirty = m.captureReorderDirty()
		}
		m.setGroupByDir(grouped)
		return true, m.persistGroupToggleCmd(dirty, grouped, generation)
	case "ctrl+t":
		return true, m.pinSelectedCmd()
	case "ctrl+r":
		m.startRename()
	case "ctrl+x":
		if sess, ok := m.selectedSession(); ok {
			m.openSessionConfirmation(sess)
		}
	case " ":
		return true, m.handleSpaceKey(key)
	case "right", "enter":
		return true, m.handleEnterKey()
	case "esc":
		return true, m.handleEscKey()
	case "backspace":
		// No text field exists on the base dashboard.
	case "e":
		m.handleEditKey(key)
	default:
		return false, nil
	}
	return true, nil
}

func (m *Model) retryRefresh() tea.Cmd {
	if m.loading {
		return nil
	}
	m.refreshError = ""
	m.loading = true
	return tea.Batch(m.refreshSessionsCmd(), m.activity.Tick)
}

// handleMouse keeps raw pointer coordinates inert. The displayed view resolves
// them through its captured compositor and sends a semantic intent instead.
func (m Model) handleMouse(tea.MouseMsg) (tea.Model, tea.Cmd) {
	// Bubble Tea v2 delivers this raw event to Update as well as to View.OnMouse.
	// Only the view callback knows which compositor was actually displayed, so
	// raw coordinates are deliberately inert here.
	return m, nil
}

// handleEscKey quits the base dashboard.
func (m *Model) handleEscKey() tea.Cmd {
	m.input = ""
	m.quitting = true
	// Flush a pending reorder before exiting (F59); Sequence so the flush
	// completes before Quit is delivered (#91).
	return tea.Sequence(m.flushReorder(), tea.Quit)
}

func (m *Model) startRename() {
	sess, ok := m.selectedSession()
	if !ok {
		return
	}
	m.renaming = true
	m.renameTargetAgent = sess.AgentType
	m.renameTargetID = sess.ID
	m.input = sess.DisplayName
}

func (m *Model) handleSpaceKey(_ string) tea.Cmd {
	if len(m.sessions) == 0 {
		return nil
	}
	// Space restarts a stopped session in the background.
	if sess, ok := m.selectedSession(); ok && sess.ProcAlive == adapter.Exited {
		m.setMessage("restarting " + firstNonEmpty(sess.DisplayName, sess.ID))
		return m.resumeSelectedCmd()
	}
	return nil
}

func (m *Model) handleEnterKey() tea.Cmd {
	if len(m.sessions) > 0 {
		return m.attachSelectedCmd()
	}
	return nil
}

func (m *Model) handleEditKey(_ string) {
	m.wizard = true
	m.wizardStep = 0
	m.input = ""
	m.wizardProfile = ""
	m.wizardAgentExplicit = false
	m.wizardAgent = m.wizardProviderDefault("")
	m.wizardAlias = ""
	m.wizardCwd = "."
}

func (m *Model) backspaceInput() {
	if r := []rune(m.input); len(r) > 0 {
		m.input = string(r[:len(r)-1])
	}
}

func (m Model) handlePaste(content string) Model {
	if content == "" {
		return m
	}
	if m.filterActive {
		m.filterQuery += content
		m.reconcileFilterSelection()
		return m
	}
	if m.wizard || m.renaming {
		m.input += content
	}
	return m
}

func (m Model) handleRenameKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "enter":
		sess, ok := m.sessionByIdentity(m.renameTargetAgent, m.renameTargetID)
		name := m.input
		m.renaming = false
		m.renameTargetAgent = ""
		m.renameTargetID = ""
		m.input = ""
		// The target session vanished (killed externally / list emptied) while the
		// modal was open: close the modal without panicking (F27).
		if !ok {
			return m, nil
		}
		agentName, id := sess.AgentType, sess.ID
		return m, func() tea.Msg {
			return sessionsLoadedMsg{err: m.service.RenameExact(context.Background(), agentName, id, name)}
		}
	case "esc":
		m.renaming = false
		m.renameTargetAgent = ""
		m.renameTargetID = ""
		m.input = ""
	default:
		m.editText(msg)
	}
	return m, nil
}

// editText applies a printable keypress to m.input. PasteMsg is handled
// separately by handlePaste; Alt chords and control keys never leak into text.
func (m *Model) editText(msg tea.KeyPressMsg) {
	switch {
	case msg.Code == tea.KeyBackspace:
		m.backspaceInput()
	case msg.Code == tea.KeySpace:
		m.input += " "
	case msg.Text != "" && !msg.Mod.Contains(tea.ModAlt):
		m.input += msg.Text
	}
}

func (m Model) handleWizardKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "esc" {
		m.closeWizard()
		return m, nil
	}
	if cmd, done := m.handleWizardStepKey(key); done {
		return m, cmd
	}
	m.editText(msg)
	return m, nil
}

func (m *Model) closeWizard() {
	m.wizard = false
	m.input = ""
}

func (m *Model) handleWizardStepKey(key string) (tea.Cmd, bool) {
	switch m.wizardStep {
	case 0:
		return m.handleWizardAgentKey(key)
	case 1:
		return m.handleWizardAliasKey(key)
	case 2:
		return m.handleWizardCwdKey(key)
	case 3:
		return m.handleWizardPromptKey(key)
	}
	return nil, false
}

func (m *Model) handleWizardAgentKey(key string) (tea.Cmd, bool) {
	switch key {
	case "tab":
		m.cycleDefaultAgent()
		m.wizardAgent = m.defaultAgent
		m.wizardAgentExplicit = true
		return m.persistDefaultAgent(), true
	case "shift+tab", "right":
		m.cycleWizardProfile()
		if !m.wizardAgentExplicit {
			m.wizardAgent = m.wizardProviderDefault(m.wizardProfile)
		}
		return nil, true
	case "enter":
		if m.wizardAgent == "" {
			m.wizardAgent = m.defaultAgent
		}
		m.wizardStep = 1
		m.input = m.wizardAlias
		return nil, true
	}
	return nil, false
}

func (m *Model) handleWizardAliasKey(key string) (tea.Cmd, bool) {
	switch key {
	case "enter":
		m.wizardAlias = strings.TrimSpace(m.input)
		m.wizardStep = 2
		m.input = m.wizardCwd
		return nil, true
	}
	return nil, false
}

func (m *Model) handleWizardCwdKey(key string) (tea.Cmd, bool) {
	switch key {
	case "tab":
		// Complete the typed path against the filesystem (C2-8). Marked done so
		// the literal tab never leaks into the buffer.
		m.input = globComplete(m.input)
		return nil, true
	case "enter":
		m.wizardCwd = firstNonEmpty(m.input, ".")
		m.wizardStep = 3
		m.input = ""
		return nil, true
	}
	return nil, false
}

func (m *Model) handleWizardPromptKey(key string) (tea.Cmd, bool) {
	switch key {
	case "ctrl+g":
		// Compose the prompt in $EDITOR for multi-line input. Launched via
		// tea.ExecProcess so the TUI screen state is restored cleanly — a raw
		// exec.Command would corrupt the alt-screen (C2-8).
		return m.editPromptCmd(), true
	case "enter":
		spec := parseDispatchSpec(m.input, firstNonEmpty(m.wizardAgent, m.defaultAgent))
		if spec.Alias == "" {
			spec.Alias = m.wizardAlias
		}
		cwd := m.wizardCwd
		m.closeWizard()
		return m.dispatchWithNameCwdProfileCmd(spec.Agent, spec.Alias, spec.Name, spec.Prompt, cwd, m.wizardProfile), true
	}
	return nil, false
}

// editPromptCmd composes the wizard prompt in $EDITOR. It seeds a temp file with
// the current buffer, launches the editor via the injected runner
// (tea.ExecProcess in production, which suspends/restores the alt-screen
// cleanly), and on exit loads the file back via promptEditedMsg. Using
// exec.Command directly instead would leave the terminal in raw mode and corrupt
// the TUI (C2-8).
func (m Model) editPromptCmd() tea.Cmd {
	runner := m.execProcess
	if runner == nil {
		runner = tea.ExecProcess
	}
	seed := m.input
	f, err := os.CreateTemp("", "uam-prompt-*.txt")
	if err != nil {
		return func() tea.Msg { return promptEditedMsg{err: fmt.Errorf("create prompt buffer: %w", err)} }
	}
	path := f.Name()
	if _, err := f.WriteString(seed); err != nil {
		_ = f.Close()
		return func() tea.Msg { return promptEditedMsg{err: fmt.Errorf("seed prompt buffer: %w", err)} }
	}
	if err := f.Close(); err != nil {
		return func() tea.Msg { return promptEditedMsg{err: fmt.Errorf("close prompt buffer: %w", err)} }
	}
	editor := firstNonEmpty(os.Getenv("VISUAL"), os.Getenv("EDITOR"), "vi")
	cmd := exec.Command(editor, path) // #nosec G204,G702 -- editor is the user's own $VISUAL/$EDITOR (their environment, not external input), path is a temp file we created; this is the standard "edit in $EDITOR" pattern (git/kubectl).
	return runner(cmd, func(err error) tea.Msg {
		defer func() { _ = os.Remove(path) }()
		if err != nil {
			return promptEditedMsg{err: fmt.Errorf("editor exited: %w", err)}
		}
		data, readErr := os.ReadFile(path) // #nosec G304 -- path is the temp file we just created above.
		if readErr != nil {
			return promptEditedMsg{err: fmt.Errorf("read edited prompt: %w", readErr)}
		}
		return promptEditedMsg{text: string(data)}
	})
}

// isGitRepo reports whether dir is inside a git working tree by walking up the
// directory tree looking for a .git entry, the way git itself resolves the repo
// root. Used to warn in the wizard when dispatching outside a repo means there is
// no checkpoint to recover the agent's work from (C2-8).
func isGitRepo(dir string) bool {
	d, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	for {
		if validGitMarker(filepath.Join(d, ".git")) {
			return true
		}
		parent := filepath.Dir(d)
		if parent == d {
			return false
		}
		d = parent
	}
}

// validGitMarker accepts both an ordinary .git directory and the gitdir file
// used by linked worktrees. Mere existence is insufficient: temporary roots
// and interrupted tooling sometimes leave an empty .git entry behind, which
// must not suppress the wizard's no-checkpoint warning.
func validGitMarker(marker string) bool {
	info, err := os.Stat(marker)
	if err != nil {
		return false
	}
	gitDir := marker
	if !info.IsDir() {
		if !info.Mode().IsRegular() {
			return false
		}
		contents, readErr := os.ReadFile(marker) // #nosec G304 -- marker is the literal .git entry found while walking the user-selected local workspace.
		if readErr != nil {
			return false
		}
		line := strings.TrimSpace(string(contents))
		const prefix = "gitdir:"
		if !strings.HasPrefix(strings.ToLower(line), prefix) {
			return false
		}
		gitDir = strings.TrimSpace(line[len(prefix):])
		if !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(filepath.Dir(marker), gitDir)
		}
	}
	head, headErr := os.Stat(filepath.Join(gitDir, "HEAD")) // #nosec G703 -- linked-worktree .git files intentionally name their local git directory; only HEAD metadata is inspected.
	return headErr == nil && !head.IsDir()
}

// globComplete completes a partially-typed path against the filesystem. It
// returns the longest unambiguous match (the sole match, or the shared prefix of
// several); when nothing matches it returns the input unchanged so Tab is a
// no-op rather than destructive (C2-8).
func globComplete(input string) string {
	if input == "" {
		return input
	}
	matches, err := filepath.Glob(input + "*")
	if err != nil || len(matches) == 0 {
		return input
	}
	if len(matches) == 1 {
		return matches[0]
	}
	return longestCommonPrefix(matches)
}

func longestCommonPrefix(items []string) string {
	if len(items) == 0 {
		return ""
	}
	prefix := items[0]
	for _, s := range items[1:] {
		for !strings.HasPrefix(s, prefix) {
			prefix = prefix[:len(prefix)-1]
			if prefix == "" {
				return ""
			}
		}
	}
	return prefix
}

func (m *Model) cycleDefaultAgent() {
	if m.service.Registry == nil {
		return
	}
	enabled := m.service.Registry.Enabled()
	if len(enabled) == 0 {
		return
	}
	idx := 0
	for i, a := range enabled {
		if a.Name() == m.defaultAgent {
			idx = i + 1
		}
	}
	m.defaultAgent = enabled[idx%len(enabled)].Name()
}

func (m *Model) cycleWizardProfile() {
	choices := append([]string{""}, m.profileNames...)
	for i, name := range choices {
		if name == m.wizardProfile {
			m.wizardProfile = choices[(i+1)%len(choices)]
			return
		}
	}
	m.wizardProfile = ""
}

func (m Model) wizardProviderDefault(profileName string) string {
	if profileName == "" {
		profileName = m.defaultProfile
	}
	if provider := m.profileProviders[profileName]; provider != "" {
		return provider
	}
	return m.defaultAgent
}

type dispatchSpec struct {
	Agent  string
	Alias  string
	Name   string
	Prompt string
}

func parseDispatchSpec(input, def string) dispatchSpec {
	spec := dispatchSpec{Agent: def}
	rest := strings.TrimLeft(input, " \t")
	if token, next, ok := consumeDispatchToken(rest, "@"); ok {
		spec.Agent, spec.Alias = splitAgentAlias(token)
		rest = next
	}
	if token, next, ok := consumeDispatchToken(rest, "#"); ok {
		spec.Name = token
		rest = next
	}
	spec.Prompt = rest
	return spec
}

func splitAgentAlias(token string) (agent, alias string) {
	if agent, alias, ok := strings.Cut(token, ":"); ok {
		return agent, alias
	}
	return token, ""
}

func consumeDispatchToken(input, prefix string) (token, rest string, ok bool) {
	if !strings.HasPrefix(input, prefix) {
		return "", input, false
	}
	withoutPrefix := input[len(prefix):]
	if i := strings.IndexAny(withoutPrefix, " \t"); i >= 0 {
		return withoutPrefix[:i], strings.TrimLeft(withoutPrefix[i:], " \t"), true
	}
	return withoutPrefix, "", true
}

func (m Model) dispatchNamedCmd(agent, alias, name, prompt string) tea.Cmd {
	return m.dispatchWithNameCwdProfileCmd(agent, alias, name, prompt, "", "")
}
func (m Model) dispatchWithNameCwdProfileCmd(agent, alias, name, prompt, cwd, profile string) tea.Cmd {
	return func() tea.Msg {
		// Mode is decided by the resolved launch policy (explicit, alias-assigned
		// or default profile; built-in default is yolo), never by the call site.
		sess, err := m.service.DispatchNamedWithAliasProfile(context.Background(), agent, alias, name, prompt, cwd, "", profile)
		return dispatchedMsg{session: sess, err: err}
	}
}
func (m Model) selectedSession() (adapter.Session, bool) {
	if len(m.sessions) == 0 || m.selected < 0 || m.selected >= len(m.sessions) {
		return adapter.Session{}, false
	}
	return m.sessions[m.selected], true
}

// sessionByID returns the session with the given id, falling back to the
// selected row when id is empty. Modal flows (rename/stop-confirm) snapshot the
// target id at open time so a refresh that reorders the list mid-modal still
// acts on the originally-chosen session (C2-1, F29).
func (m Model) sessionByID(id string) (adapter.Session, bool) {
	if id == "" {
		return m.selectedSession()
	}
	for _, sess := range m.sessions {
		if sess.ID == id {
			return sess, true
		}
	}
	return adapter.Session{}, false
}

func (m Model) sessionByIdentity(agentName, id string) (adapter.Session, bool) {
	if agentName == "" {
		return m.sessionByID(id)
	}
	if id == "" {
		return m.selectedSession()
	}
	for _, sess := range m.sessions {
		if sess.AgentType == agentName && sess.ID == id {
			return sess, true
		}
	}
	return adapter.Session{}, false
}

// resumeSelectedCmd restarts the selected session's backend session in the
// background, then reloads so it moves into RUNNING.
func (m Model) resumeSelectedCmd() tea.Cmd {
	sess, ok := m.selectedSession()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		if err := m.service.ResumeBackgroundExact(context.Background(), sess.AgentType, sess.ID); err != nil {
			if errors.Is(err, ErrAmbiguousResume) {
				return latestRequiredMsg{action: latestResume, agent: sess.AgentType, id: sess.ID, name: firstNonEmpty(sess.DisplayName, sess.ID), err: err}
			}
			return sessionsLoadedMsg{err: err}
		}
		return m.loadSessionsCmd()()
	}
}

// persistDefaultAgent persists the default-agent choice. On failure it surfaces
// the error in the status line instead of swallowing it; on success it returns a
// reload command so the UI reflects the stored config (F55).
func (m *Model) persistDefaultAgent() tea.Cmd {
	if err := m.service.SetDefaultAgent(m.defaultAgent); err != nil {
		m.setMessage("could not save default agent: " + err.Error())
		return nil
	}
	return m.loadSessionsCmd()
}

func (m *Model) setGroupByDir(grouped bool) {
	selectedAgent, selectedID := "", ""
	if sess, ok := m.selectedSession(); ok {
		selectedAgent, selectedID = sess.AgentType, sess.ID
	}
	canonical := append([]adapter.Session(nil), m.sessions...)
	SortSessions(canonical)
	m.sessions = projectSessions(canonical, grouped)
	m.groupByDir = grouped
	for i, sess := range m.sessions {
		if sess.AgentType == selectedAgent && sess.ID == selectedID {
			m.selected = i
			return
		}
	}
	m.selected = max(0, min(m.selected, len(m.sessions)-1))
}

// stopTargetCmd stops the session with the snapshotted id, falling back to the
// selected row when id is empty, so a refresh that reorders the list while the
// stop-confirm dialog is open still stops the originally-confirmed session (F29).
func (m Model) stopTargetCmd(id string, remove bool) tea.Cmd {
	sess, ok := m.sessionByID(id)
	if !ok {
		return nil
	}
	return func() tea.Msg {
		err := m.service.Stop(context.Background(), sess.ID, remove)
		return sessionsLoadedMsg{err: err}
	}
}

func (m Model) stopTargetExactCmd(agentName, id string, remove bool) tea.Cmd {
	sess, ok := m.sessionByIdentity(agentName, id)
	if !ok {
		return nil
	}
	return func() tea.Msg {
		err := m.service.StopExact(context.Background(), sess.AgentType, sess.ID, remove)
		return sessionsLoadedMsg{err: err}
	}
}

func (m Model) restartTargetExactCmd(agentName, id string) tea.Cmd {
	sess, ok := m.sessionByIdentity(agentName, id)
	if !ok {
		return nil
	}
	return func() tea.Msg {
		if err := m.service.RestartExact(context.Background(), sess.AgentType, sess.ID); err != nil {
			if errors.Is(err, ErrAmbiguousResume) {
				return latestRequiredMsg{action: latestRestart, agent: sess.AgentType, id: sess.ID, name: firstNonEmpty(sess.DisplayName, sess.ID), err: err}
			}
			return sessionsLoadedMsg{err: err}
		}
		return m.loadSessionsCmd()()
	}
}
func (m Model) pinSelectedCmd() tea.Cmd {
	sess, ok := m.selectedSession()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		err := m.service.TogglePinExact(context.Background(), sess.AgentType, sess.ID)
		return sessionsLoadedMsg{err: err}
	}
}
func (m Model) attachSelectedCmd() tea.Cmd {
	sess, ok := m.selectedSession()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		spec, err := m.service.AttachSpecExact(context.Background(), sess.AgentType, sess.ID)
		if errors.Is(err, ErrAmbiguousResume) {
			return latestRequiredMsg{action: latestAttach, agent: sess.AgentType, id: sess.ID, name: firstNonEmpty(sess.DisplayName, sess.ID), err: err}
		}
		return attachSpecMsg{spec: spec, err: err}
	}
}

func (m Model) attachSessionCmd(sess adapter.Session) tea.Cmd {
	if sess.ID == "" || sess.AgentType == "" {
		return nil
	}
	return func() tea.Msg {
		if m.service == nil || m.service.Registry == nil {
			return sessionsLoadedMsg{err: fmt.Errorf("agent %q unavailable", sess.AgentType)}
		}
		if m.service.Store == nil {
			a, ok := m.service.Registry.Get(sess.AgentType)
			if !ok {
				return sessionsLoadedMsg{err: fmt.Errorf("agent %q unavailable", sess.AgentType)}
			}
			spec, err := a.Attach(sess.ID)
			return attachSpecMsg{spec: spec, err: err}
		}
		spec, err := m.service.AttachSpecExact(context.Background(), sess.AgentType, sess.ID)
		return attachSpecMsg{spec: spec, err: err}
	}
}

func (m Model) execAttachSpec(spec adapter.AttachSpec, err error) tea.Cmd {
	if err != nil {
		return func() tea.Msg { return sessionsLoadedMsg{err: err} }
	}
	if len(spec.Argv) == 0 {
		return func() tea.Msg { return sessionsLoadedMsg{err: fmt.Errorf("empty attach command")} }
	}
	runner := m.execProcess
	if runner == nil {
		runner = tea.ExecProcess
	}
	cmd := exec.Command(spec.Argv[0], spec.Argv[1:]...) // #nosec G204 -- attach argv is generated by trusted agent adapters, no shell expansion.
	cmd.Env = attachProcessEnvironment(os.Environ(), spec)
	return runner(cmd, func(err error) tea.Msg { return attachFinishedMsg{err: err} })
}

func attachProcessEnvironment(base []string, spec adapter.AttachSpec) []string {
	environment := make([]string, 0, len(base)+6)
	for _, assignment := range base {
		name, _, _ := strings.Cut(assignment, "=")
		if name == session.AttachQuietEnv || name == session.AttachSelectedProfileEnv || name == session.AttachEffectiveProfileEnv ||
			name == session.AttachPolicyMouseEnv || name == session.AttachPolicyPrefixEnv || name == session.AttachPolicyBackDetachEnv {
			continue
		}
		environment = append(environment, assignment)
	}
	environment = append(environment, session.AttachQuietEnv+"=1")
	if spec.Profile.Selected != "" {
		environment = append(environment, session.AttachSelectedProfileEnv+"="+spec.Profile.Selected)
	}
	if spec.Profile.Effective != "" {
		environment = append(environment, session.AttachEffectiveProfileEnv+"="+spec.Profile.Effective)
	}
	if spec.Profile.Mouse != "" || spec.Profile.ControlPrefix != "" {
		backDetach := "0"
		if spec.Profile.BackDetach {
			backDetach = "1"
		}
		environment = append(environment,
			session.AttachPolicyMouseEnv+"="+spec.Profile.Mouse,
			session.AttachPolicyPrefixEnv+"="+spec.Profile.ControlPrefix,
			session.AttachPolicyBackDetachEnv+"="+backDetach,
		)
	}
	return environment
}

func (m Model) persistOrderCmd() tea.Cmd {
	sessions := append([]adapter.Session(nil), m.sessions...)
	return m.persistSortIndicesCmd(sessions)
}

func (m Model) persistSortIndicesCmd(sessions []adapter.Session) tea.Cmd {
	sessions = append([]adapter.Session(nil), sessions...)
	return func() tea.Msg { return sessionsLoadedMsg{err: m.updateSortIndices(sessions)} }
}

func (m Model) updateSortIndices(sessions []adapter.Session) error {
	if m.persistSortIndices != nil {
		return m.persistSortIndices(sessions)
	}
	return m.service.UpdateSortIndices(sessions)
}

func (m *Model) nextGroupToggleGeneration() uint64 {
	if m.groupToggle == nil {
		m.groupToggle = &groupToggleCoordinator{}
	}
	m.groupToggle.mu.Lock()
	defer m.groupToggle.mu.Unlock()
	m.groupToggle.generation++
	return m.groupToggle.generation
}

func (m Model) isLatestGroupToggle(generation uint64) bool {
	if m.groupToggle == nil {
		return false
	}
	m.groupToggle.mu.Lock()
	defer m.groupToggle.mu.Unlock()
	return generation == m.groupToggle.generation
}

func (m Model) persistGroupToggleCmd(sessions []adapter.Session, grouped bool, generation uint64) tea.Cmd {
	sessions = append([]adapter.Session(nil), sessions...)
	return func() tea.Msg {
		if len(sessions) > 0 {
			if err := m.updateSortIndices(sessions); err != nil {
				return groupToggleResultMsg{generation: generation, grouped: grouped, err: err}
			}
		}
		m.groupToggle.mu.Lock()
		if generation != m.groupToggle.generation {
			m.groupToggle.mu.Unlock()
			return groupToggleResultMsg{generation: generation, grouped: grouped}
		}
		err := m.service.SetUI(func(ui *store.UISettings) { ui.GroupByDir = grouped })
		m.groupToggle.mu.Unlock()
		if err != nil {
			return groupToggleResultMsg{generation: generation, grouped: grouped, err: err}
		}
		loaded, _ := m.loadSessionsCmd()().(sessionsLoadedMsg)
		return groupToggleResultMsg{generation: generation, grouped: grouped, loaded: loaded}
	}
}

// The palette and the tone table live in theme.go.

// LayoutClass names the three responsive dashboard geometries. It is derived
// from the current terminal dimensions; Model deliberately stores no parallel
// layout booleans that could become contradictory after a resize.
type LayoutClass uint8

const (
	LayoutCompact LayoutClass = iota
	LayoutStandard
	LayoutWide
)

// DashboardMode is the primary dashboard surface. Like LayoutClass it is
// derived from the existing interaction state.
type DashboardMode uint8

const (
	ModeOperations DashboardMode = iota
	ModeNew
)

func (m Model) layoutClass() LayoutClass {
	w := m.width
	if w <= 0 {
		w = 98
	}
	if w < dashboardCompactMin {
		return LayoutCompact
	}
	if w >= dashboardWideMin {
		return LayoutWide
	}
	return LayoutStandard
}

func (m Model) dashboardMode() DashboardMode {
	if m.wizard {
		return ModeNew
	}
	return ModeOperations
}

// layoutMode is retained as a compatibility shim for component-level tests.
func (m Model) layoutMode() int {
	switch m.layoutClass() {
	case LayoutWide:
		return 2
	case LayoutStandard:
		return 1
	default:
		return 0
	}
}

func (m Model) View() tea.View {
	var view tea.View
	if m.sizeKnown && m.confirmationActive() && !m.quitting {
		frame := m.buildConfirmationOverlay()
		view = tea.NewView(lipgloss.Sprint(frame.content))
		view.OnMouse = frame.mouseCommand
	} else if m.sizeKnown && !m.confirmLatest && !m.confirmStop && !m.wizard && !m.renaming && !m.quitting {
		frame := m.buildDashboardFrame()
		view = tea.NewView(lipgloss.Sprint(frame.content))
		view.OnMouse = func(msg tea.MouseMsg) tea.Cmd { return m.dashboardMouseCommand(frame, msg) }
	} else {
		view = tea.NewView(lipgloss.Sprint(m.viewContent()))
	}
	view.AltScreen = true
	if MouseReportingEnabled() {
		view.MouseMode = tea.MouseModeCellMotion
	}
	return view
}

func (m Model) viewContent() string {
	if m.quitting {
		return ""
	}
	// Before Bubble Tea sends its first WindowSizeMsg, keep the first frame small
	// and stable instead of flashing the legacy unbounded dashboard.
	if !m.sizeKnown {
		// A confirmation can arrive before a WindowSizeMsg in tests and on very
		// slow remote terminals. Never hide a safety-critical modal behind loading.
		if m.confirmLatest || m.confirmStop || m.wizard || m.renaming {
			return m.unboundedView()
		}
		return m.activityView() + " " + hintStyle.Render("Loading agents")
	}
	return m.dashboardView()
}

func (m Model) unboundedView() string {
	var b strings.Builder
	b.WriteString(m.renderBranding())
	switch {
	case m.confirmLatest:
		b.WriteString(m.renderLatestConfirmation())
	case m.confirmStop:
		b.WriteString(m.renderConfirm())
	case m.wizard:
		b.WriteString(m.renderWizard())
	default:
		b.WriteString(m.renderDetails())
		b.WriteString(m.renderTable())
	}
	b.WriteString(m.renderPrompt())
	return b.String()
}

// responsiveView reserves the prompt first, then allocates the remaining rows
// to exactly one primary surface. fitScreen is a final safety rail for terminal
// sizes smaller than any useful composition; normal fixtures fit by budget.
func (m Model) responsiveView() string {
	w, h := max(1, m.width), max(0, m.height)
	if h == 0 {
		return ""
	}
	header := []string{m.responsiveHeader(w)}
	prompt := boundedNonBlankLines(m.renderPrompt(), w)
	if m.dashboardMode() == ModeNew {
		prompt = m.wizardPromptLines(w)
	}
	if len(prompt) == 0 {
		prompt = []string{bar() + " " + brandStyle.Render(caretGlyph())}
	}
	if len(prompt) >= h {
		return fitScreen(prompt[:h], w, h)
	}
	bodyBudget := max(0, h-len(header)-len(prompt))
	body := m.responsiveBody(w, bodyBudget)
	lines := append(header, body...)
	lines = append(lines, prompt...)
	return fitScreen(lines, w, h)
}

func (m Model) wizardPromptLines(width int) []string {
	step := max(0, min(m.wizardStep, 3))
	hints := []string{
		"Tab cycle · Enter confirm · Esc cancel",
		"Enter confirm · Esc cancel",
		"Tab path · Enter confirm · Esc cancel",
		"Ctrl+G edit · Enter start · Esc cancel",
	}
	field := displaytext.Sanitize(m.input)
	if step == 0 && field == "" {
		field = firstNonEmpty(m.wizardAgent, m.defaultAgent)
	}
	if step == 0 {
		field += "  profile=" + m.wizardProfileLabel()
	}
	return []string{
		ansi.Truncate(bar()+" "+hintStyle.Render("new")+" "+brandStyle.Render(caretGlyph())+" "+titleStyle.Render(field)+brandStyle.Render(cursorGlyph()), width, truncTail()),
		ansi.Truncate("  "+hintStyle.Render(hints[step]), width, truncTail()),
	}
}

func (m Model) responsiveHeader(width int) string {
	text := bar() + " " + brandStyle.Render("UAM") + "  " + hintStyle.Render("Unified Agent Manager")
	if m.layoutClass() != LayoutCompact {
		text += "  " + hintStyle.Render(version.String())
	}
	return ansi.Truncate(text, width, truncTail())
}

func (m Model) responsiveBody(width, budget int) []string {
	if budget <= 0 {
		return nil
	}
	if m.confirmLatest {
		return takeLines(boundedNonBlankLines(m.renderLatestConfirmation(), width), budget)
	}
	if m.confirmStop {
		return takeLines(boundedNonBlankLines(m.renderConfirm(), width), budget)
	}
	if m.dashboardMode() == ModeNew {
		return takeLines(boundedNonBlankLines(m.renderWizard(), width), budget)
	}
	return m.dashboardBody(width, budget)
}

func (m Model) renderSectionAtWidth(label, right string, width int) string {
	head := sectionStyle.Render(label)
	rightWidth := ansi.StringWidth(right)
	fill := max(0, width-ansi.StringWidth(head)-rightWidth-4)
	line := " " + head
	if fill > 0 {
		line += "  " + dividerStyle.Render(strings.Repeat(ruleGlyph(), fill))
	}
	if right != "" {
		line += " " + hintStyle.Render(right)
	}
	return ansi.Truncate(line, width, truncTail())
}

func tableWidthsFor(width int, class LayoutClass) (int, int, bool) {
	if class == LayoutCompact || width < 58 {
		return max(1, width-5), 0, false
	}
	name := min(30, max(10, width/3))
	return name, max(1, width-name-8), true
}

func visibleWindow(length, selected, limit int) (int, int) {
	limit = min(length, max(0, limit))
	if limit == 0 {
		return 0, 0
	}
	selected = max(0, min(selected, length-1))
	start := max(0, selected-limit/2)
	start = min(start, length-limit)
	return start, start + limit
}

func boundedNonBlankLines(s string, width int) []string {
	raw := strings.Split(strings.Trim(s, "\n"), "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		if line == "" {
			continue
		}
		lines = append(lines, ansi.Truncate(line, width, truncTail()))
	}
	return lines
}

func padRightANSI(s string, width int) string {
	s = ansi.Truncate(s, width, truncTail())
	return s + strings.Repeat(" ", max(0, width-ansi.StringWidth(s)))
}

// padLeftANSI right-aligns s in width cells; wider input is returned intact so
// the caller's truncation policy — not the padder's — decides what to cut.
func padLeftANSI(s string, width int) string {
	return strings.Repeat(" ", max(0, width-ansi.StringWidth(s))) + s
}

func takeLines(lines []string, n int) []string {
	return lines[:min(len(lines), max(0, n))]
}

func fitScreen(lines []string, width, height int) string {
	if len(lines) > height {
		lines = lines[len(lines)-height:]
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], width, truncTail())
	}
	return strings.Join(lines, "\n")
}

const uamANSILogo = ` _   _  _   __  __
| | | |/_\ |  \/  |
| |_| / _ \| |\/| |
 \___/_/ \_\_|  |_|`

func (m Model) renderBranding() string {
	var b strings.Builder
	ver := hintStyle.Render(version.String())
	if m.layoutMode() == 0 {
		b.WriteString(bar() + " " + brandStyle.Render("UAM") + "  " + hintStyle.Render("Unified Agent Manager") + "\n")
		b.WriteString(bar() + " " + ver + "\n")
		b.WriteString("\n")
		return b.String()
	}
	logo := strings.Split(uamANSILogo, "\n")
	side := []string{"", brandStyle.Render("Unified Agent Manager"), hintStyle.Render("multi-agent session control"), ver}
	for i, line := range logo {
		row := bar() + " " + brandStyle.Render(line)
		if i < len(side) && side[i] != "" {
			row += "    " + side[i]
		}
		b.WriteString(row + "\n")
	}
	b.WriteString("\n")
	return b.String()
}

// renderSection draws a borderless "LABEL ───────  right" header.
func (m Model) renderSection(label, right string) string {
	head := sectionStyle.Render(label)
	fill := max(3, m.contentWidth()-lipgloss.Width(head)-lipgloss.Width(right)-4)
	line := " " + head + "  " + dividerStyle.Render(strings.Repeat(ruleGlyph(), fill))
	if right != "" {
		line += " " + hintStyle.Render(right)
	}
	return line
}

func (m Model) renderDetails() string {
	sess, ok := m.selectedSession()
	if !ok {
		return ""
	}
	var b strings.Builder
	b.WriteString(m.renderSection("SELECTED", "") + "\n")
	name := truncate(firstNonEmpty(sess.DisplayName, sess.ID), max(12, m.contentWidth()-6))
	b.WriteString("  " + titleStyle.Render(name) + "\n")
	// Show the task/prompt here only when the session list is too narrow to
	// show it inline (no task column) — that way it stays visible exactly once.
	if _, _, showTask := m.tableWidths(); !showTask {
		b.WriteString("    " + taskStyle.Render(boundedTaskSummary(sess, max(8, m.contentWidth()-6))) + "\n")
	}
	b.WriteString("    " + hintStyle.Render("agent: "+displaytext.Sanitize(firstNonEmpty(sess.AgentType, "?"))) + "\n")
	selected, effective := m.profileLabels(sess)
	b.WriteString("    ")
	b.WriteString(hintStyle.Render("profile selected: " + displaytext.Sanitize(selected) + "  effective: " + displaytext.Sanitize(effective)))
	b.WriteByte('\n')
	if !sess.CreatedAt.IsZero() {
		b.WriteString("    " + hintStyle.Render("created: "+sess.CreatedAt.Format("Jan 02 15:04")) + "\n")
	}
	b.WriteString("    " + hintStyle.Render("cwd: "+absCwd(sess.Cwd)) + "\n")
	return b.String()
}

func (m Model) profileLabels(sess adapter.Session) (string, string) {
	selected := m.profileBySession[sessionIdentity{agent: sess.AgentType, id: sess.ID}]
	effective := selected
	if selected == "" {
		selected = "default"
		effective = m.defaultProfile
	}
	if effective == "" {
		effective = "none"
	}
	return selected, effective
}

func (m Model) renderTable() string {
	var b strings.Builder
	b.WriteString("\n")
	if m.groupByDir {
		budget := max(2, len(m.sessions)*3+4)
		lines := m.groupedSessionListLines(m.contentWidth(), budget, m.layoutClass())
		b.WriteString(strings.Join(lines, "\n"))
		if len(lines) > 0 {
			b.WriteString("\n")
		}
		return b.String()
	}
	if len(m.sessions) == 0 {
		b.WriteString(m.renderSection("SESSIONS", "0") + "\n")
		b.WriteString("  " + hintStyle.Render("no sessions — type a prompt, @agent #name prompt, or press e") + "\n")
		return b.String()
	}
	nameWidth, taskWidth, showTask := m.tableWidths()
	start, end := m.visibleSessionWindow()
	running, stopped := 0, 0
	for _, s := range m.sessions {
		if s.ProcAlive == adapter.Exited {
			stopped++
		} else {
			running++
		}
	}
	if start > 0 {
		b.WriteString("  " + hintStyle.Render(fmt.Sprintf("↑ %d more", start)) + "\n")
	}
	g1 := m.renderGroup(groupRenderOptions{label: "RUNNING", total: running, start: start, end: end, wantStopped: false, nameWidth: nameWidth, taskWidth: taskWidth, showTask: showTask})
	g2 := m.renderGroup(groupRenderOptions{label: "STOPPED", total: stopped, start: start, end: end, wantStopped: true, nameWidth: nameWidth, taskWidth: taskWidth, showTask: showTask})
	b.WriteString(g1)
	if g1 != "" && g2 != "" {
		b.WriteString("\n")
	}
	b.WriteString(g2)
	if end < len(m.sessions) {
		b.WriteString("  " + hintStyle.Render(fmt.Sprintf("↓ %d more", len(m.sessions)-end)) + "\n")
	}
	return b.String()
}

type groupRenderOptions struct {
	label       string
	total       int
	start       int
	end         int
	wantStopped bool
	nameWidth   int
	taskWidth   int
	showTask    bool
}

// renderGroup renders one process-liveness partition.
func (m Model) renderGroup(opts groupRenderOptions) string {
	var rows []string
	for i := opts.start; i < opts.end; i++ {
		s := m.sessions[i]
		if (s.ProcAlive == adapter.Exited) != opts.wantStopped {
			continue
		}
		rows = append(rows, renderRow(s, i == m.selected, opts.nameWidth, opts.taskWidth, opts.showTask))
	}
	if len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(m.renderSection(opts.label, fmt.Sprintf("%d", opts.total)) + "\n")
	for _, r := range rows {
		b.WriteString(r + "\n")
	}
	return b.String()
}

func renderRow(s adapter.Session, selected bool, nameWidth, taskWidth int, showTask bool) string {
	cursor := "  "
	if selected {
		cursor = brandStyle.Render("▸") + " "
	}
	glyph, gs := sessionGlyph(s)
	pin := ""
	if s.Pinned {
		pin = "★ "
	}
	// PR status dot in a fixed 1-column slot (blank when the session has no PR)
	// so the task column stays aligned whether or not a PR is present. Distinct
	// glyphs per status (not color-only) survive a no-color terminal (F26).
	prCell := " "
	if s.PR != nil {
		prCell = prStatusStyle(s.PR.Status).Render(prStatusDot(s.PR.Status))
	}
	nameStyle := titleStyle
	if selected {
		nameStyle = selectedStyle
	}
	detail := failureExitDetail(s)
	labelWidth := nameWidth
	if !showTask && detail != "" {
		labelWidth = max(1, nameWidth-ansi.StringWidth(detail)-1)
	}
	label := truncate(pin+firstNonEmpty(s.DisplayName, s.ID), labelWidth)
	if showTask {
		// Width-aware padding keeps the task column aligned even when the name
		// holds wide (CJK/emoji) runes (F28).
		cell := nameStyle.Render(padRight(label, nameWidth))
		return cursor + gs.Render(glyph) + " " + cell + " " + prCell + " " + taskStyle.Render(boundedTaskSummary(s, taskWidth))
	}
	// Narrow layout: state glyph + name only — one line per row. The selected
	// session's task is carried by the details panel, so rows don't repeat it.
	row := cursor + gs.Render(glyph) + " " + nameStyle.Render(label)
	if detail != "" {
		row += " " + failGlyphStyle.Render(detail)
	}
	if s.PR != nil {
		row += " " + prCell
	}
	return row
}

func (m Model) renderPrompt() string {
	var b strings.Builder
	b.WriteString("\n")
	if m.renaming {
		b.WriteString(bar() + " " + hintStyle.Render("rename") + "  " + titleStyle.Render(displaytext.Sanitize(m.input)) + brandStyle.Render(cursorGlyph()) + "\n")
	} else {
		b.WriteString(bar() + " " + titleStyle.Render("Agents") + "  " + hintStyle.Render("? / Esc close help") + "\n")
	}
	if m.message != "" {
		b.WriteString("  " + hintStyle.Render(displaytext.Sanitize(m.message)) + "\n")
	}
	return b.String()
}

func (m Model) contentWidth() int {
	if m.width <= 0 {
		return 96
	}
	return max(24, m.width-2)
}

func (m Model) tableWidths() (nameWidth, taskWidth int, showTask bool) {
	w := m.contentWidth()
	showTask = w >= 58
	if !showTask {
		return max(12, w-8), 0, false
	}
	nameWidth = min(30, max(14, w/3))
	taskWidth = max(16, w-nameWidth-8)
	return nameWidth, taskWidth, true
}

func (m Model) visibleSessionWindow() (int, int) {
	limit := len(m.sessions)
	if m.height <= 0 {
		return 0, limit
	}
	reserve := 20
	limit = min(len(m.sessions), max(3, m.height-reserve))
	start := 0
	if m.selected >= limit {
		start = m.selected - limit + 1
	}
	start = max(0, min(start, len(m.sessions)-limit))
	return start, start + limit
}

func promptText(sess adapter.Session) string {
	// Fall back to a liveness-derived label (never the raw "Failed" State enum)
	// so a reboot-survivor row doesn't read as failed when it has no prompt — it
	// is resumable, not broken (F30).
	return firstNonEmpty(sess.Prompt, livenessLabel(sess), "idle")
}

// taskSummaryText preserves the stored task while adding grounded process-exit
// detail once. Name-only compact rows render the detail separately instead.
func taskSummaryText(sess adapter.Session) string {
	detail := failureExitDetail(sess)
	if strings.TrimSpace(sess.Prompt) == "" && detail != "" {
		return detail
	}
	base := promptText(sess)
	if detail == "" || base == detail || strings.HasSuffix(base, " · "+detail) {
		return base
	}
	return base + dotSep() + detail
}

// boundedTaskSummary truncates the prompt portion first so grounded failure
// metadata remains visible at the right edge of narrow task/summary surfaces.
func boundedTaskSummary(sess adapter.Session, width int) string {
	detail := failureExitDetail(sess)
	if detail == "" || strings.TrimSpace(sess.Prompt) == "" {
		return truncate(taskSummaryText(sess), width)
	}
	suffix := dotSep() + detail
	base := promptText(sess)
	if base == detail {
		return truncate(detail, width)
	}
	base = strings.TrimSuffix(base, suffix)
	available := width - ansi.StringWidth(suffix)
	if available <= 0 {
		return truncate(detail, width)
	}
	return truncate(base, available) + suffix
}

// livenessLabel describes a prompt-less session by its liveness and Closed flag
// rather than its State enum.
func livenessLabel(sess adapter.Session) string {
	switch sess.ProcAlive {
	case adapter.Alive:
		return "running"
	default:
		return "resumable"
	}
}

// absCwd resolves a session's working directory to an absolute path.
func absCwd(cwd string) string {
	if cwd == "" {
		return "?"
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		return displaytext.Sanitize(abs)
	}
	return displaytext.Sanitize(cwd)
}

func (m Model) renderConfirm() string {
	m.ensureConfirmationForm()
	if m.confirmForm == nil {
		return ""
	}
	return m.confirmForm.View()
}

func (m Model) renderLatestConfirmation() string {
	m.ensureConfirmationForm()
	if m.confirmForm == nil {
		return ""
	}
	return m.confirmForm.View()
}

func (m Model) renderWizard() string {
	profileLabel := m.wizardProfileLabel()
	steps := []string{
		"provider — Tab cycles; Shift+Tab profile; Enter confirms:  " + firstNonEmpty(m.wizardAgent, m.defaultAgent) + "  profile=" + profileLabel,
		"command alias — blank uses provider default:  " + m.input,
		"working directory:  " + m.input,
		"#name prompt — both optional:  " + m.input,
	}
	step := m.wizardStep
	if step < 0 || step >= len(steps) {
		step = 0
	}
	var b strings.Builder
	b.WriteString("\n " + sectionStyle.Render("NEW SESSION") + "  " + hintStyle.Render(fmt.Sprintf("step %d of 4 · profile %s", step+1, profileLabel)) + "\n")
	b.WriteString("  " + titleStyle.Render(displaytext.Sanitize(steps[step])) + brandStyle.Render(cursorGlyph()) + "\n") // #nosec G602 -- step is clamped to [0, len(steps)) just above.
	switch step {
	case 2:
		// Warn when the chosen working directory is not inside a git repo: there
		// is no checkpoint to recover the agent's work from (C2-8).
		dir := firstNonEmpty(m.input, ".")
		if !isGitRepo(dir) {
			b.WriteString("  " + warnStyle.Render("⚠ not a git repo — no checkpoint to recover the agent's work") + "\n")
		}
		b.WriteString("  " + hintStyle.Render("Tab completes a path  ·  Esc cancels") + "\n")
	case 3:
		b.WriteString("  " + hintStyle.Render("Ctrl+G opens $EDITOR  ·  Esc cancels") + "\n")
	default:
		b.WriteString("  " + hintStyle.Render("Esc cancels") + "\n")
	}
	return b.String()
}

func (m Model) wizardProfileLabel() string {
	if m.wizardProfile != "" {
		return m.wizardProfile
	}
	if m.defaultProfile != "" {
		return "default:" + m.defaultProfile
	}
	return "default:none"
}

// sessionGlyph uses process liveness and recorded exit metadata rather than the
// broad State enum. Explicit stops remain neutral even when SIGTERM produced a
// negative compatibility exit code. Both the glyph and its style come from the
// tone table (theme.go), so every surface speaks one visual vocabulary and the
// glyph-distinctness invariant covers this path too.
func sessionGlyph(s adapter.Session) (string, lipgloss.Style) {
	t := toneForSession(s)
	return t.glyph, t.style()
}

func failureExitDetail(s adapter.Session) string {
	if s.ProcAlive != adapter.Exited || s.Closed || s.ExitCode == nil || *s.ExitCode == 0 {
		return ""
	}
	if *s.ExitCode < 0 {
		return "signal"
	}
	return fmt.Sprintf("exit %d", *s.ExitCode)
}

// prStatusDot returns a distinct glyph per PR status (not color-only) so the PR
// state survives a monochrome terminal or a screen scrape: open=hollow circle,
// merged=filled circle, draft=half circle, closed=cross (F26).
// prStatusDot and prStatusStyle both resolve through the tone table, which is
// what keeps a PR dot from being mistaken for a lifecycle dot: the table's
// distinctness invariant spans both families, so merged (◆) can never collide
// with live (●) the way the previous hand-written glyph sets did.
func prStatusDot(s adapter.PRStatus) string {
	if s == "" {
		return " "
	}
	return toneForPR(s).glyph
}

// prStatusStyle colours the PR dot by status. Colour is a secondary cue; the
// glyph in prStatusDot is the primary, color-independent signal (F26).
func prStatusStyle(s adapter.PRStatus) lipgloss.Style {
	return toneForPR(s).style()
}

// truncate clips s to at most n display columns, measuring with lipgloss.Width
// so multibyte and wide (CJK/emoji) runes are counted by the columns they
// occupy rather than their byte length. When clipping happens an ellipsis is
// appended and the result still fits within n columns (F28).
func truncate(s string, n int) string {
	s = displaytext.Sanitize(s)
	if n <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= n {
		return s
	}
	// Reserve one column for the ellipsis, then grow rune-by-rune until adding
	// the next rune would overflow the budget. This keeps wide runes intact and
	// never slices a multibyte sequence.
	budget := n - 1
	var b strings.Builder
	w := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if w+rw > budget {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	return b.String() + truncTail()
}

// padRight pads s with spaces to occupy exactly n display columns. If s already
// meets or exceeds n columns it is returned unchanged. Display-width padding
// keeps columns aligned when names contain wide runes, which byte-length-based
// fmt "%-*s" padding gets wrong (F28).
func padRight(s string, n int) string {
	if pad := n - lipgloss.Width(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}
