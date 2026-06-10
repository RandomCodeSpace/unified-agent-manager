package hermes

import (
	"reflect"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
)

func TestNewUsesBareHermesCommand(t *testing.T) {
	a := New(nil)
	ag, ok := a.(*adapter.Agent)
	if !ok {
		t.Fatalf("adapter type = %T", a)
	}
	if ag.Name() != "hermes" || ag.DisplayName() != "Hermes Agent" {
		t.Fatalf("bad adapter names: %q %q", ag.Name(), ag.DisplayName())
	}
	if len(ag.Candidates) != 1 {
		t.Fatalf("candidates = %+v", ag.Candidates)
	}
	// Launched bare: no --tui (fails to start) and no --yolo (unknown flag
	// kills the pane, same as opencode).
	if got, want := ag.Candidates[0].Args, []string{"hermes"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("candidate args = %v, want %v", got, want)
	}
	if len(ag.YoloArgs) != 0 {
		t.Fatalf("yolo args = %v, want none", ag.YoloArgs)
	}
}
