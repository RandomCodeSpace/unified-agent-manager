package store

import "time"

// ForkLineage also makes a successfully registered fork request idempotent
// after a restart, even when the target's selected model later changes.
type ForkLineage struct {
	SourceTaskID string `json:"source_task_id"`
	UserItemID   string `json:"user_item_id"`
	UserEventID  string `json:"user_event_id"`
	ToEventID    string `json:"to_event_id,omitempty"`
	RequestID    string `json:"request_id"`
	Model        string `json:"model"`
}

// WebFork is the small durable reservation of a UAM target identity. Empty
// ProviderSessionID means the native result is unknown; it must never cause
// another fork RPC. A returned exact identity allows registration to retry.
type WebFork struct {
	ID                   string      `json:"id"`
	Provider             string      `json:"provider"`
	SourceConversationID string      `json:"source_conversation_id"`
	ProviderSessionID    string      `json:"provider_session_id,omitempty"`
	ProjectID            string      `json:"project_id"`
	Workdir              string      `json:"workdir"`
	Effort               string      `json:"effort,omitempty"`
	ContextSize          string      `json:"context_size"`
	Mode                 Mode        `json:"mode"`
	CreatedAt            time.Time   `json:"created_at"`
	TailEventID          string      `json:"tail_event_id"`
	Lineage              ForkLineage `json:"lineage"`
}
