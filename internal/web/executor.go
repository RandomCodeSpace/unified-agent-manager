package web

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/board"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// The executor (ADR 0006 §4.3) runs approved epics. A loop shaped like the
// routine loop passes over the board after each committed board write,
// when a lane Task's turn leaves Working, after a Task's stage moves, when
// the board opens and when a landing job ends, and every tick besides.
// Each pass reads the lane Tasks under mu, the board's run facts in one
// read transaction, and the executor's memory, lets board.Next decide, and
// applies the step: starts and landings run in the background, under the
// Project's land mutex; nudges, cancels, retirements and aborts run in the
// pass. A pass never holds mu across SQL or git.

const (
	// executorTick is how often the executor passes without a kick.
	executorTick = 30 * time.Second
	// maxBackoff is the longest a provider, an epic or a landing waits
	// before the next try: 1, 2, 4, 8, then 15 minutes.
	maxBackoff = 15 * time.Minute
	// maxStartFailures is how many lane starts in a row may fail on git or
	// the store before uam pauses the epic.
	maxStartFailures = 3
)

// executorState is the executor's memory, kept between passes and lost on
// a restart (ADR 0006 §4.7). mu guards everything below pass; it is taken
// after Manager.mu and laneState.mu, never before them, and never held
// across a store call, git or a Task operation. pass serializes passes.
type executorState struct {
	pass sync.Mutex
	kick chan struct{}
	// tick is how often the loop passes without a kick; tests set it
	// before Start.
	tick time.Duration

	mu sync.Mutex
	// clock is the executor's time; tests move it.
	clock func() time.Time
	// off keeps passes from running; tests that start and end lane Tasks
	// by hand set it.
	off bool
	// nudged counts each Task's nudges since boot.
	nudged map[string]int
	// starting maps each subtask a start is in progress for to its epic.
	starting map[string]string
	// landRetry is when each done request waiting to land is tried again,
	// and landTries how many times it was tried.
	landRetry map[string]time.Time
	landTries map[string]int
	// epicBackoff is when each epic may start again, and epicFails how many
	// of its lane starts failed on git or the store in a row.
	epicBackoff map[string]time.Time
	epicFails   map[string]int
	// breakers holds each provider's breaker, and failedAt when it last
	// counted a failure; completed is each provider's latest completed
	// turn, which closes a breaker that failure opened.
	breakers  map[string]board.Breaker
	failedAt  map[string]time.Time
	completed map[string]time.Time
	// seen is, for each Task, its failed turn already counted; failed is
	// the turn each failed Task was first seen failed in, and when.
	seen   map[string]time.Time
	failed map[string]failedTurn
}

// failedTurn is a Task's failed turn: the turn's sequence, and when the
// executor first saw it failed.
type failedTurn struct {
	seq uint64
	at  time.Time
}

func newExecutorState() executorState {
	return executorState{
		kick: make(chan struct{}, 1), tick: executorTick, clock: time.Now,
		nudged: map[string]int{}, starting: map[string]string{},
		landRetry: map[string]time.Time{}, landTries: map[string]int{},
		epicBackoff: map[string]time.Time{}, epicFails: map[string]int{},
		breakers: map[string]board.Breaker{}, failedAt: map[string]time.Time{}, completed: map[string]time.Time{},
		seen: map[string]time.Time{}, failed: map[string]failedTurn{},
	}
}

// backoff is the wait after the nth failure in a row: 1, 2, 4, 8, then 15
// minutes.
func backoff(n int) time.Duration {
	if n < 1 {
		n = 1
	}
	if n > 4 {
		return maxBackoff
	}
	return min(time.Minute<<(n-1), maxBackoff)
}

// runExecutor turns the executor's passes on or off; on kicks a pass.
func (m *Manager) runExecutor(on bool) {
	m.exec.mu.Lock()
	m.exec.off = !on
	m.exec.mu.Unlock()
	if on {
		m.kickExecutor()
	}
}

// kickExecutor asks for a pass, without waiting for it.
func (m *Manager) kickExecutor() {
	select {
	case m.exec.kick <- struct{}{}:
	default:
	}
}

func (m *Manager) executorLoop() {
	defer m.wg.Done()
	ticker := time.NewTicker(m.exec.tick)
	defer ticker.Stop()
	for {
		m.executorPass()
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
		case <-m.exec.kick:
		}
	}
}

// turnLeftWorkingLocked tells the executor that s's turn left Working: a
// completed turn closes its provider's breaker, and a lane Task's turn, or
// one that closes a breaker, kicks a pass. The caller holds mu.
func (m *Manager) turnLeftWorkingLocked(s *webSession, state agentapi.TurnState) {
	kick := m.inLanes(s.workdir)
	if state == agentapi.TurnCompleted {
		m.exec.mu.Lock()
		m.exec.completed[s.provider] = m.exec.clock()
		_, open := m.exec.breakers[s.provider]
		m.exec.mu.Unlock()
		kick = kick || open
	}
	if kick {
		m.kickExecutor()
	}
}

// laneTask is what a pass needs of a lane Task besides its TaskFact.
type laneTask struct {
	workdir, detail string
	seq             uint64
}

// executorPass is one pass of the executor: it reads, decides with
// board.Next, and applies the step.
func (m *Manager) executorPass() {
	m.exec.pass.Lock()
	defer m.exec.pass.Unlock()
	m.exec.mu.Lock()
	off := m.exec.off
	// Read before the Tasks: a start that ends after this still leaves its
	// Task out of the pass, which saw it before its first prompt.
	starting := maps.Clone(m.exec.starting)
	m.exec.mu.Unlock()
	if off || m.isClosed() {
		return
	}
	ctx := m.ctx
	tasks, lanes, signedOut := m.laneTaskFacts()
	var facts board.RunFacts
	if err := m.withBoard(func(st *board.Store) error {
		var err error
		facts, err = st.RunFacts(ctx)
		return err
	}); err != nil {
		if !plannerDown(err) && !m.isClosed() {
			log.Warn("read the planner's runs failed", "error", err)
		}
		return
	}
	for _, l := range facts.Lanes {
		if tf, ok := tasks[l.TaskID]; ok && tf.Turn == board.TurnFailed {
			tf.Worked = m.laneWorked(ctx, lanes[l.TaskID].workdir)
			tasks[l.TaskID] = tf
		}
	}
	landing := m.landingCards()
	now, mem := m.executorMemory(facts, tasks, lanes, signedOut, landing, starting)
	step := board.Next(facts, tasks, mem)
	m.applyStep(ctx, now, facts, tasks, lanes, step)
}

// laneTaskFacts reads, under mu, every lane Task's facts, and the providers
// signed out, with why.
func (m *Manager) laneTaskFacts() (map[string]board.TaskFact, map[string]laneTask, map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tasks := map[string]board.TaskFact{}
	lanes := map[string]laneTask{}
	for id, s := range m.sessions {
		if s.removed || !m.inLanes(s.workdir) {
			continue
		}
		tasks[id] = board.TaskFact{Stage: boardStage(s.stage), Turn: laneTurn(s), Provider: s.provider, Retired: s.retired != ""}
		lanes[id] = laneTask{workdir: s.workdir, detail: s.detail, seq: s.turnSeq}
	}
	return tasks, lanes, m.signedOutLocked()
}

// signedOutLocked maps each provider signed out to why. The caller holds
// mu.
func (m *Manager) signedOutLocked() map[string]string {
	out := map[string]string{}
	for name, info := range m.infos {
		if info.SignedOut {
			out[name] = cmp.Or(info.Reason, "signed out")
		}
	}
	return out
}

// boardStage is a Task's stage as the planner sees it.
func boardStage(stage string) board.Stage {
	switch stage {
	case StageSettled:
		return board.StageSettled
	case StageArchived:
		return board.StageArchived
	}
	return board.StageActive
}

// laneTurn is where s's turn stands, as board.Next reads it. A cancelled
// turn with a detail was cancelled by uam, which says why; the owner's
// cancel says nothing.
func laneTurn(s *webSession) board.Turn {
	switch s.state() {
	case StateWorking, StateStarting:
		return board.TurnWorking
	case StateAwaitingPermission, StateAwaitingAnswer:
		return board.TurnWaiting
	case StateFailed:
		return board.TurnFailed
	case StateInterrupted:
		return board.TurnInterrupted
	case StateCancelled:
		if s.detail == "" {
			return board.TurnOwnerCancelled
		}
		return board.TurnUAMCancelled
	}
	return board.TurnEnded
}

// laneWorked reports whether the lane a Task works in at workdir has
// changes or commits of its own beyond the integration branch, read with
// git pinned to the lane uam made (laneAt). When git cannot tell, it has: a
// lane is never discarded on a guess.
func (m *Manager) laneWorked(ctx context.Context, workdir string) bool {
	project, l, ok := m.laneOfDir(workdir)
	if !ok {
		return true
	}
	m.mu.Lock()
	p := m.projects[project]
	var dir string
	if p != nil {
		dir = p.Dir
	}
	m.mu.Unlock()
	if p == nil {
		return true
	}
	repo, err := openLanes(ctx, project, dir)
	if err != nil {
		return true
	}
	a, err := repo.laneAt(l)
	if err != nil {
		return true
	}
	if out, err := repo.outputAt(ctx, a, "status", "--porcelain"); err != nil || out != "" {
		return true
	}
	n, err := repo.outputAt(ctx, a, "rev-list", "--count", repo.integ+"..HEAD", "--")
	return err != nil || n != "0"
}

// landingCards copies the cards a land call is in flight for.
func (m *Manager) landingCards() map[string]bool {
	m.lanes.mu.Lock()
	defer m.lanes.mu.Unlock()
	return maps.Clone(m.lanes.landing)
}

// executorMemory brings the executor's memory up to date with the pass's
// facts and returns it as board.Next reads it: a breaker a completed turn
// followed closes; a signed-out provider's stays open; each failed Task's
// failed turn is dated; a Task whose start is in progress is left out, so
// nothing acts on it before its first prompt; and entries for what is gone
// are dropped.
func (m *Manager) executorMemory(facts board.RunFacts, tasks map[string]board.TaskFact, lanes map[string]laneTask, signedOut map[string]string, landing map[string]bool, starting map[string]string) (time.Time, board.Memory) {
	e := &m.exec
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.clock()
	for p, at := range e.completed {
		if _, open := e.breakers[p]; open && at.After(e.failedAt[p]) {
			delete(e.breakers, p)
			delete(e.failedAt, p)
		}
	}
	providers := maps.Clone(e.breakers)
	for p, why := range signedOut {
		b := providers[p]
		b.Until, b.Detail = now.Add(24*time.Hour), why
		providers[p] = b
	}
	for id, tf := range tasks {
		if tf.Turn != board.TurnFailed {
			delete(e.failed, id)
			continue
		}
		f, ok := e.failed[id]
		if !ok || f.seq != lanes[id].seq {
			f = failedTurn{seq: lanes[id].seq, at: now}
			e.failed[id] = f
		}
		tf.FailedAt = f.at
		tasks[id] = tf
	}
	for id := range e.failed {
		if _, ok := tasks[id]; !ok {
			delete(e.failed, id)
		}
	}
	for id := range e.nudged {
		if _, ok := tasks[id]; !ok {
			delete(e.nudged, id)
		}
	}
	for id := range e.seen {
		if _, ok := tasks[id]; !ok {
			delete(e.seen, id)
		}
	}
	waiting := map[string]bool{}
	for _, l := range facts.Lanes {
		if starting[l.CardID] != "" || e.starting[l.CardID] != "" {
			delete(tasks, l.TaskID)
		}
		if l.Landing || l.Intent != "" {
			waiting[l.Request] = true
		}
	}
	for id := range e.landRetry {
		if !waiting[id] {
			delete(e.landRetry, id)
			delete(e.landTries, id)
		}
	}
	return now, board.Memory{
		Now: now, Nudged: maps.Clone(e.nudged), Starting: maps.Clone(e.starting), Landing: landing,
		LandRetry: maps.Clone(e.landRetry), EpicBackoff: maps.Clone(e.epicBackoff), Providers: providers, SeenFailure: maps.Clone(e.seen),
	}
}

// applyStep applies one pass's step. A provider whose failure this pass
// counted gets no start and no nudge in it.
func (m *Manager) applyStep(ctx context.Context, now time.Time, facts board.RunFacts, tasks map[string]board.TaskFact, lanes map[string]laneTask, step board.Step) {
	failed := map[string]bool{}
	for _, a := range step.ProviderFailed {
		m.exec.mu.Lock()
		m.exec.seen[a.Task] = tasks[a.Task].FailedAt
		m.exec.mu.Unlock()
		failed[a.Provider] = true
		m.providerFailed(ctx, a.Provider, lanes[a.Task].detail, epicsOn(facts, a.Provider))
	}
	for _, a := range step.Abort {
		detail := strings.TrimSpace("the provider failed before the attempt did any work: " + lanes[a.Task].detail)
		if err := m.withBoard(func(st *board.Store) error {
			_, err := st.AbortRun(ctx, a.CardID, a.Task, detail)
			return err
		}); err != nil {
			log.Warn("abort a failed lane attempt failed", "card", a.CardID, "session", a.Task, "error", err)
		}
		m.discardTask(a.Task)
	}
	for _, a := range step.Retire {
		m.retire(ctx, a, tasks[a.Task].Stage, lanes[a.Task].workdir)
	}
	for _, a := range step.Cancel {
		if _, err := m.cancelBecause(a.Task, fmt.Sprintf("uam: #%d was stopped", a.Seq)); err != nil {
			log.Info("cancel a stopped lane task failed", "session", a.Task, "error", err)
		}
	}
	for _, a := range step.Nudge {
		if !failed[a.Provider] {
			m.nudge(now, a)
		}
	}
	m.land(facts, step.Land)
	for _, p := range step.Start {
		if !failed[p.Epic.Run.Provider] {
			m.startPick(p)
		}
	}
}

// retire retires the lane Task a names, whose stage was stage and whose
// workdir is workdir (ADR 0006 §5.6). An active one is settled as the
// owner's Settle settles it, so its transcript stays on the Settled shelf;
// while that is refused, as while it is busy, the next pass tries again.
// Its planner calls end, its hold is released as Archive's reconcile
// releases it, it is marked retired, which refuses Reopen and keeps later
// passes from retiring it again, and its lane is cleaned up. One the owner
// archived has its hold released and its lane cleaned up as Archive does.
func (m *Manager) retire(ctx context.Context, a board.Act, stage board.Stage, workdir string) {
	switch stage {
	case board.StageArchived:
		m.reconcileBoard()
		m.cleanLaneOf(workdir)
		return
	case board.StageActive:
		if _, err := m.moveStage(a.Task, StageSettled, StageActive); err != nil {
			log.Info("retire a lane task failed", "session", a.Task, "error", err)
			return
		}
		m.endCalls(a.Task)
	}
	m.reconcileRetired(a.Task)
	m.markRetired(a.Task, m.retiredReason(ctx, workdir, a.Seq))
	m.cleanLaneOf(workdir)
}

// retiredReason says why the lane Task working in workdir, retired from
// #seq, is not reopened: its lane is removed. It names the commit the
// attempt landed as, or the branch that keeps what it did.
func (m *Manager) retiredReason(ctx context.Context, workdir string, seq int64) string {
	reason := fmt.Sprintf("#%d ran in a lane that is removed", seq)
	_, l, ok := m.laneOfDir(workdir)
	if !ok {
		return reason + ". Read the transcript here."
	}
	var hold board.Hold
	err := m.withBoard(func(st *board.Store) error {
		var err error
		_, hold, err = st.LaneHold(ctx, l.branch)
		return err
	})
	if err == nil && hold.Lane.LandedSHA != "" {
		return fmt.Sprintf("%s; its work landed as %s. Read the transcript here; the work is on the integration branch.", reason, shortSHA(hold.Lane.LandedSHA))
	}
	return fmt.Sprintf("%s; what it did is kept on branch %s. Read the transcript here.", reason, l.branch)
}

// markRetired records on the settled Task id, once, why it is not reopened.
// One the owner reopened or archived meanwhile is left unmarked: the next
// pass retires a reopened one again.
func (m *Manager) markRetired(id, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil || s.removed || s.stage != StageSettled || s.retired != "" {
		return
	}
	before := m.summaryLocked(s)
	s.retired = reason
	m.changedLocked(s, before)
}

// epicsOn lists the approved epics that run on provider: those not paused,
// done or cancelled.
func epicsOn(facts board.RunFacts, provider string) []board.Card {
	var out []board.Card
	for _, e := range facts.Epics {
		c := e.Epic
		if c.Run != nil && c.Run.Provider == provider && c.Paused == "" && c.Status != board.StatusDone && c.Status != board.StatusCancelled {
			out = append(out, c)
		}
	}
	return out
}

// providerFailed counts a failure against provider's breaker, which backs
// off for longer with each failure in a row, and notes on each epic in
// epics that it waits for the provider, once per distinct detail.
func (m *Manager) providerFailed(ctx context.Context, provider, detail string, epics []board.Card) {
	detail = clipRunes(strings.TrimSpace(displaytext.Sanitize(detail)), maxDetailRunes)
	m.exec.mu.Lock()
	now := m.exec.clock()
	b := m.exec.breakers[provider]
	b.Failures++
	b.Until, b.Detail = now.Add(backoff(b.Failures)), detail
	m.exec.breakers[provider] = b
	m.exec.failedAt[provider] = now
	m.exec.mu.Unlock()
	name := m.providerName(provider)
	for _, e := range epics {
		m.noteCard(ctx, e.ID, fmt.Sprintf("Waiting for %s: %s", name, cmp.Or(detail, "it failed")))
	}
}

// providerName is provider's display name.
func (m *Manager) providerName(provider string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p := m.providers[provider]; p != nil {
		return p.DisplayName()
	}
	return provider
}

// nudge sends the lane Task a's prompt to carry on (ADR 0006 §4.2), the way
// a launch sends its first one. A probe after a provider failure keeps the
// provider's breaker open until its turn shows how the provider does.
func (m *Manager) nudge(now time.Time, a board.Act) {
	var text string
	switch a.Why {
	case board.WhyRestarted:
		text = fmt.Sprintf("uam restarted while you worked on #%d; continue, then file board_request done.", a.Seq)
	case board.WhyProviderFailed:
		text = fmt.Sprintf("The provider failed during your turn; continue #%d, then file board_request done.", a.Seq)
	default:
		text = fmt.Sprintf("You still hold #%d and ended your turn without a done request. Finish it and file board_request done, or file board_request blocked with the reason.", a.Seq)
	}
	if err := m.sendFirstPrompt(a.Task, text); errors.Is(err, errTurnRunning) {
		// It works again: no nudge was due.
		return
	} else if err != nil {
		// It counts still: a holder its nudge did not reach is retired.
		log.Info("nudge a lane task failed", "session", a.Task, "error", err)
	}
	m.exec.mu.Lock()
	defer m.exec.mu.Unlock()
	m.exec.nudged[a.Task]++
	if b, ok := m.exec.breakers[a.Provider]; ok && a.Why == board.WhyProviderFailed {
		b.Until = now.Add(backoff(b.Failures))
		m.exec.breakers[a.Provider] = b
	}
}

// land lands the done requests ids in the background, each with its card
// in Landing until it returns (ADR 0006 §5.4).
func (m *Manager) land(facts board.RunFacts, ids []string) {
	for _, id := range ids {
		i := slices.IndexFunc(facts.Lanes, func(l board.LaneFacts) bool { return l.Request == id })
		if id == "" || i < 0 {
			continue
		}
		card := board.Card{ID: facts.Lanes[i].CardID, Seq: facts.Lanes[i].Seq}
		if m.markLanding(card) != nil {
			continue
		}
		m.goLanes(func(ctx context.Context) {
			defer m.kickExecutor()
			defer m.unmarkLanding(card.ID)
			out, err := m.landAndAccept(ctx, id, board.DecidedByUAM, "", false)
			m.landed(ctx, id, card, out, err)
		})
	}
}

// landed records how a landing the executor ran ended: landed, or tried
// again after a backoff, with one comment per distinct reason it waits.
func (m *Manager) landed(ctx context.Context, id string, card board.Card, out landOutcome, err error) {
	m.exec.mu.Lock()
	if err == nil && out.queued == "" {
		delete(m.exec.landRetry, id)
		delete(m.exec.landTries, id)
		m.exec.mu.Unlock()
		return
	}
	m.exec.landTries[id]++
	m.exec.landRetry[id] = m.exec.clock().Add(backoff(m.exec.landTries[id]))
	m.exec.mu.Unlock()
	if out.queued != "" {
		m.noteCard(ctx, card.ID, "Landing is queued: "+out.queued+". uam tries again.")
	} else if err != nil && !plannerDown(err) && !m.isClosed() {
		// A landing that cannot apply rejected its request already.
		log.Info("a landing did not apply", "request", id, "error", err)
	}
}

// providerFault marks a lane start that failed on the provider: its Task
// could not be made, or did not take its first prompt.
type providerFault struct{ err error }

func (f providerFault) Error() string { return f.err.Error() }
func (f providerFault) Unwrap() error { return f.err }

// startPick starts the subtask p in a lane in the background, with a
// reservation that counts it as starting until the start ends.
func (m *Manager) startPick(p board.Pick) {
	m.exec.mu.Lock()
	if m.exec.starting[p.Card.ID] != "" {
		m.exec.mu.Unlock()
		return
	}
	m.exec.starting[p.Card.ID] = p.Epic.ID
	m.exec.mu.Unlock()
	m.goLanes(func(ctx context.Context) {
		defer m.kickExecutor()
		_, _, err := m.startLane(p.Card, p.Epic)
		m.started(ctx, p, err)
	})
}

// started records how the start of p ended (ADR 0006 §4.5): a race with a
// board write counts nothing; a provider failure backs the provider off; a
// git or store failure backs the epic off, with one comment per distinct
// error, and the third in a row pauses it.
func (m *Manager) started(ctx context.Context, p board.Pick, err error) {
	var fault providerFault
	// Classified before the executor's mu: isClosed takes Manager.mu, which
	// is never taken while that is held.
	quiet := err != nil && (apiCode(err) == string(board.CodeNotReady) || plannerDown(err) || m.isClosed() || errors.Is(err, context.Canceled))
	m.exec.mu.Lock()
	delete(m.exec.starting, p.Card.ID)
	switch {
	case err == nil:
		delete(m.exec.epicFails, p.Epic.ID)
		delete(m.exec.epicBackoff, p.Epic.ID)
		m.exec.mu.Unlock()
		return
	case quiet:
		m.exec.mu.Unlock()
		return
	case errors.As(err, &fault):
		m.exec.mu.Unlock()
		m.providerFailed(ctx, p.Epic.Run.Provider, fault.Error(), []board.Card{p.Epic})
		return
	}
	n := m.exec.epicFails[p.Epic.ID] + 1
	m.exec.epicFails[p.Epic.ID] = n
	m.exec.epicBackoff[p.Epic.ID] = m.exec.clock().Add(backoff(n))
	if n >= maxStartFailures {
		// The pause stops the starts; Resume starts afresh.
		delete(m.exec.epicFails, p.Epic.ID)
		delete(m.exec.epicBackoff, p.Epic.ID)
	}
	m.exec.mu.Unlock()
	msg := clipRunes(displaytext.Sanitize(err.Error()), maxDetailRunes)
	log.Warn("start a lane failed", "card", p.Card.ID, "epic", p.Epic.ID, "error", err)
	m.noteCard(ctx, p.Epic.ID, fmt.Sprintf("Could not start #%d: %s", p.Card.Seq, msg))
	if n < maxStartFailures {
		return
	}
	if err := m.withBoard(func(st *board.Store) error {
		_, err := st.PauseRun(ctx, p.Epic.ID, fmt.Sprintf("lanes could not start %d times in a row: %s", n, msg))
		return err
	}); err != nil && !plannerDown(err) {
		log.Warn("pause an epic whose lanes do not start failed", "epic", p.Epic.ID, "error", err)
	}
}

// BoardExecutor is what the executor waits for (ADR 0006 §3.2): the
// providers nothing starts on now.
type BoardExecutor struct {
	Providers []BoardProviderWait `json:"providers"`
}

// BoardProviderWait is a provider nothing starts or is nudged on: it backs
// off after a failure until Until, or is signed out, with no Until.
type BoardProviderWait struct {
	Provider string     `json:"provider"`
	Name     string     `json:"name"`
	Detail   string     `json:"detail"`
	Until    *time.Time `json:"until"`
}

// ExecutorWaits lists the providers the executor waits for, none in an
// empty list. The Planner reads it again on each Board change while an epic
// runs: unlike BoardProject, it runs no git.
func (m *Manager) ExecutorWaits() (BoardExecutor, error) {
	if err := m.boardOn(); err != nil {
		return BoardExecutor{}, err
	}
	if w := m.executorWaits(); w != nil {
		return *w, nil
	}
	return BoardExecutor{Providers: []BoardProviderWait{}}, nil
}

// executorWaits lists the providers the executor waits for; nil for none.
func (m *Manager) executorWaits() *BoardExecutor {
	m.mu.Lock()
	signedOut := m.signedOutLocked()
	m.mu.Unlock()
	m.exec.mu.Lock()
	now := m.exec.clock()
	waits := map[string]BoardProviderWait{}
	for p, b := range m.exec.breakers {
		if now.Before(b.Until) {
			until := b.Until
			waits[p] = BoardProviderWait{Provider: p, Detail: b.Detail, Until: &until}
		}
	}
	m.exec.mu.Unlock()
	for p, why := range signedOut {
		waits[p] = BoardProviderWait{Provider: p, Detail: why}
	}
	if len(waits) == 0 {
		return nil
	}
	out := &BoardExecutor{}
	for _, p := range slices.Sorted(maps.Keys(waits)) {
		w := waits[p]
		w.Name = m.providerName(p)
		out.Providers = append(out.Providers, w)
	}
	return out
}
