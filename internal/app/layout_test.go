package app

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

func TestLayoutClassAndDashboardModeAreDerived(t *testing.T) {
	tests := []struct {
		width, height int
		want          LayoutClass
	}{
		{120, 40, LayoutWide},
		{80, 30, LayoutStandard},
		{44, 20, LayoutCompact},
		{44, 12, LayoutCompact},
		{120, 20, LayoutCompact},
	}
	for _, tc := range tests {
		m := Model{width: tc.width, height: tc.height}
		if got := m.layoutClass(); got != tc.want {
			t.Errorf("layoutClass(%dx%d) = %v, want %v", tc.width, tc.height, got, tc.want)
		}
	}

	m := Model{}
	if got := m.dashboardMode(); got != ModeOperations {
		t.Fatalf("default dashboard mode = %v, want operations", got)
	}
	m.peekOpen = true
	if got := m.dashboardMode(); got != ModePeek {
		t.Fatalf("peek dashboard mode = %v, want peek", got)
	}
	m.wizard = true
	if got := m.dashboardMode(); got != ModeNew {
		t.Fatalf("wizard dashboard mode = %v, want new", got)
	}
}

func TestDashboardRequiredFixturesStayWithinTerminal(t *testing.T) {
	sizes := []struct{ width, height int }{{120, 40}, {80, 30}, {44, 20}, {44, 12}}
	modes := []struct {
		name string
		set  func(*Model)
		want string
	}{
		{"operations", func(*Model) {}, "›"},
		{"peek", func(m *Model) { m.peekOpen = true }, "reply"},
		{"new", func(m *Model) { m.wizard = true; m.wizardStep = 3 }, "NEW SESSION"},
	}
	for _, size := range sizes {
		for _, mode := range modes {
			t.Run(fmt.Sprintf("%s/%dx%d", mode.name, size.width, size.height), func(t *testing.T) {
				m := responsiveFixture(0, 0)
				m = m.handleWindowSize(tea.WindowSizeMsg{Width: size.width, Height: size.height})
				mode.set(&m)
				view := m.View()
				assertViewGeometry(t, view, size.width, size.height)
				if !strings.Contains(view, mode.want) {
					t.Fatalf("view lost required %s affordance %q:\n%s", mode.name, mode.want, view)
				}
			})
		}
	}
}

func TestWideOperationsAndPeekUseTwoPanes(t *testing.T) {
	m := responsiveFixture(120, 40)
	operations := m.View()
	if !lineContainsAll(operations, "RUNNING", "SELECTED") {
		t.Fatalf("wide operations should place list and selected pane side-by-side:\n%s", operations)
	}
	m.peekOpen = true
	peek := m.View()
	if !lineContainsAll(peek, "RUNNING", "PEEK") {
		t.Fatalf("wide peek should retain the list beside the peek pane:\n%s", peek)
	}
}

func TestCompactRenderingKeepsUnicodeValidAndBoundsLongContent(t *testing.T) {
	m := responsiveFixture(44, 12)
	m.input = "部署 café e\u0301 🚀 " + strings.Repeat("界", 80)
	m.message = strings.Repeat("status 🚀 ", 40)
	m.peekOpen = true
	m.peekText = strings.Repeat("界", 100) + "\n" + strings.Repeat("e\u0301🚀", 80)
	view := m.View()
	if !utf8.ValidString(view) {
		t.Fatal("responsive rendering produced invalid UTF-8")
	}
	assertViewGeometry(t, view, 44, 12)
}

func TestCompactModesAreExclusiveAndKeepBottomPrompt(t *testing.T) {
	m := responsiveFixture(44, 12)
	operations := m.View()
	if strings.Contains(operations, uamANSILogo) {
		t.Fatalf("compact operations must omit the ASCII logo:\n%s", operations)
	}
	assertBottomContains(t, operations, "›")

	m.peekOpen = true
	peek := m.View()
	if strings.Contains(peek, "SESSIONS") || strings.Contains(peek, "RUNNING") {
		t.Fatalf("compact peek must replace the sessions surface:\n%s", peek)
	}
	assertBottomContains(t, peek, "reply")

	m.wizard = true
	newView := m.View()
	if strings.Contains(newView, "SESSIONS") || strings.Contains(newView, "PEEK") {
		t.Fatalf("compact new must replace other primary surfaces:\n%s", newView)
	}
	assertBottomContains(t, newView, "›")
}

func TestNoColorResponsiveViewKeepsSemanticGlyphs(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestNoColorResponsiveViewHelper$")
	cmd.Env = append(withoutColorEnvironment(os.Environ()),
		"UAM_NO_COLOR_HELPER=1", "NO_COLOR=1", "TERM=xterm-256color", "COLORTERM=truecolor")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("NO_COLOR helper failed: %v\n%s", err, out)
	}
	view := string(out)
	if strings.Contains(view, "\x1b[") {
		t.Fatalf("NO_COLOR view contains SGR escapes: %q", view)
	}
	for _, glyph := range []string{"▸", "★", "⟳", "◦", "!", "●"} {
		if !strings.Contains(view, glyph) {
			t.Fatalf("NO_COLOR view lost semantic glyph %q:\n%s", glyph, view)
		}
	}
}

func TestNoColorResponsiveViewHelper(t *testing.T) {
	if os.Getenv("UAM_NO_COLOR_HELPER") != "1" {
		return
	}
	if got := colorprofile.Env(os.Environ()); got != colorprofile.Ascii {
		t.Fatalf("NO_COLOR did not win over color-capable TERM: %s", got)
	}
	m := responsiveFixture(0, 0)
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 30})
	m.sessions[0].PR = &adapter.PRRef{Status: adapter.PRMerged}
	m.sessions[1].ProcAlive = adapter.Exited
	m.sessions[2].ProcAlive = adapter.Exited
	m.sessions[2].ExitCode = exitCode(1)
	SortSessions(m.sessions)
	m.selected = 0
	_, _ = os.Stdout.WriteString(m.View())
}

func withoutColorEnvironment(env []string) []string {
	filtered := make([]string, 0, len(env))
	for _, item := range env {
		key, _, _ := strings.Cut(item, "=")
		switch key {
		case "NO_COLOR", "TERM", "COLORTERM":
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func TestKnownZeroAndTinyHeightsStayBoundedAndRetainPrompt(t *testing.T) {
	m := responsiveFixture(0, 0)
	m.message = "refresh failed"
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 44, Height: 0})
	if got := m.View(); got != "" {
		t.Fatalf("known zero-height view must be empty, got %q", got)
	}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 44, Height: 1})
	view := m.View()
	assertViewGeometry(t, view, 44, 1)
	if !strings.Contains(view, "›") || strings.Contains(view, "refresh failed") {
		t.Fatalf("height-1 view must prioritize command prompt: %q", view)
	}
	m.peekOpen = true
	view = m.View()
	if !strings.Contains(view, "reply") {
		t.Fatalf("height-1 peek view must retain reply prompt: %q", view)
	}
}

func TestCompactWizardEveryStepKeepsEssentialAffordances(t *testing.T) {
	tests := []struct {
		step int
		want []string
	}{
		{0, []string{"provider", "Tab", "Enter", "Esc"}},
		{1, []string{"command alias", "Enter", "Esc"}},
		{2, []string{"working directory", "Tab", "Enter", "Esc", "⚠ not a git repo"}},
		{3, []string{"#name prompt", "Ctrl+G", "Enter", "Esc"}},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("step-%d", tc.step+1), func(t *testing.T) {
			m := responsiveFixture(0, 0)
			m.wizard = true
			m.wizardStep = tc.step
			m.input = "typed-current-step"
			if tc.step == 2 {
				m.input = "/definitely-not-a-git-workspace"
			}
			m = m.handleWindowSize(tea.WindowSizeMsg{Width: 44, Height: 12})
			view := m.View()
			assertViewGeometry(t, view, 44, 12)
			for _, want := range tc.want {
				if !strings.Contains(view, want) {
					t.Fatalf("wizard step %d lost %q:\n%s", tc.step+1, want, view)
				}
			}
			assertBottomContains(t, view, m.input)
		})
	}
}

func TestPeekSurfacePreservesBlankPhysicalLines(t *testing.T) {
	m := responsiveFixture(44, 12)
	m.peekText = "older\nblank-before\n\nblank-after\nnewest"
	lines := m.peekSurfaceLines(44, 7, LayoutCompact)
	got := strings.Join(lines, "\n")
	if !strings.Contains(got, "blank-before\n\nblank-after") {
		t.Fatalf("peek collapsed a physical blank line: %q", got)
	}
}

func TestResizeAcrossFixturesPreservesSelectionAndInput(t *testing.T) {
	m := responsiveFixture(120, 40)
	m.selected = 9
	want, _ := m.selectedSession()
	wantInput := m.input
	for _, size := range []struct{ width, height int }{{80, 30}, {44, 20}, {44, 12}, {120, 40}} {
		m = m.handleWindowSize(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		assertViewGeometry(t, m.View(), size.width, size.height)
		got, ok := m.selectedSession()
		if !ok || got.AgentType != want.AgentType || got.ID != want.ID {
			t.Fatalf("resize to %dx%d changed selection from %s/%s to %+v", size.width, size.height, want.AgentType, want.ID, got)
		}
		if m.input != wantInput {
			t.Fatalf("resize to %dx%d changed input: got %q want %q", size.width, size.height, m.input, wantInput)
		}
	}
}

func TestRefreshPreservesSelectionByProviderAndID(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{
		{ID: "same", AgentType: "claude", DisplayName: "claude row"},
		{ID: "same", AgentType: "codex", DisplayName: "codex row"},
		{ID: "other", AgentType: "claude", DisplayName: "other row"},
	}
	m.selected = 1
	m = m.handleSessionsLoaded(sessionsLoadedMsg{sessions: []adapter.Session{
		{ID: "other", AgentType: "claude", DisplayName: "other row"},
		{ID: "same", AgentType: "claude", DisplayName: "claude row"},
		{ID: "same", AgentType: "codex", DisplayName: "codex row"},
	}})
	selected, ok := m.selectedSession()
	if !ok || selected.AgentType != "codex" || selected.ID != "same" {
		t.Fatalf("refresh changed selected identity: %+v, ok=%v", selected, ok)
	}
}

func responsiveFixture(width, height int) Model {
	m := NewWithDeps(nil, nil)
	if width != 0 || height != 0 {
		m = m.handleWindowSize(tea.WindowSizeMsg{Width: width, Height: height})
	}
	m.defaultAgent = "claude"
	m.input = "部署 café e\u0301 🚀"
	m.peekText = "first tail line\n" + strings.Repeat("long peek 世界 🚀 ", 20) + "\nlast tail line"
	for i := 0; i < 16; i++ {
		m.sessions = append(m.sessions, adapter.Session{
			ID:          fmt.Sprintf("session-%02d", i),
			AgentType:   []string{"claude", "codex"}[i%2],
			DisplayName: fmt.Sprintf("部署 café é 🚀 session %02d with a long name", i),
			Prompt:      strings.Repeat("review 世界 ", 12),
			Cwd:         "/tmp/a/very/long/workspace/path/with/界/and/more/components",
			ProcAlive:   adapter.Alive,
			Pinned:      i == 0,
			Closed:      i >= 12,
			CreatedAt:   time.Date(2026, time.July, 12, 12, i, 0, 0, time.UTC),
		})
	}
	m.selected = 7
	return m
}

func assertViewGeometry(t *testing.T, view string, width, height int) {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(view, "\n"), "\n")
	if len(lines) > height {
		t.Fatalf("view has %d lines, terminal height is %d:\n%s", len(lines), height, view)
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got > width {
			t.Fatalf("line %d is %d columns, terminal width is %d: %q", i+1, got, width, line)
		}
	}
}

func lineContainsAll(view string, needles ...string) bool {
	for _, line := range strings.Split(view, "\n") {
		all := true
		for _, needle := range needles {
			all = all && strings.Contains(line, needle)
		}
		if all {
			return true
		}
	}
	return false
}

func assertBottomContains(t *testing.T, view, needle string) {
	t.Helper()
	lines := strings.Split(view, "\n")
	start := max(0, len(lines)-2)
	if !strings.Contains(strings.Join(lines[start:], "\n"), needle) {
		t.Fatalf("bottom prompt lost %q:\n%s", needle, view)
	}
}
