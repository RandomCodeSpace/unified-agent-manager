package web

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// A failed job's chip names what failed ("Tests failed · 2 tests"), read
// from GitLab's failure reason or the end of its job log. Only the kind and
// a count leave this file: job logs can carry runner tokens.

// gitLabFailureKinds are tried in order after failed tests; the first match
// names the failure.
var gitLabFailureKinds = []struct {
	kind string
	re   *regexp.Regexp
}{
	{"infra", regexp.MustCompile(`(?i)exit code 137|no space left on device|pull access denied|job failed \(system failure\)`)},
	{"auth", regexp.MustCompile(`(?i)\b401 unauthorized|\b403 forbidden|authentication (?:failed|required)|HTTP Basic: Access denied|permission denied \(publickey\)|could not read username`)},
	{"timeout", regexp.MustCompile(`(?i)execution took longer than|job timed out|panic: test timed out`)},
	{"lint", regexp.MustCompile(`(?i)\b(?:golangci-lint|eslint|stylelint|ruff|flake8|pylint|rubocop|shellcheck|hadolint|markdownlint)\b`)},
	{"typecheck", regexp.MustCompile(`\berror TS\d+:`)},
	{"build", regexp.MustCompile(`(?i)build failed|compilation failed|could not compile|BUILD FAILURE|\berror\[E\d+\]|cannot find symbol|\.go:\d+:\d+: |npm ERR!|make: \*\*\* .*Error \d+`)},
}

var gitLabFailureLabels = map[string]string{
	"tests": "Tests failed", "build": "Build failed", "lint": "Lint failed", "typecheck": "Type check failed",
	"timeout": "Timed out", "infra": "Runner failure", "auth": "Auth failed",
}

// goTopFailRE counts failed Go tests, not their failed subtests.
var goTopFailRE = regexp.MustCompile(`(?m)^--- FAIL: `)

// gitLabFailedTests counts failed tests: Go's top-level --- FAIL lines, else
// the failed count of Jest, Vitest, pytest, cargo or TAP output. A Go
// package that failed to build or timed out counts none.
func gitLabFailedTests(tail string) int {
	if n := len(goTopFailRE.FindAllString(tail, -1)); n > 0 || goFailPkgRE.MatchString(tail) {
		return n
	}
	if c := countsOf(tail); c != nil {
		return c.failed
	}
	return 0
}

// gitLabFailureOf names what a failed job's log tail failed on; an empty
// Kind when nothing is recognised.
func gitLabFailureOf(tail string) store.WebGitLabFailure {
	if n := gitLabFailedTests(tail); n > 0 {
		return store.WebGitLabFailure{Kind: "tests", Detail: fmt.Sprintf("%d %s", n, plural(n, "test", "tests"))}
	}
	for _, k := range gitLabFailureKinds {
		if k.re.MatchString(tail) {
			return store.WebGitLabFailure{Kind: k.kind}
		}
	}
	return store.WebGitLabFailure{}
}

// readGitLabJobFailure trusts GitLab's own runner failure reasons and reads
// the job log only for the rest; tail is the log's end when it was read.
func readGitLabJobFailure(ctx context.Context, gate glabGate, projectID int64, job GitLabJob) (failure store.WebGitLabFailure, tail string, err error) {
	if job.FailureReason == "stuck_or_timeout_failure" {
		return store.WebGitLabFailure{Kind: "timeout"}, "", nil
	}
	if slices.Contains(gitLabInfraReasons, job.FailureReason) {
		return store.WebGitLabFailure{Kind: "infra"}, "", nil
	}
	trace, err := readGitLabJobTrace(ctx, gate, projectID, job.ID)
	if err != nil {
		return store.WebGitLabFailure{}, "", err
	}
	return gitLabFailureOf(trace.Text), trace.Text, nil
}

// gitLabJobFailure labels a failed job's summary. Each job is read once: the
// label comes from the last read, else the commit's receipt, else the job
// log, and a commit UAM tracks keeps it in its receipt across restarts. The
// caller holds gitlab.work.
func (m *Manager) gitLabJobFailure(ctx context.Context, gate glabGate, projectID int64, itemKey string, summary *GitLabItemSummary, last *store.WebGitLabFailure) {
	if last == nil {
		last = m.readGitLabJobFailureOnce(ctx, gate, projectID, itemKey, summary.job)
	}
	if last != nil {
		labelGitLabJob(summary, last)
	}
}

func labelGitLabJob(summary *GitLabItemSummary, f *store.WebGitLabFailure) {
	summary.failure, summary.FailureKind, summary.FailureDetail = f, gitLabFailureLabels[f.Kind], ""
	if f.Kind == "infra" {
		summary.FailureDetail = "not your change"
	} else if summary.FailureKind != "" {
		summary.FailureDetail = f.Detail
	}
}

// readGitLabJobFailureOnce answers from the receipt of the job's commit, or
// reads the job and saves what it found there. A log the patterns miss goes
// to the Utility model, in the background. nil while GitLab rate limits.
func (m *Manager) readGitLabJobFailureOnce(ctx context.Context, gate glabGate, projectID int64, itemKey string, job *gitLabProjectJob) *store.WebGitLabFailure {
	key := gitLabCommitKey(gate.Host, gate.Path, job.sha)
	cfg, err := m.store.Load()
	if err != nil {
		return nil
	}
	rec, tracked := cfg.WebGitLab[key]
	if f, found := rec.Failures[job.ID]; found {
		return &f
	}
	f, tail, err := readGitLabJobFailure(ctx, gate, projectID, job.GitLabJob)
	m.gitLabRememberBackoff(gate.Host, err)
	if gitLabLimited(err) {
		return nil
	}
	// An unreadable log is not read again by this process; only a read one is saved.
	switch {
	case err == nil && f.Kind == "" && strings.TrimSpace(tail) != "":
		m.startGitLabFailureDecision(itemKey, key, tracked, job.GitLabJob, tail)
	case err == nil && tracked:
		m.saveGitLabJobFailure(key, job.ID, f)
	}
	return &f
}

// gitLabFailureOptions are the kinds the Utility model picks from for a log
// the patterns miss; "other" gets no label.
var gitLabFailureOptions = []DecisionOption{
	{ID: "tests", Description: "Automated tests ran and some failed"},
	{ID: "build", Description: "Compiling, bundling or packaging failed"},
	{ID: "lint", Description: "A linter, formatter or static style check failed"},
	{ID: "typecheck", Description: "A type checker reported type errors"},
	{ID: "timeout", Description: "The job or one of its steps ran out of time"},
	{ID: "infra", Description: "The runner, network, container image, disk or memory failed, not the code"},
	{ID: "auth", Description: "Credentials, tokens or permissions were missing or refused"},
	{ID: "other", Description: "The log shows a failure that fits none of the other options"},
}

// startGitLabFailureDecision asks the Utility model what a job failed on,
// from its name, stage and the last 8 KB of its log, without holding up
// GitLab reads. Shutdown waits for it. The caller holds gitlab.work.
func (m *Manager) startGitLabFailureDecision(itemKey, commitKey string, tracked bool, job GitLabJob, tail string) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.titles.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.titles.Done()
		result, err := m.Decide(context.Background(), DecisionRequest{Purpose: "ci-failure-kind",
			Instructions: "Name what this failed GitLab CI job failed on, from its name, stage and the end of its log.",
			Input:        "Job: " + job.Name + "\nStage: " + job.Stage + "\nLog tail:\n" + gitLabLogTail(tail, 8<<10),
			Options:      gitLabFailureOptions})
		var f store.WebGitLabFailure
		if err == nil && !result.Abstained {
			f.Kind = result.Choice
		}
		// A failed call is not saved: the next process asks again.
		if err == nil && tracked {
			m.saveGitLabJobFailure(commitKey, job.ID, f)
		}
		m.gitlab.work.Lock()
		defer m.gitlab.work.Unlock()
		if cached, ok := m.gitlab.items[itemKey]; ok && cached.summary.job != nil && cached.summary.job.ID == job.ID {
			labelGitLabJob(&cached.summary, &f)
			m.gitlab.items[itemKey] = cached
		}
	}()
}

func (m *Manager) saveGitLabJobFailure(key string, jobID int64, f store.WebGitLabFailure) {
	if err := m.store.Update(func(cfg *store.Config) error {
		rec, ok := cfg.WebGitLab[key]
		if !ok {
			return nil
		}
		if rec.Failures == nil {
			rec.Failures = make(map[int64]store.WebGitLabFailure)
		}
		rec.Failures[jobID] = f
		cfg.WebGitLab[key] = rec
		return nil
	}); err != nil {
		log.Warn("gitlab failure kind not saved", "job", jobID, "error", err)
	}
}
