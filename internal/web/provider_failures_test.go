package web

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

// scriptedProvider scripts what the agenttest fake cannot: work done while a
// conversation opens, conversations whose History, Close, SetModel or
// Commands misbehave, and a failing Shutdown.
type scriptedProvider struct {
	*agenttest.Provider
	mu sync.Mutex
	// onOpen runs once the fake opened a conversation; an error fails the open.
	onOpen      func(agentapi.OpenRequest) error
	onSetModel  func(*agenttest.Conversation)
	onCommands  func(*agenttest.Conversation)
	historyErr  error
	closeErr    error
	shutdownErr error
}

func newScripted(name string) *scriptedProvider {
	return &scriptedProvider{Provider: agenttest.NewProvider(name, allCaps)}
}

// script changes the provider's behaviour under its lock.
func (p *scriptedProvider) script(change func(*scriptedProvider)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	change(p)
}

func (p *scriptedProvider) Open(ctx context.Context, req agentapi.OpenRequest) (agentapi.Conversation, error) {
	conv, err := p.Provider.Open(ctx, req)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	onOpen := p.onOpen
	p.mu.Unlock()
	if onOpen != nil {
		if err := onOpen(req); err != nil {
			return nil, err
		}
	}
	return &scriptedConversation{Conversation: conv.(*agenttest.Conversation), p: p}, nil
}

func (p *scriptedProvider) Shutdown(ctx context.Context) error {
	_ = p.Provider.Shutdown(ctx)
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.shutdownErr
}

type scriptedConversation struct {
	*agenttest.Conversation
	p *scriptedProvider
}

func (c *scriptedConversation) History(ctx context.Context) (agentapi.History, error) {
	c.p.mu.Lock()
	err := c.p.historyErr
	c.p.mu.Unlock()
	if err != nil {
		return agentapi.History{}, err
	}
	return c.Conversation.History(ctx)
}

func (c *scriptedConversation) Close(ctx context.Context) error {
	_ = c.Conversation.Close(ctx)
	c.p.mu.Lock()
	defer c.p.mu.Unlock()
	return c.p.closeErr
}

func (c *scriptedConversation) SetModel(ctx context.Context, model, effort, size string) error {
	err := c.Conversation.SetModel(ctx, model, effort, size)
	c.p.mu.Lock()
	hook := c.p.onSetModel
	c.p.mu.Unlock()
	if hook != nil {
		hook(c.Conversation)
	}
	return err
}

func (c *scriptedConversation) Commands(ctx context.Context) ([]agentapi.Command, error) {
	c.p.mu.Lock()
	hook := c.p.onCommands
	c.p.mu.Unlock()
	if hook != nil {
		hook(c.Conversation)
	}
	return c.Conversation.Commands(ctx)
}

// emitWorking reports a running turn on the conversation being opened.
func emitWorking(req agentapi.OpenRequest) {
	req.Events.Emit(agentapi.Event{Kind: agentapi.EventTurn, Turn: &agentapi.Turn{State: agentapi.TurnWorking}})
}

func within(t *testing.T, what string, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// A provider whose check passes but whose catalog cannot be read is available
// with its default model only; the catalog is read again once it is stale.
func TestCatalogFailureAtStartKeepsTheProviderAvailable(t *testing.T) {
	prov := agenttest.NewProvider("fake", allCaps)
	prov.SetModels(nil, errors.New("catalog service down"))
	m := startManager(t, openTestStore(t), prov)
	if infos := m.Providers(); len(infos) != 1 || !infos[0].Available || infos[0].Reason != "" || len(infos[0].Models) != 0 {
		t.Fatalf("providers = %+v", infos)
	}
	prov.SetModels([]agentapi.Model{{ID: "a", Name: "A"}}, nil)
	setNow(m, time.Now().Add(modelsMaxAge))
	m.RefreshModels()
	if ids := modelIDs(m, "fake"); !slices.Equal(ids, []string{"a"}) || prov.ModelsCalls() != 2 {
		t.Fatalf("models after refresh = %v, reads %d", ids, prov.ModelsCalls())
	}
}

// A provider unavailable at start is checked again when a Task is created,
// so signing in to it needs no restart.
func TestUnavailableProviderIsCheckedAgainWhenATaskIsCreated(t *testing.T) {
	prov := agenttest.NewProvider("fake", allCaps)
	prov.SetCheckError(errors.New("not signed in"))
	m := startManager(t, openTestStore(t), prov)
	project := addProject(t, m, t.TempDir())
	if info := m.Providers()[0]; info.Available || info.Reason != "not signed in" {
		t.Fatalf("provider at start = %+v", info)
	}
	if _, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project}); statusOf(err) != http.StatusConflict || err.Error() != "Fake fake is unavailable: not signed in" {
		t.Fatalf("create while unavailable = %v", err)
	}
	prov.SetCheckError(nil)
	if _, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project}); err != nil {
		t.Fatalf("create after signing in = %v", err)
	}
	if info := m.Providers()[0]; !info.Available || info.Reason != "" || len(prov.Opens()) != 1 {
		t.Fatalf("provider after signing in = %+v, opens %d", info, len(prov.Opens()))
	}
}

// catalogProvider offers custom models; its catalog is whatever the fake's
// Models returns.
type catalogProvider struct{ *agenttest.Provider }

func (catalogProvider) SetCustomModels([]agentapi.CustomModel) {}

// A catalog that fails to reload after a custom model change keeps the old
// models and is read again on the next refresh.
func TestFailedCustomCatalogReloadIsRetriedOnTheNextRefresh(t *testing.T) {
	prov := catalogProvider{agenttest.NewProvider("fake", allCaps)}
	prov.SetModels([]agentapi.Model{{ID: "own"}}, nil)
	m := startManager(t, openTestStore(t), prov)
	prov.SetModels(nil, errors.New("catalog service down"))
	if _, err := m.UpdateSettings(SettingsPatch{CustomModels: customModels(acme)}); err != nil {
		t.Fatal(err)
	}
	if ids := modelIDs(m, "fake"); !slices.Equal(ids, []string{"own"}) {
		t.Fatalf("models after a failed reload = %v", ids)
	}
	prov.SetModels([]agentapi.Model{{ID: "own"}, {ID: "acme/coder"}}, nil)
	reads := prov.ModelsCalls()
	m.RefreshModels()
	if ids := modelIDs(m, "fake"); prov.ModelsCalls() != reads+1 || !slices.Equal(ids, []string{"own", "acme/coder"}) {
		t.Fatalf("models after refresh = %v, reads %d -> %d", ids, reads, prov.ModelsCalls())
	}
}

// A reopened conversation whose record cannot be read still takes the
// prompt; the transcript says why it is missing.
func TestReopenWithAnUnreadableRecordStillSends(t *testing.T) {
	st := openTestStore(t)
	id := mustUUID(t)
	seedWebRecord(t, st, id, "conv_old", StateCompleted)
	prov := newScripted("fake")
	prov.AddConversation("conv_old", []agentapi.Item{{ID: "a1", Kind: agentapi.ItemAssistant, Text: "earlier"}})
	prov.script(func(p *scriptedProvider) { p.historyErr = errors.New("record unreadable") })
	m := startManager(t, st, prov)
	if sub, err := m.Submit(id, PromptRequest{Text: "next", RequestID: mustUUID(t)}); err != nil || sub.Status != SubmissionAccepted {
		t.Fatalf("prompt = %+v, %v", sub, err)
	}
	d := detail(t, m, id)
	if !d.Open || d.History != HistoryUnavailable || d.HistoryReason != "could not read the recorded transcript: record unreadable" {
		t.Fatalf("detail = open %v, history %q %q", d.Open, d.History, d.HistoryReason)
	}
	if sends := prov.Last().Sends(); !slices.Equal(sends, []string{"next"}) {
		t.Fatalf("sends = %v", sends)
	}
}

// Closing a Task completes even when the provider fails to close the
// conversation.
func TestCloseCompletesWhenTheProviderFailsToClose(t *testing.T) {
	prov := newScripted("fake")
	m := startManager(t, openTestStore(t), prov)
	sum, conv := createSession(t, m, prov.Provider)
	prov.script(func(p *scriptedProvider) { p.closeErr = errors.New("runtime did not answer") })
	closed, err := m.Close(sum.ID)
	if err != nil || closed.Open || closed.State != StateClosed || conv.Closes() != 1 {
		t.Fatalf("close = %+v, %v; provider closes %d", closed, err, conv.Closes())
	}
}

// Shutdown asks every provider to stop and reports the first that failed.
func TestShutdownReportsAProviderThatFailedToStop(t *testing.T) {
	failing, other := newScripted("failing"), agenttest.NewProvider("other", allCaps)
	failing.script(func(p *scriptedProvider) { p.shutdownErr = errors.New("runtime still running") })
	m := startManager(t, openTestStore(t), failing, other)
	err := m.Shutdown(context.Background())
	if err == nil || err.Error() != "shut down failing: runtime still running" {
		t.Fatalf("shutdown = %v", err)
	}
	if failing.ShutdownCalls() != 1 || other.ShutdownCalls() != 1 {
		t.Fatalf("provider shutdowns = %d, %d", failing.ShutdownCalls(), other.ShutdownCalls())
	}
}

// A new conversation that already runs a turn keeps its Task, but the
// initial prompt is not sent into that turn.
func TestCreateKeepsTheTaskWhenItsConversationIsAlreadyBusy(t *testing.T) {
	prov := newScripted("fake")
	prov.script(func(p *scriptedProvider) {
		p.onOpen = func(req agentapi.OpenRequest) error { emitWorking(req); return nil }
	})
	m := startManager(t, openTestStore(t), prov)
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: addProject(t, m, t.TempDir()), Prompt: "start"})
	if err != nil || sum.State != StateWorking {
		t.Fatalf("create = %+v, %v", sum, err)
	}
	if d := detail(t, m, sum.ID); d.LastSubmission != nil || len(prov.Last().Sends()) != 0 {
		t.Fatalf("initial prompt sent into a running turn: last %+v, sends %v", d.LastSubmission, prov.Last().Sends())
	}
}

// A Task whose conversation opens while the service stops is refused and its
// conversation closed.
func TestCreateDuringShutdownClosesTheNewConversation(t *testing.T) {
	prov := newScripted("fake")
	opened, release := make(chan struct{}), make(chan struct{})
	prov.script(func(p *scriptedProvider) {
		p.onOpen = func(agentapi.OpenRequest) error { close(opened); <-release; return nil }
	})
	m := startManager(t, openTestStore(t), prov)
	project := addProject(t, m, t.TempDir())
	created := make(chan error, 1)
	go func() {
		_, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project})
		created <- err
	}()
	within(t, "the conversation to open", opened)
	stopped := make(chan error, 1)
	go func() { stopped <- m.Shutdown(context.Background()) }()
	waitUntil(t, "shutdown to begin", m.isClosed)
	close(release)
	if err := <-created; !errors.Is(err, errShuttingDown) {
		t.Fatalf("create during shutdown = %v", err)
	}
	if err := <-stopped; err != nil {
		t.Fatalf("shutdown = %v", err)
	}
	if conv := prov.Last(); conv.Closes() != 1 || len(m.List()) != 0 {
		t.Fatalf("closes %d, tasks %+v", conv.Closes(), m.List())
	}
}

// Reopening a conversation that still runs a turn refuses the prompt, with or
// without new model settings, and changes neither the model nor the turn.
func TestReopenThatFindsARunningTurnRefusesThePrompt(t *testing.T) {
	prov := newScripted("fake")
	prov.SetModels([]agentapi.Model{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}, nil)
	m := startManager(t, openTestStore(t), prov)
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: addProject(t, m, t.TempDir()), Model: "a"})
	if err != nil {
		t.Fatal(err)
	}
	prov.script(func(p *scriptedProvider) {
		p.onOpen = func(req agentapi.OpenRequest) error {
			if req.ConversationID != "" {
				emitWorking(req)
			}
			return nil
		}
	})
	for _, settings := range []*PromptSettings{nil, {Model: "b"}} {
		if _, err := m.Close(sum.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Submit(sum.ID, PromptRequest{Text: "next", RequestID: mustUUID(t), Settings: settings}); !errors.Is(err, errTurnRunning) {
			t.Fatalf("prompt with settings %+v = %v, want %v", settings, err, errTurnRunning)
		}
		d := detail(t, m, sum.ID)
		if !d.Open || d.State != StateWorking || d.Model != "a" || d.LastSubmission != nil || len(prov.Last().Sends()) != 0 {
			t.Fatalf("after refused prompt (settings %+v): %+v, sends %v", settings, d.SessionSummary, prov.Last().Sends())
		}
	}
	if opens := prov.Opens(); len(opens) != 3 || opens[2].ConversationID != sum.ConversationID {
		t.Fatalf("opens = %+v", opens)
	}
}

// A conversation that exits while a prompt is being prepared rejects the
// prompt: nothing is sent to the dead conversation.
func TestConversationExitingBeforeThePromptIsSentRejectsIt(t *testing.T) {
	prov := newScripted("fake")
	prov.SetModels([]agentapi.Model{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}, nil)
	prov.SetCommands([]agentapi.Command{{Name: "review"}}, nil)
	m := startManager(t, openTestStore(t), prov)
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: addProject(t, m, t.TempDir()), Model: "a"})
	if err != nil {
		t.Fatal(err)
	}
	first := prov.Last()

	// It exits while switching to the prompt's model.
	prov.script(func(p *scriptedProvider) {
		p.onSetModel = func(c *agenttest.Conversation) { c.Exit("crashed while switching") }
	})
	sub, err := m.Submit(sum.ID, PromptRequest{Text: "with b", RequestID: mustUUID(t), Settings: &PromptSettings{Model: "b"}})
	if err != nil || sub.Status != SubmissionRejected || sub.Error != "the provider conversation is not open" || len(first.Sends()) != 0 {
		t.Fatalf("prompt = %+v, %v; sends %v", sub, err, first.Sends())
	}

	// It exits, once reopened, while the command is looked up.
	prov.script(func(p *scriptedProvider) {
		p.onSetModel = nil
		p.onCommands = func(c *agenttest.Conversation) { c.Exit("crashed while listing") }
	})
	sub, err = m.Command(sum.ID, CommandRequest{RequestID: mustUUID(t), Name: "review"})
	reopened := prov.Last()
	if err != nil || sub.Status != SubmissionRejected || sub.Error != "the provider conversation is not open" || reopened == first || len(reopened.CommandRuns()) != 0 {
		t.Fatalf("command = %+v, %v; runs %v", sub, err, reopened.CommandRuns())
	}
	if d := detail(t, m, sum.ID); d.Open || d.State != StateFailed {
		t.Fatalf("task after the exit = %+v", d.SessionSummary)
	}
}

// Listing commands reports what the provider lists: nothing when it has no
// commands, a 502 when listing fails, and during a turn the commands that
// must wait for it as disabled.
func TestCommandListFollowsTheProvider(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	ctx := context.Background()
	prov.SetCommands(nil, agentapi.ErrUnsupported)
	if list, err := m.Commands(ctx, sum.ID); err != nil || list == nil || len(list) != 0 {
		t.Fatalf("commands of a provider without any = %v, %v", list, err)
	}
	prov.SetCommands(nil, errors.New("rpc down"))
	if _, err := m.Commands(ctx, sum.ID); statusOf(err) != http.StatusBadGateway || err.Error() != "could not list the provider's commands: rpc down" {
		t.Fatalf("failed listing = %v", err)
	}
	prov.SetCommands([]agentapi.Command{{Name: "review"}, {Name: "model", AllowDuringTurn: true}, {Name: "plan", DisabledReason: "Plan mode unavailable"}}, nil)
	mustSubmit(t, m, sum.ID, "work", mustUUID(t), ModeSend, SubmissionAccepted)
	conv.EmitTurn(agentapi.TurnWorking, "")
	list, err := m.Commands(ctx, sum.ID)
	reasons := map[string]string{}
	for _, c := range list {
		reasons[c.Name] = c.DisabledReason
	}
	if err != nil || len(list) != 3 || reasons["review"] != "Wait for the active turn to finish" || reasons["model"] != "" || reasons["plan"] != "Plan mode unavailable" {
		t.Fatalf("commands during a turn = %+v, %v", list, err)
	}
}

// A command is refused, and the Task left as it was, when the provider has
// no commands, cannot list them, or cannot run one.
func TestCommandRefusedWhenTheProviderCannotRunIt(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	for _, tc := range []struct {
		name     string
		commands []agentapi.Command
		listErr  error
		runErr   error
		status   int
		message  string
	}{
		{name: "no commands", listErr: agentapi.ErrUnsupported, status: http.StatusConflict, message: "this provider has no commands"},
		{name: "listing fails", listErr: errors.New("rpc down"), status: http.StatusBadGateway, message: "could not list the provider's commands: rpc down"},
		{name: "running unsupported", commands: []agentapi.Command{{Name: "review"}}, runErr: agentapi.ErrUnsupported, status: http.StatusConflict, message: "this provider has no commands"},
	} {
		prov.SetCommands(tc.commands, tc.listErr)
		conv.SetSendHook(func(context.Context, string) error { return tc.runErr })
		_, err := m.Command(sum.ID, CommandRequest{RequestID: mustUUID(t), Name: "review"})
		if statusOf(err) != tc.status || err.Error() != tc.message {
			t.Fatalf("%s: command = %v, want %d %q", tc.name, err, tc.status, tc.message)
		}
		if d := detail(t, m, sum.ID); d.State != StateIdle || d.LastSubmission != nil {
			t.Fatalf("%s: task after refusal = %+v, last %+v", tc.name, d.SessionSummary, d.LastSubmission)
		}
	}
	if runs := conv.CommandRuns(); len(runs) != 1 || runs[0].Name != "review" {
		t.Fatalf("command runs = %+v", runs)
	}
}

// A follow-up to a subagent whose conversation the provider closed is
// refused and not recorded, so a retry asks the provider again.
func TestSubagentFollowUpToAClosedConversationIsRefused(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := idleSubagent(t, m, prov)
	conv.SetPromptSubagentHook(func(context.Context, string, string) error { return agentapi.ErrClosed })
	rid := mustUUID(t)
	for range 2 {
		if _, err := m.PromptSubagent(sum.ID, "target", "more", rid); statusOf(err) != http.StatusConflict || err.Error() != "the provider conversation is closed" {
			t.Fatalf("follow-up = %v", err)
		}
	}
	if prompts := conv.SubagentPrompts(); len(prompts) != 2 {
		t.Fatalf("provider follow-ups = %+v", prompts)
	}
}

// A permission request raised by a runtime that then failed to open waits
// with no conversation to take it: a steer is rejected, and answering
// expires the request instead.
func TestRequestWithoutAnOpenConversationCannotBeSteeredOrAnswered(t *testing.T) {
	prov := newScripted("fake")
	m := startManager(t, openTestStore(t), prov)
	sum, _ := createSession(t, m, prov.Provider)
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	ix := permissionRequest("p1")
	prov.script(func(p *scriptedProvider) {
		p.onOpen = func(req agentapi.OpenRequest) error {
			req.Events.Emit(agentapi.Event{Kind: agentapi.EventInteraction, Interaction: &ix})
			return errors.New("runtime crashed")
		}
	})
	if sub, err := m.Submit(sum.ID, PromptRequest{Text: "next", RequestID: mustUUID(t)}); err != nil || sub.Status != SubmissionRejected {
		t.Fatalf("prompt = %+v, %v", sub, err)
	}
	if d := detail(t, m, sum.ID); d.Open || d.Pending != 1 || d.State != StateAwaitingPermission {
		t.Fatalf("task after the failed open = %+v", d.SessionSummary)
	}
	steer, err := m.Submit(sum.ID, PromptRequest{Text: "also", RequestID: mustUUID(t), Mode: ModeSteer})
	if err != nil || steer.Status != SubmissionRejected || steer.Error != "the provider conversation is not open" {
		t.Fatalf("steer = %+v, %v", steer, err)
	}
	if _, err := m.Answer(sum.ID, "p1", agentapi.Answer{Decision: "allow"}); statusOf(err) != http.StatusGone || err.Error() != "the interaction expired" {
		t.Fatalf("answer = %v", err)
	}
	if got := interactionOf(t, m, sum.ID, "p1"); got.State != agentapi.InteractionExpired || mustSummary(t, m, sum.ID).Pending != 0 {
		t.Fatalf("interaction after answer = %+v", got)
	}
	for _, conv := range prov.Conversations() {
		if len(conv.Responds()) != 0 || len(conv.Steers()) != 0 {
			t.Fatalf("reached the provider: answers %+v, steers %v", conv.Responds(), conv.Steers())
		}
	}
}
