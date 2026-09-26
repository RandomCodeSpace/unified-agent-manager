package adapter

import "fmt"

type ProviderIdentity string

const (
	ProviderClaude  ProviderIdentity = "claude"
	ProviderCodex   ProviderIdentity = "codex"
	ProviderCopilot ProviderIdentity = "copilot"
	ProviderHermes  ProviderIdentity = "hermes"
	ProviderOMP     ProviderIdentity = "omp"
)

type OuterScreenPolicy string

const (
	OuterScreenUAM     OuterScreenPolicy = "uam"
	OuterScreenPrimary OuterScreenPolicy = "primary"
)

type KeyProtocolPolicy string

const KeyProtocolNative KeyProtocolPolicy = "native"

// BackDetachPolicy is a provider's default for the attach client's quick
// detach (Ctrl+Left while the input box is empty detaches). The gesture
// assumes Ctrl+Left is a no-op at an empty prompt; providers that
// bind it to their own UI (pane or tab navigation) disable the default.
// Profiles and session overrides still take precedence either way.
type BackDetachPolicy string

const (
	// BackDetachDefault leaves the quick detach enabled (the zero value).
	BackDetachDefault BackDetachPolicy = ""
	// BackDetachDisabled turns the quick detach off unless a profile or
	// override explicitly enables it.
	BackDetachDisabled BackDetachPolicy = "disabled"
)

type ProviderTerminalPolicy struct {
	Identity    ProviderIdentity
	OuterScreen OuterScreenPolicy
	KeyProtocol KeyProtocolPolicy
	BackDetach  BackDetachPolicy
}

type TerminalPolicyAdapter interface {
	TerminalPolicy() ProviderTerminalPolicy
}

func (p ProviderTerminalPolicy) Validate() error {
	if !validProviderIdentity(p.Identity) {
		return fmt.Errorf("invalid provider terminal identity %q", p.Identity)
	}
	if p.OuterScreen != OuterScreenUAM && p.OuterScreen != OuterScreenPrimary {
		return fmt.Errorf("invalid provider outer-screen policy %q", p.OuterScreen)
	}
	if p.KeyProtocol != KeyProtocolNative {
		return fmt.Errorf("invalid provider key-protocol policy %q", p.KeyProtocol)
	}
	if p.BackDetach != BackDetachDefault && p.BackDetach != BackDetachDisabled {
		return fmt.Errorf("invalid provider back-detach policy %q", p.BackDetach)
	}
	return nil
}

func validProviderIdentity(identity ProviderIdentity) bool {
	if identity == "" {
		return false
	}
	for _, character := range identity {
		if character < 'a' || character > 'z' {
			if character < '0' || character > '9' {
				return false
			}
		}
	}
	return true
}

func nativeTerminalPolicy(identity ProviderIdentity) ProviderTerminalPolicy {
	return ProviderTerminalPolicy{Identity: identity, OuterScreen: OuterScreenUAM, KeyProtocol: KeyProtocolNative}
}
