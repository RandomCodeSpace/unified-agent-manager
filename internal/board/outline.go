package board

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// cardCols is the select list scanCard decodes.
const cardCols = `id, seq, project_id, kind, parent_id, rank, title, "desc", win_condition, status, prio, due, effort,
	labels, checklist, blocked, expires_at, held_by, pinned_sha, accept_cmd, paths, cascade_id, created_by, revision,
	created_at, updated_at, moved_at`

// node is a card as the rules see it: the Card, with its stored status next
// to the shown one, its depth, and for a container the subtasks under it.
type node struct {
	Card
	stored Status
	depth  int
	leaves []*node
}

// outline is one Project's cards, loaded in one query and derived in memory.
// Boards are small, so every rule reads the whole Project.
type outline struct {
	project string
	byID    map[string]*node
	kids    map[string][]*node // by parent ID, in rank order
	order   []*node            // depth-first, in rank order
}

func scanCard(row interface{ Scan(...any) error }) (*node, error) {
	var n node
	var kind, status, labels, checklist, paths, created, updated, moved string
	var blocked int
	var expires, accept sql.NullString
	if err := row.Scan(&n.ID, &n.Seq, &n.ProjectID, &kind, &n.ParentID, &n.Rank, &n.Title, &n.Desc, &n.WinCondition,
		&status, &n.Prio, &n.Due, &n.Effort, &labels, &checklist, &blocked, &expires, &n.HeldBy, &n.PinnedSHA, &accept,
		&paths, &n.CascadeID, &n.CreatedBy, &n.Revision, &created, &updated, &moved); err != nil {
		return nil, fmt.Errorf("board: scan card: %w", err)
	}
	n.Kind, n.stored, n.Status, n.Blocked = Kind(kind), Status(status), Status(status), blocked != 0
	for _, f := range []struct {
		raw string
		to  any
	}{{labels, &n.Labels}, {checklist, &n.Checklist}, {paths, &n.Paths}} {
		if err := json.Unmarshal([]byte(f.raw), f.to); err != nil {
			return nil, fmt.Errorf("board: card %s: %w", n.ID, err)
		}
	}
	if accept.Valid {
		n.AcceptCmd = &accept.String
	}
	if expires.Valid {
		at, err := parseStamp(expires.String)
		if err != nil {
			return nil, fmt.Errorf("board: card %s expires_at: %w", n.ID, err)
		}
		n.ExpiresAt = &at
	}
	if err := parseStamps([]string{created, updated, moved}, &n.CreatedAt, &n.UpdatedAt, &n.MovedAt); err != nil {
		return nil, fmt.Errorf("board: card %s: %w", n.ID, err)
	}
	return &n, nil
}

// parseStamps parses each stored timestamp into the matching destination.
func parseStamps(raw []string, dst ...*time.Time) error {
	for i, v := range raw {
		at, err := parseStamp(v)
		if err != nil {
			return err
		}
		*dst[i] = at
	}
	return nil
}

// load reads project's cards, their pending request counts and links, and
// derives every container.
func (t *txn) load(project string) (*outline, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT `+cardCols+` FROM cards WHERE project_id = ? ORDER BY rank, seq`, project)
	if err != nil {
		return nil, fmt.Errorf("board: load cards: %w", err)
	}
	o := &outline{project: project, byID: map[string]*node{}, kids: map[string][]*node{}}
	var all []*node
	for rows.Next() {
		n, err := scanCard(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		o.byID[n.ID] = n
		all = append(all, n)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("board: load cards: %w", err)
	}
	if err := t.loadCounts(o); err != nil {
		return nil, err
	}
	for _, n := range all {
		parent := n.ParentID
		if o.byID[parent] == nil {
			parent = ""
		}
		o.kids[parent] = append(o.kids[parent], n)
	}
	o.walk("", 0)
	return o, nil
}

// loadCounts fills pending request counts and blocker links.
func (t *txn) loadCounts(o *outline) error {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT r.card_id, COUNT(*) FROM requests r JOIN cards c ON c.id = r.card_id
		WHERE c.project_id = ? AND r.status = 'pending' GROUP BY r.card_id`, o.project)
	if err != nil {
		return fmt.Errorf("board: load requests: %w", err)
	}
	for rows.Next() {
		var id string
		var count int
		if err := rows.Scan(&id, &count); err != nil {
			_ = rows.Close()
			return fmt.Errorf("board: load requests: %w", err)
		}
		o.byID[id].PendingRequests = count
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("board: load requests: %w", err)
	}
	rows, err = t.tx.QueryContext(t.ctx, `SELECT blocker_id, blocked_id FROM links
		WHERE blocked_id IN (SELECT id FROM cards WHERE project_id = ?1)
		   OR blocker_id IN (SELECT id FROM cards WHERE project_id = ?1)
		ORDER BY blocker_id, blocked_id`, o.project)
	if err != nil {
		return fmt.Errorf("board: load links: %w", err)
	}
	for rows.Next() {
		var blocker, blocked string
		if err := rows.Scan(&blocker, &blocked); err != nil {
			_ = rows.Close()
			return fmt.Errorf("board: load links: %w", err)
		}
		if n := o.byID[blocked]; n != nil {
			n.BlockedBy = append(n.BlockedBy, blocker)
		}
		if n := o.byID[blocker]; n != nil {
			n.Blocks = append(n.Blocks, blocked)
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("board: load links: %w", err)
	}
	return nil
}

// walk orders the subtree under parent depth-first and derives each
// container after its children.
func (o *outline) walk(parent string, depth int) []*node {
	var leaves []*node
	for _, n := range o.kids[parent] {
		n.depth = depth
		o.order = append(o.order, n)
		if !n.container() {
			leaves = append(leaves, n)
			continue
		}
		n.leaves = o.walk(n.ID, depth+1)
		facts := make([]leafFact, len(n.leaves))
		for i, l := range n.leaves {
			facts[i] = leafFact{status: l.stored, confirmed: l.Confirmed(), held: l.HeldBy != "", pending: l.PendingRequests > 0}
		}
		status, progress := derive(n.stored == StatusCancelled, facts)
		n.Status, n.Progress = status, &progress
		leaves = append(leaves, n.leaves...)
	}
	return leaves
}

// leafFact is what derivation needs to know about one subtask.
type leafFact struct {
	status    Status
	confirmed bool
	held      bool
	pending   bool
}

// derive is ADR 0005 §2's table for a container, over the subtasks in its
// subtree. A container cancelled itself (dismissed, swept or cascaded) stays
// cancelled. Otherwise, in order:
//
//	any subtask held                              doing
//	no confirmed subtasks                         planned (never done)
//	every confirmed subtask cancelled             cancelled
//	every non-cancelled confirmed subtask done    done, or doing while any subtask has a pending request
//	any confirmed subtask done                    doing
//	otherwise                                     planned
//
// Progress counts confirmed subtasks only; unconfirmed live ones are
// Proposed.
func derive(cancelled bool, leaves []leafFact) (Status, Progress) {
	var p Progress
	held, pending := false, false
	confirmed, dropped := 0, 0
	for _, l := range leaves {
		held = held || l.held
		pending = pending || l.pending
		if !l.confirmed {
			if l.status != StatusCancelled {
				p.Proposed++
			}
			continue
		}
		confirmed++
		switch l.status {
		case StatusCancelled:
			dropped++
		case StatusDone:
			p.Done++
		}
	}
	p.Total = confirmed - dropped
	switch {
	case cancelled:
		return StatusCancelled, p
	case held:
		return StatusDoing, p
	case confirmed == 0:
		return StatusPlanned, p
	case p.Total == 0:
		return StatusCancelled, p
	case p.Done == p.Total && pending:
		return StatusDoing, p
	case p.Done == p.Total:
		return StatusDone, p
	case p.Done > 0:
		return StatusDoing, p
	}
	return StatusPlanned, p
}

// outline returns project's outline, loading it when a write has dropped it.
func (t *txn) outline(project string) (*outline, error) {
	if o := t.outlines[project]; o != nil {
		return o, nil
	}
	o, err := t.load(project)
	if err != nil {
		return nil, err
	}
	t.outlines[project] = o
	return o, nil
}

// locate resolves ref, a card UUID or its #seq with or without the '#', to
// its card's Project and ID.
func (t *txn) locate(ref string) (project, id string, err error) {
	ref = strings.TrimSpace(ref)
	row := t.tx.QueryRowContext(t.ctx, `SELECT project_id, id FROM cards WHERE id = ?`, ref)
	if seq, ok := parseSeqRef(ref); ok {
		row = t.tx.QueryRowContext(t.ctx, `SELECT project_id, id FROM cards WHERE seq = ?`, seq)
	}
	switch err := row.Scan(&project, &id); {
	case errors.Is(err, sql.ErrNoRows):
		return "", "", refuse(CodeNotFound, "card %s not found", ref)
	case err != nil:
		return "", "", fmt.Errorf("board: resolve card: %w", err)
	}
	return project, id, nil
}

// parseSeqRef reports whether ref names a card by number: digits only,
// optionally after one '#'.
func parseSeqRef(ref string) (int64, bool) {
	digits := strings.TrimPrefix(ref, "#")
	if digits == "" || strings.Trim(digits, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.ParseInt(digits, 10, 64)
	return n, err == nil && n > 0
}

// find resolves ref and returns its card from the Project's outline.
func (t *txn) find(ref string) (*outline, *node, error) {
	project, id, err := t.locate(ref)
	if err != nil {
		return nil, nil, err
	}
	return t.cardIn(project, id)
}

func (t *txn) cardIn(project, id string) (*outline, *node, error) {
	o, err := t.outline(project)
	if err != nil {
		return nil, nil, err
	}
	n := o.byID[id]
	if n == nil {
		return nil, nil, refuse(CodeNotFound, "card %s not found", id)
	}
	return o, n, nil
}

// subtree lists n and everything under it, depth-first.
func (o *outline) subtree(n *node) []*node {
	out := []*node{n}
	for _, kid := range o.kids[n.ID] {
		out = append(out, o.subtree(kid)...)
	}
	return out
}

// statuses snapshots every container's derived status.
func (o *outline) statuses() map[string]Status {
	out := map[string]Status{}
	for _, n := range o.order {
		if n.container() {
			out[n.ID] = n.Status
		}
	}
	return out
}

// confirmedChain reports whether every ancestor of a card placed under
// parentID is confirmed: a confirmed card never sits under an unconfirmed one.
func (o *outline) confirmedChain(parentID string) error {
	for n := o.byID[parentID]; n != nil; n = o.byID[n.ParentID] {
		if !n.Confirmed() {
			return refuse(CodeUnconfirmedParent, "%s is unconfirmed; confirm it first", n.ref())
		}
	}
	return nil
}

// duplicate returns a live sibling under parentID whose normalised title
// matches title, ignoring except and cancelled siblings.
func (o *outline) duplicate(parentID, title, except string) error {
	want := normalTitle(title)
	for _, sib := range o.kids[parentID] {
		if sib.ID != except && sib.stored != StatusCancelled && normalTitle(sib.Title) == want {
			return refuse(CodeDuplicate, "%s already has the title %q", sib.ref(), sib.Title)
		}
	}
	return nil
}

// statusOf returns a card's shown status, loading its Project when it is not
// the one given.
func (t *txn) statusOf(o *outline, id string) (*node, error) {
	if n := o.byID[id]; n != nil {
		return n, nil
	}
	var project string
	err := t.tx.QueryRowContext(t.ctx, `SELECT project_id FROM cards WHERE id = ?`, id).Scan(&project)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("board: resolve blocker: %w", err)
	}
	_, n, err := t.cardIn(project, id)
	return n, err
}

// openBlockers lists the cards blocking n that are neither done nor
// cancelled.
func (t *txn) openBlockers(o *outline, n *node) ([]*node, error) {
	var open []*node
	for _, id := range n.BlockedBy {
		b, err := t.statusOf(o, id)
		if err != nil {
			return nil, err
		}
		if b != nil && !b.Status.terminal() {
			open = append(open, b)
		}
	}
	return open, nil
}

// guard is the finishing guard: open checklist items, the blocked flag or
// open blockers refuse finishing a subtask, each with its list.
func (t *txn) guard(o *outline, n *node) error {
	var items []string
	for _, c := range n.Checklist {
		if !c.Done {
			items = append(items, c.Text)
		}
	}
	if len(items) > 0 {
		return &Error{Code: CodeGuardOpenItems, Refs: items,
			Message: fmt.Sprintf("%d of %d checklist items are still open on %s", len(items), len(n.Checklist), n.ref())}
	}
	if n.Blocked {
		return refuse(CodeGuardBlocked, "%s is flagged blocked", n.ref())
	}
	open, err := t.openBlockers(o, n)
	if err != nil {
		return err
	}
	if len(open) > 0 {
		refs := make([]string, len(open))
		for i, b := range open {
			refs[i] = b.ref()
		}
		return &Error{Code: CodeGuardBlockers, Refs: refs,
			Message: fmt.Sprintf("%s still blocks %s", strings.Join(refs, ", "), n.ref())}
	}
	return nil
}

// scope is where a Task may write (ADR 0005 §4): the container it plans
// under or was launched from, "" when it was launched on a root subtask.
type scope struct {
	project string
	cardID  string
	working bool
}

func (t *txn) scope(taskID string) (*scope, error) {
	if sc, ok := t.scopes[taskID]; ok {
		return sc, nil
	}
	var sc scope
	var kind string
	err := t.tx.QueryRowContext(t.ctx, `SELECT project_id, card_id, kind FROM scopes WHERE task_id = ?`, taskID).
		Scan(&sc.project, &sc.cardID, &kind)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		t.scopes[taskID] = nil
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("board: read scope: %w", err)
	}
	sc.working = kind == "working"
	t.scopes[taskID] = &sc
	return &sc, nil
}

func (t *txn) setScope(taskID, project, cardID string, working bool) error {
	kind := "planning"
	if working {
		kind = "working"
	}
	t.scopes[taskID] = &scope{project: project, cardID: cardID, working: working}
	return t.exec(`INSERT INTO scopes (task_id, project_id, card_id, kind, created_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(task_id) DO UPDATE SET project_id = excluded.project_id, card_id = excluded.card_id, kind = excluded.kind`,
		taskID, project, cardID, kind, stamp(t.now))
}

// inScope refuses an agent's write to n unless n is the Task's scope
// container, under it, or the subtask the Task holds. The owner has no scope.
func (t *txn) inScope(o *outline, a Actor, n *node) error {
	if a.owner() {
		return nil
	}
	if n.HeldBy == a.TaskID {
		return nil
	}
	sc, err := t.scope(a.TaskID)
	if err != nil {
		return err
	}
	if sc != nil && sc.project == o.project && sc.cardID != "" {
		for m := n; m != nil; m = o.byID[m.ParentID] {
			if m.ID == sc.cardID {
				return nil
			}
		}
	}
	return refuse(CodeForbidden, "%s is outside the Task's scope", n.ref())
}

// mutate runs fn as one write to project: it sweeps expired cards first when
// sweep is set, and afterwards settles every container fn brought to done.
func (t *txn) mutate(project string, sweep bool, fn func() error) error {
	if sweep {
		if _, err := t.sweep(project); err != nil {
			return err
		}
	}
	o, err := t.outline(project)
	if err != nil {
		return err
	}
	before := o.statuses()
	if err := fn(); err != nil {
		return err
	}
	return t.settle(project, before)
}

// settle closes every container that reached done since before, deepest
// first: its unconfirmed, unheld subtasks are cancelled with an automatic
// comment, and a roll-up of its children's close comments is added.
func (t *txn) settle(project string, before map[string]Status) error {
	o, err := t.outline(project)
	if err != nil {
		return err
	}
	var closing []*node
	for _, n := range o.order {
		if n.container() && n.Status == StatusDone && before[n.ID] != StatusDone {
			closing = append(closing, n)
		}
	}
	slices.SortStableFunc(closing, func(a, b *node) int { return b.depth - a.depth })
	closed := map[string]bool{}
	for _, c := range closing {
		cascade := t.s.newID()
		for _, leaf := range c.leaves {
			if closed[leaf.ID] || leaf.Confirmed() || leaf.HeldBy != "" || leaf.stored.terminal() {
				continue
			}
			closed[leaf.ID] = true
			if err := t.setStatus(leaf, StatusCancelled, "", cascade); err != nil {
				return err
			}
			if _, err := t.addComment(leaf, AuthorUAM, "", "closed unconfirmed with "+c.ref(), true, false); err != nil {
				return err
			}
		}
		if err := t.rollUp(o, c); err != nil {
			return err
		}
	}
	return nil
}

// sweep cancels project's expired unconfirmed cards, each with its subtree
// under one cascade and an automatic comment. It skips a card that is held,
// has a pending request, or has a held, confirmed or requested card under it.
func (t *txn) sweep(project string) (int, error) {
	o, err := t.outline(project)
	if err != nil {
		return 0, err
	}
	swept := map[string]bool{}
	count := 0
	for _, n := range o.order {
		if swept[n.ID] || n.ExpiresAt == nil || t.now.Before(*n.ExpiresAt) || n.stored == StatusCancelled || n.stored == StatusDone {
			continue
		}
		tree := o.subtree(n)
		if slices.ContainsFunc(tree, func(m *node) bool {
			return m.HeldBy != "" || m.PendingRequests > 0 || m.Confirmed()
		}) {
			continue
		}
		cascade := t.s.newID()
		for _, m := range tree {
			swept[m.ID] = true
			if m.stored.terminal() {
				continue
			}
			if err := t.setStatus(m, StatusCancelled, "", cascade); err != nil {
				return 0, err
			}
			if _, err := t.addComment(m, AuthorUAM, "", "expired unconfirmed", true, false); err != nil {
				return 0, err
			}
			count++
		}
	}
	return count, nil
}
