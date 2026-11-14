package web

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

func createOverlapThread(t *testing.T, m *Manager, prov *agenttest.Provider, req CreateRequest) (SessionSummary, *agenttest.Conversation) {
	t.Helper()
	req.Provider = prov.Name()
	sum, err := m.Create(req)
	if err != nil {
		t.Fatal(err)
	}
	return sum, prov.Last()
}

func TestWorktreeOverlapWhoTouched(t *testing.T) {
	for _, folder := range []string{".", "docs"} {
		t.Run(folder, func(t *testing.T) {
			repo := worktreeRepo(t)
			m, prov, _ := newTestManager(t)
			m.worktreeRoot = t.TempDir()
			project := addProject(t, m, filepath.Join(repo, folder))
			caller, conv := createOverlapThread(t, m, prov, CreateRequest{ProjectID: project, Name: "Caller", Place: &PlaceRequest{Mode: placeWorktree}})
			sibling, source := createOverlapThread(t, m, prov, CreateRequest{ProjectID: project, Name: "Earlier edit", Place: &PlaceRequest{Mode: placeWorktree}})
			path := "docs/guide.md"
			if folder == "docs" {
				path = "guide.md"
			}
			at := time.Now().Add(-time.Hour).UTC()
			conv.EmitItem(agentapi.Item{ID: "own", Kind: agentapi.ItemTool, Time: at, Tool: &agentapi.ToolCall{Name: "edit", Status: agentapi.ToolCompleted, Input: fmt.Sprintf(`{"path":%q}`, path)}})
			for i, p := range []string{path, filepath.Join(sibling.Workdir, path)} {
				source.EmitItem(agentapi.Item{ID: fmt.Sprintf("edit-%d", i), Kind: agentapi.ItemTool, Time: at.Add(time.Duration(i) * time.Minute), Tool: &agentapi.ToolCall{Name: "edit", Status: agentapi.ToolCompleted, Input: fmt.Sprintf(`{"path":%q}`, p)}})
			}
			// Historical attribution includes settled threads and keeps the
			// latest edit once when relative and absolute inputs name one file.
			setSession(m, sibling.ID, func(s *webSession) { s.stage = StageSettled; s.editsKnown = false })
			for _, query := range []string{"sub/../" + path, filepath.Join(caller.Workdir, path)} {
				var page uamTestPage
				uamReadJSON(t, conv, fmt.Sprintf(`{"op":"who_touched","args":{"path":%q}}`, query), &page)
				if len(page.Rows) != 2 {
					t.Errorf("who_touched(%q) = %+v; want caller and sibling from separate worktrees", query, page.Rows)
				}
				for _, row := range page.Rows {
					if row["id"] == sibling.ID && (row["last_edit"] != at.Add(time.Minute).Format(time.RFC3339Nano) || row["stage"] != StageSettled) {
						t.Errorf("sibling attribution = %+v", row)
					}
				}
				if source, _ := page.Note["source"].(string); page.Note["partial"] != true || !strings.HasPrefix(source, "edit tools, then session tracking") {
					t.Errorf("attribution caveat = %+v", page.Note)
				}
			}
			// Session tracking lists files in the sibling's own worktree.
			setSession(m, sibling.ID, func(s *webSession) { s.nativePaths = []string{filepath.Join(sibling.Workdir, "tracked.md")} })
			var tracked uamTestPage
			uamReadJSON(t, conv, `{"op":"who_touched","args":{"path":"tracked.md"}}`, &tracked)
			if len(tracked.Rows) != 1 || tracked.Rows[0]["id"] != sibling.ID || tracked.Rows[0]["basis"] != "session tracking" {
				t.Errorf("session-tracked sibling worktree path = %+v", tracked.Rows)
			}
			source.EmitItem(agentapi.Item{ID: "shell", Kind: agentapi.ItemTool, Time: at, Tool: &agentapi.ToolCall{Name: "bash", Status: agentapi.ToolCompleted, Input: `{"command":"echo shell > shell-only.md"}`}})
			var page uamTestPage
			uamReadJSON(t, conv, `{"op":"who_touched","args":{"path":"shell-only.md"}}`, &page)
			if len(page.Rows) != 0 {
				t.Errorf("shell write was attributed: %+v", page.Rows)
			}
		})
	}
}

func TestWorktreeOverlapAdvisoryFilters(t *testing.T) {
	repo := worktreeRepo(t)
	m, prov, _ := newTestManager(t)
	m.worktreeRoot = t.TempDir()
	project := addProject(t, m, filepath.Join(repo, "docs"))
	caller, conv := createOverlapThread(t, m, prov, CreateRequest{ProjectID: project, Name: "Caller", Place: &PlaceRequest{Mode: placeWorktree}})
	sibling, _ := createOverlapThread(t, m, prov, CreateRequest{ProjectID: project, Name: "Guide edit", Place: &PlaceRequest{Mode: placeWorktree}})
	now := time.Now().UTC()
	m.mu.Lock()
	m.now = func() time.Time { return now }
	m.mu.Unlock()
	for _, tc := range []struct {
		name  string
		age   time.Duration
		state string
		stage string
		child bool
		warn  bool
	}{
		{"recent running", time.Minute, StateWorking, StageActive, false, true},
		{"exactly thirty minutes", 30 * time.Minute, StateWorking, StageActive, false, true},
		{"too old", 30*time.Minute + time.Second, StateWorking, StageActive, false, false},
		{"future timestamp", -time.Second, StateWorking, StageActive, false, false},
		{"idle", time.Minute, StateIdle, StageActive, false, false},
		{"settled", time.Minute, StateWorking, StageSettled, false, false},
		{"subagent still running", time.Minute, StateIdle, StageActive, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setSession(m, sibling.ID, func(s *webSession) {
				s.base, s.stage = tc.state, tc.stage
				s.edits = map[string]time.Time{filepath.Join(sibling.Workdir, "guide.md"): now.Add(-tc.age)}
				s.subagents = nil
				if tc.child {
					s.subagents = []*agentapi.Subagent{{ID: "helper", Status: agentapi.SubagentRunning}}
				}
			})
			setSession(m, caller.ID, func(s *webSession) { s.trailPaths = nil })
			use := agentapi.ToolUse{Tool: "edit", Workdir: filepath.Join(caller.Workdir, "sub"), Args: map[string]any{"path": "../guide.md"}}
			got := conv.Request().Hooks.Pre(t.Context(), use)
			if got.Deny != "" || got.Args != nil {
				t.Errorf("overlap must remain advisory: %+v", got)
			}
			if warn := strings.Contains(got.Context, `Thread "Guide edit" (`+sibling.ID+`) edited guide.md`); warn != tc.warn {
				t.Errorf("warning = %q, want warning %v", got.Context, tc.warn)
			}
			if !tc.warn && got.Context != "" {
				t.Errorf("filtered edit produced context: %q", got.Context)
			}
			if got := conv.Request().Hooks.Pre(t.Context(), use); got.Context != "" {
				t.Errorf("repeated advisory in the same turn: %q", got.Context)
			}
		})
	}
}

func TestWorktreeOverlapScope(t *testing.T) {
	repo, unrelated := worktreeRepo(t), worktreeRepo(t)
	m, prov, _ := newTestManager(t)
	m.worktreeRoot = t.TempDir()
	project := addProject(t, m, repo)
	caller, conv := createOverlapThread(t, m, prov, CreateRequest{ProjectID: project, Name: "Caller"})
	sibling, _ := createOverlapThread(t, m, prov, CreateRequest{ProjectID: project, Name: "Sibling", Place: &PlaceRequest{Mode: placeWorktree}})
	foreign, _ := createOverlapThread(t, m, prov, CreateRequest{ProjectID: addProject(t, m, sibling.Workdir), Name: "Different project"})
	other, _ := createOverlapThread(t, m, prov, CreateRequest{ProjectID: addProject(t, m, unrelated), Name: "Unrelated repo"})
	// Project membership alone cannot establish repository identity, even
	// for a restored session whose folder belongs to an unrelated repository.
	setSession(m, other.ID, func(s *webSession) { s.projectID = project })
	at := time.Now().Add(-time.Minute)
	for _, sum := range []SessionSummary{sibling, foreign, other} {
		setSession(m, sum.ID, func(s *webSession) {
			s.base = StateWorking
			s.edits = map[string]time.Time{"README.md": at, "docs/guide.md": at}
			if sum.ID == sibling.ID {
				s.edits["../outside.md"] = at
			}
		})
	}
	// Equal escaping paths resolve to different files outside these two
	// checkout roots and must never become a repository-relative match.
	if editPath(caller.Workdir, "../outside.md") == editPath(sibling.Workdir, "../outside.md") {
		t.Fatal("fixture outside paths must be distinct")
	}
	for _, tc := range []struct {
		path string
		want string
	}{
		{"README.md", sibling.ID},
		{"guide.md", ""}, // Same basename as docs/guide.md is not the same file.
		{"../outside.md", ""},
		{filepath.Join(caller.Workdir, "..", "outside.md"), ""},
	} {
		t.Run(tc.path, func(t *testing.T) {
			var page uamTestPage
			uamReadJSON(t, conv, fmt.Sprintf(`{"op":"who_touched","args":{"path":%q}}`, tc.path), &page)
			if tc.want == "" && len(page.Rows) != 0 || tc.want != "" && (len(page.Rows) != 1 || page.Rows[0]["id"] != tc.want) {
				t.Errorf("who_touched(%q) = %+v; want only %q", tc.path, page.Rows, tc.want)
			}
			setSession(m, caller.ID, func(s *webSession) { s.trailPaths = nil })
			got := conv.Request().Hooks.Pre(t.Context(), agentapi.ToolUse{Tool: "edit", Workdir: caller.Workdir, Args: map[string]any{"path": tc.path}})
			if tc.want == "" && got.Context != "" || tc.want != "" && (!strings.Contains(got.Context, `Thread "Sibling"`) || strings.Count(got.Context, "uam trail:") != 1) {
				t.Errorf("advisory for %q = %q; want only %q", tc.path, got.Context, tc.want)
			}
			if strings.Contains(got.Context, "Different project") || strings.Contains(got.Context, "Unrelated repo") {
				t.Errorf("advisory escaped its repository or Project: %q", got.Context)
			}
		})
	}
}
