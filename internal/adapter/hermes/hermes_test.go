package hermes

import (
	"reflect"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
)

func TestNewUsesBareHermesCommand(t *testing.T) {
	a := New(nil)
	tmuxAgent, ok := a.(*adapter.TmuxAgent)
	if !ok {
		t.Fatalf("adapter type = %T", a)
	}
	if tmuxAgent.Name() != "hermes" || tmuxAgent.DisplayName() != "Hermes Agent" {
		t.Fatalf("bad adapter names: %q %q", tmuxAgent.Name(), tmuxAgent.DisplayName())
	}
	if len(tmuxAgent.Candidates) != 1 {
		t.Fatalf("candidates = %+v", tmuxAgent.Candidates)
	}
	// Launched bare: no --tui (fails to start) and no --yolo (unknown flag
	// kills the pane, same as opencode).
	if got, want := tmuxAgent.Candidates[0].Args, []string{"hermes"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("candidate args = %v, want %v", got, want)
	}
	if len(tmuxAgent.YoloArgs) != 0 {
		t.Fatalf("yolo args = %v, want none", tmuxAgent.YoloArgs)
	}
}
