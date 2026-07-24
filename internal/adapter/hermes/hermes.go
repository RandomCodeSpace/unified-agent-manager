package hermes

import (
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
)

// hermes is launched bare. `--tui` fails to start the agent, and like
// opencode hermes has no recognised auto-approve/yolo flag — passing an
// unknown flag makes the pane exit immediately and drop the user back to the
// session list. Launch as plain `hermes` until a real flag is confirmed.
var yoloArgs []string

func New(backend adapter.Backend) adapter.AgentAdapter {
	agent := adapter.NewAgent("hermes", "Hermes Agent", []adapter.CommandCandidate{{Display: "hermes", Args: []string{"hermes"}}}, yoloArgs, backend)
	agent.Terminal = adapter.ProviderTerminalPolicy{Identity: adapter.ProviderHermes, OuterScreen: adapter.OuterScreenUAM, KeyProtocol: adapter.KeyProtocolNative}
	return agent
}
