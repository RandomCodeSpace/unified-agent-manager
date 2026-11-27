package web

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func glabTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	b, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, b, err)
	}
	return strings.TrimSpace(string(b))
}

func glabTestRepo(t *testing.T, remote string) string {
	t.Helper()
	dir := t.TempDir()
	glabTestGit(t, dir, "init", "-b", "main")
	glabTestGit(t, dir, "config", "user.email", "fixture@example.com")
	glabTestGit(t, dir, "config", "user.name", "Fixture")
	glabTestGit(t, dir, "remote", "add", "origin", remote)
	return dir
}

func glabTestWrite(t *testing.T, file, text string) {
	t.Helper()
	if err := os.WriteFile(file, []byte(text), 0700); err != nil {
		t.Fatal(err)
	}
}

func glabTestFake(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "glab")
	glabTestWrite(t, bin, "#!/bin/sh\n"+script)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return bin
}

func glabWait(t *testing.T, g *glabGovernor, dir string) glabCachedGate {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		g.mu.Lock()
		e := g.dirs[dir]
		g.mu.Unlock()
		if !e.loading && !e.at.IsZero() {
			return e
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("gate refresh did not complete")
	return glabCachedGate{}
}

func TestGlabGate(t *testing.T) {
	for _, tc := range []struct {
		name, remote, script string
		enabled              bool
	}{
		{"github", "https://github.com/group/repo.git", "exit 0", false},
		{"signed-out", "https://gitlab.com/group/repo.git", "exit 1", false},
		{"https", "https://gitlab.com/group/sub/repo.git", "exit 0", true},
		{"scp", "git@gitlab.com:group/sub/repo.git", "exit 0", true},
		{"local", "/tmp/local-repo", "exit 0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			glabTestFake(t, tc.script)
			dir := glabTestRepo(t, tc.remote)
			var g glabGovernor
			if _, ok := g.gate(t.Context(), dir); ok {
				t.Fatal("cold gate enabled")
			}
			e := glabWait(t, &g, dir)
			if e.ok != tc.enabled {
				t.Fatalf("gate=%+v", e)
			}
		})
	}
	t.Run("missing", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		if _, ok := glabOrigin(t.Context(), t.TempDir()); ok {
			t.Fatal("missing executable enabled")
		}
	})
}

func TestGlabAuthCacheAndExpiry(t *testing.T) {
	bin := glabTestFake(t, "printf 'auth\\n' >> \"$0.calls\"\nexit 0")
	var g glabGovernor
	dir := glabTestRepo(t, "https://gitlab.com/group/repo.git")
	other := glabTestRepo(t, "https://gitlab.com/group/other.git")
	for _, d := range []string{dir, other} {
		g.gate(t.Context(), d)
		if !glabWait(t, &g, d).ok {
			t.Fatal("auth failed")
		}
	}
	if _, ok := g.gate(t.Context(), dir); !ok {
		t.Fatal("warm gate disabled")
	}
	data, err := os.ReadFile(bin + ".calls")
	if err != nil || string(data) != "auth\n" {
		t.Fatalf("auth calls=%q error=%v", data, err)
	}
	g.mu.Lock()
	e := g.dirs[dir]
	e.at = time.Now().Add(-glabTTL)
	g.dirs[dir] = e
	g.hosts["gitlab.com"].at = e.at
	g.mu.Unlock()
	if _, ok := g.gate(t.Context(), dir); ok {
		t.Fatal("expired gate enabled")
	}
	if !glabWait(t, &g, dir).ok {
		t.Fatal("refresh failed")
	}
	data, _ = os.ReadFile(bin + ".calls")
	if string(data) != "auth\nauth\n" {
		t.Fatalf("calls=%q", data)
	}
}

// A failed sign-in check expires after a minute, so signing in to glab
// takes effect quickly; a good one is kept longer.
func TestGlabNegativeAuthExpiresSooner(t *testing.T) {
	// The exit code is read from a file: rewriting a script that was just
	// executed can fail with "text file busy".
	bin := glabTestFake(t, "printf 'auth\\n' >> \"$0.calls\"\nexit \"$(cat \"$0.code\")\"")
	glabTestWrite(t, bin+".code", "1")
	var g glabGovernor
	dir := glabTestRepo(t, "https://gitlab.com/group/repo.git")
	g.gate(t.Context(), dir)
	if glabWait(t, &g, dir).ok {
		t.Fatal("signed-out gate enabled")
	}
	g.mu.Lock()
	e := g.dirs[dir]
	e.at = time.Now().Add(-glabNegativeTTL)
	g.dirs[dir] = e
	g.hosts["gitlab.com"].at = e.at
	g.mu.Unlock()
	glabTestWrite(t, bin+".code", "0")
	g.gate(t.Context(), dir)
	if !glabWait(t, &g, dir).ok {
		t.Fatal("sign-in was not picked up after the negative cache expired")
	}
	if data, _ := os.ReadFile(bin + ".calls"); string(data) != "auth\nauth\n" {
		t.Fatalf("calls=%q", data)
	}
	if glabCacheTTL(true) != glabTTL {
		t.Fatal("a good sign-in check expired early")
	}
}

func TestGlabRunnerBounds(t *testing.T) {
	bin := glabTestFake(t, "printf '%s' sensitive-auth-text\nexit 0")
	gate := glabGate{Dir: t.TempDir(), Host: "gitlab.com", Path: "group/repo", executable: bin}
	if out, err := glabRun(t.Context(), gate, time.Second, "auth status"); err != nil || out != "" {
		t.Fatalf("auth output=%q err=%v", out, err)
	}
	if _, err := glabRun(t.Context(), gate, time.Second, "api"); err == nil {
		t.Fatal("unsupported verb ran")
	}
	glabTestWrite(t, bin, "#!/bin/sh\nexec sleep 2\n")
	if _, err := glabRun(t.Context(), gate, time.Millisecond, "ci lint", "./.gitlab-ci.yml"); err != context.DeadlineExceeded {
		t.Fatalf("timeout=%v", err)
	}
	glabTestWrite(t, bin, "#!/bin/sh\nhead -c 1048580 /dev/zero\n")
	if out, err := glabRun(t.Context(), gate, time.Second, "ci lint"); len(out) != glabOutputLimit || err == nil {
		t.Fatalf("bound=%d err=%v", len(out), err)
	}
}

// A branch whose latest pipeline failed: uam shows the failed status and
// which job, in which stage, failed and why; the job that passed stays passed.
func TestGlabFailedPipeline(t *testing.T) {
	branch := "feat/failing"
	pipeline := gitLabFixturePipeline(gitLabFixtureSHA, branch)
	responses := gitLabClientResponses(branch)
	responses["projects/42/pipelines/91/jobs?include_retried=false&per_page=100&page=1"] = gitLabFixtureResponse{body: gitLabFixtureJSON([]any{
		map[string]any{"id": 1, "name": "compile", "stage": "build", "status": "success", "pipeline": pipeline},
		map[string]any{"id": 2, "name": "unit", "stage": "test", "status": "failed", "failure_reason": "script_failure", "pipeline": pipeline},
	})}
	got, err := readGitLabPipeline(t.Context(), gitLabClientFake(t, responses), gitLabFixtureSHA, branch)
	if err != nil {
		t.Fatal(err)
	}
	if got.Pipeline == nil || got.Pipeline.ID != 91 || got.Pipeline.Status != "failed" {
		t.Fatalf("pipeline = %+v", got.Pipeline)
	}
	var failed []GitLabJob
	for _, job := range got.Jobs {
		if job.Status == "failed" {
			failed = append(failed, job)
		}
	}
	if len(got.Jobs) != 2 || len(failed) != 1 || failed[0].Name != "unit" || failed[0].Stage != "test" || failed[0].FailureReason != "script_failure" {
		t.Fatalf("jobs = %+v", got.Jobs)
	}
}

// glabGit runs git with the package's read-only flags: a repository's own
// fsmonitor command never runs, and a failing command is an error.
func TestGlabGitUsesTheReadOnlyGitFlags(t *testing.T) {
	repo := gitRepoFixture(t)
	marker := filepath.Join(t.TempDir(), "ran")
	script := filepath.Join(t.TempDir(), "monitor.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "config", "core.fsmonitor", script)
	out, err := glabGit(t.Context(), repo, "diff", "--name-only", "-z", "--")
	if err != nil || out != "tracked.txt\x00" {
		t.Fatalf("diff = %q, %v", out, err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("repository fsmonitor ran: %v", err)
	}
	if _, err := glabGit(t.Context(), repo, "show-ref", "--verify", "--quiet", "refs/remotes/origin/none"); err == nil {
		t.Fatal("failing git command reported success")
	}
}

func TestGlabWebURL(t *testing.T) {
	gate := glabGate{Host: "ssh.example.com", Path: "group/sub/repo"}
	for _, tc := range []struct{ name, out, want string }{
		{"ssh host differs", `{"web_url":"https://gitlab.example.com/group/sub/repo"}`, "https://gitlab.example.com/group/sub/repo"},
		{"port", `{"web_url":"https://gitlab.example.com:8443/group/sub/repo"}`, "https://gitlab.example.com:8443/group/sub/repo"},
		{"sub-path", `{"web_url":"https://example.com/gitlab/group/sub/repo/"}`, "https://example.com/gitlab/group/sub/repo"},
		{"http", `{"web_url":"http://gitlab.example.com/group/sub/repo"}`, "http://gitlab.example.com/group/sub/repo"},
		{"case and warning", "warning: update available\n" + `{"web_url":"https://GitLab.Example.com/Group/Sub/Repo"}`, "https://gitlab.example.com/group/sub/repo"},
		{"other project", `{"web_url":"https://gitlab.example.com/group/other"}`, ""},
		{"user", `{"web_url":"https://user:secret@gitlab.example.com/group/sub/repo"}`, ""},
		{"scheme", `{"web_url":"javascript://gitlab.example.com/group/sub/repo"}`, ""},
		{"no host", `{"web_url":"https:///group/sub/repo"}`, ""},
		{"query", `{"web_url":"https://gitlab.example.com/group/sub/repo?x=1"}`, ""},
		{"fragment", `{"web_url":"https://gitlab.example.com/group/sub/repo#x"}`, ""},
		{"empty", `{}`, ""},
		{"not json", "glab: not found", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := glabWebURL(gate, tc.out); got != tc.want {
				t.Fatalf("glabWebURL = %q, want %q", got, tc.want)
			}
		})
	}
}

// withWeb reads the web address once per origin remote URL, again when the
// remote changes, and falls back to the remote's address when glab is signed
// out or the read fails, retrying after the negative TTL.
func TestGlabWithWeb(t *testing.T) {
	bin := glabTestFake(t, "printf '%s\\n' \"$1\" >> \"$0.calls\"\n[ \"$1\" = auth ] && exit \"$(cat \"$0.auth\")\"\ncat \"$0.json\"\n")
	set := func(auth, out string) {
		glabTestWrite(t, bin+".auth", auth)
		glabTestWrite(t, bin+".json", out)
	}
	calls := func() int {
		b, _ := os.ReadFile(bin + ".calls")
		return strings.Count(string(b), "repo\n")
	}
	dir := glabTestRepo(t, "git@ssh.example.com:group/repo.git")
	var g glabGovernor
	web := func() glabGate {
		t.Helper()
		gate, ok := glabOrigin(t.Context(), dir)
		if !ok {
			t.Fatal("no gate")
		}
		return g.withWeb(t.Context(), gate)
	}
	set("1", "")
	if gate := web(); gate.Web != "" || gate.webURL() != "https://ssh.example.com/group/repo" || calls() != 0 {
		t.Fatalf("signed out = %q %q, %d reads", gate.Web, gate.webURL(), calls())
	}
	set("0", `{"web_url":"https://gitlab.example.com:8443/gitlab/group/repo"}`)
	if gate := web(); gate.Web != "" {
		t.Fatalf("signed-out result not kept for the negative TTL: %q", gate.Web)
	}
	g.mu.Lock()
	g.hosts = nil
	for k, e := range g.webs {
		e.at = e.at.Add(-glabNegativeTTL)
		g.webs[k] = e
	}
	g.mu.Unlock()
	if gate := web(); gate.Web != "https://gitlab.example.com:8443/gitlab/group/repo" || calls() != 1 {
		t.Fatalf("signed in = %q, %d reads", gate.Web, calls())
	}
	set("0", `{"web_url":"https://changed.example.com/group/repo"}`)
	if gate := web(); gate.Web != "https://gitlab.example.com:8443/gitlab/group/repo" || calls() != 1 {
		t.Fatalf("cached = %q, %d reads", gate.Web, calls())
	}
	glabTestGit(t, dir, "remote", "set-url", "origin", "https://ssh.example.com/group/repo.git")
	if gate := web(); gate.Web != "https://changed.example.com/group/repo" || calls() != 2 {
		t.Fatalf("after the remote changed = %q, %d reads", gate.Web, calls())
	}
	glabTestGit(t, dir, "remote", "set-url", "origin", "https://ssh.example.com/group/repo")
	set("0", `{"web_url":"https://other.example.com/group/elsewhere"}`)
	if gate := web(); gate.Web != "" || gate.webURL() != "https://ssh.example.com/group/repo" || calls() != 3 {
		t.Fatalf("bad web_url = %q %q, %d reads", gate.Web, gate.webURL(), calls())
	}
}
