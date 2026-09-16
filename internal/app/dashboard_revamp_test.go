package app

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/version"
	"github.com/charmbracelet/x/ansi"
)

func dashboardFixture(width, height int) Model {
	now := time.Date(2026, time.August, 28, 12, 4, 0, 0, time.UTC)
	m := NewWithDeps(nil, nil)
	m.now = func() time.Time { return now }
	m.sessions = []adapter.Session{
		{ID: "shared", AgentType: "codex", DisplayName: "release-check", Prompt: "verify release pipeline", Cwd: "/work/uam", ProcAlive: adapter.Alive, CreatedAt: now.Add(-4 * time.Minute)},
		{ID: "shared", AgentType: "claude", DisplayName: "repair-tests", Prompt: "repair integration tests", Cwd: "/work/uam", ProcAlive: adapter.Exited, CreatedAt: now.Add(-2 * time.Hour)},
		{ID: "failed", AgentType: "opencode", DisplayName: "failing-agent", Prompt: "inspect failure", Cwd: "/work/other", ProcAlive: adapter.Exited, ExitCode: exitCode(7), CreatedAt: now.Add(-3 * time.Hour)},
	}
	m.loading = false
	m.hasLoaded = true
	return m.handleWindowSize(tea.WindowSizeMsg{Width: width, Height: height})
}

func TestDashboardHeaderKeepsSingleCellWordmarkVersionAndTime(t *testing.T) {
	previousVersion := version.Override
	version.Override = "v9.9.9"
	t.Cleanup(func() { version.Override = previousVersion })

	for _, width := range []int{40, 60, 96, 120} {
		m := dashboardFixture(width, 12)
		header := strings.SplitN(ansi.Strip(m.View().Content), "\n", 2)[0]
		for _, want := range []string{"UAM", "v9.9.9", "12:04 UTC", "Agents"} {
			if !strings.Contains(header, want) {
				t.Fatalf("width %d header missing %q: %q", width, want, header)
			}
		}
		if strings.Contains(header, "🤖") {
			t.Fatalf("width %d header retained an ambiguous-width emoji wordmark: %q", width, header)
		}
		if got := ansi.StringWidth(header); got != width {
			t.Fatalf("width %d header occupies %d cells: %q", width, got, header)
		}
	}
}

func TestDashboardShowsProviderAndObservedUpdateTimeAtEveryWidth(t *testing.T) {
	updated := time.Date(2026, time.August, 28, 11, 57, 0, 0, time.UTC)
	for _, width := range []int{40, 60, 96, 120} {
		m := dashboardFixture(width, 20)
		m.lastSeenBySession = map[sessionIdentity]time.Time{
			{agent: "codex", id: "shared"}: updated,
		}
		view := ansi.Strip(m.View().Content)
		for _, provider := range []string{"codex", "claude", "opencode"} {
			if !strings.Contains(view, provider) {
				t.Fatalf("width %d dashboard missing provider %q:\n%s", width, provider, view)
			}
		}
		if !strings.Contains(view, "Updated 11:57 UTC") {
			t.Fatalf("width %d dashboard missing observed update time and zone:\n%s", width, view)
		}
	}
}

func TestProviderLabelLeadsEveryDashboardRow(t *testing.T) {
	style := providerLabelStyle(false)
	if _, unset := style.GetBackground().(lipgloss.NoColor); unset {
		t.Fatal("provider label has no background")
	}
	if style.GetPaddingLeft() != 1 || style.GetPaddingRight() != 1 {
		t.Fatalf("provider label padding = (%d, %d), want (1, 1)", style.GetPaddingLeft(), style.GetPaddingRight())
	}

	for _, width := range []int{40, 60, 96, 120} {
		m := dashboardFixture(width, 20)
		for index, sess := range m.sessions {
			row, _, _ := m.dashboardRowParts(sess, index, width)
			plain := ansi.Strip(row)
			providerAt := strings.Index(plain, providerBadge(sess))
			stateAt := strings.Index(plain, lifecycleBadge(sess))
			nameAt := strings.Index(plain, sess.DisplayName[:3])
			if providerAt < 0 || stateAt < 0 || nameAt < 0 || providerAt >= stateAt || providerAt >= nameAt {
				t.Fatalf("width %d row does not lead with provider label: %q", width, plain)
			}
		}
	}
}

func TestSessionUpdatedLabelUsesDashboardTimezone(t *testing.T) {
	zone := time.FixedZone("NPT", 5*60*60+45*60)
	m := dashboardFixture(80, 20)
	m.now = func() time.Time { return time.Date(2026, time.August, 28, 12, 4, 0, 0, zone) }
	sess := m.sessions[0]
	m.lastSeenBySession = map[sessionIdentity]time.Time{
		sessionKey(sess): time.Date(2026, time.August, 28, 6, 12, 0, 0, time.UTC),
	}
	if got := m.sessionUpdatedLabel(sess); got != "Updated 11:57 NPT" {
		t.Fatalf("session update label = %q, want local time with timezone", got)
	}
}

func TestCompactContextDropsOptionalFieldsAsCompleteUnits(t *testing.T) {
	const id = "123e4567-e89b-12d3-a456-426614174000"
	const cwd = "/work/a-very-long-project-directory"
	m := dashboardFixture(60, 20)
	m.sessions = []adapter.Session{{ID: id, AgentType: "codex", DisplayName: "work", Cwd: cwd, ProcAlive: adapter.Alive}}
	m.selected = 0
	compact := ansi.Strip(m.dashboardContext(60))
	if !strings.Contains(compact, "codex") || !strings.Contains(compact, "Updated unknown") {
		t.Fatalf("compact context lost required fields: %q", compact)
	}
	if strings.Contains(compact, id[:8]) || strings.Contains(compact, "a-very-long") {
		t.Fatalf("compact context clipped an optional field instead of dropping it: %q", compact)
	}

	wide := ansi.Strip(m.dashboardContext(120))
	if !strings.Contains(wide, id) || !strings.Contains(wide, cwd) {
		t.Fatalf("wide context dropped fields that fit: %q", wide)
	}
}

func TestAgentsDashboardUsesLiteralOneScreenVocabulary(t *testing.T) {
	m := dashboardFixture(120, 40)
	view := ansi.Strip(m.View().Content)
	assertViewGeometry(t, view, 120, 40)
	for _, want := range []string{
		"UAM", "Agents", "3/3 sessions", "release-check", "Running", "codex", "Attach",
		"repair-tests", "Stopped", "claude", "Resume", "failing-agent", "Failed", "Stop",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("dashboard missing %q:\n%s", want, view)
		}
	}
	for _, banned := range []string{"DEPARTURES", "craft", "GATE", "boarding", "type a command"} {
		if strings.Contains(view, banned) {
			t.Fatalf("dashboard retained metaphor/composer copy %q:\n%s", banned, view)
		}
	}
	assertBorderless(t, view)
}

func TestAgentsDashboardGuaranteesFortyByTwelve(t *testing.T) {
	m := dashboardFixture(40, 12)
	view := ansi.Strip(m.View().Content)
	assertViewGeometry(t, view, 40, 12)
	for _, want := range []string{"Agents", "Running", "Stopped", "Failed", "Attach", "Resume", "▌"} {
		if !strings.Contains(view, want) {
			t.Fatalf("40x12 dashboard missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "verify release pipeline") || strings.Contains(view, "/work/uam") {
		t.Fatalf("narrow dashboard retained secondary fields:\n%s", view)
	}
}

func TestDashboardBreakpointsDependOnWidthOnly(t *testing.T) {
	for _, tc := range []struct {
		width int
		want  dashboardLayout
	}{{40, dashboardNarrow}, {59, dashboardNarrow}, {60, dashboardCompact}, {95, dashboardCompact}, {96, dashboardWide}} {
		if got := dashboardLayoutFor(tc.width); got != tc.want {
			t.Fatalf("layout(%d) = %d, want %d", tc.width, got, tc.want)
		}
	}
	wideShort := dashboardFixture(96, 12).View().Content
	wideTall := dashboardFixture(96, 40).View().Content
	for _, view := range []string{wideShort, wideTall} {
		if !strings.Contains(ansi.Strip(view), "verify release pipeline") {
			t.Fatalf("height changed wide row grammar:\n%s", view)
		}
	}
}

func TestDashboardUsesCompositeProviderSessionIdentity(t *testing.T) {
	m := dashboardFixture(100, 20)
	frame := m.buildDashboardFrame()
	intent := pointerIntentFor(t, frame, dashboardSelect, sessionIdentity{agent: "claude", id: "shared"})
	next, cmd := m.Update(intent)
	if cmd != nil {
		t.Fatal("row selection must not activate")
	}
	selected, ok := next.(Model).selectedSession()
	if !ok || selected.AgentType != "claude" || selected.ID != "shared" {
		t.Fatalf("selected wrong duplicate id: %+v", selected)
	}
}

func TestActionCellActivatesOnFirstClickButRowDoesNot(t *testing.T) {
	m := dashboardFixture(100, 20)
	frame := m.buildDashboardFrame()
	identity := sessionIdentity{agent: "claude", id: "shared"}

	row := pointerIntentFor(t, frame, dashboardSelect, identity)
	next, cmd := m.Update(row)
	if cmd != nil || next.(Model).selected != 1 {
		t.Fatalf("row click should only select: selected=%d cmd=%v", next.(Model).selected, cmd)
	}

	action := pointerIntentFor(t, frame, dashboardPrimary, identity)
	next, cmd = m.Update(action)
	if cmd == nil || next.(Model).selected != 1 {
		t.Fatalf("action click should select and activate: selected=%d cmd=%v", next.(Model).selected, cmd)
	}
}

func TestViewOnMouseUsesDisplayedCompositor(t *testing.T) {
	m := dashboardFixture(100, 20)
	frame := m.buildDashboardFrame()
	var actionID string
	for id, node := range frame.nodes {
		if node.action == dashboardPrimary && node.identity == (sessionIdentity{agent: "claude", id: "shared"}) {
			actionID = id
			break
		}
	}
	if actionID == "" {
		t.Fatal("missing action layer")
	}
	layer := frame.compositor.GetLayer(actionID)
	view := m.View()
	if view.OnMouse == nil {
		t.Fatal("dashboard view must install View.OnMouse")
	}
	cmd := view.OnMouse(tea.MouseClickMsg{X: layer.GetX(), Y: layer.GetY(), Button: tea.MouseLeft})
	if cmd == nil {
		t.Fatal("action cell click produced no semantic command")
	}
	intent, ok := cmd().(dashboardPointerIntent)
	if !ok || intent.action != dashboardPrimary || intent.identity.agent != "claude" {
		t.Fatalf("wrong pointer intent: %#v", intent)
	}
}

func TestStalePointerIntentNeverActivates(t *testing.T) {
	base := dashboardFixture(100, 20)
	identity := sessionIdentity{agent: "codex", id: "shared"}

	t.Run("resize", func(t *testing.T) {
		m := base
		intent := pointerIntentFor(t, m.buildDashboardFrame(), dashboardPrimary, identity)
		m = m.handleWindowSize(tea.WindowSizeMsg{Width: 101, Height: 20})
		next, cmd := m.Update(intent)
		assertStaleIntent(t, next.(Model), cmd)
	})

	t.Run("refresh", func(t *testing.T) {
		m := base
		intent := pointerIntentFor(t, m.buildDashboardFrame(), dashboardPrimary, identity)
		m = m.handleSessionsLoaded(sessionsLoadedMsg{refresh: true})
		next, cmd := m.Update(intent)
		assertStaleIntent(t, next.(Model), cmd)
	})

	t.Run("reorder", func(t *testing.T) {
		m := base
		intent := pointerIntentFor(t, m.buildDashboardFrame(), dashboardPrimary, identity)
		m.sessions[0], m.sessions[1] = m.sessions[1], m.sessions[0]
		next, cmd := m.Update(intent)
		assertStaleIntent(t, next.(Model), cmd)
	})

	t.Run("removal", func(t *testing.T) {
		m := base
		intent := pointerIntentFor(t, m.buildDashboardFrame(), dashboardPrimary, identity)
		m.sessions = m.sessions[1:]
		next, cmd := m.Update(intent)
		assertStaleIntent(t, next.(Model), cmd)
	})
}

func TestWheelOnlyMovesInsideRoster(t *testing.T) {
	m := dashboardFixture(80, 20)
	frame := m.buildDashboardFrame()
	inside := m.dashboardMouseCommand(frame, tea.MouseWheelMsg{X: 2, Y: frame.rosterY, Button: tea.MouseWheelDown})
	if inside == nil {
		t.Fatal("roster wheel should produce navigation intent")
	}
	next, cmd := m.Update(inside())
	if cmd != nil || next.(Model).selected != 1 {
		t.Fatalf("wheel should move one row without backend command: selected=%d cmd=%v", next.(Model).selected, cmd)
	}
	if outside := m.dashboardMouseCommand(frame, tea.MouseWheelMsg{X: 2, Y: frame.height - 1, Button: tea.MouseWheelDown}); outside != nil {
		t.Fatal("wheel over help must not move the roster")
	}
}

func TestStopCellOnlyOpensConfirmation(t *testing.T) {
	m := dashboardFixture(80, 20)
	intent := pointerIntentFor(t, m.buildDashboardFrame(), dashboardStop, sessionIdentity{agent: "codex", id: "shared"})
	next, cmd := m.Update(intent)
	got := next.(Model)
	if cmd != nil || !got.confirmStop || got.confirmStopAgent != "codex" || got.confirmStopID != "shared" {
		t.Fatalf("stop click bypassed or targeted wrong confirmation: %+v cmd=%v", got, cmd)
	}
}

func TestASCIIHeaderAndGlyphsAreMeasuredFallbacks(t *testing.T) {
	previous := ApplyTermCaps(TermCaps{Glyphs: GlyphsASCII, UTF8: true})
	defer ApplyTermCaps(previous)
	m := dashboardFixture(40, 12)
	view := ansi.Strip(m.View().Content)
	if strings.Contains(view, "🤖") || strings.Contains(view, "●") || !strings.Contains(view, "Agents") || !strings.Contains(view, "*") {
		t.Fatalf("ASCII fallback is incomplete:\n%s", view)
	}
	assertViewGeometry(t, view, 40, 12)
}

func TestSmallTerminalIsReadOnly(t *testing.T) {
	m := dashboardFixture(39, 11)
	frame := m.buildDashboardFrame()
	if len(frame.nodes) != 0 || !strings.Contains(ansi.Strip(frame.content), "Agents needs 40x12") {
		t.Fatalf("undersized frame exposed actions or hid minimum: nodes=%d\n%s", len(frame.nodes), frame.content)
	}
}

func TestDashboardPrintableKeysAreInert(t *testing.T) {
	m := dashboardFixture(80, 20)
	m.hasLoaded = true
	for _, pressed := range []string{"a", "9", " ", "backspace"} {
		model, cmd := m.handleKey(keyMsg(pressed))
		m = model.(Model)
		if cmd != nil || m.input != "" || m.selected != 0 {
			t.Fatalf("base key %q mutated dashboard: input=%q selected=%d cmd=%v", pressed, m.input, m.selected, cmd)
		}
	}
	model, _ := m.Update(tea.PasteMsg{Content: "invisible command"})
	if got := model.(Model).input; got != "" {
		t.Fatalf("base paste created hidden input %q", got)
	}

	filtered := m
	filtered.enterFilter()
	model, _ = filtered.handleKey(keyMsg("a"))
	if got := model.(Model).filterQuery; got != "a" {
		t.Fatalf("filter input stopped working: %q", got)
	}
}

func TestDashboardFilterKeyboardLifecycle(t *testing.T) {
	m := dashboardFixture(80, 20)
	m.selected = 1
	m.enterFilter()
	if !m.filterActive || !m.filterSaved {
		t.Fatal("filter did not snapshot the selected session")
	}

	handled, cmd := m.handleFilterKey(keyMsg("release"), "release")
	if !handled || cmd != nil || m.filterQuery != "release" || m.selected != 0 {
		t.Fatalf("filter text did not narrow and reconcile: query=%q selected=%d cmd=%v", m.filterQuery, m.selected, cmd)
	}
	handled, _ = m.handleFilterKey(keyMsg(" "), "space")
	if !handled || m.filterQuery != "release " {
		t.Fatalf("filter space = %q", m.filterQuery)
	}
	handled, _ = m.handleFilterKey(keyMsg("backspace"), "backspace")
	if !handled || m.filterQuery != "release" {
		t.Fatalf("filter backspace = %q", m.filterQuery)
	}
	if handled, _ := m.handleFilterKey(tea.KeyPressMsg{Code: 'x', Text: "x", Mod: tea.ModAlt}, "alt+x"); handled {
		t.Fatal("Alt-modified text was consumed by the filter")
	}

	m.filterQuery = ""
	m.selected = 0
	m.handleFilterKey(keyMsg("down"), "down")
	if m.selected != 1 {
		t.Fatalf("filter down selected %d", m.selected)
	}
	m.handleFilterKey(keyMsg("up"), "up")
	if m.selected != 0 {
		t.Fatalf("filter up selected %d", m.selected)
	}
	if handled, cmd := m.handleFilterKey(keyMsg("enter"), "enter"); !handled || cmd == nil {
		t.Fatal("filter Enter did not invoke the selected primary action")
	}

	m.filterQuery = "no-such-session"
	for _, pressed := range []string{"enter", "right", "ctrl+t", "ctrl+r", "ctrl+x"} {
		handled, cmd := m.handleFilterKey(keyMsg(pressed), pressed)
		if !handled || cmd != nil {
			t.Fatalf("no-match filter key %q escaped: handled=%v cmd=%v", pressed, handled, cmd)
		}
	}

	m.filterQuery = ""
	m.selected = 0
	m.sessions[1].ProcAlive = adapter.Alive
	handled, cmd = m.handleFilterKey(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift}, "shift+down")
	if !handled || cmd == nil || m.selected != 1 {
		t.Fatalf("filtered reorder did not use visible ordering: selected=%d cmd=%v", m.selected, cmd)
	}

	m.handleFilterKey(keyMsg("esc"), "esc")
	restored, ok := m.selectedSession()
	if m.filterActive || !ok || restored.AgentType != "claude" || restored.ID != "shared" {
		t.Fatalf("filter exit did not restore identity: active=%v selected=%+v", m.filterActive, restored)
	}
	m.filterActive = true
	m.filterSaved = false
	m.handleFilterKey(keyMsg("backspace"), "backspace")
	if m.filterActive {
		t.Fatal("empty filter backspace did not exit")
	}
}

func TestDashboardComponentFallbackAndMouseRejections(t *testing.T) {
	if len(newDashboardHelpMap(false, false, "stop").FullHelp()) != 1 {
		t.Fatal("Bubbles full help did not project the short bindings")
	}
	var empty Model
	if empty.activityView() == "" {
		t.Fatal("empty spinner model did not use the Bubbles fallback")
	}
	if got := empty.dashboardBody(0, 0); len(got) != 0 {
		t.Fatalf("zero-sized dashboard body = %#v", got)
	}

	m := dashboardFixture(80, 20)
	frame := m.buildDashboardFrame()
	if cmd := m.dashboardMouseCommand(frame, tea.MouseWheelMsg{X: 2, Y: frame.rosterY, Button: tea.MouseWheelLeft}); cmd != nil {
		t.Fatal("horizontal wheel produced a dashboard command")
	}
	if cmd := m.dashboardMouseCommand(frame, tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseRight}); cmd != nil {
		t.Fatal("right click produced a dashboard command")
	}
	frame.compositor = nil
	if cmd := m.dashboardMouseCommand(frame, tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft}); cmd != nil {
		t.Fatal("click without a compositor produced a dashboard command")
	}
	model, cmd := m.handleMouse(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft})
	if cmd != nil || model.(Model).selected != m.selected {
		t.Fatal("raw mouse coordinates bypassed the displayed-view callback")
	}
}

func TestDashboardUsesBubblesSpinnerForLoadingAndRefresh(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 20})
	initial := ansi.Strip(m.View().Content)
	if !strings.Contains(initial, "Loading agents") || !strings.Contains(initial, ansi.Strip(m.activity.View())) {
		t.Fatalf("initial load lacks Bubbles spinner feedback:\n%s", initial)
	}

	model, cmd := m.Update(m.activity.Tick())
	m = model.(Model)
	if cmd == nil || m.activity.View() == "|" {
		t.Fatalf("spinner did not advance or re-arm: frame=%q cmd=%v", m.activity.View(), cmd)
	}

	m.sessions = []adapter.Session{{ID: "one", AgentType: "codex", DisplayName: "one", ProcAlive: adapter.Alive}}
	m.hasLoaded = true
	m.loading = true
	refresh := ansi.Strip(m.View().Content)
	if !strings.Contains(refresh, "Refreshing") || !strings.Contains(refresh, ansi.Strip(m.activity.View())) || !strings.Contains(refresh, "one") {
		t.Fatalf("refresh spinner displaced the last good roster:\n%s", refresh)
	}
	m = m.handleSessionsLoaded(sessionsLoadedMsg{refresh: true})
	if settled := ansi.Strip(m.View().Content); strings.Contains(settled, "Refreshing") {
		t.Fatalf("spinner feedback survived completed refresh:\n%s", settled)
	}
}

func pointerIntentFor(t *testing.T, frame dashboardFrame, action dashboardAction, identity sessionIdentity) dashboardPointerIntent {
	t.Helper()
	for _, node := range frame.nodes {
		if node.action == action && node.identity == identity {
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
	t.Fatalf("missing node action=%q identity=%+v", action, identity)
	return dashboardPointerIntent{}
}

func assertStaleIntent(t *testing.T, m Model, cmd tea.Cmd) {
	t.Helper()
	if cmd != nil {
		t.Fatal("stale pointer intent returned a backend command")
	}
	if m.message != "View changed; select again." {
		t.Fatalf("stale pointer feedback = %q", m.message)
	}
}

func assertBorderless(t *testing.T, view string) {
	t.Helper()
	for _, glyph := range []string{"┌", "┐", "└", "┘", "│"} {
		if strings.Contains(view, glyph) {
			t.Fatalf("dashboard contains box border %q:\n%s", glyph, view)
		}
	}
}
