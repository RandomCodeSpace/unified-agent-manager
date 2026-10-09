package copilot

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

type forkClient struct {
	*fakeClient
	requests []rpc.SessionsForkRequest
	result   *rpc.SessionsForkResult
	err      error
}

func (f *forkClient) ForkSession(_ context.Context, req *rpc.SessionsForkRequest) (*rpc.SessionsForkResult, error) {
	f.requests = append(f.requests, *req)
	return f.result, f.err
}

func forkProvider(t *testing.T, f *forkClient) *webProvider {
	t.Helper()
	p := newWebProvider(func() (sdkClient, error) { return f, nil }, time.Hour)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	return p
}

func TestNativeForkBoundariesUseExactRecordedOwnerAndExcludeNextRoot(t *testing.T) {
	auto := userMessage("auto", rpc.UserMessageDeliveryIdle, "continue")
	auto.IsAutopilotContinuation = copilot.Bool(true)
	f := &forkClient{fakeClient: &fakeClient{pageSize: 2, journal: []copilot.SessionEvent{
		ev("event-1", userMessage("message-1", rpc.UserMessageDeliveryIdle, "first")),
		agentEv("child-root", "child", userMessage("message-2", rpc.UserMessageDeliveryIdle, "child")),
		ev("steer-event", userMessage("steer", rpc.UserMessageDeliverySteering, "steer")),
		ev("auto-event", auto),
		ev("idle-1", &rpc.SessionIdleData{}),
		ev("event-2", userMessage("message-2", rpc.UserMessageDeliveryIdle, "middle")),
		ev("idle-2", &rpc.SessionIdleData{}),
		ev("event-3", userMessage("message-3", rpc.UserMessageDeliveryIdle, "last")),
		ev("idle-3", &rpc.SessionIdleData{}),
	}}, result: &rpc.SessionsForkResult{SessionID: "native-target"}}
	p := forkProvider(t, f)
	for _, tc := range []struct{ user, owner, next string }{
		{"message-1", "event-1", "event-2"},
		{"message-2", "event-2", "event-3"},
		{"message-3", "event-3", ""},
	} {
		req := agentapi.ForkBoundaryRequest{ConversationID: "native-source", UserItemID: tc.user}
		boundary, err := p.ReadForkBoundary(context.Background(), req)
		want := agentapi.ForkBoundary{UserEventID: tc.owner, ToEventID: tc.next, TailEventID: "idle-3"}
		if err != nil || boundary != want {
			t.Fatalf("%s boundary = %+v, %v; want %+v", tc.user, boundary, err, want)
		}
		id, err := p.Fork(context.Background(), agentapi.ForkRequest{ForkBoundaryRequest: req, Boundary: boundary})
		if err != nil || id != "native-target" {
			t.Fatalf("fork = %q, %v", id, err)
		}
		call := f.requests[len(f.requests)-1]
		if call.SessionID != "native-source" || (tc.next == "" && call.ToEventID != nil) || (tc.next != "" && (call.ToEventID == nil || *call.ToEventID != tc.next)) {
			t.Fatalf("RPC = %+v", call)
		}
	}
	if len(f.create) != 0 || len(f.resume) != 0 || len(f.deleted) != 0 {
		t.Fatal("fork opened, created or deleted a conversation")
	}
	for _, invalid := range []string{"event-1", "steer", "auto", "message", "missing"} {
		if _, err := p.ReadForkBoundary(context.Background(), agentapi.ForkBoundaryRequest{ConversationID: "native-source", UserItemID: invalid}); !errors.Is(err, agentapi.ErrItemNotFound) {
			t.Fatalf("invalid owner %q = %v", invalid, err)
		}
	}
}

func TestNativeForkRejectsChangedUnsettledAndAmbiguousBoundary(t *testing.T) {
	f := &forkClient{fakeClient: &fakeClient{journal: []copilot.SessionEvent{
		ev("owner", userMessage("message", rpc.UserMessageDeliveryIdle, "prompt")),
		ev("idle", &rpc.SessionIdleData{}),
	}}, result: &rpc.SessionsForkResult{SessionID: "target"}}
	p := forkProvider(t, f)
	req := agentapi.ForkBoundaryRequest{ConversationID: "source", UserItemID: "message"}
	b, err := p.ReadForkBoundary(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	f.journal = append(f.journal, ev("later", userMessage("later", rpc.UserMessageDeliveryIdle, "later")))
	if _, err := p.Fork(context.Background(), agentapi.ForkRequest{ForkBoundaryRequest: req, Boundary: b}); err == nil || len(f.requests) != 0 {
		t.Fatalf("changed boundary = %v, RPCs %d", err, len(f.requests))
	}
	if _, err := p.ReadForkBoundary(context.Background(), agentapi.ForkBoundaryRequest{ConversationID: "source", UserItemID: "later"}); !errors.Is(err, agentapi.ErrBusy) {
		t.Fatalf("unsettled = %v", err)
	}
	f.journal = append(f.journal, ev("duplicate", userMessage("message", rpc.UserMessageDeliveryIdle, "same visible ID")), ev("last-idle", &rpc.SessionIdleData{}))
	if _, err := p.ReadForkBoundary(context.Background(), req); err == nil {
		t.Fatal("ambiguous visible owner accepted")
	}
}

func TestNativeForkRetriesOnlyTheReadAndPreservesUncertainRPC(t *testing.T) {
	f := &forkClient{fakeClient: &fakeClient{pageSize: 1, expireRead: 2, journal: []copilot.SessionEvent{
		ev("owner", userMessage("message", rpc.UserMessageDeliveryIdle, "prompt")),
		ev("idle", &rpc.SessionIdleData{}),
	}}, err: context.DeadlineExceeded}
	p := forkProvider(t, f)
	req := agentapi.ForkBoundaryRequest{ConversationID: "source", UserItemID: "message"}
	b, err := p.ReadForkBoundary(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	before := append([]copilot.SessionEvent(nil), f.journal...)
	if _, err := p.Fork(context.Background(), agentapi.ForkRequest{ForkBoundaryRequest: req, Boundary: b}); !errors.Is(err, agentapi.ErrForkUncertain) || len(f.requests) != 1 {
		t.Fatalf("uncertain = %v, RPCs %d", err, len(f.requests))
	}
	if !reflect.DeepEqual(before, f.journal) {
		t.Fatal("source journal changed")
	}
	f.err = &copilot.RPCError{Code: -32601, Message: "Method not found"}
	if _, err := p.Fork(context.Background(), agentapi.ForkRequest{ForkBoundaryRequest: req, Boundary: b}); !errors.Is(err, agentapi.ErrUnsupported) {
		t.Fatalf("unsupported = %v", err)
	}
	if p.Capabilities().Fork {
		t.Fatal("unsupported fork still advertised")
	}
}

// The CLI journal records no session.idle: a finished latest turn ends with
// the root assistant.turn_end of a step whose message requested no tools.
// Every prefix of the recorded journal (CLI 1.0.88) before that point is a
// turn still running, including the turn_end of a step that ran tools.
func TestNativeForkSettlesLatestTurnFromRecordedJournal(t *testing.T) {
	journal := settledJournal(t)
	first, last := "1646b4db-ed12-43b2-939e-a64e751d79f6", "a99d019b-44fa-41e3-a780-147c973a5293"
	f := &forkClient{fakeClient: &fakeClient{journal: journal}, result: &rpc.SessionsForkResult{SessionID: "native-target"}}
	p := forkProvider(t, f)
	tail := journal[len(journal)-1].ID
	for _, tc := range []struct{ user, owner, next string }{{first, journal[2].ID, journal[19].ID}, {last, journal[19].ID, ""}} {
		req := agentapi.ForkBoundaryRequest{ConversationID: "native-source", UserItemID: tc.user}
		b, err := p.ReadForkBoundary(context.Background(), req)
		if want := (agentapi.ForkBoundary{UserEventID: tc.owner, ToEventID: tc.next, TailEventID: tail}); err != nil || b != want {
			t.Fatalf("%s boundary = %+v, %v; want %+v", tc.user, b, err, want)
		}
		if id, err := p.Fork(context.Background(), agentapi.ForkRequest{ForkBoundaryRequest: req, Boundary: b}); err != nil || id != "native-target" {
			t.Fatalf("fork = %q, %v", id, err)
		}
	}
	// Index 13 is the root turn_end after the tool-requesting message, 18 the
	// first turn's final turn_end, 19 the second prompt, 22 its turn_end.
	for n := 3; n <= len(journal); n++ {
		f.journal = journal[:n]
		user := first
		if n > 19 {
			user = last
		}
		_, err := p.ReadForkBoundary(context.Background(), agentapi.ForkBoundaryRequest{ConversationID: "native-source", UserItemID: user})
		if settled := n == 19 || n > 22; settled != (err == nil) || !settled && !errors.Is(err, agentapi.ErrBusy) {
			t.Fatalf("prefix %d (%s): %v, want settled %v", n, journal[n-1].Type(), err, settled)
		}
	}
}

// The recorded autopilot completion chain (an own Task's journal, structural
// fields only) ends at the receipt after the task_complete tool call; a step
// end may follow it. A rejected completion continues the run.
func TestNativeForkSettlesLatestTurnAtAcceptedTaskCompletion(t *testing.T) {
	completed, cont, no := rpc.TaskCompletionOutcomeCompleted, rpc.TaskCompletionOutcomeContinue, false
	call := []rpc.AssistantMessageToolRequest{{Name: "task_complete", ToolCallID: "call"}}
	journal := func(receipt *rpc.SessionTaskCompleteData, end bool) []copilot.SessionEvent {
		evs := []copilot.SessionEvent{
			ev("owner", userMessage("message", rpc.UserMessageDeliveryIdle, "prompt")),
			ev("step", &rpc.AssistantTurnStartData{TurnID: "0"}),
			ev("reply", &rpc.AssistantMessageData{MessageID: "reply", ToolRequests: call}),
			ev("tool-start", &rpc.ToolExecutionStartData{ToolCallID: "call", ToolName: "task_complete"}),
			ev("tool-end", &rpc.ToolExecutionCompleteData{ToolCallID: "call", Success: true}),
			ev("receipt", receipt),
		}
		if end {
			evs = append(evs, ev("step-end", &rpc.AssistantTurnEndData{TurnID: "0"}))
		}
		return evs
	}
	req := agentapi.ForkBoundaryRequest{ConversationID: "source", UserItemID: "message"}
	for _, tc := range []struct {
		name    string
		journal []copilot.SessionEvent
		settled bool
	}{
		{"accepted", journal(&rpc.SessionTaskCompleteData{Outcome: &completed}, false), true},
		{"accepted step end", journal(&rpc.SessionTaskCompleteData{Outcome: &completed}, true), true},
		{"rejected", journal(&rpc.SessionTaskCompleteData{Outcome: &cont, Success: &no}, true), false},
		{"continued", append(journal(&rpc.SessionTaskCompleteData{Outcome: &completed}, true), ev("next-step", &rpc.AssistantTurnStartData{TurnID: "1"})), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := forkProvider(t, &forkClient{fakeClient: &fakeClient{journal: tc.journal}})
			_, err := p.ReadForkBoundary(context.Background(), req)
			if tc.settled != (err == nil) || !tc.settled && !errors.Is(err, agentapi.ErrBusy) {
				t.Fatalf("boundary = %v, want settled %v", err, tc.settled)
			}
		})
	}
}
