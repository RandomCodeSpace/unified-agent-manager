package web

import (
	"errors"
	"os"
	"path/filepath"
)

// writeFileAtomic replaces path with data, owner-only (0600). It writes a
// new temporary file named by pattern (os.CreateTemp) beside path, syncs it
// to disk and renames it over path, so a crash leaves the old file or the
// new one, never part of either. path's directory must exist.
func writeFileAtomic(path, pattern string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), pattern)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	if err = errors.Join(err, tmp.Close()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
