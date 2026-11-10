package web

import (
	"slices"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

type secretsProvider struct {
	*agenttest.Provider
	values []string
}

func (p *secretsProvider) SetSecrets(values []string, registered func()) {
	p.values = values
	registered()
	registered()
}

// The access token reaches each provider that masks secrets, and each
// registration it reports is counted.
func TestSetSecretsGivesProvidersTheTokenAndCounts(t *testing.T) {
	p := &secretsProvider{Provider: agenttest.NewProvider("fake", agentapi.Capabilities{})}
	m := NewManager(openTestStore(t), []agentapi.Provider{p})
	m.setSecrets("uam-test-token-0123456789abcdef")
	if !slices.Equal(p.values, []string{"uam-test-token-0123456789abcdef"}) {
		t.Fatalf("provider got %d values, want the token", len(p.values))
	}
	if got := countOf(m, "cli_secret_registrations"); got != 2 {
		t.Fatalf("cli_secret_registrations = %d, want 2", got)
	}
}
