package copilot

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// modelSelection is a Task's complete model selection, as SetModel takes it.
type modelSelection struct{ model, effort, contextSize string }

// errModelNotKept marks an agent change after which the Task's model could
// not be restored: the conversation then ends, so no turn runs on a model
// the Task did not choose.
var errModelNotKept = errors.New("the task's model could not be kept")

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

// selectAgentKeepingModel is every agent change: it selects id, or the
// default agent for "", and then restores the Task's model selection, since
// selecting an agent applies the agent's own authored model and effort
// (CLI ModelChangeSource "agent"). Without a known Task selection the
// runtime's selection from before the change is restored if the change
// moved it. A failed restore wraps ErrAgentUnavailable and errModelNotKept.
func (c *conversation) selectAgentKeepingModel(ctx context.Context, id string) error {
	c.mu.Lock()
	want := c.selection
	c.mu.Unlock()
	var before *rpc.CurrentModel
	if want == nil {
		var err error
		if before, err = c.sess.CurrentModel(ctx); err != nil {
			return fmt.Errorf("%w: %q: read the model before the agent change: %s", agentapi.ErrAgentUnavailable, id, rpcText(err))
		}
	}
	if id == "" {
		if err := c.sess.DeselectAgent(ctx); err != nil {
			return fmt.Errorf("return to the default agent: %s", rpcText(err))
		}
	} else if err := selectAgent(ctx, c.sess, id); err != nil {
		return err
	}
	if want == nil {
		after, err := c.sess.CurrentModel(ctx)
		if err == nil && sameModel(before, after) {
			c.setAgent(id)
			return nil
		}
		if err != nil || before == nil || before.ModelID == nil {
			return modelNotKept(id, errors.New("the model in use before the agent change is unknown"))
		}
		want = &modelSelection{model: *before.ModelID, effort: deref(before.ReasoningEffort), contextSize: "default"}
		if before.ContextTier != nil {
			want.contextSize = cmp.Or(string(*before.ContextTier), "default")
		}
	}
	if err := c.applyModel(ctx, want.model, want.effort, want.contextSize); err != nil {
		return modelNotKept(id, err)
	}
	c.setAgent(id)
	return nil
}

func modelNotKept(id string, err error) error {
	return fmt.Errorf("%w: %q: %w: %s", agentapi.ErrAgentUnavailable, id, errModelNotKept, errText(err))
}

func sameModel(a, b *rpc.CurrentModel) bool {
	if a == nil || b == nil {
		return a == b
	}
	return deref(a.ModelID) == deref(b.ModelID) && deref(a.ReasoningEffort) == deref(b.ReasoningEffort) && derefTier(a.ContextTier) == derefTier(b.ContextTier)
}

func derefTier(t *rpc.ContextTier) string {
	if t == nil {
		return ""
	}
	return string(*t)
}

func (c *conversation) setAgent(id string) {
	c.mu.Lock()
	c.agent = id
	c.mu.Unlock()
}

func (c *conversation) agentSelected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.agent != ""
}

// endIfModelNotKept ends the conversation after an agent change whose
// model restore failed.
func (c *conversation) endIfModelNotKept(ctx context.Context, err error) error {
	if errors.Is(err, errModelNotKept) {
		_ = c.selectionUncertain(ctx, err)
	}
	return err
}

// abandonOpen closes a conversation an open could not finish. A session
// this open created is deleted too: it has no turn and no Task records it.
func (p *webProvider) abandonOpen(ctx context.Context, client sdkClient, c *conversation, created bool) {
	// The open's deadline may be what failed it; the cleanup still runs.
	ctx = context.WithoutCancel(ctx)
	if err := c.Close(ctx); err != nil || !created {
		return
	}
	dctx, cancel := context.WithTimeout(ctx, titleDeleteTimeout)
	defer cancel()
	_ = client.DeleteSession(dctx, c.id)
}

// rebuildTools builds the tool set again for a stale catalog by changing
// to the agent the session already uses (rebuildTools in declaration.go),
// keeping the Task's model when that is a custom agent. The caller holds
// the tool proof.
func (c *conversation) rebuildTools(ctx context.Context) error {
	current, err := c.sess.CurrentAgent(ctx)
	if err != nil {
		return err
	}
	if current == nil {
		return c.sess.DeselectAgent(ctx)
	}
	return c.endIfModelNotKept(ctx, c.selectAgentKeepingModel(ctx, current.ID))
}

// SelectAgent selects id for the next turns, or the default agent for "",
// keeping the Task's model. It waits for a tool proof in flight, since an
// agent change builds the tool set again, and the next message proves the
// uam tools again under the new agent.
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
	err := c.selectAgentKeepingModel(ctx, id)
	if c.tools != nil {
		c.tools.invalidate()
	}
	if err != nil {
		c.p.poke()
	}
	return c.endIfModelNotKept(ctx, err)
}

// reselectAgent selects the conversation's custom agent again after a
// configuration reload, which reads agent definitions again, keeping the
// Task's model. An agent that is gone fails with ErrAgentUnavailable rather
// than leaving the next turn to another agent. The caller holds control.
func (c *conversation) reselectAgent(ctx context.Context) error {
	c.mu.Lock()
	id := c.agent
	c.mu.Unlock()
	if id == "" {
		return nil
	}
	return c.endIfModelNotKept(ctx, c.selectAgentKeepingModel(ctx, id))
}
