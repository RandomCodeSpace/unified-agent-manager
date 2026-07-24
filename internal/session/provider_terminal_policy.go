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

func attachOwnsOuterScreen(dir, name string) bool {
	state, err := readState(dir, name)
	if err != nil {
		return true
	}
	if state.ProviderIdentity == "" {
		return !strings.HasPrefix(name, "uam-codex-")
	}
	return state.ProviderIdentity != "codex"
}
