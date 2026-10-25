package copilot

import (
	"context"
	"testing"
	"time"

	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// A runtime restart ends a running turn as stopped with the note and exits
// its conversation detached, exits an idle conversation as on an idle close,
// stops the CLI, clears the stall count, and lets the next open start a new
// CLI.
func TestWebRestartRuntimeStopsRunningTurnsAndKeepsIdleOnes(t *testing.T) {
	h := openWeb(t)
	startTurn(t, h)
	h.fs.onEvent(ev("ts-1", &rpc.AssistantTurnStartData{TurnID: "0"}))
	idle := &recSink{}
	if _, err := h.p.Open(context.Background(), agentapi.OpenRequest{SessionID: "s-2", Workdir: "/work", Events: idle}); err != nil {
		t.Fatal(err)
	}
	h.p.stalls.Store(2)
	if err := h.p.RestartRuntime(context.Background(), "restarted"); err != nil {
		t.Fatal(err)
	}
	evs := h.sink.all()
	n := len(evs)
	if states := turnStates(h.sink); len(states) != 2 || states[1] != agentapi.TurnCancelled || evs[n-2].Turn == nil || evs[n-2].Turn.Error != "restarted" {
		t.Fatalf("running turn's states = %v, last turn %+v", states, evs[n-2].Turn)
	}
	if exit := evs[n-1]; exit.Kind != agentapi.EventExit || !exit.Detached || exit.Error != "restarted" {
		t.Fatalf("running conversation's exit = %+v", exit)
	}
	if ie := idle.all(); len(ie) != 1 || ie[0].Kind != agentapi.EventExit || ie[0].Detached {
		t.Fatalf("idle conversation's events = %+v", ie)
	}
	h.fc.mu.Lock()
	stopped := h.fc.stopped
	h.fc.mu.Unlock()
	if stopped != 1 || h.p.RuntimeStalls() != 0 {
		t.Fatalf("CLI stops = %d, stalls = %d", stopped, h.p.RuntimeStalls())
	}
	if _, err := h.p.Open(context.Background(), agentapi.OpenRequest{SessionID: "s-3", Workdir: "/work", Events: &recSink{}}); err != nil {
		t.Fatal(err)
	}
	if started, _ := h.fc.counts(); started != 2 {
		t.Fatalf("CLI starts after the restart = %d", started)
	}
}

// With no CLI running a restart does nothing.
func TestWebRestartRuntimeWithoutACLIDoesNothing(t *testing.T) {
	fc := &fakeClient{}
	p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
	if err := p.RestartRuntime(context.Background(), "restarted"); err != nil {
		t.Fatal(err)
	}
	if started, _ := fc.counts(); started != 0 {
		t.Fatalf("CLI starts = %d", started)
	}
}

// The stall bound is the full one until a model request was answered, then
// four times the longest silence an answered request showed, no shorter
// than webStallMin and no longer than webStallAfter.
func TestWebStallBoundAdaptsToAnsweredRequests(t *testing.T) {
	h := openWeb(t)
	conv := h.conv.(*conversation)
	bound := func() time.Duration {
		conv.mu.Lock()
		defer conv.mu.Unlock()
		return conv.stallBoundLocked()
	}
	if got := bound(); got != webStallAfter {
		t.Fatalf("bound before any answer = %v", got)
	}
	startTurn(t, h)
	h.fs.onEvent(ev("ts-1", &rpc.AssistantTurnStartData{TurnID: "0"}))
	time.Sleep(2 * time.Millisecond)
	h.fs.onEvent(ev("msg-1", &rpc.AssistantMessageData{Content: "done", MessageID: "m-1"}))
	conv.mu.Lock()
	gap := conv.modelGapMax
	conv.mu.Unlock()
	if gap <= 0 || gap > time.Minute {
		t.Fatalf("recorded gap = %v", gap)
	}
	if got := bound(); got != webStallMin {
		t.Fatalf("bound after a quick answer = %v", got)
	}
	for _, tc := range []struct{ gap, want time.Duration }{{20 * time.Second, 80 * time.Second}, {time.Minute, webStallAfter}} {
		conv.mu.Lock()
		conv.modelGapMax = tc.gap
		conv.mu.Unlock()
		if got := bound(); got != tc.want {
			t.Fatalf("bound with gap %v = %v, want %v", tc.gap, got, tc.want)
		}
	}
}
