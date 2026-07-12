package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
	tea "github.com/charmbracelet/bubbletea"
)

func TestWorkspaceProjectionPreservesCanonicalOrderWhenDisabled(t *testing.T) {
	sessions := groupingFixture(t)
	want := sessionIDs(sessions)

	got := projectSessions(sessions, false)
	if !reflect.DeepEqual(sessionIDs(got), want) {
		t.Fatalf("grouping off changed canonical order: got %v want %v", sessionIDs(got), want)
	}
}

func TestWorkspaceProjectionGroupsWithinLifecycleAndPinPartitions(t *testing.T) {
	sessions := groupingFixture(t)
	got := projectSessions(sessions, true)
	want := []string{"a1", "a3", "a2", "u1", "u2", "c1", "c2"}
	if !reflect.DeepEqual(sessionIDs(got), want) {
		t.Fatalf("grouped projection = %v, want %v", sessionIDs(got), want)
	}
}

func TestWorkspaceKeyIsAbsoluteCleanAndDoesNotResolveSymlinks(t *testing.T) {
	if got := workspaceKey(""); got != unknownWorkspaceKey {
		t.Fatalf("blank cwd key = %q, want %q", got, unknownWorkspaceKey)
	}

	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(root, "link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Fatal(err)
	}
	got := workspaceKey(filepath.Join(linkDir, "..", "link"))
	if got != linkDir {
		t.Fatalf("workspace key resolved or failed to clean symlink path: got %q want %q", got, linkDir)
	}
}

func TestGroupToggleRestoresProjectionAndSelectedIdentity(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = groupingFixture(t)
	m.selected = 2 // a3

	model, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = model.(Model)
	if got := sessionIDs(m.sessions); !reflect.DeepEqual(got, []string{"a1", "a3", "a2", "u1", "u2", "c1", "c2"}) {
		t.Fatalf("toggle on projection = %v", got)
	}
	if selected, ok := m.selectedSession(); !ok || selected.ID != "a3" {
		t.Fatalf("toggle on changed selection: %+v, ok=%v", selected, ok)
	}

	model, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = model.(Model)
	if got := sessionIDs(m.sessions); !reflect.DeepEqual(got, []string{"a1", "a2", "a3", "u1", "u2", "c1", "c2"}) {
		t.Fatalf("toggle off did not restore canonical projection: %v", got)
	}
	if selected, ok := m.selectedSession(); !ok || selected.ID != "a3" {
		t.Fatalf("toggle off changed selection: %+v, ok=%v", selected, ok)
	}
}

func TestGroupedViewShowsBoundedWorkspaceHeadingAndLiveSharingWarning(t *testing.T) {
	root := filepath.Join(t.TempDir(), "alpha-workspace")
	m := NewWithDeps(nil, nil)
	m.groupByDir = true
	m.sessions = []adapter.Session{
		{ID: "a", AgentType: "fake", DisplayName: "a", Cwd: root, ProcAlive: adapter.Alive},
		{ID: "b", AgentType: "fake", DisplayName: "b", Cwd: filepath.Join(root, "."), ProcAlive: adapter.Alive},
		{ID: "dead", AgentType: "fake", DisplayName: "dead", Cwd: root, ProcAlive: adapter.Exited},
	}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 44, Height: 20})
	view := m.View()
	assertViewGeometry(t, view, 44, 20)
	for _, want := range []string{"alpha-workspace", "3", "⚠ 2 sessions share this workspace"} {
		if !strings.Contains(view, want) {
			t.Fatalf("grouped view missing %q:\n%s", want, view)
		}
	}
}

func TestGroupedViewCountsLiveWorkspaceSharingAcrossPinPartitions(t *testing.T) {
	root := filepath.Join(t.TempDir(), "shared-across-pins")
	m := NewWithDeps(nil, nil)
	m.groupByDir = true
	m.sessions = projectSessions([]adapter.Session{
		{ID: "pinned", AgentType: "fake", Cwd: root, ProcAlive: adapter.Alive, Pinned: true},
		{ID: "plain", AgentType: "fake", Cwd: root, ProcAlive: adapter.Alive},
	}, true)
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 30})
	view := m.View()
	if got := strings.Count(view, "⚠ 2 sessions share this workspace"); got != 1 {
		t.Fatalf("sharing count must span pin partitions and render once, got %d:\n%s", got, view)
	}
}

func TestGroupedViewHandlesBlankWorkspaceWithoutSharingWarning(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.groupByDir = true
	m.sessions = []adapter.Session{
		{ID: "blank", AgentType: "fake", DisplayName: "blank", ProcAlive: adapter.Alive},
		{ID: "also-blank", AgentType: "fake", DisplayName: "also-blank", ProcAlive: adapter.Alive},
		{ID: "closed", AgentType: "fake", DisplayName: "closed", ProcAlive: adapter.Exited, Closed: true},
	}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 30})
	view := m.View()
	assertViewGeometry(t, view, 80, 30)
	if !strings.Contains(view, "Unknown workspace") {
		t.Fatalf("blank cwd needs a safe heading:\n%s", view)
	}
	if strings.Contains(view, "sessions share this workspace") {
		t.Fatalf("closed/non-live sessions must not produce a sharing warning:\n%s", view)
	}
}

func TestGroupedReorderRejectsWorkspaceBoundaryWithoutSideEffects(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.groupByDir = true
	m.sessions = []adapter.Session{
		{ID: "a", AgentType: "fake", Cwd: "/tmp/a", ProcAlive: adapter.Alive},
		{ID: "b", AgentType: "fake", Cwd: "/tmp/b", ProcAlive: adapter.Alive},
	}
	m.selected = 0
	m.peekOpen = true
	m.peekTargetAgent = "fake"
	m.peekTargetID = "a"
	m.peekText = "keep this tail"

	if cmd := m.moveSession(1); cmd != nil {
		t.Fatal("cross-workspace move must not schedule persistence")
	}
	if m.selected != 0 || m.reorderPending || m.reorderSeq != 0 {
		t.Fatalf("rejected move changed selection or persistence state: selected=%d pending=%v seq=%d", m.selected, m.reorderPending, m.reorderSeq)
	}
	if m.peekTargetID != "a" || m.peekText != "keep this tail" {
		t.Fatalf("rejected move changed peek state: target=%q text=%q", m.peekTargetID, m.peekText)
	}
	if got := sessionIDs(m.sessions); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("rejected move changed rows: %v", got)
	}
	if !strings.Contains(m.message, "workspace") {
		t.Fatalf("workspace boundary feedback = %q", m.message)
	}
}

func TestGroupedReorderAllowsNormalizedSameWorkspace(t *testing.T) {
	root := t.TempDir()
	m := NewWithDeps(nil, nil)
	m.groupByDir = true
	m.sessions = []adapter.Session{
		{ID: "a", AgentType: "fake", Cwd: root, ProcAlive: adapter.Alive},
		{ID: "b", AgentType: "fake", Cwd: filepath.Join(root, "sub", ".."), ProcAlive: adapter.Alive},
	}
	m.selected = 0
	if cmd := m.moveSession(1); cmd == nil {
		t.Fatal("same normalized workspace move must schedule persistence")
	}
	if got := sessionIDs(m.sessions); !reflect.DeepEqual(got, []string{"b", "a"}) || m.selected != 1 || !m.reorderPending {
		t.Fatalf("allowed grouped reorder did not apply: ids=%v selected=%d pending=%v", got, m.selected, m.reorderPending)
	}
}

func TestGroupedReorderSwapsOnlyMovedSortIndicesWhenGroupingTurnsOff(t *testing.T) {
	root := t.TempDir()
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{
		{ID: "a1", AgentType: "fake", Cwd: filepath.Join(root, "a"), ProcAlive: adapter.Alive, SortIndex: 0},
		{ID: "b", AgentType: "fake", Cwd: filepath.Join(root, "b"), ProcAlive: adapter.Alive, SortIndex: 1},
		{ID: "a2", AgentType: "fake", Cwd: filepath.Join(root, "a"), ProcAlive: adapter.Alive, SortIndex: 2},
	}
	m.setGroupByDir(true)
	if got := sessionIDs(m.sessions); !reflect.DeepEqual(got, []string{"a1", "a2", "b"}) {
		t.Fatalf("initial grouped projection = %v", got)
	}
	m.selected = 0
	if cmd := m.moveSession(1); cmd == nil {
		t.Fatal("within-workspace move must schedule persistence")
	}
	if m.sessions[0].SortIndex != 0 || m.sessions[1].SortIndex != 2 || m.sessions[2].SortIndex != 1 {
		t.Fatalf("move must swap only the pair's existing sort indices: %+v", m.sessions)
	}

	m.setGroupByDir(false)
	if got := sessionIDs(m.sessions); !reflect.DeepEqual(got, []string{"a2", "b", "a1"}) {
		t.Fatalf("ungrouped order lost unrelated canonical interleaving: %v", got)
	}
}

func TestGroupToggleCapturesPendingReorderBeforeProjection(t *testing.T) {
	root := t.TempDir()
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{
		{ID: "a1", AgentType: "fake", Cwd: filepath.Join(root, "a"), ProcAlive: adapter.Alive, SortIndex: 0},
		{ID: "b", AgentType: "fake", Cwd: filepath.Join(root, "b"), ProcAlive: adapter.Alive, SortIndex: 1},
		{ID: "a2", AgentType: "fake", Cwd: filepath.Join(root, "a"), ProcAlive: adapter.Alive, SortIndex: 2},
	}
	m.setGroupByDir(true)
	m.selected = 0
	_ = m.moveSession(1)
	if !m.reorderPending {
		t.Fatal("precondition: grouped reorder should be pending")
	}

	model, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = model.(Model)
	if cmd == nil {
		t.Fatal("toggle should retain commands for the captured reorder and view setting")
	}
	if m.reorderPending {
		t.Fatal("toggle must capture and clear the pending reorder before reprojecting")
	}
	if got := sessionIDs(m.sessions); !reflect.DeepEqual(got, []string{"a2", "b", "a1"}) {
		t.Fatalf("toggle lost pending grouped move: %v", got)
	}
}

func TestGroupedReorderPersistsPairIndicesWithoutReindexingOtherWorkspaces(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	m := NewWithDeps(st, nil)
	m.sessions = []adapter.Session{
		{ID: "a1", AgentType: "fake", Cwd: filepath.Join(dir, "a"), ProcAlive: adapter.Alive, SortIndex: 0},
		{ID: "b", AgentType: "fake", Cwd: filepath.Join(dir, "b"), ProcAlive: adapter.Alive, SortIndex: 1},
		{ID: "a2", AgentType: "fake", Cwd: filepath.Join(dir, "a"), ProcAlive: adapter.Alive, SortIndex: 2},
	}
	if err := m.service.UpdateSortIndices(m.sessions); err != nil {
		t.Fatal(err)
	}
	m.setGroupByDir(true)
	m.selected = 0
	_ = m.moveSession(1)
	drainCmd(m.flushReorder())

	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, id := range []string{"a1", "a2", "b"} {
		got[id] = cfg.Sessions[store.Key("fake", id)].SortIndex
	}
	want := map[string]int{"a1": 2, "a2": 0, "b": 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("persisted grouped indices = %v, want %v", got, want)
	}
}

func TestGroupedNarrowWindowKeepsSelectedWorkspaceHeading(t *testing.T) {
	root := filepath.Join(t.TempDir(), "selected-workspace")
	m := NewWithDeps(nil, nil)
	m.groupByDir = true
	for i := 0; i < 10; i++ {
		m.sessions = append(m.sessions, adapter.Session{
			ID:          string(rune('a' + i)),
			AgentType:   "fake",
			DisplayName: "row-" + string(rune('a'+i)),
			Cwd:         root,
			ProcAlive:   adapter.Alive,
			SortIndex:   i,
		})
	}
	m.selected = 6
	lines := m.groupedSessionListLines(44, 4, LayoutCompact)
	view := strings.Join(lines, "\n")
	if !strings.Contains(view, "selected-workspace") || !strings.Contains(view, "row-g") {
		t.Fatalf("scrolled grouped window orphaned selected row from its heading:\n%s", view)
	}
}

func TestWorkspaceHeadingIncludesPathAndCountWhenTheyFit(t *testing.T) {
	line := workspaceHeadingLine("/tmp/ws", 2, 44)
	for _, want := range []string{"ws", "/tmp/ws", "2"} {
		if !strings.Contains(line, want) {
			t.Fatalf("workspace heading missing %q: %q", want, line)
		}
	}
}

func groupingFixture(t *testing.T) []adapter.Session {
	t.Helper()
	root := t.TempDir()
	sessions := []adapter.Session{
		{ID: "a1", AgentType: "fake", Cwd: filepath.Join(root, "one"), ProcAlive: adapter.Alive, Pinned: true, SortIndex: 0},
		{ID: "a2", AgentType: "fake", Cwd: filepath.Join(root, "two"), ProcAlive: adapter.Alive, Pinned: true, SortIndex: 1},
		{ID: "a3", AgentType: "fake", Cwd: filepath.Join(root, "one", "."), ProcAlive: adapter.Alive, Pinned: true, SortIndex: 2},
		{ID: "u1", AgentType: "fake", Cwd: filepath.Join(root, "two"), ProcAlive: adapter.Alive, SortIndex: 3},
		{ID: "u2", AgentType: "fake", Cwd: filepath.Join(root, "one"), ProcAlive: adapter.Alive, SortIndex: 4},
		{ID: "c1", AgentType: "fake", Cwd: filepath.Join(root, "two"), ProcAlive: adapter.Exited, Closed: true, SortIndex: 5},
		{ID: "c2", AgentType: "fake", Cwd: filepath.Join(root, "one"), ProcAlive: adapter.Exited, Closed: true, SortIndex: 6},
	}
	SortSessions(sessions)
	return sessions
}

func sessionIDs(sessions []adapter.Session) []string {
	ids := make([]string, len(sessions))
	for i := range sessions {
		ids[i] = sessions[i].ID
	}
	return ids
}
