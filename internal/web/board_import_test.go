package web

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/board"
)

// importSource copies the board package's schema v11 import fixture (two
// source projects, website with 7 tasks and api with 5) into a fresh
// directory.
func importSource(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "board", "testdata", "import-v11", "kb.db"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kb.db"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func openBoard(t *testing.T) *board.Store {
	t.Helper()
	bs, err := board.Open(filepath.Join(t.TempDir(), board.FileName), board.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bs.Close() })
	return bs
}

// gitProject adds a fresh git work tree as a Project named name.
func gitProject(t *testing.T, m *Manager, name string) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	p, err := m.AddProject(dir, name)
	if err != nil || p.NoGit != "" {
		t.Fatalf("AddProject(%s) = %+v, %v", name, p, err)
	}
	return p.ID
}

func boardCount(t *testing.T, bs *board.Store, projectID string) int {
	t.Helper()
	snap, err := bs.Board(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	return len(snap.Cards)
}

func TestImportBoardMapsSourceProjectsByName(t *testing.T) {
	m, _, _ := newTestManager(t)
	bs := openBoard(t)
	website := gitProject(t, m, "website")
	if _, err := m.AddProject(t.TempDir(), "api"); err != nil {
		t.Fatal(err)
	}
	r, err := m.ImportBoard(context.Background(), bs, importSource(t))
	if err != nil {
		t.Fatal(err)
	}
	// api names a Project without git, so its cards wait in Unassigned.
	if r.Imported != 12 || r.Unassigned != 5 || boardCount(t, bs, website) != 7 || boardCount(t, bs, "") != 5 {
		t.Fatalf("report = %+v; %d cards in website, %d Unassigned", r, boardCount(t, bs, website), boardCount(t, bs, ""))
	}
}

func TestImportBoardLeavesAmbiguousNamesUnassigned(t *testing.T) {
	m, _, _ := newTestManager(t)
	bs := openBoard(t)
	gitProject(t, m, "website")
	gitProject(t, m, "website")
	gitProject(t, m, "API")
	r, err := m.ImportBoard(context.Background(), bs, importSource(t))
	if err != nil {
		t.Fatal(err)
	}
	if r.Imported != 12 || r.Unassigned != 12 {
		t.Fatalf("report = %+v, want every card Unassigned: website is shared and API differs in case", r)
	}
}
