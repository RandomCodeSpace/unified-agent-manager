package copilot

import (
	copilot "github.com/microsoft/copilot-sdk/go/v2"
	"github.com/microsoft/copilot-sdk/go/v2/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// maxCallModels bounds callModels: a call whose end never arrives would
// otherwise stay for the life of the conversation.
const maxCallModels = 256

// callModel is the model of an agent turn's calls in flight, and how many
// there are. Model is empty while calls on different models overlap, as a
// Fusion turn's do: an end does not say which of them it is.
type callModel struct {
	model string
	n     int
}

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
		key := agentID + "\x00" + d.TurnID
		if _, ok := c.callModels[key]; !ok && len(c.callModels) >= maxCallModels {
			clear(c.callModels)
		}
		if c.callModels == nil {
			c.callModels = map[string]callModel{}
		}
		model := c.p.customSelection(*d.Model, c.selected)
		call, ok := c.callModels[key]
		if ok && call.model != model {
			model = ""
		}
		c.callModels[key] = callModel{model: model, n: call.n + 1}
	case *rpc.ModelCallFinishedData:
		key := agentID + "\x00" + d.TurnID
		call, ok := c.callModels[key]
		if !ok {
			return
		}
		if call.n--; call.n > 0 {
			c.callModels[key] = call
		} else {
			delete(c.callModels, key)
		}
		if call.model != "" {
			c.emitLocked(agentapi.Event{Kind: agentapi.EventModelCall, ModelCall: &agentapi.ModelCall{Model: call.model, Outcome: string(d.Outcome), DurationMS: int64(max(d.DispatchDurationMs, 0))}})
		}
	}
}
