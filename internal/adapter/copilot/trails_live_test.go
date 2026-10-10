package copilot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

// TestTrailsLiveProbe is opt-in: two isolated CLI sessions edit only temp files.
func TestTrailsLiveProbe(t *testing.T) {
	if os.Getenv("UAM_TRAILS_LIVE_PROBE") != "1" {
		t.Skip("set UAM_TRAILS_LIVE_PROBE=1 for CLI trail probes")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	client, err := newSDKClient()
	if err != nil {
		t.Fatal(err)
	}
	defer client.ForceStop()
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	c := client.(sdkClientAdapter).c
	for _, tool := range []string{"edit", "apply_patch"} {
		t.Run(tool, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "probe.txt"), []byte("before\n"), 0600); err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			seen := false
			marker := "trail-context-indigo-742"
			session, err := c.CreateSession(ctx, &copilot.SessionConfig{
				Model: "gpt-6-luna", WorkingDirectory: dir,
				EnableConfigDiscovery: copilot.Bool(false), EnableSkills: copilot.Bool(false), EnableSessionStore: copilot.Bool(false), EnableFileHooks: copilot.Bool(false),
				AvailableTools: []string{tool}, OnPermissionRequest: copilot.PermissionHandler.ApproveAll,
				Hooks: &copilot.SessionHooks{OnPreToolUse: func(in copilot.PreToolUseHookInput, _ copilot.HookInvocation) (*copilot.PreToolUseHookOutput, error) {
					if in.ToolName != tool {
						return nil, nil
					}
					mu.Lock()
					seen = true
					mu.Unlock()
					t.Logf("tool=%s args_type=%T args=%#v", tool, in.ToolArgs, in.ToolArgs)
					return &copilot.PreToolUseHookOutput{AdditionalContext: "After this edit, include this exact probe marker in your final reply: " + marker}, nil
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = session.Disconnect(); _ = c.DeleteSession(context.Background(), session.SessionID) }()
			reply, err := session.SendAndWait(ctx, copilot.MessageOptions{Prompt: fmt.Sprintf("Use %s to change probe.txt from before to after. Make exactly one tool call, then report completion and any hook probe marker you received.", tool)})
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			got := seen
			mu.Unlock()
			if !got {
				if tool == "edit" && reply != nil {
					if msg, ok := reply.Data.(*rpc.AssistantMessageData); ok && strings.Contains(msg.Content, "no `edit` tool is available") {
						t.Skip("plain edit is unavailable in the gpt-6-luna tool set; probe unverified")
					}
				}
				t.Fatal("hook was not called")
			}
			if reply == nil {
				t.Fatal("no reply")
			}
			msg, ok := reply.Data.(*rpc.AssistantMessageData)
			if !ok || !strings.Contains(msg.Content, marker) {
				t.Fatalf("AdditionalContext marker missing: %#v", reply.Data)
			}
			data, err := os.ReadFile(filepath.Join(dir, "probe.txt"))
			if err != nil || string(data) != "after\n" {
				t.Fatalf("file=%q error=%v", data, err)
			}
			t.Log("AdditionalContext alone reached final reply; edit succeeded")
		})
	}
}
