package app

import (
	"strings"
	"testing"

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
