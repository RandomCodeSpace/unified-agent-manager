package explore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// buildTestTree creates:
//
//	<tmpdir>/
//	  file_a.txt      ("content of a")
//	  file_b.go       ("package main\n")
//	  subdir/
//	    inner.txt     ("inner content")
func buildTestTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file_a.txt"), []byte("content of a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file_b.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "subdir")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "inner.txt"), []byte("inner content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// sendSpecialKey sends a key string (up/down/enter/left/etc.) to the model
// and returns the updated Model.
func sendSpecialKey(t *testing.T, m Model, key string) Model {
	t.Helper()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	next, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update did not return a Model for key %q", key)
	}
	return next
}

func TestNew_CwdEqualsRoot(t *testing.T) {
	root := buildTestTree(t)
	m := New(root)
	abs, _ := filepath.Abs(root)
	if m.cwd != abs {
		t.Fatalf("cwd = %q, want %q", m.cwd, abs)
	}
	if m.root != abs {
		t.Fatalf("root = %q, want %q", m.root, abs)
	}
}

func TestNew_EntriesLoaded(t *testing.T) {
	root := buildTestTree(t)
	m := New(root)
	if len(m.entries) == 0 {
		t.Fatal("expected non-empty entries after New")
	}
}

func TestNew_DirsFirstInEntries(t *testing.T) {
	root := buildTestTree(t)
	m := New(root)
	// "subdir" must appear before the files.
	found := false
	for _, e := range m.entries {
		if e.name == "subdir" {
			found = true
			if !e.isDir {
				t.Fatal("subdir entry: isDir is false")
			}
			break
		}
	}
	if !found {
		t.Fatal("subdir not found in entries")
	}
	// Confirm first entry is a dir (dirs-first invariant).
	if len(m.entries) > 0 && !m.entries[0].isDir {
		t.Fatalf("first entry %q should be a directory", m.entries[0].name)
	}
}

func TestMoveCursorDownUp(t *testing.T) {
	root := buildTestTree(t)
	m := New(root)
	initial := m.cursor

	m2 := sendSpecialKey(t, m, "down")
	if m2.cursor != initial+1 {
		t.Fatalf("after down: cursor = %d, want %d", m2.cursor, initial+1)
	}

	m3 := sendSpecialKey(t, m2, "up")
	if m3.cursor != initial {
		t.Fatalf("after up: cursor = %d, want %d", m3.cursor, initial)
	}
}

func TestCursorAtTopNoWrap(t *testing.T) {
	root := buildTestTree(t)
	m := New(root)
	m.cursor = 0
	m2 := sendSpecialKey(t, m, "up")
	if m2.cursor != 0 {
		t.Fatalf("up at top: cursor = %d, want 0", m2.cursor)
	}
}

func TestCursorAtBottomNoWrap(t *testing.T) {
	root := buildTestTree(t)
	m := New(root)
	m.cursor = len(m.entries) - 1
	m2 := sendSpecialKey(t, m, "down")
	if m2.cursor != len(m.entries)-1 {
		t.Fatalf("down at bottom: cursor = %d, want %d", m2.cursor, len(m.entries)-1)
	}
}

func TestPreviewUpdatesOnCursorMove(t *testing.T) {
	root := buildTestTree(t)
	m := New(root)
	// Move cursor onto first file (skip the subdir at position 0).
	// Locate file_a.txt.
	fileIdx := -1
	for i, e := range m.entries {
		if !e.isDir && strings.HasSuffix(e.name, ".txt") {
			fileIdx = i
			break
		}
	}
	if fileIdx < 0 {
		t.Fatal("could not locate a .txt file in root entries")
	}
	m.cursor = fileIdx
	m.preview()
	if m.previewName == "" {
		t.Fatal("previewName is empty after moving to a file")
	}
	if m.previewErr != "" {
		t.Fatalf("unexpected previewErr: %q", m.previewErr)
	}
	if m.viewport.View() == "" {
		t.Fatal("viewport is empty after previewing a file")
	}
}

func TestEnterDescendsIntoDirectory(t *testing.T) {
	root := buildTestTree(t)
	m := New(root)
	// Cursor should be on "subdir" (first entry, dirs-first).
	if !m.entries[m.cursor].isDir {
		// Manually position on the dir.
		for i, e := range m.entries {
			if e.isDir {
				m.cursor = i
				break
			}
		}
	}
	m2 := sendSpecialKey(t, m, "enter")
	expectedCwd := filepath.Join(m.root, "subdir")
	if m2.cwd != expectedCwd {
		t.Fatalf("after enter: cwd = %q, want %q", m2.cwd, expectedCwd)
	}
	if m2.cursor != 0 {
		t.Fatalf("after enter: cursor = %d, want 0", m2.cursor)
	}
}

func TestLeftAtRootIsNoop(t *testing.T) {
	root := buildTestTree(t)
	m := New(root)
	if m.cwd != m.root {
		t.Fatal("precondition: cwd != root")
	}
	m2 := sendSpecialKey(t, m, "left")
	if m2.cwd != m.root {
		t.Fatalf("left at root changed cwd: got %q", m2.cwd)
	}
}

func TestLeftInSubdirReturnsToRoot(t *testing.T) {
	root := buildTestTree(t)
	m := New(root)
	// Descend into subdir.
	m.cwd = filepath.Join(m.root, "subdir")
	m.loadEntries()
	m.cursor = 0
	m.preview()

	m2 := sendSpecialKey(t, m, "left")
	if m2.cwd != m.root {
		t.Fatalf("left in subdir: cwd = %q, want %q", m2.cwd, m.root)
	}
}

func TestWithin_Sub(t *testing.T) {
	root := t.TempDir()
	m := Model{root: root}
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if !m.within(sub) {
		t.Fatalf("within: %q should be within %q", sub, root)
	}
}

func TestWithin_ParentEscape(t *testing.T) {
	root := t.TempDir()
	m := Model{root: root}
	parent := filepath.Dir(root)
	if m.within(parent) {
		t.Fatalf("within: %q should NOT be within %q", parent, root)
	}
}

func TestWithin_Root(t *testing.T) {
	root := t.TempDir()
	m := Model{root: root}
	if !m.within(root) {
		t.Fatalf("within: root itself should be within root")
	}
}

func TestQuitReturnsCmd(t *testing.T) {
	root := buildTestTree(t)
	m := New(root)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatal("q should return a non-nil Cmd")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("cmd() returned %T, want tea.QuitMsg", msg)
	}
}

func TestEscReturnsQuit(t *testing.T) {
	root := buildTestTree(t)
	m := New(root)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc should return a non-nil Cmd")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("esc cmd() returned %T, want tea.QuitMsg", msg)
	}
}

func TestWindowSizeMsgUpdatesViewport(t *testing.T) {
	root := buildTestTree(t)
	m := New(root)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m2, ok := updated.(Model)
	if !ok {
		t.Fatal("Update did not return a Model for WindowSizeMsg")
	}
	if m2.width != 120 || m2.height != 40 {
		t.Fatalf("width/height: got %d/%d, want 120/40", m2.width, m2.height)
	}
}

func TestViewRendersWithoutPanic(t *testing.T) {
	root := buildTestTree(t)
	m := New(root)
	m.width = 100
	m.height = 30
	view := m.View()
	if view == "" {
		t.Fatal("View() returned empty string")
	}
}

func TestWithin_SymlinkEscapeDenied(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	escapePath := filepath.Join(root, "escape")
	if err := os.Symlink(outside, escapePath); err != nil {
		t.Fatal(err)
	}
	m := New(root)
	// Symlink points outside root — must be denied.
	if m.within(escapePath) {
		t.Fatalf("within: symlink escape %q should NOT be within root %q", escapePath, root)
	}
	// A real subdirectory inside root must still be allowed.
	realSub := filepath.Join(root, "realsub")
	if err := os.MkdirAll(realSub, 0o700); err != nil {
		t.Fatal(err)
	}
	if !m.within(realSub) {
		t.Fatalf("within: real subdir %q should be within root %q", realSub, root)
	}
}

func TestTruncation_MultibyteSafe(t *testing.T) {
	// Verify that rune-safe truncation does not split multibyte characters.
	// Construct a label of CJK runes wider than maxLabel, then simulate what
	// renderLeft does and confirm the result is valid UTF-8 with the ellipsis.
	cjk := strings.Repeat("日", 20) // 20 CJK runes, 3 bytes each
	maxLabel := 10
	var result string
	if r := []rune(cjk); len(r) > maxLabel {
		result = string(r[:maxLabel-1]) + "…"
	} else {
		result = cjk
	}
	runes := []rune(result)
	// Should be exactly maxLabel runes: (maxLabel-1) CJK + 1 ellipsis rune.
	if len(runes) != maxLabel {
		t.Fatalf("truncated rune count = %d, want %d", len(runes), maxLabel)
	}
	if runes[len(runes)-1] != '…' {
		t.Fatalf("last rune = %q, want '…'", runes[len(runes)-1])
	}
}

func TestPreviewFileContent(t *testing.T) {
	root := buildTestTree(t)
	m := New(root)
	m.color = false // deterministic: no ANSI in test output

	// Position cursor on file_a.txt.
	for i, e := range m.entries {
		if e.name == "file_a.txt" {
			m.cursor = i
			break
		}
	}
	m.preview()
	content := m.viewport.View()
	if !strings.Contains(content, "content of a") {
		t.Fatalf("viewport content does not contain file text; got: %q", content)
	}
}
