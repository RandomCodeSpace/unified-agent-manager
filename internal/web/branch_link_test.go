package web

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/execpath"
)

func TestCleanBranchLink(t *testing.T) {
	for _, tc := range []struct {
		in, want, err string
	}{
		{"", "", ""},
		{"   ", "", ""},
		{" https://github.com/org/repo/tree/{branch} ", "https://github.com/org/repo/tree/{branch}", ""},
		{"https://git.example.com/repo?ref={branch}", "https://git.example.com/repo?ref={branch}", ""},
		{"http://github.com/org/repo/tree/{branch}", "", "https address"},
		{"github.com/org/repo/tree/{branch}", "", "https address"},
		{"https:///org/repo/tree/{branch}", "", "https address"},
		{"javascript:alert(1)//{branch}", "", "https address"},
		{"https://exa mple.com/{branch}", "", "https address"},
		{"https://user:pass@github.com/org/repo/tree/{branch}", "", "user name or password"},
		{"https://user@github.com/org/repo/tree/{branch}", "", "user name or password"},
		{"https://github.com/org/repo/tree/main", "", "exactly once"},
		{"https://github.com/{branch}/tree/{branch}", "", "exactly once"},
		{"https://github.com/" + strings.Repeat("a", maxBranchLink) + "/{branch}", "", "longer than"},
	} {
		got, err := cleanBranchLink(tc.in)
		if tc.err == "" {
			if err != nil || got != tc.want {
				t.Errorf("cleanBranchLink(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
			}
			continue
		}
		if err == nil || statusOf(err) != http.StatusBadRequest || !strings.Contains(err.Error(), tc.err) {
			t.Errorf("cleanBranchLink(%q) = %q, %v; want a 400 naming %q", tc.in, got, err, tc.err)
		}
	}
}

func TestLoadedBranchLink(t *testing.T) {
	valid, cleared, invalid, padded := "https://github.com/o/r/tree/{branch}", "", "https://github.com/o/r", " https://github.com/o/r/tree/{branch}"
	if loadedBranchLink(nil) != nil || loadedBranchLink(&invalid) != nil || loadedBranchLink(&padded) != nil {
		t.Fatal("an unset or invalid saved link was kept")
	}
	if got := loadedBranchLink(&valid); got == nil || *got != valid {
		t.Fatalf("valid link loaded as %v", got)
	}
	if got := loadedBranchLink(&cleared); got == nil || *got != "" {
		t.Fatalf("cleared link loaded as %v", got)
	}
}

func TestBranchLinkFromRemote(t *testing.T) {
	for _, tc := range []struct{ remote, want string }{
		{"https://github.com/org/repo.git\n", "https://github.com/org/repo/tree/{branch}"},
		{"git@github.com:org/repo.git", "https://github.com/org/repo/tree/{branch}"},
		{"ssh://git@GitHub.com:22/org/repo", "https://github.com/org/repo/tree/{branch}"},
		{"https://user:secret@gitlab.example.com/group/sub/project.git", "https://gitlab.example.com/group/sub/project/-/tree/{branch}"},
		{"git@code.corp.example:team/project.git", "https://code.corp.example/team/project/-/tree/{branch}"},
		{"https://code.example/team/my%20project", "https://code.example/team/my%20project/-/tree/{branch}"},
		{"/srv/repos/project.git", ""},
		{"https://github.com/repo", ""},
		{"ftp://github.com/org/repo", ""},
		{"https://github.com/{branch}/repo", "https://github.com/%7Bbranch%7D/repo/tree/{branch}"},
		{"https://gitlab.example.com/g/" + strings.Repeat("a", maxBranchLink), ""},
		{"", ""},
	} {
		got, ok := branchLinkFromRemote(tc.remote)
		if got != tc.want || ok != (tc.want != "") {
			t.Errorf("branchLinkFromRemote(%q) = %q, %v; want %q", tc.remote, got, ok, tc.want)
		}
	}
}

func TestFillBranchLink(t *testing.T) {
	if got := fillBranchLink("https://github.com/o/r/tree/{branch}", "feat/a b#?"); got != "https://github.com/o/r/tree/feat/a%20b%23%3F" {
		t.Fatalf("filled = %q", got)
	}
}

// branchLinkRepo is a git work tree on branch feat/a b with an origin remote
// that was never fetched; run runs git in it.
func branchLinkRepo(t *testing.T, origin string) (string, func(args ...string)) {
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
	run("init", "-q")
	run("commit", "-q", "--allow-empty", "-m", "init")
	run("checkout", "-q", "-b", "feat/a")
	if origin != "" {
		run("remote", "add", "origin", origin)
	}
	return dir, run
}

func TestBranchLinkDetectedOnAddAndOpensAPushedBranch(t *testing.T) {
	dir, run := branchLinkRepo(t, "https://token@github.com/org/repo.git")
	ts := newTestServer(t, ServerConfig{})
	auth := withCookie(ts)
	p, err := ts.m.AddProject(dir, "")
	if err != nil || p.BranchLink == nil || *p.BranchLink != "https://github.com/org/repo/tree/{branch}" {
		t.Fatalf("AddProject = %+v, %v", p, err)
	}
	cfg, err := ts.m.store.Load()
	if err != nil || cfg.WebProjects[p.ID].BranchLink == nil || *cfg.WebProjects[p.ID].BranchLink != *p.BranchLink {
		t.Fatalf("saved project = %+v, %v", cfg.WebProjects[p.ID], err)
	}
	sum, err := ts.m.Create(CreateRequest{Provider: "fake", ProjectID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	if sum.BranchLinkURL != "" {
		t.Fatalf("link before the Thread was opened = %q", sum.BranchLinkURL)
	}
	open := func() SessionDetail {
		t.Helper()
		w := ts.do(http.MethodGet, "/api/sessions/"+sum.ID, "", auth)
		var d SessionDetail
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &d) != nil {
			t.Fatalf("detail = %d %s", w.Code, w.Body)
		}
		return d
	}
	// Not pushed and origin's default branch unknown: main.
	if d := open(); d.Branch != "feat/a" || d.BranchLinkBranch != "main" || d.BranchLinkURL != "https://github.com/org/repo/tree/main" {
		t.Fatalf("unpushed link = %q %q", d.BranchLinkBranch, d.BranchLinkURL)
	}
	run("update-ref", "refs/remotes/origin/trunk", "HEAD")
	run("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")
	if d := open(); d.BranchLinkBranch != "trunk" || d.BranchLinkURL != "https://github.com/org/repo/tree/trunk" {
		t.Fatalf("link with origin's default branch = %q %q", d.BranchLinkBranch, d.BranchLinkURL)
	}
	run("update-ref", "refs/remotes/origin/feat/a", "HEAD")
	if d := open(); d.BranchLinkBranch != "feat/a" || d.BranchLinkURL != "https://github.com/org/repo/tree/feat/a" {
		t.Fatalf("pushed link = %q %q", d.BranchLinkBranch, d.BranchLinkURL)
	}
	// Cleared by the owner: no link, and opening never detects it again.
	if w := ts.do(http.MethodPatch, "/api/projects/"+p.ID, `{"branch_link":""}`, auth); w.Code != http.StatusOK {
		t.Fatalf("clear = %d %s", w.Code, w.Body)
	}
	if d := open(); d.BranchLinkURL != "" || d.BranchLinkBranch != "" {
		t.Fatalf("cleared link = %q %q", d.BranchLinkBranch, d.BranchLinkURL)
	}
	if cfg, err := ts.m.store.Load(); err != nil || cfg.WebProjects[p.ID].BranchLink == nil || *cfg.WebProjects[p.ID].BranchLink != "" {
		t.Fatalf("cleared link saved as %v, %v", cfg.WebProjects[p.ID].BranchLink, err)
	}
}

// A Project from before branch links, or added without an origin, has its
// link detected the first time one of its Threads is opened.
func TestBranchLinkDetectedLazilyOnOpen(t *testing.T) {
	dir, run := branchLinkRepo(t, "")
	ts := newTestServer(t, ServerConfig{})
	p, err := ts.m.AddProject(dir, "")
	if err != nil || p.BranchLink != nil {
		t.Fatalf("AddProject without origin = %+v, %v", p, err)
	}
	sum, err := ts.m.Create(CreateRequest{Provider: "fake", ProjectID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	run("remote", "add", "origin", "git@gitlab.example.com:group/project.git")
	ts.m.mu.Lock()
	ts.m.projects[p.ID].linkTried = false // as loaded from an older version
	ts.m.mu.Unlock()
	w := ts.do(http.MethodGet, "/api/sessions/"+sum.ID, "", withCookie(ts))
	var d SessionDetail
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &d) != nil || d.BranchLinkURL != "https://gitlab.example.com/group/project/-/tree/main" {
		t.Fatalf("detail = %d %q", w.Code, d.BranchLinkURL)
	}
	if cfg, err := ts.m.store.Load(); err != nil || cfg.WebProjects[p.ID].BranchLink == nil {
		t.Fatalf("detected link not saved: %v", err)
	}
}

func TestDetectBranchLinkRoute(t *testing.T) {
	dir, _ := branchLinkRepo(t, "git@gitlab.example.com:group/project.git")
	bare := t.TempDir()
	ts := newTestServer(t, ServerConfig{})
	auth := withCookie(ts)
	p := addProject(t, ts.m, dir)
	none := addProject(t, ts.m, bare)
	if _, err := ts.m.patchProject(p, nil, new(string), nil); err != nil {
		t.Fatal(err)
	}
	w := ts.do(http.MethodPost, "/api/projects/"+p+"/branch-link/detect", "", auth)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"branch_link":"https://gitlab.example.com/group/project/-/tree/{branch}"}` {
		t.Fatalf("detect = %d %s", w.Code, w.Body)
	}
	// Detecting only suggests: the cleared link stays cleared.
	if got := ts.m.Projects(); len(got) != 2 || (got[0].ID == p && *got[0].BranchLink != "") || (got[1].ID == p && *got[1].BranchLink != "") {
		t.Fatalf("projects after detect = %+v", got)
	}
	if w := ts.do(http.MethodPost, "/api/projects/"+none+"/branch-link/detect", "", auth); w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "origin") {
		t.Fatalf("detect without origin = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodPost, "/api/projects/missing/branch-link/detect", "", auth); w.Code != http.StatusNotFound {
		t.Fatalf("detect unknown project = %d %s", w.Code, w.Body)
	}
}

func TestPatchProjectBranchLink(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	auth := withCookie(ts)
	id := addProject(t, ts.m, t.TempDir())
	link := "https://gitlab.example.com/g/p/-/tree/{branch}"
	w := ts.do(http.MethodPatch, "/api/projects/"+id, `{"branch_link":" `+link+` "}`, auth)
	var p Project
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &p) != nil || p.BranchLink == nil || *p.BranchLink != link || p.Name == "" {
		t.Fatalf("set link = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodPatch, "/api/projects/"+id, `{"name":"renamed"}`, auth); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), link) {
		t.Fatalf("rename kept link = %d %s", w.Code, w.Body)
	}
	w = ts.do(http.MethodPatch, "/api/projects/"+id, `{"name":"again","branch_link":"https://github.com/o/r"}`, auth)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "{branch} exactly once") {
		t.Fatalf("invalid link = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodPatch, "/api/projects/"+id, `{}`, auth); w.Code != http.StatusBadRequest {
		t.Fatalf("empty patch = %d %s", w.Code, w.Body)
	}
	cfg, err := ts.m.store.Load()
	if err != nil || cfg.WebProjects[id].Name != "renamed" || cfg.WebProjects[id].BranchLink == nil || *cfg.WebProjects[id].BranchLink != link {
		t.Fatalf("saved = %+v, %v", cfg.WebProjects[id], err)
	}
}
