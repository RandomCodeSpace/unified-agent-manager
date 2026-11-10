package copilot

import (
	"context"
	"fmt"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// shortStall makes the stall bound test-sized.
func shortStall(t *testing.T) {
	t.Helper()
	shortStop(t)
	after := webStallAfter
	webStallAfter = 60 * time.Millisecond
	t.Cleanup(func() { webStallAfter = after })
}

func (s *fakeSession) abortCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.aborts
}

func (s *fakeSession) sentMessages() []copilot.MessageOptions {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]copilot.MessageOptions(nil), s.msgs...)
}

func (s *fakeSession) subagentCancels() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.subCancels...)
}

// subagentWaiting starts a turn whose main agent runs a subagent's task call
// and leaves the subagent waiting on a model request.
func subagentWaiting(t *testing.T, h webHarness) {
	t.Helper()
	startTurn(t, h)
	h.fs.onEvent(ev("ts-1", &rpc.AssistantTurnStartData{TurnID: "0"}))
	h.fs.onEvent(ev("msg-1", &rpc.AssistantMessageData{Content: "delegating", MessageID: "m-1"}))
	h.fs.onEvent(ev("task-1", &rpc.ToolExecutionStartData{ToolCallID: "task-call", ToolName: "task"}))
	h.fs.onEvent(agentEv("sub-start", "agent-1", &rpc.SubagentStartedData{ToolCallID: "task-call"}))
	h.fs.onEvent(agentEv("sub-ts-1", "agent-1", &rpc.AssistantTurnStartData{TurnID: "0"}))
}

func turnStates(sink *recSink) []agentapi.TurnState {
	var states []agentapi.TurnState
	for _, e := range sink.all() {
		if e.Kind == agentapi.EventTurn && (len(states) == 0 || states[len(states)-1] != e.Turn.State) {
			states = append(states, e.Turn.State)
		}
	}
	return states
}

// abortedIdle is what the CLI sends when a request is aborted.
func abortedIdle(h webHarness, n int) {
	h.fs.onEvent(ev(fmt.Sprintf("aidle-%d", n), &rpc.AssistantIdleData{Aborted: option(true)}))
	h.fs.onEvent(ev(fmt.Sprintf("sidle-%d", n), &rpc.SessionIdleData{Aborted: option(true)}))
}

// continuation is the CLI's record of uam's continuation message.
func continuation(h webHarness, id string) {
	h.fs.onEvent(ev("ev-"+id, &rpc.UserMessageData{Content: fmt.Sprintf(stallRetriedNote, seconds(webStallAfter)), Source: option(stallSource), MessageID: option(id), Delivery: option(rpc.UserMessageDeliveryIdle)}))
}

// A model request that hears nothing (a pooled connection the network
// dropped) is aborted and the turn continued: one turn, no stop, a note in
// place of the continuation message, and the turn's own message unchanged.
func TestWebStalledModelRequestIsRetriedInTheSameTurn(t *testing.T) {
	shortStall(t)
	h := openWeb(t)
	startTurn(t, h)
	h.fs.onEvent(ev("user-1", &rpc.UserMessageData{Content: "work", MessageID: option("owner-1")}))
	h.fs.onEvent(ev("ts-1", &rpc.AssistantTurnStartData{TurnID: "0"}))
	waitFor(t, "the abort of the stalled request", func() bool { return h.fs.abortCount() == 1 })
	abortedIdle(h, 1)
	waitFor(t, "the continuation", func() bool { return len(h.fs.sentMessages()) == 2 })
	sent := h.fs.sentMessages()[1]
	if sent.Source != stallSource || sent.Prompt != stallPrompt || sent.DisplayPrompt == "" {
		t.Fatalf("continuation = %+v", sent)
	}
	continuation(h, "retry-1")
	conv := h.conv.(*conversation)
	conv.mu.Lock()
	user := conv.turnChangeUser
	conv.mu.Unlock()
	if user != "owner-1" {
		t.Fatalf("the turn's message became %q", user)
	}
	h.fs.onEvent(ev("ts-2", &rpc.AssistantTurnStartData{TurnID: "1"}))
	h.fs.onEvent(ev("msg-1", &rpc.AssistantMessageData{Content: "done", MessageID: "m-1"}))
	h.fs.onEvent(ev("idle-2", &rpc.AssistantIdleData{}))
	if got := turnStates(h.sink); len(got) != 2 || got[0] != agentapi.TurnWorking || got[1] != agentapi.TurnCompleted {
		t.Fatalf("turn states = %v", got)
	}
	var note *agentapi.Item
	for _, e := range h.sink.all() {
		if e.Kind == agentapi.EventItem && e.Item.ID == "retry-1" {
			note = e.Item
		}
	}
	if note == nil || note.Kind != agentapi.ItemNotice || note.Text != fmt.Sprintf(stallRetriedNote, seconds(webStallAfter)) {
		t.Fatalf("continuation item = %+v", note)
	}
	if h.fs.abortCount() != 1 {
		t.Fatalf("aborts = %d", h.fs.abortCount())
	}
}

// A running tool is not a stall.
func TestWebStallWatchLeavesRunningToolsAlone(t *testing.T) {
	shortStall(t)
	h := openWeb(t)
	startTurn(t, h)
	h.fs.onEvent(ev("ts-1", &rpc.AssistantTurnStartData{TurnID: "0"}))
	h.fs.onEvent(ev("tool-1", &rpc.ToolExecutionStartData{ToolCallID: "call-1", ToolName: "bash"}))
	time.Sleep(5 * webStallAfter)
	if n := h.fs.abortCount(); n != 0 {
		t.Fatalf("a running tool was aborted %d times", n)
	}
	// A model message ends the wait: a turn that is not waiting on the
	// model is never aborted, whatever its silence.
	h.fs.onEvent(ev("done-1", &rpc.ToolExecutionCompleteData{ToolCallID: "call-1", Success: true}))
	h.fs.onEvent(ev("msg-1", &rpc.AssistantMessageData{Content: "thinking out loud", MessageID: "m-1"}))
	time.Sleep(5 * webStallAfter)
	if n := h.fs.abortCount(); n != 0 {
		t.Fatalf("a turn not waiting on the model was aborted %d times", n)
	}
}

// A request that stalls again after the retry ends the turn as stopped,
// with a note saying so.
func TestWebSecondStallEndsTheTurn(t *testing.T) {
	shortStall(t)
	h := openWeb(t)
	startTurn(t, h)
	h.fs.onEvent(ev("ts-1", &rpc.AssistantTurnStartData{TurnID: "0"}))
	waitFor(t, "the first abort", func() bool { return h.fs.abortCount() == 1 })
	abortedIdle(h, 1)
	waitFor(t, "the continuation", func() bool { return len(h.fs.sentMessages()) == 2 })
	continuation(h, "retry-1")
	h.fs.onEvent(ev("ts-2", &rpc.AssistantTurnStartData{TurnID: "1"}))
	waitFor(t, "the second abort", func() bool { return h.fs.abortCount() == 2 })
	abortedIdle(h, 2)
	turn := lastTurn(h.sink)
	if turn.State != agentapi.TurnCancelled || turn.Error != fmt.Sprintf(stallGaveUpNote, seconds(webStallAfter)) {
		t.Fatalf("turn = %+v", turn)
	}
	if n := len(h.fs.sentMessages()); n != 2 {
		t.Fatalf("messages sent = %d", n)
	}
	// The next turn gets a retry of its own.
	startTurn(t, h)
	h.fs.onEvent(ev("ts-3", &rpc.AssistantTurnStartData{TurnID: "2"}))
	waitFor(t, "the next turn's abort", func() bool { return h.fs.abortCount() == 3 })
	abortedIdle(h, 3)
	waitFor(t, "the next turn's continuation", func() bool { return len(h.fs.sentMessages()) == 4 })
}

// A Stop while uam aborts a stalled request ends the turn: no continuation.
func TestWebStopDuringAStallRetryEndsTheTurn(t *testing.T) {
	shortStall(t)
	h := openWeb(t)
	startTurn(t, h)
	h.fs.onEvent(ev("ts-1", &rpc.AssistantTurnStartData{TurnID: "0"}))
	waitFor(t, "the abort of the stalled request", func() bool { return h.fs.abortCount() == 1 })
	if err := h.conv.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	abortedIdle(h, 1)
	if turn := lastTurn(h.sink); turn.State != agentapi.TurnCancelled {
		t.Fatalf("turn = %+v", turn)
	}
	time.Sleep(3 * webStallAfter)
	if n := len(h.fs.sentMessages()); n != 1 {
		t.Fatalf("messages sent = %d, want no continuation", n)
	}
}

// A subagent's model request that hears nothing is cancelled through the
// subagent, once; the main agent's request is left alone.
func TestWebStalledSubagentIsCancelledOnce(t *testing.T) {
	shortStall(t)
	h := openWeb(t)
	subagentWaiting(t, h)
	waitFor(t, "the stalled subagent's cancel", func() bool { return len(h.fs.subagentCancels()) == 1 })
	time.Sleep(5 * webStallAfter)
	if got := h.fs.subagentCancels(); len(got) != 1 || got[0] != "agent-1" {
		t.Fatalf("subagent cancels = %v", got)
	}
	if n := h.fs.abortCount(); n != 0 {
		t.Fatalf("the main request was aborted %d times", n)
	}
	if turn := lastTurn(h.sink); turn.State != agentapi.TurnWorking {
		t.Fatalf("turn = %+v", turn)
	}
}

// A subagent running a tool of its own is not stalled.
func TestWebSubagentStallWatchLeavesRunningToolsAlone(t *testing.T) {
	shortStall(t)
	h := openWeb(t)
	subagentWaiting(t, h)
	h.fs.onEvent(agentEv("sub-tool", "agent-1", &rpc.ToolExecutionStartData{ToolCallID: "sub-call", ToolName: "bash"}))
	time.Sleep(5 * webStallAfter)
	if got := h.fs.subagentCancels(); len(got) != 0 {
		t.Fatalf("a subagent with a running tool was cancelled: %v", got)
	}
	// Once its tool ends, the same silent request is a stall.
	h.fs.onEvent(agentEv("sub-done", "agent-1", &rpc.ToolExecutionCompleteData{ToolCallID: "sub-call", Success: true}))
	waitFor(t, "the stalled subagent's cancel", func() bool { return len(h.fs.subagentCancels()) == 1 })
}

// Events of the subagent keep its request alive; its message ends the wait.
func TestWebSubagentEventsResetTheStallWatch(t *testing.T) {
	shortStall(t)
	h := openWeb(t)
	subagentWaiting(t, h)
	for i := range 6 {
		time.Sleep(webStallAfter / 2)
		h.fs.onEvent(agentEv(fmt.Sprintf("sub-delta-%d", i), "agent-1", &rpc.AssistantMessageDeltaData{MessageID: "sm-1", DeltaContent: "x"}))
	}
	h.fs.onEvent(agentEv("sub-msg", "agent-1", &rpc.AssistantMessageData{Content: "done", MessageID: "sm-1"}))
	time.Sleep(5 * webStallAfter)
	if got := h.fs.subagentCancels(); len(got) != 0 {
		t.Fatalf("a subagent that was heard from was cancelled: %v", got)
	}
}

// A tool call whose end was never reported, left over from a stopped turn,
// does not keep the stall watch off in the next turn.
func TestWebStallWatchIgnoresAToolLeftOverFromAnEarlierTurn(t *testing.T) {
	shortStall(t)
	h := openWeb(t)
	startTurn(t, h)
	h.fs.onEvent(ev("ts-1", &rpc.AssistantTurnStartData{TurnID: "0"}))
	h.fs.onEvent(ev("msg-1", &rpc.AssistantMessageData{Content: "running", MessageID: "m-1"}))
	h.fs.onEvent(ev("tool-1", &rpc.ToolExecutionStartData{ToolCallID: "call-1", ToolName: "bash"}))
	abortedIdle(h, 1)
	if turn := lastTurn(h.sink); turn.State != agentapi.TurnCancelled {
		t.Fatalf("stopped turn = %+v", turn)
	}
	before := h.fs.abortCount()
	startTurn(t, h)
	h.fs.onEvent(ev("ts-2", &rpc.AssistantTurnStartData{TurnID: "0"}))
	waitFor(t, "the abort of the stalled request", func() bool { return h.fs.abortCount() == before+1 })
}

// adaptiveStall makes the adaptive stall bound test-sized: floor once a
// request has answered quickly, ceiling before that.
func adaptiveStall(t *testing.T, floor, ceiling time.Duration) {
	t.Helper()
	shortStop(t)
	after, low := webStallAfter, webStallMin
	webStallAfter, webStallMin = ceiling, floor
	t.Cleanup(func() { webStallAfter, webStallMin = after, low })
}

// quickAnswer runs a turn whose model request answers at once, which brings
// the stall bound down to its floor.
func quickAnswer(t *testing.T, h webHarness) {
	t.Helper()
	startTurn(t, h)
	h.fs.onEvent(ev("ts-quick", &rpc.AssistantTurnStartData{TurnID: "0"}))
	h.fs.onEvent(ev("msg-quick", &rpc.AssistantMessageData{Content: "quick", MessageID: "m-quick"}))
	h.fs.onEvent(ev("idle-quick", &rpc.AssistantIdleData{}))
}

func stallBound(h webHarness) time.Duration {
	conv := h.conv.(*conversation)
	conv.mu.Lock()
	defer conv.mu.Unlock()
	return conv.stallBoundLocked()
}

// A request slower than the adapted bound is aborted once. Its retry gets
// the full bound, so a slow but healthy request is not aborted a second time
// and the turn goes on; its answer then raises the bound.
func TestWebRetriedRequestGetsTheFullStallBound(t *testing.T) {
	adaptiveStall(t, 150*time.Millisecond, 3*time.Second)
	h := openWeb(t)
	quickAnswer(t, h)
	startTurn(t, h)
	h.fs.onEvent(ev("ts-1", &rpc.AssistantTurnStartData{TurnID: "0"}))
	waitFor(t, "the abort at the adapted bound", func() bool { return h.fs.abortCount() == 1 })
	abortedIdle(h, 1)
	waitFor(t, "the continuation", func() bool { return len(h.fs.sentMessages()) == 3 })
	continuation(h, "retry-1")
	h.fs.onEvent(ev("ts-2", &rpc.AssistantTurnStartData{TurnID: "1"}))
	time.Sleep(4 * webStallMin)
	if n := h.fs.abortCount(); n != 1 {
		t.Fatalf("the retried request was aborted too: aborts = %d", n)
	}
	h.fs.onEvent(ev("msg-1", &rpc.AssistantMessageData{Content: "slow but here", MessageID: "m-1"}))
	h.fs.onEvent(ev("idle-1", &rpc.AssistantIdleData{}))
	if turn := lastTurn(h.sink); turn.State != agentapi.TurnCompleted || turn.StallGaveUp {
		t.Fatalf("turn = %+v", turn)
	}
	if got := stallBound(h); got <= webStallMin {
		t.Fatalf("bound after the slow answer = %v, want above the floor %v", got, webStallMin)
	}
}

// The abort that gives a twice-stalled turn up is no answer: it does not
// raise the bound for the rest of the conversation.
func TestWebGivenUpStallDoesNotSizeTheBound(t *testing.T) {
	adaptiveStall(t, 50*time.Millisecond, 800*time.Millisecond)
	h := openWeb(t)
	quickAnswer(t, h)
	startTurn(t, h)
	h.fs.onEvent(ev("ts-1", &rpc.AssistantTurnStartData{TurnID: "0"}))
	waitFor(t, "the first abort", func() bool { return h.fs.abortCount() == 1 })
	abortedIdle(h, 1)
	waitFor(t, "the continuation", func() bool { return len(h.fs.sentMessages()) == 3 })
	continuation(h, "retry-1")
	h.fs.onEvent(ev("ts-2", &rpc.AssistantTurnStartData{TurnID: "1"}))
	waitFor(t, "the second abort", func() bool { return h.fs.abortCount() == 2 })
	abortedIdle(h, 2)
	if turn := lastTurn(h.sink); turn.State != agentapi.TurnCancelled || !turn.StallGaveUp {
		t.Fatalf("turn = %+v", turn)
	}
	if got := stallBound(h); got >= webStallAfter {
		t.Fatalf("bound after a give-up = %v, the ceiling", got)
	}
}

// A Stop while the model is silent is no answer either: a conversation no
// request has answered in keeps the full bound.
func TestWebStopDoesNotSizeTheStallBound(t *testing.T) {
	adaptiveStall(t, 50*time.Millisecond, 3*time.Second)
	h := openWeb(t)
	startTurn(t, h)
	h.fs.onEvent(ev("ts-1", &rpc.AssistantTurnStartData{TurnID: "0"}))
	if err := h.conv.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	abortedIdle(h, 1)
	if turn := lastTurn(h.sink); turn.State != agentapi.TurnCancelled {
		t.Fatalf("turn = %+v", turn)
	}
	if got := stallBound(h); got != webStallAfter {
		t.Fatalf("bound after a Stop = %v, want %v", got, webStallAfter)
	}
}

// A finished subagent that takes a follow-up while the main agent is idle is
// watched like any other: its silent model request is cancelled.
func TestWebStalledSubagentFollowUpBetweenTurnsIsCancelled(t *testing.T) {
	shortStall(t)
	h := openWeb(t)
	startTurn(t, h)
	h.fs.onEvent(ev("ts-1", &rpc.AssistantTurnStartData{TurnID: "0"}))
	h.fs.onEvent(ev("msg-1", &rpc.AssistantMessageData{Content: "delegating", MessageID: "m-1"}))
	h.fs.onEvent(ev("task-1", &rpc.ToolExecutionStartData{ToolCallID: "task-call", ToolName: "task"}))
	h.fs.onEvent(agentEv("sub-start", "agent-1", &rpc.SubagentStartedData{ToolCallID: "task-call"}))
	h.fs.onEvent(agentEv("sub-done", "agent-1", &rpc.SubagentCompletedData{ToolCallID: "task-call"}))
	h.fs.onEvent(ev("task-end", &rpc.ToolExecutionCompleteData{ToolCallID: "task-call", Success: true}))
	h.fs.onEvent(ev("idle-1", &rpc.AssistantIdleData{}))
	if turn := lastTurn(h.sink); turn.State != agentapi.TurnCompleted {
		t.Fatalf("turn = %+v", turn)
	}
	followUp := agentEv("sub-ts-2", "agent-1", &rpc.AssistantTurnStartData{TurnID: "1"})
	followUp.Timestamp = time.Now().Add(time.Hour)
	h.fs.onEvent(followUp)
	waitFor(t, "the stalled follow-up's cancel", func() bool { return len(h.fs.subagentCancels()) == 1 })
	if n := h.fs.abortCount(); n != 0 {
		t.Fatalf("the idle main agent was aborted %d times", n)
	}
}

// A subagent still waiting on the model when the main turn ends keeps its
// watch: the turn's end does not drop it.
func TestWebSubagentStallWatchOutlivesTheTurn(t *testing.T) {
	shortStall(t)
	h := openWeb(t)
	subagentWaiting(t, h)
	h.fs.onEvent(ev("idle-1", &rpc.AssistantIdleData{}))
	if turn := lastTurn(h.sink); turn.State != agentapi.TurnCompleted {
		t.Fatalf("turn = %+v", turn)
	}
	waitFor(t, "the stalled subagent's cancel", func() bool { return len(h.fs.subagentCancels()) == 1 })
	time.Sleep(5 * webStallAfter)
	if got := h.fs.subagentCancels(); len(got) != 1 || got[0] != "agent-1" {
		t.Fatalf("subagent cancels = %v", got)
	}
}
