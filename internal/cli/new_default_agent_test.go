package cli

import (
	"context"
	"strings"
	"testing"
)

// C2-9 — `uam new` must re-validate the typed provider against the registry. A
// provider whose CLI is not installed is reconciled to an enabled one
// (Registry.Default falls back to the first enabled adapter) so the wizard
// dispatches to a real agent instead of erroring out on an "unavailable" name.
func TestRunNewReconcilesDisabledTypedProvider(t *testing.T) {
	svc, _ := newCLITestService(t) // only "fake" is enabled
	out := captureCLIStdout(t, func() {
		// Type a disabled provider ("claude") at the prompt.
		withCLIStdin(t, "claude\n/tmp\ndo work\n", func() { must(t, runNew(context.Background(), svc)) })
	})
	if !strings.Contains(out, "dispatched") {
		t.Fatalf("new should dispatch after reconciling the typed provider; out=%q", out)
	}
}

// C2-9 — an enabled typed provider is honored verbatim.
func TestRunNewKeepsEnabledTypedProvider(t *testing.T) {
	svc, fake := newCLITestService(t)
	captureCLIStdout(t, func() {
		withCLIStdin(t, "fake\n/tmp\ndo work\n", func() { must(t, runNew(context.Background(), svc)) })
	})
	if len(fake.sessions) == 0 {
		t.Fatal("dispatch to the enabled typed provider should have created a session")
	}
}
