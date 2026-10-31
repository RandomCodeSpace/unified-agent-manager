package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func setWorktreesDir(t *testing.T, m *Manager, dir string) (Settings, error) {
	t.Helper()
	return m.UpdateSettings(SettingsPatch{WorktreesDir: &dir})
}

// The folder for new worktrees: absolute after ~, the user's own, in no git
// repository; the default itself is stored as no setting.
func TestWorktreesDirSettingIsChecked(t *testing.T) {
	repo := worktreeRepo(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	ts := newTestServer(t, ServerConfig{})
	ts.m.worktreeRoot = filepath.Join(t.TempDir(), "default")
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	bad := map[string]string{
		"relative/path":                        "absolute path",
		file:                                   "not a directory",
		filepath.Join(repo, "docs"):            "inside a git repository",
		filepath.Join(repo, "not", "made"):     "inside a git repository",
		"/tmp/../" + strings.Repeat("\x01", 2): "control characters",
	}
	if os.Geteuid() != 0 {
		bad["/"] = "belongs to another user"
	}
	for dir, want := range bad {
		if _, err := setWorktreesDir(t, ts.m, dir); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", dir, err, want)
		}
	}
	if got, err := setWorktreesDir(t, ts.m, "~/trees/"); err != nil || got.WorktreesDir != filepath.Join(home, "trees") {
		t.Fatalf("~ = %+v, %v", got.WorktreesDir, err)
	}
	if got, err := setWorktreesDir(t, ts.m, ts.m.worktreeRoot); err != nil || got.WorktreesDir != "" {
		t.Fatalf("default = %q, %v", got.WorktreesDir, err)
	}
	// Over HTTP, and "" puts the default back.
	w := ts.do(http.MethodPatch, "/api/settings", `{"worktrees_dir":"`+filepath.Join(home, "x")+`"}`, withCookie(ts))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"worktrees_dir":"`+filepath.Join(home, "x")) {
		t.Fatalf("patch = %d %s", w.Code, w.Body)
	}
	w = ts.do(http.MethodPatch, "/api/settings", `{"worktrees_dir":""}`, withCookie(ts))
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "worktrees_dir") {
		t.Fatalf("reset = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodPatch, "/api/settings", `{"worktrees_dir":null}`, withCookie(ts)); w.Code != http.StatusBadRequest {
		t.Fatalf("null = %d", w.Code)
	}
}

// New worktrees go where the Copilot CLI puts its own: beside the
// repository as <repository>.worktrees/<branch slug> when the owner set no
// worktreePathTemplate. A repository's own Copilot settings choose the base
// (worktreeBaseRef) but never the folder. A worktree an earlier version made
// in a uam worktrees folder is still listed and removable.
func TestNewWorktreesGoWhereTheCopilotCLIPutsThem(t *testing.T) {
	repo := worktreeRepo(t)
	t.Cleanup(func() { _ = os.RemoveAll(repo + ".worktrees") })
	ts := newTestServer(t, ServerConfig{})
	ts.m.worktreeRoot = t.TempDir()
	t.Setenv("COPILOT_HOME", t.TempDir())
	project := addProject(t, ts.m, repo)
	first, err := ts.m.Create(CreateRequest{Provider: "fake", ProjectID: project, Name: "First", Place: &PlaceRequest{Mode: placeWorktree}})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(repo+".worktrees", "uam-first"); first.Worktree.Path != want {
		t.Fatalf("worktree at %q, want %q", first.Worktree.Path, want)
	}

	// The repository picks the remote's default branch as the base; its
	// folder template is ignored.
	writeRepoFile(t, repo, ".github/copilot/settings.json", `{"worktreeBaseRef":"defaultBranch","worktreePathTemplate":"`+t.TempDir()+`/{branch}"}`)
	gitIn(t, repo, "update-ref", "refs/remotes/origin/feature", "feature")
	gitIn(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/feature")
	second, err := ts.m.Create(CreateRequest{Provider: "fake", ProjectID: project, Name: "Second", Place: &PlaceRequest{Mode: placeWorktree}})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(repo+".worktrees", "uam-second"); second.Worktree.Path != want {
		t.Fatalf("second worktree at %q, want %q", second.Worktree.Path, want)
	}
	if _, err := os.Stat(filepath.Join(second.Worktree.Path, "feature-only.go")); err != nil {
		t.Fatalf("not started from the default branch: %v", err)
	}
	ts.m.mu.Lock()
	base := ts.m.sessions[second.ID].place.Base
	ts.m.mu.Unlock()
	if base != "feature" {
		t.Fatalf("base = %q", base)
	}

	// A worktree in the folder earlier versions used.
	old := filepath.Join(ts.m.worktreeRoot, "demo", "uam-old")
	gitIn(t, repo, "worktree", "add", "-q", "-b", "uam/old", old, "main")
	// A worktree of the owner's own, on a branch uam did not make, is not one.
	gitIn(t, repo, "worktree", "add", "-q", "-b", "mine", filepath.Join(t.TempDir(), "mine"), "main")
	// Nor is a Project that is itself a linked worktree on a uam/… branch.
	asProject := filepath.Join(t.TempDir(), "as-project")
	gitIn(t, repo, "worktree", "add", "-q", "-b", "uam/as-project", asProject, "main")
	addProject(t, ts.m, asProject)
	list, err := ts.m.ListWorktrees(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, e := range list.Worktrees {
		paths = append(paths, e.Path)
	}
	slices.Sort(paths)
	want := []string{old, first.Worktree.Path, second.Worktree.Path}
	slices.Sort(want)
	if !slices.Equal(paths, want) || list.Root != "<repository>.worktrees/<branch>" {
		t.Fatalf("listed %q under %q, want %q", paths, list.Root, want)
	}
	body, _ := json.Marshal(map[string]any{"path": old})
	if w := ts.do(http.MethodPost, "/api/worktrees/remove", string(body), withCookie(ts)); w.Code != http.StatusNoContent {
		t.Fatalf("remove = %d %s", w.Code, w.Body)
	}
	if !worktreeGone(old) {
		t.Fatal("not removed")
	}
}

// A worktree reached through a link is listed once, by the path its Thread
// was made with, though git lists the resolved one.
func TestWorktreeBehindALinkIsListedOnce(t *testing.T) {
	repo := worktreeRepo(t)
	ts := newTestServer(t, ServerConfig{})
	ts.m.worktreeRoot = t.TempDir()
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "trees")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("COPILOT_HOME", home)
	writeRepoFile(t, home, "settings.json", `{"worktreePathTemplate":"`+link+`/{branchSlug}"}`)
	project := addProject(t, ts.m, repo)
	sum, err := ts.m.Create(CreateRequest{Provider: "fake", ProjectID: project, Name: "Linked", Place: &PlaceRequest{Mode: placeWorktree}})
	if err != nil {
		t.Fatal(err)
	}
	list, err := ts.m.ListWorktrees(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Worktrees) != 1 || list.Worktrees[0].Path != sum.Worktree.Path || list.Worktrees[0].TaskID != sum.ID {
		t.Fatalf("listed %+v, want only %q", list.Worktrees, sum.Worktree.Path)
	}
}

// A worktrees folder left for another joins the stored earlier ones, newest
// last, and the one chosen again leaves them.
func TestKeptWorktreeRoots(t *testing.T) {
	got := keptWorktreeRoots([]string{"/a", "/b"}, "/c", "/a")
	if !slices.Equal(got, []string{"/b", "/c"}) {
		t.Fatalf("kept = %q", got)
	}
	if got := keptWorktreeRoots([]string{"/a"}, "", "/b"); !slices.Equal(got, []string{"/a"}) {
		t.Fatalf("from the default = %q", got)
	}
	var many []string
	for i := range maxWorktreeRoots + 3 {
		many = keptWorktreeRoots(many, "/r"+strings.Repeat("x", i), "")
	}
	if len(many) != maxWorktreeRoots || many[len(many)-1] != "/r"+strings.Repeat("x", maxWorktreeRoots+2) {
		t.Fatalf("capped = %d, newest %q", len(many), many[len(many)-1])
	}
}
