package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/version"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

type dashboardEntry struct {
	text         string
	sessionIndex int
}

type workspaceDashboardSection struct {
	workspace string
	liveness  adapter.ProcLiveness
	pinned    bool
}

func dashboardSectionFor(sess adapter.Session) workspaceDashboardSection {
	return workspaceDashboardSection{workspace: workspaceKey(sess.Cwd), liveness: sess.ProcAlive, pinned: sess.Pinned}
}

func (m Model) dashboardNow() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

func sessionAge(createdAt, now time.Time) string {
	if createdAt.IsZero() || createdAt.After(now) {
		return "now"
	}
	age := now.Sub(createdAt)
	switch {
	case age < time.Minute:
		return "now"
	case age < time.Hour:
		return fmt.Sprintf("%dm", int(age/time.Minute))
	case age < 48*time.Hour:
		return fmt.Sprintf("%dh", int(age/time.Hour))
	default:
		return fmt.Sprintf("%dd", int(age/(24*time.Hour)))
	}
}

func lifecycleBadge(sess adapter.Session) string {
	if sess.ProcAlive == adapter.Alive {
		return "RUNNING"
	}
	if detail := failureExitDetail(sess); detail != "" {
		return strings.ToUpper(detail)
	}
	return "STOPPED"
}

func providerBadge(sess adapter.Session) string {
	return "[" + displaytext.Sanitize(firstNonEmpty(sess.AgentType, "?")) + "]"
}

func statusBadge(sess adapter.Session) string {
	label := lifecycleBadge(sess)
	if sess.ProcAlive == adapter.Alive {
		return liveGlyphStyle.Render("[" + label + "]")
	}
	if failureExitDetail(sess) != "" {
		return failGlyphStyle.Render("[" + label + "]")
	}
	return hintStyle.Render("[" + label + "]")
}

func (m Model) sessionMatchesFilter(sess adapter.Session) bool {
	query := strings.TrimSpace(m.filterQuery)
	if !m.filterActive || query == "" {
		return true
	}
	haystack := strings.ToLower(strings.Join([]string{
		displaytext.Sanitize(sess.DisplayName),
		displaytext.Sanitize(sess.ID),
		displaytext.Sanitize(sess.AgentType),
		displaytext.Sanitize(sess.CommandAlias),
		displaytext.Sanitize(sess.Prompt),
		displaytext.Sanitize(sess.Cwd),
		lifecycleBadge(sess),
	}, "\n"))
	for _, term := range strings.Fields(strings.ToLower(query)) {
		if !strings.Contains(haystack, term) {
			return false
		}
	}
	return true
}

func (m Model) visibleSessionIndices() []int {
	indices := make([]int, 0, len(m.sessions))
	for i, sess := range m.sessions {
		if m.sessionMatchesFilter(sess) {
			indices = append(indices, i)
		}
	}
	return indices
}

func (m *Model) enterFilter() {
	if m.filterActive {
		return
	}
	if sess, ok := m.selectedSession(); ok {
		m.filterRestore = sessionIdentity{agent: sess.AgentType, id: sess.ID}
		m.filterSaved = true
	}
	m.filterActive = true
	m.filterQuery = ""
	m.reconcileFilterSelection()
}

func (m *Model) exitFilter() {
	restore, saved := m.filterRestore, m.filterSaved
	m.filterActive = false
	m.filterQuery = ""
	m.filterRestore = sessionIdentity{}
	m.filterSaved = false
	if !saved {
		return
	}
	for i, sess := range m.sessions {
		if sess.AgentType == restore.agent && sess.ID == restore.id {
			m.selected = i
			return
		}
	}
}

func (m *Model) reconcileFilterSelection() {
	if !m.filterActive {
		return
	}
	for _, index := range m.visibleSessionIndices() {
		if index == m.selected {
			return
		}
	}
	visible := m.visibleSessionIndices()
	if len(visible) > 0 {
		m.selected = visible[0]
	}
}

func (m *Model) handleFilterKey(msg tea.KeyMsg, key string) (bool, tea.Cmd) {
	noMatches := len(m.visibleSessionIndices()) == 0
	switch key {
	case "esc":
		m.exitFilter()
		return true, nil
	case "backspace":
		if m.filterQuery == "" {
			m.exitFilter()
			return true, nil
		}
		runes := []rune(m.filterQuery)
		m.filterQuery = string(runes[:len(runes)-1])
		m.reconcileFilterSelection()
		return true, nil
	case "up":
		return true, m.moveSelectionPeek(-1)
	case "down":
		return true, m.moveSelectionPeek(1)
	case "shift+up":
		return true, m.moveSession(-1)
	case "shift+down":
		return true, m.moveSession(1)
	case "enter", "right":
		if noMatches {
			return true, nil
		}
		return true, m.handleEnterKey()
	case " ":
		if noMatches {
			return true, nil
		}
		return true, m.handleSpaceKey(key)
	case "ctrl+t", "ctrl+r", "ctrl+x":
		if noMatches {
			return true, nil
		}
	}
	if msg.Type == tea.KeyRunes && !msg.Alt {
		m.filterQuery += string(msg.Runes)
		m.reconcileFilterSelection()
		return true, nil
	}
	return false, nil
}

func (m *Model) moveFilteredSession(delta int) tea.Cmd {
	visible := m.visibleSessionIndices()
	position := -1
	for i, index := range visible {
		if index == m.selected {
			position = i
			break
		}
	}
	if position < 0 || position+delta < 0 || position+delta >= len(visible) {
		return nil
	}
	return m.moveSessionTo(visible[position+delta])
}

func (m Model) dashboardView() string {
	w, h := max(1, m.width), max(0, m.height)
	if h == 0 {
		return ""
	}
	if m.helpOpen || m.confirmLatest || m.confirmStop || m.wizard || m.renaming {
		return m.responsiveView()
	}
	header := m.dashboardHeader(w)
	bottom := m.dashboardBottom(w, h)
	if len(bottom) >= h {
		return fitScreen(bottom[:h], w, h)
	}
	bodyBudget := max(0, h-1-len(bottom))
	body := m.dashboardBody(w, bodyBudget)
	lines := []string{header}
	lines = append(lines, body...)
	lines = append(lines, bottom...)
	return fitScreen(lines, w, h)
}

func (m Model) dashboardHeader(width int) string {
	left := bar() + " " + brandStyle.Render("UAM")
	if m.layoutClass() == LayoutCompact {
		left += "  " + hintStyle.Render(version.String())
	} else {
		left += "  " + hintStyle.Render("Unified Agent Manager") + "  " + hintStyle.Render(version.String())
	}
	right := "/ filter"
	if m.filterActive {
		right = "/ " + displaytext.Sanitize(m.filterQuery)
		if strings.TrimSpace(m.filterQuery) == "" {
			right = "/ filter sessions"
		}
	}
	available := width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if available < 1 {
		return ansi.Truncate(left+"  "+right, width, "…")
	}
	return left + strings.Repeat(" ", available) + hintStyle.Render(right)
}

func (m Model) dashboardBottom(width, height int) []string {
	field := hintStyle.Render("type a command…")
	label := ""
	if m.input != "" {
		field = titleStyle.Render(displaytext.Sanitize(m.input))
	}
	if m.peekOpen {
		label = hintStyle.Render("reply ")
		if m.input == "" {
			field = hintStyle.Render("type a reply…")
		}
	} else if m.filterActive {
		label = hintStyle.Render("filter / ")
		field = hintStyle.Render("type to filter…")
		if m.filterQuery != "" {
			field = titleStyle.Render(displaytext.Sanitize(m.filterQuery))
		}
	}
	composer := ansi.Truncate(bar()+" "+label+brandStyle.Render("›")+" "+field+brandStyle.Render("▏"), width, "…")
	if height <= 12 {
		hint := firstNonEmpty(m.defaultAgent, "agent") + "  ↑↓ Enter"
		if m.peekOpen {
			hint = "Enter send  Esc close"
		} else if m.filterActive {
			hint = "↑↓ Enter  Esc clear"
		}
		return []string{joinDashboardEnds(composer, hintStyle.Render(hint), width)}
	}
	footer := firstNonEmpty(m.defaultAgent, "agent") + "  Tab provider  ·  ↑↓ move  Enter attach  Space peek  / filter  ? help  e new  Esc quit"
	if m.peekOpen {
		footer = "↑↓ session  Enter send  Space close peek  Esc close"
	} else if m.filterActive {
		footer = "type to filter  ↑↓ move  Enter open  Esc clear"
	}
	lines := []string{composer, ansi.Truncate("  "+hintStyle.Render(footer), width, "…")}
	if m.message != "" && height >= 16 {
		lines = append(lines, ansi.Truncate("  "+hintStyle.Render(displaytext.Sanitize(m.message)), width, "…"))
	}
	return lines
}

func joinDashboardEnds(left, right string, width int) string {
	right = ansi.Truncate(right, width, "…")
	leftWidth := max(0, width-ansi.StringWidth(right)-1)
	left = ansi.Truncate(left, leftWidth, "…")
	gap := max(1, width-ansi.StringWidth(left)-ansi.StringWidth(right))
	return ansi.Truncate(left+strings.Repeat(" ", gap)+right, width, "…")
}

func (m Model) dashboardBody(width, budget int) []string {
	if budget <= 0 {
		return nil
	}
	if m.peekOpen && m.layoutClass() == LayoutWide {
		leftWidth := max(46, width*58/100)
		rightWidth := width - leftWidth - 3
		left := m.dashboardSessionPanel(leftWidth, budget)
		right := m.dashboardPeekPanel(rightWidth, budget)
		return joinColumns(left, right, leftWidth, rightWidth, budget)
	}
	if m.peekOpen {
		return m.dashboardPeekPanel(width, budget)
	}
	return m.dashboardSessionPanel(width, budget)
}

func (m Model) dashboardSessionPanel(width, budget int) []string {
	visible := m.visibleSessionIndices()
	right := fmt.Sprintf("%d sessions", len(m.sessions))
	if m.filterActive {
		right = fmt.Sprintf("%d/%d sessions", len(visible), len(m.sessions))
	}
	entries := m.dashboardEntries(max(1, width-2), visible)
	return borderedPanel("SESSIONS", right, entries, m.selected, width, budget)
}

func (m Model) dashboardPeekPanel(width, budget int) []string {
	name := ""
	if sess, ok := m.selectedSession(); ok {
		name = firstNonEmpty(sess.DisplayName, sess.ID)
	}
	contentBudget := max(0, budget-2)
	content := boundedTailLines(m.peekText, contentBudget, max(1, width-4))
	if len(content) == 0 {
		content = []string{hintStyle.Render("waiting for output…")}
	}
	entries := make([]dashboardEntry, 0, len(content))
	for _, line := range content {
		entries = append(entries, dashboardEntry{text: " " + line, sessionIndex: -1})
	}
	return borderedPanel("PEEK", displaytext.Sanitize(name), entries, -1, width, budget)
}

func (m Model) dashboardEntries(width int, visible []int) []dashboardEntry {
	if len(visible) == 0 {
		if m.filterActive {
			query := displaytext.Sanitize(m.filterQuery)
			return []dashboardEntry{
				{text: " " + titleStyle.Render("No sessions match “"+query+"”"), sessionIndex: -1},
				{text: " " + hintStyle.Render("Esc clear filter"), sessionIndex: -1},
			}
		}
		return []dashboardEntry{{text: " " + hintStyle.Render("No sessions yet — type a command or press e"), sessionIndex: -1}}
	}
	entries := make([]dashboardEntry, 0, len(visible)*2)
	shownBySection := map[workspaceDashboardSection]int{}
	totalBySection := map[workspaceDashboardSection]int{}
	if m.groupByDir {
		for _, sess := range m.sessions {
			totalBySection[dashboardSectionFor(sess)]++
		}
		for _, index := range visible {
			shownBySection[dashboardSectionFor(m.sessions[index])]++
		}
	}
	liveByWorkspace := liveWorkspaceCounts(m.sessions)
	warned := map[string]bool{}
	var lastSection workspaceDashboardSection
	haveSection := false
	for _, index := range visible {
		sess := m.sessions[index]
		if m.groupByDir {
			section := dashboardSectionFor(sess)
			key := section.workspace
			if !haveSection || section != lastSection {
				count := fmt.Sprintf("%d", totalBySection[section])
				if m.filterActive {
					count = fmt.Sprintf("%d/%d", shownBySection[section], totalBySection[section])
				}
				heading := " WORKSPACE " + displaytext.Sanitize(workspaceDisplayName(key)) + "  " + hintStyle.Render(count)
				entries = append(entries, dashboardEntry{text: ansi.Truncate(sectionStyle.Render(heading), width, "…"), sessionIndex: -1})
				if liveByWorkspace[key] > 1 && !warned[key] {
					warning := fmt.Sprintf(" ⚠ %d sessions share this workspace", liveByWorkspace[key])
					entries = append(entries, dashboardEntry{text: ansi.Truncate(warnStyle.Render(warning), width, "…"), sessionIndex: -1})
					warned[key] = true
				}
				lastSection = section
				haveSection = true
			}
		}
		entries = append(entries, m.dashboardSessionEntries(sess, index, width)...)
	}
	return entries
}

func (m Model) dashboardSessionEntries(sess adapter.Session, index, width int) []dashboardEntry {
	selected := index == m.selected
	primary := m.dashboardSessionPrimary(sess, selected, width)
	entries := []dashboardEntry{{text: primary, sessionIndex: index}}
	if !selected {
		return entries
	}
	details := []string{taskStyle.Render(boundedTaskSummary(sess, max(1, width-4)))}
	if m.layoutClass() != LayoutCompact {
		selectedProfile, effectiveProfile := m.profileLabels(sess)
		details = append(details, hintStyle.Render("profile selected: "+displaytext.Sanitize(selectedProfile)+"  effective: "+displaytext.Sanitize(effectiveProfile)))
		details = append(details, hintStyle.Render("cwd "+absCwd(sess.Cwd)))
		details = append(details, hintStyle.Render("id "+displaytext.Sanitize(sess.ID)))
		if sess.PR != nil {
			details = append(details, hintStyle.Render(fmt.Sprintf("PR #%d %s", sess.PR.Number, strings.ToLower(string(sess.PR.Status)))))
		}
	}
	for i, detail := range details {
		marker := brandStyle.Render("│") + "  "
		if i == len(details)-1 {
			marker = brandStyle.Render("╰─") + " "
		}
		entries = append(entries, dashboardEntry{text: ansi.Truncate(marker+detail, width, "…"), sessionIndex: index})
	}
	return entries
}

func (m Model) dashboardSessionPrimary(sess adapter.Session, selected bool, width int) string {
	cursor := "  "
	if selected {
		cursor = brandStyle.Render("╭▸") + " "
	}
	pin := ""
	if sess.Pinned {
		pin = "★ "
	}
	right := providerBadge(sess) + " " + statusBadge(sess) + " " + hintStyle.Render(sessionAge(sess.CreatedAt, m.dashboardNow()))
	leftWidth := max(1, width-ansi.StringWidth(cursor)-ansi.StringWidth(right)-1)
	name := displaytext.Sanitize(firstNonEmpty(sess.DisplayName, sess.ID))
	if !selected && m.layoutClass() != LayoutCompact {
		task := displaytext.Sanitize(taskSummaryText(sess))
		if task != "" {
			name += "  ·  " + task
		}
	}
	style := titleStyle
	if selected {
		style = selectedStyle
	}
	left := cursor + style.Render(ansi.Truncate(pin+name, leftWidth, "…"))
	return padRightANSI(left, width-ansi.StringWidth(right)-1) + " " + right
}

func borderedPanel(title, right string, entries []dashboardEntry, selected, width, budget int) []string {
	if budget <= 0 || width <= 0 {
		return nil
	}
	if budget == 1 || width < 4 {
		return []string{ansi.Truncate(title+" "+right, width, "…")}
	}
	inner := width - 2
	topLeft := "─ " + title + " "
	topRight := " " + right + " ─"
	if ansi.StringWidth(topLeft)+ansi.StringWidth(topRight) > inner {
		topRight = " ─"
		topLeft = ansi.Truncate(topLeft, max(1, inner-ansi.StringWidth(topRight)), "…")
	}
	top := "╭" + topLeft + strings.Repeat("─", max(0, inner-ansi.StringWidth(topLeft)-ansi.StringWidth(topRight))) + topRight + "╮"
	contentBudget := max(0, budget-2)
	window := dashboardEntryWindow(entries, selected, contentBudget)
	lines := make([]string, 0, budget)
	lines = append(lines, ansi.Truncate(top, width, "…"))
	for _, entry := range window {
		lines = append(lines, "│"+padRightANSI(entry.text, inner)+"│")
	}
	for len(lines) < budget-1 {
		lines = append(lines, "│"+strings.Repeat(" ", inner)+"│")
	}
	lines = append(lines, "╰"+strings.Repeat("─", inner)+"╯")
	return takeLines(lines, budget)
}

func dashboardEntryWindow(entries []dashboardEntry, selected, budget int) []dashboardEntry {
	if budget <= 0 || len(entries) == 0 {
		return nil
	}
	selectedLine := 0
	for i, entry := range entries {
		if entry.sessionIndex == selected {
			selectedLine = i
			break
		}
	}
	start, end := visibleWindow(len(entries), selectedLine, budget)
	return entries[start:end]
}
