package app

import (
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	tea "github.com/charmbracelet/bubbletea"
)

// Ctrl+C is the universal quit. A modal used to claim every key it did not
// recognise, so help, the wizard, rename and both confirmations swallowed it
// and left no way out for a user who never reaches for Esc.
func TestCtrlCQuitsFromEveryModal(t *testing.T) {
	modals := map[string]func(*Model){
		"help":           func(m *Model) { m.helpOpen = true },
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
			model, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlC})

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

// '?' opens help on an empty composer and is ordinary text otherwise — the same
// rule the other letter-shaped bindings follow. Without the guard a prompt
// could never contain a question mark.
func TestQuestionMarkTypesIntoNonEmptyComposer(t *testing.T) {
	// Given
	m := NewWithDeps(nil, adapter.NewRegistry(nil))
	m.input = "why"

	// When
	model, _ := m.handleKey(keyMsg("?"))

	// Then
	m = model.(Model)
	if m.helpOpen {
		t.Fatal("'?' opened help instead of typing into the composer")
	}
	if m.input != "why?" {
		t.Fatalf("input = %q, want %q", m.input, "why?")
	}

	// And it still opens help on an empty composer.
	empty := NewWithDeps(nil, adapter.NewRegistry(nil))
	model, _ = empty.handleKey(keyMsg("?"))
	if !model.(Model).helpOpen {
		t.Fatal("'?' on an empty composer must open help")
	}
}
