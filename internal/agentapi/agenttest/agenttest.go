// Package agenttest provides a scriptable agentapi.Provider for tests of the
// web service. Tests drive events explicitly and decide how each Send,
// Respond, Cancel or Diff call behaves; every call is recorded so a test can
// prove what the service did (and did not) ask the provider to do.
package agenttest

import (
	"context"
	"fmt"
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

	mu        sync.Mutex
	checkErr  error
	openErr   error
	known     map[string][]agentapi.Item
	convs     []*Conversation
	opens     []agentapi.OpenRequest
	shutdowns int
	nextID    int
	opened    chan *Conversation
}

// NewProvider returns a fake provider with the given name and capabilities.
func NewProvider(name string, caps agentapi.Capabilities) *Provider {
	return &Provider{
		name: name, display: "Fake " + name, caps: caps,
		known:  map[string][]agentapi.Item{},
		opened: make(chan *Conversation, 64),
	}
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
// reopen by exact ID; History returns history.
func (p *Provider) AddConversation(id string, history []agentapi.Item) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.known[id] = append([]agentapi.Item(nil), history...)
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
	var history []agentapi.Item
	if id == "" {
		p.nextID++
		id = fmt.Sprintf("conv_%s_%d", p.name, p.nextID)
		p.known[id] = nil
	} else {
		items, ok := p.known[id]
		if !ok {
			p.mu.Unlock()
			return nil, fmt.Errorf("open %s: %w", id, agentapi.ErrConversationNotFound)
		}
		history = items
	}
	c := &Conversation{id: id, req: req, sink: req.Events, history: history, provider: p}
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
	history  []agentapi.Item
	provider *Provider

	mu          sync.Mutex
	sendHook    func(ctx context.Context, prompt string) error
	respondHook func(ctx context.Context, id string, answer agentapi.Answer) error
	cancelErr   error
	diff        []agentapi.FileDiff
	diffErr     error
	sends       []string
	cancels     int
	closes      int
	responds    []Response
	closed      bool
}

func (c *Conversation) ID() string { return c.id }

// Request returns the OpenRequest that produced the conversation.
func (c *Conversation) Request() agentapi.OpenRequest { return c.req }

func (c *Conversation) History(context.Context) ([]agentapi.Item, error) {
	return append([]agentapi.Item(nil), c.history...), nil
}

// SetSendHook decides how Send behaves: return nil to accept, an error to
// reject, or block until the test releases it.
func (c *Conversation) SetSendHook(hook func(ctx context.Context, prompt string) error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sendHook = hook
}

// SetRespondHook decides how Respond behaves.
func (c *Conversation) SetRespondHook(hook func(ctx context.Context, id string, answer agentapi.Answer) error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.respondHook = hook
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

func (c *Conversation) Send(ctx context.Context, prompt string) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return agentapi.ErrClosed
	}
	c.sends = append(c.sends, prompt)
	hook := c.sendHook
	c.mu.Unlock()
	if hook != nil {
		return hook(ctx, prompt)
	}
	return nil
}

func (c *Conversation) Cancel(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cancels++
	if c.closed {
		return agentapi.ErrClosed
	}
	return c.cancelErr
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
