package web

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// uamRefused calls the uam tool and wants it refused with want.
func uamRefused(t *testing.T, conv *agenttest.Conversation, args, want string) {
	t.Helper()
	if res := uamReadCall(t, conv, args); !res.Failed || !strings.Contains(res.Text, want) {
		t.Fatalf("%s = %+v; want refusal %q", args, res, want)
	}
}

func budgetOf(m *Manager, id string) store.Budget {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[id].budget
}

// An agent renames its own Thread and sets its budget, which it may add or
// tighten but never raise or remove; the ops take no other Thread.
func TestUAMRenameAndBudgetOwnThread(t *testing.T) {
	m, prov, _ := newTestManager(t)
	caller, conv := createSession(t, m, prov)
	other, _ := createSession(t, m, prov)

	var row uamTaskRow
	uamReadJSON(t, conv, `{"op":"rename","args":{"name":"Parser fix"}}`, &row)
	if row.ID != caller.ID || row.Name != "Parser fix" || summaryOf(t, m, caller.ID).Name != "Parser fix" {
		t.Fatalf("rename = %+v", row)
	}
	uamRefused(t, conv, `{"op":"rename","args":{}}`, "name is required")
	uamRefused(t, conv, fmt.Sprintf(`{"op":"rename","args":{"name":"x","id":%q}}`, other.ID), `unknown field "id"`)
	uamRefused(t, conv, fmt.Sprintf(`{"op":"rename","args":{"name":%q}}`, strings.Repeat("n", maxNameRunes+1)), "name")
	if got := summaryOf(t, m, other.ID).Name; got != "task" {
		t.Fatalf("other thread renamed to %q", got)
	}

	uamRefused(t, conv, `{"op":"set_budget","args":{"credits":-1}}`, "budget credits must be")
	uamReadJSON(t, conv, `{"op":"set_budget","args":{"credits":5}}`, &row)
	if b := budgetOf(m, caller.ID); b.Credits != 5 {
		t.Fatalf("budget = %+v", b)
	}
	for _, args := range []string{`{"credits":6}`, `{}`, `{"quota_percent":50}`} {
		uamRefused(t, conv, `{"op":"set_budget","args":`+args+`}`, "only tighten")
	}
	uamReadJSON(t, conv, `{"op":"set_budget","args":{"credits":3,"quota_percent":40}}`, &row)
	if b := budgetOf(m, caller.ID); b.Credits != 3 || b.QuotaPercent != 40 {
		t.Fatalf("tightened budget = %+v", b)
	}
	uamRefused(t, conv, `{"op":"set_budget","args":{"credits":3,"quota_percent":41}}`, "only tighten")
	// Credits already spent count: the new limit starts afresh, so it fits in what is left.
	setSession(m, caller.ID, func(s *webSession) { s.usage = &agentapi.Usage{AIUnits: s.budget.From + 2} })
	uamRefused(t, conv, `{"op":"set_budget","args":{"credits":2,"quota_percent":40}}`, "credits above 0 and up to the 1.00 left, and quota_percent above 0 and up to 40")
	uamReadJSON(t, conv, `{"op":"set_budget","args":{"credits":1,"quota_percent":40}}`, &row)
	if b := budgetOf(m, caller.ID); b.Credits != 1 || b.Reached {
		t.Fatalf("budget tightened after spending = %+v", b)
	}
	if _, err := m.SetBudget(caller.ID, 0, 50); err != nil {
		t.Fatal(err)
	}
	uamRefused(t, conv, `{"op":"set_budget","args":{"credits":2}}`, "only tighten the budget the owner set: give quota_percent above 0 and up to 50")
	uamReadJSON(t, conv, `{"op":"set_budget","args":{"credits":2,"quota_percent":50}}`, &row)
	setSession(m, caller.ID, func(s *webSession) { s.budget.Reached = true })
	uamRefused(t, conv, `{"op":"set_budget","args":{"credits":1,"quota_percent":10}}`, "only the owner")
	if b := budgetOf(m, other.ID); b != (store.Budget{}) {
		t.Fatalf("other thread's budget = %+v", b)
	}

	// A Thread put away can no longer act.
	if _, err := m.Settle(caller.ID); err != nil {
		t.Fatal(err)
	}
	res := m.uamCall(t.Context(), caller.ID, agentapi.HostToolCall{TaskID: caller.ID, Name: uamToolName, Arguments: []byte(`{"op":"rename","args":{"name":"again"}}`)})
	if !res.Failed || !strings.Contains(res.Text, "can no longer use uam tools") || summaryOf(t, m, caller.ID).Name != "Parser fix" {
		t.Fatalf("settled rename = %+v", res)
	}
}

// An agent pins and unpins the charts its own uam_chart calls drew; a chart
// with a command needs Yolo, and other Threads' charts are out of reach.
func TestUAMPinChartOwnThread(t *testing.T) {
	m, prov, _ := newTestManager(t)
	task, conv, _ := chartTask(t, m, prov, "safe")
	sibling, err := m.Create(CreateRequest{Provider: prov.Name(), ProjectID: task.ProjectID, Name: "sibling", Mode: "yolo"})
	if err != nil {
		t.Fatal(err)
	}
	siblingConv := prov.Last()
	data := `{"title":"Lines","kind":"bar","x":"ext","y":"lines","data":[{"ext":"go","lines":1200}]}`
	if res := chartCall(t, conv, "call-1", data); res.Failed || !strings.Contains(res.Text, `call_id for uam op pin_chart is "call-1"`) {
		t.Fatalf("uam_chart = %+v", res)
	}
	if res := chartCall(t, siblingConv, "call-2", data); res.Failed {
		t.Fatalf("sibling uam_chart = %+v", res)
	}

	var pin map[string]any
	uamReadJSON(t, conv, `{"op":"pin_chart","args":{"call_id":"call-1"}}`, &pin)
	if pin["pinned_id"] == "" || pin["project"] != task.ProjectID || pinCount(m, task.ProjectID) != 1 {
		t.Fatalf("pin = %+v, count %d", pin, pinCount(m, task.ProjectID))
	}
	uamRefused(t, conv, `{"op":"pin_chart","args":{}}`, "call_id is required")
	uamRefused(t, conv, `{"op":"pin_chart","args":{"call_id":"call-2"}}`, "chart not found")
	if _, err := m.PinChart(sibling.ID, "call-2"); err != nil {
		t.Fatal(err)
	}
	uamRefused(t, conv, `{"op":"unpin_chart","args":{"call_id":"call-2"}}`, "no chart of this thread")
	if n := pinCount(m, task.ProjectID); n != 2 {
		t.Fatalf("pins after a refused unpin = %d", n)
	}
	var out map[string]any
	uamReadJSON(t, conv, `{"op":"unpin_chart","args":{"call_id":"call-1"}}`, &out)
	if out["unpinned_id"] != pin["pinned_id"] || pinCount(m, task.ProjectID) != 1 {
		t.Fatalf("unpin = %+v, count %d", out, pinCount(m, task.ProjectID))
	}

	// A chart that runs a command, drawn in Yolo, cannot be pinned from Safe.
	setSession(m, task.ID, func(s *webSession) { s.mode = store.ModeYolo })
	if res := chartCall(t, conv, "call-3", `{"title":"Echo","kind":"bar","x":"a","y":"b","command":"printf 'a,b\\nx,1\\n'","format":"csv"}`); res.Failed {
		t.Fatalf("command chart = %+v", res)
	}
	setSession(m, task.ID, func(s *webSession) { s.mode = store.ModeSafe })
	uamRefused(t, conv, `{"op":"pin_chart","args":{"call_id":"call-3"}}`, "needs Yolo")
	setSession(m, task.ID, func(s *webSession) { s.mode = store.ModeYolo })
	uamReadJSON(t, conv, `{"op":"pin_chart","args":{"call_id":"call-3"}}`, &pin)
}

// An agent runs its Project's routines now, under uam_create_task's limits.
// An agent settles and archives only the Threads it started, through the
// same stage rules as the owner.
func TestUAMSettleAndArchiveStartedThreads(t *testing.T) {
	m, prov, _ := newTestManager(t)
	caller, conv := createSession(t, m, prov)
	newThread := func(name string) string {
		t.Helper()
		s, err := m.Create(CreateRequest{Provider: prov.Name(), ProjectID: caller.ProjectID, Name: name})
		if err != nil {
			t.Fatal(err)
		}
		return s.ID
	}
	child, stranger := newThread("child"), newThread("stranger")
	setSession(m, child, func(s *webSession) { s.spawnedBy = caller.ID })
	// A Thread another Thread started is no more the caller's.
	setSession(m, stranger, func(s *webSession) { s.spawnedBy = "someone-else" })

	for _, op := range []string{"settle", "archive"} {
		uamRefused(t, conv, fmt.Sprintf(`{"op":%q,"args":{}}`, op), "id is required")
		for _, id := range []string{stranger, caller.ID, "missing"} {
			uamRefused(t, conv, fmt.Sprintf(`{"op":%q,"args":{"id":%q}}`, op, id), "only threads you started")
		}
	}
	if got := summaryOf(t, m, stranger); got.Stage != StageActive {
		t.Fatalf("stranger = %+v", got)
	}
	// A busy child is refused as the owner's settle is.
	setSession(m, child, func(s *webSession) { s.base = StateWorking })
	uamRefused(t, conv, fmt.Sprintf(`{"op":"settle","args":{"id":%q}}`, child), "stop the turn first")
	if got := summaryOf(t, m, child); got.Stage != StageActive {
		t.Fatalf("busy child settled: %+v", got)
	}
	setSession(m, child, func(s *webSession) { s.base = StateCompleted })

	var row uamTaskRow
	uamReadJSON(t, conv, fmt.Sprintf(`{"op":"settle","args":{"id":%q}}`, child), &row)
	if row.ID != child || row.Stage != StageSettled {
		t.Fatalf("settle = %+v", row)
	}
	uamRefused(t, conv, fmt.Sprintf(`{"op":"settle","args":{"id":%q}}`, child), "cannot be settled")
	uamReadJSON(t, conv, fmt.Sprintf(`{"op":"archive","args":{"id":%q}}`, child), &row)
	if row.Stage != StageArchived {
		t.Fatalf("archive = %+v", row)
	}
	uamRefused(t, conv, fmt.Sprintf(`{"op":"archive","args":{"id":%q}}`, child), "cannot be archived")
}

// A started Thread with unsaved git work of its own is refused, and an agent
// cannot force past that.
func TestUAMSettleStartedThreadAsksFirstWithUnsavedWork(t *testing.T) {
	repo := sharedFolderWithOthersWork(t)
	g := newGitTask(t, repo)
	conv := g.prov.Last()
	child, err := g.m.Create(CreateRequest{Provider: g.prov.Name(), ProjectID: summaryOf(t, g.m, g.id).ProjectID, Name: "child"})
	if err != nil {
		t.Fatal(err)
	}
	setSession(g.m, child.ID, func(s *webSession) { s.spawnedBy = g.id })
	g.prov.Last().EmitItem(toolEdit("e1", "edit", filepath.Join(repo, "tracked.txt"), agentapi.ToolCompleted, time.Now()))

	for _, op := range []string{"settle", "archive"} {
		uamRefused(t, conv, fmt.Sprintf(`{"op":%q,"args":{"id":%q}}`, op, child.ID), "own changes (1 not committed) are not saved anywhere else. Files: tracked.txt.")
		if got := summaryOf(t, g.m, child.ID); got.Stage != StageActive {
			t.Fatalf("%s moved the child: %+v", op, got)
		}
	}
	uamRefused(t, conv, fmt.Sprintf(`{"op":"settle","args":{"id":%q,"force":true}}`, child.ID), "unknown field")
	if got := summaryOf(t, g.m, child.ID); got.Stage != StageActive {
		t.Fatalf("force moved the child: %+v", got)
	}
}
