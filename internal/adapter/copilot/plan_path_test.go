package copilot

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

type planReadReply struct {
	plan *rpc.PlanReadResult
	err  error
}
type controlledPlanSession struct {
	*fakeSession
	reads   chan struct{}
	replies chan planReadReply
}

func (s *controlledPlanSession) ReadPlan(ctx context.Context) (*rpc.PlanReadResult, error) {
	select {
	case s.reads <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case reply := <-s.replies:
		return reply.plan, reply.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestWebPlanPathReadCoalescesInvalidationAndRejectsStaleResults(t *testing.T) {
	h := openWeb(t)
	runtime := &controlledPlanSession{fakeSession: h.fs, reads: make(chan struct{}, 1), replies: make(chan planReadReply, 1)}
	c := h.conv.(*conversation)
	c.mu.Lock()
	c.sess = runtime
	c.checkPlanLocked()
	c.mu.Unlock()
	read := func() {
		t.Helper()
		select {
		case <-runtime.reads:
		case <-time.After(time.Second):
			t.Fatal("plan RPC was not scheduled")
		}
	}
	read()
	h.fs.onEvent(ev("plan-changed", &rpc.SessionPlanChangedData{}))
	runtime.replies <- planReadReply{plan: &rpc.PlanReadResult{Path: copilot.String("/scratch/stale.md")}}
	read()
	runtime.replies <- planReadReply{plan: &rpc.PlanReadResult{Path: copilot.String("/scratch/./plan.md")}}
	waitFor(t, "current exact path", func() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.planPath == "/scratch/plan.md" })
	for _, event := range h.sink.all() {
		if event.Kind == agentapi.EventPlanPath && event.PlanPath != "" && event.PlanPath != "/scratch/plan.md" {
			t.Fatalf("stale path was published: %q", event.PlanPath)
		}
	}
	h.fs.onEvent(ev("plan-changed-again", &rpc.SessionPlanChangedData{}))
	read()
	runtime.replies <- planReadReply{err: errors.New("plan unavailable")}
	waitFor(t, "failed read completed", func() bool { c.mu.Lock(); defer c.mu.Unlock(); return !c.planReading })
	c.mu.Lock()
	path := c.planPath
	c.mu.Unlock()
	if path != "/scratch/plan.md" {
		t.Fatalf("failure discarded known identity: %q", path)
	}
	h.fs.onEvent(ev("plan-changed-again", &rpc.SessionPlanChangedData{}))
	var versions []uint64
	for _, event := range h.sink.all() {
		if event.Kind == agentapi.EventPlanPath && event.PlanVersion != 0 {
			versions = append(versions, event.PlanVersion)
		}
	}
	if !reflect.DeepEqual(versions, []uint64{1, 2}) {
		t.Fatalf("native invalidations = %v", versions)
	}
}

func TestWebPlanDraftDistinguishesEmptyDeletedAndReadFailure(t *testing.T) {
	h := openWeb(t)
	runtime := &controlledPlanSession{fakeSession: h.fs, reads: make(chan struct{}, 1), replies: make(chan planReadReply, 1)}
	c := h.conv.(*conversation)
	c.mu.Lock()
	c.sess = runtime
	c.mu.Unlock()
	for _, tc := range []struct {
		name    string
		reply   planReadReply
		exists  bool
		failure bool
	}{
		{"empty", planReadReply{plan: &rpc.PlanReadResult{Exists: true, Content: copilot.String("")}}, true, false},
		{"deleted", planReadReply{plan: &rpc.PlanReadResult{Exists: false, Content: copilot.String("stale")}}, false, false},
		{"error", planReadReply{err: errors.New("disk read failed")}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime.replies <- tc.reply
			draft, err := c.ReadPlanDraft(t.Context())
			<-runtime.reads
			if (err != nil) != tc.failure || draft.Exists != tc.exists || draft.Content != "" {
				t.Fatalf("draft = %+v %v", draft, err)
			}
		})
	}
	if err := c.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReadPlanDraft(t.Context()); !errors.Is(err, agentapi.ErrClosed) {
		t.Fatalf("closed draft = %v", err)
	}
}
