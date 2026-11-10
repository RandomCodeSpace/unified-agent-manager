package copilot

import (
	"context"
	"slices"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// onEvent runs on the SDK's event goroutine, which stalls the whole CLI
// connection while it runs; it only updates state and emits. Subagent events
// arrive on the same stream with the envelope agentId; they are tagged with
// it so they never enter the main transcript or end the main turn.
func (c *conversation) onEvent(ev copilot.SessionEvent) {
	var after func()
	c.mu.Lock()
	defer func() {
		c.mu.Unlock()
		if after != nil {
			after()
		}
	}()
	if c.closed {
		return
	}
	// An event while the main agent waits on the model is an answer of
	// sorts; the silence before it sizes the stall bound. An abort is not
	// one: uam's of a stalled request, retried or given up, or a Stop's.
	if !c.modelWaitAt.IsZero() && !c.stall.aborting && c.stall.note == "" && !c.stopPendingLocked() {
		c.modelGapMax = max(c.modelGapMax, time.Since(c.lastEventAt))
	}
	c.lastEvent, c.lastEventAt = string(ev.Type()), time.Now()
	if c.tools != nil {
		c.tools.observe(ev)
	}
	agentID := agentOf(ev)
	if agentID == "" {
		c.noteModelWaitLocked(ev)
	} else {
		c.noteSubagentWaitLocked(ev, agentID)
	}
	completionCtx, newCompletionEvent := c.tr.completions.observe(ev)
	c.observeCompactionLocked(ev, agentID)
	// The main agent went on after an error, so the error did not end the
	// turn and must not fail it.
	if agentID == "" && newCompletionEvent && c.turnErr != "" {
		switch ev.Data.(type) {
		case *rpc.AssistantTurnStartData, *rpc.AssistantMessageData, *rpc.ToolExecutionStartData:
			c.turnErr = ""
		}
	}
	c.observePhaseLocked(ev, agentID)
	if c.observeActivityLocked(ev, agentID) {
		return
	}
	if c.observeTodosLocked(ev, agentID) {
		return
	}
	if c.observeMCPStatusLocked(ev, agentID) {
		return
	}
	if d, ok := ev.Data.(*rpc.SessionAutopilotStoppedData); ok && agentID == "" {
		c.autopilotStoppedLocked(d, ev.Timestamp)
	}
	switch d := ev.Data.(type) {
	case *rpc.SessionModelChangeData:
		if agentID == "" {
			c.autoModelSelectionLocked(d)
		}
		return
	case *rpc.AutoModeSwitchRequestedData, *rpc.AutoModeSwitchCompletedData:
		// The callback lacks RequestID: these notifications cannot identify
		// which private question to settle and must never dismiss another.
		return
	case *rpc.SessionPlanChangedData:
		if agentID != "" || ev.ID != "" && ev.ID == c.lastPlanChange {
			return
		}
		c.lastPlanChange = ev.ID
		c.planVersion++
		c.emitLocked(agentapi.Event{Kind: agentapi.EventPlanPath, PlanVersion: c.planVersion})
		c.checkPlanLocked()
	case *rpc.SessionScheduleCreatedData, *rpc.SessionScheduleCancelledData, *rpc.SessionScheduleRearmedData:
		c.checkSchedulesLocked()
		return
	case *rpc.AssistantMessageDeltaData:
		c.emitLocked(deltaEvent(agentID, d.MessageID, agentapi.ItemAssistant, d.DeltaContent))
		return
	case *rpc.AssistantReasoningDeltaData:
		c.tr.streamedReasoning(agentID)
		c.emitLocked(deltaEvent(agentID, reasoningItemID(d.ReasoningID), agentapi.ItemReasoning, d.DeltaContent))
		return
	case *rpc.AssistantUsageData:
		if ev.ID != "" {
			if c.tokenSeen[ev.ID] {
				return
			}
			if c.tokenSeen == nil {
				c.tokenSeen = map[string]bool{}
			}
			c.tokenSeen[ev.ID] = true
			c.tokenOrder = append(c.tokenOrder, ev.ID)
			if len(c.tokenOrder) > maxTokenSeen {
				delete(c.tokenSeen, c.tokenOrder[0])
				c.tokenOrder = c.tokenOrder[1:]
			}
		}
		if tokens := modelTokens(ev, d); tokens != nil {
			if d.IsByok != nil && *d.IsByok {
				tokens.Model = c.p.customSelection(d.Model, c.selected)
			}
			c.emitLocked(agentapi.Event{Kind: agentapi.EventTokens, Tokens: tokens})
		}
		// The account's quotas as of this call, a subagent's included.
		c.p.noteQuota(d.QuotaSnapshots)
		if agentID == "" && d.Model != "" {
			c.turnModel = d.Model
			if d.IsByok != nil && *d.IsByok {
				c.turnModel = c.p.customSelection(d.Model, c.selected)
			}
		}
		if agentID == "" && d.InputTokens != nil && *d.InputTokens >= 0 {
			// Input tokens include those read from and written to the cache.
			c.usage.Prompt, c.usage.Cached = *d.InputTokens, 0
			if d.CacheReadTokens != nil {
				c.usage.Cached = min(max(*d.CacheReadTokens, 0), *d.InputTokens)
			}
			c.emitContextLocked()
		}
		// Every call costs, a subagent's included.
		if d.CopilotUsage != nil && d.CopilotUsage.TotalNanoAiu > 0 {
			c.nanoAIU += d.CopilotUsage.TotalNanoAiu
			c.emitUsageLocked()
		}
		if sa, ok := c.subs.count(ev); ok {
			c.emitLocked(agentapi.Event{Kind: agentapi.EventSubagent, Subagent: &sa})
		}
		return
	case *rpc.SessionUsageCheckpointData:
		// The CLI's session-wide total, which the next reopen reads back.
		if d.TotalNanoAiu >= 0 {
			c.nanoAIU = d.TotalNanoAiu
			c.emitUsageLocked()
		}
		return
	case *rpc.SessionUsageInfoData:
		if agentID == "" && d.CurrentTokens >= 0 && d.TokenLimit > 0 {
			c.usage.Used, c.usage.Limit = d.CurrentTokens, d.TokenLimit
			c.emitContextLocked()
		}
		return
	case *rpc.SessionTitleChangedData:
		c.emitLocked(agentapi.Event{Kind: agentapi.EventTitle, Title: d.Title})
		return
	case *rpc.SkillInvokedData, *rpc.SkillInvokedRefData:
		if load, ok := skillLoad(ev); ok {
			c.emitLocked(agentapi.Event{Kind: agentapi.EventSkill, Skill: &load})
		}
		return
	case *rpc.SubagentStartedData, *rpc.SubagentConfiguredData, *rpc.SubagentCompletedData, *rpc.SubagentFailedData:
		if sa, ok := c.subs.apply(ev); ok {
			if sa.Status.Terminal() {
				c.forgetViewsLocked(sa.ID)
			}
			if sa.Status == agentapi.SubagentCancelled {
				c.expireSubagentLocked(sa.ID)
			}
			c.emitLocked(agentapi.Event{Kind: agentapi.EventSubagent, Subagent: &sa})
			if sa.Status == agentapi.SubagentCompleted {
				c.watchLocked(sa.ID, time.Time{})
			}
		}
		return
	case *rpc.SessionBackgroundTasksChangedData:
		// Shell changes and follow-up completion both invalidate the task list.
		c.checkTasksLocked()
		return
	case *rpc.UserInputRequestedData:
		c.linkQuestionLocked(d, agentID, time.Now())
		return
	case *rpc.ElicitationRequestedData:
		c.elicitRequestedLocked(d, agentID, time.Now())
		return
	case *rpc.ElicitationCompletedData:
		c.elicitCompletedLocked(d)
		return
	case *rpc.AssistantTurnStartData:
		if agentID == "" {
			c.refreshActiveSchedulesLocked()
		}
		c.tr.stepStart[agentID] = ev.Timestamp
		if agentID == "" {
			if !newCompletionEvent {
				return
			}
			c.startTurnLocked()
			c.completionReady = completionCtx.userID != "" && completionCtx.userID == c.tr.completions.users[""]
		} else if c.resumeSubagentLocked(agentID, ev.Timestamp) {
			c.checkTasksLocked()
		}
		return
	case *rpc.SessionErrorData:
		// Between turns it ends no turn and stays the notice below. One that
		// arrives while a prompt is on its way belongs to that prompt's turn.
		if agentID == "" && (c.turnRunning || c.sending > 0) {
			c.turnErr = clip(displaytext.Sanitize(d.Message), maxErrorText)
			c.turnFailure = turnFailure(d)
		}
	case *rpc.SessionTruncationData, *rpc.SessionContextClearedData, *rpc.SessionSnapshotRewindData:
		// The agent's context lost earlier messages, its views among them.
		c.forgetViewsLocked(agentID)
	case *rpc.SessionCompactionStartData:
		// Manual (/compact) and automatic compaction both report here.
		c.forgetViewsLocked(agentID)
		if agentID == "" {
			c.compaction = compactionBlocking
			c.emitLocked(agentapi.Event{Kind: agentapi.EventCompaction, Compacting: true})
		}
		return
	case *rpc.SessionCompactionCompleteData:
		// Its notice item follows.
		c.forgetViewsLocked(agentID)
		if agentID == "" {
			c.compaction = compactionNone
			c.emitLocked(agentapi.Event{Kind: agentapi.EventCompaction, Compacted: d.Success, CompactionSummary: deref(d.SummaryContent)})
		}
	case *rpc.UserMessageData:
		if agentID == "" && newCompletionEvent && (d.Delivery == nil || *d.Delivery != rpc.UserMessageDeliverySteering) && (d.IsAutopilotContinuation == nil || !*d.IsAutopilotContinuation) && !stallContinuation(d) {
			c.completionReady = false
		}
		if agentID == "" && (d.Delivery == nil || *d.Delivery != rpc.UserMessageDeliverySteering) && (d.IsAutopilotContinuation == nil || !*d.IsAutopilotContinuation) && !stallContinuation(d) {
			c.turnChangeUser, c.turnChangeEvent = "", ""
			user := ev.ID
			if d.MessageID != nil && *d.MessageID != "" {
				user = *d.MessageID
			}
			if len(user) <= 256 && len(ev.ID) <= 256 {
				c.turnChangeUser, c.turnChangeEvent = user, ev.ID
			}
		}
		if agentID == "" && d.MessageID != nil {
			c.steers = slices.DeleteFunc(c.steers, func(st *steer) bool { return st.id == *d.MessageID })
			if c.steering > 0 {
				c.seen[*d.MessageID] = true
			}
		}
		// A message delivered while idle starts a turn: also a steer that
		// reached the CLI after the idle of the turn it was meant for.
		if agentID == "" && d.Delivery != nil && *d.Delivery == rpc.UserMessageDeliveryIdle {
			c.startTurnLocked()
		}
	case *rpc.SessionModeChangedData:
		if agentID == "" {
			c.executionRevision++
			if c.execution == nil {
				c.execution = &agentapi.ExecutionState{}
			}
			next := *c.execution
			next.Mode, next.Known = string(d.NewMode), false
			if d.NewMode != rpc.SessionModeAutopilot {
				c.autopilotTurn = false
			}
			c.execution = &next
			c.emitLocked(agentapi.Event{Kind: agentapi.EventExecution, Execution: &next})
			c.checkExecutionLocked()
		}
		return
	case *rpc.SessionAutopilotObjectiveChangedData:
		if agentID == "" {
			c.executionRevision++
			c.checkExecutionLocked()
		}
		return
	case *rpc.AssistantIdleData:
		if agentID != "" {
			return
		}
		if !c.autopilotStopped && (c.autopilotTurn || c.execution != nil && (c.execution.Mode == "autopilot" || c.execution.Mode == "")) && (d.Aborted == nil || !*d.Aborted) {
			c.idleUnresolved = true
			if c.execution != nil && c.execution.Mode == "" {
				c.checkExecutionLocked()
			}
			c.checkTasksLocked()
			c.autopilotTurn = true
			return
		}
		c.assistantIdleSeen = true
		c.finishTurnLocked(d.Aborted, ev.Timestamp)
		return
	case *rpc.SessionTaskCompleteData:
		if agentID == "" {
			// The lifecycle signal follows the receipt itself, matched to a
			// turn or not: only an explicit request to continue keeps an
			// autopilot run going. Matching gates the completion facts only.
			c.taskCompleted = completionDecision(d) != agentapi.CompletionRejected
			if newCompletionEvent && c.completionReady && c.turnRunning && completionCtx.generation != 0 && completionCtx.generation == c.tr.completions.generation {
				c.completion = c.tr.completion(ev, d)
			}
		}
	case *rpc.ToolExecutionStartData:
		// Its item follows.
		c.subs.noteCall(d)
		if sa, ok := c.subs.count(ev); ok {
			c.emitLocked(agentapi.Event{Kind: agentapi.EventSubagent, Subagent: &sa})
		}
	case *rpc.SessionIdleData:
		if agentID == "" {
			c.refreshActiveSchedulesLocked()
		}
		// Copilot CLI 1.0.89 reports the autopilot mode on the idle that ends
		// a run after task_complete too; only a task_complete tells them apart.
		if agentID == "" && d.Mode != nil && *d.Mode == rpc.SessionModeAutopilot && (d.Aborted == nil || !*d.Aborted) && !c.taskCompleted {
			c.autopilotTurn = true
			next := agentapi.ExecutionState{Mode: "autopilot"}
			if c.execution != nil {
				next = *c.execution
				next.Mode = "autopilot"
				next.Known = false
			}
			c.execution = &next
			c.executionRevision++
			c.emitLocked(agentapi.Event{Kind: agentapi.EventExecution, Execution: &next})
			c.checkExecutionLocked()
			return
		}
		if agentID == "" && (!c.assistantIdleSeen || c.autopilotTurn) && (c.taskCompleted || d.Mode == nil || *d.Mode != rpc.SessionModeAutopilot || d.Aborted != nil && *d.Aborted) {
			c.finishTurnLocked(d.Aborted, ev.Timestamp)
			c.autopilotTurn = false
		}
		return
	case *rpc.PermissionRequestedData:
		c.permissionRequestedLocked(d, ev.Timestamp, agentID)
		return
	case *rpc.PermissionCompletedData:
		after = c.permissionCompletedLocked(d)
		return
	case *rpc.SessionShutdownData:
		if d.TotalNanoAiu != nil && *d.TotalNanoAiu >= 0 {
			c.nanoAIU = *d.TotalNanoAiu
			c.emitUsageLocked()
		}
		if d.ShutdownType == rpc.ShutdownTypeError {
			reason := "Copilot session shut down"
			if d.ErrorReason != nil {
				reason += ": " + clip(displaytext.Sanitize(*d.ErrorReason), maxErrorText)
			}
			// Released as detach does it, off the event goroutine: the detach
			// is due before the exit lets anything reopen the session. The CLI
			// ended the session, so no permission request waits on an answer.
			detached := c.p.detachStarted(c.id)
			c.exitLocked(reason)
			c.p.forget(c)
			if c.aside != nil {
				c.aside()
			}
			go func() {
				defer log.Recover("copilot shut down session release", &c.mu, &c.p.mu)
				if err := c.release(context.Background(), nil, detached); err != nil {
					log.Warn("copilot session shut down: detaching it failed", "conversation", c.id, "error", err)
				}
			}()
		}
		return
	}
	var decided string
	if d, ok := ev.Data.(*rpc.ExitPlanModeCompletedData); ok {
		decided = c.decidedPlanLocked(d)
	}
	for _, it := range c.tr.items(ev) {
		if decided != "" && it.Plan != nil {
			it.Plan.RequestID = decided
		}
		c.emitLocked(agentapi.Event{Kind: agentapi.EventItem, Item: &it})
	}
}

func deltaEvent(agentID, id string, kind agentapi.ItemKind, text string) agentapi.Event {
	return agentapi.Event{Kind: agentapi.EventDelta, Delta: &agentapi.Delta{ItemID: id, Kind: kind, Text: text, AgentID: agentID}}
}

// agentOf returns the envelope's subagent instance ID, or "" for the main
// agent and session-level events.
func agentOf(ev copilot.SessionEvent) string {
	if ev.AgentID == nil {
		return ""
	}
	return *ev.AgentID
}

// nanoPerUnit converts the CLI's nano-AI units to AI units, the unit of the
// catalog's token prices (AI Credits).
const nanoPerUnit = 1e9

// emitContextLocked reports the context once a report gave its limit.
func (c *conversation) emitContextLocked() {
	if c.usage.Limit > 0 {
		usage := c.usage
		c.emitLocked(agentapi.Event{Kind: agentapi.EventContext, Context: &usage})
	}
}

// emitUsageLocked reports the conversation's AI units when they changed.
func (c *conversation) emitUsageLocked() {
	if c.nanoAIU == c.usageSent {
		return
	}
	c.usageSent = c.nanoAIU
	c.emitLocked(agentapi.Event{Kind: agentapi.EventUsage, Usage: &agentapi.Usage{AIUnits: c.nanoAIU / nanoPerUnit}})
}
