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
			if other := editPath(s.workdir, p); (other == path || sameRepositoryFile(caller, path, s, other)) && at.After(last) {
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

// sameRepositoryFile reports whether a's path and b's name one
// repository-relative path in two checkouts of the same repository, as two
// worktrees can. This is advisory overlap, never file ownership.
func sameRepositoryFile(a *webSession, ap string, b *webSession, bp string) bool {
	if a.checkout.Common == "" || a.checkout.Common != b.checkout.Common {
		return false
	}
	ar, aok := repositoryEditPath(a, ap)
	br, bok := repositoryEditPath(b, bp)
	return aok && bok && ar == br
}

// repositoryEditPath is path relative to s's checkout root, false when it
// is outside the checkout.
func repositoryEditPath(s *webSession, path string) (string, bool) {
	if s.realWorkdir != "" {
		if rel, err := filepath.Rel(s.workdir, path); err == nil && filepath.IsLocal(rel) {
			path = filepath.Join(s.realWorkdir, rel)
		}
	}
	rel, err := filepath.Rel(s.checkout.Root, path)
	return rel, err == nil && filepath.IsLocal(rel)
}
