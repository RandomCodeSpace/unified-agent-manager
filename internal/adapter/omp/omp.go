package omp

import (
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/tmux"
)

// omp (Oh My Pi, github.com/can1357/oh-my-pi) launches bare: a plain `omp`
// with no subcommand opens its TUI, the default surface (the other modes are
// explicit — `omp -p`, `omp --mode rpc`, `omp acp`). Unlike hermes/opencode,
// omp does expose a real auto-approve flag (`--auto-approve`, per `omp
// --help`), so it is wired as the yolo arg and appended in non-safe mode so
// dispatched sessions skip tool-call approval prompts, matching
// claude/codex/copilot. Model auth is an in-TUI `/login` OAuth flow, not an
// env var.
var yoloArgs = []string{"--auto-approve"}

// sessionArgs appends omp's `-c`/`--continue` flag on resume so picking
// "Resume" on an Exited omp row continues its most recent session instead of
// launching a fresh one (same approach as opencode). omp has no flag to preset
// its session ID at dispatch, so uam can't resume by uam-id directly; `-c`
// continues omp's last session. The uam UUID is never passed.
func sessionArgs(_ adapter.ResumeRequest, activity string) []string {
	if activity == "resumed" {
		return []string{"-c"}
	}
	return nil
}

func New(client *tmux.Client) adapter.AgentAdapter {
	a := adapter.NewTmuxAgent("omp", "Oh My Pi", []adapter.CommandCandidate{{Display: "omp", Args: []string{"omp"}}}, yoloArgs, client)
	a.SessionArgs = sessionArgs
	a.SkipPromptOnResume = true
	return a
}
