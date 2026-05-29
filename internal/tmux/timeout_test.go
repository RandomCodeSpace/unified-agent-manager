package tmux

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// F17 — an external tmux call must be bounded by an internal timeout so a hung
// tmux process cannot wedge a refresh indefinitely. The fake tmux sleeps far
// longer than the timeout; the call must return well before the sleep finishes.
func TestRunHonorsContextDeadline(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "tmux")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	c := New("uam")
	c.Executable = script

	done := make(chan struct{}, 1)
	start := time.Now()
	go func() {
		// Capture is a representative external read; any run-backed call works.
		_, _ = c.Capture(context.Background(), "uam-x", 10)
		done <- struct{}{}
	}()
	// Generous upper bound: well under the 60s sleep but above tmuxCallTimeout.
	select {
	case <-done:
		if elapsed := time.Since(start); elapsed > tmuxCallTimeout+5*time.Second {
			t.Fatalf("run returned but took too long: %v", elapsed)
		}
	case <-time.After(tmuxCallTimeout + 5*time.Second):
		t.Fatalf("run blocked past its internal timeout (%v)", tmuxCallTimeout)
	}
}

// F17 — a caller-supplied deadline tighter than the internal timeout must still
// be honored (the internal timeout is an upper bound, not a floor).
func TestRunHonorsTighterCallerDeadline(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "tmux")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	c := New("uam")
	c.Executable = script

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := c.Capture(ctx, "uam-x", 10); err == nil {
		t.Fatal("expected error from a tighter caller deadline")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("tighter caller deadline not honored: %v", elapsed)
	}
}
