package web

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

// worktreeRepo is a repository on main with a committed README, guide and
// image, and a branch "feature" holding one more file.
func worktreeRepo(t *testing.T) string {
	t.Helper()
	gitIdentity(t)
	repo := branchRepo(t)
	writeRepoFile(t, repo, "README.md", "# demo\n")
	writeRepoFile(t, repo, "docs/guide.md", "guide\n")
	if err := os.WriteFile(filepath.Join(repo, "shot.png"), pngBytes(t), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "seed")
	gitIn(t, repo, "switch", "-q", "-c", "feature")
	writeRepoFile(t, repo, "feature-only.go", "package feature\n")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "feature")
	gitIn(t, repo, "switch", "-q", "main")
	return repo
}

// useWorktreeRoot puts new worktrees in a temporary folder, as
// <root>/<repository folder name>/<branch slug>, by the Copilot CLI's
// worktreePathTemplate in a temporary COPILOT_HOME, and returns root.
func useWorktreeRoot(t *testing.T, m *Manager) string {
	t.Helper()
	root := t.TempDir()
	m.worktreeRoot = root
	home := t.TempDir()
	t.Setenv("COPILOT_HOME", home)
	settings, _ := json.Marshal(map[string]string{"worktreePathTemplate": root + "/{repo}/{branchSlug}"})
	if err := os.WriteFile(filepath.Join(home, "settings.json"), settings, 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// worktreeThread starts a Thread of a Project at repo in a new worktree from
// base, with worktrees under a temporary root.
func worktreeThread(t *testing.T, ts *testServer, repo, base string) (SessionSummary, string) {
	t.Helper()
	useWorktreeRoot(t, ts.m)
	project := addProject(t, ts.m, repo)
	sum, err := ts.m.Create(CreateRequest{Provider: ts.prov.Name(), ProjectID: project, Place: &PlaceRequest{Mode: placeWorktree, Base: base}})
	if err != nil {
		t.Fatal(err)
	}
	return sum, project
}

func TestCreateInNewWorktree(t *testing.T) {
	repo := worktreeRepo(t)
	ts := newTestServer(t, ServerConfig{})
	sum, _ := worktreeThread(t, ts, repo, "feature")
	branch := sum.Worktree.Branch
	if !strings.HasPrefix(branch, "uam/") || len(branch) != len("uam/")+8 || sum.Branch != branch {
		t.Fatalf("branch = %q, summary branch %q", branch, sum.Branch)
	}
	want := filepath.Join(ts.m.worktreeRoot, filepath.Base(repo), strings.ReplaceAll(branch, "/", "-"))
	if sum.Worktree.Path != want || sum.Workdir != want {
		t.Fatalf("worktree = %+v, workdir %q, want %q", sum.Worktree, sum.Workdir, want)
	}
	if opens := ts.prov.Opens(); opens[len(opens)-1].Workdir != want {
		t.Fatalf("conversation opened in %q", opens[len(opens)-1].Workdir)
	}
	if _, err := os.Stat(filepath.Join(want, "feature-only.go")); err != nil {
		t.Fatalf("the worktree does not hold the base: %v", err)
	}
	if got := gitOutput(t, want, "symbolic-ref", "--short", "HEAD"); got != branch {
		t.Fatalf("worktree HEAD = %q", got)
	}
	// The Project's folder stays on its branch, and a new branch tracks nothing.
	if got := gitOutput(t, repo, "symbolic-ref", "--short", "HEAD"); got != "main" {
		t.Fatalf("project HEAD = %q", got)
	}
	if got := gitOutput(t, repo, "for-each-ref", "--format=%(upstream)", "refs/heads/"+branch); got != "" {
		t.Fatalf("upstream = %q", got)
	}
}

// A Project in a folder of its repository runs its worktree Thread in the
// same folder of the worktree.
func TestWorktreeThreadOfASubfolderProject(t *testing.T) {
	repo := worktreeRepo(t)
	ts := newTestServer(t, ServerConfig{})
	sum, _ := worktreeThread(t, ts, filepath.Join(repo, "docs"), "")
	if sum.Workdir != filepath.Join(sum.Worktree.Path, "docs") {
		t.Fatalf("workdir = %q, worktree %q", sum.Workdir, sum.Worktree.Path)
	}
	if _, err := os.Stat(filepath.Join(sum.Workdir, "guide.md")); err != nil {
		t.Fatal(err)
	}
}

// Every file-system route of a worktree Thread reads the worktree, not the
// Project's folder.
func TestWorktreeThreadFileRoutes(t *testing.T) {
	repo := worktreeRepo(t)
	ts := newTestServer(t, ServerConfig{})
	sum, project := worktreeThread(t, ts, repo, "")
	wt := sum.Workdir
	writeRepoFile(t, wt, "wt-only.md", "only in the worktree\n")
	writeRepoFile(t, wt, "README.md", "# changed in the worktree\n")
	if err := os.WriteFile(filepath.Join(wt, "wt.png"), pngBytes(t), 0o644); err != nil {
		t.Fatal(err)
	}
	auth := withCookie(ts)
	get := func(target string, out any) {
		t.Helper()
		w := ts.do(http.MethodGet, target, "", auth)
		if w.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", target, w.Code, w.Body)
		}
		if out != nil {
			if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
				t.Fatal(err)
			}
		}
	}
	base := "/api/sessions/" + sum.ID

	t.Run("search", func(t *testing.T) {
		var list FileList
		get(base+"/files?q=wt-only", &list)
		if len(list.Files) != 1 || list.Files[0].Path != "wt-only.md" {
			t.Fatalf("files = %+v", list)
		}
	})
	t.Run("tree", func(t *testing.T) {
		var list FileList
		get(base+"/files/tree", &list)
		status := map[string]string{}
		for _, f := range list.Files {
			status[f.Path] = f.Status
		}
		if status["wt-only.md"] != "untracked" || status["README.md"] != "modified" {
			t.Fatalf("tree = %+v", list.Files)
		}
	})
	t.Run("view and file key", func(t *testing.T) {
		view := authenticatedFileView(t, ts)
		w := view(http.MethodGet, viewURL(sum.ID, "wt-only.md"))
		if w.Code != http.StatusOK || w.Body.String() != "only in the worktree\n" {
			t.Fatalf("view = %d %q", w.Code, w.Body)
		}
	})
	t.Run("raw image", func(t *testing.T) {
		w := ts.do(http.MethodGet, base+"/files/raw?path=wt.png", "", auth)
		if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "image/png" {
			t.Fatalf("raw = %d %s", w.Code, w.Header())
		}
		if w := ts.do(http.MethodGet, base+"/files/raw?path="+filepath.Join(repo, "shot.png"), "", auth); w.Code != http.StatusNotFound {
			t.Fatalf("the Project's own folder is outside the Thread's: %d", w.Code)
		}
	})
	t.Run("markdown links", func(t *testing.T) {
		w := ts.do(http.MethodPost, base+"/files/resolve", `{"paths":["wt-only.md","`+filepath.Join(wt, "README.md")+`"]}`, auth)
		var got struct{ Files []resolvedFile }
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusOK || len(got.Files) != 2 || !got.Files[0].Exists || !got.Files[1].Exists {
			t.Fatalf("resolve = %d %s", w.Code, w.Body)
		}
	})
	t.Run("changes", func(t *testing.T) {
		var out Changes
		get(base+"/changes?scope=workspace", &out)
		paths := map[string]string{}
		for _, f := range out.Files {
			paths[f.Path] = f.Status
		}
		if paths["README.md"] != "modified" || paths["wt-only.md"] != "untracked" || len(paths) != 3 {
			t.Fatalf("changes = %+v", out.Files)
		}
	})
	t.Run("commit panel", func(t *testing.T) {
		var st GitState
		get(base+"/git", &st)
		if st.Branch != sum.Worktree.Branch || len(st.Files) != 3 {
			t.Fatalf("git state = %+v", st)
		}
	})
	t.Run("terminal", func(t *testing.T) {
		setTerminal(t, ts.m, true)
		term, err := ts.m.openTerminal(project, sum.ID)
		if err != nil {
			t.Fatal(err)
		}
		dir := term.dir
		ts.m.closeTerminal(term)
		if dir != wt {
			t.Fatalf("terminal dir = %q, want %q", dir, wt)
		}
		other := addProject(t, ts.m, t.TempDir())
		if _, err := ts.m.openTerminal(other, sum.ID); err == nil {
			t.Fatal("a Thread of another Project opened the terminal")
		}
	})
	// The Project's folder is untouched by all of it.
	if out := gitOutput(t, repo, "status", "--porcelain"); out != "" {
		t.Fatalf("project status = %q", out)
	}
}

// The @ picker of a draft Thread lists the chosen base, as the worktree made
// at the first send will hold it.
func TestProjectFilesListTheBase(t *testing.T) {
	repo := worktreeRepo(t)
	ts := newTestServer(t, ServerConfig{})
	project := addProject(t, ts.m, repo)
	auth := withCookie(ts)
	list := func(query string) (int, FileList) {
		w := ts.do(http.MethodGet, "/api/projects/"+project+"/files?"+query, "", auth)
		var out FileList
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	if code, out := list("q=feature-only"); code != http.StatusOK || len(out.Files) != 0 {
		t.Fatalf("checked-out list = %d %+v", code, out)
	}
	if code, out := list("q=feature-only&base=feature"); code != http.StatusOK || len(out.Files) != 1 || out.Files[0] != (FileEntry{Path: "feature-only.go", Type: "file"}) {
		t.Fatalf("base list = %d %+v", code, out)
	}
	if code, out := list("q=docs&base=feature"); code != http.StatusOK || len(out.Files) == 0 || out.Files[0] != (FileEntry{Path: "docs", Type: "directory"}) {
		t.Fatalf("base folders = %d %+v", code, out)
	}
	for _, bad := range []string{"base=missing", "base=-p"} {
		if code, _ := list(bad); code != http.StatusBadRequest {
			t.Fatalf("%s = %d", bad, code)
		}
	}
}

func TestProjectBranchesRoute(t *testing.T) {
	repo := worktreeRepo(t)
	gitIn(t, repo, "update-ref", "refs/remotes/origin/main", "main")
	gitIn(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	ts := newTestServer(t, ServerConfig{})
	project := addProject(t, ts.m, repo)
	w := ts.do(http.MethodGet, "/api/projects/"+project+"/branches", "", withCookie(ts))
	var out BranchList
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != http.StatusOK {
		t.Fatalf("branches = %d %s", w.Code, w.Body)
	}
	got := map[string]bool{}
	for _, b := range out.Branches {
		got[b.Name] = b.Remote
		if b.UpdatedAt.IsZero() {
			t.Fatalf("no date: %+v", b)
		}
	}
	if len(got) != 3 || got["main"] || got["feature"] || !got["origin/main"] {
		t.Fatalf("branches = %+v", out.Branches)
	}
	if w := ts.do(http.MethodGet, "/api/projects/missing/branches", "", withCookie(ts)); w.Code != http.StatusNotFound {
		t.Fatalf("missing project = %d", w.Code)
	}
}

func TestCreateOnNewBranch(t *testing.T) {
	repo := worktreeRepo(t)
	ts := newTestServer(t, ServerConfig{})
	project := addProject(t, ts.m, repo)
	w := ts.do(http.MethodPost, "/api/sessions", `{"project_id":"`+project+`","provider":"fake","place":{"mode":"branch"}}`, withCookie(ts))
	var sum SessionSummary
	if err := json.Unmarshal(w.Body.Bytes(), &sum); err != nil || w.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	if sum.Workdir != repo || sum.Worktree != (WorktreeInfo{}) || !strings.HasPrefix(sum.Branch, "uam/") {
		t.Fatalf("summary = %+v", sum)
	}
	if got := gitOutput(t, repo, "symbolic-ref", "--short", "HEAD"); got != sum.Branch {
		t.Fatalf("project HEAD = %q, want %q", got, sum.Branch)
	}
	for _, body := range []string{
		`{"mode":"elsewhere"}`, `{"mode":"branch","base":"main"}`, `{"mode":"worktree","base":"--orphan"}`,
	} {
		w := ts.do(http.MethodPost, "/api/sessions", `{"project_id":"`+project+`","provider":"fake","place":`+body+`}`, withCookie(ts))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("place %s = %d %s", body, w.Code, w.Body)
		}
	}
}

// A new branch switches the folder every Thread in it shares, so it waits
// for a Thread working there, and names it; a new worktree does not wait.
func TestNewBranchRefusedWhileAThreadWorksInTheFolder(t *testing.T) {
	repo := worktreeRepo(t)
	ts := newTestServer(t, ServerConfig{})
	useWorktreeRoot(t, ts.m)
	project := addProject(t, ts.m, repo)
	other, err := ts.m.Create(CreateRequest{Provider: "fake", ProjectID: project, Name: "Write docs"})
	if err != nil {
		t.Fatal(err)
	}
	setSession(ts.m, other.ID, func(s *webSession) { s.base = StateWorking })
	w := ts.do(http.MethodPost, "/api/sessions", `{"project_id":"`+project+`","provider":"fake","place":{"mode":"branch"}}`, withCookie(ts))
	var reply struct {
		Error string   `json:"error"`
		Code  string   `json:"code"`
		Refs  []string `json:"refs"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &reply)
	if w.Code != http.StatusConflict || reply.Code != codeBranchBusy || len(reply.Refs) != 1 || reply.Refs[0] != other.ID || !strings.HasPrefix(reply.Error, "“Write docs” is working in this folder") {
		t.Fatalf("refusal = %d %s", w.Code, w.Body)
	}
	if got := gitOutput(t, repo, "symbolic-ref", "--short", "HEAD"); got != "main" {
		t.Fatalf("project HEAD = %q", got)
	}
	if n := len(ts.m.List()); n != 1 {
		t.Fatalf("sessions = %d", n)
	}
	if _, err := ts.m.Create(CreateRequest{Provider: "fake", ProjectID: project, Place: &PlaceRequest{Mode: placeWorktree}}); err != nil {
		t.Fatalf("worktree while busy: %v", err)
	}
}

// A Thread whose conversation does not open leaves no worktree or branch.
func TestFailedCreateTakesTheWorktreeBack(t *testing.T) {
	repo := worktreeRepo(t)
	ts := newTestServer(t, ServerConfig{})
	ts.prov.SetOpenError(context.DeadlineExceeded)
	if _, err := worktreeThreadErr(t, ts, repo); err == nil {
		t.Fatal("create succeeded")
	}
	if out := gitOutput(t, repo, "worktree", "list", "--porcelain"); strings.Count(out, "worktree ") != 1 {
		t.Fatalf("worktrees = %q", out)
	}
	if out := gitOutput(t, repo, "branch", "--list", "uam/*"); out != "" {
		t.Fatalf("branches = %q", out)
	}
}

func worktreeThreadErr(t *testing.T, ts *testServer, repo string) (SessionSummary, error) {
	useWorktreeRoot(t, ts.m)
	p, err := ts.m.AddProject(repo, "")
	if err != nil {
		return SessionSummary{}, err
	}
	return ts.m.Create(CreateRequest{Provider: ts.prov.Name(), ProjectID: p.ID, Place: &PlaceRequest{Mode: placeWorktree}})
}

// The branch is named once, from the Thread's name, when the worktree is
// made, and keeps that name: the Copilot CLI never renames a worktree's
// branch either.
func TestWorktreeBranchIsNamedOnceFromTheThread(t *testing.T) {
	repo := worktreeRepo(t)
	ts := newTestServer(t, ServerConfig{})
	useWorktreeRoot(t, ts.m)
	project := addProject(t, ts.m, repo)
	sum, err := ts.m.Create(CreateRequest{Provider: ts.prov.Name(), ProjectID: project, Name: "Fix the parser!", Place: &PlaceRequest{Mode: placeWorktree}})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Branch != "uam/fix-the-parser" || sum.Worktree.Branch != sum.Branch || filepath.Base(sum.Worktree.Path) != "uam-fix-the-parser" {
		t.Fatalf("summary = %+v", sum)
	}
	if _, err := ts.m.Rename(sum.ID, "Something else"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if got, _ := ts.m.Summary(sum.ID); got.Worktree.Branch != "uam/fix-the-parser" {
		t.Fatalf("renamed: %+v", got.Worktree)
	}
	if out := gitOutput(t, sum.Workdir, "symbolic-ref", "--short", "HEAD"); out != "uam/fix-the-parser" {
		t.Fatalf("HEAD = %q", out)
	}
	// A name a local or a remote branch has gets random digits, so a push
	// never lands on an older branch.
	gitIn(t, repo, "update-ref", "refs/remotes/origin/uam/add-tests", "main")
	again, err := ts.m.Create(CreateRequest{Provider: ts.prov.Name(), ProjectID: project, Name: "Add tests", Place: &PlaceRequest{Mode: placeWorktree}})
	if err != nil {
		t.Fatal(err)
	}
	if b := again.Worktree.Branch; !strings.HasPrefix(b, "uam/add-tests-") || len(b) != len("uam/add-tests-")+8 {
		t.Fatalf("branch = %q", b)
	}
}

// The agent is told its branch, its base and how its work leaves, so it
// pushes its own branch without asking which.
func TestWorktreeThreadIsToldItsBranchAndBase(t *testing.T) {
	repo := worktreeRepo(t)
	ts := newTestServer(t, ServerConfig{})
	sum, _ := worktreeThread(t, ts, repo, "feature")
	opens := ts.prov.Opens()
	got := opens[len(opens)-1].Instructions
	for _, want := range []string{"git worktree", repo, sum.Worktree.Branch, "started from feature", "`git push -u origin HEAD`", "into feature", "do not ask which branch to push"} {
		if !strings.Contains(got, want) {
			t.Fatalf("instructions lack %q: %s", want, got)
		}
	}
}

// A worktree Thread is still one after a restart.
func TestWorktreeThreadSurvivesRestart(t *testing.T) {
	repo := worktreeRepo(t)
	st := openTestStore(t)
	prov := agenttest.NewProvider("fake", allCaps)
	m := startManager(t, st, prov)
	useWorktreeRoot(t, m)
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: addProject(t, m, repo), Place: &PlaceRequest{Mode: placeWorktree, Base: "feature"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	again := startManager(t, st, agenttest.NewProvider("fake", allCaps))
	got, err := again.Summary(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Workdir != sum.Workdir || got.Worktree != sum.Worktree {
		t.Fatalf("after restart = %+v / %+v, want %+v / %+v", got.Workdir, got.Worktree, sum.Workdir, sum.Worktree)
	}
	waitUntil(t, "branch", func() bool {
		again.refreshBranches(t.Context(), true)
		got, _ = again.Summary(sum.ID)
		return got.Branch == sum.Worktree.Branch
	})
	for _, dir := range again.RecentWorkdirs() {
		if dir == sum.Workdir {
			t.Fatal("a worktree is offered as a Project folder")
		}
	}
}
