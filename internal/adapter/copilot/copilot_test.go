package copilot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/tmux"
)

func TestNew(t *testing.T) {
	a := New(nil)
	if a == nil || a.Name() != "copilot" || a.DisplayName() == "" {
		t.Fatalf("bad adapter: %#v", a)
	}
}

func TestAvailableRequiresCopilotBinary(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "gh"), "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", dir)

	a := New(nil)
	available, reason := a.Available()
	if available {
		t.Fatal("copilot adapter should not fall back to gh copilot")
	}
	if !strings.Contains(reason, "copilot not on PATH") {
		t.Fatalf("unexpected unavailable reason: %q", reason)
	}
}

func TestYoloModeUsesYoloFlag(t *testing.T) {
	a, logPath := newTestCopilotAdapter(t)
	_, err := a.Dispatch(context.Background(), adapter.DispatchRequest{Cwd: "/tmp", Mode: "yolo"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	logText := readLog(t, logPath)
	if !strings.Contains(logText, "copilot --yolo") {
		t.Fatalf("copilot yolo mode should use --yolo: %s", logText)
	}
	if strings.Contains(logText, "--autopilot") {
		t.Fatalf("copilot yolo mode should not use --autopilot: %s", logText)
	}
}

func TestDispatchSeedsCopilotSessionIDForFutureResume(t *testing.T) {
	a, logPath := newTestCopilotAdapter(t)
	sess, err := a.Dispatch(context.Background(), adapter.DispatchRequest{Prompt: "fix parser", Cwd: "/tmp", Mode: "yolo"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	logText := readLog(t, logPath)
	if !strings.Contains(logText, "--name "+sess.ID) {
		t.Fatalf("copilot dispatch should name the provider session with the UAM id: %s", logText)
	}
	if strings.Contains(logText, "--resume=") {
		t.Fatalf("initial dispatch should not try to resume a new Copilot session: %s", logText)
	}
	if !strings.Contains(logText, "send-keys") || !strings.Contains(logText, "fix parser") {
		t.Fatalf("initial dispatch should still send the prompt: %s", logText)
	}
}

func TestResumeUsesCopilotSessionIDAndDoesNotReplayPrompt(t *testing.T) {
	a, logPath := newTestCopilotAdapter(t)
	resumable, ok := a.(adapter.ResumableAdapter)
	if !ok {
		t.Fatal("copilot adapter should be resumable")
	}
	_, err := resumable.Resume(context.Background(), adapter.ResumeRequest{ID: "abc12345-dead-beef-cafe-0123456789ab", Prompt: "fix parser", Cwd: "/tmp", Mode: "yolo", TmuxSession: "uam-copilot-abc12345"})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}

	logText := readLog(t, logPath)
	if !strings.Contains(logText, "copilot --yolo --resume=abc12345-dead-beef-cafe-0123456789ab") {
		t.Fatalf("copilot resume should pass the persisted provider session id: %s", logText)
	}
	// `send-keys -t` is the prompt-injection form; the mouse copy/paste config
	// bindings legitimately contain `send-keys -X`/`-M`, so match the targeted
	// form (and the prompt text) rather than the bare substring.
	if strings.Contains(logText, "send-keys -t") || strings.Contains(logText, "fix parser") {
		t.Fatalf("resume should not replay the original prompt into the restored session: %s", logText)
	}
}

func newTestCopilotAdapter(t *testing.T) (adapter.AgentAdapter, string) {
	t.Helper()
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "copilot"), "#!/bin/sh\nexit 0\n")
	tmuxPath := filepath.Join(dir, "tmux")
	writeExecutable(t, tmuxPath, `#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_LOG"
exit 0
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	logPath := filepath.Join(dir, "tmux.log")
	t.Setenv("TMUX_LOG", logPath)

	client := tmux.New("uam")
	client.Executable = tmuxPath
	return New(client), logPath
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	logData, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(logData)
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}
