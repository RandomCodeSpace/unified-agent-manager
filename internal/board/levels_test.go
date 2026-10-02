package board

import (
	"errors"
	"fmt"
	"slices"
	"testing"
)

// Test plan 16: links map a DAG per level. Two cards are linked only when
// they are of one kind under one parent: epics, the stories of one epic, the
// subtasks of one story or epic, and root cards of one kind. Any other pair
// is refused, naming the containers to link instead when there are some. An
// agent follows the same rule, and a Task with no scope may link the epics
// it proposed.
func TestLinksJoinOneLevel(t *testing.T) {
	f := newFixture(t)
	a, sa1, la1, la2 := f.tree()
	sa2 := f.create(owner, a.ID, KindStory, "A2")
	la3 := f.create(owner, sa2.ID, KindSubtask, "A2 leaf")
	b := f.create(owner, "", KindEpic, "B")
	sb1 := f.create(owner, b.ID, KindStory, "B1")
	ea := f.create(owner, a.ID, KindSubtask, "Under A")
	eb := f.create(owner, a.ID, KindSubtask, "Also under A")
	r1 := f.create(owner, "", KindSubtask, "Root one")
	r2 := f.create(owner, "", KindSubtask, "Root two")
	rs := f.create(owner, "", KindStory, "Root story")
	rs2 := f.create(owner, "", KindStory, "Root story two")
	for _, pair := range [][2]Card{{b, a}, {sa2, sa1}, {la2, la1}, {eb, ea}, {r2, r1}, {rs2, rs}} {
		f.must(f.s.Link(f.ctx, owner, pair[0].ID, pair[1].ID))
	}
	for _, tc := range []struct {
		blocker, blocked Card
		want             string
	}{
		{sb1, sa1, fmt.Sprintf("%s can't block %s: stories depend only on stories in the same epic; link epics %s and %s instead", sb1.ref(), sa1.ref(), b.ref(), a.ref())},
		{la3, la1, fmt.Sprintf("%s can't block %s: subtasks depend only on subtasks in the same story; link stories %s and %s instead", la3.ref(), la1.ref(), sa2.ref(), sa1.ref())},
		{sa2, la1, fmt.Sprintf("%s can't block %s: subtasks depend only on subtasks in the same story; link stories %s and %s instead", sa2.ref(), la1.ref(), sa2.ref(), sa1.ref())},
		{la1, sa2, fmt.Sprintf("%s can't block %s: stories depend only on stories in the same epic; link stories %s and %s instead", la1.ref(), sa2.ref(), sa1.ref(), sa2.ref())},
		{a, sa1, fmt.Sprintf("%s can't block %s: stories depend only on stories in the same epic", a.ref(), sa1.ref())},
		{sa1, la1, fmt.Sprintf("%s can't block %s: subtasks depend only on subtasks in the same story", sa1.ref(), la1.ref())},
		{ea, la1, fmt.Sprintf("%s can't block %s: subtasks depend only on subtasks in the same story", ea.ref(), la1.ref())},
		{r1, la1, fmt.Sprintf("%s can't block %s: subtasks depend only on subtasks in the same story", r1.ref(), la1.ref())},
		{la1, r1, fmt.Sprintf("%s can't block %s: subtasks at the top level depend only on subtasks at the top level", la1.ref(), r1.ref())},
		{sa1, rs, fmt.Sprintf("%s can't block %s: stories at the top level depend only on stories at the top level", sa1.ref(), rs.ref())},
		{rs, a, fmt.Sprintf("%s can't block %s: epics depend only on epics", rs.ref(), a.ref())},
		{r1, rs, fmt.Sprintf("%s can't block %s: stories at the top level depend only on stories at the top level", r1.ref(), rs.ref())},
	} {
		err := f.s.Link(f.ctx, owner, tc.blocker.ID, tc.blocked.ID)
		wantCode(t, err, CodeInvalid)
		if err.Error() != tc.want {
			t.Errorf("link %s → %s = %q, want %q", tc.blocker.Title, tc.blocked.Title, err, tc.want)
		}
	}
	// Cycles are refused within a level.
	wantCode(t, f.s.Link(f.ctx, owner, a.ID, b.ID), CodeInvalid)
	// A planning Task links within its scope at one level only.
	f.must(f.s.StartPlanning(f.ctx, owner, a.ID, "planner"))
	planner := Agent("planner", "")
	p1 := f.create(planner, sa1.ID, KindSubtask, "P1")
	p2 := f.create(planner, sa1.ID, KindSubtask, "P2")
	f.must(f.s.Link(f.ctx, planner, p1.ID, p2.ID))
	f.must(f.s.Link(f.ctx, planner, la1.ID, p1.ID))
	wantCode(t, f.s.Link(f.ctx, planner, la3.ID, p1.ID), CodeInvalid)
	wantCode(t, f.s.Link(f.ctx, planner, sb1.ID, sa1.ID), CodeInvalid)
	wantCode(t, f.s.Link(f.ctx, planner, a.ID, b.ID), CodeForbidden) // B is outside its scope
	// A Task with no scope proposes epics and links them, to each other and
	// to confirmed epics, but no epic it did not propose.
	roamer := Agent("roamer", "")
	e1 := f.create(roamer, "", KindEpic, "Proposed one")
	e2 := f.create(roamer, "", KindEpic, "Proposed two")
	f.must(f.s.Link(f.ctx, roamer, e1.ID, e2.ID))
	f.must(f.s.Link(f.ctx, roamer, a.ID, e1.ID))
	wantCode(t, f.s.Link(f.ctx, roamer, e2.ID, e1.ID), CodeInvalid) // cycle
	wantCode(t, f.s.Link(f.ctx, roamer, e1.ID, b.ID), CodeForbidden)
	wantCode(t, f.s.Link(f.ctx, roamer, sa1.ID, e1.ID), CodeInvalid)
	wantCode(t, f.s.Link(f.ctx, planner, e1.ID, e2.ID), CodeForbidden) // not its proposal, and outside its scope
	if c := f.card(e2.ID); !slices.Equal(c.BlockedBy, []string{e1.ID}) {
		t.Fatalf("e2 blocked by %v", c.BlockedBy)
	}
	// A blocked request names a blocker at the subtask's level, as a link does.
	_, err := f.s.FileRequest(f.ctx, planner, la1.ID, RequestInput{Kind: RequestBlocked, Comment: "x", Blocker: la3.ID})
	wantCode(t, err, CodeInvalid)
}

// A linked card keeps its level: moving it to another parent, by an edit, a
// change request or its acceptance, and splitting it into a story are
// refused until its links are removed, naming them. A move within its
// parent and a split into siblings are fine.
func TestLinkedCardsKeepTheirLevel(t *testing.T) {
	f := newFixture(t)
	epic, story, one, two := f.tree()
	other := f.create(owner, epic.ID, KindStory, "Other")
	f.must(f.s.Link(f.ctx, owner, two.ID, one.ID))
	_, err := f.s.Edit(f.ctx, owner, one.ID, Patch{ParentID: &other.ID})
	wantCode(t, err, CodeInvalid)
	var e *Error
	if !errors.As(err, &e) || !slices.Equal(e.Refs, []string{two.ref()}) {
		t.Fatalf("move refusal = %v", err)
	}
	want := fmt.Sprintf("%s is linked to %s, and links join only cards of one kind under one parent; moving it changes its parent, so the owner removes those links first", one.ref(), two.ref())
	if err.Error() != want {
		t.Fatalf("move refusal = %q, want %q", err, want)
	}
	_, err = f.s.Edit(f.ctx, owner, two.ID, Patch{Rank: ptr(0)})
	f.must(err)
	f.must(f.s.StartPlanning(f.ctx, owner, epic.ID, "planner"))
	_, err = f.s.Edit(f.ctx, Agent("planner", ""), two.ID, Patch{ParentID: &other.ID})
	wantCode(t, err, CodeInvalid)
	// A change request filed before the link is refused at acceptance.
	three := f.create(owner, story.ID, KindSubtask, "Three")
	f.launch(three.ID, "worker")
	res, err := f.s.Edit(f.ctx, Agent("planner", ""), three.ID, Patch{ParentID: &other.ID})
	f.must(err)
	_, err = f.s.ReleaseHold(f.ctx, owner, three.ID, ReleaseOwner, "")
	f.must(err)
	f.must(f.s.Link(f.ctx, owner, three.ID, two.ID))
	_, err = f.s.Accept(f.ctx, owner, res.Request.ID, "")
	wantCode(t, err, CodeInvalid)
	// A linked subtask under an epic would become a story: refused.
	ea := f.create(owner, epic.ID, KindSubtask, "Under the epic")
	eb := f.create(owner, epic.ID, KindSubtask, "Also under the epic")
	f.must(f.s.Link(f.ctx, owner, ea.ID, eb.ID))
	_, err = f.s.Split(f.ctx, owner, eb.ID, []SplitChild{{Title: "a"}})
	wantCode(t, err, CodeInvalid)
	// Under a story it splits into siblings, and the cancelled original keeps its links.
	_, err = f.s.Split(f.ctx, owner, one.ID, []SplitChild{{Title: "a"}, {Title: "b"}})
	f.must(err)
	wantStatus(t, f.card(one.ID), StatusCancelled)
	// Unlinked, it moves.
	f.must(f.s.Unlink(f.ctx, owner, ea.ID, eb.ID))
	_, err = f.s.Edit(f.ctx, owner, eb.ID, Patch{ParentID: &other.ID})
	f.must(err)
}

// A blocked epic or story holds back everything under it: finishing a
// subtask waits on its own open blockers and its ancestors', each named with
// the card it comes through, and "Do whole story" puts subtasks that wait
// last. Links made before the level rule keep blocking and can be removed,
// but are not made again.
func TestBlockersHoldBackWhatIsUnder(t *testing.T) {
	f := newFixture(t)
	a, sa1, la1, la2 := f.tree()
	sa2 := f.create(owner, a.ID, KindStory, "A2")
	b := f.create(owner, "", KindEpic, "B")
	f.must(f.s.Link(f.ctx, owner, la2.ID, la1.ID))
	f.must(f.s.Link(f.ctx, owner, sa2.ID, sa1.ID))
	f.must(f.s.Link(f.ctx, owner, b.ID, a.ID))
	_, err := f.s.CheckFinishable(f.ctx, owner, la1.ID)
	wantCode(t, err, CodeGuardBlockers)
	want := fmt.Sprintf("%s still waits on %s, %s (via its story %s), %s (via its epic %s)", la1.ref(), la2.ref(), sa2.ref(), sa1.ref(), b.ref(), a.ref())
	if err.Error() != want {
		t.Fatalf("guard = %q, want %q", err, want)
	}
	var e *Error
	if !errors.As(err, &e) || !slices.Equal(e.Refs, []string{la2.ref(), sa2.ref(), b.ref()}) {
		t.Fatalf("guard refs = %v", err)
	}
	// Work still starts, and finishing waits.
	f.launch(la2.ID, "worker")
	_, err = f.s.FileRequest(f.ctx, Agent("worker", ""), la2.ID, RequestInput{Kind: RequestDone, Comment: "done"})
	wantCode(t, err, CodeGuardBlockers)
	_, err = f.s.SetStatus(f.ctx, owner, la2.ID, StatusDone, "done", false)
	wantCode(t, err, CodeGuardBlockers)
	// "Do whole story" holds a subtask whose story waits on nothing first.
	sb1 := f.create(owner, b.ID, KindStory, "B1")
	lb1 := f.create(owner, sb1.ID, KindSubtask, "B1 leaf")
	sb2 := f.create(owner, b.ID, KindStory, "B2")
	lb2 := f.create(owner, sb2.ID, KindSubtask, "B2 leaf")
	f.must(f.s.Link(f.ctx, owner, sb2.ID, sb1.ID))
	pending, err := f.s.PendingLeaves(f.ctx, b.ID)
	f.must(err)
	if len(pending) != 2 || pending[0].ID != lb2.ID || pending[1].ID != lb1.ID {
		t.Fatalf("pending = %v, want B2's leaf, then B1's, which waits", pending)
	}
	// A link across levels from before the rule still blocks; it can be
	// removed, and not made again.
	legacy := f.create(owner, sa2.ID, KindSubtask, "A2 leaf")
	f.raw(`INSERT INTO links (blocker_id, blocked_id) VALUES (?, ?)`, legacy.ID, lb2.ID)
	if c := f.card(lb2.ID); !slices.Equal(c.BlockedBy, []string{legacy.ID}) {
		t.Fatalf("legacy link not shown: %v", c.BlockedBy)
	}
	_, err = f.s.CheckFinishable(f.ctx, owner, lb2.ID)
	wantCode(t, err, CodeGuardBlockers)
	f.must(f.s.Unlink(f.ctx, owner, legacy.ID, lb2.ID))
	if _, err := f.s.CheckFinishable(f.ctx, owner, lb2.ID); err != nil {
		t.Fatalf("after unlink: %v", err)
	}
	wantCode(t, f.s.Link(f.ctx, owner, legacy.ID, lb2.ID), CodeInvalid)
}
