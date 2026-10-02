package board

import (
	"database/sql"
	"testing"
)

// Until a subtask starts, an agent in scope plans it directly, confirmed or
// not: it edits, moves, links, unlinks, splits and dismisses proposals, and
// files no change request.
func TestAgentsPlanUntilWorkStarts(t *testing.T) {
	f := newFixture(t)
	epic, story, one, two := f.tree()
	other := f.create(owner, epic.ID, KindStory, "Other")
	f.must(f.s.StartPlanning(f.ctx, owner, epic.ID, "planner"))
	planner := Agent("planner", "")
	res, err := f.s.Edit(f.ctx, planner, one.ID, Patch{Title: ptr("One, renamed"), Desc: ptr("why"), Rank: ptr(1)})
	f.must(err)
	if res.Request != nil || res.Card.Title != "One, renamed" || !res.Card.Confirmed() || res.Card.Rank != 1 {
		t.Fatalf("agent edit of a confirmed card = %+v", res)
	}
	res, err = f.s.Edit(f.ctx, planner, two.ID, Patch{ParentID: &other.ID})
	f.must(err)
	if res.Request != nil || res.Card.ParentID != other.ID {
		t.Fatalf("agent move of a confirmed card = %+v", res)
	}
	p := f.create(planner, story.ID, KindSubtask, "Proposed")
	f.must(f.s.Link(f.ctx, planner, one.ID, p.ID))
	f.must(f.s.Unlink(f.ctx, planner, one.ID, p.ID))
	f.must(f.s.Link(f.ctx, planner, story.ID, other.ID))
	f.must(f.s.Unlink(f.ctx, planner, other.ID, story.ID))
	split, err := f.s.Split(f.ctx, planner, one.ID, []SplitChild{{Title: "a"}, {Title: "b"}})
	f.must(err)
	if split.Request != nil || f.card(one.ID).Status != StatusCancelled {
		t.Fatalf("agent split of a confirmed subtask = %+v", split)
	}
	_, err = f.s.Dismiss(f.ctx, planner, p.ID)
	f.must(err)
	if c := f.card(p.ID); c.Status != StatusCancelled || !hasComment(f.comments(p.ID), "uam: dismissed") {
		t.Fatalf("dismissed = %+v", c)
	}
	_, err = f.s.Dismiss(f.ctx, planner, two.ID)
	wantCode(t, err, CodeInvalid) // confirmed: only the owner cancels it
	// Out of scope stays out of reach.
	loose := f.create(owner, "", KindSubtask, "Loose")
	_, err = f.s.Edit(f.ctx, planner, loose.ID, Patch{Title: ptr("x")})
	wantCode(t, err, CodeForbidden)
	x, y := f.create(owner, story.ID, KindSubtask, "X"), f.create(owner, story.ID, KindSubtask, "Y")
	f.must(f.s.Link(f.ctx, owner, x.ID, y.ID))
	wantCode(t, f.s.Unlink(f.ctx, Agent("nobody", ""), x.ID, y.ID), CodeForbidden)
}

// Once a subtask starts its plan is locked for the owner and agents alike:
// edits, moves, links, unlinks, splits and new checklist items are refused,
// or for an agent's edit and split filed as a request the owner accepts
// once it is released. Ticks by its Task and the owner, the owner's own
// fields, comments and requests go on. Released, it plans again, and its
// change and split requests are still pending.
func TestStartedSubtaskKeepsItsPlan(t *testing.T) {
	f := newFixture(t)
	epic, story, one, two := f.tree()
	_, err := f.s.Checklist(f.ctx, owner, one.ID, ChecklistEdit{Add: []string{"a", "b"}})
	f.must(err)
	f.launch(one.ID, "worker")
	worker := Agent("worker", "")
	other := f.create(owner, epic.ID, KindStory, "Other")
	for name, op := range map[string]func() error{
		"edit": func() error { return errOf(f.s.Edit(f.ctx, owner, one.ID, Patch{Title: ptr("x")})) },
		"prio": func() error { return errOf(f.s.Edit(f.ctx, owner, one.ID, Patch{Prio: ptr(1)})) },
		"move": func() error { return errOf(f.s.Edit(f.ctx, owner, one.ID, Patch{ParentID: &other.ID})) },
		"rank": func() error { return errOf(f.s.Edit(f.ctx, owner, one.ID, Patch{Rank: ptr(1)})) },
		"rename": func() error {
			return errOf(f.s.Edit(f.ctx, owner, one.ID, Patch{Checklist: &[]Check{{Text: "A"}, {Text: "b"}}}))
		},
		"add":        func() error { return errOf(f.s.Checklist(f.ctx, owner, one.ID, ChecklistEdit{Add: []string{"c"}})) },
		"agent add":  func() error { return errOf(f.s.Checklist(f.ctx, worker, one.ID, ChecklistEdit{Add: []string{"c"}})) },
		"link":       func() error { return f.s.Link(f.ctx, owner, two.ID, one.ID) },
		"link from":  func() error { return f.s.Link(f.ctx, owner, one.ID, two.ID) },
		"agent link": func() error { return f.s.Link(f.ctx, worker, two.ID, one.ID) },
		"split":      func() error { return errOf(f.s.Split(f.ctx, owner, one.ID, []SplitChild{{Title: "c"}})) },
	} {
		if err := op(); CodeOf(err) != CodeInProgress {
			t.Errorf("%s on a started subtask: %v, want %s", name, err, CodeInProgress)
		}
	}
	// What goes on while it runs.
	c, err := f.s.Checklist(f.ctx, worker, one.ID, ChecklistEdit{Tick: []int{0}})
	f.must(err)
	if !c.Checklist[0].Done {
		t.Fatalf("tick = %+v", c.Checklist)
	}
	_, err = f.s.Edit(f.ctx, owner, one.ID, Patch{Checklist: &[]Check{{Text: "a", Done: true}, {Text: "b", Done: true}}})
	f.must(err)
	_, err = f.s.Edit(f.ctx, owner, one.ID, Patch{AcceptCmd: &sql.NullString{Valid: true, String: "make check"}})
	f.must(err)
	_, err = f.s.AddComment(f.ctx, worker, one.ID, "working")
	f.must(err)
	change, err := f.s.Edit(f.ctx, worker, one.ID, Patch{Title: ptr("One, as it turned out")})
	f.must(err)
	split, err := f.s.Split(f.ctx, worker, one.ID, []SplitChild{{Title: "c"}})
	f.must(err)
	if change.Request == nil || split.Request == nil || f.card(one.ID).Title != "One" {
		t.Fatalf("agent requests = %+v, %+v", change.Request, split.Request)
	}
	_, err = f.s.Accept(f.ctx, owner, change.Request.ID, "")
	wantCode(t, err, CodeInProgress)
	// Released, it plans again and its requests can be accepted.
	_, err = f.s.ReleaseHold(f.ctx, owner, one.ID, ReleaseOwner, "")
	f.must(err)
	for _, id := range []string{change.Request.ID, split.Request.ID} {
		if r, err := f.s.Request(f.ctx, id); err != nil || r.Status != RequestPending {
			t.Fatalf("request after release = %+v, %v", r, err)
		}
	}
	_, err = f.s.Accept(f.ctx, owner, change.Request.ID, "")
	f.must(err)
	f.must(f.s.Link(f.ctx, owner, two.ID, one.ID))
	if got := f.card(one.ID); got.Title != "One, as it turned out" || len(got.BlockedBy) != 1 {
		t.Fatalf("released = %+v", got)
	}
	// A done subtask keeps its plan until it is back in To do.
	_, err = f.s.SetStatus(f.ctx, owner, two.ID, StatusDone, "shipped", false)
	f.must(err)
	_, err = f.s.Edit(f.ctx, owner, two.ID, Patch{Title: ptr("Two, renamed")})
	wantCode(t, err, CodeInProgress)
	wantCode(t, f.s.Unlink(f.ctx, owner, two.ID, one.ID), CodeInProgress)
	_, err = f.s.SetStatus(f.ctx, owner, two.ID, StatusTodo, "", false)
	f.must(err)
	_, err = f.s.Edit(f.ctx, owner, two.ID, Patch{Title: ptr("Two, renamed")})
	f.must(err)
	_ = story
}

// A container stays plannable while a subtask under it runs, except for
// what would take that subtask along: moving the container, or dismissing
// it, is refused until the subtask is released.
func TestContainerWithARunningSubtaskStaysPlannable(t *testing.T) {
	f := newFixture(t)
	epic, story, one, _ := f.tree()
	sibling := f.create(owner, epic.ID, KindStory, "Sibling")
	other := f.create(owner, "", KindEpic, "Other epic")
	f.launch(one.ID, "worker")
	f.must(f.s.StartPlanning(f.ctx, owner, epic.ID, "planner"))
	planner := Agent("planner", "")
	res, err := f.s.Edit(f.ctx, planner, story.ID, Patch{Title: ptr("Story, renamed")})
	f.must(err)
	if res.Request != nil || res.Card.Title != "Story, renamed" {
		t.Fatalf("container edit = %+v", res)
	}
	f.create(planner, story.ID, KindSubtask, "Added while it runs")
	f.must(f.s.Link(f.ctx, planner, sibling.ID, story.ID))
	_, err = f.s.Edit(f.ctx, owner, story.ID, Patch{ParentID: &other.ID})
	wantCode(t, err, CodeInvalid) // linked to its sibling first
	f.must(f.s.Unlink(f.ctx, owner, sibling.ID, story.ID))
	_, err = f.s.Edit(f.ctx, owner, story.ID, Patch{ParentID: &other.ID})
	wantCode(t, err, CodeInProgress)
	// A proposed story with a subtask held from before claims needed
	// confirmed cards: dismissing it would end that hold.
	ps := f.create(planner, epic.ID, KindStory, "Proposed")
	pl := f.create(planner, ps.ID, KindSubtask, "Proposed leaf")
	_, err = f.s.Launch(f.ctx, owner, epic.ID, "legacy", Baseline{}, false)
	f.must(err)
	f.done("#4", Agent("legacy", ""))
	f.legacyClaim(Agent("legacy", ""), pl.ID)
	_, err = f.s.Dismiss(f.ctx, planner, ps.ID)
	wantCode(t, err, CodeInProgress)
	_, err = f.s.Dismiss(f.ctx, owner, ps.ID)
	wantCode(t, err, CodeInProgress)
	// Released, it moves.
	for _, ref := range []string{one.ID, "#4"} {
		_, err = f.s.ReleaseHold(f.ctx, owner, ref, ReleaseOwner, "")
		f.must(err)
	}
	_, err = f.s.Edit(f.ctx, owner, story.ID, Patch{ParentID: &other.ID})
	f.must(err)
}
