package copilot

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

type steer struct {
	id, prompt  string
	state       agentapi.TurnState
	at          time.Time
	attachments []agentapi.Attachment
}

// Send starts a turn, and refuses with ErrBusy while one is running. It sends
// no mode: the CLI then delivers the prompt at once whenever its main agent is
// idle, background shells or not. An explicit "enqueue", and any prompt sent
// during a turn, is held until session.idle, which the CLI withholds while a
// background shell runs. Referenced files go as file and directory
// attachments with their absolute paths.
func (c *conversation) Send(ctx context.Context, prompt agentapi.Prompt) error {
	return c.send(ctx, copilot.MessageOptions{Prompt: prompt.Text, Attachments: attachments(prompt)})
}

func (c *conversation) send(ctx context.Context, msg copilot.MessageOptions) error {
	c.mu.Lock()
	closed, running, idles := c.closed, c.turnRunning, c.idles
	generation := c.tr.completions.generation
	if !closed && !running {
		c.sending++
	}
	c.mu.Unlock()
	if closed {
		return agentapi.ErrClosed
	}
	if running {
		return agentapi.ErrBusy
	}
	defer func() {
		c.mu.Lock()
		c.sending--
		// An error recorded for a prompt that started no turn belongs to
		// none: it must not fail a later one.
		if c.sending == 0 && !c.turnRunning {
			c.turnErr = ""
		}
		c.sendEndedLocked()
		c.mu.Unlock()
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.refreshCustomVision(ctx); err != nil {
		return err
	}
	// No turn runs: once the first proof ends, the uam tools a failed proof
	// or a tool list change refused are proved again. The message goes
	// either way.
	if c.tools != nil {
		if err := c.tools.reprove(ctx, c.sess, c.uamTools...); err != nil {
			log.Warn("copilot uam tools stay refused until the next message", "conversation", c.id, "error", err)
		}
		// A rebuild that could not keep the Task's model ended the
		// conversation; nothing is sent on the agent's model.
		if c.isClosed() {
			return agentapi.ErrClosed
		}
	}
	c.mu.Lock()
	c.completionReady = false
	c.mu.Unlock()
	if _, err := c.sess.Send(ctx, msg); err != nil {
		return c.sendError(err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// An idle may already have ended it, or a native foreground start may
	// already have begun it. Preserve a receipt that arrived after that start.
	if c.idles == idles && c.tr.completions.generation == generation {
		c.startTurnLocked()
	}
	c.abortStartedLocked()
	return nil
}

// sendError reports a JSON-RPC error answer as a plain rejection: the CLI
// received the prompt and refused it. Any other failure after the call
// started may have happened after the request was written (timeout, CLI exit,
// closed pipe), so it is reported as uncertain.
func (c *conversation) sendError(err error) error {
	var rej rejectedError
	if errors.As(err, &rej) {
		return fmt.Errorf("copilot rejected the prompt: %s", errText(rej.err))
	}
	c.p.poke()
	return fmt.Errorf("%w: %s", agentapi.ErrSubmissionUncertain, errText(err))
}

// Steer sends prompt, with its attachments, in the "immediate" mode: the CLI
// folds it into the running turn before its next model call, and moves a running foreground
// shell command to the background. It reports no turn transition. The
// message ID the CLI returns links the steer to its user message: that
// message is marked as a steer, and a steer a stopped or failed turn ends
// without is reported as not delivered (the CLI drops unused steers on abort).
func (c *conversation) Steer(ctx context.Context, prompt agentapi.Prompt) error {
	for _, attachment := range prompt.Attachments {
		if strings.HasPrefix(attachment.MIME, "image/") {
			if err := c.refreshCustomVision(ctx); err != nil {
				return err
			}
			break
		}
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return agentapi.ErrClosed
	}
	st := &steer{prompt: prompt.Text}
	c.steers = append(c.steers, st)
	c.steering++
	c.mu.Unlock()
	id, err := "", ctx.Err()
	var sentAttachments []copilot.Attachment
	if err == nil {
		sentAttachments = attachments(prompt)
		if id, err = c.sess.Send(ctx, copilot.MessageOptions{Prompt: prompt.Text, Attachments: sentAttachments, Mode: string(rpc.SendModeImmediate)}); err != nil {
			err = c.sendError(err)
		} else if id == "" {
			err = fmt.Errorf("%w: Copilot returned no message ID for the steer", agentapi.ErrSubmissionUncertain)
		}
	}
	var receiptAttachments []agentapi.Attachment
	if err == nil {
		receiptAttachments = blobAttachments(sentAttachments, nil, nil)
		for i := range receiptAttachments {
			// Native document delivery is known only from the provider echo.
			receiptAttachments[i].NotNative = false
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.steering--
	used := c.seen[id]
	if c.steering == 0 {
		clear(c.seen)
	}
	st.id = id
	if err != nil || id == "" || used || c.closed {
		c.steers = slices.DeleteFunc(c.steers, func(pending *steer) bool { return pending == st })
	} else {
		// Send accepted the exact message ID. Show a receipt while its user
		// event is delayed; the real provider item upserts this same ID.
		st.attachments = receiptAttachments
		it := agentapi.Item{ID: id, Kind: agentapi.ItemUser, Text: prompt.Text, Time: time.Now(), Delivery: agentapi.DeliverySteer, SteerStatus: agentapi.SteerAccepted, Attachments: st.attachments}
		c.emitLocked(agentapi.Event{Kind: agentapi.EventItem, Item: &it})
		if st.state != "" {
			c.undeliveredSteerLocked(st)
		}
	}
	return err
}

// undeliveredLocked reports each steer the ending turn did not use as a
// notice. The CLI drops unused steers when a turn is aborted. A steer still
// pending at a completed idle is not reported: the CLI either used it in that
// turn or, when it arrived after the idle, starts a new turn with it.
func (c *conversation) undeliveredLocked(state agentapi.TurnState, at time.Time) {
	for _, st := range c.steers {
		st.state, st.at = state, at
		if st.id != "" {
			c.undeliveredSteerLocked(st)
		}
	}
	c.steers = nil
}

func (c *conversation) undeliveredSteerLocked(st *steer) {
	reason := "the turn was stopped"
	switch st.state {
	case agentapi.TurnCompleted:
		return
	case agentapi.TurnFailed:
		reason = "the turn failed"
	}
	receipt := agentapi.Item{ID: st.id, Kind: agentapi.ItemUser, Text: st.prompt, Time: st.at, Delivery: agentapi.DeliverySteer, SteerStatus: agentapi.SteerNotDelivered, Attachments: st.attachments}
	c.emitLocked(agentapi.Event{Kind: agentapi.EventItem, Item: &receipt})
	text := "Steer not delivered: " + reason
	if st.prompt != "" { // an attachment-only steer has no text to quote
		text += "\n\n" + quote(st.prompt)
	}
	it := agentapi.Item{ID: "steer-undelivered:" + st.id, Kind: agentapi.ItemNotice, Time: st.at, Text: text}
	c.emitLocked(agentapi.Event{Kind: agentapi.EventItem, Item: &it})
}

// quote renders text as a markdown block quote.
func quote(text string) string {
	return "> " + strings.ReplaceAll(clip(text, maxToolText), "\n", "\n> ")
}
