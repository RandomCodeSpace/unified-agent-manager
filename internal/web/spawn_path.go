package web

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// spawnPathProject is the Project a uam_create_task path starts its Thread
// in. A path in a Project's folder, or below it, uses the Project with the
// deepest folder, and folder names the subfolder ("" for the Project's own).
// A path in no Project must be inside the home folder: uam asks the owner to
// confirm, then makes the folder when it is missing and adds it as a
// Project. Adding a Project always waits for that answer.
func (m *Manager) spawnPathProject(ctx context.Context, taskID, callID, raw string) (projectID, folder string, err error) {
	m.mu.Lock()
	caller := m.sessions[taskID]
	var workdir string
	if caller != nil {
		workdir = caller.workdir
	}
	m.mu.Unlock()
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", errors.New("the service home folder is unavailable")
	}
	home = resolvedPath(home)
	path, err := spawnFolder(raw, home, workdir)
	if err != nil {
		return "", "", err
	}
	if id, dir := m.projectOf(path); id != "" {
		if path != dir {
			folder = path
		}
		return id, folder, nil
	}
	if !withinDir(home, path) || path == home {
		return "", "", fmt.Errorf("%s is in no project, and uam adds new projects only inside the home folder %s", path, home)
	}
	info, err := os.Stat(path)
	missing := errors.Is(err, fs.ErrNotExist)
	switch {
	case err != nil && !missing:
		return "", "", fmt.Errorf("cannot read %s: %w", path, err)
	case err == nil && !info.IsDir():
		return "", "", fmt.Errorf("%s is a file, not a folder", path)
	}
	question := fmt.Sprintf("Add %s as a new project and start a thread there?", path)
	if missing {
		question += " The folder does not exist yet; uam makes it."
	}
	ok, err := m.askOwner(ctx, taskID, callID, "Add a project", question, "Add project")
	if err != nil {
		return "", "", err
	}
	if !ok {
		return "", "", fmt.Errorf("the owner declined adding %s as a project; do not ask again with this tool, ask the owner in your reply how to go on", path)
	}
	if missing {
		if err := os.MkdirAll(path, 0o750); err != nil {
			return "", "", fmt.Errorf("make %s: %w", path, err)
		}
	}
	p, err := m.AddProject(path, "")
	var exists *Error
	if errors.As(err, &exists) && exists.ProjectID != "" {
		return exists.ProjectID, "", nil
	}
	if err != nil {
		return "", "", err
	}
	return p.ID, "", nil
}

// spawnFolder is raw as an absolute, clean path with its links resolved:
// "~" and "~/..." are in home, a relative path is in workdir.
func spawnFolder(raw, home, workdir string) (string, error) {
	path := strings.TrimSpace(raw)
	switch {
	case path == "":
		return "", errors.New("path is empty")
	case strings.ContainsFunc(path, func(r rune) bool { return r < 0x20 || r == 0x7f }):
		return "", errors.New("path has a control character")
	case path == "~":
		path = home
	case strings.HasPrefix(path, "~/"):
		path = filepath.Join(home, path[2:])
	case !filepath.IsAbs(path):
		if workdir == "" {
			return "", errors.New("path must be absolute")
		}
		path = filepath.Join(workdir, path)
	}
	return resolvedPath(path), nil
}

// projectOf is the Project whose folder holds path, the deepest one
// when folders nest; "" when none does.
func (m *Manager) projectOf(path string) (id, dir string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.projects {
		if withinDir(p.Dir, path) && len(p.Dir) > len(dir) {
			id, dir = p.ID, p.Dir
		}
	}
	return id, dir
}
