package copilot

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
)

// TestGlabLiveProbe checks the native bash argument contract without a remote.
func TestGlabLiveProbe(t *testing.T) {
	if os.Getenv("UAM_GLAB_LIVE_PROBE") != "1" {
		t.Skip("set UAM_GLAB_LIVE_PROBE=1 for the isolated CLI probe")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client, err := newSDKClient()
	if err != nil {
		t.Fatal(err)
	}
	defer client.ForceStop()
	if err = client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	scratch := filepath.Join(root, "scratch")
	if err := os.Mkdir(scratch, 0700); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var calls int
	var rewritten bool
	c := client.(sdkClientAdapter).c
	s, err := c.CreateSession(ctx, &copilot.SessionConfig{
		Model: "gpt-6-luna", WorkingDirectory: root, AvailableTools: []string{"bash"},
		EnableConfigDiscovery: copilot.Bool(false), EnableSkills: copilot.Bool(false),
		EnableSessionStore: copilot.Bool(false), EnableFileHooks: copilot.Bool(false),
		OnPermissionRequest: copilot.PermissionHandler.ApproveAll,
		Hooks: &copilot.SessionHooks{OnPreToolUse: func(in copilot.PreToolUseHookInput, _ copilot.HookInvocation) (*copilot.PreToolUseHookOutput, error) {
			if in.ToolName != "bash" {
				return nil, nil
			}
			mu.Lock()
			defer mu.Unlock()
			calls++
			args, ok := in.ToolArgs.(map[string]any)
			if !ok {
				t.Errorf("bash args type %T", in.ToolArgs)
				return nil, nil
			}
			cwd := strings.ReplaceAll(in.WorkingDirectory, root, "$SESSION")
			t.Logf("bash call=%d cwd=%s args=%s", calls, cwd, strings.ReplaceAll(fmt.Sprint(args), root, "$SESSION"))
			command, _ := args["command"].(string)
			if strings.Contains(command, "glab-probe-before-572") {
				out := maps.Clone(args)
				out["command"] = strings.ReplaceAll(command, "glab-probe-before-572", "glab-probe-after-572")
				rewritten = true
				return &copilot.PreToolUseHookOutput{ModifiedArgs: out, AdditionalContext: "The harmless probe marker was rewritten."}, nil
			}
			return nil, nil
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Disconnect(); _ = c.DeleteSession(context.Background(), s.SessionID) }()
	for _, prompt := range []string{
		"Use bash exactly once to run this command, then finish. Do not run any other command: cd " + scratch + " && pwd",
		"Use bash exactly once to run this command, then finish. Do not add cd or run other tools: printf glab-probe-before-572 > " + filepath.Join(root, "marker") + "; pwd",
	} {
		if _, err := s.SendAndWait(ctx, copilot.MessageOptions{Prompt: prompt}); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	marker, err := os.ReadFile(filepath.Join(root, "marker"))
	if err != nil || string(marker) != "glab-probe-after-572" || !rewritten || calls != 2 {
		t.Fatalf("rewrite=%v calls=%d marker=%q error=%v", rewritten, calls, marker, err)
	}
	t.Log("ModifiedArgs preserved the native map and the rewritten command executed")
}
