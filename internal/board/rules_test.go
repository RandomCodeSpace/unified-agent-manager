package board

import (
	"testing"
)

func TestDeriveTable(t *testing.T) {
	confirmed := func(s Status) leafFact { return leafFact{status: s, confirmed: true, held: s == StatusDoing} }
	proposed := func(s Status) leafFact { return leafFact{status: s, held: s == StatusDoing} }
	pending := func(l leafFact) leafFact { l.pending = true; return l }
	for _, tc := range []struct {
		name      string
		cancelled bool
		leaves    []leafFact
		want      Status
		progress  Progress
	}{
		{"no subtasks", false, nil, StatusPlanned, Progress{}},
		{"only unconfirmed", false, []leafFact{proposed(StatusPlanned), proposed(StatusTodo)}, StatusPlanned, Progress{Proposed: 2}},
		{"no confirmed is never done", false, []leafFact{proposed(StatusCancelled)}, StatusPlanned, Progress{}},
		{"an unconfirmed hold makes doing", false, []leafFact{proposed(StatusDoing)}, StatusDoing, Progress{Proposed: 1}},
		{"a confirmed hold makes doing", false, []leafFact{confirmed(StatusDoing), confirmed(StatusDone)}, StatusDoing, Progress{Done: 1, Total: 2}},
		{"every confirmed cancelled", false, []leafFact{confirmed(StatusCancelled), confirmed(StatusCancelled), proposed(StatusPlanned)}, StatusCancelled, Progress{Proposed: 1}},
		{"every live confirmed done", false, []leafFact{confirmed(StatusDone), confirmed(StatusCancelled), proposed(StatusPlanned)}, StatusDone, Progress{Done: 1, Total: 1, Proposed: 1}},
		{"a pending request blocks done", false, []leafFact{confirmed(StatusDone), pending(proposed(StatusPlanned))}, StatusDoing, Progress{Done: 1, Total: 1, Proposed: 1}},
		{"some done", false, []leafFact{confirmed(StatusDone), confirmed(StatusTodo)}, StatusDoing, Progress{Done: 1, Total: 2}},
		{"otherwise planned", false, []leafFact{confirmed(StatusTodo), confirmed(StatusPlanned)}, StatusPlanned, Progress{Total: 2}},
		{"cancelled itself", true, []leafFact{confirmed(StatusDoing)}, StatusCancelled, Progress{Total: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, progress := derive(tc.cancelled, tc.leaves)
			if got != tc.want || progress != tc.progress {
				t.Fatalf("derive = %s %+v, want %s %+v", got, progress, tc.want, tc.progress)
			}
		})
	}
}

func TestActorTable(t *testing.T) {
	agentOps := map[op]bool{opCreate: true, opEdit: true, opChange: true, opChecklist: true, opComment: true,
		opLink: true, opClaim: true, opRequest: true, opSplit: true}
	ownerless := map[op]bool{opClaim: true, opRequest: true}
	agent := Agent("task", "sub")
	for o := range actorTable {
		if err := permit(agent, o, ""); (err == nil) != agentOps[o] {
			t.Errorf("agent %s: err = %v, want allowed %v", o, err, agentOps[o])
		}
		if err := permit(owner, o, ""); (err == nil) == ownerless[o] {
			t.Errorf("owner %s: err = %v, want allowed %v", o, err, !ownerless[o])
		}
	}
	for _, a := range []Actor{{}, {Role: RoleAgent}, {Role: "admin", TaskID: "x"}} {
		wantCode(t, permit(a, opComment, ""), CodeForbidden)
	}
	wantCode(t, permit(owner, "nonsense", ""), CodeForbidden)
	for _, tc := range []struct {
		o    op
		from Status
		ok   bool
	}{
		{opLaunch, StatusPlanned, true}, {opLaunch, StatusTodo, true}, {opLaunch, StatusDoing, false},
		{opClaim, StatusDone, false}, {opRelease, StatusDoing, true}, {opRelease, StatusTodo, false},
		{opReady, StatusDone, true}, {opReady, StatusCancelled, false}, {opDone, StatusDone, false},
		{opCancel, StatusCancelled, false}, {opRestore, StatusCancelled, true}, {opRestore, StatusTodo, false},
		{opSplit, StatusDone, false}, {opComment, StatusDone, true},
	} {
		a := owner
		if tc.o == opClaim {
			a = agent
		}
		if err := permit(a, tc.o, tc.from); (err == nil) != tc.ok {
			t.Errorf("%s from %s: err = %v, want allowed %v", tc.o, tc.from, err, tc.ok)
		}
	}
}

// Test plan 10: agents never move a card to done or cancelled, set the
// blocked flag, restore or purge.
func TestAgentLimits(t *testing.T) {
	f := newFixture(t)
	_, _, one, _ := f.tree()
	f.launch(one.ID, "task-1")
	agent := Agent("task-1", "")
	before := f.revision()
	_, err := f.s.SetStatus(f.ctx, agent, one.ID, StatusDone, "done", false)
	wantCode(t, err, CodeForbidden)
	_, err = f.s.SetStatus(f.ctx, agent, one.ID, StatusCancelled, "no", false)
	wantCode(t, err, CodeForbidden)
	blocked := true
	_, err = f.s.Edit(f.ctx, agent, one.ID, Patch{Blocked: &blocked})
	wantCode(t, err, CodeForbidden)
	_, err = f.s.Restore(f.ctx, agent, one.ID, "back")
	wantCode(t, err, CodeForbidden)
	_, err = f.s.Purge(f.ctx, agent, proj)
	wantCode(t, err, CodeForbidden)
	_, err = f.s.Launch(f.ctx, agent, one.ID, "task-2", Baseline{})
	wantCode(t, err, CodeForbidden)
	_, err = f.s.Accept(f.ctx, agent, "r", "")
	wantCode(t, err, CodeForbidden)
	_, err = f.s.Dismiss(f.ctx, agent, one.ID)
	wantCode(t, err, CodeForbidden)
	_, err = f.s.Confirm(f.ctx, agent, one.ID)
	wantCode(t, err, CodeForbidden)
	_, err = f.s.ReleaseHold(f.ctx, agent, one.ID, ReleaseOwner, "")
	wantCode(t, err, CodeForbidden)
	wantCode(t, f.s.Unlink(f.ctx, agent, one.ID, one.ID), CodeForbidden)
	wantCode(t, f.s.StartPlanning(f.ctx, agent, one.ID, "t"), CodeForbidden)
	wantCode(t, f.s.SetProjectAcceptCmd(f.ctx, agent, proj, "make"), CodeForbidden)
	if after := f.revision(); after != before {
		t.Fatalf("refused agent writes moved the revision from %d to %d", before, after)
	}
	if _, err := f.s.FileRequest(f.ctx, owner, one.ID, RequestInput{Kind: RequestDone, Comment: "x"}); CodeOf(err) != CodeForbidden {
		t.Fatalf("owner filed a request: %v", err)
	}
	if _, err := f.s.Claim(f.ctx, owner, one.ID, Baseline{}); CodeOf(err) != CodeForbidden {
		t.Fatalf("owner claimed: %v", err)
	}
}
