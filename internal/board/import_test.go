package board

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The import fixtures were written by the real kb binaries, each in a fresh
// temporary data directory, and only kb.db is kept:
//
//   - import-v11: kb v1.13.0 (schema 11). Projects website (tasks 1-7) and
//     api (8-12) across todo, doing, done and cancelled, with tags, a link::
//     tag, checklists, a blocked flag, an emoji, four comments (two by
//     --author agent), the links 1→7, 2→7, 8→9 and 3→12, a done --force task
//     with an open item, and a cancel reason posted through kb web.
//   - import-v10: kb v1.7.3 (schema 10), one task.

var websiteOnly = map[string]string{"website": proj}

// sourceCopy copies the fixture name into a fresh directory, so a test may
// change the source without touching testdata.
func sourceCopy(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name, sourceFile))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, sourceFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// openSource opens the source copy in dir for writing, as the source app
// would.
func openSource(t *testing.T, dir string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, sourceFile))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func sourceExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// sourceID returns the UUID of the source task numbered seq.
func sourceID(t *testing.T, db *sql.DB, seq int) string {
	t.Helper()
	var id string
	if err := db.QueryRow(`SELECT id FROM tasks WHERE user = 'default' AND seq = ?`, seq).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *fixture) importFrom(dir string, projects map[string]string) ImportReport {
	f.t.Helper()
	r, err := f.s.Import(f.ctx, dir, projects)
	if err != nil {
		f.t.Fatalf("import: %v", err)
	}
	return r
}

func (f *fixture) byTitle(project, title string) Card {
	f.t.Helper()
	snap, err := f.s.Board(f.ctx, project)
	f.must(err)
	for _, c := range snap.Cards {
		if c.Title == title {
			return c
		}
	}
	f.t.Fatalf("no card %q in Project %q", title, project)
	return Card{}
}

func (f *fixture) count(project string) int {
	f.t.Helper()
	snap, err := f.s.Board(f.ctx, project)
	f.must(err)
	return len(snap.Cards)
}

func wantReport(t *testing.T, got, want ImportReport) {
	t.Helper()
	if want.Skipped == nil {
		want.Skipped = []ImportSkip{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("report = %+v, want %+v", got, want)
	}
}

func TestImportFullBoard(t *testing.T) {
	f := newFixture(t)
	r := f.importFrom(sourceCopy(t, "import-v11"), websiteOnly)
	// Only website's links are copied: 8→9 sits in Unassigned and 3→12
	// crosses Projects.
	wantReport(t, r, ImportReport{Imported: 12, Unassigned: 5, Comments: 4, Links: 2})
	if len(f.changes) != 2 || f.changes[0].ProjectID != "" || f.changes[1].ProjectID != proj {
		t.Fatalf("changes = %+v, want one for Unassigned and one for %s", f.changes, proj)
	}
	if f.count(proj) != 7 || f.count("") != 5 {
		t.Fatalf("cards = %d in %s and %d Unassigned, want 7 and 5", f.count(proj), proj, f.count(""))
	}
	for _, project := range []string{proj, ""} {
		snap, err := f.s.Board(f.ctx, project)
		f.must(err)
		for _, c := range snap.Cards {
			if c.Kind != KindSubtask || c.ParentID != "" || !c.Confirmed() || c.PinnedSHA != "" || c.HeldBy != "" ||
				c.Status == StatusDoing || c.Status == StatusPlanned || c.CreatedBy != AuthorUAM {
				t.Errorf("imported card %+v, want a confirmed, unpinned, unheld root subtask", c)
			}
		}
	}
	var holds int
	f.must(f.s.db.QueryRow(`SELECT COUNT(*) FROM holds`).Scan(&holds))
	if holds != 0 {
		t.Fatalf("%d holds after an import", holds)
	}

	landing := f.byTitle(proj, "Build landing page")
	if landing.Status != StatusTodo || landing.Prio != PrioHigh || landing.Due != "2026-10-15" || landing.Effort != "M" ||
		landing.Desc != "Hero, pricing and signup." || !slices.Equal(landing.Labels, []string{"frontend", "type::feature"}) ||
		!slices.Equal(landing.Checklist, []Check{{Text: "Draft copy"}, {Text: "Pick fonts", Done: true}}) {
		t.Fatalf("landing page = %+v", landing)
	}
	if got, want := f.comments(landing.ID), []string{"uam: default wrote: Copy is ready for review", "uam: agent wrote: Fonts picked: Inter"}; !slices.Equal(got, want) {
		t.Fatalf("landing page comments = %q, want %q", got, want)
	}
	for _, c := range f.detail(landing.ID).Comments {
		if !c.Automatic || c.Close || c.CreatedAt.Year() != 2026 || c.CreatedAt.Equal(f.clock.Now()) {
			t.Fatalf("copied comment %+v, want automatic with its source time", c)
		}
	}

	post := f.byTitle(proj, "🚀 Write launch post")
	wantStatus(t, post, StatusTodo)
	if got := f.comments(post.ID); !slices.Equal(got, []string{"uam: was in progress in kb"}) {
		t.Fatalf("in-progress comments = %q", got)
	}
	for _, title := range []string{"Set up analytics", "Fix mobile nav"} {
		c := f.byTitle(proj, title)
		wantStatus(t, c, StatusDone)
		d := f.detail(c.ID)
		if len(d.Comments) != 1 || d.Comments[0].Body != "imported from kb" || !d.Comments[0].Close || !d.Comments[0].Automatic {
			t.Fatalf("%s comments = %+v, want the automatic close comment", title, d.Comments)
		}
	}
	if nav := f.byTitle(proj, "Fix mobile nav"); !slices.Equal(nav.Checklist, []Check{{Text: "Reproduce", Done: true}, {Text: "Test on iPhone"}}) {
		t.Fatalf("forced done checklist = %+v", nav.Checklist)
	}
	carousel := f.byTitle(proj, "Old carousel")
	wantStatus(t, carousel, StatusCancelled)
	if got, want := f.comments(carousel.ID), []string{"uam: default wrote: Carousels hurt conversion",
		"uam: cancelled: Replaced by the hero video", "uam: imported from kb"}; !slices.Equal(got, want) || carousel.CascadeID == "" {
		t.Fatalf("cancelled card %+v comments = %q, want %q", carousel, got, want)
	}
	launch := f.byTitle(proj, "Launch")
	blockers := slices.Sorted(slices.Values(launch.BlockedBy))
	if !launch.Blocked || !slices.Equal(launch.Labels, []string{"release"}) ||
		!slices.Equal(blockers, slices.Sorted(slices.Values([]string{landing.ID, post.ID}))) {
		t.Fatalf("launch = %+v, want blocked, labelled release and blocked by #%d and #%d", launch, landing.Seq, post.Seq)
	}

	rate := f.byTitle("", "Rate limiting")
	if got, want := f.comments(rate.ID), []string{"uam: agent wrote: Started with a fixed window", "uam: was in progress in kb"}; !slices.Equal(got, want) ||
		len(rate.BlockedBy) != 0 || rate.Status != StatusTodo {
		t.Fatalf("unassigned in-progress card %+v comments %q, want %q and no links", rate, got, want)
	}
	if load := f.byTitle("", "Load test"); len(load.BlockedBy) != 0 || load.Due != "2026-11-01" {
		t.Fatalf("cross-Project blocked card = %+v", load)
	}
	if labels, err := f.s.Labels(f.ctx, proj); err != nil || !slices.Contains(labels, "type::bug") || slices.ContainsFunc(labels, func(l string) bool {
		return l == "project::website" || l == "link::forge-42"
	}) {
		t.Fatalf("labels = %q, %v", labels, err)
	}

	// An imported cancelled card restores like any other.
	restored, err := f.s.Restore(f.ctx, owner, f.byTitle(proj, "Add dark mode").ID, "back on the list")
	f.must(err)
	wantStatus(t, restored, StatusPlanned)
}

func TestImportIsIdempotent(t *testing.T) {
	f := newFixture(t)
	dir := sourceCopy(t, "import-v11")
	f.importFrom(dir, websiteOnly)
	snap, err := f.s.Board(f.ctx, "")
	f.must(err)
	revision, unassigned := f.revision(), snap.Revision
	f.changes = nil
	wantReport(t, f.importFrom(dir, websiteOnly), ImportReport{})
	snap, err = f.s.Board(f.ctx, "")
	f.must(err)
	if len(f.changes) != 0 || f.revision() != revision || snap.Revision != unassigned {
		t.Fatalf("a repeated import changed the board: %+v", f.changes)
	}
	if f.count(proj) != 7 || f.count("") != 5 || len(f.comments(f.byTitle(proj, "Build landing page").ID)) != 2 {
		t.Fatal("a repeated import added cards or comments")
	}
}

func TestImportAppliesSourceChanges(t *testing.T) {
	f := newFixture(t)
	dir := sourceCopy(t, "import-v11")
	f.importFrom(dir, websiteOnly)
	landing, launch := f.byTitle(proj, "Build landing page"), f.byTitle(proj, "Launch")
	desc := "Ship it on Monday"
	_, err := f.s.Edit(f.ctx, owner, launch.ID, Patch{Desc: &desc})
	f.must(err)
	f.launch(landing.ID, "task-1")

	src := openSource(t, dir)
	sourceExec(t, src, `UPDATE tasks SET title = 'Build the landing page' WHERE seq = 1`)
	sourceExec(t, src, `UPDATE tasks SET status = 'cancelled' WHERE seq = 3`)
	sourceExec(t, src, `UPDATE tasks SET title = 'Launch v1' WHERE seq = 7`)
	sourceExec(t, src, `UPDATE tasks SET status = 'done' WHERE seq = 12`)
	sourceExec(t, src, `DELETE FROM task_links WHERE blocker_id = ? AND blocked_id = ?`, sourceID(t, src, 2), sourceID(t, src, 7))
	sourceExec(t, src, `INSERT INTO comments (scope, id, task_id, author, body, created_at)
		VALUES ('default', 5, ?, 'default', 'Moved to Monday', '2026-09-30T09:00:00Z')`, sourceID(t, src, 7))
	for _, user := range []string{"default", "alice"} {
		sourceExec(t, src, `INSERT INTO tasks (id, seq, user, title, status, prio, effort, tags, checks, position, created_at, moved_at, updated_at)
			VALUES (?, 13, ?, 'Write changelog', 'todo', 2, 'S', '["project::website"]', '[]', 3, ?, ?, ?)`,
			"0f0e0d0c-0000-4000-8000-"+user[:1]+"00000000000", user, "2026-09-30T09:00:00Z", "2026-09-30T09:00:00Z", "2026-09-30T09:00:00Z")
	}
	f.changes = nil
	r := f.importFrom(dir, websiteOnly)
	wantReport(t, r, ImportReport{Imported: 1, Updated: 2, Comments: 1, Skipped: []ImportSkip{
		{ID: sourceID(t, src, 1), Reason: `"Build the landing page": #1 is held by a Task`},
		{ID: sourceID(t, src, 3), Reason: `"Set up analytics": #3: cannot cancel a done subtask`},
	}})
	if len(f.changes) != 2 {
		t.Fatalf("changes = %+v, want one per Project", f.changes)
	}
	launch = f.byTitle(proj, "Launch v1")
	if launch.Desc != desc || !slices.Equal(launch.BlockedBy, []string{landing.ID}) ||
		!hasComment(f.comments(launch.ID), "uam: default wrote: Moved to Monday") {
		t.Fatalf("launch = %+v comments %q, want the source title, the owner's description, one blocker and the new comment", launch, f.comments(launch.ID))
	}
	if got := f.card(landing.ID); got.Title != "Build landing page" || got.Status != StatusDoing {
		t.Fatalf("held card = %+v, want it untouched", got)
	}
	wantStatus(t, f.byTitle(proj, "Set up analytics"), StatusDone)
	load := f.byTitle("", "Load test")
	wantStatus(t, load, StatusDone)
	if got := f.comments(load.ID); !slices.Equal(got, []string{"uam: imported from kb"}) {
		t.Fatalf("closed card comments = %q", got)
	}
	if c := f.byTitle(proj, "Write changelog"); c.Prio != PrioMedium || f.count(proj)+f.count("") != 13 {
		t.Fatalf("new card = %+v; another user's task must not be imported", c)
	}

	// The skipped change is retried once the hold ends.
	_, err = f.s.ReleaseHold(f.ctx, owner, landing.ID, ReleaseOwner, "")
	f.must(err)
	wantReport(t, f.importFrom(dir, websiteOnly), ImportReport{Updated: 1, Skipped: []ImportSkip{
		{ID: sourceID(t, src, 3), Reason: `"Set up analytics": #3: cannot cancel a done subtask`},
	}})
	if got := f.card(landing.ID); got.Title != "Build the landing page" || got.Status != StatusTodo {
		t.Fatalf("released card = %+v, want the source title", got)
	}
}

func TestImportMovesUnassignedCardsIntoAMappedProject(t *testing.T) {
	f := newFixture(t)
	dir := sourceCopy(t, "import-v11")
	f.importFrom(dir, websiteOnly)
	rate := f.byTitle("", "Rate limiting")
	r := f.importFrom(dir, map[string]string{"website": proj, "api": "p2"})
	wantReport(t, r, ImportReport{Updated: 5, Links: 1})
	if f.count("") != 0 || f.count("p2") != 5 {
		t.Fatalf("cards = %d Unassigned and %d in p2, want 0 and 5", f.count(""), f.count("p2"))
	}
	design, moved := f.byTitle("p2", "Design REST endpoints"), f.byTitle("p2", "Rate limiting")
	if moved.ID != rate.ID || !slices.Equal(moved.BlockedBy, []string{design.ID}) || len(f.comments(rate.ID)) != 2 {
		t.Fatalf("moved card = %+v, want the same card with its comments, now blocked by #%d", moved, design.Seq)
	}
	if load := f.byTitle("p2", "Load test"); len(load.BlockedBy) != 0 {
		t.Fatalf("a link across Projects was copied: %+v", load)
	}
	// A name that maps to nothing leaves nothing to move.
	wantReport(t, f.importFrom(dir, map[string]string{"website": proj, "api": "p3"}), ImportReport{})
}

func TestImportSettlesAStoryTheOwnerBuilt(t *testing.T) {
	f := newFixture(t)
	dir := sourceCopy(t, "import-v11")
	f.importFrom(dir, websiteOnly)
	story := f.create(owner, "", KindStory, "Release")
	post := f.byTitle(proj, "🚀 Write launch post")
	_, err := f.s.Edit(f.ctx, owner, post.ID, Patch{ParentID: &story.ID})
	f.must(err)
	sourceExec(t, openSource(t, dir), `UPDATE tasks SET status = 'done' WHERE seq = 2`)
	wantReport(t, f.importFrom(dir, websiteOnly), ImportReport{Updated: 1})
	wantStatus(t, f.card(story.ID), StatusDone)
	if !hasComment(f.comments(story.ID), "#2 🚀 Write launch post: imported from kb") {
		t.Fatalf("story comments = %q, want the roll-up", f.comments(story.ID))
	}
}

func TestImportSkipsWhatItCannotTake(t *testing.T) {
	f := newFixture(t)
	dir := sourceCopy(t, "import-v11")
	f.importFrom(dir, websiteOnly)
	landing := f.byTitle(proj, "Build landing page")
	_, err := f.s.SetStatus(f.ctx, owner, landing.ID, StatusCancelled, "not this quarter", false)
	f.must(err)
	src := openSource(t, dir)
	sourceExec(t, src, `UPDATE tasks SET title = 'Build the landing page' WHERE seq = 1`)
	long := strings.Repeat("x", 501)
	for i, add := range []struct{ id, title, status, tags, checks string }{
		{"bad-label", "Bad task", "todo", `["two words"]`, `[]`},
		{"bad-status", "Bad task", "archived", `[]`, `[]`},
		{"bad-title", long, "todo", `[]`, `[]`},
		// Older source boards hold blank checklist items; only they are dropped.
		{"blank-items", "Sparse", "todo", `["project::website"]`, `[{"Text":"  ","Done":false},{"Text":"Real","Done":true},{"Text":"","Done":true}]`},
	} {
		sourceExec(t, src, `INSERT INTO tasks (id, seq, user, title, status, tags, checks, created_at, moved_at, updated_at)
			VALUES (?, ?, 'default', ?, ?, ?, ?, '', '', '')`, add.id, 13+i, add.title, add.status, add.tags, add.checks)
	}
	wantReport(t, f.importFrom(dir, websiteOnly), ImportReport{Imported: 1, Skipped: []ImportSkip{
		{ID: sourceID(t, src, 1), Reason: `"Build the landing page": #1 is cancelled; restore it to take the changes`},
		{ID: "bad-label", Reason: `"Bad task": invalid label "two words": must not contain whitespace`},
		{ID: "bad-status", Reason: `"Bad task": unknown status "archived"`},
		{ID: "bad-title", Reason: fmt.Sprintf("%q: title exceeds 500 bytes", long)},
	}})
	if got := f.card(landing.ID); got.Title != "Build landing page" {
		t.Fatalf("cancelled card = %+v, want it untouched", got)
	}
	if got := f.byTitle(proj, "Sparse"); !slices.Equal(got.Checklist, []Check{{Text: "Real", Done: true}}) {
		t.Fatalf("checklist = %+v, want only the item with text", got.Checklist)
	}
}

func TestImportReopensDoneCards(t *testing.T) {
	f := newFixture(t)
	dir := sourceCopy(t, "import-v11")
	f.importFrom(dir, websiteOnly)
	// Set up analytics stays done under a story the owner then cancelled.
	analytics, nav := f.byTitle(proj, "Set up analytics"), f.byTitle(proj, "Fix mobile nav")
	story := f.create(owner, "", KindStory, "Metrics")
	_, err := f.s.Edit(f.ctx, owner, analytics.ID, Patch{ParentID: &story.ID})
	f.must(err)
	f.create(owner, story.ID, KindSubtask, "Dashboards")
	_, err = f.s.SetStatus(f.ctx, owner, story.ID, StatusCancelled, "not now", false)
	f.must(err)
	wantStatus(t, f.card(analytics.ID), StatusDone)

	src := openSource(t, dir)
	sourceExec(t, src, `UPDATE tasks SET status = 'todo' WHERE seq IN (3, 4)`)
	skip := ImportSkip{ID: sourceID(t, src, 3),
		Reason: fmt.Sprintf(`"Set up analytics": #3 is under cancelled #%d; restore that first`, story.Seq)}
	wantReport(t, f.importFrom(dir, websiteOnly), ImportReport{Updated: 1, Skipped: []ImportSkip{skip}})
	wantStatus(t, f.card(nav.ID), StatusTodo)
	wantStatus(t, f.card(analytics.ID), StatusDone)
	// The refused reopen is tried again, and applies once the story is back.
	wantReport(t, f.importFrom(dir, websiteOnly), ImportReport{Skipped: []ImportSkip{skip}})
	_, err = f.s.Restore(f.ctx, owner, story.ID, "back on")
	f.must(err)
	wantReport(t, f.importFrom(dir, websiteOnly), ImportReport{Updated: 1})
	wantStatus(t, f.card(analytics.ID), StatusTodo)
}

func TestImportLinksCardsTheOwnerMovedIn(t *testing.T) {
	f := newFixture(t)
	dir := sourceCopy(t, "import-v11")
	f.importFrom(dir, websiteOnly)
	design, rate := f.byTitle("", "Design REST endpoints"), f.byTitle("", "Rate limiting")
	p2 := "p2"
	for _, c := range []Card{design, rate} {
		_, err := f.s.Edit(f.ctx, owner, c.ID, Patch{ProjectID: &p2})
		f.must(err)
	}
	wantReport(t, f.importFrom(dir, websiteOnly), ImportReport{Links: 1})
	if got := f.card(rate.ID); !slices.Equal(got.BlockedBy, []string{design.ID}) {
		t.Fatalf("moved card = %+v, want it blocked by #%d", got, design.Seq)
	}
}

func TestImportRetriesALinkRefusedForACycle(t *testing.T) {
	f := newFixture(t)
	dir := sourceCopy(t, "import-v11")
	f.importFrom(dir, websiteOnly)
	landing, post, launch := f.byTitle(proj, "Build landing page"), f.byTitle(proj, "🚀 Write launch post"), f.byTitle(proj, "Launch")
	f.must(f.s.Link(f.ctx, owner, landing.ID, post.ID))
	src := openSource(t, dir)
	sourceExec(t, src, `INSERT INTO task_links (scope, blocker_id, blocked_id) VALUES ('default', ?, ?)`, sourceID(t, src, 2), sourceID(t, src, 1))
	wantReport(t, f.importFrom(dir, websiteOnly), ImportReport{Skipped: []ImportSkip{{ID: sourceID(t, src, 1),
		Reason: fmt.Sprintf(`"Build landing page": the link would create a cycle: #%d already blocks #%d`, landing.Seq, post.Seq)}}})

	// Once the owner removes the cycle the link is made. The owner's unlink
	// of an imported link stands.
	f.must(f.s.Unlink(f.ctx, owner, landing.ID, post.ID))
	f.must(f.s.Unlink(f.ctx, owner, landing.ID, launch.ID))
	wantReport(t, f.importFrom(dir, websiteOnly), ImportReport{Links: 1})
	if got := f.card(landing.ID); !slices.Equal(got.BlockedBy, []string{post.ID}) {
		t.Fatalf("landing page = %+v, want it blocked by #%d", got, post.Seq)
	}
	if got := f.card(launch.ID); !slices.Equal(got.BlockedBy, []string{post.ID}) {
		t.Fatalf("launch = %+v, want the owner's unlink kept", got)
	}
}

func TestImportKeepsRepeatedTitles(t *testing.T) {
	f := newFixture(t)
	dir := sourceCopy(t, "import-v11")
	sourceExec(t, openSource(t, dir), `INSERT INTO tasks (id, seq, user, title, status, tags, checks, created_at, moved_at, updated_at)
		VALUES ('again', 13, 'default', 'Launch', 'todo', '["project::website"]', '[]', '', '', '')`)
	wantReport(t, f.importFrom(dir, websiteOnly), ImportReport{Imported: 13, Unassigned: 5, Comments: 4, Links: 2})
	launches := 0
	snap, err := f.s.Board(f.ctx, proj)
	f.must(err)
	for _, c := range snap.Cards {
		if c.Title == "Launch" && c.ParentID == "" {
			launches++
		}
	}
	if launches != 2 {
		t.Fatalf("%d root cards titled Launch, want both kept", launches)
	}
}

func TestImportRetriesACopyThatRacedAWriter(t *testing.T) {
	f := newFixture(t)
	dir := sourceCopy(t, "import-v11")
	src := openSource(t, dir)
	copies := 0
	afterSourceCopy = func() {
		copies++
		if copies == 1 {
			// The source app writes between the copies, so its WAL appears.
			sourceExec(t, src, `UPDATE tasks SET title = 'Build the landing page' WHERE seq = 1`)
		}
	}
	t.Cleanup(func() { afterSourceCopy = nil })
	f.importFrom(dir, websiteOnly)
	if copies != 2 {
		t.Fatalf("copied %d times, want a second copy after the write", copies)
	}
	f.byTitle(proj, "Build the landing page")
}

func TestImportRefusesASourceThatKeepsChanging(t *testing.T) {
	f := newFixture(t)
	dir := sourceCopy(t, "import-v11")
	src := openSource(t, dir)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	copies := 0
	afterSourceCopy = func() {
		copies++
		sourceExec(t, src, `UPDATE tasks SET title = ? WHERE seq = 1`, fmt.Sprint("Title ", copies))
	}
	t.Cleanup(func() { afterSourceCopy = nil })
	_, err := f.s.Import(f.ctx, dir, websiteOnly)
	wantCode(t, err, CodeImportBusy)
	if err.Error() != "the source database is being written; try again" || copies != 1+copyRetries {
		t.Fatalf("refusal = %q after %d copies, want %d", err, copies, 1+copyRetries)
	}
	if entries, err := os.ReadDir(tmp); err != nil || len(entries) != 0 {
		t.Fatalf("temporary directory holds %v, %v after the refusal", entries, err)
	}
	if f.count(proj)+f.count("") != 0 {
		t.Fatal("a refused import wrote cards")
	}
}

// TestOpenUpgradesAV1Board opens a board written before import_refs existed.
func TestOpenUpgradesAV1Board(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	all := migrations
	t.Cleanup(func() { migrations = all })
	migrations = all[:1]
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	card, err := s.Create(context.Background(), owner, NewCard{ProjectID: proj, Kind: KindSubtask, Title: "Before"})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	migrations = all
	s, err = Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	var version string
	if err := s.db.QueryRow(`SELECT v FROM meta WHERE k = 'schema_version'`).Scan(&version); err != nil || version != strconv.Itoa(len(migrations)) {
		t.Fatalf("schema_version = %q, %v, want %d", version, err, len(migrations))
	}
	if got, err := s.Card(context.Background(), card.ID); err != nil || got.Title != "Before" {
		t.Fatalf("card after the upgrade = %+v, %v", got, err)
	}
	r, err := s.Import(context.Background(), sourceCopy(t, "import-v11"), websiteOnly)
	if err != nil || r.Imported != 12 {
		t.Fatalf("import after the upgrade = %+v, %v", r, err)
	}
}

func TestImportKeepsPurgedCardsGone(t *testing.T) {
	f := newFixture(t)
	dir := sourceCopy(t, "import-v11")
	f.importFrom(dir, websiteOnly)
	if n, err := f.s.Purge(f.ctx, owner, proj); err != nil || n != 2 {
		t.Fatalf("purge = %d, %v", n, err)
	}
	src := openSource(t, dir)
	wantReport(t, f.importFrom(dir, websiteOnly), ImportReport{Skipped: []ImportSkip{
		{ID: sourceID(t, src, 5), Reason: `"Old carousel": its card was purged`},
		{ID: sourceID(t, src, 6), Reason: `"Add dark mode": its card was purged`},
	}})
	if f.count(proj) != 5 {
		t.Fatalf("%d cards after re-importing purged ones, want 5", f.count(proj))
	}
}

func TestImportRefusesOtherSchemas(t *testing.T) {
	f := newFixture(t)
	newer := sourceCopy(t, "import-v11")
	sourceExec(t, openSource(t, newer), `UPDATE meta SET v = '12' WHERE k = 'schema_version'`)
	sources := []string{sourceCopy(t, "import-v10"), newer}
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	for _, dir := range sources {
		_, err := f.s.Import(f.ctx, dir, websiteOnly)
		wantCode(t, err, CodeImportSchema)
		if err.Error() != "This board needs kb v1.13.0 or newer: open it once with kb, then import again." {
			t.Fatalf("refusal = %q", err)
		}
	}
	if entries, err := os.ReadDir(tmp); err != nil || len(entries) != 0 {
		t.Fatalf("temporary directory holds %v, %v after refusals", entries, err)
	}
	if f.count(proj)+f.count("") != 0 || len(f.changes) != 0 {
		t.Fatal("a refused import wrote cards")
	}
}

func TestImportRefusesMissingSources(t *testing.T) {
	f := newFixture(t)
	_, err := f.s.Import(f.ctx, "relative/dir", websiteOnly)
	wantCode(t, err, CodeInvalid)
	_, err = f.s.Import(f.ctx, t.TempDir(), websiteOnly)
	wantCode(t, err, CodeNotFound)
	dir := t.TempDir()
	f.must(os.Mkdir(filepath.Join(dir, sourceFile), 0o700))
	_, err = f.s.Import(f.ctx, dir, websiteOnly)
	wantCode(t, err, CodeInvalid)

	// Symbolic links are refused, for the database and for its WAL.
	real := sourceCopy(t, "import-v11")
	linked := t.TempDir()
	f.must(os.Symlink(filepath.Join(real, sourceFile), filepath.Join(linked, sourceFile)))
	walLinked := sourceCopy(t, "import-v11")
	f.must(os.Symlink(filepath.Join(real, sourceFile), filepath.Join(walLinked, sourceFile+"-wal")))
	// A file named as the source directory is refused too.
	for _, dir := range []string{linked, walLinked, filepath.Join(real, sourceFile)} {
		_, err = f.s.Import(f.ctx, dir, websiteOnly)
		wantCode(t, err, CodeInvalid)
	}
	if f.count(proj)+f.count("") != 0 {
		t.Fatal("a refused import wrote cards")
	}
}

func TestImportNeverTouchesTheSource(t *testing.T) {
	f := newFixture(t)
	dir := sourceCopy(t, "import-v11")
	// A running source app holds a change in its WAL, not yet checkpointed.
	src := openSource(t, dir)
	sourceExec(t, src, `UPDATE tasks SET title = 'Build the landing page' WHERE seq = 1`)
	type snapshot struct {
		data []byte
		mod  time.Time
	}
	files := map[string]snapshot{}
	for _, name := range []string{sourceFile, sourceFile + "-wal", sourceFile + "-shm"} {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		f.must(err)
		info, err := os.Stat(path)
		f.must(err)
		files[path] = snapshot{data, info.ModTime()}
	}
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	f.importFrom(dir, websiteOnly)
	f.byTitle(proj, "Build the landing page")
	for path, want := range files {
		data, err := os.ReadFile(path)
		f.must(err)
		info, err := os.Stat(path)
		f.must(err)
		if !bytes.Equal(data, want.data) || !info.ModTime().Equal(want.mod) {
			t.Errorf("%s changed during the import", path)
		}
	}
	if entries, err := os.ReadDir(tmp); err != nil || len(entries) != 0 {
		t.Fatalf("temporary directory holds %v, %v after the import", entries, err)
	}
}
