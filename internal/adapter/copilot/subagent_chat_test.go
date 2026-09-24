package copilot

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// agentTaskInfo is a task-list entry; an idle one entered idle now.
func agentTaskInfo(id string, status rpc.TaskStatus, mode rpc.TaskExecutionMode) rpc.TaskInfo {
	info := &rpc.TaskAgentInfo{ID: id, Status: status, ExecutionMode: option(mode)}
	if status == rpc.TaskStatusIdle {
		info.IdleSince = option(time.Now())
	}
	return info
}

func (s *fakeSession) setTasks(tasks ...rpc.TaskInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks = tasks
}

func (s *fakeSession) lists() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.taskLists
}

func (s *fakeSession) messages() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.subMessages...)
}

// subagent returns the latest emitted record of agent id.
func (r *recSink) subagent(id string) *agentapi.Subagent {
	var got *agentapi.Subagent
	for _, e := range r.all() {
		if e.Kind == agentapi.EventSubagent && e.Subagent.ID == id {
			got = e.Subagent
		}
	}
	return got
}

// settleTasks waits until at least reads task-list reads ran and none runs.
func settleTasks(t *testing.T, h webHarness, reads int) {
	t.Helper()
	c := h.conv.(*conversation)
	waitFor(t, "task-list reads", func() bool {
		if h.fs.lists() < reads {
			return false
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		return !c.listing
	})
}

// finish starts and completes agent-1 while the task list holds tasks.
func finish(t *testing.T, h webHarness, tasks ...rpc.TaskInfo) {
	t.Helper()
	h.fs.onEvent(agentEv("start", "agent-1", &rpc.SubagentStartedData{ToolCallID: "call_1", AgentName: "general-purpose"}))
	h.fs.setTasks(tasks...)
	h.fs.onEvent(agentEv("done", "agent-1", &rpc.SubagentCompletedData{ToolCallID: "call_1"}))
	settleTasks(t, h, 1)
}

func TestWebSubagentIdleOnlyFromTheExactSyncTaskListEntry(t *testing.T) {
	for _, c := range []struct {
		name  string
		tasks []rpc.TaskInfo
		want  agentapi.SubagentStatus
	}{
		{"idle sync", []rpc.TaskInfo{agentTaskInfo("agent-1", rpc.TaskStatusIdle, rpc.TaskExecutionModeSync)}, agentapi.SubagentIdle},
		{"idle background", []rpc.TaskInfo{agentTaskInfo("agent-1", rpc.TaskStatusIdle, rpc.TaskExecutionModeBackground)}, agentapi.SubagentCompleted},
		{"idle without mode", []rpc.TaskInfo{&rpc.TaskAgentInfo{ID: "agent-1", Status: rpc.TaskStatusIdle}}, agentapi.SubagentCompleted},
		{"completed", []rpc.TaskInfo{agentTaskInfo("agent-1", rpc.TaskStatusCompleted, rpc.TaskExecutionModeSync)}, agentapi.SubagentCompleted},
		{"another agent idle", []rpc.TaskInfo{agentTaskInfo("agent-2", rpc.TaskStatusIdle, rpc.TaskExecutionModeSync)}, agentapi.SubagentCompleted},
		{"not listed", nil, agentapi.SubagentCompleted},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := openWeb(t)
			finish(t, h, c.tasks...)
			if sa := h.sink.subagent("agent-1"); sa.Status != c.want {
				t.Fatalf("status = %+v, want %s", sa, c.want)
			}
		})
	}
	t.Run("running, then idle", func(t *testing.T) {
		h := openWeb(t)
		finish(t, h, agentTaskInfo("agent-1", rpc.TaskStatusRunning, rpc.TaskExecutionModeSync))
		if sa := h.sink.subagent("agent-1"); sa.Status != agentapi.SubagentCompleted {
			t.Fatalf("status = %+v", sa)
		}
		h.fs.setTasks(agentTaskInfo("agent-1", rpc.TaskStatusIdle, rpc.TaskExecutionModeSync))
		h.fs.onEvent(ev("bg", &rpc.SessionBackgroundTasksChangedData{}))
		settleTasks(t, h, 2)
		if sa := h.sink.subagent("agent-1"); sa.Status != agentapi.SubagentIdle || sa.EndedAt.IsZero() {
			t.Fatalf("status = %+v", sa)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		h := openWeb(t)
		h.fs.onEvent(agentEv("start", "agent-1", &rpc.SubagentStartedData{ToolCallID: "call_1"}))
		h.fs.setTasks(agentTaskInfo("agent-1", rpc.TaskStatusIdle, rpc.TaskExecutionModeSync))
		h.fs.onEvent(agentEv("done", "agent-1", &rpc.SubagentCompletedData{ToolCallID: "call_1", Cancelled: copilot.Bool(true)}))
		h.fs.onEvent(ev("bg", &rpc.SessionBackgroundTasksChangedData{}))
		if sa := h.sink.subagent("agent-1"); sa.Status != agentapi.SubagentCancelled || h.fs.lists() != 0 {
			t.Fatalf("status = %+v after %d reads", sa, h.fs.lists())
		}
	})
}

func TestWebSubagentFollowUpRunsAndReturnsToIdle(t *testing.T) {
	h := openWeb(t)
	ctx := context.Background()
	_ = h.conv.Send(ctx, "delegate")
	h.fs.onEvent(ev("call", &rpc.ToolExecutionStartData{ToolCallID: "call_1", ToolName: "task"}))
	finish(t, h, agentTaskInfo("agent-1", rpc.TaskStatusIdle, rpc.TaskExecutionModeSync))
	h.fs.onEvent(ev("returned", &rpc.ToolExecutionCompleteData{ToolCallID: "call_1", Success: true}))
	h.fs.onEvent(ev("main-idle", &rpc.SessionIdleData{}))
	if sa := h.sink.subagent("agent-1"); sa.Status != agentapi.SubagentIdle {
		t.Fatalf("status = %+v", sa)
	}
	mark := len(h.sink.all())

	// The CLI reports the task running once it accepts the message.
	h.fs.setTasks(agentTaskInfo("agent-1", rpc.TaskStatusRunning, rpc.TaskExecutionModeSync))
	if err := h.conv.PromptSubagent(ctx, "agent-1", "again"); err != nil {
		t.Fatal(err)
	}
	if got := h.fs.messages(); len(got) != 1 || got[0] != "agent-1: again" {
		t.Fatalf("messages = %v", got)
	}
	if sa := h.sink.subagent("agent-1"); sa.Status != agentapi.SubagentRunning || !sa.EndedAt.IsZero() || sa.ParentToolCallID != "call_1" {
		t.Fatalf("after accept = %+v", sa)
	}
	if err := h.conv.PromptSubagent(ctx, "agent-1", "more"); err == nil || len(h.fs.messages()) != 1 {
		t.Fatalf("follow-up to a running subagent = %v, messages %v", err, h.fs.messages())
	}
	settleTasks(t, h, 2)
	h.fs.onEvent(ev("bg1", &rpc.SessionBackgroundTasksChangedData{}))
	settleTasks(t, h, 3)
	if sa := h.sink.subagent("agent-1"); sa.Status != agentapi.SubagentRunning {
		t.Fatalf("while the list says running = %+v", sa)
	}

	// The follow-up's own events, all tagged with the agent.
	msgID, delivery := "follow-1", rpc.UserMessageDeliveryIdle
	for _, e := range []copilot.SessionEvent{
		agentEv("f1", "agent-1", &rpc.UserMessageData{Content: "again", MessageID: &msgID, Delivery: &delivery}),
		agentEv("f2", "agent-1", &rpc.AssistantTurnStartData{TurnID: "1"}),
		agentEv("f3", "agent-1", &rpc.AssistantMessageDeltaData{MessageID: "reply-1", DeltaContent: "SUB"}),
		agentEv("f4", "agent-1", &rpc.AssistantMessageData{MessageID: "reply-1", Content: "SUB-SECOND"}),
		agentEv("f5", "agent-1", &rpc.ToolExecutionStartData{ToolCallID: "tool-1", ToolName: "bash"}),
		agentEv("f6", "agent-1", &rpc.ToolExecutionCompleteData{ToolCallID: "tool-1", Success: true}),
		agentEv("f7", "agent-1", &rpc.AssistantUsageData{Model: "sub-model"}),
		agentEv("f8", "agent-1", &rpc.AssistantTurnEndData{TurnID: "1"}),
		agentEv("f9", "agent-1", &rpc.SessionIdleData{}),
	} {
		h.fs.onEvent(e)
	}
	var user *agentapi.Item
	for _, e := range h.sink.all()[mark:] {
		switch e.Kind {
		case agentapi.EventItem:
			if e.Item.AgentID != "agent-1" {
				t.Fatalf("follow-up item reached the main transcript: %+v", e.Item)
			}
			if e.Item.Kind == agentapi.ItemUser {
				user = e.Item
			}
		case agentapi.EventDelta:
			if e.Delta.AgentID != "agent-1" {
				t.Fatalf("follow-up delta reached the main transcript: %+v", e.Delta)
			}
		case agentapi.EventTurn, agentapi.EventContext:
			t.Fatalf("follow-up changed the Task: %+v", e)
		}
	}
	if user == nil || user.ID != "follow-1" || user.Text != "again" || user.Delivery != "" {
		t.Fatalf("follow-up user item = %+v", user)
	}

	h.fs.setTasks(agentTaskInfo("agent-1", rpc.TaskStatusIdle, rpc.TaskExecutionModeSync))
	h.fs.onEvent(ev("bg2", &rpc.SessionBackgroundTasksChangedData{}))
	settleTasks(t, h, 4)
	if sa := h.sink.subagent("agent-1"); sa.Status != agentapi.SubagentIdle || sa.EndedAt.IsZero() {
		t.Fatalf("after the follow-up = %+v", sa)
	}
	h.fs.setTasks(agentTaskInfo("agent-1", rpc.TaskStatusRunning, rpc.TaskExecutionModeSync))
	if err := h.conv.PromptSubagent(ctx, "agent-1", "third"); err != nil || len(h.fs.messages()) != 2 {
		t.Fatalf("second follow-up = %v, messages %v", err, h.fs.messages())
	}
	settleTasks(t, h, 5)
	// Stop still works on a running follow-up; the list reports the end.
	h.fs.setTasks(agentTaskInfo("agent-1", rpc.TaskStatusCancelled, rpc.TaskExecutionModeSync))
	if err := h.conv.CancelSubagent(ctx, "agent-1"); err != nil || len(h.fs.subCancels) != 1 || h.fs.subCancels[0] != "agent-1" {
		t.Fatalf("stop = %v, cancels %v", err, h.fs.subCancels)
	}
	settleTasks(t, h, 6)
	if sa := h.sink.subagent("agent-1"); sa.Status != agentapi.SubagentCancelled {
		t.Fatalf("after stop = %+v", sa)
	}
}

func TestWebPromptSubagentRefusalsAndOutcomes(t *testing.T) {
	ctx := context.Background()
	h := openWeb(t)
	h.fs.onEvent(agentEv("start", "agent-1", &rpc.SubagentStartedData{ToolCallID: "call_1"}))
	for _, id := range []string{"agent-9", "agent-1", "call_1"} {
		if err := h.conv.PromptSubagent(ctx, id, "hi"); err == nil {
			t.Fatalf("PromptSubagent(%s) was sent", id)
		}
	}
	h.fs.onEvent(agentEv("done", "agent-1", &rpc.SubagentCompletedData{ToolCallID: "call_1"}))
	settleTasks(t, h, 1)
	if err := h.conv.PromptSubagent(ctx, "agent-1", "hi"); err == nil || len(h.fs.messages()) != 0 {
		t.Fatalf("completed subagent = %v, messages %v", err, h.fs.messages())
	}

	notAccepting := "Agent not found or not accepting messages"
	for _, c := range []struct {
		name      string
		result    *rpc.TasksSendMessageResult
		err       error
		uncertain bool
		text      string
	}{
		{"not sent", &rpc.TasksSendMessageResult{Error: &notAccepting}, nil, false, notAccepting},
		{"rpc rejection", nil, rejectedError{errors.New("bad request")}, false, "bad request"},
		{"transport failure", nil, errors.New("connection reset"), true, "connection reset"},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := openWeb(t)
			finish(t, h, agentTaskInfo("agent-1", rpc.TaskStatusIdle, rpc.TaskExecutionModeSync))
			h.fs.subMessageResult, h.fs.subMessageErr = c.result, c.err
			err := h.conv.PromptSubagent(ctx, "agent-1", "hi")
			if err == nil || errors.Is(err, agentapi.ErrSubmissionUncertain) != c.uncertain || !strings.Contains(err.Error(), c.text) {
				t.Fatalf("PromptSubagent = %v", err)
			}
			if len(h.fs.messages()) != 1 {
				t.Fatalf("resent: %v", h.fs.messages())
			}
			if c.uncertain {
				// It may have arrived: running until the task list settles it.
				if !slices.ContainsFunc(h.sink.all(), func(e agentapi.Event) bool {
					return e.Kind == agentapi.EventSubagent && e.Subagent.Status == agentapi.SubagentRunning && e.Subagent.EndedAt.IsZero()
				}) {
					t.Fatal("an uncertain follow-up did not block another")
				}
				settleTasks(t, h, 2)
			}
			if sa := h.sink.subagent("agent-1"); sa.Status != agentapi.SubagentIdle {
				t.Fatalf("a follow-up that was not accepted changed the status: %+v", sa)
			}
		})
	}
}

func TestWebEndedConversationDropsIdleToCompleted(t *testing.T) {
	for _, end := range []string{"close", "shutdown"} {
		t.Run(end, func(t *testing.T) {
			h := openWeb(t)
			finish(t, h, agentTaskInfo("agent-1", rpc.TaskStatusIdle, rpc.TaskExecutionModeSync))
			if end == "close" {
				if err := h.conv.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
			} else {
				reason := "gone"
				h.fs.onEvent(ev("shutdown", &rpc.SessionShutdownData{ShutdownType: rpc.ShutdownTypeError, ErrorReason: &reason}))
			}
			if sa := h.sink.subagent("agent-1"); sa.Status != agentapi.SubagentCompleted {
				t.Fatalf("after %s = %+v", end, sa)
			}
			if err := h.conv.PromptSubagent(context.Background(), "agent-1", "hi"); !errors.Is(err, agentapi.ErrClosed) || len(h.fs.messages()) != 0 {
				t.Fatalf("PromptSubagent after %s = %v", end, err)
			}
		})
	}
}

func TestWebReopenRestoresIdleOnlyFromOneTaskListRead(t *testing.T) {
	for _, c := range []struct {
		name  string
		tasks []rpc.TaskInfo
		want  agentapi.SubagentStatus
	}{
		{"listed idle sync", []rpc.TaskInfo{agentTaskInfo("agent-1", rpc.TaskStatusIdle, rpc.TaskExecutionModeSync)}, agentapi.SubagentIdle},
		{"listed idle background", []rpc.TaskInfo{agentTaskInfo("agent-1", rpc.TaskStatusIdle, rpc.TaskExecutionModeBackground)}, agentapi.SubagentCompleted},
		{"not listed", nil, agentapi.SubagentCompleted},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := openWeb(t)
			// The recorded log also holds the cancelled completion of the
			// previous disconnect.
			h.fs.events = []copilot.SessionEvent{
				agentEv("e1", "agent-1", &rpc.SubagentStartedData{ToolCallID: "call_1"}),
				agentEv("e2", "agent-1", &rpc.SubagentCompletedData{ToolCallID: "call_1"}),
				agentEv("e3", "agent-1", &rpc.SubagentCompletedData{ToolCallID: "call_1", Cancelled: copilot.Bool(true)}),
			}
			h.fs.setTasks(c.tasks...)
			recorded, err := h.conv.History(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(recorded.Subagents) != 1 || recorded.Subagents[0].Status != c.want || h.fs.lists() != 1 {
				t.Fatalf("history = %+v after %d reads", recorded.Subagents, h.fs.lists())
			}
			err = h.conv.PromptSubagent(context.Background(), "agent-1", "hi")
			if (err == nil) != (c.want == agentapi.SubagentIdle) {
				t.Fatalf("PromptSubagent = %v", err)
			}
		})
	}
}

// The task list can still report the idle from before a follow-up it has
// accepted; only an idle entry newer than the send ends the follow-up.
func TestWebSubagentFollowUpIgnoresTheIdleFromBeforeIt(t *testing.T) {
	ctx := context.Background()
	h := openWeb(t)
	finish(t, h, agentTaskInfo("agent-1", rpc.TaskStatusIdle, rpc.TaskExecutionModeSync))
	if err := h.conv.PromptSubagent(ctx, "agent-1", "again"); err != nil {
		t.Fatal(err)
	}
	settleTasks(t, h, 2)
	h.fs.onEvent(ev("bg1", &rpc.SessionBackgroundTasksChangedData{}))
	settleTasks(t, h, 3)
	if sa := h.sink.subagent("agent-1"); sa.Status != agentapi.SubagentRunning {
		t.Fatalf("a stale idle entry ended the follow-up: %+v", sa)
	}
	if err := h.conv.PromptSubagent(ctx, "agent-1", "more"); err == nil || len(h.fs.messages()) != 1 {
		t.Fatalf("follow-up during a follow-up = %v, messages %v", err, h.fs.messages())
	}
	h.fs.setTasks(agentTaskInfo("agent-1", rpc.TaskStatusIdle, rpc.TaskExecutionModeSync))
	h.fs.onEvent(ev("bg2", &rpc.SessionBackgroundTasksChangedData{}))
	settleTasks(t, h, 4)
	if sa := h.sink.subagent("agent-1"); sa.Status != agentapi.SubagentIdle {
		t.Fatalf("after a newer idle entry = %+v", sa)
	}
}
