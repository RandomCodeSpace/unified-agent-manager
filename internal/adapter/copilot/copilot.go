package copilot

import (
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/tmux"
)

func New(client *tmux.Client) adapter.AgentAdapter {
	agent := adapter.NewTmuxAgent("copilot", "GitHub Copilot", []adapter.CommandCandidate{{Display: "copilot", Args: []string{"copilot"}}}, []string{"--yolo"}, adapter.DefaultPatterns("copilot"), client)
	agent.SessionArgs = func(req adapter.ResumeRequest, activity string) []string {
		if req.ID == "" {
			return nil
		}
		if activity == "dispatched" {
			return []string{"--name", req.ID}
		}
		return []string{"--resume=" + req.ID}
	}
	agent.SkipPromptOnResume = true
	return agent
}
