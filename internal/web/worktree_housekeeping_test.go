package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A Project whose Threads' worktrees are still on disk is not removed: with
// it gone nothing would list them.
func TestRemoveProjectRefusedWhileItsWorktreesAreOnDisk(t *testing.T) {
	repo := worktreeRepo(t)
	ts := newTestServer(t, ServerConfig{})
	sum, project := worktreeThread(t, ts, repo, "")
	wt := sum.Worktree.Path
	// Not committed, so archiving keeps the worktree.
	writeRepoFile(t, wt, "draft.txt", "draft\n")
	if _, err := ts.m.Archive(sum.ID); err != nil {
		t.Fatal(err)
	}
	err := ts.m.RemoveProject(project)
	if statusOf(err) != http.StatusConflict || !strings.Contains(err.Error(), "1 worktree of this project is still on disk ("+wt+")") || !strings.Contains(err.Error(), "Settings → Projects & worktrees") {
		t.Fatalf("remove with a worktree on disk = %v", err)
	}
	if err := ts.m.RemoveWorktree(t.Context(), wt, true); err != nil {
		t.Fatal(err)
	}
	if err := ts.m.RemoveProject(project); err != nil {
		t.Fatalf("remove once the worktree is gone = %v", err)
	}
}

// A sub-folder Project whose folder is not on the base branch gets no
// worktree: the Thread would start in a folder that is not there.
func TestWorktreeRefusedWhenTheProjectFolderIsNotOnTheBase(t *testing.T) {
	repo := worktreeRepo(t)
	dir := filepath.Join(repo, "scratch")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	ts := newTestServer(t, ServerConfig{})
	root := useWorktreeRoot(t, ts.m)
	project := addProject(t, ts.m, dir)
	_, err := ts.m.Create(CreateRequest{Provider: ts.prov.Name(), ProjectID: project, Place: &PlaceRequest{Mode: placeWorktree}})
	if statusOf(err) != http.StatusConflict || !strings.Contains(err.Error(), "the folder scratch is not on main") {
		t.Fatalf("create = %v", err)
	}
	if left := worktreeDirs(root); len(left) != 0 || gitOutput(t, repo, "branch", "--list", branchPrefix+"*") != "" {
		t.Fatalf("left behind: worktrees %v, branches %q", left, gitOutput(t, repo, "branch", "--list", branchPrefix+"*"))
	}
}

// The hourly sweep deletes the uam/… branch a Thread left when the owner
// settled, archived or deleted it, once its worktree is gone and its work
// is proven on origin's default branch, and no other.
func TestLandedBranchesOfThreadsSettledByHandAreRemoved(t *testing.T) {
	// settled puts the Thread away by hand and waits for its worktree to go.
	settled := func(t *testing.T, ts *testServer, sum SessionSummary) {
		t.Helper()
		if _, err := ts.m.Settle(sum.ID); err != nil {
			t.Fatal(err)
		}
		waitUntil(t, "worktree removed", func() bool { return worktreeGone(sum.Worktree.Path) })
	}
	for name, tc := range map[string]struct {
		land func(t *testing.T, ts *testServer, repo string, sum SessionSummary)
		gone bool
	}{
		"merged": {func(t *testing.T, ts *testServer, repo string, sum SessionSummary) {
			gitIn(t, repo, "merge", "-q", "--no-ff", "-m", "merge", sum.Worktree.Branch)
			settled(t, ts, sum)
		}, true},
		"squashed": {func(t *testing.T, ts *testServer, repo string, sum SessionSummary) {
			squashInto(t, repo, sum.Worktree.Branch)
			settled(t, ts, sum)
		}, true},
		"deleted Thread": {func(t *testing.T, ts *testServer, repo string, sum SessionSummary) {
			gitIn(t, repo, "merge", "-q", "--no-ff", "-m", "merge", sum.Worktree.Branch)
			if _, err := ts.m.Archive(sum.ID); err != nil {
				t.Fatal(err)
			}
			waitUntil(t, "worktree removed", func() bool { return worktreeGone(sum.Worktree.Path) })
			if err := ts.m.Delete(sum.ID); err != nil {
				t.Fatal(err)
			}
		}, true},
		"not landed": {func(t *testing.T, ts *testServer, _ string, sum SessionSummary) { settled(t, ts, sum) }, false},
		"a commit reset away": {func(t *testing.T, ts *testServer, repo string, sum SessionSummary) {
			commitFile(t, sum.Worktree.Path, "lost.txt", "lost\n")
			gitIn(t, sum.Worktree.Path, "reset", "-q", "--hard", "HEAD~1")
			gitIn(t, repo, "merge", "-q", "--no-ff", "-m", "merge", sum.Worktree.Branch)
			settled(t, ts, sum)
		}, false},
		"worktree still on disk": {func(t *testing.T, ts *testServer, repo string, sum SessionSummary) {
			gitIn(t, repo, "merge", "-q", "--no-ff", "-m", "merge", sum.Worktree.Branch)
			writeRepoFile(t, sum.Worktree.Path, "draft.txt", "draft\n")
			if _, err := ts.m.Settle(sum.ID); err != nil {
				t.Fatal(err)
			}
			// Off the branch, so only the Thread's record still names it.
			gitIn(t, sum.Worktree.Path, "switch", "-q", "--detach")
		}, false},
		"active Thread": {func(t *testing.T, ts *testServer, repo string, sum SessionSummary) {
			gitIn(t, repo, "merge", "-q", "--no-ff", "-m", "merge", sum.Worktree.Branch)
			if err := ts.m.RemoveWorktree(t.Context(), sum.Worktree.Path, false); err != nil {
				t.Fatal(err)
			}
		}, false},
	} {
		t.Run(name, func(t *testing.T) {
			ts, repo, sum, land := mergedThread(t)
			tc.land(t, ts, repo, sum)
			land()
			ts.m.removeLandedBranches()
			if gone := !branchExists(t, repo, sum.Worktree.Branch); gone != tc.gone {
				t.Fatalf("branch gone = %v, want %v", gone, tc.gone)
			}
			if !branchExists(t, repo, "feature") {
				t.Fatal("removed a branch uam did not make")
			}
		})
	}
}
