package copilot

import (
	copilot "github.com/microsoft/copilot-sdk/go/v2"
	"github.com/microsoft/copilot-sdk/go/v2/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// maxCallModels bounds callModels: a call whose end never arrives would
// otherwise stay for the life of the conversation.
const maxCallModels = 256

// observeModelCallLocked reports each finished model call with its model.
// Only the start of a call names the model, so it is kept until the end,
// which carries the same turn ID. A custom model is reported by its
// selection ID, the ID the subagent model list uses.
func (c *conversation) observeModelCallLocked(ev copilot.SessionEvent, agentID string) {
	switch d := ev.Data.(type) {
	case *rpc.ModelCallStartData:
		if d.Model == nil || *d.Model == "" {
			return
		}
		if len(c.callModels) >= maxCallModels {
			clear(c.callModels)
		}
		if c.callModels == nil {
			c.callModels = map[string]string{}
		}
		c.callModels[agentID+"\x00"+d.TurnID] = c.p.customSelection(*d.Model, c.selected)
	case *rpc.ModelCallFinishedData:
		key := agentID + "\x00" + d.TurnID
		model, ok := c.callModels[key]
		if !ok {
			return
		}
		delete(c.callModels, key)
		c.emitLocked(agentapi.Event{Kind: agentapi.EventModelCall, ModelCall: &agentapi.ModelCall{Model: model, Outcome: string(d.Outcome), DurationMS: int64(max(d.DispatchDurationMs, 0))}})
	}
}
