package web

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func toolEdit(id, tool, path string, status agentapi.ToolStatus, at time.Time) agentapi.Item {
	input, _ := json.Marshal(map[string]string{"path": path})
	return agentapi.Item{ID: id, Kind: agentapi.ItemTool, Time: at, Tool: &agentapi.ToolCall{Name: tool, Status: status, Input: string(input)}}
}

func scopePaths(t *testing.T, m *Manager, id, scope string) (Changes, []string) {
	t.Helper()
	out, err := m.Changes(t.Context(), id, scope)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range out.Files {
		paths = append(paths, f.Path)
	}
	return out, paths
}

func cachedDiff(m *Manager, id string) *DiffStat {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[id].diff
}

// The task scope lists the files the Task's edit tools touched, the turn
// scope those touched since its latest ordinary prompt, both compared with
// HEAD; reads, failed calls and files changed by someone else stay out.
func TestTaskAndTurnScopes(t *testing.T) {
	old := diffDelay
	diffDelay = time.Millisecond
	t.Cleanup(func() { diffDelay = old })
	repo := gitRepoFixture(t)
	m, prov, _ := newTestManager(t)
	sum, err := m.Create(CreateRequest{Provider: prov.Name(), ProjectID: addProject(t, m, repo)})
	if err != nil {
		t.Fatal(err)
	}
	conv := prov.Last()
	t0 := time.Now().Add(-time.Hour)
	conv.EmitItem(agentapi.Item{ID: "u1", Kind: agentapi.ItemUser, Text: "first", Time: t0})
	conv.EmitItem(toolEdit("e1", "edit", filepath.Join(repo, "tracked.txt"), agentapi.ToolCompleted, t0.Add(time.Second)))
	conv.EmitTurn(agentapi.TurnCompleted, "")
	conv.EmitItem(agentapi.Item{ID: "u2", Kind: agentapi.ItemUser, Text: "second", Time: t0.Add(time.Minute)})
	conv.EmitItem(toolEdit("e2", "create", "sub/new file.txt", agentapi.ToolCompleted, t0.Add(time.Minute+time.Second)))
	conv.EmitItem(toolEdit("v1", "view", "unchanged.txt", agentapi.ToolCompleted, t0.Add(time.Minute+2*time.Second)))
	conv.EmitItem(toolEdit("f1", "edit", "unchanged.txt", agentapi.ToolFailed, t0.Add(time.Minute+3*time.Second)))
	// A steer joins the running turn; it does not start a new one.
	conv.EmitItem(agentapi.Item{ID: "s1", Kind: agentapi.ItemUser, Text: "steer", Delivery: agentapi.DeliverySteer, Time: t0.Add(2 * time.Minute)})
	writeRepoFile(t, repo, "other.txt", "by hand\n")

	task, paths := scopePaths(t, m, sum.ID, ScopeTask)
	if want := []string{"tracked.txt", "sub/new file.txt"}; !task.Supported || task.Scope != ScopeTask || !slices.Equal(paths, want) {
		t.Fatalf("task scope = %+v paths %q, want %q", task, paths, want)
	}
	if c := task.Counts; c == nil || *c != (ScopeCounts{Task: 2, Turn: 1, Workspace: 3}) {
		t.Fatalf("counts = %+v", c)
	}
	if task.Reason != "" || task.Files[0].Digest == "" || task.Files[1].Additions != 3 {
		t.Fatalf("task files = %+v reason %q", task.Files, task.Reason)
	}
	if _, paths := scopePaths(t, m, sum.ID, ScopeTurn); !slices.Equal(paths, []string{"sub/new file.txt"}) {
		t.Fatalf("turn scope = %q", paths)
	}
	if all, paths := scopePaths(t, m, sum.ID, ScopeWorkspace); len(paths) != 3 || all.Counts == nil || all.Label != workspaceLabel {
		t.Fatalf("workspace scope = %+v", all)
	}
	if d := cachedDiff(m, sum.ID); d == nil || *d != (DiffStat{Files: 2, Additions: 5, Deletions: 1}) {
		t.Fatalf("summary diff = %+v", d)
	}

	if f, err := m.FileChange(t.Context(), sum.ID, ScopeTask, "tracked.txt"); err != nil || f.Additions != 2 {
		t.Fatalf("task file = %+v, %v", f, err)
	}
	for _, tc := range []struct{ scope, path string }{{ScopeTurn, "tracked.txt"}, {ScopeTask, "other.txt"}, {ScopeTask, "unchanged.txt"}} {
		if _, err := m.FileChange(t.Context(), sum.ID, tc.scope, tc.path); statusOf(err) != http.StatusBadRequest {
			t.Fatalf("%s %s: error %v, want 400", tc.scope, tc.path, err)
		}
	}

	// A later edit is counted in the background and its digest changes.
	before := task.Files[0].Digest
	writeRepoFile(t, repo, "tracked.txt", "one\nthree\nfour\nfive\n")
	conv.EmitItem(toolEdit("e3", "edit", "tracked.txt", agentapi.ToolCompleted, t0.Add(3*time.Minute)))
	deadline := time.Now().Add(5 * time.Second)
	for d := cachedDiff(m, sum.ID); d == nil || d.Additions != 6; d = cachedDiff(m, sum.ID) {
		if time.Now().After(deadline) {
			t.Fatalf("summary diff not recounted: %+v", d)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if again, _ := scopePaths(t, m, sum.ID, ScopeTask); again.Files[0].Digest == before {
		t.Fatalf("digest did not change: %q", before)
	}
}

// A Task read from its record before history loads says its edits may be
// partial, and an unknown scope is refused.
func TestTaskScopePartialAndUnknownScope(t *testing.T) {
	repo := gitRepoFixture(t)
	m, id := taskInDir(t, repo)
	m.mu.Lock()
	m.sessions[id].editsKnown = false
	m.mu.Unlock()
	out, paths := scopePaths(t, m, id, ScopeTask)
	if len(paths) != 0 || out.Reason != partialEdits || out.Files == nil {
		t.Fatalf("partial task scope = %+v", out)
	}
	if cachedDiff(m, id) != nil {
		t.Fatal("a Task without edits has a total")
	}
	if _, err := m.Changes(t.Context(), id, "everything"); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("unknown scope: %v", err)
	}
}

// The record's edits, subagents' included, count once it is read.
func TestNoteHistoryRecordsEdits(t *testing.T) {
	s := newSession("id", "fake", "", "/w", "", time.Now())
	m := &Manager{}
	m.closed = true // no recount in this unit test
	at := time.Now()
	sub := toolEdit("e2", "edit", "b.go", agentapi.ToolCompleted, at)
	sub.AgentID = "agent-1"
	m.noteHistoryLocked(s, agentapi.History{Items: []agentapi.Item{
		{ID: "u", Kind: agentapi.ItemUser, Time: at},
		toolEdit("e1", "apply_patch", "", agentapi.ToolCompleted, at),
		toolEdit("e3", "create", "a.go", agentapi.ToolRunning, at.Add(-time.Second)),
		sub,
	}, Truncated: true}, true)
	// The running create may yet fail: it is not an edit until it completes.
	if s.editsKnown || len(s.edits) != 1 || !s.turnStart.Equal(at) {
		t.Fatalf("edits = %v known %v turn %v", s.edits, s.editsKnown, s.turnStart)
	}
	m.noteHistoryLocked(s, agentapi.History{}, true)
	if !s.editsKnown {
		t.Fatal("a whole record did not make the edits known")
	}
}
