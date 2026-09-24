package copilot

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

type runtimeSession struct {
	*fakeSession
	runtimeMu sync.Mutex
	state     agentapi.ExecutionState
	sets      []rpc.SessionMode
	modeErr   error
	readErr   error
}

func (s *runtimeSession) Execution(context.Context) (*agentapi.ExecutionState, error) {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	out := s.state
	return &out, s.readErr
}
func (s *runtimeSession) SetExecutionMode(_ context.Context, mode rpc.SessionMode) error {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	s.sets = append(s.sets, mode)
	if s.modeErr != nil {
		return s.modeErr
	}
	s.state.Mode = string(mode)
	return nil
}
func runtimeHarness(t *testing.T) (webHarness, *runtimeSession) {
	h := openWeb(t)
	runtime := &runtimeSession{fakeSession: h.fs, state: agentapi.ExecutionState{Known: true, Mode: "interactive"}}
	c := h.conv.(*conversation)
	c.mu.Lock()
	c.sess = runtime
	c.mu.Unlock()
	c.refreshExecution(context.Background())
	h.fs.commands = []rpc.SlashCommandInfo{{Name: "autopilot", Aliases: []string{"goal"}, Kind: rpc.SlashCommandKindBuiltin, AllowDuringAgentExecution: true}, {Name: "review", Kind: rpc.SlashCommandKindBuiltin}}
	return h, runtime
}

func TestWebCommandResultsDoNotStartForeground(t *testing.T) {
	for _, tc := range []struct {
		result rpc.SlashCommandInvocationResult
		kind   string
	}{
		{&rpc.SlashCommandTextResult{Text: "hello\x1b[31m", Markdown: copilot.Bool(true)}, "text"},
		{&rpc.SlashCommandCompletedResult{}, "completed"},
		{&rpc.SlashCommandSelectSubcommandResult{Command: "autopilot", Title: "Choose", Options: []rpc.SlashCommandSelectSubcommandOption{{Name: "on", Description: "Enable"}}}, "select"},
		{&rpc.SlashCommandAddTimelineEntryResult{Entry: rpc.SlashCommandTimelineEntry{Text: "status"}}, "text"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			h, _ := runtimeHarness(t)
			h.fs.invoke = tc.result
			result, err := h.conv.(agentapi.CommandExecutor).ExecuteCommand(context.Background(), "goal", agentapi.Prompt{})
			if err != nil || result == nil || result.Kind != tc.kind {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if len(h.fs.msgs) != 0 || h.fs.invoked[0] != "autopilot " {
				t.Fatalf("invoke=%v messages=%v", h.fs.invoked, h.fs.msgs)
			}
			for _, ev := range h.sink.all() {
				if ev.Turn != nil {
					t.Fatal("nonprompt command started foreground")
				}
			}
		})
	}
}

func TestWebAutopilotLifecycleAndStop(t *testing.T) {
	h, runtime := runtimeHarness(t)
	mode := rpc.SessionModeAutopilot
	h.fs.invoke = &rpc.SlashCommandAgentPromptResult{Prompt: "complete the objective", Mode: &mode}
	if _, err := h.conv.(agentapi.CommandExecutor).ExecuteCommand(context.Background(), "goal", agentapi.Prompt{Text: "objective"}); err != nil {
		t.Fatal(err)
	}
	if len(runtime.sets) != 1 || runtime.sets[0] != mode {
		t.Fatalf("mode mutations=%v", runtime.sets)
	}
	h.fs.onEvent(ev("assistant", &rpc.AssistantIdleData{}))
	h.fs.onEvent(ev("continue", &rpc.SessionIdleData{Mode: &mode}))
	for _, event := range h.sink.all() {
		if event.Turn != nil && event.Turn.State != agentapi.TurnWorking {
			t.Fatal("intermediate idle completed autopilot")
		}
	}
	h.fs.onEvent(ev("terminal", &rpc.SessionIdleData{}))
	if last := h.sink.last(); last.Turn == nil || last.Turn.State != agentapi.TurnCompleted {
		t.Fatalf("terminal idle=%+v", last)
	}
	if runtime.state.Mode != "autopilot" {
		t.Fatal("final idle invented mode exit")
	}
	if err := h.conv.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runtime.state.Mode != "interactive" || h.fs.aborts != 1 {
		t.Fatalf("stop state=%+v aborts=%d", runtime.state, h.fs.aborts)
	}
}

func TestWebAutopilotCompletedModeAndPartialFailure(t *testing.T) {
	h, runtime := runtimeHarness(t)
	mode := rpc.SessionModeAutopilot
	h.fs.invoke = &rpc.SlashCommandCompletedResult{Mode: &mode}
	result, err := h.conv.(agentapi.CommandExecutor).ExecuteCommand(context.Background(), "autopilot", agentapi.Prompt{Text: "on"})
	if err != nil || result.Kind != "completed" || runtime.state.Mode != "autopilot" || len(h.fs.sent) > 0 {
		t.Fatalf("completed mode result=%+v state=%+v err=%v", result, runtime.state, err)
	}
	if _, err = h.conv.(agentapi.CommandExecutor).ExecuteCommand(context.Background(), "autopilot", agentapi.Prompt{Text: "on"}); err != nil || len(runtime.sets) != 1 {
		t.Fatalf("mode already applied repeated: %v %v", runtime.sets, err)
	}
	runtime.modeErr = errors.New("mode off failed")
	if err = h.conv.Cancel(context.Background()); err == nil || h.fs.aborts != 1 {
		t.Fatalf("partial stop=%v aborts=%d", err, h.fs.aborts)
	}
	if runtime.state.Mode != "autopilot" {
		t.Fatal("failed stop claimed interactive")
	}
}

func TestWebCommandPostInvokeFailureIsUncertain(t *testing.T) {
	h, _ := runtimeHarness(t)
	h.fs.invoke = &rpc.SlashCommandAgentPromptResult{Prompt: "review"}
	h.fs.sendErr = rejectedError{errors.New("rejected")}
	if _, err := h.conv.(agentapi.CommandExecutor).ExecuteCommand(context.Background(), "review", agentapi.Prompt{}); !errors.Is(err, agentapi.ErrSubmissionUncertain) {
		t.Fatalf("postinvoke failure=%v", err)
	}
	h.fs.invokeErr = errors.New("timeout")
	if _, err := h.conv.(agentapi.CommandExecutor).ExecuteCommand(context.Background(), "autopilot", agentapi.Prompt{}); !errors.Is(err, agentapi.ErrSubmissionUncertain) {
		t.Fatalf("invoke timeout=%v", err)
	}
	before := len(h.fs.invoked)
	if _, err := h.conv.(agentapi.CommandExecutor).ExecuteCommand(context.Background(), "autopilot", agentapi.Prompt{Attachments: []agentapi.Blob{{Name: "file"}}}); err == nil || len(h.fs.invoked) != before {
		t.Fatal("attachment rejected after invoking")
	}
}

func TestWebShellStopIsTypedAndKeepsForeground(t *testing.T) {
	h := openWeb(t)
	shell := &rpc.TaskShellInfo{ID: "server", Command: "server", Status: rpc.TaskStatusRunning}
	h.fs.setTasks(shell, agentTaskInfo("agent", rpc.TaskStatusRunning, rpc.TaskExecutionModeBackground))
	controller := h.conv.(agentapi.BackgroundTaskController)
	snapshot, err := controller.CancelBackgroundTask(context.Background(), "server")
	if err != nil || !snapshot.Known || len(snapshot.Tasks) != 1 || snapshot.Tasks[0].Status != "running" {
		t.Fatalf("accepted pending=%+v %v", snapshot, err)
	}
	if h.fs.aborts != 0 || len(h.fs.subCancels) != 1 || h.fs.subCancels[0] != "server" {
		t.Fatalf("wrong cancellation=%v aborts=%d", h.fs.subCancels, h.fs.aborts)
	}
	if _, err = controller.CancelBackgroundTask(context.Background(), "agent"); !errors.Is(err, agentapi.ErrBackgroundTaskNotFound) || len(h.fs.subCancels) != 1 {
		t.Fatalf("subagent through shell route=%v", err)
	}
	h.fs.subCancelRejected = true
	if _, err = controller.CancelBackgroundTask(context.Background(), "server"); !errors.Is(err, agentapi.ErrBackgroundTaskInactive) {
		t.Fatalf("rejected cancel reported success: %v", err)
	}
	h.fs.tasksErr = errors.New("offline")
	snapshot, err = controller.CancelBackgroundTask(context.Background(), "server")
	if err == nil || snapshot.Known || len(snapshot.Tasks) != 1 {
		t.Fatalf("failed refresh=%+v %v", snapshot, err)
	}
}

func TestWebAutopilotIdleBoundaryEstablishesUnknownMode(t *testing.T) {
	h := openWeb(t)
	c := h.conv.(*conversation)
	c.mu.Lock()
	c.execution = &agentapi.ExecutionState{Known: false}
	c.mu.Unlock()
	if err := h.conv.Send(context.Background(), agentapi.Prompt{Text: "continue"}); err != nil {
		t.Fatal(err)
	}
	mode := rpc.SessionModeAutopilot
	h.fs.onEvent(ev("continuation", &rpc.SessionIdleData{Mode: &mode}))
	h.fs.onEvent(ev("next-turn", &rpc.AssistantTurnStartData{}))
	h.fs.onEvent(ev("assistant", &rpc.AssistantIdleData{}))
	for _, event := range h.sink.all() {
		if event.Turn != nil && event.Turn.State != agentapi.TurnWorking {
			t.Fatalf("unknown initial mode finished early: %+v", event)
		}
	}
	h.fs.onEvent(ev("terminal", &rpc.SessionIdleData{}))
	if last := h.sink.last(); last.Turn == nil || last.Turn.State != agentapi.TurnCompleted {
		t.Fatalf("terminal event=%+v", last)
	}
}

func TestWebReadOnlyNativeCommandsRejectArgumentsBeforeInvoke(t *testing.T) {
	h := openWeb(t)
	h.fs.commands = []rpc.SlashCommandInfo{}
	for _, name := range []string{"env", "skills"} {
		h.fs.commands = append(h.fs.commands, rpc.SlashCommandInfo{Name: name, Kind: rpc.SlashCommandKindBuiltin, Input: &rpc.SlashCommandInput{Hint: "mutating options", Choices: []rpc.SlashCommandInputChoice{{Name: "change"}}}})
	}
	listed, err := h.conv.Commands(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, cmd := range listed {
		if cmd.DisabledReason != "" || cmd.InputHint != "" || len(cmd.InputChoices) != 0 {
			t.Fatalf("wrong readonly catalog=%+v", cmd)
		}
	}
	h.fs.invoke = &rpc.SlashCommandTextResult{Text: "current state"}
	for _, name := range []string{"env", "skills"} {
		result, err := h.conv.(agentapi.CommandExecutor).ExecuteCommand(context.Background(), name, agentapi.Prompt{})
		if err != nil || result == nil || result.Text != "current state" {
			t.Fatalf("readonly result=%+v err=%v", result, err)
		}
		before := len(h.fs.invoked)
		if _, err := h.conv.(agentapi.CommandExecutor).ExecuteCommand(context.Background(), name, agentapi.Prompt{Text: "change"}); err == nil || len(h.fs.invoked) != before {
			t.Fatalf("argument invoked: %s err=%v", name, err)
		}
	}
}

func TestWebUnknownExecutionDoesNotAssumeInteractiveCompletion(t *testing.T) {
	h := openWeb(t)
	c := h.conv.(*conversation)
	c.mu.Lock()
	c.execution = &agentapi.ExecutionState{Known: false}
	c.mu.Unlock()
	h.fs.onEvent(ev("start", &rpc.AssistantTurnStartData{}))
	h.fs.onEvent(ev("iteration-idle", &rpc.AssistantIdleData{}))
	for _, event := range h.sink.all() {
		if event.Turn != nil && event.Turn.State != agentapi.TurnWorking {
			t.Fatal("unknown mode treated as interactive")
		}
	}
	h.fs.onEvent(ev("terminal", &rpc.SessionIdleData{}))
	if last := h.sink.last(); last.Turn == nil || last.Turn.State != agentapi.TurnCompleted {
		t.Fatalf("terminal=%+v", last)
	}
}

func TestWebLeavingAutopilotRestoresAssistantIdleCompletion(t *testing.T) {
	h, runtime := runtimeHarness(t)
	c := h.conv.(*conversation)
	runtime.state.Mode = "autopilot"
	c.refreshExecution(context.Background())
	h.fs.onEvent(ev("start", &rpc.AssistantTurnStartData{}))
	runtime.state.Mode = "interactive"
	c.refreshExecution(context.Background())
	h.fs.onEvent(ev("foreground-idle", &rpc.AssistantIdleData{}))
	if last := h.sink.last(); last.Turn == nil || last.Turn.State != agentapi.TurnCompleted {
		t.Fatalf("interactive idle=%+v", last)
	}
}

func TestWebAutopilotContinuationItemRetainsStructuredDelivery(t *testing.T) {
	h := openWeb(t)
	message := userMessage("continuation", rpc.UserMessageDeliveryIdle, "continue")
	message.IsAutopilotContinuation = copilot.Bool(true)
	event := ev("auto-user", message)
	h.fs.onEvent(event)
	if item := h.sink.last().Item; item == nil || item.Delivery != agentapi.DeliveryAutopilot {
		t.Fatalf("live continuation=%+v", item)
	}
	h.fs.events = []copilot.SessionEvent{event}
	recorded, err := h.conv.History(context.Background())
	if err != nil || len(recorded.Items) != 1 || recorded.Items[0].Delivery != agentapi.DeliveryAutopilot {
		t.Fatalf("recorded continuation=%+v err=%v", recorded.Items, err)
	}
}
