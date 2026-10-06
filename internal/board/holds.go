package board

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Launch starts taskID's hold on the subtask ref and scopes the Task to the
// subtask's parent. On a container it is "Do whole story": it holds the
// container's first pending confirmed subtask and scopes the Task to the
// container. Work starts only on confirmed cards: while the held subtask or
// an ancestor is unconfirmed, Launch refuses with CodeUnconfirmed, listing
// them, unless confirm is set. Launch is an owner touch: the held subtask
// and its unconfirmed ancestors are confirmed and pinned, in the write that
// starts the hold. base is the working tree state the hold's evidence is
// measured from. Under an approved epic nothing is launched by hand
// (CodeRunOwned).
func (s *Store) Launch(ctx context.Context, a Actor, ref, taskID string, base Baseline, confirm bool) (Card, error) {
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
		err = t.mutate(a, project, true, func() error {
			o, n, err := t.cardIn(project, id)
			if err != nil {
				return err
			}
			leaf, scopeID, err := t.launchLeaf(o, a, n, confirm)
			if err != nil {
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
			return t.startHold(o, leaf, taskID, base, Lane{}, nil)
		})
		if err != nil {
			return err
		}
		out, err = t.view(project, held)
		return err
	})
	return withRevision(out, changes), err
}

// CheckLaunch refuses, without writing, a launch of ref that Launch would
// refuse for the cards' own state, so a caller can check before it creates
// the launch's Task. Launch checks again in its own write.
func (s *Store) CheckLaunch(ctx context.Context, a Actor, ref string, confirm bool) error {
	if err := permit(a, opLaunch, ""); err != nil {
		return err
	}
	return s.read(ctx, func(t *txn) error {
		o, n, err := t.find(ref)
		if err != nil {
			return err
		}
		if o.project == "" {
			return errReadOnly
		}
		_, _, err = t.launchLeaf(o, a, n, confirm)
		return err
	})
}

// Attach makes the existing Task taskID work on a subtask, as Launch does for
// a new Task: its hold starts and its scope becomes the subtask's parent. ref
// is the subtask, which must not have started, or a story or an epic, under
// which a new subtask titled title is created for the Task. It is work, so
// it refuses with CodeUnconfirmed while the subtask or a parent is
// unconfirmed, unless confirm is set, in which case they are confirmed in
// the same write. A Task works on one subtask at a time. Under an approved
// epic nothing is attached (CodeRunOwned).
func (s *Store) Attach(ctx context.Context, a Actor, ref, taskID, title string, base Baseline, confirm bool) (Card, error) {
	if err := permit(a, opLaunch, ""); err != nil {
		return Card{}, err
	}
	if strings.TrimSpace(taskID) == "" {
		return Card{}, invalid("attaching needs a Task")
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
		err = t.mutate(a, project, true, func() error {
			var seq int64
			switch err := t.tx.QueryRowContext(t.ctx, `SELECT seq FROM cards WHERE held_by = ? AND held_by <> ''`, taskID).Scan(&seq); {
			case err == nil:
				return refuse(CodeLimit, "the Task already works on #%d", seq)
			case !errors.Is(err, sql.ErrNoRows):
				return fmt.Errorf("board: read the Task's hold: %w", err)
			}
			o, n, err := t.cardIn(project, id)
			if err != nil {
				return err
			}
			if err := o.runOwned(n); err != nil {
				return err
			}
			if n.container() {
				if un := o.unconfirmed(n); len(un) > 0 && !confirm {
					return unconfirmedRefusal(un, "work on %[1]s starts only once %[1]s is confirmed, so confirm the attach")
				}
				created, err := t.create(a, project, n.ID, NewCard{Kind: KindSubtask, Title: title})
				if err != nil {
					return err
				}
				if o, n, err = t.cardIn(project, created.ID); err != nil {
					return err
				}
			} else if err := inProgress(n); err != nil {
				return err
			} else if n.stored != StatusPlanned && n.stored != StatusTodo {
				return invalid("%s is %s; only a planned or todo subtask can be attached", n.ref(), n.stored)
			}
			leaf, scopeID, err := t.launchLeaf(o, a, n, confirm)
			if err != nil {
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
			return t.startHold(o, leaf, taskID, base, Lane{}, nil)
		})
		if err != nil {
			return err
		}
		out, err = t.view(project, held)
		return err
	})
	return withRevision(out, changes), err
}

// launchLeaf resolves the subtask a launch of n holds and the scope its Task
// gets, refusing what Launch refuses before it writes: nothing under an
// approved epic is started by hand (CodeRunOwned).
func (t *txn) launchLeaf(o *outline, a Actor, n *node, confirm bool) (*node, string, error) {
	if err := o.runOwned(n); err != nil {
		return nil, "", err
	}
	leaf, scopeID := n, n.ParentID
	if n.container() {
		leaves, err := t.pending(o, n)
		if err != nil {
			return nil, "", err
		}
		if len(leaves) == 0 {
			return nil, "", invalid("%s has no pending confirmed subtasks", n.ref())
		}
		leaf, scopeID = leaves[0], n.ID
	}
	if err := permit(a, opLaunch, leaf.stored); err != nil {
		return nil, "", err
	}
	if err := o.underCancelled(leaf, nil); err != nil {
		return nil, "", err
	}
	if un := o.unconfirmed(leaf); len(un) > 0 && !confirm {
		return nil, "", unconfirmedRefusal(un, "launching confirms %[1]s, so confirm the launch")
	}
	return leaf, scopeID, nil
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
// the Task's scope and confirmed, with its ancestors (startHold). A Task may have only one hold without a pending done,
// blocked or split request at a time, and a planning Task may hold nothing.
// Nothing under an approved epic is claimed (CodeRunOwned).
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
		if err := o.runOwned(n); err != nil {
			return err
		}
		if err := o.underCancelled(n, nil); err != nil {
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
		if err := t.tx.QueryRowContext(t.ctx, openHoldsQuery, a.TaskID).Scan(&open); err != nil {
			return fmt.Errorf("board: count holds: %w", err)
		}
		if open > 0 {
			return refuse(CodeLimit, "the Task already holds a subtask without a pending request")
		}
		return t.startHold(o, n, a.TaskID, base, Lane{}, nil)
	})
}

// openHoldsQuery counts a Task's holds that have no pending done, blocked or
// split request from it; a change or cancel request exempts nothing. The
// non-empty held_by term lets SQLite use the cards_held partial index.
const openHoldsQuery = `SELECT COUNT(*) FROM cards c WHERE c.held_by = ?1 AND c.held_by <> '' AND NOT EXISTS (
	SELECT 1 FROM requests r WHERE r.card_id = c.id AND r.task_id = ?1 AND r.status = 'pending'
	AND r.kind IN ('done', 'blocked', 'split'))`

// heldQuery lists every held card from the cards_held index.
const heldQuery = `SELECT id, seq, project_id, held_by FROM cards WHERE held_by <> ''`

// agentWrite runs fn on the card ref inside one sweeping, settling write,
// after refusing Unassigned cards and cards outside an agent's scope.
func (s *Store) agentWrite(ctx context.Context, a Actor, ref string, fn func(*txn, *outline, *node) error) (Card, error) {
	return s.ownerWrite(ctx, a, ref, func(t *txn, o *outline, n *node) error {
		if err := t.inScope(o, a, n); err != nil {
			return err
		}
		return fn(t, o, n)
	})
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
	return s.ownerWrite(ctx, a, ref, func(t *txn, _ *outline, n *node) error {
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

// startHold records taskID's new attempt at n and moves n to doing. Every
// hold starts here, so it refuses while n or an ancestor is unconfirmed:
// agents plan on proposals, and work starts only on what the owner
// confirmed. A lane attempt has lane's branch and the done subtasks it
// waited on; any other attempt passes the zero Lane and nil.
func (t *txn) startHold(o *outline, n *node, taskID string, base Baseline, lane Lane, waited []string) error {
	if un := o.unconfirmed(n); len(un) > 0 {
		return unconfirmedRefusal(un, "work on %[1]s starts only once the owner confirms %[1]s")
	}
	var attempt int
	if err := t.tx.QueryRowContext(t.ctx, `SELECT COUNT(*) + 1 FROM holds WHERE card_id = ?`, n.ID).Scan(&attempt); err != nil {
		return fmt.Errorf("board: count attempts: %w", err)
	}
	blobs := base.Blobs
	if blobs == nil {
		blobs = map[string]string{}
	}
	if err := t.exec(`INSERT INTO holds (id, card_id, task_id, attempt, started_at, baseline_head, baseline_status, baseline_blobs,
		branch, waited_on) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, t.s.newID(), n.ID, taskID, attempt, stamp(t.now), base.Head,
		encode(orEmpty(base.Dirty)), encode(blobs), lane.Branch, encode(orEmpty(waited))); err != nil {
		return err
	}
	if err := t.setStatus(n, StatusDoing, taskID, ""); err != nil {
		return err
	}
	n.Lane = nil
	if lane.Branch != "" {
		n.Lane = &Lane{Branch: lane.Branch}
	}
	return nil
}

// releaseHold is the single path by which a hold ends (ADR 0005 §5): it
// closes the attempt on the held subtask n with reason and moves n to status
// to. A lane attempt that ended without landing pauses n (laneEnded). A
// subtask that is still unconfirmed when released, one held before holds
// needed confirmed cards, is given a fresh expiry.
func (t *txn) releaseHold(n *node, reason ReleaseReason, to Status, cascade string) error {
	hold, err := t.openHold(n)
	if err != nil {
		return err
	}
	if err := t.exec(`UPDATE holds SET ended_at = ?, end_reason = ? WHERE id = ?`, stamp(t.now), string(reason), hold.ID); err != nil {
		return err
	}
	if err := t.setStatus(n, to, "", cascade); err != nil {
		return err
	}
	if hold.Lane.Branch != "" {
		if err := t.laneEnded(n, hold, reason); err != nil {
			return err
		}
	}
	if n.Confirmed() || to.terminal() {
		return nil
	}
	expires := t.now.Add(ExpiryWindow)
	n.ExpiresAt = &expires
	return t.updateCard(n)
}

// laneEnded pauses the lane subtask n, whose attempt h ended for reason,
// and says so (ADR 0006 §4.5): uam pauses it when the attempt ended or was
// rejected without landing, unless a card at or above it is paused
// already, and the owner's pause stands when the owner stopped it, so it
// never relaunches by itself. uam pauses only a card under an approved
// epic. Every other reason pauses nothing and says nothing.
func (t *txn) laneEnded(n *node, h Hold, reason ReleaseReason) error {
	var pause, ended string
	switch reason {
	case ReleaseEnded, ReleaseRejected:
		pause, ended = PausedUAM, "ended without landing"
	case ReleaseOwner, ReleaseSettled:
		pause, ended = PausedOwner, "stopped"
	default:
		return nil
	}
	o, err := t.outline(n.ProjectID)
	if err != nil {
		return err
	}
	if m := o.byID[n.ID]; m == nil || o.approved(m) == nil || (pause == PausedUAM && o.pausedAt(m) != nil) {
		pause = ""
	}
	body := fmt.Sprintf("attempt #%d %s; branch %s kept", h.Attempt, ended, h.Lane.Branch)
	if pause != "" {
		n.Paused = pause
		if err := t.updateCard(n); err != nil {
			return err
		}
		body = "paused: " + body
	}
	_, err = t.addComment(n, AuthorUAM, "", body, true, false)
	return err
}

// notLanding refuses changing the status of n, or ending its hold, while
// its open lane attempt carries a landing intent (ADR 0006 §4.4): its
// commit may already be on the integration branch. Only the write that
// finishes the landing, or one the caller marked in t.landing, passes.
func (t *txn) notLanding(n *node) error {
	if n.HeldBy == "" || n.Lane == nil || n.Lane.LandedSHA == "" || t.landing[n.ID] {
		return nil
	}
	return refuse(CodeLanding, "%s is landing on its integration branch; retry once it has landed", n.ref())
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
	seq                     int64
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
// each Task ID to its stage, as the caller read it at asOf on the store's
// clock; a Task missing from it has been deleted. A hold that started at or
// after asOf may belong to a Task the snapshot predates, so it is left alone.
// uncommitted maps a Project ID to its working tree's uncommitted paths, when
// known. It reads only stored state, so Settled Tasks keep their holds across
// restarts, and when nothing has ended it writes nothing. It returns the
// number of holds released.
func (s *Store) Reconcile(ctx context.Context, tasks map[string]Stage, asOf time.Time, uncommitted map[string][]string) (int, error) {
	released := 0
	_, err := s.write(ctx, func(t *txn) error {
		released = 0
		rows, err := t.tx.QueryContext(t.ctx, heldQuery)
		if err != nil {
			return fmt.Errorf("board: load holds: %w", err)
		}
		var held []heldLeaf
		for rows.Next() {
			var h heldLeaf
			if err := rows.Scan(&h.cardID, &h.seq, &h.project, &h.taskID); err != nil {
				_ = rows.Close()
				return fmt.Errorf("board: load holds: %w", err)
			}
			held = append(held, h)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return fmt.Errorf("board: load holds: %w", err)
		}
		slices.SortFunc(held, func(a, b heldLeaf) int { return cmp.Compare(a.seq, b.seq) })
		for _, h := range endedHolds(held, tasks) {
			err := t.mutate(Actor{}, h.project, false, func() error {
				_, n, err := t.cardIn(h.project, h.cardID)
				if err != nil {
					return err
				}
				hold, err := t.openHold(n)
				if err != nil {
					return err
				}
				// A landing is left for recovery to finish.
				if !hold.StartedAt.Before(asOf) || hold.Lane.LandedSHA != "" {
					return nil
				}
				if err := t.releaseHold(n, ReleaseEnded, StatusTodo, ""); err != nil {
					return err
				}
				released++
				// A lane's release says itself how it ended; the working
				// tree read is the Project directory's, not the lane's.
				if hold.Lane.Branch != "" {
					return nil
				}
				body := fmt.Sprintf("attempt #%d ended", hold.Attempt)
				if paths, ok := uncommitted[h.project]; ok {
					body += ", uncommitted: " + listOrNone(paths)
				}
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
		baseline_blobs, ended_at, end_reason, branch, landed_sha, reverted_sha, waited_on FROM holds WHERE card_id = ? ORDER BY attempt`, cardID)
	if err != nil {
		return nil, fmt.Errorf("board: list holds: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Hold
	for rows.Next() {
		h := Hold{CardID: cardID}
		var started, dirty, blobs, ended, reason, waited string
		if err := rows.Scan(&h.ID, &h.TaskID, &h.Attempt, &started, &h.Baseline.Head, &dirty, &blobs, &ended, &reason,
			&h.Lane.Branch, &h.Lane.LandedSHA, &h.Lane.RevertedSHA, &waited); err != nil {
			return nil, fmt.Errorf("board: scan hold: %w", err)
		}
		h.EndReason = ReleaseReason(reason)
		if err := json.Unmarshal([]byte(dirty), &h.Baseline.Dirty); err != nil {
			return nil, fmt.Errorf("board: hold %s baseline: %w", h.ID, err)
		}
		if err := json.Unmarshal([]byte(blobs), &h.Baseline.Blobs); err != nil {
			return nil, fmt.Errorf("board: hold %s baseline: %w", h.ID, err)
		}
		if err := json.Unmarshal([]byte(waited), &h.WaitedOn); err != nil {
			return nil, fmt.Errorf("board: hold %s waited on: %w", h.ID, err)
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
