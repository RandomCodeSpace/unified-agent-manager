package adapter

import (
	"context"
	"time"
)

type Context = context.Context

// State is the lifecycle bucket for a managed session. We deliberately keep
// only two values: anything richer (working / needs-input / completed)
// requires text-scraping the pane and produced more false positives than
// real signal. The pane content drives the activity summary line instead;
// the State here is grounded in the pane PID being alive or not.
type State string

const (
	Active State = "Active"
	Failed State = "Failed"
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
	ID           string
	AgentType    string
	CommandAlias string
	DisplayName  string
	Prompt       string
	Cwd          string
	TmuxSession  string
	State        State
	ProcAlive    ProcLiveness
	LastChange   time.Time
	CreatedAt    time.Time
	PR           *PRRef
	Pinned       bool
	Group        string
	SortIndex    int
	// Closed mirrors store.StatusClosedByUser: true when the user retired
	// this session through uam (`uam stop`, exit-in-session via the tmux
	// hook, or an external `tmux kill-session`). False otherwise — including
	// for dead-pane sessions left over from a reboot, which remain in the
	// Active group and resume on attach.
	Closed bool
}

type PeekResult struct {
	TailText string
}

type AttachSpec struct{ Argv []string }

type ResumeRequest struct {
	ID           string
	Name         string
	CommandAlias string
	Prompt       string
	Cwd          string
	Mode         string
	TmuxSession  string
	CreatedAt    time.Time
}

type ResumableAdapter interface {
	Resume(ctx Context, req ResumeRequest) (Session, error)
}

// HasSessionAdapter reports whether the agent's underlying session for id is
// still live. Optional: Service.Stop probes it after a failed kill to avoid
// deleting/flagging a record whose pane is still running (F04). TmuxAgent
// implements it for free, so all tmux-backed providers inherit it.
type HasSessionAdapter interface {
	HasSession(ctx Context, id string) bool
}

type DispatchRequest struct {
	Prompt       string
	Cwd          string
	Mode         string
	Name         string
	CommandAlias string
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
}
