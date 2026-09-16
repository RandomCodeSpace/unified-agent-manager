package app

import (
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
)

// Ctrl+C is the universal quit. A modal used to claim every key it did not
// recognise, so the wizard, rename and both confirmations swallowed it
// and left no way out for a user who never reaches for Esc.
func TestCtrlCQuitsFromEveryModal(t *testing.T) {
	modals := map[string]func(*Model){
		"expanded help":  func(m *Model) { m.helpOpen = true },
		"stop confirm":   func(m *Model) { m.confirmStop = true },
		"latest confirm": func(m *Model) { m.confirmLatest = true },
		"wizard":         func(m *Model) { m.wizard = true },
		"rename":         func(m *Model) { m.renaming = true },
		"filter":         func(m *Model) { m.filterActive = true; m.filterQuery = "abc" },
		"none":           func(m *Model) {},
	}
	for name, open := range modals {
		t.Run(name, func(t *testing.T) {
			// Given
			m := NewWithDeps(nil, adapter.NewRegistry(nil))
			open(&m)

			// When
			model, cmd := m.handleKey(keyMsg("ctrl+c"))

			// Then
			if !model.(Model).quitting {
				t.Fatal("Ctrl+C did not quit")
			}
			if cmd == nil {
				t.Fatal("Ctrl+C produced no quit command")
			}
		})
	}
}

// '?' is an explicit dashboard action. Stale hidden input must not resurrect a
// removed text field or change the binding.
func TestQuestionMarkTogglesDashboardHelpFooter(t *testing.T) {
	// Given
	m := NewWithDeps(nil, adapter.NewRegistry(nil))
	m.input = "why"

	// When
	model, _ := m.handleKey(keyMsg("?"))

	// Then
	m = model.(Model)
	if !m.helpOpen {
		t.Fatal("'?' did not open help")
	}
	if m.input != "why" {
		t.Fatalf("question mark mutated hidden input: %q", m.input)
	}
	model, _ = m.handleKey(keyMsg("?"))
	if model.(Model).helpOpen {
		t.Fatal("second '?' did not restore primary help hints")
	}

	// And it still opens help on an empty dashboard state.
	empty := NewWithDeps(nil, adapter.NewRegistry(nil))
	model, _ = empty.handleKey(keyMsg("?"))
	if !model.(Model).helpOpen {
		t.Fatal("'?' must expand help")
	}
}
