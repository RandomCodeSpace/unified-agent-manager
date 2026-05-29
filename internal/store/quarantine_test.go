package store

import (
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// F43 — when quarantining a corrupt config fails (moveAside rename error), Load
// must NOT swallow the error and hand back DefaultConfig: returning a default
// would let the next Save overwrite the still-present original with no backup.
// Propagating the error makes the app refuse to clobber.
// ---------------------------------------------------------------------------

func TestLoadCorruptJSONQuarantineFailurePreservesOriginal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can rename within a read-only directory; cannot force a moveAside failure")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	corrupt := []byte("{bad json")
	if err := os.WriteFile(path, corrupt, 0o644); err != nil {
		t.Fatal(err)
	}
	// Pre-create the lock file so the flock OpenFile succeeds on an EXISTING file
	// even after the directory is made read-only: that isolates the failure to
	// the moveAside rename (which needs write perm on the directory), which is
	// exactly the path under test.
	if err := os.WriteFile(path+".lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Make the parent directory read-only so the moveAside rename fails.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	cfg, err := s.Load()
	if err == nil {
		t.Fatalf("Load must return an error when quarantine fails, got cfg=%+v", cfg)
	}
	// The original corrupt file must still be present (not silently lost) and
	// untouched, so a later run can recover or the user can inspect it.
	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("original file vanished after failed quarantine: %v", readErr)
	}
	if string(got) != string(corrupt) {
		t.Fatalf("original file mutated: %q", got)
	}
}

func TestLoadCorruptJSONQuarantineSuccessStartsFreshWithNonNilMap(t *testing.T) {
	// The success path must still return a normalized DefaultConfig (non-nil map)
	// and no error, preserving TestLoadCorruptJSONBacksUpAndStartsFresh behavior.
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	if err := os.WriteFile(path, []byte("{bad json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("Load on a writable dir must succeed: %v", err)
	}
	if cfg.Sessions == nil {
		t.Fatal("returned config has a nil Sessions map")
	}
	if cfg.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("schema = %d, want %d", cfg.SchemaVersion, CurrentSchemaVersion)
	}
}
