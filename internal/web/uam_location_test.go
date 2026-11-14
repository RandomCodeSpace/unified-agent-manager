package web

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func assertUAMLocation(t *testing.T, row map[string]any, workdir, branch string, worktree WorktreeInfo) {
	t.Helper()
	if row["workdir"] != workdir {
		t.Errorf("workdir = %v, want %q", row["workdir"], workdir)
	}
	if branch == "" {
		if _, ok := row["branch"]; ok {
			t.Errorf("unavailable branch should be omitted: %+v", row)
		}
	} else if row["branch"] != branch {
		t.Errorf("branch = %v, want %q", row["branch"], branch)
	}
	if worktree.Path == "" {
		if _, ok := row["worktree"]; ok {
			t.Errorf("project folder should omit managed worktree metadata: %+v", row)
		}
		return
	}
	got, ok := row["worktree"].(map[string]any)
	if !ok || got["path"] != worktree.Path || got["branch"] != worktree.Branch {
		t.Errorf("worktree = %v, want path %q and branch %q", row["worktree"], worktree.Path, worktree.Branch)
	}
}

func TestUAMReadLocations(t *testing.T) {
	repo := worktreeRepo(t)
	m, prov, _ := newTestManager(t)
	m.worktreeRoot = t.TempDir()
	// A Project below the repository root distinguishes the actual working
	// directory from the managed worktree's root.
	project := addProject(t, m, filepath.Join(repo, "docs"))
	folder, err := m.Create(CreateRequest{Provider: prov.Name(), ProjectID: project})
	if err != nil {
		t.Fatal(err)
	}
	folderConv := prov.Last()
	isolated, err := m.Create(CreateRequest{Provider: prov.Name(), ProjectID: project, Place: &PlaceRequest{Mode: placeWorktree, Base: "feature"}})
	if err != nil {
		t.Fatal(err)
	}
	isolatedConv := prov.Last()
	m.refreshBranches(t.Context(), true, project)
	branch := gitOutput(t, isolated.Worktree.Path, "symbolic-ref", "--short", "HEAD")
	for _, tc := range []struct {
		name     string
		id       string
		conv     *agenttest.Conversation
		workdir  string
		branch   string
		worktree WorktreeInfo
	}{
		{"project folder", folder.ID, folderConv, filepath.Join(repo, "docs"), "main", WorktreeInfo{}},
		{"managed worktree", isolated.ID, isolatedConv, filepath.Join(isolated.Worktree.Path, "docs"), branch, WorktreeInfo{Path: isolated.Worktree.Path, Branch: branch}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("sense", func(t *testing.T) {
				var row map[string]any
				uamReadJSON(t, tc.conv, `{"op":"sense"}`, &row)
				assertUAMLocation(t, row, tc.workdir, tc.branch, tc.worktree)
			})
			t.Run("task", func(t *testing.T) {
				var row map[string]any
				uamReadJSON(t, folderConv, fmt.Sprintf(`{"op":"task","args":{"id":%q}}`, tc.id), &row)
				assertUAMLocation(t, row, tc.workdir, tc.branch, tc.worktree)
			})
			t.Run("tasks", func(t *testing.T) {
				var page uamTestPage
				uamReadJSON(t, folderConv, `{"op":"tasks"}`, &page)
				if len(page.Rows) != 2 {
					t.Fatalf("tasks: %+v", page)
				}
				for _, row := range page.Rows {
					if row["id"] == tc.id {
						assertUAMLocation(t, row, tc.workdir, tc.branch, tc.worktree)
						return
					}
				}
				t.Fatalf("thread %s missing from tasks", tc.id)
			})
		})
	}
}

func TestUAMReadLocationOutsideGit(t *testing.T) {
	m, prov, _ := newTestManager(t)
	caller, conv := createSession(t, m, prov)
	for _, args := range []string{`{"op":"sense"}`, fmt.Sprintf(`{"op":"task","args":{"id":%q}}`, caller.ID)} {
		var row map[string]any
		uamReadJSON(t, conv, args, &row)
		assertUAMLocation(t, row, caller.Workdir, "", WorktreeInfo{})
	}
	var page uamTestPage
	uamReadJSON(t, conv, `{"op":"tasks"}`, &page)
	if len(page.Rows) != 1 {
		t.Fatalf("tasks: %+v", page)
	}
	assertUAMLocation(t, page.Rows[0], caller.Workdir, "", WorktreeInfo{})
}

func TestUAMReadLocationProjectScope(t *testing.T) {
	repo := worktreeRepo(t)
	m, prov, _ := newTestManager(t)
	m.worktreeRoot = t.TempDir()
	caller, conv := createSession(t, m, prov)
	other, err := m.Create(CreateRequest{Provider: prov.Name(), ProjectID: addProject(t, m, repo), Place: &PlaceRequest{Mode: placeWorktree, Base: "feature"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []store.Mode{store.ModeSafe, store.ModeAssisted, store.ModeYolo} {
		t.Run(string(mode), func(t *testing.T) {
			setSession(m, caller.ID, func(s *webSession) { s.mode = mode })
			var page uamTestPage
			uamReadJSON(t, conv, `{"op":"tasks"}`, &page)
			if len(page.Rows) != 1 || page.Rows[0]["id"] != caller.ID {
				t.Fatalf("default tasks escaped the caller's Project: %+v", page)
			}
			for _, op := range []string{"task", "tasks"} {
				t.Run(op, func(t *testing.T) {
					args := fmt.Sprintf(`{"op":"task","args":{"id":%q}}`, other.ID)
					if op == "tasks" {
						args = fmt.Sprintf(`{"op":"tasks","args":{"project":%q}}`, other.ProjectID)
					}
					if mode != store.ModeYolo {
						res := uamReadCall(t, conv, args)
						if !res.Failed || !strings.Contains(res.Text, "requires Yolo") || strings.Contains(res.Text, other.Workdir) || strings.Contains(res.Text, other.Branch) {
							t.Fatalf("cross-project location read: %+v", res)
						}
						return
					}
					var row map[string]any
					if op == "task" {
						uamReadJSON(t, conv, args, &row)
					} else {
						var page uamTestPage
						uamReadJSON(t, conv, args, &page)
						if len(page.Rows) != 1 {
							t.Fatalf("Yolo tasks: %+v", page)
						}
						row = page.Rows[0]
					}
					assertUAMLocation(t, row, other.Workdir, other.Branch, other.Worktree)
				})
			}
		})
	}
}
