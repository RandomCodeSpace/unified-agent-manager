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
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/version"
	"github.com/charmbracelet/x/ansi"
)

const (
	dashboardWideMin     = 96
	dashboardCompactMin  = 60
	dashboardMinWidth    = 40
	dashboardMinHeight   = 12
	dashboardHeaderLines = 2
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
)

// dashboardEntry is a compatibility projection used by component tests. The
// actual screen is clipped by the Bubbles viewport in buildDashboardFrame.
type dashboardEntry struct {
	text         string
	sessionIndex int
	blockStart   bool
}

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

type dashboardHelpMap struct {
	move, open, filter, stop, quit key.Binding
}

func (m dashboardHelpMap) ShortHelp() []key.Binding {
	return []key.Binding{m.move, m.open, m.filter, m.stop, m.quit}
}

func (m dashboardHelpMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{m.ShortHelp()}
}

func newDashboardHelpMap(retry, expanded bool, manageLabel string) dashboardHelpMap {
	if expanded && !retry {
		return dashboardHelpMap{
			move:   key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "new")),
			open:   key.NewBinding(key.WithKeys("ctrl+t"), key.WithHelp("ctrl+t", "pin")),
			filter: key.NewBinding(key.WithKeys("ctrl+r"), key.WithHelp("ctrl+r", "rename")),
			stop:   key.NewBinding(key.WithKeys("ctrl+s"), key.WithHelp("ctrl+s", "group")),
			quit:   key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "back")),
		}
	}
	manage := key.NewBinding(key.WithKeys("ctrl+x"), key.WithHelp("ctrl+x", manageLabel))
	if retry {
		manage = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "retry"))
	}
	return dashboardHelpMap{
		move:   key.NewBinding(key.WithKeys("up", "down"), key.WithHelp(arrowsHint(), "move")),
		open:   key.NewBinding(key.WithKeys("enter"), key.WithHelp(enterHint(), "open")),
		filter: key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		stop:   manage,
		quit:   key.NewBinding(key.WithKeys("esc", "ctrl+c"), key.WithHelp("esc", "quit")),
	}
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

func sessionKey(sess adapter.Session) sessionIdentity {
	return sessionIdentity{agent: sess.AgentType, id: sess.ID}
}

func dashboardSessionNodeID(identity sessionIdentity, action string) string {
	key := base64.RawURLEncoding.EncodeToString([]byte(identity.agent + "\x00" + identity.id))
	if action == "row" {
		return "session/" + key + "/row"
	}
	return "session/" + key + "/action/" + action
}

func sessionActionSignature(sess adapter.Session) string {
	return sess.AgentType + "\x00" + sess.ID + "\x00" + primaryActionLabel(sess)
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
	case "space":
		m.filterQuery += " "
		m.reconcileFilterSelection()
		return true, nil
	case "ctrl+t", "ctrl+r", "ctrl+x":
		if noMatches {
			return true, nil
		}
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
	if m.confirmLatest || m.confirmStop || m.wizard || m.renaming {
		return m.responsiveView()
	}
	return m.buildDashboardFrame().content
}

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
		message := fmt.Sprintf("Agents needs %dx%d; current terminal is %dx%d", dashboardMinWidth, dashboardMinHeight, w, h)
		frame.content = fitScreen([]string{dashboardBrandVersion(), "", warnStyle.Render(ansi.Truncate(message, w, truncTail()))}, w, h)
		frame.compositor = lipgloss.NewCompositor(lipgloss.NewLayer(frame.content))
		return frame
	}

	frame.rosterY = dashboardHeaderLines
	frame.rosterH = max(1, h-dashboardHeaderLines-3)
	contextRuleY := frame.rosterY + frame.rosterH
	contextY := contextRuleY + 1
	helpY := contextY + 1

	lines := make([]string, h)
	lines[0] = m.dashboardHeader(w)
	lines[1] = dividerStyle.Render(strings.Repeat(ruleGlyph(), w))
	lines[contextRuleY] = dividerStyle.Render(strings.Repeat(ruleGlyph(), w))
	lines[contextY] = m.dashboardContext(w)
	lines[helpY] = m.dashboardHelp(w)

	visible := m.visibleSessionIndices()
	start := dashboardViewportStart(visible, m.selected, frame.rosterH)
	end := min(len(visible), start+frame.rosterH)
	// Keep the viewport's full logical line space while rendering only its
	// visible window. Bubbles remains responsible for offset, clipping, and fill;
	// UAM avoids styling hundreds of lines the viewport will immediately discard.
	rowLines := make([]string, len(visible))
	for position := start; position < end; position++ {
		index := visible[position]
		rowLines[position] = m.dashboardRow(m.sessions[index], index, w)
	}
	if len(visible) == 0 {
		empty := "No agents yet"
		if !m.hasLoaded {
			empty = m.activityView() + " " + hintStyle.Render("Loading agents")
		} else if m.filterActive {
			empty = fmt.Sprintf("No agents match %q", displaytext.Sanitize(m.filterQuery))
		}
		rowLines = []string{"  " + hintStyle.Render(ansi.Truncate(empty, max(1, w-2), truncTail()))}
	}

	vp := viewport.New(viewport.WithWidth(w), viewport.WithHeight(frame.rosterH))
	vp.FillHeight = true
	vp.MouseWheelEnabled = false
	vp.SetContentLines(rowLines)
	vp.SetYOffset(start)
	rosterLines := strings.Split(vp.View(), "\n")
	for i := 0; i < frame.rosterH && i < len(rosterLines); i++ {
		lines[frame.rosterY+i] = rosterLines[i]
	}

	base := fitScreen(lines, w, h)
	layers := []*lipgloss.Layer{lipgloss.NewLayer(base).Z(0)}
	for position := start; position < end; position++ {
		index := visible[position]
		sess := m.sessions[index]
		identity := sessionKey(sess)
		y := frame.rosterY + position - start
		rowText, actionX, actionText := m.dashboardRowParts(sess, index, w)
		rowHitText := ansi.Truncate(rowText, actionX, "")
		rowID := dashboardSessionNodeID(identity, "row")
		actionID := dashboardSessionNodeID(identity, strings.ToLower(primaryActionLabel(sess)))
		layers = append(layers,
			lipgloss.NewLayer(rowHitText).ID(rowID).X(0).Y(y).Z(1),
			lipgloss.NewLayer(actionText).ID(actionID).X(actionX).Y(y).Z(2),
		)
		frame.nodes[rowID] = dashboardNode{action: dashboardSelect, identity: identity}
		frame.nodes[actionID] = dashboardNode{action: dashboardPrimary, identity: identity, signature: sessionActionSignature(sess)}
	}
	if m.refreshError != "" && !m.loading {
		retryText := dashboardRetryStyle().Render(" Retry ")
		retryX := max(0, w-ansi.StringWidth(retryText))
		retryID := "footer/retry"
		layers = append(layers, lipgloss.NewLayer(retryText).ID(retryID).X(retryX).Y(contextY).Z(3))
		frame.nodes[retryID] = dashboardNode{action: dashboardRetry}
	} else if sess, ok := m.selectedSession(); ok {
		stopText := dashboardStopStyle().Render(" " + secondaryActionLabel(sess) + " ")
		stopX := max(0, w-ansi.StringWidth(stopText))
		stopID := dashboardSessionNodeID(sessionKey(sess), strings.ToLower(secondaryActionLabel(sess)))
		layers = append(layers, lipgloss.NewLayer(stopText).ID(stopID).X(stopX).Y(contextY).Z(3))
		frame.nodes[stopID] = dashboardNode{action: dashboardStop, identity: sessionKey(sess), signature: sessionActionSignature(sess)}
	}
	frame.compositor = lipgloss.NewCompositor(layers...)
	frame.content = fitScreen(strings.Split(frame.compositor.Render(), "\n"), w, h)
	return frame
}

func dashboardViewportStart(visible []int, selected, height int) int {
	position := 0
	for i, index := range visible {
		if index == selected {
			position = i
			break
		}
	}
	start, _ := visibleWindow(len(visible), position, height)
	return start
}

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
	write(m.message)
	write(m.refreshError)
	for _, index := range m.visibleSessionIndices() {
		sess := m.sessions[index]
		write(sessionActionSignature(sess))
		write(firstNonEmpty(sess.DisplayName, sess.ID))
	}
	return h.Sum64()
}

func (m Model) dashboardHeader(width int) string {
	left := dashboardBrandVersion()
	visible, total := len(m.visibleSessionIndices()), len(m.sessions)
	clock := m.dashboardNow().Format("15:04 MST")
	count := fmt.Sprintf("%d/%d sessions", visible, total)
	if dashboardLayoutFor(width) == dashboardNarrow {
		count = fmt.Sprintf("%d/%d", visible, total)
	}
	base := clock + dotSep() + "Agents" + dotSep() + count
	right := base
	available := max(1, width-ansi.StringWidth(left)-1)
	if m.loading {
		right = m.activityView() + " Refreshing" + dotSep() + base
	} else if !m.hasLoaded && len(m.sessions) == 0 {
		right = m.activityView() + " Loading" + dotSep() + base
	}
	if ansi.StringWidth(right) > available && m.loading {
		// Narrow terminals keep the clock, surface identity, and activity mark;
		// the verbose progress word and count yield first.
		right = clock + dotSep() + "Agents" + dotSep() + m.activityView()
	}
	if m.filterActive {
		query := displaytext.Sanitize(m.filterQuery)
		if query == "" {
			query = "type to filter"
		}
		right = clock + dotSep() + "Agents" + dotSep() + "Filter: " + query + dotSep() + count
	}
	// UAM and the version are the fixed identity of the application. Status,
	// query, and counts share the remaining cells and truncate before branding.
	right = ansi.Truncate(right, available, truncTail())
	return joinDashboardEnds(left, hintStyle.Render(right), width)
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

func (m Model) dashboardRow(sess adapter.Session, index, width int) string {
	row, _, _ := m.dashboardRowParts(sess, index, width)
	return row
}

func (m Model) dashboardRowParts(sess adapter.Session, index, width int) (string, int, string) {
	action := dashboardActionStyle(index == m.selected).Render(" " + primaryActionLabel(sess) + " ")
	actionWidth := ansi.StringWidth(action)
	actionX := max(0, width-actionWidth)
	contentWidth := max(1, actionX-1)
	state := lifecycleBadge(sess)
	stateText := toneForSession(sess).mark() + " " + toneForSession(sess).render(state)
	name := displaytext.Sanitize(firstNonEmpty(sess.DisplayName, sess.ID, "unnamed"))
	provider := providerBadge(sess)
	selected := index == m.selected
	rail := " "
	nameRender := titleStyle.Render(name)
	if selected {
		rail = toneOf(toneSelected).mark()
		nameRender = selectedStyle.Render(name)
	}
	providerLabel := providerLabelStyle(selected).Render(provider)
	providerWidth := ansi.StringWidth(providerLabel)

	var content string
	switch dashboardLayoutFor(width) {
	case dashboardWide:
		stateCell := padRightANSI(stateText, 13)
		nameWidth := min(24, max(12, contentWidth/4))
		fixed := 26 + providerWidth + nameWidth
		taskWidth := max(1, contentWidth-fixed)
		content = rail + " " + providerLabel + " " + stateCell + "  " +
			padRightANSI(ansi.Truncate(nameRender, nameWidth, truncTail()), nameWidth) + "  " +
			padRightANSI(taskStyle.Render(boundedTaskSummary(sess, taskWidth)), taskWidth) + "  " +
			padLeftANSI(ageText(sess, m.dashboardNow()), 4)
	case dashboardCompact:
		prefix := rail + " " + providerLabel + " " + stateText + dotSep()
		nameWidth := max(1, contentWidth-ansi.StringWidth(prefix))
		content = prefix + ansi.Truncate(nameRender, nameWidth, truncTail())
	default:
		prefix := rail + " " + providerLabel + " " + stateText + " "
		nameWidth := max(1, contentWidth-ansi.StringWidth(prefix))
		content = prefix + ansi.Truncate(nameRender, nameWidth, truncTail())
	}
	content = padRightANSI(ansi.Truncate(content, contentWidth, truncTail()), contentWidth)
	return content + " " + action, actionX, action
}

func dashboardActionStyle(selected bool) lipgloss.Style {
	style := lipgloss.NewStyle().Bold(true).Foreground(actionTextColor).Background(accentColor)
	if !selected {
		style = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	}
	return style
}

func providerLabelStyle(selected bool) lipgloss.Style {
	background := dividerColor
	if selected {
		background = accentColor
	}
	return lipgloss.NewStyle().
		Bold(true).
		Foreground(actionTextColor).
		Background(background).
		Padding(0, 1)
}

func dashboardStopStyle() lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(failColor)
}

func dashboardRetryStyle() lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(actionTextColor).Background(accentColor)
}

func secondaryActionLabel(sess adapter.Session) string {
	if sessionIsRunning(sess) {
		return "Stop"
	}
	return "Remove"
}

func (m Model) dashboardContext(width int) string {
	if m.refreshError != "" && !m.loading {
		retry := dashboardRetryStyle().Render(" Retry ")
		budget := max(1, width-ansi.StringWidth(retry)-1)
		failure := "Refresh failed: " + displaytext.Sanitize(m.refreshError)
		return joinDashboardEnds(warnStyle.Render(ansi.Truncate(failure, budget, truncTail())), retry, width)
	}
	if m.message != "" {
		return ansi.Truncate(warnStyle.Render(displaytext.Sanitize(m.message)), width, truncTail())
	}
	sess, ok := m.selectedSession()
	if !ok {
		return hintStyle.Render("Select an agent session to open it")
	}
	stop := dashboardStopStyle().Render(" " + secondaryActionLabel(sess) + " ")
	budget := max(1, width-ansi.StringWidth(stop)-2)
	id := displaytext.Sanitize(sess.ID)
	provider := providerBadge(sess)
	updated := m.sessionUpdatedLabel(sess)
	detail := provider + dotSep() + updated
	switch dashboardLayoutFor(width) {
	case dashboardWide, dashboardCompact:
		for _, field := range []string{id, displaytext.Sanitize(absCwd(sess.Cwd))} {
			candidate := detail + dotSep() + field
			if ansi.StringWidth(candidate) > budget {
				break
			}
			detail = candidate
		}
	}
	return joinDashboardEnds(hintStyle.Render(ansi.Truncate(detail, budget, truncTail())), stop, width)
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

func (m Model) dashboardBody(width, budget int) []string {
	return entryLines(m.dashboardBodyEntries(width, budget), width)
}

func (m Model) dashboardBodyEntries(width, budget int) []dashboardEntry {
	if width <= 0 || budget <= 0 {
		return nil
	}
	visible := m.visibleSessionIndices()
	rows := make([]dashboardEntry, 0, len(visible))
	for _, index := range visible {
		rows = append(rows, dashboardEntry{text: m.dashboardRow(m.sessions[index], index, width), sessionIndex: index, blockStart: true})
	}
	if len(rows) == 0 {
		return []dashboardEntry{{text: hintStyle.Render("No agents yet"), sessionIndex: -1}}
	}
	return windowBlocks(rows, m.selected, budget)
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
			delta = -1
		case tea.MouseWheelDown:
			delta = 1
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
	if intent.action == dashboardMove {
		m.moveSelection(intent.delta)
		return m, nil
	}
	if intent.action == dashboardRetry {
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

func entryLines(entries []dashboardEntry, width int) []string {
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		lines = append(lines, ansi.Truncate(entry.text, width, truncTail()))
	}
	return lines
}

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
	return entries[start : start+budget]
}
