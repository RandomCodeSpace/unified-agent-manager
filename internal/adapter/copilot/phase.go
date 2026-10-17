package copilot

import (
	"cmp"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// maxPhaseInput bounds a running tool's arguments on the phase, in bytes.
const maxPhaseInput = 2048

// phaseState is the running turn's model calls in flight, by agent ID (""
// for the main agent), and its running tool calls, by call ID. It follows
// the provider's events only. Guarded by mu.
type phaseState struct {
	calls map[string]phaseCall
	tools map[string]phaseTool
}

type phaseCall struct {
	at    time.Time
	model string
}

type phaseTool struct {
	agentID, name, input string
	at                   time.Time
}

// observePhaseLocked folds ev into the turn's phase and reports a change on
// the activity. An agent's model call runs from its assistant.turn_start to
// its usage report, its first tool call, its turn's end, an abort, its
// assistant.idle or an error; a retry keeps it. A task call is not a
// running tool: its subagent reports its own calls and tools.
func (c *conversation) observePhaseLocked(ev copilot.SessionEvent, agentID string) {
	p := &c.phase
	if p.calls == nil {
		p.calls, p.tools = map[string]phaseCall{}, map[string]phaseTool{}
	}
	at := ev.Timestamp
	if at.IsZero() {
		at = time.Now()
	}
	changed := false
	switch d := ev.Data.(type) {
	case *rpc.AssistantTurnStartData:
		if _, ok := p.calls[agentID]; !ok {
			p.calls[agentID] = phaseCall{at: at, model: c.callModelLocked(agentID, d.Model)}
			changed = true
		}
	case *rpc.AssistantUsageData, *rpc.AssistantTurnEndData, *rpc.AbortData, *rpc.AssistantIdleData, *rpc.SessionErrorData:
		changed = p.endCall(agentID)
	case *rpc.ToolExecutionStartData:
		changed = p.endCall(agentID)
		if d.ToolName != taskTool {
			p.tools[d.ToolCallID] = phaseTool{agentID: agentID, name: d.ToolName, input: clip(compactJSON(d.Arguments), maxPhaseInput), at: at}
			changed = true
		}
	case *rpc.ToolExecutionCompleteData:
		_, changed = p.tools[d.ToolCallID]
		delete(p.tools, d.ToolCallID)
	case *rpc.SubagentCompletedData:
		changed = p.endAgent(c.subagentOf(agentID, d.ToolCallID))
	case *rpc.SubagentFailedData:
		changed = p.endAgent(c.subagentOf(agentID, d.ToolCallID))
	}
	if changed {
		c.emitPhaseLocked()
	}
}

// subagentOf is the subagent a subagent.* event is about: its envelope
// agent, else the one its spawning call started.
func (c *conversation) subagentOf(agentID, callID string) string {
	if agentID == "" {
		agentID = c.subs.byCall[callID]
	}
	return agentID
}

// callModelLocked is the model of an agent's model call: as its start names
// it, else the main agent's last or selected model, or the subagent's.
func (c *conversation) callModelLocked(agentID string, named *string) string {
	model := orEmpty(named)
	if agentID != "" {
		if sa := c.subs.byID[agentID]; model == "" && sa != nil {
			model = sa.Model
		}
		return model
	}
	if model == "" {
		model = cmp.Or(c.turnModel, c.selected)
	}
	if model == "" {
		return ""
	}
	return c.p.customSelection(model, c.selected)
}

// endCall ends the agent's model call in flight and reports whether there
// was one.
func (p *phaseState) endCall(agentID string) bool {
	_, ok := p.calls[agentID]
	delete(p.calls, agentID)
	return ok
}

// endAgent ends a subagent's model call and its running tools.
func (p *phaseState) endAgent(agentID string) bool {
	if agentID == "" {
		return false
	}
	changed := p.endCall(agentID)
	for id, tool := range p.tools {
		if tool.agentID == agentID {
			delete(p.tools, id)
			changed = true
		}
	}
	return changed
}

// snapshot is the phase to report, nil while no call is in flight and no
// tool runs. Ties go to the lower ID, so equal states report alike.
func (p *phaseState) snapshot() *agentapi.Phase {
	if len(p.calls) == 0 && len(p.tools) == 0 {
		return nil
	}
	var out agentapi.Phase
	oldest := ""
	for id, call := range p.calls {
		out.Calls++
		if id != "" {
			out.Subagents++
		}
		if out.Calls == 1 || call.at.Before(out.CallsSince) || call.at.Equal(out.CallsSince) && id < oldest {
			out.CallsSince, oldest = call.at, id
		}
	}
	if main, ok := p.calls[""]; ok {
		out.Model = main.model
	} else if out.Calls > 0 {
		out.Model = p.calls[oldest].model
	}
	first := true
	for id, tool := range p.tools {
		if first || tool.at.Before(out.ToolSince) || tool.at.Equal(out.ToolSince) && id < oldest {
			first = false
			out.ToolSince, oldest = tool.at, id
			out.Tool, out.Input = tool.name, tool.input
		}
	}
	return &out
}

// emitPhaseLocked reports the phase on the activity when it changed.
func (c *conversation) emitPhaseLocked() {
	next := c.phase.snapshot()
	if samePhase(next, c.act.now.Phase) {
		return
	}
	c.act.now.Phase = next
	c.emitActivityLocked()
}

func samePhase(a, b *agentapi.Phase) bool {
	return a == b || a != nil && b != nil && *a == *b
}
