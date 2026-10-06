package board

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// laneRun authorizes four lanes, so the global cap binds before the epic's
// own limit.
var laneRun = RunSettings{Provider: "copilot", Model: "gpt-6-luna", Mode: "yolo", Parallel: 4}

// shownUnder lists what an Approve dialog of epic shows: the epic and every
// card under it that is not cancelled, nor under a cancelled card.
func (f *fixture) shownUnder(epic string) []string {
	f.t.Helper()
	snap, err := f.s.Board(f.ctx, f.card(epic).ProjectID)
	f.must(err)
	kids := map[string][]Card{}
	for _, c := range snap.Cards {
		kids[c.ParentID] = append(kids[c.ParentID], c)
	}
	var walk func(id string) []string
	walk = func(id string) []string {
		out := []string{id}
		for _, k := range kids[id] {
			if k.Status != StatusCancelled {
				out = append(out, walk(k.ID)...)
			}
		}
		return out
	}
	return walk(f.card(epic).ID)
}

// approveRun approves epic with run, listing everything the dialog shows.
func (f *fixture) approveRun(epic string, run RunSettings) {
	f.t.Helper()
	if _, err := f.s.Approve(f.ctx, owner, epic, run, f.items(f.shownUnder(epic)...)); err != nil {
		f.t.Fatalf("approve %s: %v", epic, err)
	}
}

// lane starts task's run attempt at the subtask ref, as a lane start does.
func (f *fixture) lane(ref, task string) Card {
	f.t.Helper()
	c, err := f.s.StartRun(f.ctx, Owner(""), ref, task, Baseline{Head: "tip-1"}, Lane{Branch: "uam-plan-p1-" + task})
	if err != nil {
		f.t.Fatalf("start run %s: %v", ref, err)
	}
	return c
}

// landDone files task's done request on the subtask ref after its
// acceptance command passed, as a lane's claim does.
func (f *fixture) landDone(ref, task string) FiledRequest {
	f.t.Helper()
	filed, err := f.s.FileRequestDetail(f.ctx, Agent(task, ""), ref, RequestInput{Kind: RequestDone, Comment: "finished " + ref, PassedCmd: "go test ./..."})
	if err != nil {
		f.t.Fatalf("done request on %s: %v", ref, err)
	}
	return filed
}

// lastHold returns the subtask ref's latest attempt.
func (f *fixture) lastHold(ref string) Hold {
	f.t.Helper()
	holds := f.detail(ref).Holds
	if len(holds) == 0 {
		f.t.Fatalf("%s has no attempts", ref)
	}
	return holds[len(holds)-1]
}

func refsOf(cards ...Card) []string {
	out := make([]string, len(cards))
	for i, c := range cards {
		out[i] = c.ref()
	}
	return out
}

const landedSHA = "9f3e2a1c0b4d5e6f708192a3b4c5d6e7f8091a2b"

// A run start holds the subtask for a new Task scoped to that subtask
// alone: it claims no sibling and creates no card.
func TestStartRunScopesTheLeaf(t *testing.T) {
	f := newFixture(t)
	f.acceptCmd("go test ./...")
	epic, story, one, two := f.tree()
	_, err := f.s.StartRun(f.ctx, Owner(""), one.ID, "run-1", Baseline{Head: "tip-1"}, Lane{Branch: "b"})
	wantCode(t, err, CodeInvalid) // no approved epic
	f.approveRun(epic.ID, testRun)

	got, err := f.s.StartRun(f.ctx, Owner("head-9"), one.ID, "run-1", Baseline{Head: "tip-1"}, Lane{Branch: "uam-plan-p1-1-abcd"})
	f.must(err)
	if got.Status != StatusDoing || got.HeldBy != "run-1" || got.PinnedSHA != "head-1" || got.Lane == nil || *got.Lane != (Lane{Branch: "uam-plan-p1-1-abcd"}) {
		t.Fatalf("started %+v, lane %+v", got, got.Lane)
	}
	h := f.lastHold(one.ID)
	if h.EndedAt != nil || h.TaskID != "run-1" || h.Lane.Branch != "uam-plan-p1-1-abcd" || h.Baseline.Head != "tip-1" || len(h.Baseline.Dirty) != 0 {
		t.Fatalf("hold = %+v", h)
	}
	if !hasComment(f.comments(one.ID), "uam: started by the run of "+epic.ref()) {
		t.Fatalf("comments = %v", f.comments(one.ID))
	}

	run := Agent("run-1", "")
	_, err = f.s.Claim(f.ctx, run, two.ID, Baseline{})
	wantCode(t, err, CodeForbidden)
	for _, in := range []NewCard{
		{ProjectID: proj, Kind: KindSubtask, ParentID: story.ID, Title: "Sibling"},
		{ProjectID: proj, Kind: KindStory, ParentID: epic.ID, Title: "Story"},
		{ProjectID: proj, Kind: KindEpic, Title: "Epic of its own"},
	} {
		_, err := f.s.Create(f.ctx, run, in)
		wantCode(t, err, CodeForbidden)
	}
	if _, err := f.s.AddComment(f.ctx, run, one.ID, "parser done"); err != nil {
		t.Fatal(err)
	}

	// The subtask is held, so the same start again is not ready.
	_, err = f.s.StartRun(f.ctx, Owner(""), one.ID, "run-2", Baseline{}, Lane{Branch: "c"})
	wantRefusal(t, err, CodeNotReady, one.ref())
	// A run start is uam's, on a subtask, for a Task, on a branch.
	_, err = f.s.StartRun(f.ctx, run, two.ID, "run-3", Baseline{}, Lane{Branch: "d"})
	wantCode(t, err, CodeForbidden)
	_, err = f.s.StartRun(f.ctx, Owner(""), two.ID, " ", Baseline{}, Lane{Branch: "d"})
	wantCode(t, err, CodeInvalid)
	_, err = f.s.StartRun(f.ctx, Owner(""), two.ID, "run-3", Baseline{}, Lane{})
	wantCode(t, err, CodeInvalid)
	_, err = f.s.StartRun(f.ctx, Owner(""), story.ID, "run-3", Baseline{}, Lane{Branch: "d"})
	wantCode(t, err, CodeInvalid)
}

// A subtask runs only when it is ready: confirmed, nothing at or above it
// paused, not flagged blocked, and waiting on nothing open, its own
// blockers and its story's and epic's included.
func TestStartRunRefusesNotReady(t *testing.T) {
	f := newFixture(t)
	f.acceptCmd("go test ./...")
	epic := f.create(owner, "", KindEpic, "Epic")
	a := f.create(owner, epic.ID, KindStory, "A")
	a1 := f.create(owner, a.ID, KindSubtask, "a1")
	a2 := f.create(owner, a.ID, KindSubtask, "a2")
	a3 := f.create(owner, a.ID, KindSubtask, "a3")
	a4 := f.create(owner, a.ID, KindSubtask, "a4")
	b := f.create(owner, epic.ID, KindStory, "B")
	b1 := f.create(owner, b.ID, KindSubtask, "b1")
	d := f.create(owner, epic.ID, KindStory, "D")
	d1 := f.create(owner, d.ID, KindSubtask, "d1")
	open := f.create(owner, "", KindEpic, "Open")
	f.create(owner, f.create(owner, open.ID, KindStory, "Open story").ID, KindSubtask, "o1")
	later := f.create(owner, "", KindEpic, "Later")
	g := f.create(owner, later.ID, KindStory, "G")
	g1 := f.create(owner, g.ID, KindSubtask, "g1")
	for _, l := range [][2]Card{{a1, a2}, {a, b}, {open, later}} {
		f.must(f.s.Link(f.ctx, owner, l[0].ID, l[1].ID))
	}
	f.approveRun(epic.ID, testRun)
	f.approveRun(later.ID, testRun)
	for _, c := range []Card{a4, d} {
		_, err := f.s.Edit(f.ctx, owner, c.ID, Patch{Paused: ptr(true)})
		f.must(err)
	}
	_, err := f.s.Edit(f.ctx, owner, a3.ID, Patch{Blocked: ptr(true)})
	f.must(err)
	f.must(f.s.StartPlanning(f.ctx, owner, epic.ID, "planner"))
	a5 := f.create(Agent("planner", ""), a.ID, KindSubtask, "a5")
	loose := f.create(owner, "", KindSubtask, "Loose")
	unapproved := f.create(owner, open.ID, KindSubtask, "Unapproved")

	f.must(f.s.CanStart(f.ctx, a1.ID))
	before := f.revision()
	for name, tc := range map[string]struct {
		card Card
		refs []string
	}{
		"its own blocker":     {a2, refsOf(a1)},
		"its story's blocker": {b1, refsOf(a)},
		"its epic's blocker":  {g1, refsOf(open)},
		"paused itself":       {a4, refsOf(a4)},
		"under a pause":       {d1, refsOf(d)},
		"flagged blocked":     {a3, refsOf(a3)},
		"a proposal":          {a5, refsOf(a5)},
		"a root subtask":      {loose, nil},
		"under no approval":   {unapproved, nil},
		"a story":             {a, nil},
	} {
		check := f.s.CanStart(f.ctx, tc.card.ID)
		_, err := f.s.StartRun(f.ctx, Owner(""), tc.card.ID, "run", Baseline{}, Lane{Branch: "b"})
		for _, err := range []error{check, err} {
			if tc.refs == nil {
				if CodeOf(err) != CodeInvalid {
					t.Errorf("%s: %v, want invalid", name, err)
				}
				continue
			}
			var e *Error
			if CodeOf(err) != CodeNotReady || !errors.As(err, &e) || !slices.Equal(e.Refs, tc.refs) {
				t.Errorf("%s: %v, want not_ready about %v", name, err, tc.refs)
			}
		}
	}
	if f.revision() != before {
		t.Fatalf("refused starts wrote: revision %d → %d", before, f.revision())
	}
	// Once its blocker is done, the subtask is ready.
	_, err = f.s.SetStatus(f.ctx, owner, a1.ID, StatusDone, "shipped", false)
	f.must(err)
	f.must(f.s.CanStart(f.ctx, a2.ID))
	f.lane(a2.ID, "run")
}

// Slots are counted from the stored lane holds alone: the epic's parallel
// limit and the global cap bind whatever the holders' stages, and a hold
// with a pending done, blocked or split request from its Task frees its
// slot.
func TestLaneSlotsComeFromStoredHolds(t *testing.T) {
	f := newFixture(t)
	f.acceptCmd("go test ./...")
	one := f.create(owner, "", KindEpic, "One")
	story := f.create(owner, one.ID, KindStory, "Story")
	var s []Card
	for _, title := range []string{"s0", "s1", "s2", "s3", "s4", "s5"} {
		s = append(s, f.create(owner, story.ID, KindSubtask, title))
	}
	f.approveRun(one.ID, testRun) // two at a time
	f.lane(s[0].ID, "t0")
	f.lane(s[1].ID, "t1")
	_, err := f.s.StartRun(f.ctx, Owner(""), s[2].ID, "t2", Baseline{}, Lane{Branch: "b2"})
	wantRefusal(t, err, CodeNotReady, s[0].ref(), s[1].ref())
	wantRefusal(t, f.s.CanStart(f.ctx, s[2].ID), CodeNotReady, s[0].ref(), s[1].ref())
	// The store knows no Task stages: Settled holders keep their slots.
	n, err := f.s.Reconcile(f.ctx, map[string]Stage{"t0": StageSettled, "t1": StageSettled}, f.asOf(), nil)
	if err != nil || n != 0 {
		t.Fatalf("reconcile = %d, %v", n, err)
	}
	wantCode(t, f.s.CanStart(f.ctx, s[2].ID), CodeNotReady)
	// A change or cancel request frees nothing.
	_, err = f.s.FileRequest(f.ctx, Agent("t0", ""), s[0].ID, RequestInput{Kind: RequestCancel, Comment: "not needed"})
	f.must(err)
	wantCode(t, f.s.CanStart(f.ctx, s[2].ID), CodeNotReady)

	f.landDone(s[0].ID, "t0")
	f.lane(s[2].ID, "t2")
	_, err = f.s.FileRequest(f.ctx, Agent("t1", ""), s[1].ID, RequestInput{Kind: RequestBlocked, Comment: "needs a decision"})
	f.must(err)
	f.lane(s[3].ID, "t3")
	if res, err := f.s.Split(f.ctx, Agent("t2", ""), s[2].ID, []SplitChild{{Title: "Half"}}); err != nil || res.Request == nil {
		t.Fatalf("split = %+v, %v", res, err)
	}
	f.lane(s[4].ID, "t4")
	wantRefusal(t, f.s.CanStart(f.ctx, s[5].ID), CodeNotReady, s[3].ref(), s[4].ref())

	// The global cap counts every Project's lanes: two more start, and the
	// next is refused though its epic allows four.
	two := f.create(owner, "", KindEpic, "Two")
	twoStory := f.create(owner, two.ID, KindStory, "Two story")
	var u []Card
	for _, title := range []string{"u0", "u1", "u2"} {
		u = append(u, f.create(owner, twoStory.ID, KindSubtask, title))
	}
	f.approveRun(two.ID, laneRun)
	f.lane(u[0].ID, "v0")
	f.lane(u[1].ID, "v1")
	inUse := refsOf(s[3], s[4], u[0], u[1])
	wantRefusal(t, f.s.CanStart(f.ctx, u[2].ID), CodeNotReady, inUse...)
	f.must(f.s.SetProjectAcceptCmd(f.ctx, owner, "p2", "go test ./..."))
	other, err := f.s.Create(f.ctx, owner, NewCard{ProjectID: "p2", Kind: KindEpic, Title: "Elsewhere"})
	f.must(err)
	leaf, err := f.s.Create(f.ctx, owner, NewCard{Kind: KindSubtask, ParentID: other.ID, Title: "w0"})
	f.must(err)
	f.approveRun(other.ID, laneRun)
	_, err = f.s.StartRun(f.ctx, Owner(""), leaf.ID, "w0", Baseline{}, Lane{Branch: "bw"})
	wantRefusal(t, err, CodeNotReady, inUse...)
}

// A run start records the done subtasks it waited on: every done subtask at
// or under a blocker of the subtask or of one of its ancestors.
func TestStartRunRecordsWaitedOn(t *testing.T) {
	f := newFixture(t)
	f.acceptCmd("go test ./...")
	epic := f.create(owner, "", KindEpic, "Epic")
	a := f.create(owner, epic.ID, KindStory, "A")
	a1 := f.create(owner, a.ID, KindSubtask, "a1")
	a2 := f.create(owner, a.ID, KindSubtask, "a2")
	a3 := f.create(owner, a.ID, KindSubtask, "a3")
	b := f.create(owner, epic.ID, KindStory, "B")
	b0 := f.create(owner, b.ID, KindSubtask, "b0")
	b1 := f.create(owner, b.ID, KindSubtask, "b1")
	c := f.create(owner, epic.ID, KindStory, "C")
	c1 := f.create(owner, c.ID, KindSubtask, "c1")
	c2 := f.create(owner, c.ID, KindSubtask, "c2")
	z := f.create(owner, "", KindEpic, "Z")
	z1 := f.create(owner, f.create(owner, z.ID, KindStory, "Z story").ID, KindSubtask, "z1")
	// A done card keeps its links, so the subtasks are linked first and the
	// containers once their work is done.
	f.must(f.s.Link(f.ctx, owner, b0.ID, b1.ID))
	for _, d := range []Card{a1, a2, b0, c1, z1} {
		_, err := f.s.SetStatus(f.ctx, owner, d.ID, StatusDone, "shipped", false)
		f.must(err)
	}
	_, err := f.s.SetStatus(f.ctx, owner, a3.ID, StatusCancelled, "not needed", false)
	f.must(err)
	for _, l := range [][2]Card{{a, b}, {z, epic}} {
		f.must(f.s.Link(f.ctx, owner, l[0].ID, l[1].ID))
	}
	f.approveRun(epic.ID, testRun)

	f.lane(b1.ID, "run-b")
	want := []string{a1.ID, a2.ID, b0.ID, z1.ID}
	if got := f.lastHold(b1.ID).WaitedOn; !slices.Equal(got, want) {
		t.Fatalf("waited on %v, want %v", got, want)
	}
	// A done subtask that blocks nothing is not waited on.
	f.lane(c2.ID, "run-c")
	if got := f.lastHold(c2.ID).WaitedOn; !slices.Equal(got, []string{z1.ID}) {
		t.Fatalf("waited on %v, want only %s", got, z1.ID)
	}
	// A subtask that waited on nothing records none.
	plain, _, plainOne, _ := f.tree2()
	f.approveRun(plain.ID, testRun)
	f.lane(plainOne.ID, "run-plain")
	if got := f.lastHold(plainOne.ID).WaitedOn; got == nil || len(got) != 0 {
		t.Fatalf("waited on %#v, want none", got)
	}
	// Links changed later leave the record be.
	f.must(f.s.Unlink(f.ctx, owner, z.ID, epic.ID))
	if got := f.lastHold(b1.ID).WaitedOn; !slices.Equal(got, want) {
		t.Fatalf("after unlinking: waited on %v", got)
	}
}

// When a lane's hold ends without landing, the subtask pauses: uam's pause
// when the attempt ended or was rejected, the owner's when the owner
// stopped it. A hold that ended otherwise pauses nothing.
func TestLaneReleasePausesByReason(t *testing.T) {
	f := newFixture(t)
	f.acceptCmd("go test ./...")
	epic := f.create(owner, "", KindEpic, "Epic")
	story := f.create(owner, epic.ID, KindStory, "Story")
	r := map[string]Card{}
	for _, name := range []string{"ended", "rejected", "released", "settled", "aborted", "blocked", "accepted", "cancelled"} {
		r[name] = f.create(owner, story.ID, KindSubtask, name)
	}
	later := f.create(owner, epic.ID, KindStory, "Later")
	under := f.create(owner, later.ID, KindSubtask, "under a pause")
	f.approveRun(epic.ID, laneRun)
	branch := func(task string) string { return "uam-plan-p1-" + task }
	release := map[string]func(c Card){
		"ended": func(c Card) {
			_, err := f.s.Reconcile(f.ctx, map[string]Stage{}, f.asOf(), map[string][]string{proj: {"owner.go"}})
			f.must(err)
		},
		"rejected": func(c Card) {
			_, err := f.s.Reject(f.ctx, owner, f.landDone(c.ID, "t-rejected").Request.ID, "not what was asked", false)
			f.must(err)
		},
		"released": func(c Card) {
			_, err := f.s.ReleaseHold(f.ctx, owner, c.ID, ReleaseOwner, "")
			f.must(err)
		},
		"settled": func(c Card) {
			_, err := f.s.ReleaseHold(f.ctx, owner, c.ID, ReleaseSettled, "")
			f.must(err)
		},
		"aborted": func(c Card) {
			_, err := f.s.AbortRun(f.ctx, c.ID, "t-aborted", "the first prompt was refused")
			f.must(err)
		},
		"blocked": func(c Card) {
			req, err := f.s.FileRequest(f.ctx, Agent("t-blocked", ""), c.ID, RequestInput{Kind: RequestBlocked, Comment: "stuck"})
			f.must(err)
			_, err = f.s.Accept(f.ctx, owner, req.ID, "")
			f.must(err)
		},
		"accepted": func(c Card) {
			id := f.landDone(c.ID, "t-accepted").Request.ID
			f.must(f.s.MarkLanding(f.ctx, id, landedSHA))
			_, err := f.s.AcceptLanded(f.ctx, id, landedSHA, "uam-plan-p1", DecidedByUAM, "")
			f.must(err)
		},
		"cancelled": func(c Card) {
			_, err := f.s.SetStatus(f.ctx, owner, c.ID, StatusCancelled, "dropped", false)
			f.must(err)
		},
	}
	for _, tc := range []struct {
		reason ReleaseReason
		paused string
		note   string
	}{
		{ReleaseEnded, PausedUAM, "paused: attempt #1 ended without landing; branch " + branch("t-ended") + " kept"},
		{ReleaseRejected, PausedUAM, "paused: attempt #1 ended without landing; branch " + branch("t-rejected") + " kept"},
		{ReleaseOwner, PausedOwner, "paused: attempt #1 stopped; branch " + branch("t-released") + " kept"},
		{ReleaseSettled, PausedOwner, "paused: attempt #1 stopped; branch " + branch("t-settled") + " kept"},
		{ReleaseAborted, "", "attempt #1 aborted: the first prompt was refused"},
		{ReleaseBlocked, "", ""},
		{ReleaseAccepted, "", ""},
		{ReleaseCancelled, "", ""},
	} {
		name := string(tc.reason)
		c := r[name]
		f.lane(c.ID, "t-"+name)
		release[name](c)
		got := f.card(c.ID)
		if got.HeldBy != "" || got.Paused != tc.paused || f.lastHold(c.ID).EndReason != tc.reason {
			t.Errorf("%s: card %+v, hold %+v", name, got, f.lastHold(c.ID))
		}
		comments := f.comments(c.ID)
		if tc.note != "" && !hasComment(comments, "uam: "+tc.note) {
			t.Errorf("%s: comments %v, want %q", name, comments, tc.note)
		}
		if hasComment(comments, "uncommitted") || (tc.note == "" && hasComment(comments, "paused")) {
			t.Errorf("%s: comments %v", name, comments)
		}
	}

	// A subtask already under a pause gets no pause of its own.
	f.lane(under.ID, "t-under")
	_, err := f.s.Edit(f.ctx, owner, later.ID, Patch{Paused: ptr(true)})
	f.must(err)
	_, err = f.s.Reconcile(f.ctx, map[string]Stage{}, f.asOf(), nil)
	f.must(err)
	if got := f.card(under.ID); got.Paused != "" || !hasComment(f.comments(under.ID), "uam: attempt #1 ended without landing; branch "+branch("t-under")+" kept") {
		t.Fatalf("under a pause: %+v, %v", got, f.comments(under.ID))
	}
}

// An accepted blocked request on a lane hold sets the flag and ends the
// attempt, with no pause: the flag holds the subtask back, and clearing it
// lets it run again.
func TestAcceptBlockedOnALaneHoldEndsIt(t *testing.T) {
	f := newFixture(t)
	f.acceptCmd("go test ./...")
	epic, _, one, _ := f.tree()
	f.approveRun(epic.ID, testRun)
	f.lane(one.ID, "run")
	req, err := f.s.FileRequest(f.ctx, Agent("run", ""), one.ID, RequestInput{Kind: RequestBlocked, Comment: "needs the parser"})
	f.must(err)
	_, err = f.s.Accept(f.ctx, owner, req.ID, "")
	f.must(err)
	got := f.card(one.ID)
	if got.Status != StatusTodo || got.HeldBy != "" || !got.Blocked || got.Paused != "" || f.lastHold(one.ID).EndReason != ReleaseBlocked {
		t.Fatalf("after the accepted blocked request: %+v, hold %+v", got, f.lastHold(one.ID))
	}
	wantRefusal(t, f.s.CanStart(f.ctx, one.ID), CodeNotReady, one.ref())
	_, err = f.s.Edit(f.ctx, owner, one.ID, Patch{Blocked: ptr(false)})
	f.must(err)
	f.lane(one.ID, "run-2")
	if h := f.lastHold(one.ID); h.Attempt != 2 || h.TaskID != "run-2" {
		t.Fatalf("second attempt = %+v", h)
	}

	// A manual hold outside approved epics keeps its hold, as before.
	_, _, plain, _ := f.tree2()
	f.launch(plain.ID, "manual")
	req, err = f.s.FileRequest(f.ctx, Agent("manual", ""), plain.ID, RequestInput{Kind: RequestBlocked, Comment: "stuck"})
	f.must(err)
	_, err = f.s.Accept(f.ctx, owner, req.ID, "")
	f.must(err)
	if got := f.card(plain.ID); got.HeldBy != "manual" || !got.Blocked {
		t.Fatalf("manual hold after the accepted blocked request: %+v", got)
	}
}

// While an open hold carries a landing intent, nothing changes the card's
// status or ends its hold, and reconcile leaves it for recovery: only
// ClearLanding and AcceptLanded pass.
func TestLandingIntentIsSticky(t *testing.T) {
	f := newFixture(t)
	f.acceptCmd("go test ./...")
	epic, story, one, two := f.tree()
	f.approveRun(epic.ID, testRun)
	f.lane(one.ID, "run")
	filed := f.landDone(one.ID, "run")
	if filed.Wait != WaitLanding || filed.Request.Status != RequestPending {
		t.Fatalf("lane done = %+v", filed)
	}
	var p struct {
		Landing bool `json:"landing"`
	}
	if err := json.Unmarshal(filed.Request.Payload, &p); err != nil || !p.Landing {
		t.Fatalf("payload = %s, %v", filed.Request.Payload, err)
	}
	id := filed.Request.ID
	f.must(f.s.MarkLanding(f.ctx, id, landedSHA))
	if got := f.card(one.ID); got.Status != StatusDoing || got.Lane == nil || got.Lane.LandedSHA != landedSHA {
		t.Fatalf("landing card = %+v, lane %+v", got, got.Lane)
	}
	wantCode(t, f.s.MarkLanding(f.ctx, id, "0123456"), CodeLanding)

	before := f.revision()
	run := Agent("run", "")
	for name, err := range map[string]error{
		"release": func() error { _, err := f.s.ReleaseHold(f.ctx, owner, one.ID, ReleaseOwner, ""); return err }(),
		"settle":  func() error { _, err := f.s.ReleaseHold(f.ctx, owner, one.ID, ReleaseSettled, ""); return err }(),
		"cancel": func() error {
			_, err := f.s.SetStatus(f.ctx, owner, one.ID, StatusCancelled, "dropped", false)
			return err
		}(),
		"cancel its story": func() error {
			_, err := f.s.SetStatus(f.ctx, owner, story.ID, StatusCancelled, "dropped", false)
			return err
		}(),
		"back to To do": func() error { _, err := f.s.SetStatus(f.ctx, owner, one.ID, StatusTodo, "", false); return err }(),
		"reject":        func() error { _, err := f.s.Reject(f.ctx, owner, id, "no", false); return err }(),
		"reject, live":  func() error { _, err := f.s.Reject(f.ctx, owner, id, "no", true); return err }(),
		"file done again": func() error {
			_, err := f.s.FileRequest(f.ctx, run, one.ID, RequestInput{Kind: RequestDone, Comment: "again"})
			return err
		}(),
		"file blocked": func() error {
			_, err := f.s.FileRequest(f.ctx, run, one.ID, RequestInput{Kind: RequestBlocked, Comment: "stuck"})
			return err
		}(),
		"abort":       func() error { _, err := f.s.AbortRun(f.ctx, one.ID, "run", ""); return err }(),
		"land failed": func() error { _, err := f.s.LandFailed(f.ctx, id, "conflict", false); return err }(),
	} {
		if CodeOf(err) != CodeLanding {
			t.Errorf("%s = %v, want landing", name, err)
		}
	}
	if f.revision() != before {
		t.Fatalf("refused writes moved the revision from %d to %d", before, f.revision())
	}
	if n, err := f.s.Reconcile(f.ctx, map[string]Stage{}, f.asOf(), nil); err != nil || n != 0 || f.card(one.ID).HeldBy != "run" {
		t.Fatalf("reconcile = %d, %v; card %+v", n, err, f.card(one.ID))
	}

	f.must(f.s.ClearLanding(f.ctx, id))
	if got := f.card(one.ID); got.Lane.LandedSHA != "" || got.PendingRequests != 1 {
		t.Fatalf("after clearing: %+v, lane %+v", got, got.Lane)
	}
	f.must(f.s.MarkLanding(f.ctx, id, landedSHA))
	if _, err := f.s.AcceptLanded(f.ctx, id, landedSHA, "uam-plan-p1", DecidedByUAM, ""); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, f.card(one.ID), StatusDone)

	// A removed Project's landing ends with it: its repository is gone.
	f.lane(two.ID, "run-2")
	f.must(f.s.MarkLanding(f.ctx, f.landDone(two.ID, "run-2").Request.ID, landedSHA))
	if _, err := f.s.Unassign(f.ctx, proj); err != nil {
		t.Fatal(err)
	}
	if got := f.card(two.ID); got.ProjectID != "" || got.HeldBy != "" || got.Paused != "" || f.lastHold(two.ID).EndReason != ReleaseEnded {
		t.Fatalf("unassigned while landing: %+v", got)
	}
}

// AcceptLanded finishes a landing: the request is accepted by the decider
// given, the subtask is done, and its hold keeps the landed sha.
func TestAcceptLandedRecordsSha(t *testing.T) {
	f := newFixture(t)
	f.acceptCmd("go test ./...")
	epic, story, one, two := f.tree()
	three := f.create(owner, story.ID, KindSubtask, "Three")
	f.approveRun(epic.ID, testRun)
	f.lane(one.ID, "run")
	// Mark done would skip landing.
	_, err := f.s.SetStatus(f.ctx, owner, one.ID, StatusDone, "by hand", true)
	wantCode(t, err, CodeInvalid)
	id := f.landDone(one.ID, "run").Request.ID
	_, err = f.s.AcceptLanded(f.ctx, id, landedSHA, "uam-plan-p1", DecidedByUAM, "")
	wantCode(t, err, CodeInvalid) // no intent
	f.must(f.s.MarkLanding(f.ctx, id, landedSHA))
	_, err = f.s.AcceptLanded(f.ctx, id, "0123456789", "uam-plan-p1", DecidedByUAM, "")
	wantCode(t, err, CodeInvalid) // another sha
	r, err := f.s.AcceptLanded(f.ctx, id, landedSHA, "uam-plan-p1", DecidedByUAM, "")
	f.must(err)
	if r.Status != RequestAccepted || r.DecidedBy != DecidedByUAM {
		t.Fatalf("request = %+v", r)
	}
	got := f.card(one.ID)
	if got.Status != StatusDone || got.HeldBy != "" || got.Paused != "" || got.Lane == nil || *got.Lane != (Lane{Branch: "uam-plan-p1-run", LandedSHA: landedSHA}) {
		t.Fatalf("landed card = %+v, lane %+v", got, got.Lane)
	}
	if h := f.lastHold(one.ID); h.EndReason != ReleaseAccepted || h.Lane.LandedSHA != landedSHA {
		t.Fatalf("hold = %+v", h)
	}
	if c := f.comments(one.ID); !hasComment(c, "finished "+one.ID) || !hasComment(c, "uam: Landed on uam-plan-p1 as 9f3e2a1") {
		t.Fatalf("comments = %v", c)
	}

	// The owner's land job decides as the owner, with the owner's note.
	f.lane(two.ID, "run-2")
	filed, err := f.s.FileRequestDetail(f.ctx, Agent("run-2", ""), two.ID, RequestInput{Kind: RequestDone, Comment: "done"})
	f.must(err)
	if filed.Wait != WaitCommandChanged {
		t.Fatalf("wait = %q", filed.Wait)
	}
	// The plain Accept does not land it.
	_, err = f.s.Accept(f.ctx, owner, filed.Request.ID, "")
	wantCode(t, err, CodeInvalid)
	f.must(f.s.MarkLanding(f.ctx, filed.Request.ID, landedSHA))
	r, err = f.s.AcceptLanded(f.ctx, filed.Request.ID, landedSHA, "uam-plan-p1", DecidedByOwner, "looks right")
	f.must(err)
	if r.DecidedBy != DecidedByOwner || r.DecisionComment != "looks right" {
		t.Fatalf("owner's landing = %+v", r)
	}

	// Only a pending done request on a lane hold lands.
	manual := f.create(owner, f.create(owner, "", KindStory, "Plain").ID, KindSubtask, "Manual")
	f.launch(manual.ID, "manual")
	wantCode(t, f.s.MarkLanding(f.ctx, f.done(manual.ID, Agent("manual", "")).ID, landedSHA), CodeInvalid)
	f.lane(three.ID, "run-3")
	blocked, err := f.s.FileRequest(f.ctx, Agent("run-3", ""), three.ID, RequestInput{Kind: RequestBlocked, Comment: "stuck"})
	f.must(err)
	wantCode(t, f.s.MarkLanding(f.ctx, blocked.ID, landedSHA), CodeInvalid)
	wantCode(t, f.s.MarkLanding(f.ctx, r.ID, landedSHA), CodeInvalid) // accepted
}

// A content failure rejects the lane's done request as uam's decision. A
// live holder keeps its hold to try again; one that is not live is
// released, which pauses the subtask.
func TestLandFailedRejectsAndKeepsTheHold(t *testing.T) {
	f := newFixture(t)
	f.acceptCmd("go test ./...")
	epic, _, one, _ := f.tree()
	f.approveRun(epic.ID, testRun)
	f.lane(one.ID, "run")
	id := f.landDone(one.ID, "run").Request.ID
	r, err := f.s.LandFailed(f.ctx, id, "merge conflict in a.go", true)
	f.must(err)
	if r.Status != RequestRejected || r.DecidedBy != DecidedByUAM || r.DecisionComment != "merge conflict in a.go" {
		t.Fatalf("request = %+v", r)
	}
	got := f.card(one.ID)
	if got.Status != StatusDoing || got.HeldBy != "run" || got.Paused != "" || got.PendingRequests != 0 {
		t.Fatalf("card = %+v", got)
	}
	if !hasComment(f.comments(one.ID), "uam: landing failed: merge conflict in a.go") {
		t.Fatalf("comments = %v", f.comments(one.ID))
	}
	_, err = f.s.LandFailed(f.ctx, id, "again", true)
	wantCode(t, err, CodeInvalid) // decided
	_, err = f.s.LandFailed(f.ctx, f.landDone(one.ID, "run").Request.ID, " ", false)
	wantCode(t, err, CodeInvalid) // no reason

	id = f.landDone(one.ID, "run").Request.ID
	if _, err := f.s.LandFailed(f.ctx, id, "acceptance failed on the new tip", false); err != nil {
		t.Fatal(err)
	}
	got = f.card(one.ID)
	if got.Status != StatusTodo || got.HeldBy != "" || got.Paused != PausedUAM || f.lastHold(one.ID).EndReason != ReleaseRejected {
		t.Fatalf("after a failure with the holder gone: %+v, hold %+v", got, f.lastHold(one.ID))
	}
}

// ownerShape is a story B whose subtask b1 waits on story A (a1 done) and on
// cancelled story C, beside story D (d1, d2), under epic.
type ownerShape struct {
	epic, a, a1, b, b1, c, d, d1 Card
}

func (f *fixture) ownerShape(title string) ownerShape {
	f.t.Helper()
	var s ownerShape
	s.epic = f.create(owner, "", KindEpic, title)
	s.a = f.create(owner, s.epic.ID, KindStory, title+" A")
	s.a1 = f.create(owner, s.a.ID, KindSubtask, title+" a1")
	s.b = f.create(owner, s.epic.ID, KindStory, title+" B")
	s.b1 = f.create(owner, s.b.ID, KindSubtask, title+" b1")
	s.c = f.create(owner, s.epic.ID, KindStory, title+" C")
	f.create(owner, s.c.ID, KindSubtask, title+" c1")
	s.d = f.create(owner, s.epic.ID, KindStory, title+" D")
	s.d1 = f.create(owner, s.d.ID, KindSubtask, title+" d1")
	f.create(owner, s.d.ID, KindSubtask, title+" d2")
	for _, blocker := range []Card{s.a, s.c} {
		f.must(f.s.Link(f.ctx, owner, blocker.ID, s.b.ID))
	}
	_, err := f.s.SetStatus(f.ctx, owner, s.a1.ID, StatusDone, "shipped", false)
	f.must(err)
	_, err = f.s.SetStatus(f.ctx, owner, s.c.ID, StatusCancelled, "not now", false)
	f.must(err)
	return s
}

// ownerWrites are the owner's writes that would give b1 a new open blocker
// or the blocked flag.
func (f *fixture) ownerWrites(s ownerShape) map[string]func() error {
	return map[string]func() error{
		"restore a cancelled blocker": func() error { _, err := f.s.Restore(f.ctx, owner, s.c.ID, "after all"); return err },
		"link":                        func() error { return f.s.Link(f.ctx, owner, s.d.ID, s.b.ID) },
		"move into a done blocker": func() error {
			_, err := f.s.Edit(f.ctx, owner, s.d1.ID, Patch{ParentID: &s.a.ID})
			return err
		},
		"blocked flag": func() error { _, err := f.s.Edit(f.ctx, owner, s.b1.ID, Patch{Blocked: ptr(true)}); return err },
		"reopen":       func() error { _, err := f.s.SetStatus(f.ctx, owner, s.a1.ID, StatusTodo, "", false); return err },
	}
}

// Under an approved epic no owner write gives a running lane a new open
// blocker or the blocked flag: the owner Stops it first. Outside approved
// epics the owner's writes are not checked.
func TestOwnerWritesKeepRunningLanesUnblocked(t *testing.T) {
	f := newFixture(t)
	f.acceptCmd("go test ./...")
	s := f.ownerShape("Run")
	f.approveRun(s.epic.ID, testRun)
	f.lane(s.b1.ID, "run")
	f.must(f.s.StartPlanning(f.ctx, owner, s.epic.ID, "planner"))
	f.create(Agent("planner", ""), s.a.ID, KindSubtask, "Run a2")
	wantStatus(t, f.card(s.a.ID), StatusDone)

	writes := f.ownerWrites(s)
	writes["approve a proposal under a done blocker"] = func() error {
		_, err := f.s.Approve(f.ctx, owner, s.epic.ID, testRun, f.items(f.shownUnder(s.epic.ID)...))
		return err
	}
	before := f.revision()
	for name, write := range writes {
		err := write()
		wantRefusal(t, err, CodeInProgress, s.b1.ref())
		if !strings.Contains(err.Error(), "Stop") {
			t.Errorf("%s: %v, want it to say to Stop the subtask", name, err)
		}
	}
	if f.revision() != before {
		t.Fatalf("refused writes moved the revision from %d to %d", before, f.revision())
	}
	// Once it is stopped, the owner's write goes through.
	_, err := f.s.ReleaseHold(f.ctx, owner, s.b1.ID, ReleaseOwner, "")
	f.must(err)
	f.must(f.s.Link(f.ctx, owner, s.d.ID, s.b.ID))

	plain := f.ownerShape("Plain")
	f.launch(plain.b1.ID, "manual")
	for name, write := range f.ownerWrites(plain) {
		if err := write(); err != nil {
			t.Errorf("%s outside approved epics: %v", name, err)
		}
	}
}

// Migration v6 adds the lane columns to holds and the base branch and
// acceptance limit to the Project settings, and it is one-way.
func TestMigrateV5ToV6(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	all := migrations
	t.Cleanup(func() { migrations = all })
	migrations = all[:5]
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// The store's queries read v6's columns, so the v5 board is written by hand.
	now := stamp(time.Now())
	for _, q := range []string{
		`INSERT INTO cards (id, seq, project_id, kind, parent_id, title, status, held_by, created_by, created_at, updated_at, moved_at)
		VALUES ('e1', 1, '` + proj + `', 'epic', '', 'Before', 'planned', '', 'owner', '` + now + `', '` + now + `', '` + now + `'),
		       ('s1', 2, '` + proj + `', 'subtask', 'e1', 'Leaf', 'doing', 'task-1', 'owner', '` + now + `', '` + now + `', '` + now + `')`,
		`INSERT INTO holds (id, card_id, task_id, attempt, started_at) VALUES ('h1', 's1', 'task-1', 1, '` + now + `')`,
		`INSERT INTO project_settings (project_id, accept_cmd) VALUES ('` + proj + `', 'make test')`,
		`INSERT INTO sequences (name, next) VALUES ('card', 3)`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.Close()

	migrations = all
	s, err = Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var version string
	if err := s.db.QueryRow(`SELECT v FROM meta WHERE k = 'schema_version'`).Scan(&version); err != nil || version != strconv.Itoa(len(migrations)) {
		t.Fatalf("schema_version = %q, %v", version, err)
	}
	d, err := s.Detail(ctx, "s1")
	if err != nil || len(d.Holds) != 1 || d.Holds[0].Lane != (Lane{}) || d.Holds[0].WaitedOn == nil || len(d.Holds[0].WaitedOn) != 0 || d.Card.Lane != nil {
		t.Fatalf("a hold from v5 = %+v, card lane %+v, %v", d.Holds, d.Card.Lane, err)
	}
	ps, err := s.ProjectSettings(ctx, proj)
	if err != nil || ps != (ProjectSettings{ProjectID: proj, AcceptCmd: "make test", AcceptParallel: 1}) {
		t.Fatalf("settings from v5 = %+v, %v", ps, err)
	}
	if ps, err := s.ProjectSettings(ctx, "p2"); err != nil || ps.AcceptParallel != 1 {
		t.Fatalf("settings of a Project with none = %+v, %v", ps, err)
	}
	for _, q := range []string{
		`UPDATE project_settings SET accept_parallel = 0`,
		`UPDATE project_settings SET accept_parallel = 5`,
	} {
		if _, err := s.db.Exec(q); err == nil {
			t.Fatalf("v6 took %s", q)
		}
	}
	if err := s.SetProjectAcceptParallel(ctx, owner, proj, 3); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProjectBaseRef(ctx, owner, proj, " main "); err != nil {
		t.Fatal(err)
	}
	wantCode(t, s.SetProjectAcceptParallel(ctx, owner, proj, 5), CodeInvalid)
	wantCode(t, s.SetProjectAcceptParallel(ctx, Agent("t", ""), proj, 2), CodeForbidden)
	wantCode(t, s.SetProjectBaseRef(ctx, owner, proj, "a\nb"), CodeInvalid)
	if ps, err := s.ProjectSettings(ctx, proj); err != nil || ps.AcceptParallel != 3 || ps.BaseRef != "main" || ps.AcceptCmd != "make test" {
		t.Fatalf("settings = %+v, %v", ps, err)
	}
	_ = s.Close()

	migrations = all[:5]
	if s, err := Open(path, Options{}); err == nil {
		_ = s.Close()
		t.Fatal("a v5 binary opened a v6 board")
	}
}

// Accepting an agent's split request under an approved epic makes its
// parts proposals, to approve from the epic; outside approved epics the
// parts take the subtask's confirmation, as before.
func TestAcceptedSplitUnderApprovedEpicMakesProposals(t *testing.T) {
	f := newFixture(t)
	f.acceptCmd("go test ./...")
	epic, story, one, _ := f.tree()
	f.approveRun(epic.ID, testRun)
	f.lane(one.ID, "run")
	res, err := f.s.Split(f.ctx, Agent("run", ""), one.ID, []SplitChild{{Title: "Lex"}, {Title: "Scan"}})
	f.must(err)
	if res.Request == nil {
		t.Fatalf("split = %+v", res)
	}
	_, err = f.s.ReleaseHold(f.ctx, owner, one.ID, ReleaseOwner, "")
	f.must(err)
	_, err = f.s.Accept(f.ctx, owner, res.Request.ID, "")
	f.must(err)
	var parts []Card
	for _, c := range f.children(story.ID) {
		if c.Title == "Lex" || c.Title == "Scan" {
			parts = append(parts, c)
		}
	}
	if len(parts) != 2 {
		t.Fatalf("parts = %+v", parts)
	}
	for _, c := range parts {
		if c.Confirmed() || c.Status != StatusPlanned {
			t.Fatalf("part %+v", c)
		}
		wantRefusal(t, f.s.CanStart(f.ctx, c.ID), CodeNotReady, c.ref())
	}
	wantStatus(t, f.card(one.ID), StatusCancelled)

	_, plainStory, plain, _ := f.tree2()
	f.launch(plain.ID, "manual")
	res, err = f.s.Split(f.ctx, Agent("manual", ""), plain.ID, []SplitChild{{Title: "Half"}})
	f.must(err)
	_, err = f.s.ReleaseHold(f.ctx, owner, plain.ID, ReleaseOwner, "")
	f.must(err)
	_, err = f.s.Accept(f.ctx, owner, res.Request.ID, "")
	f.must(err)
	for _, c := range f.children(plainStory.ID) {
		if c.Title == "Half" && !c.Confirmed() {
			t.Fatalf("a part outside approved epics = %+v", c)
		}
	}
}
