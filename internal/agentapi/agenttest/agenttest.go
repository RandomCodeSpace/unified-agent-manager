// Package agenttest provides a scriptable agentapi.Provider for tests of the
// web service. Tests drive events explicitly and decide how the model catalog,
// the command list and each Send, RunCommand, Steer, Respond, Cancel,
// SetModel, Title, SetTitle or Diff call behave; every call is recorded so a
// test can prove what the service did (and did not) ask the provider to do.
package agenttest

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// Provider is a fake agentapi.Provider. The zero value is not usable; call
// NewProvider.
type Provider struct {
	name    string
	display string
	caps    agentapi.Capabilities

	mu          sync.Mutex
	checkErr    error
	openErr     error
	setModelErr error
	models      []agentapi.Model
	modelsErr   error
	modelsCalls int
	commands    []agentapi.Command
	commandsErr error
	quotas      []agentapi.Quota
	quotaErr    error
	quotaCalls  int
	titleHook   func(ctx context.Context, req agentapi.TitleRequest) (string, error)
	titles      []agentapi.TitleRequest
	known       map[string]agentapi.History
	convs       []*Conversation
	opens       []agentapi.OpenRequest
	shutdowns   int
	nextID      int
	opened      chan *Conversation
	readHook    func(ctx context.Context, req agentapi.ReadRequest) (agentapi.History, error)
	reads       []agentapi.ReadRequest
	previous    []agentapi.PreviousConversation
	previousErr error
	listDirs    []string
	inUse       []string
	inUseErr    error
	inUseChecks [][]string
	inUseHook   func(context.Context, []string) ([]string, error)
}

// NewProvider returns a fake provider with the given name and capabilities.
func NewProvider(name string, caps agentapi.Capabilities) *Provider {
	return &Provider{
		name: name, display: "Fake " + name, caps: caps,
		known:  map[string]agentapi.History{},
		opened: make(chan *Conversation, 64),
	}
}

// SetOpenSetModelError makes SetModel fail with err on conversations opened
// afterwards (nil restores success).
func (p *Provider) SetOpenSetModelError(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.setModelErr = err
}

// SetModels decides what Models returns.
func (p *Provider) SetModels(models []agentapi.Model, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.models, p.modelsErr = append([]agentapi.Model(nil), models...), err
}

func (p *Provider) Models(context.Context) ([]agentapi.Model, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.modelsCalls++
	return append([]agentapi.Model(nil), p.models...), p.modelsErr
}

// SetQuota decides what Quota returns. The service reads quotas only from a
// provider created with the usage capability.
func (p *Provider) SetQuota(quotas []agentapi.Quota, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.quotas, p.quotaErr = append([]agentapi.Quota(nil), quotas...), err
}

func (p *Provider) Quota(context.Context) ([]agentapi.Quota, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.quotaCalls++
	return append([]agentapi.Quota(nil), p.quotas...), p.quotaErr
}

// QuotaCalls reports how often Quota ran.
func (p *Provider) QuotaCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.quotaCalls
}

// SetTitleHook decides how Title behaves. Without a hook Title fails. The
// service asks for titles only from a provider created with the titles
// capability.
func (p *Provider) SetTitleHook(hook func(ctx context.Context, req agentapi.TitleRequest) (string, error)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.titleHook = hook
}

func (p *Provider) Title(ctx context.Context, req agentapi.TitleRequest) (string, error) {
	p.mu.Lock()
	p.titles = append(p.titles, req)
	hook := p.titleHook
	p.mu.Unlock()
	if hook == nil {
		return "", fmt.Errorf("fake %s has no title hook", p.name)
	}
	return hook(ctx, req)
}

// TitleRequests returns every Title call, oldest first.
func (p *Provider) TitleRequests() []agentapi.TitleRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]agentapi.TitleRequest(nil), p.titles...)
}

// SetCommands decides what every conversation's Commands returns.
func (p *Provider) SetCommands(commands []agentapi.Command, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.commands, p.commandsErr = append([]agentapi.Command(nil), commands...), err
}

// ModelsCalls reports how often Models ran.
func (p *Provider) ModelsCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.modelsCalls
}

func (p *Provider) Name() string                        { return p.name }
func (p *Provider) DisplayName() string                 { return p.display }
func (p *Provider) Capabilities() agentapi.Capabilities { return p.caps }

// SetCheckError makes Check fail with err (nil restores success).
func (p *Provider) SetCheckError(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.checkErr = err
}

// SetOpenError makes every Open fail with err (nil restores success).
func (p *Provider) SetOpenError(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.openErr = err
}

// AddConversation registers an existing provider conversation that Open may
// reopen by exact ID; History returns items and subagents.
func (p *Provider) AddConversation(id string, items []agentapi.Item, subagents ...agentapi.Subagent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.known[id] = agentapi.History{Items: append([]agentapi.Item(nil), items...), Subagents: append([]agentapi.Subagent(nil), subagents...)}
}

// SetHistoryUsage makes History of the registered conversation id report
// usage.
func (p *Provider) SetHistoryUsage(id string, usage agentapi.Usage) {
	p.mu.Lock()
	defer p.mu.Unlock()
	h := p.known[id]
	h.Usage = &usage
	p.known[id] = h
}

// SetHistory registers an existing provider conversation with its complete
// record, Model and Truncated included.
func (p *Provider) SetHistory(id string, h agentapi.History) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.known[id] = h
}

// ReadHistory returns the record of a known conversation, or
// agentapi.ErrConversationNotFound; SetReadHook replaces that. Every call is
// recorded.
func (p *Provider) ReadHistory(ctx context.Context, req agentapi.ReadRequest) (agentapi.History, error) {
	p.mu.Lock()
	p.reads = append(p.reads, req)
	hook := p.readHook
	h, ok := p.known[req.ConversationID]
	p.mu.Unlock()
	if hook != nil {
		return hook(ctx, req)
	}
	if !ok {
		return agentapi.History{}, fmt.Errorf("read %s: %w", req.ConversationID, agentapi.ErrConversationNotFound)
	}
	h.Items = append([]agentapi.Item(nil), h.Items...)
	h.Subagents = append([]agentapi.Subagent(nil), h.Subagents...)
	return h, nil
}

// SetReadHook decides how ReadHistory behaves: return a record or an error,
// or block until the test releases it.
func (p *Provider) SetReadHook(hook func(ctx context.Context, req agentapi.ReadRequest) (agentapi.History, error)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.readHook = hook
}

// Reads returns every ReadHistory request, oldest first.
func (p *Provider) Reads() []agentapi.ReadRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]agentapi.ReadRequest(nil), p.reads...)
}

// SetPrevious decides what Previous returns for any directory.
func (p *Provider) SetPrevious(list []agentapi.PreviousConversation, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.previous, p.previousErr = append([]agentapi.PreviousConversation(nil), list...), err
}

func (p *Provider) Previous(_ context.Context, workdir string) ([]agentapi.PreviousConversation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.listDirs = append(p.listDirs, workdir)
	out := []agentapi.PreviousConversation{}
	for _, c := range p.previous {
		if workdir == "" || c.Workdir == "" || c.Workdir == workdir {
			out = append(out, c)
		}
	}
	return out, p.previousErr
}

// PreviousDirs returns the directory of every Previous call, oldest first.
func (p *Provider) PreviousDirs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.listDirs...)
}

// SetInUse decides which conversations InUse reports as held by another
// client (those among the asked IDs), or the error it fails with.
func (p *Provider) SetInUse(ids []string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.inUse, p.inUseErr = append([]string(nil), ids...), err
}

func (p *Provider) SetInUseHook(hook func(context.Context, []string) ([]string, error)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.inUseHook = hook
}

func (p *Provider) InUse(ctx context.Context, ids []string) ([]string, error) {
	p.mu.Lock()
	p.inUseChecks = append(p.inUseChecks, append([]string(nil), ids...))
	if hook := p.inUseHook; hook != nil {
		p.mu.Unlock()
		return hook(ctx, ids)
	}
	defer p.mu.Unlock()
	if p.inUseErr != nil {
		return nil, p.inUseErr
	}
	var held []string
	for _, id := range ids {
		for _, h := range p.inUse {
			if h == id {
				held = append(held, id)
			}
		}
	}
	return held, nil
}

// InUseChecks returns the IDs of every InUse call, oldest first.
func (p *Provider) InUseChecks() [][]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][]string(nil), p.inUseChecks...)
}

// ForgetConversation removes a conversation so reopening it reports
// agentapi.ErrConversationNotFound.
func (p *Provider) ForgetConversation(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.known, id)
}

func (p *Provider) Check(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.checkErr
}

func (p *Provider) Open(_ context.Context, req agentapi.OpenRequest) (agentapi.Conversation, error) {
	p.mu.Lock()
	p.opens = append(p.opens, req)
	if p.openErr != nil {
		err := p.openErr
		p.mu.Unlock()
		return nil, err
	}
	id := req.ConversationID
	var history agentapi.History
	if id == "" {
		p.nextID++
		id = fmt.Sprintf("conv_%s_%d", p.name, p.nextID)
		p.known[id] = agentapi.History{}
	} else {
		known, ok := p.known[id]
		if !ok {
			p.mu.Unlock()
			return nil, fmt.Errorf("open %s: %w", id, agentapi.ErrConversationNotFound)
		}
		history = known
	}
	c := &Conversation{id: id, req: req, sink: req.Events, history: history, provider: p, setModelErr: p.setModelErr}
	p.convs = append(p.convs, c)
	p.mu.Unlock()
	select {
	case p.opened <- c:
	default:
	}
	return c, nil
}

func (p *Provider) Shutdown(context.Context) error {
	p.mu.Lock()
	p.shutdowns++
	convs := append([]*Conversation(nil), p.convs...)
	p.mu.Unlock()
	for _, c := range convs {
		c.markClosed()
	}
	return nil
}

// Opens returns every Open request received, oldest first.
func (p *Provider) Opens() []agentapi.OpenRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]agentapi.OpenRequest(nil), p.opens...)
}

// Conversations returns every conversation opened, oldest first.
func (p *Provider) Conversations() []*Conversation {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]*Conversation(nil), p.convs...)
}

// Last returns the most recently opened conversation, or nil.
func (p *Provider) Last() *Conversation {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.convs) == 0 {
		return nil
	}
	return p.convs[len(p.convs)-1]
}

// WaitOpened returns the next conversation opened after the call, or nil when
// timeout elapses first.
func (p *Provider) WaitOpened(timeout time.Duration) *Conversation {
	select {
	case c := <-p.opened:
		return c
	case <-time.After(timeout):
		return nil
	}
}

// ShutdownCalls reports how often Shutdown ran.
func (p *Provider) ShutdownCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.shutdowns
}

// Response is one recorded Respond call.
type Response struct {
	InteractionID string
	Answer        agentapi.Answer
}

// Conversation is a fake agentapi.Conversation.
type Conversation struct {
	id       string
	req      agentapi.OpenRequest
	sink     agentapi.EventSink
	history  agentapi.History
	provider *Provider

	mu            sync.Mutex
	sendHook      func(ctx context.Context, prompt string) error
	steerHook     func(ctx context.Context, prompt string) error
	respondHook   func(ctx context.Context, id string, answer agentapi.Answer) error
	cancelHook    func(ctx context.Context) error
	subCancelHook func(ctx context.Context, agentID string) error
	subCancels    []string
	subPromptHook func(ctx context.Context, agentID, text string) error
	subPrompts    []SubagentPrompt
	cancelErr     error
	setModelErr   error
	setTitleErr   error
	titles        []string
	diff          []agentapi.FileDiff
	diffErr       error
	sends         []string
	prompts       []agentapi.Prompt
	runs          []CommandRun
	steers        []string
	steerPrompts  []agentapi.Prompt
	modelSets     []string
	settings      []agentapi.OpenRequest
	cancels       int
	closes        int
	responds      []Response
	closed        bool
}

func (c *Conversation) ID() string { return c.id }

// Request returns the OpenRequest that produced the conversation.
func (c *Conversation) Request() agentapi.OpenRequest { return c.req }

func (c *Conversation) History(context.Context) (agentapi.History, error) {
	h := agentapi.History{
		Items:     append([]agentapi.Item(nil), c.history.Items...),
		Subagents: append([]agentapi.Subagent(nil), c.history.Subagents...),
	}
	if c.history.Usage != nil {
		usage := *c.history.Usage
		h.Usage = &usage
	}
	return h, nil
}

// SetModelError makes SetModel fail with err (nil restores success).
func (c *Conversation) SetModelError(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setModelErr = err
}

func (c *Conversation) SetModel(_ context.Context, model, effort, contextSize string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return agentapi.ErrClosed
	}
	c.modelSets = append(c.modelSets, model)
	c.settings = append(c.settings, agentapi.OpenRequest{Model: model, Effort: effort, ContextSize: contextSize})
	return c.setModelErr
}

// ModelSets returns every model passed to SetModel, oldest first.
func (c *Conversation) ModelSets() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.modelSets...)
}

// SetTitleError makes SetTitle fail with err (nil restores success).
func (c *Conversation) SetTitleError(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setTitleErr = err
}

func (c *Conversation) SetTitle(_ context.Context, title string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return agentapi.ErrClosed
	}
	c.titles = append(c.titles, title)
	return c.setTitleErr
}

// Titles returns every title passed to SetTitle, oldest first.
func (c *Conversation) Titles() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.titles...)
}

// SetSendHook decides how Send behaves: return nil to accept, an error to
// reject, or block until the test releases it.
func (c *Conversation) SetSendHook(hook func(ctx context.Context, prompt string) error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sendHook = hook
}

// SetSteerHook decides how Steer behaves, as SetSendHook does for Send.
func (c *Conversation) SetSteerHook(hook func(ctx context.Context, prompt string) error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.steerHook = hook
}

// SetRespondHook decides how Respond behaves.
func (c *Conversation) SetRespondHook(hook func(ctx context.Context, id string, answer agentapi.Answer) error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.respondHook = hook
}

// SetCancelHook decides how Cancel behaves, as SetSendHook does for Send.
func (c *Conversation) SetCancelHook(hook func(ctx context.Context) error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cancelHook = hook
}

// SetCancelSubagentHook decides how CancelSubagent behaves.
func (c *Conversation) SetCancelSubagentHook(hook func(context.Context, string) error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.subCancelHook = hook
}

// SubagentCancels returns the exact agent IDs passed to CancelSubagent.
func (c *Conversation) SubagentCancels() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.subCancels...)
}

// SubagentPrompt is one recorded PromptSubagent call.
type SubagentPrompt struct {
	AgentID string
	Text    string
}

// SetPromptSubagentHook decides how PromptSubagent behaves, as SetSendHook
// does for Send. The fake reports no subagent status on its own.
func (c *Conversation) SetPromptSubagentHook(hook func(ctx context.Context, agentID, text string) error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.subPromptHook = hook
}

// SubagentPrompts returns every PromptSubagent call, oldest first.
func (c *Conversation) SubagentPrompts() []SubagentPrompt {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]SubagentPrompt(nil), c.subPrompts...)
}

// SetCancelError makes Cancel return err.
func (c *Conversation) SetCancelError(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cancelErr = err
}

// SetDiff sets what Diff returns.
func (c *Conversation) SetDiff(files []agentapi.FileDiff, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.diff, c.diffErr = files, err
}

func (c *Conversation) Send(ctx context.Context, prompt agentapi.Prompt) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return agentapi.ErrClosed
	}
	c.sends = append(c.sends, prompt.Text)
	c.prompts = append(c.prompts, prompt)
	hook := c.sendHook
	c.mu.Unlock()
	if hook != nil {
		return hook(ctx, prompt.Text)
	}
	return nil
}

func (c *Conversation) Commands(context.Context) ([]agentapi.Command, error) {
	c.provider.mu.Lock()
	defer c.provider.mu.Unlock()
	return append([]agentapi.Command(nil), c.provider.commands...), c.provider.commandsErr
}

// CommandRun is one recorded RunCommand call.
type CommandRun struct {
	Name string
	Args agentapi.Prompt
}

// RunCommand behaves like Send: the send hook decides the outcome, called
// with "/name arguments".
func (c *Conversation) RunCommand(ctx context.Context, name string, args agentapi.Prompt) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return agentapi.ErrClosed
	}
	c.runs = append(c.runs, CommandRun{Name: name, Args: args})
	hook := c.sendHook
	c.mu.Unlock()
	if hook != nil {
		return hook(ctx, strings.TrimSpace("/"+name+" "+args.Text))
	}
	return nil
}

func (c *Conversation) Steer(ctx context.Context, prompt agentapi.Prompt) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return agentapi.ErrClosed
	}
	c.steers = append(c.steers, prompt.Text)
	c.steerPrompts = append(c.steerPrompts, prompt)
	hook := c.steerHook
	c.mu.Unlock()
	if hook != nil {
		return hook(ctx, prompt.Text)
	}
	return nil
}

func (c *Conversation) Cancel(ctx context.Context) error {
	c.mu.Lock()
	c.cancels++
	if c.closed {
		c.mu.Unlock()
		return agentapi.ErrClosed
	}
	hook, err := c.cancelHook, c.cancelErr
	c.mu.Unlock()
	if hook != nil {
		return hook(ctx)
	}
	return err
}

func (c *Conversation) CancelSubagent(ctx context.Context, agentID string) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return agentapi.ErrClosed
	}
	c.subCancels = append(c.subCancels, agentID)
	hook := c.subCancelHook
	c.mu.Unlock()
	if hook != nil {
		return hook(ctx, agentID)
	}
	return nil
}

func (c *Conversation) PromptSubagent(ctx context.Context, agentID, text string) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return agentapi.ErrClosed
	}
	c.subPrompts = append(c.subPrompts, SubagentPrompt{AgentID: agentID, Text: text})
	hook := c.subPromptHook
	c.mu.Unlock()
	if hook != nil {
		return hook(ctx, agentID, text)
	}
	return nil
}

func (c *Conversation) Respond(ctx context.Context, id string, answer agentapi.Answer) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return agentapi.ErrClosed
	}
	c.responds = append(c.responds, Response{InteractionID: id, Answer: answer})
	hook := c.respondHook
	c.mu.Unlock()
	if hook != nil {
		return hook(ctx, id, answer)
	}
	return nil
}

func (c *Conversation) Diff(context.Context) ([]agentapi.FileDiff, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.provider.caps.SessionDiff {
		return nil, agentapi.ErrUnsupported
	}
	return append([]agentapi.FileDiff(nil), c.diff...), c.diffErr
}

func (c *Conversation) Close(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closes++
	c.closed = true
	return nil
}

func (c *Conversation) markClosed() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
}

// Emit delivers ev to the service's sink exactly as an adapter would.
func (c *Conversation) Emit(ev agentapi.Event) { c.sink.Emit(ev) }

// EmitTurn reports a turn transition.
func (c *Conversation) EmitTurn(state agentapi.TurnState, errText string) {
	c.Emit(agentapi.Event{Kind: agentapi.EventTurn, Turn: &agentapi.Turn{State: state, Error: errText}})
}

// EmitItem upserts a transcript item.
func (c *Conversation) EmitItem(item agentapi.Item) {
	c.Emit(agentapi.Event{Kind: agentapi.EventItem, Item: &item})
}

// EmitDelta appends streamed text to an item.
func (c *Conversation) EmitDelta(itemID string, kind agentapi.ItemKind, text string) {
	c.Emit(agentapi.Event{Kind: agentapi.EventDelta, Delta: &agentapi.Delta{ItemID: itemID, Kind: kind, Text: text}})
}

// EmitInteraction upserts a permission request or question.
func (c *Conversation) EmitInteraction(ix agentapi.Interaction) {
	c.Emit(agentapi.Event{Kind: agentapi.EventInteraction, Interaction: &ix})
}

// EmitTitle reports a provider-generated title.
func (c *Conversation) EmitTitle(title string) {
	c.Emit(agentapi.Event{Kind: agentapi.EventTitle, Title: title})
}

// EmitSubagent upserts a subagent record.
func (c *Conversation) EmitSubagent(sa agentapi.Subagent) {
	c.Emit(agentapi.Event{Kind: agentapi.EventSubagent, Subagent: &sa})
}

// Exit simulates the provider runtime exiting: the conversation becomes
// unusable and EventExit is delivered with reason.
func (c *Conversation) Exit(reason string) {
	c.markClosed()
	c.Emit(agentapi.Event{Kind: agentapi.EventExit, Error: reason})
}

// Sends returns every prompt passed to Send, oldest first.
func (c *Conversation) Sends() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.sends...)
}

// Prompts returns every prompt passed to Send, with its references.
func (c *Conversation) Prompts() []agentapi.Prompt {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]agentapi.Prompt(nil), c.prompts...)
}

// CommandRuns returns every RunCommand call, oldest first.
func (c *Conversation) CommandRuns() []CommandRun {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]CommandRun(nil), c.runs...)
}

// Steers returns every prompt passed to Steer, oldest first.
func (c *Conversation) Steers() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.steers...)
}

// SteerPrompts returns every prompt passed to Steer, with its references.
func (c *Conversation) SteerPrompts() []agentapi.Prompt {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]agentapi.Prompt(nil), c.steerPrompts...)
}

// Cancels reports how often Cancel ran.
func (c *Conversation) Cancels() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cancels
}

// Closes reports how often Close ran.
func (c *Conversation) Closes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closes
}

// Responds returns every Respond call, oldest first.
func (c *Conversation) Responds() []Response {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Response(nil), c.responds...)
}

// ModelSettings returns the complete selections passed to SetModel.
func (c *Conversation) ModelSettings() []agentapi.OpenRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]agentapi.OpenRequest(nil), c.settings...)
}
