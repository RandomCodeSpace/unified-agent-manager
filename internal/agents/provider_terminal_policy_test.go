package agents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/adaptertest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/session"
)

func TestProviderTerminalPolicyMatrix(t *testing.T) {
	want := map[string]adapter.ProviderTerminalPolicy{
		"claude":  {Identity: adapter.ProviderClaude, OuterScreen: adapter.OuterScreenUAM, KeyProtocol: adapter.KeyProtocolNative},
		"codex":   {Identity: adapter.ProviderCodex, OuterScreen: adapter.OuterScreenPrimary, KeyProtocol: adapter.KeyProtocolNative},
		"copilot": {Identity: adapter.ProviderCopilot, OuterScreen: adapter.OuterScreenUAM, KeyProtocol: adapter.KeyProtocolNative},
		"hermes":  {Identity: adapter.ProviderHermes, OuterScreen: adapter.OuterScreenUAM, KeyProtocol: adapter.KeyProtocolNative},
		// omp sets no mouse modes and never enters ?1049 — verified on a PTY —
		// so like codex it owns the primary screen and its scrollback.
		"omp": {Identity: adapter.ProviderOMP, OuterScreen: adapter.OuterScreenPrimary, KeyProtocol: adapter.KeyProtocolNative},
	}
	observed := make(map[string]adapter.ProviderTerminalPolicy, len(want))

	for _, provider := range Default(&adaptertest.Backend{}) {
		policyProvider, ok := provider.(adapter.TerminalPolicyAdapter)
		if !ok {
			t.Fatalf("provider %q does not expose typed terminal policy", provider.Name())
		}
		if got := policyProvider.TerminalPolicy(); got != want[provider.Name()] {
			t.Errorf("provider %q terminal policy = %+v, want %+v", provider.Name(), got, want[provider.Name()])
		} else {
			observed[provider.Name()] = got
		}
		delete(want, provider.Name())
	}
	if len(want) != 0 {
		t.Fatalf("terminal policy matrix omitted providers: %+v", want)
	}
	writeProviderTerminalPolicyEvidence(t, observed)
}

// TestPrimaryScreenProvidersMatchAdapterPolicies pins the two halves of the
// outer-screen decision together. A provider declares its policy on its
// adapter, but the attach client decides at runtime from a set in
// internal/session that never sees the adapter — so the declaration is
// documentation and the set is behaviour. When they disagreed, omp declared
// one thing and attached as another, and the wheel stopped scrolling.
func TestPrimaryScreenProvidersMatchAdapterPolicies(t *testing.T) {
	for _, provider := range Default(&adaptertest.Backend{}) {
		policyProvider, ok := provider.(adapter.TerminalPolicyAdapter)
		if !ok {
			t.Fatalf("provider %q does not expose typed terminal policy", provider.Name())
		}
		declaredPrimary := policyProvider.TerminalPolicy().OuterScreen == adapter.OuterScreenPrimary
		if runtimePrimary := session.PrimaryScreenProvider(provider.Name()); runtimePrimary != declaredPrimary {
			t.Errorf("provider %q declares primary-screen=%v but the attach client decides %v",
				provider.Name(), declaredPrimary, runtimePrimary)
		}
	}
}

func writeProviderTerminalPolicyEvidence(t *testing.T, policies map[string]adapter.ProviderTerminalPolicy) {
	t.Helper()
	dir := os.Getenv("UAM_TASK3_EVIDENCE_DIR")
	if dir == "" {
		return
	}
	data, err := json.MarshalIndent(struct {
		Policies               map[string]adapter.ProviderTerminalPolicy `json:"policies"`
		KeyProtocolTranslation bool                                      `json:"key_protocol_translation"`
	}{Policies: policies, KeyProtocolTranslation: false}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(dir, "task-3-provider-policy.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}
