package app

import (
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	tea "github.com/charmbracelet/bubbletea"
)

func TestModelViewBasics(t *testing.T) {
	m := modelWithTwoSessions()
	out := m.View()
	if !strings.Contains(out, "ACTIVE") || !strings.Contains(out, "SELECTED") {
		t.Fatalf("view=%s", out)
	}
	if strings.Contains(out, "TMUX: LIVE") || strings.Contains(out, "TMUX: DEAD") {
		t.Fatalf("view should hide textual tmux liveness: %s", out)
	}
}

func TestModelKeyNavigationAndDispatchParsing(t *testing.T) {
	m := modelWithTwoSessions()
	model, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = model.(Model)
	for _, key := range []string{"down", "up", " ", "esc", "?", "esc", "ctrl+s", "tab"} {
		model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		m = model.(Model)
	}
	assertSelectionAfterKey(t, &m, tea.KeyDown, 1)
	assertSelectionAfterKey(t, &m, tea.KeyUp, 0)
	assertDispatchParsing(t, m)
}

func TestModelModalViewsAndHelpers(t *testing.T) {
	m := modelWithTwoSessions()
	m.helpOpen = true
	if !strings.Contains(m.View(), "Keys:") {
		t.Fatal("missing help")
	}
	m.confirmStop = true
	m.helpOpen = false
	if !strings.Contains(m.View(), "Stop") {
		t.Fatal("missing confirm")
	}
	m.wizard = true
	m.confirmStop = false
	if !strings.Contains(m.View(), "NEW SESSION") {
		t.Fatal("missing wizard")
	}
	assertViewHelpers(t)
}

func modelWithTwoSessions() Model {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{{ID: "1", AgentType: "fake", DisplayName: "one", Prompt: "waiting prompt", Cwd: "/tmp/one", ProcAlive: adapter.Alive, PR: &adapter.PRRef{Status: adapter.PROpen}}, {ID: "2", AgentType: "fake", DisplayName: "two", Prompt: "done prompt", ProcAlive: adapter.Exited}}
	return m
}

func assertSelectionAfterKey(t *testing.T, m *Model, key tea.KeyType, want int) {
	t.Helper()
	model, _ := m.handleKey(tea.KeyMsg{Type: key})
	*m = model.(Model)
	if m.selected != want {
		t.Fatalf("selected=%d", m.selected)
	}
}

func assertDispatchParsing(t *testing.T, m Model) {
	t.Helper()
	m.input = "@fake do work"
	if spec := parseDispatchSpec(m.input, "claude"); spec.Prompt != "do work" || spec.Agent != "fake" {
		t.Fatalf("%q %q", spec.Prompt, spec.Agent)
	}
	spec := parseDispatchSpec("@fake #my-session do work", "claude")
	if spec.Agent != "fake" || spec.Name != "my-session" || spec.Prompt != "do work" {
		t.Fatalf("spec=%+v", spec)
	}
	spec = parseDispatchSpec("@fake:review #my-session do work", "claude")
	if spec.Agent != "fake" || spec.Alias != "review" || spec.Name != "my-session" || spec.Prompt != "do work" {
		t.Fatalf("alias spec=%+v", spec)
	}
	spec = parseDispatchSpec("@fake   #my-session   do   work\twith tabs", "claude")
	if spec.Agent != "fake" || spec.Alias != "" || spec.Name != "my-session" || spec.Prompt != "do   work\twith tabs" {
		t.Fatalf("spaced spec=%+v", spec)
	}
	spec = parseDispatchSpec("@fake #my-session", "claude")
	if spec.Agent != "fake" || spec.Name != "my-session" || spec.Prompt != "" {
		t.Fatalf("named no-prompt spec=%+v", spec)
	}
	if spec := parseDispatchSpec("do work", "claude"); spec.Prompt != "do work" || spec.Agent != "claude" {
		t.Fatalf("%q %q", spec.Prompt, spec.Agent)
	}
	if spec := parseDispatchSpec("do   work\twith tabs", "claude"); spec.Prompt != "do   work\twith tabs" || spec.Agent != "claude" {
		t.Fatalf("%q %q", spec.Prompt, spec.Agent)
	}
	if sess, ok := m.selectedSession(); !ok || sess.ID != "1" {
		t.Fatalf("selected session %+v %v", sess, ok)
	}
}

func assertViewHelpers(t *testing.T) {
	t.Helper()
	if stateLabel(adapter.Active) != "active" || prStatusDot(adapter.PRMerged) == " " {
		t.Fatal("status helpers bad")
	}
	if truncate("abcdef", 4) != "abc…" || trimLines("a\nb\nc", 2) != "b\nc" {
		t.Fatal("text helpers bad")
	}
}

func TestModelUpdateMessages(t *testing.T) {
	m := NewWithDeps(nil, nil)
	model, _ := m.Update(sessionsLoadedMsg{sessions: []adapter.Session{{ID: "1"}}, defaultAgent: "fake", groupByDir: true})
	m = model.(Model)
	if len(m.sessions) != 1 || m.defaultAgent != "fake" || !m.groupByDir {
		t.Fatalf("bad load %+v", m)
	}
	model, _ = m.Update(peekLoadedMsg{text: "tail"})
	m = model.(Model)
	if m.peekText != "tail" {
		t.Fatal(m.peekText)
	}
	model, _ = m.Update(dispatchedMsg{session: adapter.Session{ID: "abc"}})
	m = model.(Model)
	if !strings.Contains(m.message, "abc") {
		t.Fatal(m.message)
	}
}

func TestDispatchedMessageAttachesNewSession(t *testing.T) {
	fake := &svcFakeAdapter{name: "fake", available: true}
	m := NewWithDeps(nil, adapter.NewRegistry([]adapter.AgentAdapter{fake}))
	var gotArgs []string
	m.execProcess = func(cmd *exec.Cmd, cb tea.ExecCallback) tea.Cmd {
		gotArgs = append([]string(nil), cmd.Args...)
		return func() tea.Msg { return cb(nil) }
	}

	model, cmd := m.Update(dispatchedMsg{session: adapter.Session{ID: "abc12345", AgentType: "fake"}})
	m = model.(Model)
	if cmd == nil {
		t.Fatal("expected attach spec command")
	}
	specMsg, ok := cmd().(attachSpecMsg)
	if !ok {
		t.Fatalf("expected attachSpecMsg, got %T", specMsg)
	}
	model, cmd = m.Update(specMsg)
	m = model.(Model)
	if cmd == nil {
		t.Fatal("expected exec attach command")
	}
	finishedMsg, ok := cmd().(attachFinishedMsg)
	if !ok {
		t.Fatalf("expected attachFinishedMsg, got %T", finishedMsg)
	}
	model, refreshCmd := m.Update(finishedMsg)
	m = model.(Model)
	if refreshCmd == nil {
		t.Fatal("expected refresh command after returning from attach")
	}
	want := []string{"echo", "abc12345"}
	if !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("attach args = %v, want %v", gotArgs, want)
	}
	if m.input != "" || !strings.Contains(m.message, "returned to uam") {
		t.Fatalf("message/input not updated after attach: message=%q input=%q", m.message, m.input)
	}
}

// F03 — a dispatch that returns a live session alongside an advisory persist
// error must still attach (the session is running); the warning surfaces in the
// status line but does not abort the attach.
func TestDispatchedAttachesLiveSessionDespiteAdvisoryError(t *testing.T) {
	fake := &svcFakeAdapter{name: "fake", available: true}
	m := NewWithDeps(nil, adapter.NewRegistry([]adapter.AgentAdapter{fake}))
	m.execProcess = func(cmd *exec.Cmd, cb tea.ExecCallback) tea.Cmd {
		return func() tea.Msg { return cb(nil) }
	}
	model, cmd := m.Update(dispatchedMsg{session: adapter.Session{ID: "abc12345", AgentType: "fake"}, err: errors.New("persist boom")})
	m = model.(Model)
	if cmd == nil {
		t.Fatal("a live session must still attach even with an advisory error")
	}
	if _, ok := cmd().(attachSpecMsg); !ok {
		t.Fatalf("expected attachSpecMsg from the attach command")
	}
}

// F03 — a dispatch failure with no live session (empty ID) must NOT attach; it
// only reports the error.
func TestDispatchedFailureWithoutSessionDoesNotAttach(t *testing.T) {
	m := NewWithDeps(nil, nil)
	model, cmd := m.Update(dispatchedMsg{session: adapter.Session{}, err: errors.New("agent unavailable")})
	m = model.(Model)
	if cmd != nil {
		t.Fatal("a failed dispatch with no live session must not attach")
	}
	if !strings.Contains(m.message, "agent unavailable") {
		t.Fatalf("expected error in status line, got %q", m.message)
	}
}

func TestViewShowsDetailsOnTopAndTaskInTable(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{
		{ID: "1", AgentType: "fake", DisplayName: "one", Prompt: "fix the parser", Cwd: "/tmp/project", TmuxSession: "uam-fake-1", ProcAlive: adapter.Alive},
		{ID: "2", AgentType: "fake", DisplayName: "old", Prompt: "old prompt", Cwd: "/tmp/old", TmuxSession: "uam-fake-2", ProcAlive: adapter.Exited, Closed: true},
	}
	view := m.View()
	if !strings.Contains(view, "cwd: /tmp/project") {
		t.Fatalf("view should show the absolute cwd in details: %s", view)
	}
	if strings.Contains(view, "⠋") || strings.Contains(view, "💀") || strings.Contains(view, "TMUX: LIVE") || strings.Contains(view, "🚀") || strings.Contains(view, "🟢") {
		t.Fatalf("view should use compact styling, no spinner/skull/large emoji: %s", view)
	}
	if strings.Contains(view, "1 live") || strings.Contains(view, "1 dead") || strings.Contains(view, "agent fake") {
		t.Fatalf("view should not show aggregate header stats: %s", view)
	}
	table := m.renderTable()
	if !strings.Contains(table, "ACTIVE") || !strings.Contains(table, "CLOSED") {
		t.Fatalf("table should group sessions into ACTIVE and CLOSED: %s", table)
	}
	if !strings.Contains(table, "one") || !strings.Contains(table, "fix the parser") {
		t.Fatalf("table should show session name and task: %s", table)
	}
}

func TestSpaceRestartsStoppedSessionInsteadOfPeeking(t *testing.T) {
	// A running session: Space opens the peek panel.
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{{ID: "1", AgentType: "fake", DisplayName: "live", ProcAlive: adapter.Alive}}
	if cmd := m.handleSpaceKey(" "); cmd == nil || !m.peekOpen {
		t.Fatalf("space on a running session should peek: cmd=%v peekOpen=%v", cmd, m.peekOpen)
	}

	// A stopped session: Space restarts it and does not open the peek panel.
	m = NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{{ID: "2", AgentType: "fake", DisplayName: "stopped", ProcAlive: adapter.Exited}}
	cmd := m.handleSpaceKey(" ")
	if m.peekOpen {
		t.Fatal("space on a stopped session should not open the peek panel")
	}
	if cmd == nil {
		t.Fatal("space on a stopped session should return a resume command")
	}
}

func TestSessionRowsStayStaticAcrossRefresh(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{{ID: "live", DisplayName: "live", ProcAlive: adapter.Alive}, {ID: "dead", DisplayName: "dead", ProcAlive: adapter.Exited, Closed: true}}
	before := m.renderTable()
	if !strings.Contains(before, "ACTIVE") || !strings.Contains(before, "CLOSED") {
		t.Fatalf("table should group sessions into ACTIVE and CLOSED: %s", before)
	}
	if strings.Contains(before, "⠋") || strings.Contains(before, "💀") || strings.Contains(before, "🚀") || strings.Contains(before, "🔴") || strings.Contains(before, "🟢") {
		t.Fatalf("table should stay glyph-based, no spinner/skull/emoji: %s", before)
	}

	model, cmd := m.Update(refreshMsg(time.Now()))
	m = model.(Model)
	if cmd == nil {
		t.Fatal("expected refresh command batch")
	}
	after := m.renderTable()
	if after != before {
		t.Fatalf("table should remain static across refresh\nbefore=%s\nafter=%s", before, after)
	}
}

func TestWizardAndRenameKeys(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.wizard = true
	model, _ := m.handleWizardKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)
	if m.wizardStep != 1 {
		t.Fatalf("step=%d", m.wizardStep)
	}
	if m.input != "" {
		t.Fatalf("alias input=%q", m.input)
	}
	model, _ = m.handleWizardKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = model.(Model)
	if !strings.Contains(m.input, "x") {
		t.Fatalf("input=%q", m.input)
	}
	m.sessions = []adapter.Session{{ID: "1", DisplayName: "old"}}
	m.renaming = true
	m.input = "new"
	model, _ = m.handleRenameKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = model.(Model)
	if m.renaming {
		t.Fatal("still renaming")
	}
}

func TestMovementAndQuitBranches(t *testing.T) {
	m := modelWithTwoSessions()
	if handled, cmd := m.handleMovementKey("shift+up"); !handled || cmd != nil {
		t.Fatalf("boundary move handled=%v cmd=%v", handled, cmd)
	}
	if handled, cmd := m.handleMovementKey("shift+down"); !handled || cmd == nil || m.selected != 1 || m.sessions[1].ID != "1" {
		t.Fatalf("move down failed handled=%v cmd=%v selected=%d sessions=%v", handled, cmd, m.selected, m.sessions)
	}

	m = modelWithTwoSessions()
	if handled, cmd := m.handleActionKey("ctrl+c"); !handled || cmd == nil || !m.quitting {
		t.Fatalf("quit branch handled=%v cmd=%v quitting=%v", handled, cmd, m.quitting)
	}
	m = modelWithTwoSessions()
	m.input = "typed"
	model, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = model.(Model)
	if cmd != nil || m.input != "typedq" || m.quitting {
		t.Fatalf("q should type into input: cmd=%v input=%q quitting=%v", cmd, m.input, m.quitting)
	}

	m = modelWithTwoSessions()
	m.peekOpen = true
	if handled, cmd := m.handleActionKey("esc"); !handled || cmd != nil || m.peekOpen || m.quitting {
		t.Fatalf("esc should close peek first: handled=%v cmd=%v peek=%v quitting=%v", handled, cmd, m.peekOpen, m.quitting)
	}

	m = modelWithTwoSessions()
	m.input = "draft"
	if handled, cmd := m.handleActionKey("esc"); !handled || cmd != nil || m.input != "" || m.quitting {
		t.Fatalf("esc should clear input next: handled=%v cmd=%v input=%q quitting=%v", handled, cmd, m.input, m.quitting)
	}

	m = modelWithTwoSessions()
	if handled, cmd := m.handleActionKey("esc"); !handled || cmd == nil || !m.quitting {
		t.Fatalf("esc should quit on empty main screen: handled=%v cmd=%v quitting=%v", handled, cmd, m.quitting)
	}
}

func TestInputWindowAndStateBranches(t *testing.T) {
	m := Model{}
	if cmd := m.handleEnterKey(); cmd != nil {
		t.Fatalf("empty enter should not command: %v", cmd)
	}
	m.input = "draft"
	if cmd := m.handleSpaceKey(" "); cmd != nil || m.input != "draft " {
		t.Fatalf("space input branch cmd=%v input=%q", cmd, m.input)
	}
	m = modelWithTwoSessions()
	m.peekOpen = true
	if cmd := m.handleSpaceKey(" "); cmd != nil || m.peekOpen {
		t.Fatalf("closing peek cmd=%v peek=%v", cmd, m.peekOpen)
	}

	m.height = 12
	m.selected = 1
	m.peekOpen = true
	start, end := m.visibleSessionWindow()
	if start < 0 || end < start || end > len(m.sessions) {
		t.Fatalf("bad window %d:%d", start, end)
	}
	if stateLabel(adapter.Active) != "active" || stateLabel(adapter.Failed) != "failed" {
		t.Fatal("state labels not covered")
	}
}

func TestRenameEditingBranches(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{{ID: "1", DisplayName: "old"}}
	m.renaming = true
	m.input = "ab"
	model, cmd := m.handleRenameKey(tea.KeyMsg{Type: tea.KeyBackspace})
	m = model.(Model)
	if cmd != nil || m.input != "a" {
		t.Fatalf("backspace input=%q cmd=%v", m.input, cmd)
	}
	model, cmd = m.handleRenameKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")})
	m = model.(Model)
	if cmd != nil || m.input != "az" {
		t.Fatalf("rune input=%q cmd=%v", m.input, cmd)
	}
	model, cmd = m.handleRenameKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)
	if cmd == nil || m.renaming || m.input != "" {
		t.Fatalf("enter rename cmd=%v renaming=%v input=%q", cmd, m.renaming, m.input)
	}
}
