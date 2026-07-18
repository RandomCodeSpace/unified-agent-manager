package codex

import (
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
)

// sessionArgs keeps Codex on the terminal's primary screen so its transcript
// remains in scrollback through UAM. On resume it also appends `resume --last`;
// Codex has no flag for presetting its session ID, so UAM cannot resume by UAM
// ID directly. The UAM UUID is never passed.
func sessionArgs(_ adapter.ResumeRequest, activity string) []string {
	if activity == "resumed" {
		return []string{"--no-alt-screen", "resume", "--last"}
	}
	return []string{"--no-alt-screen"}
}

func New(backend adapter.Backend) adapter.AgentAdapter {
	a := adapter.NewAgent("codex", "OpenAI Codex", []adapter.CommandCandidate{{Display: "codex", Args: []string{"codex"}}}, []string{"--sandbox", "danger-full-access"}, backend)
	a.SessionArgs = sessionArgs
	a.ResumeKindFor = func(adapter.ResumeRequest) adapter.ResumeKind { return adapter.ResumeHeuristic }
	a.SkipPromptOnResume = true
	return a
}
