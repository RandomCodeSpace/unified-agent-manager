package web

import (
	"context"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func phaseEvent(p *agentapi.Phase) agentapi.Event {
	return agentapi.Event{Kind: agentapi.EventActivity, Activity: &agentapi.Activity{Phase: p}}
}

func shownPhase(t *testing.T, m *Manager, id string) *Phase {
	t.Helper()
	a := detail(t, m, id).TurnActivity
	if a == nil {
		return nil
	}
	return a.Phase
}

func TestPhaseWaitingBeatsModelBeatsLocal(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	at := func(sec int64) time.Time { return time.Unix(sec, 0).UTC() }

	// A call reported before the turn starts shows once it does.
	conv.Emit(phaseEvent(&agentapi.Phase{Calls: 2, Subagents: 1, Model: "gpt-6-\x1b[1mluna", CallsSince: at(10)}))
	if p := shownPhase(t, m, sum.ID); p != nil {
		t.Fatalf("phase outside a turn = %+v", p)
	}
	conv.EmitTurn(agentapi.TurnWorking, "")
	if p := shownPhase(t, m, sum.ID); p == nil || p.Kind != PhaseModel || p.Model != "gpt-6-luna" || !p.Since.Equal(at(10)) || p.Calls != 2 || p.Subagents != 1 {
		t.Fatalf("model phase = %+v", p)
	}

	// A permission request waits on the owner, whatever else runs.
	conv.EmitItem(agentapi.Item{ID: "call-1", Kind: agentapi.ItemTool, Time: at(11), Tool: &agentapi.ToolCall{Name: "bash", Status: agentapi.ToolRunning}})
	ask := agentapi.Interaction{ID: "perm-1", Kind: agentapi.InteractionPermission, Title: "Run a command", State: agentapi.InteractionPending, Time: at(12), ToolCallID: "call-1"}
	conv.EmitInteraction(ask)
	if p := shownPhase(t, m, sum.ID); p == nil || p.Kind != PhaseWaiting || p.Detail != "approve bash" || !p.Since.Equal(at(12)) || p.Calls != 2 {
		t.Fatalf("waiting phase = %+v", p)
	}
	ask.State = agentapi.InteractionAnswered
	conv.EmitInteraction(ask)
	if p := shownPhase(t, m, sum.ID); p == nil || p.Kind != PhaseModel {
		t.Fatalf("after the answer = %+v", p)
	}

	// No call in flight and a tool running: local, with its argument.
	conv.Emit(phaseEvent(&agentapi.Phase{Tool: "bash", Input: `{"command":"go test\n  ./internal/vterm/..."}`, ToolSince: at(13)}))
	if p := shownPhase(t, m, sum.ID); p == nil || p.Kind != PhaseLocal || p.Tool != "bash" || p.Detail != "go test ./internal/vterm/..." || !p.Since.Equal(at(13)) || p.Calls != 0 {
		t.Fatalf("local phase = %+v", p)
	}
	// A question: answer it.
	conv.EmitInteraction(agentapi.Interaction{ID: "q-1", Kind: agentapi.InteractionQuestion, Title: "Question", State: agentapi.InteractionPending, Time: at(14), Questions: []agentapi.Question{{Text: "Which?", Custom: true}}})
	if p := shownPhase(t, m, sum.ID); p == nil || p.Kind != PhaseWaiting || p.Detail != "answer question" {
		t.Fatalf("question phase = %+v", p)
	}
	conv.EmitInteraction(agentapi.Interaction{ID: "q-1", Kind: agentapi.InteractionQuestion, Title: "Question", State: agentapi.InteractionExpired, Time: at(14)})

	// Nothing in flight: no phase; the turn's end clears what was.
	conv.Emit(phaseEvent(nil))
	if p := shownPhase(t, m, sum.ID); p != nil {
		t.Fatalf("idle phase = %+v", p)
	}
	conv.Emit(phaseEvent(&agentapi.Phase{Calls: 1, CallsSince: at(15)}))
	conv.EmitTurn(agentapi.TurnCompleted, "")
	if p := shownPhase(t, m, sum.ID); p != nil {
		t.Fatalf("phase after the turn = %+v", p)
	}
	// A later turn does not show the last one's phase.
	conv.EmitTurn(agentapi.TurnWorking, "")
	if p := shownPhase(t, m, sum.ID); p != nil {
		t.Fatalf("new turn phase = %+v", p)
	}
}

func TestModelCallsCountThreadsSubagentsLocalAndBackgroundAI(t *testing.T) {
	defer func(d time.Duration) { modelCallsDelay = d }(modelCallsDelay)
	modelCallsDelay = 5 * time.Millisecond
	m, prov, _ := newTestManager(t)
	sub, snapshot, err := m.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Unsubscribe(sub)
	if f := parseFrame(t, snapshot); string(f.data["model_calls"]) != `{"threads":0,"subagents":0,"background":0,"local":0}` {
		t.Fatalf("snapshot model_calls = %s", f.data["model_calls"])
	}
	next := func() ModelCalls {
		t.Helper()
		var got ModelCalls
		decodeField(t, frameOf(t, sub, "model_calls"), "model_calls", &got)
		return got
	}

	a, convA := createSession(t, m, prov)
	_, convB := createSession(t, m, prov)
	convA.EmitTurn(agentapi.TurnWorking, "")
	convB.EmitTurn(agentapi.TurnWorking, "")
	// A burst coalesces into one frame.
	convA.Emit(phaseEvent(&agentapi.Phase{Calls: 1, CallsSince: time.Unix(1, 0)}))
	convA.Emit(phaseEvent(&agentapi.Phase{Calls: 2, Subagents: 1, CallsSince: time.Unix(1, 0)}))
	convB.Emit(phaseEvent(&agentapi.Phase{Tool: "bash", ToolSince: time.Unix(2, 0)}))
	if got := next(); got != (ModelCalls{Threads: 1, Subagents: 1, Local: 1}) {
		t.Fatalf("counts = %+v", got)
	}

	// A Background AI call counts while it runs.
	release, started := make(chan struct{}), make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = m.runUtility(context.Background(), UtilityCall{Purpose: "title", Provider: prov.Name(), Model: "gpt-6-luna"}, "prompt", func(context.Context, func(agentapi.UtilityUsage)) (string, error) {
			close(started)
			<-release
			return "reply", nil
		})
	}()
	<-started
	if got := next(); got.Background != 1 || got.Threads != 1 {
		t.Fatalf("with a Background AI call = %+v", got)
	}
	close(release)
	<-done
	if got := next(); got.Background != 0 {
		t.Fatalf("after the Background AI call = %+v", got)
	}

	// A Thread waiting on the owner keeps its subagent's call counted, not its own.
	convA.EmitInteraction(agentapi.Interaction{ID: "q", Kind: agentapi.InteractionQuestion, Title: "Question", State: agentapi.InteractionPending, Time: time.Unix(3, 0), Questions: []agentapi.Question{{Text: "Which?", Custom: true}}})
	convA.Emit(phaseEvent(&agentapi.Phase{Calls: 1, Subagents: 1, CallsSince: time.Unix(1, 0)}))
	if got := next(); got != (ModelCalls{Subagents: 1, Local: 1}) {
		t.Fatalf("while waiting = %+v", got)
	}
	if p := shownPhase(t, m, a.ID); p == nil || p.Kind != PhaseWaiting {
		t.Fatalf("thread A phase = %+v", p)
	}
}
