package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func providerIdentityTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func providerIdentityTestPath(t *testing.T, dir, name string) string {
	t.Helper()
	path, err := ProviderIdentityPath(dir, name)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func writeProviderIdentityFixture(t *testing.T, path, contents string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestProviderIdentityPath(t *testing.T) {
	dir := providerIdentityTestDir(t)
	name := "uam-opencode-a1b2c3d4"

	got, err := ProviderIdentityPath(dir, name)
	if err != nil {
		t.Fatalf("ProviderIdentityPath: %v", err)
	}
	want := filepath.Join(dir, name+".provider.json")
	if got != want {
		t.Fatalf("ProviderIdentityPath = %q, want %q", got, want)
	}
	if _, err := ProviderIdentityPath(dir, "../escape"); !errors.Is(err, ErrInvalidSessionName) {
		t.Fatalf("invalid name error = %v, want ErrInvalidSessionName", err)
	}

	missingDir := filepath.Join(t.TempDir(), "not-created-yet")
	got, err = ProviderIdentityPath(missingDir, name)
	if err != nil {
		t.Fatalf("ProviderIdentityPath before runtime directory creation: %v", err)
	}
	if want := filepath.Join(missingDir, name+".provider.json"); got != want {
		t.Fatalf("ProviderIdentityPath before runtime directory creation = %q, want %q", got, want)
	}
}

func TestProviderIdentityAtomicRoundTrip(t *testing.T) {
	dir := providerIdentityTestDir(t)
	name := "uam-opencode-a1b2c3d4"
	if err := WriteProviderIdentity(dir, name, "ses_abc123"); err != nil {
		t.Fatalf("WriteProviderIdentity: %v", err)
	}

	path := providerIdentityTestPath(t, dir, name)
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("identity mode = %v, want regular 0600", info.Mode())
	}
	got, err := ReadProviderIdentity(dir, name)
	if err != nil {
		t.Fatalf("ReadProviderIdentity: %v", err)
	}
	if got != "ses_abc123" {
		t.Fatalf("ReadProviderIdentity = %q, want ses_abc123", got)
	}

	temps, err := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temps) != 0 {
		t.Fatalf("temporary identity files remain after success: %v", temps)
	}
}

func TestProviderIdentityWriteReplacesSafeDestination(t *testing.T) {
	const (
		name     = "uam-opencode-a1b2c3d4"
		original = `{"session_name":"uam-opencode-a1b2c3d4","provider_session_id":"ses_original"}`
	)
	dir := providerIdentityTestDir(t)
	path := providerIdentityTestPath(t, dir, name)
	writeProviderIdentityFixture(t, path, original, 0o600)

	if err := WriteProviderIdentity(dir, name, "ses_replacement"); err != nil {
		t.Fatalf("WriteProviderIdentity replacement: %v", err)
	}
	got, err := ReadProviderIdentity(dir, name)
	if err != nil {
		t.Fatalf("ReadProviderIdentity replacement: %v", err)
	}
	if got != "ses_replacement" {
		t.Fatalf("ReadProviderIdentity replacement = %q, want ses_replacement", got)
	}
}

func TestProviderIdentityWriteRejectsUnsafeDestination(t *testing.T) {
	const (
		name     = "uam-opencode-a1b2c3d4"
		original = `{"session_name":"uam-opencode-a1b2c3d4","provider_session_id":"ses_original"}`
	)

	t.Run("symlink", func(t *testing.T) {
		dir := providerIdentityTestDir(t)
		path := providerIdentityTestPath(t, dir, name)
		target := filepath.Join(t.TempDir(), "target.json")
		writeProviderIdentityFixture(t, target, original, 0o600)
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}

		if err := WriteProviderIdentity(dir, name, "ses_replacement"); err == nil {
			t.Error("WriteProviderIdentity must reject a symlink destination")
		}
		gotTarget, err := os.Readlink(path)
		if err != nil {
			t.Fatalf("destination is no longer a symlink: %v", err)
		}
		if gotTarget != target {
			t.Fatalf("symlink target = %q, want %q", gotTarget, target)
		}
		got, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != original {
			t.Fatalf("symlink target contents = %q, want original contents", got)
		}
	})

	t.Run("non-regular object", func(t *testing.T) {
		dir := providerIdentityTestDir(t)
		path := providerIdentityTestPath(t, dir, name)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		marker := filepath.Join(path, "marker")
		if err := os.WriteFile(marker, []byte("unchanged"), 0o600); err != nil {
			t.Fatal(err)
		}

		if err := WriteProviderIdentity(dir, name, "ses_replacement"); err == nil {
			t.Error("WriteProviderIdentity must reject a non-regular destination")
		}
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() {
			t.Fatalf("destination mode = %v, want directory", info.Mode())
		}
		got, err := os.ReadFile(marker)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "unchanged" {
			t.Fatalf("directory marker contents = %q, want unchanged", got)
		}
	})

	t.Run("wrong mode", func(t *testing.T) {
		dir := providerIdentityTestDir(t)
		path := providerIdentityTestPath(t, dir, name)
		writeProviderIdentityFixture(t, path, original, 0o644)

		if err := WriteProviderIdentity(dir, name, "ses_replacement"); err == nil {
			t.Error("WriteProviderIdentity must reject mode 0644")
		}
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o644 {
			t.Fatalf("destination mode = %04o, want 0644", info.Mode().Perm())
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != original {
			t.Fatalf("destination contents = %q, want original contents", got)
		}
	})

	t.Run("foreign owner", func(t *testing.T) {
		dir := providerIdentityTestDir(t)
		path := providerIdentityTestPath(t, dir, name)
		writeProviderIdentityFixture(t, path, original, 0o600)
		foreignUID := os.Getuid() + 1
		if foreignUID == os.Getuid() {
			foreignUID++
		}
		if err := os.Chown(path, foreignUID, -1); err != nil {
			t.Skipf("platform does not permit foreign-owner fixture: %v", err)
		}

		if err := WriteProviderIdentity(dir, name, "ses_replacement"); err == nil {
			t.Error("WriteProviderIdentity must reject a foreign-owned destination")
		}
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			t.Fatalf("destination ownership is unavailable")
		}
		if int(stat.Uid) != foreignUID {
			t.Fatalf("destination uid = %d, want %d", stat.Uid, foreignUID)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != original {
			t.Fatalf("destination contents = %q, want original contents", got)
		}
	})
}

func TestProviderIdentityMissingReturnsEmpty(t *testing.T) {
	dir := providerIdentityTestDir(t)
	got, err := ReadProviderIdentity(dir, "uam-opencode-a1b2c3d4")
	if err != nil || got != "" {
		t.Fatalf("ReadProviderIdentity missing = (%q, %v), want empty success", got, err)
	}
}

func TestProviderIdentityRejectsInvalidProviderIDBeforeWrite(t *testing.T) {
	dir := providerIdentityTestDir(t)
	name := "uam-opencode-a1b2c3d4"
	if err := WriteProviderIdentity(dir, name, "-unsafe"); err == nil {
		t.Fatal("WriteProviderIdentity must reject an invalid provider ID")
	}
	path := providerIdentityTestPath(t, dir, name)
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("invalid provider ID created %s: %v", path, err)
	}
}

func TestProviderIdentityReadFailsClosed(t *testing.T) {
	const (
		name    = "uam-opencode-a1b2c3d4"
		valid   = `{"session_name":"uam-opencode-a1b2c3d4","provider_session_id":"ses_abc123"}`
		foreign = `{"session_name":"uam-opencode-a1b2c3d4","provider_session_id":"ses_foreign"}`
	)

	t.Run("symlink", func(t *testing.T) {
		dir := providerIdentityTestDir(t)
		path := providerIdentityTestPath(t, dir, name)
		target := filepath.Join(t.TempDir(), "target.json")
		writeProviderIdentityFixture(t, target, valid, 0o600)
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadProviderIdentity(dir, name); err == nil {
			t.Fatal("ReadProviderIdentity must reject a symlink")
		}
	})

	t.Run("directory", func(t *testing.T) {
		dir := providerIdentityTestDir(t)
		path := providerIdentityTestPath(t, dir, name)
		if err := os.Mkdir(path, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadProviderIdentity(dir, name); err == nil {
			t.Fatal("ReadProviderIdentity must reject a directory")
		}
	})

	t.Run("mode 0644", func(t *testing.T) {
		dir := providerIdentityTestDir(t)
		path := providerIdentityTestPath(t, dir, name)
		writeProviderIdentityFixture(t, path, valid, 0o644)
		if _, err := ReadProviderIdentity(dir, name); err == nil {
			t.Fatal("ReadProviderIdentity must reject mode 0644")
		}
	})

	t.Run("foreign owner", func(t *testing.T) {
		dir := providerIdentityTestDir(t)
		path := providerIdentityTestPath(t, dir, name)
		writeProviderIdentityFixture(t, path, foreign, 0o600)
		foreignUID := os.Getuid() + 1
		if foreignUID == os.Getuid() {
			foreignUID++
		}
		if err := os.Chown(path, foreignUID, -1); err != nil {
			t.Skipf("platform does not permit foreign-owner fixture: %v", err)
		}
		if _, err := ReadProviderIdentity(dir, name); err == nil {
			t.Fatal("ReadProviderIdentity must reject a foreign-owned file")
		}
	})

	t.Run("larger than limit", func(t *testing.T) {
		dir := providerIdentityTestDir(t)
		path := providerIdentityTestPath(t, dir, name)
		writeProviderIdentityFixture(t, path, strings.Repeat("x", 1025), 0o600)
		if _, err := ReadProviderIdentity(dir, name); err == nil {
			t.Fatal("ReadProviderIdentity must reject an oversized file")
		}
	})

	for _, tt := range []struct {
		name     string
		contents string
	}{
		{name: "malformed JSON", contents: `{"session_name":`},
		{name: "trailing JSON value", contents: valid + ` {}`},
		{name: "embedded name mismatch", contents: `{"session_name":"uam-opencode-deadbeef","provider_session_id":"ses_abc123"}`},
		{name: "invalid embedded provider ID", contents: `{"session_name":"uam-opencode-a1b2c3d4","provider_session_id":"-unsafe"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := providerIdentityTestDir(t)
			path := providerIdentityTestPath(t, dir, name)
			writeProviderIdentityFixture(t, path, tt.contents, 0o600)
			if _, err := ReadProviderIdentity(dir, name); err == nil {
				t.Fatalf("ReadProviderIdentity must reject %s", tt.name)
			}
		})
	}
}

func TestProviderIdentityFailedWritePreservesPreviousValue(t *testing.T) {
	dir := providerIdentityTestDir(t)
	name := "uam-opencode-a1b2c3d4"
	if err := WriteProviderIdentity(dir, name, "ses_original"); err != nil {
		t.Fatal(err)
	}
	if err := WriteProviderIdentity(dir, name, "-invalid"); err == nil {
		t.Fatal("replacement write must reject an invalid provider ID")
	}
	got, err := ReadProviderIdentity(dir, name)
	if err != nil || got != "ses_original" {
		t.Fatalf("identity after failed write = (%q, %v), want ses_original", got, err)
	}
}
