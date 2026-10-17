package copilot

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// phaseEv is an event at second sec, from agentID ("" for the main agent).
func phaseEv(id, agentID string, sec int64, data rpc.SessionEventData) copilot.SessionEvent {
	e := ev(id, data)
	if agentID != "" {
		e = agentEv(id, agentID, data)
	}
	e.Timestamp = time.Unix(sec, 0)
	return e
}

// notActivity drops the activity events, which tool calls bring for the
// phase.
func notActivity(evs []agentapi.Event) []agentapi.Event {
	return slices.DeleteFunc(evs, func(e agentapi.Event) bool { return e.Kind == agentapi.EventActivity })
}

// lastPhase is the phase of the last activity event.
func lastPhase(t *testing.T, h webHarness) *agentapi.Phase {
	t.Helper()
	evs := h.sink.all()
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Kind == agentapi.EventActivity {
			return evs[i].Activity.Phase
		}
	}
	t.Fatal("no activity event")
	return nil
}

func TestWebPhaseFollowsMainAndSubagentCalls(t *testing.T) {
	h := openWeb(t)
	_ = h.conv.Send(context.Background(), agentapi.Prompt{Text: "hi"})
	h.fs.onEvent(phaseEv("s1", "", 100, &rpc.AssistantTurnStartData{TurnID: "0", Model: copilot.String("gpt-6-luna")}))
	if p := lastPhase(t, h); p == nil || p.Calls != 1 || p.Subagents != 0 || p.Model != "gpt-6-luna" || !p.CallsSince.Equal(time.Unix(100, 0)) || p.Tool != "" {
		t.Fatalf("main call = %+v", p)
	}
	// The main agent spawns a subagent: the task call is no running tool.
	h.fs.onEvent(phaseEv("t1", "", 101, &rpc.ToolExecutionStartData{ToolCallID: "call-task", ToolName: taskTool, Arguments: map[string]any{"description": "look"}}))
	if p := lastPhase(t, h); p != nil {
		t.Fatalf("after the task call = %+v, want none", p)
	}
	h.fs.onEvent(phaseEv("s2", "child", 102, &rpc.AssistantTurnStartData{TurnID: "0", Model: copilot.String("ollama/deepseek-v4.1-flash")}))
	h.fs.onEvent(phaseEv("s3", "", 103, &rpc.AssistantTurnStartData{TurnID: "1", Model: copilot.String("gpt-6-luna")}))
	// Both in flight: the main agent's model shows, since the oldest call.
	if p := lastPhase(t, h); p == nil || p.Calls != 2 || p.Subagents != 1 || p.Model != "gpt-6-luna" || !p.CallsSince.Equal(time.Unix(102, 0)) {
		t.Fatalf("overlapping calls = %+v", p)
	}
	// A retry keeps the call in flight.
	h.fs.onEvent(phaseEv("r1", "child", 104, &rpc.AssistantTurnRetryData{}))
	h.fs.onEvent(phaseEv("u1", "", 105, &rpc.AssistantUsageData{Model: "gpt-6-luna"}))
	if p := lastPhase(t, h); p == nil || p.Calls != 1 || p.Subagents != 1 || p.Model != "ollama/deepseek-v4.1-flash" {
		t.Fatalf("subagent call alone = %+v", p)
	}
	// The subagent's tool call ends its model call; the tool runs locally.
	h.fs.onEvent(phaseEv("t2", "child", 106, &rpc.ToolExecutionStartData{ToolCallID: "call-bash", ToolName: "bash", Arguments: map[string]any{"command": "go test ./..."}}))
	if p := lastPhase(t, h); p == nil || p.Calls != 0 || p.Tool != "bash" || p.Detail != "go test ./..." || !p.ToolSince.Equal(time.Unix(106, 0)) {
		t.Fatalf("local tool = %+v", p)
	}
	h.fs.onEvent(phaseEv("t3", "child", 107, &rpc.ToolExecutionCompleteData{ToolCallID: "call-bash", Success: true}))
	if p := lastPhase(t, h); p != nil {
		t.Fatalf("after the tool = %+v, want none", p)
	}
}

// The short argument comes from the whole arguments, however long they are,
// and stays short.
func TestWebPhaseDetailOfLongArguments(t *testing.T) {
	h := openWeb(t)
	_ = h.conv.Send(context.Background(), agentapi.Prompt{Text: "hi"})
	body := strings.Repeat("package main\n\nfunc main() {}\n", 200) // about 5 KB
	heredoc := "cat > notes.md <<'EOF'\n" + strings.Repeat("a line of notes\n", 400) + "EOF"
	for i, c := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"create", map[string]any{"path": "/work/main.go", "file_text": body}, "/work/main.go"},
		{"edit", map[string]any{"path": "/work/main.go", "old_str": body, "new_str": body + "// end\n"}, "/work/main.go"},
		{"bash", map[string]any{"command": heredoc, "description": "Write the notes"}, ""},
	} {
		id := fmt.Sprintf("call-%d", i)
		h.fs.onEvent(phaseEv("t"+id, "", int64(100+i), &rpc.ToolExecutionStartData{ToolCallID: id, ToolName: c.tool, Arguments: c.args}))
		p := lastPhase(t, h)
		if p == nil || p.Tool != c.tool || utf8.RuneCountInString(p.Detail) > maxPhaseDetailRunes || strings.Contains(p.Detail, "{") {
			t.Fatalf("%s detail = %+v", c.tool, p)
		}
		if c.want != "" && p.Detail != c.want {
			t.Fatalf("%s detail = %q, want %q", c.tool, p.Detail, c.want)
		}
		if c.want == "" && !strings.HasPrefix(p.Detail, "cat > notes.md <<'EOF' a line of notes") {
			t.Fatalf("heredoc detail = %q", p.Detail)
		}
		h.fs.onEvent(phaseEv("c"+id, "", int64(100+i), &rpc.ToolExecutionCompleteData{ToolCallID: id, Success: true}))
	}
}

func TestWebPhaseEndsWithAbortErrorAndSubagentEnd(t *testing.T) {
	h := openWeb(t)
	_ = h.conv.Send(context.Background(), agentapi.Prompt{Text: "hi"})
	h.fs.onEvent(phaseEv("s1", "", 100, &rpc.AssistantTurnStartData{TurnID: "0"}))
	h.fs.onEvent(phaseEv("a1", "", 101, &rpc.AbortData{Reason: rpc.AbortReasonUserInitiated}))
	if p := lastPhase(t, h); p != nil {
		t.Fatalf("after abort = %+v", p)
	}
	h.fs.onEvent(phaseEv("s2", "child", 102, &rpc.AssistantTurnStartData{TurnID: "0"}))
	h.fs.onEvent(phaseEv("e1", "child", 103, &rpc.SessionErrorData{ErrorType: "query", Message: "boom"}))
	if p := lastPhase(t, h); p != nil {
		t.Fatalf("after error = %+v", p)
	}
	// A subagent that ends takes its running tools with it.
	h.fs.onEvent(phaseEv("t1", "child", 104, &rpc.ToolExecutionStartData{ToolCallID: "c1", ToolName: "view"}))
	h.fs.onEvent(phaseEv("c1", "child", 105, &rpc.SubagentCompletedData{ToolCallID: "call-task"}))
	if p := lastPhase(t, h); p != nil {
		t.Fatalf("after the subagent ended = %+v", p)
	}
}

func TestWebPhaseIsPerTurn(t *testing.T) {
	h := openWeb(t)
	ctx := context.Background()
	_ = h.conv.Send(ctx, agentapi.Prompt{Text: "hi"})
	h.fs.onEvent(phaseEv("s1", "", 100, &rpc.AssistantTurnStartData{TurnID: "0"}))
	// A tool whose end never comes does not outlive the turn.
	h.fs.onEvent(phaseEv("t1", "", 101, &rpc.ToolExecutionStartData{ToolCallID: "c1", ToolName: "bash"}))
	h.fs.onEvent(phaseEv("i1", "", 102, &rpc.SessionIdleData{Aborted: copilot.Bool(true)}))
	if e := h.sink.last(); e.Kind != agentapi.EventTurn || e.Turn.State != agentapi.TurnCancelled {
		t.Fatalf("turn end = %+v", e)
	}
	_ = h.conv.Send(ctx, agentapi.Prompt{Text: "again"})
	h.fs.onEvent(phaseEv("s2", "", 110, &rpc.AssistantTurnStartData{TurnID: "0"}))
	if p := lastPhase(t, h); p == nil || p.Calls != 1 || p.Tool != "" || !p.CallsSince.Equal(time.Unix(110, 0)) {
		t.Fatalf("next turn = %+v", p)
	}
	// The activity a new turn starts with carries its first call.
	c := h.conv.(*conversation)
	c.mu.Lock()
	p := c.act.now.Phase
	c.mu.Unlock()
	if p == nil || p.Calls != 1 {
		t.Fatalf("turn activity phase = %+v", p)
	}
}

// startBackground has the main agent start subagent bg in the background,
// its model call in flight from second 102.
func startBackground(h webHarness) {
	h.fs.onEvent(phaseEv("bt", "", 101, &rpc.ToolExecutionStartData{ToolCallID: "call-bg", ToolName: taskTool}))
	h.fs.onEvent(phaseEv("bs", "bg", 101, &rpc.SubagentStartedData{ToolCallID: "call-bg", AgentName: "general-purpose", AgentDescription: "Survey the docs", ExecutionMode: copilot.String("background"), Model: copilot.String("ollama/deepseek-v4.1-flash")}))
	h.fs.onEvent(phaseEv("bc", "", 101, &rpc.ToolExecutionCompleteData{ToolCallID: "call-bg", Success: true}))
	h.fs.onEvent(phaseEv("b1", "bg", 102, &rpc.AssistantTurnStartData{TurnID: "0"}))
}

// endTurn ends the main agent's turn and checks that the turn's end came
// first, then only the phase that outlives it.
func endTurn(t *testing.T, h webHarness, sec int64) {
	t.Helper()
	h.fs.onEvent(phaseEv(fmt.Sprintf("e%d", sec), "", sec, &rpc.AssistantTurnEndData{TurnID: "0"}))
	h.fs.onEvent(phaseEv(fmt.Sprintf("i%d", sec), "", sec, &rpc.SessionIdleData{}))
	evs := h.sink.all()
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Kind == agentapi.EventTurn {
			if evs[i].Turn.State == agentapi.TurnWorking || slices.ContainsFunc(evs[i+1:], func(e agentapi.Event) bool { return e.Kind != agentapi.EventActivity }) {
				t.Fatalf("turn end = %+v, then %+v", evs[i].Turn, evs[i+1:])
			}
			return
		}
	}
	t.Fatal("no turn end")
}

func TestWebPhaseSubagentOutlivesTheTurn(t *testing.T) {
	h := openWeb(t)
	ctx := context.Background()
	_ = h.conv.Send(ctx, agentapi.Prompt{Text: "hi"})
	h.fs.onEvent(phaseEv("s1", "", 100, &rpc.AssistantTurnStartData{TurnID: "0", Model: copilot.String("gpt-6-luna")}))
	startBackground(h)
	h.fs.onEvent(phaseEv("s1b", "", 103, &rpc.AssistantTurnStartData{TurnID: "1", Model: copilot.String("gpt-6-luna")}))
	// A subagent whose start was never seen does not outlive the turn.
	h.fs.onEvent(phaseEv("g1", "ghost", 103, &rpc.AssistantTurnStartData{TurnID: "0"}))
	if p := lastPhase(t, h); p == nil || p.Calls != 3 || p.Subagents != 2 || p.Model != "gpt-6-luna" || p.Agent != "" {
		t.Fatalf("in the turn = %+v", p)
	}
	endTurn(t, h, 104)
	want := agentapi.Phase{Calls: 1, Subagents: 1, Model: "ollama/deepseek-v4.1-flash", Agent: "Survey the docs", CallsSince: time.Unix(102, 0)}
	if p := lastPhase(t, h); p == nil || *p != want {
		t.Fatalf("after the turn = %+v, want %+v", p, want)
	}

	// Between turns it goes on: its tool runs locally, then its next call.
	h.fs.onEvent(phaseEv("bt1", "bg", 105, &rpc.ToolExecutionStartData{ToolCallID: "c1", ToolName: "bash", Arguments: map[string]any{"command": "go test ./..."}}))
	if p := lastPhase(t, h); p == nil || p.Calls != 0 || p.Tool != "bash" || !p.ToolSubagent || p.Detail != "go test ./..." {
		t.Fatalf("its tool between turns = %+v", p)
	}
	h.fs.onEvent(phaseEv("bt2", "bg", 106, &rpc.ToolExecutionCompleteData{ToolCallID: "c1", Success: true}))
	h.fs.onEvent(phaseEv("b2", "bg", 107, &rpc.AssistantTurnStartData{TurnID: "1"}))
	if p := lastPhase(t, h); p == nil || p.Calls != 1 || p.Subagents != 1 || p.Tool != "" || !p.CallsSince.Equal(time.Unix(107, 0)) {
		t.Fatalf("its call between turns = %+v", p)
	}

	// The next turn keeps it, with the main agent's call.
	_ = h.conv.Send(ctx, agentapi.Prompt{Text: "again"})
	h.fs.onEvent(phaseEv("s2", "", 110, &rpc.AssistantTurnStartData{TurnID: "0", Model: copilot.String("gpt-6-luna")}))
	if p := lastPhase(t, h); p == nil || p.Calls != 2 || p.Subagents != 1 || p.Model != "gpt-6-luna" || p.Agent != "" || !p.CallsSince.Equal(time.Unix(107, 0)) {
		t.Fatalf("in the next turn = %+v", p)
	}
	endTurn(t, h, 111)
	if p := lastPhase(t, h); p == nil || p.Calls != 1 || p.Subagents != 1 {
		t.Fatalf("after the next turn = %+v", p)
	}
	// Its end ends it: nothing is left, and a trailing turn start or tool
	// no later than its end starts nothing.
	h.fs.onEvent(phaseEv("bd", "bg", 112, &rpc.SubagentCompletedData{ToolCallID: "call-bg"}))
	if p := lastPhase(t, h); p != nil {
		t.Fatalf("after it completed = %+v", p)
	}
	h.fs.onEvent(phaseEv("b3", "bg", 112, &rpc.AssistantTurnStartData{TurnID: "2"}))
	h.fs.onEvent(phaseEv("bt3", "bg", 113, &rpc.ToolExecutionStartData{ToolCallID: "c3", ToolName: "bash"}))
	if p := lastPhase(t, h); p != nil {
		t.Fatalf("after its trailing events = %+v", p)
	}
	// A later start is a follow-up that resumes it.
	h.fs.onEvent(phaseEv("b4", "bg", 120, &rpc.AssistantTurnStartData{TurnID: "3"}))
	if p := lastPhase(t, h); p == nil || p.Subagents != 1 || !p.CallsSince.Equal(time.Unix(120, 0)) {
		t.Fatalf("a follow-up = %+v", p)
	}
}

// A turn end held for its todo list is followed by the phase that outlives
// it, so the end, which clears the turn's activity, never hides it.
func TestWebPhaseFollowsAHeldTurnEnd(t *testing.T) {
	h := openWeb(t)
	entered, gate := gateTodoReads(t, h.fs)
	h.fs.setTodos(todoRow("a", "Step", "in_progress"))
	_ = h.conv.Send(context.Background(), agentapi.Prompt{Text: "hi"})
	h.fs.onEvent(phaseEv("s1", "", 100, &rpc.AssistantTurnStartData{TurnID: "0"}))
	startBackground(h)
	h.fs.onEvent(todosChanged("t1", "", 103))
	h.fs.onEvent(phaseEv("i1", "", 104, &rpc.SessionIdleData{}))
	<-entered
	gate <- struct{}{}
	waitFor(t, "the phase after the released turn end", func() bool {
		evs := h.sink.all()
		for i := len(evs) - 1; i > 0; i-- {
			if evs[i].Kind == agentapi.EventTurn {
				return false
			}
			if p := evs[i].Activity; evs[i].Kind == agentapi.EventActivity && p.Phase != nil && p.Phase.Subagents == 1 && p.Phase.Calls == 1 {
				return evs[i-1].Kind == agentapi.EventTurn && evs[i-1].Turn.State == agentapi.TurnCompleted
			}
		}
		return false
	})
}

func TestWebPhaseBetweenTurnsEndsWithTheSubagent(t *testing.T) {
	between := func(t *testing.T) webHarness {
		t.Helper()
		h := openWeb(t)
		_ = h.conv.Send(context.Background(), agentapi.Prompt{Text: "hi"})
		h.fs.onEvent(phaseEv("s1", "", 100, &rpc.AssistantTurnStartData{TurnID: "0"}))
		startBackground(h)
		endTurn(t, h, 103)
		h.fs.onEvent(phaseEv("bt1", "bg", 104, &rpc.ToolExecutionStartData{ToolCallID: "c1", ToolName: "view"}))
		h.fs.onEvent(phaseEv("b2", "bg", 105, &rpc.AssistantTurnStartData{TurnID: "1"}))
		if p := lastPhase(t, h); p == nil || p.Subagents != 1 || p.Tool != "view" {
			t.Fatalf("between turns = %+v", p)
		}
		return h
	}
	t.Run("failed", func(t *testing.T) {
		h := between(t)
		h.fs.onEvent(phaseEv("bf", "bg", 106, &rpc.SubagentFailedData{ToolCallID: "call-bg", Error: "boom"}))
		if p := lastPhase(t, h); p != nil {
			t.Fatalf("after it failed = %+v", p)
		}
		// Its trailing events start nothing.
		h.fs.onEvent(phaseEv("b3", "bg", 107, &rpc.AssistantTurnStartData{TurnID: "2"}))
		h.fs.onEvent(phaseEv("bt3", "bg", 107, &rpc.ToolExecutionStartData{ToolCallID: "c3", ToolName: "bash"}))
		if p := lastPhase(t, h); p != nil {
			t.Fatalf("after its trailing events = %+v", p)
		}
	})
	t.Run("aborted", func(t *testing.T) {
		h := between(t)
		h.fs.onEvent(phaseEv("ba", "bg", 106, &rpc.AbortData{Reason: rpc.AbortReasonUserInitiated}))
		if p := lastPhase(t, h); p != nil {
			t.Fatalf("after its abort = %+v", p)
		}
	})
	t.Run("stopped", func(t *testing.T) {
		h := between(t)
		h.fs.setTasks(agentTaskInfo("bg", rpc.TaskStatusCancelled, rpc.TaskExecutionModeBackground))
		if err := h.conv.CancelSubagent(context.Background(), "bg"); err != nil {
			t.Fatal(err)
		}
		if p := lastPhase(t, h); p != nil {
			t.Fatalf("after the stop = %+v", p)
		}
		h.fs.onEvent(phaseEv("b3", "bg", 107, &rpc.AssistantTurnStartData{TurnID: "2"}))
		h.fs.onEvent(phaseEv("bt3", "bg", 107, &rpc.ToolExecutionStartData{ToolCallID: "c3", ToolName: "bash"}))
		if p := lastPhase(t, h); p != nil {
			t.Fatalf("after its trailing events = %+v", p)
		}
	})
	t.Run("settled by the task list", func(t *testing.T) {
		h := between(t)
		reads := h.fs.lists()
		// An end the list dates before its latest call settles nothing.
		h.fs.setTasks(&rpc.TaskAgentInfo{ID: "bg", Status: rpc.TaskStatusCompleted, CompletedAt: option(time.Unix(104, 0))})
		h.fs.onEvent(ev("tc1", &rpc.SessionBackgroundTasksChangedData{}))
		settleTasks(t, h, reads+1)
		if p := lastPhase(t, h); p == nil || p.Subagents != 1 {
			t.Fatalf("after an older end = %+v", p)
		}
		h.fs.setTasks(&rpc.TaskAgentInfo{ID: "bg", Status: rpc.TaskStatusCompleted, CompletedAt: option(time.Unix(106, 0))})
		h.fs.onEvent(ev("tc2", &rpc.SessionBackgroundTasksChangedData{}))
		settleTasks(t, h, reads+2)
		if p := lastPhase(t, h); p != nil {
			t.Fatalf("after the list settled it = %+v", p)
		}
	})
	t.Run("settled idle by the task list", func(t *testing.T) {
		h := between(t)
		reads := h.fs.lists()
		// An idle entry ended when it went idle, whatever run completed
		// before, as applyTasksLocked reads it.
		h.fs.setTasks(&rpc.TaskAgentInfo{ID: "bg", Status: rpc.TaskStatusIdle, CompletedAt: option(time.Unix(104, 0)), IdleSince: option(time.Unix(106, 0))})
		h.fs.onEvent(ev("tc1", &rpc.SessionBackgroundTasksChangedData{}))
		settleTasks(t, h, reads+1)
		if p := lastPhase(t, h); p != nil {
			t.Fatalf("after the list showed it idle = %+v", p)
		}
	})
}

func TestWebPhaseCompactionIsAMainAgentCall(t *testing.T) {
	h := openWeb(t)
	_ = h.conv.Send(context.Background(), agentapi.Prompt{Text: "hi"})
	h.fs.onEvent(phaseEv("s1", "", 100, &rpc.AssistantTurnStartData{TurnID: "0", Model: copilot.String("gpt-6-luna")}))
	h.fs.onEvent(phaseEv("u1", "", 101, &rpc.AssistantUsageData{Model: "gpt-6-luna"}))
	// In a turn: its start names no model, so the Thread's shows; it runs
	// alongside the main agent's tools.
	h.fs.onEvent(phaseEv("k1", "", 101, &rpc.SessionCompactionStartData{}))
	h.fs.onEvent(phaseEv("t1", "", 102, &rpc.ToolExecutionStartData{ToolCallID: "c1", ToolName: "bash"}))
	if p := lastPhase(t, h); p == nil || p.Calls != 1 || p.Subagents != 0 || !p.Compacting || p.Model != "gpt-6-luna" || !p.CallsSince.Equal(time.Unix(101, 0)) || p.Tool != "bash" {
		t.Fatalf("compacting in the turn = %+v", p)
	}
	// The main agent's own call shows over it, both counted.
	h.fs.onEvent(phaseEv("s2", "", 103, &rpc.AssistantTurnStartData{TurnID: "1"}))
	if p := lastPhase(t, h); p == nil || p.Calls != 2 || p.Compacting {
		t.Fatalf("with the main agent's call = %+v", p)
	}
	h.fs.onEvent(phaseEv("u2", "", 104, &rpc.AssistantUsageData{Model: "gpt-6-luna"}))
	h.fs.onEvent(phaseEv("k2", "", 105, &rpc.SessionCompactionCompleteData{Success: true}))
	if p := lastPhase(t, h); p == nil || p.Calls != 0 || p.Compacting || p.Tool != "bash" {
		t.Fatalf("after it completed = %+v", p)
	}
	h.fs.onEvent(phaseEv("t2", "", 106, &rpc.ToolExecutionCompleteData{ToolCallID: "c1", Success: true}))

	// A failed one ends, and so does one the main agent's abort ends.
	h.fs.onEvent(phaseEv("k3", "", 107, &rpc.SessionCompactionStartData{}))
	h.fs.onEvent(phaseEv("k4", "", 108, &rpc.SessionCompactionCompleteData{Success: false, Error: copilot.String("model error"), StatusCode: option(int64(500))}))
	if p := lastPhase(t, h); p != nil {
		t.Fatalf("after it failed = %+v", p)
	}
	h.fs.onEvent(phaseEv("k5", "", 109, &rpc.SessionCompactionStartData{}))
	h.fs.onEvent(phaseEv("a1", "", 110, &rpc.AbortData{Reason: rpc.AbortReasonUserInitiated}))
	if p := lastPhase(t, h); p != nil {
		t.Fatalf("after the abort = %+v", p)
	}
	// One still running at the idle ends with the turn.
	h.fs.onEvent(phaseEv("k6", "", 111, &rpc.SessionCompactionStartData{}))
	endTurn(t, h, 112)
	if evs := h.sink.all(); evs[len(evs)-1].Kind != agentapi.EventTurn {
		t.Fatalf("after the turn = %+v, want no phase after its end", evs[len(evs)-1].Activity)
	}

	// Between turns (/compact, or in the background) it shows on its own.
	h.fs.onEvent(phaseEv("k7", "", 120, &rpc.SessionCompactionStartData{Trigger: option(rpc.CompactionTriggerManual)}))
	if p := lastPhase(t, h); p == nil || p.Calls != 1 || p.Subagents != 0 || !p.Compacting || !p.CallsSince.Equal(time.Unix(120, 0)) {
		t.Fatalf("compacting between turns = %+v", p)
	}
	h.fs.onEvent(phaseEv("k8", "", 125, &rpc.SessionCompactionCompleteData{Success: true}))
	if p := lastPhase(t, h); p != nil {
		t.Fatalf("after it completed between turns = %+v", p)
	}
	// A main-agent error between turns ends one too.
	h.fs.onEvent(phaseEv("k9", "", 130, &rpc.SessionCompactionStartData{}))
	h.fs.onEvent(phaseEv("x1", "", 131, &rpc.SessionErrorData{ErrorType: "query", Message: "boom"}))
	if p := lastPhase(t, h); p != nil {
		t.Fatalf("after the error = %+v", p)
	}
	// So does the session's idle between turns.
	h.fs.onEvent(phaseEv("k10", "", 140, &rpc.SessionCompactionStartData{}))
	h.fs.onEvent(phaseEv("i2", "", 141, &rpc.SessionIdleData{}))
	if p := lastPhase(t, h); p != nil {
		t.Fatalf("after the idle = %+v", p)
	}
}
