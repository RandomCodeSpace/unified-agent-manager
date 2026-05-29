package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
	tea "github.com/charmbracelet/bubbletea"
)

func TestCycleAndPRDots(t *testing.T) {
	m := NewWithDeps(nil, adapter.NewRegistry([]adapter.AgentAdapter{&svcFakeAdapter{name: "b", available: true}, &svcFakeAdapter{name: "a", available: true}}))
	m.defaultAgent = "a"
	m.cycleDefaultAgent()
	if m.defaultAgent != "b" {
		t.Fatalf("agent=%s", m.defaultAgent)
	}
	m.cycleDefaultAgent()
	if m.defaultAgent != "a" {
		t.Fatalf("agent=%s", m.defaultAgent)
	}
	for _, st := range []adapter.PRStatus{adapter.PROpen, adapter.PRDraft, adapter.PRClosed, adapter.PRMerged, adapter.PRNone} {
		_ = prStatusDot(st)
	}
	if _, ok := (Model{}).selectedSession(); ok {
		t.Fatal("empty selected should fail")
	}
}

func TestTabPersistsDefaultAgentChoice(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "sessions.json"))
	m := NewWithDeps(st, adapter.NewRegistry([]adapter.AgentAdapter{&svcFakeAdapter{name: "a", available: true}, &svcFakeAdapter{name: "b", available: true}}))
	m.defaultAgent = "a"
	model, _ := m.handleKey(keyMsg("tab"))
	m = model.(Model)
	if m.defaultAgent != "b" {
		t.Fatalf("agent=%s", m.defaultAgent)
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultAgent != "b" {
		t.Fatalf("stored default agent=%q", cfg.DefaultAgent)
	}
}

func TestWizardTabPersistsDefaultAgentChoice(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "sessions.json"))
	m := NewWithDeps(st, adapter.NewRegistry([]adapter.AgentAdapter{&svcFakeAdapter{name: "a", available: true}, &svcFakeAdapter{name: "b", available: true}}))
	m.wizard = true
	m.defaultAgent = "a"
	model, _ := m.handleWizardKey(keyMsg("tab"))
	m = model.(Model)
	if m.defaultAgent != "b" || m.wizardAgent != "b" {
		t.Fatalf("default=%s wizard=%s", m.defaultAgent, m.wizardAgent)
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultAgent != "b" {
		t.Fatalf("stored default agent=%q", cfg.DefaultAgent)
	}
}

func TestModelCommandFactories(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "sessions.json"))
	fake := &svcFakeAdapter{name: "fake", available: true, sessions: []adapter.Session{{ID: "abc12345", AgentType: "fake", DisplayName: "live", Cwd: "/tmp", TmuxSession: "uam-fake-abc12345", State: adapter.Active, CreatedAt: time.Now()}}}
	m := NewWithDeps(st, adapter.NewRegistry([]adapter.AgentAdapter{fake}))
	m.sessions = fake.sessions
	m.defaultAgent = "fake"
	if New().defaultAgent == "" {
		t.Fatal("New default empty")
	}
	if !NewWizard(st, m.service.Registry).wizard {
		t.Fatal("NewWizard not wizard")
	}
	if m.Init() == nil {
		t.Fatal("Init returned nil")
	}
	if m.loadSessionsCmd()() == nil {
		t.Fatal("nil load msg")
	}
	if msg := m.dispatchNamedCmd("fake", "", "prompt")(); msg.(dispatchedMsg).err != nil {
		t.Fatalf("dispatch msg=%+v", msg)
	}
	if msg := m.peekSelectedCmd()(); msg.(peekLoadedMsg).text != "tail" {
		t.Fatalf("peek msg=%+v", msg)
	}
	if msg := m.pinSelectedCmd()(); msg.(sessionsLoadedMsg).err != nil {
		t.Fatalf("pin msg=%+v", msg)
	}
	if msg := m.stopSelectedCmd(false)(); msg.(sessionsLoadedMsg).err != nil {
		t.Fatalf("stop msg=%+v", msg)
	}
	if msg := m.persistOrderCmd()(); msg.(sessionsLoadedMsg).err != nil {
		t.Fatalf("persist msg=%+v", msg)
	}
	if m.attachSelectedCmd()() == nil {
		t.Fatal("attach returned nil msg")
	}
}

func TestHandleKeyBranches(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "sessions.json"))
	fake := &svcFakeAdapter{name: "fake", available: true, sessions: []adapter.Session{{ID: "abc12345", AgentType: "fake", DisplayName: "live", Cwd: "/tmp", TmuxSession: "uam-fake-abc12345", State: adapter.Active, CreatedAt: time.Now()}}}
	m := NewWithDeps(st, adapter.NewRegistry([]adapter.AgentAdapter{fake}))
	m.sessions = fake.sessions
	m.defaultAgent = "fake"
	cases := []string{"ctrl+t", "ctrl+r", "esc", "ctrl+x", "n", "e", "esc", "backspace"}
	for _, key := range cases {
		model, _ := m.handleKey(keyMsg(key))
		m = model.(Model)
	}
	m.input = "abc"
	model, _ := m.handleKey(keyMsg("backspace"))
	m = model.(Model)
	if m.input != "ab" {
		t.Fatalf("backspace=%q", m.input)
	}
	model, _ = m.handleKey(keyMsg("x"))
	m = model.(Model)
	if !strings.HasSuffix(m.input, "x") {
		t.Fatalf("rune input=%q", m.input)
	}
	m.input = "@fake hello"
	model, cmd := m.handleKey(keyMsg("enter"))
	m = model.(Model)
	if cmd == nil {
		t.Fatal("expected dispatch cmd")
	}
	m.input = ""
	model, cmd = m.handleKey(keyMsg("right"))
	_ = model.(Model)
	if cmd == nil {
		t.Fatal("expected attach cmd")
	}
}

func TestPromptTypingAllowsAgentMentionsSpacesAndShortcutLetters(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "sessions.json"))
	fake := &svcFakeAdapter{name: "fake", available: true, sessions: []adapter.Session{{ID: "abc12345", AgentType: "fake", DisplayName: "live", Cwd: "/tmp", TmuxSession: "uam-fake-abc12345", State: adapter.Active, CreatedAt: time.Now()}}}
	m := NewWithDeps(st, adapter.NewRegistry([]adapter.AgentAdapter{fake}))
	m.sessions = fake.sessions
	m.defaultAgent = "fake"

	for _, r := range "@fake #bugfix hello world" {
		model, _ := m.handleKey(keyMsg(string(r)))
		m = model.(Model)
	}
	if m.input != "@fake #bugfix hello world" {
		t.Fatalf("typed input=%q", m.input)
	}

	_, cmd := m.handleKey(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("expected dispatch command")
	}
	msg := cmd().(dispatchedMsg)
	if msg.err != nil {
		t.Fatalf("dispatch err=%v", msg.err)
	}
	if msg.session.AgentType != "fake" || msg.session.DisplayName != "bugfix" {
		t.Fatalf("session=%+v", msg.session)
	}
}

func TestRenameAndWizardEnterBranches(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "sessions.json"))
	fake := &svcFakeAdapter{name: "fake", available: true, sessions: []adapter.Session{{ID: "abc12345", AgentType: "fake", DisplayName: "live", Cwd: "/tmp", TmuxSession: "uam-fake-abc12345", State: adapter.Active, CreatedAt: time.Now()}}}
	m := NewWithDeps(st, adapter.NewRegistry([]adapter.AgentAdapter{fake}))
	m.sessions = fake.sessions
	m.defaultAgent = "fake"
	m.renaming = true
	m.input = "renamed"
	model, cmd := m.handleRenameKey(keyMsg("enter"))
	m = model.(Model)
	if m.renaming || cmd == nil {
		t.Fatal("rename enter failed")
	}
	m.wizard = true
	m.wizardStep = 0
	m.defaultAgent = "fake"
	model, _ = m.handleWizardKey(keyMsg("enter"))
	m = model.(Model)
	m.input = "/tmp"
	model, _ = m.handleWizardKey(keyMsg("enter"))
	m = model.(Model)
	m.input = "prompt"
	model, cmd = m.handleWizardKey(keyMsg("enter"))
	m = model.(Model)
	if m.wizard || cmd == nil {
		t.Fatal("wizard dispatch failed")
	}
}

func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "ctrl+t":
		return tea.KeyMsg{Type: tea.KeyCtrlT}
	case "ctrl+r":
		return tea.KeyMsg{Type: tea.KeyCtrlR}
	case "ctrl+x":
		return tea.KeyMsg{Type: tea.KeyCtrlX}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
	case "n":
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}
	case "e":
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")}
	case "x":
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func TestServiceNilAndUnavailableBranches(t *testing.T) {
	svc := NewService(nil, nil)
	if _, err := svc.DispatchNamed(context.Background(), "x", "", "", "", ""); err == nil {
		t.Fatal("want registry error")
	}
	if err := svc.SetUI(func(*store.UISettings) {}); err != nil {
		t.Fatal(err)
	}
	if err := svc.UpdateSortOrder(nil); err != nil {
		t.Fatal(err)
	}
}
