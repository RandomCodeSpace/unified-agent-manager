package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// droppedFixture is a state file of the given schema with one valid record
// and one that fails validation; both carry a field this version does not
// model.
func droppedFixture(t *testing.T, dir string, schema int) string {
	t.Helper()
	path := filepath.Join(dir, "sessions.json")
	data := fmt.Sprintf(`{
  "schema_version": %d,
  "default_agent": "claude",
  "sessions": {
    "claude:goodone": {"id": "goodone", "agent": "claude", "status": "active", "name": "keep me", "future_field": 1},
    "claude:bad;id": {"id": "bad;id", "agent": "claude", "status": "active", "name": "only copy", "future_field": {"n": 2}}
  }
}`, schema)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A record dropped at load must outlive the save that removes it from the
// state file: it is kept, as it was on disk, in a file beside it, written
// once however often the file is loaded, and the valid records are untouched.
func TestLoadKeepsDroppedRecordsBesideTheFile(t *testing.T) {
	var logs bytes.Buffer
	previous := log.SetLogger(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { log.SetLogger(previous) })
	path := droppedFixture(t, t.TempDir(), CurrentSchemaVersion)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		cfg, err := s.Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if _, ok := cfg.Sessions["claude:bad;id"]; ok || cfg.Sessions["claude:goodone"].Name != "keep me" {
			t.Fatalf("sessions after load = %+v", cfg.Sessions)
		}
	}
	// A second Store on the path (another process) must not write a second copy.
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Update(func(cfg *Config) error { cfg.DefaultAgent = "copilot"; return nil }); err != nil {
		t.Fatalf("update: %v", err)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "bad;id") || !strings.Contains(string(saved), `"future_field": 1`) || !strings.Contains(string(saved), "keep me") {
		t.Fatalf("saved file = %s", saved)
	}
	kept, _ := filepath.Glob(path + ".dropped.*")
	if len(kept) != 1 {
		t.Fatalf("kept files = %v, want one", kept)
	}
	if info, err := os.Stat(kept[0]); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("kept file mode = %v, %v", info, err)
	}
	raw, err := os.ReadFile(kept[0])
	if err != nil {
		t.Fatal(err)
	}
	var records map[string]map[string]any
	if err := json.Unmarshal(raw, &records); err != nil {
		t.Fatalf("kept file is not JSON: %v\n%s", err, raw)
	}
	bad := records["claude:bad;id"]
	if len(records) != 1 || bad["id"] != "bad;id" || bad["name"] != "only copy" || fmt.Sprint(bad["future_field"]) != "map[n:2]" {
		t.Fatalf("kept records = %s", raw)
	}
	// The log names the file and never quotes a record.
	if !strings.Contains(logs.String(), kept[0]) || strings.Contains(logs.String(), "only copy") {
		t.Fatalf("logs = %s", logs.String())
	}
	// Nothing is left to drop, so a later load keeps nothing more.
	if _, err := s.Load(); err != nil {
		t.Fatal(err)
	}
	if again, _ := filepath.Glob(path + ".dropped.*"); len(again) != 1 {
		t.Fatalf("kept files after the save = %v", again)
	}
	if tmp, _ := filepath.Glob(path + ".tmp.*"); len(tmp) != 0 {
		t.Fatalf("temp files left = %v", tmp)
	}
}

// When the dropped records cannot be kept, the load fails instead of handing
// back a state whose next save would remove the only copy.
func TestLoadFailsWhenDroppedRecordsCannotBeKept(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write to a read-only directory")
	}
	dir := t.TempDir()
	path := droppedFixture(t, dir, CurrentSchemaVersion)
	before, _ := os.ReadFile(path)
	// The lock file must exist before the directory turns read-only.
	if err := os.WriteFile(path+".lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if cfg, err := s.Load(); err == nil {
		t.Fatalf("load = %+v, want an error", cfg.Sessions)
	}
	if err := s.Update(func(*Config) error { return nil }); err == nil {
		t.Fatal("update saved without keeping the dropped records")
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Fatalf("state file changed: %s", after)
	}
}

// A file from a newer uam is read-only, so nothing can remove its records
// and no copy is written.
func TestLoadNewerSchemaKeepsNoDroppedCopy(t *testing.T) {
	path := droppedFixture(t, t.TempDir(), CurrentSchemaVersion+1)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := s.Load()
	if err != nil || !cfg.ReadOnly {
		t.Fatalf("load = %v, read-only %v", err, cfg.ReadOnly)
	}
	if _, ok := cfg.Sessions["claude:bad;id"]; ok {
		t.Fatal("the invalid record was not dropped")
	}
	if kept, _ := filepath.Glob(path + ".dropped.*"); len(kept) != 0 {
		t.Fatalf("kept files = %v", kept)
	}
}
