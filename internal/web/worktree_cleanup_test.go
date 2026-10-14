package web

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func worktreeGone(path string) bool {
	_, err := os.Lstat(path)
	return errors.Is(err, fs.ErrNotExist)
}

// Settling removes a clean worktree, keeps its branch, and logs the
// removal; using the Thread again recreates it from the branch.
func TestSettleRemovesTheWorktreeAndReopenRecreatesIt(t *testing.T) {
	repo := worktreeRepo(t)
	ts := newTestServer(t, ServerConfig{})
	sum, _ := worktreeThread(t, ts, repo, "")
	path, branch := sum.Worktree.Path, sum.Worktree.Branch
	if _, err := ts.m.Rename(sum.ID, "Fix the parser"); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "branch named", func() bool {
		got, _ := ts.m.Summary(sum.ID)
		branch = got.Worktree.Branch
		return branch == "uam/fix-the-parser"
	})
	if _, err := ts.m.Settle(sum.ID); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "worktree removed", func() bool { return worktreeGone(path) })
	if got := gitOutput(t, repo, "branch", "--list", branch); !strings.Contains(got, branch) {
		t.Fatalf("branch gone: %q", got)
	}
	var removed []RemovedWorktree
	waitUntil(t, "removal logged", func() bool {
		removed = ts.m.removedLog()
		return len(removed) == 1
	})
	if r := removed[0]; r.Path != path || r.Branch != branch || r.TaskID != sum.ID || r.Thread != "Fix the parser" || r.SizeBytes <= 0 {
		t.Fatalf("removed = %+v", r)
	}
	// The list is kept in memory first and written to its file after.
	waitUntil(t, "removal log file", func() bool {
		data, err := os.ReadFile(ts.m.removedLogPath())
		return err == nil && strings.Contains(string(data), branch)
	})

	if _, err := ts.m.Reopen(sum.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.m.Submit(sum.ID, PromptRequest{Text: "again", RequestID: mustUUID(t)}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "worktree recreated", func() bool { return !worktreeGone(filepath.Join(path, "README.md")) })
	if got := gitOutput(t, path, "symbolic-ref", "--short", "HEAD"); got != branch {
		t.Fatalf("recreated on %q", got)
	}
	// Back in use, it is listed once: among the worktrees, not as removed.
	list, err := ts.m.ListWorktrees(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(list.Worktrees, func(e WorktreeEntry) bool { return e.Path == path }) || slices.ContainsFunc(list.Removed, func(r RemovedWorktree) bool { return r.Path == path }) {
		t.Fatalf("worktrees = %+v, removed = %+v", list.Worktrees, list.Removed)
	}
}

// A worktree with changes is kept on settling and listed with them.
func TestSettleKeepsAWorktreeWithChanges(t *testing.T) {
	repo := worktreeRepo(t)
	ts := newTestServer(t, ServerConfig{})
	sum, _ := worktreeThread(t, ts, repo, "")
	path := sum.Worktree.Path
	writeRepoFile(t, path, "README.md", "changed\n")
	writeRepoFile(t, path, "new.txt", "new\n")
	gitIn(t, path, "commit", "-q", "--allow-empty", "-m", "local")
	if _, err := ts.m.Archive(sum.ID); err != nil {
		t.Fatal(err)
	}
	var list Worktrees
	waitUntil(t, "sizes", func() bool {
		var err error
		if list, err = ts.m.ListWorktrees(t.Context()); err != nil {
			t.Fatal(err)
		}
		return len(list.Worktrees) == 1 && list.Worktrees[0].SizeBytes != nil
	})
	e := list.Worktrees[0]
	if worktreeGone(path) || e.Path != path || e.TaskID != sum.ID || e.Stage != StageArchived || e.Uncommitted != 2 || e.Unpushed != 3 || e.Branch != sum.Worktree.Branch || *e.SizeBytes <= 0 {
		t.Fatalf("listed = %+v", e)
	}
	// The Thread's summary carries the size for its branch chip.
	if got, _ := ts.m.Summary(sum.ID); got.Worktree.SizeBytes != *e.SizeBytes {
		t.Fatalf("summary size = %d, want %d", got.Worktree.SizeBytes, *e.SizeBytes)
	}
	if list.Root != ts.m.worktreeRoot || list.Disk == nil || list.Disk.TotalBytes == 0 || len(list.Removed) != 0 {
		t.Fatalf("list = %+v", list)
	}
}

// Remove asks before losing changes, keeps the branch, and needs a path
// from the list.
func TestRemoveWorktreeRoute(t *testing.T) {
	repo := worktreeRepo(t)
	ts := newTestServer(t, ServerConfig{})
	sum, _ := worktreeThread(t, ts, repo, "")
	path := sum.Worktree.Path
	writeRepoFile(t, path, "new.txt", "new\n")
	auth := withCookie(ts)
	remove := func(body any) (int, map[string]any) {
		raw, _ := json.Marshal(body)
		w := ts.do(http.MethodPost, "/api/worktrees/remove", string(raw), auth)
		var reply map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &reply)
		return w.Code, reply
	}
	if code, reply := remove(map[string]any{"path": path}); code != http.StatusConflict || reply["code"] != codeWorktreeUnsaved || reply["error"] != "1 changed file is not committed and would be lost." {
		t.Fatalf("unforced = %d %v", code, reply)
	}
	for _, bad := range []string{repo, filepath.Dir(path), path + "/..", "/etc"} {
		if code, _ := remove(map[string]any{"path": bad, "force": true}); code != http.StatusNotFound {
			t.Fatalf("%s = %d", bad, code)
		}
	}
	setSession(ts.m, sum.ID, func(s *webSession) { s.base = StateWorking })
	if code, reply := remove(map[string]any{"path": path, "force": true}); code != http.StatusConflict || !strings.Contains(reply["error"].(string), "is working in this worktree") {
		t.Fatalf("working = %d %v", code, reply)
	}
	setSession(ts.m, sum.ID, func(s *webSession) { s.base = StateCompleted })
	if code, reply := remove(map[string]any{"path": path, "force": true}); code != http.StatusNoContent {
		t.Fatalf("forced = %d %v", code, reply)
	}
	if !worktreeGone(path) || !strings.Contains(gitOutput(t, repo, "branch", "--list", sum.Worktree.Branch), sum.Worktree.Branch) {
		t.Fatal("not removed, or the branch went with it")
	}
	// The owner's removal is not one on settling.
	if got := ts.m.removedLog(); len(got) != 0 {
		t.Fatalf("logged = %+v", got)
	}
	w := ts.do(http.MethodGet, "/api/worktrees", "", auth)
	var list Worktrees
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || w.Code != http.StatusOK || len(list.Worktrees) != 0 {
		t.Fatalf("list = %d %s", w.Code, w.Body)
	}
	// An empty log is [], never null: the Settings page reads its length.
	if !strings.Contains(w.Body.String(), `"removed":[]`) {
		t.Fatalf("removed not an empty array: %s", w.Body)
	}
}

func TestMetaCarriesTheDisk(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	ts.m.worktreeRoot = filepath.Join(t.TempDir(), "not", "made", "yet")
	w := ts.do(http.MethodGet, "/api/meta", "", withCookie(ts))
	var meta Meta
	if err := json.Unmarshal(w.Body.Bytes(), &meta); err != nil || meta.Disk == nil || meta.Disk.TotalBytes == 0 || meta.Disk.FreeBytes > meta.Disk.TotalBytes {
		t.Fatalf("meta = %d %s", w.Code, w.Body)
	}
	if meta.Disk.Low != (float64(meta.Disk.FreeBytes) < 0.1*float64(meta.Disk.TotalBytes)) {
		t.Fatalf("low = %+v", meta.Disk)
	}
}

// Run again on a worktree Thread runs in its worktree, recreated from the
// branch when settling removed it, and the worktree stays while the new
// Thread is active.
func TestRunAgainRunsInTheWorktree(t *testing.T) {
	repo := worktreeRepo(t)
	ts := newTestServer(t, ServerConfig{})
	sum, _ := worktreeThread(t, ts, repo, "")
	path, branch := sum.Worktree.Path, sum.Worktree.Branch
	finishTurn(ts.prov.Last(), "fix it", "done")
	if _, err := ts.m.Settle(sum.ID); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "worktree removed", func() bool { return worktreeGone(path) })

	again, err := ts.m.Rerun(sum.ID, RerunRequest{RequestID: mustUUID(t)})
	if err != nil {
		t.Fatal(err)
	}
	if again.Workdir != sum.Workdir || again.Worktree.Path != path || again.Worktree.Branch != branch || again.Branch != branch {
		t.Fatalf("run again = workdir %q, worktree %+v, branch %q", again.Workdir, again.Worktree, again.Branch)
	}
	if opens := ts.prov.Opens(); opens[len(opens)-1].Workdir != sum.Workdir {
		t.Fatalf("conversation opened in %q", opens[len(opens)-1].Workdir)
	}
	if got := gitOutput(t, path, "symbolic-ref", "--short", "HEAD"); got != branch {
		t.Fatalf("recreated on %q", got)
	}
	if sends := ts.prov.Last().Sends(); !slices.Equal(sends, []string{"fix it"}) {
		t.Fatalf("run again sent %q", sends)
	}
	ts.m.mu.Lock()
	inUse := ts.m.worktreeInUseLocked(path)
	ts.m.mu.Unlock()
	if !inUse {
		t.Fatal("the new Thread's worktree would be removed with the settled one")
	}
}

// A Thread whose worktree was removed keeps showing its branch.
func TestSettledWorktreeThreadKeepsItsBranch(t *testing.T) {
	repo := worktreeRepo(t)
	ts := newTestServer(t, ServerConfig{})
	sum, project := worktreeThread(t, ts, repo, "")
	if _, err := ts.m.Settle(sum.ID); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "worktree removed", func() bool { return worktreeGone(sum.Worktree.Path) })
	ts.m.refreshBranches(t.Context(), true, project)
	if got, _ := ts.m.Summary(sum.ID); got.Branch != sum.Worktree.Branch || got.Worktree.Branch != sum.Worktree.Branch {
		t.Fatalf("branch = %q, worktree %+v", got.Branch, got.Worktree)
	}
}

// Branching from a turn is refused in a worktree with the reason and what
// to do instead.
func TestWorktreeThreadForkSaysWhy(t *testing.T) {
	repo := worktreeRepo(t)
	ts := newTestServer(t, ServerConfig{})
	sum, _ := worktreeThread(t, ts, repo, "")
	_, err := ts.m.Fork(sum.ID, ForkRequest{UserItemID: "u-1", RequestID: mustUUID(t)})
	if statusOf(err) != http.StatusConflict || !strings.Contains(err.Error(), "Project folder") || !strings.Contains(err.Error(), "Run again") {
		t.Fatalf("fork = %v", err)
	}
}
