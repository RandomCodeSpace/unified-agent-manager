package agentapi

import "time"

// ScheduleSnapshot describes the open conversation's active native schedules.
// Unknown is never an authoritative empty list. Truncated retains a partial
// catalog; entries contain display metadata, never the scheduled prompt.
type ScheduleSnapshot struct {
	Supported bool            `json:"supported"`
	Known     bool            `json:"known"`
	Entries   []ScheduleEntry `json:"entries"`
	Truncated bool            `json:"truncated,omitempty"`
	Reason    string          `json:"reason,omitempty"`
}

// ScheduleEntry is provider-owned timing. IDs are strings to preserve native
// int64 IDs exactly in a browser; UAM does not calculate or arm the next run.
type ScheduleEntry struct {
	ID         string    `json:"id"`
	Recurring  bool      `json:"recurring"`
	SelfPaced  bool      `json:"self_paced,omitempty"`
	IntervalMs int64     `json:"interval_ms,omitempty"`
	Cron       string    `json:"cron,omitempty"`
	Timezone   string    `json:"timezone,omitempty"`
	NextRunAt  time.Time `json:"next_run_at,omitzero"`
}
