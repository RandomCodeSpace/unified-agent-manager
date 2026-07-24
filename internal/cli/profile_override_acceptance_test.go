package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func TestProfileOverrideAndEffectiveCLIContract(t *testing.T) {
	// Given
	svc, _ := newCLITestService(t)
	seedProfileSession(t, svc, "full-session-id", "focused")
	must(t, runCommand(context.Background(), svc, []string{"profile", "default", "focused"}, noopRunTUI))

	// When
	must(t, runCommand(context.Background(), svc, []string{
		"profile", "override", "full-session-id",
		"--provider", "fake",
		"--mode", "yolo",
		"--alias", "fake",
		"--mouse", "on",
		"--prefix", "C-z",
		"--back-detach", "on",
		"--scrollback", "9000",
	}, noopRunTUI))
	effectiveJSON := captureCLIStdout(t, func() {
		must(t, runCommand(context.Background(), svc, []string{
			"profile", "effective", "full-session-id", "--json",
		}, noopRunTUI))
	})
	effectiveText := captureCLIStdout(t, func() {
		must(t, runCommand(context.Background(), svc, []string{
			"profile", "effective", "full-session-id",
		}, noopRunTUI))
	})
	listText := captureCLIStdout(t, func() {
		must(t, runCommand(context.Background(), svc, []string{"profile", "ls"}, noopRunTUI))
	})
	showText := captureCLIStdout(t, func() {
		must(t, runCommand(context.Background(), svc, []string{"profile", "show", "focused"}, noopRunTUI))
	})

	// Then
	var effective map[string]any
	if err := json.Unmarshal([]byte(effectiveJSON), &effective); err != nil {
		t.Fatalf("decode effective profile: %v\n%s", err, effectiveJSON)
	}
	if effective["session_id"] != "full-session-id" || effective["effective_profile"] != "focused" {
		t.Fatalf("effective profile = %#v", effective)
	}
	if !strings.Contains(effectiveText, "full-session-id") {
		t.Fatalf("effective text = %q", effectiveText)
	}
	if !strings.Contains(listText, "focused\tdefault") || !strings.Contains(showText, "focused\tdefault=true") {
		t.Fatalf("profile text list=%q show=%q", listText, showText)
	}

	cfg, err := svc.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	overrides := cfg.Sessions[store.Key("fake", "full-session-id")].ProfileOverrides
	if overrides == nil ||
		overrides.Mode == nil || *overrides.Mode != store.ModeYolo ||
		overrides.CommandAlias == nil || *overrides.CommandAlias != "fake" ||
		overrides.Mouse == nil || *overrides.Mouse != store.MousePolicyOn ||
		overrides.ControlPrefix == nil || *overrides.ControlPrefix != "C-z" ||
		overrides.BackDetach == nil || !*overrides.BackDetach ||
		overrides.ScrollbackLines == nil || *overrides.ScrollbackLines != 9000 {
		t.Fatalf("stored overrides = %+v", overrides)
	}

	must(t, runCommand(context.Background(), svc, []string{
		"profile", "override", "full-session-id",
		"--unset", "mode",
		"--unset", "alias",
		"--unset", "mouse",
		"--unset", "prefix",
		"--unset", "back-detach",
		"--unset", "scrollback",
	}, noopRunTUI))
	cfg, err = svc.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Sessions[store.Key("fake", "full-session-id")].ProfileOverrides; got != nil {
		t.Fatalf("cleared overrides = %+v, want nil", got)
	}
}
