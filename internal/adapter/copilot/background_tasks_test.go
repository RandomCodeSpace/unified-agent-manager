package copilot

import (
	"context"
	"errors"
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
