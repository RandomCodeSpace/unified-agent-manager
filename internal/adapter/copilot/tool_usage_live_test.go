package copilot

import (
	"bufio"
	"context"
	"encoding/json"
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

// TestToolUsageLiveProbe finds which tools report a separate toolUsage
// charge on gpt-6-luna and whether the CLI's checkpoint and shutdown totals
// already include it. Isolated sessions in a private COPILOT_HOME only.
func TestToolUsageLiveProbe(t *testing.T) {
	if os.Getenv("UAM_TOOLUSAGE_LIVE_PROBE") != "1" {
		t.Skip("set UAM_TOOLUSAGE_LIVE_PROBE=1 for the paid gpt-6-luna toolUsage probe")
	}
	home := t.TempDir()
	t.Setenv("COPILOT_HOME", home)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
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
	cases := []struct{ name, prompt string }{
		{"web_search", "Use the web search tool exactly once to find the latest stable Go release. Reply with the version only."},
		{"web_fetch", "Use web_fetch exactly once on https://example.com/. Reply with the page title only."},
		{"subagent", "Use the task tool exactly once to have the search-probe agent search the web for the latest stable Go release. Do not search yourself. Reply with its answer only."},
	}
	emitted := false
	var report []string
	for _, tc := range cases {
		var mu sync.Mutex
		names := map[string]string{}
		var tools, calls []string
		var assistantNano, toolNano, checkpoint float64
		checkpoints := 0
		var checkpointIDs []string
		seen := map[string]bool{}
		config := &copilot.SessionConfig{
			Model: "gpt-6-luna", WorkingDirectory: t.TempDir(),
			EnableConfigDiscovery: copilot.Bool(false), EnableSkills: copilot.Bool(false), EnableFileHooks: copilot.Bool(false),
			OnPermissionRequest: copilot.PermissionHandler.ApproveAll,
			OnEvent: func(e copilot.SessionEvent) {
				mu.Lock()
				defer mu.Unlock()
				switch d := e.Data.(type) {
				case *rpc.ToolExecutionStartData:
					names[d.ToolCallID] = d.ToolName
				case *rpc.ToolExecutionCompleteData:
					tools = append(tools, names[d.ToolCallID])
					if u := d.ToolUsage; u != nil {
						nano := 0.0
						if u.CopilotUsage != nil {
							nano = u.CopilotUsage.TotalNanoAiu
						}
						t.Logf("%s: toolUsage tool=%s model=%s request=%s nano_aiu=%.0f repeat=%v", tc.name, names[d.ToolCallID], u.Model, u.RequestID, nano, seen[u.RequestID])
						if !seen[u.RequestID] {
							seen[u.RequestID] = true
							toolNano += nano
						}
					}
				case *rpc.AssistantUsageData:
					nano := 0.0
					if d.CopilotUsage != nil {
						nano = d.CopilotUsage.TotalNanoAiu
						assistantNano += nano
					}
					calls = append(calls, fmt.Sprintf("%s/%s:%.0f", agentOf(e), d.Model, nano))
				case *rpc.SessionUsageCheckpointData:
					checkpoints++
					checkpoint, checkpointIDs = d.TotalNanoAiu, d.ToolUsageRequestIDs
				}
			},
		}
		switch tc.name {
		case "web_fetch":
			config.AvailableTools = []string{"web_fetch"}
		case "subagent":
			config.CustomAgents = []copilot.CustomAgentConfig{{Name: "search-probe", Description: "Isolated web search probe", Model: "gpt-6-luna", Prompt: "Search the web exactly once with your web search tool and report the answer briefly."}}
		}
		s, err := c.CreateSession(ctx, config)
		if err != nil {
			t.Fatal(err)
		}
		_, sendErr := s.SendAndWait(ctx, copilot.MessageOptions{Prompt: tc.prompt})
		_ = s.Disconnect()
		shutdown, journalTool, journalIDs := journalTotals(t, home, s.SessionID)
		if err := c.DeleteSession(context.Background(), s.SessionID); err != nil {
			t.Errorf("delete probe session: %v", err)
		}
		if sendErr != nil {
			t.Errorf("%s: %v", tc.name, sendErr)
			continue
		}
		mu.Lock()
		sum := assistantNano + toolNano
		line := fmt.Sprintf("%s: tools=%v calls(agent/model:nano)=%v assistant_nano=%.0f tool_nano=%.0f sum=%.0f checkpoints=%d last_checkpoint=%.0f checkpoint_tool_ids=%v shutdown=%s journal_tool_nano=%.0f journal_shutdown_tool_ids=%v",
			tc.name, tools, calls, assistantNano, toolNano, sum, checkpoints, checkpoint, checkpointIDs, shutdown, journalTool, journalIDs)
		mu.Unlock()
		t.Log(line)
		report = append(report, line)
		if toolNano > 0 {
			emitted = true
			if checkpoints > 0 && checkpoint != sum {
				t.Errorf("%s: checkpoint %.0f != assistant+tool %.0f", tc.name, checkpoint, sum)
			}
			if shutdown != "" && shutdown != fmt.Sprintf("%.0f", sum) {
				t.Errorf("%s: shutdown %s != assistant+tool %.0f", tc.name, shutdown, sum)
			}
		}
	}
	out := filepath.Join(os.TempDir(), "uam-toolusage-probe")
	if err := os.MkdirAll(out, 0o700); err == nil {
		_ = os.WriteFile(filepath.Join(out, "receipt.md"), []byte("# toolUsage probe ("+time.Now().Format(time.RFC3339)+", gpt-6-luna)\n\n- "+strings.Join(report, "\n- ")+"\n"), 0o600)
	}
	if !emitted {
		t.Fatal("no tool on gpt-6-luna reported toolUsage with a charge")
	}
}

// journalTotals reads the session journal's shutdown total and the summed
// toolUsage charges it recorded.
func journalTotals(t *testing.T, home, id string) (shutdown string, toolNano float64, ids []string) {
	f, err := os.Open(filepath.Join(home, "session-state", id, "events.jsonl"))
	if err != nil {
		t.Logf("journal: %v", err)
		return "", 0, nil
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		var ev struct {
			Type string `json:"type"`
			Data struct {
				TotalNanoAiu        *float64 `json:"totalNanoAiu"`
				ToolUsageRequestIDs []string `json:"toolUsageRequestIds"`
				ToolUsage           *struct {
					CopilotUsage *struct {
						TotalNanoAiu float64 `json:"totalNanoAiu"`
					} `json:"copilotUsage"`
				} `json:"toolUsage"`
			} `json:"data"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch {
		case ev.Type == "session.shutdown" && ev.Data.TotalNanoAiu != nil:
			shutdown, ids = fmt.Sprintf("%.0f", *ev.Data.TotalNanoAiu), ev.Data.ToolUsageRequestIDs
		case ev.Type == "tool.execution_complete" && ev.Data.ToolUsage != nil && ev.Data.ToolUsage.CopilotUsage != nil:
			toolNano += ev.Data.ToolUsage.CopilotUsage.TotalNanoAiu
		}
	}
	return shutdown, toolNano, ids
}
