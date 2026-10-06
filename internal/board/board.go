// Package board is the planner's store and rules (ADR 0005): epics, stories
// and subtasks for each Project, kept in one SQLite database through
// modernc.org/sqlite.
//
// Every rule in ADR 0005 §1–§9 is enforced here, inside the transaction of
// the write it guards: the actor table, derived container status, scope and
// caps, holds and their single release path, requests, split, cancel,
// cascade, restore, the expiry sweep and purge. The package knows nothing
// about HTTP, Copilot, the session store or git: evidence, HEAD and working
// tree state are supplied by the caller.
//
// "Task" always means a uam conversation. The leaf card kind is subtask.
package board

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode"
)

// FileName is the planner database's file name, created beside the session
// store.
const FileName = "board.db"

// Kind is a card's place in the tree. Kinds rank epic < story < subtask, and
// a parent must outrank its child.
type Kind string

// The card kinds. Subtask is the leaf; epics and stories are containers.
const (
	KindEpic    Kind = "epic"
	KindStory   Kind = "story"
	KindSubtask Kind = "subtask"
)

func (k Kind) valid() bool { return k == KindEpic || k == KindStory || k == KindSubtask }

// level is the kind rank: a parent's level must be lower than its child's.
func (k Kind) level() int {
	switch k {
	case KindEpic:
		return 0
	case KindStory:
		return 1
	}
	return 2
}

// canHold reports whether a card of kind k may be the parent of kind child.
func (k Kind) canHold(child Kind) bool { return k.level() < child.level() }

// holdRefusal refuses a card of kind child under a card of kind k.
func (k Kind) holdRefusal(child Kind) error {
	article := func(k Kind) string {
		if k == KindEpic {
			return "an"
		}
		return "a"
	}
	return invalid("%s %s cannot hold %s %s", article(k), k, article(child), child)
}

// Status is a leaf's stored status, or a container's derived one.
type Status string

// The statuses. Planned means never launched; todo means released after an
// attempt, or marked ready by the owner.
const (
	StatusPlanned   Status = "planned"
	StatusTodo      Status = "todo"
	StatusDoing     Status = "doing"
	StatusDone      Status = "done"
	StatusCancelled Status = "cancelled"
)

func (s Status) terminal() bool { return s == StatusDone || s == StatusCancelled }

// The priority scale: 1 high, 2 medium, 3 low (the default).
const (
	PrioHigh    = 1
	PrioMedium  = 2
	PrioLow     = 3
	PrioDefault = PrioLow
)

// DefaultEffort is the estimate a card takes when none is given.
const DefaultEffort = "S"

// ExpiryWindow is how long an agent-created card stays unconfirmed before the
// sweep cancels it.
const ExpiryWindow = 14 * 24 * time.Hour

// Caps counted per Task (ADR 0005 §4, ADR 0006). Calls made by a Task's
// subagents count against the Task.
const (
	CapCreated      = 50  // live cards a Task created; deleted and expired ones free their place
	CapCreatedTotal = 200 // cards a Task may create in its lifetime, deleted and expired ones included; only purge frees a place
	CapUnconfirmed  = 10  // live unconfirmed children a Task may add to one container, the root included
	CapComments     = 20  // non-automatic comments a Task may add to one card
)

// Input bounds.
const (
	maxLineBytes     = 500
	maxTextBytes     = 64 << 10
	maxEvidenceBytes = 1 << 20
	maxListItems     = 100
)

// Check is one checklist item.
type Check struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

// Progress is a container's done ÷ non-cancelled confirmed leaves, plus the
// unconfirmed leaves shown as "+N proposed".
type Progress struct {
	Done     int
	Total    int
	Proposed int
}

// Card is one node on a board. Status and Progress are derived for
// containers; Progress is nil on subtasks.
type Card struct {
	ID           string
	Seq          int64
	ProjectID    string // "" is the read-only Unassigned list
	Kind         Kind
	ParentID     string // "" at the root
	Rank         int
	Title        string
	Desc         string
	WinCondition string
	Status       Status
	Progress     *Progress
	Prio         int
	Due          string
	Effort       string
	Labels       []string
	Checklist    []Check
	Blocked      bool
	BlockedBy    []string
	Blocks       []string
	// ExpiresAt is nil once the card is confirmed.
	ExpiresAt *time.Time
	HeldBy    string
	// WorkedBy is the Task of the subtask's latest attempt, which stays
	// after the attempt ends; "" when none started.
	WorkedBy  string
	PinnedSHA string
	// AcceptCmd is nil to inherit the Project default, "" for none.
	AcceptCmd *string
	Paths     []string
	CascadeID string
	CreatedBy string
	// Paused is "", PausedOwner or PausedUAM: nothing new starts at or under
	// the card (ADR 0006 §4.6). Only a card under an epic, or an approved
	// epic, is paused.
	Paused string
	// Run is an approved epic's run; nil on every other card.
	Run *Run
	// Lane is the git state of the subtask's latest attempt when that
	// attempt ran in a lane; nil otherwise.
	Lane            *Lane
	PendingRequests int
	Revision        int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
	MovedAt         time.Time
}

// Confirmed reports whether the owner has saved, launched, accepted or
// restored the card.
func (c Card) Confirmed() bool { return c.ExpiresAt == nil }

func (c Card) container() bool { return c.Kind != KindSubtask }

// ref renders the card's user-facing handle.
func (c Card) ref() string { return fmt.Sprintf("#%d", c.Seq) }

// Who paused a card: the owner's Pause, or uam when an attempt ended
// without landing.
const (
	PausedOwner = "owner"
	PausedUAM   = "uam"
)

// Comment authors.
const (
	AuthorOwner = "owner"
	AuthorUAM   = "uam"
)

// TaskAuthor is the comment author for a Task.
func TaskAuthor(taskID string) string { return "task:" + taskID }

// Comment is one comment on a card. Automatic comments are written by uam and
// are exempt from the caps.
type Comment struct {
	ID        int64
	CardID    string
	Author    string
	AgentID   string
	Body      string
	Automatic bool
	// Close marks the comment a card was finished with; a container's
	// roll-up is made of its children's close comments.
	Close     bool
	CreatedAt time.Time
}

// Baseline is the working tree state recorded when a hold starts. Blobs
// maps each dirty path to the blob name of its content then, "" when the
// path did not exist; a dirty path missing from it counts as changed since.
type Baseline struct {
	Head  string            `json:"head"`
	Dirty []string          `json:"dirty"`
	Blobs map[string]string `json:"blobs,omitempty"`
}

// Hold is one attempt at a subtask by a Task.
type Hold struct {
	ID        string
	CardID    string
	TaskID    string
	Attempt   int
	StartedAt time.Time
	Baseline  Baseline
	EndedAt   *time.Time
	EndReason ReleaseReason
	// Lane is the attempt's git state; zero for an attempt in the Project
	// directory.
	Lane Lane
	// WaitedOn lists the IDs of the done subtasks a lane attempt waited on
	// when it started (ADR 0006 §5.3).
	WaitedOn []string
}

// Lane is a lane attempt's git state (ADR 0006 §5.1). Branch, the attempt
// branch, marks the attempt as a lane's. LandedSHA is the landing intent
// while the attempt is open and the commit it landed as once it ended;
// RevertedSHA is the commit that reverted it.
type Lane struct {
	Branch      string
	LandedSHA   string
	RevertedSHA string
}

// ReleaseReason is why a hold ended. Every hold ends through ReleaseHold's
// single path with one of these.
type ReleaseReason string

// The release reasons (ADR 0005 §5).
const (
	ReleaseAccepted  ReleaseReason = "accepted"  // request accepted → done
	ReleaseDone      ReleaseReason = "done"      // owner marked the subtask done
	ReleaseRejected  ReleaseReason = "rejected"  // request rejected, holder not live → todo
	ReleaseSettled   ReleaseReason = "settled"   // Settle dialog released it → todo
	ReleaseEnded     ReleaseReason = "ended"     // Task archived or deleted → todo
	ReleaseOwner     ReleaseReason = "released"  // owner Release → todo
	ReleaseCancelled ReleaseReason = "cancelled" // owner cancel or cascade → cancelled
	ReleaseSplit     ReleaseReason = "split"     // the subtask became a story; the hold moved (holds before split needed a release)
	ReleaseAborted   ReleaseReason = "aborted"   // a lane start failed after its hold was written → todo
	ReleaseBlocked   ReleaseReason = "blocked"   // a blocked request accepted on a lane hold → todo
)

// Stage is a Task's lifecycle stage as Reconcile sees it. A Task absent from
// the map given to Reconcile has been deleted.
type Stage string

// The Task stages.
const (
	StageActive   Stage = "active"
	StageSettled  Stage = "settled"
	StageArchived Stage = "archived"
)

// Code classifies a refusal so callers can map it to their own errors.
type Code string

// The refusal codes. The first six are ADR 0005 §14's.
const (
	CodeGuardOpenItems Code = "guard_open_items"
	CodeGuardBlockers  Code = "guard_blockers"
	CodeGuardBlocked   Code = "guard_blocked"
	CodeNotHeld        Code = "not_held"
	CodeReadOnly       Code = "read_only"
	CodeInvalid        Code = "invalid"
	CodeNotFound       Code = "not_found"
	CodeForbidden      Code = "forbidden"
	CodeLimit          Code = "limit"
	CodeDuplicate      Code = "duplicate"
	// CodeUnconfirmed refuses starting work while the subtask or an
	// ancestor is unconfirmed; Refs lists them.
	CodeUnconfirmed Code = "unconfirmed"
	// CodeInProgress refuses changing the plan of a started subtask
	// (lock.go) until the owner releases it.
	CodeInProgress Code = "in_progress"
	// CodeRunOwned refuses a manual start or a per-card confirmation under
	// an approved epic (ADR 0006 §6.2): uam runs it, and the owner approves
	// from the epic.
	CodeRunOwned Code = "run_owned"
	// CodeStale refuses an approval whose listed cards changed since the
	// dialog showed them; Refs lists them.
	CodeStale Code = "stale"
	// CodeNotReady refuses a run start of a subtask that is not ready, or
	// that no lane slot is free for (ADR 0006 §4.1); Refs lists what holds
	// it back.
	CodeNotReady Code = "not_ready"
	// CodeLanding refuses a write that would change the status or end the
	// hold of a subtask whose landing is under way (ADR 0006 §4.4).
	CodeLanding Code = "landing"
	// CodeRevertRunning refuses a revert while an attempt that started on
	// top of what it reverts still runs (ADR 0006 §5.7); Refs lists them.
	CodeRevertRunning Code = "revert_running"
	// The acceptance refusals (ADR 0005 §6), raised by the caller that runs
	// acceptance: the Project's runner stayed busy past the timeout, or the
	// command exited non-zero.
	CodeAcceptanceBusy   Code = "acceptance_busy"
	CodeAcceptanceFailed Code = "acceptance_failed"
)

// Error is a refusal from a board rule. Refs lists the cards or items the
// refusal is about, such as open checklist items or open blockers.
type Error struct {
	Code    Code
	Message string
	Refs    []string
}

func (e *Error) Error() string { return e.Message }

// Is matches another *Error with the same code, so errors.Is(err,
// ErrNotFound) works.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code && t.Message == ""
}

// ErrNotFound matches, with errors.Is, every not-found refusal.
var ErrNotFound = &Error{Code: CodeNotFound}

// CodeOf returns err's refusal code, or "" when err is not a refusal.
func CodeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func refuse(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

func invalid(format string, args ...any) *Error { return refuse(CodeInvalid, format, args...) }

// dueLayout is the due date's format.
const dueLayout = "2006-01-02"

// checkLine refuses a single-line field that is too long or holds a line
// break.
func checkLine(name, v string) error {
	if strings.ContainsAny(v, "\r\n") {
		return invalid("%s must not contain line breaks", name)
	}
	if len(v) > maxLineBytes {
		return invalid("%s exceeds %d bytes", name, maxLineBytes)
	}
	return nil
}

// validateFields checks the card fields every writer may set.
func validateFields(c Card) error {
	if strings.TrimSpace(c.Title) == "" {
		return invalid("title must not be empty")
	}
	for _, f := range []struct{ name, v string }{
		{"title", c.Title}, {"win condition", c.WinCondition}, {"due", c.Due}, {"effort", c.Effort},
	} {
		if err := checkLine(f.name, f.v); err != nil {
			return err
		}
	}
	if len(c.Desc) > maxTextBytes {
		return invalid("description exceeds %d bytes", maxTextBytes)
	}
	if c.Prio < PrioHigh || c.Prio > PrioLow {
		return invalid("invalid priority %d (want 1 high, 2 medium or 3 low)", c.Prio)
	}
	if c.Due != "" {
		if _, err := time.Parse(dueLayout, c.Due); err != nil || len(c.Due) != len(dueLayout) {
			return invalid("invalid due date %q (want YYYY-MM-DD)", c.Due)
		}
	}
	switch c.Effort {
	case "S", "M", "L":
	default:
		return invalid("invalid effort %q (want S, M or L)", c.Effort)
	}
	if len(c.Labels) > maxListItems || len(c.Checklist) > maxListItems {
		return invalid("labels and checklist are limited to %d items", maxListItems)
	}
	for _, label := range c.Labels {
		switch {
		case label == "":
			return invalid("label must not be empty")
		case strings.IndexFunc(label, unicode.IsSpace) >= 0:
			return invalid("invalid label %q: must not contain whitespace", label)
		case label[0] == '#':
			return invalid("invalid label %q: must not start with '#'", label)
		}
		if err := checkLine("label", label); err != nil {
			return err
		}
	}
	for _, item := range c.Checklist {
		if strings.TrimSpace(item.Text) == "" {
			return invalid("checklist text must not be empty")
		}
		if err := checkLine("checklist text", item.Text); err != nil {
			return err
		}
	}
	return nil
}

// validateOwnerFields checks the owner-only fields.
func validateOwnerFields(c Card) error {
	if c.AcceptCmd != nil && len(*c.AcceptCmd) > maxTextBytes {
		return invalid("acceptance command exceeds %d bytes", maxTextBytes)
	}
	if len(c.Paths) > maxListItems {
		return invalid("paths are limited to %d globs", maxListItems)
	}
	for _, glob := range c.Paths {
		if strings.TrimSpace(glob) == "" {
			return invalid("path glob must not be empty")
		}
		if err := checkLine("path glob", glob); err != nil {
			return err
		}
		if _, err := path.Match(glob, ""); err != nil {
			return invalid("invalid path glob %q", glob)
		}
	}
	return nil
}

// normalTitle is the form the duplicate check compares: case-folded, with
// runs of whitespace collapsed.
func normalTitle(title string) string {
	return strings.Join(strings.Fields(strings.ToLower(title)), " ")
}

// checkComment refuses an empty or oversized comment body.
func checkComment(body string) (string, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", invalid("comment must not be empty")
	}
	if len(body) > maxTextBytes {
		return "", invalid("comment exceeds %d bytes", maxTextBytes)
	}
	return body, nil
}
