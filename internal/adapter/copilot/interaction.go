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
)

type interaction struct {
	permissionUse          agentapi.ToolUse // original tool plus native permission kind
	permissionHookResolved bool             // native hook requests never become owner interactions
	agentapi.Interaction
	decisions map[string]rpc.PermissionDecision // permissions: option id -> decision
	reply     chan userReply                    // questions: releases the blocked SDK handler
	planReply chan copilot.ExitPlanModeResult   // native review: one explicit owner response
	answering bool                              // a permission answer is in flight
	auto      bool                              // Auto fallback needs a person's explicit Yes
	onceKey   string                            // permissions: the session option's scope, "" without one
	// Elicitations only: the CLI's request ID once its event is linked, and
	// the key that links it.
	requestID, elicitKey string
}

type userReply struct {
	resp   copilot.UserInputResponse
	elicit copilot.ElicitationResult
	err    error
}

func (c *conversation) emitInteractionLocked(in *interaction) {
	if in.permissionHookResolved {
		return
	}
	v := in.Interaction
	c.emitLocked(agentapi.Event{Kind: agentapi.EventInteraction, Interaction: &v})
}

func (c *conversation) Respond(ctx context.Context, id string, ans agentapi.Answer) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return agentapi.ErrClosed
	}
	in := c.pending[id]
	if in == nil || in.answering || in.permissionHookResolved {
		c.mu.Unlock()
		return agentapi.ErrInteractionGone
	}
	if in.planReply != nil {
		defer c.mu.Unlock()
		return c.answerPlanLocked(in, ans)
	}
	if in.reply != nil {
		defer c.mu.Unlock()
		return c.answerLocked(in, ans)
	}
	decision, ok := in.decisions[ans.Decision]
	if !ok {
		c.mu.Unlock()
		return fmt.Errorf("copilot: unknown permission decision %q", ans.Decision)
	}
	if _, once := decision.(*rpc.PermissionDecisionApproveOnce); once && ans.Auto {
		decision = &rpc.PermissionDecisionApproveOnce{} // no person approved it
	}
	in.answering = true
	c.mu.Unlock()

	applied, err := c.sess.RespondPermission(ctx, id, decision)

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending[id] != in { // closed or withdrawn while the answer was in flight
		if err == nil && !applied {
			return agentapi.ErrInteractionGone
		}
		return err
	}
	if err != nil {
		in.answering = false
		c.p.poke()
		return fmt.Errorf("copilot permission answer: %s", errText(err))
	}
	delete(c.pending, id)
	switch {
	case !applied:
		in.State = agentapi.InteractionExpired
	case ans.Decision == "reject":
		in.State, in.Resolution = agentapi.InteractionRejected, optionLabel(in.Options, ans.Decision)
	default:
		in.State, in.Resolution = agentapi.InteractionAnswered, optionLabel(in.Options, ans.Decision)
		if ans.Decision == "approve_once" && !ans.Auto && in.onceKey != "" {
			if c.approvedOnce == nil {
				c.approvedOnce = map[string]int{}
			}
			c.approvedOnce[in.onceKey]++
		}
	}
	c.emitInteractionLocked(in)
	if !applied {
		return agentapi.ErrInteractionGone
	}
	return nil
}

// answerLocked releases a blocked ask_user handler. A rejected question
// returns an error to the CLI instead of inventing an answer.
func (c *conversation) answerLocked(in *interaction, ans agentapi.Answer) error {
	if in.auto && ans.Auto {
		return errors.New("copilot: Auto fallback needs an explicit user answer")
	}
	if in.Elicitation != nil {
		return c.answerElicitationLocked(in, ans)
	}
	if ans.Cancel {
		return errors.New("copilot: only a form or a link can be cancelled")
	}
	q := in.Questions[0]
	var r userReply
	if ans.Reject {
		r.err = errDeclined
		in.State = agentapi.InteractionRejected
	} else {
		if len(ans.Answers) != 1 || len(ans.Answers[0]) == 0 || slices.Contains(ans.Answers[0], "") {
			return errors.New("copilot: a question needs an answer")
		}
		if len(ans.Answers[0]) > 1 && !q.Multiple {
			return errors.New("copilot: a question needs exactly one answer")
		}
		// The CLI takes one string back: several chosen options go joined, as a free-form answer.
		a := strings.Join(ans.Answers[0], ", ")
		chosen := len(ans.Answers[0]) == 1 && slices.Contains(q.Choices, a)
		if !chosen && !q.Custom {
			return errors.New("copilot: the answer must be one of the offered choices")
		}
		r.resp = copilot.UserInputResponse{Answer: a, WasFreeform: !chosen}
		in.State = agentapi.InteractionAnswered
	}
	delete(c.pending, in.ID)
	c.settledLocked(in, time.Now())
	c.emitInteractionLocked(in)
	in.reply <- r
	return nil
}

// optionLabel returns the label the user saw for a permission option.
func optionLabel(options []agentapi.Option, id string) string {
	for _, o := range options {
		if o.ID == id {
			return o.Label
		}
	}
	return id
}

// expireSubagentLocked withdraws only this agent's abandoned interactions.
// The CLI may leave its permissions pending after a successful stop.
func (c *conversation) expireSubagentLocked(agentID string) {
	for id, in := range c.pending {
		if in.AgentID != agentID {
			continue
		}
		delete(c.pending, id)
		in.State, in.Resolution = agentapi.InteractionExpired, "the subagent was stopped"
		c.emitInteractionLocked(in)
		if in.reply != nil {
			c.settledLocked(in, time.Now())
			c.elicits.settled(in)
			in.reply <- userReply{err: errNoUser}
		}
	}
}

// expireLocked withdraws every pending interaction that is not being
// answered: blocked questions get an error, never an answer. It returns the
// permission request ids the CLI may still be waiting on.
func (c *conversation) expireLocked() []string {
	c.expirePlansLocked()
	var perms []string
	for id, in := range c.pending {
		if in.answering {
			continue
		}
		delete(c.pending, id)
		if in.permissionHookResolved {
			continue
		}
		in.State = agentapi.InteractionExpired
		c.emitInteractionLocked(in)
		if in.reply != nil {
			c.settledLocked(in, time.Now())
			c.elicits.settled(in)
			in.reply <- userReply{err: errNoUser}
		} else {
			perms = append(perms, id)
		}
	}
	return perms
}
