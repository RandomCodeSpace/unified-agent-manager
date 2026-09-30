package board

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// Test plan 8, plus the finishing guard: claim_done and Accept refuse when
// the subtask isn't doing or isn't held by the requesting Task.
func TestDoneRequest(t *testing.T) {
	f := newFixture(t)
	epic, story, one, two := f.tree()
	f.launch(one.ID, "task-1")
	agent := Agent("task-1", "sub")
	_, err := f.s.Checklist(f.ctx, owner, one.ID, ChecklistEdit{Add: []string{"write tests"}})
	f.must(err)
	for _, check := range []func() error{
		func() error { return errOf(f.s.CheckFinishable(f.ctx, agent, one.ID)) },
		func() error {
			return errOf(f.s.FileRequest(f.ctx, agent, one.ID, RequestInput{Kind: RequestDone, Comment: "done"}))
		},
	} {
		err := check()
		wantCode(t, err, CodeGuardOpenItems)
		if e := err.(*Error); !slices.Equal(e.Refs, []string{"write tests"}) {
			t.Fatalf("refs = %v", e.Refs)
		}
	}
	_, err = f.s.Checklist(f.ctx, agent, one.ID, ChecklistEdit{Tick: []int{0}})
	f.must(err)
	_, err = f.s.Edit(f.ctx, owner, one.ID, Patch{Blocked: ptr(true)})
	f.must(err)
	_, err = f.s.FileRequest(f.ctx, agent, one.ID, RequestInput{Kind: RequestDone, Comment: "done"})
	wantCode(t, err, CodeGuardBlocked)
	_, err = f.s.Edit(f.ctx, owner, one.ID, Patch{Blocked: ptr(false)})
	f.must(err)
	f.must(f.s.Link(f.ctx, owner, two.ID, one.ID))
	_, err = f.s.CheckFinishable(f.ctx, agent, one.ID)
	wantCode(t, err, CodeGuardBlockers)
	if e := err.(*Error); !slices.Equal(e.Refs, []string{"#4"}) {
		t.Fatalf("refs = %v", e.Refs)
	}
	// Test plan 16: a cancelled blocker doesn't block.
	_, err = f.s.SetStatus(f.ctx, owner, two.ID, StatusCancelled, "not needed", false)
	f.must(err)
	fin, err := f.s.CheckFinishable(f.ctx, agent, one.ID)
	if err != nil || fin.AcceptCmd != "" {
		t.Fatalf("finishable = %+v, %v", fin, err)
	}
	// Another Task, or a subtask that isn't doing, is refused.
	f.must(f.s.StartPlanning(f.ctx, owner, story.ID, "planner"))
	_, err = f.s.FileRequest(f.ctx, Agent("planner", ""), one.ID, RequestInput{Kind: RequestDone, Comment: "mine"})
	wantCode(t, err, CodeNotHeld)
	three := f.create(owner, story.ID, KindSubtask, "Three")
	_, err = f.s.CheckFinishable(f.ctx, Agent("planner", ""), three.ID)
	wantCode(t, err, CodeNotHeld)
	_, err = f.s.CheckFinishable(f.ctx, agent, story.ID)
	wantCode(t, err, CodeInvalid)
	for _, in := range []RequestInput{
		{Kind: RequestDone},
		{Kind: RequestSplit, Comment: "x"},
		{Kind: RequestDone, Comment: "x", Flags: []string{"bogus"}},
		{Kind: RequestDone, Comment: "x", Evidence: json.RawMessage(`{`)},
		{Kind: RequestDone, Comment: "x", Blocker: two.ID},
		{Kind: RequestCancel, Comment: "x", Flags: []string{FlagOverlap}},
		{Kind: RequestDone, Comment: "x", ProposedAcceptCmd: strings.Repeat("x", maxTextBytes+1)},
	} {
		_, err := f.s.FileRequest(f.ctx, agent, one.ID, in)
		wantCode(t, err, CodeInvalid)
	}
	evidence := json.RawMessage(`{"diff":{"added":3}}`)
	req, err := f.s.FileRequest(f.ctx, agent, one.ID, RequestInput{Kind: RequestDone, Comment: " all green ",
		Evidence: evidence, Flags: []string{FlagOverlap, FlagBaselineMissing}, ProposedAcceptCmd: "go test ./..."})
	f.must(err)
	if req.Status != RequestPending || req.Comment != "all green" || req.AgentID != "sub" || string(req.Evidence) != string(evidence) ||
		!slices.Equal(req.Flags, []string{FlagOverlap, FlagBaselineMissing}) || req.BaseRevision == 0 || !strings.Contains(string(req.Payload), "go test") {
		t.Fatalf("request = %+v", req)
	}
	if c := f.card(one.ID); c.PendingRequests != 1 {
		t.Fatalf("pending = %d", c.PendingRequests)
	}
	snap, err := f.s.Board(f.ctx, proj)
	f.must(err)
	if len(snap.Requests) != 1 || snap.Requests[0].ID != req.ID {
		t.Fatalf("inbox = %+v", snap.Requests)
	}
	// Accept refuses a request whose Task doesn't hold the subtask.
	f.raw(`UPDATE requests SET task_id = 'task-x' WHERE id = ?`, req.ID)
	_, err = f.s.Accept(f.ctx, owner, req.ID, "")
	wantCode(t, err, CodeNotHeld)
	f.raw(`UPDATE requests SET task_id = 'task-1' WHERE id = ?`, req.ID)
	got, err := f.s.Accept(f.ctx, Owner("head-5"), req.ID, " nice ")
	f.must(err)
	c := f.card(one.ID)
	d := f.detail(one.ID)
	last := d.Comments[len(d.Comments)-1]
	if got.Status != RequestAccepted || got.DecisionComment != "nice" || got.DecidedAt == nil || c.Status != StatusDone ||
		c.HeldBy != "" || c.PinnedSHA != "head-5" || d.Holds[0].EndReason != ReleaseAccepted ||
		last.Body != "all green" || last.Author != "task:task-1" || last.AgentID != "sub" || !last.Close || last.Automatic {
		t.Fatalf("accepted: %+v, card %+v, comment %+v", got, c, last)
	}
	_, err = f.s.Accept(f.ctx, owner, req.ID, "")
	wantCode(t, err, CodeInvalid)
	_, err = f.s.Accept(f.ctx, owner, "missing", "")
	wantCode(t, err, CodeNotFound)
	_, err = f.s.Request(f.ctx, "missing")
	wantCode(t, err, CodeNotFound)
	_ = epic
}

// ADR 0005 decision 5: a done request whose acceptance command passed is
// accepted as it is filed, through the owner's Accept path, and recorded as
// uam's decision with an automatic comment and its evidence.
func TestDoneRequestAcceptedWhenItsCommandPassed(t *testing.T) {
	f := newFixture(t)
	epic, story, one, two := f.tree()
	f.must(f.s.SetProjectAcceptCmd(f.ctx, owner, proj, "make check"))
	f.launch(one.ID, "task-1")
	agent := Agent("task-1", "sub")
	evidence := json.RawMessage(`{"accept":{"cmd":"make check","exit":0}}`)
	done := func(ref string, a Actor, passed string) Request {
		t.Helper()
		r, err := f.s.FileRequest(f.ctx, a, ref, RequestInput{Kind: RequestDone, Comment: "finished " + ref, Evidence: evidence, PassedCmd: passed})
		f.must(err)
		return r
	}
	// A command the subtask no longer resolves to accepts nothing.
	stale := done(one.ID, agent, "make old")
	if stale.Status != RequestPending || f.card(one.ID).Status != StatusDoing {
		t.Fatalf("filed with another command: %+v", stale)
	}
	got := done(one.ID, agent, "make check")
	c, d := f.card(one.ID), f.detail(one.ID)
	if got.Status != RequestAccepted || got.DecidedBy != AuthorUAM || got.DecidedAt == nil || got.DecisionComment != "" ||
		string(got.Evidence) != string(evidence) || got.AgentID != "sub" {
		t.Fatalf("accepted request = %+v", got)
	}
	if c.Status != StatusDone || c.HeldBy != "" || c.PendingRequests != 0 || d.Holds[0].EndReason != ReleaseAccepted {
		t.Fatalf("accepted subtask = %+v, holds %+v", c, d.Holds)
	}
	if r, err := f.s.Request(f.ctx, stale.ID); err != nil || r.Status != RequestWithdrawn || r.DecidedBy != "" {
		t.Fatalf("the replaced request = %+v, %v", r, err)
	}
	close, auto := d.Comments[len(d.Comments)-2], d.Comments[len(d.Comments)-1]
	if close.Author != "task:task-1" || close.Body != "finished "+one.ID || !close.Close || close.Automatic ||
		auto.Author != AuthorUAM || auto.Body != AutoAcceptComment || !auto.Automatic || auto.Close {
		t.Fatalf("comments = %+v", d.Comments)
	}
	wantStatus(t, f.card(story.ID), StatusDoing)
	// The last subtask rolls the story and the epic up.
	f.launch(two.ID, "task-2")
	done(two.ID, Agent("task-2", ""), "make check")
	wantStatus(t, f.card(story.ID), StatusDone)
	wantStatus(t, f.card(epic.ID), StatusDone)
	if !hasComment(f.comments(story.ID), "uam: Closed with:") {
		t.Fatalf("story comments = %v", f.comments(story.ID))
	}

	// A proposal and its proposed story are confirmed with it, and the owner
	// can still tell its acceptance from their own.
	f.must(f.s.StartPlanning(f.ctx, owner, epic.ID, "planner"))
	planner := Agent("planner", "")
	proposedStory := f.create(planner, epic.ID, KindStory, "Proposed story")
	proposed := f.create(planner, proposedStory.ID, KindSubtask, "Proposed")
	three := f.create(owner, epic.ID, KindSubtask, "Three")
	_, err := f.s.Edit(f.ctx, owner, three.ID, Patch{AcceptCmd: &sql.NullString{Valid: true}})
	f.must(err)
	f.launch(three.ID, "task-3")
	worker := Agent("task-3", "")
	pending := done(three.ID, worker, "")
	_, err = f.s.Claim(f.ctx, worker, proposed.ID, Baseline{Head: "h"})
	f.must(err)
	done(proposed.ID, worker, "make check")
	if c := f.card(proposed.ID); c.Status != StatusDone || !c.Confirmed() || c.PinnedSHA != "" || !f.card(proposedStory.ID).Confirmed() {
		t.Fatalf("accepted proposal = %+v, story %+v", c, f.card(proposedStory.ID))
	}
	byOwner, err := f.s.Accept(f.ctx, owner, pending.ID, "")
	f.must(err)
	if byOwner.DecidedBy != AuthorOwner || hasComment(f.comments(three.ID), AutoAcceptComment) {
		t.Fatalf("the owner's acceptance = %+v, comments %v", byOwner, f.comments(three.ID))
	}
}

// A passed done request that would close a container still holding
// proposals, which closing cancels, waits for the owner and names them.
func TestPassedDoneRequestWaitsWhenItWouldCloseProposals(t *testing.T) {
	f := newFixture(t)
	epic, story, one, two := f.tree()
	f.must(f.s.SetProjectAcceptCmd(f.ctx, owner, proj, "make check"))
	passed := RequestInput{Kind: RequestDone, Comment: "done", PassedCmd: "make check"}
	f.launch(one.ID, "task-1")
	worker := Agent("task-1", "")
	followUp := f.create(worker, story.ID, KindSubtask, "Follow-up")
	// #4 is still open, so accepting #3 closes nothing.
	got, err := f.s.File(f.ctx, worker, one.ID, passed)
	f.must(err)
	if got.Request.Status != RequestAccepted || got.Closes != nil {
		t.Fatalf("filing that closes nothing = %+v", got)
	}
	// Accepting the last confirmed subtask would close the story and cancel
	// the proposal, so it waits.
	f.launch(two.ID, "task-2")
	got, err = f.s.File(f.ctx, Agent("task-2", ""), two.ID, passed)
	f.must(err)
	if got.Request.Status != RequestPending || got.Closes == nil || got.Closes.ID != story.ID ||
		len(got.Proposals) != 1 || got.Proposals[0].ID != followUp.ID {
		t.Fatalf("filing that would close proposals = %+v", got)
	}
	wantStatus(t, f.card(two.ID), StatusDoing)
	wantStatus(t, f.card(followUp.ID), StatusPlanned)
	wantStatus(t, f.card(story.ID), StatusDoing)
	// With the proposal dismissed, it is accepted and the story closes.
	_, err = f.s.Dismiss(f.ctx, owner, followUp.ID)
	f.must(err)
	got, err = f.s.File(f.ctx, Agent("task-2", ""), two.ID, passed)
	f.must(err)
	if got.Request.Status != RequestAccepted || got.Closes != nil {
		t.Fatalf("filing with no proposal left = %+v", got)
	}
	wantStatus(t, f.card(story.ID), StatusDone)
	wantStatus(t, f.card(epic.ID), StatusDone)
}

// A blank acceptance command is none: it is stored trimmed, and one stored
// blank before that resolves to none, so nothing passes it.
func TestBlankAcceptCmdIsNone(t *testing.T) {
	f := newFixture(t)
	_, _, one, two := f.tree()
	f.must(f.s.SetProjectAcceptCmd(f.ctx, owner, proj, " \t\n"))
	if ps, err := f.s.ProjectSettings(f.ctx, proj); err != nil || ps.AcceptCmd != "" {
		t.Fatalf("settings = %+v, %v", ps, err)
	}
	f.must(f.s.SetProjectAcceptCmd(f.ctx, owner, proj, "  make check\n"))
	if ps, _ := f.s.ProjectSettings(f.ctx, proj); ps.AcceptCmd != "make check" {
		t.Fatalf("a padded command is stored as %q", ps.AcceptCmd)
	}
	edited, err := f.s.Edit(f.ctx, owner, one.ID, Patch{AcceptCmd: &sql.NullString{String: "  ", Valid: true}})
	f.must(err)
	if c := edited.Card; c.AcceptCmd == nil || *c.AcceptCmd != "" {
		t.Fatalf("a blank card command = %v", c.AcceptCmd)
	}
	// Stored blank before commands were trimmed.
	f.raw(`UPDATE project_settings SET accept_cmd = ' ' WHERE project_id = ?`, proj)
	f.raw(`UPDATE cards SET accept_cmd = ' ' WHERE id = ?`, one.ID)
	f.launch(one.ID, "task-1")
	f.launch(two.ID, "task-2")
	for _, tc := range []struct{ ref, task string }{{one.ID, "task-1"}, {two.ID, "task-2"}} {
		agent := Agent(tc.task, "")
		fin, err := f.s.CheckFinishable(f.ctx, agent, tc.ref)
		if err != nil || fin.AcceptCmd != "" {
			t.Fatalf("finishable %s = %+v, %v", tc.ref, fin, err)
		}
		r, err := f.s.FileRequest(f.ctx, agent, tc.ref, RequestInput{Kind: RequestDone, Comment: "x", PassedCmd: " "})
		if err != nil || r.Status != RequestPending {
			t.Fatalf("filed on %s = %+v, %v", tc.ref, r, err)
		}
	}
}

// Only a done request is accepted automatically, and only with the command
// the subtask resolves to: a proposed command never counts.
func TestOnlyAPassedDoneRequestIsAcceptedAutomatically(t *testing.T) {
	f := newFixture(t)
	_, story, one, two := f.tree()
	f.launch(one.ID, "task-1")
	agent := Agent("task-1", "")
	// No command resolves, so nothing passed.
	r, err := f.s.FileRequest(f.ctx, agent, one.ID, RequestInput{Kind: RequestDone, Comment: "x", ProposedAcceptCmd: "make check", PassedCmd: "make check"})
	f.must(err)
	if r.Status != RequestPending || f.card(one.ID).Status != StatusDoing {
		t.Fatalf("filed without a command: %+v", r)
	}
	f.must(f.s.SetProjectAcceptCmd(f.ctx, owner, proj, "make check"))
	for _, in := range []RequestInput{
		{Kind: RequestCancel, Comment: "x", PassedCmd: "make check"},
		{Kind: RequestBlocked, Comment: "x", PassedCmd: "make check"},
	} {
		_, err := f.s.FileRequest(f.ctx, agent, one.ID, in)
		wantCode(t, err, CodeInvalid)
	}
	for _, kind := range []RequestKind{RequestCancel, RequestBlocked} {
		r, err := f.s.FileRequest(f.ctx, agent, one.ID, RequestInput{Kind: kind, Comment: "x"})
		f.must(err)
		if r.Status != RequestPending {
			t.Fatalf("%s request = %+v", kind, r)
		}
	}
	split, err := f.s.Split(f.ctx, agent, one.ID, []SplitChild{{Title: "Half"}})
	f.must(err)
	if split.Request == nil || split.Request.Status != RequestPending {
		t.Fatalf("split = %+v", split)
	}
	if c := f.card(one.ID); c.Status != StatusDoing || c.PendingRequests != 4 {
		t.Fatalf("subtask = %+v", c)
	}
	_, _ = story, two
}

func TestRejectRequest(t *testing.T) {
	f := newFixture(t)
	_, _, one, _ := f.tree()
	f.launch(one.ID, "task-1")
	agent := Agent("task-1", "")
	req := f.done(one.ID, agent)
	_, err := f.s.Reject(f.ctx, owner, req.ID, " ", true)
	wantCode(t, err, CodeInvalid)
	got, err := f.s.Reject(f.ctx, owner, req.ID, "needs tests", true)
	f.must(err)
	if got.Status != RequestRejected || got.DecisionComment != "needs tests" || f.card(one.ID).HeldBy != "task-1" ||
		len(f.comments(one.ID)) != 0 {
		t.Fatalf("rejected while live: %+v, card %+v", got, f.card(one.ID))
	}
	req = f.done(one.ID, agent)
	_, err = f.s.Reject(f.ctx, owner, req.ID, "stale", false)
	f.must(err)
	c := f.card(one.ID)
	if c.Status != StatusTodo || c.HeldBy != "" || !hasComment(f.comments(one.ID), "owner: stale") ||
		f.detail(one.ID).Holds[0].EndReason != ReleaseRejected {
		t.Fatalf("rejected while not live: %+v", c)
	}
	_, err = f.s.Reject(f.ctx, owner, req.ID, "again", false)
	wantCode(t, err, CodeInvalid)
}

// Test plan 9: any status change on a subtask withdraws its pending
// requests; a newer request of the same kind from the same Task replaces
// the older one.
func TestRequestsWithdrawnOnStatusChange(t *testing.T) {
	f := newFixture(t)
	_, story, one, two := f.tree()
	f.must(f.s.StartPlanning(f.ctx, owner, story.ID, "planner"))
	planner := Agent("planner", "")
	cancel := func(ref string) Request {
		r, err := f.s.FileRequest(f.ctx, planner, ref, RequestInput{Kind: RequestCancel, Comment: "obsolete"})
		f.must(err)
		return r
	}
	status := func(id string) RequestStatus {
		r, err := f.s.Request(f.ctx, id)
		f.must(err)
		return r.Status
	}
	first := cancel(two.ID)
	second := cancel(two.ID)
	if status(first.ID) != RequestWithdrawn || status(second.ID) != RequestPending {
		t.Fatal("a newer request did not replace the older one")
	}
	blocked, err := f.s.FileRequest(f.ctx, planner, two.ID, RequestInput{Kind: RequestBlocked, Comment: "waiting"})
	f.must(err)
	_, err = f.s.SetStatus(f.ctx, owner, two.ID, StatusTodo, "", false)
	f.must(err)
	if status(second.ID) != RequestWithdrawn || status(blocked.ID) != RequestWithdrawn {
		t.Fatal("marking ready kept pending requests")
	}
	third := cancel(two.ID)
	f.launch(two.ID, "task-2")
	if status(third.ID) != RequestWithdrawn {
		t.Fatal("launching kept a pending request")
	}
	f.launch(one.ID, "task-1")
	done := f.done(one.ID, Agent("task-1", ""))
	_, err = f.s.SetStatus(f.ctx, owner, one.ID, StatusDone, "shipped directly", false)
	f.must(err)
	if status(done.ID) != RequestWithdrawn {
		t.Fatal("marking done kept a pending request")
	}
	_, err = f.s.FileRequest(f.ctx, planner, one.ID, RequestInput{Kind: RequestCancel, Comment: "x"})
	wantCode(t, err, CodeInvalid) // done is terminal
}

func TestAcceptCancelAndBlocked(t *testing.T) {
	f := newFixture(t)
	epic, story, one, two := f.tree()
	f.must(f.s.StartPlanning(f.ctx, owner, epic.ID, "planner"))
	planner := Agent("planner", "")
	// Blocked with a blocker links it; without one it sets the flag.
	proposed := f.create(planner, story.ID, KindSubtask, "Proposed")
	_, err := f.s.FileRequest(f.ctx, planner, one.ID, RequestInput{Kind: RequestBlocked, Comment: "x", Blocker: proposed.ID})
	wantCode(t, err, CodeInvalid) // an unconfirmed blocker
	_, err = f.s.FileRequest(f.ctx, planner, one.ID, RequestInput{Kind: RequestBlocked, Comment: "x", Blocker: one.ID})
	wantCode(t, err, CodeInvalid)
	byLink, err := f.s.FileRequest(f.ctx, planner, one.ID, RequestInput{Kind: RequestBlocked, Comment: "needs two", Blocker: "#4"})
	f.must(err)
	_, err = f.s.Accept(f.ctx, owner, byLink.ID, "")
	f.must(err)
	if c := f.card(one.ID); c.Blocked || !slices.Equal(c.BlockedBy, []string{two.ID}) {
		t.Fatalf("blocked by link: %+v", c)
	}
	again, err := f.s.FileRequest(f.ctx, planner, one.ID, RequestInput{Kind: RequestBlocked, Comment: "still", Blocker: two.ID})
	f.must(err)
	_, err = f.s.Accept(f.ctx, owner, again.ID, "")
	f.must(err) // the existing link is fine
	byFlag, err := f.s.FileRequest(f.ctx, planner, two.ID, RequestInput{Kind: RequestBlocked, Comment: "stuck"})
	f.must(err)
	_, err = f.s.Accept(f.ctx, owner, byFlag.ID, "")
	f.must(err)
	if !f.card(two.ID).Blocked {
		t.Fatal("accepting blocked did not set the flag")
	}
	// Cancel on a subtask, then on a container (a cascade).
	leafCancel, err := f.s.FileRequest(f.ctx, planner, two.ID, RequestInput{Kind: RequestCancel, Comment: "superseded"})
	f.must(err)
	_, err = f.s.Accept(f.ctx, owner, leafCancel.ID, "")
	f.must(err)
	c := f.card(two.ID)
	if c.Status != StatusCancelled || c.CascadeID == "" || !hasComment(f.comments(two.ID), "task:planner: superseded") {
		t.Fatalf("cancelled: %+v %v", c, f.comments(two.ID))
	}
	_, err = f.s.FileRequest(f.ctx, planner, story.ID, RequestInput{Kind: RequestDone, Comment: "x"})
	wantCode(t, err, CodeInvalid)
	storyCancel, err := f.s.FileRequest(f.ctx, planner, story.ID, RequestInput{Kind: RequestCancel, Comment: "descoped"})
	f.must(err)
	_, err = f.s.Accept(f.ctx, owner, storyCancel.ID, "")
	f.must(err)
	wantStatus(t, f.card(story.ID), StatusCancelled)
	wantStatus(t, f.card(one.ID), StatusCancelled)
	_, err = f.s.FileRequest(f.ctx, planner, story.ID, RequestInput{Kind: RequestCancel, Comment: "again"})
	wantCode(t, err, CodeInvalid)
}

// Test plan 11: a split never produces a done child, and a split of a
// confirmed or held subtask is exactly one pending request.
func TestSplit(t *testing.T) {
	f := newFixture(t)
	epic, story, _, _ := f.tree()
	f.must(f.s.StartPlanning(f.ctx, owner, epic.ID, "planner"))
	planner := Agent("planner", "")
	u, err := f.s.Create(f.ctx, planner, NewCard{ProjectID: proj, Kind: KindSubtask, ParentID: epic.ID, Title: "Big",
		Checklist: []Check{{Text: "ticked", Done: true}, {Text: "open"}}})
	f.must(err)
	res, err := f.s.Split(f.ctx, planner, u.ID, []SplitChild{{Title: " first ", WinCondition: "works"}})
	f.must(err)
	if res.Request != nil || res.Card.Kind != KindStory || len(res.Card.Checklist) != 0 {
		t.Fatalf("direct split = %+v", res)
	}
	snap, err := f.s.Board(f.ctx, proj)
	f.must(err)
	var kids []Card
	for _, c := range snap.Cards {
		if c.ParentID == u.ID {
			kids = append(kids, c)
		}
	}
	if len(kids) != 3 || kids[0].Title != "first" || kids[0].WinCondition != "works" || kids[1].Title != "ticked" || kids[2].Title != "open" {
		t.Fatalf("children = %+v", kids)
	}
	for _, k := range kids {
		if k.Status == StatusDone || k.Confirmed() || k.CreatedBy != "task:planner" {
			t.Fatalf("child %+v", k)
		}
	}
	if kids[1].PendingRequests != 1 || kids[0].PendingRequests != 0 {
		t.Fatal("the ticked item has no pending done request")
	}
	tick := f.detail(kids[1].ID).Requests[0]
	if tick.Kind != RequestDone || tick.TaskID != "planner" || !strings.Contains(tick.Comment, "ticked on #5") {
		t.Fatalf("tick request = %+v", tick)
	}
	// Refusals: nothing to split into, repeated titles, containers.
	loose := f.create(owner, epic.ID, KindSubtask, "Loose")
	_, err = f.s.Split(f.ctx, owner, loose.ID, nil)
	wantCode(t, err, CodeInvalid)
	_, err = f.s.Split(f.ctx, owner, loose.ID, []SplitChild{{Title: "a"}, {Title: "A "}})
	wantCode(t, err, CodeDuplicate)
	_, err = f.s.Split(f.ctx, owner, story.ID, []SplitChild{{Title: "a"}})
	wantCode(t, err, CodeInvalid)
	_, err = f.s.Split(f.ctx, owner, loose.ID, []SplitChild{{Title: "bad\ntitle"}})
	wantCode(t, err, CodeInvalid)
	// A held, confirmed subtask: one request, then the hold moves on accept.
	big := f.create(owner, epic.ID, KindSubtask, "Held big")
	_, err = f.s.Checklist(f.ctx, owner, big.ID, ChecklistEdit{Add: []string{"x", "y"}, Tick: nil})
	f.must(err)
	f.launch(big.ID, "worker")
	worker := Agent("worker", "")
	_, err = f.s.Checklist(f.ctx, worker, big.ID, ChecklistEdit{Tick: []int{0}})
	f.must(err)
	before, err := f.s.Board(f.ctx, proj)
	f.must(err)
	res, err = f.s.Split(f.ctx, worker, big.ID, []SplitChild{{Title: "part one"}})
	f.must(err)
	after, err := f.s.Board(f.ctx, proj)
	f.must(err)
	if res.Request == nil || res.Request.Kind != RequestSplit || len(after.Cards) != len(before.Cards) ||
		len(after.Requests) != len(before.Requests)+1 || res.Card.Kind != KindSubtask || res.Card.HeldBy != "worker" {
		t.Fatalf("held split = %+v", res)
	}
	_, err = f.s.Accept(f.ctx, Owner("head-3"), res.Request.ID, "")
	f.must(err)
	story2 := f.card(big.ID)
	if story2.Kind != KindStory || story2.HeldBy != "" || story2.Status != StatusDoing {
		t.Fatalf("after accept: %+v", story2)
	}
	d := f.detail(big.ID)
	if d.Holds[0].EndReason != ReleaseSplit {
		t.Fatalf("old hold = %+v", d.Holds)
	}
	snap, err = f.s.Board(f.ctx, proj)
	f.must(err)
	var parts []Card
	for _, c := range snap.Cards {
		if c.ParentID == big.ID {
			parts = append(parts, c)
		}
	}
	if len(parts) != 3 || parts[0].HeldBy != "worker" || parts[1].Status != StatusDone || parts[2].Status != StatusPlanned {
		t.Fatalf("parts = %+v", parts)
	}
	for _, p := range parts {
		if !p.Confirmed() || p.PinnedSHA != "head-3" || p.CreatedBy != "task:worker" {
			t.Fatalf("part %+v", p)
		}
	}
	moved := f.detail(parts[0].ID).Holds
	if len(moved) != 1 || moved[0].Baseline.Head != "base" || moved[0].TaskID != "worker" {
		t.Fatalf("moved hold = %+v", moved)
	}
	// The owner's split applies at once, with confirmed children.
	res, err = f.s.Split(f.ctx, owner, loose.ID, []SplitChild{{Title: "a"}, {Title: "b"}})
	f.must(err)
	if res.Request != nil || res.Card.Kind != KindStory || !res.Card.Confirmed() {
		t.Fatalf("owner split = %+v", res)
	}
	// Accepting a split of a card that changed since is refused.
	other := f.create(owner, epic.ID, KindSubtask, "Other")
	f.launch(other.ID, "worker-2")
	req, err := f.s.Split(f.ctx, Agent("worker-2", ""), other.ID, []SplitChild{{Title: "o1"}})
	f.must(err)
	f.raw(`UPDATE cards SET kind = 'story', status = 'planned', held_by = '' WHERE id = ?`, other.ID)
	f.raw(`UPDATE holds SET ended_at = 'x' WHERE card_id = ?`, other.ID)
	f.raw(`UPDATE requests SET status = 'pending' WHERE id = ?`, req.Request.ID)
	_, err = f.s.Accept(f.ctx, owner, req.Request.ID, "")
	wantCode(t, err, CodeInvalid)
}

// A subtask under a story can't become a story, so a split makes siblings
// right after it and cancels it under its own cascade; restoring it leaves
// the siblings.
func TestSplitIntoSiblings(t *testing.T) {
	f := newFixture(t)
	_, story, _, _ := f.tree()
	f.must(f.s.StartPlanning(f.ctx, owner, story.ID, "planner"))
	planner := Agent("planner", "")
	big, err := f.s.Create(f.ctx, planner, NewCard{ProjectID: proj, Kind: KindSubtask, ParentID: story.ID, Title: "Big",
		Checklist: []Check{{Text: "ticked", Done: true}, {Text: "open"}}})
	f.must(err)
	f.create(owner, story.ID, KindSubtask, "Tail")
	// A new sibling may not repeat a live sibling's title.
	_, err = f.s.Split(f.ctx, planner, big.ID, []SplitChild{{Title: "two"}})
	wantCode(t, err, CodeDuplicate)

	res, err := f.s.Split(f.ctx, planner, big.ID, []SplitChild{{Title: "first", WinCondition: "works"}})
	f.must(err)
	if res.Request != nil || res.Card.Kind != KindSubtask || res.Card.Status != StatusCancelled || res.Card.CascadeID == "" {
		t.Fatalf("direct sibling split = %+v", res)
	}
	kids := f.children(story.ID)
	var titles []string
	for i, k := range kids {
		titles = append(titles, k.Title)
		if k.Rank != i {
			t.Fatalf("%s rank = %d, want %d", k.Title, k.Rank, i)
		}
	}
	if want := []string{"One", "Two", "Big", "first", "ticked", "open", "Tail"}; !slices.Equal(titles, want) {
		t.Fatalf("siblings = %v, want %v", titles, want)
	}
	made := kids[3:6]
	for _, k := range made {
		if k.Kind != KindSubtask || k.Status != StatusPlanned || k.Confirmed() || k.CreatedBy != "task:planner" {
			t.Fatalf("sibling %+v", k)
		}
	}
	if made[0].WinCondition != "works" || made[0].PendingRequests != 0 || made[1].PendingRequests != 1 || made[2].PendingRequests != 0 {
		t.Fatalf("siblings = %+v", made)
	}
	tick := f.detail(made[1].ID).Requests[0]
	if tick.Kind != RequestDone || tick.TaskID != "planner" || !strings.Contains(tick.Comment, "ticked on "+big.ref()) {
		t.Fatalf("tick request = %+v", tick)
	}
	note := "uam: split into " + made[0].ref() + ", " + made[1].ref() + ", " + made[2].ref()
	if !slices.Contains(f.comments(big.ID), note) {
		t.Fatalf("comments = %q, want %q", f.comments(big.ID), note)
	}
	restored, err := f.s.Restore(f.ctx, owner, big.ID, "keep the original too")
	f.must(err)
	if restored.Status != StatusPlanned || !restored.Confirmed() || len(f.children(story.ID)) != 7 {
		t.Fatalf("restored = %+v", restored)
	}
	for _, k := range made {
		if f.card(k.ID).Status != StatusPlanned {
			t.Fatalf("restore touched sibling %+v", f.card(k.ID))
		}
	}

	// The split's own subtask leaves the Task's unconfirmed count on the
	// story, so it may split into exactly the cap.
	f2 := newFixture(t)
	_, s2, _, _ := f2.tree()
	f2.must(f2.s.StartPlanning(f2.ctx, owner, s2.ID, "planner"))
	mine := f2.create(planner, s2.ID, KindSubtask, "Mine")
	var many []SplitChild
	for i := range CapUnconfirmed + 1 {
		many = append(many, SplitChild{Title: fmt.Sprintf("part %d", i)})
	}
	_, err = f2.s.Split(f2.ctx, planner, mine.ID, many)
	wantCode(t, err, CodeLimit)
	_, err = f2.s.Split(f2.ctx, planner, mine.ID, many[:CapUnconfirmed])
	f2.must(err)

	// Under an epic or at the root, a split still makes a story.
	root := f.create(owner, "", KindSubtask, "Root")
	res, err = f.s.Split(f.ctx, owner, root.ID, []SplitChild{{Title: "a"}})
	f.must(err)
	if res.Card.Kind != KindStory || len(f.children(root.ID)) != 1 {
		t.Fatalf("root split = %+v", res)
	}
}

// A held subtask under a story splits by request; accepting it makes the
// siblings, accepts the ticked one, moves the hold and cancels the original.
func TestSplitIntoSiblingsByRequest(t *testing.T) {
	f := newFixture(t)
	_, story, one, _ := f.tree()
	_, err := f.s.Checklist(f.ctx, owner, one.ID, ChecklistEdit{Add: []string{"x", "y"}})
	f.must(err)
	f.launch(one.ID, "worker")
	worker := Agent("worker", "")
	_, err = f.s.Checklist(f.ctx, worker, one.ID, ChecklistEdit{Tick: []int{0}})
	f.must(err)
	res, err := f.s.Split(f.ctx, worker, one.ID, []SplitChild{{Title: "part one"}})
	f.must(err)
	if res.Request == nil || res.Request.Kind != RequestSplit || len(f.children(story.ID)) != 2 {
		t.Fatalf("held split = %+v", res)
	}
	_, err = f.s.Accept(f.ctx, Owner("head-3"), res.Request.ID, "")
	f.must(err)
	orig := f.card(one.ID)
	if orig.Kind != KindSubtask || orig.Status != StatusCancelled || orig.HeldBy != "" || orig.CascadeID == "" {
		t.Fatalf("original after accept = %+v", orig)
	}
	if h := f.detail(one.ID).Holds; len(h) != 1 || h[0].EndReason != ReleaseSplit {
		t.Fatalf("original hold = %+v", h)
	}
	kids := f.children(story.ID)
	var titles []string
	for _, k := range kids {
		titles = append(titles, k.Title)
	}
	if want := []string{"One", "part one", "x", "y", "Two"}; !slices.Equal(titles, want) {
		t.Fatalf("siblings = %v, want %v", titles, want)
	}
	part, x, y := kids[1], kids[2], kids[3]
	if part.HeldBy != "worker" || part.Status != StatusDoing || x.Status != StatusDone || y.Status != StatusPlanned {
		t.Fatalf("siblings = %+v", kids[1:4])
	}
	for _, k := range kids[1:4] {
		if !k.Confirmed() || k.PinnedSHA != "head-3" || k.CreatedBy != "task:worker" {
			t.Fatalf("sibling %+v", k)
		}
	}
	if h := f.detail(part.ID).Holds; len(h) != 1 || h[0].Baseline.Head != "base" || h[0].TaskID != "worker" {
		t.Fatalf("moved hold = %+v", h)
	}
	wantStatus(t, f.card(story.ID), StatusDoing)
	note := "uam: split into " + part.ref() + ", " + x.ref() + ", " + y.ref()
	if !slices.Contains(f.comments(one.ID), note) {
		t.Fatalf("comments = %q", f.comments(one.ID))
	}
	// Restoring the original reopens it as todo and leaves the siblings.
	restored, err := f.s.Restore(f.ctx, owner, one.ID, "keep it")
	f.must(err)
	if restored.Status != StatusTodo || f.card(part.ID).HeldBy != "worker" || f.card(x.ID).Status != StatusDone ||
		len(f.children(story.ID)) != 5 {
		t.Fatalf("restored = %+v", restored)
	}
}

// Editing an acceptance command, a card's own or the Project default its
// card inherits, reports the pending done requests it bears on, so the board
// frame carries their evidence to be shown stale (ADR 0005 §6).
func TestCommandEditReportsPendingDoneRequests(t *testing.T) {
	f := newFixture(t)
	_, story, one, two := f.tree()
	f.launch(one.ID, "task-1")
	f.launch(two.ID, "task-2")
	first := f.done(one.ID, Agent("task-1", ""))
	second := f.done(two.ID, Agent("task-2", ""))
	_, err := f.s.FileRequest(f.ctx, Agent("task-1", ""), story.ID, RequestInput{Kind: RequestCancel, Comment: "not needed"})
	f.must(err)
	last := func() Change {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.changes[len(f.changes)-1]
	}
	_, err = f.s.Edit(f.ctx, owner, one.ID, Patch{AcceptCmd: &sql.NullString{String: "make check", Valid: true}})
	f.must(err)
	if got := last().Requests; !slices.Equal(got, []string{first.ID}) {
		t.Fatalf("card command edit reported requests %v, want %v", got, []string{first.ID})
	}
	// #3 has its own command now, so only #4 inherits the default.
	f.must(f.s.SetProjectAcceptCmd(f.ctx, owner, proj, "go test ./..."))
	if got := last().Requests; !slices.Equal(got, []string{second.ID}) {
		t.Fatalf("default command edit reported requests %v, want %v", got, []string{second.ID})
	}
	_, err = f.s.Edit(f.ctx, owner, two.ID, Patch{Title: ptr("Two, renamed")})
	f.must(err)
	if got := last().Requests; len(got) != 0 {
		t.Fatalf("an edit without a command reported requests %v", got)
	}
}
