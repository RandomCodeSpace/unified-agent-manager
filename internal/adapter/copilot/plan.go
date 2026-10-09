package copilot

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

const (
	maxPlanContent = 256 << 10
	maxPlanLabel   = 4 << 10
)

// planVersions retains only two bounded reader snapshots. The digest counts
// content revisions without mistaking duplicate durable evidence for a revision.
type planVersions struct {
	revision            int
	hash                [32]byte
	current, previous   string
	truncated           bool
	previousTruncated   bool
	previousUnavailable bool
	dropped             bool
}

func (v *planVersions) observe(content string) {
	hash := sha256.Sum256([]byte(content))
	if v.revision != 0 && v.hash == hash {
		if v.dropped {
			v.current = strings.Clone(clip(content, maxPlanContent))
			v.previousUnavailable, v.dropped = v.revision > 1, false
		}
		return
	}
	v.previousUnavailable = v.revision > 0 && v.dropped
	v.dropped = false
	v.previous, v.current = v.current, strings.Clone(clip(content, maxPlanContent))
	v.previousTruncated = v.truncated
	v.hash, v.truncated = hash, len(content) > maxPlanContent
	v.revision++
}

func (v *planVersions) review(id, summary string) agentapi.PlanReview {
	return agentapi.PlanReview{RequestID: id, Summary: planLabel(summary), Revision: v.revision, Content: v.current, Previous: v.previous, Truncated: v.truncated, PreviousTruncated: v.previousTruncated, PreviousUnavailable: v.previousUnavailable}
}

func planLabel(text string) string { return strings.Clone(clip(cleanText(text), maxPlanLabel)) }

func (v *planVersions) dropBodies() { v.current, v.previous, v.dropped = "", "", true }

func recordedPlan(ev copilot.SessionEvent) (id, summary, content string, ok bool) {
	switch d := ev.Data.(type) {
	case *rpc.ExitPlanModeRequestedData:
		return d.RequestID, d.Summary, d.PlanContent, d.RequestID != ""
	case *rpc.HumanResponseRecordedData:
		if response := planResponse(d.Response); response != nil {
			return d.RequestID, response.Summary, response.PlanContent, d.RequestID != ""
		}
	}
	return "", "", "", false
}

func planResponse(in rpc.HumanResponseRecordedResponse) *rpc.HumanResponseRecordedResponseExitPlanMode {
	switch response := in.(type) {
	case *rpc.HumanResponseRecordedResponseExitPlanMode:
		return response
	case rpc.HumanResponseRecordedResponseExitPlanMode:
		return &response
	default:
		return nil
	}
}

func planActionLabel(action agentapi.PlanAction) string {
	switch action {
	case agentapi.PlanAutopilot:
		return "Implement in autopilot"
	case agentapi.PlanAutopilotFleet:
		return "Implement with subagents"
	case agentapi.PlanInteractive:
		return "Implement interactively"
	case agentapi.PlanExitOnly:
		return "Leave planning without implementation"
	default:
		return "Execution action unavailable"
	}
}

// planItem shares compact decision conversion between live and cold history.
// Requested events seed metadata but do not create a second actionable review.
func (t *transcript) planItem(ev copilot.SessionEvent) (agentapi.Item, bool) {
	it := agentapi.Item{Time: ev.Timestamp, AgentID: agentOf(ev), Kind: agentapi.ItemNotice}
	id, summary, _, request := recordedPlan(ev)
	if request {
		if t.planReviews == nil {
			t.planReviews = map[string]agentapi.PlanReview{}
		}
		key := it.AgentID + "\x00" + id
		if len(t.planReviews) >= 200 && t.planReviews[key].RequestID == "" {
			clear(t.planReviews)
		}
		t.planReviews[key] = agentapi.PlanReview{RequestID: id, Summary: planLabel(summary)}
	}
	var approved *bool
	var action agentapi.PlanAction
	feedback := ""
	switch d := ev.Data.(type) {
	case *rpc.ExitPlanModeRequestedData:
		return it, false
	case *rpc.ExitPlanModeCompletedData:
		id, approved, feedback = d.RequestID, d.Approved, orEmpty(d.Feedback)
		if d.SelectedAction != nil {
			action = agentapi.PlanAction(*d.SelectedAction)
		}
	case *rpc.HumanResponseRecordedData:
		response := planResponse(d.Response)
		if response == nil {
			return it, false
		}
		id, approved, feedback = d.RequestID, &response.Approved, orEmpty(response.Feedback)
		if response.SelectedAction != nil {
			action = agentapi.PlanAction(*response.SelectedAction)
		}
	default:
		return it, false
	}
	if id == "" {
		return it, false
	}
	plan := t.planReviews[it.AgentID+"\x00"+id]
	plan.RequestID = id
	it.ID, it.Plan = "plan-"+id, &plan
	if it.AgentID != "" {
		// The SDK callback has no subagent identity; child receipts stay text.
		it.Plan = nil
	}
	first, second := "Plan review ended", "Decision unavailable"
	if approved != nil {
		if *approved {
			first, second = "Plan approved", planActionLabel(action)
		} else {
			first, second = "Plan not approved", "Review ended"
			if strings.TrimSpace(feedback) != "" {
				first, second = "Plan revision requested", clip(cleanText(feedback), maxPlanLabel)
			}
		}
	}
	if plan.Summary != "" {
		first += " · " + plan.Summary
	}
	it.Text = first + "\n" + second
	return it, true
}

func planAction(raw string) agentapi.PlanAction {
	action := agentapi.PlanAction(raw)
	if action.Valid() {
		return action
	}
	return ""
}

// reviewPlan is the sole response owner for the SDK's native review. The
// callback has no native request ID or deferred result, so it waits like
// askUser; requested/completed events never answer this callback a second time.
func (c *conversation) reviewPlan(req copilot.ExitPlanModeRequest, _ copilot.ExitPlanModeInvocation) (copilot.ExitPlanModeResult, error) {
	if len(req.PlanContent) > maxPlanContent {
		return copilot.ExitPlanModeResult{Feedback: "The plan is too large for web review. Shorten it before requesting approval."}, nil
	}
	c.mu.Lock()
	if c.closed || c.planStopped {
		c.mu.Unlock()
		return copilot.ExitPlanModeResult{}, errNoUser
	}
	metadata, id, epoch := c.plans, c.id, c.planEpoch
	c.mu.Unlock()
	// Settled turns retain only revision facts. Rehydrate bodies from the
	// provider's exact journal outside the callback lock for the next review.
	if (metadata.revision == 0 || metadata.dropped) && id != "" {
		ctx, cancel := context.WithTimeout(context.Background(), webTasksTimeout)
		versions, _, err := c.p.readPlanVersions(ctx, agentapi.ReadRequest{ConversationID: id}, "", &metadata)
		cancel()
		if err == nil && (metadata.revision == 0 || versions.revision == metadata.revision && versions.hash == metadata.hash) {
			c.mu.Lock()
			if !c.closed && !c.planStopped && c.planEpoch == epoch && c.plans.revision == metadata.revision && c.plans.hash == metadata.hash && (metadata.revision == 0 || c.plans.dropped) {
				c.plans = versions
			}
			c.mu.Unlock()
		}
	}
	plan := &agentapi.PlanReview{Summary: planLabel(req.Summary), Content: req.PlanContent}
	for _, raw := range req.Actions {
		if action := planAction(raw); action != "" && !slices.Contains(plan.Actions, action) {
			plan.Actions = append(plan.Actions, action)
		}
	}
	if len(plan.Actions) == 0 {
		return copilot.ExitPlanModeResult{Feedback: "This runtime offered no supported plan actions. The plan was not approved."}, nil
	}
	if action := planAction(req.RecommendedAction); slices.Contains(plan.Actions, action) {
		plan.Recommended = action
	}
	in := &interaction{Interaction: agentapi.Interaction{ID: "plan-" + rand.Text(), Kind: agentapi.InteractionPlanReview, Title: "Plan ready", State: agentapi.InteractionPending, Time: time.Now(), Plan: plan}, planReply: make(chan copilot.ExitPlanModeResult, 1)}
	plan.RequestID = in.ID
	c.mu.Lock()
	if c.closed || c.planStopped || c.planEpoch != epoch {
		c.mu.Unlock()
		return copilot.ExitPlanModeResult{}, errNoUser
	}
	c.plans.observe(req.PlanContent)
	plan.Revision, plan.Previous, plan.PreviousTruncated = c.plans.revision, c.plans.previous, c.plans.previousTruncated
	plan.PreviousUnavailable = c.plans.previousUnavailable
	c.pending[in.ID] = in
	c.emitInteractionLocked(in)
	c.mu.Unlock()
	return <-in.planReply, nil
}

func (c *conversation) answerPlanLocked(in *interaction, ans agentapi.Answer) error {
	if ans.Plan == nil || ans.Decision != "" || len(ans.Answers) > 0 || ans.Reject || ans.Auto {
		return errors.New("copilot: a plan review takes an explicit plan answer")
	}
	a := ans.Plan
	if a.Action != "" && a.Feedback != "" || a.Action == "" && strings.TrimSpace(a.Feedback) == "" || len(a.Feedback) > agentapi.MaxPlanFeedbackBytes {
		return errors.New("copilot: a plan review takes one action or bounded feedback")
	}
	if a.Action != "" && !slices.Contains(in.Plan.Actions, a.Action) {
		return errors.New("copilot: the plan action was not offered")
	}
	delete(c.pending, in.ID)
	in.State = agentapi.InteractionAnswered
	in.Resolution = "feedback sent"
	if a.Action != "" {
		in.Resolution = string(a.Action)
	}
	clearPlanBody(in)
	c.emitInteractionLocked(in)
	c.answeredPlanLocked(answeredPlan{id: in.ID, approved: a.Action != "", action: string(a.Action), feedback: a.Feedback})
	if c.turnRunning {
		c.act.now.Plan = a.Action != "" && a.Action != agentapi.PlanExitOnly
		c.emitActivityLocked()
	}
	in.planReply <- copilot.ExitPlanModeResult{Approved: a.Action != "", SelectedAction: string(a.Action), Feedback: a.Feedback}
	return nil
}

const planCancelled = "The plan review was cancelled. Do not start implementation."

// maxAnsweredPlans bounds the answers waiting for their native receipt.
const maxAnsweredPlans = 8

// answeredPlan is an answer given to the review id, as the native receipt
// of the decision records it. A conversation's planAnswered holds those
// whose receipt has not arrived, oldest first.
type answeredPlan struct {
	id, action, feedback string
	approved             bool
}

func (c *conversation) answeredPlanLocked(a answeredPlan) {
	if len(c.planAnswered) >= maxAnsweredPlans {
		c.planAnswered = c.planAnswered[1:]
	}
	c.planAnswered = append(c.planAnswered, a)
}

// decidedPlanLocked returns the review whose answer the native receipt d
// records: the oldest one waiting, when the decision matches it; "" for a
// receipt UAM's callback did not answer.
func (c *conversation) decidedPlanLocked(d *rpc.ExitPlanModeCompletedData) string {
	if len(c.planAnswered) == 0 {
		return ""
	}
	a := c.planAnswered[0]
	action := ""
	if d.SelectedAction != nil {
		action = string(*d.SelectedAction)
	}
	if d.Approved == nil || *d.Approved != a.approved || action != a.action || orEmpty(d.Feedback) != a.feedback {
		return ""
	}
	c.planAnswered = c.planAnswered[1:]
	return a.id
}

func clearPlanBody(in *interaction) {
	if in.Plan != nil {
		plan := *in.Plan
		plan.Content, plan.Previous = "", ""
		in.Plan = &plan
	}
}

// expirePlansLocked releases review callbacks before Stop talks to the runtime.
// It never returns approval or changes the Task's permission policy.
func (c *conversation) expirePlansLocked() {
	c.planEpoch++
	for id, in := range c.pending {
		if in.planReply == nil {
			continue
		}
		delete(c.pending, id)
		in.State = agentapi.InteractionExpired
		clearPlanBody(in)
		c.emitInteractionLocked(in)
		c.answeredPlanLocked(answeredPlan{id: in.ID, feedback: planCancelled})
		in.planReply <- copilot.ExitPlanModeResult{Feedback: planCancelled}
	}
	c.plans.dropBodies()
}

type planSession interface {
	ReadPlan(context.Context) (*rpc.PlanReadResult, error)
}

func (a sdkSessionAdapter) ReadPlan(ctx context.Context) (*rpc.PlanReadResult, error) {
	return a.s.RPC.Plan.Read(ctx)
}

func (c *conversation) ReadPlanDraft(ctx context.Context) (agentapi.PlanDraft, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return agentapi.PlanDraft{}, agentapi.ErrClosed
	}
	runtime, ok := c.sess.(planSession)
	c.mu.Unlock()
	if !ok {
		return agentapi.PlanDraft{}, agentapi.ErrUnsupported
	}
	plan, err := runtime.ReadPlan(ctx)
	if err != nil {
		return agentapi.PlanDraft{}, err
	}
	if plan == nil {
		return agentapi.PlanDraft{}, errors.New("copilot: empty plan read result")
	}
	draft := agentapi.PlanDraft{Exists: plan.Exists}
	if draft.Exists && plan.Content != nil {
		draft.Truncated = len(*plan.Content) > maxPlanContent
		draft.Content = strings.Clone(clip(displaytext.SanitizeText(*plan.Content), maxPlanContent))
	}
	return draft, nil
}

// checkPlanLocked keeps only the scratch file's exact identity. The body is
// read with its review, not retained in session summaries or progress state.
func (c *conversation) checkPlanLocked() {
	if c.closed || c.sess == nil {
		return
	}
	runtime, ok := c.sess.(planSession)
	if !ok {
		return
	}
	if c.planReading {
		c.planAgain = true
		return
	}
	c.planReading = true
	go func() {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), webTasksTimeout)
			plan, err := runtime.ReadPlan(ctx)
			cancel()
			c.mu.Lock()
			if !c.closed && !c.planAgain && err == nil && plan != nil && plan.Path != nil && filepath.IsAbs(*plan.Path) {
				path := filepath.Clean(*plan.Path)
				if path != c.planPath {
					c.planPath = path
					c.emitLocked(agentapi.Event{Kind: agentapi.EventPlanPath, PlanPath: path})
				}
			}
			again := c.planAgain && !c.closed
			c.planReading, c.planAgain = again, false
			c.mu.Unlock()
			if !again {
				return
			}
		}
	}()
}

// ReadPlanReview folds the native journal at the exact request boundary. It
// neither resumes a closed Task nor substitutes today's scratch-plan body.
func (p *webProvider) ReadPlanReview(ctx context.Context, req agentapi.ReadRequest, id string) (agentapi.PlanReview, error) {
	_, found, err := p.readPlanVersions(ctx, req, id, nil)
	if err != nil {
		return agentapi.PlanReview{}, err
	}
	if found == nil {
		return agentapi.PlanReview{}, agentapi.ErrItemNotFound
	}
	found.Content = displaytext.SanitizeText(found.Content)
	found.Previous = displaytext.SanitizeText(found.Previous)
	return *found, nil
}

func (p *webProvider) readPlanVersions(ctx context.Context, req agentapi.ReadRequest, id string, prior *planVersions) (planVersions, *agentapi.PlanReview, error) {
	if req.ConversationID == "" {
		return planVersions{}, nil, errors.New("copilot: ReadRequest.ConversationID is required")
	}
	client, err := p.ensureStarted(ctx)
	if err != nil {
		return planVersions{}, nil, err
	}
	var found *agentapi.PlanReview
	var versions planVersions
	var restored *planVersions
	for attempt := 1; ; attempt++ {
		found = nil
		versions = planVersions{}
		restored = nil
		seen := map[string]bool{}
		err = p.readForward(ctx, client, req.ConversationID, func(ev copilot.SessionEvent) {
			requestID, summary, content, ok := recordedPlan(ev)
			if !ok || agentOf(ev) != "" || seen[requestID] {
				return
			}
			seen[requestID] = true
			versions.observe(content)
			if prior != nil && prior.revision > 0 && versions.revision == prior.revision && versions.hash == prior.hash {
				copy := versions
				restored = &copy
			}
			if requestID == id {
				plan := versions.review(id, summary)
				found = &plan
			}
		})
		if !errors.Is(err, errJournalChanged) || attempt == webWindowAttempts {
			break
		}
	}
	if err != nil {
		return planVersions{}, nil, err
	}
	if restored != nil {
		versions = *restored
	}
	return versions, found, nil
}
