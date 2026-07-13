// Package providerstate validates the persistent root used for provider-owned
// launch metadata. Ownership, file type, and symlink violations fail closed;
// writable ancestors are reported while UAM-owned children remain owner-only.
package providerstate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	uamlog "github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

var warnedWritableAncestors sync.Map

// EnsureTrustedBase creates base when absent and verifies that it is a real,
// current-user-owned directory. Group/other-writable ancestors emit a warning
// because some managed systems do not allow users to change those modes.
func EnsureTrustedBase(base string) error {
	return walkTrustedPath(base, true)
}

// VerifyTrustedBase performs the read-side form without mutation.
func VerifyTrustedBase(base string) error {
	return walkTrustedPath(base, false)
}

func walkTrustedPath(base string, create bool) error {
	if !filepath.IsAbs(base) {
		return fmt.Errorf("provider state base must be absolute")
	}
	clean := filepath.Clean(base)
	volume := filepath.VolumeName(clean)
	path := string(os.PathSeparator)
	if volume != "" {
		path = volume + string(os.PathSeparator)
	}
	rootInfo, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if err := verifyComponent(path, rootInfo); err != nil {
		return err
	}
	rawParts := strings.Split(strings.TrimPrefix(clean, path), string(os.PathSeparator))
	parts := make([]string, 0, len(rawParts))
	for _, part := range rawParts {
		if part != "" {
			parts = append(parts, part)
		}
	}
	for i, part := range parts {
		candidate := filepath.Join(path, part)
		info, err := os.Lstat(candidate)
		if os.IsNotExist(err) && create {
			if err := os.Mkdir(candidate, 0o700); err != nil && !os.IsExist(err) {
				return err
			}
			info, err = os.Lstat(candidate)
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			st, ok := info.Sys().(*syscall.Stat_t)
			if !ok || !allowRootOwnedSymlink(int(st.Uid), i == len(parts)-1) {
				return fmt.Errorf("provider state ancestor %s is an untrusted symlink", candidate)
			}
			resolved, err := filepath.EvalSymlinks(candidate)
			if err != nil {
				return err
			}
			if err := verifyResolvedAncestry(resolved); err != nil {
				return err
			}
			path = resolved
			continue
		}
		if err := verifyComponent(candidate, info); err != nil {
			return err
		}
		path = candidate
	}
	return nil
}

func allowRootOwnedSymlink(uid int, isFinal bool) bool { return uid == 0 && !isFinal }

// verifyResolvedAncestry validates the physical target of an eligible
// root-owned system symlink without following any further unchecked link.
func verifyResolvedAncestry(target string) error {
	if !filepath.IsAbs(target) {
		return fmt.Errorf("resolved provider state path must be absolute")
	}
	clean := filepath.Clean(target)
	volume := filepath.VolumeName(clean)
	path := string(os.PathSeparator)
	if volume != "" {
		path = volume + string(os.PathSeparator)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if err := verifyComponent(path, info); err != nil {
		return err
	}
	for _, part := range strings.Split(strings.TrimPrefix(clean, path), string(os.PathSeparator)) {
		if part == "" {
			continue
		}
		path = filepath.Join(path, part)
		info, err = os.Lstat(path)
		if err != nil {
			return err
		}
		if err := verifyComponent(path, info); err != nil {
			return err
		}
	}
	return nil
}

func verifyComponent(path string, info os.FileInfo) error {
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("provider state ancestor %s is not a real directory", path)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("provider state ancestor %s has unknown ownership", path)
	}
	uid := int(st.Uid)
	if uid != 0 && uid != os.Getuid() {
		return fmt.Errorf("provider state ancestor %s is owned by uid %d", path, uid)
	}
	if info.Mode().Perm()&0o022 != 0 {
		rootSticky := uid == 0 && info.Mode()&os.ModeSticky != 0
		if !rootSticky {
			if _, loaded := warnedWritableAncestors.LoadOrStore(path, struct{}{}); !loaded {
				uamlog.Warn("provider state ancestor is group/other-writable",
					"path", path,
					"mode", fmt.Sprintf("%04o", info.Mode().Perm()),
				)
			}
		}
	}
	return nil
}
