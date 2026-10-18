package copilot

import (
	"cmp"
	"strings"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/toolargs"
)

// maxPhaseDetailRunes bounds a running tool's short argument on the phase.
const maxPhaseDetailRunes = 200

// compactionCall keys the main agent's compaction among the model calls; an
// agent ID never holds a NUL.
const compactionCall = "\x00compaction"

// phaseState is the conversation's model calls in flight, by agent ID (""
// for the main agent, compactionCall for its compaction), and its running
// tool calls, by call ID. It follows the provider's events only. A turn's
// end keeps only the subagents still running. Guarded by mu.
type phaseState struct {
	calls map[string]phaseCall
	tools map[string]phaseTool
}

type phaseCall struct {
	at    time.Time
	model string
	// agent describes a subagent's call: its description, else its name.
	agent string
}

type phaseTool struct {
	agentID, name, detail string
	at                    time.Time
}

// observePhaseLocked folds ev into the phase and reports a change on the
// activity. An agent's model call runs from its assistant.turn_start to its
// usage report, its first tool call, its turn's end, an abort, its
// assistant.idle or an error; a retry keeps it. A subagent's abort also ends
// its tools. Compaction is a call of the main agent from its start to its
// end, failed or not, or to the main agent's abort, error or idle, or to the
// main agent's first progress, which shows it runs in the background
// (observeCompactionLocked); its start names no model, so it takes the
// Thread's. A task call is not a running
// tool: its subagent reports its own calls and tools.
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
		if _, ok := p.calls[agentID]; !ok && c.phaseAgentLocked(agentID, ev.Timestamp, true) {
			p.calls[agentID] = phaseCall{at: at, model: c.callModelLocked(agentID, d.Model), agent: c.agentLabelLocked(agentID)}
			changed = true
		}
	case *rpc.SessionCompactionStartData:
		if _, ok := p.calls[compactionCall]; !ok && agentID == "" {
			p.calls[compactionCall] = phaseCall{at: at, model: c.callModelLocked("", d.Model)}
			changed = true
		}
	case *rpc.SessionCompactionCompleteData:
		changed = agentID == "" && p.endCall(compactionCall)
	case *rpc.AssistantUsageData, *rpc.AssistantTurnEndData:
		changed = p.endCall(agentID)
	case *rpc.SessionIdleData:
		// One in a turn may be stale; the turn's end ends its compaction.
		changed = agentID == "" && !c.turnRunning && p.endCall(compactionCall)
	case *rpc.AbortData, *rpc.AssistantIdleData, *rpc.SessionErrorData:
		_, abort := d.(*rpc.AbortData)
		switch {
		case agentID == "":
			changed = p.endCall(compactionCall)
			changed = p.endCall("") || changed
		case abort:
			changed = p.endAgent(agentID)
		default:
			changed = p.endCall(agentID)
		}
	case *rpc.ToolExecutionStartData:
		changed = p.endCall(agentID)
		if d.ToolName != taskTool && c.phaseAgentLocked(agentID, ev.Timestamp, false) {
			// The short argument from the whole arguments, as the transcript
			// picks it: a cut JSON prefix would not parse.
			arg, _ := toolargs.Compact(d.ToolName, compactJSON(d.Arguments))
			p.tools[d.ToolCallID] = phaseTool{agentID: agentID, name: d.ToolName, detail: clipRuneCount(arg, maxPhaseDetailRunes), at: at}
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

// endAgentPhaseLocked ends a subagent's model call and running tools that
// ended without its events: one uam stopped, or one the task list settled.
func (c *conversation) endAgentPhaseLocked(agentID string) {
	if c.phase.endAgent(agentID) {
		c.emitPhaseLocked()
	}
}

// endTurnPhaseLocked ends, with the main turn, the main agent's call, its
// compaction and its tools, and the calls and tools of subagents no longer
// running: only subagents that outlive the turn stay, until their own end.
func (c *conversation) endTurnPhaseLocked() {
	for id := range c.phase.calls {
		if !c.subagentRunsLocked(id) {
			delete(c.phase.calls, id)
		}
	}
	for id, tool := range c.phase.tools {
		if !c.subagentRunsLocked(tool.agentID) {
			delete(c.phase.tools, id)
		}
	}
}

// settlePhaseLocked ends the calls and tools of subagents the task list
// shows ended since they started: their own end may never be reported, and
// between turns nothing else would end them.
func (c *conversation) settlePhaseLocked(tasks []rpc.TaskInfo) {
	latest := map[string]time.Time{}
	for id, call := range c.phase.calls {
		latest[id] = laterOf(latest[id], call.at)
	}
	for _, tool := range c.phase.tools {
		latest[tool.agentID] = laterOf(latest[tool.agentID], tool.at)
	}
	changed := false
	for id, since := range latest {
		t := agentTask(tasks, id)
		if id == "" || t == nil || t.Status == rpc.TaskStatusRunning {
			continue
		}
		if ended := taskEndedAt(t); ended != nil && !ended.Before(since) {
			changed = c.phase.endAgent(id) || changed
		}
	}
	if changed {
		c.emitPhaseLocked()
	}
}

// taskEndedAt is when a task-list entry ended, as applyTasksLocked reads
// it: an idle entry's IdleSince, else its CompletedAt; nil when unknown.
func taskEndedAt(t *rpc.TaskAgentInfo) *time.Time {
	if t.IdleSince != nil && t.Status == rpc.TaskStatusIdle {
		return t.IdleSince
	}
	return t.CompletedAt
}

func laterOf(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// phaseAgentLocked reports whether an event of agentID at at may start a
// model call or a tool: the main agent's, a subagent's uam does not know or
// one running, or a turn start that resumes a finished or idle one, as
// resumeSubagentLocked takes it. The trailing events of a stopped, failed,
// cancelled or settled subagent start nothing.
func (c *conversation) phaseAgentLocked(agentID string, at time.Time, turnStart bool) bool {
	if agentID == "" {
		return true
	}
	sa := c.subs.byID[agentID]
	switch {
	case c.stoppedSubagents[agentID]:
		return false
	case sa == nil || sa.Status == agentapi.SubagentRunning:
		return true
	}
	return turnStart && (sa.Status == agentapi.SubagentCompleted || sa.Status == agentapi.SubagentIdle) && at.After(sa.EndedAt)
}

// subagentRunsLocked reports whether agentID is a subagent still running.
func (c *conversation) subagentRunsLocked(agentID string) bool {
	sa := c.subs.byID[agentID]
	return agentID != "" && sa != nil && sa.Status == agentapi.SubagentRunning && !c.stoppedSubagents[agentID]
}

// agentLabelLocked describes a subagent on its calls: its description, else
// its name; "" for the main agent.
func (c *conversation) agentLabelLocked(agentID string) string {
	sa := c.subs.byID[agentID]
	if agentID == "" || sa == nil {
		return ""
	}
	return clipRuneCount(cmp.Or(strings.TrimSpace(sa.Description), strings.TrimSpace(sa.Name)), maxPhaseDetailRunes)
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
// tool runs. Outside a turn the main agent's own call and tools are left
// out: its next turn's first call starts before the turn does, and shows
// with it. Ties go to the lower ID, so equal states report alike.
func (p *phaseState) snapshot(turn bool) *agentapi.Phase {
	var out agentapi.Phase
	oldest := ""
	for id, call := range p.calls {
		if id == "" && !turn {
			continue
		}
		out.Calls++
		if id != "" && id != compactionCall {
			out.Subagents++
		}
		if out.Calls == 1 || call.at.Before(out.CallsSince) || call.at.Equal(out.CallsSince) && id < oldest {
			out.CallsSince, oldest = call.at, id
		}
	}
	shown, ok := p.calls[""]
	if !ok || !turn {
		shown, ok = p.calls[compactionCall]
		out.Compacting = ok
	}
	if !ok && out.Calls > 0 {
		shown = p.calls[oldest]
		out.Agent = shown.agent
	}
	out.Model = shown.model
	first, oldest := true, ""
	for id, tool := range p.tools {
		if tool.agentID == "" && !turn {
			continue
		}
		if first || tool.at.Before(out.ToolSince) || tool.at.Equal(out.ToolSince) && id < oldest {
			first = false
			out.ToolSince, oldest = tool.at, id
			out.Tool, out.Detail, out.ToolSubagent = tool.name, tool.detail, tool.agentID != ""
		}
	}
	if out.Calls == 0 && first {
		return nil
	}
	return &out
}

// emitPhaseLocked reports the phase on the activity when it changed.
func (c *conversation) emitPhaseLocked() {
	next := c.phase.snapshot(c.turnRunning)
	if samePhase(next, c.act.now.Phase) {
		return
	}
	c.act.now.Phase = next
	c.emitActivityLocked()
}

// emitPhaseAfterTurnLocked reports the phase again right after a turn's end,
// which ends the turn's activity: the subagents that outlive the turn, and
// whatever started while the end was held.
func (c *conversation) emitPhaseAfterTurnLocked() {
	if c.act.now.Phase != nil {
		c.emitActivityLocked()
	}
}

func samePhase(a, b *agentapi.Phase) bool {
	return a == b || a != nil && b != nil && *a == *b
}
