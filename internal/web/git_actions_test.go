package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

// gitIdentity points git's global configuration at a file holding a name
// and an email, and returns that file.
func gitIdentity(t *testing.T) string {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(cfg, []byte("[user]\n\tname = Owner\n\temail = owner@example.com\n[commit]\n\tgpgsign = false\n[init]\n\tdefaultBranch = main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	return cfg
}

// gitTask is a Task in a repository whose Utility model is "b".
type gitTask struct {
	t    *testing.T
	ts   *testServer
	m    *Manager
	prov *agenttest.Provider
	repo string
	id   string
}

func newGitTask(t *testing.T, repo string) *gitTask {
	t.Helper()
	prov := agenttest.NewProvider("fake", utilityCaps)
	prov.SetModels(selectionModels(), nil)
	m := startManager(t, openTestStore(t), prov)
	srv, err := NewServer(ServerConfig{Manager: m, Token: testToken, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	g := &gitTask{t: t, ts: &testServer{srv: srv, m: m, prov: prov}, m: m, prov: prov, repo: repo}
	if w := g.ts.do(http.MethodPatch, "/api/settings", `{"title_model":{"fake":"b"}}`, withCookie(g.ts)); w.Code != http.StatusOK {
		t.Fatalf("settings = %d %s", w.Code, w.Body)
	}
	sum, err := m.Create(CreateRequest{Provider: prov.Name(), ProjectID: addProject(t, m, repo), Name: "Fix the parser"})
	if err != nil {
		t.Fatal(err)
	}
	g.id = sum.ID
	return g
}

// call sends body to the Task's git route and decodes the reply into out.
func (g *gitTask) call(method, route, body string, want int, out any) map[string]any {
	g.t.Helper()
	w := g.ts.do(method, "/api/sessions/"+g.id+"/git"+route, body, withCookie(g.ts))
	if w.Code != want {
		g.t.Fatalf("%s %s = %d %s, want %d", method, route, w.Code, w.Body, want)
	}
	if out != nil {
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			g.t.Fatal(err)
		}
		return nil
	}
	var reply map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &reply)
	return reply
}

func (g *gitTask) state() GitState {
	g.t.Helper()
	var st GitState
	g.call(http.MethodGet, "", "", http.StatusOK, &st)
	return st
}

// revert sends a whole-file revert of path and decodes the reply.
func (g *gitTask) revert(path string, want int) map[string]any {
	g.t.Helper()
	w := g.ts.do(http.MethodPost, "/api/sessions/"+g.id+"/changes/revert", `{"path":"`+path+`"}`, withCookie(g.ts))
	if w.Code != want {
		g.t.Fatalf("revert %s = %d %s, want %d", path, w.Code, w.Body, want)
	}
	var reply map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &reply)
	return reply
}

// setSession changes the Task id under the Manager's lock.
func setSession(m *Manager, id string, f func(*webSession)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f(m.sessions[id])
}

// While any Task in the repository is mid-turn, write actions are refused
// and the state says why.
func TestGitWritesWaitForTasksMidTurn(t *testing.T) {
	gitIdentity(t)
	repo := gitRepoFixture(t)
	g := newGitTask(t, repo)
	// A Task in a subdirectory of the same repository.
	other, err := g.m.Create(CreateRequest{Provider: "fake", ProjectID: addProject(t, g.m, filepath.Join(repo, "sub")), Name: "Write docs"})
	if err != nil {
		t.Fatal(err)
	}
	setSession(g.m, other.ID, func(s *webSession) { s.base = StateWorking })
	if st := g.state(); st.Busy != "“Write docs” is still working in this repository. Wait for it to finish." {
		t.Fatalf("busy = %q", st.Busy)
	}
	setEdits(g.m, g.id, true, "tracked.txt")
	if reply := g.revert("tracked.txt", http.StatusConflict); reply["code"] != codeGitBusy {
		t.Fatalf("revert while busy = %v", reply)
	}
	// More busy Threads are counted after the first name.
	var more []string
	for i, want := range []string{
		"“Write docs” and 1 other thread are still working in this repository. Wait for them to finish.",
		"“Write docs” and 2 other threads are still working in this repository. Wait for them to finish.",
	} {
		s, err := g.m.Create(CreateRequest{Provider: "fake", ProjectID: other.ProjectID, Name: fmt.Sprintf("Zeta %d", i)})
		if err != nil {
			t.Fatal(err)
		}
		more = append(more, s.ID)
		setSession(g.m, s.ID, func(s *webSession) { s.base = StateWorking })
		if st := g.state(); st.Busy != want {
			t.Fatalf("busy with %d more = %q", i+1, st.Busy)
		}
	}
	for _, id := range append(more, other.ID) {
		setSession(g.m, id, func(s *webSession) { s.base = StateIdle })
	}
	if st := g.state(); st.Busy != "" {
		t.Fatalf("busy after the turn = %q", st.Busy)
	}
	g.revert("tracked.txt", http.StatusOK)
}

func TestGitInitOnlyOutsideARepository(t *testing.T) {
	gitIdentity(t)
	dir := t.TempDir()
	g := newGitTask(t, dir)
	g.m.refreshBranches(t.Context(), true)
	if st := g.state(); st.Repo || !st.CanInit || st.Reason == "" {
		t.Fatalf("state outside git = %+v", st)
	}
	var st GitState
	g.call(http.MethodPost, "/init", "", http.StatusOK, &st)
	if !st.Repo || st.CanInit || st.HasCommits {
		t.Fatalf("state after init = %+v", st)
	}
	if p := g.m.Projects()[0]; p.NoGit != "" {
		t.Fatalf("project after init = %+v", p)
	}
	if reply := g.call(http.MethodPost, "/init", "", http.StatusConflict, nil); !strings.Contains(reply["error"].(string), "already in a Git repository") {
		t.Fatalf("second init = %v", reply)
	}
}

// setEdits records paths as the Task's edits, as its edit tools would.
func setEdits(m *Manager, id string, known bool, paths ...string) {
	setSession(m, id, func(s *webSession) {
		s.edits, s.editsKnown = map[string]time.Time{}, known
		for _, p := range paths {
			s.edits[p] = time.Now()
		}
	})
}

// Whose files are whose comes from the Tasks' edit records, which outlive
// their transcripts: an evicted transcript neither hides another Task's files
// nor this Task's own.
func TestGitStateOwnershipFromEditRecords(t *testing.T) {
	gitIdentity(t)
	repo := gitRepoFixture(t)
	writeRepoFile(t, repo, "theirs.txt", "b\n")
	g := newGitTask(t, repo)
	other, err := g.m.Create(CreateRequest{Provider: "fake", ProjectID: g.m.Projects()[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	setEdits(g.m, g.id, true, filepath.Join(repo, "tracked.txt"))
	setEdits(g.m, other.ID, true, "theirs.txt")
	setSession(g.m, other.ID, func(s *webSession) { s.items = nil })
	st := g.state()
	files := map[string]GitFile{}
	for _, f := range st.Files {
		files[f.Path] = f
	}
	if !st.TaskFilesKnown || !files["tracked.txt"].Mine || !files["theirs.txt"].OtherTask || files["sub/new file.txt"].Mine || files["sub/new file.txt"].OtherTask {
		t.Fatalf("state = %+v", st)
	}
	// After a restart nothing of this Task's is known yet.
	setEdits(g.m, g.id, false)
	if st := g.state(); st.TaskFilesKnown {
		t.Fatalf("state with unknown edits = %+v", st)
	}
}

// Background shell jobs still running, and a Task whose conversation is
// opening for a prompt, keep write actions waiting like a turn does.
func TestGitWritesWaitForBackgroundJobsAndStartingTasks(t *testing.T) {
	gitIdentity(t)
	repo := gitRepoFixture(t)
	g := newGitTask(t, repo)
	other, err := g.m.Create(CreateRequest{Provider: "fake", ProjectID: g.m.Projects()[0].ID, Name: "Build it"})
	if err != nil {
		t.Fatal(err)
	}
	conv := g.prov.Last()
	conv.Emit(agentapi.Event{Kind: agentapi.EventBackgroundTasks, BackgroundTasks: &agentapi.BackgroundTasks{Known: true, Tasks: []agentapi.BackgroundTask{{ID: "sleep", Status: "running"}}}})
	if st := g.state(); !strings.Contains(st.Busy, "“Build it” is still working") {
		t.Fatalf("busy with a background job = %q", st.Busy)
	}
	setEdits(g.m, g.id, true, "tracked.txt")
	g.revert("tracked.txt", http.StatusConflict)
	conv.Emit(agentapi.Event{Kind: agentapi.EventBackgroundTasks, BackgroundTasks: &agentapi.BackgroundTasks{Known: true, Tasks: []agentapi.BackgroundTask{{ID: "sleep", Status: "completed"}}}})
	if st := g.state(); st.Busy != "" {
		t.Fatalf("busy after the job = %q", st.Busy)
	}
	setSession(g.m, other.ID, func(s *webSession) { s.opening = make(chan struct{}) })
	if st := g.state(); st.Busy == "" {
		t.Fatal("not busy while a task is starting")
	}
	setSession(g.m, other.ID, func(s *webSession) { s.opening = nil })
}

// No turn starts in the repository while a git write action runs there: a
// prompt sent meanwhile waits for the action to end.
func TestGitWriteHoldsTurnStarts(t *testing.T) {
	gitIdentity(t)
	repo := gitRepoFixture(t)
	g := newGitTask(t, repo)
	other, err := g.m.Create(CreateRequest{Provider: "fake", ProjectID: g.m.Projects()[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	conv := g.prov.Last()
	// The repository's write lock, as a revert or a set-up holds it.
	end, err := g.m.beginWrite(repo)
	if err != nil {
		t.Fatal(err)
	}
	sent := make(chan error, 1)
	go func() {
		_, err := g.m.Submit(other.ID, PromptRequest{Text: "edit things", RequestID: mustUUID(t), Mode: ModeSend})
		sent <- err
	}()
	time.Sleep(300 * time.Millisecond)
	if n := len(conv.Sends()); n != 0 {
		t.Fatalf("a turn started mid-write: %d sends", n)
	}
	end()
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
	if n := len(conv.Sends()); n != 1 {
		t.Fatalf("sends after the write = %d", n)
	}
}

// A Thread in another repository that edited files of the same names is no
// evidence about this repository's files.
func TestGitStateIgnoresEditsInOtherRepositories(t *testing.T) {
	gitIdentity(t)
	repo := gitRepoFixture(t)
	g := newGitTask(t, repo)
	elsewhere := gitRepoFixture(t)
	other, err := g.m.Create(CreateRequest{Provider: "fake", ProjectID: addProject(t, g.m, elsewhere)})
	if err != nil {
		t.Fatal(err)
	}
	setEdits(g.m, g.id, true)
	setEdits(g.m, other.ID, true, "tracked.txt", filepath.Join(elsewhere, "sub/new file.txt"))
	for _, f := range g.state().Files {
		if f.OtherTask || f.Mine {
			t.Fatalf("file attributed from another repository: %+v", f)
		}
	}
}
