package copilot

import (
	"testing"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

const readOnceNote = "uam read-once: /work/a.go is unchanged since your earlier view in this conversation; use that result."

// view runs the post-tool hook for a view of path (and rng) by agent that
// returned text, and returns the text the model gets.
func view(t *testing.T, h webHarness, agent, path string, rng []any, text string) string {
	t.Helper()
	args := map[string]any{"path": path}
	if rng != nil {
		args["view_range"] = rng
	}
	result := map[string]any{"resultType": "success", "textResultForLlm": text, "sessionLog": "log"}
	out, err := h.fc.create[0].Hooks.OnPostToolUse(copilot.PostToolUseHookInput{AgentID: agent, ToolName: "view", ToolArgs: args, ToolResult: result}, copilot.HookInvocation{})
	if err != nil {
		t.Fatal(err)
	}
	if out == nil {
		return text
	}
	got := out.ModifiedResult.(map[string]any)
	if got["resultType"] != "success" || got["sessionLog"] != "log" {
		t.Errorf("replacement lost fields: %v", got)
	}
	return got["textResultForLlm"].(string)
}

func TestReadOnceRepeatIsNoted(t *testing.T) {
	h := openWeb(t)
	if got := view(t, h, "", "/work/a.go", nil, "package a"); got != "package a" {
		t.Fatalf("first view = %q", got)
	}
	for range 2 {
		if got := view(t, h, "", "/work/a.go", nil, "package a"); got != readOnceNote {
			t.Fatalf("repeat view = %q", got)
		}
	}
	if n := h.conv.(*conversation).viewRepeats; n != 2 {
		t.Errorf("viewRepeats = %d, want 2", n)
	}
	endTurn(t, h, 10)
	if n := h.conv.(*conversation).viewRepeats; n != 0 {
		t.Errorf("viewRepeats after the turn = %d, want 0", n)
	}
}

func TestReadOnceChangedContentPasses(t *testing.T) {
	h := openWeb(t)
	view(t, h, "", "/work/a.go", nil, "package a")
	if got := view(t, h, "", "/work/a.go", nil, "package a // edited"); got != "package a // edited" {
		t.Fatalf("changed view = %q", got)
	}
	// The changed text is now the one the agent has.
	if got := view(t, h, "", "/work/a.go", nil, "package a // edited"); got != readOnceNote {
		t.Fatalf("repeat of the changed view = %q", got)
	}
	if got := view(t, h, "", "/work/a.go", nil, "package a"); got != "package a" {
		t.Fatalf("view after a revert = %q", got)
	}
}

func TestReadOncePerAgentAndRange(t *testing.T) {
	h := openWeb(t)
	view(t, h, "", "/work/a.go", nil, "package a")
	for name, got := range map[string]string{
		"range":       view(t, h, "", "/work/a.go", []any{float64(1), float64(5)}, "package a"),
		"subagent":    view(t, h, "child", "/work/a.go", nil, "package a"),
		"other agent": view(t, h, "other", "/work/a.go", nil, "package a"),
		"other path":  view(t, h, "", "/work/b.go", nil, "package a"),
	} {
		if got != "package a" {
			t.Errorf("%s: view = %q, want the full result", name, got)
		}
	}
	if got := view(t, h, "child", "/work/a.go", nil, "package a"); got != readOnceNote {
		t.Errorf("subagent repeat = %q", got)
	}
	// Another tool is never replaced.
	out, err := h.fc.create[0].Hooks.OnPostToolUse(copilot.PostToolUseHookInput{ToolName: "bash", ToolArgs: map[string]any{"path": "/work/a.go"}, ToolResult: map[string]any{"textResultForLlm": "package a"}}, copilot.HookInvocation{})
	if err != nil || out != nil {
		t.Errorf("bash hook = %+v, %v", out, err)
	}
	// A subagent's end forgets only its own views.
	h.fs.onEvent(agentEv("started", "child", &rpc.SubagentStartedData{ToolCallID: "tool", AgentName: "explore"}))
	h.fs.onEvent(agentEv("done", "child", &rpc.SubagentCompletedData{ToolCallID: "tool"}))
	if got := view(t, h, "child", "/work/a.go", nil, "package a"); got != "package a" {
		t.Errorf("view after the subagent ended = %q", got)
	}
	if got := view(t, h, "", "/work/a.go", nil, "package a"); got != readOnceNote {
		t.Errorf("main agent repeat after a subagent ended = %q", got)
	}
}

func TestReadOnceResetsOnCompaction(t *testing.T) {
	h := openWeb(t)
	threshold := rpc.CompactionTriggerThreshold
	view(t, h, "", "/work/a.go", nil, "package a")
	view(t, h, "child", "/work/a.go", nil, "package a")
	h.fs.onEvent(phaseEv("k1", "", 101, &rpc.SessionCompactionStartData{Trigger: &threshold}))
	// Nothing is replaced while it runs.
	if got := view(t, h, "", "/work/a.go", nil, "package a"); got != "package a" {
		t.Fatalf("view during compaction = %q", got)
	}
	h.fs.onEvent(phaseEv("k2", "", 102, &rpc.SessionCompactionCompleteData{Success: true, Trigger: &threshold}))
	if got := view(t, h, "", "/work/a.go", nil, "package a"); got != "package a" {
		t.Fatalf("view after compaction = %q", got)
	}
	if got := view(t, h, "", "/work/a.go", nil, "package a"); got != readOnceNote {
		t.Fatalf("repeat after compaction = %q", got)
	}
	// The main agent's compaction leaves a subagent's context alone.
	if got := view(t, h, "child", "/work/a.go", nil, "package a"); got != readOnceNote {
		t.Fatalf("subagent repeat after the main compaction = %q", got)
	}
}
