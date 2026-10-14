package copilot

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
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

// A ping that fails, rather than times out, proves the CLI gone: every
// conversation on it ends as at a CLI exit, and the next message starts a
// new CLI.
func TestWebStopFailsTheRuntimeWhenThePingFails(t *testing.T) {
	shortStop(t)
	h := openWeb(t)
	otherSink := &recSink{}
	if _, err := h.p.Open(context.Background(), agentapi.OpenRequest{SessionID: "other", Events: otherSink}); err != nil {
		t.Fatal(err)
	}
	startTurn(t, h)
	useSession(h.conv, hangingAbortSession{h.fs})
	h.fc.mu.Lock()
	h.fc.pingErr = errors.New("CLI process exited: signal: killed")
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

// A ping that times out does not prove the CLI gone, as a busy CLI can miss
// one: only the stopped conversation ends, and the watchdog pings at once
// to judge the CLI.
func TestWebStopPingTimeoutDetachesOnlyTheStoppedConversation(t *testing.T) {
	shortStop(t)
	h := openWeb(t)
	otherSink := &recSink{}
	other, err := h.p.Open(context.Background(), agentapi.OpenRequest{SessionID: "other", Events: otherSink})
	if err != nil {
		t.Fatal(err)
	}
	startTurn(t, h)
	h.fc.mu.Lock()
	h.fc.pingErr = context.DeadlineExceeded
	h.fc.mu.Unlock()
	if err := h.conv.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the detached exit", func() bool { return exitOf(h.sink) != nil })
	if exit := exitOf(h.sink); !exit.Detached {
		t.Fatalf("exit = %+v", exit)
	}
	if _, forced := h.fc.counts(); forced != 0 || other.(*conversation).isClosed() || exitOf(otherSink) != nil {
		t.Fatalf("a ping timeout ended the CLI: forced %d", forced)
	}
	waitFor(t, "the watchdog's ping", func() bool { return h.fc.pingCount() == 2 })
	time.Sleep(20 * time.Millisecond)
	if _, forced := h.fc.counts(); forced != 0 || exitOf(otherSink) != nil {
		t.Fatal("one missed watchdog ping ended the CLI")
	}
}

// failingSendSession blocks its first Send until release, then fails it;
// later sends go through.
type failingSendSession struct {
	*fakeSession
	once             sync.Once
	entered, release chan struct{}
}

func (s *failingSendSession) Send(ctx context.Context, msg copilot.MessageOptions) (string, error) {
	first := false
	s.once.Do(func() { first = true })
	if first {
		close(s.entered)
		<-s.release
		return "", rejectedError{errors.New("refused")}
	}
	return s.fakeSession.Send(ctx, msg)
}

// A Stop armed for a prompt on its way ends with that prompt when it starts
// no turn: the next prompt's turn is not stopped by it.
func TestWebStopForAFailedSendSparesTheNextTurn(t *testing.T) {
	shortStop(t)
	h := openWeb(t)
	sess := &failingSendSession{fakeSession: h.fs, entered: make(chan struct{}), release: make(chan struct{})}
	useSession(h.conv, sess)
	sent := make(chan error, 1)
	go func() { sent <- h.conv.Send(context.Background(), agentapi.Prompt{Text: "first"}) }()
	<-sess.entered
	if err := h.conv.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(sess.release)
	if err := <-sent; err == nil {
		t.Fatal("the first send went through")
	}
	startTurn(t, h)
	time.Sleep(3 * webStopGrace)
	if exitOf(h.sink) != nil || h.fs.isDisconnected() || h.fc.pingCount() != 0 {
		t.Fatal("the stop of a failed send ended the next turn")
	}
	if turn := lastTurn(h.sink); turn.State != agentapi.TurnWorking {
		t.Fatalf("turn = %+v", turn)
	}
}

// A Stop that overtakes a prompt on its way aborts the turn the prompt
// starts, once, as soon as it starts.
func TestWebStopOvertakingASendAbortsTheTurnItStarts(t *testing.T) {
	shortStop(t)
	webStopGrace = time.Minute
	h := openWeb(t)
	release, entered := make(chan struct{}), make(chan struct{})
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
	close(release)
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
	aborts := func() int {
		h.fs.mu.Lock()
		defer h.fs.mu.Unlock()
		return h.fs.aborts
	}
	waitFor(t, "the abort of the started turn", func() bool { return aborts() == 2 })
	time.Sleep(20 * time.Millisecond)
	if n := aborts(); n != 2 {
		t.Fatalf("aborts = %d", n)
	}
}

// A detached conversation answers its pending permission requests as
// unavailable, so a resumed session is not left waiting on them.
func TestWebStopDetachAnswersPendingPermissions(t *testing.T) {
	shortStop(t)
	h := openWeb(t)
	startTurn(t, h)
	h.fs.onEvent(ev("request", shellRequest("p")))
	if err := h.conv.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the detached exit", func() bool { return exitOf(h.sink) != nil })
	waitFor(t, "the permission answer", func() bool { return h.fs.answer("p") != nil })
	if _, ok := h.fs.answer("p").(*rpc.PermissionDecisionUserNotAvailable); !ok {
		t.Fatalf("answer = %T", h.fs.answer("p"))
	}
	if ix := h.sink.interaction("p"); ix.State != agentapi.InteractionExpired {
		t.Fatalf("permission = %+v", ix)
	}
}

func autopilotHarness(t *testing.T) (webHarness, *runtimeSession, *conversation) {
	t.Helper()
	h, runtime := runtimeHarness(t)
	runtime.runtimeMu.Lock()
	runtime.state.Mode = "autopilot"
	runtime.runtimeMu.Unlock()
	c := h.conv.(*conversation)
	c.refreshExecution(context.Background())
	return h, runtime, c
}

func (s *runtimeSession) mode() string {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	return s.state.Mode
}

// Stop on an autopilot conversation with no turn has nothing to protect, so
// without control it does not claim a stop it did not make.
func TestWebStopIdleAutopilotWithoutControlIsRefused(t *testing.T) {
	shortStop(t)
	h, runtime, c := autopilotHarness(t)
	c.control.Lock()
	err := h.conv.Cancel(context.Background())
	c.control.Unlock()
	if !errors.Is(err, agentapi.ErrBusy) {
		t.Fatalf("Stop = %v", err)
	}
	h.fs.mu.Lock()
	aborts := h.fs.aborts
	h.fs.mu.Unlock()
	if aborts != 0 || runtime.mode() != "autopilot" {
		t.Fatalf("aborts %d, mode %s", aborts, runtime.mode())
	}
	if err := h.conv.Cancel(context.Background()); err != nil || runtime.mode() != "interactive" {
		t.Fatalf("retried Stop = %v, mode %s", err, runtime.mode())
	}
}

// Stop of a running autopilot turn without control still aborts at once,
// and turns autopilot off once control is free.
func TestWebStopRunningAutopilotLeavesAutopilotAfterControl(t *testing.T) {
	shortStop(t)
	webStopGrace = time.Minute
	h, runtime, c := autopilotHarness(t)
	startTurn(t, h)
	c.control.Lock()
	err := h.conv.Cancel(context.Background())
	mode := runtime.mode()
	c.control.Unlock()
	if err != nil || mode != "autopilot" {
		t.Fatalf("Stop = %v, mode %s without control", err, mode)
	}
	waitFor(t, "autopilot off", func() bool { return runtime.mode() == "interactive" })
}

// Reopening a conversation waits for its pending detach, so a late detach
// cannot take the session from its new owner.
func TestWebReopenWaitsForAPendingDetach(t *testing.T) {
	h := openWeb(t)
	release := make(chan struct{})
	h.fs.mu.Lock()
	h.fs.disconnectHook = func() error {
		<-release
		return nil
	}
	h.fs.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := h.conv.Close(ctx); err == nil {
		t.Fatal("Close did not give up waiting for the detach")
	}
	var mu sync.Mutex
	released := false
	go func() {
		time.Sleep(100 * time.Millisecond)
		mu.Lock()
		released = true
		mu.Unlock()
		close(release)
	}()
	if _, err := h.p.Open(context.Background(), agentapi.OpenRequest{SessionID: "s-1", ConversationID: "s-1", Events: &recSink{}}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !released {
		t.Fatal("the reopen did not wait for the pending detach")
	}
}

// The detach is due from the moment the conversation ends: a reopen while
// the detach still answers a pending permission waits for it, so the
// detach never reaches the CLI after the resume.
func TestWebReopenWaitsForADetachStillAnsweringPermissions(t *testing.T) {
	shortStop(t)
	h := openWeb(t)
	startTurn(t, h)
	h.fs.onEvent(ev("request", shellRequest("p")))
	answering, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	h.fs.mu.Lock()
	h.fs.respondHook = func(context.Context, string, rpc.PermissionDecision) (bool, error) {
		once.Do(func() { close(answering) })
		<-release
		return true, nil
	}
	h.fs.mu.Unlock()
	if err := h.conv.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-answering
	opened := make(chan error, 1)
	go func() {
		_, err := h.p.Open(context.Background(), agentapi.OpenRequest{SessionID: "s-1", ConversationID: "s-1", Events: &recSink{}})
		opened <- err
	}()
	select {
	case err := <-opened:
		close(release)
		t.Fatalf("reopened before the detach was sent: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-opened:
		if err != nil || !h.fs.isDisconnected() {
			t.Fatalf("reopen = %v, detached %v", err, h.fs.isDisconnected())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the reopen kept waiting after the detach")
	}
}

// A detach that never returns delays a reopen only until it is due: release
// stops waiting for it then too.
func TestWebReopenStopsWaitingWhenTheDetachIsDue(t *testing.T) {
	wait := webDetachWait
	webDetachWait = 100 * time.Millisecond
	t.Cleanup(func() { webDetachWait = wait })
	h := openWeb(t)
	never := make(chan struct{})
	t.Cleanup(func() { close(never) })
	h.fs.mu.Lock()
	h.fs.disconnectHook = func() error {
		<-never
		return nil
	}
	h.fs.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := h.conv.Close(ctx); err == nil {
		t.Fatal("Close did not give up waiting for the detach")
	}
	reopen := func() time.Duration {
		began := time.Now()
		if _, err := h.p.Open(context.Background(), agentapi.OpenRequest{SessionID: "s-1", ConversationID: "s-1", Events: &recSink{}}); err != nil {
			t.Fatal(err)
		}
		return time.Since(began)
	}
	if took := reopen(); took > time.Second {
		t.Fatalf("first reopen waited %v", took)
	}
	if took := reopen(); took > 50*time.Millisecond {
		t.Fatalf("a later reopen waited %v for a detach long due", took)
	}
}

// A reopen waits for every detach of the session in flight, not only the
// latest.
func TestWebReopenWaitsForEveryDetachInFlight(t *testing.T) {
	h := openWeb(t)
	first := h.p.detachStarted("x")
	second := h.p.detachStarted("x")
	second()
	done := make(chan struct{})
	go func() {
		h.p.awaitDetach(context.Background(), "x")
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("the reopen did not wait for the earlier detach")
	case <-time.After(50 * time.Millisecond):
	}
	first()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the reopen kept waiting")
	}
}

// A mode chosen after Stop, while the reset waited for control, stands.
func TestWebStopAutopilotResetKeepsALaterModeChoice(t *testing.T) {
	shortStop(t)
	webStopGrace = time.Minute
	h, runtime, c := autopilotHarness(t)
	startTurn(t, h)
	c.control.Lock()
	if err := h.conv.Cancel(context.Background()); err != nil {
		c.control.Unlock()
		t.Fatal(err)
	}
	// The owner turns autopilot on again; it runs before the reset.
	if err := c.ensureExecutionMode(context.Background(), rpc.SessionModeAutopilot); err != nil {
		c.control.Unlock()
		t.Fatal(err)
	}
	c.control.Unlock()
	time.Sleep(50 * time.Millisecond)
	c.control.Lock()
	mode := runtime.mode()
	c.control.Unlock()
	if mode != "autopilot" {
		t.Fatalf("the reset overrode the later choice: mode %s", mode)
	}
}

// Outside autopilot, Stop without control and without a turn still aborts,
// as Stop did before it was bounded: only an idle autopilot Stop is refused.
func TestWebStopWithoutControlOrTurnAbortsOutsideAutopilot(t *testing.T) {
	shortStop(t)
	h := openWeb(t)
	c := h.conv.(*conversation)
	c.control.Lock()
	err := h.conv.Cancel(context.Background())
	c.control.Unlock()
	h.fs.mu.Lock()
	aborts := h.fs.aborts
	h.fs.mu.Unlock()
	if err != nil || aborts != 1 {
		t.Fatalf("Stop = %v, aborts %d", err, aborts)
	}
}
