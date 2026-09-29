package board

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const proj = "p1"

var owner = Owner("head-1")

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

type fixture struct {
	t       *testing.T
	s       *Store
	path    string
	clock   *testClock
	ctx     context.Context
	mu      sync.Mutex
	changes []Change
}

// newFixture opens a store with a fixed clock and sequential IDs, and checks
// the stored invariants after every committed write.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	clock := &testClock{now: time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)}
	var mu sync.Mutex
	next := 0
	path := filepath.Join(t.TempDir(), FileName)
	s, err := Open(path, Options{Now: clock.Now, NewID: func() string {
		mu.Lock()
		defer mu.Unlock()
		next++
		return fmt.Sprintf("id-%04d", next)
	}})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, s: s, path: path, clock: clock, ctx: context.Background()}
	s.OnChange(func(c Change) {
		f.mu.Lock()
		f.changes = append(f.changes, c)
		f.mu.Unlock()
		f.invariants()
	})
	t.Cleanup(func() { _ = s.Close() })
	return f
}

// invariants checks ADR 0005's stored invariants: doing ⇔ held_by, one open
// attempt per hold, no pending request on a terminal card, no confirmed card
// under an unconfirmed one, and no open subtask under a cancelled card.
func (f *fixture) invariants() {
	f.t.Helper()
	for _, q := range []struct{ name, sql string }{
		{"doing without hold", `SELECT COUNT(*) FROM cards WHERE (status = 'doing') <> (held_by <> '')`},
		{"hold without attempt", `SELECT COUNT(*) FROM cards c WHERE c.held_by <> '' AND NOT EXISTS (
			SELECT 1 FROM holds h WHERE h.card_id = c.id AND h.ended_at = '' AND h.task_id = c.held_by)`},
		{"attempt without hold", `SELECT COUNT(*) FROM holds h WHERE h.ended_at = '' AND NOT EXISTS (
			SELECT 1 FROM cards c WHERE c.id = h.card_id AND c.held_by = h.task_id)`},
		{"pending on terminal", `SELECT COUNT(*) FROM requests r JOIN cards c ON c.id = r.card_id
			WHERE r.status = 'pending' AND c.status IN ('done', 'cancelled')`},
		{"confirmed under unconfirmed", `SELECT COUNT(*) FROM cards c JOIN cards p ON p.id = c.parent_id
			WHERE c.expires_at IS NULL AND p.expires_at IS NOT NULL`},
		{"container status", `SELECT COUNT(*) FROM cards WHERE kind <> 'subtask' AND status NOT IN ('planned', 'cancelled')`},
		{"open under cancelled", `WITH RECURSIVE up(id, parent) AS (
			SELECT id, parent_id FROM cards WHERE kind = 'subtask' AND status NOT IN ('done', 'cancelled')
			UNION ALL SELECT up.id, c.parent_id FROM up JOIN cards c ON c.id = up.parent)
			SELECT COUNT(*) FROM up JOIN cards p ON p.id = up.parent WHERE p.status = 'cancelled'`},
	} {
		var n int
		if err := f.s.db.QueryRow(q.sql).Scan(&n); err != nil {
			f.t.Errorf("invariant %s: %v", q.name, err)
			continue
		}
		if n != 0 {
			f.t.Errorf("invariant %s broken on %d rows", q.name, n)
		}
	}
}

func (f *fixture) create(a Actor, parent string, kind Kind, title string) Card {
	f.t.Helper()
	c, err := f.s.Create(f.ctx, a, NewCard{ProjectID: proj, Kind: kind, ParentID: parent, Title: title})
	if err != nil {
		f.t.Fatalf("create %q: %v", title, err)
	}
	return c
}

func (f *fixture) card(ref string) Card {
	f.t.Helper()
	c, err := f.s.Card(f.ctx, ref)
	if err != nil {
		f.t.Fatalf("card %s: %v", ref, err)
	}
	return c
}

func (f *fixture) detail(ref string) Detail {
	f.t.Helper()
	d, err := f.s.Detail(f.ctx, ref)
	if err != nil {
		f.t.Fatalf("detail %s: %v", ref, err)
	}
	return d
}

func (f *fixture) must(err error) {
	f.t.Helper()
	if err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) revision() int64 {
	f.t.Helper()
	snap, err := f.s.Board(f.ctx, proj)
	f.must(err)
	return snap.Revision
}

// tree builds epic › story › subtasks one and two, all owner-created.
func (f *fixture) tree() (epic, story, one, two Card) {
	epic = f.create(owner, "", KindEpic, "Epic")
	story = f.create(owner, epic.ID, KindStory, "Story")
	one = f.create(owner, story.ID, KindSubtask, "One")
	two = f.create(owner, story.ID, KindSubtask, "Two")
	return
}

func (f *fixture) launch(ref, task string) Card {
	f.t.Helper()
	c, err := f.s.Launch(f.ctx, owner, ref, task, Baseline{Head: "base", Dirty: []string{"x.go"}})
	if err != nil {
		f.t.Fatalf("launch %s: %v", ref, err)
	}
	return c
}

func (f *fixture) done(ref string, a Actor) Request {
	f.t.Helper()
	r, err := f.s.FileRequest(f.ctx, a, ref, RequestInput{Kind: RequestDone, Comment: "finished " + ref})
	if err != nil {
		f.t.Fatalf("done request on %s: %v", ref, err)
	}
	return r
}

// asOf returns a Task-snapshot time after every hold started so far.
func (f *fixture) asOf() time.Time {
	return f.clock.Now().Add(time.Second)
}

// children returns parent's direct children in rank order.
func (f *fixture) children(parent string) []Card {
	f.t.Helper()
	snap, err := f.s.Board(f.ctx, proj)
	f.must(err)
	var out []Card
	for _, c := range snap.Cards {
		if c.ParentID == parent {
			out = append(out, c)
		}
	}
	return out
}

func (f *fixture) comments(ref string) []string {
	f.t.Helper()
	var out []string
	for _, c := range f.detail(ref).Comments {
		out = append(out, c.Author+": "+c.Body)
	}
	return out
}

func (f *fixture) raw(query string, args ...any) {
	f.t.Helper()
	if _, err := f.s.db.Exec(query, args...); err != nil {
		f.t.Fatalf("%s: %v", query, err)
	}
}

// unassigned inserts a confirmed root subtask in the Unassigned list, as the
// one-time import will.
func (f *fixture) unassigned(title string) string {
	f.t.Helper()
	var id string
	_, err := f.s.write(f.ctx, func(t *txn) error {
		o, err := t.outline("")
		if err != nil {
			return err
		}
		n := &node{stored: StatusPlanned, Card: Card{Kind: KindSubtask, Title: title, Prio: PrioDefault, Effort: DefaultEffort,
			CreatedBy: AuthorUAM, CreatedAt: t.now, UpdatedAt: t.now, MovedAt: t.now}}
		err = t.insertCard(o, n)
		id = n.ID
		return err
	})
	f.must(err)
	return id
}

func wantCode(t *testing.T, err error, code Code) {
	t.Helper()
	if got := CodeOf(err); got != code {
		t.Fatalf("error = %v (code %q), want code %q", err, got, code)
	}
}

func wantStatus(t *testing.T, c Card, want Status) {
	t.Helper()
	if c.Status != want {
		t.Fatalf("%s %q status = %s, want %s", c.ref(), c.Title, c.Status, want)
	}
}

func hasComment(comments []string, substr string) bool {
	for _, c := range comments {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

func TestErrorMatching(t *testing.T) {
	err := fmt.Errorf("wrap: %w", refuse(CodeNotFound, "card x not found"))
	if !errors.Is(err, ErrNotFound) {
		t.Fatal("a wrapped not-found refusal does not match ErrNotFound")
	}
	if errors.Is(refuse(CodeInvalid, "x"), ErrNotFound) || errors.Is(refuse(CodeNotFound, "a"), refuse(CodeNotFound, "b")) {
		t.Fatal("refusals match beyond their code sentinel")
	}
	if CodeOf(errors.New("plain")) != "" {
		t.Fatal("a plain error has a code")
	}
}

// The acceptance refusals are raised outside the store, and their codes are
// part of the HTTP and tool contract.
func TestAcceptanceCodes(t *testing.T) {
	for code, want := range map[Code]string{CodeAcceptanceBusy: "acceptance_busy", CodeAcceptanceFailed: "acceptance_failed"} {
		if err := fmt.Errorf("wrap: %w", &Error{Code: code, Message: "x"}); string(CodeOf(err)) != want {
			t.Fatalf("code %q reads as %q", want, CodeOf(err))
		}
	}
}
