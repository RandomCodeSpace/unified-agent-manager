package board

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
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

// The owner's Revert of landed work (ADR 0006 §5.7) rolls forward, as a
// landing does: Revert records it in the store before the integration
// branch moves to the revert commit, and UndoRevert withdraws it when the
// branch never took that commit.

// Landing is a landed subtask's commit on its integration branch.
type Landing struct {
	CardID string
	Seq    int64
	Title  string
	SHA    string
}

// Closure is what a revert takes along: the landed subtasks to revert, in
// outline order, their landed commits, and the running attempts that
// started on top of one of them, which the owner Stops first.
type Closure struct {
	Cards    []Card
	Landings []Landing
	Running  []Card
}

// landed reports whether the subtask n is done by its latest attempt's
// landing, which no revert undid.
func (n *node) landed() bool {
	return !n.container() && n.stored == StatusDone && n.Lane != nil && n.Lane.LandedSHA != "" && n.Lane.RevertedSHA == ""
}

// RevertClosure returns what reverting the card ref takes along. The seeds
// are the subtask, or every landed subtask under a story or an epic, and
// the cards include adds the same way. The closure is the seeds and the
// landed subtasks that started on top of one of them, transitively: from
// the done subtasks each attempt recorded it waited on as it started, so a
// link changed since changes nothing. A seed with no landed work is refused.
func (s *Store) RevertClosure(ctx context.Context, ref string, include []string) (Closure, error) {
	var out Closure
	err := s.read(ctx, func(t *txn) error {
		o, n, err := t.find(ref)
		if err != nil {
			return err
		}
		if o.project == "" {
			return errReadOnly
		}
		out, err = t.closure(o, n, include)
		return err
	})
	return out, err
}

// CheckRevert refuses, without writing, a revert Revert would refuse, and
// returns its closure: the closure must be the cards expect lists (stale),
// and no running attempt may have started on top of it (revert_running).
func (s *Store) CheckRevert(ctx context.Context, a Actor, ref string, include, expect []string) (Closure, error) {
	if err := permit(a, opRevert, ""); err != nil {
		return Closure{}, err
	}
	var out Closure
	err := s.read(ctx, func(t *txn) error {
		o, n, err := t.find(ref)
		if err != nil {
			return err
		}
		if o.project == "" {
			return errReadOnly
		}
		if out, err = t.closure(o, n, include); err != nil {
			return err
		}
		return t.revertable(out, expect)
	})
	return out, err
}

// Revert records the owner's revert of the card ref, with include, in
// commit, the revert commit built on the integration tip, before the
// branch moves to it: each card of the closure is To do again, paused by
// the owner, and each of its landings is marked reverted in commit, with
// uam's comment and the reason, when given. It refuses as CheckRevert
// does, writing nothing.
func (s *Store) Revert(ctx context.Context, a Actor, ref string, include, expect []string, commit, reason string) (Closure, error) {
	if err := permit(a, opRevert, ""); err != nil {
		return Closure{}, err
	}
	if err := checkSHA(commit); err != nil {
		return Closure{}, err
	}
	body := "reverted in " + shortSHA(commit)
	if reason = strings.TrimSpace(reason); reason != "" {
		why, err := checkComment(reason)
		if err != nil {
			return Closure{}, err
		}
		body += ": " + why
	}
	var out Closure
	_, err := s.ownerWrite(ctx, a, ref, func(t *txn, o *outline, n *node) error {
		c, err := t.closure(o, n, include)
		if err != nil {
			return err
		}
		if err := t.revertable(c, expect); err != nil {
			return err
		}
		for _, card := range c.Cards {
			m := o.byID[card.ID]
			if err := t.setStatus(m, StatusTodo, "", ""); err != nil {
				return err
			}
			m.Paused = PausedOwner
			if err := t.updateCard(m); err != nil {
				return err
			}
			if err := t.exec(`UPDATE holds SET reverted_sha = ? WHERE card_id = ? AND branch <> '' AND landed_sha <> ''
				AND reverted_sha = '' AND end_reason = ?`, commit, m.ID, string(ReleaseAccepted)); err != nil {
				return err
			}
			if _, err := t.addComment(m, AuthorUAM, "", body, true, false); err != nil {
				return err
			}
		}
		out = c
		return nil
	})
	return out, err
}

// UndoRevert withdraws the revert recorded in commit, which the integration
// branch never took (ADR 0006 §4.4): its landings are unmarked, each of its
// subtasks still To do and unheld is done again and no longer paused, and
// uam's comment on each says why with reason. These are uam's writes. A
// pause the owner set on a landed subtask before the revert is not kept: it
// held nothing back, since a landed subtask goes back to To do only through
// a Revert or a Reopen, which both pause it.
func (s *Store) UndoRevert(ctx context.Context, commit, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return invalid("undoing a revert needs a reason")
	}
	body, err := checkComment("revert not applied: " + strings.TrimSpace(reason))
	if err != nil {
		return err
	}
	_, err = s.write(ctx, func(t *txn) error {
		rows, err := t.tx.QueryContext(t.ctx, `SELECT DISTINCT c.project_id, c.id, c.seq FROM holds h JOIN cards c ON c.id = h.card_id
			WHERE h.reverted_sha = ? AND c.project_id <> '' ORDER BY c.seq`, commit)
		if err != nil {
			return fmt.Errorf("board: find a revert: %w", err)
		}
		cards := map[string][]string{}
		var projects []string
		for rows.Next() {
			var project, id string
			var seq int64
			if err := rows.Scan(&project, &id, &seq); err != nil {
				_ = rows.Close()
				return fmt.Errorf("board: find a revert: %w", err)
			}
			if cards[project] == nil {
				projects = append(projects, project)
			}
			cards[project] = append(cards[project], id)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return fmt.Errorf("board: find a revert: %w", err)
		}
		for _, project := range projects {
			err := t.mutate(Actor{}, project, false, func() error {
				o, err := t.outline(project)
				if err != nil {
					return err
				}
				for _, id := range cards[project] {
					n := o.byID[id]
					if err := t.exec(`UPDATE holds SET reverted_sha = '' WHERE card_id = ? AND reverted_sha = ?`, id, commit); err != nil {
						return err
					}
					if n.stored == StatusTodo && n.HeldBy == "" {
						if err := t.setStatus(n, StatusDone, "", ""); err != nil {
							return err
						}
						n.Paused = ""
						if err := t.updateCard(n); err != nil {
							return err
						}
					}
					if _, err := t.addComment(n, AuthorUAM, "", body, true, false); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

// Reverted lists, in #seq order, the subtasks whose latest attempt's
// landing was reverted and that are still To do and unheld: the reverts
// whose commit recovery checks is on the integration branch (ADR 0006
// §4.7).
func (s *Store) Reverted(ctx context.Context) ([]Card, error) {
	var out []Card
	err := s.read(ctx, func(t *txn) error {
		ids, err := t.ids(`SELECT c.id FROM cards c WHERE c.status = 'todo' AND c.held_by = '' AND c.project_id <> ''
			AND EXISTS (SELECT 1 FROM holds h WHERE h.card_id = c.id AND h.reverted_sha <> '') ORDER BY c.seq`)
		if err != nil {
			return err
		}
		for _, id := range ids {
			_, n, err := t.find(id)
			if err != nil {
				return err
			}
			if n.Lane != nil && n.Lane.RevertedSHA != "" {
				out = append(out, n.Card)
			}
		}
		return nil
	})
	return out, err
}

// Reopen is the owner's "Reopen without reverting code" of the landed
// subtask ref, for when Revert cannot apply (ADR 0006 §5.7): it is To do
// again, paused by the owner, and its landed commit stays on the
// integration branch integ, as uam's comment says. A non-empty comment is
// added as the owner's. Nothing moves in git, and the subtasks that landed
// on top of it stay landed.
func (s *Store) Reopen(ctx context.Context, a Actor, ref, integ, comment string) (Card, error) {
	if err := permit(a, opReady, ""); err != nil {
		return Card{}, err
	}
	if err := checkLine("integration branch", integ); err != nil {
		return Card{}, err
	}
	body := strings.TrimSpace(comment)
	if body != "" {
		var err error
		if body, err = checkComment(body); err != nil {
			return Card{}, err
		}
	}
	return s.ownerWrite(ctx, a, ref, func(t *txn, _ *outline, n *node) error {
		if !n.landed() {
			return invalid("%s has no landed work to keep; only a landed subtask is reopened without reverting its code", n.ref())
		}
		sha := n.Lane.LandedSHA
		if err := t.setStatus(n, StatusTodo, "", ""); err != nil {
			return err
		}
		n.Paused = PausedOwner
		if err := t.updateCard(n); err != nil {
			return err
		}
		if _, err := t.addComment(n, AuthorUAM, "", fmt.Sprintf("reopened without reverting %s; the code stays on %s", shortSHA(sha), integ), true, false); err != nil {
			return err
		}
		if body == "" {
			return nil
		}
		_, err := t.addComment(n, AuthorOwner, "", body, false, false)
		return err
	})
}

// closure is RevertClosure's core, on the card n of the outline o.
func (t *txn) closure(o *outline, n *node, include []string) (Closure, error) {
	type attempt struct {
		n      *node
		waited []string
	}
	landed := map[string][]Hold{}
	var running []attempt
	for _, m := range o.order {
		if m.container() || m.Lane == nil {
			continue
		}
		holds, err := t.holds(m.ID)
		if err != nil {
			return Closure{}, err
		}
		for _, h := range holds {
			switch {
			case h.Lane.Branch == "":
			case h.EndedAt == nil:
				running = append(running, attempt{m, h.WaitedOn})
			case m.landed() && h.EndReason == ReleaseAccepted && h.Lane.LandedSHA != "" && h.Lane.RevertedSHA == "":
				landed[m.ID] = append(landed[m.ID], h)
			}
		}
	}
	in := map[string]bool{}
	seed := func(m *node) error {
		if !m.container() {
			if landed[m.ID] == nil {
				return invalid("%s has no landed work to revert", m.ref())
			}
			in[m.ID] = true
			return nil
		}
		found := false
		for _, l := range m.leaves {
			if landed[l.ID] != nil {
				in[l.ID], found = true, true
			}
		}
		if !found {
			return invalid("%s has no landed subtask to revert", m.ref())
		}
		return nil
	}
	if err := seed(n); err != nil {
		return Closure{}, err
	}
	for _, ref := range include {
		_, id, err := t.locate(ref)
		if err != nil {
			return Closure{}, err
		}
		m := o.byID[id]
		if m == nil {
			return Closure{}, invalid("%s is not on this board", ref)
		}
		if err := seed(m); err != nil {
			return Closure{}, err
		}
	}
	waits := func(waited []string) bool { return slices.ContainsFunc(waited, func(id string) bool { return in[id] }) }
	for grew := true; grew; {
		grew = false
		for id, holds := range landed {
			if !in[id] && slices.ContainsFunc(holds, func(h Hold) bool { return waits(h.WaitedOn) }) {
				in[id], grew = true, true
			}
		}
	}
	var out Closure
	for _, m := range o.order {
		if !in[m.ID] {
			continue
		}
		out.Cards = append(out.Cards, m.Card)
		for _, h := range landed[m.ID] {
			out.Landings = append(out.Landings, Landing{CardID: m.ID, Seq: m.Seq, Title: m.Title, SHA: h.Lane.LandedSHA})
		}
	}
	for _, r := range running {
		if waits(r.waited) {
			out.Running = append(out.Running, r.n.Card)
		}
	}
	return out, nil
}

// revertable refuses the revert of the closure c unless its cards are the
// ones expect lists, by ID or ref (stale), and no running attempt started
// on top of one of them (revert_running).
func (t *txn) revertable(c Closure, expect []string) error {
	listed := map[string]bool{}
	var stale []string
	for _, ref := range expect {
		_, n, err := t.find(ref)
		switch {
		case CodeOf(err) == CodeNotFound:
			stale = append(stale, ref)
		case err != nil:
			return err
		default:
			listed[n.ID] = true
			if !slices.ContainsFunc(c.Cards, func(card Card) bool { return card.ID == n.ID }) {
				stale = append(stale, n.ref())
			}
		}
	}
	for _, card := range c.Cards {
		if !listed[card.ID] {
			stale = append(stale, card.ref())
		}
	}
	if len(stale) > 0 {
		return &Error{Code: CodeStale, Refs: stale, Message: fmt.Sprintf(
			"what this reverts changed since it was shown (%s); look again and revert what is there now", strings.Join(stale, ", "))}
	}
	if len(c.Running) == 0 {
		return nil
	}
	refs := refsOfCards(c.Running)
	return &Error{Code: CodeRevertRunning, Refs: refs, Message: fmt.Sprintf(
		"%s started on top of what this reverts and %s still running: Stop %s first", strings.Join(refs, ", "), isAre(len(refs)), strings.Join(refs, ", "))}
}

func refsOfCards(cards []Card) []string {
	out := make([]string, len(cards))
	for i, c := range cards {
		out[i] = c.ref()
	}
	return out
}
