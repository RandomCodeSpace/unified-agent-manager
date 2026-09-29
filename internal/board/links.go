// Ported from github.com/RandomCodeSpace/kb internal/store/links.go (MIT).

package board

import (
	"context"
	"fmt"
)

// Link records "blocker blocks blocked". Both cards must be in the same
// Project, and the blocker must be confirmed. Self links, duplicates and
// links that would close a cycle are refused. Agents may link a blocked card
// in their scope.
func (s *Store) Link(ctx context.Context, a Actor, blockerRef, blockedRef string) error {
	if err := permit(a, opLink, ""); err != nil {
		return err
	}
	_, err := s.agentWrite(ctx, a, blockedRef, func(t *txn, o *outline, blocked *node) error {
		blocker, err := t.blocker(o, blocked, blockerRef)
		if err != nil {
			return err
		}
		return t.link(o, blocker, blocked)
	})
	return err
}

// blocker resolves ref as a blocker of n: a confirmed card in n's Project,
// other than n.
func (t *txn) blocker(o *outline, n *node, ref string) (*node, error) {
	project, id, err := t.locate(ref)
	if err != nil {
		return nil, err
	}
	if project != o.project {
		return nil, invalid("a blocker must be in the same Project")
	}
	b := o.byID[id]
	if b.ID == n.ID {
		return nil, invalid("a card cannot block itself")
	}
	if !b.Confirmed() {
		return nil, invalid("%s is unconfirmed; links may point only at confirmed cards", b.ref())
	}
	return b, nil
}

func (t *txn) link(o *outline, blocker, blocked *node) error {
	cyclic, err := t.reaches(blocked.ID, blocker.ID)
	if err != nil {
		return err
	}
	if cyclic {
		return invalid("the link would create a cycle: %s already blocks %s", blocked.ref(), blocker.ref())
	}
	res, err := t.tx.ExecContext(t.ctx, `INSERT INTO links (blocker_id, blocked_id) VALUES (?, ?)
		ON CONFLICT(blocker_id, blocked_id) DO NOTHING`, blocker.ID, blocked.ID)
	if err != nil {
		return fmt.Errorf("board: insert link: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return refuse(CodeDuplicate, "%s already blocks %s", blocker.ref(), blocked.ref())
	}
	t.changed(o.project, blocker.ID)
	t.changed(o.project, blocked.ID)
	return nil
}

// Unlink removes the link between two cards, whichever way it points.
func (s *Store) Unlink(ctx context.Context, a Actor, aRef, bRef string) error {
	if err := permit(a, opUnlink, ""); err != nil {
		return err
	}
	_, err := s.write(ctx, func(t *txn) error {
		ap, aID, err := t.locate(aRef)
		if err != nil {
			return err
		}
		bp, bID, err := t.locate(bRef)
		if err != nil {
			return err
		}
		if ap == "" || bp == "" {
			return errReadOnly
		}
		res, err := t.tx.ExecContext(t.ctx, `DELETE FROM links WHERE (blocker_id = ?1 AND blocked_id = ?2) OR (blocker_id = ?2 AND blocked_id = ?1)`, aID, bID)
		if err != nil {
			return fmt.Errorf("board: delete link: %w", err)
		}
		if n, err := res.RowsAffected(); err == nil && n == 0 {
			return refuse(CodeNotFound, "no link between %s and %s", aRef, bRef)
		}
		t.changed(ap, aID)
		t.changed(bp, bID)
		return nil
	})
	return err
}

// reaches reports whether from reaches to along blocks edges: the cycle
// check for Link. Boards are small; a breadth-first search in the
// transaction.
func (t *txn) reaches(from, to string) (bool, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT blocker_id, blocked_id FROM links`)
	if err != nil {
		return false, fmt.Errorf("board: load links: %w", err)
	}
	defer func() { _ = rows.Close() }()
	edges := map[string][]string{}
	for rows.Next() {
		var blocker, blocked string
		if err := rows.Scan(&blocker, &blocked); err != nil {
			return false, fmt.Errorf("board: scan link: %w", err)
		}
		edges[blocker] = append(edges[blocker], blocked)
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("board: load links: %w", err)
	}
	queue, seen := []string{from}, map[string]bool{from: true}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range edges[cur] {
			if next == to {
				return true, nil
			}
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return false, nil
}
