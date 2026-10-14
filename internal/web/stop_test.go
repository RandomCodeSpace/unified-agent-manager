package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

const unconfirmedStop = "Copilot did not confirm the stop"

// endUnconfirmed reports what the Copilot adapter reports when the CLI did
// not end a turn after Stop: the turn ends as cancelled, then only this
// conversation ends.
func endUnconfirmed(conv interface{ Emit(agentapi.Event) }) {
	conv.Emit(agentapi.Event{Kind: agentapi.EventTurn, Turn: &agentapi.Turn{State: agentapi.TurnCancelled, Error: unconfirmedStop}})
	conv.Emit(agentapi.Event{Kind: agentapi.EventExit, Error: unconfirmedStop, Detached: true})
}

// Stop does not wait behind another operation on the Thread stuck in the
// provider: it reaches the provider within stopOpWait.
func TestStopDoesNotWaitForAStuckOperation(t *testing.T) {
	wait := stopOpWait
	stopOpWait = 50 * time.Millisecond
	t.Cleanup(func() { stopOpWait = wait })
	m, prov, _ := newTestManager(t)
	sum, conv := busySession(t, m, prov)
	entered, release := make(chan struct{}), make(chan struct{})
	conv.SetSteerHook(func(context.Context, string) error {
		close(entered)
		<-release
		return nil
	})
	steered := make(chan error, 1)
	go func() {
		_, err := m.Submit(sum.ID, PromptRequest{Text: "more", RequestID: mustUUID(t), Mode: ModeSteer})
		steered <- err
	}()
	<-entered
	stopped := make(chan error, 1)
	go func() {
		_, err := m.Cancel(sum.ID)
		stopped <- err
	}()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("Stop = %v", err)
		}
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("Stop waited for the stuck steer")
	}
	if conv.Cancels() != 1 {
		t.Fatalf("cancels = %d", conv.Cancels())
	}
	endUnconfirmed(conv)
	if d := detail(t, m, sum.ID); d.State != StateCancelled || d.StateDetail != unconfirmedStop || d.Open {
		t.Fatalf("after the stop = %+v", d.SessionSummary)
	}
	close(release)
	if err := <-steered; err != nil {
		t.Fatalf("steer = %v", err)
	}
	if d := detail(t, m, sum.ID); d.State != StateCancelled {
		t.Fatalf("the late steer changed the state to %s", d.State)
	}
}

// A conversation ended after a stop the provider did not confirm leaves
// the Thread stopped, not failed, even with queued prompts and a running
// subagent, and the next message reopens it.
func TestStopUnconfirmedKeepsTheThreadStoppedAndReopens(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := busySession(t, m, prov)
	mustSubmit(t, m, sum.ID, "B", mustUUID(t), ModeQueue, SubmissionQueued)
	conv.EmitSubagent(agentapi.Subagent{ID: "agent-1", Name: "helper", Status: agentapi.SubagentRunning})
	if _, err := m.Cancel(sum.ID); err != nil {
		t.Fatal(err)
	}
	endUnconfirmed(conv)
	d := detail(t, m, sum.ID)
	if d.State != StateCancelled || d.StateDetail != unconfirmedStop || d.Open || !d.QueuePaused || queueTexts(d) != "B" {
		t.Fatalf("after the stop = %+v queue=%s", d.SessionSummary, queueTexts(d))
	}
	if len(d.Subagents) != 1 || d.Subagents[0].Status != agentapi.SubagentCancelled {
		t.Fatalf("subagents = %+v", d.Subagents)
	}

	opens := len(prov.Opens())
	sub, err := m.Submit(sum.ID, PromptRequest{Text: "next", RequestID: mustUUID(t), Mode: ModeSend})
	if err != nil || sub.Status != SubmissionAccepted {
		t.Fatalf("next message = %+v, %v", sub, err)
	}
	reopened := prov.Last()
	if len(prov.Opens()) != opens+1 || reopened == conv || reopened.ID() != sum.ConversationID {
		t.Fatalf("opens %d -> %d, reopened %v", opens, len(prov.Opens()), reopened)
	}
	if sends := reopened.Sends(); len(sends) != 1 || sends[0] != "next" {
		t.Fatalf("reopened sends = %q", sends)
	}
	if d := detail(t, m, sum.ID); d.State != StateWorking || !d.Open {
		t.Fatalf("after the next message = %+v", d.SessionSummary)
	}
}

// A provider that cannot stop now because another operation holds the
// conversation (Copilot's idle autopilot without control) is a conflict to
// retry, not a provider failure.
func TestStopBusyConversationIsAConflict(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := busySession(t, m, prov)
	conv.SetCancelHook(func(context.Context) error {
		return fmt.Errorf("%w: another operation is in progress", agentapi.ErrBusy)
	})
	if _, err := m.Cancel(sum.ID); statusOf(err) != http.StatusConflict || !strings.Contains(err.Error(), "try Stop again") {
		t.Fatalf("Stop = %v (%d)", err, statusOf(err))
	}
}
