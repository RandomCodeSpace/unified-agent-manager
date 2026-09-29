package board

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOpenPragmasAndMode(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, path := range []string{filepath.Join(t.TempDir(), FileName), filepath.Join("rel", "hash#mark", FileName)} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		s, err := Open(path, Options{})
		if err != nil {
			t.Fatalf("Open(%q): %v", path, err)
		}
		for pragma, want := range map[string]string{
			"journal_mode": "wal", "foreign_keys": "1", "temp_store": "2", "synchronous": "2", "busy_timeout": "5000",
		} {
			var got string
			if err := s.db.QueryRow("PRAGMA " + pragma).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("PRAGMA %s = %q, want %q", pragma, got, want)
			}
		}
		if _, err := s.Create(context.Background(), owner, NewCard{ProjectID: proj, Kind: KindEpic, Title: "E"}); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %v, want 0600", path, info.Mode().Perm())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(data, []byte("SQLite format 3\x00")) {
			t.Fatalf("%s is not the database", path)
		}
		// Reopening applies no migration twice and keeps the data.
		s, err = Open(path, Options{})
		if err != nil {
			t.Fatal(err)
		}
		var version string
		if err := s.db.QueryRow(`SELECT v FROM meta WHERE k = 'schema_version'`).Scan(&version); err != nil || version != "1" {
			t.Fatalf("schema_version = %q, %v", version, err)
		}
		if snap, err := s.Board(context.Background(), proj); err != nil || len(snap.Cards) != 1 || snap.Revision != 1 {
			t.Fatalf("reopened board = %+v, %v", snap, err)
		}
		_ = s.Close()
	}
}

func TestOpenRefusesBadDatabases(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(filepath.Join(dir, "missing", FileName), Options{}); err == nil {
		t.Fatal("opened a database in a missing directory")
	}
	garbage := filepath.Join(dir, "garbage.db")
	if err := os.WriteFile(garbage, bytes.Repeat([]byte("not a database "), 512), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(garbage, Options{}); err == nil {
		t.Fatal("opened a file that is not a database")
	}
	for _, version := range []string{"99", "x", "-1", "01"} {
		path := filepath.Join(dir, "v"+version+".db")
		s, err := Open(path, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`UPDATE meta SET v = ? WHERE k = 'schema_version'`, version); err != nil {
			t.Fatal(err)
		}
		_ = s.Close()
		if s, err := Open(path, Options{}); err == nil {
			_ = s.Close()
			t.Fatalf("opened a database at schema version %q", version)
		}
	}
}

func TestSeqIsGlobalAndNeverReused(t *testing.T) {
	f := newFixture(t)
	a := f.create(owner, "", KindSubtask, "A")
	b, err := f.s.Create(f.ctx, owner, NewCard{ProjectID: "p2", Kind: KindSubtask, Title: "B"})
	f.must(err)
	c := f.create(owner, "", KindSubtask, "C")
	if a.Seq != 1 || b.Seq != 2 || c.Seq != 3 {
		t.Fatalf("seqs = %d %d %d, want 1 2 3 across Projects", a.Seq, b.Seq, c.Seq)
	}
	_, err = f.s.SetStatus(f.ctx, owner, "#3", StatusCancelled, "drop", false)
	f.must(err)
	if n, err := f.s.Purge(f.ctx, owner, proj); err != nil || n != 1 {
		t.Fatalf("purge = %d, %v", n, err)
	}
	if d := f.create(owner, "", KindSubtask, "D"); d.Seq != 4 {
		t.Fatalf("seq after purging #3 = %d, want 4", d.Seq)
	}
}

func TestRefs(t *testing.T) {
	f := newFixture(t)
	a := f.create(owner, "", KindSubtask, "A")
	for _, ref := range []string{a.ID, "#1", "1", " #1 "} {
		if got := f.card(ref); got.ID != a.ID {
			t.Fatalf("Card(%q) = %s", ref, got.ID)
		}
	}
	for _, ref := range []string{"#2", "#0", "", "#", "#1x", "id-9"} {
		if _, err := f.s.Card(f.ctx, ref); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Card(%q) error = %v, want not found", ref, err)
		}
	}
	cards, err := f.s.Cards(f.ctx, []string{"missing", a.ID})
	if err != nil || len(cards) != 1 || cards[0].ID != a.ID {
		t.Fatalf("Cards = %+v, %v", cards, err)
	}
}

func TestChangesAndRevisions(t *testing.T) {
	f := newFixture(t)
	epic, story, one, _ := f.tree()
	f.mu.Lock()
	f.changes = nil
	f.mu.Unlock()
	before := f.revision()
	c, err := f.s.Checklist(f.ctx, owner, one.ID, ChecklistEdit{Add: []string{"step"}})
	f.must(err)
	if got := f.revision(); got != before+1 || c.Revision != got {
		t.Fatalf("revision after one write = %d (card %d), want %d", got, c.Revision, before+1)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.changes) != 1 {
		t.Fatalf("changes = %+v, want one", f.changes)
	}
	ch := f.changes[0]
	want := map[string]bool{epic.ID: true, story.ID: true, one.ID: true}
	if ch.ProjectID != proj || ch.Revision != before+1 || len(ch.Cards) != len(want) {
		t.Fatalf("change = %+v, want the card and its ancestors", ch)
	}
	for _, id := range ch.Cards {
		if !want[id] {
			t.Fatalf("change lists %s", id)
		}
	}
}

func TestConcurrentWrites(t *testing.T) {
	f := newFixture(t)
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.s.Create(f.ctx, owner, NewCard{ProjectID: proj, Kind: KindSubtask, Title: fmt.Sprintf("card %d", i)})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		f.must(err)
	}
	snap, err := f.s.Board(f.ctx, proj)
	f.must(err)
	seen := map[int64]bool{}
	for _, c := range snap.Cards {
		seen[c.Seq] = true
	}
	if len(snap.Cards) != 16 || len(seen) != 16 || snap.Revision != 16 {
		t.Fatalf("%d cards, %d seqs, revision %d; want 16 each", len(snap.Cards), len(seen), snap.Revision)
	}
}

// A second process holding the write lock makes a write wait, not fail.
func TestBusyRetry(t *testing.T) {
	f := newFixture(t)
	other, err := Open(f.path, Options{})
	f.must(err)
	defer func() { _ = other.Close() }()
	conn, err := other.db.Conn(f.ctx)
	f.must(err)
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(f.ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := f.s.Create(f.ctx, owner, NewCard{ProjectID: proj, Kind: KindSubtask, Title: "waits"})
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	if _, err := conn.ExecContext(f.ctx, `COMMIT`); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("write under a busy database: %v", err)
	}
	if isBusy(errors.New("busy")) {
		t.Fatal("a plain error counts as busy")
	}
	if busyBackoff(0) != time.Millisecond || busyBackoff(50) != 10*time.Millisecond {
		t.Fatal("unexpected backoff")
	}
}

func TestClosedStore(t *testing.T) {
	f := newFixture(t)
	_, _, one, _ := f.tree()
	f.must(f.s.Close())
	ctx := f.ctx
	checks := []error{
		errOf(f.s.Card(ctx, one.ID)),
		errOf(f.s.Board(ctx, proj)),
		errOf(f.s.Create(ctx, owner, NewCard{ProjectID: proj, Kind: KindEpic, Title: "x"})),
		errOf(f.s.Reconcile(ctx, nil, f.asOf(), nil)),
		errOf(f.s.Sweep(ctx)),
		errOf(f.s.Request(ctx, "r")),
		errOf(f.s.ProjectSettings(ctx, proj)),
		errOf(f.s.Labels(ctx, proj)),
	}
	for i, err := range checks {
		if err == nil {
			t.Errorf("call %d on a closed store succeeded", i)
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	g := newFixture(t)
	if _, err := g.s.Create(cancelled, owner, NewCard{ProjectID: proj, Kind: KindEpic, Title: "x"}); err == nil {
		t.Fatal("a cancelled context wrote")
	}
}

func errOf[T any](_ T, err error) error { return err }

// Mixed reads and writes from many goroutines, with OnChange swapped
// underneath them, fail only with rule refusals and keep the invariants.
func TestConcurrentMixedOperations(t *testing.T) {
	f := newFixture(t)
	epic, story, _, _ := f.tree()
	f.must(f.s.StartPlanning(f.ctx, owner, epic.ID, "planner"))
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 15 {
				var err error
				switch (g + i) % 6 {
				case 0:
					_, err = f.s.Create(f.ctx, owner, NewCard{ProjectID: proj, Kind: KindSubtask, ParentID: story.ID, Title: fmt.Sprintf("o%d-%d", g, i)})
				case 1:
					_, err = f.s.Create(f.ctx, Agent("planner", fmt.Sprint(g)), NewCard{Kind: KindSubtask, ParentID: story.ID, Title: fmt.Sprintf("a%d-%d", g, i)})
				case 2:
					_, err = f.s.Board(f.ctx, proj)
				case 3:
					_, err = f.s.Reconcile(f.ctx, map[string]Stage{}, f.asOf(), nil)
				case 4:
					_, err = f.s.Sweep(f.ctx)
					f.s.OnChange(func(Change) { f.invariants() })
				case 5:
					var snap Snapshot
					snap, err = f.s.Board(f.ctx, proj)
					for _, c := range snap.Cards {
						if c.Kind == KindSubtask && c.Status == StatusPlanned {
							_, err = f.s.Launch(f.ctx, owner, c.ID, fmt.Sprintf("t%d", g), Baseline{})
							break
						}
					}
				}
				if err != nil && CodeOf(err) == "" {
					t.Errorf("goroutine %d step %d: %v", g, i, err)
				}
			}
		}()
	}
	wg.Wait()
	f.invariants()
}

// The queries on hot paths read through an index; the two held-by queries
// must use the cards_held partial index.
func TestQueryPlans(t *testing.T) {
	f := newFixture(t)
	f.tree()
	for _, tc := range []struct {
		query string
		args  []any
		index string
	}{
		{openHoldsQuery, []any{"t"}, "cards_held"},
		{heldQuery, nil, "cards_held"},
		{`SELECT ` + cardCols + ` FROM cards WHERE project_id = ? ORDER BY rank, seq`, []any{proj}, "cards_project_parent"},
		{`SELECT r.card_id, COUNT(*) FROM requests r JOIN cards c ON c.id = r.card_id
			WHERE c.project_id = ? AND r.status = 'pending' GROUP BY r.card_id`, []any{proj}, "requests_card"},
		{`SELECT blocker_id, blocked_id FROM links
			WHERE blocked_id IN (SELECT id FROM cards WHERE project_id = ?1)
			   OR blocker_id IN (SELECT id FROM cards WHERE project_id = ?1)
			ORDER BY blocker_id, blocked_id`, []any{proj}, "links_blocked"},
		{`SELECT COUNT(*) FROM cards WHERE created_by = ?`, []any{"task:x"}, "cards_created_by"},
		{`SELECT COUNT(*) FROM comments WHERE card_id = ? AND author = ? AND automatic = 0`, []any{"x", "a"}, "comments_card"},
		{`SELECT id FROM requests WHERE card_id = ? AND task_id = ? AND kind = ? AND status = 'pending'`, []any{"x", "t", "done"}, "requests_card"},
		{`DELETE FROM links WHERE blocker_id = ?1 OR blocked_id = ?1`, []any{"x"}, "links_blocked"},
	} {
		rows, err := f.s.db.Query(`EXPLAIN QUERY PLAN `+tc.query, tc.args...)
		f.must(err)
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			f.must(rows.Scan(&id, &parent, &unused, &detail))
			plan = append(plan, detail)
		}
		f.must(errors.Join(rows.Err(), rows.Close()))
		joined := strings.Join(plan, "; ")
		for _, line := range plan {
			if (strings.HasPrefix(line, "SCAN ") || strings.HasPrefix(line, "SEARCH ")) && !strings.Contains(line, "INDEX") {
				t.Errorf("%.60s… reads a whole table: %s", tc.query, joined)
			}
		}
		if !strings.Contains(joined, "INDEX "+tc.index) {
			t.Errorf("%.60s… doesn't use %s: %s", tc.query, tc.index, joined)
		}
	}
}
