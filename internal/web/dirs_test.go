package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func mkdirs(t *testing.T, root string, names ...string) {
	t.Helper()
	for _, name := range names {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func writeFiles(t *testing.T, root string, names ...string) {
	t.Helper()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// lockDir makes dir unreadable until the test ends.
func lockDir(t *testing.T, dir string, mode os.FileMode) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	if err := os.Chmod(dir, mode); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
}

func TestListDirsPathRules(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, "a")
	writeFiles(t, root, "file")
	for _, tc := range []struct {
		name, path string
		want       int
	}{
		{"directory", root, http.StatusOK},
		{"root", "/", http.StatusOK},
		{"relative", "a", http.StatusBadRequest},
		{"dot relative", "./a", http.StatusBadRequest},
		{"trailing slash", root + "/", http.StatusBadRequest},
		{"double slash", root + "//a", http.StatusBadRequest},
		{"dot dot", root + "/a/../a", http.StatusBadRequest},
		{"dot", root + "/.", http.StatusBadRequest},
		{"NUL", root + "/a\x00", http.StatusBadRequest},
		{"invalid UTF-8", root + "/\xff", http.StatusBadRequest},
		{"missing", root + "/missing", http.StatusNotFound},
		{"under a file", root + "/file/a", http.StatusNotFound},
		{"file", root + "/file", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := listDirs(tc.path, maxDirEntries)
			got := http.StatusOK
			if err != nil {
				got = statusOf(err)
			}
			if got != tc.want {
				t.Fatalf("listDirs(%q) = %d (%v), want %d", tc.path, got, err, tc.want)
			}
		})
	}
}

func TestListDirsEntries(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, "beta", "Alpha", "alpha", ".hidden", "repo/.git", "worktree", "x\x1b[31mred", "bad\xff")
	writeFiles(t, root, "file.txt", "worktree/.git")
	for link, target := range map[string]string{"linkdir": "beta", "linkfile": "file.txt", "dangling": "nope"} {
		if err := os.Symlink(target, filepath.Join(root, link)); err != nil {
			t.Fatal(err)
		}
	}
	list, err := listDirs(root, maxDirEntries)
	if err != nil {
		t.Fatal(err)
	}
	at := func(name string) string { return filepath.Join(root, name) }
	want := []DirEntry{
		{Name: ".hidden", Path: at(".hidden"), Hidden: true},
		{Name: "Alpha", Path: at("Alpha")},
		{Name: "alpha", Path: at("alpha")},
		{Name: "beta", Path: at("beta")},
		{Name: "linkdir", Path: at("linkdir"), Link: true},
		{Name: "repo", Path: at("repo"), Git: true},
		{Name: "worktree", Path: at("worktree"), Git: true},
		{Name: "xred", Path: at("x\x1b[31mred")},
	}
	if !slices.Equal(list.Entries, want) {
		t.Fatalf("entries =\n%+v\nwant\n%+v", list.Entries, want)
	}
	if list.Path != root || list.Parent != filepath.Dir(root) || list.Truncated {
		t.Fatalf("list = path %q parent %q truncated %v", list.Path, list.Parent, list.Truncated)
	}
}

func TestListDirsCap(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, "c", "a", "b")
	list, err := listDirs(root, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Entries) != 2 || list.Entries[0].Name != "a" || list.Entries[1].Name != "b" || !list.Truncated {
		t.Fatalf("capped list = %+v", list)
	}
	if list, _ := listDirs(root, 3); len(list.Entries) != 3 || list.Truncated {
		t.Fatalf("list at the cap = %+v", list)
	}
}

func TestListDirsRootHasNoParent(t *testing.T) {
	list, err := listDirs("/", maxDirEntries)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(list)
	if list.Path != "/" || strings.Contains(string(raw), `"parent"`) {
		t.Fatalf("root list = %s", raw)
	}
}

func TestListDirsPermissionDenied(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, "locked/inner")
	lockDir(t, filepath.Join(root, "locked"), 0)
	for _, p := range []string{filepath.Join(root, "locked"), filepath.Join(root, "locked", "inner")} {
		if _, err := listDirs(p, maxDirEntries); statusOf(err) != http.StatusForbidden {
			t.Fatalf("listDirs(%q) = %v, want 403", p, err)
		}
	}
}

func TestMakeDirRules(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, "taken")
	writeFiles(t, root, "file")
	for _, tc := range []struct {
		name, parent, dir string
		want              int
	}{
		{"no parent", "", "x", http.StatusBadRequest},
		{"relative parent", "tmp", "x", http.StatusBadRequest},
		{"unclean parent", root + "/", "x", http.StatusBadRequest},
		{"missing parent", root + "/missing", "x", http.StatusNotFound},
		{"parent is a file", root + "/file", "x", http.StatusBadRequest},
		{"empty", root, "", http.StatusBadRequest},
		{"dot", root, ".", http.StatusBadRequest},
		{"dot dot", root, "..", http.StatusBadRequest},
		{"slash", root, "a/b", http.StatusBadRequest},
		{"NUL", root, "a\x00b", http.StatusBadRequest},
		{"control", root, "a\x01b", http.StatusBadRequest},
		{"DEL", root, "a\x7fb", http.StatusBadRequest},
		{"C1 control", root, "a\u0085b", http.StatusBadRequest},
		{"tab", root, "a\tb", http.StatusBadRequest},
		{"newline", root, "a\nb", http.StatusBadRequest},
		{"leading space", root, " a", http.StatusBadRequest},
		{"trailing space", root, "a ", http.StatusBadRequest},
		{"leading no-break space", root, " a", http.StatusBadRequest},
		{"over 255 bytes", root, strings.Repeat("é", 128), http.StatusBadRequest},
		{"invalid UTF-8", root, "a\xff", http.StatusBadRequest},
		{"existing folder", root, "taken", http.StatusConflict},
		{"existing file", root, "file", http.StatusConflict},
		{"plain", root, "new", http.StatusCreated},
		{"spaces and dots inside", root, "my new.folder", http.StatusCreated},
		{"unicode", root, "ünïcødé", http.StatusCreated},
		{"hidden", root, ".cache", http.StatusCreated},
		{"255 bytes", root, strings.Repeat("x", 255), http.StatusCreated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := makeDir(tc.parent, tc.dir)
			got := http.StatusCreated
			if err != nil {
				got = statusOf(err)
			}
			if got != tc.want {
				t.Fatalf("makeDir(%q, %q) = %d (%v), want %d", tc.parent, tc.dir, got, err, tc.want)
			}
			if err != nil {
				return
			}
			info, err := os.Lstat(p)
			if p != filepath.Join(tc.parent, tc.dir) || err != nil || !info.IsDir() {
				t.Fatalf("created %q: %v %v", p, info, err)
			}
			// 0755 less the umask.
			if perm := info.Mode().Perm(); perm&^0o755 != 0 || perm&0o700 != 0o700 {
				t.Fatalf("mode = %v, want 0755 less the umask", perm)
			}
		})
	}
}

func TestMakeDirPermissionDenied(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, "readonly")
	lockDir(t, filepath.Join(root, "readonly"), 0o555)
	if _, err := makeDir(filepath.Join(root, "readonly"), "x"); statusOf(err) != http.StatusForbidden {
		t.Fatalf("makeDir in a read-only folder = %v, want 403", err)
	}
}

func TestFolderRoutes(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	root := t.TempDir()
	mkdirs(t, root, "sub")
	list := "/api/fs/dirs?" + url.Values{"path": {root}}.Encode()
	create := `{"parent":"` + root + `","name":"made"}`

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		if w := ts.do(method, list, create); w.Code != http.StatusUnauthorized {
			t.Fatalf("%s without sign-in = %d, want 401", method, w.Code)
		}
	}
	if w := ts.do(http.MethodGet, list, "", withHost("evil.example"), withCookie(ts)); w.Code != http.StatusForbidden {
		t.Fatalf("foreign Host = %d, want 403", w.Code)
	}
	for _, opt := range []reqOpt{withHeader("Sec-Fetch-Site", "cross-site"), withHeader("Origin", "https://evil.example")} {
		if w := ts.do(http.MethodPost, "/api/fs/dirs", create, withCookie(ts), opt); w.Code != http.StatusForbidden {
			t.Fatalf("cross-origin POST = %d, want 403", w.Code)
		}
	}
	if w := ts.do(http.MethodPost, "/api/fs/dirs", create, withCookie(ts), withHeader("Content-Type", "text/plain")); w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain POST = %d, want 415", w.Code)
	}
	if _, err := os.Stat(filepath.Join(root, "made")); !os.IsNotExist(err) {
		t.Fatalf("a refused request created the folder: %v", err)
	}

	w := ts.do(http.MethodGet, list, "", withCookie(ts))
	var got DirList
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &got) != nil ||
		got.Path != root || len(got.Entries) != 1 || got.Entries[0].Path != filepath.Join(root, "sub") {
		t.Fatalf("list = %d %s", w.Code, w.Body)
	}
	t.Setenv("HOME", root)
	if w := ts.do(http.MethodGet, "/api/fs/dirs", "", withCookie(ts)); w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Path != root {
		t.Fatalf("list without a path = %d %s, want the home directory", w.Code, w.Body)
	}
	for target, want := range map[string]int{"/api/fs/dirs?path=sub": http.StatusBadRequest, list + "%2Fmissing": http.StatusNotFound} {
		if w := ts.do(http.MethodGet, target, "", withCookie(ts)); w.Code != want || !strings.Contains(w.Body.String(), `"error"`) {
			t.Fatalf("GET %s = %d %s, want %d", target, w.Code, w.Body, want)
		}
	}

	w = ts.do(http.MethodPost, "/api/fs/dirs", create, withCookie(ts))
	var made struct{ Path string }
	if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &made) != nil || made.Path != filepath.Join(root, "made") {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodPost, "/api/fs/dirs", create, withCookie(ts)); w.Code != http.StatusConflict {
		t.Fatalf("create again = %d %s, want 409", w.Code, w.Body)
	}
	if w := ts.do(http.MethodPost, "/api/fs/dirs", `{"parent":`, withCookie(ts)); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid JSON = %d, want 400", w.Code)
	}
}
