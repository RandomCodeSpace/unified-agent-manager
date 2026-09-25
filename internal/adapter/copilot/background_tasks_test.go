package copilot

import (
	"context"
	"errors"
	"strings"
	"testing"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func TestWebAssistantIdleEndsForegroundWithBackgroundShell(t *testing.T) {
	h := openWeb(t)
	if err := h.conv.Send(context.Background(), agentapi.Prompt{Text: "start a server"}); err != nil {
		t.Fatal(err)
	}
	h.fs.setTasks(&rpc.TaskShellInfo{ID: "server", Command: "python3 -m http.server 8000", Status: rpc.TaskStatusRunning})
	h.fs.onEvent(ev("final", &rpc.AssistantMessageData{MessageID: "reply", Content: "The server is running."}))
	h.fs.onEvent(ev("model-end", &rpc.AssistantTurnEndData{TurnID: "1"}))
	for _, event := range h.sink.all() {
		if event.Turn != nil && event.Turn.State != agentapi.TurnWorking {
			t.Fatal("a message or model iteration ended the foreground turn")
		}
	}
	h.fs.onEvent(ev("main-idle", &rpc.AssistantIdleData{}))
	if got := h.sink.last(); got.Turn == nil || got.Turn.State != agentapi.TurnCompleted {
		t.Fatalf("foreground still working after assistant.idle: %+v", got)
	}
	if h.fs.aborts != 0 || len(h.fs.subCancels) != 0 {
		t.Fatal("finishing the foreground stopped background work")
	}
	h.fs.onEvent(ev("continuation", &rpc.AssistantTurnStartData{TurnID: "2"}))
	if got := h.sink.last(); got.Turn == nil || got.Turn.State != agentapi.TurnWorking {
		t.Fatalf("foreground continuation not working: %+v", got)
	}
	h.fs.onEvent(ev("old-session-idle", &rpc.SessionIdleData{}))
	if got := h.sink.last(); got.Turn == nil || got.Turn.State != agentapi.TurnWorking {
		t.Fatalf("session-level idle overwrote foreground lifecycle: %+v", got)
	}
	h.fs.onEvent(agentEv("child-idle", "child", &rpc.AssistantIdleData{}))
	if got := h.sink.last(); got.Turn == nil || got.Turn.State != agentapi.TurnWorking {
		t.Fatalf("child idle ended foreground: %+v", got)
	}
	h.fs.onEvent(ev("cancelled", &rpc.AssistantIdleData{Aborted: copilot.Bool(true)}))
	if got := h.sink.last(); got.Turn == nil || got.Turn.State != agentapi.TurnCancelled {
		t.Fatalf("foreground abort = %+v", got)
	}
}

// The CLI holds a prompt sent with an explicit "enqueue", or during a turn,
// until session.idle, which it withholds while a background shell runs. A
// prompt after the foreground idle goes without a mode so the CLI delivers it
// now; one during a running turn is refused, so nothing is held.
func TestWebSendWithBackgroundShellIsDeliveredNotHeld(t *testing.T) {
	h := openWeb(t)
	ctx := context.Background()
	if err := h.conv.Send(ctx, agentapi.Prompt{Text: "start a server"}); err != nil {
		t.Fatal(err)
	}
	h.fs.setTasks(&rpc.TaskShellInfo{ID: "server", Command: "python3 -m http.server 8000", Status: rpc.TaskStatusRunning})
	h.fs.onEvent(ev("background", &rpc.SessionBackgroundTasksChangedData{}))
	settleTasks(t, h, 1)
	if err := h.conv.Send(ctx, agentapi.Prompt{Text: "too early"}); !errors.Is(err, agentapi.ErrBusy) {
		t.Fatalf("Send during a running turn = %v, want ErrBusy", err)
	}
	h.fs.onEvent(ev("main-idle", &rpc.AssistantIdleData{}))
	if err := h.conv.Send(ctx, agentapi.Prompt{Text: "follow-up"}); err != nil {
		t.Fatal(err)
	}
	if got := h.sink.last(); got.Turn == nil || got.Turn.State != agentapi.TurnWorking {
		t.Fatalf("follow-up did not start a turn: %+v", got)
	}
	if strings.Join(h.fs.sent, ",") != "start a server,follow-up" || strings.Join(h.fs.modes, ",") != "," {
		t.Fatalf("sent %q with modes %q, want both without a mode and nothing during the turn", h.fs.sent, h.fs.modes)
	}
	if err := h.conv.Send(ctx, agentapi.Prompt{Text: "again"}); !errors.Is(err, agentapi.ErrBusy) || len(h.fs.sent) != 2 {
		t.Fatalf("Send during the follow-up turn = %v, sent %q", err, h.fs.sent)
	}
}

func TestWebBackgroundShellChangeWithoutSubagent(t *testing.T) {
	h := openWeb(t)
	h.fs.setTasks(&rpc.TaskShellInfo{ID: "server", Command: "python3 -m http.server 8000", Description: "Game server", Status: rpc.TaskStatusRunning})
	h.fs.onEvent(ev("background", &rpc.SessionBackgroundTasksChangedData{}))
	settleTasks(t, h, 1)
	if got := h.sink.last(); got.Kind != agentapi.EventBackgroundTasks || got.BackgroundTasks == nil || !got.BackgroundTasks.Known || len(got.BackgroundTasks.Tasks) != 1 || got.BackgroundTasks.Tasks[0].Status != "running" {
		t.Fatalf("background shell missing: %+v", got)
	}
	h.fs.mu.Lock()
	h.fs.tasksErr = errors.New("task list unavailable")
	h.fs.mu.Unlock()
	h.fs.onEvent(ev("unknown", &rpc.SessionBackgroundTasksChangedData{}))
	settleTasks(t, h, 2)
	if got := h.sink.last().BackgroundTasks; got == nil || got.Known || len(got.Tasks) != 1 {
		t.Fatalf("failed refresh claimed current shell status: %+v", got)
	}
	h.fs.mu.Lock()
	h.fs.tasksErr = nil
	h.fs.mu.Unlock()
	h.fs.setTasks(&rpc.TaskShellInfo{ID: "server", Command: "python3 -m http.server 8000", Status: rpc.TaskStatusCompleted})
	h.fs.onEvent(ev("finished", &rpc.SessionBackgroundTasksChangedData{}))
	settleTasks(t, h, 3)
	if got := h.sink.last().BackgroundTasks; got == nil || !got.Known || got.Tasks[0].Status != "completed" {
		t.Fatalf("shell completion missing: %+v", got)
	}
	h.fs.setTasks(agentTaskInfo("helper", rpc.TaskStatusRunning, rpc.TaskExecutionModeBackground))
	h.fs.onEvent(ev("removed", &rpc.SessionBackgroundTasksChangedData{}))
	settleTasks(t, h, 4)
	if got := h.sink.last().BackgroundTasks; got == nil || !got.Known || len(got.Tasks) != 0 {
		t.Fatalf("removed shell or agent mislabeled as shell: %+v", got)
	}
}

func TestWebAssistantIdleDuringSendDoesNotRestoreWorking(t *testing.T) {
	h := openWeb(t)
	h.fs.beforeReturn = func(string) { h.fs.onEvent(ev("fast-idle", &rpc.AssistantIdleData{})) }
	if err := h.conv.Send(context.Background(), agentapi.Prompt{Text: "quick"}); err != nil {
		t.Fatal(err)
	}
	if got := h.sink.last(); got.Turn == nil || got.Turn.State != agentapi.TurnCompleted {
		t.Fatalf("Send restored working after foreground completion: %+v", got)
	}
}

func TestWebRepeatedAssistantIdlePreservesFailureAndRejectedSend(t *testing.T) {
	h := openWeb(t)
	if err := h.conv.Send(context.Background(), agentapi.Prompt{Text: "go"}); err != nil {
		t.Fatal(err)
	}
	h.fs.onEvent(ev("error", &rpc.SessionErrorData{ErrorType: "rate_limit", Message: "rate limited"}))
	idle := ev("foreground-idle", &rpc.AssistantIdleData{})
	h.fs.onEvent(idle)
	h.fs.onEvent(idle)
	h.fs.sendErr = rejectedError{errors.New("prompt refused")}
	if err := h.conv.Send(context.Background(), agentapi.Prompt{Text: "rejected"}); err == nil {
		t.Fatal("expected refused prompt")
	}
	h.fs.onEvent(idle)
	terminal := 0
	for _, event := range h.sink.all() {
		if event.Turn != nil && event.Turn.State != agentapi.TurnWorking {
			terminal++
		}
	}
	if got := h.sink.last(); terminal != 1 || got.Turn == nil || got.Turn.State != agentapi.TurnFailed || got.Turn.Error != "rate limited" {
		t.Fatalf("repeated idle replaced failed result: count=%d turn=%+v", terminal, got.Turn)
	}
	// The next prompt can finish before Send returns. Its actual provider
	// start event opens the next completion boundary.
	h.fs.sendErr = nil
	h.fs.beforeReturn = func(string) {
		h.fs.onEvent(ev("next-start", &rpc.AssistantTurnStartData{TurnID: "2"}))
		h.fs.onEvent(ev("next-idle", &rpc.AssistantIdleData{}))
	}
	if err := h.conv.Send(context.Background(), agentapi.Prompt{Text: "retry"}); err != nil {
		t.Fatal(err)
	}
	if got := h.sink.last(); got.Turn == nil || got.Turn.State != agentapi.TurnCompleted {
		t.Fatalf("fast follow-up restored working: %+v", got)
	}
}

func TestWebReopenPublishesKnownEmptyShellSnapshot(t *testing.T) {
	h := openWeb(t)
	if err := h.conv.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	nextSink := &recSink{}
	conv, err := h.p.Open(context.Background(), agentapi.OpenRequest{SessionID: "s-1", ConversationID: "s-1", Workdir: "/work", Events: nextSink})
	if err != nil {
		t.Fatal(err)
	}
	next := webHarness{p: h.p, fc: h.fc, fs: h.fc.sessions[len(h.fc.sessions)-1], conv: conv, sink: nextSink}
	settleTasks(t, next, 1)
	if got := nextSink.last().BackgroundTasks; got == nil || !got.Known || len(got.Tasks) != 0 {
		t.Fatalf("successful empty refresh left old unknown shells: %+v", got)
	}
}

// unresolvedIdle opens a Task whose open-time execution read failed, runs a
// turn that leaves a background shell running and delivers the main-agent
// assistant.idle without session.idle, which the CLI defers while the shell
// runs (rpc.SessionIdleData/AssistantIdleData docs). The read at that idle
// returns resolved.
func unresolvedIdle(t *testing.T, resolved agentapi.ExecutionState) (webHarness, func() *agentapi.Turn) {
	t.Helper()
	h, runtime := runtimeHarness(t)
	c := h.conv.(*conversation)
	runtime.readErr = errors.New("mode read failed")
	c.mu.Lock()
	c.execution = nil
	c.mu.Unlock()
	c.refreshExecution(context.Background())
	runtime.runtimeMu.Lock()
	runtime.readErr, runtime.state = nil, resolved
	runtime.runtimeMu.Unlock()
	if err := h.conv.Send(context.Background(), agentapi.Prompt{Text: "start a server"}); err != nil {
		t.Fatal(err)
	}
	h.fs.onEvent(ev("start", &rpc.AssistantTurnStartData{TurnID: "1"}))
	h.fs.setTasks(&rpc.TaskShellInfo{ID: "server", Command: "python3 -m http.server 8000", Status: rpc.TaskStatusRunning})
	h.fs.onEvent(ev("background", &rpc.SessionBackgroundTasksChangedData{}))
	settleTasks(t, h, 1)
	h.fs.onEvent(ev("final", &rpc.AssistantMessageData{MessageID: "reply", Content: "The server is running."}))
	h.fs.onEvent(ev("model-end", &rpc.AssistantTurnEndData{TurnID: "1"}))
	h.fs.onEvent(ev("main-idle", &rpc.AssistantIdleData{}))
	// The idle's own execution read resolves the mode.
	waitFor(t, "execution read after assistant.idle", func() bool {
		ex := h.sink.last().Execution
		return ex != nil && ex.Known
	})
	lastTurn := func() *agentapi.Turn {
		var last *agentapi.Turn
		for _, event := range h.sink.all() {
			if event.Turn != nil {
				last = event.Turn
			}
		}
		return last
	}
	return h, lastTurn
}

func TestWebAssistantIdleEndsTurnWithBackgroundShellWhenModeUnknown(t *testing.T) {
	h, lastTurn := unresolvedIdle(t, agentapi.ExecutionState{Known: true, Mode: "interactive"})
	if last := lastTurn(); last == nil || last.State != agentapi.TurnCompleted {
		t.Fatalf("turn = %+v", last)
	}
	if h.fs.aborts != 0 {
		t.Fatal("ending the turn stopped the background shell")
	}
}

// Copilot CLI 1.0.88, live: in autopilot, an agent that starts an attached
// async shell and calls task_complete ends with assistant.idle. The CLI
// defers session.idle, and with it the autopilot continuation decision,
// while an attached shell runs, so nothing follows until the shell exits.
// A follow-up sent then without a mode is delivered at once.
func TestWebAutopilotAssistantIdleWithAttachedShellEndsTurn(t *testing.T) {
	h, runtime := runtimeHarness(t)
	c := h.conv.(*conversation)
	runtime.state.Mode = "autopilot"
	c.refreshExecution(context.Background())
	ctx := context.Background()
	lastTurn := func() *agentapi.Turn {
		var last *agentapi.Turn
		for _, event := range h.sink.all() {
			if event.Turn != nil {
				last = event.Turn
			}
		}
		return last
	}
	if err := h.conv.Send(ctx, agentapi.Prompt{Text: "start a server"}); err != nil {
		t.Fatal(err)
	}
	h.fs.onEvent(ev("user", userMessage("p1", rpc.UserMessageDeliveryIdle, "start a server")))
	h.fs.onEvent(ev("start", &rpc.AssistantTurnStartData{TurnID: "0"}))
	h.fs.setTasks(&rpc.TaskShellInfo{ID: "0", Command: "python3 -m http.server 8390", AttachmentMode: rpc.TaskShellInfoAttachmentModeAttached, Status: rpc.TaskStatusRunning})
	h.fs.onEvent(ev("background", &rpc.SessionBackgroundTasksChangedData{}))
	settleTasks(t, h, 1)
	h.fs.onEvent(ev("end", &rpc.AssistantTurnEndData{TurnID: "0"}))
	h.fs.onEvent(ev("next", &rpc.AssistantTurnStartData{TurnID: "1"}))
	h.fs.onEvent(ev("done", &rpc.SessionTaskCompleteData{}))
	h.fs.onEvent(ev("end-1", &rpc.AssistantTurnEndData{TurnID: "1"}))
	h.fs.onEvent(ev("main-idle", &rpc.AssistantIdleData{}))
	waitFor(t, "autopilot turn to end with the attached shell running", func() bool {
		last := lastTurn()
		return last != nil && last.State == agentapi.TurnCompleted
	})
	if h.fs.aborts != 0 || len(h.fs.subCancels) != 0 {
		t.Fatal("ending the turn stopped the background shell")
	}
	c.mu.Lock()
	tasks := c.backgroundTasks
	c.mu.Unlock()
	if tasks == nil || len(tasks.Tasks) != 1 || tasks.Tasks[0].Status != "running" {
		t.Fatalf("background shell = %+v", tasks)
	}
	if err := h.conv.Send(ctx, agentapi.Prompt{Text: "follow-up"}); err != nil {
		t.Fatalf("follow-up after the autopilot idle: %v", err)
	}
	if last := lastTurn(); last == nil || last.State != agentapi.TurnWorking {
		t.Fatalf("follow-up did not start a turn: %+v", last)
	}
	if strings.Join(h.fs.modes, ",") != "," {
		t.Fatalf("modes %q, want the follow-up sent without a mode", h.fs.modes)
	}
}

func TestWebUnresolvedAssistantIdleKeepsAutopilotWorking(t *testing.T) {
	h, lastTurn := unresolvedIdle(t, agentapi.ExecutionState{Known: true, Mode: "autopilot"})
	if last := lastTurn(); last == nil || last.State != agentapi.TurnWorking {
		t.Fatalf("assistant.idle ended an autopilot turn: %+v", last)
	}
	h.fs.onEvent(ev("terminal", &rpc.SessionIdleData{}))
	if last := lastTurn(); last == nil || last.State != agentapi.TurnCompleted {
		t.Fatalf("terminal session.idle = %+v", last)
	}
}
