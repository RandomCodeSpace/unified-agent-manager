package codex

import (
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/mux"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/tmux"
)

func New(client *tmux.Client) adapter.AgentAdapter {
	return adapter.NewTmuxAgent("codex", "OpenAI Codex", []adapter.CommandCandidate{{Display: "codex", Args: []string{"codex"}}}, []string{"--sandbox", "danger-full-access"}, adapter.DefaultPatterns("codex"), client)
}

// NewBackend constructs the Codex adapter wired through mux.Backend.
// Preferred over New(*tmux.Client) for new code; both share behavior.
func NewBackend(b mux.Backend) *adapter.BackendAgent {
	return adapter.NewBackendAgent("codex", "OpenAI Codex", []adapter.CommandCandidate{{Display: "codex", Args: []string{"codex"}}}, []string{"--sandbox", "danger-full-access"}, adapter.DefaultPatterns("codex"), b)
}
