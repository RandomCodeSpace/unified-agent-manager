// Ported from github.com/RandomCodeSpace/kb internal/store/links.go (MIT).

package board

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// Link records "blocker blocks blocked". Both cards must be in the same
// Project, of the same kind under the same parent (sameLevel); either may be
// a proposal, since dependencies are part of planning and only starting work
// needs the owner's confirmation. Self links, duplicates and links that
// would close a cycle are refused. Agents may link a blocked card in their
// scope (inScope), which includes the epics a Task proposed, unless it is a
// container with work started under it (startedWork).
func (s *Store) Link(ctx context.Context, a Actor, blockerRef, blockedRef string) error {
	if err := permit(a, opLink, ""); err != nil {
		return err
	}
	_, err := s.write(ctx, func(t *txn) error {
		project, id, err := t.locate(blockedRef)
		if err != nil {
			return err
		}
		if project == "" {
			return errReadOnly
		}
		return t.mutate(a, project, true, func() error {
			o, blocked, err := t.cardIn(project, id)
			if err != nil {
				return err
			}
			if err := t.inScope(o, a, blocked); err != nil {
				return err
			}
			if !a.owner() {
				if err := o.startedWork(blocked); err != nil {
					return err
				}
			}
			blocker, err := t.blocker(o, blocked, blockerRef)
			if err != nil {
				return err
			}
			return t.link(o, blocker, blocked)
		})
	})
	return err
}

// blocker resolves ref as a blocker of n: a card in n's Project other than
// n, at n's level, so a blocked request is refused as it is filed.
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
	if err := o.sameLevel(b, n); err != nil {
		return nil, err
	}
	return b, nil
}

// sameLevel refuses a link between cards that are not of one kind under one
// parent: links map a DAG per level, epics among epics, stories among the
// stories of one epic, subtasks among the subtasks of one story, and cards
// at the root among root cards of their kind. The refusal names the pair of
// containers to link instead, when there is one.
func (o *outline) sameLevel(blocker, blocked *node) error {
	if blocker.Kind == blocked.Kind && blocker.ParentID == blocked.ParentID {
		return nil
	}
	kinds := blocked.Kind.plural()
	rule := fmt.Sprintf("%s depend only on %s", kinds, kinds)
	switch p := o.byID[blocked.ParentID]; {
	case p != nil:
		rule += " in the same " + string(p.Kind)
	case blocked.Kind != KindEpic:
		rule = fmt.Sprintf("%s at the top level depend only on %s at the top level", kinds, kinds)
	}
	msg := fmt.Sprintf("%s can't block %s: %s", blocker.ref(), blocked.ref(), rule)
	// The nearest pair of ancestors, one on each side, that may be linked.
	for x := blocker; x != nil; x = o.byID[x.ParentID] {
		for y := blocked; y != nil; y = o.byID[y.ParentID] {
			if x != y && x.Kind == y.Kind && x.ParentID == y.ParentID {
				return invalid("%s; link %s %s and %s instead", msg, x.Kind.plural(), x.ref(), y.ref())
			}
		}
	}
	return invalid("%s", msg)
}

// plural is the kind's plural, for messages.
func (k Kind) plural() string {
	if k == KindStory {
		return "stories"
	}
	return string(k) + "s"
}

// unlinkedFor refuses a change that takes n out of its level, a move to
// another parent or a split into a story, while n has blocker links: a link
// joins cards of one kind under one parent, so the links go first. why says
// what the change does.
func (o *outline) unlinkedFor(n *node, why string) error {
	linked := slices.Concat(n.BlockedBy, n.Blocks)
	if len(linked) == 0 {
		return nil
	}
	refs := make([]string, 0, len(linked))
	for _, id := range linked {
		if m := o.byID[id]; m != nil && !slices.Contains(refs, m.ref()) {
			refs = append(refs, m.ref())
		}
	}
	return &Error{Code: CodeInvalid, Refs: refs, Message: fmt.Sprintf(
		"%s is linked to %s, and links join only cards of one kind under one parent; %s, so the owner removes those links first",
		n.ref(), strings.Join(refs, ", "), why)}
}

// link records blocker blocks blocked, after the level, cycle and duplicate
// checks. Links made before the level rule keep working; only new ones are
// checked.
func (t *txn) link(o *outline, blocker, blocked *node) error {
	if err := o.sameLevel(blocker, blocked); err != nil {
		return err
	}
	for _, n := range []*node{blocked, blocker} {
		if err := inProgress(n); err != nil {
			return err
		}
	}
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

// Unlink removes the link between two cards, whichever way it points. An
// agent unlinks where it may link: the blocked card is in its scope, and no
// container with work started under it. Neither card may have started
// (lock.go).
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
		oa, x, err := t.cardIn(ap, aID)
		if err != nil {
			return err
		}
		ob, y, err := t.cardIn(bp, bID)
		if err != nil {
			return err
		}
		for _, n := range []*node{x, y} {
			if err := inProgress(n); err != nil {
				return err
			}
		}
		if !a.owner() {
			// The blocked end decides the scope, as for Link.
			o, blocked := ob, y
			if slices.Contains(x.BlockedBy, y.ID) {
				o, blocked = oa, x
			}
			if err := t.inScope(o, a, blocked); err != nil {
				return err
			}
			if err := o.startedWork(blocked); err != nil {
				return err
			}
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
