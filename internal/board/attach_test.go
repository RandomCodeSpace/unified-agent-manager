package board

import "testing"

// An existing Task works on a subtask: a new one under a story, or one not
// started yet. Attaching is work, so it goes through the confirm step, and a
// Task works on one subtask at a time.
func TestAttach(t *testing.T) {
	f := newFixture(t)
	epic, story, one, two := f.tree()
	base := Baseline{Head: "base"}
	got, err := f.s.Attach(f.ctx, Owner("head-2"), story.ID, "task-1", "Fix the flaky test", base, false)
	f.must(err)
	if got.Kind != KindSubtask || got.ParentID != story.ID || got.Title != "Fix the flaky test" || got.HeldBy != "task-1" ||
		got.WorkedBy != "task-1" || got.Status != StatusDoing || !got.Confirmed() || got.PinnedSHA != "head-2" {
		t.Fatalf("attached %+v", got)
	}
	if h := f.detail(got.ID).Holds; len(h) != 1 || h[0].TaskID != "task-1" || h[0].Baseline.Head != "base" {
		t.Fatalf("holds = %+v", h)
	}
	// The Task is scoped like a launched one: to the story.
	f.create(Agent("task-1", ""), story.ID, KindSubtask, "Found along the way")
	_, err = f.s.Create(f.ctx, Agent("task-1", ""), NewCard{ProjectID: proj, Kind: KindStory, ParentID: epic.ID, Title: "x"})
	wantCode(t, err, CodeForbidden)
	// One subtask at a time; a started subtask can't be attached; a new one needs a title.
	_, err = f.s.Attach(f.ctx, owner, one.ID, "task-1", "", base, false)
	wantCode(t, err, CodeLimit)
	_, err = f.s.Attach(f.ctx, owner, got.ID, "task-2", "", base, false)
	wantCode(t, err, CodeInProgress)
	_, err = f.s.Attach(f.ctx, owner, story.ID, "task-2", " ", base, false)
	wantCode(t, err, CodeInvalid)
	_, err = f.s.Attach(f.ctx, owner, story.ID, "", "x", base, false)
	wantCode(t, err, CodeInvalid)
	// An existing subtask keeps its title.
	held, err := f.s.Attach(f.ctx, owner, two.ID, "task-2", "ignored", base, false)
	f.must(err)
	if held.ID != two.ID || held.Title != "Two" || held.HeldBy != "task-2" {
		t.Fatalf("attached %+v", held)
	}
	// WorkedBy stays once the attempt ends.
	_, err = f.s.ReleaseHold(f.ctx, owner, two.ID, ReleaseOwner, "")
	f.must(err)
	if c := f.card(two.ID); c.HeldBy != "" || c.WorkedBy != "task-2" {
		t.Fatalf("released %+v", c)
	}
	// Under a proposal, attaching asks to confirm first and writes nothing.
	f.must(f.s.StartPlanning(f.ctx, owner, epic.ID, "planner"))
	ps := f.create(Agent("planner", ""), epic.ID, KindStory, "Proposed")
	before := f.revision()
	_, err = f.s.Attach(f.ctx, owner, ps.ID, "task-3", "New work", base, false)
	wantUnconfirmed(t, err, ps.ref())
	if f.revision() != before {
		t.Fatal("a refused attach wrote")
	}
	got, err = f.s.Attach(f.ctx, Owner("head-3"), ps.ID, "task-3", "New work", base, true)
	f.must(err)
	if s := f.card(ps.ID); !s.Confirmed() || s.PinnedSHA != "head-3" || got.HeldBy != "task-3" {
		t.Fatalf("attached %+v under %+v", got, s)
	}
	pl := f.create(Agent("planner", ""), epic.ID, KindStory, "Proposed two")
	leaf := f.create(Agent("planner", ""), pl.ID, KindSubtask, "Proposed leaf")
	_, err = f.s.Attach(f.ctx, owner, leaf.ID, "task-4", "", base, false)
	wantUnconfirmed(t, err, leaf.ref(), pl.ref())
	_, err = f.s.Attach(f.ctx, owner, leaf.ID, "task-4", "", base, true)
	f.must(err)
	_, err = f.s.Attach(f.ctx, Agent("task-5", ""), story.ID, "task-5", "x", base, false)
	wantCode(t, err, CodeForbidden)
}
