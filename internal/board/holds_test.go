package board

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLaunch(t *testing.T) {
	f := newFixture(t)
	epic, story, one, two := f.tree()
	held := f.launch(one.ID, "task-1")
	if held.Status != StatusDoing || held.HeldBy != "task-1" || !held.Confirmed() || held.PinnedSHA != "head-1" {
		t.Fatalf("launched %+v", held)
	}
	wantStatus(t, f.card(story.ID), StatusDoing)
	holds := f.detail(one.ID).Holds
	if len(holds) != 1 || holds[0].Attempt != 1 || holds[0].TaskID != "task-1" || holds[0].EndedAt != nil ||
		holds[0].Baseline.Head != "base" || !slices.Equal(holds[0].Baseline.Dirty, []string{"x.go"}) || holds[0].Baseline.Blobs["x.go"] != "b10b" {
		t.Fatalf("holds = %+v", holds)
	}
	// The Task is scoped to the subtask's parent.
	f.create(Agent("task-1", ""), story.ID, KindSubtask, "Sibling")
	_, err := f.s.Create(f.ctx, Agent("task-1", ""), NewCard{ProjectID: proj, Kind: KindStory, ParentID: epic.ID, Title: "x"})
	wantCode(t, err, CodeForbidden)
	_, err = f.s.Launch(f.ctx, owner, one.ID, "task-9", Baseline{}, false)
	wantCode(t, err, CodeInvalid)
	_, err = f.s.Launch(f.ctx, owner, two.ID, " ", Baseline{}, false)
	wantCode(t, err, CodeInvalid)
	// "Do whole story" holds the first pending confirmed subtask, blocked ones last.
	_, err = f.s.Edit(f.ctx, owner, two.ID, Patch{Blocked: ptr(true)})
	f.must(err)
	three := f.create(owner, story.ID, KindSubtask, "Three")
	pending, err := f.s.PendingLeaves(f.ctx, story.ID)
	f.must(err)
	if len(pending) != 2 || pending[0].ID != three.ID || pending[1].ID != two.ID {
		t.Fatalf("pending = %+v, want three then the blocked two", pending)
	}
	_, err = f.s.PendingLeaves(f.ctx, one.ID)
	wantCode(t, err, CodeInvalid)
	got, err := f.s.Launch(f.ctx, owner, epic.ID, "task-2", Baseline{Head: "h2"}, false)
	f.must(err)
	if got.ID != three.ID || got.HeldBy != "task-2" {
		t.Fatalf("whole-epic launch held %+v", got)
	}
	f.create(Agent("task-2", ""), epic.ID, KindStory, "Scoped to the epic")
	got, err = f.s.Launch(f.ctx, owner, story.ID, "task-3", Baseline{}, false)
	f.must(err)
	if got.ID != two.ID {
		t.Fatalf("launch held %s, want the blocked two last", got.ID)
	}
	_, err = f.s.Launch(f.ctx, owner, story.ID, "task-4", Baseline{}, false)
	wantCode(t, err, CodeInvalid)
	// A launch that would confirm proposals is refused unless it says to
	// confirm them, naming the subtask and then its unconfirmed parents; the
	// check before the Task exists says the same. Confirmed, it confirms and
	// pins them in the write that starts the hold, so the sweep can no
	// longer expire the story.
	f.must(f.s.StartPlanning(f.ctx, owner, epic.ID, "planner"))
	s2 := f.create(Agent("planner", ""), epic.ID, KindStory, "Proposed")
	leaf := f.create(Agent("planner", ""), s2.ID, KindSubtask, "Proposed leaf")
	_, err = f.s.Launch(f.ctx, Owner("head-5"), leaf.ID, "task-5", Baseline{}, false)
	wantUnconfirmed(t, err, leaf.ref(), s2.ref())
	wantUnconfirmed(t, f.s.CheckLaunch(f.ctx, owner, leaf.ID, false), leaf.ref(), s2.ref())
	f.must(f.s.CheckLaunch(f.ctx, owner, leaf.ID, true))
	if c := f.card(leaf.ID); c.Confirmed() || c.HeldBy != "" || f.card(s2.ID).Confirmed() {
		t.Fatalf("a refused launch wrote %+v", c)
	}
	got, err = f.s.Launch(f.ctx, Owner("head-5"), leaf.ID, "task-5", Baseline{}, true)
	f.must(err)
	if story := f.card(s2.ID); !got.Confirmed() || got.PinnedSHA != "head-5" || !story.Confirmed() || story.PinnedSHA != "head-5" {
		t.Fatalf("launched %+v under %+v", got, story)
	}
	f.clock.advance(2 * ExpiryWindow)
	_, err = f.s.Sweep(f.ctx)
	f.must(err)
	wantStatus(t, f.card(s2.ID), StatusDoing)
	wantCode(t, f.s.StartPlanning(f.ctx, owner, leaf.ID, "planner"), CodeInvalid)
	wantCode(t, f.s.StartPlanning(f.ctx, owner, epic.ID, ""), CodeInvalid)
	wantCode(t, f.s.StartPlanning(f.ctx, owner, "#99", "p"), CodeNotFound)
}

// A hold stored before baselines had blob names still reads, with none.
func TestHoldFromBeforeBaselineBlobs(t *testing.T) {
	f := newFixture(t)
	_, _, one, _ := f.tree()
	f.launch(one.ID, "task-1")
	for _, col := range []string{"branch", "landed_sha", "reverted_sha", "waited_on"} {
		f.raw(`ALTER TABLE holds DROP COLUMN ` + col)
	}
	f.raw(`ALTER TABLE project_settings DROP COLUMN base_ref`)
	f.raw(`ALTER TABLE project_settings DROP COLUMN accept_parallel`)
	f.raw(`ALTER TABLE holds DROP COLUMN baseline_blobs`)
	f.raw(`DROP TABLE import_refs`)
	f.raw(`ALTER TABLE requests DROP COLUMN decided_by`)
	f.raw(`ALTER TABLE cards DROP COLUMN paused`)
	f.raw(`DROP TABLE runs`)
	f.raw(`UPDATE meta SET v = '1' WHERE k = 'schema_version'`)
	f.must(f.s.Close())
	s, err := Open(f.path, Options{})
	f.must(err)
	defer func() { _ = s.Close() }()
	d, err := s.Detail(f.ctx, one.ID)
	f.must(err)
	if len(d.Holds) != 1 || len(d.Holds[0].Baseline.Blobs) != 0 || !slices.Equal(d.Holds[0].Baseline.Dirty, []string{"x.go"}) {
		t.Fatalf("holds = %+v", d.Holds)
	}
}

func TestClaim(t *testing.T) {
	f := newFixture(t)
	epic, story, one, two := f.tree()
	f.launch(one.ID, "task-1")
	agent := Agent("task-1", "sub")
	// One hold without a pending request at a time.
	_, err := f.s.Claim(f.ctx, agent, two.ID, Baseline{})
	wantCode(t, err, CodeLimit)
	f.done(one.ID, agent)
	got, err := f.s.Claim(f.ctx, agent, two.ID, Baseline{Head: "h", Dirty: nil})
	f.must(err)
	if got.Status != StatusDoing || got.HeldBy != "task-1" || f.detail(two.ID).Holds[0].Attempt != 1 {
		t.Fatalf("claimed %+v", got)
	}
	_, err = f.s.Claim(f.ctx, agent, story.ID, Baseline{})
	wantCode(t, err, CodeInvalid)
	outside := f.create(owner, epic.ID, KindSubtask, "Outside")
	_, err = f.s.Claim(f.ctx, agent, outside.ID, Baseline{})
	wantCode(t, err, CodeForbidden)
	f.must(f.s.StartPlanning(f.ctx, owner, epic.ID, "planner"))
	_, err = f.s.Claim(f.ctx, Agent("planner", ""), outside.ID, Baseline{})
	wantCode(t, err, CodeForbidden)
	_, err = f.s.Claim(f.ctx, Agent("task-9", ""), outside.ID, Baseline{})
	wantCode(t, err, CodeForbidden)
	_, err = f.s.Claim(f.ctx, agent, one.ID, Baseline{})
	wantCode(t, err, CodeInvalid) // already doing
}

func TestReleaseHold(t *testing.T) {
	f := newFixture(t)
	_, story, one, two := f.tree()
	f.launch(one.ID, "task-1")
	agent := Agent("task-1", "")
	got, err := f.s.ReleaseHold(f.ctx, owner, one.ID, ReleaseOwner, "  pausing  ")
	f.must(err)
	d := f.detail(one.ID)
	if got.Status != StatusTodo || got.HeldBy != "" || d.Holds[0].EndReason != ReleaseOwner || d.Holds[0].EndedAt == nil ||
		!hasComment(f.comments(one.ID), "owner: pausing") {
		t.Fatalf("released %+v, holds %+v", got, d.Holds)
	}
	_, err = f.s.ReleaseHold(f.ctx, owner, one.ID, ReleaseOwner, "")
	wantCode(t, err, CodeInvalid) // not doing
	_, err = f.s.ReleaseHold(f.ctx, owner, one.ID, ReleaseEnded, "")
	wantCode(t, err, CodeInvalid)
	_, err = f.s.ReleaseHold(f.ctx, owner, one.ID, ReleaseSettled, strings.Repeat("x", maxTextBytes+1))
	wantCode(t, err, CodeInvalid)
	f.launch(two.ID, "task-2")
	if _, err := f.s.ReleaseHold(f.ctx, owner, two.ID, ReleaseSettled, ""); err != nil {
		t.Fatal(err)
	}
	// Releasing an unconfirmed subtask, held from before claims needed
	// confirmed cards, re-arms its expiry.
	f.launch(one.ID, "task-1")
	f.done(one.ID, agent)
	mine := f.create(agent, story.ID, KindSubtask, "Mine")
	_, err = f.s.Claim(f.ctx, agent, mine.ID, Baseline{})
	wantUnconfirmed(t, err, mine.ref())
	f.legacyClaim(agent, mine.ID)
	f.clock.advance(10 * 24 * time.Hour)
	got, err = f.s.ReleaseHold(f.ctx, owner, mine.ID, ReleaseOwner, "")
	f.must(err)
	if got.Confirmed() || !got.ExpiresAt.Equal(f.clock.Now().Add(ExpiryWindow)) {
		t.Fatalf("expiry after release = %v, want %v", got.ExpiresAt, f.clock.Now().Add(ExpiryWindow))
	}
}

// Test plan 6 and 7: reconciliation releases the holds of Archived, deleted
// or absent Tasks, and changes nothing for Active and Settled ones.
func TestReconcile(t *testing.T) {
	f := newFixture(t)
	_, story, one, two := f.tree()
	three := f.create(owner, story.ID, KindSubtask, "Three")
	four := f.create(owner, story.ID, KindSubtask, "Four")
	f.launch(one.ID, "active")
	f.launch(two.ID, "settled")
	f.launch(three.ID, "archived")
	f.launch(four.ID, "deleted")
	tasks := map[string]Stage{"active": StageActive, "settled": StageSettled, "archived": StageArchived}
	n, err := f.s.Reconcile(f.ctx, tasks, f.asOf(), map[string][]string{proj: {"b.go", "a.go"}})
	f.must(err)
	if n != 2 {
		t.Fatalf("released %d holds, want 2", n)
	}
	for _, c := range []Card{three, four} {
		got := f.card(c.ID)
		if got.Status != StatusTodo || got.HeldBy != "" || !hasComment(f.comments(c.ID), "uam: attempt #1 ended, uncommitted: a.go, b.go") {
			t.Fatalf("%s after reconcile: %+v %v", c.Title, got, f.comments(c.ID))
		}
		if f.detail(c.ID).Holds[0].EndReason != ReleaseEnded {
			t.Fatal("wrong end reason")
		}
	}
	for _, c := range []Card{one, two} {
		if got := f.card(c.ID); got.HeldBy == "" {
			t.Fatalf("%s lost its hold", c.Title)
		}
	}
	// A restart with the same Tasks writes nothing.
	before := f.revision()
	n, err = f.s.Reconcile(f.ctx, tasks, f.asOf(), nil)
	if err != nil || n != 0 || f.revision() != before || len(f.comments(one.ID)) != 0 {
		t.Fatalf("second reconcile: %d, %v, revision %d → %d", n, err, before, f.revision())
	}
	// Later attempts are numbered; the uncommitted list is optional.
	f.launch(three.ID, "gone")
	if n, err = f.s.Reconcile(f.ctx, tasks, f.asOf(), map[string][]string{proj: nil}); err != nil || n != 1 {
		t.Fatalf("reconcile = %d, %v", n, err)
	}
	f.launch(three.ID, "gone-too")
	if _, err = f.s.Reconcile(f.ctx, tasks, f.asOf(), nil); err != nil {
		t.Fatal(err)
	}
	comments := f.comments(three.ID)
	if !hasComment(comments, "attempt #2 ended, uncommitted: none") || comments[len(comments)-1] != "uam: attempt #3 ended" {
		t.Fatalf("comments = %v", comments)
	}
}

func TestEndedHolds(t *testing.T) {
	held := []heldLeaf{{cardID: "a", taskID: "t1"}, {cardID: "b", taskID: "t2"}, {cardID: "c", taskID: "t3"}, {cardID: "d", taskID: "t4"}}
	got := endedHolds(held, map[string]Stage{"t1": StageActive, "t2": StageSettled, "t3": StageArchived, "t4": "unknown"})
	if len(got) != 2 || got[0].cardID != "c" || got[1].cardID != "d" {
		t.Fatalf("ended = %+v", got)
	}
	if endedHolds(held, map[string]Stage{"t1": StageActive, "t2": StageActive, "t3": StageSettled, "t4": StageActive}) != nil {
		t.Fatal("live Tasks lost holds")
	}
}

// A hold that started at or after the Task snapshot's time is left alone:
// its Task may be newer than the snapshot.
func TestReconcileSkipsHoldsNewerThanSnapshot(t *testing.T) {
	f := newFixture(t)
	_, story, one, two := f.tree()
	three := f.create(owner, story.ID, KindSubtask, "Three")
	f.launch(one.ID, "old")
	f.clock.advance(time.Second)
	asOf := f.clock.Now()
	tasks := map[string]Stage{} // read at asOf: "old" was deleted, the rest didn't exist yet
	f.launch(two.ID, "new")
	f.clock.advance(time.Second)
	f.launch(three.ID, "newer")
	n, err := f.s.Reconcile(f.ctx, tasks, asOf, nil)
	f.must(err)
	if n != 1 || f.card(one.ID).HeldBy != "" || f.card(two.ID).HeldBy != "new" || f.card(three.ID).HeldBy != "newer" {
		t.Fatalf("released %d; holds %q %q %q", n, f.card(one.ID).HeldBy, f.card(two.ID).HeldBy, f.card(three.ID).HeldBy)
	}
}

// Only a pending done, blocked or split request exempts a hold from the
// one-hold cap; a change or cancel request doesn't.
func TestOneHoldCapExemptions(t *testing.T) {
	f := newFixture(t)
	_, story, one, two := f.tree()
	three := f.create(owner, story.ID, KindSubtask, "Three")
	four := f.create(owner, story.ID, KindSubtask, "Four")
	f.launch(one.ID, "w")
	w := Agent("w", "")
	res, err := f.s.Edit(f.ctx, w, one.ID, Patch{Desc: ptr("note")})
	f.must(err)
	if res.Request == nil {
		t.Fatal("expected a change request")
	}
	_, err = f.s.Claim(f.ctx, w, two.ID, Baseline{})
	wantCode(t, err, CodeLimit)
	_, err = f.s.FileRequest(f.ctx, w, one.ID, RequestInput{Kind: RequestCancel, Comment: "drop it"})
	f.must(err)
	_, err = f.s.Claim(f.ctx, w, two.ID, Baseline{})
	wantCode(t, err, CodeLimit)
	_, err = f.s.FileRequest(f.ctx, w, one.ID, RequestInput{Kind: RequestBlocked, Comment: "stuck"})
	f.must(err)
	_, err = f.s.Claim(f.ctx, w, two.ID, Baseline{})
	f.must(err)
	_, err = f.s.Split(f.ctx, w, two.ID, []SplitChild{{Title: "half"}})
	f.must(err)
	_, err = f.s.Claim(f.ctx, w, three.ID, Baseline{})
	f.must(err)
	_, err = f.s.Claim(f.ctx, w, four.ID, Baseline{})
	wantCode(t, err, CodeLimit)
}
