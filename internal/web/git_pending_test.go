package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

// pendingOf is the Task's unsaved git work as its summary serves it.
func pendingOf(t *testing.T, m *Manager, id string) GitPending {
	t.Helper()
	p := summaryOf(t, m, id).GitPending
	if p == nil {
		return GitPending{Uncommitted: -1, Unpushed: -1}
	}
	return *p
}

func TestGitPendingCountsTrackedChangesAndUnpushedCommits(t *testing.T) {
	gitIdentity(t)
	repo := gitRepoFixture(t)
	g := newGitTask(t, repo)
	// A second Task in a Project on a subdirectory shares the repository's count.
	sub, err := g.m.Create(CreateRequest{Provider: g.prov.Name(), ProjectID: addProject(t, g.m, filepath.Join(repo, "sub"))})
	if err != nil {
		t.Fatal(err)
	}
	want := func(what string, p GitPending) {
		t.Helper()
		waitUntil(t, what, func() bool { return pendingOf(t, g.m, g.id) == p && pendingOf(t, g.m, sub.ID) == p })
	}

	// One changed tracked file; the untracked one does not count, and
	// without a remote there is nowhere to push.
	g.m.refreshBranches(context.Background(), true)
	want("first read", GitPending{Uncommitted: 1})

	remote := filepath.Join(t.TempDir(), "remote.git")
	gitIn(t, repo, "init", "-q", "--bare", remote)
	gitIn(t, repo, "remote", "add", "origin", remote)
	gitIn(t, repo, "commit", "-q", "-m", "fix: one", "--", "tracked.txt")
	g.m.refreshBranches(context.Background(), true)
	// No remote-tracking branch yet: every commit is unpushed.
	want("after commit", GitPending{Unpushed: 2})
	gitIn(t, repo, "push", "-q", "-u", "origin", "HEAD")
	g.m.refreshBranches(context.Background(), true)
	want("after push", GitPending{})

	writeRepoFile(t, repo, "unchanged.txt", "changed\n")
	gitIn(t, repo, "commit", "-q", "-am", "feat: local")
	writeRepoFile(t, repo, "tracked.txt", "edited again\n")
	// A Project branch refresh reads it again.
	g.m.refreshBranches(context.Background(), true)
	want("after refresh", GitPending{Uncommitted: 1, Unpushed: 1})
}

func TestGitPendingOutsideARepository(t *testing.T) {
	m, prov, _ := newTestManager(t)
	dir := t.TempDir()
	sum, err := m.Create(CreateRequest{Provider: prov.Name(), ProjectID: addProject(t, m, dir)})
	if err != nil {
		t.Fatal(err)
	}
	m.refreshGitPending(context.Background(), dir)
	if p := summaryOf(t, m, sum.ID).GitPending; p != nil {
		t.Fatalf("git_pending outside a repository = %+v", p)
	}
}

func TestCountTrackedChanges(t *testing.T) {
	status := "1 .M N... 100644 100644 100644 a a a.txt\x00" +
		"2 R. N... 100644 100644 100644 a a R100 new.txt\x00old.txt\x00" +
		"u UU N... 100644 100644 100644 100644 a a a both.txt\x00" +
		"1 D. N... 100644 000000 000000 a 0 gone.txt\x00"
	status += "1 .M N... 100644 100644 100644 a a sub/with space.txt\x00"
	n, paths := trackedChanges([]byte(status), 4)
	if n != 5 || !slices.Equal(paths, []string{"a.txt", "new.txt", "both.txt", "gone.txt"}) {
		t.Fatalf("count = %d, paths %q", n, paths)
	}
	if _, paths := trackedChanges([]byte("1 .M N... 100644 100644 100644 a a sub/with space.txt\x00"), 3); !slices.Equal(paths, []string{"sub/with space.txt"}) {
		t.Fatalf("paths with a space = %q", paths)
	}
	if n, paths := trackedChanges(nil, 3); n != 0 || paths != nil {
		t.Fatalf("empty = %d %q", n, paths)
	}
}

// unsavedRefusal is checkUnsaved's refusal of the Thread id, nil when it
// would settle.
func unsavedRefusal(t *testing.T, m *Manager, id string) *Error {
	t.Helper()
	err := m.checkUnsaved(t.Context(), id)
	if err == nil {
		return nil
	}
	var refused *Error
	if !errors.As(err, &refused) || refused.Code != codeUnsavedWork || refused.Pending == nil {
		t.Fatalf("checkUnsaved = %v", err)
	}
	slices.Sort(refused.Refs)
	return refused
}

// sharedFolderWithOthersWork is gitRepoFixture with a remote and a commit
// on no remote: tracked.txt changed, sub/new file.txt new and one commit
// not pushed, none of it any Thread's.
func sharedFolderWithOthersWork(t *testing.T) string {
	t.Helper()
	gitIdentity(t)
	repo := gitRepoFixture(t)
	gitIn(t, repo, "remote", "add", "origin", filepath.Join(t.TempDir(), "remote.git"))
	return repo
}

// Settle and Archive refuse a Thread with unsaved work of its own unless
// forced, naming the counts and its first changed files. In a folder
// Threads share, the others' changes and commits on no remote do not stop
// a Thread with none of its own.
func TestSettleAndArchiveAskFirstWithUnsavedWork(t *testing.T) {
	repo := sharedFolderWithOthersWork(t)
	g := newGitTask(t, repo)
	conv := g.prov.Last()
	// The Thread changed a tracked file and made a new one with its edit tools.
	conv.EmitItem(toolEdit("e1", "edit", filepath.Join(repo, "tracked.txt"), agentapi.ToolCompleted, time.Now()))
	writeRepoFile(t, repo, "notes.txt", "new\n")
	conv.EmitItem(toolEdit("e2", "create", "notes.txt", agentapi.ToolCompleted, time.Now()))

	other, err := g.m.Create(CreateRequest{Provider: g.prov.Name(), ProjectID: summaryOf(t, g.m, g.id).ProjectID})
	if err != nil {
		t.Fatal(err)
	}
	if w := g.ts.do(http.MethodPost, "/api/sessions/"+other.ID+"/settle", "", withCookie(g.ts)); w.Code != http.StatusOK {
		t.Fatalf("settle beside others' work = %d %s", w.Code, w.Body)
	}
	// The folder's indicator still counts everything there.
	g.m.refreshGitPending(t.Context(), repo)
	if p := pendingOf(t, g.m, g.id); p.Uncommitted != 1 || p.Unpushed != 1 {
		t.Fatalf("folder git_pending = %+v", p)
	}

	// Archive's dry run answers with the refusal Archive would meet, and moves nothing.
	if w := g.ts.do(http.MethodGet, "/api/sessions/"+other.ID+"/archive", "", withCookie(g.ts)); w.Code != http.StatusNoContent {
		t.Fatalf("dry run beside others' work = %d %s", w.Code, w.Body)
	}
	dry := g.ts.do(http.MethodGet, "/api/sessions/"+g.id+"/archive", "", withCookie(g.ts))
	for _, action := range []string{"settle", "archive"} {
		w := g.ts.do(http.MethodPost, "/api/sessions/"+g.id+"/"+action, "", withCookie(g.ts))
		if action == "archive" && (dry.Code != w.Code || dry.Body.String() != w.Body.String()) {
			t.Fatalf("dry run = %d %s, archive = %d %s", dry.Code, dry.Body, w.Code, w.Body)
		}
		var refused struct {
			Error   string     `json:"error"`
			Code    string     `json:"code"`
			Refs    []string   `json:"refs"`
			Pending GitPending `json:"git_pending"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &refused); err != nil || w.Code != http.StatusConflict {
			t.Fatalf("%s = %d %s", action, w.Code, w.Body)
		}
		slices.Sort(refused.Refs)
		if refused.Code != codeUnsavedWork || refused.Pending != (GitPending{Uncommitted: 2}) || !slices.Equal(refused.Refs, []string{"notes.txt", "tracked.txt"}) ||
			!strings.Contains(refused.Error, "own changes (2 not committed)") {
			t.Fatalf("%s refused = %+v", action, refused)
		}
		if got := summaryOf(t, g.m, g.id); got.Stage != StageActive {
			t.Fatalf("%s moved the Thread: %+v", action, got)
		}
	}
	w := g.ts.do(http.MethodPost, "/api/sessions/"+g.id+"/settle", `{"force":true}`, withCookie(g.ts))
	if w.Code != http.StatusOK || summaryOf(t, g.m, g.id).Stage != StageSettled {
		t.Fatalf("forced settle = %d %s", w.Code, w.Body)
	}
	if w := g.ts.do(http.MethodPost, "/api/sessions/"+g.id+"/archive", `{"force":true}`, withCookie(g.ts)); w.Code != http.StatusOK {
		t.Fatalf("forced archive = %d %s", w.Code, w.Body)
	}
}

// A native Thread's own files include those only its native session diff
// lists, such as one a shell command wrote; after the conversation closes
// the last read stands, without resuming it, and survives a restart.
func TestSettleGuardReadsTheNativeSessionDiff(t *testing.T) {
	repo := sharedFolderWithOthersWork(t)
	prov := agenttest.NewProvider("native", agentapi.Capabilities{History: true, SessionDiff: true, SessionDiffNeedsTracking: true})
	st := openTestStore(t)
	m := startManager(t, st, prov)
	sum, err := m.Create(CreateRequest{Provider: "native", ProjectID: addProject(t, m, repo)})
	if err != nil {
		t.Fatal(err)
	}
	if refused := unsavedRefusal(t, m, sum.ID); refused != nil {
		t.Fatalf("refused with no work of its own: %+v", refused)
	}
	writeRepoFile(t, repo, "shell.txt", "printf\n")
	prov.Last().SetDiff([]agentapi.FileDiff{{Path: "shell.txt", Status: "added", Patch: "--- /dev/null\n+++ b/shell.txt\n@@ -0,0 +1 @@\n+printf\n"}}, nil)
	refused := unsavedRefusal(t, m, sum.ID)
	if refused == nil || *refused.Pending != (GitPending{Uncommitted: 1}) || !slices.Equal(refused.Refs, []string{"shell.txt"}) {
		t.Fatalf("native-only file refusal = %+v", refused)
	}
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	opens := len(prov.Opens())
	if refused := unsavedRefusal(t, m, sum.ID); refused == nil || !slices.Equal(refused.Refs, []string{"shell.txt"}) {
		t.Fatalf("closed Thread refusal = %+v", refused)
	}
	if len(prov.Opens()) != opens {
		t.Fatal("the guard resumed a closed conversation")
	}
	// The last read survives a restart; another file's dirt stays the others'.
	writeRepoFile(t, repo, "other.txt", "not mine\n")
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	prov = agenttest.NewProvider("native", agentapi.Capabilities{History: true, SessionDiff: true, SessionDiffNeedsTracking: true})
	m = startManager(t, st, prov)
	if refused := unsavedRefusal(t, m, sum.ID); refused == nil || *refused.Pending != (GitPending{Uncommitted: 1}) || !slices.Equal(refused.Refs, []string{"shell.txt"}) {
		t.Fatalf("restarted closed Thread refusal = %+v", refused)
	}
	if len(prov.Opens()) != 0 {
		t.Fatal("the guard resumed a closed conversation after a restart")
	}
	gitIn(t, repo, "add", "shell.txt")
	gitIn(t, repo, "commit", "-q", "-m", "feat: shell")
	if refused := unsavedRefusal(t, m, sum.ID); refused != nil {
		t.Fatalf("refused once its work was committed: %+v", refused)
	}
}

// The Wide diff case: Changes lists an edit-tool edit and a shell write the
// session tracking lists, the shell one marked; another change in the folder
// shows only among the others; who_touched names the session file; and the
// Settle guard still counts only the Thread's own files.
func TestChangesListSessionTrackedFilesAndOthers(t *testing.T) {
	repo := sharedFolderWithOthersWork(t)
	prov := agenttest.NewProvider("native", agentapi.Capabilities{History: true, SessionDiff: true, SessionDiffNeedsTracking: true})
	m := startManager(t, openTestStore(t), prov)
	sum, err := m.Create(CreateRequest{Provider: "native", ProjectID: addProject(t, m, repo)})
	if err != nil {
		t.Fatal(err)
	}
	writeRepoFile(t, repo, "README.md", "edited\n")
	setSession(m, sum.ID, func(s *webSession) { s.edits = map[string]time.Time{"README.md": time.Now()}; s.editsKnown = true })
	writeRepoFile(t, repo, "regions.json", "{}\n") // sed -i, say
	writeRepoFile(t, repo, "other.txt", "not mine\n")
	prov.Last().SetDiff([]agentapi.FileDiff{
		{Path: "README.md", Status: "modified", Patch: "@@ -1 +1 @@\n-x\n+edited\n"},
		{Path: "regions.json", Status: "added", Patch: "--- /dev/null\n+++ b/regions.json\n@@ -0,0 +1 @@\n+{}\n"},
	}, nil)

	out, err := m.Changes(t.Context(), sum.ID, ScopeTask)
	if err != nil {
		t.Fatal(err)
	}
	tracked := map[string]bool{}
	for _, f := range out.Files {
		tracked[f.Path] = f.SessionTracked
	}
	if len(out.Files) != 2 || tracked["README.md"] || !tracked["regions.json"] || out.Counts.Task != 2 {
		t.Fatalf("task files = %+v", out.Files)
	}
	others := map[string]bool{}
	for _, f := range out.Others {
		others[f.Path] = true
	}
	if !others["other.txt"] || !others["tracked.txt"] || others["README.md"] || others["regions.json"] {
		t.Fatalf("others = %+v", out.Others)
	}
	if _, err := m.FileChange(t.Context(), sum.ID, ScopeTask, "regions.json"); err != nil {
		t.Fatalf("session-tracked file diff: %v", err)
	}
	if turn, err := m.Changes(t.Context(), sum.ID, ScopeTurn); err != nil || turn.Others != nil {
		t.Fatalf("turn scope = %+v, %v", turn, err)
	}

	// Session tracking does not prove who wrote the file: no revert.
	var refusal *Error
	if err := m.RevertChange(t.Context(), sum.ID, "regions.json", ""); !errors.As(err, &refusal) || refusal.Status != http.StatusBadRequest {
		t.Fatalf("revert of a session-tracked file = %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(repo, "regions.json")); string(got) != "{}\n" {
		t.Fatalf("regions.json = %q", got)
	}

	var page uamTestPage
	uamReadJSON(t, prov.Last(), `{"op":"who_touched","args":{"path":"regions.json"}}`, &page)
	if len(page.Rows) != 1 || page.Rows[0]["id"] != sum.ID || page.Rows[0]["basis"] != "session tracking" {
		t.Fatalf("who_touched = %+v", page)
	}
	uamReadJSON(t, prov.Last(), `{"op":"who_touched","args":{"path":"other.txt"}}`, &page)
	if len(page.Rows) != 0 {
		t.Fatalf("who_touched other = %+v", page)
	}

	if refused := unsavedRefusal(t, m, sum.ID); refused == nil || *refused.Pending != (GitPending{Uncommitted: 2}) || !slices.Equal(refused.Refs, []string{"README.md", "regions.json"}) {
		t.Fatalf("settle refusal = %+v", refused)
	}
}

// A shell write the session diff lists is read as the turn ends and saved,
// so after a restart the Settle guard still counts it, though nothing read
// Changes while the conversation was open.
func TestSessionPathsReadAtTurnEndSurviveRestart(t *testing.T) {
	repo := sharedFolderWithOthersWork(t)
	prov := agenttest.NewProvider("native", agentapi.Capabilities{History: true, SessionDiff: true, SessionDiffNeedsTracking: true})
	st := openTestStore(t)
	m := startManager(t, st, prov)
	sum, err := m.Create(CreateRequest{Provider: "native", ProjectID: addProject(t, m, repo)})
	if err != nil {
		t.Fatal(err)
	}
	conv := prov.Last()
	conv.EmitTurn(agentapi.TurnWorking, "")
	writeRepoFile(t, repo, "shell.txt", "printf\n")
	conv.SetDiff([]agentapi.FileDiff{{Path: "shell.txt", Status: "added", Patch: "--- /dev/null\n+++ b/shell.txt\n@@ -0,0 +1 @@\n+printf\n"}}, nil)
	conv.EmitTurn(agentapi.TurnCompleted, "")
	waitUntil(t, "the turn-end session read", func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return slices.Equal(m.sessions[sum.ID].nativePaths, []string{"shell.txt"})
	})
	idle, err := m.Create(CreateRequest{Provider: "native", ProjectID: sum.ProjectID}) // never prompted
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	prov = agenttest.NewProvider("native", agentapi.Capabilities{History: true, SessionDiff: true, SessionDiffNeedsTracking: true})
	m = startManager(t, st, prov)
	if refused := unsavedRefusal(t, m, sum.ID); refused == nil || !slices.Equal(refused.Refs, []string{"shell.txt"}) {
		t.Fatalf("restarted refusal = %+v", refused)
	}
	if len(prov.Opens()) != 0 {
		t.Fatal("the guard resumed a closed conversation after a restart")
	}

	// Reading Changes lists the saved session paths and leaves the closed
	// Thread as it was: no open, no state change, no notice.
	before := mustSummary(t, m, sum.ID).State
	out, err := m.Changes(t.Context(), sum.ID, ScopeSession)
	if err != nil || out.Label != closedSessionLabel || len(out.Files) != 1 || out.Files[0].Path != "shell.txt" || !out.Files[0].SessionTracked {
		t.Fatalf("restarted session changes = %+v, %v", out, err)
	}
	if _, err := m.FileChange(t.Context(), sum.ID, ScopeSession, "shell.txt"); err != nil {
		t.Fatalf("restarted session file: %v", err)
	}
	if len(prov.Opens()) != 0 || mustSummary(t, m, sum.ID).State != before {
		t.Fatalf("reading Changes opened the conversation (%d) or moved the state to %s", len(prov.Opens()), mustSummary(t, m, sum.ID).State)
	}
	if _, err := m.Changes(t.Context(), idle.ID, ScopeSession); err != nil || len(prov.Opens()) != 0 || mustSummary(t, m, idle.ID).State != StateIdle {
		t.Fatalf("idle Thread after reading Changes: %v, opens %d, state %s", err, len(prov.Opens()), mustSummary(t, m, idle.ID).State)
	}
	for _, kind := range []string{noticeQuestion, noticePermission, noticeFailed, noticeFinished} {
		if n := countOf(m, "notices_"+kind); n != 0 {
			t.Fatalf("reading Changes fired %d %s notices", n, kind)
		}
	}
}

// A Thread in its own worktree owns all the work there: any changed tracked
// file and any commit on no remote stops Settle, and the quiet-Task sweep.
func TestSettleGuardInOwnWorktreeCountsTheWholeWorktree(t *testing.T) {
	repo := worktreeRepo(t)
	ts := newTestServer(t, ServerConfig{})
	sum, _ := worktreeThread(t, ts, repo, "feature")
	wt := sum.Worktree.Path
	writeRepoFile(t, wt, "README.md", "# changed by hand\n")
	refused := unsavedRefusal(t, ts.m, sum.ID)
	if refused == nil || *refused.Pending != (GitPending{Uncommitted: 1}) || !slices.Equal(refused.Refs, []string{"README.md"}) ||
		!strings.Contains(refused.Message, "worktree") {
		t.Fatalf("worktree refusal = %+v", refused)
	}
	gitIn(t, repo, "remote", "add", "origin", filepath.Join(t.TempDir(), "remote.git"))
	gitIn(t, wt, "commit", "-q", "-am", "docs: readme")
	if refused := unsavedRefusal(t, ts.m, sum.ID); refused == nil || refused.Pending.Uncommitted != 0 || refused.Pending.Unpushed == 0 {
		t.Fatalf("unpushed worktree refusal = %+v", refused)
	}
	setNow(ts.m, time.Now().Add(autoSettleAfter+time.Hour))
	ts.m.autoSettle()
	if got := summaryOf(t, ts.m, sum.ID); got.Stage != StageActive {
		t.Fatalf("settled a worktree with unpushed work: %+v", got)
	}
	// Without a remote there is nowhere to push: the worktree is clean.
	gitIn(t, repo, "remote", "remove", "origin")
	ts.m.autoSettle()
	if got := summaryOf(t, ts.m, sum.ID); got.Stage != StageSettled {
		t.Fatalf("clean quiet worktree Thread = %+v", got)
	}
}

// The 7-day sweep uses Settle's rule: a quiet Thread with its own unsaved
// files stays active, until they are saved, while one beside others' work
// settles.
func TestAutoSettleSparesUnsavedWork(t *testing.T) {
	repo := sharedFolderWithOthersWork(t)
	g := newGitTask(t, repo)
	writeRepoFile(t, repo, "notes.txt", "new\n")
	g.prov.Last().EmitItem(toolEdit("e1", "create", "notes.txt", agentapi.ToolCompleted, time.Now()))
	other, err := g.m.Create(CreateRequest{Provider: g.prov.Name(), ProjectID: summaryOf(t, g.m, g.id).ProjectID})
	if err != nil {
		t.Fatal(err)
	}
	setNow(g.m, time.Now().Add(autoSettleAfter+time.Hour))
	g.m.autoSettle()
	if got := summaryOf(t, g.m, g.id); got.Stage != StageActive {
		t.Fatalf("settled with unsaved work: %+v", got)
	}
	if got := summaryOf(t, g.m, other.ID); got.Stage != StageSettled || got.SettledBy != settledByAuto {
		t.Fatalf("quiet Thread beside others' work = %+v", got)
	}
	gitIn(t, repo, "add", "notes.txt")
	gitIn(t, repo, "commit", "-q", "-m", "docs: notes")
	g.m.autoSettle()
	if got := summaryOf(t, g.m, g.id); got.Stage != StageSettled || got.SettledBy != settledByAuto {
		t.Fatalf("clean quiet Thread = %+v", got)
	}
}

// A Thread whose only files are session-tracked (a shell write, no edit
// tool) still reads one file's diff, and the Manager stays usable.
func TestSessionTrackedFileDiffWithoutEdits(t *testing.T) {
	repo := sharedFolderWithOthersWork(t)
	prov := agenttest.NewProvider("native", agentapi.Capabilities{History: true, SessionDiff: true, SessionDiffNeedsTracking: true})
	m := startManager(t, openTestStore(t), prov)
	sum, err := m.Create(CreateRequest{Provider: "native", ProjectID: addProject(t, m, repo)})
	if err != nil {
		t.Fatal(err)
	}
	writeRepoFile(t, repo, "w2-shell.txt", "w2\n")
	prov.Last().SetDiff([]agentapi.FileDiff{{Path: "w2-shell.txt", Status: "added", Patch: "--- /dev/null\n+++ b/w2-shell.txt\n@@ -0,0 +1 @@\n+w2\n"}}, nil)
	if _, err := m.Changes(t.Context(), sum.ID, ScopeTask); err != nil {
		t.Fatal(err)
	}
	if _, err := m.FileChange(t.Context(), sum.ID, ScopeTask, "w2-shell.txt"); err != nil {
		t.Fatalf("session-tracked file diff: %v", err)
	}
	if _, err := m.Changes(t.Context(), sum.ID, ScopeTask); err != nil {
		t.Fatalf("after the file diff: %v", err)
	}
}
