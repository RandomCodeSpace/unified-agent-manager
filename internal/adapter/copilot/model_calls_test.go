package copilot

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	copilot "github.com/microsoft/copilot-sdk/go/v2"
	"github.com/microsoft/copilot-sdk/go/v2/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func modelCalls(sink *recSink) []agentapi.ModelCall {
	var out []agentapi.ModelCall
	for _, e := range sink.all() {
		if e.Kind == agentapi.EventModelCall && e.ModelCall != nil {
			out = append(out, *e.ModelCall)
		}
	}
	return out
}

// A finished model call is reported with the model its start named; a
// subagent's call with the same turn ID is kept apart from the main agent's.
func TestModelCallEventsAreMapped(t *testing.T) {
	h := openWeb(t)
	c := h.conv.(*conversation)
	h.p.SetCustomModels(subagentCustom)
	model := func(id string) *string { return &id }
	sub := func(e copilot.SessionEvent) copilot.SessionEvent { id := "agent-1"; e.AgentID = &id; return e }
	c.onEvent(ev("1", &rpc.ModelCallStartData{Model: model("gpt-6-luna"), TurnID: "t1"}))
	c.onEvent(sub(ev("2", &rpc.ModelCallStartData{Model: model("deepseek-v4.1-flash"), TurnID: "t1"})))
	if got := modelCalls(h.sink); len(got) != 0 {
		t.Fatalf("reported before the call ended: %+v", got)
	}
	c.onEvent(sub(ev("3", &rpc.ModelCallFinishedData{TurnID: "t1", Outcome: rpc.ModelCallFinishedOutcomeRejected, DispatchDurationMs: 250})))
	c.onEvent(ev("4", &rpc.ModelCallFinishedData{TurnID: "t1", Outcome: rpc.ModelCallFinishedOutcomeError, DispatchDurationMs: 1234.6}))
	// An end with no start, a repeated end and a start that names no model
	// report nothing.
	c.onEvent(ev("5", &rpc.ModelCallFinishedData{TurnID: "t1", Outcome: rpc.ModelCallFinishedOutcomeSuccess}))
	c.onEvent(ev("6", &rpc.ModelCallStartData{TurnID: "t2"}))
	c.onEvent(ev("7", &rpc.ModelCallFinishedData{TurnID: "t2", Outcome: rpc.ModelCallFinishedOutcomeSuccess}))
	// A custom model is reported by its selection ID; a negative duration as none.
	c.onEvent(ev("8", &rpc.ModelCallStartData{Model: model("m"), TurnID: "t3"}))
	c.onEvent(ev("9", &rpc.ModelCallFinishedData{TurnID: "t3", Outcome: rpc.ModelCallFinishedOutcomeSuccess, DispatchDurationMs: -1}))
	c.onEvent(ev("10", &rpc.ModelCallStartData{Model: model("gpt-6-luna"), TurnID: "t4"}))
	c.onEvent(ev("11", &rpc.ModelCallFinishedData{TurnID: "t4", Outcome: rpc.ModelCallFinishedOutcomeCancelled, DispatchDurationMs: 5}))
	want := []agentapi.ModelCall{
		{Model: "deepseek-v4.1-flash", Outcome: "rejected", DurationMS: 250},
		{Model: "gpt-6-luna", Outcome: "error", DurationMS: 1234},
		{Model: subagentCustom[0].SelectionID(), Outcome: "success"},
		{Model: "gpt-6-luna", Outcome: "cancelled", DurationMS: 5},
	}
	if got := modelCalls(h.sink); !reflect.DeepEqual(got, want) {
		t.Fatalf("model calls = %+v, want %+v", got, want)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.callModels) != 0 {
		t.Fatalf("ended calls kept: %v", c.callModels)
	}
}

// Calls whose end never arrives do not grow without bound.
func TestModelCallStartsAreBounded(t *testing.T) {
	h := openWeb(t)
	c := h.conv.(*conversation)
	m := "gpt-6-luna"
	for i := range maxCallModels * 2 {
		c.onEvent(ev(fmt.Sprint("s", i), &rpc.ModelCallStartData{Model: &m, TurnID: fmt.Sprint("t", i)}))
	}
	c.mu.Lock()
	n := len(c.callModels)
	c.mu.Unlock()
	if n == 0 || n > maxCallModels {
		t.Fatalf("kept %d starts, bound %d", n, maxCallModels)
	}
}

// The host may move a subagent to another allowed model; an answer outside
// the allowed models is ignored, and the agent is told why the model changed.
func TestPreToolUseTakesTheHostsHealthyModel(t *testing.T) {
	t.Setenv("UAM_TEST_SUBAGENT_UNSET_KEY", "")
	h := openWeb(t)
	c := h.conv.(*conversation)
	h.p.SetCustomModels(subagentCustom)
	allowed := []string{"gpt-6-luna", "nokey/m", "deepseek-v4.1-flash"}
	h.p.SetSubagentModels(allowed)
	for _, tc := range []struct {
		name, requested, answer, want string
	}{
		{"moved off the requested model", "gpt-6-luna", "deepseek-v4.1-flash", "deepseek-v4.1-flash"},
		{"moved off the fallback", "other", "deepseek-v4.1-flash", "deepseek-v4.1-flash"},
		{"kept", "gpt-6-luna", "gpt-6-luna", ""},
		{"no answer", "gpt-6-luna", "", ""},
		{"outside the allowlist", "gpt-6-luna", "other", ""},
		{"allowed but not usable", "gpt-6-luna", "nokey/m", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var picked string
			var usable []string
			c.hooks.SubagentModel = func(p string, u []string) string {
				picked, usable = p, slices.Clone(u)
				return tc.answer
			}
			args := map[string]any{"prompt": "look", "model": tc.requested}
			out, err := c.preToolUse(taskCall(args), copilot.HookInvocation{})
			if err != nil {
				t.Fatal(err)
			}
			if picked != "gpt-6-luna" || !slices.Equal(usable, []string{"gpt-6-luna", "deepseek-v4.1-flash"}) {
				t.Fatalf("host asked about %q among %v", picked, usable)
			}
			if tc.want == "" && tc.requested == "gpt-6-luna" {
				if out != nil {
					t.Fatalf("output = %+v, want the call left alone", out)
				}
				return
			}
			got, _ := out.ModifiedArgs.(map[string]any)
			if !maps.Equal(got, map[string]any{"prompt": "look", "model": tc.want}) || out.PermissionDecision != "" {
				t.Fatalf("output = %+v, want model %s", out, tc.want)
			}
			for _, part := range []string{"gpt-6-luna's recent model calls failed", "do not start it again", strings.Join(allowed, ", ")} {
				if !strings.Contains(out.AdditionalContext, part) {
					t.Fatalf("context %q lacks %q", out.AdditionalContext, part)
				}
			}
		})
	}
}

// Without a subagent model list nothing is limited, so nothing is moved.
func TestPreToolUseAsksNoHealthWithoutAnAllowlist(t *testing.T) {
	h := openWeb(t)
	c := h.conv.(*conversation)
	c.hooks.SubagentModel = func(string, []string) string { t.Error("host asked"); return "x" }
	if out, err := c.preToolUse(taskCall(map[string]any{"model": "any"}), copilot.HookInvocation{}); out != nil || err != nil {
		t.Fatalf("preToolUse = %+v, %v", out, err)
	}
}

// Calls that overlap in one agent's turn, as a Fusion turn's do, cannot be
// told apart at their ends, so a failure is never pinned on the wrong model.
func TestOverlappingModelCallsAreNotMisattributed(t *testing.T) {
	h := openWeb(t)
	c := h.conv.(*conversation)
	a, b := "gpt-6-luna", "deepseek-v4.1-flash"
	c.onEvent(ev("1", &rpc.ModelCallStartData{Model: &a, TurnID: "t1"}))
	c.onEvent(ev("2", &rpc.ModelCallStartData{Model: &b, TurnID: "t1"}))
	c.onEvent(ev("3", &rpc.ModelCallFinishedData{TurnID: "t1", Outcome: rpc.ModelCallFinishedOutcomeError}))
	c.onEvent(ev("4", &rpc.ModelCallFinishedData{TurnID: "t1", Outcome: rpc.ModelCallFinishedOutcomeSuccess}))
	// Overlapping calls on one model still say which model ended.
	c.onEvent(ev("5", &rpc.ModelCallStartData{Model: &a, TurnID: "t2"}))
	c.onEvent(ev("6", &rpc.ModelCallStartData{Model: &a, TurnID: "t2"}))
	c.onEvent(ev("7", &rpc.ModelCallFinishedData{TurnID: "t2", Outcome: rpc.ModelCallFinishedOutcomeError}))
	c.onEvent(ev("8", &rpc.ModelCallFinishedData{TurnID: "t2", Outcome: rpc.ModelCallFinishedOutcomeError}))
	// Once the overlap has ended, the turn's next call is reported again.
	c.onEvent(ev("9", &rpc.ModelCallStartData{Model: &b, TurnID: "t1"}))
	c.onEvent(ev("10", &rpc.ModelCallFinishedData{TurnID: "t1", Outcome: rpc.ModelCallFinishedOutcomeRejected}))
	want := []agentapi.ModelCall{{Model: a, Outcome: "error"}, {Model: a, Outcome: "error"}, {Model: b, Outcome: "rejected"}}
	if got := modelCalls(h.sink); !reflect.DeepEqual(got, want) {
		t.Fatalf("model calls = %+v, want %+v", got, want)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.callModels) != 0 {
		t.Fatalf("ended calls kept: %v", c.callModels)
	}
}
