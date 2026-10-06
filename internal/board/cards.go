package board

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

// NewCard is a card to create. A card created by the owner is confirmed,
// except under a proposal, where it is a proposal too; one created by an
// agent expires ExpiryWindow after creation unless confirmed.
type NewCard struct {
	ProjectID    string
	Kind         Kind
	ParentID     string // a card ref; "" creates at the root
	Title        string
	Desc         string
	WinCondition string
	Prio         int // 0 takes PrioDefault
	Due          string
	Effort       string // "" takes DefaultEffort
	Labels       []string
	Checklist    []Check
}

// Patch is a partial card update; nil fields are left unchanged. The
// owner-only fields never appear in a change request.
type Patch struct {
	Title        *string   `json:"title,omitempty"`
	Desc         *string   `json:"desc,omitempty"`
	WinCondition *string   `json:"win_condition,omitempty"`
	Prio         *int      `json:"prio,omitempty"`
	Due          *string   `json:"due,omitempty"`
	Effort       *string   `json:"effort,omitempty"`
	Labels       *[]string `json:"labels,omitempty"`
	Checklist    *[]Check  `json:"checklist,omitempty"`
	// ParentID moves the card: a card ref, or "" for the root.
	ParentID *string `json:"parent_id,omitempty"`
	// Rank places the card at that index among its siblings.
	Rank *int `json:"rank,omitempty"`

	Blocked *bool `json:"-"`
	// Paused pauses (true) or resumes (false) a card under an approved
	// epic: the owner's Pause / Resume. Resume clears uam's pause too. It
	// is no part of the card's plan, so a started card takes it.
	Paused *bool `json:"-"`
	// AcceptCmd sets the subtask's acceptance command: an invalid
	// NullString inherits the Project default, a valid "" means none. It is
	// stored trimmed, so a blank one is none.
	AcceptCmd *sql.NullString `json:"-"`
	Paths     *[]string       `json:"-"`
	// ProjectID moves a card out of Unassigned into a Project.
	ProjectID *string `json:"-"`
}

func (p Patch) empty() bool { return p == Patch{} }

func (p Patch) ownerOnly() bool {
	return p.Blocked != nil || p.Paused != nil || p.AcceptCmd != nil || p.Paths != nil || p.ProjectID != nil
}

// EditResult is an edit's outcome: the card, and the change request filed
// instead when an agent's edit needs the owner. AcrossRun says it was filed
// because the move changes the card's epic while one of them is approved,
// OutOfPause because the move takes the card out from under a pause.
type EditResult struct {
	Card       Card
	Request    *Request
	AcrossRun  bool
	OutOfPause bool
}

// ChecklistEdit ticks, unticks and appends checklist items; indexes are
// zero-based into the current checklist.
type ChecklistEdit struct {
	Tick   []int
	Untick []int
	Add    []string
}

// Filter narrows List. Zero fields match every card.
type Filter struct {
	// Query is free text matched against title, description and labels:
	// every word must appear, the last as a prefix.
	Query  string
	Status Status
	Kind   Kind
	// Parent is a card ref; only its direct children match.
	Parent string
}

// Snapshot is one Project's board at one revision: every card, including
// cancelled ones, and the pending requests.
type Snapshot struct {
	Cards    []Card
	Requests []Request
	Revision int64
}

// Detail is one card with its comments, requests and hold history.
type Detail struct {
	Card     Card
	Comments []Comment
	Requests []Request
	Holds    []Hold
}

var errReadOnly = refuse(CodeReadOnly, "Unassigned cards are read-only; move the card into a Project first")

// Create adds a card. Agents may create stories and subtasks under a
// container in their scope, and a Task with no scope may propose epics at
// the root, within the caps; the owner may create any kind anywhere the kind
// rules allow, and the owner's card confirms its unconfirmed ancestors.
func (s *Store) Create(ctx context.Context, a Actor, in NewCard) (Card, error) {
	o := opCreate
	if in.ParentID == "" {
		o = opCreateTop
		if in.Kind == KindEpic {
			o = opCreateEpic
		}
	}
	if err := permit(a, o, ""); err != nil {
		return Card{}, err
	}
	if !in.Kind.valid() {
		return Card{}, invalid("invalid kind %q", in.Kind)
	}
	var out Card
	changes, err := s.write(ctx, func(t *txn) error {
		project, parentID := in.ProjectID, ""
		if in.ParentID != "" {
			p, id, err := t.locate(in.ParentID)
			if err != nil {
				return err
			}
			if in.ProjectID != "" && p != in.ProjectID {
				return invalid("the parent is in another Project")
			}
			project, parentID = p, id
		}
		if project == "" {
			return errReadOnly
		}
		var id string
		err := t.mutate(a, project, true, func() error {
			n, err := t.create(a, project, parentID, in)
			if n != nil {
				id = n.ID
			}
			return err
		})
		if err != nil {
			return err
		}
		out, err = t.view(project, id)
		return err
	})
	return withRevision(out, changes), err
}

func (t *txn) create(a Actor, project, parentID string, in NewCard) (*node, error) {
	o, err := t.outline(project)
	if err != nil {
		return nil, err
	}
	if parentID != "" {
		parent := o.byID[parentID]
		if !parent.Kind.canHold(in.Kind) {
			return nil, parent.Kind.holdRefusal(in.Kind)
		}
		if parent.stored == StatusCancelled {
			return nil, invalid("%s is cancelled", parent.ref())
		}
		if err := o.underCancelled(parent, nil); err != nil {
			return nil, err
		}
		if err := t.inScope(o, a, parent); err != nil {
			return nil, err
		}
	} else if err := t.atRoot(a); err != nil {
		return nil, err
	}
	n := &node{stored: StatusPlanned, Card: Card{
		ProjectID: project, Kind: in.Kind, ParentID: parentID, Title: strings.TrimSpace(in.Title),
		Desc: in.Desc, WinCondition: strings.TrimSpace(in.WinCondition), Status: StatusPlanned,
		Prio: in.Prio, Due: in.Due, Effort: in.Effort, Labels: in.Labels, Checklist: in.Checklist,
		CreatedBy: a.author(), CreatedAt: t.now, UpdatedAt: t.now, MovedAt: t.now,
	}}
	if n.Prio == 0 {
		n.Prio = PrioDefault
	}
	if n.Effort == "" {
		n.Effort = DefaultEffort
	}
	if err := validateFields(n.Card); err != nil {
		return nil, err
	}
	switch {
	case a.owner() && (parentID == "" || o.byID[parentID].Confirmed()):
		n.PinnedSHA = a.Head
	case a.owner():
		// Planning under a proposal confirms nothing (decision 10).
		rearm(t.now, n)
		if err := t.rearmAncestors(o, parentID); err != nil {
			return nil, err
		}
	default:
		rearm(t.now, n)
	}
	if err := o.duplicate(parentID, n.Title, ""); err != nil {
		return nil, err
	}
	if err := t.caps(o, a, parentID, 1, 1); err != nil {
		return nil, err
	}
	if err := t.insertCard(o, n); err != nil {
		return nil, err
	}
	return n, nil
}

// caps refuses an agent write that would take its Task past a cap: created
// more cards to create, live and over its lifetime, and unconfirmed more live
// unconfirmed children of parentID. Subagents count against their Task.
func (t *txn) caps(o *outline, a Actor, parentID string, created, unconfirmed int) error {
	if a.owner() {
		return nil
	}
	author := a.author()
	if created > 0 {
		var count, total int
		if err := t.tx.QueryRowContext(t.ctx, `SELECT COALESCE(SUM(status <> 'cancelled'), 0), COUNT(*) FROM cards WHERE created_by = ?`, author).Scan(&count, &total); err != nil {
			return fmt.Errorf("board: count created cards: %w", err)
		}
		// Deleting frees no place under the ceiling, so it is checked first.
		if total+created > CapCreatedTotal {
			return refuse(CodeLimit, "the Task may create at most %d cards in its lifetime, deleted and expired ones included", CapCreatedTotal)
		}
		if count+created > CapCreated {
			return refuse(CodeLimit, "the Task may have at most %d live cards it created; deleted and expired ones don't count", CapCreated)
		}
	}
	if unconfirmed > 0 {
		count := 0
		for _, kid := range o.kids[parentID] {
			if kid.CreatedBy == author && !kid.Confirmed() && kid.stored != StatusCancelled {
				count++
			}
		}
		if count+unconfirmed > CapUnconfirmed {
			return refuse(CodeLimit, "the Task may add at most %d unconfirmed children to one container", CapUnconfirmed)
		}
	}
	return nil
}

// Edit applies p to the card ref. The owner may edit any field, and the
// edit confirms the card and its ancestors. An agent edits a card in its
// scope directly while it has not started, confirmed or not. Once a subtask
// has started its plan is locked (lock.go): the owner's change to it is
// refused, and an agent's is filed as a change request, which replaces the
// Task's earlier pending one. An agent patch carrying an owner-only field is
// refused before any write.
func (s *Store) Edit(ctx context.Context, a Actor, ref string, p Patch) (EditResult, error) {
	if p.empty() {
		return EditResult{}, invalid("nothing to change")
	}
	if p.ownerOnly() {
		if err := permit(a, opOwnerFields, ""); err != nil {
			return EditResult{}, err
		}
	}
	if p.Rank != nil && *p.Rank < 0 {
		return EditResult{}, invalid("invalid rank %d", *p.Rank)
	}
	var out EditResult
	changes, err := s.write(ctx, func(t *txn) error {
		project, id, err := t.locate(ref)
		if err != nil {
			return err
		}
		target := project
		if p.ProjectID != nil && *p.ProjectID != project {
			if project != "" || *p.ProjectID == "" {
				return invalid("only an Unassigned card can move into a Project")
			}
			target = *p.ProjectID
		} else if project == "" {
			return errReadOnly
		}
		err = t.mutate(a, target, true, func() error {
			if target != project {
				return t.moveIn(a, project, id, target, p)
			}
			var err error
			out, err = t.edit(a, project, id, p)
			return err
		})
		if err != nil {
			return err
		}
		out.Card, err = t.view(target, id)
		return err
	})
	out.Card = withRevision(out.Card, changes)
	return out, err
}

// edit applies p to the card id as Edit does, or files it as a change
// request; the result carries no card.
func (t *txn) edit(a Actor, project, id string, p Patch) (EditResult, error) {
	o, n, err := t.cardIn(project, id)
	if err != nil {
		return EditResult{}, err
	}
	if n.stored == StatusCancelled {
		return EditResult{}, invalid("%s is cancelled; restore it first", n.ref())
	}
	if err := t.inScope(o, a, n); err != nil {
		return EditResult{}, err
	}
	if !a.owner() && a.Proposals && n.Confirmed() {
		return EditResult{}, refuse(CodeForbidden, "%s is confirmed; this agent edits only proposals", n.ref())
	}
	locked := n.started() && p.planning(n)
	if locked && a.owner() {
		return EditResult{}, inProgress(n)
	}
	if err := permit(a, opEdit, ""); err != nil {
		return EditResult{}, err
	}
	plan, err := t.planEdit(o, a, n, p)
	if err != nil {
		return EditResult{}, err
	}
	// An agent never puts a confirmed card under a proposal, which the owner
	// could dismiss with it: the owner decides that move, and accepting it
	// confirms the proposal.
	if !a.owner() && plan.moving && n.Confirmed() && plan.parent != "" && len(o.unconfirmed(o.byID[plan.parent])) > 0 {
		locked = true
	}
	// Nor does it move a card into or out of an approved epic, where it would
	// run, or stop running, under settings the owner did not approve for it
	// (ADR 0006 §6.3 rule 5).
	across := !a.owner() && plan.moving && o.crossesRun(n, plan.parent)
	// Nor out from under a pause, which only the owner lifts.
	unpaused := !a.owner() && plan.moving && !across && o.leavesPause(n, plan.parent)
	if locked || across || unpaused {
		if err := permit(a, opChange, ""); err != nil {
			return EditResult{}, err
		}
		req, err := t.fileRequest(o, a, n, requestFiling{kind: RequestChange, payload: payload{Patch: &p}})
		return EditResult{Request: &req, AcrossRun: across && !n.started(), OutOfPause: unpaused && !n.started()}, err
	}
	return EditResult{}, t.applyEdit(o, a, n, p, plan)
}

// editPlan is a validated edit: the card after it and where it will sit.
type editPlan struct {
	card   Card
	parent string
	moving bool
}

// planEdit validates p against n without writing, so a change request is
// refused at filing when it could never apply.
func (t *txn) planEdit(o *outline, a Actor, n *node, p Patch) (editPlan, error) {
	c := n.Card
	for _, f := range []struct {
		from *string
		to   *string
	}{{p.Title, &c.Title}, {p.Desc, &c.Desc}, {p.WinCondition, &c.WinCondition}, {p.Due, &c.Due}, {p.Effort, &c.Effort}} {
		if f.from != nil {
			*f.to = *f.from
		}
	}
	c.Title, c.WinCondition = strings.TrimSpace(c.Title), strings.TrimSpace(c.WinCondition)
	if p.Prio != nil {
		c.Prio = *p.Prio
	}
	if p.Labels != nil {
		c.Labels = slices.Clone(*p.Labels)
	}
	if p.Checklist != nil {
		c.Checklist = slices.Clone(*p.Checklist)
	}
	if p.Blocked != nil {
		c.Blocked = *p.Blocked
	}
	if p.AcceptCmd != nil {
		c.AcceptCmd = nil
		if p.AcceptCmd.Valid {
			cmd := strings.TrimSpace(p.AcceptCmd.String)
			c.AcceptCmd = &cmd
		}
	}
	if p.Paths != nil {
		c.Paths = slices.Clone(*p.Paths)
	}
	if p.Paused != nil {
		if o.approved(n) == nil {
			return editPlan{}, invalid("%s is not under an approved epic; only approved work is paused", n.ref())
		}
		c.Paused = ""
		if *p.Paused {
			c.Paused = PausedOwner
		}
	}
	if err := validateFields(c); err != nil {
		return editPlan{}, err
	}
	if err := validateOwnerFields(c); err != nil {
		return editPlan{}, err
	}
	plan := editPlan{card: c, parent: n.ParentID}
	if p.ParentID != nil {
		parent := ""
		if ref := strings.TrimSpace(*p.ParentID); ref != "" {
			project, id, err := t.locate(ref)
			if err != nil {
				return editPlan{}, err
			}
			if project != o.project {
				return editPlan{}, invalid("the new parent is in another Project")
			}
			parent = id
		}
		plan.parent, plan.moving = parent, parent != n.ParentID
	}
	if plan.moving {
		if err := o.unlinkedFor(n, "moving it changes its parent"); err != nil {
			return editPlan{}, err
		}
		if err := o.startedUnder(n, false); err != nil {
			return editPlan{}, err
		}
		if err := t.checkParent(o, a, n, plan.parent); err != nil {
			return editPlan{}, err
		}
	}
	if plan.moving || normalTitle(c.Title) != normalTitle(n.Title) {
		if err := o.duplicate(plan.parent, c.Title, n.ID); err != nil {
			return editPlan{}, err
		}
	}
	return plan, nil
}

// checkParent refuses moving n under parent when the kinds don't allow it,
// the parent is cancelled, or it is outside an agent's scope. Kind ranks
// strictly decrease downwards, so a card can never be moved under itself.
func (t *txn) checkParent(o *outline, a Actor, n *node, parent string) error {
	if parent == "" {
		return permit(a, opCreateTop, "")
	}
	pn := o.byID[parent]
	if !pn.Kind.canHold(n.Kind) {
		return pn.Kind.holdRefusal(n.Kind)
	}
	if pn.stored == StatusCancelled {
		return invalid("%s is cancelled", pn.ref())
	}
	if err := o.underCancelled(pn, nil); err != nil {
		return err
	}
	if err := t.inScope(o, a, pn); err != nil {
		return err
	}
	if n.CreatedBy == a.author() && !n.Confirmed() {
		return t.caps(o, a, parent, 0, 1)
	}
	return nil
}

func (t *txn) applyEdit(o *outline, a Actor, n *node, p Patch, plan editPlan) error {
	oldParent := n.ParentID
	c := plan.card
	n.Title, n.Desc, n.WinCondition, n.Prio, n.Due, n.Effort = c.Title, c.Desc, c.WinCondition, c.Prio, c.Due, c.Effort
	n.Labels, n.Checklist, n.Blocked, n.AcceptCmd, n.Paths, n.Paused = c.Labels, c.Checklist, c.Blocked, c.AcceptCmd, c.Paths, c.Paused
	if plan.moving || p.Rank != nil {
		if err := t.place(o, n, plan.parent, p.Rank); err != nil {
			return err
		}
	}
	// Only a card under an approved epic is paused: moved out of one, the
	// card and everything under it drop their pauses.
	if plan.moving && o.approved(n) == nil {
		n.Paused = ""
		for _, m := range o.subtree(n)[1:] {
			if m.Paused != "" {
				m.Paused = ""
				if err := t.updateCard(m); err != nil {
					return err
				}
			}
		}
	}
	if a.owner() {
		if err := t.planned(o, a, n); err != nil {
			return err
		}
	}
	if err := t.updateCard(n); err != nil {
		return err
	}
	if p.AcceptCmd != nil {
		if err := t.commandChanged(o.project, n.ID); err != nil {
			return err
		}
	}
	if plan.moving && oldParent != "" {
		t.changed(o.project, oldParent)
	}
	if p.Labels != nil {
		return t.upsertLabels(o.project, n.Labels)
	}
	return nil
}

// moveIn moves an Unassigned card, with its subtree, into project, applying
// the rest of p. Moving counts as a touch.
func (t *txn) moveIn(a Actor, from, id, project string, p Patch) error {
	src, n, err := t.cardIn(from, id)
	if err != nil {
		return err
	}
	o, err := t.outline(project)
	if err != nil {
		return err
	}
	moved := *n
	moved.ProjectID, moved.ParentID = project, ""
	p.ProjectID = nil
	if p.ParentID == nil {
		root := ""
		p.ParentID = &root
	}
	plan, err := t.planEdit(o, a, &moved, p)
	if err != nil {
		return err
	}
	if err := o.duplicate(plan.parent, plan.card.Title, n.ID); err != nil {
		return err
	}
	plan.moving = true
	for _, m := range src.subtree(n) {
		if err := t.exec(`UPDATE cards SET project_id = ?, updated_at = ? WHERE id = ?`, project, stamp(t.now), m.ID); err != nil {
			return err
		}
		t.removed(from, m.ID)
		t.changed(project, m.ID)
	}
	return t.applyEdit(o, a, &moved, p, plan)
}

// place puts n under parent at index rank among its siblings, rewriting the
// siblings' ranks, or after the last sibling when rank is nil.
func (t *txn) place(o *outline, n *node, parent string, rank *int) error {
	var sibs []*node
	for _, sib := range o.kids[parent] {
		if sib.ID != n.ID {
			sibs = append(sibs, sib)
		}
	}
	n.ParentID = parent
	if rank == nil {
		n.Rank = 0
		if len(sibs) > 0 {
			n.Rank = sibs[len(sibs)-1].Rank + 1
		}
		return nil
	}
	index := min(*rank, len(sibs))
	return t.rerank(o, slices.Concat(sibs[:index], []*node{n}, sibs[index:]), n)
}

// rerank gives each card in ordered its index as its rank, writing each one
// whose rank changed except skip, which the caller writes.
func (t *txn) rerank(o *outline, ordered []*node, skip *node) error {
	for i, m := range ordered {
		if m.Rank == i {
			continue
		}
		m.Rank = i
		if m == skip {
			continue
		}
		if err := t.exec(`UPDATE cards SET rank = ?, updated_at = ? WHERE id = ?`, i, stamp(t.now), m.ID); err != nil {
			return err
		}
		t.changed(o.project, m.ID)
	}
	return nil
}

// Checklist ticks, unticks and adds checklist items. Agents may do this on
// confirmed cards in their scope too. On a started subtask nothing is added,
// and only its Task and the owner tick.
func (s *Store) Checklist(ctx context.Context, a Actor, ref string, e ChecklistEdit) (Card, error) {
	if err := permit(a, opChecklist, ""); err != nil {
		return Card{}, err
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
		err = t.mutate(a, project, true, func() error {
			o, n, err := t.cardIn(project, id)
			if err != nil {
				return err
			}
			if n.stored.terminal() {
				return invalid("%s is %s", n.ref(), n.stored)
			}
			if err := t.inScope(o, a, n); err != nil {
				return err
			}
			// A started subtask's items are its plan; ticking them is
			// progress, for its Task and the owner.
			if n.started() && len(e.Add) > 0 {
				return inProgress(n)
			}
			if n.started() && !a.owner() && n.HeldBy != a.TaskID {
				return refuse(CodeForbidden, "%s is in progress: only the Task holding it ticks its checklist", n.ref())
			}
			list := slices.Clone(n.Checklist)
			for _, set := range []struct {
				indexes []int
				done    bool
			}{{e.Tick, true}, {e.Untick, false}} {
				for _, i := range set.indexes {
					if i < 0 || i >= len(list) {
						return invalid("no checklist item %d on %s", i, n.ref())
					}
					list[i].Done = set.done
				}
			}
			for _, text := range e.Add {
				list = append(list, Check{Text: strings.TrimSpace(text)})
			}
			c := n.Card
			c.Checklist = list
			if err := validateFields(c); err != nil {
				return err
			}
			n.Checklist = list
			if a.owner() {
				if err := t.planned(o, a, n); err != nil {
					return err
				}
			}
			return t.updateCard(n)
		})
		if err != nil {
			return err
		}
		out, err = t.view(project, id)
		return err
	})
	return withRevision(out, changes), err
}

// Confirm confirms a card: it stops expiring and is pinned to the owner's
// HEAD, and so is every unconfirmed ancestor. Under an approved epic it is
// refused with CodeRunOwned: the owner approves there from the epic.
func (s *Store) Confirm(ctx context.Context, a Actor, ref string) (Card, error) {
	if err := permit(a, opConfirm, ""); err != nil {
		return Card{}, err
	}
	return s.ownerWrite(ctx, a, ref, func(t *txn, o *outline, n *node) error {
		if err := o.runOwned(n); err != nil {
			return err
		}
		if n.stored == StatusCancelled {
			return invalid("%s is cancelled; restore it instead", n.ref())
		}
		if err := t.confirm(o, a, n); err != nil {
			return err
		}
		return t.updateCard(n)
	})
}

// ownerWrite runs fn on the card ref inside one sweeping, settling write by
// a to its Project, refusing Unassigned cards, and returns the card
// afterwards.
func (s *Store) ownerWrite(ctx context.Context, a Actor, ref string, fn func(*txn, *outline, *node) error) (Card, error) {
	var out Card
	changes, err := s.write(ctx, func(t *txn) error {
		project, id, err := t.locate(ref)
		if err != nil {
			return err
		}
		if project == "" {
			return errReadOnly
		}
		err = t.mutate(a, project, true, func() error {
			o, n, err := t.cardIn(project, id)
			if err != nil {
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

// confirm is an owner touch on n: it confirms n and pins it to the owner's
// HEAD, when known, and does the same to every unconfirmed ancestor, so a
// confirmed card never sits under an unconfirmed one. The caller writes n.
func (t *txn) confirm(o *outline, a Actor, n *node) error {
	touch(a, n)
	return t.confirmAncestors(o, a, n.ParentID)
}

// confirmAncestors confirms, pins and writes every unconfirmed card from
// parentID up to the root.
func (t *txn) confirmAncestors(o *outline, a Actor, parentID string) error {
	for p := o.byID[parentID]; p != nil; p = o.byID[p.ParentID] {
		if p.Confirmed() {
			continue
		}
		touch(a, p)
		if err := t.updateCard(p); err != nil {
			return err
		}
	}
	return nil
}

// touch confirms n and pins it to the owner's HEAD, when known.
func touch(a Actor, n *node) {
	n.ExpiresAt = nil
	if a.Head != "" {
		n.PinnedSHA = a.Head
	}
}

// planned records the owner's planning write on n (an edit, a move, a
// checklist change, a split), which confirms no proposal (decision 10). A
// confirmed n is re-pinned to the owner's HEAD, and moving it under a
// proposal confirms that proposal, since a confirmed card never sits under
// one. A proposal and its proposed parents get a fresh expiry instead, so
// one the owner is working on doesn't lapse. The caller writes n.
func (t *txn) planned(o *outline, a Actor, n *node) error {
	if n.Confirmed() {
		return t.confirm(o, a, n)
	}
	rearm(t.now, n)
	return t.rearmAncestors(o, n.ParentID)
}

// rearmAncestors gives every unconfirmed card from parentID up to the root
// a fresh expiry and writes it.
func (t *txn) rearmAncestors(o *outline, parentID string) error {
	for p := o.byID[parentID]; p != nil; p = o.byID[p.ParentID] {
		if p.Confirmed() {
			continue
		}
		rearm(t.now, p)
		if err := t.updateCard(p); err != nil {
			return err
		}
	}
	return nil
}

// rearm makes the unconfirmed n expire ExpiryWindow from now.
func rearm(now time.Time, n *node) {
	expires := now.Add(ExpiryWindow)
	n.ExpiresAt = &expires
}

// view returns the card id as the Project's outline now derives it.
func (t *txn) view(project, id string) (Card, error) {
	_, n, err := t.cardIn(project, id)
	if err != nil {
		return Card{}, err
	}
	return n.Card, nil
}

// withRevision sets c's revision to the one its write committed.
func withRevision(c Card, changes []Change) Card {
	for _, ch := range changes {
		if ch.ProjectID == c.ProjectID && slices.Contains(ch.Cards, c.ID) {
			c.Revision = ch.Revision
		}
	}
	return c
}

// encode marshals a stored JSON column. Its values are strings, checks and
// request payloads, which always encode.
func encode(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func orEmpty[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

func nullTime(n *node) any {
	if n.ExpiresAt == nil {
		return nil
	}
	return stamp(*n.ExpiresAt)
}

func nullString(v *string) any {
	if v == nil {
		return nil
	}
	return *v
}

func (t *txn) insertCard(o *outline, n *node) error {
	seq, err := t.nextSeq()
	if err != nil {
		return err
	}
	n.ID, n.Seq = t.s.newID(), seq
	if kids := o.kids[n.ParentID]; len(kids) > 0 {
		n.Rank = kids[len(kids)-1].Rank + 1
	}
	labels, checklist, paths := encode(orEmpty(n.Labels)), encode(orEmpty(n.Checklist)), encode(orEmpty(n.Paths))
	if err := t.exec(`INSERT INTO cards (id, seq, project_id, kind, parent_id, rank, title, "desc", win_condition,
		status, prio, due, effort, labels, checklist, blocked, expires_at, held_by, pinned_sha, accept_cmd, paths,
		paused, cascade_id, created_by, created_at, updated_at, moved_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.ID, n.Seq, n.ProjectID, string(n.Kind), n.ParentID, n.Rank, n.Title, n.Desc, n.WinCondition,
		string(n.stored), n.Prio, n.Due, n.Effort, labels, checklist, boolInt(n.Blocked), nullTime(n), n.HeldBy,
		n.PinnedSHA, nullString(n.AcceptCmd), paths, n.Paused, n.CascadeID, n.CreatedBy,
		stamp(n.CreatedAt), stamp(n.UpdatedAt), stamp(n.MovedAt)); err != nil {
		return err
	}
	t.changed(n.ProjectID, n.ID)
	return t.upsertLabels(n.ProjectID, n.Labels)
}

// updateCard writes n's fields. Status, hold and cascade change only through
// setStatus.
func (t *txn) updateCard(n *node) error {
	n.UpdatedAt = t.now
	labels, checklist, paths := encode(orEmpty(n.Labels)), encode(orEmpty(n.Checklist)), encode(orEmpty(n.Paths))
	if err := t.exec(`UPDATE cards SET project_id = ?, kind = ?, parent_id = ?, rank = ?, title = ?, "desc" = ?,
		win_condition = ?, prio = ?, due = ?, effort = ?, labels = ?, checklist = ?, blocked = ?, expires_at = ?,
		pinned_sha = ?, accept_cmd = ?, paths = ?, paused = ?, updated_at = ? WHERE id = ?`,
		n.ProjectID, string(n.Kind), n.ParentID, n.Rank, n.Title, n.Desc, n.WinCondition, n.Prio, n.Due, n.Effort,
		labels, checklist, boolInt(n.Blocked), nullTime(n), n.PinnedSHA, nullString(n.AcceptCmd), paths, n.Paused,
		stamp(n.UpdatedAt), n.ID); err != nil {
		return err
	}
	t.changed(n.ProjectID, n.ID)
	return nil
}

// setStatus is the only writer of a card's status, hold and cascade. Any
// status change withdraws the card's pending requests, so none is made
// while the card is landing.
func (t *txn) setStatus(n *node, to Status, heldBy, cascade string) error {
	if err := t.notLanding(n); err != nil {
		return err
	}
	n.stored, n.HeldBy, n.CascadeID, n.MovedAt, n.UpdatedAt = to, heldBy, cascade, t.now, t.now
	if !n.container() {
		n.Status = to
	}
	if err := t.exec(`UPDATE cards SET status = ?, held_by = ?, cascade_id = ?, moved_at = ?, updated_at = ? WHERE id = ?`,
		string(to), heldBy, cascade, stamp(t.now), stamp(t.now), n.ID); err != nil {
		return err
	}
	t.changed(n.ProjectID, n.ID)
	return t.withdraw(n, to == StatusTodo)
}

// withdraw marks n's pending requests withdrawn. With planning set, as n
// returns to To do, its change and split requests stay: they change its
// plan, which they may only once it is no longer in progress (lock.go).
func (t *txn) withdraw(n *node, planning bool) error {
	ids, err := t.ids(`SELECT id FROM requests WHERE card_id = ?1 AND status = 'pending'
		AND NOT (?2 AND kind IN ('change', 'split'))`, n.ID, planning)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := t.exec(`UPDATE requests SET status = 'withdrawn', decided_at = ? WHERE id = ?`, stamp(t.now), id); err != nil {
			return err
		}
		t.requestChanged(n.ProjectID, id)
	}
	n.PendingRequests = max(0, n.PendingRequests-len(ids))
	return nil
}

// ids runs a query returning one text column.
func (t *txn) ids(query string, args ...any) ([]string, error) {
	rows, err := t.tx.QueryContext(t.ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("board: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("board: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("board: %w", err)
	}
	return out, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// upsertLabels records labels as the Project's most recently used.
func (t *txn) upsertLabels(project string, labels []string) error {
	for _, label := range labels {
		if err := t.exec(`INSERT INTO sequences (name, next) VALUES ('label', 1)
			ON CONFLICT(name) DO UPDATE SET next = next + 1`); err != nil {
			return err
		}
		if err := t.exec(`INSERT INTO labels (project_id, label, last_used)
			VALUES (?, ?, (SELECT next FROM sequences WHERE name = 'label'))
			ON CONFLICT(project_id, label) DO UPDATE SET last_used = excluded.last_used`, project, label); err != nil {
			return err
		}
	}
	return nil
}

// Labels lists a Project's labels, most recently used first.
func (s *Store) Labels(ctx context.Context, projectID string) ([]string, error) {
	var out []string
	err := s.read(ctx, func(t *txn) error {
		var err error
		out, err = t.ids(`SELECT label FROM labels WHERE project_id = ? ORDER BY last_used DESC, label`, projectID)
		return err
	})
	return out, err
}

// Card returns the card ref.
func (s *Store) Card(ctx context.Context, ref string) (Card, error) {
	var out Card
	err := s.read(ctx, func(t *txn) error {
		_, n, err := t.find(ref)
		if err == nil {
			out = n.Card
		}
		return err
	})
	return out, err
}

// Cards returns the cards with the given IDs that still exist, in the order
// given.
func (s *Store) Cards(ctx context.Context, ids []string) ([]Card, error) {
	var out []Card
	err := s.read(ctx, func(t *txn) error {
		for _, id := range ids {
			_, n, err := t.find(id)
			if CodeOf(err) == CodeNotFound {
				continue
			}
			if err != nil {
				return err
			}
			out = append(out, n.Card)
		}
		return nil
	})
	return out, err
}

// Board returns one Project's snapshot; "" is the Unassigned list.
func (s *Store) Board(ctx context.Context, projectID string) (Snapshot, error) {
	var out Snapshot
	err := s.read(ctx, func(t *txn) error {
		o, err := t.outline(projectID)
		if err != nil {
			return err
		}
		for _, n := range o.order {
			out.Cards = append(out.Cards, n.Card)
		}
		if out.Requests, err = t.queryRequests(requestCols+`WHERE c.project_id = ? AND r.status = 'pending' ORDER BY r.created_at, r.id`, projectID); err != nil {
			return err
		}
		out.Revision, err = t.revision(projectID)
		return err
	})
	return out, err
}

// Detail returns the card ref with its comments, requests and holds.
func (s *Store) Detail(ctx context.Context, ref string) (Detail, error) {
	var out Detail
	err := s.read(ctx, func(t *txn) error {
		_, n, err := t.find(ref)
		if err != nil {
			return err
		}
		out.Card = n.Card
		if out.Comments, err = t.comments(n.ID); err != nil {
			return err
		}
		if out.Requests, err = t.queryRequests(requestCols+`WHERE r.card_id = ? ORDER BY r.created_at, r.id`, n.ID); err != nil {
			return err
		}
		out.Holds, err = t.holds(n.ID)
		return err
	})
	return out, err
}

// List returns a Project's cards matching f, in outline order.
func (s *Store) List(ctx context.Context, projectID string, f Filter) ([]Card, error) {
	if len(f.Query) > maxSearchBytes {
		return nil, invalid("search query too long")
	}
	var out []Card
	err := s.read(ctx, func(t *txn) error {
		o, err := t.outline(projectID)
		if err != nil {
			return err
		}
		parent := ""
		if f.Parent != "" {
			if _, parent, err = t.locate(f.Parent); err != nil {
				return err
			}
		}
		var hits map[string]bool
		if match := ftsSearchQuery(f.Query); match != "" {
			ids, err := t.ids(`SELECT id FROM cards_fts WHERE cards_fts MATCH ? AND project = ?`, match, projectID)
			if err != nil {
				return err
			}
			hits = map[string]bool{}
			for _, id := range ids {
				hits[id] = true
			}
		}
		for _, n := range o.order {
			if (f.Status != "" && n.Status != f.Status) || (f.Kind != "" && n.Kind != f.Kind) ||
				(f.Parent != "" && n.ParentID != parent) || (hits != nil && !hits[n.ID]) {
				continue
			}
			out = append(out, n.Card)
		}
		return nil
	})
	return out, err
}

// PendingLeaves returns the confirmed subtasks under the container ref that
// are waiting to be worked on, depth-first, with blocked ones last.
func (s *Store) PendingLeaves(ctx context.Context, ref string) ([]Card, error) {
	var out []Card
	err := s.read(ctx, func(t *txn) error {
		o, n, err := t.find(ref)
		if err != nil {
			return err
		}
		leaves, err := t.pending(o, n)
		for _, l := range leaves {
			out = append(out, l.Card)
		}
		return err
	})
	return out, err
}

func (t *txn) pending(o *outline, n *node) ([]*node, error) {
	if !n.container() {
		return nil, invalid("%s is a subtask", n.ref())
	}
	var ready, blocked []*node
	for _, l := range n.leaves {
		if !l.Confirmed() || l.HeldBy != "" || (l.stored != StatusPlanned && l.stored != StatusTodo) {
			continue
		}
		waits, err := t.waitsOn(o, l)
		if err != nil {
			return nil, err
		}
		if l.Blocked || len(waits) > 0 {
			blocked = append(blocked, l)
		} else {
			ready = append(ready, l)
		}
	}
	return append(ready, blocked...), nil
}

// StaleCandidates returns the subtasks staleness is computed for: confirmed,
// not terminal and not held.
func (s *Store) StaleCandidates(ctx context.Context, projectID string) ([]Card, error) {
	var out []Card
	err := s.read(ctx, func(t *txn) error {
		o, err := t.outline(projectID)
		if err != nil {
			return err
		}
		for _, n := range o.order {
			if !n.container() && n.Confirmed() && n.HeldBy == "" && !n.stored.terminal() {
				out = append(out, n.Card)
			}
		}
		return nil
	})
	return out, err
}
