package codex

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
	if a == nil || a.Name() != "codex" || a.DisplayName() == "" {
		t.Fatalf("bad adapter: %#v", a)
	}
}

// TestYoloArgs locks in codex's full-access flags exactly. A drift here
// silently changes the sandbox posture of dispatched sessions.
func TestYoloArgs(t *testing.T) {
	ta, ok := New(nil).(*adapter.Agent)
	if !ok {
		t.Fatalf("expected *adapter.Agent")
	}
	if got, want := ta.YoloArgs, []string{"--sandbox", "danger-full-access"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("YoloArgs = %v, want %v", got, want)
	}
}

// TestNewWiresSessionArgs asserts New installs the SessionArgs hook and
// SkipPromptOnResume. Without this wiring, picking "Resume" on a codex row
// would relaunch a fresh agent (no resume) AND re-fire the original prompt.
func TestNewWiresSessionArgs(t *testing.T) {
	ta, ok := New(nil).(*adapter.Agent)
	if !ok {
		t.Fatalf("expected *adapter.Agent")
	}
	if ta.SessionArgs == nil {
		t.Fatal("expected SessionArgs to be wired")
	}
	if !ta.SkipPromptOnResume {
		t.Fatal("expected SkipPromptOnResume to be true")
	}
}

func TestResumeKindRemainsHeuristicEvenWithUnrelatedStoredIdentity(t *testing.T) {
	ag := New(nil).(adapter.ResumeKindAdapter)
	if got := ag.ResumeKind(adapter.ResumeRequest{ProviderSessionID: "legacy-value"}); got != adapter.ResumeHeuristic {
		t.Fatalf("ResumeKind=%q, want heuristic", got)
	}
}

// TestResumeAppendsResumeLastAndDoesNotReplayPrompt: resuming an Exited codex
// row must use codex's `resume --last` and must NOT replay the original prompt
// into the restored session, nor pass the uam UUID.
func TestResumeAppendsResumeLastAndDoesNotReplayPrompt(t *testing.T) {
	a, be := newTestCodexAdapter(t)
	resumable, ok := a.(adapter.ResumableAdapter)
	if !ok {
		t.Fatal("codex adapter should be resumable")
	}
	_, err := resumable.Resume(context.Background(), adapter.ResumeRequest{ID: "abc12345-dead-beef-cafe-0123456789ab", Prompt: "fix parser", Cwd: "/tmp", Mode: "yolo", SessionName: "uam-codex-abc12345"})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	argv := be.CommandLog()
	if !strings.Contains(argv, "codex --sandbox danger-full-access resume --last") {
		t.Fatalf("codex resume should append resume --last: %s", argv)
	}
	// The uam UUID may appear in the UAM_ID env var, but must never be passed
	// as a flag argument to codex (no resume <uuid> / --resume <uuid>).
	if strings.Contains(argv, "resume --last abc12345-dead-beef-cafe-0123456789ab") ||
		strings.Contains(argv, "--resume") {
		t.Fatalf("codex resume must not pass the uam UUID as a flag arg: %s", argv)
	}
	if sends := be.CallsOf("send"); len(sends) != 0 {
		t.Fatalf("resume should not replay the original prompt: %+v", sends)
	}
}

// TestDispatchUnchanged_sendsPromptNoResume: dispatch keeps its byte-identical
// argv (no resume) and still sends the prompt.
func TestDispatchUnchanged_sendsPromptNoResume(t *testing.T) {
	a, be := newTestCodexAdapter(t)
	_, err := a.Dispatch(context.Background(), adapter.DispatchRequest{Prompt: "fix parser", Cwd: "/tmp", Mode: "yolo"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if argv := be.CommandLog(); strings.Contains(argv, "resume --last") {
		t.Fatalf("dispatch must not append resume --last: %s", argv)
	}
	sends := be.CallsOf("send")
	if len(sends) != 1 || sends[0].Text != "fix parser" {
		t.Fatalf("dispatch should send the prompt: %+v", sends)
	}
}

func newTestCodexAdapter(t *testing.T) (adapter.AgentAdapter, *adaptertest.Backend) {
	t.Helper()
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "codex"))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	be := &adaptertest.Backend{}
	return New(be), be
}

func writeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}
