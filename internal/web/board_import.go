package web

import (
	"context"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/board"
)

// ImportBoard imports the external board database kept in dir, an absolute
// directory path, into the planner store (board.Store.Import); it is refused
// while the planner is off. A source project goes to the git Project with
// exactly its name, compared case-sensitively. A name that no git Project
// has, or that several share, leaves its cards in Unassigned.
func (m *Manager) ImportBoard(ctx context.Context, dir string) (board.ImportReport, error) {
	if err := m.boardOn(); err != nil {
		return board.ImportReport{}, err
	}
	// Projects reads git, so it runs before the store is used.
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
	var report board.ImportReport
	err := m.withBoard(func(st *board.Store) error {
		var err error
		report, err = st.Import(ctx, dir, projects)
		return err
	})
	return report, err
}
