package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// switchProject is a Project at a worktreeRepo (on main, with a branch
// "feature") whose origin has a branch "remote-only" no local branch has.
func switchProject(t *testing.T, ts *testServer) (repo, project string) {
	t.Helper()
	repo = worktreeRepo(t)
	gitIn(t, repo, "remote", "add", "origin", "https://example.invalid/repo.git")
	gitIn(t, repo, "update-ref", "refs/remotes/origin/remote-only", "feature")
	ts.m.worktreeRoot = t.TempDir()
	return repo, addProject(t, ts.m, repo)
}

func headOf(t *testing.T, dir string) string {
	t.Helper()
	return gitOutput(t, dir, "symbolic-ref", "--short", "HEAD")
}

// refusal is an error reply's message, code and refs.
type refusal struct {
	Error string   `json:"error"`
	Code  string   `json:"code"`
	Refs  []string `json:"refs"`
}

func createWithPlace(t *testing.T, ts *testServer, project, place string) (int, SessionSummary, refusal) {
	t.Helper()
	w := ts.do(http.MethodPost, "/api/sessions", `{"project_id":"`+project+`","provider":"fake","place":`+place+`}`, withCookie(ts))
	var sum SessionSummary
	var no refusal
	_ = json.Unmarshal(w.Body.Bytes(), &sum)
	_ = json.Unmarshal(w.Body.Bytes(), &no)
	return w.Code, sum, no
}

// A draft's existing branch is checked out when the first message creates
// the Thread; until then nothing changes (reading the branches is all the
// draft does).
func TestDraftSwitchAppliesAtSend(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	repo, project := switchProject(t, ts)
	list, err := ts.m.ProjectBranches(t.Context(), project)
	if err != nil || list.Busy != nil || list.Uncommitted != 0 {
		t.Fatalf("branches = %+v, %v", list, err)
	}
	if got := headOf(t, repo); got != "main" {
		t.Fatalf("a draft moved the folder to %q", got)
	}
	code, sum, no := createWithPlace(t, ts, project, `{"mode":"switch","branch":"feature"}`)
	if code != http.StatusCreated || sum.Branch != "feature" || sum.Workdir != repo || sum.Worktree != (WorktreeInfo{}) {
		t.Fatalf("create = %d %+v %+v", code, sum, no)
	}
	if got := headOf(t, repo); got != "feature" {
		t.Fatalf("HEAD = %q", got)
	}
	waitUntil(t, "project branch", func() bool {
		ts.m.mu.Lock()
		defer ts.m.mu.Unlock()
		return ts.m.projects[project].Branch == "feature"
	})
	for _, bad := range []string{
		`{"mode":"switch"}`, `{"mode":"switch","branch":"-f"}`, `{"mode":"current","branch":"feature"}`,
		`{"mode":"worktree","remote":true}`, `{"mode":"switch","branch":"feature","base":"main"}`,
	} {
		if code, _, no := createWithPlace(t, ts, project, bad); code != http.StatusBadRequest {
			t.Fatalf("place %s = %d %+v", bad, code, no)
		}
	}
	for _, missing := range []string{`{"mode":"switch","branch":"nope"}`, `{"mode":"switch","branch":"origin/nope","remote":true}`} {
		if code, _, no := createWithPlace(t, ts, project, missing); code != http.StatusBadRequest || !strings.Contains(no.Error, "not a") {
			t.Fatalf("place %s = %d %+v", missing, code, no)
		}
	}
}

// A remote branch gets a local branch of the same name tracking it.
func TestSwitchToARemoteBranchTracksIt(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	repo, project := switchProject(t, ts)
	code, sum, no := createWithPlace(t, ts, project, `{"mode":"switch","branch":"origin/remote-only","remote":true}`)
	if code != http.StatusCreated || sum.Branch != "remote-only" {
		t.Fatalf("create = %d %+v %+v", code, sum, no)
	}
	if got := headOf(t, repo); got != "remote-only" {
		t.Fatalf("HEAD = %q", got)
	}
	if got := gitOutput(t, repo, "for-each-ref", "--format=%(upstream)", "refs/heads/remote-only"); got != "refs/remotes/origin/remote-only" {
		t.Fatalf("upstream = %q", got)
	}
	// Back on main, choosing it again checks out the tracking branch; a
	// local branch tracking something else is refused.
	gitIn(t, repo, "switch", "-q", "main")
	if code, _, no := createWithPlace(t, ts, project, `{"mode":"switch","branch":"origin/remote-only","remote":true}`); code != http.StatusCreated || headOf(t, repo) != "remote-only" {
		t.Fatalf("again = %d %+v", code, no)
	}
	gitIn(t, repo, "update-ref", "refs/remotes/origin/feature", "main")
	if code, _, no := createWithPlace(t, ts, project, `{"mode":"switch","branch":"origin/feature","remote":true}`); code != http.StatusConflict || !strings.Contains(no.Error, "choose it under Local") {
		t.Fatalf("untracked local = %d %+v", code, no)
	}
}

// A Thread that is not finished keeps the folder on its branch; settled,
// archived and worktree Threads do not count.
func TestSwitchWaitsForUnfinishedThreadsInTheFolder(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	repo, project := switchProject(t, ts)
	other, err := ts.m.Create(CreateRequest{Provider: "fake", ProjectID: project, Name: "Write docs"})
	if err != nil {
		t.Fatal(err)
	}
	inWorktree, err := ts.m.Create(CreateRequest{Provider: "fake", ProjectID: project, Name: "Elsewhere", Place: &PlaceRequest{Mode: placeWorktree}})
	if err != nil {
		t.Fatal(err)
	}
	setSession(ts.m, inWorktree.ID, func(s *webSession) { s.base = StateWorking })
	for _, state := range []string{StateWorking, StateStarting, StateAwaitingAnswer, StateAwaitingPermission} {
		setSession(ts.m, other.ID, func(s *webSession) { s.base = state })
		for _, place := range []string{`{"mode":"switch","branch":"feature"}`, `{"mode":"branch"}`} {
			code, _, no := createWithPlace(t, ts, project, place)
			if code != http.StatusConflict || no.Code != codeBranchBusy || len(no.Refs) != 1 || no.Refs[0] != other.ID || no.Error != "“Write docs” is working in this folder. Wait for it to finish." {
				t.Fatalf("%s while %s = %d %+v", place, state, code, no)
			}
		}
		list, _ := ts.m.ProjectBranches(t.Context(), project)
		if list.Busy == nil || list.Busy.ID != other.ID || list.Busy.Name != "Write docs" {
			t.Fatalf("branches busy = %+v", list.Busy)
		}
	}
	setSession(ts.m, other.ID, func(s *webSession) { s.base = StateCompleted; s.queue = []QueuedPrompt{{}} })
	if code, _, no := createWithPlace(t, ts, project, `{"mode":"switch","branch":"feature"}`); code != http.StatusConflict || no.Code != codeBranchBusy {
		t.Fatalf("queued = %d %+v", code, no)
	}
	setSession(ts.m, other.ID, func(s *webSession) { s.queue = nil; s.compacting = true })
	if code, _, no := createWithPlace(t, ts, project, `{"mode":"switch","branch":"feature"}`); code != http.StatusConflict || no.Code != codeBranchBusy {
		t.Fatalf("compacting = %d %+v", code, no)
	}
	// Settled or archived, it no longer counts (nor does the worktree Thread).
	setSession(ts.m, other.ID, func(s *webSession) { s.compacting = false })
	for _, stage := range []string{StageSettled, StageArchived} {
		setSession(ts.m, other.ID, func(s *webSession) { s.base, s.stage = StateWorking, stage })
		if list, _ := ts.m.ProjectBranches(t.Context(), project); list.Busy != nil {
			t.Fatalf("%s Thread holds the folder: %+v", stage, list.Busy)
		}
		setSession(ts.m, other.ID, func(s *webSession) { s.base = StateCompleted })
		if code, _, no := createWithPlace(t, ts, project, `{"mode":"switch","branch":"feature"}`); code != http.StatusCreated {
			t.Fatalf("other %s = %d %+v", stage, code, no)
		}
		gitIn(t, repo, "switch", "-q", "main")
	}
}

// Changed tracked files keep the folder from switching to an existing
// branch, not to a new one (they move along); untracked files do not count.
func TestUncommittedFilesBlockOnlyAnExistingBranch(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	repo, project := switchProject(t, ts)
	writeRepoFile(t, repo, "scratch.txt", "untracked\n")
	if code, _, no := createWithPlace(t, ts, project, `{"mode":"switch","branch":"feature"}`); code != http.StatusCreated {
		t.Fatalf("untracked only = %d %+v", code, no)
	}
	gitIn(t, repo, "switch", "-q", "main")
	writeRepoFile(t, repo, "README.md", "changed\n")
	writeRepoFile(t, repo, "docs/guide.md", "changed\n")
	list, _ := ts.m.ProjectBranches(t.Context(), project)
	if list.Uncommitted != 2 {
		t.Fatalf("uncommitted = %d", list.Uncommitted)
	}
	code, _, no := createWithPlace(t, ts, project, `{"mode":"switch","branch":"feature"}`)
	if code != http.StatusConflict || no.Code != codeBranchDirty || no.Error != "2 files not committed" || len(no.Refs) != 1 || no.Refs[0] != "2" {
		t.Fatalf("dirty = %d %+v", code, no)
	}
	if got := headOf(t, repo); got != "main" {
		t.Fatalf("HEAD = %q", got)
	}
	code, sum, no := createWithPlace(t, ts, project, `{"mode":"branch"}`)
	if code != http.StatusCreated || !strings.HasPrefix(sum.Branch, branchPrefix) || headOf(t, repo) != sum.Branch {
		t.Fatalf("new branch = %d %+v %+v", code, sum, no)
	}
	if got := gitOutput(t, repo, "status", "--porcelain", "--untracked-files=no"); strings.Count(got, "\n")+1 != 2 {
		t.Fatalf("changes did not move along: %q", got)
	}
}

// A Thread whose conversation does not open leaves the folder as it was.
func TestFailedCreateSwitchesBack(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	repo, project := switchProject(t, ts)
	ts.prov.SetOpenError(context.DeadlineExceeded)
	if code, _, _ := createWithPlace(t, ts, project, `{"mode":"switch","branch":"origin/remote-only","remote":true}`); code == http.StatusCreated {
		t.Fatal("create succeeded")
	}
	if got := headOf(t, repo); got != "main" {
		t.Fatalf("HEAD = %q", got)
	}
	if got := gitOutput(t, repo, "branch", "--list", "remote-only"); got != "" {
		t.Fatalf("tracking branch kept: %q", got)
	}
}

// A started Thread in the Project's folder switches it at once, under the
// same rules, itself included; a worktree Thread keeps its branch.
func TestStartedThreadSwitchesTheFolder(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	repo, project := switchProject(t, ts)
	sum, err := ts.m.Create(CreateRequest{Provider: "fake", ProjectID: project, Name: "Fix the parser"})
	if err != nil {
		t.Fatal(err)
	}
	post := func(id, body string) (int, SessionSummary, refusal) {
		t.Helper()
		w := ts.do(http.MethodPost, "/api/sessions/"+id+"/git/branch", body, withCookie(ts))
		var out SessionSummary
		var no refusal
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		_ = json.Unmarshal(w.Body.Bytes(), &no)
		return w.Code, out, no
	}
	code, got, no := post(sum.ID, `{"mode":"switch","branch":"feature"}`)
	if code != http.StatusOK || got.Branch != "feature" || headOf(t, repo) != "feature" {
		t.Fatalf("switch = %d %+v %+v", code, got, no)
	}
	setSession(ts.m, sum.ID, func(s *webSession) { s.base = StateWorking })
	if code, _, no := post(sum.ID, `{"mode":"switch","branch":"main"}`); code != http.StatusConflict || no.Code != codeBranchBusy || !strings.HasPrefix(no.Error, "“Fix the parser”") {
		t.Fatalf("itself working = %d %+v", code, no)
	}
	setSession(ts.m, sum.ID, func(s *webSession) { s.base = StateCompleted })
	// A new branch takes the Thread's name.
	if code, _, no := post(sum.ID, `{"mode":"branch"}`); code != http.StatusOK {
		t.Fatalf("new branch = %d %+v", code, no)
	}
	waitUntil(t, "branch named", func() bool {
		got, _ := ts.m.Summary(sum.ID)
		return got.Branch == "uam/fix-the-parser" && headOf(t, repo) == "uam/fix-the-parser"
	})
	for _, bad := range []string{`{"mode":"worktree"}`, `{"mode":"current"}`, `{"mode":"switch"}`} {
		if code, _, no := post(sum.ID, bad); code != http.StatusBadRequest {
			t.Fatalf("%s = %d %+v", bad, code, no)
		}
	}
	wt, err := ts.m.Create(CreateRequest{Provider: "fake", ProjectID: project, Place: &PlaceRequest{Mode: placeWorktree}})
	if err != nil {
		t.Fatal(err)
	}
	if code, _, no := post(wt.ID, `{"mode":"switch","branch":"main"}`); code != http.StatusConflict || !strings.Contains(no.Error, "own worktree") {
		t.Fatalf("worktree Thread = %d %+v", code, no)
	}
	if _, err := ts.m.Archive(sum.ID); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := post(sum.ID, `{"mode":"switch","branch":"main"}`); code != http.StatusConflict {
		t.Fatalf("archived = %d", code)
	}
}
