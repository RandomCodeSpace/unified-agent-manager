package web

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConnectionStorageMakesItsDirectoryPrivateAndIgnoresAncestors(t *testing.T) {
	// Directly below the sticky OS temp root, so no private testing ancestor
	// masks the exposed permissions this case sets up.
	base, err := os.MkdirTemp("", "uam-connection-permissions-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(base, 0o700); _ = os.RemoveAll(base) })
	// A group- and world-writable uam directory is uam's to fix: it opens and ends up private.
	if err := os.Chmod(base, 0o777); err != nil {
		t.Fatal(err)
	}
	r, err := openConnectionRegistry(context.Background(), base, testToken)
	if err != nil {
		t.Fatalf("writable registry directory refused: %v", err)
	}
	r.close()
	if info, err := os.Stat(base); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("registry directory mode = %v, %v; want 0700", info.Mode().Perm(), err)
	}
	// A writable ancestor is the user's setup; the uam directory under it still opens, and is still made private.
	if err := os.Chmod(base, 0o777); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(base, "uam")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	r, err = openConnectionRegistry(context.Background(), child, testToken)
	if err != nil {
		t.Fatalf("registry under a writable ancestor refused: %v", err)
	}
	r.close()
	if info, err := os.Stat(child); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("child mode = %v, %v; want 0700", info.Mode().Perm(), err)
	}
	if info, err := os.Stat(base); err != nil || info.Mode().Perm() != 0o777 {
		t.Fatalf("ancestor mode = %v, %v; want left alone", info.Mode().Perm(), err)
	}
	// A regular file in the directory's place is refused by name.
	file := filepath.Join(base, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openConnectionRoot(file); err == nil || !strings.Contains(err.Error(), file) {
		t.Fatalf("file accepted as registry directory: %v", err)
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
