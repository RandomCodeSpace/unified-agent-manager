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
