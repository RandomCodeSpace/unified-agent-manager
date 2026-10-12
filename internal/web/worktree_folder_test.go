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

// New worktrees go in the chosen folder, made private on first use; the
// worktrees already made stay where they are and are still listed, removed
// on settling and removable, after the setting changes again and after
// their Thread is deleted.
func TestNewWorktreesGoInTheChosenFolder(t *testing.T) {
	repo := worktreeRepo(t)
	ts := newTestServer(t, ServerConfig{})
	first, project := worktreeThread(t, ts, repo, "")
	chosen := filepath.Join(t.TempDir(), "trees")
	if _, err := setWorktreesDir(t, ts.m, chosen); err != nil {
		t.Fatal(err)
	}
	second, err := ts.m.Create(CreateRequest{Provider: "fake", ProjectID: project, Place: &PlaceRequest{Mode: placeWorktree}})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(filepath.Dir(second.Worktree.Path)) != chosen {
		t.Fatalf("second worktree at %q, want under %q", second.Worktree.Path, chosen)
	}
	if info, err := os.Stat(chosen); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("chosen folder = %v, %v", info, err)
	}
	// Folders uam did not make there are not worktrees.
	if err := os.MkdirAll(filepath.Join(chosen, "photos", "2026"), 0o700); err != nil {
		t.Fatal(err)
	}
	listed := func() []string {
		t.Helper()
		list, err := ts.m.ListWorktrees(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if dir := ts.m.Settings().WorktreesDir; dir != "" && list.Root != dir || list.DefaultRoot != ts.m.worktreeRoot {
			t.Fatalf("roots = %q, %q", list.Root, list.DefaultRoot)
		}
		var paths []string
		for _, e := range list.Worktrees {
			paths = append(paths, e.Path)
		}
		slices.Sort(paths)
		return paths
	}
	want := []string{first.Worktree.Path, second.Worktree.Path}
	slices.Sort(want)
	if got := listed(); !slices.Equal(got, want) {
		t.Fatalf("listed = %q, want %q", got, want)
	}

	// Back to the default: the chosen folder's worktree is still listed,
	// even once its Thread is deleted (it has changes, so it stays).
	if _, err := setWorktreesDir(t, ts.m, ""); err != nil {
		t.Fatal(err)
	}
	writeRepoFile(t, second.Worktree.Path, "notes.txt", "keep me\n")
	if _, err := ts.m.Archive(second.ID); err != nil {
		t.Fatal(err)
	}
	if err := ts.m.Delete(second.ID); err != nil {
		t.Fatal(err)
	}
	if got := listed(); !slices.Equal(got, want) {
		t.Fatalf("after reset and delete = %q, want %q", got, want)
	}
	// It is still removable from Settings.
	body, _ := json.Marshal(map[string]any{"path": second.Worktree.Path, "force": true})
	if w := ts.do(http.MethodPost, "/api/worktrees/remove", string(body), withCookie(ts)); w.Code != http.StatusNoContent {
		t.Fatalf("remove = %d %s", w.Code, w.Body)
	}
	if !worktreeGone(second.Worktree.Path) {
		t.Fatal("not removed")
	}

	// A worktree made before the folder changed is removed on settling.
	if _, err := setWorktreesDir(t, ts.m, filepath.Join(t.TempDir(), "elsewhere")); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.m.Settle(first.ID); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "first worktree removed", func() bool { return worktreeGone(first.Worktree.Path) })
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
