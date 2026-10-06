package board

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

// An approved epic (ADR 0006 §6.2): the owner approves an epic once, which
// confirms the listed subtree and writes the epic's run, the only
// authorization to run anything under it. From then on nothing under it is
// started or confirmed card by card.

// MaxParallel is the most subtasks of one epic that may run at a time.
const MaxParallel = 4

// The run modes, as a Task's.
const (
	ModeSafe = "safe"
	ModeYolo = "yolo"
)

// RunSettings is what an approval authorizes the epic's subtasks to run
// with. The model is explicit: no default stands in for it.
type RunSettings struct {
	Provider    string
	Model       string
	Effort      string
	ContextSize string
	Mode        string
	Parallel    int
}

// Run is an approved epic's run.
type Run struct {
	RunSettings
	ApprovedAt time.Time
}

// ApproveItem is a card the Approve dialog showed, at the revision it
// showed it.
type ApproveItem struct {
	ID       string
	Revision int64
}

// check refuses settings no run may take. Whether the provider offers the
// model is the caller's to check.
func (r RunSettings) check() error {
	switch {
	case strings.TrimSpace(r.Provider) == "":
		return invalid("a run needs a provider")
	case strings.TrimSpace(r.Model) == "":
		return invalid("a run needs a model; pick one, as no default stands in for it")
	case r.Mode != ModeSafe && r.Mode != ModeYolo:
		return invalid("invalid mode %q (want safe or yolo)", r.Mode)
	case r.Parallel < 1 || r.Parallel > MaxParallel:
		return invalid("parallel must be 1 to %d, not %d", MaxParallel, r.Parallel)
	}
	for _, f := range []struct{ name, v string }{{"provider", r.Provider}, {"model", r.Model}, {"effort", r.Effort}, {"context size", r.ContextSize}} {
		if err := checkLine(f.name, f.v); err != nil {
			return err
		}
	}
	return nil
}

// Approve is the owner's approval of the epic ref (ADR 0006 §6.2). items
// are the cards the dialog showed, at the revisions it showed them: each
// listed proposal is confirmed and pinned to the owner's HEAD, as an owner
// touch confirms, and a proposal not listed stays one. The epic's run is
// written, or updated when it was approved before, and its own pause is
// cleared. It refuses, writing nothing, unless the epic is live and not
// done; every item is a live card under it at its listed revision (stale
// otherwise); no subtask under it is held; every live story and the epic
// keep a live subtask that is confirmed or listed; and every such subtask
// not started resolves to an acceptance command.
func (s *Store) Approve(ctx context.Context, a Actor, ref string, settings RunSettings, items []ApproveItem) (Card, error) {
	if err := permit(a, opApprove, ""); err != nil {
		return Card{}, err
	}
	if err := settings.check(); err != nil {
		return Card{}, err
	}
	return s.ownerWrite(ctx, a, ref, func(t *txn, o *outline, n *node) error {
		listed, err := t.approvable(o, n, items)
		if err != nil {
			return err
		}
		touch(a, n)
		n.Paused = ""
		if err := t.updateCard(n); err != nil {
			return err
		}
		for _, m := range listed {
			if m == n {
				continue
			}
			if err := t.confirm(o, a, m); err != nil {
				return err
			}
			if err := t.updateCard(m); err != nil {
				return err
			}
		}
		return t.exec(`INSERT INTO runs (epic_id, provider, model, effort, context_size, mode, parallel, approved_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(epic_id) DO UPDATE SET provider = excluded.provider, model = excluded.model, effort = excluded.effort,
			context_size = excluded.context_size, mode = excluded.mode, parallel = excluded.parallel, approved_at = excluded.approved_at`,
			n.ID, settings.Provider, settings.Model, settings.Effort, settings.ContextSize, settings.Mode, settings.Parallel, stamp(t.now))
	})
}

// approvable checks Approve's refusals on the epic n and returns the listed
// cards.
func (t *txn) approvable(o *outline, n *node, items []ApproveItem) ([]*node, error) {
	switch {
	case n.Kind != KindEpic:
		return nil, invalid("%s is a %s; approve its epic", n.ref(), n.Kind)
	case n.stored == StatusCancelled:
		return nil, invalid("%s is cancelled; restore it first", n.ref())
	case n.Status == StatusDone:
		return nil, invalid("%s is done; nothing is left to run", n.ref())
	}
	tree := o.subtree(n)
	in := map[string]bool{}
	for _, m := range tree {
		in[m.ID] = true
	}
	// A card changed since the dialog showed it is stale, wherever it is
	// now, so the dialog reloads before anything else is said.
	var stale, outside []string
	listed := map[string]bool{}
	var out []*node
	for _, item := range items {
		m := o.byID[item.ID]
		if m != nil && m.Revision != item.Revision {
			stale = append(stale, m.ref())
		}
		switch {
		case m == nil:
			outside = append(outside, item.ID)
		case !in[m.ID] || m.stored == StatusCancelled || o.underCancelled(m, nil) != nil:
			outside = append(outside, m.ref())
		case !listed[m.ID]:
			listed[m.ID] = true
			out = append(out, m)
		}
	}
	if len(stale) > 0 {
		return nil, &Error{Code: CodeStale, Refs: stale, Message: fmt.Sprintf(
			"%s changed since the approval was shown; look again and approve what is there now", strings.Join(stale, ", "))}
	}
	if len(outside) > 0 {
		return nil, &Error{Code: CodeInvalid, Refs: outside, Message: fmt.Sprintf(
			"%s %s not a live card under %s", strings.Join(outside, ", "), isAre(len(outside)), n.ref())}
	}
	live := func(m *node) bool { return m.stored != StatusCancelled && o.underCancelled(m, nil) == nil }
	runs := func(l *node) bool { return live(l) && (l.Confirmed() || listed[l.ID]) }
	var held, empty, noCmd []string
	settings, err := t.settings(o.project)
	if err != nil {
		return nil, err
	}
	for _, m := range tree {
		if !live(m) {
			continue
		}
		if m.container() {
			if !slices.ContainsFunc(m.leaves, runs) {
				empty = append(empty, m.ref())
			}
			continue
		}
		if m.HeldBy != "" {
			held = append(held, m.ref())
		}
		if !runs(m) || m.started() {
			continue
		}
		cmd := strings.TrimSpace(settings.AcceptCmd)
		if m.AcceptCmd != nil {
			cmd = strings.TrimSpace(*m.AcceptCmd)
		}
		if cmd == "" {
			noCmd = append(noCmd, m.ref())
		}
	}
	switch {
	case len(held) > 0:
		return nil, &Error{Code: CodeInvalid, Refs: held, Message: fmt.Sprintf(
			"%s %s held by a Task; finish or release it first, as work started by hand and approved work never mix", strings.Join(held, ", "), isAre(len(held)))}
	case len(empty) > 0:
		return nil, &Error{Code: CodeInvalid, Refs: empty, Message: fmt.Sprintf(
			"%s would have no confirmed subtask, and a card with none never finishes; list a subtask under it, add one, or cancel it", strings.Join(empty, ", "))}
	case len(noCmd) > 0:
		return nil, &Error{Code: CodeInvalid, Refs: noCmd, Message: fmt.Sprintf(
			"%s %s no acceptance command, so its done would always wait for you; set one on it or a Project default", strings.Join(noCmd, ", "), hasHave(len(noCmd)))}
	}
	return out, nil
}

func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

func hasHave(n int) string {
	if n == 1 {
		return "has"
	}
	return "have"
}

// loadRuns fills each approved epic's Run.
func (t *txn) loadRuns(o *outline) error {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT epic_id, provider, model, effort, context_size, mode, parallel, approved_at
		FROM runs WHERE epic_id IN (SELECT id FROM cards WHERE project_id = ?)`, o.project)
	if err != nil {
		return fmt.Errorf("board: load runs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, at string
		var r Run
		if err := rows.Scan(&id, &r.Provider, &r.Model, &r.Effort, &r.ContextSize, &r.Mode, &r.Parallel, &at); err != nil {
			return fmt.Errorf("board: load runs: %w", err)
		}
		if r.ApprovedAt, err = parseStamp(at); err != nil {
			return fmt.Errorf("board: run of %s: %w", id, err)
		}
		if n := o.byID[id]; n != nil {
			n.Run = &r
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("board: load runs: %w", err)
	}
	return nil
}

// epicOf returns the epic n sits under, n itself for an epic; nil at the
// root, which counts as no epic.
func (o *outline) epicOf(n *node) *node {
	top := n
	for p := o.byID[n.ParentID]; p != nil; p = o.byID[p.ParentID] {
		top = p
	}
	if top.Kind != KindEpic {
		return nil
	}
	return top
}

// approved returns the approved epic n sits under, n itself when it is
// one; nil when there is none.
func (o *outline) approved(n *node) *node {
	if e := o.epicOf(n); e != nil && e.Run != nil {
		return e
	}
	return nil
}

// runOwned refuses a manual start or a per-card confirmation of n under an
// approved epic: the approval owns that work.
func (o *outline) runOwned(n *node) error {
	e := o.approved(n)
	if e == nil {
		return nil
	}
	if e == n {
		return refuse(CodeRunOwned, "%s is an approved epic: nothing under it is started or confirmed card by card; approve it again from the epic", n.ref())
	}
	return refuse(CodeRunOwned, "%s is under approved epic %s: it is not started or confirmed card by card; approve it from the epic", n.ref(), e.ref())
}

// crossesRun reports whether moving n under parent ("" for the root)
// changes its epic while either epic is approved: the card would run, or
// stop running, under settings the owner did not approve for it.
func (o *outline) crossesRun(n *node, parent string) bool {
	from := o.epicOf(n)
	var to *node
	if p := o.byID[parent]; p != nil {
		to = o.epicOf(p)
	}
	return from != to && ((from != nil && from.Run != nil) || (to != nil && to.Run != nil))
}

// pausedAt returns the nearest paused card at or above n, n itself first;
// nil when nothing on its path is paused.
func (o *outline) pausedAt(n *node) *node {
	for m := n; m != nil; m = o.byID[m.ParentID] {
		if m.Paused != "" {
			return m
		}
	}
	return nil
}

// leavesPause reports whether moving n under parent ("" for the root) takes
// it out from under a pause: a paused card is at or above it now, and none
// would be after the move, its own pause included.
func (o *outline) leavesPause(n *node, parent string) bool {
	if n.Paused != "" || o.pausedAt(n) == nil {
		return false
	}
	p := o.byID[parent]
	return p == nil || o.pausedAt(p) == nil
}

// staffed is the number of live confirmed subtasks under the container n.
func staffed(n *node) int {
	count := 0
	for _, l := range n.leaves {
		if l.Confirmed() && l.stored != StatusCancelled {
			count++
		}
	}
	return count
}

// staffedApproved lists the live confirmed containers under approved epics
// that have confirmed work, which an agent's write must not empty (ADR 0006
// §6.3 rule 3). Other writes are not checked.
func staffedApproved(a Actor, o *outline) map[string]bool {
	if a.Role != RoleAgent {
		return nil
	}
	out := map[string]bool{}
	for _, n := range o.order {
		if n.container() && n.Confirmed() && n.stored != StatusCancelled && o.approved(n) != nil && staffed(n) > 0 {
			out[n.ID] = true
		}
	}
	return out
}

// noneEmptied refuses the write, naming them, when a container in before is
// still live and has no confirmed subtask left: under an approved epic it
// would never finish and would hold back what waits on it.
func (t *txn) noneEmptied(project string, before map[string]bool) error {
	if len(before) == 0 {
		return nil
	}
	o, err := t.outline(project)
	if err != nil {
		return err
	}
	var refs []string
	for _, n := range o.order {
		if before[n.ID] && n.stored != StatusCancelled && o.underCancelled(n, nil) == nil && staffed(n) == 0 {
			refs = append(refs, n.ref())
		}
	}
	if len(refs) == 0 {
		return nil
	}
	return &Error{Code: CodeInvalid, Refs: refs, Message: fmt.Sprintf(
		"This would leave %s with no confirmed subtask, and approved work with none never finishes: keep one there, or ask the owner", strings.Join(refs, ", "))}
}
