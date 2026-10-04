package web

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func registryFixture(t *testing.T) *connectionRegistry {
	t.Helper()
	r, err := openConnectionRegistry(context.Background(), t.TempDir(), testToken)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.close)
	return r
}
func targetFixture() connectionTarget {
	return connectionTarget{Connection: Connection{ID: uuid.NewString(), InstanceID: uuid.NewString(), Label: "remote", BaseURL: "https://remote.example", Enabled: true, ProtocolMajor: 1, Capabilities: []string{"events-v1"}}, Credential: "fixture-workload-credential"}
}
func TestConnectionRegistryLifecycle(t *testing.T) {
	r := registryFixture(t)
	target := targetFixture()
	changes := r.Changed()
	c, err := r.put(target, 0)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-changes:
	default:
		t.Fatal("missing registry change")
	}
	target.Generation = c.Generation
	_, live, err := r.Acquire(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(r.List())
	if strings.Contains(string(raw), target.Credential) || strings.Contains(string(raw), testToken) {
		t.Fatal("registry list disclosed credentials")
	}
	c.Capabilities[0] = "mutated"
	if r.List()[0].Capabilities[0] != "events-v1" {
		t.Fatal("list aliases registry state")
	}
	target.Enabled = false
	if _, err := r.put(target, target.Generation); err != nil {
		t.Fatal(err)
	}
	select {
	case <-live.Done():
	default:
		t.Fatal("disable kept live context")
	}
	if _, _, err := r.Acquire(c.ID); err == nil {
		t.Fatal("disabled connection acquired")
	}
	if _, err := r.put(target, 1); err == nil {
		t.Fatal("stale update accepted")
	}
	restarted, err := openConnectionRegistry(context.Background(), filepath.Dir(r.path), testToken)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.close()
	if restarted.InstanceID() != r.InstanceID() || len(restarted.List()) != 1 || restarted.List()[0].Enabled {
		t.Fatal("restart lost identity or registration")
	}
	if err := r.remove(c.ID, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := r.put(target, 2); err == nil {
		t.Fatal("stale update resurrected removed entry")
	}
	info, err := os.Stat(r.path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("registry is not owner-only", err)
	}
}
func TestConnectionRegistryIdentity(t *testing.T) {
	r := registryFixture(t)
	target := targetFixture()
	target.InstanceID = r.InstanceID()
	if _, err := r.put(target, 0); err == nil {
		t.Fatal("self attachment accepted")
	}
	target.InstanceID = uuid.NewString()
	if _, err := r.put(target, 0); err != nil {
		t.Fatal(err)
	}
	alias := target
	alias.ID = uuid.NewString()
	alias.BaseURL = "https://alias.example"
	if _, err := r.put(alias, 0); err == nil {
		t.Fatal("alias counted twice")
	}
	target.InstanceID = uuid.NewString()
	if _, err := r.put(target, 1); err == nil {
		t.Fatal("replacement silently accepted")
	}
}
func TestWorkloadGrantRestartRevokeAndMasterRotation(t *testing.T) {
	r := registryFixture(t)
	grant, credential, err := r.issue(uuid.NewString(), "home")
	if err != nil {
		t.Fatal(err)
	}
	_, live, err := r.authenticate(credential)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(r.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), credential) || strings.Contains(string(raw), testToken) {
		t.Fatal("target persisted raw grant or master")
	}
	restarted, err := openConnectionRegistry(context.Background(), filepath.Dir(r.path), testToken)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.close()
	if _, _, err := restarted.authenticate(credential); err != nil {
		t.Fatal("grant did not survive restart")
	}
	if err := r.revoke(grant.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-live.Done():
	default:
		t.Fatal("revocation kept live stream")
	}
	if _, _, err := r.authenticate(credential); err == nil {
		t.Fatal("revoked grant accepted")
	}
	_, activeCredential, err := r.issue(uuid.NewString(), "still-active")
	if err != nil {
		t.Fatal(err)
	}
	sameMaster, err := openConnectionRegistry(context.Background(), filepath.Dir(r.path), testToken)
	if err != nil {
		t.Fatal(err)
	}
	defer sameMaster.close()
	if _, _, err := sameMaster.authenticate(activeCredential); err != nil {
		t.Fatal("valid grant missing before rotation")
	}
	rotated, err := openConnectionRegistry(context.Background(), filepath.Dir(restarted.path), strings.Repeat("x", 64))
	if err != nil {
		t.Fatal(err)
	}
	defer rotated.close()
	if rotated.InstanceID() != r.InstanceID() {
		t.Fatal("master rotation changed instance identity")
	}
	if _, _, err := rotated.authenticate(activeCredential); err == nil {
		t.Fatal("rotated master preserved grant")
	}
}
func TestConnectionRegistryFailedWriteKeepsLiveState(t *testing.T) {
	r := registryFixture(t)
	target := targetFixture()
	c, err := r.put(target, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, live, _ := r.Acquire(c.ID)
	if err := r.root.Close(); err != nil {
		t.Fatal(err)
	}
	target.Enabled = false
	if _, err := r.put(target, c.Generation); err == nil {
		t.Fatal("write unexpectedly succeeded")
	}
	if len(r.List()) != 1 || !r.List()[0].Enabled {
		t.Fatal("failed write mutated registration")
	}
	select {
	case <-live.Done():
		t.Fatal("failed update revoked active connection")
	default:
	}
}
func TestConnectionRegistryPrivateFileRequired(t *testing.T) {
	r := registryFixture(t)
	if err := os.Chmod(r.path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := openConnectionRegistry(context.Background(), filepath.Dir(r.path), testToken); err == nil {
		t.Fatal("public registry accepted")
	}
}
func TestConnectionRegistryConcurrentIdentityReads(t *testing.T) {
	r := registryFixture(t)
	target := targetFixture()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 100 {
			if !validInstanceID(r.InstanceID()) {
				t.Error("invalid identity")
			}
			_ = r.List()
		}
	}()
	for i := range 20 {
		c, err := r.put(target, uint64(i))
		if err != nil {
			t.Fatal(err)
		}
		target.Generation = c.Generation
	}
	wg.Wait()
}
