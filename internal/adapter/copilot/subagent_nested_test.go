package copilot

import (
	"testing"

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
