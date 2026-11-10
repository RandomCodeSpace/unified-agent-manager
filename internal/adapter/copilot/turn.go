package copilot

import (
	"maps"
	"time"

	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

func (c *conversation) startTurnLocked() {
	c.releaseTurnLocked()
	if !c.turnRunning {
		c.planEpoch++
		c.planStopped = false
		c.turnAt = time.Now()
		c.forgetUnendedToolsLocked()
	}
	c.startActivityLocked()
	c.foregroundIdle, c.turnRunning = false, true
	c.idleUnresolved, c.taskCompleted, c.autopilotStopped = false, false, false
	c.completion = nil
	c.completionReady = false
	c.autopilotTurn = c.execution != nil && c.execution.Mode == "autopilot"
	// The phase now with the main agent's own call, and the subagents and
	// compaction that ran before the turn; it goes first, as the call that
	// starts a turn does.
	c.emitPhaseLocked()
	c.emitLocked(agentapi.Event{Kind: agentapi.EventTurn, Turn: &agentapi.Turn{State: agentapi.TurnWorking}})
}

// forgetUnendedToolsLocked drops, when a turn starts, the transcript's
// running calls of the main agent and of subagents no longer running. Their
// end was never reported (a call an abort interrupted), and nothing else
// would drop them. At the start and not the earlier turn's end: an end
// reported late still finds its call.
func (c *conversation) forgetUnendedToolsLocked() {
	for id, agent := range c.tr.toolAgents {
		if !c.subagentRunsLocked(agent) {
			delete(c.tr.tools, id)
			delete(c.tr.toolAgents, id)
			delete(c.tr.shells, id)
			delete(c.tr.clippedInput, id)
		}
	}
	maps.DeleteFunc(c.tr.hidden, func(_, agent string) bool { return !c.subagentRunsLocked(agent) })
}

// turnFailure is what a session error says beside its message: its category
// as an ErrorKind, and the provider's code, HTTP status and remediation
// action when it sent them.
func turnFailure(d *rpc.SessionErrorData) agentapi.TurnFailure {
	f := agentapi.TurnFailure{Kind: agentapi.ErrorKindOf(d.ErrorType), Code: clip(displaytext.Sanitize(deref(d.ErrorCode)), maxErrorCode)}
	if d.StatusCode != nil {
		f.Status = int(*d.StatusCode)
	}
	if d.Remediation != nil {
		f.Remediation = clip(displaytext.Sanitize(string(*d.Remediation)), maxErrorCode)
	}
	return f
}

func (c *conversation) finishTurnLocked(aborted *bool, at time.Time) {
	if c.foregroundIdle {
		// An error and the idle that ends its turn can both arrive before the
		// prompt's send returns, which is what starts the turn: start it
		// here. Otherwise the send starts it afterwards, with no idle left
		// to end it.
		if c.sending == 0 || c.turnErr == "" {
			return
		}
		c.startTurnLocked()
	}
	if aborted != nil && *aborted && c.continueStalledLocked() {
		return
	}
	c.foregroundIdle, c.turnRunning = true, false
	c.planStopped = true
	c.expirePlansLocked()
	c.idleUnresolved = false
	c.idles++
	turn := agentapi.Turn{State: agentapi.TurnCompleted, Model: c.turnModel, Completion: c.completion}
	switch {
	case aborted != nil && *aborted:
		turn.State, turn.Reason, turn.Error = agentapi.TurnCancelled, c.act.abortReason, c.stopNote
		if turn.Error == "" {
			turn.Error = c.stall.note
		}
		c.expireLocked()
	case c.turnErr != "":
		failure := c.turnFailure
		turn.State, turn.Error, turn.Failure = agentapi.TurnFailed, c.turnErr, &failure
	}
	c.turnErr, c.turnModel = "", ""
	c.act = activity{}
	c.endBackgroundCompactionLocked()
	turn.ReadOnceHits, turn.StallGaveUp = c.viewRepeats, c.stall.retries > 1
	if c.viewRepeats > 0 {
		log.Info("copilot read-once: repeat views not re-sent", "session", c.id, "count", c.viewRepeats)
		c.viewRepeats = 0
	}
	// Subagents still running go on between turns: their phase follows the
	// turn's end (emitTurnEndLocked).
	c.endTurnPhaseLocked()
	c.act.now.Phase = c.phase.snapshot(false)
	c.endStallLocked()
	c.undeliveredLocked(turn.State, at)
	c.emitTurnEndLocked(turn)
	c.completion = nil
}
