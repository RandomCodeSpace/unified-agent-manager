package copilot

import (
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
)

func New(backend adapter.Backend) adapter.AgentAdapter {
	agent := adapter.NewAgent("copilot", "GitHub Copilot", []adapter.CommandCandidate{{Display: "copilot", Args: []string{"copilot"}}}, []string{"--yolo"}, backend)
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
