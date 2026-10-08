package web

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
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

// commandProvider opens conversations that run typed commands, as Copilot's
// do, and records each command with its arguments.
type commandProvider struct {
	*agenttest.Provider
	mu       sync.Mutex
	commands []string
}

type commandConversation struct {
	agentapi.Conversation
	p *commandProvider
}

func (p *commandProvider) Open(ctx context.Context, req agentapi.OpenRequest) (agentapi.Conversation, error) {
	conv, err := p.Provider.Open(ctx, req)
	if err != nil {
		return nil, err
	}
	return &commandConversation{Conversation: conv, p: p}, nil
}

func (c *commandConversation) ExecuteCommand(_ context.Context, name string, prompt agentapi.Prompt) (*agentapi.CommandResult, error) {
	c.p.mu.Lock()
	defer c.p.mu.Unlock()
	c.p.commands = append(c.p.commands, name+" "+prompt.Text)
	return &agentapi.CommandResult{Kind: "completed"}, nil
}

func (p *commandProvider) ran() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.commands)
}

// newCommandRoutineManager is newRoutineManager with a provider whose
// conversations offer /autopilot.
func newCommandRoutineManager(t *testing.T) (*Manager, *commandProvider, string) {
	t.Helper()
	prov := &commandProvider{Provider: agenttest.NewProvider("fake", allCaps)}
	prov.SetModels(routineModels, nil)
	prov.SetCommands([]agentapi.Command{{Name: "autopilot", AllowDuringTurn: true}}, nil)
	m := startManager(t, openTestStore(t), prov)
	return m, prov, addProject(t, m, t.TempDir())
}

// Nobody watches a run, so a new routine is Yolo with autopilot: its Task
// turns autopilot on before the first message goes.
func TestRoutineDefaultsToYoloWithAutopilot(t *testing.T) {
	m, prov, project := newCommandRoutineManager(t)
	r := mustRoutine(t, m, project, hourly("Unattended"))
	if r.Mode != "yolo" || !r.Autopilot {
		t.Fatalf("created = %+v", r)
	}
	run := runNow(t, m, project, r.ID)
	sum, err := m.Summary(run.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Mode != "yolo" {
		t.Fatalf("task mode = %q", sum.Mode)
	}
	if got := prov.ran(); !slices.Equal(got, []string{"autopilot on"}) {
		t.Fatalf("commands = %q", got)
	}
	if sends := taskConv(t, prov.Provider, sum.ID).Sends(); !slices.Equal(sends, []string{"check the dependencies"}) {
		t.Fatalf("sends = %q", sends)
	}
}

// Safe, or Yolo without autopilot, is kept when asked for: no autopilot.
func TestRoutineExplicitModesSkipAutopilot(t *testing.T) {
	m, prov, project := newCommandRoutineManager(t)
	safe := hourly("Watched")
	safe.Mode = ptr("safe")
	yolo := hourly("One turn")
	yolo.Mode, yolo.Autopilot = ptr("yolo"), ptr(false)
	for _, in := range []RoutineInput{safe, yolo} {
		r := mustRoutine(t, m, project, in)
		if r.Mode != *in.Mode || r.Autopilot {
			t.Fatalf("created = %+v", r)
		}
		run := runNow(t, m, project, r.ID)
		if sum, err := m.Summary(run.TaskID); err != nil || sum.Mode != *in.Mode {
			t.Fatalf("task = %+v, %v", sum, err)
		}
	}
	if got := prov.ran(); len(got) != 0 {
		t.Fatalf("commands = %q", got)
	}
}

// A run starts a normal Task in the Project: named after the routine and the
// date, in the routine's mode, marked as the routine's, without
// uam_create_task, with the prompt as its first message. Its turn's end is
// the run's outcome.
func TestRoutineRunStartsATaskAndRecordsItsOutcome(t *testing.T) {
	m, prov, _, project := newRoutineManager(t)
	in := hourly("Deps check")
	in.Mode = ptr("safe")
	r := mustRoutine(t, m, project, in)
	if r.Mode != "safe" || r.Autopilot || !r.Enabled || r.MaxRunsPerDay != defaultRoutineRunsPerDay || r.MaxMinutes != defaultRoutineMinutes || r.NextRun.IsZero() {
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

// A turn still running after the time limit is cancelled, and the run says
// so: the safety net of an autopilot run that never calls task_complete.
func TestRoutineTimeLimitCancelsTheTurn(t *testing.T) {
	m, prov, project := newCommandRoutineManager(t)
	in := hourly("Long one")
	in.MaxMinutes = ptr(5)
	r := mustRoutine(t, m, project, in)
	run := runNow(t, m, project, r.ID)
	if got := prov.ran(); !slices.Equal(got, []string{"autopilot on"}) {
		t.Fatalf("commands = %q", got)
	}
	conv := taskConv(t, prov.Provider, run.TaskID)
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
	// The Task says the routine stopped it, not the owner.
	sum, err := m.Summary(run.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if sum.State != StateCancelled || sum.StateDetail != "Stopped at the routine's time limit (5 min)" || sum.StopReason != stopTimeLimit {
		t.Fatalf("task = %s, %q, %q", sum.State, sum.StateDetail, sum.StopReason)
	}
	// The owner's own Stop on a later turn records no such reason.
	mustSubmit(t, m, run.TaskID, "try again", mustUUID(t), ModeSend, SubmissionAccepted)
	if _, err := m.Cancel(run.TaskID); err != nil {
		t.Fatal(err)
	}
	conv.EmitTurn(agentapi.TurnCancelled, "")
	if sum, _ := m.Summary(run.TaskID); sum.State != StateCancelled || sum.StateDetail != "" || sum.StopReason != stopOwner {
		t.Fatalf("task after the owner's stop = %s, %q, %q", sum.State, sum.StateDetail, sum.StopReason)
	}
}

// Runs that started a Task count against the day's limit; skips do not.
func TestRoutineDailyLimit(t *testing.T) {
	m, prov, _, project := newRoutineManager(t)
	in := hourly("Twice a day")
	in.MaxRunsPerDay = ptr(2)
	r := mustRoutine(t, m, project, in)
	first := runNow(t, m, project, r.ID)
	if busy, err := m.RunRoutine(r.ID); err != nil || busy.Runs[0].Outcome != RunSkipped || busy.Runs[0].Reason != "still running" {
		t.Fatalf("run while busy = %+v, %v", busy.Runs, err)
	}
	taskConv(t, prov, first.TaskID).EmitTurn(agentapi.TurnCompleted, "")
	m.checkRoutines()
	second := runNow(t, m, project, r.ID)
	if second.TaskID == "" {
		t.Fatalf("second run = %+v, want a Task: the skip must not count", second)
	}
	taskConv(t, prov, second.TaskID).EmitTurn(agentapi.TurnCompleted, "")
	m.checkRoutines()
	again, err := m.RunRoutine(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Runs[0]; got.Outcome != RunSkipped || !strings.Contains(got.Reason, "limit of 2 runs a day") {
		t.Fatalf("third run = %+v", got)
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

// A running run followed by a "still running" skip is resolved from its Task
// after a restart, not failed as never started. The service stops before
// its check saw the turn end, as on a crash.
func TestRoutineRestartKeepsARunningRunBeforeASkip(t *testing.T) {
	prov := agenttest.NewProvider("fake", allCaps)
	prov.SetModels(routineModels, nil)
	st := openTestStore(t)
	m := NewManager(st, []agentapi.Provider{prov})
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	project := addProject(t, m, t.TempDir())
	r := mustRoutine(t, m, project, hourly("Busy"))
	run := runNow(t, m, project, r.ID)
	if again, err := m.RunRoutine(r.ID); err != nil || again.Runs[0].Outcome != RunSkipped {
		t.Fatalf("second run = %+v, %v", again.Runs, err)
	}
	taskConv(t, prov, run.TaskID).EmitTurn(agentapi.TurnCompleted, "")
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The stored run is as the crash left it: still running.
	if err := st.Update(func(cfg *store.Config) error {
		stored := cfg.WebRoutines[r.ID]
		stored.Runs[0].Outcome, stored.Runs[0].EndedAt = RunRunning, time.Time{}
		cfg.WebRoutines[r.ID] = stored
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	m2 := startManager(t, st, prov)
	m2.checkRoutines()
	runs := routine(t, m2, project, r.ID).Runs
	if len(runs) != 2 || runs[1].ID != run.ID || runs[1].Outcome != RunFinished {
		t.Fatalf("runs after restart = %+v", runs)
	}
}

// A run's Task says in its export which routine started it, as a spawned or
// rerun Task names its origin.
func TestRoutineTaskExportNamesTheRoutine(t *testing.T) {
	m, _, _, project := newRoutineManager(t)
	r := mustRoutine(t, m, project, hourly("Nightly"))
	run := runNow(t, m, project, r.ID)
	body, _, err := m.ExportMarkdown(context.Background(), run.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "\n- Started by routine "+r.ID+"\n") {
		t.Fatalf("export = %s", body)
	}
}

// A weekly routine on Sunday keeps its day in what the page reads: weekday 0
// is a day, not an empty value.
func TestWeeklyRoutineOnSundayListsItsDay(t *testing.T) {
	m, _, _, project := newRoutineManager(t)
	in := hourly("Sunday review")
	in.Schedule = &store.RoutineSchedule{Kind: store.ScheduleWeekly, Time: "09:00", Weekday: 0}
	r := mustRoutine(t, m, project, in)
	body, err := json.Marshal(routine(t, m, project, r.ID))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"schedule":{"kind":"weekly","time":"09:00","weekday":0}`) {
		t.Fatalf("listed routine = %s", body)
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

// Saved prompts were removed as a feature, but the ones already on disk stay
// there untouched: a settings change and removing the Project they named
// both keep them as written.
func TestStoredSavedPromptsSurvive(t *testing.T) {
	m, _, st, project := newRoutineManager(t)
	want := `[{"id":"p1","name":"Review","text":"review the diff","project_id":"` + project + `","created_at":"2026-09-30T10:00:00Z"},{"id":"p2","name":"Tests","text":"run the tests","created_at":"2026-09-30T10:01:00Z"}]`
	raw := func() map[string]json.RawMessage {
		t.Helper()
		data, err := os.ReadFile(st.Path())
		if err != nil {
			t.Fatal(err)
		}
		var file map[string]json.RawMessage
		if err := json.Unmarshal(data, &file); err != nil {
			t.Fatal(err)
		}
		var web map[string]json.RawMessage
		if err := json.Unmarshal(file["web_settings"], &web); err != nil {
			t.Fatal(err)
		}
		return web
	}
	data, err := os.ReadFile(st.Path())
	if err != nil {
		t.Fatal(err)
	}
	var file map[string]any
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	web, _ := file["web_settings"].(map[string]any)
	if web == nil {
		web = map[string]any{}
	}
	web["saved_prompts"] = json.RawMessage(want)
	file["web_settings"] = web
	if data, err = json.Marshal(file); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.Path(), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateSettings(SettingsPatch{SendDefault: ptr(store.WebSendQueue)}); err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveProject(project); err != nil {
		t.Fatal(err)
	}
	got := raw()
	var gotList, wantList any
	if err := json.Unmarshal(got["saved_prompts"], &gotList); err != nil {
		t.Fatalf("saved_prompts = %s: %v", got["saved_prompts"], err)
	}
	_ = json.Unmarshal([]byte(want), &wantList)
	if !reflect.DeepEqual(gotList, wantList) || string(got["send_default"]) != `"queue"` {
		t.Fatalf("web_settings after a save = %v", got)
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

// GET /api/routines lists every Project's routines, oldest first, behind
// the sign-in like the per-Project list.
func TestAllRoutinesListsEveryProject(t *testing.T) {
	m, prov, _, a := newRoutineManager(t)
	srv, err := NewServer(ServerConfig{Manager: m, Token: testToken, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	ts := &testServer{srv: srv, m: m, prov: prov}
	b := addProject(t, ts.m, t.TempDir())
	first := mustRoutine(t, ts.m, a, hourly("First"))
	second := mustRoutine(t, ts.m, b, hourly("Second"))
	if w := ts.do(http.MethodGet, "/api/routines", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("signed out = %d", w.Code)
	}
	w := ts.do(http.MethodGet, "/api/routines", "", withCookie(ts))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	var got struct{ Routines []Routine }
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(got.Routines))
	for _, r := range got.Routines {
		ids = append(ids, r.ProjectID+"/"+r.ID)
	}
	if want := []string{a + "/" + first.ID, b + "/" + second.ID}; !slices.Equal(ids, want) {
		t.Fatalf("listed = %v, want %v", ids, want)
	}
}
