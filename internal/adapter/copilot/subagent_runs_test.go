package copilot

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// runsOf is agent-1's latest emitted runs as "trigger:status" pairs. It
// fails when a running run has an end time or an ended one lacks it.
func runsOf(t *testing.T, h webHarness) string {
	t.Helper()
	sa := h.sink.subagent("agent-1")
	var out []string
	for _, r := range sa.Runs {
		if r.StartedAt.IsZero() || r.EndedAt.IsZero() != (r.Status == agentapi.SubagentRunning) {
			t.Fatalf("run times of %+v", sa.Runs)
		}
		out = append(out, r.Trigger+":"+string(r.Status))
	}
	return strings.Join(out, ",")
}

func TestWebSubagentRunsSpawnThenComplete(t *testing.T) {
	h := openWeb(t)
	start := agentEv("start", "agent-1", &rpc.SubagentStartedData{ToolCallID: "call_1", AgentName: "general-purpose"})
	h.fs.onEvent(start)
	if got := runsOf(t, h); got != "spawn:running" {
		t.Fatalf("runs = %s", got)
	}
	// Duplicate starts, a configuration and the disconnect's second,
	// cancelled completion add no run.
	h.fs.onEvent(agentEv("start-again", "agent-1", &rpc.SubagentStartedData{ToolCallID: "call_1"}))
	h.fs.onEvent(agentEv("configured", "agent-1", &rpc.SubagentConfiguredData{Model: "m"}))
	h.fs.onEvent(agentEv("done", "agent-1", &rpc.SubagentCompletedData{ToolCallID: "call_1"}))
	h.fs.onEvent(agentEv("done-again", "agent-1", &rpc.SubagentCompletedData{ToolCallID: "call_1", Cancelled: copilot.Bool(true)}))
	settleTasks(t, h, 1)
	sa := h.sink.subagent("agent-1")
	if got := runsOf(t, h); got != "spawn:completed" || !sa.Runs[0].StartedAt.Equal(start.Timestamp) || !sa.Runs[0].EndedAt.Equal(sa.EndedAt) {
		t.Fatalf("runs = %s, %+v", got, sa)
	}
	recorded := history([]copilot.SessionEvent{start, agentEv("done", "agent-1", &rpc.SubagentFailedData{ToolCallID: "call_1", Error: "boom"})})
	if runs := recorded.Subagents[0].Runs; len(runs) != 1 || runs[0].Status != agentapi.SubagentFailed || runs[0].Trigger != agentapi.SubagentTriggerSpawn {
		t.Fatalf("recorded runs = %+v", runs)
	}
}

func TestWebSubagentFollowUpsAddUserRuns(t *testing.T) {
	ctx := context.Background()
	h := openWeb(t)
	finish(t, h, agentTaskInfo("agent-1", rpc.TaskStatusIdle, rpc.TaskExecutionModeSync))
	if got := runsOf(t, h); got != "spawn:idle" {
		t.Fatalf("runs = %s", got)
	}
	h.fs.setTasks(agentTaskInfo("agent-1", rpc.TaskStatusRunning, rpc.TaskExecutionModeSync))
	before := time.Now()
	if err := h.conv.PromptSubagent(ctx, "agent-1", "again"); err != nil {
		t.Fatal(err)
	}
	sa := h.sink.subagent("agent-1")
	if got := runsOf(t, h); got != "spawn:idle,user:running" || sa.Runs[1].StartedAt.Before(before) {
		t.Fatalf("runs = %s, %+v", got, sa.Runs)
	}
	// The follow-up's own turn is not another run.
	turn := agentEv("f2", "agent-1", &rpc.AssistantTurnStartData{TurnID: "1"})
	turn.Timestamp = time.Now().Add(time.Second)
	h.fs.onEvent(turn)
	settleTasks(t, h, 2)
	h.fs.setTasks(agentTaskInfo("agent-1", rpc.TaskStatusIdle, rpc.TaskExecutionModeSync))
	h.fs.onEvent(ev("bg", &rpc.SessionBackgroundTasksChangedData{}))
	settleTasks(t, h, 3)
	if got := runsOf(t, h); got != "spawn:idle,user:idle" {
		t.Fatalf("runs = %s", got)
	}
	// An uncertain follow-up may have arrived: it runs too.
	h.fs.subMessageErr = errors.New("connection reset")
	h.fs.setTasks(agentTaskInfo("agent-1", rpc.TaskStatusRunning, rpc.TaskExecutionModeSync))
	if err := h.conv.PromptSubagent(ctx, "agent-1", "third"); !errors.Is(err, agentapi.ErrSubmissionUncertain) {
		t.Fatalf("PromptSubagent = %v", err)
	}
	settleTasks(t, h, 4)
	if got := runsOf(t, h); got != "spawn:idle,user:idle,user:running" {
		t.Fatalf("runs = %s", got)
	}
	// A refused follow-up adds none.
	h2 := openWeb(t)
	finish(t, h2, agentTaskInfo("agent-1", rpc.TaskStatusIdle, rpc.TaskExecutionModeSync))
	refused := "not accepting"
	h2.fs.subMessageResult = &rpc.TasksSendMessageResult{Error: &refused}
	if err := h2.conv.PromptSubagent(ctx, "agent-1", "hi"); err == nil {
		t.Fatal("refused follow-up succeeded")
	}
	if got := runsOf(t, h2); got != "spawn:idle" {
		t.Fatalf("runs after a refusal = %s", got)
	}
}

// The subagent's turn can start before PromptSubagent hears back from the
// CLI. That run is still the follow-up's.
func TestWebSubagentTurnDuringFollowUpSendIsUserRun(t *testing.T) {
	h := openWeb(t)
	finish(t, h, agentTaskInfo("agent-1", rpc.TaskStatusIdle, rpc.TaskExecutionModeSync))
	turn := agentEv("f1", "agent-1", &rpc.AssistantTurnStartData{TurnID: "0"})
	turn.Timestamp = time.Now().Add(time.Second)
	h.fs.subMessageHook = func() { h.fs.onEvent(turn) }
	if err := h.conv.PromptSubagent(context.Background(), "agent-1", "again"); err != nil {
		t.Fatal(err)
	}
	settleTasks(t, h, 2)
	if got, sa := runsOf(t, h), h.sink.subagent("agent-1"); got != "spawn:idle,user:running" || !sa.Runs[1].StartedAt.Equal(turn.Timestamp) {
		t.Fatalf("runs = %s, %+v", got, sa.Runs)
	}
}

func TestWebSubagentBackgroundReuseAddsAgentRun(t *testing.T) {
	h := openWeb(t)
	finish(t, h, agentTaskInfo("agent-1", rpc.TaskStatusIdle, rpc.TaskExecutionModeBackground))
	h.fs.setTasks(agentTaskInfo("agent-1", rpc.TaskStatusRunning, rpc.TaskExecutionModeBackground))
	h.fs.onEvent(ev("reused", &rpc.SessionBackgroundTasksChangedData{}))
	settleTasks(t, h, 2)
	if got := runsOf(t, h); got != "spawn:completed,agent:running" {
		t.Fatalf("runs = %s", got)
	}
	// Further reports of the same active period add no run.
	h.fs.onEvent(ev("still-running", &rpc.SessionBackgroundTasksChangedData{}))
	settleTasks(t, h, 3)
	h.fs.setTasks(agentTaskInfo("agent-1", rpc.TaskStatusIdle, rpc.TaskExecutionModeBackground))
	h.fs.onEvent(ev("finished-again", &rpc.SessionBackgroundTasksChangedData{}))
	settleTasks(t, h, 4)
	sa := h.sink.subagent("agent-1")
	if got := runsOf(t, h); got != "spawn:completed,agent:completed" || !sa.Runs[1].EndedAt.Equal(sa.EndedAt) {
		t.Fatalf("runs = %s, %+v", got, sa)
	}
}

func TestWebSubagentReuseTurnAddsAgentRun(t *testing.T) {
	for _, mode := range []rpc.TaskExecutionMode{rpc.TaskExecutionModeSync, rpc.TaskExecutionModeBackground} {
		t.Run(string(mode), func(t *testing.T) {
			h := openWeb(t)
			finish(t, h, agentTaskInfo("agent-1", rpc.TaskStatusIdle, mode))
			first := "spawn:" + string(h.sink.subagent("agent-1").Status)
			started := agentEv("reuse-turn", "agent-1", &rpc.AssistantTurnStartData{TurnID: "0"})
			started.Timestamp = time.Now().Add(time.Second)
			h.fs.onEvent(started)
			h.fs.onEvent(started) // a duplicate while it runs
			settleTasks(t, h, 2)
			if got, sa := runsOf(t, h), h.sink.subagent("agent-1"); got != first+",agent:running" || !sa.Runs[1].StartedAt.Equal(started.Timestamp) {
				t.Fatalf("runs = %s, %+v", got, sa.Runs)
			}
			task := agentTaskInfo("agent-1", rpc.TaskStatusIdle, mode).(*rpc.TaskAgentInfo)
			task.IdleSince = option(started.Timestamp.Add(time.Second))
			h.fs.setTasks(task)
			h.fs.onEvent(ev("done-again", &rpc.SessionBackgroundTasksChangedData{}))
			settleTasks(t, h, 3)
			want := first + ",agent:completed"
			if mode == rpc.TaskExecutionModeSync {
				want = first + ",agent:idle"
			}
			if got := runsOf(t, h); got != want {
				t.Fatalf("runs = %s, want %s", got, want)
			}
			// The same turn start, replayed after the run ended, is stale.
			h.fs.onEvent(started)
			settleTasks(t, h, 3)
			if got := runsOf(t, h); got != want {
				t.Fatalf("runs after a stale start = %s", got)
			}
		})
	}
}
