package app

import (
	"strings"
	"testing"
	"time"

	"github.com/randomcodespace/unified-agent-manager/internal/adapter"
)

func TestRenderRowsShowsTmuxStatusNameAndPrompt(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{{ID: "1", AgentType: "claude", DisplayName: "live", Prompt: "fix bug", ProcAlive: adapter.Alive}, {ID: "2", AgentType: "codex", DisplayName: "dead", Prompt: "old work", ProcAlive: adapter.Exited}}
	out := m.renderTable()
	if !strings.Contains(out, "SESSIONS") || !strings.Contains(out, "⠋") || !strings.Contains(out, "💀") || !strings.Contains(out, "fix bug") {
		t.Fatalf("missing tmux/name/prompt rows: %s", out)
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
	if strings.Contains(out, "fix the parser") || strings.Contains(out, "prompt:") {
		t.Fatalf("details should not show prompt text: %s", out)
	}
	if strings.Contains(out, "uam-claude-abc12345") || strings.Contains(out, "tmux:") {
		t.Fatalf("details should not show tmux session name: %s", out)
	}
	if !strings.Contains(out, "\n│ created: May 18 07:04") {
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
