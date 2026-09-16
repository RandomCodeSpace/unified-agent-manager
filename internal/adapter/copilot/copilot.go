package copilot

import (
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
)

func New(backend adapter.Backend) adapter.AgentAdapter {
	agent := adapter.NewAgent("copilot", "GitHub Copilot", []adapter.CommandCandidate{{Display: "copilot", Args: []string{"copilot"}}}, []string{"--yolo"}, backend)
	// Copilot binds bare left arrow to its session sidebar; the attach
	// client's quick detach is Ctrl+Left, which copilot does not bind, so the
	// gesture stays at its default.
	agent.Terminal = adapter.ProviderTerminalPolicy{Identity: adapter.ProviderCopilot, OuterScreen: adapter.OuterScreenUAM, KeyProtocol: adapter.KeyProtocolNative}
	// copilot supports exact-session resume natively. The uam id is a UUID,
	// so --session-id pins the new Copilot session's primary id to it at
	// dispatch and --resume then matches by session id — the most stable
	// lookup the CLI offers. --name seeds the same value as the session name
	// for display and as a fallback match: sessions dispatched by older uam
	// versions carry the uam id only as a name, and --resume falls back to
	// exact (case-insensitive) name matching for them.
	agent.SessionArgs = func(req adapter.ResumeRequest, activity string) []string {
		if req.ID == "" {
			return nil
		}
		if activity == "dispatched" {
			return []string{"--session-id", req.ID, "--name", req.ID}
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
