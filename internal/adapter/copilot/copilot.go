package copilot

import (
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/mux"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/tmux"
)

func New(client *tmux.Client) adapter.AgentAdapter {
	agent := adapter.NewTmuxAgent("copilot", "GitHub Copilot", []adapter.CommandCandidate{{Display: "copilot", Args: []string{"copilot"}}}, []string{"--yolo"}, adapter.DefaultPatterns("copilot"), client)
	agent.SessionArgs = sessionArgs
	agent.SkipPromptOnResume = true
	return agent
}

// NewBackend constructs the Copilot adapter wired through mux.Backend.
// Preferred over New(*tmux.Client) for new code; both share behavior.
func NewBackend(b mux.Backend) *adapter.BackendAgent {
	agent := adapter.NewBackendAgent("copilot", "GitHub Copilot", []adapter.CommandCandidate{{Display: "copilot", Args: []string{"copilot"}}}, []string{"--yolo"}, adapter.DefaultPatterns("copilot"), b)
	agent.SessionArgs = sessionArgs
	agent.SkipPromptOnResume = true
	return agent
}

func sessionArgs(req adapter.ResumeRequest, activity string) []string {
	if req.ID == "" {
		return nil
	}
	if activity == "dispatched" {
		return []string{"--name", req.ID}
	}
	return []string{"--resume=" + req.ID}
}
