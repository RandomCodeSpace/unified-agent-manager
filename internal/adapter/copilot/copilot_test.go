package copilot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/adaptertest"
)

func TestNew(t *testing.T) {
	a := New(nil)
	if a == nil || a.Name() != "copilot" || a.DisplayName() == "" {
		t.Fatalf("bad adapter: %#v", a)
	}
}

func TestResumeKindIsExactForLegacyRecordsWithoutProviderID(t *testing.T) {
	a := New(nil).(adapter.ResumeKindAdapter)
	if got := a.ResumeKind(adapter.ResumeRequest{ID: "abc12345"}); got != adapter.ResumeExact {
		t.Fatalf("ResumeKind=%q, want exact", got)
	}
}

func TestAvailableRequiresCopilotBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	a := New(nil)
	if ok, reason := a.Available(); ok || reason == "" {
		t.Fatalf("Available = %v %q, want unavailable with reason", ok, reason)
	}
}

func TestYoloModeUsesYoloFlag(t *testing.T) {
	a, be := newTestCopilotAdapter(t)
	_, err := a.Dispatch(context.Background(), adapter.DispatchRequest{Cwd: "/tmp", Mode: "yolo"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	argv := be.CommandLog()
	if !strings.Contains(argv, "copilot --yolo") {
		t.Fatalf("copilot yolo mode should use --yolo: %s", argv)
	}
	if strings.Contains(argv, "--autopilot") {
		t.Fatalf("copilot yolo mode should not use --autopilot: %s", argv)
	}
}

func TestDispatchSeedsCopilotSessionIDForFutureResume(t *testing.T) {
	a, be := newTestCopilotAdapter(t)
	sess, err := a.Dispatch(context.Background(), adapter.DispatchRequest{Prompt: "fix parser", Cwd: "/tmp", Mode: "yolo"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	argv := be.CommandLog()
	if !strings.Contains(argv, "--name "+sess.ID) {
		t.Fatalf("copilot dispatch should name the provider session with the UAM id: %s", argv)
	}
	if strings.Contains(argv, "--resume=") {
		t.Fatalf("initial dispatch should not try to resume a new Copilot session: %s", argv)
	}
	sends := be.CallsOf("send")
	if len(sends) != 1 || sends[0].Text != "fix parser" {
		t.Fatalf("initial dispatch should still send the prompt: %+v", sends)
	}
}

func TestResumeUsesCopilotSessionIDAndDoesNotReplayPrompt(t *testing.T) {
	a, be := newTestCopilotAdapter(t)
	resumable, ok := a.(adapter.ResumableAdapter)
	if !ok {
		t.Fatal("copilot adapter should be resumable")
	}
	_, err := resumable.Resume(context.Background(), adapter.ResumeRequest{ID: "abc12345-dead-beef-cafe-0123456789ab", Prompt: "fix parser", Cwd: "/tmp", Mode: "yolo", SessionName: "uam-copilot-abc12345"})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	argv := be.CommandLog()
	if !strings.Contains(argv, "copilot --yolo --resume=abc12345-dead-beef-cafe-0123456789ab") {
		t.Fatalf("copilot resume should pass the persisted provider session id: %s", argv)
	}
	if sends := be.CallsOf("send"); len(sends) != 0 {
		t.Fatalf("resume should not replay the original prompt into the restored session: %+v", sends)
	}
}

func newTestCopilotAdapter(t *testing.T) (adapter.AgentAdapter, *adaptertest.Backend) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "copilot"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	be := &adaptertest.Backend{}
	return New(be), be
}

// Dispatch must record the seeded session name as the provider session id so
// the store reflects exactly what --resume will target.
func TestDispatchRecordsProviderSessionID(t *testing.T) {
	a, _ := newTestCopilotAdapter(t)
	sess, err := a.Dispatch(context.Background(), adapter.DispatchRequest{Cwd: "/tmp", Mode: "yolo"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if sess.ProviderSessionID != sess.ID {
		t.Fatalf("ProviderSessionID = %q, want the uam id %q", sess.ProviderSessionID, sess.ID)
	}
}
