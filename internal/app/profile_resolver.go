package app

import (
	"fmt"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func ResolveProfilePolicy(input ResolutionInput) (EffectivePolicy, error) {
	policy := EffectivePolicy{
		launch: LaunchPolicySnapshot{
			provider: input.Config.DefaultAgent, mode: store.ModeYolo,
			scrollbackLines: defaultScrollbackLines, term: fixedTERM,
		},
		attachment: attachmentDefaults{
			mouse: store.MousePolicyAuto, controlPrefix: defaultControlPrefix, backDetach: true,
		},
	}
	if policy.launch.provider == "" {
		policy.launch.provider = store.DefaultAgentName
	}
	if input.Session.Agent != "" {
		policy.launch.provider = input.Session.Agent
	}

	selected, err := applySelectedProfile(&policy, input)
	if err != nil {
		return EffectivePolicy{}, err
	}
	if err := validateProviderPolicy(input.ProviderPolicy, policy.launch.provider); err != nil {
		return EffectivePolicy{}, err
	}
	policy.launch.providerPolicy = input.ProviderPolicy
	policy.attachment.outerScreen = input.ProviderPolicy.OuterScreen

	if !selected {
		err = applyLegacySessionDefaults(&policy, input.Session)
	}
	if err != nil {
		return EffectivePolicy{}, err
	}
	reason := "legacy"
	fallback := ""
	profile := policy.profile
	layers := "global"
	if input.Session.Agent != "" {
		layers += ",session"
	}
	if profile != "" {
		reason = "default_profile"
		if input.Session.Profile != "" {
			reason = "session_profile"
		}
		layers += ",profile"
		if input.Session.ProfileOverrides != nil {
			layers += ",session_override"
		}
	}
	if len(policy.diagnostics) > 0 {
		reason = policy.diagnostics[0].Code
		fallback = "legacy"
		profile = "redacted"
	}
	log.Diagnostic(log.DiagnosticEvent{
		Event: "profile.resolution", Session: input.Session.SessionName, Reason: reason,
		Provider: policy.launch.provider, Profile: profile, Policy: layers, Fallback: fallback,
	})
	if input.ProviderPolicy.OuterScreen == adapter.OuterScreenPrimary {
		log.Diagnostic(log.DiagnosticEvent{
			Event: "provider.exception", Session: input.Session.SessionName,
			Reason: "provider_primary_screen", Provider: policy.launch.provider, Policy: string(input.ProviderPolicy.OuterScreen),
		})
	}
	return policy, nil
}

func applySelectedProfile(policy *EffectivePolicy, input ResolutionInput) (bool, error) {
	profileName := input.Session.Profile
	profileScope := "session"
	if profileName == "" {
		profileName = input.Config.DefaultProfile
		profileScope = "default"
	}
	profile, selected, err := selectedProfile(input.Config, profileName)
	if err != nil {
		return false, err
	}
	if !selected {
		if profileName != "" {
			policy.diagnostics = []PolicyDiagnostic{{Code: DiagnosticProfileFallback, Profile: profileName, Scope: profileScope}}
		}
		return false, nil
	}
	if profile.Provider != nil && input.Session.Agent != "" && input.Session.Agent != *profile.Provider {
		return false, fmt.Errorf("profile %q provider %q is incompatible with session provider %q", profileName, *profile.Provider, input.Session.Agent)
	}
	policy.profile = profileName
	if profile.Provider != nil {
		policy.launch.provider = *profile.Provider
	}
	applyProfile(policy, profile)
	if input.Session.ProfileOverrides == nil {
		return true, nil
	}
	if err := store.ValidateSessionProfileOverrides(*input.Session.ProfileOverrides); err != nil {
		return false, fmt.Errorf("session profile overrides: %w", err)
	}
	applySessionOverrides(policy, *input.Session.ProfileOverrides)
	return true, nil
}

func selectedProfile(cfg store.Config, name string) (store.Profile, bool, error) {
	if name == "" {
		return store.Profile{}, false, nil
	}
	profile, found := cfg.Profiles[name]
	if !found {
		return store.Profile{}, false, nil
	}
	if err := store.ValidateProfile(profile); err != nil {
		return store.Profile{}, false, fmt.Errorf("profile %q: %w", name, err)
	}
	return profile, true, nil
}

func validateProviderPolicy(policy adapter.ProviderTerminalPolicy, provider string) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	if string(policy.Identity) != provider {
		return fmt.Errorf("provider terminal policy identity %q does not match resolved provider %q", policy.Identity, provider)
	}
	return nil
}

func applyLegacySessionDefaults(policy *EffectivePolicy, session store.SessionRecord) error {
	if session.Mode == store.ModeSafe || session.Mode == store.ModeYolo {
		policy.launch.mode = session.Mode
	}
	if session.CommandAlias == "" {
		return nil
	}
	overrides := store.SessionProfileOverrides{CommandAlias: &session.CommandAlias}
	if err := store.ValidateSessionProfileOverrides(overrides); err != nil {
		return fmt.Errorf("legacy session defaults: %w", err)
	}
	policy.launch.commandAlias = session.CommandAlias
	return nil
}

func applyProfile(policy *EffectivePolicy, profile store.Profile) {
	if profile.Mode != nil {
		policy.launch.mode = *profile.Mode
	}
	if profile.CommandAlias != nil {
		policy.launch.commandAlias = *profile.CommandAlias
	}
	if profile.Mouse != nil {
		policy.attachment.mouse = *profile.Mouse
	}
	if profile.ControlPrefix != nil {
		policy.attachment.controlPrefix = *profile.ControlPrefix
	}
	if profile.BackDetach != nil {
		policy.attachment.backDetach = *profile.BackDetach
	}
	if profile.ScrollbackLines != nil {
		policy.launch.scrollbackLines = *profile.ScrollbackLines
	}
}

func applySessionOverrides(policy *EffectivePolicy, overrides store.SessionProfileOverrides) {
	applyProfile(policy, store.Profile{
		Mode: overrides.Mode, CommandAlias: overrides.CommandAlias, Mouse: overrides.Mouse,
		ControlPrefix: overrides.ControlPrefix, BackDetach: overrides.BackDetach,
		ScrollbackLines: overrides.ScrollbackLines,
	})
}
