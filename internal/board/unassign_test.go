package board

import (
	"slices"
	"testing"
)

func TestUnassignMovesTheWholeBoard(t *testing.T) {
	f := newFixture(t)
	epic, story, one, two := f.tree()
	f.must(f.s.Link(f.ctx, owner, two.ID, one.ID))
	f.launch(one.ID, "task-1")
	agent := Agent("task-1", "")
	req, err := f.s.FileRequest(f.ctx, agent, one.ID, RequestInput{Kind: RequestCancel, Comment: "drop"})
	f.must(err)
	other, err := f.s.Create(f.ctx, owner, NewCard{ProjectID: "p2", Kind: KindSubtask, Title: "Elsewhere"})
	f.must(err)
	before := len(f.changes)

	n, err := f.s.Unassign(f.ctx, proj)
	f.must(err)
	if n != 4 {
		t.Fatalf("moved %d cards, want 4", n)
	}
	snap, err := f.s.Board(f.ctx, proj)
	f.must(err)
	if len(snap.Cards) != 0 {
		t.Fatalf("the removed Project still has %d cards", len(snap.Cards))
	}
	snap, err = f.s.Board(f.ctx, "")
	f.must(err)
	var ids []string
	for _, c := range snap.Cards {
		ids = append(ids, c.ID)
	}
	if !slices.Equal(ids, []string{epic.ID, story.ID, one.ID, two.ID}) {
		t.Fatalf("unassigned = %v, want the tree in outline order", ids)
	}
	moved := f.card(one.ID)
	if moved.ProjectID != "" || moved.ParentID != story.ID || moved.HeldBy != "" || moved.Status != StatusTodo ||
		!slices.Equal(moved.BlockedBy, []string{two.ID}) {
		t.Fatalf("moved subtask = %+v", moved)
	}
	if !hasComment(f.comments(one.ID), "uam: attempt #1 ended") {
		t.Fatalf("comments = %v, want the ended attempt", f.comments(one.ID))
	}
	if r, err := f.s.Request(f.ctx, req.ID); err != nil || r.Status != RequestWithdrawn {
		t.Fatalf("request = %+v, %v; want withdrawn", r, err)
	}
	if f.card(other.ID).ProjectID != "p2" {
		t.Fatal("another Project's card moved")
	}
	// One write: the removed Project loses its cards and Unassigned gains them.
	changes := f.changes[before:]
	if len(changes) != 2 || changes[0].ProjectID != "" || len(changes[0].Cards) != 4 ||
		changes[1].ProjectID != proj || len(changes[1].Removed) != 4 || !slices.Contains(changes[1].Requests, req.ID) {
		t.Fatalf("changes = %+v", changes)
	}
	// The moved cards are read-only until the owner moves one back in.
	_, err = f.s.Confirm(f.ctx, owner, two.ID)
	wantCode(t, err, CodeReadOnly)
	_, err = f.s.Unassign(f.ctx, "")
	wantCode(t, err, CodeReadOnly)
	if n, err := f.s.Unassign(f.ctx, "gone"); err != nil || n != 0 {
		t.Fatalf("unassign of an empty Project = %d, %v", n, err)
	}
}

// A removed Project's approvals and pauses are for its repository: the
// cards leave them behind, and an epic moved into another Project carries
// none.
func TestUnassignDropsApproval(t *testing.T) {
	f := newFixture(t)
	f.must(f.s.SetProjectAcceptCmd(f.ctx, owner, proj, "go test ./..."))
	epic, story, one, two := f.tree()
	c, err := f.s.Approve(f.ctx, owner, epic.ID, RunSettings{Provider: "copilot", Model: "gpt-6-luna", Mode: "safe", Parallel: 2}, f.items(epic.ID, story.ID, one.ID, two.ID))
	f.must(err)
	if c.Run == nil {
		t.Fatal("the epic was not approved")
	}
	_, err = f.s.Edit(f.ctx, owner, story.ID, Patch{Paused: ptr(true)})
	f.must(err)

	_, err = f.s.Unassign(f.ctx, proj)
	f.must(err)
	if got := f.card(epic.ID); got.Run != nil || f.card(story.ID).Paused != "" {
		t.Fatalf("unassigned: epic run %+v, story paused %q", got.Run, f.card(story.ID).Paused)
	}
	var runs int
	f.must(f.s.db.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&runs))
	if runs != 0 {
		t.Fatalf("%d runs left after the unassign", runs)
	}
	res, err := f.s.Edit(f.ctx, owner, epic.ID, Patch{ProjectID: ptr("p2")})
	f.must(err)
	if res.Card.Run != nil || f.card(story.ID).Paused != "" {
		t.Fatalf("moved into p2: %+v", res.Card)
	}
}
