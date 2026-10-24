package web

import (
	"context"
	"fmt"
	"regexp"
	"slices"

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
// the job log only for the rest.
func readGitLabJobFailure(ctx context.Context, gate glabGate, projectID int64, job GitLabJob) (store.WebGitLabFailure, error) {
	if job.FailureReason == "stuck_or_timeout_failure" {
		return store.WebGitLabFailure{Kind: "timeout"}, nil
	}
	if slices.Contains(gitLabInfraReasons, job.FailureReason) {
		return store.WebGitLabFailure{Kind: "infra"}, nil
	}
	trace, err := readGitLabJobTrace(ctx, gate, projectID, job.ID)
	if err != nil {
		return store.WebGitLabFailure{}, err
	}
	return gitLabFailureOf(trace.Text), nil
}

// gitLabJobFailure labels a failed job's summary. Each job is read once: the
// label comes from the last read, else the commit's receipt, else the job
// log, and a commit UAM tracks keeps it in its receipt across restarts. The
// caller holds gitlab.work.
func (m *Manager) gitLabJobFailure(ctx context.Context, gate glabGate, projectID int64, summary *GitLabItemSummary, last *store.WebGitLabFailure) {
	if last == nil {
		last = m.readGitLabJobFailureOnce(ctx, gate, projectID, summary.job)
	}
	if last == nil {
		return
	}
	summary.failure = last
	if summary.FailureKind = gitLabFailureLabels[last.Kind]; last.Kind == "infra" {
		summary.FailureDetail = "not your change"
	} else if summary.FailureKind != "" {
		summary.FailureDetail = last.Detail
	}
}

// readGitLabJobFailureOnce answers from the receipt of the job's commit, or
// reads the job and saves what it found there. nil while GitLab rate limits.
func (m *Manager) readGitLabJobFailureOnce(ctx context.Context, gate glabGate, projectID int64, job *gitLabProjectJob) *store.WebGitLabFailure {
	key := gitLabCommitKey(gate.Host, gate.Path, job.sha)
	cfg, err := m.store.Load()
	if err != nil {
		return nil
	}
	rec, tracked := cfg.WebGitLab[key]
	if f, found := rec.Failures[job.ID]; found {
		return &f
	}
	f, err := readGitLabJobFailure(ctx, gate, projectID, job.GitLabJob)
	m.gitLabRememberBackoff(gate.Host, err)
	if gitLabLimited(err) {
		return nil
	}
	// An unreadable log is not read again by this process; only a read one is saved.
	if err == nil && tracked {
		m.saveGitLabJobFailure(key, job.ID, f)
	}
	return &f
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
