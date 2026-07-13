package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
	tea "github.com/charmbracelet/bubbletea"
)

func TestDashboardDesktopShowsOperationalMetadataWithoutDefaultSplitPane(t *testing.T) {
	now := time.Date(2026, time.July, 13, 12, 0, 0, 0, time.UTC)
	m := NewWithDeps(nil, nil)
	m.now = func() time.Time { return now }
	m.sessions = []adapter.Session{
		{
			ID: "full-session-identity", AgentType: "codex", DisplayName: "release-check",
			Prompt: "verify the release pipeline", Cwd: "/work/unified-agent-manager",
			SessionName: "uam-codex-full-session-identity", ProcAlive: adapter.Alive,
			CreatedAt: now.Add(-2 * time.Hour), PR: &adapter.PRRef{Number: 41, Status: adapter.PROpen},
		},
		{
			ID: "stopped-session", AgentType: "claude", DisplayName: "failed-tests",
			Prompt: "repair integration tests", Cwd: "/work/other", ProcAlive: adapter.Exited,
			CreatedAt: now.Add(-3 * 24 * time.Hour), ExitCode: exitCode(7),
		},
	}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 120, Height: 40})

	view := m.View()
	assertViewGeometry(t, view, 120, 40)
	for _, want := range []string{
		"SESSIONS", "2 sessions", "/ filter", "release-check", "codex", "RUNNING", "2h",
		"verify the release pipeline", "/work/unified-agent-manager", "full-session-identity", "PR #41",
		"failed-tests", "claude", "EXIT 7", "3d",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("desktop dashboard missing %q:\n%s", want, view)
		}
	}
	if lineContainsAll(view, "SESSIONS", "SELECTED") {
		t.Fatalf("operations view must use the full list instead of a default selected split pane:\n%s", view)
	}
	if !strings.ContainsAny(view, "╭┌") || !strings.ContainsAny(view, "╯┘") {
		t.Fatalf("dashboard should render a bordered sessions panel:\n%s", view)
	}
}

func TestDashboardCompactUsesOneLineRowsAndTwoLineSelection(t *testing.T) {
	now := time.Date(2026, time.July, 13, 12, 0, 0, 0, time.UTC)
	m := NewWithDeps(nil, nil)
	m.now = func() time.Time { return now }
	m.sessions = []adapter.Session{
		{ID: "one", AgentType: "codex", DisplayName: "selected", Prompt: "fix copy and paste", ProcAlive: adapter.Alive, CreatedAt: now.Add(-8 * time.Minute)},
		{ID: "two", AgentType: "claude", DisplayName: "ordinary", Prompt: "this task stays collapsed", ProcAlive: adapter.Exited, CreatedAt: now.Add(-90 * time.Minute)},
	}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 44, Height: 12})

	view := m.View()
	assertViewGeometry(t, view, 44, 12)
	for _, want := range []string{"╭▸", "╰─", "selected", "codex", "RUNNING", "8m", "fix copy and paste", "ordinary", "claude", "STOPPED", "1h"} {
		if !strings.Contains(view, want) {
			t.Fatalf("compact dashboard missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "this task stays collapsed") {
		t.Fatalf("ordinary compact rows must stay one line:\n%s", view)
	}
	assertBottomContains(t, view, "›")
}

func TestDashboardFilterUsesEmptyPromptSlashAndPreservesPromptSlash(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{
		{ID: "one", AgentType: "codex", DisplayName: "release", Prompt: "ship pipeline", Cwd: "/work/uam", ProcAlive: adapter.Alive},
		{ID: "two", AgentType: "claude", DisplayName: "docs", Prompt: "write guide", Cwd: "/work/docs", ProcAlive: adapter.Exited},
	}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 30})

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = model.(Model)
	if !m.filterActive || m.input != "" {
		t.Fatalf("empty-prompt slash should enter filter without editing command: active=%v input=%q", m.filterActive, m.input)
	}
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("claude")})
	m = model.(Model)
	view := m.View()
	if !strings.Contains(view, "/ claude") || !strings.Contains(view, "docs") || strings.Contains(view, "release") {
		t.Fatalf("live provider filter did not project visible sessions:\n%s", view)
	}
	selected, ok := m.selectedSession()
	if !ok || selected.AgentType != "claude" || selected.ID != "two" {
		t.Fatalf("filter selection did not target matched identity: %+v ok=%v", selected, ok)
	}

	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = model.(Model)
	if m.filterActive || m.filterQuery != "" {
		t.Fatalf("Esc should clear and exit filtering: active=%v query=%q", m.filterActive, m.filterQuery)
	}
	selected, ok = m.selectedSession()
	if !ok || selected.AgentType != "codex" || selected.ID != "one" {
		t.Fatalf("Esc did not restore the pre-filter selection: %+v ok=%v", selected, ok)
	}

	m.input = "open"
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = model.(Model)
	if m.filterActive || m.input != "open/" {
		t.Fatalf("slash in an existing prompt must stay literal: active=%v input=%q", m.filterActive, m.input)
	}
}

func TestDashboardFilterShowsNoMatchesAndMatchesLifecycleWorkspaceAndTask(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{
		{ID: "one", AgentType: "codex", DisplayName: "release", Prompt: "ship pipeline", Cwd: "/work/uam", ProcAlive: adapter.Alive},
		{ID: "two", AgentType: "claude", DisplayName: "docs", Prompt: "write guide", Cwd: "/work/docs", ProcAlive: adapter.Exited},
	}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 30})

	for _, query := range []string{"pipeline", "docs", "stopped"} {
		model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
		m = model.(Model)
		model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(query)})
		m = model.(Model)
		view := m.View()
		if !strings.Contains(view, "docs") && query != "pipeline" {
			t.Fatalf("query %q did not match expected session:\n%s", query, view)
		}
		if query == "pipeline" && !strings.Contains(view, "release") {
			t.Fatalf("task query did not match release session:\n%s", view)
		}
		model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		m = model.(Model)
	}

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = model.(Model)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("definitely absent")})
	m = model.(Model)
	view := m.View()
	if !strings.Contains(view, "No sessions match") || !strings.Contains(view, "0/2 sessions") {
		t.Fatalf("empty filter result needs an explicit state and matched count:\n%s", view)
	}
}

func TestDashboardAgeAndLifecycleLabelsAreEvidenceBased(t *testing.T) {
	now := time.Date(2026, time.July, 13, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		session adapter.Session
		wantAge string
		want    string
	}{
		{"future", adapter.Session{ProcAlive: adapter.Alive, CreatedAt: now.Add(time.Hour)}, "now", "RUNNING"},
		{"seconds", adapter.Session{ProcAlive: adapter.Alive, CreatedAt: now.Add(-45 * time.Second)}, "now", "RUNNING"},
		{"minutes", adapter.Session{ProcAlive: adapter.Alive, CreatedAt: now.Add(-59 * time.Minute)}, "59m", "RUNNING"},
		{"hours", adapter.Session{ProcAlive: adapter.Exited, CreatedAt: now.Add(-47 * time.Hour)}, "47h", "STOPPED"},
		{"days", adapter.Session{ProcAlive: adapter.Exited, CreatedAt: now.Add(-48 * time.Hour), ExitCode: exitCode(9)}, "2d", "EXIT 9"},
		{"signal", adapter.Session{ProcAlive: adapter.Exited, CreatedAt: now.Add(-time.Hour), ExitCode: exitCode(-1)}, "1h", "SIGNAL"},
		{"explicit stop", adapter.Session{ProcAlive: adapter.Exited, CreatedAt: now.Add(-time.Hour), ExitCode: exitCode(-1), Closed: true}, "1h", "STOPPED"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sessionAge(tc.session.CreatedAt, now); got != tc.wantAge {
				t.Fatalf("sessionAge() = %q, want %q", got, tc.wantAge)
			}
			if got := lifecycleBadge(tc.session); got != tc.want {
				t.Fatalf("lifecycleBadge() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDashboardFilterRefreshAndNavigationUseCompositeIdentity(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{
		{ID: "same", AgentType: "codex", DisplayName: "release", Prompt: "ship", ProcAlive: adapter.Alive},
		{ID: "hidden", AgentType: "claude", DisplayName: "docs", Prompt: "write", ProcAlive: adapter.Alive},
		{ID: "same", AgentType: "claude", DisplayName: "release notes", Prompt: "ship", ProcAlive: adapter.Alive},
	}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 30})
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = model.(Model)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("release")})
	m = model.(Model)

	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = model.(Model)
	selected, ok := m.selectedSession()
	if !ok || selected.AgentType != "claude" || selected.ID != "same" {
		t.Fatalf("filtered navigation did not skip hidden row: %+v ok=%v", selected, ok)
	}

	m = m.handleSessionsLoaded(sessionsLoadedMsg{sessions: []adapter.Session{
		{ID: "same", AgentType: "claude", DisplayName: "release notes", Prompt: "ship", ProcAlive: adapter.Alive},
		{ID: "same", AgentType: "codex", DisplayName: "release", Prompt: "ship", ProcAlive: adapter.Alive},
		{ID: "hidden", AgentType: "claude", DisplayName: "docs", Prompt: "write", ProcAlive: adapter.Alive},
	}})
	selected, ok = m.selectedSession()
	if !ok || selected.AgentType != "claude" || selected.ID != "same" {
		t.Fatalf("refresh retargeted duplicate ID across providers: %+v ok=%v", selected, ok)
	}
}

func TestDashboardSlashDoesNotStealReplyAndCanFilterEmptyDashboard(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.peekOpen = true
	m.sessions = []adapter.Session{{ID: "one", ProcAlive: adapter.Alive}}
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = model.(Model)
	if m.filterActive || m.input != "/" {
		t.Fatalf("slash should remain literal in Peek reply input: active=%v input=%q", m.filterActive, m.input)
	}

	m = NewWithDeps(nil, nil)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = model.(Model)
	if !m.filterActive || m.input != "" {
		t.Fatalf("empty dashboard slash should enter filter mode: active=%v input=%q", m.filterActive, m.input)
	}
}

func TestDashboardTinyKeyboardLayoutAndGroupedFilterStayBounded(t *testing.T) {
	now := time.Date(2026, time.July, 13, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	m := NewWithDeps(nil, nil)
	m.now = func() time.Time { return now }
	m.groupByDir = true
	m.sessions = []adapter.Session{
		{ID: "one", AgentType: "codex", DisplayName: "release", Prompt: "ship", Cwd: root, ProcAlive: adapter.Alive, CreatedAt: now.Add(-time.Minute)},
		{ID: "two", AgentType: "claude", DisplayName: "docs", Prompt: "write", Cwd: root, ProcAlive: adapter.Alive, CreatedAt: now.Add(-time.Hour)},
	}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 44, Height: 10})
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = model.(Model)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("codex")})
	m = model.(Model)
	view := m.View()
	assertViewGeometry(t, view, 44, 10)
	for _, want := range []string{"1/2 sessions", "WORKSPACE", "1/2", "⚠ 2 sessions share this workspace", "release", "codex", "RUNNING", "ship", "›"} {
		if !strings.Contains(view, want) {
			t.Fatalf("tiny grouped filter missing %q:\n%s", want, view)
		}
	}
}

func TestDashboardFooterShowsDefaultProviderAndFilterComposer(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.defaultAgent = "opencode"
	m.sessions = []adapter.Session{{ID: "one", AgentType: "opencode", DisplayName: "one", ProcAlive: adapter.Alive}}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 30})
	view := m.View()
	if !strings.Contains(view, "opencode") || !strings.Contains(view, "Tab provider") {
		t.Fatalf("footer must expose the provider selected for bare dispatches:\n%s", view)
	}
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = model.(Model)
	view = m.View()
	if !strings.Contains(view, "filter / ") || !strings.Contains(view, "type to filter") {
		t.Fatalf("active filter must replace the command-looking composer:\n%s", view)
	}
}

func TestDashboardWorkspaceCountsAreScopedToLifecycleAndPinSections(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.groupByDir = true
	m.sessions = []adapter.Session{
		{ID: "running", AgentType: "codex", Cwd: "/work/shared", ProcAlive: adapter.Alive},
		{ID: "stopped", AgentType: "codex", Cwd: "/work/shared", ProcAlive: adapter.Exited},
	}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 30})
	view := m.View()
	if got := strings.Count(view, "WORKSPACE shared"); got != 2 {
		t.Fatalf("workspace should have one heading per lifecycle section, got %d:\n%s", got, view)
	}
	if strings.Contains(view, "WORKSPACE shared  2") {
		t.Fatalf("workspace heading must not claim rows from a different lifecycle section:\n%s", view)
	}
}

func TestDashboardSelectedIdentityAndPRSurviveLongWorkspaceAtStandardWidth(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{{
		ID: "12345678-1234-1234-1234-123456789abc", AgentType: "codex", DisplayName: "selected",
		Prompt: "review", Cwd: "/home/developer/projects/a/very/long/workspace/path/that/needs/truncation",
		ProcAlive: adapter.Alive, PR: &adapter.PRRef{Number: 99, Status: adapter.PRMerged},
	}}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 30})
	view := m.View()
	for _, want := range []string{"cwd /home/developer", "id 12345678-1234-1234-1234-123456789abc", "PR #99"} {
		if !strings.Contains(view, want) {
			t.Fatalf("selected metadata lost %q behind long workspace:\n%s", want, view)
		}
	}
}

func TestDashboardTinyFooterKeepsGuidanceWithLongInput(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.defaultAgent = "codex"
	m.input = strings.Repeat("界", 80)
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 44, Height: 10})
	view := m.View()
	assertViewGeometry(t, view, 44, 10)
	assertBottomContains(t, view, "↑↓ Enter")
}

func TestDashboardFilterNoMatchActionsAreSafeAndBackspaceExits(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{{ID: "one", AgentType: "codex", DisplayName: "release", ProcAlive: adapter.Alive}}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 30})
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = model.(Model)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("absent")})
	m = model.(Model)
	for _, key := range []tea.KeyType{tea.KeyEnter, tea.KeySpace, tea.KeyCtrlT, tea.KeyCtrlR, tea.KeyCtrlX} {
		model, cmd := m.Update(tea.KeyMsg{Type: key})
		m = model.(Model)
		if cmd != nil || m.renaming || m.confirmStop {
			t.Fatalf("no-match key %v acted on an invisible session: cmd=%v rename=%v confirm=%v", key, cmd, m.renaming, m.confirmStop)
		}
	}
	for range len([]rune("absent")) {
		model, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
		m = model.(Model)
	}
	if !m.filterActive || m.filterQuery != "" {
		t.Fatalf("backspace should edit the Unicode-safe query before exiting: active=%v query=%q", m.filterActive, m.filterQuery)
	}
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = model.(Model)
	if m.filterActive {
		t.Fatal("backspace on an empty filter should exit filtering")
	}
}

func TestDashboardFilterHandlesUnicodePasteAndFilteredReorder(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{
		{ID: "one", AgentType: "codex", DisplayName: "first 界", Prompt: "review 世界", ProcAlive: adapter.Alive, SortIndex: 0},
		{ID: "hidden", AgentType: "codex", DisplayName: "plain", Prompt: "unrelated", ProcAlive: adapter.Alive, SortIndex: 1},
		{ID: "two", AgentType: "codex", DisplayName: "second 界", Prompt: "review 世界", ProcAlive: adapter.Alive, SortIndex: 2},
	}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 30})
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = model.(Model)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("世界")})
	m = model.(Model)
	view := m.View()
	if !strings.Contains(view, "first 界") || !strings.Contains(view, "second 界") || strings.Contains(view, "plain") {
		t.Fatalf("Unicode pasted filter did not preserve rune semantics:\n%s", view)
	}
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyShiftDown})
	m = model.(Model)
	if cmd == nil || m.sessions[0].ID != "two" || m.sessions[1].ID != "hidden" || m.sessions[2].ID != "one" || m.selected != 2 {
		t.Fatalf("filtered reorder did not exchange matching endpoints safely: ids=%v selected=%d cmd=%v", sessionIDs(m.sessions), m.selected, cmd)
	}
}

func TestDashboardFilterMatchesEveryDocumentedFieldWithANDTerms(t *testing.T) {
	base := []adapter.Session{
		{ID: "managed-ABC-123", AgentType: "codex", CommandAlias: "nightly", DisplayName: "Release Captain", Prompt: "Ship the pipeline", Cwd: "/work/Unified-Agent-Manager", ProcAlive: adapter.Alive},
		{ID: "other", AgentType: "claude", DisplayName: "Documentation", Prompt: "Write a guide", Cwd: "/work/docs", ProcAlive: adapter.Exited},
	}
	for _, query := range []string{"abc-123", "NIGHTLY", "release captain", "ship PIPELINE", "unified-agent-manager", "codex running", "claude stopped"} {
		t.Run(query, func(t *testing.T) {
			m := NewWithDeps(nil, nil)
			m.sessions = append([]adapter.Session(nil), base...)
			m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 30})
			model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
			m = model.(Model)
			model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(query)})
			m = model.(Model)
			if len(m.visibleSessionIndices()) != 1 {
				t.Fatalf("query %q matched %d sessions, want 1:\n%s", query, len(m.visibleSessionIndices()), m.View())
			}
		})
	}
}

func TestDashboardFilteredActionsTargetMatchedCompositeIdentityAndEscClosesPeekFirst(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{
		{ID: "same", AgentType: "codex", DisplayName: "release", ProcAlive: adapter.Alive},
		{ID: "same", AgentType: "claude", DisplayName: "docs", ProcAlive: adapter.Alive},
	}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 30})
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = model.(Model)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("claude")})
	m = model.(Model)

	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	m = model.(Model)
	if !m.renaming || m.renameTargetAgent != "claude" || m.renameTargetID != "same" {
		t.Fatalf("rename targeted the wrong filtered session: agent=%q id=%q", m.renameTargetAgent, m.renameTargetID)
	}
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = model.(Model)

	model, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = model.(Model)
	if !m.peekOpen || m.peekTargetAgent != "claude" || m.peekTargetID != "same" {
		t.Fatalf("peek targeted the wrong filtered session: open=%v agent=%q id=%q", m.peekOpen, m.peekTargetAgent, m.peekTargetID)
	}
	query := m.filterQuery
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("send reply")})
	m = model.(Model)
	if m.input != "send reply" || m.filterQuery != query {
		t.Fatalf("Peek reply text leaked into filter: input=%q query=%q", m.input, m.filterQuery)
	}
	model, reply := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)
	if reply == nil || m.input != "" || m.filterQuery != query {
		t.Fatalf("Enter did not route filtered Peek text as a reply: cmd=%v input=%q query=%q", reply, m.input, m.filterQuery)
	}
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = model.(Model)
	if m.peekOpen || !m.filterActive {
		t.Fatalf("first Esc should close Peek while retaining filter: peek=%v filter=%v", m.peekOpen, m.filterActive)
	}
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = model.(Model)
	if m.filterActive {
		t.Fatal("second Esc should clear the retained filter")
	}
}

func TestDashboardFilteredAttachPinStopAndGroupingStayOnMatchedIdentity(t *testing.T) {
	id := "same0001"
	codexSession := adapter.Session{ID: id, AgentType: "codex", DisplayName: "release", SessionName: "uam-codex-same0001", Cwd: "/work/codex", ProcAlive: adapter.Alive}
	claudeSession := adapter.Session{ID: id, AgentType: "claude", DisplayName: "docs", SessionName: "uam-claude-same0001", Cwd: "/work/claude", ProcAlive: adapter.Alive}
	codex := &svcFakeAdapter{name: "codex", available: true, sessions: []adapter.Session{codexSession}}
	claude := &svcFakeAdapter{name: "claude", available: true, sessions: []adapter.Session{claudeSession}}
	st, err := store.Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(cfg *store.Config) error {
		cfg.Sessions[store.Key("codex", id)] = RecordFromSession(codexSession, store.ModeYolo)
		cfg.Sessions[store.Key("claude", id)] = RecordFromSession(claudeSession, store.ModeYolo)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	m := NewWithDeps(st, adapter.NewRegistry([]adapter.AgentAdapter{codex, claude}))
	m.sessions = []adapter.Session{codexSession, claudeSession}
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 30})
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = model.(Model)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("claude")})
	m = model.(Model)

	model, attach := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)
	if attach == nil {
		t.Fatal("filtered attach returned no command")
	}
	if msg := attach(); msg == nil {
		t.Fatal("filtered attach returned no message")
	}
	if claude.attachedID != id || codex.attachedID != "" {
		t.Fatalf("filtered attach crossed provider identity: claude=%q codex=%q", claude.attachedID, codex.attachedID)
	}

	model, pin := m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	m = model.(Model)
	if pin == nil {
		t.Fatal("filtered pin returned no command")
	}
	_ = pin()
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Sessions[store.Key("claude", id)].Pinned || cfg.Sessions[store.Key("codex", id)].Pinned {
		t.Fatalf("filtered pin crossed provider identity: claude=%v codex=%v", cfg.Sessions[store.Key("claude", id)].Pinned, cfg.Sessions[store.Key("codex", id)].Pinned)
	}

	model, groupCmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = model.(Model)
	selected, ok := m.selectedSession()
	if groupCmd == nil || !m.groupByDir || !ok || selected.AgentType != "claude" || selected.ID != id {
		t.Fatalf("filtered grouping lost selection/persistence command: cmd=%v grouped=%v selected=%+v", groupCmd, m.groupByDir, selected)
	}

	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
	m = model.(Model)
	if !m.confirmStop || m.confirmStopAgent != "claude" || m.confirmStopID != id {
		t.Fatalf("filtered stop confirmation targeted wrong identity: agent=%q id=%q", m.confirmStopAgent, m.confirmStopID)
	}
	model, stop := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)
	if stop == nil {
		t.Fatal("filtered stop returned no command")
	}
	_ = stop()
	if claude.stoppedID != id || codex.stoppedID != "" {
		t.Fatalf("filtered stop crossed provider identity: claude=%q codex=%q", claude.stoppedID, codex.stoppedID)
	}
}

func BenchmarkDashboardRenderAndFilter(b *testing.B) {
	for _, count := range []int{100, 1000} {
		b.Run(fmt.Sprintf("sessions-%d", count), func(b *testing.B) {
			now := time.Date(2026, time.July, 13, 12, 0, 0, 0, time.UTC)
			m := NewWithDeps(nil, nil)
			m.now = func() time.Time { return now }
			m = m.handleWindowSize(tea.WindowSizeMsg{Width: 120, Height: 40})
			for i := range count {
				m.sessions = append(m.sessions, adapter.Session{
					ID: fmt.Sprintf("session-%04d", i), AgentType: []string{"codex", "claude"}[i%2],
					DisplayName: fmt.Sprintf("session %04d", i), Prompt: "review the release pipeline",
					Cwd: "/work/unified-agent-manager", ProcAlive: adapter.Alive, CreatedAt: now.Add(-time.Duration(i) * time.Minute),
				})
			}
			m.filterActive = true
			m.filterQuery = "codex release"
			b.ResetTimer()
			for range b.N {
				_ = m.View()
			}
		})
	}
}
