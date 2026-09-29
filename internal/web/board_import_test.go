package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
	return importFixture(t, "import-v11")
}

// importFixture copies the board package's import fixture name into a
// fresh directory.
func importFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "board", "testdata", name, "kb.db"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kb.db"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// plannerStore turns m's planner on and returns its store, for reading
// what an import wrote.
func plannerStore(t *testing.T, m *Manager) *board.Store {
	t.Helper()
	on := true
	if _, err := m.UpdateSettings(SettingsPatch{Planner: &on}); err != nil {
		t.Fatal(err)
	}
	return m.board.st
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
	bs := plannerStore(t, m)
	website := gitProject(t, m, "website")
	if _, err := m.AddProject(t.TempDir(), "api"); err != nil {
		t.Fatal(err)
	}
	r, err := m.ImportBoard(context.Background(), importSource(t))
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
	plannerStore(t, m)
	gitProject(t, m, "website")
	gitProject(t, m, "website")
	gitProject(t, m, "API")
	r, err := m.ImportBoard(context.Background(), importSource(t))
	if err != nil {
		t.Fatal(err)
	}
	if r.Imported != 12 || r.Unassigned != 12 {
		t.Fatalf("report = %+v, want every card Unassigned: website is shared and API differs in case", r)
	}
}

// POST /api/board/import answers with the report, and refuses with a status
// for each case: an empty or relative dir, a directory without a source, a
// source at another schema (422 import_schema), a source that kept changing
// (409 import_busy), and the planner off.
func TestImportRoute(t *testing.T) {
	f := newPlanner(t)
	body := func(dir string) string { return fmt.Sprintf(`{"dir":%q}`, dir) }
	var r board.ImportReport
	f.call(http.MethodPost, "/api/board/import", body(importSource(t)), http.StatusOK, &r)
	if r.Imported != 12 || r.Unassigned != 12 || r.Skipped == nil {
		t.Fatalf("report = %+v", r)
	}
	f.refused(http.MethodPost, "/api/board/import", body(""), http.StatusBadRequest, string(board.CodeInvalid))
	f.refused(http.MethodPost, "/api/board/import", body("relative/kb"), http.StatusBadRequest, string(board.CodeInvalid))
	f.refused(http.MethodPost, "/api/board/import", body(t.TempDir()), http.StatusNotFound, string(board.CodeNotFound))
	f.refused(http.MethodPost, "/api/board/import", body(importFixture(t, "import-v10")), http.StatusUnprocessableEntity, string(board.CodeImportSchema))
	// A source that keeps changing can't be staged from here; its refusal
	// maps like any board conflict.
	var busy *Error
	if !errors.As(boardError(&board.Error{Code: board.CodeImportBusy, Message: "busy"}), &busy) || busy.Status != http.StatusConflict || busy.Code != string(board.CodeImportBusy) {
		t.Fatalf("import_busy = %+v", busy)
	}
	f.call(http.MethodPatch, "/api/settings", `{"planner":false}`, http.StatusOK, nil)
	f.refused(http.MethodPost, "/api/board/import", body(importSource(t)), http.StatusConflict, codePlannerOff)
}
