package copilot

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
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
	CreateSession(ctx context.Context, cfg *copilot.SessionConfig) (sdkSession, error)
	ResumeSession(ctx context.Context, id string, cfg *copilot.ResumeSessionConfig) (sdkSession, error)
}

// sdkSession is the part of a Copilot SDK session the web provider drives.
type sdkSession interface {
	ID() string
	// Send submits prompt with the given delivery mode and returns the
	// message ID the CLI assigned to it.
	Send(ctx context.Context, prompt, mode string) (string, error)
	SwitchModel(ctx context.Context, req *rpc.ModelSwitchToRequest) (*rpc.ModelSwitchToResult, error)
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

func (a sdkClientAdapter) CreateSession(ctx context.Context, cfg *copilot.SessionConfig) (sdkSession, error) {
	s, err := a.c.CreateSession(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return sdkSessionAdapter{s}, nil
}

func (a sdkClientAdapter) ResumeSession(ctx context.Context, id string, cfg *copilot.ResumeSessionConfig) (sdkSession, error) {
	s, err := a.c.ResumeSession(ctx, id, cfg)
	if err != nil {
		return nil, err
	}
	return sdkSessionAdapter{s}, nil
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

func (a sdkSessionAdapter) SetEffort(ctx context.Context, effort string) error {
	_, err := a.s.RPC.Model.SetReasoningEffort(ctx, &rpc.ModelSetReasoningEffortRequest{ReasoningEffort: effort})
	return err
}

func (a sdkSessionAdapter) Send(ctx context.Context, prompt, mode string) (string, error) {
	id, err := a.s.Send(ctx, copilot.MessageOptions{Prompt: prompt, Mode: mode})
	if err != nil && isRPCError(err) {
		return "", rejectedError{err}
	}
	return id, err
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

	mu     sync.Mutex
	client sdkClient
	stop   chan struct{} // closed to end the current watchdog
	convs  map[*conversation]struct{}
	shut   bool
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
	return agentapi.Capabilities{Cancel: true, Permissions: true, Questions: true, History: true, ContextSize: true}
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
		mo := agentapi.Model{ID: m.ID, Name: m.Name, Efforts: append([]string{}, m.SupportedReasoningEfforts...), ContextSizes: []agentapi.ContextSize{}}
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
	return out, nil
}

func (p *webProvider) Open(ctx context.Context, req agentapi.OpenRequest) (agentapi.Conversation, error) {
	if req.Events == nil {
		return nil, errors.New("copilot: OpenRequest.Events is required")
	}
	client, err := p.ensureStarted(ctx)
	if err != nil {
		return nil, err
	}
	c := &conversation{
		p: p, client: client, sink: req.Events, pending: map[string]*interaction{}, tr: newTranscript(), subs: newSubagentLog(),
		seen: map[string]bool{}, watch: map[string]time.Time{},
	}
	// Permission requests are answered through the pending-permission RPC
	// with the request id from the permission.requested event; the SDK
	// callback only registers this client as the one that decides.
	deferPermission := func(copilot.PermissionRequest, copilot.PermissionInvocation) (rpc.PermissionDecision, error) {
		return &rpc.PermissionDecisionNoResult{}, nil
	}
	var sess sdkSession
	if req.ConversationID == "" {
		sess, err = client.CreateSession(ctx, &copilot.SessionConfig{
			SessionID:           req.SessionID,
			WorkingDirectory:    req.Workdir,
			Model:               req.Model,
			ReasoningEffort:     req.Effort,
			ContextTier:         copilot.ContextTier(req.ContextSize),
			Streaming:           copilot.Bool(true),
			OnPermissionRequest: deferPermission,
			OnUserInputRequest:  c.askUser,
			OnEvent:             c.onEvent,
		})
	} else {
		sess, err = client.ResumeSession(ctx, req.ConversationID, &copilot.ResumeSessionConfig{
			WorkingDirectory: req.Workdir,
			Streaming:        copilot.Bool(true),
			// Explicit false: nil keeps the runtime default, false treats tool
			// calls and prompts pending at the last suspend as interrupted.
			ContinuePendingWork: copilot.Bool(false),
			OnPermissionRequest: deferPermission,
			OnUserInputRequest:  c.askUser,
			OnEvent:             c.onEvent,
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
	c.mu.Unlock()
	if !p.track(c) {
		_ = c.Close(ctx)
		return nil, agentapi.ErrClosed
	}
	return c, nil
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

	mu               sync.Mutex
	closed           bool
	pending          map[string]*interaction
	stoppedSubagents map[string]bool
	tr               *transcript
	subs             *subagentLog
	turnErr          string
	idles            int
	// turnModel is the model of the turn's latest main-agent model call.
	turnModel string
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

// Send uses the "enqueue" mode explicitly: a prompt that reaches a CLI still
// busy with a turn runs after that turn instead of joining it.
func (c *conversation) Send(ctx context.Context, prompt string) error {
	c.mu.Lock()
	closed, idles := c.closed, c.idles
	c.mu.Unlock()
	if closed {
		return agentapi.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := c.sess.Send(ctx, prompt, string(rpc.SendModeEnqueue)); err != nil {
		return c.sendError(err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// An idle seen while Send was in flight already ended this turn.
	if c.idles == idles {
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

// Steer sends prompt in the "immediate" mode: the CLI folds it into the
// running turn before its next model call, and moves a running foreground
// shell command to the background. It reports no turn transition. The
// message ID the CLI returns links the steer to its user message: that
// message is marked as a steer, and a steer a stopped or failed turn ends
// without is reported as not delivered (the CLI drops unused steers on abort).
func (c *conversation) Steer(ctx context.Context, prompt string) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return agentapi.ErrClosed
	}
	st := &steer{prompt: prompt}
	c.steers = append(c.steers, st)
	c.steering++
	c.mu.Unlock()
	id, err := "", ctx.Err()
	if err == nil {
		if id, err = c.sess.Send(ctx, prompt, string(rpc.SendModeImmediate)); err != nil {
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
	if c.isClosed() {
		return agentapi.ErrClosed
	}
	if err := c.sess.Abort(ctx); err != nil {
		c.p.poke()
		return fmt.Errorf("copilot cancel: %s", errText(err))
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

// checkTasksLocked reads the task list for the watched agents on its own
// goroutine: a request made on the event goroutine stalls the connection its
// answer arrives on. One read runs at a time; asking during it adds one more.
func (c *conversation) checkTasksLocked() {
	if c.closed || c.sess == nil || len(c.watch) == 0 {
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
		ctx, cancel := context.WithTimeout(context.Background(), webTasksTimeout)
		tasks, err := sess.ListTasks(ctx)
		cancel()
		c.mu.Lock()
		switch {
		case err != nil:
			c.p.poke()
		case !c.closed:
			c.applyTasksLocked(tasks)
		}
		again := c.relist && !c.closed && len(c.watch) > 0
		c.listing, c.relist = again, false
		c.mu.Unlock()
		c.taskRPC.Unlock()
		if !again {
			return
		}
	}
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
		c.emitLocked(deltaEvent(agentID, reasoningItemID(d.ReasoningID), agentapi.ItemReasoning, d.DeltaContent))
		return
	case *rpc.AssistantUsageData:
		if agentID == "" && d.Model != "" {
			c.turnModel = d.Model
		}
		return
	case *rpc.SessionUsageInfoData:
		if agentID == "" && d.CurrentTokens >= 0 && d.TokenLimit > 0 {
			c.emitLocked(agentapi.Event{Kind: agentapi.EventContext, Context: &agentapi.Context{Used: d.CurrentTokens, Limit: d.TokenLimit}})
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
		// The only sign that a follow-up ended: it sends no subagent event.
		c.checkTasksLocked()
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
			c.emitLocked(agentapi.Event{Kind: agentapi.EventTurn, Turn: &agentapi.Turn{State: agentapi.TurnWorking}})
		}
	case *rpc.SessionIdleData:
		if agentID != "" {
			return
		}
		c.idles++
		turn := agentapi.Turn{State: agentapi.TurnCompleted, Model: c.turnModel}
		switch {
		case d.Aborted != nil && *d.Aborted:
			turn.State = agentapi.TurnCancelled
			// An abort withdraws the turn's prompts; the CLI stops waiting.
			c.expireLocked()
		case c.turnErr != "":
			turn.State, turn.Error = agentapi.TurnFailed, c.turnErr
		}
		c.turnErr, c.turnModel = "", ""
		c.undeliveredLocked(turn.State, ev.Timestamp)
		c.emitLocked(agentapi.Event{Kind: agentapi.EventTurn, Turn: &turn})
		return
	case *rpc.PermissionRequestedData:
		c.permissionRequestedLocked(d, ev.Timestamp, agentID)
		return
	case *rpc.PermissionCompletedData:
		c.permissionCompletedLocked(d)
		return
	case *rpc.SessionShutdownData:
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
	if it, ok := c.tr.item(ev); ok {
		c.emitLocked(agentapi.Event{Kind: agentapi.EventItem, Item: &it})
	}
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
		Interaction: agentapi.Interaction{ID: d.RequestID, Kind: agentapi.InteractionPermission, Title: title, Detail: clip(detail, maxToolText), State: agentapi.InteractionPending, Time: at, AgentID: agentID},
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
}

// maxEndedTools bounds transcript.ended. Forgetting older IDs only lets a
// partial result that arrives very late reopen its tool call.
const maxEndedTools = 1024

func newTranscript() *transcript {
	return &transcript{tools: map[string]*agentapi.ToolCall{}, ended: map[string]struct{}{}}
}

func (t *transcript) item(ev copilot.SessionEvent) (agentapi.Item, bool) {
	it := agentapi.Item{Time: ev.Timestamp, AgentID: agentOf(ev)}
	switch d := ev.Data.(type) {
	case *rpc.UserMessageData:
		it.ID, it.Kind, it.Text = ev.ID, agentapi.ItemUser, d.Content
		if d.MessageID != nil && *d.MessageID != "" {
			it.ID = *d.MessageID
		}
		if d.Delivery != nil && *d.Delivery == rpc.UserMessageDeliverySteering {
			it.Delivery = agentapi.DeliverySteer
		}
	case *rpc.AssistantMessageData:
		if d.Content == "" { // tool-call-only message
			return it, false
		}
		it.ID, it.Kind, it.Text = d.MessageID, agentapi.ItemAssistant, d.Content
	case *rpc.AssistantReasoningData:
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
		}
		if !d.Success {
			tc.Status = agentapi.ToolFailed
			if d.Error != nil {
				tc.Output = clip(d.Error.Message, maxToolText)
			}
		}
		it.ID, it.Kind, it.Tool = d.ToolCallID, agentapi.ItemTool, tc
	default:
		return it, false
	}
	return it, true
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
	index := map[[2]string]int{}
	for _, ev := range evs {
		if ev.Ephemeral != nil && *ev.Ephemeral {
			continue
		}
		subs.apply(ev)
		it, ok := t.item(ev)
		if !ok {
			continue
		}
		key := [2]string{it.AgentID, it.ID}
		if i, seen := index[key]; seen {
			it.Time = items[i].Time
			items[i] = it
			continue
		}
		index[key] = len(items)
		items = append(items, it)
	}
	return agentapi.History{Items: items, Subagents: subs.list()}
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
