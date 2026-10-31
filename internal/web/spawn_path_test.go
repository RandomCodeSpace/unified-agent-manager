package web

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// Edit tools may write only in the Thread's folder, its Copilot session
// folder and the temporary directory; a link is judged by its target.
func TestEditScopeKeepsEditToolsInTheThreadFolder(t *testing.T) {
	t.Setenv("COPILOT_HOME", filepath.Join(t.TempDir(), ".copilot"))
	m, prov, _ := newTestManager(t)
	sum, _ := createSession(t, m, prov)
	s := budgetSession(t, m, sum.ID)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(s.workdir, "escape")); err != nil {
		t.Fatal(err)
	}
	// Every t.TempDir is in the temporary directory, which stays writable:
	// point it elsewhere so the folders above are outside it.
	t.Setenv("TMPDIR", t.TempDir())
	m.mu.Lock()
	state := m.sessionStateDirLocked(s)
	m.mu.Unlock()
	for name, tc := range map[string]struct {
		use  agentapi.ToolUse
		deny bool
	}{
		"relative edit":      {agentapi.ToolUse{Tool: "edit", Args: map[string]any{"path": "src/a.go"}}, false},
		"absolute inside":    {agentapi.ToolUse{Tool: "create", Args: map[string]any{"path": filepath.Join(s.workdir, "new.go")}}, false},
		"temporary file":     {agentapi.ToolUse{Tool: "create", Args: map[string]any{"path": filepath.Join(os.TempDir(), "scratch-uam-test.txt")}}, false},
		"session folder":     {agentapi.ToolUse{Tool: "create", Args: map[string]any{"path": filepath.Join(state, "files", "notes.md")}}, false},
		"shell is not known": {agentapi.ToolUse{Tool: "bash", Args: map[string]any{"command": "echo x > /etc/x"}}, false},
		"absolute outside":   {agentapi.ToolUse{Tool: "edit", Args: map[string]any{"path": filepath.Join(outside, "b.go")}}, true},
		"climbs out":         {agentapi.ToolUse{Tool: "edit", Args: map[string]any{"path": "../elsewhere/c.go"}}, true},
		"patch outside":      {agentapi.ToolUse{Tool: "apply_patch", Args: map[string]any{"patch": "*** Begin Patch\n*** Update File: a.go\n*** Add File: " + filepath.Join(outside, "d.go") + "\n*** End Patch"}}, true},
		"link out":           {agentapi.ToolUse{Tool: "create", Args: map[string]any{"path": "escape/e.go"}}, true},
	} {
		t.Run(name, func(t *testing.T) {
			tc.use.Workdir = s.workdir
			v := m.preToolUse(t.Context(), s, tc.use)
			if got := v.Deny != ""; got != tc.deny {
				t.Fatalf("deny = %q", v.Deny)
			}
			if tc.deny && (!strings.Contains(v.Deny, "uam scope:") || !strings.Contains(v.Deny, createTaskToolName)) {
				t.Fatalf("deny = %q", v.Deny)
			}
		})
	}
	m.mu.Lock()
	s.spawnedBy = "parent"
	m.mu.Unlock()
	if v := m.preToolUse(t.Context(), s, agentapi.ToolUse{Tool: "edit", Workdir: s.workdir, Args: map[string]any{"path": filepath.Join(outside, "b.go")}}); !strings.Contains(v.Deny, "Tell the owner") {
		t.Fatalf("spawned thread's deny = %q", v.Deny)
	}
}

// A Thread folder reached through a link (a linked home or worktrees folder)
// keeps its edits allowed: the edit path is resolved, so the folder must be.
func TestEditScopeAllowsAThreadFolderBehindALink(t *testing.T) {
	t.Setenv("COPILOT_HOME", filepath.Join(t.TempDir(), ".copilot"))
	m, prov, _ := newTestManager(t)
	sum, _ := createSession(t, m, prov)
	s := budgetSession(t, m, sum.ID)
	link := filepath.Join(t.TempDir(), "worktree")
	if err := os.Symlink(s.workdir, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", t.TempDir())
	m.mu.Lock()
	s.workdir = link
	m.mu.Unlock()
	for _, path := range []string{"src/a.go", filepath.Join(link, "b.go")} {
		if v := m.preToolUse(t.Context(), s, agentapi.ToolUse{Tool: "edit", Workdir: link, Args: map[string]any{"path": path}}); v.Deny != "" {
			t.Fatalf("%s: deny = %q", path, v.Deny)
		}
	}
}

// A path in a Project, or below its folder, starts the Thread there with no
// question; the new Thread is in yolo only when its creator is.
func TestCreateTaskPathInAProject(t *testing.T) {
	m, prov, _ := newTestManager(t)
	caller, conv := createSession(t, m, prov)
	dir := resolvedPath(t.TempDir())
	target, err := m.AddProject(dir, "Target")
	if err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "pkg")
	if err := os.Mkdir(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	got, child := spawnOK(t, m, prov, conv, "call-1", fmt.Sprintf(`{"path":%q,"prompt":"fix it"}`, sub))
	if got.ProjectID != target.ID || got.Mode != "safe" || len(child.Sends()) != 1 || child.Sends()[0] != "Work in the folder "+sub+".\n\nfix it" {
		t.Fatalf("created = %+v, sends %q", got, child.Sends())
	}
	if _, err := m.SetMode(caller.ID, "yolo"); err != nil {
		t.Fatal(err)
	}
	got, child = spawnOK(t, m, prov, conv, "call-2", fmt.Sprintf(`{"path":%q,"prompt":"again"}`, dir))
	if got.ProjectID != target.ID || got.Mode != "yolo" || child.Sends()[0] != "again" {
		t.Fatalf("created by a yolo thread = %+v, sends %q", got, child.Sends())
	}
	if d := detail(t, m, caller.ID); len(d.Interactions) != 0 {
		t.Fatalf("a path in a project asked: %+v", d.Interactions)
	}
}

// A folder in no Project needs the owner's answer before it becomes one:
// Add project makes the folder and starts the Thread there, Cancel and Stop
// add nothing, and a folder outside the home folder is refused unasked.
func TestCreateTaskPathAsksBeforeAddingAProject(t *testing.T) {
	home := resolvedPath(t.TempDir())
	t.Setenv("HOME", home)
	m, prov, _ := newTestManager(t)
	caller, conv := createSession(t, m, prov)
	ask := func(callID, path string) (<-chan agentapi.HostToolResult, agentapi.Interaction) {
		t.Helper()
		done := make(chan agentapi.HostToolResult, 1)
		go func() {
			args := json.RawMessage(fmt.Sprintf(`{"path":%q,"prompt":"set it up"}`, path))
			res, err := conv.CallTool(context.Background(), agentapi.HostToolCall{Name: createTaskToolName, CallID: callID, Arguments: args})
			if err != nil {
				res = agentapi.HostToolResult{Text: err.Error(), Failed: true}
			}
			done <- res
		}()
		var ix agentapi.Interaction
		waitUntil(t, "the owner's question", func() bool {
			d := detail(t, m, caller.ID)
			for _, i := range d.Interactions {
				if i.ToolCallID == callID && i.State == agentapi.InteractionPending {
					ix = i
					return d.State == StateAwaitingAnswer
				}
			}
			return false
		})
		return done, ix
	}
	answer := func(ix agentapi.Interaction, choice string) {
		t.Helper()
		if _, err := m.Answer(caller.ID, ix.ID, agentapi.Answer{Answers: [][]string{{choice}}}); err != nil {
			t.Fatal(err)
		}
	}

	declined := filepath.Join(home, "declined")
	done, ix := ask("call-no", "~/declined")
	if !strings.Contains(ix.Questions[0].Text, declined) || !strings.Contains(ix.Questions[0].Text, "does not exist yet") {
		t.Fatalf("question = %+v", ix.Questions)
	}
	answer(ix, confirmCancel)
	if res := <-done; !res.Failed || !strings.Contains(res.Text, "declined") {
		t.Fatalf("declined = %+v", res)
	}
	if _, err := os.Stat(declined); !os.IsNotExist(err) {
		t.Fatalf("a declined folder was made: %v", err)
	}

	done, _ = ask("call-stop", "~/stopped")
	if _, err := m.Cancel(caller.ID); err != nil {
		t.Fatal(err)
	}
	if res := <-done; !res.Failed || !strings.Contains(res.Text, "withdrawn") {
		t.Fatalf("stopped = %+v", res)
	}

	added := filepath.Join(home, "code", "app")
	done, ix = ask("call-yes", "~/code/app")
	answer(ix, "Add project")
	res := <-done
	if res.Failed || !strings.Contains(res.Text, "Started thread") {
		t.Fatalf("added = %+v", res)
	}
	if info, err := os.Stat(added); err != nil || !info.IsDir() {
		t.Fatalf("folder = %v, %v", info, err)
	}
	m.mu.Lock()
	id, dir := "", ""
	for _, p := range m.projects {
		if p.Dir == added {
			id, dir = p.ID, p.Dir
		}
	}
	m.mu.Unlock()
	if id == "" || dir != added {
		t.Fatalf("no project for %s", added)
	}
	waitSpawns(t, m)
	var child SessionSummary
	for _, s := range m.List() {
		if s.ProjectID == id {
			child = s
		}
	}
	if child.ID == "" || child.SpawnedBy != caller.ID || child.Mode != "safe" {
		t.Fatalf("thread in the new project = %+v", child)
	}

	spawnRefused(t, m, prov, conv, fmt.Sprintf(`{"path":%q,"prompt":"p"}`, t.TempDir()), "only inside the home folder")
}
