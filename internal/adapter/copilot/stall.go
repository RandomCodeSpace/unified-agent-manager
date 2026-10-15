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
	// note is the cancelled turn's note after a second stall.
	note string
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
			c.stall.timer = time.AfterFunc(webStallAfter, c.checkStall)
		}
	case *rpc.AssistantMessageData, *rpc.AssistantTurnEndData, *rpc.AssistantIdleData, *rpc.SessionIdleData, *rpc.AbortData:
		c.modelWaitAt = time.Time{}
	case *rpc.UserMessageData:
		if stallContinuation(d) {
			c.stall.aborting, c.stall.sending = false, false
		}
	}
}

// checkStall aborts a main-agent model request that has heard nothing for
// webStallAfter, and otherwise looks again when it could have.
func (c *conversation) checkStall() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stall.timer = nil
	if c.closed || !c.turnRunning || c.modelWaitAt.IsZero() {
		return
	}
	wait := webStallAfter - time.Since(c.lastEventAt)
	if wait <= 0 && (len(c.tr.tools) > 0 || len(c.pending) > 0 || c.stopPendingLocked() || c.stall.aborting) {
		wait = webStallAfter
	}
	if wait > 0 {
		c.stall.timer = time.AfterFunc(wait, c.checkStall)
		return
	}
	report := []any{"conversation", c.id, "turn_ms", time.Since(c.turnAt).Milliseconds(),
		"silence_ms", time.Since(c.lastEventAt).Milliseconds(), "last_event", c.lastEvent}
	if c.stall.retries > 0 {
		c.stall.note = fmt.Sprintf(stallGaveUpNote, seconds(webStallAfter))
		log.Warn("copilot model request stalled again; ending the turn", report...)
	} else {
		c.stall.aborting = true
		log.Warn("copilot model request stalled; retrying", report...)
	}
	c.stall.retries++
	go c.abortStalled(c.sess)
}

// abortStalled aborts the stalled request. A failed abort leaves the turn
// to Stop, and the watch looks again later.
func (c *conversation) abortStalled(sess sdkSession) {
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
		go c.sendContinuation(c.sess)
	}
	return true
}

// sendContinuation continues the turn after the stalled request. A refused
// continuation ends the turn as stopped, with the reason.
func (c *conversation) sendContinuation(sess sdkSession) {
	ctx, cancel := context.WithTimeout(context.Background(), webStopRPCWait)
	defer cancel()
	_, err := sess.Send(ctx, copilot.MessageOptions{Prompt: stallPrompt, DisplayPrompt: fmt.Sprintf(stallRetriedNote, seconds(webStallAfter)), Source: stallSource})
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
