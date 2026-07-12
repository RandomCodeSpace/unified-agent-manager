package copilot

import (
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
)

func New(backend adapter.Backend) adapter.AgentAdapter {
	agent := adapter.NewAgent("copilot", "GitHub Copilot", []adapter.CommandCandidate{{Display: "copilot", Args: []string{"copilot"}}}, []string{"--yolo"}, backend)
	// copilot supports exact-session resume natively: --name seeds the new
	// session's name with the uam id at dispatch, and --resume matches it
	// exactly (case-insensitive) on resume.
	agent.SessionArgs = func(req adapter.ResumeRequest, activity string) []string {
		if req.ID == "" {
			return nil
		}
		if activity == "dispatched" {
			return []string{"--name", req.ID}
		}
		return []string{"--resume=" + req.ID}
	}
	// Record the seeded name as the provider session id so the store reflects
	// what resume will target (parity with the claude adapter).
	agent.ProviderSession = func(req adapter.ResumeRequest, activity string) string {
		if req.ID != "" {
			return req.ID
		}
		return req.ProviderSessionID
	}
	agent.ResumeKindFor = func(adapter.ResumeRequest) adapter.ResumeKind { return adapter.ResumeExact }
	agent.SkipPromptOnResume = true
	return agent
}
