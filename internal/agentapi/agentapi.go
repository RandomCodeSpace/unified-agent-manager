// Package agentapi is the provider-neutral contract between the uam web
// service and structured provider integrations such as the Copilot SDK.
// It carries only what the web interface needs; provider specifics stay
// inside the adapters.
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
	"encoding/json"
	"errors"
	"slices"
	"time"
)

// Provider names match the uam agent names used in sessions.json.
const (
	ProviderCopilot = "copilot"
)

var (
	// ErrConversationNotFound reports that an exact conversation ID does not
	// exist in the provider's store. The caller must not create a replacement.
	ErrConversationNotFound = errors.New("provider conversation not found")
	// ErrItemNotFound reports that a conversation's record has no item with
	// the requested agent and ID.
	ErrItemNotFound = errors.New("recorded item not found")
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
	// ErrForkUncertain means a native fork may exist, but its exact returned
	// identity was not confirmed. A caller must not repeat the native RPC.
	ErrForkUncertain = errors.New("conversation branch outcome is unknown")
	// ErrRewindUncertain means a native rewind may have changed files or
	// history, but its result was lost. A caller must not repeat the RPC.
	ErrRewindUncertain = errors.New("conversation rewind outcome is unknown")
)

// Capabilities advertises what an adapter really supports. The UI hides or
// disables controls for unsupported operations instead of pretending.
type Capabilities struct {
	// Plan supports native plan mode and explicit review in the composer.
	Plan        bool `json:"plan,omitempty"`
	Cancel      bool `json:"cancel"`
	Permissions bool `json:"permissions"`
	Questions   bool `json:"questions"`
	// SessionDiff is true when the provider records per-conversation file
	// changes (Conversation.Diff). Workspace Git diffs are separate.
	SessionDiff bool `json:"session_diff"`
	// SessionDiffNeedsTracking limits the default native scope to Tasks that
	// opted into capture before their first turn. Explicit session reads still
	// report the provider's current availability for legacy conversations.
	SessionDiffNeedsTracking bool `json:"-"`
	History                  bool `json:"history"`
	// Fork copies a recorded owner-turn prefix without resending messages.
	Fork bool `json:"fork,omitempty"`
	// Rewind discards an open conversation's recorded suffix natively.
	Rewind bool `json:"rewind,omitempty"`
	// ContextSize is the per-Task context-tier exception to provider parity.
	ContextSize bool `json:"context_size"`
	// ContextBreakdown is an optional on-demand ContextReader, never a stream.
	ContextBreakdown bool `json:"context_breakdown,omitempty"`
	// Usage is the second exception: the provider implements QuotaReporter
	// and reports each conversation's AI units through EventUsage.
	Usage bool `json:"usage"`
	// UsageMetrics advertises an on-demand native conversation reader, separate
	// from account quotas and the combined EventUsage total.
	UsageMetrics bool `json:"usage_metrics,omitempty"`
	// Aside is true when the provider's conversations implement AsideAsker.
	Aside bool `json:"aside,omitempty"`
	// Titles is true when the provider implements Titler and its
	// conversations implement SetTitle, so a chosen model can title a Task.
	Titles bool `json:"titles"`
	// Import is a capability-gated exception to provider parity: the provider lists
	// the conversations recorded for a folder and tells when another client
	// holds one open (Importer).
	Import         bool `json:"import"`
	ExecutionModes bool `json:"execution_modes,omitempty"`
	// HostTools is true when the provider registers OpenRequest.Tools in its
	// conversations and implements UtilityRunner.
	HostTools bool `json:"host_tools,omitempty"`
	// Account is true when the provider implements AccountManager.
	Account bool `json:"account,omitempty"`
	// DeviceSignIn is true when the provider implements DeviceSignInManager.
	DeviceSignIn bool `json:"device_sign_in,omitempty"`
	// MCP is true when the provider implements MCPConfigurer and its
	// conversations implement MCPController.
	MCP bool `json:"mcp,omitempty"`
	// CLIUpdate is true when the provider implements CLIUpdater.
	CLIUpdate bool `json:"cli_update,omitempty"`
	// SubagentModels is true when the provider implements SubagentModelUser.
	SubagentModels bool `json:"subagent_models,omitempty"`
	// GitHubMCP is true when the provider implements GitHubMCPUser.
	GitHubMCP bool `json:"github_mcp,omitempty"`
}

// Provider creates and reopens conversations for one provider runtime.
type Provider interface {
	// Name returns the provider identifier used in persisted task records.
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

// Forker is an optional provider-level capability. Boundary reads use only
// the persisted record; they never open the source conversation. Fork must
// revalidate the exact boundary before calling the provider and never retry
// an RPC whose result is uncertain. Any error after a possible native side
// effect must wrap ErrForkUncertain; other errors guarantee no fork exists.
type Forker interface {
	ReadForkBoundary(context.Context, ForkBoundaryRequest) (ForkBoundary, error)
	Fork(context.Context, ForkRequest) (string, error)
}

type ForkBoundaryRequest struct {
	ConversationID string
	UserItemID     string
}

// ForkBoundary includes the selected owner turn. ToEventID is the next
// owner-start event, which the native fork excludes. Empty means the proven
// current end; TailEventID detects an append before that fork starts.
type ForkBoundary struct {
	UserEventID string `json:"user_event_id"`
	ToEventID   string `json:"to_event_id,omitempty"`
	TailEventID string `json:"tail_event_id"`
}

type ForkRequest struct {
	ForkBoundaryRequest
	Boundary ForkBoundary
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

// UtilityRunner is implemented by a provider whose Capabilities.HostTools is
// true. RunUtility asks req.Model to answer req.Prompt in a throwaway
// conversation whose whole system message is req.System and whose only
// tools are req.Tools: no provider tools, no discovered configuration, no
// session store, and every permission request rejected. It returns the
// model's final reply as it came and deletes the conversation whatever the
// outcome. ctx bounds the whole call.
type UtilityRunner interface {
	RunUtility(ctx context.Context, req UtilityRequest) (string, error)
}

// UtilityRequest is one UtilityRunner call.
type UtilityRequest struct {
	// Model is a model ID from Provider.Models; Workdir is the project
	// directory the conversation runs in.
	Model, Workdir string
	// Purpose names the job in the provider's client name and logs, such as
	// "title".
	Purpose        string
	System, Prompt string
	// Attachments go with Prompt as uploads, such as the images a title
	// is made from.
	Attachments []Blob
	// Tools and CallTool are as in OpenRequest. Their calls have no TaskID.
	Tools    []HostTool
	CallTool func(context.Context, HostToolCall) HostToolResult
	// Timeout, when positive, also bounds the whole call.
	Timeout time.Duration
	// OnUsage, when set, receives what the call's model requests reported,
	// summed, once before the call returns; it is not called when none
	// reported usage.
	OnUsage func(UtilityUsage)
}

// UtilityUsage is what the model requests of one Utility call reported,
// summed: tokens in and out, and their cost in AI Credits, 0 when not
// reported.
type UtilityUsage struct {
	InputTokens, OutputTokens int64
	Credits                   float64
	// Tokens preserves each reported model call for dated usage accounting.
	Tokens []TokenUsage
}

// TokenUsage is one model call, including subagent calls. Input includes
// cache reads and writes; neither cache count is added again to the total.
type TokenUsage struct {
	Model                                string
	Time                                 time.Time
	Input, Output, CacheRead, CacheWrite int64
	// DurationMS is the model call's own duration when the provider reports it.
	DurationMS int64
}

// UsageSessionRecorder lets the host persist ownership of provider sessions
// before inference. Configure it before starting or checking the provider.
// Active marks ownership before inference; inactive follows a successful disconnect.
// A recorder error prevents use of the session without deleting its history.
type UsageSessionRecorder interface {
	SetUsageSessionRecorder(func(sessionID string, active bool) error)
}

// CustomModelUser is implemented by a provider that can offer custom
// models next to its own. SetCustomModels replaces them: Models lists them,
// and conversations opened or switched afterwards can select them.
type CustomModelUser interface {
	SetCustomModels([]CustomModel)
}

// SubagentModelUser is implemented by a provider whose agents start
// subagents on a model they choose. SetSubagentModels limits every subagent
// started afterwards, in open conversations too, to the model IDs in models;
// the first is the fallback when the Task's own model is not among them. An
// empty list lifts the limit.
type SubagentModelUser interface {
	SetSubagentModels(models []string)
}

// GitHubMCPUser is implemented by a provider whose runtime has a built-in
// GitHub MCP server. SetGitHubMCP turns it on or off for every conversation
// opened or reopened afterwards, and for the open ones. It is off until the
// first call.
type GitHubMCPUser interface {
	SetGitHubMCP(on bool)
}

// CustomModel is an OpenAI-compatible model the owner brought. Its model ID
// is SelectionID. APIKey holds a saved key, or APIKeyEnv names the service
// environment variable the provider reads when it needs the key.
type CustomModel struct {
	// Name names the provider connection; models with one Name share
	// BaseURL, WireAPI and APIKeyEnv.
	Name        string
	DisplayName string
	BaseURL     string
	ModelID     string
	Vision      bool
	// WireAPI is "completions" (also for "") or "responses".
	WireAPI   string
	APIKeyEnv string
	APIKey    string `json:"-"`
}

// SelectionID is the model's ID among the provider's models.
func (m CustomModel) SelectionID() string { return m.Name + "/" + m.ModelID }

// TitleRequest is one Titler call.
type TitleRequest struct {
	// Model is a model ID from Provider.Models.
	Model string
	// Workdir is the Task's project directory.
	Workdir string
	// Text is the Task's first message, already sanitized and clipped; it
	// may be empty when the message carried only uploads or files.
	Text string
	// Images are the first message's images, set only when Model accepts
	// them.
	Images []Blob
	// Reply is the agent's reply to the first message, sanitized and
	// clipped, set when the title waited for the first turn.
	Reply string
	// OnUsage is as in UtilityRequest.
	OnUsage func(UtilityUsage)
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

// ItemDiffReader reads one immutable committed edit from the recorded native event.
// It sends nothing, resumes no conversation, and never reads the current file.
type ItemDiffReader interface {
	ReadItemDiff(ctx context.Context, req ItemDiffRequest) (ItemDiff, error)
}
type ItemDiffRequest struct {
	ReadRequest
	AgentID, ItemID, EventID, Path string
}

// ItemDiff contains only the requested file's recorded patch. An unavailable
// native detail reports Status without inventing content or line counts.
type ItemDiff struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	Patch  string `json:"patch,omitempty"`
}

const MaxFileEdits = 32
const MaxFileEditPathBytes = 4096
const MaxFileEditPathsBytes = 16 << 10

// The browser's separate 2 MiB cache charges UTF-16 text and entry overhead.
const MaxEditPatchBytes = (1 << 20) - 256

// ReadRequest names the conversation to read and its project directory.
type ReadRequest struct {
	ConversationID string
	Workdir        string
}

// HistoryPager is implemented by a HistoryReader that can read one agent's
// recorded transcript a window at a time, so items too many or too long to
// keep in memory stay reachable. Like ReadHistory it sends nothing and
// changes nothing, and it also reads the record of an open conversation.
// ErrConversationNotFound means the record is gone; ErrItemNotFound that it
// has no item req.ItemID of req.AgentID.
type HistoryPager interface {
	ReadHistoryWindow(ctx context.Context, req WindowRequest) (HistoryWindow, error)
}

// WindowRequest names one recorded item and how many of its agent's items
// around it to read. An empty ItemID stands for the end of the record.
type WindowRequest struct {
	ReadRequest
	// AgentID is "" for the main agent, otherwise a subagent instance ID.
	// Only that agent's items are read.
	AgentID string
	ItemID  string
	// Before and After bound the items read before and after ItemID.
	Before, After int
}

// HistoryWindow is a contiguous run of one agent's recorded items, oldest
// first and whole: no text is clipped.
type HistoryWindow struct {
	Items []Item
	// At is the index of the requested item in Items, len(Items) for the
	// end of the record.
	At int
	// Start is set when Items begins with the agent's first recorded item.
	Start bool
	// Next is the ID of the item recorded right after Items, or "" when
	// Items ends with the newest one.
	Next string
}

// SubagentPager is implemented by a HistoryReader that can read a
// conversation's recorded subagents a window at a time, so every one stays
// listable when only the newest are kept in memory. Like ReadHistory it
// sends nothing and changes nothing. ErrConversationNotFound means the
// record is gone; ErrItemNotFound that it records no subagent req.AgentID.
type SubagentPager interface {
	ReadSubagents(ctx context.Context, req SubagentRequest) (SubagentWindow, error)
}

// SubagentRequest names one recorded subagent and how many of those
// recorded before it to read.
type SubagentRequest struct {
	ReadRequest
	AgentID string
	Before  int
}

// SubagentWindow is a contiguous run of a conversation's subagents in the
// order they were first recorded, ending with the requested one. Each record
// is as ReadHistory reports it.
type SubagentWindow struct {
	Subagents []Subagent
	// Start is set when Subagents begins with the first recorded subagent.
	Start bool
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
	// ValidateFile checks a declaration candidate without granting access or
	// reading file bytes, and returns its normalized absolute display path.
	ValidateFile func(context.Context, string) (string, error)
	// Tools are the web service's host tools, registered beside the
	// adapter's own when Capabilities.HostTools is set. CallTool, bound to
	// this Task by the web service, runs each of their calls.
	Tools    []HostTool
	CallTool func(context.Context, HostToolCall) HostToolResult
	// SkillDirectories are directories of the web service's own skills, each
	// holding <name>/SKILL.md, loaded beside the ones the provider discovers.
	SkillDirectories []string
	// CompactionThreshold is the share of the context window (0 to 1) at
	// which the conversation starts compacting; 0 keeps the provider default.
	// It applies on create and on every reopen.
	CompactionThreshold float64
}

// MaxHostToolArguments bounds the JSON arguments of one host tool call;
// adapters refuse a larger call without calling CallTool.
const MaxHostToolArguments = 64 << 10

// HostTool is a tool the web service runs in-process. Adapters register it
// as given, without a permission prompt and never deferred, and forward each
// call to CallTool; they never interpret it. Name is unique among the
// conversation's tools and must not clash with a provider tool: an adapter
// that finds a clash, or a tool of that name from an MCP server, fails the
// open. Parameters is the JSON Schema object of the arguments.
type HostTool struct {
	Name        string
	Description string
	Parameters  map[string]any
}

// HostToolCall is one call of a HostTool.
type HostToolCall struct {
	Name string
	// CallID is the provider's tool call ID, the ID of the call's ItemTool
	// item. A call repeated with the same CallID gets the first one's result
	// without reaching CallTool again.
	CallID string
	// TaskID is the conversation's OpenRequest.SessionID, the Task the call
	// belongs to.
	TaskID string
	// AgentID is the subagent instance that made the call, as in
	// Item.AgentID; it is empty for the main agent and when the provider does
	// not say.
	AgentID string
	// Arguments is the model's JSON object as received, at most
	// MaxHostToolArguments bytes.
	Arguments json.RawMessage
}

// HostToolResult is what a host tool call returns. CallTool returns
// promptly and stops when its context ends: the call was cancelled or the
// conversation closed.
type HostToolResult struct {
	// Text is what the model sees.
	Text string
	// Failed marks a refused or failed call; Text says why.
	Failed bool
	// Payload is optional JSON the web service keeps for its own display of
	// the call. Adapters never send it to the model.
	Payload json.RawMessage
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
	// sent during a turn into that turn must run it after the turn or refuse
	// it with ErrBusy instead.
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
	Steer(ctx context.Context, prompt Prompt) error
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

// TurnChangeReader snapshots only the finalized foreground owner turn of an
// existing open conversation. It reads native captures, never current Git or
// files, and must refuse a boundary followed by another ordinary owner turn.
type TurnChangeReader interface {
	TurnChanges(context.Context, string) (NativeTurnChanges, error)
}

const (
	MaxTurnChangeFiles           = 32
	MaxTurnChangePathBytes       = 16 << 10
	MaxTurnChangeCount     int64 = 1_000_000_000
)

// NativeTurnChanges is the immutable native captured suffix at its owner's
// settle boundary. No patches or user message text belong in this metadata.
type NativeTurnChanges struct {
	Status    string           `json:"status"`
	EventID   string           `json:"event_id,omitempty"`
	Files     int64            `json:"files,omitempty"`
	Additions int64            `json:"additions,omitempty"`
	Deletions int64            `json:"deletions,omitempty"`
	Omitted   int64            `json:"omitted,omitempty"`
	Entries   []NativeTurnFile `json:"entries,omitempty"`
}

type NativeTurnFile struct {
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	Additions int64  `json:"additions,omitempty"`
	Deletions int64  `json:"deletions,omitempty"`
}

// HistoryRewinder is optional on an open Conversation. PreviewRewind is
// readonly. Rewind issues the native request once: it has no idempotency key,
// so any error after a possible side effect wraps ErrRewindUncertain, and
// other errors guarantee nothing was sent.
type HistoryRewinder interface {
	PreviewRewind(ctx context.Context, userItemID string) (RewindPreview, error)
	Rewind(ctx context.Context, userEventID, mode string) (RewindResult, error)
}

const (
	RewindConversation         = "conversation"
	RewindConversationAndFiles = "conversation-and-files"
	MaxRewindDiscarded         = 2000
	MaxRewindResultFiles       = 256
)

// RewindPreview binds the exact native boundary that begins the discarded
// suffix. Discarded holds the newest discarded root owner item IDs; Turns
// counts all of them. Files are the native forward counts over the suffix.
type RewindPreview struct {
	UserEventID    string            `json:"user_event_id"`
	TailEventID    string            `json:"tail_event_id"`
	Turns          int               `json:"turns"`
	Discarded      []string          `json:"-"`
	FilesAvailable bool              `json:"files_available"`
	FilesReason    string            `json:"files_reason,omitempty"`
	Files          NativeTurnChanges `json:"files"`
}

// RewindResult keeps every native outcome and the presence of its optional
// fields. Lists are bounded; Omitted counts say how many paths were dropped.
type RewindResult struct {
	Outcome         string       `json:"outcome"`
	Error           *string      `json:"error,omitempty"`
	EventsRemoved   *int64       `json:"events_removed,omitempty"`
	RestoredFiles   []string     `json:"restored_files"`
	SkippedFiles    []RewindSkip `json:"skipped_files"`
	RestoredOmitted int          `json:"restored_omitted,omitempty"`
	SkippedOmitted  int          `json:"skipped_omitted,omitempty"`
}

type RewindSkip struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Prompt is one user message: the text as typed, plus project files and
// uploads the web service has already checked.
type Prompt struct {
	Text        string
	Files       []File
	Attachments []Blob
}

// Blob is uploaded content sent inline. MIME is image/png, image/jpeg,
// image/gif, image/webp, application/pdf or text/plain. Path, set for a PDF,
// is the absolute path of a copy of Data under the file's own name
// (UploadsDir/<task>/<id>.d/<name>), for a provider that reads documents
// from disk.
type Blob struct {
	Name string
	MIME string
	Data []byte
	Path string
}

// UploadsDir names the directory the web service keeps uploads in.
const UploadsDir = "web-attachments"

// Attachment describes uploaded content: an upload's metadata, or content a
// user item carried. ID names the web service's stored copy and is empty
// when it has none.
type Attachment struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
	MIME string `json:"mime"`
	Size int64  `json:"size,omitempty"`
	// NotNative is set on a document the provider did not pass to the model
	// as a document: the agent got its path, to read with its own tools.
	NotNative bool `json:"not_native,omitempty"`
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
	// EventPlanPath reports the exact provider-owned scratch plan identity,
	// for project-change exclusion. It is not a local-file read permission.
	EventPlanPath EventKind = "plan_path"
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
	// EventSchedules replaces open-conversation native schedule display metadata.
	EventSchedules EventKind = "schedules"
	// EventContext reports main-agent context usage; it is never persisted.
	EventContext EventKind = "context"
	// EventUsage reports the conversation's AI units so far in Event.Usage.
	EventUsage     EventKind = "usage"
	EventTokens    EventKind = "tokens"
	EventExecution EventKind = "execution"
	// EventCompaction reports that the provider started (Compacting) or
	// finished compacting the main conversation; how it ended arrives as a
	// notice item.
	EventCompaction EventKind = "compaction"
	// EventActivity replaces the running turn's live activity with
	// Event.Activity; it is never persisted.
	EventActivity EventKind = "activity"
	// EventTodos replaces the conversation's todo list with Event.Todos; it
	// is never persisted.
	EventTodos EventKind = "todos"
	// EventModelSelection reports provider-confirmed main-agent model settings,
	// never the owner's consent to a requested change.
	EventModelSelection EventKind = "model_selection"
)

// Event is one adapter notification. Exactly one payload matches Kind.
type Event struct {
	Kind            EventKind
	Item            *Item
	Delta           *Delta
	Turn            *Turn
	Interaction     *Interaction
	PlanPath        string
	PlanVersion     uint64 // lightweight invalidation from native plan_changed
	Subagent        *Subagent
	BackgroundTasks *BackgroundTasks
	Schedules       *ScheduleSnapshot
	Context         *Context
	Usage           *Usage
	Tokens          *TokenUsage
	Execution       *ExecutionState
	// Error is the sanitized reason for EventExit.
	Error string
	// Title is the untrusted provider title for EventTitle.
	Title string
	// Compacting is the payload of EventCompaction.
	Compacting     bool
	Activity       *Activity
	Todos          *TodoList
	ModelSelection *ModelSelection
}

// ModelSelection contains confirmed settings. Nil optional fields mean the
// provider did not report them; context usage and capacity are separate facts.
type ModelSelection struct {
	Model       string
	Effort      *string
	ContextSize *string
}

// Activity is the main agent's live state in the running turn. The adapter
// starts it afresh with each turn; the provider never records it.
type Activity struct {
	// Plan is true only after this turn's explicit native implementation approval.
	Plan bool `json:"plan,omitempty"`
	// Intent is what the main agent says it is doing, "" when it says
	// nothing.
	Intent string `json:"intent,omitempty"`
	// Retry is set while the turn's current model call is being retried.
	Retry *Retry `json:"retry,omitempty"`
}

// Retry describes a model call the provider is retrying.
type Retry struct {
	// Count is how many times the call was retried so far.
	Count int `json:"count"`
	// Reason is the provider's reason code, such as "rate_limited", when it
	// gives one.
	Reason string `json:"reason,omitempty"`
	// Status is the HTTP status of the attempt that failed, 0 when unknown.
	Status int `json:"status,omitempty"`
	// Network is set when that attempt failed without a response.
	Network bool      `json:"network,omitempty"`
	At      time.Time `json:"at"`
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

// CompletionDecision is the provider's response to a completion request.
type CompletionDecision string

const (
	CompletionAccepted CompletionDecision = "accepted"
	CompletionRejected CompletionDecision = "rejected"
	CompletionBlocked  CompletionDecision = "blocked"
	CompletionUnknown  CompletionDecision = "unknown"
)

// TaskCompletion preserves label-safe native facts. UserItemID is the exact
// ordinary user-message ID; empty means the receipt cannot be correlated.
type TaskCompletion struct {
	Decision   CompletionDecision `json:"decision"`
	UserItemID string             `json:"user_item_id,omitempty"`
	Summary    string             `json:"summary,omitempty"`
	Reason     string             `json:"reason,omitempty"`
	Blocker    *CompletionBlocker `json:"blocker,omitempty"`
}

// CompletionBlocker omits opaque permission-recovery data.
type CompletionBlocker struct {
	Kind      string `json:"kind"`
	Reason    string `json:"reason"`
	Resumable bool   `json:"resumable"`
}

// Item is one transcript entry. Text is untrusted provider output.
type Item struct {
	ID   string    `json:"id"`
	Kind ItemKind  `json:"kind"`
	Text string    `json:"text,omitempty"`
	Tool *ToolCall `json:"tool,omitempty"`
	// Completion is a provider's bounded completion decision, preserved as
	// a notice. It is separate from the foreground turn's lifecycle.
	Completion *TaskCompletion `json:"completion,omitempty"`
	// Plan is compact native review metadata; its body is read on demand.
	Plan *PlanReview `json:"plan,omitempty"`
	// Time is when the item began: a tool call's start, a thought's model
	// call start, a message's first text.
	Time time.Time `json:"time"`
	// EndedAt is when a tool call or thought finished, from the provider's
	// record; zero while it runs or when the provider did not say.
	EndedAt time.Time `json:"ended_at,omitzero"`
	// AgentID is empty for the main agent, otherwise the exact subagent
	// instance ID from the provider.
	AgentID string `json:"agent_id,omitempty"`
	// Delivery is DeliverySteer on a user item that joined a running turn
	// as a steer, and empty otherwise.
	Delivery string `json:"delivery,omitempty"`
	// SteerStatus marks a local receipt before the provider records a user
	// message. The provider's item with the same ID replaces this receipt.
	SteerStatus string `json:"steer_status,omitempty"`
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
	// Clipped is set when a text of the item was shortened to bound memory.
	// A HistoryPager reads the item whole.
	Clipped bool `json:"-"`
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

const (
	SteerAccepted     = "accepted"
	SteerNotDelivered = "not_delivered"
)

// DeliveryAutopilot marks a provider-generated continuation of the same turn.
const DeliveryAutopilot = "autopilot"

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
	// Native committed edits are bounded metadata. The exact completion event
	// identifies their immutable patch, fetched separately through ItemDiffReader.
	EditEventID        string     `json:"edit_event_id,omitempty"`
	FileEdits          []FileEdit `json:"file_edits,omitempty"`
	FileEditsTruncated bool       `json:"file_edits_truncated,omitempty"`
	// ExitCode is a shell command's exit code, when the provider reports
	// one; nil otherwise.
	ExitCode *int `json:"exit_code,omitempty"`
	// Declaration is display intent from a completed host tool call. It is
	// neither file provenance nor authority to open the path.
	Declaration *FileDeclaration `json:"declaration,omitempty"`
	// Tail is the newest output lines of a running shell call, oldest first,
	// at most 10, each at most 512 bytes; nil once the call ends.
	Tail []OutputLine `json:"tail,omitempty"`
}

// FileEdit is a native committed mutation, including edits from a failed call.
// Additions and Deletions are known only when DiffStatus is "available".
type FileEdit struct {
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	Additions  int    `json:"additions,omitempty"`
	Deletions  int    `json:"deletions,omitempty"`
	DiffStatus string `json:"diff_status"`
}

// OutputLine is one line of a running shell call's output.
type OutputLine struct {
	Text string `json:"text"`
	Err  bool   `json:"err,omitempty"` // stream "stderr"; stdout and terminal are false
}

// FileDeclaration contains only bounded display metadata. Opening still
// follows the current workdir resolver or exact-file temporary grant flow.
type FileDeclaration struct {
	ArtifactID string `json:"artifact_id"`
	Path       string `json:"path"`
	Title      string `json:"title,omitempty"`
	TypeHint   string `json:"type_hint,omitempty"`
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
	// Completion is the main agent's receipt from this foreground event
	// generation only. Consumers must also match its UserItemID.
	Completion *TaskCompletion `json:"completion,omitempty"`
	State      TurnState       `json:"state"`
	// Error is the sanitized provider error for TurnFailed.
	Error string `json:"error,omitempty"`
	// Model is the model the provider reported for this turn, when known.
	Model string `json:"model,omitempty"`
	// Reason is the provider's code for why a TurnCancelled turn stopped,
	// such as "autopilot_credit_limit"; "" when it gave none.
	Reason string `json:"reason,omitempty"`
}

// InteractionKind separates permission requests from questions.
type InteractionKind string

const (
	InteractionPermission InteractionKind = "permission"
	InteractionQuestion   InteractionKind = "question"
	InteractionPlanReview InteractionKind = "plan_review"
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
	Detail string      `json:"detail,omitempty"`
	Plan   *PlanReview `json:"plan,omitempty"`
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
	// Auto is set by the web service, never by a provider: yolo mode is
	// answering this pending request, so it does not wait for the user.
	Auto bool `json:"auto,omitempty"`
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
	Plan     *PlanAnswer `json:"plan,omitempty"`
	Decision string      `json:"decision,omitempty"`
	Answers  [][]string  `json:"answers,omitempty"`
	Reject   bool        `json:"reject,omitempty"`
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
	ParentToolCallID string `json:"parent_tool_call_id,omitempty"`
	// ParentAgentID is the subagent whose tool call spawned it, empty when
	// the main agent did: that call is in the parent's transcript.
	ParentAgentID string `json:"parent_agent_id,omitempty"`
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	// Background is set when it was launched in the background: its parent
	// tool call then only acknowledges the launch, and its result is its own
	// last message.
	Background bool           `json:"background,omitempty"`
	Status     SubagentStatus `json:"status"`
	// Error is the provider's reason for SubagentFailed.
	Error     string    `json:"error,omitempty"`
	StartedAt time.Time `json:"started_at,omitzero"`
	EndedAt   time.Time `json:"ended_at,omitzero"`
	// Runs lists its periods of work, oldest first, when the provider tracks
	// them: a reused subagent keeps its ID and gains a run per reuse. The
	// latest run carries Status and EndedAt. At most MaxSubagentRuns are
	// kept: the first and the newest.
	Runs []SubagentRun `json:"runs,omitempty"`
	// Tokens is the input and output tokens it consumed, over all runs.
	Tokens int64 `json:"tokens,omitempty"`
	// ToolCalls is the number of tool calls it made, over all runs.
	ToolCalls int64 `json:"tool_calls,omitempty"`
	// Retry is set while its current model call is being retried. It is
	// live only: the provider does not record retries.
	Retry *Retry `json:"retry,omitempty"`
	// Result is the start of the output its parent tool call recorded, when
	// a read record has it but not that tool call's item.
	Result string `json:"-"`
}

// SubagentRun is one period of work of a subagent. Status is the status
// the run ended with, or SubagentRunning; EndedAt is zero while it runs.
type SubagentRun struct {
	StartedAt time.Time      `json:"started_at,omitzero"`
	EndedAt   time.Time      `json:"ended_at,omitzero"`
	Status    SubagentStatus `json:"status"`
	// Trigger says who started the run: SubagentTriggerSpawn, -Agent or -User.
	Trigger string `json:"trigger"`
}

const (
	// SubagentTriggerSpawn is the first run, started by the spawning tool call.
	SubagentTriggerSpawn = "spawn"
	// SubagentTriggerAgent is a reuse the main agent started.
	SubagentTriggerAgent = "agent"
	// SubagentTriggerUser is a follow-up sent from UAM.
	SubagentTriggerUser = "user"

	MaxSubagentRuns = 50
	// SubagentRunBytes bounds one run's JSON size, for byte budgets.
	SubagentRunBytes = 144
)

// Snapshot returns a copy of sa that owns its Runs, the latest carrying sa's
// Status and EndedAt. Status changes reach the latest run through it.
func (sa Subagent) Snapshot() Subagent {
	if n := len(sa.Runs); n > 0 {
		sa.Runs = slices.Clone(sa.Runs)
		sa.Runs[n-1].Status, sa.Runs[n-1].EndedAt = sa.Status, sa.EndedAt
	}
	return sa
}

// StartRun ends the latest run as sa says and opens a running one at at.
// A record that ran without runs gets its first, spawn run back first. The
// caller sets the record's own fields.
func (sa *Subagent) StartRun(at time.Time, trigger string) {
	if len(sa.Runs) == 0 && sa.Status != "" {
		sa.Runs = []SubagentRun{{StartedAt: sa.StartedAt, Trigger: SubagentTriggerSpawn}}
	}
	runs := append(sa.Snapshot().Runs, SubagentRun{StartedAt: at, Status: SubagentRunning, Trigger: trigger})
	sa.Runs = CapSubagentRuns(runs)
}

// CapSubagentRuns keeps the first run and the newest, MaxSubagentRuns in all.
func CapSubagentRuns(runs []SubagentRun) []SubagentRun {
	if len(runs) <= MaxSubagentRuns {
		return runs
	}
	return append(runs[:1:1], runs[len(runs)-MaxSubagentRuns+1:]...)
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

// TodoList is the todo list the agents keep in the conversation.
type TodoList struct {
	// Known is false when the list could not be read.
	Known bool   `json:"known"`
	Todos []Todo `json:"todos"`
}

// Todo is one row of a TodoList, in the provider's order.
type Todo struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Status is pending, in_progress, done or blocked.
	Status string `json:"status"`
	// Note says why a blocked row is blocked; "" for the others.
	Note string `json:"note,omitempty"`
	// AgentID is the subagent that first wrote the row; "" for the main
	// agent or when that is not known.
	AgentID string `json:"agent_id,omitempty"`
	// ChangedAt is when its status last changed, as far as uam saw it;
	// zero when it was first seen on opening the conversation.
	ChangedAt time.Time `json:"changed_at,omitzero"`
}

// Todo statuses.
const (
	TodoPending    = "pending"
	TodoInProgress = "in_progress"
	TodoDone       = "done"
	TodoBlocked    = "blocked"
)

// FileDiff is one provider-recorded file change. Before/After hold full file
// text when available; Patch holds a unified diff when that is what the
// provider returns.
type FileDiff struct {
	// CountsUnknown distinguishes omitted/binary patches from zero changes.
	CountsUnknown bool   `json:"counts_unknown,omitempty"`
	Binary        bool   `json:"binary,omitempty"`
	Truncated     bool   `json:"truncated,omitempty"`
	OldPath       string `json:"old_path,omitempty"`
	Path          string `json:"path"`
	Status        string `json:"status,omitempty"`
	Additions     int    `json:"additions"`
	Deletions     int    `json:"deletions"`
	Before        string `json:"before,omitempty"`
	After         string `json:"after,omitempty"`
	Patch         string `json:"patch,omitempty"`
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
