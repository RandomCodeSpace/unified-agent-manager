package adapter

import "fmt"

type ProviderIdentity string

const (
	ProviderClaude   ProviderIdentity = "claude"
	ProviderCodex    ProviderIdentity = "codex"
	ProviderCopilot  ProviderIdentity = "copilot"
	ProviderHermes   ProviderIdentity = "hermes"
	ProviderOMP      ProviderIdentity = "omp"
	ProviderOpenCode ProviderIdentity = "opencode"
)

type OuterScreenPolicy string

const (
	OuterScreenUAM     OuterScreenPolicy = "uam"
	OuterScreenPrimary OuterScreenPolicy = "primary"
)

type KeyProtocolPolicy string

const KeyProtocolNative KeyProtocolPolicy = "native"

type ProviderTerminalPolicy struct {
	Identity    ProviderIdentity
	OuterScreen OuterScreenPolicy
	KeyProtocol KeyProtocolPolicy
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
