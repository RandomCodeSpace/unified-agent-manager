package tmux

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newCountingListClient(t *testing.T) (*Client, string) {
	t.Helper()
	dir := t.TempDir()
	tmuxPath := filepath.Join(dir, "tmux")
	if err := os.WriteFile(tmuxPath, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_LOG"
case "$*" in
  *"list-sessions"*) echo "uam-fake-abc12345|1710000000|0|1|/tmp/repo|fakeagent" ;;
esac
exit 0
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	logPath := filepath.Join(dir, "tmux.log")
	t.Setenv("TMUX_LOG", logPath)
	c := New("uam")
	c.Executable = tmuxPath
	return c, logPath
}

func countListSessions(t *testing.T, logPath string) int {
	t.Helper()
	b, _ := os.ReadFile(logPath)
	n := 0
	for _, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, "list-sessions") {
			n++
		}
	}
	return n
}

// F60 — multiple List calls within the cache TTL (one per adapter per refresh
// tick) must collapse to a single list-sessions shell-out.
func TestListIsTTLCachedWithinWindow(t *testing.T) {
	c, logPath := newCountingListClient(t)
	clock := time.Unix(1710000000, 0)
	c.now = func() time.Time { return clock }

	for i := 0; i < 5; i++ {
		if _, err := c.List(context.Background()); err != nil {
			t.Fatalf("List %d: %v", i, err)
		}
	}
	if got := countListSessions(t, logPath); got != 1 {
		t.Fatalf("List calls within TTL must collapse to one shell-out, got %d", got)
	}
}

// F60 — once the TTL elapses, List must shell out again to pick up new sessions.
func TestListRefreshesAfterTTL(t *testing.T) {
	c, logPath := newCountingListClient(t)
	clock := time.Unix(1710000000, 0)
	c.now = func() time.Time { return clock }

	if _, err := c.List(context.Background()); err != nil {
		t.Fatalf("List 1: %v", err)
	}
	clock = clock.Add(listCacheTTL + time.Millisecond)
	c.now = func() time.Time { return clock }
	if _, err := c.List(context.Background()); err != nil {
		t.Fatalf("List 2: %v", err)
	}
	if got := countListSessions(t, logPath); got != 2 {
		t.Fatalf("List past TTL must re-shell-out, got %d", got)
	}
}

// F60 — a caller that mutates the returned slice must not corrupt the cache for
// the next caller within the TTL window.
func TestListReturnsCopyCallersCannotMutateCache(t *testing.T) {
	c, _ := newCountingListClient(t)
	clock := time.Unix(1710000000, 0)
	c.now = func() time.Time { return clock }

	first, err := c.List(context.Background())
	if err != nil || len(first) != 1 {
		t.Fatalf("List 1: len=%d err=%v", len(first), err)
	}
	first[0].Name = "mutated"

	second, err := c.List(context.Background())
	if err != nil || len(second) != 1 {
		t.Fatalf("List 2: len=%d err=%v", len(second), err)
	}
	if second[0].Name != "uam-fake-abc12345" {
		t.Fatalf("cached slice was mutated by a prior caller: %q", second[0].Name)
	}
}
