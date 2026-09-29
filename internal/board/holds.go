package board

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Launch starts taskID's hold on the subtask ref and scopes the Task to the
// subtask's parent. On a container it is "Do whole story": it holds the
// container's first pending confirmed subtask and scopes the Task to the
// container. Launch is an owner touch: the held subtask and its unconfirmed
// ancestors are confirmed and pinned. base is the working tree state the
// hold's evidence is measured from.
func (s *Store) Launch(ctx context.Context, a Actor, ref, taskID string, base Baseline) (Card, error) {
	if err := permit(a, opLaunch, ""); err != nil {
		return Card{}, err
	}
	if strings.TrimSpace(taskID) == "" {
		return Card{}, invalid("a launch needs a Task")
	}
	var out Card
	changes, err := s.write(ctx, func(t *txn) error {
		project, id, err := t.locate(ref)
		if err != nil {
			return err
		}
		if project == "" {
			return errReadOnly
		}
		var held string
		err = t.mutate(project, true, func() error {
			o, n, err := t.cardIn(project, id)
			if err != nil {
				return err
			}
			leaf, scopeID := n, n.ParentID
			if n.container() {
				leaves, err := t.pending(o, n)
				if err != nil {
					return err
				}
				if len(leaves) == 0 {
					return invalid("%s has no pending confirmed subtasks", n.ref())
				}
				leaf, scopeID = leaves[0], n.ID
			}
			if err := permit(a, opLaunch, leaf.stored); err != nil {
				return err
			}
			held = leaf.ID
			if err := t.confirm(o, a, leaf); err != nil {
				return err
			}
			if err := t.updateCard(leaf); err != nil {
				return err
			}
			if err := t.setScope(taskID, project, scopeID, true); err != nil {
				return err
			}
			return t.startHold(leaf, taskID, base)
		})
		if err != nil {
			return err
		}
		out, err = t.view(project, held)
		return err
	})
	return withRevision(out, changes), err
}

// StartPlanning scopes taskID, a planning Task or a Utility scout, to the
// container ref. Such a Task creates and edits under the container and holds
// nothing.
func (s *Store) StartPlanning(ctx context.Context, a Actor, ref, taskID string) error {
	if err := permit(a, opPlan, ""); err != nil {
		return err
	}
	if strings.TrimSpace(taskID) == "" {
		return invalid("planning needs a Task")
	}
	_, err := s.write(ctx, func(t *txn) error {
		project, id, err := t.locate(ref)
		if err != nil {
			return err
		}
		if project == "" {
			return errReadOnly
		}
		_, n, err := t.cardIn(project, id)
		if err != nil {
			return err
		}
		if !n.container() {
			return invalid("%s is a subtask; plan under a container", n.ref())
		}
		return t.setScope(taskID, project, n.ID, false)
	})
	return err
}

// Claim starts the agent's Task's hold on the subtask ref, which must be in
// the Task's scope. A Task may have only one hold without a pending request
// at a time, and a planning Task may hold nothing.
func (s *Store) Claim(ctx context.Context, a Actor, ref string, base Baseline) (Card, error) {
	if err := permit(a, opClaim, ""); err != nil {
		return Card{}, err
	}
	return s.agentWrite(ctx, a, ref, func(t *txn, o *outline, n *node) error {
		if n.container() {
			return invalid("%s is a %s; only subtasks are held", n.ref(), n.Kind)
		}
		if err := permit(a, opClaim, n.stored); err != nil {
			return err
		}
		sc, err := t.scope(a.TaskID)
		if err != nil {
			return err
		}
		if sc == nil || !sc.working {
			return refuse(CodeForbidden, "a planning Task holds nothing")
		}
		var open int
		if err := t.tx.QueryRowContext(t.ctx, `SELECT COUNT(*) FROM cards c WHERE c.held_by = ? AND NOT EXISTS (
			SELECT 1 FROM requests r WHERE r.card_id = c.id AND r.task_id = ? AND r.status = 'pending')`,
			a.TaskID, a.TaskID).Scan(&open); err != nil {
			return fmt.Errorf("board: count holds: %w", err)
		}
		if open > 0 {
			return refuse(CodeLimit, "the Task already holds a subtask without a pending request")
		}
		return t.startHold(n, a.TaskID, base)
	})
}

// agentWrite runs fn on the card ref inside one sweeping, settling write,
// after refusing Unassigned cards and cards outside an agent's scope.
func (s *Store) agentWrite(ctx context.Context, a Actor, ref string, fn func(*txn, *outline, *node) error) (Card, error) {
	var out Card
	changes, err := s.write(ctx, func(t *txn) error {
		project, id, err := t.locate(ref)
		if err != nil {
			return err
		}
		if project == "" {
			return errReadOnly
		}
		err = t.mutate(project, true, func() error {
			o, n, err := t.cardIn(project, id)
			if err != nil {
				return err
			}
			if err := t.inScope(o, a, n); err != nil {
				return err
			}
			return fn(t, o, n)
		})
		if err != nil {
			return err
		}
		out, err = t.view(project, id)
		return err
	})
	return withRevision(out, changes), err
}

// ReleaseHold ends the hold on the subtask ref and returns it to todo, for
// the owner's Release (ReleaseOwner) or the Settle dialog's release
// (ReleaseSettled). A non-empty comment is added as the owner's. Every other
// way a hold ends goes through the same internal path.
func (s *Store) ReleaseHold(ctx context.Context, a Actor, ref string, reason ReleaseReason, comment string) (Card, error) {
	if err := permit(a, opRelease, ""); err != nil {
		return Card{}, err
	}
	if reason != ReleaseOwner && reason != ReleaseSettled {
		return Card{}, invalid("release reason %q is not the owner's", reason)
	}
	body := strings.TrimSpace(comment)
	if body != "" {
		var err error
		if body, err = checkComment(body); err != nil {
			return Card{}, err
		}
	}
	return s.ownerWrite(ctx, ref, func(t *txn, _ *outline, n *node) error {
		if err := permit(a, opRelease, n.stored); err != nil {
			return err
		}
		if err := t.releaseHold(n, reason, StatusTodo, ""); err != nil {
			return err
		}
		if body == "" {
			return nil
		}
		_, err := t.addComment(n, AuthorOwner, "", body, false, false)
		return err
	})
}

// startHold records taskID's new attempt at n and moves n to doing.
func (t *txn) startHold(n *node, taskID string, base Baseline) error {
	var attempt int
	if err := t.tx.QueryRowContext(t.ctx, `SELECT COUNT(*) + 1 FROM holds WHERE card_id = ?`, n.ID).Scan(&attempt); err != nil {
		return fmt.Errorf("board: count attempts: %w", err)
	}
	if err := t.exec(`INSERT INTO holds (id, card_id, task_id, attempt, started_at, baseline_head, baseline_status)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, t.s.newID(), n.ID, taskID, attempt, stamp(t.now), base.Head, encode(orEmpty(base.Dirty))); err != nil {
		return err
	}
	return t.setStatus(n, StatusDoing, taskID, "")
}

// releaseHold is the single path by which a hold ends (ADR 0005 §5): it
// closes the attempt on the held subtask n with reason and moves n to status
// to. A subtask that is still unconfirmed when released is given a fresh
// expiry.
func (t *txn) releaseHold(n *node, reason ReleaseReason, to Status, cascade string) error {
	if err := t.exec(`UPDATE holds SET ended_at = ?, end_reason = ? WHERE card_id = ? AND ended_at = ''`,
		stamp(t.now), string(reason), n.ID); err != nil {
		return err
	}
	if err := t.setStatus(n, to, "", cascade); err != nil {
		return err
	}
	if n.Confirmed() || to.terminal() {
		return nil
	}
	expires := t.now.Add(ExpiryWindow)
	n.ExpiresAt = &expires
	return t.updateCard(n)
}

// openHold returns n's current attempt.
func (t *txn) openHold(n *node) (Hold, error) {
	holds, err := t.holds(n.ID)
	if err != nil {
		return Hold{}, err
	}
	for _, h := range holds {
		if h.EndedAt == nil {
			return h, nil
		}
	}
	return Hold{}, invalid("%s has no open attempt", n.ref())
}

// heldLeaf is one held subtask as reconciliation sees it.
type heldLeaf struct {
	cardID, project, taskID string
}

// endedHolds is reconciliation's pure core: the holds whose Task is neither
// Active nor Settled. A Task missing from tasks has been deleted.
func endedHolds(held []heldLeaf, tasks map[string]Stage) []heldLeaf {
	var out []heldLeaf
	for _, h := range held {
		if stage := tasks[h.taskID]; stage != StageActive && stage != StageSettled {
			out = append(out, h)
		}
	}
	return out
}

// Reconcile releases every hold whose Task is Archived or deleted to todo,
// with the automatic comment "attempt #n ended, uncommitted: …". tasks maps
// each Task ID to its stage; a Task missing from it has been deleted.
// uncommitted maps a Project ID to its working tree's uncommitted paths, when
// known. It reads only stored state, so Settled Tasks keep their holds across
// restarts, and when nothing has ended it writes nothing. It returns the
// number of holds released.
func (s *Store) Reconcile(ctx context.Context, tasks map[string]Stage, uncommitted map[string][]string) (int, error) {
	released := 0
	_, err := s.write(ctx, func(t *txn) error {
		released = 0
		rows, err := t.tx.QueryContext(t.ctx, `SELECT id, project_id, held_by FROM cards WHERE held_by <> '' ORDER BY seq`)
		if err != nil {
			return fmt.Errorf("board: load holds: %w", err)
		}
		var held []heldLeaf
		for rows.Next() {
			var h heldLeaf
			if err := rows.Scan(&h.cardID, &h.project, &h.taskID); err != nil {
				_ = rows.Close()
				return fmt.Errorf("board: load holds: %w", err)
			}
			held = append(held, h)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return fmt.Errorf("board: load holds: %w", err)
		}
		for _, h := range endedHolds(held, tasks) {
			err := t.mutate(h.project, false, func() error {
				_, n, err := t.cardIn(h.project, h.cardID)
				if err != nil {
					return err
				}
				hold, err := t.openHold(n)
				if err != nil {
					return err
				}
				if err := t.releaseHold(n, ReleaseEnded, StatusTodo, ""); err != nil {
					return err
				}
				body := fmt.Sprintf("attempt #%d ended", hold.Attempt)
				if paths, ok := uncommitted[h.project]; ok {
					body += ", uncommitted: " + listOrNone(paths)
				}
				released++
				_, err = t.addComment(n, AuthorUAM, "", body, true, false)
				return err
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	return released, err
}

// holds lists a card's attempts, oldest first.
func (t *txn) holds(cardID string) ([]Hold, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT id, task_id, attempt, started_at, baseline_head, baseline_status,
		ended_at, end_reason FROM holds WHERE card_id = ? ORDER BY attempt`, cardID)
	if err != nil {
		return nil, fmt.Errorf("board: list holds: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Hold
	for rows.Next() {
		h := Hold{CardID: cardID}
		var started, dirty, ended, reason string
		if err := rows.Scan(&h.ID, &h.TaskID, &h.Attempt, &started, &h.Baseline.Head, &dirty, &ended, &reason); err != nil {
			return nil, fmt.Errorf("board: scan hold: %w", err)
		}
		h.EndReason = ReleaseReason(reason)
		if err := json.Unmarshal([]byte(dirty), &h.Baseline.Dirty); err != nil {
			return nil, fmt.Errorf("board: hold %s baseline: %w", h.ID, err)
		}
		if err := parseStamps([]string{started}, &h.StartedAt); err != nil {
			return nil, fmt.Errorf("board: hold %s: %w", h.ID, err)
		}
		if ended != "" {
			at, err := parseStamp(ended)
			if err != nil {
				return nil, fmt.Errorf("board: hold %s: %w", h.ID, err)
			}
			h.EndedAt = &at
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("board: list holds: %w", err)
	}
	return out, nil
}

func listOrNone(items []string) string {
	if len(items) == 0 {
		return "none"
	}
	items = slices.Clone(items)
	slices.Sort(items)
	return strings.Join(items, ", ")
}
