package cli

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agents"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/tmux"
)

// F14 — cli.NewService must register exactly the shared adapter set built by
// agents.Default (which both it and app.New consume). With every provider's CLI
// stubbed on PATH, the enabled registry must contain all six — including
// hermes, the one the old hand-rolled app.New list dropped. Comparing against
// agents.Default's pre-availability Name() set is what makes this a parity
// guard: if a future edit forks the CLI wiring off the shared list, this fails.
func TestNewServiceRegistryMatchesSharedAdapterSet(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"claude", "codex", "copilot", "hermes", "omp", "opencode"} {
		writeCLIExecutable(t, filepath.Join(dir, name))
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	st, err := store.Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(st)

	got := make([]string, 0)
	for _, a := range svc.Registry.Enabled() {
		got = append(got, a.Name())
	}
	sort.Strings(got)

	want := make([]string, 0)
	for _, a := range agents.Default(tmux.New("uam")) {
		want = append(want, a.Name())
	}
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("cli registry %v != shared adapter set %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cli registry %v != shared adapter set %v", got, want)
		}
	}
	for _, a := range got {
		if a == "hermes" {
			return
		}
	}
	t.Fatal("cli registry must include hermes")
}
