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
	"github.com/charmbracelet/x/ansi"
)

func TestLedgerShowsIdentityWithoutInternals(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.now = func() time.Time { return time.Date(2026, time.May, 18, 12, 0, 0, 0, time.UTC) }
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
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 100, Height: 24})
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"bugfix", "claude", "Running", "created today 07:04", "/tmp/repo"} {
		if !strings.Contains(view, want) {
			t.Fatalf("ledger missing %q:\n%s", want, view)
		}
	}
	for _, banned := range []string{"TMUX", "uam-claude-abc12345", "needs input", "working", "fix the parser"} {
		if strings.Contains(view, banned) {
			t.Fatalf("ledger leaked %q:\n%s", banned, view)
		}
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
		"v9.9.9",
		"clean",
		"Running",
		"Attach",
		"new session",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing UAM branding %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "1 live") || strings.Contains(view, "1 dead") || strings.Contains(view, "agent fake") || strings.Contains(view, "DEPARTURES") {
		t.Fatalf("branding should not reintroduce aggregate header stats: %s", view)
	}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 44, Height: 12})
	if compact := m.View().Content; !strings.Contains(compact, "UAM") || !strings.Contains(compact, "v9.9.9") {
		t.Fatalf("compact dashboard should keep UAM and version identity: %s", compact)
	}
}

// TestViewUsesBorderlessSessionsRule pins the borderless dashboard and literal
// session count.
func TestViewUsesBorderlessSessionsRule(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{{ID: "1", DisplayName: "clean", Cwd: "/tmp/repo", ProcAlive: adapter.Alive}}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 30})

	view := m.View().Content
	if !strings.Contains(view, "─") || !strings.Contains(view, "1-1 of 1") {
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
	for _, want := range []string{"active-one", "old-one", "Running", "Stopped", "Attach", "codex", "/tmp/old"} {
		if !strings.Contains(view, want) {
			t.Fatalf("narrow view missing %q:\n%s", want, view)
		}
	}
	m.selected = 1
	if stopped := m.View().Content; !strings.Contains(stopped, "Resume") {
		t.Fatalf("narrow ledger for a stopped session missing Resume:\n%s", stopped)
	}
	if strings.Contains(view, "🚀") || strings.Contains(view, "🔴") || strings.Contains(view, "🟢") {
		t.Fatalf("view should avoid large emoji on mobile:\n%s", view)
	}
}
