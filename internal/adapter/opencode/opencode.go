package opencode

import (
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/tmux"
)

func New(client *tmux.Client) adapter.AgentAdapter {
	return adapter.NewTmuxAgent("opencode", "OpenCode", []adapter.CommandCandidate{{Display: "opencode", Args: []string{"opencode"}}}, []string{"--auto-approve"}, adapter.DefaultPatterns("opencode"), client)
}
