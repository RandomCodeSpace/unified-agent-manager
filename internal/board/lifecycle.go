package board

import (
	"context"
	"fmt"
	"slices"
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
	return s.ownerWrite(ctx, a, ref, func(t *txn, o *outline, n *node) error {
		if n.container() {
			if to != StatusCancelled || force {
				return invalid("a %s's status is derived from its subtasks; it can only be cancelled", n.Kind)
			}
			if n.stored == StatusCancelled || n.Status == StatusDone {
				return invalid("%s is already %s", n.ref(), n.Status)
			}
			return t.cancel(o, n, body, AuthorOwner)
		}
		// Done and To do confirm the subtask, which under an approved epic
		// only the epic's approval does.
		if to != StatusCancelled && !n.Confirmed() {
			if err := o.runOwned(n); err != nil {
				return err
			}
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

// Dismiss is the owner's drop of an unconfirmed card: it cancels the card
// and everything under it, with the automatic comment "dismissed". It never
// ends a hold under the card (lock.go). Agents delete instead.
func (s *Store) Dismiss(ctx context.Context, a Actor, ref string) (Card, error) {
	if err := permit(a, opDismiss, ""); err != nil {
		return Card{}, err
	}
	return s.ownerWrite(ctx, a, ref, func(t *txn, o *outline, n *node) error {
		if n.Confirmed() {
			return invalid("%s is confirmed; cancel it instead", n.ref())
		}
		if n.stored == StatusCancelled {
			return invalid("%s is already cancelled", n.ref())
		}
		if err := o.startedUnder(n, true); err != nil {
			return err
		}
		return t.drop(o, n, AuthorUAM, "", "dismissed")
	})
}

// DeleteResult is an agent's delete: the card, now cancelled, and the live
// cards outside it that a deleted card blocked, which no longer wait on it.
type DeleteResult struct {
	Card     Card
	Released []Card
}

// Delete is an agent's delete (ADR 0006): it cancels the card ref in the
// agent's reach, confirmed or not, and everything under it, in one cascade
// the owner can restore, each card stamped "deleted" by the Task. It is
// refused while anything in the subtree has started, while a started
// subtask waits on a card in it, and when it would bring a container above
// the card to done or cancelled: that would cancel the container's
// proposals and release its dependents before any replacement exists. An
// approved epic itself is never deleted: the agent files a cancel request.
func (s *Store) Delete(ctx context.Context, a Actor, ref string) (DeleteResult, error) {
	if err := permit(a, opDelete, ""); err != nil {
		return DeleteResult{}, err
	}
	var out DeleteResult
	card, err := s.agentWrite(ctx, a, ref, func(t *txn, o *outline, n *node) error {
		out.Released = nil
		if n.stored == StatusCancelled {
			return invalid("%s is already cancelled", n.ref())
		}
		if a.Proposals && n.Confirmed() {
			return refuse(CodeForbidden, "%s is confirmed; this agent deletes only proposals", n.ref())
		}
		if n.Run != nil {
			return refuse(CodeForbidden, "%s is an approved epic; file a cancel request on it for the owner instead", n.ref())
		}
		if err := inProgress(n); err != nil {
			return err
		}
		if err := o.startedUnder(n, false); err != nil {
			return err
		}
		tree := o.subtree(n)
		if err := t.awaited(o, tree); err != nil {
			return err
		}
		above := o.above(n)
		released := dependents(o, tree)
		if err := t.drop(o, n, a.author(), a.AgentID, "deleted"); err != nil {
			return err
		}
		if err := t.closesNone(o.project, n, above,
			"Deleting %[1]s would make %[2]s, and a delete closes no other card: edit %[1]s instead, or file a cancel request on it for the owner"); err != nil {
			return err
		}
		after, err := t.outline(o.project)
		if err != nil {
			return err
		}
		for _, id := range released {
			out.Released = append(out.Released, after.byID[id].Card)
		}
		return nil
	})
	out.Card = card
	return out, err
}

// above maps each container above n to its status.
func (o *outline) above(n *node) map[string]Status {
	out := map[string]Status{}
	for p := o.byID[n.ParentID]; p != nil; p = o.byID[p.ParentID] {
		out[p.ID] = p.Status
	}
	return out
}

// closesNone refuses the agent write on n in progress, naming them, when it
// brought a container above n to done or cancelled: that would cancel the
// container's proposals and release its dependents. before is o.above(n)
// from before the write; format gets n's ref and the closed containers, as
// "#3 done, #1 done".
func (t *txn) closesNone(project string, n *node, before map[string]Status, format string) error {
	after, err := t.outline(project)
	if err != nil {
		return err
	}
	var refs, closed []string
	for p := after.byID[n.ParentID]; p != nil; p = after.byID[p.ParentID] {
		if p.Status.terminal() && p.Status != before[p.ID] {
			refs = append(refs, p.ref())
			closed = append(closed, fmt.Sprintf("%s %s", p.ref(), p.Status))
		}
	}
	if len(refs) == 0 {
		return nil
	}
	return &Error{Code: CodeInvalid, Refs: refs, Message: fmt.Sprintf(format, n.ref(), strings.Join(closed, ", "))}
}

// awaited refuses deleting the cards of tree while a started subtask waits
// on one of them, directly or through an ancestor, naming it: deleting
// would release it.
func (t *txn) awaited(o *outline, tree []*node) error {
	in := map[string]bool{}
	for _, m := range tree {
		in[m.ID] = true
	}
	var refs, named []string
	for _, n := range o.order {
		if !n.started() {
			continue
		}
		waits, err := t.waitsOn(o, n)
		if err != nil {
			return err
		}
		if i := slices.IndexFunc(waits, func(w waiting) bool { return in[w.blocker.ID] }); i >= 0 {
			refs = append(refs, n.ref())
			named = append(named, fmt.Sprintf("%s has started and waits on %s", n.ref(), waits[i].named()))
		}
	}
	if len(refs) == 0 {
		return nil
	}
	return &Error{Code: CodeInProgress, Refs: refs, Message: strings.Join(named, "; ") + ", and started work keeps what it waits on"}
}

// dependents lists the IDs of the live cards outside tree that an open card
// of tree blocks, in tree order.
func dependents(o *outline, tree []*node) []string {
	in := map[string]bool{}
	for _, m := range tree {
		in[m.ID] = true
	}
	var out []string
	for _, m := range tree {
		if m.Status.terminal() {
			continue
		}
		for _, id := range m.Blocks {
			if d := o.byID[id]; d != nil && !in[id] && !d.Status.terminal() && !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
	}
	return out
}

// drop cancels n and every card under it that is not yet done or
// cancelled, in one cascade, each with the automatic comment body by
// author.
func (t *txn) drop(o *outline, n *node, author, agentID, body string) error {
	cascade := t.s.newID()
	for _, m := range o.subtree(n) {
		if m != n && m.Status.terminal() {
			continue
		}
		if err := t.cancelNode(m, cascade); err != nil {
			return err
		}
		if _, err := t.addComment(m, author, agentID, body, true, false); err != nil {
			return err
		}
	}
	return nil
}

// Restore reopens exactly the cards cancelled with the card ref, in one
// cascade, and confirms each, with its unconfirmed ancestors. Under an
// approved epic each card but the epic itself comes back as a proposal with
// a fresh expiry instead, to approve again (ADR 0006 §6.3), unless live
// confirmed work outside the cascade sits under it; cancelled work under
// such a card becomes a proposal with it, as no confirmed card sits under a
// proposal; and a container the restore brings to done stays open with
// them. A subtask with an earlier attempt reopens as todo, one without as
// planned. It needs a comment, and is refused while a card of the cascade
// sits under a cancelled card outside it.
func (s *Store) Restore(ctx context.Context, a Actor, ref, comment string) (Card, error) {
	if err := permit(a, opRestore, ""); err != nil {
		return Card{}, err
	}
	body, err := checkComment(comment)
	if err != nil {
		return Card{}, invalid("a restore needs a comment")
	}
	return s.ownerWrite(ctx, a, ref, func(t *txn, o *outline, n *node) error {
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
			e := o.approved(m)
			if e != nil && m.container() {
				t.keepOpen[m.ID] = true
			}
			if e != nil && e != m && !confirmedUnder(o, m, in) {
				rearm(t.now, m)
				for _, c := range o.subtree(m)[1:] {
					if !in[c.ID] && c.Confirmed() {
						rearm(t.now, c)
						if err := t.updateCard(c); err != nil {
							return err
						}
					}
				}
			} else if err := t.confirm(o, a, m); err != nil {
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
		return t.mutate(a, projectID, false, func() error {
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

// confirmedUnder reports whether a live confirmed card outside set sits
// under n: n then stays confirmed, as no confirmed card sits under a
// proposal.
func confirmedUnder(o *outline, n *node, set map[string]bool) bool {
	return slices.ContainsFunc(o.subtree(n)[1:], func(m *node) bool {
		return !set[m.ID] && m.Confirmed() && m.stored != StatusCancelled
	})
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
		`DELETE FROM runs WHERE epic_id = ?1`,
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
			err := t.mutate(Actor{}, p, false, func() error {
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
