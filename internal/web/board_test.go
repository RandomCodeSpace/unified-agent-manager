package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/board"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// plannerFixture is a server with the planner on and a Project in a Git
// repository.
type plannerFixture struct {
	t       *testing.T
	ts      *testServer
	m       *Manager
	repo    string
	project string
}

func newPlanner(t *testing.T) *plannerFixture {
	t.Helper()
	ts := newTestServer(t, ServerConfig{})
	repo := branchRepo(t)
	f := &plannerFixture{t: t, ts: ts, m: ts.m, repo: repo, project: addProject(t, ts.m, repo)}
	f.call(http.MethodPatch, "/api/settings", `{"planner":true}`, http.StatusOK, nil)
	return f
}

// do sends one signed-in request.
func (f *plannerFixture) do(method, target, body string) *httptest.ResponseRecorder {
	return f.ts.do(method, target, body, withCookie(f.ts))
}

// call sends one request, requires the status, and decodes the reply into
// out when it is not nil.
func (f *plannerFixture) call(method, target, body string, want int, out any) {
	f.t.Helper()
	w := f.do(method, target, body)
	if w.Code != want {
		f.t.Fatalf("%s %s = %d %s, want %d", method, target, w.Code, w.Body, want)
	}
	if out != nil {
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			f.t.Fatalf("%s %s reply %s: %v", method, target, w.Body, err)
		}
	}
}

// refused sends one request and requires the status and error code.
func (f *plannerFixture) refused(method, target, body string, status int, code string) map[string]json.RawMessage {
	f.t.Helper()
	w := f.do(method, target, body)
	var reply map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
		f.t.Fatalf("%s %s reply %s: %v", method, target, w.Body, err)
	}
	var got string
	_ = json.Unmarshal(reply["code"], &got)
	if w.Code != status || got != code || len(reply["error"]) < 3 {
		f.t.Fatalf("%s %s = %d %s, want %d code %q", method, target, w.Code, w.Body, status, code)
	}
	return reply
}

func (f *plannerFixture) create(kind board.Kind, parent, title string, extra ...string) BoardCard {
	f.t.Helper()
	parentJSON := "null"
	if parent != "" {
		parentJSON = fmt.Sprintf("%q", parent)
	}
	body := fmt.Sprintf(`{"project_id":%q,"kind":%q,"parent_id":%s,"title":%q%s}`, f.project, kind, parentJSON, title, strings.Join(extra, ""))
	var c BoardCard
	f.call(http.MethodPost, "/api/board/cards", body, http.StatusCreated, &c)
	return c
}

func (f *plannerFixture) card(ref string) BoardCardDetail {
	f.t.Helper()
	var d BoardCardDetail
	f.call(http.MethodGet, "/api/board/cards/"+ref, "", http.StatusOK, &d)
	return d
}

func (f *plannerFixture) launch(ref string) (BoardCard, SessionSummary) {
	f.t.Helper()
	var reply struct {
		Card    BoardCard      `json:"card"`
		Session SessionSummary `json:"session"`
	}
	f.call(http.MethodPost, "/api/board/cards/"+ref+"/launch", `{}`, http.StatusCreated, &reply)
	return reply.Card, reply.Session
}

// store runs fn on the open planner store, as an agent tool or the owner
// would outside the API.
func (f *plannerFixture) store(fn func(context.Context, *board.Store) error) {
	f.t.Helper()
	if err := f.m.withBoard(func(st *board.Store) error { return fn(context.Background(), st) }); err != nil {
		f.t.Fatal(err)
	}
}

func (f *plannerFixture) conversation(id string) *agenttest.Conversation {
	f.t.Helper()
	for _, c := range f.ts.prov.Conversations() {
		if c.Request().SessionID == id {
			return c
		}
	}
	f.t.Fatalf("no conversation for task %s", id)
	return nil
}

func comments(d BoardCardDetail) []string {
	var out []string
	for _, c := range d.Comments {
		out = append(out, c.Author+": "+c.Body)
	}
	return out
}

func TestPlannerSwitch(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	repo := branchRepo(t)
	project := addProject(t, ts.m, repo)
	auth := withCookie(ts)
	f := &plannerFixture{t: t, ts: ts, m: ts.m, repo: repo, project: project}
	f.refused(http.MethodGet, "/api/board?project_id="+project, "", http.StatusConflict, codePlannerOff)
	f.refused(http.MethodPost, "/api/board/cards", `{"project_id":"`+project+`","kind":"epic","title":"E"}`, http.StatusConflict, codePlannerOff)
	if w := ts.do(http.MethodPatch, "/api/settings", `{"planner":"yes"}`, auth); w.Code != http.StatusBadRequest {
		t.Fatalf("planner yes = %d %s", w.Code, w.Body)
	}
	path := filepath.Join(filepath.Dir(ts.m.store.Path()), board.FileName)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("board.db exists before the switch is on: %v", err)
	}
	sub, _, err := ts.m.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	var settings Settings
	f.call(http.MethodPatch, "/api/settings", `{"planner":true}`, http.StatusOK, &settings)
	if !settings.Planner || !ts.m.Settings().Planner {
		t.Fatalf("settings after on = %+v", settings)
	}
	if frame := frameOf(t, sub, "settings"); !strings.Contains(string(frame.data["settings"]), `"planner":true`) {
		t.Fatalf("settings frame = %s", frame.data["settings"])
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("board.db = %v, %v; want mode 0600 beside sessions.json", info, err)
	}
	var snap BoardSnapshot
	f.call(http.MethodGet, "/api/board?project_id="+project, "", http.StatusOK, &snap)
	if len(snap.Cards) != 0 || snap.Revision != 0 {
		t.Fatalf("empty board = %+v", snap)
	}
	if w := ts.do(http.MethodGet, "/api/board?project_id="+project, "", auth); !strings.Contains(w.Body.String(), `"cards":[]`) {
		t.Fatalf("empty board JSON = %s", w.Body)
	}
	f.create(board.KindEpic, "", "Epic")
	cfg, err := ts.m.store.Load()
	if err != nil || !cfg.WebSettings.Planner {
		t.Fatalf("stored planner = %v, %v", cfg.WebSettings.Planner, err)
	}

	// Off closes the store; the routes refuse and the cards stay on disk.
	f.call(http.MethodPatch, "/api/settings", `{"planner":false}`, http.StatusOK, &settings)
	if settings.Planner {
		t.Fatal("planner still on")
	}
	f.refused(http.MethodGet, "/api/board?project_id="+project, "", http.StatusConflict, codePlannerOff)
	f.refused(http.MethodGet, "/api/board/cards/%231", "", http.StatusConflict, codePlannerOff)
	f.call(http.MethodPatch, "/api/settings", `{"planner":true}`, http.StatusOK, nil)
	f.call(http.MethodGet, "/api/board?project_id="+project, "", http.StatusOK, &snap)
	if len(snap.Cards) != 1 || snap.Cards[0].Title != "Epic" {
		t.Fatalf("board after off and on = %+v", snap)
	}
	// An unchanged switch writes nothing and keeps the store.
	f.call(http.MethodPatch, "/api/settings", `{"planner":true}`, http.StatusOK, nil)
	f.call(http.MethodGet, "/api/board?project_id="+project, "", http.StatusOK, &snap)
}

func TestPlannerBoardFrames(t *testing.T) {
	f := newPlanner(t)
	sub, _, err := f.m.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	epic := f.create(board.KindEpic, "", "Epic")
	fr := frameOf(t, sub, "board")
	var ev boardEvent
	raw, _ := json.Marshal(fr.data)
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatal(err)
	}
	if ev.ProjectID != f.project || ev.Revision != 1 || len(ev.Cards) != 1 || ev.Cards[0].ID != epic.ID || !ev.Cards[0].Confirmed ||
		ev.Cards[0].Revision != 1 || len(ev.Removed) != 0 || len(ev.Requests) != 0 || fr.seq == 0 {
		t.Fatalf("board frame = %+v", ev)
	}
	story := f.create(board.KindStory, epic.ID, "Story")
	fr = frameOf(t, sub, "board")
	raw, _ = json.Marshal(fr.data)
	_ = json.Unmarshal(raw, &ev)
	// The frame carries the new card and its ancestors, whose derived state changed.
	ids := []string{ev.Cards[0].ID, ev.Cards[len(ev.Cards)-1].ID}
	slices.Sort(ids)
	wantIDs := []string{epic.ID, story.ID}
	slices.Sort(wantIDs)
	if ev.Revision != 2 || len(ev.Cards) != 2 || !slices.Equal(ids, wantIDs) {
		t.Fatalf("second frame = %+v", ev)
	}
	// Every write the UI makes produces a frame, including links and comments.
	one := f.create(board.KindSubtask, story.ID, "One")
	two := f.create(board.KindSubtask, story.ID, "Two")
	frameOf(t, sub, "board")
	frameOf(t, sub, "board")
	f.call(http.MethodPost, "/api/board/links", fmt.Sprintf(`{"blocker":%q,"blocked":%q}`, one.ID, two.ID), http.StatusNoContent, nil)
	if fr = frameOf(t, sub, "board"); !strings.Contains(string(fr.data["cards"]), one.ID) {
		t.Fatalf("link frame = %s", fr.data["cards"])
	}
	f.call(http.MethodPost, "/api/board/cards/"+two.ID+"/comments", `{"body":"note"}`, http.StatusCreated, nil)
	frameOf(t, sub, "board")
}

// The snapshot carries each Board's revision while the planner is on, so a
// browser reloads only the Boards it is behind on.
func TestPlannerSnapshotBoards(t *testing.T) {
	f := newPlanner(t)
	boards := func() json.RawMessage {
		t.Helper()
		sub, snap, err := f.m.Subscribe("")
		if err != nil {
			t.Fatal(err)
		}
		f.m.Unsubscribe(sub)
		return parseFrame(t, snap).data["boards"]
	}
	if got, want := string(boards()), fmt.Sprintf(`{"":0,%q:0}`, f.project); got != want {
		t.Fatalf("boards = %s, want %s", got, want)
	}
	f.create(board.KindEpic, "", "Epic")
	f.create(board.KindEpic, "", "Other")
	other := addProject(t, f.m, t.TempDir())
	var revs map[string]int64
	if err := json.Unmarshal(boards(), &revs); err != nil || len(revs) != 3 || revs[f.project] != 2 || revs[""] != 0 || revs[other] != 0 {
		t.Fatalf("boards after two writes = %v, %v", revs, err)
	}
	// Reopening the store reads the revisions back.
	f.call(http.MethodPatch, "/api/settings", `{"planner":false}`, http.StatusOK, nil)
	if raw := boards(); raw != nil {
		t.Fatalf("boards while off = %s", raw)
	}
	f.call(http.MethodPatch, "/api/settings", `{"planner":true}`, http.StatusOK, nil)
	if err := json.Unmarshal(boards(), &revs); err != nil || revs[f.project] != 2 {
		t.Fatalf("boards after reopening = %v, %v", revs, err)
	}
}

func TestPlannerCardRoutes(t *testing.T) {
	f := newPlanner(t)
	epic := f.create(board.KindEpic, "", "Epic", `,"desc":"why","win_condition":"shipped","prio":1,"effort":"L","due":"2026-10-01","labels":["web"]`)
	if epic.Seq != 1 || epic.Desc != "why" || epic.WinCondition != "shipped" || epic.Prio != 1 || epic.Effort != "L" || epic.Due != "2026-10-01" ||
		!slices.Equal(epic.Labels, []string{"web"}) || epic.ParentID != nil || !epic.Confirmed || epic.ExpiresAt != nil ||
		epic.PinnedSHA != gitOutput(t, f.repo, "rev-parse", "HEAD") || epic.Progress == nil || epic.Status != board.StatusPlanned {
		t.Fatalf("created epic = %+v", epic)
	}
	story := f.create(board.KindStory, epic.ID, "Story")
	one := f.create(board.KindSubtask, story.ID, "One", `,"checklist":[{"text":"a","done":false}]`)
	two := f.create(board.KindSubtask, story.ID, "Two")
	if one.ProjectID != f.project || *one.ParentID != story.ID || one.Progress != nil || len(one.Checklist) != 1 {
		t.Fatalf("created subtask = %+v", one)
	}
	f.refused(http.MethodPost, "/api/board/cards", fmt.Sprintf(`{"project_id":%q,"kind":"subtask","parent_id":%q,"title":"one"}`, f.project, story.ID), http.StatusConflict, string(board.CodeDuplicate))
	f.refused(http.MethodPost, "/api/board/cards", fmt.Sprintf(`{"project_id":%q,"kind":"task","title":"x"}`, f.project), http.StatusBadRequest, string(board.CodeInvalid))

	// The owner's edit: accept_cmd null inherits, "" is none, a string runs.
	var c BoardCard
	f.call(http.MethodPatch, "/api/board/cards/"+one.ID, `{"title":"One!","accept_cmd":"go test ./...","paths":["internal/*"],"blocked":true}`, http.StatusOK, &c)
	if c.Title != "One!" || c.AcceptCmd == nil || *c.AcceptCmd != "go test ./..." || !slices.Equal(c.Paths, []string{"internal/*"}) || !c.Blocked {
		t.Fatalf("edited = %+v", c)
	}
	f.call(http.MethodPatch, "/api/board/cards/"+one.ID, `{"accept_cmd":"","blocked":false}`, http.StatusOK, &c)
	if c.AcceptCmd == nil || *c.AcceptCmd != "" {
		t.Fatalf("accept_cmd none = %v", c.AcceptCmd)
	}
	if w := f.do(http.MethodPatch, "/api/board/cards/"+one.ID, `{"accept_cmd":null}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"accept_cmd":null`) {
		t.Fatalf("accept_cmd inherit = %d %s", w.Code, w.Body)
	}
	f.refused(http.MethodPatch, "/api/board/cards/"+one.ID, `{"accept_cmd":1}`, http.StatusBadRequest, string(board.CodeInvalid))
	f.refused(http.MethodPatch, "/api/board/cards/"+one.ID, `{"parent_id":1}`, http.StatusBadRequest, string(board.CodeInvalid))
	f.refused(http.MethodPatch, "/api/board/cards/"+one.ID, `{}`, http.StatusBadRequest, string(board.CodeInvalid))

	// Move by #seq, to the root and back under the story at rank 0.
	f.call(http.MethodPost, "/api/board/cards/%23"+fmt.Sprint(two.Seq)+"/move", `{"parent_id":null,"rank":0}`, http.StatusOK, &c)
	if c.ParentID != nil || c.Rank != 0 {
		t.Fatalf("moved to the root = %+v", c)
	}
	f.call(http.MethodPost, "/api/board/cards/"+two.ID+"/move", fmt.Sprintf(`{"parent_id":%q,"rank":0}`, story.ID), http.StatusOK, &c)
	if *c.ParentID != story.ID || c.Rank != 0 {
		t.Fatalf("moved back = %+v", c)
	}
	f.refused(http.MethodPost, "/api/board/cards/"+two.ID+"/move", `{"parent_id":7}`, http.StatusBadRequest, string(board.CodeInvalid))

	// The finishing guard, force, and the comment rule.
	reply := f.refused(http.MethodPost, "/api/board/cards/"+one.ID+"/status", `{"status":"done","comment":"ok"}`, http.StatusConflict, string(board.CodeGuardOpenItems))
	if string(reply["refs"]) != `["a"]` {
		t.Fatalf("guard refs = %s", reply["refs"])
	}
	f.refused(http.MethodPost, "/api/board/cards/"+one.ID+"/status", `{"status":"done"}`, http.StatusBadRequest, string(board.CodeInvalid))
	f.refused(http.MethodPost, "/api/board/cards/"+one.ID+"/status", `{"status":"doing","comment":"x"}`, http.StatusBadRequest, string(board.CodeInvalid))
	f.call(http.MethodPost, "/api/board/cards/"+one.ID+"/status", `{"status":"done","comment":"ok","force":true}`, http.StatusOK, &c)
	if c.Status != board.StatusDone {
		t.Fatalf("forced done = %+v", c)
	}
	f.call(http.MethodPost, "/api/board/cards/"+one.ID+"/status", `{"status":"todo"}`, http.StatusOK, &c)
	if c.Status != board.StatusTodo {
		t.Fatalf("ready = %+v", c)
	}

	// Cancel the story as a cascade, then restore it with a comment.
	f.call(http.MethodPost, "/api/board/cards/"+story.ID+"/status", `{"status":"cancelled","comment":"not now"}`, http.StatusOK, &c)
	if c.Status != board.StatusCancelled || f.card(two.ID).Card.Status != board.StatusCancelled {
		t.Fatalf("cascade = %+v", c)
	}
	f.refused(http.MethodPost, "/api/board/cards/"+story.ID+"/restore", `{}`, http.StatusBadRequest, string(board.CodeInvalid))
	f.call(http.MethodPost, "/api/board/cards/"+story.ID+"/restore", `{"comment":"now"}`, http.StatusOK, &c)
	if c.Status != board.StatusPlanned || f.card(two.ID).Card.Status != board.StatusPlanned {
		t.Fatalf("restored = %+v", c)
	}

	// Split, comments, confirm (a re-pin), links and the detail.
	whole := f.create(board.KindSubtask, epic.ID, "Whole")
	f.call(http.MethodPost, "/api/board/cards/"+whole.ID+"/split", `{"children":[{"title":"Part a","win_condition":"a"},{"title":"Part b"}]}`, http.StatusOK, &c)
	if c.Kind != board.KindStory || c.ID != whole.ID {
		t.Fatalf("split = %+v", c)
	}
	var comment BoardComment
	f.call(http.MethodPost, "/api/board/cards/"+two.ID+"/comments", `{"body":"hello"}`, http.StatusCreated, &comment)
	if comment.ID == "" || comment.Author != board.AuthorOwner || comment.Body != "hello" || comment.Automatic {
		t.Fatalf("comment = %+v", comment)
	}
	f.refused(http.MethodPost, "/api/board/cards/"+two.ID+"/comments", `{"body":" "}`, http.StatusBadRequest, string(board.CodeInvalid))
	gitIn(t, f.repo, "commit", "-q", "--allow-empty", "-m", "next")
	f.call(http.MethodPost, "/api/board/cards/"+epic.ID+"/confirm", ``, http.StatusOK, &c)
	if head := gitOutput(t, f.repo, "rev-parse", "HEAD"); c.PinnedSHA != head {
		t.Fatalf("re-pin = %s, want %s", c.PinnedSHA, head)
	}
	f.call(http.MethodPost, "/api/board/links", fmt.Sprintf(`{"blocker":%q,"blocked":%q}`, epic.ID, one.ID), http.StatusNoContent, nil)
	if d := f.card(one.ID); !slices.Equal(d.Card.BlockedBy, []string{epic.ID}) {
		t.Fatalf("blocked_by = %v", d.Card.BlockedBy)
	}
	f.refused(http.MethodPost, "/api/board/links", fmt.Sprintf(`{"blocker":%q,"blocked":%q}`, epic.ID, one.ID), http.StatusConflict, string(board.CodeDuplicate))
	f.refused(http.MethodPost, "/api/board/links", `{"blocker":"","blocked":"x"}`, http.StatusBadRequest, string(board.CodeInvalid))
	f.call(http.MethodDelete, "/api/board/links?blocker="+epic.ID+"&blocked="+one.ID, "", http.StatusNoContent, nil)
	f.refused(http.MethodDelete, "/api/board/links?blocker="+epic.ID+"&blocked="+one.ID, "", http.StatusNotFound, string(board.CodeNotFound))
	d := f.card("%23" + fmt.Sprint(two.Seq))
	if d.Card.ID != two.ID || !slices.Contains(comments(d), "owner: hello") || d.Holds == nil || d.Requests == nil {
		t.Fatalf("detail = %+v", d)
	}
	f.refused(http.MethodGet, "/api/board/cards/%23999", "", http.StatusNotFound, string(board.CodeNotFound))

	// Dismiss a proposal an agent made, then purge what is cancelled.
	f.store(func(ctx context.Context, st *board.Store) error {
		return st.StartPlanning(ctx, board.Owner(""), epic.ID, "planner")
	})
	var proposal board.Card
	f.store(func(ctx context.Context, st *board.Store) error {
		var err error
		proposal, err = st.Create(ctx, board.Agent("planner", ""), board.NewCard{Kind: board.KindStory, ParentID: epic.ID, Title: "Proposal"})
		return err
	})
	f.call(http.MethodPost, "/api/board/cards/"+proposal.ID+"/dismiss", `{}`, http.StatusOK, &c)
	if c.Status != board.StatusCancelled {
		t.Fatalf("dismissed = %+v", c)
	}
	var purged map[string]int
	f.call(http.MethodPost, "/api/board/purge", fmt.Sprintf(`{"project_id":%q}`, f.project), http.StatusOK, &purged)
	if purged["purged"] != 1 {
		t.Fatalf("purged = %v", purged)
	}
	f.refused(http.MethodPost, "/api/board/purge", `{}`, http.StatusBadRequest, string(board.CodeInvalid))
	f.refused(http.MethodPost, "/api/board/purge", `{"project_id":"gone"}`, http.StatusNotFound, "")
	var snap BoardSnapshot
	f.call(http.MethodGet, "/api/board?project_id="+f.project, "", http.StatusOK, &snap)
	if len(snap.Cards) != 7 || snap.Revision == 0 {
		t.Fatalf("board = %d cards, revision %d", len(snap.Cards), snap.Revision)
	}
	f.refused(http.MethodGet, "/api/board", "", http.StatusBadRequest, string(board.CodeInvalid))
	f.refused(http.MethodGet, "/api/board?project_id=gone", "", http.StatusNotFound, "")
	// check, triage, suggest and import come with later features.
	for _, action := range []string{"check", "triage", "suggest"} {
		if w := f.do(http.MethodPost, "/api/board/cards/"+one.ID+"/"+action, `{}`); w.Code != http.StatusNotFound {
			t.Fatalf("%s = %d", action, w.Code)
		}
	}
	if w := f.do(http.MethodPost, "/api/board/import", `{"dir":"/x"}`); w.Code != http.StatusNotFound {
		t.Fatalf("import = %d", w.Code)
	}
}

func TestPlannerProjectSettings(t *testing.T) {
	f := newPlanner(t)
	var p BoardProject
	f.call(http.MethodGet, "/api/board/projects/"+f.project, "", http.StatusOK, &p)
	if p != (BoardProject{}) {
		t.Fatalf("project settings = %+v", p)
	}
	f.call(http.MethodPatch, "/api/board/projects/"+f.project, `{"accept_cmd":"make test"}`, http.StatusOK, &p)
	if p.AcceptCmd != "make test" {
		t.Fatalf("patched = %+v", p)
	}
	f.call(http.MethodGet, "/api/board/projects/"+f.project, "", http.StatusOK, &p)
	if p.AcceptCmd != "make test" || p.Git != "" {
		t.Fatalf("project settings = %+v", p)
	}
	f.refused(http.MethodPatch, "/api/board/projects/"+f.project, `{}`, http.StatusBadRequest, string(board.CodeInvalid))
	f.refused(http.MethodGet, "/api/board/projects/gone", "", http.StatusNotFound, "")
	plain := addProject(t, f.m, t.TempDir())
	f.call(http.MethodGet, "/api/board/projects/"+plain, "", http.StatusOK, &p)
	if p.Git != noGitRepository {
		t.Fatalf("plain project git = %q", p.Git)
	}
}

func TestPlannerRefusesProjectsWithoutGit(t *testing.T) {
	f := newPlanner(t)
	plain := addProject(t, f.m, t.TempDir())
	f.refused(http.MethodGet, "/api/board?project_id="+plain, "", http.StatusConflict, codeNoGit)
	f.refused(http.MethodPost, "/api/board/cards", fmt.Sprintf(`{"project_id":%q,"kind":"epic","title":"E"}`, plain), http.StatusConflict, codeNoGit)
	f.refused(http.MethodPatch, "/api/board/projects/"+plain, `{"accept_cmd":""}`, http.StatusConflict, codeNoGit)
	f.refused(http.MethodPost, "/api/board/purge", fmt.Sprintf(`{"project_id":%q}`, plain), http.StatusConflict, codeNoGit)
	// A card that reached the Project anyway can't be written or launched.
	var c board.Card
	f.store(func(ctx context.Context, st *board.Store) error {
		var err error
		c, err = st.Create(ctx, board.Owner(""), board.NewCard{ProjectID: plain, Kind: board.KindSubtask, Title: "Leaf"})
		return err
	})
	f.refused(http.MethodPost, "/api/board/cards/"+c.ID+"/confirm", `{}`, http.StatusConflict, codeNoGit)
	f.refused(http.MethodPost, "/api/board/cards/"+c.ID+"/launch", `{}`, http.StatusConflict, codeNoGit)
	if len(f.m.List()) != 0 {
		t.Fatal("a refused launch created a task")
	}
	if e := noGitError(noGitInstalled); e.Code != codeNoGit || !strings.Contains(e.Message, "not installed") {
		t.Fatalf("not installed = %+v", e)
	}
}

func TestPlannerLaunchHoldsAndSendsThePreamble(t *testing.T) {
	f := newPlanner(t)
	epic := f.create(board.KindEpic, "", "Epic")
	story := f.create(board.KindStory, epic.ID, "Story", `,"win_condition":"story done"`)
	one := f.create(board.KindSubtask, story.ID, "One", `,"win_condition":"one passes","desc":"Do the thing.","checklist":[{"text":"write it","done":false}]`)
	writeRepoFile(t, f.repo, "dirty.txt", "x")

	held, task := f.launch(one.ID)
	if held.ID != one.ID || held.Status != board.StatusDoing || held.HeldBy != task.ID || task.Name != "#3 One" || task.ProjectID != f.project {
		t.Fatalf("launch = %+v, %+v", held, task)
	}
	d := f.card(one.ID)
	head := gitOutput(t, f.repo, "rev-parse", "HEAD")
	if len(d.Holds) != 1 || d.Holds[0].TaskID != task.ID || d.Holds[0].Attempt != 1 || d.Holds[0].BaselineHead != head ||
		!slices.Equal(d.Holds[0].BaselineDirty, []string{"dirty.txt"}) || d.Holds[0].EndedAt != nil {
		t.Fatalf("holds = %+v", d.Holds)
	}
	sends := f.conversation(task.ID).Sends()
	want := "You are working on a subtask of this project's uam planner.\n\n" +
		"Subtask: #3 One\nPath: #1 › #2 › #3\nWin condition: one passes\n\nDescription:\nDo the thing.\n\n" +
		"Checklist:\n- [ ] write it\n\n" +
		"Rules:\n- Read and update the board with the board tools.\n- Finish with a done request.\n" +
		"- Never mark anything done yourself: only the owner closes work.\n"
	if len(sends) != 1 || sends[0] != want {
		t.Fatalf("preamble = %q, want %q", sends, want)
	}
	// A held subtask can't be launched again, and nothing is created for it.
	f.refused(http.MethodPost, "/api/board/cards/"+one.ID+"/launch", `{}`, http.StatusBadRequest, string(board.CodeInvalid))
	if len(f.m.List()) != 1 {
		t.Fatalf("tasks = %d", len(f.m.List()))
	}

	// "Do whole story" holds the first pending subtask and lists the rest.
	two := f.create(board.KindSubtask, story.ID, "Two")
	three := f.create(board.KindSubtask, story.ID, "Three", `,"win_condition":"three passes"`)
	held, task = f.launch(story.ID)
	if held.ID != two.ID || held.HeldBy != task.ID {
		t.Fatalf("whole story held %+v", held)
	}
	got := f.conversation(task.ID).Sends()[0]
	for _, part := range []string{
		"You are working through a story", "Story: #2 Story\nPath: #1 › #2\nWin condition: story done\n",
		"You hold #4 Two first.\nPending subtasks after it, in order:\n- #5 Three: three passes\n",
		"- Finish each subtask with a done request, then claim the next pending one.\n",
	} {
		if !strings.Contains(got, part) {
			t.Fatalf("whole story preamble %q lacks %q", got, part)
		}
	}
	f.refused(http.MethodPost, "/api/board/cards/"+three.ID+"/launch", `{"model":"missing"}`, http.StatusBadRequest, "")
	if len(f.m.List()) != 2 {
		t.Fatalf("a refused selection left a task: %d", len(f.m.List()))
	}
	empty := f.create(board.KindEpic, "", "Empty")
	f.refused(http.MethodPost, "/api/board/cards/"+empty.ID+"/launch", `{}`, http.StatusBadRequest, string(board.CodeInvalid))
}

func TestPlannerPlanScopesATaskToTheContainer(t *testing.T) {
	f := newPlanner(t)
	epic := f.create(board.KindEpic, "", "Epic")
	leaf := f.create(board.KindSubtask, epic.ID, "Leaf")
	var reply struct {
		Session SessionSummary `json:"session"`
	}
	f.call(http.MethodPost, "/api/board/cards/"+epic.ID+"/plan", `{"brief":"Split the epic into stories."}`, http.StatusCreated, &reply)
	if reply.Session.Name != "Plan #1 Epic" {
		t.Fatalf("plan task = %+v", reply.Session)
	}
	got := f.conversation(reply.Session.ID).Sends()[0]
	for _, part := range []string{"You are planning the work under a card", "Epic: #1 Epic\nPath: #1\n", "Brief:\nSplit the epic into stories.\n",
		"- Create and edit stories and subtasks under #1.", "- Plan only: hold no subtask"} {
		if !strings.Contains(got, part) {
			t.Fatalf("plan preamble %q lacks %q", got, part)
		}
	}
	// The planning Task writes under the container as an agent, and holds nothing.
	f.store(func(ctx context.Context, st *board.Store) error {
		_, err := st.Create(ctx, board.Agent(reply.Session.ID, ""), board.NewCard{Kind: board.KindStory, ParentID: epic.ID, Title: "Proposed"})
		return err
	})
	if d := f.card(epic.ID); d.Card.Progress == nil || d.Card.HeldBy != "" {
		t.Fatalf("epic after planning = %+v", d.Card)
	}
	f.refused(http.MethodPost, "/api/board/cards/"+leaf.ID+"/plan", `{}`, http.StatusBadRequest, string(board.CodeInvalid))
}

// failingSends is a provider whose conversations refuse every prompt.
type failingSends struct{ *agenttest.Provider }

func (p failingSends) Open(ctx context.Context, req agentapi.OpenRequest) (agentapi.Conversation, error) {
	conv, err := p.Provider.Open(ctx, req)
	if c, ok := conv.(*agenttest.Conversation); ok {
		c.SetSendHook(func(context.Context, string) error { return errors.New("refused") })
	}
	return conv, err
}

func TestPlannerLaunchFailureLeavesNothingBehind(t *testing.T) {
	prov := agenttest.NewProvider("fake", allCaps)
	m := startManager(t, openTestStore(t), failingSends{prov})
	srv, err := NewServer(ServerConfig{Manager: m, Token: testToken, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	repo := branchRepo(t)
	f := &plannerFixture{t: t, ts: &testServer{srv: srv, m: m, prov: prov}, m: m, repo: repo, project: addProject(t, m, repo)}
	f.call(http.MethodPatch, "/api/settings", `{"planner":true}`, http.StatusOK, nil)
	leaf := f.create(board.KindSubtask, "", "Leaf")
	w := f.do(http.MethodPost, "/api/board/cards/"+leaf.ID+"/launch", `{}`)
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "first prompt") {
		t.Fatalf("failed launch = %d %s", w.Code, w.Body)
	}
	if list := m.List(); len(list) != 0 {
		t.Fatalf("tasks after a failed launch = %+v", list)
	}
	d := f.card(leaf.ID)
	if d.Card.Status != board.StatusTodo || d.Card.HeldBy != "" || len(d.Holds) != 1 || d.Holds[0].EndReason != string(board.ReleaseEnded) ||
		!slices.ContainsFunc(comments(d), func(c string) bool { return strings.HasPrefix(c, "uam: attempt #1 ended, uncommitted: none") }) {
		t.Fatalf("leaf after a failed launch = %+v, comments %v", d, comments(d))
	}
}

// idle ends the Task's running turn so it can settle.
func (f *plannerFixture) idle(id string) {
	f.t.Helper()
	f.conversation(id).EmitTurn(agentapi.TurnCompleted, "")
	waitUntil(f.t, "the turn to end", func() bool { s, _ := f.m.Summary(id); return !busy(s.State) })
}

func TestPlannerSettleDecidesEachHold(t *testing.T) {
	f := newPlanner(t)
	keep, release, cancel := f.create(board.KindSubtask, "", "Keep"), f.create(board.KindSubtask, "", "Release"), f.create(board.KindSubtask, "", "Cancel")
	tasks := map[string]string{}
	for _, c := range []BoardCard{keep, release, cancel} {
		_, task := f.launch(c.ID)
		tasks[c.ID] = task.ID
		f.idle(task.ID)
	}
	settle := func(card string) string { return "/api/sessions/" + tasks[card] + "/settle" }
	reply := f.refused(http.MethodPost, settle(keep.ID), "", http.StatusConflict, codeHoldsUndecided)
	var cards []BoardCard
	if err := json.Unmarshal(reply["cards"], &cards); err != nil || len(cards) != 1 || cards[0].ID != keep.ID {
		t.Fatalf("undecided cards = %s, %v", reply["cards"], err)
	}
	f.refused(http.MethodPost, settle(keep.ID), `{"holds":{}}`, http.StatusConflict, codeHoldsUndecided)
	f.refused(http.MethodPost, settle(keep.ID), fmt.Sprintf(`{"holds":{%q:{"action":"drop"}}}`, keep.ID), http.StatusBadRequest, string(board.CodeInvalid))
	f.refused(http.MethodPost, settle(cancel.ID), fmt.Sprintf(`{"holds":{%q:{"action":"cancel","comment":" "}}}`, cancel.ID), http.StatusBadRequest, string(board.CodeInvalid))
	if s, _ := f.m.Summary(tasks[keep.ID]); s.Stage != StageActive {
		t.Fatal("a refused settle settled the task")
	}

	var summary SessionSummary
	f.call(http.MethodPost, settle(keep.ID), fmt.Sprintf(`{"holds":{%q:{"action":"keep"}}}`, keep.ID), http.StatusOK, &summary)
	if summary.Stage != StageSettled || f.card(keep.ID).Card.HeldBy != tasks[keep.ID] {
		t.Fatalf("keep = %+v, %+v", summary, f.card(keep.ID).Card)
	}
	f.call(http.MethodPost, settle(release.ID), fmt.Sprintf(`{"holds":{%q:{"action":"release","comment":"later"}}}`, release.ID), http.StatusOK, nil)
	if d := f.card(release.ID); d.Card.Status != board.StatusTodo || d.Holds[0].EndReason != string(board.ReleaseSettled) || !slices.Contains(comments(d), "owner: later") {
		t.Fatalf("release = %+v", d)
	}
	f.call(http.MethodPost, settle(cancel.ID), fmt.Sprintf(`{"holds":{%q:{"action":"cancel","comment":"moot"}}}`, cancel.ID), http.StatusOK, nil)
	if d := f.card(cancel.ID); d.Card.Status != board.StatusCancelled || d.Holds[0].EndReason != string(board.ReleaseCancelled) {
		t.Fatalf("cancel = %+v", d)
	}
	// A kept hold survives Reopen; Archive releases it.
	if _, err := f.m.Reopen(tasks[keep.ID]); err != nil {
		t.Fatal(err)
	}
	if f.card(keep.ID).Card.HeldBy != tasks[keep.ID] {
		t.Fatal("reopen released a kept hold")
	}
	if _, err := f.m.Archive(tasks[keep.ID]); err != nil {
		t.Fatal(err)
	}
	if d := f.card(keep.ID); d.Card.Status != board.StatusTodo || d.Card.HeldBy != "" || !slices.ContainsFunc(comments(d), func(c string) bool { return strings.HasPrefix(c, "uam: attempt #1 ended") }) {
		t.Fatalf("archived holder = %+v, %v", d.Card, comments(d))
	}
	// A Task holding nothing settles with no body, as before.
	sum, _ := createSession(t, f.m, f.ts.prov)
	f.call(http.MethodPost, "/api/sessions/"+sum.ID+"/settle", "", http.StatusOK, &summary)
	if summary.Stage != StageSettled {
		t.Fatalf("plain settle = %+v", summary)
	}
	if w := f.do(http.MethodPost, "/api/sessions/"+sum.ID+"/settle", "{"); w.Code != http.StatusBadRequest {
		t.Fatalf("bad settle body = %d", w.Code)
	}
	if w := f.do(http.MethodPost, "/api/sessions/"+sum.ID+"/settle", `{"holds":"`+strings.Repeat("x", maxBodyBytes)+`"}`); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized settle body = %d", w.Code)
	}
}

func TestPlannerRequestsAcceptAndReject(t *testing.T) {
	f := newPlanner(t)
	one, two := f.create(board.KindSubtask, "", "One"), f.create(board.KindSubtask, "", "Two")
	_, taskOne := f.launch(one.ID)
	_, taskTwo := f.launch(two.ID)
	file := func(card, task string) board.Request {
		var r board.Request
		f.store(func(ctx context.Context, st *board.Store) error {
			var err error
			r, err = st.FileRequest(ctx, board.Agent(task, ""), card, board.RequestInput{Kind: board.RequestDone, Comment: "finished"})
			return err
		})
		return r
	}

	// Rejecting while the Task runs a turn steers it, and the hold stays.
	r := file(one.ID, taskOne.ID)
	var got BoardRequest
	f.refused(http.MethodPost, "/api/board/requests/"+r.ID+"/reject", `{"reason":" "}`, http.StatusBadRequest, string(board.CodeInvalid))
	f.call(http.MethodPost, "/api/board/requests/"+r.ID+"/reject", `{"reason":"the test still fails"}`, http.StatusOK, &got)
	if got.Status != board.RequestRejected || got.DecisionComment != "the test still fails" || string(got.Evidence) != "{}" {
		t.Fatalf("rejected = %+v", got)
	}
	if steers := f.conversation(taskOne.ID).Steers(); len(steers) != 1 || steers[0] != "The owner rejected your done request on #1 One: the test still fails" {
		t.Fatalf("steers = %q", steers)
	}
	if c := f.card(one.ID).Card; c.HeldBy != taskOne.ID || c.Status != board.StatusDoing {
		t.Fatalf("hold after a live reject = %+v", c)
	}
	f.refused(http.MethodPost, "/api/board/requests/"+r.ID+"/reject", `{"reason":"again"}`, http.StatusBadRequest, string(board.CodeInvalid))

	// Rejecting after the Task settled releases the hold with the reason.
	r = file(one.ID, taskOne.ID)
	f.idle(taskOne.ID)
	f.call(http.MethodPost, "/api/sessions/"+taskOne.ID+"/settle", fmt.Sprintf(`{"holds":{%q:{"action":"keep"}}}`, one.ID), http.StatusOK, nil)
	f.call(http.MethodPost, "/api/board/requests/"+r.ID+"/reject", `{"reason":"start over"}`, http.StatusOK, nil)
	if d := f.card(one.ID); d.Card.Status != board.StatusTodo || !slices.Contains(comments(d), "owner: start over") {
		t.Fatalf("settled reject = %+v, %v", d.Card, comments(d))
	}
	if steers := f.conversation(taskOne.ID).Steers(); len(steers) != 1 {
		t.Fatalf("a settled task was steered: %q", steers)
	}

	// Accept marks the subtask done and ends the hold.
	r = file(two.ID, taskTwo.ID)
	var snap BoardSnapshot
	f.call(http.MethodGet, "/api/board?project_id="+f.project, "", http.StatusOK, &snap)
	if len(snap.Requests) != 1 || snap.Requests[0].ID != r.ID || snap.Requests[0].Kind != board.RequestDone || len(snap.Requests[0].Payload) == 0 {
		t.Fatalf("inbox = %+v", snap.Requests)
	}
	f.call(http.MethodPost, "/api/board/requests/"+r.ID+"/accept", ``, http.StatusOK, &got)
	if got.Status != board.RequestAccepted {
		t.Fatalf("accepted = %+v", got)
	}
	if d := f.card(two.ID); d.Card.Status != board.StatusDone || d.Holds[0].EndReason != string(board.ReleaseAccepted) {
		t.Fatalf("accepted card = %+v", d)
	}
	f.refused(http.MethodPost, "/api/board/requests/missing/accept", `{}`, http.StatusNotFound, string(board.CodeNotFound))
}

func TestPlannerReleaseEndsTheHold(t *testing.T) {
	f := newPlanner(t)
	leaf := f.create(board.KindSubtask, "", "Leaf")
	f.refused(http.MethodPost, "/api/board/cards/"+leaf.ID+"/release", `{}`, http.StatusBadRequest, string(board.CodeInvalid))
	_, task := f.launch(leaf.ID)
	var c BoardCard
	f.call(http.MethodPost, "/api/board/cards/"+leaf.ID+"/release", `{"comment":"not now"}`, http.StatusOK, &c)
	if c.Status != board.StatusTodo || c.HeldBy != "" {
		t.Fatalf("released = %+v", c)
	}
	// The Task stays; it settles with nothing to decide.
	f.idle(task.ID)
	f.call(http.MethodPost, "/api/sessions/"+task.ID+"/settle", `{}`, http.StatusOK, nil)
}

func TestPlannerRemoveProjectMovesCardsToUnassigned(t *testing.T) {
	f := newPlanner(t)
	epic := f.create(board.KindEpic, "", "Epic")
	leaf := f.create(board.KindSubtask, epic.ID, "Leaf")
	_, task := f.launch(leaf.ID)
	f.idle(task.ID)
	if _, err := f.m.Archive(task.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.m.RemoveProject(f.project); err != nil {
		t.Fatal(err)
	}
	var snap BoardSnapshot
	f.call(http.MethodGet, "/api/board?project_id=unassigned", "", http.StatusOK, &snap)
	if len(snap.Cards) != 2 || snap.Cards[0].ID != epic.ID || snap.Cards[1].ID != leaf.ID || snap.Cards[1].ProjectID != "" || snap.Cards[1].HeldBy != "" {
		t.Fatalf("unassigned = %+v", snap.Cards)
	}
	// Unassigned cards are read-only until moved into a Project.
	f.refused(http.MethodPost, "/api/board/cards/"+leaf.ID+"/confirm", `{}`, http.StatusConflict, string(board.CodeReadOnly))
	f.refused(http.MethodPost, "/api/board/cards/"+leaf.ID+"/launch", `{}`, http.StatusConflict, string(board.CodeReadOnly))
	next := addProject(t, f.m, f.repo)
	var c BoardCard
	f.call(http.MethodPatch, "/api/board/cards/"+epic.ID, fmt.Sprintf(`{"project_id":%q}`, next), http.StatusOK, &c)
	if c.ProjectID != next {
		t.Fatalf("moved in = %+v", c)
	}
	f.project = next
	f.call(http.MethodGet, "/api/board?project_id="+next, "", http.StatusOK, &snap)
	if len(snap.Cards) != 2 {
		t.Fatalf("moved board = %+v", snap.Cards)
	}
}

func TestPlannerReconcileAtStart(t *testing.T) {
	st := openTestStore(t)
	repo := branchRepo(t)
	writeRepoFile(t, repo, "wip.txt", "x")
	projectID, settled, archived, deleted := mustUUID(t), mustUUID(t), mustUUID(t), mustUUID(t)
	now := time.Now().UTC()
	if err := st.Update(func(cfg *store.Config) error {
		cfg.WebSettings.Planner = true
		cfg.WebProjects = map[string]store.WebProject{projectID: {ID: projectID, Name: "p", Dir: repo, CreatedAt: now}}
		for id, stage := range map[string]string{settled: StageSettled, archived: StageArchived} {
			cfg.Sessions[store.Key("fake", id)] = store.SessionRecord{
				ID: id, Agent: "fake", Name: "t", Mode: store.ModeSafe, Workdir: repo, CreatedAt: now, LastSeenAt: now,
				Status: store.StatusActive, Surface: store.SurfaceWeb, ProviderSessionID: "conv_" + id,
				Web: &store.WebState{Turn: StateIdle, UpdatedAt: now, ProjectID: projectID, Stage: stage},
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	db, err := board.Open(filepath.Join(filepath.Dir(st.Path()), board.FileName), board.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cards := map[string]string{}
	for _, task := range []string{settled, archived, deleted} {
		c, err := db.Create(ctx, board.Owner(""), board.NewCard{ProjectID: projectID, Kind: board.KindSubtask, Title: "for " + task})
		if err == nil {
			_, err = db.Launch(ctx, board.Owner(""), c.ID, task, board.Baseline{})
		}
		if err != nil {
			t.Fatal(err)
		}
		cards[task] = c.ID
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	m := startManager(t, st, agenttest.NewProvider("fake", allCaps))
	cardDetail := func(task string) board.Detail {
		var d board.Detail
		if err := m.withBoard(func(s *board.Store) error {
			var err error
			d, err = s.Detail(ctx, cards[task])
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return d
	}
	if d := cardDetail(settled); d.Card.HeldBy != settled || len(d.Comments) != 0 {
		t.Fatalf("a settled task's hold after a restart = %+v", d)
	}
	for _, task := range []string{archived, deleted} {
		d := cardDetail(task)
		if d.Card.HeldBy != "" || d.Card.Status != board.StatusTodo || len(d.Comments) != 1 || d.Comments[0].Body != "attempt #1 ended, uncommitted: wip.txt" {
			t.Fatalf("an ended task's hold after a restart = %+v", d)
		}
	}
	// A second start changes nothing.
	_ = m.Shutdown(ctx)
	m = startManager(t, st, agenttest.NewProvider("fake", allCaps))
	if d := cardDetail(archived); len(d.Comments) != 1 {
		t.Fatalf("a second start wrote comments: %+v", d.Comments)
	}
}

func TestPlannerBrokenDatabaseStillSettles(t *testing.T) {
	st := openTestStore(t)
	if err := st.Update(func(cfg *store.Config) error { cfg.WebSettings.Planner = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(filepath.Dir(st.Path()), board.FileName), 0o700); err != nil {
		t.Fatal(err)
	}
	prov := agenttest.NewProvider("fake", allCaps)
	m := startManager(t, st, prov)
	if _, err := m.Board(unassignedBoard); !errors.Is(err, errPlannerBroken) {
		t.Fatalf("board with a broken database = %v", err)
	}
	sum, _ := createSession(t, m, prov)
	if _, err := m.Settle(sum.ID); err != nil {
		t.Fatalf("settle with a broken planner database: %v", err)
	}
	// Turning the switch off and on again retries the open.
	if _, err := m.UpdateSettings(SettingsPatch{Planner: new(bool)}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Board(unassignedBoard); !errors.Is(err, errPlannerOff) {
		t.Fatalf("board off = %v", err)
	}
	on := true
	if _, err := m.UpdateSettings(SettingsPatch{Planner: &on}); err == nil || m.Settings().Planner {
		t.Fatalf("on over a broken database = %v, planner %v", err, m.Settings().Planner)
	}
}

func TestPreambleNotesStaleness(t *testing.T) {
	p := preambleInput{card: board.Card{Seq: 4, Kind: board.KindSubtask, Title: "Leaf"}, stale: "3 commits behind"}
	if got := p.String(); !strings.Contains(got, "\nThe code moved on since this subtask was planned:\n3 commits behind\n") {
		t.Fatalf("preamble = %q", got)
	}
	if upperFirst("") != "" {
		t.Fatal("upperFirst of nothing")
	}
}

func TestBoardJSONShapes(t *testing.T) {
	cmd := "make"
	expires := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	c := boardCard(board.Card{ID: "c", Seq: 1, Kind: board.KindStory, Status: board.StatusPlanned, AcceptCmd: &cmd, ExpiresAt: &expires,
		Progress: &board.Progress{Done: 1, Total: 2, Proposed: 3}})
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{`"parent_id":null`, `"labels":[]`, `"checklist":[]`, `"blocked_by":[]`, `"accept_cmd":"make"`, `"paths":[]`,
		`"progress":{"done":1,"total":2,"proposed":3}`, `"confirmed":false`, `"expires_at":"2026-10-01T00:00:00Z"`} {
		if !strings.Contains(string(data), part) {
			t.Fatalf("card JSON %s lacks %s", data, part)
		}
	}
	if strings.Contains(string(data), `"held_by"`) || strings.Contains(string(data), `"due"`) {
		t.Fatalf("card JSON %s sends empty optional fields", data)
	}
	r, err := json.Marshal(boardRequest(board.Request{ID: "r", Payload: json.RawMessage(`{"a":1}`)}))
	if err != nil || !strings.Contains(string(r), `"payload":{"a":1}`) || !strings.Contains(string(r), `"evidence":{}`) || !strings.Contains(string(r), `"flags":[]`) {
		t.Fatalf("request JSON = %s, %v", r, err)
	}
	h, err := json.Marshal(boardHold(board.Hold{ID: "h", TaskID: "t", Attempt: 1}))
	if err != nil || !strings.Contains(string(h), `"baseline_dirty":[]`) || strings.Contains(string(h), `"ended_at"`) {
		t.Fatalf("hold JSON = %s, %v", h, err)
	}
	cm, err := json.Marshal(boardComment(board.Comment{ID: 7, Author: "uam", Automatic: true}))
	if err != nil || !strings.HasPrefix(string(cm), `{"id":"7","author":"uam","body":"","automatic":true,`) {
		t.Fatalf("comment JSON = %s, %v", cm, err)
	}
}
