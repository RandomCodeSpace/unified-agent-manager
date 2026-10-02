package board

import "slices"

// A card in progress keeps its plan (ADR 0005 decision 8). Until a subtask
// starts, agents in scope and the owner plan it freely; once a Task holds it,
// or it is done, nobody changes its plan (its fields, place, links or
// checklist items) until the owner releases it or moves it back to To do.
// Ticking its checklist, comments, requests and the owner's lifecycle
// actions go on.

// started reports whether the subtask n has started: a Task holds it, or it
// is done. A container never starts as a whole.
func (n *node) started() bool {
	return !n.container() && (n.HeldBy != "" || n.stored == StatusDoing || n.stored == StatusDone)
}

// inProgress refuses a change to the plan of n while it has started.
func inProgress(n *node) error {
	switch {
	case !n.started():
		return nil
	case n.stored == StatusDone:
		return refuse(CodeInProgress, "Move %s back to To do first: it is done, and a done card keeps its plan", n.ref())
	}
	return refuse(CodeInProgress, "Release %s first: it is in progress, and a card in progress keeps its plan", n.ref())
}

// startedUnder refuses a change to the container n that would take a
// started card under it along (a move), or, with heldOnly, end a hold (a
// cancel, which leaves done cards be).
func (o *outline) startedUnder(n *node, heldOnly bool) error {
	for _, m := range o.subtree(n) {
		if m == n || !m.started() || (heldOnly && m.HeldBy == "") {
			continue
		}
		if m.stored == StatusDone {
			return refuse(CodeInProgress, "Move %s back to To do first: it is done under %s, and a done card keeps its plan", m.ref(), n.ref())
		}
		return refuse(CodeInProgress, "Release %s first: it is in progress under %s, and a card in progress keeps its plan", m.ref(), n.ref())
	}
	return nil
}

// planning reports whether p changes n's plan: any field but the owner-only
// ones, or checklist items other than their ticks.
func (p Patch) planning(n *node) bool {
	if p.Title != nil || p.Desc != nil || p.WinCondition != nil || p.Prio != nil || p.Due != nil ||
		p.Effort != nil || p.Labels != nil || p.ParentID != nil || p.Rank != nil {
		return true
	}
	return p.Checklist != nil && !slices.EqualFunc(*p.Checklist, n.Checklist, func(a, b Check) bool { return a.Text == b.Text })
}
