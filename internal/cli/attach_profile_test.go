package cli

import (
	"slices"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/session"
)

func TestAttachEnvironmentIncludesResolvedProfileSnapshot(t *testing.T) {
	// Given
	base := []string{
		"KEEP=value",
		session.AttachMouseEnv + "=off",
		session.AttachPrefixEnv + "=C-z",
		session.AttachBackDetachEnv + "=0",
		session.AttachQuietEnv + "=0",
		session.AttachSelectedProfileEnv + "=stale-selected",
		session.AttachEffectiveProfileEnv + "=stale-effective",
	}
	spec := adapter.AttachSpec{Profile: adapter.AttachProfileSnapshot{
		Selected: "default", Effective: "focused", Mouse: "on", ControlPrefix: "C-a", BackDetach: true,
	}}

	// When
	environment := attachEnvironment(base, spec)

	// Then
	for _, expected := range []string{
		"KEEP=value",
		session.AttachMouseEnv + "=off",
		session.AttachPrefixEnv + "=C-z",
		session.AttachBackDetachEnv + "=0",
		session.AttachQuietEnv + "=1",
		session.AttachSelectedProfileEnv + "=default",
		session.AttachEffectiveProfileEnv + "=focused",
		session.AttachPolicyMouseEnv + "=on",
		session.AttachPolicyPrefixEnv + "=C-a",
		session.AttachPolicyBackDetachEnv + "=1",
	} {
		if !slices.Contains(environment, expected) {
			t.Fatalf("attach environment %q lacks %q", environment, expected)
		}
	}
	for _, stale := range []string{
		session.AttachQuietEnv + "=0",
		session.AttachSelectedProfileEnv + "=stale-selected",
		session.AttachEffectiveProfileEnv + "=stale-effective",
	} {
		if slices.Contains(environment, stale) {
			t.Fatalf("attach environment %q retained stale transport value %q", environment, stale)
		}
	}
}
