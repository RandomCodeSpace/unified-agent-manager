package web

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func TestTrailSiblingAndTurnScope(t *testing.T) {
	now := time.Now()
	caller := &webSession{id: "caller", projectID: "project", workdir: "/repo", turnStart: now}
	sibling := &webSession{id: "sibling", name: "Fix login", projectID: "project", workdir: "/repo", stage: StageActive, base: StateWorking, edits: map[string]time.Time{"auth.go": now.Add(-time.Minute)}}
	m := &Manager{now: func() time.Time { return now }, sessions: map[string]*webSession{caller.id: caller, sibling.id: sibling}}
	use := agentapi.ToolUse{Tool: "edit", Workdir: "/repo/sub", Args: map[string]any{"path": "../auth.go"}}
	want := `uam trail: Task "Fix login" edited auth.go 1 min ago and is still running. Re-read it first.`
	if got := m.preToolUse(t.Context(), caller, use).Context; got != want {
		t.Fatalf("trail=%q", got)
	}
	if got := m.preToolUse(t.Context(), caller, use).Context; got != "" {
		t.Fatalf("repeat=%q", got)
	}
	caller.turnStart = now.Add(time.Second)
	if got := m.preToolUse(t.Context(), caller, use).Context; got != want {
		t.Fatalf("next turn=%q", got)
	}
	sibling.name, sibling.title = "", "Generated title"
	caller.trailPaths = nil
	if got := m.preToolUse(t.Context(), caller, use).Context; !strings.Contains(got, `Task "Generated title"`) {
		t.Fatalf("automatic title=%q", got)
	}
	sibling.name = "Fix login"
	for _, tc := range []struct {
		name   string
		change func()
	}{
		{"other project", func() { sibling.projectID = "other" }},
		{"own task", func() { delete(m.sessions, sibling.id); caller.edits = sibling.edits }},
		{"idle", func() { sibling.base = StateIdle }},
		{"old edit", func() { sibling.edits["auth.go"] = now.Add(-31 * time.Minute) }},
		{"settled", func() { sibling.stage = StageSettled }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sibling.projectID = "project"
			sibling.base = StateWorking
			sibling.stage = StageActive
			sibling.edits["auth.go"] = now.Add(-time.Minute)
			m.sessions[sibling.id] = sibling
			caller.trailPaths = nil
			tc.change()
			if got := m.preToolUse(t.Context(), caller, use).Context; got != "" {
				t.Fatalf("unexpected trail=%q", got)
			}
		})
	}
}

func TestTrailPatchAndSiblingCap(t *testing.T) {
	now := time.Now()
	caller := &webSession{id: "caller", projectID: "project", workdir: "/repo"}
	m := &Manager{now: func() time.Time { return now }, sessions: map[string]*webSession{"caller": caller}}
	for _, id := range []string{"a", "b", "c", "d"} {
		m.sessions[id] = &webSession{id: id, name: id, projectID: "project", workdir: "/repo", stage: StageActive, base: StateWorking, edits: map[string]time.Time{"auth.go": now}}
	}
	use := agentapi.ToolUse{Tool: "apply_patch", Workdir: "/repo", Args: map[string]any{"patch": "*** Begin Patch\n*** Update File: auth.go\n@@\n-a\n+b\n*** Add File: new.go\n+x\n*** Delete File: old.go\n*** End Patch"}}
	paths := trailPaths(use)
	if len(paths) != 2 || paths[0] != filepath.Join("/repo", "auth.go") || paths[1] != "/repo/new.go" {
		t.Fatalf("paths=%v", paths)
	}
	if got := m.preToolUse(t.Context(), caller, use).Context; strings.Count(got, "uam trail:") != 3 || strings.Contains(got, `Task "d"`) {
		t.Fatalf("cap=%q", got)
	}
	use.Tool = "bash"
	if got := trailPaths(use); len(got) != 0 {
		t.Fatalf("unrelated tool paths=%v", got)
	}
}

func TestTrailFromRecordedNativePatch(t *testing.T) {
	now := time.Now()
	source := &webSession{id: "source", name: "Source", projectID: "p", workdir: "/repo", stage: StageActive, base: StateWorking}
	source.noteEdits(agentapi.Item{ID: "patch", Kind: agentapi.ItemTool, Time: now.Add(-time.Second), Tool: &agentapi.ToolCall{Name: "apply_patch", Status: agentapi.ToolCompleted, Input: `"*** Begin Patch\n*** Add File: fixture/trail.txt\n+first-task-edit\n*** End Patch\n"`}})
	caller := &webSession{id: "caller", projectID: "p", workdir: "/repo"}
	m := &Manager{now: func() time.Time { return now }, sessions: map[string]*webSession{source.id: source, caller.id: caller}}
	req := m.withHostToolsLocked(agentapi.OpenRequest{SessionID: caller.id}, caller)
	got := req.Hooks.Pre(t.Context(), agentapi.ToolUse{Tool: "apply_patch", Workdir: "/repo", Args: map[string]any{"patch": "*** Begin Patch\n*** Update File: fixture/trail.txt\n@@\n first-task-edit\n+second-task-edit\n*** End Patch\n"}})
	if !strings.Contains(got.Context, `Task "Source" edited fixture/trail.txt`) {
		t.Fatalf("context=%q, recorded edits=%v", got.Context, source.edits)
	}
}
