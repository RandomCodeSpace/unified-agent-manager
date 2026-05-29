package pr

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// F02 — Check must honor a context deadline: a hung `gh` is cancelled when the
// caller's context expires rather than blocking the refresh indefinitely.
func TestCheckHonorsContextDeadline(t *testing.T) {
	dir := t.TempDir()
	gh := filepath.Join(dir, "gh")
	// Sleep far longer than the deadline so the only way Check returns promptly
	// is by honoring ctx cancellation.
	if err := os.WriteFile(gh, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UAM_GH_BIN", gh)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := Check(ctx, "https://github.com/o/r/pull/1")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Check should fail when the context deadline is exceeded")
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Fatalf("Check did not return promptly after deadline: %v", elapsed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Check blocked past the context deadline (gh not reaped)")
	}
}
