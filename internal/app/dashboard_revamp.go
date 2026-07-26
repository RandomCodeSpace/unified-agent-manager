package app

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/version"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// dashboardEntry is one physical body line. sessionIndex ties every line —
// including a block's continuation lines — back to the session it describes,
// which is what lets a mouse tap anywhere inside a block resolve to the same
// target a chip keypress would select. blockStart marks the first line of a
// session block so windowing can refuse to render a block half-open.
type dashboardEntry struct {
	text         string
	sessionIndex int
	blockStart   bool
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
	return shortDuration(now.Sub(createdAt))
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

// providerBadge names the harness that owns a session. It is plain text rather
// than a bracketed token: the borderless layout separates fields by position,
// so brackets would only spend cells a 40-column phone cannot spare.
func providerBadge(sess adapter.Session) string {
	return displaytext.Sanitize(firstNonEmpty(sess.AgentType, "?"))
}

// statusBadge renders the lifecycle word in its session's tone. The tone's
// glyph — not this text — is the guaranteed carrier: compact layouts drop the
// word and keep the glyph.
func statusBadge(sess adapter.Session) string {
	return toneForSession(sess).render(lifecycleBadge(sess))
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
	bodyBudget := max(0, h-dashboardHeaderLines-len(bottom))
	body := m.dashboardBody(w, bodyBudget)
	// The body is padded out to its whole budget so the composer and footer stay
	// pinned to the bottom of the terminal. The retired bordered panel used to
	// fill the frame with its own blank rows; without that padding a short
	// roster collapsed the layout upward and left the command line floating in
	// the middle of the screen with a void beneath it.
	for len(body) < bodyBudget {
		body = append(body, "")
	}
	lines := []string{header}
	lines = append(lines, body...)
	lines = append(lines, bottom...)
	return fitScreen(lines, w, h)
}

// dashboardHeaderLines is the number of lines dashboardView renders above the
// body. Mouse hit-testing subtracts it from the event's row, so it must stay in
// step with dashboardView's composition.
const dashboardHeaderLines = 1

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
	rendered := hintStyle.Render(right)
	// The fleet vitals sit between the brand and the filter affordance so the
	// first line answers "what is the state of everything" before the eye
	// reaches any row. On a narrow screen they degrade category by category
	// rather than vanishing whole, and the passive "/ filter" hint — which the
	// footer repeats — yields to them before they are dropped entirely.
	vitals := vitalsFor(m.sessions)
	budget := width - ansi.StringWidth(left) - ansi.StringWidth(rendered) - 4
	if chips := vitals.chipsWithin(budget); chips != "" {
		gap := width - ansi.StringWidth(left) - ansi.StringWidth(chips) - ansi.StringWidth(rendered) - 2
		return left + strings.Repeat(" ", max(1, gap)) + chips + "  " + rendered
	}
	if !m.filterActive {
		if chips := vitals.chipsWithin(width - ansi.StringWidth(left) - 2); chips != "" {
			gap := width - ansi.StringWidth(left) - ansi.StringWidth(chips)
			return left + strings.Repeat(" ", max(1, gap)) + chips
		}
	}
	available := width - ansi.StringWidth(left) - ansi.StringWidth(rendered)
	if available < 1 {
		return ansi.Truncate(left+"  "+rendered, width, "…")
	}
	return left + strings.Repeat(" ", available) + rendered
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
		hint := firstNonEmpty(m.defaultAgent, "agent") + "  1-9 jump  ↑↓ Enter"
		if m.peekOpen {
			hint = "Enter send  Esc close"
		} else if m.filterActive {
			hint = "↑↓ Enter  Esc clear"
		}
		return []string{joinDashboardEnds(composer, hintStyle.Render(hint), width)}
	}
	footer := firstNonEmpty(m.defaultAgent, "agent") + "  Tab provider  ·  1-9 / tap jump  ·  ↑↓ move  Enter attach  Space peek  / filter  ? help  e new  Esc quit"
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
	return entryLines(m.dashboardBodyEntries(width, budget), width)
}

// dashboardBodyEntries is the single source of truth for the body: dashboardBody
// renders it and the mouse hit-test indexes it, so a tap can never resolve to a
// different session than the one under the pointer.
func (m Model) dashboardBodyEntries(width, budget int) []dashboardEntry {
	if budget <= 0 {
		return nil
	}
	// The wide layout is a permanent two-pane cockpit: the roster stays on the
	// left at one line per session, and the right pane is the selected session's
	// detail and live output. A 30-row terminal holding three sessions used to
	// spend 24 rows on nothing; this is what those rows are for.
	if m.cockpitOpen() {
		leftWidth, rightWidth := cockpitWidths(width)
		if rightWidth >= cockpitMinDetail {
			left := m.sessionPanelEntriesAt(leftWidth, budget, densityMin)
			right := m.cockpitLines(rightWidth, budget)
			return joinEntryColumns(left, right, leftWidth, rightWidth, budget)
		}
	}
	if m.peekOpen {
		return textEntries(m.peekPanelLines(width, budget))
	}
	return m.sessionPanelEntries(width, budget)
}

// cockpitOpen reports whether the two-pane cockpit is in play.
//
// It deliberately does not reuse LayoutWide. LayoutWide demands 28 rows because
// it governs how much *vertical* detail a single column can afford; two columns
// need horizontal room and almost no extra height, and tying them together left
// a 100x26 terminal — an ordinary window — rendering the single-column body with
// two thirds of the screen empty.
//
// A known size is required because layoutClass() classifies an unsized model as
// Wide, and component-level tests render unsized models expecting one column.
func (m Model) cockpitOpen() bool {
	return m.sizeKnown && m.width >= cockpitMinWidth && m.height >= cockpitMinHeight
}

const (
	// cockpitGutter matches the column separator joinEntryColumns inserts.
	cockpitGutter = 3
	// cockpitMinDetail is the narrowest right pane worth rendering; below it the
	// body falls back to one full-width column.
	cockpitMinDetail = 30
	cockpitMinWidth  = 96
	// cockpitMinHeight only has to leave the detail pane a few rows once the
	// header and command line are reserved.
	cockpitMinHeight = 20
)

// cockpitWidths splits the body. The roster is given enough width to stay
// readable but is deliberately the smaller pane: it carries one line per
// session, because the detail it used to expand inline now lives to its right.
func cockpitWidths(width int) (int, int) {
	left := width * 42 / 100
	if left < 34 {
		left = 34
	}
	if left > 56 {
		left = 56
	}
	return left, width - left - cockpitGutter
}

func entryLines(entries []dashboardEntry, width int) []string {
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		lines = append(lines, ansi.Truncate(entry.text, width, "…"))
	}
	return lines
}

func textEntries(lines []string) []dashboardEntry {
	entries := make([]dashboardEntry, 0, len(lines))
	for _, line := range lines {
		entries = append(entries, dashboardEntry{text: line, sessionIndex: -1})
	}
	return entries
}

// joinEntryColumns pairs the session column with the peek column line for line.
// The session column keeps its identity so taps on the left half still resolve;
// the peek half is inert.
func joinEntryColumns(left []dashboardEntry, right []string, leftWidth, rightWidth, budget int) []dashboardEntry {
	n := min(budget, max(len(left), len(right)))
	entries := make([]dashboardEntry, 0, n)
	for i := 0; i < n; i++ {
		entry := dashboardEntry{sessionIndex: -1}
		l := ""
		if i < len(left) {
			entry = left[i]
			l = entry.text
		}
		r := ""
		if i < len(right) {
			r = right[i]
		}
		// A row whose right pane is empty is padded to nothing but trailing
		// spaces, so drop the padding rather than emit a line of whitespace.
		if strings.TrimSpace(r) == "" {
			entry.text = l
		} else {
			entry.text = padRightANSI(l, leftWidth) + "   " + ansi.Truncate(r, rightWidth, "…")
		}
		entries = append(entries, entry)
	}
	return entries
}

// sessionPanelEntries renders the roster borderlessly: one rule line, then one
// block per visible session at the density the remaining budget affords. The
// former panel border cost two rows and two columns; both are handed back to
// the data, which is exactly what a 20-row phone cannot spare.
func (m Model) sessionPanelEntries(width, budget int) []dashboardEntry {
	return m.sessionPanelEntriesAt(width, budget, 0)
}

// sessionPanelEntriesAt renders the roster. forced pins the density rung; 0 lets
// densityFor divide the budget. The cockpit pins it to one line per session
// because the right pane owns the detail.
func (m Model) sessionPanelEntriesAt(width, budget, forced int) []dashboardEntry {
	if budget <= 0 || width <= 0 {
		return nil
	}
	visible := m.visibleSessionIndices()
	rule := dashboardEntry{text: m.sessionsRule(width, visible), sessionIndex: -1}
	if budget == 1 {
		return []dashboardEntry{rule}
	}
	inner := budget - 1
	density := forced
	if density <= 0 {
		density = densityFor(len(visible), inner, m.sectionOverhead(visible))
	}
	entries := m.sessionEntries(width, visible, density)
	return append([]dashboardEntry{rule}, windowBlocks(entries, m.selected, inner)...)
}

// sessionsRule replaces the old panel's top border. The counts on its right are
// harness-independent by construction: they are cardinalities over
// adapter.Session, not anything a provider printed.
func (m Model) sessionsRule(width int, visible []int) string {
	right := fmt.Sprintf("%d", len(m.sessions))
	if m.filterActive {
		right = fmt.Sprintf("%d/%d", len(visible), len(m.sessions))
	}
	if m.groupByDir && m.layoutClass() != LayoutCompact {
		if n := countWorkspaces(m.sessions); n > 1 {
			right += fmt.Sprintf(" · %d ws", n)
		}
	}
	return hairlineRule(sectionStyle.Render("SESSIONS"), hintStyle.Render(right), width)
}

// hairlineRule is the borderless section divider: a label, a thin rule filling
// the gap, and an optional right-aligned annotation. It replaces the panel
// borders everywhere, so the whole UI shares one section vocabulary.
func hairlineRule(title, right string, width int) string {
	if width <= 0 {
		return ""
	}
	if right == "" {
		fill := width - ansi.StringWidth(title) - 1
		if fill < 1 {
			return ansi.Truncate(title, width, "…")
		}
		return title + " " + dividerStyle.Render(strings.Repeat("─", fill))
	}
	fill := width - ansi.StringWidth(title) - ansi.StringWidth(right) - 2
	if fill < 1 {
		return ansi.Truncate(title+" "+right, width, "…")
	}
	return title + " " + dividerStyle.Render(strings.Repeat("─", fill)) + " " + right
}

// cockpitLines renders the right pane: who the selected session is, where it
// runs, what it was asked to do, and what it has most recently printed. It is
// the surface that makes the dashboard answer "what is happening" without an
// attach — the roster answers "what exists".
func (m Model) cockpitLines(width, budget int) []string {
	if budget <= 0 || width <= 0 {
		return nil
	}
	sess, ok := m.selectedSession()
	if !ok {
		if m.filterActive {
			return []string{hintStyle.Render("no session matches the filter")}
		}
		return []string{hintStyle.Render("no sessions yet — type a command or press e")}
	}

	lines := make([]string, 0, budget)
	name := displaytext.Sanitize(firstNonEmpty(sess.DisplayName, sess.ID))

	// Space means "show me what this is doing", so a focused peek gives the
	// output the entire pane rather than making it share with metadata that is
	// one keystroke away.
	if m.peekOpen {
		lines = append(lines, hairlineRule(sectionStyle.Render("PEEK"), hintStyle.Render(name), width))
		tail := boundedTailLines(m.peekText, budget-1, width)
		if len(tail) == 0 {
			tail = []string{hintStyle.Render("waiting for output…")}
		}
		return takeLines(append(lines, tail...), budget)
	}

	lines = append(lines, joinDashboardEnds(
		toneForSession(sess).mark()+" "+titleStyle.Render(name),
		hintStyle.Render(providerBadge(sess))+"  "+statusBadge(sess),
		width,
	))
	lines = append(lines, hintStyle.Render(truncatePathLeft(homeRelativeCwd(sess.Cwd), width)))
	for _, line := range m.cockpitProvenance(sess) {
		lines = append(lines, ansi.Truncate(line, width, "…"))
	}
	if sess.PR != nil {
		lines = append(lines, ansi.Truncate(
			toneForPR(sess.PR.Status).mark()+" "+hintStyle.Render(fmt.Sprintf("pull request #%d %s",
				sess.PR.Number, strings.ToLower(string(sess.PR.Status)))), width, "…"))
	}
	if sess.Pinned {
		lines = append(lines, toneOf(tonePinned).mark()+" "+hintStyle.Render("pinned"))
	}
	if shared := liveWorkspaceCounts(m.sessions)[workspaceKey(sess.Cwd)]; shared > 1 {
		lines = append(lines, ansi.Truncate(toneOf(toneWarn).mark()+
			warnStyle.Render(fmt.Sprintf(" %d live sessions share this workspace", shared)), width, "…"))
	}

	if budget-len(lines) > 6 {
		lines = append(lines, "")
		lines = append(lines, hairlineRule(sectionStyle.Render("TASK"), "", width))
		task := displaytext.Sanitize(taskSummaryText(sess))
		if strings.TrimSpace(task) == "" {
			task = "no task recorded"
		}
		for _, line := range strings.Split(ansi.Wrap(task, width, " -"), "\n") {
			lines = append(lines, taskStyle.Render(line))
			if len(lines) >= budget-4 {
				break
			}
		}
	}

	remaining := budget - len(lines) - 2
	if remaining < 1 {
		return takeLines(lines, budget)
	}
	lines = append(lines, "")
	lines = append(lines, hairlineRule(sectionStyle.Render("OUTPUT"), hintStyle.Render("Space focuses"), width))
	tail := boundedTailLines(m.peekText, remaining, width)
	if len(tail) == 0 {
		hint := "waiting for output…"
		if sess.ProcAlive == adapter.Exited {
			hint = "stopped — Space resumes it in the background"
		}
		tail = []string{hintStyle.Render(hint)}
	}
	lines = append(lines, tail...)
	return takeLines(lines, budget)
}

// cockpitProvenance is the identity block: resume fidelity, id, profile and any
// command alias — the same harness-independent fields the compact rows carry,
// given room to be spelled out.
//
// It returns lines rather than one string on purpose. Joined into a single row
// this overflowed a 61-column pane and truncation silently ate the profile,
// which is exactly the datum someone opens this pane to check. The detail pane's
// abundant resource is vertical space, so it spends that instead.
func (m Model) cockpitProvenance(sess adapter.Session) []string {
	resume := "resumes exactly"
	if resumeTone(sess).key == toneResumeRecent {
		resume = "resumes most recent"
	}
	identity := resumeTone(sess).mark() + " " + hintStyle.Render(resume+" · "+displaytext.Sanitize(sess.ID))

	settings := make([]string, 0, 2)
	selectedProfile, effectiveProfile := m.profileLabels(sess)
	if selectedProfile != "default" || effectiveProfile != "none" {
		settings = append(settings, "profile "+displaytext.Sanitize(selectedProfile)+"→"+displaytext.Sanitize(effectiveProfile))
	}
	if alias := displaytext.Sanitize(sess.CommandAlias); alias != "" && alias != displaytext.Sanitize(sess.AgentType) {
		settings = append(settings, "alias "+alias)
	}
	lines := []string{identity}
	if len(settings) > 0 {
		lines = append(lines, hintStyle.Render(strings.Join(settings, " · ")))
	}
	return lines
}

func countWorkspaces(sessions []adapter.Session) int {
	seen := make(map[string]struct{}, len(sessions))
	for _, sess := range sessions {
		seen[workspaceKey(sess.Cwd)] = struct{}{}
	}
	return len(seen)
}

// sectionOverhead counts the body lines sessionEntries will spend on workspace
// headings and contention warnings. densityFor needs this number *before* the
// density is chosen, because those lines are emitted afterwards and would
// otherwise push the last block off a screen the user was told would fit.
func (m Model) sectionOverhead(visible []int) int {
	if !m.groupByDir || len(visible) == 0 {
		return 0
	}
	count := 0
	live := liveWorkspaceCounts(m.sessions)
	warned := map[string]bool{}
	var lastSection workspaceDashboardSection
	haveSection := false
	for _, index := range visible {
		section := dashboardSectionFor(m.sessions[index])
		if haveSection && section == lastSection {
			continue
		}
		count++
		if live[section.workspace] > 1 && !warned[section.workspace] {
			count++
			warned[section.workspace] = true
		}
		lastSection, haveSection = section, true
	}
	return count
}

func (m Model) sessionEntries(width int, visible []int, density int) []dashboardEntry {
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
	entries := make([]dashboardEntry, 0, len(visible)*density+4)
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
	split := workspaceSectionSplit(m.sessions, visible)
	warned := map[string]bool{}
	var lastSection workspaceDashboardSection
	haveSection := false
	for position, index := range visible {
		sess := m.sessions[index]
		if m.groupByDir {
			section := dashboardSectionFor(sess)
			key := section.workspace
			if !haveSection || section != lastSection {
				entries = append(entries, dashboardEntry{
					text:         ansi.Truncate(m.workspaceHeading(section, shownBySection, totalBySection, visible, split), width, "…"),
					sessionIndex: -1,
				})
				if liveByWorkspace[key] > 1 && !warned[key] {
					warning := " " + toneOf(toneWarn).mark() + warnStyle.Render(fmt.Sprintf(" %d sessions share this workspace", liveByWorkspace[key]))
					entries = append(entries, dashboardEntry{text: ansi.Truncate(warning, width, "…"), sessionIndex: -1})
					warned[key] = true
				}
				lastSection = section
				haveSection = true
			}
		}
		entries = append(entries, m.sessionBlock(sess, index, position, width, density)...)
	}
	return entries
}

// workspaceHeading is borderless and carries section-level aggregates: how many
// sessions the group holds, how many are live, and how long the stalest one has
// sat. All three are counts and timestamp deltas, so they read the same for
// every harness.
//
// Sections are partitioned by lifecycle and pin as well as by directory, so one
// workspace can legitimately head several groups. When that happens the heading
// names the partition — an unlabelled name repeated three times reads as a
// rendering fault rather than as the three distinct groups it is.
func (m Model) workspaceHeading(section workspaceDashboardSection, shown, total map[workspaceDashboardSection]int, visible []int, split map[string]int) string {
	key := section.workspace
	name := displaytext.Sanitize(workspaceDisplayName(key))
	if split[key] > 1 {
		name += hintStyle.Render(" · " + partitionLabel(section))
	}
	group := make([]adapter.Session, 0, total[section])
	for _, index := range visible {
		if dashboardSectionFor(m.sessions[index]) == section {
			group = append(group, m.sessions[index])
		}
	}
	summary := sectionVitalsFor(group, m.dashboardNow()).summary()
	if m.filterActive {
		summary = hintStyle.Render(fmt.Sprintf("%d/%d", shown[section], total[section]))
	}
	return " " + toneOf(toneWorkspace).mark() + " " + sectionStyle.Render(name) + "  " + summary
}

func partitionLabel(section workspaceDashboardSection) string {
	label := "stopped"
	if section.liveness == adapter.Alive {
		label = "live"
	}
	if section.pinned {
		label = "pinned " + label
	}
	return label
}

// workspaceSectionSplit counts how many distinct partitions each workspace is
// spread across in the current projection.
func workspaceSectionSplit(sessions []adapter.Session, visible []int) map[string]int {
	seen := map[workspaceDashboardSection]struct{}{}
	split := map[string]int{}
	for _, index := range visible {
		section := dashboardSectionFor(sessions[index])
		if _, done := seen[section]; done {
			continue
		}
		seen[section] = struct{}{}
		split[section.workspace]++
	}
	return split
}

// chipDigits addresses the first ten visible sessions without a modifier. Digits
// rather than letters: a leading letter is text the composer must be free to
// receive, and stealing nine of them would break dispatching any prompt that
// starts with one. Beyond ten, "/" narrows the roster and re-chips it.
const chipDigits = "1234567890"

func chipFor(position int) string {
	if position < 0 || position >= len(chipDigits) {
		return " "
	}
	return string(chipDigits[position])
}

// sessionBlock emits exactly density lines for every session — not just the
// selected one. Detail stops being a property of the cursor, which is what lets
// three sessions on a phone each show their task, workspace and provenance.
func (m Model) sessionBlock(sess adapter.Session, index, position, width, density int) []dashboardEntry {
	selected := index == m.selected
	edge := " "
	if selected {
		edge = toneOf(toneSelected).mark()
	}
	entries := []dashboardEntry{{
		text:         m.sessionRowPrimary(sess, edge, position, selected, width, density),
		sessionIndex: index,
		blockStart:   true,
	}}
	if density >= 2 {
		task := boundedTaskSummary(sess, max(1, width-6))
		entries = append(entries, dashboardEntry{
			text:         ansi.Truncate(edge+"    "+taskStyle.Render(task), width, "…"),
			sessionIndex: index,
		})
	}
	if density >= 3 {
		entries = append(entries, dashboardEntry{
			text:         ansi.Truncate(edge+"    "+m.sessionProvenance(sess, max(1, width-6)), width, "…"),
			sessionIndex: index,
		})
	}
	return entries
}

func (m Model) sessionRowPrimary(sess adapter.Session, edge string, position int, selected bool, width, density int) string {
	chip := hintStyle.Render(chipFor(position))
	if selected {
		chip = selectedStyle.Render(chipFor(position))
	}
	pin := ""
	if sess.Pinned {
		pin = toneOf(tonePinned).mark() + " "
	}
	name := displaytext.Sanitize(firstNonEmpty(sess.DisplayName, sess.ID))
	// At the densest rung there is no second line, so the task rides inline
	// rather than being lost — except in the cockpit, where the pane to the
	// right is already showing the task in full and squeezing it in here only
	// truncated the session name it has to share the row with.
	if density == 1 {
		if m.cockpitOpen() {
			// The task is on the right, but a failure code is short and is the
			// one thing you must not have to select a row to discover.
			if detail := failureExitDetail(sess); detail != "" {
				name += "  ·  " + detail
			}
		} else if task := displaytext.Sanitize(taskSummaryText(sess)); task != "" {
			name += "  ·  " + task
		}
	}
	style := titleStyle
	if selected {
		style = selectedStyle
	}
	right := m.rowTrailer(sess)
	leftWidth := max(1, width-ansi.StringWidth(right)-1)
	left := edge + chip + " " + toneForSession(sess).mark() + " " + style.Render(ansi.Truncate(pin+name, max(1, leftWidth-4), "…"))
	return padRightANSI(left, width-ansi.StringWidth(right)-1) + " " + right
}

// rowTrailer is the right-aligned status column. The lifecycle word is dropped
// on compact layouts and the tone glyph in the gutter carries liveness there —
// no datum is lost, only its spelled-out form.
func (m Model) rowTrailer(sess adapter.Session) string {
	parts := make([]string, 0, 4)
	if sess.PR != nil {
		parts = append(parts, toneForPR(sess.PR.Status).mark()+hintStyle.Render(fmt.Sprintf("%d", sess.PR.Number)))
	}
	parts = append(parts, hintStyle.Render(providerBadge(sess)))
	// The spelled-out lifecycle word is dropped in two cases: a compact layout,
	// which has no cells to spare, and the cockpit, where the pane to the right
	// spells it out for the focused session and the roster is a navigation list.
	// The tone glyph in the gutter carries liveness in both, so the fact
	// survives and only its wording goes. Keeping the word in a 42-column
	// cockpit roster truncated session names to nothing — a worse trade than
	// dropping a label the adjacent pane already shows.
	if m.layoutClass() != LayoutCompact && !m.cockpitOpen() {
		parts = append(parts, statusBadge(sess))
	}
	parts = append(parts, ageText(sess, m.dashboardNow()))
	return strings.Join(parts, "  ")
}

// sessionProvenance is the third rung: where the session runs, which profile it
// resolves to, whether it can be resumed exactly, and its id. Every field is
// read straight off adapter.Session or the profile map, so the line is byte-for
// byte reproducible for any harness.
func (m Model) sessionProvenance(sess adapter.Session, width int) string {
	// The tail is grounded metadata of bounded length; the path is the one
	// unbounded field. Sizing the path against whatever the tail leaves — and
	// trimming it from the front, since a path is recognised by its end —
	// keeps a deeply nested workspace from pushing the id off the line.
	tail := make([]string, 0, 4)
	// "default→none" is what every session reports when no profile is
	// configured at all, so printing it spends cells to say nothing. Show the
	// pair only once it carries a decision.
	selectedProfile, effectiveProfile := m.profileLabels(sess)
	if selectedProfile != "default" || effectiveProfile != "none" {
		tail = append(tail, hintStyle.Render(displaytext.Sanitize(selectedProfile)+"→"+displaytext.Sanitize(effectiveProfile)))
	}
	tail = append(tail, resumeTone(sess).mark())
	if alias := displaytext.Sanitize(sess.CommandAlias); alias != "" && alias != displaytext.Sanitize(sess.AgentType) {
		tail = append(tail, hintStyle.Render(alias))
	}
	// The id is what `uam attach <id>` takes, so desktop keeps it whole; only a
	// compact screen trades it for a prefix, and it trades it deliberately
	// rather than letting truncation sever it at an arbitrary column.
	id := displaytext.Sanitize(sess.ID)
	if m.layoutClass() == LayoutCompact {
		id = shortID(sess.ID)
	}
	tail = append(tail, hintStyle.Render(id))

	trailer := strings.Join(tail, "  ")
	cwd := homeRelativeCwd(sess.Cwd)
	if budget := width - ansi.StringWidth(trailer) - 2; budget >= 1 {
		cwd = truncatePathLeft(cwd, budget)
	}
	return hintStyle.Render(cwd) + "  " + trailer
}

// truncatePathLeft drops leading path segments rather than trailing ones: the
// last components identify a workspace, the first ones are almost always shared
// prefix.
func truncatePathLeft(path string, width int) string {
	if width <= 1 || ansi.StringWidth(path) <= width {
		return path
	}
	runes := []rune(path)
	for start := 0; start < len(runes); start++ {
		if candidate := "…" + string(runes[start+1:]); ansi.StringWidth(candidate) <= width {
			return candidate
		}
	}
	return "…"
}

// homeRelativeCwd abbreviates the user's home directory to "~". On a 40-column
// screen the leading "/home/<user>" is a dozen cells that say nothing about
// which workspace this is. absCwd keeps its absolute contract for the surfaces
// that need an unambiguous path.
func homeRelativeCwd(cwd string) string {
	abs := absCwd(cwd)
	home, err := os.UserHomeDir()
	if err != nil || home == "" || home == "/" {
		return abs
	}
	if abs == home {
		return "~"
	}
	if strings.HasPrefix(abs, home+string(os.PathSeparator)) {
		return "~" + abs[len(home):]
	}
	return abs
}

func shortID(id string) string {
	clean := displaytext.Sanitize(id)
	runes := []rune(clean)
	if len(runes) <= 8 {
		return clean
	}
	return string(runes[:8])
}

// windowBlocks scrolls in lines but never severs a block: the start is snapped
// back to a block boundary, so a session is either fully on screen or absent
// rather than showing a task line with no name above it.
func windowBlocks(entries []dashboardEntry, selected, budget int) []dashboardEntry {
	if budget <= 0 || len(entries) == 0 {
		return nil
	}
	if len(entries) <= budget {
		return entries
	}
	selectedLine := 0
	for i, entry := range entries {
		if entry.sessionIndex == selected {
			selectedLine = i
			break
		}
	}
	start, _ := visibleWindow(len(entries), selectedLine, budget)
	if start > len(entries)-budget {
		start = len(entries) - budget
	}
	// Snapping back only ever decreases start, so start+budget stays in range.
	for start > 0 && entries[start].sessionIndex >= 0 && !entries[start].blockStart {
		start--
	}
	return entries[start : start+budget]
}

func (m Model) peekPanelLines(width, budget int) []string {
	if budget <= 0 || width <= 0 {
		return nil
	}
	name := ""
	if sess, ok := m.selectedSession(); ok {
		name = firstNonEmpty(sess.DisplayName, sess.ID)
	}
	rule := hairlineRule(sectionStyle.Render("PEEK"), hintStyle.Render(displaytext.Sanitize(name)), width)
	if budget == 1 {
		return []string{rule}
	}
	content := boundedTailLines(m.peekText, budget-1, max(1, width-1))
	if len(content) == 0 {
		content = []string{hintStyle.Render("waiting for output…")}
	}
	lines := make([]string, 0, budget)
	lines = append(lines, rule)
	for _, line := range content {
		lines = append(lines, " "+line)
	}
	return takeLines(lines, budget)
}

// dashboardHitSession maps a terminal row to the session rendered on it. It
// recomposes the body with the same inputs dashboardView used rather than
// caching a hit map, so the mapping cannot go stale between a frame and a click.
func (m Model) dashboardHitSession(row int) (int, bool) {
	w, h := max(1, m.width), max(0, m.height)
	if h == 0 || !m.sizeKnown {
		return 0, false
	}
	if m.helpOpen || m.confirmLatest || m.confirmStop || m.wizard || m.renaming {
		return 0, false
	}
	bottom := m.dashboardBottom(w, h)
	if len(bottom) >= h {
		return 0, false
	}
	bodyBudget := max(0, h-dashboardHeaderLines-len(bottom))
	entries := m.dashboardBodyEntries(w, bodyBudget)
	// fitScreen keeps the tail when a frame overflows, which would shift every
	// row upward. Refuse to guess in that case rather than select the wrong
	// session.
	if dashboardHeaderLines+len(entries)+len(bottom) > h {
		return 0, false
	}
	index := row - dashboardHeaderLines
	if index < 0 || index >= len(entries) {
		return 0, false
	}
	if entries[index].sessionIndex < 0 {
		return 0, false
	}
	return entries[index].sessionIndex, true
}
