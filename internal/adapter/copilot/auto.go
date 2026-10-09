package copilot

import (
	"crypto/rand"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// confirmAuto is deliberately a question, never a permission. The SDK owns
// the model switch; this callback consents to this request only.
func (c *conversation) confirmAuto(req copilot.AutoModeSwitchRequest, inv copilot.AutoModeSwitchInvocation) (copilot.AutoModeSwitchResponse, error) {
	text := "Switch to Auto for this request? Copilot will choose a model; usage and cost may differ."
	if req.RetryAfterSeconds != nil {
		n := *req.RetryAfterSeconds
		if n >= 0 && !math.IsNaN(n) && !math.IsInf(n, 0) {
			text += fmt.Sprintf(" The provider reports a rate-limit reset in %g seconds.", n)
		}
	}
	in := &interaction{
		Interaction: agentapi.Interaction{
			ID: "auto-" + rand.Text(), Kind: agentapi.InteractionQuestion,
			Title: "Auto model fallback", State: agentapi.InteractionPending, Time: time.Now(),
			Questions: []agentapi.Question{{Text: text, Choices: []string{"No", "Yes"}}},
		},
		reply: make(chan userReply, 1), auto: true,
	}
	c.mu.Lock()
	if c.closed || c.sess == nil || inv.SessionID != c.id {
		c.mu.Unlock()
		return rpc.AutoModeSwitchResponseNo, nil
	}
	c.pending[in.ID] = in
	c.emitInteractionLocked(in)
	c.mu.Unlock()
	r := <-in.reply
	c.mu.Lock()
	current := !c.closed && in.State == agentapi.InteractionAnswered
	c.mu.Unlock()
	if current && r.err == nil && !r.resp.WasFreeform && r.resp.Answer == "Yes" {
		return rpc.AutoModeSwitchResponseYes, nil
	}
	return rpc.AutoModeSwitchResponseNo, nil
}

// The SDK callback has no native RequestID, so requested/completed native
// notifications cannot safely identify or settle one of these questions.
// Only its private reply, or the normal interaction lifecycle, releases it.
func (c *conversation) expireAutoLocked() {
	for id, in := range c.pending {
		if !in.auto {
			continue
		}
		delete(c.pending, id)
		in.State = agentapi.InteractionExpired
		c.emitInteractionLocked(in)
		in.reply <- userReply{err: errNoUser}
	}
}

func selectionID(raw string, limit int) bool {
	return raw != "" && len(raw) <= limit && utf8.ValidString(raw) && strings.IndexFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0
}

func (c *conversation) autoModelSelectionLocked(d *rpc.SessionModelChangeData) {
	if d.Cause == nil || *d.Cause != "rate_limit_auto_switch" || !selectionID(d.NewModel, 120) {
		return
	}
	// A native receipt from before a later manual selection is stale.
	if d.PreviousModel != nil && *d.PreviousModel != "" && c.selected != "" && *d.PreviousModel != c.selected {
		return
	}
	selection := &agentapi.ModelSelection{Model: strings.Clone(d.NewModel)}
	if d.ReasoningEffort != nil && (*d.ReasoningEffort == "" || selectionID(*d.ReasoningEffort, 64)) {
		v := strings.Clone(*d.ReasoningEffort)
		selection.Effort = &v
	}
	if d.ContextTier != nil && (*d.ContextTier == rpc.ContextTierDefault || *d.ContextTier == rpc.ContextTierLongContext) {
		v := strings.Clone(string(*d.ContextTier))
		selection.ContextSize = &v
	}
	c.selected = selection.Model
	c.emitLocked(agentapi.Event{Kind: agentapi.EventModelSelection, ModelSelection: selection})
}
