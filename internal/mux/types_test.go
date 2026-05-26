package mux

import (
	"testing"
	"time"
)

func TestSpawnSpecZeroDefaults(t *testing.T) {
	s := SpawnSpec{}
	if s.Cols != 0 || s.Rows != 0 || s.Scrollback != 0 {
		t.Fatalf("expected zero defaults, got cols=%d rows=%d scrollback=%d", s.Cols, s.Rows, s.Scrollback)
	}
}

func TestSessionHandleStringRoundtrip(t *testing.T) {
	h := SessionHandle("uam-claude-abc12345")
	if string(h) != "uam-claude-abc12345" {
		t.Fatalf("handle should be a string alias")
	}
}

func TestPaneCaptureLinesPreserved(t *testing.T) {
	pc := PaneCapture{Lines: []string{"a", "b"}, PaneCmd: "claude", PanePID: 42, CapturedAt: time.Unix(1, 0)}
	if len(pc.Lines) != 2 || pc.PanePID != 42 || pc.PaneCmd != "claude" {
		t.Fatalf("PaneCapture fields not preserved: %+v", pc)
	}
}

func TestEventKindConstants(t *testing.T) {
	if EventOutput == "" || EventExit == "" || EventResize == "" {
		t.Fatalf("EventKind constants must be non-empty")
	}
	if EventOutput == EventExit || EventOutput == EventResize || EventExit == EventResize {
		t.Fatalf("EventKind constants must be distinct")
	}
}
