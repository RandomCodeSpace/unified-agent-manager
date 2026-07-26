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
)

func TestDashboardDesktopShowsOperationalMetadataWithoutDefaultSplitPane(t *testing.T) {
	now := time.Date(2026, time.July, 13, 12, 0, 0, 0, time.UTC)
	m := NewWithDeps(nil, nil)
	m.now = func() time.Time { return now }
	m.sessions = []adapter.Session{
		{
			ID: "full-session-identity", AgentType: "codex", DisplayName: "release-check",
			Prompt: "verify the release pipeline", Cwd: "/work/unified-agent-manager",
			SessionName: "uam-codex-full-session-identity", ProcAlive: adapter.Alive,
			CreatedAt: now.Add(-2 * time.Hour), PR: &adapter.PRRef{Number: 41, Status: adapter.PROpen},
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
		"SESSIONS", "/ filter", "release-check", "codex", "RUNNING", "2h",
		"verify the release pipeline", "/work/unified-agent-manager", "full-session-identity", "◇41",
		"failed-tests", "claude", "exit 7", "3d",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("desktop dashboard missing %q:\n%s", want, view)
		}
	}
	if !lineContainsAll(view, "SESSIONS", "2") {
		t.Fatalf("the sessions rule must carry the roster count:\n%s", view)
	}
	if lineContainsAll(view, "SESSIONS", "SELECTED") {
		t.Fatalf("operations view must use the full list instead of a default selected split pane:\n%s", view)
	}
	// The wide layout is a cockpit: the roster is one line per session and the
	// selected session's detail lives in the pane beside it.
	for _, want := range []string{"TASK", "OUTPUT", "resumes most recent", "pull request #41 open"} {
		if !strings.Contains(view, want) {
			t.Fatalf("cockpit pane missing %q:\n%s", want, view)
		}
	}
	if !lineContainsAll(view, "SESSIONS", "release-check") {
		t.Fatalf("the roster and the detail pane should share the top body row:\n%s", view)
	}
	assertBorderless(t, view)

	// Narrow enough for one column: there the density ladder gives every
	// session its own task line, not just the one under the cursor.
	single := m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 40}).View()
	if !strings.Contains(single, "repair integration tests") {
		t.Fatalf("single-column layout must show an unselected session's task:\n%s", single)
	}
	assertBorderless(t, single)
}

// TestCockpitOnlyNeedsWidthNotHeight guards the reason cockpitOpen does not
// reuse LayoutWide: LayoutWide demands 28 rows because it governs vertical
// detail, but two columns need horizontal room. Tying them together left an
// ordinary 100x26 window rendering one column with two thirds of the screen
// blank.
func TestCockpitOnlyNeedsWidthNotHeight(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{{ID: "a", AgentType: "codex", DisplayName: "only", Prompt: "work", ProcAlive: adapter.Alive}}

	wideShort := m.handleWindowSize(tea.WindowSizeMsg{Width: 100, Height: 26})
	if !wideShort.cockpitOpen() {
		t.Fatal("a 100x26 terminal is wide enough for two panes")
	}
	if wideShort.layoutClass() == LayoutWide {
		t.Fatal("fixture no longer exercises the case: 100x26 should not be LayoutWide")
	}
	if !lineContainsAll(wideShort.View(), "SESSIONS", "only") {
		t.Fatalf("cockpit should render both panes on the top body row:\n%s", wideShort.View())
	}

	if narrow := m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 40}); narrow.cockpitOpen() {
		t.Fatal("80 columns is not enough for two panes")
	}
	if squat := m.handleWindowSize(tea.WindowSizeMsg{Width: 120, Height: 12}); squat.cockpitOpen() {
		t.Fatal("a 12-row terminal has no room for a detail pane")
	}
	// An unsized model must not be treated as a cockpit: layoutClass classifies
	// it as Wide and component tests render it expecting one column.
	if (Model{}).cockpitOpen() {
		t.Fatal("an unsized model must not open the cockpit")
	}
}

// TestCockpitFollowsTheCursorAndFocusesOnSpace pins the two behaviours that make
// the pane trustworthy: it re-captures output when the selection moves (so the
// tail never sits under the wrong name), and Space gives the output the whole
// pane.
func TestCockpitFollowsTheCursorAndFocusesOnSpace(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{
		{ID: "a", AgentType: "codex", DisplayName: "first", Prompt: "one", ProcAlive: adapter.Alive},
		{ID: "b", AgentType: "claude", DisplayName: "second", Prompt: "two", ProcAlive: adapter.Alive},
	}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.peekText = "stale output from the first session"

	// Moving the cursor must clear the stale tail and request a fresh capture,
	// even though the peek panel was never opened.
	moved := m
	cmd := moved.moveSelectionPeek(1)
	if moved.selected != 1 {
		t.Fatalf("selection did not move: %d", moved.selected)
	}
	if moved.peekText != "" {
		t.Fatalf("cockpit must drop the previous session's tail, got %q", moved.peekText)
	}
	if cmd == nil {
		t.Fatal("cockpit must re-capture output when the selection moves")
	}
	// The reply target is peek-panel state and must not move with the cursor.
	if moved.peekTargetID != "" {
		t.Fatalf("reply target should stay unset while the peek panel is closed, got %q", moved.peekTargetID)
	}

	unfocused := m.View()
	if !strings.Contains(unfocused, "TASK") || !strings.Contains(unfocused, "OUTPUT") {
		t.Fatalf("unfocused cockpit should show both sections:\n%s", unfocused)
	}
	focused := m
	focused.peekOpen = true
	view := focused.View()
	if !lineContainsAll(view, "SESSIONS", "PEEK") {
		t.Fatalf("a focused cockpit should title its pane PEEK beside the roster:\n%s", view)
	}
	if strings.Contains(view, "TASK") {
		t.Fatalf("a focused cockpit gives the output the whole pane:\n%s", view)
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

// TestDashboardCompactSpendsItsBudgetOnEverySessionEqually replaces the older
// expectation that only the selected row carried detail. The density ladder
// divides the body budget uniformly, so a small roster on a small screen shows
// every session's task rather than making the operator walk the cursor to read
// them one at a time.
func TestDashboardCompactSpendsItsBudgetOnEverySessionEqually(t *testing.T) {
	now := time.Date(2026, time.July, 13, 12, 0, 0, 0, time.UTC)
	m := NewWithDeps(nil, nil)
	m.now = func() time.Time { return now }
	m.sessions = []adapter.Session{
		{ID: "one", AgentType: "codex", DisplayName: "selected", Prompt: "fix copy and paste", ProcAlive: adapter.Alive, CreatedAt: now.Add(-8 * time.Minute)},
		{ID: "two", AgentType: "claude", DisplayName: "ordinary", Prompt: "this task stays collapsed", ProcAlive: adapter.Exited, CreatedAt: now.Add(-90 * time.Minute)},
	}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 44, Height: 12})

	view := m.View()
	assertViewGeometry(t, view, 44, 12)
	for _, want := range []string{
		"▌", "1", "2", "selected", "codex", "8m", "fix copy and paste",
		"ordinary", "claude", "1h", "this task stays collapsed",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("compact dashboard missing %q:\n%s", want, view)
		}
	}
	// Compact trades the spelled-out lifecycle for its glyph — the only datum
	// the narrow layout drops, and it drops the word, not the fact.
	if !strings.Contains(view, "●") || !strings.Contains(view, "○") {
		t.Fatalf("compact rows must still carry a lifecycle glyph:\n%s", view)
	}
	if strings.Contains(view, "RUNNING") || strings.Contains(view, "STOPPED") {
		t.Fatalf("compact rows should not spend cells on the lifecycle word:\n%s", view)
	}
	assertBorderless(t, view)
	assertBottomContains(t, view, "›")
}

// TestDashboardChipsAddressTheFirstTenVisibleSessions pins the zero-chord
// addressing contract: a digit is printed next to each of the first ten visible
// sessions, pressing it moves the cursor there, and pressing it again attaches.
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
// verb, and a tap on any line of a block — including its task and provenance
// lines — resolves to that block's session.
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

	// The header is not a session, and neither is a row past the body.
	if _, ok := m.dashboardHitSession(0); ok {
		t.Fatalf("a tap on the header must not select a session")
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
	if !strings.Contains(view, "/ claude") || !strings.Contains(view, "docs") || strings.Contains(view, "release") {
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

	for _, query := range []string{"pipeline", "docs", "stopped"} {
		model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
		m = model.(Model)
		model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(query)})
		m = model.(Model)
		view := m.View()
		if !strings.Contains(view, "docs") && query != "pipeline" {
			t.Fatalf("query %q did not match expected session:\n%s", query, view)
		}
		if query == "pipeline" && !strings.Contains(view, "release") {
			t.Fatalf("task query did not match release session:\n%s", view)
		}
		model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		m = model.(Model)
	}

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = model.(Model)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("definitely absent")})
	m = model.(Model)
	view := m.View()
	if !strings.Contains(view, "No sessions match") || !strings.Contains(view, "0/2") {
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

func TestDashboardTinyKeyboardLayoutAndGroupedFilterStayBounded(t *testing.T) {
	now := time.Date(2026, time.July, 13, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	m := NewWithDeps(nil, nil)
	m.now = func() time.Time { return now }
	m.groupByDir = true
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
	for _, want := range []string{"1/2", "▸", "▲ 2 sessions share this workspace", "release", "codex", "ship", "›"} {
		if !strings.Contains(view, want) {
			t.Fatalf("tiny grouped filter missing %q:\n%s", want, view)
		}
	}
}

func TestDashboardFooterShowsDefaultProviderAndFilterComposer(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.defaultAgent = "opencode"
	m.sessions = []adapter.Session{{ID: "one", AgentType: "opencode", DisplayName: "one", ProcAlive: adapter.Alive}}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 30})
	view := m.View()
	if !strings.Contains(view, "opencode") || !strings.Contains(view, "Tab provider") {
		t.Fatalf("footer must expose the provider selected for bare dispatches:\n%s", view)
	}
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = model.(Model)
	view = m.View()
	if !strings.Contains(view, "filter / ") || !strings.Contains(view, "type to filter") {
		t.Fatalf("active filter must replace the command-looking composer:\n%s", view)
	}
}

func TestDashboardWorkspaceCountsAreScopedToLifecycleAndPinSections(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.groupByDir = true
	m.sessions = []adapter.Session{
		{ID: "running", AgentType: "codex", Cwd: "/work/shared", ProcAlive: adapter.Alive},
		{ID: "stopped", AgentType: "codex", Cwd: "/work/shared", ProcAlive: adapter.Exited},
	}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 30})
	view := m.View()
	// One heading per lifecycle partition, each naming its partition so the
	// repeated workspace name reads as intent rather than as a render fault.
	if got := strings.Count(view, "▸"); got != 2 {
		t.Fatalf("workspace should have one heading per lifecycle section, got %d:\n%s", got, view)
	}
	if !strings.Contains(view, "shared · live") || !strings.Contains(view, "shared · stopped") {
		t.Fatalf("each heading must name its lifecycle partition:\n%s", view)
	}
}

func TestDashboardSelectedIdentityAndPRSurviveLongWorkspaceAtStandardWidth(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{{
		ID: "12345678-1234-1234-1234-123456789abc", AgentType: "codex", DisplayName: "selected",
		Prompt: "review", Cwd: "/home/developer/projects/a/very/long/workspace/path/that/needs/truncation",
		ProcAlive: adapter.Alive, PR: &adapter.PRRef{Number: 99, Status: adapter.PRMerged},
	}}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 30})
	view := m.View()
	// The path is the only unbounded field on the provenance line, so it is the
	// one that yields — from the front, keeping the segments that identify the
	// workspace — while the id and the PR mark survive intact.
	for _, want := range []string{"needs/truncation", "12345678-1234-1234-1234-123456789abc", "◆99"} {
		if !strings.Contains(view, want) {
			t.Fatalf("selected metadata lost %q behind long workspace:\n%s", want, view)
		}
	}
}

func TestDashboardTinyFooterKeepsGuidanceWithLongInput(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.defaultAgent = "codex"
	m.input = strings.Repeat("界", 80)
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 44, Height: 10})
	view := m.View()
	assertViewGeometry(t, view, 44, 10)
	assertBottomContains(t, view, "↑↓ Enter")
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
	if !strings.Contains(view, "first 界") || !strings.Contains(view, "second 界") || strings.Contains(view, "plain") {
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
	for _, query := range []string{"abc-123", "NIGHTLY", "release captain", "ship PIPELINE", "unified-agent-manager", "codex running", "claude stopped"} {
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
