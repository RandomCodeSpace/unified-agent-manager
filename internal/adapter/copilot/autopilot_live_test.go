package copilot

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

// TestAutopilotStoppedLiveProbe logs the order of the events that end an
// autopilot run on the installed CLI: session.task_complete, assistant.idle,
// session.idle (with its mode) and session.autopilot_stopped (with its
// reason). It uses isolated temporary sessions and a private COPILOT_HOME;
// no owner Threads are touched.
func TestAutopilotStoppedLiveProbe(t *testing.T) {
	if os.Getenv("UAM_AUTOPILOT_LIVE_PROBE") != "1" {
		t.Skip("set UAM_AUTOPILOT_LIVE_PROBE=1 for the CLI autopilot probe")
	}
	t.Setenv("COPILOT_HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
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
	cases := []struct{ name, prompt string }{
		{"task-complete", "Write the word hi to a.txt in the current directory, then finish the task."},
		{"abort", "Use bash to run `sleep 30`, then write the word hi to b.txt, then finish the task."},
		{"attached-shell", "Use bash in async mode (attached, not detached) to start `sleep 40`. Do not wait for it or read its output. Then finish the task at once."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var order []string
			stopped, toolStarted := make(chan struct{}), make(chan struct{})
			var stopOnce, toolOnce sync.Once
			last := time.Now()
			config := &copilot.SessionConfig{Model: "gpt-6-luna", WorkingDirectory: t.TempDir(), EnableConfigDiscovery: copilot.Bool(false), EnableSkills: copilot.Bool(false), EnableSessionStore: copilot.Bool(false), EnableFileHooks: copilot.Bool(false), OnPermissionRequest: copilot.PermissionHandler.ApproveAll,
				OnEvent: func(e copilot.SessionEvent) {
					var line string
					switch d := e.Data.(type) {
					case *rpc.SessionTaskCompleteData:
						line = "session.task_complete"
					case *rpc.AssistantIdleData:
						line = fmt.Sprintf("assistant.idle aborted=%v", d.Aborted != nil && *d.Aborted)
					case *rpc.SessionIdleData:
						mode := "<nil>"
						if d.Mode != nil {
							mode = string(*d.Mode)
						}
						line = fmt.Sprintf("session.idle mode=%s aborted=%v", mode, d.Aborted != nil && *d.Aborted)
					case *rpc.SessionAutopilotStoppedData:
						line = fmt.Sprintf("session.autopilot_stopped reason=%s request_id_set=%v", d.Reason, d.RequestID != nil)
						stopOnce.Do(func() { close(stopped) })
					case *rpc.ToolExecutionStartData:
						line = "tool.execution_start " + d.ToolName
						toolOnce.Do(func() { close(toolStarted) })
					case *rpc.AbortData:
						line = "abort"
					case *rpc.SessionModeChangedData:
						line = "session.mode_changed " + string(d.NewMode)
					case *rpc.SessionBackgroundTasksChangedData:
						line = "session.background_tasks_changed"
					}
					mu.Lock()
					defer mu.Unlock()
					last = time.Now()
					if line != "" && agentOf(e) == "" {
						order = append(order, line)
					}
				},
			}
			s, err := c.CreateSession(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Disconnect(); _ = c.DeleteSession(context.Background(), s.SessionID) }()
			if _, err = s.RPC.Mode.Set(ctx, &rpc.ModeSetRequest{Mode: rpc.SessionModeAutopilot}); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Send(ctx, copilot.MessageOptions{Prompt: tc.prompt}); err != nil {
				t.Fatal(err)
			}
			if tc.name == "abort" {
				select {
				case <-toolStarted:
				case <-time.After(3 * time.Minute):
					t.Fatal("no tool started")
				}
				time.Sleep(2 * time.Second)
				if err = s.Abort(ctx); err != nil {
					t.Fatal(err)
				}
			}
			// The run ends at autopilot_stopped plus a grace for the events
			// after it, or after a long quiet without it.
			deadline := time.After(5 * time.Minute)
		wait:
			for {
				select {
				case <-stopped:
					time.Sleep(10 * time.Second)
					break wait
				case <-deadline:
					break wait
				case <-time.After(5 * time.Second):
					mu.Lock()
					quiet := time.Since(last)
					mu.Unlock()
					if quiet > 90*time.Second {
						break wait
					}
				}
			}
			mu.Lock()
			defer mu.Unlock()
			t.Logf("order:\n  %s", strings.Join(order, "\n  "))
			got := false
			for _, line := range order {
				got = got || strings.HasPrefix(line, "session.autopilot_stopped")
			}
			if !got && tc.name != "attached-shell" {
				t.Errorf("no session.autopilot_stopped in %s", tc.name)
			}
		})
	}
}
