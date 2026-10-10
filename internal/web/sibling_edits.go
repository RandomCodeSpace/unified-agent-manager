package web

import (
	"cmp"
	"path/filepath"
	"slices"
	"time"
)

type siblingEdit struct {
	Session *webSession
	At      time.Time
}

func editPath(workdir, path string) string {
	if !filepath.IsAbs(path) {
		path = filepath.Join(workdir, path)
	}
	return filepath.Clean(path)
}

// siblingEditsLocked returns same-Project edits of an absolute path, newest
// first. The caller holds m.mu; returned sessions must stay under that lock.
func (m *Manager) siblingEditsLocked(caller *webSession, path string) []siblingEdit {
	var out []siblingEdit
	if caller.projectID == "" {
		return out
	}
	for _, s := range m.sessions {
		if s == caller || s.removed || s.projectID != caller.projectID {
			continue
		}
		var last time.Time
		for p, at := range s.edits {
			if editPath(s.workdir, p) == path && at.After(last) {
				last = at
			}
		}
		if !last.IsZero() {
			out = append(out, siblingEdit{s, last})
		}
	}
	slices.SortFunc(out, func(a, b siblingEdit) int {
		if n := b.At.Compare(a.At); n != 0 {
			return n
		}
		return cmp.Compare(a.Session.id, b.Session.id)
	})
	return out
}
