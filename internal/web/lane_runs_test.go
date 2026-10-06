package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/board"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/execpath"
	uamlog "github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// laneRun is a planner whose Project holds a.txt on main and an approved
// epic, parallel 2, whose one story holds the given subtasks: each Launch
// of one starts a lane (ADR 0006 §5.3).
type laneRun struct {
	*plannerFixture
	epic   BoardCard
	leaves []BoardCard
}

func newLaneRun(t *testing.T, cmd string, titles ...string) *laneRun {
	t.Helper()
	acceptEnv(t)
	f := newPlanner(t)
	commitFile(t, f.repo, "a.txt", "one\ntwo\nthree\n")
	f.m.mu.Lock()
	info := f.m.infos["fake"]
	info.Models = []agentapi.Model{{ID: "luna", Name: "Luna"}}
	f.m.infos["fake"] = info
	f.m.mu.Unlock()
	f.setAcceptCmd(cmd)
	r := &laneRun{plannerFixture: f, epic: f.create(board.KindEpic, "", "Epic")}
	story := f.create(board.KindStory, r.epic.ID, "Story")
	refs := []string{r.epic.ID, story.ID}
	for _, title := range titles {
		leaf := f.create(board.KindSubtask, story.ID, title)
		r.leaves = append(r.leaves, leaf)
		refs = append(refs, leaf.ID)
	}
	f.call(http.MethodPost, "/api/board/cards/"+r.epic.ID+"/approve", f.approveBody("luna", refs...), http.StatusOK, nil)
	return r
}

// start launches the lane of subtask i and returns its Task and lane.
func (r *laneRun) start(i int) (SessionSummary, lane) {
	r.t.Helper()
	c, task := r.launch(r.leaves[i].ID)
	if c.Lane == nil || c.Lane.Branch == "" || c.Status != board.StatusDoing || c.HeldBy != task.ID {
		r.t.Fatalf("lane start = %+v, lane %+v", c, c.Lane)
	}
	l, err := laneOf(r.m.lanesRoot(), r.project, c.Lane.Branch)
	if err != nil {
		r.t.Fatal(err)
	}
	if task.Workdir != l.dir {
		r.t.Fatalf("the lane Task works in %s, want %s", task.Workdir, l.dir)
	}
	return task, l
}

func (r *laneRun) integ() string { return integBranch(r.project) }

// tip is the integration branch's commit.
func (r *laneRun) tip() string {
	r.t.Helper()
	return gitOutput(r.t, r.repo, "rev-parse", r.integ())
}

func (r *laneRun) doneArgs(i int) string {
	return fmt.Sprintf(`{"ref":%q,"kind":"done","comment":"did %s"}`, r.leaves[i].ID, r.leaves[i].Title)
}

// landed requires subtask i's done claim from task to land, and returns
// the landing commit.
func (r *laneRun) landed(task SessionSummary, i int) string {
	r.t.Helper()
	reply := r.toolOK(task.ID, "board_request", r.doneArgs(i))
	tip := r.tip()
	want := fmt.Sprintf("#%d is done and landed on %s as %s. You are finished; end your turn.", r.leaves[i].Seq, r.integ(), shortSHA(tip))
	if reply.Text != want || reply.Card == nil || reply.Card.Status != board.StatusDone {
		r.t.Fatalf("done = %+v, want %q", reply, want)
	}
	return tip
}

// queueLanding has git refuse to move the integration branch while fn
// runs: a worktree has it checked out.
func (r *laneRun) queueLanding(fn func()) {
	r.t.Helper()
	held := filepath.Join(r.t.TempDir(), "held")
	gitIn(r.t, r.repo, "worktree", "add", "-q", held, r.integ())
	fn()
	gitIn(r.t, r.repo, "worktree", "remove", "--force", held)
}

// gitTry runs git in dir as gitOutput does and returns its error.
func gitTry(t *testing.T, dir string, args ...string) error {
	t.Helper()
	git, err := execpath.Resolve("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	cmd := exec.Command(git, append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, out.String())
	}
	return nil
}

func gone(path string) bool {
	_, err := os.Stat(path)
	return os.IsNotExist(err)
}

func requestEvidence(t *testing.T, r BoardRequest) Evidence {
	t.Helper()
	var ev Evidence
	if err := json.Unmarshal(r.Evidence, &ev); err != nil {
		t.Fatal(err)
	}
	return ev
}

// A done claim in a lane commits what was left, and lands the lane as one
// commit on the integration branch, whose trailers name the card and the
// request; the subtask is done and its attempt keeps the commit.
func TestLaneDoneLandsOneCommit(t *testing.T) {
	r := newLaneRun(t, "test -f b.txt && test -f c.txt", "Add b")
	task, l := r.start(0)
	if got := gitOutput(t, l.dir, "rev-parse", "--abbrev-ref", "HEAD"); got != l.branch {
		t.Fatalf("the lane is on %s, want %s", got, l.branch)
	}
	main, before := gitOutput(t, r.repo, "rev-parse", "main"), r.tip()
	commitFile(t, l.dir, "b.txt", "b\n")
	commitFile(t, l.dir, "b.txt", "bb\n")
	writeRepoFile(t, l.dir, "c.txt", "left over\n")
	tip := r.landed(task, 0)

	if got := gitOutput(t, r.repo, "rev-list", "--count", before+".."+tip); got != "1" || gitOutput(t, r.repo, "rev-parse", tip+"^") != before {
		t.Fatalf("landed %s commits on %s", got, before)
	}
	d := r.card(r.leaves[0].ID)
	req := d.Requests[0]
	if got := gitOutput(t, r.repo, "log", "-1", "--format=%s%n%(trailers:key=Uam-Card,valueonly)%(trailers:key=Uam-Request,valueonly)", tip); got != fmt.Sprintf("Add b (#%d)\n#%d\n%s", r.leaves[0].Seq, r.leaves[0].Seq, req.ID) {
		t.Fatalf("landing commit = %q", got)
	}
	if got := gitOutput(t, r.repo, "show", "--format=", "--name-only", tip); got != "b.txt\nc.txt" {
		t.Fatalf("landed files = %q", got)
	}
	if gitOutput(t, r.repo, "rev-parse", "main") != main {
		t.Fatal("landing moved the owner's branch")
	}
	if d.Card.Status != board.StatusDone || d.Card.Lane == nil || d.Card.Lane.LandedSHA != tip || d.Holds[0].LandedSHA != tip || d.Holds[0].Branch != l.branch ||
		req.Status != board.RequestAccepted || req.DecidedBy != board.AuthorUAM || !slices.Contains(comments(d), fmt.Sprintf("uam: Landed on %s as %s", r.integ(), shortSHA(tip))) {
		t.Fatalf("landed card = %+v, holds %+v, request %+v, comments %q", d.Card, d.Holds, req, comments(d))
	}
	var p BoardProject
	r.call(http.MethodGet, "/api/board/projects/"+r.project, "", http.StatusOK, &p)
	if p.AcceptParallel != 1 || p.Integration == nil || *p.Integration != (BoardIntegration{Branch: r.integ(), BaseRef: "main", Ahead: 1}) {
		t.Fatalf("project = %+v, integration %+v", p, p.Integration)
	}
	r.call(http.MethodPatch, "/api/board/projects/"+r.project, `{"accept_parallel":2}`, http.StatusOK, &p)
	if p.AcceptParallel != 2 || p.AcceptCmd != "test -f b.txt && test -f c.txt" {
		t.Fatalf("patched project = %+v", p)
	}
	r.refused(http.MethodPatch, "/api/board/projects/"+r.project, `{"accept_parallel":9}`, http.StatusBadRequest, string(board.CodeInvalid))
	r.refused(http.MethodPatch, "/api/board/projects/"+r.project, `{"base_ref":"nope"}`, http.StatusConflict, "")
}

// A claim whose lane does not merge with the integration tip is refused
// with the files and the agent's steps, and nothing is filed; once the agent
// merged, it lands.
func TestLaneDoneRefusedOnConflict(t *testing.T) {
	r := newLaneRun(t, "true", "First", "Second")
	one, l1 := r.start(0)
	two, l2 := r.start(1)
	commitFile(t, l1.dir, "a.txt", "ONE\ntwo\nthree\n")
	commitFile(t, l2.dir, "a.txt", "uno\ntwo\nthree\n")
	r.landed(one, 0)
	head := gitOutput(t, l2.dir, "rev-parse", "HEAD")
	reply := r.toolRefused(two.ID, "board_request", r.doneArgs(1), codeLandConflict)
	if !strings.Contains(reply.Text, "git merge "+r.integ()) || !strings.Contains(reply.Text, "a.txt") || !slices.Contains(reply.Refs, fmt.Sprintf("#%d", r.leaves[0].Seq)) {
		t.Fatalf("conflict = %+v", reply)
	}
	if d := r.card(r.leaves[1].ID); len(d.Requests) != 0 || d.Card.Status != board.StatusDoing || d.Card.HeldBy != two.ID {
		t.Fatalf("after the conflict = %+v, requests %+v", d.Card, d.Requests)
	}
	if gitOutput(t, l2.dir, "rev-parse", "HEAD") != head || gitTry(t, l2.dir, "rev-parse", "--verify", "--quiet", "MERGE_HEAD") == nil {
		t.Fatal("the refused claim left the lane moved or mid-merge")
	}
	if err := gitTry(t, l2.dir, "merge", r.integ()); err == nil {
		t.Fatal("the agent's merge did not conflict")
	}
	writeRepoFile(t, l2.dir, "a.txt", "ONE, uno\ntwo\nthree\n")
	gitIn(t, l2.dir, "commit", "-q", "-am", "merge")
	r.landed(two, 1)
	if got := gitOutput(t, r.repo, "show", r.integ()+":a.txt"); got != "ONE, uno\ntwo\nthree" {
		t.Fatalf("landed a.txt = %q", got)
	}
}

// A claim while the agent's own merge is unfinished is refused, and uam
// leaves the merge to the agent.
func TestLaneClaimRefusedMidMerge(t *testing.T) {
	r := newLaneRun(t, "true", "First", "Second")
	one, l1 := r.start(0)
	two, l2 := r.start(1)
	commitFile(t, l1.dir, "a.txt", "ONE\ntwo\nthree\n")
	commitFile(t, l2.dir, "a.txt", "uno\ntwo\nthree\n")
	r.landed(one, 0)
	if err := gitTry(t, l2.dir, "merge", r.integ()); err == nil {
		t.Fatal("the agent's merge did not conflict")
	}
	reply := r.toolRefused(two.ID, "board_request", r.doneArgs(1), codeLandConflict)
	if !strings.Contains(reply.Text, "finish your merge of "+r.integ()) || !strings.Contains(reply.Text, "a.txt") {
		t.Fatalf("mid-merge claim = %+v", reply)
	}
	if len(r.card(r.leaves[1].ID).Requests) != 0 || gitTry(t, l2.dir, "rev-parse", "--verify", "--quiet", "MERGE_HEAD") != nil {
		t.Fatal("the claim filed a request or ended the agent's merge")
	}
}

// A landing whose tip moved since the claim merges the new tip into the lane
// and runs the acceptance command again: red, it rejects the request and
// tells the Task why.
func TestLaneLandRerunsAcceptanceWhenTipMoved(t *testing.T) {
	r := newLaneRun(t, "true", "First", "Second")
	r.call(http.MethodPatch, "/api/board/cards/"+r.leaves[1].ID, `{"accept_cmd":"test ! -f b.txt"}`, http.StatusOK, nil)
	one, l1 := r.start(0)
	two, l2 := r.start(1)
	commitFile(t, l1.dir, "b.txt", "b\n")
	commitFile(t, l2.dir, "c.txt", "c\n")
	r.queueLanding(func() {
		reply := r.toolOK(two.ID, "board_request", r.doneArgs(1))
		if !strings.Contains(reply.Text, "landing is queued") || !strings.Contains(reply.Text, "End your turn.") {
			t.Fatalf("queued = %q", reply.Text)
		}
	})
	req := r.card(r.leaves[1].ID).Requests[0]
	if req.Status != board.RequestPending || !strings.Contains(string(req.Payload), `"landing":true`) {
		t.Fatalf("queued request = %+v", req)
	}
	first := r.landed(one, 0)
	r.idle(two.ID)
	sends := len(r.conversation(two.ID).Sends())
	var job struct {
		JobID string `json:"job_id"`
	}
	r.call(http.MethodPost, "/api/board/requests/"+req.ID+"/accept", `{}`, http.StatusAccepted, &job)
	waitUntil(t, "the landing to fail", func() bool { return r.card(r.leaves[1].ID).Requests[0].Status == board.RequestRejected })
	d := r.card(r.leaves[1].ID)
	if d.Requests[0].DecidedBy != board.AuthorUAM || !strings.Contains(d.Requests[0].DecisionComment, "exited 1") ||
		d.Card.Status != board.StatusDoing || d.Card.HeldBy != two.ID || r.tip() != first {
		t.Fatalf("after the red re-run = %+v, request %+v, tip %s", d.Card, d.Requests[0], r.tip())
	}
	waitUntil(t, "the Task to be told", func() bool {
		s := r.conversation(two.ID).Sends()
		return len(s) > sends && strings.Contains(s[len(s)-1], "exited 1")
	})
}

// A lane carrying a landing the integration branch no longer has cannot
// land.
func TestLaneLandRefusesStaleLane(t *testing.T) {
	r := newLaneRun(t, "true", "Leaf")
	task, l := r.start(0)
	writeRepoFile(t, l.dir, "x.txt", "x\n")
	gitIn(t, l.dir, "add", "-A")
	gitIn(t, l.dir, "commit", "-q", "-m", "Elsewhere (#9)\n\nUam-Card: #9\nUam-Request: gone")
	reply := r.toolRefused(task.ID, "board_request", r.doneArgs(0), codeLandStale)
	if !strings.Contains(reply.Text, "end your turn") || len(r.card(r.leaves[0].ID).Requests) != 0 {
		t.Fatalf("stale lane = %+v", reply)
	}
}

// Archiving the holder while its landing commits leaves the ref and the
// store agreeing: the commit phase runs to its end.
func TestLandCommitPhaseSurvivesArchive(t *testing.T) {
	r := newLaneRun(t, "true", "Leaf")
	task, l := r.start(0)
	commitFile(t, l.dir, "b.txt", "b\n")
	r.idle(task.ID)
	before := r.tip()
	archived := make(chan error, 1)
	r.m.landHook = func(stage string) {
		if stage != landIntent {
			return
		}
		go func() { _, err := r.m.Archive(task.ID); archived <- err }()
		waitUntil(t, "the Task to be archived", func() bool { s, _ := r.m.Summary(task.ID); return s.Stage == StageArchived })
	}
	r.callTool(task.ID, "", "board_request", r.doneArgs(0))
	if err := <-archived; err != nil {
		t.Fatal(err)
	}
	tip := r.tip()
	d := r.card(r.leaves[0].ID)
	if tip == before || d.Card.Status != board.StatusDone || d.Card.Lane.LandedSHA != tip || d.Requests[0].Status != board.RequestAccepted {
		t.Fatalf("after archive in the commit phase: tip %s, card %+v, request %+v", tip, d.Card, d.Requests[0])
	}
}

// A compare-and-swap lost to another writer withdraws the intent: the
// request stays pending, to land later.
func TestLandCASFailureClearsIntent(t *testing.T) {
	r := newLaneRun(t, "true", "Leaf")
	task, l := r.start(0)
	commitFile(t, l.dir, "b.txt", "b\n")
	var moved string
	r.m.landHook = func(stage string) {
		if stage != landIntent {
			return
		}
		if c := r.card(r.leaves[0].ID).Card; c.Lane == nil || c.Lane.LandedSHA == "" {
			t.Errorf("no intent before the ref moves: %+v", c.Lane)
		}
		tip := r.tip()
		moved = gitOutput(t, r.repo, "commit-tree", tip+"^{tree}", "-p", tip, "-m", "by hand")
		gitIn(t, r.repo, "update-ref", "refs/heads/"+r.integ(), moved)
	}
	reply := r.toolOK(task.ID, "board_request", r.doneArgs(0))
	if !strings.Contains(reply.Text, "landing is queued") {
		t.Fatalf("lost compare-and-swap = %q", reply.Text)
	}
	d := r.card(r.leaves[0].ID)
	if r.tip() != moved || d.Card.Status != board.StatusDoing || d.Card.Lane.LandedSHA != "" || d.Requests[0].Status != board.RequestPending {
		t.Fatalf("after the lost compare-and-swap: tip %s, card %+v, lane %+v, request %+v", r.tip(), d.Card, d.Card.Lane, d.Requests[0])
	}
}

// A landing intent a crash left is finished when the planner opens: the
// ref is fast-forwarded when it did not move yet, and the store finalized;
// an intent the integration branch diverged from is withdrawn.
func TestBootFinishesLandingIntent(t *testing.T) {
	for _, tc := range []struct {
		name  string
		moved bool
		lands bool
	}{{"ref moved", true, true}, {"ref not moved", false, true}, {"diverged", false, false}} {
		t.Run(tc.name, func(t *testing.T) {
			r := newLaneRun(t, "true", "Leaf")
			task, l := r.start(0)
			commitFile(t, l.dir, "b.txt", "b\n")
			r.queueLanding(func() { r.toolOK(task.ID, "board_request", r.doneArgs(0)) })
			req := r.card(r.leaves[0].ID).Requests[0]
			ctx := context.Background()
			repo, err := openLanes(ctx, r.project, r.repo)
			if err != nil {
				t.Fatal(err)
			}
			tip := r.tip()
			sha, err := repo.squashLane(ctx, l, tip, landMessage(r.leaves[0].Title, r.leaves[0].Seq, "did it", req.ID))
			if err != nil {
				t.Fatal(err)
			}
			r.store(func(ctx context.Context, st *board.Store) error { return st.MarkLanding(ctx, req.ID, sha) })
			switch {
			case tc.moved:
				gitIn(t, r.repo, "update-ref", "refs/heads/"+r.integ(), sha, tip)
			case !tc.lands:
				other := gitOutput(t, r.repo, "commit-tree", tip+"^{tree}", "-p", tip, "-m", "by hand")
				gitIn(t, r.repo, "update-ref", "refs/heads/"+r.integ(), other, tip)
			}
			r.m.closeBoard()
			if err := r.m.openBoard(ctx); err != nil {
				t.Fatal(err)
			}
			d := r.card(r.leaves[0].ID)
			if tc.lands {
				if r.tip() != sha || d.Card.Status != board.StatusDone || d.Card.Lane.LandedSHA != sha || d.Requests[0].Status != board.RequestAccepted {
					t.Fatalf("after boot: tip %s, card %+v, lane %+v, request %+v", r.tip(), d.Card, d.Card.Lane, d.Requests[0])
				}
				return
			}
			if d.Card.Status != board.StatusDoing || d.Card.Lane.LandedSHA != "" || d.Requests[0].Status != board.RequestPending {
				t.Fatalf("after boot, diverged: card %+v, lane %+v, request %+v", d.Card, d.Card.Lane, d.Requests[0])
			}
		})
	}
}

// A lane's evidence is its own work on top of the integration tip: not the
// owner's uncommitted files, nor another lane's work.
func TestLaneEvidenceIsTheLaneOnly(t *testing.T) {
	r := newLaneRun(t, "true", "First", "Second")
	one, l1 := r.start(0)
	two, l2 := r.start(1)
	writeRepoFile(t, r.repo, "owner.txt", "the owner's\n")
	commitFile(t, l1.dir, "b.txt", "b\n")
	commitFile(t, l2.dir, "c.txt", "c\n")
	r.landed(one, 0)
	r.landed(two, 1)
	for i, want := range []string{"b.txt", "c.txt"} {
		ev := requestEvidence(t, r.card(r.leaves[i].ID).Requests[0])
		if len(ev.Diff.Files) != 1 || ev.Diff.Files[0].Path != want || ev.Diff.Files[0].Overlap != nil || len(ev.Commits) == 0 {
			t.Fatalf("evidence of #%d = %+v, commits %+v", r.leaves[i].Seq, ev.Diff, ev.Commits)
		}
	}
}

// A lane's change to test or build files does not hold its done request
// (ADR 0006 §2 item 9): it lands at once, and the flag stays on the
// accepted request in the card's JSON, for the owner to see.
func TestLaneDoneRecordsTestsOrBuildChanged(t *testing.T) {
	r := newLaneRun(t, "true", "Cover it")
	task, l := r.start(0)
	commitFile(t, l.dir, "x_test.go", "package x\n")
	r.landed(task, 0)
	reqs := r.card(r.leaves[0].ID).Requests
	if len(reqs) != 1 || reqs[0].Status != board.RequestAccepted || reqs[0].DecidedBy != board.AuthorUAM ||
		!slices.Equal(reqs[0].Flags, []string{board.FlagTestsOrBuildChanged}) {
		t.Fatalf("the landed request = %+v", reqs)
	}
}

// The owner's Accept of a lane's done request lands it as a job: 202, then
// board_job frames; while the holder works it is refused.
func TestOwnerAcceptLandsAsAJob(t *testing.T) {
	r := newLaneRun(t, "true", "Leaf")
	task, _ := r.start(0)
	reply := r.toolOK(task.ID, "board_request", r.doneArgs(0))
	if !strings.Contains(reply.Text, "waits for the owner") || !strings.Contains(reply.Text, board.FlagNoChangeInTree) {
		t.Fatalf("done with nothing changed = %q", reply.Text)
	}
	req := r.card(r.leaves[0].ID).Requests[0]
	accept := "/api/board/requests/" + req.ID + "/accept"
	r.refused(http.MethodPost, accept, `{}`, http.StatusConflict, codeTaskWorking)
	r.idle(task.ID)
	sub, _, err := r.m.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	defer r.m.Unsubscribe(sub)
	var job struct {
		JobID string `json:"job_id"`
	}
	r.call(http.MethodPost, accept, `{"comment":"fine"}`, http.StatusAccepted, &job)
	running, done := jobFrame(t, sub), jobFrame(t, sub)
	if running.JobID != job.JobID || running.Kind != jobLand || running.Status != jobRunning || running.CardID != r.leaves[0].ID ||
		done.JobID != job.JobID || done.Status != jobDone {
		t.Fatalf("frames = %+v then %+v", running, done)
	}
	d := r.card(r.leaves[0].ID)
	if d.Card.Status != board.StatusDone || d.Card.Lane.LandedSHA != r.tip() || d.Requests[0].Status != board.RequestAccepted ||
		d.Requests[0].DecidedBy != board.DecidedByOwner || d.Requests[0].DecisionComment != "fine" {
		t.Fatalf("accepted = %+v, request %+v", d.Card, d.Requests[0])
	}
}

// Release of a lane hold is Stop: the subtask is paused by the owner, and
// its Task cancelled with uam's reason, archived, and its lane committed and
// removed, the branch kept.
func TestReleaseOfLaneHoldStops(t *testing.T) {
	r := newLaneRun(t, "true", "Leaf")
	task, l := r.start(0)
	writeRepoFile(t, l.dir, "wip.txt", "half\n")
	conv := r.conversation(task.ID)
	cancelled := make(chan string, 1)
	conv.SetCancelHook(func(context.Context) error {
		conv.EmitTurn(agentapi.TurnCancelled, "")
		s, _ := r.m.Summary(task.ID)
		cancelled <- s.StateDetail
		return nil
	})
	var c BoardCard
	r.call(http.MethodPost, "/api/board/cards/"+r.leaves[0].ID+"/release", `{}`, http.StatusOK, &c)
	if c.Status != board.StatusTodo || c.Paused != board.PausedOwner || c.HeldBy != "" {
		t.Fatalf("stopped = %+v", c)
	}
	waitUntil(t, "the lane to be removed", func() bool {
		s, _ := r.m.Summary(task.ID)
		return s.Stage == StageArchived && gone(l.dir)
	})
	if got, want := <-cancelled, fmt.Sprintf("uam: #%d was stopped", r.leaves[0].Seq); got != want || conv.Cancels() != 1 {
		t.Fatalf("the stopped Task's turn ended with %q after %d cancels, want %q", got, conv.Cancels(), want)
	}
	if got := gitOutput(t, r.repo, "log", "-1", "--format=%s", l.branch); got != fmt.Sprintf("#%d: work in progress", r.leaves[0].Seq) {
		t.Fatalf("the kept branch's last commit = %q", got)
	}
	want := fmt.Sprintf("uam: paused: attempt #1 stopped; branch %s kept", l.branch)
	if d := r.card(r.leaves[0].ID); !slices.Contains(comments(d), want) {
		t.Fatalf("comments = %q, want %q", comments(d), want)
	}
}

// Settle cannot keep a lane hold: it is released, which pauses the
// subtask, or cancelled.
func TestSettleOfLaneHolderReleases(t *testing.T) {
	r := newLaneRun(t, "true", "Leaf")
	task, _ := r.start(0)
	r.idle(task.ID)
	settle := "/api/sessions/" + task.ID + "/settle"
	leaf := r.leaves[0].ID
	r.refused(http.MethodPost, settle, fmt.Sprintf(`{"holds":{%q:{"action":"keep"}}}`, leaf), http.StatusBadRequest, string(board.CodeInvalid))
	if s, _ := r.m.Summary(task.ID); s.Stage != StageActive {
		t.Fatal("a refused settle settled the Task")
	}
	r.call(http.MethodPost, settle, fmt.Sprintf(`{"holds":{%q:{"action":"release"}}}`, leaf), http.StatusOK, nil)
	if d := r.card(leaf); d.Card.Status != board.StatusTodo || d.Card.Paused != board.PausedOwner || d.Holds[0].EndReason != string(board.ReleaseSettled) {
		t.Fatalf("settled = %+v, holds %+v", d.Card, d.Holds)
	}
}

// The acceptance runs of a Project, its lanes' included, run one at a time
// by default.
func TestAcceptanceLimitPerProject(t *testing.T) {
	log := filepath.Join(t.TempDir(), "runs")
	r := newLaneRun(t, fmt.Sprintf("echo start >> '%s'; sleep 0.3; echo end >> '%s'", log, log), "First", "Second")
	one, l1 := r.start(0)
	two, l2 := r.start(1)
	commitFile(t, l1.dir, "b.txt", "b\n")
	commitFile(t, l2.dir, "c.txt", "c\n")
	var wg sync.WaitGroup
	for i, task := range []SessionSummary{one, two} {
		call, args := r.conversation(task.ID).Request().CallTool, json.RawMessage(r.doneArgs(i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := call(context.Background(), agentapi.HostToolCall{Name: "board_request", CallID: "c", TaskID: task.ID, Arguments: args})
			if res.Failed {
				t.Errorf("done of #%d = %s", r.leaves[i].Seq, res.Text)
			}
		}()
	}
	wg.Wait()
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(data))
	if len(lines) < 4 || len(lines)%2 != 0 {
		t.Fatalf("runs = %q", lines)
	}
	for i, line := range lines {
		if want := []string{"start", "end"}[i%2]; line != want {
			t.Fatalf("acceptance runs overlapped: %q", lines)
		}
	}
	for i := range r.leaves {
		if c := r.card(r.leaves[i].ID).Card; c.Status != board.StatusDone {
			t.Fatalf("#%d = %s", c.Seq, c.Status)
		}
	}
}

// A lane's acceptance run killed by the timeout files the claim flagged, for
// the owner, rather than refusing it as red.
func TestAcceptanceTimeoutIsFlagged(t *testing.T) {
	r := newLaneRun(t, "sleep 5", "Leaf")
	r.m.accept.timeout = 300 * time.Millisecond
	task, l := r.start(0)
	commitFile(t, l.dir, "b.txt", "b\n")
	reply := r.toolOK(task.ID, "board_request", r.doneArgs(0))
	if !strings.Contains(reply.Text, "waits for the owner") || !strings.Contains(reply.Text, board.FlagAcceptanceCouldNotRun) {
		t.Fatalf("timed out = %q", reply.Text)
	}
	if req := r.card(r.leaves[0].ID).Requests[0]; req.Status != board.RequestPending || !slices.Contains(req.Flags, board.FlagAcceptanceCouldNotRun) {
		t.Fatalf("request = %+v", req)
	}
}

// Archiving a lane Task commits what its lane left to the attempt branch
// and removes the lane; the branch goes too once its work landed.
func TestArchivedLaneIsCommittedAndRemoved(t *testing.T) {
	r := newLaneRun(t, "true", "Kept", "Landed")
	kept, lk := r.start(0)
	landed, ll := r.start(1)
	writeRepoFile(t, lk.dir, "wip.txt", "half\n")
	commitFile(t, ll.dir, "b.txt", "b\n")
	r.landed(landed, 1)
	for _, task := range []SessionSummary{kept, landed} {
		r.idle(task.ID)
		if _, err := r.m.Archive(task.ID); err != nil {
			t.Fatal(err)
		}
	}
	waitUntil(t, "the lanes to be removed", func() bool { return gone(lk.dir) && gone(ll.dir) })
	if subject, files := gitOutput(t, r.repo, "log", "-1", "--format=%s", lk.branch), gitOutput(t, r.repo, "show", "--format=", "--name-only", lk.branch); subject != fmt.Sprintf("#%d: work in progress", r.leaves[0].Seq) || files != "wip.txt" {
		t.Fatalf("kept branch = %q with %q", subject, files)
	}
	waitUntil(t, "the landed branch to be deleted", func() bool {
		return gitTry(t, r.repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+ll.branch) != nil
	})
	if list := gitOutput(t, r.repo, "worktree", "list"); strings.Contains(list, r.m.lanesRoot()) {
		t.Fatalf("worktrees = %s", list)
	}
}

// Lanes sit outside the Project directory, so a lane Task at work never
// blocks the owner's commit.
func TestOwnerCommitNotBlockedByLaneTasks(t *testing.T) {
	r := newLaneRun(t, "true", "Leaf")
	task, _ := r.start(0)
	if s, _ := r.m.Summary(task.ID); !busy(s.State) {
		t.Fatalf("the lane Task is %s, want it working", s.State)
	}
	owner := r.newTask(r.project)
	writeRepoFile(t, r.repo, "notes.txt", "x\n")
	if _, err := r.m.GitCommit(context.Background(), owner.ID, []string{"notes.txt"}, "docs: notes"); err != nil {
		t.Fatal(err)
	}
}

// A lane Task gets only the run's tools: no planning, claiming or
// uam_create_task. It is told it works on its subtask alone, in its lane,
// and its git panel neither pushes nor pulls. Only uam makes a Task in the
// lanes.
func TestLaneTaskGetsTheRunToolSubset(t *testing.T) {
	r := newLaneRun(t, "true", "Leaf")
	tip := r.tip()
	task, l := r.start(0)
	conv := r.conversation(task.ID)
	if got := toolNames(conv.Request().Tools); !slices.Equal(got, append(slices.Clone(runTools), chartToolName)) {
		t.Fatalf("lane tools = %v", got)
	}
	want := fmt.Sprintf("You work only on #%d, in your own git worktree on branch %s, made from %s at %s.", r.leaves[0].Seq, l.branch, r.integ(), shortSHA(tip))
	if sends := conv.Sends(); len(sends) != 1 || !strings.Contains(sends[0], want) {
		t.Fatalf("preamble = %q, want %q", sends, want)
	}
	ctx := context.Background()
	for name, action := range map[string]func(context.Context, string) (GitResult, error){"push": r.m.GitPush, "pull": r.m.GitPull} {
		if _, err := action(ctx, task.ID); err == nil || !strings.Contains(err.Error(), "lane") {
			t.Fatalf("%s from a lane = %v", name, err)
		}
	}
	if _, err := r.m.Create(CreateRequest{Provider: "fake", ProjectID: r.project, workdir: t.TempDir()}); err == nil {
		t.Fatal("a Task was made outside the lanes")
	}
}

// Lane directories never push the owner's folders out of the recent list.
func TestLaneWorkdirsNotInRecent(t *testing.T) {
	r := newLaneRun(t, "true", "Leaf")
	task, _ := r.start(0)
	plain := r.newTask(r.project)
	recent := r.m.RecentWorkdirs()
	if slices.Contains(recent, task.Workdir) || !slices.Contains(recent, plain.Workdir) {
		t.Fatalf("recent = %v", recent)
	}
}

// A lane attempt that ends without landing pauses the subtask and says so,
// without the owner's uncommitted files, which are not the lane's.
func TestLaneEndedCommentListsNoOwnerFiles(t *testing.T) {
	r := newLaneRun(t, "true", "Leaf")
	task, l := r.start(0)
	writeRepoFile(t, r.repo, "owner-notes.txt", "mine\n")
	r.idle(task.ID)
	if _, err := r.m.Archive(task.ID); err != nil {
		t.Fatal(err)
	}
	d := r.card(r.leaves[0].ID)
	want := fmt.Sprintf("uam: paused: attempt #1 ended without landing; branch %s kept", l.branch)
	if d.Card.Paused != board.PausedUAM || !slices.Contains(comments(d), want) {
		t.Fatalf("ended = %+v, comments %q", d.Card, comments(d))
	}
	for _, c := range comments(d) {
		if strings.Contains(c, "owner-notes.txt") || strings.Contains(c, "uncommitted") {
			t.Fatalf("comment %q names the owner's files", c)
		}
	}
}

// When the base brings what conflicts with landed work, Approve refuses
// with the files, and a lane starts without it, which the epic notes once.
func TestLaneBaseConflictRefusesApproveAndIsNoted(t *testing.T) {
	r := newLaneRun(t, "true", "First", "Second")
	one, l1 := r.start(0)
	commitFile(t, l1.dir, "a.txt", "ONE\ntwo\nthree\n")
	r.landed(one, 0)
	main := commitFile(t, r.repo, "a.txt", "uno\ntwo\nthree\n")
	tip := r.tip()
	_, l2 := r.start(1)
	if got := gitOutput(t, l2.dir, "rev-parse", "HEAD"); got != tip {
		t.Fatalf("the lane started at %s, want the integration tip %s", got, tip)
	}
	want := fmt.Sprintf("uam: %s does not take main at %s, so lanes start without it", r.integ(), shortSHA(main))
	if d := r.card(r.epic.ID); !slices.ContainsFunc(comments(d), func(c string) bool { return strings.HasPrefix(c, want) }) {
		t.Fatalf("epic comments = %q, want %q", comments(d), want)
	}
	reply := r.refused(http.MethodPost, "/api/board/cards/"+r.epic.ID+"/approve", r.approveBody("luna", r.epic.ID), http.StatusConflict, codeMergeConflict)
	if !strings.Contains(string(reply["error"]), "a.txt") {
		t.Fatalf("approve on a conflicting base = %s", reply["error"])
	}
}

// Recovery sweeps what no attempt has: a lane directory is committed and
// removed, a stray attempt branch with no work deleted, and a landing on
// the integration branch whose request was not accepted noted on its card.
func TestBootSweepsLanesNoAttemptHas(t *testing.T) {
	r := newLaneRun(t, "true", "Leaf")
	task, l := r.start(0)
	commitFile(t, l.dir, "b.txt", "b\n")
	r.queueLanding(func() { r.toolOK(task.ID, "board_request", r.doneArgs(0)) })
	req := r.card(r.leaves[0].ID).Requests[0]
	ctx := context.Background()
	repo, err := openLanes(ctx, r.project, r.repo)
	if err != nil {
		t.Fatal(err)
	}
	tip := r.tip()
	orphan, err := newLane(r.m.lanesRoot(), r.project, 9)
	if err == nil {
		err = repo.addLane(ctx, orphan, tip)
	}
	stray, err2 := newLane(r.m.lanesRoot(), r.project, 8)
	if err != nil || err2 != nil {
		t.Fatal(err, err2)
	}
	writeRepoFile(t, orphan.dir, "left.txt", "left\n")
	gitIn(t, r.repo, "branch", stray.branch, tip)
	// Someone moved the integration branch to the landing by hand.
	sha, err := repo.squashLane(ctx, l, tip, landMessage(r.leaves[0].Title, r.leaves[0].Seq, "did it", req.ID))
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, r.repo, "update-ref", "refs/heads/"+r.integ(), sha, tip)
	r.m.closeBoard()
	if err := r.m.openBoard(ctx); err != nil {
		t.Fatal(err)
	}
	if !gone(orphan.dir) || gitOutput(t, r.repo, "show", "--format=", "--name-only", orphan.branch) != "left.txt" {
		t.Fatal("the orphan lane was not committed and removed")
	}
	if gitTry(t, r.repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+stray.branch) == nil {
		t.Fatal("the stray branch was kept")
	}
	if gone(l.dir) {
		t.Fatal("the held lane was swept")
	}
	want := fmt.Sprintf("uam: %s carries %s, which lands this card's request, but the request is pending", r.integ(), shortSHA(sha))
	if d := r.card(r.leaves[0].ID); !slices.ContainsFunc(comments(d), func(c string) bool { return strings.HasPrefix(c, want) }) {
		t.Fatalf("comments = %q, want %q", comments(d), want)
	}
	// A second boot notes nothing new.
	r.m.closeBoard()
	if err := r.m.openBoard(ctx); err != nil {
		t.Fatal(err)
	}
	if got := comments(r.card(r.leaves[0].ID)); strings.Count(strings.Join(got, "\n"), want) != 1 {
		t.Fatalf("a second boot noted again: %q", got)
	}
}

// uam's own git work for lanes runs no hook, even with a hooks path set in
// the repository's shared configuration from a lane: not in a lane start, a
// landing with leftovers or a merge, a lane removal, or the boot sweep.
// Hooks still run for git the owner runs.
func TestLaneGitIgnoresPlantedHooks(t *testing.T) {
	r := newLaneRun(t, "true", "First", "Second")
	one, l1 := r.start(0)
	ran, hooks := filepath.Join(t.TempDir(), "ran"), t.TempDir()
	for _, name := range []string{"reference-transaction", "post-checkout", "pre-commit", "prepare-commit-msg", "commit-msg",
		"post-commit", "pre-merge-commit", "post-merge", "post-index-change"} {
		if err := os.WriteFile(filepath.Join(hooks, name), fmt.Appendf(nil, "#!/bin/sh\necho %s >> '%s'\n", name, ran), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, l1.dir, "config", "core.hooksPath", hooks)
	noHooks := func(step string) {
		t.Helper()
		if out, err := os.ReadFile(ran); err == nil {
			t.Errorf("%s ran planted hooks:\n%s", step, out)
			_ = os.Remove(ran)
		}
	}

	two, l2 := r.start(1)
	noHooks("a lane start")
	writeRepoFile(t, l1.dir, "b.txt", "b\n")
	r.landed(one, 0)
	noHooks("a landing with leftovers")
	writeRepoFile(t, l2.dir, "c.txt", "c\n")
	r.landed(two, 1)
	noHooks("a landing that merges the moved tip")
	r.idle(one.ID)
	if _, err := r.m.Archive(one.ID); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the landed lane and its branch to go", func() bool {
		return gone(l1.dir) && gitTry(t, r.repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+l1.branch) != nil
	})
	noHooks("a lane removal")

	orphan, err := newLane(r.m.lanesRoot(), r.project, 9)
	stray, err2 := newLane(r.m.lanesRoot(), r.project, 8)
	if err != nil || err2 != nil {
		t.Fatal(err, err2)
	}
	gitIn(t, r.repo, "-c", "core.hooksPath=/dev/null", "worktree", "add", "-q", "-b", orphan.branch, orphan.dir, r.integ())
	writeRepoFile(t, orphan.dir, "left.txt", "left\n")
	gitIn(t, r.repo, "-c", "core.hooksPath=/dev/null", "branch", stray.branch, r.integ())
	r.m.closeBoard()
	if err := r.m.openBoard(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !gone(orphan.dir) || gitTry(t, r.repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+stray.branch) == nil {
		t.Fatal("the boot sweep did not run")
	}
	noHooks("the boot sweep")

	gitIn(t, r.repo, "branch", "probe")
	if _, err := os.Stat(ran); err != nil {
		t.Fatal("the planted hooks do not run for the owner's git either, so this test proves nothing")
	}
}

// Recovery leaves a Project that never had a lane alone: git keeps the
// worktrees it lists there, a missing one included, and a Project that is
// not a git repository logs nothing.
func TestBootLeavesProjectsWithoutLanesAlone(t *testing.T) {
	f := newPlanner(t)
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	away := filepath.Join(parent, "away")
	gitIn(t, f.repo, "worktree", "add", "-q", "--detach", away)
	if err := os.RemoveAll(away); err != nil {
		t.Fatal(err)
	}
	addProject(t, f.m, t.TempDir())
	logs := &lockedBuffer{}
	previous := uamlog.SetLogger(slog.New(slog.NewTextHandler(logs, nil)))
	t.Cleanup(func() { uamlog.SetLogger(previous) })
	f.m.closeBoard()
	if err := f.m.openBoard(context.Background()); err != nil {
		t.Fatal(err)
	}
	if list := gitOutput(t, f.repo, "worktree", "list", "--porcelain"); !strings.Contains(list, away) {
		t.Fatalf("worktrees = %s, want %s kept", list, away)
	}
	if strings.Contains(logs.String(), "lanes failed") {
		t.Fatalf("logs = %s", logs)
	}
}
