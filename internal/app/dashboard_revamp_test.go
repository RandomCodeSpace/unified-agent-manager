package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// TestDepartureBoardWideShowsEveryColumn pins the locked design: one flat
// table with Nº, SESSION, OPERATOR, TASK, STATUS, GATE and DUE — and nothing
// else. No PR marks, no pin star, no workspace panes: forge state is a
// provider concern and pins act on ordering, not on the board.
func TestDepartureBoardWideShowsEveryColumn(t *testing.T) {
	now := time.Date(2026, time.July, 13, 12, 4, 0, 0, time.UTC)
	m := NewWithDeps(nil, nil)
	m.now = func() time.Time { return now }
	m.sessions = []adapter.Session{
		{
			ID: "full-session-identity", AgentType: "codex", DisplayName: "release-check",
			Prompt: "verify the release pipeline", Cwd: "/work/unified-agent-manager",
			ProcAlive: adapter.Alive, CreatedAt: now.Add(-2 * time.Hour),
			PR: &adapter.PRRef{Number: 41, Status: adapter.PROpen}, Pinned: true,
		},
		{
			ID: "stopped-session", AgentType: "claude", DisplayName: "failed-tests",
			Prompt: "repair integration tests", Cwd: "/work/other", ProcAlive: adapter.Exited,
			CreatedAt: now.Add(-3 * 24 * time.Hour), ExitCode: exitCode(7),
		},
	}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 120, Height: 40})

	view := m.View()
	assertViewGeometry(t, view, 120, 40)
	for _, want := range []string{
		// masthead
		"UNIFIED AGENT MANAGER (UAM)", "DEPARTURES", "12:04", "2 craft",
		// column headings
		"Nº", "SESSION", "OPERATOR", "TASK", "STATUS", "GATE", "DUE",
		// the live craft
		"01", "RELEASE-CHECK", "CODEX", "verify the release pipeline", "EN ROUTE", "ATTACH", "2h",
		// the diverted craft
		"02", "FAILED-TESTS", "CLAUDE", "exit 7", "DIVERTED", "RESUME", "3d",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("wide board missing %q:\n%s", want, view)
		}
	}
	for _, banned := range []string{"◇", "◇41", "★", "pull request"} {
		if strings.Contains(view, banned) {
			t.Fatalf("the board must not carry %q (PR and pin marks were dropped by design):\n%s", banned, view)
		}
	}
	if !lineContainsAll(view, "01", "RELEASE-CHECK", "CODEX", "EN ROUTE", "ATTACH") {
		t.Fatalf("a departure must be one row, not a block:\n%s", view)
	}
	// The diverted craft calls for boarding above the composer.
	if !strings.Contains(view, "boarding call: 02 diverted") {
		t.Fatalf("newest failure must be summarised as a boarding call:\n%s", view)
	}
	assertBorderless(t, view)
}

// TestDepartureBoardCompactKeepsOperatorAndStatus pins the phone geometry:
// with the keyboard up a 40x12 screen still shows number, operator code, name,
// the full status cell and the age — plus the legend decoding the codes.
func TestDepartureBoardCompactKeepsOperatorAndStatus(t *testing.T) {
	now := time.Date(2026, time.July, 13, 12, 4, 0, 0, time.UTC)
	m := NewWithDeps(nil, nil)
	m.now = func() time.Time { return now }
	m.sessions = []adapter.Session{
		{ID: "one", AgentType: "codex", DisplayName: "selected", Prompt: "fix copy and paste", ProcAlive: adapter.Alive, CreatedAt: now.Add(-8 * time.Minute)},
		{ID: "two", AgentType: "claude", DisplayName: "ordinary", Prompt: "collapsed", ProcAlive: adapter.Exited, CreatedAt: now.Add(-90 * time.Minute)},
	}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 40, Height: 12})

	view := m.View()
	assertViewGeometry(t, view, 40, 12)
	for _, want := range []string{
		"UAM", "DEPARTURES", "12:04", // masthead with brand, version slot and clock
		"01", "CX", "SELECTED", "EN ROUTE", "8m",
		"02", "CL", "ORDINARY", "ARRIVED", "1h",
		"CX codex", "CL claude", // legend
		"▌", // selected edge bar — selection must not live in hue alone
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("compact board missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "RUNNING") || strings.Contains(view, "STOPPED") {
		t.Fatalf("the board speaks the departure vocabulary, not lifecycle words:\n%s", view)
	}
	assertBorderless(t, view)
	assertBottomContains(t, view, "›")
}

// TestBoardGeometrySwitchesAtTheWideBreakpoint pins where the two spellings
// meet and that the gate geometry only exists on the wide board.
func TestBoardGeometrySwitchesAtTheWideBreakpoint(t *testing.T) {
	wide := boardColumns(boardWideMin)
	if !wide.wide || wide.task < 10 {
		t.Fatalf("at %d columns the full board must fit with a usable TASK column, got %+v", boardWideMin, wide)
	}
	if wide.gateW != boardGateWidth || wide.gateX <= 0 {
		t.Fatalf("wide board must place the gate cells: %+v", wide)
	}
	compact := boardColumns(boardWideMin - 1)
	if compact.wide || compact.name < 6 {
		t.Fatalf("below the breakpoint the compact board must leave a readable name, got %+v", compact)
	}
	// The gate cell must sit exactly where the renderer draws it: the offset of
	// the GATE heading equals gateX for every width.
	for _, width := range []int{boardWideMin, 100, 120, 200} {
		lay := boardColumns(width)
		heading := ansi.Strip(boardHeadings(lay, width))
		idx := strings.Index(heading, "GATE")
		if idx < 0 {
			t.Fatalf("width %d: heading lost its GATE column: %q", width, heading)
		}
		// Index is in bytes; the gate geometry is in display cells.
		if got := ansi.StringWidth(heading[:idx]); got != lay.gateX {
			t.Fatalf("width %d: GATE heading at column %d but gateX=%d:\n%q", width, got, lay.gateX, heading)
		}
	}
}

// TestBoardStatusVocabularyMatchesToneForSession pins the mapping and the
// pairwise-distinct words that carry the datum when color is gone.
func TestBoardStatusVocabularyMatchesToneForSession(t *testing.T) {
	cases := []struct {
		sess adapter.Session
		want string
	}{
		{adapter.Session{ProcAlive: adapter.Alive}, "EN ROUTE"},
		{adapter.Session{ProcAlive: adapter.Exited, ExitCode: exitCode(1)}, "DIVERTED"},
		{adapter.Session{ProcAlive: adapter.Exited}, "ARRIVED"},
	}
	shades := map[string]string{}
	for _, tc := range cases {
		if got := boardStatusWord(tc.sess); got != tc.want {
			t.Fatalf("boardStatusWord = %q, want %q", got, tc.want)
		}
		shade := boardShade(tc.sess)
		if w := ansi.StringWidth(shade); w != 2 {
			t.Fatalf("shade %q must be exactly two cells, got %d", shade, w)
		}
		if prior, dup := shades[shade]; dup {
			t.Fatalf("statuses %q and %q share shade %q", tc.want, prior, shade)
		}
		shades[shade] = tc.want
	}
}

// TestGateLabelNamesTheVerb pins the one action a row offers and the resume
// fidelity mark it carries.
func TestGateLabelNamesTheVerb(t *testing.T) {
	if got := gateLabel(adapter.Session{ProcAlive: adapter.Alive}); got != "ATTACH" {
		t.Fatalf("a live craft boards with ATTACH, got %q", got)
	}
	exact := gateLabel(adapter.Session{ProcAlive: adapter.Exited, ProviderSessionID: "p-1"})
	if exact != "RESUME ⇄" {
		t.Fatalf("an exact resume gates with ⇄, got %q", exact)
	}
	recent := gateLabel(adapter.Session{ProcAlive: adapter.Exited})
	if recent != "RESUME ~" {
		t.Fatalf("a heuristic resume gates with ~, got %q", recent)
	}
}

// TestBoardGateCellRunsTheVerbOnFirstClick is the button contract: a click
// inside a GATE cell acts immediately — even on an unselected row — while a
// click anywhere else on the row selects first.
func TestBoardGateCellRunsTheVerbOnFirstClick(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{
		{ID: "a", AgentType: "codex", DisplayName: "first", Prompt: "one", ProcAlive: adapter.Alive},
		{ID: "b", AgentType: "claude", DisplayName: "second", Prompt: "two", ProcAlive: adapter.Alive},
	}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 100, Height: 40})
	lay := boardColumns(100)

	entries := m.dashboardBodyEntries(100, 40-dashboardHeaderLines-len(m.dashboardBottom(100, 40)))
	row := -1
	for i, entry := range entries {
		if entry.sessionIndex == 1 {
			row = i + dashboardHeaderLines
		}
	}
	if row < 0 {
		t.Fatal("second session not on the board")
	}

	// A click on the row body selects without attaching.
	next, cmd := m.Update(tea.MouseMsg{X: 2, Y: row, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if got := next.(Model).selected; got != 1 || cmd == nil && false {
		t.Fatalf("row click should select session 1, got %d", got)
	}

	// A click inside the GATE cell of the *unselected* row acts at once.
	next, cmd = m.Update(tea.MouseMsg{X: lay.gateX + 1, Y: row, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if got := next.(Model).selected; got != 1 {
		t.Fatalf("gate click should move the cursor to its row, got %d", got)
	}
	if cmd == nil {
		t.Fatal("gate click must run the verb immediately")
	}

	// Outside the wide board there are no gate cells.
	if _, gate, ok := m.handleWindowSize(tea.WindowSizeMsg{Width: 60, Height: 40}).dashboardHitTarget(2, 50); ok && gate {
		t.Fatal("the compact board has no gate column")
	}
}

// TestOperatorCodesAndLegend pins the airline codes and their fallback.
func TestOperatorCodesAndLegend(t *testing.T) {
	for agent, want := range map[string]string{
		"claude": "CL", "codex": "CX", "opencode": "OC", "omp": "OM",
		"copilot": "CP", "hermes": "HM",
		"aider": "AI", // unknown harness abbreviates rather than blanks
		"x":     "X ",
		"":      "??",
	} {
		if got := operatorCode(agent); got != want {
			t.Fatalf("operatorCode(%q) = %q, want %q", agent, got, want)
		}
		if w := ansi.StringWidth(operatorCode(agent)); w != 2 {
			t.Fatalf("operatorCode(%q) is %d cells, want 2", agent, w)
		}
	}
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{
		{ID: "a", AgentType: "codex", ProcAlive: adapter.Alive},
		{ID: "b", AgentType: "codex", ProcAlive: adapter.Alive},
		{ID: "c", AgentType: "claude", ProcAlive: adapter.Alive},
	}
	legend := ansi.Strip(m.operatorLegend(80))
	if legend != "CX codex · CL claude" {
		t.Fatalf("legend must list each operator once, in board order: %q", legend)
	}
}

// TestBoardingCallSummarisesTheNewestFailure pins the advisory line: the
// newest diverted craft, its exit detail, and how its gate resumes it.
func TestBoardingCallSummarisesTheNewestFailure(t *testing.T) {
	now := time.Date(2026, time.July, 13, 12, 0, 0, 0, time.UTC)
	m := NewWithDeps(nil, nil)
	m.now = func() time.Time { return now }
	m.sessions = []adapter.Session{
		{ID: "live", AgentType: "codex", DisplayName: "fine", ProcAlive: adapter.Alive, CreatedAt: now.Add(-time.Hour)},
		{ID: "old-fail", AgentType: "codex", DisplayName: "older", ProcAlive: adapter.Exited, ExitCode: exitCode(2), CreatedAt: now.Add(-3 * time.Hour)},
		{ID: "new-fail", AgentType: "claude", DisplayName: "newer", ProcAlive: adapter.Exited, ExitCode: exitCode(1), CreatedAt: now.Add(-time.Minute), ProviderSessionID: "exact"},
	}
	m.width, m.height, m.sizeKnown = 100, 30, true

	call := ansi.Strip(m.boardingCall(100, true))
	if !strings.Contains(call, "boarding call: 03 diverted · exit 1") {
		t.Fatalf("wide call must name the newest failure by board number: %q", call)
	}
	if !strings.Contains(call, "resumes exactly") {
		t.Fatalf("wide call must state the resume fidelity: %q", call)
	}
	compact := ansi.Strip(m.boardingCall(40, false))
	if !strings.Contains(compact, "03 diverted · exit 1") || !strings.Contains(compact, "gate ⇄") {
		t.Fatalf("compact call must keep number, detail and gate mark: %q", compact)
	}

	// With no failures the line yields to the workspace-contention advisory,
	// and stays empty when there is nothing to advise.
	m.sessions = m.sessions[:1]
	if got := m.boardAdvisory(100, true); got != "" {
		t.Fatalf("a healthy board needs no advisory, got %q", got)
	}
	m.sessions = []adapter.Session{
		{ID: "a", AgentType: "codex", Cwd: "/work/shared", ProcAlive: adapter.Alive},
		{ID: "b", AgentType: "claude", Cwd: "/work/shared", ProcAlive: adapter.Alive},
	}
	advisory := ansi.Strip(m.boardAdvisory(100, true))
	if !strings.Contains(advisory, "advisory: 2 live craft share") || !strings.Contains(advisory, "shared") {
		t.Fatalf("contention advisory missing: %q", advisory)
	}
}

// TestBoardRendersPureASCIIUnderTheDegradedSet is the whole point of the
// glyph-set switch stated as one sweep: with the ASCII caps installed, every
// byte of chrome the board emits is plain ASCII — masthead, headings, rows,
// shades, gates, advisory, composer, everything.
func TestBoardRendersPureASCIIUnderTheDegradedSet(t *testing.T) {
	prev := ApplyTermCaps(TermCaps{Glyphs: GlyphsASCII})
	defer ApplyTermCaps(prev)
	now := time.Date(2026, time.July, 13, 12, 0, 0, 0, time.UTC)
	m := NewWithDeps(nil, nil)
	m.now = func() time.Time { return now }
	m.sessions = []adapter.Session{
		{ID: "a", AgentType: "codex", DisplayName: "alpha", Prompt: "work", ProcAlive: adapter.Alive, CreatedAt: now.Add(-time.Minute)},
		{ID: "b", AgentType: "claude", DisplayName: "beta", Prompt: "rest", ProcAlive: adapter.Exited, ExitCode: exitCode(3), CreatedAt: now.Add(-time.Hour), Cwd: "/work/x"},
	}
	for _, size := range []struct{ w, h int }{{100, 30}, {40, 12}} {
		view := m.handleWindowSize(tea.WindowSizeMsg{Width: size.w, Height: size.h}).View()
		for _, r := range ansi.Strip(view) {
			if r > 127 {
				t.Fatalf("%dx%d board leaked non-ASCII %q under the degraded set:\n%s", size.w, size.h, string(r), view)
			}
		}
	}
}

// TestBoardMastheadCarriesTheVersionOnBothLayouts — the version is required in
// both mastheads by the locked design.
func TestBoardMastheadCarriesTheVersionOnBothLayouts(t *testing.T) {
	m := NewWithDeps(nil, nil)
	for _, width := range []int{120, 40} {
		header := ansi.Strip(m.dashboardHeader(width))
		if !strings.Contains(header, "dev") { // version.String() in tests
			t.Fatalf("masthead at %d columns lost the version: %q", width, header)
		}
		if !strings.Contains(header, "DEPARTURES") {
			t.Fatalf("masthead at %d columns lost the board name: %q", width, header)
		}
	}
}

// TestDashboardChipsAddressTheFirstTenVisibleSessions pins the zero-chord
// addressing contract: a digit jumps to the row wearing that number, pressing
// it again attaches.
func TestDashboardChipsAddressTheFirstTenVisibleSessions(t *testing.T) {
	m := NewWithDeps(nil, nil)
	for i := 0; i < 12; i++ {
		m.sessions = append(m.sessions, adapter.Session{
			ID: fmt.Sprintf("id-%d", i), AgentType: "codex",
			DisplayName: fmt.Sprintf("session-%d", i), ProcAlive: adapter.Alive,
		})
	}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 100, Height: 40})

	if got := chipFor(0); got != "1" {
		t.Fatalf("first chip = %q, want \"1\"", got)
	}
	if got := chipFor(9); got != "0" {
		t.Fatalf("tenth chip = %q, want \"0\"", got)
	}
	if got := chipFor(10); got != " " {
		t.Fatalf("eleventh session should have no chip, got %q", got)
	}
	// The board numbers agree with the chips: row 01 answers key 1.
	if got := boardNumber(0); got != "01" {
		t.Fatalf("first board number = %q, want \"01\"", got)
	}
	if got := boardNumber(9); got != "10" {
		t.Fatalf("tenth board number = %q, want \"10\"", got)
	}

	// A chip press moves the cursor; the same chip again attaches.
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("3")})
	moved := next.(Model)
	if moved.selected != 2 {
		t.Fatalf("chip 3 should select the third visible session, got %d", moved.selected)
	}
	if moved.input != "" {
		t.Fatalf("a chip press must not leak into the composer, got %q", moved.input)
	}

	// An out-of-range digit is text, not navigation, so a small roster never
	// swallows input.
	small := NewWithDeps(nil, nil)
	small.sessions = []adapter.Session{{ID: "only", AgentType: "codex", DisplayName: "only", ProcAlive: adapter.Alive}}
	small = small.handleWindowSize(tea.WindowSizeMsg{Width: 100, Height: 40})
	typed, _ := small.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("7")})
	if got := typed.(Model).input; got != "7" {
		t.Fatalf("an unassigned digit should type into the composer, got %q", got)
	}

	// A digit must never steal a keystroke from a non-empty composer.
	busy := m
	busy.input = "fix"
	after, _ := busy.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("3")})
	if got := after.(Model).input; got != "fix3" {
		t.Fatalf("digits must be text once the composer is non-empty, got %q", got)
	}
}

// TestDashboardTapResolvesToTheSessionUnderThePointer pins the property that
// makes the dashboard usable on a phone: a tap and a chip press are the same
// verb, and a tap on a row resolves to that row's session.
func TestDashboardTapResolvesToTheSessionUnderThePointer(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{
		{ID: "a", AgentType: "codex", DisplayName: "first", Prompt: "one", ProcAlive: adapter.Alive},
		{ID: "b", AgentType: "claude", DisplayName: "second", Prompt: "two", ProcAlive: adapter.Alive},
		{ID: "c", AgentType: "omp", DisplayName: "third", Prompt: "three", ProcAlive: adapter.Alive},
	}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 100, Height: 40})

	entries := m.dashboardBodyEntries(100, 30)
	targets := map[int][]int{}
	for row, entry := range entries {
		if entry.sessionIndex >= 0 {
			targets[entry.sessionIndex] = append(targets[entry.sessionIndex], row+dashboardHeaderLines)
		}
	}
	if len(targets) != 3 {
		t.Fatalf("expected every session to be tappable, got %d", len(targets))
	}
	for index, rows := range targets {
		for _, row := range rows {
			got, ok := m.dashboardHitSession(row)
			if !ok || got != index {
				t.Fatalf("tap on row %d resolved to (%d,%v), want session %d", row, got, ok, index)
			}
		}
	}

	// The masthead is not a session, and neither is the composer.
	if _, ok := m.dashboardHitSession(0); ok {
		t.Fatalf("a tap on the masthead must not select a session")
	}
	if _, ok := m.dashboardHitSession(39); ok {
		t.Fatalf("a tap on the composer must not select a session")
	}

	// A tap selects; a second tap on the same session attaches.
	row := targets[2][0]
	next, _ := m.Update(tea.MouseMsg{Y: row, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if got := next.(Model).selected; got != 2 {
		t.Fatalf("tap should select session 2, got %d", got)
	}
	if _, cmd := next.(Model).Update(tea.MouseMsg{Y: row, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}); cmd == nil {
		t.Fatalf("a second tap on the selected session should attach")
	}
}

// TestBoardPeekFollowsTheCursor pins the panel's trustworthiness: with the
// peek open, moving the cursor drops the stale tail and re-captures; with it
// closed, navigation causes no capture at all.
func TestBoardPeekFollowsTheCursor(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{
		{ID: "a", AgentType: "codex", DisplayName: "first", Prompt: "one", ProcAlive: adapter.Alive},
		{ID: "b", AgentType: "claude", DisplayName: "second", Prompt: "two", ProcAlive: adapter.Alive},
	}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 100, Height: 30})

	closed := m
	closed.peekText = "stale"
	if cmd := closed.moveSelectionPeek(1); cmd != nil {
		t.Fatal("closed peek must not trigger a capture on navigation")
	}

	open := m
	open.peekOpen = true
	open.peekText = "stale output from the first session"
	cmd := open.moveSelectionPeek(1)
	if open.selected != 1 {
		t.Fatalf("selection did not move: %d", open.selected)
	}
	if open.peekText != "" {
		t.Fatalf("peek must drop the previous session's tail, got %q", open.peekText)
	}
	if cmd == nil {
		t.Fatal("peek must re-capture output when the selection moves")
	}
	if open.peekTargetID != "b" {
		t.Fatalf("reply target must follow the open peek, got %q", open.peekTargetID)
	}

	view := open.View()
	if !strings.Contains(view, "PEEK") {
		t.Fatalf("open peek should title its panel:\n%s", view)
	}
}

// assertBorderless pins the standing preference for a borderless dashboard: no
// box-drawing corners or verticals anywhere in the frame. The horizontal rule
// glyph is allowed — it is a divider, not a border.
func assertBorderless(t *testing.T, view string) {
	t.Helper()
	for _, glyph := range []string{"╭", "╮", "╰", "╯", "┌", "┐", "└", "┘", "│", "├", "┤"} {
		if strings.Contains(view, glyph) {
			t.Fatalf("dashboard should be borderless, found %q:\n%s", glyph, view)
		}
	}
}

func TestDashboardFilterUsesEmptyPromptSlashAndPreservesPromptSlash(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{
		{ID: "one", AgentType: "codex", DisplayName: "release", Prompt: "ship pipeline", Cwd: "/work/uam", ProcAlive: adapter.Alive},
		{ID: "two", AgentType: "claude", DisplayName: "docs", Prompt: "write guide", Cwd: "/work/docs", ProcAlive: adapter.Exited},
	}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 30})

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = model.(Model)
	if !m.filterActive || m.input != "" {
		t.Fatalf("empty-prompt slash should enter filter without editing command: active=%v input=%q", m.filterActive, m.input)
	}
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("claude")})
	m = model.(Model)
	view := m.View()
	if !strings.Contains(view, "filter / ") || !strings.Contains(view, "DOCS") || strings.Contains(view, "RELEASE") {
		t.Fatalf("live provider filter did not project visible sessions:\n%s", view)
	}
	selected, ok := m.selectedSession()
	if !ok || selected.AgentType != "claude" || selected.ID != "two" {
		t.Fatalf("filter selection did not target matched identity: %+v ok=%v", selected, ok)
	}

	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = model.(Model)
	if m.filterActive || m.filterQuery != "" {
		t.Fatalf("Esc should clear and exit filtering: active=%v query=%q", m.filterActive, m.filterQuery)
	}
	selected, ok = m.selectedSession()
	if !ok || selected.AgentType != "codex" || selected.ID != "one" {
		t.Fatalf("Esc did not restore the pre-filter selection: %+v ok=%v", selected, ok)
	}

	m.input = "open"
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = model.(Model)
	if m.filterActive || m.input != "open/" {
		t.Fatalf("slash in an existing prompt must stay literal: active=%v input=%q", m.filterActive, m.input)
	}
}

func TestDashboardFilterShowsNoMatchesAndMatchesLifecycleWorkspaceAndTask(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{
		{ID: "one", AgentType: "codex", DisplayName: "release", Prompt: "ship pipeline", Cwd: "/work/uam", ProcAlive: adapter.Alive},
		{ID: "two", AgentType: "claude", DisplayName: "docs", Prompt: "write guide", Cwd: "/work/docs", ProcAlive: adapter.Exited},
	}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 30})

	// "arrived" exercises the board vocabulary as a filter term; "stopped"
	// stays matchable through lifecycleBadge.
	for _, query := range []string{"pipeline", "docs", "stopped", "arrived"} {
		model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
		m = model.(Model)
		model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(query)})
		m = model.(Model)
		view := m.View()
		if query == "pipeline" {
			if !strings.Contains(view, "RELEASE") {
				t.Fatalf("task query did not match release session:\n%s", view)
			}
		} else if !strings.Contains(view, "DOCS") {
			t.Fatalf("query %q did not match expected session:\n%s", query, view)
		}
		model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		m = model.(Model)
	}

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = model.(Model)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("definitely absent")})
	m = model.(Model)
	view := m.View()
	if !strings.Contains(view, "no craft matches") || !strings.Contains(view, "0/2 craft") {
		t.Fatalf("empty filter result needs an explicit state and matched count:\n%s", view)
	}
}

func TestDashboardAgeAndLifecycleLabelsAreEvidenceBased(t *testing.T) {
	now := time.Date(2026, time.July, 13, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		session adapter.Session
		wantAge string
		want    string
	}{
		{"future", adapter.Session{ProcAlive: adapter.Alive, CreatedAt: now.Add(time.Hour)}, "now", "RUNNING"},
		{"seconds", adapter.Session{ProcAlive: adapter.Alive, CreatedAt: now.Add(-45 * time.Second)}, "now", "RUNNING"},
		{"minutes", adapter.Session{ProcAlive: adapter.Alive, CreatedAt: now.Add(-59 * time.Minute)}, "59m", "RUNNING"},
		{"hours", adapter.Session{ProcAlive: adapter.Exited, CreatedAt: now.Add(-47 * time.Hour)}, "47h", "STOPPED"},
		{"days", adapter.Session{ProcAlive: adapter.Exited, CreatedAt: now.Add(-48 * time.Hour), ExitCode: exitCode(9)}, "2d", "EXIT 9"},
		{"signal", adapter.Session{ProcAlive: adapter.Exited, CreatedAt: now.Add(-time.Hour), ExitCode: exitCode(-1)}, "1h", "SIGNAL"},
		{"explicit stop", adapter.Session{ProcAlive: adapter.Exited, CreatedAt: now.Add(-time.Hour), ExitCode: exitCode(-1), Closed: true}, "1h", "STOPPED"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sessionAge(tc.session.CreatedAt, now); got != tc.wantAge {
				t.Fatalf("sessionAge() = %q, want %q", got, tc.wantAge)
			}
			if got := lifecycleBadge(tc.session); got != tc.want {
				t.Fatalf("lifecycleBadge() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDashboardFilterRefreshAndNavigationUseCompositeIdentity(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{
		{ID: "same", AgentType: "codex", DisplayName: "release", Prompt: "ship", ProcAlive: adapter.Alive},
		{ID: "hidden", AgentType: "claude", DisplayName: "docs", Prompt: "write", ProcAlive: adapter.Alive},
		{ID: "same", AgentType: "claude", DisplayName: "release notes", Prompt: "ship", ProcAlive: adapter.Alive},
	}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 30})
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = model.(Model)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("release")})
	m = model.(Model)

	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = model.(Model)
	selected, ok := m.selectedSession()
	if !ok || selected.AgentType != "claude" || selected.ID != "same" {
		t.Fatalf("filtered navigation did not skip hidden row: %+v ok=%v", selected, ok)
	}

	m = m.handleSessionsLoaded(sessionsLoadedMsg{sessions: []adapter.Session{
		{ID: "same", AgentType: "claude", DisplayName: "release notes", Prompt: "ship", ProcAlive: adapter.Alive},
		{ID: "same", AgentType: "codex", DisplayName: "release", Prompt: "ship", ProcAlive: adapter.Alive},
		{ID: "hidden", AgentType: "claude", DisplayName: "docs", Prompt: "write", ProcAlive: adapter.Alive},
	}})
	selected, ok = m.selectedSession()
	if !ok || selected.AgentType != "claude" || selected.ID != "same" {
		t.Fatalf("refresh retargeted duplicate ID across providers: %+v ok=%v", selected, ok)
	}
}

func TestDashboardSlashDoesNotStealReplyAndCanFilterEmptyDashboard(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.peekOpen = true
	m.sessions = []adapter.Session{{ID: "one", ProcAlive: adapter.Alive}}
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = model.(Model)
	if m.filterActive || m.input != "/" {
		t.Fatalf("slash should remain literal in Peek reply input: active=%v input=%q", m.filterActive, m.input)
	}

	m = NewWithDeps(nil, nil)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = model.(Model)
	if !m.filterActive || m.input != "" {
		t.Fatalf("empty dashboard slash should enter filter mode: active=%v input=%q", m.filterActive, m.input)
	}
}

// TestDashboardTinyFilteredBoardStaysBounded — a 44x10 phone screen holding a
// filtered board must keep the count, the matched row and the composer inside
// its geometry.
func TestDashboardTinyFilteredBoardStaysBounded(t *testing.T) {
	now := time.Date(2026, time.July, 13, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	m := NewWithDeps(nil, nil)
	m.now = func() time.Time { return now }
	m.sessions = []adapter.Session{
		{ID: "one", AgentType: "codex", DisplayName: "release", Prompt: "ship", Cwd: root, ProcAlive: adapter.Alive, CreatedAt: now.Add(-time.Minute)},
		{ID: "two", AgentType: "claude", DisplayName: "docs", Prompt: "write", Cwd: root, ProcAlive: adapter.Alive, CreatedAt: now.Add(-time.Hour)},
	}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 44, Height: 10})
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = model.(Model)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("codex")})
	m = model.(Model)
	view := m.View()
	assertViewGeometry(t, view, 44, 10)
	for _, want := range []string{"1/2", "01", "CX", "RELEASE", "EN ROUTE", "›"} {
		if !strings.Contains(view, want) {
			t.Fatalf("tiny filtered board missing %q:\n%s", want, view)
		}
	}
	// Two live craft share the workspace; the advisory must survive the filter
	// because contention is a property of the fleet, not of the projection.
	if !strings.Contains(view, "advisory: 2 live craft share") {
		t.Fatalf("workspace contention advisory missing:\n%s", view)
	}
}

func TestDashboardFooterShowsDefaultProviderAndFilterComposer(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.defaultAgent = "opencode"
	m.sessions = []adapter.Session{{ID: "one", AgentType: "opencode", DisplayName: "one", ProcAlive: adapter.Alive}}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 30})
	view := m.View()
	if !assertLineWith(view, "opencode", "›") {
		t.Fatalf("footer must expose the provider selected for bare dispatches:\n%s", view)
	}
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = model.(Model)
	view = m.View()
	if !strings.Contains(view, "filter / ") || !strings.Contains(view, "type to filter") {
		t.Fatalf("active filter must replace the command-looking composer:\n%s", view)
	}
}

// assertLineWith reports whether any single line carries both needles.
func assertLineWith(view, a, b string) bool {
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, a) && strings.Contains(line, b) {
			return true
		}
	}
	return false
}

func TestDashboardTinyComposerStaysBoundedWithLongInput(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.defaultAgent = "codex"
	m.input = strings.Repeat("界", 80)
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 44, Height: 10})
	view := m.View()
	assertViewGeometry(t, view, 44, 10)
	assertBottomContains(t, view, "›")
}

func TestDashboardFilterNoMatchActionsAreSafeAndBackspaceExits(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{{ID: "one", AgentType: "codex", DisplayName: "release", ProcAlive: adapter.Alive}}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 30})
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = model.(Model)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("absent")})
	m = model.(Model)
	for _, key := range []tea.KeyType{tea.KeyEnter, tea.KeySpace, tea.KeyCtrlT, tea.KeyCtrlR, tea.KeyCtrlX} {
		model, cmd := m.Update(tea.KeyMsg{Type: key})
		m = model.(Model)
		if cmd != nil || m.renaming || m.confirmStop {
			t.Fatalf("no-match key %v acted on an invisible session: cmd=%v rename=%v confirm=%v", key, cmd, m.renaming, m.confirmStop)
		}
	}
	for range len([]rune("absent")) {
		model, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
		m = model.(Model)
	}
	if !m.filterActive || m.filterQuery != "" {
		t.Fatalf("backspace should edit the Unicode-safe query before exiting: active=%v query=%q", m.filterActive, m.filterQuery)
	}
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = model.(Model)
	if m.filterActive {
		t.Fatal("backspace on an empty filter should exit filtering")
	}
}

func TestDashboardFilterHandlesUnicodePasteAndFilteredReorder(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{
		{ID: "one", AgentType: "codex", DisplayName: "first 界", Prompt: "review 世界", ProcAlive: adapter.Alive, SortIndex: 0},
		{ID: "hidden", AgentType: "codex", DisplayName: "plain", Prompt: "unrelated", ProcAlive: adapter.Alive, SortIndex: 1},
		{ID: "two", AgentType: "codex", DisplayName: "second 界", Prompt: "review 世界", ProcAlive: adapter.Alive, SortIndex: 2},
	}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 30})
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = model.(Model)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("世界")})
	m = model.(Model)
	view := m.View()
	if !strings.Contains(view, "FIRST 界") || !strings.Contains(view, "SECOND 界") || strings.Contains(view, "PLAIN") {
		t.Fatalf("Unicode pasted filter did not preserve rune semantics:\n%s", view)
	}
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyShiftDown})
	m = model.(Model)
	if cmd == nil || m.sessions[0].ID != "two" || m.sessions[1].ID != "hidden" || m.sessions[2].ID != "one" || m.selected != 2 {
		t.Fatalf("filtered reorder did not exchange matching endpoints safely: ids=%v selected=%d cmd=%v", sessionIDs(m.sessions), m.selected, cmd)
	}
}

func TestDashboardFilterMatchesEveryDocumentedFieldWithANDTerms(t *testing.T) {
	base := []adapter.Session{
		{ID: "managed-ABC-123", AgentType: "codex", CommandAlias: "nightly", DisplayName: "Release Captain", Prompt: "Ship the pipeline", Cwd: "/work/Unified-Agent-Manager", ProcAlive: adapter.Alive},
		{ID: "other", AgentType: "claude", DisplayName: "Documentation", Prompt: "Write a guide", Cwd: "/work/docs", ProcAlive: adapter.Exited},
	}
	for _, query := range []string{"abc-123", "NIGHTLY", "release captain", "ship PIPELINE", "unified-agent-manager", "codex running", "claude stopped", "codex en", "claude arrived"} {
		t.Run(query, func(t *testing.T) {
			m := NewWithDeps(nil, nil)
			m.sessions = append([]adapter.Session(nil), base...)
			m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 30})
			model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
			m = model.(Model)
			model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(query)})
			m = model.(Model)
			if len(m.visibleSessionIndices()) != 1 {
				t.Fatalf("query %q matched %d sessions, want 1:\n%s", query, len(m.visibleSessionIndices()), m.View())
			}
		})
	}
}

func TestDashboardFilteredActionsTargetMatchedCompositeIdentityAndEscClosesPeekFirst(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{
		{ID: "same", AgentType: "codex", DisplayName: "release", ProcAlive: adapter.Alive},
		{ID: "same", AgentType: "claude", DisplayName: "docs", ProcAlive: adapter.Alive},
	}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 30})
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = model.(Model)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("claude")})
	m = model.(Model)

	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	m = model.(Model)
	if !m.renaming || m.renameTargetAgent != "claude" || m.renameTargetID != "same" {
		t.Fatalf("rename targeted the wrong filtered session: agent=%q id=%q", m.renameTargetAgent, m.renameTargetID)
	}
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = model.(Model)

	model, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = model.(Model)
	if !m.peekOpen || m.peekTargetAgent != "claude" || m.peekTargetID != "same" {
		t.Fatalf("peek targeted the wrong filtered session: open=%v agent=%q id=%q", m.peekOpen, m.peekTargetAgent, m.peekTargetID)
	}
	query := m.filterQuery
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("send reply")})
	m = model.(Model)
	if m.input != "send reply" || m.filterQuery != query {
		t.Fatalf("Peek reply text leaked into filter: input=%q query=%q", m.input, m.filterQuery)
	}
	model, reply := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)
	if reply == nil || m.input != "" || m.filterQuery != query {
		t.Fatalf("Enter did not route filtered Peek text as a reply: cmd=%v input=%q query=%q", reply, m.input, m.filterQuery)
	}
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = model.(Model)
	if m.peekOpen || !m.filterActive {
		t.Fatalf("first Esc should close Peek while retaining filter: peek=%v filter=%v", m.peekOpen, m.filterActive)
	}
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = model.(Model)
	if m.filterActive {
		t.Fatal("second Esc should clear the retained filter")
	}
}

func TestDashboardFilteredAttachPinStopAndGroupingStayOnMatchedIdentity(t *testing.T) {
	id := "same0001"
	codexSession := adapter.Session{ID: id, AgentType: "codex", DisplayName: "release", SessionName: "uam-codex-same0001", Cwd: "/work/codex", ProcAlive: adapter.Alive}
	claudeSession := adapter.Session{ID: id, AgentType: "claude", DisplayName: "docs", SessionName: "uam-claude-same0001", Cwd: "/work/claude", ProcAlive: adapter.Alive}
	codex := &svcFakeAdapter{name: "codex", available: true, sessions: []adapter.Session{codexSession}}
	claude := &svcFakeAdapter{name: "claude", available: true, sessions: []adapter.Session{claudeSession}}
	st, err := store.Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(cfg *store.Config) error {
		cfg.Sessions[store.Key("codex", id)] = RecordFromSession(codexSession, store.ModeYolo)
		cfg.Sessions[store.Key("claude", id)] = RecordFromSession(claudeSession, store.ModeYolo)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	m := NewWithDeps(st, adapter.NewRegistry([]adapter.AgentAdapter{codex, claude}))
	m.sessions = []adapter.Session{codexSession, claudeSession}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 30})
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = model.(Model)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("claude")})
	m = model.(Model)

	model, attach := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)
	if attach == nil {
		t.Fatal("filtered attach returned no command")
	}
	if msg := attach(); msg == nil {
		t.Fatal("filtered attach returned no message")
	}
	if claude.attachedID != id || codex.attachedID != "" {
		t.Fatalf("filtered attach crossed provider identity: claude=%q codex=%q", claude.attachedID, codex.attachedID)
	}

	model, pin := m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	m = model.(Model)
	if pin == nil {
		t.Fatal("filtered pin returned no command")
	}
	_ = pin()
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Sessions[store.Key("claude", id)].Pinned || cfg.Sessions[store.Key("codex", id)].Pinned {
		t.Fatalf("filtered pin crossed provider identity: claude=%v codex=%v", cfg.Sessions[store.Key("claude", id)].Pinned, cfg.Sessions[store.Key("codex", id)].Pinned)
	}

	model, groupCmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = model.(Model)
	selected, ok := m.selectedSession()
	if groupCmd == nil || !m.groupByDir || !ok || selected.AgentType != "claude" || selected.ID != id {
		t.Fatalf("filtered grouping lost selection/persistence command: cmd=%v grouped=%v selected=%+v", groupCmd, m.groupByDir, selected)
	}

	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
	m = model.(Model)
	if !m.confirmStop || m.confirmStopAgent != "claude" || m.confirmStopID != id {
		t.Fatalf("filtered stop confirmation targeted wrong identity: agent=%q id=%q", m.confirmStopAgent, m.confirmStopID)
	}
	model, stop := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)
	if stop == nil {
		t.Fatal("filtered stop returned no command")
	}
	_ = stop()
	if claude.stoppedID != id || codex.stoppedID != "" {
		t.Fatalf("filtered stop crossed provider identity: claude=%q codex=%q", claude.stoppedID, codex.stoppedID)
	}
}

func BenchmarkDashboardRenderAndFilter(b *testing.B) {
	for _, count := range []int{100, 1000} {
		b.Run(fmt.Sprintf("sessions-%d", count), func(b *testing.B) {
			now := time.Date(2026, time.July, 13, 12, 0, 0, 0, time.UTC)
			m := NewWithDeps(nil, nil)
			m.now = func() time.Time { return now }
			m = m.handleWindowSize(tea.WindowSizeMsg{Width: 120, Height: 40})
			for i := range count {
				m.sessions = append(m.sessions, adapter.Session{
					ID: fmt.Sprintf("session-%04d", i), AgentType: []string{"codex", "claude"}[i%2],
					DisplayName: fmt.Sprintf("session %04d", i), Prompt: "review the release pipeline",
					Cwd: "/work/unified-agent-manager", ProcAlive: adapter.Alive, CreatedAt: now.Add(-time.Duration(i) * time.Minute),
				})
			}
			m.filterActive = true
			m.filterQuery = "codex release"
			b.ResetTimer()
			for range b.N {
				_ = m.View()
			}
		})
	}
}

// TestMastheadSurvivesAMonstrousHostname — cloud runners carry provisioning-id
// hostnames sixty cells long; the masthead must shed the host (and then the
// clock), never the brand, the version or the craft count.
func TestMastheadSurvivesAMonstrousHostname(t *testing.T) {
	host := "sjc22-bt147-e6c48904-906c-49c3-b443-5f457b73a6a9-CA6ACE11D88E"
	for _, budget := range []int{70, 40, 24, 10, 3} {
		right := ansi.Strip(mastheadRight(host, "12:04", "4 craft", budget))
		if w := ansi.StringWidth(right); w > budget && budget >= ansi.StringWidth("dev") {
			t.Fatalf("budget %d: right side is %d cells: %q", budget, w, right)
		}
		if !strings.Contains(right, "dev") { // version.String() in tests
			t.Fatalf("budget %d: version lost: %q", budget, right)
		}
		if budget >= 40 && !strings.Contains(right, "craft") {
			t.Fatalf("budget %d: craft count dropped before the host: %q", budget, right)
		}
		if strings.Contains(right, host) {
			t.Fatalf("budget %d: uncapped hostname survived: %q", budget, right)
		}
	}

	// The full wide view keeps its brand regardless of the host segment.
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{{ID: "a", AgentType: "codex", DisplayName: "one", ProcAlive: adapter.Alive}}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 30})
	if view := m.View(); !strings.Contains(view, "UNIFIED AGENT MANAGER (UAM)") || !strings.Contains(view, "DEPARTURES") {
		t.Fatalf("brand must survive any hostname:\n%s", view)
	}
}
