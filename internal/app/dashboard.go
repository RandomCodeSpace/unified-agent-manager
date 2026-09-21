package app

import (
	"encoding/base64"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/version"
	"github.com/charmbracelet/x/ansi"
)

// ─── the deck ────────────────────────────────────────────────────────────────
//
// The dashboard is a launcher and a switchboard, not a table. Top to bottom:
//
//	header      UAM, version, page range and fleet counts, clock
//	launch pad  the provider and directory a new session starts with; `n`
//	            expands it in place into an inline form
//	rule
//	deck        sessions as two-line stamps in 1..N columns; the first nine on
//	            a page carry a door digit that attaches directly
//	rule
//	ledger      the selected session in full, plus its action words
//	help        one Bubbles help line
//
// Every datum on screen comes from the store record or the live process
// check. Nothing here reads provider output, so nothing moves on a refresh
// tick except the clock and real lifecycle changes.

const (
	dashboardWideMin    = 96
	dashboardCompactMin = 60
	dashboardMinWidth   = 40
	dashboardMinHeight  = 12
)

type dashboardLayout uint8

const (
	dashboardNarrow dashboardLayout = iota
	dashboardCompact
	dashboardWide
)

type dashboardAction string

const (
	dashboardSelect  dashboardAction = "select"
	dashboardPrimary dashboardAction = "primary"
	dashboardStop    dashboardAction = "stop"
	dashboardRetry   dashboardAction = "retry"
	dashboardMove    dashboardAction = "move"
	dashboardLaunch  dashboardAction = "launch"
)

type dashboardNode struct {
	action    dashboardAction
	identity  sessionIdentity
	signature string
}

type dashboardFrame struct {
	content    string
	compositor *lipgloss.Compositor
	nodes      map[string]dashboardNode
	signature  uint64
	width      int
	height     int
	rosterY    int
	rosterH    int
	geometry   deckGeometry
}

type dashboardPointerIntent struct {
	frameSignature  uint64
	width           int
	height          int
	action          dashboardAction
	identity        sessionIdentity
	actionSignature string
	delta           int
}

// dashboardPlan is the vertical budget of one frame. Everything but the deck
// has a fixed height, so the deck gets whatever remains.
type dashboardPlan struct {
	padY, padH       int
	ruleY            int
	rosterY, rosterH int
	ruleY2           int
	ledgerY, ledgerH int
	helpY            int
}

func (m Model) dashboardPlan(width, height int) dashboardPlan {
	p := dashboardPlan{padY: 1, padH: m.launchPadHeight()}
	p.ruleY = p.padY + p.padH
	p.rosterY = p.ruleY + 1
	p.helpY = height - 1
	// The ledger yields rows before the deck does: one stamp must always fit.
	p.ledgerH = max(1, min(ledgerHeight(width), height-p.rosterY-2-stampHeight))
	p.ledgerY = p.helpY - p.ledgerH
	p.ruleY2 = p.ledgerY - 1
	p.rosterH = max(stampHeight, p.ruleY2-p.rosterY)
	return p
}

func ledgerHeight(width int) int {
	switch dashboardLayoutFor(width) {
	case dashboardWide:
		return 2
	case dashboardCompact:
		return 3
	default:
		return 1
	}
}

func (m Model) deckGeometry() deckGeometry {
	plan := m.dashboardPlan(max(1, m.width), max(dashboardMinHeight, m.height))
	return deckGeometryFor(max(1, m.width), plan.rosterH)
}

type dashboardHelpMap []key.Binding

func (m dashboardHelpMap) ShortHelp() []key.Binding  { return m }
func (m dashboardHelpMap) FullHelp() [][]key.Binding { return [][]key.Binding{m} }

func newDashboardHelpMap(retry, expanded bool, manageLabel string) dashboardHelpMap {
	bind := func(keys, label string) key.Binding {
		return key.NewBinding(key.WithKeys(keys), key.WithHelp(keys, label))
	}
	if expanded && !retry {
		return dashboardHelpMap{
			bind(enterHint(), "open"),
			bind("space", "background"),
			bind("ctrl+x", manageLabel),
			bind("ctrl+r", "rename"),
			bind("ctrl+t", "pin"),
			bind("ctrl+s", "group"),
			bind("shift+"+arrowsHint(), "reorder"),
			bind("?", "back"),
		}
	}
	keys := dashboardHelpMap{}
	if retry {
		keys = append(keys, bind("r", "retry"))
	}
	return append(keys,
		bind(arrowsAllHint(), "move"),
		bind("1-9", "warp"),
		bind("0", "back"),
		bind("n", "new"),
		bind("/", "filter"),
		bind("?", "help"),
		bind("esc", "quit"),
	)
}

func dashboardLayoutFor(width int) dashboardLayout {
	switch {
	case width >= dashboardWideMin:
		return dashboardWide
	case width >= dashboardCompactMin:
		return dashboardCompact
	default:
		return dashboardNarrow
	}
}

func (m Model) dashboardNow() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

func lifecycleBadge(sess adapter.Session) string {
	switch {
	case sess.ProcAlive == adapter.Alive:
		return "Running"
	case failureExitDetail(sess) != "":
		return "Failed"
	default:
		return "Stopped"
	}
}

func providerBadge(sess adapter.Session) string {
	return displaytext.Sanitize(firstNonEmpty(sess.AgentType, "?"))
}

func primaryActionLabel(sess adapter.Session) string {
	if sess.ProcAlive == adapter.Alive {
		return "Attach"
	}
	return "Resume"
}

func secondaryActionLabel(sess adapter.Session) string {
	if sessionIsRunning(sess) {
		return "Stop"
	}
	return "Remove"
}

func sessionKey(sess adapter.Session) sessionIdentity {
	return sessionIdentity{agent: sess.AgentType, id: sess.ID}
}

// dashboardSessionNodeID names a compositor layer. "row" is the stamp body
// (select), "door" is the digit cell (primary), anything else is an action
// word in the ledger.
func dashboardSessionNodeID(identity sessionIdentity, action string) string {
	key := base64.RawURLEncoding.EncodeToString([]byte(identity.agent + "\x00" + identity.id))
	switch action {
	case "row", "door":
		return "session/" + key + "/" + action
	default:
		return "session/" + key + "/action/" + action
	}
}

func sessionActionSignature(sess adapter.Session) string {
	return sess.AgentType + "\x00" + sess.ID + "\x00" + primaryActionLabel(sess)
}

// ─── filter ──────────────────────────────────────────────────────────────────

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

// positionOf locates a canonical index inside the visible list, defaulting
// to the first position when the selection is hidden.
func positionOf(visible []int, index int) int {
	for i, candidate := range visible {
		if candidate == index {
			return i
		}
	}
	return 0
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

func (m *Model) handleFilterKey(msg tea.KeyPressMsg, pressed string) (bool, tea.Cmd) {
	noMatches := len(m.visibleSessionIndices()) == 0
	switch pressed {
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
	case "shift+up":
		return true, m.moveSession(-m.deckGeometry().cols)
	case "shift+down":
		return true, m.moveSession(m.deckGeometry().cols)
	case "shift+left":
		return true, m.moveSession(-1)
	case "shift+right":
		return true, m.moveSession(1)
	case "enter":
		if noMatches {
			return true, nil
		}
		return true, m.handleEnterKey()
	case "space":
		m.filterQuery += " "
		m.reconcileFilterSelection()
		return true, nil
	case "ctrl+t", "ctrl+r", "ctrl+x":
		if noMatches {
			return true, nil
		}
	}
	if handled, cmd := m.handleMovementKey(pressed); handled {
		return true, cmd
	}
	if msg.Text != "" && !msg.Mod.Contains(tea.ModAlt) {
		m.filterQuery += msg.Text
		m.reconcileFilterSelection()
		return true, nil
	}
	return false, nil
}

func (m *Model) moveFilteredSession(delta int) tea.Cmd {
	visible := m.visibleSessionIndices()
	position := positionOf(visible, m.selected)
	if len(visible) == 0 || position+delta < 0 || position+delta >= len(visible) {
		return nil
	}
	return m.moveSessionTo(visible[position+delta])
}

// ─── frame ───────────────────────────────────────────────────────────────────

func (m Model) buildDashboardFrame() dashboardFrame {
	w, h := max(1, m.width), max(0, m.height)
	frame := dashboardFrame{
		width: w, height: h, signature: m.dashboardSignature(),
		nodes: make(map[string]dashboardNode),
	}
	if h == 0 {
		return frame
	}
	if w < dashboardMinWidth || h < dashboardMinHeight {
		message := fmt.Sprintf("UAM needs %dx%d; current terminal is %dx%d", dashboardMinWidth, dashboardMinHeight, w, h)
		frame.content = fitScreen([]string{dashboardBrandVersion(), "", warnStyle.Render(ansi.Truncate(message, w, truncTail()))}, w, h)
		frame.compositor = lipgloss.NewCompositor(lipgloss.NewLayer(frame.content))
		return frame
	}

	plan := m.dashboardPlan(w, h)
	frame.rosterY, frame.rosterH = plan.rosterY, plan.rosterH
	frame.geometry = deckGeometryFor(w, plan.rosterH)
	visible := m.visibleSessionIndices()
	start, end := frame.geometry.pageBounds(len(visible), positionOf(visible, m.selected))

	lines := make([]string, h)
	lines[0] = m.dashboardHeader(w, start, end, len(visible))
	copy(lines[plan.padY:], m.launchPadLines(w))
	rule := dividerStyle.Render(strings.Repeat(ruleGlyph(), w))
	lines[plan.ruleY] = rule
	lines[plan.ruleY2] = rule
	ledger, verbs := m.ledgerLines(w, plan.ledgerH)
	copy(lines[plan.ledgerY:], ledger)
	lines[plan.helpY] = m.dashboardHelp(w)

	stamps := m.deckLines(frame.geometry, visible, start, end, plan.rosterH)
	copy(lines[plan.rosterY:], stamps)

	base := fitScreen(lines, w, h)
	layers := []*lipgloss.Layer{lipgloss.NewLayer(base).Z(0)}
	for position := start; position < end; position++ {
		sess := m.sessions[visible[position]]
		identity := sessionKey(sess)
		slot := position - start
		x, y := frame.geometry.slotOrigin(slot)
		y += plan.rosterY
		line1, line2 := stamps[y-plan.rosterY], stamps[y-plan.rosterY+1]
		// The body excludes the rail and door cells so the door layer owns its
		// cell alone and the hit regions stay disjoint.
		body := ansi.Cut(line1, x+2, x+frame.geometry.cell) + "\n" + ansi.Cut(line2, x+2, x+frame.geometry.cell)
		rowID := dashboardSessionNodeID(identity, "row")
		layers = append(layers, lipgloss.NewLayer(body).ID(rowID).X(x+2).Y(y).Z(1))
		frame.nodes[rowID] = dashboardNode{action: dashboardSelect, identity: identity}
		if slot < deckDoors {
			doorID := dashboardSessionNodeID(identity, "door")
			layers = append(layers, lipgloss.NewLayer(ansi.Cut(line1, x+1, x+2)).ID(doorID).X(x+1).Y(y).Z(2))
			frame.nodes[doorID] = dashboardNode{action: dashboardPrimary, identity: identity, signature: sessionActionSignature(sess)}
		}
	}
	for _, verb := range verbs {
		layers = append(layers, lipgloss.NewLayer(verb.text).ID(verb.id).X(verb.x).Y(plan.ledgerY+verb.line).Z(3))
		frame.nodes[verb.id] = verb.node
	}
	if !m.launchOpen {
		padID := "launch/pad"
		layers = append(layers, lipgloss.NewLayer(lines[plan.padY]).ID(padID).X(0).Y(plan.padY).Z(1))
		frame.nodes[padID] = dashboardNode{action: dashboardLaunch}
	}
	frame.compositor = lipgloss.NewCompositor(layers...)
	frame.content = fitScreen(strings.Split(frame.compositor.Render(), "\n"), w, h)
	return frame
}

// deckLines renders the visible page of stamps into rosterH lines. Empty
// slots and the gap rows stay blank; the empty state replaces the grid.
func (m Model) deckLines(g deckGeometry, visible []int, start, end, rosterH int) []string {
	lines := make([]string, rosterH)
	if len(visible) == 0 {
		copy(lines, m.deckEmptyLines(g.cell*g.cols+g.cols-1, rosterH))
		return lines
	}
	blank := strings.Repeat(" ", g.cell)
	for row := 0; row < g.rows; row++ {
		y := row * (stampHeight + g.gap)
		if y+1 >= rosterH {
			break
		}
		first := make([]string, 0, g.cols)
		second := make([]string, 0, g.cols)
		for col := 0; col < g.cols; col++ {
			position := start + row*g.cols + col
			if position >= end {
				first, second = append(first, blank), append(second, blank)
				continue
			}
			index := visible[position]
			door := 0
			if slot := position - start; slot < deckDoors {
				door = slot + 1
			}
			l1, l2 := m.stampLines(stamp{sess: m.sessions[index], door: door, selected: index == m.selected}, g.cell)
			first, second = append(first, l1), append(second, l2)
		}
		lines[y] = strings.Join(first, " ")
		lines[y+1] = strings.Join(second, " ")
	}
	return lines
}

func (m Model) deckEmptyLines(width, budget int) []string {
	var text []string
	switch {
	case !m.hasLoaded:
		text = []string{"", "   " + m.activityView() + " " + hintStyle.Render("Loading sessions")}
	case m.filterActive:
		text = []string{"", "   " + hintStyle.Render(fmt.Sprintf("No sessions match %q", displaytext.Sanitize(m.filterQuery)))}
	default:
		provider := displaytext.Sanitize(m.launchProviderDefault(""))
		dir := m.displayPath(firstNonEmpty(m.launchCwd, "."))
		text = []string{
			"",
			"   " + titleStyle.Render("No sessions yet.") + " " + hintStyle.Render("The deck fills as you launch."),
			"",
			"   " + brandStyle.Render("n") + "         " + hintStyle.Render("launch here: "+provider+" in "+dir),
			"   " + brandStyle.Render("tab") + "       " + hintStyle.Render("cycle the default provider"),
			"   " + brandStyle.Render("uam new") + "   " + hintStyle.Render("launch from a shell; the session appears on this deck"),
			"",
			"   " + hintStyle.Render("Sessions are numbered doors: press the digit to enter,"),
			"   " + hintStyle.Render("ctrl+left to come back here. 0 returns to the last one you left."),
		}
	}
	lines := make([]string, 0, len(text))
	for _, line := range takeLines(text, budget) {
		lines = append(lines, ansi.Truncate(line, width, truncTail()))
	}
	return lines
}

// ─── header ──────────────────────────────────────────────────────────────────

func (m Model) dashboardHeader(width, start, end, total int) string {
	left := dashboardBrandVersion()
	clock := m.dashboardNow().Format("15:04 MST")
	summary := m.headerSummary(width, start, end, total)
	if dashboardLayoutFor(width) == dashboardNarrow {
		// UAM and the version are the fixed identity; the summary yields
		// before them and the clock is the last thing to go.
		room := max(1, width-ansi.StringWidth(left)-1)
		right := clock
		if summary != "" && ansi.StringWidth(summary+dotSep()+clock) <= room {
			right = summary + dotSep() + clock
		}
		return joinDashboardEnds(left, hintStyle.Render(right), width)
	}
	return joinDashboardThree(left, hintStyle.Render(summary), hintStyle.Render(clock), width)
}

// headerSummary is the page range plus fleet counts. Counts come from
// process liveness and exit codes, never from output.
func (m Model) headerSummary(width, start, end, total int) string {
	narrow := dashboardLayoutFor(width) == dashboardNarrow
	var parts []string
	switch {
	case m.loading:
		parts = append(parts, m.activityView()+" Refreshing")
	case !m.hasLoaded && len(m.sessions) == 0:
		parts = append(parts, m.activityView()+" Loading")
	}
	if m.filterActive {
		query := displaytext.Sanitize(m.filterQuery)
		if query == "" {
			query = "type to filter"
		}
		parts = append(parts, "filter: "+query)
	}
	switch {
	case total == 0:
		parts = append(parts, "0 sessions")
	case dashboardLayoutFor(width) == dashboardWide:
		parts = append(parts, fmt.Sprintf("sessions %d-%d of %d", start+1, end, total))
	default:
		parts = append(parts, fmt.Sprintf("%d-%d of %d", start+1, end, total))
	}
	if narrow {
		return strings.Join(parts, dotSep())
	}
	running, failed := 0, 0
	for _, sess := range m.sessions {
		switch {
		case sess.ProcAlive == adapter.Alive:
			running++
		case failureExitDetail(sess) != "":
			failed++
		}
	}
	if running > 0 {
		parts = append(parts, fmt.Sprintf("%d running", running))
	}
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", failed))
	}
	return strings.Join(parts, dotSep())
}

func dashboardBrandVersion() string {
	badge := lipgloss.NewStyle().
		Bold(true).
		Foreground(actionTextColor).
		Background(accentColor).
		Padding(0, 1).
		Render("UAM")
	return badge + " " + hintStyle.Render(version.String())
}

// ─── ledger ──────────────────────────────────────────────────────────────────

type ledgerVerb struct {
	id   string
	text string
	x    int
	line int
	node dashboardNode
}

// ledgerLines renders exactly height lines describing the selected session,
// or the transient state that outranks it (rename, refresh failure, message).
// It also returns the clickable action words with their positions.
func (m Model) ledgerLines(width, height int) ([]string, []ledgerVerb) {
	lines := make([]string, height)
	sess, ok := m.selectedSession()
	var verbs []ledgerVerb
	actionLine := height - 1
	switch {
	case m.renaming:
		field := titleStyle.Render(displaytext.Sanitize(m.input)) + brandStyle.Render(cursorGlyph())
		lines[0] = joinDashboardEnds(bar()+" "+hintStyle.Render("rename")+"  "+field, hintStyle.Render(enterHint()+" save"+dotSep()+"esc cancel"), width)
		return lines, nil
	case m.refreshError != "" && !m.loading:
		retry := dashboardRetryStyle().Render(" Retry ")
		retryX := max(0, width-ansi.StringWidth(retry))
		failure := "Refresh failed: " + displaytext.Sanitize(m.refreshError)
		lines[0] = joinDashboardEnds(warnStyle.Render(ansi.Truncate(failure, max(1, retryX-1), truncTail())), retry, width)
		verbs = append(verbs, ledgerVerb{id: "footer/retry", text: retry, x: retryX, line: 0, node: dashboardNode{action: dashboardRetry}})
		if height == 1 {
			return lines, verbs
		}
	case m.message != "":
		lines[0] = ansi.Truncate(warnStyle.Render(displaytext.Sanitize(m.message)), width, truncTail())
		if height == 1 {
			return lines, verbs
		}
	case !ok:
		if len(m.sessions) > 0 || m.filterActive {
			lines[0] = hintStyle.Render("  nothing selected")
		}
		return lines, nil
	default:
		if height > 1 {
			var rest []string
			lines[0], rest = m.ledgerIdentityLine(sess, width, dashboardLayoutFor(width) == dashboardWide)
			if height > 2 {
				lines[1], _ = ledgerFit("  "+hintStyle.Render(m.displayPath(sess.Cwd)), rest, width)
			}
		}
	}
	if !ok {
		return lines, verbs
	}
	line, actionVerbs := m.ledgerActionLine(sess, width, height == 1)
	lines[actionLine] = line
	for i := range actionVerbs {
		actionVerbs[i].line = actionLine
	}
	return lines, append(verbs, actionVerbs...)
}

// ledgerIdentityLine renders the selected session's core facts (name,
// provider, status, created) and appends optional fields only while they fit
// whole. It returns the fields that did not fit so a taller ledger can carry
// them on its next line.
func (m Model) ledgerIdentityLine(sess adapter.Session, width int, withPath bool) (string, []string) {
	now := m.dashboardNow()
	t := toneForSession(sess)
	pin := ""
	if sess.Pinned {
		pin = toneOf(tonePinned).mark() + " "
	}
	core := []string{
		pin + selectedStyle.Render(displayNameOf(sess)),
		providerBadge(sess),
		t.mark() + " " + t.render(stampStatus(sess)),
		"created " + createdDetail(sess.CreatedAt, now),
	}
	var optional []string
	if sess.ProcAlive != adapter.Alive {
		optional = append(optional, m.sessionUpdatedLabel(sess))
	}
	if sess.PR != nil && sess.PR.Number > 0 {
		optional = append(optional, fmt.Sprintf("PR #%d", sess.PR.Number))
	}
	if profile := m.sessionProfileLabel(sess); profile != "" {
		optional = append(optional, "profile "+profile)
	}
	if withPath {
		optional = append(optional, m.displayPath(sess.Cwd), "id "+displaytext.Sanitize(sess.ID))
	}
	return ledgerFit(bar()+" "+strings.Join(core, hintStyle.Render(dotSep())), optional, width)
}

// ledgerFit appends fields to line while each still fits within width and
// returns the line plus the fields it had to leave out.
func ledgerFit(line string, fields []string, width int) (string, []string) {
	for i, field := range fields {
		candidate := line + hintStyle.Render(dotSep()+field)
		if ansi.StringWidth(candidate) > width {
			return ansi.Truncate(line, width, truncTail()), fields[i:]
		}
		line = candidate
	}
	return ansi.Truncate(line, width, truncTail()), nil
}

// sessionProfileLabel names the profile a session was launched with, or the
// default that applies when it recorded none.
func (m Model) sessionProfileLabel(sess adapter.Session) string {
	if selected := m.profileBySession[sessionKey(sess)]; selected != "" {
		return displaytext.Sanitize(selected)
	}
	if m.defaultProfile != "" {
		return "default:" + displaytext.Sanitize(m.defaultProfile)
	}
	return ""
}

// createdDetail is the ledger's fuller creation stamp.
func createdDetail(created, now time.Time) string {
	if created.IsZero() {
		return "unknown"
	}
	local := created.In(now.Location())
	y, mo, d := local.Date()
	ny, nmo, nd := now.Date()
	switch {
	case y == ny && mo == nmo && d == nd:
		return "today " + local.Format("15:04")
	case y == ny:
		return local.Format("02 Jan 15:04")
	default:
		return local.Format("02 Jan 2006")
	}
}

// ledgerActionLine spells what each key does to the selected session. The
// primary and the stop/remove words are clickable.
func (m Model) ledgerActionLine(sess adapter.Session, width int, railed bool) (string, []ledgerVerb) {
	identity := sessionKey(sess)
	prefix := "  "
	if railed {
		prefix = bar() + " "
	}
	type segment struct {
		text   string
		action dashboardAction
		id     string
	}
	primary := primaryActionLabel(sess)
	secondary := secondaryActionLabel(sess)
	segments := []segment{
		{text: hintStyle.Render(enterHint() + " ")},
		{text: brandStyle.Render(primary), action: dashboardPrimary, id: dashboardSessionNodeID(identity, strings.ToLower(primary))},
	}
	if sess.ProcAlive != adapter.Alive {
		segments = append(segments, segment{text: hintStyle.Render(dotSep() + "space background")})
	}
	segments = append(segments,
		segment{text: hintStyle.Render(dotSep() + "ctrl+x ")},
		segment{text: dashboardStopStyle().Render(secondary), action: dashboardStop, id: dashboardSessionNodeID(identity, strings.ToLower(secondary))},
		segment{text: hintStyle.Render(dotSep() + "ctrl+r Rename")},
		segment{text: hintStyle.Render(dotSep() + "ctrl+t Pin")},
		segment{text: hintStyle.Render(dotSep() + "shift+" + arrowsHint() + " reorder")},
	)
	var b strings.Builder
	b.WriteString(prefix)
	x := ansi.StringWidth(prefix)
	var verbs []ledgerVerb
	for _, seg := range segments {
		segWidth := ansi.StringWidth(seg.text)
		if x+segWidth > width {
			break
		}
		if seg.action != "" {
			verbs = append(verbs, ledgerVerb{id: seg.id, text: seg.text, x: x, node: dashboardNode{action: seg.action, identity: identity, signature: sessionActionSignature(sess)}})
		}
		b.WriteString(seg.text)
		x += segWidth
	}
	return ansi.Truncate(b.String(), width, truncTail()), verbs
}

func dashboardStopStyle() lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(failColor)
}

func dashboardRetryStyle() lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(actionTextColor).Background(accentColor)
}

func (m Model) sessionUpdatedLabel(sess adapter.Session) string {
	stamp := m.lastSeenBySession[sessionKey(sess)]
	if stamp.IsZero() {
		return "Updated unknown"
	}
	local := stamp.In(m.dashboardNow().Location())
	return "Updated " + local.Format("15:04 MST")
}

func (m Model) dashboardHelp(width int) string {
	h := help.New()
	h.SetWidth(width)
	h.ShortSeparator = dotSep()
	h.Ellipsis = truncTail()
	h.Styles.ShortKey = brandStyle
	h.Styles.ShortDesc = hintStyle
	h.Styles.ShortSeparator = hintStyle
	h.Styles.Ellipsis = hintStyle
	if m.launchOpen {
		return ansi.Truncate(h.View(launchHelpMap()), width, truncTail())
	}
	manageLabel := "manage"
	if sess, ok := m.selectedSession(); ok {
		manageLabel = strings.ToLower(secondaryActionLabel(sess))
	}
	return ansi.Truncate(h.View(newDashboardHelpMap(m.refreshError != "" && !m.loading, m.helpOpen, manageLabel)), width, truncTail())
}

func (m Model) activityView() string {
	if len(m.activity.Spinner.Frames) == 0 {
		fallback := spinner.New(spinner.WithSpinner(spinner.Line), spinner.WithStyle(brandStyle))
		return fallback.View()
	}
	return m.activity.View()
}

// ─── signature and pointer ───────────────────────────────────────────────────

// dashboardSignature hashes the visible state a pointer intent depends on.
// Bubble Tea keeps the displayed view's OnMouse callback until the content
// changes, so the hash must cover exactly what moves hit boxes and nothing
// that a no-op refresh touches.
func (m Model) dashboardSignature() uint64 {
	h := fnv.New64a()
	write := func(value string) {
		_, _ = h.Write([]byte(value))
		_, _ = h.Write([]byte{0})
	}
	write(strconv.FormatUint(m.dashboardRevision, 10))
	write(strconv.Itoa(m.width))
	write(strconv.Itoa(m.height))
	write(strconv.Itoa(m.selected))
	write(strconv.FormatBool(m.filterActive))
	write(m.filterQuery)
	write(strconv.FormatBool(m.loading))
	write(strconv.FormatBool(m.hasLoaded))
	write(strconv.FormatBool(m.launchOpen))
	write(strconv.FormatBool(m.renaming))
	write(m.message)
	write(m.refreshError)
	for _, index := range m.visibleSessionIndices() {
		sess := m.sessions[index]
		write(sessionActionSignature(sess))
		write(firstNonEmpty(sess.DisplayName, sess.ID))
	}
	return h.Sum64()
}

func (m Model) dashboardMouseCommand(frame dashboardFrame, msg tea.MouseMsg) tea.Cmd {
	mouse := msg.Mouse()
	switch typed := msg.(type) {
	case tea.MouseWheelMsg:
		if mouse.Y < frame.rosterY || mouse.Y >= frame.rosterY+frame.rosterH {
			return nil
		}
		delta := 0
		switch typed.Button {
		case tea.MouseWheelUp:
			delta = -max(1, frame.geometry.cols)
		case tea.MouseWheelDown:
			delta = max(1, frame.geometry.cols)
		default:
			return nil
		}
		return func() tea.Msg {
			return dashboardPointerIntent{frameSignature: frame.signature, width: frame.width, height: frame.height, action: dashboardMove, delta: delta}
		}
	case tea.MouseClickMsg:
		if typed.Button != tea.MouseLeft || frame.compositor == nil {
			return nil
		}
	default:
		return nil
	}
	hit := frame.compositor.Hit(mouse.X, mouse.Y)
	if hit.Empty() {
		return nil
	}
	node, ok := frame.nodes[hit.ID()]
	if !ok {
		return nil
	}
	return func() tea.Msg {
		return dashboardPointerIntent{
			frameSignature:  frame.signature,
			width:           frame.width,
			height:          frame.height,
			action:          node.action,
			identity:        node.identity,
			actionSignature: node.signature,
		}
	}
}

func (m Model) handleDashboardPointer(intent dashboardPointerIntent) (tea.Model, tea.Cmd) {
	if intent.frameSignature != m.dashboardSignature() || intent.width != m.width || intent.height != m.height {
		m.setMessage("View changed; select again.")
		return m, nil
	}
	switch intent.action {
	case dashboardMove:
		m.moveSelection(intent.delta)
		return m, nil
	case dashboardLaunch:
		m.openLaunchPad()
		return m, nil
	case dashboardRetry:
		if m.refreshError == "" || m.loading {
			m.setMessage("View changed; select again.")
			return m, nil
		}
		return m, m.retryRefresh()
	}
	index := -1
	for i, sess := range m.sessions {
		if sessionKey(sess) == intent.identity {
			index = i
			break
		}
	}
	if index < 0 {
		m.setMessage("View changed; select again.")
		return m, nil
	}
	sess := m.sessions[index]
	if intent.action != dashboardSelect && intent.actionSignature != sessionActionSignature(sess) {
		m.setMessage("View changed; select again.")
		return m, nil
	}
	m.selected = index
	switch intent.action {
	case dashboardSelect:
		return m, nil
	case dashboardPrimary:
		return m, m.attachSelectedCmd()
	case dashboardStop:
		m.openSessionConfirmation(sess)
		return m, nil
	default:
		return m, nil
	}
}

func joinDashboardEnds(left, right string, width int) string {
	right = ansi.Truncate(right, width, truncTail())
	leftWidth := max(0, width-ansi.StringWidth(right)-1)
	left = ansi.Truncate(left, leftWidth, truncTail())
	gap := max(1, width-ansi.StringWidth(left)-ansi.StringWidth(right))
	return ansi.Truncate(left+strings.Repeat(" ", gap)+right, width, truncTail())
}

// joinDashboardThree pins left and right to the edges and centres mid in the
// remaining cells, truncating mid first when the line is short.
func joinDashboardThree(left, mid, right string, width int) string {
	right = ansi.Truncate(right, width, truncTail())
	left = ansi.Truncate(left, max(0, width-ansi.StringWidth(right)-1), truncTail())
	room := width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if room < 3 {
		return joinDashboardEnds(left, right, width)
	}
	mid = ansi.Truncate(mid, room-2, truncTail())
	midWidth := ansi.StringWidth(mid)
	leftGap := max(1, (room-midWidth)/2)
	rightGap := max(1, room-midWidth-leftGap)
	return ansi.Truncate(left+strings.Repeat(" ", leftGap)+mid+strings.Repeat(" ", rightGap)+right, width, truncTail())
}
