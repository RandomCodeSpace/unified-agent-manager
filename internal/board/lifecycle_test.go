package board

import (
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
	_, err = f.s.Claim(f.ctx, worker, heldLeaf.ID, Baseline{})
	f.must(err)
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
	f.must(f.s.Link(f.ctx, owner, otherLeaf.ID, three.ID))
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

// Test plan 16: a cancelled or done blocker doesn't block, and a link to an
// unconfirmed card is refused.
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
	wantCode(t, f.s.Link(f.ctx, planner, proposed.ID, one.ID), CodeInvalid)
	f.must(f.s.Link(f.ctx, planner, story.ID, proposed.ID)) // an unconfirmed card may be blocked
	outside := f.create(owner, epic.ID, KindSubtask, "Outside")
	wantCode(t, f.s.Link(f.ctx, planner, one.ID, outside.ID), CodeForbidden)
	// A container blocker is open until its derived status is terminal.
	blockerStory := f.create(owner, epic.ID, KindStory, "Blocker story")
	b1 := f.create(owner, blockerStory.ID, KindSubtask, "B1")
	f.must(f.s.Link(f.ctx, owner, blockerStory.ID, outside.ID))
	_, err = f.s.CheckFinishable(f.ctx, owner, outside.ID)
	wantCode(t, err, CodeGuardBlockers)
	_, err = f.s.SetStatus(f.ctx, owner, b1.ID, StatusDone, "done", false)
	f.must(err)
	if _, err := f.s.CheckFinishable(f.ctx, owner, outside.ID); err != nil {
		t.Fatalf("a done container still blocks: %v", err)
	}
	_, err = f.s.CheckFinishable(f.ctx, owner, one.ID)
	wantCode(t, err, CodeGuardBlockers)
	_, err = f.s.SetStatus(f.ctx, owner, two.ID, StatusDone, "done", false)
	f.must(err)
	if _, err := f.s.CheckFinishable(f.ctx, owner, one.ID); err != nil {
		t.Fatalf("a done blocker still blocks: %v", err)
	}
	// Unlink, whichever way round.
	f.must(f.s.Unlink(f.ctx, owner, one.ID, two.ID))
	wantCode(t, f.s.Unlink(f.ctx, owner, two.ID, one.ID), CodeNotFound)
	wantCode(t, f.s.Unlink(f.ctx, owner, "#99", one.ID), CodeNotFound)
	wantCode(t, f.s.Unlink(f.ctx, owner, one.ID, "#99"), CodeNotFound)
	un := f.unassigned("Unassigned")
	wantCode(t, f.s.Unlink(f.ctx, owner, un, one.ID), CodeReadOnly)
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
