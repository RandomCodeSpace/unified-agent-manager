package cli

import (
	"context"
	"errors"
	"testing"
)

// F24 — `uam kill-all` must invoke the session teardown exactly once.
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

// F24 — a teardown error must propagate so the user learns sessions are still
// up (idempotency when nothing is running is handled inside KillAll, not here).
func TestRunKillAllPropagatesError(t *testing.T) {
	kill := func(ctx context.Context) error { return errors.New("boom") }
	if err := runKillAll(context.Background(), kill); err == nil {
		t.Fatal("runKillAll must propagate a teardown error")
	}
}

// F24 — `kill-all` must be routed before store access to the default teardown path.
// An empty session runtime dir (via UAM_SESSION_DIR) keeps the test off any
// real sessions: KillAll over zero sessions is an idempotent success.
func TestRunWithoutStoreKillAllDispatches(t *testing.T) {
	t.Setenv("UAM_SESSION_DIR", secureSessionDir(t))

	out := captureCLIStdout(t, func() {
		if handled, err := runWithoutStore(context.Background(), []string{"kill-all"}); !handled || err != nil {
			t.Fatalf("kill-all dispatch: %v", err)
		}
	})
	if out == "" {
		t.Fatal("kill-all should print a confirmation line")
	}
}
