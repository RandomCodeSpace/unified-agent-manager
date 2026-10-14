package copilot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// shortStop makes the Stop bounds test-sized.
func shortStop(t *testing.T) {
	t.Helper()
	grace, lock, call, ping := webStopGrace, webStopLockWait, webStopRPCWait, webStopPingWait
	webStopGrace, webStopLockWait, webStopRPCWait, webStopPingWait = 150*time.Millisecond, 20*time.Millisecond, 40*time.Millisecond, 40*time.Millisecond
	t.Cleanup(func() { webStopGrace, webStopLockWait, webStopRPCWait, webStopPingWait = grace, lock, call, ping })
}

// hangingAbortSession is a session whose Abort never returns before its
// context ends, as with a CLI that no longer serves the session.
type hangingAbortSession struct{ *fakeSession }

func (s hangingAbortSession) Abort(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func useSession(c agentapi.Conversation, s sdkSession) {
	conv := c.(*conversation)
	conv.mu.Lock()
	conv.sess = s
	conv.mu.Unlock()
}

func (s *fakeSession) isDisconnected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.disconnected
}

func (f *fakeClient) pingCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pings
}

func exitOf(sink *recSink) *agentapi.Event {
	for _, e := range sink.all() {
		if e.Kind == agentapi.EventExit {
			return &e
		}
	}
	return nil
}

func lastTurn(sink *recSink) *agentapi.Turn {
	var got *agentapi.Turn
	for _, e := range sink.all() {
		if e.Kind == agentapi.EventTurn {
			got = e.Turn
		}
	}
	return got
}

// startTurn sends a prompt, which starts a turn.
func startTurn(t *testing.T, h webHarness) {
	t.Helper()
	if err := h.conv.Send(context.Background(), agentapi.Prompt{Text: "work"}); err != nil {
		t.Fatal(err)
	}
	if turn := lastTurn(h.sink); turn == nil || turn.State != agentapi.TurnWorking {
		t.Fatalf("turn after send = %+v", turn)
	}
}

// A turn the CLI does not end after Stop, while the CLI still answers,
// ends as stopped: only this conversation is detached, the CLI keeps
// running, and the next message resumes the same conversation on it.
func TestWebStopUnconfirmedDetachesOnlyThisConversation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		abort func(h webHarness)
	}{
		{"abort accepted", func(webHarness) {}},
		{"abort fails", func(h webHarness) { h.fs.abortErr = errors.New("session not found") }},
		{"abort hangs", func(h webHarness) { useSession(h.conv, hangingAbortSession{h.fs}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shortStop(t)
			h := openWeb(t)
			otherSink := &recSink{}
			other, err := h.p.Open(context.Background(), agentapi.OpenRequest{SessionID: "other", Events: otherSink})
			if err != nil {
				t.Fatal(err)
			}
			startTurn(t, h)
			tc.abort(h)
			began := time.Now()
			if err := h.conv.Cancel(context.Background()); err != nil {
				t.Fatalf("Stop = %v; the turn ends either way", err)
			}
			if took := time.Since(began); took > time.Second {
				t.Fatalf("Stop took %v", took)
			}
			waitFor(t, "the detached exit", func() bool { return exitOf(h.sink) != nil })
			exit := exitOf(h.sink)
			if !exit.Detached || exit.Error != stopUnconfirmed {
				t.Fatalf("exit = %+v", exit)
			}
			if turn := lastTurn(h.sink); turn.State != agentapi.TurnCancelled || turn.Error != stopUnconfirmed {
				t.Fatalf("turn = %+v", turn)
			}
			waitFor(t, "the session detach", h.fs.isDisconnected)
			if started, forced := h.fc.counts(); started != 1 || forced != 0 || h.fc.pingCount() != 1 {
				t.Fatalf("CLI started %d, force-stopped %d, pinged %d", started, forced, h.fc.pingCount())
			}
			if other.(*conversation).isClosed() || exitOf(otherSink) != nil {
				t.Fatal("another conversation on the CLI ended")
			}
			if err := h.conv.Send(context.Background(), agentapi.Prompt{Text: "late"}); !errors.Is(err, agentapi.ErrClosed) {
				t.Fatalf("send on the detached conversation = %v", err)
			}

			// The next message resumes the conversation on the same CLI.
			sink := &recSink{}
			again, err := h.p.Open(context.Background(), agentapi.OpenRequest{SessionID: "s-1", ConversationID: "s-1", Events: sink})
			if err != nil {
				t.Fatalf("reopen: %v", err)
			}
			if err := again.Send(context.Background(), agentapi.Prompt{Text: "next"}); err != nil {
				t.Fatalf("send after reopen: %v", err)
			}
			if started, _ := h.fc.counts(); started != 1 || len(h.fc.resume) != 1 {
				t.Fatalf("reopen started %d CLIs, resumed %d", started, len(h.fc.resume))
			}
		})
	}
}

// A CLI that does not answer a ping either is gone: every conversation on
// it ends as at a CLI exit, and the next message starts a new CLI.
func TestWebStopFailsTheRuntimeWhenTheCLIDoesNotAnswer(t *testing.T) {
	shortStop(t)
	h := openWeb(t)
	otherSink := &recSink{}
	if _, err := h.p.Open(context.Background(), agentapi.OpenRequest{SessionID: "other", Events: otherSink}); err != nil {
		t.Fatal(err)
	}
	startTurn(t, h)
	useSession(h.conv, hangingAbortSession{h.fs})
	h.fc.mu.Lock()
	h.fc.pingErr = context.DeadlineExceeded
	h.fc.mu.Unlock()
	if err := h.conv.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the runtime failure", func() bool { return exitOf(h.sink) != nil && exitOf(otherSink) != nil })
	for _, sink := range []*recSink{h.sink, otherSink} {
		if exit := exitOf(sink); exit.Detached || exit.Error == "" {
			t.Fatalf("exit = %+v", exit)
		}
	}
	if _, forced := h.fc.counts(); forced != 1 {
		t.Fatalf("CLI force-stopped %d times", forced)
	}
}

// A turn that ends after Stop keeps its conversation: nothing is pinged or
// detached when the check comes.
func TestWebStopConfirmedKeepsTheConversation(t *testing.T) {
	shortStop(t)
	h := openWeb(t)
	startTurn(t, h)
	if err := h.conv.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.fs.onEvent(ev("idle", &rpc.SessionIdleData{Aborted: option(true)}))
	if turn := lastTurn(h.sink); turn.State != agentapi.TurnCancelled || turn.Error != "" {
		t.Fatalf("turn = %+v", turn)
	}
	time.Sleep(3 * webStopGrace)
	if exitOf(h.sink) != nil || h.fs.isDisconnected() || h.fc.pingCount() != 0 {
		t.Fatalf("a confirmed stop detached the conversation: pings %d", h.fc.pingCount())
	}
	// A later turn gets its own check.
	startTurn(t, h)
	if err := h.conv.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the detached exit", func() bool { return exitOf(h.sink) != nil })
}

// Stop does not wait for another control operation stuck in the CLI: it
// aborts without the mode change, and the turn still ends.
func TestWebStopDoesNotWaitForAStuckControlHolder(t *testing.T) {
	shortStop(t)
	h := openWeb(t)
	startTurn(t, h)
	c := h.conv.(*conversation)
	c.control.Lock()
	defer c.control.Unlock()
	done := make(chan error, 1)
	go func() { done <- h.conv.Cancel(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop waited for the control holder")
	}
	h.fs.mu.Lock()
	aborts := h.fs.aborts
	h.fs.mu.Unlock()
	if aborts != 1 {
		t.Fatalf("aborts = %d", aborts)
	}
	waitFor(t, "the detached exit", func() bool { return exitOf(h.sink) != nil })
}

// A Stop while the prompt is still on its way to the CLI is a Stop of the
// turn it starts.
func TestWebStopDuringASendEndsTheTurnItStarts(t *testing.T) {
	shortStop(t)
	h := openWeb(t)
	release := make(chan struct{})
	entered := make(chan struct{})
	h.fs.mu.Lock()
	h.fs.beforeReturn = func(string) {
		close(entered)
		<-release
	}
	h.fs.mu.Unlock()
	sent := make(chan error, 1)
	go func() { sent <- h.conv.Send(context.Background(), agentapi.Prompt{Text: "work"}) }()
	<-entered
	if err := h.conv.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the detached exit", func() bool { return exitOf(h.sink) != nil })
	if turn := lastTurn(h.sink); turn == nil || turn.State != agentapi.TurnCancelled {
		t.Fatalf("turn = %+v", turn)
	}
	close(release)
	if err := <-sent; err != nil {
		t.Fatalf("send = %v", err)
	}
	if turn := lastTurn(h.sink); turn.State != agentapi.TurnCancelled {
		t.Fatalf("the late send result restarted the turn: %+v", turn)
	}
}
