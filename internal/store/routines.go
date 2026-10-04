package store

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// Routine schedule kinds: daily at a time, Monday to Friday at a time, every
// N hours, or weekly on one day at a time. Times are the service's local time.
const (
	ScheduleDaily    = "daily"
	ScheduleWeekdays = "weekdays"
	ScheduleHours    = "hours"
	ScheduleWeekly   = "weekly"
)

// Routine bounds: every N hours takes 1 to MaxRoutineHours; a day allows 1 to
// MaxRoutineRunsPerDay runs, and a run 1 to MaxRoutineMinutes minutes.
// MaxRoutineRuns is how many runs a routine's history keeps, newest last.
const (
	MaxRoutineHours      = 24
	MaxRoutineRunsPerDay = 100
	MaxRoutineMinutes    = 720
	MaxRoutineRuns       = 100
)

// RoutineSchedule is when a routine runs. Time is "HH:MM" (24-hour) for every
// kind but hours; Hours is set only for hours, and Weekday (0 Sunday to 6
// Saturday) only for weekly. Weekday is always written: 0 is Sunday.
type RoutineSchedule struct {
	Kind    string `json:"kind"`
	Time    string `json:"time,omitempty"`
	Hours   int    `json:"hours,omitempty"`
	Weekday int    `json:"weekday"`
}

// Clock returns the schedule's hour and minute.
func (s RoutineSchedule) Clock() (hour, minute int, err error) {
	t, err := time.Parse("15:04", s.Time)
	if err != nil || len(s.Time) != 5 {
		return 0, 0, fmt.Errorf("time must be HH:MM, such as 09:00")
	}
	return t.Hour(), t.Minute(), nil
}

// Validate reports why s is not a schedule a routine can have.
func (s RoutineSchedule) Validate() error {
	switch s.Kind {
	case ScheduleHours:
		if s.Hours < 1 || s.Hours > MaxRoutineHours {
			return fmt.Errorf("every N hours takes 1 to %d hours", MaxRoutineHours)
		}
		if s.Time != "" || s.Weekday != 0 {
			return fmt.Errorf("every N hours takes no time or day")
		}
		return nil
	case ScheduleDaily, ScheduleWeekdays, ScheduleWeekly:
	default:
		return fmt.Errorf("schedule must be daily, weekdays, hours or weekly")
	}
	if _, _, err := s.Clock(); err != nil {
		return err
	}
	if s.Hours != 0 {
		return fmt.Errorf("only every N hours takes hours")
	}
	if s.Weekday < 0 || s.Weekday > 6 || (s.Kind != ScheduleWeekly && s.Weekday != 0) {
		return fmt.Errorf("weekday must be 0 (Sunday) to 6 (Saturday), and only for weekly")
	}
	return nil
}

// WebRoutineRun is one firing of a routine: when, what started it, the Task it
// created and how it ended. Outcome is running until the run ends; Reason
// explains a skip or a failure.
type WebRoutineRun struct {
	ID      string    `json:"id"`
	At      time.Time `json:"at"`
	Trigger string    `json:"trigger"`
	TaskID  string    `json:"task_id,omitempty"`
	Outcome string    `json:"outcome"`
	Reason  string    `json:"reason,omitempty"`
	EndedAt time.Time `json:"ended_at,omitzero"`
}

// WebRoutine is recurring work in a Project: on its schedule, the web service
// starts a Task with Prompt on Model. NextRun is the next due firing, zero
// while paused; Runs is the newest MaxRoutineRuns firings, oldest first.
// Autopilot turns autopilot on in each run's Task: it keeps working until
// the agent calls task_complete or the run's time limit stops it.
type WebRoutine struct {
	ID            string          `json:"id"`
	ProjectID     string          `json:"project_id"`
	Name          string          `json:"name"`
	Prompt        string          `json:"prompt"`
	Provider      string          `json:"provider"`
	Model         string          `json:"model"`
	Schedule      RoutineSchedule `json:"schedule"`
	Enabled       bool            `json:"enabled"`
	Mode          Mode            `json:"mode"`
	Autopilot     bool            `json:"autopilot,omitempty"`
	MaxRunsPerDay int             `json:"max_runs_per_day"`
	MaxMinutes    int             `json:"max_minutes"`
	CreatedAt     time.Time       `json:"created_at"`
	NextRun       time.Time       `json:"next_run,omitzero"`
	Runs          []WebRoutineRun `json:"runs,omitempty"`

	unknown map[string]json.RawMessage
}

type webRoutineAlias WebRoutine

var knownWebRoutineFields = map[string]struct{}{
	"id": {}, "project_id": {}, "name": {}, "prompt": {}, "provider": {}, "model": {}, "schedule": {}, "enabled": {},
	"mode": {}, "autopilot": {}, "max_runs_per_day": {}, "max_minutes": {}, "created_at": {}, "next_run": {}, "runs": {},
}

func (r WebRoutine) MarshalJSON() ([]byte, error) {
	return marshalUnknownJSON(webRoutineAlias(r), r.unknown, knownWebRoutineFields)
}

func (r *WebRoutine) UnmarshalJSON(data []byte) error {
	var alias webRoutineAlias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	*r = WebRoutine(alias)
	unknown, err := decodeUnknownJSON(data, knownWebRoutineFields)
	if err != nil {
		return err
	}
	r.unknown = unknown
	return nil
}

// dropInvalidRoutines removes stored routines a hand edit or a bug could
// have broken: a bad ID, a missing Project, an unusable schedule, mode or
// limit. Its Project is looked up in cfg, so a routine never outlives one.
// Runs beyond MaxRoutineRuns are cut, oldest first.
func dropInvalidRoutines(cfg *Config) {
	for key, r := range cfg.WebRoutines {
		reason := ""
		_, project := cfg.WebProjects[r.ProjectID]
		switch {
		case r.ID == "" || r.ID != key || isUnsafeArgv(r.ID):
			reason = "invalid id"
		case !project:
			reason = "no such project"
		case r.Schedule.Validate() != nil:
			reason = "invalid schedule"
		case r.Mode != ModeSafe && r.Mode != ModeYolo:
			reason = "invalid mode"
		case r.MaxRunsPerDay < 1 || r.MaxRunsPerDay > MaxRoutineRunsPerDay || r.MaxMinutes < 1 || r.MaxMinutes > MaxRoutineMinutes:
			reason = "invalid limits"
		case r.Provider == "" || hasControlChar(r.Provider) || len(r.Provider) > maxWebModelBytes || hasControlChar(r.Model) || len(r.Model) > maxWebModelBytes:
			reason = "invalid model"
		}
		if reason != "" {
			log.Warn("dropping invalid routine", "key", key, "reason", reason)
			delete(cfg.WebRoutines, key)
			continue
		}
		if n := len(r.Runs); n > MaxRoutineRuns {
			r.Runs = r.Runs[n-MaxRoutineRuns:]
			cfg.WebRoutines[key] = r
		}
	}
	if len(cfg.WebRoutines) == 0 {
		cfg.WebRoutines = nil
	}
}
