package copilot

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// Stop's bounds: a running turn ends within about 20 seconds of Stop
// whatever the CLI does. Tests shorten them.
var (
	// webStopGrace is how long the CLI has after Stop to end the turn
	// before uam ends it itself.
	webStopGrace = 10 * time.Second
	// webStopLockWait bounds Stop's wait for another control operation.
	webStopLockWait = 2 * time.Second
	// webStopRPCWait bounds each of Stop's RPCs: the mode change and the
	// abort.
	webStopRPCWait = 4 * time.Second
	// webStopPingWait bounds the ping that tells a stuck session from a
	// stuck CLI.
	webStopPingWait = 5 * time.Second
	// webDetachWait is how long after a detach starts a reopen of the same
	// session waits for it; release stops waiting then too.
	webDetachWait = webStopTimeout
)

// stopUnconfirmed is the note of a turn uam stopped itself.
const stopUnconfirmed = "Copilot did not confirm the stop"

// stopWatch is a Stop's check that the running turn ended. Guarded by mu.
type stopWatch struct {
	at    time.Time
	idles int
	// ping and call are the ping's and a late abort's bounds.
	ping, call time.Duration
	// send is set while the watch waits on a prompt on its way, which has
	// started no turn yet; ended once that prompt started none.
	send, ended bool
	// abort is "pending" until Abort returns, then "ok" or its error.
	abort    string
	abortFor time.Duration
}

// lockWithin takes mu if it can within d, and reports whether it did.
// TryLock fails while mu is in starvation mode even when it is free, so a
// contended mu can make the caller wait the whole of d and go on without
// it: Stop accepts that delay, which d bounds.
func lockWithin(mu *sync.Mutex, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for !mu.TryLock() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
	return true
}

// watchStopLocked arms the check that the running turn, or the one a prompt
// on its way starts, ends within webStopGrace, and returns it; nil when
// nothing runs. A repeated Stop keeps the first one's deadline.
func (c *conversation) watchStopLocked() *stopWatch {
	if !c.turnRunning && c.sending == 0 {
		return nil
	}
	if w := c.stopping; w != nil && w.idles == c.idles {
		return w
	}
	w := &stopWatch{at: time.Now(), idles: c.idles, ping: webStopPingWait, call: webStopRPCWait, send: !c.turnRunning, abort: "pending"}
	c.stopping = w
	time.AfterFunc(webStopGrace, func() { c.stopUnconfirmed(w) })
	return w
}

// sendEndedLocked ends the stop check when the prompts it waited on are all
// sent or refused and none started a turn: a later prompt's turn is not the
// one Stop was for.
func (c *conversation) sendEndedLocked() {
	if w := c.stopping; w != nil && !c.turnRunning && c.sending == 0 {
		w.ended = true
		c.stopping = nil
	}
}

// abortStartedLocked aborts, once and on its own goroutine, the turn a
// prompt started after a Stop overtook it: the Stop's own abort reached the
// CLI before that turn did.
func (c *conversation) abortStartedLocked() {
	w := c.stopping
	if w == nil || !w.send || !c.turnRunning || c.idles != w.idles {
		return
	}
	w.send = false
	sess := c.sess
	go func() {
		defer log.Recover("copilot abort")
		ctx, cancel := context.WithTimeout(context.Background(), w.call)
		defer cancel()
		began := time.Now()
		err := sess.Abort(ctx)
		c.aborted(w, err, time.Since(began))
	}()
}

// aborted records Abort's result for the stop check w.
func (c *conversation) aborted(w *stopWatch, err error, took time.Duration) {
	if w == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	w.abort, w.abortFor = "ok", took
	if err != nil {
		w.abort = errText(err)
	}
}

// leaveAutopilotLater turns autopilot off once control is free, after a Stop
// of a running turn that could not: the next turn must not run on its own.
// A mode chosen since, or a turn started since, stands. A conversation that
// has ended by then reopens in the mode the CLI kept, which the Thread
// shows.
func (c *conversation) leaveAutopilotLater(cause error) {
	runtime, ok := c.sess.(executionSession)
	c.mu.Lock()
	interactive := c.execution != nil && c.execution.Known && c.execution.Mode == string(rpc.SessionModeInteractive)
	ops, idles := c.modeOps, c.idles
	c.mu.Unlock()
	if !ok || interactive && cause == nil {
		return
	}
	go func() {
		defer log.Recover("copilot stop check")
		c.control.Lock()
		defer c.control.Unlock()
		c.mu.Lock()
		closed, chosen := c.closed, c.modeOps != ops || c.turnRunning && c.idles != idles
		c.mu.Unlock()
		switch {
		case closed:
			log.Warn("copilot stop: the conversation ended before its execution mode was reset", "conversation", c.id)
			return
		case chosen:
			log.Info("copilot stop: keeping the execution mode chosen after Stop", "conversation", c.id)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), webStopTimeout)
		defer cancel()
		if err := runtime.SetExecutionMode(ctx, rpc.SessionModeInteractive); err != nil {
			log.Warn("copilot stop: resetting the execution mode failed", "conversation", c.id, "error", errText(err))
		}
		c.refreshExecution(ctx)
	}()
}

// stopEndedLocked reports whether the turn w checks has ended.
func (c *conversation) stopEndedLocked(w *stopWatch) bool {
	return w.ended || c.closed || c.idles != w.idles || !c.turnRunning && c.sending == 0
}

// stopUnconfirmed ends a turn the CLI did not end after Stop. A ping that
// fails proves the CLI gone, as for the watchdog. One that answers, or only
// times out (a busy CLI can miss one; the watchdog judges it), leaves the
// CLI running and ends only this conversation.
func (c *conversation) stopUnconfirmed(w *stopWatch) {
	c.mu.Lock()
	if c.stopping == w {
		c.stopping = nil
	}
	if c.stopEndedLocked(w) {
		c.mu.Unlock()
		return
	}
	report := c.stopReportLocked(w)
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), w.ping)
	began := time.Now()
	err := c.client.Ping(ctx)
	cancel()
	gone := err != nil && !errors.Is(err, context.DeadlineExceeded)
	ping, outcome := "ok", "conversation detached"
	switch {
	case gone:
		ping, outcome = errText(err), "runtime failed"
	case err != nil:
		ping = errText(err)
	}
	report = append(report, "ping", ping, "ping_ms", time.Since(began).Milliseconds(), "outcome", outcome)
	log.Warn("copilot did not end the turn after Stop", report...)
	if gone {
		c.p.fail(c.client, "Copilot CLI stopped answering: "+exitText(err))
		return
	}
	if err != nil {
		c.p.poke()
	}
	c.detach(w)
}

// stopReportLocked describes the turn w checks, without message content.
func (c *conversation) stopReportLocked(w *stopWatch) []any {
	now := time.Now()
	report := []any{"conversation", c.id, "since_stop_ms", now.Sub(w.at).Milliseconds(),
		"abort", w.abort, "abort_ms", w.abortFor.Milliseconds(), "sending", c.sending}
	if c.turnRunning {
		report = append(report, "turn_ms", now.Sub(c.turnAt).Milliseconds())
	}
	if c.lastEvent != "" {
		report = append(report, "last_event", c.lastEvent, "last_event_age_ms", now.Sub(c.lastEventAt).Milliseconds())
	}
	var tools []string
	for _, tc := range c.tr.tools {
		tools = append(tools, tc.Name)
	}
	slices.Sort(tools)
	permissions, questions := 0, 0
	for _, in := range c.pending {
		if in.Kind == agentapi.InteractionPermission {
			permissions++
		} else {
			questions++
		}
	}
	subagents := 0
	for _, sa := range c.subs.byID {
		if sa.Status == agentapi.SubagentRunning {
			subagents++
		}
	}
	return append(report, "running_tools", tools, "pending_permissions", permissions,
		"pending_questions", questions, "running_subagents", subagents)
}

// detach ends the turn w checks as stopped and this conversation as at an
// exit of it alone, then detaches its SDK session; the next prompt resumes
// it. The CLI drops a turn when its session's last owner detaches. Pending
// permission requests are answered as unavailable first, as by Close: one
// may be what the turn waits on.
func (c *conversation) detach(w *stopWatch) {
	c.mu.Lock()
	if c.stopEndedLocked(w) {
		c.mu.Unlock()
		return
	}
	perms := c.expireLocked()
	// The detach is due before the exit lets anything reopen it.
	detached := c.p.detachStarted(c.id)
	// A prompt still on its way has started no turn here yet: end the one
	// the Thread shows.
	aborted := true
	c.foregroundIdle = false
	c.stopNote = stopUnconfirmed
	c.finishTurnLocked(&aborted, time.Now())
	c.stopNote = ""
	c.endLocked(agentapi.Event{Kind: agentapi.EventExit, Error: stopUnconfirmed, Detached: true})
	if c.aside != nil {
		c.aside()
	}
	c.mu.Unlock()
	if err := c.release(context.Background(), perms, detached); err != nil {
		log.Warn("copilot stop: detaching the session failed", "conversation", c.id, "error", err)
	}
}
