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
