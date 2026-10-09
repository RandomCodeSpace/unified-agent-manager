package store

import (
	"encoding/json"
	"slices"
	"time"
)

// WebRewind is a Task's native rewind receipt. It is saved before the native
// request, which has no idempotency key, so a lost result or restart never
// repeats it. State is pending, applied, uncertain or done. Discarded lists
// only the Task's own turn timings' owner items in the discarded suffix.
type WebRewind struct {
	RequestID   string          `json:"request_id"`
	UserItemID  string          `json:"user_item_id"`
	UserEventID string          `json:"user_event_id"`
	Mode        string          `json:"mode"`
	State       string          `json:"state"`
	Discarded   []string        `json:"discarded,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

// Clone gives each holder its own lists.
func (r *WebRewind) Clone() *WebRewind {
	if r == nil {
		return nil
	}
	c := *r
	c.Discarded = slices.Clone(r.Discarded)
	c.Result = slices.Clone(r.Result)
	return &c
}
