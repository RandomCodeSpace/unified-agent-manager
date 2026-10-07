package web

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// A Task record written while the planner existed loads as an ordinary
// Task, and startup deletes the removed planner's files: board.db with its
// -wal and -shm files and lanes/, whose lane worktree its repository no
// longer lists. Other files beside them and the uam-plan-* branch stay.
func TestPlannerRecordsLoadAsOrdinaryTasks(t *testing.T) {
	st := openTestStore(t)
	root := filepath.Dir(st.Path())
	repo := gitRepoFixture(t)
	lane := filepath.Join(root, "lanes", "project", "1-abcdef12")
	gitIn(t, repo, "worktree", "add", "-q", "-b", "uam-plan-card", lane)
	if !strings.Contains(gitOutput(t, repo, "worktree", "list", "--porcelain"), lane) {
		t.Fatal("the lane worktree was not added")
	}
	for _, name := range []string{"board.db", "board.db-wal", "board.db-shm", "board.db.bak"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	id, now := mustUUID(t), time.Now().UTC()
	var web store.WebState
	if err := json.Unmarshal([]byte(`{"turn":"completed","stage":"settled","settled_at":"`+now.Format(time.RFC3339)+`","retired":"its lane was removed"}`), &web); err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(cfg *store.Config) error {
		cfg.Sessions[store.Key("fake", id)] = store.SessionRecord{
			ID: id, Agent: "fake", Name: "lane task", Mode: store.ModeSafe, Workdir: lane, CreatedAt: now, LastSeenAt: now,
			Status: store.StatusActive, Surface: store.SurfaceWeb, ProviderSessionID: "conv_lane", Web: &web,
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	m := startManager(t, st, agenttest.NewProvider("fake", allCaps))
	sum, err := m.Summary(id)
	if err != nil || sum.Stage != StageSettled || sum.Workdir != lane {
		t.Fatalf("summary = %+v, %v", sum, err)
	}
	if raw, _ := json.Marshal(sum); strings.Contains(string(raw), "retired") {
		t.Fatalf("summary JSON = %s", raw)
	}
	if sum, err = m.Reopen(id); err != nil || sum.Stage != "" {
		t.Fatalf("reopen = %+v, %v", sum, err)
	}
	for _, name := range []string{"board.db", "board.db-wal", "board.db-shm", "lanes"} {
		if _, err := os.Lstat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("%s is still there: %v", name, err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(root, "board.db.bak")); err != nil || string(data) != "old" {
		t.Fatalf("board.db.bak = %q, %v", data, err)
	}
	if list := gitOutput(t, repo, "worktree", "list", "--porcelain"); strings.Contains(list, lane) {
		t.Fatalf("the repository still lists the lane:\n%s", list)
	}
	if branches := gitOutput(t, repo, "branch", "--list", "uam-plan-*"); !strings.Contains(branches, "uam-plan-card") {
		t.Fatalf("branches = %q", branches)
	}
}

// A lanes or board.db symlink is refused and what it points at stays; a
// folder without the planner's files is left as it is.
func TestRemovePlannerFilesRefusesSymlinks(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	kept := filepath.Join(outside, "lanes", "project", "lane", "work.go")
	if err := os.MkdirAll(filepath.Dir(kept), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kept, []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "board.db"), []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"lanes", "board.db"} {
		if err := os.Symlink(filepath.Join(outside, name), filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	removePlannerFiles(context.Background(), dir)
	for _, name := range []string{"lanes", "board.db"} {
		if info, err := os.Lstat(filepath.Join(dir, name)); err != nil || info.Mode().Type() != os.ModeSymlink {
			t.Fatalf("%s = %v, %v", name, info, err)
		}
	}
	for _, path := range []string{kept, filepath.Join(outside, "board.db")} {
		if data, err := os.ReadFile(path); err != nil || string(data) != "kept" {
			t.Fatalf("%s = %q, %v", path, data, err)
		}
	}

	empty := t.TempDir()
	removePlannerFiles(context.Background(), empty)
	removePlannerFiles(context.Background(), filepath.Join(empty, "missing"))
	if entries, err := os.ReadDir(empty); err != nil || len(entries) != 0 {
		t.Fatalf("empty folder = %v, %v", entries, err)
	}
}
