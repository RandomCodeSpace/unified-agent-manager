package adapter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	return NewTmuxAgent("fake", "Fake Agent", []CommandCandidate{{Display: "fakeagent", Args: []string{"fakeagent"}}}, []string{"--yolo"}, client), logPath
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
	if sess.AgentType != "fake" || sess.State != Active || sess.TmuxSession == "" {
		t.Fatalf("bad session: %+v", sess)
	}
	list, err := ag.List(context.Background())
	if err != nil || len(list) != 1 {
		t.Fatalf("List len=%d err=%v", len(list), err)
	}
	if list[0].PR == nil || list[0].PR.Number != 7 {
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
}

func assertTmuxLifecycleLog(t *testing.T, logPath string) {
	t.Helper()
	logData, _ := os.ReadFile(logPath)
	logText := string(logData)
	for _, want := range []string{"set-option", "bind-key", "new-session", "send-keys", "kill-session"} {
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
	ag := NewTmuxAgent("fake", "Fake Agent", []CommandCandidate{{Display: "fakeagent", Args: []string{"fakeagent"}}}, []string{"--yolo"}, client)
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

// F19 — a resume/dispatch that creates the tmux session but then fails to send
// the prompt must roll back the live (prompt-less) session, otherwise it lingers
// as an orphan the store records as Exited/closed.
func TestStartSessionRollsBackTmuxOnSendLineFailure(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "fakeagent"), "#!/bin/sh\nexit 0\n")
	tmuxPath := filepath.Join(dir, "tmux")
	// send-keys fails; everything else (new-session, kill-session) succeeds.
	writeExecutable(t, tmuxPath, `#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_LOG"
case "$*" in
  *"send-keys"*) echo "boom" >&2; exit 1 ;;
esac
exit 0
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	logPath := filepath.Join(dir, "tmux.log")
	t.Setenv("TMUX_LOG", logPath)

	client := tmux.New("uam")
	client.Executable = tmuxPath
	ag := NewTmuxAgent("fake", "Fake Agent", []CommandCandidate{{Display: "fakeagent", Args: []string{"fakeagent"}}}, []string{"--yolo"}, client)
	_, err := ag.Dispatch(context.Background(), DispatchRequest{Prompt: "hello", Cwd: "/tmp", Mode: "yolo"})
	if err == nil {
		t.Fatal("expected dispatch error when send-keys fails")
	}
	logData, _ := os.ReadFile(logPath)
	logText := string(logData)
	if !strings.Contains(logText, "new-session") {
		t.Fatalf("session should have been created: %s", logText)
	}
	if !strings.Contains(logText, "kill-session") {
		t.Fatalf("send-keys failure must roll back the created session via kill-session: %s", logText)
	}
}

// F19 trap — if the rollback Kill itself fails, the caller must still see the
// original SendLine error (not the kill error).
func TestStartSessionReturnsSendLineErrorWhenRollbackKillAlsoFails(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "fakeagent"), "#!/bin/sh\nexit 0\n")
	tmuxPath := filepath.Join(dir, "tmux")
	writeExecutable(t, tmuxPath, `#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_LOG"
case "$*" in
  *"send-keys"*) echo "sendboom" >&2; exit 1 ;;
  *"kill-session"*) echo "killboom" >&2; exit 1 ;;
esac
exit 0
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	logPath := filepath.Join(dir, "tmux.log")
	t.Setenv("TMUX_LOG", logPath)

	client := tmux.New("uam")
	client.Executable = tmuxPath
	ag := NewTmuxAgent("fake", "Fake Agent", []CommandCandidate{{Display: "fakeagent", Args: []string{"fakeagent"}}}, []string{"--yolo"}, client)
	_, err := ag.Dispatch(context.Background(), DispatchRequest{Prompt: "hello", Cwd: "/tmp", Mode: "yolo"})
	if err == nil {
		t.Fatal("expected dispatch error")
	}
	if !strings.Contains(err.Error(), "sendboom") {
		t.Fatalf("error should surface the original send-keys failure, got: %v", err)
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
	ag := NewTmuxAgent("fake", "Fake Agent", []CommandCandidate{{Display: "fakeagent", Args: []string{"fakeagent"}}}, []string{"--yolo"}, client)
	sess, err := ag.Dispatch(context.Background(), DispatchRequest{Cwd: "/tmp", Mode: "yolo"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if sess.DisplayName != "tmp" {
		t.Fatalf("DisplayName=%q, want dir-derived name", sess.DisplayName)
	}
	logData, _ := os.ReadFile(logPath)
	if strings.Contains(string(logData), "send-keys") {
		t.Fatalf("empty prompt should not be sent: %s", logData)
	}
}

// F32 — target() must use tmux exact-match (`=` prefix) so a neighbour session
// whose name shares the truncated prefix is never hit by `-t`. Drive Stop/Peek
// through a fake tmux and assert the recorded `-t` token is exact-anchored.
func newTargetingAgent(t *testing.T) (*TmuxAgent, string) {
	t.Helper()
	dir := t.TempDir()
	tmuxPath := filepath.Join(dir, "tmux")
	writeExecutable(t, tmuxPath, `#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_LOG"
case "$*" in
  *"capture-pane"*) printf 'tail\n' ;;
esac
exit 0
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	logPath := filepath.Join(dir, "tmux.log")
	t.Setenv("TMUX_LOG", logPath)
	client := tmux.New("uam")
	client.Executable = tmuxPath
	return NewTmuxAgent("fake", "Fake Agent", []CommandCandidate{{Display: "fakeagent", Args: []string{"fakeagent"}}}, []string{"--yolo"}, client), logPath
}

func TestTargetUsesExactMatchForFullUUID(t *testing.T) {
	ag, logPath := newTargetingAgent(t)
	// A full-UUID id whose first 8 chars name the session.
	if err := ag.Stop(context.Background(), "abc12345-dead-beef-cafe-0123456789ab"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	logData, _ := os.ReadFile(logPath)
	logText := string(logData)
	if !strings.Contains(logText, "-t =uam-fake-abc12345") {
		t.Fatalf("kill must target the exact session, got: %s", logText)
	}
}

func TestTargetUsesExactMatchForCanonicalName(t *testing.T) {
	ag, logPath := newTargetingAgent(t)
	// A canonical (already uam-prefixed) name must also be exact-anchored so a
	// longer neighbour ("uam-fake-abc123450" etc.) is never matched by prefix.
	if _, err := ag.Peek(context.Background(), "uam-fake-abc12345"); err != nil {
		t.Fatalf("Peek: %v", err)
	}
	logData, _ := os.ReadFile(logPath)
	logText := string(logData)
	if !strings.Contains(logText, "-t =uam-fake-abc12345") {
		t.Fatalf("capture must target the exact session, got: %s", logText)
	}
}

func TestDisplayNameFromDir(t *testing.T) {
	if got := displayNameFromDir("/home/dev/projects/uam"); got != "uam" {
		t.Fatalf("dir name = %q, want uam", got)
	}
	if got := displayNameFromDir("/"); got != "untitled" {
		t.Fatalf("root dir name = %q, want untitled", got)
	}
	cwd, _ := os.Getwd()
	if got := displayNameFromDir("."); got != filepath.Base(cwd) {
		t.Fatalf("relative dir name = %q, want %q", got, filepath.Base(cwd))
	}
}

func TestTmuxAgentUnavailable(t *testing.T) {
	ag := NewTmuxAgent("missing", "Missing", []CommandCandidate{{Display: "definitely-missing", Args: []string{"definitely-missing-uam-test"}}}, nil, nil)
	if ok, reason := ag.Available(); ok || reason == "" {
		t.Fatalf("Available = %v %q", ok, reason)
	}
	if _, err := ag.Dispatch(context.Background(), DispatchRequest{}); err == nil {
		t.Fatal("expected dispatch error")
	}
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}
