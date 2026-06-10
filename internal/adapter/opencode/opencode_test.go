package opencode

import (
	"reflect"
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
	ta, ok := got.(*adapter.Agent)
	if !ok {
		t.Fatalf("expected *adapter.Agent, got %T", got)
	}
	if len(ta.YoloArgs) != 0 {
		t.Fatalf("opencode YoloArgs must be empty, got %v", ta.YoloArgs)
	}
}

// TestSessionArgsAppendsContinueOnResume asserts the SessionArgs
// hook returns opencode's `-c` (continue) on resume and nothing on
// dispatch. opencode has no flag for presetting its session ID at
// dispatch, so id-based resume isn't possible at v0.1.x; `-c`
// resumes the most recent session in the current cwd.
func TestSessionArgsAppendsContinueOnResume(t *testing.T) {
	if got := sessionArgs(adapter.ResumeRequest{ID: "x"}, "dispatched"); got != nil {
		t.Fatalf("dispatched should add no flags, got %v", got)
	}
	if got, want := sessionArgs(adapter.ResumeRequest{ID: "x"}, "resumed"), []string{"-c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("resumed got %v want %v", got, want)
	}
}

// TestNewWiresSessionArgs asserts New installs the SessionArgs hook
// and SkipPromptOnResume. Without this wiring, picking "Resume" on
// an opencode row would re-launch opencode with no continuation
// flag, starting a fresh TUI instead of resuming the prior session.
func TestNewWiresSessionArgs(t *testing.T) {
	got := New(nil)
	ta, ok := got.(*adapter.Agent)
	if !ok {
		t.Fatalf("expected *adapter.Agent, got %T", got)
	}
	if ta.SessionArgs == nil {
		t.Fatal("expected SessionArgs to be wired")
	}
	if !ta.SkipPromptOnResume {
		t.Fatal("expected SkipPromptOnResume to be true")
	}
}

// A recorded provider session id must resume that exact opencode session
// (--session ses_...) instead of the project's most recent (-c).
func TestResumeTargetsExactSessionWhenIDKnown(t *testing.T) {
	ag, ok := New(nil).(*adapter.Agent)
	if !ok {
		t.Fatalf("expected *adapter.Agent")
	}
	got := ag.SessionArgs(adapter.ResumeRequest{ProviderSessionID: "ses_2132323b6ffe"}, "resumed")
	if len(got) != 2 || got[0] != "--session" || got[1] != "ses_2132323b6ffe" {
		t.Fatalf("resume args = %v, want exact --session", got)
	}
	if got := ag.SessionArgs(adapter.ResumeRequest{}, "resumed"); len(got) != 1 || got[0] != "-c" {
		t.Fatalf("resume args without id = %v, want -c fallback", got)
	}
}
