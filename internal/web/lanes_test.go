package web

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// laneProject is the Project ID the lane fixtures use.
const laneProject = "3f2a9c41-7b1d-4e8a-9c2f-0a1b2c3d4e5f"

// laneFixture is a repository on main with one commit, opened for lanes,
// and a lanes root outside it.
type laneFixture struct {
	t    *testing.T
	ctx  context.Context
	r    *laneRepo
	top  string
	root string
}

func newLaneFixture(t *testing.T) *laneFixture {
	t.Helper()
	gitIdentity(t)
	top := t.TempDir()
	gitIn(t, top, "init", "-q", "-b", "main")
	commitFile(t, top, "a.txt", "one\n")
	ctx := context.Background()
	r, err := openLanes(ctx, laneProject, top)
	if err != nil {
		t.Fatal(err)
	}
	return &laneFixture{t: t, ctx: ctx, r: r, top: top, root: t.TempDir()}
}

// commitFile writes text to dir/name and commits everything, returning the
// new commit.
func commitFile(t *testing.T, dir, name, text string) string {
	t.Helper()
	writeRepoFile(t, dir, name, text)
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "change "+name)
	return gitOutput(t, dir, "rev-parse", "HEAD")
}

// tip is the integration branch's commit, synced with main on first need.
func (f *laneFixture) tip() string {
	f.t.Helper()
	tip, err := f.r.syncInteg(f.ctx, "main")
	if err != nil {
		f.t.Fatal(err)
	}
	return tip
}

// start adds a worktree for an attempt at #seq on the integration tip.
func (f *laneFixture) start(seq int64) lane {
	f.t.Helper()
	l, err := newLane(f.root, laneProject, seq)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.r.addLane(f.ctx, l, f.tip()); err != nil {
		f.t.Fatal(err)
	}
	return l
}

// land lands the attempt the way a done claim does and returns the
// landing commit.
func (f *laneFixture) land(l lane, seq int64) string {
	f.t.Helper()
	tip := f.tip()
	if err := f.r.checkLane(f.ctx, l, tip); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.r.commitLeftovers(f.ctx, l, seq); err != nil {
		f.t.Fatal(err)
	}
	if err := f.r.mergeTip(f.ctx, l, tip); err != nil {
		f.t.Fatal(err)
	}
	landed, err := f.r.squashLane(f.ctx, l, tip, landMessage("Subtask", seq, "did it", fmt.Sprintf("req-%d", seq)))
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.r.moveBranch(f.ctx, f.r.integ, landed, tip); err != nil {
		f.t.Fatal(err)
	}
	return landed
}

// refs lists every ref of the repository with its commit.
func (f *laneFixture) refs() string {
	f.t.Helper()
	return gitOutput(f.t, f.top, "for-each-ref", "--format=%(refname) %(objectname)")
}

// wantCode fails unless err is a refusal with code.
func wantCode(t *testing.T, err error, code string) *Error {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("err = %v, want code %s", err, code)
	}
	return e
}

func TestLaneLandsOntoAMovedTip(t *testing.T) {
	f := newLaneFixture(t)
	main := gitOutput(t, f.top, "rev-parse", "main")
	first, second := f.start(1), f.start(2)
	if f.tip() != main {
		t.Fatalf("integration branch starts at %s, want main %s", f.tip(), main)
	}
	commitFile(t, first.dir, "b.txt", "from one\n")
	landed1 := f.land(first, 1)

	// The second lane forked before the first landed and leaves its work
	// uncommitted.
	writeRepoFile(t, second.dir, "c.txt", "from two\n")
	if ok, err := f.r.commitLeftovers(f.ctx, second, 2); err != nil || !ok {
		t.Fatalf("commitLeftovers = %v, %v", ok, err)
	}
	if got := gitOutput(t, second.dir, "log", "-1", "--format=%s"); got != "#2: work in progress" {
		t.Fatalf("leftover commit subject = %q", got)
	}
	if ok, err := f.r.commitLeftovers(f.ctx, second, 2); err != nil || ok {
		t.Fatalf("a clean lane committed again: %v, %v", ok, err)
	}
	if err := f.r.checkLane(f.ctx, second, landed1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.squashLane(f.ctx, second, landed1, "x"); err == nil {
		t.Fatal("squashLane landed a lane without the tip, which would undo #1")
	}
	if err := f.r.mergeTip(f.ctx, second, landed1); err != nil {
		t.Fatal(err)
	}
	if got := gitOutput(t, second.dir, "log", "-1", "--format=%(trailers:key=Uam-Merge,valueonly)"); got != landed1 {
		t.Fatalf("uam's merge names %q, want the tip %s", got, landed1)
	}
	landed2, err := f.r.squashLane(f.ctx, second, landed1, landMessage("Add c", 2, "c.txt holds two", "req-2"))
	if err != nil {
		t.Fatal(err)
	}
	if got := f.refs(); strings.Contains(got, landed2) {
		t.Fatalf("squashLane moved a ref: %s", got)
	}
	if err := f.r.moveBranch(f.ctx, f.r.integ, landed2, landed1); err != nil {
		t.Fatal(err)
	}

	if got := gitOutput(t, f.top, "rev-parse", f.r.integ); got != landed2 {
		t.Fatalf("integration tip = %s, want %s", got, landed2)
	}
	if got := gitOutput(t, f.top, "rev-list", "--parents", "-n1", landed2); got != landed2+" "+landed1 {
		t.Fatalf("landing parents = %q, want only the tip", got)
	}
	if got := gitOutput(t, f.top, "ls-tree", "--name-only", landed2); got != "a.txt\nb.txt\nc.txt" {
		t.Fatalf("landed tree = %q", got)
	}
	if got := gitOutput(t, f.top, "log", "-1", "--format=%s%n%(trailers:key=Uam-Card,valueonly)%(trailers:key=Uam-Request,valueonly)", landed2); got != "Add c (#2)\n#2\nreq-2" {
		t.Fatalf("landing message = %q", got)
	}
	if got := gitOutput(t, f.top, "rev-parse", "main"); got != main {
		t.Fatalf("landing moved main to %s", got)
	}
}

func TestLaneTipMergeConflictListsFilesAndAborts(t *testing.T) {
	f := newLaneFixture(t)
	first, second := f.start(1), f.start(2)
	commitFile(t, first.dir, "a.txt", "from one\n")
	landed := f.land(first, 1)
	commitFile(t, second.dir, "a.txt", "from two\n")
	writeRepoFile(t, second.dir, "notes.txt", "scratch\n")
	head := gitOutput(t, second.dir, "rev-parse", "HEAD")
	status := gitOutput(t, second.dir, "status", "--porcelain")

	e := wantCode(t, f.r.mergeTip(f.ctx, second, landed), codeLandConflict)
	if !strings.Contains(e.Message, "a.txt") || !strings.Contains(e.Message, "git merge "+f.r.integ) {
		t.Fatalf("refusal = %q, want the file and the merge step", e.Message)
	}
	if !slices.Equal(e.Refs, []string{"#1"}) {
		t.Fatalf("refs = %v, want the card that changed a.txt", e.Refs)
	}
	if got := gitOutput(t, second.dir, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD moved to %s", got)
	}
	if got := gitOutput(t, second.dir, "status", "--porcelain"); got != status {
		t.Fatalf("status = %q, want %q", got, status)
	}
	if merging, err := f.r.mergeHead(f.ctx, second.dir); err != nil || merging {
		t.Fatalf("lane still mid-merge: %v, %v", merging, err)
	}
	if err := f.r.mergeTip(f.ctx, second, head); err != nil {
		t.Fatalf("merging a tip the lane has: %v", err)
	}
}

func TestLaneMidMergeIsDetected(t *testing.T) {
	f := newLaneFixture(t)
	first, second := f.start(1), f.start(2)
	commitFile(t, first.dir, "a.txt", "from one\n")
	landed := f.land(first, 1)
	commitFile(t, second.dir, "a.txt", "from two\n")
	head := gitOutput(t, second.dir, "rev-parse", "HEAD")

	// The agent's own merge stops on the conflict.
	if _, err := runGitWrite(f.ctx, second.dir, nil, "merge", "--no-edit", landed); err == nil {
		t.Fatal("merge did not conflict")
	}
	e := wantCode(t, f.r.checkLane(f.ctx, second, landed), codeLandConflict)
	if !strings.Contains(e.Message, "a.txt") {
		t.Fatalf("refusal = %q, want the unmerged file", e.Message)
	}
	// mergeTip leaves the agent's merge and its partial resolution alone.
	writeRepoFile(t, second.dir, "a.txt", "both\n")
	_ = wantCode(t, f.r.mergeTip(f.ctx, second, landed), codeLandConflict)
	if merging, err := f.r.mergeHead(f.ctx, second.dir); err != nil || !merging {
		t.Fatalf("mergeTip ended the agent's merge: %v, %v", merging, err)
	}
	if got, err := os.ReadFile(filepath.Join(second.dir, "a.txt")); err != nil || string(got) != "both\n" {
		t.Fatalf("a.txt = %q, %v; want the agent's resolution", got, err)
	}
	gitIn(t, second.dir, "add", "a.txt")
	e = wantCode(t, f.r.checkLane(f.ctx, second, landed), codeLandConflict)
	if !strings.Contains(e.Message, "finish your merge of "+f.r.integ) {
		t.Fatalf("refusal = %q", e.Message)
	}
	gitIn(t, second.dir, "commit", "-q", "--no-edit")
	if err := f.r.checkLane(f.ctx, second, landed); err != nil {
		t.Fatal(err)
	}

	// Unmerged paths without MERGE_HEAD, from a cherry-pick.
	gitIn(t, second.dir, "reset", "-q", "--hard", head)
	if _, err := runGitWrite(f.ctx, second.dir, nil, "cherry-pick", landed); err == nil {
		t.Fatal("cherry-pick did not conflict")
	}
	if merging, _ := f.r.mergeHead(f.ctx, second.dir); merging {
		t.Fatal("a cherry-pick wrote MERGE_HEAD")
	}
	e = wantCode(t, f.r.checkLane(f.ctx, second, landed), codeLandConflict)
	if !strings.Contains(e.Message, "a.txt") {
		t.Fatalf("refusal = %q, want the unmerged file", e.Message)
	}
}

func TestLaneStaleWhenTheIntegrationBranchLostALanding(t *testing.T) {
	f := newLaneFixture(t)
	fork := f.tip()
	first, second := f.start(1), f.start(2)
	commitFile(t, first.dir, "b.txt", "one\n")
	landed := f.land(first, 1)
	commitFile(t, second.dir, "c.txt", "two\n")
	if err := f.r.mergeTip(f.ctx, second, landed); err != nil {
		t.Fatal(err)
	}
	if err := f.r.checkLane(f.ctx, second, landed); err != nil {
		t.Fatalf("a lane with the tip merged in: %v", err)
	}

	// The integration branch is moved back by hand.
	gitIn(t, f.top, "update-ref", "refs/heads/"+f.r.integ, fork)
	e := wantCode(t, f.r.checkLane(f.ctx, second, fork), codeLandStale)
	if !strings.Contains(e.Message, "end your turn") {
		t.Fatalf("refusal = %q", e.Message)
	}
}

func TestLaneCompareAndSwapFailsOnAStaleTip(t *testing.T) {
	f := newLaneFixture(t)
	tip := f.tip()
	l := f.start(1)
	commitFile(t, l.dir, "b.txt", "one\n")
	next, err := f.r.squashLane(f.ctx, l, tip, landMessage("B", 1, "", "req-1"))
	if err != nil {
		t.Fatal(err)
	}
	other := commitFile(t, f.top, "c.txt", "owner\n")

	_ = wantCode(t, f.r.moveBranch(f.ctx, f.r.integ, next, other), codeGitBusy)
	_ = wantCode(t, f.r.moveBranch(f.ctx, f.r.integ, next, ""), codeGitBusy)
	if got := gitOutput(t, f.top, "rev-parse", f.r.integ); got != tip {
		t.Fatalf("a failed compare-and-swap moved the branch to %s", got)
	}

	// A worktree with the integration branch checked out blocks every move.
	view := filepath.Join(t.TempDir(), "vi\x1b[31mew")
	gitIn(t, f.top, "worktree", "add", "-q", view, f.r.integ)
	e := wantCode(t, f.r.moveBranch(f.ctx, f.r.integ, next, tip), codeGitBusy)
	if !strings.Contains(e.Message, "checked out") || strings.Contains(e.Message, "\x1b") {
		t.Fatalf("refusal = %q", e.Message)
	}
	gitIn(t, f.top, "worktree", "remove", view)
	if err := f.r.moveBranch(f.ctx, f.r.integ, next, tip); err != nil {
		t.Fatal(err)
	}
}

func TestLaneFinishRule(t *testing.T) {
	f := newLaneFixture(t)
	tip := f.tip()
	commit := func(name string, parent string) string {
		t.Helper()
		l := f.start(9)
		commitFile(t, l.dir, name, name+"\n")
		c, err := f.r.squashLane(f.ctx, l, parent, landMessage(name, 9, "", "req"))
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	ahead := commit("b.txt", tip)
	if on, err := f.r.finishOnInteg(f.ctx, ahead); err != nil || !on {
		t.Fatalf("integ behind the intent: %v, %v", on, err)
	}
	if got := f.tip(); got != ahead {
		t.Fatalf("integ = %s, want fast-forwarded to %s", got, ahead)
	}
	if on, err := f.r.finishOnInteg(f.ctx, tip); err != nil || !on {
		t.Fatalf("intent already on integ: %v, %v", on, err)
	}
	if on, err := f.r.finishOnInteg(f.ctx, ahead); err != nil || !on {
		t.Fatalf("intent at the tip: %v, %v", on, err)
	}
	diverged := commit("c.txt", tip)
	if on, err := f.r.finishOnInteg(f.ctx, diverged); err != nil || on {
		t.Fatalf("diverged intent: %v, %v", on, err)
	}
	if on, err := f.r.finishOnInteg(f.ctx, strings.Repeat("0", len(tip)-1)+"1"); err != nil || on {
		t.Fatalf("missing intent: %v, %v", on, err)
	}
	if got := f.tip(); got != ahead {
		t.Fatalf("integ = %s after diverged intents, want %s", got, ahead)
	}
}

func TestLaneRevertChain(t *testing.T) {
	f := newLaneFixture(t)
	start := f.tip()
	one := f.start(1)
	commitFile(t, one.dir, "b.txt", "one\n")
	landed1 := f.land(one, 1)
	two := f.start(2)
	commitFile(t, two.dir, "c.txt", "two\n")
	landed2 := f.land(two, 2)
	before := f.refs()

	// Given oldest first, it reverts newest first.
	chain, err := f.r.revertChain(f.ctx, landed2, []revertItem{{sha: landed1, seq: 1, title: "Add b"}, {sha: landed2, seq: 2, title: "Add c"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.refs(); got != before {
		t.Fatalf("the revert chain moved refs:\n%s\nwant\n%s", got, before)
	}
	if got := gitOutput(t, f.top, "log", "-2", "--format=%s|%(trailers:key=Uam-Revert,valueonly,separator=)", chain); got != "Revert #1 Add b|#1\nRevert #2 Add c|#2" {
		t.Fatalf("revert commits = %q", got)
	}
	if err := f.r.moveBranch(f.ctx, f.r.integ, chain, landed2); err != nil {
		t.Fatal(err)
	}
	if got, want := gitOutput(t, f.top, "rev-parse", f.r.integ+"^{tree}"), gitOutput(t, f.top, "rev-parse", start+"^{tree}"); got != want {
		t.Fatalf("reverted tree = %s, want the start's %s", got, want)
	}

	// A later landing changed c.txt, so reverting #3 alone conflicts.
	three := f.start(3)
	commitFile(t, three.dir, "c.txt", "three\n")
	landed3 := f.land(three, 3)
	four := f.start(4)
	commitFile(t, four.dir, "c.txt", "four\n")
	landed4 := f.land(four, 4)
	before = f.refs()
	status := gitOutput(t, f.top, "status", "--porcelain")
	_, err = f.r.revertChain(f.ctx, landed4, []revertItem{{sha: landed3, seq: 3, title: "Add c again"}})
	e := wantCode(t, err, codeRevertConflict)
	if !strings.Contains(e.Message, "c.txt") || !slices.Equal(e.Refs, []string{"#4"}) {
		t.Fatalf("refusal = %q refs %v, want c.txt and #4", e.Message, e.Refs)
	}
	if got := f.refs(); got != before {
		t.Fatalf("a refused revert moved refs")
	}
	if got := gitOutput(t, f.top, "status", "--porcelain"); got != status {
		t.Fatalf("a refused revert changed the working tree: %q", got)
	}

	// A commit that is not on the integration branch's first-parent line.
	owner := commitFile(t, f.top, "d.txt", "owner\n")
	_, err = f.r.revertChain(f.ctx, landed4, []revertItem{{sha: owner, seq: 5, title: "Owner"}})
	_ = wantCode(t, err, codeRevertConflict)
}

func TestLaneWorkdirOfAProjectInASubdirectory(t *testing.T) {
	f := newLaneFixture(t)
	commitFile(t, f.top, "svc/api/main.go", "package main\n")
	project := filepath.Join(f.top, "svc", "api")
	r, err := openLanes(f.ctx, laneProject, project)
	if err != nil {
		t.Fatal(err)
	}
	if r.top != f.top {
		t.Fatalf("repo top = %s, want %s", r.top, f.top)
	}
	l := f.start(1)
	dir, err := laneWorkdir(l, r.top, project)
	if err != nil || dir != filepath.Join(l.dir, "svc", "api") {
		t.Fatalf("workdir = %q, %v", dir, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "main.go")); err != nil {
		t.Fatal(err)
	}
	if dir, err := laneWorkdir(l, r.top, f.top); err != nil || dir != l.dir {
		t.Fatalf("workdir at the top = %q, %v", dir, err)
	}
	if _, err := laneWorkdir(l, r.top, t.TempDir()); err == nil {
		t.Fatal("a directory outside the repository mapped into the lane")
	}
}

func TestLaneSync(t *testing.T) {
	f := newLaneFixture(t)
	if tip, err := f.r.syncInteg(f.ctx, "nope"); err == nil {
		t.Fatalf("sync with a missing base = %s", tip)
	}
	start := f.tip()
	if got := gitOutput(t, f.top, "rev-parse", f.r.integ); got != start {
		t.Fatalf("integration branch created at %s, want main %s", got, start)
	}
	l := f.start(1)
	commitFile(t, l.dir, "b.txt", "lane\n")
	landed := f.land(l, 1)

	// Owner work on main merges in, integ first.
	owner := commitFile(t, f.top, "c.txt", "owner\n")
	synced := f.tip()
	if got := gitOutput(t, f.top, "rev-list", "--parents", "-n1", synced); got != synced+" "+landed+" "+owner {
		t.Fatalf("sync merge parents = %q", got)
	}
	if again := f.tip(); again != synced {
		t.Fatalf("a second sync moved integ to %s", again)
	}

	// A conflicting change on main refuses and moves nothing.
	other := f.start(2)
	commitFile(t, other.dir, "a.txt", "lane\n")
	landed = f.land(other, 2)
	commitFile(t, f.top, "a.txt", "owner\n")
	_, err := f.r.syncInteg(f.ctx, "main")
	e := wantCode(t, err, codeMergeConflict)
	if !strings.Contains(e.Message, "a.txt") || !slices.Equal(e.Refs, []string{"#2"}) {
		t.Fatalf("refusal = %q refs %v", e.Message, e.Refs)
	}
	if got := gitOutput(t, f.top, "rev-parse", f.r.integ); got != landed {
		t.Fatalf("a refused sync moved integ to %s", got)
	}
}

func TestLaneSyncAndMergeSettle(t *testing.T) {
	f := newLaneFixture(t)
	l := f.start(1)
	commitFile(t, l.dir, "b.txt", "lane\n")
	f.land(l, 1)
	// needsMerge is the driver's merge fact (plan §5.8): !hasAll(base, integ).
	needsMerge := func() bool {
		t.Helper()
		has, err := f.r.hasAll(f.ctx, gitOutput(t, f.top, "rev-parse", "main"), gitOutput(t, f.top, "rev-parse", f.r.integ))
		if err != nil {
			t.Fatal(err)
		}
		return !has
	}
	settled := func(when string) {
		t.Helper()
		before := f.refs()
		for range 2 {
			f.tip()
			if merged, err := f.r.mergeIntoBase(f.ctx, "main", "Merge plan"); err != nil || merged != "" {
				t.Fatalf("%s: merging again = %q, %v", when, merged, err)
			}
			if needsMerge() {
				t.Fatalf("%s: main still needs a merge", when)
			}
		}
		if got := f.refs(); got != before {
			t.Fatalf("%s: sync and merge wrote commits that change nothing:\n%s\nwant\n%s", when, got, before)
		}
	}
	if !needsMerge() {
		t.Fatal("main has the landing before any merge")
	}
	if _, err := f.r.mergeIntoBase(f.ctx, "main", "Merge plan"); err != nil {
		t.Fatal(err)
	}
	settled("after a merge")

	// The owner's commit reaches integ through a sync merge, and the merge
	// back would bring nothing, though integ is no ancestor of main.
	integ := gitOutput(t, f.top, "rev-parse", f.r.integ)
	owner := commitFile(t, f.top, "c.txt", "owner\n")
	synced := f.tip()
	if synced == integ {
		t.Fatal("the owner's commit did not reach integ")
	}
	if on, err := f.r.isAncestor(f.ctx, synced, owner); err != nil || on {
		t.Fatalf("integ is an ancestor of main (%v, %v); the sync merge did not happen", on, err)
	}
	settled("after the owner's commit")
}

func TestLaneWorktreeOutsideTheRepositoryIsNotInTasksIn(t *testing.T) {
	gitIdentity(t)
	top := branchRepo(t)
	m, id := taskInDir(t, top)
	r, err := openLanes(context.Background(), laneProject, top)
	if err != nil {
		t.Fatal(err)
	}
	tip, err := r.syncInteg(context.Background(), "main")
	if err != nil {
		t.Fatal(err)
	}
	l, err := newLane(m.lanesRoot(), laneProject, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.addLane(context.Background(), l, tip); err != nil {
		t.Fatal(err)
	}
	all := func(*webSession) bool { return true }
	if got := m.tasksIn(top, all); len(got) != 1 {
		t.Fatalf("tasks in the repository = %d, want the owner's Task", len(got))
	}
	setSession(m, id, func(s *webSession) { s.workdir = l.dir })
	if got := m.tasksIn(top, all); len(got) != 0 {
		t.Fatalf("a lane Task counts as in the repository")
	}
	if got := m.tasksIn(l.dir, all); len(got) != 1 {
		t.Fatalf("tasks in the lane = %d, want 1", len(got))
	}
}

func TestLaneAttemptNamesNeverRepeat(t *testing.T) {
	f := newLaneFixture(t)
	tip := f.tip()
	seen := map[string]bool{f.r.integ: true}
	var lanes []lane
	for range 200 {
		l, err := newLane(f.root, laneProject, 7)
		if err != nil {
			t.Fatal(err)
		}
		if seen[l.branch] || seen[l.dir] {
			t.Fatalf("attempt name repeated: %s", l.branch)
		}
		seen[l.branch], seen[l.dir] = true, true
		if !strings.HasPrefix(l.branch, "uam-plan-3f2a9c41-7-") || filepath.Dir(l.dir) != filepath.Join(f.root, laneProject) {
			t.Fatalf("lane = %+v", l)
		}
		if again, err := laneOf(f.root, laneProject, l.branch); err != nil || again != l {
			t.Fatalf("laneOf(%s) = %+v, %v", l.branch, again, err)
		}
		lanes = append(lanes, l)
	}
	for _, l := range lanes[:3] {
		if err := f.r.addLane(f.ctx, l, tip); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Count(gitOutput(t, f.top, "for-each-ref", "--format=%(refname)", "refs/heads/uam-plan-*"), "\n") + 1; got != 4 {
		t.Fatalf("uam branches = %d, want integ and 3 attempts", got)
	}
	for _, bad := range []string{f.r.integ, f.r.integ + "-7-../../x1234567", "uam-plan-00000000-7-1a2b3c4d", f.r.integ + "-7-1A2B3C4D"} {
		if _, err := laneOf(f.root, laneProject, bad); err == nil {
			t.Fatalf("laneOf accepted %q", bad)
		}
	}
}

func TestLaneRemove(t *testing.T) {
	f := newLaneFixture(t)
	first, second := f.start(1), f.start(2)
	commitFile(t, first.dir, "a.txt", "from one\n")
	landed := f.land(first, 1)
	commitFile(t, second.dir, "a.txt", "from two\n")
	if _, err := runGitWrite(f.ctx, second.dir, nil, "merge", "--no-edit", landed); err == nil {
		t.Fatal("merge did not conflict")
	}
	writeRepoFile(t, second.dir, "notes.txt", "left over\n")

	// Unlanded: the merge is aborted, leftovers kept on the branch.
	if err := f.r.removeLane(f.ctx, second, 2, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(second.dir); !os.IsNotExist(err) {
		t.Fatalf("worktree still there: %v", err)
	}
	if got := gitOutput(t, f.top, "show", second.branch+":notes.txt"); got != "left over" {
		t.Fatalf("leftover = %q", got)
	}
	if got := gitOutput(t, f.top, "rev-list", "--count", "--merges", second.branch); got != "0" {
		t.Fatalf("an aborted merge was committed: %s merges", got)
	}

	// Landed: the branch goes too; a worktree already gone is pruned.
	if err := f.r.removeLane(f.ctx, first, 1, true); err != nil {
		t.Fatal(err)
	}
	if got := gitOutput(t, f.top, "branch", "--list", first.branch); got != "" {
		t.Fatalf("landed branch kept: %q", got)
	}
	third := f.start(3)
	if err := os.RemoveAll(third.dir); err != nil {
		t.Fatal(err)
	}
	if err := f.r.removeLane(f.ctx, third, 3, true); err != nil {
		t.Fatal(err)
	}
	if got := gitOutput(t, f.top, "worktree", "list", "--porcelain"); strings.Contains(got, third.dir) {
		t.Fatalf("worktree not pruned:\n%s", got)
	}
}

func TestLaneMergeIntoACheckedOutBase(t *testing.T) {
	f := newLaneFixture(t)
	main := gitOutput(t, f.top, "rev-parse", "main")
	l := f.start(1)
	commitFile(t, l.dir, "b.txt", "lane\n")
	landed := f.land(l, 1)

	merged, err := f.r.mergeIntoBase(f.ctx, "main", "Merge plan: Epic (#1)")
	if err != nil {
		t.Fatal(err)
	}
	if got := gitOutput(t, f.top, "rev-list", "--parents", "-n1", "HEAD"); got != merged+" "+main+" "+landed {
		t.Fatalf("merge parents = %q", got)
	}
	if got := gitOutput(t, f.top, "log", "-1", "--format=%s"); got != "Merge plan: Epic (#1)" {
		t.Fatalf("merge subject = %q", got)
	}
	if _, err := os.Stat(filepath.Join(f.top, "b.txt")); err != nil {
		t.Fatal("the owner's working tree lacks the merged file")
	}
	if again, err := f.r.mergeIntoBase(f.ctx, "main", "again"); err != nil || again != "" {
		t.Fatalf("merging again = %q, %v", again, err)
	}

	// Uncommitted owner changes to a file the merge changes.
	f.tip()
	next := f.start(2)
	commitFile(t, next.dir, "a.txt", "lane\n")
	f.land(next, 2)
	writeRepoFile(t, f.top, "a.txt", "owner, uncommitted\n")
	head := gitOutput(t, f.top, "rev-parse", "HEAD")
	_, err = f.r.mergeIntoBase(f.ctx, "main", "Merge")
	if e := wantCode(t, err, codeLocalChanges); !strings.Contains(e.Message, "a.txt") {
		t.Fatalf("refusal = %q", e.Message)
	}
	if got := gitOutput(t, f.top, "rev-parse", "HEAD"); got != head {
		t.Fatal("a refused merge moved main")
	}

	// A hook refusing the merge commit leaves no merge in progress.
	gitIn(t, f.top, "checkout", "-q", "--", "a.txt")
	hook := filepath.Join(f.top, ".git", "hooks", "pre-merge-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.mergeIntoBase(f.ctx, "main", "Merge"); err == nil {
		t.Fatal("merge ran past a refusing hook")
	}
	if merging, _ := f.r.mergeHead(f.ctx, f.top); merging {
		t.Fatal("the owner's directory was left mid-merge")
	}
	if got := gitOutput(t, f.top, "status", "--porcelain"); got != "" {
		t.Fatalf("the owner's directory was left changed: %q", got)
	}
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}

	// A conflict with the owner's committed work refuses before git merge.
	commitFile(t, f.top, "a.txt", "owner\n")
	head = gitOutput(t, f.top, "rev-parse", "HEAD")
	_, err = f.r.mergeIntoBase(f.ctx, "main", "Merge")
	if e := wantCode(t, err, codeMergeConflict); !strings.Contains(e.Message, "a.txt") || !slices.Equal(e.Refs, []string{"#2"}) {
		t.Fatalf("refusal = %q refs %v", e.Message, e.Refs)
	}
	if got := gitOutput(t, f.top, "rev-parse", "HEAD"); got != head {
		t.Fatal("a refused merge moved main")
	}
}

func TestLaneMergeIntoABaseNobodyHasCheckedOut(t *testing.T) {
	f := newLaneFixture(t)
	main := gitOutput(t, f.top, "rev-parse", "main")
	l := f.start(1)
	commitFile(t, l.dir, "b.txt", "lane\n")
	landed := f.land(l, 1)
	gitIn(t, f.top, "checkout", "-q", "-b", "owner")
	writeRepoFile(t, f.top, "b.txt", "owner, uncommitted\n")

	merged, err := f.r.mergeIntoBase(f.ctx, "main", "Merge plan")
	if err != nil {
		t.Fatal(err)
	}
	if got := gitOutput(t, f.top, "rev-list", "--parents", "-n1", "main"); got != merged+" "+main+" "+landed {
		t.Fatalf("main = %q, want the merge of main and integ", got)
	}
	if got := gitOutput(t, f.top, "rev-parse", "--abbrev-ref", "HEAD"); got != "owner" {
		t.Fatalf("checked out = %s", got)
	}
	if got := gitOutput(t, f.top, "status", "--porcelain"); got != "?? b.txt" {
		t.Fatalf("the owner's working tree changed: %q", got)
	}
}

func TestLaneMergeWaitsForTheOwnersGitOperation(t *testing.T) {
	f := newLaneFixture(t)
	l := f.start(1)
	commitFile(t, l.dir, "b.txt", "lane\n")
	f.land(l, 1)
	// A side commit that conflicts with main on c.txt, which the plan never
	// touches, so the merge itself is clean.
	gitIn(t, f.top, "checkout", "-q", "-b", "side")
	side := commitFile(t, f.top, "c.txt", "side\n")
	gitIn(t, f.top, "checkout", "-q", "main")
	commitFile(t, f.top, "c.txt", "main\n")
	head := commitFile(t, f.top, "d.txt", "main\n")
	refused := func(want string) {
		t.Helper()
		_, err := f.r.mergeIntoBase(f.ctx, "main", "Merge plan")
		if e := wantCode(t, err, codeLocalChanges); !strings.Contains(e.Message, want) {
			t.Fatalf("refusal = %q, want %q", e.Message, want)
		}
		if got := gitOutput(t, f.top, "rev-parse", "main"); got != head {
			t.Fatalf("a refused merge moved main to %s", got)
		}
	}

	if _, err := runGitWrite(f.ctx, f.top, nil, "cherry-pick", side); err == nil {
		t.Fatal("cherry-pick did not conflict")
	}
	refused("a cherry-pick is in progress")
	gitIn(t, f.top, "cherry-pick", "--quit")
	refused("unmerged paths: c.txt")
	gitIn(t, f.top, "reset", "-q", "--hard")

	// A rebase of main detaches HEAD, yet main is still the owner's here.
	if _, err := runGitWrite(f.ctx, f.top, nil, "rebase", "--exec", "false", "HEAD~1"); err == nil {
		t.Fatal("rebase did not stop")
	}
	refused("a rebase is in progress")
	gitIn(t, f.top, "rebase", "--abort")
	if merged, err := f.r.mergeIntoBase(f.ctx, "main", "Merge plan"); err != nil || merged == "" {
		t.Fatalf("merge after the owner finished = %q, %v", merged, err)
	}
}

func TestLaneBranchMidRebaseOrBisectCountsAsCheckedOut(t *testing.T) {
	f := newLaneFixture(t)
	tip := f.tip()
	view := filepath.Join(t.TempDir(), "view")
	gitIn(t, f.top, "worktree", "add", "-q", "-b", "side", view, "main")
	commitFile(t, view, "b.txt", "one\n")
	side := commitFile(t, view, "c.txt", "two\n")
	// A worktree whose directory is gone holds nothing up.
	gone := filepath.Join(t.TempDir(), "gone")
	gitIn(t, f.top, "worktree", "add", "-q", "--detach", gone, "main")
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	busy := func(op string) {
		t.Helper()
		if got := gitOutput(t, f.top, "worktree", "list", "--porcelain"); strings.Contains(got, "branch refs/heads/side") {
			t.Fatalf("%s: git lists side as checked out; the case needs a detached HEAD", op)
		}
		if dir, err := f.r.checkedOut(f.ctx, "side"); err != nil || realPath(dir) != realPath(view) {
			t.Fatalf("%s: checkedOut = %q, %v; want %s", op, dir, err, view)
		}
		_ = wantCode(t, f.r.moveBranch(f.ctx, "side", tip, side), codeGitBusy)
	}

	if _, err := runGitWrite(f.ctx, view, nil, "rebase", "--exec", "false", "HEAD~1"); err == nil {
		t.Fatal("rebase did not stop")
	}
	busy("rebase")
	gitIn(t, view, "rebase", "--abort")
	gitIn(t, view, "bisect", "start", "HEAD", "HEAD~2")
	busy("bisect")
	gitIn(t, view, "bisect", "reset")

	// Detached with neither in progress, the branch is free to move.
	gitIn(t, view, "switch", "-q", "--detach")
	if err := f.r.moveBranch(f.ctx, "side", tip, side); err != nil {
		t.Fatal(err)
	}
}

func TestLaneBranchesBesideABranchNamedUam(t *testing.T) {
	f := newLaneFixture(t)
	gitIn(t, f.top, "branch", "uam")
	if err := f.r.preflight(f.ctx, "main"); err != nil {
		t.Fatal(err)
	}
	l := f.start(1)
	for _, branch := range []string{"uam", f.r.integ, l.branch} {
		gitIn(t, f.top, "rev-parse", "--verify", "-q", "refs/heads/"+branch)
	}
}

func TestLanePreflight(t *testing.T) {
	f := newLaneFixture(t)
	if err := f.r.preflight(f.ctx, "main"); err != nil {
		t.Fatal(err)
	}
	if err := f.r.preflight(f.ctx, "missing"); err == nil {
		t.Fatal("a missing base passed")
	}
	if err := f.r.preflight(f.ctx, "main@{1}"); err == nil {
		t.Fatal("a base that is no branch name passed")
	}
	empty := t.TempDir()
	gitIn(t, empty, "init", "-q", "-b", "main")
	r, err := openLanes(f.ctx, laneProject, empty)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.preflight(f.ctx, "main"); err == nil {
		t.Fatal("a repository without commits passed")
	}

	gitIn(t, f.top, "config", "user.useConfigOnly", "true")
	anonymous := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(anonymous, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", anonymous)
	_ = wantCode(t, f.r.preflight(f.ctx, "main"), codeNoGitIdentity)
}

func TestLaneGitVersion(t *testing.T) {
	for v, want := range map[string]bool{
		"git version 2.54.0":                 true,
		"git version 2.40.0":                 true,
		"git version 3.0.1":                  true,
		"git version 2.39.3 (Apple Git-146)": false,
		"git version 2.45.2.windows.1":       true,
		"git version 1.99.9":                 false,
		"nonsense":                           false,
	} {
		if got := gitAtLeast(v, 2, 40); got != want {
			t.Errorf("gitAtLeast(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestLaneRefusesADirectoryThatIsNoWorktree(t *testing.T) {
	f := newLaneFixture(t)
	tip := f.tip()
	// A plain directory inside the repository: git would fall through to it.
	l := lane{branch: f.r.integ + "-1-1a2b3c4d", dir: filepath.Join(f.top, "plain")}
	writeRepoFile(t, l.dir, "x.txt", "stray\n")
	head := gitOutput(t, f.top, "rev-parse", "HEAD")
	if _, err := f.r.commitLeftovers(f.ctx, l, 1); err == nil {
		t.Fatal("commitLeftovers ran outside a lane")
	}
	if err := f.r.mergeTip(f.ctx, l, tip); err == nil {
		t.Fatal("mergeTip ran outside a lane")
	}
	if _, err := f.r.squashLane(f.ctx, l, tip, "x"); err == nil {
		t.Fatal("squashLane ran outside a lane")
	}
	if err := f.r.removeLane(f.ctx, l, 1, true); err == nil {
		t.Fatal("removeLane ran outside a lane")
	}
	if got := gitOutput(t, f.top, "rev-parse", "HEAD"); got != head {
		t.Fatal("the owner's branch got a commit")
	}
	if got := gitOutput(t, f.top, "status", "--porcelain"); got != "?? plain/" {
		t.Fatalf("status = %q", got)
	}
}

func TestLaneGitFailuresAreErrors(t *testing.T) {
	f := newLaneFixture(t)
	tip := f.tip()
	l := f.start(1)
	ctx, cancel := context.WithCancel(f.ctx)
	cancel()
	failures := map[string]error{
		"preflight":  f.r.preflight(ctx, "main"),
		"addLane":    f.r.addLane(ctx, l, tip),
		"removeLane": f.r.removeLane(ctx, l, 1, true),
		"checkLane":  f.r.checkLane(ctx, l, tip),
		"mergeTip":   f.r.mergeTip(ctx, l, tip),
		"moveBranch": f.r.moveBranch(ctx, f.r.integ, tip, tip),
	}
	_, failures["openLanes"] = openLanes(ctx, laneProject, f.top)
	_, failures["syncInteg"] = f.r.syncInteg(ctx, "main")
	_, failures["commitLeftovers"] = f.r.commitLeftovers(ctx, l, 1)
	_, failures["squashLane"] = f.r.squashLane(ctx, l, tip, "x")
	_, failures["finishOnInteg"] = f.r.finishOnInteg(ctx, tip)
	_, failures["revertChain"] = f.r.revertChain(ctx, tip, nil)
	_, failures["mergeIntoBase"] = f.r.mergeIntoBase(ctx, "main", "x")
	for name, err := range failures {
		if err == nil {
			t.Errorf("%s succeeded without git", name)
		}
	}
	if _, err := os.Stat(l.dir); err != nil {
		t.Fatalf("the lane was touched: %v", err)
	}
}

func TestLaneFileListIsCapped(t *testing.T) {
	var files []string
	for i := range 12 {
		files = append(files, fmt.Sprintf("f%d.go", i))
	}
	if got := fileList(files); !strings.HasSuffix(got, "f9.go and 2 more") || strings.Count(got, ", ") != 9 {
		t.Fatalf("fileList = %q", got)
	}
	if got := fileList([]string{"a\x1b[31m.go"}); strings.Contains(got, "\x1b") {
		t.Fatalf("fileList kept a control sequence: %q", got)
	}
}
