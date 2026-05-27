package adapter

import (
	"context"
	"time"
)

type Context = context.Context

type State string

const (
	NeedsInput     State = "NeedsInput"
	Working        State = "Working"
	Completed      State = "Completed"
	ReadyForReview State = "ReadyForReview"
	Failed         State = "Failed"
	Idle           State = "Idle"
)

type ProcLiveness string

const (
	Alive  ProcLiveness = "Alive"
	Exited ProcLiveness = "Exited"
)

type PRStatus string

const (
	PRNone   PRStatus = "None"
	PROpen   PRStatus = "Open"
	PRMerged PRStatus = "Merged"
	PRClosed PRStatus = "Closed"
	PRDraft  PRStatus = "Draft"
)

type PRRef struct {
	URL    string
	Owner  string
	Repo   string
	Number int
	Status PRStatus
}

type Session struct {
	ID          string
	AgentType   string
	DisplayName string
	Prompt      string
	Cwd         string
	TmuxSession string
	State       State
	ProcAlive   ProcLiveness
	Activity    string
	LastChange  time.Time
	CreatedAt   time.Time
	PR          *PRRef
	Pinned      bool
	Group       string
	SortIndex   int
	// Closed mirrors store.StatusClosedByUser: true when the user retired
	// this session through uam (`uam stop`, exit-in-session via the tmux
	// hook, or an external `tmux kill-session`). False otherwise — including
	// for dead-pane sessions left over from a reboot, which remain in the
	// Active group and resume on attach.
	Closed bool
}

type PeekResult struct {
	TailText      string
	Summary       string
	AwaitingInput bool
	State         State
}

type AttachSpec struct{ Argv []string }

type ResumeRequest struct {
	ID          string
	Name        string
	Prompt      string
	Cwd         string
	Mode        string
	TmuxSession string
	CreatedAt   time.Time
}

type ResumableAdapter interface {
	Resume(ctx Context, req ResumeRequest) (Session, error)
}

type SessionEvent struct {
	SessionID string
	Kind      string
}

type DispatchRequest struct {
	Prompt string
	Cwd    string
	Mode   string
	Name   string
}

type AgentAdapter interface {
	Name() string
	DisplayName() string
	Available() (bool, string)
	Dispatch(ctx Context, req DispatchRequest) (Session, error)
	List(ctx Context) ([]Session, error)
	Peek(ctx Context, id string) (PeekResult, error)
	Reply(ctx Context, id, text string) error
	Attach(id string) (AttachSpec, error)
	Stop(ctx Context, id string) error
	Rename(ctx Context, id, newName string) error
	Subscribe(ctx Context) (<-chan SessionEvent, error)
}
