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
	// The launch pad replaces the modal wizard: it expands in place under the
	// header and shares the input buffer with rename.
	launchOpen          bool
	launchField         launchField
	launchAgent         string
	launchAgentExplicit bool
	launchProfile       string
	launchDir           string
	launchName          string
	launchPrompt        string
	// launchCwd is the process working directory captured once at startup; it
	// is the default directory for new sessions. homeDir shortens paths to ~.
	launchCwd string
	homeDir   string
	// lastAttached is the session most recently opened this run. It answers
	// the 0 key and places the cursor after a detach. In-memory only.
	lastAttached      sessionIdentity
	profileNames      []string
	profileProviders  map[string]string
	defaultProfile    string
	profileBySession  map[sessionIdentity]string
	lastSeenBySession map[sessionIdentity]time.Time
	groupByDir        bool
	execProcess       func(*exec.Cmd, tea.ExecCallback) tea.Cmd
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
	spec     adapter.AttachSpec
	err      error
	identity sessionIdentity
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

// promptEditedMsg carries the result of editing the launch prompt in $EDITOR.
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
		profileProviders: map[string]string{},
		profileBySession: map[sessionIdentity]string{}, lastSeenBySession: map[sessionIdentity]time.Time{}, execProcess: tea.ExecProcess,
		activity:       spinner.New(spinner.WithSpinner(spinner.Line), spinner.WithStyle(brandStyle)),
		loading:        true,
		darkBackground: compat.HasDarkBackground,
		sessionLoads:   &sessionLoadCoordinator{},
	}
	if cwd, err := os.Getwd(); err == nil {
		m.launchCwd = cwd
	}
	if home, err := os.UserHomeDir(); err == nil {
		m.homeDir = home
	}
	// The baked-in OpenCode default may not be installed; reconcile it to an
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
		if msg.err == nil && msg.identity != (sessionIdentity{}) {
			m.lastAttached = msg.identity
		}
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
	// Land on the door you just left so re-entry is one keystroke.
	for i, sess := range m.sessions {
		if sessionKey(sess) == m.lastAttached {
			m.selected = i
			break
		}
	}
	return m
}

// handlePromptEdited loads the text the user composed in $EDITOR back into the
// launch pad prompt buffer. On error the buffer is left untouched and the error is
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
	// launch pad, rename and both confirmations with no way out but Esc — and no
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
	// filter, launch pad, rename, and confirmation input was routed above.
	return m, nil
}

func (m Model) handleModalKey(msg tea.KeyPressMsg, key string) (bool, tea.Model, tea.Cmd) {
	if m.confirmLatest || m.confirmStop {
		return m.handleConfirmationKey(msg, key)
	}
	if m.launchOpen {
		model, cmd := m.handleLaunchKey(msg)
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
			return attachSpecMsg{spec: spec, err: err, identity: sessionIdentity{agent: agentName, id: id}}
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

// handleMovementKey walks the deck. Up and down step by one row (the column
// count), left and right by one stamp, page keys by one page; shifted arrows
// reorder along the same axes.
func (m *Model) handleMovementKey(key string) (bool, tea.Cmd) {
	g := m.deckGeometry()
	switch key {
	case "up":
		m.moveSelection(-g.cols)
	case "down":
		m.moveSelection(g.cols)
	case "left":
		m.moveSelection(-1)
	case "right":
		m.moveSelection(1)
	case "pgup":
		m.moveSelection(-g.perPage())
	case "pgdown":
		m.moveSelection(g.perPage())
	case "home":
		m.moveSelection(-len(m.sessions))
	case "end":
		m.moveSelection(len(m.sessions))
	case "shift+up":
		return true, m.moveSession(-g.cols)
	case "shift+down":
		return true, m.moveSession(g.cols)
	case "shift+left":
		return true, m.moveSession(-1)
	case "shift+right":
		return true, m.moveSession(1)
	default:
		return false, nil
	}
	return true, nil
}

// moveSelection moves the cursor by delta visible positions, clamped to the
// ends of the deck so a row or page step off the edge lands on the last or
// first stamp instead of doing nothing.
func (m *Model) moveSelection(delta int) {
	visible := m.visibleSessionIndices()
	if len(visible) == 0 {
		return
	}
	position := positionOf(visible, m.selected)
	next := max(0, min(position+delta, len(visible)-1))
	m.selected = visible[next]
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

// warpToDoor selects the stamp behind a door digit on the visible page and
// opens it. Digits past the page's last stamp do nothing.
func (m *Model) warpToDoor(door int) tea.Cmd {
	visible := m.visibleSessionIndices()
	start, end := m.deckGeometry().pageBounds(len(visible), positionOf(visible, m.selected))
	slot := start + door - 1
	if door < 1 || door > deckDoors || slot >= end {
		return nil
	}
	m.selected = visible[slot]
	return m.attachSelectedCmd()
}

// warpBack reopens the session most recently attached this run.
func (m *Model) warpBack() tea.Cmd {
	for i, sess := range m.sessions {
		if sessionKey(sess) == m.lastAttached && m.sessionMatchesFilter(sess) {
			m.selected = i
			return m.attachSelectedCmd()
		}
	}
	m.setMessage("no session opened yet this run")
	return nil
}

// moveSessionTo applies the shared identity-safe reorder invariants between two
// canonical indices. Both normal and filtered navigation resolve their target
// index before entering this path, so hidden rows are never mistaken for the
// selected session.
func (m *Model) moveSessionTo(next int) tea.Cmd {
	if m.selected < 0 || m.selected >= len(m.sessions) || next < 0 || next >= len(m.sessions) || next == m.selected {
		return nil
	}
	// SortSessions buckets rows by Pinned before honoring SortIndex. A swap
	// that crosses the boundary is undone on the next refresh (the row snaps
	// back to its partition), so reject it and give honest feedback instead of
	// a move that silently reverts (F34).
	if !samePartition(m.sessions[m.selected], m.sessions[next]) {
		m.setMessage("can't reorder across the pinned boundary")
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
// partition — they share the same Pinned flag. Only within a partition does
// SortIndex (and therefore a manual reorder) take effect (F34). Liveness is
// deliberately not a partition: a session keeps its door when it stops.
func samePartition(a, b adapter.Session) bool {
	return a.Pinned == b.Pinned
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
	case "enter":
		return true, m.handleEnterKey()
	case "esc":
		return true, m.handleEscKey()
	case "backspace":
		// No text field exists on the base dashboard.
	case "n", "e":
		m.openLaunchPad()
	case "0":
		return true, m.warpBack()
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		return true, m.warpToDoor(int(key[0] - '0'))
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
	if (m.launchOpen && m.launchField != launchFieldProvider) || m.renaming {
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

// editPromptCmd composes the launch pad prompt in $EDITOR. It seeds a temp file with
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
// root. Used to warn in the launch pad when dispatching outside a repo means there is
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
// must not suppress the launch pad's no-checkpoint warning.
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
		return attachSpecMsg{spec: spec, err: err, identity: sessionKey(sess)}
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
			return attachSpecMsg{spec: spec, err: err, identity: sessionKey(sess)}
		}
		spec, err := m.service.AttachSpecExact(context.Background(), sess.AgentType, sess.ID)
		return attachSpecMsg{spec: spec, err: err, identity: sessionKey(sess)}
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

func (m Model) View() tea.View {
	var view tea.View
	switch {
	case m.quitting:
		view = tea.NewView("")
	case !m.sizeKnown:
		// Before Bubble Tea sends its first WindowSizeMsg, keep the first frame
		// small and stable. A confirmation can arrive before a WindowSizeMsg in
		// tests and on very slow remote terminals; never hide it behind loading.
		view = tea.NewView(lipgloss.Sprint(m.preSizeView()))
	case m.confirmationActive():
		frame := m.buildConfirmationOverlay()
		view = tea.NewView(lipgloss.Sprint(frame.content))
		view.OnMouse = frame.mouseCommand
	default:
		frame := m.buildDashboardFrame()
		view = tea.NewView(lipgloss.Sprint(frame.content))
		view.OnMouse = func(msg tea.MouseMsg) tea.Cmd { return m.dashboardMouseCommand(frame, msg) }
	}
	view.AltScreen = true
	if MouseReportingEnabled() {
		view.MouseMode = tea.MouseModeCellMotion
	}
	return view
}

func padRightANSI(s string, width int) string {
	s = ansi.Truncate(s, width, truncTail())
	return s + strings.Repeat(" ", max(0, width-ansi.StringWidth(s)))
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
// prStatusDot resolves through the tone table, which is what keeps a PR dot
// from being mistaken for a lifecycle dot: the table's distinctness invariant
// spans both families, so merged (◆) can never collide with live (●) the way
// the previous hand-written glyph sets did.
func prStatusDot(s adapter.PRStatus) string {
	if s == "" {
		return " "
	}
	return toneForPR(s).glyph
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

// preSizeView is the frame shown before the first WindowSizeMsg arrives.
func (m Model) preSizeView() string {
	if m.confirmationActive() {
		m.ensureConfirmationForm()
		if m.confirmForm != nil {
			return m.confirmForm.View()
		}
	}
	return m.activityView() + " " + hintStyle.Render("Loading sessions")
}
