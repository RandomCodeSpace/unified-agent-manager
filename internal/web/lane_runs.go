package web

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/board"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// Lane runs (ADR 0006 §4.4, §5.3-5.6): a subtask of an approved epic runs
// in a Task of its own, in a lane, and lands on the Project's integration
// branch. This file wires lanes.go into the planner: lane starts, the
// landing of a done claim, the owner's Accept as a job, Stop, the cleanup
// of a lane whose Task is archived, and the recovery at boot.

const (
	// codeTaskWorking refuses the owner's Accept of a lane's done request
	// while its Task's turn runs: landing works in the Task's lane.
	codeTaskWorking = "task_working"
	// jobLand is the owner's Accept of a lane's done request (board_ai.go).
	jobLand = "land"
	// landIntent is the landing step after the intent is stored and before
	// the integration branch moves, for landHook.
	landIntent = "intent"
	// laneStopWait bounds how long Stop waits for the stopped Task's turn to
	// end before archiving it.
	laneStopWait = 2 * time.Minute
	// maxLandTail bounds the acceptance output a failed landing quotes.
	maxLandTail = 2 << 10
)

// runTools are the only planner tools a lane Task gets (ADR 0006 §5.3): it
// works on its one subtask, and plans, claims and creates Tasks nowhere.
var runTools = []string{"board_get", "board_list", "board_checklist", "board_comment", "board_request"}

// laneState is the web side of lanes. land holds each Project's land mutex,
// which serializes every read-then-write of its integration branch; landing
// marks the cards a land call is in flight for; wg counts the lane work
// running in the background, which Shutdown waits for.
type laneState struct {
	mu      sync.Mutex
	land    map[string]chan struct{}
	landing map[string]bool
	wg      sync.WaitGroup
}

// lockLand takes the Project's land mutex, or gives up when ctx ends.
func (m *Manager) lockLand(ctx context.Context, project string) (func(), error) {
	m.lanes.mu.Lock()
	if m.lanes.land == nil {
		m.lanes.land = map[string]chan struct{}{}
	}
	mu := m.lanes.land[project]
	if mu == nil {
		mu = make(chan struct{}, 1)
		m.lanes.land[project] = mu
	}
	m.lanes.mu.Unlock()
	select {
	case mu <- struct{}{}:
		return func() { <-mu }, nil
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

// markLanding marks a land call in flight for card, refusing while
// another one is.
func (m *Manager) markLanding(card board.Card) error {
	m.lanes.mu.Lock()
	defer m.lanes.mu.Unlock()
	if m.lanes.landing[card.ID] {
		return &Error{Status: http.StatusConflict, Code: string(board.CodeLanding), Message: fmt.Sprintf("#%d is landing already; wait for it", card.Seq)}
	}
	if m.lanes.landing == nil {
		m.lanes.landing = map[string]bool{}
	}
	m.lanes.landing[card.ID] = true
	return nil
}

func (m *Manager) unmarkLanding(card string) {
	m.lanes.mu.Lock()
	delete(m.lanes.landing, card)
	m.lanes.mu.Unlock()
}

// goLanes runs fn in the background, bound to the service, unless the
// service shuts down.
func (m *Manager) goLanes(fn func(ctx context.Context)) {
	m.mu.Lock()
	closed := m.closed
	if !closed {
		m.lanes.wg.Add(1)
	}
	m.mu.Unlock()
	if closed {
		return
	}
	go func() {
		defer m.lanes.wg.Done()
		fn(m.ctx)
	}()
}

// inLanes reports whether dir is a lane, under the lanes root. uam makes
// every lane path from the root itself, so the check is lexical.
func (m *Manager) inLanes(dir string) bool {
	if dir == "" || m.store == nil {
		return false
	}
	root, dir := m.lanesRoot(), filepath.Clean(dir)
	return dir != root && inDir(root, dir)
}

// laneOfDir is the Project and the lane a lane Task's workdir is in.
func (m *Manager) laneOfDir(workdir string) (string, lane, bool) {
	if !m.inLanes(workdir) {
		return "", lane{}, false
	}
	rel, err := filepath.Rel(m.lanesRoot(), filepath.Clean(workdir))
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if err != nil || len(parts) < 2 {
		return "", lane{}, false
	}
	l, err := laneOf(m.lanesRoot(), parts[0], integBranch(parts[0])+"-"+parts[1])
	return parts[0], l, err == nil
}

// laneSeq is the #seq an attempt branch names.
func laneSeq(project, branch string) int64 {
	part, _ := strings.CutPrefix(branch, integBranch(project)+"-")
	seq, _, _ := strings.Cut(part, "-")
	n, _ := strconv.ParseInt(seq, 10, 64)
	return n
}

// holderBusy reports whether the Task id's turn runs or waits for input.
func (m *Manager) holderBusy(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	return s != nil && busy(s.state())
}

// taskActive reports whether the Task id is in the active stage.
func (m *Manager) taskActive(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	return s != nil && !s.removed && s.stage == StageActive
}

// laneBase opens the repository of the Project project in dir for lanes
// and returns the branch its integration branch follows: the stored one, or
// the branch checked out in dir. The preflight must pass.
func (m *Manager) laneBase(ctx context.Context, project, dir string) (*laneRepo, string, error) {
	repo, err := openLanes(ctx, project, dir)
	if err != nil {
		return nil, "", err
	}
	var ps board.ProjectSettings
	if err := m.withBoard(func(st *board.Store) error {
		var err error
		ps, err = st.ProjectSettings(ctx, project)
		return err
	}); err != nil {
		return nil, "", err
	}
	base := ps.BaseRef
	if base == "" {
		if base, err = repo.currentBranch(ctx); err != nil {
			return nil, "", err
		}
	}
	if err := repo.preflight(ctx, base); err != nil {
		return nil, "", err
	}
	return repo, base, nil
}

// approvePreflight runs, under the Project's land mutex, what Approve needs
// of git (ADR 0006 §5.2): the preflight, and the sync of the integration
// branch with its base, which refuses merge_conflict. It returns the base
// branch and the land mutex's unlock.
func (m *Manager) approvePreflight(ctx context.Context, project string) (string, func(), error) {
	dir, err := m.boardDir(ctx, project)
	if err != nil {
		return "", nil, err
	}
	unlock, err := m.lockLand(ctx, project)
	if err != nil {
		return "", nil, err
	}
	repo, base, err := m.laneBase(ctx, project, dir)
	if err == nil {
		_, err = repo.syncInteg(ctx, base)
	}
	if err != nil {
		unlock()
		return "", nil, err
	}
	return base, unlock, nil
}

// startLane starts a lane at the subtask c of the approved epic (ADR 0006
// §5.3): under the Project's land mutex it syncs the integration branch,
// checks the subtask is ready, adds the lane at the integration tip, makes
// the Task in it with the run's settings, and starts the hold; then it
// sends the run's preamble. A start that fails after the Task exists ends
// the attempt as aborted, discards the Task, and removes the lane.
func (m *Manager) startLane(c, epic board.Card) (board.Card, SessionSummary, error) {
	ctx := m.ctx
	dir, err := m.boardDir(ctx, c.ProjectID)
	if err != nil {
		return c, SessionSummary{}, err
	}
	unlock, err := m.lockLand(ctx, c.ProjectID)
	if err != nil {
		return c, SessionSummary{}, err
	}
	held, summary, start, err := m.startLaneLocked(ctx, c, epic, dir)
	unlock()
	if err != nil {
		return c, SessionSummary{}, err
	}
	prompt, err := m.runPreamble(ctx, held, start)
	if err == nil {
		err = m.sendFirstPrompt(summary.ID, prompt)
	}
	if err != nil {
		m.abortLane(held, summary.ID, err)
		return c, SessionSummary{}, err
	}
	summary, err = m.Summary(summary.ID)
	return held, summary, err
}

// laneStart is where a lane started: its branch, and the integration
// branch's commit it was made from.
type laneStart struct {
	branch, integ, tip string
}

func (m *Manager) startLaneLocked(ctx context.Context, c, epic board.Card, dir string) (board.Card, SessionSummary, laneStart, error) {
	repo, base, err := m.laneBase(ctx, c.ProjectID, dir)
	if err != nil {
		return c, SessionSummary{}, laneStart{}, err
	}
	tip, err := repo.syncInteg(ctx, base)
	if code := apiCode(err); code == codeMergeConflict {
		// The lane starts without what base brought; the epic says so once
		// per base commit.
		baseTip, _ := repo.tipOf(ctx, base)
		m.noteCard(ctx, epic.ID, fmt.Sprintf("%s does not take %s at %s, so lanes start without it: %s", repo.integ, displaytext.Sanitize(base), shortSHA(baseTip), err.Error()))
		tip, err = repo.tipOf(ctx, repo.integ)
	}
	if err != nil {
		return c, SessionSummary{}, laneStart{}, err
	}
	if err := m.withBoard(func(st *board.Store) error { return st.CanStart(ctx, c.ID) }); err != nil {
		return c, SessionSummary{}, laneStart{}, err
	}
	l, err := newLane(m.lanesRoot(), c.ProjectID, c.Seq)
	if err != nil {
		return c, SessionSummary{}, laneStart{}, err
	}
	if err := os.MkdirAll(filepath.Dir(l.dir), 0o700); err != nil {
		return c, SessionSummary{}, laneStart{}, err
	}
	if err := repo.addLane(ctx, l, tip); err != nil {
		return c, SessionSummary{}, laneStart{}, err
	}
	workdir, err := laneWorkdir(l, repo.top, dir)
	if err != nil {
		m.dropLane(ctx, repo, c.ProjectID, l)
		return c, SessionSummary{}, laneStart{}, err
	}
	run := epic.Run
	create := CreateRequest{ProjectID: c.ProjectID, Provider: run.Provider, Model: run.Model, Effort: run.Effort, ContextSize: run.ContextSize,
		Mode: run.Mode, Name: clipRunes(fmt.Sprintf("#%d %s", c.Seq, c.Title), maxNameRunes), workdir: workdir}
	summary, err := m.Create(create)
	if err != nil {
		m.dropLane(ctx, repo, c.ProjectID, l)
		return c, SessionSummary{}, laneStart{}, err
	}
	m.titleBoardTask(summary.ID, create.Name)
	var held board.Card
	err = m.withBoard(func(st *board.Store) error {
		var err error
		held, err = st.StartRun(ctx, board.Owner(""), c.ID, summary.ID, board.Baseline{Head: tip, Dirty: []string{}}, board.Lane{Branch: l.branch})
		return err
	})
	if err != nil {
		// No hold was written: discarding the Task removes the lane.
		m.discardTask(summary.ID)
		return c, SessionSummary{}, laneStart{}, err
	}
	return held, summary, laneStart{branch: l.branch, integ: repo.integ, tip: tip}, nil
}

// dropLane removes the lane l whose start failed before a Task was made in
// it. The caller holds the land mutex.
func (m *Manager) dropLane(ctx context.Context, repo *laneRepo, project string, l lane) {
	if err := m.cleanLaneLocked(ctx, repo, project, l); err != nil {
		log.Warn("remove the lane of a failed start failed", "branch", l.branch, "error", err)
	}
}

// abortLane ends the lane attempt of task at c, which failed to start
// because of cause, as aborted: no pause, and the Task and its lane go.
func (m *Manager) abortLane(c board.Card, task string, cause error) {
	err := m.withBoard(func(st *board.Store) error {
		_, err := st.AbortRun(m.ctx, c.ID, task, cause.Error())
		return err
	})
	if err != nil {
		log.Warn("abort a lane start failed", "card", c.ID, "session", task, "error", err)
	}
	m.discardTask(task)
}

// runPreamble is a lane Task's first prompt (ADR 0006 §6.4).
func (m *Manager) runPreamble(ctx context.Context, held board.Card, start laneStart) (string, error) {
	p := preambleInput{card: held, held: &held, lane: &start}
	err := m.withBoard(func(st *board.Store) error {
		var err error
		p.path, err = ancestors(ctx, st, held)
		return err
	})
	return p.String(), err
}

// ancestors are c's ancestors, root first.
func ancestors(ctx context.Context, st *board.Store, c board.Card) ([]board.Card, error) {
	var path []board.Card
	for id := c.ParentID; id != ""; {
		parent, err := st.Card(ctx, id)
		if err != nil {
			return nil, err
		}
		path = append([]board.Card{parent}, path...)
		id = parent.ParentID
	}
	return path, nil
}

// laneOfHold opens the repository of the card's Project for lanes and the
// lane of the attempt h.
func (m *Manager) laneOfHold(ctx context.Context, c board.Card, h board.Hold) (*laneRepo, lane, error) {
	dir, err := m.boardDir(ctx, c.ProjectID)
	if err != nil {
		return nil, lane{}, err
	}
	repo, err := openLanes(ctx, c.ProjectID, dir)
	if err != nil {
		return nil, lane{}, err
	}
	l, err := laneOf(m.lanesRoot(), c.ProjectID, h.Lane.Branch)
	return repo, l, err
}

// laneDone is a done claim in a lane (ADR 0006 §5.4): the claim is checked
// and measured on top of the integration tip, filed, and landed in the same
// call when nothing holds it back.
func (m *Manager) laneDone(ctx context.Context, sc boardScope, in requestArgs, c board.Card, hold board.Hold) (toolReply, error) {
	task := sc.actor.TaskID
	repo, l, err := m.laneOfHold(ctx, c, hold)
	if err != nil {
		return toolReply{}, err
	}
	touched, span := m.taskWork(task, hold.StartedAt)
	res, err := evaluateClaim(ctx, boardClaims{m}, m.acceptIn(ctx, c.ProjectID), claimInput{
		Actor: sc.actor, Ref: c.ID, Dir: sc.dir, Comment: in.Comment, ProposedAcceptCmd: in.ProposedAcceptCmd,
		Touched: touched, Transcript: span, Lane: true,
		Prepare: func(ctx context.Context, card board.Card) (board.Baseline, error) {
			return prepareLaneClaim(ctx, repo, l, card.Seq)
		},
	})
	if err != nil {
		return toolReply{}, err
	}
	if err := m.markLanding(c); err != nil {
		return toolReply{}, err
	}
	defer m.unmarkLanding(c.ID)
	var filed board.FiledRequest
	if err := m.withBoard(func(st *board.Store) error {
		var err error
		filed, err = st.FileRequestDetail(ctx, sc.actor, c.ID, res.Request)
		return err
	}); err != nil {
		return toolReply{}, err
	}
	if filed.Wait != board.WaitLanding {
		return cardReply(c, "%s", waitingText(c, filed, res)), nil
	}
	out, err := m.landAndAccept(ctx, filed.Request.ID, board.DecidedByUAM, "", true)
	switch {
	case err != nil:
		return toolReply{}, err
	case out.queued != "":
		return cardReply(c, "Filed a done request on #%d; landing is queued: %s. uam lands it once that clears. End your turn.", c.Seq, out.queued), nil
	}
	c.Status = board.StatusDone
	return cardReply(c, "#%d is done and landed on %s as %s. You are finished; end your turn.", c.Seq, repo.integ, shortSHA(out.sha)), nil
}

// prepareLaneClaim readies the lane l for a claim at #seq and returns the
// baseline its evidence is measured from, the integration tip: it refuses
// while the agent's merge is unfinished, commits what is left, refuses a
// stale lane, and merges the tip in.
func prepareLaneClaim(ctx context.Context, repo *laneRepo, l lane, seq int64) (board.Baseline, error) {
	if err := repo.notMerging(ctx, l); err != nil {
		return board.Baseline{}, err
	}
	if _, err := repo.commitLeftovers(ctx, l, seq); err != nil {
		return board.Baseline{}, err
	}
	tip, err := repo.tipOf(ctx, repo.integ)
	if err != nil {
		return board.Baseline{}, err
	}
	if tip == "" {
		return board.Baseline{}, newError(http.StatusConflict, "there is no branch %s", repo.integ)
	}
	if err := repo.checkLane(ctx, l, tip); err != nil {
		return board.Baseline{}, err
	}
	if err := repo.mergeTip(ctx, l, tip); err != nil {
		return board.Baseline{}, err
	}
	return board.Baseline{Head: tip, Dirty: []string{}}, nil
}

// landOutcome is a landing that did not fail: landed as sha, or queued,
// with why, its request still pending.
type landOutcome struct {
	sha    string
	queued string
}

// landFailure is a landing that cannot apply as the lane is (ADR 0006
// §4.5): the request is rejected with it.
type landFailure struct{ err error }

func (f landFailure) Error() string { return f.err.Error() }
func (f landFailure) Unwrap() error { return f.err }

// landAndAccept lands the pending lane done request id and accepts it as
// by, with the owner's note comment (ADR 0006 §5.4), under the Project's
// land mutex. Preparation runs on ctx: re-checking the lane, and, when the
// integration tip moved since the claim, merging it in and running the
// acceptance command again. The commit phase runs to its end whatever
// happens to ctx: the intent is stored, the branch moved by
// compare-and-swap, and the request accepted. A content failure rejects the
// request, tells the Task when the call is not its own, and is returned; a
// transient one leaves the request pending, queued.
func (m *Manager) landAndAccept(ctx context.Context, id string, by board.DecidedBy, comment string, own bool) (landOutcome, error) {
	r, c, hold, err := m.landingRequest(ctx, id)
	if err != nil {
		return landOutcome{}, err
	}
	repo, l, err := m.laneOfHold(ctx, c, hold)
	if err != nil {
		return landOutcome{}, err
	}
	unlock, err := m.lockLand(ctx, c.ProjectID)
	if err != nil {
		return landOutcome{}, err
	}
	defer unlock()
	if r, c, hold, err = m.landingRequest(ctx, id); err != nil {
		return landOutcome{}, err
	}
	if hold.Lane.LandedSHA != "" {
		out, done, err := m.finishLanding(ctx, repo, r, hold.Lane.LandedSHA, by, comment)
		if done || err != nil {
			return out, err
		}
	}
	if !own && m.holderBusy(r.TaskID) {
		return landOutcome{queued: "its Task is working"}, nil
	}
	sha, tip, err := m.prepareLanding(ctx, repo, l, r, c)
	if err != nil {
		var failure landFailure
		if errors.As(err, &failure) {
			return landOutcome{}, m.landFailed(ctx, r, c, failure.err, own)
		}
		if code := apiCode(err); code == string(board.CodeAcceptanceBusy) {
			return landOutcome{queued: "the acceptance runs of this project are busy"}, nil
		}
		return landOutcome{}, err
	}

	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), controlTimeout)
	defer cancel()
	err = m.withBoard(func(st *board.Store) error { return st.MarkLanding(cctx, id, sha) })
	switch code := board.CodeOf(err); {
	case code == board.CodeGuardOpenItems || code == board.CodeGuardBlocked || code == board.CodeGuardBlockers:
		return landOutcome{}, m.landFailed(cctx, r, c, err, own)
	case err != nil:
		return landOutcome{}, err
	}
	if m.landHook != nil {
		m.landHook(landIntent)
	}
	if err := repo.moveBranch(cctx, repo.integ, sha, tip); err != nil {
		if cerr := m.withBoard(func(st *board.Store) error { return st.ClearLanding(cctx, id) }); cerr != nil {
			log.Warn("withdraw a landing intent failed; recovery finishes it", "request", id, "error", cerr)
		}
		return landOutcome{queued: displaytext.Sanitize(err.Error())}, nil
	}
	if _, err := m.acceptLanded(cctx, id, sha, repo.integ, by, comment); err != nil {
		return landOutcome{}, err
	}
	return landOutcome{sha: sha}, nil
}

// landingRequest reads the pending done request id, its card and the
// lane attempt it would land.
func (m *Manager) landingRequest(ctx context.Context, id string) (board.Request, board.Card, board.Hold, error) {
	var r board.Request
	var d board.Detail
	err := m.withBoard(func(st *board.Store) error {
		var err error
		if r, err = st.Request(ctx, id); err != nil {
			return err
		}
		d, err = st.Detail(ctx, r.CardID)
		return err
	})
	if err != nil {
		return r, d.Card, board.Hold{}, err
	}
	hold, ok := openHold(d.Holds, r.TaskID)
	switch {
	case r.Status != board.RequestPending:
		return r, d.Card, hold, invalidBoard("the request is %s", r.Status)
	case r.Kind != board.RequestDone || !ok || hold.Lane.Branch == "":
		return r, d.Card, hold, invalidBoard("#%d does not run in a lane", d.Card.Seq)
	}
	return r, d.Card, hold, nil
}

// prepareLanding is the landing's preparation for the request r on c: it
// returns the landing commit, on top of the integration tip it returns. A
// content failure is a landFailure.
func (m *Manager) prepareLanding(ctx context.Context, repo *laneRepo, l lane, r board.Request, c board.Card) (string, string, error) {
	tip, err := repo.tipOf(ctx, repo.integ)
	if err != nil {
		return "", "", err
	}
	if tip == "" {
		return "", "", newError(http.StatusConflict, "there is no branch %s", repo.integ)
	}
	if err := repo.checkLane(ctx, l, tip); err != nil {
		return "", "", contentFailure(err)
	}
	head, err := repo.laneHead(ctx, l)
	if err != nil {
		return "", "", err
	}
	if has, err := repo.isAncestor(ctx, tip, head); err != nil {
		return "", "", err
	} else if !has {
		if err := m.rerunOnTip(ctx, repo, l, c, tip); err != nil {
			return "", "", err
		}
	}
	sha, err := repo.squashLane(ctx, l, tip, landMessage(c.Title, c.Seq, r.Comment, r.ID))
	return sha, tip, err
}

// rerunOnTip merges the moved integration tip into the lane and runs c's
// acceptance command on the result, which is what lands.
func (m *Manager) rerunOnTip(ctx context.Context, repo *laneRepo, l lane, c board.Card, tip string) error {
	if err := repo.mergeTip(ctx, l, tip); err != nil {
		return contentFailure(err)
	}
	var cmd string
	if err := m.withBoard(func(st *board.Store) error {
		var err error
		cmd, err = acceptCmdOf(ctx, st, c, map[string]string{})
		return err
	}); err != nil || cmd == "" {
		return err
	}
	dir, err := m.laneDir(ctx, c, l)
	if err != nil {
		return err
	}
	res, err := m.acceptIn(ctx, c.ProjectID).run(ctx, dir, cmd)
	switch {
	case errors.Is(err, errAcceptanceNotRun):
		return landFailure{&board.Error{Code: board.CodeAcceptanceFailed, Message: fmt.Sprintf(
			"the acceptance command could not run on %s at %s, merged into your lane: %s", repo.integ, shortSHA(tip), res.Tail)}}
	case err != nil:
		return err
	case res.timedOut:
		return landFailure{&board.Error{Code: board.CodeAcceptanceFailed, Message: fmt.Sprintf(
			"the acceptance command did not finish in time on %s at %s, merged into your lane", repo.integ, shortSHA(tip))}}
	case res.Exit != 0:
		return landFailure{&board.Error{Code: board.CodeAcceptanceFailed, Message: fmt.Sprintf(
			"the acceptance command exited %d on %s at %s, merged into your lane; output tail:\n%s", res.Exit, repo.integ, shortSHA(tip), clipTail(res.Tail, maxLandTail))}}
	}
	return nil
}

// laneDir is where c's lane Task works: the lane plus the Project's place
// in its repository.
func (m *Manager) laneDir(ctx context.Context, c board.Card, l lane) (string, error) {
	dir, err := m.boardDir(ctx, c.ProjectID)
	if err != nil {
		return "", err
	}
	repo, err := openEvidenceRepo(ctx, dir)
	if err != nil {
		return "", err
	}
	return laneWorkdir(l, repo.top, dir)
}

// contentFailure marks a refusal of the lane as it is as a landFailure;
// any other error is returned as it is.
func contentFailure(err error) error {
	switch apiCode(err) {
	case codeLandConflict, codeLandStale:
		return landFailure{err}
	}
	return err
}

// apiCode is err's API refusal code, "" for none.
func apiCode(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return string(board.CodeOf(err))
}

// clipTail is the last n bytes of s, at a rune boundary.
func clipTail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[len(s)-n:], "")
}

// landFailed rejects the request r on c, which cannot land because of
// cause, as uam's decision, and, when the call is not the Task's own, tells
// the Task why. It returns cause.
func (m *Manager) landFailed(ctx context.Context, r board.Request, c board.Card, cause error, own bool) error {
	active := m.taskActive(r.TaskID)
	ctx = context.WithoutCancel(ctx)
	if err := m.withBoard(func(st *board.Store) error {
		_, err := st.LandFailed(ctx, r.ID, cause.Error(), active)
		return err
	}); err != nil {
		log.Warn("reject a failed landing failed", "request", r.ID, "error", err)
	}
	if !own && active {
		text := fmt.Sprintf("uam could not land %s: %s\nFix it in your lane, then file board_request done again.", cardRef(c), cause.Error())
		if err := m.sendRejection(r.TaskID, text); err != nil {
			log.Warn("tell a lane task its landing failed", "session", r.TaskID, "error", err)
		}
	}
	return cause
}

// finishLanding finishes the landing intent sha of the request r with the
// roll-forward rule (ADR 0006 §4.4): done is true once it landed, when the
// integration branch has sha or is fast-forwarded to it. When the branch
// diverged from sha, the intent is withdrawn and done is false, to land
// again.
func (m *Manager) finishLanding(ctx context.Context, repo *laneRepo, r board.Request, sha string, by board.DecidedBy, comment string) (landOutcome, bool, error) {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), controlTimeout)
	defer cancel()
	on, err := repo.finishOnInteg(cctx, sha)
	if apiCode(err) == codeGitBusy {
		return landOutcome{queued: displaytext.Sanitize(err.Error())}, true, nil
	}
	if err != nil {
		return landOutcome{}, true, err
	}
	if !on {
		err := m.withBoard(func(st *board.Store) error { return st.ClearLanding(cctx, r.ID) })
		return landOutcome{}, false, err
	}
	if _, err := m.acceptLanded(cctx, r.ID, sha, repo.integ, by, comment); err != nil {
		return landOutcome{}, true, err
	}
	return landOutcome{sha: sha}, true, nil
}

func (m *Manager) acceptLanded(ctx context.Context, id, sha, integ string, by board.DecidedBy, comment string) (board.Request, error) {
	var r board.Request
	err := m.withBoard(func(st *board.Store) error {
		var err error
		r, err = st.AcceptLanded(ctx, id, sha, integ, by, comment)
		return err
	})
	if err != nil {
		log.Warn("record a landing failed; recovery finishes it", "request", id, "error", err)
	}
	return r, err
}

// acceptLane starts the owner's Accept of the lane's done request r on c
// as a land job (ADR 0006 §5.5) and returns its ID. It is refused while the
// Task's turn runs.
func (m *Manager) acceptLane(r board.Request, c board.Card, comment string) (string, error) {
	if m.holderBusy(r.TaskID) {
		return "", &Error{Status: http.StatusConflict, Code: codeTaskWorking,
			Message: fmt.Sprintf("the Task working on #%d is running; accept once its turn ends, or Stop it", c.Seq)}
	}
	if err := m.markLanding(c); err != nil {
		return "", err
	}
	m.mu.Lock()
	job, err := m.startJobLocked(jobLand, c)
	if err == nil {
		m.broadcastJobLocked(job, jobRunning, "", nil)
	}
	m.mu.Unlock()
	if err != nil {
		m.unmarkLanding(c.ID)
		return "", err
	}
	go m.runLand(job, r.ID, comment)
	return job.id, nil
}

// runLand runs one land job to its end, bound to the service rather than
// to the request that started it.
func (m *Manager) runLand(job *boardJob, id, comment string) {
	ctx, cancel := m.bound(context.Background())
	defer cancel()
	defer m.unmarkLanding(job.card.ID)
	out, err := m.landAndAccept(ctx, id, board.DecidedByOwner, comment, false)
	switch {
	case err != nil:
		m.endJob(job, jobFailed, clipRunes(displaytext.Sanitize(err.Error()), maxDetailRunes), nil)
	case out.queued != "":
		m.endJob(job, jobFailed, clipRunes("landing is queued: "+out.queued, maxDetailRunes), nil)
	default:
		m.endJob(job, jobDone, "", nil)
	}
}

// ReleaseCard is the owner's Release of the subtask ref. On a lane's
// subtask it is Stop (ADR 0006 §4.6): the store pauses it, and the Task is
// cancelled with uam's reason, archived, and its lane cleaned up.
func (m *Manager) ReleaseCard(ref, comment string) (BoardCard, error) {
	var before, c board.Card
	err := m.boardWrite(ref, func(ctx context.Context, st *board.Store, a board.Actor) error {
		var err error
		if before, err = st.Card(ctx, ref); err != nil {
			return err
		}
		c, err = st.ReleaseHold(ctx, a, ref, board.ReleaseOwner, comment)
		return err
	})
	if err == nil && before.Lane != nil && before.HeldBy != "" {
		task, seq := before.HeldBy, before.Seq
		m.goLanes(func(ctx context.Context) { m.stopLaneTask(ctx, task, seq) })
	}
	return boardCard(c), err
}

// stopLaneTask ends the lane Task id whose attempt at #seq was stopped: its
// running turn is cancelled with uam's reason, and once the turn ended it
// is archived, which cleans up its lane.
func (m *Manager) stopLaneTask(ctx context.Context, id string, seq int64) {
	if _, err := m.cancelBecause(id, fmt.Sprintf("uam: #%d was stopped", seq)); err != nil && m.holderBusy(id) {
		log.Warn("cancel a stopped lane task failed", "session", id, "error", err)
	}
	deadline := time.NewTimer(laneStopWait)
	defer deadline.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := m.Archive(id); err == nil || !m.taskActive(id) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			log.Warn("a stopped lane task did not end its turn; archive it by hand", "session", id)
			return
		case <-tick.C:
		}
	}
}

// cleanLaneOf cleans up the lane of the archived Task whose workdir is
// workdir, in the background; a Task outside the lanes has none.
func (m *Manager) cleanLaneOf(workdir string) {
	project, l, ok := m.laneOfDir(workdir)
	if !ok {
		return
	}
	m.goLanes(func(ctx context.Context) { m.cleanLane(ctx, project, l) })
}

// cleanLane ends the lane l of the Project project, whose Task is gone
// (ADR 0006 §5.6), under the land mutex: a merge in progress is aborted,
// what is left is committed to the attempt branch, and the worktree is
// removed. The branch is deleted when its work landed, when its start was
// aborted, or when no attempt has it and it holds no commit of its own;
// otherwise it is kept. A lane whose attempt is still open is left alone.
func (m *Manager) cleanLane(ctx context.Context, project string, l lane) {
	if err := m.cleanLaneErr(ctx, project, l); err != nil && !plannerDown(err) {
		log.Warn("clean up a lane failed", "branch", l.branch, "error", err)
	}
}

func (m *Manager) cleanLaneErr(ctx context.Context, project string, l lane) error {
	m.mu.Lock()
	p := m.projects[project]
	var dir string
	if p != nil {
		dir = p.Dir
	}
	m.mu.Unlock()
	if p == nil {
		return nil
	}
	unlock, err := m.lockLand(ctx, project)
	if err != nil {
		return err
	}
	defer unlock()
	repo, err := openLanes(ctx, project, dir)
	if err != nil {
		return err
	}
	return m.cleanLaneLocked(ctx, repo, project, l)
}

// cleanLaneLocked is cleanLane for a caller holding the land mutex.
func (m *Manager) cleanLaneLocked(ctx context.Context, repo *laneRepo, project string, l lane) error {
	var hold board.Hold
	err := m.withBoard(func(st *board.Store) error {
		var err error
		_, hold, err = st.LaneHold(ctx, l.branch)
		return err
	})
	seq := laneSeq(project, l.branch)
	switch {
	case errors.Is(err, board.ErrNotFound) || apiCode(err) == string(board.CodeNotFound):
		// A lane no attempt has: what it left is committed first, so a
		// branch holding work is kept.
		if err := repo.removeLane(ctx, l, seq, false); err != nil {
			return err
		}
		none, err := repo.ownCommitsNone(ctx, l.branch)
		if err != nil || !none {
			return err
		}
		return repo.deleteBranch(ctx, l.branch)
	case err != nil:
		return err
	case hold.EndedAt == nil:
		return nil
	}
	return repo.removeLane(ctx, l, seq, hold.Lane.LandedSHA != "" || hold.EndReason == board.ReleaseAborted)
}

// deleteBranch deletes branch, when there is one.
func (r *laneRepo) deleteBranch(ctx context.Context, branch string) error {
	if tip, err := r.tipOf(ctx, branch); err != nil || tip == "" {
		return err
	}
	if _, err := runLaneGit(ctx, gitAt{dir: r.top}, "branch", "-D", branch); err != nil {
		return gitFailed("git branch -D failed", err)
	}
	return nil
}

// ownCommitsNone reports whether the attempt branch holds no commit the
// integration branch lacks; a branch that is gone holds none.
func (r *laneRepo) ownCommitsNone(ctx context.Context, branch string) (bool, error) {
	tip, err := r.tipOf(ctx, branch)
	if err != nil || tip == "" {
		return true, err
	}
	integ, err := r.tipOf(ctx, r.integ)
	if err != nil {
		return false, err
	}
	if integ == "" {
		return false, nil
	}
	n, err := r.output(ctx, r.top, "rev-list", "--count", integ+".."+tip, "--")
	return n == "0", err
}

// noteCard adds uam's comment body to the card id once, logging a failure.
func (m *Manager) noteCard(ctx context.Context, id, body string) {
	err := m.withBoard(func(st *board.Store) error {
		_, err := st.Note(ctx, id, body)
		return err
	})
	if err != nil && !plannerDown(err) {
		log.Warn("add a planner note failed", "card", id, "error", err)
	}
}

// recoverLanes is the first lane pass after the planner opens (ADR 0006
// §4.7): it finishes the landings a crash interrupted, aborts the merges
// uam left in lanes whose Task is not working, sweeps the lanes no attempt
// or active Task has, and names on its card each landing on an integration
// branch whose request was not accepted.
func (m *Manager) recoverLanes(ctx context.Context) {
	var held []board.Card
	if err := m.withBoard(func(st *board.Store) error {
		var err error
		held, err = st.Held(ctx)
		return err
	}); err != nil {
		log.Warn("read the lanes to recover failed", "error", err)
		return
	}
	open := map[string]bool{}
	for _, c := range held {
		if c.Lane == nil {
			continue
		}
		open[c.Lane.Branch] = true
		if err := m.recoverLane(ctx, c); err != nil {
			log.Warn("recover a lane failed", "card", c.ID, "branch", c.Lane.Branch, "error", err)
		}
	}
	m.mu.Lock()
	type project struct{ id, dir string }
	var projects []project
	for id, p := range m.projects {
		if p.NoGit == "" {
			projects = append(projects, project{id, p.Dir})
		}
	}
	m.mu.Unlock()
	for _, p := range projects {
		if err := m.sweepLanes(ctx, p.id, p.dir, open); err != nil {
			log.Warn("sweep a project's lanes failed", "project", p.id, "error", err)
		}
	}
}

// recoverLane finishes the landing intent of the lane held at c, and
// aborts a merge uam left in its lane while its Task is not working.
func (m *Manager) recoverLane(ctx context.Context, c board.Card) error {
	unlock, err := m.lockLand(ctx, c.ProjectID)
	if err != nil {
		return err
	}
	defer unlock()
	var d board.Detail
	if err := m.withBoard(func(st *board.Store) error {
		var err error
		d, err = st.Detail(ctx, c.ID)
		return err
	}); err != nil {
		return err
	}
	hold, ok := openHold(d.Holds, c.HeldBy)
	if !ok {
		return nil
	}
	repo, l, err := m.laneOfHold(ctx, c, hold)
	if err != nil {
		return err
	}
	if hold.Lane.LandedSHA != "" {
		i := slices.IndexFunc(d.Requests, func(r board.Request) bool {
			return r.Kind == board.RequestDone && r.Status == board.RequestPending && r.TaskID == c.HeldBy
		})
		if i >= 0 {
			if _, _, err := m.finishLanding(ctx, repo, d.Requests[i], hold.Lane.LandedSHA, board.DecidedByUAM, ""); err != nil {
				return err
			}
		}
	}
	if m.holderBusy(c.HeldBy) {
		return nil
	}
	return repo.abortOwnMerge(ctx, l)
}

// abortOwnMerge aborts a merge in the lane that uam started: its message
// carries the Uam-Merge trailer. A merge the agent started is the agent's.
func (r *laneRepo) abortOwnMerge(ctx context.Context, l lane) error {
	if _, err := os.Stat(l.dir); err != nil {
		return nil
	}
	a, err := r.laneAt(l)
	if err != nil {
		return err
	}
	merging, err := r.mergeHead(ctx, a)
	if err != nil || !merging {
		return err
	}
	paths, err := r.gitPaths(ctx, a, "MERGE_MSG")
	if err != nil {
		return err
	}
	msg, err := os.ReadFile(paths[0]) // #nosec G304 -- a path git names inside its own directory.
	if err != nil || !strings.Contains(string(msg), "\n"+trailerMerge+": ") {
		return nil
	}
	if _, err := runLaneGit(ctx, a, "merge", "--abort"); err != nil {
		return gitFailed("git merge --abort failed", err)
	}
	return nil
}

// sweepLanes is recovery's pass over the Project project's lanes: each lane
// directory no open attempt and no active Task has is cleaned up, git
// forgets worktrees whose directory is gone, attempt branches no attempt
// has and that hold no commit of their own are deleted, and each landing on
// the integration branch whose request is not accepted is named on its
// card. A Project that never had a lane is left alone, git not run: its
// repository's worktrees are the owner's.
func (m *Manager) sweepLanes(ctx context.Context, project, dir string, open map[string]bool) error {
	m.mu.Lock()
	var active []string
	for _, s := range m.sessions {
		if !s.removed && s.stage == StageActive && m.inLanes(s.workdir) {
			active = append(active, s.workdir)
		}
	}
	m.mu.Unlock()
	entries, err := os.ReadDir(filepath.Join(m.lanesRoot(), project))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		l, err := laneOf(m.lanesRoot(), project, integBranch(project)+"-"+e.Name())
		if err != nil || !e.IsDir() || open[l.branch] || slices.ContainsFunc(active, func(w string) bool { return inDir(l.dir, w) }) {
			continue
		}
		m.cleanLane(ctx, project, l)
	}
	unlock, err := m.lockLand(ctx, project)
	if err != nil {
		return err
	}
	defer unlock()
	repo, err := openLanes(ctx, project, dir)
	if err != nil {
		return err
	}
	if _, err := runLaneGit(ctx, gitAt{dir: repo.top}, "worktree", "prune"); err != nil {
		return gitFailed("git worktree prune failed", err)
	}
	if err := m.sweepBranches(ctx, repo, project); err != nil {
		return err
	}
	return m.crossCheck(ctx, repo, project)
}

// sweepBranches deletes the attempt branches a crash left between adding a
// lane and starting its attempt: no attempt has them, no worktree has them
// checked out, and they hold no commit of their own.
func (m *Manager) sweepBranches(ctx context.Context, repo *laneRepo, project string) error {
	out, err := repo.output(ctx, repo.top, "for-each-ref", "--format=%(refname:short)", "refs/heads/"+integBranch(project)+"-*")
	if err != nil {
		return err
	}
	for _, branch := range strings.Fields(out) {
		if _, err := laneOf(m.lanesRoot(), project, branch); err != nil {
			continue
		}
		err := m.withBoard(func(st *board.Store) error {
			_, _, err := st.LaneHold(ctx, branch)
			return err
		})
		if !errors.Is(err, board.ErrNotFound) && apiCode(err) != string(board.CodeNotFound) {
			continue
		}
		if dir, err := repo.checkedOut(ctx, branch); err != nil || dir != "" {
			continue
		}
		if none, err := repo.ownCommitsNone(ctx, branch); err != nil || !none {
			continue
		}
		if _, err := runLaneGit(ctx, gitAt{dir: repo.top}, "branch", "-D", branch); err != nil {
			log.Warn("delete a stray attempt branch failed", "branch", branch, "error", err)
		}
	}
	return nil
}

// crossCheck names, on its card, each landing on the Project's integration
// branch since it forked from its base whose request is not accepted: only
// an edit of the integration branch by hand leaves one.
func (m *Manager) crossCheck(ctx context.Context, repo *laneRepo, project string) error {
	var ps board.ProjectSettings
	if err := m.withBoard(func(st *board.Store) error {
		var err error
		ps, err = st.ProjectSettings(ctx, project)
		return err
	}); err != nil {
		return err
	}
	if ps.BaseRef == "" {
		return nil
	}
	tip, err := repo.tipOf(ctx, repo.integ)
	if err != nil || tip == "" {
		return err
	}
	baseTip, err := repo.tipOf(ctx, ps.BaseRef)
	if err != nil || baseTip == "" {
		return err
	}
	out, err := repo.output(ctx, repo.top, "log", "--format=%H %(trailers:key="+trailerRequest+",valueonly)", baseTip+".."+tip, "--")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		sha, id := fields[0], fields[1]
		var r board.Request
		err := m.withBoard(func(st *board.Store) error {
			var err error
			r, err = st.Request(ctx, id)
			return err
		})
		if err != nil || r.Status == board.RequestAccepted {
			continue
		}
		m.noteCard(ctx, r.CardID, fmt.Sprintf("%s carries %s, which lands this card's request, but the request is %s: the branch was edited outside uam", repo.integ, shortSHA(sha), r.Status))
	}
	return nil
}

// BoardIntegration is a Project's integration branch (ADR 0006 §3.2): the
// branch it follows, how many landings it has that the base lacks, and
// how many commits the base has that it lacks.
type BoardIntegration struct {
	Branch  string `json:"branch"`
	BaseRef string `json:"base_ref"`
	Ahead   int    `json:"ahead"`
	Behind  int    `json:"behind"`
}

// integration reads the Project's integration branch in dir; nil before
// it has one, or when git cannot tell.
func integration(ctx context.Context, project, dir, base string) *BoardIntegration {
	repo, err := openLanes(ctx, project, dir)
	if err != nil {
		return nil
	}
	tip, err := repo.tipOf(ctx, repo.integ)
	if err != nil || tip == "" {
		return nil
	}
	out := &BoardIntegration{Branch: repo.integ, BaseRef: base}
	baseTip, err := repo.tipOf(ctx, base)
	if err != nil || baseTip == "" {
		return out
	}
	if landings, err := repo.output(ctx, repo.top, "log", "--first-parent", "--format=%(trailers:key="+trailerRequest+",valueonly)", baseTip+".."+tip, "--"); err == nil {
		out.Ahead = len(strings.Fields(landings))
	}
	if behind, err := repo.output(ctx, repo.top, "rev-list", "--count", tip+".."+baseTip, "--"); err == nil {
		out.Behind, _ = strconv.Atoi(behind)
	}
	return out
}

// checkBaseRef refuses a base branch the Project's repository in dir does
// not have.
func checkBaseRef(ctx context.Context, project, dir, base string) error {
	repo, err := openLanes(ctx, project, dir)
	if err != nil {
		return err
	}
	_, code, _, err := runGit(ctx, repo.git, repo.top, 4096, "check-ref-format", "refs/heads/"+base)
	if err != nil {
		return err
	}
	tip := ""
	if code == 0 {
		if tip, err = repo.tipOf(ctx, base); err != nil {
			return err
		}
	}
	if tip == "" || strings.HasPrefix(base, integPrefix) {
		return newError(http.StatusConflict, "there is no branch %s to follow", displaytext.Sanitize(cmp.Or(base, `""`)))
	}
	return nil
}
