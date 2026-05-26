package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestReadDaemonPidParses(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("UAM_RUNTIME_DIR", dir)
	path := filepath.Join(dir, "uam.pid")
	if err := os.WriteFile(path, []byte("12345\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pid, err := readDaemonPid()
	if err != nil {
		t.Fatalf("readDaemonPid: %v", err)
	}
	if pid != 12345 {
		t.Fatalf("expected 12345, got %d", pid)
	}
}

func TestReadDaemonPidErrorsOnMissing(t *testing.T) {
	t.Setenv("UAM_RUNTIME_DIR", t.TempDir())
	if _, err := readDaemonPid(); err == nil {
		t.Fatalf("expected error for missing pidfile")
	}
}

func TestReadDaemonPidErrorsOnBadContents(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("UAM_RUNTIME_DIR", dir)
	path := filepath.Join(dir, "uam.pid")
	if err := os.WriteFile(path, []byte("not-a-number\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readDaemonPid(); err == nil {
		t.Fatalf("expected parse error")
	}
}

func TestReadDaemonPidIsConsistentWithStrconv(t *testing.T) {
	// Sanity: the parser must round-trip values strconv.Itoa produces.
	dir := t.TempDir()
	t.Setenv("UAM_RUNTIME_DIR", dir)
	want := os.Getpid()
	if err := os.WriteFile(filepath.Join(dir, "uam.pid"), []byte(strconv.Itoa(want)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readDaemonPid()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("round-trip mismatch: got %d want %d", got, want)
	}
}
