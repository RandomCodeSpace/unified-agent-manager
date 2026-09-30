package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/board"
)

// evidenceFiles indexes an evidence diff's files by path.
func evidenceFiles(ev Evidence) map[string]EvidenceFile {
	out := map[string]EvidenceFile{}
	for _, f := range ev.Diff.Files {
		out[f.Path] = f
	}
	return out
}

func TestEvidenceBaselineCommitsDirtyAndOverlap(t *testing.T) {
	ctx := context.Background()
	repo := branchRepo(t)
	writeRepoFile(t, repo, "pkg/a.go", "one\ntwo\n")
	writeRepoFile(t, repo, "keep.txt", "same\n")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "base")
	writeRepoFile(t, repo, "pre.txt", "dirty before the hold\n")

	base, err := baseline(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if head := gitOutput(t, repo, "rev-parse", "HEAD"); base.Head != head || !slices.Equal(base.Dirty, []string{"pre.txt"}) {
		t.Fatalf("baseline = %+v, want HEAD %s with pre.txt dirty", base, head)
	}

	writeRepoFile(t, repo, "pkg/a.go", "one\nthree\nfour\n")
	gitIn(t, repo, "commit", "-q", "-am", "feat: \x1b[31mred\x1b[0m change")
	writeRepoFile(t, repo, "pkg/a_test.go", "test\n")
	writeRepoFile(t, repo, "pkg/new.go", "a\nb\nc\n")
	writeRepoFile(t, repo, "pre.txt", "still dirty\n")
	touched := []string{"pkg/a.go", filepath.Join(repo, "pkg", "new.go"), "../outside.go"}
	others := []heldFiles{
		{Card: 13, TaskID: "task-b", Files: []string{"pkg/a_test.go"}},
		{Card: 9, TaskID: "task-c", Files: []string{filepath.Join(repo, "pkg", "a_test.go"), "keep.txt"}},
	}
	ev, err := collectEvidence(ctx, repo, base, touched, others)
	if err != nil {
		t.Fatal(err)
	}
	files := evidenceFiles(ev)
	want := map[string]EvidenceFile{
		"pkg/a.go":      {Path: "pkg/a.go", Added: 2, Deleted: 1, ByTask: true},
		"pkg/new.go":    {Path: "pkg/new.go", Added: 3, ByTask: true},
		"pkg/a_test.go": {Path: "pkg/a_test.go", Added: 1, Overlap: &EvidenceOverlap{Card: 9, TaskID: "task-c"}},
		"pre.txt":       {Path: "pre.txt", Added: 1, PreDirty: true},
	}
	if len(files) != len(want) {
		t.Fatalf("diff files = %+v, want %v", ev.Diff.Files, want)
	}
	for p, w := range want {
		got := files[p]
		if got.Added != w.Added || got.Deleted != w.Deleted || got.ByTask != w.ByTask || got.PreDirty != w.PreDirty ||
			(got.Overlap == nil) != (w.Overlap == nil) || (got.Overlap != nil && *got.Overlap != *w.Overlap) {
			t.Fatalf("%s = %+v (overlap %+v), want %+v", p, got, got.Overlap, w)
		}
	}
	if ev.Diff.Added != 7 || ev.Diff.Deleted != 1 {
		t.Fatalf("diff totals = +%d -%d, want +7 -1", ev.Diff.Added, ev.Diff.Deleted)
	}
	if len(ev.Commits) != 1 || ev.Commits[0].SHA != gitOutput(t, repo, "rev-parse", "HEAD") || ev.Commits[0].Subject != "feat: red change" {
		t.Fatalf("commits = %+v", ev.Commits)
	}
	if ev.Baseline.Head != base.Head || !slices.Equal(ev.Baseline.Dirty, base.Dirty) {
		t.Fatalf("evidence baseline = %+v", ev.Baseline)
	}
}

func TestEvidenceFromBeforeTheFirstCommit(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	gitIn(t, repo, "init", "-q")
	base, err := baseline(ctx, repo)
	if err != nil || base.Head != "" || len(base.Dirty) != 0 {
		t.Fatalf("baseline = %+v, %v", base, err)
	}
	writeRepoFile(t, repo, "go.mod", "module x\n")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "first")
	ev, err := collectEvidence(ctx, repo, base, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ev.Diff.Files) != 1 || ev.Diff.Files[0].Path != "go.mod" || ev.Diff.Added != 1 || len(ev.Commits) != 1 {
		t.Fatalf("evidence = %+v", ev)
	}
	data, err := json.Marshal(ev)
	if err != nil || !strings.Contains(string(data), `"dirty":[]`) {
		t.Fatalf("evidence JSON = %s, %v", data, err)
	}
}

func TestEvidenceRefusesOutsideGitAndBadBaselines(t *testing.T) {
	ctx := context.Background()
	if _, err := baseline(ctx, t.TempDir()); err == nil {
		t.Fatal("a baseline outside git succeeded")
	}
	repo := branchRepo(t)
	if _, err := collectEvidence(ctx, repo, board.Baseline{Head: "--output=/tmp/x"}, nil, nil); err == nil {
		t.Fatal("an option as the baseline HEAD was accepted")
	}
}

// A baseline commit that no longer exists still gives evidence: the touched
// files against HEAD, no commits, flagged.
func TestEvidenceWithTheBaselineCommitGone(t *testing.T) {
	ctx := context.Background()
	repo := branchRepo(t)
	writeRepoFile(t, repo, "t.go", "one\n")
	writeRepoFile(t, repo, "other.go", "one\n")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "base")
	writeRepoFile(t, repo, "t.go", "one\ntwo\n")
	writeRepoFile(t, repo, "other.go", "one\ntwo\n")
	writeRepoFile(t, repo, "new.go", "x\n")
	gone := board.Baseline{Head: strings.Repeat("ab", 20)}
	ev, err := collectEvidence(ctx, repo, gone, []string{"t.go", "new.go"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	files := evidenceFiles(ev)
	if len(files) != 2 || files["t.go"].Added != 1 || files["new.go"].Added != 1 || len(ev.Commits) != 0 || !ev.baselineMissing {
		t.Fatalf("evidence = %+v", ev)
	}
	if flags := evidenceFlags(ev, "", false); !slices.Equal(flags, []string{board.FlagBaselineMissing}) {
		t.Fatalf("flags = %q", flags)
	}
	ev, err = collectEvidence(ctx, repo, gone, nil, nil)
	if err != nil || len(ev.Diff.Files) != 0 || !slices.Equal(evidenceFlags(ev, "", false), []string{board.FlagBaselineMissing}) {
		t.Fatalf("with nothing touched: %+v, %v", ev, err)
	}
}

// Paths dirty when the hold started count only when their content changed
// since, so an earlier attempt's leftovers are not this attempt's work.
func TestEvidencePreDirtyPaths(t *testing.T) {
	ctx := context.Background()
	repo := branchRepo(t)
	for _, name := range []string{"kept.txt", "changed.txt", "reverted.txt"} {
		writeRepoFile(t, repo, name, "base\n")
	}
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "base")
	for _, name := range []string{"kept.txt", "changed.txt", "reverted.txt"} {
		writeRepoFile(t, repo, name, "base\nleftover\n")
	}
	for _, name := range []string{"left.txt", "rewritten.txt", "removed.txt"} {
		writeRepoFile(t, repo, name, "untracked\n")
	}
	base, err := baseline(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(base.Dirty) != 6 || len(base.Blobs) != 6 || base.Blobs["kept.txt"] == "" {
		t.Fatalf("baseline = %+v", base)
	}
	// Only the baseline survives the store, so read it back as the store does.
	stored := func(b board.Baseline) board.Baseline {
		t.Helper()
		data, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		var out board.Baseline
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	ev, err := collectEvidence(ctx, repo, stored(base), nil, nil)
	if err != nil || len(ev.Diff.Files) != 0 || !slices.Equal(evidenceFlags(ev, "", false), []string{board.FlagNoChangeInTree}) {
		t.Fatalf("untouched leftovers = %+v, %v", ev.Diff, err)
	}

	writeRepoFile(t, repo, "changed.txt", "base\nleftover\nmore\n")
	writeRepoFile(t, repo, "reverted.txt", "base\n")
	writeRepoFile(t, repo, "rewritten.txt", "untracked\nagain\n")
	if err := os.Remove(filepath.Join(repo, "removed.txt")); err != nil {
		t.Fatal(err)
	}
	ev, err = collectEvidence(ctx, repo, stored(base), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	files := evidenceFiles(ev)
	want := map[string]int{"changed.txt": 2, "reverted.txt": 0, "rewritten.txt": 2, "removed.txt": 0}
	if len(files) != len(want) {
		t.Fatalf("files = %+v, want %v", ev.Diff.Files, want)
	}
	for p, added := range want {
		if f, ok := files[p]; !ok || !f.PreDirty || f.Added != added {
			t.Fatalf("%s = %+v, want pre-dirty with +%d", p, f, added)
		}
	}
	if evidenceFlags(ev, "", false) != nil {
		t.Fatalf("flags = %q", evidenceFlags(ev, "", false))
	}

	// A baseline stored without blob names cannot prove a path unchanged.
	old := stored(base)
	old.Blobs = nil
	ev, err = collectEvidence(ctx, repo, old, nil, nil)
	if f, ok := evidenceFiles(ev)["kept.txt"]; err != nil || !ok || !f.PreDirty {
		t.Fatalf("an old baseline hid kept.txt: %+v, %v", ev.Diff.Files, err)
	}
}

// Every dirty path is recorded, not only the listed ones.
func TestEvidencePreDirtyBeyondTheListCap(t *testing.T) {
	ctx := context.Background()
	repo := branchRepo(t)
	for i := range maxChangedFiles + 5 {
		writeRepoFile(t, repo, fmt.Sprintf("u%04d.txt", i), "x\n")
	}
	base, err := baseline(ctx, repo)
	if err != nil || len(base.Dirty) != maxChangedFiles+5 || len(base.Blobs) != maxChangedFiles+5 {
		t.Fatalf("baseline has %d paths and %d blobs, %v", len(base.Dirty), len(base.Blobs), err)
	}
	last := fmt.Sprintf("u%04d.txt", maxChangedFiles+4)
	writeRepoFile(t, repo, last, "x\ny\n")
	ev, err := collectEvidence(ctx, repo, base, nil, nil)
	if err != nil || len(ev.Diff.Files) != 1 || ev.Diff.Files[0].Path != last || !ev.Diff.Files[0].PreDirty {
		t.Fatalf("files = %+v, %v", ev.Diff.Files, err)
	}
	if len(ev.Baseline.Dirty) != maxChangedFiles || ev.Baseline.Blobs != nil {
		t.Fatalf("the evidence baseline lists %d paths, blobs %v", len(ev.Baseline.Dirty), ev.Baseline.Blobs != nil)
	}
}

// A file name that is not UTF-8 does not survive the store's JSON; it must
// then count as changed, never be hidden.
func TestEvidenceNonUTF8PreDirtyPathIsNeverHidden(t *testing.T) {
	ctx := context.Background()
	repo := branchRepo(t)
	name := "bad\xff.txt"
	if err := os.WriteFile(filepath.Join(repo, name), []byte("x\n"), 0o644); err != nil {
		t.Skipf("the file system refuses the name: %v", err)
	}
	base, err := baseline(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	var stored board.Baseline
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	ev, err := collectEvidence(ctx, repo, stored, nil, nil)
	if _, ok := evidenceFiles(ev)[name]; err != nil || !ok {
		t.Fatalf("the unchanged file was hidden: %+v, %v", ev.Diff.Files, err)
	}
}

// The flags see every changed file, not only the listed ones.
func TestEvidenceFlagsCountFilesBeyondTheList(t *testing.T) {
	ctx := context.Background()
	repo := branchRepo(t)
	base, err := baseline(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	for i := range maxChangedFiles {
		writeRepoFile(t, repo, fmt.Sprintf("a%04d.txt", i), "x\n")
	}
	writeRepoFile(t, repo, "zz_test.go", "package zz\n")
	ev, err := collectEvidence(ctx, repo, base, nil, []heldFiles{{Card: 3, TaskID: "task-2", Files: []string{"zz_test.go"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(ev.Diff.Files) != maxChangedFiles || slices.ContainsFunc(ev.Diff.Files, func(f EvidenceFile) bool { return f.Path == "zz_test.go" }) ||
		ev.Diff.Added != maxChangedFiles+1 {
		t.Fatalf("listed %d files, +%d", len(ev.Diff.Files), ev.Diff.Added)
	}
	if flags := evidenceFlags(ev, "make", false); !slices.Equal(flags, []string{board.FlagTestsOrBuildChanged, board.FlagOverlap}) {
		t.Fatalf("flags = %q", flags)
	}
}

// A path removed from the index is both deleted and untracked; it is listed
// once.
func TestEvidenceListsAnUntrackedRemovalOnce(t *testing.T) {
	ctx := context.Background()
	repo := branchRepo(t)
	writeRepoFile(t, repo, "f.txt", "one\ntwo\n")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "base")
	base, err := baseline(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "rm", "-q", "--cached", "f.txt")
	ev, err := collectEvidence(ctx, repo, base, nil, nil)
	if err != nil || len(ev.Diff.Files) != 1 || ev.Diff.Files[0] != (EvidenceFile{Path: "f.txt", Deleted: 2}) {
		t.Fatalf("files = %+v, %v", ev.Diff.Files, err)
	}
}

// A Task's tools may reach the work tree through a symbolic link.
func TestEvidencePathsThroughSymlink(t *testing.T) {
	repo := branchRepo(t)
	writeRepoFile(t, repo, "sub/x.go", "x\n")
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(filepath.Join(repo, "sub"), link); err != nil {
		t.Fatal(err)
	}
	resolve := repoPaths(repo, link)
	for p, want := range map[string]string{
		"x.go":                          "sub/x.go",
		filepath.Join(link, "x.go"):     "sub/x.go",
		filepath.Join(repo, "top.go"):   "top.go",
		filepath.Join(repo, "sub", "y"): "sub/y",
		"../escape.go":                  "",
		filepath.Join(t.TempDir(), "o"): "",
		link:                            "",
	} {
		if got, ok := resolve(p); got != want || ok != (want != "") {
			t.Fatalf("resolve(%q) = %q, %v, want %q", p, got, ok, want)
		}
	}
}

func TestTestOrBuildPath(t *testing.T) {
	for p, want := range map[string]bool{
		"internal/web/x_test.go": true, "web/src/a.test.ts": true, "test/fixture.txt": true,
		"pkg/tests/data.json": true, "src/__tests__/a.js": true, "Makefile": true, "web/package.json": true,
		"go.mod": true, "go.sum": true, "web/package-lock.json": true, ".github/workflows/ci.yml": true,
		"main.go": false, "docs/testing.md": false, "latest/x.go": false, "sub/.github/x": false, "contest.go": false,
	} {
		if got := testOrBuildPath(p); got != want {
			t.Fatalf("testOrBuildPath(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestTouchedFilesFromEditTools(t *testing.T) {
	tool := func(name string, status agentapi.ToolStatus, input string) agentapi.Item {
		return agentapi.Item{Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: name, Status: status, Input: input}}
	}
	items := []agentapi.Item{
		{Kind: agentapi.ItemAssistant, Text: "hi"},
		tool("view", agentapi.ToolCompleted, `{"path":"viewed.go"}`),
		tool("edit", agentapi.ToolCompleted, `{"path":"a.go"}`),
		tool("create", agentapi.ToolRunning, `{"path":"/abs/b.go"}`),
		tool("edit", agentapi.ToolFailed, `{"path":"failed.go"}`),
		tool("apply_patch", agentapi.ToolCompleted, "*** Begin Patch\n*** Add File: c.go\n+x\n*** Update File: a.go\n@@\n-x\n+y\n*** End Patch"),
		tool("bash", agentapi.ToolCompleted, `{"path":"shell.go"}`),
	}
	if got, want := touchedFiles(items), []string{"a.go", "/abs/b.go", "c.go"}; !slices.Equal(got, want) {
		t.Fatalf("touchedFiles = %q, want %q", got, want)
	}
}

// acceptEnv runs acceptance commands with /bin/sh and an empty home, so no
// login profile of the machine running the tests slows or changes them.
func acceptEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ENV", "")
	t.Setenv("SHELL", "/bin/sh")
}

func TestAcceptRunnerExitCodes(t *testing.T) {
	acceptEnv(t)
	ctx := context.Background()
	repo := branchRepo(t)
	var r acceptRunners
	for _, tc := range []struct {
		cmd  string
		exit int
		tail string
	}{
		{"echo ok; echo err >&2", 0, "ok\nerr\n"},
		{"echo failing; exit 3", 3, "failing\n"},
		{"uam-no-such-command-for-tests", 127, "not found"},
	} {
		res, err := r.run(ctx, repo, tc.cmd)
		if err != nil {
			t.Fatalf("%q: %v", tc.cmd, err)
		}
		if res.Exit != tc.exit || !strings.Contains(res.Tail, tc.tail) || res.Cmd != tc.cmd || res.CmdHash != commandHash(tc.cmd) ||
			res.Head != gitOutput(t, repo, "rev-parse", "HEAD") || res.Dirty || res.RanAt.IsZero() {
			t.Fatalf("%q: result = %+v", tc.cmd, res)
		}
	}
	writeRepoFile(t, repo, "dirty.txt", "x\n")
	if res, err := r.run(ctx, repo, "true"); err != nil || !res.Dirty {
		t.Fatalf("dirty run = %+v, %v", res, err)
	}
	if _, err := r.run(ctx, repo, ""); board.CodeOf(err) != board.CodeInvalid {
		t.Fatalf("an empty command ran: %v", err)
	}
	if len(commandHash("x")) != 64 || commandHash("x") == commandHash("y") {
		t.Fatal("command hashes are not sha256 hex")
	}
}

func TestAcceptRunnerShellFallbackAndStartFailure(t *testing.T) {
	acceptEnv(t)
	ctx := context.Background()
	repo := branchRepo(t)
	var r acceptRunners
	for _, shell := range []string{"", "sh", "/no/such/shell"} {
		t.Setenv("SHELL", shell)
		res, err := r.run(ctx, repo, `echo "shell:$0"`)
		if err != nil || res.Exit != 0 || !regexp.MustCompile(`shell:/\S*(bash|sh)\n`).MatchString(res.Tail) {
			t.Fatalf("SHELL=%q: %+v, %v", shell, res, err)
		}
	}
	res, err := r.run(ctx, filepath.Join(repo, "missing"), "true")
	if !errors.Is(err, errAcceptanceNotRun) || res.Exit != -1 || !strings.Contains(res.Tail, "did not start") || res.CmdHash == "" {
		t.Fatalf("a missing directory ran: %+v, %v", res, err)
	}
}

// endsAfterFirstCheck is a context that reads as live on its first Err and
// as cancelled after, so it ends between run's first check and the start.
type endsAfterFirstCheck struct {
	context.Context
	checks atomic.Int32
}

func (c *endsAfterFirstCheck) Err() error {
	if c.checks.Add(1) > 1 {
		return context.Canceled
	}
	return nil
}

// A start that fails because the caller's context ended reports the
// context, not "could not run", so no flagged claim is filed.
func TestAcceptRunnerStartFailureAfterCancel(t *testing.T) {
	acceptEnv(t)
	var r acceptRunners
	ctx := &endsAfterFirstCheck{Context: context.Background()}
	res, err := r.run(ctx, filepath.Join(t.TempDir(), "missing"), "true")
	if !errors.Is(err, context.Canceled) || errors.Is(err, errAcceptanceNotRun) || res != (AcceptResult{}) {
		t.Fatalf("run = %+v, %v", res, err)
	}
}

// pidFrom waits for a command to write its background child's PID to file.
func pidFrom(t *testing.T, file string) int {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		data, err := os.ReadFile(file)
		if pid, perr := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && perr == nil && pid > 0 {
			return pid
		}
		if time.Now().After(deadline) {
			t.Fatalf("no PID in %s", file)
		}
	}
}

func TestAcceptRunnerTimeoutKillsProcessGroup(t *testing.T) {
	acceptEnv(t)
	repo := branchRepo(t)
	pidFile := filepath.Join(t.TempDir(), "pid")
	r := acceptRunners{timeout: time.Second}
	started := time.Now()
	res, err := r.run(context.Background(), repo, fmt.Sprintf("sleep 30 & echo $! > %q; wait", pidFile))
	if err != nil {
		t.Fatal(err)
	}
	if res.Exit != 137 || !strings.Contains(res.Tail, "did not finish within 1s") || time.Since(started) > 10*time.Second {
		t.Fatalf("timed-out run = %+v after %s", res, time.Since(started))
	}
	waitGone(t, pidFrom(t, pidFile))
}

func TestAcceptRunnerKillsLeftoversOfAFinishedCommand(t *testing.T) {
	acceptEnv(t)
	repo := branchRepo(t)
	pidFile := filepath.Join(t.TempDir(), "pid")
	var r acceptRunners
	res, err := r.run(context.Background(), repo, fmt.Sprintf("sleep 30 >/dev/null 2>&1 & echo $! > %q", pidFile))
	if err != nil || res.Exit != 0 {
		t.Fatalf("run = %+v, %v", res, err)
	}
	waitGone(t, pidFrom(t, pidFile))
}

func TestAcceptRunnerCancelDiscardsTheRun(t *testing.T) {
	acceptEnv(t)
	repo := branchRepo(t)
	pidFile := filepath.Join(t.TempDir(), "pid")
	var r acceptRunners
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	var res AcceptResult
	go func() {
		var err error
		res, err = r.run(ctx, repo, fmt.Sprintf("sleep 30 & echo $! > %q; wait", pidFile))
		done <- err
	}()
	pid := pidFrom(t, pidFile)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) || res != (AcceptResult{}) {
		t.Fatalf("cancelled run = %+v, %v", res, err)
	}
	waitGone(t, pid)
	if _, err := r.run(ctx, repo, "true"); !errors.Is(err, context.Canceled) {
		t.Fatalf("a run on a cancelled context = %v", err)
	}
}

func TestAcceptRunnerOneAtATimePerDirectory(t *testing.T) {
	acceptEnv(t)
	repo := branchRepo(t)
	lock := filepath.Join(t.TempDir(), "lock")
	var r acceptRunners
	var wg sync.WaitGroup
	results := make([]AcceptResult, 3)
	for i := range results {
		wg.Go(func() {
			res, err := r.run(context.Background(), repo, fmt.Sprintf("mkdir %q || exit 9; sleep 0.2; rmdir %q", lock, lock))
			if err != nil {
				t.Error(err)
			}
			results[i] = res
		})
	}
	wg.Wait()
	for _, res := range results {
		if res.Exit != 0 {
			t.Fatalf("runs overlapped: %+v", results)
		}
	}
}

func TestAcceptRunnerBusy(t *testing.T) {
	acceptEnv(t)
	repo := branchRepo(t)
	r := acceptRunners{timeout: 200 * time.Millisecond}
	slot := r.slot(filepath.Clean(repo))
	slot <- struct{}{} // an earlier run holds the directory
	started := time.Now()
	if _, err := r.run(context.Background(), repo, "true"); board.CodeOf(err) != board.CodeAcceptanceBusy || err.Error() != "acceptance busy, retry" {
		t.Fatalf("a run past the wait = %v", err)
	}
	if time.Since(started) < 200*time.Millisecond {
		t.Fatal("the busy refusal did not wait for the timeout")
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	if _, err := r.run(ctx, repo, "true"); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled wait = %v", err)
	}
	<-slot
	if res, err := r.run(context.Background(), repo, "true"); err != nil || res.Exit != 0 {
		t.Fatalf("a run after the release = %+v, %v", res, err)
	}
	other := t.TempDir()
	slot <- struct{}{}
	if res, err := r.run(context.Background(), other, "true"); err != nil || res.Exit != 0 {
		t.Fatalf("another directory waited: %+v, %v", res, err)
	}
}

func TestAcceptRunnerTailKeepsTheLast64KiB(t *testing.T) {
	acceptEnv(t)
	repo := branchRepo(t)
	var r acceptRunners
	res, err := r.run(context.Background(), repo, `i=0; while [ $i -lt 3000 ]; do echo "line $i ................................................"; i=$((i+1)); done`)
	if err != nil || res.Exit != 0 {
		t.Fatalf("run = %v, %v", res.Exit, err)
	}
	if len(res.Tail) > acceptTailBytes || len(res.Tail) < acceptTailBytes-100 || !strings.HasSuffix(res.Tail, "line 2999 ................................................\n") ||
		strings.Contains(res.Tail, "line 0 ") {
		t.Fatalf("tail is %d bytes: %.80q…", len(res.Tail), res.Tail)
	}
}

func TestTailBuffer(t *testing.T) {
	b := &tailBuffer{limit: 8}
	for _, w := range []string{"abc", "defgh", "ij"} {
		if n, err := b.Write([]byte(w)); n != len(w) || err != nil {
			t.Fatalf("write = %d, %v", n, err)
		}
	}
	if b.String() != "cdefghij" {
		t.Fatalf("tail = %q", b.String())
	}
	_, _ = b.Write([]byte("0123456789"))
	if b.String() != "23456789" {
		t.Fatalf("tail after a long write = %q", b.String())
	}
	b = &tailBuffer{limit: 4}
	_, _ = b.Write([]byte("xé€"))
	if b.String() != "€" {
		t.Fatalf("a cut character was kept: %q", b.String())
	}
}

// fakeClaimStore records the store calls a claim makes.
type fakeClaimStore struct {
	calls  *[]string
	fin    board.Finishable
	finErr error
	holds  []board.Hold
}

func (s fakeClaimStore) CheckFinishable(_ context.Context, a board.Actor, ref string) (board.Finishable, error) {
	*s.calls = append(*s.calls, "check "+a.TaskID+" "+ref)
	return s.fin, s.finErr
}

func (s fakeClaimStore) Detail(_ context.Context, ref string) (board.Detail, error) {
	*s.calls = append(*s.calls, "detail "+ref)
	return board.Detail{Card: s.fin.Card, Holds: s.holds}, nil
}

// fakeRunner records runs, and answers with res and err or, when real is
// set, runs the command.
type fakeRunner struct {
	calls *[]string
	res   AcceptResult
	err   error
	real  *acceptRunners
}

func (r fakeRunner) run(ctx context.Context, dir, cmd string) (AcceptResult, error) {
	*r.calls = append(*r.calls, "run "+cmd)
	if r.real != nil {
		return r.real.run(ctx, dir, cmd)
	}
	return r.res, r.err
}

// claimScene is a repository with a subtask held by task-1 since its
// baseline.
func claimScene(t *testing.T, cmd string) (string, *[]string, fakeClaimStore) {
	t.Helper()
	repo := branchRepo(t)
	base, err := baseline(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	calls := &[]string{}
	ended := time.Now()
	st := fakeClaimStore{
		calls: calls,
		fin: board.Finishable{AcceptCmd: cmd, Card: board.Card{ID: "card-1", Seq: 12, Kind: board.KindSubtask,
			Checklist: []board.Check{{Text: "a", Done: true}, {Text: "b", Done: true}}}},
		holds: []board.Hold{
			{TaskID: "task-1", Baseline: board.Baseline{Head: "0000"}, EndedAt: &ended},
			{TaskID: "task-2", Baseline: board.Baseline{Head: "1111"}},
			{TaskID: "task-1", Baseline: base},
		},
	}
	return repo, calls, st
}

func TestClaimRefusalsStopBeforeTheRun(t *testing.T) {
	ctx := context.Background()
	repo, calls, st := claimScene(t, "touch ran")
	st.finErr = &board.Error{Code: board.CodeGuardOpenItems, Message: "open items"}
	runner := fakeRunner{calls: calls}
	in := claimInput{Actor: board.Agent("task-1", ""), Ref: "#12", Dir: repo}
	if _, err := evaluateClaim(ctx, st, runner, in); board.CodeOf(err) != board.CodeGuardOpenItems {
		t.Fatalf("guard refusal = %v", err)
	}
	if !slices.Equal(*calls, []string{"check task-1 #12"}) {
		t.Fatalf("calls = %q", *calls)
	}

	*calls, st.finErr = nil, nil
	in.Actor = board.Agent("task-3", "")
	if _, err := evaluateClaim(ctx, st, runner, in); board.CodeOf(err) != board.CodeNotHeld {
		t.Fatalf("a claim without an open hold = %v", err)
	}
	if !slices.Equal(*calls, []string{"check task-3 #12", "detail card-1"}) {
		t.Fatalf("calls = %q", *calls)
	}

	in.Actor = board.Agent("task-1", "")
	for _, exit := range []int{1, 127} {
		*calls = nil
		runner.res = AcceptResult{Cmd: "touch ran", Exit: exit, Tail: "boom"}
		in.OtherHolds = func(context.Context, board.Card) ([]heldFiles, error) {
			*calls = append(*calls, "holds")
			return nil, nil
		}
		res, err := evaluateClaim(ctx, st, runner, in)
		if board.CodeOf(err) != board.CodeAcceptanceFailed || !strings.Contains(err.Error(), "exited "+strconv.Itoa(exit)) ||
			!strings.Contains(err.Error(), "boom") || !strings.Contains(err.Error(), "red by design") {
			t.Fatalf("exit %d: %v", exit, err)
		}
		if res.Evidence.Accept == nil || res.Evidence.Accept.Exit != exit || res.Evidence.Diff.Files == nil || res.Request.Kind != "" {
			t.Fatalf("exit %d: result = %+v", exit, res)
		}
		if !slices.Equal(*calls, []string{"check task-1 #12", "detail card-1", "holds", "run touch ran"}) {
			t.Fatalf("exit %d: calls = %q", exit, *calls)
		}
	}

	runner.err = errAcceptanceBusy
	if _, err := evaluateClaim(ctx, st, runner, in); board.CodeOf(err) != board.CodeAcceptanceBusy {
		t.Fatalf("busy = %v", err)
	}
	runner.err = context.Canceled
	if _, err := evaluateClaim(ctx, st, runner, in); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled = %v", err)
	}
}

func TestClaimWithNoCommandAndNoChange(t *testing.T) {
	repo, calls, st := claimScene(t, "")
	res, err := evaluateClaim(context.Background(), st, fakeRunner{calls: calls}, claimInput{
		Actor: board.Agent("task-1", "a1"), Ref: "card-1", Dir: repo, Comment: "done", ProposedAcceptCmd: "make test",
		Transcript: &EvidenceTranscript{TaskID: "task-1", FromItem: "i1", ToItem: "i9"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(*calls, []string{"check task-1 card-1", "detail card-1"}) {
		t.Fatalf("calls = %q", *calls)
	}
	in := res.Request
	if in.Kind != board.RequestDone || in.Comment != "done" || in.ProposedAcceptCmd != "make test" || in.PassedCmd != "" || !slices.Equal(in.Flags, []string{board.FlagNoChangeInTree}) {
		t.Fatalf("request = %+v", in)
	}
	var ev Evidence
	if err := json.Unmarshal(in.Evidence, &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Accept != nil || ev.Checklist != (EvidenceChecklist{Done: 2, Total: 2}) || ev.Transcript == nil || ev.Transcript.ToItem != "i9" ||
		ev.Baseline.Head != st.holds[2].Baseline.Head {
		t.Fatalf("evidence = %+v", ev)
	}
}

// The evidence is gathered before the run: files the acceptance command
// writes, such as a lockfile, are not the Task's work.
func TestClaimCollectsEvidenceBeforeTheRun(t *testing.T) {
	acceptEnv(t)
	repo, calls, st := claimScene(t, "echo generated > go.sum")
	writeRepoFile(t, repo, "x.go", "package x\n")
	runner := fakeRunner{calls: calls, real: &acceptRunners{}}
	res, err := evaluateClaim(context.Background(), st, runner, claimInput{
		Actor: board.Agent("task-1", ""), Ref: "#12", Dir: repo, Comment: "done", Touched: []string{"x.go"},
		OtherHolds: func(_ context.Context, card board.Card) ([]heldFiles, error) {
			*calls = append(*calls, "holds "+card.ID)
			return []heldFiles{{Card: 13, TaskID: "task-2", Files: []string{"x.go"}}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(*calls, []string{"check task-1 #12", "detail card-1", "holds card-1", "run echo generated > go.sum"}) {
		t.Fatalf("calls = %q", *calls)
	}
	if _, err := os.Stat(filepath.Join(repo, "go.sum")); err != nil {
		t.Fatalf("the command did not run: %v", err)
	}
	files := evidenceFiles(res.Evidence)
	if _, ok := files["go.sum"]; ok || len(files) != 1 || !files["x.go"].ByTask || files["x.go"].Overlap == nil {
		t.Fatalf("evidence = %+v", res.Evidence.Diff)
	}
	if a := res.Evidence.Accept; a == nil || a.Exit != 0 || a.CmdHash != commandHash(st.fin.AcceptCmd) {
		t.Fatalf("accept = %+v", a)
	}
	var filed Evidence
	if err := json.Unmarshal(res.Request.Evidence, &filed); err != nil || filed.Accept == nil || len(filed.Diff.Files) != 1 {
		t.Fatalf("filed evidence = %s, %v", res.Request.Evidence, err)
	}
	if want := []string{board.FlagOverlap}; !slices.Equal(res.Request.Flags, want) {
		t.Fatalf("flags = %q, want %q", res.Request.Flags, want)
	}
	if res.Request.PassedCmd != st.fin.AcceptCmd {
		t.Fatalf("passed = %q", res.Request.PassedCmd)
	}
}

func TestClaimFiledWhenAcceptanceCouldNotRun(t *testing.T) {
	repo, calls, st := claimScene(t, "make test")
	runner := fakeRunner{calls: calls, res: AcceptResult{Cmd: "make test", Exit: -1, Tail: "the shell did not start: x"},
		err: fmt.Errorf("%w: x", errAcceptanceNotRun)}
	res, err := evaluateClaim(context.Background(), st, runner, claimInput{Actor: board.Agent("task-1", ""), Ref: "#12", Dir: repo})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Request.Flags, []string{board.FlagAcceptanceCouldNotRun}) || res.Evidence.Accept == nil || res.Evidence.Accept.Exit != -1 || res.Request.PassedCmd != "" {
		t.Fatalf("result = %+v", res)
	}
	st.holds = nil
	if _, err := evaluateClaim(context.Background(), st, runner, claimInput{Actor: board.Agent("task-1", ""), Ref: "#12", Dir: repo}); board.CodeOf(err) != board.CodeNotHeld {
		t.Fatalf("no hold = %v", err)
	}
	if _, err := evaluateClaim(context.Background(), fakeClaimStore{calls: calls, fin: st.fin, holds: []board.Hold{{TaskID: "task-1"}}}, runner,
		claimInput{Actor: board.Agent("task-1", ""), Ref: "#12", Dir: repo, OtherHolds: func(context.Context, board.Card) ([]heldFiles, error) {
			return nil, errors.New("holds failed")
		}}); err == nil || err.Error() != "holds failed" {
		t.Fatalf("a holds failure = %v", err)
	}
}

func TestMarshalEvidenceFitsTheStoreLimit(t *testing.T) {
	long := strings.Repeat("p", 4000)
	ev := Evidence{Baseline: board.Baseline{Dirty: []string{}}, Accept: &AcceptResult{Tail: strings.Repeat("\x01", acceptTailBytes)}}
	for i := range 300 {
		ev.Diff.Files = append(ev.Diff.Files, EvidenceFile{Path: long + strconv.Itoa(i)})
		ev.Baseline.Dirty = append(ev.Baseline.Dirty, long)
	}
	data, err := marshalEvidence(ev)
	if err != nil || len(data) > maxEvidenceJSON || !json.Valid(data) {
		t.Fatalf("evidence is %d bytes, %v", len(data), err)
	}
	var got Evidence
	if err := json.Unmarshal(data, &got); err != nil || len(got.Diff.Files) == 0 || len(got.Diff.Files) == 300 {
		t.Fatalf("files kept = %d, %v", len(got.Diff.Files), err)
	}
}
