package board

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// testRun is the run an approval in these tests authorizes.
var testRun = RunSettings{Provider: "copilot", Model: "gpt-6-luna", Mode: "safe", Parallel: 2}

// items lists the cards refs as an Approve dialog shows them now: each at
// its current revision.
func (f *fixture) items(refs ...string) []ApproveItem {
	f.t.Helper()
	out := make([]ApproveItem, len(refs))
	for i, ref := range refs {
		c := f.card(ref)
		out[i] = ApproveItem{ID: c.ID, Revision: c.Revision}
	}
	return out
}

// approve approves epic with the cards refs listed, as the owner at HEAD
// "head-1".
func (f *fixture) approve(epic string, refs ...string) Card {
	f.t.Helper()
	c, err := f.s.Approve(f.ctx, owner, epic, testRun, f.items(refs...), "")
	if err != nil {
		f.t.Fatalf("approve %s: %v", epic, err)
	}
	return c
}

// acceptCmd sets the Project default acceptance command.
func (f *fixture) acceptCmd(cmd string) {
	f.t.Helper()
	f.must(f.s.SetProjectAcceptCmd(f.ctx, owner, proj, cmd))
}

// agentPlan is a plan a Task proposed: epic › story one (a, b) and story
// two (c), all proposals.
type agentPlan struct {
	epic, one, two, a, b, c Card
}

func (f *fixture) agentPlan(a Actor) agentPlan {
	f.t.Helper()
	var p agentPlan
	p.epic = f.create(a, "", KindEpic, "Calculator")
	p.one = f.create(a, p.epic.ID, KindStory, "Parse")
	p.two = f.create(a, p.epic.ID, KindStory, "Eval")
	p.a = f.create(a, p.one.ID, KindSubtask, "Tokens")
	p.b = f.create(a, p.one.ID, KindSubtask, "Tree")
	p.c = f.create(a, p.two.ID, KindSubtask, "Walk")
	return p
}

func (p agentPlan) ids() []string {
	return []string{p.epic.ID, p.one.ID, p.two.ID, p.a.ID, p.b.ID, p.c.ID}
}

// Approve confirms the listed proposals, nested ones included, pins them to
// the owner's HEAD and records the run on the epic. A proposal added later
// stays one until an approval lists it, and an approval that does not list
// it is stale.
func TestApproveConfirmsListedSubtree(t *testing.T) {
	f := newFixture(t)
	f.acceptCmd("go test ./...")
	planner := Agent("planner", "")
	p := f.agentPlan(planner)
	got, err := f.s.Approve(f.ctx, Owner("head-2"), p.epic.ID, testRun, f.items(p.ids()...), " main ")
	f.must(err)
	if got.ID != p.epic.ID || got.Run == nil || got.Run.RunSettings != testRun || !got.Run.ApprovedAt.Equal(f.clock.Now()) || got.Paused != "" {
		t.Fatalf("approved epic = %+v, run %+v", got, got.Run)
	}
	for _, id := range p.ids() {
		if c := f.card(id); !c.Confirmed() || c.PinnedSHA != "head-2" {
			t.Fatalf("%s after the approval = %+v", c.Title, c)
		}
	}
	// A run shows on the epic only.
	if f.card(p.one.ID).Run != nil {
		t.Fatal("a story carries the run")
	}

	later := f.create(planner, p.one.ID, KindSubtask, "Errors")
	if later.Confirmed() {
		t.Fatal("a card added after the approval is confirmed")
	}
	again := RunSettings{Provider: "copilot", Model: "ollama/deepseek-v4.1-flash", Effort: "high", ContextSize: "default", Mode: "yolo", Parallel: 4}
	_, err = f.s.Approve(f.ctx, owner, p.epic.ID, again, f.items(p.ids()...), "")
	wantRefusal(t, err, CodeStale, later.ref())
	if c := f.card(later.ID); c.Confirmed() || f.card(p.epic.ID).Run.RunSettings != testRun {
		t.Fatalf("a stale approval wrote: %+v, run %+v", c, f.card(p.epic.ID).Run)
	}
	got, err = f.s.Approve(f.ctx, owner, p.epic.ID, again, f.items(append(p.ids(), later.ID)...), "next")
	f.must(err)
	if got.Run.RunSettings != again {
		t.Fatalf("approving again kept the run %+v", got.Run)
	}
	// The first approval set the branch the run follows, in the same write;
	// approving again keeps it.
	if ps, err := f.s.ProjectSettings(f.ctx, proj); err != nil || ps.BaseRef != "main" {
		t.Fatalf("base branch = %q, %v; want main", ps.BaseRef, err)
	}
	if c := f.card(later.ID); !c.Confirmed() {
		t.Fatalf("a listed proposal = %+v", c)
	}

	// A card that is not under the epic is the caller's mistake, named in
	// the message.
	other := f.create(owner, "", KindEpic, "Other")
	_, err = f.s.Approve(f.ctx, owner, p.epic.ID, testRun, f.items(append(p.ids(), later.ID, other.ID)...), "")
	wantRefusal(t, err, CodeInvalid)
	if !strings.Contains(err.Error(), other.ref()) {
		t.Fatalf("refusal = %v, want it to name %s", err, other.ref())
	}
	_, err = f.s.Approve(f.ctx, owner, p.one.ID, testRun, f.items(p.one.ID), "")
	wantCode(t, err, CodeInvalid)
	_, err = f.s.Approve(f.ctx, owner, p.epic.ID, testRun, f.items(append(p.ids(), later.ID)...), "a\nb")
	wantCode(t, err, CodeInvalid)
	_, err = f.s.Approve(f.ctx, planner, p.epic.ID, testRun, f.items(p.ids()...), "")
	wantCode(t, err, CodeForbidden)
}

func TestApproveRefuses(t *testing.T) {
	f := newFixture(t)
	planner := Agent("planner", "")
	epic := f.create(owner, "", KindEpic, "Epic")
	story := f.create(owner, epic.ID, KindStory, "Story")
	one := f.create(owner, story.ID, KindSubtask, "One")
	refused := func(settings RunSettings, items []ApproveItem, code Code, refs ...string) {
		t.Helper()
		before := f.revision()
		_, err := f.s.Approve(f.ctx, owner, epic.ID, settings, items, "")
		if refs == nil {
			wantCode(t, err, code)
		} else {
			wantRefusal(t, err, code, refs...)
		}
		if after := f.revision(); after != before {
			t.Fatalf("a refused approval moved the revision from %d to %d", before, after)
		}
	}

	// Every subtask that may run needs an acceptance command.
	refused(testRun, f.items(epic.ID, story.ID, one.ID), CodeInvalid, one.ref())
	f.acceptCmd("go test ./...")

	empty := f.create(owner, epic.ID, KindStory, "Empty")
	refused(testRun, f.items(epic.ID, story.ID, one.ID, empty.ID), CodeInvalid, empty.ref())
	_, err := f.s.SetStatus(f.ctx, owner, empty.ID, StatusCancelled, "not needed", false)
	f.must(err)

	// The dialog shows every live card under the epic: one it did not
	// list, at any depth and the epic included, is a change it did not
	// show, so the approval is stale.
	f.must(f.s.StartPlanning(f.ctx, owner, epic.ID, "planner"))
	proposed := f.create(planner, epic.ID, KindStory, "Proposed")
	idea := f.create(planner, proposed.ID, KindSubtask, "Idea")
	refused(testRun, f.items(epic.ID, story.ID, one.ID, proposed.ID), CodeStale, idea.ref())
	refused(testRun, f.items(story.ID, one.ID, proposed.ID, idea.ID), CodeStale, epic.ref())
	_, err = f.s.Dismiss(f.ctx, owner, proposed.ID)
	f.must(err)

	for _, bad := range []RunSettings{
		{Provider: "copilot", Mode: "safe", Parallel: 2},
		{Provider: "copilot", Model: " ", Mode: "safe", Parallel: 2},
		{Provider: "copilot", Model: "m", Mode: "auto", Parallel: 2},
		{Provider: "copilot", Model: "m", Mode: "safe", Parallel: 0},
		{Provider: "copilot", Model: "m", Mode: "safe", Parallel: 5},
		{Model: "m", Mode: "safe", Parallel: 2},
	} {
		refused(bad, f.items(epic.ID, story.ID, one.ID), CodeInvalid)
	}

	// A card changed after the dialog showed it.
	items := f.items(epic.ID, story.ID, one.ID)
	_, err = f.s.Edit(f.ctx, owner, one.ID, Patch{Title: ptr("One, renamed")})
	f.must(err)
	refused(testRun, items, CodeStale, epic.ref(), story.ref(), one.ref())

	// Manual and lane work never mix: a held subtask is finished or released first.
	f.launch(one.ID, "worker")
	refused(testRun, f.items(epic.ID, story.ID, one.ID), CodeInvalid, one.ref())
	_, err = f.s.ReleaseHold(f.ctx, owner, one.ID, ReleaseOwner, "")
	f.must(err)

	_, err = f.s.SetStatus(f.ctx, owner, one.ID, StatusDone, "shipped", false)
	f.must(err)
	wantStatus(t, f.card(epic.ID), StatusDone)
	refused(testRun, f.items(epic.ID, story.ID, one.ID), CodeInvalid)

	cancelled := f.create(owner, "", KindEpic, "Cancelled")
	f.create(owner, f.create(owner, cancelled.ID, KindStory, "S").ID, KindSubtask, "T")
	_, err = f.s.SetStatus(f.ctx, owner, cancelled.ID, StatusCancelled, "dropped", false)
	f.must(err)
	_, err = f.s.Approve(f.ctx, owner, cancelled.ID, testRun, f.items(cancelled.ID), "")
	wantCode(t, err, CodeInvalid)
}

// A story whose confirmed subtasks were all cancelled shows as cancelled,
// so the Approve dialog leaves it and its proposals out; when proposals
// under it are live, the refusal names the story and says why it is not
// shown. Cancelling the story lets the approval through.
func TestApproveNamesAHiddenCancelledStory(t *testing.T) {
	f := newFixture(t)
	f.acceptCmd("go test ./...")
	planner := Agent("planner", "")
	p := f.agentPlan(planner)
	f.approve(p.epic.ID, p.ids()...)
	f.create(planner, p.two.ID, KindSubtask, "Fold")
	_, err := f.s.SetStatus(f.ctx, owner, p.c.ID, StatusCancelled, "not needed", false)
	f.must(err)
	wantStatus(t, f.card(p.two.ID), StatusCancelled)

	_, err = f.s.Approve(f.ctx, owner, p.epic.ID, testRun, f.items(f.shownUnder(p.epic.ID)...), "")
	wantRefusal(t, err, CodeInvalid, p.two.ref())
	if want := fmt.Sprintf("%[1]s shows as cancelled, so the Approve dialog leaves it out, but proposals under it are live and would never run: cancel %[1]s, then approve", p.two.ref()); err.Error() != want {
		t.Fatalf("refusal = %q, want %q", err, want)
	}
	_, err = f.s.SetStatus(f.ctx, owner, p.two.ID, StatusCancelled, "dropped", false)
	f.must(err)
	f.must(func() error {
		_, err := f.s.Approve(f.ctx, owner, p.epic.ID, testRun, f.items(f.shownUnder(p.epic.ID)...), "")
		return err
	}())
}

// The pause is the owner's flag, on a card under an epic or on an approved
// epic, and holds on a card that has started.
func TestPausedIsOwnerOnly(t *testing.T) {
	f := newFixture(t)
	f.acceptCmd("go test ./...")
	epic, story, one, two := f.tree()
	_, err := f.s.Edit(f.ctx, owner, epic.ID, Patch{Paused: ptr(true)})
	wantCode(t, err, CodeInvalid) // not approved yet, and approving clears it
	_, err = f.s.SetStatus(f.ctx, owner, one.ID, StatusDone, "shipped", false)
	f.must(err)
	f.approve(epic.ID, epic.ID, story.ID, one.ID, two.ID)

	f.must(f.s.StartPlanning(f.ctx, owner, epic.ID, "planner"))
	_, err = f.s.Edit(f.ctx, Agent("planner", ""), two.ID, Patch{Paused: ptr(true)})
	wantCode(t, err, CodeForbidden)

	for _, c := range []Card{one, story, epic} {
		res, err := f.s.Edit(f.ctx, owner, c.ID, Patch{Paused: ptr(true)})
		f.must(err)
		if res.Card.Paused != PausedOwner || res.Request != nil {
			t.Fatalf("paused %s = %+v", c.Title, res)
		}
	}
	wantStatus(t, f.card(one.ID), StatusDone)
	f.raw(`UPDATE cards SET paused = 'uam' WHERE id = ?`, two.ID)
	if got := f.card(two.ID).Paused; got != PausedUAM {
		t.Fatalf("paused by uam = %q", got)
	}
	res, err := f.s.Edit(f.ctx, owner, two.ID, Patch{Paused: ptr(false)})
	f.must(err)
	if res.Card.Paused != "" {
		t.Fatalf("resumed = %+v", res.Card)
	}
	// Approving again clears only the epic's own pause.
	f.approve(epic.ID, epic.ID, story.ID, one.ID, two.ID)
	if f.card(epic.ID).Paused != "" || f.card(story.ID).Paused != PausedOwner {
		t.Fatalf("after approving again: epic %q, story %q", f.card(epic.ID).Paused, f.card(story.ID).Paused)
	}
	// Moved under another epic, approved or not, a card and everything under
	// it keep their pauses; moved to the root, outside any epic, where
	// nothing could clear them, they drop them.
	plain := f.create(owner, "", KindEpic, "Plain")
	moving := f.create(owner, epic.ID, KindStory, "Moving")
	inner := f.create(owner, moving.ID, KindSubtask, "Inner")
	for _, id := range []string{moving.ID, inner.ID} {
		_, err := f.s.Edit(f.ctx, owner, id, Patch{Paused: ptr(true)})
		f.must(err)
	}
	res, err = f.s.Edit(f.ctx, owner, moving.ID, Patch{ParentID: ptr(plain.ID)})
	f.must(err)
	if res.Card.Paused != PausedOwner || f.card(inner.ID).Paused != PausedOwner {
		t.Fatalf("moved to another epic: story %q, subtask %q", res.Card.Paused, f.card(inner.ID).Paused)
	}
	res, err = f.s.Edit(f.ctx, owner, moving.ID, Patch{ParentID: ptr("")})
	f.must(err)
	if res.Card.Paused != "" || f.card(inner.ID).Paused != "" {
		t.Fatalf("moved to the root: story %q, subtask %q", res.Card.Paused, f.card(inner.ID).Paused)
	}
}

// readyUnder lists the titles of the subtasks RunFacts finds ready under
// the approved epic id, in outline order.
func (f *fixture) readyUnder(id string) []string {
	f.t.Helper()
	facts, err := f.s.RunFacts(f.ctx)
	f.must(err)
	var out []string
	for _, e := range facts.Epics {
		if e.Epic.ID == id {
			for _, c := range e.Ready {
				out = append(out, c.Title)
			}
		}
	}
	return out
}

// The owner may pause a story or subtask before approving its epic (ADR
// 0006 §4.6): nothing runs before the approval, and the pause holds through
// it, so the paused subtask is never ready while its siblings are, until
// Resume. A card at the root, outside any epic, is refused, and so is an
// epic not approved yet, whose own pause approving clears. It stays the
// owner's flag.
func TestPauseBeforeApproval(t *testing.T) {
	f := newFixture(t)
	f.acceptCmd("go test ./...")
	planner := Agent("planner", "")
	p := f.agentPlan(planner)
	_, err := f.s.Edit(f.ctx, planner, p.c.ID, Patch{Paused: ptr(true)})
	wantCode(t, err, CodeForbidden)
	for _, c := range []Card{f.create(owner, "", KindSubtask, "Loose"), p.epic} {
		_, err := f.s.Edit(f.ctx, owner, c.ID, Patch{Paused: ptr(true)})
		wantCode(t, err, CodeInvalid)
	}
	res, err := f.s.Edit(f.ctx, owner, p.b.ID, Patch{Paused: ptr(true)})
	f.must(err)
	if res.Card.Paused != PausedOwner || res.Request != nil {
		t.Fatalf("paused before approval = %+v", res)
	}

	f.approve(p.epic.ID, p.ids()...)
	if got := f.card(p.b.ID).Paused; got != PausedOwner {
		t.Fatalf("approving cleared the subtask's pause: %q", got)
	}
	if got := f.readyUnder(p.epic.ID); !slices.Equal(got, []string{"Tokens", "Walk"}) {
		t.Fatalf("ready with Tree paused = %v", got)
	}
	_, err = f.s.Edit(f.ctx, owner, p.b.ID, Patch{Paused: ptr(false)})
	f.must(err)
	if got := f.readyUnder(p.epic.ID); !slices.Equal(got, []string{"Tokens", "Tree", "Walk"}) {
		t.Fatalf("ready after Resume = %v", got)
	}
}

// Approving keeps every card's own pause, set before the approval or since,
// by the owner or by uam; only the epic's own pause is cleared, as
// approving an epic means run it.
func TestApproveKeepsCardPauses(t *testing.T) {
	f := newFixture(t)
	f.acceptCmd("go test ./...")
	epic, story, one, two := f.tree()
	for _, c := range []Card{story, one} {
		_, err := f.s.Edit(f.ctx, owner, c.ID, Patch{Paused: ptr(true)})
		f.must(err)
	}
	f.approve(epic.ID, epic.ID, story.ID, one.ID, two.ID)
	_, err := f.s.Edit(f.ctx, owner, epic.ID, Patch{Paused: ptr(true)})
	f.must(err)
	f.raw(`UPDATE cards SET paused = 'uam' WHERE id = ?`, two.ID)
	f.approve(epic.ID, epic.ID, story.ID, one.ID, two.ID)
	for _, tc := range []struct {
		card Card
		want string
	}{{epic, ""}, {story, PausedOwner}, {one, PausedOwner}, {two, PausedUAM}} {
		if got := f.card(tc.card.ID).Paused; got != tc.want {
			t.Errorf("%s paused %q after approving again, want %q", tc.card.Title, got, tc.want)
		}
	}
}

// Under an approved epic nothing starts by hand and nothing is confirmed
// card by card: uam runs it.
func TestManualStartsRefusedUnderApprovedEpic(t *testing.T) {
	f := newFixture(t)
	f.acceptCmd("go test ./...")
	planner := Agent("planner", "")
	p := f.agentPlan(planner)
	f.approve(p.epic.ID, p.ids()...)
	later := f.create(planner, p.one.ID, KindSubtask, "Errors")
	base := Baseline{Head: "h"}
	before := f.revision()
	for name, err := range map[string]error{
		"launch": func() error { _, err := f.s.Launch(f.ctx, owner, p.a.ID, "t1", base, false); return err }(),
		"launch with confirm": func() error {
			_, err := f.s.Launch(f.ctx, owner, later.ID, "t2", base, true)
			return err
		}(),
		"do whole story":    func() error { _, err := f.s.Launch(f.ctx, owner, p.one.ID, "t3", base, false); return err }(),
		"do the whole epic": func() error { _, err := f.s.Launch(f.ctx, owner, p.epic.ID, "t4", base, true); return err }(),
		"check launch":      f.s.CheckLaunch(f.ctx, owner, p.a.ID, false),
		"attach":            func() error { _, err := f.s.Attach(f.ctx, owner, p.a.ID, "t5", "", base, false); return err }(),
		"attach to a story": func() error { _, err := f.s.Attach(f.ctx, owner, p.one.ID, "t6", "New", base, true); return err }(),
		"claim":             func() error { _, err := f.s.Claim(f.ctx, planner, p.a.ID, base); return err }(),
		"confirm":           func() error { _, err := f.s.Confirm(f.ctx, owner, later.ID); return err }(),
		"confirm the epic":  func() error { _, err := f.s.Confirm(f.ctx, owner, p.epic.ID); return err }(),
	} {
		if CodeOf(err) != CodeRunOwned || !strings.Contains(err.Error(), p.epic.ref()) {
			t.Errorf("%s = %v, want run_owned naming %s", name, err, p.epic.ref())
		}
	}
	if after := f.revision(); after != before {
		t.Fatalf("refused starts moved the revision from %d to %d", before, after)
	}
	// Outside approved epics nothing changes.
	other := f.create(owner, "", KindEpic, "Other")
	leaf := f.create(owner, other.ID, KindSubtask, "Leaf")
	f.launch(leaf.ID, "worker")
}

// An agent's move that changes a card's epic, while either epic is
// approved, goes to the owner as a change request: into one, out of one,
// and from the root. A move inside an approved epic applies.
func TestAgentMoveAcrossApprovedEpicIsAChangeRequest(t *testing.T) {
	f := newFixture(t)
	f.acceptCmd("go test ./...")
	planner := Agent("planner", "")
	p := f.agentPlan(planner)
	extra := f.create(planner, p.one.ID, KindSubtask, "Spare")
	f.approve(p.epic.ID, append(p.ids(), extra.ID)...)
	free := f.create(planner, "", KindEpic, "Free")
	freeStory := f.create(planner, free.ID, KindStory, "Free story")
	loose := f.create(planner, freeStory.ID, KindSubtask, "Loose")
	f.create(planner, freeStory.ID, KindSubtask, "Stays")
	other := f.create(planner, "", KindEpic, "Other")
	otherStory := f.create(planner, other.ID, KindStory, "Other story")
	root := f.create(owner, "", KindStory, "Root story")
	rootLeaf := f.create(owner, root.ID, KindSubtask, "Root leaf")
	f.create(owner, root.ID, KindSubtask, "Root stays")
	// The Task plans under the root story too, and keeps the cards it created.
	f.must(f.s.StartPlanning(f.ctx, owner, root.ID, "planner"))

	for name, move := range map[string]struct{ card, to Card }{
		"into":          {loose, p.two},
		"out of":        {extra, freeStory},
		"from the root": {rootLeaf, p.two},
	} {
		res, err := f.s.Edit(f.ctx, planner, move.card.ID, Patch{ParentID: &move.to.ID})
		f.must(err)
		if res.Request == nil || res.Request.Kind != RequestChange || res.Card.ParentID != move.card.ParentID {
			t.Fatalf("%s: %+v", name, res)
		}
	}
	// Inside the approved epic the move applies.
	res, err := f.s.Edit(f.ctx, planner, extra.ID, Patch{ParentID: &p.two.ID})
	f.must(err)
	if res.Request != nil || res.Card.ParentID != p.two.ID {
		t.Fatalf("a move inside the epic = %+v", res)
	}
	// Between epics nobody approved, too.
	if res, err := f.s.Edit(f.ctx, planner, loose.ID, Patch{ParentID: &otherStory.ID}); err != nil || res.Request != nil {
		t.Fatalf("a move between unapproved epics = %+v, %v", res, err)
	}
}

// Restore under an approved epic brings the cards back as proposals with a
// fresh expiry, to approve again; a card with confirmed work under it stays
// confirmed, and the epic itself comes back confirmed.
func TestRestoreUnderApprovedEpicMakesProposals(t *testing.T) {
	f := newFixture(t)
	f.acceptCmd("go test ./...")
	epic, story, one, two := f.tree()
	other := f.create(owner, epic.ID, KindStory, "Other")
	three := f.create(owner, other.ID, KindSubtask, "Three")
	_, err := f.s.SetStatus(f.ctx, owner, one.ID, StatusDone, "shipped", false)
	f.must(err)
	f.approve(epic.ID, epic.ID, story.ID, one.ID, two.ID, other.ID, three.ID)

	_, err = f.s.SetStatus(f.ctx, owner, other.ID, StatusCancelled, "not now", false)
	f.must(err)
	f.clock.advance(time.Hour)
	_, err = f.s.Restore(f.ctx, owner, other.ID, "after all")
	f.must(err)
	expires := f.clock.Now().Add(ExpiryWindow)
	for _, c := range []Card{other, three} {
		got := f.card(c.ID)
		if got.Confirmed() || !got.ExpiresAt.Equal(expires) || got.Status != StatusPlanned {
			t.Fatalf("%s after the restore = %+v", c.Title, got)
		}
	}

	// The story keeps its done subtask confirmed, so it stays confirmed.
	_, err = f.s.SetStatus(f.ctx, owner, epic.ID, StatusCancelled, "dropped", false)
	f.must(err)
	_, err = f.s.Restore(f.ctx, owner, epic.ID, "back")
	f.must(err)
	if got := f.card(epic.ID); !got.Confirmed() || got.Run == nil {
		t.Fatalf("restored epic = %+v", got)
	}
	if got := f.card(story.ID); !got.Confirmed() {
		t.Fatalf("a story with done work = %+v", got)
	}
	if got := f.card(two.ID); got.Confirmed() || got.Status != StatusPlanned {
		t.Fatalf("restored subtask = %+v", got)
	}
	if got := f.card(one.ID); !got.Confirmed() || got.Status != StatusDone {
		t.Fatalf("done subtask = %+v", got)
	}

	// Outside approved epics Restore confirms, as before.
	plain, plainStory, plainOne, _ := f.tree2()
	_, err = f.s.SetStatus(f.ctx, owner, plainStory.ID, StatusCancelled, "no", false)
	f.must(err)
	_, err = f.s.Restore(f.ctx, owner, plainStory.ID, "yes")
	f.must(err)
	if !f.card(plainOne.ID).Confirmed() || !f.card(plain.ID).Confirmed() {
		t.Fatal("a restore outside approved epics left a proposal")
	}
}

// tree2 is a second owner tree, beside tree's.
func (f *fixture) tree2() (epic, story, one, two Card) {
	epic = f.create(owner, "", KindEpic, "Epic 2")
	story = f.create(owner, epic.ID, KindStory, "Story 2")
	one = f.create(owner, story.ID, KindSubtask, "One 2")
	two = f.create(owner, story.ID, KindSubtask, "Two 2")
	return
}

// An agent never leaves an approved container without confirmed work, and
// never deletes the approved epic itself.
func TestAgentMayNotEmptyAnApprovedContainer(t *testing.T) {
	f := newFixture(t)
	f.acceptCmd("go test ./...")
	planner := Agent("planner", "")
	p := f.agentPlan(planner)
	f.approve(p.epic.ID, p.ids()...)

	_, err := f.s.Delete(f.ctx, planner, p.c.ID)
	wantRefusal(t, err, CodeInvalid, p.two.ref())
	_, err = f.s.Edit(f.ctx, planner, p.c.ID, Patch{ParentID: &p.one.ID})
	wantRefusal(t, err, CodeInvalid, p.two.ref())
	if !strings.Contains(err.Error(), "no confirmed subtask") {
		t.Fatalf("refusal = %v", err)
	}
	_, err = f.s.Edit(f.ctx, planner, p.c.ID, Patch{ParentID: &p.epic.ID})
	wantRefusal(t, err, CodeInvalid, p.two.ref())
	_, err = f.s.Delete(f.ctx, planner, p.epic.ID)
	wantCode(t, err, CodeForbidden)

	// With another confirmed subtask beside it, it moves and goes.
	if _, err := f.s.Edit(f.ctx, planner, p.b.ID, Patch{ParentID: &p.two.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Delete(f.ctx, planner, p.c.ID); err != nil {
		t.Fatal(err)
	}
	// The owner may still empty a story.
	if _, err := f.s.Edit(f.ctx, owner, p.b.ID, Patch{ParentID: &p.one.ID}); err != nil {
		t.Fatal(err)
	}
}

// Migration v5 adds the pause and the runs table, and it is one-way: the
// older binary refuses the newer database.
func TestMigrateV4ToV5(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	all := migrations
	t.Cleanup(func() { migrations = all })
	migrations = all[:4]
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// The store's queries read v5's columns, so the v4 board is written by hand.
	now := stamp(time.Now())
	if _, err := s.db.Exec(`INSERT INTO cards (id, seq, project_id, kind, parent_id, title, status, created_by, created_at, updated_at, moved_at)
		VALUES ('e1', 1, ?1, 'epic', '', 'Before', 'planned', 'owner', ?2, ?2, ?2),
		       ('s1', 2, ?1, 'subtask', 'e1', 'Leaf', 'planned', 'owner', ?2, ?2, ?2)`, proj, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO sequences (name, next) VALUES ('card', 3)`); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	epic := Card{ID: "e1"}

	migrations = all
	s, err = Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var version string
	if err := s.db.QueryRow(`SELECT v FROM meta WHERE k = 'schema_version'`).Scan(&version); err != nil || version != strconv.Itoa(len(migrations)) {
		t.Fatalf("schema_version = %q, %v", version, err)
	}
	got, err := s.Card(ctx, epic.ID)
	if err != nil || got.Paused != "" || got.Run != nil {
		t.Fatalf("an epic from v4 = %+v, %v", got, err)
	}
	for _, q := range []string{
		`UPDATE cards SET paused = 'later' WHERE id = '` + epic.ID + `'`,
		`INSERT INTO runs (epic_id, provider, model, mode, parallel, approved_at) VALUES ('e', 'copilot', '', 'safe', 2, 'now')`,
		`INSERT INTO runs (epic_id, provider, model, mode, parallel, approved_at) VALUES ('e', 'copilot', 'm', 'auto', 2, 'now')`,
		`INSERT INTO runs (epic_id, provider, model, mode, parallel, approved_at) VALUES ('e', 'copilot', 'm', 'safe', 5, 'now')`,
	} {
		if _, err := s.db.Exec(q); err == nil {
			t.Fatalf("v5 took %s", q)
		}
	}
	if err := s.SetProjectAcceptCmd(ctx, owner, proj, "make test"); err != nil {
		t.Fatal(err)
	}
	leaf, err := s.Card(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, owner, epic.ID, testRun, []ApproveItem{{ID: epic.ID, Revision: got.Revision}, {ID: leaf.ID, Revision: leaf.Revision}}, ""); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	migrations = all[:4]
	if s, err := Open(path, Options{}); err == nil {
		_ = s.Close()
		t.Fatal("a v4 binary opened a v5 board")
	}
}

func TestPurgeDropsRun(t *testing.T) {
	f := newFixture(t)
	f.acceptCmd("go test ./...")
	epic, story, one, two := f.tree()
	f.approve(epic.ID, epic.ID, story.ID, one.ID, two.ID)
	_, err := f.s.SetStatus(f.ctx, owner, epic.ID, StatusCancelled, "dropped", false)
	f.must(err)
	if n, err := f.s.Purge(f.ctx, owner, proj); err != nil || n != 4 {
		t.Fatalf("purge = %d, %v", n, err)
	}
	var runs int
	f.must(f.s.db.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&runs))
	if runs != 0 {
		t.Fatalf("%d runs left after the purge", runs)
	}
}

// An agent's move never takes a card out from under a pause: leaving a
// place with a paused card at or above it for one with none goes to the
// owner as a change request. A move under a pause applies, and so does one
// that keeps the card's own pause.
func TestAgentMoveOutOfPausedIsAChangeRequest(t *testing.T) {
	f := newFixture(t)
	f.acceptCmd("go test ./...")
	planner := Agent("planner", "")
	p := f.agentPlan(planner)
	d := f.create(planner, p.two.ID, KindSubtask, "Fold")
	f.approve(p.epic.ID, append(p.ids(), d.ID)...)
	_, err := f.s.Edit(f.ctx, owner, p.one.ID, Patch{Paused: ptr(true)})
	f.must(err)

	for name, to := range map[string]Card{"to a story not paused": p.two, "to the epic": p.epic} {
		res, err := f.s.Edit(f.ctx, planner, p.a.ID, Patch{ParentID: &to.ID})
		f.must(err)
		if res.Request == nil || res.Request.Kind != RequestChange || !res.OutOfPause || res.AcrossRun || res.Card.ParentID != p.one.ID {
			t.Fatalf("%s: %+v", name, res)
		}
	}
	if res, err := f.s.Edit(f.ctx, planner, d.ID, Patch{ParentID: &p.one.ID}); err != nil || res.Request != nil || res.Card.ParentID != p.one.ID {
		t.Fatalf("a move under the pause = %+v, %v", res, err)
	}
	_, err = f.s.Edit(f.ctx, owner, p.b.ID, Patch{Paused: ptr(true)})
	f.must(err)
	res, err := f.s.Edit(f.ctx, planner, p.b.ID, Patch{ParentID: &p.two.ID})
	if err != nil || res.Request != nil || res.Card.ParentID != p.two.ID || res.Card.Paused != PausedOwner {
		t.Fatalf("a move keeping its own pause = %+v, %v", res, err)
	}
}

// A pause set before approval survives an agent's moves: the only moves
// that drop pauses, out of every epic, are beyond an agent (the root is
// the owner's, and a story outside any epic is outside its scope).
func TestAgentMoveOutOfEpicKeepsPauses(t *testing.T) {
	f := newFixture(t)
	planner := Agent("planner", "")
	p := f.agentPlan(planner)
	loose := f.create(owner, "", KindStory, "Loose")
	_, err := f.s.Edit(f.ctx, owner, p.b.ID, Patch{Paused: ptr(true)})
	f.must(err)

	root := ""
	for _, to := range []string{root, loose.ID} {
		res, err := f.s.Edit(f.ctx, planner, p.b.ID, Patch{ParentID: &to})
		if err == nil && res.Request == nil {
			t.Fatalf("an agent moved #%s out of every epic: %+v", p.b.ID, res)
		}
	}
	if c := f.card(p.b.ID); c.ParentID != p.one.ID || c.Paused != PausedOwner {
		t.Fatalf("after the agent's moves: parent %s, paused %q", c.ParentID, c.Paused)
	}
}

// Accepting a blocked request flags a proposal under an approved epic but
// leaves it a proposal: only approving the epic confirms it.
func TestAcceptBlockedKeepsAProposalUnderApprovedEpic(t *testing.T) {
	f := newFixture(t)
	f.acceptCmd("go test ./...")
	planner := Agent("planner", "")
	p := f.agentPlan(planner)
	f.approve(p.epic.ID, p.ids()...)
	later := f.create(planner, p.one.ID, KindSubtask, "Errors")
	req, err := f.s.FileRequest(f.ctx, planner, later.ID, RequestInput{Kind: RequestBlocked, Comment: "waits on the lexer"})
	f.must(err)
	_, err = f.s.Accept(f.ctx, owner, req.ID, "")
	f.must(err)
	if c := f.card(later.ID); c.Confirmed() || !c.Blocked {
		t.Fatalf("after the accepted blocked request = %+v", c)
	}
}

// Under an approved epic only its approval confirms a proposal. Accepting
// a change request that moves a confirmed subtask under a proposal story
// leaves the story a proposal, and the subtask keeps its confirmation but
// waits for the next approval to run; accepting the done request of a
// ticked part split off a proposal confirms neither the part nor the story
// the proposal became.
func TestAcceptUnderApprovedEpicConfirmsNoProposal(t *testing.T) {
	f := newFixture(t)
	f.acceptCmd("go test ./...")
	planner := Agent("planner", "")
	p := f.agentPlan(planner)
	f.approve(p.epic.ID, p.ids()...)

	later := f.create(planner, p.epic.ID, KindStory, "Later")
	res, err := f.s.Edit(f.ctx, planner, p.a.ID, Patch{ParentID: &later.ID})
	f.must(err)
	if res.Request == nil {
		t.Fatalf("moving a confirmed subtask under a proposal = %+v", res)
	}
	_, err = f.s.Accept(f.ctx, Owner("head-3"), res.Request.ID, "")
	f.must(err)
	if c := f.card(later.ID); c.Confirmed() {
		t.Fatalf("the accepted move confirmed the story: %+v", c)
	}
	if c := f.card(p.a.ID); !c.Confirmed() || c.ParentID != later.ID {
		t.Fatalf("the moved subtask = %+v", c)
	}
	wantRefusal(t, f.s.CanStart(f.ctx, p.a.ID), CodeNotReady, later.ref())

	docs := f.create(planner, p.epic.ID, KindSubtask, "Docs")
	_, err = f.s.Checklist(f.ctx, owner, docs.ID, ChecklistEdit{Add: []string{"Guide", "Reference"}})
	f.must(err)
	_, err = f.s.Checklist(f.ctx, owner, docs.ID, ChecklistEdit{Tick: []int{0}})
	f.must(err)
	_, err = f.s.Split(f.ctx, planner, docs.ID, nil)
	f.must(err)
	var guide Card
	for _, c := range f.children(docs.ID) {
		if c.Title == "Guide" {
			guide = c
		}
	}
	reqs := f.detail(guide.ID).Requests
	if len(reqs) != 1 || reqs[0].Kind != RequestDone || reqs[0].Status != RequestPending {
		t.Fatalf("the ticked part's requests = %+v", reqs)
	}
	_, err = f.s.Accept(f.ctx, owner, reqs[0].ID, "")
	f.must(err)
	if c := f.card(guide.ID); c.Status != StatusDone || c.Confirmed() {
		t.Fatalf("the accepted part = %+v", c)
	}
	if c := f.card(docs.ID); c.Kind != KindStory || c.Confirmed() {
		t.Fatalf("the story the proposal became = %+v", c)
	}
}

// An agent's split never turns a confirmed subtask under an approved epic
// into a story of proposals, which would never finish.
func TestAgentSplitUnderApprovedEpicKeepsItFinishable(t *testing.T) {
	f := newFixture(t)
	f.acceptCmd("go test ./...")
	planner := Agent("planner", "")
	p := f.agentPlan(planner)
	direct := f.create(planner, p.epic.ID, KindSubtask, "Docs")
	f.approve(p.epic.ID, append(p.ids(), direct.ID)...)
	before := f.revision()
	_, err := f.s.Split(f.ctx, planner, direct.ID, []SplitChild{{Title: "Guide"}, {Title: "Reference"}})
	wantRefusal(t, err, CodeInvalid, direct.ref())
	if c := f.card(direct.ID); c.Kind != KindSubtask || f.revision() != before {
		t.Fatalf("the refused split wrote: %+v", c)
	}
	// A proposal there splits, and so does a confirmed subtask under a
	// story, whose parts join the story beside its other confirmed work.
	later := f.create(planner, p.epic.ID, KindSubtask, "Later")
	if _, err := f.s.Split(f.ctx, planner, later.ID, []SplitChild{{Title: "Half"}, {Title: "Other half"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Split(f.ctx, planner, p.a.ID, []SplitChild{{Title: "Lex"}, {Title: "Scan"}}); err != nil {
		t.Fatal(err)
	}
}

// Restore under an approved epic leaves a card a proposal when the only
// confirmed work under it was cancelled before, and that work becomes a
// proposal with it: the card plans again, and approving the epic again
// confirms what the dialog shows.
func TestRestoreUnderApprovedEpicSkipsCancelledWork(t *testing.T) {
	f := newFixture(t)
	f.acceptCmd("go test ./...")
	epic, story, one, two := f.tree()
	f.approve(epic.ID, epic.ID, story.ID, one.ID, two.ID)
	for _, c := range []Card{one, story} {
		_, err := f.s.SetStatus(f.ctx, owner, c.ID, StatusCancelled, "not now", false)
		f.must(err)
	}
	_, err := f.s.Restore(f.ctx, owner, story.ID, "after all")
	f.must(err)
	if got := f.card(story.ID); got.Confirmed() || got.Status != StatusPlanned {
		t.Fatalf("a story with only proposals live under it = %+v", got)
	}
	if got := f.card(two.ID); got.Confirmed() {
		t.Fatalf("restored subtask = %+v", got)
	}
	if got := f.card(one.ID); got.Confirmed() || got.Status != StatusCancelled {
		t.Fatalf("cancelled subtask under the restored story = %+v", got)
	}
	f.approve(epic.ID, epic.ID, story.ID, two.ID)
	if !f.card(story.ID).Confirmed() || !f.card(two.ID).Confirmed() {
		t.Fatal("approving again left the restored cards proposals")
	}
}

// The owner's done or To do on a proposal under an approved epic would
// confirm it card by card: it is refused, and a confirmed subtask there
// still takes it.
func TestSetStatusRefusesAProposalUnderApprovedEpic(t *testing.T) {
	f := newFixture(t)
	f.acceptCmd("go test ./...")
	planner := Agent("planner", "")
	p := f.agentPlan(planner)
	f.approve(p.epic.ID, p.ids()...)
	later := f.create(planner, p.one.ID, KindSubtask, "Errors")
	before := f.revision()
	for _, to := range []Status{StatusDone, StatusTodo} {
		_, err := f.s.SetStatus(f.ctx, owner, later.ID, to, "by hand", to == StatusDone)
		if CodeOf(err) != CodeRunOwned || !strings.Contains(err.Error(), p.epic.ref()) {
			t.Fatalf("%s on a proposal = %v, want run_owned naming %s", to, err, p.epic.ref())
		}
	}
	if c := f.card(later.ID); c.Confirmed() || f.revision() != before {
		t.Fatalf("refused status changes wrote: %+v", c)
	}
	if _, err := f.s.SetStatus(f.ctx, owner, p.a.ID, StatusDone, "shipped", false); err != nil {
		t.Fatal(err)
	}
}

// A card the owner adds under an approved epic is a proposal, as an
// agent's is (ADR 0006 §6.2): the cards above it keep their confirmation,
// and only approving the epic again confirms it, after the checks every
// listed card gets. Outside approved epics the owner's card under a
// confirmed parent is confirmed.
func TestOwnerCardUnderApprovedEpicIsAProposal(t *testing.T) {
	f := newFixture(t)
	epic, story, one, two := f.tree()
	cmd := sql.NullString{String: "go test ./...", Valid: true}
	for _, id := range []string{one.ID, two.ID} {
		_, err := f.s.Edit(f.ctx, owner, id, Patch{AcceptCmd: &cmd})
		f.must(err)
	}
	f.approve(epic.ID, epic.ID, story.ID, one.ID, two.ID)

	f.clock.advance(time.Hour)
	added := f.create(owner, story.ID, KindSubtask, "Three")
	other := f.create(owner, epic.ID, KindStory, "Other")
	under := f.create(owner, other.ID, KindSubtask, "Four")
	expires := f.clock.Now().Add(ExpiryWindow)
	for _, c := range []Card{added, other, under} {
		if got := f.card(c.ID); got.Confirmed() || got.ExpiresAt == nil || !got.ExpiresAt.Equal(expires) || got.PinnedSHA != "" {
			t.Fatalf("%s added after the approval = %+v", c.Title, got)
		}
	}
	for _, c := range []Card{epic, story} {
		if !f.card(c.ID).Confirmed() {
			t.Fatalf("%s lost its confirmation", c.Title)
		}
	}

	all := []string{epic.ID, story.ID, one.ID, two.ID, added.ID, other.ID, under.ID}
	_, err := f.s.Approve(f.ctx, owner, epic.ID, testRun, f.items(all...), "")
	wantRefusal(t, err, CodeInvalid, added.ref(), under.ref())
	f.acceptCmd("go test ./...")
	f.approve(epic.ID, all...)
	for _, c := range []Card{added, other, under} {
		if !f.card(c.ID).Confirmed() {
			t.Fatalf("%s after approving again = %+v", c.Title, f.card(c.ID))
		}
	}

	_, plainStory, _, _ := f.tree2()
	if c := f.create(owner, plainStory.ID, KindSubtask, "Plain"); !c.Confirmed() || c.PinnedSHA != "head-1" {
		t.Fatalf("the owner's card outside approved epics = %+v", c)
	}
}

// A finished approved epic opens again by approving what was added under it
// since (ADR 0006 §6.2): the owner's card or an agent's there is a proposal,
// and approving the epic again confirms it, so the epic is no longer done.
// With nothing added, it has nothing left to run.
func TestApproveReopensAFinishedEpic(t *testing.T) {
	f := newFixture(t)
	f.acceptCmd("go test ./...")
	epic, story, one, two := f.tree()
	f.approve(epic.ID, epic.ID, story.ID, one.ID, two.ID)
	for _, c := range []Card{one, two} {
		_, err := f.s.SetStatus(f.ctx, owner, c.ID, StatusDone, "shipped", false)
		f.must(err)
	}
	wantStatus(t, f.card(epic.ID), StatusDone)
	_, err := f.s.Approve(f.ctx, owner, epic.ID, testRun, f.items(f.shownUnder(epic.ID)...), "")
	wantCode(t, err, CodeInvalid)

	added := f.create(owner, story.ID, KindSubtask, "Follow-up")
	f.must(f.s.StartPlanning(f.ctx, owner, epic.ID, "planner"))
	idea := f.create(Agent("planner", ""), epic.ID, KindSubtask, "Idea")
	wantStatus(t, f.card(epic.ID), StatusDone)
	f.approveRun(epic.ID, testRun)
	for _, c := range []Card{added, idea} {
		if got := f.card(c.ID); !got.Confirmed() || got.Status != StatusPlanned {
			t.Fatalf("%s after approving the finished epic again = %+v", c.Title, got)
		}
	}
	wantStatus(t, f.card(epic.ID), StatusDoing)
	wantStatus(t, f.card(story.ID), StatusDoing)
}
