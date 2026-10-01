package web

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

var assistCaps = func() agentapi.Capabilities { c := allCaps; c.Titles, c.HostTools = true, true; return c }()

// assistManager starts a manager whose provider "fake" pages its records
// and runs Utility calls on model "a".
func assistManager(t *testing.T, st *store.Store, prov *agenttest.Pager) (*Manager, string) {
	t.Helper()
	m := startManager(t, st, prov)
	if _, err := m.UpdateSettings(SettingsPatch{TitleModel: map[string]string{"fake": "a"}}); err != nil {
		t.Fatal(err)
	}
	return m, addProject(t, m, t.TempDir())
}

func newAssistProvider() *agenttest.Pager {
	prov := agenttest.NewPager("fake", assistCaps)
	prov.SetModels(selectionModels(), nil)
	return prov
}

// utilityCalls counts the Utility calls made for purpose.
func utilityCalls(prov *agenttest.Pager, purpose string) int {
	n := 0
	for _, r := range prov.UtilityRequests() {
		if r.Purpose == purpose {
			n++
		}
	}
	return n
}

func codeOf(n int) *int { return &n }

// finishTurn plays one completed turn: the user's message, tool calls and
// the assistant's reply.
func finishTurn(conv *agenttest.Conversation, user, reply string, tools ...agentapi.Item) {
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.EmitItem(agentapi.Item{ID: "u-" + user, Kind: agentapi.ItemUser, Text: user})
	for _, it := range tools {
		conv.EmitItem(it)
	}
	conv.EmitItem(agentapi.Item{ID: "a-" + user, Kind: agentapi.ItemAssistant, Text: reply})
	conv.EmitTurn(agentapi.TurnCompleted, "")
}

func TestSuggestedRepliesAreAskedForOncePerState(t *testing.T) {
	prov := newAssistProvider()
	st := openTestStore(t)
	m, project := assistManager(t, st, prov)
	release := make(chan struct{})
	prov.SetUtilityHook(func(_ context.Context, req agentapi.UtilityRequest) (string, error) {
		if req.Purpose != "suggest-replies" {
			return "", fmt.Errorf("no %s here", req.Purpose)
		}
		<-release
		if !strings.Contains(req.Prompt, "make the test pass") || !strings.Contains(req.Prompt, "Fixed it.") || req.Model != "a" {
			return "", fmt.Errorf("unexpected request %+v", req)
		}
		return "<think>hm</think>\n1. Run the full suite\n- Commit this\n\"run the full suite\"\nShow me the diff\nOne more", nil
	})
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Name: "t"})
	if err != nil {
		t.Fatal(err)
	}
	conv := prov.Last()

	// Nothing is suggested while the turn runs.
	conv.EmitTurn(agentapi.TurnWorking, "")
	if got, err := m.SuggestReplies(context.Background(), sum.ID); err != nil || got.ItemID != "" || len(got.Replies) != 0 {
		t.Fatalf("while working = %+v, %v", got, err)
	}
	finishTurn(conv, "make the test pass", "Fixed it.")

	// Two looks at once make one call.
	var wg sync.WaitGroup
	results := make([]Suggestions, 2)
	for i := range results {
		wg.Go(func() {
			got, err := m.SuggestReplies(context.Background(), sum.ID)
			if err != nil {
				t.Error(err)
			}
			results[i] = got
		})
	}
	waitUntil(t, "the suggestion call", func() bool { return utilityCalls(prov, "suggest-replies") == 1 })
	close(release)
	wg.Wait()
	want := []string{"Run the full suite", "Commit this", "Show me the diff"}
	for _, got := range results {
		if got.ItemID != "a-make the test pass" || !slices.Equal(got.Replies, want) {
			t.Fatalf("suggestions = %+v", got)
		}
	}
	if got, _ := m.SuggestReplies(context.Background(), sum.ID); !slices.Equal(got.Replies, want) || utilityCalls(prov, "suggest-replies") != 1 {
		t.Fatalf("a second look = %+v after %d calls", got, utilityCalls(prov, "suggest-replies"))
	}

	// They survive a restart without another call.
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	prov.SetHistory(conv.ID(), agentapi.History{Items: []agentapi.Item{
		{ID: "u-make the test pass", Kind: agentapi.ItemUser, Text: "make the test pass"},
		{ID: "a-make the test pass", Kind: agentapi.ItemAssistant, Text: "Fixed it."},
	}})
	m2 := startManager(t, st, prov)
	if _, err := m2.Detail(sum.ID); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the recorded history", func() bool { d, _ := m2.Detail(sum.ID); return d.History == HistoryLoaded })
	if got, _ := m2.SuggestReplies(context.Background(), sum.ID); !slices.Equal(got.Replies, want) || utilityCalls(prov, "suggest-replies") != 1 {
		t.Fatalf("after a restart = %+v after %d calls", got, utilityCalls(prov, "suggest-replies"))
	}

	// Turned off, nothing is suggested.
	off := false
	if _, err := m2.UpdateSettings(SettingsPatch{SuggestReplies: &off}); err != nil {
		t.Fatal(err)
	}
	if got, _ := m2.SuggestReplies(context.Background(), sum.ID); got.ItemID != "" || len(got.Replies) != 0 {
		t.Fatalf("turned off = %+v", got)
	}
	if m2.Settings().suggestReplies() {
		t.Fatal("the setting stayed on")
	}
}

func TestOutcomeLineClaimsOnlyWhatTheEvidenceShows(t *testing.T) {
	prov := newAssistProvider()
	m, project := assistManager(t, openTestStore(t), prov)
	verb := make(chan string, 1)
	prov.SetUtilityHook(func(_ context.Context, req agentapi.UtilityRequest) (string, error) {
		if req.Purpose != "outcome" {
			return "", fmt.Errorf("no %s here", req.Purpose)
		}
		return <-verb, nil
	})
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Name: "t"})
	if err != nil {
		t.Fatal(err)
	}
	conv := prov.Last()
	tool := func(id, name, input string, status agentapi.ToolStatus, code *int) agentapi.Item {
		return agentapi.Item{ID: id, Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: name, Input: input, Status: status, ExitCode: code}}
	}
	finishTurn(conv, "fix the flaky test", "Fixed the flaky test.",
		tool("t1", "edit", `{"path":"a.go"}`, agentapi.ToolCompleted, nil),
		tool("t2", "create", `{"path":"b_test.go"}`, agentapi.ToolCompleted, nil),
		tool("t3", "edit", `{"path":"a.go"}`, agentapi.ToolCompleted, nil),
		tool("t4", "edit", `{"path":"c.go"}`, agentapi.ToolFailed, nil),
		tool("t5", "bash", `{"command":"go test ./..."}`, agentapi.ToolCompleted, codeOf(1)),
		tool("t6", "bash", `{"command":"grep -r nothing ."}`, agentapi.ToolCompleted, codeOf(1)),
		tool("t7", "bash", `{"command":"cd x && go test ./pkg"}`, agentapi.ToolCompleted, codeOf(0)),
	)
	if got := summaryOf(t, m, sum.ID).Outcome; got != "2 files changed; tests pass; 2 commands failed" {
		t.Fatalf("outcome before the verb = %q", got)
	}
	verb <- "Fixed the flaky test."
	waitUntil(t, "the verb phrase", func() bool {
		return summaryOf(t, m, sum.ID).Outcome == "Fixed the flaky test; 2 files changed; tests pass; 2 commands failed"
	})

	// A new turn clears it; one without tools or a model says only the verb.
	conv.EmitTurn(agentapi.TurnWorking, "")
	if got := summaryOf(t, m, sum.ID).Outcome; got != "" {
		t.Fatalf("outcome while working = %q", got)
	}
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.EmitItem(agentapi.Item{ID: "u-why", Kind: agentapi.ItemUser, Text: "why"})
	conv.EmitItem(agentapi.Item{ID: "a-why", Kind: agentapi.ItemAssistant, Text: "Because."})
	// Copilot can record an empty thought after the answer.
	conv.EmitItem(agentapi.Item{ID: "r-why", Kind: agentapi.ItemReasoning})
	conv.EmitTurn(agentapi.TurnCompleted, "")
	if got := summaryOf(t, m, sum.ID).Outcome; got != "" {
		t.Fatalf("outcome before the verb = %q", got)
	}
	verb <- "Explained the cause"
	waitUntil(t, "the second verb phrase", func() bool { return summaryOf(t, m, sum.ID).Outcome == "Explained the cause" })
	if n := utilityCalls(prov, "outcome"); n != 2 {
		t.Fatalf("outcome calls = %d", n)
	}
}

// The outcome line reads a turn as the finish card does (web/src/lib/
// evidence.ts), so the two never disagree.
func TestOutcomeEvidenceMatchesTheFinishCard(t *testing.T) {
	tool := func(name, input string, status agentapi.ToolStatus, code *int, agent string) agentapi.Item {
		return agentapi.Item{Kind: agentapi.ItemTool, AgentID: agent, Tool: &agentapi.ToolCall{Name: name, Input: input, Status: status, ExitCode: code}}
	}
	cmd := func(command string, code int) agentapi.Item {
		return tool("bash", fmt.Sprintf(`{"command":%q}`, command), agentapi.ToolCompleted, codeOf(code), "")
	}
	for _, tc := range []struct {
		name  string
		items []agentapi.Item
		want  string
	}{
		{"a piped test run is unclear", []agentapi.Item{cmd("go test ./... | tail -5", 0)}, ""},
		{"pipefail keeps its status", []agentapi.Item{cmd("set -o pipefail; go test ./... | tail -5", 0)}, "tests pass"},
		{"the first check names the command", []agentapi.Item{cmd("go vet ./... && go test ./...", 0)}, ""},
		{"wrappers are skipped", []agentapi.Item{cmd("CI=1 timeout 60 npm test", 1)}, "tests fail; 1 command failed"},
		{"the last test run decides", []agentapi.Item{cmd("pytest", 1), cmd("python -m pytest -q", 0)}, "tests pass; 1 command failed"},
		{"subagents are not the turn's evidence", []agentapi.Item{
			tool("bash", `{"command":"go test ./..."}`, agentapi.ToolCompleted, codeOf(0), "agent-1"),
			tool("edit", `{"path":"/repo/x.go"}`, agentapi.ToolCompleted, nil, "agent-1"),
		}, ""},
		{"paths count once, relative to the folder", []agentapi.Item{
			tool("edit", `{"path":"/repo/a.go"}`, agentapi.ToolCompleted, nil, ""),
			tool("write", `{"path":"a.go"}`, agentapi.ToolCompleted, nil, ""),
			tool("write", `{"path":"/repo/b.go"}`, agentapi.ToolCompleted, nil, ""),
		}, "2 files changed"},
	} {
		s := &webSession{workdir: "/repo", items: append([]agentapi.Item{{Kind: agentapi.ItemUser, Text: "go"}}, tc.items...)}
		ev, ok := s.turnEvidence()
		if got := outcomeLine("", ev); !ok || !strings.EqualFold(got, tc.want) {
			t.Errorf("%s: outcome = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestRerunStartsALinkedTaskWithTheLastMessage(t *testing.T) {
	prov := newAssistProvider()
	m, project := assistManager(t, openTestStore(t), prov)
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Name: "t", Model: "a", Effort: "high", Mode: "yolo"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Rerun(sum.ID, RerunRequest{}); statusOf(err) != 409 {
		t.Fatalf("rerun without a message = %v", err)
	}
	finishTurn(prov.Last(), "first", "one")
	finishTurn(prov.Last(), "second", "two")

	again, err := m.Rerun(sum.ID, RerunRequest{RequestID: mustUUID(t)})
	if err != nil {
		t.Fatal(err)
	}
	if again.RerunOf != sum.ID || again.Model != "a" || again.Effort != "high" || again.Mode != "yolo" || again.ProjectID != project {
		t.Fatalf("run again = %+v", again)
	}
	if sends := prov.Last().Sends(); !slices.Equal(sends, []string{"second"}) {
		t.Fatalf("run again sent %q", sends)
	}
	other, err := m.Rerun(sum.ID, RerunRequest{Model: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if other.RerunOf != sum.ID || other.Model != "b" || other.Effort != "" || other.Mode != "yolo" {
		t.Fatalf("another model = %+v", other)
	}
}

func TestExportHoldsTheWholeRecord(t *testing.T) {
	prov := newAssistProvider()
	m, project := assistManager(t, openTestStore(t), prov)
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Name: "Fix: the flaky test!"})
	if err != nil {
		t.Fatal(err)
	}
	conv := prov.Last()
	// The record holds more than one read's worth; the service holds only the live tail.
	var record []agentapi.Item
	for i := range 2500 {
		record = append(record, agentapi.Item{ID: fmt.Sprintf("r%d", i), Kind: agentapi.ItemAssistant, Text: fmt.Sprintf("message %d", i)})
	}
	record = append(record,
		agentapi.Item{ID: "cmd", Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "bash", Status: agentapi.ToolCompleted, Input: `{"command":"go test ./... <x>"}`, Output: "ok ```", ExitCode: codeOf(0)}},
		agentapi.Item{ID: "sub", Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "task", Status: agentapi.ToolCompleted, Input: `{"description":"survey"}`, Output: "found three"}},
	)
	prov.SetHistory(conv.ID(), agentapi.History{Items: record})
	conv.EmitItem(record[len(record)-1])
	conv.EmitItem(agentapi.Item{ID: "live", Kind: agentapi.ItemUser, Text: "and the newest message"})

	body, name, err := m.ExportMarkdown(context.Background(), sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(body)
	if name != "fix-the-flaky-test.md" {
		t.Fatalf("file name = %q", name)
	}
	for _, want := range []string{
		"# Fix: the flaky test!\n", "message 0\n", "message 2499\n",
		"<details><summary>Ran <code>go test ./... &lt;x&gt;</code> · exit 0</summary>", "````text\nok ```\n````",
		"found three", "## You · ", "and the newest message",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("export lacks %q:\n%s", want, doc[max(0, len(doc)-1500):])
		}
	}
	if strings.Count(doc, "message 1234\n") != 1 || len(prov.WindowReads()) < 3 {
		t.Fatalf("export repeated items or read %d windows", len(prov.WindowReads()))
	}
}

func TestSavedPrompts(t *testing.T) {
	prov := newAssistProvider()
	st := openTestStore(t)
	m, project := assistManager(t, st, prov)
	global, err := m.AddPrompt(AddPromptRequest{Name: " Review ", Text: "Review the diff\nfor bugs"})
	if err != nil || global.Name != "Review" {
		t.Fatalf("add = %+v, %v", global, err)
	}
	scoped, err := m.AddPrompt(AddPromptRequest{Name: "Ship", Text: "Commit and push", ProjectID: project})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []AddPromptRequest{{Name: "", Text: "x"}, {Name: "x", Text: "  "}, {Name: "x", Text: "a\x1b"}, {Name: "x", Text: "y", ProjectID: "nope"}} {
		if _, err := m.AddPrompt(bad); statusOf(err) != 400 {
			t.Fatalf("add %+v = %v", bad, err)
		}
	}
	if _, err := m.RenamePrompt(global.ID, "Careful review"); err != nil {
		t.Fatal(err)
	}
	if err := m.DeletePrompt(scoped.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.DeletePrompt(scoped.ID); statusOf(err) != 404 {
		t.Fatalf("delete twice = %v", err)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	m2 := startManager(t, st, prov)
	got := m2.Settings().SavedPrompts
	if len(got) != 1 || got[0].ID != global.ID || got[0].Name != "Careful review" || got[0].Text != "Review the diff\nfor bugs" {
		t.Fatalf("saved prompts after a restart = %+v", got)
	}
}

func TestAssistPastTheUtilityLimit(t *testing.T) {
	prov := newAssistProvider()
	m, project := assistManager(t, openTestStore(t), prov)
	prov.SetUtilityHook(func(_ context.Context, req agentapi.UtilityRequest) (string, error) {
		if req.Purpose == "outcome" {
			return "Fixed it", nil
		}
		return "Commit this", nil
	})
	zero := 0
	limit := &zero
	if _, err := m.UpdateSettings(SettingsPatch{UtilityLimit: &limit}); err != nil {
		t.Fatal(err)
	}
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Name: "t"})
	if err != nil {
		t.Fatal(err)
	}
	finishTurn(prov.Last(), "fix", "Fixed.", agentapi.Item{ID: "e", Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "edit", Input: `{"path":"a.go"}`, Status: agentapi.ToolCompleted}})
	if got, err := m.SuggestReplies(context.Background(), sum.ID); err != nil || got.ItemID != "" || len(got.Replies) != 0 {
		t.Fatalf("paused suggestions = %+v, %v", got, err)
	}
	waitUntil(t, "the skipped calls in the log", func() bool { return len(m.UtilityLog(0, 10).Calls) == 2 })
	if got := summaryOf(t, m, sum.ID).Outcome; got != "1 file changed" || len(prov.UtilityRequests()) != 0 {
		t.Fatalf("paused outcome = %q after %d calls", got, len(prov.UtilityRequests()))
	}
	// Once calls may run again, a look asks for them: nothing was kept.
	var unset *int
	if _, err := m.UpdateSettings(SettingsPatch{UtilityLimit: &unset}); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.SuggestReplies(context.Background(), sum.ID); !slices.Equal(got.Replies, []string{"Commit this"}) {
		t.Fatalf("suggestions after the limit = %+v", got)
	}
}
