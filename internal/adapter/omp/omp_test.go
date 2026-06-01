package omp

import (
	"reflect"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
)

func TestNew(t *testing.T) {
	a := New(nil)
	if a == nil || a.Name() != "omp" || a.DisplayName() != "Oh My Pi" {
		t.Fatalf("bad adapter: %#v", a)
	}
}

// omp's interactive TUI is launched with the bare `omp` command (no
// subcommand); the auto-approve flag rides in YoloArgs, not the candidate.
func TestNewUsesBareOmpCommand(t *testing.T) {
	got := New(nil)
	ta, ok := got.(*adapter.TmuxAgent)
	if !ok {
		t.Fatalf("expected *adapter.TmuxAgent, got %T", got)
	}
	if len(ta.Candidates) != 1 {
		t.Fatalf("candidates = %+v", ta.Candidates)
	}
	if got, want := ta.Candidates[0].Args, []string{"omp"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("candidate args = %v, want %v", got, want)
	}
}

// `--auto-approve` is omp's real auto-approve flag (verified via `omp --help`),
// appended in non-safe (yolo) mode so dispatched sessions don't pause for
// tool-call approval — matching claude/codex/copilot.
func TestYoloArgsUsesAutoApprove(t *testing.T) {
	got := New(nil)
	ta, ok := got.(*adapter.TmuxAgent)
	if !ok {
		t.Fatalf("expected *adapter.TmuxAgent, got %T", got)
	}
	if want := []string{"--auto-approve"}; !reflect.DeepEqual(ta.YoloArgs, want) {
		t.Fatalf("YoloArgs = %v, want %v", ta.YoloArgs, want)
	}
}

// On resume omp gets `-c`/`--continue` to continue its last session (same as
// opencode); a fresh dispatch adds nothing.
func TestSessionArgsAppendsContinueOnResume(t *testing.T) {
	if got := sessionArgs(adapter.ResumeRequest{ID: "x"}, "dispatched"); got != nil {
		t.Fatalf("dispatched should add no flags, got %v", got)
	}
	if got, want := sessionArgs(adapter.ResumeRequest{ID: "x"}, "resumed"), []string{"-c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("resumed got %v want %v", got, want)
	}
}

// New must wire the SessionArgs hook and SkipPromptOnResume so picking
// "Resume" continues omp's prior session instead of starting a fresh TUI.
func TestNewWiresSessionArgs(t *testing.T) {
	got := New(nil)
	ta, ok := got.(*adapter.TmuxAgent)
	if !ok {
		t.Fatalf("expected *adapter.TmuxAgent, got %T", got)
	}
	if ta.SessionArgs == nil {
		t.Fatal("expected SessionArgs to be wired")
	}
	if !ta.SkipPromptOnResume {
		t.Fatal("expected SkipPromptOnResume to be true")
	}
}
