package web

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

// A run killed mid-write leaves its temp files beside the store; the next
// start removes them and leaves every other file alone.
func TestStartRemovesTempFilesOfAKilledRun(t *testing.T) {
	st := openTestStore(t)
	dir := filepath.Dir(st.Path())
	orphans := []string{".token-usage-4269162127", ".worktrees-1", ".uam-counts-2", ".fetch-fuses-3", utilityLogFile + ".tmp.4"}
	kept := []string{"web-token-usage.json.bak.1", ".token-usage-notes", "notes.txt"}
	for _, name := range append(append([]string{}, orphans...), kept...) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	startManager(t, st, agenttest.NewProvider("fake", allCaps))
	for _, name := range orphans {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s survived the start: %v", name, err)
		}
	}
	for _, name := range kept {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("the start removed %s: %v", name, err)
		}
	}
}

// writeFileAtomic leaves the whole new file, owner-only, and no temp file.
func TestWriteFileAtomicReplacesWhole(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.json")
	for _, data := range []string{"first", "second"} {
		if err := writeFileAtomic(path, ".f-*", []byte(data)); err != nil {
			t.Fatal(err)
		}
		if got, err := os.ReadFile(path); err != nil || string(got) != data {
			t.Fatalf("file = %q, %v", got, err)
		}
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode())
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("dir holds %d files", len(entries))
	}
	if err := writeFileAtomic(filepath.Join(dir, "missing", "f"), ".f-*", nil); err == nil {
		t.Fatal("wrote into a missing directory")
	}
}
