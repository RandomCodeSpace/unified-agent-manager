package app

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
	tea "github.com/charmbracelet/bubbletea"
)

// renameTestModel wires a real store backed by a fake adapter that lists two
// live sessions, so Service.Rename / Service.Stop resolve through the genuine
// Find path and persist against the correct store record.
func renameTestModel(t *testing.T, sessions []adapter.Session) (Model, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	fake := &svcFakeAdapter{name: "fake", available: true, sessions: sessions}
	m := NewWithDeps(st, adapter.NewRegistry([]adapter.AgentAdapter{fake}))
	m.sessions = append([]adapter.Session(nil), sessions...)
	return m, st
}

func recordName(t *testing.T, st *store.Store, agent, id string) string {
	t.Helper()
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Sessions[store.Key(agent, id)].Name
}

// F27 — pressing Enter to confirm a rename must not panic when the session list
// has emptied (e.g. the renamed session was killed externally) between opening
// the modal and confirming. Clamping m.selected alone is still out-of-range on a
// zero-length slice, so the Enter branch must route through selectedSession().
func TestRenameEnterOnEmptiedListDoesNotPanic(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{{ID: "1", AgentType: "fake", DisplayName: "old"}}
	m.startRename()
	if !m.renaming {
		t.Fatal("expected rename mode to be active")
	}
	// The list empties out from under the modal.
	m.sessions = nil
	m.selected = 0

	model, _ := m.handleRenameKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)
	if m.renaming {
		t.Fatal("Enter on an emptied list should close the rename modal")
	}
	if m.input != "" {
		t.Fatalf("Enter should clear the rename input, got %q", m.input)
	}
}

// C2-1 — a refresh that reorders the session list while the rename modal is open
// must not retarget the rename to whatever row now sits under m.selected. The
// target session id is snapshotted at startRename and resolved at Enter.
func TestRenameTargetsOriginalSessionAfterReorder(t *testing.T) {
	live := []adapter.Session{
		{ID: "alpha", AgentType: "fake", DisplayName: "alpha", TmuxSession: "uam-fake-alpha", State: adapter.Active, CreatedAt: time.Now()},
		{ID: "beta", AgentType: "fake", DisplayName: "beta", TmuxSession: "uam-fake-beta", State: adapter.Active, CreatedAt: time.Now()},
	}
	m, st := renameTestModel(t, live)
	m.selected = 0
	m.startRename()
	m.input = "alpha-renamed"

	// A refresh reorders the list: beta now sits under the cursor (index 0).
	m.sessions = []adapter.Session{live[1], live[0]}
	m.selected = 0

	model, cmd := m.handleRenameKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)
	if cmd == nil {
		t.Fatal("expected a rename command")
	}
	cmd() // execute the rename against the service

	if got := recordName(t, st, "fake", "alpha"); got != "alpha-renamed" {
		t.Fatalf("rename should target the originally-selected session alpha, got name %q on alpha", got)
	}
	if got := recordName(t, st, "fake", "beta"); got == "alpha-renamed" {
		t.Fatalf("rename leaked onto the reordered session beta: name=%q", got)
	}
}

// F29 — multibyte input and paste (a multi-rune KeyRunes) must reach the rename
// buffer verbatim. The old len(key)==1 byte check dropped both.
func TestRenameAcceptsMultibyteAndPaste(t *testing.T) {
	cases := []struct {
		name  string
		runes []rune
	}{
		{"e-acute", []rune("é")},
		{"cjk", []rune("世界")},
		{"emoji", []rune("🚀")},
		{"paste", []rune("hello world")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewWithDeps(nil, nil)
			m.sessions = []adapter.Session{{ID: "1", DisplayName: "x"}}
			m.renaming = true
			m.input = ""
			model, _ := m.handleRenameKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: tc.runes})
			m = model.(Model)
			if m.input != string(tc.runes) {
				t.Fatalf("rename input = %q, want %q", m.input, string(tc.runes))
			}
		})
	}
}

// F29 — Alt-chords (e.g. Alt+a) must not leak as literal text into the rename
// buffer.
func TestRenameIgnoresAltChord(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{{ID: "1", DisplayName: "x"}}
	m.renaming = true
	m.input = ""
	model, _ := m.handleRenameKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a"), Alt: true})
	m = model.(Model)
	if m.input != "" {
		t.Fatalf("Alt+a should not type into the rename buffer, got %q", m.input)
	}
}

// F29 — the wizard prompt input must also accept multibyte/paste and ignore Alt.
func TestWizardInputAcceptsMultibyteAndIgnoresAlt(t *testing.T) {
	m := NewWithDeps(nil, nil)
	m.wizard = true
	m.wizardStep = 2
	m.input = ""
	model, _ := m.handleWizardKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("café 世界")})
	m = model.(Model)
	if m.input != "café 世界" {
		t.Fatalf("wizard input = %q, want %q", m.input, "café 世界")
	}
	model, _ = m.handleWizardKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z"), Alt: true})
	m = model.(Model)
	if m.input != "café 世界" {
		t.Fatalf("Alt+z should not type into the wizard buffer, got %q", m.input)
	}
}

// F29 — the stop-confirm dialog must act on the session selected when it was
// opened, not whatever row a refresh reorder slid under the cursor.
func TestStopConfirmTargetsOriginalSessionAfterReorder(t *testing.T) {
	live := []adapter.Session{
		{ID: "alpha", AgentType: "fake", DisplayName: "alpha", TmuxSession: "uam-fake-alpha", State: adapter.Active, CreatedAt: time.Now()},
		{ID: "beta", AgentType: "fake", DisplayName: "beta", TmuxSession: "uam-fake-beta", State: adapter.Active, CreatedAt: time.Now()},
	}
	m, st := renameTestModel(t, live)
	m.selected = 0
	// Seed both store records so a remove is observable.
	if err := st.Update(func(cfg *store.Config) error {
		cfg.Sessions[store.Key("fake", "alpha")] = RecordFromSession(live[0], store.ModeYolo)
		cfg.Sessions[store.Key("fake", "beta")] = RecordFromSession(live[1], store.ModeYolo)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Open the confirm dialog on alpha (index 0).
	if handled, cmd := m.handleActionKey("ctrl+x"); !handled || cmd != nil {
		t.Fatalf("ctrl+x should open confirm: handled=%v cmd=%v", handled, cmd)
	}
	if !m.confirmStop {
		t.Fatal("expected confirmStop")
	}

	// Refresh reorders: beta now at index 0.
	m.sessions = []adapter.Session{live[1], live[0]}
	m.selected = 0

	_, model, cmd := m.handleModalKey(tea.KeyMsg{Type: tea.KeyEnter}, "enter")
	m = model.(Model)
	if cmd == nil {
		t.Fatal("expected a stop command from confirm Enter")
	}
	cmd()

	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Sessions[store.Key("fake", "alpha")]; ok {
		t.Fatalf("stop should have removed the originally-confirmed session alpha; record still present")
	}
	if _, ok := cfg.Sessions[store.Key("fake", "beta")]; !ok {
		t.Fatalf("stop targeted the reordered session beta instead of alpha")
	}
}
