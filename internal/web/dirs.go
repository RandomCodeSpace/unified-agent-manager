package web

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

const (
	// maxDirEntries caps one folder listing.
	maxDirEntries = 1000
	// maxDirNameBytes is the longest name most Linux file systems take.
	maxDirNameBytes = 255
)

// DirEntry is one folder in a listing. Name is for display; Path is exact.
type DirEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Git means the folder holds .git, a directory or, in a linked worktree,
	// a file.
	Git    bool `json:"git"`
	Hidden bool `json:"hidden"`
	// Link means the entry is a symbolic link to a directory.
	Link bool `json:"link"`
}

// DirList answers GET /api/fs/dirs. Parent is empty at /.
type DirList struct {
	Path      string     `json:"path"`
	Parent    string     `json:"parent,omitempty"`
	Entries   []DirEntry `json:"entries"`
	Truncated bool       `json:"truncated"`
}

// listDirs lists the folders in p, the service user's home when p is empty:
// directories and links to them, never files. At most limit entries are
// returned, sorted by name without regard to case.
func listDirs(p string, limit int) (DirList, error) {
	if p == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return DirList{}, fmt.Errorf("find the home directory: %w", err)
		}
		p = filepath.Clean(home)
	}
	if err := checkDir("path", p); err != nil {
		return DirList{}, err
	}
	all, err := os.ReadDir(p)
	if err != nil {
		return DirList{}, dirError("path", err)
	}
	out := DirList{Path: p, Entries: []DirEntry{}}
	if p != filepath.Dir(p) {
		out.Parent = filepath.Dir(p)
	}
	for _, e := range all {
		name := e.Name()
		if !utf8.ValidString(name) {
			continue
		}
		full := filepath.Join(p, name)
		link := e.Type()&fs.ModeSymlink != 0
		if link {
			if info, err := os.Stat(full); err != nil || !info.IsDir() {
				continue
			}
		} else if !e.IsDir() {
			continue
		}
		out.Entries = append(out.Entries, DirEntry{Name: name, Path: full, Hidden: strings.HasPrefix(name, "."), Link: link})
	}
	slices.SortFunc(out.Entries, func(a, b DirEntry) int {
		if c := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	if len(out.Entries) > limit {
		out.Entries, out.Truncated = out.Entries[:limit], true
	}
	for i := range out.Entries {
		e := &out.Entries[i]
		if info, err := os.Stat(filepath.Join(e.Path, ".git")); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			e.Git = true
		}
		e.Name = cleanTitle(e.Name)
	}
	log.Debug("web folders listed", "path", p, "entries", len(out.Entries), "truncated", out.Truncated)
	return out, nil
}

// makeDir creates the folder name inside parent with mode 0755 and returns
// its path. It never creates missing parents.
func makeDir(parent, name string) (string, error) {
	if parent == "" {
		return "", newError(http.StatusBadRequest, "parent is required")
	}
	if err := checkDirName(name); err != nil {
		return "", err
	}
	if err := checkDir("parent", parent); err != nil {
		return "", err
	}
	p := filepath.Join(parent, name)
	if err := os.Mkdir(p, 0o755); err != nil { // #nosec G301 -- a Project folder, created as the owner's shell would.
		if errors.Is(err, fs.ErrExist) {
			return "", newError(http.StatusConflict, "a file or folder with this name already exists")
		}
		return "", dirError("parent", err)
	}
	log.Info("web folder created", "path", p)
	return p, nil
}

// checkDir refuses a path that is not absolute and in clean form, before the
// file system is asked, and then one that is not an existing directory.
func checkDir(field, p string) error {
	switch {
	case !utf8.ValidString(p) || strings.ContainsRune(p, 0):
		return newError(http.StatusBadRequest, "%s is not a valid path", field)
	case !filepath.IsAbs(p):
		return newError(http.StatusBadRequest, "%s must be an absolute path", field)
	case filepath.Clean(p) != p:
		return newError(http.StatusBadRequest, "%s must be in clean form", field)
	}
	info, err := os.Stat(p)
	if err != nil {
		return dirError(field, err)
	}
	if !info.IsDir() {
		return newError(http.StatusBadRequest, "%s is not a directory", field)
	}
	return nil
}

// checkDirName refuses a new folder name that is not one plain path element.
func checkDirName(name string) error {
	switch {
	case name == "":
		return newError(http.StatusBadRequest, "name is required")
	case !utf8.ValidString(name):
		return newError(http.StatusBadRequest, "name is not valid UTF-8")
	case len(name) > maxDirNameBytes:
		return newError(http.StatusBadRequest, "name is longer than %d bytes", maxDirNameBytes)
	case name == "." || name == "..":
		return newError(http.StatusBadRequest, "name cannot be . or ..")
	case strings.ContainsRune(name, '/'):
		return newError(http.StatusBadRequest, "name cannot contain /")
	case strings.ContainsFunc(name, unicode.IsControl):
		return newError(http.StatusBadRequest, "name cannot contain control characters")
	case strings.TrimSpace(name) != name:
		return newError(http.StatusBadRequest, "name cannot start or end with a space")
	}
	return nil
}

// dirError maps a file system failure on field to its HTTP status.
func dirError(field string, err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR):
		return newError(http.StatusNotFound, "%s does not exist", field)
	case errors.Is(err, fs.ErrPermission):
		return newError(http.StatusForbidden, "permission denied")
	}
	return fmt.Errorf("%s: %w", field, err)
}
