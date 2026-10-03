package web

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func run(start time.Time, status agentapi.SubagentStatus, trigger string) agentapi.SubagentRun {
	r := agentapi.SubagentRun{StartedAt: start, Status: status, Trigger: trigger}
	if status != agentapi.SubagentRunning {
		r.EndedAt = start.Add(time.Second)
	}
	return r
}

func runsText(runs []agentapi.SubagentRun) string {
	var out []string
	for _, r := range runs {
		out = append(out, r.Trigger+":"+string(r.Status))
	}
	return strings.Join(out, ",")
}

func TestSubagentUpdatesKeepKnownRuns(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	t0 := time.Now().Add(-time.Hour)
	spawn, agent := run(t0, agentapi.SubagentCompleted, "spawn"), run(t0.Add(time.Minute), agentapi.SubagentRunning, "agent")
	conv.EmitSubagent(agentapi.Subagent{ID: "a", Name: "helper", Status: agentapi.SubagentRunning, StartedAt: agent.StartedAt,
		Runs: []agentapi.SubagentRun{spawn, agent}})
	held := func() agentapi.Subagent {
		t.Helper()
		d := detail(t, m, sum.ID)
		if len(d.Subagents) != 1 {
			t.Fatalf("subagents = %+v", d.Subagents)
		}
		return d.Subagents[0]
	}
	// An update without runs keeps them; its status reaches the latest run.
	end := agent.StartedAt.Add(time.Second)
	conv.EmitSubagent(agentapi.Subagent{ID: "a", Status: agentapi.SubagentIdle, EndedAt: end})
	if sa := held(); runsText(sa.Runs) != "spawn:completed,agent:idle" || !sa.Runs[1].EndedAt.Equal(end) {
		t.Fatalf("runs = %+v", sa.Runs)
	}
	// So does a record rebuilt from recorded events, which knows only the
	// spawn run.
	conv.EmitSubagent(agentapi.Subagent{ID: "a", Status: agentapi.SubagentIdle, EndedAt: end, Runs: []agentapi.SubagentRun{spawn}})
	if sa := held(); runsText(sa.Runs) != "spawn:completed,agent:idle" {
		t.Fatalf("runs = %+v", sa.Runs)
	}
	// A newer latest run of such a record is added.
	user := run(end.Add(time.Minute), agentapi.SubagentRunning, "user")
	conv.EmitSubagent(agentapi.Subagent{ID: "a", Status: agentapi.SubagentRunning, StartedAt: user.StartedAt, Runs: []agentapi.SubagentRun{spawn, user}})
	if sa := held(); runsText(sa.Runs) != "spawn:completed,agent:idle,user:running" {
		t.Fatalf("runs = %+v", sa.Runs)
	}
	// The compact detail carries them.
	d, err := m.CompactDetail(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Subagents []struct {
			Runs []agentapi.SubagentRun `json:"runs"`
		} `json:"subagents"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil || len(decoded.Subagents) != 1 || runsText(decoded.Subagents[0].Runs) != "spawn:completed,agent:idle,user:running" {
		t.Fatalf("compact detail = %v, %s", err, raw)
	}
	// Closing the conversation cancels the running run.
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	if sa := held(); runsText(sa.Runs) != "spawn:completed,agent:idle,user:cancelled" || sa.Runs[2].EndedAt.IsZero() {
		t.Fatalf("runs after close = %+v", sa.Runs)
	}
}

func TestRecordedSubagentPagesCarryRuns(t *testing.T) {
	record := subagentRecord(250)
	t0 := time.Now().Add(-time.Hour)
	record.Subagents[10].Runs = []agentapi.SubagentRun{run(t0, agentapi.SubagentCompleted, "spawn"), run(t0.Add(time.Minute), agentapi.SubagentRunning, "agent")}
	m, _, _, sum := archivedTask(t, record, true)
	d, err := m.CompactDetail(sum.ID)
	if err != nil || d.SubagentsBefore == "" {
		t.Fatalf("detail = %v, before %q", err, d.SubagentsBefore)
	}
	page, err := m.OlderSubagents(sum.ID, d.SubagentsBefore)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Subagents []struct {
			ID     string                  `json:"id"`
			Status agentapi.SubagentStatus `json:"status"`
			Runs   []agentapi.SubagentRun  `json:"runs"`
		} `json:"subagents"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, sa := range decoded.Subagents {
		if sa.ID == "sa-010" {
			// Nothing of a closed conversation runs, its latest run included.
			if sa.Status != agentapi.SubagentCancelled || runsText(sa.Runs) != "spawn:completed,agent:cancelled" {
				t.Fatalf("recorded subagent = %+v", sa)
			}
			if record.Subagents[10].Runs[1].Status != agentapi.SubagentRunning {
				t.Fatal("the page changed the record's runs")
			}
			return
		}
	}
	t.Fatalf("sa-010 not paged: %s", raw)
}

func TestExportCountsSubagentRuns(t *testing.T) {
	t0 := time.Now()
	sa := &agentapi.Subagent{Name: "helper", Status: agentapi.SubagentCompleted}
	tc := &agentapi.ToolCall{Name: "task", Status: agentapi.ToolCompleted}
	if line := toolSummary(tc, sa, ""); strings.Contains(line, "runs") {
		t.Fatalf("one run = %q", line)
	}
	sa.Runs = []agentapi.SubagentRun{run(t0, agentapi.SubagentCompleted, "spawn"), run(t0.Add(time.Minute), agentapi.SubagentCompleted, "agent")}
	if line := toolSummary(tc, sa, ""); !strings.Contains(line, "· completed · 2 runs") {
		t.Fatalf("two runs = %q", line)
	}
}

func TestCompactDetailOutlinesMessagesAndSubagentCalls(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	t0 := time.Now().Add(-time.Hour)
	conv.EmitItem(agentapi.Item{ID: "u1", Kind: agentapi.ItemUser, Text: "  Audit every package\nthen fix them", Time: t0})
	conv.EmitItem(agentapi.Item{ID: "c1", Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "task", Status: agentapi.ToolCompleted}, Time: t0.Add(time.Second)})
	conv.EmitItem(agentapi.Item{ID: "b1", Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "bash", Status: agentapi.ToolCompleted}, Time: t0.Add(2 * time.Second)})
	conv.EmitItem(agentapi.Item{ID: "a1", Kind: agentapi.ItemAssistant, Text: "Done.", Time: t0.Add(3 * time.Second)})
	d, err := m.CompactDetail(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range d.Outline {
		got = append(got, it.ID+":"+string(it.Kind)+":"+it.Text)
	}
	if strings.Join(got, ",") != "u1:user:Audit every package,c1:tool:" || d.Outline[1].Tool == nil || d.Outline[1].Tool.Name != "task" {
		t.Fatalf("outline = %+v", d.Outline)
	}
}
