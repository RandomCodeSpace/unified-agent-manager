package session

import (
	"bytes"
	"fmt"
	"os"
	"testing"
)

func todo11ExpectedDiagnosticTermHint(wireTermHint string) string {
	switch wireTermHint {
	case "alacritty", "ghostty", "screen-256color", "tmux-256color",
		"wezterm", "xterm-256color", "xterm-kitty":
		return wireTermHint
	default:
		return "redacted"
	}
}

func todo11ValidateNegotiatedTermHint(expected, observed string) error {
	if observed != expected {
		return fmt.Errorf("host negotiated TERM = %q, want %q", observed, expected)
	}
	return nil
}

func TestTodo11TermHintMutationIsDetected(t *testing.T) {
	// Given: an expected TERM distinct from the value placed on the real v2 wire.
	const expected = "xterm-256color"
	const mutated = "screen-256color"
	fixture := todo11LoadFixture(t)
	harness := todo11StartHost(t, fixture, 9001)

	// When: the production runHost attach path consumes and diagnoses the mutation.
	attached := harness.attach(t, mutated, roleController)
	observed := harness.negotiatedTermHint(t, attached.clientID)
	err := todo11ValidateNegotiatedTermHint(expected, observed)
	todo11WriteInput(t, attached, todo11ProviderExit)
	harness.waitInputCount(t, 1)
	socketRemoved, runtimeEntries := harness.cleanup(t)

	// Then: the diagnostic carries the mutated wire value and validation rejects it.
	if observed != mutated {
		t.Fatalf("host negotiated TERM = %q, want mutated wire value %q", observed, mutated)
	}
	if err == nil {
		t.Fatal("TERM mutation was not detected")
	}
	if !socketRemoved || runtimeEntries != 0 {
		t.Fatalf("mutation host cleanup = socket removed %t, runtime entries %d", socketRemoved, runtimeEntries)
	}
}

func TestTodo11UnsupportedTermHintIsRedacted(t *testing.T) {
	// Given: an unsupported TERM containing diagnostic-sensitive text.
	const unsupported = "secret-token-terminal"
	fixture := todo11LoadFixture(t)
	harness := todo11StartHost(t, fixture, 9002)

	// When: the production runHost attach path diagnoses the unsupported hint.
	attached := harness.attach(t, unsupported, roleController)
	observed := harness.negotiatedTermHint(t, attached.clientID)

	// Then: only the bounded redaction classification is emitted.
	if observed != "redacted" {
		t.Fatalf("unsupported host TERM diagnostic = %q, want redacted", observed)
	}
	diagnostics, err := os.ReadFile(harness.diagnosticsPath)
	if err != nil {
		t.Fatal(err)
	}
	todo11WriteInput(t, attached, todo11ProviderExit)
	harness.waitInputCount(t, 1)
	socketRemoved, runtimeEntries := harness.cleanup(t)
	if bytes.Contains(diagnostics, []byte(unsupported)) {
		t.Fatal("unsupported raw TERM leaked into diagnostics")
	}
	if !socketRemoved || runtimeEntries != 0 {
		t.Fatalf("redaction host cleanup = socket removed %t, runtime entries %d", socketRemoved, runtimeEntries)
	}
}
