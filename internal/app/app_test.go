package app

import (
	"image/color"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/compat"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/version"
)

func TestRenderTableGroupsSessionsByStatus(t *testing.T) {
	m := NewWithDeps(nil, nil)
	// Process liveness alone determines the RUNNING/STOPPED partition.
	m.sessions = []adapter.Session{
		{ID: "1", AgentType: "claude", DisplayName: "live-one", Prompt: "fix bug", ProcAlive: adapter.Alive},
		{ID: "2", AgentType: "codex", DisplayName: "stopped-active", Prompt: "rebooted work", ProcAlive: adapter.Exited},
		{ID: "3", AgentType: "claude", DisplayName: "closed-one", Prompt: "old work", ProcAlive: adapter.Exited, Closed: true},
	}
	out := m.renderTable()
	for _, want := range []string{"RUNNING", "STOPPED", "live-one", "stopped-active", "closed-one", "fix bug"} {
		if !strings.Contains(out, want) {
			t.Fatalf("table missing %q: %s", want, out)
		}
	}
	if strings.Contains(out, "ACTIVE") || strings.Contains(out, "CLOSED") {
		t.Fatalf("legacy lifecycle groups remain: %s", out)
	}
	if strings.Contains(out, "⠋") || strings.Contains(out, "💀") || strings.Contains(out, "🚀") || strings.Contains(out, "🔴") || strings.Contains(out, "🟢") {
		t.Fatalf("table should stay glyph-based, no spinner/emoji: %s", out)
	}
	if strings.Contains(out, "claude") || strings.Contains(out, "codex") {
		t.Fatalf("table should not show an agent column: %s", out)
	}
	if strings.Index(out, "stopped-active") < strings.Index(out, "STOPPED") || strings.Index(out, "closed-one") < strings.Index(out, "STOPPED") {
		t.Fatalf("all exited sessions must render under STOPPED: %s", out)
	}
}

func TestRenderTableTaskShowsPrompt(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{{
		ID:          "1",
		AgentType:   "claude",
		DisplayName: "live",
		Prompt:      "fix bug",
		ProcAlive:   adapter.Alive,
	}}

	out := m.renderTable()
	if !strings.Contains(out, "fix bug") {
		t.Fatalf("task column should show the session prompt: %s", out)
	}
}

func TestRenderDetailsShowsPromptOnMobileOnly(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{{
		ID:          "abc12345",
		AgentType:   "claude",
		DisplayName: "bugfix",
		Prompt:      "fix the parser",
		Cwd:         "/tmp/repo",
		SessionName: "uam-claude-abc12345",
		ProcAlive:   adapter.Alive,
		State:       adapter.Active,
		CreatedAt:   time.Date(2026, time.May, 18, 7, 4, 0, 0, time.UTC),
	}}

	m.width = 56 // narrow enough that the list has no inline task column
	mobile := m.renderDetails()
	if !strings.Contains(mobile, "fix the parser") {
		t.Fatalf("mobile details should show the session prompt: %s", mobile)
	}

	m.width = 100
	desktop := m.renderDetails()
	if strings.Contains(desktop, "fix the parser") {
		t.Fatalf("desktop details should not duplicate the prompt already shown in the list row: %s", desktop)
	}

	for _, out := range []string{mobile, desktop} {
		if !strings.Contains(out, "bugfix") || !strings.Contains(out, "agent: claude") {
			t.Fatalf("details should show name and agent: %s", out)
		}
		if strings.Contains(out, "id:") || strings.Contains(out, "abc12345") {
			t.Fatalf("details should not show the session id: %s", out)
		}
		if strings.Contains(out, "needs input") || strings.Contains(out, "working") {
			t.Fatalf("details should not show the state label (RUNNING/STOPPED conveys it): %s", out)
		}
		if strings.Contains(out, "●") || strings.Contains(out, "○") || strings.Contains(out, "TMUX") || strings.Contains(out, "uam-claude-abc12345") {
			t.Fatalf("details should not show liveness markers or tmux name: %s", out)
		}
		if !strings.Contains(out, "cwd: /tmp/repo") || !strings.Contains(out, "created: May 18 07:04") {
			t.Fatalf("details should show absolute cwd and created date: %s", out)
		}
	}
}

func TestRenderTableNarrowShowsNamesWithoutInlineTask(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.width = 42
	m.sessions = []adapter.Session{{ID: "1", DisplayName: "responsive", Prompt: "running the test suite", ProcAlive: adapter.Alive}}

	out := m.renderTable()
	if !strings.Contains(out, "responsive") || !strings.Contains(out, "RUNNING") {
		t.Fatalf("narrow table should show the session name under RUNNING: %s", out)
	}
	if strings.Contains(out, "running the test suite") {
		t.Fatalf("narrow table rows should not repeat the task inline (the details panel shows it): %s", out)
	}
}

func TestThemeUsesAdaptiveProfessionalPaletteWithoutSelectedBackground(t *testing.T) {
	adaptiveStyles := map[string]color.Color{
		"title":   titleStyle.GetForeground(),
		"brand":   brandStyle.GetForeground(),
		"section": sectionStyle.GetForeground(),
		"task":    taskStyle.GetForeground(),
		"divider": dividerStyle.GetForeground(),
	}
	for name, color := range adaptiveStyles {
		if _, ok := color.(compat.AdaptiveColor); !ok {
			t.Fatalf("%s color should auto-adapt to light/dark terminal backgrounds, got %T", name, color)
		}
	}

	if _, ok := selectedStyle.GetBackground().(lipgloss.NoColor); !ok {
		t.Fatalf("selected session should be indicated by the arrow only; background = %T", selectedStyle.GetBackground())
	}
}

func TestViewShowsAgentsBrandingAndDashboard(t *testing.T) {
	oldVersion := version.Override
	version.Override = "v9.9.9"
	t.Cleanup(func() { version.Override = oldVersion })

	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{{ID: "1", DisplayName: "clean", Cwd: "/tmp/repo", ProcAlive: adapter.Alive}}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 120, Height: 30})

	view := m.View().Content
	for _, want := range []string{
		"UAM",
		"Agents",
		"v9.9.9",
		"clean",
		"Running",
		"Attach",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing UAM branding %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "1 live") || strings.Contains(view, "1 dead") || strings.Contains(view, "agent fake") || strings.Contains(view, "DEPARTURES") {
		t.Fatalf("branding should not reintroduce aggregate header stats: %s", view)
	}
	if strings.Contains(view, uamANSILogo) {
		t.Fatalf("responsive dashboard should not spend rows on the legacy ASCII logo: %s", view)
	}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 44, Height: 12})
	if compact := m.View().Content; !strings.Contains(compact, "UAM") || !strings.Contains(compact, "v9.9.9") || !strings.Contains(compact, "Agents") {
		t.Fatalf("compact dashboard should keep UAM, version, and Agents identity: %s", compact)
	}
}

// TestViewUsesBorderlessSessionsRule pins the borderless dashboard and literal
// session count.
func TestViewUsesBorderlessSessionsRule(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{{ID: "1", DisplayName: "clean", Cwd: "/tmp/repo", ProcAlive: adapter.Alive}}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 30})

	view := m.View().Content
	if !strings.Contains(view, "─") || !strings.Contains(view, "1/1 sessions") {
		t.Fatalf("view should frame the roster with rules and carry the session count: %s", view)
	}
	assertBorderless(t, view)
}

func TestViewIsInformationRichAndBoundedOnNarrowScreens(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{
		{ID: "1", AgentType: "codex", DisplayName: "active-one", Prompt: "fixing spacing", Cwd: "/tmp/repo", ProcAlive: adapter.Alive},
		{ID: "2", AgentType: "claude", DisplayName: "old-one", Cwd: "/tmp/old", ProcAlive: adapter.Exited, Closed: true},
	}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 44, Height: 12})

	view := m.View().Content
	for _, want := range []string{"active-one", "old-one", "Running", "Stopped", "Attach", "Resume", "codex"} {
		if !strings.Contains(view, want) {
			t.Fatalf("narrow view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "🚀") || strings.Contains(view, "🔴") || strings.Contains(view, "🟢") {
		t.Fatalf("view should avoid large emoji on mobile:\n%s", view)
	}
}
