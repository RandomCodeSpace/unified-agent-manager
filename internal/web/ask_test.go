package web

import (
	"context"
	"sync"
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
	if a := got.Ask; *a != (Ask{Kind: agentapi.InteractionQuestion, Title: "Which option?"}) {
		t.Fatalf("question ask = %+v", a)
	}
	if again := mustSummary(t, m, sum.ID); again.Ask != got.Ask || again != got {
		t.Fatal("an unchanged ask must keep its pointer, so the summary compares equal")
	}

	// A permission outranks the question, as the state does.
	conv.EmitInteraction(permissionRequest("p1"))
	waitUntil(t, "permission ask", func() bool {
		a := mustSummary(t, m, sum.ID).Ask
		return a != nil && *a == (Ask{Kind: agentapi.InteractionPermission, Title: "Run rm -rf build?"})
	})

	if _, err := m.Answer(sum.ID, "p1", agentapi.Answer{Decision: "allow"}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "question ask again", func() bool {
		a := mustSummary(t, m, sum.ID).Ask
		return a != nil && a.Kind == agentapi.InteractionQuestion
	})
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
	// Hold yolo's answer, so the request stays pending while yolo answers it.
	release, answering := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	conv.SetRespondHook(func(context.Context, string, agentapi.Answer) error {
		close(answering)
		<-release
		return nil
	})
	conv.EmitInteraction(onceRequest("p1", ""))
	<-answering
	if ix := interactionOf(t, m, sum.ID, "p1"); ix.State != agentapi.InteractionPending || !ix.Auto {
		t.Fatalf("held request = %+v", ix)
	}
	if got := mustSummary(t, m, sum.ID); got.Ask != nil {
		t.Fatalf("ask while yolo answers = %+v", got.Ask)
	}
	unblock()
	waitAllowed(t, m, sum.ID, "p1")
	if got := mustSummary(t, m, sum.ID); got.Ask != nil || got.EventAt.IsZero() || got.EventAt.Second() != 0 {
		t.Fatalf("summary = ask %+v, event_at %v", got.Ask, got.EventAt)
	}
}
