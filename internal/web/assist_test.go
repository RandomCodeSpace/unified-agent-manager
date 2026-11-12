package web

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	uamlog "github.com/RandomCodeSpace/unified-agent-manager/internal/log"
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

// utilityCalls counts the Utility calls made for purpose, and the decisions
// logged as it.
func utilityCalls(prov *agenttest.Pager, purpose string) int {
	n := 0
	for _, r := range prov.UtilityRequests() {
		if r.Purpose == purpose {
			n++
		}
	}
	for _, r := range prov.DecisionRequests() {
		if "decision-"+r.Purpose == purpose {
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
		if req.Purpose != purposeSuggestReplies {
			return "", fmt.Errorf("no %s here", req.Purpose)
		}
		<-release
		if !strings.Contains(req.Prompt, "make the test pass") || !strings.Contains(req.Prompt, "Fixed it. Shall I commit it?") {
			return "", fmt.Errorf("unexpected request %+v", req)
		}
		return `Sure: {"reply":"Yes, commit it"}`, nil
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
	finishTurn(conv, "make the test pass", "Fixed it. Shall I commit it?")

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
	waitUntil(t, "the suggestion call", func() bool { return utilityCalls(prov, purposeSuggestReplies) == 1 })
	close(release)
	wg.Wait()
	// The reply is shared by concurrent callers.
	want := []string{"Yes, commit it"}
	for _, got := range results {
		if got.ItemID != "a-make the test pass" || !slices.Equal(got.Replies, want) {
			t.Fatalf("suggestions = %+v", got)
		}
	}
	if got, _ := m.SuggestReplies(context.Background(), sum.ID); !slices.Equal(got.Replies, want) || utilityCalls(prov, purposeSuggestReplies) != 1 {
		t.Fatalf("a second look = %+v after %d calls", got, utilityCalls(prov, purposeSuggestReplies))
	}

	// They survive a restart without another call.
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	prov.SetHistory(conv.ID(), agentapi.History{Items: []agentapi.Item{
		{ID: "u-make the test pass", Kind: agentapi.ItemUser, Text: "make the test pass"},
		{ID: "a-make the test pass", Kind: agentapi.ItemAssistant, Text: "Fixed it. Shall I commit it?"},
	}})
	m2 := startManager(t, st, prov)
	if _, err := m2.Detail(sum.ID); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the recorded history", func() bool { d, _ := m2.Detail(sum.ID); return d.History == HistoryLoaded })
	if got, _ := m2.SuggestReplies(context.Background(), sum.ID); !slices.Equal(got.Replies, want) || utilityCalls(prov, purposeSuggestReplies) != 1 {
		t.Fatalf("after a restart = %+v after %d calls", got, utilityCalls(prov, purposeSuggestReplies))
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

func TestSuggestionPromptQuotesOnlyLastUserAndFinalReply(t *testing.T) {
	prov := newAssistProvider()
	m, project := assistManager(t, openTestStore(t), prov)
	suggests(prov, "Commit it")
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Name: "t"})
	if err != nil {
		t.Fatal(err)
	}
	conv := prov.Last()
	finishTurn(conv, "earlier", "Done earlier.")
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.EmitItem(agentapi.Item{ID: "u1", Kind: agentapi.ItemUser, Text: "fix the build"})
	conv.EmitItem(agentapi.Item{ID: "r1", Kind: agentapi.ItemReasoning, Text: "secret thought"})
	conv.EmitItem(agentapi.Item{ID: "a1", Kind: agentapi.ItemAssistant, Text: "Running the tests."})
	conv.EmitItem(agentapi.Item{ID: "t1", Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "bash", Status: agentapi.ToolCompleted, Input: `{"command":"go test ./..."}`, Output: strings.Repeat("x", 15000) + "Run the full test suite", ExitCode: codeOf(1)}})
	conv.EmitItem(agentapi.Item{ID: "u2", Kind: agentapi.ItemUser, Text: "also lint", Delivery: agentapi.DeliverySteer})
	conv.EmitItem(agentapi.Item{ID: "s1", Kind: agentapi.ItemUser, AgentID: "agent-1", Text: "subagent request"})
	conv.EmitItem(agentapi.Item{ID: "a2", Kind: agentapi.ItemAssistant, Text: "Fixed and linted. Want a commit?"})
	conv.EmitTurn(agentapi.TurnCompleted, "")

	got, err := m.SuggestReplies(context.Background(), sum.ID)
	if err != nil || !slices.Equal(got.Replies, []string{"Commit it"}) {
		t.Fatalf("suggestion = %+v, %v", got, err)
	}
	want := "<user_message>\nalso lint\n</user_message>\n<agent_reply>\nFixed and linted. Want a commit?\n</agent_reply>"
	for _, req := range prov.UtilityRequests() {
		if req.Prompt != want {
			t.Errorf("prompt included more than the last user message and final reply: %q", req.Prompt)
		}
		if req.System != suggestRepliesSystem || req.ResponseSchema == nil || req.Model != "a" {
			t.Errorf("request = %+v", req)
		}
	}
	if n := utilityCalls(prov, purposeSuggestReplies); n != 1 {
		t.Errorf("suggestion calls = %d, want 1", n)
	}
}

// suggests plays the Utility model for suggestion calls: it writes reply.
func suggests(prov *agenttest.Pager, reply string) {
	prov.SetUtilityHook(func(_ context.Context, req agentapi.UtilityRequest) (string, error) {
		if req.Purpose != purposeSuggestReplies {
			return "", fmt.Errorf("no %s here", req.Purpose)
		}
		return fmt.Sprintf(`{"reply":%q}`, reply), nil
	})
}

// A reply is one line of at most 120 characters, or none; anything else is
// an unusable answer.
func TestSuggestionAnswerContract(t *testing.T) {
	long := strings.Repeat("界", maxSuggestedReplyRunes)
	for name, tc := range map[string]struct {
		reply, want string
		invalid     bool
	}{
		"a reply":          {reply: `{"reply":"Run the tests"}`, want: "Run the tests"},
		"none":             {reply: `{"reply":""}`},
		"null":             {reply: `{"reply":null}`},
		"fenced":           {reply: "```json\n{\"reply\":\"Push it\"}\n```", want: "Push it"},
		"made one line":    {reply: `{"reply":"  Yes,\n go\tahead \u001b[31m "}`, want: "Yes, go ahead"},
		"at the limit":     {reply: `{"reply":"` + long + `"}`, want: long},
		"too long":         {reply: `{"reply":"` + long + `x"}`, invalid: true},
		"not an object":    {reply: "Run the tests", invalid: true},
		"another field":    {reply: `{"reply":"Run the tests","why":"x"}`, invalid: true},
		"not valid UTF-8":  {reply: "{\"reply\":\"\xff\"}", invalid: true},
		"a second answer":  {reply: `{"reply":"a"} {"reply":"b"}`, invalid: true},
		"a refusal":        {reply: "I cannot help with that.", invalid: true},
		"reasoning before": {reply: `<think>{"reply":"no"}</think>{"reply":"Yes"}`, want: "Yes"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := parseSuggestion(tc.reply)
			if got != tc.want || (err != nil) != tc.invalid || err != nil && !errors.Is(err, errInvalidSuggestion) {
				t.Fatalf("parseSuggestion = %q, %v", got, err)
			}
		})
	}
}

// Without a Utility model nothing is asked or kept: a suggestion never runs
// on the Thread's own model. Choosing one asks.
func TestSuggestionNeedsAUtilityModel(t *testing.T) {
	prov := newAssistProvider()
	m, project := assistManager(t, openTestStore(t), prov)
	suggests(prov, "Run the tests")
	if _, err := m.UpdateSettings(SettingsPatch{TitleModel: map[string]string{"fake": store.WebTitleModelNone}}); err != nil {
		t.Fatal(err)
	}
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Name: "t"})
	if err != nil {
		t.Fatal(err)
	}
	finishTurn(prov.Last(), "fix it", "Fixed parse.go. I have not run the tests.")
	if got, err := m.SuggestReplies(context.Background(), sum.ID); err != nil || got.ItemID != "" || len(got.Replies) != 0 || len(prov.UtilityRequests())+len(m.UtilityLog(0, 10).Calls) != 0 {
		t.Fatalf("without a Utility model = %+v, %v after %d calls", got, err, len(prov.UtilityRequests()))
	}
	if _, err := m.UpdateSettings(SettingsPatch{TitleModel: map[string]string{"fake": "a"}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.SuggestReplies(context.Background(), sum.ID); !slices.Equal(got.Replies, []string{"Run the tests"}) {
		t.Fatalf("with a Utility model = %+v", got)
	}
}

// When the model writes none, or an unusable answer, none shows, and the
// turn is not asked again.
func TestSuggestionNoneShowsNothingAndIsKept(t *testing.T) {
	for reply, valid := range map[string]bool{"": true, strings.Repeat("x", maxSuggestedReplyRunes+1): false} {
		t.Run(fmt.Sprint(valid), func(t *testing.T) { suggestionNone(t, reply, valid) })
	}
}

func suggestionNone(t *testing.T, reply string, valid bool) {
	prov := newAssistProvider()
	m, project := assistManager(t, openTestStore(t), prov)
	suggests(prov, reply)
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Name: "t"})
	if err != nil {
		t.Fatal(err)
	}
	finishTurn(prov.Last(), "delete it", "Should I delete hello.txt?")
	for range 2 {
		if got, err := m.SuggestReplies(context.Background(), sum.ID); err != nil || got.ItemID == "" || len(got.Replies) != 0 {
			t.Fatalf("suggestion = %+v, %v", got, err)
		}
	}
	calls := m.UtilityLog(0, 10).Calls
	if n := utilityCalls(prov, purposeSuggestReplies); n != 1 || len(calls) != 1 || calls[0].TaskID != sum.ID || calls[0].Purpose != purposeSuggestReplies || calls[0].Model != "a" || calls[0].Answer != "" || calls[0].Valid == nil || *calls[0].Valid != valid {
		t.Fatalf("%d calls, log %+v", n, calls)
	}
}

func TestSuggestionPromptBoundsEachMessage(t *testing.T) {
	for _, fill := range []string{"x", "界"} {
		for _, size := range []int{2000, 2001, 15000} {
			t.Run(fmt.Sprintf("%s/%d", fill, size), func(t *testing.T) {
				user := "REQUEST" + strings.Repeat(fill, size-14) + "REQUEST"
				reply := "REPLY" + strings.Repeat(fill, size-10) + "REPLY"
				wantUser, wantReply := user, reply
				if size > 2000 {
					wantUser = "REQUEST" + strings.Repeat(fill, 993) + "\n…\n" + strings.Repeat(fill, 993) + "REQUEST"
					wantReply = "REPLY" + strings.Repeat(fill, 995) + "\n…\n" + strings.Repeat(fill, 995) + "REPLY"
				}
				s := &webSession{items: []agentapi.Item{
					{Kind: agentapi.ItemUser, Text: user},
					{Kind: agentapi.ItemAssistant, Text: reply},
				}}
				got := s.suggestionPrompt(1)
				want := "<user_message>\n" + wantUser + "\n</user_message>\n<agent_reply>\n" + wantReply + "\n</agent_reply>"
				if got != want {
					t.Fatalf("prompt did not preserve the first and last 1000 characters of each oversized message: got %d runes, want %d", utf8.RuneCountInString(got), utf8.RuneCountInString(want))
				}
			})
		}
	}
}

func TestOutcomeLineClaimsOnlyWhatTheEvidenceShows(t *testing.T) {
	prov := newAssistProvider()
	m, project := assistManager(t, openTestStore(t), prov)
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
		t.Fatalf("outcome of an edit turn = %q", got)
	}

	// A new turn clears it; one without edits or commands reads "Answered".
	conv.EmitTurn(agentapi.TurnWorking, "")
	if got := summaryOf(t, m, sum.ID).Outcome; got != "" {
		t.Fatalf("outcome while working = %q", got)
	}
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.EmitItem(agentapi.Item{ID: "u-why", Kind: agentapi.ItemUser, Text: "why"})
	conv.EmitItem(agentapi.Item{ID: "a-why", Kind: agentapi.ItemAssistant, Text: "The cause is a stale cache."})
	// Copilot can record an empty thought after the answer.
	conv.EmitItem(agentapi.Item{ID: "r-why", Kind: agentapi.ItemReasoning})
	conv.EmitTurn(agentapi.TurnCompleted, "")
	if got := summaryOf(t, m, sum.ID).Outcome; got != "Answered" {
		t.Fatalf("outcome of a turn without tools = %q", got)
	}
	if n := len(prov.UtilityRequests()); n != 0 {
		t.Fatalf("outcome made %d Utility calls", n)
	}
}

// Only a turn without edits or commands gets a phrase, "Answered"; a shell
// turn reads its evidence alone.
func TestOutcomeAnswersOnlyTurnsWithoutEditsOrCommands(t *testing.T) {
	prov := newAssistProvider()
	m, project := assistManager(t, openTestStore(t), prov)
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Name: "t"})
	if err != nil {
		t.Fatal(err)
	}
	conv := prov.Last()
	finishTurn(conv, "what is 2+2", "4.",
		agentapi.Item{ID: "r1", Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "view", Input: `{"path":"a.go"}`, Status: agentapi.ToolCompleted}})
	if got := summaryOf(t, m, sum.ID).Outcome; got != "Answered" {
		t.Fatalf("outcome of a read-only turn = %q", got)
	}
	finishTurn(conv, "list files", "Two files.",
		agentapi.Item{ID: "c1", Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "bash", Input: `{"command":"ls"}`, Status: agentapi.ToolCompleted, ExitCode: codeOf(0)}})
	if got := summaryOf(t, m, sum.ID).Outcome; got != "" {
		t.Fatalf("outcome of a shell turn = %q", got)
	}
	if n := len(prov.UtilityRequests()); n != 0 {
		t.Fatalf("outcome made %d Utility calls", n)
	}
}

// The outcome line reads the turn evidence the finish card shows
// (turn_evidence.go), so the two never disagree.
func TestOutcomeLineReadsTheTurnEvidence(t *testing.T) {
	type step func(e *evidenceTurn)
	cmd := func(command string, exit int) step {
		return func(e *evidenceTurn) { e.bash(fmt.Sprintf("c%d", len(e.s.items)), command, exit, "") }
	}
	edit := func(tool, path, agent string) step {
		return func(e *evidenceTurn) {
			e.note(agentapi.Item{ID: fmt.Sprintf("e%d", len(e.s.items)), Kind: agentapi.ItemTool, AgentID: agent, Tool: &agentapi.ToolCall{Name: tool, Status: agentapi.ToolCompleted, Input: fmt.Sprintf(`{"path":%q}`, path)}})
		}
	}
	for _, tc := range []struct {
		name  string
		steps []step
		want  string
	}{
		{"a piped test run is unclear", []step{cmd("go test ./... | tail -5", 0)}, ""},
		{"pipefail keeps its status", []step{cmd("set -o pipefail; go test ./... | tail -5", 0)}, "tests pass"},
		{"a compound line counts its test", []step{cmd("go vet ./... && go test ./...", 0)}, "tests pass"},
		{"a later command's status is not the test's", []step{cmd("go test ./... || true", 0)}, ""},
		{"wrappers are skipped", []step{cmd("CI=1 timeout 60 npm test", 1)}, "tests fail; 1 command failed"},
		{"the last test run decides", []step{cmd("pytest", 1), cmd("python -m pytest -q", 0)}, "tests pass; 1 command failed"},
		{"a subagent's commands are not the turn's, its edits are", []step{
			func(e *evidenceTurn) {
				e.note(agentapi.Item{ID: "s1", Kind: agentapi.ItemTool, AgentID: "agent-1", Tool: &agentapi.ToolCall{Name: "bash", Status: agentapi.ToolCompleted, Input: `{"command":"go test ./..."}`, ExitCode: codeOf(0)}})
			},
			edit("edit", "/repo/x.go", "agent-1"),
		}, "1 file changed"},
		{"paths count once, relative to the folder", []step{
			edit("edit", "/repo/a.go", ""), edit("write", "a.go", ""), edit("write", "/repo/b.go", ""),
		}, "2 files changed"},
	} {
		e := newEvidenceTurn()
		for _, st := range tc.steps {
			st(e)
		}
		if got := outcomeLine("", e.facts(t).outcome("/repo")); !strings.EqualFold(got, tc.want) {
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
	// In autopilot the CLI continues the turn with a message of its own.
	prov.Last().EmitItem(agentapi.Item{ID: "auto", Kind: agentapi.ItemUser, Text: "Continue working on the task.", Delivery: agentapi.DeliveryAutopilot})

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

func TestAssistPastTheUtilityLimit(t *testing.T) {
	prov := newAssistProvider()
	m, project := assistManager(t, openTestStore(t), prov)
	suggests(prov, "Run them")
	zero := 0
	limit := &zero
	if _, err := m.UpdateSettings(SettingsPatch{UtilityLimit: &limit}); err != nil {
		t.Fatal(err)
	}
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Name: "t"})
	if err != nil {
		t.Fatal(err)
	}
	finishTurn(prov.Last(), "fix", "Fixed. Run the tests?", agentapi.Item{ID: "e", Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "edit", Input: `{"path":"a.go"}`, Status: agentapi.ToolCompleted}})
	if got, err := m.SuggestReplies(context.Background(), sum.ID); err != nil || got.ItemID != "" || len(got.Replies) != 0 {
		t.Fatalf("paused suggestions = %+v, %v", got, err)
	}
	if n := len(m.UtilityLog(0, 10).Calls); n != 1 {
		t.Fatalf("%d skipped calls in the log, want the suggestion only", n)
	}
	if got := summaryOf(t, m, sum.ID).Outcome; got != "1 file changed" || len(prov.UtilityRequests()) != 0 {
		t.Fatalf("outcome = %q after %d calls", got, len(prov.UtilityRequests()))
	}
	// Once calls may run again, a look asks for them: nothing was kept.
	var unset *int
	if _, err := m.UpdateSettings(SettingsPatch{UtilityLimit: &unset}); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.SuggestReplies(context.Background(), sum.ID); !slices.Equal(got.Replies, []string{"Run them"}) {
		t.Fatalf("suggestions after the limit = %+v", got)
	}
}

// A pause is logged once: every look at a Thread asks again while it lasts,
// and only the first is a line. Calls allowed again end it.
func TestPausedSuggestionsAreLoggedOnce(t *testing.T) {
	logs := &lockedBuffer{}
	previous := uamlog.SetLogger(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { uamlog.SetLogger(previous) })
	prov := newAssistProvider()
	m, project := assistManager(t, openTestStore(t), prov)
	suggests(prov, "Yes")
	zero := 0
	limit := &zero
	if _, err := m.UpdateSettings(SettingsPatch{UtilityLimit: &limit}); err != nil {
		t.Fatal(err)
	}
	var tasks []string
	var convs []*agenttest.Conversation
	for range 2 {
		sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Name: "t"})
		if err != nil {
			t.Fatal(err)
		}
		tasks = append(tasks, sum.ID)
		convs = append(convs, prov.Last())
	}
	turn := func(user string) {
		for _, conv := range convs {
			finishTurn(conv, user, "Fixed. Shall I commit?")
		}
	}
	look := func() {
		for _, id := range tasks {
			if _, err := m.SuggestReplies(context.Background(), id); err != nil {
				t.Fatal(err)
			}
		}
	}
	count := func(level string) int {
		return strings.Count(logs.String(), "level="+level+` msg="no reply suggested"`)
	}
	turn("fix")
	look()
	look()
	if info, debug := count("INFO"), count("DEBUG"); info != 1 || debug != 3 {
		t.Fatalf("four paused looks logged %d at info and %d at debug, want 1 and 3", info, debug)
	}
	var unset *int
	if _, err := m.UpdateSettings(SettingsPatch{UtilityLimit: &unset}); err != nil {
		t.Fatal(err)
	}
	look()
	if _, err := m.UpdateSettings(SettingsPatch{UtilityLimit: &limit}); err != nil {
		t.Fatal(err)
	}
	turn("fix again")
	look()
	if info := count("INFO"); info != 2 {
		t.Fatalf("the second pause brought the info lines to %d, want 2", info)
	}
}
