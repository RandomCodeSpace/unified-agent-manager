package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/app"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func TestProfileCLIContract(t *testing.T) {
	// Given
	svc, _ := newCLITestService(t)

	// When
	err := runCommand(context.Background(), svc, []string{"profile", "set", "focused", "--mode", "safe", "--mouse", "off", "--prefix", "C-a", "--back-detach", "off", "--scrollback", "8000"}, noopRunTUI)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"profile", "ls", "--json"}, {"profile", "show", "focused", "--json"}, {"profile", "default", "focused"}} {
		if err := runCommand(context.Background(), svc, args, noopRunTUI); err != nil {
			t.Fatalf("run %v: %v", args, err)
		}
	}
	help := captureCLIStderr(t, Usage)
	for _, command := range []string{"profile ls", "profile show", "profile set", "profile rm", "profile default", "profile assign", "profile override", "profile effective"} {
		if !strings.Contains(help, command) {
			t.Fatalf("help missing %q:\n%s", command, help)
		}
	}
}

func TestProfileJSONIsStable(t *testing.T) {
	// Given
	svc, _ := newCLITestService(t)
	must(t, runCommand(context.Background(), svc, []string{"profile", "set", "zeta", "--mode", "safe"}, noopRunTUI))
	must(t, runCommand(context.Background(), svc, []string{"profile", "set", "alpha", "--mode", "yolo"}, noopRunTUI))

	// When
	out := captureCLIStdout(t, func() {
		must(t, runCommand(context.Background(), svc, []string{"profile", "ls", "--json"}, noopRunTUI))
	})

	// Then
	var document struct {
		Profiles []struct {
			Name string `json:"name"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal([]byte(out), &document); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, out)
	}
	if len(document.Profiles) != 2 || document.Profiles[0].Name != "alpha" || document.Profiles[1].Name != "zeta" {
		t.Fatalf("profile order = %+v", document.Profiles)
	}
}

func TestReferencedProfileDeleteRejected(t *testing.T) {
	// Given
	svc, _ := newCLITestService(t)
	seedProfileSession(t, svc, "full-session-id", "focused")

	// When
	err := runCommand(context.Background(), svc, []string{"profile", "rm", "focused"}, noopRunTUI)

	// Then
	if err == nil || !strings.Contains(err.Error(), "full-session-id") {
		t.Fatalf("referenced deletion error = %v", err)
	}
}

func TestProfileAssignmentExactSession(t *testing.T) {
	// Given
	svc, _ := newCLITestService(t)
	seedProfileSession(t, svc, "full-session-id", "")

	// When
	if err := runCommand(context.Background(), svc, []string{"profile", "assign", "full-session-id", "focused"}, noopRunTUI); err != nil {
		t.Fatalf("exact assignment: %v", err)
	}
	err := runCommand(context.Background(), svc, []string{"profile", "assign", "full", "none"}, noopRunTUI)

	// Then
	if err == nil {
		t.Fatal("assignment accepted a session ID prefix")
	}
	cfg, loadErr := svc.Store.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if got := cfg.Sessions[store.Key("fake", "full-session-id")].Profile; got != "focused" {
		t.Fatalf("profile after rejected prefix assignment = %q", got)
	}
}

func TestDispatchProfileSelection(t *testing.T) {
	// Given
	svc, _ := newCLITestService(t)
	must(t, runCommand(context.Background(), svc, []string{"profile", "set", "focused", "--provider", "fake", "--mode", "safe"}, noopRunTUI))

	// When
	_ = captureCLIStdout(t, func() {
		must(t, RunDispatch(context.Background(), svc, []string{"--profile", "focused", "--cwd", "/tmp", "fake", "work"}))
	})

	// Then
	cfg, err := svc.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	record := cfg.Sessions[store.Key("fake", "abc12345")]
	if record.Profile != "focused" || record.Mode != store.ModeSafe {
		t.Fatalf("dispatch profile = %q mode=%q", record.Profile, record.Mode)
	}
}

func TestNewProfileSelection(t *testing.T) {
	// Given
	svc, _ := newCLITestService(t)
	must(t, runCommand(context.Background(), svc, []string{"profile", "set", "focused", "--provider", "fake", "--mode", "safe"}, noopRunTUI))

	// When
	withCLIStdin(t, "fake\n\n/tmp\nwork\n", func() {
		_ = captureCLIStdout(t, func() {
			must(t, runNewWithArgs(context.Background(), svc, []string{"--profile", "focused"}, noopRunTUI))
		})
	})

	// Then
	cfg, err := svc.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	record := cfg.Sessions[store.Key("fake", "abc12345")]
	if record.Profile != "focused" || record.Mode != store.ModeSafe {
		t.Fatalf("new profile = %q mode=%q", record.Profile, record.Mode)
	}
}

func TestProfileSubcommandHelpFlagSucceedsAndPrintsUsage(t *testing.T) {
	svc, _ := newCLITestService(t)
	cases := []struct {
		args  []string
		usage string
	}{
		{[]string{"profile", "ls", "-h"}, "Usage of profile ls:"},
		{[]string{"profile", "show", "-h"}, "Usage of profile show:"},
		{[]string{"profile", "show", "focused", "--help"}, "Usage of profile show:"},
		{[]string{"profile", "set", "-h"}, "Usage of profile set:"},
		{[]string{"profile", "set", "focused", "-h"}, "Usage of profile set:"},
		{[]string{"profile", "override", "-h"}, "Usage of profile override:"},
		{[]string{"profile", "effective", "-h"}, "Usage of profile effective:"},
	}
	for _, tc := range cases {
		var err error
		stderr := captureCLIStderr(t, func() { err = runCommand(context.Background(), svc, tc.args, noopRunTUI) })
		if err != nil {
			t.Fatalf("uam %s: help must not fail, got %v", strings.Join(tc.args, " "), err)
		}
		if !strings.Contains(stderr, tc.usage) || !strings.Contains(stderr, "-json") && !strings.Contains(stderr, "-mode") {
			t.Fatalf("uam %s stderr = %q, want %q with flag list", strings.Join(tc.args, " "), stderr, tc.usage)
		}
	}
	var err error
	stderr := captureCLIStderr(t, func() {
		err = runCommand(context.Background(), svc, []string{"profile", "set", "focused", "--bogus"}, noopRunTUI)
	})
	if err == nil || !strings.Contains(stderr, "Usage of profile set:") {
		t.Fatalf("unknown profile flag: err=%v stderr=%q, want error with usage", err, stderr)
	}
}

func TestProfileFlagValidation(t *testing.T) {
	// Given
	svc, _ := newCLITestService(t)
	if err := runCommand(context.Background(), svc, []string{"profile", "set", "unsafe", "--mode", "safe"}, noopRunTUI); err != nil {
		t.Fatalf("valid baseline: %v", err)
	}
	invalid := []string{"profile", "set", "unsafe", "--alias", "$(touch nope)", "--prefix", "Ctrl-A", "--scrollback", "99"}

	// When
	err := runCommand(context.Background(), svc, invalid, noopRunTUI)

	// Then
	if err == nil {
		t.Fatal("invalid profile flags were accepted")
	}
	cfg, loadErr := svc.Store.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	profile, exists := cfg.Profiles["unsafe"]
	if !exists || profile.Mode == nil || *profile.Mode != store.ModeSafe || profile.CommandAlias != nil {
		t.Fatalf("invalid update changed baseline: %+v", profile)
	}
}

func seedProfileSession(t *testing.T, svc *app.Service, id, profile string) {
	t.Helper()
	err := svc.Store.Update(func(cfg *store.Config) error {
		cfg.Profiles["focused"] = store.Profile{Mode: profilePointer(store.ModeSafe)}
		cfg.Sessions[store.Key("fake", id)] = store.SessionRecord{
			ID: id, Agent: "fake", Name: "seeded", Mode: store.ModeYolo, Workdir: "/tmp",
			SessionName: "uam-fake-" + store.ShortID(id), Profile: profile,
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func profilePointer[T any](value T) *T { return &value }
