package app

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	uamlog "github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func TestProfileResolutionStructuredLog(t *testing.T) {
	// Given: a retained session whose selected profile was deleted.
	var output bytes.Buffer
	previous := uamlog.SetLogger(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { uamlog.SetLogger(previous) })
	cfg := store.DefaultConfig()
	record := store.SessionRecord{
		ID: "a1", Agent: "claude", SessionName: "secret-input-7f3a", Profile: "provider-output-91bc",
	}

	// When: the effective policy resolves through the legacy fallback.
	_, err := ResolveProfilePolicy(ResolutionInput{
		Config:  cfg,
		Session: record,
		ProviderPolicy: adapter.ProviderTerminalPolicy{
			Identity: adapter.ProviderClaude, OuterScreen: adapter.OuterScreenPrimary, KeyProtocol: adapter.KeyProtocolNative,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Then: resolution and provider-exception events expose bounded policy
	// metadata, not persisted prompt or environment data.
	logs := output.String()
	for _, event := range []string{"profile.resolution", "provider.exception"} {
		if !strings.Contains(logs, `"event":"`+event+`"`) {
			t.Fatalf("missing %s in logs:\n%s", event, logs)
		}
	}
	if !strings.Contains(logs, `"profile":"redacted"`) || !strings.Contains(logs, `"reason":"profile_fallback"`) {
		t.Fatalf("missing fallback fields:\n%s", logs)
	}
	for _, secret := range []string{"secret-input-7f3a", "provider-output-91bc", "TOKEN=private-value"} {
		if strings.Contains(logs, secret) {
			t.Fatalf("profile diagnostics retained sentinel %q:\n%s", secret, logs)
		}
	}
}
