package web

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// worktreeForkManager is taskForkManager with the source Thread in its own
// worktree of worktreeRepo, started from main.
func worktreeForkManager(t *testing.T) (*Manager, *taskForkProvider, *store.Store, SessionSummary, string) {
	t.Helper()
	repo := worktreeRepo(t)
	caps := allCaps
	caps.Fork, caps.Import, caps.ContextSize = true, true, true
	p := &taskForkProvider{Pager: agenttest.NewPager("fake", caps)}
	p.SetModels(selectionModels(), nil)
	st := openTestStore(t)
	m := startManager(t, st, p)
	useWorktreeRoot(t, m)
	project := addProject(t, m, repo)
	source, err := m.Create(CreateRequest{ProjectID: project, Provider: "fake", Model: "a", Mode: "yolo", Name: "Source work", Place: &PlaceRequest{Mode: placeWorktree, Base: "main"}})
	if err != nil {
		t.Fatal(err)
	}
	if source.Worktree.Path == "" {
		t.Fatal("source has no worktree")
	}
	finishTurn(p.Last(), "original", "original reply")
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	return m, p, st, source, repo
}

// folderSnapshot is what a worktree holds: its HEAD, its status with
// untracked and ignored files, and the bytes of the named files.
func folderSnapshot(t *testing.T, dir string, names ...string) string {
	t.Helper()
	state := gitOutput(t, dir, "rev-parse", "HEAD") + "\n" + gitOutput(t, dir, "status", "--porcelain", "--ignored", "--untracked-files=all")
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		state += "\n" + name + "=" + string(data)
	}
	return state
}

func TestForkWorktreeThreadStartsANewWorktreeAtTheSourceHead(t *testing.T) {
	m, _, st, source, repo := worktreeForkManager(t)
	before := folderSnapshot(t, source.Worktree.Path)
	branch, err := m.Fork(source.ID, ForkRequest{UserItemID: "u-original", Model: "a", RequestID: mustUUID(t)})
	if err != nil {
		t.Fatal(err)
	}
	if branch.Worktree.Path == source.Worktree.Path || branch.Workdir != branch.Worktree.Path || branch.Branch == source.Branch || !strings.HasPrefix(branch.Branch, "uam/") {
		t.Fatalf("branch = %+v worktree %+v, source worktree %+v", branch, branch.Worktree, source.Worktree)
	}
	if got, want := gitOutput(t, branch.Workdir, "rev-parse", "HEAD"), gitOutput(t, source.Workdir, "rev-parse", "HEAD"); got != want {
		t.Fatalf("branch HEAD %s, source HEAD %s", got, want)
	}
	if got := gitOutput(t, branch.Workdir, "symbolic-ref", "--short", "HEAD"); got != branch.Branch {
		t.Fatalf("branch worktree HEAD = %q", got)
	}
	if out := gitOutput(t, branch.Workdir, "status", "--porcelain"); out != "" {
		t.Fatalf("clean source left changes: %q", out)
	}
	if after := folderSnapshot(t, source.Worktree.Path); after != before {
		t.Fatalf("source changed:\n%s\n%s", before, after)
	}
	if got := gitOutput(t, repo, "symbolic-ref", "--short", "HEAD"); got != "main" {
		t.Fatalf("project HEAD = %q", got)
	}
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	rec := cfg.Sessions[store.Key("fake", branch.ID)]
	if rec.Workdir != branch.Workdir || rec.Web.Place == nil || rec.Web.Place.Worktree != branch.Worktree.Path || rec.Web.Place.Base != "main" || len(cfg.WebForks) != 0 {
		t.Fatalf("record = %+v place %+v, pending %+v", rec, rec.Web.Place, cfg.WebForks)
	}
}

func TestForkWorktreeThreadCarriesUncommittedAndUntrackedFiles(t *testing.T) {
	m, _, _, source, _ := worktreeForkManager(t)
	dir := source.Worktree.Path
	writeRepoFile(t, dir, "README.md", "# demo\nchanged\n")
	if err := os.WriteFile(filepath.Join(dir, "shot.png"), append(pngBytes(t), 0, 1, 2), 0o644); err != nil {
		t.Fatal(err)
	}
	writeRepoFile(t, dir, "docs/staged.md", "staged\n")
	gitIn(t, dir, "add", "docs/staged.md")
	writeRepoFile(t, dir, ".gitignore", "*.log\n")
	writeRepoFile(t, dir, "notes/todo.txt", "untracked\n")
	writeRepoFile(t, dir, "build.log", "ignored\n")
	if err := os.Symlink("README.md", filepath.Join(dir, "readme-link")); err != nil {
		t.Fatal(err)
	}
	carried := []string{"README.md", "shot.png", "docs/staged.md", ".gitignore", "notes/todo.txt"}
	before := folderSnapshot(t, dir, append(carried, "build.log")...)

	branch, err := m.Fork(source.ID, ForkRequest{UserItemID: "u-original", Model: "a", RequestID: mustUUID(t)})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range carried {
		want, _ := os.ReadFile(filepath.Join(dir, name))
		got, err := os.ReadFile(filepath.Join(branch.Workdir, name))
		if err != nil || string(got) != string(want) {
			t.Fatalf("%s = %q, %v; want %q", name, got, err, want)
		}
	}
	if target, err := os.Readlink(filepath.Join(branch.Workdir, "readme-link")); err != nil || target != "README.md" {
		t.Fatalf("symlink = %q, %v", target, err)
	}
	if _, err := os.Lstat(filepath.Join(branch.Workdir, "build.log")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ignored file copied: %v", err)
	}
	if after := folderSnapshot(t, dir, append(carried, "build.log")...); after != before {
		t.Fatalf("source changed:\n%s\n%s", before, after)
	}
}

func TestForkWorktreeThreadTakesTheWorktreeBackOnFailure(t *testing.T) {
	assertNoBranchWorktree := func(t *testing.T, m *Manager, st *store.Store, repo string) {
		t.Helper()
		if out := gitOutput(t, repo, "worktree", "list", "--porcelain"); strings.Count(out, "worktree ") != 2 {
			t.Fatalf("worktrees = %q", out)
		}
		if out := gitOutput(t, repo, "branch", "--list", "uam/*"); strings.Count(out, "uam/") != 1 {
			t.Fatalf("branches = %q", out)
		}
		if n := len(pendingForks(t, st)); n != 0 {
			t.Fatalf("pending forks = %d", n)
		}
		entries, _ := os.ReadDir(filepath.Join(m.worktreeRoot, filepath.Base(repo)))
		if len(entries) != 1 {
			t.Fatalf("worktree folders = %d", len(entries))
		}
	}
	t.Run("provider refuses", func(t *testing.T) {
		m, p, st, source, repo := worktreeForkManager(t)
		p.forkHook = func(agentapi.ForkRequest) (string, error) { return "", errors.New("refused") }
		if _, err := m.Fork(source.ID, ForkRequest{UserItemID: "u-original", Model: "a", RequestID: mustUUID(t)}); err == nil {
			t.Fatal("fork succeeded")
		}
		assertNoBranchWorktree(t, m, st, repo)
	})
	t.Run("an untracked file cannot be read", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root reads any file")
		}
		m, p, st, source, repo := worktreeForkManager(t)
		locked := filepath.Join(source.Worktree.Path, "secret.txt")
		if err := os.WriteFile(locked, []byte("x"), 0o000); err != nil {
			t.Fatal(err)
		}
		_, err := m.Fork(source.ID, ForkRequest{UserItemID: "u-original", Model: "a", RequestID: mustUUID(t)})
		if err == nil || !strings.Contains(err.Error(), "secret.txt") {
			t.Fatalf("err = %v", err)
		}
		if p.forkCount() != 0 {
			t.Fatal("forked after the copy failed")
		}
		assertNoBranchWorktree(t, m, st, repo)
	})
}

// A source whose worktree uam removed, which held no changes, branches from
// the source's branch.
func TestForkWorktreeThreadWhoseWorktreeWasRemoved(t *testing.T) {
	m, _, _, source, repo := worktreeForkManager(t)
	tip := gitOutput(t, source.Worktree.Path, "rev-parse", "HEAD")
	gitIn(t, repo, "worktree", "remove", source.Worktree.Path)
	branch, err := m.Fork(source.ID, ForkRequest{UserItemID: "u-original", Model: "a", RequestID: mustUUID(t)})
	if err != nil {
		t.Fatal(err)
	}
	if got := gitOutput(t, branch.Workdir, "rev-parse", "HEAD"); got != tip || branch.Worktree.Path == source.Worktree.Path {
		t.Fatalf("branch HEAD %s, want %s; worktree %+v", got, tip, branch.Worktree)
	}
	if _, err := os.Stat(source.Worktree.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source worktree recreated: %v", err)
	}
}

// Dismissing an uncertain branch request removes the worktree and branch
// made for it; one with changes is kept.
func TestForkWorktreeThreadDismissRemovesItsWorktree(t *testing.T) {
	m, p, st, source, repo := worktreeForkManager(t)
	p.forkHook = func(agentapi.ForkRequest) (string, error) { return "", agentapi.ErrForkUncertain }
	uncertain := func(model string) store.WebFork {
		t.Helper()
		req := ForkRequest{UserItemID: "u-original", Model: model, RequestID: mustUUID(t)}
		if _, err := m.Fork(source.ID, req); !errors.Is(err, errForkUncertain) {
			t.Fatalf("fork = %v", err)
		}
		for _, pending := range pendingForks(t, st) {
			if pending.Lineage.RequestID == req.RequestID && pending.Place != nil {
				return pending
			}
		}
		t.Fatal("no reservation with a worktree")
		return store.WebFork{}
	}
	clean, dirty := uncertain("a"), uncertain("")
	writeRepoFile(t, dirty.Place.Worktree, "work.txt", "keep me\n")
	for _, fork := range []store.WebFork{dirty, clean} {
		if err := m.DismissFork(source.ID, ForkRequest{UserItemID: "u-original", Model: fork.Lineage.Model}); err != nil {
			t.Fatal(err)
		}
	}
	waitUntil(t, "clean worktree and branch removed", func() bool {
		return worktreeGone(clean.Place.Worktree) && gitOutput(t, repo, "branch", "--list", clean.Place.Branch) == ""
	})
	// The dirty one was dismissed first; git refuses to remove it.
	for deadline := time.Now().Add(300 * time.Millisecond); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(filepath.Join(dirty.Place.Worktree, "work.txt")); err != nil {
			t.Fatalf("dirty worktree removed: %v", err)
		}
	}
	if out := gitOutput(t, repo, "branch", "--list", dirty.Place.Branch); out == "" {
		t.Fatal("dirty worktree's branch removed")
	}
	if _, err := os.Stat(source.Worktree.Path); err != nil {
		t.Fatalf("source worktree removed: %v", err)
	}
}
