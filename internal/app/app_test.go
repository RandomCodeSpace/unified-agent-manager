package app

import (
	"strings"
	"testing"

	"github.com/randomcodespace/unified-agent-manager/internal/adapter"
)

func TestRenderRowsGroupsByState(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{{ID: "1", AgentType: "claude", DisplayName: "needs", State: adapter.NeedsInput}, {ID: "2", AgentType: "codex", DisplayName: "done", State: adapter.Completed}}
	out := m.renderTable()
	if !strings.Contains(out, "NEEDS INPUT") || !strings.Contains(out, "COMPLETED") {
		t.Fatalf("missing groups: %s", out)
	}
}
