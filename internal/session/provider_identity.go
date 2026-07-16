package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
	"golang.org/x/sys/unix"
)

const maxProviderIdentityBytes = 1024

type providerIdentity struct {
	SessionName       string `json:"session_name"`
	ProviderSessionID string `json:"provider_session_id"`
}

// ProviderIdentityPath returns the canonical provider identity handoff path
// inside the verified native-session runtime boundary.
func ProviderIdentityPath(dir, name string) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".provider.json"), nil
}

// WriteProviderIdentity atomically publishes a provider session identity in
// the native-session runtime directory.
func WriteProviderIdentity(dir, name, providerSessionID string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if !store.ValidProviderSessionID(providerSessionID) {
		return fmt.Errorf("invalid provider session ID")
	}
	if err := VerifyDir(dir); err != nil {
		return err
	}
	path := filepath.Join(dir, name+".provider.json")
	info, err := os.Lstat(path)
	if err == nil {
		if err := verifyProviderIdentityFile(path, info); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat provider identity destination: %w", err)
	}
	data, err := json.Marshal(providerIdentity{SessionName: name, ProviderSessionID: providerSessionID})
	if err != nil {
		return fmt.Errorf("encode provider identity: %w", err)
	}

	tmp, err := os.CreateTemp(dir, "."+name+".provider-*.tmp")
	if err != nil {
		return fmt.Errorf("create provider identity temporary file: %w", err)
	}
	tmpPath := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		_ = os.Remove(tmpPath)
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("restrict provider identity temporary file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write provider identity temporary file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync provider identity temporary file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		closed = true
		return fmt.Errorf("close provider identity temporary file: %w", err)
	}
	closed = true
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("publish provider identity: %w", err)
	}
	return nil
}

// ReadProviderIdentity returns the verified provider session identity. A
// missing handoff is advisory and returns an empty identity without error.
func ReadProviderIdentity(dir, name string) (string, error) {
	path, err := ProviderIdentityPath(dir, name)
	if err != nil {
		return "", err
	}
	if err := VerifyDir(dir); err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("stat provider identity: %w", err)
	}
	if err := verifyProviderIdentityFile(path, info); err != nil {
		return "", err
	}

	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0) // #nosec G304 -- validated canonical path inside a verified owner-only directory.
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("open provider identity: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	defer func() { _ = file.Close() }()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return "", fmt.Errorf("inspect provider identity descriptor: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || os.FileMode(stat.Mode).Perm() != 0o600 || int(stat.Uid) != os.Getuid() {
		return "", fmt.Errorf("provider identity changed during verification")
	}

	data, err := io.ReadAll(io.LimitReader(file, maxProviderIdentityBytes+1))
	if err != nil {
		return "", fmt.Errorf("read provider identity: %w", err)
	}
	if len(data) > maxProviderIdentityBytes {
		return "", fmt.Errorf("provider identity exceeds %d bytes", maxProviderIdentityBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var identity providerIdentity
	if err := decoder.Decode(&identity); err != nil {
		return "", fmt.Errorf("decode provider identity: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return "", fmt.Errorf("provider identity contains a trailing JSON value")
		}
		return "", fmt.Errorf("decode provider identity trailing data: %w", err)
	}
	if identity.SessionName != name {
		return "", fmt.Errorf("provider identity session name %q does not match %q", identity.SessionName, name)
	}
	if !store.ValidProviderSessionID(identity.ProviderSessionID) {
		return "", fmt.Errorf("provider identity contains an invalid provider session ID")
	}
	return identity.ProviderSessionID, nil
}

func verifyProviderIdentityFile(path string, info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("provider identity %s is not a regular non-symlink file", path)
	}
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("provider identity %s has unsafe mode %04o; want 0600", path, info.Mode().Perm())
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("provider identity %s ownership is unavailable", path)
	}
	if int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("provider identity %s is owned by uid %d, not the current user", path, stat.Uid)
	}
	return nil
}
