package daemonruntime

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestVerifyDirRejectsUnsafeRuntimeDirectories(t *testing.T) {
	parent := t.TempDir()

	missing := filepath.Join(parent, "missing")
	if err := VerifyDir(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("VerifyDir missing error = %v, want os.ErrNotExist", err)
	}

	permissive := filepath.Join(parent, "permissive")
	if err := os.Mkdir(permissive, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(permissive, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := VerifyDir(permissive); err == nil {
		t.Fatal("VerifyDir must reject a group/world-accessible runtime directory")
	}
	info, err := os.Stat(permissive)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("VerifyDir mutated mode to %o, want read-only verification", got)
	}

	target := filepath.Join(parent, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := VerifyDir(link); err == nil {
		t.Fatal("VerifyDir must reject a symlink runtime directory")
	}
}

func TestDefaultDirResolution(t *testing.T) {
	t.Setenv("UAM_SESSION_DIR", "/custom/dir")
	if got := DefaultDir(); got != "/custom/dir" {
		t.Fatalf("DefaultDir with override = %q", got)
	}
	t.Setenv("UAM_SESSION_DIR", "")
	// XDG_RUNTIME_DIR must NOT be used: logind deletes it on logout while
	// detached hosts keep running, which would strand live sessions.
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	want := filepath.Join(os.TempDir(), "uam-"+strconv.Itoa(os.Getuid()))
	if got := DefaultDir(); got != want {
		t.Fatalf("DefaultDir = %q, want per-uid temp dir %q", got, want)
	}
}

func TestEnsureDirRejectsNonDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "occupied")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDir(path); err == nil {
		t.Fatal("EnsureDir over a regular file must fail")
	}
}

func TestEnsureDirAcceptsOwnDirAndRestrictsMode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "runtime")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDir(dir); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %o, want 0700", info.Mode().Perm())
	}
}

func TestProcStartTimeReadsSelf(t *testing.T) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("no /proc on this platform")
	}
	if got := procStartTime(os.Getpid()); got <= 0 {
		t.Fatalf("procStartTime(self) = %d, want > 0", got)
	}
	if got := procStartTime(0); got != 0 {
		t.Fatalf("procStartTime(0) = %d, want 0", got)
	}
}

// EnsureDir creates or repairs only a real directory: a symlink to one is
// refused and its target is left as it was.
func TestEnsureDirRejectsSymlink(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDir(link); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("EnsureDir symlink = %v, want a refusal", err)
	}
	if info, err := os.Stat(target); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("EnsureDir changed the symlink target: %v, %v", info, err)
	}
}

// A runtime directory owned by another user is refused.
func TestVerifyDirRejectsAnotherUsersDirectory(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root owns /")
	}
	if err := VerifyDir("/"); err == nil || !strings.Contains(err.Error(), "not the current user") {
		t.Fatalf("VerifyDir(/) = %v, want an ownership refusal", err)
	}
}

// Liveness and identity probes never address process groups or report a
// process that does not exist.
func TestProcProbesRefuseInvalidPIDs(t *testing.T) {
	for _, pid := range []int{0, -1} {
		if ProcAlive(pid) {
			t.Fatalf("ProcAlive(%d) = true; kill(%d, 0) probes a process group", pid, pid)
		}
	}
	// Above any Linux pid_max (at most 2^22), so no process has it.
	const absent = 1 << 30
	if ProcAlive(absent) {
		t.Fatal("ProcAlive reported a PID no process can have")
	}
	if got := ProcStartTime(absent); got != 0 {
		t.Fatalf("ProcStartTime(absent) = %d, want 0", got)
	}
}
