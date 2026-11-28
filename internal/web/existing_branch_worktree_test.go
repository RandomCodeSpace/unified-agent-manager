package web

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

// A worktree on an existing local branch checks that branch out: commits
// land on it, no uam/ branch is made, and settling, reopening, archiving and
// deleting never delete it.
func TestWorktreeOnAnExistingBranch(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	repo, project := switchProject(t, ts)
	code, sum, no := createWithPlace(t, ts, project, `{"mode":"worktree","branch":"feature"}`)
	if code != http.StatusCreated || sum.Worktree.Branch != "feature" || !sum.Worktree.Existing || sum.Worktree.Base != "" {
		t.Fatalf("create = %d %+v %+v", code, sum.Worktree, no)
	}
	if got := headOf(t, sum.Workdir); got != "feature" {
		t.Fatalf("worktree HEAD = %q", got)
	}
	commitFile(t, sum.Workdir, "work.txt", "work\n")
	if got := gitOutput(t, repo, "ls-tree", "--name-only", "feature"); !strings.Contains(got, "work.txt") {
		t.Fatalf("feature lacks the commit: %q", got)
	}
	if got := gitOutput(t, repo, "branch", "--list", "uam/*"); got != "" {
		t.Fatalf("uam branches = %q", got)
	}
	ts.m.mu.Lock()
	text := ts.m.placeInstructionsLocked(ts.m.sessions[sum.ID])
	ts.m.mu.Unlock()
	if !strings.Contains(text, "existing branch feature") || !strings.Contains(text, "git push -u origin HEAD") {
		t.Fatalf("instructions = %q", text)
	}

	if _, err := ts.m.Settle(sum.ID); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "worktree removed", func() bool { return worktreeGone(sum.Worktree.Path) })
	if _, err := ts.m.Reopen(sum.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.m.Submit(sum.ID, PromptRequest{Text: "again", RequestID: mustUUID(t)}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "worktree recreated", func() bool { return !worktreeGone(filepath.Join(sum.Workdir, "work.txt")) })
	finishTurn(ts.prov.Last(), "again", "done")
	if _, err := ts.m.Archive(sum.ID); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "worktree removed on archive", func() bool { return worktreeGone(sum.Worktree.Path) })
	if err := ts.m.Delete(sum.ID); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "worktree removed on delete", func() bool { return worktreeGone(sum.Worktree.Path) })
	if got := gitOutput(t, repo, "branch", "--list", "feature"); !strings.Contains(got, "feature") {
		t.Fatalf("feature deleted: %q", got)
	}

	// A branch checked out elsewhere, here main in the Project folder, is refused by name.
	code, _, no = createWithPlace(t, ts, project, `{"mode":"worktree","branch":"main"}`)
	if code != http.StatusConflict || !strings.Contains(no.Error, repo) || !strings.Contains(no.Error, "New branch from") {
		t.Fatalf("main = %d %+v", code, no)
	}
	for _, bad := range []string{`{"mode":"worktree","branch":"feature","base":"main"}`, `{"mode":"worktree","branch":"-f"}`} {
		if code, _, no := createWithPlace(t, ts, project, bad); code != http.StatusBadRequest {
			t.Fatalf("place %s = %d %+v", bad, code, no)
		}
	}
	if code, _, no := createWithPlace(t, ts, project, `{"mode":"worktree","branch":"nope"}`); code != http.StatusBadRequest || !strings.Contains(no.Error, "not a") {
		t.Fatalf("missing = %d %+v", code, no)
	}
}

// A remote branch no local branch has is fetched and checked out on a new
// local branch that tracks it; one a local branch shares a name with is
// chosen under Local.
func TestWorktreeOnARemoteBranchTracksIt(t *testing.T) {
	repo := worktreeRepo(t)
	origin := filepath.Join(t.TempDir(), "origin.git")
	gitIn(t, repo, "init", "-q", "--bare", origin)
	gitIn(t, repo, "remote", "add", "origin", origin)
	gitIn(t, repo, "push", "-q", "origin", "feature:remote-only", "feature")
	ts := newTestServer(t, ServerConfig{})
	useWorktreeRoot(t, ts.m)
	project := addProject(t, ts.m, repo)
	code, sum, no := createWithPlace(t, ts, project, `{"mode":"worktree","branch":"origin/remote-only","remote":true}`)
	if code != http.StatusCreated || sum.Worktree.Branch != "remote-only" || !sum.Worktree.Existing {
		t.Fatalf("create = %d %+v %+v", code, sum.Worktree, no)
	}
	if got := gitOutput(t, repo, "for-each-ref", "--format=%(upstream:short)", "refs/heads/remote-only"); got != "origin/remote-only" {
		t.Fatalf("upstream = %q", got)
	}
	code, _, no = createWithPlace(t, ts, project, `{"mode":"worktree","branch":"origin/feature","remote":true}`)
	if code != http.StatusConflict || !strings.Contains(no.Error, "choose it under Local") {
		t.Fatalf("shadowed = %d %+v", code, no)
	}
	if code, _, no := createWithPlace(t, ts, project, `{"mode":"current","branch":"feature"}`); code != http.StatusBadRequest {
		t.Fatalf("branch outside switch and worktree = %d %+v", code, no)
	}
}

// A worktree on an existing branch is still one after a restart, and a
// failed create takes the worktree back but never the branch.
func TestExistingBranchWorktreeSurvivesRestartAndFailedCreate(t *testing.T) {
	repo := worktreeRepo(t)
	st := openTestStore(t)
	m := startManager(t, st, agenttest.NewProvider("fake", allCaps))
	useWorktreeRoot(t, m)
	project := addProject(t, m, repo)
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Place: &PlaceRequest{Mode: placeWorktree, Branch: "feature"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	again := startManager(t, st, agenttest.NewProvider("fake", allCaps))
	got, err := again.Summary(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Workdir != sum.Workdir || got.Worktree != sum.Worktree || !got.Worktree.Existing {
		t.Fatalf("after restart = %+v, want %+v", got.Worktree, sum.Worktree)
	}

	gitIn(t, repo, "branch", "other", "feature")
	ts := newTestServer(t, ServerConfig{})
	useWorktreeRoot(t, ts.m)
	other := addProject(t, ts.m, repo)
	ts.prov.SetOpenError(context.DeadlineExceeded)
	if _, err := ts.m.Create(CreateRequest{Provider: ts.prov.Name(), ProjectID: other, Place: &PlaceRequest{Mode: placeWorktree, Branch: "other"}}); err == nil {
		t.Fatal("create succeeded")
	}
	if out := gitOutput(t, repo, "worktree", "list", "--porcelain"); strings.Contains(out, "refs/heads/other") {
		t.Fatalf("worktrees = %q", out)
	}
	if got := gitOutput(t, repo, "branch", "--list", "other"); !strings.Contains(got, "other") {
		t.Fatalf("undo deleted the branch: %q", got)
	}
}
