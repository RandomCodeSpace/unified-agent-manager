package copilot

import (
	"context"
	"maps"
	"strings"
	"testing"

	copilot "github.com/github/copilot-sdk/go"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// subagentCustom has a custom model whose key variable is unset and one
// with a saved key.
var subagentCustom = []agentapi.CustomModel{
	{Name: "nokey", BaseURL: "https://llm.example/v1", ModelID: "m", APIKeyEnv: "UAM_TEST_SUBAGENT_UNSET_KEY"},
	{Name: "keyed", BaseURL: "https://llm.example/v1", ModelID: "m", APIKey: "fixture-key"},
}

func TestSubagentModelChoice(t *testing.T) {
	t.Setenv("UAM_TEST_SUBAGENT_UNSET_KEY", "")
	p := &webProvider{}
	p.SetCustomModels(subagentCustom)
	for _, tc := range []struct {
		name, requested, task string
		allowed               []string
		want                  string
	}{
		{"requested allowed", "b", "a", []string{"a", "b"}, "b"},
		{"requested not allowed uses task", "c", "a", []string{"b", "a"}, "a"},
		{"task not allowed uses first", "c", "d", []string{"b", "a"}, "b"},
		{"none requested uses task", "", "a", []string{"b", "a"}, "a"},
		{"nothing requested or selected uses first", "", "", []string{"b", "a"}, "b"},
		{"requested custom without key skipped", "nokey/m", "keyed/m", []string{"nokey/m", "keyed/m"}, "keyed/m"},
		{"task custom without key skipped", "c", "nokey/m", []string{"nokey/m", "b"}, "b"},
		{"first custom without key skipped", "", "", []string{"nokey/m", "keyed/m"}, "keyed/m"},
		{"none usable", "nokey/m", "a", []string{"nokey/m"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := p.subagentModel(tc.allowed, tc.requested, tc.task); got != tc.want {
				t.Fatalf("subagentModel(%v, %q, %q) = %q, want %q", tc.allowed, tc.requested, tc.task, got, tc.want)
			}
		})
	}
}

func TestSubagentModelsAreCopied(t *testing.T) {
	p := &webProvider{}
	in := []string{"a", "b"}
	p.SetSubagentModels(in)
	in[0] = "x"
	got := p.subagentModels()
	got[1] = "y"
	if again := p.subagentModels(); again[0] != "a" || again[1] != "b" {
		t.Fatalf("subagent models = %v", again)
	}
	p.SetSubagentModels(nil)
	if got := p.subagentModels(); len(got) != 0 {
		t.Fatalf("lifted limit = %v", got)
	}
}

func taskCall(args any) copilot.PreToolUseHookInput {
	return copilot.PreToolUseHookInput{ToolName: "task", ToolArgs: args}
}

func TestPreToolUseLeavesOtherCallsAlone(t *testing.T) {
	h := openWeb(t)
	c := h.conv.(*conversation)
	args := map[string]any{"model": "not-allowed"}
	if out, err := c.preToolUse(taskCall(args), copilot.HookInvocation{}); out != nil || err != nil {
		t.Fatalf("no allowlist: %+v %v", out, err)
	}
	h.p.SetSubagentModels([]string{"gpt-6-luna"})
	if out, err := c.preToolUse(copilot.PreToolUseHookInput{ToolName: "bash", ToolArgs: args}, copilot.HookInvocation{}); out != nil || err != nil {
		t.Fatalf("other tool: %+v %v", out, err)
	}
	if out, err := c.preToolUse(taskCall(map[string]any{"model": "gpt-6-luna"}), copilot.HookInvocation{}); out != nil || err != nil {
		t.Fatalf("allowed request: %+v %v", out, err)
	}
}

// A task call on a model outside the allowlist leaves on the Task's model
// when allowed, else the first allowed; the other arguments are kept and the
// SDK's arguments are not changed in place.
func TestPreToolUseRewritesModel(t *testing.T) {
	h := openWeb(t)
	c := h.conv.(*conversation)
	h.p.SetSubagentModels([]string{"gpt-6-luna", "deepseek-v4.1-flash"})
	for _, tc := range []struct {
		name, selected, turn string
		args                 any
		want                 map[string]any
	}{
		{"first allowed", "", "", map[string]any{"agent_type": "explore", "prompt": "look", "model": "other"}, map[string]any{"agent_type": "explore", "prompt": "look", "model": "gpt-6-luna"}},
		{"selected model", "deepseek-v4.1-flash", "gpt-6-luna", map[string]any{"prompt": "look", "model": "other"}, map[string]any{"prompt": "look", "model": "deepseek-v4.1-flash"}},
		{"turn model", "", "deepseek-v4.1-flash", map[string]any{"prompt": "look"}, map[string]any{"prompt": "look", "model": "deepseek-v4.1-flash"}},
		{"missing model", "", "", map[string]any{"prompt": "look"}, map[string]any{"prompt": "look", "model": "gpt-6-luna"}},
		{"no arguments", "", "", nil, map[string]any{"model": "gpt-6-luna"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c.mu.Lock()
			c.selected, c.turnModel = tc.selected, tc.turn
			c.mu.Unlock()
			var before map[string]any
			if m, ok := tc.args.(map[string]any); ok {
				before = maps.Clone(m)
			}
			out, err := c.preToolUse(taskCall(tc.args), copilot.HookInvocation{})
			if err != nil || out == nil {
				t.Fatalf("preToolUse = %+v, %v", out, err)
			}
			got, ok := out.ModifiedArgs.(map[string]any)
			if !ok || !maps.Equal(got, tc.want) || out.PermissionDecision != "" || !strings.Contains(out.AdditionalContext, "do not start it again") {
				t.Fatalf("output = %+v, want args %v", out, tc.want)
			}
			if m, ok := tc.args.(map[string]any); ok && !maps.Equal(m, before) {
				t.Fatalf("input args mutated: %v, was %v", m, before)
			}
		})
	}
}

func TestPreToolUseDeniesWhenNoAllowedModelIsUsable(t *testing.T) {
	t.Setenv("UAM_TEST_SUBAGENT_UNSET_KEY", "")
	h := openWeb(t)
	c := h.conv.(*conversation)
	h.p.SetCustomModels(subagentCustom)
	h.p.SetSubagentModels([]string{"nokey/m"})
	out, err := c.preToolUse(taskCall(map[string]any{"model": "nokey/m"}), copilot.HookInvocation{})
	if err != nil || out == nil || out.PermissionDecision != "deny" || out.PermissionDecisionReason == "" || out.ModifiedArgs != nil {
		t.Fatalf("preToolUse = %+v, %v", out, err)
	}
}

// Created and resumed sessions both carry the hook, bound to their
// conversation.
func TestWebPreToolUseHookOnCreateAndResume(t *testing.T) {
	h := openWeb(t)
	if _, err := h.p.Open(context.Background(), agentapi.OpenRequest{SessionID: "s-2", ConversationID: "s-1", Workdir: "/work", Events: &recSink{}}); err != nil {
		t.Fatal(err)
	}
	h.p.SetSubagentModels([]string{"gpt-6-luna"})
	for name, cfg := range map[string]*copilot.SessionHooks{"create": h.fc.create[0].Hooks, "resume": h.fc.resume[0].Hooks} {
		if cfg == nil || cfg.OnPreToolUse == nil {
			t.Fatalf("%s hooks = %+v", name, cfg)
		}
		out, err := cfg.OnPreToolUse(taskCall(map[string]any{"model": "other"}), copilot.HookInvocation{})
		if err != nil || out == nil {
			t.Fatalf("%s hook = %+v, %v", name, out, err)
		}
		if got, _ := out.ModifiedArgs.(map[string]any); got["model"] != "gpt-6-luna" {
			t.Fatalf("%s hook = %+v, %v", name, out, err)
		}
	}
	if !h.p.Capabilities().SubagentModels {
		t.Fatal("capability SubagentModels not set")
	}
}
