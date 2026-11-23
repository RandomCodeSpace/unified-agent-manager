package copilot

import (
	"context"
	"maps"
	"testing"

	copilot "github.com/microsoft/copilot-sdk/go/v2"
	"github.com/microsoft/copilot-sdk/go/v2/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
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
	out, err := h.fc.create[0].Hooks.Handlers.OnPostToolUse(copilot.PostToolUseHookInput{AgentID: agent, ToolName: "view", ToolArgs: args, ToolResult: result}, copilot.HookInvocation{})
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

// A lost prompt cache prefix is counted by its primary reason, a subagent's
// too, and reported once with the turn that ends next.
func TestCacheBreaksAreReportedWithTheTurn(t *testing.T) {
	h := openWeb(t)
	_ = h.conv.Send(context.Background(), agentapi.Prompt{Text: "hi"})
	h.fs.onEvent(ev("b1", &rpc.PromptCacheBreakData{PrimaryReason: "tools_changed", ContributingReasons: []string{"tools_changed", "ttl_expired"}, FrontierTokens: 1000}))
	h.fs.onEvent(agentEv("b2", "sub-1", &rpc.PromptCacheBreakData{PrimaryReason: "tools_changed", FrontierTokens: 200}))
	h.fs.onEvent(ev("b3", &rpc.PromptCacheBreakData{PrimaryReason: "ttl_expired", FrontierTokens: 30, CacheConfigChangedFields: []string{"ttl"}}))
	h.fs.onEvent(ev("b4", &rpc.PromptCacheBreakData{FrontierTokens: -5}))
	lastTurn := func() agentapi.Turn {
		t.Helper()
		evs := h.sink.all()
		for i := len(evs) - 1; i >= 0; i-- {
			if evs[i].Kind == agentapi.EventTurn {
				return *evs[i].Turn
			}
		}
		t.Fatal("no turn")
		return agentapi.Turn{}
	}
	if turn := lastTurn(); turn.State != agentapi.TurnWorking || turn.CacheBreaks != nil || turn.CacheBreakTokens != 0 {
		t.Fatalf("running turn = %+v, want no cache breaks yet", turn)
	}
	endTurn(t, h, 10)
	turn := lastTurn()
	if want := map[string]int{"tools_changed": 2, "ttl_expired": 1, "unknown": 1}; !maps.Equal(turn.CacheBreaks, want) || turn.CacheBreakTokens != 1230 {
		t.Fatalf("turn cache breaks = %v, %d tokens; want %v, 1230", turn.CacheBreaks, turn.CacheBreakTokens, want)
	}
	_ = h.conv.Send(context.Background(), agentapi.Prompt{Text: "again"})
	endTurn(t, h, 20)
	if turn := lastTurn(); turn.CacheBreaks != nil || turn.CacheBreakTokens != 0 {
		t.Fatalf("next turn repeated the cache breaks: %v, %d", turn.CacheBreaks, turn.CacheBreakTokens)
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
	out, err := h.fc.create[0].Hooks.Handlers.OnPostToolUse(copilot.PostToolUseHookInput{ToolName: "bash", ToolArgs: map[string]any{"path": "/work/a.go"}, ToolResult: map[string]any{"textResultForLlm": "package a"}}, copilot.HookInvocation{})
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

// A truncation, a cleared context and a rewind each drop earlier messages
// from the agent's context, so its next view passes in full.
func TestReadOnceResetsWhenTheConversationLosesMessages(t *testing.T) {
	for name, lost := range map[string]rpc.SessionEventData{
		"truncation":      &rpc.SessionTruncationData{PerformedBy: "BasicTruncator", MessagesRemovedDuringTruncation: 40, TokensRemovedDuringTruncation: 90000},
		"context cleared": &rpc.SessionContextClearedData{MessagesCleared: 40},
		"rewind":          &rpc.SessionSnapshotRewindData{UpToEventID: "e1", EventsRemoved: 40},
	} {
		t.Run(name, func(t *testing.T) {
			h := openWeb(t)
			view(t, h, "", "/work/a.go", nil, "package a")
			view(t, h, "child", "/work/a.go", nil, "package a")
			h.fs.onEvent(ev("lost-1", lost))
			if got := view(t, h, "", "/work/a.go", nil, "package a"); got != "package a" {
				t.Fatalf("view after the loss = %q", got)
			}
			if got := view(t, h, "", "/work/a.go", nil, "package a"); got != readOnceNote {
				t.Fatalf("repeat after the loss = %q", got)
			}
			// The main agent's loss leaves a subagent's context alone.
			if got := view(t, h, "child", "/work/a.go", nil, "package a"); got != readOnceNote {
				t.Fatalf("subagent repeat after the main agent's loss = %q", got)
			}
		})
	}
}
