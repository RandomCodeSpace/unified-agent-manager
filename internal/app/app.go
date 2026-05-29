package app

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/claude"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/codex"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/copilot"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/opencode"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/tmux"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/version"
)

type Model struct {
	width, height  int
	quitting       bool
	service        *Service
	sessions       []adapter.Session
	selected       int
	input          string
	defaultAgent   string
	message        string
	messageSetAt   time.Time
	peekOpen       bool
	peekText       string
	helpOpen       bool
	confirmStop    bool
	confirmStopID  string
	renaming       bool
	renameTargetID string
	wizard         bool
	wizardStep     int
	wizardAgent    string
	wizardCwd      string
	groupByDir     bool
	execProcess    func(*exec.Cmd, tea.ExecCallback) tea.Cmd
}

// messageTTL is how long a status/error line stays on screen before a refresh
// tick clears it. A just-emitted message must survive at least one 2s tick, so
// the TTL is several ticks long (F53).
const messageTTL = 8 * time.Second

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
type refreshMsg time.Time

func New() Model {
	st, _ := store.Open(store.DefaultPath())
	client := tmux.New("uam")
	reg := adapter.NewRegistry([]adapter.AgentAdapter{claude.New(client), codex.New(client), copilot.New(client), opencode.New(client)})
	return NewWithDeps(st, reg)
}

func NewWithDeps(st *store.Store, reg *adapter.Registry) Model {
	return Model{service: NewService(st, reg), defaultAgent: "claude", wizardCwd: ".", execProcess: tea.ExecProcess}
}
func NewWizard(st *store.Store, reg *adapter.Registry) Model {
	m := NewWithDeps(st, reg)
	m.wizard = true
	return m
}

func (m Model) Init() tea.Cmd { return tea.Batch(m.loadSessionsCmd(), refreshTick()) }

func refreshTick() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return refreshMsg(t) })
}

func (m Model) loadSessionsCmd() tea.Cmd {
	return func() tea.Msg {
		sessions, cfg, err := m.service.LoadSessions(context.Background())
		return sessionsLoadedMsg{sessions: sessions, defaultAgent: cfg.DefaultAgent, groupByDir: cfg.UI.GroupByDir, err: err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.handleWindowSize(msg), nil
	case refreshMsg:
		m.expireMessage(time.Time(msg))
		return m, tea.Batch(m.loadSessionsCmd(), refreshTick())
	case sessionsLoadedMsg:
		return m.handleSessionsLoaded(msg), nil
	case peekLoadedMsg:
		return m.handlePeekLoaded(msg), nil
	case dispatchedMsg:
		return m.handleDispatched(msg)
	case attachSpecMsg:
		return m, m.execAttachSpec(msg.spec, msg.err)
	case attachFinishedMsg:
		return m.handleAttachFinished(msg), m.loadSessionsCmd()
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleWindowSize(msg tea.WindowSizeMsg) Model {
	m.width, m.height = msg.Width, msg.Height
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
	if msg.err != nil {
		m.setMessage(msg.err.Error())
		return m
	}
	if msg.sessions != nil {
		m.sessions = msg.sessions
		m.groupByDir = msg.groupByDir
	}
	if msg.defaultAgent != "" {
		m.defaultAgent = msg.defaultAgent
	}
	if m.selected >= len(m.sessions) {
		m.selected = max(0, len(m.sessions)-1)
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

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if handled, model, cmd := m.handleModalKey(msg, key); handled {
		return model, cmd
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
	if m.confirmStop {
		if key == "y" || key == "enter" {
			m.confirmStop = false
			id := m.confirmStopID
			m.confirmStopID = ""
			return true, m, m.stopTargetCmd(id, true)
		}
		if key == "n" || key == "esc" {
			m.confirmStop = false
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
	m.peekText = ""
	return m.peekSelectedCmd()
}

func (m *Model) moveSelection(delta int) {
	next := m.selected + delta
	if next >= 0 && next < len(m.sessions) {
		m.selected = next
	}
}

func (m *Model) moveSession(delta int) tea.Cmd {
	next := m.selected + delta
	if next < 0 || next >= len(m.sessions) {
		return nil
	}
	m.sessions[m.selected], m.sessions[next] = m.sessions[next], m.sessions[m.selected]
	m.selected = next
	return m.persistOrderCmd()
}

func (m *Model) handleActionKey(key string) (bool, tea.Cmd) {
	switch key {
	case "ctrl+c":
		m.quitting = true
		return true, tea.Quit
	case "tab":
		m.cycleDefaultAgent()
		return true, m.persistDefaultAgent()
	case "?":
		m.helpOpen = true
	case "ctrl+s":
		m.groupByDir = !m.groupByDir
		return true, m.persistGroupByDir()
	case "ctrl+t":
		return true, m.pinSelectedCmd()
	case "ctrl+r":
		m.startRename()
	case "ctrl+x":
		if sess, ok := m.selectedSession(); ok {
			m.confirmStop = true
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
		m.peekOpen = false
		return nil
	}
	if m.input != "" {
		m.input = ""
		return nil
	}
	m.quitting = true
	return tea.Quit
}

func (m *Model) startRename() {
	sess, ok := m.selectedSession()
	if !ok {
		return
	}
	m.renaming = true
	m.renameTargetID = sess.ID
	m.input = sess.DisplayName
}

func (m *Model) handleSpaceKey(key string) tea.Cmd {
	if strings.TrimSpace(m.input) != "" || len(m.sessions) == 0 {
		m.input += key
		return nil
	}
	// A stopped session has no live tmux pane to peek into — Space restarts it
	// in the background instead.
	if sess, ok := m.selectedSession(); ok && sess.ProcAlive == adapter.Exited {
		m.setMessage("restarting " + firstNonEmpty(sess.DisplayName, sess.ID))
		return m.resumeSelectedCmd()
	}
	m.peekOpen = !m.peekOpen
	if m.peekOpen {
		return m.peekSelectedCmd()
	}
	return nil
}

func (m *Model) handleEnterKey() tea.Cmd {
	if strings.TrimSpace(m.input) != "" {
		spec := parseDispatchSpec(m.input, m.defaultAgent)
		return m.dispatchNamedCmd(spec.Agent, spec.Name, spec.Prompt)
	}
	if len(m.sessions) > 0 {
		return m.attachSelectedCmd()
	}
	return nil
}

func (m *Model) handleEditKey(key string) {
	if strings.TrimSpace(m.input) != "" {
		m.input += key
		return
	}
	m.wizard = true
	m.wizardStep = 0
	m.input = ""
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
		sess, ok := m.sessionByID(m.renameTargetID)
		name := m.input
		m.renaming = false
		m.renameTargetID = ""
		m.input = ""
		// The target session vanished (killed externally / list emptied) while the
		// modal was open: close the modal without panicking (F27).
		if !ok {
			return m, nil
		}
		id := sess.ID
		return m, func() tea.Msg { return sessionsLoadedMsg{err: m.service.Rename(context.Background(), id, name)} }
	case "esc":
		m.renaming = false
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
		return m.handleWizardCwdKey(key)
	case 2:
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
		m.input = m.wizardCwd
		return nil, true
	}
	return nil, false
}

func (m *Model) handleWizardCwdKey(key string) (tea.Cmd, bool) {
	if key != "enter" {
		return nil, false
	}
	m.wizardCwd = firstNonEmpty(m.input, ".")
	m.wizardStep = 2
	m.input = ""
	return nil, true
}

func (m *Model) handleWizardPromptKey(key string) (tea.Cmd, bool) {
	if key != "enter" {
		return nil, false
	}
	spec := parseDispatchSpec(m.input, firstNonEmpty(m.wizardAgent, m.defaultAgent))
	cwd := m.wizardCwd
	m.closeWizard()
	return m.dispatchWithNameCwdCmd(spec.Agent, spec.Name, spec.Prompt, cwd), true
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
	Name   string
	Prompt string
}

func parseDispatchInput(input, def string) (string, string) {
	spec := parseDispatchSpec(input, def)
	return spec.Prompt, spec.Agent
}

func parseDispatchSpec(input, def string) dispatchSpec {
	fields := strings.Fields(input)
	spec := dispatchSpec{Agent: def}
	if len(fields) > 0 && strings.HasPrefix(fields[0], "@") {
		spec.Agent = strings.TrimPrefix(fields[0], "@")
		fields = fields[1:]
	}
	if len(fields) > 0 && strings.HasPrefix(fields[0], "#") {
		spec.Name = strings.TrimPrefix(fields[0], "#")
		fields = fields[1:]
	}
	spec.Prompt = strings.Join(fields, " ")
	return spec
}

func (m Model) dispatchCmd(agent, prompt string) tea.Cmd {
	return m.dispatchWithCwdCmd(agent, prompt, "")
}
func (m Model) dispatchNamedCmd(agent, name, prompt string) tea.Cmd {
	return m.dispatchWithNameCwdCmd(agent, name, prompt, "")
}
func (m Model) dispatchWithCwdCmd(agent, prompt, cwd string) tea.Cmd {
	return m.dispatchWithNameCwdCmd(agent, "", prompt, cwd)
}
func (m Model) dispatchWithNameCwdCmd(agent, name, prompt, cwd string) tea.Cmd {
	return func() tea.Msg {
		sess, err := m.service.DispatchNamed(context.Background(), agent, name, prompt, cwd, string(store.ModeYolo))
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
func (m Model) peekSelectedCmd() tea.Cmd {
	sess, ok := m.selectedSession()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		p, err := m.service.Peek(context.Background(), sess.ID)
		return peekLoadedMsg{text: p.TailText, err: err}
	}
}

// resumeSelectedCmd restarts the selected session's tmux session in the
// background, then reloads so it moves into the ACTIVE group.
func (m Model) resumeSelectedCmd() tea.Cmd {
	sess, ok := m.selectedSession()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		if err := m.service.ResumeBackground(context.Background(), sess.ID); err != nil {
			return sessionsLoadedMsg{err: err}
		}
		return m.loadSessionsCmd()()
	}
}
func (m Model) stopSelectedCmd(remove bool) tea.Cmd {
	return m.stopTargetCmd("", remove)
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

// persistGroupByDir persists the group-by-dir toggle. On failure it surfaces the
// error and reverts the in-memory flag so the UI matches the unchanged stored
// state; on success it returns a reload command (F55).
func (m *Model) persistGroupByDir() tea.Cmd {
	grouped := m.groupByDir
	if err := m.service.SetUI(func(ui *store.UISettings) { ui.GroupByDir = grouped }); err != nil {
		m.groupByDir = !grouped
		m.setMessage("could not save view setting: " + err.Error())
		return nil
	}
	return m.loadSessionsCmd()
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
func (m Model) pinSelectedCmd() tea.Cmd {
	sess, ok := m.selectedSession()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		err := m.service.TogglePin(context.Background(), sess.ID)
		return sessionsLoadedMsg{err: err}
	}
}
func (m Model) attachSelectedCmd() tea.Cmd {
	sess, ok := m.selectedSession()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		spec, err := m.service.AttachSpec(context.Background(), sess.ID)
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
	return runner(cmd, func(err error) tea.Msg { return attachFinishedMsg{err: err} })
}

func (m Model) persistOrderCmd() tea.Cmd {
	sessions := append([]adapter.Session(nil), m.sessions...)
	return func() tea.Msg { return sessionsLoadedMsg{err: m.service.UpdateSortOrder(sessions)} }
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
)

var (
	brandStyle    = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(textColor)
	sectionStyle  = lipgloss.NewStyle().Bold(true).Foreground(mutedColor)
	hintStyle     = lipgloss.NewStyle().Foreground(mutedColor)
	dividerStyle  = lipgloss.NewStyle().Foreground(dividerColor)
	taskStyle     = lipgloss.NewStyle().Foreground(taskColor)
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
)

// bar is the accent rule that marks the brand and command lines.
func bar() string { return brandStyle.Render("▌") }

// layoutMode buckets the usable width: 0 narrow (mobile), 1 mid, 2 wide.
func (m Model) layoutMode() int {
	switch w := m.contentWidth(); {
	case w >= 76:
		return 2
	case w >= 48:
		return 1
	default:
		return 0
	}
}

func (m Model) View() string {
	if m.quitting {
		return ""
	}
	var b strings.Builder
	b.WriteString(m.renderBranding())
	switch {
	case m.helpOpen:
		b.WriteString(m.renderHelp())
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
		b.WriteString("    " + taskStyle.Render(truncate(promptText(sess), max(8, m.contentWidth()-6))) + "\n")
	}
	b.WriteString("    " + hintStyle.Render("agent: "+firstNonEmpty(sess.AgentType, "?")) + "\n")
	if !sess.CreatedAt.IsZero() {
		b.WriteString("    " + hintStyle.Render("created: "+sess.CreatedAt.Format("Jan 02 15:04")) + "\n")
	}
	b.WriteString("    " + hintStyle.Render("cwd: "+absCwd(sess.Cwd)) + "\n")
	return b.String()
}

func (m Model) renderTable() string {
	var b strings.Builder
	b.WriteString("\n")
	if len(m.sessions) == 0 {
		b.WriteString(m.renderSection("SESSIONS", "0") + "\n")
		b.WriteString("  " + hintStyle.Render("no sessions — type a prompt, @agent #name prompt, or press e") + "\n")
		return b.String()
	}
	nameWidth, taskWidth, showTask := m.tableWidths()
	start, end := m.visibleSessionWindow()
	active, closed := 0, 0
	for _, s := range m.sessions {
		if s.Closed {
			closed++
		} else {
			active++
		}
	}
	if start > 0 {
		b.WriteString("  " + hintStyle.Render(fmt.Sprintf("↑ %d more", start)) + "\n")
	}
	// Two groups: Active (anything not flagged closed_by_user — including
	// reboot-survivors that will resume on attach) and Closed (the user
	// explicitly retired these via uam stop, exit-in-session, or external
	// tmux kill-session).
	g1 := m.renderGroup("ACTIVE", active, start, end, false, nameWidth, taskWidth, showTask)
	g2 := m.renderGroup("CLOSED", closed, start, end, true, nameWidth, taskWidth, showTask)
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

// renderGroup renders the windowed sessions whose Closed flag matches
// wantClosed under a section header. Empty groups render nothing.
func (m Model) renderGroup(label string, total, start, end int, wantClosed bool, nameWidth, taskWidth int, showTask bool) string {
	var rows []string
	for i := start; i < end; i++ {
		s := m.sessions[i]
		if s.Closed != wantClosed {
			continue
		}
		rows = append(rows, renderRow(s, i == m.selected, nameWidth, taskWidth, showTask))
	}
	if len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(m.renderSection(label, fmt.Sprintf("%d", total)) + "\n")
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
	label := truncate(pin+firstNonEmpty(s.DisplayName, s.ID), nameWidth)
	if showTask {
		// Width-aware padding keeps the task column aligned even when the name
		// holds wide (CJK/emoji) runes (F28).
		cell := nameStyle.Render(padRight(label, nameWidth))
		return cursor + gs.Render(glyph) + " " + cell + " " + prCell + " " + taskStyle.Render(truncate(promptText(s), taskWidth))
	}
	// Narrow layout: state glyph + name only — one line per row. The selected
	// session's task is carried by the details panel, so rows don't repeat it.
	row := cursor + gs.Render(glyph) + " " + nameStyle.Render(label)
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
		b.WriteString(bar() + " " + hintStyle.Render("rename") + "  " + titleStyle.Render(m.input) + brandStyle.Render("▏") + "\n")
	} else {
		field := hintStyle.Render("type a command…")
		if m.input != "" {
			field = titleStyle.Render(m.input)
		}
		hints := hintStyle.Render(m.defaultAgent + "  ·  ? help  ·  e new  ·  Esc quit")
		b.WriteString(bar() + " " + brandStyle.Render("›") + " " + field + brandStyle.Render("▏") + "   " + hints + "\n")
	}
	if m.message != "" {
		b.WriteString("  " + hintStyle.Render(m.message) + "\n")
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

// livenessLabel describes a prompt-less session by its liveness and Closed flag
// rather than its State enum.
func livenessLabel(sess adapter.Session) string {
	switch {
	case sess.ProcAlive == adapter.Alive:
		return "running"
	case sess.Closed:
		return "closed"
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
		return abs
	}
	return cwd
}

func (m Model) renderHelp() string {
	rows := []string{
		"↑/↓  move        Enter/→  attach        Space  peek",
		"Tab  cycle agent     Ctrl+T  pin        Ctrl+R  rename",
		"Ctrl+X  stop/remove      Ctrl+S  group-by-dir",
		"e  new session       Esc  quit",
		"dispatch:  @agent #name prompt   (name & prompt optional)",
	}
	var b strings.Builder
	b.WriteString("\n " + sectionStyle.Render("Keys:") + "\n")
	for _, r := range rows {
		b.WriteString("  " + hintStyle.Render(r) + "\n")
	}
	return b.String()
}

func (m Model) renderConfirm() string {
	sess, _ := m.sessionByID(m.confirmStopID)
	name := firstNonEmpty(sess.DisplayName, sess.ID, "session")
	return "\n " + sectionStyle.Render("Stop session") + "\n  " +
		hintStyle.Render("Stop and remove ") + titleStyle.Render(name) + hintStyle.Render("?") +
		"   " + brandStyle.Render("y") + hintStyle.Render(" / ") + titleStyle.Render("N") + "\n"
}

func (m Model) renderWizard() string {
	steps := []string{
		"provider — Tab cycles, Enter confirms:  " + firstNonEmpty(m.wizardAgent, m.defaultAgent),
		"working directory:  " + m.input,
		"#name prompt — both optional:  " + m.input,
	}
	step := m.wizardStep
	if step < 0 || step >= len(steps) {
		step = 0
	}
	var b strings.Builder
	b.WriteString("\n " + sectionStyle.Render("NEW SESSION") + "  " + hintStyle.Render(fmt.Sprintf("step %d of 3", step+1)) + "\n")
	b.WriteString("  " + titleStyle.Render(steps[step]) + brandStyle.Render("▏") + "\n") // #nosec G602 -- step is clamped to [0, len(steps)) just above.
	b.WriteString("  " + hintStyle.Render("Esc cancels") + "\n")
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

// sessionGlyph picks the row glyph from the session's liveness and Closed flag
// rather than its State enum, so a reboot-survivor (Exited but not user-closed)
// renders as a neutral "resumable" dot instead of the red Failed glyph under the
// ACTIVE group (F30).
func sessionGlyph(s adapter.Session) (string, lipgloss.Style) {
	switch {
	case s.ProcAlive == adapter.Alive:
		return "⟳", liveGlyphStyle
	case s.Closed:
		// User-retired, dead pane: muted resting dot in the CLOSED group.
		return "•", hintStyle
	default:
		// Reboot-survivor / externally-killed but resumable: neutral paused
		// glyph, NOT the red failure mark.
		return "◦", hintStyle
	}
}

func stateGlyph(s adapter.State) (string, lipgloss.Style) {
	switch s {
	case adapter.Active:
		return "⟳", liveGlyphStyle
	case adapter.Failed:
		return "✕", failGlyphStyle
	default:
		return "•", hintStyle
	}
}

func stateLabel(s adapter.State) string {
	return strings.ToLower(string(s))
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
	s = strings.ReplaceAll(s, "\n", " ")
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
