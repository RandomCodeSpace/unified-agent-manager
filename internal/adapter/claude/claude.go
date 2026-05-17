package claude

import (
	"github.com/randomcodespace/unified-agent-manager/internal/adapter"
	"github.com/randomcodespace/unified-agent-manager/internal/tmux"
)

func New(client *tmux.Client) adapter.AgentAdapter {
	return adapter.NewTmuxAgent("claude", "Claude Code", []adapter.CommandCandidate{{Display: "claude", Args: []string{"claude"}}}, []string{"--dangerously-skip-permissions"}, adapter.DefaultPatterns("claude"), client)
}
