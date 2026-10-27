package copilot

import (
	"context"
	"fmt"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// A model request the CLI sends on a pooled connection that the network
// dropped without a close (a firewall's idle timeout on the way to a
// corporate proxy) gets no answer and no error: the turn waits for good,
// and so does every later request on that connection. Aborting the request
// makes the CLI discard the connection, and the next request opens a new
// one. So a main-agent model request that has heard nothing for
// webStallAfter, with no tool running and nothing waiting on the user, is
// aborted and the turn continued once with a note; a second stall in the
// same turn ends it as stopped. Tests shorten the bound.
var webStallAfter = 3 * time.Minute

// webStallMin is the shortest bound stallBoundLocked adapts to: below it a
// slow first token of a long prompt would read as a stall.
var webStallMin = time.Minute

// stallBoundLocked is how long the main agent's model request may stay
// silent before it counts as stalled: webStallAfter until the conversation
// has seen a request answered, then four times the longest silence an
// answered request showed, within webStallMin and webStallAfter. A Thread
// whose requests answer in seconds no longer waits three minutes.
func (c *conversation) stallBoundLocked() time.Duration {
	if c.modelGapMax <= 0 {
		return webStallAfter
	}
	return min(webStallAfter, max(webStallMin, 4*c.modelGapMax))
}

// stallSource marks uam's continuation of a stalled request on its user
// message, live and in the CLI's record, so it reads as a note and never as
// the turn's own message.
const stallSource = "uam-stall-retry"

// stallPrompt is what the model reads; the note is what the timeline shows.
const stallPrompt = "Your previous model request got no answer because of a network stall and was cancelled. Continue exactly where you left off."

const (
	stallRetriedNote = "No answer from the model for %s; uam retried the request."
	stallGaveUpNote  = "No answer from the model for %s, again after a retry. Send the message again to retry."
)

// stallState is the running turn's stall watch. Guarded by mu.
type stallState struct {
	timer *time.Timer
	// retries counts the stalls retried in the running turn.
	retries int
	// aborting is set from uam's abort of a stalled request until its
	// continuation reaches the CLI: the turn runs on through the abort's idle.
	aborting, sending bool
	// note is the cancelled turn's note after a second stall; silence the
	// bound the stalls exceeded, for the notes.
	note    string
	silence time.Duration
	// subWait follows each subagent's model requests by agent ID.
	subWait map[string]subagentWait
}

// subagentWait is one subagent's stall watch: whether it waits on a model
// request, when it was last heard from, and whether uam cancelled it.
type subagentWait struct {
	lastAt             time.Time
	waiting, cancelled bool
}

// stallContinuation reports whether d is uam's continuation of a stalled
// request.
func stallContinuation(d *rpc.UserMessageData) bool {
	return d.Source != nil && *d.Source == stallSource
}

// noteModelWaitLocked follows whether the main agent waits on a model
// request: from its assistant.turn_start until the model's message, the end
// of that call, or an idle.
func (c *conversation) noteModelWaitLocked(ev copilot.SessionEvent) {
	switch d := ev.Data.(type) {
	case *rpc.AssistantTurnStartData:
		c.modelWaitAt = time.Now()
		if c.stall.timer == nil {
			c.stall.timer = time.AfterFunc(c.stallBoundLocked(), c.checkStall)
		}
	case *rpc.AssistantMessageData, *rpc.AssistantTurnEndData, *rpc.AssistantIdleData, *rpc.SessionIdleData, *rpc.AbortData:
		c.modelWaitAt = time.Time{}
	case *rpc.UserMessageData:
		if stallContinuation(d) {
			c.stall.aborting, c.stall.sending = false, false
		}
	}
}

// noteSubagentWaitLocked is noteModelWaitLocked for a subagent, which the
// main agent's watch never sees: its parent's task call is a running tool.
func (c *conversation) noteSubagentWaitLocked(ev copilot.SessionEvent, agentID string) {
	w, seen := c.stall.subWait[agentID]
	switch ev.Data.(type) {
	case *rpc.AssistantTurnStartData:
		w.waiting = true
		if c.stall.timer == nil {
			c.stall.timer = time.AfterFunc(webStallAfter, c.checkStall)
		}
	case *rpc.AssistantMessageData, *rpc.AssistantTurnEndData, *rpc.AssistantIdleData, *rpc.SessionIdleData, *rpc.AbortData:
		w.waiting = false
	default:
		if !seen {
			return
		}
	}
	w.lastAt = time.Now()
	if c.stall.subWait == nil {
		c.stall.subWait = map[string]subagentWait{}
	}
	c.stall.subWait[agentID] = w
}

// checkStall aborts a main-agent model request that has heard nothing for
// webStallAfter, cancels such a subagent, and otherwise looks again when
// either could have stalled.
func (c *conversation) checkStall() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stall.timer = nil
	if c.closed || !c.turnRunning {
		return
	}
	next := c.checkSubagentStallsLocked()
	if c.modelWaitAt.IsZero() {
		if next > 0 {
			c.stall.timer = time.AfterFunc(next, c.checkStall)
		}
		return
	}
	bound := c.stallBoundLocked()
	wait := bound - time.Since(c.lastEventAt)
	if wait <= 0 && (len(c.tr.tools) > 0 || len(c.pending) > 0 || c.stopPendingLocked() || c.stall.aborting) {
		wait = bound
	}
	if wait > 0 {
		if next > 0 {
			wait = min(wait, next)
		}
		c.stall.timer = time.AfterFunc(wait, c.checkStall)
		return
	}
	c.stall.silence = bound
	report := []any{"conversation", c.id, "turn_ms", time.Since(c.turnAt).Milliseconds(),
		"silence_ms", time.Since(c.lastEventAt).Milliseconds(), "bound_ms", bound.Milliseconds(), "last_event", c.lastEvent}
	if c.stall.retries > 0 {
		c.stall.note = fmt.Sprintf(stallGaveUpNote, seconds(bound))
		c.p.stalls.Add(1)
		log.Warn("copilot model request stalled again; ending the turn", report...)
	} else {
		c.stall.aborting = true
		log.Warn("copilot model request stalled; retrying", report...)
	}
	c.stall.retries++
	go c.abortStalled(c.sess)
}

// checkSubagentStallsLocked cancels, once each, the subagents whose model
// request has heard nothing for webStallAfter while none of their tools runs,
// and reports when the next could stall, or zero.
func (c *conversation) checkSubagentStallsLocked() time.Duration {
	var next time.Duration
	busy := len(c.pending) > 0 || c.stopPendingLocked()
	for id, w := range c.stall.subWait {
		if sa := c.subs.byID[id]; c.stoppedSubagents[id] || sa != nil && sa.Status.Terminal() {
			delete(c.stall.subWait, id)
			continue
		}
		if !w.waiting || w.cancelled {
			continue
		}
		wait := webStallAfter - time.Since(w.lastAt)
		if wait <= 0 && (busy || c.subagentToolRunningLocked(id)) {
			wait = webStallAfter
		}
		if wait > 0 {
			if next == 0 || wait < next {
				next = wait
			}
			continue
		}
		w.cancelled = true
		c.stall.subWait[id] = w
		log.Warn("copilot subagent model request stalled; cancelling", "conversation", c.id, "agent", id,
			"silence_ms", time.Since(w.lastAt).Milliseconds())
		go c.cancelStalledSubagent(c.id, id)
	}
	return next
}

// subagentToolRunningLocked reports whether a tool call of the agent runs.
// The transcript's running calls carry no agent; toolAgents maps them.
func (c *conversation) subagentToolRunningLocked(agentID string) bool {
	for _, agent := range c.tr.toolAgents {
		if agent == agentID {
			return true
		}
	}
	return false
}

// cancelStalledSubagent cancels the stalled subagent; its parent's task call
// then ends and the main agent goes on.
func (c *conversation) cancelStalledSubagent(conv, agentID string) {
	defer log.Recover("copilot stalled subagent cancel")
	ctx, cancel := context.WithTimeout(context.Background(), webStopRPCWait)
	defer cancel()
	if err := c.CancelSubagent(ctx, agentID); err != nil {
		log.Warn("copilot stalled subagent not cancelled", "conversation", conv, "agent", agentID, "error", errText(err))
	}
}

// abortStalled aborts the stalled request. A failed abort leaves the turn
// to Stop, and the watch looks again later.
func (c *conversation) abortStalled(sess sdkSession) {
	defer log.Recover("copilot stall abort")
	ctx, cancel := context.WithTimeout(context.Background(), webStopRPCWait)
	defer cancel()
	err := sess.Abort(ctx)
	if err == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	log.Warn("copilot stalled request abort failed", "conversation", c.id, "error", errText(err))
	c.stall.aborting, c.stall.note = false, ""
	if !c.closed && c.turnRunning && c.stall.timer == nil {
		c.stall.timer = time.AfterFunc(webStallAfter, c.checkStall)
	}
}

// continueStalledLocked keeps the turn running through the idle that ends
// the stalled request uam aborted, and sends the continuation once. It
// reports whether the idle is that one; a Stop since the abort ends the
// turn as usual.
func (c *conversation) continueStalledLocked() bool {
	if !c.stall.aborting {
		return false
	}
	if c.stopPendingLocked() {
		c.stall.aborting = false
		return false
	}
	if !c.stall.sending {
		c.stall.sending = true
		// The CLI drops a steer it had not used when the request is aborted.
		c.undeliveredLocked(agentapi.TurnCancelled, time.Now())
		go c.sendContinuation(c.sess, c.stall.silence)
	}
	return true
}

// sendContinuation continues the turn after the stalled request, whose
// silence exceeded bound. A refused continuation ends the turn as stopped,
// with the reason.
func (c *conversation) sendContinuation(sess sdkSession, bound time.Duration) {
	defer log.Recover("copilot stall continuation")
	ctx, cancel := context.WithTimeout(context.Background(), webStopRPCWait)
	defer cancel()
	_, err := sess.Send(ctx, copilot.MessageOptions{Prompt: stallPrompt, DisplayPrompt: fmt.Sprintf(stallRetriedNote, seconds(bound)), Source: stallSource})
	if err == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	log.Warn("copilot stalled turn not continued", "conversation", c.id, "error", errText(err))
	if !c.stall.sending {
		return // the continuation arrived after all
	}
	c.stall.aborting, c.stall.sending = false, false
	c.stall.note = "uam could not retry the request: " + errText(err)
	c.finishTurnLocked(copilot.Bool(true), time.Now())
}

// stopPendingLocked reports whether a Stop for the running turn is pending.
func (c *conversation) stopPendingLocked() bool {
	return c.stopping != nil && c.stopping.idles == c.idles
}

// endStallLocked clears the stall watch of a turn that ended.
func (c *conversation) endStallLocked() {
	if c.stall.timer != nil {
		c.stall.timer.Stop()
	}
	c.stall = stallState{}
	c.modelWaitAt = time.Time{}
}
