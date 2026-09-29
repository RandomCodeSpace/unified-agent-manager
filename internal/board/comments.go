package board

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// AddComment adds a's comment to the card ref. Agents may comment on cards in
// their scope, up to CapComments per card per Task.
func (s *Store) AddComment(ctx context.Context, a Actor, ref, body string) (Comment, error) {
	if err := permit(a, opComment, ""); err != nil {
		return Comment{}, err
	}
	body, err := checkComment(body)
	if err != nil {
		return Comment{}, err
	}
	var out Comment
	_, err = s.agentWrite(ctx, a, ref, func(t *txn, _ *outline, n *node) error {
		if !a.owner() {
			var count int
			if err := t.tx.QueryRowContext(t.ctx, `SELECT COUNT(*) FROM comments WHERE card_id = ? AND author = ? AND automatic = 0`,
				n.ID, a.author()).Scan(&count); err != nil {
				return fmt.Errorf("board: count comments: %w", err)
			}
			if count >= CapComments {
				return refuse(CodeLimit, "the Task may add at most %d comments to one card", CapComments)
			}
		}
		out, err = t.addComment(n, a.author(), a.AgentID, body, false, false)
		return err
	})
	return out, err
}

func (t *txn) addComment(n *node, author, agentID, body string, automatic, close bool) (Comment, error) {
	res, err := t.tx.ExecContext(t.ctx, `INSERT INTO comments (card_id, author, agent_id, body, automatic, close, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, n.ID, author, agentID, body, boolInt(automatic), boolInt(close), stamp(t.now))
	if err != nil {
		return Comment{}, fmt.Errorf("board: insert comment: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Comment{}, fmt.Errorf("board: insert comment: %w", err)
	}
	t.changed(n.ProjectID, n.ID)
	return Comment{ID: id, CardID: n.ID, Author: author, AgentID: agentID, Body: body, Automatic: automatic, Close: close, CreatedAt: t.now}, nil
}

func (t *txn) comments(cardID string) ([]Comment, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT id, author, agent_id, body, automatic, close, created_at
		FROM comments WHERE card_id = ? ORDER BY id`, cardID)
	if err != nil {
		return nil, fmt.Errorf("board: list comments: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Comment
	for rows.Next() {
		c := Comment{CardID: cardID}
		var automatic, close int
		var created string
		if err := rows.Scan(&c.ID, &c.Author, &c.AgentID, &c.Body, &automatic, &close, &created); err != nil {
			return nil, fmt.Errorf("board: scan comment: %w", err)
		}
		c.Automatic, c.Close = automatic != 0, close != 0
		if err := parseStamps([]string{created}, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("board: comment %d: %w", c.ID, err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("board: list comments: %w", err)
	}
	return out, nil
}

// rollUp adds container c's roll-up: one line per done child, from its
// close comment.
func (t *txn) rollUp(o *outline, c *node) error {
	var lines []string
	for _, kid := range o.kids[c.ID] {
		if kid.Status != StatusDone {
			continue
		}
		var body string
		err := t.tx.QueryRowContext(t.ctx, `SELECT body FROM comments WHERE card_id = ? AND close = 1 ORDER BY id DESC LIMIT 1`, kid.ID).Scan(&body)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			body = "done"
		case err != nil:
			return fmt.Errorf("board: read close comment: %w", err)
		}
		lines = append(lines, fmt.Sprintf("%s %s: %s", kid.ref(), kid.Title, strings.ReplaceAll(body, "\n", " ")))
	}
	if len(lines) == 0 {
		return nil
	}
	_, err := t.addComment(c, AuthorUAM, "", "Closed with:\n"+strings.Join(lines, "\n"), true, true)
	return err
}
