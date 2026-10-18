package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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
	g.call(http.MethodPost, "/commit", `{"paths":["tracked.txt"],"message":"fix: one"}`, http.StatusOK, nil)
	// No remote-tracking branch yet: every commit is unpushed.
	want("after commit", GitPending{Unpushed: 2})
	g.call(http.MethodPost, "/push", "", http.StatusOK, nil)
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

	for _, action := range []string{"settle", "archive"} {
		w := g.ts.do(http.MethodPost, "/api/sessions/"+g.id+"/"+action, "", withCookie(g.ts))
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
// the last read stands, without resuming it.
func TestSettleGuardReadsTheNativeSessionDiff(t *testing.T) {
	repo := sharedFolderWithOthersWork(t)
	prov := agenttest.NewProvider("native", agentapi.Capabilities{History: true, SessionDiff: true, SessionDiffNeedsTracking: true})
	m := startManager(t, openTestStore(t), prov)
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
	gitIn(t, repo, "add", "shell.txt")
	gitIn(t, repo, "commit", "-q", "-m", "feat: shell")
	if refused := unsavedRefusal(t, m, sum.ID); refused != nil {
		t.Fatalf("refused once its work was committed: %+v", refused)
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
