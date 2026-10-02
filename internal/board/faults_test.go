package board

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// faultScene is one board with something for every write to act on.
type faultScene struct {
	f                                        *fixture
	epic, story, one, two, three, four, five Card
	other, proposed, p2root, l3, big         Card
	r1, r2, split                            Request
	unassigned                               string
}

func newFaultScene(t *testing.T) *faultScene {
	f := newFixture(t)
	s := &faultScene{f: f}
	s.epic, s.story, s.one, s.two = f.tree()
	s.three = f.create(owner, s.story.ID, KindSubtask, "Three")
	s.four = f.create(owner, s.story.ID, KindSubtask, "Four")
	s.five = f.create(owner, s.story.ID, KindSubtask, "Five")
	s.other = f.create(owner, s.epic.ID, KindStory, "Other")
	f.must(f.s.StartPlanning(f.ctx, owner, s.epic.ID, "planner"))
	planner := Agent("planner", "")
	var err error
	s.proposed, err = f.s.Create(f.ctx, planner, NewCard{ProjectID: proj, Kind: KindSubtask, ParentID: s.epic.ID,
		Title: "Proposed", Checklist: []Check{{Text: "a", Done: true}, {Text: "b"}}})
	f.must(err)
	f.launch(s.one.ID, "task-1")
	worker := Agent("task-1", "")
	s.r1 = f.done(s.one.ID, worker)
	s.big = f.create(owner, s.epic.ID, KindSubtask, "Big")
	f.launch(s.big.ID, "task-5")
	_, err = f.s.Split(f.ctx, Agent("task-5", ""), s.big.ID, []SplitChild{{Title: "c1"}})
	f.must(err)
	// A released subtask keeps its split request, which can then be accepted.
	ready := f.create(owner, s.epic.ID, KindSubtask, "Ready to split")
	f.launch(ready.ID, "task-6")
	res, err := f.s.Split(f.ctx, Agent("task-6", ""), ready.ID, []SplitChild{{Title: "r1"}})
	f.must(err)
	s.split = *res.Request
	_, err = f.s.ReleaseHold(f.ctx, owner, ready.ID, ReleaseOwner, "")
	f.must(err)
	s.r2, err = f.s.FileRequest(f.ctx, planner, s.two.ID, RequestInput{Kind: RequestCancel, Comment: "obsolete"})
	f.must(err)
	f.must(f.s.Link(f.ctx, owner, s.two.ID, s.five.ID))
	f.launch(s.four.ID, "task-4")
	_, err = f.s.FileRequest(f.ctx, planner, s.four.ID, RequestInput{Kind: RequestCancel, Comment: "x"})
	f.must(err)
	_, err = f.s.SetStatus(f.ctx, owner, s.four.ID, StatusCancelled, "gone", false)
	f.must(err)
	// p2 holds an expired suggestion; p3 a story one subtask short of done.
	s.p2root, err = f.s.Create(f.ctx, owner, NewCard{ProjectID: "p2", Kind: KindEpic, Title: "P2"})
	f.must(err)
	f.must(f.s.StartPlanning(f.ctx, owner, s.p2root.ID, "planner-2"))
	expiring, err := f.s.Create(f.ctx, Agent("planner-2", ""), NewCard{ProjectID: "p2", Kind: KindStory, ParentID: s.p2root.ID, Title: "Expiring"})
	f.must(err)
	f.raw(`UPDATE cards SET expires_at = '2000-01-01T00:00:00.000000000Z' WHERE id = ?`, expiring.ID)
	s3, err := f.s.Create(f.ctx, owner, NewCard{ProjectID: "p3", Kind: KindStory, Title: "S3"})
	f.must(err)
	s.l3, err = f.s.Create(f.ctx, owner, NewCard{ProjectID: "p3", Kind: KindSubtask, ParentID: s3.ID, Title: "L3"})
	f.must(err)
	f.must(f.s.StartPlanning(f.ctx, owner, s3.ID, "planner-3"))
	_, err = f.s.Create(f.ctx, Agent("planner-3", ""), NewCard{ProjectID: "p3", Kind: KindSubtask, ParentID: s3.ID, Title: "U3"})
	f.must(err)
	s.unassigned = f.unassigned("Loose")
	return s
}

var faultOps = map[string]func(s *faultScene) error{
	"comment": func(s *faultScene) error { return errOf(s.f.s.AddComment(s.f.ctx, owner, s.three.ID, "x")) },
	"release": func(s *faultScene) error {
		return errOf(s.f.s.ReleaseHold(s.f.ctx, owner, s.one.ID, ReleaseOwner, "x"))
	},
	"done": func(s *faultScene) error {
		return errOf(s.f.s.SetStatus(s.f.ctx, owner, s.three.ID, StatusDone, "x", true))
	},
	"cancel": func(s *faultScene) error {
		return errOf(s.f.s.SetStatus(s.f.ctx, owner, s.three.ID, StatusCancelled, "x", false))
	},
	"cascade": func(s *faultScene) error {
		return errOf(s.f.s.SetStatus(s.f.ctx, owner, s.story.ID, StatusCancelled, "x", false))
	},
	"ready": func(s *faultScene) error {
		return errOf(s.f.s.SetStatus(s.f.ctx, owner, s.three.ID, StatusTodo, "x", false))
	},
	"dismiss":      func(s *faultScene) error { return errOf(s.f.s.Dismiss(s.f.ctx, owner, s.proposed.ID)) },
	"restore":      func(s *faultScene) error { return errOf(s.f.s.Restore(s.f.ctx, owner, s.four.ID, "x")) },
	"accept":       func(s *faultScene) error { return errOf(s.f.s.Accept(s.f.ctx, owner, s.r1.ID, "")) },
	"reject":       func(s *faultScene) error { return errOf(s.f.s.Reject(s.f.ctx, owner, s.r1.ID, "x", false)) },
	"acceptCancel": func(s *faultScene) error { return errOf(s.f.s.Accept(s.f.ctx, owner, s.r2.ID, "")) },
	"acceptSplit":  func(s *faultScene) error { return errOf(s.f.s.Accept(s.f.ctx, owner, s.split.ID, "")) },
	"reconcile":    func(s *faultScene) error { return errOf(s.f.s.Reconcile(s.f.ctx, nil, s.f.asOf(), nil)) },
	"sweep":        func(s *faultScene) error { return errOf(s.f.s.Sweep(s.f.ctx)) },
	"sweepOnWrite": func(s *faultScene) error { return errOf(s.f.s.AddComment(s.f.ctx, owner, s.p2root.ID, "x")) },
	"settle": func(s *faultScene) error {
		return errOf(s.f.s.SetStatus(s.f.ctx, owner, s.l3.ID, StatusDone, "x", false))
	},
	"launch": func(s *faultScene) error {
		return errOf(s.f.s.Launch(s.f.ctx, owner, s.three.ID, "task-3", Baseline{}, false))
	},
	"claim": func(s *faultScene) error {
		return errOf(s.f.s.Claim(s.f.ctx, Agent("task-1", ""), s.three.ID, Baseline{}))
	},
	"plan": func(s *faultScene) error { return s.f.s.StartPlanning(s.f.ctx, owner, s.epic.ID, "p9") },
	"create": func(s *faultScene) error {
		return errOf(s.f.s.Create(s.f.ctx, owner, NewCard{ProjectID: proj, Kind: KindSubtask, ParentID: s.story.ID, Title: "New"}))
	},
	"labels": func(s *faultScene) error {
		return errOf(s.f.s.Create(s.f.ctx, owner, NewCard{ProjectID: proj, Kind: KindSubtask, ParentID: s.story.ID, Title: "L", Labels: []string{"l"}}))
	},
	"edit": func(s *faultScene) error {
		return errOf(s.f.s.Edit(s.f.ctx, owner, s.three.ID, Patch{Title: ptr("Renamed")}))
	},
	"move": func(s *faultScene) error { return errOf(s.f.s.Edit(s.f.ctx, owner, s.three.ID, Patch{Rank: ptr(0)})) },
	"change": func(s *faultScene) error {
		return errOf(s.f.s.Edit(s.f.ctx, Agent("task-1", ""), s.one.ID, Patch{Title: ptr("One v2")}))
	},
	"request": func(s *faultScene) error {
		return errOf(s.f.s.FileRequest(s.f.ctx, Agent("planner", ""), s.three.ID, RequestInput{Kind: RequestCancel, Comment: "x"}))
	},
	"requestAgain": func(s *faultScene) error {
		return errOf(s.f.s.FileRequest(s.f.ctx, Agent("planner", ""), s.two.ID, RequestInput{Kind: RequestCancel, Comment: "again"}))
	},
	"split": func(s *faultScene) error {
		return errOf(s.f.s.Split(s.f.ctx, Agent("planner", ""), s.proposed.ID, []SplitChild{{Title: "p1"}}))
	},
	"splitRequest": func(s *faultScene) error {
		return errOf(s.f.s.Split(s.f.ctx, Agent("task-5", ""), s.big.ID, []SplitChild{{Title: "c2"}}))
	},
	"link":     func(s *faultScene) error { return s.f.s.Link(s.f.ctx, owner, s.three.ID, s.five.ID) },
	"unlink":   func(s *faultScene) error { return s.f.s.Unlink(s.f.ctx, owner, s.two.ID, s.five.ID) },
	"purge":    func(s *faultScene) error { return errOf(s.f.s.Purge(s.f.ctx, owner, proj)) },
	"settings": func(s *faultScene) error { return s.f.s.SetProjectAcceptCmd(s.f.ctx, owner, proj, "x") },
	"moveIn": func(s *faultScene) error {
		return errOf(s.f.s.Edit(s.f.ctx, owner, s.unassigned, Patch{ProjectID: ptr(proj)}))
	},
	"confirm": func(s *faultScene) error { return errOf(s.f.s.Confirm(s.f.ctx, owner, s.proposed.ID)) },
	"checklist": func(s *faultScene) error {
		return errOf(s.f.s.Checklist(s.f.ctx, owner, s.three.ID, ChecklistEdit{Add: []string{"x"}}))
	},
}

// Every write fails as a whole when one of its statements fails: the
// trigger aborts the statement and nothing of the write is committed.
func TestInjectedWriteFaults(t *testing.T) {
	for _, tc := range []struct {
		trigger string
		ops     []string
	}{
		{"BEFORE INSERT ON comments", []string{"comment", "release", "done", "cancel", "cascade", "ready", "dismiss", "restore",
			"accept", "reject", "acceptCancel", "reconcile", "sweep", "sweepOnWrite", "settle"}},
		{"BEFORE INSERT ON comments WHEN new.automatic = 1 AND new.close = 0", []string{"cascade", "dismiss", "reconcile", "sweep", "settle"}},
		{"BEFORE INSERT ON comments WHEN new.automatic = 1 AND new.close = 1", []string{"settle"}},
		{"BEFORE INSERT ON holds", []string{"launch", "claim"}},
		{"BEFORE UPDATE ON holds", []string{"release", "accept", "reject", "reconcile", "cascade"}},
		{"BEFORE INSERT ON requests", []string{"request", "change", "split", "splitRequest"}},
		{"BEFORE UPDATE ON requests", []string{"accept", "reject", "acceptCancel", "requestAgain", "splitRequest", "cascade"}},
		{"BEFORE INSERT ON cards", []string{"create", "split", "acceptSplit", "labels"}},
		{"BEFORE UPDATE OF status ON cards", []string{"done", "cancel", "cascade", "launch", "claim", "release", "dismiss",
			"restore", "accept", "reject", "reconcile", "sweep", "sweepOnWrite", "split", "acceptCancel", "acceptSplit", "ready"}},
		{"BEFORE UPDATE OF status ON cards WHEN new.status = 'cancelled' AND old.expires_at IS NOT NULL", []string{"settle"}},
		{"BEFORE UPDATE OF title ON cards", []string{"edit", "confirm", "checklist", "accept", "launch", "moveIn"}},
		{"BEFORE UPDATE OF rank ON cards", []string{"move"}},
		{"BEFORE UPDATE OF revision ON cards", []string{"comment", "edit"}},
		{"BEFORE UPDATE OF project_id ON cards", []string{"moveIn"}},
		{"BEFORE DELETE ON comments", []string{"purge"}},
		{"BEFORE DELETE ON requests", []string{"purge"}},
		{"BEFORE DELETE ON holds", []string{"purge"}},
		{"BEFORE DELETE ON cards", []string{"purge"}},
		{"BEFORE DELETE ON links", []string{"unlink"}},
		{"BEFORE INSERT ON links", []string{"link"}},
		{"BEFORE INSERT ON labels", []string{"labels"}},
		{"BEFORE UPDATE ON sequences", []string{"create"}},
		{"BEFORE UPDATE ON revisions", []string{"comment"}},
		{"BEFORE INSERT ON scopes", []string{"plan", "launch"}},
		{"BEFORE INSERT ON project_settings", []string{"settings"}},
	} {
		t.Run(tc.trigger, func(t *testing.T) {
			t.Parallel()
			s := newFaultScene(t)
			before := s.f.revision()
			s.f.raw(`CREATE TRIGGER injected ` + tc.trigger + ` BEGIN SELECT RAISE(ABORT, 'injected'); END`)
			for _, name := range tc.ops {
				if err := faultOps[name](s); err == nil || !strings.Contains(err.Error(), "injected") {
					t.Errorf("%s: error = %v, want the injected fault", name, err)
				}
			}
			if after := s.f.revision(); after != before {
				t.Errorf("failed writes moved the revision from %d to %d", before, after)
			}
		})
	}
}

// Rows that no longer decode fail the reads and writes that load them.
func TestCorruptRows(t *testing.T) {
	reads := map[string]func(s *faultScene) error{
		"board":      func(s *faultScene) error { return errOf(s.f.s.Board(s.f.ctx, proj)) },
		"card":       func(s *faultScene) error { return errOf(s.f.s.Card(s.f.ctx, s.one.ID)) },
		"cards":      func(s *faultScene) error { return errOf(s.f.s.Cards(s.f.ctx, []string{s.one.ID})) },
		"list":       func(s *faultScene) error { return errOf(s.f.s.List(s.f.ctx, proj, Filter{})) },
		"stale":      func(s *faultScene) error { return errOf(s.f.s.StaleCandidates(s.f.ctx, proj)) },
		"similar":    func(s *faultScene) error { return errOf(s.f.s.Similar(s.f.ctx, proj, "Three", "", 3)) },
		"pending":    func(s *faultScene) error { return errOf(s.f.s.PendingLeaves(s.f.ctx, s.story.ID)) },
		"finish":     func(s *faultScene) error { return errOf(s.f.s.CheckFinishable(s.f.ctx, owner, s.one.ID)) },
		"detail":     func(s *faultScene) error { return errOf(s.f.s.Detail(s.f.ctx, s.one.ID)) },
		"detail4":    func(s *faultScene) error { return errOf(s.f.s.Detail(s.f.ctx, s.four.ID)) },
		"getRequest": func(s *faultScene) error { return errOf(s.f.s.Request(s.f.ctx, s.r1.ID)) },
		"reconcile":  func(s *faultScene) error { return errOf(s.f.s.Reconcile(s.f.ctx, nil, s.f.asOf(), nil)) },
		"sweep":      func(s *faultScene) error { return errOf(s.f.s.Sweep(s.f.ctx)) },
		"purge":      func(s *faultScene) error { return errOf(s.f.s.Purge(s.f.ctx, owner, proj)) },
		"unlink":     func(s *faultScene) error { return s.f.s.Unlink(s.f.ctx, owner, s.two.ID, s.five.ID) },
		"settings":   func(s *faultScene) error { return errOf(s.f.s.ProjectSettings(s.f.ctx, proj)) },
	}
	all := []string{"board", "card", "cards", "list", "stale", "similar", "pending", "finish", "detail", "reconcile", "purge"}
	writes := []string{"comment", "edit", "create", "launch", "claim", "dismiss", "restore", "accept", "reject", "split",
		"splitRequest", "request", "link", "checklist", "confirm", "cascade", "plan", "moveIn"}
	for _, tc := range []struct {
		name, sql string
		arg       func(s *faultScene) string
		ops       []string
	}{
		{"card labels", `UPDATE cards SET labels = 'x' WHERE id = ?`, func(s *faultScene) string { return s.three.ID }, append(all, writes...)},
		{"card expiry", `UPDATE cards SET expires_at = 'x' WHERE id = ?`, func(s *faultScene) string { return s.proposed.ID }, all},
		{"card stamps", `UPDATE cards SET moved_at = 'x' WHERE id = ?`, func(s *faultScene) string { return s.two.ID }, all},
		{"request flags", `UPDATE requests SET flags = 'x' WHERE id = ?`, func(s *faultScene) string { return s.r1.ID }, []string{"board", "getRequest", "detail", "accept"}},
		{"request created", `UPDATE requests SET created_at = 'x' WHERE id = ?`, func(s *faultScene) string { return s.r1.ID }, []string{"getRequest"}},
		{"request decided", `UPDATE requests SET decided_at = 'x' WHERE id = ?`, func(s *faultScene) string { return s.r1.ID }, []string{"getRequest"}},
		{"request payload", `UPDATE requests SET payload = 'x' WHERE id = ?`, func(s *faultScene) string { return s.r1.ID }, []string{"accept"}},
		{"hold baseline", `UPDATE holds SET baseline_status = 'x' WHERE card_id <> ?`, func(s *faultScene) string { return "" }, []string{"detail", "reconcile"}},
		{"hold started", `UPDATE holds SET started_at = 'x' WHERE card_id = ?`, func(s *faultScene) string { return s.one.ID }, []string{"detail"}},
		{"hold ended", `UPDATE holds SET ended_at = 'x' WHERE card_id = ?`, func(s *faultScene) string { return s.four.ID }, []string{"detail4"}},
		{"comment stamp", `UPDATE comments SET created_at = 'x' WHERE card_id = ?`, func(s *faultScene) string { return s.four.ID }, []string{"detail4"}},
		{"cross-project card", `UPDATE cards SET labels = 'x' WHERE id = ?`, func(s *faultScene) string { return s.p2root.ID }, []string{"sweep"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newFaultScene(t)
			s.f.raw(tc.sql, tc.arg(s))
			for _, name := range tc.ops {
				op := reads[name]
				if op == nil {
					op = faultOps[name]
				}
				if err := op(s); err == nil {
					t.Errorf("%s succeeded over a corrupt row", name)
				}
			}
		})
	}
	s := newFaultScene(t)
	if _, err := s.f.s.db.Exec(`DROP TABLE project_settings`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"settings", "finish"} {
		if err := reads[name](s); err == nil {
			t.Errorf("%s succeeded without its table", name)
		}
	}
	if _, err := s.f.s.db.Exec(`DROP TABLE links`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"unlink", "board"} {
		if err := reads[name](s); err == nil {
			t.Errorf("%s succeeded without its table", name)
		}
	}
	if err := faultOps["link"](s); err == nil {
		t.Error("link succeeded without its table")
	}
}

// A card linked to a blocker in another Project reads that Project's
// derived status.
func TestCrossProjectBlocker(t *testing.T) {
	f := newFixture(t)
	a := f.unassigned("Blocked")
	b := f.unassigned("Blocker")
	f.raw(`INSERT INTO links (blocker_id, blocked_id) VALUES (?, ?)`, b, a)
	_, err := f.s.Edit(f.ctx, owner, a, Patch{ProjectID: ptr(proj)})
	f.must(err)
	_, err = f.s.CheckFinishable(f.ctx, owner, a)
	wantCode(t, err, CodeGuardBlockers)
	f.raw(`UPDATE cards SET status = 'done' WHERE id = ?`, b)
	if _, err := f.s.CheckFinishable(f.ctx, owner, a); err != nil {
		t.Fatalf("a done blocker elsewhere still blocks: %v", err)
	}
	f.raw(`INSERT INTO links (blocker_id, blocked_id) VALUES ('gone', ?)`, a)
	if _, err := f.s.CheckFinishable(f.ctx, owner, a); err != nil {
		t.Fatalf("a missing blocker blocks: %v", err)
	}
}

func TestOpenFailures(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir, Options{}); err == nil {
		t.Fatal("opened a directory")
	}
	path := filepath.Join(dir, "conflict.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE cards (x)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if _, err := Open(path, Options{}); err == nil || !strings.Contains(err.Error(), "migrate to v1") {
		t.Fatalf("Open over a conflicting table: %v", err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(dir, 0o700) }()
	if os.Getuid() != 0 {
		if _, err := Open(filepath.Join(dir, "new.db"), Options{}); err == nil {
			t.Fatal("created a database in a read-only directory")
		}
	}
}
