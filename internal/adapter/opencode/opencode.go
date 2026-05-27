package opencode

import (
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/tmux"
)

// opencode has no CLI flag for auto-approval / yolo: permission
// policy is read from ~/.config/opencode/config.json (or set via the
// TUI's /permission flow). Passing an unrecognised flag here (e.g.
// --auto-approve) causes yargs to print the help banner and exit 0
// instead of entering the default TUI command, which makes the pane
// die immediately and drop the user back to the uam session list.
// Therefore opencode is launched with no yolo args.
var yoloArgs []string

func New(client *tmux.Client) adapter.AgentAdapter {
	return adapter.NewTmuxAgent("opencode", "OpenCode", []adapter.CommandCandidate{{Display: "opencode", Args: []string{"opencode"}}}, yoloArgs, adapter.DefaultPatterns("opencode"), client)
}
