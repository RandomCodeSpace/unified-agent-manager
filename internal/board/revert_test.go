package board

import (
	"fmt"
	"slices"
	"testing"
)

// landed lands task's lane attempt at the subtask ref as sha, as a lane's
// done claim does once its commit is on the integration branch.
func (f *fixture) landed(ref, task, sha string) {
	f.t.Helper()
	f.lane(ref, task)
	id := f.landDone(ref, task).Request.ID
	f.must(f.s.MarkLanding(f.ctx, id, sha))
	if _, err := f.s.AcceptLanded(f.ctx, id, sha, "uam-plan-p1", DecidedByUAM, ""); err != nil {
		f.t.Fatal(err)
	}
}

func idsOf(cards []Card) []string {
	out := make([]string, len(cards))
	for i, c := range cards {
		out[i] = c.ID
	}
	return out
}

// A revert takes along the landed subtasks that started on top of what it
// reverts, as each recorded when it started: links changed after they
// landed change nothing.
func TestRevertClosureUsesStartSnapshots(t *testing.T) {
	f := newFixture(t)
	f.acceptCmd("go test ./...")
	epic := f.create(owner, "", KindEpic, "Epic")
	a := f.create(owner, epic.ID, KindStory, "A")
	a1 := f.create(owner, a.ID, KindSubtask, "a1")
	b := f.create(owner, epic.ID, KindStory, "B")
	b1 := f.create(owner, b.ID, KindSubtask, "b1")
	b2 := f.create(owner, b.ID, KindSubtask, "b2")
	b3 := f.create(owner, b.ID, KindSubtask, "b3")
	c := f.create(owner, epic.ID, KindStory, "C")
	c1 := f.create(owner, c.ID, KindSubtask, "c1")
	f.must(f.s.Link(f.ctx, owner, a.ID, b.ID))
	f.must(f.s.Link(f.ctx, owner, b1.ID, b2.ID))
	f.approveRun(epic.ID, laneRun)
	sha := func(i int) string { return fmt.Sprintf("%040x", i) }
	f.landed(a1.ID, "run-a1", sha(1))
	f.landed(c1.ID, "run-c1", sha(2))
	f.landed(b1.ID, "run-b1", sha(3))
	f.landed(b2.ID, "run-b2", sha(4))
	f.lane(b3.ID, "run-b3")

	check := func(name string, ref string, include []string, cards []Card, running ...Card) {
		t.Helper()
		got, err := f.s.RevertClosure(f.ctx, ref, include)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !slices.Equal(idsOf(got.Cards), idsOf(cards)) || !slices.Equal(idsOf(got.Running), idsOf(running)) {
			t.Fatalf("%s: closure %v running %v, want %v running %v", name, refsOf(got.Cards...), refsOf(got.Running...), refsOf(cards...), refsOf(running...))
		}
	}
	check("a1", a1.ID, nil, []Card{a1, b1, b2}, b3)
	got, err := f.s.RevertClosure(f.ctx, a1.ID, nil)
	f.must(err)
	want := []Landing{{a1.ID, a1.Seq, "a1", sha(1)}, {b1.ID, b1.Seq, "b1", sha(3)}, {b2.ID, b2.Seq, "b2", sha(4)}}
	if !slices.Equal(got.Landings, want) {
		t.Fatalf("landings = %+v, want %+v", got.Landings, want)
	}
	// A story seeds its landed subtasks; include adds seeds, with what
	// started on top of them.
	check("story B", b.ID, nil, []Card{b1, b2})
	check("c1 with a1", c1.ID, []string{a1.ref()}, []Card{a1, b1, b2, c1}, b3)

	// Links changed after the subtasks started change nothing.
	f.must(f.s.Unlink(f.ctx, owner, a.ID, b.ID))
	f.must(f.s.Link(f.ctx, owner, c.ID, a.ID))
	check("a1 unlinked", a1.ID, nil, []Card{a1, b1, b2}, b3)
	check("c1 relinked", c1.ID, nil, []Card{c1})

	// Only landed work is reverted.
	for _, ref := range []string{b3.ID, f.create(owner, "", KindStory, "Empty").ID} {
		_, err := f.s.RevertClosure(f.ctx, ref, nil)
		wantCode(t, err, CodeInvalid)
	}
}

// Reopen without reverting code moves a landed subtask back to To do,
// paused by the owner, and keeps its landed commit: the subtasks that
// landed on top of it stay landed, and no running lane may gain a blocker.
func TestReopenKeepsCode(t *testing.T) {
	f := newFixture(t)
	f.acceptCmd("go test ./...")
	epic := f.create(owner, "", KindEpic, "Epic")
	a := f.create(owner, epic.ID, KindStory, "A")
	a1 := f.create(owner, a.ID, KindSubtask, "a1")
	b := f.create(owner, epic.ID, KindStory, "B")
	b1 := f.create(owner, b.ID, KindSubtask, "b1")
	b2 := f.create(owner, b.ID, KindSubtask, "b2")
	f.must(f.s.Link(f.ctx, owner, a.ID, b.ID))
	f.approveRun(epic.ID, laneRun)
	f.landed(a1.ID, "run-a1", landedSHA)
	f.landed(b1.ID, "run-b1", fmt.Sprintf("%040x", 2))
	f.lane(b2.ID, "run-b2")

	_, err := f.s.Reopen(f.ctx, owner, a1.ID, "uam-plan-p1", "")
	wantRefusal(t, err, CodeInProgress, b2.ref())
	_, err = f.s.ReleaseHold(f.ctx, owner, b2.ID, ReleaseOwner, "")
	f.must(err)
	got, err := f.s.Reopen(f.ctx, owner, a1.ID, "uam-plan-p1", "the parser is wrong")
	f.must(err)
	if got.Status != StatusTodo || got.Paused != PausedOwner || got.HeldBy != "" || got.Lane == nil ||
		*got.Lane != (Lane{Branch: "uam-plan-p1-run-a1", LandedSHA: landedSHA}) {
		t.Fatalf("reopened = %+v, lane %+v", got, got.Lane)
	}
	c := f.comments(a1.ID)
	if !hasComment(c, "uam: reopened without reverting 9f3e2a1; the code stays on uam-plan-p1") || !hasComment(c, "owner: the parser is wrong") {
		t.Fatalf("comments = %v", c)
	}
	if b1 := f.card(b1.ID); b1.Status != StatusDone || b1.Lane.LandedSHA == "" || b1.Lane.RevertedSHA != "" {
		t.Fatalf("the dependent = %+v, lane %+v", b1, b1.Lane)
	}
	// Only landed work is reopened this way.
	for _, ref := range []string{a1.ID, b2.ID} {
		_, err := f.s.Reopen(f.ctx, owner, ref, "uam-plan-p1", "")
		wantCode(t, err, CodeInvalid)
	}
	_, err = f.s.Reopen(f.ctx, Agent("run-b1", ""), b1.ID, "uam-plan-p1", "")
	wantCode(t, err, CodeForbidden)
}
