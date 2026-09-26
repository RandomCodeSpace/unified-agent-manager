package session

import (
	"fmt"
	"regexp"
	"strings"
)

var providerIdentityRE = regexp.MustCompile(`^[a-z0-9]+$`)

func validateProviderIdentity(identity string) error {
	if identity == "" || providerIdentityRE.MatchString(identity) {
		return nil
	}
	return fmt.Errorf("invalid provider identity %q", identity)
}

// primaryScreenProviders are the providers whose TUI renders on the terminal's
// primary screen: they never enter the alternate screen themselves and they
// enable no mouse tracking, so their history lives in the terminal's own
// scrollback and the wheel is the terminal's to handle.
//
// Wrapping one of these in the attach client's alternate screen destroys
// exactly that: the alt screen has no scrollback, so there is nothing left for
// the wheel to move and the session appears frozen at one page. Providers that
// drive their own alt screen and their own mouse reporting (claude, copilot,
// hermes) are unaffected and must keep uam's outer screen, which is
// what contains their escape sequences and gets reset on detach.
//
// Membership is a statement about observed terminal behaviour, verified by
// launching the provider on a PTY and recording which DEC private modes it
// sets. It must agree with the provider's declared adapter.OuterScreenPolicy;
// TestPrimaryScreenProvidersMatchAdapterPolicies pins the two together.
var primaryScreenProviders = map[string]bool{
	"codex": true,
	"omp":   true,
}

// PrimaryScreenProvider reports whether identity names a provider that owns
// the primary screen. Exported so the adapter-policy parity test can check the
// runtime decision against each provider's declaration.
func PrimaryScreenProvider(identity string) bool { return primaryScreenProviders[identity] }

func attachOwnsOuterScreen(dir, name string) bool {
	state, err := readState(dir, name)
	if err != nil {
		return true
	}
	if state.ProviderIdentity == "" {
		// Legacy records predate the identity handoff, so the session name is
		// the only evidence available.
		for identity := range primaryScreenProviders {
			if strings.HasPrefix(name, "uam-"+identity+"-") {
				return false
			}
		}
		return true
	}
	return !primaryScreenProviders[state.ProviderIdentity]
}
