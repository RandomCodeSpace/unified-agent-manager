package web

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
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

// Settle and Archive refuse a Thread whose repository has unsaved work
// unless forced, naming the counts and the first changed files; a clean
// Thread settles as before.
func TestSettleAndArchiveAskFirstWithUnsavedWork(t *testing.T) {
	gitIdentity(t)
	repo := gitRepoFixture(t)
	g := newGitTask(t, repo)
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
		if refused.Code != codeUnsavedWork || refused.Pending != (GitPending{Uncommitted: 1}) || !slices.Equal(refused.Refs, []string{"tracked.txt"}) ||
			!strings.Contains(refused.Error, "1 not committed") {
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

	gitIn(t, repo, "commit", "-q", "-am", "fix: tracked")
	clean, err := g.m.Create(CreateRequest{Provider: g.prov.Name(), ProjectID: summaryOf(t, g.m, g.id).ProjectID})
	if err != nil {
		t.Fatal(err)
	}
	if w := g.ts.do(http.MethodPost, "/api/sessions/"+clean.ID+"/settle", "", withCookie(g.ts)); w.Code != http.StatusOK {
		t.Fatalf("clean settle = %d %s", w.Code, w.Body)
	}
}

// The 7-day sweep leaves a quiet Thread with unsaved work active and
// settles it once the work is saved.
func TestAutoSettleSparesUnsavedWork(t *testing.T) {
	gitIdentity(t)
	repo := gitRepoFixture(t)
	g := newGitTask(t, repo)
	setNow(g.m, summaryOf(t, g.m, g.id).UpdatedAt.Add(autoSettleAfter))
	g.m.autoSettle()
	if got := summaryOf(t, g.m, g.id); got.Stage != StageActive {
		t.Fatalf("settled with unsaved work: %+v", got)
	}
	gitIn(t, repo, "commit", "-q", "-am", "fix: tracked")
	g.m.autoSettle()
	if got := summaryOf(t, g.m, g.id); got.Stage != StageSettled || got.SettledBy != settledByAuto {
		t.Fatalf("clean quiet Thread = %+v", got)
	}
}
