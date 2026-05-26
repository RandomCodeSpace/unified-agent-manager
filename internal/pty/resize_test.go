package pty

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResizeSetsWinsize(t *testing.T) {
	p, err := Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = p.Close() }()
	if err := Resize(p.Master, 120, 40); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	cols, rows, err := GetWinsize(p.Master)
	if err != nil {
		t.Fatalf("GetWinsize: %v", err)
	}
	if cols != 120 || rows != 40 {
		t.Fatalf("expected 120x40, got %dx%d", cols, rows)
	}
}

// TestResizeOnNonTTY confirms that the TIOCSWINSZ / TIOCGWINSZ helpers
// surface the wrapped errno when fed a non-tty fd. This also exercises
// the error-formatting branches in Resize and GetWinsize.
func TestResizeOnNonTTY(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "notatty"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer func() { _ = f.Close() }()
	if err := Resize(f, 80, 24); err == nil {
		t.Fatalf("expected Resize to fail on non-tty fd")
	}
	if _, _, err := GetWinsize(f); err == nil {
		t.Fatalf("expected GetWinsize to fail on non-tty fd")
	}
}
