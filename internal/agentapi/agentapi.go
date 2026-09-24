// Package agentapi is the provider-neutral contract between the uam web
// service and the structured provider integrations (the Copilot SDK and the
// OpenCode server). It carries only what the web interface needs; provider
// specifics stay inside the adapters.
//
// Lifetime rules every implementation must follow:
//
//   - A Conversation belongs to the web service, never to an HTTP request or a
//     browser connection. The context passed to a method bounds only that
//     call; turns keep running after the call returns.
//   - Events are delivered through the EventSink supplied at Open until Close
//     returns. Emit never blocks on browsers, so adapters may call it from
//     their event goroutines.
//   - Adapters never replay a prompt, never pick "the latest" conversation,
//     and never answer a pending interaction on the user's behalf. Only the
//     web service may, for permission requests of a Task in yolo mode.
package agentapi

import (
	"context"
	"errors"
	"time"
)

// Provider names match the uam agent names used in sessions.json.
const (
	ProviderCopilot  = "copilot"
	ProviderOpenCode = "opencode"
)

var (
	// ErrConversationNotFound reports that an exact conversation ID does not
	// exist in the provider's store. The caller must not create a replacement.
	ErrConversationNotFound = errors.New("provider conversation not found")
	// ErrInteractionGone reports that the provider no longer considers an
	// interaction pending (answered elsewhere, expired, or the turn ended).
	ErrInteractionGone = errors.New("interaction is no longer pending")
	// ErrUnsupported reports an operation the provider does not offer.
	ErrUnsupported = errors.New("operation not supported by this provider")
	// ErrClosed reports use of a Conversation after Close or after its
	// provider runtime exited.
	ErrClosed = errors.New("conversation is closed")
	// ErrSubmissionUncertain wraps a Send failure after which the provider may
	// or may not have accepted the prompt. The caller must surface the
	// uncertainty and must not resubmit automatically.
	ErrSubmissionUncertain = errors.New("prompt submission outcome is unknown")
	// ErrBusy reports that the provider rejected a prompt because a turn is
	// still running.
	ErrBusy = errors.New("a turn is already running")
)

// Capabilities advertises what an adapter really supports. The UI hides or
// disables controls for unsupported operations instead of pretending.
type Capabilities struct {
	Cancel      bool `json:"cancel"`
	Permissions bool `json:"permissions"`
	Questions   bool `json:"questions"`
	// SessionDiff is true when the provider records per-conversation file
	// changes (Conversation.Diff). Workspace Git diffs are separate.
	SessionDiff bool `json:"session_diff"`
	History     bool `json:"history"`
	// ContextSize is the per-Task context-tier exception to provider parity.
	ContextSize bool `json:"context_size"`
}

// Provider creates and reopens conversations for one provider runtime.
type Provider interface {
	// Name returns ProviderCopilot or ProviderOpenCode.
	Name() string
	// DisplayName returns a human label, e.g. "GitHub Copilot".
	DisplayName() string
	Capabilities() Capabilities
	// Check reports whether the installed runtime is present and compatible,
	// without starting a conversation. The error text is shown to the user.
	Check(ctx context.Context) error
	// Models returns the models the signed-in account can select. An empty
	// list or ErrUnsupported means only the provider default is offered. A
	// provider that lists models must support Conversation.SetModel.
	Models(ctx context.Context) ([]Model, error)
	// Open creates a conversation when req.ConversationID is empty, otherwise
	// reopens exactly that conversation (ErrConversationNotFound when it no
	// longer exists). ctx bounds the open call only.
	Open(ctx context.Context, req OpenRequest) (Conversation, error)
	// Shutdown closes every conversation and stops runtimes this provider
	// started. It never stops a runtime it did not start.
	Shutdown(ctx context.Context) error
}

// Model is one selectable model.
type Model struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Efforts      []string      `json:"efforts"`
	ContextSizes []ContextSize `json:"context_sizes"`
}

// ContextSize is a selectable provider tier and its prompt token budget.
type ContextSize struct {
	ID     string `json:"id"`
	Tokens int64  `json:"tokens"`
}

// Context is the provider's latest live usage report, not a token estimate.
type Context struct {
	Used  int64 `json:"used"`
	Limit int64 `json:"limit"`
}

// OpenRequest identifies the managed session and its project.
type OpenRequest struct {
	// SessionID is the uam managed-session ID (a UUID).
	SessionID string
	// ConversationID is the exact provider conversation ID, or "" to create.
	ConversationID string
	// Workdir is the absolute, canonical project directory.
	Workdir string
	// Title is the user-visible session name.
	Title string
	// Model is the model ID for a new conversation; "" means the provider
	// default. It is ignored on reopen, so reopening never changes the model.
	Model string
	// Effort and ContextSize apply only when creating a conversation.
	Effort      string
	ContextSize string
	// Events receives every event for this conversation until Close returns.
	Events EventSink
}

// EventSink receives adapter events. Implementations must not block.
type EventSink interface {
	Emit(Event)
}

// Conversation is one open provider conversation owned by the web service.
type Conversation interface {
	// ID returns the exact provider conversation ID to persist.
	ID() string
	// History returns the provider-recorded transcript and subagents of a
	// reopened conversation.
	History(ctx context.Context) (History, error)
	// Send submits one user prompt that starts a turn and returns once the
	// provider accepted or rejected it. The turn continues asynchronously and
	// is reported through events. Ambiguous failures wrap
	// ErrSubmissionUncertain. A provider that would otherwise fold a prompt
	// sent during a turn into that turn must run it after the turn instead.
	Send(ctx context.Context, prompt string) error
	// Steer adds prompt to the turn that is running, before the provider's
	// next model call, and returns once the provider accepted or rejected
	// it. The caller steers only while a turn runs; it never starts a turn.
	// An accepted steer cannot be withdrawn. When the provider uses it, its
	// user item carries Delivery DeliverySteer; when the turn ends without
	// the provider using it, the adapter emits an ItemNotice saying so.
	// Ambiguous failures wrap ErrSubmissionUncertain and are never resent.
	// ErrUnsupported means the provider cannot steer.
	Steer(ctx context.Context, prompt string) error
	// Cancel aborts the current turn. The conversation stays open.
	Cancel(ctx context.Context) error
	// Respond answers a pending interaction. It returns ErrInteractionGone
	// when the provider no longer considers the interaction pending.
	Respond(ctx context.Context, interactionID string, answer Answer) error
	// Diff returns provider-recorded file changes for this conversation, or
	// ErrUnsupported when Capabilities().SessionDiff is false.
	Diff(ctx context.Context) ([]FileDiff, error)
	// SetModel applies the complete selection from the next turn on. Empty
	// effort means Default; contextSize is default or a catalog tier ID.
	// The caller validates the selection and never switches during a turn.
	SetModel(ctx context.Context, model, effort, contextSize string) error
	// Close disconnects from the conversation without deleting it. Pending
	// interactions end without an answer being fabricated.
	Close(ctx context.Context) error
}

// History is a reopened conversation's provider-recorded state.
type History struct {
	// Items is the transcript, oldest first. Subagent items carry AgentID.
	Items []Item
	// Subagents are the subagent records the recorded events allow to
	// rebuild.
	Subagents []Subagent
}

// EventKind discriminates Event payloads.
type EventKind string

const (
	// EventItem upserts a transcript item; a later event with the same
	// AgentID and Item.ID replaces the earlier one (partial tool updates never
	// duplicate).
	EventItem EventKind = "item"
	// EventDelta appends streamed text to Item.ID, creating it when absent.
	EventDelta EventKind = "delta"
	// EventTurn reports a turn state transition.
	EventTurn EventKind = "turn"
	// EventInteraction upserts a permission request or question by ID.
	EventInteraction EventKind = "interaction"
	// EventExit reports that the conversation or its runtime became unusable
	// (process exit, event-stream failure). The conversation is then closed.
	EventExit EventKind = "exit"
	// EventTitle reports the provider-generated conversation title in
	// Event.Title.
	EventTitle EventKind = "title"
	// EventSubagent upserts a subagent record by Subagent.ID.
	EventSubagent EventKind = "subagent"
	// EventContext reports main-agent context usage; it is never persisted.
	EventContext EventKind = "context"
)

// Event is one adapter notification. Exactly one payload matches Kind.
type Event struct {
	Kind        EventKind
	Item        *Item
	Delta       *Delta
	Turn        *Turn
	Interaction *Interaction
	Subagent    *Subagent
	Context     *Context
	// Error is the sanitized reason for EventExit.
	Error string
	// Title is the untrusted provider title for EventTitle.
	Title string
}

// ItemKind classifies transcript entries.
type ItemKind string

const (
	ItemUser      ItemKind = "user"
	ItemAssistant ItemKind = "assistant"
	ItemReasoning ItemKind = "reasoning"
	ItemTool      ItemKind = "tool"
	// ItemNotice is provider-originated status text (errors, aborts, info).
	ItemNotice ItemKind = "notice"
)

// Item is one transcript entry. Text is untrusted provider output.
type Item struct {
	ID   string    `json:"id"`
	Kind ItemKind  `json:"kind"`
	Text string    `json:"text,omitempty"`
	Tool *ToolCall `json:"tool,omitempty"`
	Time time.Time `json:"time"`
	// AgentID is empty for the main agent, otherwise the exact subagent
	// instance ID from the provider.
	AgentID string `json:"agent_id,omitempty"`
	// Delivery is DeliverySteer on a user item that joined a running turn
	// as a steer, and empty otherwise.
	Delivery string `json:"delivery,omitempty"`
}

// DeliverySteer marks a user item that arrived as a steer.
const DeliverySteer = "steer"

// ToolStatus is the lifecycle of one tool call.
type ToolStatus string

const (
	ToolPending   ToolStatus = "pending"
	ToolRunning   ToolStatus = "running"
	ToolCompleted ToolStatus = "completed"
	ToolFailed    ToolStatus = "failed"
)

// ToolCall describes one tool invocation. Input and Output are display text
// (JSON or plain) that adapters truncate to a bounded size.
type ToolCall struct {
	Name   string     `json:"name"`
	Title  string     `json:"title,omitempty"`
	Status ToolStatus `json:"status"`
	Input  string     `json:"input,omitempty"`
	Output string     `json:"output,omitempty"`
}

// Delta appends Text to the item ItemID of kind Kind.
type Delta struct {
	ItemID string   `json:"item_id"`
	Kind   ItemKind `json:"kind"`
	Text   string   `json:"text"`
	// AgentID is the item's agent, as in Item.AgentID.
	AgentID string `json:"agent_id,omitempty"`
}

// TurnState is the adapter-reported state of the current turn. Waiting for
// permission or an answer is derived by the web service from pending
// interactions, not reported here.
type TurnState string

const (
	TurnWorking   TurnState = "working"
	TurnCompleted TurnState = "completed"
	TurnCancelled TurnState = "cancelled"
	TurnFailed    TurnState = "failed"
)

// Turn reports a turn transition with evidence from the provider.
type Turn struct {
	State TurnState `json:"state"`
	// Error is the sanitized provider error for TurnFailed.
	Error string `json:"error,omitempty"`
	// Model is the model the provider reported for this turn, when known.
	Model string `json:"model,omitempty"`
}

// InteractionKind separates permission requests from questions.
type InteractionKind string

const (
	InteractionPermission InteractionKind = "permission"
	InteractionQuestion   InteractionKind = "question"
)

// InteractionState is the lifecycle of one interaction.
type InteractionState string

const (
	InteractionPending  InteractionState = "pending"
	InteractionAnswered InteractionState = "answered"
	InteractionRejected InteractionState = "rejected"
	// InteractionExpired means the provider withdrew the request (turn ended,
	// timeout, conversation closed) without an answer from this service.
	InteractionExpired InteractionState = "expired"
)

// Interaction is a provider request that needs the user.
type Interaction struct {
	ID    string          `json:"id"`
	Kind  InteractionKind `json:"kind"`
	Title string          `json:"title"`
	// Detail is display text: command, paths, patterns, or tool arguments.
	Detail string `json:"detail,omitempty"`
	// Options lists permission decisions the provider accepts. Answer.Decision
	// must be one of these IDs.
	Options []Option `json:"options,omitempty"`
	// Questions lists question prompts. Answer.Answers must have one entry per
	// question.
	Questions  []Question       `json:"questions,omitempty"`
	State      InteractionState `json:"state"`
	Resolution string           `json:"resolution,omitempty"`
	Time       time.Time        `json:"time"`
	// AgentID names the subagent that asked, when the provider says so.
	AgentID string `json:"agent_id,omitempty"`
}

// Option is one permission decision.
type Option struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Reject marks decisions that deny the request.
	Reject bool `json:"reject,omitempty"`
	// AllowOnce marks the decision that allows this one request and nothing
	// more. A provider leaves it off when its policy says a person must
	// decide: the web service's yolo mode answers only with this option.
	AllowOnce bool `json:"allow_once,omitempty"`
}

// Question is one prompt inside a question interaction.
type Question struct {
	Text    string   `json:"text"`
	Header  string   `json:"header,omitempty"`
	Choices []string `json:"choices,omitempty"`
	// Multiple allows selecting more than one choice.
	Multiple bool `json:"multiple,omitempty"`
	// Custom allows a free-form answer.
	Custom bool `json:"custom"`
}

// Answer responds to an interaction. Permission answers set Decision; question
// answers set Answers (one slice per Question, holding chosen choices and/or a
// custom text). Reject declines a question without answering it.
type Answer struct {
	Decision string     `json:"decision,omitempty"`
	Answers  [][]string `json:"answers,omitempty"`
	Reject   bool       `json:"reject,omitempty"`
	// Auto marks an answer UAM gave on its own (yolo), not one a person chose.
	// It is internal: a client can never set it.
	Auto bool `json:"-"`
}

// SubagentStatus is the lifecycle of one subagent.
type SubagentStatus string

const (
	SubagentRunning   SubagentStatus = "running"
	SubagentCompleted SubagentStatus = "completed"
	SubagentFailed    SubagentStatus = "failed"
	SubagentCancelled SubagentStatus = "cancelled"
)

// Terminal reports whether s is final. Once a subagent is terminal, later
// updates for it are ignored: a provider may report the end more than once.
func (s SubagentStatus) Terminal() bool {
	return s == SubagentCompleted || s == SubagentFailed || s == SubagentCancelled
}

// Subagent is one agent instance the main agent delegated work to. Its
// transcript items carry AgentID == ID.
type Subagent struct {
	ID     string `json:"id"`
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
	// ParentToolCallID is the ID of the tool call item that spawned it.
	ParentToolCallID string         `json:"parent_tool_call_id,omitempty"`
	Name             string         `json:"name"`
	Description      string         `json:"description,omitempty"`
	Status           SubagentStatus `json:"status"`
	// Error is the provider's reason for SubagentFailed.
	Error     string    `json:"error,omitempty"`
	StartedAt time.Time `json:"started_at,omitzero"`
	EndedAt   time.Time `json:"ended_at,omitzero"`
}

// FileDiff is one provider-recorded file change. Before/After hold full file
// text when available; Patch holds a unified diff when that is what the
// provider returns.
type FileDiff struct {
	Path      string `json:"path"`
	Status    string `json:"status,omitempty"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Before    string `json:"before,omitempty"`
	After     string `json:"after,omitempty"`
	Patch     string `json:"patch,omitempty"`
}
