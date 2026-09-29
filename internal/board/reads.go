package board

import (
	"context"
	"fmt"
)

// Held returns every held subtask on every board, in #seq order.
func (s *Store) Held(ctx context.Context) ([]Card, error) {
	var out []Card
	err := s.read(ctx, func(t *txn) error {
		ids, err := t.ids(`SELECT id FROM cards WHERE held_by <> '' ORDER BY seq`)
		if err != nil {
			return err
		}
		for _, id := range ids {
			_, n, err := t.find(id)
			if err != nil {
				return err
			}
			out = append(out, n.Card)
		}
		return nil
	})
	return out, err
}

// Revisions returns every Project's board revision, "" for Unassigned. A
// Project that was never written has none.
func (s *Store) Revisions(ctx context.Context) (map[string]int64, error) {
	out := map[string]int64{}
	err := s.read(ctx, func(t *txn) error {
		rows, err := t.tx.QueryContext(t.ctx, `SELECT project_id, revision FROM revisions`)
		if err != nil {
			return fmt.Errorf("board: read revisions: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var project string
			var revision int64
			if err := rows.Scan(&project, &revision); err != nil {
				return fmt.Errorf("board: read revisions: %w", err)
			}
			out[project] = revision
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("board: read revisions: %w", err)
		}
		return nil
	})
	return out, err
}
