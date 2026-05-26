package claude

import (
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/mux"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/tmux"
)

func New(client *tmux.Client) adapter.AgentAdapter {
	return adapter.NewTmuxAgent("claude", "Claude Code", []adapter.CommandCandidate{{Display: "claude", Args: []string{"claude"}}}, []string{"--dangerously-skip-permissions"}, adapter.DefaultPatterns("claude"), client)
}

// NewBackend constructs the Claude adapter wired through mux.Backend.
// Preferred over New(*tmux.Client) for new code; both share behavior.
func NewBackend(b mux.Backend) *adapter.BackendAgent {
	return adapter.NewBackendAgent("claude", "Claude Code", []adapter.CommandCandidate{{Display: "claude", Args: []string{"claude"}}}, []string{"--dangerously-skip-permissions"}, adapter.DefaultPatterns("claude"), b)
}
