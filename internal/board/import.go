// Reads boards written by kb (github.com/RandomCodeSpace/kb, MIT) at its schema v11.

package board

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// The source board: its database file, the one schema version Import reads,
// and the board user whose tasks it imports.
const (
	sourceFile   = "kb.db"
	sourceSchema = "11"
	sourceUser   = "default"
)

// The source tags that are not labels: a task's project, and a link to a
// forge issue.
const (
	projectTag = "project::"
	linkTag    = "link::"
)

// CodeImportSchema refuses a source board at a schema version Import does not
// read.
const CodeImportSchema Code = "import_schema"

var errImportSchema = refuse(CodeImportSchema, "This board needs kb v1.13.0 or newer: open it once with kb, then import again.")

// importStatus maps a source status to the imported subtask's. There is no
// hold on import, so a task in progress becomes todo.
var importStatus = map[string]Status{
	"todo":      StatusTodo,
	"doing":     StatusTodo,
	"done":      StatusDone,
	"cancelled": StatusCancelled,
}

// ImportReport is what one Import did. Imported counts the cards it added,
// Unassigned how many of those went to the Unassigned list, and Updated the
// existing cards it changed. Comments and Links count the comments and
// blocker links it copied. Skipped lists the source tasks, by their ID, that
// it left alone, and why.
type ImportReport struct {
	Imported   int          `json:"imported"`
	Updated    int          `json:"updated"`
	Unassigned int          `json:"unassigned"`
	Comments   int          `json:"comments"`
	Links      int          `json:"links"`
	Skipped    []ImportSkip `json:"skipped"`
}

// ImportSkip is one source task an import left alone.
type ImportSkip struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

func (r *ImportReport) skip(st *sourceTask, reason string) {
	r.Skipped = append(r.Skipped, ImportSkip{ID: st.ID, Reason: fmt.Sprintf("%q: %s", st.Title, reason)})
}

// Import copies the board of the external kb app kept in the directory src,
// its kb.db at schema v11, into the store in one transaction. projects maps a
// source project name to a Project ID.
//
// The source is never opened in place: its database, with any -wal and -shm
// files, is copied into a fresh owner-only temporary directory, read there
// read-only, and deleted. Any other schema version is refused with
// CodeImportSchema.
//
// Each task of the source's default user becomes a confirmed subtask at the
// root, with no pin, in the Project its project:: tag maps to, or in
// Unassigned. Title, description, priority, due date, effort, checklist and
// blocked flag carry over, and every tag but project:: and link:: becomes a
// label. todo and doing become todo, done done and cancelled cancelled; a
// task in progress gets the automatic comment "was in progress in kb", and a
// done or cancelled one the automatic close comment "imported from kb".
// Comments and cancel reasons are copied as uam's automatic comments, naming
// their author, and blocker links where both cards are in one Project.
//
// Cards are keyed on the task's UUID, so importing again adds nothing twice.
// A second import applies only what changed at the source since the last
// one, so the owner's edits stand until the source changes the same field;
// it moves a card still in Unassigned into a Project that now maps, and
// copies new comments. A change to a held card, to a card cancelled here, or
// a status the owner could not set directly is skipped and retried by the
// next import. A purged card is never brought back.
func (s *Store) Import(ctx context.Context, src string, projects map[string]string) (ImportReport, error) {
	tasks, err := readSource(ctx, src)
	if err != nil {
		return ImportReport{}, err
	}
	var report ImportReport
	_, err = s.write(ctx, func(t *txn) error {
		report = ImportReport{Skipped: []ImportSkip{}}
		return t.importBoard(tasks, projects, &report)
	})
	if err != nil {
		return ImportReport{}, err
	}
	return report, nil
}

// sourceTask is one source task with its comments, its cancel reason and the
// IDs of the tasks blocking it.
type sourceTask struct {
	ID, Emoji, Title, Desc, Status string
	Blocked                        bool
	Prio                           int
	Due, Effort, Tags, Checks      string
	CreatedAt, MovedAt             string
	Reason                         string
	Comments                       []sourceComment
	Blockers                       []string
}

type sourceComment struct {
	ID                      int64
	Author, Body, CreatedAt string
}

// readSource copies the source database in dir into a fresh temporary
// directory, reads the copy and deletes it.
func readSource(ctx context.Context, dir string) ([]*sourceTask, error) {
	if !filepath.IsAbs(dir) {
		return nil, invalid("the import source must be an absolute directory path")
	}
	tmp, err := os.MkdirTemp("", "uam-import-")
	if err != nil {
		return nil, fmt.Errorf("board: import: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	copied := filepath.Join(tmp, sourceFile)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		err := copyFile(filepath.Join(dir, sourceFile+suffix), copied+suffix)
		switch {
		case errors.Is(err, os.ErrNotExist) && suffix == "":
			return nil, refuse(CodeNotFound, "%s holds no %s", dir, sourceFile)
		case errors.Is(err, os.ErrNotExist):
		case err != nil:
			return nil, err
		}
	}
	return readCopy(ctx, copied)
}

// copyFile copies the regular file from into the new owner-only file to.
func copyFile(from, to string) error {
	info, err := os.Stat(from)
	if err != nil {
		return fmt.Errorf("board: import: %w", err)
	}
	if !info.Mode().IsRegular() {
		return invalid("%s is not a regular file", from)
	}
	in, err := os.Open(from) // #nosec G304 -- the owner's chosen import source, opened only to read it.
	if err != nil {
		return fmt.Errorf("board: import: %w", err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- inside the import's own temporary directory.
	if err != nil {
		return fmt.Errorf("board: import: %w", err)
	}
	_, err = io.Copy(out, in)
	if err := errors.Join(err, out.Close()); err != nil {
		return fmt.Errorf("board: import: copy %s: %w", from, err)
	}
	return nil
}

// readCopy reads the default user's tasks, in creation order, from the
// copied database, opened read-only in one snapshot.
func readCopy(ctx context.Context, path string) ([]*sourceTask, error) {
	dsn, err := sqliteDSN(path, "query_only(1)", "temp_store(2)")
	if err != nil {
		return nil, fmt.Errorf("board: import path: %w", err)
	}
	db, err := sql.Open("sqlite", dsn+"&mode=ro")
	if err != nil {
		return nil, fmt.Errorf("board: import: %w", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	if err := checkIntegrity(db); err != nil {
		return nil, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("board: import: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var version string
	err = tx.QueryRowContext(ctx, `SELECT v FROM meta WHERE k = 'schema_version'`).Scan(&version)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil || version != sourceSchema {
		return nil, errImportSchema
	}
	var tasks []*sourceTask
	byID := map[string]*sourceTask{}
	err = eachRow(ctx, tx, `SELECT id, emoji, title, "desc", status, blocked, prio, due, effort, tags, checks, created_at, moved_at
		FROM tasks WHERE user = ? ORDER BY seq, id`, func(rows *sql.Rows) error {
		var st sourceTask
		if err := rows.Scan(&st.ID, &st.Emoji, &st.Title, &st.Desc, &st.Status, &st.Blocked, &st.Prio, &st.Due, &st.Effort,
			&st.Tags, &st.Checks, &st.CreatedAt, &st.MovedAt); err != nil {
			return err
		}
		tasks = append(tasks, &st)
		byID[st.ID] = &st
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = eachRow(ctx, tx, `SELECT task_id, reason FROM tombstones WHERE scope = ?`, func(rows *sql.Rows) error {
		var id, reason string
		if err := rows.Scan(&id, &reason); err != nil {
			return err
		}
		if st := byID[id]; st != nil {
			st.Reason = reason
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = eachRow(ctx, tx, `SELECT id, task_id, author, body, created_at FROM comments WHERE scope = ? ORDER BY id`, func(rows *sql.Rows) error {
		var c sourceComment
		var id string
		if err := rows.Scan(&c.ID, &id, &c.Author, &c.Body, &c.CreatedAt); err != nil {
			return err
		}
		if st := byID[id]; st != nil {
			st.Comments = append(st.Comments, c)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = eachRow(ctx, tx, `SELECT blocker_id, blocked_id FROM task_links WHERE scope = ? ORDER BY blocker_id, blocked_id`, func(rows *sql.Rows) error {
		var blocker, blocked string
		if err := rows.Scan(&blocker, &blocked); err != nil {
			return err
		}
		if st := byID[blocked]; st != nil && byID[blocker] != nil {
			st.Blockers = append(st.Blockers, blocker)
		}
		return nil
	})
	return tasks, err
}

// eachRow runs a source query for the default user and scans each row.
func eachRow(ctx context.Context, tx *sql.Tx, query string, scan func(*sql.Rows) error) error {
	rows, err := tx.QueryContext(ctx, query, sourceUser)
	if err != nil {
		return fmt.Errorf("board: import: read source: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return fmt.Errorf("board: import: read source: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("board: import: read source: %w", err)
	}
	return nil
}

// importState is what an import took from one source task. The next import
// compares the source with it, so it applies only what changed since.
type importState struct {
	Title     string   `json:"title"`
	Desc      string   `json:"desc"`
	Prio      int      `json:"prio"`
	Due       string   `json:"due"`
	Effort    string   `json:"effort"`
	Labels    []string `json:"labels"`
	Checklist []Check  `json:"checklist"`
	Blocked   bool     `json:"blocked"`
	// Status is the source status, before importStatus maps it.
	Status string `json:"status"`
	Reason string `json:"reason"`
	// Blockers are the source IDs of the tasks blocking this one.
	Blockers []string `json:"blockers"`
}

// mapTask maps st to the fields its card takes, and names the source project
// its project:: tag gives, "" for none.
func mapTask(st *sourceTask) (importState, string, error) {
	var tags []string
	if err := json.Unmarshal([]byte(st.Tags), &tags); err != nil {
		return importState{}, "", invalid("unreadable tags")
	}
	var checks []Check
	if err := json.Unmarshal([]byte(st.Checks), &checks); err != nil {
		return importState{}, "", invalid("unreadable checklist")
	}
	if _, ok := importStatus[st.Status]; !ok {
		return importState{}, "", invalid("unknown status %q", st.Status)
	}
	s := importState{
		Title: strings.TrimSpace(st.Title), Desc: st.Desc, Prio: st.Prio, Due: st.Due, Effort: st.Effort,
		Labels: []string{}, Checklist: orEmpty(checks), Blocked: st.Blocked, Status: st.Status, Reason: st.Reason,
		Blockers: orEmpty(st.Blockers),
	}
	if st.Emoji != "" {
		s.Title = st.Emoji + " " + s.Title
	}
	if s.Prio < PrioHigh || s.Prio > PrioLow {
		s.Prio = PrioDefault
	}
	if s.Effort == "" {
		s.Effort = DefaultEffort
	}
	project := ""
	for _, tag := range tags {
		switch {
		case strings.HasPrefix(tag, projectTag):
			if project == "" {
				project = strings.TrimPrefix(tag, projectTag)
			}
		case !strings.HasPrefix(tag, linkTag):
			s.Labels = append(s.Labels, tag)
		}
	}
	err := validateFields(Card{Title: s.Title, Desc: s.Desc, Prio: s.Prio, Due: s.Due, Effort: s.Effort, Labels: s.Labels, Checklist: s.Checklist})
	return s, project, err
}

// importEntry is one source task's card after the import: its Project,
// whether this import added or moved it, and the task's blockers as the last
// import recorded them and as they are now.
type importEntry struct {
	cardID, project string
	placed          bool
	before, after   []string
}

// importNote is an automatic comment an import adds.
type importNote struct {
	body  string
	close bool
}

func (t *txn) importBoard(tasks []*sourceTask, projects map[string]string, r *ImportReport) error {
	// A status the import changes may finish a story the owner has since
	// moved an imported card under, so every Project already holding
	// imported cards is settled afterwards, as mutate does.
	settled, err := t.ids(`SELECT DISTINCT c.project_id FROM import_refs i JOIN cards c ON c.id = i.card_id
		WHERE c.project_id <> '' ORDER BY c.project_id`)
	if err != nil {
		return err
	}
	before := map[string]map[string]Status{}
	for _, p := range settled {
		o, err := t.outline(p)
		if err != nil {
			return err
		}
		before[p] = o.statuses()
	}
	entries := map[string]*importEntry{}
	for _, st := range tasks {
		e, err := t.importTask(st, projects, r)
		if err != nil {
			return err
		}
		if e != nil {
			entries[st.ID] = e
		}
	}
	for _, st := range tasks {
		if e := entries[st.ID]; e != nil {
			if err := t.importLinks(st, e, entries, r); err != nil {
				return err
			}
		}
	}
	for _, p := range settled {
		if err := t.settle(p, before[p]); err != nil {
			return err
		}
	}
	return nil
}

// importTask adds or updates st's card, copies its new comments and records
// what it took. It returns nil for a task it skipped.
func (t *txn) importTask(st *sourceTask, projects map[string]string, r *ImportReport) (*importEntry, error) {
	next, name, err := mapTask(st)
	if err != nil {
		r.skip(st, err.Error())
		return nil, nil
	}
	target := ""
	if name != "" {
		target = projects[name]
	}
	var cardID, raw string
	var last int64
	err = t.tx.QueryRowContext(t.ctx, `SELECT card_id, state, last_comment FROM import_refs WHERE source_id = ?`, st.ID).
		Scan(&cardID, &raw, &last)
	var (
		n     *node
		e     *importEntry
		saved = next
		notes []importNote
	)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if n, err = t.importCard(st, next, target); err != nil {
			return nil, err
		}
		r.Imported++
		if target == "" {
			r.Unassigned++
		}
		e = &importEntry{cardID: n.ID, project: target, placed: true, after: next.Blockers}
		notes = importNotes(importState{}, next, n.stored.terminal())
	case err != nil:
		return nil, fmt.Errorf("board: read import ref: %w", err)
	default:
		var prev importState
		if err := json.Unmarshal([]byte(raw), &prev); err != nil {
			return nil, fmt.Errorf("board: import ref %s: %w", st.ID, err)
		}
		project, id, err := t.locate(cardID)
		if CodeOf(err) == CodeNotFound {
			r.skip(st, "its card was purged")
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if _, n, err = t.cardIn(project, id); err != nil {
			return nil, err
		}
		e = &importEntry{cardID: id, project: project, before: prev.Blockers, after: next.Blockers}
		if saved, notes, err = t.importUpdate(st, n, prev, next, target, e, r); err != nil {
			return nil, err
		}
	}
	if last, err = t.importComments(n, st, last, r); err != nil {
		return nil, err
	}
	for _, note := range notes {
		if _, err := t.addComment(n, AuthorUAM, "", note.body, true, note.close); err != nil {
			return nil, err
		}
	}
	if err := t.exec(`INSERT INTO import_refs (source_id, card_id, state, last_comment) VALUES (?, ?, ?, ?)
		ON CONFLICT(source_id) DO UPDATE SET state = excluded.state, last_comment = excluded.last_comment`,
		st.ID, e.cardID, encode(saved), last); err != nil {
		return nil, err
	}
	return e, nil
}

// importCard adds st's card at the root of target.
func (t *txn) importCard(st *sourceTask, next importState, target string) (*node, error) {
	o, err := t.outline(target)
	if err != nil {
		return nil, err
	}
	status := importStatus[next.Status]
	n := &node{stored: status, Card: Card{
		ProjectID: target, Kind: KindSubtask, Title: next.Title, Desc: next.Desc, Status: status,
		Prio: next.Prio, Due: next.Due, Effort: next.Effort, Labels: next.Labels, Checklist: next.Checklist,
		Blocked: next.Blocked, CreatedBy: AuthorUAM,
		CreatedAt: sourceTime(st.CreatedAt, t.now), UpdatedAt: t.now, MovedAt: sourceTime(st.MovedAt, t.now),
	}}
	if status == StatusCancelled {
		n.CascadeID = t.s.newID()
	}
	return n, t.insertCard(o, n)
}

// importUpdate applies what changed at the source between prev and next to
// the card n, and moves it out of Unassigned when target now maps. It
// returns the state to record and the automatic comments to add. A change
// the card can't take is skipped and left out of the recorded state, so the
// next import tries it again.
func (t *txn) importUpdate(st *sourceTask, n *node, prev, next importState, target string, e *importEntry, r *ImportReport) (importState, []importNote, error) {
	moved := n.ProjectID == "" && target != ""
	if moved {
		o, err := t.outline(target)
		if err != nil {
			return importState{}, nil, err
		}
		t.removed("", n.ID)
		n.ProjectID = target
		if err := t.place(o, n, "", nil); err != nil {
			return importState{}, nil, err
		}
		e.project, e.placed = target, true
	}
	c := n.Card
	fields := mergeFields(&c, prev, next)
	to := importStatus[next.Status]
	status := importStatus[prev.Status] != to && n.stored != to
	saved := next
	if reason := importConflict(n, fields, status, to); reason != "" {
		r.skip(st, reason)
		saved = prev
		saved.Blockers = next.Blockers
		fields, status = false, false
		next = prev
	}
	notes := importNotes(prev, next, status && to.terminal())
	if fields {
		labels := !slices.Equal(n.Labels, c.Labels)
		n.Card = c
		if labels {
			if err := t.upsertLabels(n.ProjectID, n.Labels); err != nil {
				return importState{}, nil, err
			}
		}
	}
	if moved || fields {
		if err := t.updateCard(n); err != nil {
			return importState{}, nil, err
		}
	}
	if status {
		cascade := ""
		if to == StatusCancelled {
			cascade = t.s.newID()
		}
		if err := t.setStatus(n, to, "", cascade); err != nil {
			return importState{}, nil, err
		}
	}
	if moved || fields || status || len(notes) > 0 {
		r.Updated++
	}
	return saved, notes, nil
}

// mergeFields applies to c each field that changed from prev to next, and
// reports whether any did.
func mergeFields(c *Card, prev, next importState) bool {
	changed := false
	for _, f := range []struct {
		dst      *string
		from, to string
	}{{&c.Title, prev.Title, next.Title}, {&c.Desc, prev.Desc, next.Desc}, {&c.Due, prev.Due, next.Due}, {&c.Effort, prev.Effort, next.Effort}} {
		if f.from != f.to {
			*f.dst, changed = f.to, true
		}
	}
	if prev.Prio != next.Prio {
		c.Prio, changed = next.Prio, true
	}
	if prev.Blocked != next.Blocked {
		c.Blocked, changed = next.Blocked, true
	}
	if !slices.Equal(prev.Labels, next.Labels) {
		c.Labels, changed = slices.Clone(next.Labels), true
	}
	if !slices.Equal(prev.Checklist, next.Checklist) {
		c.Checklist, changed = slices.Clone(next.Checklist), true
	}
	return changed
}

// importConflict says why the card n can't take a source change, "" when it
// can: a held card and a card cancelled here keep their fields and status,
// and a status change must be one the owner could make directly.
func importConflict(n *node, fields, status bool, to Status) string {
	if !fields && !status {
		return ""
	}
	switch {
	case n.HeldBy != "":
		return fmt.Sprintf("%s is held by a Task", n.ref())
	case n.stored == StatusCancelled:
		return fmt.Sprintf("%s is cancelled; restore it to take the changes", n.ref())
	case !status:
		return ""
	}
	o := map[Status]op{StatusDone: opDone, StatusCancelled: opCancel, StatusTodo: opReady}[to]
	if err := permit(Owner(""), o, n.stored); err != nil {
		return fmt.Sprintf("%s: %v", n.ref(), err)
	}
	return ""
}

// importNotes are the automatic comments for a source task that moved from
// prev to next: it went in progress, it gained a cancel reason, or, when the
// import closed its card, the close comment.
func importNotes(prev, next importState, closed bool) []importNote {
	var out []importNote
	if next.Status == "doing" && prev.Status != "doing" {
		out = append(out, importNote{body: "was in progress in kb"})
	}
	if next.Reason != "" && next.Reason != prev.Reason {
		out = append(out, importNote{body: "cancelled: " + next.Reason})
	}
	if closed {
		out = append(out, importNote{body: "imported from kb", close: true})
	}
	return out
}

// importComments copies st's comments numbered above last to n as uam's
// automatic comments, keeping their time and naming their author, and
// returns the highest number copied.
func (t *txn) importComments(n *node, st *sourceTask, last int64, r *ImportReport) (int64, error) {
	for _, c := range st.Comments {
		if c.ID <= last {
			continue
		}
		if err := t.exec(`INSERT INTO comments (card_id, author, body, automatic, created_at) VALUES (?, ?, ?, 1, ?)`,
			n.ID, AuthorUAM, c.Author+" wrote: "+c.Body, stamp(sourceTime(c.CreatedAt, t.now))); err != nil {
			return 0, err
		}
		t.changed(n.ProjectID, n.ID)
		last = c.ID
		r.Comments++
	}
	return last, nil
}

// importLinks copies the blocker links of st's card e that are new since the
// last import, or that this import made possible by adding or moving a card,
// where both cards are in one Project. Links the source dropped since are
// removed.
func (t *txn) importLinks(st *sourceTask, e *importEntry, entries map[string]*importEntry, r *ImportReport) error {
	for _, id := range e.after {
		b := entries[id]
		if b == nil || e.project == "" || b.project != e.project || (!e.placed && !b.placed && slices.Contains(e.before, id)) {
			continue
		}
		o, err := t.outline(e.project)
		if err != nil {
			return err
		}
		err = t.link(o, o.byID[b.cardID], o.byID[e.cardID])
		switch code := CodeOf(err); {
		case err == nil:
			r.Links++
		case code == CodeDuplicate:
		case code == CodeInvalid:
			r.skip(st, err.Error())
		default:
			return err
		}
	}
	for _, id := range e.before {
		b := entries[id]
		if b == nil || slices.Contains(e.after, id) {
			continue
		}
		res, err := t.tx.ExecContext(t.ctx, `DELETE FROM links WHERE blocker_id = ? AND blocked_id = ?`, b.cardID, e.cardID)
		if err != nil {
			return fmt.Errorf("board: delete link: %w", err)
		}
		if n, err := res.RowsAffected(); err == nil && n > 0 {
			t.changed(b.project, b.cardID)
			t.changed(e.project, e.cardID)
		}
	}
	return nil
}

// sourceTime parses a source timestamp, falling back when it is unreadable.
func sourceTime(v string, fallback time.Time) time.Time {
	at, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return fallback
	}
	return at.UTC()
}
