package adapter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/tmux"
)

// C2-4 — a relative --cwd must be resolved to an absolute path exactly once in
// startSession, before BOTH CreateSession (the tmux -c arg) and the returned
// Session.Cwd. Otherwise the relative path is persisted verbatim and a later
// resume re-resolves it against uam's process cwd, launching the agent in the
// wrong directory.
func TestTmuxAgentDispatchRelativeCwdIsNormalized(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "fakeagent"), "#!/bin/sh\nexit 0\n")
	tmuxPath := filepath.Join(dir, "tmux")
	writeExecutable(t, tmuxPath, `#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_LOG"
exit 0
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	logPath := filepath.Join(dir, "tmux.log")
	t.Setenv("TMUX_LOG", logPath)

	// Run from a known directory so filepath.Abs has a deterministic base.
	base := t.TempDir()
	t.Chdir(base)
	wantAbs := filepath.Join(base, "sub", "project")

	client := tmux.New("uam")
	client.Executable = tmuxPath
	ag := NewTmuxAgent("fake", "Fake Agent", []CommandCandidate{{Display: "fakeagent", Args: []string{"fakeagent"}}}, []string{"--yolo"}, client)

	sess, err := ag.Dispatch(context.Background(), DispatchRequest{Prompt: "hi", Cwd: "sub/project", Mode: "yolo"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if sess.Cwd != wantAbs {
		t.Fatalf("returned Session.Cwd = %q, want absolute %q", sess.Cwd, wantAbs)
	}
	logText := func() string { b, _ := os.ReadFile(logPath); return string(b) }()
	if !strings.Contains(logText, "-c "+wantAbs) {
		t.Fatalf("tmux new-session -c arg must be the absolute cwd %q, log: %s", wantAbs, logText)
	}
	if strings.Contains(logText, "-c sub/project") {
		t.Fatalf("relative cwd must not reach tmux verbatim, log: %s", logText)
	}
}
