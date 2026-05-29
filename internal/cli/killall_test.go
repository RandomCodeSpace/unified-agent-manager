package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// F24 — `uam kill-all` must invoke the tmux server teardown exactly once.
func TestRunKillAllInvokesServerTeardown(t *testing.T) {
	calls := 0
	kill := func(ctx context.Context) error {
		calls++
		return nil
	}
	if err := runKillAll(context.Background(), kill); err != nil {
		t.Fatalf("runKillAll: %v", err)
	}
	if calls != 1 {
		t.Fatalf("server teardown invoked %d times, want 1", calls)
	}
}

// F24 — a teardown error must propagate so the user learns the server is still
// up (idempotency on a dead server is handled inside KillServer, not here).
func TestRunKillAllPropagatesError(t *testing.T) {
	kill := func(ctx context.Context) error { return errors.New("boom") }
	if err := runKillAll(context.Background(), kill); err == nil {
		t.Fatal("runKillAll must propagate a teardown error")
	}
}

// F24 — `kill-all` must be routed by runCommand to the default teardown path.
// A fake tmux (via UAM_TMUX_BIN) keeps the test off any real `uam` server and
// host-independent: it reports a dead server, which the idempotent KillServer
// treats as success.
func TestRunCommandKillAllDispatches(t *testing.T) {
	svc, _ := newCLITestService(t)
	dir := t.TempDir()
	fakeTmux := filepath.Join(dir, "tmux")
	if err := os.WriteFile(fakeTmux, []byte("#!/bin/sh\necho 'no server running on /tmp/tmux' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UAM_TMUX_BIN", fakeTmux)

	out := captureCLIStdout(t, func() {
		if err := runCommand(context.Background(), svc, []string{"kill-all"}, func(context.Context, tea.Model) error { return nil }); err != nil {
			t.Fatalf("kill-all dispatch: %v", err)
		}
	})
	if out == "" {
		t.Fatal("kill-all should print a confirmation line")
	}
}
