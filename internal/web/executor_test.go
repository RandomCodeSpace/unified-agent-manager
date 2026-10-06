package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/board"
)

// runExec turns the executor on, on a clock the test moves: the returned
// func moves it forward and kicks a pass.
func (r *laneRun) runExec() func(time.Duration) {
	var ahead atomic.Int64
	r.m.exec.mu.Lock()
	r.m.exec.clock = func() time.Time { return time.Now().Add(time.Duration(ahead.Load())) }
	r.m.exec.mu.Unlock()
	r.m.runExecutor(true)
	return func(d time.Duration) {
		ahead.Add(int64(d))
		r.m.kickExecutor()
	}
}

// link makes subtask i block subtask j.
func (r *laneRun) link(i, j int) {
	r.t.Helper()
	r.call(http.MethodPost, "/api/board/links", fmt.Sprintf(`{"blocker":%q,"blocked":%q}`, r.leaves[i].ID, r.leaves[j].ID), http.StatusNoContent, nil)
}

// running waits until uam started subtask i in a lane and its Task works on
// the run's first prompt, and returns the Task and the lane.
func (r *laneRun) running(i int) (SessionSummary, lane) {
	r.t.Helper()
	var c BoardCard
	waitUntil(r.t, fmt.Sprintf("#%d to start", r.leaves[i].Seq), func() bool {
		c = r.card(r.leaves[i].ID).Card
		if c.HeldBy == "" || c.Lane == nil {
			return false
		}
		s, err := r.m.Summary(c.HeldBy)
		return err == nil && busy(s.State)
	})
	task, err := r.m.Summary(c.HeldBy)
	if err != nil {
		r.t.Fatal(err)
	}
	l, err := laneOf(r.m.lanesRoot(), r.project, c.Lane.Branch)
	if err != nil {
		r.t.Fatal(err)
	}
	return task, l
}

// laneTasks lists the Tasks working in lanes.
func laneTasks(m *Manager) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for id, s := range m.sessions {
		if !s.removed && m.inLanes(s.workdir) {
			out = append(out, id)
		}
	}
	return out
}

// startFailures is how many lane starts under the epic failed on git or
// the store in a row.
func startFailures(m *Manager, epic string) int {
	m.exec.mu.Lock()
	defer m.exec.mu.Unlock()
	return m.exec.epicFails[epic]
}

// sends lists every prompt sent to the Task id, over each conversation it
// opened.
func sends(prov *agenttest.Provider, id string) []string {
	var out []string
	for _, c := range prov.Conversations() {
		if c.Request().SessionID == id {
			out = append(out, c.Sends()...)
		}
	}
	return out
}

func emptyDir(t *testing.T, dir string) bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return len(entries) == 0
}

// Approving an epic runs it (ADR 0006 §4): its ready subtasks start at once,
// each in a lane of its own; one that waits on them starts once both
// landed, on top of their work. A Task whose work landed is never
// cancelled: it reads that it is finished and ends its turn, and is then
// archived, its lane removed and its branch deleted.
func TestExecutorRunsAnEpic(t *testing.T) {
	r := planLaneRun(t, "true", "A", "B", "C")
	r.link(0, 2)
	r.link(1, 2)
	r.runExec()
	r.approve()
	a, la := r.running(0)
	b, lb := r.running(1)
	if la.dir == lb.dir || la.branch == lb.branch {
		t.Fatalf("A and B share a lane: %+v", la)
	}
	if c := r.card(r.leaves[2].ID).Card; c.HeldBy != "" {
		t.Fatalf("C started before what it waits on: %+v", c)
	}
	commitFile(t, la.dir, "from-a.txt", "a\n")
	commitFile(t, lb.dir, "from-b.txt", "b\n")
	r.landed(a, 0)
	if c := r.card(r.leaves[2].ID).Card; c.HeldBy != "" {
		t.Fatalf("C started before B landed: %+v", c)
	}
	r.landed(b, 1)
	_, lc := r.running(2)
	for _, name := range []string{"from-a.txt", "from-b.txt"} {
		if _, err := os.Stat(filepath.Join(lc.dir, name)); err != nil {
			t.Fatalf("C's lane lacks %s: %v", name, err)
		}
	}
	for i, l := range []lane{la, lb} {
		task := []SessionSummary{a, b}[i]
		r.idle(task.ID)
		waitUntil(t, "the landed Task to be archived and its lane removed", func() bool {
			s, _ := r.m.Summary(task.ID)
			return s.Stage == StageArchived && gone(l.dir) && gitTry(t, r.repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+l.branch) != nil
		})
		if n := r.conversation(task.ID).Cancels(); n != 0 {
			t.Fatalf("the Task that landed #%d was cancelled %d times", r.leaves[i].Seq, n)
		}
	}
}

// A restart (ADR 0006 §4.7): the next service lands a done request that
// waited to land, nudges once each Task whose turn the restart interrupted,
// and starts nothing again: no other Task is made, and each subtask keeps
// its attempt.
func TestExecutorRestartNudgesAndLands(t *testing.T) {
	r := planLaneRun(t, "true", "A", "B")
	r.runExec()
	r.approve()
	a, la := r.running(0)
	b, _ := r.running(1)
	commitFile(t, la.dir, "from-a.txt", "a\n")
	r.queueLanding(func() { r.toolOK(a.ID, "board_request", r.doneArgs(0)) })
	ctx := context.Background()
	if err := r.m.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	m := NewManager(r.m.store, []agentapi.Provider{r.ts.prov})
	m.exec.tick = 20 * time.Millisecond
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	detail := func(i int) BoardCardDetail {
		t.Helper()
		d, err := m.CardDetail(r.leaves[i].ID)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	waitUntil(t, "A to land", func() bool { return detail(0).Card.Status == board.StatusDone })
	want := fmt.Sprintf("uam restarted while you worked on #%d", r.leaves[1].Seq)
	nudges := func(id, text string) int {
		n := 0
		for _, s := range sends(r.ts.prov, id) {
			if strings.Contains(s, text) {
				n++
			}
		}
		return n
	}
	waitUntil(t, "B to be nudged", func() bool { return nudges(b.ID, want) == 1 })
	// A few more passes nudge nothing again.
	time.Sleep(10 * m.exec.tick)
	if n := nudges(b.ID, want); n != 1 {
		t.Fatalf("B was nudged %d times", n)
	}
	if n := nudges(a.ID, "uam restarted"); n != 0 {
		t.Fatalf("A, whose work waited to land, was nudged %d times", n)
	}
	d := detail(0)
	if d.Requests[0].Status != board.RequestAccepted || d.Card.Lane == nil || d.Card.Lane.LandedSHA != r.tip() {
		t.Fatalf("A after the restart = %+v, request %+v", d.Card, d.Requests[0])
	}
	if d := detail(1); d.Card.HeldBy != b.ID || len(d.Holds) != 1 {
		t.Fatalf("B after the restart = %+v, holds %+v", d.Card, d.Holds)
	}
	made := 0
	for _, o := range r.ts.prov.Opens() {
		if o.ConversationID == "" {
			made++
		}
	}
	if made != 2 {
		t.Fatalf("%d Tasks were made, want 2", made)
	}
}

// With a landing's intent stored and the integration branch moved, but the
// board not told, as a crash leaves it, the next boot finishes the landing
// and the run goes on: what waits on it starts on top of it.
func TestExecutorBootFinishesALanding(t *testing.T) {
	r := planLaneRun(t, "true", "A", "C")
	r.link(0, 1)
	r.runExec()
	r.approve()
	a, la := r.running(0)
	commitFile(t, la.dir, "from-a.txt", "a\n")
	r.queueLanding(func() { r.toolOK(a.ID, "board_request", r.doneArgs(0)) })
	// The service stops in the landing's commit phase.
	r.m.runExecutor(false)
	req := r.card(r.leaves[0].ID).Requests[0]
	ctx := context.Background()
	repo, err := openLanes(ctx, r.project, r.repo)
	if err != nil {
		t.Fatal(err)
	}
	tip := r.tip()
	sha, err := repo.squashLane(ctx, la, tip, landMessage(r.leaves[0].Title, r.leaves[0].Seq, "did A", req.ID))
	if err != nil {
		t.Fatal(err)
	}
	r.store(func(ctx context.Context, st *board.Store) error { return st.MarkLanding(ctx, req.ID, sha) })
	gitIn(t, r.repo, "update-ref", "refs/heads/"+r.integ(), sha, tip)
	r.m.closeBoard()
	r.m.runExecutor(true)
	if err := r.m.openBoard(ctx); err != nil {
		t.Fatal(err)
	}
	if d := r.card(r.leaves[0].ID); d.Card.Status != board.StatusDone || d.Requests[0].Status != board.RequestAccepted {
		t.Fatalf("A after boot = %+v, request %+v", d.Card, d.Requests[0])
	}
	_, lc := r.running(1)
	if _, err := os.Stat(filepath.Join(lc.dir, "from-a.txt")); err != nil {
		t.Fatalf("C's lane lacks A's work: %v", err)
	}
	if n := r.conversation(a.ID).Cancels(); n != 0 {
		t.Fatalf("A was cancelled %d times", n)
	}
}

// The executor landing an approved epic's last subtask, a done request whose
// landing was queued, merges the epic into main with no click (ADR 0006
// §5.8), once: the passes after it, the landed Tasks' retirement and a
// restart merge nothing again.
func TestExecutorFinishedEpicMergesOnce(t *testing.T) {
	r := planLaneRun(t, "true", "A", "B")
	move := r.runExec()
	r.approve()
	a, la := r.running(0)
	b, lb := r.running(1)
	commitFile(t, la.dir, "from-a.txt", "a\n")
	r.landed(a, 0)
	main := gitOutput(t, r.repo, "rev-parse", "main")
	sub := r.subscribe()
	commitFile(t, lb.dir, "from-b.txt", "b\n")
	r.queueLanding(func() { r.toolOK(b.ID, "board_request", r.doneArgs(1)) })
	if c := r.card(r.leaves[1].ID).Card; c.Status == board.StatusDone {
		t.Fatalf("B landed in its own call: %+v", c)
	}
	// The executor lands it once its Task ends its turn and the retry is due.
	r.idle(b.ID)
	move(16 * time.Minute)
	if end := r.nextMerge(sub); end.Status != jobDone {
		t.Fatalf("merge job = %+v", end)
	}
	if d := r.card(r.leaves[1].ID); d.Card.Status != board.StatusDone || d.Requests[0].DecidedBy != board.DecidedByUAM {
		t.Fatalf("B = %+v, request %+v", d.Card, d.Requests[0])
	}
	merged := gitOutput(t, r.repo, "rev-parse", "main")
	if got := gitOutput(t, r.repo, "rev-list", "--parents", "-n1", merged); got != merged+" "+main+" "+r.tip() {
		t.Fatalf("main = %q, want the merge of main and %s", got, r.tip())
	}
	r.idle(a.ID)
	for _, task := range []SessionSummary{a, b} {
		waitUntil(t, "the landed Task to be retired", func() bool { s, _ := r.m.Summary(task.ID); return s.Stage == StageArchived })
	}
	move(16 * time.Minute)
	r.m.executorPass()
	r.reboot()
	r.noMerge(sub)
	if got := gitOutput(t, r.repo, "rev-parse", "main"); got != merged {
		t.Fatalf("main moved again, to %s", got)
	}
	if got := r.epicComments("uam: Merged"); len(got) != 1 {
		t.Fatalf("merge comments = %q, want one", got)
	}
}

// A start whose Task cannot be made fails on the provider (ADR 0006 §4.5):
// the lane goes, nothing is paused, and nothing more starts on that
// provider until it has backed off, which the Project reports as a wait
// and the epic notes.
func TestExecutorFailingCreateBacksOffTheProvider(t *testing.T) {
	r := planLaneRun(t, "true", "Leaf")
	r.ts.prov.SetOpenError(errors.New("no capacity"))
	move := r.runExec()
	r.approve()
	var p BoardProject
	waitUntil(t, "the provider to back off", func() bool {
		r.call(http.MethodGet, "/api/board/projects/"+r.project, "", http.StatusOK, &p)
		return p.Executor != nil && len(p.Executor.Providers) == 1
	})
	if w := p.Executor.Providers[0]; w.Provider != "fake" || w.Name != "Fake fake" || !strings.Contains(w.Detail, "no capacity") || w.Until == nil || !w.Until.After(time.Now()) {
		t.Fatalf("provider wait = %+v", w)
	}
	opens := len(r.ts.prov.Opens())
	r.m.executorPass()
	if n := len(r.ts.prov.Opens()); n != opens || opens != 1 {
		t.Fatalf("%d opens, then %d while the provider backs off", opens, n)
	}
	if ids := laneTasks(r.m); len(ids) != 0 {
		t.Fatalf("lane Tasks left: %v", ids)
	}
	if !emptyDir(t, filepath.Join(r.m.lanesRoot(), r.project)) {
		t.Fatal("the failed start left its lane")
	}
	if out := gitOutput(t, r.repo, "for-each-ref", "--format=%(refname:short)", "refs/heads/"+r.integ()+"-*"); out != "" {
		t.Fatalf("attempt branches left: %s", out)
	}
	leaf, epic := r.card(r.leaves[0].ID), r.card(r.epic.ID)
	if leaf.Card.Paused != "" || leaf.Card.HeldBy != "" || epic.Card.Paused != "" || startFailures(r.m, r.epic.ID) != 0 {
		t.Fatalf("after the provider failed: leaf %+v, epic %+v", leaf.Card, epic.Card)
	}
	if !slices.ContainsFunc(comments(epic), func(c string) bool {
		return strings.HasPrefix(c, "uam: Waiting for Fake fake: ") && strings.Contains(c, "no capacity")
	}) {
		t.Fatalf("epic comments = %q", comments(epic))
	}
	r.ts.prov.SetOpenError(nil)
	move(2 * time.Minute)
	r.running(0)
}

// Lane starts that keep failing on git back the epic off, and the third in
// a row pauses it as uam, saying why (ADR 0006 §4.5). Resume runs the
// Approve checks, and the subtask starts at once.
func TestExecutorPausesAnEpicAfterThreeGitStartFailures(t *testing.T) {
	r := planLaneRun(t, "true", "Leaf")
	// A file stands where the Project's lanes go, so no lane can be added.
	blocker := filepath.Join(r.m.lanesRoot(), r.project)
	if err := os.MkdirAll(filepath.Dir(blocker), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	move := r.runExec()
	r.approve()
	for n := 1; n <= 2; n++ {
		waitUntil(t, fmt.Sprintf("start failure %d", n), func() bool { return startFailures(r.m, r.epic.ID) == n })
		if c := r.card(r.epic.ID).Card; c.Paused != "" {
			t.Fatalf("paused after %d failures", n)
		}
		move(16 * time.Minute)
	}
	waitUntil(t, "the epic to be paused", func() bool { return r.card(r.epic.ID).Card.Paused == board.PausedUAM })
	epic := r.card(r.epic.ID)
	if !slices.ContainsFunc(comments(epic), func(c string) bool {
		return strings.HasPrefix(c, "uam: paused: lanes could not start 3 times in a row: ") && strings.Contains(c, "not a directory")
	}) {
		t.Fatalf("epic comments = %q", comments(epic))
	}
	if c := r.card(r.leaves[0].ID).Card; c.Paused != "" || c.HeldBy != "" || len(laneTasks(r.m)) != 0 {
		t.Fatalf("after the failures: leaf %+v, lane Tasks %v", c, laneTasks(r.m))
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	r.call(http.MethodPatch, "/api/board/cards/"+r.epic.ID, `{"paused":false}`, http.StatusOK, nil)
	r.running(0)
}

// A start that loses a race with a board write, here a pause landing
// between the check and the start's own, discards its Task and lane and
// counts nothing: once resumed, the subtask starts at once.
func TestExecutorNotReadyRaceCountsNothing(t *testing.T) {
	r := planLaneRun(t, "true", "Leaf")
	var once sync.Once
	paused := make(chan error, 1)
	r.m.landHook = func(stage string) {
		if stage != laneCreated {
			return
		}
		once.Do(func() {
			yes := true
			paused <- r.m.withBoard(func(st *board.Store) error {
				_, err := st.Edit(context.Background(), board.Owner(""), r.leaves[0].ID, board.Patch{Paused: &yes})
				return err
			})
		})
	}
	r.runExec()
	r.approve()
	if err := <-paused; err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the raced start to be discarded", func() bool {
		return len(laneTasks(r.m)) == 0 && emptyDir(t, filepath.Join(r.m.lanesRoot(), r.project))
	})
	if n := startFailures(r.m, r.epic.ID); n != 0 {
		t.Fatalf("the race counted %d failures", n)
	}
	if epic := r.card(r.epic.ID); epic.Card.Paused != "" || slices.ContainsFunc(comments(epic), func(c string) bool { return strings.Contains(c, "could not start") }) {
		t.Fatalf("after the race: epic %+v, comments %q", epic.Card, comments(epic))
	}
	r.call(http.MethodPatch, "/api/board/cards/"+r.leaves[0].ID, `{"paused":false}`, http.StatusOK, nil)
	r.running(0)
}

// Resume of an approved epic runs the Approve checks (ADR 0006 §4.6): a base
// branch that no longer merges into the integration branch refuses it, and
// the epic stays paused; the first Resume in a Project with no base branch
// recorded records the branch checked out.
func TestResumeOfAnApprovedEpicRunsThePreflight(t *testing.T) {
	r := newLaneRun(t, "true", "Leaf")
	epic := "/api/board/cards/" + r.epic.ID
	r.call(http.MethodPatch, epic, `{"paused":true}`, http.StatusOK, nil)
	side := filepath.Join(t.TempDir(), "side")
	gitIn(t, r.repo, "worktree", "add", "-q", side, r.integ())
	commitFile(t, side, "a.txt", "ONE\ntwo\nthree\n")
	gitIn(t, r.repo, "worktree", "remove", "--force", side)
	commitFile(t, r.repo, "a.txt", "uno\ntwo\nthree\n")
	reply := r.refused(http.MethodPatch, epic, `{"paused":false}`, http.StatusConflict, codeMergeConflict)
	if !strings.Contains(string(reply["error"]), "a.txt") || r.card(r.epic.ID).Card.Paused != board.PausedOwner {
		t.Fatalf("resume on a conflicting base = %s", reply["error"])
	}
	gitIn(t, r.repo, "reset", "-q", "--hard", "HEAD~1")
	r.store(func(ctx context.Context, st *board.Store) error {
		return st.SetProjectBaseRef(ctx, board.Owner(""), r.project, "")
	})
	var c BoardCard
	r.call(http.MethodPatch, epic, `{"paused":false}`, http.StatusOK, &c)
	var p BoardProject
	r.call(http.MethodGet, "/api/board/projects/"+r.project, "", http.StatusOK, &p)
	if c.Paused != "" || p.Integration == nil || p.Integration.BaseRef != "main" {
		t.Fatalf("resumed = %+v, project %+v", c, p.Integration)
	}
}

// A lane Task that ends its turn without a done request is nudged once to
// finish or say why it is blocked; ending again without one retires it:
// the Task is archived, never cancelled, its lane removed and its branch
// kept, and the subtask paused by uam (ADR 0006 §4.2, §4.5).
func TestExecutorNudgesOnceThenRetires(t *testing.T) {
	r := planLaneRun(t, "true", "Leaf")
	r.runExec()
	r.approve()
	task, l := r.running(0)
	conv := r.conversation(task.ID)
	r.idle(task.ID)
	want := fmt.Sprintf("You still hold #%d and ended your turn without a done request.", r.leaves[0].Seq)
	waitUntil(t, "the nudge", func() bool { s := conv.Sends(); return len(s) == 2 && strings.HasPrefix(s[1], want) })
	r.idle(task.ID)
	waitUntil(t, "the Task to be retired", func() bool {
		s, _ := r.m.Summary(task.ID)
		return s.Stage == StageArchived && gone(l.dir)
	})
	d := r.card(r.leaves[0].ID)
	if d.Card.Paused != board.PausedUAM || d.Card.HeldBy != "" || len(conv.Sends()) != 2 || conv.Cancels() != 0 {
		t.Fatalf("retired: %+v, sends %q, %d cancels", d.Card, conv.Sends(), conv.Cancels())
	}
	if err := gitTry(t, r.repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+l.branch); err != nil {
		t.Fatalf("the attempt branch went: %v", err)
	}
}

// A lane Task whose turn fails before it did any work ends its attempt as
// aborted (ADR 0006 §4.5): the subtask is back to do, not paused, its Task
// and lane go, its provider backs off, and the executor forgets the Task.
func TestExecutorAbortsAnAttemptThatFailedBeforeWork(t *testing.T) {
	r := planLaneRun(t, "true", "Leaf")
	r.runExec()
	r.approve()
	task, l := r.running(0)
	r.conversation(task.ID).EmitTurn(agentapi.TurnFailed, "model unavailable")
	waitUntil(t, "the attempt to end", func() bool {
		_, err := r.m.Summary(task.ID)
		return r.card(r.leaves[0].ID).Card.HeldBy == "" && err != nil && gone(l.dir)
	})
	d := r.card(r.leaves[0].ID)
	if d.Card.Paused != "" || d.Card.Status != board.StatusTodo || len(d.Holds) != 1 || d.Holds[0].EndReason != string(board.ReleaseAborted) {
		t.Fatalf("aborted: %+v, holds %+v", d.Card, d.Holds)
	}
	var p BoardProject
	r.call(http.MethodGet, "/api/board/projects/"+r.project, "", http.StatusOK, &p)
	if p.Executor == nil || !strings.Contains(p.Executor.Providers[0].Detail, "model unavailable") {
		t.Fatalf("executor = %+v", p.Executor)
	}
	r.m.kickExecutor()
	waitUntil(t, "the executor to forget the Task", func() bool {
		r.m.exec.mu.Lock()
		defer r.m.exec.mu.Unlock()
		_, seen := r.m.exec.seen[task.ID]
		return !seen
	})
}

// A lane Task's turn that fails after it did work counts against its
// provider (ADR 0006 §4.5): nothing is nudged while the provider backs off;
// then one probe nudge tells the Task to carry on, and a completed turn on
// the provider closes its breaker. Nothing is paused.
func TestExecutorProbesAProviderThatFailedDuringWork(t *testing.T) {
	r := planLaneRun(t, "true", "Leaf")
	move := r.runExec()
	r.approve()
	task, l := r.running(0)
	commitFile(t, l.dir, "half.txt", "half\n")
	conv := r.conversation(task.ID)
	conv.EmitTurn(agentapi.TurnFailed, "rate limited")
	var p BoardProject
	waitUntil(t, "the provider to back off", func() bool {
		r.call(http.MethodGet, "/api/board/projects/"+r.project, "", http.StatusOK, &p)
		return p.Executor != nil
	})
	if w := p.Executor.Providers[0]; w.Detail != "rate limited" || w.Until == nil {
		t.Fatalf("provider wait = %+v", w)
	}
	probe := fmt.Sprintf("The provider failed during your turn; continue #%d", r.leaves[0].Seq)
	probed := func() bool {
		return slices.ContainsFunc(conv.Sends(), func(s string) bool { return strings.HasPrefix(s, probe) })
	}
	r.m.executorPass()
	if probed() {
		t.Fatal("nudged while the provider backs off")
	}
	move(2 * time.Minute)
	waitUntil(t, "the probe", probed)
	r.landed(task, 0)
	r.idle(task.ID)
	waitUntil(t, "the breaker to close", func() bool {
		r.call(http.MethodGet, "/api/board/projects/"+r.project, "", http.StatusOK, &p)
		return p.Executor == nil
	})
	if c := r.card(r.leaves[0].ID).Card; c.Status != board.StatusDone || c.Paused != "" {
		t.Fatalf("after the probe: %+v", c)
	}
}

// The executor reads whether a failed lane Task did work in the lane uam
// made, as uam's other git for lanes does, never where the lane's directory
// leads: a lane that became a symbolic link to the owner's clean checkout
// still has the commit it made, so its attempt is not discarded.
func TestLaneWorkedReadsOnlyTheLaneUamMade(t *testing.T) {
	r := newLaneRun(t, "true", "Leaf")
	task, l := r.start(0)
	ctx := context.Background()
	if r.m.laneWorked(ctx, task.Workdir) {
		t.Fatal("a lane with no work counts as worked")
	}
	commitFile(t, l.dir, "b.txt", "lane\n")
	if !r.m.laneWorked(ctx, task.Workdir) {
		t.Fatal("a lane with a commit of its own counts as not worked")
	}
	if err := os.Rename(l.dir, l.dir+".moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(r.repo, l.dir); err != nil {
		t.Fatal(err)
	}
	if !r.m.laneWorked(ctx, task.Workdir) {
		t.Fatal("a lane linked to the owner's clean checkout counts as not worked")
	}
}

// While its provider is signed out nothing starts on it: the Project reports
// the wait, with no end, and the subtask starts once the provider is signed
// in again (ADR 0006 §4.5).
func TestExecutorWaitsWhileSignedOut(t *testing.T) {
	r := newLaneRun(t, "true", "Leaf")
	signOut := func(out bool) {
		r.m.mu.Lock()
		info := r.m.infos["fake"]
		info.SignedOut, info.Reason = out, ""
		if out {
			info.Reason = "Fake fake is signed out. Sign in in Settings."
		}
		r.m.infos["fake"] = info
		r.m.mu.Unlock()
	}
	signOut(true)
	r.runExec()
	r.m.executorPass()
	waitUntil(t, "no start in flight", func() bool {
		r.m.exec.mu.Lock()
		defer r.m.exec.mu.Unlock()
		return len(r.m.exec.starting) == 0
	})
	r.m.exec.mu.Lock()
	tried := r.m.exec.breakers["fake"].Failures
	r.m.exec.mu.Unlock()
	if tried != 0 {
		t.Fatalf("%d starts were tried while signed out", tried)
	}
	var p BoardProject
	r.call(http.MethodGet, "/api/board/projects/"+r.project, "", http.StatusOK, &p)
	if p.Executor == nil || p.Executor.Providers[0].Until != nil || !strings.Contains(p.Executor.Providers[0].Detail, "signed out") {
		t.Fatalf("executor = %+v", p.Executor)
	}
	// The Planner reads the waits alone on each Board change: no git.
	var e BoardExecutor
	r.call(http.MethodGet, "/api/board/executor", "", http.StatusOK, &e)
	if !slices.Equal(e.Providers, p.Executor.Providers) {
		t.Fatalf("executor waits = %+v, want %+v", e, p.Executor)
	}
	if n := len(r.ts.prov.Opens()); n != 0 {
		t.Fatalf("%d Tasks opened while signed out", n)
	}
	signOut(false)
	r.m.kickExecutor()
	r.running(0)
	r.call(http.MethodGet, "/api/board/executor", "", http.StatusOK, &e)
	if e.Providers == nil || len(e.Providers) != 0 {
		t.Fatalf("executor waits after signing in = %+v, want none", e)
	}
}

// A lane start that failed is recorded without waiting for mu while it
// holds the executor's lock: a turn that ends at that moment holds mu and
// takes the executor's lock (ADR 0006 §4.3), so that order would deadlock.
func TestExecutorStartFailureKeepsTheLockOrder(t *testing.T) {
	r := planLaneRun(t, "true", "Leaf")
	pick := board.Pick{Epic: board.Card{ID: r.epic.ID, Seq: r.epic.Seq}, Card: board.Card{ID: r.leaves[0].ID, Seq: r.leaves[0].Seq}}
	recorded := make(chan struct{})
	r.m.mu.Lock()
	go func() {
		defer close(recorded)
		r.m.started(context.Background(), pick, errors.New("no space left on device"))
	}()
	// Time for the failed start to reach its locks before the turn takes
	// the executor's.
	time.Sleep(50 * time.Millisecond)
	took := make(chan struct{})
	go func() {
		// What a completed turn records, with mu held.
		r.m.exec.mu.Lock()
		r.m.exec.completed["fake"] = r.m.exec.clock()
		r.m.exec.mu.Unlock()
		close(took)
	}()
	select {
	case <-took:
		r.m.mu.Unlock()
	case <-time.After(2 * time.Second):
		r.m.mu.Unlock()
		<-recorded
		t.Fatal("a failed lane start held the executor's lock while it waited for mu")
	}
	<-recorded
	if n := startFailures(r.m, r.epic.ID); n != 1 {
		t.Fatalf("start failures = %d", n)
	}
}

// A landing intent waits out its retry time as a done waiting to land does
// (ADR 0006 §4.5), so the executor keeps that time while the intent is
// stored, also for a done the owner's Accept began to land.
func TestExecutorKeepsALandingIntentsRetryTime(t *testing.T) {
	m := &Manager{exec: newExecutorState()}
	at := time.Now().Add(time.Minute)
	m.exec.landRetry["r1"], m.exec.landTries["r1"] = at, 1
	facts := board.RunFacts{Lanes: []board.LaneFacts{{CardID: "c1", Seq: 1, TaskID: "t1", Pending: board.RequestDone, Request: "r1", Intent: "abc1234"}}}
	_, mem := m.executorMemory(facts, map[string]board.TaskFact{}, map[string]laneTask{}, nil, nil, nil)
	if !mem.LandRetry["r1"].Equal(at) || m.exec.landTries["r1"] != 1 {
		t.Fatalf("retry = %v, tries %d", mem.LandRetry["r1"], m.exec.landTries["r1"])
	}
}

// A provider failure notes only the epics it holds back (ADR 0006 §4.5):
// one paused, done or cancelled runs nothing more on the provider.
func TestEpicsOnListsTheEpicsThatRunOnTheProvider(t *testing.T) {
	run := func(provider string) *board.Run {
		return &board.Run{RunSettings: board.RunSettings{Provider: provider}}
	}
	epic := func(id, provider string, status board.Status, paused string) board.EpicFacts {
		return board.EpicFacts{Epic: board.Card{ID: id, Kind: board.KindEpic, Status: status, Paused: paused, Run: run(provider)}}
	}
	facts := board.RunFacts{Epics: []board.EpicFacts{
		epic("doing", "copilot", board.StatusDoing, ""),
		epic("todo", "copilot", board.StatusTodo, ""),
		epic("paused", "copilot", board.StatusDoing, board.PausedOwner),
		epic("done", "copilot", board.StatusDone, ""),
		epic("cancelled", "copilot", board.StatusCancelled, ""),
		epic("other", "other", board.StatusDoing, ""),
	}}
	var ids []string
	for _, e := range epicsOn(facts, "copilot") {
		ids = append(ids, e.ID)
	}
	if !slices.Equal(ids, []string{"doing", "todo"}) {
		t.Fatalf("epics on copilot = %v, want doing and todo", ids)
	}
}
