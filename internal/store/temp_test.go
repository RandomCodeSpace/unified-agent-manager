package store

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// F45 — the atomic write must use a randomly-suffixed temp file in the SAME
// directory (os.CreateTemp(filepath.Dir(path), ...)). The old fixed
// "<path>.tmp.<pid>" name had no O_EXCL/random suffix, so a stale orphan from a
// previously-killed run with the same PID could linger or be silently reused.
// ---------------------------------------------------------------------------

func TestSaveLeavesNoTempOrphan(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Save(DefaultConfig()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if name == "sessions.json" || name == "sessions.json.lock" {
			continue
		}
		t.Fatalf("unexpected leftover file after Save: %q", name)
	}
}

func TestSaveDoesNotUsePredictablePidTempName(t *testing.T) {
	// Pre-occupy the OLD predictable "<path>.tmp.<pid>" name with a DIRECTORY.
	// The legacy code opened that exact path with O_CREATE|O_TRUNC|O_WRONLY,
	// which fails on a directory, so Save would error. The fixed code uses
	// os.CreateTemp with a random suffix and never touches the predictable
	// name, so Save succeeds — proving the temp name is no longer predictable.
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	predictable := fmt.Sprintf("%s.tmp.%d", path, os.Getpid())
	if err := os.Mkdir(predictable, 0o700); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Save(DefaultConfig()); err != nil {
		t.Fatalf("Save must not rely on the predictable PID temp name: %v", err)
	}
	if _, err := s.Load(); err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
}
