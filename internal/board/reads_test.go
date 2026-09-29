package board

import "testing"

func TestHeld(t *testing.T) {
	f := newFixture(t)
	_, story, one, two := f.tree()
	held, err := f.s.Held(f.ctx)
	f.must(err)
	if len(held) != 0 {
		t.Fatalf("held before any launch = %+v", held)
	}
	f.launch(two.ID, "task-2")
	f.launch(one.ID, "task-1")
	held, err = f.s.Held(f.ctx)
	f.must(err)
	if len(held) != 2 || held[0].ID != one.ID || held[0].HeldBy != "task-1" || held[1].ID != two.ID || held[1].HeldBy != "task-2" {
		t.Fatalf("held = %+v, want one then two", held)
	}
	_, err = f.s.ReleaseHold(f.ctx, owner, one.ID, ReleaseOwner, "")
	f.must(err)
	held, err = f.s.Held(f.ctx)
	f.must(err)
	if len(held) != 1 || held[0].ID != two.ID {
		t.Fatalf("held after a release = %+v", held)
	}
	wantStatus(t, f.card(story.ID), StatusDoing)
}

func TestRevisions(t *testing.T) {
	f := newFixture(t)
	revs, err := f.s.Revisions(f.ctx)
	f.must(err)
	if len(revs) != 0 {
		t.Fatalf("revisions of an empty store = %v", revs)
	}
	f.create(owner, "", KindEpic, "One")
	f.create(owner, "", KindEpic, "Two")
	_, err = f.s.Create(f.ctx, owner, NewCard{ProjectID: "p2", Kind: KindEpic, Title: "Other"})
	f.must(err)
	f.unassigned("Loose")
	revs, err = f.s.Revisions(f.ctx)
	f.must(err)
	if len(revs) != 3 || revs[proj] != 2 || revs["p2"] != 1 || revs[""] != 1 || revs[proj] != f.revision() {
		t.Fatalf("revisions = %v", revs)
	}
}
