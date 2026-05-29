// Package agents is the single source of truth for the set of agent adapters
// uam manages. Both the TUI entrypoint (internal/app) and the CLI service
// wiring (internal/cli) build their registry from Default so the two can never
// drift — previously each hand-maintained its own list and app.New silently
// omitted hermes (F14). It is a leaf package: the providers import
// internal/adapter and internal/tmux, and nothing imports back into agents, so
// there is no import cycle.
package agents

import (
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/claude"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/codex"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/copilot"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/hermes"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/opencode"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/tmux"
)

// Default returns every supported agent adapter, built against client. The
// returned slice is the pre-availability list (it is not LookPath-filtered);
// callers pass it to adapter.NewRegistry, which probes Available() and hides
// the ones whose CLI is not installed.
func Default(client *tmux.Client) []adapter.AgentAdapter {
	return []adapter.AgentAdapter{
		claude.New(client),
		codex.New(client),
		copilot.New(client),
		hermes.New(client),
		opencode.New(client),
	}
}
