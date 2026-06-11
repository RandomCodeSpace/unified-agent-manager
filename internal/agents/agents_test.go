package agents

import (
	"sort"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/session"
)

// F14 — Default is the single source of truth for the adapter list. It must
// build all six providers (claude, codex, copilot, hermes, omp, opencode)
// regardless of whether their CLI is installed, because the names are compared
// on the raw pre-availability list (Enabled() would be LookPath-filtered to
// empty in CI).
func TestDefaultBuildsAllProviders(t *testing.T) {
	client := session.NewClient()
	got := make([]string, 0)
	for _, a := range Default(client) {
		got = append(got, a.Name())
	}
	sort.Strings(got)

	want := []string{"claude", "codex", "copilot", "hermes", "omp", "opencode"}
	if len(got) != len(want) {
		t.Fatalf("Default built %d adapters %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Default adapter set = %v, want %v", got, want)
		}
	}
}

// F14 — the brief calls out hermes specifically because the old hand-maintained
// app.New list omitted it; assert it is present.
func TestDefaultIncludesHermes(t *testing.T) {
	client := session.NewClient()
	for _, a := range Default(client) {
		if a.Name() == "hermes" {
			return
		}
	}
	t.Fatal("Default must include the hermes adapter")
}
