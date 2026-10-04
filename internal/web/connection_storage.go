package web

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/google/uuid"
)

// Pin the actual configuration directory. A group-writable parent could replace
// a checked pathname, so walk from the filesystem root through owned descriptors.
// Sticky shared roots such as /tmp are allowed only for an owned child.
func openConnectionRoot(dir string) (*os.Root, error) {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(string(filepath.Separator))
	if err != nil {
		return nil, err
	}
	fail := func() (*os.Root, error) {
		_ = root.Close()
		return nil, errors.New("connection storage requires an owned directory protected from replacement")
	}
	parts := strings.Split(strings.TrimPrefix(canonical, string(filepath.Separator)), string(filepath.Separator))
	stickyParent := false
	privateAncestor := false
	for i := -1; i < len(parts); i++ {
		if i >= 0 && parts[i] != "" {
			next, openErr := root.OpenRoot(parts[i])
			if openErr != nil {
				_ = root.Close()
				return nil, openErr
			}
			_ = root.Close()
			root = next
		}
		info, statErr := root.Stat(".")
		if statErr != nil || !info.IsDir() {
			return fail()
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return fail()
		}
		owned := int64(st.Uid) == int64(os.Geteuid())
		if (!owned && st.Uid != 0) || (stickyParent && !owned) {
			return fail()
		}
		writable := info.Mode().Perm()&0o022 != 0
		stickyParent = writable && info.Mode()&os.ModeSticky != 0
		if writable && !stickyParent && !privateAncestor {
			return fail()
		}
		if i == len(parts)-1 && (!owned || (writable && !privateAncestor)) {
			return fail()
		}
		privateAncestor = privateAncestor || (owned && info.Mode().Perm()&0o077 == 0)
	}
	return root, nil
}

func privateConnectionFile(info os.FileInfo) bool {
	if info == nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return false
	}
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
