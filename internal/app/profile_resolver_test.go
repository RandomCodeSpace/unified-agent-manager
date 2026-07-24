package app

import (
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func TestProfilePrecedenceAndExplicitFalse(t *testing.T) {
	profile := completeProfile("claude", store.ModeSafe, "profile-alias", store.MousePolicyOff, "C-a", true, 8000)
	overrides := completeSessionOverrides(store.ModeYolo, "session-alias", store.MousePolicyOn, "C-c", false, 9000)
	cfg := store.DefaultConfig()
	cfg.DefaultProfile = "global"
	cfg.Profiles["global"] = completeProfile("opencode", store.ModeSafe, "global-alias", store.MousePolicyAuto, "C-b", true, 4000)
	cfg.Profiles["focused"] = profile
	record := store.SessionRecord{Agent: "claude", Profile: "focused", ProfileOverrides: &overrides}

	effective, err := ResolveProfilePolicy(ResolutionInput{
		Config: cfg, Session: record, ProviderPolicy: claudeTerminalPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	attachment, err := effective.NewAttachment(ClientTemporaryOverride{
		Mouse: pointer(store.MousePolicyOff), ControlPrefix: pointer("C-d"),
	}, allClientCapabilities())
	if err != nil {
		t.Fatal(err)
	}

	launch := effective.LaunchSnapshot()
	if launch.Provider() != "claude" || launch.Mode() != store.ModeYolo || launch.CommandAlias() != "session-alias" || launch.ScrollbackLines() != 9000 {
		t.Fatalf("launch precedence = provider=%q mode=%q alias=%q scrollback=%d", launch.Provider(), launch.Mode(), launch.CommandAlias(), launch.ScrollbackLines())
	}
	if attachment.Mouse() != store.MousePolicyOff || attachment.ControlPrefix() != "C-d" || attachment.BackDetach() {
		t.Fatalf("attachment precedence = mouse=%q prefix=%q back_detach=%v", attachment.Mouse(), attachment.ControlPrefix(), attachment.BackDetach())
	}
	if !attachment.MouseFiltered() || !attachment.OwnsOuterScreen() {
		t.Fatalf("capability-constrained behavior = filter=%v own_screen=%v", attachment.MouseFiltered(), attachment.OwnsOuterScreen())
	}
	temporaryEnabled, err := effective.NewAttachment(ClientTemporaryOverride{BackDetach: pointer(true)}, allClientCapabilities())
	if err != nil {
		t.Fatal(err)
	}
	if !temporaryEnabled.BackDetach() {
		t.Fatal("client-local temporary override did not win over explicit session false")
	}
}

func TestCapabilitiesConstrainResolvedPolicy(t *testing.T) {
	cfg := store.DefaultConfig()
	record := store.SessionRecord{Agent: "claude"}
	effective, err := ResolveProfilePolicy(ResolutionInput{Config: cfg, Session: record, ProviderPolicy: claudeTerminalPolicy()})
	if err != nil {
		t.Fatal(err)
	}
	temporary := ClientTemporaryOverride{Mouse: pointer(store.MousePolicyOff)}

	limited, err := effective.NewAttachment(temporary, ClientCapabilities{})
	if err != nil {
		t.Fatal(err)
	}
	capable, err := effective.NewAttachment(temporary, allClientCapabilities())
	if err != nil {
		t.Fatal(err)
	}
	auto, err := effective.NewAttachment(ClientTemporaryOverride{}, allClientCapabilities())
	if err != nil {
		t.Fatal(err)
	}

	if limited.Mouse() != store.MousePolicyOff || limited.MouseFiltered() || limited.OwnsOuterScreen() {
		t.Fatalf("missing capabilities overrode policy: %+v", limited)
	}
	if !capable.MouseFiltered() || !capable.OwnsOuterScreen() {
		t.Fatalf("supported constraints not applied: %+v", capable)
	}
	if auto.MouseFiltered() {
		t.Fatal("capability enabled behavior that policy did not request")
	}
}

func completeProfile(provider string, mode store.Mode, alias string, mouse store.MousePolicy, prefix string, backDetach bool, scrollback int) store.Profile {
	return store.Profile{
		Provider: pointer(provider), Mode: pointer(mode), CommandAlias: pointer(alias), Mouse: pointer(mouse),
		ControlPrefix: pointer(prefix), BackDetach: pointer(backDetach), ScrollbackLines: pointer(scrollback),
	}
}

func completeSessionOverrides(mode store.Mode, alias string, mouse store.MousePolicy, prefix string, backDetach bool, scrollback int) store.SessionProfileOverrides {
	return store.SessionProfileOverrides{
		Mode: pointer(mode), CommandAlias: pointer(alias), Mouse: pointer(mouse), ControlPrefix: pointer(prefix),
		BackDetach: pointer(backDetach), ScrollbackLines: pointer(scrollback),
	}
}

func claudeTerminalPolicy() adapter.ProviderTerminalPolicy {
	return adapter.ProviderTerminalPolicy{Identity: adapter.ProviderClaude, OuterScreen: adapter.OuterScreenUAM, KeyProtocol: adapter.KeyProtocolNative}
}

func allClientCapabilities() ClientCapabilities {
	return ClientCapabilities{FramedOutput: true, RoleEvents: true, LocalMouseFilter: true, OwnedScreen: true}
}

func pointer[T any](value T) *T { return &value }
