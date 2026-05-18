package app

import (
	"context"
	"fmt"
	"os/exec"
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
	width, height int
	quitting      bool
	service       *Service
	sessions      []adapter.Session
	selected      int
	input         string
	defaultAgent  string
	message       string
	peekOpen      bool
	peekText      string
	helpOpen      bool
	confirmStop   bool
	renaming      bool
	wizard        bool
	wizardStep    int
	wizardAgent   string
	wizardCwd     string
	groupByDir    bool
	execProcess   func(*exec.Cmd, tea.ExecCallback) tea.Cmd
}

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

func (m Model) handleSessionsLoaded(msg sessionsLoadedMsg) Model {
	if msg.err != nil {
		m.message = msg.err.Error()
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
		m.message = msg.err.Error()
	} else {
		m.peekText = msg.text
	}
	return m
}

func (m Model) handleDispatched(msg dispatchedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.message = msg.err.Error()
		return m, nil
	}
	m.message = "attaching " + msg.session.ID
	m.input = ""
	return m, m.attachSessionCmd(msg.session)
}

func (m Model) handleAttachFinished(msg attachFinishedMsg) Model {
	if msg.err != nil {
		m.message = "session exited: " + msg.err.Error()
	} else {
		m.message = "returned to uam"
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
	m.appendKeyInput(msg, key)
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
			return true, m, m.stopSelectedCmd(true)
		}
		if key == "n" || key == "esc" {
			m.confirmStop = false
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
	case "ctrl+c", "q":
		return true, m.handleQuitOrInput(key)
	case "tab":
		m.cycleDefaultAgent()
		_ = m.service.SetDefaultAgent(m.defaultAgent)
	case "?":
		m.helpOpen = true
	case "ctrl+s":
		m.groupByDir = !m.groupByDir
		_ = m.service.SetUI(func(ui *store.UISettings) { ui.GroupByDir = m.groupByDir })
	case "ctrl+t":
		return true, m.pinSelectedCmd()
	case "ctrl+r":
		m.startRename()
	case "ctrl+x":
		m.confirmStop = len(m.sessions) > 0
	case " ":
		return true, m.handleSpaceKey(key)
	case "right", "enter":
		return true, m.handleEnterKey()
	case "esc":
		m.input = ""
		m.peekOpen = false
	case "backspace":
		m.backspaceInput()
	case "e":
		m.handleEditKey(key)
	default:
		return false, nil
	}
	return true, nil
}

func (m *Model) handleQuitOrInput(key string) tea.Cmd {
	if strings.TrimSpace(m.input) == "" {
		m.quitting = true
		return tea.Quit
	}
	if key == "q" {
		m.input += key
	}
	return nil
}

func (m *Model) startRename() {
	if len(m.sessions) == 0 {
		return
	}
	m.renaming = true
	m.input = m.sessions[m.selected].DisplayName
}

func (m *Model) handleSpaceKey(key string) tea.Cmd {
	if strings.TrimSpace(m.input) != "" || len(m.sessions) == 0 {
		m.input += key
		return nil
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
	if len(m.input) > 0 {
		m.input = m.input[:len(m.input)-1]
	}
}

func (m *Model) appendKeyInput(msg tea.KeyMsg, key string) {
	if msg.Type == tea.KeyRunes || len([]rune(key)) == 1 {
		m.input += key
	}
}

func (m Model) handleRenameKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "enter":
		id := m.sessions[m.selected].ID
		name := m.input
		m.renaming = false
		m.input = ""
		return m, func() tea.Msg { return sessionsLoadedMsg{err: m.service.Rename(context.Background(), id, name)} }
	case "esc":
		m.renaming = false
		m.input = ""
	case "backspace":
		if len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}
	default:
		if len(key) == 1 || key == " " {
			m.input += key
		}
	}
	return m, nil
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
	m.editWizardInput(key)
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
		_ = m.service.SetDefaultAgent(m.defaultAgent)
		m.wizardAgent = m.defaultAgent
		return nil, true
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

func (m *Model) editWizardInput(key string) {
	if key == "backspace" {
		m.backspaceInput()
		return
	}
	if len(key) == 1 || key == " " {
		m.input += key
	}
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
func (m Model) stopSelectedCmd(remove bool) tea.Cmd {
	sess, ok := m.selectedSession()
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

var (
	accentColor  = lipgloss.AdaptiveColor{Light: "#1F2937", Dark: "#E5E7EB"}
	mutedColor   = lipgloss.AdaptiveColor{Light: "#64748B", Dark: "#94A3B8"}
	dividerColor = lipgloss.AdaptiveColor{Light: "#CBD5E1", Dark: "#334155"}
	taskColor    = lipgloss.AdaptiveColor{Light: "#334155", Dark: "#CBD5E1"}
	liveColor    = lipgloss.AdaptiveColor{Light: "#047857", Dark: "#34D399"}
	deadColor    = lipgloss.AdaptiveColor{Light: "#DC2626", Dark: "#F87171"}
)

var titleStyle = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
var brandStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#0F766E", Dark: "#2DD4BF"})
var hintStyle = lipgloss.NewStyle().Foreground(mutedColor).Faint(true)
var selectedStyle = lipgloss.NewStyle()
var detailStyle = lipgloss.NewStyle()
var dividerStyle = lipgloss.NewStyle().Foreground(dividerColor).Faint(true)
var sectionStyle = lipgloss.NewStyle().Foreground(accentColor).Bold(true)
var liveStyle = lipgloss.NewStyle().Foreground(liveColor).Bold(true)
var deadStyle = lipgloss.NewStyle().Foreground(deadColor).Bold(true)
var taskStyle = lipgloss.NewStyle().Foreground(taskColor)

func (m Model) View() string {
	if m.quitting {
		return ""
	}
	body := m.renderBranding()
	if m.helpOpen {
		body += m.renderHelp()
	} else if m.confirmStop {
		body += "Stop and remove selected session? y/N\n"
	} else if m.wizard {
		body += m.renderWizard()
	} else {
		body += m.renderDetails()
		body += m.renderTable()
		if m.peekOpen {
			body += "\n" + hintStyle.Render("peek") + "\n" + trimLines(m.peekText, max(5, m.height/3))
		}
	}
	prompt := fmt.Sprintf("\n%s %s_   %s", hintStyle.Render("command"), m.input, hintStyle.Render("agent:"+m.defaultAgent+" · ? help · e new"))
	if m.renaming {
		prompt = "\n" + hintStyle.Render("rename") + " " + m.input + "_"
	}
	if m.message != "" {
		prompt += "\n" + hintStyle.Render(m.message)
	}
	body += prompt
	return body
}

const uamANSILogo = ` _   _  _   __  __ 
| | | |/_\ |  \/  |
| |_| / _ \| |\/| |
 \___/_/ \_\_|  |_|`

func (m Model) renderBranding() string {
	subtitle := fmt.Sprintf("Unified Agent Manager · %s", version.String())
	if m.contentWidth() < 34 {
		return brandStyle.Render("UAM") + "\n" + hintStyle.Render(subtitle) + "\n" + m.renderDivider() + "\n"
	}
	return brandStyle.Render(uamANSILogo) + "\n" + hintStyle.Render(subtitle) + "\n" + m.renderDivider() + "\n"
}

func (m Model) renderTable() string {
	if len(m.sessions) == 0 {
		return "\nNo sessions. Type @claude #bugfix fix bug, type @claude for an empty session, press e for wizard, or run uam dispatch.\n"
	}
	nameWidth, taskWidth, showTask := m.tableWidths()
	start, end := m.visibleSessionWindow()
	var b strings.Builder
	b.WriteString("\n" + sectionStyle.Render("SESSIONS") + "\n")
	if showTask {
		b.WriteString(hintStyle.Render(fmt.Sprintf("  %-*s %s", nameWidth+2, "NAME", "TASK")) + "\n")
	} else {
		b.WriteString(hintStyle.Render(fmt.Sprintf("  %-*s", nameWidth+2, "NAME")) + "\n")
	}
	if start > 0 {
		b.WriteString(hintStyle.Render(fmt.Sprintf("  ↑ %d more", start)) + "\n")
	}
	for i, s := range m.sessions[start:end] {
		idx := start + i
		cursor := " "
		if idx == m.selected {
			cursor = "›"
		}
		pin := ""
		if s.Pinned {
			pin = "★ "
		}
		line := fmt.Sprintf("%s %s %-*s", cursor, m.tmuxMark(s), nameWidth, truncate(pin+firstNonEmpty(s.DisplayName, s.ID), nameWidth))
		if showTask {
			line += " " + taskStyle.Render(truncate(promptText(s), taskWidth))
		}
		if idx == m.selected {
			line = selectedStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	if end < len(m.sessions) {
		b.WriteString(hintStyle.Render(fmt.Sprintf("  ↓ %d more", len(m.sessions)-end)) + "\n")
	}
	return b.String()
}

func (m Model) renderDetails() string {
	sess, ok := m.selectedSession()
	if !ok {
		return ""
	}
	nameWidth := max(12, m.contentWidth()-8)
	lines := []string{
		sectionStyle.Render("SELECTED"),
		fmt.Sprintf("%s  %s", m.tmuxMark(sess), titleStyle.Render(truncate(firstNonEmpty(sess.DisplayName, sess.ID), nameWidth))),
		fmt.Sprintf("agent: %s · id: %s", firstNonEmpty(sess.AgentType, "?"), firstNonEmpty(sess.ID, "?")),
	}
	if !sess.CreatedAt.IsZero() {
		lines = append(lines, "created: "+sess.CreatedAt.Format("Jan 02 15:04"))
	}
	lines = append(lines, fmt.Sprintf("cwd: %s", firstNonEmpty(sess.Cwd, "?")))
	return detailStyle.Render(strings.Join(lines, "\n")) + "\n" + m.renderDivider() + "\n"
}

func (m Model) renderDivider() string {
	return dividerStyle.Render(strings.Repeat("─", max(20, m.contentWidth())))
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

func (m Model) tmuxMark(sess adapter.Session) string {
	if sess.ProcAlive == adapter.Alive {
		return liveStyle.Render("●")
	}
	return deadStyle.Render("○")
}

func promptText(sess adapter.Session) string {
	return firstNonEmpty(sess.Prompt, sess.Activity, "<no prompt>")
}

func (m Model) renderHelp() string {
	return "\nKeys: ↑/↓ select · Enter/→ attach · Space peek · @agent #name prompt dispatch (name/prompt optional) · Tab default agent · Ctrl+T pin · Ctrl+R rename · Ctrl+X stop/remove · Ctrl+S group · e wizard · q quit\n"
}
func (m Model) renderWizard() string {
	steps := []string{"Pick provider (Tab cycles, Enter confirms): " + firstNonEmpty(m.wizardAgent, m.defaultAgent), "Pick workdir: " + m.input, "Enter #name prompt (both optional): " + m.input}
	return "\nNEW SESSION\n" + steps[m.wizardStep] + "\nEsc cancels\n"
}
func stateLabel(s adapter.State) string {
	switch s {
	case adapter.NeedsInput:
		return "needs input"
	case adapter.ReadyForReview:
		return "review"
	default:
		return strings.ToLower(string(s))
	}
}

func prStatusDot(s adapter.PRStatus) string {
	switch s {
	case adapter.PRMerged:
		return "●"
	case adapter.PRDraft:
		return "◐"
	case adapter.PRClosed:
		return "◯"
	case adapter.PROpen:
		return "●"
	default:
		return " "
	}
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return s[:max(0, n-1)] + "…"
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
