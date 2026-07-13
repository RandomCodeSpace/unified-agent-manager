package providerstate

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	uamlog "github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

func TestRootOwnedSymlinkPolicyIsAncestorOnly(t *testing.T) {
	if !allowRootOwnedSymlink(0, false) {
		t.Fatal("root-owned system ancestor should be eligible")
	}
	if allowRootOwnedSymlink(0, true) {
		t.Fatal("the provider-state base itself must not be a symlink")
	}
	if allowRootOwnedSymlink(os.Getuid(), false) && os.Getuid() != 0 {
		t.Fatal("user-owned ancestor symlink accepted")
	}
}

func TestTrustedBaseRejectsSymlinkAtBase(t *testing.T) {
	parent, target := t.TempDir(), t.TempDir()
	base := filepath.Join(parent, "state")
	if err := os.Symlink(target, base); err != nil {
		t.Fatal(err)
	}
	if err := EnsureTrustedBase(base); err == nil {
		t.Fatal("symlinked provider-state base accepted")
	}
}

func TestResolvedAncestryWarnsForWritableComponent(t *testing.T) {
	unsafe := filepath.Join(t.TempDir(), "unsafe")
	if err := os.Mkdir(unsafe, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unsafe, 0o770); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	previous := uamlog.SetLogger(slog.New(slog.NewTextHandler(&output, nil)))
	t.Cleanup(func() { uamlog.SetLogger(previous) })

	if err := verifyResolvedAncestry(unsafe); err != nil {
		t.Fatalf("writable resolved target ancestry blocked: %v", err)
	}
	if err := verifyResolvedAncestry(unsafe); err != nil {
		t.Fatalf("repeated writable ancestry check blocked: %v", err)
	}
	warning := output.String()
	if !strings.Contains(warning, "provider state ancestor is group/other-writable") ||
		!strings.Contains(warning, unsafe) || !strings.Contains(warning, "0770") {
		t.Fatalf("missing actionable writable-ancestor warning: %q", warning)
	}
	if count := strings.Count(warning, "provider state ancestor is group/other-writable"); count != 1 {
		t.Fatalf("writable ancestor warning count = %d, want 1: %q", count, warning)
	}
}

func TestRootOwnedSystemSymlinkAncestorUsesTrustedResolvedTarget(t *testing.T) {
	for _, link := range []string{"/var", "/lib", "/lib64"} {
		info, err := os.Lstat(link)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 {
			continue
		}
		resolved, err := filepath.EvalSymlinks(link)
		if err != nil {
			continue
		}
		entries, err := os.ReadDir(resolved)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			logical := filepath.Join(link, entry.Name())
			if err := VerifyTrustedBase(logical); err != nil {
				t.Fatalf("trusted root symlink ancestor %s rejected: %v", logical, err)
			}
			return
		}
	}
	t.Skip("no root-owned system symlink ancestor with a directory target")
}
