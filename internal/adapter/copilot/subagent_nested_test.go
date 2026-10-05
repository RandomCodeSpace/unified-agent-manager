package copilot

import (
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/github/copilot-sdk/go/rpc"
)

func TestSubagentLogKeepsTheSpawningSubagent(t *testing.T) {
	l := newSubagentLog()
	l.apply(agentEv("s1", "outer", &rpc.SubagentStartedData{ToolCallID: "call-outer", AgentName: "research"}))
	l.apply(agentEv("s2", "inner", &rpc.SubagentStartedData{ToolCallID: "call-inner", AgentName: "research", ParentID: option("outer")}))
	sa, ok := l.apply(agentEv("s3", "inner", &rpc.SubagentCompletedData{ToolCallID: "call-inner", AgentName: "research"}))
	if !ok || sa.ParentAgentID != "outer" || sa.ParentToolCallID != "call-inner" {
		t.Fatalf("inner = %+v", sa)
	}
	if outer := l.byID["outer"]; outer.ParentAgentID != "" {
		t.Fatalf("outer = %+v", *outer)
	}
}

func TestSubagentLogMarksBackgroundLaunches(t *testing.T) {
	l := newSubagentLog()
	l.apply(agentEv("s1", "bg", &rpc.SubagentStartedData{ToolCallID: "call-bg", AgentName: "research", ExecutionMode: option("background")}))
	l.apply(agentEv("s2", "fg", &rpc.SubagentStartedData{ToolCallID: "call-fg", AgentName: "research", ExecutionMode: option("sync")}))
	sa, ok := l.apply(agentEv("s3", "bg", &rpc.SubagentCompletedData{ToolCallID: "call-bg", AgentName: "research"}))
	if !ok || !sa.Background {
		t.Fatalf("background = %+v", sa)
	}
	if l.byID["fg"].Background {
		t.Fatalf("sync = %+v", *l.byID["fg"])
	}
}

func TestSubagentCountsTokensAndToolCalls(t *testing.T) {
	h := openWeb(t)
	usage := func(id, agentID string, in, out int64) {
		h.fs.onEvent(agentEv(id, agentID, &rpc.AssistantUsageData{Model: "m", InputTokens: option(in), OutputTokens: option(out)}))
	}
	updates := func() []agentapi.Subagent {
		var out []agentapi.Subagent
		for _, e := range h.sink.all() {
			if e.Kind == agentapi.EventSubagent {
				out = append(out, *e.Subagent)
			}
		}
		return out
	}
	h.fs.onEvent(agentEv("e1", "agent-1", &rpc.SubagentStartedData{ToolCallID: "call_1", AgentName: "explore"}))
	usage("e2", "agent-1", 100, 20)
	usage("e3", "", 5000, 500) // the main agent's
	h.fs.onEvent(agentEv("e4", "agent-1", &rpc.ToolExecutionStartData{ToolCallID: "t1", ToolName: "view"}))
	h.fs.onEvent(agentEv("e5", "agent-1", &rpc.ToolExecutionStartData{ToolCallID: "t1", ToolName: "view"}))
	usage("e6", "agent-1", 300, 30)
	// Started, two usage events and one tool call.
	if got := updates(); len(got) != 4 || got[3].Tokens != 450 || got[3].ToolCalls != 1 {
		t.Fatalf("live updates = %+v", got)
	}
	// The end event's totals are final; later usage is not counted.
	h.fs.onEvent(agentEv("e7", "agent-1", &rpc.SubagentCompletedData{ToolCallID: "call_1", TotalTokens: option[int64](460), TotalToolCalls: option[int64](2)}))
	usage("e8", "agent-1", 1, 1)
	if got := updates(); len(got) != 5 || got[4].Tokens != 460 || got[4].ToolCalls != 2 || got[4].Status != agentapi.SubagentCompleted {
		t.Fatalf("final updates = %+v", got)
	}

	// The cancelled end on disconnect carries the totals of every run.
	l := newSubagentLog()
	l.apply(agentEv("s1", "a", &rpc.SubagentStartedData{ToolCallID: "c", AgentName: "explore"}))
	l.apply(agentEv("s2", "a", &rpc.SubagentCompletedData{ToolCallID: "c", TotalTokens: option[int64](460), TotalToolCalls: option[int64](2)}))
	sa, ok := l.apply(agentEv("s3", "a", &rpc.SubagentCompletedData{ToolCallID: "c", Cancelled: option(true), TotalTokens: option[int64](900), TotalToolCalls: option[int64](3)}))
	if !ok || sa.Tokens != 900 || sa.ToolCalls != 3 || sa.Status != agentapi.SubagentCompleted {
		t.Fatalf("later end = %v, %+v", ok, sa)
	}
}
