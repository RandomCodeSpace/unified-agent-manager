package web

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// Routines are recurring work in a Project (docs/web.md): on its schedule, in
// the service's local time, a routine starts a normal Task with its prompt and
// model. A run never overlaps the previous one, a day allows at most
// MaxRunsPerDay runs, and a turn still running after MaxMinutes is cancelled.
// A firing missed while the service was down runs once when it starts.

// Run outcomes. A run is running from its firing until its Task's turn ends.
const (
	RunRunning   = "running"
	RunFinished  = "finished"
	RunFailed    = "failed"
	RunCancelled = "cancelled"
	RunTimeLimit = "time_limit"
	RunSkipped   = "skipped"
)

// Run triggers: the schedule, the schedule's firing missed while the service
// was down (run once on start), or Run now.
const (
	TriggerSchedule = "schedule"
	TriggerMissed   = "missed"
	TriggerManual   = "manual"
)

// Routine defaults for a create that leaves them out.
const (
	defaultRoutineRunsPerDay = 24
	defaultRoutineMinutes    = 30
	// maxRoutineNameRunes leaves room in a Task name for " · " and the date.
	maxRoutineNameRunes = maxNameRunes - 20
	// routineCancelRetry is how long a run over its time limit waits before
	// asking again to cancel a turn that is still running.
	routineCancelRetry = time.Minute
)

// routineTick is how often the scheduler checks for due firings and ended
// runs; a var for tests.
var routineTick = 10 * time.Second

// routineState is the scheduler's state.
//
// mu guards list and active and orders routine writes to sessions.json, which
// are made while holding it. It is taken before Manager.mu, never while
// holding it, a Task's op or projectMu; RemoveProject takes it after
// projectMu.
type routineState struct {
	mu     sync.Mutex
	list   map[string]*store.WebRoutine
	active map[string]*activeRun
	// started is when the service loaded the routines: a firing due before it
	// was missed while the service was down.
	started time.Time
	kick    chan struct{}
}

// activeRun is a routine's running run. taskID is empty while its Task is
// being created; limited is set once the time limit asked to cancel it.
type activeRun struct {
	runID, taskID string
	started       time.Time
	limited       bool
	cancelAt      time.Time
}

// Routine is a routine as the browser sees it; Runs is newest first.
type Routine struct {
	ID            string                `json:"id"`
	ProjectID     string                `json:"project_id"`
	Name          string                `json:"name"`
	Prompt        string                `json:"prompt"`
	Provider      string                `json:"provider"`
	Model         string                `json:"model"`
	Schedule      store.RoutineSchedule `json:"schedule"`
	Enabled       bool                  `json:"enabled"`
	Mode          string                `json:"mode"`
	Autopilot     bool                  `json:"autopilot"`
	MaxRunsPerDay int                   `json:"max_runs_per_day"`
	MaxMinutes    int                   `json:"max_minutes"`
	CreatedAt     time.Time             `json:"created_at"`
	NextRun       time.Time             `json:"next_run,omitzero"`
	Runs          []store.WebRoutineRun `json:"runs"`
}

// RoutineInput is the body of a routine's create (every field but Model,
// Enabled, Mode, Autopilot and the limits required) or edit (only the fields
// given change).
type RoutineInput struct {
	Name          *string                `json:"name"`
	Prompt        *string                `json:"prompt"`
	Model         *string                `json:"model"`
	Schedule      *store.RoutineSchedule `json:"schedule"`
	Enabled       *bool                  `json:"enabled"`
	Mode          *string                `json:"mode"`
	Autopilot     *bool                  `json:"autopilot"`
	MaxRunsPerDay *int                   `json:"max_runs_per_day"`
	MaxMinutes    *int                   `json:"max_minutes"`
}

var errRoutineNotFound = newError(http.StatusNotFound, "routine not found")

func routineView(r *store.WebRoutine) Routine {
	runs := slices.Clone(r.Runs)
	slices.Reverse(runs)
	if runs == nil {
		runs = []store.WebRoutineRun{}
	}
	return Routine{ID: r.ID, ProjectID: r.ProjectID, Name: r.Name, Prompt: r.Prompt, Provider: r.Provider, Model: r.Model, Schedule: r.Schedule,
		Enabled: r.Enabled, Mode: string(r.Mode), Autopilot: r.Autopilot, MaxRunsPerDay: r.MaxRunsPerDay, MaxMinutes: r.MaxMinutes, CreatedAt: r.CreatedAt, NextRun: r.NextRun, Runs: runs}
}

// nextRun is a schedule's first firing after now. Every N hours steps from
// prev, the firing just due, or from now when there is none; the other kinds
// take the next clock time on a day they allow, in now's location.
func nextRun(s store.RoutineSchedule, prev, now time.Time) time.Time {
	if s.Kind == store.ScheduleHours {
		step := time.Duration(s.Hours) * time.Hour
		if prev.IsZero() {
			return now.Truncate(time.Minute).Add(step)
		}
		next := prev.Add(step)
		if !next.After(now) {
			next = next.Add(step * (now.Sub(next)/step + 1))
		}
		return next
	}
	hour, minute, _ := s.Clock()
	for d := 0; d <= 7; d++ {
		t := time.Date(now.Year(), now.Month(), now.Day()+d, hour, minute, 0, 0, now.Location())
		if !t.After(now) {
			continue
		}
		switch wd := t.Weekday(); {
		case s.Kind == store.ScheduleWeekdays && (wd == time.Saturday || wd == time.Sunday):
			continue
		case s.Kind == store.ScheduleWeekly && int(wd) != s.Weekday:
			continue
		}
		return t
	}
	return now.Add(7 * 24 * time.Hour)
}

// runsToday counts r's runs since local midnight that started, or tried to
// start, a Task: skips do not count.
func runsToday(r *store.WebRoutine, now time.Time) int {
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	n := 0
	for _, run := range r.Runs {
		if run.Outcome != RunSkipped && !run.At.Before(midnight) {
			n++
		}
	}
	return n
}

// routineTaskName is a run's Task name: the routine's name and the firing's
// local date and time.
func routineTaskName(name string, at time.Time) string {
	return name + " · " + at.Format("2006-01-02 15:04")
}

// loadRoutines takes the stored routines at Start. The latest run still
// running when the service stopped is resolved from its Task by the first
// check, or failed when it had no Task yet; skips recorded after it do not
// change that. An enabled routine without a next run gets one.
func (m *Manager) loadRoutines(cfg store.Config) {
	now := m.now()
	rs := &m.routines
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.list, rs.active, rs.started = map[string]*store.WebRoutine{}, map[string]*activeRun{}, now
	rs.kick = make(chan struct{}, 1)
	var changed []string
	for _, id := range slices.Sorted(maps.Keys(cfg.WebRoutines)) {
		r := cfg.WebRoutines[id]
		dirty := false
		latest := -1
		for i, run := range r.Runs {
			if run.Outcome == RunRunning {
				latest = i
			}
		}
		for i := range r.Runs {
			run := &r.Runs[i]
			if run.Outcome != RunRunning {
				continue
			}
			if run.TaskID == "" || i != latest {
				run.Outcome, run.Reason, run.EndedAt = RunFailed, "the uam web service stopped before the task started", now
				dirty = true
				continue
			}
			rs.active[id] = &activeRun{runID: run.ID, taskID: run.TaskID, started: run.At}
		}
		if r.Enabled && r.NextRun.IsZero() {
			r.NextRun = nextRun(r.Schedule, time.Time{}, now)
			dirty = true
		}
		rs.list[id] = &r
		if dirty {
			changed = append(changed, id)
		}
	}
	m.saveRoutinesLocked(changed...)
}

// saveRoutinesLocked writes the routines ids as held, or deletes those no
// longer held. A failed write is logged; the held state stays in force.
// The caller holds routines.mu.
func (m *Manager) saveRoutinesLocked(ids ...string) {
	if len(ids) == 0 {
		return
	}
	if err := m.store.Update(func(cfg *store.Config) error {
		for _, id := range ids {
			r := m.routines.list[id]
			if r == nil {
				delete(cfg.WebRoutines, id)
				continue
			}
			if cfg.WebRoutines == nil {
				cfg.WebRoutines = map[string]store.WebRoutine{}
			}
			cfg.WebRoutines[id] = *r
		}
		return nil
	}); err != nil {
		log.Warn("save routines failed", "error", err)
	}
}

func (m *Manager) routineLoop() {
	defer m.wg.Done()
	ticker := time.NewTicker(routineTick)
	defer ticker.Stop()
	for {
		m.checkRoutines()
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
		case <-m.routines.kick:
		}
	}
}

func (m *Manager) kickRoutines() {
	select {
	case m.routines.kick <- struct{}{}:
	default:
	}
}

// routineStart is a run whose Task is to be created.
type routineStart struct {
	routine store.WebRoutine
	run     store.WebRoutineRun
}

// checkRoutines ends the runs whose Task's turn ended, cancels the turns over
// their time limit, and fires the routines that are due. A due firing moves
// the routine's next run past now first, so a firing missed while the
// service was down runs once, not once per missed firing.
func (m *Manager) checkRoutines() {
	if m.isClosed() {
		return
	}
	rs := &m.routines
	// cancels maps each Task over its run's time limit to its stop reason.
	cancels := map[string]string{}
	var starts []routineStart
	rs.mu.Lock()
	if len(rs.list) == 0 {
		rs.mu.Unlock()
		return
	}
	now := m.now()
	changed := map[string]bool{}
	for _, id := range slices.Sorted(maps.Keys(rs.active)) {
		run, r := rs.active[id], rs.list[id]
		if r == nil {
			delete(rs.active, id)
			continue
		}
		outcome, reason, cancel := m.runOutcome(r, run, now)
		if cancel {
			cancels[run.taskID] = fmt.Sprintf("Stopped at the routine's time limit (%d min)", r.MaxMinutes)
		}
		if outcome != "" {
			endRun(r, run.runID, outcome, reason, now)
			delete(rs.active, id)
			changed[id] = true
		}
	}
	for _, id := range slices.Sorted(maps.Keys(rs.list)) {
		r := rs.list[id]
		if !r.Enabled || r.NextRun.IsZero() || r.NextRun.After(now) {
			continue
		}
		trigger := TriggerSchedule
		if r.NextRun.Before(rs.started) {
			trigger = TriggerMissed
		}
		r.NextRun = nextRun(r.Schedule, r.NextRun, now)
		if start, ok := m.fireLocked(r, trigger, now); ok {
			starts = append(starts, start)
		}
		changed[id] = true
	}
	m.saveRoutinesLocked(slices.Sorted(maps.Keys(changed))...)
	rs.mu.Unlock()
	for _, id := range slices.Sorted(maps.Keys(cancels)) {
		if _, err := m.cancelBecause(id, stopTimeLimit, cancels[id]); err != nil {
			log.Warn("cancel a routine run over its time limit failed", "session", id, "error", err)
		}
	}
	for _, start := range starts {
		m.goRun(start)
	}
}

// goRun starts a run's Task in the background, unless the service is
// stopping: then the run stays running without a Task, and the next start
// records it as failed.
func (m *Manager) goRun(start routineStart) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.wg.Add(1)
	go m.startRun(start)
}

// runOutcome is how run ended, from its Task's state, or "" while its turn
// runs; cancel asks to cancel a turn over the time limit. The caller holds
// routines.mu.
func (m *Manager) runOutcome(r *store.WebRoutine, run *activeRun, now time.Time) (outcome, reason string, cancel bool) {
	limit := time.Duration(r.MaxMinutes) * time.Minute
	limitReason := fmt.Sprintf("cancelled after the limit of %d %s", r.MaxMinutes, plural(r.MaxMinutes, "minute", "minutes"))
	if run.taskID == "" {
		return "", "", false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[run.taskID]
	if s == nil {
		return RunFailed, "the task was deleted", false
	}
	switch state := s.state(); state {
	case StateCompleted:
		return RunFinished, "", false
	case StateCancelled, StateClosed:
		if run.limited {
			return RunTimeLimit, limitReason, false
		}
		return RunCancelled, "stopped in the task", false
	case StateFailed, StateInterrupted:
		return RunFailed, cmp.Or(s.detail, "the turn failed"), false
	case StateIdle:
		if run.limited {
			return RunTimeLimit, limitReason, false
		}
		reason := "the first message was not sent"
		if s.last != nil && s.last.Error != "" {
			reason = s.last.Error
		}
		return RunFailed, reason, false
	}
	if now.Sub(run.started) < limit || now.Before(run.cancelAt) {
		return "", "", false
	}
	run.limited, run.cancelAt = true, now.Add(routineCancelRetry)
	return "", "", true
}

// endRun records how r's run runID ended.
func endRun(r *store.WebRoutine, runID, outcome, reason string, now time.Time) {
	for i := range r.Runs {
		if r.Runs[i].ID == runID {
			r.Runs[i].Outcome, r.Runs[i].Reason, r.Runs[i].EndedAt = outcome, clipRunes(reason, maxDetailRunes), now
		}
	}
}

// fireLocked records a firing of r: skipped while its previous run, or its
// last run's Task, is still working, or once the day's runs reached the
// limit; else running, to be started. The caller holds routines.mu.
func (m *Manager) fireLocked(r *store.WebRoutine, trigger string, now time.Time) (routineStart, bool) {
	id, err := newUUID()
	if err != nil {
		log.Warn("generate routine run id failed", "routine", r.ID, "error", err)
		return routineStart{}, false
	}
	run := store.WebRoutineRun{ID: id, At: now, Trigger: trigger}
	switch {
	case m.routines.active[r.ID] != nil || m.lastRunBusy(r):
		run.Outcome, run.Reason, run.EndedAt = RunSkipped, "still running", now
	case runsToday(r, now) >= r.MaxRunsPerDay:
		run.Outcome, run.Reason, run.EndedAt = RunSkipped, fmt.Sprintf("the limit of %d %s a day was reached", r.MaxRunsPerDay, plural(r.MaxRunsPerDay, "run", "runs")), now
	default:
		run.Outcome = RunRunning
		m.routines.active[r.ID] = &activeRun{runID: run.ID, started: now}
	}
	r.Runs = append(r.Runs, run)
	if n := len(r.Runs); n > store.MaxRoutineRuns {
		r.Runs = slices.Clone(r.Runs[n-store.MaxRoutineRuns:])
	}
	log.Info("routine fired", "routine", r.ID, "trigger", trigger, "outcome", run.Outcome)
	return routineStart{routine: *r, run: run}, run.Outcome == RunRunning
}

// lastRunBusy reports whether the Task of r's latest run with one is still
// working, for example on a follow-up the owner sent it.
func (m *Manager) lastRunBusy(r *store.WebRoutine) bool {
	for i := len(r.Runs) - 1; i >= 0; i-- {
		if r.Runs[i].TaskID == "" {
			continue
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		s := m.sessions[r.Runs[i].TaskID]
		return s != nil && busy(s.state())
	}
	return false
}

// startRun creates a run's Task as Create would, with the routine's prompt
// as its first message, and records the Task, or the failure, on the run.
func (m *Manager) startRun(start routineStart) {
	defer m.wg.Done()
	r := start.routine
	req := CreateRequest{ProjectID: r.ProjectID, Provider: r.Provider, Model: r.Model, Name: routineTaskName(r.Name, start.run.At), Prompt: r.Prompt, Mode: string(r.Mode), routineID: r.ID, autopilot: r.Autopilot}
	summary, err := func() (SessionSummary, error) {
		prov, workdir, mode, err := m.checkCreate(&req)
		if err != nil {
			return SessionSummary{}, err
		}
		return m.createChecked(req, prov, workdir, mode)
	}()
	rs := &m.routines
	rs.mu.Lock()
	defer rs.mu.Unlock()
	held, run := rs.list[r.ID], rs.active[r.ID]
	if held == nil || run == nil || run.runID != start.run.ID {
		return
	}
	if err != nil {
		log.Warn("routine run did not start its task", "routine", r.ID, "error", err)
		_, msg := errorStatus(err)
		endRun(held, run.runID, RunFailed, msg, m.now())
		delete(rs.active, r.ID)
	} else {
		run.taskID = summary.ID
		for i := range held.Runs {
			if held.Runs[i].ID == run.runID {
				held.Runs[i].TaskID = summary.ID
			}
		}
	}
	m.saveRoutinesLocked(r.ID)
	m.kickRoutines()
}

// startAutopilot turns autopilot on in a run's new Task before its first
// message, as /autopilot on does: the Task then keeps working until the agent
// calls task_complete, or the run's time limit stops it. When that fails the
// first message still goes, and the Task works one turn as usual. A provider
// without typed commands would take the command as a prompt: it is skipped.
func (m *Manager) startAutopilot(s *webSession) {
	m.mu.Lock()
	_, typed := s.conv.(agentapi.CommandExecutor)
	m.mu.Unlock()
	if !typed {
		log.Warn("routine run left autopilot off: the provider has no autopilot command", "session", s.id)
		return
	}
	reqID, err := newUUID()
	if err == nil {
		var sub Submission
		sub, err = m.executeCommand(s, CommandRequest{RequestID: reqID, Name: "autopilot", Arguments: "on"})
		if err == nil && sub.Status != SubmissionAccepted {
			err = errors.New(sub.Error)
		}
	}
	if err != nil {
		log.Warn("routine run could not turn autopilot on", "session", s.id, "error", err)
	}
}

// Routines lists a Project's routines, oldest first.
func (m *Manager) Routines(projectID string) ([]Routine, error) {
	m.mu.Lock()
	known := m.projects[projectID] != nil
	m.mu.Unlock()
	if !known {
		return nil, errProjectNotFound
	}
	return m.listRoutines(projectID), nil
}

// AllRoutines lists every Project's routines, oldest first.
func (m *Manager) AllRoutines() []Routine {
	return m.listRoutines("")
}

// listRoutines lists projectID's routines, or every routine when it is
// empty, oldest first.
func (m *Manager) listRoutines(projectID string) []Routine {
	rs := &m.routines
	rs.mu.Lock()
	defer rs.mu.Unlock()
	out := []Routine{}
	for _, r := range rs.list {
		if projectID == "" || r.ProjectID == projectID {
			out = append(out, routineView(r))
		}
	}
	slices.SortFunc(out, func(a, b Routine) int { return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), strings.Compare(a.ID, b.ID)) })
	return out
}

// applyRoutine sets the fields in on r, checked as a create or an edit checks
// them. The model is checked against the catalog on a create and when an
// edit sets it, so pausing never depends on the catalog. The caller holds
// routines.mu.
func (m *Manager) applyRoutine(r *store.WebRoutine, in RoutineInput, create bool) error {
	if in.Name != nil {
		name := strings.TrimSpace(displaytext.Sanitize(*in.Name))
		switch {
		case name == "":
			return newError(http.StatusBadRequest, "name is required")
		case utf8.RuneCountInString(name) > maxRoutineNameRunes:
			return newError(http.StatusBadRequest, "name is longer than %d characters", maxRoutineNameRunes)
		}
		r.Name = name
	}
	if in.Prompt != nil {
		if err := checkSpawnPrompt(*in.Prompt); err != nil {
			return newError(http.StatusBadRequest, "%s", err.Error())
		}
		r.Prompt = *in.Prompt
	}
	if in.Schedule != nil {
		if err := in.Schedule.Validate(); err != nil {
			return newError(http.StatusBadRequest, "%s", err.Error())
		}
		r.Schedule = *in.Schedule
	}
	if in.Mode != nil {
		mode, err := parseMode(*in.Mode)
		if err != nil {
			return err
		}
		r.Mode = mode
	}
	if in.Autopilot != nil {
		r.Autopilot = *in.Autopilot
	}
	if in.MaxRunsPerDay != nil {
		if *in.MaxRunsPerDay < 1 || *in.MaxRunsPerDay > store.MaxRoutineRunsPerDay {
			return newError(http.StatusBadRequest, "runs a day must be 1 to %d", store.MaxRoutineRunsPerDay)
		}
		r.MaxRunsPerDay = *in.MaxRunsPerDay
	}
	if in.MaxMinutes != nil {
		if *in.MaxMinutes < 1 || *in.MaxMinutes > store.MaxRoutineMinutes {
			return newError(http.StatusBadRequest, "minutes a run must be 1 to %d", store.MaxRoutineMinutes)
		}
		r.MaxMinutes = *in.MaxMinutes
	}
	if !create && in.Model == nil {
		return nil
	}
	if in.Model != nil {
		r.Model = *in.Model
	}
	if r.Model == "" {
		return newError(http.StatusBadRequest, "model is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.validateSelectionLocked(r.Provider, r.Model, "", "default")
}

// CreateRoutine adds a routine to a Project. Without a model it takes the one
// New task starts with; it is enabled, with the default limits unless the
// input says otherwise. Nobody watches a run, so it is Yolo with autopilot
// unless the input says otherwise; autopilot defaults to on only in Yolo.
func (m *Manager) CreateRoutine(projectID string, in RoutineInput) (Routine, error) {
	if in.Name == nil || in.Prompt == nil || in.Schedule == nil {
		return Routine{}, newError(http.StatusBadRequest, "name, prompt and schedule are required")
	}
	id, err := newUUID()
	if err != nil {
		return Routine{}, fmt.Errorf("generate routine id: %w", err)
	}
	now := m.now()
	r := store.WebRoutine{ID: id, ProjectID: projectID, Enabled: true, Mode: store.ModeYolo, MaxRunsPerDay: defaultRoutineRunsPerDay, MaxMinutes: defaultRoutineMinutes, CreatedAt: now}
	m.mu.Lock()
	known := m.projects[projectID] != nil
	r.Provider, r.Model = m.newTaskSelectionLocked()
	m.mu.Unlock()
	if !known {
		return Routine{}, errProjectNotFound
	}
	if in.Enabled != nil {
		r.Enabled = *in.Enabled
	}
	rs := &m.routines
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if err := m.applyRoutine(&r, in, true); err != nil {
		return Routine{}, err
	}
	if in.Autopilot == nil {
		r.Autopilot = r.Mode == store.ModeYolo
	}
	if r.Enabled {
		r.NextRun = nextRun(r.Schedule, time.Time{}, now)
	}
	if err := m.store.Update(func(cfg *store.Config) error {
		if _, ok := cfg.WebProjects[projectID]; !ok {
			return errProjectNotFound
		}
		if cfg.WebRoutines == nil {
			cfg.WebRoutines = map[string]store.WebRoutine{}
		}
		cfg.WebRoutines[id] = r
		return nil
	}); err != nil {
		if errors.Is(err, errProjectNotFound) {
			return Routine{}, err
		}
		return Routine{}, fmt.Errorf("save routine: %w", err)
	}
	rs.list[id] = &r
	log.Info("routine created", "routine", id, "project", projectID)
	return routineView(&r), nil
}

// UpdateRoutine edits a routine. Pausing clears its next run; resuming it,
// or changing its schedule while enabled, sets the next one from now. A run
// in progress is not affected.
func (m *Manager) UpdateRoutine(id string, in RoutineInput) (Routine, error) {
	rs := &m.routines
	rs.mu.Lock()
	defer rs.mu.Unlock()
	held := rs.list[id]
	if held == nil {
		return Routine{}, errRoutineNotFound
	}
	next := *held
	next.Runs = slices.Clone(held.Runs)
	if err := m.applyRoutine(&next, in, false); err != nil {
		return Routine{}, err
	}
	if in.Enabled != nil {
		next.Enabled = *in.Enabled
	}
	switch {
	case !next.Enabled:
		next.NextRun = time.Time{}
	case !held.Enabled || next.Schedule != held.Schedule:
		next.NextRun = nextRun(next.Schedule, time.Time{}, m.now())
	}
	*held = next
	m.saveRoutinesLocked(id)
	return routineView(held), nil
}

// DeleteRoutine removes a routine and its run history. Tasks its runs
// started stay, a running one included.
func (m *Manager) DeleteRoutine(id string) error {
	rs := &m.routines
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.list[id] == nil {
		return errRoutineNotFound
	}
	delete(rs.list, id)
	delete(rs.active, id)
	m.saveRoutinesLocked(id)
	log.Info("routine deleted", "routine", id)
	return nil
}

// RunRoutine fires a routine now, paused or not, under the same rules as its
// schedule: it is skipped while the previous run is still running or once
// the day's runs reached the limit. Its next scheduled run does not move.
func (m *Manager) RunRoutine(id string) (Routine, error) {
	if m.isClosed() {
		return Routine{}, errShuttingDown
	}
	rs := &m.routines
	rs.mu.Lock()
	held := rs.list[id]
	if held == nil {
		rs.mu.Unlock()
		return Routine{}, errRoutineNotFound
	}
	start, ok := m.fireLocked(held, TriggerManual, m.now())
	m.saveRoutinesLocked(id)
	view := routineView(held)
	if ok {
		m.goRun(start)
	}
	rs.mu.Unlock()
	return view, nil
}

// forgetRoutinesLocked drops a removed Project's routines; RemoveProject
// deletes them from the store in its own write. The caller holds
// routines.mu.
func (m *Manager) forgetRoutinesLocked(projectID string) {
	for id, r := range m.routines.list {
		if r.ProjectID == projectID {
			delete(m.routines.list, id)
			delete(m.routines.active, id)
		}
	}
}

func (s *Server) routineRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/routines", s.handleAllRoutines)
	mux.HandleFunc("GET /api/projects/{id}/routines", s.handleRoutines)
	mux.HandleFunc("POST /api/projects/{id}/routines", s.handleCreateRoutine)
	mux.HandleFunc("PATCH /api/routines/{id}", s.handleUpdateRoutine)
	mux.HandleFunc("DELETE /api/routines/{id}", s.handleDeleteRoutine)
	mux.HandleFunc("POST /api/routines/{id}/run", s.handleRunRoutine)
}

func (s *Server) handleRoutines(w http.ResponseWriter, r *http.Request) {
	list, err := s.m.Routines(r.PathValue("id"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"routines": list})
}

func (s *Server) handleAllRoutines(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"routines": s.m.AllRoutines()})
}

func (s *Server) handleCreateRoutine(w http.ResponseWriter, r *http.Request) {
	var body RoutineInput
	if !decodeBody(w, r, &body) {
		return
	}
	out, err := s.m.CreateRoutine(r.PathValue("id"), body)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) handleUpdateRoutine(w http.ResponseWriter, r *http.Request) {
	var body RoutineInput
	if !decodeBody(w, r, &body) {
		return
	}
	out, err := s.m.UpdateRoutine(r.PathValue("id"), body)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDeleteRoutine(w http.ResponseWriter, r *http.Request) {
	if err := s.m.DeleteRoutine(r.PathValue("id")); err != nil {
		writeFailure(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRunRoutine(w http.ResponseWriter, r *http.Request) {
	out, err := s.m.RunRoutine(r.PathValue("id"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, out)
}
