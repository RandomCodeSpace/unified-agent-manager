package tmuxbackend

import (
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/mux"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/tmux"
)

// Compile-time assertion that *Backend satisfies mux.Backend.
var _ mux.Backend = (*Backend)(nil)

func TestNewWrapsClient(t *testing.T) {
	c := tmux.New("uam-test")
	b := New(c)
	if b == nil {
		t.Fatal("New returned nil")
	}
}

func TestNewNilClientUsesDefault(t *testing.T) {
	b := New(nil)
	if b == nil {
		t.Fatal("New(nil) should construct a Backend with a default client")
	}
}

func TestSpawnRoutesToCreateSession(t *testing.T) {
	// Integration-like: requires the real tmux client to round-trip.
	// Guard with the integration tag and skip otherwise.
	t.Skip("covered by integration test in backend_agent_integration_test.go")
}

func TestPaneCaptureLinesFromTmuxOutput(t *testing.T) {
	// Capture conversion: tmux returns a single string; we split on '\n'
	// into Lines while preserving order.
	raw := "line1\nline2\nline3"
	pc := mux.PaneCapture{Lines: splitCapture(raw), CapturedAt: time.Now()}
	if len(pc.Lines) != 3 || pc.Lines[0] != "line1" || pc.Lines[2] != "line3" {
		t.Fatalf("splitCapture failed: %+v", pc.Lines)
	}
}

func TestEnvSliceToMap(t *testing.T) {
	in := []string{"K1=v1", "K2=v2", "bogus", "=novalue"}
	out := envSliceToMap(in)
	if out["K1"] != "v1" || out["K2"] != "v2" {
		t.Fatalf("envSliceToMap basic: %+v", out)
	}
	if _, ok := out["bogus"]; ok {
		t.Fatalf("entries without '=' must be skipped")
	}
	if _, ok := out[""]; ok {
		t.Fatalf("leading-= entries must be skipped")
	}
	if envSliceToMap(nil) != nil {
		t.Fatalf("nil input should return nil map")
	}
}

func TestSplitCaptureEmpty(t *testing.T) {
	if splitCapture("") != nil {
		t.Fatalf("empty string should yield nil slice")
	}
	if got := splitCapture("only\n"); len(got) != 1 || got[0] != "only" {
		t.Fatalf("trailing newline must be trimmed: %+v", got)
	}
}
