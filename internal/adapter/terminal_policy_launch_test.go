package adapter

import (
	"context"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/adaptertest"
)

func TestTodo8LaunchSnapshotReachesHostCreate(t *testing.T) {
	backend := &adaptertest.Backend{}
	agent := NewAgent("fake", "Fake", []CommandCandidate{{Display: "true", Args: []string{"/bin/true"}}}, nil, backend)
	if _, err := agent.Dispatch(context.Background(), DispatchRequest{Cwd: t.TempDir(), ScrollbackLines: 8123, Mode: "safe"}); err != nil {
		t.Fatal(err)
	}
	creates := backend.CallsOf("create")
	if len(creates) != 1 {
		t.Fatalf("create calls = %d, want 1", len(creates))
	}
	if creates[0].ScrollbackLines != 8123 {
		t.Fatalf("host create scrollback = %d, want 8123", creates[0].ScrollbackLines)
	}
}
