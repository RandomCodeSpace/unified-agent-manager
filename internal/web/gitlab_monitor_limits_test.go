package web

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func gitLabLimitsTask(t *testing.T, responses func(string) map[string]gitLabFixtureResponse) (*gitTask, glabGate, string) {
	t.Helper()
	repo := branchRepo(t)
	gitIn(t, repo, "remote", "add", "origin", "https://gitlab.example.com/group/sub/repo.git")
	sha := gitOutput(t, repo, "rev-parse", "HEAD")
	gate := gitLabClientFake(t, responses(sha))
	t.Setenv("PATH", filepath.Dir(gate.executable)+string(os.PathListSeparator)+os.Getenv("PATH"))
	g := newGitTask(t, repo)
	if err := g.m.recordGitLabCommit(t.Context(), g.id, sha, "main"); err != nil {
		t.Fatal(err)
	}
	return g, gate, sha
}

func gitLabLimitsCalls(t *testing.T, gate glabGate) int {
	t.Helper()
	calls, err := os.ReadFile(gate.executable + ".calls")
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(calls), "\n")
}

func TestGitLabMonitorHonorsRetryAfter(t *testing.T) {
	g, gate, _ := gitLabLimitsTask(t, func(string) map[string]gitLabFixtureResponse {
		return map[string]gitLabFixtureResponse{
			"projects/group%2Fsub%2Frepo": {status: http.StatusTooManyRequests, retryAfter: "600", body: `{"message":"slow down"}`},
		}
	})
	first, err := g.m.gitLabState(t.Context(), g.id, true)
	if err != nil || !strings.Contains(first.Error, "rate limiting") {
		t.Fatalf("first rate limit: %+v, %v", first, err)
	}
	if got := gitLabLimitsCalls(t, gate); got != 1 {
		t.Fatalf("requests=%d, want 1", got)
	}
	g.m.gitlab.work.Lock()
	until := g.m.gitlab.retryAt[gate.Host]
	g.m.gitlab.work.Unlock()
	if remaining := time.Until(until); remaining < 9*time.Minute || remaining > 10*time.Minute {
		t.Fatalf("Retry-After not respected: remaining=%v", remaining)
	}
	if _, err := g.m.gitLabState(t.Context(), g.id, true); err != nil {
		t.Fatal(err)
	}
	if got := gitLabLimitsCalls(t, gate); got != 1 {
		t.Fatalf("request repeated during backoff: %d", got)
	}
	// Another Task on the same host must share the server's backoff.
	otherRepo := branchRepo(t)
	gitIn(t, otherRepo, "remote", "add", "origin", "https://gitlab.example.com/group/another.git")
	other, err := g.m.Create(CreateRequest{Provider: g.prov.Name(), ProjectID: addProject(t, g.m, otherRepo), Name: "other task"})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.m.recordGitLabCommit(t.Context(), other.ID, gitOutput(t, otherRepo, "rev-parse", "HEAD"), "main"); err != nil {
		t.Fatal(err)
	}
	second, err := g.m.gitLabState(t.Context(), other.ID, true)
	if err != nil || !strings.Contains(second.Error, "rate limiting") || gitLabLimitsCalls(t, gate) != 1 {
		t.Fatalf("host backoff bypassed: %+v, %v", second, err)
	}
	g.m.gitlab.work.Lock()
	g.m.gitlab.retryAt[gate.Host] = time.Now().Add(-time.Second)
	g.m.gitlab.work.Unlock()
	if _, err := g.m.gitLabState(t.Context(), g.id, true); err != nil {
		t.Fatal(err)
	}
	if got := gitLabLimitsCalls(t, gate); got != 2 {
		t.Fatalf("request did not resume after backoff expired: %d", got)
	}
}

func TestGitLabMonitorBacksOffFailedAutomaticCreation(t *testing.T) {
	g, gate, sha := gitLabLimitsTask(t, func(sha string) map[string]gitLabFixtureResponse {
		responses := make(map[string]gitLabFixtureResponse)
		for endpoint, response := range gitLabClientResponses("main") {
			response.body = strings.ReplaceAll(response.body, gitLabFixtureSHA, sha)
			responses[strings.ReplaceAll(endpoint, gitLabFixtureSHA, sha)] = response
		}
		responses["projects/42/jobs/1/trace"] = gitLabFixtureResponse{body: "test fixture failed\n"}
		return responses
	})
	if w := g.ts.do(http.MethodPatch, "/api/settings", `{"gitlab_auto_investigate":true}`, withCookie(g.ts)); w.Code != http.StatusOK {
		t.Fatalf("enable Auto: %d %s", w.Code, w.Body)
	}
	g.prov.SetOpenError(errors.New("fixture provider unavailable"))
	before := len(g.prov.Opens())
	first, err := g.m.gitLabState(t.Context(), g.id, true)
	if err != nil || !strings.Contains(first.Error, "fixture provider unavailable") || len(g.prov.Opens()) != before+1 {
		t.Fatalf("expected failed creation: %+v, %v, opens=%d", first, err, len(g.prov.Opens()))
	}
	key := gitLabCommitKey(gate.Host, gate.Path, sha)
	g.m.gitlab.work.Lock()
	until := g.m.gitlab.retryAt[key]
	g.m.gitlab.work.Unlock()
	if remaining := time.Until(until); remaining < 4*time.Minute || remaining > 5*time.Minute {
		t.Fatalf("failed creation backoff=%v", remaining)
	}
	if _, err := g.m.gitLabState(t.Context(), g.id, true); err != nil {
		t.Fatal(err)
	}
	if got := len(g.prov.Opens()); got != before+1 {
		t.Fatalf("provider reopened during creation backoff: %d", got)
	}
	g.m.gitlab.work.Lock()
	g.m.gitlab.retryAt[key] = time.Now().Add(-time.Second)
	g.m.gitlab.work.Unlock()
	if _, err := g.m.gitLabState(t.Context(), g.id, true); err != nil {
		t.Fatal(err)
	}
	if got := len(g.prov.Opens()); got != before+2 {
		t.Fatalf("provider did not reopen after creation backoff expired: %d", got)
	}
	// Retried creation must keep the reserved event-to-task association.
	cfg, err := g.m.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if receipts := cfg.WebGitLab[key].Investigations; len(receipts) != 1 || receipts[gitLabEventKey(42, 91)].TaskID == "" || receipts[gitLabEventKey(42, 91)].Registered {
		t.Fatalf("wrong pending reservation: %+v", receipts)
	}
}
