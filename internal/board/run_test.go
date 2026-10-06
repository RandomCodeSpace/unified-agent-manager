package board

import (
	"context"
	"path/filepath"
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
	c, err := f.s.Approve(f.ctx, owner, epic, testRun, f.items(refs...))
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

// Approve confirms exactly the listed proposals, nested ones included, pins
// them to the owner's HEAD and records the run on the epic. A proposal added
// later stays one until an approval lists it.
func TestApproveConfirmsListedSubtree(t *testing.T) {
	f := newFixture(t)
	f.acceptCmd("go test ./...")
	planner := Agent("planner", "")
	p := f.agentPlan(planner)
	got, err := f.s.Approve(f.ctx, Owner("head-2"), p.epic.ID, testRun, f.items(p.ids()...))
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
	got, err = f.s.Approve(f.ctx, owner, p.epic.ID, again, f.items(p.ids()...))
	f.must(err)
	if got.Run.RunSettings != again {
		t.Fatalf("approving again kept the run %+v", got.Run)
	}
	if c := f.card(later.ID); c.Confirmed() {
		t.Fatalf("an unlisted proposal = %+v", c)
	}
	f.approve(p.epic.ID, append(p.ids(), later.ID)...)
	if c := f.card(later.ID); !c.Confirmed() {
		t.Fatalf("a listed proposal = %+v", c)
	}

	other := f.create(owner, "", KindEpic, "Other")
	_, err = f.s.Approve(f.ctx, owner, p.epic.ID, testRun, f.items(p.epic.ID, other.ID))
	wantRefusal(t, err, CodeInvalid, other.ref())
	_, err = f.s.Approve(f.ctx, owner, p.one.ID, testRun, f.items(p.one.ID))
	wantCode(t, err, CodeInvalid)
	_, err = f.s.Approve(f.ctx, planner, p.epic.ID, testRun, f.items(p.ids()...))
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
		_, err := f.s.Approve(f.ctx, owner, epic.ID, settings, items)
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
	refused(testRun, f.items(epic.ID), CodeInvalid, one.ref())
	f.acceptCmd("go test ./...")

	empty := f.create(owner, epic.ID, KindStory, "Empty")
	refused(testRun, f.items(epic.ID), CodeInvalid, empty.ref())
	_, err := f.s.SetStatus(f.ctx, owner, empty.ID, StatusCancelled, "not needed", false)
	f.must(err)

	// A story whose only live subtasks are proposals the dialog did not list.
	f.must(f.s.StartPlanning(f.ctx, owner, epic.ID, "planner"))
	proposed := f.create(planner, epic.ID, KindStory, "Proposed")
	idea := f.create(planner, proposed.ID, KindSubtask, "Idea")
	refused(testRun, f.items(epic.ID, proposed.ID), CodeInvalid, proposed.ref())
	_, err = f.s.Dismiss(f.ctx, owner, proposed.ID)
	f.must(err)
	_ = idea

	for _, bad := range []RunSettings{
		{Provider: "copilot", Mode: "safe", Parallel: 2},
		{Provider: "copilot", Model: " ", Mode: "safe", Parallel: 2},
		{Provider: "copilot", Model: "m", Mode: "auto", Parallel: 2},
		{Provider: "copilot", Model: "m", Mode: "safe", Parallel: 0},
		{Provider: "copilot", Model: "m", Mode: "safe", Parallel: 5},
		{Model: "m", Mode: "safe", Parallel: 2},
	} {
		refused(bad, f.items(epic.ID), CodeInvalid)
	}

	// A card changed after the dialog showed it.
	items := f.items(epic.ID, one.ID)
	_, err = f.s.Edit(f.ctx, owner, one.ID, Patch{Title: ptr("One, renamed")})
	f.must(err)
	refused(testRun, items, CodeStale, epic.ref(), one.ref())

	// Manual and lane work never mix: a held subtask is finished or released first.
	f.launch(one.ID, "worker")
	refused(testRun, f.items(epic.ID), CodeInvalid, one.ref())
	_, err = f.s.ReleaseHold(f.ctx, owner, one.ID, ReleaseOwner, "")
	f.must(err)

	_, err = f.s.SetStatus(f.ctx, owner, one.ID, StatusDone, "shipped", false)
	f.must(err)
	wantStatus(t, f.card(epic.ID), StatusDone)
	refused(testRun, f.items(epic.ID), CodeInvalid)

	cancelled := f.create(owner, "", KindEpic, "Cancelled")
	f.create(owner, f.create(owner, cancelled.ID, KindStory, "S").ID, KindSubtask, "T")
	_, err = f.s.SetStatus(f.ctx, owner, cancelled.ID, StatusCancelled, "dropped", false)
	f.must(err)
	_, err = f.s.Approve(f.ctx, owner, cancelled.ID, testRun, f.items(cancelled.ID))
	wantCode(t, err, CodeInvalid)
}

// The pause is the owner's flag, under an approved epic only, and holds on a
// card that has started.
func TestPausedIsOwnerOnly(t *testing.T) {
	f := newFixture(t)
	f.acceptCmd("go test ./...")
	epic, story, one, two := f.tree()
	_, err := f.s.Edit(f.ctx, owner, story.ID, Patch{Paused: ptr(true)})
	wantCode(t, err, CodeInvalid) // not approved yet
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
	f.approve(epic.ID, epic.ID)
	if f.card(epic.ID).Paused != "" || f.card(story.ID).Paused != PausedOwner {
		t.Fatalf("after approving again: epic %q, story %q", f.card(epic.ID).Paused, f.card(story.ID).Paused)
	}
	// Moved out of the approved epic, a card and everything under it drop
	// their pauses, which nothing could clear there.
	plain := f.create(owner, "", KindEpic, "Plain")
	moving := f.create(owner, epic.ID, KindStory, "Moving")
	inner := f.create(owner, moving.ID, KindSubtask, "Inner")
	for _, id := range []string{moving.ID, inner.ID} {
		_, err := f.s.Edit(f.ctx, owner, id, Patch{Paused: ptr(true)})
		f.must(err)
	}
	res, err = f.s.Edit(f.ctx, owner, moving.ID, Patch{ParentID: ptr(plain.ID)})
	f.must(err)
	if res.Card.Paused != "" || f.card(inner.ID).Paused != "" {
		t.Fatalf("moved out: story %q, subtask %q", res.Card.Paused, f.card(inner.ID).Paused)
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
	if err := s.db.QueryRow(`SELECT v FROM meta WHERE k = 'schema_version'`).Scan(&version); err != nil || version != "5" || version != strconv.Itoa(len(migrations)) {
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
	if _, err := s.Approve(ctx, owner, epic.ID, testRun, []ApproveItem{{ID: epic.ID, Revision: got.Revision}}); err != nil {
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
