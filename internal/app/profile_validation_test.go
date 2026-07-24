package app

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func TestProfileCannotOverrideSafetyOrTerm(t *testing.T) {
	unsafeDocuments := []string{
		`{"term":"xterm-kitty"}`,
		`{"TERM":"screen-256color"}`,
		`{"command":["sh","-c","id"]}`,
		`{"env":{"TOKEN":"secret"}}`,
		`{"allow_latest":true}`,
	}
	for _, document := range unsafeDocuments {
		t.Run(document, func(t *testing.T) {
			var profile store.Profile
			if err := json.Unmarshal([]byte(document), &profile); err != nil {
				t.Fatal(err)
			}
			cfg := store.DefaultConfig()
			cfg.DefaultProfile = "unsafe"
			cfg.Profiles["unsafe"] = profile

			_, err := ResolveProfilePolicy(ResolutionInput{Config: cfg, Session: store.SessionRecord{Agent: "claude"}, ProviderPolicy: claudeTerminalPolicy()})
			if err == nil || !strings.Contains(err.Error(), "prohibited") {
				t.Fatalf("ResolveProfilePolicy error = %v, want prohibited override rejection", err)
			}
		})
	}

	effective, err := ResolveProfilePolicy(ResolutionInput{Config: store.DefaultConfig(), Session: store.SessionRecord{Agent: "claude"}, ProviderPolicy: claudeTerminalPolicy()})
	if err != nil {
		t.Fatal(err)
	}
	if effective.LaunchSnapshot().TERM() != "xterm-256color" {
		t.Fatalf("TERM = %q, want fixed safety invariant", effective.LaunchSnapshot().TERM())
	}
	for _, document := range []string{`{"TERM":"screen-256color"}`, `{"env":{"TOKEN":"secret"}}`, `{"provider":"codex"}`, `{"allow_latest":true}`} {
		var overrides store.SessionProfileOverrides
		if err := json.Unmarshal([]byte(document), &overrides); err != nil {
			t.Fatal(err)
		}
		cfg := store.DefaultConfig()
		cfg.DefaultProfile = "safe"
		cfg.Profiles["safe"] = store.Profile{}
		_, err := ResolveProfilePolicy(ResolutionInput{
			Config: cfg, Session: store.SessionRecord{Agent: "claude", ProfileOverrides: &overrides}, ProviderPolicy: claudeTerminalPolicy(),
		})
		if err == nil || !strings.Contains(err.Error(), "prohibited") {
			t.Fatalf("session override %s error = %v, want prohibited override rejection", document, err)
		}
	}
}

func TestProfileResolverRejectsMalformedValuesAndProviderMismatch(t *testing.T) {
	tests := []struct {
		name    string
		profile store.Profile
	}{
		{name: "mode", profile: store.Profile{Mode: pointer(store.Mode("turbo"))}},
		{name: "mouse", profile: store.Profile{Mouse: pointer(store.MousePolicy("sometimes"))}},
		{name: "alias", profile: store.Profile{CommandAlias: pointer("unsafe alias")}},
		{name: "prefix uppercase", profile: store.Profile{ControlPrefix: pointer("C-A")}},
		{name: "prefix malformed", profile: store.Profile{ControlPrefix: pointer("Ctrl-a")}},
		{name: "scrollback low", profile: store.Profile{ScrollbackLines: pointer(99)}},
		{name: "scrollback high", profile: store.Profile{ScrollbackLines: pointer(100001)}},
		{name: "provider malformed", profile: store.Profile{Provider: pointer("../claude")}},
		{name: "provider mismatch", profile: store.Profile{Provider: pointer("codex")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := store.DefaultConfig()
			cfg.DefaultProfile = "bad"
			cfg.Profiles["bad"] = test.profile
			_, err := ResolveProfilePolicy(ResolutionInput{Config: cfg, Session: store.SessionRecord{Agent: "claude"}, ProviderPolicy: claudeTerminalPolicy()})
			if err == nil {
				t.Fatal("ResolveProfilePolicy unexpectedly accepted malformed profile")
			}
		})
	}
}

func TestProfileResolverRejectsInvalidBuiltInProviderPolicy(t *testing.T) {
	tests := []adapter.ProviderTerminalPolicy{
		{Identity: "../claude", OuterScreen: adapter.OuterScreenUAM, KeyProtocol: adapter.KeyProtocolNative},
		{Identity: adapter.ProviderClaude, OuterScreen: "unknown", KeyProtocol: adapter.KeyProtocolNative},
		{Identity: adapter.ProviderClaude, OuterScreen: adapter.OuterScreenUAM, KeyProtocol: "translated"},
	}
	for _, policy := range tests {
		_, err := ResolveProfilePolicy(ResolutionInput{Config: store.DefaultConfig(), Session: store.SessionRecord{Agent: "claude"}, ProviderPolicy: policy})
		if err == nil {
			t.Fatalf("ResolveProfilePolicy accepted invalid built-in policy %+v", policy)
		}
	}
}

func TestProfileProviderIsDispatchDefaultAndBoundsAreInclusive(t *testing.T) {
	for _, scrollback := range []int{100, 100000} {
		cfg := store.DefaultConfig()
		cfg.DefaultProfile = "dispatch"
		cfg.Profiles["dispatch"] = store.Profile{
			Provider: pointer("claude"), ControlPrefix: pointer("C-z"), ScrollbackLines: pointer(scrollback),
		}

		effective, err := ResolveProfilePolicy(ResolutionInput{Config: cfg, ProviderPolicy: claudeTerminalPolicy()})
		if err != nil {
			t.Fatal(err)
		}
		launch := effective.LaunchSnapshot()
		if launch.Provider() != "claude" || launch.ScrollbackLines() != scrollback {
			t.Fatalf("dispatch profile = provider=%q scrollback=%d", launch.Provider(), launch.ScrollbackLines())
		}
	}
}

func TestProfileUnsetFieldsInheritLegacyDefaults(t *testing.T) {
	cfg := store.DefaultConfig()
	cfg.DefaultProfile = "partial"
	cfg.Profiles["partial"] = store.Profile{}

	effective, err := ResolveProfilePolicy(ResolutionInput{Config: cfg, Session: store.SessionRecord{Agent: "claude"}, ProviderPolicy: claudeTerminalPolicy()})
	if err != nil {
		t.Fatal(err)
	}
	attachment, err := effective.NewAttachment(ClientTemporaryOverride{}, allClientCapabilities())
	if err != nil {
		t.Fatal(err)
	}

	launch := effective.LaunchSnapshot()
	if launch.Mode() != store.ModeYolo || launch.CommandAlias() != "" || launch.ScrollbackLines() != 4000 {
		t.Fatalf("unset launch fields did not inherit: mode=%q alias=%q scrollback=%d", launch.Mode(), launch.CommandAlias(), launch.ScrollbackLines())
	}
	if attachment.Mouse() != store.MousePolicyAuto || attachment.ControlPrefix() != "C-b" || !attachment.BackDetach() {
		t.Fatalf("unset attachment fields did not inherit: %+v", attachment)
	}
}

func TestMissingProfileUsesLegacyDefaultsAndDiagnostic(t *testing.T) {
	cfg := store.DefaultConfig()
	cfg.DefaultProfile = "deleted"
	record := store.SessionRecord{Agent: "claude", Mode: store.ModeSafe, CommandAlias: "legacy-alias"}

	effective, err := ResolveProfilePolicy(ResolutionInput{Config: cfg, Session: record, ProviderPolicy: claudeTerminalPolicy()})
	if err != nil {
		t.Fatal(err)
	}
	attachment, err := effective.NewAttachment(ClientTemporaryOverride{}, allClientCapabilities())
	if err != nil {
		t.Fatal(err)
	}

	launch := effective.LaunchSnapshot()
	if launch.Provider() != "claude" || launch.Mode() != store.ModeSafe || launch.CommandAlias() != "legacy-alias" || launch.ScrollbackLines() != 4000 {
		t.Fatalf("legacy launch fallback = provider=%q mode=%q alias=%q scrollback=%d", launch.Provider(), launch.Mode(), launch.CommandAlias(), launch.ScrollbackLines())
	}
	if attachment.Mouse() != store.MousePolicyAuto || attachment.ControlPrefix() != "C-b" || !attachment.BackDetach() {
		t.Fatalf("legacy attachment fallback = mouse=%q prefix=%q back_detach=%v", attachment.Mouse(), attachment.ControlPrefix(), attachment.BackDetach())
	}
	diagnostics := effective.Diagnostics()
	if len(diagnostics) != 1 || diagnostics[0].Code != DiagnosticProfileFallback || diagnostics[0].Profile != "deleted" {
		t.Fatalf("fallback diagnostics = %+v", diagnostics)
	}
}

func TestProfileResolutionIsDeterministicAndSourceImmutable(t *testing.T) {
	cfg := store.DefaultConfig()
	cfg.DefaultProfile = "stable"
	cfg.Profiles["stable"] = completeProfile("claude", store.ModeSafe, "stable-alias", store.MousePolicyOff, "C-f", false, 6400)
	before, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	input := ResolutionInput{Config: cfg, Session: store.SessionRecord{Agent: "claude"}, ProviderPolicy: claudeTerminalPolicy()}
	var firstLaunch LaunchPolicySnapshot
	var firstAttachment AttachmentPolicySnapshot
	for attempt := range 25 {
		effective, resolveErr := ResolveProfilePolicy(input)
		if resolveErr != nil {
			t.Fatal(resolveErr)
		}
		attachment, attachErr := effective.NewAttachment(ClientTemporaryOverride{}, allClientCapabilities())
		if attachErr != nil {
			t.Fatal(attachErr)
		}
		if attempt == 0 {
			firstLaunch = effective.LaunchSnapshot()
			firstAttachment = attachment
			continue
		}
		if effective.LaunchSnapshot() != firstLaunch || attachment != firstAttachment {
			t.Fatalf("attempt %d produced a different snapshot", attempt)
		}
	}
	after, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("resolution mutated persistent source: before=%s after=%s", before, after)
	}
}
