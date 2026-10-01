package web

import (
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func TestSummaryAskNamesTheRequestTheTaskWaitsOn(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	conv.EmitTurn(agentapi.TurnWorking, "")
	q := agentapi.Interaction{ID: "q1", Kind: agentapi.InteractionQuestion, Title: "Question from Copilot", State: agentapi.InteractionPending,
		Questions: []agentapi.Question{{Text: "\nWhich option?\n\nMore context.", Choices: []string{"A (Recommended)", "B"}, Multiple: true}}}
	conv.EmitInteraction(q)
	waitUntil(t, "question ask", func() bool { return mustSummary(t, m, sum.ID).Ask != nil })
	got := mustSummary(t, m, sum.ID)
	if a := got.Ask; a.ID != "q1" || a.Kind != agentapi.InteractionQuestion || a.Title != "Which option?" || !a.Multiple || a.Custom || a.Questions != 1 || strings.Join(a.Choices, ",") != "A (Recommended),B" {
		t.Fatalf("question ask = %+v", a)
	}
	if again := mustSummary(t, m, sum.ID); again.Ask != got.Ask || again != got {
		t.Fatal("an unchanged ask must keep its pointer, so the summary compares equal")
	}

	// A permission outranks the question, as the state does; its detail is the first line.
	p := permissionRequest("p1")
	p.Detail = "rm -rf build\n\nWarning: destructive"
	conv.EmitInteraction(p)
	waitUntil(t, "permission ask", func() bool { a := mustSummary(t, m, sum.ID).Ask; return a != nil && a.ID == "p1" })
	if a := mustSummary(t, m, sum.ID).Ask; a.Kind != agentapi.InteractionPermission || a.Title != "Run rm -rf build?" || a.Detail != "rm -rf build" || len(a.Options) != 2 || a.Choices != nil {
		t.Fatalf("permission ask = %+v", a)
	}

	if _, err := m.Answer(sum.ID, "p1", agentapi.Answer{Decision: "allow"}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "question ask again", func() bool { a := mustSummary(t, m, sum.ID).Ask; return a != nil && a.ID == "q1" })
	if _, err := m.Answer(sum.ID, "q1", agentapi.Answer{Answers: [][]string{{"B"}}}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "no ask", func() bool { return mustSummary(t, m, sum.ID).Ask == nil })
}

func TestSummaryAskSkipsRequestsYoloAnswers(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createTask(t, m, prov, "yolo")
	mustSubmit(t, m, sum.ID, "work", mustUUID(t), ModeSend, SubmissionAccepted)
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.EmitInteraction(onceRequest("p1", ""))
	waitAllowed(t, m, sum.ID, "p1")
	if got := mustSummary(t, m, sum.ID); got.Ask != nil || got.EventAt.IsZero() || got.EventAt.Second() != 0 {
		t.Fatalf("summary = ask %+v, event_at %v", got.Ask, got.EventAt)
	}
}
