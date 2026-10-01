package web

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

var routineModels = []agentapi.Model{{ID: "m1", Name: "Model one"}}

func ptr[T any](v T) *T { return &v }

// newRoutineManager is a started Manager whose fake provider offers m1, and
// a Project in a temporary directory.
func newRoutineManager(t *testing.T) (*Manager, *agenttest.Provider, *store.Store, string) {
	t.Helper()
	prov := agenttest.NewProvider("fake", allCaps)
	prov.SetModels(routineModels, nil)
	st := openTestStore(t)
	m := startManager(t, st, prov)
	return m, prov, st, addProject(t, m, t.TempDir())
}

func hourly(name string) RoutineInput {
	return RoutineInput{Name: ptr(name), Prompt: ptr("check the dependencies"), Model: ptr("m1"), Schedule: &store.RoutineSchedule{Kind: store.ScheduleHours, Hours: 1}}
}

func mustRoutine(t *testing.T, m *Manager, project string, in RoutineInput) Routine {
	t.Helper()
	r, err := m.CreateRoutine(project, in)
	if err != nil {
		t.Fatalf("CreateRoutine: %v", err)
	}
	return r
}

// routine returns the routine id as listed in project.
func routine(t *testing.T, m *Manager, project, id string) Routine {
	t.Helper()
	list, err := m.Routines(project)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range list {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("routine %s not listed", id)
	return Routine{}
}

// runNow fires id and waits until the run has its Task or ended.
func runNow(t *testing.T, m *Manager, project, id string) store.WebRoutineRun {
	t.Helper()
	r, err := m.RunRoutine(id)
	if err != nil {
		t.Fatalf("RunRoutine: %v", err)
	}
	runID := r.Runs[0].ID
	var run store.WebRoutineRun
	waitUntil(t, "the run's task", func() bool {
		for _, got := range routine(t, m, project, id).Runs {
			if got.ID == runID {
				run = got
			}
		}
		return run.TaskID != "" || run.Outcome != RunRunning
	})
	return run
}

func TestNextRun(t *testing.T) {
	loc := time.FixedZone("test", 2*3600)
	// Friday 2026-10-02 10:30 local.
	now := time.Date(2026, 10, 2, 10, 30, 0, 0, loc)
	at := func(d, h, mi int) time.Time { return time.Date(2026, 10, d, h, mi, 0, 0, loc) }
	for _, tc := range []struct {
		name  string
		s     store.RoutineSchedule
		prev  time.Time
		want  time.Time
		after time.Time
	}{
		{"daily later today", store.RoutineSchedule{Kind: store.ScheduleDaily, Time: "11:00"}, time.Time{}, at(2, 11, 0), now},
		{"daily passed today", store.RoutineSchedule{Kind: store.ScheduleDaily, Time: "09:00"}, time.Time{}, at(3, 9, 0), now},
		{"weekdays skips the weekend", store.RoutineSchedule{Kind: store.ScheduleWeekdays, Time: "09:00"}, time.Time{}, at(5, 9, 0), now},
		{"weekly on Wednesday", store.RoutineSchedule{Kind: store.ScheduleWeekly, Time: "08:15", Weekday: 3}, time.Time{}, at(7, 8, 15), now},
		{"weekly today later", store.RoutineSchedule{Kind: store.ScheduleWeekly, Time: "12:00", Weekday: 5}, time.Time{}, at(2, 12, 0), now},
		{"hours from now", store.RoutineSchedule{Kind: store.ScheduleHours, Hours: 6}, time.Time{}, at(2, 16, 30), now},
		{"hours from the due firing", store.RoutineSchedule{Kind: store.ScheduleHours, Hours: 6}, at(2, 10, 0), at(2, 16, 0), now},
		// Down for days: the next firing is the first step after now, not a backlog.
		{"hours after a long gap", store.RoutineSchedule{Kind: store.ScheduleHours, Hours: 6}, at(1, 4, 0).Add(-48 * time.Hour), at(2, 16, 0), now},
	} {
		if got := nextRun(tc.s, tc.prev, tc.after); !got.Equal(tc.want) {
			t.Errorf("%s: next = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestRoutineScheduleValidation(t *testing.T) {
	m, _, _, project := newRoutineManager(t)
	for _, s := range []store.RoutineSchedule{
		{Kind: "cron", Time: "09:00"},
		{Kind: store.ScheduleDaily, Time: "9am"},
		{Kind: store.ScheduleDaily, Time: "25:00"},
		{Kind: store.ScheduleHours, Hours: 0},
		{Kind: store.ScheduleHours, Hours: 25},
		{Kind: store.ScheduleWeekly, Time: "09:00", Weekday: 7},
		{Kind: store.ScheduleDaily, Time: "09:00", Weekday: 2},
	} {
		in := hourly("x")
		in.Schedule = &s
		if _, err := m.CreateRoutine(project, in); statusOf(err) != http.StatusBadRequest {
			t.Errorf("schedule %+v: err = %v, want 400", s, err)
		}
	}
	in := hourly("x")
	in.Model = ptr("not-offered")
	if _, err := m.CreateRoutine(project, in); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("unknown model: err = %v", err)
	}
	if _, err := m.CreateRoutine("nope", hourly("x")); statusOf(err) != http.StatusNotFound {
		t.Fatalf("unknown project: err = %v", err)
	}
	in = hourly("x")
	in.MaxMinutes = ptr(0)
	if _, err := m.CreateRoutine(project, in); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("zero minutes: err = %v", err)
	}
}

// A run starts a normal Task in the Project: named after the routine and the
// date, in safe mode by default, marked as the routine's, without
// uam_create_task, with the prompt as its first message. Its turn's end is
// the run's outcome.
func TestRoutineRunStartsATaskAndRecordsItsOutcome(t *testing.T) {
	m, prov, _, project := newRoutineManager(t)
	r := mustRoutine(t, m, project, hourly("Deps check"))
	if r.Mode != "safe" || !r.Enabled || r.MaxRunsPerDay != defaultRoutineRunsPerDay || r.MaxMinutes != defaultRoutineMinutes || r.NextRun.IsZero() {
		t.Fatalf("created = %+v", r)
	}
	run := runNow(t, m, project, r.ID)
	if run.Trigger != TriggerManual || run.Outcome != RunRunning || run.TaskID == "" {
		t.Fatalf("run = %+v", run)
	}
	sum, err := m.Summary(run.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sum.Name, "Deps check · ") || sum.RoutineID != r.ID || sum.Mode != "safe" || sum.ProjectID != project || sum.Model != "m1" || sum.State != StateWorking {
		t.Fatalf("task = %+v", sum)
	}
	conv := taskConv(t, prov, sum.ID)
	if sends := conv.Sends(); !slices.Equal(sends, []string{"check the dependencies"}) {
		t.Fatalf("sends = %q", sends)
	}
	if slices.Contains(toolNames(conv.Request().Tools), createTaskToolName) {
		t.Fatal("a routine's task got uam_create_task")
	}
	m.checkRoutines()
	if got := routine(t, m, project, r.ID).Runs[0]; got.Outcome != RunRunning {
		t.Fatalf("run while working = %+v", got)
	}
	conv.EmitTurn(agentapi.TurnCompleted, "")
	m.checkRoutines()
	if got := routine(t, m, project, r.ID).Runs[0]; got.Outcome != RunFinished || got.EndedAt.IsZero() {
		t.Fatalf("run after the turn = %+v", got)
	}
}

// A firing while the previous run's Task still works is recorded as skipped
// and starts nothing.
func TestRoutineRunsNeverOverlap(t *testing.T) {
	m, prov, _, project := newRoutineManager(t)
	r := mustRoutine(t, m, project, hourly("Flaky tests"))
	first := runNow(t, m, project, r.ID)
	opens := len(prov.Opens())
	second, err := m.RunRoutine(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := second.Runs[0]; got.Outcome != RunSkipped || got.Reason != "still running" || got.TaskID != "" {
		t.Fatalf("second run = %+v", got)
	}
	if len(prov.Opens()) != opens {
		t.Fatal("a skipped run opened a conversation")
	}
	// The run ended, but the owner sent its Task a follow-up: still running.
	conv := taskConv(t, prov, first.TaskID)
	conv.EmitTurn(agentapi.TurnCompleted, "")
	m.checkRoutines()
	mustSubmit(t, m, first.TaskID, "and the lock file", mustUUID(t), ModeSend, SubmissionAccepted)
	if third, _ := m.RunRoutine(r.ID); third.Runs[0].Outcome != RunSkipped {
		t.Fatalf("run beside a working follow-up = %+v", third.Runs[0])
	}
}

// A turn still running after the time limit is cancelled, and the run says so.
func TestRoutineTimeLimitCancelsTheTurn(t *testing.T) {
	m, prov, _, project := newRoutineManager(t)
	in := hourly("Long one")
	in.MaxMinutes = ptr(5)
	r := mustRoutine(t, m, project, in)
	run := runNow(t, m, project, r.ID)
	conv := taskConv(t, prov, run.TaskID)
	m.checkRoutines()
	if conv.Cancels() != 0 {
		t.Fatal("cancelled within the limit")
	}
	m.routines.mu.Lock()
	m.routines.active[r.ID].started = m.now().Add(-6 * time.Minute)
	m.routines.mu.Unlock()
	m.checkRoutines()
	if conv.Cancels() != 1 {
		t.Fatalf("cancels = %d, want 1", conv.Cancels())
	}
	conv.EmitTurn(agentapi.TurnCancelled, "")
	m.checkRoutines()
	got := routine(t, m, project, r.ID).Runs[0]
	if got.Outcome != RunTimeLimit || !strings.Contains(got.Reason, "5 minutes") {
		t.Fatalf("run = %+v", got)
	}
}

// Runs that started a Task count against the day's limit; skips do not.
func TestRoutineDailyLimit(t *testing.T) {
	m, prov, _, project := newRoutineManager(t)
	in := hourly("Once a day")
	in.MaxRunsPerDay = ptr(1)
	r := mustRoutine(t, m, project, in)
	run := runNow(t, m, project, r.ID)
	taskConv(t, prov, run.TaskID).EmitTurn(agentapi.TurnCompleted, "")
	m.checkRoutines()
	again, err := m.RunRoutine(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Runs[0]; got.Outcome != RunSkipped || !strings.Contains(got.Reason, "limit of 1 run a day") {
		t.Fatalf("second run = %+v", got)
	}
}

// Pausing clears the next run and stops firings; resuming sets the next one.
func TestRoutinePauseAndResume(t *testing.T) {
	m, _, _, project := newRoutineManager(t)
	r := mustRoutine(t, m, project, hourly("Pausable"))
	paused, err := m.UpdateRoutine(r.ID, RoutineInput{Enabled: ptr(false)})
	if err != nil || paused.Enabled || !paused.NextRun.IsZero() {
		t.Fatalf("paused = %+v, %v", paused, err)
	}
	resumed, err := m.UpdateRoutine(r.ID, RoutineInput{Enabled: ptr(true), Mode: ptr("yolo")})
	if err != nil || !resumed.Enabled || resumed.NextRun.IsZero() || resumed.Mode != "yolo" {
		t.Fatalf("resumed = %+v, %v", resumed, err)
	}
	if err := m.DeleteRoutine(r.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := m.Routines(project); len(list) != 0 {
		t.Fatalf("routines after delete = %+v", list)
	}
}

// Across a restart the next run is kept, and a firing missed while the
// service was down runs once, not once per missed firing. A run whose turn
// the stop interrupted is recorded as failed.
func TestRoutineRestartRunsAMissedFiringOnce(t *testing.T) {
	prov := agenttest.NewProvider("fake", allCaps)
	prov.SetModels(routineModels, nil)
	st := openTestStore(t)
	m := NewManager(st, []agentapi.Provider{prov})
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	project := addProject(t, m, t.TempDir())
	kept := mustRoutine(t, m, project, hourly("Kept"))
	missed := mustRoutine(t, m, project, hourly("Missed"))
	interrupted := runNow(t, m, project, kept.ID)
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Three days pass with the service down.
	if err := st.Update(func(cfg *store.Config) error {
		r := cfg.WebRoutines[missed.ID]
		r.NextRun = time.Now().Add(-72 * time.Hour).Truncate(time.Minute)
		cfg.WebRoutines[missed.ID] = r
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	m2 := startManager(t, st, prov)
	waitUntil(t, "the missed firing", func() bool { return len(routine(t, m2, project, missed.ID).Runs) > 0 })
	m2.checkRoutines()
	got := routine(t, m2, project, missed.ID)
	if len(got.Runs) != 1 || got.Runs[0].Trigger != TriggerMissed || !got.NextRun.After(time.Now()) || got.NextRun.After(time.Now().Add(time.Hour)) {
		t.Fatalf("missed routine = %+v", got)
	}
	if k := routine(t, m2, project, kept.ID); !k.NextRun.Equal(kept.NextRun) || len(k.Runs) != 1 {
		t.Fatalf("kept routine = %+v, next run was %v", k, kept.NextRun)
	} else if k.Runs[0].ID != interrupted.ID || k.Runs[0].Outcome != RunFailed || !strings.Contains(k.Runs[0].Reason, "stopped") {
		t.Fatalf("interrupted run = %+v", k.Runs[0])
	}
}

// Removing a Project removes its routines, on disk too.
func TestRemoveProjectRemovesItsRoutines(t *testing.T) {
	m, _, st, project := newRoutineManager(t)
	mustRoutine(t, m, project, hourly("Gone"))
	if err := m.RemoveProject(project); err != nil {
		t.Fatal(err)
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.WebRoutines) != 0 {
		t.Fatalf("stored routines = %+v", cfg.WebRoutines)
	}
	m.routines.mu.Lock()
	defer m.routines.mu.Unlock()
	if len(m.routines.list) != 0 {
		t.Fatal("routine still held")
	}
}

// A due firing while the service runs starts a run on the schedule and moves
// the next run one step on.
func TestRoutineFiresOnSchedule(t *testing.T) {
	m, _, _, project := newRoutineManager(t)
	r := mustRoutine(t, m, project, hourly("Scheduled"))
	due := m.now().Add(-time.Second)
	m.routines.mu.Lock()
	// The service has run since before the firing was due.
	m.routines.started = due.Add(-time.Minute)
	m.routines.list[r.ID].NextRun = due
	m.routines.mu.Unlock()
	m.checkRoutines()
	got := routine(t, m, project, r.ID)
	if len(got.Runs) != 1 || got.Runs[0].Trigger != TriggerSchedule || !got.NextRun.Equal(due.Add(time.Hour)) {
		t.Fatalf("routine = %+v", got)
	}
	waitUntil(t, "the scheduled run's task", func() bool { return routine(t, m, project, r.ID).Runs[0].TaskID != "" })
}
