package web

import (
	"context"
	"slices"
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

	// The phase the provider reports before the turn starts shows, and the
	// turn's start keeps it.
	conv.Emit(phaseEvent(&agentapi.Phase{Calls: 2, Subagents: 1, Model: "gpt-6-\x1b[1mluna", CallsSince: at(10)}))
	if p := shownPhase(t, m, sum.ID); p == nil || p.Kind != PhaseModel || p.By != "" || p.Calls != 2 {
		t.Fatalf("phase before the turn = %+v", p)
	}
	conv.EmitTurn(agentapi.TurnWorking, "")
	if p := shownPhase(t, m, sum.ID); p == nil || p.Kind != PhaseModel || p.By != "" || p.Model != "gpt-6-luna" || !p.Since.Equal(at(10)) || p.Calls != 2 || p.Subagents != 1 {
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
	conv.Emit(phaseEvent(&agentapi.Phase{Tool: "bash", Detail: "go test\n  ./internal/vterm/...", ToolSince: at(13)}))
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
	modelCallsDelay = 50 * time.Millisecond
	m, prov, _ := newTestManager(t)
	sub, snapshot, err := m.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Unsubscribe(sub)
	if f := parseFrame(t, snapshot); string(f.data["model_calls"]) != `{"threads":0,"subagents":0,"background":0,"local":0,"busy":0,"model_threads":[],"subagent_threads":[],"local_threads":[]}` {
		t.Fatalf("snapshot model_calls = %s", f.data["model_calls"])
	}
	next := func() ModelCalls {
		t.Helper()
		var got ModelCalls
		decodeField(t, frameOf(t, sub, "model_calls"), "model_calls", &got)
		return got
	}

	a, convA := createSession(t, m, prov)
	b, convB := createSession(t, m, prov)
	convA.EmitTurn(agentapi.TurnWorking, "")
	convB.EmitTurn(agentapi.TurnWorking, "")
	// A burst coalesces into one frame.
	convA.Emit(phaseEvent(&agentapi.Phase{Calls: 1, CallsSince: time.Unix(1, 0)}))
	convA.Emit(phaseEvent(&agentapi.Phase{Calls: 2, Subagents: 1, CallsSince: time.Unix(1, 0)}))
	convB.Emit(phaseEvent(&agentapi.Phase{Tool: "bash", ToolSince: time.Unix(2, 0)}))
	if got := next(); !sameCalls(got, ModelCalls{Threads: 1, Subagents: 1, Local: 1, ModelThreads: []string{a.ID}, SubagentThreads: []string{a.ID}, LocalThreads: []string{b.ID}}) {
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
	if got := next(); !sameCalls(got, ModelCalls{Subagents: 1, Local: 1, SubagentThreads: []string{a.ID}, LocalThreads: []string{b.ID}}) {
		t.Fatalf("while waiting = %+v", got)
	}
	if p := shownPhase(t, m, a.ID); p == nil || p.Kind != PhaseWaiting {
		t.Fatalf("thread A phase = %+v", p)
	}
}

// A browser that subscribed while the counts were briefly other gets them
// back, even when they return to what the last frame said.
func TestModelCallsFrameFollowsASnapshotTakenMeanwhile(t *testing.T) {
	defer func(d time.Duration) { modelCallsDelay = d }(modelCallsDelay)
	modelCallsDelay = 100 * time.Millisecond
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.Emit(phaseEvent(&agentapi.Phase{Calls: 1, CallsSince: time.Unix(1, 0)}))
	sub, snapshot, err := m.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Unsubscribe(sub)
	if f := parseFrame(t, snapshot); string(f.data["model_calls"]) != `{"threads":1,"subagents":0,"background":0,"local":0,"busy":1,"model_threads":["`+sum.ID+`"],"subagent_threads":[],"local_threads":[]}` {
		t.Fatalf("snapshot model_calls = %s", f.data["model_calls"])
	}
	conv.Emit(phaseEvent(nil))
	var got ModelCalls
	decodeField(t, frameOf(t, sub, "model_calls"), "model_calls", &got)
	if !sameCalls(got, ModelCalls{}) {
		t.Fatalf("counts after the call = %+v", got)
	}
}

// A Utility call that panics is no longer counted.
func TestUtilityCallThatPanicsIsNotCountedAfter(t *testing.T) {
	m, prov, _ := newTestManager(t)
	func() {
		defer func() { _ = recover() }()
		_, _ = m.runUtility(context.Background(), UtilityCall{Purpose: "title", Provider: prov.Name(), Model: "gpt-6-luna"}, "prompt", func(context.Context, func(agentapi.UtilityUsage)) (string, error) {
			panic("provider")
		})
	}()
	m.mu.Lock()
	live := m.utilityLive
	m.mu.Unlock()
	if live != 0 {
		t.Fatalf("Utility calls in flight after a panic = %d", live)
	}
}

// sameCalls compares counts and Thread lists, an empty list as none.
func sameCalls(a, b ModelCalls) bool {
	return a.Threads == b.Threads && a.Subagents == b.Subagents && a.Background == b.Background && a.Local == b.Local &&
		slices.Equal(a.ModelThreads, b.ModelThreads) && slices.Equal(a.SubagentThreads, b.SubagentThreads) && slices.Equal(a.LocalThreads, b.LocalThreads)
}

func modelCallsNow(m *Manager) ModelCalls {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.modelCallsLocked()
}

// A subagent that outlives its turn shows and counts between turns as a
// subagent's call or tool, never as the Thread's own; compaction counts as
// the Thread's own call; a close ends both.
func TestPhaseBetweenTurnsShowsSubagentsAndCompaction(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	at := func(sec int64) time.Time { return time.Unix(sec, 0).UTC() }
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.Emit(phaseEvent(&agentapi.Phase{Calls: 2, Subagents: 1, Model: "gpt-6-luna", CallsSince: at(1)}))
	conv.EmitTurn(agentapi.TurnCompleted, "")
	if p := shownPhase(t, m, sum.ID); p != nil {
		t.Fatalf("at the turn's end = %+v", p)
	}

	// The provider reports the subagent still running after the end.
	conv.Emit(phaseEvent(&agentapi.Phase{Calls: 1, Subagents: 1, Model: "ollama/deepseek-v4.1-flash", Agent: "Survey\nthe docs", CallsSince: at(2)}))
	if p := shownPhase(t, m, sum.ID); p == nil || p.Kind != PhaseModel || p.By != PhaseBySubagent || p.Detail != "Survey the docs" || p.Model != "ollama/deepseek-v4.1-flash" || !p.Since.Equal(at(2)) {
		t.Fatalf("subagent call between turns = %+v", p)
	}
	if got := modelCallsNow(m); !sameCalls(got, ModelCalls{Subagents: 1, SubagentThreads: []string{sum.ID}}) {
		t.Fatalf("counts with a subagent call between turns = %+v", got)
	}
	conv.Emit(phaseEvent(&agentapi.Phase{Tool: "bash", Detail: "go test ./...", ToolSince: at(3), ToolSubagent: true}))
	if p := shownPhase(t, m, sum.ID); p == nil || p.Kind != PhaseLocal || p.By != PhaseBySubagent || p.Tool != "bash" || p.Detail != "go test ./..." {
		t.Fatalf("subagent tool between turns = %+v", p)
	}
	if got := modelCallsNow(m); !sameCalls(got, ModelCalls{Local: 1, LocalThreads: []string{sum.ID}}) {
		t.Fatalf("counts with a subagent tool between turns = %+v", got)
	}

	// Compaction between turns: the Thread's own model call.
	conv.Emit(phaseEvent(&agentapi.Phase{Calls: 1, Model: "gpt-6-luna", Compacting: true, CallsSince: at(4)}))
	if p := shownPhase(t, m, sum.ID); p == nil || p.Kind != PhaseModel || p.By != PhaseByCompaction || p.Model != "gpt-6-luna" || p.Detail != "" {
		t.Fatalf("compaction between turns = %+v", p)
	}
	if got := modelCallsNow(m); !sameCalls(got, ModelCalls{Threads: 1, ModelThreads: []string{sum.ID}}) {
		t.Fatalf("counts while compacting = %+v", got)
	}

	// A new turn keeps what runs; in a turn a subagent's call is not marked.
	conv.Emit(phaseEvent(&agentapi.Phase{Calls: 1, Subagents: 1, Model: "m", Agent: "Survey", CallsSince: at(5)}))
	conv.EmitTurn(agentapi.TurnWorking, "")
	if p := shownPhase(t, m, sum.ID); p == nil || p.Kind != PhaseModel || p.By != "" || p.Detail != "" || !p.Since.Equal(at(5)) {
		t.Fatalf("at the next turn's start = %+v", p)
	}
	conv.Emit(phaseEvent(&agentapi.Phase{Calls: 2, Model: "m", Compacting: true, CallsSince: at(5)}))
	if p := shownPhase(t, m, sum.ID); p == nil || p.By != PhaseByCompaction {
		t.Fatalf("compaction in a turn = %+v", p)
	}
	conv.EmitTurn(agentapi.TurnCompleted, "")
	conv.Emit(phaseEvent(&agentapi.Phase{Calls: 1, Subagents: 1, Model: "m", CallsSince: at(6)}))

	// The conversation's exit ends it: nothing is left counted.
	conv.Emit(agentapi.Event{Kind: agentapi.EventExit, Error: "gone"})
	if p := shownPhase(t, m, sum.ID); p != nil {
		t.Fatalf("after the exit = %+v", p)
	}
	if got := modelCallsNow(m); !sameCalls(got, ModelCalls{}) {
		t.Fatalf("counts after the exit = %+v", got)
	}
}

// Closing a Thread whose subagent outlived its turn takes the call off the
// count, with a frame.
func TestModelCallsDropAClosedThreadsSubagent(t *testing.T) {
	defer func(d time.Duration) { modelCallsDelay = d }(modelCallsDelay)
	modelCallsDelay = 50 * time.Millisecond
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	conv.Emit(phaseEvent(&agentapi.Phase{Calls: 1, Subagents: 1, CallsSince: time.Unix(1, 0)}))
	sub, snapshot, err := m.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Unsubscribe(sub)
	if f := parseFrame(t, snapshot); string(f.data["model_calls"]) != `{"threads":0,"subagents":1,"background":0,"local":0,"busy":1,"model_threads":[],"subagent_threads":["`+sum.ID+`"],"local_threads":[]}` {
		t.Fatalf("snapshot model_calls = %s", f.data["model_calls"])
	}
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	if p := shownPhase(t, m, sum.ID); p != nil {
		t.Fatalf("after the close = %+v", p)
	}
	var got ModelCalls
	decodeField(t, frameOf(t, sub, "model_calls"), "model_calls", &got)
	if !sameCalls(got, ModelCalls{}) {
		t.Fatalf("counts after the close = %+v", got)
	}
}

// Each list of Threads stops at maxModelCallThreads, sorted; the counts
// stay whole.
func TestModelCallsNameAtMostTheCapOfThreads(t *testing.T) {
	m, prov, _ := newTestManager(t)
	n := maxModelCallThreads + 3
	for range n {
		_, conv := createSession(t, m, prov)
		conv.Emit(phaseEvent(&agentapi.Phase{Calls: 2, Subagents: 1, CallsSince: time.Unix(1, 0)}))
	}
	got := modelCallsNow(m)
	if got.Threads != n || got.Subagents != n || len(got.ModelThreads) != maxModelCallThreads || len(got.SubagentThreads) != maxModelCallThreads || len(got.LocalThreads) != 0 {
		t.Fatalf("counts = %d threads, %d subagents; lists %d, %d, %d", got.Threads, got.Subagents, len(got.ModelThreads), len(got.SubagentThreads), len(got.LocalThreads))
	}
	if !slices.IsSorted(got.ModelThreads) {
		t.Fatalf("model Threads not sorted: %v", got.ModelThreads)
	}
}
