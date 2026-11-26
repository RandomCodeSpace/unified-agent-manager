package copilot

import (
	"context"
	"time"

	"github.com/microsoft/copilot-sdk/go/v2/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

type sandboxSession interface {
	SandboxState(context.Context) (*rpc.SessionSandboxGetStateResult, error)
}

func (a sdkSessionAdapter) SandboxState(ctx context.Context) (*rpc.SessionSandboxGetStateResult, error) {
	return a.s.RPC.Sandbox.GetState(ctx)
}

// sandboxSeen is the (incarnation, revision) of the last state published.
type sandboxSeen struct {
	known                 bool
	incarnation, revision int64
}

func sandboxState(config any, source string, disabled bool, override *bool) agentapi.SandboxState {
	fields, _ := config.(map[string]any)
	enabled, _ := fields["enabled"].(bool)
	return agentapi.SandboxState{
		Enabled:      enabled,
		OffForThread: disabled || override != nil && !*override,
		Source:       clip(displaytext.Sanitize(source), 64),
	}
}

// publishSandboxLocked publishes a state newer than the last one published;
// a read that raced a change event cannot undo it.
func (c *conversation) publishSandboxLocked(incarnation, revision int64, state agentapi.SandboxState) {
	last := c.sandbox
	if last.known && (incarnation < last.incarnation || incarnation == last.incarnation && revision <= last.revision) {
		return
	}
	c.sandbox = sandboxSeen{known: true, incarnation: incarnation, revision: revision}
	c.emitLocked(agentapi.Event{Kind: agentapi.EventSandbox, Sandbox: &state})
}

// readSandbox reads the state once after an open; changes arrive as
// session.sandbox_state_changed.
func (c *conversation) readSandbox(ctx context.Context, runtime sandboxSession) {
	defer log.Recover("copilot sandbox read", &c.mu, &c.p.mu)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	res, err := runtime.SandboxState(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	if err != nil || res == nil {
		// Older CLIs lack the method: the state is unknown, so nothing shows.
		if !c.sandbox.known {
			c.emitLocked(agentapi.Event{Kind: agentapi.EventSandbox})
		}
		return
	}
	c.publishSandboxLocked(res.Incarnation, res.Revision, sandboxState(res.Config, res.ConfigSource, res.DisabledForSession, res.SessionOverride))
}
