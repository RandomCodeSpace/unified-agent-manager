package web

import (
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func TestBackgroundShellSnapshotIsIndependentOfForegroundAndBecomesUnknownOnExit(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	sub, _, err := m.Subscribe(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	tasks := agentapi.BackgroundTasks{Known: true, Tasks: []agentapi.BackgroundTask{{ID: "server", Command: "python3 -m http.server 8000", Description: "Game server", Status: "running"}}}
	conv.Emit(agentapi.Event{Kind: agentapi.EventBackgroundTasks, BackgroundTasks: &tasks})
	conv.EmitTurn(agentapi.TurnCompleted, "")
	d := detail(t, m, sum.ID)
	if d.State != StateCompleted || d.BackgroundTasks == nil || !d.BackgroundTasks.Known || len(d.BackgroundTasks.Tasks) != 1 || d.BackgroundTasks.Tasks[0].Status != "running" || len(d.Subagents) != 0 {
		t.Fatalf("completed foreground with background shell = %+v", d)
	}
	var snapshot agentapi.BackgroundTasks
	decodeField(t, frameOf(t, sub, "background_tasks"), "background_tasks", &snapshot)
	if !snapshot.Known || len(snapshot.Tasks) != 1 || snapshot.Tasks[0].ID != "server" {
		t.Fatalf("background SSE snapshot = %+v", snapshot)
	}
	// Provider and API callers cannot mutate the retained snapshot.
	tasks.Tasks[0].Command = "changed by provider"
	d.BackgroundTasks.Tasks[0].Command = "changed by reader"
	if got := detail(t, m, sum.ID).BackgroundTasks.Tasks[0].Command; got != "python3 -m http.server 8000" {
		t.Fatalf("snapshot aliased: %s", got)
	}
	conv.EmitTurn(agentapi.TurnWorking, "")
	if got := detail(t, m, sum.ID); got.State != StateWorking || got.BackgroundTasks.Tasks[0].Status != "running" {
		t.Fatalf("foreground continuation = %+v", got)
	}
	conv.Emit(agentapi.Event{Kind: agentapi.EventExit, Error: "disconnected"})
	if got := detail(t, m, sum.ID).BackgroundTasks; got == nil || got.Known || len(got.Tasks) != 1 {
		t.Fatalf("disconnected background state = %+v", got)
	}
}

func TestBackgroundShellSnapshotBecomesUnknownOnClose(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	conv.Emit(agentapi.Event{Kind: agentapi.EventBackgroundTasks, BackgroundTasks: &agentapi.BackgroundTasks{Known: true, Tasks: []agentapi.BackgroundTask{{ID: "server", Status: "running"}}}})
	conv.EmitTurn(agentapi.TurnCompleted, "")
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	if got := detail(t, m, sum.ID).BackgroundTasks; got == nil || got.Known {
		t.Fatalf("closed conversation claims known shell liveness: %+v", got)
	}
}
