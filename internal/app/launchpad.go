package app

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/charmbracelet/x/ansi"
)

// ─── launch pad ──────────────────────────────────────────────────────────────
//
// The launch pad is the line directly under the header. Closed, it names the
// provider and directory a new session would start with. Open, it expands in
// place into four fields (provider, directory, name, prompt) with the deck
// still visible beneath, so launching never replaces the screen.

type launchField uint8

const (
	launchFieldProvider launchField = iota
	launchFieldDir
	launchFieldName
	launchFieldPrompt
	launchFieldCount
)

const launchLabelWidth = 11

func (m Model) launchPadHeight() int {
	if m.launchOpen {
		return int(launchFieldCount)
	}
	return 1
}

// openLaunchPad resets the fields to their defaults and focuses the name, so
// typing starts a name immediately.
func (m *Model) openLaunchPad() {
	m.launchOpen = true
	m.launchProfile = ""
	m.launchAgentExplicit = false
	m.launchAgent = m.launchProviderDefault("")
	m.launchDir = firstNonEmpty(m.launchCwd, ".")
	m.launchName = ""
	m.launchPrompt = ""
	m.launchField = launchFieldName
	m.input = ""
}

func (m *Model) closeLaunchPad() {
	m.launchOpen = false
	m.input = ""
}

// commitLaunchInput stores the shared text buffer into the focused field.
func (m *Model) commitLaunchInput() {
	switch m.launchField {
	case launchFieldDir:
		m.launchDir = m.input
	case launchFieldName:
		m.launchName = m.input
	case launchFieldPrompt:
		m.launchPrompt = m.input
	}
}

func (m *Model) focusLaunchField(field launchField) {
	m.commitLaunchInput()
	m.launchField = field
	switch field {
	case launchFieldDir:
		m.input = m.launchDir
	case launchFieldName:
		m.input = m.launchName
	case launchFieldPrompt:
		m.input = m.launchPrompt
	default:
		m.input = ""
	}
}

func (m *Model) cycleLaunchProfile() {
	choices := append([]string{""}, m.profileNames...)
	for i, name := range choices {
		if name == m.launchProfile {
			m.launchProfile = choices[(i+1)%len(choices)]
			return
		}
	}
	m.launchProfile = ""
}

func (m Model) launchProviderDefault(profileName string) string {
	if profileName == "" {
		profileName = m.defaultProfile
	}
	if provider := m.profileProviders[profileName]; provider != "" {
		return provider
	}
	return m.defaultAgent
}

func (m Model) launchProfileLabel() string {
	if m.launchProfile != "" {
		return m.launchProfile
	}
	if m.defaultProfile != "" {
		return "default:" + m.defaultProfile
	}
	return "default:none"
}

func (m Model) handleLaunchKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.closeLaunchPad()
		return m, nil
	case "enter":
		return m, m.launchCmd()
	case "up":
		if m.launchField > launchFieldProvider {
			m.focusLaunchField(m.launchField - 1)
		}
		return m, nil
	case "down":
		if m.launchField+1 < launchFieldCount {
			m.focusLaunchField(m.launchField + 1)
		}
		return m, nil
	case "tab":
		switch m.launchField {
		case launchFieldProvider:
			m.cycleDefaultAgent()
			m.launchAgent = m.defaultAgent
			m.launchAgentExplicit = true
			return m, m.persistDefaultAgent()
		case launchFieldDir:
			// Complete the typed path against the filesystem. Marked done so the
			// literal tab never leaks into the buffer.
			m.input = globComplete(m.input)
			return m, nil
		default:
			m.focusLaunchField((m.launchField + 1) % launchFieldCount)
			return m, nil
		}
	case "shift+tab":
		if m.launchField == launchFieldProvider {
			m.cycleLaunchProfile()
			if !m.launchAgentExplicit {
				m.launchAgent = m.launchProviderDefault(m.launchProfile)
			}
			return m, nil
		}
		m.focusLaunchField(m.launchField - 1)
		return m, nil
	case "ctrl+g":
		// Compose the prompt in $EDITOR for multi-line input. Launched via
		// tea.ExecProcess so the TUI screen state is restored cleanly.
		m.focusLaunchField(launchFieldPrompt)
		return m, m.editPromptCmd()
	}
	if m.launchField != launchFieldProvider {
		m.editText(msg)
	}
	return m, nil
}

// launchCmd dispatches the session described by the pad and closes it. The
// prompt still accepts the @agent:alias #name shorthand; the name field wins
// over a #name in the prompt when both are given.
func (m *Model) launchCmd() tea.Cmd {
	m.commitLaunchInput()
	spec := parseDispatchSpec(m.launchPrompt, firstNonEmpty(m.launchAgent, m.defaultAgent))
	name := firstNonEmpty(strings.TrimSpace(m.launchName), spec.Name)
	cwd := firstNonEmpty(strings.TrimSpace(m.launchDir), ".")
	profile := m.launchProfile
	m.closeLaunchPad()
	return m.dispatchWithNameCwdProfileCmd(spec.Agent, spec.Alias, name, spec.Prompt, cwd, profile)
}

// launchPadLines renders the pad at the given width: one line when closed,
// launchFieldCount lines when open.
func (m Model) launchPadLines(width int) []string {
	if !m.launchOpen {
		return []string{m.launchPadClosedLine(width)}
	}
	lines := make([]string, 0, launchFieldCount)
	for field := launchFieldProvider; field < launchFieldCount; field++ {
		lines = append(lines, m.launchPadFieldLine(field, width))
	}
	return lines
}

func (m Model) launchPadClosedLine(width int) string {
	provider := displaytext.Sanitize(m.launchProviderDefault(""))
	dir := m.displayPath(firstNonEmpty(m.launchCwd, "."))
	if dashboardLayoutFor(width) == dashboardNarrow {
		prefix := brandStyle.Render("n") + " " + titleStyle.Render("new") + "  " + titleStyle.Render(provider) + "  "
		budget := max(1, width-ansi.StringWidth(prefix))
		return ansi.Truncate(prefix+hintStyle.Render(elidePathLeft(dir, budget)), width, truncTail())
	}
	left := brandStyle.Render("n") + "  " + titleStyle.Render("new session") + "   " + titleStyle.Render(provider) + "   " + hintStyle.Render(dir)
	if m.defaultProfile != "" {
		left += hintStyle.Render(dotSep() + "profile " + displaytext.Sanitize(m.defaultProfile))
	}
	return joinDashboardEnds(left, hintStyle.Render("tab provider"), width)
}

func (m Model) launchPadFieldLine(field launchField, width int) string {
	focused := field == m.launchField
	label, value, hints, warning := m.launchFieldParts(field)
	labelRender := hintStyle.Render(padRightANSI(label, launchLabelWidth))
	if focused {
		labelRender = brandStyle.Render(padRightANSI(label, launchLabelWidth))
	}
	valueRender := titleStyle.Render(displaytext.Sanitize(value))
	if focused && field != launchFieldProvider {
		valueRender += brandStyle.Render(cursorGlyph())
	}
	left := bar() + " " + labelRender + valueRender
	// Narrow terminals keep the value; the help line carries the pad keys.
	if !focused || dashboardLayoutFor(width) == dashboardNarrow {
		return ansi.Truncate(left, width, truncTail())
	}
	segments := make([]string, 0, len(hints)+1)
	if warning != "" {
		segments = append(segments, toneOf(toneWarn).mark()+warnStyle.Render(" "+warning))
	}
	for _, hint := range hints {
		segments = append(segments, hintStyle.Render(hint))
	}
	// The typed value outranks the hints: drop hint segments from the tail
	// until the line fits, so the warning is the last to go.
	for len(segments) > 0 {
		hint := strings.Join(segments, hintStyle.Render(dotSep()))
		if ansi.StringWidth(left)+1+ansi.StringWidth(hint) <= width {
			return joinDashboardEnds(left, hint, width)
		}
		segments = segments[:len(segments)-1]
	}
	return ansi.Truncate(left, width, truncTail())
}

// launchHelpMap is the help line while the pad is open.
func launchHelpMap() dashboardHelpMap {
	bind := func(keys, label string) key.Binding {
		return key.NewBinding(key.WithKeys(keys), key.WithHelp(keys, label))
	}
	return dashboardHelpMap{
		bind(enterHint(), "start"),
		bind("tab", "next"),
		bind(arrowsHint(), "field"),
		bind("ctrl+g", "editor"),
		bind("esc", "cancel"),
	}
}

// launchFieldParts returns the label, displayed value, focused-line hint
// segments and an optional warning for a pad field. The focused text field
// shows the live buffer.
func (m Model) launchFieldParts(field launchField) (label, value string, hints []string, warning string) {
	focused := field == m.launchField
	start, cancel := enterHint()+" start", "esc cancel"
	switch field {
	case launchFieldProvider:
		value = firstNonEmpty(m.launchAgent, m.defaultAgent)
		hints = []string{"tab cycle", start, cancel}
		if len(m.profileNames) > 0 || m.defaultProfile != "" {
			value += dotSep() + "profile " + m.launchProfileLabel()
			hints = []string{"tab cycle", "shift+tab profile", start, cancel}
		}
		return "provider", value, hints, ""
	case launchFieldDir:
		dir := m.launchDir
		if focused {
			dir = m.input
		} else {
			dir = m.displayPath(firstNonEmpty(dir, "."))
		}
		if focused && !isGitRepo(firstNonEmpty(m.input, ".")) {
			// No checkpoint exists to recover the agent's work from outside a repo.
			warning = "not a git repo"
		}
		return "directory", dir, []string{"tab completes", start, cancel}, warning
	case launchFieldName:
		name := m.launchName
		if focused {
			name = m.input
		}
		return "name", name, []string{start, arrowsHint() + " field", cancel}, ""
	default:
		prompt := m.launchPrompt
		if focused {
			prompt = m.input
		}
		return "prompt", prompt, []string{"ctrl+g $EDITOR", start, cancel}, ""
	}
}
