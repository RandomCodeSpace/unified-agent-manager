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
	// Usage is the second exception: the provider implements QuotaReporter
	// and reports each conversation's AI units through EventUsage.
	Usage bool `json:"usage"`
	// Titles is true when the provider implements Titler and its
	// conversations implement SetTitle, so a chosen model can title a Task.
	Titles bool `json:"titles"`
	// Import is a capability-gated exception to provider parity: the provider lists
	// the conversations recorded for a folder and tells when another client
	// holds one open (Importer).
	Import         bool `json:"import"`
	ExecutionModes bool `json:"execution_modes,omitempty"`
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

// QuotaReporter is implemented by a provider whose Capabilities.Usage is
// true. Quota reads the signed-in account's quotas, sorted by Type; ctx
// bounds the call.
type QuotaReporter interface {
	Quota(ctx context.Context) ([]Quota, error)
}

// Titler is implemented by a provider whose Capabilities.Titles is true.
type Titler interface {
	// Title asks req.Model for a title of a Task's first message and returns
	// the model's reply as it came; the caller cleans it. The call runs in a
	// throwaway conversation without tools that it deletes whatever the
	// outcome. ctx bounds the whole call.
	Title(ctx context.Context, req TitleRequest) (string, error)
}

// TitleRequest is one Titler call.
type TitleRequest struct {
	// Model is a model ID from Provider.Models.
	Model string
	// Workdir is the Task's project directory.
	Workdir string
	// Text is the Task's first message, already sanitized and clipped.
	Text string
}

// Quota is one account quota as the provider reported it.
type Quota struct {
	// Type is the provider's quota key, e.g. Copilot's premium_interactions.
	Type string
	// Used is what the current period used; Entitlement is what it includes,
	// 0 when Unlimited.
	Used        int64
	Entitlement int64
	Unlimited   bool
	// RemainingPercent is the provider's percentage of the entitlement left.
	RemainingPercent float64
	// Overage is what was used beyond the entitlement this period.
	Overage float64
	// ResetAt is when the provider says the quota resets; zero when it does
	// not say. It is not checked here.
	ResetAt time.Time
}

// Relative model cost tiers, cheapest first.
const (
	CostLow      = "low"
	CostMedium   = "medium"
	CostHigh     = "high"
	CostVeryHigh = "very_high"
)

// HistoryReader is implemented by a provider that can read a conversation's
// recorded transcript without opening it: nothing is sent, no other client is
// shut out, and the provider's record does not change. ctx bounds the read.
// ErrConversationNotFound means the exact ID has no record.
type HistoryReader interface {
	ReadHistory(ctx context.Context, req ReadRequest) (History, error)
}

// ReadRequest names the conversation to read and its project directory.
type ReadRequest struct {
	ConversationID string
	Workdir        string
}

// Importer is implemented by a provider whose Capabilities.Import is set.
type Importer interface {
	// Previous lists the conversations recorded with exactly workdir as
	// their working directory. An empty workdir lists all local conversations.
	Previous(ctx context.Context, workdir string) ([]PreviousConversation, error)
	// InUse returns the IDs among ids that another process holds open. It is
	// a snapshot: a client can open one right after it. The provider's own
	// runtime is never reported.
	InUse(ctx context.Context, ids []string) ([]string, error)
}

// PreviousConversation is one conversation Importer.Previous lists. Title is
// untrusted provider text.
type PreviousConversation struct {
	ID        string
	Title     string
	Workdir   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Model is one selectable model.
type Model struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Efforts      []string      `json:"efforts"`
	ContextSizes []ContextSize `json:"context_sizes"`
	// Media is what the model accepts besides text. Nil means the provider
	// reports nothing (Copilot's auto), and uploads are not gated.
	Media *Media `json:"media,omitempty"`
	// CostTier is one of the Cost constants, the provider's relative cost of
	// the model; empty when it reports none.
	CostTier string `json:"cost_tier,omitempty"`
	// DiscountPercent is the whole-number discount (1-100) the provider
	// applies to usage billed through this model, such as Copilot's auto; 0
	// when it reports none.
	DiscountPercent int `json:"discount_percent,omitempty"`
	// Prices are the model's token prices; nil when the provider reports
	// none.
	Prices *Prices `json:"prices,omitempty"`
}

// Prices are a model's token prices in AI Credits, the unit of
// Usage.AIUnits, per BatchSize tokens. A nil price or a zero size was not
// reported.
type Prices struct {
	BatchSize int64 `json:"batch_size,omitempty"`
	TierPrices
	// LongContext holds the long-context tier's prices, which apply past the
	// default MaxPromptTokens or when that tier is selected.
	LongContext *TierPrices `json:"long_context,omitempty"`
}

// TierPrices are one context tier's prices per batch and its prompt budget.
type TierPrices struct {
	Input           *float64 `json:"input,omitempty"`
	Output          *float64 `json:"output,omitempty"`
	CacheRead       *float64 `json:"cache_read,omitempty"`
	CacheWrite      *float64 `json:"cache_write,omitempty"`
	MaxPromptTokens int64    `json:"max_prompt_tokens,omitempty"`
}

// Media gates image and PDF uploads for one model. Text is always accepted.
type Media struct {
	Images bool `json:"images"`
	PDF    bool `json:"pdf"`
	// MaxImages is the most images one prompt may carry; 0 means no limit
	// was reported.
	MaxImages int `json:"max_images,omitempty"`
	// Types lists the accepted image and document MIME types; empty means
	// any type Images and PDF allow.
	Types []string `json:"types,omitempty"`
}

// ContextSize is a selectable provider tier and its prompt token budget.
type ContextSize struct {
	ID     string `json:"id"`
	Tokens int64  `json:"tokens"`
}

// Context is the provider's latest live usage report, not a token estimate.
// Prompt is the input tokens of the latest main-agent model call and Cached
// how many of them the provider read from its cache; both are 0 until a
// call reports them.
type Context struct {
	Used   int64 `json:"used"`
	Limit  int64 `json:"limit"`
	Prompt int64 `json:"prompt,omitempty"`
	Cached int64 `json:"cached,omitempty"`
}

// Usage is what a conversation consumed. AIUnits is its total so far, main
// agent and subagents together, never an increment: a later report replaces
// an earlier one.
type Usage struct {
	AIUnits float64 `json:"ai_units"`
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
	Send(ctx context.Context, prompt Prompt) error
	// Commands lists the slash commands that become a prompt: skills and
	// the provider's prompt commands. Nothing else is offered.
	Commands(ctx context.Context) ([]Command, error)
	// RunCommand runs one command from Commands with args.Text as its
	// arguments. It starts a turn like Send, with Send's outcomes; the user
	// item shows "/name arguments". A result that would not start a prompt
	// turn is a rejection. ErrUnsupported means the provider has no commands.
	RunCommand(ctx context.Context, name string, args Prompt) error
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
	// CancelSubagent stops only the exact agent instance. Its final status
	// arrives through EventSubagent; the parent and siblings stay running.
	CancelSubagent(ctx context.Context, agentID string) error
	// PromptSubagent sends text to the exact agent instance while it is
	// SubagentIdle, and returns once the provider accepted or refused it. It
	// is not a turn: the main agent does not see it. An accepted prompt moves
	// the subagent back to SubagentRunning through EventSubagent. Ambiguous
	// failures wrap ErrSubmissionUncertain and are never resent.
	// ErrUnsupported means the provider cannot chat with a subagent.
	PromptSubagent(ctx context.Context, agentID, text string) error
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
	// SetTitle names the conversation in the provider's own store, so the
	// provider's clients show title and the provider no longer titles it.
	// ErrUnsupported means the provider cannot.
	SetTitle(ctx context.Context, title string) error
	// Close disconnects from the conversation without deleting it. Pending
	// interactions end without an answer being fabricated.
	Close(ctx context.Context) error
}

// Prompt is one user message: the text as typed, plus project files and
// uploads the web service has already checked.
type Prompt struct {
	Text        string
	Files       []File
	Attachments []Blob
}

// Blob is uploaded content sent inline. MIME is image/png, image/jpeg,
// image/gif, image/webp, application/pdf or text/plain.
type Blob struct {
	Name string
	MIME string
	Data []byte
}

// Attachment describes uploaded content: an upload's metadata, or content a
// user item carried. ID names the web service's stored copy and is empty
// when it has none.
type Attachment struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
	MIME string `json:"mime"`
	Size int64  `json:"size,omitempty"`
	// SHA256 is the hex digest of the content, when the provider's record
	// has it; the web service matches it to its stored copy. It is never
	// sent to browsers.
	SHA256 string `json:"-"`
}

// File is a project file or directory the user referenced. Path is
// absolute; Rel is the path relative to the working directory, shown to the
// user.
type File struct {
	Path string
	Rel  string
	Dir  bool
}

// Command is one slash command. Kind is "skill" for a skill and "command"
// for any other prompt command.
type Command struct {
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	Kind            string          `json:"kind"`
	InputHint       string          `json:"input_hint"`
	Aliases         []string        `json:"aliases,omitempty"`
	AllowDuringTurn bool            `json:"allow_during_turn,omitempty"`
	DisabledReason  string          `json:"disabled_reason,omitempty"`
	InputChoices    []CommandOption `json:"input_choices,omitempty"`
	InputRequired   bool            `json:"input_required,omitempty"`
}

type CommandOption struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Group       string `json:"group,omitempty"`
}

// CommandExecutor handles commands with typed results as well as prompts.
// A nil result means an agent prompt was submitted. Ambiguous provider
// failures wrap ErrSubmissionUncertain and must never be retried implicitly.
type CommandExecutor interface {
	ExecuteCommand(context.Context, string, Prompt) (*CommandResult, error)
}

type CommandResult struct {
	Kind         string          `json:"kind"`
	Text         string          `json:"text,omitempty"`
	Markdown     bool            `json:"markdown,omitempty"`
	PrefillInput string          `json:"prefill_input,omitempty"`
	Title        string          `json:"title,omitempty"`
	Command      string          `json:"command,omitempty"`
	Options      []CommandOption `json:"options,omitempty"`
	Action       string          `json:"action,omitempty"`
}

// ExecutionState is a runtime observation, separate from permission policy.
// A disconnected runtime retains the last observation with Known=false.
type ExecutionState struct {
	Known     bool                `json:"known"`
	Mode      string              `json:"mode,omitempty"`
	Objective *AutopilotObjective `json:"objective,omitempty"`
}

type AutopilotObjective struct {
	ID                int64    `json:"id"`
	Objective         string   `json:"objective"`
	Status            string   `json:"status"`
	TurnCount         int64    `json:"turn_count"`
	PauseReason       string   `json:"pause_reason,omitempty"`
	CompletionSummary string   `json:"completion_summary,omitempty"`
	CreditsUsed       *float64 `json:"credits_used,omitempty"`
	CreditLimit       *float64 `json:"credit_limit,omitempty"`
}

// Command kinds.
const (
	CommandSkill  = "skill"
	CommandPrompt = "command"
)

// History is a reopened conversation's provider-recorded state.
type History struct {
	// Items is the transcript, oldest first. Subagent items carry AgentID.
	Items []Item
	// Subagents are the subagent records the recorded events allow to
	// rebuild.
	Subagents []Subagent
	// Usage is the conversation's AI units as recorded, or nil when the
	// record has none.
	Usage *Usage
	// Model is the main agent's model as the record last states it, or ""
	// when the record does not say.
	Model string
	// Truncated is set when only the newest part of a record too large to
	// read whole was returned.
	Truncated bool
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
	// EventBackgroundTasks replaces the live background shell task snapshot.
	EventBackgroundTasks EventKind = "background_tasks"
	// EventContext reports main-agent context usage; it is never persisted.
	EventContext EventKind = "context"
	// EventUsage reports the conversation's AI units so far in Event.Usage.
	EventUsage     EventKind = "usage"
	EventExecution EventKind = "execution"
)

// Event is one adapter notification. Exactly one payload matches Kind.
type Event struct {
	Kind            EventKind
	Item            *Item
	Delta           *Delta
	Turn            *Turn
	Interaction     *Interaction
	Subagent        *Subagent
	BackgroundTasks *BackgroundTasks
	Context         *Context
	Usage           *Usage
	Execution       *ExecutionState
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
	// Attachments are the uploads a user item carried; the adapter never
	// includes their bytes.
	Attachments []Attachment `json:"attachments,omitempty"`
	// Images are the images a tool item's result returned, in the
	// provider's order. The adapter includes their bytes; the web service
	// stores them and passes on only the stored copies' metadata.
	Images []Image `json:"images,omitempty"`
	// ImagesNote is set by the web service when it did not keep some of a
	// tool item's images, and says why. Adapters leave it empty.
	ImagesNote string `json:"images_note,omitempty"`
}

// Image is one image a tool's result returned. Adapters fill Data, MIME and
// Name when the provider gives one; when the provider's record keeps only
// the digest, they fill SHA256 without Data. The web service digests Data
// itself, stores it with the Task, sets ID and Size, and clears Data, so
// image bytes never reach a browser inline.
type Image struct {
	// ID names the web service's stored copy.
	ID   string `json:"id"`
	MIME string `json:"mime"`
	Size int64  `json:"size"`
	Name string `json:"name,omitempty"`
	// SHA256 is the hex digest of the bytes. The web service matches it to
	// a stored copy when Data is empty. It is never sent to browsers.
	SHA256 string `json:"-"`
	// Data is the image's bytes, from the adapter to the web service only.
	Data []byte `json:"-"`
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
	// ToolCallID names the provider tool call a request is for: the ID of
	// that call's ItemTool item with the same AgentID. For a permission it
	// is the call that needs it; for a question, the tool call that asked
	// (Copilot's ask_user). It is empty when unknown.
	ToolCallID string `json:"tool_call_id,omitempty"`
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
	SubagentRunning SubagentStatus = "running"
	// SubagentIdle means the live provider reports the subagent finished and
	// ready for a follow-up. It is not terminal.
	SubagentIdle      SubagentStatus = "idle"
	SubagentCompleted SubagentStatus = "completed"
	SubagentFailed    SubagentStatus = "failed"
	SubagentCancelled SubagentStatus = "cancelled"
)

// Terminal reports whether s is an end status. Failed and cancelled are final;
// completed only becomes SubagentIdle when the live provider reports that the
// subagent takes a follow-up. Other later updates are ignored: a provider may
// report the end more than once.
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

// BackgroundTasks describes provider-owned shell processes independently of
// the foreground turn. Known is false when their current state cannot be read;
// Tasks then retains the last known snapshot, not a claim of process liveness.
type BackgroundTasks struct {
	Known bool             `json:"known"`
	Tasks []BackgroundTask `json:"tasks"`
}

type BackgroundTask struct {
	ID          string    `json:"id"`
	Description string    `json:"description,omitempty"`
	Command     string    `json:"command"`
	Status      string    `json:"status"`
	StartedAt   time.Time `json:"started_at,omitzero"`
	EndedAt     time.Time `json:"ended_at,omitzero"`
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

// BackgroundTaskController stops a provider-owned shell process independently
// of the foreground turn. The returned snapshot is freshly read when Known.
type BackgroundTaskController interface {
	CancelBackgroundTask(context.Context, string) (BackgroundTasks, error)
}

var (
	ErrBackgroundTaskNotFound = errors.New("background shell task not found")
	ErrBackgroundTaskInactive = errors.New("background shell task is not running")
)
