package claude

import (
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
)

// sessionArgs appends claude's `--continue` flag on resume so picking "Resume"
// on an Exited claude row reattaches to the agent's most recent session instead
// of relaunching a fresh one. claude has no flag for presetting its session ID
// at dispatch, so uam can't resume by uam-id directly; `--continue` picks
// claude's last session for the current cwd. The uam UUID is never passed.
func sessionArgs(_ adapter.ResumeRequest, activity string) []string {
	if activity == "resumed" {
		return []string{"--continue"}
	}
	return nil
}

func New(backend adapter.Backend) adapter.AgentAdapter {
	a := adapter.NewAgent("claude", "Claude Code", []adapter.CommandCandidate{{Display: "claude", Args: []string{"claude"}}}, []string{"--dangerously-skip-permissions"}, backend)
	a.SessionArgs = sessionArgs
	a.SkipPromptOnResume = true
	return a
}
