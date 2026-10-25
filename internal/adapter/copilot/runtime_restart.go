package copilot

import (
	"context"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// RestartRuntime stops the running CLI whatever runs on it, so the next use
// starts a new one with fresh connections: the owner's way out, from the
// browser, when model requests stall on connections the network dropped. A
// running turn ends as stopped with reason as its note first, so its Thread
// reads Stopped rather than failed; an idle conversation ends and reopens on
// its next use, as after an idle close. Starts wait meanwhile. With no CLI
// running it does nothing.
func (p *webProvider) RestartRuntime(ctx context.Context, reason string) error {
	release, err := p.holdStarts()
	if err != nil {
		return err
	}
	defer release()
	p.mu.Lock()
	c := p.detachLocked()
	convs := p.takeConvs()
	p.stalls.Store(0)
	p.mu.Unlock()
	for _, conv := range convs {
		conv.interrupt(reason)
	}
	if c == nil {
		return nil
	}
	log.Info("copilot CLI restarted on request", "conversations", len(convs))
	return p.stopDetached(ctx, c)
}

// RuntimeStalls counts the turns the running CLI's model requests stalled
// twice in since it started.
func (p *webProvider) RuntimeStalls() int { return int(p.stalls.Load()) }

// interrupt ends the conversation for a runtime restart. A running turn ends
// as stopped with reason first, and the exit then reads as detached: the
// Thread keeps its state and resumes on its next use rather than failing.
func (c *conversation) interrupt(reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	running := c.turnRunning
	if running {
		// A stall retry under way must not continue the turn this ends.
		c.stall.aborting = false
		aborted := true
		c.stopNote = reason
		c.finishTurnLocked(&aborted, time.Now())
		c.stopNote = ""
	}
	c.endLocked(agentapi.Event{Kind: agentapi.EventExit, Error: reason, Detached: running})
}
