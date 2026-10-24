package web

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func TestGitLabFailureTheRegexesMissIsDecidedOnce(t *testing.T) {
	sha := strings.Repeat("c", 40)
	job := func(id, name, reason string) gitLabFixtureResponse {
		return gitLabFixtureResponse{body: `{"id":` + id + `,"name":"` + name + `","stage":"verify","status":"failed","failure_reason":"` + reason + `","pipeline":{"id":91,"project_id":42,"sha":"` + sha + `"}}`}
	}
	responses := gitLabItemResponses()
	responses["projects/42/jobs/1"] = job("1", "odd", "script_failure")
	responses["projects/42/jobs/1/trace"] = gitLabFixtureResponse{body: "EARLY-MARKER\n" + strings.Repeat("noise line\n", 2000) + "mypy: 3 problems in src/app.py\n"}
	responses["projects/42/jobs/5"] = job("5", "vague", "script_failure")
	responses["projects/42/jobs/5/trace"] = gitLabFixtureResponse{body: "something odd happened\n"}
	responses["projects/42/jobs/3"] = job("3", "build", "runner_system_failure")
	f := newGitLabItemsFixture(t, 0, responses)
	if _, err := f.m.UpdateSettings(SettingsPatch{TitleModel: map[string]string{"fake": "b"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.m.recordGitLabCommit(t.Context(), f.id, sha, "main"); err != nil {
		t.Fatal(err)
	}
	f.prov.SetUtilityHook(func(_ context.Context, req agentapi.UtilityRequest) (string, error) {
		if strings.Contains(req.Prompt, "Job: odd") {
			return `{"choice":"typecheck"}`, nil
		}
		return `{"choice":null}`, nil
	})
	at := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	f.m.now = func() time.Time { return at }
	refs := []string{"job:1", "job:5", "job:3"}
	items, err := f.m.gitLabItems(t.Context(), f.id, refs)
	if err != nil || items[0].FailureKind != "" || items[1].FailureKind != "" || items[2].FailureKind != "Runner failure" {
		t.Fatalf("first read = %+v, %v", items, err)
	}
	f.m.titles.Wait()
	reqs := f.prov.UtilityRequests()
	if len(reqs) != 2 {
		t.Fatalf("utility calls = %d, want one per job the patterns missed", len(reqs))
	}
	for _, req := range reqs {
		if req.Purpose != "decision-ci-failure-kind" || !strings.Contains(req.Prompt, `Stage: verify`) || strings.Contains(req.Prompt, "EARLY-MARKER") || len(req.Prompt) > 12<<10 {
			t.Fatalf("decision request = %q, %d prompt bytes", req.Purpose, len(req.Prompt))
		}
	}
	// The cached read shows the decision at once; an abstention shows nothing.
	items, err = f.m.gitLabItems(t.Context(), f.id, refs)
	if err != nil || items[0].FailureKind != "Type check failed" || items[0].FailureDetail != "" || items[1].FailureKind != "" {
		t.Fatalf("decided labels = %+v, %v", items[:2], err)
	}
	cfg, err := f.m.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	failures := cfg.WebGitLab[gitLabCommitKey("gitlab.example.com", "group/sub/repo", sha)].Failures
	if failures[1] != (store.WebGitLabFailure{Kind: "typecheck"}) || len(failures) != 3 {
		t.Fatalf("receipt failures = %+v", failures)
	}
	if _, found := failures[5]; !found {
		t.Fatal("the abstention was not kept, so the job would be asked about again")
	}
	// A restart asks nothing again.
	f.m.gitlab.items = nil
	at = at.Add(gitLabItemTTL)
	items, err = f.m.gitLabItems(t.Context(), f.id, refs)
	f.m.titles.Wait()
	if err != nil || items[0].FailureKind != "Type check failed" || len(f.prov.UtilityRequests()) != 2 {
		t.Fatalf("after restart = %+v, %v, %d calls", items[0], err, len(f.prov.UtilityRequests()))
	}
}

func TestGitLabFailureOfTraceTails(t *testing.T) {
	for _, tc := range []struct{ name, tail, kind, detail string }{
		{"go tests", "=== RUN   TestA\n--- FAIL: TestA (0.00s)\n    --- FAIL: TestA/sub (0.00s)\n--- FAIL: TestB (0.01s)\nFAIL\nFAIL\texample.com/pkg\t0.2s\n", "tests", "2 tests"},
		{"vitest", " Test Files  1 failed (1)\n      Tests  1 failed | 12 passed (13)\n", "tests", "1 test"},
		{"pytest", "==== 1 failed, 4 passed in 0.31s ====\n", "tests", "1 test"},
		{"go build", "# example.com/pkg\npkg/a.go:12:3: undefined: foo\nFAIL\texample.com/pkg [build failed]\n", "build", ""},
		{"go timeout", "panic: test timed out after 10m0s\nrunning tests:\n\tTestSlow (10m0s)\nFAIL\texample.com/pkg\t600.1s\n", "timeout", ""},
		{"runner timeout", "ERROR: Job failed: execution took longer than 1h0m0s seconds\n", "timeout", ""},
		{"oom", "Killed\nERROR: Job failed: exit code 137\n", "infra", ""},
		{"disk", "write /builds/x: no space left on device\n", "infra", ""},
		{"image", "Error response from daemon: pull access denied for private/image\n", "infra", ""},
		{"evicted", `ERROR: Job failed (system failure): pod "runner-7k2-build" was evicted`, "infra", ""},
		{"auth", "remote: HTTP Basic: Access denied\nfatal: Authentication failed for 'https://gitlab.example.com/x.git/'\n", "auth", ""},
		{"lint", "$ golangci-lint run ./...\npkg/a.go:3:1: exported func Foo should have comment (revive)\n", "lint", ""},
		{"typecheck", "src/a.ts(3,7): error TS2322: Type 'string' is not assignable to type 'number'.\n", "typecheck", ""},
		{"unknown", "something odd happened\nERROR: Job failed: exit code 1\n", "", ""},
	} {
		got := gitLabFailureOf(tc.tail)
		if got.Kind != tc.kind || got.Detail != tc.detail {
			t.Errorf("%s: %+v, want %q %q", tc.name, got, tc.kind, tc.detail)
		}
	}
}

func TestGitLabFailedJobIsLabelledOnceAndKeptInTheReceipt(t *testing.T) {
	sha := strings.Repeat("b", 40)
	responses := gitLabItemResponses()
	responses["projects/42/jobs/1"] = gitLabFixtureResponse{body: `{"id":1,"name":"test","stage":"test","status":"failed","failure_reason":"script_failure","pipeline":{"id":91,"project_id":42,"sha":"` + sha + `"}}`}
	// The secret stands in for a runner token a log can carry: it must never reach the label.
	responses["projects/42/jobs/1/trace"] = gitLabFixtureResponse{body: "token glrt-secret\n--- FAIL: TestA (0.00s)\n--- FAIL: TestB (0.00s)\nFAIL\texample.com/pkg\t0.1s\n"}
	responses["projects/42/jobs/3"] = gitLabFixtureResponse{body: `{"id":3,"name":"build","stage":"build","status":"failed","failure_reason":"runner_system_failure","pipeline":{"id":91,"project_id":42,"sha":"` + sha + `"}}`}
	f := newGitLabItemsFixture(t, 0, responses)
	if err := f.m.recordGitLabCommit(t.Context(), f.id, sha, "main"); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	f.m.now = func() time.Time { return at }
	read := func() []GitLabItemSummary {
		t.Helper()
		items, err := f.m.gitLabItems(t.Context(), f.id, []string{"job:1", "job:3", "pipeline:91"})
		if err != nil {
			t.Fatal(err)
		}
		return items
	}
	traces := func() int {
		n := 0
		for _, call := range f.calls(t) {
			if strings.HasSuffix(call, "/trace") {
				n++
			}
		}
		return n
	}
	check := func(items []GitLabItemSummary) {
		t.Helper()
		if items[0].FailureKind != "Tests failed" || items[0].FailureDetail != "2 tests" || items[1].FailureKind != "Runner failure" || items[1].FailureDetail != "not your change" {
			t.Fatalf("job labels = %+v", items[:2])
		}
		if items[2].FailureKind != "" || items[2].FailureDetail != "" {
			t.Fatalf("the pipeline chip was labelled: %+v", items[2])
		}
		for _, item := range items {
			if strings.Contains(item.FailureKind+item.FailureDetail, "secret") {
				t.Fatalf("log text reached the label: %+v", item)
			}
		}
	}
	check(read())
	if traces() != 1 {
		t.Fatalf("job logs read %d times, want once (the runner failure needs none)", traces())
	}
	cfg, err := f.m.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	failures := cfg.WebGitLab[gitLabCommitKey("gitlab.example.com", "group/sub/repo", sha)].Failures
	if failures[1] != (store.WebGitLabFailure{Kind: "tests", Detail: "2 tests"}) || failures[3] != (store.WebGitLabFailure{Kind: "infra"}) {
		t.Fatalf("receipt failures = %+v", failures)
	}
	// Later reads, and a restart that forgets every read, answer from the receipt.
	at = at.Add(gitLabItemTTL)
	check(read())
	f.m.gitlab.items = nil
	at = at.Add(gitLabItemTTL)
	check(read())
	if traces() != 1 {
		t.Fatalf("job log read again: %d reads", traces())
	}
}
