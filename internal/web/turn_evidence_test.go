package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// A command line's checks, each with how much of its exit status is its own.
func TestCommandChecksAttributeTheExitStatus(t *testing.T) {
	long := "go test -count=1 " + strings.Repeat("./internal/some/package/path ", 14) + "2>&1 | tail -5"
	for _, tc := range []struct {
		command string
		want    []checkRun
	}{
		{"go test ./...", []checkRun{{checkTest, statusOwn}}},
		{"cd web && CI=1 npm run test", []checkRun{{checkTest, statusOwn}}},
		{"timeout 60 env CI=1 npx vitest run", []checkRun{{checkTest, statusOwn}}},
		{"time go test ./...", []checkRun{{checkTest, statusOwn}}},
		{"(cd web && npm test)", []checkRun{{checkTest, statusOwn}}},
		{"make", []checkRun{{checkBuild, statusOwn}}},
		// After `;`, `||`, a line break or `&` the status is a later command's.
		{"go test ./... || true", []checkRun{{checkTest, statusElsewhere}}},
		{"go test ./... ; echo done", []checkRun{{checkTest, statusElsewhere}}},
		{"go test ./...\necho done", []checkRun{{checkTest, statusElsewhere}}},
		{"go test ./... &", []checkRun{{checkTest, statusElsewhere}}},
		{"true || go test ./...", []checkRun{{checkTest, statusElsewhere}}},
		{"if go test ./...; then echo ok; fi", []checkRun{{checkTest, statusElsewhere}}},
		// A line break or a comment is no command of its own.
		{"cd web\nnpm test", []checkRun{{checkTest, statusOwn}}},
		{"# run the suite\ngo test ./...", []checkRun{{checkTest, statusOwn}}},
		// Pipes hand the status to their last command, unless pipefail.
		{"go test ./... 2>&1 | tail -20", []checkRun{{checkTest, statusPiped}}},
		{long, []checkRun{{checkTest, statusPiped}}},
		{"go test ./... | tail ; echo", []checkRun{{checkTest, statusPiped}}},
		{"set -o pipefail; go test ./... | tail", []checkRun{{checkTest, statusOwn}}},
		// A quoted bar is no pipe.
		{"go test -run 'TestA|TestB' ./...", []checkRun{{checkTest, statusOwn}}},
		// A compound line runs every check it names.
		{"go build ./... && go test ./...", []checkRun{{checkBuild, statusAndThen}, {checkTest, statusOwn}}},
		{"go vet ./... && go test ./... && echo ok", []checkRun{{checkVet, statusAndThen}, {checkTest, statusAndThen}}},
		{"git status", nil},
		{"echo go test", nil},
		{"cat go.test.md", nil},
		{"go test 'unclosed", nil},
	} {
		if got := commandChecks(tc.command); !slices.Equal(got, tc.want) {
			t.Errorf("%q: checks = %v, want %v", tc.command, got, tc.want)
		}
	}
}

func TestCountsOfTheCommonTestSummaries(t *testing.T) {
	for output, want := range map[string]string{
		"ok  \texample.com/a\t0.01s\nok  \texample.com/b\t0.02s\n":                                      "2 packages ok",
		"--- FAIL: TestAdd (0.00s)\nFAIL\nFAIL\texample.com/calc\t0.003s\nFAIL\n<shellId: 0 completed>": "1 package failed, 1 test failed",
		"Tests:       1 failed, 12 passed, 13 total":                                                    "12 passed, 1 failed",
		" Tests  14 passed (14)":                         "14 passed",
		"===== 3 passed in 0.12s =====":                  "3 passed",
		"test result: ok. 4 passed; 0 failed; 0 ignored": "4 passed",
		"# tests 5\n# pass 5\n# fail 0":                  "5 passed",
	} {
		if got := countsOf(output); got == nil || got.label != want {
			t.Errorf("%q: counts = %+v, want %q", output, got, want)
		}
	}
	if got := countsOf("nothing here"); got != nil {
		t.Errorf("counts of nothing = %+v", got)
	}
}

// evidenceTurn notes a prompt at t0 and then items one second apart.
type evidenceTurn struct {
	s  *webSession
	at time.Time
}

func newEvidenceTurn() *evidenceTurn {
	t0 := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	e := &evidenceTurn{s: newSession("id", "fake", "", "/repo", "", t0), at: t0}
	e.note(agentapi.Item{ID: "u", Kind: agentapi.ItemUser, Text: "fix it"})
	return e
}

func (e *evidenceTurn) note(it agentapi.Item) {
	e.at = e.at.Add(time.Second)
	if it.Time.IsZero() {
		it.Time = e.at
	}
	if it.Kind == agentapi.ItemTool && it.Tool.Status == agentapi.ToolCompleted && it.EndedAt.IsZero() {
		it.EndedAt = it.Time.Add(500 * time.Millisecond)
	}
	e.s.items = append(e.s.items, it)
	e.s.itemIdx[itemKey(it.AgentID, it.ID)] = len(e.s.items) - 1
	e.s.noteEdits(it)
}

func (e *evidenceTurn) bash(id, command string, exit int, output string) {
	input, _ := json.Marshal(map[string]string{"command": command})
	e.note(agentapi.Item{ID: id, Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "bash", Status: agentapi.ToolCompleted, Input: string(input), Output: output, ExitCode: &exit}})
}

func (e *evidenceTurn) edit(id, path, agent string) {
	input, _ := json.Marshal(map[string]string{"path": path})
	e.note(agentapi.Item{ID: id, Kind: agentapi.ItemTool, AgentID: agent, Tool: &agentapi.ToolCall{Name: "edit", Status: agentapi.ToolCompleted, Input: string(input)}})
}

func (e *evidenceTurn) reply(text string) {
	e.note(agentapi.Item{ID: "a", Kind: agentapi.ItemAssistant, Text: text})
}

func (e *evidenceTurn) facts(t *testing.T) turnFacts {
	t.Helper()
	f, ok := e.s.turnFacts()
	if !ok {
		t.Fatal("no turn")
	}
	return f
}

const (
	goOK   = "ok  \texample.com/x\t0.1s\n"
	goFAIL = "--- FAIL: TestA (0.00s)\nFAIL\nFAIL\texample.com/x\t0.1s\nFAIL\n"
)

func claimDetails(claims []EvidenceClaim) []string {
	var out []string
	for _, c := range claims {
		out = append(out, fmt.Sprintf("%s | %v | %s", c.Text, c.Verified, c.Detail))
	}
	return out
}

// A test whose status is not the line's own passes or fails by its whole
// output only; a failing run is never called verified.
func TestCheckOutcomesFollowTheReportedStatus(t *testing.T) {
	for _, tc := range []struct {
		command, output string
		exit            int
		outcome, note   string
	}{
		{"go test ./...", goOK, 0, outcomePass, ""},
		{"go test ./...", goFAIL, 1, outcomeFail, ""},
		{"go test ./... || true", goFAIL, 0, outcomeFail, ""},
		{"go test ./... ; echo done", goFAIL, 0, outcomeFail, ""},
		{"go test ./... ; echo done", goOK, 0, outcomePass, "passed by its output"},
		{"go test ./... || true", "weird", 0, outcomeUnclear, "the exit status is a later command’s"},
		{"go test ./... 2>&1 | tail -3", goOK, 0, outcomeUnclear, "piped, so the exit status is the last command’s"},
		{"go test ./... 2>&1 | tail -3", goFAIL, 0, outcomeFail, ""},
		{"go test -run 'TestA|TestB' ./...", goFAIL, 1, outcomeFail, ""},
		{"go build ./... && go test ./...", goFAIL, 1, outcomeFail, ""},
		{"go build ./... && go vet ./...", "", 1, outcomeFail, ""},
		{"go build ./... && echo built", "", 1, outcomeUnclear, "a later command may have failed"},
	} {
		e := newEvidenceTurn()
		e.bash("c", tc.command, tc.exit, tc.output)
		f := e.facts(t)
		if len(f.checks) != 1 || f.checks[0].Outcome != tc.outcome || f.checks[0].Note != tc.note {
			t.Errorf("%q exit %d: checks = %+v, want %s %q", tc.command, tc.exit, f.checks, tc.outcome, tc.note)
		}
	}
	// The F23 turn: `|| true` hides the failure from the exit status only.
	e := newEvidenceTurn()
	e.bash("c", "go test ./... || true", 0, goFAIL)
	e.reply("All tests pass.")
	f := e.facts(t)
	if got := claimDetails(f.judgeClaims("/repo")); !slices.Equal(got, []string{"All tests pass. | false | Not verified · the last run failed"}) {
		t.Errorf("claims = %q", got)
	}
	if got := outcomeLine("", f.outcome("/repo")); got != "Tests fail" {
		t.Errorf("outcome = %q", got)
	}
	// A compound line is labelled with every check it runs.
	e = newEvidenceTurn()
	e.bash("c", "go build ./... && go test ./...", 1, goFAIL)
	e.reply("The build succeeds. Tests pass.")
	f = e.facts(t)
	if c := f.checks[0]; !slices.Equal(c.Kinds, []string{checkBuild, checkTest}) || c.Counts != "1 package failed, 1 test failed" || c.TookMS == nil || *c.TookMS != 500 {
		t.Errorf("compound check = %+v", c)
	}
	if got := claimDetails(f.judgeClaims("/repo")); !slices.Equal(got, []string{
		"The build succeeds. | false | Not verified · the last run’s result is unclear",
		"Tests pass. | false | Not verified · the last run failed (exit 1)",
	}) {
		t.Errorf("compound claims = %q", got)
	}
}

func TestClaimsAreJudgedAgainstTheWholeTurn(t *testing.T) {
	e := newEvidenceTurn()
	e.edit("e1", "/repo/calc.go", "")
	e.bash("t1", "go test ./...", 0, goOK)
	e.edit("e2", "/repo/README.md", "")
	e.reply("Fixed Add. All tests pass and there are no regressions. I updated the README. The CHANGELOG is updated too. docs/terminal.md describes it. Lint is clean. I did not run the linter, but the build compiles.")
	got := claimDetails(e.facts(t).judgeClaims("/repo"))
	want := []string{
		// README was edited after the test run: a doc edit leaves the run's result standing.
		"All tests pass and there are no regressions. | true | go test ./... · exit 0",
		"I updated the README. | true | edited README.md",
		"The CHANGELOG is updated too. | false | Not verified · no edit to CHANGELOG in this turn",
		"docs/terminal.md describes it. | false | Not verified · no edit to docs/terminal.md in this turn",
		"Lint is clean. | false | Not verified · no lint run in this turn",
		"I did not run the linter, but the build compiles. | false | Not verified · no build in this turn",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("claims =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// Code edited after the run, by the main agent or a subagent, stales it.
	for _, agent := range []string{"", "agent-1"} {
		e := newEvidenceTurn()
		e.bash("t1", "go test ./...", 0, goOK)
		e.edit("e1", "calc.go", agent)
		e.reply("All tests pass.")
		if got := claimDetails(e.facts(t).judgeClaims("/repo")); !slices.Equal(got, []string{"All tests pass. | false | Not verified · files changed after the last run"}) {
			t.Errorf("edit by %q after the run: %q", agent, got)
		}
	}

	// Positive claims beside a negation are still judged; failures are none.
	for sentence, want := range map[string][]string{
		"All tests pass; I did not touch the public API.": {checkTest},
		"Tests pass with no failures.":                    {checkTest},
		"No tests failed.":                                {checkTest},
		"go vet ./... passed with no reported issues.":    {checkVet},
		"The build and tests pass.":                       {checkTest, checkBuild},
		"The tests failed on TestAdd.":                    nil,
		"I did not run the tests.":                        nil,
		"Tests pass but the build fails.":                 {checkTest},
		"I changed Add to return a + b.":                  nil,
		"I updated the README to describe Add.":           {claimDocs},
		"I did not update the README.":                    nil,
	} {
		if got := claimTopics(sentence); !slices.Equal(got, want) {
			t.Errorf("claimTopics(%q) = %q, want %q", sentence, got, want)
		}
	}
	if got := sentences("Done. Tests pass.\n\n```\ngo test\n```\n- Updated `README.md`, e.g. the usage. docs/x.md too.\nFixed `Add`. `go test ./...` passes."); !slices.Equal(got, []string{"Done.", "Tests pass.", "Updated README.md, e.g. the usage.", "docs/x.md too.", "Fixed Add.", "go test ./... passes."}) {
		t.Errorf("sentences = %q", got)
	}
}

// A long turn's early test run still backs its claim, also once the held
// transcript was trimmed (F26).
func TestTurnEvidenceCoversTheWholeTurn(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, err := m.Create(CreateRequest{Provider: prov.Name(), ProjectID: addProject(t, m, t.TempDir())})
	if err != nil {
		t.Fatal(err)
	}
	conv := prov.Last()
	t0 := time.Now().Add(-time.Hour)
	at := func(i int) time.Time { return t0.Add(time.Duration(i) * time.Millisecond) }
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.EmitItem(agentapi.Item{ID: "u", Kind: agentapi.ItemUser, Text: "fix", Time: at(0)})
	exit := 0
	conv.EmitItem(agentapi.Item{ID: "test", Kind: agentapi.ItemTool, Time: at(1), EndedAt: at(2), Tool: &agentapi.ToolCall{Name: "bash", Status: agentapi.ToolCompleted, Input: `{"command":"go test ./..."}`, Output: goOK, ExitCode: &exit}})
	for i := range maxItems + 100 {
		conv.EmitItem(agentapi.Item{ID: fmt.Sprintf("c%d", i), Kind: agentapi.ItemTool, Time: at(10 + i), Tool: &agentapi.ToolCall{Name: "bash", Status: agentapi.ToolCompleted, Input: `{"command":"ls"}`, ExitCode: &exit}})
	}
	conv.EmitItem(agentapi.Item{ID: "a", Kind: agentapi.ItemAssistant, Text: "All tests pass.", Time: at(maxItems + 200)})
	conv.EmitTurn(agentapi.TurnCompleted, "")
	m.mu.Lock()
	_, held := m.sessions[sum.ID].itemIdx["test"]
	m.mu.Unlock()
	if held {
		t.Fatal("the test run is still held; the transcript was not trimmed")
	}
	ev, err := m.TurnEvidence(t.Context(), sum.ID, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ev.Checks) != 1 || ev.Checks[0].ItemID != "test" || ev.Checks[0].Outcome != outcomePass {
		t.Fatalf("checks = %+v", ev.Checks)
	}
	if got := claimDetails(ev.Claims); !slices.Equal(got, []string{"All tests pass. | true | go test ./... · exit 0"}) {
		t.Fatalf("claims = %q", got)
	}
	if got := summaryOf(t, m, sum.ID).Outcome; got != "Tests pass" {
		t.Fatalf("outcome = %q", got)
	}
	// Since a look before the turn: every command, not the newest page.
	ev, err = m.TurnEvidence(t.Context(), sum.ID, t0.Add(-time.Minute), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if ev.Since == nil || ev.Since.Text != fmt.Sprintf("the agent finished and ran the tests plus %d other commands", maxItems+100) || ev.Since.IDs[0] != "u" || len(ev.Since.IDs) != maxItems+103 {
		t.Fatalf("since = %+v", ev.Since)
	}
}

// One reading of which files an edit call changed (D1, F25, F49, F50).
func TestEditedPathsReadEveryEditTool(t *testing.T) {
	patch := "*** Begin Patch\n*** Update File: a.go\n@@ func x() {\n foo()\n\n-bar()\n+baz()\n*** Add File: b.go\n+x\n*** Update File: c.go\n*** Move to: d.go\n@@\n-a\n+b\n*** End Patch\n\n"
	str, _ := json.Marshal(patch)
	obj, _ := json.Marshal(map[string]string{"input": patch})
	big, _ := json.Marshal(patch + strings.Repeat("+filler\n", 10000))
	all := []string{"a.go", "b.go", "c.go", "d.go"}
	for _, tc := range []struct {
		name, tool, input string
		status            agentapi.ToolStatus
		want              []string
	}{
		{"raw patch with blank lines", "apply_patch", patch, agentapi.ToolCompleted, all},
		{"patch as a JSON string", "apply_patch", string(str), agentapi.ToolCompleted, all},
		{"patch in a JSON object", "apply_patch", string(obj), agentapi.ToolCompleted, all},
		{"patch of 64 KiB and more", "apply_patch", string(big), agentapi.ToolCompleted, all},
		{"patch clipped before its end", "apply_patch", string(str[:strings.Index(string(str), "Update File: c.go")]), agentapi.ToolCompleted, []string{"a.go", "b.go"}},
		{"tool name in another case", "Apply_Patch", patch, agentapi.ToolCompleted, all},
		{"write", "write", `{"path":"w.go","content":"x"}`, agentapi.ToolCompleted, []string{"w.go"}},
		{"Edit", "Edit", `{"file_path":"/repo/e.go"}`, agentapi.ToolCompleted, []string{"/repo/e.go"}},
		{"create clipped mid content", "create", `{"path":"n.go","file_text":"package n\n` + strings.Repeat("x", 100), agentapi.ToolCompleted, []string{"n.go"}},
		{"a failed edit", "edit", `{"path":"f.go"}`, agentapi.ToolFailed, nil},
		{"a read", "view", `{"path":"v.go"}`, agentapi.ToolCompleted, nil},
	} {
		got := editedPaths(&agentapi.ToolCall{Name: tc.tool, Input: tc.input, Status: tc.status})
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: paths = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// An edit counts once it completed: one seen running that then failed is
// never the Task's (F04, F36).
func TestNoteEditsSkipsEditsThatDidNotComplete(t *testing.T) {
	s := newSession("id", "fake", "", "/w", "", time.Now())
	at := time.Now()
	s.noteEdits(toolEdit("e", "edit", "x.go", agentapi.ToolRunning, at))
	s.noteEdits(toolEdit("e", "edit", "x.go", agentapi.ToolFailed, at))
	if len(s.edits) != 0 {
		t.Fatalf("a failed edit was kept: %v", s.edits)
	}
	s.noteEdits(toolEdit("d", "edit", "y.go", agentapi.ToolRunning, at))
	done := toolEdit("d", "edit", "y.go", agentapi.ToolCompleted, at)
	done.EndedAt = at.Add(time.Second)
	if !s.noteEdits(done) || !s.edits["y.go"].Equal(done.EndedAt) {
		t.Fatalf("a completed edit = %v", s.edits)
	}
}

// The card's files are relative to the repository, as Changes lists them,
// with their line counts, in a Project that is a subfolder (F45); the card,
// the outcome line and the turn scope count the same files.
func TestTurnFilesMatchChangesInASubfolderProject(t *testing.T) {
	old := diffDelay
	diffDelay = time.Millisecond
	t.Cleanup(func() { diffDelay = old })
	repo := gitRepoFixture(t)
	m, prov, _ := newTestManager(t)
	sum, err := m.Create(CreateRequest{Provider: prov.Name(), ProjectID: addProject(t, m, filepath.Join(repo, "sub"))})
	if err != nil {
		t.Fatal(err)
	}
	conv := prov.Last()
	patch, _ := json.Marshal("*** Begin Patch\n*** Add File: new file.txt\n+a\n*** Update File: ../tracked.txt\n@@\n-two\n+three\n*** End Patch")
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.EmitItem(agentapi.Item{ID: "u", Kind: agentapi.ItemUser, Text: "go"})
	conv.EmitItem(agentapi.Item{ID: "p", Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "apply_patch", Status: agentapi.ToolCompleted, Input: string(patch)}})
	conv.EmitItem(agentapi.Item{ID: "a", Kind: agentapi.ItemAssistant, Text: "Done."})
	conv.EmitTurn(agentapi.TurnCompleted, "")
	ev, err := m.TurnEvidence(t.Context(), sum.ID, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range ev.Files {
		if f.Additions == nil || f.Deletions == nil {
			t.Fatalf("no line counts for %s", f.Path)
		}
		got = append(got, fmt.Sprintf("%s +%d -%d", f.Path, *f.Additions, *f.Deletions))
	}
	if !slices.Equal(got, []string{"tracked.txt +2 -1", "sub/new file.txt +3 -0"}) {
		t.Fatalf("files = %q", got)
	}
	if _, turn := scopePaths(t, m, sum.ID, ScopeTurn); !slices.Equal(turn, []string{"tracked.txt", "sub/new file.txt"}) {
		t.Fatalf("turn scope = %q", turn)
	}
	if got := summaryOf(t, m, sum.ID).Outcome; got != "2 files changed" {
		t.Fatalf("outcome = %q", got)
	}
}

// The outcome line's phrase lands after the turn it words: it is no new
// activity, so a Task the owner watched finish stays read (F28).
func TestOutcomePhraseDoesNotMarkTheTaskUnread(t *testing.T) {
	prov := newAssistProvider()
	m, project := assistManager(t, openTestStore(t), prov)
	verb := make(chan string, 1)
	prov.SetUtilityHook(func(context.Context, agentapi.UtilityRequest) (string, error) { return <-verb, nil })
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Name: "t"})
	if err != nil {
		t.Fatal(err)
	}
	finishTurn(prov.Last(), "fix", "Fixed it.")
	before := summaryOf(t, m, sum.ID).UpdatedAt
	time.Sleep(5 * time.Millisecond)
	verb <- "Fixed it"
	waitUntil(t, "the verb phrase", func() bool { return summaryOf(t, m, sum.ID).Outcome == "Fixed it" })
	if after := summaryOf(t, m, sum.ID).UpdatedAt; !after.Equal(before) {
		t.Fatalf("updated_at moved from %v to %v", before, after)
	}
}

func TestSinceYouLeftCountsWhatHappenedAfterTheMark(t *testing.T) {
	e := newEvidenceTurn()
	mark := e.at
	e.edit("e1", "calc.go", "")
	e.bash("t1", "go test ./...", 0, "")
	e.bash("t2", "git diff", 0, "")
	e.edit("e2", "/repo/README.md", "")
	e.edit("e3", "docs/x.md", "agent-1")
	e.reply("done")
	e.s.turnTimings = []TurnTiming{{ID: "tt", StartedAt: mark, EndedAt: e.at, State: StateCompleted}}
	since := e.s.sinceYouLeft(mark, e.at.Add(time.Hour))
	if since == nil || since.Text != "the agent finished, ran the tests plus 1 other command, and changed 3 files" || !slices.Equal(since.IDs, []string{"e1", "t1", "t2", "e2", "a"}) {
		t.Fatalf("since = %+v", since)
	}
	if got := e.s.sinceYouLeft(e.at, e.at.Add(time.Hour)); got != nil {
		t.Fatalf("nothing new = %+v", got)
	}
	// What happens after the owner came back is seen, not news.
	if got := e.s.sinceYouLeft(mark, mark.Add(1700*time.Millisecond)); got == nil || got.Text != "changed 1 file" {
		t.Fatalf("until = %+v", got)
	}
	e.s.interactions = []*interaction{{Interaction: agentapi.Interaction{ID: "q", Kind: agentapi.InteractionQuestion, Time: mark.Add(time.Second)}}}
	e.s.base = StateWorking
	if got := e.s.sinceYouLeft(mark, mark.Add(1700*time.Millisecond)); got == nil || got.Text != "the agent is still working, changed 1 file, and asked you a question" {
		t.Fatalf("working = %+v", got)
	}
}

func TestTurnEvidenceRoute(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	sum, conv := createSession(t, ts.m, ts.prov)
	finishTurn(conv, "fix", "All tests pass.", agentapi.Item{ID: "t", Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "bash", Status: agentapi.ToolCompleted, Input: `{"command":"go test ./... || true"}`, Output: goFAIL, ExitCode: codeOf(0)}})
	auth := withCookie(ts)
	w := ts.do(http.MethodGet, "/api/sessions/"+sum.ID+"/evidence?since="+time.Now().Add(-time.Hour).Format(time.RFC3339Nano), "", auth)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var ev TurnEvidence
	if err := json.Unmarshal(w.Body.Bytes(), &ev); err != nil {
		t.Fatal(err)
	}
	if len(ev.Checks) != 1 || ev.Checks[0].Outcome != outcomeFail || len(ev.Claims) != 1 || ev.Claims[0].Verified || ev.Since == nil {
		t.Fatalf("evidence = %+v", ev)
	}
	if w := ts.do(http.MethodGet, "/api/sessions/"+sum.ID+"/evidence?since=yesterday", "", auth); w.Code != http.StatusBadRequest {
		t.Fatalf("bad since: %d", w.Code)
	}
	if w := ts.do(http.MethodGet, "/api/sessions/nope/evidence", "", auth); w.Code != http.StatusNotFound {
		t.Fatalf("unknown task: %d", w.Code)
	}
}
