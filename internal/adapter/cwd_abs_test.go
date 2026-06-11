package adapter

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/adaptertest"
)

// C2-4 — a relative --cwd must be resolved to an absolute path exactly once in
// startSession, before BOTH CreateSession and the returned Session.Cwd.
// Otherwise the relative path is persisted verbatim and a later resume
// re-resolves it against uam's process cwd, launching the agent in the wrong
// directory.
func TestAgentDispatchRelativeCwdIsNormalized(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "fakeagent"), "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	// Run from a known directory so filepath.Abs has a deterministic base.
	base := t.TempDir()
	t.Chdir(base)
	wantAbs := filepath.Join(base, "sub", "project")

	be := &adaptertest.Backend{}
	ag := NewAgent("fake", "Fake Agent", []CommandCandidate{{Display: "fakeagent", Args: []string{"fakeagent"}}}, []string{"--yolo"}, be)

	sess, err := ag.Dispatch(context.Background(), DispatchRequest{Prompt: "hi", Cwd: "sub/project", Mode: "yolo"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if sess.Cwd != wantAbs {
		t.Fatalf("returned Session.Cwd = %q, want absolute %q", sess.Cwd, wantAbs)
	}
	creates := be.CallsOf("create")
	if len(creates) != 1 || creates[0].Cwd != wantAbs {
		t.Fatalf("CreateSession cwd must be the absolute path %q, got %+v", wantAbs, creates)
	}
}
