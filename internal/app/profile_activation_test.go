package app

import (
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func TestRunningAttachKeepsNegotiatedSnapshot(t *testing.T) {
	cfg := configWithAttachmentProfile("C-a", false)
	first, err := ResolveProfilePolicy(ResolutionInput{Config: cfg, Session: store.SessionRecord{Agent: "claude"}, ProviderPolicy: claudeTerminalPolicy()})
	if err != nil {
		t.Fatal(err)
	}
	running, err := first.NewAttachment(ClientTemporaryOverride{}, allClientCapabilities())
	if err != nil {
		t.Fatal(err)
	}
	updated := cfg.Profiles["attach"]
	updated.ControlPrefix = pointer("C-z")
	updated.BackDetach = pointer(true)
	cfg.Profiles["attach"] = updated
	if _, err := ResolveProfilePolicy(ResolutionInput{Config: cfg, Session: store.SessionRecord{Agent: "claude"}, ProviderPolicy: claudeTerminalPolicy()}); err != nil {
		t.Fatal(err)
	}

	if running.ControlPrefix() != "C-a" || running.BackDetach() {
		t.Fatalf("running attachment snapshot mutated: prefix=%q back_detach=%v", running.ControlPrefix(), running.BackDetach())
	}
}

func TestReconnectUsesUpdatedProfile(t *testing.T) {
	cfg := configWithAttachmentProfile("C-a", false)
	initial, err := ResolveProfilePolicy(ResolutionInput{Config: cfg, Session: store.SessionRecord{Agent: "claude"}, ProviderPolicy: claudeTerminalPolicy()})
	if err != nil {
		t.Fatal(err)
	}
	oldAttachment, err := initial.NewAttachment(ClientTemporaryOverride{}, allClientCapabilities())
	if err != nil {
		t.Fatal(err)
	}
	profile := cfg.Profiles["attach"]
	profile.ControlPrefix = pointer("C-z")
	profile.BackDetach = pointer(true)
	cfg.Profiles["attach"] = profile
	updated, err := ResolveProfilePolicy(ResolutionInput{Config: cfg, Session: store.SessionRecord{Agent: "claude"}, ProviderPolicy: claudeTerminalPolicy()})
	if err != nil {
		t.Fatal(err)
	}
	reconnected, err := updated.NewAttachment(ClientTemporaryOverride{}, allClientCapabilities())
	if err != nil {
		t.Fatal(err)
	}

	if oldAttachment.ControlPrefix() != "C-a" || oldAttachment.BackDetach() {
		t.Fatalf("old attachment changed: %+v", oldAttachment)
	}
	if reconnected.ControlPrefix() != "C-z" || !reconnected.BackDetach() {
		t.Fatalf("reconnect missed update: %+v", reconnected)
	}
}

func TestLaunchOnlyProfileChangesWaitForResume(t *testing.T) {
	cfg := store.DefaultConfig()
	cfg.DefaultProfile = "launch"
	cfg.Profiles["launch"] = completeProfile("claude", store.ModeSafe, "old-alias", store.MousePolicyAuto, "C-b", true, 4000)
	initial, err := ResolveProfilePolicy(ResolutionInput{Config: cfg, Session: store.SessionRecord{Agent: "claude"}, ProviderPolicy: claudeTerminalPolicy()})
	if err != nil {
		t.Fatal(err)
	}
	runningHost := initial.LaunchSnapshot()
	profile := cfg.Profiles["launch"]
	profile.Mode = pointer(store.ModeYolo)
	profile.CommandAlias = pointer("new-alias")
	profile.ScrollbackLines = pointer(7000)
	cfg.Profiles["launch"] = profile
	updated, err := ResolveProfilePolicy(ResolutionInput{Config: cfg, Session: store.SessionRecord{Agent: "claude"}, ProviderPolicy: claudeTerminalPolicy()})
	if err != nil {
		t.Fatal(err)
	}
	resumedHost := updated.LaunchSnapshot()

	if runningHost.Mode() != store.ModeSafe || runningHost.CommandAlias() != "old-alias" || runningHost.ScrollbackLines() != 4000 {
		t.Fatalf("running host snapshot mutated: mode=%q alias=%q scrollback=%d", runningHost.Mode(), runningHost.CommandAlias(), runningHost.ScrollbackLines())
	}
	if resumedHost.Mode() != store.ModeYolo || resumedHost.CommandAlias() != "new-alias" || resumedHost.ScrollbackLines() != 7000 {
		t.Fatalf("resume snapshot missed update: mode=%q alias=%q scrollback=%d", resumedHost.Mode(), resumedHost.CommandAlias(), resumedHost.ScrollbackLines())
	}
}

func configWithAttachmentProfile(prefix string, backDetach bool) store.Config {
	cfg := store.DefaultConfig()
	cfg.DefaultProfile = "attach"
	cfg.Profiles["attach"] = store.Profile{ControlPrefix: pointer(prefix), BackDetach: pointer(backDetach)}
	return cfg
}
