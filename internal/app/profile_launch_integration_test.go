package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func TestDispatchNamedWithAliasUsesDefaultProfileWhenLaunchArgumentsUnset(t *testing.T) {
	// Given
	cfg := profileLaunchConfig("fake", "default", store.ModeSafe, "profile-alias", 8100)
	svc, fake := newProfileLaunchService(t, cfg)

	// When
	session, err := svc.DispatchNamedWithAlias(context.Background(), "fake", "", "profiled", "do work", t.TempDir(), "")

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if fake.dispatched == nil || fake.dispatched.Mode != string(store.ModeSafe) || fake.dispatched.CommandAlias != "profile-alias" || fake.dispatched.ScrollbackLines != 8100 {
		t.Fatalf("dispatch request = %+v, want default profile launch fields", fake.dispatched)
	}
	cfg, err = svc.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	record := cfg.Sessions[store.Key("fake", session.ID)]
	if record.Mode != store.ModeSafe || record.CommandAlias != "profile-alias" {
		t.Fatalf("persisted launch snapshot = mode=%q alias=%q", record.Mode, record.CommandAlias)
	}
}

func TestDispatchNamedWithAliasKeepsExplicitLaunchArgumentsOverProfile(t *testing.T) {
	// Given
	cfg := profileLaunchConfig("fake", "default", store.ModeSafe, "profile-alias", 8100)
	svc, fake := newProfileLaunchService(t, cfg)

	// When
	_, err := svc.DispatchNamedWithAlias(context.Background(), "fake", "explicit-alias", "profiled", "do work", t.TempDir(), string(store.ModeYolo))

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if fake.dispatched == nil || fake.dispatched.Mode != string(store.ModeYolo) || fake.dispatched.CommandAlias != "explicit-alias" {
		t.Fatalf("dispatch request = %+v, want explicit launch fields", fake.dispatched)
	}
}

func TestDispatchNamedWithAliasRejectsIncompatibleDefaultProfileBeforeLaunch(t *testing.T) {
	// Given
	cfg := profileLaunchConfig("other", "default", store.ModeSafe, "profile-alias", 8100)
	svc, fake := newProfileLaunchService(t, cfg)

	// When
	_, err := svc.DispatchNamedWithAlias(context.Background(), "fake", "", "profiled", "do work", t.TempDir(), "")

	// Then
	if err == nil {
		t.Fatal("DispatchNamedWithAlias accepted a default profile for another provider")
	}
	if fake.dispatched != nil {
		t.Fatalf("incompatible profile reached adapter dispatch: %+v", fake.dispatched)
	}
}

func TestResumeBackgroundUsesSelectedProfileAndSessionOverrides(t *testing.T) {
	// Given
	cfg := profileLaunchConfig("fake", "focused", store.ModeSafe, "profile-alias", 8100)
	overrides := store.SessionProfileOverrides{
		Mode: pointer(store.ModeYolo), CommandAlias: pointer("session-alias"), ScrollbackLines: pointer(9100),
	}
	cfg.Sessions[store.Key("fake", "resume0001")] = store.SessionRecord{
		ID: "resume0001", Agent: "fake", Name: "profiled", Prompt: "resume work", Workdir: t.TempDir(),
		SessionName: "uam-fake-resume0001", Mode: store.ModeSafe, CommandAlias: "legacy-alias",
		Profile: "focused", ProfileOverrides: &overrides, Status: store.StatusActive,
	}
	svc, fake := newProfileLaunchService(t, cfg)

	// When
	err := svc.ResumeBackground(context.Background(), "resume0001")

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if fake.resumed == nil || fake.resumed.Mode != string(store.ModeYolo) || fake.resumed.CommandAlias != "session-alias" || fake.resumed.ScrollbackLines != 9100 {
		t.Fatalf("resume request = %+v, want selected profile session overrides", fake.resumed)
	}
}

func TestResumeBackgroundRejectsIncompatibleSelectedProfileBeforeLaunch(t *testing.T) {
	// Given
	cfg := profileLaunchConfig("other", "focused", store.ModeSafe, "profile-alias", 8100)
	cfg.Sessions[store.Key("fake", "resume0001")] = store.SessionRecord{
		ID: "resume0001", Agent: "fake", Workdir: t.TempDir(), SessionName: "uam-fake-resume0001",
		Profile: "focused", Status: store.StatusActive,
	}
	svc, fake := newProfileLaunchService(t, cfg)

	// When
	err := svc.ResumeBackground(context.Background(), "resume0001")

	// Then
	if err == nil {
		t.Fatal("ResumeBackground accepted a selected profile for another provider")
	}
	if fake.resumed != nil {
		t.Fatalf("incompatible profile reached adapter resume: %+v", fake.resumed)
	}
}

func newProfileLaunchService(t *testing.T, cfg store.Config) (*Service, *svcFakeAdapter) {
	t.Helper()
	persistentStore, err := store.Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := persistentStore.Save(cfg); err != nil {
		t.Fatal(err)
	}
	fake := &svcFakeAdapter{name: "fake", available: true, resumeKind: adapter.ResumeExact}
	return NewService(persistentStore, adapter.NewRegistry([]adapter.AgentAdapter{fake})), fake
}

func profileLaunchConfig(provider, profileName string, mode store.Mode, alias string, scrollback int) store.Config {
	cfg := store.DefaultConfig()
	cfg.DefaultAgent = "fake"
	cfg.DefaultProfile = profileName
	cfg.Profiles[profileName] = store.Profile{
		Provider: pointer(provider), Mode: pointer(mode), CommandAlias: pointer(alias), ScrollbackLines: pointer(scrollback),
	}
	return cfg
}
