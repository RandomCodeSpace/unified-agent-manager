package hermes

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/adaptertest"
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

func TestLaunchAndResumeUseBareProviderNativeCommand(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hermes"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	backend := &adaptertest.Backend{}
	agent := New(backend).(*adapter.Agent)
	if _, err := agent.Dispatch(context.Background(), adapter.DispatchRequest{Cwd: t.TempDir(), Mode: "yolo"}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if _, err := agent.Resume(context.Background(), adapter.ResumeRequest{ID: "deadbeef", Cwd: t.TempDir(), Mode: "yolo", SessionName: "uam-hermes-deadbeef"}); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	for _, call := range backend.CallsOf("create") {
		if got := strings.Join(call.Command, " "); got != "hermes" {
			t.Fatalf("Hermes launch command = %q, want bare provider command", got)
		}
	}
}
