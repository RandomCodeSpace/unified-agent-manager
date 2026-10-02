package board

import (
	"context"
	"fmt"
	"strings"
)

// SetStatus is the owner's direct status change. On a subtask: done needs a
// comment and passes the finishing guard unless force is set; cancelled
// needs a comment; todo marks a planned or done subtask ready, or releases a
// doing one. On a container only cancelled is allowed, as a cascade over its
// subtree; force exists on subtasks only.
func (s *Store) SetStatus(ctx context.Context, a Actor, ref string, to Status, comment string, force bool) (Card, error) {
	o := map[Status]op{StatusDone: opDone, StatusCancelled: opCancel, StatusTodo: opReady}[to]
	if o == "" {
		return Card{}, invalid("cannot set status %q", to)
	}
	if err := permit(a, o, ""); err != nil {
		return Card{}, err
	}
	body := strings.TrimSpace(comment)
	if to != StatusTodo || body != "" {
		var err error
		if body, err = checkComment(body); err != nil {
			return Card{}, invalid("marking a card %s needs a comment", to)
		}
	}
	return s.ownerWrite(ctx, ref, func(t *txn, o *outline, n *node) error {
		if n.container() {
			if to != StatusCancelled || force {
				return invalid("a %s's status is derived from its subtasks; it can only be cancelled", n.Kind)
			}
			if n.stored == StatusCancelled || n.Status == StatusDone {
				return invalid("%s is already %s", n.ref(), n.Status)
			}
			return t.cancel(o, n, body, AuthorOwner)
		}
		switch to {
		case StatusDone:
			if err := permit(a, opDone, n.stored); err != nil {
				return err
			}
			if !force {
				if err := t.guard(o, n); err != nil {
					return err
				}
			}
			if _, err := t.addComment(n, AuthorOwner, "", body, false, true); err != nil {
				return err
			}
			if n.HeldBy != "" {
				if err := t.releaseHold(n, ReleaseDone, StatusDone, ""); err != nil {
					return err
				}
			} else if err := t.setStatus(n, StatusDone, "", ""); err != nil {
				return err
			}
			if err := t.confirm(o, a, n); err != nil {
				return err
			}
			return t.updateCard(n)
		case StatusCancelled:
			if err := permit(a, opCancel, n.stored); err != nil {
				return err
			}
			return t.cancel(o, n, body, AuthorOwner)
		}
		if n.stored == StatusDoing {
			if err := t.releaseHold(n, ReleaseOwner, StatusTodo, ""); err != nil {
				return err
			}
		} else {
			if err := permit(a, opReady, n.stored); err != nil {
				return err
			}
			if err := o.underCancelled(n, nil); err != nil {
				return err
			}
			if err := t.setStatus(n, StatusTodo, "", ""); err != nil {
				return err
			}
			if err := t.confirm(o, a, n); err != nil {
				return err
			}
			if err := t.updateCard(n); err != nil {
				return err
			}
		}
		if body == "" {
			return nil
		}
		_, err := t.addComment(n, AuthorOwner, "", body, false, false)
		return err
	})
}

// cancel cancels n under a new cascade with body as author's comment. On a
// container it is the cascade: every card under it whose shown status is not
// terminal is cancelled, so a container that is already done keeps its
// status; holds are released and requests withdrawn, and each subtask is
// stamped "cancelled with #n: …".
func (t *txn) cancel(o *outline, n *node, body, author string) error {
	cascade := t.s.newID()
	if !n.container() {
		if err := t.cancelNode(n, cascade); err != nil {
			return err
		}
		_, err := t.addComment(n, author, "", body, false, false)
		return err
	}
	for _, m := range o.subtree(n) {
		if m != n && m.Status.terminal() {
			continue
		}
		if err := t.cancelNode(m, cascade); err != nil {
			return err
		}
		if m.container() {
			continue
		}
		if _, err := t.addComment(m, AuthorUAM, "", fmt.Sprintf("cancelled with %s: %s", n.ref(), body), true, false); err != nil {
			return err
		}
	}
	_, err := t.addComment(n, author, "", body, false, false)
	return err
}

// cancelNode cancels one card under cascade, releasing its hold.
func (t *txn) cancelNode(n *node, cascade string) error {
	if n.HeldBy != "" {
		return t.releaseHold(n, ReleaseCancelled, StatusCancelled, cascade)
	}
	return t.setStatus(n, StatusCancelled, "", cascade)
}

// Dismiss cancels an unconfirmed card and everything under it, with the
// automatic comment "dismissed": the owner's, or an agent's in its scope
// while planning. It never ends a hold under the card, nor, for an agent, on
// it (lock.go).
func (s *Store) Dismiss(ctx context.Context, a Actor, ref string) (Card, error) {
	if err := permit(a, opDismiss, ""); err != nil {
		return Card{}, err
	}
	return s.agentWrite(ctx, a, ref, func(t *txn, o *outline, n *node) error {
		if n.Confirmed() {
			return invalid("%s is confirmed; cancel it instead", n.ref())
		}
		if n.stored == StatusCancelled {
			return invalid("%s is already cancelled", n.ref())
		}
		if !a.owner() {
			if err := inProgress(n); err != nil {
				return err
			}
		}
		if err := o.startedUnder(n, true); err != nil {
			return err
		}
		cascade := t.s.newID()
		for _, m := range o.subtree(n) {
			if m != n && m.Status.terminal() {
				continue
			}
			if err := t.cancelNode(m, cascade); err != nil {
				return err
			}
			if _, err := t.addComment(m, AuthorUAM, "", "dismissed", true, false); err != nil {
				return err
			}
		}
		return nil
	})
}

// Restore reopens exactly the cards cancelled with the card ref, in one
// cascade, and confirms each, with its unconfirmed ancestors. A subtask with
// an earlier attempt reopens as todo, one without as planned. It needs a
// comment, and is refused while a card of the cascade sits under a cancelled
// card outside it.
func (s *Store) Restore(ctx context.Context, a Actor, ref, comment string) (Card, error) {
	if err := permit(a, opRestore, ""); err != nil {
		return Card{}, err
	}
	body, err := checkComment(comment)
	if err != nil {
		return Card{}, invalid("a restore needs a comment")
	}
	return s.ownerWrite(ctx, ref, func(t *txn, o *outline, n *node) error {
		if n.stored != StatusCancelled {
			return invalid("%s is not cancelled", n.ref())
		}
		set := []*node{n}
		if n.CascadeID != "" {
			set = nil
			for _, m := range o.order {
				if m.CascadeID == n.CascadeID && m.stored == StatusCancelled {
					set = append(set, m)
				}
			}
		}
		in := map[string]bool{}
		for _, m := range set {
			in[m.ID] = true
		}
		for _, m := range set {
			if err := o.underCancelled(m, in); err != nil {
				return err
			}
			if err := o.duplicate(m.ParentID, m.Title, m.ID); err != nil {
				return err
			}
		}
		for _, m := range set {
			to := StatusPlanned
			if !m.container() {
				var attempts int
				if err := t.tx.QueryRowContext(t.ctx, `SELECT COUNT(*) FROM holds WHERE card_id = ?`, m.ID).Scan(&attempts); err != nil {
					return fmt.Errorf("board: count attempts: %w", err)
				}
				if attempts > 0 {
					to = StatusTodo
				}
			}
			if err := t.setStatus(m, to, "", ""); err != nil {
				return err
			}
			if err := t.confirm(o, a, m); err != nil {
				return err
			}
			if err := t.updateCard(m); err != nil {
				return err
			}
		}
		_, err := t.addComment(n, AuthorOwner, "", body, false, false)
		return err
	})
}

// Purge deletes projectID's cancelled cards whose whole subtree is
// cancelled, with their comments, links, requests and holds. It is the only
// hard delete. It returns the number of cards deleted.
func (s *Store) Purge(ctx context.Context, a Actor, projectID string) (int, error) {
	if err := permit(a, opPurge, ""); err != nil {
		return 0, err
	}
	if projectID == "" {
		return 0, errReadOnly
	}
	count := 0
	_, err := s.write(ctx, func(t *txn) error {
		count = 0
		return t.mutate(projectID, false, func() error {
			o, err := t.outline(projectID)
			if err != nil {
				return err
			}
			gone := map[string]bool{}
			for _, n := range o.order {
				if gone[n.ID] || n.stored != StatusCancelled {
					continue
				}
				tree := o.subtree(n)
				if !allCancelled(tree) {
					continue
				}
				for _, m := range tree {
					gone[m.ID] = true
					if err := t.purge(projectID, m.ID); err != nil {
						return err
					}
					count++
				}
			}
			return nil
		})
	})
	return count, err
}

func allCancelled(tree []*node) bool {
	for _, m := range tree {
		if m.stored != StatusCancelled {
			return false
		}
	}
	return true
}

func (t *txn) purge(project, id string) error {
	for _, q := range []string{
		`DELETE FROM comments WHERE card_id = ?1`,
		`DELETE FROM links WHERE blocker_id = ?1 OR blocked_id = ?1`,
		`DELETE FROM requests WHERE card_id = ?1`,
		`DELETE FROM holds WHERE card_id = ?1`,
		`DELETE FROM cards WHERE id = ?1`,
	} {
		if err := t.exec(q, id); err != nil {
			return err
		}
	}
	t.removed(project, id)
	return nil
}

// Sweep runs the expiry sweep over every Project, as at boot. It returns
// the number of cards cancelled.
func (s *Store) Sweep(ctx context.Context) (int, error) {
	count := 0
	_, err := s.write(ctx, func(t *txn) error {
		count = 0
		projects, err := t.ids(`SELECT DISTINCT project_id FROM cards
			WHERE expires_at IS NOT NULL AND status <> 'cancelled' AND project_id <> '' ORDER BY project_id`)
		if err != nil {
			return err
		}
		for _, p := range projects {
			err := t.mutate(p, false, func() error {
				n, err := t.sweep(p)
				count += n
				return err
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	return count, err
}
