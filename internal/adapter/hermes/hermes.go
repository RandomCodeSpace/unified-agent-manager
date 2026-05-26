package hermes

import (
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/mux"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/tmux"
)

func New(client *tmux.Client) adapter.AgentAdapter {
	return adapter.NewTmuxAgent("hermes", "Hermes Agent", []adapter.CommandCandidate{{Display: "hermes", Args: []string{"hermes", "--tui"}}}, []string{"--yolo"}, adapter.DefaultPatterns("hermes"), client)
}

// NewBackend constructs the Hermes adapter wired through mux.Backend.
// Preferred over New(*tmux.Client) for new code; both share behavior.
func NewBackend(b mux.Backend) *adapter.BackendAgent {
	return adapter.NewBackendAgent("hermes", "Hermes Agent", []adapter.CommandCandidate{{Display: "hermes", Args: []string{"hermes", "--tui"}}}, []string{"--yolo"}, adapter.DefaultPatterns("hermes"), b)
}
