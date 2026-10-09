package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func fleetLinkedLogin(t *testing.T, n *connectedTestNode) (memory, stored string) {
	t.Helper()
	cfg, err := n.m.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	link, _ := n.m.accountLink(fleetProvider)
	return link.Login, cfg.WebAccountLinks[fleetProvider].Login
}

func fleetConnections(t *testing.T, n *connectedTestNode) []connectionView {
	t.Helper()
	_, data := n.request(t, http.MethodGet, "/api/connections", nil, http.StatusOK)
	var out struct {
		Connections []connectionView `json:"connections"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out.Connections
}

// Two instances linked to different Copilot accounts never pair: the target
// refuses before issuing a grant and the home names both accounts.
func TestFleetPairingRefusesDifferentAccounts(t *testing.T) {
	f := newConnectedAccountFleet(t, []string{"octo", "mallory", ""})
	a, b := f.nodes[0], f.nodes[1]
	_, data := a.request(t, http.MethodPost, "/api/connections", map[string]any{"label": "B box", "base_url": b.http.URL, "token": b.token, "allow_private": true}, http.StatusConflict)
	var refusal struct{ Error, Code string }
	if err := json.Unmarshal(data, &refusal); err != nil {
		t.Fatal(err)
	}
	if refusal.Code != codeAccountMismatch || refusal.Error != "B box is linked to Copilot account mallory; this instance is linked to octo. Both must use the same account." {
		t.Fatalf("refusal = %+v", refusal)
	}
	if _, grants := b.request(t, http.MethodGet, "/api/federation/grants", nil, http.StatusOK); !bytes.Contains(grants, []byte(`"grants":[]`)) {
		t.Fatalf("refused pairing issued a grant: %s", grants)
	}
	if got := fleetConnections(t, a); len(got) != 0 {
		t.Fatalf("refused pairing was saved: %+v", got)
	}
	for _, n := range []*connectedTestNode{a, b} {
		if memory, stored := fleetLinkedLogin(t, n); memory != map[string]string{"A": "octo", "B": "mallory"}[n.name] || stored != memory {
			t.Fatalf("%s link changed: memory %q stored %q", n.name, memory, stored)
		}
	}
}

// The unlinked side adopts the other's link, in either direction, and one
// signed in as another account it cannot sign out becomes a mismatch.
func TestFleetPairingAdoptsTheLinkedAccount(t *testing.T) {
	f := newConnectedAccountFleet(t, []string{"octo", "", ""})
	a, b, c := f.nodes[0], f.nodes[1], f.nodes[2]
	a.connect(t, b) // B, the target, adopts the home's link.
	c.acct.mu.Lock()
	c.acct.signedIn, c.acct.login, c.acct.source = true, "mallory", agentapi.AccountEnv
	c.acct.mu.Unlock()
	c.connect(t, a) // C, the home, adopts the target's link.
	for _, n := range []*connectedTestNode{b, c} {
		if memory, stored := fleetLinkedLogin(t, n); memory != "octo" || stored != "octo" {
			t.Fatalf("%s did not adopt octo: memory %q stored %q", n.name, memory, stored)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for !providerInfo(t, c.m, fleetProvider).AccountMismatch {
		if time.Now().After(deadline) {
			t.Fatalf("C signed in as another account is not a mismatch: %+v", providerInfo(t, c.m, fleetProvider))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if providerInfo(t, b.m, fleetProvider).AccountMismatch {
		t.Fatal("B, signed out, is marked a mismatch")
	}
}

// A peer that sends or returns no account pairs as before.
func TestFleetPairingWithoutAccountsPairsAsBefore(t *testing.T) {
	f := newConnectedAccountFleet(t, []string{"", "", "mallory"})
	a, b, c := f.nodes[0], f.nodes[1], f.nodes[2]
	a.connect(t, b)
	for _, n := range []*connectedTestNode{a, b} {
		if _, linked := n.m.accountLink(fleetProvider); linked {
			t.Fatalf("%s linked without any account", n.name)
		}
	}
	// An older home sends no account field; C pairs and reports its own.
	_, data := c.request(t, http.MethodPost, "/api/federation/pair", map[string]string{"token": c.token, "client_instance_id": a.srv.connections.InstanceID(), "label": "A"}, http.StatusCreated)
	var paired federationPairResponse
	if err := json.Unmarshal(data, &paired); err != nil || paired.Account == nil || paired.Account.Login != "mallory" {
		t.Fatalf("pair response = %s (%v)", data, err)
	}
}

// A connection whose link changes to another account is marked by the
// registry; the home then refuses its workload routes but not its account
// or Settings.
func TestFleetRegistryMarksMismatchAndProxyRefusesWork(t *testing.T) {
	f := newConnectedAccountFleet(t, []string{"octo", "octo", ""})
	a, b := f.nodes[0], f.nodes[1]
	ab := a.connect(t, b)
	if got := fleetConnections(t, a); len(got) != 1 || got[0].Status != "" {
		t.Fatalf("same account marked: %+v", got)
	}
	b.request(t, http.MethodDelete, "/api/providers/copilot/account/link", nil, http.StatusOK)
	b.request(t, http.MethodPost, "/api/providers/copilot/account/sign-in", map[string]string{"token": "other"}, http.StatusOK)
	got := fleetConnections(t, a)
	if len(got) != 1 || got[0].Status != codeAccountMismatch || got[0].Reason != "B is linked to Copilot account mallory; this instance is linked to octo. Both must use the same account." {
		t.Fatalf("changed link not marked: %+v", got)
	}
	before := f.counts()
	_, data := a.request(t, http.MethodPost, connectedTestPath(ab, "/api/sessions"), map[string]string{"project_id": f.project}, http.StatusConflict)
	if !bytes.Contains(data, []byte(`"code":"account_not_linked"`)) || !bytes.Contains(data, []byte("mallory")) || f.counts() != before {
		t.Fatalf("task create reached a mismatched connection: %s", data)
	}
	a.request(t, http.MethodPost, connectedTestPath(ab, "/api/sessions/"+f.taskID+"/prompt"), PromptRequest{Text: "go", RequestID: mustUUID(t), Mode: ModeSend}, http.StatusConflict)
	_, data = a.request(t, http.MethodPost, connectedTestPath(ab, "/api/sessions/"+f.taskID+"/fork"), ForkRequest{UserItemID: "u1", RequestID: mustUUID(t)}, http.StatusConflict)
	if !bytes.Contains(data, []byte(`"code":"account_not_linked"`)) || f.counts() != before {
		t.Fatalf("task branch reached a mismatched connection: %s", data)
	}
	_, data = a.request(t, http.MethodPost, connectedTestPath(ab, "/api/sessions/"+f.taskID+"/fork/dismiss"), ForkRequest{UserItemID: "u1"}, http.StatusConflict)
	if !bytes.Contains(data, []byte(`"code":"account_not_linked"`)) || f.counts() != before {
		t.Fatalf("branch dismissal reached a mismatched connection: %s", data)
	}
	_, data = a.request(t, http.MethodPost, connectedTestPath(ab, "/api/sessions/"+f.taskID+"/rewind"), RewindRequest{UserItemID: "u1", Mode: "conversation", Token: "t", RequestID: mustUUID(t)}, http.StatusConflict)
	if !bytes.Contains(data, []byte(`"code":"account_not_linked"`)) || f.counts() != before {
		t.Fatalf("task rewind reached a mismatched connection: %s", data)
	}
	a.request(t, http.MethodGet, connectedTestPath(ab, "/api/providers/copilot/account"), nil, http.StatusOK)
	a.request(t, http.MethodGet, connectedTestPath(ab, "/api/settings"), nil, http.StatusOK)
	a.request(t, http.MethodGet, connectedTestPath(ab, "/api/sessions"), nil, http.StatusOK)
}

// A connection's stored version and capabilities come from pairing; an
// upgraded instance reports new ones, which the next registry read records
// under the same generation.
func TestFleetRegistryRefreshesVersionAndCapabilities(t *testing.T) {
	f := newConnectedTestFleet(t)
	a, b := f.nodes[0], f.nodes[1]
	ab := a.connect(t, b)
	want := b.srv.federationDescriptor()
	if ab.Version != want.Version || !slices.Equal(ab.Capabilities, want.Capabilities) {
		t.Fatalf("pairing recorded %+v, want %+v", ab, want)
	}
	// The record falls behind what B reports (B was upgraded after pairing).
	registry := a.srv.connections
	registry.mu.Lock()
	stale := registry.data.Connections[ab.ID]
	stale.Version, stale.Capabilities = "v0.0.0-stale", slices.Clone(want.Capabilities[:len(want.Capabilities)-1])
	registry.data.Connections[ab.ID] = stale
	registry.mu.Unlock()
	got := fleetConnections(t, a)
	if len(got) != 1 || got[0].Version != want.Version || !slices.Equal(got[0].Capabilities, want.Capabilities) || got[0].Generation != ab.Generation {
		t.Fatalf("registry read did not refresh the descriptor: %+v, want version %q, capabilities %v, generation %d", got, want.Version, want.Capabilities, ab.Generation)
	}
	saved, err := registry.lookup(ab.ID)
	if err != nil || saved.Version != want.Version || saved.Credential != stale.Credential {
		t.Fatalf("refresh not kept, or credential lost: %+v (%v)", saved, err)
	}
}
