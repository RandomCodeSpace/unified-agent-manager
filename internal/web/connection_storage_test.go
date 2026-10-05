package web

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestConnectionStorageDirectoryProtection(t *testing.T) {
	// Directly below the sticky OS temp root, so no private testing ancestor
	// masks the exposed directory permissions this case is verifying.
	base, err := os.MkdirTemp("", "uam-connection-permissions-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(base, 0o700); _ = os.RemoveAll(base) })
	if err := os.Chmod(base, 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := openConnectionRegistry(context.Background(), base, testToken)
	if err != nil {
		t.Fatalf("owned0755 directory rejected: %v", err)
	}
	r.close()
	if err := os.Chmod(base, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := openConnectionRegistry(context.Background(), base, testToken); err == nil {
		t.Fatal("exposed writable registry directory accepted")
	}
	child := filepath.Join(base, "owned-child")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := openConnectionRegistry(context.Background(), child, testToken); err == nil {
		t.Fatal("writable ancestor allowed pathname replacement")
	}
}

func TestConnectionStorageRejectsSymlinkFileAndPinsDirectory(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "state")
	r, err := openConnectionRegistry(context.Background(), dir, testToken)
	if err != nil {
		t.Fatal(err)
	}
	defer r.close()
	original, _ := os.ReadFile(r.path)
	if err := os.Rename(dir, filepath.Join(base, "retained")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := r.put(targetFixture(), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, connectionFileName)); !os.IsNotExist(err) {
		t.Fatal("save followed a replaced registry directory")
	}
	retained := filepath.Join(base, "retained", connectionFileName)
	updated, _ := os.ReadFile(retained)
	if string(updated) == string(original) {
		t.Fatal("anchored save did not update retained directory")
	}
	if err := os.Symlink(retained, filepath.Join(dir, connectionFileName)); err != nil {
		t.Fatal(err)
	}
	if _, err := openConnectionRegistry(context.Background(), dir, testToken); err == nil {
		t.Fatal("symlink registry file accepted")
	}
}

func TestConnectionStorageSafeDirectoryAlias(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "real")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	r, err := openConnectionRegistry(context.Background(), alias, testToken)
	if err != nil {
		t.Fatal(err)
	}
	defer r.close()
	second, err := openConnectionRegistry(context.Background(), dir, testToken)
	if err != nil {
		t.Fatal(err)
	}
	defer second.close()
	if second.InstanceID() != r.InstanceID() {
		t.Fatal("safe configured directory alias changed identity")
	}
}
