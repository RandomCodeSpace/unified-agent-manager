// Ported from github.com/RandomCodeSpace/kb internal/store/store.go, sqlite.go and migrate.go (MIT).

package board

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	sqlite "modernc.org/sqlite" // database/sql driver "sqlite"
)

// Options supplies the store's clock and ID source. Zero fields use
// time.Now and random UUIDs.
type Options struct {
	Now   func() time.Time
	NewID func() string
}

// Change describes one committed write to one Project's board: the Project's
// new revision, the cards whose stored or derived state changed (ancestors
// included), the cards removed and the requests written.
type Change struct {
	ProjectID string
	Revision  int64
	Cards     []string
	Removed   []string
	Requests  []string
}

// Store is the planner database. It is safe for concurrent use: the pool
// holds one connection, so writers serialize in-process, and a busy database
// held by another process is retried.
type Store struct {
	db    *sql.DB
	now   func() time.Time
	newID func() string

	mu       sync.Mutex
	onChange func(Change)
}

// busyTimeout mirrors the DSN's busy_timeout(5000): how long a write waits
// for another process holding the write lock before it gives up.
const busyTimeout = 5 * time.Second

// timeLayout stores UTC timestamps at a fixed width, so they sort as text.
const timeLayout = "2006-01-02T15:04:05.000000000Z"

// Open opens, creating when needed, the planner database at path with mode
// 0600, and applies pending migrations. The directory must exist.
func Open(path string, opts Options) (*Store, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.NewID == nil {
		opts.NewID = uuid.NewString
	}
	// temp_store(2) keeps sorters and temporary tables in memory, so card
	// text never spills to $TMPDIR. journal_mode is not a DSN pragma: on a
	// fresh database it takes a write lock, so WAL is enabled after the
	// migration's BEGIN IMMEDIATE has serialized other openers.
	dsn, err := sqliteDSN(path, "busy_timeout(5000)", "foreign_keys(1)", "temp_store(2)", "synchronous(FULL)")
	if err != nil {
		return nil, fmt.Errorf("board: database path: %w", err)
	}
	if err := prepareFile(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("board: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	for _, step := range []func(*sql.DB) error{checkIntegrity, migrate, enableWAL} {
		if err := step(db); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	if err := chmodFiles(path); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db, now: opts.Now, newID: opts.NewID}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// OnChange registers fn to receive each committed write's changes, one per
// Project, after the commit and outside any transaction. Writers call fn from
// their own goroutines, so calls may overlap and arrive out of revision
// order; a Change's Revision orders them.
func (s *Store) OnChange(fn func(Change)) {
	s.mu.Lock()
	s.onChange = fn
	s.mu.Unlock()
}

// sqliteDSN returns a file URI for the host path with the given connection
// pragmas. Relative paths are resolved before URI encoding so they cannot
// become URI authorities.
func sqliteDSN(path string, pragmas ...string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	uriPath := "/" + strings.TrimPrefix(filepath.ToSlash(absolute), "/")
	query := url.Values{}
	for _, pragma := range pragmas {
		query.Add("_pragma", pragma)
	}
	return (&url.URL{Scheme: "file", Path: uriPath, RawQuery: query.Encode()}).String(), nil
}

// prepareFile creates the database file owner-only before SQLite opens it,
// and tightens an existing one; SQLite derives the WAL and SHM modes from it.
func prepareFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600) // #nosec G304 -- the caller's own planner database path.
	if err != nil {
		return fmt.Errorf("board: create database: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("board: create database: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("board: chmod %s: %w", path, err)
	}
	return nil
}

func chmodFiles(path string) error {
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(name, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("board: chmod %s: %w", name, err)
		}
	}
	return nil
}

func checkIntegrity(db *sql.DB) error {
	var result string
	if err := db.QueryRow(`PRAGMA quick_check(1)`).Scan(&result); err != nil {
		return fmt.Errorf("board: database integrity check failed: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("board: database integrity check failed: %s", result)
	}
	return nil
}

func enableWAL(db *sql.DB) error {
	for attempt := 0; ; attempt++ {
		var mode string
		err := db.QueryRow(`PRAGMA journal_mode=WAL`).Scan(&mode)
		if err == nil {
			if !strings.EqualFold(mode, "wal") {
				return fmt.Errorf("board: enable WAL: journal mode is %q", mode)
			}
			return nil
		}
		if !isBusy(err) || attempt >= 9 {
			return fmt.Errorf("board: enable WAL: %w", err)
		}
		time.Sleep(time.Duration(attempt+1) * time.Millisecond)
	}
}

// migrate creates the meta table and applies pending schema versions under
// BEGIN IMMEDIATE, so two openers cannot both apply the same version.
func migrate(db *sql.DB) error {
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("board: migration connection: %w", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("board: begin migration: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
		}
	}()
	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS meta (k TEXT PRIMARY KEY, v TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("board: create meta: %w", err)
	}
	var raw string
	err = conn.QueryRowContext(ctx, `SELECT v FROM meta WHERE k = 'schema_version'`).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("board: read schema version: %w", err)
	}
	version := 0
	if err == nil {
		version, err = strconv.Atoi(raw)
		if err != nil || version < 0 || raw != strconv.Itoa(version) {
			return fmt.Errorf("board: incompatible schema version %q", raw)
		}
		if version > len(migrations) {
			return fmt.Errorf("board: schema version %d is newer than this binary supports", version)
		}
	}
	for v := version; v < len(migrations); v++ {
		if _, err := conn.ExecContext(ctx, migrations[v]); err != nil {
			return fmt.Errorf("board: migrate to v%d: %w", v+1, err)
		}
		if _, err := conn.ExecContext(ctx, `INSERT INTO meta (k, v) VALUES ('schema_version', ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`, strconv.Itoa(v+1)); err != nil {
			return fmt.Errorf("board: record schema version %d: %w", v+1, err)
		}
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("board: commit migrations: %w", err)
	}
	committed = true
	return nil
}

func isBusy(err error) bool {
	var e *sqlite.Error
	if !errors.As(err, &e) {
		return false
	}
	code := e.Code() & 0xff
	return code == 5 || code == 6 // SQLITE_BUSY or SQLITE_LOCKED
}

// busyBackoff ramps a retry sleep to 10ms and holds it there.
func busyBackoff(attempt int) time.Duration {
	return min(time.Duration(attempt+1)*time.Millisecond, 10*time.Millisecond)
}

// txn is one transaction with the state the rules share: the clock reading,
// a cache of loaded outlines, and the changes to report after the commit.
type txn struct {
	ctx      context.Context
	tx       *sql.Tx
	s        *Store
	now      time.Time
	outlines map[string]*outline
	changes  map[string]*changeSet
	scopes   map[string]*scope
	// keepOpen lists containers settle leaves open in this write, though
	// they reached done: a Restore under an approved epic brings their
	// cards back as proposals to approve, which closing would cancel.
	keepOpen map[string]bool
	// landing lists the cards this write may move though their landing is
	// under way (notLanding): the write that finishes the landing.
	landing map[string]bool
}

type changeSet struct {
	cards, removed, requests map[string]bool
}

// write runs fn in a transaction and commits it, retrying while another
// process holds the database. database/sql begins deferred transactions, so
// a write that upgrades a read snapshot gets SQLITE_BUSY at once and the
// connection's busy_timeout never sees it; retrying for the same budget here
// makes a second process wait for the lock instead of failing the write.
func (s *Store) write(ctx context.Context, fn func(*txn) error) ([]Change, error) {
	deadline := time.Now().Add(busyTimeout)
	for attempt := 0; ; attempt++ {
		changes, err := s.writeOnce(ctx, fn)
		if err == nil {
			s.notify(changes)
			return changes, nil
		}
		if !isBusy(err) || !time.Now().Before(deadline) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(busyBackoff(attempt)):
		}
	}
}

func (s *Store) writeOnce(ctx context.Context, fn func(*txn) error) ([]Change, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("board: begin: %w", err)
	}
	// After Commit this is a no-op; on any other exit, a panic in fn
	// included, it releases the write lock.
	defer func() { _ = tx.Rollback() }()
	t := s.newTxn(ctx, tx)
	if err := fn(t); err != nil {
		return nil, err
	}
	changes, err := t.finish()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("board: commit: %w", err)
	}
	return changes, nil
}

// read runs fn in a transaction that is always rolled back, so it sees one
// consistent snapshot.
func (s *Store) read(ctx context.Context, fn func(*txn) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("board: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	return fn(s.newTxn(ctx, tx))
}

func (s *Store) newTxn(ctx context.Context, tx *sql.Tx) *txn {
	return &txn{
		ctx: ctx, tx: tx, s: s, now: s.now().UTC(),
		outlines: map[string]*outline{},
		changes:  map[string]*changeSet{},
		scopes:   map[string]*scope{},
		keepOpen: map[string]bool{},
		landing:  map[string]bool{},
	}
}

func (s *Store) notify(changes []Change) {
	s.mu.Lock()
	fn := s.onChange
	s.mu.Unlock()
	if fn == nil {
		return
	}
	for _, c := range changes {
		fn(c)
	}
}

// finish bumps each changed Project's revision once, stamps the changed
// cards with it, and returns the changes in Project order.
func (t *txn) finish() ([]Change, error) {
	projects := make([]string, 0, len(t.changes))
	for p := range t.changes {
		projects = append(projects, p)
	}
	slices.Sort(projects)
	out := make([]Change, 0, len(projects))
	for _, p := range projects {
		set := t.changes[p]
		if err := t.exec(`INSERT INTO revisions (project_id, revision) VALUES (?, 1)
			ON CONFLICT(project_id) DO UPDATE SET revision = revision + 1`, p); err != nil {
			return nil, err
		}
		revision, err := t.revision(p)
		if err != nil {
			return nil, err
		}
		o, err := t.outline(p)
		if err != nil {
			return nil, err
		}
		cards := map[string]bool{}
		for id := range set.cards {
			for n := o.byID[id]; n != nil; n = o.byID[n.ParentID] {
				cards[n.ID] = true
			}
		}
		change := Change{ProjectID: p, Revision: revision, Cards: sortedKeys(cards), Removed: sortedKeys(set.removed), Requests: sortedKeys(set.requests)}
		for _, id := range change.Cards {
			if err := t.exec(`UPDATE cards SET revision = ? WHERE id = ?`, revision, id); err != nil {
				return nil, err
			}
		}
		out = append(out, change)
	}
	return out, nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func (t *txn) set(project string) *changeSet {
	set := t.changes[project]
	if set == nil {
		set = &changeSet{cards: map[string]bool{}, removed: map[string]bool{}, requests: map[string]bool{}}
		t.changes[project] = set
	}
	return set
}

// changed records a write to a card and drops the Project's cached outline.
func (t *txn) changed(project, id string) {
	t.set(project).cards[id] = true
	delete(t.outlines, project)
}

func (t *txn) removed(project, id string) {
	set := t.set(project)
	delete(set.cards, id)
	set.removed[id] = true
	delete(t.outlines, project)
}

func (t *txn) requestChanged(project, id string) {
	t.set(project).requests[id] = true
	delete(t.outlines, project)
}

func (t *txn) exec(query string, args ...any) error {
	if _, err := t.tx.ExecContext(t.ctx, query, args...); err != nil {
		return fmt.Errorf("board: %w", err)
	}
	return nil
}

func (t *txn) revision(project string) (int64, error) {
	var revision int64
	err := t.tx.QueryRowContext(t.ctx, `SELECT revision FROM revisions WHERE project_id = ?`, project).Scan(&revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("board: read revision: %w", err)
	}
	return revision, nil
}

// nextSeq allocates the next board-wide card number. The counter only
// advances, so a number is never reused.
func (t *txn) nextSeq() (int64, error) {
	if err := t.exec(`INSERT INTO sequences (name, next) VALUES ('card', 2)
		ON CONFLICT(name) DO UPDATE SET next = next + 1`); err != nil {
		return 0, err
	}
	var next int64
	if err := t.tx.QueryRowContext(t.ctx, `SELECT next FROM sequences WHERE name = 'card'`).Scan(&next); err != nil {
		return 0, fmt.Errorf("board: read sequence: %w", err)
	}
	return next - 1, nil
}

func stamp(t time.Time) string { return t.UTC().Format(timeLayout) }

func parseStamp(v string) (time.Time, error) { return time.Parse(time.RFC3339Nano, v) }
