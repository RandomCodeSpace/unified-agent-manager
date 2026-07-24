package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/app"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func TestNewProfileProviderBecomesPromptDefault(t *testing.T) {
	// Given: the global provider and selected profile provider deliberately differ.
	persistentStore, err := store.Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := store.DefaultConfig()
	cfg.DefaultAgent = "codex"
	cfg.Profiles["claudeprof"] = store.Profile{Provider: profilePointer("claude"), Mode: profilePointer(store.ModeSafe)}
	if err := persistentStore.Save(cfg); err != nil {
		t.Fatal(err)
	}
	codex := &cliFakeAdapter{name: "codex"}
	claude := &cliFakeAdapter{name: "claude"}
	svc := app.NewService(persistentStore, adapter.NewRegistry([]adapter.AgentAdapter{codex, claude}))

	// When: Enter accepts the provider prompt default.
	var output string
	withCLIStdin(t, "\n\n/tmp\nprofile work\n", func() {
		output = captureCLIStdout(t, func() {
			must(t, runNewWithArgs(context.Background(), svc, []string{"--profile", "claudeprof"}, noopRunTUI))
		})
	})

	// Then: the profile provider is the prompt/dispatch default, not global codex.
	if !strings.Contains(output, "provider [claude]:") {
		t.Fatalf("new prompt did not use profile provider:\n%s", output)
	}
	if len(claude.sessions) != 1 || len(codex.sessions) != 0 {
		t.Fatalf("dispatch counts claude=%d codex=%d", len(claude.sessions), len(codex.sessions))
	}
}

func TestProfileSetUpdatesExistingAtomically(t *testing.T) {
	// Given
	svc, _ := newCLITestService(t)
	must(t, runCommand(context.Background(), svc, []string{"profile", "set", "focused", "--mode", "safe"}, noopRunTUI))

	// When: set updates the existing map entry rather than creating a duplicate.
	must(t, runCommand(context.Background(), svc, []string{"profile", "set", "focused", "--mode", "yolo", "--mouse", "off"}, noopRunTUI))
	cfg, err := svc.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	beforeInvalid, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	invalidErr := runCommand(context.Background(), svc, []string{"profile", "set", "focused", "--alias", "prompt-like $(nope)"}, noopRunTUI)
	afterInvalid, err := svc.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	afterInvalidJSON, err := json.Marshal(afterInvalid)
	if err != nil {
		t.Fatal(err)
	}

	// Then
	profile := cfg.Profiles["focused"]
	if len(cfg.Profiles) != 1 || profile.Mode == nil || *profile.Mode != store.ModeYolo || profile.Mouse == nil || *profile.Mouse != store.MousePolicyOff {
		t.Fatalf("updated profiles = %+v", cfg.Profiles)
	}
	if invalidErr == nil || !bytes.Equal(beforeInvalid, afterInvalidJSON) {
		t.Fatalf("invalid update err=%v changed=%t", invalidErr, !bytes.Equal(beforeInvalid, afterInvalidJSON))
	}
}

func TestNewExplicitProviderPreservesPrecedenceAndRejectsConflict(t *testing.T) {
	// Given
	persistentStore, err := store.Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := store.DefaultConfig()
	cfg.DefaultAgent = "codex"
	cfg.Profiles["claudeprof"] = store.Profile{Provider: profilePointer("claude")}
	if err := persistentStore.Save(cfg); err != nil {
		t.Fatal(err)
	}
	codex := &cliFakeAdapter{name: "codex"}
	claude := &cliFakeAdapter{name: "claude"}
	svc := app.NewService(persistentStore, adapter.NewRegistry([]adapter.AgentAdapter{codex, claude}))

	// When
	var dispatchErr error
	withCLIStdin(t, "codex\n\n/tmp\nwork\n", func() {
		dispatchErr = runNewWithArgs(context.Background(), svc, []string{"--profile", "claudeprof"}, noopRunTUI)
	})

	// Then
	if dispatchErr == nil || !strings.Contains(dispatchErr.Error(), "incompatible") {
		t.Fatalf("explicit conflicting provider error = %v", dispatchErr)
	}
	if len(codex.sessions) != 0 || len(claude.sessions) != 0 {
		t.Fatalf("conflicting provider dispatched codex=%d claude=%d", len(codex.sessions), len(claude.sessions))
	}
}
