// Package explore provides a borderless, jailed file-explorer with live
// syntax-highlighted preview, intended to run as a Bubble Tea TUI inside a
// tmux split via the `uam explore [dir]` subcommand.
package explore

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// leftPaneWidth is the fixed column width of the file-list pane.
const leftPaneWidth = 32

// Package-level lipgloss styles (borderless, minimal).
var (
	styleAccent = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	styleDim    = lipgloss.NewStyle().Faint(true)
	styleCursor = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14"))
	styleDir    = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
)

// dirEntry represents a single filesystem entry in the current directory.
type dirEntry struct {
	name  string
	isDir bool
}

// Model is the Bubble Tea model for the file explorer.
type Model struct {
	root        string // absolute jail root — navigation cannot escape this
	cwd         string // absolute current directory (always root or descendant)
	entries     []dirEntry
	cursor      int
	viewport    viewport.Model
	width       int
	height      int
	previewName string
	previewErr  string
	color       bool
}

// New constructs a Model jailed to dir (resolved to an absolute path).
func New(dir string) Model {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	const defaultW, defaultH = 80, 24
	m := Model{
		root:   abs,
		cwd:    abs,
		color:  colorEnabled(),
		width:  defaultW,
		height: defaultH,
	}
	_, rightW := paneWidthsFor(defaultW)
	m.viewport = viewport.New(rightW, bodyHeightFor(defaultH)-2)
	m.loadEntries()
	m.preview()
	return m
}

// Run launches the Bubble Tea program in alt-screen mode and blocks until exit.
func Run(dir string) error {
	_, err := tea.NewProgram(New(dir), tea.WithAltScreen()).Run()
	return err
}

// Init satisfies tea.Model. No initial command is needed.
func (m Model) Init() tea.Cmd {
	return nil
}

// Update handles incoming messages and returns the updated model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		_, rightW := m.paneWidths()
		bodyH := m.bodyHeight()
		m.viewport.Width = rightW
		m.viewport.Height = bodyH - 2

	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
				m.preview()
			}

		case "down", "j":
			if m.cursor < len(m.entries)-1 {
				m.cursor++
				m.preview()
			}

		case "enter", "right", "l":
			if len(m.entries) == 0 {
				break
			}
			e := m.entries[m.cursor]
			if e.isDir {
				target := filepath.Join(m.cwd, e.name)
				if m.within(target) {
					m.cwd = target
					m.loadEntries()
					m.cursor = 0
					m.preview()
				}
			}
			// file: already previewed on cursor move — no-op on enter

		case "left", "h", "backspace":
			if m.cwd != m.root {
				m.cwd = filepath.Dir(m.cwd)
				m.loadEntries()
				m.cursor = 0
				m.preview()
			}
			// at root: jailed, no-op

		case "pgup", "pgdown", "ctrl+u", "ctrl+d":
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd

		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		}
	}
	return m, nil
}

// View renders the borderless 2-pane layout.
func (m Model) View() string {
	leftW, rightW := m.paneWidths()
	bodyH := m.bodyHeight()

	left := m.renderLeft(leftW, bodyH)
	right := m.renderRight(rightW, bodyH)

	body := lipgloss.JoinHorizontal(lipgloss.Top, left, right)

	footer := styleDim.Render("↑↓ move · →/enter open · ←/bksp up · pgup/pgdn scroll · q quit")
	// Pad footer to full width to avoid cursor bleed on resize.
	footer = lipgloss.NewStyle().Width(m.width).Render(footer)

	return lipgloss.JoinVertical(lipgloss.Left, body, footer)
}

// renderLeft builds the file-list pane.
func (m Model) renderLeft(w, bodyH int) string {
	// Header: relative path from root.
	rel, err := filepath.Rel(m.root, m.cwd)
	if err != nil || rel == "." {
		rel = "./"
	} else {
		rel = "./" + rel
	}
	header := styleAccent.Width(w).Render(rel)
	divider := styleDim.Width(w).Render(strings.Repeat("─", w))

	// Visible window of entries centred on cursor.
	listH := max(bodyH-2, 1) // header + divider
	start, end := m.visibleWindow(listH)

	var rows []string
	for i := start; i < end && i < len(m.entries); i++ {
		e := m.entries[i]
		label := e.name
		if e.isDir {
			label += "/"
		}
		// Truncate to fit pane width minus the 2-char cursor prefix.
		maxLabel := max(w-2, 1)
		if r := []rune(label); len(r) > maxLabel {
			label = string(r[:maxLabel-1]) + "…"
		}

		var row string
		if i == m.cursor {
			prefix := styleCursor.Render("▸ ")
			text := styleCursor.Render(label)
			row = prefix + text
		} else {
			prefix := "  "
			if e.isDir {
				row = prefix + styleDir.Render(label)
			} else {
				row = prefix + label
			}
		}
		rows = append(rows, lipgloss.NewStyle().Width(w).Render(row))
	}

	listContent := strings.Join(rows, "\n")
	return lipgloss.NewStyle().Width(w).Height(bodyH).Render(
		header + "\n" + divider + "\n" + listContent,
	)
}

// renderRight builds the preview pane.
func (m Model) renderRight(w, bodyH int) string {
	name := m.previewName
	if name == "" {
		name = "(no file)"
	}
	header := styleAccent.Width(w).Render(name)
	divider := styleDim.Width(w).Render(strings.Repeat("─", w))

	var content string
	if m.previewErr != "" {
		content = styleDim.Render(m.previewErr)
	} else {
		content = m.viewport.View()
	}

	return lipgloss.NewStyle().Width(w).Height(bodyH).Render(
		header + "\n" + divider + "\n" + content,
	)
}

// loadEntries reads m.cwd, populates m.entries (dirs first, then files, each
// alphabetically, including hidden entries), and clears previewErr on success.
func (m *Model) loadEntries() {
	des, err := os.ReadDir(m.cwd)
	if err != nil {
		m.entries = nil
		m.previewErr = fmt.Sprintf("cannot read dir: %v", err)
		return
	}
	m.previewErr = ""
	var dirs, files []dirEntry
	for _, de := range des {
		e := dirEntry{name: de.Name(), isDir: de.IsDir()}
		if de.IsDir() {
			dirs = append(dirs, e)
		} else {
			files = append(files, e)
		}
	}
	// Both slices come from os.ReadDir which returns alphabetical order already,
	// but sort explicitly to guarantee stability.
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].name < dirs[j].name })
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	m.entries = append(dirs, files...)
}

// within reports whether target is inside (or equal to) m.root after resolving
// symlinks, so a symlink that points outside the project can't escape the jail.
// On any resolution failure it denies (returns false) — fail closed.
func (m Model) within(target string) bool {
	root, err := filepath.EvalSymlinks(m.root)
	if err != nil {
		return false
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, resolved)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// preview updates the viewport with the content of the currently highlighted
// entry. Directories show a placeholder; files are highlighted and line-numbered.
func (m *Model) preview() {
	if len(m.entries) == 0 {
		m.previewName = ""
		m.previewErr = "(empty directory)"
		m.viewport.SetContent("")
		return
	}
	e := m.entries[m.cursor]
	if e.isDir {
		m.previewName = e.name + "/"
		m.previewErr = ""
		m.viewport.SetContent("(directory)")
		m.viewport.GotoTop()
		return
	}
	m.previewName = e.name
	fullPath := filepath.Join(m.cwd, e.name)
	content, err := readFilePreview(fullPath, m.color)
	if err != nil {
		m.previewErr = err.Error()
		m.viewport.SetContent("")
		return
	}
	m.previewErr = ""
	m.viewport.SetContent(withLineNumbers(content))
	m.viewport.GotoTop()
}

// paneWidthsFor returns (leftWidth, rightWidth) for the given total terminal width.
func paneWidthsFor(total int) (int, int) {
	if total <= leftPaneWidth+4 {
		half := total / 2
		return half, total - half
	}
	return leftPaneWidth, total - leftPaneWidth
}

// bodyHeightFor returns pane body height for the given terminal height.
func bodyHeightFor(total int) int {
	return max(total-1, 4)
}

// paneWidths returns (leftWidth, rightWidth) given the current terminal width.
func (m Model) paneWidths() (int, int) {
	return paneWidthsFor(m.width)
}

// bodyHeight returns the number of rows available for pane content (total
// minus the 1-line footer).
func (m Model) bodyHeight() int {
	return bodyHeightFor(m.height)
}

// visibleWindow returns the [start, end) slice indices that keep cursor visible
// within a window of listH rows.
func (m Model) visibleWindow(listH int) (int, int) {
	n := len(m.entries)
	if n == 0 {
		return 0, 0
	}
	start := max(m.cursor-listH/2, 0)
	end := start + listH
	if end > n {
		end = n
		start = max(end-listH, 0)
	}
	return start, end
}
