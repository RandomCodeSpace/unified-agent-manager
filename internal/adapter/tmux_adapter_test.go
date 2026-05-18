package adapter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/tmux"
)

func TestTmuxAgentLifecycleWithFakeTmux(t *testing.T) {
	ag, logPath := setupLifecycleAgent(t)
	assertAgentAvailable(t, ag)
	assertAgentDispatchAndList(t, ag)
	assertAgentInteractions(t, ag)
	assertTmuxLifecycleLog(t, logPath)
}

func setupLifecycleAgent(t *testing.T) (*TmuxAgent, string) {
	t.Helper()
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "fakeagent"), "#!/bin/sh\nexit 0\n")
	tmuxPath := filepath.Join(dir, "tmux")
	writeExecutable(t, tmuxPath, `#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_LOG"
case "$*" in
  *"list-sessions"*) echo "uam-fake-abc12345|1710000000|0|1|/tmp/repo|fakeagent" ;;
  *"capture-pane"*) printf 'Thinking...\ncreated https://github.com/o/r/pull/7\n' ;;
esac
exit 0
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	logPath := filepath.Join(dir, "tmux.log")
	t.Setenv("TMUX_LOG", logPath)
	client := tmux.New("uam")
	client.Executable = tmuxPath
	return NewTmuxAgent("fake", "Fake Agent", []CommandCandidate{{Display: "fakeagent", Args: []string{"fakeagent"}}}, []string{"--yolo"}, DefaultPatterns("fake"), client), logPath
}

func assertAgentAvailable(t *testing.T, ag *TmuxAgent) {
	t.Helper()
	if ok, reason := ag.Available(); !ok || reason != "" {
		t.Fatalf("Available = %v %q", ok, reason)
	}
	if ag.Name() != "fake" || ag.DisplayName() != "Fake Agent" {
		t.Fatalf("names wrong")
	}
}

func assertAgentDispatchAndList(t *testing.T, ag *TmuxAgent) {
	t.Helper()
	sess, err := ag.Dispatch(context.Background(), DispatchRequest{Prompt: "hello", Cwd: "/tmp", Mode: "yolo"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if sess.AgentType != "fake" || sess.State != Working || sess.TmuxSession == "" {
		t.Fatalf("bad session: %+v", sess)
	}
	list, err := ag.List(context.Background())
	if err != nil || len(list) != 1 {
		t.Fatalf("List len=%d err=%v", len(list), err)
	}
	if list[0].PR == nil || list[0].PR.Number != 7 || list[0].Activity == "" {
		t.Fatalf("bad classified list: %+v", list[0])
	}
}

func assertAgentInteractions(t *testing.T, ag *TmuxAgent) {
	t.Helper()
	peek, err := ag.Peek(context.Background(), "abc12345")
	if err != nil || !strings.Contains(peek.TailText, "Thinking") {
		t.Fatalf("Peek: %+v %v", peek, err)
	}
	for _, action := range []func() error{
		func() error { return ag.Reply(context.Background(), "abc12345", "ok") },
		func() error { _, err := ag.Attach("abc12345"); return err },
		func() error { return ag.Stop(context.Background(), "abc12345") },
		func() error { return ag.Rename(context.Background(), "abc12345", "name") },
	} {
		if err := action(); err != nil {
			t.Fatal(err)
		}
	}
	if ch, err := ag.Subscribe(context.Background()); err != nil || ch != nil {
		t.Fatalf("Subscribe = %v %v", ch, err)
	}
	assertChangedRecently(t, ag)
}

func assertChangedRecently(t *testing.T, ag *TmuxAgent) {
	t.Helper()
	if !ag.changedRecently("pane", "a", time.Minute) {
		t.Fatal("first change should be recent")
	}
	if !ag.changedRecently("pane", "a", time.Minute) {
		t.Fatal("same hash inside window should be recent")
	}
}

func assertTmuxLifecycleLog(t *testing.T, logPath string) {
	t.Helper()
	logData, _ := os.ReadFile(logPath)
	logText := string(logData)
	for _, want := range []string{"new-session", "send-keys", "kill-session"} {
		if !strings.Contains(logText, want) {
			t.Fatalf("log missing %s: %s", want, logData)
		}
	}
	if strings.Contains(logText, "exec bash") {
		t.Fatalf("agent exit should terminate tmux session, log should not keep a fallback shell: %s", logData)
	}
}

func TestTmuxAgentResumeUsesPersistedMetadata(t *testing.T) {
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

	client := tmux.New("uam")
	client.Executable = tmuxPath
	ag := NewTmuxAgent("fake", "Fake Agent", []CommandCandidate{{Display: "fakeagent", Args: []string{"fakeagent"}}}, []string{"--yolo"}, DefaultPatterns("fake"), client)
	sess, err := ag.Resume(context.Background(), ResumeRequest{ID: "abc12345-dead-beef-cafe-0123456789ab", Name: "bugfix", Prompt: "fix parser", Cwd: "/tmp/project", Mode: "yolo", TmuxSession: "uam-fake-abc12345"})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if sess.ID != "abc12345-dead-beef-cafe-0123456789ab" || sess.DisplayName != "bugfix" || sess.Prompt != "fix parser" || sess.Cwd != "/tmp/project" || sess.TmuxSession != "uam-fake-abc12345" || sess.ProcAlive != Alive {
		t.Fatalf("resumed session did not preserve metadata: %+v", sess)
	}
	logData, _ := os.ReadFile(logPath)
	logText := string(logData)
	for _, want := range []string{"new-session", "uam-fake-abc12345", "/tmp/project", "fakeagent --yolo", "send-keys", "fix parser"} {
		if !strings.Contains(logText, want) {
			t.Fatalf("resume log missing %q: %s", want, logText)
		}
	}
}

func TestTmuxAgentDispatchWithoutPromptSkipsSendKeys(t *testing.T) {
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

	client := tmux.New("uam")
	client.Executable = tmuxPath
	ag := NewTmuxAgent("fake", "Fake Agent", []CommandCandidate{{Display: "fakeagent", Args: []string{"fakeagent"}}}, []string{"--yolo"}, DefaultPatterns("fake"), client)
	sess, err := ag.Dispatch(context.Background(), DispatchRequest{Cwd: "/tmp", Mode: "yolo"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if sess.DisplayName != "untitled" {
		t.Fatalf("DisplayName=%q", sess.DisplayName)
	}
	logData, _ := os.ReadFile(logPath)
	if strings.Contains(string(logData), "send-keys") {
		t.Fatalf("empty prompt should not be sent: %s", logData)
	}
}

func TestTmuxAgentUnavailable(t *testing.T) {
	ag := NewTmuxAgent("missing", "Missing", []CommandCandidate{{Display: "definitely-missing", Args: []string{"definitely-missing-uam-test"}}}, nil, DefaultPatterns("missing"), nil)
	if ok, reason := ag.Available(); ok || reason == "" {
		t.Fatalf("Available = %v %q", ok, reason)
	}
	if _, err := ag.Dispatch(context.Background(), DispatchRequest{}); err == nil {
		t.Fatal("expected dispatch error")
	}
}

func TestDetectAdditionalBranches(t *testing.T) {
	p := DefaultPatterns("claude")
	if state, _, _ := ClassifyPane([]string{"Error: boom"}, "claude", true, false, p); state != Failed {
		t.Fatalf("want failed got %s", state)
	}
	if state, _, _ := ClassifyPane([]string{"plain", ">"}, "claude", true, false, p); state != Completed {
		t.Fatalf("want completed got %s", state)
	}
	if state, _, _ := ClassifyPane([]string{"plain"}, "claude", true, false, p); state != Completed {
		t.Fatalf("want completed fallback got %s", state)
	}
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}
