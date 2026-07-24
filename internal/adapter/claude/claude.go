package claude

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
)

// sessionArgs picks claude's session flags per activity.
//
// Dispatch seeds claude's own session id with the uam UUID (`--session-id`)
// when the installed claude supports it, so the provider session is
// addressable later. Resume then targets that exact session (`--resume <id>`)
// instead of `--continue`, whose "most recent conversation in this cwd"
// heuristic resumes the WRONG conversation when several uam sessions share a
// directory. Records without a seeded id (older claude, pre-upgrade sessions)
// keep the `--continue` fallback.
func sessionArgs(req adapter.ResumeRequest, activity string) []string {
	if activity == "resumed" {
		if req.ProviderSessionID != "" {
			return []string{"--resume", req.ProviderSessionID}
		}
		return []string{"--continue"}
	}
	if req.ID != "" && supportsSessionID() {
		return []string{"--session-id", req.ID}
	}
	return nil
}

// providerSession reports the provider-side session id a launch will use, for
// persistence: the seeded uam UUID on dispatch, or the id an exact resume
// re-targets. Empty when the installed claude cannot seed ids.
func providerSession(req adapter.ResumeRequest, activity string) string {
	if activity == "dispatched" && req.ID != "" && supportsSessionID() {
		return req.ID
	}
	return req.ProviderSessionID
}

// sessionIDSupport caches, per resolved claude binary, whether its --help
// advertises --session-id. Older claude releases reject unknown flags at
// startup, so seeding must be probed, not assumed. Keyed by path (not a
// sync.Once) so a PATH change — or a test pointing at a different fake —
// re-probes.
var sessionIDSupport sync.Map // map[string]bool

func supportsSessionID() bool {
	path, err := exec.LookPath("claude")
	if err != nil {
		return false
	}
	if v, ok := sessionIDSupport.Load(path); ok {
		return v.(bool)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, path, "--help").CombinedOutput() // #nosec G204 -- path resolved via LookPath for the fixed name "claude".
	supported := strings.Contains(string(out), "--session-id")
	sessionIDSupport.Store(path, supported)
	return supported
}

func New(backend adapter.Backend) adapter.AgentAdapter {
	a := adapter.NewAgent("claude", "Claude Code", []adapter.CommandCandidate{{Display: "claude", Args: []string{"claude"}}}, []string{"--dangerously-skip-permissions"}, backend)
	a.Terminal = adapter.ProviderTerminalPolicy{Identity: adapter.ProviderClaude, OuterScreen: adapter.OuterScreenUAM, KeyProtocol: adapter.KeyProtocolNative}
	a.SessionArgs = sessionArgs
	a.ProviderSession = providerSession
	a.SkipPromptOnResume = true
	return a
}
