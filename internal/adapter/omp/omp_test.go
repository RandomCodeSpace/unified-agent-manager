package omp

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/adaptertest"
)

func TestNew(t *testing.T) {
	a := New(nil)
	if a == nil || a.Name() != "omp" || a.DisplayName() != "Oh My Pi" {
		t.Fatalf("bad adapter: %#v", a)
	}
}

func newTestAgent(t *testing.T) (*adapter.Agent, *adaptertest.Backend) {
	t.Helper()
	bin := t.TempDir()
	path := filepath.Join(bin, "omp")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	be := &adaptertest.Backend{}
	return New(be).(*adapter.Agent), be
}

func TestManagedSessionsUseDistinctIsolatedDirectories(t *testing.T) {
	ag, be := newTestAgent(t)
	for range 2 {
		if _, err := ag.Dispatch(context.Background(), adapter.DispatchRequest{Cwd: "/tmp", Mode: "safe"}); err != nil {
			t.Fatal(err)
		}
	}
	creates := be.CallsOf("create")
	if len(creates) != 2 {
		t.Fatalf("creates=%d", len(creates))
	}
	first, second := strings.Join(creates[0].Command, " "), strings.Join(creates[1].Command, " ")
	if !strings.Contains(first, "--session-dir") || !strings.Contains(second, "--session-dir") || first == second {
		t.Fatalf("isolated argv: %q / %q", first, second)
	}
}

func TestResumeKindUsesExistingSafeDerivedDirectory(t *testing.T) {
	ag, be := newTestAgent(t)
	id := "aaaaaaaa-dead-beef-cafe-0123456789ab"
	dir, err := ensureSessionDir(id)
	if err != nil {
		t.Fatal(err)
	}
	if got := ag.ResumeKind(adapter.ResumeRequest{ID: id}); got != adapter.ResumeExact {
		t.Fatalf("kind=%q dir=%q", got, dir)
	}
	if _, err := ag.Resume(context.Background(), adapter.ResumeRequest{ID: id, Cwd: "/tmp", Mode: "safe", SessionName: "uam-omp-aaaaaaaa"}); err != nil {
		t.Fatal(err)
	}
	argv := strings.Join(be.CallsOf("create")[0].Command, " ")
	if !strings.Contains(argv, "--session-dir "+dir+" -c") {
		t.Fatalf("argv=%q", argv)
	}
}

func TestLegacyResumeWithoutDerivedDirectoryKeepsBareContinue(t *testing.T) {
	ag, be := newTestAgent(t)
	id := "aaaaaaaa-dead-beef-cafe-0123456789ab"
	if got := ag.ResumeKind(adapter.ResumeRequest{ID: id}); got != adapter.ResumeHeuristic {
		t.Fatalf("kind=%q", got)
	}
	if _, err := ag.Resume(context.Background(), adapter.ResumeRequest{ID: id, Cwd: "/tmp", Mode: "safe", SessionName: "uam-omp-aaaaaaaa"}); err != nil {
		t.Fatal(err)
	}
	argv := strings.Join(be.CallsOf("create")[0].Command, " ")
	if strings.Contains(argv, "--session-dir") || !strings.HasSuffix(argv, " -c") {
		t.Fatalf("argv=%q", argv)
	}
}

func TestResumeRejectsUnsafeExistingDerivedDirectory(t *testing.T) {
	ag, be := newTestAgent(t)
	id := "aaaaaaaa-dead-beef-cafe-0123456789ab"
	dir, err := sessionDir(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = ag.Resume(context.Background(), adapter.ResumeRequest{ID: id, Cwd: "/tmp", Mode: "safe", SessionName: "uam-omp-aaaaaaaa"})
	if err == nil {
		t.Fatal("unsafe derived directory accepted")
	}
	if len(be.CallsOf("create")) != 0 {
		t.Fatal("unsafe directory created backend session")
	}
}

func TestResumeRejectsSymlinkDerivedDirectory(t *testing.T) {
	ag, be := newTestAgent(t)
	id := "aaaaaaaa-dead-beef-cafe-0123456789ab"
	dir, err := sessionDir(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), dir); err != nil {
		t.Fatal(err)
	}
	_, err = ag.Resume(context.Background(), adapter.ResumeRequest{ID: id, Cwd: "/tmp", Mode: "safe", SessionName: "uam-omp-aaaaaaaa"})
	if err == nil {
		t.Fatal("symlink derived directory accepted")
	}
	if len(be.CallsOf("create")) != 0 {
		t.Fatal("symlink directory created backend session")
	}
}

func TestDispatchRejectsIntermediateStateSymlink(t *testing.T) {
	ag, be := newTestAgent(t)
	base := os.Getenv("XDG_STATE_HOME")
	if err := os.Symlink(t.TempDir(), filepath.Join(base, "uam")); err != nil {
		t.Fatal(err)
	}
	_, err := ag.Dispatch(context.Background(), adapter.DispatchRequest{Cwd: "/tmp", Mode: "safe"})
	if err == nil {
		t.Fatal("intermediate provider-state symlink accepted")
	}
	if len(be.CallsOf("create")) != 0 {
		t.Fatal("unsafe state path created backend session")
	}
}

func TestDispatchWarnsForWritableStateBase(t *testing.T) {
	ag, be := newTestAgent(t)
	base := os.Getenv("XDG_STATE_HOME")
	if err := os.Chmod(base, 0o772); err != nil {
		t.Fatal(err)
	}
	_, err := ag.Dispatch(context.Background(), adapter.DispatchRequest{Cwd: "/tmp", Mode: "safe"})
	if err != nil {
		t.Fatalf("group/other-writable XDG_STATE_HOME blocked: %v", err)
	}
	if len(be.CallsOf("create")) != 1 {
		t.Fatal("writable state base did not create backend session")
	}
}

// omp's interactive TUI is launched with the bare `omp` command (no
// subcommand); the auto-approve flag rides in YoloArgs, not the candidate.
func TestNewUsesBareOmpCommand(t *testing.T) {
	got := New(nil)
	ta, ok := got.(*adapter.Agent)
	if !ok {
		t.Fatalf("expected *adapter.Agent, got %T", got)
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
	ta, ok := got.(*adapter.Agent)
	if !ok {
		t.Fatalf("expected *adapter.Agent, got %T", got)
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
