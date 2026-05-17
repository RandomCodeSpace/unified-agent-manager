package app

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const version = "0.0.1-dev"

type Model struct {
	width, height int
	quitting      bool
}

func New() Model { return Model{} }

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			m.quitting = true
			return m, tea.Quit
		}
	}
	return m, nil
}

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	hintStyle   = lipgloss.NewStyle().Faint(true)
	borderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Padding(0, 1)
)

func (m Model) View() string {
	if m.quitting {
		return ""
	}
	body := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("unified-agent-manager"),
		"",
		fmt.Sprintf("version %s", version),
		"",
		"Phase 0 skeleton — no sessions yet.",
		"",
		hintStyle.Render("press q to quit"),
	)
	if m.width == 0 {
		return body
	}
	return borderStyle.Width(m.width - 2).Render(body)
}
