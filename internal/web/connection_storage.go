package web

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/google/uuid"
)

// openConnectionRoot opens the configuration directory, which uam creates and
// owns, pinned by descriptor so a later rename cannot redirect a save. The
// directory is made private (0700) when it is not, since uam is its owner;
// only a directory owned by another user is refused, by name. Ancestors are
// not inspected: a shared home or a group-writable parent is the user's
// setup, not a reason for uam web not to start.
func openConnectionRoot(dir string) (*os.Root, error) {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("connection storage: %s is not a directory", absolute)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int64(st.Uid) != int64(os.Geteuid()) {
		return nil, fmt.Errorf("connection storage: %s is owned by another user; run uam as its owner or point UAM_CONFIG_DIR at a directory of your own", absolute)
	}
	if info.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(absolute, 0o700); err != nil { // #nosec G302 -- a directory needs the execute bit; 0700 is the private mode uam creates it with.
			return nil, fmt.Errorf("connection storage: make %s private: %w", absolute, err)
		}
	}
	return os.OpenRoot(absolute)
}

// ownedByUser reports whether info belongs to the user uam runs as.
func ownedByUser(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int64(st.Uid) == int64(os.Geteuid())
}

func writeConnectionDisk(root *os.Root, data []byte, initial bool) error {
	id, err := uuid.NewRandom()
	if err != nil {
		return err
	}
	name := ".connections-" + id.String()
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove(name) }()
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return err
	}
	if initial {
		// Publish identity once, including concurrent first starts.
		return root.Link(name, connectionFileName)
	}
	return root.Rename(name, connectionFileName)
}
