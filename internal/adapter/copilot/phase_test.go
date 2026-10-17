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
