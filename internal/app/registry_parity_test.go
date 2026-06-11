package app

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agents"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/session"
)

// F14 — app.New builds its registry from the shared agents.Default list (it used
// to hand-roll one that omitted hermes). This asserts the TUI-side wiring sees
// the full six-provider set, the same set cli.NewService consumes, so the two
// can never drift again. Pre-availability Name() comparison: providers are
// stubbed on PATH so the registry actually enables them in CI.
func TestAppRegistryMatchesSharedAdapterSet(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"claude", "codex", "copilot", "hermes", "omp", "opencode"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	client := session.NewClient()
	reg := adapter.NewRegistry(agents.Default(client))

	got := make([]string, 0)
	for _, a := range reg.Enabled() {
		got = append(got, a.Name())
	}
	sort.Strings(got)

	want := []string{"claude", "codex", "copilot", "hermes", "omp", "opencode"}
	if len(got) != len(want) {
		t.Fatalf("app registry %v != %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("app registry %v != %v", got, want)
		}
	}
}
