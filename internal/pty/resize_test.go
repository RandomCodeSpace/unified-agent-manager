package pty

import "testing"

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
