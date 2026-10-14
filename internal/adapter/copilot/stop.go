package copilot

import (
	"context"
	"slices"
	"sync"
	"time"

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
)

// stopUnconfirmed is the note of a turn uam stopped itself.
const stopUnconfirmed = "Copilot did not confirm the stop"

// stopWatch is a Stop's check that the running turn ended. Guarded by mu.
type stopWatch struct {
	at    time.Time
	idles int
	ping  time.Duration
	// abort is "pending" until Abort returns, then "ok" or its error.
	abort    string
	abortFor time.Duration
}

// lockWithin takes mu if it can within d, and reports whether it did.
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
	w := &stopWatch{at: time.Now(), idles: c.idles, ping: webStopPingWait, abort: "pending"}
	c.stopping = w
	time.AfterFunc(webStopGrace, func() { c.stopUnconfirmed(w) })
	return w
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

// stopEndedLocked reports whether the turn w checks has ended.
func (c *conversation) stopEndedLocked(w *stopWatch) bool {
	return c.closed || c.idles != w.idles || !c.turnRunning && c.sending == 0
}

// stopUnconfirmed ends a turn the CLI did not end after Stop. A CLI that
// does not answer a ping is gone, as when the watchdog finds it so; one
// that answers keeps running, and only this conversation ends.
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
	ping, outcome := "ok", "conversation detached"
	if err != nil {
		ping, outcome = errText(err), "runtime failed"
	}
	report = append(report, "ping", ping, "ping_ms", time.Since(began).Milliseconds(), "outcome", outcome)
	log.Warn("copilot did not end the turn after Stop", report...)
	if err != nil {
		c.p.fail(c.client, "Copilot CLI stopped answering: "+exitText(err))
		return
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
// it. The CLI drops a turn when its session's last owner detaches.
func (c *conversation) detach(w *stopWatch) {
	c.mu.Lock()
	if c.stopEndedLocked(w) {
		c.mu.Unlock()
		return
	}
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
	if err := c.release(context.Background(), nil); err != nil {
		log.Warn("copilot stop: detaching the session failed", "conversation", c.id, "error", err)
	}
}
