package store

import "time"

// WebSuggestions are the replies suggested to the owner once the Task's
// main transcript ended with the item ItemID; Replies may be empty when
// none came. They are kept so the same state is never asked for again.
type WebSuggestions struct {
	ItemID  string   `json:"item_id"`
	Replies []string `json:"replies"`
}

// WebDone is the done check of the Task's main transcript ending with the
// item ItemID: Verdict is done, not_done, unsure or failed (the call
// failed), By is model or rule, and Line is the final reply's line the
// model cited for done. It is kept so the same state is never asked for
// again.
type WebDone struct {
	ItemID  string    `json:"item_id"`
	Verdict string    `json:"verdict"`
	By      string    `json:"by"`
	Line    string    `json:"line,omitempty"`
	At      time.Time `json:"at"`
}
