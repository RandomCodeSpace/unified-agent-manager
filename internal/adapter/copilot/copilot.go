package copilot

import (
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/tmux"
)

func New(client *tmux.Client) adapter.AgentAdapter {
	return adapter.NewTmuxAgent("copilot", "GitHub Copilot", []adapter.CommandCandidate{{Display: "copilot", Args: []string{"copilot"}}, {Display: "gh copilot", Args: []string{"gh", "copilot"}}}, []string{"--autopilot"}, adapter.DefaultPatterns("copilot"), client)
}
