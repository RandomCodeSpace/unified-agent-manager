package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

func TestWorkspaceDiffTreatsGitReportedNamesAsPaths(t *testing.T) {
	repo := gitRepoFixture(t)
	for _, name := range []string{"--output=should-not-exist", "$(touch should-not-exist)", "semi; touch should-not-exist"} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(repo, name), []byte("literal-data\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			diff, err := workspaceFileDiff(t.Context(), repo, name)
			if err != nil {
				t.Fatal(err)
			}
			if diff.Path != name || !strings.Contains(diff.Patch, "+literal-data\n") {
				t.Fatalf("filename was not treated literally: %+v", diff)
			}
			if _, err := os.Stat(filepath.Join(repo, "should-not-exist")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("filename triggered command or option processing: %v", err)
			}
		})
	}
}

// gitIn runs git in dir without the user's configuration.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	gitOutput(t, dir, args...)
}

// gitOutput runs git in dir without the user's configuration and returns
// its trimmed standard output.
func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	git, err := execpath.Resolve("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	cmd := exec.Command(git, append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, stderr.String())
	}
	return strings.TrimSpace(string(out))
}

// branchRepo is a repository with one commit on branch main.
func branchRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	return dir
}

func TestProjectBranchFromGit(t *testing.T) {
	m, _, _ := newTestManager(t)
	repo := branchRepo(t)
	gitIn(t, repo, "switch", "-q", "-c", "feature/x")
	sub := filepath.Join(repo, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(t.TempDir(), "linked")
	gitIn(t, repo, "worktree", "add", "-q", "-b", "topic", linked)
	if info, err := os.Lstat(filepath.Join(linked, ".git")); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("linked worktree .git should be a file: %v, %v", info, err)
	}
	detached := branchRepo(t)
	gitIn(t, detached, "switch", "-q", "--detach")
	// HEAD naming a ref outside refs/heads is no branch either.
	remoteHead := branchRepo(t)
	gitIn(t, remoteHead, "symbolic-ref", "HEAD", "refs/remotes/origin/main")
	plain := t.TempDir()
	want := map[string]string{repo: "feature/x", sub: "feature/x", linked: "topic", detached: "", remoteHead: "", plain: ""}
	for dir, branch := range want {
		// Only a folder outside any work tree is marked; a detached HEAD is still git.
		if p, err := m.AddProject(dir, ""); err != nil || p.Branch != branch || (p.NoGit == noGitRepository) != (dir == plain) {
			t.Fatalf("AddProject(%s) = %+v, %v; want branch %q", dir, p, err, branch)
		}
	}
	_, snapRaw, err := m.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	var listed []struct {
		Dir    string  `json:"dir"`
		Branch *string `json:"branch"`
		NoGit  *string `json:"no_git"`
	}
	decodeField(t, parseFrame(t, snapRaw), "projects", &listed)
	if len(listed) != len(want) {
		t.Fatalf("snapshot projects = %+v", listed)
	}
	for _, p := range listed {
		switch branch := want[p.Dir]; {
		case branch == "" && p.Branch != nil:
			t.Fatalf("%s lists branch %q, want none", p.Dir, *p.Branch)
		case branch != "" && (p.Branch == nil || *p.Branch != branch):
			t.Fatalf("%s lists branch %v, want %q", p.Dir, p.Branch, branch)
		case (p.NoGit != nil) != (p.Dir == plain):
			t.Fatalf("%s lists no_git %v", p.Dir, p.NoGit)
		}
	}
}

func TestProjectNoGitClearsOnceInitialised(t *testing.T) {
	m, _, _ := newTestManager(t)
	dir := t.TempDir()
	project := addProject(t, m, dir)
	if p := m.Projects()[0]; p.NoGit != noGitRepository {
		t.Fatalf("plain folder = %+v, want no_git %q", p, noGitRepository)
	}
	sub, _, err := m.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "init", "-q", "-b", "main")
	m.refreshBranches(t.Context(), true, project)
	var announced Project
	decodeField(t, frameOf(t, sub, "project"), "project", &announced)
	if announced.NoGit != "" || announced.Branch != "main" {
		t.Fatalf("project frame after git init = %+v", announced)
	}
}

func TestProjectBranchRereadAtTurnEndChangesAndListing(t *testing.T) {
	m, prov, _ := newTestManager(t)
	repo := branchRepo(t)
	project := addProject(t, m, repo)
	sum, err := m.Create(CreateRequest{Provider: prov.Name(), ProjectID: project})
	if err != nil {
		t.Fatal(err)
	}
	conv := prov.Last()
	sub, _, err := m.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	expect := func(branch string) {
		t.Helper()
		var announced Project
		decodeField(t, frameOf(t, sub, "project"), "project", &announced)
		if announced.ID != project || announced.Branch != branch {
			t.Fatalf("project frame = %+v, want branch %q", announced, branch)
		}
	}

	gitIn(t, repo, "switch", "-q", "-c", "agent-work")
	conv.EmitTurn(agentapi.TurnCompleted, "")
	expect("agent-work")

	gitIn(t, repo, "switch", "-q", "--detach")
	if _, err := m.Changes(t.Context(), sum.ID, ScopeWorkspace); err != nil {
		t.Fatal(err)
	}
	expect("")

	// Listing reuses a fresh read and re-reads a stale one.
	gitIn(t, repo, "switch", "-q", "main")
	m.mu.Lock()
	m.branchAt[project] = time.Now()
	m.mu.Unlock()
	if got := m.Projects()[0].Branch; got != "" {
		t.Fatalf("listing re-read a fresh branch: %q", got)
	}
	m.mu.Lock()
	m.branchAt[project] = time.Time{}
	m.mu.Unlock()
	if got := m.Projects()[0].Branch; got != "main" {
		t.Fatalf("listing kept a stale branch: %q", got)
	}
	expect("main")
}

// taskInDir starts a Task in dir and returns its manager and ID.
func taskInDir(t *testing.T, dir string) (*Manager, string) {
	t.Helper()
	m, prov, _ := newTestManager(t)
	sum, err := m.Create(CreateRequest{Provider: prov.Name(), ProjectID: addProject(t, m, dir)})
	if err != nil {
		t.Fatal(err)
	}
	return m, sum.ID
}

// writeRepoFile writes text to dir/name, creating parent directories.
func writeRepoFile(t *testing.T, dir, name, text string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// listWorkspace lists the workspace changes of a Task by path.
func listWorkspace(t *testing.T, m *Manager, id string) (Changes, map[string]ChangedFile) {
	t.Helper()
	out, err := m.Changes(t.Context(), id, ScopeWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]ChangedFile{}
	for _, f := range out.Files {
		files[f.Path] = f
	}
	return out, files
}

// Unknown sessions and scopes are refused before any diff is read.
func TestChangesRefuseUnknownSessionAndScope(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, _ := createSession(t, m, prov)
	ctx := t.Context()
	cases := []struct {
		name string
		call func() error
		want int
	}{
		{"workspace list", func() error { _, err := m.Changes(ctx, "missing", ScopeWorkspace); return err }, http.StatusNotFound},
		{"session list", func() error { _, err := m.Changes(ctx, "missing", ScopeSession); return err }, http.StatusNotFound},
		{"workspace file", func() error { _, err := m.FileChange(ctx, "missing", ScopeWorkspace, "a.go"); return err }, http.StatusNotFound},
		{"session file", func() error { _, err := m.FileChange(ctx, "missing", ScopeSession, "a.go"); return err }, http.StatusNotFound},
		{"file scope", func() error { _, err := m.FileChange(ctx, sum.ID, "everything", "a.go"); return err }, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); statusOf(err) != tc.want {
				t.Fatalf("error = %v (status %d), want %d", err, statusOf(err), tc.want)
			}
		})
	}
}

// The session view explains when the provider cannot list changes, caps a
// huge list and refuses a conversation that is not open.
func TestSessionScopeUnsupportedCappedAndClosed(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	ctx := t.Context()

	many := make([]agentapi.FileDiff, maxChangedFiles+1)
	for i := range many {
		many[i] = agentapi.FileDiff{Path: fmt.Sprintf("f%04d.go", i), Status: "added", Additions: 1}
	}
	conv.SetDiff(many, nil)
	out, err := m.Changes(ctx, sum.ID, ScopeSession)
	if err != nil || !out.Supported || len(out.Files) != maxChangedFiles || out.Reason != "showing the first 1000 changed files" {
		t.Fatalf("capped changes = %d files, %q, %v", len(out.Files), out.Reason, err)
	}
	if _, err := m.FileChange(ctx, sum.ID, ScopeSession, fmt.Sprintf("f%04d.go", maxChangedFiles)); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("file past the cap = %v, want 400", err)
	}

	conv.SetDiff(nil, agentapi.ErrUnsupported)
	out, err = m.Changes(ctx, sum.ID, ScopeSession)
	if err != nil || out.Supported || out.Reason != "the provider does not record file changes for this conversation" || len(out.Files) != 0 {
		t.Fatalf("unsupported changes = %+v, %v", out, err)
	}
	if _, err := m.FileChange(ctx, sum.ID, ScopeSession, "f0000.go"); statusOf(err) != http.StatusConflict ||
		!strings.Contains(err.Error(), "does not record file changes") {
		t.Fatalf("unsupported file change = %v, want 409 with the reason", err)
	}

	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Changes(ctx, sum.ID, ScopeSession); statusOf(err) != http.StatusConflict || !strings.Contains(err.Error(), "not open") {
		t.Fatalf("closed conversation changes = %v, want 409", err)
	}
}

// Git failures surface as errors or reasons, never as a clean tree.
func TestWorkspaceChangesGitFailures(t *testing.T) {
	if _, err := execpath.Resolve("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Run("request cancelled", func(t *testing.T) {
		m, id := taskInDir(t, gitRepoFixture(t))
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := m.Changes(ctx, id, ScopeWorkspace); statusOf(err) != http.StatusGatewayTimeout {
			t.Fatalf("Changes = %v, want 504", err)
		}
		if _, err := m.FileChange(ctx, id, ScopeWorkspace, "tracked.txt"); statusOf(err) != http.StatusGatewayTimeout {
			t.Fatalf("FileChange = %v, want 504", err)
		}
	})
	t.Run("folder removed", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "gone")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		m, id := taskInDir(t, dir)
		if err := os.Remove(dir); err != nil {
			t.Fatal(err)
		}
		out, err := m.Changes(t.Context(), id, ScopeWorkspace)
		if err != nil || out.Supported || !strings.HasPrefix(out.Reason, "git could not read this directory: fatal: cannot change to") {
			t.Fatalf("removed folder changes = %+v, %v", out, err)
		}
		if _, err := m.FileChange(t.Context(), id, ScopeWorkspace, "a.txt"); statusOf(err) != http.StatusConflict || !strings.Contains(err.Error(), "git could not read") {
			t.Fatalf("removed folder file = %v, want 409 with the reason", err)
		}
	})
	t.Run("corrupt index", func(t *testing.T) {
		repo := gitRepoFixture(t)
		m, id := taskInDir(t, repo)
		if err := os.WriteFile(filepath.Join(repo, ".git", "index"), []byte(strings.Repeat("garbage!", 8)), 0o644); err != nil {
			t.Fatal(err)
		}
		// git explains a bad index over two stderr lines; only the first is shown.
		if _, err := m.Changes(t.Context(), id, ScopeWorkspace); statusOf(err) != http.StatusBadGateway ||
			!strings.HasPrefix(err.Error(), "git status failed: ") || strings.Contains(err.Error(), "\n") {
			t.Fatalf("corrupt index changes = %v, want 502", err)
		}
		if _, err := m.FileChange(t.Context(), id, ScopeWorkspace, "tracked.txt"); statusOf(err) != http.StatusBadGateway {
			t.Fatalf("corrupt index file = %v, want 502", err)
		}
	})
	t.Run("missing object", func(t *testing.T) {
		repo := gitRepoFixture(t)
		blob := gitOutput(t, repo, "rev-parse", "HEAD:tracked.txt")
		if err := os.Remove(filepath.Join(repo, ".git", "objects", blob[:2], blob[2:])); err != nil {
			t.Fatal(err)
		}
		m, id := taskInDir(t, repo)
		// Status needs no blob, so the file is still listed, without counts.
		_, files := listWorkspace(t, m, id)
		if f := files["tracked.txt"]; f.Status != "modified" || f.Additions != 0 || f.Deletions != 0 {
			t.Fatalf("tracked change without its blob = %+v", f)
		}
		if f := files["sub/new file.txt"]; f.Additions != 3 {
			t.Fatalf("untracked change = %+v", f)
		}
		if _, err := m.FileChange(t.Context(), id, ScopeWorkspace, "tracked.txt"); statusOf(err) != http.StatusBadGateway ||
			!strings.HasPrefix(err.Error(), "git diff failed: ") {
			t.Fatalf("diff without its blob = %v, want 502", err)
		}
	})
	t.Run("tree not listable", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root lists any directory")
		}
		repo := gitRepoFixture(t)
		m, id := taskInDir(t, repo)
		if err := os.Chmod(repo, 0o100); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(repo, 0o755) })
		if _, err := m.Changes(t.Context(), id, ScopeWorkspace); statusOf(err) != http.StatusBadGateway ||
			!strings.Contains(err.Error(), "could not open working tree") {
			t.Fatalf("unlistable tree = %v, want 502", err)
		}
	})
}

// Before the first commit every listed file is counted from disk and diffed
// against nothing.
func TestWorkspaceChangesBeforeFirstCommit(t *testing.T) {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	writeRepoFile(t, dir, "staged.txt", "x\ny")
	writeRepoFile(t, dir, "blob.bin", "a\x00b\n")
	gitIn(t, dir, "add", "staged.txt")
	m, id := taskInDir(t, dir)
	out, files := listWorkspace(t, m, id)
	if !out.Supported || len(files) != 2 {
		t.Fatalf("changes = %+v", out)
	}
	if f := files["staged.txt"]; f.Status != "added" || f.Additions != 2 {
		t.Fatalf("staged file without trailing newline = %+v", f)
	}
	if f := files["blob.bin"]; f.Status != "untracked" || f.Additions != 0 {
		t.Fatalf("binary file = %+v", f)
	}
	d, err := m.FileChange(t.Context(), id, ScopeWorkspace, "staged.txt")
	if err != nil || d.Status != "added" || d.Additions != 2 || !strings.Contains(d.Patch, "+y") {
		t.Fatalf("staged diff = %+v, %v", d, err)
	}
}

// Renames list the new path once, deletions count removed lines, and binary
// files are listed without counts.
func TestWorkspaceChangesRenamesDeletionsAndBinaries(t *testing.T) {
	dir := branchRepo(t)
	writeRepoFile(t, dir, "from.txt", "a\n")
	writeRepoFile(t, dir, "doomed.txt", "b\nc\n")
	writeRepoFile(t, dir, "data.bin", "\x00\x01")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-q", "-m", "files")
	gitIn(t, dir, "mv", "from.txt", "to.txt")
	if err := os.Remove(filepath.Join(dir, "doomed.txt")); err != nil {
		t.Fatal(err)
	}
	writeRepoFile(t, dir, "data.bin", "\x00\x02")
	m, id := taskInDir(t, dir)
	out, files := listWorkspace(t, m, id)
	if len(out.Files) != 3 {
		t.Fatalf("changes = %+v", out.Files)
	}
	if f := files["to.txt"]; f.Status != "renamed" {
		t.Fatalf("renamed file = %+v", f)
	}
	if f := files["doomed.txt"]; f.Status != "deleted" || f.Deletions != 2 || f.Additions != 0 {
		t.Fatalf("deleted file = %+v", f)
	}
	if f := files["data.bin"]; f.Status != "modified" || f.Additions != 0 || f.Deletions != 0 {
		t.Fatalf("binary file = %+v", f)
	}
}

// A huge working tree change set is cut to the first maxChangedFiles files.
func TestWorkspaceChangesCapsFileCount(t *testing.T) {
	dir := branchRepo(t)
	for i := range maxChangedFiles + 1 {
		writeRepoFile(t, dir, fmt.Sprintf("f%04d.txt", i), "x\n")
	}
	m, id := taskInDir(t, dir)
	out, _ := listWorkspace(t, m, id)
	if !out.Supported || len(out.Files) != maxChangedFiles || out.Reason != "showing the first 1000 changed files" {
		t.Fatalf("capped changes = %d files, %q", len(out.Files), out.Reason)
	}
}

// parseStatus names each porcelain XY code, keeps the original path of a
// staged rename and skips that of other renames and copies.
func TestParseStatusNamesEveryCode(t *testing.T) {
	out := "UU both\x00AA added-both\x00DD deleted-both\x00R  new\x00old\x00 C copy\x00orig\x00A  add\x00 D del\x00M  mod\x00?? loose\x00x\x00"
	want := []statusEntry{
		{path: "both", status: "conflicted", x: 'U', y: 'U'},
		{path: "added-both", status: "conflicted", x: 'A', y: 'A'},
		{path: "deleted-both", status: "conflicted", x: 'D', y: 'D'},
		{path: "new", orig: "old", status: "renamed", x: 'R', y: ' '},
		{path: "copy", status: "copied", x: ' ', y: 'C'},
		{path: "add", status: "added", x: 'A', y: ' '},
		{path: "del", status: "deleted", x: ' ', y: 'D'},
		{path: "mod", status: "modified", x: 'M', y: ' '},
		{path: "loose", status: "untracked", x: '?', y: '?', untracked: true},
	}
	if got := parseStatus([]byte(out)); !slices.Equal(got, want) {
		t.Fatalf("parseStatus = %+v\nwant %+v", got, want)
	}
}

// countLines counts text only: binary data, unreadable files and a spent
// budget add no lines.
func TestCountLinesTextOnlyWithinBudget(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	writeRepoFile(t, dir, "text", "a\nb")
	writeRepoFile(t, dir, "binary", "a\x00b\n")
	cases := []struct {
		name, path          string
		budget, lines, left int
	}{
		{"last line without newline", "text", 100, 2, 97},
		{"binary", "binary", 100, 0, 96},
		{"budget spent", "text", 0, 0, 0},
	}
	for _, tc := range cases {
		if lines, left := countLines(root, tc.path, tc.budget); lines != tc.lines || left != tc.left {
			t.Errorf("%s: countLines = %d lines, %d left; want %d, %d", tc.name, lines, left, tc.lines, tc.left)
		}
	}
	if os.Geteuid() != 0 {
		writeRepoFile(t, dir, "locked", "a\n")
		if err := os.Chmod(filepath.Join(dir, "locked"), 0); err != nil {
			t.Fatal(err)
		}
		if lines, left := countLines(root, "locked", 100); lines != 0 || left != 100 {
			t.Errorf("unreadable: countLines = %d lines, %d left; want 0, 100", lines, left)
		}
	}
}

// A git binary that cannot run is a gateway error.
func TestRunGitReportsUnrunnableBinary(t *testing.T) {
	_, _, _, err := runGit(t.Context(), filepath.Join(t.TempDir(), "git"), t.TempDir(), 64, "status")
	if statusOf(err) != http.StatusBadGateway || !strings.HasPrefix(err.Error(), "git failed: ") {
		t.Fatalf("runGit = %v, want 502", err)
	}
}

// firstNonEmpty picks the first set value, or none.
func TestFirstNonEmpty(t *testing.T) {
	for _, tc := range []struct {
		in   []string
		want string
	}{
		{[]string{"Copilot", "copilot"}, "Copilot"},
		{[]string{"", "copilot"}, "copilot"},
		{[]string{"", ""}, ""},
		{nil, ""},
	} {
		if got := firstNonEmpty(tc.in...); got != tc.want {
			t.Errorf("firstNonEmpty(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
