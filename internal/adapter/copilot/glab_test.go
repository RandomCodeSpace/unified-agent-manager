package copilot

import (
	"context"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	copilot "github.com/github/copilot-sdk/go"
)

func TestGlabModifiedArgsMapping(t *testing.T) {
	h := openWeb(t)
	c := h.conv.(*conversation)
	args := map[string]any{"command": "glab issue list -P 10", "description": "List issues"}
	c.hooks.Pre = func(context.Context, agentapi.ToolUse) agentapi.ToolVerdict { return agentapi.ToolVerdict{Args: args} }
	out, err := h.fc.create[0].Hooks.OnPreToolUse(copilot.PreToolUseHookInput{ToolName: "bash", ToolArgs: map[string]any{"command": "glab issue list"}}, copilot.HookInvocation{})
	if err != nil || out == nil {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	got, ok := out.ModifiedArgs.(map[string]any)
	if !ok || got["command"] != args["command"] || got["description"] != args["description"] || out.PermissionDecision != "" {
		t.Fatalf("args=%+v", out)
	}
}
