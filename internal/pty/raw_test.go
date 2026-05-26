package pty

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestMakeRawTogglesAndRestores(t *testing.T) {
	p, err := Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = p.Close() }()

	// Snapshot the slave's pre-raw termios so we can compare after restore.
	preFd := int(p.Slave.Fd())
	pre, err := unix.IoctlGetTermios(preFd, tcGetattr)
	if err != nil {
		t.Fatalf("IoctlGetTermios pre: %v", err)
	}

	restore, err := MakeRaw(p.Slave)
	if err != nil {
		t.Fatalf("MakeRaw: %v", err)
	}
	mid, err := unix.IoctlGetTermios(preFd, tcGetattr)
	if err != nil {
		t.Fatalf("IoctlGetTermios mid: %v", err)
	}
	// Raw mode disables ECHO and ICANON in Lflag.
	if mid.Lflag&unix.ECHO != 0 {
		t.Fatalf("expected ECHO cleared in raw mode")
	}
	if mid.Lflag&unix.ICANON != 0 {
		t.Fatalf("expected ICANON cleared in raw mode")
	}

	if err := restore(); err != nil {
		t.Fatalf("restore: %v", err)
	}
	post, err := unix.IoctlGetTermios(preFd, tcGetattr)
	if err != nil {
		t.Fatalf("IoctlGetTermios post: %v", err)
	}
	// After restore, the relevant Lflag bits should match the original.
	if pre.Lflag != post.Lflag {
		t.Fatalf("Lflag mismatch after restore: pre=%x post=%x", pre.Lflag, post.Lflag)
	}
}

func TestMakeRawOnNonTTY(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "notatty"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := MakeRaw(f); err == nil {
		t.Fatalf("expected MakeRaw to fail on non-tty fd")
	}
}
