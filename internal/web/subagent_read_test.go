package web

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// A subagent read of an evicted transcript ends when its caller leaves, not
// when the record's read does.
func TestSubagentReadEndsWithItsCaller(t *testing.T) {
	m, prov, _, tasks := closedTasks(t, StageSettled)
	release := make(chan struct{})
	prov.SetReadHook(func(ctx context.Context, _ agentapi.ReadRequest) (agentapi.History, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return agentapi.History{}, context.Canceled
	})
	t.Cleanup(func() { close(release) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() {
		_, err := m.subagent(ctx, tasks[0].ID, "sa1")
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("subagent read = %v, want the caller's cancellation", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the subagent read still waits after its caller left")
	}
}
