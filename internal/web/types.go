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

// Submission outcomes.
const (
	SubmissionAccepted  = "accepted"
	SubmissionRejected  = "rejected"
	SubmissionUncertain = "uncertain"
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
}

// Meta is the /api/meta response.
type Meta struct {
	Version        string         `json:"version"`
	Providers      []ProviderInfo `json:"providers"`
	RecentWorkdirs []string       `json:"recent_workdirs"`
}

// SessionSummary is one web session as listed.
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
}

// SessionDetail is a summary plus the retained transcript and interactions.
type SessionDetail struct {
	SessionSummary
	Items            []agentapi.Item        `json:"items"`
	Interactions     []agentapi.Interaction `json:"interactions"`
	HistoryTruncated bool                   `json:"history_truncated"`
	LastSubmission   *Submission            `json:"last_submission"`
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
