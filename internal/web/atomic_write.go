package web

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// removeTempFiles removes the files in dir that writeFileAtomic left when the
// process was killed mid-write: a name of one of prefixes and the digits
// os.CreateTemp adds. The caller is the only writer of those files and writes
// none meanwhile.
func removeTempFiles(dir string, prefixes ...string) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		temp := slices.ContainsFunc(prefixes, func(p string) bool {
			digits, ok := strings.CutPrefix(e.Name(), p)
			return ok && digits != "" && strings.Trim(digits, "0123456789") == ""
		})
		if temp && e.Type().IsRegular() {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
