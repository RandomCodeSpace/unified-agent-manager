package web

import (
	"context"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/board"
)

// ImportBoard imports the external board database kept in dir into bs
// (board.Store.Import). A source project goes to the git Project with
// exactly its name, compared case-sensitively. A name that no git Project
// has, or that several share, leaves its cards in Unassigned.
func (m *Manager) ImportBoard(ctx context.Context, bs *board.Store, dir string) (board.ImportReport, error) {
	projects := map[string]string{}
	shared := map[string]bool{}
	for _, p := range m.Projects() {
		if p.NoGit != "" {
			continue
		}
		if _, ok := projects[p.Name]; ok {
			shared[p.Name] = true
		}
		projects[p.Name] = p.ID
	}
	for name := range shared {
		delete(projects, name)
	}
	return bs.Import(ctx, dir, projects)
}
