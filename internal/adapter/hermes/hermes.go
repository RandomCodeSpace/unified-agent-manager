package hermes

import (
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/tmux"
)

func New(client *tmux.Client) adapter.AgentAdapter {
	return adapter.NewTmuxAgent("hermes", "Hermes Agent", []adapter.CommandCandidate{{Display: "hermes", Args: []string{"hermes", "--tui"}}}, []string{"--yolo"}, adapter.DefaultPatterns("hermes"), client)
}
