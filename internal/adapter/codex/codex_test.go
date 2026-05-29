package codex

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/tmux"
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
	ta, ok := New(nil).(*adapter.TmuxAgent)
	if !ok {
		t.Fatalf("expected *adapter.TmuxAgent")
	}
	if got, want := ta.YoloArgs, []string{"--sandbox", "danger-full-access"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("YoloArgs = %v, want %v", got, want)
	}
}

// TestNewWiresSessionArgs asserts New installs the SessionArgs hook and
// SkipPromptOnResume. Without this wiring, picking "Resume" on a codex row
// would relaunch a fresh agent (no resume) AND re-fire the original prompt.
func TestNewWiresSessionArgs(t *testing.T) {
	ta, ok := New(nil).(*adapter.TmuxAgent)
	if !ok {
		t.Fatalf("expected *adapter.TmuxAgent")
	}
	if ta.SessionArgs == nil {
		t.Fatal("expected SessionArgs to be wired")
	}
	if !ta.SkipPromptOnResume {
		t.Fatal("expected SkipPromptOnResume to be true")
	}
}

// TestResumeAppendsResumeLastAndDoesNotReplayPrompt: resuming an Exited codex
// row must use codex's `resume --last` and must NOT replay the original prompt
// into the restored session, nor pass the uam UUID.
func TestResumeAppendsResumeLastAndDoesNotReplayPrompt(t *testing.T) {
	a, logPath := newTestCodexAdapter(t)
	resumable, ok := a.(adapter.ResumableAdapter)
	if !ok {
		t.Fatal("codex adapter should be resumable")
	}
	_, err := resumable.Resume(context.Background(), adapter.ResumeRequest{ID: "abc12345-dead-beef-cafe-0123456789ab", Prompt: "fix parser", Cwd: "/tmp", Mode: "yolo", TmuxSession: "uam-codex-abc12345"})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	logText := readLog(t, logPath)
	if !strings.Contains(logText, "codex --sandbox danger-full-access resume --last") {
		t.Fatalf("codex resume should append resume --last: %s", logText)
	}
	// The uam UUID may appear in the UAM_ID env var, but must never be passed
	// as a flag argument to codex (no resume <uuid> / --resume <uuid>).
	if strings.Contains(logText, "resume --last abc12345-dead-beef-cafe-0123456789ab") ||
		strings.Contains(logText, "--resume") {
		t.Fatalf("codex resume must not pass the uam UUID as a flag arg: %s", logText)
	}
	if strings.Contains(logText, "send-keys") || strings.Contains(logText, "fix parser") {
		t.Fatalf("resume should not replay the original prompt: %s", logText)
	}
}

// TestDispatchUnchanged_sendsPromptNoResume: dispatch keeps its byte-identical
// argv (no resume) and still sends the prompt.
func TestDispatchUnchanged_sendsPromptNoResume(t *testing.T) {
	a, logPath := newTestCodexAdapter(t)
	_, err := a.Dispatch(context.Background(), adapter.DispatchRequest{Prompt: "fix parser", Cwd: "/tmp", Mode: "yolo"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	logText := readLog(t, logPath)
	if strings.Contains(logText, "resume --last") {
		t.Fatalf("dispatch must not append resume --last: %s", logText)
	}
	if !strings.Contains(logText, "send-keys") || !strings.Contains(logText, "fix parser") {
		t.Fatalf("dispatch should send the prompt: %s", logText)
	}
}

func newTestCodexAdapter(t *testing.T) (adapter.AgentAdapter, string) {
	t.Helper()
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "codex"), "#!/bin/sh\nexit 0\n")
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
