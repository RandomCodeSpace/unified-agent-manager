package copilot

import (
	"bufio"
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

const (
	webPingInterval = 10 * time.Second
	webPingTimeout  = 10 * time.Second
	webStartTimeout = 30 * time.Second
	webStopTimeout  = 15 * time.Second
	webCheckTimeout = 10 * time.Second
	webTasksTimeout = 10 * time.Second
	maxToolText     = 64 << 10
	maxErrorText    = 512
	// Read-only history retains at most webReadBytes encoded events from
	// webReadPages newest pages. Older pages are drained to release the snapshot.
	webReadEvents = 1000
	webReadPages  = 64
	webReadBytes  = 16 << 20
)

var (
	errNoUser      = errors.New("no user is available to answer")
	errDeclined    = errors.New("the user declined to answer")
	cliVersionExpr = regexp.MustCompile(`\d+\.\d+\.\d+`)
)

// sdkClient is the part of the Copilot SDK client the web provider drives.
// Tests replace it with a fake so no CLI or model is involved.
type sdkClient interface {
	Start(ctx context.Context) error
	Stop() error
	ForceStop()
	Ping(ctx context.Context) error
	ListModels(ctx context.Context) ([]rpc.Model, error)
	// Quota reads the signed-in account's quota snapshots by type.
	Quota(ctx context.Context) (map[string]rpc.AccountQuotaSnapshot, error)
	CreateSession(ctx context.Context, cfg *copilot.SessionConfig) (sdkSession, error)
	ResumeSession(ctx context.Context, id string, cfg *copilot.ResumeSessionConfig) (sdkSession, error)
	// DeleteSession deletes a session and its stored data for good.
	DeleteSession(ctx context.Context, id string) error
	// ReadEvents reads one page of a session's persisted journal without
	// creating, resuming or activating the session.
	ReadEvents(ctx context.Context, req *rpc.SessionsReadPersistedEventsRequest) (*rpc.EventsReadResult, error)
	// ListSessions lists the sessions whose working directory is exactly
	// workdir.
	ListSessions(ctx context.Context, workdir string) ([]copilot.SessionMetadata, error)
	// CheckInUse returns the ids another process holds by a live in-use lock.
	CheckInUse(ctx context.Context, ids []string) ([]string, error)
}

// sdkSession is the part of a Copilot SDK session the web provider drives.
type sdkSession interface {
	ID() string
	// Send submits a message and returns the message ID the CLI assigned to
	// it.
	Send(ctx context.Context, msg copilot.MessageOptions) (string, error)
	// SendAndWait submits a message, waits for the session to go idle and
	// returns the text of the last assistant message.
	SendAndWait(ctx context.Context, msg copilot.MessageOptions) (string, error)
	// SetName names the session through the experimental session.name.set,
	// which also stops the CLI from naming it.
	SetName(ctx context.Context, name string) error
	// ListCommands lists the session's built-in commands and skills.
	ListCommands(ctx context.Context) ([]rpc.SlashCommandInfo, error)
	// InvokeCommand resolves a command; it starts no turn.
	InvokeCommand(ctx context.Context, name, input string) (rpc.SlashCommandInvocationResult, error)
	SwitchModel(ctx context.Context, req *rpc.ModelSwitchToRequest) (*rpc.ModelSwitchToResult, error)
	// AddProviders registers custom providers and models on the open
	// session through the experimental session.provider.add.
	AddProviders(ctx context.Context, providers []copilot.NamedProviderConfig, models []copilot.ProviderModelConfig) error
	SetEffort(ctx context.Context, effort string) error
	Abort(ctx context.Context) error
	CancelSubagent(ctx context.Context, agentID string) (bool, error)
	// ListTasks returns the tasks the CLI tracks, subagents included.
	ListTasks(ctx context.Context) ([]rpc.TaskInfo, error)
	// MessageSubagent sends a follow-up to one agent task.
	MessageSubagent(ctx context.Context, agentID, message string) (*rpc.TasksSendMessageResult, error)
	Events(ctx context.Context) ([]copilot.SessionEvent, error)
	// RespondPermission answers a pending permission request. It reports false
	// when the CLI no longer considered the request pending.
	RespondPermission(ctx context.Context, requestID string, decision rpc.PermissionDecision) (bool, error)
	Disconnect() error
}

// rejectedError marks a request the CLI answered with a JSON-RPC error: it
// reached the CLI and was refused, so nothing was accepted.
type rejectedError struct{ err error }

func (e rejectedError) Error() string { return e.err.Error() }
func (e rejectedError) Unwrap() error { return e.err }

type sdkClientAdapter struct{ c *copilot.Client }

// ImportSupported probes optional CLI methods without opening a session.
func (a sdkClientAdapter) ImportSupported(ctx context.Context) bool {
	if _, err := a.CheckInUse(ctx, []string{}); err != nil {
		return false
	}
	_, err := a.ReadEvents(ctx, &rpc.SessionsReadPersistedEventsRequest{SessionID: "00000000-0000-4000-8000-000000000000"})
	return err == nil || strings.Contains(err.Error(), "journal is unavailable")
}

func (a sdkClientAdapter) Start(ctx context.Context) error { return a.c.Start(ctx) }
func (a sdkClientAdapter) Stop() error                     { return a.c.Stop() }
func (a sdkClientAdapter) ForceStop()                      { a.c.ForceStop() }

func (a sdkClientAdapter) Ping(ctx context.Context) error {
	_, err := a.c.Ping(ctx, "")
	return err
}

// ListModels sends the same models.list request as Client.ListModels, which
// caches its answer until the CLI stops; an entitlement change must show up
// while the service runs.
func (a sdkClientAdapter) ListModels(ctx context.Context) ([]rpc.Model, error) {
	res, err := a.c.RPC.Models.List(ctx, &rpc.ModelsListRequest{})
	if err != nil {
		return nil, err
	}
	return res.Models, nil
}

// Quota sends the experimental account.getQuota request.
func (a sdkClientAdapter) Quota(ctx context.Context) (map[string]rpc.AccountQuotaSnapshot, error) {
	res, err := a.c.RPC.Account.GetQuota(ctx, &rpc.AccountGetQuotaRequest{})
	if err != nil {
		return nil, err
	}
	return res.QuotaSnapshots, nil
}

func (a sdkClientAdapter) CreateSession(ctx context.Context, cfg *copilot.SessionConfig) (sdkSession, error) {
	s, err := a.c.CreateSession(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return sdkSessionAdapter{s}, nil
}

func (a sdkClientAdapter) DeleteSession(ctx context.Context, id string) error {
	return a.c.DeleteSession(ctx, id)
}

func (a sdkClientAdapter) ResumeSession(ctx context.Context, id string, cfg *copilot.ResumeSessionConfig) (sdkSession, error) {
	s, err := a.c.ResumeSession(ctx, id, cfg)
	if err != nil {
		return nil, err
	}
	return sdkSessionAdapter{s}, nil
}

func (a sdkClientAdapter) ReadEvents(ctx context.Context, req *rpc.SessionsReadPersistedEventsRequest) (*rpc.EventsReadResult, error) {
	return a.c.RPC.Sessions.ReadPersistedEvents(ctx, req)
}

func (a sdkClientAdapter) ListSessions(ctx context.Context, workdir string) ([]copilot.SessionMetadata, error) {
	if workdir == "" {
		return a.c.ListSessions(ctx, nil)
	}
	return a.c.ListSessions(ctx, &copilot.SessionListFilter{WorkingDirectory: workdir})
}

func (a sdkClientAdapter) CheckInUse(ctx context.Context, ids []string) ([]string, error) {
	res, err := a.c.RPC.Sessions.CheckInUse(ctx, &rpc.SessionsCheckInUseRequest{SessionIDs: ids})
	if err != nil {
		return nil, err
	}
	return res.InUse, nil
}

type sdkSessionAdapter struct{ s *copilot.Session }

func (a sdkSessionAdapter) ID() string                      { return a.s.SessionID }
func (a sdkSessionAdapter) Abort(ctx context.Context) error { return a.s.Abort(ctx) }
func (a sdkSessionAdapter) Disconnect() error               { return a.s.Disconnect() }
func (a sdkSessionAdapter) Events(ctx context.Context) ([]copilot.SessionEvent, error) {
	return a.s.GetEvents(ctx)
}

func (a sdkSessionAdapter) CancelSubagent(ctx context.Context, agentID string) (bool, error) {
	result, err := a.s.RPC.Tasks.Cancel(ctx, &rpc.TasksCancelRequest{ID: agentID})
	if err != nil {
		return false, err
	}
	return result.Cancelled, nil
}

func (a sdkSessionAdapter) ListTasks(ctx context.Context) ([]rpc.TaskInfo, error) {
	result, err := a.s.RPC.Tasks.List(ctx)
	if err != nil {
		return nil, err
	}
	return result.Tasks, nil
}

func (a sdkSessionAdapter) MessageSubagent(ctx context.Context, agentID, message string) (*rpc.TasksSendMessageResult, error) {
	result, err := a.s.RPC.Tasks.SendMessage(ctx, &rpc.TasksSendMessageRequest{ID: agentID, Message: message})
	if err != nil && isRPCError(err) {
		return nil, rejectedError{err}
	}
	return result, err
}

func (a sdkSessionAdapter) SwitchModel(ctx context.Context, req *rpc.ModelSwitchToRequest) (*rpc.ModelSwitchToResult, error) {
	return a.s.RPC.Model.SwitchTo(ctx, req)
}

func (a sdkSessionAdapter) AddProviders(ctx context.Context, providers []copilot.NamedProviderConfig, models []copilot.ProviderModelConfig) error {
	_, err := a.s.RPC.Provider.Add(ctx, addProviders(providers, models))
	return err
}

func (a sdkSessionAdapter) SetEffort(ctx context.Context, effort string) error {
	_, err := a.s.RPC.Model.SetReasoningEffort(ctx, &rpc.ModelSetReasoningEffortRequest{ReasoningEffort: effort})
	return err
}

func (a sdkSessionAdapter) Send(ctx context.Context, msg copilot.MessageOptions) (string, error) {
	id, err := a.s.Send(ctx, msg)
	if err != nil && isRPCError(err) {
		return "", rejectedError{err}
	}
	return id, err
}

func (a sdkSessionAdapter) SendAndWait(ctx context.Context, msg copilot.MessageOptions) (string, error) {
	ev, err := a.s.SendAndWait(ctx, msg)
	if err != nil || ev == nil {
		return "", err
	}
	if d, ok := ev.Data.(*copilot.AssistantMessageData); ok {
		return d.Content, nil
	}
	return "", nil
}

func (a sdkSessionAdapter) SetName(ctx context.Context, name string) error {
	_, err := a.s.RPC.Name.Set(ctx, &rpc.NameSetRequest{Name: name})
	return err
}

func (a sdkSessionAdapter) ListCommands(ctx context.Context) ([]rpc.SlashCommandInfo, error) {
	res, err := a.s.RPC.Commands.List(ctx, &rpc.SessionCommandsListRequest{
		IncludeBuiltins: copilot.Bool(true), IncludeSkills: copilot.Bool(true), IncludeClientCommands: copilot.Bool(false),
	})
	if err != nil {
		return nil, err
	}
	return res.Commands, nil
}

func (a sdkSessionAdapter) InvokeCommand(ctx context.Context, name, input string) (rpc.SlashCommandInvocationResult, error) {
	return a.s.RPC.Commands.Invoke(ctx, &rpc.CommandsInvokeRequest{Name: name, Input: &input})
}

func (a sdkSessionAdapter) RespondPermission(ctx context.Context, requestID string, decision rpc.PermissionDecision) (bool, error) {
	res, err := a.s.RPC.Permissions.HandlePendingPermissionRequest(ctx, &rpc.PermissionDecisionRequest{RequestID: requestID, Result: decision})
	if err != nil {
		return false, err
	}
	return res.Success, nil
}

// isRPCError reports whether err carries the SDK's JSON-RPC error response
// type. The type lives in an internal SDK package, so it is matched by name.
func isRPCError(err error) bool {
	for ; err != nil; err = errors.Unwrap(err) {
		if fmt.Sprintf("%T", err) == "*jsonrpc2.Error" {
			return true
		}
	}
	return false
}

// webProvider drives the installed copilot CLI through the Copilot Go SDK. It
// owns one CLI process, started by the first Open and kept until Shutdown or
// until the watchdog finds it dead.
type webProvider struct {
	newClient func() (sdkClient, error)
	pingEvery time.Duration
	kick      chan struct{}

	mu              sync.Mutex
	client          sdkClient
	stop            chan struct{} // closed to end the current watchdog
	convs           map[*conversation]struct{}
	shut            bool
	importSupported bool
	importProbed    bool
	// customMu guards custom, the owner's custom models; it is never held
	// with mu, which a CLI start holds for long.
	customMu sync.Mutex
	custom   []agentapi.CustomModel
}

// NewWebProvider returns the Copilot integration for the web service.
func NewWebProvider() agentapi.Provider {
	return newWebProvider(newSDKClient, webPingInterval)
}

func newWebProvider(newClient func() (sdkClient, error), pingEvery time.Duration) *webProvider {
	return &webProvider{newClient: newClient, pingEvery: pingEvery, kick: make(chan struct{}, 1), convs: map[*conversation]struct{}{}}
}

// newSDKClient points the SDK at the installed CLI. Token, config directory
// and environment stay unset so the user's own login and ~/.copilot settings
// apply unchanged.
func newSDKClient() (sdkClient, error) {
	path, err := resolveCopilot()
	if err != nil {
		return nil, err
	}
	return sdkClientAdapter{copilot.NewClient(&copilot.ClientOptions{Connection: copilot.StdioConnection{Path: path}})}, nil
}

func resolveCopilot() (string, error) {
	path, err := exec.LookPath("copilot")
	if err != nil {
		return "", errors.New("GitHub Copilot CLI (copilot) is not installed or not on PATH")
	}
	if nodeScript(path) {
		if _, err := exec.LookPath("node"); err != nil {
			return "", fmt.Errorf("%s is a Node.js script but node is not on PATH", path)
		}
	}
	return path, nil
}

func nodeScript(path string) bool {
	f, err := os.Open(path) // #nosec G304 -- resolveCopilot supplies the service owner's PATH executable; only its bounded shebang is read.
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	line, _ := bufio.NewReader(io.LimitReader(f, 256)).ReadString('\n')
	return strings.HasPrefix(line, "#!") && strings.Contains(line, "node")
}

func (p *webProvider) Name() string        { return agentapi.ProviderCopilot }
func (p *webProvider) DisplayName() string { return "GitHub Copilot" }

func (p *webProvider) Capabilities() agentapi.Capabilities {
	p.mu.Lock()
	defer p.mu.Unlock()
	return agentapi.Capabilities{Cancel: true, ExecutionModes: true, Permissions: true, Questions: true, History: true, ContextSize: true, Usage: true, Titles: true, Import: p.importSupported}
}

func (p *webProvider) Check(ctx context.Context) error {
	path, err := resolveCopilot()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, webCheckTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version") // #nosec G204 -- resolveCopilot uses the service owner's PATH; fixed argument, no shell or request input.
	// The npm shim runs the real binary as a child that inherits the output
	// pipe; WaitDelay stops a timed-out check from waiting on that child.
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Errorf("copilot --version failed: %s", clip(displaytext.Sanitize(detail), maxErrorText))
	}
	if !cliVersionExpr.Match(out) {
		return fmt.Errorf("copilot --version printed no version: %s", clip(displaytext.Sanitize(string(out)), maxErrorText))
	}
	return nil
}

// Models lists the models the signed-in account can select: entries with no
// policy or an enabled one. Others are listed by the CLI but not selectable.
func (p *webProvider) Models(ctx context.Context) ([]agentapi.Model, error) {
	client, err := p.ensureStarted(ctx)
	if err != nil {
		return nil, err
	}
	models, err := client.ListModels(ctx)
	if err != nil {
		p.poke()
		return nil, fmt.Errorf("list copilot models: %s", errText(err))
	}
	out := []agentapi.Model{}
	for _, m := range models {
		if m.ID == "" || (m.Policy != nil && m.Policy.State != rpc.ModelPolicyStateEnabled) {
			continue
		}
		mo := agentapi.Model{ID: m.ID, Name: m.Name, Efforts: append([]string{}, m.SupportedReasoningEfforts...), ContextSizes: []agentapi.ContextSize{}, Media: media(m.Capabilities), CostTier: costTier(m.ModelPickerPriceCategory)}
		if m.Billing != nil {
			if d := m.Billing.DiscountPercent; d != nil && *d > 0 && *d <= 100 {
				mo.DiscountPercent = int(*d)
			}
			mo.Prices = prices(m.Billing.TokenPrices)
		}
		if m.ID == "auto" {
			mo.Efforts = []string{}
		}
		if m.ID != "auto" && m.Billing != nil && m.Billing.TokenPrices != nil {
			prices := m.Billing.TokenPrices
			if prices.MaxPromptTokens != nil && *prices.MaxPromptTokens > 0 {
				mo.ContextSizes = append(mo.ContextSizes, agentapi.ContextSize{ID: "default", Tokens: *prices.MaxPromptTokens})
			}
			if prices.LongContext != nil && prices.LongContext.MaxPromptTokens != nil && *prices.LongContext.MaxPromptTokens > 0 && len(mo.ContextSizes) > 0 {
				mo.ContextSizes = append(mo.ContextSizes, agentapi.ContextSize{ID: "long_context", Tokens: *prices.LongContext.MaxPromptTokens})
			}
		}
		out = append(out, mo)
	}
	return append(out, customCatalog(p.customModels())...), nil
}

// prices copies the catalog's token prices, preferring cacheReadPrice and
// maxPromptTokens over their deprecated names cachePrice and contextMax.
func prices(tp *rpc.ModelBillingTokenPrices) *agentapi.Prices {
	if tp == nil {
		return nil
	}
	p := &agentapi.Prices{TierPrices: tierPrices(tp.InputPrice, tp.OutputPrice, cmp.Or(tp.CacheReadPrice, tp.CachePrice), tp.CacheWritePrice, cmp.Or(tp.MaxPromptTokens, tp.ContextMax))}
	if tp.BatchSize != nil {
		p.BatchSize = *tp.BatchSize
	}
	if lc := tp.LongContext; lc != nil {
		long := tierPrices(lc.InputPrice, lc.OutputPrice, cmp.Or(lc.CacheReadPrice, lc.CachePrice), lc.CacheWritePrice, cmp.Or(lc.MaxPromptTokens, lc.ContextMax))
		p.LongContext = &long
	}
	return p
}

func tierPrices(input, output, cacheRead, cacheWrite *float64, maxPrompt *int64) agentapi.TierPrices {
	value := func(v *float64) *float64 {
		if v == nil {
			return nil
		}
		c := *v
		return &c
	}
	t := agentapi.TierPrices{Input: value(input), Output: value(output), CacheRead: value(cacheRead), CacheWrite: value(cacheWrite)}
	if maxPrompt != nil {
		t.MaxPromptTokens = *maxPrompt
	}
	return t
}

// costTier maps the catalog's relative cost tier; an unknown one is dropped.
func costTier(c *rpc.ModelPickerPriceCategory) string {
	if c == nil {
		return ""
	}
	switch *c {
	case rpc.ModelPickerPriceCategoryLow:
		return agentapi.CostLow
	case rpc.ModelPickerPriceCategoryMedium:
		return agentapi.CostMedium
	case rpc.ModelPickerPriceCategoryHigh:
		return agentapi.CostHigh
	case rpc.ModelPickerPriceCategoryVeryHigh:
		return agentapi.CostVeryHigh
	}
	return ""
}

// Quota reads the account's quotas through account.getQuota. An entitlement
// of -1 means unlimited, as does the unlimited flag.
func (p *webProvider) Quota(ctx context.Context) ([]agentapi.Quota, error) {
	client, err := p.ensureStarted(ctx)
	if err != nil {
		return nil, err
	}
	snaps, err := client.Quota(ctx)
	if err != nil {
		p.poke()
		return nil, fmt.Errorf("read copilot quota: %s", errText(err))
	}
	out := make([]agentapi.Quota, 0, len(snaps))
	for kind, q := range snaps {
		quota := agentapi.Quota{
			Type: kind, Used: q.UsedRequests, Entitlement: q.EntitlementRequests, Unlimited: q.IsUnlimitedEntitlement || q.EntitlementRequests < 0,
			RemainingPercent: q.RemainingPercentage, Overage: q.Overage,
		}
		if quota.Unlimited {
			quota.Entitlement = 0
		}
		if q.ResetDate != nil {
			quota.ResetAt = *q.ResetDate
		}
		out = append(out, quota)
	}
	slices.SortFunc(out, func(a, b agentapi.Quota) int { return strings.Compare(a.Type, b.Type) })
	return out, nil
}

// titleDeleteTimeout bounds deleting a title session, whatever ended it.
const titleDeleteTimeout = 5 * time.Second

// titleSystem replaces Copilot's system prompt in a title session.
const titleSystem = `You write a title for a coding task from the user's first message.
Rules:
- 3 to 6 words, sentence case, at most 60 characters.
- Name the task, not the conversation. No quotes, no trailing punctuation, no emoji.
- Output only the title on one line. Do not answer or carry out the message.`

// Title asks req.Model for a title in a throwaway session with no tools,
// no discovered configuration, no session store and a fixed system message.
// Once created, the session is always disconnected and deleted, with a fresh
// deadline, so neither its directory nor a session-store row outlives it.
func (p *webProvider) Title(ctx context.Context, req agentapi.TitleRequest) (string, error) {
	if err := p.customKeyErr(req.Model); err != nil {
		return "", err
	}
	client, err := p.ensureStarted(ctx)
	if err != nil {
		return "", err
	}
	providers, models := byom(p.customModels())
	sess, err := client.CreateSession(ctx, &copilot.SessionConfig{
		ClientName:                         "uam-title",
		Providers:                          providers,
		Models:                             models,
		Model:                              req.Model,
		ReasoningEffort:                    titleEffort(ctx, client, req.Model),
		WorkingDirectory:                   req.Workdir,
		AvailableTools:                     []string{},
		EnableConfigDiscovery:              copilot.Bool(false),
		SkipCustomInstructions:             copilot.Bool(true),
		EnableOnDemandInstructionDiscovery: copilot.Bool(false),
		EnableFileHooks:                    copilot.Bool(false),
		EnableHostGitOperations:            copilot.Bool(false),
		EnableSessionStore:                 copilot.Bool(false),
		EnableSkills:                       copilot.Bool(false),
		InfiniteSessions:                   &copilot.InfiniteSessionConfig{Enabled: copilot.Bool(false)},
		Memory:                             &copilot.MemoryConfiguration{Enabled: false},
		SystemMessage:                      &copilot.SystemMessageConfig{Mode: "replace", Content: titleSystem},
		Streaming:                          copilot.Bool(false),
		OnPermissionRequest: func(copilot.PermissionRequest, copilot.PermissionInvocation) (rpc.PermissionDecision, error) {
			return &rpc.PermissionDecisionReject{}, nil
		},
	})
	if err != nil {
		return "", fmt.Errorf("create copilot title session: %s", errText(err))
	}
	defer func() {
		id := sess.ID()
		if err := sess.Disconnect(); err != nil {
			log.Info("disconnect copilot title session failed", "conversation", id, "error", err)
		}
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), titleDeleteTimeout)
		defer cancel()
		if err := client.DeleteSession(dctx, id); err != nil {
			log.Warn("delete copilot title session failed", "conversation", id, "error", err)
		}
	}()
	reply, err := sess.SendAndWait(ctx, copilot.MessageOptions{Prompt: "<user_message>\n" + req.Text + "\n</user_message>"})
	if err != nil {
		return "", fmt.Errorf("copilot title: %s", errText(err))
	}
	return reply, nil
}

// titleEffort is the lowest reasoning effort model supports, or "" for its
// default when it lists none or the catalog cannot be read.
func titleEffort(ctx context.Context, client sdkClient, model string) string {
	models, err := client.ListModels(ctx)
	if err != nil {
		return ""
	}
	for _, m := range models {
		if m.ID != model {
			continue
		}
		for _, effort := range []string{"none", "minimal", "low"} {
			if slices.Contains(m.SupportedReasoningEfforts, effort) {
				return effort
			}
		}
	}
	return ""
}

// media is a model's upload gate from the catalog: images need
// supports.vision, PDFs application/pdf among the vision media types. A
// model that reports neither (auto) gets no gate.
func media(c rpc.ModelCapabilities) *agentapi.Media {
	var vision *bool
	if c.Supports != nil {
		vision = c.Supports.Vision
	}
	var limits *rpc.ModelCapabilitiesLimitsVision
	if c.Limits != nil {
		limits = c.Limits.Vision
	}
	if vision == nil && limits == nil {
		return nil
	}
	md := &agentapi.Media{Images: vision != nil && *vision}
	if limits != nil {
		md.MaxImages = int(limits.MaxPromptImages)
		md.Types = slices.Clone(limits.SupportedMediaTypes)
		md.PDF = slices.Contains(limits.SupportedMediaTypes, "application/pdf")
	}
	return md
}

func (p *webProvider) Open(ctx context.Context, req agentapi.OpenRequest) (agentapi.Conversation, error) {
	if req.Events == nil {
		return nil, errors.New("copilot: OpenRequest.Events is required")
	}
	if req.ConversationID == "" {
		if err := p.customKeyErr(req.Model); err != nil {
			return nil, err
		}
	}
	client, err := p.ensureStarted(ctx)
	if err != nil {
		return nil, err
	}
	c := &conversation{
		p: p, client: client, sink: req.Events, pending: map[string]*interaction{}, tr: newTranscript(), subs: newSubagentLog(),
		seen: map[string]bool{}, watch: map[string]time.Time{},
		reportEmptyTasks: req.ConversationID != "",
	}
	if req.ConversationID == "" {
		c.selected = req.Model
	}
	// A resumed session keeps its selected model but not its custom
	// models, so every open supplies them.
	custom := p.customModels()
	providers, models := byom(custom)
	c.byom = newRegistered(custom)
	// Permission requests are answered through the pending-permission RPC
	// with the request id from the permission.requested event; the SDK
	// callback only registers this client as the one that decides.
	deferPermission := func(copilot.PermissionRequest, copilot.PermissionInvocation) (rpc.PermissionDecision, error) {
		return &rpc.PermissionDecisionNoResult{}, nil
	}
	var sess sdkSession
	if req.ConversationID == "" {
		sess, err = client.CreateSession(ctx, &copilot.SessionConfig{
			SessionID:             req.SessionID,
			WorkingDirectory:      req.Workdir,
			Model:                 req.Model,
			ReasoningEffort:       req.Effort,
			ContextTier:           copilot.ContextTier(req.ContextSize),
			Providers:             providers,
			Models:                models,
			Streaming:             copilot.Bool(true),
			OnPermissionRequest:   deferPermission,
			OnUserInputRequest:    c.askUser,
			OnExitPlanModeRequest: refusePlanExit,
			OnEvent:               c.onEvent,
			// Discovery loads what the terminal CLI loads for this directory:
			// skills, project agents, custom instructions, MCP servers and
			// hooks. The owner turned it on for web Tasks (#176).
			EnableConfigDiscovery: copilot.Bool(true),
		})
	} else {
		sess, err = client.ResumeSession(ctx, req.ConversationID, &copilot.ResumeSessionConfig{
			WorkingDirectory: req.Workdir,
			Providers:        providers,
			Models:           models,
			Streaming:        copilot.Bool(true),
			// Explicit false: nil keeps the runtime default, false treats tool
			// calls and prompts pending at the last suspend as interrupted.
			ContinuePendingWork:   copilot.Bool(false),
			OnPermissionRequest:   deferPermission,
			OnUserInputRequest:    c.askUser,
			OnExitPlanModeRequest: refusePlanExit,
			OnEvent:               c.onEvent,
			// Resumed Tasks discover the same configuration as new ones.
			EnableConfigDiscovery: copilot.Bool(true),
		})
	}
	if err != nil {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()
		if req.ConversationID != "" && strings.Contains(err.Error(), "Session not found") {
			return nil, fmt.Errorf("%w: %s", agentapi.ErrConversationNotFound, req.ConversationID)
		}
		p.poke()
		return nil, fmt.Errorf("open copilot conversation: %s", errText(err))
	}
	// Under mu: a subagent event may already start a task-list read.
	c.mu.Lock()
	c.sess, c.id = sess, sess.ID()
	if req.ConversationID != "" || c.relist {
		c.relist = false
		c.checkTasksLocked()
	}
	c.mu.Unlock()
	if !p.track(c) {
		_ = c.Close(ctx)
		return nil, agentapi.ErrClosed
	}
	c.control.Lock()
	c.refreshExecution(ctx)
	c.control.Unlock()
	return c, nil
}

// ReadHistory reads the conversation's persisted journal through the
// experimental sessions.readPersistedEvents, which neither resumes nor locks
// the session: nothing is written, no hook or MCP server starts, and a
// terminal resuming it meanwhile sees no other holder. Measured against CLI
// 1.0.88 (#193), it returns the same transcript as a resume. The newest
// webReadPages pages of a larger journal are kept.
func (p *webProvider) ReadHistory(ctx context.Context, req agentapi.ReadRequest) (agentapi.History, error) {
	if req.ConversationID == "" {
		return agentapi.History{}, errors.New("copilot: ReadRequest.ConversationID is required")
	}
	client, err := p.ensureStarted(ctx)
	if err != nil {
		return agentapi.History{}, err
	}
	// Newest first, so a journal cut at webReadPages keeps its latest turns.
	backward, max := rpc.EventsReadDirectionBackward, int64(webReadEvents)
	read := &rpc.SessionsReadPersistedEventsRequest{SessionID: req.ConversationID, Direction: &backward, Max: &max}
	var pages [][]copilot.SessionEvent
	truncated := false
	bytesKept := 0
	model := ""
	for {
		res, err := client.ReadEvents(ctx, read)
		switch {
		case err != nil && strings.Contains(err.Error(), "journal is unavailable"):
			return agentapi.History{}, fmt.Errorf("%w: %s", agentapi.ErrConversationNotFound, req.ConversationID)
		case err != nil:
			p.poke()
			return agentapi.History{}, fmt.Errorf("read copilot history: %s", errText(err))
		case res == nil || res.CursorStatus == rpc.EventsCursorStatusExpired:
			return agentapi.History{}, errors.New("read copilot history: the journal changed while it was read")
		}
		// The last model selection may predate the retained transcript. Keep
		// it while draining older pages so import preserves the model.
		for i := len(res.Events) - 1; i >= 0 && model == ""; i-- {
			if agentOf(res.Events[i]) == "" {
				model = recordedModel(res.Events[i])
			}
		}
		if !truncated {
			start := len(res.Events)
			for start > 0 {
				encoded, err := json.Marshal(res.Events[start-1])
				if err != nil {
					return agentapi.History{}, fmt.Errorf("measure copilot history: %w", err)
				}
				if bytesKept+len(encoded) > webReadBytes {
					truncated = true
					break
				}
				bytesKept += len(encoded)
				start--
			}
			pages = append(pages, slices.Clone(res.Events[start:]))
			if len(pages) == webReadPages && res.HasMore {
				truncated = true
			}
		}
		if !res.HasMore {
			break
		}
		// Finish a truncated snapshot without retaining older pages. The SDK
		// has no release-cursor API; completion releases its pinned journal.
		cursor := res.Cursor
		read = &rpc.SessionsReadPersistedEventsRequest{SessionID: req.ConversationID, Cursor: &cursor, Direction: &backward, Max: &max}
	}
	var evs []copilot.SessionEvent
	for i := len(pages) - 1; i >= 0; i-- {
		evs = append(evs, pages[i]...)
	}
	recorded := history(evs)
	recorded.Model = model
	recorded.Truncated = truncated
	return recorded, nil
}

// Previous lists the local sessions recorded with exactly workdir as their
// working directory, newest first. A session is listed once it has its first
// message.
func (p *webProvider) Previous(ctx context.Context, workdir string) ([]agentapi.PreviousConversation, error) {
	client, err := p.ensureStarted(ctx)
	if err != nil {
		return nil, err
	}
	list, err := client.ListSessions(ctx, workdir)
	if err != nil {
		p.poke()
		return nil, fmt.Errorf("list copilot sessions: %s", errText(err))
	}
	out := []agentapi.PreviousConversation{}
	for _, s := range list {
		// A remote session has no local journal to read.
		if s.IsRemote || s.Context == nil || (workdir != "" && s.Context.WorkingDirectory != workdir) {
			continue
		}
		c := agentapi.PreviousConversation{ID: s.SessionID, Workdir: s.Context.WorkingDirectory, CreatedAt: s.StartTime, UpdatedAt: s.ModifiedTime}
		if s.Summary != nil {
			c.Title = *s.Summary
		}
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b agentapi.PreviousConversation) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	return out, nil
}

// InUse reports which of ids another process holds, through the
// experimental sessions.checkInUse: live in-use locks on this host under the
// same COPILOT_HOME. The CLI this provider runs is not reported.
func (p *webProvider) InUse(ctx context.Context, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	client, err := p.ensureStarted(ctx)
	if err != nil {
		return nil, err
	}
	held, err := client.CheckInUse(ctx, ids)
	if err != nil {
		p.poke()
		return nil, fmt.Errorf("check copilot sessions in use: %s", errText(err))
	}
	return held, nil
}

func (p *webProvider) ensureStarted(ctx context.Context) (sdkClient, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.shut {
		return nil, agentapi.ErrClosed
	}
	if p.client != nil {
		return p.client, nil
	}
	c, err := p.newClient()
	if err != nil {
		return nil, err
	}
	// ctx bounds only the start; the process itself is not tied to it.
	sctx, cancel := context.WithTimeout(ctx, webStartTimeout)
	defer cancel()
	if err := c.Start(sctx); err != nil {
		c.ForceStop()
		return nil, fmt.Errorf("start copilot CLI: %s", errText(err))
	}
	if !p.importProbed {
		if probe, ok := c.(interface{ ImportSupported(context.Context) bool }); ok {
			probeCtx, probeCancel := context.WithTimeout(ctx, 5*time.Second)
			p.importSupported = probe.ImportSupported(probeCtx)
			probeCancel()
		}
		p.importProbed = true
	}
	p.client, p.stop = c, make(chan struct{})
	go p.watch(c, p.stop) // #nosec G118 -- provider-owned watchdog outlives requests; Shutdown/fail closes stop and each ping has a timeout.
	return c, nil
}

// watch pings the CLI until stop closes. The SDK reports neither a CLI exit
// nor a hang, so a failed ping is the only signal that the runtime is gone.
func (p *webProvider) watch(c sdkClient, stop <-chan struct{}) {
	t := time.NewTicker(p.pingEvery)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		case <-p.kick:
		}
		ctx, cancel := context.WithTimeout(context.Background(), webPingTimeout)
		err := c.Ping(ctx)
		cancel()
		if err != nil {
			p.fail(c, "Copilot CLI stopped: "+exitText(err))
			return
		}
	}
}

// poke asks the watchdog for an immediate ping after an unexpected error.
func (p *webProvider) poke() {
	select {
	case p.kick <- struct{}{}:
	default:
	}
}

// fail ends every conversation on a dead client and resets the provider so a
// later Open starts a fresh CLI. Nothing is resent or reopened.
func (p *webProvider) fail(c sdkClient, reason string) {
	p.mu.Lock()
	if p.client != c {
		p.mu.Unlock()
		return
	}
	p.client = nil
	close(p.stop)
	p.stop = nil
	convs := p.takeConvs()
	p.mu.Unlock()
	for _, conv := range convs {
		conv.exit(reason)
	}
	c.ForceStop()
}

func (p *webProvider) takeConvs() []*conversation {
	convs := make([]*conversation, 0, len(p.convs))
	for c := range p.convs {
		convs = append(convs, c)
	}
	clear(p.convs)
	return convs
}

func (p *webProvider) track(c *conversation) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.shut || p.client != c.client {
		return false
	}
	p.convs[c] = struct{}{}
	return true
}

func (p *webProvider) forget(c *conversation) {
	p.mu.Lock()
	delete(p.convs, c)
	p.mu.Unlock()
}

func (p *webProvider) Shutdown(ctx context.Context) error {
	p.mu.Lock()
	p.shut = true
	convs := p.takeConvs()
	client := p.client
	p.client = nil
	if p.stop != nil {
		close(p.stop)
		p.stop = nil
	}
	p.mu.Unlock()
	var errs []error
	for _, c := range convs {
		if err := c.Close(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if client != nil {
		if err := stopClient(ctx, client); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// stopClient stops the CLI gracefully and kills it when that takes too long.
// Stop kills the process even when it reports an error.
func stopClient(ctx context.Context, c sdkClient) error {
	ctx, cancel := context.WithTimeout(ctx, webStopTimeout)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Stop() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("stop copilot CLI: %s", errText(err))
		}
		return nil
	case <-ctx.Done():
		c.ForceStop()
		return fmt.Errorf("stop copilot CLI: %w", ctx.Err())
	}
}

// conversation is one open Copilot session. Every Emit happens under mu and
// only while the conversation is open, so nothing is delivered after Close.
type conversation struct {
	p      *webProvider
	client sdkClient
	sess   sdkSession
	id     string
	sink   agentapi.EventSink

	mu      sync.Mutex
	closed  bool
	pending map[string]*interaction
	// questions pairs user_input.requested events with ask_user callbacks.
	questions        questionLinks
	stoppedSubagents map[string]bool
	tr               *transcript
	subs             *subagentLog
	turnErr          string
	idles            int
	// Once the provider reports foreground idle, session.idle is only its
	// broader background-work boundary and must not end another turn.
	assistantIdleSeen bool
	autopilotTurn     bool
	foregroundIdle    bool
	// turnRunning is set from a foreground turn's start until its end. Send
	// refuses a prompt while it is: the CLI holds any prompt sent during a
	// turn until session.idle, which never comes while a background shell runs.
	turnRunning bool
	// idleUnresolved marks a main-agent assistant.idle seen while the mode
	// was autopilot or unknown. The CLI withholds session.idle, and with it
	// the autopilot continuation, while background work runs, so a read
	// showing a non-autopilot mode or a running attached shell ends the turn
	// instead.
	idleUnresolved    bool
	backgroundTasks   *agentapi.BackgroundTasks
	execution         *agentapi.ExecutionState
	executionRevision uint64
	control           sync.Mutex
	reportEmptyTasks  bool
	// turnModel is the model of the turn's latest main-agent model call.
	turnModel string
	// byom is what the session has of the custom models.
	byom registered
	// selected is the model this client last selected: at create or by
	// SetModel; "" until then on a resumed session.
	selected string
	// usage is the latest main-agent context report, kept so a model call's
	// cache report can be sent with it.
	usage agentapi.Context
	// nanoAIU is the conversation's cost so far in nano-AI units: the
	// session total the CLI last recorded plus each model call reported
	// since. usageSent is what was last emitted.
	nanoAIU, usageSent float64
	// steers includes sends in flight so idle can record their outcome before
	// the CLI returns the message ID. Accepted, unused steers remain here.
	steers []*steer
	// steering counts Steer calls in flight. While one is, seen records the
	// main-agent user messages the CLI used, because a steer can be used
	// before the Send that carried it returns its message ID.
	steering int
	seen     map[string]bool
	// watch holds the agents only session.tasks.list can settle: a finished
	// one that may take follow-ups, and one running a follow-up, keyed to the
	// time the follow-up was sent. listing is set while a read runs; relist
	// asks it for one more.
	watch           map[string]time.Time
	listing, relist bool
	// taskRPC orders task-list reads and follow-up sends, so a list read from
	// before a send never overwrites the follow-up it started.
	taskRPC sync.Mutex
}

type steer struct {
	id, prompt string
	state      agentapi.TurnState
	at         time.Time
}

type interaction struct {
	agentapi.Interaction
	decisions map[string]rpc.PermissionDecision // permissions: option id -> decision
	reply     chan userReply                    // questions: releases the blocked SDK handler
	answering bool                              // a permission answer is in flight
}

type userReply struct {
	resp copilot.UserInputResponse
	err  error
}

// questionLinks pairs each user_input.requested event with the ask_user
// callback for the same question. The callback carries the question and
// choices only; the event carries the same question with its tool call and
// agent. Neither names the other, so they pair by question text in arrival
// order, whichever side comes first. Every entry is bounded and expires:
// the two sides of one question arrive within moments of each other.
type questionLinks struct {
	// events are the events without a callback yet, by question text,
	// oldest first.
	events map[string][]questionEvent
	// waiting are the pending questions without an event yet, oldest first.
	waiting []*interaction
	// spent counts, by question text, the questions that were answered
	// before their event came, with the time of the newest, so a late event
	// is dropped rather than attached to the next question with that text.
	spent map[string]spentQuestions
}

type questionEvent struct {
	toolCallID, agentID string
	at                  time.Time
}

type spentQuestions struct {
	n  int
	at time.Time
}

// maxQuestionLinks bounds each side of questionLinks; questionLinkTTL is how
// long one side waits for the other. A question past either shows without
// its tool call.
const (
	maxQuestionLinks = 16
	questionLinkTTL  = 30 * time.Second
)

// linkQuestionLocked handles a user_input.requested event: it links the
// oldest waiting question with that text, which is emitted again with the
// link and its agent, or is dropped when that question was already answered,
// or waits for its callback.
func (c *conversation) linkQuestionLocked(d *rpc.UserInputRequestedData, agentID string, now time.Time) {
	if d.ToolCallID == nil {
		return
	}
	id := strings.TrimSpace(*d.ToolCallID)
	if id == "" {
		return
	}
	q := &c.questions
	q.expire(now)
	// An older callback may already be answered while the next same-text
	// question waits. Consume that older event before matching a waiter.
	if sp, ok := q.spent[d.Question]; ok {
		if sp.n--; sp.n == 0 {
			delete(q.spent, d.Question)
		} else {
			q.spent[d.Question] = sp
		}
		return
	}
	for i, in := range q.waiting {
		if in.Questions[0].Text != d.Question {
			continue
		}
		q.waiting = slices.Delete(q.waiting, i, i+1)
		in.ToolCallID = id
		if in.AgentID == "" {
			in.AgentID = agentID
		}
		c.emitInteractionLocked(in)
		return
	}
	if q.events == nil {
		q.events = map[string][]questionEvent{}
	}
	if q.count() >= maxQuestionLinks {
		q.dropOldestEvent()
	}
	q.events[d.Question] = append(q.events[d.Question], questionEvent{toolCallID: id, agentID: agentID, at: now})
}

// askedLocked gives a new question its event's tool call and agent when the
// event came first; otherwise the question waits for it.
func (c *conversation) askedLocked(in *interaction, now time.Time) {
	q := &c.questions
	q.expire(now)
	text := in.Questions[0].Text
	if evs := q.events[text]; len(evs) > 0 {
		in.ToolCallID, in.AgentID = evs[0].toolCallID, evs[0].agentID
		if len(evs) == 1 {
			delete(q.events, text)
		} else {
			q.events[text] = evs[1:]
		}
		return
	}
	if len(q.waiting) >= maxQuestionLinks {
		q.waiting = q.waiting[1:]
	}
	q.waiting = append(q.waiting, in)
}

// settledLocked forgets a question that ended. One still waiting for its
// event is counted as spent, so that event links nothing when it comes.
func (c *conversation) settledLocked(in *interaction, now time.Time) {
	q := &c.questions
	i := slices.Index(q.waiting, in)
	if i < 0 {
		return
	}
	q.waiting = slices.Delete(q.waiting, i, i+1)
	if q.spent == nil {
		q.spent = map[string]spentQuestions{}
	}
	if len(q.spent) >= maxQuestionLinks {
		clear(q.spent)
	}
	sp := q.spent[in.Questions[0].Text]
	q.spent[in.Questions[0].Text] = spentQuestions{n: sp.n + 1, at: now}
}

func (q *questionLinks) count() int {
	n := 0
	for _, evs := range q.events {
		n += len(evs)
	}
	return n
}

func (q *questionLinks) dropOldestEvent() {
	var oldest string
	var at time.Time
	for text, evs := range q.events {
		if oldest == "" || evs[0].at.Before(at) {
			oldest, at = text, evs[0].at
		}
	}
	if len(q.events[oldest]) == 1 {
		delete(q.events, oldest)
	} else {
		q.events[oldest] = q.events[oldest][1:]
	}
}

// expire drops the events and spent counts older than questionLinkTTL. A
// waiting question stays as long as it is pending: the user may take long
// to answer, and its event, if it comes at all, comes at once.
func (q *questionLinks) expire(now time.Time) {
	for text, evs := range q.events {
		evs = slices.DeleteFunc(evs, func(e questionEvent) bool { return now.Sub(e.at) > questionLinkTTL })
		if len(evs) == 0 {
			delete(q.events, text)
		} else {
			q.events[text] = evs
		}
	}
	for text, sp := range q.spent {
		if now.Sub(sp.at) > questionLinkTTL {
			delete(q.spent, text)
		}
	}
}

func (c *conversation) ID() string { return c.id }

func (c *conversation) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *conversation) emitLocked(e agentapi.Event) {
	if !c.closed {
		c.sink.Emit(e)
	}
}

func (c *conversation) emitInteractionLocked(in *interaction) {
	v := in.Interaction
	c.emitLocked(agentapi.Event{Kind: agentapi.EventInteraction, Interaction: &v})
}

func (c *conversation) History(ctx context.Context) (agentapi.History, error) {
	if c.isClosed() {
		return agentapi.History{}, agentapi.ErrClosed
	}
	evs, err := c.sess.Events(ctx)
	if err != nil {
		c.p.poke()
		return agentapi.History{}, fmt.Errorf("copilot history: %s", errText(err))
	}
	// Reconcile cancelled agents before a resumed CLI can replay abandoned
	// permission requests. History does not replace newer terminal live state.
	c.mu.Lock()
	for _, ev := range evs {
		c.subs.apply(ev)
	}
	for _, sa := range c.subs.byID {
		if sa.Status == agentapi.SubagentCancelled {
			c.expireSubagentLocked(sa.ID)
		}
	}
	c.mu.Unlock()
	recorded := history(evs)
	// Model-call reports are not recorded; the CLI's session total is. Live
	// calls after the last recorded total keep the higher live figure.
	if total, ok := recordedTotal(evs); ok {
		c.mu.Lock()
		c.nanoAIU = max(c.nanoAIU, total)
		recorded.Usage = &agentapi.Usage{AIUnits: c.nanoAIU / nanoPerUnit}
		c.mu.Unlock()
	}
	c.restoreIdle(ctx, recorded.Subagents)
	return recorded, nil
}

// restoreIdle reads the task list once for a reopened conversation's
// completed subagents. Recorded events never say that one still takes
// follow-ups; only the live list does.
func (c *conversation) restoreIdle(ctx context.Context, subs []agentapi.Subagent) {
	if !slices.ContainsFunc(subs, func(sa agentapi.Subagent) bool { return sa.Status == agentapi.SubagentCompleted }) {
		return
	}
	c.taskRPC.Lock()
	defer c.taskRPC.Unlock()
	tasks, err := c.sess.ListTasks(ctx)
	if err != nil {
		c.p.poke()
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range subs {
		if sa := c.subs.byID[subs[i].ID]; sa != nil && sa.Status == agentapi.SubagentCompleted &&
			subs[i].Status == agentapi.SubagentCompleted && takesFollowUps(agentTask(tasks, sa.ID)) {
			sa.Status, subs[i].Status = agentapi.SubagentIdle, agentapi.SubagentIdle
		}
	}
}

// SetModel changes all three settings between turns and checks the runtime's
// result before the manager records success.
func (c *conversation) SetModel(ctx context.Context, model, effort, contextSize string) error {
	if c.isClosed() {
		return agentapi.ErrClosed
	}
	if err := c.p.customKeyErr(model); err != nil {
		return err
	}
	if err := c.registerCustom(ctx, model); err != nil {
		return err
	}
	tier := rpc.ContextTier(contextSize)
	req := &rpc.ModelSwitchToRequest{ModelID: model, ContextTier: &tier, RunCompactionPreflight: copilot.Bool(true)}
	if effort != "" {
		req.ReasoningEffort = &effort
	}
	res, err := c.sess.SwitchModel(ctx, req)
	if err != nil {
		c.p.poke()
		// Only a JSON-RPC rejection proves that nothing changed.
		if !isRPCError(err) {
			return c.selectionUncertain(ctx, fmt.Errorf("model switch outcome is unknown: %s", errText(err)))
		}
		return fmt.Errorf("copilot model switch: %s", errText(err))
	}
	if res == nil {
		return c.selectionUncertain(ctx, errors.New("model switch returned no result"))
	}
	status := ""
	if res.Status != nil {
		status = *res.Status
	}
	if status == "confirmation_required" || status == "cancelled" {
		return fmt.Errorf("copilot model switch %s; the selection was not applied", status)
	}
	if (res.Deferred != nil && *res.Deferred) || status != "applied" || res.PersistenceError != nil {
		return c.selectionUncertain(ctx, fmt.Errorf("model switch did not confirm a durable immediate selection (status %q)", status))
	}
	if res.ModelID != nil && *res.ModelID != model {
		return c.selectionUncertain(ctx, errors.New("model switch returned a different model"))
	}
	if state := res.ModelState; state != nil {
		if (state.ModelID != nil && *state.ModelID != model) || (state.ContextTier != nil && *state.ContextTier != tier) || (effort != "" && (state.ReasoningEffort == nil || *state.ReasoningEffort != effort)) {
			return c.selectionUncertain(ctx, errors.New("model switch returned different settings"))
		}
		if contextSize == "long_context" && state.ContextTier == nil {
			return c.selectionUncertain(ctx, errors.New("model switch did not confirm the context size"))
		}
	} else if contextSize == "long_context" || effort != "" {
		return c.selectionUncertain(ctx, errors.New("model switch did not report the selected settings"))
	}
	if effort == "" {
		// switchTo rejects an empty effort, while omitting it preserves the
		// current effort on a same-model switch. This RPC explicitly resets it.
		if err := c.sess.SetEffort(ctx, ""); err != nil {
			return c.selectionUncertain(ctx, fmt.Errorf("model changed but resetting effort failed: %s", errText(err)))
		}
	}
	// The old report describes the old selection; wait for a fresh one.
	c.mu.Lock()
	c.usage = agentapi.Context{}
	c.selected = model
	c.mu.Unlock()
	return nil
}

// An uncertain or partial switch must not let the next prompt run with the
// old displayed settings. Reopening reapplies the Task's durable selection.
func (c *conversation) selectionUncertain(ctx context.Context, err error) error {
	reason := clip(displaytext.Sanitize(err.Error()), maxErrorText)
	c.mu.Lock()
	if !c.closed {
		c.emitLocked(agentapi.Event{Kind: agentapi.EventExit, Error: reason})
	}
	c.mu.Unlock()
	_ = c.Close(ctx)
	return fmt.Errorf("copilot model settings: %s", reason)
}

// SetTitle names the session with session.name.set. The CLI records the name
// as set by the user, so neither it nor the terminal CLI renames it later.
func (c *conversation) SetTitle(ctx context.Context, title string) error {
	if c.isClosed() {
		return agentapi.ErrClosed
	}
	if err := c.sess.SetName(ctx, title); err != nil {
		c.p.poke()
		return fmt.Errorf("copilot rename: %s", errText(err))
	}
	return nil
}

// Send starts a turn, and refuses with ErrBusy while one is running. It sends
// no mode: the CLI then delivers the prompt at once whenever its main agent is
// idle, background shells or not. An explicit "enqueue", and any prompt sent
// during a turn, is held until session.idle, which the CLI withholds while a
// background shell runs. Referenced files go as file and directory
// attachments with their absolute paths.
func (c *conversation) Send(ctx context.Context, prompt agentapi.Prompt) error {
	return c.send(ctx, copilot.MessageOptions{Prompt: prompt.Text, Attachments: attachments(prompt)})
}

// attachments maps a prompt's references to Copilot attachments. Uploads go
// inline as blobs, except one with a named copy on the host (a PDF), which
// goes as that file: the CLI passes a document to the model natively only
// where the model client supports it, and otherwise gives the agent the
// file's path, which a blob does not have.
func attachments(p agentapi.Prompt) []copilot.Attachment {
	var out []copilot.Attachment
	for _, f := range p.Files {
		if f.Dir {
			out = append(out, &rpc.AttachmentDirectory{Path: f.Path, DisplayName: f.Rel})
		} else {
			out = append(out, &rpc.AttachmentFile{Path: f.Path, DisplayName: f.Rel})
		}
	}
	for _, b := range p.Attachments {
		if b.Path != "" {
			out = append(out, &rpc.AttachmentFile{Path: b.Path, DisplayName: b.Name})
			continue
		}
		data, name := base64.StdEncoding.EncodeToString(b.Data), b.Name
		out = append(out, &rpc.AttachmentBlob{Data: &data, MIMEType: b.MIME, DisplayName: &name})
	}
	return out
}

// blobAttachments describes the uploads a user message carried, without
// their bytes. A live blob has the data; a recorded one has the asset ID, the
// SHA-256 of the bytes, instead. An upload sent as its named copy is a file
// under the web service's upload store, hashed from disk; it is NotNative
// unless the message lists its type among those the CLI sent natively and
// its path is not among those that fell back to the path flow.
func blobAttachments(atts []copilot.Attachment, native, fallback []string) []agentapi.Attachment {
	var out []agentapi.Attachment
	for _, a := range atts {
		if f, ok := a.(*rpc.AttachmentFile); ok {
			if att, ok := uploadFile(f); ok {
				att.NotNative = !slices.Contains(native, att.MIME) || slices.Contains(fallback, f.Path)
				out = append(out, att)
			}
			continue
		}
		b, ok := a.(*rpc.AttachmentBlob)
		if !ok {
			continue
		}
		att := agentapi.Attachment{MIME: b.MIMEType}
		if b.DisplayName != nil {
			att.Name = *b.DisplayName
		}
		if b.ByteLength != nil {
			att.Size = *b.ByteLength
		}
		switch {
		case b.Data != nil:
			if data, err := base64.StdEncoding.DecodeString(*b.Data); err == nil {
				sum := sha256.Sum256(data)
				att.SHA256, att.Size = hex.EncodeToString(sum[:]), int64(len(data))
			}
		case b.AssetID != nil:
			if digest, ok := strings.CutPrefix(*b.AssetID, "sha256:"); ok {
				att.SHA256 = strings.ToLower(digest)
			}
		}
		out = append(out, att)
	}
	return out
}

// uploadFile describes a file attachment that is an upload's named copy,
// <UploadsDir>/<task>/<id>.d/<name>; any other file is a reference, not an
// upload. A copy no longer on disk keeps its name and type, without a hash.
func uploadFile(f *rpc.AttachmentFile) (agentapi.Attachment, bool) {
	if filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(f.Path)))) != agentapi.UploadsDir || !strings.HasSuffix(filepath.Base(filepath.Dir(f.Path)), ".d") {
		return agentapi.Attachment{}, false
	}
	att := agentapi.Attachment{Name: f.DisplayName, MIME: "application/pdf"}
	file, err := os.Open(f.Path) // #nosec G304 -- a path under the web service's own upload store, checked above.
	if err != nil {
		return att, true
	}
	defer func() { _ = file.Close() }()
	h := sha256.New()
	if n, err := io.Copy(h, file); err == nil {
		att.SHA256, att.Size = hex.EncodeToString(h.Sum(nil)), n
	}
	return att, true
}

func (c *conversation) send(ctx context.Context, msg copilot.MessageOptions) error {
	c.mu.Lock()
	closed, running, idles := c.closed, c.turnRunning, c.idles
	c.mu.Unlock()
	if closed {
		return agentapi.ErrClosed
	}
	if running {
		return agentapi.ErrBusy
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := c.sess.Send(ctx, msg); err != nil {
		return c.sendError(err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// An idle seen while Send was in flight already ended this turn.
	if c.idles == idles {
		c.foregroundIdle, c.turnRunning = false, true
		c.idleUnresolved = false
		c.autopilotTurn = c.execution != nil && c.execution.Mode == "autopilot"
		c.emitLocked(agentapi.Event{Kind: agentapi.EventTurn, Turn: &agentapi.Turn{State: agentapi.TurnWorking}})
	}
	return nil
}

// sendError reports a JSON-RPC error answer as a plain rejection: the CLI
// received the prompt and refused it. Any other failure after the call
// started may have happened after the request was written (timeout, CLI exit,
// closed pipe), so it is reported as uncertain.
func (c *conversation) sendError(err error) error {
	var rej rejectedError
	if errors.As(err, &rej) {
		return fmt.Errorf("copilot rejected the prompt: %s", errText(rej.err))
	}
	c.p.poke()
	return fmt.Errorf("%w: %s", agentapi.ErrSubmissionUncertain, errText(err))
}

// Steer sends prompt, with its attachments, in the "immediate" mode: the CLI
// folds it into the running turn before its next model call, and moves a running foreground
// shell command to the background. It reports no turn transition. The
// message ID the CLI returns links the steer to its user message: that
// message is marked as a steer, and a steer a stopped or failed turn ends
// without is reported as not delivered (the CLI drops unused steers on abort).
func (c *conversation) Steer(ctx context.Context, prompt agentapi.Prompt) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return agentapi.ErrClosed
	}
	st := &steer{prompt: prompt.Text}
	c.steers = append(c.steers, st)
	c.steering++
	c.mu.Unlock()
	id, err := "", ctx.Err()
	if err == nil {
		if id, err = c.sess.Send(ctx, copilot.MessageOptions{Prompt: prompt.Text, Attachments: attachments(prompt), Mode: string(rpc.SendModeImmediate)}); err != nil {
			err = c.sendError(err)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.steering--
	used := c.seen[id]
	if c.steering == 0 {
		clear(c.seen)
	}
	st.id = id
	if err != nil || id == "" || used || c.closed {
		c.steers = slices.DeleteFunc(c.steers, func(pending *steer) bool { return pending == st })
	} else if st.state != "" {
		c.undeliveredSteerLocked(st)
	}
	return err
}

func (c *conversation) Cancel(ctx context.Context) error {
	c.control.Lock()
	defer c.control.Unlock()
	if c.isClosed() {
		return agentapi.ErrClosed
	}
	var modeErr error
	if runtime, ok := c.sess.(executionSession); ok {
		modeErr = runtime.SetExecutionMode(ctx, rpc.SessionModeInteractive)
		c.refreshExecution(ctx)
	}
	// Abort must still run after a partial mode failure, with its own bounded
	// context if the mode operation used up the original deadline.
	abortCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	abortErr := c.sess.Abort(abortCtx)
	if modeErr != nil || abortErr != nil {
		return fmt.Errorf("copilot stop: %w", errors.Join(modeErr, abortErr))
	}
	return nil
}

func (c *conversation) CancelSubagent(ctx context.Context, agentID string) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return agentapi.ErrClosed
	}
	if sa := c.subs.byID[agentID]; c.stoppedSubagents[agentID] || sa != nil && sa.Status.Terminal() {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()
	cancelled, err := c.sess.CancelSubagent(ctx, agentID)
	if err != nil {
		c.p.poke()
		return fmt.Errorf("copilot cancel subagent: %s", errText(err))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !cancelled {
		if sa := c.subs.byID[agentID]; sa != nil && sa.Status.Terminal() {
			return nil
		}
		return errors.New("copilot did not cancel the subagent")
	}
	if c.stoppedSubagents == nil {
		c.stoppedSubagents = map[string]bool{}
	}
	c.stoppedSubagents[agentID] = true
	c.expireSubagentLocked(agentID)
	// A stopped follow-up may end without a subagent event.
	c.checkTasksLocked()
	return nil
}

// PromptSubagent sends text to exactly agentID, only while this record says
// idle: the CLI accepts a message for a running agent and never delivers it.
// An accepted follow-up runs until the task list reports it idle or ended.
func (c *conversation) PromptSubagent(ctx context.Context, agentID, text string) error {
	c.taskRPC.Lock()
	defer c.taskRPC.Unlock()
	c.mu.Lock()
	closed, sa := c.closed, c.subs.byID[agentID]
	var status agentapi.SubagentStatus
	if sa != nil {
		status = sa.Status
	}
	c.mu.Unlock()
	switch {
	case closed:
		return agentapi.ErrClosed
	case sa == nil:
		return errors.New("copilot has no such subagent")
	case status != agentapi.SubagentIdle:
		return fmt.Errorf("the subagent is %s, not waiting for a follow-up", status)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	sent := time.Now()
	res, err := c.sess.MessageSubagent(ctx, agentID, text)
	var uncertain error
	switch {
	case err != nil:
		uncertain = c.sendError(err)
		if !errors.Is(uncertain, agentapi.ErrSubmissionUncertain) {
			return uncertain
		}
	case res == nil:
		uncertain = fmt.Errorf("%w: copilot returned no result", agentapi.ErrSubmissionUncertain)
	case !res.Sent:
		reason := "copilot did not deliver the follow-up"
		if res.Error != nil && *res.Error != "" {
			reason += ": " + clip(displaytext.Sanitize(*res.Error), maxErrorText)
		}
		return errors.New(reason)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if sa.Status == agentapi.SubagentIdle {
		// A follow-up that may have arrived blocks another until the task list
		// settles it. An accepted one ends only with an idle entry newer than
		// the send: the list can still report the idle from before it.
		if uncertain != nil {
			sent = time.Time{}
		}
		sa.Status, sa.EndedAt = agentapi.SubagentRunning, time.Time{}
		v := *sa
		c.emitLocked(agentapi.Event{Kind: agentapi.EventSubagent, Subagent: &v})
		c.watchLocked(agentID, sent)
	}
	return uncertain
}

func (c *conversation) Diff(context.Context) ([]agentapi.FileDiff, error) {
	return nil, agentapi.ErrUnsupported
}

func (c *conversation) Respond(ctx context.Context, id string, ans agentapi.Answer) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return agentapi.ErrClosed
	}
	in := c.pending[id]
	if in == nil || in.answering {
		c.mu.Unlock()
		return agentapi.ErrInteractionGone
	}
	if in.reply != nil {
		defer c.mu.Unlock()
		return c.answerLocked(in, ans)
	}
	decision, ok := in.decisions[ans.Decision]
	if !ok {
		c.mu.Unlock()
		return fmt.Errorf("copilot: unknown permission decision %q", ans.Decision)
	}
	if _, once := decision.(*rpc.PermissionDecisionApproveOnce); once && ans.Auto {
		decision = &rpc.PermissionDecisionApproveOnce{} // no person approved it
	}
	in.answering = true
	c.mu.Unlock()

	applied, err := c.sess.RespondPermission(ctx, id, decision)

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending[id] != in { // closed or withdrawn while the answer was in flight
		if err == nil && !applied {
			return agentapi.ErrInteractionGone
		}
		return err
	}
	if err != nil {
		in.answering = false
		c.p.poke()
		return fmt.Errorf("copilot permission answer: %s", errText(err))
	}
	delete(c.pending, id)
	switch {
	case !applied:
		in.State = agentapi.InteractionExpired
	case ans.Decision == "reject":
		in.State, in.Resolution = agentapi.InteractionRejected, optionLabel(in.Options, ans.Decision)
	default:
		in.State, in.Resolution = agentapi.InteractionAnswered, optionLabel(in.Options, ans.Decision)
	}
	c.emitInteractionLocked(in)
	if !applied {
		return agentapi.ErrInteractionGone
	}
	return nil
}

// answerLocked releases a blocked ask_user handler. A rejected question
// returns an error to the CLI instead of inventing an answer.
func (c *conversation) answerLocked(in *interaction, ans agentapi.Answer) error {
	q := in.Questions[0]
	var r userReply
	if ans.Reject {
		r.err = errDeclined
		in.State = agentapi.InteractionRejected
	} else {
		if len(ans.Answers) != 1 || len(ans.Answers[0]) != 1 || ans.Answers[0][0] == "" {
			return errors.New("copilot: a question needs exactly one answer")
		}
		a := ans.Answers[0][0]
		chosen := slices.Contains(q.Choices, a)
		if !chosen && !q.Custom {
			return errors.New("copilot: the answer must be one of the offered choices")
		}
		r.resp = copilot.UserInputResponse{Answer: a, WasFreeform: !chosen}
		in.State = agentapi.InteractionAnswered
	}
	delete(c.pending, in.ID)
	c.settledLocked(in, time.Now())
	c.emitInteractionLocked(in)
	in.reply <- r
	return nil
}

func (c *conversation) Close(ctx context.Context) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	perms := c.expireLocked()
	c.endIdleLocked()
	c.closed = true
	clear(c.pending)
	c.mu.Unlock()
	c.p.forget(c)

	ctx, cancel := context.WithTimeout(ctx, webStopTimeout)
	defer cancel()
	for _, id := range perms {
		_, _ = c.sess.RespondPermission(ctx, id, &rpc.PermissionDecisionUserNotAvailable{})
	}
	done := make(chan error, 1)
	go func() { done <- c.sess.Disconnect() }()
	select {
	case err := <-done:
		if err != nil {
			c.p.poke()
			return fmt.Errorf("close copilot conversation: %s", errText(err))
		}
		return nil
	case <-ctx.Done():
		c.p.poke()
		return fmt.Errorf("close copilot conversation: %w", ctx.Err())
	}
}

// expireSubagentLocked withdraws only this agent's abandoned interactions.
// The CLI may leave its permissions pending after a successful stop.
func (c *conversation) expireSubagentLocked(agentID string) {
	for id, in := range c.pending {
		if in.AgentID != agentID {
			continue
		}
		delete(c.pending, id)
		in.State, in.Resolution = agentapi.InteractionExpired, "the subagent was stopped"
		c.emitInteractionLocked(in)
		if in.reply != nil {
			c.settledLocked(in, time.Now())
			in.reply <- userReply{err: errNoUser}
		}
	}
}

// expireLocked withdraws every pending interaction that is not being
// answered: blocked questions get an error, never an answer. It returns the
// permission request ids the CLI may still be waiting on.
func (c *conversation) expireLocked() []string {
	var perms []string
	for id, in := range c.pending {
		if in.answering {
			continue
		}
		delete(c.pending, id)
		in.State = agentapi.InteractionExpired
		c.emitInteractionLocked(in)
		if in.reply != nil {
			c.settledLocked(in, time.Now())
			in.reply <- userReply{err: errNoUser}
		} else {
			perms = append(perms, id)
		}
	}
	return perms
}

func (c *conversation) exit(reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.exitLocked(reason)
}

func (c *conversation) exitLocked(reason string) {
	if c.closed {
		return
	}
	c.expireLocked()
	clear(c.pending)
	c.endIdleLocked()
	c.emitLocked(agentapi.Event{Kind: agentapi.EventExit, Error: reason})
	c.closed = true
}

// endIdleLocked reports idle subagents as completed when the conversation
// ends: without a live task list nothing says they still take follow-ups.
func (c *conversation) endIdleLocked() {
	for _, id := range c.subs.order {
		if sa := c.subs.byID[id]; sa.Status == agentapi.SubagentIdle {
			sa.Status = agentapi.SubagentCompleted
			v := *sa
			c.emitLocked(agentapi.Event{Kind: agentapi.EventSubagent, Subagent: &v})
		}
	}
	clear(c.watch)
}

// watchLocked has the task list settle agentID from now on. A non-zero sent
// ignores idle entries from before that follow-up.
func (c *conversation) watchLocked(agentID string, sent time.Time) {
	c.watch[agentID] = sent
	c.checkTasksLocked()
}

// checkTasksLocked reads background shells and watched agents on its own
// goroutine: a request made on the event goroutine stalls the connection its
// answer arrives on. One read runs at a time; asking during it adds one more.
func (c *conversation) checkTasksLocked() {
	if c.closed {
		return
	}
	if c.sess == nil {
		c.relist = true
		return
	}
	if c.listing {
		c.relist = true
		return
	}
	c.listing = true
	go c.readTasks(c.sess)
}

func (c *conversation) readTasks(sess sdkSession) {
	for {
		c.taskRPC.Lock()
		// Only a read started after the idle can show the CLI still defers it.
		c.mu.Lock()
		afterIdle := c.idleUnresolved
		c.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), webTasksTimeout)
		tasks, err := sess.ListTasks(ctx)
		cancel()
		c.mu.Lock()
		switch {
		case err != nil:
			c.p.poke()
			if !c.closed && c.backgroundTasks != nil {
				snapshot := *c.backgroundTasks
				snapshot.Known = false
				c.backgroundTasks = &snapshot
				c.emitLocked(agentapi.Event{Kind: agentapi.EventBackgroundTasks, BackgroundTasks: &snapshot})
			}
		case !c.closed:
			c.applyShellTasksLocked(tasks)
			c.applyTasksLocked(tasks)
			if afterIdle && c.idleUnresolved && attachedShellRunning(tasks) {
				c.finishTurnLocked(nil, time.Now())
			}
		}
		again := c.relist && !c.closed
		c.listing, c.relist = again, false
		c.mu.Unlock()
		c.taskRPC.Unlock()
		if !again {
			return
		}
	}
}

// attachedShellRunning reports whether tasks hold a running attached shell:
// work the CLI withholds session.idle for.
func attachedShellRunning(tasks []rpc.TaskInfo) bool {
	return slices.ContainsFunc(tasks, func(task rpc.TaskInfo) bool {
		shell, ok := task.(*rpc.TaskShellInfo)
		return ok && shell.AttachmentMode == rpc.TaskShellInfoAttachmentModeAttached && shell.Status == rpc.TaskStatusRunning
	})
}

func (c *conversation) applyShellTasksLocked(tasks []rpc.TaskInfo) {
	snapshot := agentapi.BackgroundTasks{Known: true, Tasks: []agentapi.BackgroundTask{}}
	for _, task := range tasks {
		shell, ok := task.(*rpc.TaskShellInfo)
		if !ok {
			continue
		}
		item := agentapi.BackgroundTask{
			ID: shell.ID, Command: clip(displaytext.Sanitize(shell.Command), maxToolText),
			Description: clip(displaytext.Sanitize(shell.Description), maxErrorText),
			Status:      string(shell.Status), StartedAt: shell.StartedAt,
		}
		if shell.CompletedAt != nil {
			item.EndedAt = *shell.CompletedAt
		}
		snapshot.Tasks = append(snapshot.Tasks, item)
	}
	if c.backgroundTasks == nil && len(snapshot.Tasks) == 0 && !c.reportEmptyTasks {
		return
	}
	c.reportEmptyTasks = false
	if c.backgroundTasks != nil && c.backgroundTasks.Known && slices.Equal(c.backgroundTasks.Tasks, snapshot.Tasks) {
		return
	}
	c.backgroundTasks = &snapshot
	c.emitLocked(agentapi.Event{Kind: agentapi.EventBackgroundTasks, BackgroundTasks: &snapshot})
}

// applyTasksLocked settles the watched agents from one task-list read. Only
// the entry for the exact agent counts. It is idle only when the list says
// idle with a synchronous wait: a follow-up to a background agent wakes the
// main agent. A finished agent is otherwise left completed; a follow-up ends
// with the status the list reports.
func (c *conversation) applyTasksLocked(tasks []rpc.TaskInfo) {
	for id, sent := range c.watch {
		sa, t := c.subs.byID[id], agentTask(tasks, id)
		followUp := sa != nil && sa.Status == agentapi.SubagentRunning
		if !followUp && (sa == nil || sa.Status != agentapi.SubagentCompleted) {
			delete(c.watch, id) // an event ended it meanwhile
			continue
		}
		if t == nil && followUp || t != nil && t.Status == rpc.TaskStatusRunning || followUp && idleBefore(t, sent) {
			continue // not settled yet
		}
		delete(c.watch, id)
		status := agentapi.SubagentCompleted
		switch {
		case takesFollowUps(t):
			status = agentapi.SubagentIdle
		case !followUp:
			continue
		case t.Status == rpc.TaskStatusFailed:
			status = agentapi.SubagentFailed
			if t.Error != nil {
				sa.Error = clip(displaytext.Sanitize(*t.Error), maxErrorText)
			}
		case t.Status == rpc.TaskStatusCancelled:
			status = agentapi.SubagentCancelled
			c.expireSubagentLocked(id)
		}
		sa.Status = status
		if sa.EndedAt.IsZero() {
			sa.EndedAt = time.Now()
			if t.IdleSince != nil && status == agentapi.SubagentIdle {
				sa.EndedAt = *t.IdleSince
			}
		}
		v := *sa
		c.emitLocked(agentapi.Event{Kind: agentapi.EventSubagent, Subagent: &v})
	}
}

// agentTask returns the task list's entry for exactly agentID, or nil.
func agentTask(tasks []rpc.TaskInfo, agentID string) *rpc.TaskAgentInfo {
	for _, t := range tasks {
		if a, ok := t.(*rpc.TaskAgentInfo); ok && a.ID == agentID {
			return a
		}
	}
	return nil
}

// idleBefore reports an idle entry that entered idle before sent, so it does
// not describe the follow-up sent then. An entry without the time counts.
func idleBefore(t *rpc.TaskAgentInfo, sent time.Time) bool {
	return !sent.IsZero() && t != nil && t.Status == rpc.TaskStatusIdle && t.IdleSince != nil && !t.IdleSince.After(sent)
}

// takesFollowUps reports a task-list entry that waits for a follow-up the
// main agent never sees: idle, and started for a synchronous wait.
func takesFollowUps(t *rpc.TaskAgentInfo) bool {
	return t != nil && t.Status == rpc.TaskStatusIdle && t.ExecutionMode != nil && *t.ExecutionMode == rpc.TaskExecutionModeSync
}

// askUser is the SDK's ask_user callback. It runs on its own goroutine and
// blocks until the user answers or the conversation ends.
func (c *conversation) askUser(req copilot.UserInputRequest, _ copilot.UserInputInvocation) (copilot.UserInputResponse, error) {
	freeform := req.AllowFreeform == nil || *req.AllowFreeform
	in := &interaction{
		Interaction: agentapi.Interaction{
			ID:        "question-" + rand.Text(),
			Kind:      agentapi.InteractionQuestion,
			Title:     "Question from Copilot",
			Questions: []agentapi.Question{{Text: req.Question, Choices: req.Choices, Custom: freeform || len(req.Choices) == 0}},
			State:     agentapi.InteractionPending,
			Time:      time.Now(),
		},
		reply: make(chan userReply, 1),
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return copilot.UserInputResponse{}, errNoUser
	}
	c.askedLocked(in, time.Now())
	c.pending[in.ID] = in
	c.emitInteractionLocked(in)
	c.mu.Unlock()
	r := <-in.reply
	return r.resp, r.err
}

// onEvent runs on the SDK's event goroutine, which stalls the whole CLI
// connection while it runs; it only updates state and emits. Subagent events
// arrive on the same stream with the envelope agentId; they are tagged with
// it so they never enter the main transcript or end the main turn.
func (c *conversation) onEvent(ev copilot.SessionEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	agentID := agentOf(ev)
	switch d := ev.Data.(type) {
	case *rpc.AssistantMessageDeltaData:
		c.emitLocked(deltaEvent(agentID, d.MessageID, agentapi.ItemAssistant, d.DeltaContent))
		return
	case *rpc.AssistantReasoningDeltaData:
		c.tr.streamedReasoning(agentID)
		c.emitLocked(deltaEvent(agentID, reasoningItemID(d.ReasoningID), agentapi.ItemReasoning, d.DeltaContent))
		return
	case *rpc.AssistantUsageData:
		if agentID == "" && d.Model != "" {
			c.turnModel = d.Model
			if d.IsByok != nil && *d.IsByok {
				c.turnModel = c.p.customSelection(d.Model, c.selected)
			}
		}
		if agentID == "" && d.InputTokens != nil && *d.InputTokens >= 0 {
			// Input tokens include those read from and written to the cache.
			c.usage.Prompt, c.usage.Cached = *d.InputTokens, 0
			if d.CacheReadTokens != nil {
				c.usage.Cached = min(max(*d.CacheReadTokens, 0), *d.InputTokens)
			}
			c.emitContextLocked()
		}
		// Every call costs, a subagent's included.
		if d.CopilotUsage != nil && d.CopilotUsage.TotalNanoAiu > 0 {
			c.nanoAIU += d.CopilotUsage.TotalNanoAiu
			c.emitUsageLocked()
		}
		return
	case *rpc.SessionUsageCheckpointData:
		// The CLI's session-wide total, which the next reopen reads back.
		if d.TotalNanoAiu >= 0 {
			c.nanoAIU = d.TotalNanoAiu
			c.emitUsageLocked()
		}
		return
	case *rpc.SessionUsageInfoData:
		if agentID == "" && d.CurrentTokens >= 0 && d.TokenLimit > 0 {
			c.usage.Used, c.usage.Limit = d.CurrentTokens, d.TokenLimit
			c.emitContextLocked()
		}
		return
	case *rpc.SessionTitleChangedData:
		c.emitLocked(agentapi.Event{Kind: agentapi.EventTitle, Title: d.Title})
		return
	case *rpc.SubagentStartedData, *rpc.SubagentConfiguredData, *rpc.SubagentCompletedData, *rpc.SubagentFailedData:
		if sa, ok := c.subs.apply(ev); ok {
			if sa.Status == agentapi.SubagentCancelled {
				c.expireSubagentLocked(sa.ID)
			}
			c.emitLocked(agentapi.Event{Kind: agentapi.EventSubagent, Subagent: &sa})
			if sa.Status == agentapi.SubagentCompleted {
				c.watchLocked(sa.ID, time.Time{})
			}
		}
		return
	case *rpc.SessionBackgroundTasksChangedData:
		// Shell changes and follow-up completion both invalidate the task list.
		c.checkTasksLocked()
		return
	case *rpc.UserInputRequestedData:
		c.linkQuestionLocked(d, agentID, time.Now())
		return
	case *rpc.AssistantTurnStartData:
		if agentID == "" {
			c.foregroundIdle, c.turnRunning = false, true
			c.idleUnresolved = false
			c.autopilotTurn = c.execution != nil && c.execution.Mode == "autopilot"
			c.emitLocked(agentapi.Event{Kind: agentapi.EventTurn, Turn: &agentapi.Turn{State: agentapi.TurnWorking}})
		}
		return
	case *rpc.SessionErrorData:
		if agentID == "" {
			c.turnErr = clip(displaytext.Sanitize(d.Message), maxErrorText)
		}
	case *rpc.UserMessageData:
		if agentID == "" && d.MessageID != nil {
			c.steers = slices.DeleteFunc(c.steers, func(st *steer) bool { return st.id == *d.MessageID })
			if c.steering > 0 {
				c.seen[*d.MessageID] = true
			}
		}
		// A message delivered while idle starts a turn: also a steer that
		// reached the CLI after the idle of the turn it was meant for.
		if agentID == "" && d.Delivery != nil && *d.Delivery == rpc.UserMessageDeliveryIdle {
			c.foregroundIdle, c.turnRunning = false, true
			c.idleUnresolved = false
			c.autopilotTurn = c.execution != nil && c.execution.Mode == "autopilot"
			c.emitLocked(agentapi.Event{Kind: agentapi.EventTurn, Turn: &agentapi.Turn{State: agentapi.TurnWorking}})
		}
	case *rpc.SessionModeChangedData:
		if agentID == "" {
			c.executionRevision++
			if c.execution == nil {
				c.execution = &agentapi.ExecutionState{}
			}
			next := *c.execution
			next.Mode, next.Known = string(d.NewMode), false
			if d.NewMode != rpc.SessionModeAutopilot {
				c.autopilotTurn = false
			}
			c.execution = &next
			c.emitLocked(agentapi.Event{Kind: agentapi.EventExecution, Execution: &next})
			c.checkExecutionLocked()
		}
		return
	case *rpc.SessionAutopilotObjectiveChangedData:
		if agentID == "" {
			c.executionRevision++
			c.checkExecutionLocked()
		}
		return
	case *rpc.AssistantIdleData:
		if agentID != "" {
			return
		}
		if (c.autopilotTurn || c.execution != nil && (c.execution.Mode == "autopilot" || c.execution.Mode == "")) && (d.Aborted == nil || !*d.Aborted) {
			c.idleUnresolved = true
			if c.execution != nil && c.execution.Mode == "" {
				c.checkExecutionLocked()
			}
			c.checkTasksLocked()
			c.autopilotTurn = true
			return
		}
		c.assistantIdleSeen = true
		c.finishTurnLocked(d.Aborted, ev.Timestamp)
		return
	case *rpc.SessionIdleData:
		if agentID == "" && d.Mode != nil && *d.Mode == rpc.SessionModeAutopilot && (d.Aborted == nil || !*d.Aborted) {
			c.autopilotTurn = true
			next := agentapi.ExecutionState{Mode: "autopilot"}
			if c.execution != nil {
				next = *c.execution
				next.Mode = "autopilot"
				next.Known = false
			}
			c.execution = &next
			c.executionRevision++
			c.emitLocked(agentapi.Event{Kind: agentapi.EventExecution, Execution: &next})
			c.checkExecutionLocked()
			return
		}
		if agentID == "" && (!c.assistantIdleSeen || c.autopilotTurn) && (d.Mode == nil || *d.Mode != rpc.SessionModeAutopilot || d.Aborted != nil && *d.Aborted) {
			c.finishTurnLocked(d.Aborted, ev.Timestamp)
			c.autopilotTurn = false
		}
		return
	case *rpc.PermissionRequestedData:
		c.permissionRequestedLocked(d, ev.Timestamp, agentID)
		return
	case *rpc.PermissionCompletedData:
		c.permissionCompletedLocked(d)
		return
	case *rpc.SessionShutdownData:
		if d.TotalNanoAiu != nil && *d.TotalNanoAiu >= 0 {
			c.nanoAIU = *d.TotalNanoAiu
			c.emitUsageLocked()
		}
		if d.ShutdownType == rpc.ShutdownTypeError {
			reason := "Copilot session shut down"
			if d.ErrorReason != nil {
				reason += ": " + clip(displaytext.Sanitize(*d.ErrorReason), maxErrorText)
			}
			c.exitLocked(reason)
			c.p.forget(c)
		}
		return
	}
	for _, it := range c.tr.items(ev) {
		c.emitLocked(agentapi.Event{Kind: agentapi.EventItem, Item: &it})
	}
}

// nanoPerUnit converts the CLI's nano-AI units to AI units, the unit of the
// catalog's token prices (AI Credits).
const nanoPerUnit = 1e9

// emitContextLocked reports the context once a report gave its limit.
func (c *conversation) emitContextLocked() {
	if c.usage.Limit > 0 {
		usage := c.usage
		c.emitLocked(agentapi.Event{Kind: agentapi.EventContext, Context: &usage})
	}
}

// emitUsageLocked reports the conversation's AI units when they changed.
func (c *conversation) emitUsageLocked() {
	if c.nanoAIU == c.usageSent {
		return
	}
	c.usageSent = c.nanoAIU
	c.emitLocked(agentapi.Event{Kind: agentapi.EventUsage, Usage: &agentapi.Usage{AIUnits: c.nanoAIU / nanoPerUnit}})
}

func (c *conversation) finishTurnLocked(aborted *bool, at time.Time) {
	if c.foregroundIdle {
		return
	}
	c.foregroundIdle, c.turnRunning = true, false
	c.idleUnresolved = false
	c.idles++
	turn := agentapi.Turn{State: agentapi.TurnCompleted, Model: c.turnModel}
	switch {
	case aborted != nil && *aborted:
		turn.State = agentapi.TurnCancelled
		c.expireLocked()
	case c.turnErr != "":
		turn.State, turn.Error = agentapi.TurnFailed, c.turnErr
	}
	c.turnErr, c.turnModel = "", ""
	c.undeliveredLocked(turn.State, at)
	c.emitLocked(agentapi.Event{Kind: agentapi.EventTurn, Turn: &turn})
}

// undeliveredLocked reports each steer the ending turn did not use as a
// notice. The CLI drops unused steers when a turn is aborted. A steer still
// pending at a completed idle is not reported: the CLI either used it in that
// turn or, when it arrived after the idle, starts a new turn with it.
func (c *conversation) undeliveredLocked(state agentapi.TurnState, at time.Time) {
	for _, st := range c.steers {
		st.state, st.at = state, at
		if st.id != "" {
			c.undeliveredSteerLocked(st)
		}
	}
	c.steers = nil
}

func (c *conversation) undeliveredSteerLocked(st *steer) {
	reason := "the turn was stopped"
	switch st.state {
	case agentapi.TurnCompleted:
		return
	case agentapi.TurnFailed:
		reason = "the turn failed"
	}
	it := agentapi.Item{ID: "steer-undelivered:" + st.id, Kind: agentapi.ItemNotice, Time: st.at, Text: "Steer not delivered: " + reason + "\n\n" + quote(st.prompt)}
	c.emitLocked(agentapi.Event{Kind: agentapi.EventItem, Item: &it})
}

// quote renders text as a markdown block quote.
func quote(text string) string {
	return "> " + strings.ReplaceAll(clip(text, maxToolText), "\n", "\n> ")
}

func (c *conversation) permissionRequestedLocked(d *rpc.PermissionRequestedData, at time.Time, agentID string) {
	if sa := c.subs.byID[agentID]; agentID != "" && (c.stoppedSubagents[agentID] || sa != nil && sa.Status == agentapi.SubagentCancelled) {
		return
	}
	if d.ResolvedByHook != nil && *d.ResolvedByHook {
		return
	}
	if old := c.pending[d.RequestID]; old != nil && old.answering {
		return
	}
	title, detail := describePermission(d)
	in := &interaction{
		Interaction: agentapi.Interaction{ID: d.RequestID, Kind: agentapi.InteractionPermission, Title: title, Detail: clip(detail, maxToolText), State: agentapi.InteractionPending, Time: at, AgentID: agentID, ToolCallID: permissionToolCallID(d.PermissionRequest)},
		decisions:   map[string]rpc.PermissionDecision{},
	}
	add := func(opt agentapi.Option, dec rpc.PermissionDecision) {
		in.Options = append(in.Options, opt)
		in.decisions[opt.ID] = dec
	}
	// A request the managed policy says a person must approve, or one this
	// SDK cannot read, is never marked for automatic approval.
	_, raw := d.PermissionRequest.(*rpc.RawPermissionRequest)
	managed := d.PermissionRequest == nil || raw || d.PermissionRequest.RequiresManagedApproval()
	add(agentapi.Option{ID: "approve_once", Label: "Allow once", AllowOnce: !managed}, &rpc.PermissionDecisionApproveOnce{ApprovedInteractively: copilot.Bool(true)})
	if dec := sessionApproval(d.PromptRequest); dec != nil {
		add(agentapi.Option{ID: "approve_session", Label: "Allow for this session"}, dec)
	}
	add(agentapi.Option{ID: "reject", Label: "Deny", Reject: true}, &rpc.PermissionDecisionReject{})
	c.pending[d.RequestID] = in
	c.emitInteractionLocked(in)
}

// permissionCompletedLocked reports a permission the CLI resolved without
// this client (a hook, policy, or the turn ending).
func (c *conversation) permissionCompletedLocked(d *rpc.PermissionCompletedData) {
	in := c.pending[d.RequestID]
	if in == nil || in.reply != nil || in.answering {
		return
	}
	delete(c.pending, d.RequestID)
	in.State = agentapi.InteractionExpired
	if d.Result != nil {
		in.Resolution = kindText(string(d.Result.Kind()))
		switch d.Result.Kind() {
		case rpc.PermissionResultKindApproved, rpc.PermissionResultKindApprovedForSession, rpc.PermissionResultKindApprovedForLocation:
			in.State = agentapi.InteractionAnswered
		case rpc.PermissionResultKindCancelled, rpc.PermissionResultKindDeniedNoApprovalRuleAndCouldNotRequestFromUser:
		default:
			in.State = agentapi.InteractionRejected
		}
	}
	c.emitInteractionLocked(in)
}

// sessionApproval builds the "allow for this session" decision the same way
// the CLI's own prompt does, for the prompt kinds where that is unambiguous.
// URL (origin pattern computed natively), path, hook, factory and extension
// prompts get no session option.
func sessionApproval(pr rpc.PermissionPromptRequest) rpc.PermissionDecision {
	var approval rpc.PermissionDecisionApproveForSessionApproval
	switch r := pr.(type) {
	case *rpc.PermissionPromptRequestCommands:
		if r.CanOfferSessionApproval && len(r.CommandIdentifiers) > 0 {
			approval = &rpc.PermissionDecisionApproveForSessionApprovalCommands{CommandIdentifiers: r.CommandIdentifiers}
		}
	case *rpc.PermissionPromptRequestWrite:
		if r.CanOfferSessionApproval {
			approval = &rpc.PermissionDecisionApproveForSessionApprovalWrite{}
		}
	case *rpc.PermissionPromptRequestRead:
		approval = &rpc.PermissionDecisionApproveForSessionApprovalRead{}
	case *rpc.PermissionPromptRequestMCP:
		approval = &rpc.PermissionDecisionApproveForSessionApprovalMCP{ServerName: r.ServerName, ToolName: &r.ToolName}
	case *rpc.PermissionPromptRequestMemory:
		approval = &rpc.PermissionDecisionApproveForSessionApprovalMemory{}
	case *rpc.PermissionPromptRequestCustomTool:
		approval = &rpc.PermissionDecisionApproveForSessionApprovalCustomTool{ToolName: r.ToolName}
	}
	if approval == nil {
		return nil
	}
	return &rpc.PermissionDecisionApproveForSession{Approval: approval}
}

func describePermission(d *rpc.PermissionRequestedData) (title, detail string) {
	switch r := d.PromptRequest.(type) {
	case *rpc.PermissionPromptRequestCommands:
		detail = r.FullCommandText
		if r.Warning != nil {
			detail += "\n\nWarning: " + *r.Warning
		}
		return "Run shell command", detail + sandboxBypass(r.RequestSandboxBypass, r.RequestSandboxBypassReason)
	case *rpc.PermissionPromptRequestWrite:
		return "Write file", r.FileName + "\n\n" + r.Diff
	case *rpc.PermissionPromptRequestRead:
		return "Read file", r.Path
	case *rpc.PermissionPromptRequestPath:
		return "Access paths outside the workspace", strings.Join(r.Paths, "\n")
	case *rpc.PermissionPromptRequestURL:
		return "Fetch URL", r.URL + sandboxBypass(r.RequestSandboxBypass, r.RequestSandboxBypassReason)
	case *rpc.PermissionPromptRequestMCP:
		return "Run MCP tool " + r.ServerName + "/" + r.ToolName, compactJSON(r.Args)
	case *rpc.PermissionPromptRequestCustomTool:
		return "Run tool " + r.ToolName, compactJSON(r.Args)
	case *rpc.PermissionPromptRequestMemory:
		return "Store memory", r.Fact
	case *rpc.PermissionPromptRequestHook:
		detail = compactJSON(r.ToolArgs)
		if r.HookMessage != nil {
			detail = *r.HookMessage + "\n\n" + detail
		}
		return "Confirm " + r.ToolName, detail
	}
	kind := "unknown"
	if d.PermissionRequest != nil {
		kind = string(d.PermissionRequest.Kind())
	}
	return "Permission request: " + kind, compactJSON(d.PermissionRequest)
}

// permissionToolCallID returns the tool call a permission request is for,
// or "" when the request does not say. It is the ToolCallID of the tool
// execution events, which the transcript uses as the tool item's ID. Every
// request kind in SDK 1.0.14 has an optional toolCallId; a kind the SDK
// cannot read keeps its raw JSON, which is read for the same field.
func permissionToolCallID(pr rpc.PermissionRequest) string {
	var id *string
	switch r := pr.(type) {
	case *rpc.PermissionRequestShell:
		id = r.ToolCallID
	case *rpc.PermissionRequestWrite:
		id = r.ToolCallID
	case *rpc.PermissionRequestRead:
		id = r.ToolCallID
	case *rpc.PermissionRequestURL:
		id = r.ToolCallID
	case *rpc.PermissionRequestMCP:
		id = r.ToolCallID
	case *rpc.PermissionRequestCustomTool:
		id = r.ToolCallID
	case *rpc.PermissionRequestMemory:
		id = r.ToolCallID
	case *rpc.PermissionRequestHook:
		id = r.ToolCallID
	case *rpc.PermissionRequestFactory:
		id = r.ToolCallID
	case *rpc.PermissionRequestExtensionEnvAccess:
		id = r.ToolCallID
	case *rpc.PermissionRequestExtensionManagement:
		id = r.ToolCallID
	case *rpc.PermissionRequestExtensionPermissionAccess:
		id = r.ToolCallID
	case *rpc.RawPermissionRequest:
		var v struct {
			ToolCallID *string `json:"toolCallId"`
		}
		if json.Unmarshal(r.Raw, &v) == nil {
			id = v.ToolCallID
		}
	}
	if id == nil {
		return ""
	}
	return strings.TrimSpace(*id)
}

func sandboxBypass(requested *bool, reason *string) string {
	if requested == nil || !*requested {
		return ""
	}
	s := "\n\nRequests to run outside the sandbox."
	if reason != nil {
		s += " " + *reason
	}
	return s
}

// transcript maps session events to transcript items. It keeps running tool
// calls by id so start, partial and final results upsert one item.
type transcript struct {
	tools map[string]*agentapi.ToolCall
	// ended holds the IDs of recently completed tool calls. A shell command
	// a steer moved to the background completes its call at once, then keeps
	// streaming partial output under the same ID; that must not reopen it.
	ended map[string]struct{}
	// assets holds recent session.binary_asset events by asset ID. The CLI
	// records one just before the tool completion that names it.
	assets map[string]*rpc.SessionBinaryAssetData
	// reasoned marks each agent that streamed reasoning since its last
	// assistant message, so that message's recorded reasoningText is not
	// shown a second time.
	reasoned map[string]bool
}

// maxEndedTools bounds transcript.ended. Forgetting older IDs only lets a
// partial result that arrives very late reopen its tool call.
const maxEndedTools = 1024

// maxAssets bounds transcript.assets. A tool result that names a forgotten
// asset reports the image by its digest alone.
const maxAssets = 64

func newTranscript() *transcript {
	return &transcript{tools: map[string]*agentapi.ToolCall{}, ended: map[string]struct{}{}, assets: map[string]*rpc.SessionBinaryAssetData{}, reasoned: map[string]bool{}}
}

// items maps one event to its transcript items. An assistant message whose
// agent streamed no reasoning since its previous message yields the
// message's recorded reasoningText as a reasoning item first: the CLI records
// reasoning only there, so this is how thinking comes back from history. A
// live turn streams assistant.reasoning before the message, and that item
// already holds the text.
func (t *transcript) items(ev copilot.SessionEvent) []agentapi.Item {
	var out []agentapi.Item
	if d, ok := ev.Data.(*rpc.AssistantMessageData); ok {
		agentID := agentOf(ev)
		if text := reasoningText(d); text != "" && !t.reasoned[agentID] {
			out = append(out, agentapi.Item{ID: reasoningItemID(d.MessageID), Kind: agentapi.ItemReasoning, Text: text, Time: ev.Timestamp, AgentID: agentID})
		}
		delete(t.reasoned, agentID)
	}
	if it, ok := t.item(ev); ok {
		out = append(out, it)
	}
	return out
}

// streamedReasoning notes that an agent's reasoning arrived live, ahead of
// the message it belongs to.
func (t *transcript) streamedReasoning(agentID string) {
	t.reasoned[agentID] = true
}

func reasoningText(d *rpc.AssistantMessageData) string {
	if d.ReasoningText == nil {
		return ""
	}
	return strings.TrimSpace(*d.ReasoningText)
}

func (t *transcript) item(ev copilot.SessionEvent) (agentapi.Item, bool) {
	it := agentapi.Item{Time: ev.Timestamp, AgentID: agentOf(ev)}
	switch d := ev.Data.(type) {
	case *rpc.UserMessageData:
		it.ID, it.Kind, it.Text = ev.ID, agentapi.ItemUser, d.Content
		if d.MessageID != nil && *d.MessageID != "" {
			it.ID = *d.MessageID
		}
		it.Attachments = blobAttachments(d.Attachments, d.SupportedNativeDocumentMIMETypes, d.NativeDocumentPathFallbackPaths)
		if d.Delivery != nil && *d.Delivery == rpc.UserMessageDeliverySteering {
			it.Delivery = agentapi.DeliverySteer
		} else if d.IsAutopilotContinuation != nil && *d.IsAutopilotContinuation {
			it.Delivery = agentapi.DeliveryAutopilot
		}
	case *rpc.AssistantMessageData:
		if d.Content == "" { // tool-call-only message
			return it, false
		}
		it.ID, it.Kind, it.Text = d.MessageID, agentapi.ItemAssistant, d.Content
	case *rpc.AssistantReasoningData:
		t.streamedReasoning(it.AgentID)
		it.ID, it.Kind, it.Text = reasoningItemID(d.ReasoningID), agentapi.ItemReasoning, d.Content
	case *rpc.SessionCompactionCompleteData:
		it.ID, it.Kind, it.Text = ev.ID, agentapi.ItemNotice, "Conversation compacted."
		if !d.Success {
			it.Text = "Conversation compaction failed."
			if d.Error != nil {
				it.Text += " " + clip(displaytext.Sanitize(*d.Error), maxErrorText)
			}
		}
	case *rpc.SessionTruncationData:
		it.ID, it.Kind, it.Text = ev.ID, agentapi.ItemNotice, fmt.Sprintf("Conversation truncated: %d messages and %d tokens removed.", d.MessagesRemovedDuringTruncation, d.TokensRemovedDuringTruncation)
	case *rpc.SessionErrorData:
		it.ID, it.Kind, it.Text = ev.ID, agentapi.ItemNotice, "Error: "+displaytext.Sanitize(d.Message)
	case *rpc.ToolExecutionStartData:
		tc := &agentapi.ToolCall{Name: d.ToolName, Status: agentapi.ToolRunning, Input: clip(compactJSON(d.Arguments), maxToolText)}
		t.tools[d.ToolCallID] = tc
		delete(t.ended, d.ToolCallID) // a new call reusing an ended ID
		it.ID, it.Kind, it.Tool = d.ToolCallID, agentapi.ItemTool, cloneTool(tc)
	case *rpc.ToolExecutionPartialResultData:
		if _, ended := t.ended[d.ToolCallID]; ended {
			return it, false
		}
		tc := t.tool(d.ToolCallID)
		tc.Output = clip(tc.Output+d.PartialOutput, maxToolText)
		it.ID, it.Kind, it.Tool = d.ToolCallID, agentapi.ItemTool, cloneTool(tc)
	case *rpc.ToolExecutionCompleteData:
		tc := t.tool(d.ToolCallID)
		delete(t.tools, d.ToolCallID)
		if len(t.ended) >= maxEndedTools {
			clear(t.ended)
		}
		t.ended[d.ToolCallID] = struct{}{}
		tc.Status = agentapi.ToolCompleted
		if d.Result != nil {
			tc.Output = clip(d.Result.Content, maxToolText)
			it.Images = t.images(d.Result)
		}
		if !d.Success {
			tc.Status = agentapi.ToolFailed
			if d.Error != nil {
				tc.Output = clip(d.Error.Message, maxToolText)
			}
		}
		it.ID, it.Kind, it.Tool = d.ToolCallID, agentapi.ItemTool, tc
	case *rpc.SessionBinaryAssetData:
		if len(t.assets) >= maxAssets {
			clear(t.assets)
		}
		t.assets[d.AssetID] = d
		return it, false
	default:
		return it, false
	}
	return it, true
}

// images returns the images a tool result carried: its image content blocks
// and its model-facing binary results. A live result holds the bytes. A
// recorded one names a session.binary_asset event by its asset ID,
// "sha256:<hex>" of the bytes; without that event only the digest is known.
func (t *transcript) images(r *rpc.ToolExecutionCompleteResult) []agentapi.Image {
	var out []agentapi.Image
	add := func(data, mime string) {
		b, err := base64.StdEncoding.DecodeString(data)
		if err != nil || len(b) == 0 {
			return
		}
		out = append(out, agentapi.Image{MIME: mime, Data: b})
	}
	for _, c := range r.Contents {
		if img, ok := c.(*rpc.ToolExecutionCompleteContentImage); ok {
			add(img.Data, img.MIMEType)
		}
	}
	for _, b := range r.BinaryResultsForLlm {
		if b == nil || b.Type() != rpc.PersistedBinaryResultTypeImage {
			continue
		}
		switch b := b.(type) {
		case *rpc.PersistedBinaryImage:
			add(b.Data, b.MIMEType)
		case *rpc.BinaryAssetReference:
			if a := t.assets[b.AssetID]; a != nil {
				add(a.Data, a.MIMEType)
			} else if digest, ok := strings.CutPrefix(b.AssetID, "sha256:"); ok {
				out = append(out, agentapi.Image{MIME: b.MIMEType, SHA256: strings.ToLower(digest)})
			}
		}
	}
	return out
}

func (t *transcript) tool(id string) *agentapi.ToolCall {
	tc := t.tools[id]
	if tc == nil { // started before this client attached
		tc = &agentapi.ToolCall{Status: agentapi.ToolRunning}
		t.tools[id] = tc
	}
	return tc
}

// history maps a recorded event log to transcript items, oldest first, with
// one item per agent and id, and to the subagents it records. Ephemeral
// events (streaming deltas) are skipped.
func history(evs []copilot.SessionEvent) agentapi.History {
	t, subs := newTranscript(), newSubagentLog()
	var items []agentapi.Item
	var model string
	index := map[[2]string]int{}
	for _, ev := range evs {
		if ev.Ephemeral != nil && *ev.Ephemeral {
			continue
		}
		if m := recordedModel(ev); m != "" && agentOf(ev) == "" {
			model = m
		}
		subs.apply(ev)
		for _, it := range t.items(ev) {
			key := [2]string{it.AgentID, it.ID}
			if i, seen := index[key]; seen {
				it.Time = items[i].Time
				items[i] = it
				continue
			}
			index[key] = len(items)
			items = append(items, it)
		}
	}
	h := agentapi.History{Items: items, Subagents: subs.list(), Model: model}
	if total, ok := recordedTotal(evs); ok {
		h.Usage = &agentapi.Usage{AIUnits: total / nanoPerUnit}
	}
	return h
}

// recordedTotal is the session-wide nano-AI units of the latest recorded
// usage checkpoint or shutdown. The CLI never records assistant.usage.
func recordedTotal(evs []copilot.SessionEvent) (total float64, ok bool) {
	for _, ev := range evs {
		var t *float64
		switch d := ev.Data.(type) {
		case *rpc.SessionUsageCheckpointData:
			t = &d.TotalNanoAiu
		case *rpc.SessionShutdownData:
			t = d.TotalNanoAiu
		}
		if t != nil && *t >= 0 && (ev.Ephemeral == nil || !*ev.Ephemeral) {
			total, ok = *t, true
		}
	}
	return total, ok
}

// recordedModel is the model an event records as selected, or "".
func recordedModel(ev copilot.SessionEvent) string {
	var selected *string
	switch d := ev.Data.(type) {
	case *rpc.SessionModelChangeData:
		return d.NewModel
	case *rpc.SessionStartData:
		selected = d.SelectedModel
	case *rpc.SessionResumeData:
		selected = d.SelectedModel
	}
	if selected == nil {
		return ""
	}
	return *selected
}

// agentOf returns the envelope's subagent instance ID, or "" for the main
// agent and session-level events.
func agentOf(ev copilot.SessionEvent) string {
	if ev.AgentID == nil {
		return ""
	}
	return *ev.AgentID
}

// subagentLog keeps each subagent's record by its agent ID. Events change a
// running record, and end an idle one only as failed or cancelled. The first
// terminal event wins: Copilot reports a second, cancelled completion for an
// idle subagent when the client disconnects. Only the task list makes a
// completed record idle, and only an accepted follow-up makes it run again.
type subagentLog struct {
	byID   map[string]*agentapi.Subagent
	byCall map[string]string // spawning tool call ID -> agent ID
	order  []string
}

func newSubagentLog() *subagentLog {
	return &subagentLog{byID: map[string]*agentapi.Subagent{}, byCall: map[string]string{}}
}

// apply folds a subagent.* event into the log and returns the changed record,
// or false when nothing changed.
func (l *subagentLog) apply(ev copilot.SessionEvent) (agentapi.Subagent, bool) {
	agentID := agentOf(ev)
	var callID, name, errMsg string
	var status agentapi.SubagentStatus
	switch d := ev.Data.(type) {
	case *rpc.SubagentStartedData:
		if agentID == "" {
			return agentapi.Subagent{}, false
		}
		sa := l.get(agentID)
		if sa.Status.Terminal() || sa.Status == agentapi.SubagentIdle {
			return agentapi.Subagent{}, false
		}
		sa.ParentToolCallID, sa.Name, sa.Description = d.ToolCallID, subagentName(d.AgentDisplayName, d.AgentName), d.AgentDescription
		sa.Status, sa.StartedAt = agentapi.SubagentRunning, ev.Timestamp
		if d.Model != nil && sa.Model == "" {
			sa.Model = *d.Model
		}
		l.byCall[d.ToolCallID] = agentID
		return *sa, true
	case *rpc.SubagentConfiguredData:
		if agentID == "" {
			return agentapi.Subagent{}, false
		}
		sa := l.get(agentID)
		if sa.Status.Terminal() || sa.Status == agentapi.SubagentIdle {
			return agentapi.Subagent{}, false
		}
		sa.Model = d.Model
		sa.Effort = ""
		if d.ReasoningEffort != nil {
			sa.Effort = *d.ReasoningEffort
		}
		return *sa, true
	case *rpc.SubagentCompletedData:
		callID, name, status = d.ToolCallID, subagentName(d.AgentDisplayName, d.AgentName), agentapi.SubagentCompleted
		if d.Cancelled != nil && *d.Cancelled {
			status = agentapi.SubagentCancelled
		}
	case *rpc.SubagentFailedData:
		callID, name, status = d.ToolCallID, subagentName(d.AgentDisplayName, d.AgentName), agentapi.SubagentFailed
		errMsg = clip(displaytext.Sanitize(d.Error), maxErrorText)
	default:
		return agentapi.Subagent{}, false
	}
	if agentID == "" {
		agentID = l.byCall[callID]
	}
	if agentID == "" {
		return agentapi.Subagent{}, false
	}
	sa := l.get(agentID)
	if sa.Status.Terminal() || sa.Status == agentapi.SubagentIdle && status == agentapi.SubagentCompleted {
		return agentapi.Subagent{}, false
	}
	if sa.ParentToolCallID == "" {
		sa.ParentToolCallID = callID
	}
	if sa.Name == "" {
		sa.Name = name
	}
	sa.Status, sa.Error, sa.EndedAt = status, errMsg, ev.Timestamp
	return *sa, true
}

func (l *subagentLog) get(agentID string) *agentapi.Subagent {
	sa := l.byID[agentID]
	if sa == nil {
		sa = &agentapi.Subagent{ID: agentID}
		l.byID[agentID] = sa
		l.order = append(l.order, agentID)
	}
	return sa
}

func (l *subagentLog) list() []agentapi.Subagent {
	out := make([]agentapi.Subagent, 0, len(l.order))
	for _, id := range l.order {
		out = append(out, *l.byID[id])
	}
	return out
}

func subagentName(display, name string) string {
	if display != "" {
		return display
	}
	return name
}

func cloneTool(tc *agentapi.ToolCall) *agentapi.ToolCall {
	v := *tc
	return &v
}

func deltaEvent(agentID, id string, kind agentapi.ItemKind, text string) agentapi.Event {
	return agentapi.Event{Kind: agentapi.EventDelta, Delta: &agentapi.Delta{ItemID: id, Kind: kind, Text: text, AgentID: agentID}}
}

// reasoningItemID keeps reasoning items apart from the message they precede.
func reasoningItemID(id string) string { return "reasoning:" + id }

func compactJSON(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// optionLabel returns the label the user saw for a permission option.
func optionLabel(options []agentapi.Option, id string) string {
	for _, o := range options {
		if o.ID == id {
			return o.Label
		}
	}
	return id
}

// kindText turns a CLI result kind such as "denied-interactively-by-user"
// into display text.
func kindText(kind string) string {
	text := strings.ReplaceAll(kind, "-", " ")
	if text == "" {
		return text
	}
	return strings.ToUpper(text[:1]) + text[1:]
}

// exitText reports why the CLI died without the stderr tail the SDK appends:
// after an abrupt exit that tail comes from the npm wrapper and misleads
// (for example "no platform package found" after the native CLI was killed).
func exitText(err error) string {
	msg, _, _ := strings.Cut(err.Error(), "\nstderr:")
	return clip(displaytext.Sanitize(msg), maxErrorText)
}

func errText(err error) string {
	return clip(displaytext.Sanitize(err.Error()), maxErrorText)
}

// clip cuts s to at most n bytes on a rune boundary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
