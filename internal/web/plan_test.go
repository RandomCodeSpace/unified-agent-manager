package web

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

type draftPlanConversation struct {
	agentapi.Conversation
	draft agentapi.PlanDraft
	err   error
	read  func()
}

func (c *draftPlanConversation) ReadPlanDraft(context.Context) (agentapi.PlanDraft, error) {
	if c.read != nil {
		c.read()
	}
	return c.draft, c.err
}

func TestPlanDraftReadsOnlyTheExistingConversation(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	runtime := &draftPlanConversation{Conversation: conv, draft: agentapi.PlanDraft{Exists: true}}
	m.mu.Lock()
	m.sessions[sum.ID].conv = runtime
	m.mu.Unlock()
	draft, err := m.PlanDraft(t.Context(), sum.ID)
	if err != nil || !draft.Exists || draft.Content != "" {
		t.Fatalf("empty existing draft = %+v %v", draft, err)
	}
	runtime.draft = agentapi.PlanDraft{Exists: false, Content: "stale"}
	draft, err = m.PlanDraft(t.Context(), sum.ID)
	if err != nil || draft.Exists || draft.Content != "" {
		t.Fatalf("deleted draft = %+v %v", draft, err)
	}
	runtime.err = errors.New("disk failed")
	if _, err := m.PlanDraft(t.Context(), sum.ID); statusOf(err) != http.StatusBadGateway {
		t.Fatalf("read failure = %v", err)
	}
	runtime.err = nil
	runtime.read = func() { m.mu.Lock(); m.sessions[sum.ID].gen++; m.mu.Unlock() }
	if _, err := m.PlanDraft(t.Context(), sum.ID); statusOf(err) != http.StatusConflict {
		t.Fatalf("replaced conversation = %v", err)
	}
	runtime.read = nil
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	opens := len(prov.Opens())
	if _, err := m.PlanDraft(t.Context(), sum.ID); statusOf(err) != http.StatusConflict {
		t.Fatalf("closed current draft = %v", err)
	}
	if len(prov.Opens()) != opens {
		t.Fatal("draft reader resumed a closed Task")
	}
}

func TestPlanVersionInvalidationContainsNoBody(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	conv.Emit(agentapi.Event{Kind: agentapi.EventPlanPath, PlanVersion: 1})
	if d := detail(t, m, sum.ID); d.PlanVersion != 1 {
		t.Fatalf("version = %d", d.PlanVersion)
	}
}

type historyPlanProvider struct {
	*agenttest.Provider
	plan agentapi.PlanReview
	err  error
	read agentapi.ReadRequest
}

func (p *historyPlanProvider) ReadPlanReview(_ context.Context, request agentapi.ReadRequest, _ string) (agentapi.PlanReview, error) {
	p.read = request
	return p.plan, p.err
}

func TestPlanReaderDoesNotReopenAClosedTaskOrSubstituteAnotherReview(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, _ := createSession(t, m, prov)
	reader := &historyPlanProvider{Provider: prov, plan: agentapi.PlanReview{RequestID: "native-review", Content: "# Reviewed\n\nPreserve data."}}
	m.mu.Lock()
	m.providers[prov.Name()] = reader
	m.mu.Unlock()
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	opens := len(prov.Opens())
	plan, err := m.PlanReview(t.Context(), sum.ID, "native-review")
	if err != nil || plan.Content != reader.plan.Content || reader.read.ConversationID != sum.ConversationID {
		t.Fatalf("exact review = %+v, %v, request %+v", plan, err, reader.read)
	}
	if len(prov.Opens()) != opens {
		t.Fatal("closed reader reopened the Task")
	}
	reader.plan.RequestID = "another-review"
	if _, err := m.PlanReview(t.Context(), sum.ID, "native-review"); statusOf(err) != http.StatusBadGateway {
		t.Fatalf("different native review = %v", err)
	}
	reader.err = agentapi.ErrItemNotFound
	if _, err := m.PlanReview(t.Context(), sum.ID, "native-review"); statusOf(err) != http.StatusNotFound {
		t.Fatalf("missing native review = %v", err)
	}
}

func TestPlanReviewTakesOneOfferedActionOrFeedback(t *testing.T) {
	review := agentapi.Interaction{ID: "plan-review", Kind: agentapi.InteractionPlanReview, Title: "Plan ready", Plan: &agentapi.PlanReview{RequestID: "plan-review", Content: "Keep the data", Actions: []agentapi.PlanAction{agentapi.PlanInteractive, agentapi.PlanExitOnly}}}
	for _, tc := range []struct {
		name   string
		answer agentapi.Answer
		valid  bool
	}{
		{"interactive", agentapi.Answer{Plan: &agentapi.PlanAnswer{Action: agentapi.PlanInteractive}}, true},
		{"exit only", agentapi.Answer{Plan: &agentapi.PlanAnswer{Action: agentapi.PlanExitOnly}}, true},
		{"feedback", agentapi.Answer{Plan: &agentapi.PlanAnswer{Feedback: "Revise step two"}}, true},
		{"unoffered", agentapi.Answer{Plan: &agentapi.PlanAnswer{Action: agentapi.PlanAutopilotFleet}}, false},
		{"mixed plan", agentapi.Answer{Plan: &agentapi.PlanAnswer{Action: agentapi.PlanInteractive, Feedback: "Also revise"}}, false},
		{"mixed blank feedback", agentapi.Answer{Plan: &agentapi.PlanAnswer{Action: agentapi.PlanInteractive, Feedback: " "}}, false},
		{"mixed question", agentapi.Answer{Plan: &agentapi.PlanAnswer{Action: agentapi.PlanInteractive}, Answers: [][]string{{"yes"}}}, false},
		{"mixed permission", agentapi.Answer{Plan: &agentapi.PlanAnswer{Action: agentapi.PlanInteractive}, Decision: "allow"}, false},
		{"mixed form cancel", agentapi.Answer{Plan: &agentapi.PlanAnswer{Action: agentapi.PlanInteractive}, Cancel: true}, false},
		{"empty", agentapi.Answer{Plan: &agentapi.PlanAnswer{}}, false},
		{"blank feedback", agentapi.Answer{Plan: &agentapi.PlanAnswer{Feedback: "  "}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAnswer(review, tc.answer)
			if (err == nil) != tc.valid {
				t.Fatalf("validation = %v, valid=%v", err, tc.valid)
			}
		})
	}
}

func TestPlanScratchExclusionUsesOnlyTheKnownExactPath(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	m.mu.Lock()
	s := m.sessions[sum.ID]
	s.workdir = t.TempDir()
	planPath := filepath.Join(s.workdir, "scratch", "plan.md")
	m.mu.Unlock()
	at := time.Now()
	conv.EmitItem(toolEdit("plan-write", "edit", planPath, agentapi.ToolCompleted, at))
	conv.EmitItem(toolEdit("project-plan", "edit", "docs/plan.md", agentapi.ToolCompleted, at))
	conv.Emit(agentapi.Event{Kind: agentapi.EventPlanPath, PlanPath: planPath})
	conv.EmitItem(toolEdit("plan-write-again", "edit", "scratch/plan.md", agentapi.ToolCompleted, at.Add(time.Second)))
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(s.edits) != 1 || s.edits["docs/plan.md"].IsZero() {
		t.Fatalf("project edit attribution = %+v", s.edits)
	}
	if s.isPlanPath("docs/plan.md") || s.isPlanPath(filepath.Join(s.workdir, "plan.md")) {
		t.Fatal("basename was treated as a scratch plan identity")
	}
	// The browser excludes the same exact file from a turn's changed files.
	if d := m.detailLocked(s); d.PlanPath != planPath {
		t.Fatalf("detail plan path = %q", d.PlanPath)
	}
}

func TestPlanActivityBelongsOnlyToTheCurrentTurn(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.Emit(agentapi.Event{Kind: agentapi.EventActivity, Activity: &agentapi.Activity{Plan: true}})
	if !detail(t, m, sum.ID).TurnActivity.Plan {
		t.Fatal("approved current turn lost plan context")
	}
	conv.EmitTurn(agentapi.TurnCompleted, "")
	conv.Emit(agentapi.Event{Kind: agentapi.EventActivity, Activity: &agentapi.Activity{Plan: true}})
	if detail(t, m, sum.ID).TurnActivity.Plan {
		t.Fatal("late plan context attached to an ended turn")
	}
	conv.EmitTurn(agentapi.TurnWorking, "")
	if detail(t, m, sum.ID).TurnActivity.Plan {
		t.Fatal("plan context carried into a new turn")
	}
}

func TestPlanReviewNeedsOwnerAndDropsResolvedBodies(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	review := agentapi.Interaction{ID: "plan-review", Kind: agentapi.InteractionPlanReview, Title: "Plan ready", State: agentapi.InteractionPending, Time: time.Now(), Plan: &agentapi.PlanReview{RequestID: "plan-review", Summary: "Preserve data", Content: "# Plan", Previous: "# Old plan", Actions: []agentapi.PlanAction{agentapi.PlanInteractive}}}
	conv.EmitInteraction(review)
	d := detail(t, m, sum.ID)
	if d.Pending != 1 || d.Ask == nil || d.Ask.Kind != agentapi.InteractionPlanReview || d.Ask.Title != "Plan ready" {
		t.Fatalf("plan ask = %+v", d.SessionSummary)
	}
	answered, err := m.Answer(sum.ID, review.ID, agentapi.Answer{Plan: &agentapi.PlanAnswer{Action: agentapi.PlanInteractive}})
	if err != nil {
		t.Fatal(err)
	}
	if answered.Resolution != "interactive" || answered.Plan.Content != "" || answered.Plan.Previous != "" {
		t.Fatalf("resolved review = %+v", answered)
	}
	if _, err := m.Answer(sum.ID, review.ID, agentapi.Answer{Plan: &agentapi.PlanAnswer{Action: agentapi.PlanInteractive}}); statusOf(err) != http.StatusConflict {
		t.Fatalf("second answer = %v", err)
	}
	if review.Plan.Content != "# Plan" {
		t.Fatal("answer mutated the provider's emitted snapshot")
	}
}

// Yolo answers permission requests only: a plan review waits for the owner's
// explicit choice, when it arrives and when Yolo is chosen while it waits,
// even one that carries an allow-once option.
func TestYoloNeverAnswersAPlanReview(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	if _, err := m.SetMode(sum.ID, "yolo"); err != nil {
		t.Fatal(err)
	}
	review := agentapi.Interaction{ID: "plan-review", Kind: agentapi.InteractionPlanReview, Title: "Plan ready", State: agentapi.InteractionPending, Time: time.Now(),
		Options: []agentapi.Option{{ID: "once", Label: "Allow", AllowOnce: true}},
		Plan:    &agentapi.PlanReview{RequestID: "plan-review", Content: "# Plan", Actions: []agentapi.PlanAction{agentapi.PlanAutopilot}}}
	conv.EmitInteraction(review)
	for _, mode := range []string{"safe", "yolo"} {
		if _, err := m.SetMode(sum.ID, mode); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(30 * time.Millisecond)
	if ix := interactionOf(t, m, sum.ID, review.ID); ix.State != agentapi.InteractionPending || ix.Auto || len(conv.Responds()) != 0 {
		t.Fatalf("yolo answered the plan review: %+v, responds %d", ix, len(conv.Responds()))
	}
}

// The provider's journal may hold no review bodies: a review's bounded
// snapshot is kept by UAM under the review ID the browser reads, and under
// the provider's request ID its transcript receipt carries, before and after
// the answer, the close and a restart.
func TestPlanReaderServesKeptReviewsByBothIDs(t *testing.T) {
	m, prov, st := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	const review, native = "plan-QJ2W5Z7KXN3M4R6T8V2B4D6F8H", "5b0c9b0e-6f1d-4d0a-9c39-0c7f3b1f2a11"
	conv.EmitInteraction(agentapi.Interaction{ID: review, Kind: agentapi.InteractionPlanReview, Title: "Plan ready", State: agentapi.InteractionPending, Time: time.Now(), Plan: &agentapi.PlanReview{RequestID: review, Summary: "Preserve data", Revision: 2, Content: "# Plan v2", Previous: "# Plan v1", Actions: []agentapi.PlanAction{agentapi.PlanInteractive}}})
	read := func(m *Manager, rid string) agentapi.PlanReview {
		t.Helper()
		plan, err := m.PlanReview(t.Context(), sum.ID, rid)
		if err != nil || plan.RequestID != rid || plan.Content != "# Plan v2" || plan.Previous != "# Plan v1" || plan.Revision != 2 {
			t.Fatalf("read %s = %+v, %v", rid, plan, err)
		}
		return plan
	}
	read(m, review)
	if _, err := m.Answer(sum.ID, review, agentapi.Answer{Plan: &agentapi.PlanAnswer{Action: agentapi.PlanInteractive}}); err != nil {
		t.Fatal(err)
	}
	conv.EmitItem(agentapi.Item{ID: "plan-" + native, Kind: agentapi.ItemNotice, Text: "Plan approved\nImplement interactively", Plan: &agentapi.PlanReview{RequestID: review}})
	read(m, review)
	read(m, native)
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted := startManager(t, st, prov)
	read(restarted, review)
	read(restarted, native)
	if _, err := restarted.PlanReview(t.Context(), sum.ID, "plan-unknown"); statusOf(err) != http.StatusConflict && statusOf(err) != http.StatusNotFound {
		t.Fatalf("unknown review = %v", err)
	}
}
