package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
	width, height int
	sizeKnown     bool
	quitting      bool
	loading       bool
	service       *Service
	sessions      []adapter.Session
	selected      int
	input         string
	filterActive  bool
	filterQuery   string
	filterRestore sessionIdentity
	filterSaved   bool
	defaultAgent  string
	message       string
	messageSetAt  time.Time
	peekOpen      bool
	peekText      string
	// peekTargetID is the session whose pane the open peek panel shows. While
	// peek is open the command line doubles as a reply composer: typed text +
	// Enter sends to this session via Service.Reply rather than dispatching a new
	// agent. Snapshotting the id (mirroring renameTargetID) keeps a reorder under
	// the cursor from misrouting the reply (F36).
	peekTargetID        string
	peekTargetAgent     string
	helpOpen            bool
	confirmStop         bool
	confirmStopID       string
	confirmStopAgent    string
	confirmLatest       bool
	confirmLatestAgent  string
	confirmLatestID     string
	confirmLatestName   string
	confirmLatestAction latestAction
	renaming            bool
	renameTargetID      string
	renameTargetAgent   string
	wizard              bool
	wizardStep          int
	wizardAgent         string
	wizardAlias         string
	wizardCwd           string
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
	// lastPeekAt records, per session id, when its pane was last captured for
	// the peek panel. The peek-focus ticker re-polls the focused session at most
	// once per peekFocusInterval; the map is keyed by id (not row index) because
	// rows reorder every refresh tick (C2-11). peekClock is the injectable clock
	// for that gate.
	lastPeekAt map[string]time.Time
	peekClock  func() time.Time
	// now is the presentation clock used for deterministic session-age labels.
	// Discovery refreshes LastChange on every scan, so the dashboard deliberately
	// derives age from CreatedAt instead.
	now func() time.Time
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

// peekTickInterval drives the peek-focus poll. The tick is what makes an open
// peek panel update live without coupling peek freshness to the slower 2s
// session refresh (C2-11).
const peekTickInterval = time.Second

// peekFocusInterval is the minimum spacing between captures of the focused
// session's pane while the peek panel is open. The peek-focus ticker fires
// every second; this gate (id-keyed) keeps a focused session from being
// captured faster than once per interval even if rows reorder under the cursor
// (C2-11).
const peekFocusInterval = time.Second

type sessionsLoadedMsg struct {
	sessions     []adapter.Session
	defaultAgent string
	groupByDir   bool
	err          error
}
type peekLoadedMsg struct {
	text string
	err  error
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

// peekTickMsg is the peek-focus poll tick. When the peek panel is open it
// re-captures the focused session's pane (rate-limited per id) so the panel
// follows live output; when closed it just re-arms (C2-11).
type peekTickMsg time.Time

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
	m := Model{service: NewService(st, reg), defaultAgent: store.DefaultAgentName, wizardCwd: ".", execProcess: tea.ExecProcess, lastPeekAt: map[string]time.Time{}, peekClock: time.Now}
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
	return tea.Batch(m.loadSessionsCmd(), refreshTick(), peekTick(), prRefreshTick(100*time.Millisecond))
}

func refreshTick() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return refreshMsg(t) })
}

func prRefreshTick(after time.Duration) tea.Cmd {
	return tea.Tick(after, func(t time.Time) tea.Msg { return prRefreshMsg(t) })
}

func peekTick() tea.Cmd {
	return tea.Tick(peekTickInterval, func(t time.Time) tea.Msg { return peekTickMsg(t) })
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
	return func() tea.Msg {
		if m.reloadSessions != nil {
			return m.reloadSessions()
		}
		sessions, cfg, err := m.service.LoadSessions(context.Background())
		return sessionsLoadedMsg{sessions: sessions, defaultAgent: cfg.DefaultAgent, groupByDir: cfg.UI.GroupByDir, err: err}
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
	case refreshMsg:
		next, startedLoad := m.refreshStep(time.Time(msg))
		// The ticker is re-armed unconditionally so refreshes never stop; the
		// load is added only when one wasn't already in flight (F17).
		if startedLoad {
			return next, tea.Batch(next.loadSessionsCmd(), refreshTick())
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
		return m, m.loadSessionsCmd()
	case peekTickMsg:
		// Re-arm the peek ticker unconditionally; only re-capture the focused
		// session when the panel is open and the per-id rate limit allows it
		// (C2-11).
		next, peekCmd := m.peekFocusStep(time.Time(msg))
		return next, tea.Batch(peekCmd, peekTick())
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
	case peekLoadedMsg:
		return m.handlePeekLoaded(msg), nil
	case dispatchedMsg:
		return m.handleDispatched(msg)
	case attachSpecMsg:
		return m, m.execAttachSpec(msg.spec, msg.err)
	case attachFinishedMsg:
		return m.handleAttachFinished(msg), tea.Batch(m.loadSessionsCmd(), tea.ClearScreen, tea.WindowSize())
	case latestRequiredMsg:
		if !errors.Is(msg.err, ErrAmbiguousResume) {
			m.setMessage(msg.err.Error())
			return m, nil
		}
		m.confirmLatest = true
		m.confirmLatestAction = msg.action
		m.confirmLatestAgent = msg.agent
		m.confirmLatestID = msg.id
		m.confirmLatestName = msg.name
		m.message = ""
		m.messageSetAt = time.Time{}
		return m, nil
	case promptEditedMsg:
		return m.handlePromptEdited(msg), nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleWindowSize(msg tea.WindowSizeMsg) Model {
	m.width, m.height = msg.Width, msg.Height
	m.sizeKnown = true
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
	// Clear the in-flight guard unconditionally — even on error — or a single
	// failed load wedges the refresh ticker forever (F17).
	m.loading = false
	if msg.err != nil {
		m.setMessage(msg.err.Error())
		return m
	}
	selectedAgent, selectedID := "", ""
	if sess, ok := m.selectedSession(); ok {
		selectedAgent, selectedID = sess.AgentType, sess.ID
	}
	if msg.sessions != nil {
		m.sessions = projectSessions(msg.sessions, msg.groupByDir)
		m.groupByDir = msg.groupByDir
		// Drop peek throttle stamps for sessions that no longer exist so the
		// map cannot grow without bound across many session lifetimes.
		live := make(map[string]struct{}, len(m.sessions))
		for _, sess := range m.sessions {
			live[sess.ID] = struct{}{}
		}
		for id := range m.lastPeekAt {
			if _, ok := live[id]; !ok {
				delete(m.lastPeekAt, id)
			}
		}
	}
	if msg.defaultAgent != "" {
		// A persisted default may name an agent whose CLI was since uninstalled;
		// reconcile it to an enabled provider rather than dispatching to a
		// disabled one (C2-9).
		m.defaultAgent = m.validateDefaultAgent(msg.defaultAgent)
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

func (m Model) handlePeekLoaded(msg peekLoadedMsg) Model {
	if msg.err != nil {
		m.setMessage(msg.err.Error())
	} else {
		m.peekText = msg.text
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

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if handled, model, cmd := m.handleModalKey(msg, key); handled {
		return model, cmd
	}
	// Peek owns the command composer for replies. Keep the filtered projection,
	// but route text/Enter/Esc through the established reply flow while it is open.
	if m.filterActive && !m.peekOpen {
		if handled, cmd := m.handleFilterKey(msg, key); handled {
			return m, cmd
		}
	}
	if key == "/" && m.input == "" && !m.peekOpen {
		m.enterFilter()
		return m, nil
	}
	if handled, cmd := m.handleMovementKey(key); handled {
		return m, cmd
	}
	if handled, cmd := m.handleActionKey(key); handled {
		return m, cmd
	}
	m.appendKeyInput(msg)
	return m, nil
}

func (m Model) handleModalKey(msg tea.KeyMsg, key string) (bool, tea.Model, tea.Cmd) {
	if m.helpOpen {
		if key == "?" || key == "esc" {
			m.helpOpen = false
		}
		return true, m, nil
	}
	if m.confirmLatest {
		if key == "y" || key == "enter" {
			action, agentName, id := m.confirmLatestAction, m.confirmLatestAgent, m.confirmLatestID
			m.clearLatestConfirmation()
			return true, m, m.retryLatestCmd(action, agentName, id)
		}
		if key == "n" || key == "esc" {
			m.clearLatestConfirmation()
		}
		return true, m, nil
	}
	if m.confirmStop {
		if key == "y" || key == "enter" {
			m.confirmStop = false
			agentName, id := m.confirmStopAgent, m.confirmStopID
			m.confirmStopAgent = ""
			m.confirmStopID = ""
			return true, m, m.stopTargetExactCmd(agentName, id, true)
		}
		if key == "r" {
			m.confirmStop = false
			agentName, id := m.confirmStopAgent, m.confirmStopID
			m.confirmStopAgent = ""
			m.confirmStopID = ""
			return true, m, m.restartTargetExactCmd(agentName, id)
		}
		if key == "n" || key == "esc" {
			m.confirmStop = false
			m.confirmStopAgent = ""
			m.confirmStopID = ""
		}
		return true, m, nil
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
		return true, m.moveSelectionPeek(-1)
	case "down":
		return true, m.moveSelectionPeek(1)
	case "shift+up":
		return true, m.moveSession(-1)
	case "shift+down":
		return true, m.moveSession(1)
	}
	return false, nil
}

// moveSelectionPeek moves the cursor and, when the peek panel is open and the
// selection actually changed, re-fires the peek for the newly selected session.
// The stale peek text is blanked synchronously so the panel never shows a frame
// of the previous session's tail. Gated on peekOpen so plain navigation with the
// panel closed doesn't trigger an N+1 capture storm (C2-2).
func (m *Model) moveSelectionPeek(delta int) tea.Cmd {
	prev := m.selected
	m.moveSelection(delta)
	if !m.peekOpen || m.selected == prev {
		return nil
	}
	// The peek panel follows the cursor; keep the reply target in sync so a reply
	// goes to the session the user is actually looking at (F36).
	if sess, ok := m.selectedSession(); ok {
		m.peekTargetAgent = sess.AgentType
		m.peekTargetID = sess.ID
	}
	m.peekText = ""
	return m.peekSelectedCmd()
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

// peekFocusStep handles a peek-focus tick: when the panel is open it re-captures
// the focused session's pane at most once per peekFocusInterval (gated by id so
// a reorder under the cursor can't double-capture), keeping the panel live. It
// returns the (possibly nil) peek command; the caller re-arms the ticker (C2-11).
func (m Model) peekFocusStep(now time.Time) (Model, tea.Cmd) {
	if m.peekClock != nil {
		now = m.peekClock()
	}
	if !m.peekOpen {
		return m, nil
	}
	sess, ok := m.selectedSession()
	if !ok {
		return m, nil
	}
	if !m.shouldPollFocusedPeek(sess.ID, now) {
		return m, nil
	}
	if m.lastPeekAt == nil {
		m.lastPeekAt = map[string]time.Time{}
	}
	m.lastPeekAt[sess.ID] = now
	return m, m.peekSelectedCmd()
}

// shouldPollFocusedPeek reports whether the focused session id is due for a
// peek capture: never polled, or last polled at least peekFocusInterval ago.
// Keyed by id so the rate limit follows the session, not the row index (C2-11).
func (m Model) shouldPollFocusedPeek(id string, now time.Time) bool {
	if id == "" {
		return false
	}
	last, seen := m.lastPeekAt[id]
	if !seen {
		return true
	}
	return now.Sub(last) >= peekFocusInterval
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
		// having fired doesn't lose the manual order (F59).
		return true, tea.Batch(m.flushReorder(), tea.Quit)
	case "tab":
		m.cycleDefaultAgent()
		return true, m.persistDefaultAgent()
	case "?":
		m.helpOpen = true
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
			m.confirmStop = true
			m.confirmStopAgent = sess.AgentType
			m.confirmStopID = sess.ID
		}
	case " ":
		return true, m.handleSpaceKey(key)
	case "right", "enter":
		return true, m.handleEnterKey()
	case "esc":
		return true, m.handleEscKey()
	case "backspace":
		m.backspaceInput()
	case "e":
		m.handleEditKey(key)
	default:
		return false, nil
	}
	return true, nil
}

// handleEscKey makes Esc back out one level per press: close the peek panel,
// then clear the command input, and finally quit the uam TUI.
func (m *Model) handleEscKey() tea.Cmd {
	if m.peekOpen {
		// Esc backs out of the peek/reply composer WITHOUT sending the in-progress
		// reply (F36).
		m.peekOpen = false
		m.peekTargetAgent = ""
		m.peekTargetID = ""
		return nil
	}
	if m.input != "" {
		m.input = ""
		return nil
	}
	m.quitting = true
	// Flush a pending reorder before exiting (F59).
	return tea.Batch(m.flushReorder(), tea.Quit)
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

func (m *Model) handleSpaceKey(key string) tea.Cmd {
	if strings.TrimSpace(m.input) != "" || len(m.sessions) == 0 {
		m.input += key
		return nil
	}
	// A stopped session has no live process to peek into — Space restarts it
	// in the background instead.
	if sess, ok := m.selectedSession(); ok && sess.ProcAlive == adapter.Exited {
		m.setMessage("restarting " + firstNonEmpty(sess.DisplayName, sess.ID))
		return m.resumeSelectedCmd()
	}
	m.peekOpen = !m.peekOpen
	if m.peekOpen {
		// Snapshot the peeked session so an Enter-to-reply routes to it even if a
		// refresh reorders the list under the cursor (F36).
		if sess, ok := m.selectedSession(); ok {
			m.peekTargetAgent = sess.AgentType
			m.peekTargetID = sess.ID
		}
		return m.peekSelectedCmd()
	}
	m.peekTargetAgent = ""
	m.peekTargetID = ""
	return nil
}

func (m *Model) handleEnterKey() tea.Cmd {
	// Reply sub-mode: while the peek panel is open the command line is a reply
	// composer. Non-empty input + Enter sends to the peeked session via
	// Service.Reply and re-peeks, instead of dispatching a new agent. Checked
	// before the dispatch/attach branch so peek+typed-text never spawns a session
	// (F36).
	if m.peekOpen && strings.TrimSpace(m.input) != "" {
		return m.replyToPeekCmd()
	}
	if strings.TrimSpace(m.input) != "" {
		spec := parseDispatchSpec(m.input, m.defaultAgent)
		return m.dispatchNamedCmd(spec.Agent, spec.Alias, spec.Name, spec.Prompt)
	}
	if len(m.sessions) > 0 {
		return m.attachSelectedCmd()
	}
	return nil
}

// replyToPeekCmd sends the typed input to the peeked session via Service.Reply,
// clears the composer, and re-peeks so the panel shows the agent's response. The
// reply target is the snapshotted peekTargetID (falling back to the selected
// session) so a reorder under the cursor can't misroute it (F36).
func (m *Model) replyToPeekCmd() tea.Cmd {
	sess, ok := m.sessionByIdentity(m.peekTargetAgent, m.peekTargetID)
	if !ok {
		return nil
	}
	text := m.input
	m.input = ""
	agentName, id := sess.AgentType, sess.ID
	return func() tea.Msg {
		if err := m.service.ReplyExact(context.Background(), agentName, id, text); err != nil {
			return peekLoadedMsg{err: err}
		}
		p, err := m.service.PeekExact(context.Background(), agentName, id)
		return peekLoadedMsg{text: p.TailText, err: err}
	}
}

func (m *Model) handleEditKey(key string) {
	if strings.TrimSpace(m.input) != "" {
		m.input += key
		return
	}
	m.wizard = true
	m.wizardStep = 0
	m.input = ""
	m.wizardAlias = ""
	m.wizardCwd = "."
}

func (m *Model) backspaceInput() {
	if r := []rune(m.input); len(r) > 0 {
		m.input = string(r[:len(r)-1])
	}
}

func (m *Model) appendKeyInput(msg tea.KeyMsg) {
	m.editText(msg)
}

func (m Model) handleRenameKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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

// editText applies a printable-input edit to m.input: it appends pasted/typed
// runes (KeyRunes without Alt — covers multibyte and bracketed paste), handles
// Space and Backspace, and ignores Alt-chords and control keys so they never
// leak as literal text (F29).
func (m *Model) editText(msg tea.KeyMsg) {
	switch {
	case msg.Type == tea.KeyBackspace:
		m.backspaceInput()
	case msg.Type == tea.KeySpace:
		m.input += " "
	case msg.Type == tea.KeyRunes && !msg.Alt:
		m.input += string(msg.Runes)
	}
}

func (m Model) handleWizardKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
		return m.persistDefaultAgent(), true
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
		return m.dispatchWithNameCwdCmd(spec.Agent, spec.Alias, spec.Name, spec.Prompt, cwd), true
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
		if _, statErr := os.Stat(filepath.Join(d, ".git")); statErr == nil {
			return true
		}
		parent := filepath.Dir(d)
		if parent == d {
			return false
		}
		d = parent
	}
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
	return m.dispatchWithNameCwdCmd(agent, alias, name, prompt, "")
}
func (m Model) dispatchWithNameCwdCmd(agent, alias, name, prompt, cwd string) tea.Cmd {
	return func() tea.Msg {
		sess, err := m.service.DispatchNamedWithAlias(context.Background(), agent, alias, name, prompt, cwd, string(store.ModeYolo))
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

func (m Model) peekSelectedCmd() tea.Cmd {
	sess, ok := m.selectedSession()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		p, err := m.service.PeekExact(context.Background(), sess.AgentType, sess.ID)
		return peekLoadedMsg{text: p.TailText, err: err}
	}
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
		a, ok := m.service.Registry.Get(sess.AgentType)
		if !ok {
			return sessionsLoadedMsg{err: fmt.Errorf("agent %q unavailable", sess.AgentType)}
		}
		spec, err := a.Attach(sess.ID)
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
	cmd.Env = append(os.Environ(), session.AttachQuietEnv+"=1")
	return runner(cmd, func(err error) tea.Msg { return attachFinishedMsg{err: err} })
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

// ─── theme ───────────────────────────────────────────────────────────────
// Borderless adaptive palette: one teal accent, semantic status colors, and
// AdaptiveColor everywhere so the UI reads well on light and dark terminals.

var (
	accentColor  = lipgloss.AdaptiveColor{Light: "#0F766E", Dark: "#2DD4BF"}
	textColor    = lipgloss.AdaptiveColor{Light: "#0F172A", Dark: "#E8EDF4"}
	mutedColor   = lipgloss.AdaptiveColor{Light: "#64748B", Dark: "#8B97AC"}
	dividerColor = lipgloss.AdaptiveColor{Light: "#D6DEE8", Dark: "#2B3547"}
	taskColor    = lipgloss.AdaptiveColor{Light: "#475569", Dark: "#AEBACD"}
	liveColor    = lipgloss.AdaptiveColor{Light: "#047857", Dark: "#34D399"}
	failColor    = lipgloss.AdaptiveColor{Light: "#DC2626", Dark: "#F87171"}
	warnColor    = lipgloss.AdaptiveColor{Light: "#B45309", Dark: "#FBBF24"}
)

var (
	brandStyle    = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(textColor)
	sectionStyle  = lipgloss.NewStyle().Bold(true).Foreground(mutedColor)
	hintStyle     = lipgloss.NewStyle().Foreground(mutedColor)
	dividerStyle  = lipgloss.NewStyle().Foreground(dividerColor)
	taskStyle     = lipgloss.NewStyle().Foreground(taskColor)
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	warnStyle     = lipgloss.NewStyle().Foreground(warnColor)
)

// bar is the accent rule that marks the brand and command lines.
func bar() string { return brandStyle.Render("▌") }

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
// derived from the existing interaction state, keeping wizard/peek behavior as
// the source of truth for all existing key flows.
type DashboardMode uint8

const (
	ModeOperations DashboardMode = iota
	ModePeek
	ModeNew
)

func (m Model) layoutClass() LayoutClass {
	w, h := m.width, m.height
	if w <= 0 {
		w = 98
	}
	// An unknown height means callers are rendering a component outside a real
	// terminal (many legacy unit tests do this), so classify by width only.
	if h > 0 && (w < 58 || h < 24) || w < 58 {
		return LayoutCompact
	}
	if w >= 96 && (h == 0 || h >= 28) {
		return LayoutWide
	}
	return LayoutStandard
}

func (m Model) dashboardMode() DashboardMode {
	if m.wizard {
		return ModeNew
	}
	if m.peekOpen {
		return ModePeek
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

func (m Model) View() string {
	if m.quitting {
		return ""
	}
	// Before Bubble Tea sends its first WindowSizeMsg, keep the first frame small
	// and stable instead of flashing the legacy unbounded dashboard.
	if !m.sizeKnown {
		// A confirmation can arrive before a WindowSizeMsg in tests and on very
		// slow remote terminals. Never hide a safety-critical modal behind loading.
		if m.helpOpen || m.confirmLatest || m.confirmStop || m.wizard || m.renaming {
			return m.unboundedView()
		}
		return bar() + " " + brandStyle.Render("UAM") + "  " + hintStyle.Render("loading dashboard…")
	}
	return m.dashboardView()
}

func (m Model) unboundedView() string {
	var b strings.Builder
	b.WriteString(m.renderBranding())
	switch {
	case m.helpOpen:
		b.WriteString(m.renderHelp())
	case m.confirmLatest:
		b.WriteString(m.renderLatestConfirmation())
	case m.confirmStop:
		b.WriteString(m.renderConfirm())
	case m.wizard:
		b.WriteString(m.renderWizard())
	default:
		b.WriteString(m.renderDetails())
		b.WriteString(m.renderTable())
		if m.peekOpen {
			b.WriteString(m.renderPeek())
		}
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
		prompt = []string{bar() + " " + brandStyle.Render("›")}
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
	return []string{
		ansi.Truncate(bar()+" "+hintStyle.Render("new")+" "+brandStyle.Render("›")+" "+titleStyle.Render(field)+brandStyle.Render("▏"), width, "…"),
		ansi.Truncate("  "+hintStyle.Render(hints[step]), width, "…"),
	}
}

func (m Model) responsiveHeader(width int) string {
	text := bar() + " " + brandStyle.Render("UAM") + "  " + hintStyle.Render("Unified Agent Manager")
	if m.layoutClass() != LayoutCompact {
		text += "  " + hintStyle.Render(version.String())
	}
	return ansi.Truncate(text, width, "…")
}

func (m Model) responsiveBody(width, budget int) []string {
	if budget <= 0 {
		return nil
	}
	if m.helpOpen {
		return takeLines(boundedNonBlankLines(m.renderHelp(), width), budget)
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

// boundedTailLines scans backward only far enough to find the requested tail.
// It retains empty physical lines because terminal output spacing is content,
// while avoiding a split/join of a potentially multi-thousand-line pane.
func boundedTailLines(s string, n, width int) []string {
	if n <= 0 || s == "" {
		return nil
	}
	start, breaks := 0, 0
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] != '\n' {
			continue
		}
		breaks++
		if breaks == n {
			start = i + 1
			break
		}
	}
	lines := strings.Split(s[start:], "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], width, "…")
	}
	return lines
}

func (m Model) renderSectionAtWidth(label, right string, width int) string {
	head := sectionStyle.Render(label)
	rightWidth := ansi.StringWidth(right)
	fill := max(0, width-ansi.StringWidth(head)-rightWidth-4)
	line := " " + head
	if fill > 0 {
		line += "  " + dividerStyle.Render(strings.Repeat("─", fill))
	}
	if right != "" {
		line += " " + hintStyle.Render(right)
	}
	return ansi.Truncate(line, width, "…")
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
		lines = append(lines, ansi.Truncate(line, width, "…"))
	}
	return lines
}

func joinColumns(left, right []string, leftWidth, rightWidth, budget int) []string {
	n := min(budget, max(len(left), len(right)))
	lines := make([]string, 0, n)
	for i := 0; i < n; i++ {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		lines = append(lines, padRightANSI(l, leftWidth)+"   "+ansi.Truncate(r, rightWidth, "…"))
	}
	return lines
}

func padRightANSI(s string, width int) string {
	s = ansi.Truncate(s, width, "…")
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
		lines[i] = ansi.Truncate(lines[i], width, "…")
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
	line := " " + head + "  " + dividerStyle.Render(strings.Repeat("─", fill))
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
	if !sess.CreatedAt.IsZero() {
		b.WriteString("    " + hintStyle.Render("created: "+sess.CreatedAt.Format("Jan 02 15:04")) + "\n")
	}
	b.WriteString("    " + hintStyle.Render("cwd: "+absCwd(sess.Cwd)) + "\n")
	return b.String()
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

func (m Model) renderPeek() string {
	return "\n" + m.renderSection("PEEK", "") + "\n" + trimLines(m.peekText, max(5, m.height/3)) + "\n"
}

func (m Model) renderPrompt() string {
	var b strings.Builder
	b.WriteString("\n")
	if m.renaming {
		b.WriteString(bar() + " " + hintStyle.Render("rename") + "  " + titleStyle.Render(displaytext.Sanitize(m.input)) + brandStyle.Render("▏") + "\n")
	} else if m.peekOpen {
		// The command line doubles as a reply composer while peek is open: label
		// it so the sub-mode is discoverable (Enter sends, Esc closes) (F36).
		field := hintStyle.Render("type a reply…")
		if m.input != "" {
			field = titleStyle.Render(displaytext.Sanitize(m.input))
		}
		hints := hintStyle.Render("Enter send  ·  Esc close")
		b.WriteString(bar() + " " + hintStyle.Render("reply") + " " + brandStyle.Render("›") + " " + field + brandStyle.Render("▏") + "   " + hints + "\n")
	} else {
		field := hintStyle.Render("type a command…")
		if m.input != "" {
			field = titleStyle.Render(displaytext.Sanitize(m.input))
		}
		hints := hintStyle.Render(m.defaultAgent + "  ·  ? help  ·  e new  ·  Esc quit")
		b.WriteString(bar() + " " + brandStyle.Render("›") + " " + field + brandStyle.Render("▏") + "   " + hints + "\n")
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
	if m.peekOpen {
		reserve += max(5, m.height/3) + 2
	}
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
	return base + " · " + detail
}

// boundedTaskSummary truncates the prompt portion first so grounded failure
// metadata remains visible at the right edge of narrow task/summary surfaces.
func boundedTaskSummary(sess adapter.Session, width int) string {
	detail := failureExitDetail(sess)
	if detail == "" || strings.TrimSpace(sess.Prompt) == "" {
		return truncate(taskSummaryText(sess), width)
	}
	suffix := " · " + detail
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

func (m Model) renderHelp() string {
	rows := []string{
		"↑/↓  move   Shift+↑/↓  reorder   Enter/→  attach/resume",
		"Space  peek running / resume stopped",
		"/  filter sessions when the command line is empty",
		"Tab  cycle agent     Ctrl+T  pin        Ctrl+R  rename",
		"Ctrl+X  stop+remove / restart    Ctrl+S  group-by-dir",
		"e  new session       Esc  quit",
		"in session:  ← detach (when input empty)    Ctrl+B d  detach",
		"dispatch:  @agent:alias #name prompt   (alias, name & prompt optional)",
	}
	var b strings.Builder
	b.WriteString("\n " + sectionStyle.Render("Keys:") + "\n")
	for _, r := range rows {
		b.WriteString("  " + hintStyle.Render(r) + "\n")
	}
	return b.String()
}

func (m Model) renderConfirm() string {
	sess, _ := m.sessionByIdentity(m.confirmStopAgent, m.confirmStopID)
	name := displaytext.Sanitize(firstNonEmpty(sess.DisplayName, sess.ID, "session"))
	return "\n " + sectionStyle.Render("Stop session") + "\n  " +
		hintStyle.Render("Stop and remove ") + titleStyle.Render(name) + hintStyle.Render("?") +
		"   " + brandStyle.Render("y") + hintStyle.Render(" / restart ") + brandStyle.Render("r") + hintStyle.Render(" / ") + titleStyle.Render("N") + "\n"
}

func (m Model) renderLatestConfirmation() string {
	provider := displaytext.Sanitize(firstNonEmpty(m.confirmLatestAgent, "provider"))
	name := displaytext.Sanitize(firstNonEmpty(m.confirmLatestName, m.confirmLatestID, "session"))
	return "\n " + sectionStyle.Render("Confirm latest conversation") + "\n  " +
		hintStyle.Render("Several retained conversations share provider ") + titleStyle.Render(provider) +
		hintStyle.Render(" and this workspace. Continuing ") + titleStyle.Render(string(m.confirmLatestAction)+" "+name) +
		hintStyle.Render(" may select the provider's latest conversation.") +
		"   " + brandStyle.Render("y/Enter") + hintStyle.Render(" continue · n/Esc cancel") + "\n"
}

func (m Model) renderWizard() string {
	steps := []string{
		"provider — Tab cycles, Enter confirms:  " + firstNonEmpty(m.wizardAgent, m.defaultAgent),
		"command alias — blank uses provider default:  " + m.input,
		"working directory:  " + m.input,
		"#name prompt — both optional:  " + m.input,
	}
	step := m.wizardStep
	if step < 0 || step >= len(steps) {
		step = 0
	}
	var b strings.Builder
	b.WriteString("\n " + sectionStyle.Render("NEW SESSION") + "  " + hintStyle.Render(fmt.Sprintf("step %d of 4", step+1)) + "\n")
	b.WriteString("  " + titleStyle.Render(displaytext.Sanitize(steps[step])) + brandStyle.Render("▏") + "\n") // #nosec G602 -- step is clamped to [0, len(steps)) just above.
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

// liveGlyphStyle / failGlyphStyle are hoisted to package vars so renderRow does
// not allocate a fresh lipgloss.Style per row per frame. They keep AdaptiveColor
// (resolved at render time, not pre-baked) so the palette still adapts to
// light/dark terminals (F58).
var (
	liveGlyphStyle = lipgloss.NewStyle().Bold(true).Foreground(liveColor)
	failGlyphStyle = lipgloss.NewStyle().Bold(true).Foreground(failColor)
)

// sessionGlyph uses process liveness and recorded exit metadata rather than the
// broad State enum. Explicit stops remain neutral even when SIGTERM produced a
// negative compatibility exit code.
func sessionGlyph(s adapter.Session) (string, lipgloss.Style) {
	switch {
	case s.ProcAlive == adapter.Alive:
		return "⟳", liveGlyphStyle
	case failureExitDetail(s) != "":
		return "!", failGlyphStyle
	default:
		// Clean exits and explicit stops are both stopped and resumable. An
		// explicit SIGTERM is not a provider failure merely because its stored
		// compatibility exit code is -1.
		return "◦", hintStyle
	}
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
func prStatusDot(s adapter.PRStatus) string {
	switch s {
	case adapter.PROpen:
		return "○"
	case adapter.PRMerged:
		return "●"
	case adapter.PRDraft:
		return "◐"
	case adapter.PRClosed:
		return "✕"
	default:
		return " "
	}
}

// prStatusStyle colours the PR dot by status. Colour is a secondary cue; the
// glyph in prStatusDot is the primary, color-independent signal (F26).
func prStatusStyle(s adapter.PRStatus) lipgloss.Style {
	switch s {
	case adapter.PRMerged:
		return liveGlyphStyle
	case adapter.PRClosed:
		return failGlyphStyle
	default:
		return hintStyle
	}
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
	return b.String() + "…"
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
func trimLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
