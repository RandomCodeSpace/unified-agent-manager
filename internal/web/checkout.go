package web

import (
	"context"
	"strings"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/execpath"
)

// checkoutLocation is a working directory's Git checkout: Root is its top
// level and Common the repository's shared Git directory, so two worktrees
// of one repository have different Roots and the same Common. Both are ""
// outside Git.
type checkoutLocation struct{ Root, Common string }

// locateCheckout reads workdir's checkout with one git call. The caller must
// not hold mu. Located checkouts are cached by workdir; a folder outside Git
// is asked again next time, since it may become a repository.
func (m *Manager) locateCheckout(ctx context.Context, workdir string) checkoutLocation {
	m.checkoutsMu.Lock()
	loc, ok := m.checkouts[workdir]
	m.checkoutsMu.Unlock()
	if ok {
		return loc
	}
	git, err := execpath.Resolve("git")
	if err != nil {
		return checkoutLocation{}
	}
	out, code, _, err := runGit(ctx, git, workdir, 8192, "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-common-dir")
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if err != nil || code != 0 || len(lines) != 2 {
		return checkoutLocation{}
	}
	loc = checkoutLocation{Root: realPath(strings.TrimSpace(lines[0])), Common: realPath(strings.TrimSpace(lines[1]))}
	m.checkoutsMu.Lock()
	if m.checkouts == nil {
		m.checkouts = map[string]checkoutLocation{}
	}
	m.checkouts[workdir] = loc
	m.checkoutsMu.Unlock()
	return loc
}

// locateCheckouts sets each session's checkout, as Start does for the
// sessions it loaded. It runs git, so never under mu.
func (m *Manager) locateCheckouts(sessions []*webSession) {
	for _, s := range sessions {
		loc := m.locateCheckout(m.ctx, s.workdir)
		m.mu.Lock()
		s.checkout = loc
		m.mu.Unlock()
	}
}
