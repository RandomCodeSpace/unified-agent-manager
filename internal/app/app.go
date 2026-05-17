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

	"github.com/randomcodespace/unified-agent-manager/internal/adapter"
	"github.com/randomcodespace/unified-agent-manager/internal/adapter/claude"
	"github.com/randomcodespace/unified-agent-manager/internal/adapter/codex"
	"github.com/randomcodespace/unified-agent-manager/internal/adapter/copilot"
	"github.com/randomcodespace/unified-agent-manager/internal/adapter/opencode"
	"github.com/randomcodespace/unified-agent-manager/internal/store"
	"github.com/randomcodespace/unified-agent-manager/internal/tmux"
)

const version = "0.1.0"

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
type tickMsg time.Time

func New() Model {
	st, _ := store.Open(store.DefaultPath())
	client := tmux.New("uam")
	reg := adapter.NewRegistry([]adapter.AgentAdapter{claude.New(client), codex.New(client), copilot.New(client), opencode.New(client)})
	return NewWithDeps(st, reg)
}

func NewWithDeps(st *store.Store, reg *adapter.Registry) Model {
	return Model{service: NewService(st, reg), defaultAgent: "claude", wizardCwd: "."}
}
func NewWizard(st *store.Store, reg *adapter.Registry) Model {
	m := NewWithDeps(st, reg)
	m.wizard = true
	return m
}

func (m Model) Init() tea.Cmd { return tea.Batch(m.loadSessionsCmd(), tick()) }

func tick() tea.Cmd { return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }) }

func (m Model) loadSessionsCmd() tea.Cmd {
	return func() tea.Msg {
		sessions, cfg, err := m.service.LoadSessions(context.Background())
		return sessionsLoadedMsg{sessions: sessions, defaultAgent: cfg.DefaultAgent, groupByDir: cfg.UI.GroupByDir, err: err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tickMsg:
		return m, tea.Batch(m.loadSessionsCmd(), tick())
	case sessionsLoadedMsg:
		if msg.err != nil {
			m.message = msg.err.Error()
			return m, nil
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
		return m, nil
	case peekLoadedMsg:
		if msg.err != nil {
			m.message = msg.err.Error()
		} else {
			m.peekText = msg.text
		}
		return m, nil
	case dispatchedMsg:
		if msg.err != nil {
			m.message = msg.err.Error()
		} else {
			m.message = "dispatched " + msg.session.ID
			m.input = ""
		}
		return m, m.loadSessionsCmd()
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.helpOpen {
		if key == "?" || key == "esc" {
			m.helpOpen = false
		}
		return m, nil
	}
	if m.confirmStop {
		if key == "y" || key == "enter" {
			m.confirmStop = false
			return m, m.stopSelectedCmd(true)
		}
		if key == "n" || key == "esc" {
			m.confirmStop = false
		}
		return m, nil
	}
	if m.wizard {
		return m.handleWizardKey(msg)
	}
	if m.renaming {
		return m.handleRenameKey(msg)
	}
	switch key {
	case "ctrl+c", "q":
		if strings.TrimSpace(m.input) == "" {
			m.quitting = true
			return m, tea.Quit
		}
	case "up":
		if m.selected > 0 {
			m.selected--
		}
	case "down":
		if m.selected < len(m.sessions)-1 {
			m.selected++
		}
	case "shift+up":
		if m.selected > 0 {
			m.sessions[m.selected], m.sessions[m.selected-1] = m.sessions[m.selected-1], m.sessions[m.selected]
			m.selected--
			return m, m.persistOrderCmd()
		}
	case "shift+down":
		if m.selected < len(m.sessions)-1 {
			m.sessions[m.selected], m.sessions[m.selected+1] = m.sessions[m.selected+1], m.sessions[m.selected]
			m.selected++
			return m, m.persistOrderCmd()
		}
	case "tab":
		m.cycleDefaultAgent()
	case "?":
		m.helpOpen = true
	case "ctrl+s":
		m.groupByDir = !m.groupByDir
		_ = m.service.SetUI(func(ui *store.UISettings) { ui.GroupByDir = m.groupByDir })
	case "ctrl+t":
		return m, m.pinSelectedCmd()
	case "ctrl+r":
		if len(m.sessions) > 0 {
			m.renaming = true
			m.input = m.sessions[m.selected].DisplayName
		}
	case "ctrl+x":
		if len(m.sessions) > 0 {
			m.confirmStop = true
		}
	case " ":
		if len(m.sessions) > 0 {
			m.peekOpen = !m.peekOpen
			if m.peekOpen {
				return m, m.peekSelectedCmd()
			}
		}
	case "right":
		fallthrough
	case "enter":
		if strings.TrimSpace(m.input) != "" {
			prompt, agent := parseDispatchInput(m.input, m.defaultAgent)
			return m, m.dispatchCmd(agent, prompt)
		}
		if len(m.sessions) > 0 {
			return m, m.attachSelectedCmd()
		}
	case "esc":
		m.input = ""
		m.peekOpen = false
	case "backspace":
		if len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}
	case "e":
		m.wizard = true
		m.wizardStep = 0
		m.input = ""
		m.wizardCwd = "."
	default:
		if len(key) == 1 || key == " " {
			m.input += key
		}
	}
	return m, nil
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
		m.wizard = false
		m.input = ""
		return m, nil
	}
	switch m.wizardStep {
	case 0:
		if key == "tab" {
			m.cycleDefaultAgent()
			m.wizardAgent = m.defaultAgent
			return m, nil
		}
		if key == "enter" {
			if m.wizardAgent == "" {
				m.wizardAgent = m.defaultAgent
			}
			m.wizardStep = 1
			m.input = m.wizardCwd
			return m, nil
		}
	case 1:
		if key == "enter" {
			m.wizardCwd = firstNonEmpty(m.input, ".")
			m.wizardStep = 2
			m.input = ""
			return m, nil
		}
	case 2:
		if key == "enter" {
			prompt := m.input
			agent := firstNonEmpty(m.wizardAgent, m.defaultAgent)
			cwd := m.wizardCwd
			m.wizard = false
			m.input = ""
			return m, m.dispatchWithCwdCmd(agent, prompt, cwd)
		}
	}
	if key == "backspace" {
		if len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}
	} else if len(key) == 1 || key == " " {
		m.input += key
	}
	return m, nil
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
func parseDispatchInput(input, def string) (string, string) {
	fields := strings.Fields(input)
	if len(fields) > 0 && strings.HasPrefix(fields[0], "@") {
		return strings.TrimSpace(strings.TrimPrefix(input, fields[0])), strings.TrimPrefix(fields[0], "@")
	}
	return input, def
}

func (m Model) dispatchCmd(agent, prompt string) tea.Cmd {
	return m.dispatchWithCwdCmd(agent, prompt, "")
}
func (m Model) dispatchWithCwdCmd(agent, prompt, cwd string) tea.Cmd {
	return func() tea.Msg {
		sess, err := m.service.Dispatch(context.Background(), agent, prompt, cwd, string(store.ModeYolo))
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
		if err != nil {
			return sessionsLoadedMsg{err: err}
		}
		cmd := exec.Command(spec.Argv[0], spec.Argv[1:]...)
		return tea.ExecProcess(cmd, func(err error) tea.Msg { return sessionsLoadedMsg{err: err} })()
	}
}

func (m Model) persistOrderCmd() tea.Cmd {
	sessions := append([]adapter.Session(nil), m.sessions...)
	return func() tea.Msg { return sessionsLoadedMsg{err: m.service.UpdateSortOrder(sessions)} }
}

var titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
var hintStyle = lipgloss.NewStyle().Faint(true)
var selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
var borderStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)

func (m Model) View() string {
	if m.quitting {
		return ""
	}
	body := titleStyle.Render(fmt.Sprintf("unified-agent-manager %s", version)) + "\n"
	if m.helpOpen {
		body += m.renderHelp()
	} else if m.confirmStop {
		body += "Stop and remove selected session? y/N\n"
	} else if m.wizard {
		body += m.renderWizard()
	} else {
		body += m.renderTable()
		if m.peekOpen {
			body += "\n" + hintStyle.Render("peek") + "\n" + trimLines(m.peekText, max(5, m.height/3))
		}
	}
	prompt := fmt.Sprintf("\n> %s_   [agent:%s] [?] help [e] new", m.input, m.defaultAgent)
	if m.renaming {
		prompt = "\nrename> " + m.input + "_"
	}
	if m.message != "" {
		prompt += "\n" + hintStyle.Render(m.message)
	}
	body += prompt
	if m.width > 0 {
		return borderStyle.Width(max(20, m.width-2)).Render(body)
	}
	return body
}

func (m Model) renderTable() string {
	if len(m.sessions) == 0 {
		return "\nNo sessions. Type @claude fix bug, press e for wizard, or run uam dispatch.\n"
	}
	var b strings.Builder
	current := adapter.State("")
	for i, s := range m.sessions {
		if !m.groupByDir && s.State != current {
			current = s.State
			b.WriteString("\n" + strings.ToUpper(stateLabel(s.State)) + "\n")
		}
		if m.groupByDir {
			grp := firstNonEmpty(s.Group, filepath.Base(s.Cwd))
			if i == 0 || grp != firstNonEmpty(m.sessions[i-1].Group, filepath.Base(m.sessions[i-1].Cwd)) {
				b.WriteString("\n" + strings.ToUpper(grp) + "\n")
			}
		}
		cursor := " "
		if i == m.selected {
			cursor = "›"
		}
		pin := " "
		if s.Pinned {
			pin = "★"
		}
		prDot := " "
		if s.PR != nil {
			prDot = prStatusDot(s.PR.Status)
		}
		line := fmt.Sprintf("%s %s %s %-8s %-14s %-20s %s", cursor, pin, prDot, s.AgentType, stateLabel(s.State), truncate(s.DisplayName, 20), truncate(s.Activity, 40))
		if i == m.selected {
			line = selectedStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func (m Model) renderHelp() string {
	return "\nKeys: ↑/↓ select · Enter/→ attach · Space peek · type prompt Enter dispatch · @agent prompt select agent · Tab default agent · Ctrl+T pin · Ctrl+R rename · Ctrl+X stop/remove · Ctrl+S group · e wizard · q quit\n"
}
func (m Model) renderWizard() string {
	steps := []string{"Pick provider (Tab cycles, Enter confirms): " + firstNonEmpty(m.wizardAgent, m.defaultAgent), "Pick workdir: " + m.input, "Enter prompt: " + m.input}
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
