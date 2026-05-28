package app

import (
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/version"
	"github.com/charmbracelet/lipgloss"
)

func TestRenderTableGroupsSessionsByStatus(t *testing.T) {
	m := NewWithDeps(nil, nil)
	// live-one: live pane, status=active → ACTIVE
	// stopped-active: dead pane, status=active (e.g., reboot survivor) → ACTIVE
	// closed-one: dead pane, status=closed_by_user → CLOSED
	m.sessions = []adapter.Session{
		{ID: "1", AgentType: "claude", DisplayName: "live-one", Prompt: "fix bug", ProcAlive: adapter.Alive},
		{ID: "2", AgentType: "codex", DisplayName: "stopped-active", Prompt: "rebooted work", ProcAlive: adapter.Exited},
		{ID: "3", AgentType: "claude", DisplayName: "closed-one", Prompt: "old work", ProcAlive: adapter.Exited, Closed: true},
	}
	out := m.renderTable()
	for _, want := range []string{"ACTIVE", "CLOSED", "live-one", "stopped-active", "closed-one", "fix bug"} {
		if !strings.Contains(out, want) {
			t.Fatalf("table missing %q: %s", want, out)
		}
	}
	if strings.Contains(out, "STOPPED") {
		t.Fatalf("STOPPED group should be replaced by CLOSED: %s", out)
	}
	if strings.Contains(out, "⠋") || strings.Contains(out, "💀") || strings.Contains(out, "🚀") || strings.Contains(out, "🔴") || strings.Contains(out, "🟢") {
		t.Fatalf("table should stay glyph-based, no spinner/emoji: %s", out)
	}
	if strings.Contains(out, "claude") || strings.Contains(out, "codex") {
		t.Fatalf("table should not show an agent column: %s", out)
	}
	// stopped-active belongs above CLOSED so its row appears before any closed_one entry.
	if strings.Index(out, "stopped-active") > strings.Index(out, "CLOSED") {
		t.Fatalf("active-but-stopped sessions should render under ACTIVE, before CLOSED: %s", out)
	}
}

func TestRenderTableTaskShowsLiveActivityBeforeOriginalPrompt(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{{
		ID:          "1",
		AgentType:   "claude",
		DisplayName: "live",
		Prompt:      "fix bug",
		Activity:    "editing parser.go",
		ProcAlive:   adapter.Alive,
	}}

	out := m.renderTable()
	if !strings.Contains(out, "editing parser.go") {
		t.Fatalf("task column should show current session activity: %s", out)
	}
	if strings.Contains(out, "fix bug") {
		t.Fatalf("task column should prefer live activity over original prompt: %s", out)
	}
}

func TestRenderDetailsShowsActivityOnMobileOnly(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{{
		ID:          "abc12345",
		AgentType:   "claude",
		DisplayName: "bugfix",
		Prompt:      "fix the parser",
		Activity:    "rebuilding the parser",
		Cwd:         "/tmp/repo",
		TmuxSession: "uam-claude-abc12345",
		ProcAlive:   adapter.Alive,
		State:       adapter.Active,
		CreatedAt:   time.Date(2026, time.May, 18, 7, 4, 0, 0, time.UTC),
	}}

	m.width = 56 // narrow enough that the list has no inline task column
	mobile := m.renderDetails()
	if !strings.Contains(mobile, "rebuilding the parser") {
		t.Fatalf("mobile details should show the current activity: %s", mobile)
	}

	m.width = 100
	desktop := m.renderDetails()
	if strings.Contains(desktop, "rebuilding the parser") {
		t.Fatalf("desktop details should not duplicate activity already shown in the list row: %s", desktop)
	}

	for _, out := range []string{mobile, desktop} {
		if !strings.Contains(out, "bugfix") || !strings.Contains(out, "agent: claude") {
			t.Fatalf("details should show name and agent: %s", out)
		}
		if strings.Contains(out, "id:") || strings.Contains(out, "abc12345") {
			t.Fatalf("details should not show the session id: %s", out)
		}
		if strings.Contains(out, "needs input") || strings.Contains(out, "working") {
			t.Fatalf("details should not show the state label (ACTIVE/STOPPED conveys it): %s", out)
		}
		if strings.Contains(out, "●") || strings.Contains(out, "○") || strings.Contains(out, "TMUX") || strings.Contains(out, "uam-claude-abc12345") {
			t.Fatalf("details should not show liveness markers or tmux name: %s", out)
		}
		if !strings.Contains(out, "cwd: /tmp/repo") || !strings.Contains(out, "created: May 18 07:04") {
			t.Fatalf("details should show absolute cwd and created date: %s", out)
		}
	}
}

func TestRenderTableNarrowShowsNamesWithoutInlineActivity(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.width = 42
	m.sessions = []adapter.Session{{ID: "1", DisplayName: "responsive", Activity: "running the test suite", ProcAlive: adapter.Alive}}

	out := m.renderTable()
	if !strings.Contains(out, "responsive") || !strings.Contains(out, "ACTIVE") {
		t.Fatalf("narrow table should show the session name under ACTIVE: %s", out)
	}
	if strings.Contains(out, "running the test suite") {
		t.Fatalf("narrow table rows should not repeat activity inline (the details panel shows it): %s", out)
	}
}

func TestThemeUsesAdaptiveProfessionalPaletteWithoutSelectedBackground(t *testing.T) {
	adaptiveStyles := map[string]lipgloss.TerminalColor{
		"title":   titleStyle.GetForeground(),
		"brand":   brandStyle.GetForeground(),
		"section": sectionStyle.GetForeground(),
		"task":    taskStyle.GetForeground(),
		"divider": dividerStyle.GetForeground(),
	}
	for name, color := range adaptiveStyles {
		if _, ok := color.(lipgloss.AdaptiveColor); !ok {
			t.Fatalf("%s color should auto-adapt to light/dark terminal backgrounds, got %T", name, color)
		}
	}

	if _, ok := selectedStyle.GetBackground().(lipgloss.NoColor); !ok {
		t.Fatalf("selected session should be indicated by the arrow only; background = %T", selectedStyle.GetBackground())
	}
}

func TestViewShowsUAMBrandingNameAndANSILogo(t *testing.T) {
	oldVersion := version.Override
	version.Override = "v9.9.9"
	t.Cleanup(func() { version.Override = oldVersion })

	m := NewWithDeps(nil, nil)
	m.width = 80
	m.sessions = []adapter.Session{{ID: "1", DisplayName: "clean", Cwd: "/tmp/repo", ProcAlive: adapter.Alive}}

	view := m.View()
	for _, want := range []string{
		" _   _  _   __  __",
		"| | | |/_\\ |  \\/  |",
		"Unified Agent Manager",
		"v9.9.9",
		"SELECTED",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing UAM branding %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "1 live") || strings.Contains(view, "1 dead") || strings.Contains(view, "agent fake") {
		t.Fatalf("branding should not reintroduce aggregate header stats: %s", view)
	}
}

func TestViewUsesLightDividerWithoutBorders(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.width = 80
	m.sessions = []adapter.Session{{ID: "1", DisplayName: "clean", Cwd: "/tmp/repo", ProcAlive: adapter.Alive}}

	view := m.View()
	if strings.ContainsAny(view, "╭╮╰╯│┌┐└┘") {
		t.Fatalf("view should not render box borders: %s", view)
	}
	if !strings.Contains(view, "────") {
		t.Fatalf("view should keep a light divider between details and sessions: %s", view)
	}
}

func TestViewIsCompactAndBorderlessOnNarrowScreens(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.width = 44
	m.sessions = []adapter.Session{
		{ID: "1", DisplayName: "active-one", Activity: "fixing spacing", Cwd: "/tmp/repo", ProcAlive: adapter.Alive},
		{ID: "2", DisplayName: "old-one", Cwd: "/tmp/old", ProcAlive: adapter.Exited, Closed: true},
	}

	view := m.View()
	for _, want := range []string{"SELECTED", "ACTIVE", "CLOSED", "active-one", "old-one", "fixing spacing"} {
		if !strings.Contains(view, want) {
			t.Fatalf("narrow view missing %q:\n%s", want, view)
		}
	}
	if strings.ContainsAny(view, "╭╮╰╯│┌┐└┘") {
		t.Fatalf("narrow view should stay borderless:\n%s", view)
	}
	if strings.Contains(view, "🚀") || strings.Contains(view, "🔴") || strings.Contains(view, "🟢") {
		t.Fatalf("view should avoid large emoji on mobile:\n%s", view)
	}
}
