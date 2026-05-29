package app

import (
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
)

// C2-9 — the default-agent selector must never land on a disabled agent. When
// the hardcoded/persisted default is not in the enabled registry, NewWithDeps
// falls back to the registry's chosen default so Enter-with-no-input and the
// prompt hint target a provider that actually exists.
func TestNewWithDepsFallsBackToEnabledWhenDefaultNotEnabled(t *testing.T) {
	// Only "codex" is enabled; the baked-in default "claude" is not.
	reg := adapter.NewRegistry([]adapter.AgentAdapter{
		&svcFakeAdapter{name: "codex", available: true},
		&svcFakeAdapter{name: "claude", available: false},
	})
	m := NewWithDeps(nil, reg)
	if m.defaultAgent != "codex" {
		t.Fatalf("defaultAgent = %q, want the enabled fallback %q", m.defaultAgent, "codex")
	}
}

// C2-9 — a persisted DefaultAgent that is no longer enabled (CLI uninstalled)
// must be reconciled to an enabled provider when sessions load, not displayed
// and dispatched-to verbatim.
func TestHandleSessionsLoadedReconcilesDisabledDefaultAgent(t *testing.T) {
	reg := adapter.NewRegistry([]adapter.AgentAdapter{
		&svcFakeAdapter{name: "opencode", available: true},
	})
	m := NewWithDeps(nil, reg)
	m = m.handleSessionsLoaded(sessionsLoadedMsg{defaultAgent: "claude"})
	if m.defaultAgent != "opencode" {
		t.Fatalf("loaded defaultAgent = %q, want enabled fallback %q", m.defaultAgent, "opencode")
	}
}

// C2-9 — when the persisted default IS enabled it must be honored as-is.
func TestHandleSessionsLoadedKeepsEnabledDefaultAgent(t *testing.T) {
	reg := adapter.NewRegistry([]adapter.AgentAdapter{
		&svcFakeAdapter{name: "codex", available: true},
		&svcFakeAdapter{name: "claude", available: true},
	})
	m := NewWithDeps(nil, reg)
	m = m.handleSessionsLoaded(sessionsLoadedMsg{defaultAgent: "claude"})
	if m.defaultAgent != "claude" {
		t.Fatalf("loaded defaultAgent = %q, want %q (it is enabled)", m.defaultAgent, "claude")
	}
}

// C2-9 nil-guard — with no registry (or none enabled) validation must not panic
// and must leave the baked-in default in place.
func TestDefaultAgentValidationNilRegistryDoesNotPanic(t *testing.T) {
	m := NewWithDeps(nil, nil)
	if m.defaultAgent != "claude" {
		t.Fatalf("nil-registry default = %q, want %q", m.defaultAgent, "claude")
	}
	m = m.handleSessionsLoaded(sessionsLoadedMsg{defaultAgent: "anything"})
	if m.defaultAgent != "anything" {
		// With no registry to validate against, the loaded value is kept verbatim.
		t.Fatalf("nil-registry loaded default = %q, want it kept verbatim", m.defaultAgent)
	}

	// Registry with nothing enabled: Default returns nil, so the candidate is
	// kept (no enabled agent to fall back to).
	empty := adapter.NewRegistry([]adapter.AgentAdapter{&svcFakeAdapter{name: "codex", available: false}})
	m2 := NewWithDeps(nil, empty)
	if m2.defaultAgent != "claude" {
		t.Fatalf("none-enabled default = %q, want baked-in %q kept", m2.defaultAgent, "claude")
	}
}
