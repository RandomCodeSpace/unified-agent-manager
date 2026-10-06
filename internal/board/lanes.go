package board

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// LaneHold returns the lane attempt on the attempt branch and its subtask,
// so cleanup can tell whether the attempt landed; a branch no attempt has
// is not found.
func (s *Store) LaneHold(ctx context.Context, branch string) (Card, Hold, error) {
	var card Card
	var hold Hold
	err := s.read(ctx, func(t *txn) error {
		if strings.TrimSpace(branch) == "" {
			return &Error{Code: CodeNotFound, Message: "no lane attempt has no branch"}
		}
		var cardID string
		err := t.tx.QueryRowContext(t.ctx, `SELECT card_id FROM holds WHERE branch = ?`, branch).Scan(&cardID)
		if errors.Is(err, sql.ErrNoRows) {
			return &Error{Code: CodeNotFound, Message: fmt.Sprintf("no lane attempt is on %s", branch)}
		}
		if err != nil {
			return fmt.Errorf("board: find lane attempt: %w", err)
		}
		_, n, err := t.find(cardID)
		if err != nil {
			return err
		}
		holds, err := t.holds(n.ID)
		if err != nil {
			return err
		}
		for _, h := range holds {
			if h.Lane.Branch == branch {
				card, hold = n.Card, h
				return nil
			}
		}
		return &Error{Code: CodeNotFound, Message: fmt.Sprintf("no lane attempt is on %s", branch)}
	})
	return card, hold, err
}

// Note adds uam's automatic comment body to the card ref, once: a card that
// has the same comment from uam already is left as it is. It reports
// whether it added the comment.
func (s *Store) Note(ctx context.Context, ref, body string) (bool, error) {
	body, err := checkComment(body)
	if err != nil {
		return false, err
	}
	added := false
	_, err = s.ownerWrite(ctx, Actor{}, ref, func(t *txn, _ *outline, n *node) error {
		var count int
		if err := t.tx.QueryRowContext(t.ctx, `SELECT COUNT(*) FROM comments WHERE card_id = ? AND author = ? AND body = ?`,
			n.ID, AuthorUAM, body).Scan(&count); err != nil {
			return fmt.Errorf("board: find note: %w", err)
		}
		if count > 0 {
			return nil
		}
		added = true
		_, err := t.addComment(n, AuthorUAM, "", body, true, false)
		return err
	})
	return added, err
}
