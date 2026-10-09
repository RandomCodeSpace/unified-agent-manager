package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func progressEvents(t *testing.T) []copilot.SessionEvent {
	t.Helper()
	journal := []copilot.SessionEvent{
		ev("start", &rpc.ToolExecutionStartData{ToolCallID: "main", ToolName: "mcp_fetch", Arguments: map[string]any{"url": "https://example.test"}}),
		ev("progress", &rpc.ToolExecutionProgressData{ToolCallID: "main", ProgressMessage: "Connecting", StructuredContent: map[string]any{"percent": 50}}),
		ev("progress-next", &rpc.ToolExecutionProgressData{ToolCallID: "main", ProgressMessage: "Reading"}),
		agentEv("child-start", "child", &rpc.ToolExecutionStartData{ToolCallID: "child-call", ToolName: "bash", Arguments: map[string]any{"command": "pwd"}}),
		agentEv("child-output", "child", &rpc.ToolShellOutputData{ToolCallID: "child-call", Sequence: 0, Text: "stdout\n"}),
		agentEv("child-progress", "child", &rpc.ToolExecutionProgressData{ToolCallID: "child-call", ProgressMessage: "\x1b[31m Waiting\x1b[0m\nfor file\x00\t"}),
	}
	for i := range journal {
		journal[i].Timestamp = journal[i].Timestamp.Add(time.Duration(i) * time.Second)
	}
	body, err := json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &journal); err != nil {
		t.Fatal(err)
	}
	return journal
}

func TestWebToolProgressOnCreateResumeAndReplay(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(fmt.Sprintf("resume=%v", resume), func(t *testing.T) {
			fc := &fakeClient{}
			p := readerProvider(fc)
			t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
			sink := &recSink{}
			req := agentapi.OpenRequest{SessionID: "progress", Events: sink}
			if resume {
				req.ConversationID = "progress"
			}
			conv, err := p.Open(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			fs := fc.sessions[0]
			fs.events = progressEvents(t)
			for _, e := range fs.events {
				fs.onEvent(e)
			}
			var live []agentapi.Item
			for _, e := range sink.all() {
				if e.Kind == agentapi.EventItem {
					live = append(live, *e.Item)
				}
			}
			if len(live) != 6 || live[1].Tool.Progress != "Connecting" || live[2].Tool.Progress != "Reading" || live[5].Tool.Progress != "Waiting for file" {
				t.Fatalf("live items = %+v", live)
			}
			if live[2].ID != "main" || live[2].AgentID != "" || live[2].Tool.Output != "" || live[5].ID != "child-call" || live[5].AgentID != "child" || live[5].Tool.Output != "stdout\n" || !reflect.DeepEqual(live[5].Tool.Tail, []agentapi.OutputLine{{Text: "stdout"}}) {
				t.Fatalf("progress changed tool output or identity: main %+v, child %+v", live[2], live[5])
			}
			h, err := conv.History(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			want := []agentapi.Item{live[2], live[5]}
			want[0].Time, want[1].Time = fs.events[0].Timestamp, fs.events[3].Timestamp
			if !reflect.DeepEqual(h.Items, want) {
				t.Fatalf("history = %+v, want %+v", h.Items, want)
			}
		})
	}
}

func TestWebToolProgressRejectsMissingWrongAgentAndEndedCalls(t *testing.T) {
	h := openWeb(t)
	progress := func(id, agent, message string) {
		h.fs.onEvent(agentEv("progress", agent, &rpc.ToolExecutionProgressData{ToolCallID: id, ProgressMessage: message}))
	}
	progress("missing", "", "Missing")
	if len(h.sink.all()) != 0 {
		t.Fatal("progress created a missing call")
	}
	h.fs.onEvent(ev("start", &rpc.ToolExecutionStartData{ToolCallID: "known", ToolName: "mcp_fetch"}))
	n := len(h.sink.all())
	progress("known", "other-agent", "Wrong agent")
	progress("known", "", " \n\t\x00")
	if len(h.sink.all()) != n {
		t.Fatal("progress accepted a wrong agent or empty message")
	}
	progress("known", "", strings.Repeat("界", 200))
	it := h.sink.last().Item
	if it == nil || it.Tool.Progress != strings.Repeat("界", 170) || it.Clipped {
		t.Fatalf("bounded progress = %+v", it)
	}
	n = len(h.sink.all())
	progress("known", "", strings.Repeat("界", 200))
	if len(h.sink.all()) != n {
		t.Fatal("unchanged progress emitted another update")
	}
	for _, success := range []bool{true, false} {
		h.fs.onEvent(ev("start", &rpc.ToolExecutionStartData{ToolCallID: "known", ToolName: "mcp_fetch"}))
		progress("known", "", "Running")
		h.fs.onEvent(ev("complete", &rpc.ToolExecutionCompleteData{ToolCallID: "known", Success: success, Result: &rpc.ToolExecutionCompleteResult{Content: "FINAL"}}))
		final := h.sink.last().Item
		if final.Tool.Progress != "" || final.Tool.Output != "FINAL" || final.Tool.Tail != nil {
			t.Fatalf("final = %+v", final)
		}
		n = len(h.sink.all())
		progress("known", "", "Late")
		if len(h.sink.all()) != n {
			t.Fatal("late progress reopened a finished call")
		}
	}
}

func TestWebToolProgressKeepsClippedInputAndOutput(t *testing.T) {
	h := openWeb(t)
	h.fs.onEvent(ev("start", &rpc.ToolExecutionStartData{ToolCallID: "shell", ToolName: "bash", Arguments: map[string]any{"command": strings.Repeat("x", maxToolText)}}))
	h.fs.onEvent(ev("output", &rpc.ToolShellOutputData{ToolCallID: "shell", Sequence: 0, Text: strings.Repeat("y", maxToolText+1)}))
	before := h.sink.last().Item
	h.fs.onEvent(ev("progress", &rpc.ToolExecutionProgressData{ToolCallID: "shell", ProgressMessage: "Waiting"}))
	after := h.sink.last().Item
	if !before.Clipped || !after.Clipped || before.Tool.Input != after.Tool.Input || before.Tool.Output != after.Tool.Output || !reflect.DeepEqual(before.Tool.Tail, after.Tool.Tail) {
		t.Fatal("progress changed the clipped body or its shell tail")
	}
	h.fs.onEvent(ev("unknown-output", &rpc.ToolShellOutputData{ToolCallID: "unknown", Sequence: 0, Text: "stdout"}))
	n := len(h.sink.all())
	h.fs.onEvent(ev("unknown-progress", &rpc.ToolExecutionProgressData{ToolCallID: "unknown", ProgressMessage: "Unknown start"}))
	if len(h.sink.all()) != n {
		t.Fatal("a shell output without a recorded start accepted progress")
	}
}

func TestReadHistoryToolProgressAcrossPagesAndWholeWindows(t *testing.T) {
	fc := &fakeClient{journal: progressEvents(t), pageSize: 1}
	p := readerProvider(fc)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	h, err := p.ReadHistory(context.Background(), agentapi.ReadRequest{ConversationID: "progress"})
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Items) != 2 || h.Items[0].Tool.Progress != "Reading" || h.Items[1].Tool.Progress != "Waiting for file" || len(fc.resume) != 0 || len(fc.create) != 0 {
		t.Fatalf("history = %+v", h.Items)
	}
	w, err := p.ReadHistoryWindow(context.Background(), agentapi.WindowRequest{ReadRequest: agentapi.ReadRequest{ConversationID: "progress"}, AgentID: "child", ItemID: "child-call"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(w.Items, h.Items[1:]) {
		t.Fatalf("child whole window = %+v, want %+v", w.Items, h.Items[1:])
	}
	fc.journal = append(fc.journal, ev("final", &rpc.ToolExecutionCompleteData{ToolCallID: "main", Success: true, Result: &rpc.ToolExecutionCompleteResult{Content: "FINAL"}}), ev("late", &rpc.ToolExecutionProgressData{ToolCallID: "main", ProgressMessage: "Late"}))
	h, err = p.ReadHistory(context.Background(), agentapi.ReadRequest{ConversationID: "progress"})
	if err != nil || len(h.Items) != 2 || h.Items[0].Tool.Progress != "" || h.Items[0].Tool.Output != "FINAL" || h.Items[0].Tool.Status != agentapi.ToolCompleted {
		t.Fatalf("final history = %+v, error %v", h.Items, err)
	}
	fc.journal = append(fc.journal, agentEv("long-progress", "child", &rpc.ToolExecutionProgressData{ToolCallID: "child-call", ProgressMessage: strings.Repeat("界", 200)}))
	w, err = p.ReadHistoryWindow(context.Background(), agentapi.WindowRequest{ReadRequest: agentapi.ReadRequest{ConversationID: "progress"}, AgentID: "child", ItemID: "child-call"})
	if err != nil || len(w.Items) != 1 || w.Items[0].Tool.Progress != strings.Repeat("界", 170) || w.Items[0].Clipped {
		t.Fatalf("whole progress bound = %+v, error %v", w.Items, err)
	}
}
