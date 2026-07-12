package codex

import (
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
)

// sessionArgs appends codex's `resume --last` subcommand on resume so picking
// "Resume" on an Exited codex row reattaches to the agent's most recent session
// instead of relaunching a fresh one. codex has no flag for presetting its
// session ID at dispatch, so uam can't resume by uam-id directly; `resume
// --last` picks codex's last session. The uam UUID is never passed.
func sessionArgs(_ adapter.ResumeRequest, activity string) []string {
	if activity == "resumed" {
		return []string{"resume", "--last"}
	}
	return nil
}

func New(backend adapter.Backend) adapter.AgentAdapter {
	a := adapter.NewAgent("codex", "OpenAI Codex", []adapter.CommandCandidate{{Display: "codex", Args: []string{"codex"}}}, []string{"--sandbox", "danger-full-access"}, backend)
	a.SessionArgs = sessionArgs
	a.ResumeKindFor = func(adapter.ResumeRequest) adapter.ResumeKind { return adapter.ResumeHeuristic }
	a.SkipPromptOnResume = true
	return a
}
