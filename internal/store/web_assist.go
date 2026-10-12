package store

// WebSuggestions are the replies suggested to the owner once the Task's
// main transcript ended with the item ItemID; Replies may be empty when
// none came. They are kept so the same state is never asked for again.
type WebSuggestions struct {
	ItemID  string   `json:"item_id"`
	Replies []string `json:"replies"`
}
