package tmux

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientCommandsWithFakeTmux(t *testing.T) {
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
	log := filepath.Join(dir, "log")
	t.Setenv("TMUX_LOG", log)
	c := New("uam")
	if c.Socket != "uam" {
		t.Fatal(c.Socket)
	}
	if err := c.CreateSession(context.Background(), "uam-a", "/tmp", map[string]string{"A": "B"}, []string{"cmd", "arg with space"}); err != nil {
		t.Fatal(err)
	}
	list, err := c.List(context.Background())
	if err != nil || len(list) != 1 {
		t.Fatalf("list=%v err=%v", list, err)
	}
	cap, err := c.Capture(context.Background(), "uam-a", 0)
	if err != nil || !strings.Contains(cap, "pane text") {
		t.Fatalf("cap=%q err=%v", cap, err)
	}
	if err := c.SendLine(context.Background(), "uam-a", "hello"); err != nil {
		t.Fatal(err)
	}
	if err := c.Kill(context.Background(), "uam-a"); err != nil {
		t.Fatal(err)
	}
	if !c.HasSession(context.Background(), "uam-a") {
		t.Fatal("expected session")
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
