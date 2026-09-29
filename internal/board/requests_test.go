package board

import (
	"encoding/json"
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
		Evidence: evidence, Flags: []string{FlagOverlap}, ProposedAcceptCmd: "go test ./..."})
	f.must(err)
	if req.Status != RequestPending || req.Comment != "all green" || req.AgentID != "sub" || string(req.Evidence) != string(evidence) ||
		!slices.Equal(req.Flags, []string{FlagOverlap}) || req.BaseRevision == 0 || !strings.Contains(string(req.Payload), "go test") {
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
	epic, story, one, _ := f.tree()
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
	// Refusals: under a story, nothing to split into, repeated titles, containers.
	_, err = f.s.Split(f.ctx, owner, one.ID, []SplitChild{{Title: "x"}})
	wantCode(t, err, CodeInvalid)
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
