package tmux

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// F24 — KillServer must tear down the private tmux server by emitting
// `tmux -L <socket> kill-server`.
func TestKillServerEmitsKillServer(t *testing.T) {
	c, logPath := setupFakeTmuxClient(t)
	if err := c.KillServer(context.Background()); err != nil {
		t.Fatalf("KillServer: %v", err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "kill-server") {
		t.Fatalf("KillServer did not emit kill-server: %s", data)
	}
}

// F24 — killing an already-dead server must be idempotent: tmux exits non-zero
// with a "no server" message, which KillServer treats as success.
func TestKillServerOnDeadServerIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "tmux")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_LOG"
echo "no server running on /tmp/tmux-uam" >&2
exit 1
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_LOG", filepath.Join(dir, "log"))
	c := New("uam")
	c.Executable = script

	if err := c.KillServer(context.Background()); err != nil {
		t.Fatalf("KillServer on a dead server must be idempotent, got: %v", err)
	}
}

// F24 — a genuine failure (not a missing server) must still propagate.
func TestKillServerPropagatesGenuineError(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "tmux")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_LOG"
echo "permission denied" >&2
exit 1
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_LOG", filepath.Join(dir, "log"))
	c := New("uam")
	c.Executable = script

	if err := c.KillServer(context.Background()); err == nil {
		t.Fatal("KillServer must propagate a genuine (non-missing-server) error")
	}
}
