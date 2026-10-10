package copilot

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

func TestFuseHookMappings(t *testing.T) {
	h := openWeb(t)
	c := h.conv.(*conversation)
	c.hooks.Pre = func(context.Context, agentapi.ToolUse) agentapi.ToolVerdict {
		return agentapi.ToolVerdict{Context: "warning", Deny: "refused 403; reopens 20:02"}
	}
	out, err := h.fc.create[0].Hooks.OnPreToolUse(copilot.PreToolUseHookInput{ToolName: "web_fetch"}, copilot.HookInvocation{})
	if err != nil || out.PermissionDecision != "deny" || out.PermissionDecisionReason != "refused 403; reopens 20:02" || out.AdditionalContext != "warning" {
		t.Fatalf("pre=%+v error=%v", out, err)
	}
	c.hooks.Failed = func(_ context.Context, use agentapi.ToolUse, failure string) string {
		if use.Tool != "web_fetch" || use.Args["url"] != "https://example.com/a" || use.Workdir != "/repo" || failure != "status code 403" {
			t.Errorf("use=%+v failure=%q", use, failure)
		}
		return "failed context"
	}
	failed, err := h.fc.create[0].Hooks.OnPostToolUseFailure(copilot.PostToolUseFailureHookInput{ToolName: "web_fetch", ToolArgs: map[string]any{"url": "https://example.com/a"}, WorkingDirectory: "/repo", Error: "status code 403"}, copilot.HookInvocation{})
	if err != nil || failed == nil || failed.AdditionalContext != "failed context" {
		t.Fatalf("failed=%+v error=%v", failed, err)
	}
}

func TestFuseAutomaticPermissionFallbackExcludesOwnerAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name                             string
		result                           rpc.PermissionResult
		human, answering, resolved, want bool
	}{
		{name: "no user", result: &rpc.PermissionDeniedNoApprovalRuleAndCouldNotRequestFromUser{}, want: true},
		{name: "rule", result: &rpc.PermissionDeniedByRules{}, want: true},
		{name: "policy", result: &rpc.PermissionDeniedByContentExclusionPolicy{}, want: true},
		{name: "owner", result: &rpc.PermissionDeniedInteractivelyByUser{}},
		{name: "owner attributed", result: &rpc.PermissionDeniedByRules{}, human: true},
		{name: "answer in flight", result: &rpc.PermissionDeniedByRules{}, answering: true},
		{name: "hook refusal", result: &rpc.PermissionDeniedByPermissionRequestHook{}, resolved: true, want: true},
		{name: "cancel", result: &rpc.PermissionCancelled{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := openWeb(t)
			c := h.conv.(*conversation)
			called := false
			c.hooks.Failed = func(_ context.Context, use agentapi.ToolUse, failure string) string {
				called = true
				if !c.mu.TryLock() {
					t.Fatal("host callback held adapter mutex")
				}
				c.mu.Unlock()
				if use.Tool != "bash" || use.PermissionKind != "shell" || !strings.HasPrefix(use.Args["command"].(string), "ls /etc/ssl") || failure != string(tc.result.Kind()) {
					t.Errorf("fallback=%+v %q", use, failure)
				}
				return "deferred to next Pre"
			}
			h.fs.onEvent(ev("start", &rpc.ToolExecutionStartData{ToolCallID: "call", ToolName: "bash", Arguments: map[string]any{"command": "ls /etc/ssl"}}))
			h.fs.onEvent(ev("ask", &rpc.PermissionRequestedData{RequestID: "request", ResolvedByHook: copilot.Bool(tc.resolved), PermissionRequest: &rpc.PermissionRequestShell{ToolCallID: copilot.String("call"), FullCommandText: "ls /etc/ssl"}, PromptRequest: &rpc.PermissionPromptRequestCommands{FullCommandText: "ls /etc/ssl"}}))
			c.mu.Lock()
			c.pending["request"].answering = tc.answering
			c.mu.Unlock()
			result := &rpc.PermissionCompletedData{RequestID: "request", Result: tc.result}
			if tc.human {
				source := rpc.PermissionDecisionSourceHumanResponse
				result.DecisionSource = &source
			}
			h.fs.onEvent(ev("done", result))
			if tc.resolved {
				for _, event := range h.sink.all() {
					if event.Kind == agentapi.EventInteraction {
						t.Fatal("automatic hook request surfaced to owner")
					}
				}
			}
			if called != tc.want {
				t.Fatalf("callback=%v want=%v", called, tc.want)
			}
		})
	}
}

func TestFusePermissionKindUsesVerifiedCatalog(t *testing.T) {
	mcp := []rpc.CurrentToolMetadata{}
	for _, name := range []string{"forge-read_issue", "other-get_file", "third-read_config"} {
		mcp = append(mcp, rpc.CurrentToolMetadata{Name: name, MCPServerName: copilot.String("forge"), MCPToolName: copilot.String(name)})
	}
	after := append(append([]rpc.CurrentToolMetadata{}, mcp...), rpc.CurrentToolMetadata{Name: declarationToolName})
	d := newDeclarationTool(func(context.Context, string) (string, error) { return "/tmp/file", nil })
	defer d.stop()
	fs := &fakeSession{id: "session", toolCatalogs: []fakeToolCatalog{{tools: mcp}, {tools: after}}}
	if err := d.catalog(t.Context(), fs, d.tool()); err != nil {
		t.Fatal(err)
	}
	h := openWeb(t)
	c := h.conv.(*conversation)
	c.tools = d.toolGate
	var seen agentapi.ToolUse
	c.hooks.Pre = func(_ context.Context, use agentapi.ToolUse) agentapi.ToolVerdict {
		seen = use
		return agentapi.ToolVerdict{}
	}
	for _, name := range []string{"forge-read_issue", "other-get_file", "third-read_config", "unproven-tool"} {
		args := map[string]any{"query": "fixture"}
		if _, err := c.preToolUse(copilot.PreToolUseHookInput{ToolName: name, ToolArgs: args}, copilot.HookInvocation{}); err != nil {
			t.Fatal(err)
		}
		want := "mcp"
		if name == "unproven-tool" {
			want = ""
		}
		if seen.PermissionKind != want || seen.Tool != name || seen.Args["query"] != "fixture" {
			t.Fatalf("use=%+v", seen)
		}
	}
	d.observe(ev("changed", &rpc.MCPToolsListChangedData{}))
	if got := d.permissionKind("forge-read_issue"); got != "" {
		t.Fatalf("stale metadata: %q", got)
	}
}

// A close between the recorded resolvedByHook request and its completion
// must neither expose it nor send a redundant permission response.
func TestFuseHiddenPermissionLifecycle(t *testing.T) {
	h := openWeb(t)
	c := h.conv.(*conversation)
	h.fs.onEvent(ev("hidden", &rpc.PermissionRequestedData{RequestID: "hidden", ResolvedByHook: copilot.Bool(true), PermissionRequest: &rpc.PermissionRequestShell{FullCommandText: "ls /etc/ssl"}}))
	if err := c.Respond(t.Context(), "hidden", agentapi.Answer{Decision: "reject"}); !errors.Is(err, agentapi.ErrInteractionGone) {
		t.Fatalf("hidden respond=%v", err)
	}
	if err := c.Send(t.Context(), agentapi.Prompt{Text: "continue"}); err != nil {
		t.Fatalf("hidden request blocked prompt: %v", err)
	}
	if err := c.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(c.pending) != 0 {
		t.Fatal("hidden request retained after close")
	}
	h.fs.mu.Lock()
	answers := len(h.fs.answers)
	h.fs.mu.Unlock()
	if answers != 0 {
		t.Fatalf("close responded to %d already-resolved permissions", answers)
	}
	for _, event := range h.sink.all() {
		if event.Kind == agentapi.EventInteraction {
			t.Fatal("hidden request surfaced during prompt or close")
		}
	}
}
