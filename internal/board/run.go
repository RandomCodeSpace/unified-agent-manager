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

// MaxLanes is the most lane attempts that take a slot at a time, over every
// Project (ADR 0006 §4.4).
const MaxLanes = 4

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
// touch confirms. The epic's run is written, or updated when it was
// approved before, and its own pause is cleared. base, trimmed, becomes the
// Project's base branch when it has none; "" leaves it as it is. It
// refuses, writing nothing, unless the epic is live and not done; every
// item is a live card under it; every card the dialog shows (shown) is
// listed at its current revision (stale otherwise); no subtask under it is
// held but by a lane; every live story and the epic keep a live subtask
// that is confirmed or listed; and every such subtask not started resolves
// to an acceptance command.
func (s *Store) Approve(ctx context.Context, a Actor, ref string, settings RunSettings, items []ApproveItem, base string) (Card, error) {
	if err := permit(a, opApprove, ""); err != nil {
		return Card{}, err
	}
	if err := settings.check(); err != nil {
		return Card{}, err
	}
	base = strings.TrimSpace(base)
	if err := checkLine("base branch", base); err != nil {
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
		if err := t.exec(`INSERT INTO runs (epic_id, provider, model, effort, context_size, mode, parallel, approved_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(epic_id) DO UPDATE SET provider = excluded.provider, model = excluded.model, effort = excluded.effort,
			context_size = excluded.context_size, mode = excluded.mode, parallel = excluded.parallel, approved_at = excluded.approved_at`,
			n.ID, settings.Provider, settings.Model, settings.Effort, settings.ContextSize, settings.Mode, settings.Parallel, stamp(t.now)); err != nil || base == "" {
			return err
		}
		return t.exec(`INSERT INTO project_settings (project_id, base_ref) VALUES (?, ?)
			ON CONFLICT(project_id) DO UPDATE SET base_ref = excluded.base_ref WHERE project_settings.base_ref = ''`, o.project, base)
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
	// An item that is not a live card under the epic is the caller's
	// mistake, not a change to the plan, so it names no cards to show again.
	if len(stale) == 0 && len(outside) > 0 {
		return nil, invalid("%s %s not a live card under %s", strings.Join(outside, ", "), isAre(len(outside)), n.ref())
	}
	// A card the dialog shows but did not list was added or moved in
	// since it opened.
	for _, m := range o.shown(n) {
		if !listed[m.ID] {
			stale = append(stale, m.ref())
		}
	}
	if len(stale) > 0 {
		return nil, &Error{Code: CodeStale, Refs: stale, Message: fmt.Sprintf(
			"%s changed since the approval was shown; look again and approve what is there now", strings.Join(stale, ", "))}
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
		if m.HeldBy != "" && m.Lane == nil {
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

// shown lists what the Approve dialog shows of the epic n: n and every card
// under it whose status, and every ancestor's, is not cancelled, depth
// first. A container all of whose confirmed subtasks were cancelled shows as
// cancelled and is left out with its subtree, where only proposals are live.
func (o *outline) shown(n *node) []*node {
	out := []*node{n}
	for _, kid := range o.kids[n.ID] {
		if kid.Status != StatusCancelled {
			out = append(out, o.shown(kid)...)
		}
	}
	return out
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

// StartRun starts a lane attempt at the subtask ref under an approved epic
// for the new Task taskID (ADR 0006 §4.4, §5.3): it refuses with
// CodeNotReady unless the subtask is ready and a slot is free under both
// the epic's parallel limit and MaxLanes, scopes the Task to the subtask
// alone, records the done subtasks it waited on, and starts the hold on
// lane's branch from base, the integration branch's tip. The subtask is
// confirmed already, so nothing is confirmed or pinned. A ref outside
// approved epics, or a container, is refused as invalid.
func (s *Store) StartRun(ctx context.Context, a Actor, ref, taskID string, base Baseline, lane Lane) (Card, error) {
	if err := permit(a, opLaunch, ""); err != nil {
		return Card{}, err
	}
	if strings.TrimSpace(taskID) == "" {
		return Card{}, invalid("a run start needs a Task")
	}
	if strings.TrimSpace(lane.Branch) == "" || lane.LandedSHA != "" || lane.RevertedSHA != "" {
		return Card{}, invalid("a run start needs its attempt branch, and nothing landed yet")
	}
	if err := checkLine("branch", lane.Branch); err != nil {
		return Card{}, err
	}
	return s.ownerWrite(ctx, a, ref, func(t *txn, o *outline, n *node) error {
		e, err := t.startable(o, n)
		if err != nil {
			return err
		}
		if err := t.setScope(taskID, o.project, n.ID, true); err != nil {
			return err
		}
		if err := t.startHold(o, n, taskID, base, Lane{Branch: lane.Branch}, o.waitedOn(n)); err != nil {
			return err
		}
		_, err = t.addComment(n, AuthorUAM, "", "started by the run of "+e.ref(), true, false)
		return err
	})
}

// CanStart refuses, without writing, a run start of the subtask ref that
// StartRun would refuse, so a lane start checks before it makes a worktree
// and a Task. StartRun checks again in its own write.
func (s *Store) CanStart(ctx context.Context, ref string) error {
	return s.read(ctx, func(t *txn) error {
		o, n, err := t.find(ref)
		if err != nil {
			return err
		}
		if o.project == "" {
			return errReadOnly
		}
		_, err = t.startable(o, n)
		return err
	})
}

// AbortRun ends taskID's lane attempt at the subtask ref, which never
// started its work: the start failed after the hold was written (ADR 0006
// §4.5). The subtask returns to todo with no pause, and detail, when given,
// is said in an automatic comment, cut to one line's length.
func (s *Store) AbortRun(ctx context.Context, ref, taskID, detail string) (Card, error) {
	body := strings.TrimSpace(detail)
	if len(body) > maxLineBytes {
		body = strings.ToValidUTF8(body[:maxLineBytes], "") + "…"
	}
	return s.ownerWrite(ctx, Actor{}, ref, func(t *txn, _ *outline, n *node) error {
		if n.container() || n.HeldBy != taskID || n.Lane == nil {
			return refuse(CodeNotHeld, "%s is not in a lane attempt of this Task", n.ref())
		}
		hold, err := t.openHold(n)
		if err != nil {
			return err
		}
		if err := t.releaseHold(n, ReleaseAborted, StatusTodo, ""); err != nil {
			return err
		}
		note := fmt.Sprintf("attempt #%d aborted", hold.Attempt)
		if body != "" {
			note += ": " + body
		}
		_, err = t.addComment(n, AuthorUAM, "", note, true, false)
		return err
	})
}

// startable refuses a run start of n, as StartRun does, and returns its
// approved epic: n must be a subtask under an approved epic, ready, with a
// slot free under the epic's parallel limit and under MaxLanes.
func (t *txn) startable(o *outline, n *node) (*node, error) {
	if n.container() {
		return nil, invalid("%s is a %s; only subtasks run", n.ref(), n.Kind)
	}
	e := o.approved(n)
	if e == nil {
		return nil, invalid("%s is not under an approved epic", n.ref())
	}
	if err := t.ready(o, n); err != nil {
		return nil, err
	}
	lanes, err := t.lanesInUse()
	if err != nil {
		return nil, err
	}
	var mine []string
	for _, l := range lanes {
		if m := o.byID[l.id]; m != nil && o.epicOf(m) == e {
			mine = append(mine, l.ref())
		}
	}
	if len(mine) >= e.Run.Parallel {
		return nil, &Error{Code: CodeNotReady, Refs: mine, Message: fmt.Sprintf(
			"%s runs %d at a time, and %s %s running", e.ref(), e.Run.Parallel, strings.Join(mine, ", "), isAre(len(mine)))}
	}
	if len(lanes) >= MaxLanes {
		refs := make([]string, len(lanes))
		for i, l := range lanes {
			refs[i] = l.ref()
		}
		return nil, &Error{Code: CodeNotReady, Refs: refs, Message: fmt.Sprintf(
			"At most %d subtasks run at a time, and %s %s running", MaxLanes, strings.Join(refs, ", "), isAre(len(refs)))}
	}
	return e, nil
}

// ready refuses with CodeNotReady, naming what holds it back, a run start
// of the subtask n (ADR 0006 §4.1): it must be planned or todo, unheld,
// with no pending request, confirmed with its ancestors, under no
// cancelled or paused card, not flagged blocked, and waiting on nothing
// open, its own blockers and its ancestors' included.
func (t *txn) ready(o *outline, n *node) error {
	notReady := func(refs []*node, format string, args ...any) error {
		e := refuse(CodeNotReady, format, args...)
		for _, m := range refs {
			e.Refs = append(e.Refs, m.ref())
		}
		return e
	}
	switch {
	case n.HeldBy != "" || n.stored == StatusDoing:
		return notReady([]*node{n}, "%s is in progress", n.ref())
	case n.stored == StatusDone || n.stored == StatusCancelled:
		return notReady([]*node{n}, "%s is %s", n.ref(), n.stored)
	case n.PendingRequests > 0:
		return notReady([]*node{n}, "%s has a request waiting for the owner", n.ref())
	}
	if un := o.unconfirmed(n); len(un) > 0 {
		return notReady(un, "%s %s not approved; approve the epic again to run it", refList(un), isAre(len(un)))
	}
	for p := o.byID[n.ParentID]; p != nil; p = o.byID[p.ParentID] {
		if p.stored == StatusCancelled {
			return notReady([]*node{p}, "%s is under cancelled %s", n.ref(), p.ref())
		}
	}
	if p := o.pausedAt(n); p != nil {
		return notReady([]*node{p}, "%s is paused", p.ref())
	}
	if n.Blocked {
		return notReady([]*node{n}, "%s is flagged blocked", n.ref())
	}
	waits, err := t.waitsOn(o, n)
	if err != nil {
		return err
	}
	if len(waits) > 0 {
		blockers := make([]*node, len(waits))
		named := make([]string, len(waits))
		for i, w := range waits {
			blockers[i], named[i] = w.blocker, w.named()
		}
		return notReady(blockers, "%s waits on %s", n.ref(), strings.Join(named, ", "))
	}
	return nil
}

func refList(nodes []*node) string {
	refs := make([]string, len(nodes))
	for i, m := range nodes {
		refs[i] = m.ref()
	}
	return strings.Join(refs, ", ")
}

// laneSlot is a subtask whose lane attempt takes a slot.
type laneSlot struct {
	id  string
	seq int64
}

func (l laneSlot) ref() string { return fmt.Sprintf("#%d", l.seq) }

// lanesQuery lists, over every Project in #seq order, the subtasks whose
// open lane attempt takes a slot: one with a pending done, blocked or split
// request from its Task frees it, as for the one-hold cap (openHoldsQuery).
const lanesQuery = `SELECT c.id, c.seq FROM holds h JOIN cards c ON c.id = h.card_id
	WHERE h.ended_at = '' AND h.branch <> '' AND NOT EXISTS (
	SELECT 1 FROM requests r WHERE r.card_id = h.card_id AND r.task_id = h.task_id AND r.status = 'pending'
	AND r.kind IN ('done', 'blocked', 'split'))
	ORDER BY c.seq`

// lanesInUse is the one count of lane slots in use (ADR 0006 §4.1), read
// from the stored holds alone, whatever their Tasks' stages.
func (t *txn) lanesInUse() ([]laneSlot, error) {
	rows, err := t.tx.QueryContext(t.ctx, lanesQuery)
	if err != nil {
		return nil, fmt.Errorf("board: count lanes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []laneSlot
	for rows.Next() {
		var l laneSlot
		if err := rows.Scan(&l.id, &l.seq); err != nil {
			return nil, fmt.Errorf("board: count lanes: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("board: count lanes: %w", err)
	}
	return out, nil
}

// waitedOn lists, in outline order, the done subtasks at or under a blocker
// of n or of one of its ancestors: what a lane attempt at n starts on top
// of (ADR 0006 §5.3).
func (o *outline) waitedOn(n *node) []string {
	done := map[string]bool{}
	for m := n; m != nil; m = o.byID[m.ParentID] {
		for _, id := range m.BlockedBy {
			b := o.byID[id]
			if b == nil {
				continue
			}
			for _, x := range o.subtree(b) {
				if !x.container() && x.stored == StatusDone {
					done[x.ID] = true
				}
			}
		}
	}
	out := []string{}
	for _, x := range o.order {
		if done[x.ID] {
			out = append(out, x.ID)
		}
	}
	return out
}
