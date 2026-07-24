package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateProviderSessionPersistsExplicitIdentity(t *testing.T) {
	client := newTestClient(t)
	name := "uam-fake-a1b2c3d4"
	if err := client.CreateProviderSession(context.Background(), CreateSpec{
		Name: name, Cwd: t.TempDir(), ProviderIdentity: "codex",
		Command: []string{"/bin/sh", "-c", "sleep 60"},
	}); err != nil {
		t.Fatalf("CreateProviderSession: %v", err)
	}

	state, err := readState(client.Dir, name)
	if err != nil {
		t.Fatalf("readState: %v", err)
	}
	if state.ProviderIdentity != "codex" {
		t.Fatalf("state provider identity = %q, want codex", state.ProviderIdentity)
	}
}

func TestCreateProviderSessionRejectsMalformedIdentity(t *testing.T) {
	client := newTestClient(t)
	err := client.CreateProviderSession(context.Background(), CreateSpec{
		Name: "uam-fake-b1c2d3e4", Cwd: t.TempDir(), ProviderIdentity: "../codex",
		Command: []string{"/bin/sh", "-c", "sleep 60"},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid provider identity") {
		t.Fatalf("CreateProviderSession error = %v, want malformed identity rejection", err)
	}
}

func TestExplicitProviderIdentityControlsAttachScreenOwnership(t *testing.T) {
	tests := []struct {
		name             string
		sessionName      string
		providerIdentity string
		wantOuterScreen  bool
	}{
		{name: "generic provider with misleading Codex name", sessionName: "uam-codex-a1a1a1a1", providerIdentity: "claude", wantOuterScreen: true},
		{name: "Codex provider with generic name", sessionName: "uam-fake-b2b2b2b2", providerIdentity: "codex", wantOuterScreen: false},
		{name: "unknown provider", sessionName: "uam-fake-c3c3c3c3", providerIdentity: "futureagent", wantOuterScreen: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newTestClient(t)
			marker := "identity-" + test.providerIdentity
			if err := client.CreateProviderSession(context.Background(), CreateSpec{
				Name: test.sessionName, Cwd: t.TempDir(), ProviderIdentity: test.providerIdentity,
				Command: []string{"/bin/sh", "-c", "echo " + marker + "; sleep 60"},
			}); err != nil {
				t.Fatalf("CreateProviderSession: %v", err)
			}
			attached := startQuietAttach(t, client.Dir, test.sessionName, 80, 24)
			waitFor(t, "provider identity replay", func() bool { return strings.Contains(attached.Snapshot(), marker) })
			if got := strings.Contains(attached.Snapshot(), screenEnter); got != test.wantOuterScreen {
				t.Fatalf("screen enter present = %v, want %v: %q", got, test.wantOuterScreen, attached.Snapshot())
			}
			attached.Detach(t)
			waitFor(t, "screen cleanup", func() bool {
				return strings.Contains(attached.Snapshot(), screenExit) == test.wantOuterScreen
			})
		})
	}
}

func TestAttachScreenOwnershipUsesExplicitProviderIdentity(t *testing.T) {
	tests := []struct {
		name             string
		sessionName      string
		providerIdentity string
		want             bool
	}{
		{name: "explicit generic overrides misleading Codex name", sessionName: "uam-codex-11111111", providerIdentity: "claude", want: true},
		{name: "explicit Codex overrides generic name", sessionName: "uam-fake-22222222", providerIdentity: "codex", want: false},
		{name: "unknown provider is safely generic", sessionName: "uam-fake-33333333", providerIdentity: "futureagent", want: true},
		{name: "legacy Codex name keeps primary screen", sessionName: "uam-codex-44444444", want: false},
		{name: "legacy generic name owns outer screen", sessionName: "uam-fake-55555555", want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Chmod(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := writeState(dir, State{Name: test.sessionName, ProviderIdentity: test.providerIdentity}); err != nil {
				t.Fatal(err)
			}
			if got := attachOwnsOuterScreen(dir, test.sessionName); got != test.want {
				t.Fatalf("attachOwnsOuterScreen() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestAttachScreenOwnershipMalformedMetadataFallsBackToGeneric(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	name := "uam-codex-66666666"
	path := filepath.Join(dir, name+".json")
	if err := os.WriteFile(path, []byte(`{"name":"uam-codex-66666666","provider_identity":"../codex"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if !attachOwnsOuterScreen(dir, name) {
		t.Fatal("malformed host metadata must use safe generic screen ownership")
	}
}
