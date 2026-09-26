// Package daemonruntime owns the web daemon's private runtime directory and
// process identity checks. It does not launch or control terminal sessions.
package daemonruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// DefaultDir preserves UAM_SESSION_DIR and the existing per-user location so
// upgrades can find and safely stop an already-running web daemon. The temp
// directory survives logout, unlike XDG_RUNTIME_DIR on some hosts.
func DefaultDir() string {
	if v := os.Getenv("UAM_SESSION_DIR"); v != "" {
		return v
	}
	return filepath.Join(os.TempDir(), "uam-"+strconv.Itoa(os.Getuid()))
}

// EnsureDir creates or restricts a real, current-user-owned runtime directory.
func EnsureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create session dir %s: %w", dir, err)
	}
	if err := verifyDirIdentity(dir); err != nil {
		return err
	}
	// MkdirAll is a no-op on an existing directory. Creation paths retain the
	// historical repair behavior for a directory owned by this user; read and
	// control paths use VerifyDir and fail closed instead.
	if err := os.Chmod(dir, 0o700); err != nil { // #nosec G302 -- directory needs the execute bit; owner-only.
		return fmt.Errorf("restrict session dir %s: %w", dir, err)
	}
	return VerifyDir(dir)
}

// VerifyDir checks ownership and owner-only permissions without changing files.
func VerifyDir(dir string) error {
	if err := verifyDirIdentity(dir); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("stat session dir %s: %w", dir, err)
	}
	if info.Mode().Perm() != 0o700 {
		return fmt.Errorf("session dir %s has unsafe mode %04o; want 0700", dir, info.Mode().Perm())
	}
	return nil
}

func verifyDirIdentity(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("stat session dir %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("session dir %s is not a directory", dir)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return fmt.Errorf("session dir %s is owned by uid %d, not the current user", dir, st.Uid)
	}
	return nil
}

// ProcAlive probes liveness; callers must also check identity before signaling.
func ProcAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

// ProcStartTime returns a kernel process identity, or zero if unavailable.
func ProcStartTime(pid int) int64 { return procStartTime(pid) }
