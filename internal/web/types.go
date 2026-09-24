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
}

// Project is a directory the user added; its Tasks are web sessions whose
// project_id is ID.
type Project struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Dir       string    `json:"dir"`
	CreatedAt time.Time `json:"created_at"`
	// Defaults are omitted when the Project has none.
	Defaults TaskDefaults `json:"defaults,omitzero"`
	// Branch is the branch checked out in Dir's git work tree, read from git
	// and never stored. It is empty when Dir is not in a work tree, HEAD is
	// detached, or git cannot tell.
	Branch string `json:"branch,omitempty"`
}

// TaskDefaults are the settings a Project's new Tasks start with. The
// browser resolves them against the live models when it creates a Task.
// ContextSize is "default" unless a tier is chosen; Mode is safe or yolo.
type TaskDefaults struct {
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	Effort      string `json:"effort"`
	ContextSize string `json:"context_size"`
	Mode        string `json:"mode"`
}

// Meta is the /api/meta response.
type Meta struct {
	Version        string         `json:"version"`
	Providers      []ProviderInfo `json:"providers"`
	RecentWorkdirs []string       `json:"recent_workdirs"`
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
	Mode string `json:"mode"`
	// Stage is omitted for an active Task, otherwise StageSettled or
	// StageArchived; SettledAt and ArchivedAt say when.
	Stage      string    `json:"stage,omitempty"`
	SettledAt  time.Time `json:"settled_at,omitzero"`
	ArchivedAt time.Time `json:"archived_at,omitzero"`
}

// SessionDetail is a summary plus the retained main-agent transcript,
// interactions and subagents.
type SessionDetail struct {
	SessionSummary
	Items            []agentapi.Item        `json:"items"`
	Interactions     []agentapi.Interaction `json:"interactions"`
	Subagents        []agentapi.Subagent    `json:"subagents"`
	HistoryTruncated bool                   `json:"history_truncated"`
	LastSubmission   *Submission            `json:"last_submission"`
	// Queue holds the prompts waiting for the running turn, oldest first.
	Queue []QueuedPrompt `json:"queue"`
	// QueuePaused is set while the queue waits for the user to resume or
	// clear it; it is never set with an empty queue.
	QueuePaused bool `json:"queue_paused"`
}

// QueuedPrompt is one prompt in a Task's queue.
type QueuedPrompt struct {
	RequestID string    `json:"request_id"`
	Text      string    `json:"text"`
	QueuedAt  time.Time `json:"queued_at"`
	// Files are the project paths the prompt references; they are checked
	// again when it is sent.
	Files []string `json:"files,omitempty"`
}

// PromptRequest is the POST /api/sessions/{id}/prompt body.
type PromptRequest struct {
	Text      string `json:"text"`
	RequestID string `json:"request_id"`
	Mode      string `json:"mode"`
	// Files are project paths relative to the Task's directory, sent as
	// structured references. A steer takes none.
	Files []string `json:"files"`
}

// CommandRequest is the POST /api/sessions/{id}/command body. Name is a
// command from GET /api/sessions/{id}/commands, without the slash.
type CommandRequest struct {
	RequestID string   `json:"request_id"`
	Name      string   `json:"name"`
	Arguments string   `json:"arguments"`
	Files     []string `json:"files"`
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
	RequestID string    `json:"request_id"`
	Status    string    `json:"status"`
	Error     string    `json:"error"`
	Time      time.Time `json:"time"`
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
