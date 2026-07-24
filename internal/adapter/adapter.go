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
	Active    State = "Active"
	Completed State = "Completed"
	Failed    State = "Failed"
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
	SessionName  string
	// ProviderSessionID is the agent CLI's own session identifier, recorded
	// when the provider lets uam seed or learn it (e.g. claude --session-id).
	// It upgrades resume from "most recent conversation in this cwd" to an
	// exact-session resume.
	ProviderSessionID string
	State             State
	ProcAlive         ProcLiveness
	LastChange        time.Time
	CreatedAt         time.Time
	PR                *PRRef
	Pinned            bool
	Group             string
	SortIndex         int
	// ExitCode is the agent process's exit status from its most recent close
	// (-1 when it died on a signal), recorded by the session host. Nil while
	// the session is live or when no exit has been observed.
	ExitCode *int
	// Closed mirrors store.StatusClosedByUser: explicit UAM stop/restart reason
	// metadata retained for compatibility. Dashboard lifecycle grouping uses
	// ProcAlive, so every exited process is STOPPED regardless of this flag.
	Closed bool
}

type PeekResult struct {
	TailText string
}

type AttachProfileSnapshot struct {
	Selected      string
	Effective     string
	Mouse         string
	ControlPrefix string
	BackDetach    bool
}

type AttachSpec struct {
	Argv    []string
	Profile AttachProfileSnapshot
}

type ResumeRequest struct {
	ID              string
	Name            string
	CommandAlias    string
	ScrollbackLines int
	Prompt          string
	Cwd             string
	Mode            string
	SessionName     string
	// ProviderSessionID is the persisted provider-side session id, when one
	// was recorded at dispatch; providers that support exact resume use it
	// instead of their "most recent" heuristic.
	ProviderSessionID string
	CreatedAt         time.Time
	// ExecutablePath is transient launch metadata populated by Agent only
	// after alias validation and PATH resolution. Preparation hooks may probe
	// it; it is never persisted.
	ExecutablePath string
}

// LaunchPreparation is provider-owned launch metadata computed after the
// canonical backend identity and cwd are known, but before any session is
// created. Slices and maps are copied by Agent before use.
type LaunchPreparation struct {
	Command           []string
	ExtraArgs         []string
	Env               map[string]string
	ProviderSessionID string
}

type PrepareLaunchFunc func(ctx Context, req ResumeRequest, activity, sessionName, cwd string) (LaunchPreparation, error)

type ResumeKind string

const (
	ResumeExact       ResumeKind = "exact"
	ResumeHeuristic   ResumeKind = "heuristic"
	ResumeUnsupported ResumeKind = "unsupported"
)

type ResumeKindAdapter interface {
	ResumeKind(ResumeRequest) ResumeKind
}

type ResumableAdapter interface {
	Resume(ctx Context, req ResumeRequest) (Session, error)
}

// HasSessionAdapter reports whether the agent's underlying session for id is
// still live. Optional: Service.Stop probes it after a failed kill to avoid
// deleting/flagging a record whose process is still running (F04). Agent
// implements it for free, so every provider inherits it.
type HasSessionAdapter interface {
	HasSession(ctx Context, id string) bool
}

type DispatchRequest struct {
	Prompt          string
	Cwd             string
	Mode            string
	Name            string
	CommandAlias    string
	ScrollbackLines int
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
