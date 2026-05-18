package app

import (
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/version"
	"github.com/charmbracelet/lipgloss"
)

func TestRenderRowsShowsTmuxStatusNameAndPrompt(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{{ID: "1", AgentType: "claude", DisplayName: "live", Prompt: "fix bug", ProcAlive: adapter.Alive}, {ID: "2", AgentType: "codex", DisplayName: "dead", Prompt: "old work", ProcAlive: adapter.Exited}}
	out := m.renderTable()
	if !strings.Contains(out, "SESSIONS") || !strings.Contains(out, "●") || !strings.Contains(out, "○") || !strings.Contains(out, "fix bug") {
		t.Fatalf("missing tmux/name/prompt rows: %s", out)
	}
	if strings.Contains(out, "⠋") || strings.Contains(out, "💀") || strings.Contains(out, "🚀") || strings.Contains(out, "🔴") || strings.Contains(out, "🟢") {
		t.Fatalf("table should use compact one-cell static status dots: %s", out)
	}
	if strings.Contains(out, "NEEDS INPUT") || strings.Contains(out, "COMPLETED") || strings.Contains(out, "claude") || strings.Contains(out, "codex") {
		t.Fatalf("table should not show semantic state or agent columns: %s", out)
	}
}

func TestRenderDetailsHidesPromptAndTmuxNameWithCreatedSeparately(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{{
		ID:          "abc12345",
		AgentType:   "claude",
		DisplayName: "bugfix",
		Prompt:      "fix the parser",
		Cwd:         "/tmp/repo",
		TmuxSession: "uam-claude-abc12345",
		ProcAlive:   adapter.Alive,
		CreatedAt:   time.Date(2026, time.May, 18, 7, 4, 0, 0, time.UTC),
	}}

	out := m.renderDetails()
	if !strings.Contains(out, "●") || strings.Contains(out, "TMUX: LIVE") || strings.Contains(out, "TMUX: DEAD") {
		t.Fatalf("details should show compact marker-only liveness: %s", out)
	}
	if strings.Contains(out, "fix the parser") || strings.Contains(out, "prompt:") {
		t.Fatalf("details should not show prompt text: %s", out)
	}
	if strings.Contains(out, "uam-claude-abc12345") || strings.Contains(out, "tmux:") {
		t.Fatalf("details should not show tmux session name: %s", out)
	}
	if !strings.Contains(out, "\ncreated: May 18 07:04") {
		t.Fatalf("created date should be on its own line: %s", out)
	}
}

func TestRenderTableIsResponsiveOnNarrowWidths(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.width = 42
	m.sessions = []adapter.Session{{ID: "1", DisplayName: "responsive", Prompt: "this task is intentionally long", ProcAlive: adapter.Alive}}

	out := m.renderTable()
	if strings.Contains(out, "TASK") || strings.Contains(out, "this task") {
		t.Fatalf("narrow table should hide task column: %s", out)
	}
	if !strings.Contains(out, "responsive") || !strings.Contains(out, "SESSIONS") {
		t.Fatalf("narrow table should still show session names: %s", out)
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
		" _   _  _   __  __ ",
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

func TestViewUsesCompactVerticalSpacingAndSmallMarkers(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.width = 44
	m.sessions = []adapter.Session{
		{ID: "1", DisplayName: "active", Prompt: "fix spacing", Cwd: "/tmp/repo", ProcAlive: adapter.Alive},
		{ID: "2", DisplayName: "old", Prompt: "archive", Cwd: "/tmp/old", ProcAlive: adapter.Exited},
	}

	view := m.View()
	for _, want := range []string{
		"SELECTED\n●",
		"active\nagent:",
		"SESSIONS\n",
		"NAME                                \n›",
		"old",
		"\n\ncommand",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing compact spacing/marker %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "🚀") || strings.Contains(view, "🔴") || strings.Contains(view, "\n\n●") || strings.Contains(view, "\n\nagent:") || strings.Contains(view, "SESSIONS\n\n") {
		t.Fatalf("view should avoid large emoji and extra vertical spacing on mobile:\n%s", view)
	}
}
