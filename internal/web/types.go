// Package web is the `uam web` service: a detached per-user process that owns
// web-surface managed sessions, drives providers through agentapi, and serves
// the browser interface described in docs/adr/0004-web-interface.md.
//
// Provider conversations and turns belong to the Manager, never to an HTTP
// request or an event-stream connection. Browsers only observe and submit.
package web

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// Session states reported to browsers.
const (
	StateIdle               = "idle"
	StateStarting           = "starting"
	StateWorking            = "working"
	StateAwaitingPermission = "awaiting_permission"
	StateAwaitingAnswer     = "awaiting_answer"
	StateCompleted          = "completed"
	StateCancelled          = "cancelled"
	StateFailed             = "failed"
	StateInterrupted        = "interrupted"
	StateClosed             = "closed"
)

// Submission outcomes. A queued prompt is "queued" until it is sent, then
// takes the send's outcome; one removed from the queue unsent is "cancelled".
const (
	SubmissionAccepted  = "accepted"
	SubmissionRejected  = "rejected"
	SubmissionUncertain = "uncertain"
	SubmissionQueued    = "queued"
	SubmissionCancelled = "cancelled"
)

// Task stages. A Task is active until the user settles or archives it;
// archived is final.
const (
	StageActive   = ""
	StageSettled  = "settled"
	StageArchived = "archived"
)

// Transcript states of SessionDetail.History. A Task whose conversation is
// not open has its recorded transcript read without opening it: loading
// until a "history" event carries it, unavailable when it could not be read.
const (
	HistoryLoaded      = "loaded"
	HistoryLoading     = "loading"
	HistoryUnavailable = "unavailable"
)

// Prompt modes. While a turn runs, send is refused, queue holds the prompt
// until the turn completes, and steer adds it to the running turn. While the
// Task is idle, all three send it.
const (
	ModeSend  = "send"
	ModeQueue = "queue"
	ModeSteer = "steer"
)

// Change scopes.
const (
	ScopeSession   = "session"
	ScopeWorkspace = "workspace"
)

// ProviderInfo describes one provider for the create form.
type ProviderInfo struct {
	Name         string                `json:"name"`
	DisplayName  string                `json:"display_name"`
	Available    bool                  `json:"available"`
	Reason       string                `json:"reason"`
	Capabilities agentapi.Capabilities `json:"capabilities"`
	// Models are the selectable models; empty means the provider default only.
	Models []agentapi.Model `json:"models"`
	// CheapestModel is the cheapest priced model not hidden in Settings: the
	// Utility model when Settings name none. Omitted when none is priced.
	CheapestModel string `json:"cheapest_model,omitempty"`
}

// Project is a directory the user added; its Tasks are web sessions whose
// project_id is ID.
type Project struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Dir       string    `json:"dir"`
	CreatedAt time.Time `json:"created_at"`
	Badge     Badge     `json:"badge"`
	// Branch is the branch checked out in Dir's git work tree, read from git
	// and never stored. It is empty when Dir is not in a work tree, HEAD is
	// detached, or git cannot tell.
	Branch string `json:"branch,omitempty"`
}

// TaskDefaults are the settings a new Task starts with (Settings). The
// browser resolves them against the live models when it creates a Task.
// ContextSize is "default" unless a tier is chosen; Mode is safe or yolo.
type TaskDefaults struct {
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	Effort      string `json:"effort"`
	ContextSize string `json:"context_size"`
	Mode        string `json:"mode"`
}

// Badge is a Project's badge: Text is two uppercase ASCII letters or digits,
// unique among Projects; Color is one of badgeColors.
type Badge struct {
	Text  string `json:"text"`
	Color string `json:"color"`
}

// Settings are the web interface's settings, shared by every browser.
type Settings struct {
	// SendDefault is what Enter does while a turn runs: steer or queue.
	SendDefault string `json:"send_default"`
	// HiddenModels lists, by provider, the model IDs the browser does not
	// offer, sorted; omitted when none is hidden. IDs the provider no longer
	// lists are kept. The service never refuses a hidden model.
	HiddenModels map[string][]string `json:"hidden_models,omitempty"`
	// TitleModel maps a provider to its Utility model, the model UAM uses for
	// its own small AI jobs such as titling new Tasks: a model ID, or
	// store.WebTitleModelNone when the provider keeps its own title. A
	// provider without an entry uses ProviderInfo.CheapestModel. Omitted when
	// no provider has an entry.
	TitleModel map[string]string `json:"title_model,omitempty"`
	// CustomModels are the OpenAI-compatible models the owner brought;
	// omitted when there are none. Their model IDs are name/model_id.
	CustomModels []CustomModel `json:"custom_models,omitempty"`
	// TaskDefaults are the settings a new Task starts with; omitted when
	// unset, and the browser then starts from the provider's own defaults.
	TaskDefaults TaskDefaults `json:"task_defaults,omitzero"`
}

// CustomModel is one custom model in Settings. APIKeyEnv only names the
// service environment variable holding the key; KeyPresent says whether it
// is set and non-empty there. No key value is ever sent.
type CustomModel struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
	BaseURL     string `json:"base_url"`
	ModelID     string `json:"model_id"`
	WireAPI     string `json:"wire_api,omitempty"`
	APIKeyEnv   string `json:"api_key_env"`
	KeyPresent  bool   `json:"key_present"`
}

// AccountUsage is the GET /api/usage response: the account quotas of every
// provider with the usage capability, from the last read that succeeded.
// Stale is set while the latest read failed; UpdatedAt is when the oldest of
// the shown quotas was read, omitted before any read succeeded.
type AccountUsage struct {
	Quotas    []Quota   `json:"quotas"`
	Stale     bool      `json:"stale"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

// Quota is one account quota. Entitlement is 0 when Unlimited; ResetAt is
// omitted unless the provider reported a time still in the future.
type Quota struct {
	Provider         string    `json:"provider"`
	Type             string    `json:"type"`
	Used             int64     `json:"used"`
	Entitlement      int64     `json:"entitlement"`
	Unlimited        bool      `json:"unlimited"`
	RemainingPercent float64   `json:"remaining_percent"`
	Overage          float64   `json:"overage"`
	ResetAt          time.Time `json:"reset_at,omitzero"`
}

// Meta is the /api/meta response.
type Meta struct {
	Version         string         `json:"version"`
	Providers       []ProviderInfo `json:"providers"`
	RecentWorkdirs  []string       `json:"recent_workdirs"`
	TempRoot        string         `json:"temp_root,omitempty"`
	TempRootAliases []string       `json:"temp_root_aliases,omitempty"`
}

// SessionSummary is one web session (a Task) as listed.
type SessionSummary struct {
	ID             string    `json:"id"`
	Provider       string    `json:"provider"`
	Name           string    `json:"name"`
	Workdir        string    `json:"workdir"`
	ConversationID string    `json:"conversation_id"`
	State          string    `json:"state"`
	StateDetail    string    `json:"state_detail"`
	Open           bool      `json:"open"`
	Pending        int       `json:"pending"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	// Capabilities come from the provider so the browser can hide controls
	// the provider does not really support.
	Capabilities agentapi.Capabilities `json:"capabilities"`
	ProjectID    string                `json:"project_id"`
	// Model is the selected model; empty means the provider default.
	Model       string            `json:"model"`
	Effort      string            `json:"effort"`
	ContextSize string            `json:"context_size"`
	Context     *agentapi.Context `json:"context,omitempty"`
	// Usage is the AI units the Task's conversation used, main agent and
	// subagents together, once the provider reports them; it is not
	// persisted and after a restart comes back only from provider history.
	Usage *agentapi.Usage `json:"usage,omitempty"`
	// Title is the provider-generated title. Name may be empty; browsers
	// display name || title || "New task".
	Title string `json:"title"`
	// LastModel is the model the provider reported for the latest turn that
	// reported one. It is not persisted.
	LastModel string `json:"last_model"`
	// SubagentsRunning counts subagents that have not ended.
	SubagentsRunning int `json:"subagents_running"`
	// Queued counts prompts waiting in the Task's queue.
	Queued int `json:"queued"`
	// Mode is safe or yolo. A yolo Task's permission requests are allowed
	// once without asking; questions still wait for the user.
	Mode      string                   `json:"mode"`
	Execution *agentapi.ExecutionState `json:"execution"`
	// Stage is omitted for an active Task, otherwise StageSettled or
	// StageArchived; SettledAt and ArchivedAt say when.
	Stage      string    `json:"stage,omitempty"`
	SettledAt  time.Time `json:"settled_at,omitzero"`
	ArchivedAt time.Time `json:"archived_at,omitzero"`
}

// SessionDetail is a summary plus the retained main-agent transcript,
// interactions and subagents.
type TurnTiming = store.TurnTiming

type SessionDetail struct {
	TurnTimings []TurnTiming `json:"turn_timings"`
	SessionSummary
	// Seq orders this snapshot against events on the same service.
	Seq              uint64                    `json:"seq"`
	Items            []agentapi.Item           `json:"items"`
	Interactions     []agentapi.Interaction    `json:"interactions"`
	Subagents        []agentapi.Subagent       `json:"subagents"`
	HistoryTruncated bool                      `json:"history_truncated"`
	LastSubmission   *Submission               `json:"last_submission"`
	BackgroundTasks  *agentapi.BackgroundTasks `json:"background_tasks,omitempty"`
	// Queue holds the prompts waiting for the running turn, oldest first.
	Queue []QueuedPrompt `json:"queue"`
	// QueuePaused is set while the queue waits for the user to resume or
	// clear it; it is never set with an empty queue.
	QueuePaused bool `json:"queue_paused"`
	// History says whether Items and Subagents hold the conversation's
	// recorded transcript (HistoryLoaded, HistoryLoading or
	// HistoryUnavailable); HistoryReason says why it is unavailable.
	History       string `json:"history"`
	HistoryReason string `json:"history_reason,omitempty"`
	// Present for paged clients, empty when all retained items are included.
	HistoryBefore *string `json:"history_before,omitempty"`
}

// PreviousConversation is a provider conversation recorded for a Project's
// directory that no Task is linked to. InUse is set while another client
// holds it open.
type PreviousConversation struct {
	Provider       string    `json:"provider"`
	ConversationID string    `json:"conversation_id"`
	Title          string    `json:"title"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	InUse          bool      `json:"in_use"`
}

// PromptSettings is the complete selection for one prompt's next turn.
type PromptSettings struct {
	Model       string `json:"model"`
	Effort      string `json:"effort"`
	ContextSize string `json:"context_size"`
}

// QueuedPrompt is one prompt in a Task's queue.
type QueuedPrompt struct {
	RequestID string    `json:"request_id"`
	Text      string    `json:"text"`
	QueuedAt  time.Time `json:"queued_at"`
	// Files are the project paths the prompt references; they are checked
	// again when it is sent.
	Files []string `json:"files,omitempty"`
	// Attachments are the uploads the prompt carries.
	Attachments []agentapi.Attachment `json:"attachments,omitempty"`
	Settings    PromptSettings        `json:"settings"`
}

// PromptRequest is the POST /api/sessions/{id}/prompt body.
type PromptRequest struct {
	Text      string `json:"text"`
	RequestID string `json:"request_id"`
	Mode      string `json:"mode"`
	// Files are project paths relative to the Task's directory, sent as
	// structured references.
	Files []string `json:"files"`
	// Attachments are IDs from POST /api/sessions/{id}/attachments.
	Attachments []string `json:"attachments"`
	// Omitted settings snapshot the Task's current selection. A running steer
	// may only use that same selection; new settings require a queued turn.
	Settings *PromptSettings `json:"settings,omitempty"`
}

// CommandRequest is the POST /api/sessions/{id}/command body. Name is a
// command from GET /api/sessions/{id}/commands, without the slash.
type CommandRequest struct {
	RequestID   string   `json:"request_id"`
	Name        string   `json:"name"`
	Arguments   string   `json:"arguments"`
	Files       []string `json:"files"`
	Attachments []string `json:"attachments"`
}

// SubagentDetail is one subagent and its retained transcript.
type SubagentDetail struct {
	// Seq is the SSE sequence captured with the transcript and metadata.
	Seq      uint64            `json:"seq"`
	Subagent agentapi.Subagent `json:"subagent"`
	Items    []agentapi.Item   `json:"items"`
}

// Submission is the recorded outcome of one prompt request.
type Submission struct {
	RequestID     string                  `json:"request_id"`
	Status        string                  `json:"status"`
	Error         string                  `json:"error"`
	Time          time.Time               `json:"time"`
	CommandResult *agentapi.CommandResult `json:"command_result,omitempty"`
}

// ChangedFile is one entry of a Changes listing.
type ChangedFile struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

// Changes lists changed files for one scope. Label states plainly what the
// scope includes.
type Changes struct {
	Scope     string        `json:"scope"`
	Label     string        `json:"label"`
	Supported bool          `json:"supported"`
	Reason    string        `json:"reason"`
	Files     []ChangedFile `json:"files"`
}

// Error is a failure with the HTTP status the server reports for it.
type Error struct {
	Status  int
	Message string
	// ProjectID names the existing Project when adding a directory that
	// already has one.
	ProjectID string
}

func (e *Error) Error() string { return e.Message }

func newError(status int, format string, args ...any) *Error {
	return &Error{Status: status, Message: fmt.Sprintf(format, args...)}
}

// errorStatus maps err to an HTTP status and message.
func errorStatus(err error) (int, string) {
	var webErr *Error
	if errors.As(err, &webErr) {
		return webErr.Status, webErr.Message
	}
	return http.StatusInternalServerError, "internal error"
}
