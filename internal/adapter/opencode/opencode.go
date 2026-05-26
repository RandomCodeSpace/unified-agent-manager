package opencode

import (
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/mux"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/tmux"
)

func New(client *tmux.Client) adapter.AgentAdapter {
	return adapter.NewTmuxAgent("opencode", "OpenCode", []adapter.CommandCandidate{{Display: "opencode", Args: []string{"opencode"}}}, []string{"--auto-approve"}, adapter.DefaultPatterns("opencode"), client)
}

// NewBackend constructs the OpenCode adapter wired through mux.Backend.
// Preferred over New(*tmux.Client) for new code; both share behavior.
func NewBackend(b mux.Backend) *adapter.BackendAgent {
	return adapter.NewBackendAgent("opencode", "OpenCode", []adapter.CommandCandidate{{Display: "opencode", Args: []string{"opencode"}}}, []string{"--auto-approve"}, adapter.DefaultPatterns("opencode"), b)
}
