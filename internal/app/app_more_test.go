package app

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/randomcodespace/unified-agent-manager/internal/adapter"
)

func TestModelViewAndKeys(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{{ID: "1", AgentType: "fake", DisplayName: "one", State: adapter.NeedsInput, Activity: "waiting", PR: &adapter.PRRef{Status: adapter.PROpen}}, {ID: "2", AgentType: "fake", DisplayName: "two", State: adapter.Completed}}
	if out := m.View(); !strings.Contains(out, "NEEDS INPUT") {
		t.Fatalf("view=%s", out)
	}
	model, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = model.(Model)
	for _, key := range []string{"down", "up", " ", "esc", "?", "esc", "ctrl+s", "tab"} {
		model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		if cast, ok := model.(Model); ok {
			m = cast
		}
	}
	model, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	m = model.(Model)
	if m.selected != 1 {
		t.Fatalf("selected=%d", m.selected)
	}
	model, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyUp})
	m = model.(Model)
	if m.selected != 0 {
		t.Fatalf("selected=%d", m.selected)
	}
	m.input = "@fake do work"
	if prompt, agent := parseDispatchInput(m.input, "claude"); prompt != "do work" || agent != "fake" {
		t.Fatalf("%q %q", prompt, agent)
	}
	if prompt, agent := parseDispatchInput("do work", "claude"); prompt != "do work" || agent != "claude" {
		t.Fatalf("%q %q", prompt, agent)
	}
	if sess, ok := m.selectedSession(); !ok || sess.ID != "1" {
		t.Fatalf("selected session %+v %v", sess, ok)
	}
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
	if stateLabel(adapter.ReadyForReview) != "review" || prStatusDot(adapter.PRMerged) == " " || truncate("abcdef", 4) != "abc…" || trimLines("a\nb\nc", 2) != "b\nc" {
		t.Fatal("helpers bad")
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
	_, _ = m.Update(tickMsg(time.Now()))
}

func TestWizardAndRenameKeys(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.wizard = true
	model, _ := m.handleWizardKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)
	if m.wizardStep != 1 {
		t.Fatalf("step=%d", m.wizardStep)
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
