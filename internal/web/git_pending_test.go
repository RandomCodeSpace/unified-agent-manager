package web

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
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
	if n := countTrackedChanges([]byte(status)); n != 4 {
		t.Fatalf("count = %d, want 4", n)
	}
	if n := countTrackedChanges(nil); n != 0 {
		t.Fatalf("empty count = %d", n)
	}
}
