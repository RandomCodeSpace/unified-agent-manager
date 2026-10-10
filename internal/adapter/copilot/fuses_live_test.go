package copilot

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

// TestFusesLiveProbe uses isolated temporary sessions; no owner Tasks are touched.
func TestFusesLiveProbe(t *testing.T) {
	if os.Getenv("UAM_FUSES_LIVE_PROBE") != "1" {
		t.Skip("set UAM_FUSES_LIVE_PROBE=1 for CLI fuse probes")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	client, err := newSDKClient()
	if err != nil {
		t.Fatal(err)
	}
	defer client.ForceStop()
	if err = client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	c := client.(sdkClientAdapter).c
	for _, kind := range []string{"pre-deny", "safe-denial", "subagent-fetch", "fetch-429"} {
		t.Run(kind, func(t *testing.T) {
			var mu sync.Mutex
			var pre, failures []string
			var permissionResults []string
			marker := "fuse-deny-indigo-943"
			config := &copilot.SessionConfig{Model: "gpt-6-luna", WorkingDirectory: t.TempDir(), EnableConfigDiscovery: copilot.Bool(false), EnableSkills: copilot.Bool(false), EnableSessionStore: copilot.Bool(false), EnableFileHooks: copilot.Bool(false), OnPermissionRequest: copilot.PermissionHandler.ApproveAll,
				Hooks: &copilot.SessionHooks{
					OnPreToolUse: func(in copilot.PreToolUseHookInput, _ copilot.HookInvocation) (*copilot.PreToolUseHookOutput, error) {
						mu.Lock()
						pre = append(pre, in.ToolName)
						mu.Unlock()
						if kind == "pre-deny" && in.ToolName == "web_fetch" {
							return &copilot.PreToolUseHookOutput{PermissionDecision: "deny", PermissionDecisionReason: marker}, nil
						}
						return nil, nil
					},
					OnPostToolUseFailure: func(in copilot.PostToolUseFailureHookInput, _ copilot.HookInvocation) (*copilot.PostToolUseFailureHookOutput, error) {
						mu.Lock()
						failures = append(failures, in.ToolName+": "+in.Error)
						mu.Unlock()
						return &copilot.PostToolUseFailureHookOutput{AdditionalContext: "Include the probe marker fuse-failed-indigo-943 in your final reply."}, nil
					},
				},
				OnEvent: func(e copilot.SessionEvent) {
					if d, ok := e.Data.(*rpc.PermissionCompletedData); ok && d.Result != nil {
						mu.Lock()
						permissionResults = append(permissionResults, string(d.Result.Kind()))
						mu.Unlock()
					}
				},
			}
			prompt := "Use web_fetch exactly once on https://example.com/. Do not retry or use other tools. Report the exact refusal reason, including any probe marker."
			switch kind {
			case "pre-deny":
				config.AvailableTools = []string{"web_fetch"}
			case "safe-denial":
				config.AvailableTools = []string{"bash"}
				config.OnPermissionRequest = func(copilot.PermissionRequest, copilot.PermissionInvocation) (rpc.PermissionDecision, error) {
					return &rpc.PermissionDecisionUserNotAvailable{}, nil
				}
				prompt = "Use bash exactly once to run ls /etc/ssl. Do not retry or use other tools. Report the denial and any hook probe marker."
			case "subagent-fetch":
				config.AvailableTools = []string{"task", "web_fetch"}
				config.CustomAgents = []copilot.CustomAgentConfig{{Name: "fetch-probe", Description: "Isolated web fetch probe", Model: "gpt-6-luna", Tools: []string{"web_fetch"}, Prompt: "Fetch the requested URL exactly once and report the result and any hook probe marker. Never retry."}}
				prompt = "Use task to call fetch-probe on model gpt-6-luna to web_fetch https://httpbin.org/status/403 exactly once. Do not fetch yourself. Report its result and any hook probe marker."
			case "fetch-429":
				config.AvailableTools = []string{"web_fetch"}
				prompt = "Use web_fetch exactly once on https://httpbin.org/status/429. Do not retry or use other tools. Report the result and any hook probe marker."
			}
			s, err := c.CreateSession(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Disconnect(); _ = c.DeleteSession(context.Background(), s.SessionID) }()
			reply, err := s.SendAndWait(ctx, copilot.MessageOptions{Prompt: prompt})
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			t.Logf("pre=%v failures=%q permission_results=%v", pre, failures, permissionResults)
			content := ""
			if reply != nil {
				if msg, ok := reply.Data.(*rpc.AssistantMessageData); ok {
					content = msg.Content
				}
			}
			t.Logf("reply=%s", content)
			if kind == "pre-deny" && !strings.Contains(content, marker) {
				t.Fatal("deny reason missing from model reply")
			}
			if kind == "safe-denial" && len(permissionResults) == 0 {
				t.Fatal("no native permission result")
			}
			if kind == "subagent-fetch" && (!containsProbeTool(pre, "task") || !containsProbeTool(pre, "web_fetch")) {
				t.Fatal("subagent fetch hook not observed")
			}
		})
	}
}
func containsProbeTool(tools []string, name string) bool {
	for _, tool := range tools {
		if tool == name {
			return true
		}
	}
	return false
}
