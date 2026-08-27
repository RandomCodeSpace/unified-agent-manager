package app

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/version"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// ─── the DEPARTURES board ─────────────────────────────────────────────────────
//
// The dashboard is an airport departures board: one flat table, one row per
// session, fixed columns that read top to bottom at a glance. The metaphor is
// load-bearing rather than decorative — every column maps to an operational
// question. SESSION is the craft, OPERATOR is the harness flying it, TASK is
// the flight plan, STATUS is EN ROUTE / DIVERTED / ARRIVED over exactly the
// distinctions toneForSession draws, GATE is the one verb that acts on the row,
// and DUE is how long the craft has been out. Everything on the board is a pure
// field read off adapter.Session, so a claude row and a codex row render
// identically from the same facts.
//
// Two geometries share the vocabulary: the wide board spells every column out;
// the compact board (a phone with the keyboard up) keeps Nº, a two-letter
// operator code, the name, the status cell and the age on a single line.

// dashboardEntry is one physical body line. sessionIndex ties every line back
// to the session it describes, which is what lets a mouse tap anywhere on a row
// resolve to the same target a chip keypress would select. blockStart marks the
// first line of a session block so windowing can refuse to render a block
// half-open.
type dashboardEntry struct {
	text         string
	sessionIndex int
	blockStart   bool
}

func (m Model) dashboardNow() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
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
		boardStatusWord(sess),
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
		m.moveSelection(-1)
		return true, nil
	case "down":
		m.moveSelection(1)
		return true, nil
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
		m.filterQuery += " "
		m.reconcileFilterSelection()
		return true, nil
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
	// pinned to the bottom of the terminal; a short roster must not collapse the
	// layout upward and leave the command line floating mid-screen.
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

// boardHost names the machine the fleet runs on — the datum that
// distinguishes one uam masthead from another when the user has three SSH tabs
// open to three hosts. Resolved once: a hostname does not change mid-session.
var boardHost = sync.OnceValue(func() string {
	host, err := os.Hostname()
	if err != nil {
		return ""
	}
	return displaytext.Sanitize(host)
})

// dashboardHeader is the masthead. Both geometries carry the brand and the
// version — the version is how a "works here, broken there" report across the
// user's machines starts being answerable.
func (m Model) dashboardHeader(width int) string {
	clock := m.dashboardNow().Format("15:04")
	if !boardColumns(width).wide {
		left := bar() + " " + brandStyle.Render("UAM") + " " + titleStyle.Render(version.String()) +
			hintStyle.Render(dotSep()+"DEPARTURES"+dotSep()+clock)
		// The filtered count is the one wide-masthead datum the phone cannot
		// lose: it is how "why is the board suddenly short" answers itself.
		if m.filterActive {
			left += hintStyle.Render(dotSep() + fmt.Sprintf("%d/%d", len(m.visibleSessionIndices()), len(m.sessions)))
		}
		return ansi.Truncate(left, width, truncTail())
	}
	left := bar() + " " + brandStyle.Render("UNIFIED AGENT MANAGER (UAM)") + hintStyle.Render(dotSep()+"DEPARTURES")
	craft := fmt.Sprintf("%d craft", len(m.sessions))
	if m.filterActive {
		craft = fmt.Sprintf("%d/%d craft", len(m.visibleSessionIndices()), len(m.sessions))
	}
	right := mastheadRight(boardHost(), clock, craft, width-ansi.StringWidth(left)-2)
	return joinDashboardEnds(left, right, width)
}

// mastheadRightHostCap bounds the hostname's spend in the masthead. Cloud
// hosts carry provisioning-id names sixty cells long; past this point the
// name stops identifying the machine to a human and starts eating the brand.
const mastheadRightHostCap = 20

// mastheadRight fits the version, clock, host and craft count into the cells
// the brand leaves over, dropping the host first and the clock second rather
// than letting truncation shear the brand off the left edge — which is exactly
// what a long cloud hostname used to do. The version and the craft count are
// the two segments a bug report and a glance both need, so they go last.
func mastheadRight(host, clock, craft string, budget int) string {
	if host != "" {
		host = ansi.Truncate(host, mastheadRightHostCap, truncTail())
	}
	candidates := [][]string{{clock, host, craft}, {clock, craft}, {craft}}
	for _, segments := range candidates {
		kept := make([]string, 0, len(segments))
		for _, segment := range segments {
			if segment != "" {
				kept = append(kept, segment)
			}
		}
		right := titleStyle.Render(version.String()) + hintStyle.Render(dotSep()+strings.Join(kept, dotSep()))
		if ansi.StringWidth(right) <= budget {
			return right
		}
	}
	return titleStyle.Render(version.String())
}

// ─── board geometry ───────────────────────────────────────────────────────────

// boardWideMin is the width at which the full column board fits. Below it the
// compact rows carry the same facts in an abbreviated spelling.
const boardWideMin = 78

const (
	boardNumWidth    = 2
	boardSessionCol  = 18
	boardOperatorCol = 8
	// boardStatusWidth fits the widest cell: two shade cells, a space, and
	// "EN ROUTE".
	boardStatusWidth = 11
	// boardGateWidth fits "RESUME ⇄".
	boardGateWidth = 8
	boardDueWidth  = 3
)

// boardLayout is the resolved geometry for one frame. gateX/gateW exist so the
// mouse hit-test and the renderer cannot disagree about where the GATE cells
// are: both read the same numbers.
type boardLayout struct {
	wide bool
	// task is the flexible column's width on the wide board.
	task int
	// name is the flexible name width on the compact board.
	name  int
	gateX int
	gateW int
}

func boardColumns(width int) boardLayout {
	// Fixed spend on the wide board: edge(1) sp(1) Nº(2) gap(3) session gap(2)
	// operator gap(2) [task] gap(2) status gap(2) gate gap(2) due.
	fixed := 1 + 1 + boardNumWidth + 3 + boardSessionCol + 2 + boardOperatorCol + 2 +
		2 + boardStatusWidth + 2 + boardGateWidth + 2 + boardDueWidth
	if width >= boardWideMin {
		task := width - fixed
		return boardLayout{
			wide:  true,
			task:  task,
			gateX: 1 + 1 + boardNumWidth + 3 + boardSessionCol + 2 + boardOperatorCol + 2 + task + 2 + boardStatusWidth + 2,
			gateW: boardGateWidth,
		}
	}
	// Compact spend: edge(1) Nº(2) sp code(2) sp [name] sp status sp(2) due.
	compactFixed := 1 + boardNumWidth + 1 + 2 + 1 + 1 + boardStatusWidth + 2 + boardDueWidth
	return boardLayout{wide: false, name: max(6, width-compactFixed)}
}

// ─── board vocabulary ─────────────────────────────────────────────────────────

// boardStatusWord maps the lifecycle onto the departure vocabulary using
// exactly the distinctions toneForSession draws, so the word and the tone can
// never disagree. The words are pairwise distinct on purpose: they are the
// carrier of the datum when color is gone, the shade cell only reinforces.
// HOLDING is reserved for a future blocked-state signal.
func boardStatusWord(sess adapter.Session) string {
	switch toneForSession(sess).key {
	case toneLive:
		return "EN ROUTE"
	case toneFailed:
		return "DIVERTED"
	default:
		return "ARRIVED"
	}
}

// boardShade is the two-cell fill in front of the status word: solid for a
// craft en route, heavy shade for a diverted one, light shade for one arrived.
// Block Elements are East-Asian-Ambiguous, so the ASCII set swaps them with
// the rest of the vocabulary.
func boardShade(sess adapter.Session) string {
	if asciiGlyphs() {
		switch toneForSession(sess).key {
		case toneLive:
			return "##"
		case toneFailed:
			return "XX"
		default:
			return ".."
		}
	}
	switch toneForSession(sess).key {
	case toneLive:
		return "██"
	case toneFailed:
		return "▓▓"
	default:
		return "░░"
	}
}

func boardStatusCell(sess adapter.Session) string {
	return toneForSession(sess).render(boardShade(sess) + " " + boardStatusWord(sess))
}

// gateLabel is the one verb the row offers: a live craft is boarded with
// ATTACH; a stopped one is sent back out with RESUME, marked by whether the
// resume is exact or the provider's most-recent heuristic — the same
// distinction resumeTone draws everywhere else.
func gateLabel(sess adapter.Session) string {
	if sess.ProcAlive == adapter.Alive {
		return "ATTACH"
	}
	return "RESUME " + resumeTone(sess).activeGlyph()
}

// operatorCodes are the airline codes the compact board flies under: two cells
// per harness, legend rendered above the composer. An unknown harness keeps
// its first two letters, so a new provider is abbreviated rather than blank.
var operatorCodes = map[string]string{
	"claude":   "CL",
	"codex":    "CX",
	"opencode": "OC",
	"omp":      "OM",
	"copilot":  "CP",
	"hermes":   "HM",
}

func operatorCode(agentType string) string {
	key := strings.ToLower(strings.TrimSpace(displaytext.Sanitize(agentType)))
	if code, ok := operatorCodes[key]; ok {
		return code
	}
	runes := []rune(strings.ToUpper(key))
	switch {
	case len(runes) >= 2:
		return string(runes[:2])
	case len(runes) == 1:
		return string(runes) + " "
	default:
		return "??"
	}
}

// ─── body ─────────────────────────────────────────────────────────────────────

func (m Model) dashboardBody(width, budget int) []string {
	return entryLines(m.dashboardBodyEntries(width, budget), width)
}

// dashboardBodyEntries is the single source of truth for the body: dashboardBody
// renders it and the mouse hit-test indexes it, so a tap can never resolve to a
// different session than the one under the pointer.
func (m Model) dashboardBodyEntries(width, budget int) []dashboardEntry {
	if budget <= 0 || width <= 0 {
		return nil
	}
	lay := boardColumns(width)
	chrome := []dashboardEntry{{text: boardRule(width), sessionIndex: -1}}
	if lay.wide && budget >= 4 {
		chrome = append(chrome,
			dashboardEntry{text: boardHeadings(lay, width), sessionIndex: -1},
			dashboardEntry{text: boardRule(width), sessionIndex: -1},
		)
	}
	rowBudget := budget - len(chrome)
	if rowBudget <= 0 {
		return chrome[:budget]
	}
	rows := m.boardRows(lay, width)
	return append(chrome, windowBlocks(rows, m.selected, rowBudget)...)
}

// boardRule is the full-width hairline that frames the board.
func boardRule(width int) string {
	if width <= 0 {
		return ""
	}
	return dividerStyle.Render(strings.Repeat(ruleGlyph(), width))
}

// boardHeadings mirrors boardRow's geometry cell for cell; a heading that
// drifted from its column would be worse than none.
func boardHeadings(lay boardLayout, width int) string {
	heading := "  " + padRightANSI(numHeading(), boardNumWidth) + "   " +
		padRightANSI("SESSION", boardSessionCol) + "  " +
		padRightANSI("OPERATOR", boardOperatorCol) + "  " +
		padRightANSI("TASK", lay.task) + "  " +
		padRightANSI("STATUS", boardStatusWidth) + "  " +
		padRightANSI("GATE", lay.gateW) + "  " +
		padLeftANSI("DUE", boardDueWidth)
	return ansi.Truncate(hintStyle.Render(heading), width, truncTail())
}

// numHeading spells Nº — U+00BA is East-Asian-Ambiguous, so the ASCII set
// writes it out.
func numHeading() string {
	if asciiGlyphs() {
		return "No"
	}
	return "Nº"
}

func (m Model) boardRows(lay boardLayout, width int) []dashboardEntry {
	visible := m.visibleSessionIndices()
	if len(visible) == 0 {
		if m.filterActive {
			query := displaytext.Sanitize(m.filterQuery)
			return []dashboardEntry{
				{text: "  " + titleStyle.Render("no craft matches \""+query+"\""), sessionIndex: -1},
				{text: "  " + hintStyle.Render("Esc clears the filter"), sessionIndex: -1},
			}
		}
		return []dashboardEntry{{text: "  " + hintStyle.Render("no scheduled departures — type a command or press e"), sessionIndex: -1}}
	}
	now := m.dashboardNow()
	rows := make([]dashboardEntry, 0, len(visible))
	for position, index := range visible {
		text := ""
		if lay.wide {
			text = m.boardRow(m.sessions[index], index, position, lay, now)
		} else {
			text = m.boardRowCompact(m.sessions[index], index, position, lay, now)
		}
		rows = append(rows, dashboardEntry{
			text:         ansi.Truncate(text, width, truncTail()),
			sessionIndex: index,
			blockStart:   true,
		})
	}
	return rows
}

// boardRow is one wide departure. The columns are fixed so thirty rows scan as
// seven vertical stripes rather than thirty sentences.
func (m Model) boardRow(sess adapter.Session, index, position int, lay boardLayout, now time.Time) string {
	selected := index == m.selected
	edge := " "
	nameStyle, numStyle := titleStyle, hintStyle
	if selected {
		edge = toneOf(toneSelected).mark()
		nameStyle, numStyle = selectedStyle, selectedStyle
	}
	name := strings.ToUpper(displaytext.Sanitize(firstNonEmpty(sess.DisplayName, sess.ID)))
	operator := strings.ToUpper(providerBadge(sess))
	return edge + " " +
		numStyle.Render(boardNumber(position)) + "   " +
		padRightANSI(nameStyle.Render(ansi.Truncate(name, boardSessionCol, truncTail())), boardSessionCol) + "  " +
		padRightANSI(hintStyle.Render(ansi.Truncate(operator, boardOperatorCol, truncTail())), boardOperatorCol) + "  " +
		padRightANSI(taskStyle.Render(boundedTaskSummary(sess, lay.task)), lay.task) + "  " +
		padRightANSI(boardStatusCell(sess), boardStatusWidth) + "  " +
		padRightANSI(brandStyle.Render(gateLabel(sess)), lay.gateW) + "  " +
		padLeftANSI(ageText(sess, now), boardDueWidth)
}

// boardRowCompact is one departure with the keyboard up: number, operator
// code, name, status cell, age. Same facts, abbreviated spelling.
func (m Model) boardRowCompact(sess adapter.Session, index, position int, lay boardLayout, now time.Time) string {
	selected := index == m.selected
	edge := " "
	nameStyle, numStyle := titleStyle, hintStyle
	if selected {
		edge = toneOf(toneSelected).mark()
		nameStyle, numStyle = selectedStyle, selectedStyle
	}
	name := strings.ToUpper(displaytext.Sanitize(firstNonEmpty(sess.DisplayName, sess.ID)))
	return edge +
		numStyle.Render(boardNumber(position)) + " " +
		brandStyle.Render(operatorCode(sess.AgentType)) + " " +
		padRightANSI(nameStyle.Render(ansi.Truncate(name, lay.name, truncTail())), lay.name) + " " +
		padRightANSI(boardStatusCell(sess), boardStatusWidth) + "  " +
		padLeftANSI(ageText(sess, now), boardDueWidth)
}

// boardNumber is the two-digit flight number. It exists to match chipDigits:
// row 01 answers key 1, row 10 answers key 0.
func boardNumber(position int) string {
	return fmt.Sprintf("%02d", position+1)
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

// chipRangeLabel names the live digit range for the footer: "keys 1-4" for a
// four-row board rather than a static promise about keys that do nothing.
func chipRangeLabel(visible int) string {
	bound := min(visible, len(chipDigits))
	switch {
	case bound <= 0:
		return ""
	case bound == 1:
		return "key 1"
	case bound == 10:
		return "keys 1-0"
	default:
		return fmt.Sprintf("keys 1-%d", bound)
	}
}

// ─── bottom: rule, legend, advisory, composer ────────────────────────────────

func (m Model) dashboardBottom(width, height int) []string {
	lay := boardColumns(width)
	lines := make([]string, 0, 5)
	// A terminal squeezed to a couple of rows keeps the composer — the one
	// line the user acts through — and sheds the frame around it.
	if height >= 4 {
		lines = append(lines, boardRule(width))
	}
	if !lay.wide && height >= 12 {
		if legend := m.operatorLegend(width); legend != "" {
			lines = append(lines, legend)
		}
	}
	if height >= 10 {
		if advisory := m.boardAdvisory(width, lay.wide); advisory != "" {
			lines = append(lines, advisory)
		}
	}
	lines = append(lines, m.boardComposer(width, lay.wide))
	if m.message != "" && height >= 14 {
		lines = append(lines, ansi.Truncate("  "+hintStyle.Render(displaytext.Sanitize(m.message)), width, truncTail()))
	}
	return lines
}

// operatorLegend decodes the compact board's airline codes, listing only the
// operators actually on the board.
func (m Model) operatorLegend(width int) string {
	seen := map[string]bool{}
	parts := make([]string, 0, 4)
	for _, index := range m.visibleSessionIndices() {
		name := strings.ToLower(providerBadge(m.sessions[index]))
		if seen[name] {
			continue
		}
		seen[name] = true
		parts = append(parts, brandStyle.Render(operatorCode(m.sessions[index].AgentType))+" "+hintStyle.Render(name))
	}
	if len(parts) == 0 {
		return ""
	}
	return ansi.Truncate(strings.Join(parts, hintStyle.Render(dotSep())), width, truncTail())
}

// boardAdvisory is the one-line ground report above the composer. A boarding
// call — the newest diverted craft and how its gate resumes it — outranks the
// workspace-contention advisory; both are facts the operator should not have
// to select a row to discover.
func (m Model) boardAdvisory(width int, wide bool) string {
	if call := m.boardingCall(width, wide); call != "" {
		return call
	}
	return m.contentionAdvisory(width)
}

func (m Model) boardingCall(width int, wide bool) string {
	visible := m.visibleSessionIndices()
	position, found := -1, false
	var newest time.Time
	for pos, index := range visible {
		sess := m.sessions[index]
		if failureExitDetail(sess) == "" {
			continue
		}
		if !found || sess.CreatedAt.After(newest) {
			position, newest, found = pos, sess.CreatedAt, true
		}
	}
	if !found {
		return ""
	}
	sess := m.sessions[visible[position]]
	detail := failureExitDetail(sess)
	resume := "resumes exactly"
	if resumeTone(sess).key == toneResumeRecent {
		resume = "resumes most recent"
	}
	if !wide {
		return ansi.Truncate(warnStyle.Render(boardNumber(position)+" diverted"+dotSep()+detail+dotSep()+"gate "+resumeTone(sess).activeGlyph()),
			width, truncTail())
	}
	return ansi.Truncate("  "+warnStyle.Render("boarding call: "+boardNumber(position)+" diverted"+dotSep()+detail+dashSep()+"gate "+resume),
		width, truncTail())
}

// contentionAdvisory warns when several live craft share one workspace — two
// agents mutating the same checkout is the accident the operator most wants
// called out before it happens.
func (m Model) contentionAdvisory(width int) string {
	counts := liveWorkspaceCounts(m.sessions)
	worstKey, worst := "", 0
	for key, n := range counts {
		// Ties break lexically so the advisory is stable frame to frame.
		if n > worst || (n == worst && key < worstKey) {
			worstKey, worst = key, n
		}
	}
	if worst <= 1 {
		return ""
	}
	text := fmt.Sprintf("advisory: %d live craft share %s", worst, displaytext.Sanitize(workspaceDisplayName(worstKey)))
	return ansi.Truncate("  "+toneOf(toneWarn).mark()+" "+warnStyle.Render(text), width, truncTail())
}

func (m Model) boardComposer(width int, wide bool) string {
	visible := len(m.visibleSessionIndices())
	placeholder := "type a command" + hintEllipsis()
	if !wide {
		hint := strings.TrimSpace(chipRangeLabel(visible) + " board" + dotSep() + "tap a row")
		if visible == 0 {
			hint = "type a command"
		}
		placeholder = hint + hintEllipsis()
	}
	field := hintStyle.Render(placeholder)
	label := ""
	typed := m.input != ""
	if typed {
		field = titleStyle.Render(displaytext.Sanitize(m.input))
	}
	if m.filterActive {
		label = hintStyle.Render("filter / ")
		field = hintStyle.Render("type to filter" + hintEllipsis())
		if m.filterQuery != "" {
			field = titleStyle.Render(displaytext.Sanitize(m.filterQuery))
		}
	}
	composer := bar() + " " + label + brandStyle.Render(caretGlyph()) + " " + field + brandStyle.Render(cursorGlyph())
	// While the operator is typing, the composer owns the whole line; the key
	// hints return the moment it empties.
	if typed || !wide {
		return ansi.Truncate(composer, width, truncTail())
	}
	segments := []string{arrowsHint() + " " + enterHint(), "click a gate", "/ filter", "e new", "? help"}
	if chips := chipRangeLabel(visible); chips != "" {
		segments = append([]string{chips}, segments...)
	}
	if m.defaultAgent != "" {
		segments = append([]string{m.defaultAgent}, segments...)
	}
	if m.filterActive {
		segments = []string{arrowsHint(), enterHint() + " open", "Esc clears"}
	}
	// The composer owns the line; the hints fit in what it leaves, dropping
	// their least essential tail segments rather than eating the prompt.
	budget := width - ansi.StringWidth(composer) - 2
	hints := ""
	for len(segments) > 0 {
		joined := strings.Join(segments, dotSep())
		if ansi.StringWidth(joined) <= budget {
			hints = joined
			break
		}
		segments = segments[:len(segments)-1]
	}
	if hints == "" {
		return ansi.Truncate(composer, width, truncTail())
	}
	return joinDashboardEnds(composer, hintStyle.Render(hints), width)
}

func joinDashboardEnds(left, right string, width int) string {
	right = ansi.Truncate(right, width, truncTail())
	leftWidth := max(0, width-ansi.StringWidth(right)-1)
	left = ansi.Truncate(left, leftWidth, truncTail())
	gap := max(1, width-ansi.StringWidth(left)-ansi.StringWidth(right))
	return ansi.Truncate(left+strings.Repeat(" ", gap)+right, width, truncTail())
}

func entryLines(entries []dashboardEntry, width int) []string {
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		lines = append(lines, ansi.Truncate(entry.text, width, truncTail()))
	}
	return lines
}

// windowBlocks scrolls in lines but never severs a block: the start is snapped
// back to a block boundary, so a session is either fully on screen or absent.
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

// ─── hit-testing ──────────────────────────────────────────────────────────────

// dashboardHitSession maps a terminal row to the session rendered on it. It
// recomposes the body with the same inputs dashboardView used rather than
// caching a hit map, so the mapping cannot go stale between a frame and a click.
func (m Model) dashboardHitSession(row int) (int, bool) {
	index, _, ok := m.dashboardHitTarget(row, -1)
	return index, ok
}

// dashboardHitTarget resolves a pointer position to a session and reports
// whether it landed inside the row's GATE cell. The gate geometry comes from
// the same boardColumns call the renderer used, so a button can only be where
// a button is drawn. col < 0 means "row only" for callers without a column.
func (m Model) dashboardHitTarget(row, col int) (int, bool, bool) {
	w, h := max(1, m.width), max(0, m.height)
	if h == 0 || !m.sizeKnown {
		return 0, false, false
	}
	if m.helpOpen || m.confirmLatest || m.confirmStop || m.wizard || m.renaming {
		return 0, false, false
	}
	bottom := m.dashboardBottom(w, h)
	if len(bottom) >= h {
		return 0, false, false
	}
	bodyBudget := max(0, h-dashboardHeaderLines-len(bottom))
	entries := m.dashboardBodyEntries(w, bodyBudget)
	// fitScreen keeps the tail when a frame overflows, which would shift every
	// row upward. Refuse to guess in that case rather than select the wrong
	// session.
	if dashboardHeaderLines+len(entries)+len(bottom) > h {
		return 0, false, false
	}
	index := row - dashboardHeaderLines
	if index < 0 || index >= len(entries) {
		return 0, false, false
	}
	if entries[index].sessionIndex < 0 {
		return 0, false, false
	}
	lay := boardColumns(w)
	gate := lay.wide && col >= lay.gateX && col < lay.gateX+lay.gateW
	return entries[index].sessionIndex, gate, true
}
