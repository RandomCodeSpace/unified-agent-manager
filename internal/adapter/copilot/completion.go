package copilot

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

const maxCompletionEvents = 1024

type completionContext struct {
	generation uint64
	userID     string
}

// completionTrace follows native parent links, not timestamps or the CLI's
// reusable turn IDs. A parent link names the chronologically preceding
// durable event, and events arrive in order. The live stream lacks some
// durable events the journal keeps (internal records such as
// session.usage_record), so a durable event whose parent never arrived
// follows the last durable event that did. Only a receipt must name a known
// parent: one that does not keeps no anchor. Ephemeral events are not in
// the journal's chain, so they are not kept and cannot evict its links.
type completionTrace struct {
	generation uint64
	events     map[string]completionContext
	users      map[string]string
	last       completionContext
}

func completionID(id string) string {
	if len(id) > 256 || !utf8.ValidString(id) || strings.ContainsFunc(id, unicode.IsControl) {
		return ""
	}
	return id
}

func (t *completionTrace) observe(ev copilot.SessionEvent) (completionContext, bool) {
	if old, ok := t.events[ev.ID]; ok && ev.ID != "" {
		return old, false
	}
	durable := ev.Ephemeral == nil || !*ev.Ephemeral
	var ctx completionContext
	if ev.ParentID != nil {
		var known bool
		ctx, known = t.events[*ev.ParentID]
		if _, receipt := ev.Data.(*rpc.SessionTaskCompleteData); !known && durable && !receipt {
			ctx = t.last
		}
	}
	if !durable {
		return ctx, true
	}
	agentID := agentOf(ev)
	if d, ok := ev.Data.(*rpc.UserMessageData); ok && completionID(agentID) == agentID && (d.Delivery == nil || *d.Delivery != rpc.UserMessageDeliverySteering) && (d.IsAutopilotContinuation == nil || !*d.IsAutopilotContinuation) {
		id := ev.ID
		if d.MessageID != nil && *d.MessageID != "" {
			id = *d.MessageID
		}
		if t.users == nil {
			t.users = map[string]string{}
		}
		if len(t.users) >= maxCompletionEvents {
			clear(t.users)
		}
		t.users[agentID] = completionID(id)
		if agentID == "" {
			ctx.userID = t.users[agentID]
		}
	}
	if _, start := ev.Data.(*rpc.AssistantTurnStartData); start && agentID == "" {
		t.generation++
		ctx.generation = t.generation
	}
	if id := completionID(ev.ID); id != "" {
		if t.events == nil {
			t.events = map[string]completionContext{}
		}
		if len(t.events) >= maxCompletionEvents {
			clear(t.events)
		}
		t.events[id] = ctx
	}
	t.last = ctx
	return ctx, true
}

func completionDecision(d *rpc.SessionTaskCompleteData) agentapi.CompletionDecision {
	if d.Outcome != nil {
		switch *d.Outcome {
		case rpc.TaskCompletionOutcomeCompleted:
			if d.Success == nil || *d.Success {
				return agentapi.CompletionAccepted
			}
		case rpc.TaskCompletionOutcomeContinue:
			if d.Success == nil || !*d.Success {
				return agentapi.CompletionRejected
			}
		case rpc.TaskCompletionOutcomeBlocked:
			if d.Success == nil || !*d.Success {
				return agentapi.CompletionBlocked
			}
		}
		return agentapi.CompletionUnknown
	}
	if d.Success != nil {
		if *d.Success {
			return agentapi.CompletionAccepted
		}
		return agentapi.CompletionRejected
	}
	return agentapi.CompletionUnknown
}

func completionText(text *string, limit int) string {
	if text == nil {
		return ""
	}
	return strings.Clone(clip(strings.TrimSpace(displaytext.Sanitize(*text)), limit))
}

func (t *transcript) completion(ev copilot.SessionEvent, d *rpc.SessionTaskCompleteData) *agentapi.TaskCompletion {
	c := &agentapi.TaskCompletion{Decision: completionDecision(d), Summary: completionText(d.Summary, 512), Reason: completionText(d.Reason, 256)}
	if b := d.Blocker; b != nil {
		c.Blocker = &agentapi.CompletionBlocker{Kind: strings.Clone(clip(displaytext.Sanitize(string(b.Kind)), 64)), Reason: strings.Clone(clip(displaytext.Sanitize(string(b.Reason)), 64)), Resumable: b.Resumable}
	}
	if c.Decision == agentapi.CompletionUnknown && c.Summary == "" && c.Reason == "" && c.Blocker == nil && d.Success == nil && d.Outcome == nil {
		return nil // Old empty receipts carry no new displayable facts.
	}
	if agentID := agentOf(ev); agentID != "" {
		c.UserItemID = t.completions.users[agentID]
	} else {
		c.UserItemID = t.completions.events[ev.ID].userID
	}
	return c
}

func completionNotice(c *agentapi.TaskCompletion) string {
	label := "Completion decision unknown"
	switch c.Decision {
	case agentapi.CompletionAccepted:
		label = "Completion accepted"
	case agentapi.CompletionRejected:
		label = "Completion rejected"
	case agentapi.CompletionBlocked:
		label = "Completion blocked"
	}
	literal := func(text string) string {
		fence := "`"
		for strings.Contains(text, fence) {
			fence += "`"
		}
		return fence + " " + text + " " + fence
	}
	parts := []string{label}
	if c.Summary != "" {
		parts = append(parts, "Summary: "+literal(c.Summary))
	}
	if c.Reason != "" {
		parts = append(parts, "Reason: "+literal(c.Reason))
	}
	if b := c.Blocker; b != nil {
		resumable := "no"
		if b.Resumable {
			resumable = "yes"
		}
		parts = append(parts, "Blocker kind: "+literal(b.Kind), "Blocker reason: "+literal(b.Reason), "Resumable: "+resumable)
	}
	return strings.Join(parts, " · ")
}
