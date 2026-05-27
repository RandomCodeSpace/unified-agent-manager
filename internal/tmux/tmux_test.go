package tmux

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientCommandsWithFakeTmux(t *testing.T) {
	c, logPath := setupFakeTmuxClient(t)
	if c.Socket != "uam" {
		t.Fatal(c.Socket)
	}
	assertCreateSessionCommand(t, c, logPath)
	assertClientReadCommands(t, c)
	assertClientWriteCommands(t, c)
	assertClientHelpers(t, c)
}

func setupFakeTmuxClient(t *testing.T) (*Client, string) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "tmux")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_LOG"
case "$*" in
  *"list-sessions"*) echo "uam-a|1|0|1|/tmp|bash" ;;
  *"capture-pane"*) echo "pane text" ;;
  *"has-session"*) exit 0 ;;
esac
exit 0
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	logPath := filepath.Join(dir, "log")
	t.Setenv("TMUX_LOG", logPath)
	c := New("uam")
	c.Executable = script
	return c, logPath
}

func assertCreateSessionCommand(t *testing.T, c *Client, logPath string) {
	t.Helper()
	if err := c.CreateSession(context.Background(), "uam-a", "/tmp", map[string]string{"A": "B"}, []string{"cmd", "arg with space"}); err != nil {
		t.Fatal(err)
	}
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	logText := string(logData)
	if strings.Contains(logText, " -e ") {
		t.Fatalf("CreateSession should not rely on tmux new-session -e because older tmux rejects it: %s", logText)
	}
	if !strings.Contains(logText, "env A=B cmd \"arg with space\"") {
		t.Fatalf("CreateSession should prefix the shell command with env assignments: %s", logText)
	}
}

func assertClientReadCommands(t *testing.T, c *Client) {
	t.Helper()
	list, err := c.List(context.Background())
	if err != nil || len(list) != 1 {
		t.Fatalf("list=%v err=%v", list, err)
	}
	cap, err := c.Capture(context.Background(), "uam-a", 0)
	if err != nil || !strings.Contains(cap, "pane text") {
		t.Fatalf("cap=%q err=%v", cap, err)
	}
}

func assertClientWriteCommands(t *testing.T, c *Client) {
	t.Helper()
	for _, action := range []func() error{
		func() error { return c.SendLine(context.Background(), "uam-a", "hello") },
		func() error { return c.Kill(context.Background(), "uam-a") },
	} {
		if err := action(); err != nil {
			t.Fatal(err)
		}
	}
}

func assertClientHelpers(t *testing.T, c *Client) {
	t.Helper()
	if !c.HasSession(context.Background(), "uam-a") {
		t.Fatal("expected session")
	}
	argv, err := c.AttachArgv("uam-a")
	if err != nil {
		t.Fatalf("AttachArgv: %v", err)
	}
	if len(argv) != 6 || argv[0] != c.Executable || argv[1] != "-L" {
		t.Fatalf("attach argv: %v", argv)
	}
	if got := c.AttachArgs("uam-a"); len(got) != 5 || got[0] != "-L" {
		t.Fatalf("attach args: %v", got)
	}
	if !PaneAlive(os.Getpid()) {
		t.Fatal("current process should be alive")
	}
	if PaneAlive(-1) {
		t.Fatal("negative pid should not be alive")
	}
	joined := ShellJoin([]string{"abc", "two words"})
	if !strings.Contains(joined, "two words") {
		t.Fatalf("join=%s", joined)
	}
}

func TestEnsureServerConfigInstallsSessionClosedHook(t *testing.T) {
	c, logPath := setupFakeTmuxClient(t)
	if err := c.EnsureServerConfig(context.Background()); err != nil {
		t.Fatalf("EnsureServerConfig: %v", err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "set-hook -g session-closed") {
		t.Fatalf("session-closed hook not installed: %s", data)
	}
	if !strings.Contains(string(data), "notify-closed") {
		t.Fatalf("hook command should reference notify-closed: %s", data)
	}
	// #{hook_session_name} must reach tmux verbatim so it can substitute
	// the dying session's name at fire time.
	if !strings.Contains(string(data), "hook_session_name") {
		t.Fatalf("hook command must pass through tmux format variable: %s", data)
	}
}

func TestSessionClosedHookCommandRejectsUnsafePaths(t *testing.T) {
	// We can't directly fake os.Executable without an injection seam, but
	// we can at least sanity-check the format on the real test binary path.
	cmd := sessionClosedHookCommand()
	if cmd == "" {
		t.Skip("test binary path was rejected as unsafe — skipping format check")
	}
	if !strings.Contains(cmd, "run-shell") {
		t.Fatalf("hook command must use run-shell: %q", cmd)
	}
	if !strings.Contains(cmd, "notify-closed") {
		t.Fatalf("hook command must reference notify-closed: %q", cmd)
	}
	if !strings.Contains(cmd, "'#{hook_session_name}'") {
		t.Fatalf("session name must be single-quoted for the inner shell: %q", cmd)
	}
}

func TestExecutablePathRejectsUnsafeOverrides(t *testing.T) {
	c := New("uam")
	c.Executable = "tmux"
	if _, err := c.ExecutablePath(); err == nil {
		t.Fatal("relative client executable should be rejected")
	}

	nonExecutable := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(nonExecutable, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.Executable = nonExecutable
	if _, err := c.ExecutablePath(); err == nil {
		t.Fatal("non-executable client executable should be rejected")
	}

	t.Setenv("UAM_TMUX_BIN", "tmux")
	c.Executable = ""
	if _, err := c.ExecutablePath(); err == nil {
		t.Fatal("relative UAM_TMUX_BIN should be rejected")
	}
}
