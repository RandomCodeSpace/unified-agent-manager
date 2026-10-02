package board

import (
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCreateValidation(t *testing.T) {
	f := newFixture(t)
	epic := f.create(owner, "", KindEpic, "Epic")
	long := strings.Repeat("x", maxLineBytes+1)
	for _, tc := range []struct {
		name string
		in   NewCard
		code Code
	}{
		{"blank title", NewCard{Title: "  "}, CodeInvalid},
		{"title line break", NewCard{Title: "a\nb"}, CodeInvalid},
		{"long title", NewCard{Title: long}, CodeInvalid},
		{"long description", NewCard{Title: "d", Desc: strings.Repeat("x", maxTextBytes+1)}, CodeInvalid},
		{"bad due", NewCard{Title: "d", Due: "2026-13-01"}, CodeInvalid},
		{"short due", NewCard{Title: "d", Due: "2026-1-01"}, CodeInvalid},
		{"bad effort", NewCard{Title: "d", Effort: "XL"}, CodeInvalid},
		{"bad priority", NewCard{Title: "d", Prio: 4}, CodeInvalid},
		{"empty label", NewCard{Title: "d", Labels: []string{""}}, CodeInvalid},
		{"spaced label", NewCard{Title: "d", Labels: []string{"a b"}}, CodeInvalid},
		{"hash label", NewCard{Title: "d", Labels: []string{"#a"}}, CodeInvalid},
		{"long label", NewCard{Title: "d", Labels: []string{long}}, CodeInvalid},
		{"blank item", NewCard{Title: "d", Checklist: []Check{{Text: " "}}}, CodeInvalid},
		{"item line break", NewCard{Title: "d", Checklist: []Check{{Text: "a\rb"}}}, CodeInvalid},
		{"too many labels", NewCard{Title: "d", Labels: slices.Repeat([]string{"l"}, maxListItems+1)}, CodeInvalid},
		{"bad kind", NewCard{Title: "d", Kind: "task"}, CodeInvalid},
		{"story under story", NewCard{Title: "d", Kind: KindStory, ParentID: "#2"}, CodeInvalid},
		{"epic under epic", NewCard{Title: "d", Kind: KindEpic, ParentID: epic.ID}, CodeInvalid},
		{"missing parent", NewCard{Title: "d", ParentID: "#99"}, CodeNotFound},
		{"parent in another Project", NewCard{Title: "d", ParentID: epic.ID, ProjectID: "p2"}, CodeInvalid},
		{"Unassigned", NewCard{Title: "d", ProjectID: "-"}, CodeReadOnly},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.in
			if in.Kind == "" {
				in.Kind = KindSubtask
			}
			if in.ProjectID == "" {
				in.ProjectID = proj
			}
			if in.ProjectID == "-" {
				in.ProjectID = ""
			}
			if tc.name == "story under story" {
				f.create(owner, epic.ID, KindStory, "Parent story")
			}
			_, err := f.s.Create(f.ctx, owner, in)
			wantCode(t, err, tc.code)
		})
	}
	c, err := f.s.Create(f.ctx, owner, NewCard{ProjectID: proj, Kind: KindSubtask, ParentID: epic.ID, Title: "  Full  ",
		Desc: "d", WinCondition: " passes ", Prio: PrioHigh, Due: "2026-10-01", Effort: "L", Labels: []string{"api", "ui"},
		Checklist: []Check{{Text: "a"}}})
	f.must(err)
	if c.Title != "Full" || c.WinCondition != "passes" || c.Prio != PrioHigh || c.Effort != "L" || !c.Confirmed() ||
		c.PinnedSHA != "head-1" || c.CreatedBy != AuthorOwner || c.Status != StatusPlanned || c.Rank != 1 {
		t.Fatalf("created %+v", c)
	}
	d := f.create(owner, epic.ID, KindSubtask, "Second")
	if d.Rank != 2 || d.Prio != PrioDefault || d.Effort != DefaultEffort {
		t.Fatalf("defaults %+v", d)
	}
	labels, err := f.s.Labels(f.ctx, proj)
	f.must(err)
	if !slices.Equal(labels, []string{"ui", "api"}) {
		t.Fatalf("labels = %v, want most recent first", labels)
	}
}

func TestCreateRules(t *testing.T) {
	f := newFixture(t)
	epic, _, one, _ := f.tree()
	_, err := f.s.Create(f.ctx, owner, NewCard{ProjectID: proj, Kind: KindSubtask, ParentID: one.ID, Title: "x"})
	wantCode(t, err, CodeInvalid) // a subtask holds nothing
	// The owner's card under an unconfirmed one is a proposal too, and
	// re-arms its parent's expiry rather than confirming it.
	f.must(f.s.StartPlanning(f.ctx, owner, epic.ID, "planner"))
	agent := Agent("planner", "")
	proposed := f.create(agent, epic.ID, KindStory, "Proposed story")
	if proposed.Confirmed() || proposed.ExpiresAt == nil || !proposed.ExpiresAt.Equal(f.clock.Now().Add(ExpiryWindow)) ||
		proposed.CreatedBy != "task:planner" || proposed.PinnedSHA != "" {
		t.Fatalf("agent-created card %+v", proposed)
	}
	f.clock.advance(time.Hour)
	under, err := f.s.Create(f.ctx, Owner("head-2"), NewCard{ProjectID: proj, Kind: KindSubtask, ParentID: proposed.ID, Title: "x"})
	f.must(err)
	rearmed := f.clock.Now().Add(ExpiryWindow)
	if got := f.card(proposed.ID); under.Confirmed() || !under.ExpiresAt.Equal(rearmed) || under.CreatedBy != AuthorOwner ||
		got.Confirmed() || !got.ExpiresAt.Equal(rearmed) || got.PinnedSHA != "" {
		t.Fatalf("owner card %+v under %+v", under, got)
	}
	// Agents create no epic under a card, by the kind rule the owner's cards
	// follow too, no story at the root, no epic there from a Task with a
	// scope, and nothing outside their scope.
	_, err = f.s.Create(f.ctx, agent, NewCard{ProjectID: proj, Kind: KindEpic, ParentID: epic.ID, Title: "x"})
	wantCode(t, err, CodeInvalid)
	_, err = f.s.Create(f.ctx, agent, NewCard{ProjectID: proj, Kind: KindStory, Title: "x"})
	wantCode(t, err, CodeForbidden)
	_, err = f.s.Create(f.ctx, agent, NewCard{ProjectID: proj, Kind: KindEpic, Title: "x"})
	wantCode(t, err, CodeForbidden)
	other := f.create(owner, "", KindEpic, "Other")
	_, err = f.s.Create(f.ctx, agent, NewCard{ProjectID: proj, Kind: KindStory, ParentID: other.ID, Title: "x"})
	wantCode(t, err, CodeForbidden)
	_, err = f.s.Create(f.ctx, Agent("nobody", ""), NewCard{ProjectID: proj, Kind: KindStory, ParentID: epic.ID, Title: "x"})
	wantCode(t, err, CodeForbidden)
	// Nothing goes under a cancelled card.
	_, err = f.s.SetStatus(f.ctx, owner, other.ID, StatusCancelled, "not now", false)
	f.must(err)
	_, err = f.s.Create(f.ctx, owner, NewCard{ProjectID: proj, Kind: KindStory, ParentID: other.ID, Title: "x"})
	wantCode(t, err, CodeInvalid)
}

// Decision 4: a Task with no scope proposes epics at the root, as proposals
// like any other: they expire, the root is their container for the
// unconfirmed cap, they count toward the created cap, and a duplicate root
// title is refused. Only epics go at the root, and a Task launched from a
// card or planning under one creates none there.
func TestAgentRootEpics(t *testing.T) {
	f := newFixture(t)
	f.create(owner, "", KindEpic, "Owner's epic")
	free := Agent("free", "sub")
	first := f.create(free, "", KindEpic, "Proposed epic")
	if first.Confirmed() || first.ExpiresAt == nil || !first.ExpiresAt.Equal(f.clock.Now().Add(ExpiryWindow)) ||
		first.CreatedBy != "task:free" || first.ParentID != "" || first.Status != StatusPlanned || first.PinnedSHA != "" {
		t.Fatalf("agent epic %+v", first)
	}
	for _, in := range []struct {
		card NewCard
		code Code
	}{
		{NewCard{Kind: KindStory, Title: "Loose story"}, CodeForbidden},
		{NewCard{Kind: KindSubtask, Title: "Loose subtask"}, CodeForbidden},
		{NewCard{Kind: KindEpic, ParentID: first.ID, Title: "Nested"}, CodeInvalid},
		{NewCard{Kind: KindEpic, Title: " proposed EPIC "}, CodeDuplicate},
		{NewCard{Kind: KindEpic, Title: "Owner's epic"}, CodeDuplicate},
	} {
		in.card.ProjectID = proj
		_, err := f.s.Create(f.ctx, free, in.card)
		wantCode(t, err, in.code)
	}
	_, err := f.s.Create(f.ctx, free, NewCard{Kind: KindEpic, Title: "Unassigned"})
	wantCode(t, err, CodeReadOnly)
	// Planning under a card, or working on a root subtask, scopes the Task.
	f.must(f.s.StartPlanning(f.ctx, owner, "#1", "planner"))
	f.launch(f.create(owner, "", KindSubtask, "Loose").ID, "worker")
	for _, task := range []string{"planner", "worker"} {
		_, err := f.s.Create(f.ctx, Agent(task, ""), NewCard{ProjectID: proj, Kind: KindEpic, Title: "From " + task})
		wantCode(t, err, CodeForbidden)
	}

	// The root is one container for the unconfirmed cap.
	var proposed []Card
	for i := range CapUnconfirmed - 1 {
		proposed = append(proposed, f.create(free, "", KindEpic, fmt.Sprintf("Epic %d", i)))
	}
	_, err = f.s.Create(f.ctx, free, NewCard{ProjectID: proj, Kind: KindEpic, Title: "One too many"})
	wantCode(t, err, CodeLimit)
	// The owner confirms one and dismisses another; each frees a place.
	c, err := f.s.Confirm(f.ctx, Owner("head-2"), first.ID)
	f.must(err)
	if !c.Confirmed() || c.ExpiresAt != nil || c.PinnedSHA != "head-2" {
		t.Fatalf("confirmed agent epic %+v", c)
	}
	f.create(free, "", KindEpic, "After the confirm")
	if c, err = f.s.Dismiss(f.ctx, owner, proposed[0].ID); err != nil || c.Status != StatusCancelled {
		t.Fatalf("dismissed %+v, %v", c, err)
	}
	f.create(free, "", KindEpic, "After the dismiss")

	// A Task planning under a proposed epic adds proposals of its own there.
	f.must(f.s.StartPlanning(f.ctx, owner, proposed[1].ID, "decomposer"))
	story := f.create(Agent("decomposer", ""), proposed[1].ID, KindStory, "Story under a proposal")
	leaf := f.create(Agent("decomposer", ""), story.ID, KindSubtask, "Leaf under a proposal")

	// Unconfirmed, they expire with the window, and a root proposal takes
	// its proposed subtree with it.
	f.clock.advance(ExpiryWindow)
	f.create(owner, "", KindEpic, "A write sweeps")
	for _, c := range f.children("") {
		if c.CreatedBy == "task:free" && !c.Confirmed() && c.Status != StatusCancelled {
			t.Fatalf("%s outlived its expiry: %+v", c.Title, c)
		}
	}
	swept := f.card(proposed[1].ID)
	for _, id := range []string{story.ID, leaf.ID} {
		if c := f.card(id); c.Status != StatusCancelled || c.CascadeID == "" || c.CascadeID != swept.CascadeID || !hasComment(f.comments(id), "uam: expired unconfirmed") {
			t.Fatalf("%s under the swept epic = %+v", c.Title, c)
		}
	}
	// And they count toward the created cap: with nothing left to confirm,
	// the Task's 21st card is refused.
	for i := range CapCreated - 12 {
		f.create(free, "", KindEpic, fmt.Sprintf("Late %d", i))
	}
	_, err = f.s.Create(f.ctx, free, NewCard{ProjectID: proj, Kind: KindEpic, Title: "Past the cap"})
	wantCode(t, err, CodeLimit)
	if !strings.Contains(err.Error(), fmt.Sprintf("at most %d cards", CapCreated)) {
		t.Fatalf("past the created cap: %v", err)
	}
}

// A Task's scope includes every card it created that has not started, and
// everything under one, so a Task with no scope builds out the epics it
// proposes from one request: stories, subtasks and links at each level,
// within the caps. Cards it did not create stay out of reach: another
// Task's proposals and the owner's cards, confirmed or not, unless under its
// own card. A started card keeps its plan, and the Task still starts no work.
func TestAgentBuildsOutItsEpics(t *testing.T) {
	f := newFixture(t)
	free := Agent("free", "sub")
	e1 := f.create(free, "", KindEpic, "Epic one")
	e2 := f.create(free, "", KindEpic, "Epic two")
	f.must(f.s.Link(f.ctx, free, e1.ID, e2.ID))
	s1 := f.create(free, e1.ID, KindStory, "Story one")
	s2 := f.create(free, e1.ID, KindStory, "Story two")
	f.must(f.s.Link(f.ctx, free, s1.ID, s2.ID))
	l1 := f.create(free, s1.ID, KindSubtask, "Leaf one")
	l2 := f.create(free, s1.ID, KindSubtask, "Leaf two")
	f.must(f.s.Link(f.ctx, free, l1.ID, l2.ID))
	f.must(f.s.Unlink(f.ctx, free, l1.ID, l2.ID))
	f.must(f.s.Link(f.ctx, free, l1.ID, l2.ID))
	if c := f.card(l2.ID); c.Confirmed() || c.ExpiresAt == nil || !slices.Equal(c.BlockedBy, []string{l1.ID}) {
		t.Fatalf("proposed leaf %+v", c)
	}
	res, err := f.s.Edit(f.ctx, free, s2.ID, Patch{Title: ptr("Story two, renamed")})
	f.must(err)
	if res.Request != nil || res.Card.Title != "Story two, renamed" {
		t.Fatalf("edit of its own story = %+v", res)
	}
	// Levels still hold, and owner-only fields stay the owner's.
	wantCode(t, f.s.Link(f.ctx, free, s1.ID, l1.ID), CodeInvalid)
	_, err = f.s.Edit(f.ctx, free, l1.ID, Patch{AcceptCmd: &sql.NullString{String: "make test", Valid: true}})
	wantCode(t, err, CodeForbidden)

	// Cards it did not create stay out of reach: another Task's proposal and
	// the owner's confirmed cards.
	theirs := f.create(Agent("other", ""), "", KindEpic, "Their epic")
	ownerEpic, ownerStory, ownerLeaf, _ := f.tree()
	for _, c := range []Card{theirs, ownerEpic, ownerStory} {
		_, err = f.s.Create(f.ctx, free, NewCard{ProjectID: proj, Kind: KindSubtask, ParentID: c.ID, Title: "Mine"})
		wantCode(t, err, CodeForbidden)
	}
	for _, c := range []Card{theirs, ownerEpic, ownerStory, ownerLeaf} {
		_, err = f.s.Edit(f.ctx, free, c.ID, Patch{Title: ptr("Taken")})
		wantCode(t, err, CodeForbidden)
	}
	wantCode(t, f.s.Link(f.ctx, free, e1.ID, theirs.ID), CodeForbidden)
	wantCode(t, f.s.Link(f.ctx, free, theirs.ID, ownerEpic.ID), CodeForbidden)
	_, err = f.s.Create(f.ctx, Agent("other", ""), NewCard{ProjectID: proj, Kind: KindStory, ParentID: e1.ID, Title: "Theirs in mine"})
	wantCode(t, err, CodeForbidden)
	_, err = f.s.Edit(f.ctx, free, ownerStory.ID, Patch{ParentID: &e1.ID})
	wantCode(t, err, CodeForbidden)
	_, err = f.s.Edit(f.ctx, free, f.create(free, e1.ID, KindStory, "Story three").ID, Patch{ParentID: &ownerEpic.ID})
	wantCode(t, err, CodeForbidden)
	// The owner's card added under the Task's own story is part of its plan.
	added := f.create(owner, s1.ID, KindSubtask, "Owner's addition")
	f.must(f.s.Link(f.ctx, free, added.ID, l1.ID))

	// The unconfirmed cap counts per container under its own cards too.
	for i := range CapUnconfirmed {
		f.create(free, e2.ID, KindStory, fmt.Sprintf("More %d", i))
	}
	_, err = f.s.Create(f.ctx, free, NewCard{ProjectID: proj, Kind: KindStory, ParentID: e2.ID, Title: "One too many"})
	wantCode(t, err, CodeLimit)

	// It starts no work, and a started card keeps its plan.
	_, err = f.s.Claim(f.ctx, free, l2.ID, Baseline{Head: "base"})
	wantCode(t, err, CodeForbidden)
	_, err = f.s.Launch(f.ctx, owner, l2.ID, "worker", Baseline{Head: "base"}, true)
	f.must(err)
	if res, err = f.s.Edit(f.ctx, free, l2.ID, Patch{Title: ptr("Changed")}); err != nil || res.Request == nil || f.card(l2.ID).Title != "Leaf two" {
		t.Fatalf("edit of its started subtask = %+v, %v; want a change request", res, err)
	}
	wantCode(t, f.s.Unlink(f.ctx, free, l1.ID, l2.ID), CodeInProgress)
	f.must(f.s.Unlink(f.ctx, free, s1.ID, s2.ID))
	_, err = f.s.Edit(f.ctx, free, s1.ID, Patch{ParentID: &e2.ID})
	wantCode(t, err, CodeInProgress)
	// Its other cards stay plannable, though the launch confirmed them.
	f.create(free, s2.ID, KindSubtask, "Still planning")
	f.invariants()
}

// Test plan 18: the duplicate-title refusal ignores cancelled siblings.
func TestDuplicateTitles(t *testing.T) {
	f := newFixture(t)
	_, story, one, two := f.tree()
	_, err := f.s.Create(f.ctx, owner, NewCard{ProjectID: proj, Kind: KindSubtask, ParentID: story.ID, Title: "  ONE "})
	wantCode(t, err, CodeDuplicate)
	title := "one"
	_, err = f.s.Edit(f.ctx, owner, two.ID, Patch{Title: &title})
	wantCode(t, err, CodeDuplicate)
	// A card at the root is not a sibling of one under the story.
	f.create(owner, "", KindSubtask, "One")
	_, err = f.s.SetStatus(f.ctx, owner, one.ID, StatusCancelled, "replaced", false)
	f.must(err)
	f.create(owner, story.ID, KindSubtask, "one")
	_, err = f.s.Edit(f.ctx, owner, two.ID, Patch{Title: &title})
	wantCode(t, err, CodeDuplicate)
	// Restoring a card whose title is now taken is refused too.
	_, err = f.s.Restore(f.ctx, owner, one.ID, "back")
	wantCode(t, err, CodeDuplicate)
}

// Test plan 17: caps count per Task, and subagents count against it.
func TestCaps(t *testing.T) {
	f := newFixture(t)
	epic, story, _, _ := f.tree()
	f.must(f.s.StartPlanning(f.ctx, owner, epic.ID, "planner"))
	main, sub := Agent("planner", ""), Agent("planner", "sub-1")
	for i := range CapUnconfirmed {
		a := main
		if i%2 == 1 {
			a = sub
		}
		f.create(a, story.ID, KindSubtask, "s"+string(rune('a'+i)))
	}
	_, err := f.s.Create(f.ctx, sub, NewCard{ProjectID: proj, Kind: KindSubtask, ParentID: story.ID, Title: "over"})
	wantCode(t, err, CodeLimit)
	// Another Task has its own count.
	f.must(f.s.StartPlanning(f.ctx, owner, epic.ID, "other"))
	f.create(Agent("other", ""), story.ID, KindSubtask, "other's")
	// The created cap spans containers.
	for i := range CapCreated - CapUnconfirmed {
		f.create(main, epic.ID, KindStory, "story "+string(rune('a'+i)))
	}
	_, err = f.s.Create(f.ctx, main, NewCard{ProjectID: proj, Kind: KindSubtask, ParentID: epic.ID, Title: "one more"})
	wantCode(t, err, CodeLimit)
	// Comments: CapComments per card per Task, automatic ones exempt.
	for i := range CapComments {
		a := main
		if i%2 == 1 {
			a = sub
		}
		_, err := f.s.AddComment(f.ctx, a, story.ID, "note")
		f.must(err)
	}
	_, err = f.s.AddComment(f.ctx, sub, story.ID, "one too many")
	wantCode(t, err, CodeLimit)
	_, err = f.s.AddComment(f.ctx, Agent("other", ""), story.ID, "mine")
	f.must(err)
	_, err = f.s.AddComment(f.ctx, owner, story.ID, "owner's")
	f.must(err)
	_, err = f.s.AddComment(f.ctx, owner, story.ID, " ")
	wantCode(t, err, CodeInvalid)
}

func TestEditOwner(t *testing.T) {
	f := newFixture(t)
	epic, story, one, two := f.tree()
	f.must(f.s.StartPlanning(f.ctx, owner, epic.ID, "planner"))
	proposed := f.create(Agent("planner", ""), story.ID, KindSubtask, "Proposed")
	title, desc, win, due, effort, prio := "Renamed", "body", "tests pass", "2026-12-31", "M", PrioMedium
	labels, checklist, paths := []string{"x"}, []Check{{Text: "a", Done: true}}, []string{"internal/**", "*.go"}
	blocked := true
	cmd := sql.NullString{String: "go test ./...", Valid: true}
	res, err := f.s.Edit(f.ctx, Owner("head-2"), proposed.ID, Patch{Title: &title, Desc: &desc, WinCondition: &win, Due: &due,
		Effort: &effort, Prio: &prio, Labels: &labels, Checklist: &checklist, Paths: &paths, Blocked: &blocked, AcceptCmd: &cmd})
	f.must(err)
	c := res.Card
	// Editing plans, so the proposal stays one, with a fresh expiry.
	if res.Request != nil || c.Confirmed() || !c.ExpiresAt.Equal(f.clock.Now().Add(ExpiryWindow)) || c.PinnedSHA != "" || c.Title != title || c.Desc != desc ||
		c.WinCondition != win || c.Due != due || c.Effort != effort || c.Prio != prio || !c.Blocked ||
		c.AcceptCmd == nil || *c.AcceptCmd != cmd.String || !slices.Equal(c.Paths, paths) || !slices.Equal(c.Labels, labels) {
		t.Fatalf("owner edit = %+v", c)
	}
	// The acceptance command is tri-state. Setting the Project default is a
	// change to the Project's Board.
	before, changes := f.revision(), len(f.changes)
	f.must(f.s.SetProjectAcceptCmd(f.ctx, owner, proj, "make check"))
	if f.revision() != before+1 || len(f.changes) != changes+1 || f.changes[changes].ProjectID != proj || len(f.changes[changes].Cards) != 0 {
		t.Fatalf("setting the default: revision %d → %d, changes %+v", before, f.revision(), f.changes[changes:])
	}
	for _, tc := range []struct {
		set  sql.NullString
		want string
	}{{sql.NullString{}, "make check"}, {sql.NullString{Valid: true}, ""}, {cmd, cmd.String}} {
		set := tc.set
		unblock := false
		clear := []Check{}
		_, err := f.s.Edit(f.ctx, owner, proposed.ID, Patch{AcceptCmd: &set, Blocked: &unblock, Checklist: &clear})
		f.must(err)
		fin, err := f.s.CheckFinishable(f.ctx, owner, proposed.ID)
		f.must(err)
		if fin.AcceptCmd != tc.want {
			t.Fatalf("accept command for %+v = %q, want %q", tc.set, fin.AcceptCmd, tc.want)
		}
	}
	settings, err := f.s.ProjectSettings(f.ctx, proj)
	if err != nil || settings.AcceptCmd != "make check" {
		t.Fatalf("settings = %+v, %v", settings, err)
	}
	wantCode(t, f.s.SetProjectAcceptCmd(f.ctx, owner, "", "x"), CodeReadOnly)
	wantCode(t, f.s.SetProjectAcceptCmd(f.ctx, owner, proj, strings.Repeat("x", maxTextBytes+1)), CodeInvalid)
	// Invalid owner fields.
	for _, p := range []Patch{
		{Paths: &[]string{"["}}, {Paths: &[]string{" "}}, {Paths: &[]string{"a\nb"}},
		{Paths: ptr(slices.Repeat([]string{"a"}, maxListItems+1))},
		{AcceptCmd: &sql.NullString{Valid: true, String: strings.Repeat("x", maxTextBytes+1)}},
		{}, {Rank: ptr(-1)},
	} {
		_, err := f.s.Edit(f.ctx, owner, one.ID, p)
		wantCode(t, err, CodeInvalid)
	}
	// Moving: ranks, kinds, cycles.
	other := f.create(owner, epic.ID, KindStory, "Other story")
	root := ""
	res, err = f.s.Edit(f.ctx, owner, two.ID, Patch{ParentID: &other.ID})
	f.must(err)
	if res.Card.ParentID != other.ID || res.Card.Rank != 0 {
		t.Fatalf("moved = %+v", res.Card)
	}
	res, err = f.s.Edit(f.ctx, owner, one.ID, Patch{ParentID: &other.ID, Rank: ptr(0)})
	f.must(err)
	if res.Card.Rank != 0 || f.card(two.ID).Rank != 1 {
		t.Fatalf("ranks = %d %d, want 0 1", res.Card.Rank, f.card(two.ID).Rank)
	}
	res, err = f.s.Edit(f.ctx, owner, one.ID, Patch{Rank: ptr(9)})
	f.must(err)
	if res.Card.Rank != 1 || f.card(two.ID).Rank != 0 {
		t.Fatal("rank past the end did not append")
	}
	if res, err = f.s.Edit(f.ctx, owner, two.ID, Patch{ParentID: &root}); err != nil || res.Card.ParentID != "" {
		t.Fatalf("move to root = %+v, %v", res.Card, err)
	}
	for _, tc := range []struct {
		ref, parent string
		code        Code
	}{
		{epic.ID, other.ID, CodeInvalid}, // a story cannot hold an epic
		{story.ID, one.ID, CodeInvalid},  // a subtask holds nothing
		{"#99", root, CodeNotFound},
		{one.ID, "#99", CodeNotFound},
	} {
		parent := tc.parent
		_, err := f.s.Edit(f.ctx, owner, tc.ref, Patch{ParentID: &parent})
		wantCode(t, err, tc.code)
	}
	loose := f.create(owner, "", KindSubtask, "Loose")
	elsewhere, err := f.s.Create(f.ctx, owner, NewCard{ProjectID: "p2", Kind: KindEpic, Title: "Elsewhere"})
	f.must(err)
	_, err = f.s.Edit(f.ctx, owner, story.ID, Patch{ParentID: &elsewhere.ID})
	wantCode(t, err, CodeInvalid)
	p2 := "p2"
	_, err = f.s.Edit(f.ctx, owner, story.ID, Patch{ProjectID: &p2})
	wantCode(t, err, CodeInvalid)
	same := proj
	if _, err := f.s.Edit(f.ctx, owner, story.ID, Patch{ProjectID: &same, Title: ptr("Story renamed")}); err != nil {
		t.Fatal(err)
	}
	// A cancelled card must be restored before it is edited.
	_, err = f.s.SetStatus(f.ctx, owner, other.ID, StatusCancelled, "drop", false)
	f.must(err)
	_, err = f.s.Edit(f.ctx, owner, other.ID, Patch{Title: ptr("x")})
	wantCode(t, err, CodeInvalid)
	_, err = f.s.Edit(f.ctx, owner, loose.ID, Patch{ParentID: &other.ID})
	wantCode(t, err, CodeInvalid)
}

func ptr[T any](v T) *T { return &v }

// Test plan 1 (store part): an agent update carrying accept_cmd, paths,
// blocked or project is refused before any write.
func TestAgentEdits(t *testing.T) {
	f := newFixture(t)
	epic, story, one, _ := f.tree()
	f.launch(one.ID, "task-1")
	agent := Agent("task-1", "sub")
	before := f.revision()
	for _, p := range []Patch{
		{AcceptCmd: &sql.NullString{Valid: true, String: "rm -rf /"}}, {Paths: &[]string{"*"}},
		{Blocked: ptr(true)}, {ProjectID: ptr("p2")},
	} {
		_, err := f.s.Edit(f.ctx, agent, one.ID, p)
		wantCode(t, err, CodeForbidden)
	}
	if f.revision() != before {
		t.Fatal("a refused owner-only field reached the database")
	}
	// On an unconfirmed card in scope the edit applies directly.
	mine := f.create(agent, story.ID, KindSubtask, "Mine")
	res, err := f.s.Edit(f.ctx, agent, mine.ID, Patch{Title: ptr("Mine, renamed"), Rank: ptr(0)})
	f.must(err)
	if res.Request != nil || res.Card.Title != "Mine, renamed" || res.Card.Confirmed() || res.Card.Rank != 0 {
		t.Fatalf("agent edit = %+v", res)
	}
	// On a started card it becomes a change request; a newer one replaces it.
	first, err := f.s.Edit(f.ctx, agent, one.ID, Patch{Title: ptr("One v2")})
	f.must(err)
	second, err := f.s.Edit(f.ctx, agent, one.ID, Patch{Title: ptr("One v3"), Desc: ptr("why")})
	f.must(err)
	if first.Request == nil || second.Request == nil || f.card(one.ID).Title != "One" {
		t.Fatalf("change requests = %+v %+v", first.Request, second.Request)
	}
	old, err := f.s.Request(f.ctx, first.Request.ID)
	f.must(err)
	if old.Status != RequestWithdrawn || second.Request.Kind != RequestChange || second.Request.AgentID != "sub" {
		t.Fatalf("older = %s, newer = %+v", old.Status, second.Request)
	}
	// A change that could never apply is refused at filing.
	_, err = f.s.Edit(f.ctx, agent, one.ID, Patch{Title: ptr("Two")})
	wantCode(t, err, CodeDuplicate)
	// It applies once the subtask is released.
	_, err = f.s.Accept(f.ctx, Owner("head-9"), second.Request.ID, "ok")
	wantCode(t, err, CodeInProgress)
	_, err = f.s.ReleaseHold(f.ctx, owner, one.ID, ReleaseOwner, "")
	f.must(err)
	req, err := f.s.Accept(f.ctx, Owner("head-9"), second.Request.ID, "ok")
	f.must(err)
	got := f.card(one.ID)
	if req.Status != RequestAccepted || req.DecisionComment != "ok" || got.Title != "One v3" || got.Desc != "why" || got.PinnedSHA != "head-9" {
		t.Fatalf("accepted change: %+v, card %+v", req, got)
	}
	// Agents reparent within scope, never to the root.
	root := ""
	_, err = f.s.Edit(f.ctx, agent, mine.ID, Patch{ParentID: &root})
	wantCode(t, err, CodeForbidden)
	_, err = f.s.Edit(f.ctx, agent, mine.ID, Patch{ParentID: &epic.ID})
	wantCode(t, err, CodeForbidden) // an epic can hold a subtask, but the epic is outside the scope
	outside := f.create(owner, "", KindSubtask, "Outside")
	_, err = f.s.Edit(f.ctx, agent, outside.ID, Patch{Title: ptr("x")})
	wantCode(t, err, CodeForbidden)
}

// An agent limited to proposals, a Utility job, edits its proposals and
// never files a change request: its edit of a confirmed card is refused in
// the edit's own transaction.
func TestProposalsOnlyAgentEdits(t *testing.T) {
	f := newFixture(t)
	_, story, one, _ := f.tree()
	f.must(f.s.StartPlanning(f.ctx, owner, story.ID, "job-1"))
	job := Agent("job-1", "")
	job.Proposals = true
	mine := f.create(job, story.ID, KindSubtask, "Proposed")
	res, err := f.s.Edit(f.ctx, job, mine.ID, Patch{Desc: ptr("refined")})
	f.must(err)
	if res.Request != nil || res.Card.Desc != "refined" || res.Card.Confirmed() {
		t.Fatalf("proposal edit = %+v", res)
	}
	before := f.revision()
	_, err = f.s.Edit(f.ctx, job, one.ID, Patch{Title: ptr("One v2")})
	wantCode(t, err, CodeForbidden)
	if d := f.detail(one.ID); f.revision() != before || len(d.Requests) != 0 || d.Card.Title != "One" {
		t.Fatalf("a refused edit wrote: requests %+v, card %+v", d.Requests, d.Card)
	}
	// Once the owner confirms the proposal, it is out of the job's reach too.
	_, err = f.s.Confirm(f.ctx, owner, mine.ID)
	f.must(err)
	_, err = f.s.Edit(f.ctx, job, mine.ID, Patch{Desc: ptr("again")})
	wantCode(t, err, CodeForbidden)
}

func TestMoveOutOfUnassigned(t *testing.T) {
	f := newFixture(t)
	epic := f.create(owner, "", KindEpic, "Epic")
	id := f.unassigned("Imported")
	f.unassigned("Epic")
	for _, err := range []error{
		errOf(f.s.Edit(f.ctx, owner, id, Patch{Title: ptr("x")})),
		errOf(f.s.AddComment(f.ctx, owner, id, "x")),
		errOf(f.s.Checklist(f.ctx, owner, id, ChecklistEdit{Add: []string{"x"}})),
		errOf(f.s.Confirm(f.ctx, owner, id)),
		errOf(f.s.Launch(f.ctx, owner, id, "t", Baseline{}, false)),
		errOf(f.s.Purge(f.ctx, owner, "")),
		errOf(f.s.Edit(f.ctx, owner, id, Patch{ProjectID: ptr("")})),
		f.s.Link(f.ctx, owner, id, id),
		f.s.StartPlanning(f.ctx, owner, id, "t"),
	} {
		wantCode(t, err, CodeReadOnly)
	}
	_, err := f.s.Edit(f.ctx, owner, "#3", Patch{ProjectID: ptr(proj)})
	wantCode(t, err, CodeDuplicate) // "Epic" is taken at the root
	res, err := f.s.Edit(f.ctx, Owner("head-7"), id, Patch{ProjectID: ptr(proj), ParentID: &epic.ID, Title: ptr("Moved")})
	f.must(err)
	c := res.Card
	if c.ProjectID != proj || c.ParentID != epic.ID || c.Title != "Moved" || c.PinnedSHA != "head-7" || !c.Confirmed() {
		t.Fatalf("moved card = %+v", c)
	}
	un, err := f.s.Board(f.ctx, "")
	f.must(err)
	if len(un.Cards) != 1 {
		t.Fatalf("Unassigned holds %d cards, want 1", len(un.Cards))
	}
	hits, err := f.s.List(f.ctx, proj, Filter{Query: "moved"})
	f.must(err)
	if len(hits) != 1 || hits[0].ID != id {
		t.Fatalf("search in the new Project = %+v", hits)
	}
}

func TestChecklistAndConfirm(t *testing.T) {
	f := newFixture(t)
	epic, story, one, two := f.tree()
	f.must(f.s.StartPlanning(f.ctx, owner, story.ID, "story-planner"))
	_, err := f.s.Checklist(f.ctx, Agent("story-planner", ""), one.ID, ChecklistEdit{Add: []string{" a ", "b", "c"}})
	f.must(err)
	_, err = f.s.Checklist(f.ctx, Agent("story-planner", ""), two.ID, ChecklistEdit{Add: []string{" "}})
	wantCode(t, err, CodeInvalid)
	f.launch(one.ID, "task-1")
	agent := Agent("task-1", "")
	// Started, its items are its plan; only its Task and the owner tick them.
	_, err = f.s.Checklist(f.ctx, agent, one.ID, ChecklistEdit{Add: []string{"d"}})
	wantCode(t, err, CodeInProgress)
	_, err = f.s.Checklist(f.ctx, Agent("story-planner", ""), one.ID, ChecklistEdit{Tick: []int{0}})
	wantCode(t, err, CodeForbidden)
	c, err := f.s.Checklist(f.ctx, agent, one.ID, ChecklistEdit{Tick: []int{0, 1}, Untick: []int{1}})
	f.must(err)
	if want := []Check{{Text: "a", Done: true}, {Text: "b"}, {Text: "c"}}; !slices.Equal(c.Checklist, want) {
		t.Fatalf("checklist = %+v", c.Checklist)
	}
	for _, e := range []ChecklistEdit{{Tick: []int{3}}, {Untick: []int{-1}}} {
		_, err := f.s.Checklist(f.ctx, agent, one.ID, e)
		wantCode(t, err, CodeInvalid)
	}
	outside := f.create(owner, "", KindSubtask, "Outside")
	_, err = f.s.Checklist(f.ctx, agent, outside.ID, ChecklistEdit{Add: []string{"x"}})
	wantCode(t, err, CodeForbidden)
	_, err = f.s.SetStatus(f.ctx, owner, outside.ID, StatusCancelled, "drop", false)
	f.must(err)
	_, err = f.s.Checklist(f.ctx, owner, outside.ID, ChecklistEdit{Add: []string{"x"}})
	wantCode(t, err, CodeInvalid)
	// Confirm takes the unconfirmed ancestors with it, never a cancelled card.
	f.must(f.s.StartPlanning(f.ctx, owner, epic.ID, "planner"))
	planner := Agent("planner", "")
	s2 := f.create(planner, epic.ID, KindStory, "Suggested story")
	leaf := f.create(planner, s2.ID, KindSubtask, "Suggested leaf")
	got, err := f.s.Confirm(f.ctx, owner, leaf.ID)
	f.must(err)
	if story := f.card(s2.ID); !got.Confirmed() || got.PinnedSHA != "head-1" || !story.Confirmed() || story.PinnedSHA != "head-1" {
		t.Fatalf("confirmed %+v under %+v", got, story)
	}
	_, err = f.s.Confirm(f.ctx, owner, outside.ID)
	wantCode(t, err, CodeInvalid)
	_ = story
}

func TestListAndSearch(t *testing.T) {
	f := newFixture(t)
	epic, story, one, two := f.tree()
	_, err := f.s.Edit(f.ctx, owner, one.ID, Patch{Desc: ptr("authentication logging"), Labels: &[]string{"backend"}})
	f.must(err)
	for _, tc := range []struct {
		f    Filter
		want []string
	}{
		{Filter{}, []string{epic.ID, story.ID, one.ID, two.ID}},
		{Filter{Query: "authentication log"}, []string{one.ID}},
		{Filter{Query: "auth log"}, nil},
		{Filter{Query: "backend"}, []string{one.ID}},
		{Filter{Query: `"quoted" stuff`}, nil},
		{Filter{Kind: KindSubtask}, []string{one.ID, two.ID}},
		{Filter{Status: StatusPlanned, Kind: KindStory}, []string{story.ID}},
		{Filter{Parent: story.ID}, []string{one.ID, two.ID}},
		{Filter{Parent: "#1", Query: "sto"}, []string{story.ID}},
	} {
		got, err := f.s.List(f.ctx, proj, tc.f)
		f.must(err)
		var ids []string
		for _, c := range got {
			ids = append(ids, c.ID)
		}
		if !slices.Equal(ids, tc.want) {
			t.Errorf("List(%+v) = %v, want %v", tc.f, ids, tc.want)
		}
	}
	_, err = f.s.List(f.ctx, proj, Filter{Query: strings.Repeat("x", maxSearchBytes+1)})
	wantCode(t, err, CodeInvalid)
	_, err = f.s.List(f.ctx, proj, Filter{Parent: "#99"})
	wantCode(t, err, CodeNotFound)
}

func TestSimilar(t *testing.T) {
	f := newFixture(t)
	a := f.create(owner, "", KindSubtask, "Fix login redirect loop")
	f.create(owner, "", KindSubtask, "Login page styling")
	f.create(owner, "", KindSubtask, "Unrelated database work")
	hits, err := f.s.Similar(f.ctx, proj, "login redirect loop broken", "", 5)
	f.must(err)
	if len(hits) == 0 || hits[0].ID != a.ID || hits[0].Status != StatusPlanned {
		t.Fatalf("hits = %+v", hits)
	}
	if hits, err = f.s.Similar(f.ctx, proj, "login redirect loop", a.ID, 1); err != nil || len(hits) > 1 {
		t.Fatalf("excluding the card: %+v, %v", hits, err)
	}
	if hits, _ := f.s.Similar(f.ctx, proj, "  ", "", 5); hits != nil {
		t.Fatal("blank query matched")
	}
	if hits, _ := f.s.Similar(f.ctx, proj, "login", "", 0); hits != nil {
		t.Fatal("zero limit matched")
	}
	_, err = f.s.Similar(f.ctx, proj, strings.Repeat("x ", maxSearchBytes), "", 5)
	wantCode(t, err, CodeInvalid)
}

func TestSimilarityAndQueries(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want float64
	}{
		{"Fix login bug", "fix LOGIN bug!", 1},
		{"add api", "", 0},
		{"alpha beta", "alpha gamma", 0.5},
		{"a b c", "a b c", 0}, // tokens under three runes are ignored
	} {
		if got := Similarity(tc.a, tc.b); got != tc.want {
			t.Errorf("Similarity(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
	if got := ftsSearchQuery(`auth "log`); got != `"auth" AND """log"*` {
		t.Errorf("ftsSearchQuery = %s", got)
	}
	if got := ftsAnyQuery("a b"); got != `"a" OR "b"` {
		t.Errorf("ftsAnyQuery = %s", got)
	}
	if got := len(quotedTokens(strings.Repeat("w ", 20))); got != maxSearchTokens {
		t.Errorf("tokens = %d, want %d", got, maxSearchTokens)
	}
}

// Every owner touch on a card (Confirm, launch, a status change, an
// acceptance, a restore, or moving a confirmed card under a proposal)
// confirms and pins its unconfirmed ancestors, leaves confirmed ones alone,
// and so keeps them from the sweep. Planning writes don't (decision 10).
func TestOwnerTouchConfirmsAncestors(t *testing.T) {
	head := Owner("head-9")
	for _, tc := range []struct {
		name string
		op   func(f *fixture, epic, story, leaf Card) error
	}{
		{"confirm", func(f *fixture, _, _, leaf Card) error {
			_, err := f.s.Confirm(f.ctx, head, leaf.ID)
			return err
		}},
		{"launch", func(f *fixture, _, _, leaf Card) error {
			_, err := f.s.Launch(f.ctx, head, leaf.ID, "task-9", Baseline{}, true)
			return err
		}},
		{"done", func(f *fixture, _, _, leaf Card) error {
			_, err := f.s.SetStatus(f.ctx, head, leaf.ID, StatusDone, "shipped", false)
			return err
		}},
		{"ready", func(f *fixture, _, _, leaf Card) error {
			_, err := f.s.SetStatus(f.ctx, head, leaf.ID, StatusTodo, "", false)
			return err
		}},
		{"move", func(f *fixture, epic, story, _ Card) error {
			x := f.create(owner, epic.ID, KindSubtask, "X")
			_, err := f.s.Edit(f.ctx, head, x.ID, Patch{ParentID: &story.ID})
			return err
		}},
		{"accept change", func(f *fixture, epic, story, _ Card) error {
			x := f.create(owner, epic.ID, KindSubtask, "X")
			res, err := f.s.Edit(f.ctx, Agent("planner", ""), x.ID, Patch{ParentID: &story.ID})
			if err != nil {
				return err
			}
			_, err = f.s.Accept(f.ctx, head, res.Request.ID, "")
			return err
		}},
		{"accept blocked", func(f *fixture, epic, _, leaf Card) error {
			worker := Agent("worker", "")
			first := f.create(owner, epic.ID, KindSubtask, "First")
			f.launch(first.ID, "worker")
			f.done(first.ID, worker)
			r, err := f.s.FileRequest(f.ctx, worker, leaf.ID, RequestInput{Kind: RequestBlocked, Comment: "stuck"})
			if err != nil {
				return err
			}
			_, err = f.s.Accept(f.ctx, head, r.ID, "")
			return err
		}},
		{"restore", func(f *fixture, _, _, leaf Card) error {
			if _, err := f.s.Dismiss(f.ctx, owner, leaf.ID); err != nil {
				return err
			}
			_, err := f.s.Restore(f.ctx, head, leaf.ID, "back")
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			epic := f.create(owner, "", KindEpic, "Epic")
			f.must(f.s.StartPlanning(f.ctx, owner, epic.ID, "planner"))
			story := f.create(Agent("planner", ""), epic.ID, KindStory, "Suggested")
			leaf := f.create(Agent("planner", ""), story.ID, KindSubtask, "Suggested leaf")
			f.must(tc.op(f, epic, story, leaf))
			got := f.card(story.ID)
			if !got.Confirmed() || got.PinnedSHA != "head-9" {
				t.Fatalf("story after %s = %+v", tc.name, got)
			}
			if e := f.card(epic.ID); e.PinnedSHA != "head-1" {
				t.Fatalf("a confirmed ancestor was re-pinned: %+v", e)
			}
			f.clock.advance(2 * ExpiryWindow)
			_, err := f.s.Sweep(f.ctx)
			f.must(err)
			if got := f.card(story.ID); got.Status == StatusCancelled {
				t.Fatalf("the sweep expired the story after %s", tc.name)
			}
		})
	}
}

// The owner's planning writes on a proposal (edit, rank, move, checklist,
// link, split, and a new card under it) never confirm it or its proposed
// parents: only Confirm and launching do (decision 10). Each write but a
// link re-arms the expiry of the card and its proposed parents, so a
// proposal the owner is working on outlives its first 14 days.
func TestOwnerPlanningKeepsProposals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rearm bool
		op    func(f *fixture, story, leaf, other Card) error
	}{
		{"save", true, func(f *fixture, _, leaf, _ Card) error {
			_, err := f.s.Edit(f.ctx, owner, leaf.ID, Patch{Desc: ptr("x")})
			return err
		}},
		{"rank", true, func(f *fixture, _, leaf, _ Card) error {
			_, err := f.s.Edit(f.ctx, owner, leaf.ID, Patch{Rank: ptr(1)})
			return err
		}},
		{"move", true, func(f *fixture, story, leaf, _ Card) error {
			_, err := f.s.Edit(f.ctx, owner, leaf.ID, Patch{ParentID: &story.ID, Rank: ptr(1)})
			return err
		}},
		{"checklist", true, func(f *fixture, _, leaf, _ Card) error {
			_, err := f.s.Checklist(f.ctx, owner, leaf.ID, ChecklistEdit{Add: []string{"x"}})
			return err
		}},
		{"create", true, func(f *fixture, story, _, _ Card) error {
			c, err := f.s.Create(f.ctx, owner, NewCard{ProjectID: proj, Kind: KindSubtask, ParentID: story.ID, Title: "new"})
			if err == nil && c.Confirmed() {
				return fmt.Errorf("the owner's card under a proposal is confirmed: %+v", c)
			}
			return err
		}},
		{"split", true, func(f *fixture, story, leaf, _ Card) error {
			if _, err := f.s.Split(f.ctx, owner, leaf.ID, []SplitChild{{Title: "a"}, {Title: "b"}}); err != nil {
				return err
			}
			for _, k := range f.children(story.ID) {
				if k.Confirmed() {
					return fmt.Errorf("a split part under a proposal is confirmed: %+v", k)
				}
			}
			return nil
		}},
		{"link", false, func(f *fixture, _, leaf, other Card) error {
			return f.s.Link(f.ctx, owner, other.ID, leaf.ID)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			epic := f.create(owner, "", KindEpic, "Epic")
			f.must(f.s.StartPlanning(f.ctx, owner, epic.ID, "planner"))
			planner := Agent("planner", "")
			story := f.create(planner, epic.ID, KindStory, "Suggested")
			leaf := f.create(planner, story.ID, KindSubtask, "Suggested leaf")
			other := f.create(planner, story.ID, KindSubtask, "Other leaf")
			f.clock.advance(ExpiryWindow / 2)
			f.must(tc.op(f, story, leaf, other))
			for _, c := range []Card{f.card(story.ID), f.card(leaf.ID)} {
				if c.Status == StatusCancelled {
					continue // the split card
				}
				if c.Confirmed() || c.PinnedSHA != "" {
					t.Fatalf("%s after %s = %+v, want it a proposal", c.Title, tc.name, c)
				}
			}
			if e := f.card(epic.ID); e.PinnedSHA != "head-1" {
				t.Fatalf("a confirmed ancestor was re-pinned: %+v", e)
			}
			// Past the first expiry, a re-armed proposal is still there.
			f.clock.advance(ExpiryWindow/2 + time.Hour)
			_, err := f.s.Sweep(f.ctx)
			f.must(err)
			if got := f.card(story.ID); (got.Status == StatusCancelled) == tc.rearm {
				t.Fatalf("story after %s and the first expiry = %s, re-armed %v", tc.name, got.Status, tc.rearm)
			}
		})
	}
}

// Search input is never passed to FTS5 as syntax, so hostile queries match
// or miss but never fail.
func TestSearchSurvivesHostileQueries(t *testing.T) {
	f := newFixture(t)
	f.tree()
	for _, q := range []string{"*", "-", "()", `"`, `""`, "a*", "NEAR(", "title:x", "^", "one OR", "\x00", "ünï", "!!!", "a\"b", "col:*"} {
		if _, err := f.s.List(f.ctx, proj, Filter{Query: q}); err != nil {
			t.Errorf("list %q: %v", q, err)
		}
		if _, err := f.s.Similar(f.ctx, proj, q, "", 5); err != nil {
			t.Errorf("similar %q: %v", q, err)
		}
	}
}
