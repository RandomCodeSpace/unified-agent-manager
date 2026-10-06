package web

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/board"
)

// subscribe follows the run's frames until the test ends.
func (r *laneRun) subscribe() *Subscriber {
	r.t.Helper()
	sub, _, err := r.m.Subscribe("")
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { r.m.Unsubscribe(sub) })
	return sub
}

// nextMerge skips frames until a merge job of the run's Project starts, and
// returns that job's last frame.
func (r *laneRun) nextMerge(sub *Subscriber) boardJobEvent {
	r.t.Helper()
	id := ""
	for {
		ev := jobFrame(r.t, sub)
		switch {
		case ev.Kind != jobMerge || ev.ProjectID != r.project:
		case ev.Status == jobRunning:
			id = ev.JobID
		case ev.JobID == id:
			return ev
		}
	}
}

// noMerge requires that no merge job started so far.
func (r *laneRun) noMerge(sub *Subscriber) {
	r.t.Helper()
	for {
		select {
		case raw := <-sub.Frames():
			if f := parseFrame(r.t, raw); f.event == "board_job" && strings.Contains(string(raw), `"kind":"merge"`) {
				r.t.Fatalf("a merge job started: %s", raw)
			}
		default:
			return
		}
	}
}

// reboot closes the planner and opens it again, as a restart does.
func (r *laneRun) reboot() {
	r.t.Helper()
	r.m.closeBoard()
	if err := r.m.openBoard(context.Background()); err != nil {
		r.t.Fatal(err)
	}
}

// integration is the Project's integration branch, as the Planner header
// reads it.
func (r *laneRun) integration() BoardIntegration {
	r.t.Helper()
	var p BoardProject
	r.call(http.MethodGet, "/api/board/projects/"+r.project, "", http.StatusOK, &p)
	if p.Integration == nil {
		r.t.Fatal("the project has no integration branch")
	}
	return *p.Integration
}

func (r *laneRun) epicComments(prefix string) []string {
	r.t.Helper()
	var out []string
	for _, c := range comments(r.card(r.epic.ID)) {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

// slowRetries holds a waiting merge back until retryNow. Called before the
// run starts, its cleanup runs after the run's.
func slowRetries(t *testing.T) {
	t.Helper()
	saved := mergeBackoff
	mergeBackoff = []time.Duration{time.Hour}
	t.Cleanup(func() { mergeBackoff = saved })
}

// retryNow has uam try the Project's waiting merge again at once.
func (r *laneRun) retryNow() {
	r.t.Helper()
	r.m.lanes.mu.Lock()
	defer r.m.lanes.mu.Unlock()
	pm := r.m.lanes.merges[r.project]
	if pm == nil || pm.retry == nil {
		r.t.Fatal("no merge waits to be tried again")
	}
	pm.retry.Reset(0)
}

// An approved epic that finishes is merged into main with no click, and its
// epic says what the merge carried; once main has everything the
// integration branch carries nothing fires again, after a sync merge of an
// owner commit too.
func TestMergeWhenApprovedEpicFinishes(t *testing.T) {
	r := newFinishingRun(t, "true", "Add b", "Add c")
	sub := r.subscribe()
	main := gitOutput(t, r.repo, "rev-parse", "main")
	r.landWith(0, "b.txt", "b\n")
	r.noMerge(sub)
	landed := r.landWith(1, "c.txt", "c\n")
	if end := r.nextMerge(sub); end.Status != jobDone || end.CardID != "" {
		t.Fatalf("merge job = %+v", end)
	}
	merged := gitOutput(t, r.repo, "rev-parse", "main")
	if got := gitOutput(t, r.repo, "rev-list", "--parents", "-n1", merged); got != merged+" "+main+" "+landed {
		t.Fatalf("main = %q, want the merge of main and %s", got, landed)
	}
	if got, want := gitOutput(t, r.repo, "log", "-1", "--format=%s", merged), fmt.Sprintf("Merge %s: Epic (#%d)", r.integ(), r.epic.Seq); got != want {
		t.Fatalf("merge subject = %q, want %q", got, want)
	}
	if gone(filepath.Join(r.repo, "c.txt")) {
		t.Fatal("the owner's working tree lacks the merged work")
	}
	want := fmt.Sprintf("uam: Merged into main as %s. It carried:\n- #%d Add b\n- #%d Add c", shortSHA(merged), r.leaves[0].Seq, r.leaves[1].Seq)
	if got := r.epicComments("uam: Merged"); !slices.Equal(got, []string{want}) {
		t.Fatalf("epic comments = %q, want %q", got, want)
	}
	if i := r.integration(); i.Ahead != 0 || i.Merge != nil {
		t.Fatalf("integration after the merge = %+v", i)
	}

	// The owner commits on main, and the integration branch takes it as a
	// sync merge: main has everything, so a restart merges nothing.
	owner := commitFile(t, r.repo, "d.txt", "owner\n")
	repo, err := openLanes(context.Background(), r.project, r.repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.syncInteg(context.Background(), "main"); err != nil {
		t.Fatal(err)
	}
	r.reboot()
	r.noMerge(sub)
	if gitOutput(t, r.repo, "rev-parse", "main") != owner || r.integration().Ahead != 0 {
		t.Fatal("main moved, or counts the sync merge as ahead")
	}
}

// An approved epic the owner finishes with no landing, by cancelling its
// last open subtask, is merged into main as one whose last subtask lands.
func TestMergeWhenOwnerFinishesApprovedEpic(t *testing.T) {
	r := newFinishingRun(t, "true", "Add b", "Add c")
	sub := r.subscribe()
	r.landWith(0, "b.txt", "b\n")
	r.noMerge(sub)
	r.call(http.MethodPost, "/api/board/cards/"+r.leaves[1].ID+"/status", `{"status":"cancelled","comment":"not needed"}`, http.StatusOK, nil)
	if end := r.nextMerge(sub); end.Status != jobDone {
		t.Fatalf("merge job = %+v", end)
	}
	if got := gitOutput(t, r.repo, "show", "main:b.txt"); got != "b" {
		t.Fatalf("main's b.txt = %q", got)
	}
}

// Once main has everything the integration branch carries, as after the
// owner merges it by hand in Terminal, the header drops a blocked or a
// waiting merge, and the waiting one's retry with it.
func TestMergeStateClearsOnceMainHasItAll(t *testing.T) {
	t.Run("blocked", func(t *testing.T) {
		r := newFinishingRun(t, "true", "Change a")
		sub := r.subscribe()
		task, l := r.start(0)
		commitFile(t, l.dir, "a.txt", "lane\n")
		commitFile(t, r.repo, "a.txt", "owner\n")
		r.landed(task, 0)
		if end := r.nextMerge(sub); end.Status != jobFailed {
			t.Fatalf("conflicting merge = %+v", end)
		}
		if m := r.integration().Merge; m == nil || m.State != mergeBlocked {
			t.Fatalf("merge state = %+v", m)
		}
		gitIn(t, r.repo, "merge", "-q", "--no-edit", "-X", "theirs", r.integ())
		if i := r.integration(); i.Ahead != 0 || i.Merge != nil {
			t.Fatalf("integration once the owner merged by hand = %+v", i)
		}
	})
	t.Run("waiting", func(t *testing.T) {
		slowRetries(t)
		r := newFinishingRun(t, "true", "Add b")
		sub := r.subscribe()
		writeRepoFile(t, r.repo, "b.txt", "owner, uncommitted\n")
		r.landWith(0, "b.txt", "b\n")
		if end := r.nextMerge(sub); end.Status != jobFailed {
			t.Fatalf("merge over uncommitted b.txt = %+v", end)
		}
		if err := os.Remove(filepath.Join(r.repo, "b.txt")); err != nil {
			t.Fatal(err)
		}
		gitIn(t, r.repo, "merge", "-q", "--no-edit", r.integ())
		if i := r.integration(); i.Ahead != 0 || i.Merge != nil {
			t.Fatalf("integration once the owner merged by hand = %+v", i)
		}
		r.m.lanes.mu.Lock()
		retry := r.m.lanes.merges[r.project].retry
		r.m.lanes.mu.Unlock()
		if retry != nil {
			t.Fatal("the merge main no longer needs is still tried again")
		}
	})
}

// A merge into main checked out in the Project directory runs git's hooks
// there; uncommitted changes the merge would overwrite make it wait, named
// once on the epic, and uam tries it again until it goes through.
func TestMergeIntoCheckedOutBranch(t *testing.T) {
	slowRetries(t)
	r := newFinishingRun(t, "true", "Add b")
	sub := r.subscribe()
	writeHook(t, r.repo, "post-merge", "#!/bin/sh\necho ran > .git/merged-by-hook\n")
	writeRepoFile(t, r.repo, "b.txt", "owner, uncommitted\n")
	main := gitOutput(t, r.repo, "rev-parse", "main")
	r.landWith(0, "b.txt", "b\n")
	for range 2 {
		end := r.nextMerge(sub)
		if end.Status != jobFailed || !strings.Contains(end.Error, "b.txt") {
			t.Fatalf("merge over uncommitted b.txt = %+v", end)
		}
		m := r.integration().Merge
		if m == nil || m.State != mergeWaiting || !strings.Contains(m.Reason, "b.txt") || m.RetryAt == nil {
			t.Fatalf("merge state = %+v", m)
		}
		r.retryNow()
	}
	if gitOutput(t, r.repo, "rev-parse", "main") != main {
		t.Fatal("a waiting merge moved main")
	}
	if waits := r.epicComments("uam: Merge of "); len(waits) != 1 || !strings.Contains(waits[0], "is waiting") || !strings.Contains(waits[0], "b.txt") {
		t.Fatalf("waiting comments = %q", waits)
	}
	if end := r.nextMerge(sub); end.Status != jobFailed {
		t.Fatalf("third try = %+v", end)
	}
	if err := os.Remove(filepath.Join(r.repo, "b.txt")); err != nil {
		t.Fatal(err)
	}
	r.retryNow()
	if end := r.nextMerge(sub); end.Status != jobDone {
		t.Fatalf("merge once b.txt went = %+v", end)
	}
	if got := gitOutput(t, r.repo, "rev-parse", "main^1"); got != main {
		t.Fatalf("main^1 = %s, want %s", got, main)
	}
	if _, err := os.Stat(filepath.Join(r.repo, ".git", "merged-by-hook")); err != nil {
		t.Fatal("the merge did not run the post-merge hook")
	}
	if m := r.integration().Merge; m != nil {
		t.Fatalf("merge state after it went through = %+v", m)
	}
}

func writeHook(t *testing.T, repo, name, script string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, ".git", "hooks", name), []byte(script), 0o755); err != nil { // #nosec G306 -- a hook git must run.
		t.Fatal(err)
	}
}

// A merge into main checked out nowhere moves the main ref only: the
// owner's working tree and its uncommitted files stay as they are.
func TestMergeIntoBranchNotCheckedOutMovesTheRefOnly(t *testing.T) {
	r := newFinishingRun(t, "true", "Add b")
	sub := r.subscribe()
	gitIn(t, r.repo, "checkout", "-q", "-b", "owner")
	writeRepoFile(t, r.repo, "b.txt", "owner, uncommitted\n")
	main := gitOutput(t, r.repo, "rev-parse", "main")
	landed := r.landWith(0, "b.txt", "b\n")
	if end := r.nextMerge(sub); end.Status != jobDone {
		t.Fatalf("merge job = %+v", end)
	}
	merged := gitOutput(t, r.repo, "rev-parse", "main")
	if got := gitOutput(t, r.repo, "rev-list", "--parents", "-n1", merged); got != merged+" "+main+" "+landed {
		t.Fatalf("main = %q, want the merge of main and %s", got, landed)
	}
	if got := gitOutput(t, r.repo, "rev-parse", "--abbrev-ref", "HEAD"); got != "owner" {
		t.Fatalf("checked out = %s", got)
	}
	if got := gitOutput(t, r.repo, "status", "--porcelain"); got != "?? b.txt" {
		t.Fatalf("the owner's working tree changed: %q", got)
	}
}

// A merge that conflicts with main is blocked: the epic names the files and
// the cards, and nothing tries it again for the same tips, a restart
// included, until the owner's Retry merge or new tips.
func TestMergeConflictBlocksUntilRetryOrNewTip(t *testing.T) {
	r := newFinishingRun(t, "true", "Change a")
	sub := r.subscribe()
	task, l := r.start(0)
	commitFile(t, l.dir, "a.txt", "lane\n")
	owner := commitFile(t, r.repo, "a.txt", "owner\n")
	r.landed(task, 0)
	end := r.nextMerge(sub)
	if end.Status != jobFailed || !strings.Contains(end.Error, "a.txt") {
		t.Fatalf("conflicting merge = %+v", end)
	}
	m := r.integration().Merge
	if m == nil || m.State != mergeBlocked || !strings.Contains(m.Reason, "a.txt") || m.RetryAt != nil {
		t.Fatalf("merge state = %+v", m)
	}
	card := fmt.Sprintf("#%d", r.leaves[0].Seq)
	blocked := r.epicComments("uam: Merge of ")
	if len(blocked) != 1 || !strings.Contains(blocked[0], "is blocked") || !strings.Contains(blocked[0], "a.txt") || !strings.Contains(blocked[0], card) {
		t.Fatalf("blocked comments = %q", blocked)
	}

	r.reboot()
	r.noMerge(sub)

	var job struct {
		JobID string `json:"job_id"`
	}
	r.call(http.MethodPost, "/api/board/projects/"+r.project+"/merge", `{}`, http.StatusAccepted, &job)
	if again := r.nextMerge(sub); again.JobID != job.JobID || again.Status != jobFailed {
		t.Fatalf("Retry merge = %+v, want job %s failed", again, job.JobID)
	}
	if got := r.epicComments("uam: Merge of "); len(got) != 1 {
		t.Fatalf("a retry of the same tips commented again: %q", got)
	}

	// The owner takes the change back on main: new tips, so the next pass
	// merges.
	gitIn(t, r.repo, "revert", "--no-edit", owner)
	r.reboot()
	if end := r.nextMerge(sub); end.Status != jobDone {
		t.Fatalf("merge on new tips = %+v", end)
	}
	if got := gitOutput(t, r.repo, "show", "main:a.txt"); got != "lane" {
		t.Fatalf("main's a.txt = %q", got)
	}
	if m := r.integration().Merge; m != nil {
		t.Fatalf("merge state after it went through = %+v", m)
	}
}

// The owner's Revert of work main already has merges the revert into main.
func TestRevertOfMergedWorkMergesAgain(t *testing.T) {
	r := newFinishingRun(t, "true", "Add b")
	sub := r.subscribe()
	before := gitOutput(t, r.repo, "rev-parse", "main^{tree}")
	r.landWith(0, "b.txt", "b\n")
	if end := r.nextMerge(sub); end.Status != jobDone {
		t.Fatalf("merge job = %+v", end)
	}
	leaf := r.leaves[0]
	if p := r.preview(leaf.ID); !p.Merged {
		t.Fatalf("preview = %+v, want merged", p)
	}
	if end := r.revert(leaf.ID, revertBody(nil, leaf.ID)); end.Status != jobDone {
		t.Fatalf("revert job = %+v", end)
	}
	if end := r.nextMerge(sub); end.Status != jobDone {
		t.Fatalf("merge of the revert = %+v", end)
	}
	if gitOutput(t, r.repo, "rev-parse", "main^{tree}") != before || !gone(filepath.Join(r.repo, "b.txt")) {
		t.Fatal("main still has the reverted work")
	}
	want := fmt.Sprintf("- the revert of #%d Add b", leaf.Seq)
	if got := r.epicComments("uam: Merged"); len(got) != 2 || !strings.HasSuffix(got[1], want) {
		t.Fatalf("merge comments = %q, want the second to end %q", got, want)
	}
	if c := r.card(leaf.ID).Card; c.Status != board.StatusTodo || c.Paused != board.PausedOwner {
		t.Fatalf("the reverted subtask = %+v", c)
	}
}

// The first pass after the planner opens merges only work of a finished
// approved epic that the base branch lacks: with one epic merged and
// another still running, a restart merges nothing, so the running epic's
// landed work waits for its epic as it does without a restart.
func TestBootMergesOnlyFinishedEpicsWork(t *testing.T) {
	r := newFinishingRun(t, "true", "Add b")
	sub := r.subscribe()
	r.landWith(0, "b.txt", "b\n")
	if end := r.nextMerge(sub); end.Status != jobDone {
		t.Fatalf("merge of the finished epic = %+v", end)
	}
	main := gitOutput(t, r.repo, "rev-parse", "main")
	epic := r.create(board.KindEpic, "", "Second")
	story := r.create(board.KindStory, epic.ID, "Story")
	first, second := r.create(board.KindSubtask, story.ID, "Add c"), r.create(board.KindSubtask, story.ID, "Add d")
	r.call(http.MethodPost, "/api/board/cards/"+epic.ID+"/approve", r.approveBody("luna", epic.ID, story.ID, first.ID, second.ID), http.StatusOK, nil)
	r.leaves = append(r.leaves, first, second)
	r.landWith(1, "c.txt", "c\n")
	r.noMerge(sub)
	r.reboot()
	r.noMerge(sub)
	if gitOutput(t, r.repo, "rev-parse", "main") != main || r.integration().Ahead != 1 {
		t.Fatal("a restart merged the running epic's work")
	}
}

// A merge that finds another git process's lock in its way waits and is
// tried again, as one that waits on uncommitted changes does: the index
// lock where main is checked out, or main's ref lock where it is not.
func TestMergeWaitsOnAGitLock(t *testing.T) {
	for _, tc := range []struct {
		name, lock string
		elsewhere  bool
	}{{"index lock", "index.lock", false}, {"ref lock", filepath.Join("refs", "heads", "main.lock"), true}} {
		t.Run(tc.name, func(t *testing.T) {
			slowRetries(t)
			r := newFinishingRun(t, "true", "Add b")
			sub := r.subscribe()
			if tc.elsewhere {
				gitIn(t, r.repo, "checkout", "-q", "-b", "owner")
			}
			lock := filepath.Join(r.repo, ".git", tc.lock)
			if err := os.WriteFile(lock, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			main := gitOutput(t, r.repo, "rev-parse", "main")
			r.landWith(0, "b.txt", "b\n")
			if end := r.nextMerge(sub); end.Status != jobFailed || !strings.Contains(end.Error, ".lock") {
				t.Fatalf("merge behind a lock = %+v", end)
			}
			if m := r.integration().Merge; m == nil || m.State != mergeWaiting || m.RetryAt == nil {
				t.Fatalf("merge state = %+v", m)
			}
			if gitOutput(t, r.repo, "rev-parse", "main") != main {
				t.Fatal("a waiting merge moved main")
			}
			if err := os.Remove(lock); err != nil {
				t.Fatal(err)
			}
			r.retryNow()
			if end := r.nextMerge(sub); end.Status != jobDone {
				t.Fatalf("merge once the lock went = %+v", end)
			}
		})
	}
}

// The merge preview lists what a merge would carry into the base branch,
// oldest first: each landing and revert it lacks, with its subtask and
// epic, the landings that changed tests or build files marked; nothing
// once the base has it all.
func TestMergePreviewListsWhatItCarries(t *testing.T) {
	r := newLaneRun(t, "true", "Add b", "Test c")
	sub := r.subscribe()
	r.landWith(0, "b.txt", "b\n")
	r.landWith(1, "c_test.go", "package c\n")
	b, c := r.leaves[0], r.leaves[1]
	if end := r.revert(b.ID, revertBody(nil, b.ID)); end.Status != jobDone {
		t.Fatalf("revert job = %+v", end)
	}
	preview := func() MergePreview {
		var p MergePreview
		r.call(http.MethodGet, "/api/board/projects/"+r.project+"/merge", "", http.StatusOK, &p)
		return p
	}
	want := MergePreview{Branch: r.integ(), BaseRef: "main", Items: []MergeCarried{
		{CardID: b.ID, Seq: b.Seq, Title: "Add b", EpicID: r.epic.ID},
		{CardID: c.ID, Seq: c.Seq, Title: "Test c", EpicID: r.epic.ID, Flagged: true},
		{CardID: b.ID, Seq: b.Seq, Title: "Add b", EpicID: r.epic.ID, Revert: true},
	}}
	if got := preview(); !reflect.DeepEqual(got, want) {
		t.Fatalf("preview = %+v, want %+v", got, want)
	}
	r.call(http.MethodPost, "/api/board/projects/"+r.project+"/merge", `{}`, http.StatusAccepted, nil)
	if end := r.nextMerge(sub); end.Status != jobDone {
		t.Fatalf("early merge = %+v", end)
	}
	if got := preview(); got.BaseRef != "main" || len(got.Items) != 0 || got.Items == nil {
		t.Fatalf("preview once main has it all = %+v", got)
	}
}
