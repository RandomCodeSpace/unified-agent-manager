package board

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// Test plan 12 and 13: derivation ignores unconfirmed subtasks except
// holds; a container can't become done while a subtask under it is held or
// has a pending request; on done its unconfirmed, unheld subtasks are
// cancelled with the automatic comment and a roll-up is added.
func TestContainersReachDone(t *testing.T) {
	f := newFixture(t)
	epic, story, one, two := f.tree()
	f.must(f.s.StartPlanning(f.ctx, owner, epic.ID, "planner"))
	planner := Agent("planner", "")
	proposed := f.create(planner, story.ID, KindSubtask, "Proposed")
	if c := f.card(story.ID); c.Status != StatusPlanned || *c.Progress != (Progress{Total: 2, Proposed: 1}) {
		t.Fatalf("story = %s %+v", c.Status, c.Progress)
	}
	f.launch(one.ID, "task-1")
	req := f.done(one.ID, Agent("task-1", ""))
	_, err := f.s.SetStatus(f.ctx, owner, two.ID, StatusDone, "shipped", false)
	f.must(err)
	// One is still held with a pending request, so the story is not done.
	wantStatus(t, f.card(story.ID), StatusDoing)
	// A pending request on an unconfirmed subtask also keeps it open.
	pend, err := f.s.FileRequest(f.ctx, planner, proposed.ID, RequestInput{Kind: RequestCancel, Comment: "drop me"})
	f.must(err)
	_, err = f.s.Accept(f.ctx, owner, req.ID, "")
	f.must(err)
	c := f.card(story.ID)
	if c.Status != StatusDoing || *c.Progress != (Progress{Done: 2, Total: 2, Proposed: 1}) {
		t.Fatalf("story with a pending request = %s %+v", c.Status, c.Progress)
	}
	_, err = f.s.Reject(f.ctx, owner, pend.ID, "keep it", true)
	f.must(err)
	wantStatus(t, f.card(story.ID), StatusDone)
	wantStatus(t, f.card(epic.ID), StatusDone)
	p := f.card(proposed.ID)
	if p.Status != StatusCancelled || !hasComment(f.comments(proposed.ID), "uam: closed unconfirmed with #2") {
		t.Fatalf("proposed after close: %+v %v", p, f.comments(proposed.ID))
	}
	roll := f.detail(story.ID).Comments
	last := roll[len(roll)-1]
	if !last.Automatic || !last.Close || last.Author != AuthorUAM ||
		last.Body != "Closed with:\n#3 One: finished "+one.ID+"\n#4 Two: shipped" {
		t.Fatalf("story roll-up = %+v", last)
	}
	epicRoll := f.detail(epic.ID).Comments
	if len(epicRoll) != 1 || !strings.HasPrefix(epicRoll[0].Body, "Closed with:\n#2 Story: Closed with: #3 One") {
		t.Fatalf("epic roll-up = %+v", epicRoll)
	}
	// Reopening and finishing again adds another roll-up, nothing more.
	_, err = f.s.SetStatus(f.ctx, owner, two.ID, StatusTodo, "one more thing", false)
	f.must(err)
	wantStatus(t, f.card(story.ID), StatusDoing)
	_, err = f.s.SetStatus(f.ctx, owner, two.ID, StatusDone, "really shipped", false)
	f.must(err)
	if n := len(f.detail(story.ID).Comments); n != 2 {
		t.Fatalf("story has %d comments, want two roll-ups", n)
	}
}

func TestSetStatusAndDismiss(t *testing.T) {
	f := newFixture(t)
	epic, story, one, two := f.tree()
	_, err := f.s.Checklist(f.ctx, owner, one.ID, ChecklistEdit{Add: []string{"open item"}})
	f.must(err)
	_, err = f.s.SetStatus(f.ctx, owner, one.ID, StatusDone, "done", false)
	wantCode(t, err, CodeGuardOpenItems)
	_, err = f.s.SetStatus(f.ctx, owner, one.ID, StatusDone, " ", true)
	wantCode(t, err, CodeInvalid)
	got, err := f.s.SetStatus(f.ctx, owner, one.ID, StatusDone, "forced", true)
	f.must(err)
	if got.Status != StatusDone || !f.detail(one.ID).Comments[0].Close {
		t.Fatalf("forced done = %+v", got)
	}
	for _, tc := range []struct {
		ref   string
		to    Status
		force bool
	}{
		{story.ID, StatusDone, false}, {story.ID, StatusCancelled, true}, {story.ID, StatusTodo, false},
		{two.ID, StatusDoing, false}, {two.ID, StatusPlanned, false},
	} {
		_, err := f.s.SetStatus(f.ctx, owner, tc.ref, tc.to, "c", tc.force)
		wantCode(t, err, CodeInvalid)
	}
	// Ready, release, reopen.
	got, err = f.s.SetStatus(f.ctx, owner, two.ID, StatusTodo, "", false)
	f.must(err)
	wantStatus(t, got, StatusTodo)
	f.launch(two.ID, "task-2")
	got, err = f.s.SetStatus(f.ctx, owner, two.ID, StatusTodo, "stop", false)
	f.must(err)
	if got.HeldBy != "" || f.detail(two.ID).Holds[0].EndReason != ReleaseOwner || !hasComment(f.comments(two.ID), "owner: stop") {
		t.Fatalf("released by status: %+v", got)
	}
	got, err = f.s.SetStatus(f.ctx, owner, one.ID, StatusTodo, "", false)
	f.must(err)
	wantStatus(t, got, StatusTodo)
	f.launch(one.ID, "task-1")
	got, err = f.s.SetStatus(f.ctx, owner, one.ID, StatusDone, "done while held", true)
	f.must(err)
	if got.HeldBy != "" || f.detail(one.ID).Holds[0].EndReason != ReleaseDone {
		t.Fatalf("done while held: %+v", got)
	}
	got, err = f.s.SetStatus(f.ctx, owner, two.ID, StatusCancelled, "drop", false)
	f.must(err)
	wantStatus(t, got, StatusCancelled)
	_, err = f.s.SetStatus(f.ctx, owner, two.ID, StatusTodo, "", false)
	wantCode(t, err, CodeInvalid)
	_, err = f.s.SetStatus(f.ctx, owner, two.ID, StatusCancelled, "again", false)
	wantCode(t, err, CodeInvalid)
	// Dismiss: unconfirmed cards only, with everything under them.
	f.must(f.s.StartPlanning(f.ctx, owner, epic.ID, "planner"))
	planner := Agent("planner", "")
	s2 := f.create(planner, epic.ID, KindStory, "Suggested")
	leaf := f.create(planner, s2.ID, KindSubtask, "Suggested leaf")
	_, err = f.s.Dismiss(f.ctx, owner, story.ID)
	wantCode(t, err, CodeInvalid)
	got, err = f.s.Dismiss(f.ctx, owner, s2.ID)
	f.must(err)
	wantStatus(t, got, StatusCancelled)
	wantStatus(t, f.card(leaf.ID), StatusCancelled)
	if !hasComment(f.comments(leaf.ID), "uam: dismissed") || f.card(leaf.ID).CascadeID != got.CascadeID {
		t.Fatal("dismissal did not cascade")
	}
	_, err = f.s.Dismiss(f.ctx, owner, s2.ID)
	wantCode(t, err, CodeInvalid)
	// The story is done (one done, two cancelled), so it can't be cancelled.
	wantStatus(t, f.card(story.ID), StatusDone)
	_, err = f.s.SetStatus(f.ctx, owner, story.ID, StatusCancelled, "late", false)
	wantCode(t, err, CodeInvalid)
}

// Test plan 15: restore reopens exactly one cascade's cards and confirms
// them, and needs a comment.
func TestCascadeAndRestore(t *testing.T) {
	f := newFixture(t)
	epic, story, one, two := f.tree()
	three := f.create(owner, story.ID, KindSubtask, "Three")
	four := f.create(owner, story.ID, KindSubtask, "Four")
	_, err := f.s.SetStatus(f.ctx, owner, two.ID, StatusDone, "shipped", false)
	f.must(err)
	_, err = f.s.SetStatus(f.ctx, owner, four.ID, StatusCancelled, "not needed", false)
	f.must(err)
	fourCascade := f.card(four.ID).CascadeID
	f.launch(one.ID, "task-1")
	req := f.done(one.ID, Agent("task-1", ""))
	got, err := f.s.SetStatus(f.ctx, owner, story.ID, StatusCancelled, "descoped", false)
	f.must(err)
	cascade := got.CascadeID
	if got.Status != StatusCancelled || cascade == "" || cascade == fourCascade {
		t.Fatalf("cascade = %+v", got)
	}
	for _, c := range []Card{one, three} {
		got := f.card(c.ID)
		if got.Status != StatusCancelled || got.CascadeID != cascade || !hasComment(f.comments(c.ID), "uam: cancelled with #2: descoped") {
			t.Fatalf("%s after cascade: %+v %v", c.Title, got, f.comments(c.ID))
		}
	}
	if f.card(two.ID).Status != StatusDone || f.card(four.ID).CascadeID != fourCascade {
		t.Fatal("the cascade touched terminal cards")
	}
	if r, _ := f.s.Request(f.ctx, req.ID); r.Status != RequestWithdrawn || f.detail(one.ID).Holds[0].EndReason != ReleaseCancelled {
		t.Fatal("the cascade kept a hold or a request")
	}
	if !hasComment(f.comments(story.ID), "owner: descoped") {
		t.Fatal("no owner comment on the container")
	}
	_, err = f.s.Restore(f.ctx, owner, three.ID, " ")
	wantCode(t, err, CodeInvalid)
	_, err = f.s.Restore(f.ctx, owner, four.ID, "want it back")
	wantCode(t, err, CodeInvalid) // under the cancelled story, outside its cascade
	_, err = f.s.Restore(f.ctx, owner, two.ID, "not cancelled")
	wantCode(t, err, CodeInvalid)
	_, err = f.s.Restore(f.ctx, Owner("head-8"), three.ID, "back in scope")
	f.must(err)
	for _, tc := range []struct {
		c    Card
		want Status
	}{{story, StatusDoing}, {one, StatusTodo}, {three, StatusPlanned}, {two, StatusDone}, {four, StatusCancelled}} {
		got := f.card(tc.c.ID)
		wantStatus(t, got, tc.want)
		if tc.want != StatusDone && tc.want != StatusCancelled && (got.CascadeID != "" || got.PinnedSHA != "head-8" || !got.Confirmed()) {
			t.Fatalf("%s restored as %+v", tc.c.Title, got)
		}
	}
	if !hasComment(f.comments(three.ID), "owner: back in scope") {
		t.Fatal("no restore comment")
	}
	if _, err := f.s.Restore(f.ctx, owner, four.ID, "now it can"); err != nil {
		t.Fatal(err)
	}
	_ = epic
}

// Test plan 14: the sweep never cancels a held card, a card with a pending
// request, or an ancestor of a held card; swept cards can be restored.
func TestSweep(t *testing.T) {
	f := newFixture(t)
	epic, story, one, _ := f.tree()
	loose := f.create(owner, epic.ID, KindSubtask, "Epic-level")
	f.must(f.s.StartPlanning(f.ctx, owner, epic.ID, "planner"))
	planner := Agent("planner", "")
	plain := f.create(planner, epic.ID, KindStory, "Plain")
	plainLeaf := f.create(planner, plain.ID, KindSubtask, "Plain leaf")
	requested := f.create(planner, story.ID, KindSubtask, "Requested")
	_, err := f.s.FileRequest(f.ctx, planner, requested.ID, RequestInput{Kind: RequestCancel, Comment: "?"})
	f.must(err)
	parent := f.create(planner, epic.ID, KindStory, "Parent of held")
	heldLeaf := f.create(planner, parent.ID, KindSubtask, "Held leaf")
	f.launch(loose.ID, "worker")
	worker := Agent("worker", "")
	f.done(loose.ID, worker)
	f.legacyClaim(worker, heldLeaf.ID)
	f.clock.advance(ExpiryWindow - time.Minute)
	if n, err := f.s.Sweep(f.ctx); err != nil || n != 0 {
		t.Fatalf("early sweep = %d, %v", n, err)
	}
	f.clock.advance(time.Minute)
	n, err := f.s.Sweep(f.ctx)
	f.must(err)
	if n != 2 {
		t.Fatalf("swept %d cards, want plain and its leaf", n)
	}
	for _, c := range []Card{plain, plainLeaf} {
		got := f.card(c.ID)
		if got.Status != StatusCancelled || !hasComment(f.comments(c.ID), "uam: expired unconfirmed") {
			t.Fatalf("%s: %+v", c.Title, got)
		}
	}
	for _, c := range []Card{requested, parent, heldLeaf, one} {
		if f.card(c.ID).Status == StatusCancelled {
			t.Fatalf("the sweep cancelled %s", c.Title)
		}
	}
	if f.card(plain.ID).CascadeID != f.card(plainLeaf.ID).CascadeID {
		t.Fatal("a swept subtree is not one cascade")
	}
	got, err := f.s.Restore(f.ctx, owner, plainLeaf.ID, "keep it")
	f.must(err)
	if !got.Confirmed() || f.card(plain.ID).Status != StatusPlanned || !f.card(plain.ID).Confirmed() {
		t.Fatalf("restored swept cards: %+v", got)
	}
	// The sweep also runs on each outline write in the Project.
	late := f.create(planner, epic.ID, KindStory, "Late")
	f.clock.advance(ExpiryWindow)
	_, err = f.s.AddComment(f.ctx, owner, epic.ID, "any write")
	f.must(err)
	wantStatus(t, f.card(late.ID), StatusCancelled)
	// Restoring under an unconfirmed parent confirms the parent.
	s3 := f.create(planner, epic.ID, KindStory, "S3")
	l3 := f.create(planner, s3.ID, KindSubtask, "L3")
	f.clock.advance(time.Hour)
	_, err = f.s.Dismiss(f.ctx, owner, l3.ID)
	f.must(err)
	got, err = f.s.Restore(f.ctx, owner, l3.ID, "back")
	f.must(err)
	if !got.Confirmed() || !f.card(s3.ID).Confirmed() {
		t.Fatalf("restored %+v under an unconfirmed story", got)
	}
}

func TestPurge(t *testing.T) {
	f := newFixture(t)
	epic, story, one, two := f.tree()
	three := f.create(owner, story.ID, KindSubtask, "Three")
	other := f.create(owner, "", KindEpic, "Other")
	otherStory := f.create(owner, other.ID, KindStory, "Other story")
	otherLeaf := f.create(owner, otherStory.ID, KindSubtask, "Other leaf")
	// A link from before links joined one level only.
	f.raw(`INSERT INTO links (blocker_id, blocked_id) VALUES (?, ?)`, otherLeaf.ID, three.ID)
	f.launch(otherLeaf.ID, "task-1")
	_, err := f.s.AddComment(f.ctx, Agent("task-1", ""), otherLeaf.ID, "working")
	f.must(err)
	_, err = f.s.SetStatus(f.ctx, owner, two.ID, StatusDone, "shipped", false)
	f.must(err)
	_, err = f.s.SetStatus(f.ctx, owner, story.ID, StatusCancelled, "descoped", false)
	f.must(err)
	_, err = f.s.SetStatus(f.ctx, owner, other.ID, StatusCancelled, "gone", false)
	f.must(err)
	n, err := f.s.Purge(f.ctx, owner, proj)
	f.must(err)
	if n != 5 { // one, three, and the whole other epic
		t.Fatalf("purged %d, want 5", n)
	}
	snap, err := f.s.Board(f.ctx, proj)
	f.must(err)
	var ids []string
	for _, c := range snap.Cards {
		ids = append(ids, c.ID)
	}
	if !slices.Equal(ids, []string{epic.ID, story.ID, two.ID}) {
		t.Fatalf("left %v", ids)
	}
	for _, table := range []string{"comments", "links", "holds", "requests"} {
		var count int
		f.must(f.s.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE `+map[string]string{
			"comments": "card_id", "links": "blocker_id", "holds": "card_id", "requests": "card_id"}[table]+` = ?`, otherLeaf.ID).Scan(&count))
		if count != 0 {
			t.Fatalf("%d %s rows left for a purged card", count, table)
		}
	}
	if _, err := f.s.Card(f.ctx, one.ID); CodeOf(err) != CodeNotFound {
		t.Fatal("a purged card is still found")
	}
}

// Test plan 16: a cancelled or done blocker doesn't block.
func TestLinks(t *testing.T) {
	f := newFixture(t)
	epic, story, one, two := f.tree()
	f.must(f.s.Link(f.ctx, owner, "#4", "#3"))
	if c := f.card(one.ID); !slices.Equal(c.BlockedBy, []string{two.ID}) || !slices.Equal(f.card(two.ID).Blocks, []string{one.ID}) {
		t.Fatalf("links = %+v", c)
	}
	wantCode(t, f.s.Link(f.ctx, owner, two.ID, one.ID), CodeDuplicate)
	wantCode(t, f.s.Link(f.ctx, owner, one.ID, two.ID), CodeInvalid) // cycle
	wantCode(t, f.s.Link(f.ctx, owner, one.ID, one.ID), CodeInvalid)
	wantCode(t, f.s.Link(f.ctx, owner, "#99", one.ID), CodeNotFound)
	elsewhere, err := f.s.Create(f.ctx, owner, NewCard{ProjectID: "p2", Kind: KindSubtask, Title: "Elsewhere"})
	f.must(err)
	wantCode(t, f.s.Link(f.ctx, owner, elsewhere.ID, one.ID), CodeInvalid)
	f.must(f.s.StartPlanning(f.ctx, owner, story.ID, "planner"))
	planner := Agent("planner", "")
	proposed := f.create(planner, story.ID, KindSubtask, "Proposed")
	f.must(f.s.Link(f.ctx, planner, one.ID, proposed.ID)) // an unconfirmed card may be blocked
	outside := f.create(owner, epic.ID, KindSubtask, "Outside")
	wantCode(t, f.s.Link(f.ctx, planner, one.ID, outside.ID), CodeForbidden)
	// A container blocker is open until its derived status is terminal, and
	// everything under the card it blocks waits on it.
	blockerStory := f.create(owner, epic.ID, KindStory, "Blocker story")
	b1 := f.create(owner, blockerStory.ID, KindSubtask, "B1")
	f.must(f.s.Link(f.ctx, owner, blockerStory.ID, story.ID))
	_, err = f.s.CheckFinishable(f.ctx, owner, two.ID)
	wantCode(t, err, CodeGuardBlockers)
	if want := fmt.Sprintf("%s still waits on %s (via its story %s)", two.ref(), blockerStory.ref(), story.ref()); err.Error() != want {
		t.Fatalf("guard = %q, want %q", err, want)
	}
	_, err = f.s.SetStatus(f.ctx, owner, b1.ID, StatusDone, "done", false)
	f.must(err)
	if _, err := f.s.CheckFinishable(f.ctx, owner, two.ID); err != nil {
		t.Fatalf("a done container still blocks: %v", err)
	}
	_, err = f.s.CheckFinishable(f.ctx, owner, one.ID)
	wantCode(t, err, CodeGuardBlockers)
	_, err = f.s.SetStatus(f.ctx, owner, two.ID, StatusDone, "done", false)
	f.must(err)
	if _, err := f.s.CheckFinishable(f.ctx, owner, one.ID); err != nil {
		t.Fatalf("a done blocker still blocks: %v", err)
	}
	// Unlink, whichever way round, once neither card is done or in progress.
	wantCode(t, f.s.Unlink(f.ctx, owner, one.ID, two.ID), CodeInProgress)
	_, err = f.s.SetStatus(f.ctx, owner, two.ID, StatusTodo, "", false)
	f.must(err)
	f.must(f.s.Unlink(f.ctx, owner, one.ID, two.ID))
	wantCode(t, f.s.Unlink(f.ctx, owner, two.ID, one.ID), CodeNotFound)
	wantCode(t, f.s.Unlink(f.ctx, owner, "#99", one.ID), CodeNotFound)
	wantCode(t, f.s.Unlink(f.ctx, owner, one.ID, "#99"), CodeNotFound)
	un := f.unassigned("Unassigned")
	wantCode(t, f.s.Unlink(f.ctx, owner, un, one.ID), CodeReadOnly)
}

// Test plan 16: dependencies are planning, so a link joins proposals and
// confirmed cards in every combination, for an agent within its scope and
// for the owner anywhere; self links, duplicates and cycles are still
// refused. A proposal blocks like any card until it is done or cancelled:
// dismissing or expiring it cancels it, which releases the link and keeps
// its row, and purge deletes the row with the card.
func TestLinksOnProposals(t *testing.T) {
	f := newFixture(t)
	epic, story, one, two := f.tree()
	f.must(f.s.StartPlanning(f.ctx, owner, story.ID, "planner"))
	planner := Agent("planner", "")
	p1 := f.create(planner, story.ID, KindSubtask, "P1")
	p2 := f.create(planner, story.ID, KindSubtask, "P2")
	p3 := f.create(planner, story.ID, KindSubtask, "P3")
	f.must(f.s.Link(f.ctx, planner, p1.ID, p2.ID))  // proposal blocks proposal
	f.must(f.s.Link(f.ctx, planner, p2.ID, one.ID)) // proposal blocks confirmed
	f.must(f.s.Link(f.ctx, planner, two.ID, p3.ID)) // confirmed blocks proposal
	if c := f.card(p2.ID); !slices.Equal(c.BlockedBy, []string{p1.ID}) || !slices.Equal(c.Blocks, []string{one.ID}) {
		t.Fatalf("p2 links = %v, %v", c.BlockedBy, c.Blocks)
	}
	wantCode(t, f.s.Link(f.ctx, planner, one.ID, p1.ID), CodeInvalid) // p1 → p2 → one → p1
	wantCode(t, f.s.Link(f.ctx, owner, one.ID, p1.ID), CodeInvalid)
	wantCode(t, f.s.Link(f.ctx, planner, p1.ID, p2.ID), CodeDuplicate)
	wantCode(t, f.s.Link(f.ctx, planner, p3.ID, p3.ID), CodeInvalid)
	// An agent links only a blocked card in its scope; the owner links any
	// two cards of one level.
	story2 := f.create(owner, epic.ID, KindStory, "Story 2")
	f.must(f.s.StartPlanning(f.ctx, owner, epic.ID, "other"))
	q := f.create(Agent("other", ""), story2.ID, KindSubtask, "Q")
	wantCode(t, f.s.Link(f.ctx, planner, p3.ID, q.ID), CodeForbidden)
	wantCode(t, f.s.Link(f.ctx, owner, p3.ID, q.ID), CodeInvalid) // another story
	outside := f.create(owner, story2.ID, KindSubtask, "Outside")
	f.must(f.s.Link(f.ctx, owner, q.ID, outside.ID))
	// An open proposal blocks finishing.
	_, err := f.s.CheckFinishable(f.ctx, owner, one.ID)
	wantCode(t, err, CodeGuardBlockers)
	_, err = f.s.CheckFinishable(f.ctx, owner, outside.ID)
	wantCode(t, err, CodeGuardBlockers)
	links := func(id string) int {
		var n int
		f.must(f.s.db.QueryRow(`SELECT COUNT(*) FROM links WHERE blocker_id = ?1 OR blocked_id = ?1`, id).Scan(&n))
		return n
	}
	// Dismissing a proposal releases what it blocked.
	_, err = f.s.Dismiss(f.ctx, owner, p2.ID)
	f.must(err)
	if _, err := f.s.CheckFinishable(f.ctx, owner, one.ID); err != nil {
		t.Fatalf("a dismissed proposal still blocks: %v", err)
	}
	// So does its expiry.
	f.clock.advance(ExpiryWindow + time.Minute)
	_, err = f.s.Sweep(f.ctx)
	f.must(err)
	wantStatus(t, f.card(q.ID), StatusCancelled)
	if _, err := f.s.CheckFinishable(f.ctx, owner, outside.ID); err != nil {
		t.Fatalf("an expired proposal still blocks: %v", err)
	}
	if links(p2.ID) != 2 || links(q.ID) != 1 || !slices.Equal(f.card(outside.ID).BlockedBy, []string{q.ID}) {
		t.Fatal("cancelling a proposal deleted its links")
	}
	n, err := f.s.Purge(f.ctx, owner, proj)
	f.must(err)
	if n != 4 {
		t.Fatalf("purged %d cards, want the four proposals", n)
	}
	for _, c := range []Card{p1, p2, p3, q} {
		if links(c.ID) != 0 {
			t.Fatalf("purge left the links of %s", c.Title)
		}
	}
	if c := f.card(one.ID); len(c.BlockedBy) != 0 {
		t.Fatalf("one is still blocked by %v", c.BlockedBy)
	}
}

// Test plan 16: nothing agentic runs on an unconfirmed card. Every way a
// hold starts refuses while the subtask or an ancestor is a proposal: a
// working Task's claim, and a launch the owner didn't confirm. Planning on
// proposals still works, and a confirmed launch confirms the whole chain in
// the write that starts the hold, leaving unconfirmed blockers as they are.
func TestWorkStartsOnlyOnConfirmedCards(t *testing.T) {
	f := newFixture(t)
	epic, _, _, _ := f.tree()
	f.must(f.s.StartPlanning(f.ctx, owner, epic.ID, "planner"))
	planner := Agent("planner", "")
	ps := f.create(planner, epic.ID, KindStory, "Proposed story")
	pa := f.create(planner, ps.ID, KindSubtask, "Proposed A")
	pb := f.create(planner, ps.ID, KindSubtask, "Proposed B")
	f.must(f.s.Link(f.ctx, planner, pb.ID, pa.ID))
	// A working Task scoped to the epic plans on the proposals but can't
	// claim them.
	whole, err := f.s.Launch(f.ctx, owner, epic.ID, "epic-task", Baseline{}, false)
	f.must(err) // "Do whole story" holds a confirmed subtask
	if whole.ID == pa.ID || whole.ID == pb.ID {
		t.Fatalf("do whole story held a proposal: %+v", whole)
	}
	epicWorker := Agent("epic-task", "")
	f.done(whole.ID, epicWorker)
	_, err = f.s.Claim(f.ctx, epicWorker, pa.ID, Baseline{})
	wantUnconfirmed(t, err, pa.ref(), ps.ref())
	_, err = f.s.Edit(f.ctx, epicWorker, pa.ID, Patch{Title: ptr("Proposed A, renamed")})
	f.must(err)
	// "Do whole story" on a story of proposals has nothing to hold.
	_, err = f.s.Launch(f.ctx, owner, ps.ID, "t-story", Baseline{}, true)
	wantCode(t, err, CodeInvalid)
	// A confirmed launch confirms the subtask and its story at once; its
	// proposed blocker stays a proposal and still blocks.
	_, err = f.s.Launch(f.ctx, owner, pa.ID, "t-a", Baseline{}, false)
	wantUnconfirmed(t, err, pa.ref(), ps.ref())
	held, err := f.s.Launch(f.ctx, Owner("head-7"), pa.ID, "t-a", Baseline{}, true)
	f.must(err)
	if story := f.card(ps.ID); held.HeldBy != "t-a" || !held.Confirmed() || !story.Confirmed() || story.PinnedSHA != "head-7" {
		t.Fatalf("launched %+v under %+v", held, story)
	}
	if b := f.card(pb.ID); b.Confirmed() {
		t.Fatal("launching confirmed the blocker")
	}
	_, err = f.s.CheckFinishable(f.ctx, owner, pa.ID)
	wantCode(t, err, CodeGuardBlockers)
	// A confirmed card under a proposal, as an earlier store could leave
	// it, is refused too.
	pc := f.create(planner, epic.ID, KindStory, "Proposed C")
	leaf := f.create(owner, pc.ID, KindSubtask, "Owner leaf")
	f.raw(`UPDATE cards SET expires_at = ? WHERE id = ?`, stamp(f.clock.Now().Add(ExpiryWindow)), pc.ID)
	_, err = f.s.Claim(f.ctx, epicWorker, leaf.ID, Baseline{})
	wantUnconfirmed(t, err, pc.ref())
	wantUnconfirmed(t, f.s.CheckLaunch(f.ctx, owner, leaf.ID, false), pc.ref())
}

// Test plan 19: staleness is never computed for held subtasks.
func TestStaleCandidates(t *testing.T) {
	f := newFixture(t)
	epic, story, one, two := f.tree()
	three := f.create(owner, story.ID, KindSubtask, "Three")
	f.launch(one.ID, "task-1")
	_, err := f.s.SetStatus(f.ctx, owner, two.ID, StatusDone, "done", false)
	f.must(err)
	f.create(Agent("task-1", ""), story.ID, KindSubtask, "Unconfirmed")
	got, err := f.s.StaleCandidates(f.ctx, proj)
	f.must(err)
	if len(got) != 1 || got[0].ID != three.ID {
		t.Fatalf("stale candidates = %+v", got)
	}
	_ = epic
}

func TestDetail(t *testing.T) {
	f := newFixture(t)
	_, _, one, _ := f.tree()
	f.launch(one.ID, "task-1")
	f.done(one.ID, Agent("task-1", "a1"))
	_, err := f.s.AddComment(f.ctx, Agent("task-1", "a1"), one.ID, "note")
	f.must(err)
	d := f.detail("#3")
	if d.Card.ID != one.ID || len(d.Holds) != 1 || len(d.Requests) != 1 || len(d.Comments) != 1 ||
		d.Comments[0].AgentID != "a1" || d.Comments[0].Author != "task:task-1" || d.Comments[0].CreatedAt.IsZero() {
		t.Fatalf("detail = %+v", d)
	}
	_, err = f.s.Detail(f.ctx, "#99")
	wantCode(t, err, CodeNotFound)
}

// A cascade skips a container that is already done, as cancelling it
// directly is refused, and nothing is then added under it.
func TestCascadeKeepsDoneContainers(t *testing.T) {
	f := newFixture(t)
	epic, story, one, two := f.tree()
	other := f.create(owner, epic.ID, KindStory, "Other")
	three := f.create(owner, other.ID, KindSubtask, "Three")
	for _, c := range []Card{one, two} {
		_, err := f.s.SetStatus(f.ctx, owner, c.ID, StatusDone, "ok", false)
		f.must(err)
	}
	wantStatus(t, f.card(story.ID), StatusDone)
	_, err := f.s.SetStatus(f.ctx, owner, story.ID, StatusCancelled, "drop", false)
	wantCode(t, err, CodeInvalid)
	_, err = f.s.SetStatus(f.ctx, owner, epic.ID, StatusCancelled, "drop epic", false)
	f.must(err)
	if got := f.card(story.ID); got.Status != StatusDone || got.CascadeID != "" || got.Progress.Done != 2 {
		t.Fatalf("done story after its epic's cascade: %+v", got)
	}
	wantStatus(t, f.card(other.ID), StatusCancelled)
	wantStatus(t, f.card(three.ID), StatusCancelled)
	_, err = f.s.Create(f.ctx, owner, NewCard{ProjectID: proj, Kind: KindSubtask, ParentID: story.ID, Title: "Late"})
	wantCode(t, err, CodeInvalid)
	loose := f.create(owner, "", KindSubtask, "Loose")
	_, err = f.s.Edit(f.ctx, owner, loose.ID, Patch{ParentID: &story.ID})
	wantCode(t, err, CodeInvalid)
	_, err = f.s.Restore(f.ctx, owner, epic.ID, "back")
	f.must(err)
	wantStatus(t, f.card(story.ID), StatusDone)
	wantStatus(t, f.card(three.ID), StatusPlanned)
}

// A subtask under a cancelled card is never reopened or held on its own:
// ready, Launch and Claim refuse it until the cancelled card is restored.
func TestNothingReopensUnderCancelledParent(t *testing.T) {
	f := newFixture(t)
	epic, story, one, two := f.tree()
	_, err := f.s.SetStatus(f.ctx, owner, one.ID, StatusDone, "ok", false)
	f.must(err)
	_, err = f.s.SetStatus(f.ctx, owner, story.ID, StatusCancelled, "drop story", false)
	f.must(err)
	wantStatus(t, f.card(story.ID), StatusCancelled)
	_, err = f.s.SetStatus(f.ctx, owner, one.ID, StatusTodo, "", false)
	wantCode(t, err, CodeInvalid)
	_, err = f.s.Restore(f.ctx, owner, one.ID, "back")
	wantCode(t, err, CodeInvalid) // done, not cancelled
	// A subtask left open under the cancelled story, as older data may be,
	// can't be launched or claimed.
	loose := f.create(owner, epic.ID, KindSubtask, "Loose")
	f.launch(loose.ID, "w")
	f.done(loose.ID, Agent("w", ""))
	f.raw(`UPDATE cards SET status = 'todo', cascade_id = '' WHERE id = ?`, two.ID)
	_, err = f.s.Launch(f.ctx, owner, two.ID, "t1", Baseline{}, false)
	wantCode(t, err, CodeInvalid)
	_, err = f.s.Claim(f.ctx, Agent("w", ""), two.ID, Baseline{})
	wantCode(t, err, CodeInvalid)
	f.raw(`UPDATE cards SET status = 'cancelled', cascade_id = ? WHERE id = ?`, f.card(story.ID).CascadeID, two.ID)
	// Restoring the story's cascade reopens it; then ready works again.
	_, err = f.s.Restore(f.ctx, owner, story.ID, "back")
	f.must(err)
	got, err := f.s.SetStatus(f.ctx, owner, one.ID, StatusTodo, "", false)
	f.must(err)
	wantStatus(t, got, StatusTodo)
}

// When a container reaches done its own pending requests are withdrawn, so
// none is left pending on a finished card.
func TestDoneContainerWithdrawsRequests(t *testing.T) {
	f := newFixture(t)
	epic, story, one, two := f.tree()
	f.must(f.s.StartPlanning(f.ctx, owner, epic.ID, "planner"))
	r, err := f.s.FileRequest(f.ctx, Agent("planner", ""), story.ID, RequestInput{Kind: RequestCancel, Comment: "not needed"})
	f.must(err)
	for _, c := range []Card{one, two} {
		_, err := f.s.SetStatus(f.ctx, owner, c.ID, StatusDone, "ok", false)
		f.must(err)
	}
	wantStatus(t, f.card(story.ID), StatusDone)
	got, err := f.s.Request(f.ctx, r.ID)
	f.must(err)
	if got.Status != RequestWithdrawn || f.card(story.ID).PendingRequests != 0 {
		t.Fatalf("request on the done story = %s", got.Status)
	}
	_, err = f.s.Accept(f.ctx, owner, r.ID, "")
	wantCode(t, err, CodeInvalid)
}
