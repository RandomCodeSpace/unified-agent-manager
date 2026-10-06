package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/board"
)

// link makes subtask i wait on subtask j.
func (r *laneRun) link(j, i int) {
	r.t.Helper()
	r.call(http.MethodPost, "/api/board/links", fmt.Sprintf(`{"blocker":%q,"blocked":%q}`, r.leaves[j].ID, r.leaves[i].ID), http.StatusNoContent, nil)
}

// landWith starts subtask i's lane, commits name in it and lands it, and
// returns the landing commit.
func (r *laneRun) landWith(i int, name, text string) string {
	r.t.Helper()
	task, l := r.start(i)
	commitFile(r.t, l.dir, name, text)
	return r.landed(task, i)
}

// revertBody is the owner's Revert post with the cards the preview showed.
func revertBody(include []string, expect ...string) string {
	return fmt.Sprintf(`{"include":%s,"expect":%s,"comment":"it broke the build"}`, jsonList(include), jsonList(expect))
}

func jsonList(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = fmt.Sprintf("%q", s)
	}
	return "[" + strings.Join(quoted, ",") + "]"
}

// revert posts the owner's Revert of ref and returns its job's last frame.
func (r *laneRun) revert(ref, body string) boardJobEvent {
	r.t.Helper()
	sub, _, err := r.m.Subscribe("")
	if err != nil {
		r.t.Fatal(err)
	}
	defer r.m.Unsubscribe(sub)
	var job struct {
		JobID string `json:"job_id"`
	}
	r.call(http.MethodPost, "/api/board/cards/"+ref+"/revert", body, http.StatusAccepted, &job)
	running, end := jobFrame(r.t, sub), jobFrame(r.t, sub)
	if running.JobID != job.JobID || running.Kind != jobRevert || running.Status != jobRunning || end.JobID != job.JobID {
		r.t.Fatalf("frames = %+v then %+v, want job %s", running, end, job.JobID)
	}
	return end
}

func (r *laneRun) preview(ref string, include ...string) RevertPreview {
	r.t.Helper()
	var p RevertPreview
	r.call(http.MethodGet, "/api/board/cards/"+ref+"/revert?"+url.Values{"include": include}.Encode(), "", http.StatusOK, &p)
	return p
}

// reverted requires subtask i to be reverted in commit: To do, paused by
// the owner, its landing kept and marked reverted.
func (r *laneRun) reverted(i int, landed, commit string) {
	r.t.Helper()
	d := r.card(r.leaves[i].ID)
	h := d.Holds[len(d.Holds)-1]
	if d.Card.Status != board.StatusTodo || d.Card.Paused != board.PausedOwner || d.Card.Lane == nil || d.Card.Lane.LandedSHA != landed ||
		d.Card.Lane.RevertedSHA != commit || h.RevertedSHA != commit || !slices.Contains(comments(d), "uam: reverted in "+shortSHA(commit)+": it broke the build") {
		r.t.Fatalf("#%d after the revert = %+v, lane %+v, comments %q", r.leaves[i].Seq, d.Card, d.Card.Lane, comments(d))
	}
}

func (r *laneRun) reflog() int {
	r.t.Helper()
	return len(strings.Fields(gitOutput(r.t, r.repo, "reflog", "show", "--format=%H", "refs/heads/"+r.integ())))
}

// The owner reverts one landed subtask: the preview names its commit and
// files, and the job puts one revert commit on the integration branch,
// whose tree is the one before the landing, and the subtask is To do,
// paused by the owner.
func TestRevertOneLandedSubtask(t *testing.T) {
	r := newLaneRun(t, "true", "Add b")
	before, main := r.tip(), gitOutput(t, r.repo, "rev-parse", "main")
	landed := r.landWith(0, "b.txt", "b\n")
	leaf := r.leaves[0]

	want := RevertPreview{Branch: r.integ(), Cards: []string{leaf.ID}, Running: []string{},
		Landings: []RevertLanding{{CardID: leaf.ID, Seq: leaf.Seq, Title: "Add b", SHA: landed, Files: []string{"b.txt"}}}}
	if got := r.preview(leaf.ID); !reflect.DeepEqual(got, want) {
		t.Fatalf("preview = %+v, want %+v", got, want)
	}
	if end := r.revert(leaf.ID, revertBody(nil, leaf.ID)); end.Status != jobDone {
		t.Fatalf("revert job = %+v", end)
	}
	tip := r.tip()
	msg := fmt.Sprintf("Revert #%d Add b\n\nThis reverts %s.\n\nUam-Revert: #%d", leaf.Seq, landed, leaf.Seq)
	if gitOutput(t, r.repo, "rev-parse", tip+"^") != landed || gitOutput(t, r.repo, "log", "-1", "--format=%B", tip) != msg {
		t.Fatalf("revert commit %s = %q, want %q on %s", tip, gitOutput(t, r.repo, "log", "-1", "--format=%B", tip), msg, landed)
	}
	if gitOutput(t, r.repo, "rev-parse", tip+"^{tree}") != gitOutput(t, r.repo, "rev-parse", before+"^{tree}") {
		t.Fatal("the reverted tree is not the one before the landing")
	}
	if gitOutput(t, r.repo, "rev-parse", "main") != main {
		t.Fatal("the revert moved the owner's branch")
	}
	r.reverted(0, landed, tip)
}

// A revert takes along the landed subtasks that started on top of what it
// reverts, newest first, in one move of the integration branch; a landed
// subtask that started on top of nothing reverted stays.
func TestRevertTakesDoneDependentsAlong(t *testing.T) {
	r := newLaneRun(t, "true", "First", "Second", "Third")
	r.link(0, 1)
	first := r.landWith(0, "one.txt", "one\n")
	second := r.landWith(1, "two.txt", "two\n")
	r.landWith(2, "three.txt", "three\n")
	before, moves := r.tip(), r.reflog()

	if p := r.preview(r.leaves[0].ID); !slices.Equal(p.Cards, []string{r.leaves[0].ID, r.leaves[1].ID}) {
		t.Fatalf("preview cards = %v", p.Cards)
	}
	if end := r.revert(r.leaves[0].ID, revertBody(nil, r.leaves[1].ID, r.leaves[0].ID)); end.Status != jobDone {
		t.Fatalf("revert job = %+v", end)
	}
	tip := r.tip()
	if got, want := gitOutput(t, r.repo, "log", "--format=%s", before+".."+tip), fmt.Sprintf("Revert #%d First\nRevert #%d Second", r.leaves[0].Seq, r.leaves[1].Seq); got != want {
		t.Fatalf("revert commits = %q, want %q", got, want)
	}
	if gitOutput(t, r.repo, "rev-parse", tip+"~2") != before || r.reflog() != moves+1 {
		t.Fatalf("the integration branch moved %d times to %s", r.reflog()-moves, tip)
	}
	if got := gitOutput(t, r.repo, "ls-tree", "--name-only", tip); got != "a.txt\nthree.txt" {
		t.Fatalf("tree after the revert = %q", got)
	}
	r.reverted(0, first, tip)
	r.reverted(1, second, tip)
	if c := r.card(r.leaves[2].ID).Card; c.Status != board.StatusDone || c.Lane.RevertedSHA != "" {
		t.Fatalf("the independent subtask = %+v, lane %+v", c, c.Lane)
	}
}

// A running attempt that started on top of what a revert takes along
// refuses the revert at once: the owner Stops it first.
func TestRevertRefusedWhileADependentRuns(t *testing.T) {
	r := newLaneRun(t, "true", "First", "Second")
	r.link(0, 1)
	r.landWith(0, "one.txt", "one\n")
	r.start(1)
	first, second := r.leaves[0], r.leaves[1]
	if p := r.preview(first.ID); !slices.Equal(p.Cards, []string{first.ID}) || !slices.Equal(p.Running, []string{second.ID}) {
		t.Fatalf("preview = %+v", p)
	}
	tip := r.tip()
	reply := r.refused(http.MethodPost, "/api/board/cards/"+first.ID+"/revert", revertBody(nil, first.ID), http.StatusConflict, string(board.CodeRevertRunning))
	if refs := string(reply["refs"]); refs != fmt.Sprintf(`["#%d"]`, second.Seq) || !strings.Contains(string(reply["error"]), "Stop") {
		t.Fatalf("refusal = %s refs %s", reply["error"], refs)
	}
	if r.tip() != tip || r.card(first.ID).Card.Status != board.StatusDone {
		t.Fatal("a refused revert changed something")
	}
	r.call(http.MethodPost, "/api/board/cards/"+second.ID+"/release", `{}`, http.StatusOK, nil)
	if end := r.revert(first.ID, revertBody(nil, first.ID)); end.Status != jobDone {
		t.Fatalf("revert after the stop = %+v", end)
	}
}

// A revert that conflicts with a later landing fails with the files and
// that landing's card, and changes nothing; including the card reverts both.
func TestRevertConflictNamesLaterCards(t *testing.T) {
	r := newLaneRun(t, "true", "Add x", "Change x")
	r.landWith(0, "x.txt", "one\n")
	r.landWith(1, "x.txt", "two\n")
	first, second := r.leaves[0], r.leaves[1]
	p := r.preview(first.ID)
	if p.Conflict == nil || p.Conflict.Code != codeRevertConflict || !slices.Equal(p.Conflict.Refs, []string{fmt.Sprintf("#%d", second.Seq)}) ||
		!strings.Contains(p.Conflict.Message, "x.txt") {
		t.Fatalf("preview conflict = %+v", p.Conflict)
	}
	tip := r.tip()
	end := r.revert(first.ID, revertBody(nil, first.ID))
	if end.Status != jobFailed || !strings.Contains(end.Error, "x.txt") || !strings.Contains(end.Error, fmt.Sprintf("#%d", second.Seq)) {
		t.Fatalf("conflicting revert job = %+v", end)
	}
	if d := r.card(first.ID); r.tip() != tip || d.Card.Status != board.StatusDone || d.Card.Paused != "" {
		t.Fatalf("a failed revert changed the card %+v or the tip", d.Card)
	}
	if p := r.preview(first.ID, second.ID); p.Conflict != nil || !slices.Equal(p.Cards, []string{first.ID, second.ID}) {
		t.Fatalf("preview with the later card = %+v", p)
	}
	if end := r.revert(first.ID, revertBody([]string{second.ID}, first.ID, second.ID)); end.Status != jobDone {
		t.Fatalf("revert with the later card = %+v", end)
	}
	if got := gitOutput(t, r.repo, "ls-tree", "--name-only", r.tip()); got != "a.txt" {
		t.Fatalf("tree = %q", got)
	}
}

// A revert whose cards differ from the ones the preview showed is refused
// at once.
func TestRevertExpectMismatch(t *testing.T) {
	r := newLaneRun(t, "true", "First", "Second")
	r.link(0, 1)
	r.landWith(0, "one.txt", "one\n")
	r.landWith(1, "two.txt", "two\n")
	first, second := r.leaves[0], r.leaves[1]
	tip := r.tip()
	target := "/api/board/cards/" + first.ID + "/revert"
	reply := r.refused(http.MethodPost, target, revertBody(nil, first.ID), http.StatusConflict, string(board.CodeStale))
	if refs := string(reply["refs"]); refs != fmt.Sprintf(`["#%d"]`, second.Seq) {
		t.Fatalf("stale refs = %s", refs)
	}
	r.refused(http.MethodPost, target, `{}`, http.StatusConflict, string(board.CodeStale))
	if r.tip() != tip {
		t.Fatal("a refused revert moved the integration branch")
	}
}

// A revert of a story reverts every subtask that landed under it.
func TestRevertOnAStorySeedsItsLandedSubtasks(t *testing.T) {
	r := newLaneRun(t, "true", "First", "Second", "Later")
	first := r.landWith(0, "one.txt", "one\n")
	second := r.landWith(1, "two.txt", "two\n")
	story := *r.leaves[0].ParentID
	if p := r.preview(story); !slices.Equal(p.Cards, []string{r.leaves[0].ID, r.leaves[1].ID}) || len(p.Landings) != 2 {
		t.Fatalf("story preview = %+v", p)
	}
	if end := r.revert(story, revertBody(nil, r.leaves[0].ID, r.leaves[1].ID)); end.Status != jobDone {
		t.Fatalf("story revert = %+v", end)
	}
	tip := r.tip()
	r.reverted(0, first, tip)
	r.reverted(1, second, tip)
	if c := r.card(r.leaves[2].ID).Card; c.Lane != nil || c.Paused != "" {
		t.Fatalf("the subtask that never started = %+v", c)
	}
}

// A revert writes the store before the integration branch moves: a lost
// compare-and-swap undoes the store, and a revert a crash interrupted is
// finished when the planner opens, or undone when the branch moved on.
func TestRevertWritesTheStoreFirst(t *testing.T) {
	t.Run("lost compare-and-swap", func(t *testing.T) {
		r := newLaneRun(t, "true", "Leaf")
		landed := r.landWith(0, "b.txt", "b\n")
		leaf := r.leaves[0]
		var moved string
		r.m.landHook = func(stage string) {
			if stage != revertIntent {
				return
			}
			if c := r.card(leaf.ID).Card; c.Status != board.StatusTodo || c.Paused != board.PausedOwner || c.Lane.RevertedSHA == "" || r.tip() != landed {
				t.Errorf("before the branch moves: %+v, lane %+v, tip %s", c, c.Lane, r.tip())
			}
			moved = gitOutput(t, r.repo, "commit-tree", landed+"^{tree}", "-p", landed, "-m", "by hand")
			gitIn(t, r.repo, "update-ref", "refs/heads/"+r.integ(), moved, landed)
		}
		end := r.revert(leaf.ID, revertBody(nil, leaf.ID))
		if end.Status != jobFailed || !strings.Contains(end.Error, "revert not applied") {
			t.Fatalf("revert job = %+v", end)
		}
		d := r.card(leaf.ID)
		if r.tip() != moved || d.Card.Status != board.StatusDone || d.Card.Paused != "" || d.Card.Lane.RevertedSHA != "" || d.Holds[0].RevertedSHA != "" ||
			!slices.ContainsFunc(comments(d), func(c string) bool { return strings.HasPrefix(c, "uam: revert not applied: ") }) {
			t.Fatalf("after the lost compare-and-swap: tip %s, card %+v, lane %+v, comments %q", r.tip(), d.Card, d.Card.Lane, comments(d))
		}
	})
	for _, tc := range []struct {
		name    string
		applies bool
	}{{"ref not moved", true}, {"diverged", false}} {
		t.Run(tc.name, func(t *testing.T) {
			r := newLaneRun(t, "true", "Leaf")
			landed := r.landWith(0, "b.txt", "b\n")
			leaf := r.leaves[0]
			ctx := context.Background()
			repo, err := openLanes(ctx, r.project, r.repo)
			if err != nil {
				t.Fatal(err)
			}
			commit, err := repo.revertChain(ctx, landed, []revertItem{{sha: landed, seq: leaf.Seq, title: leaf.Title}})
			if err != nil {
				t.Fatal(err)
			}
			r.store(func(ctx context.Context, st *board.Store) error {
				_, err := st.Revert(ctx, board.Owner(""), leaf.ID, nil, []string{leaf.ID}, commit, "it broke the build")
				return err
			})
			other := landed
			if !tc.applies {
				other = gitOutput(t, r.repo, "commit-tree", landed+"^{tree}", "-p", landed, "-m", "by hand")
				gitIn(t, r.repo, "update-ref", "refs/heads/"+r.integ(), other, landed)
			}
			r.m.closeBoard()
			if err := r.m.openBoard(ctx); err != nil {
				t.Fatal(err)
			}
			if tc.applies {
				if r.tip() != commit {
					t.Fatalf("after boot the tip is %s, want the revert %s", r.tip(), commit)
				}
				r.reverted(0, landed, commit)
				return
			}
			d := r.card(leaf.ID)
			if r.tip() != other || d.Card.Status != board.StatusDone || d.Card.Paused != "" || d.Card.Lane.RevertedSHA != "" ||
				!slices.ContainsFunc(comments(d), func(c string) bool { return strings.HasPrefix(c, "uam: revert not applied: ") }) {
				t.Fatalf("after boot, diverged: tip %s, card %+v, lane %+v, comments %q", r.tip(), d.Card, d.Card.Lane, comments(d))
			}
		})
	}
}

// A revert that went through stays as it is when the planner opens again,
// whatever became of the integration branch since: deleted once merged, or
// reset by hand to before the reverted landing. uam cannot tell whether
// such a branch ever took the revert, so it neither withdraws the revert
// nor moves the branch.
func TestRecoveryKeepsAFinishedRevert(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(r *laneRun, landed string) string
	}{
		{"branch deleted", func(r *laneRun, _ string) string {
			gitIn(r.t, r.repo, "update-ref", "-d", "refs/heads/"+r.integ())
			return ""
		}},
		{"branch reset by hand", func(r *laneRun, landed string) string {
			before := gitOutput(r.t, r.repo, "rev-parse", landed+"^")
			gitIn(r.t, r.repo, "update-ref", "refs/heads/"+r.integ(), before)
			return before
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newLaneRun(t, "true", "Add b")
			landed := r.landWith(0, "b.txt", "b\n")
			if end := r.revert(r.leaves[0].ID, revertBody(nil, r.leaves[0].ID)); end.Status != jobDone {
				t.Fatalf("revert job = %+v", end)
			}
			commit := r.tip()
			want := tc.edit(r, landed)
			r.reboot()
			r.reverted(0, landed, commit)
			if want == "" {
				if gitTry(t, r.repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+r.integ()) == nil {
					t.Fatal("boot brought the deleted integration branch back")
				}
			} else if r.tip() != want {
				t.Fatalf("after boot the integration branch is at %s, want %s", r.tip(), want)
			}
			if slices.ContainsFunc(comments(r.card(r.leaves[0].ID)), func(c string) bool { return strings.HasPrefix(c, "uam: revert not applied") }) {
				t.Fatal("boot withdrew a revert that went through")
			}
		})
	}
}

// A reverted subtask waits, paused, until the owner resumes it; its next
// attempt starts from the integration tip without the reverted change, and
// lands again.
func TestRevertedSubtaskWaitsForResumeThenRerunsWithoutTheChange(t *testing.T) {
	r := newLaneRun(t, "true", "Add b")
	r.landWith(0, "b.txt", "b\n")
	leaf := r.leaves[0]
	if end := r.revert(leaf.ID, revertBody(nil, leaf.ID)); end.Status != jobDone {
		t.Fatalf("revert job = %+v", end)
	}
	r.refused(http.MethodPost, "/api/board/cards/"+leaf.ID+"/launch", `{}`, http.StatusConflict, string(board.CodeNotReady))
	r.call(http.MethodPatch, "/api/board/cards/"+leaf.ID, `{"paused":false}`, http.StatusOK, nil)
	task, l := r.start(0)
	if !gone(filepath.Join(l.dir, "b.txt")) {
		t.Fatal("the new attempt starts with the reverted change")
	}
	if c := r.card(leaf.ID).Card; *c.Lane != (BoardLane{Branch: l.branch}) {
		t.Fatalf("the new attempt's lane = %+v", c.Lane)
	}
	commitFile(t, l.dir, "b.txt", "b again\n")
	r.landed(task, 0)
}

// Revert and Retry merge run as jobs: 202 with the job's ID, then
// board_job frames.
func TestRevertAndMergeAreJobs(t *testing.T) {
	r := newLaneRun(t, "true", "Leaf")
	sub := r.subscribe()
	r.landWith(0, "b.txt", "b\n")
	leaf := r.leaves[0]

	// The epic is not finished: the owner merges what landed early.
	var job struct {
		JobID string `json:"job_id"`
	}
	r.call(http.MethodPost, "/api/board/projects/"+r.project+"/merge", `{}`, http.StatusAccepted, &job)
	if end := r.nextMerge(sub); end.JobID != job.JobID || end.Status != jobDone || end.CardID != "" {
		t.Fatalf("merge job = %+v, want job %s", end, job.JobID)
	}
	if gone(filepath.Join(r.repo, "b.txt")) {
		t.Fatal("the early merge did not reach main")
	}
	// Merging again finds main has it all, and moves nothing.
	main := gitOutput(t, r.repo, "rev-parse", "main")
	r.call(http.MethodPost, "/api/board/projects/"+r.project+"/merge", `{}`, http.StatusAccepted, &job)
	if end := r.nextMerge(sub); end.JobID != job.JobID || end.Status != jobDone || gitOutput(t, r.repo, "rev-parse", "main") != main {
		t.Fatalf("merge with nothing left = %+v", end)
	}

	if end := r.revert(leaf.ID, revertBody(nil, leaf.ID)); end.Status != jobDone || end.CardID != leaf.ID || end.Kind != jobRevert {
		t.Fatalf("revert job = %+v", end)
	}
	if end := r.nextMerge(sub); end.Status != jobDone || !gone(filepath.Join(r.repo, "b.txt")) {
		t.Fatalf("merge of the revert = %+v", end)
	}
}

// The owner's move back to To do is refused on a landed subtask, which is
// reverted instead, or reopened without reverting its code: To do, paused,
// its commit kept on the integration branch, which does not move.
func TestBackToTodoRefusedOnLanded(t *testing.T) {
	r := newLaneRun(t, "true", "Add b", "Then c")
	r.link(0, 1)
	landed := r.landWith(0, "b.txt", "b\n")
	r.landWith(1, "c.txt", "c\n")
	leaf, status := r.leaves[0], "/api/board/cards/"+r.leaves[0].ID+"/status"
	tip := r.tip()
	reply := r.refused(http.MethodPost, status, `{"status":"todo"}`, http.StatusConflict, string(board.CodeInvalid))
	if !strings.Contains(string(reply["error"]), fmt.Sprintf("Revert #%d instead", leaf.Seq)) {
		t.Fatalf("refusal = %s", reply["error"])
	}
	r.refused(http.MethodPost, status, `{"status":"done","keep_code":true,"comment":"x"}`, http.StatusBadRequest, string(board.CodeInvalid))
	var c BoardCard
	r.call(http.MethodPost, status, `{"status":"todo","keep_code":true,"comment":"fix it by hand"}`, http.StatusOK, &c)
	if c.Status != board.StatusTodo || c.Paused != board.PausedOwner || c.Lane == nil || c.Lane.LandedSHA != landed || c.Lane.RevertedSHA != "" {
		t.Fatalf("reopened = %+v, lane %+v", c, c.Lane)
	}
	d := r.card(leaf.ID)
	if want := fmt.Sprintf("uam: reopened without reverting %s; the code stays on %s", shortSHA(landed), r.integ()); !slices.Contains(comments(d), want) ||
		!slices.Contains(comments(d), "owner: fix it by hand") {
		t.Fatalf("comments = %q, want %q", comments(d), want)
	}
	if r.tip() != tip || r.card(r.leaves[1].ID).Card.Status != board.StatusDone {
		t.Fatal("reopening moved the integration branch or the dependent")
	}
}
