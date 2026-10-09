package copilot

import (
	"context"
	"errors"
	"fmt"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// selectAgent selects the custom agent id, the stable ID native discovery
// reports. A failure is ErrAgentUnavailable: the caller never goes on with
// another agent.
func selectAgent(ctx context.Context, session sdkSession, id string) error {
	selected, err := session.SelectAgent(ctx, id)
	if err == nil && (selected == nil || (selected.ID != id && selected.Name != id)) {
		err = errors.New("the runtime selected a different agent")
	}
	if err != nil {
		return fmt.Errorf("%w: %q: %s", agentapi.ErrAgentUnavailable, id, rpcText(err))
	}
	return nil
}

// abandonOpen disconnects a session an open could not finish. A session
// this open created is deleted too: it has no turn and no Task records it.
func (p *webProvider) abandonOpen(ctx context.Context, client sdkClient, sess sdkSession, created bool) {
	id := sess.ID()
	if err := sess.Disconnect(); err != nil {
		p.poke()
		return
	}
	if err := p.recordUsageSession(client, id, false); err != nil {
		return
	}
	if created {
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), titleDeleteTimeout)
		defer cancel()
		_ = client.DeleteSession(dctx, id)
	}
}

// SelectAgent selects id for the next turns, or the default agent for "".
// It waits for a tool proof in flight, since an agent change builds the
// tool set again, and the next message proves the uam tools again under
// the new agent.
func (c *conversation) SelectAgent(ctx context.Context, id string) error {
	c.control.Lock()
	defer c.control.Unlock()
	c.mu.Lock()
	closed, running := c.closed, c.turnRunning
	c.mu.Unlock()
	if closed {
		return agentapi.ErrClosed
	}
	if running {
		return agentapi.ErrBusy
	}
	if c.tools != nil {
		c.tools.proof.Lock()
		defer c.tools.proof.Unlock()
	}
	var err error
	if id == "" {
		if err = c.sess.DeselectAgent(ctx); err != nil {
			err = fmt.Errorf("return to the default agent: %s", rpcText(err))
		}
	} else {
		err = selectAgent(ctx, c.sess, id)
	}
	if c.tools != nil {
		c.tools.invalidate()
	}
	if err != nil {
		c.p.poke()
		return err
	}
	c.agent = id
	return nil
}

// reselectAgent selects the conversation's custom agent again after a
// configuration reload, which reads agent definitions again. An agent that
// is gone fails with ErrAgentUnavailable rather than leaving the next turn
// to another agent. The caller holds control.
func (c *conversation) reselectAgent(ctx context.Context) error {
	if c.agent == "" {
		return nil
	}
	return selectAgent(ctx, c.sess, c.agent)
}
