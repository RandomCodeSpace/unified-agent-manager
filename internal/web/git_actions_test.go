package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

// setSession changes the Task id under the Manager's lock.
func setSession(m *Manager, id string, f func(*webSession)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f(m.sessions[id])
}

func editItem(path string) agentapi.Item {
	return agentapi.Item{Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "edit", Status: agentapi.ToolCompleted, Input: `{"path":"` + path + `"}`}}
}

func TestGitCommitStagesExactlyTheChosenFiles(t *testing.T) {
	gitIdentity(t)
	repo := branchRepo(t)
	writeRepoFile(t, repo, "keep.txt", "keep\n")
	writeRepoFile(t, repo, "gone.txt", "gone\n")
	writeRepoFile(t, repo, "other.txt", "other\n")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "feat: seed")
	writeRepoFile(t, repo, "keep.txt", "kept\n")
	writeRepoFile(t, repo, "sub/new :(glob)*.txt", "new\n")
	if err := os.Remove(filepath.Join(repo, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	writeRepoFile(t, repo, "other.txt", "staged by hand\n")
	gitIn(t, repo, "add", "other.txt")
	writeRepoFile(t, repo, "left.txt", "not chosen\n")
	g := newGitTask(t, repo)

	// This Task edited keep.txt; another Task edited left.txt.
	other, err := g.m.Create(CreateRequest{Provider: "fake", ProjectID: g.m.Projects()[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	setSession(g.m, g.id, func(s *webSession) { s.items, s.history = []agentapi.Item{editItem("keep.txt")}, HistoryLoaded })
	setSession(g.m, other.ID, func(s *webSession) { s.items = []agentapi.Item{editItem(filepath.Join(repo, "left.txt"))} })
	st := g.state()
	files := map[string]GitFile{}
	for _, f := range st.Files {
		files[f.Path] = f
	}
	if !st.Repo || st.Branch != "main" || !st.HasCommits || st.Remote || st.Busy != "" || !st.TaskFilesKnown || len(files) != 5 ||
		!files["keep.txt"].Mine || files["keep.txt"].OtherTask || !files["left.txt"].OtherTask || files["gone.txt"].Mine || files["gone.txt"].Status != "deleted" {
		t.Fatalf("state = %+v", st)
	}

	message := "fix(parser): keep the kept line\n\nThe body stays  as written.\n# not a comment to strip"
	body, _ := json.Marshal(map[string]any{"paths": []string{"keep.txt", "gone.txt", "sub/new :(glob)*.txt"}, "message": message + "\n\n"})
	var res GitResult
	g.call(http.MethodPost, "/commit", string(body), http.StatusOK, &res)
	if res.Commit == "" || !strings.HasPrefix(res.Summary, "Committed 3 files as ") {
		t.Fatalf("commit = %+v", res)
	}
	if got := gitOutput(t, repo, "log", "-1", "--format=%B"); got != message {
		t.Fatalf("message = %q, want %q", got, message)
	}
	if got := gitOutput(t, repo, "show", "--name-status", "--format=", "HEAD"); got != "D\tgone.txt\nM\tkeep.txt\nA\tsub/new :(glob)*.txt" {
		t.Fatalf("commit files = %q", got)
	}
	// What was staged by hand stays staged; what was not chosen stays.
	if got := gitOutput(t, repo, "status", "--porcelain"); got != "M  other.txt\n?? left.txt" {
		t.Fatalf("status after = %q", got)
	}
}

func TestGitCommitRefusals(t *testing.T) {
	cfg := gitIdentity(t)
	repo := gitRepoFixture(t)
	g := newGitTask(t, repo)
	for _, tc := range []struct{ body, want string }{
		{`{"paths":["tracked.txt"],"message":"  \n"}`, "write a commit message"},
		{`{"paths":[],"message":"x"}`, "choose at least one file"},
		{`{"paths":["unchanged.txt"],"message":"x"}`, "is not a changed file"},
		{`{"paths":["../outside.txt"],"message":"x"}`, "is not a changed file"},
		{`{"paths":["/etc/passwd"],"message":"x"}`, "is not a changed file"},
	} {
		if reply := g.call(http.MethodPost, "/commit", tc.body, http.StatusBadRequest, nil); !strings.Contains(reply["error"].(string), tc.want) {
			t.Fatalf("%s: %v", tc.body, reply)
		}
	}

	// A failing hook refuses the commit with its output.
	hook := filepath.Join(repo, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho 'lint: tracked.txt is wrong' >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	reply := g.call(http.MethodPost, "/commit", `{"paths":["tracked.txt"],"message":"fix: x"}`, http.StatusConflict, nil)
	if msg := reply["error"].(string); !strings.Contains(msg, "Git refused the commit") || !strings.Contains(msg, "lint: tracked.txt is wrong") {
		t.Fatalf("hook refusal = %q", msg)
	}
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}

	// No identity: a plain sentence on how to set one.
	if err := os.WriteFile(cfg, []byte("[user]\n\tuseConfigOnly = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_AUTHOR_NAME", "")
	t.Setenv("GIT_AUTHOR_EMAIL", "")
	t.Setenv("GIT_COMMITTER_NAME", "")
	t.Setenv("GIT_COMMITTER_EMAIL", "")
	t.Setenv("EMAIL", "")
	reply = g.call(http.MethodPost, "/commit", `{"paths":["tracked.txt"],"message":"fix: x"}`, http.StatusConflict, nil)
	if msg := reply["error"].(string); !strings.Contains(msg, "Git doesn't know who you are") {
		t.Fatalf("identity refusal = %q", msg)
	}
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
	if st := g.state(); st.Busy != "“Write docs” is still working in this repository. Wait for its turn to finish." {
		t.Fatalf("busy = %q", st.Busy)
	}
	for _, route := range []string{"/commit", "/push", "/pull"} {
		body := ""
		if route == "/commit" {
			body = `{"paths":["tracked.txt"],"message":"fix: x"}`
		}
		if reply := g.call(http.MethodPost, route, body, http.StatusConflict, nil); reply["code"] != codeGitBusy {
			t.Fatalf("%s while busy = %v", route, reply)
		}
	}
	setSession(g.m, other.ID, func(s *webSession) { s.base = StateIdle })
	if st := g.state(); st.Busy != "" {
		t.Fatalf("busy after the turn = %q", st.Busy)
	}
	g.call(http.MethodPost, "/commit", `{"paths":["tracked.txt"],"message":"fix: x"}`, http.StatusOK, nil)
}

func TestGitPushAndPull(t *testing.T) {
	gitIdentity(t)
	repo := gitRepoFixture(t)
	g := newGitTask(t, repo)
	if reply := g.call(http.MethodPost, "/push", "", http.StatusConflict, nil); !strings.Contains(reply["error"].(string), "no remote named origin") {
		t.Fatalf("push without a remote = %v", reply)
	}
	if reply := g.call(http.MethodPost, "/pull", "", http.StatusConflict, nil); !strings.Contains(reply["error"].(string), "no upstream") {
		t.Fatalf("pull without an upstream = %v", reply)
	}
	remote := filepath.Join(t.TempDir(), "remote.git")
	gitIn(t, repo, "init", "-q", "--bare", remote)
	gitIn(t, repo, "remote", "add", "origin", remote)
	branch := gitOutput(t, repo, "branch", "--show-current")
	var res GitResult
	g.call(http.MethodPost, "/push", "", http.StatusOK, &res)
	if res.Summary != "Pushed "+branch+" to origin." || gitOutput(t, repo, "rev-parse", "--abbrev-ref", "@{u}") != "origin/"+branch {
		t.Fatalf("first push = %+v", res)
	}
	g.call(http.MethodPost, "/commit", `{"paths":["tracked.txt"],"message":"fix: one"}`, http.StatusOK, nil)
	if st := g.state(); st.Ahead != 1 || st.Upstream != "origin/"+branch || !st.Remote {
		t.Fatalf("state before push = %+v", st)
	}
	g.call(http.MethodPost, "/push", "", http.StatusOK, &res)
	if gitOutput(t, remote, "log", "-1", "--format=%s", branch) != "fix: one" {
		t.Fatal("the push did not reach the remote")
	}

	// Someone else pushes; Pull fast-forwards.
	clone := filepath.Join(t.TempDir(), "clone")
	gitIn(t, repo, "clone", "-q", remote, clone)
	writeRepoFile(t, clone, "remote.txt", "r\n")
	gitIn(t, clone, "add", ".")
	gitIn(t, clone, "commit", "-q", "-m", "feat: remote")
	gitIn(t, clone, "push", "-q")
	g.call(http.MethodPost, "/pull", "", http.StatusOK, &res)
	if res.Summary != "Pulled 1 new commit." || gitOutput(t, repo, "log", "-1", "--format=%s") != "feat: remote" {
		t.Fatalf("pull = %+v", res)
	}
	g.call(http.MethodPost, "/pull", "", http.StatusOK, &res)
	if res.Summary != "Already up to date." {
		t.Fatalf("second pull = %+v", res)
	}

	// Both sides move: Pull refuses to merge, Push refuses to force.
	writeRepoFile(t, clone, "remote.txt", "r2\n")
	gitIn(t, clone, "commit", "-q", "-am", "feat: remote two")
	gitIn(t, clone, "push", "-q")
	g.call(http.MethodPost, "/commit", `{"paths":["sub/new file.txt"],"message":"feat: local"}`, http.StatusOK, nil)
	gitIn(t, repo, "fetch", "-q")
	if reply := g.call(http.MethodPost, "/pull", "", http.StatusConflict, nil); !strings.HasPrefix(reply["error"].(string), "Can't fast-forward") {
		t.Fatalf("diverged pull = %v", reply)
	}
	if reply := g.call(http.MethodPost, "/push", "", http.StatusConflict, nil); !strings.Contains(reply["error"].(string), "Pull first, then push") {
		t.Fatalf("diverged push = %v", reply)
	}
	if gitOutput(t, remote, "log", "-1", "--format=%s", branch) != "feat: remote two" {
		t.Fatal("a refused push changed the remote")
	}
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
	// The first commit works too.
	writeRepoFile(t, dir, "a.txt", "a\n")
	g.call(http.MethodPost, "/commit", `{"paths":["a.txt"],"message":"Initial commit"}`, http.StatusOK, nil)
}

func TestDraftCommitMessage(t *testing.T) {
	gitIdentity(t)
	repo := gitRepoFixture(t)
	gitIn(t, repo, "commit", "-q", "--allow-empty", "-m", "feat(web): add the panel")
	gitIn(t, repo, "commit", "-q", "--allow-empty", "-m", "fix: keep the order")
	g := newGitTask(t, repo)
	g.prov.SetUtilityHook(func(context.Context, agentapi.UtilityRequest) (string, error) {
		return "```\nfix(parser): keep three lines\n\nThe parser now keeps them.\n\nCo-authored-by: Copilot <copilot@github.com>\nGenerated with an AI assistant\n```", nil
	})
	var draft CommitDraft
	g.call(http.MethodPost, "/message", `{"paths":["tracked.txt","sub/new file.txt"]}`, http.StatusOK, &draft)
	if draft != (CommitDraft{Message: "fix(parser): keep three lines\n\nThe parser now keeps them.", Conventional: true, Model: "b"}) {
		t.Fatalf("draft = %+v", draft)
	}
	reqs := g.prov.UtilityRequests()
	if len(reqs) != 1 || reqs[0].Purpose != "commit-message" || reqs[0].Model != "b" || reqs[0].System != commitDraftSystem || reqs[0].Timeout != commitDraftTimeout {
		t.Fatalf("utility requests = %+v", reqs)
	}
	for _, part := range []string{"fix: keep the order\nfeat(web): add the panel\ninit\n</recent_subjects>", "use Conventional Commits",
		"tracked.txt (changed)\nsub/new file.txt (new)\n</files>", "+three", "+b\n"} {
		if !strings.Contains(reqs[0].Prompt, part) {
			t.Fatalf("prompt %q lacks %q", reqs[0].Prompt, part)
		}
	}
	g.call(http.MethodPost, "/message", `{"paths":["unchanged.txt"]}`, http.StatusBadRequest, nil)
	// Background AI off: refused before the model, with the limit's own words.
	if w := g.ts.do(http.MethodPatch, "/api/settings", `{"utility_daily_limit":0}`, withCookie(g.ts)); w.Code != http.StatusOK {
		t.Fatalf("settings = %d %s", w.Code, w.Body)
	}
	if reply := g.call(http.MethodPost, "/message", `{"paths":["tracked.txt"]}`, http.StatusConflict, nil); reply["code"] != codeUtilityPaused || len(g.prov.UtilityRequests()) != 1 {
		t.Fatalf("paused draft = %v", reply)
	}
	if w := g.ts.do(http.MethodPatch, "/api/settings", `{"utility_daily_limit":null}`, withCookie(g.ts)); w.Code != http.StatusOK {
		t.Fatalf("settings = %d %s", w.Code, w.Body)
	}
	g.prov.SetUtilityHook(func(context.Context, agentapi.UtilityRequest) (string, error) { return "", errors.New("model gone") })
	if reply := g.call(http.MethodPost, "/message", `{"paths":["tracked.txt"]}`, http.StatusBadGateway, nil); reply["code"] != codeUtilityFailed {
		t.Fatalf("failed draft = %v", reply)
	}
	if len(gitOutput(t, repo, "status", "--porcelain")) == 0 || gitOutput(t, repo, "log", "-1", "--format=%s") != "fix: keep the order" {
		t.Fatal("drafting a message changed the repository")
	}
}

func TestCleanCommitMessage(t *testing.T) {
	for reply, want := range map[string]string{
		"feat: add x":                         "feat: add x",
		"<think>hmm</think>\n\"fix: y\"\n\n":  "fix: y",
		"Commit message: docs: z\n\n\n\nbody": "docs: z\n\nbody",
		"fix: a\n\nbody\n\nSigned-off-by: A <a@b>\n🤖 Generated with [Claude Code](https://x)\nAssisted-by: GPT": "fix: a\n\nbody",
		"```text\nchore: b\n```": "chore: b",
		"Co-authored-by: x <y>":  "",
	} {
		if got := cleanCommitMessage(reply); got != want {
			t.Fatalf("cleanCommitMessage(%q) = %q, want %q", reply, got, want)
		}
	}
	if !conventionalRepo([]string{"feat(x): a", "fix!: b", "Merge things"}) || conventionalRepo([]string{"Add a", "Fix b", "feat: c"}) || conventionalRepo(nil) {
		t.Fatal("conventionalRepo misjudged")
	}
}
