package opencode

import (
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
)

func TestNew(t *testing.T) {
	a := New(nil)
	if a == nil || a.Name() != "opencode" || a.DisplayName() == "" {
		t.Fatalf("bad adapter: %#v", a)
	}
}

// TestNoYoloArgs locks in that opencode is launched with no
// auto-approve / yolo flag. opencode's CLI does not expose one;
// passing an unrecognised flag like --auto-approve makes opencode
// print help and exit 0 instead of entering the TUI, killing the
// pane on dispatch. Regression guard.
func TestNoYoloArgs(t *testing.T) {
	got := New(nil)
	ta, ok := got.(*adapter.TmuxAgent)
	if !ok {
		t.Fatalf("expected *adapter.TmuxAgent, got %T", got)
	}
	if len(ta.YoloArgs) != 0 {
		t.Fatalf("opencode YoloArgs must be empty, got %v", ta.YoloArgs)
	}
}
