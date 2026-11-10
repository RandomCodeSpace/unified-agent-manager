package copilot

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
)

// subagentLog keeps each subagent's record by its agent ID. Events change a
// running record, and end an idle one only as failed or cancelled. The first
// terminal event wins: Copilot reports a second, cancelled completion for an
// idle subagent when the client disconnects. Only the task list makes a
// completed record idle. The conversation separately tracks accepted follow-ups
// and provider-reported reuse, which can make a completed or idle record run again.
type subagentLog struct {
	byID   map[string]*agentapi.Subagent
	byCall map[string]string          // spawning tool call ID -> agent ID
	asked  map[string]string          // spawning tool call ID -> the call's description
	prompt map[string]string          // spawning tool call ID -> the start of the call's prompt, until its start
	tools  map[string]map[string]bool // agent ID -> tool call IDs counted live
	order  []string
}

func newSubagentLog() *subagentLog {
	return &subagentLog{byID: map[string]*agentapi.Subagent{}, byCall: map[string]string{}, asked: map[string]string{}, prompt: map[string]string{}, tools: map[string]map[string]bool{}}
}

// noteCall keeps what a task call asked its subagent to do, in the call's own
// words: a predefined agent's description says only what that agent is for,
// the same on every spawn.
func (l *subagentLog) noteCall(d *rpc.ToolExecutionStartData) {
	if d.ToolName != taskTool {
		return
	}
	args, _ := d.Arguments.(map[string]any)
	if text, _ := args["description"].(string); strings.TrimSpace(text) != "" {
		l.asked[d.ToolCallID] = displaytext.Sanitize(strings.TrimSpace(text))
	}
	if text, _ := args["prompt"].(string); strings.TrimSpace(text) != "" {
		l.prompt[d.ToolCallID] = clip(displaytext.Sanitize(strings.TrimSpace(text)), subagentPromptBytes)
	}
}

// apply folds a subagent.* event into the log and returns the changed record,
// or false when nothing changed.
func (l *subagentLog) apply(ev copilot.SessionEvent) (agentapi.Subagent, bool) {
	agentID := agentOf(ev)
	var callID, name, errMsg string
	var status agentapi.SubagentStatus
	var tokens, toolCalls *int64
	switch d := ev.Data.(type) {
	case *rpc.SubagentStartedData:
		if agentID == "" {
			return agentapi.Subagent{}, false
		}
		sa := l.get(agentID)
		if sa.Status.Terminal() || sa.Status == agentapi.SubagentIdle {
			return agentapi.Subagent{}, false
		}
		if len(sa.Runs) == 0 {
			sa.StartRun(ev.Timestamp, agentapi.SubagentTriggerSpawn)
		}
		sa.ParentToolCallID, sa.Name, sa.Description = d.ToolCallID, subagentName(d.AgentDisplayName, d.AgentName), cmp.Or(l.asked[d.ToolCallID], d.AgentDescription)
		sa.Type = cmp.Or(d.AgentName, deref(d.AgentType))
		if d.ParentID != nil {
			sa.ParentAgentID = *d.ParentID
		}
		sa.Background = d.ExecutionMode != nil && *d.ExecutionMode == "background"
		sa.Status, sa.StartedAt = agentapi.SubagentRunning, ev.Timestamp
		if d.Model != nil && sa.Model == "" {
			sa.Model = *d.Model
		}
		l.byCall[d.ToolCallID] = agentID
		out := sa.Snapshot()
		// Only the start carries the prompt: it feeds the Utility name once.
		out.Prompt = l.prompt[d.ToolCallID]
		delete(l.prompt, d.ToolCallID)
		return out, true
	case *rpc.SubagentConfiguredData:
		if agentID == "" {
			return agentapi.Subagent{}, false
		}
		sa := l.get(agentID)
		if sa.Status.Terminal() || sa.Status == agentapi.SubagentIdle {
			return agentapi.Subagent{}, false
		}
		sa.Model = d.Model
		sa.Effort = ""
		if d.ReasoningEffort != nil {
			sa.Effort = *d.ReasoningEffort
		}
		return sa.Snapshot(), true
	case *rpc.SubagentCompletedData:
		callID, name, status = d.ToolCallID, subagentName(d.AgentDisplayName, d.AgentName), agentapi.SubagentCompleted
		if d.Cancelled != nil && *d.Cancelled {
			status = agentapi.SubagentCancelled
		}
		tokens, toolCalls = d.TotalTokens, d.TotalToolCalls
	case *rpc.SubagentFailedData:
		callID, name, status = d.ToolCallID, subagentName(d.AgentDisplayName, d.AgentName), agentapi.SubagentFailed
		errMsg = clip(displaytext.Sanitize(d.Error), maxErrorText)
		tokens, toolCalls = d.TotalTokens, d.TotalToolCalls
	case *rpc.ToolExecutionStartData:
		l.noteCall(d)
		return agentapi.Subagent{}, false
	default:
		return agentapi.Subagent{}, false
	}
	if agentID == "" {
		agentID = l.byCall[callID]
	}
	if agentID == "" {
		return agentapi.Subagent{}, false
	}
	sa := l.get(agentID)
	if sa.Status.Terminal() || sa.Status == agentapi.SubagentIdle && status == agentapi.SubagentCompleted {
		// A later end, such as the cancelled one on disconnect, still
		// reports the totals of every run, reuses included.
		if tokens != nil && *tokens > sa.Tokens || toolCalls != nil && *toolCalls > sa.ToolCalls {
			sa.Tokens, sa.ToolCalls = max(sa.Tokens, orZero(tokens)), max(sa.ToolCalls, orZero(toolCalls))
			return sa.Snapshot(), true
		}
		return agentapi.Subagent{}, false
	}
	if sa.ParentToolCallID == "" {
		sa.ParentToolCallID = callID
	}
	if sa.Name == "" {
		sa.Name = name
	}
	sa.Status, sa.Error, sa.EndedAt = status, errMsg, ev.Timestamp
	// The provider's totals replace the live counts.
	if tokens != nil {
		sa.Tokens = *tokens
	}
	if toolCalls != nil {
		sa.ToolCalls = *toolCalls
	}
	delete(l.tools, agentID)
	return sa.Snapshot(), true
}

// count adds a running subagent's model call usage or tool call start to
// its live counts and returns the changed record, or false when nothing
// changed. Usage events are not recorded, so only live events count; the
// end event's totals replace the sums.
func (l *subagentLog) count(ev copilot.SessionEvent) (agentapi.Subagent, bool) {
	agentID := agentOf(ev)
	sa := l.byID[agentID]
	if agentID == "" || sa == nil || sa.Status != agentapi.SubagentRunning {
		return agentapi.Subagent{}, false
	}
	switch d := ev.Data.(type) {
	case *rpc.AssistantUsageData:
		n := max(orZero(d.InputTokens), 0) + max(orZero(d.OutputTokens), 0)
		if n == 0 {
			return agentapi.Subagent{}, false
		}
		sa.Tokens += n
	case *rpc.ToolExecutionStartData:
		if l.tools[agentID][d.ToolCallID] {
			return agentapi.Subagent{}, false
		}
		if l.tools[agentID] == nil {
			l.tools[agentID] = map[string]bool{}
		}
		l.tools[agentID][d.ToolCallID] = true
		sa.ToolCalls++
	default:
		return agentapi.Subagent{}, false
	}
	return sa.Snapshot(), true
}

func orZero(n *int64) int64 {
	if n == nil {
		return 0
	}
	return *n
}

func (l *subagentLog) get(agentID string) *agentapi.Subagent {
	sa := l.byID[agentID]
	if sa == nil {
		sa = &agentapi.Subagent{ID: agentID}
		l.byID[agentID] = sa
		l.order = append(l.order, agentID)
	}
	return sa
}

func (l *subagentLog) list() []agentapi.Subagent {
	out := make([]agentapi.Subagent, 0, len(l.order))
	for _, id := range l.order {
		out = append(out, l.byID[id].Snapshot())
	}
	return out
}

func subagentName(display, name string) string {
	if display != "" {
		return display
	}
	return name
}

func (c *conversation) CancelSubagent(ctx context.Context, agentID string) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return agentapi.ErrClosed
	}
	if sa := c.subs.byID[agentID]; c.stoppedSubagents[agentID] || sa != nil && sa.Status.Terminal() {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()
	cancelled, err := c.sess.CancelSubagent(ctx, agentID)
	if err != nil {
		c.p.poke()
		return fmt.Errorf("copilot cancel subagent: %s", errText(err))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !cancelled {
		if sa := c.subs.byID[agentID]; sa != nil && sa.Status.Terminal() {
			return nil
		}
		return errors.New("copilot did not cancel the subagent")
	}
	if c.stoppedSubagents == nil {
		c.stoppedSubagents = map[string]bool{}
	}
	c.stoppedSubagents[agentID] = true
	c.expireSubagentLocked(agentID)
	// A stopped follow-up may end without a subagent event, and without the
	// end of its model call.
	c.endAgentPhaseLocked(agentID)
	c.checkTasksLocked()
	return nil
}

func (c *conversation) setPrompting(agentID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prompting = agentID
}

// PromptSubagent sends text to exactly agentID, only while this record says
// idle: the CLI accepts a message for a running agent and never delivers it.
// An accepted follow-up runs until the task list reports it idle or ended.
func (c *conversation) PromptSubagent(ctx context.Context, agentID, text string) error {
	c.taskRPC.Lock()
	defer c.taskRPC.Unlock()
	c.mu.Lock()
	closed, sa := c.closed, c.subs.byID[agentID]
	var status agentapi.SubagentStatus
	if sa != nil {
		status = sa.Status
	}
	c.mu.Unlock()
	switch {
	case closed:
		return agentapi.ErrClosed
	case sa == nil:
		return errors.New("copilot has no such subagent")
	case status != agentapi.SubagentIdle:
		return fmt.Errorf("the subagent is %s, not waiting for a follow-up", status)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.setPrompting(agentID)
	defer c.setPrompting("")
	sent := time.Now()
	res, err := c.sess.MessageSubagent(ctx, agentID, text)
	var uncertain error
	switch {
	case err != nil:
		uncertain = c.sendError(err)
		if !errors.Is(uncertain, agentapi.ErrSubmissionUncertain) {
			return uncertain
		}
	case res == nil:
		uncertain = fmt.Errorf("%w: copilot returned no result", agentapi.ErrSubmissionUncertain)
	case !res.Sent:
		reason := "copilot did not deliver the follow-up"
		if res.Error != nil && *res.Error != "" {
			reason += ": " + clip(displaytext.Sanitize(*res.Error), maxErrorText)
		}
		return errors.New(reason)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if sa.Status == agentapi.SubagentIdle {
		// A follow-up that may have arrived blocks another until the task list
		// settles it. An accepted one ends only with an idle entry newer than
		// the send: the list can still report the idle from before it.
		sa.StartRun(sent, agentapi.SubagentTriggerUser)
		if uncertain != nil {
			sent = time.Time{}
		}
		sa.Status, sa.EndedAt = agentapi.SubagentRunning, time.Time{}
		v := sa.Snapshot()
		c.emitLocked(agentapi.Event{Kind: agentapi.EventSubagent, Subagent: &v})
		c.watchLocked(agentID, sent)
	}
	return uncertain
}

// resumeSubagentLocked records a follow-up started by the main agent rather
// than UAM's composer. A later start distinguishes reuse from stale events.
// One seen while PromptSubagent sends to the agent is that follow-up's run.
func (c *conversation) resumeSubagentLocked(agentID string, started time.Time) bool {
	sa := c.subs.byID[agentID]
	if sa == nil || c.stoppedSubagents[agentID] ||
		(sa.Status != agentapi.SubagentCompleted && sa.Status != agentapi.SubagentIdle) || !started.After(sa.EndedAt) {
		return false
	}
	trigger := agentapi.SubagentTriggerAgent
	if c.prompting == agentID {
		trigger = agentapi.SubagentTriggerUser
	}
	sa.StartRun(started, trigger)
	sa.Status, sa.StartedAt, sa.EndedAt = agentapi.SubagentRunning, started, time.Time{}
	c.watch[agentID] = started
	v := sa.Snapshot()
	c.emitLocked(agentapi.Event{Kind: agentapi.EventSubagent, Subagent: &v})
	return true
}
