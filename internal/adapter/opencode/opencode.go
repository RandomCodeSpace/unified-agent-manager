package opencode

import (
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
)

// opencode has no CLI flag for auto-approval / yolo: permission
// policy is read from ~/.config/opencode/config.json (or set via the
// TUI's /permission flow). Passing an unrecognised flag here (e.g.
// --auto-approve) causes yargs to print the help banner and exit 0
// instead of entering the default TUI command, which makes the pane
// die immediately and drop the user back to the uam session list.
// Therefore opencode is launched with no yolo args.
var yoloArgs []string

// sessionArgs appends opencode's `-c` (continue) flag on resume.
// opencode has no flag for presetting its session ID at dispatch,
// so uam can't resume by uam-id directly; `-c` instead picks
// opencode's most recent session for the current cwd. If multiple
// opencode rows share a cwd, all of them resume to the same
// most-recent session — a limitation of opencode's CLI surface, not
// of this wiring.
func sessionArgs(_ adapter.ResumeRequest, activity string) []string {
	if activity == "resumed" {
		return []string{"-c"}
	}
	return nil
}

func New(backend adapter.Backend) adapter.AgentAdapter {
	a := adapter.NewAgent("opencode", "OpenCode", []adapter.CommandCandidate{{Display: "opencode", Args: []string{"opencode"}}}, yoloArgs, backend)
	a.SessionArgs = sessionArgs
	a.SkipPromptOnResume = true
	return a
}
