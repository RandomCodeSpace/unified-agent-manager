package board

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
)

// The executor (ADR 0006 §4) runs approved epics: each pass reads the
// board's facts in one transaction (RunFacts) and decides, with no I/O,
// what to start, nudge, cancel, retire, abort and land (Next). The caller
// applies the step.

// RunFacts is the board as one executor pass sees it (ADR 0006 §4.1).
type RunFacts struct {
	// Epics lists every approved epic, Project by Project in outline order.
	Epics []EpicFacts
	// Lanes lists every open lane attempt in #seq order, those whose slot a
	// pending request frees included.
	Lanes []LaneFacts
	// Ended maps each Task whose lane attempt ended to that attempt.
	Ended map[string]LaneEnd
	// InUse is the number of lane slots in use over every Project, the
	// count StartRun checks against MaxLanes.
	InUse int
}

// EpicFacts is an approved epic in one pass.
type EpicFacts struct {
	// Epic carries the run it was approved with and its pause.
	Epic Card
	// InUse is the number of lane slots in use under the epic, the count
	// StartRun checks against its parallel limit.
	InUse int
	// Ready lists, in outline order, the subtasks under the epic that a run
	// may start: those StartRun would not refuse as not ready.
	Ready []Card
}

// LaneFacts is an open lane attempt in one pass.
type LaneFacts struct {
	CardID string
	Seq    int64
	// TaskID is the attempt's Task, the holder.
	TaskID string
	// Pending is the kind of the holder's pending done, blocked or split
	// request on the subtask, a done before the others, and Request its
	// ID; "" when it has none.
	Pending RequestKind
	Request string
	// Landing marks a pending done that waits only to land.
	Landing bool
	// Intent is the landing intent, the commit the attempt lands as; ""
	// when none is stored.
	Intent string
}

// LaneEnd is how a lane Task's attempt ended.
type LaneEnd struct {
	CardID string
	Seq    int64
	Reason ReleaseReason
}

// runProjectsQuery lists the Projects with an approved epic.
const runProjectsQuery = `SELECT DISTINCT c.project_id FROM runs r JOIN cards c ON c.id = r.epic_id ORDER BY c.project_id`

// openLanesQuery lists every open lane attempt in #seq order.
const openLanesQuery = `SELECT h.card_id, c.seq, h.task_id, h.landed_sha FROM holds h JOIN cards c ON c.id = h.card_id
	WHERE h.ended_at = '' AND h.branch <> '' ORDER BY c.seq`

// laneRequestsQuery lists the pending done, blocked and split requests the
// holders of open lane attempts filed on them, each card's done first.
const laneRequestsQuery = `SELECT r.card_id, r.id, r.kind, r.payload FROM requests r
	JOIN holds h ON h.card_id = r.card_id AND h.task_id = r.task_id AND h.ended_at = '' AND h.branch <> ''
	WHERE r.status = 'pending' AND r.kind IN ('done', 'blocked', 'split')
	ORDER BY r.card_id, r.kind <> 'done', r.created_at`

// endedLanesQuery lists the ended lane attempts, each Task's latest last.
const endedLanesQuery = `SELECT h.task_id, h.card_id, c.seq, h.end_reason FROM holds h JOIN cards c ON c.id = h.card_id
	WHERE h.ended_at <> '' AND h.branch <> '' ORDER BY h.task_id, h.ended_at`

// RunFacts reads, in one transaction, what an executor pass decides on:
// every approved epic with its ready subtasks and its slots in use, every
// open lane attempt, and each lane Task's ended attempt. Ready and the
// slot counts are the ones StartRun checks, so a start RunFacts offers is
// refused only when a write lands in between.
func (s *Store) RunFacts(ctx context.Context) (RunFacts, error) {
	var out RunFacts
	err := s.read(ctx, func(t *txn) error {
		out = RunFacts{Ended: map[string]LaneEnd{}}
		lanes, err := t.lanesInUse()
		if err != nil {
			return err
		}
		out.InUse = len(lanes)
		projects, err := t.column(runProjectsQuery)
		if err != nil {
			return err
		}
		for _, p := range projects {
			o, err := t.outline(p)
			if err != nil {
				return err
			}
			for _, n := range o.order {
				if n.Kind != KindEpic || n.Run == nil {
					continue
				}
				e := EpicFacts{Epic: n.Card, InUse: len(o.lanesUnder(n, lanes))}
				for _, l := range n.leaves {
					switch err := t.ready(o, l); {
					case err == nil:
						e.Ready = append(e.Ready, l.Card)
					case CodeOf(err) != CodeNotReady:
						return err
					}
				}
				out.Epics = append(out.Epics, e)
			}
		}
		if out.Lanes, err = t.openLanes(); err != nil {
			return err
		}
		return t.endedLanes(out.Ended)
	})
	return out, err
}

// column runs query and returns its one text column.
func (t *txn) column(query string, args ...any) ([]string, error) {
	rows, err := t.tx.QueryContext(t.ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("board: query: %w", err)
	}
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("board: query: %w", err)
		}
		out = append(out, v)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("board: query: %w", err)
	}
	return out, nil
}

// openLanes lists every open lane attempt with its holder's pending
// request.
func (t *txn) openLanes() ([]LaneFacts, error) {
	rows, err := t.tx.QueryContext(t.ctx, openLanesQuery)
	if err != nil {
		return nil, fmt.Errorf("board: load lanes: %w", err)
	}
	var out []LaneFacts
	at := map[string]int{}
	for rows.Next() {
		var l LaneFacts
		if err := rows.Scan(&l.CardID, &l.Seq, &l.TaskID, &l.Intent); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("board: load lanes: %w", err)
		}
		at[l.CardID] = len(out)
		out = append(out, l)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("board: load lanes: %w", err)
	}
	rows, err = t.tx.QueryContext(t.ctx, laneRequestsQuery)
	if err != nil {
		return nil, fmt.Errorf("board: load lane requests: %w", err)
	}
	for rows.Next() {
		var card, id, kind, raw string
		if err := rows.Scan(&card, &id, &kind, &raw); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("board: load lane requests: %w", err)
		}
		i, ok := at[card]
		if !ok || out[i].Pending != "" {
			continue
		}
		var p payload
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("board: request %s payload: %w", id, err)
		}
		out[i].Pending, out[i].Request = RequestKind(kind), id
		out[i].Landing = out[i].Pending == RequestDone && p.Landing
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("board: load lane requests: %w", err)
	}
	return out, nil
}

// endedLanes fills ended with each Task's latest ended lane attempt.
func (t *txn) endedLanes(ended map[string]LaneEnd) error {
	rows, err := t.tx.QueryContext(t.ctx, endedLanesQuery)
	if err != nil {
		return fmt.Errorf("board: load ended lanes: %w", err)
	}
	for rows.Next() {
		var task, reason string
		var e LaneEnd
		if err := rows.Scan(&task, &e.CardID, &e.Seq, &reason); err != nil {
			_ = rows.Close()
			return fmt.Errorf("board: load ended lanes: %w", err)
		}
		e.Reason = ReleaseReason(reason)
		ended[task] = e
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("board: load ended lanes: %w", err)
	}
	return nil
}

// Turn is where a lane Task's turn stands, as the caller reads it.
type Turn int

// The turns.
const (
	TurnWorking        Turn = iota // a turn is running
	TurnWaiting                    // the turn waits for a permission or an answer
	TurnEnded                      // the turn completed, or none ran
	TurnFailed                     // the turn failed, or the runtime exited
	TurnInterrupted                // a restart interrupted the turn
	TurnOwnerCancelled             // the owner cancelled the turn
	TurnUAMCancelled               // uam cancelled the turn, with a reason
)

// busy reports whether a turn is running or waiting for input: such a Task
// can't be archived until it ends.
func (tn Turn) busy() bool { return tn == TurnWorking || tn == TurnWaiting }

// TaskFact is a lane Task as one executor pass sees it.
type TaskFact struct {
	Stage    Stage
	Turn     Turn
	Provider string
	// Worked reports whether the lane has commits or changes beyond its
	// base; the caller reads it for a failed holder only.
	Worked bool
	// FailedAt is when a failed turn ended.
	FailedAt time.Time
}

// Breaker is a provider's circuit breaker (ADR 0006 §4.5), kept by the
// caller: while Until is ahead nothing starts and nothing is nudged on the
// provider.
type Breaker struct {
	Until    time.Time
	Failures int
	Detail   string
}

// Memory is what the executor keeps between passes, read by Next.
type Memory struct {
	Now time.Time
	// Nudged counts each Task's nudges since boot.
	Nudged map[string]int
	// Starting maps each subtask a start is in progress for to its epic.
	Starting map[string]string
	// Landing marks each subtask a land call is in flight for, from a tool
	// call, an owner job or the executor.
	Landing map[string]bool
	// LandRetry is when each done request waiting to land may be tried
	// again after a transient failure.
	LandRetry map[string]time.Time
	// EpicBackoff is when each epic may start again after a failed start.
	EpicBackoff map[string]time.Time
	// Providers holds each provider's breaker.
	Providers map[string]Breaker
	// SeenFailure is, for each Task, the failed turn already reported.
	SeenFailure map[string]time.Time
}

// Pick is a subtask to start under its approved epic, with the epic's run.
type Pick struct {
	Epic Card
	Card Card
}

// Why says why a lane Task is nudged or cancelled.
type Why string

// The reasons (ADR 0006 §4.2).
const (
	// WhyRestarted: a restart interrupted the holder's turn.
	WhyRestarted Why = "restarted"
	// WhyNoDone: the holder's turn ended without a done request.
	WhyNoDone Why = "no_done"
	// WhyProviderFailed: the provider failed during the holder's turn.
	WhyProviderFailed Why = "provider_failed"
	// WhyStopped: a cancel of a Task whose attempt was stopped.
	WhyStopped Why = "stopped"
)

// Act is something to do to a lane Task about the subtask it holds or last
// held.
type Act struct {
	Task     string
	CardID   string
	Seq      int64
	Provider string
	// Why is set on a nudge and a cancel.
	Why Why
}

// Step is what one executor pass does. Land lists done request IDs;
// ProviderFailed the Tasks whose failed turn counts against their
// provider's breaker, each failure once.
type Step struct {
	Start                        []Pick
	Nudge, Cancel, Retire, Abort []Act
	Land                         []string
	ProviderFailed               []Act
}

// maxFailedNudges is how many nudges a holder whose turns keep failing gets
// before it is retired.
const maxFailedNudges = 3

// Next is the executor's pure step (ADR 0006 §4.2): from the board's facts,
// the lane Tasks' facts (a Task missing from tasks is gone) and the
// executor's memory, it decides what one pass does. It reads its inputs
// only.
//
// Each open lane attempt, in #seq order, takes the first row that applies:
//
//	a landing intent, no land call in flight, its retry
//	  time passed                                             land
//	a done waiting to land, no land call in flight, its
//	  retry time passed, holder not working or waiting        land
//	any other pending done, blocked or split from the holder  nothing: the owner decides
//	holder gone                                               nothing: reconcile releases it
//	holder settled or archived                                retire
//	working, waiting, or cancelled by the owner               nothing
//	interrupted, not nudged since boot                        nudge
//	ended or cancelled by uam: not nudged / nudged            nudge / retire
//	failed, nothing in the lane                               abort
//	failed after work: nudged 3 times / otherwise             retire / nudge once reported
//
// A failed turn not reported yet is reported as a provider failure. A lane
// Task holding nothing is retired once its turn is not busy, or when
// settled; while busy, one whose attempt did not land is cancelled.
//
// Each approved epic that is not paused, not backing off and whose
// provider's breaker is closed starts its ready subtasks, highest priority
// first, then in outline order, up to its parallel limit and MaxLanes over
// every epic, less the slots in use and the starts in progress.
//
// Nothing starts and nothing is nudged on a provider whose breaker is
// open, and a provider gets at most one nudge per pass.
func Next(f RunFacts, tasks map[string]TaskFact, mem Memory) Step {
	var st Step
	open := func(provider string) bool { return mem.Now.Before(mem.Providers[provider].Until) }
	nudged := map[string]bool{}
	nudge := func(a Act, why Why) {
		if open(a.Provider) || nudged[a.Provider] {
			return
		}
		nudged[a.Provider] = true
		a.Why = why
		st.Nudge = append(st.Nudge, a)
	}
	holding := map[string]bool{}
	held := map[string]bool{}
	for _, l := range f.Lanes {
		holding[l.TaskID], held[l.CardID] = true, true
		tf, live := tasks[l.TaskID]
		a := Act{Task: l.TaskID, CardID: l.CardID, Seq: l.Seq, Provider: tf.Provider}
		switch {
		case l.Intent != "":
			if !mem.Landing[l.CardID] && !mem.Now.Before(mem.LandRetry[l.Request]) {
				st.Land = append(st.Land, l.Request)
			}
			continue
		case l.Landing:
			if !mem.Landing[l.CardID] && !mem.Now.Before(mem.LandRetry[l.Request]) && (!live || !tf.Turn.busy()) {
				st.Land = append(st.Land, l.Request)
			}
			continue
		case l.Pending != "" || !live:
			continue
		case tf.Stage == StageSettled || tf.Stage == StageArchived:
			st.Retire = append(st.Retire, a)
			continue
		}
		switch tf.Turn {
		case TurnInterrupted:
			if mem.Nudged[l.TaskID] == 0 {
				nudge(a, WhyRestarted)
			}
		case TurnEnded, TurnUAMCancelled:
			if mem.Nudged[l.TaskID] == 0 {
				nudge(a, WhyNoDone)
			} else {
				st.Retire = append(st.Retire, a)
			}
		case TurnFailed:
			seen, ok := mem.SeenFailure[l.TaskID]
			fresh := !ok || !seen.Equal(tf.FailedAt)
			if fresh {
				st.ProviderFailed = append(st.ProviderFailed, a)
			}
			switch {
			case !tf.Worked:
				st.Abort = append(st.Abort, a)
			case mem.Nudged[l.TaskID] >= maxFailedNudges:
				st.Retire = append(st.Retire, a)
			case !fresh:
				nudge(a, WhyProviderFailed)
			}
		}
	}

	ids := make([]string, 0, len(tasks))
	for id := range tasks {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		e, ok := f.Ended[id]
		tf := tasks[id]
		if holding[id] || !ok || tf.Stage == StageArchived {
			continue
		}
		a := Act{Task: id, CardID: e.CardID, Seq: e.Seq, Provider: tf.Provider}
		switch {
		case tf.Stage != StageSettled && tf.Turn.busy() && e.Reason == ReleaseAccepted:
		case tf.Stage != StageSettled && tf.Turn.busy():
			a.Why = WhyStopped
			st.Cancel = append(st.Cancel, a)
		default:
			st.Retire = append(st.Retire, a)
		}
	}

	starting := map[string]int{}
	free := MaxLanes - f.InUse
	for card, epic := range mem.Starting {
		if !held[card] {
			starting[epic]++
			free--
		}
	}
	for _, e := range f.Epics {
		run := e.Epic.Run
		if free <= 0 || run == nil || e.Epic.Paused != "" || mem.Now.Before(mem.EpicBackoff[e.Epic.ID]) || open(run.Provider) {
			continue
		}
		var ready []Card
		for _, c := range e.Ready {
			if mem.Starting[c.ID] == "" {
				ready = append(ready, c)
			}
		}
		slices.SortStableFunc(ready, func(a, b Card) int { return cmp.Compare(a.Prio, b.Prio) })
		n := min(run.Parallel-e.InUse-starting[e.Epic.ID], free, len(ready))
		for _, c := range ready[:max(n, 0)] {
			st.Start = append(st.Start, Pick{Epic: e.Epic, Card: c})
		}
		free -= max(n, 0)
	}
	return st
}
