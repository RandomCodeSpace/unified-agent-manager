package board

import (
	"fmt"
	"testing"
)

// Accepting re-runs the rules against the card as it is now, not as it was
// when the request was filed.
func TestAcceptRechecks(t *testing.T) {
	f := newFixture(t)
	epic, story, one, _ := f.tree()
	f.launch(one.ID, "task-1")
	r := f.done(one.ID, Agent("task-1", ""))
	_, err := f.s.Edit(f.ctx, owner, one.ID, Patch{Blocked: ptr(true)})
	f.must(err)
	_, err = f.s.Accept(f.ctx, owner, r.ID, "")
	wantCode(t, err, CodeGuardBlocked)

	// A working Task can't claim a proposal under a proposed story; once
	// the owner confirms the subtask, and with it the story, it can.
	f.must(f.s.StartPlanning(f.ctx, owner, epic.ID, "planner"))
	planner := Agent("planner", "")
	loose := f.create(owner, epic.ID, KindSubtask, "Epic-level")
	f.launch(loose.ID, "worker")
	worker := Agent("worker", "")
	f.done(loose.ID, worker)
	ps := f.create(planner, epic.ID, KindStory, "Proposed story")
	pl := f.create(planner, ps.ID, KindSubtask, "Proposed leaf")
	_, err = f.s.Claim(f.ctx, worker, pl.ID, Baseline{})
	wantUnconfirmed(t, err, pl.ref(), ps.ref())
	if c := f.card(pl.ID); c.HeldBy != "" || c.Status != StatusPlanned {
		t.Fatalf("a refused claim wrote %+v", c)
	}
	_, err = f.s.Confirm(f.ctx, owner, pl.ID)
	f.must(err)
	_, err = f.s.Claim(f.ctx, worker, pl.ID, Baseline{})
	f.must(err)
	done := f.done(pl.ID, worker)
	_, err = f.s.Accept(f.ctx, owner, done.ID, "")
	f.must(err)
	if !f.card(ps.ID).Confirmed() || !f.card(pl.ID).Confirmed() {
		t.Fatal("the subtask or its story is unconfirmed")
	}

	// A container that reached done can no longer be cancelled by request.
	s2 := f.create(owner, epic.ID, KindStory, "Second story")
	s2a := f.create(owner, s2.ID, KindSubtask, "Only")
	cancel, err := f.s.FileRequest(f.ctx, planner, s2.ID, RequestInput{Kind: RequestCancel, Comment: "drop"})
	f.must(err)
	_, err = f.s.SetStatus(f.ctx, owner, s2a.ID, StatusDone, "done", false)
	f.must(err)
	wantStatus(t, f.card(s2.ID), StatusDone)
	_, err = f.s.Accept(f.ctx, owner, cancel.ID, "")
	wantCode(t, err, CodeInvalid)

	// A blocker that was purged, or a link that now closes a cycle.
	x := f.create(owner, story.ID, KindSubtask, "X")
	y := f.create(owner, story.ID, KindSubtask, "Y")
	gone := f.create(owner, story.ID, KindSubtask, "Gone")
	byGone, err := f.s.FileRequest(f.ctx, planner, x.ID, RequestInput{Kind: RequestBlocked, Comment: "a", Blocker: gone.ID})
	f.must(err)
	_, err = f.s.SetStatus(f.ctx, owner, gone.ID, StatusCancelled, "gone", false)
	f.must(err)
	_, err = f.s.Purge(f.ctx, owner, proj)
	f.must(err)
	_, err = f.s.Accept(f.ctx, owner, byGone.ID, "")
	wantCode(t, err, CodeNotFound)
	byY, err := f.s.FileRequest(f.ctx, planner, x.ID, RequestInput{Kind: RequestBlocked, Comment: "b", Blocker: y.ID})
	f.must(err)
	f.must(f.s.Link(f.ctx, owner, x.ID, y.ID))
	_, err = f.s.Accept(f.ctx, owner, byY.ID, "")
	wantCode(t, err, CodeInvalid)

	// A split whose children now repeat a checklist item.
	big := f.create(owner, epic.ID, KindSubtask, "Big")
	f.launch(big.ID, "splitter")
	split, err := f.s.Split(f.ctx, Agent("splitter", ""), big.ID, []SplitChild{{Title: "part"}})
	f.must(err)
	_, err = f.s.Checklist(f.ctx, owner, big.ID, ChecklistEdit{Add: []string{"Part"}})
	wantCode(t, err, CodeInProgress)
	_, err = f.s.Accept(f.ctx, owner, split.Request.ID, "")
	wantCode(t, err, CodeInProgress)
	// Released, the subtask keeps the split request and plans again.
	_, err = f.s.ReleaseHold(f.ctx, owner, big.ID, ReleaseOwner, "")
	f.must(err)
	_, err = f.s.Checklist(f.ctx, owner, big.ID, ChecklistEdit{Add: []string{"Part"}})
	f.must(err)
	_, err = f.s.Accept(f.ctx, owner, split.Request.ID, "")
	wantCode(t, err, CodeDuplicate)

	// A change whose title is now taken.
	f.launch(big.ID, "changer")
	change, err := f.s.Edit(f.ctx, Agent("changer", ""), big.ID, Patch{Title: ptr("Bigger")})
	f.must(err)
	f.create(owner, epic.ID, KindSubtask, "bigger")
	_, err = f.s.ReleaseHold(f.ctx, owner, big.ID, ReleaseOwner, "")
	f.must(err)
	_, err = f.s.Accept(f.ctx, owner, change.Request.ID, "")
	wantCode(t, err, CodeDuplicate)
}

func TestOwnerAndAgentEdgeCases(t *testing.T) {
	f := newFixture(t)
	epic, story, one, two := f.tree()
	_, err := f.s.SetStatus(f.ctx, owner, one.ID, StatusDone, "done", false)
	f.must(err)
	_, err = f.s.SetStatus(f.ctx, owner, one.ID, StatusDone, "again", false)
	wantCode(t, err, CodeInvalid)
	_, err = f.s.Edit(f.ctx, owner, two.ID, Patch{Title: ptr(" ")})
	wantCode(t, err, CodeInvalid)
	f.must(f.s.StartPlanning(f.ctx, owner, epic.ID, "planner"))
	planner := Agent("planner", "")
	ps := f.create(planner, epic.ID, KindStory, "Proposed")
	pl := f.create(planner, ps.ID, KindSubtask, "Proposed leaf")
	un := f.unassigned("Loose")
	_, err = f.s.CheckFinishable(f.ctx, owner, un)
	wantCode(t, err, CodeReadOnly)
	// Moving its own unconfirmed card counts against the Task's cap on the
	// new container.
	for i := range CapUnconfirmed {
		f.create(planner, story.ID, KindSubtask, fmt.Sprintf("filler %d", i))
	}
	_, err = f.s.Edit(f.ctx, planner, pl.ID, Patch{ParentID: &story.ID})
	wantCode(t, err, CodeLimit)
	// Split caps.
	var many []SplitChild
	for i := range CapUnconfirmed + 1 {
		many = append(many, SplitChild{Title: fmt.Sprintf("child %d", i)})
	}
	mine := f.create(planner, epic.ID, KindSubtask, "Mine")
	_, err = f.s.Split(f.ctx, planner, mine.ID, many)
	wantCode(t, err, CodeLimit)
	held := f.create(owner, epic.ID, KindSubtask, "Held")
	f.launch(held.ID, "worker")
	for i := range CapCreated {
		many = append(many, SplitChild{Title: fmt.Sprintf("more %d", i)})
	}
	_, err = f.s.Split(f.ctx, Agent("worker", ""), held.ID, many)
	wantCode(t, err, CodeLimit)
	// Dismissing and sweeping skip what is already cancelled.
	extra := f.create(planner, ps.ID, KindSubtask, "Extra")
	_, err = f.s.Dismiss(f.ctx, owner, extra.ID)
	f.must(err)
	f.clock.advance(ExpiryWindow)
	if n, err := f.s.Sweep(f.ctx); err != nil || n < 2 {
		t.Fatalf("sweep = %d, %v", n, err)
	}
	if c := f.card(extra.ID); c.CascadeID == f.card(ps.ID).CascadeID {
		t.Fatal("the sweep re-cancelled a dismissed card")
	}
	_, err = f.s.Dismiss(f.ctx, owner, ps.ID)
	wantCode(t, err, CodeInvalid)
}

func TestLinkCycleThroughDiamond(t *testing.T) {
	f := newFixture(t)
	var ids []string
	for _, title := range []string{"A", "B", "C", "D"} {
		ids = append(ids, f.create(owner, "", KindSubtask, title).ID)
	}
	a, b, c, d := ids[0], ids[1], ids[2], ids[3]
	for _, l := range [][2]string{{a, b}, {a, c}, {b, d}, {c, d}} {
		f.must(f.s.Link(f.ctx, owner, l[0], l[1]))
	}
	wantCode(t, f.s.Link(f.ctx, owner, d, a), CodeInvalid)
	f.must(f.s.Link(f.ctx, owner, b, c))
}
