package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/board"
)

var utilityCaps = func() agentapi.Capabilities { c := allCaps; c.Titles, c.HostTools = true, true; return c }()

// newUtilityPlanner is newPlanner with a provider that runs Utility jobs,
// on the Utility model Settings name.
func newUtilityPlanner(t *testing.T) *plannerFixture {
	t.Helper()
	prov := agenttest.NewProvider("fake", utilityCaps)
	prov.SetModels(selectionModels(), nil)
	m := startManager(t, openTestStore(t), prov)
	srv, err := NewServer(ServerConfig{Manager: m, Token: testToken, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	repo := branchRepo(t)
	f := &plannerFixture{t: t, ts: &testServer{srv: srv, m: m, prov: prov}, m: m, repo: repo, project: addProject(t, m, repo)}
	f.call(http.MethodPatch, "/api/settings", `{"planner":true,"title_model":{"fake":"b"}}`, http.StatusOK, nil)
	return f
}

// utilityModel is the Utility model the fixture's Settings name.
func (f *plannerFixture) utilityModel() string { return f.m.Settings().TitleModel[f.ts.prov.Name()] }

func (f *plannerFixture) commit(msg string) string {
	f.t.Helper()
	gitIn(f.t, f.repo, "add", "-A")
	gitIn(f.t, f.repo, "commit", "-q", "--allow-empty", "-m", msg)
	return gitOutput(f.t, f.repo, "rev-parse", "HEAD")
}

func (f *plannerFixture) snapshot() BoardSnapshot {
	f.t.Helper()
	var snap BoardSnapshot
	f.call(http.MethodGet, "/api/board?project_id="+f.project, "", http.StatusOK, &snap)
	return snap
}

func acceptOf(t *testing.T, r BoardRequest) AcceptResult {
	t.Helper()
	var ev Evidence
	if err := json.Unmarshal(r.Evidence, &ev); err != nil || ev.Accept == nil {
		t.Fatalf("evidence %s: %v", r.Evidence, err)
	}
	return *ev.Accept
}

// Check at HEAD runs the subtask's resolved command through the Project's
// runner: green and red are results, a runner busy past the timeout
// refuses, and nothing is recorded.
func TestCheckAtHead(t *testing.T) {
	acceptEnv(t)
	f := newPlanner(t)
	story := f.create(board.KindStory, "", "Story")
	leaf := f.create(board.KindSubtask, story.ID, "Leaf")
	check := func(ref string) AcceptResult {
		t.Helper()
		var reply struct {
			Accept AcceptResult `json:"accept"`
		}
		f.call(http.MethodPost, "/api/board/cards/"+ref+"/check", "", http.StatusOK, &reply)
		return reply.Accept
	}
	f.refused(http.MethodPost, "/api/board/cards/"+leaf.ID+"/check", "", http.StatusBadRequest, string(board.CodeInvalid))
	f.call(http.MethodPatch, "/api/board/projects/"+f.project, `{"accept_cmd":"echo green"}`, http.StatusOK, nil)
	head := gitOutput(t, f.repo, "rev-parse", "HEAD")
	if res := check(leaf.ID); res.Exit != 0 || res.Cmd != "echo green" || res.CmdHash != commandHash("echo green") || res.Head != head ||
		res.Stale || res.RanAt.IsZero() || res.Tail != "green\n" {
		t.Fatalf("green check = %+v", res)
	}
	// The subtask's own command wins over the default; red is a result.
	f.call(http.MethodPatch, "/api/board/cards/"+leaf.ID, `{"accept_cmd":"echo red; exit 3"}`, http.StatusOK, nil)
	if res := check(fmt.Sprintf("%d", leaf.Seq)); res.Exit != 3 || res.Tail != "red\n" {
		t.Fatalf("red check = %+v", res)
	}
	f.refused(http.MethodPost, "/api/board/cards/"+story.ID+"/check", "", http.StatusBadRequest, string(board.CodeInvalid))

	dir, err := f.m.boardDir(context.Background(), f.project)
	if err != nil {
		t.Fatal(err)
	}
	f.m.accept.timeout = 20 * time.Millisecond
	slot := f.m.accept.slot(filepath.Clean(dir))
	slot <- struct{}{}
	f.refused(http.MethodPost, "/api/board/cards/"+leaf.ID+"/check", "", http.StatusConflict, string(board.CodeAcceptanceBusy))
	<-slot
	if d := f.card(leaf.ID); len(d.Requests) != 0 || len(d.Comments) != 0 {
		t.Fatalf("a check recorded %+v and %+v", d.Requests, d.Comments)
	}
}

// Editing a command, the Project default a subtask inherits or its own,
// shows the green rows of earlier runs stale (ADR 0005 §6) in the inbox,
// the card detail and the board frame, with nothing stored.
func TestEditingACommandShowsEarlierGreenRowsStale(t *testing.T) {
	acceptEnv(t)
	f := newPlanner(t)
	f.setAcceptCmd("true")
	leaf := f.create(board.KindSubtask, "", "Leaf")
	_, task := f.launch(leaf.ID)
	f.toolOK(task.ID, "board_request", fmt.Sprintf(`{"ref":%q,"kind":"done","comment":"done"}`, leaf.ID))
	inbox := func() BoardRequest {
		t.Helper()
		snap := f.snapshot()
		if len(snap.Requests) != 1 {
			t.Fatalf("inbox = %+v", snap.Requests)
		}
		return snap.Requests[0]
	}
	if a := acceptOf(t, inbox()); a.Exit != 0 || a.Stale {
		t.Fatalf("a fresh green row = %+v", a)
	}
	sub, _, err := f.m.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	defer f.m.Unsubscribe(sub)
	framed := func() BoardRequest {
		t.Helper()
		var ev boardEvent
		raw, _ := json.Marshal(frameOf(t, sub, "board").data)
		if err := json.Unmarshal(raw, &ev); err != nil || len(ev.Requests) != 1 {
			t.Fatalf("board frame %s: %v", raw, err)
		}
		return ev.Requests[0]
	}
	f.call(http.MethodPatch, "/api/board/projects/"+f.project, `{"accept_cmd":"true && true"}`, http.StatusOK, nil)
	if !acceptOf(t, framed()).Stale || !acceptOf(t, inbox()).Stale || !acceptOf(t, f.card(leaf.ID).Requests[0]).Stale {
		t.Fatal("a green row for the old default is not stale")
	}
	// Set back to the command that ran, the row is current again.
	f.call(http.MethodPatch, "/api/board/cards/"+leaf.ID, `{"accept_cmd":"true"}`, http.StatusOK, nil)
	if acceptOf(t, framed()).Stale || acceptOf(t, inbox()).Stale {
		t.Fatal("a green row for the current command is stale")
	}
	f.call(http.MethodPatch, "/api/board/cards/"+leaf.ID, `{"accept_cmd":""}`, http.StatusOK, nil)
	framed()
	var accepted BoardRequest
	f.call(http.MethodPost, "/api/board/requests/"+inbox().ID+"/accept", `{}`, http.StatusOK, &accepted)
	if a := acceptOf(t, accepted); !a.Stale || a.Exit != 0 {
		t.Fatalf("accepted row = %+v", a)
	}
}

// GET /api/board carries staleness on each stale subtask it is computed
// for, and on nothing else.
func TestBoardCarriesStaleness(t *testing.T) {
	f := newPlanner(t)
	story := f.create(board.KindStory, "", "Story")
	leaf := f.create(board.KindSubtask, story.ID, "Leaf")
	f.call(http.MethodPatch, "/api/board/cards/"+leaf.ID, `{"paths":["docs/**"]}`, http.StatusOK, nil)
	held := f.create(board.KindSubtask, story.ID, "Held")
	f.launch(held.ID)
	for _, c := range f.snapshot().Cards {
		if c.Stale != nil {
			t.Fatalf("#%d at its pin carries %+v", c.Seq, c.Stale)
		}
	}
	writeRepoFile(t, f.repo, "docs/a.md", "# A\n")
	f.commit("docs: a")
	fresh := f.create(board.KindSubtask, story.ID, "Fresh")
	w := f.do(http.MethodGet, "/api/board?project_id="+f.project, "")
	if n := strings.Count(w.Body.String(), `"stale":`); n != 1 || !strings.Contains(w.Body.String(), `"stale":{"behind":1,"diverged":false,"files":["docs/a.md"]}`) {
		t.Fatalf("board = %s", w.Body)
	}
	var snap BoardSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	for _, c := range snap.Cards {
		if (c.ID == leaf.ID) != (c.Stale != nil) {
			t.Fatalf("#%d stale = %+v; held %s, fresh %s", c.Seq, c.Stale, held.ID, fresh.ID)
		}
	}
	// The Unassigned list has no repository to be stale against.
	f.call(http.MethodGet, "/api/board?project_id=unassigned", "", http.StatusOK, nil)
}

// Launching a stale subtask carries the log since its pin and the changed
// files that match its paths in the preamble, and "Do whole story" does
// for the subtask it holds; a subtask at HEAD gets no note.
func TestLaunchPreambleCarriesStaleness(t *testing.T) {
	f := newPlanner(t)
	story := f.create(board.KindStory, "", "Story")
	one := f.create(board.KindSubtask, story.ID, "One")
	f.call(http.MethodPatch, "/api/board/cards/"+one.ID, `{"paths":["docs/**"]}`, http.StatusOK, nil)
	two := f.create(board.KindSubtask, story.ID, "Two")
	pin := gitOutput(t, f.repo, "rev-parse", "HEAD")
	writeRepoFile(t, f.repo, "docs/a.md", "# A\n")
	docs := f.commit("docs: add a")
	head := f.commit("chore: nothing")
	_, task := f.launch(one.ID)
	want := "\nThe code moved on since this subtask was planned:\n" +
		"It was pinned at commit " + pin[:12] + ". HEAD is now " + head[:12] + ", 2 commits ahead of the pin.\n" +
		"Log since the pin, newest first:\n- " + head[:12] + " chore: nothing\n- " + docs[:12] + " docs: add a\n" +
		"Files changed since the pin that match the subtask's paths:\n- docs/a.md\n\nRules:"
	if got := f.conversation(task.ID).Sends()[0]; !strings.Contains(got, want) {
		t.Fatalf("preamble %q lacks %q", got, want)
	}
	held, task := f.launch(story.ID)
	if got := f.conversation(task.ID).Sends()[0]; held.ID != two.ID || !strings.Contains(got, "\nThe code moved on since this subtask was planned:\nIt was pinned at commit "+pin[:12]) {
		t.Fatalf("whole story held %s, preamble %q", held.ID, got)
	}
	three := f.create(board.KindSubtask, "", "Three")
	if _, task := f.launch(three.ID); strings.Contains(f.conversation(task.ID).Sends()[0], "moved on") {
		t.Fatal("a subtask at HEAD has a staleness note")
	}
}

func TestStaleReportText(t *testing.T) {
	log := make([]EvidenceCommit, maxStaleLog)
	for i := range log {
		log[i] = EvidenceCommit{SHA: fmt.Sprintf("%012d", i) + strings.Repeat("f", 28), Subject: fmt.Sprintf("change %d", i)}
	}
	r := staleReport{Stale: Stale{Behind: 52, Diverged: true, Files: []string{"a\x1b[31m.go"}}, pin: strings.Repeat("a", 40), head: strings.Repeat("b", 40), log: log}
	got := r.String()
	for _, part := range []string{"HEAD is now bbbbbbbbbbbb, 52 commits ahead of the pin, and no longer descends from it.\n", "- 000000000049 change 49\n",
		"- and 2 commits before these\n", "- a.go"} {
		if !strings.Contains(got, part) {
			t.Fatalf("report %q lacks %q", got, part)
		}
	}
	gone := staleReport{Stale: Stale{Diverged: true}, pin: "aaaa", head: "bbbb", gone: true}.String()
	if gone != "It was pinned at commit aaaa, which is no longer in the repository's history, so there is no log since it. HEAD is now bbbb." {
		t.Fatalf("gone pin = %q", gone)
	}
}

func TestParseTriage(t *testing.T) {
	long := strings.Repeat("é", maxTriageRunes+5)
	for reply, want := range map[string]Triage{
		`{"verdict":"moot","sentence":"Done in the docs commit."}`:           {Verdict: verdictMoot, Sentence: "Done in the docs commit."},
		" {\"sentence\":\"Still\\nneeded.\",\"verdict\":\"valid\"}\n":        {Verdict: verdictValid, Sentence: "Still needed."},
		`{"verdict":"conflicts","sentence":"` + long + `"}`:                  {Verdict: verdictConflicts, Sentence: clipRunes(long, maxTriageRunes)},
		`{"verdict":"valid","sentence":"  padded \u001b[31mred\u001b[0m  "}`: {Verdict: verdictValid, Sentence: "padded red"},
	} {
		if got, err := parseTriage(reply); err != nil || got != want {
			t.Fatalf("parseTriage(%q) = %+v, %v, want %+v", reply, got, err, want)
		}
	}
	for _, reply := range []string{
		"", "moot", "null", "[]", `"valid"`,
		"```json\n{\"verdict\":\"moot\",\"sentence\":\"x\"}\n```",
		`{"verdict":"moot"}`, `{"sentence":"x"}`, `{"verdict":"maybe","sentence":"x"}`, `{"verdict":"Valid","sentence":"x"}`,
		`{"verdict":1,"sentence":"x"}`, `{"verdict":"valid","sentence":" \n "}`, `{"verdict":"valid","sentence":"x","why":"y"}`,
		`{"verdict":"valid","sentence":"x"} {"verdict":"moot","sentence":"y"}`, `{"verdict":"valid","sentence":"x"} and more`,
	} {
		if got, err := parseTriage(reply); err == nil {
			t.Fatalf("parseTriage(%q) = %+v, want a refusal", reply, got)
		}
	}
}

// Triage asks the Utility model about a stale subtask with its card, pin,
// log and changed files, writes nothing, and keeps the answer per subtask
// and HEAD. An answer that is not the JSON asked for fails and is not kept.
func TestTriageStaleSubtask(t *testing.T) {
	f := newUtilityPlanner(t)
	leaf := f.create(board.KindSubtask, "", "Write the guide", `,"win_condition":"docs/guide.md exists"`)
	f.call(http.MethodPatch, "/api/board/cards/"+leaf.ID, `{"paths":["docs/**"]}`, http.StatusOK, nil)
	pin := gitOutput(t, f.repo, "rev-parse", "HEAD")
	route := "/api/board/cards/" + leaf.ID + "/triage"
	f.refused(http.MethodPost, route, "", http.StatusBadRequest, string(board.CodeInvalid))
	writeRepoFile(t, f.repo, "docs/guide.md", "# Guide\n")
	head := f.commit("docs: add the guide")
	answer := `{"verdict":"moot","sentence":"The guide landed in the docs commit."}`
	f.ts.prov.SetUtilityHook(func(context.Context, agentapi.UtilityRequest) (string, error) { return answer, nil })
	var got Triage
	f.call(http.MethodPost, route, "", http.StatusOK, &got)
	if got != (Triage{Verdict: verdictMoot, Sentence: "The guide landed in the docs commit.", Head: head}) {
		t.Fatalf("triage = %+v", got)
	}
	dir, _ := f.m.boardDir(context.Background(), f.project)
	reqs := f.ts.prov.UtilityRequests()
	if len(reqs) != 1 || reqs[0].Purpose != "planner-triage" || reqs[0].Model != f.utilityModel() || reqs[0].Workdir != dir || reqs[0].System != triageSystem ||
		len(reqs[0].Tools) != 0 || reqs[0].CallTool != nil || reqs[0].Timeout != triageTimeout {
		t.Fatalf("utility requests = %+v", reqs)
	}
	for _, part := range []string{"<subtask>\n#1 Write the guide\nWin condition: docs/guide.md exists\n", "Paths: docs/**\n</subtask>",
		"It was pinned at commit " + pin[:12] + ". HEAD is now " + head[:12] + ", 1 commit ahead", "- " + head[:12] + " docs: add the guide\n",
		"match the subtask's paths:\n- docs/guide.md\n</since_pin>"} {
		if !strings.Contains(reqs[0].Prompt, part) {
			t.Fatalf("prompt %q lacks %q", reqs[0].Prompt, part)
		}
	}
	// Again at the same HEAD: the kept answer, without a model call, and the
	// Board is unchanged.
	revision := f.snapshot().Revision
	answer = `{"verdict":"valid","sentence":"Asked again."}`
	f.call(http.MethodPost, "/api/board/cards/1/triage", "", http.StatusOK, &got)
	if got.Verdict != verdictMoot || len(f.ts.prov.UtilityRequests()) != 1 || f.snapshot().Revision != revision {
		t.Fatalf("second triage = %+v after %d calls", got, len(f.ts.prov.UtilityRequests()))
	}
	// A new HEAD asks again; an unreadable answer fails and is not kept.
	head = f.commit("chore: bump")
	answer = "```json\n{\"verdict\":\"conflicts\",\"sentence\":\"x\"}\n```"
	f.refused(http.MethodPost, route, "", http.StatusBadGateway, codeUtilityFailed)
	answer = `{"verdict":"conflicts","sentence":"The bump moved the docs."}`
	f.call(http.MethodPost, route, "", http.StatusOK, &got)
	if got.Verdict != verdictConflicts || got.Head != head || len(f.ts.prov.UtilityRequests()) != 3 {
		t.Fatalf("triage at a new HEAD = %+v after %d calls", got, len(f.ts.prov.UtilityRequests()))
	}
	f.ts.prov.SetUtilityHook(func(context.Context, agentapi.UtilityRequest) (string, error) { return "", errors.New("model gone") })
	f.commit("chore: again")
	f.refused(http.MethodPost, route, "", http.StatusBadGateway, codeUtilityFailed)
	// Staleness is never computed for a held subtask, so it is not triaged.
	f.launch(leaf.ID)
	f.refused(http.MethodPost, route, "", http.StatusBadRequest, string(board.CodeInvalid))
}

// Without a provider that runs Utility jobs, triage and suggestions are
// refused before any work.
func TestUtilityJobsNeedAProvider(t *testing.T) {
	f := newPlanner(t)
	story := f.create(board.KindStory, "", "Story")
	leaf := f.create(board.KindSubtask, story.ID, "Leaf")
	f.commit("chore: move on")
	f.refused(http.MethodPost, "/api/board/cards/"+leaf.ID+"/triage", "", http.StatusConflict, codeUtilityUnavailable)
	f.refused(http.MethodPost, "/api/board/cards/"+story.ID+"/suggest", `{}`, http.StatusConflict, codeUtilityUnavailable)
	if len(f.ts.prov.UtilityRequests()) != 0 {
		t.Fatal("a provider without host tools ran a Utility job")
	}
}

type toolRun struct {
	name string
	res  agentapi.HostToolResult
}

// playTools returns a CallTool player for a utility hook, which runs off
// the test's goroutine, recording each result in runs.
func playTools(ctx context.Context, req agentapi.UtilityRequest, runs *[]toolRun) func(name, args string) {
	return func(name, args string) {
		*runs = append(*runs, toolRun{name: name, res: req.CallTool(ctx, agentapi.HostToolCall{Name: name, Arguments: json.RawMessage(args)})})
	}
}

// replies decodes the recorded results, on the test's goroutine.
func replies(t *testing.T, runs []toolRun) []toolReply {
	t.Helper()
	out := make([]toolReply, len(runs))
	for i, r := range runs {
		out[i] = decodeReply(t, r.res)
	}
	return out
}

func jobFrame(t *testing.T, sub *Subscriber) boardJobEvent {
	t.Helper()
	fr := frameOf(t, sub, "board_job")
	var ev boardJobEvent
	raw, _ := json.Marshal(fr.data)
	if err := json.Unmarshal(raw, &ev); err != nil || ev.Seq == 0 {
		t.Fatalf("board_job frame %s: %v", raw, err)
	}
	return ev
}

// A suggestion job runs a store-less Utility conversation whose only tools
// are four planner tools scoped to its container: it proposes unconfirmed
// cards there, refs outside the container are not found, confirmed cards
// are not edited, and board_job frames say when it runs and ends.
func TestSuggestProposesInsideTheContainer(t *testing.T) {
	f := newUtilityPlanner(t)
	epic := f.create(board.KindEpic, "", "Epic")
	story := f.create(board.KindStory, epic.ID, "Story")
	other := f.create(board.KindStory, epic.ID, "Other")
	confirmed := f.create(board.KindSubtask, story.ID, "Confirmed")
	sub, _, err := f.m.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	defer f.m.Unsubscribe(sub)
	var runs []toolRun
	f.ts.prov.SetUtilityHook(func(ctx context.Context, req agentapi.UtilityRequest) (string, error) {
		call := playTools(ctx, req, &runs)
		call("board_list", `{}`)
		call("board_create", fmt.Sprintf(`{"kind":"subtask","parent":%q,"title":"Proposed","win_condition":"it works"}`, story.ID))
		call("board_create", fmt.Sprintf(`{"kind":"subtask","parent":%q,"title":"Elsewhere"}`, other.ID))
		call("board_create", `{"kind":"story","parent":"#1","title":"Above"}`)
		call("board_edit", fmt.Sprintf(`{"ref":%q,"title":"Renamed"}`, confirmed.ID))
		call("board_edit", `{"ref":"#5","desc":"Refined."}`)
		call("board_claim", `{"ref":"#5"}`)
		return "Proposed one subtask.", nil
	})
	var reply struct {
		JobID string `json:"job_id"`
	}
	f.call(http.MethodPost, "/api/board/cards/"+story.ID+"/suggest", `{"brief":"Plan the story","document":"Step one.\nStep two.","max":3}`, http.StatusAccepted, &reply)
	running, done := jobFrame(t, sub), jobFrame(t, sub)
	if running != (boardJobEvent{Seq: running.Seq, JobID: reply.JobID, CardID: story.ID, Status: jobRunning}) ||
		done != (boardJobEvent{Seq: done.Seq, JobID: reply.JobID, CardID: story.ID, Status: jobDone}) || done.Seq <= running.Seq {
		t.Fatalf("frames = %+v then %+v for job %s", running, done, reply.JobID)
	}
	req := f.ts.prov.UtilityRequests()[0]
	if got := toolNames(req.Tools); !slices.Equal(got, []string{"board_get", "board_list", "board_create", "board_edit"}) || req.System != suggestSystem ||
		req.Purpose != "planner-suggest" || req.Model != f.utilityModel() || req.Timeout != suggestTimeout {
		t.Fatalf("utility request = %+v with tools %q", req, got)
	}
	for _, part := range []string{"Container: the story #2 Story.\nSplit the document", "Create at most 3 cards in all.\n",
		"<brief>\nPlan the story\n</brief>\n<document>\nStep one.\nStep two.\n</document>"} {
		if !strings.Contains(req.Prompt, part) {
			t.Fatalf("prompt %q lacks %q", req.Prompt, part)
		}
	}
	want := []struct {
		failed bool
		code   board.Code
	}{{false, ""}, {false, ""}, {true, board.CodeNotFound}, {true, board.CodeNotFound}, {true, board.CodeForbidden}, {false, ""}, {true, board.CodeInvalid}}
	got := replies(t, runs)
	for i, w := range want {
		if runs[i].res.Failed != w.failed || got[i].Code != string(w.code) {
			t.Fatalf("call %d %s = %+v, want failed %v code %q", i, runs[i].name, got[i], w.failed, w.code)
		}
	}
	if !strings.Contains(got[0].Text, `#2 story "Story"`) || strings.Contains(got[0].Text, "Other") {
		t.Fatalf("list = %q", got[0].Text)
	}
	d := f.card("#5")
	if d.Card.Title != "Proposed" || d.Card.Confirmed || d.Card.ExpiresAt == nil || *d.Card.ParentID != story.ID || d.Card.Desc != "Refined." {
		t.Fatalf("proposal = %+v", d.Card)
	}
	if c := f.card(confirmed.ID); c.Card.Title != "Confirmed" || len(c.Requests) != 0 {
		t.Fatalf("confirmed card = %+v, requests %+v", c.Card, c.Requests)
	}
	if n := len(f.snapshot().Cards); n != 5 {
		t.Fatalf("cards = %d, want the four plus one proposal", n)
	}
}

// One suggestion job runs per container at a time; another container runs
// its own. The per-Task caps count against the job, as does its max, and a
// job that fails says why in its frame.
func TestSuggestJobsOnePerContainerWithTheCaps(t *testing.T) {
	f := newUtilityPlanner(t)
	epic := f.create(board.KindEpic, "", "Epic")
	story := f.create(board.KindStory, epic.ID, "Story")
	other := f.create(board.KindStory, epic.ID, "Other")
	sub, _, err := f.m.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	defer f.m.Unsubscribe(sub)
	release := make(chan struct{})
	var jobs atomic.Int32
	var runs []toolRun
	f.ts.prov.SetUtilityHook(func(ctx context.Context, req agentapi.UtilityRequest) (string, error) {
		if strings.Contains(req.Prompt, "#3 Other") {
			return "", errors.New("the model is unavailable")
		}
		<-release
		n := jobs.Add(1)
		call := playTools(ctx, req, &runs)
		for i := range board.CapUnconfirmed + 1 {
			call("board_create", fmt.Sprintf(`{"kind":"subtask","parent":%q,"title":"Part %d.%d"}`, story.ID, n, i))
		}
		return "done", nil
	})
	suggest := func(ref, body string) string {
		t.Helper()
		var reply struct {
			JobID string `json:"job_id"`
		}
		f.call(http.MethodPost, "/api/board/cards/"+ref+"/suggest", body, http.StatusAccepted, &reply)
		return reply.JobID
	}
	first := suggest(story.ID, `{"max":20}`)
	if ev := jobFrame(t, sub); ev.JobID != first || ev.Status != jobRunning {
		t.Fatalf("first frame = %+v", ev)
	}
	f.refused(http.MethodPost, "/api/board/cards/"+story.ID+"/suggest", `{}`, http.StatusConflict, codeSuggestBusy)
	second := suggest(other.ID, "")
	if running, failed := jobFrame(t, sub), jobFrame(t, sub); running.JobID != second || running.Status != jobRunning || failed.JobID != second ||
		failed.Status != jobFailed || failed.CardID != other.ID || !strings.Contains(failed.Error, "the model is unavailable") {
		t.Fatalf("second job frames = %+v, %+v", running, failed)
	}
	close(release)
	if ev := jobFrame(t, sub); ev.JobID != first || ev.Status != jobDone {
		t.Fatalf("first job end = %+v", ev)
	}
	for i, r := range replies(t, runs) {
		if limited := i == board.CapUnconfirmed; runs[i].res.Failed != limited || limited && r.Code != string(board.CodeLimit) {
			t.Fatalf("create %d = %+v", i, r)
		}
	}
	// The container is free again; the new job's max stops it first.
	runs = nil
	third := suggest(story.ID, `{"max":1}`)
	jobFrame(t, sub)
	if ev := jobFrame(t, sub); ev.JobID != third || ev.Status != jobDone {
		t.Fatalf("third job end = %+v", ev)
	}
	if got := replies(t, runs[:2]); runs[0].res.Failed || !runs[1].res.Failed || got[1].Code != string(board.CodeLimit) || !strings.Contains(got[1].Text, "at most 1 cards") {
		t.Fatalf("capped creates = %+v", got)
	}
}

func TestSuggestRefusals(t *testing.T) {
	f := newUtilityPlanner(t)
	story := f.create(board.KindStory, "", "Story")
	leaf := f.create(board.KindSubtask, story.ID, "Leaf")
	route := "/api/board/cards/" + story.ID + "/suggest"
	f.refused(http.MethodPost, "/api/board/cards/"+leaf.ID+"/suggest", `{}`, http.StatusBadRequest, string(board.CodeInvalid))
	for _, body := range []string{`{"max":21}`, `{"max":-1}`, fmt.Sprintf(`{"document":%q}`, strings.Repeat("x", maxSuggestDocument+1)),
		fmt.Sprintf(`{"brief":%q}`, strings.Repeat("x", maxSuggestBrief+1))} {
		f.refused(http.MethodPost, route, body, http.StatusBadRequest, string(board.CodeInvalid))
	}
	f.refused(http.MethodPost, "/api/board/cards/99/suggest", `{}`, http.StatusNotFound, string(board.CodeNotFound))
	f.call(http.MethodPatch, "/api/settings", `{"planner":false}`, http.StatusOK, nil)
	f.refused(http.MethodPost, route, `{}`, http.StatusConflict, codePlannerOff)
	if len(f.ts.prov.UtilityRequests()) != 0 {
		t.Fatal("a refused suggestion ran")
	}
}
