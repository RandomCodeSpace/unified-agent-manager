package web

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

func TestClosedLiveTranscriptsReloadReadOnly(t *testing.T) {
	for _, action := range []string{"close", "settle", "archive"} {
		t.Run(action, func(t *testing.T) {
			prov := agenttest.NewPager("fake", allCaps)
			m := startManager(t, openTestStore(t), prov)
			sum, conv := createSession(t, m, prov.Provider)
			item := agentapi.Item{ID: "reply", Kind: agentapi.ItemAssistant, Text: "recorded reply"}
			conv.EmitItem(item)
			release := make(chan struct{})
			prov.SetReadHook(func(ctx context.Context, req agentapi.ReadRequest) (agentapi.History, error) {
				select {
				case <-release:
					return agentapi.History{Items: []agentapi.Item{item}}, nil
				case <-ctx.Done():
					return agentapi.History{}, ctx.Err()
				}
			})
			var closed SessionSummary
			var err error
			switch action {
			case "close":
				closed, err = m.Close(sum.ID)
			case "settle":
				closed, err = m.Settle(sum.ID)
			case "archive":
				closed, err = m.Archive(sum.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if d := detail(t, m, sum.ID); d.History != HistoryLoading || len(d.Items) != 0 {
				t.Fatalf("closed transcript = history %q, %d items; want a released transcript loading from disk", d.History, len(d.Items))
			}
			close(release)
			d := waitHistory(t, m, sum.ID, HistoryLoaded)
			if len(d.Items) != 1 || d.Items[0].ID != "reply" || d.Items[0].Text != "recorded reply" || d.Open || d.State != closed.State || d.Stage != closed.Stage || !d.UpdatedAt.Equal(closed.UpdatedAt) {
				t.Fatalf("reloaded transcript = %+v", d)
			}
			if len(prov.Opens()) != 1 || len(prov.Reads()) != 1 || prov.Reads()[0].ConversationID != sum.ConversationID {
				t.Fatalf("opens = %d, reads = %+v; want one read of the original conversation without reopening", len(prov.Opens()), prov.Reads())
			}
		})
	}
}

func TestClosedTranscriptStaysUntilItsLastViewerLeaves(t *testing.T) {
	prov := agenttest.NewPager("fake", allCaps)
	m := startManager(t, openTestStore(t), prov)
	sum, conv := createSession(t, m, prov.Provider)
	conv.EmitItem(agentapi.Item{ID: "reply", Kind: agentapi.ItemAssistant, Text: "keep while viewed"})
	first, _, err := m.Subscribe(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	last, _, err := m.Subscribe(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Settle(sum.ID); err != nil {
		t.Fatal(err)
	}
	m.Unsubscribe(first)
	if d := detail(t, m, sum.ID); d.History != HistoryLoaded || len(d.Items) != 1 || len(prov.Reads()) != 0 {
		t.Fatalf("viewed transcript = %+v, reads %d", d, len(prov.Reads()))
	}
	m.Unsubscribe(last)
	if d := detail(t, m, sum.ID); d.History != HistoryLoading || len(d.Items) != 0 {
		t.Fatalf("after last viewer = history %q, %d items", d.History, len(d.Items))
	}
}

func TestClosedTranscriptDirectReadsRecoverFromTheRecord(t *testing.T) {
	for _, request := range []string{"body", "subagent", "evidence"} {
		t.Run(request, func(t *testing.T) {
			prov := agenttest.NewPager("fake", allCaps)
			m := startManager(t, openTestStore(t), prov)
			sum, conv := createSession(t, m, prov.Provider)
			at := time.Now()
			exit := 0
			record := agentapi.History{
				Items: []agentapi.Item{
					{ID: "prompt", Kind: agentapi.ItemUser, Text: "run the tests", Time: at},
					{ID: "check", Kind: agentapi.ItemTool, Time: at.Add(time.Second), Tool: &agentapi.ToolCall{Name: "bash", Status: agentapi.ToolCompleted, Input: `{"command":"go test ./..."}`, Output: goOK + strings.Repeat("output\n", maxToolText), ExitCode: &exit}},
					{ID: "reply", Kind: agentapi.ItemAssistant, Text: "All tests passed.", Time: at.Add(2 * time.Second)},
					{ID: "child", Kind: agentapi.ItemAssistant, AgentID: "sa", Text: "recorded child reply", Time: at.Add(time.Second)},
				},
				Subagents: []agentapi.Subagent{{ID: "sa", Name: "helper", Status: agentapi.SubagentCompleted}},
			}
			for _, item := range record.Items {
				conv.EmitItem(item)
			}
			conv.EmitSubagent(record.Subagents[0])
			prov.SetHistory(sum.ConversationID, record)
			if _, err := m.Archive(sum.ID); err != nil {
				t.Fatal(err)
			}
			switch request {
			case "body":
				body, err := m.ItemBody(sum.ID, "", "check")
				if err != nil || body.Item.Tool == nil || body.Item.Tool.Output != record.Items[1].Tool.Output || body.Item.Clipped {
					t.Fatalf("whole recorded body = %+v, %v", body, err)
				}
			case "subagent":
				d, err := m.CompactSubagent(sum.ID, "sa")
				if err != nil || d.Subagent.ID != "sa" || len(d.Items) != 1 || d.Items[0].ID != "child" {
					t.Fatalf("recorded subagent = %+v, %v", d, err)
				}
			case "evidence":
				e, err := m.TurnEvidence(context.Background(), sum.ID, time.Time{}, time.Time{})
				if err != nil || len(e.Checks) != 1 || e.Checks[0].Command != "go test ./..." {
					t.Fatalf("recorded evidence = %+v, %v", e, err)
				}
			}
			if len(prov.Opens()) != 1 {
				t.Fatalf("direct read opened %d conversations", len(prov.Opens()))
			}
		})
	}
}
