package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/execpath"
)

func gitRepoFixture(t *testing.T) string {
	t.Helper()
	git, err := execpath.Resolve("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(git, append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, text string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q")
	write("tracked.txt", "one\ntwo\n")
	write("unchanged.txt", "same\n")
	run("add", ".")
	run("commit", "-q", "-m", "init")
	write("tracked.txt", "one\nthree\nfour\n")
	write("sub/new file.txt", "a\nb\nc\n")
	return dir
}

func TestWorkspaceChangesListAndDiffOnlyStatusPaths(t *testing.T) {
	repo := gitRepoFixture(t)
	ts := newTestServer(t, ServerConfig{})
	auth := withCookie(ts)
	sum, err := ts.m.Create(CreateRequest{Provider: "fake", ProjectID: addProject(t, ts.m, filepath.Join(repo, "sub"))})
	if err != nil {
		t.Fatal(err)
	}
	w := ts.do(http.MethodGet, "/api/sessions/"+sum.ID+"/changes?scope=workspace", "", auth)
	var changes Changes
	if err := json.Unmarshal(w.Body.Bytes(), &changes); err != nil || w.Code != http.StatusOK {
		t.Fatalf("changes = %d %s", w.Code, w.Body)
	}
	if !changes.Supported || changes.Scope != ScopeWorkspace || !strings.Contains(changes.Label, "Whole working tree") ||
		!strings.Contains(changes.Label, "not made by this session") {
		t.Fatalf("changes header = %+v", changes)
	}
	files := map[string]ChangedFile{}
	for _, f := range changes.Files {
		files[f.Path] = f
	}
	if f := files["tracked.txt"]; f.Status != "modified" || f.Additions != 2 || f.Deletions != 1 {
		t.Fatalf("tracked change = %+v (all %+v)", f, changes.Files)
	}
	if f := files["sub/new file.txt"]; f.Status != "untracked" || f.Additions != 3 {
		t.Fatalf("untracked change = %+v", f)
	}
	if _, listed := files["unchanged.txt"]; listed || len(files) != 2 {
		t.Fatalf("unexpected files %+v", changes.Files)
	}

	fileDiff := func(path string) (*agentapi.FileDiff, int) {
		w := ts.do(http.MethodGet, "/api/sessions/"+sum.ID+"/changes/file?scope=workspace&path="+url.QueryEscape(path), "", auth)
		if w.Code != http.StatusOK {
			return nil, w.Code
		}
		var d agentapi.FileDiff
		if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
			t.Fatal(err)
		}
		return &d, w.Code
	}
	if d, code := fileDiff("tracked.txt"); code != http.StatusOK || !strings.Contains(d.Patch, "+three") || !strings.Contains(d.Patch, "-two") || d.Additions != 2 || d.Deletions != 1 {
		t.Fatalf("tracked diff = %d %+v", code, d)
	}
	if d, code := fileDiff("sub/new file.txt"); code != http.StatusOK || !strings.Contains(d.Patch, "+c") || d.Additions != 3 {
		t.Fatalf("untracked diff = %d %+v", code, d)
	}
	for _, bad := range []string{"unchanged.txt", "../../etc/passwd", "/etc/passwd", "--output=/tmp/x", ""} {
		if _, code := fileDiff(bad); code != http.StatusBadRequest {
			t.Fatalf("path %q = %d, want 400", bad, code)
		}
	}
	if w := ts.do(http.MethodGet, "/api/sessions/"+sum.ID+"/changes?scope=everything", "", auth); w.Code != http.StatusBadRequest {
		t.Fatalf("bad scope = %d", w.Code)
	}
}

func TestWorkspaceChangesOutsideGit(t *testing.T) {
	if _, err := execpath.Resolve("git"); err != nil {
		t.Skip("git is not installed")
	}
	ts := newTestServer(t, ServerConfig{})
	sum, err := ts.m.Create(CreateRequest{Provider: "fake", ProjectID: addProject(t, ts.m, t.TempDir())})
	if err != nil {
		t.Fatal(err)
	}
	w := ts.do(http.MethodGet, "/api/sessions/"+sum.ID+"/changes?scope=workspace", "", withCookie(ts))
	var changes Changes
	if err := json.Unmarshal(w.Body.Bytes(), &changes); err != nil || changes.Supported || !strings.Contains(changes.Reason, "not in a Git working tree") {
		t.Fatalf("non-git changes = %d %s", w.Code, w.Body)
	}
}

func TestSessionScopeChanges(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	auth := withCookie(ts)
	sum, conv := createSession(t, ts.m, ts.prov)
	conv.SetDiff([]agentapi.FileDiff{{Path: "main.go", Status: "modified", Additions: 1, Deletions: 1, Before: "a\n", After: "b\n"}}, nil)
	w := ts.do(http.MethodGet, "/api/sessions/"+sum.ID+"/changes?scope=session", "", auth)
	var changes Changes
	if err := json.Unmarshal(w.Body.Bytes(), &changes); err != nil || !changes.Supported || changes.Label != sessionLabel ||
		len(changes.Files) != 1 || changes.Files[0].Path != "main.go" {
		t.Fatalf("session changes = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodGet, "/api/sessions/"+sum.ID+"/changes/file?scope=session&path=main.go", "", auth); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"after":"b\n"`) {
		t.Fatalf("session file diff = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodGet, "/api/sessions/"+sum.ID+"/changes/file?scope=session&path=other.go", "", auth); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown session path = %d", w.Code)
	}
	conv.SetDiff(nil, errors.New("server gone"))
	if w := ts.do(http.MethodGet, "/api/sessions/"+sum.ID+"/changes?scope=session", "", auth); w.Code != http.StatusBadGateway {
		t.Fatalf("provider diff failure = %d", w.Code)
	}

	noDiff := agenttest.NewProvider("nodiff", agentapi.Capabilities{History: true})
	m := startManager(t, openTestStore(t), noDiff)
	created, err := m.Create(CreateRequest{Provider: "nodiff", ProjectID: addProject(t, m, t.TempDir())})
	if err != nil {
		t.Fatal(err)
	}
	out, err := m.Changes(t.Context(), created.ID, ScopeSession)
	if err != nil || out.Supported || !strings.Contains(out.Reason, "does not record file changes") || out.Label != sessionLabel {
		t.Fatalf("unsupported session diff = %+v, %v", out, err)
	}
}

func TestCountLinesSkipsLinksAndSpecialFiles(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(fifo, link); err != nil {
		t.Fatal(err)
	}
	regular := filepath.Join(dir, "file")
	if err := os.WriteFile(regular, []byte("a\nb\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan [2]int, 1)
	go func() {
		a, _ := countLines(root, "link", 1<<20)
		b, _ := countLines(root, "fifo", 1<<20)
		done <- [2]int{a, b}
	}()
	select {
	case got := <-done:
		if got != [2]int{0, 0} {
			t.Fatalf("link/fifo counted %v lines, want none", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("countLines blocked on a FIFO")
	}
	if n, _ := countLines(root, "file", 1<<20); n != 2 {
		t.Fatalf("regular file lines = %d, want 2", n)
	}
}

func TestCountLinesRejectsChangedParentSymlink(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	parent := filepath.Join(dir, "new")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "file"), []byte("inside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "file"), []byte("private\ncontents\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Git listed new/file before a concurrent writer replaced its parent.
	if err := os.Rename(parent, parent+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, parent); err != nil {
		t.Fatal(err)
	}
	const budget = 1024
	if n, left := countLines(root, "new/file", budget); n != 0 || left != budget {
		t.Fatalf("read outside repository: lines=%d, remaining budget=%d", n, left)
	}
}
