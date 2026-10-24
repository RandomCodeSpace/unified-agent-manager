package copilot

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

// TestReadOnceLiveProbe checks, on the installed CLI with a private
// COPILOT_HOME and disposable sessions, that a post-tool hook can replace a
// repeat view's result (a), what shape a view result has (b), that a
// subagent's calls carry an agent ID and the main agent's do not (c), and the
// same for bash (d, recorded only).
func TestReadOnceLiveProbe(t *testing.T) {
	if os.Getenv("UAM_READONCE_LIVE_PROBE") != "1" {
		t.Skip("set UAM_READONCE_LIVE_PROBE=1 for the read-once probe")
	}
	t.Setenv("COPILOT_HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
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
	const marker = "readonce-note-teal-517"
	type call struct{ tool, agent, args, result string }
	run := func(t *testing.T, tools []string, agents []copilot.CustomAgentConfig, prompt string, replace func(n int, in copilot.PostToolUseHookInput) any) ([]call, string) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("line one\nsecret word: amber-falcon\nline three\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		var mu sync.Mutex
		var calls []call
		views := 0
		config := &copilot.SessionConfig{Model: "gpt-6-luna", WorkingDirectory: dir, EnableConfigDiscovery: copilot.Bool(false), EnableSkills: copilot.Bool(false), EnableSessionStore: copilot.Bool(false), EnableFileHooks: copilot.Bool(false), OnPermissionRequest: copilot.PermissionHandler.ApproveAll,
			AvailableTools: tools, CustomAgents: agents,
			Hooks: &copilot.SessionHooks{OnPostToolUse: func(in copilot.PostToolUseHookInput, _ copilot.HookInvocation) (*copilot.PostToolUseHookOutput, error) {
				args, _ := json.Marshal(in.ToolArgs)
				result, _ := json.Marshal(in.ToolResult)
				mu.Lock()
				defer mu.Unlock()
				calls = append(calls, call{in.ToolName, in.AgentID, string(args), string(result)})
				if in.ToolName != "view" || replace == nil {
					return nil, nil
				}
				views++
				if r := replace(views, in); r != nil {
					return &copilot.PostToolUseHookOutput{ModifiedResult: r}, nil
				}
				return nil, nil
			}},
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
		content := ""
		if reply != nil {
			if msg, ok := reply.Data.(*rpc.AssistantMessageData); ok {
				content = msg.Content
			}
		}
		mu.Lock()
		defer mu.Unlock()
		for _, v := range calls {
			t.Logf("tool=%s agent_id=%q args=%s result=%.600s", v.tool, v.agent, v.args, v.result)
		}
		t.Logf("reply=%s", content)
		return calls, content
	}
	note := func(n int, in copilot.PostToolUseHookInput) any {
		if n < 2 {
			return nil
		}
		text := "uam read-once: notes.txt is unchanged since your earlier view in this conversation; use that result. Probe marker " + marker + "."
		m, ok := in.ToolResult.(map[string]any)
		if !ok {
			return text
		}
		out := map[string]any{}
		for k, v := range m {
			out[k] = v
		}
		out["textResultForLlm"] = text
		delete(out, "contents")
		return out
	}
	t.Run("a-modified-view", func(t *testing.T) {
		passed := 0
		for i := range 3 {
			calls, reply := run(t, []string{"view"}, nil, "Use the view tool on notes.txt. Then use the view tool on notes.txt a second time. Quote exactly what the second view returned, verbatim, including any probe marker. Do not use other tools.", note)
			views := 0
			for _, v := range calls {
				if v.tool == "view" {
					views++
				}
			}
			ok := views >= 2 && strings.Contains(reply, marker)
			t.Logf("run %d: views=%d marker_quoted=%v", i+1, views, ok)
			if ok {
				passed++
			}
		}
		t.Logf("(a) %d/3", passed)
		if passed != 3 {
			t.Errorf("(a) failed: %d/3", passed)
		}
	})
	t.Run("c-subagent-agent-id", func(t *testing.T) {
		agents := []copilot.CustomAgentConfig{{Name: "view-probe", Description: "Isolated view probe", Model: "gpt-6-luna", Tools: []string{"view"}, Prompt: "View the requested file exactly once and report its contents. Never use other tools."}}
		calls, _ := run(t, []string{"task", "view"}, agents, "First use the view tool yourself on notes.txt. Then use task to call view-probe on model gpt-6-luna to view notes.txt. Report both results.", nil)
		var mainIDs, subIDs []string
		for _, v := range calls {
			if v.tool != "view" {
				continue
			}
			if v.agent == "" {
				mainIDs = append(mainIDs, v.agent)
			} else {
				subIDs = append(subIDs, v.agent)
			}
		}
		t.Logf("(c) main views=%d subagent views=%d subagent ids=%v", len(mainIDs), len(subIDs), subIDs)
		if len(mainIDs) == 0 || len(subIDs) == 0 {
			t.Error("(c) failed: need a main view with an empty agent ID and a subagent view with one")
		}
	})
	t.Run("d-bash", func(t *testing.T) {
		agents := []copilot.CustomAgentConfig{{Name: "bash-probe", Description: "Isolated bash probe", Model: "gpt-6-luna", Tools: []string{"bash"}, Prompt: "Run the requested command exactly once and report its output. Never use other tools."}}
		run(t, []string{"task", "bash"}, agents, "First use bash yourself to run: cat notes.txt. Then use task to call bash-probe on model gpt-6-luna to run: cat notes.txt. Report both results.", nil)
	})
}
