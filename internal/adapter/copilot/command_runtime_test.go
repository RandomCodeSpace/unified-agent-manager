package copilot

import (
	"context"
	"errors"
	"strings"
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

func TestWebCommandTextKeepsLines(t *testing.T) {
	for _, tc := range []struct {
		name    string
		result  rpc.SlashCommandInvocationResult
		text    string
		prefill string
	}{
		{"text", &rpc.SlashCommandTextResult{Text: "Context Usage\r\n  System Prompt  9.5k\x1b[31m\n  Free Space  104.8k"}, "Context Usage\n  System Prompt  9.5k\n  Free Space  104.8k", ""},
		{"completed", &rpc.SlashCommandCompletedResult{Message: copilot.String("done\r\nnext")}, "done\nnext", ""},
		{"timeline", &rpc.SlashCommandAddTimelineEntryResult{Entry: rpc.SlashCommandTimelineEntry{Text: "a\nb"}, PrefillInput: copilot.String("fix\r\nthis")}, "a\nb", "fix\nthis"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := runtimeHarness(t)
			h.fs.invoke = tc.result
			result, err := h.conv.(agentapi.CommandExecutor).ExecuteCommand(context.Background(), "goal", agentapi.Prompt{})
			if err != nil || result == nil || result.Text != tc.text || result.PrefillInput != tc.prefill {
				t.Fatalf("result=%+v err=%v", result, err)
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

// A mode change shows at once, unconfirmed, and is then read back from the
// runtime, as is an objective change; a subagent's are ignored.
func TestWebModeAndObjectiveEventsRereadExecution(t *testing.T) {
	h, runtime := runtimeHarness(t)
	executions := func() []*agentapi.ExecutionState {
		var out []*agentapi.ExecutionState
		for _, e := range h.sink.all() {
			if e.Kind == agentapi.EventExecution {
				out = append(out, e.Execution)
			}
		}
		return out
	}
	set := func(state agentapi.ExecutionState) {
		runtime.runtimeMu.Lock()
		defer runtime.runtimeMu.Unlock()
		runtime.state = state
	}
	before := len(executions())
	set(agentapi.ExecutionState{Known: true, Mode: "autopilot"})
	h.fs.onEvent(ev("mode", &rpc.SessionModeChangedData{NewMode: rpc.SessionModeAutopilot, PreviousMode: rpc.SessionModeInteractive}))
	if got := executions(); len(got) <= before || got[before].Known || got[before].Mode != "autopilot" {
		t.Fatalf("mode change = %+v", got[before:])
	}
	waitFor(t, "mode read back", func() bool {
		got := executions()
		last := got[len(got)-1]
		return last.Known && last.Mode == "autopilot"
	})

	set(agentapi.ExecutionState{Known: true, Mode: "autopilot", Objective: &agentapi.AutopilotObjective{ID: 1, Objective: "ship it", Status: "active"}})
	h.fs.onEvent(ev("goal", &rpc.SessionAutopilotObjectiveChangedData{Operation: "set"}))
	waitFor(t, "objective read back", func() bool {
		got := executions()
		last := got[len(got)-1]
		return last.Known && last.Objective != nil && last.Objective.Objective == "ship it"
	})

	h.fs.onEvent(agentEv("sub-mode", "agent-1", &rpc.SessionModeChangedData{NewMode: rpc.SessionModePlan}))
	h.fs.onEvent(agentEv("sub-goal", "agent-1", &rpc.SessionAutopilotObjectiveChangedData{Operation: "set"}))
	for _, e := range executions() {
		if e.Mode == "plan" {
			t.Fatalf("subagent mode change reached the Task: %+v", e)
		}
	}

	// Without runtime mode control the change is shown, and stays unconfirmed.
	plain := openWeb(t)
	plain.fs.onEvent(ev("mode", &rpc.SessionModeChangedData{NewMode: rpc.SessionModePlan}))
	if last := plain.sink.last(); last.Kind != agentapi.EventExecution || last.Execution.Known || last.Execution.Mode != "plan" {
		t.Fatalf("mode change without runtime = %+v", last)
	}
}

// A failed read keeps the last known state, marked unknown; a closed
// conversation reports none.
func TestWebExecutionReadFailureKeepsLastState(t *testing.T) {
	h, runtime := runtimeHarness(t)
	c := h.conv.(*conversation)
	runtime.readErr = errors.New("offline")
	c.refreshExecution(context.Background())
	if last := h.sink.last(); last.Kind != agentapi.EventExecution || last.Execution.Known || last.Execution.Mode != "interactive" {
		t.Fatalf("failed read = %+v", last)
	}
	if err := h.conv.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	n := len(h.sink.all())
	c.refreshExecution(context.Background())
	if len(h.sink.all()) != n {
		t.Fatalf("closed conversation emitted %+v", h.sink.all()[n:])
	}
}

type commandListFailure struct{ *fakeSession }

func (commandListFailure) ListCommands(context.Context) ([]rpc.SlashCommandInfo, error) {
	return nil, errors.New("catalog offline")
}

// The catalog describes the commands the Task's settings handle and marks
// those the web client cannot run; neither kind is ever invoked.
func TestWebCommandCatalogDescribesWebHandling(t *testing.T) {
	h := openWeb(t)
	ctx := context.Background()
	h.fs.commands = nil
	for _, name := range []string{"rename", allowAllCommand, "permissions", "model", "every", "share"} {
		h.fs.commands = append(h.fs.commands, rpc.SlashCommandInfo{Name: name, Description: "native", Kind: rpc.SlashCommandKindBuiltin, Input: &rpc.SlashCommandInput{Hint: "value", Required: copilot.Bool(true)}})
	}
	listed, err := h.conv.Commands(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]agentapi.Command{}
	for _, cmd := range listed {
		byName[cmd.Name] = cmd
	}
	if c := byName["rename"]; c.Description != "Rename this web Task" || c.InputHint != "Task name (omit to open rename)" || c.InputRequired || c.DisabledReason != "" {
		t.Fatalf("rename = %+v", c)
	}
	if c := byName[allowAllCommand]; !strings.Contains(c.Description, "permission policy") || c.DisabledReason != "" {
		t.Fatalf("allow-all = %+v", c)
	}
	if c := byName["permissions"]; c.Description != "Manage this Task's Safe/Yolo permission policy" || c.DisabledReason != "" {
		t.Fatalf("permissions = %+v", c)
	}
	for name, reason := range map[string]string{"every": "Scheduled commands", "share": "Publishing and remote sessions"} {
		if !strings.Contains(byName[name].DisabledReason, reason) {
			t.Fatalf("%s = %+v", name, byName[name])
		}
	}
	for _, name := range []string{"rename", allowAllCommand, "permissions", "model", "every", "share"} {
		if _, err := h.conv.(agentapi.CommandExecutor).ExecuteCommand(ctx, name, agentapi.Prompt{Text: "value"}); err == nil {
			t.Fatalf("%s ran", name)
		}
	}
	if len(h.fs.invoked) != 0 {
		t.Fatalf("invoked %v", h.fs.invoked)
	}
}

// Commands are refused before invocation when they cannot run as asked;
// an outcome the web client cannot show is reported as uncertain.
func TestWebCommandRefusalsAndUncertainOutcomes(t *testing.T) {
	h, runtime := runtimeHarness(t)
	exec := h.conv.(agentapi.CommandExecutor)
	ctx := context.Background()
	h.fs.commands = append(h.fs.commands, rpc.SlashCommandInfo{Name: "init", Kind: rpc.SlashCommandKindBuiltin, Input: &rpc.SlashCommandInput{Hint: "what", Required: copilot.Bool(true)}})
	if _, err := exec.ExecuteCommand(ctx, "init", agentapi.Prompt{}); err == nil || !strings.Contains(err.Error(), "requires arguments") {
		t.Fatalf("init without arguments = %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := exec.ExecuteCommand(cancelled, "review", agentapi.Prompt{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled command = %v", err)
	}
	if len(h.fs.invoked) != 0 {
		t.Fatalf("refused commands invoked %v", h.fs.invoked)
	}

	plan, autopilot, sandbox := rpc.SessionModePlan, rpc.SessionModeAutopilot, rpc.SandboxSessionChange("enabled")
	for name, result := range map[string]rpc.SlashCommandInvocationResult{
		"empty prompt":   &rpc.SlashCommandAgentPromptResult{Prompt: " "},
		"sandbox change": &rpc.SlashCommandTextResult{Text: "done", SandboxSessionChange: &sandbox},
		"no result":      nil,
		"plan mode":      &rpc.SlashCommandCompletedResult{Mode: &plan},
	} {
		h.fs.invoke = result
		if _, err := exec.ExecuteCommand(ctx, "goal", agentapi.Prompt{}); !errors.Is(err, agentapi.ErrSubmissionUncertain) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	// A prompt whose mode cannot be applied is not sent.
	runtime.modeErr = errors.New("mode refused")
	h.fs.invoke = &rpc.SlashCommandAgentPromptResult{Prompt: "complete the objective", Mode: &autopilot}
	if _, err := exec.ExecuteCommand(ctx, "goal", agentapi.Prompt{Text: "objective"}); !errors.Is(err, agentapi.ErrSubmissionUncertain) || len(h.fs.sent) != 0 {
		t.Fatalf("unapplied mode err = %v, sent %v", err, h.fs.sent)
	}

	// Without runtime mode control a mode change is never assumed.
	plain := openWeb(t)
	plain.fs.commands = []rpc.SlashCommandInfo{{Name: "autopilot", Kind: rpc.SlashCommandKindBuiltin}}
	plain.fs.invoke = &rpc.SlashCommandCompletedResult{Mode: &autopilot}
	if _, err := plain.conv.(agentapi.CommandExecutor).ExecuteCommand(ctx, "autopilot", agentapi.Prompt{Text: "on"}); !errors.Is(err, agentapi.ErrSubmissionUncertain) {
		t.Fatalf("mode without runtime control = %v", err)
	}

	// Plan exit approval is refused, never granted.
	if res, err := plain.fc.create[0].OnExitPlanModeRequest(copilot.ExitPlanModeRequest{}, copilot.ExitPlanModeInvocation{}); err != nil || res.Approved || res.Feedback == "" {
		t.Fatalf("plan exit = %+v, %v", res, err)
	}
}

func TestWebCommandsFailWithTheCatalogOrConversation(t *testing.T) {
	h := openWeb(t)
	ctx := context.Background()
	c := h.conv.(*conversation)
	c.mu.Lock()
	c.sess = commandListFailure{h.fs}
	c.mu.Unlock()
	if _, err := h.conv.Commands(ctx); err == nil || !strings.Contains(err.Error(), "copilot commands: catalog offline") {
		t.Fatalf("Commands = %v", err)
	}
	if _, err := h.conv.(agentapi.CommandExecutor).ExecuteCommand(ctx, "usage", agentapi.Prompt{}); err == nil || !strings.Contains(err.Error(), "catalog offline") {
		t.Fatalf("ExecuteCommand = %v", err)
	}
	if err := h.conv.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := h.conv.Commands(ctx); !errors.Is(err, agentapi.ErrClosed) {
		t.Fatalf("closed Commands = %v", err)
	}
	if _, err := h.conv.(agentapi.CommandExecutor).ExecuteCommand(ctx, "usage", agentapi.Prompt{}); !errors.Is(err, agentapi.ErrClosed) {
		t.Fatalf("closed ExecuteCommand = %v", err)
	}
}

// failingRefresh answers the first task listing and fails the rest.
type failingRefresh struct {
	*fakeSession
	lists int
}

func (s *failingRefresh) ListTasks(ctx context.Context) ([]rpc.TaskInfo, error) {
	s.lists++
	if s.lists > 1 {
		return nil, errors.New("offline")
	}
	return s.fakeSession.ListTasks(ctx)
}

func TestWebShellStopFailures(t *testing.T) {
	h := openWeb(t)
	controller := h.conv.(agentapi.BackgroundTaskController)
	ctx := context.Background()
	h.fs.setTasks(&rpc.TaskShellInfo{ID: "done", Command: "make", Status: rpc.TaskStatusCompleted}, &rpc.TaskShellInfo{ID: "server", Command: "server", Status: rpc.TaskStatusRunning})
	if _, err := controller.CancelBackgroundTask(ctx, "done"); !errors.Is(err, agentapi.ErrBackgroundTaskInactive) || len(h.fs.subCancels) != 0 {
		t.Fatalf("finished task = %v, cancels %v", err, h.fs.subCancels)
	}
	h.fs.subCancelErr = errors.New("refused")
	if _, err := controller.CancelBackgroundTask(ctx, "server"); err == nil || !strings.Contains(err.Error(), "cancel shell task: refused") {
		t.Fatalf("refused cancel = %v", err)
	}
	h.fs.subCancelErr = nil
	c := h.conv.(*conversation)
	c.mu.Lock()
	c.sess = &failingRefresh{fakeSession: h.fs}
	c.mu.Unlock()
	if snapshot, err := controller.CancelBackgroundTask(ctx, "server"); err == nil || !strings.Contains(err.Error(), "refresh failed") || snapshot.Known {
		t.Fatalf("failed refresh = %+v, %v", snapshot, err)
	}
	if err := h.conv.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.CancelBackgroundTask(ctx, "server"); !errors.Is(err, agentapi.ErrClosed) {
		t.Fatalf("closed = %v", err)
	}
}
